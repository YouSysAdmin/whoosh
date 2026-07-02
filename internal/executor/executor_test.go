package executor_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/executor"
	"github.com/yousysadmin/whoosh/internal/masking"
	"github.com/yousysadmin/whoosh/internal/plugins"
	"github.com/yousysadmin/whoosh/transport/ssh"
	"github.com/yousysadmin/whoosh/transport/sshtest"
)

func newTestConfig(srv *sshtest.Server, deployTo string) *ast.DeployFile {
	return &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: deployTo, Branch: "main"},
		Vars:  map[string]any{"GREETING": "hello"},
		Stage: "test",
		Hosts: []ast.Host{{
			Address:      srv.Host,
			Port:         srv.Port,
			User:         "deploy",
			IdentityFile: srv.IdentityFile,
			Roles:        []string{"app"},
		}},
		Tasks: map[string]*ast.Task{
			"greet": {Roles: []string{"app"}, Cmds: []string{"echo {{.GREETING}}-from-{{.host}}"}},
			"build": {Local: true, Cmds: []string{"echo building {{.app_name}}"}},
			"indir": {Roles: []string{"app"}, Dir: "/tmp", Envs: map[string]string{"FOO": "bar"}, Cmds: []string{"echo $FOO in $(pwd)"}},
		},
	}
}

// deployTree returns a temp deploy_to with a current/ dir, since remote task commands now default to running inside the
// release (current) directory.
func deployTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "current"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func run(t *testing.T, srv *sshtest.Server, task string) string {
	t.Helper()
	var buf bytes.Buffer
	ex := executor.New(newTestConfig(srv, deployTree(t)), executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	defer ex.Close()
	if err := ex.RunTask(context.Background(), task); err != nil {
		t.Fatalf("RunTask(%q): %v\noutput:\n%s", task, err, buf.String())
	}
	return buf.String()
}

func TestRunTask_Remote(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	out := run(t, srv, "greet")
	if !strings.Contains(out, "hello-from-"+srv.Host) {
		t.Fatalf("missing rendered remote output, got:\n%s", out)
	}
}

func TestRunTask_Local(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	out := run(t, srv, "build")
	if !strings.Contains(out, "building myapp") {
		t.Fatalf("missing local output, got:\n%s", out)
	}
}

func TestRunTask_DirAndEnv(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	out := run(t, srv, "indir")
	if !strings.Contains(out, "bar in /tmp") {
		t.Fatalf("dir/env not applied, got:\n%s", out)
	}
}

func TestRunTask_DirIsTemplated(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	deployTo := deployTree(t)
	cfg := newTestConfig(srv, deployTo)
	// A pre-release hook pattern: dir points at an existing path via a template (the release dir wouldn't exist yet).
	// {{.deploy_to}} must resolve.
	cfg.Tasks["indir-tmpl"] = &ast.Task{
		Roles: []string{"app"},
		Dir:   "{{.deploy_to}}",
		Cmds:  []string{"pwd"},
	}

	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	if err := ex.RunTask(context.Background(), "indir-tmpl"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}
	ex.Close()

	if !strings.Contains(buf.String(), deployTo) {
		t.Fatalf("templated dir did not resolve to %q in:\n%s", deployTo, buf.String())
	}
}

func TestRunTask_ActionParamsAreTemplated(t *testing.T) {
	reg, err := plugins.Load(nil) // empty registry
	if err != nil {
		t.Fatalf("plugins.Load: %v", err)
	}
	var got map[string]any
	if err := reg.AddAction("test:echo", func(_ context.Context, params map[string]any, _ io.Writer) error {
		got = params
		return nil
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}

	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: t.TempDir(), Branch: "main"},
		Stage: "prod",
		Vars:  map[string]any{"asg_name": "my-asg"},
		Tasks: map[string]*ast.Task{
			"roll": {Action: "test:echo", With: map[string]any{
				"name":            "{{ .asg_name }}",                          // from vars
				"stage":           "{{ .stage }}",                             // from deploy context
				"launch_template": map[string]any{"id": "{{ .asg_name }}-lt"}, // nested
				"keep":            3,                                          // non-string left intact
				"flag":            false,
			}},
		},
	}

	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{Out: &buf, Registry: reg})
	defer ex.Close()
	if err := ex.RunTask(context.Background(), "roll"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}

	if got["name"] != "my-asg" {
		t.Errorf("name = %v, want my-asg (templated from vars)", got["name"])
	}
	if got["stage"] != "prod" {
		t.Errorf("stage = %v, want prod (templated from deploy context)", got["stage"])
	}
	lt, _ := got["launch_template"].(map[string]any)
	if lt == nil || lt["id"] != "my-asg-lt" {
		t.Errorf("launch_template = %v, want id=my-asg-lt (nested templating)", got["launch_template"])
	}
	if got["keep"] != 3 || got["flag"] != false {
		t.Errorf("non-string params changed: keep=%v flag=%v", got["keep"], got["flag"])
	}
}

func TestRunTask_SkipsInactivePluginAction(t *testing.T) {
	reg, err := plugins.Load(nil)
	if err != nil {
		t.Fatalf("plugins.Load: %v", err)
	}
	called := false
	if err := reg.AddAction("aws:ec2:ami:create", func(context.Context, map[string]any, io.Writer) error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}

	cfg := &ast.DeployFile{
		App:            ast.App{Name: "a", DeployTo: t.TempDir()},
		Stage:          "staging",
		SkippedPlugins: []string{"aws"}, // aws plugins inactive for this stage
		Tasks: map[string]*ast.Task{
			"bake": {Action: "aws:ec2:ami:create"},
		},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{Out: &buf, Registry: reg})
	defer ex.Close()

	if err := ex.RunTask(context.Background(), "bake"); err != nil {
		t.Fatalf("a skipped action task must not error: %v", err)
	}
	if called {
		t.Error("action ran despite its plugins being inactive for the stage")
	}
}

func TestRunTask_SkipsTaskInactiveForStage(t *testing.T) {
	newCfg := func(stage string) *ast.DeployFile {
		return &ast.DeployFile{
			App:   ast.App{Name: "a", DeployTo: t.TempDir()},
			Stage: stage,
			Tasks: map[string]*ast.Task{
				// active everywhere except staging
				"prod-only": {Local: true, Except: []string{"staging"}, Cmds: []string{"echo RAN_PROD_ONLY"}},
			},
		}
	}

	// Excluded stage: the task is skipped - no error, and its command never runs.
	var buf bytes.Buffer
	ex := executor.New(newCfg("staging"), executor.Options{Out: &buf})
	defer ex.Close()
	if err := ex.RunTask(context.Background(), "prod-only"); err != nil {
		t.Fatalf("an inactive task must not error: %v", err)
	}
	if strings.Contains(buf.String(), "RAN_PROD_ONLY") {
		t.Errorf("task ran despite being inactive for stage staging:\n%s", buf.String())
	}

	// Active stage: the task runs.
	var buf2 bytes.Buffer
	ex2 := executor.New(newCfg("production"), executor.Options{Out: &buf2})
	defer ex2.Close()
	if err := ex2.RunTask(context.Background(), "prod-only"); err != nil {
		t.Fatalf("active task: %v", err)
	}
	if !strings.Contains(buf2.String(), "RAN_PROD_ONLY") {
		t.Errorf("active task did not run for stage production:\n%s", buf2.String())
	}
}

func TestRunTask_ExposesHostRoles(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	cfg := newTestConfig(srv, deployTree(t))
	cfg.Hosts[0].Roles = []string{"app", "worker"} // the host has multiple roles
	cfg.Tasks["roles-echo"] = &ast.Task{
		Roles: []string{"app"},
		Cmds:  []string{`echo "env=$ROLES tmpl={{ join "," .roles }}"`},
	}

	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	defer ex.Close()
	if err := ex.RunTask(context.Background(), "roles-echo"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}
	// Both the $ROLES env var and the {{.roles}} template see the host's full roles.
	if !strings.Contains(buf.String(), "env=app,worker tmpl=app,worker") {
		t.Fatalf("host roles not exposed; want env=app,worker tmpl=app,worker in:\n%s", buf.String())
	}
}

func TestRunTask_ResolvesDeployEnvInCmd(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	deployTo := deployTree(t)
	cfg := newTestConfig(srv, deployTo)
	cfg.Tasks["done"] = &ast.Task{
		Roles: []string{"app"},
		Cmds:  []string{`echo "Released {{.app_name}} to $RELEASE_PATH on $HOST"`},
	}

	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	if err := ex.RunTask(context.Background(), "done"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}
	ex.Close()

	want := "Released myapp to " + filepath.Join(deployTo, "current") + " on " + srv.Host
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("deploy env not resolved in cmd: want %q in\n%s", want, buf.String())
	}
}

func TestRunTask_ExposesKeepReleases(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	cfg := newTestConfig(srv, deployTree(t))
	cfg.App.KeepReleases = 7 // app.keep_releases -> {{.keep_releases}} / $KEEP_RELEASES
	cfg.Tasks["keep"] = &ast.Task{
		Roles: []string{"app"},
		Cmds:  []string{`echo "tmpl={{.keep_releases}} env=$KEEP_RELEASES"`},
	}

	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	if err := ex.RunTask(context.Background(), "keep"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}
	ex.Close()

	if !strings.Contains(buf.String(), "tmpl=7 env=7") {
		t.Fatalf("keep_releases not exposed to template/env:\n%s", buf.String())
	}
}

func TestRunTaskInPhase_ExposesPhase(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "app", DeployTo: "/srv/app"},
		Stage: "test",
		Hosts: []ast.Host{{Address: "h1", Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"note": {Roles: []string{"app"}, Cmds: []string{`echo "phase={{.phase}} env=$DEPLOY_PHASE"`}},
		},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf, DryRun: true})
	defer ex.Close()
	if err := ex.RunTaskInPhase(context.Background(), "note", "deploy:publishing"); err != nil {
		t.Fatalf("RunTaskInPhase: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "phase=deploy:publishing") {
		t.Errorf("template {{.phase}} not set:\n%s", out)
	}
	if !strings.Contains(out, `export DEPLOY_PHASE="deploy:publishing"`) {
		t.Errorf("$DEPLOY_PHASE not exported:\n%s", out)
	}
	// The phase is restored after the run: a plain RunTask must not see it.
	buf.Reset()
	if err := ex.RunTask(context.Background(), "note"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if strings.Contains(buf.String(), "deploy:publishing") {
		t.Errorf("phase leaked into a standalone run:\n%s", buf.String())
	}
}

func TestRunTask_CaptureJSONState(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "app", DeployTo: "/srv/app"},
		Stage: "test",
		Hosts: []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"whoami": {Local: true, Output: "json", Cmds: []string{`echo '{"Account":"123","Role":"web"}'`}},
			"use": {
				Local: true,
				Deps:  []string{"whoami"},
				Envs:  map[string]string{"ACCT": "{{ .tasks.whoami.Account }}"},
				Cmds:  []string{`echo "account={{ .tasks.whoami.Account }} role={{ .tasks.whoami.Role }} env=$ACCT"`},
			},
		},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	if err := ex.RunTask(context.Background(), "use"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}
	ex.Close()
	if out := buf.String(); !strings.Contains(out, "account=123 role=web env=123") {
		t.Fatalf("captured JSON state not consumed (template + env):\n%s", out)
	}
}

func TestRunTask_CaptureTextAndLines(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "app", DeployTo: "/srv/app"},
		Stage: "test",
		Hosts: []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"ver":   {Local: true, Output: "text", Cmds: []string{"echo '  v1.2.3  '"}}, // trimmed
			"hosts": {Local: true, Output: "lines", Cmds: []string{`printf 'a\nb\nc\n'`}},
			"show": {
				Local: true,
				Deps:  []string{"ver", "hosts"},
				Cmds:  []string{`echo "ver={{ .tasks.ver }} first={{ index .tasks.hosts 0 }} count={{ len .tasks.hosts }}"`},
			},
		},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	if err := ex.RunTask(context.Background(), "show"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}
	ex.Close()
	if out := buf.String(); !strings.Contains(out, "ver=v1.2.3 first=a count=3") {
		t.Fatalf("text/lines state not consumed:\n%s", out)
	}
}

func TestRunTask_CaptureDryRunRenders(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "app", DeployTo: "/srv/app"},
		Stage: "test",
		Hosts: []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"whoami": {Local: true, Output: "json", Cmds: []string{`echo '{"Account":"123"}'`}},
			"use":    {Local: true, Deps: []string{"whoami"}, Cmds: []string{`echo "acct={{ .tasks.whoami.Account }}"`}},
		},
	}
	// Dry-run must not error even though the producer didn't run (the field is unknown -> "<no value>"), so a deploy
	// --dry-run with a state chain still works.
	out := dryRunPlan(t, cfg, "use")
	if !strings.Contains(out, "capture local:") {
		t.Errorf("producer capture step not shown in dry-run:\n%s", out)
	}
	if !strings.Contains(out, "acct=<no value>") {
		t.Errorf("consumer not rendered leniently in dry-run:\n%s", out)
	}
}

func TestRunTask_MissingStateIsError(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "app", DeployTo: "/srv/app"},
		Stage: "test",
		Hosts: []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"use": {Local: true, Cmds: []string{`echo "{{ .tasks.nope }}"`}},
		},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	defer ex.Close()
	// A real run is strict: referencing unset task state is an error (typo guard).
	if err := ex.RunTask(context.Background(), "use"); err == nil {
		t.Fatal("expected error referencing undefined task state")
	}
}

// orderOf reads a log file each task appended its name to and returns the names comma-joined in run order.
func orderOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read order log: %v", err)
	}
	return strings.Join(strings.Fields(string(data)), ",")
}

func TestRunTask_TaskHooksBracketInvocation(t *testing.T) {
	log := filepath.Join(t.TempDir(), "ORDER")
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "app", DeployTo: "/srv/app"},
		Stage: "test",
		Hosts: []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"restart_sidekiq":  {Local: true, Deps: []string{"setup"}, Cmds: []string{"echo restart >> " + log}},
			"setup":            {Local: true, Cmds: []string{"echo setup >> " + log}},
			"prepare":          {Local: true, Cmds: []string{"echo prepare >> " + log}},
			"push_to_newrelic": {Local: true, Cmds: []string{"echo newrelic >> " + log}},
		},
		// A before/after hook keyed by a task name wraps that task wherever it runs - no phase involved.
		Hooks: ast.Hooks{
			Before: map[string][]string{"restart_sidekiq": {"prepare"}},
			After:  map[string][]string{"restart_sidekiq": {"push_to_newrelic"}},
		},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	if err := ex.RunTask(context.Background(), "restart_sidekiq"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}
	ex.Close()
	// Before hook, then the task's own dep, then its body, then the after hook.
	if got, want := orderOf(t, log), "prepare,setup,restart,newrelic"; got != want {
		t.Fatalf("hook order = %q, want %q\n%s", got, want, buf.String())
	}
}

func TestRunTask_TaskHookFiresWhenTaskRunsAsDep(t *testing.T) {
	log := filepath.Join(t.TempDir(), "ORDER")
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "app", DeployTo: "/srv/app"},
		Stage: "test",
		Hosts: []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"deploy_app": {Local: true, Deps: []string{"migrate"}, Cmds: []string{"echo deploy >> " + log}},
			"migrate":    {Local: true, Cmds: []string{"echo migrate >> " + log}},
			"notify":     {Local: true, Cmds: []string{"echo notify >> " + log}},
		},
		// notify is hooked after migrate, which here runs only as a dependency of deploy_app - the hook still fires.
		Hooks: ast.Hooks{After: map[string][]string{"migrate": {"notify"}}},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	if err := ex.RunTask(context.Background(), "deploy_app"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}
	ex.Close()
	if got, want := orderOf(t, log), "migrate,notify,deploy"; got != want {
		t.Fatalf("hook order = %q, want %q\n%s", got, want, buf.String())
	}
}

func TestRunTask_TaskHookCycleDetected(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "app", DeployTo: "/srv/app"},
		Stage: "test",
		Hosts: []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"loop": {Local: true, Cmds: []string{"true"}},
		},
		// A hook that re-enters the task it wraps must be caught, not recurse forever.
		Hooks: ast.Hooks{After: map[string][]string{"loop": {"loop"}}},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	defer ex.Close()
	err := ex.RunTask(context.Background(), "loop")
	if err == nil {
		t.Fatalf("expected a cycle error from a self-referential hook\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want a cycle error, got: %v", err)
	}
}

func TestMarkUnreachable_ExcludesHost(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "app", DeployTo: "/srv/app"},
		Stage: "test",
		Hosts: []ast.Host{
			{Address: "h1", Roles: []string{"app"}},
			{Address: "h2", Roles: []string{"app"}},
		},
		Tasks: map[string]*ast.Task{
			"t": {Roles: []string{"app"}, Cmds: []string{"echo hi"}},
		},
	}

	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	defer ex.Close()
	if got := len(ex.Hosts()); got != 2 {
		t.Fatalf("Servers() = %d, want 2", got)
	}
	ex.MarkUnreachable("h1")
	got := ex.Hosts()
	if len(got) != 1 || got[0].Address != "h2" {
		t.Fatalf("Servers() after exclude = %+v, want [h2]", got)
	}

	// Task targeting honors the exclusion too (dry-run only plans for h2).
	var buf2 bytes.Buffer
	ex2 := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf2, DryRun: true})
	defer ex2.Close()
	ex2.MarkUnreachable("h1")
	if err := ex2.RunTask(context.Background(), "t"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	out := buf2.String()
	if strings.Contains(out, "h1") || !strings.Contains(out, "h2") {
		t.Fatalf("excluded host still targeted by task:\n%s", out)
	}
}

func TestRunTask_Scripts(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	dir := t.TempDir()
	scriptsDir := filepath.Join(dir, "deploy", "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "aa.sh"),
		[]byte("echo file-script app=$APP_NAME host=$HOST greeting=$GREETING custom=$CUSTOM\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "current"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: dir, Branch: "main"},
		Vars:  map[string]any{"GREETING": "hello"},
		Stage: "test",
		Dir:   dir,
		Hosts: []ast.Host{{Address: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: srv.IdentityFile, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"scripted": {
				Roles: []string{"app"},
				Envs:  map[string]string{"CUSTOM": "xyz"},
				Scripts: []ast.Script{
					{Path: "aa.sh", Interpreter: "/bin/bash"},                           // file from scripts dir
					{Name: "inline", Script: "echo inline-host={{.host}}-stage=$STAGE"}, // templated + env
				},
			},
		},
	}

	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	defer ex.Close()
	if err := ex.RunTask(context.Background(), "scripted"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}

	out := buf.String()
	for _, want := range []string{
		"file-script app=myapp host=" + srv.Host + " greeting=hello custom=xyz",
		"inline-host=" + srv.Host + "-stage=test",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestRunTask_TemplatedFileScript(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	dir := t.TempDir()
	scriptsDir := filepath.Join(dir, "deploy", "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A .tmpl file is templated automatically; it reads the whole config + host.
	body := "echo app={{.config.app.name}} host={{.host}} stage={{.stage}}\n" +
		"{{range .config.hosts}}echo role={{index .roles 0}}\n{{end}}"
	if err := os.WriteFile(filepath.Join(scriptsDir, "info.sh.tmpl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "current"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: dir, Branch: "main"},
		Stage: "prod",
		Dir:   dir,
		Hosts: []ast.Host{{Address: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: srv.IdentityFile, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"info": {Roles: []string{"app"}, Scripts: []ast.Script{{Path: "info.sh.tmpl"}}},
		},
	}

	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	defer ex.Close()
	if err := ex.RunTask(context.Background(), "info"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}

	out := buf.String()
	for _, want := range []string{"app=myapp host=" + srv.Host + " stage=prod", "role=app"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestRunTask_ScriptMissingFile(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: "/tmp/deploy"},
		Stage: "test",
		Dir:   t.TempDir(),
		Hosts: []ast.Host{{Address: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: srv.IdentityFile, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"x": {Roles: []string{"app"}, Scripts: []ast.Script{{Path: "nope.sh"}}},
		},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	defer ex.Close()
	if err := ex.RunTask(context.Background(), "x"); err == nil {
		t.Fatal("expected error for missing script file")
	}
}

// dryRunPlan runs a task in dry-run and returns the printed plan, so we can assert the generated command (env exports,
// cd) without a live host.
func dryRunPlan(t *testing.T, cfg *ast.DeployFile, task string) string {
	t.Helper()
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf, DryRun: true})
	defer ex.Close()
	if err := ex.RunTask(context.Background(), task); err != nil {
		t.Fatalf("RunTask(%q): %v", task, err)
	}
	return buf.String()
}

func TestTask_DefaultsToReleaseDir(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: "/srv/app"},
		Stage: "test",
		Hosts: []ast.Host{{Address: "h1", Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"def":   {Roles: []string{"app"}, Cmds: []string{"pwd"}},
			"fixed": {Roles: []string{"app"}, Dir: "/custom", Cmds: []string{"pwd"}},
			"local": {Local: true, Cmds: []string{"pwd"}},
		},
	}
	// No dir -> cd into the release (current for standalone runs).
	if out := dryRunPlan(t, cfg, "def"); !strings.Contains(out, "cd '/srv/app/current' && pwd") {
		t.Errorf("default dir plan = %q, want cd into /srv/app/current", out)
	}
	// Explicit dir wins.
	if out := dryRunPlan(t, cfg, "fixed"); !strings.Contains(out, "cd '/custom' && pwd") {
		t.Errorf("explicit dir plan = %q, want cd into /custom", out)
	}
	// Local task: no cd (runs in the operator's cwd).
	if out := dryRunPlan(t, cfg, "local"); strings.Contains(out, "cd ") {
		t.Errorf("local task should not cd, got %q", out)
	}
}

func TestTask_GlobalEnvExportedAndExpands(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: "/srv/app"},
		Stage: "test",
		Envs:  map[string]string{"A": "base", "B": "$A-x", "PATH": "$HOME/.rbenv/shims:$PATH"},
		Hosts: []ast.Host{{Address: "h1", Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"t": {Roles: []string{"app"}, Envs: map[string]string{"A": "task"}, Cmds: []string{"echo hi"}},
		},
	}
	out := dryRunPlan(t, cfg, "t")
	// Values are double-quoted so $-references stay for the remote shell to expand.
	if !strings.Contains(out, `export PATH="$HOME/.rbenv/shims:$PATH"`) {
		t.Errorf("missing expandable PATH export in:\n%s", out)
	}
	if !strings.Contains(out, `export B="$A-x"`) {
		t.Errorf("missing expandable B export in:\n%s", out)
	}
	// Task env overrides global env.
	if !strings.Contains(out, `export A="task"`) {
		t.Errorf("task env should override global env (A), got:\n%s", out)
	}
}

func TestTask_EnvTemplatesFromSystem(t *testing.T) {
	t.Setenv("DEPLOY_SECRET", "s3cr3t")
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: "/srv/app"},
		Stage: "test",
		Envs:  map[string]string{"GLOBAL_TOK": `{{ env "DEPLOY_SECRET" }}`},
		Hosts: []ast.Host{{Address: "h1", Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"t": {
				Roles: []string{"app"},
				Envs:  map[string]string{"TASK_TOK": `{{ env "DEPLOY_SECRET" }}-x`},
				Cmds:  []string{"echo hi"},
			},
		},
	}
	out := dryRunPlan(t, cfg, "t")
	if !strings.Contains(out, `export GLOBAL_TOK="s3cr3t"`) {
		t.Errorf("global env not templated from system env:\n%s", out)
	}
	if !strings.Contains(out, `export TASK_TOK="s3cr3t-x"`) {
		t.Errorf("task env not templated from system env:\n%s", out)
	}
}

func TestRunTask_RedactsOutput(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	cfg := newTestConfig(srv, deployTree(t))
	cfg.Tasks["leak"] = &ast.Task{
		Roles: []string{"app"},
		Cmds:  []string{`echo "gem source https://u:ghp_5zsMYNabcdefghijklmnopqrstuvwxyz0123@example.com"`},
	}

	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	if err := ex.RunTask(context.Background(), "leak"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}
	ex.Close() // flush the redacting writer

	out := buf.String()
	if strings.Contains(out, "ghp_5zsMYN") {
		t.Errorf("secret leaked in command output:\n%s", out)
	}
	if !strings.Contains(out, masking.Placeholder) {
		t.Errorf("expected redaction in output:\n%s", out)
	}
}

// captureLogs installs a JSON slog handler writing to a buffer for the duration of the test, returning the buffer.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestRunTask_LogModeRoutesRemoteOutput(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	logs := captureLogs(t)
	cfg := newTestConfig(srv, deployTree(t))
	cfg.Log.RawRemoteLog = new(false) // raw_remote_log: false -> route output through the logger

	var raw bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &raw})
	if err := ex.RunTask(context.Background(), "greet"); err != nil {
		t.Fatalf("RunTask: %v\nlogs:\n%s", err, logs.String())
	}
	ex.Close()

	// Nothing command-related on the raw stream: neither the output line nor the echoed command.
	if r := raw.String(); strings.Contains(r, "hello-from-") || strings.Contains(r, "$ echo") {
		t.Fatalf("command output/echo leaked to raw stream in log mode:\n%s", r)
	}
	// The logger got a structured "output" record carrying the host, the running task, and the line.
	got := logs.String()
	for _, want := range []string{
		`"msg":"output"`,
		`"output":"hello-from-` + srv.Host,
		`"task":"greet"`,
		`"host":"` + srv.Host + `"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in log output:\n%s", want, got)
		}
	}
	// The echoed command is logged too (as an "exec" record), not streamed raw.
	if !strings.Contains(got, `"msg":"exec"`) {
		t.Fatalf("echoed command not routed through logger:\n%s", got)
	}
}

func TestRunTask_LogModeRoutesLocalOutput(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	logs := captureLogs(t)
	cfg := newTestConfig(srv, deployTree(t))
	cfg.Log.RawRemoteLog = new(false)

	var raw bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &raw})
	if err := ex.RunTask(context.Background(), "build"); err != nil { // local task: echo building myapp
		t.Fatalf("RunTask: %v\nlogs:\n%s", err, logs.String())
	}
	ex.Close()

	if strings.Contains(raw.String(), "building myapp") {
		t.Fatalf("local output leaked to raw stream in log mode:\n%s", raw.String())
	}
	got := logs.String()
	if !strings.Contains(got, `"output":"building myapp"`) || !strings.Contains(got, `"host":"local"`) {
		t.Fatalf("local output not routed through logger with host=local:\n%s", got)
	}
}

func TestRunTask_LogModeRedactsOutput(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	logs := captureLogs(t)
	cfg := newTestConfig(srv, deployTree(t))
	cfg.Log.RawRemoteLog = new(false)
	cfg.Tasks["leak"] = &ast.Task{
		Roles: []string{"app"},
		Cmds:  []string{`echo "token ghp_5zsMYNabcdefghijklmnopqrstuvwxyz0123"`},
	}

	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: io.Discard})
	if err := ex.RunTask(context.Background(), "leak"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	ex.Close()

	got := logs.String()
	if strings.Contains(got, "ghp_5zsMYN") {
		t.Fatalf("secret leaked through the logger in log mode:\n%s", got)
	}
	if !strings.Contains(got, masking.Placeholder) {
		t.Fatalf("expected redaction in logged output:\n%s", got)
	}
}

func TestRunTask_ColorizesHostPrefix(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	cfg := newTestConfig(srv, deployTree(t))
	cfg.Tasks["noisy"] = &ast.Task{Roles: []string{"app"}, Cmds: []string{"echo out; echo oops >&2"}}

	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf, Color: true})
	if err := ex.RunTask(context.Background(), "noisy"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}
	ex.Close()

	out := buf.String()
	green := "\033[32m[" + srv.Host + "]\033[0m"
	if !strings.Contains(out, green+" $ ") { // the echoed command
		t.Fatalf("echoed command prefix not green:\n%q", out)
	}
	if !strings.Contains(out, green+" out") { // stdout output
		t.Fatalf("stdout prefix not green:\n%q", out)
	}
	if !strings.Contains(out, green+" oops") { // stderr output uses the same (green) host prefix
		t.Fatalf("stderr prefix not green:\n%q", out)
	}
	if strings.Contains(out, "\033[31m") {
		t.Fatalf("no output should be red (stderr is not an error):\n%q", out)
	}
}

// localHostCfg builds a config with one local:true host (cluster path, no SSH server) and the given tasks.
func localHostCfg(t *testing.T, tasks map[string]*ast.Task) *ast.DeployFile {
	t.Helper()
	return &ast.DeployFile{
		App:   ast.App{Name: "app", DeployTo: deployTree(t), Branch: "main"},
		Stage: "test",
		Hosts: []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}},
		Tasks: tasks,
	}
}

func TestRunTask_SilentOutputHiddenOnSuccess(t *testing.T) {
	logs := captureLogs(t)
	cfg := localHostCfg(t, map[string]*ast.Task{
		"quiet": {Roles: []string{"app"}, SilentOutput: true, Cmds: []string{"echo hush-line"}},
	})
	var raw bytes.Buffer
	ex := executor.New(cfg, executor.Options{Out: &raw})
	if err := ex.RunTask(context.Background(), "quiet"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	ex.Close()

	if strings.Contains(raw.String(), "hush-line") {
		t.Fatalf("output not suppressed on success:\n%s", raw.String())
	}
	// silent_output still announces the task (unlike silent).
	if g := logs.String(); !strings.Contains(g, `"msg":"task"`) || !strings.Contains(g, `"name":"quiet"`) {
		t.Fatalf("task not announced:\n%s", g)
	}
}

func TestRunTask_SilentOutputShownOnFailure(t *testing.T) {
	cfg := localHostCfg(t, map[string]*ast.Task{
		"quiet-fail": {Roles: []string{"app"}, SilentOutput: true, Cmds: []string{"echo boom-line; exit 3"}},
	})
	var raw bytes.Buffer
	ex := executor.New(cfg, executor.Options{Out: &raw})
	err := ex.RunTask(context.Background(), "quiet-fail")
	ex.Close()

	if err == nil {
		t.Fatal("expected the failing task to return an error")
	}
	if !strings.Contains(raw.String(), "boom-line") {
		t.Fatalf("captured output not flushed on failure:\n%s", raw.String())
	}
}

func TestRunTask_SilentOutputLogMode(t *testing.T) {
	logs := captureLogs(t)
	cfg := localHostCfg(t, map[string]*ast.Task{
		"ok":  {Roles: []string{"app"}, SilentOutput: true, Cmds: []string{"echo hush-line"}},
		"bad": {Roles: []string{"app"}, SilentOutput: true, Cmds: []string{"echo doom-line; exit 2"}},
	})
	cfg.Log.RawRemoteLog = new(false) // log mode: output normally becomes structured records
	var raw bytes.Buffer
	ex := executor.New(cfg, executor.Options{Out: &raw})
	defer ex.Close()

	// Success: the buffered output record is discarded, not emitted.
	if err := ex.RunTask(context.Background(), "ok"); err != nil {
		t.Fatalf("RunTask ok: %v", err)
	}
	if strings.Contains(logs.String(), "hush-line") {
		t.Fatalf("output emitted on success in log mode:\n%s", logs.String())
	}
	// Failure: the buffered records are replayed through the logger.
	if err := ex.RunTask(context.Background(), "bad"); err == nil {
		t.Fatal("expected failure")
	}
	if g := logs.String(); !strings.Contains(g, `"output":"doom-line"`) {
		t.Fatalf("buffered output not replayed on failure:\n%s", g)
	}
}

func TestRunTask_SilentOutputRedactsFlushedOutput(t *testing.T) {
	cfg := localHostCfg(t, map[string]*ast.Task{
		"leak": {Roles: []string{"app"}, SilentOutput: true,
			Cmds: []string{`echo "token ghp_5zsMYNabcdefghijklmnopqrstuvwxyz0123"; exit 1`}},
	})
	var raw bytes.Buffer
	ex := executor.New(cfg, executor.Options{Out: &raw})
	if err := ex.RunTask(context.Background(), "leak"); err == nil {
		t.Fatal("expected failure")
	}
	ex.Close()

	out := raw.String()
	if strings.Contains(out, "ghp_5zsMYN") {
		t.Fatalf("secret leaked when flushing on failure:\n%s", out)
	}
	if !strings.Contains(out, masking.Placeholder) {
		t.Fatalf("expected redaction in flushed output:\n%s", out)
	}
}

func TestHosts_ExcludesNonDeploy(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: "/tmp/deploy"},
		Stage: "test",
		Hosts: []ast.Host{
			{Address: "keep.example.com", Roles: []string{"app"}},
			{Address: "skip.example.com", Roles: []string{"app"}, Deploy: new(false)},
		},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	defer ex.Close()

	got := ex.Hosts()
	if len(got) != 1 || got[0].Address != "keep.example.com" {
		t.Fatalf("Servers() = %+v, want only keep.example.com", got)
	}
}

func TestRunTask_SkipsNonDeployHost(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: "/tmp/deploy", Branch: "main"},
		Stage: "test",
		Hosts: []ast.Host{
			{Address: "keep.example.com", Roles: []string{"app"}},
			{Address: "skip.example.com", Roles: []string{"app"}, Deploy: new(false)},
		},
		Tasks: map[string]*ast.Task{
			"greet": {Roles: []string{"app"}, Cmds: []string{"echo hi"}},
		},
	}
	var buf bytes.Buffer
	// dry-run so no host is dialed; we only assert which hosts are targeted.
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf, DryRun: true})
	defer ex.Close()
	if err := ex.RunTask(context.Background(), "greet"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "keep.example.com") {
		t.Errorf("deploy-enabled host missing from plan:\n%s", out)
	}
	if strings.Contains(out, "skip.example.com") {
		t.Errorf("deploy:false host should be skipped:\n%s", out)
	}
}

func TestRunTask_Unknown(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	var buf bytes.Buffer
	ex := executor.New(newTestConfig(srv, t.TempDir()), executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	defer ex.Close()
	if err := ex.RunTask(context.Background(), "does-not-exist"); err == nil {
		t.Fatal("expected error for unknown task")
	}
}

// Task commands are echoed (host-prefixed) before they run, so the console and the --log-file transcript show what was
// sent to the server, not just output.
func TestRunTask_EchoesRemoteCommand(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	out := run(t, srv, "greet")
	want := "[" + srv.Host + "] $ echo hello-from-" + srv.Host
	if !strings.Contains(out, want) {
		t.Fatalf("expected echoed command %q, got:\n%s", want, out)
	}
}

func TestRunTask_EchoesLocalCommand(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	out := run(t, srv, "build")
	if !strings.Contains(out, "[local] $ echo building myapp") {
		t.Fatalf("expected local command echo with host prefix, got:\n%s", out)
	}
}

func TestRunTask_LocalOutputHostPrefixed(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	// A local task's output is tagged "[local]" like remote hosts, not emitted bare.
	out := run(t, srv, "build")
	if !strings.Contains(out, "[local] building myapp") {
		t.Fatalf("local task output missing [local] host prefix, got:\n%s", out)
	}
}

func TestRunTask_LocalPrefixColorized(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "app", DeployTo: deployTree(t)},
		Stage: "test",
		Hosts: []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{"hc": {Local: true, Cmds: []string{"echo done"}}},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{Out: &buf, Color: true})
	if err := ex.RunTask(context.Background(), "hc"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}
	ex.Close()

	green := "\033[32m[local]\033[0m"
	out := buf.String()
	if !strings.Contains(out, green+" $ ") { // echoed command
		t.Fatalf("local echo prefix not green:\n%q", out)
	}
	if !strings.Contains(out, green+" done") { // command output
		t.Fatalf("local output prefix not green:\n%q", out)
	}
}

// A value marked sensitive via envSecret is masked in the echoed command (and in the command's output), so echoing the
// command can't leak it.
func TestRunTask_EchoRedactsSensitiveValue(t *testing.T) {
	t.Setenv("WHOOSH_ECHO_SECRET", "supersecret_value_1234")
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: deployTree(t), Branch: "main"},
		Stage: "test",
		Hosts: []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"login": {Local: true, Cmds: []string{`echo configuring {{ envSecret "WHOOSH_ECHO_SECRET" }}`}},
		},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	defer ex.Close()
	if err := ex.RunTask(context.Background(), "login"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}
	if strings.Contains(buf.String(), "supersecret_value_1234") {
		t.Errorf("sensitive value leaked into output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), masking.Placeholder) {
		t.Errorf("expected redaction placeholder, got:\n%s", buf.String())
	}
}

// A non_deploy task targets the inventory-only (deploy:false) hosts, the inverse of normal task targeting, a normal
// task skips them.
// (Local servers stand in for remote ones - the host-prefixed echo confirms which hosts were targeted.)
func TestRunTask_NonDeployTargetsInventoryHosts(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: deployTree(t), Branch: "main"},
		Stage: "test",
		Hosts: []ast.Host{
			{Address: "deploy-host", Local: true, Roles: []string{"app"}},                  // deploy: default true
			{Address: "asg-host", Local: true, Roles: []string{"asg"}, Deploy: new(false)}, // deploy:false
		},
		Tasks: map[string]*ast.Task{
			"normal":  {Cmds: []string{"echo hi"}},
			"oncheck": {NonDeploy: true, Cmds: []string{"echo hi"}},
			"all":     {AllHosts: true, Cmds: []string{"echo hi"}},
		},
	}

	runOn := func(task string) string {
		var buf bytes.Buffer
		ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
		defer ex.Close()
		if err := ex.RunTask(context.Background(), task); err != nil {
			t.Fatalf("RunTask(%q): %v\n%s", task, err, buf.String())
		}
		return buf.String()
	}

	nd := runOn("oncheck")
	if !strings.Contains(nd, "[asg-host] $") {
		t.Errorf("non_deploy task should target asg-host, got:\n%s", nd)
	}
	if strings.Contains(nd, "[deploy-host] $") {
		t.Errorf("non_deploy task should NOT target the deploy host, got:\n%s", nd)
	}

	normal := runOn("normal")
	if !strings.Contains(normal, "[deploy-host] $") {
		t.Errorf("normal task should target deploy-host, got:\n%s", normal)
	}
	if strings.Contains(normal, "[asg-host] $") {
		t.Errorf("normal task should NOT target the deploy:false host, got:\n%s", normal)
	}

	all := runOn("all")
	if !strings.Contains(all, "[deploy-host] $") || !strings.Contains(all, "[asg-host] $") {
		t.Errorf("all_hosts task should target every host, got:\n%s", all)
	}
}

// A task's strict_host_key:false skips known_hosts verification for its SSH connections, while a task without it
// inherits the stage's strict setting and fails when the host isn't known.
// (Fresh executor per run: the cluster pools one connection per host, so a failed strict dial would otherwise be
// cached.)
func TestRunTask_StrictHostKeyOverride(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	kh := filepath.Join(t.TempDir(), "known_hosts") // empty: no host is known
	if err := os.WriteFile(kh, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: deployTree(t), Branch: "main"},
		Stage: "test",
		Hosts: []ast.Host{{
			Address: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: srv.IdentityFile, Roles: []string{"app"},
		}},
		Tasks: map[string]*ast.Task{
			"strict": {Cmds: []string{"echo hi"}},                            // inherits strict -> fails
			"skip":   {StrictHostKey: new(false), Cmds: []string{"echo hi"}}, // override -> connects
		},
	}
	run := func(task string) error {
		var buf bytes.Buffer
		ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: true, KnownHostsFile: kh}, Out: &buf})
		defer ex.Close()
		return ex.RunTask(context.Background(), task)
	}

	if err := run("strict"); err == nil {
		t.Error("strict task should fail handshake against empty known_hosts")
	}
	if err := run("skip"); err != nil {
		t.Errorf("strict_host_key:false task should connect, got: %v", err)
	}
}

// Plugin-injected imports (e.g.
// SSM params) are exposed to tasks both as template values ({{ .ssm.secret }}) and as env vars ($SSM_SECRET /
// $SSM_DB_URL).
func TestRunTask_ExposesImports(t *testing.T) {
	cfg := &ast.DeployFile{
		App:     ast.App{Name: "myapp", DeployTo: deployTree(t), Branch: "main"},
		Stage:   "test",
		Hosts:   []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}},
		Imports: map[string]map[string]string{"ssm": {"secret": "topsecretval", "db-url": "postgres://h/db"}},
		Tasks: map[string]*ast.Task{
			"show": {Local: true, Cmds: []string{`echo "tmpl={{ .ssm.secret }} env=$SSM_SECRET db=$SSM_DB_URL"`}},
		},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
	defer ex.Close()
	if err := ex.RunTask(context.Background(), "show"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "tmpl=topsecretval env=topsecretval db=postgres://h/db") {
		t.Fatalf("imports not exposed in template/env, got:\n%s", buf.String())
	}
}

// runAction binds a plugin.HostFileWriter to the task's hosts, so an action can render a generated file onto them
// (into the release dir for a relative path).
func TestRunAction_HostFileWriter(t *testing.T) {
	reg, err := plugins.Load(nil)
	if err != nil {
		t.Fatalf("plugins.Load: %v", err)
	}
	if err := reg.AddAction("test:writefile", func(ctx context.Context, _ map[string]any, _ io.Writer) error {
		return plugins.HostFileWriterFrom(ctx).WriteFile(ctx, "rendered.env", []byte("K=v\n"))
	}); err != nil {
		t.Fatalf("AddAction: %v", err)
	}

	deployTo := deployTree(t) // has current/
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "a", DeployTo: deployTo, Branch: "main"},
		Stage: "test",
		Hosts: []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{"wf": {Action: "test:writefile", Roles: []string{"app"}}},
	}
	var buf bytes.Buffer
	ex := executor.New(cfg, executor.Options{Out: &buf, Registry: reg, Color: true})
	defer ex.Close()
	if err := ex.RunTask(context.Background(), "wf"); err != nil {
		t.Fatalf("RunTask: %v\n%s", err, buf.String())
	}

	// The action's per-host progress line carries the green host prefix, like command output.
	if out := buf.String(); !strings.Contains(out, "\033[32m[localhost]\033[0m rendering ") {
		t.Fatalf("HostFileWriter progress prefix not green:\n%q", out)
	}

	// Relative path resolved against the release dir (current); written 0600.
	p := filepath.Join(deployTo, "current", "rendered.env")
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("file not rendered on host: %v", err)
	}
	if string(got) != "K=v\n" {
		t.Errorf("content = %q, want %q", got, "K=v\n")
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// continue_on_error turns a host command failure non-fatal: the run completes (the failed host is logged, not aborted).
// Without it, the failure aborts.
func TestRunTask_ContinueOnError(t *testing.T) {
	cfg := &ast.DeployFile{
		App:   ast.App{Name: "myapp", DeployTo: deployTree(t), Branch: "main"},
		Stage: "test",
		Hosts: []ast.Host{{Address: "localhost", Local: true, Roles: []string{"app"}}},
		Tasks: map[string]*ast.Task{
			"boom":      {Roles: []string{"app"}, Cmds: []string{"exit 7"}},
			"boom-cont": {Roles: []string{"app"}, ContinueOnError: true, Cmds: []string{"exit 7"}},
		},
	}
	run := func(task string) error {
		var buf bytes.Buffer
		ex := executor.New(cfg, executor.Options{SSH: ssh.Options{StrictHostKey: false}, Out: &buf})
		defer ex.Close()
		return ex.RunTask(context.Background(), task)
	}
	if err := run("boom"); err == nil {
		t.Error("a failing command should abort without continue_on_error")
	}
	if err := run("boom-cont"); err != nil {
		t.Errorf("continue_on_error should make the failure non-fatal, got %v", err)
	}
}
