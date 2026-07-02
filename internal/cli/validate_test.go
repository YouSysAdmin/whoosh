package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// runValidate writes a Deployfile (+ a deploy/uat.yml stage file) in a temp dir, switches to it, and runs `whoosh uat
// validate`, returning Execute's error.
func runValidate(t *testing.T, deployfile string) error {
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
	t.Chdir(dir)

	args := []string{"uat", "validate"}
	root := newRootCmd(args)
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return root.Execute()
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
