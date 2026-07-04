package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runStageCmd writes a Deployfile (+ a deploy/uat.yml stage file and any extra files) in a temp dir, switches to it,
// and runs `whoosh uat <action>`, returning the command's stdout and Execute's error.
func runStageCmd(t *testing.T, deployfile string, extra map[string]string, action string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Deployfile.yml"), []byte(deployfile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deploy", "uat.yml"), []byte("version: \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range extra {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)

	args := []string{"uat", action}
	root := newRootCmd(args)
	root.SetArgs(args)
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(io.Discard)
	err := root.Execute()
	return out.String(), err
}

// runValidate runs `whoosh uat validate` against the given Deployfile (see runStageCmd).
func runValidate(t *testing.T, deployfile string) error {
	t.Helper()
	_, err := runStageCmd(t, deployfile, nil, "validate")
	return err
}

func TestValidateCmd_GoodConfig(t *testing.T) {
	good := `version: "1"
app:
  name: myapp
  deploy_to: /srv/myapp
tasks:
  greet:
    cmds: [ "echo hi" ]
`
	if err := runValidate(t, good); err != nil {
		t.Fatalf("valid config should pass validate, got: %v", err)
	}
}

func TestValidateCmd_MissingRequiredField(t *testing.T) {
	// app.deploy_to is required - Validate must reject it.
	bad := `version: "1"
app:
  name: myapp
`
	if err := runValidate(t, bad); err == nil {
		t.Fatal("config missing app.deploy_to should fail validate")
	}
}

func TestValidateCmd_TemplatedVars(t *testing.T) {
	// Vars are rendered at load, so validate exercises them offline: env/envSecret resolve through env_files.
	good := `version: "1"
app:
  name: myapp
  deploy_to: /srv/myapp
env_files: [ ./dev.env ]
vars:
  app_version: '{{ env "WHOOSH_TEST_VALIDATE_VERSION" }}'
tasks:
  greet:
    cmds: [ "echo {{ .app_version }}" ]
`
	if _, err := runStageCmd(t, good, map[string]string{"dev.env": "WHOOSH_TEST_VALIDATE_VERSION=9.9.9\n"}, "validate"); err != nil {
		t.Fatalf("templated vars from env_files should pass validate, got: %v", err)
	}

	// A broken var template (single-quoted arg) is now a load-time error validate catches.
	bad := strings.Replace(good, `'{{ env "WHOOSH_TEST_VALIDATE_VERSION" }}'`, `"{{ env 'WHOOSH_TEST_VALIDATE_VERSION' }}"`, 1)
	if _, err := runStageCmd(t, bad, nil, "validate"); err == nil {
		t.Fatal("single-quoted template arg in vars should fail validate")
	}
}

func TestValidateCmd_TemplateCheck(t *testing.T) {
	base := `version: "1"
app:
  name: myapp
  deploy_to: /srv/myapp
%s
`
	cases := []struct {
		name    string
		body    string
		extra   map[string]string
		wantErr string // substring of the error; "" = must pass
	}{
		{
			name: "unquoted env arg in envs is caught",
			body: "envs:\n  RAILS_ENV: '{{ env RAILS_ENV }}'",
			// Go parses the bare identifier as a function name.
			wantErr: "envs.RAILS_ENV",
		},
		{
			name:    "required guard on an unset env var is caught",
			body:    "envs:\n  APP_VERSION: '{{ required \"APP_VERSION must be exported\" (env \"APP_VERSION\") }}'",
			wantErr: "APP_VERSION must be exported",
		},
		{
			name:  "required guard satisfied by env_files passes",
			body:  "env_files: [ ./dev.env ]\nenvs:\n  APP_VERSION: '{{ required \"nope\" (env \"APP_VERSION\") }}'",
			extra: map[string]string{"dev.env": "APP_VERSION=1.2.3\n"},
		},
		{
			name: "run-time keys pass leniently",
			body: "tasks:\n  t:\n    cmds: [ 'echo {{ .release_path }} {{ .host }} {{ .tasks.whoami.Account }}' ]",
		},
		{
			name:    "bad cmd template is caught",
			body:    "tasks:\n  t:\n    cmds: [ 'echo {{ .x' ]",
			wantErr: `task "t" cmds[0]`,
		},
		{
			name:    "bad inline script is caught",
			body:    "tasks:\n  t:\n    scripts:\n      - name: s\n        script: '{{ env BAD }}'",
			wantErr: `task "t" script s`,
		},
		{
			name:    "missing script file is caught",
			body:    "tasks:\n  t:\n    scripts:\n      - path: nope.sh",
			wantErr: `task "t" script nope.sh`,
		},
		{
			name: "task inactive for the stage is skipped",
			body: "tasks:\n  t:\n    except: [ uat ]\n    cmds: [ 'echo {{ env BROKEN }}' ]",
		},
		{
			// Task output exists only at run time - a required guard on it must not fail the offline check.
			name: "required guard on task output is skipped",
			body: "tasks:\n  t:\n    cmds: [ 'deploy {{ required \"no ami\" .tasks.build.ami }}' ]",
		},
		{
			name:    "bad with param is caught with its key",
			body:    "tasks:\n  t:\n    action: systemd:restart\n    with:\n      name: '{{ env BAD }}'",
			wantErr: `task "t" with.name`,
		},
		{
			name: "with param using task output is skipped",
			body: "tasks:\n  t:\n    action: systemd:restart\n    with:\n      name: '{{ required \"x\" .tasks.build.name }}'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runStageCmd(t, fmt.Sprintf(base, tc.body), tc.extra, "validate")
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected validate to pass, got: %v\n%s", err, out)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validate to fail")
			}
			// Findings are printed as a plain list on stdout; the error carries only the count.
			if !strings.Contains(out, tc.wantErr) {
				t.Fatalf("findings should mention %q, got:\n%s", tc.wantErr, out)
			}
		})
	}
}

// The template check runs at the end of config load (loadConfig), so EVERY command - deploy, deploy:check, and a
// plain task run alike - fails up front on a config mistake, before any host is touched.
func TestTemplateCheck_RunsOnEveryCommand(t *testing.T) {
	// 192.0.2.1 is TEST-NET: if the check didn't abort first, the run would hang on the SSH connect timeout.
	broken := `version: "1"
app:
  name: myapp
  deploy_to: /srv/myapp
  repo: git@example.com:org/app.git
hosts:
  - address: 192.0.2.1
    roles: [ app ]
envs:
  APP_VERSION: '{{ required "APP_VERSION must be exported" (env "APP_VERSION") }}'
tasks:
  greet:
    roles: [ app ]
    cmds: [ "echo hi" ]
`
	for _, action := range []string{"deploy", "deploy:check", "greet"} {
		out, err := runStageCmd(t, broken, nil, action)
		if err == nil {
			t.Errorf("%s should fail the template check", action)
		}
		if !strings.Contains(out, "APP_VERSION must be exported") {
			t.Errorf("%s should print the findings, got:\n%s", action, out)
		}
	}
}

// The check runs BEFORE plugins load - plugin Configure/startup may dial SSH/cloud (credentials, inventory), and a
// config mistake must fail without any of that. A declared-but-unregistered plugin only errors at plugins.Load, so
// the template error surfacing first proves the ordering.
func TestTemplateCheck_BeforePluginLoad(t *testing.T) {
	broken := `version: "1"
app:
  name: myapp
  deploy_to: /srv/myapp
plugins:
  - name: not-compiled-in
envs:
  APP_VERSION: '{{ required "APP_VERSION must be exported" (env "APP_VERSION") }}'
tasks:
  greet:
    local: true
    cmds: [ "echo hi" ]
`
	out, err := runStageCmd(t, broken, nil, "greet")
	if err == nil || !strings.Contains(err.Error(), "template check") {
		t.Fatalf("want the template-check error before the unknown-plugin error, got: %v", err)
	}
	if !strings.Contains(out, "APP_VERSION must be exported") {
		t.Fatalf("findings should be printed, got:\n%s", out)
	}
}

func TestConfigCmd_MasksSecretVars(t *testing.T) {
	const secret = "cfgtok_not-a-known-pattern_445566"
	cfgYAML := `version: "1"
app:
  name: myapp
  deploy_to: /srv/myapp
env_files: [ ./dev.env ]
vars:
  webhook: '{{ envSecret "WHOOSH_TEST_CONFIG_SECRET" }}'
`
	out, err := runStageCmd(t, cfgYAML, map[string]string{"dev.env": "WHOOSH_TEST_CONFIG_SECRET=" + secret + "\n"}, "config")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if strings.Contains(out, secret) {
		t.Errorf("config dump leaks an envSecret var value:\n%s", out)
	}
	if !strings.Contains(out, "webhook") {
		t.Errorf("config dump should still list the var key:\n%s", out)
	}
}

func TestValidateCmd_BadTaskShape(t *testing.T) {
	// action cannot be combined with cmds.
	bad := `version: "1"
app:
  name: myapp
  deploy_to: /srv/myapp
tasks:
  oops:
    action: aws:ec2:asg:refresh
    cmds: [ "echo no" ]
`
	if err := runValidate(t, bad); err == nil {
		t.Fatal("task combining action with cmds should fail validate")
	}
}
