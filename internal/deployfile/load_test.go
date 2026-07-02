package deployfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh/internal/deployfile"
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

// writeProject lays down a shared Deployfile and one stage file in a temp dir and returns the path to the Deployfile.
func writeProject(t *testing.T, shared, stageName, stage string) string {
	t.Helper()
	dir := t.TempDir()
	df := filepath.Join(dir, "Deployfile.yml")
	if err := os.WriteFile(df, []byte(shared), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deploy", stageName+".yml"), []byte(stage), 0o644); err != nil {
		t.Fatal(err)
	}
	return df
}

// writeFragment writes an included fragment at rel (relative to the Deployfile's directory), creating parent dirs as
// needed.
func writeFragment(t *testing.T, df, rel, content string) {
	t.Helper()
	p := filepath.Join(filepath.Dir(df), rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_MergesStageOverBase(t *testing.T) {
	shared := `
version: "1"
app:
  name: myapp
  repo: git@example.com:org/app.git
  branch: main
  deploy_to: /var/www/myapp
  keep_releases: 5
vars:
  RAILS_ENV: production
  COMMON: base
ssh:
  user: deploy
  port: 22
tasks:
  restart:
    desc: Restart
    roles: [app]
    cmds: ["systemctl restart {{.app_name}}"]
`
	stage := `
app:
  branch: release/prod
  deploy_to: /srv/myapp
vars:
  RAILS_ENV: staging
hosts:
  - address: web1.example.com
    roles: [app, web]
  - address: db1.example.com
    roles: [db]
    port: 2222
`
	df := writeProject(t, shared, "production", stage)

	cfg, err := deployfile.Load(df, "production")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Stage overrides scalar app fields.
	if cfg.App.Branch != "release/prod" {
		t.Errorf("branch = %q, want release/prod", cfg.App.Branch)
	}
	if cfg.App.DeployTo != "/srv/myapp" {
		t.Errorf("deploy_to = %q, want /srv/myapp", cfg.App.DeployTo)
	}
	// Base scalar not overridden is retained.
	if cfg.App.Name != "myapp" {
		t.Errorf("name = %q, want myapp", cfg.App.Name)
	}
	// Vars merge: stage overrides one key, base key survives.
	if cfg.Vars["RAILS_ENV"] != "staging" {
		t.Errorf("RAILS_ENV = %v, want staging", cfg.Vars["RAILS_ENV"])
	}
	if cfg.Vars["COMMON"] != "base" {
		t.Errorf("COMMON = %v, want base", cfg.Vars["COMMON"])
	}
	// Tasks come from base.
	if _, ok := cfg.Tasks["restart"]; !ok {
		t.Errorf("restart task missing after merge")
	}
	// Servers from stage, with SSH defaults applied.
	if len(cfg.Hosts) != 2 {
		t.Fatalf("servers = %d, want 2", len(cfg.Hosts))
	}
	web := cfg.Hosts[0]
	if web.User != "deploy" || web.Port != 22 {
		t.Errorf("web1 ssh defaults not applied: user=%q port=%d", web.User, web.Port)
	}
	db := cfg.Hosts[1]
	if db.Port != 2222 {
		t.Errorf("db1 explicit port lost: %d", db.Port)
	}
	if cfg.Stage != "production" {
		t.Errorf("stage = %q, want production", cfg.Stage)
	}
}

func TestLoad_AppliesDefaults(t *testing.T) {
	shared := `
app:
  name: app
  repo: git@example.com:org/app.git
  deploy_to: /srv/app
`
	stage := `
hosts:
  - address: h1
    roles: [app]
`
	df := writeProject(t, shared, "staging", stage)
	cfg, err := deployfile.Load(df, "staging")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.App.KeepReleases != ast.DefaultKeepReleases {
		t.Errorf("keep_releases default = %d, want %d", cfg.App.KeepReleases, ast.DefaultKeepReleases)
	}
	if cfg.App.Branch != ast.DefaultBranch {
		t.Errorf("branch default = %q, want %q", cfg.App.Branch, ast.DefaultBranch)
	}
	if cfg.Hosts[0].Port != ast.DefaultSSHPort {
		t.Errorf("server port default = %d, want %d", cfg.Hosts[0].Port, ast.DefaultSSHPort)
	}
}

func TestLoad_UnknownStage(t *testing.T) {
	df := writeProject(t, "app:\n  name: a\n  repo: r\n  deploy_to: /d\n", "staging", "hosts: []\n")
	if _, err := deployfile.Load(df, "nope"); err == nil {
		t.Fatal("expected error for unknown stage")
	}
}

func TestLoad_RejectsUnknownFields(t *testing.T) {
	df := writeProject(t, "app:\n  name: a\n  repo: r\n  deploy_to: /d\n  nonsense: x\n", "staging", "hosts: []\n")
	if _, err := deployfile.Load(df, "staging"); err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestLoad_RejectsAmbiguousScript(t *testing.T) {
	shared := `
app:
  name: a
  repo: r
  deploy_to: /d
tasks:
  bad:
    scripts:
      - path: x.sh
        script: "echo hi"
`
	df := writeProject(t, shared, "staging", "hosts: []\n")
	if _, err := deployfile.Load(df, "staging"); err == nil {
		t.Fatal("expected error: script has both path and inline content")
	}
}

func TestLoad_RejectsEmptyScript(t *testing.T) {
	shared := `
app:
  name: a
  repo: r
  deploy_to: /d
tasks:
  bad:
    scripts:
      - interpreter: /bin/bash
`
	df := writeProject(t, shared, "staging", "hosts: []\n")
	if _, err := deployfile.Load(df, "staging"); err == nil {
		t.Fatal("expected error: script has neither path nor inline content")
	}
}

func TestScriptsLocation_DefaultAndOverride(t *testing.T) {
	// No stage dir on disk: default to scripts/ under the preferred stage dir.
	c := &ast.DeployFile{Dir: "/proj"}
	if got := deployfile.ScriptsLocation(c); got != "/proj/whoosh/scripts" {
		t.Errorf("default ScriptsLocation = %q, want /proj/whoosh/scripts", got)
	}

	// When a stage dir exists, scripts/ resolves under it (here: deploy/).
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	c = &ast.DeployFile{Dir: dir}
	if got, want := deployfile.ScriptsLocation(c), filepath.Join(dir, "deploy", "scripts"); got != want {
		t.Errorf("probed ScriptsLocation = %q, want %q", got, want)
	}

	// Explicit scripts_dir overrides the default (relative is joined to Dir).
	c = &ast.DeployFile{Dir: "/proj", ScriptsDir: "ops/scripts"}
	if got := deployfile.ScriptsLocation(c); got != "/proj/ops/scripts" {
		t.Errorf("relative override = %q", got)
	}
	c.ScriptsDir = "/abs/scripts"
	if got := deployfile.ScriptsLocation(c); got != "/abs/scripts" {
		t.Errorf("absolute override = %q", got)
	}
}

func TestLoad_WhooshStageDir(t *testing.T) {
	dir := t.TempDir()
	shared := `version: "1"
app:
  name: a
  repo: git@example.com:a.git
  deploy_to: /srv/a
`
	df := filepath.Join(dir, "Whooshfile.yml")
	if err := os.WriteFile(df, []byte(shared), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "whoosh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "whoosh", "prod.yml"),
		[]byte("hosts:\n  - address: h1\n    roles: [app]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := deployfile.Load(df, "prod")
	if err != nil {
		t.Fatalf("load with whoosh/ stage dir: %v", err)
	}
	if len(cfg.Hosts) != 1 || cfg.Hosts[0].Address != "h1" {
		t.Errorf("stage file under whoosh/ not loaded: %+v", cfg.Hosts)
	}
}

func TestDiscover_Whooshfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Whooshfile.yml"), []byte("app:\n  name: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := deployfile.Discover(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "Whooshfile.yml" {
		t.Errorf("Discover = %q, want Whooshfile.yml", got)
	}
}

func TestLoad_PluginsAndActionTask(t *testing.T) {
	shared := `
app:
  name: a
  repo: r
  deploy_to: /d
plugins:
  - name: aws:ec2:inventory
    params:
      region: eu-west-1
      tags: { Environment: production }
  - name: aws:ec2:asg
    params:
      region: eu-west-1
tasks:
  refresh:
    desc: refresh asg
    action: aws:ec2:asg:refresh
    with: { name: my-asg }
`
	df := writeProject(t, shared, "staging", "hosts: []\n")
	cfg, err := deployfile.Load(df, "staging")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Plugins) != 2 || cfg.Plugins[0].Name != "aws:ec2:inventory" || cfg.Plugins[1].Name != "aws:ec2:asg" {
		t.Fatalf("plugins list not parsed: %+v", cfg.Plugins)
	}
	if cfg.Plugins[0].Params["region"] != "eu-west-1" {
		t.Errorf("plugins params not parsed: %+v", cfg.Plugins[0].Params)
	}
	task := cfg.Tasks["refresh"]
	if task == nil || task.Action != "aws:ec2:asg:refresh" || task.With["name"] != "my-asg" {
		t.Fatalf("action task not parsed: %+v", task)
	}
}

func TestLoad_RejectsActionWithCmds(t *testing.T) {
	shared := `
app: { name: a, repo: r, deploy_to: /d }
tasks:
  bad: { action: x, cmds: ["echo hi"] }
`
	df := writeProject(t, shared, "staging", "hosts: []\n")
	if _, err := deployfile.Load(df, "staging"); err == nil {
		t.Fatal("expected error: action combined with cmds")
	}
}

func TestLoad_RejectsInvalidOutput(t *testing.T) {
	shared := `
app: { name: a, repo: r, deploy_to: /d }
tasks:
  bad: { output: yaml, cmds: ["echo hi"] }
`
	df := writeProject(t, shared, "staging", "hosts: []\n")
	if _, err := deployfile.Load(df, "staging"); err == nil {
		t.Fatal("expected error: invalid output format")
	}
}

func TestLoad_RejectsOutputWithAction(t *testing.T) {
	shared := `
app: { name: a, repo: r, deploy_to: /d }
tasks:
  bad: { output: json, action: x }
`
	df := writeProject(t, shared, "staging", "hosts: []\n")
	if _, err := deployfile.Load(df, "staging"); err == nil {
		t.Fatal("expected error: output combined with action")
	}
}

func TestLoad_RejectsInvalidOnUnreachable(t *testing.T) {
	shared := `
app: { name: a, repo: r, deploy_to: /d }
on_unreachable: maybe
`
	df := writeProject(t, shared, "staging", "hosts: []\n")
	if _, err := deployfile.Load(df, "staging"); err == nil {
		t.Fatal("expected error: invalid on_unreachable value")
	}
}

func TestLoad_OnUnreachableAndRequired(t *testing.T) {
	shared := `
app: { name: a, repo: r, deploy_to: /d }
on_unreachable: skip
`
	stage := `
hosts:
  - { address: db1, required: true }
  - { address: web1 }
`
	df := writeProject(t, shared, "staging", stage)
	cfg, err := deployfile.Load(df, "staging")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.OnUnreachable != ast.OnUnreachableSkip {
		t.Errorf("on_unreachable = %q, want skip", cfg.OnUnreachable)
	}
	want := map[string]bool{"db1": true, "web1": false}
	for _, s := range cfg.Hosts {
		if s.IsRequired() != want[s.Address] {
			t.Errorf("%s IsRequired = %v, want %v", s.Address, s.IsRequired(), want[s.Address])
		}
	}
}

func TestLoad_RejectsPluginWithoutName(t *testing.T) {
	shared := `
app: { name: a, repo: r, deploy_to: /d }
plugins:
  - params: { region: x }
`
	df := writeProject(t, shared, "staging", "hosts: []\n")
	if _, err := deployfile.Load(df, "staging"); err == nil {
		t.Fatal("expected error: plugins without name")
	}
}

func TestLoad_RejectsNegativeKeepReleases(t *testing.T) {
	// A negative keep_releases would make the cleanup script select every release (including the live one) for rm -rf.
	shared := `
app: { name: a, repo: r, deploy_to: /d, keep_releases: -1 }
`
	df := writeProject(t, shared, "staging", "hosts: []\n")
	if _, err := deployfile.Load(df, "staging"); err == nil {
		t.Fatal("expected error: negative keep_releases")
	}
}

func TestLoad_IncludeMergesAndPrecedence(t *testing.T) {
	shared := `
app: { name: a, repo: r, deploy_to: /d }
vars: { COMMON: base, ONLY_BASE: keep, OVERRIDE_ME: base }
tasks:
  restart: { cmds: ["echo restart"] }
  dup: { cmds: ["echo base"] }
`
	stage := `
include: [shared/common.yml]
vars: { COMMON: stage }
hosts:
  - { address: h1, roles: [app] }
`
	df := writeProject(t, shared, "production", stage)
	writeFragment(t, df, "deploy/shared/common.yml", `
vars: { COMMON: included, FROM_INCLUDE: x, OVERRIDE_ME: included }
tasks:
  migrate: { cmds: ["echo migrate"] }
  dup: { cmds: ["echo included"] }
`)

	cfg, err := deployfile.Load(df, "production")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Tasks: base + include both present.
	for _, name := range []string{"restart", "migrate", "dup"} {
		if _, ok := cfg.Tasks[name]; !ok {
			t.Errorf("task %q missing after include merge", name)
		}
	}
	// Include wins over base for a same-named task.
	if got := cfg.Tasks["dup"].Cmds[0]; got != "echo included" {
		t.Errorf("dup cmd = %q, want include to win (echo included)", got)
	}
	// Precedence on vars: stage > include > base; unique keys all survive.
	want := map[string]string{
		"COMMON":       "stage",    // stage wins
		"OVERRIDE_ME":  "included", // include wins over base
		"ONLY_BASE":    "keep",     // base survives
		"FROM_INCLUDE": "x",        // include survives
	}
	for k, v := range want {
		if cfg.Vars[k] != v {
			t.Errorf("vars[%q] = %v, want %v", k, cfg.Vars[k], v)
		}
	}
}

func TestLoad_IncludeNestedRelative(t *testing.T) {
	shared := `app: { name: a, repo: r, deploy_to: /d }`
	stage := `
include: [shared/web.yml]
hosts:
  - { address: h1, roles: [app] }
`
	df := writeProject(t, shared, "production", stage)
	// deploy/shared/web.yml climbs out to deploy/libs/proxy.yml via "../".
	writeFragment(t, df, "deploy/shared/web.yml", `
include: ["../libs/proxy.yml"]
tasks:
  web: { cmds: ["echo web"] }
`)
	writeFragment(t, df, "deploy/libs/proxy.yml", `
vars: { PROXY: enabled }
tasks:
  proxy: { cmds: ["echo proxy"] }
`)

	cfg, err := deployfile.Load(df, "production")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, name := range []string{"web", "proxy"} {
		if _, ok := cfg.Tasks[name]; !ok {
			t.Errorf("task %q missing; nested/relative include not resolved", name)
		}
	}
	if cfg.Vars["PROXY"] != "enabled" {
		t.Errorf("PROXY = %v, want enabled (from ../libs/proxy.yml)", cfg.Vars["PROXY"])
	}
}

func TestLoad_IncludeDiamondMergedOnce(t *testing.T) {
	// b.yml and c.yml both include d.yml; d must be merged once, or its concatenated
	// fields (hosts, plugins, env_files) would be duplicated in the final config.
	shared := `app: { name: a, repo: r, deploy_to: /d }`
	stage := `
include: [shared/b.yml, shared/c.yml]
hosts:
  - { address: h1, roles: [app] }
`
	df := writeProject(t, shared, "production", stage)
	writeFragment(t, df, "deploy/shared/b.yml", `
include: [d.yml]
tasks: { b: { cmds: ["echo b"] } }
`)
	writeFragment(t, df, "deploy/shared/c.yml", `
include: [d.yml]
tasks: { c: { cmds: ["echo c"] } }
`)
	writeFragment(t, df, "deploy/shared/d.yml", `
hosts:
  - { address: shared-host, roles: [db] }
`)

	cfg, err := deployfile.Load(df, "production")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, name := range []string{"b", "c"} {
		if _, ok := cfg.Tasks[name]; !ok {
			t.Errorf("task %q missing after diamond include merge", name)
		}
	}
	var n int
	for _, h := range cfg.Hosts {
		if h.Address == "shared-host" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("shared-host appears %d times, want 1 (diamond include must merge once)", n)
	}
}

func TestLoad_IncludeScalarForm(t *testing.T) {
	shared := `app: { name: a, repo: r, deploy_to: /d }`
	stage := `
include: shared/common.yml
hosts:
  - { address: h1, roles: [app] }
`
	df := writeProject(t, shared, "production", stage)
	writeFragment(t, df, "deploy/shared/common.yml", `tasks: { only: { cmds: ["echo"] } }`)

	cfg, err := deployfile.Load(df, "production")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := cfg.Tasks["only"]; !ok {
		t.Error("scalar include form did not parse/merge")
	}
}

func TestLoad_IncludeCircular(t *testing.T) {
	shared := `app: { name: a, repo: r, deploy_to: /d }`
	stage := `
include: [shared/a.yml]
hosts:
  - { address: h1, roles: [app] }
`
	df := writeProject(t, shared, "production", stage)
	writeFragment(t, df, "deploy/shared/a.yml", `include: [b.yml]`)
	writeFragment(t, df, "deploy/shared/b.yml", `include: [a.yml]`)

	_, err := deployfile.Load(df, "production")
	if err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("expected circular include error, got %v", err)
	}
}

func TestLoad_IncludeMissing(t *testing.T) {
	shared := `app: { name: a, repo: r, deploy_to: /d }`
	stage := `
include: [shared/nope.yml]
hosts:
  - { address: h1, roles: [app] }
`
	df := writeProject(t, shared, "production", stage)
	if _, err := deployfile.Load(df, "production"); err == nil {
		t.Fatal("expected error for missing include file")
	}
}

func TestLoad_IncludeNotLeaked(t *testing.T) {
	shared := `app: { name: a, repo: r, deploy_to: /d }`
	stage := `
include: shared/common.yml
hosts:
  - { address: h1, roles: [app] }
`
	df := writeProject(t, shared, "production", stage)
	writeFragment(t, df, "deploy/shared/common.yml", `tasks: { only: { cmds: ["echo"] } }`)

	cfg, err := deployfile.Load(df, "production")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Include != nil {
		t.Errorf("Include should be nil after load, got %v", cfg.Include)
	}
	m, err := cfg.AsMap()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m["include"]; ok {
		t.Error("include key leaked into AsMap()/config output")
	}
}

func TestLoad_IncludeInDeployfile(t *testing.T) {
	// The shared Deployfile itself includes a fragment (resolved relative to the Deployfile's dir); it merges below
	// everything, confirming uniform handling.
	shared := `
include: [base-extra.yml]
app: { name: a, repo: r, deploy_to: /d }
`
	stage := `
hosts:
  - { address: h1, roles: [app] }
`
	df := writeProject(t, shared, "production", stage)
	writeFragment(t, df, "base-extra.yml", `tasks: { fromdeployfileinclude: { cmds: ["echo"] } }`)

	cfg, err := deployfile.Load(df, "production")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := cfg.Tasks["fromdeployfileinclude"]; !ok {
		t.Error("include in the shared Deployfile was not resolved")
	}
}

func TestDiscover_PrefersOverride(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "custom.yml")
	if err := os.WriteFile(custom, []byte("app:\n  name: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := deployfile.Discover(dir, custom)
	if err != nil {
		t.Fatal(err)
	}
	if got != custom {
		t.Errorf("Discover override = %q, want %q", got, custom)
	}
}
