package ssm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/yousysadmin/whoosh"
	"github.com/yousysadmin/whoosh/plugins/aws/internal/dotenv"
	"github.com/yousysadmin/whoosh/plugins/aws/internal/params"
)

// Feature is the `actions:` entry name and the namespace prefix of the actions this package registers
// ("aws:ssm:<action>").
const Feature = "aws:ssm"

// actionSSMEnvFile writes a dotenv file from SSM parameters fetched by prefix.
const actionSSMEnvFile = Feature + ":to-dotenv"

// ssmAPI is the slice of the SSM client the plugin uses: list parameters under a path prefix (paginated), and fetch a
// single parameter (the leaf-path fallback).
type ssmAPI interface {
	GetParametersByPath(ctx context.Context, in *awsssm.GetParametersByPathInput, optFns ...func(*awsssm.Options)) (*awsssm.GetParametersByPathOutput, error)
	GetParameter(ctx context.Context, in *awsssm.GetParameterInput, optFns ...func(*awsssm.Options)) (*awsssm.GetParameterOutput, error)
}

// ssmPlugin backs the aws:ssm:* actions.
type ssmPlugin struct {
	api ssmAPI
	// defaults are the feature-level params (the `aws:ssm` actions: entry), layered under the to-dotenv task's `with:`
	// (the task wins). The same entry's `prefixes` also drive the startup context-injection hook below.
	defaults map[string]any
}

// Register registers the aws:ssm:to-dotenv action (always) and, when aws:ssm is listed under actions: with prefixes, a
// startup hook that loads those parameters into the template context ({{ .ssm.* }} / $SSM_*).
// The feature params also serve as defaults for the to-dotenv action.
// The SSM client is built from the shared AWS config.
func Register(reg *whoosh.Registry, awsCfg awssdk.Config, fp map[string]map[string]any) error {
	p := &ssmPlugin{api: awsssm.NewFromConfig(awsCfg), defaults: fp[Feature]}
	if err := reg.AddAction(actionSSMEnvFile, p.runEnvironmentFile); err != nil {
		return err
	}
	if cfg, ok := fp[Feature]; ok {
		var cp ssmContextParams
		if err := whoosh.DecodeParams(cfg, &cp); err != nil {
			return err
		}
		if len(cp.Prefixes) > 0 {
			reg.AddStartup(p.startup(cp))
		}
	}
	return nil
}

// ssmEnvFileParams is the `with:` for aws:ssm:to-dotenv.
type ssmEnvFileParams struct {
	// Prefixes are the SSM parameter paths to read.
	// Each is fetched recursively (every parameter under it); a prefix that is itself a single parameter (a leaf) is
	// fetched directly too, so "/app/prod" and "/shared/github-key" both work.
	Prefixes []string `yaml:"prefixes"`
	// Path is where the env file is written, on the machine running whoosh (actions run operator-side).
	// Relative paths resolve against its cwd.
	Path string `yaml:"path"`
	// Recursive walks the whole tree under each prefix (default true).
	Recursive *bool `yaml:"recursive"`
	// Decrypt decrypts SecureString parameters (default true).
	Decrypt *bool `yaml:"decrypt"`
	// FullKeyPath keeps the full parameter path as the env key (normalized); by default only the last path segment is used
	// (/app/prod/DB_URL -> DB_URL).
	FullKeyPath bool `yaml:"full_key_path"`
	// Multiline keeps real newlines inside quoted values (default true), which the dotenv/Rails gems require for values
	// like PEM keys and certs. Set false to collapse newlines to a literal \n (single-line entries) instead.
	Multiline *bool `yaml:"multiline"`
}

// ssmContextParams configures the aws:ssm startup hook (the `actions: - name: aws:ssm` entry's params) that loads
// parameters into the template context once at load, so every host's render reuses the same fetch.
type ssmContextParams struct {
	// Prefixes are the SSM paths to load (recursively); a leaf path works too.
	Prefixes []string `yaml:"prefixes"`
	// Namespace is the template namespace the values land under (default "ssm"), exposed as {{ .<namespace>.<key> }} and
	// $<NAMESPACE>_<KEY>.
	Namespace string `yaml:"namespace"`
	// Recursive walks the whole tree under each prefix (default true).
	Recursive *bool `yaml:"recursive"`
	// Decrypt decrypts SecureString parameters (default true).
	Decrypt *bool `yaml:"decrypt"`
}

// startup returns a plugin startup hook that fetches the configured prefixes once and injects them into
// cfg.Imports[namespace], keyed by each parameter's last path segment (so /my-app/prod/secret -> {{ .ssm.secret }} /
// $SSM_SECRET). Each value is registered with redact so it's masked wherever whoosh prints it.
func (s *ssmPlugin) startup(p ssmContextParams) whoosh.StartupFunc {
	namespace := p.Namespace
	if namespace == "" {
		namespace = "ssm"
	}
	recursive := params.Or(p.Recursive, true)
	decrypt := params.Or(p.Decrypt, true)

	return func(ctx context.Context, cfg *whoosh.DeployFile) error {
		count := 0
		for _, prefix := range p.Prefixes {
			params, err := s.fetchPrefix(ctx, prefix, recursive, decrypt)
			if err != nil {
				return fmt.Errorf("ssm %s: %w", prefix, err)
			}
			for name, value := range params {
				key := name[strings.LastIndex(name, "/")+1:]
				cfg.AddImport(namespace, key, value)
				whoosh.AddSecret(value)
				count++
			}
		}
		slog.Info("loaded SSM parameters into context", "namespace", namespace, "params", count, "prefixes", len(p.Prefixes))
		return nil
	}
}

// runEnvironmentFile fetches the parameters under each prefix and writes them as a dotenv file at Path.
// Keys are normalized to dotenv form (UPPERCASE, non-alnum -> '_'); the file is written 0600 since it holds secrets.
func (s *ssmPlugin) runEnvironmentFile(ctx context.Context, raw map[string]any, _ io.Writer) error {
	var p ssmEnvFileParams
	if err := params.DecodeFeature(s.defaults, raw, &p); err != nil {
		return fmt.Errorf("aws:ssm:to-dotenv params: %w", err)
	}
	if len(p.Prefixes) == 0 {
		return fmt.Errorf("aws:ssm:to-dotenv: 'prefixes' is required")
	}
	if p.Path == "" {
		return fmt.Errorf("aws:ssm:to-dotenv: 'path' is required")
	}
	recursive := params.Or(p.Recursive, true)
	decrypt := params.Or(p.Decrypt, true)

	env := map[string]string{}
	for _, prefix := range p.Prefixes {
		params, err := s.fetchPrefix(ctx, prefix, recursive, decrypt)
		if err != nil {
			return fmt.Errorf("ssm %s: %w", prefix, err)
		}
		for name, value := range params {
			key := name
			if !p.FullKeyPath {
				key = name[strings.LastIndex(name, "/")+1:]
			}
			env[dotenv.NormalizeKey(key)] = value
		}
	}

	content := []byte(dotenv.Render(env, params.Or(p.Multiline, true)))
	// Fetched once, operator-side.
	// Render the file on the task's hosts when the executor provided a host writer (the normal case); otherwise (e.g. a
	// unit test, or no executor) fall back to writing it on the operator machine.
	if w := whoosh.HostFileWriterFrom(ctx); w != nil {
		if err := w.WriteFile(ctx, p.Path, content); err != nil {
			return err
		}
		slog.Info("rendered env file from SSM on hosts", "path", p.Path, "params", len(env), "prefixes", len(p.Prefixes))
		return nil
	}
	if err := os.WriteFile(p.Path, content, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", p.Path, err)
	}
	slog.Info("wrote env file from SSM", "path", p.Path, "params", len(env), "prefixes", len(p.Prefixes))
	return nil
}

// fetchPrefix returns name->value for a prefix.
// A trailing "/" means "everything under this path" - GetParametersByPath (recursive), the slash trimmed for the API
// call.
// Without a trailing slash the prefix is a single parameter name - GetParameter; a not-found single parameter yields no
// entries (not an error).
func (s *ssmPlugin) fetchPrefix(ctx context.Context, prefix string, recursive, decrypt bool) (map[string]string, error) {
	out := map[string]string{}
	if strings.HasSuffix(prefix, "/") {
		path := strings.TrimRight(prefix, "/")
		var token *string
		for {
			resp, err := s.api.GetParametersByPath(ctx, &awsssm.GetParametersByPathInput{
				Path:           awssdk.String(path),
				Recursive:      awssdk.Bool(recursive),
				WithDecryption: awssdk.Bool(decrypt),
				NextToken:      token,
			})
			if err != nil {
				return nil, err
			}
			for _, prm := range resp.Parameters {
				out[awssdk.ToString(prm.Name)] = awssdk.ToString(prm.Value)
			}
			if resp.NextToken == nil || *resp.NextToken == "" {
				break
			}
			token = resp.NextToken
		}
		return out, nil
	}
	// No trailing slash: a single parameter.
	resp, err := s.api.GetParameter(ctx, &awsssm.GetParameterInput{
		Name:           awssdk.String(prefix),
		WithDecryption: awssdk.Bool(decrypt),
	})
	if err != nil {
		if _, ok := errors.AsType[*ssmtypes.ParameterNotFound](err); ok {
			return out, nil // the named parameter doesn't exist
		}
		return nil, err
	}
	out[awssdk.ToString(resp.Parameter.Name)] = awssdk.ToString(resp.Parameter.Value)
	return out, nil
}
