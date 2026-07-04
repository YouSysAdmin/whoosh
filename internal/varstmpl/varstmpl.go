// Package varstmpl renders command strings and scripts from Deployfile tasks.
// Templates use Go text/template syntax with Masterminds/sprig helpers, e.g.
//
//	cd {{.release_path}} && RAILS_ENV={{.RAILS_ENV}} bin/rails db:migrate
//
// The well-known deployment keys and user vars are exposed at the top level, and the whole resolved Deployfile is available
// under {{.config}} (keyed by the YAML field names) for flexible logic, e.g.
//
//	{{range .config.hosts}}{{if has "web" .roles}}{{.address}} {{end}}{{end}}
//
// Referencing an undefined key is an error, so typos surface immediately.
//
// Sprig supplies the general-purpose helpers (toJson/fromJson, join/splitList, default/ternary, ...); whoosh
// adds toYaml/fromYaml/fromYamlArray/required (helperFuncs), the secret-marking sensitive (secretFuncs), and
// env/envSecret (envFuncs) - which read the process environment falling back to the Deployfile's env_files values.
package varstmpl

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"gopkg.in/yaml.v3"

	"github.com/yousysadmin/whoosh/internal/masking"
)

// Context carries the values available to a command template.
// User-defined vars are merged in alongside the well-known deploy keys, on a key collision the well-known key wins so
// built-ins can't be shadowed.
type Context struct {
	Vars             map[string]any
	AppName          string
	Repo             string
	Branch           string
	Stage            string
	DeployTo         string
	ReleasesPath     string
	SharedPath       string
	RepoPath         string
	CurrentPath      string
	ReleasePath      string
	ReleaseTimestamp string
	CommitHash       string
	KeepReleases     int
	Host             string
	// Roles are the roles of the host the command is running on (the matched server's full role set), exposed as
	// {{.roles}} and (comma-joined) $ROLES.
	Roles []string
	// Phase is the deploy phase a hook task is running for (e.g.
	// "deploy:publishing" or "deploy:failed"), empty for a standalone task run.
	Phase string
	// DeployError is the failure message exposed to deploy:failed hook tasks, empty otherwise.
	DeployError string
	// Tasks is run-scoped state captured from tasks declaring `output:`, keyed by task name, exposed as {{ .tasks.<name>
	// }} (a task's parsed output).
	Tasks map[string]any
	// Config is the whole resolved Deployfile as a map (YAML field names), exposed under {{.config}} for flexible logic in
	// scripts.
	Config map[string]any
	// Imports are values a plugin injected at load (e.g. SSM parameters), keyed namespace -> key -> value.
	// Each namespace is exposed as {{ .<ns>.<key> }}.
	Imports map[string]map[string]string
	// EnvFileValues are the Deployfile's env_files (dotenv) values. The env/envSecret template funcs consult them when
	// the process env var is unset (a set process var wins, even when empty - the dotenv non-override convention).
	EnvFileValues map[string]string
}

// lookupEnv resolves an env/envSecret name: the process environment first (a set-but-empty var still wins), then the
// env_files values, else "".
func (c Context) lookupEnv(name string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return c.EnvFileValues[name]
}

// Data flattens the context into the map exposed to templates.
func (c Context) Data() map[string]any {
	m := make(map[string]any, len(c.Vars)+19+len(c.Imports))
	for k, v := range c.Vars {
		m[k] = v
	}
	m["config"] = c.Config
	m["app_name"] = c.AppName
	m["repo"] = c.Repo
	m["branch"] = c.Branch
	m["stage"] = c.Stage
	m["deploy_to"] = c.DeployTo
	m["releases_path"] = c.ReleasesPath
	m["shared_path"] = c.SharedPath
	m["repo_path"] = c.RepoPath
	m["current_path"] = c.CurrentPath
	m["release_path"] = c.ReleasePath
	m["release_timestamp"] = c.ReleaseTimestamp
	m["commit_hash"] = c.CommitHash
	m["keep_releases"] = c.KeepReleases
	m["host"] = c.Host
	m["roles"] = c.Roles
	m["phase"] = c.Phase
	m["error"] = c.DeployError
	tasks := c.Tasks
	if tasks == nil {
		tasks = map[string]any{}
	}
	m["tasks"] = tasks
	// Plugin-injected namespaces, e.g. {{ .ssm.secret }}.
	// A namespace never shadows a well-known key above (those are set after the vars spread, imports are added last but
	// use their own namespace key, so a clash would only be with a same-named var/builtin - avoid naming a namespace
	// "config"/"tasks"/etc.).
	for ns, kv := range c.Imports {
		nm := make(map[string]any, len(kv))
		for k, v := range kv {
			nm[k] = v
		}
		m[ns] = nm
	}
	return m
}

// Render expands a single template string against the context with strict missing-key checking: a
// referenced-but-undefined key is an error, so typos surface immediately.
func Render(text string, c Context) (string, error) {
	return RenderWith(text, c, true)
}

// sprigFuncs is built once: sprig's FuncMap is large, process-constant, and only read by Funcs (which copies the
// entries into each template), so every render sharing one map is safe and avoids rebuilding it per command/param.
var sprigFuncs = sprig.TxtFuncMap()

// RenderWith is Render with selectable missing-key handling.
// With strict=false a missing key renders as "<no value>" instead of erroring - used for --dry-run, where run-time-only
// values (e.g. captured task state from `output:` tasks the preview doesn't execute) aren't known yet, so a preview
// shouldn't hard-fail.
func RenderWith(text string, c Context, strict bool) (string, error) {
	missingkey := "error"
	if !strict {
		missingkey = "default"
	}
	t, err := template.New("cmd").
		Funcs(sprigFuncs).
		Funcs(helperFuncs()).
		Funcs(secretFuncs()).
		Funcs(envFuncs(c)).
		Option("missingkey=" + missingkey).
		Parse(text)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", text, err)
	}
	var sb strings.Builder
	if err := t.Execute(&sb, c.Data()); err != nil {
		return "", fmt.Errorf("render template %q: %w", text, rootCause(err))
	}
	return sb.String(), nil
}

// rootCause returns the innermost error of a template execution failure - the actual cause, e.g. the error a helper
// like `required` returned. text/template wraps it in location noise (`executing "cmd" at <...>: error calling
// required: ...`) that buries the message; errors it formats itself (like a strict missing key) have no inner error
// and are returned as-is.
func rootCause(err error) error {
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			return err
		}
		err = inner
	}
}

// helperFuncs returns whoosh's own general-purpose helpers - the gaps sprig doesn't cover (its JSON funcs have no
// YAML counterparts, and missingkey=error only catches undefined keys, not defined-but-empty values). Each returns an
// error so a bad value fails the render loudly:
//
//	{{ toYaml .config.app }}                     // any value as YAML (no trailing newline)
//	{{ (fromYaml .tasks.info).version }}         // parse a YAML mapping
//	{{ range fromYamlArray .hosts_yaml }}...     // parse a YAML sequence
//	{{ required "vars.bucket must be set" .bucket }}  // fail with the message when nil/empty
func helperFuncs() template.FuncMap {
	return template.FuncMap{
		"toYaml": func(v any) (string, error) {
			b, err := yaml.Marshal(v)
			if err != nil {
				return "", fmt.Errorf("toYaml: %w", err)
			}
			return strings.TrimSuffix(string(b), "\n"), nil
		},
		"fromYaml": func(s string) (map[string]any, error) {
			var m map[string]any
			if err := yaml.Unmarshal([]byte(s), &m); err != nil {
				return nil, fmt.Errorf("fromYaml: %w", err)
			}
			return m, nil
		},
		"fromYamlArray": func(s string) ([]any, error) {
			var a []any
			if err := yaml.Unmarshal([]byte(s), &a); err != nil {
				return nil, fmt.Errorf("fromYamlArray: %w", err)
			}
			return a, nil
		},
		"required": func(msg string, v any) (any, error) {
			if v == nil {
				return nil, fmt.Errorf("required: %s", msg)
			}
			if s, ok := v.(string); ok && s == "" {
				return nil, fmt.Errorf("required: %s", msg)
			}
			return v, nil
		},
	}
}

// secretFuncs returns template helpers that mark a value sensitive: the value is returned for use in the command but
// also registered with redact, so it is masked everywhere whoosh prints it (command echo, output, dry-run plans, logs)
// - even when no built-in pattern would recognize it.
//
//	{{ sensitive .db_password }}  // mark any value (var, expression) sensitive
//
// (envSecret, the env-reading counterpart, lives in envFuncs since it needs the context's env_files values.)
func secretFuncs() template.FuncMap {
	return template.FuncMap{
		"sensitive": func(v any) string {
			s := fmt.Sprint(v)
			masking.AddSecret(s)
			return s
		},
	}
}

// envFuncs returns the environment-reading helpers, bound to c so they see the Deployfile's env_files values (process
// env wins; see Context.lookupEnv). "env" deliberately overrides sprig's os.Getenv alias - chained after sprigFuncs,
// last registration wins - and the tiny per-render map keeps the shared sprigFuncs cache intact.
//
//	{{ env "APP_VERSION" }}       // process env, else env_files
//	{{ envSecret "REG_TOKEN" }}   // like env, but the value is always redacted
//
// (Template function names can't contain '-', so it's envSecret, not env-sens.)
func envFuncs(c Context) template.FuncMap {
	return template.FuncMap{
		"env": c.lookupEnv,
		"envSecret": func(name string) string {
			v := c.lookupEnv(name)
			masking.AddSecret(v)
			return v
		},
	}
}

// RenderParams deep-renders the string values in a params map (e.g. an action task's `with:` or a plugin's `params:`)
// as Go templates against c. Numbers, booleans, and nesting (maps and lists) are preserved, only strings are rendered.
// The input is never mutated (a non-empty input yields a new map, an empty/nil one is returned as-is).
func RenderParams(params map[string]any, c Context, strict bool) (map[string]any, error) {
	if len(params) == 0 {
		return params, nil
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		rv, err := renderParamValue(v, c, strict)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = rv
	}
	return out, nil
}

func renderParamValue(v any, c Context, strict bool) (any, error) {
	switch t := v.(type) {
	case string:
		return RenderWith(t, c, strict)
	case map[string]any:
		return RenderParams(t, c, strict)
	case []any:
		out := make([]any, len(t))
		for i, el := range t {
			rv, err := renderParamValue(el, c, strict)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	default:
		return v, nil
	}
}
