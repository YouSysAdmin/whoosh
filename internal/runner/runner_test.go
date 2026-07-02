package runner_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh/internal/runner"
	"github.com/yousysadmin/whoosh/transport/sshtest"
)

func TestRunCommand_Remote(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start test server: %v", err)
	}
	defer srv.Close()

	targets := []runner.Target{{Host: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: srv.IdentityFile}}

	var buf bytes.Buffer
	results := runner.RunCommand(context.Background(), targets, runner.Options{StrictHostKey: false},
		"echo hello-from-ssh", &buf, false, 0, false)

	if runner.Failed(results) {
		t.Fatalf("command failed: %+v", results)
	}
	if out := buf.String(); !strings.Contains(out, "hello-from-ssh") || !strings.Contains(out, "["+srv.Host+"]") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestRunCommand_Local(t *testing.T) {
	targets := []runner.Target{{Host: "localhost", Local: true}}

	var buf bytes.Buffer
	results := runner.RunCommand(context.Background(), targets, runner.Options{},
		"echo hello-from-local", &buf, false, 0, false)

	if runner.Failed(results) {
		t.Fatalf("local command failed: %+v", results)
	}
	if out := buf.String(); !strings.Contains(out, "hello-from-local") || !strings.Contains(out, "[localhost]") {
		t.Fatalf("unexpected local output: %q", out)
	}
}

func TestRunCommand_LocalNonZeroExitIsError(t *testing.T) {
	targets := []runner.Target{{Host: "localhost", Local: true}}
	var buf bytes.Buffer
	results := runner.RunCommand(context.Background(), targets, runner.Options{}, "exit 4", &buf, false, 0, false)
	if !runner.Failed(results) {
		t.Fatal("expected failure for non-zero exit")
	}
}

func TestHostLabel(t *testing.T) {
	if got := runner.HostLabel("h1", false); got != "[h1]" {
		t.Fatalf("no color: got %q, want [h1]", got)
	}
	if got := runner.HostLabel("h1", true); got != "\033[32m[h1]\033[0m" {
		t.Fatalf("color: got %q, want green-wrapped", got)
	}
}

func TestRunCommand_ColorizesPrefix(t *testing.T) {
	targets := []runner.Target{{Host: "localhost", Local: true}}
	var buf bytes.Buffer
	// The host prefix is green on both streams - it marks the host, not severity (stderr is not "error").
	runner.RunCommand(context.Background(), targets, runner.Options{}, "echo out; echo err >&2", &buf, true, 0, false)
	out := buf.String()
	if !strings.Contains(out, "\033[32m[localhost]\033[0m out") {
		t.Fatalf("stdout prefix not green:\n%q", out)
	}
	if !strings.Contains(out, "\033[32m[localhost]\033[0m err") {
		t.Fatalf("stderr prefix not green:\n%q", out)
	}
	if strings.Contains(out, "\033[31m") {
		t.Fatalf("stderr must not be red:\n%q", out)
	}
}
