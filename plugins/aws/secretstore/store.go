package secretstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/yousysadmin/whoosh"
	"github.com/yousysadmin/whoosh/plugins/aws/internal/dotenv"
	"github.com/yousysadmin/whoosh/plugins/aws/internal/params"
)

// Feature is the `actions:` entry name and the namespace prefix of the actions this package registers
// ("aws:secrets:<action>").
const Feature = "aws:secrets"

// actionSecretsEnvFile writes a dotenv file from Secrets Manager secrets fetched by prefix/name.
const actionSecretsEnvFile = Feature + ":to-dotenv"

// secretsAPI is the slice of the Secrets Manager client the plugin uses: list secrets by name prefix (paginated) and
// fetch a single secret's value.
type secretsAPI interface {
	ListSecrets(ctx context.Context, in *secretsmanager.ListSecretsInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error)
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// secretsPlugin backs the aws:secrets:* actions.
type secretsPlugin struct {
	api secretsAPI
	// defaults are the feature-level params (the `aws:secrets` actions: entry), layered under the to-dotenv task's `with:`
	// (the task wins). The same entry's `prefixes` also drive the startup context-injection hook below.
	defaults map[string]any
}

// Register registers the aws:secrets:to-dotenv action (always) and, when aws:secrets is listed under actions: with
// prefixes, a startup hook that loads those secrets into the template context ({{ .secrets.* }} / $SECRETS_*).
// The feature params also serve as defaults for the to-dotenv action.
// The Secrets Manager client is built from the shared AWS config.
func Register(reg *whoosh.Registry, awsCfg awssdk.Config, fp map[string]map[string]any) error {
	p := &secretsPlugin{api: secretsmanager.NewFromConfig(awsCfg), defaults: fp[Feature]}
	if err := reg.AddAction(actionSecretsEnvFile, p.runEnvironmentFile); err != nil {
		return err
	}
	if cfg, ok := fp[Feature]; ok {
		var cp secretsContextParams
		if err := whoosh.DecodeParams(cfg, &cp); err != nil {
			return err
		}
		if len(cp.Prefixes) > 0 {
			reg.AddStartup(p.startup(cp))
		}
	}
	return nil
}

// rawSecret is one fetched secret before key derivation: its name and raw value.
type rawSecret struct {
	name  string
	value string
}

// secretsEnvFileParams is the `with:` for aws:secrets:to-dotenv.
type secretsEnvFileParams struct {
	// Prefixes are the secret names/paths to read.
	// A prefix ending in "/" lists every secret whose name starts with it, one without a trailing slash is a single secret
	// name.
	Prefixes []string `yaml:"prefixes"`
	// Path is where the env file is written (on the task's hosts when an executor host writer is present, else
	// operator-side). Relative -> the release dir.
	Path string `yaml:"path"`
	// JSON controls how a secret's value becomes env vars.
	// Unset (the default) = auto-detect: a value that parses as a JSON object is expanded into one var per key, anything
	// else becomes a single var. true = require a JSON object (error otherwise). false = never parse, the whole value is
	// one var.
	JSON *bool `yaml:"json"`
	// FullKeyPath keeps the full secret name as the env key (normalized) for non-expanded (single-value) secrets, by
	// default only the last path segment is used (/app/prod/DB_URL -> DB_URL). Ignored for JSON-expanded secrets.
	FullKeyPath bool `yaml:"full_key_path"`
	// Multiline keeps real newlines inside quoted values (default true), which the dotenv/Rails gems require for values
	// like PEM keys and certs. Set false to collapse newlines to a literal \n (single-line entries) instead.
	Multiline *bool `yaml:"multiline"`
}

// secretsContextParams configures the aws:secrets startup hook (the `actions: - name: aws:secrets` entry's params) that
// loads secrets into the template context once at load, so every host's render reuses the same fetch.
type secretsContextParams struct {
	// Prefixes are the secret names/paths to load (trailing "/" = a set).
	Prefixes []string `yaml:"prefixes"`
	// Namespace is the template namespace the values land under (default "secrets"), exposed as {{ .<namespace>.<key> }}
	// and $<NAMESPACE>_<KEY>.
	Namespace string `yaml:"namespace"`
	// JSON controls JSON-object expansion (see secretsEnvFileParams.JSON).
	JSON *bool `yaml:"json"`
}

// startup returns a plugin startup hook that fetches the configured prefixes once and injects them into
// cfg.Imports[namespace].
// A JSON-object secret contributes one entry per key (keyed by the JSON key), any other secret contributes one entry
// keyed by its last name segment (so /my-app/prod/secret -> {{ .secrets.secret }} / $SECRETS_SECRET).
// Each value is registered with redact so it's masked wherever whoosh prints it.
func (s *secretsPlugin) startup(p secretsContextParams) whoosh.StartupFunc {
	namespace := p.Namespace
	if namespace == "" {
		namespace = "secrets"
	}
	return func(ctx context.Context, cfg *whoosh.DeployFile) error {
		count := 0
		for _, prefix := range p.Prefixes {
			secs, err := s.fetchPrefix(ctx, prefix)
			if err != nil {
				return fmt.Errorf("secrets %s: %w", prefix, err)
			}
			for _, sec := range secs {
				obj, expanded, err := expandSecret(sec.value, p.JSON)
				if err != nil {
					return fmt.Errorf("secrets %s: %w", sec.name, err)
				}
				if expanded {
					for k, v := range obj {
						cfg.AddImport(namespace, k, v)
						whoosh.AddSecret(v)
						count++
					}
					continue
				}
				key := sec.name[strings.LastIndex(sec.name, "/")+1:]
				cfg.AddImport(namespace, key, sec.value)
				whoosh.AddSecret(sec.value)
				count++
			}
		}
		slog.Info("loaded Secrets Manager secrets into context", "namespace", namespace, "secrets", count, "prefixes", len(p.Prefixes))
		return nil
	}
}

// runEnvironmentFile fetches the secrets under each prefix and writes them as a dotenv file at Path.
// A JSON-object secret expands into one var per key, any other secret becomes one var keyed by its (last-segment) name.
// Keys are normalized to dotenv form (UPPERCASE, non-alnum -> '_'), the file is written 0600.
func (s *secretsPlugin) runEnvironmentFile(ctx context.Context, raw map[string]any, _ io.Writer) error {
	var p secretsEnvFileParams
	if err := params.DecodeFeature(s.defaults, raw, &p); err != nil {
		return fmt.Errorf("aws:secrets:to-dotenv params: %w", err)
	}
	if len(p.Prefixes) == 0 {
		return fmt.Errorf("aws:secrets:to-dotenv: 'prefixes' is required")
	}
	if p.Path == "" {
		return fmt.Errorf("aws:secrets:to-dotenv: 'path' is required")
	}

	env := map[string]string{}
	for _, prefix := range p.Prefixes {
		secs, err := s.fetchPrefix(ctx, prefix)
		if err != nil {
			return fmt.Errorf("secrets %s: %w", prefix, err)
		}
		for _, sec := range secs {
			obj, expanded, err := expandSecret(sec.value, p.JSON)
			if err != nil {
				return fmt.Errorf("secrets %s: %w", sec.name, err)
			}
			if expanded {
				for k, v := range obj {
					env[dotenv.NormalizeKey(k)] = v
				}
				continue
			}
			key := sec.name
			if !p.FullKeyPath {
				key = key[strings.LastIndex(key, "/")+1:]
			}
			env[dotenv.NormalizeKey(key)] = sec.value
		}
	}

	content := []byte(dotenv.Render(env, params.Or(p.Multiline, true)))
	// Fetched once, operator-side.
	// Render the file on the task's hosts when the executor provided a host writer (the normal case), otherwise (e.g. a
	// unit test, or no executor) fall back to writing it on the operator machine.
	if w := whoosh.HostFileWriterFrom(ctx); w != nil {
		if err := w.WriteFile(ctx, p.Path, content); err != nil {
			return err
		}
		slog.Info("rendered env file from Secrets Manager on hosts", "path", p.Path, "vars", len(env), "prefixes", len(p.Prefixes))
		return nil
	}
	if err := os.WriteFile(p.Path, content, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", p.Path, err)
	}
	slog.Info("wrote env file from Secrets Manager", "path", p.Path, "vars", len(env), "prefixes", len(p.Prefixes))
	return nil
}

// fetchPrefix returns the secrets for a prefix.
// A trailing "/" means "every secret whose name starts with this prefix" - ListSecrets (paginated, name-prefix filter)
// then GetSecretValue for each.
// Without a trailing slash the prefix is a single secret name - GetSecretValue, a not-found single secret yields no
// entries.
func (s *secretsPlugin) fetchPrefix(ctx context.Context, prefix string) ([]rawSecret, error) {
	if strings.HasSuffix(prefix, "/") {
		var names []string
		var token *string
		for {
			resp, err := s.api.ListSecrets(ctx, &secretsmanager.ListSecretsInput{
				Filters:   []smtypes.Filter{{Key: smtypes.FilterNameStringTypeName, Values: []string{prefix}}},
				NextToken: token,
			})
			if err != nil {
				return nil, err
			}
			for _, e := range resp.SecretList {
				// The name filter is a prefix match server-side, re-check locally so the set is exactly
				// "names starting with this prefix".
				if name := awssdk.ToString(e.Name); strings.HasPrefix(name, prefix) {
					names = append(names, name)
				}
			}
			if resp.NextToken == nil || *resp.NextToken == "" {
				break
			}
			token = resp.NextToken
		}
		out := make([]rawSecret, 0, len(names))
		for _, name := range names {
			v, ok, err := s.getValue(ctx, name)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, rawSecret{name: name, value: v})
			}
		}
		return out, nil
	}
	// No trailing slash: a single secret.
	v, ok, err := s.getValue(ctx, prefix)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return []rawSecret{{name: prefix, value: v}}, nil
}

// getValue fetches one secret's value.
// A missing secret returns ok=false (skipped, not an error), mirroring the SSM single-parameter fallback.
// SecretString is preferred, a binary-only secret is returned as its raw bytes.
func (s *secretsPlugin) getValue(ctx context.Context, id string) (string, bool, error) {
	resp, err := s.api.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: awssdk.String(id)})
	if err != nil {
		if _, ok := errors.AsType[*smtypes.ResourceNotFoundException](err); ok {
			return "", false, nil
		}
		return "", false, err
	}
	if resp.SecretString != nil {
		return *resp.SecretString, true, nil
	}
	if resp.SecretBinary != nil {
		return string(resp.SecretBinary), true, nil
	}
	return "", true, nil
}

// expandSecret decides how a secret's raw value becomes env vars.
// With jsonMode false it never parses (the value is one var).
// Otherwise it tries to parse the value as a JSON object: on success it returns the expanded map (expanded=true), when
// jsonMode is nil (auto) a non-object falls back to a single var, and when jsonMode is true a non-object is an error.
func expandSecret(value string, jsonMode *bool) (map[string]string, bool, error) {
	if jsonMode != nil && !*jsonMode {
		return nil, false, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(value), &obj); err == nil && obj != nil {
		out := make(map[string]string, len(obj))
		for k, v := range obj {
			out[k] = scalarToString(v)
		}
		return out, true, nil
	}
	if jsonMode != nil && *jsonMode {
		return nil, false, fmt.Errorf("value is not a JSON object (json: true requires one)")
	}
	return nil, false, nil
}

// scalarToString renders a JSON value for a dotenv entry: strings verbatim, null as empty, and everything else
// (numbers, bools, nested structures) as compact JSON.
func scalarToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}
