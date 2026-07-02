package rbenv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh"
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

// load configures the rbenv plugin with params and runs its startup hook against cfg, returning the mutated config.
func load(t *testing.T, cfg *ast.DeployFile, params map[string]any) {
	t.Helper()
	reg, err := whoosh.Load([]whoosh.PluginSpec{{Name: pluginName, Params: params}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := reg.RunStartup(context.Background(), cfg); err != nil {
		t.Fatalf("startup: %v", err)
	}
}

func TestStartup_ContributesTaskAndHook(t *testing.T) {
	cfg := &ast.DeployFile{}
	load(t, cfg, nil)

	task, ok := cfg.Tasks[defaultTaskName]
	if !ok {
		t.Fatalf("task %q not registered; have %v", defaultTaskName, keys(cfg.Tasks))
	}
	if len(task.Scripts) != 1 || !strings.Contains(task.Scripts[0].Script, "rbenv") {
		t.Fatalf("setup task should carry the embedded rbenv script")
	}
	if task.Dir != "." {
		t.Errorf("task Dir = %q, want %q (deploy dirs may not exist at before:starting)", task.Dir, ".")
	}
	if got := cfg.Hooks.Before[defaultPhase]; len(got) != 1 || got[0] != defaultTaskName {
		t.Errorf("before[%s] = %v, want [%s]", defaultPhase, got, defaultTaskName)
	}
}

func TestStartup_InjectsPath(t *testing.T) {
	cfg := &ast.DeployFile{}
	load(t, cfg, nil)

	if cfg.Envs["RBENV_ROOT"] != defaultRoot {
		t.Errorf("RBENV_ROOT = %q, want %q", cfg.Envs["RBENV_ROOT"], defaultRoot)
	}
	wantPath := defaultRoot + "/bin:" + defaultRoot + "/shims:$PATH"
	if cfg.Envs["PATH"] != wantPath {
		t.Errorf("PATH = %q, want %q", cfg.Envs["PATH"], wantPath)
	}
	if cfg.Imports["rbenv"]["root"] != defaultRoot {
		t.Errorf("import rbenv.root = %q, want %q", cfg.Imports["rbenv"]["root"], defaultRoot)
	}
}

func TestStartup_PrependsExistingPath(t *testing.T) {
	cfg := &ast.DeployFile{Envs: map[string]string{"PATH": "/opt/bin:$PATH"}}
	load(t, cfg, nil)

	want := defaultRoot + "/bin:" + defaultRoot + "/shims:/opt/bin:$PATH"
	if cfg.Envs["PATH"] != want {
		t.Errorf("PATH = %q, want %q", cfg.Envs["PATH"], want)
	}
}

func TestStartup_InjectPathDisabled(t *testing.T) {
	cfg := &ast.DeployFile{}
	load(t, cfg, map[string]any{"inject_path": false})

	if _, ok := cfg.Envs["PATH"]; ok {
		t.Errorf("PATH should not be injected when inject_path: false, got %q", cfg.Envs["PATH"])
	}
	// The task still needs RBENV_ROOT itself.
	if cfg.Tasks[defaultTaskName].Envs["RBENV_ROOT"] != defaultRoot {
		t.Errorf("task RBENV_ROOT = %q, want %q", cfg.Tasks[defaultTaskName].Envs["RBENV_ROOT"], defaultRoot)
	}
}

func TestStartup_VersionsUnionAndFlags(t *testing.T) {
	dir := t.TempDir()
	// operator-side .ruby-version (with a "ruby-" prefix to exercise stripping)
	if err := os.WriteFile(filepath.Join(dir, ".ruby-version"), []byte("ruby-3.2.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &ast.DeployFile{Dir: dir}
	load(t, cfg, map[string]any{
		"versions": []string{"3.3.0", "3.2.2"}, // 3.2.2 also comes from the file → deduped
		"global":   "3.3.0",
		"prune":    true,
		"update":   true,
		"roles":    []string{"app"},
		"shells":   []string{"bash", "zsh", "fish"},
	})

	env := cfg.Tasks[defaultTaskName].Envs
	got := strings.Fields(env["RBENV_VERSIONS"])
	if !equalSet(got, []string{"3.3.0", "3.2.2"}) {
		t.Errorf("RBENV_VERSIONS = %q, want the set {3.3.0, 3.2.2}", env["RBENV_VERSIONS"])
	}
	assertEnv(t, env, "RBENV_PRUNE", "1")
	assertEnv(t, env, "RBENV_UPDATE", "1")
	assertEnv(t, env, "RBENV_GLOBAL", "3.3.0")
	assertEnv(t, env, "RBENV_SHELLS", "bash zsh fish")
	assertEnv(t, env, "RBENV_INSTALL_RUBY", "1")

	if roles := cfg.Tasks[defaultTaskName].Roles; len(roles) != 1 || roles[0] != "app" {
		t.Errorf("task roles = %v, want [app]", roles)
	}
}

func TestStartup_BuildEnv(t *testing.T) {
	cfg := &ast.DeployFile{}
	load(t, cfg, map[string]any{
		"build_env": map[string]any{
			"RUBY_CONFIGURE_OPTS": "--with-jemalloc --enable-yjit",
			"MAKEOPTS":            "-j 1",
			// A collision with a control var must NOT override the plugin's own value.
			"RBENV_VERSIONS": "hijacked",
		},
		"versions": []string{"3.3.0"},
	})

	env := cfg.Tasks[defaultTaskName].Envs
	assertEnv(t, env, "RUBY_CONFIGURE_OPTS", "--with-jemalloc --enable-yjit")
	assertEnv(t, env, "MAKEOPTS", "-j 1")
	assertEnv(t, env, "RBENV_VERSIONS", "3.3.0") // control var wins over build_env
}

func TestStartup_DefaultGems(t *testing.T) {
	cfg := &ast.DeployFile{}
	load(t, cfg, map[string]any{
		"default_gems": []string{"bundler", "bcat ~>0.6", "rails --pre"},
	})
	env := cfg.Tasks[defaultTaskName].Envs
	// Newline-separated so entries keep their internal spaces; written verbatim to $RBENV_ROOT/default-gems.
	assertEnv(t, env, "RBENV_DEFAULT_GEMS_LIST", "bundler\nbcat ~>0.6\nrails --pre")
	assertEnv(t, env, "RBENV_DEFAULT_GEMS_REPO", defaultDefaultGemsRepo)
}

func TestStartup_DefaultGemsEmptyAndCustomRepo(t *testing.T) {
	// No default_gems -> empty list (script skips the plugin + file).
	cfg := &ast.DeployFile{}
	load(t, cfg, nil)
	assertEnv(t, cfg.Tasks[defaultTaskName].Envs, "RBENV_DEFAULT_GEMS_LIST", "")

	// A custom repo is passed through.
	cfg = &ast.DeployFile{}
	load(t, cfg, map[string]any{
		"default_gems":      []string{"bundler"},
		"default_gems_repo": "https://example.com/rbenv-default-gems.git",
	})
	assertEnv(t, cfg.Tasks[defaultTaskName].Envs, "RBENV_DEFAULT_GEMS_REPO", "https://example.com/rbenv-default-gems.git")
}

func TestStartup_ExtraPlugins(t *testing.T) {
	cfg := &ast.DeployFile{}
	load(t, cfg, map[string]any{
		"plugins": []map[string]any{
			{"name": "rbenv-vars", "repo": "https://github.com/rbenv/rbenv-vars.git", "version": "v1.2.0"},
			{"name": "rbenv-installer", "repo": "https://github.com/rbenv/rbenv-installer.git", "version": "b53c0f1"},
			{"name": "my-plugin", "repo": "https://ddddd"}, // no version
		},
	})
	// One "<name> <repo> [version]" per line; the script clones each into $RBENV_ROOT/plugins/<name>.
	want := "rbenv-vars https://github.com/rbenv/rbenv-vars.git v1.2.0\n" +
		"rbenv-installer https://github.com/rbenv/rbenv-installer.git b53c0f1\n" +
		"my-plugin https://ddddd"
	assertEnv(t, cfg.Tasks[defaultTaskName].Envs, "RBENV_PLUGINS", want)

	// Unset -> empty (script skips the section).
	cfg = &ast.DeployFile{}
	load(t, cfg, nil)
	assertEnv(t, cfg.Tasks[defaultTaskName].Envs, "RBENV_PLUGINS", "")
}

func TestStartup_ExtraPluginsValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		plugin map[string]any
	}{
		{"missing repo", map[string]any{"name": "broken"}},
		{"missing name", map[string]any{"repo": "https://x"}},
		{"name with space", map[string]any{"name": "bad name", "repo": "https://x"}},
		{"version with space", map[string]any{"name": "ok", "repo": "https://x", "version": "v1 v2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, err := whoosh.Load([]whoosh.PluginSpec{{Name: pluginName, Params: map[string]any{
				"plugins": []map[string]any{tc.plugin},
			}}})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := reg.RunStartup(context.Background(), &ast.DeployFile{}); err == nil {
				t.Fatalf("expected an error for %s", tc.name)
			}
		})
	}
}

func TestStartup_ReadRubyVersionDisabled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".ruby-version"), []byte("3.2.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &ast.DeployFile{Dir: dir}
	load(t, cfg, map[string]any{"read_ruby_version": false, "versions": []string{"3.3.0"}})

	env := cfg.Tasks[defaultTaskName].Envs
	if env["RBENV_VERSIONS"] != "3.3.0" {
		t.Errorf("RBENV_VERSIONS = %q, want %q (file must be ignored)", env["RBENV_VERSIONS"], "3.3.0")
	}
	assertEnv(t, env, "RBENV_READ_RUBY_VERSION", "0")
}

func TestStartup_CustomReposAndInstallRubyOff(t *testing.T) {
	cfg := &ast.DeployFile{}
	load(t, cfg, map[string]any{
		"rbenv_repo":      "https://example.com/rbenv.git",
		"ruby_build_repo": "https://example.com/ruby-build.git",
		"install_ruby":    false,
	})
	env := cfg.Tasks[defaultTaskName].Envs
	assertEnv(t, env, "RBENV_REPO", "https://example.com/rbenv.git")
	assertEnv(t, env, "RUBY_BUILD_REPO", "https://example.com/ruby-build.git")
	assertEnv(t, env, "RBENV_INSTALL_RUBY", "0")
}

func TestStartup_AfterHookAndCustomPhase(t *testing.T) {
	cfg := &ast.DeployFile{}
	load(t, cfg, map[string]any{"when": "after", "phase": "deploy:updated", "task_name": "ruby:prepare"})

	if _, ok := cfg.Tasks["ruby:prepare"]; !ok {
		t.Fatalf("custom task name not registered")
	}
	if got := cfg.Hooks.After["deploy:updated"]; len(got) != 1 || got[0] != "ruby:prepare" {
		t.Errorf("after[deploy:updated] = %v, want [ruby:prepare]", got)
	}
	if len(cfg.Hooks.Before) != 0 {
		t.Errorf("no before hook expected, got %v", cfg.Hooks.Before)
	}
}

func TestExpandRoot(t *testing.T) {
	cases := map[string]string{
		"":              defaultRoot,
		"~":             "$HOME",
		"~/.rbenv":      "$HOME/.rbenv",
		"~/tools/rbenv": "$HOME/tools/rbenv",
		"/opt/rbenv":    "/opt/rbenv",
		"$HOME/.rbenv":  "$HOME/.rbenv",
	}
	for in, want := range cases {
		if got := expandRoot(in); got != want {
			t.Errorf("expandRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadLocalRubyVersion(t *testing.T) {
	dir := t.TempDir()
	if got := readLocalRubyVersion(dir, ".ruby-version"); got != "" {
		t.Errorf("missing file should yield %q, got %q", "", got)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ruby-version"), []byte("ruby-3.3.0 # jruby\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readLocalRubyVersion(dir, ".ruby-version"); got != "3.3.0" {
		t.Errorf("readLocalRubyVersion = %q, want %q", got, "3.3.0")
	}
}

func assertEnv(t *testing.T, env map[string]string, key, want string) {
	t.Helper()
	if env[key] != want {
		t.Errorf("env[%q] = %q, want %q", key, env[key], want)
	}
}

func keys(m map[string]*ast.Task) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func equalSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			return false
		}
	}
	return true
}
