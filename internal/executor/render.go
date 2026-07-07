package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/varstmpl"
)

// execEnv builds the environment exported for a task's commands and scripts: env_files (dotenv), then the global env,
// then the deploy context as standard names ($RELEASE_PATH, $HOST, ...), then the task's env (most specific wins).
// cmds and scripts share this, so $RELEASE_PATH/$HOST resolve in either.
// Config vars are template-only ({{ .var }}), surface one to the shell explicitly with `envs: { NAME: "{{ .var }}" }`.
// The user-supplied env values (global and task) are Go-templated, so they can pull from whoosh's own environment with
// {{ env "VAR" }} (e.g. a registry credential).
func (e *Executor) execEnv(host string, task *ast.Task) (map[string]string, error) {
	env := make(map[string]string, len(e.cfg.EnvFileValues)+len(e.env)+len(task.Envs)+15)
	// env_files values are a base layer that the global/task `envs` override.
	for k, v := range e.cfg.EnvFileValues {
		env[k] = v
	}
	globalEnv, err := e.globalEnv(host)
	if err != nil {
		// Dry-run previews leniently: a global env that needs run-time state must not break the plan.
		if !e.dryRun {
			return nil, err
		}
	}
	for k, v := range globalEnv {
		env[k] = v
	}
	env["DEPLOY_TO"] = e.base.DeployTo
	env["RELEASES_PATH"] = e.base.ReleasesPath
	env["SHARED_PATH"] = e.base.SharedPath
	env["REPO_PATH"] = e.base.RepoPath
	env["CURRENT_PATH"] = e.base.CurrentPath
	env["RELEASE_PATH"] = e.base.ReleasePath
	env["RELEASE_TIMESTAMP"] = e.base.ReleaseTimestamp
	env["COMMIT_HASH"] = e.base.CommitHash
	env["PREVIOUS_COMMIT_HASH"] = e.base.PreviousCommitHash
	env["APP_NAME"] = e.base.AppName
	env["BRANCH"] = e.base.Branch
	env["REPO"] = e.base.Repo
	env["STAGE"] = e.base.Stage
	env["DEPLOYER"] = e.base.Deployer
	env["HOST"] = host
	env["ROLES"] = strings.Join(e.rolesFor(host), ",")
	env["DEPLOY_PHASE"] = e.base.Phase
	env["DEPLOY_ERROR"] = e.base.DeployError
	env["DEPLOY_CHANGELOG"] = e.base.Changelog
	env["KEEP_RELEASES"] = strconv.Itoa(e.base.KeepReleases)
	// Plugin-injected values (e.g. SSM params) as $<NS>_<KEY>, e.g. ssm/secret -> $SSM_SECRET.
	// These mirror the {{ .<ns>.<key> }} template values.
	for ns, kv := range e.cfg.Imports {
		for k, v := range kv {
			env[envName(ns+"_"+k)] = v
		}
	}
	taskEnv, err := e.renderEnvMap(task.Envs, host)
	if err != nil {
		return nil, err
	}
	for k, v := range taskEnv {
		env[k] = v
	}
	return env, nil
}

// envName renders a string as a shell env var name: uppercased, with each run of non-alphanumeric characters collapsed
// to a single underscore and surrounding underscores trimmed (e.g. "ssm_db-url" -> "SSM_DB_URL").
func envName(s string) string {
	var b strings.Builder
	underscore := false
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			underscore = false
		} else if !underscore {
			b.WriteByte('_')
			underscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

// renderEnvMap Go-templates each value of a task's env map against the deploy context (host, vars, {{.config}}, and
// sprig helpers like {{ env "VAR" }} - which, going through e.render, also sees the resolved global envs). Keys are
// left as-is. It returns the input unchanged when there is nothing to render.
func (e *Executor) renderEnvMap(m map[string]string, host string) (map[string]string, error) {
	if len(m) == 0 {
		return m, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		rv, err := e.render(v, host)
		if err != nil {
			return nil, fmt.Errorf("env %q: %w", k, err)
		}
		out[k] = rv
	}
	return out, nil
}

// readScriptFile reads a script referenced by Path: absolute as-is, otherwise relative to the scripts directory.
func (e *Executor) readScriptFile(p string) ([]byte, error) {
	full := p
	if !filepath.IsAbs(p) {
		full = filepath.Join(e.scriptsDir, p)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("read script %q: %w", p, err)
	}
	return data, nil
}

func scriptLabel(sc ast.Script) string {
	switch {
	case sc.Name != "":
		return sc.Name
	case sc.Path != "":
		return filepath.Base(sc.Path)
	default:
		return "inline"
	}
}

// rolesFor returns the full role set of the host(s) matching addr (deduped), so a command can pass the host's roles -
// e.g. `whenever --update-crontab --roles=$ROLES`.
func (e *Executor) rolesFor(host string) []string {
	var roles []string
	seen := map[string]bool{}
	for _, h := range e.cfg.Hosts {
		if h.Address != host {
			continue
		}
		for _, r := range h.Roles {
			if !seen[r] {
				seen[r] = true
				roles = append(roles, r)
			}
		}
	}
	return roles
}

// baseContext is the per-host render context WITHOUT GlobalEnvValues - what global env values themselves render
// against (env lookup: process env, then env_files - no self-reference).
func (e *Executor) baseContext(host string) varstmpl.Context {
	c := e.base
	c.Host = host
	c.Roles = e.rolesFor(host)
	return c
}

// globalEnv resolves the global `envs:` for host, each value rendered against the base context. Values may reference
// run-time keys ({{.release_path}}, {{.phase}}, {{.tasks.*}}), so they are rendered fresh on every call, never cached.
func (e *Executor) globalEnv(host string) (map[string]string, error) {
	if len(e.env) == 0 {
		return nil, nil
	}
	c := e.baseContext(host)
	out := make(map[string]string, len(e.env))
	for k, v := range e.env {
		rv, err := varstmpl.RenderWith(v, c, !e.dryRun)
		if err != nil {
			return nil, fmt.Errorf("env %q: %w", k, err)
		}
		out[k] = rv
	}
	return out, nil
}

// render is the task-time render: the base context plus the resolved global envs, so {{ env "X" }} in cmds, scripts,
// task envs, and dir sees process env > global envs > env_files.
func (e *Executor) render(raw, host string) (string, error) {
	c := e.baseContext(host)
	ge, err := e.globalEnv(host)
	if err != nil {
		// Dry-run previews leniently: a global env that needs run-time state must not break the plan.
		if !e.dryRun {
			return "", err
		}
	} else {
		c.GlobalEnvValues = ge
	}
	// Dry-run renders leniently: captured task state ({{.tasks.*}}) and other run-time-only values aren't known when
	// previewing, so a missing key yields "<no value>" instead of failing the preview. Real runs stay strict.
	return varstmpl.RenderWith(raw, c, !e.dryRun)
}

// taskDir resolves the working directory for a task's commands: an explicit `dir:` wins (and is Go-templated, so a
// `deploy:init`/pre-release hook can use e.g. dir: "{{.deploy_to}}" or "{{.shared_path}}"), a local (operator-side)
// task uses the operator's cwd, otherwise remote commands default to the release directory (the in-progress release
// during a deploy, else the live `current`), so e.g. `bundle install` runs where the Gemfile is.
// A task/hook running before the release exists must set `dir:` to an existing path.
func (e *Executor) taskDir(task *ast.Task, host string) (string, error) {
	if task.Dir != "" {
		return e.render(task.Dir, host)
	}
	if task.Local {
		return "", nil
	}
	return e.base.ReleasePath, nil
}
