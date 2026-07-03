// Package rbenv provides the compiled-in `rbenv` whoosh plugin: it installs and
// wires up rbenv (+ ruby-build) on the deploy hosts and makes it available to
// every later task, before the new release goes live.
//
// Listing the plugin is enough - its startup hook contributes a task
// (rbenv:setup) and auto-wires it to run before the deploy:updated phase, so
// there is nothing to add to hooks:. On each run it, on every targeted host:
//
//  1. checks whether rbenv is installed (under RBENV_ROOT or on PATH) and, if
//     not, git-clones rbenv (+ ruby-build) into RBENV_ROOT (default $HOME/.rbenv);
//  2. registers `rbenv init` in the shell rc files (.bashrc/.zshrc/...);
//  3. ensures every desired Ruby version is installed - the union of the
//     plugin's `versions:` param and any `.ruby-version` file (read on the
//     operator at load time, and from the previous release on the host) - and,
//     when `prune: true`, uninstalls versions that are not in that set;
//  4. injects rbenv into whoosh's own env context (prepends $RBENV_ROOT/bin and
//     shims to PATH and exports RBENV_ROOT) so subsequent tasks - bundle, rails,
//     rake - resolve the rbenv Ruby without any extra config.
//
// Example:
//
//	plugins:
//	  - name: rbenv
//	    params:
//	      root: "$HOME/.rbenv"             # install dir (default $HOME/.rbenv)
//	      versions: ["3.3.0"]              # versions to ensure (unioned with .ruby-version)
//	      global: "3.3.0"                  # optional: rbenv global
//	      prune: true                      # remove Ruby versions not in the desired set
//	      shells: ["bash", "zsh"]          # shell rc files to register rbenv in
//	      roles: ["app"]                   # restrict to these roles (default: all deployable hosts)
//	      build_env:                       # env passed to ruby-build at compile time
//	        RUBY_CONFIGURE_OPTS: "--with-jemalloc --enable-yjit"
//	        MAKEOPTS: "-j 1"
//
// The host needs `git` and, to build Ruby, the usual ruby-build toolchain
// (a C compiler, openssl/readline/zlib headers). rbenv/ruby-build installs are
// idempotent, so leaving the plugin listed across deploys is cheap.
package rbenv

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yousysadmin/whoosh"
)

const (
	pluginName             = "rbenv"
	pluginVersion          = "1.0.0"
	defaultPhase           = whoosh.PhaseUpdated // default anchor for the contributed setup task's hook
	defaultTaskName        = "rbenv:setup"
	defaultRbenvRepo       = "https://github.com/rbenv/rbenv.git"
	defaultRubyBuildRepo   = "https://github.com/rbenv/ruby-build.git"
	defaultDefaultGemsRepo = "https://github.com/rbenv/rbenv-default-gems.git"
	defaultRoot            = "$HOME/.rbenv"
	defaultRubyVersion     = ".ruby-version"
)

// setupScript is the host-side shell script the contributed task runs. It is configured entirely through the
// environment (RBENV_* vars set in the task's Envs), so the script stays static and testable.
//
//go:embed templates/setup.sh
var setupScript string

func init() {
	whoosh.Register(pluginName, func() whoosh.Plugin { return &plugin{} })
}

type plugin struct{}

// rbenvPlugin is one entry of the plugins: list - an additional rbenv plugin to git-clone into
// $RBENV_ROOT/plugins/<name> on each host, optionally pinned to a version.
type rbenvPlugin struct {
	Name    string `yaml:"name"`    // plugin dir name under $RBENV_ROOT/plugins (e.g. "rbenv-vars")
	Repo    string `yaml:"repo"`    // git URL to clone
	Version string `yaml:"version"` // optional tag, branch, or commit SHA to check out
}

// Version reports the plugin's version (whoosh.Versioner), shown by `whoosh plugins` / `whoosh version`.
func (p *plugin) Version() string { return pluginVersion }

// params is the plugin's Deployfile `params:` block.
type params struct {
	// Root is the rbenv install dir. "~" and "~/..." expand to "$HOME/...". Default "$HOME/.rbenv".
	Root string `yaml:"root"`
	// RbenvRepo / RubyBuildRepo are the git sources cloned when rbenv/ruby-build are absent.
	RbenvRepo     string `yaml:"rbenv_repo"`
	RubyBuildRepo string `yaml:"ruby_build_repo"`
	// DefaultGems are gems installed into every Ruby built by rbenv, via the rbenv-default-gems plugin. Each entry is one
	// line of the default-gems file: a gem name optionally followed by a version requirement or gem options, e.g.
	// "bundler", "bcat ~>0.6", "rails --pre". When non-empty, the plugin is installed and $RBENV_ROOT/default-gems is
	// (re)written from this list; when empty, neither happens.
	DefaultGems []string `yaml:"default_gems"`
	// DefaultGemsRepo is the git source for the rbenv-default-gems plugin (default the official repo).
	DefaultGemsRepo string `yaml:"default_gems_repo"`
	// Plugins are additional rbenv plugins to git-clone into $RBENV_ROOT/plugins/<name> (e.g. rbenv-vars, rbenv-ctags).
	// ruby-build and rbenv-default-gems are handled by their own pinned options and need not be listed here.
	Plugins []rbenvPlugin `yaml:"plugins"`
	// Versions are the Ruby versions to ensure installed, unioned with any .ruby-version file.
	Versions []string `yaml:"versions"`
	// ReadRubyVersion also derives versions from .ruby-version (default true): read on the operator at load
	// (RubyVersionFile) and from the previous release on each host ($CURRENT_PATH/.ruby-version).
	ReadRubyVersion *bool `yaml:"read_ruby_version"`
	// RubyVersionFile is the operator-side .ruby-version path read at load, relative to the Deployfile dir. Default
	// ".ruby-version".
	RubyVersionFile string `yaml:"ruby_version_file"`
	// Prune uninstalls Ruby versions that are not in the desired set (default false - destructive, so opt-in).
	Prune bool `yaml:"prune"`
	// Global, when set, runs `rbenv global`; the version is always kept (never pruned).
	Global string `yaml:"global"`
	// Update git-pulls rbenv/ruby-build when they are already installed (default false).
	Update bool `yaml:"update"`
	// InstallRuby installs ruby-build and the Ruby versions (default true); false installs only rbenv.
	InstallRuby *bool `yaml:"install_ruby"`
	// Shells are the shell rc files rbenv init is registered in (default [bash, zsh]).
	Shells []string `yaml:"shells"`
	// BuildEnv is extra environment exported for the setup task, so it reaches ruby-build when it compiles Ruby - e.g.
	// RUBY_CONFIGURE_OPTS: "--with-jemalloc --enable-yjit" or MAKEOPTS: "-j 1". Values are shell-expanded, so
	// "-j $(nproc)" works. The plugin's own RBENV_* control vars always win over these.
	BuildEnv map[string]string `yaml:"build_env"`
	// Roles restricts the setup to hosts filling these roles (default: all deployable hosts).
	Roles []string `yaml:"roles"`
	// InjectPath prepends $RBENV_ROOT/bin and shims to whoosh's global PATH and exports RBENV_ROOT for every later task
	// (default true).
	InjectPath *bool `yaml:"inject_path"`
	// TaskName overrides the contributed task's name (default "rbenv:setup").
	TaskName string `yaml:"task_name"`
	// Phase / When place the setup hook: When is "before" (default) or "after", Phase is the phase to anchor to (default
	// "deploy:updated").
	Phase string `yaml:"phase"`
	When  string `yaml:"when"`
}

// Configure decodes the params and registers the startup hook.
func (p *plugin) Configure(spec whoosh.PluginSpec, reg *whoosh.Registry) error {
	var pr params
	if err := whoosh.DecodeParams(spec.Params, &pr); err != nil {
		return fmt.Errorf("rbenv params: %w", err)
	}
	reg.AddStartup(pr.startup)
	return nil
}

// startup injects rbenv into the template/env context and contributes the setup task, wiring it to run before (or
// after) the configured phase. It runs at load, before the executor reads cfg.Envs, so the PATH injection reaches every
// later task.
func (p params) startup(_ context.Context, cfg *whoosh.DeployFile) error {
	root := expandRoot(p.Root)

	// Requirement 4: make rbenv available to every subsequent task via whoosh's own env context. Env values are
	// shell-expanded when exported, so $HOME/$PATH resolve on the host.
	if boolOr(p.InjectPath, true) {
		if cfg.Envs == nil {
			cfg.Envs = map[string]string{}
		}
		cfg.Envs["RBENV_ROOT"] = root
		path := cfg.Envs["PATH"]
		if path == "" {
			path = "$PATH"
		}
		cfg.Envs["PATH"] = root + "/bin:" + root + "/shims:" + path
	}
	// Also expose it to templates as {{ .rbenv.root }}.
	cfg.AddImport("rbenv", "root", root)

	// Resolve the desired versions operator-side: explicit params, plus the app's .ruby-version (read here, at load) and
	// the global version. The host script adds the previous release's .ruby-version on top.
	versions := append([]string(nil), p.Versions...)
	if boolOr(p.ReadRubyVersion, true) {
		if v := readLocalRubyVersion(cfg.Dir, def(p.RubyVersionFile, defaultRubyVersion)); v != "" {
			versions = append(versions, v)
		}
	}
	if p.Global != "" {
		versions = append(versions, p.Global)
	}
	versions = dedupe(versions)

	shells := p.Shells
	if len(shells) == 0 {
		shells = []string{"bash", "zsh"}
	}

	extraPlugins, err := renderExtraPlugins(p.Plugins)
	if err != nil {
		return err
	}

	// Start from the user's build env (RUBY_CONFIGURE_OPTS, MAKEOPTS, ...) so ruby-build sees it at compile time, then
	// set the RBENV_* control vars on top - the plugin's own vars always win over build_env.
	env := map[string]string{}
	for k, v := range p.BuildEnv {
		env[k] = v
	}
	env["RBENV_ROOT"] = root
	env["RBENV_REPO"] = def(p.RbenvRepo, defaultRbenvRepo)
	env["RUBY_BUILD_REPO"] = def(p.RubyBuildRepo, defaultRubyBuildRepo)
	env["RBENV_DEFAULT_GEMS_REPO"] = def(p.DefaultGemsRepo, defaultDefaultGemsRepo)
	// One gem per line - entries may contain spaces ("bcat ~>0.6", "rails --pre"), so newlines separate them and the
	// script writes the value verbatim to $RBENV_ROOT/default-gems. Empty when no gems are configured (script skips it).
	env["RBENV_DEFAULT_GEMS_LIST"] = strings.Join(p.DefaultGems, "\n")
	// Extra rbenv plugins, one "<name> <git-url>" per line; the script clones each into $RBENV_ROOT/plugins/<name>.
	env["RBENV_PLUGINS"] = strings.Join(extraPlugins, "\n")
	env["RBENV_VERSIONS"] = strings.Join(versions, " ")
	env["RBENV_GLOBAL"] = p.Global
	env["RBENV_SHELLS"] = strings.Join(shells, " ")
	env["RBENV_PRUNE"] = boolStr(p.Prune)
	env["RBENV_UPDATE"] = boolStr(p.Update)
	env["RBENV_INSTALL_RUBY"] = boolStr(boolOr(p.InstallRuby, true))
	env["RBENV_READ_RUBY_VERSION"] = boolStr(boolOr(p.ReadRubyVersion, true))

	task := &whoosh.Task{
		Desc:  "Install/verify rbenv + ruby-build and ensure Ruby versions",
		Dir:   ".", // run from $HOME - the hook is movable (phase:), including to phases where the deploy dirs don't exist yet
		Roles: p.Roles,
		Envs:  env,
		Scripts: []whoosh.Script{{
			Name:   "rbenv-setup",
			Script: setupScript,
		}},
	}
	name := def(p.TaskName, defaultTaskName)
	cfg.AddTask(name, task)

	phase := def(p.Phase, defaultPhase)
	if strings.EqualFold(p.When, "after") {
		cfg.AddHookAfter(phase, name)
	} else {
		cfg.AddHookBefore(phase, name)
	}
	return nil
}

// expandRoot resolves the configured rbenv root: an empty value is the default $HOME/.rbenv, a leading "~" becomes
// "$HOME" (so the value is usable as a shell-expanded env value), any other path is kept as-is.
func expandRoot(root string) string {
	root = strings.TrimSpace(root)
	switch {
	case root == "":
		return defaultRoot
	case root == "~":
		return "$HOME"
	case strings.HasPrefix(root, "~/"):
		return "$HOME/" + root[2:]
	default:
		return root
	}
}

// readLocalRubyVersion reads a .ruby-version file on the operator (relative paths resolve against the Deployfile dir),
// returning the first token with any leading "ruby-" stripped, or "" if the file is missing/empty.
func readLocalRubyVersion(dir, file string) string {
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, file)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(data))
	if i := strings.IndexAny(v, " \t\r\n"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimPrefix(v, "ruby-")
}

// renderExtraPlugins validates the plugins: list and renders each entry as a "<name> <repo> [version]" line for the
// setup script (which clones each into $RBENV_ROOT/plugins/<name>, checked out at version when given). name and repo are
// required; all three must be single tokens since the wire format is space-delimited.
func renderExtraPlugins(plugins []rbenvPlugin) ([]string, error) {
	var lines []string
	for i, pl := range plugins {
		name := strings.TrimSpace(pl.Name)
		repo := strings.TrimSpace(pl.Repo)
		version := strings.TrimSpace(pl.Version)
		if name == "" || repo == "" {
			return nil, fmt.Errorf("rbenv: plugins[%d]: both 'name' and 'repo' are required", i)
		}
		for _, f := range [...]struct{ what, val string }{{"name", name}, {"repo", repo}, {"version", version}} {
			if strings.ContainsAny(f.val, " \t\r\n") {
				return nil, fmt.Errorf("rbenv: plugins[%d]: %s %q must not contain whitespace", i, f.what, f.val)
			}
		}
		line := name + " " + repo
		if version != "" {
			line += " " + version
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// dedupe trims, drops blanks, and removes duplicate versions, preserving first-seen order.
func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// def returns v when non-empty, otherwise fallback.
func def(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// boolOr returns *p, or def when p is nil.
func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// boolStr renders a bool as the "1"/"0" the shell script expects.
func boolStr(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
