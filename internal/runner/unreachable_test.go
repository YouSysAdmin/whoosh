package runner_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/yousysadmin/whoosh/internal/errors"
	"github.com/yousysadmin/whoosh/internal/runner"
	"github.com/yousysadmin/whoosh/transport/sshtest"
)

func TestIsUnreachable(t *testing.T) {
	u := &errors.UnreachableError{Err: errors.New("boom")}
	if !errors.IsUnreachable(u) {
		t.Error("direct UnreachableError not detected")
	}
	if !errors.IsUnreachable(fmt.Errorf("context: %w", u)) {
		t.Error("wrapped UnreachableError not detected")
	}
	if errors.IsUnreachable(errors.New("plain")) {
		t.Error("plain error wrongly classified unreachable")
	}
	if errors.IsUnreachable(nil) {
		t.Error("nil wrongly classified unreachable")
	}
}

// TestRun_DialFailureIsUnreachable: a host that won't accept a connection yields an Unreachable result (the
// on_unreachable policy keys off this).
func TestRun_DialFailureIsUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close() // nothing listening now -> connection refused

	c := runner.NewCluster(runner.Options{StrictHostKey: false}, io.Discard)
	defer c.Close()
	results := c.Run(context.Background(),
		[]runner.Target{{Host: "127.0.0.1", Port: port}},
		func(string) string { return "echo hi" }, 0, false)

	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("expected a dial failure, got %+v", results)
	}
	if !errors.IsUnreachable(results[0].Err) {
		t.Errorf("dial failure should be unreachable, got %v", results[0].Err)
	}
}

// TestRun_CommandExitIsNotUnreachable: a command that runs and exits non-zero is a command failure, not an unreachable
// host - so `skip` never silently tolerates it.
func TestRun_CommandExitIsNotUnreachable(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	c := runner.NewCluster(runner.Options{StrictHostKey: false}, io.Discard)
	defer c.Close()
	results := c.Run(context.Background(),
		[]runner.Target{{Host: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: srv.IdentityFile}},
		func(string) string { return "exit 7" }, 0, false)

	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("expected a non-zero exit, got %+v", results)
	}
	if errors.IsUnreachable(results[0].Err) {
		t.Errorf("a command exit must not be classified unreachable, got %v", results[0].Err)
	}
}
