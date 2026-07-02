package runner_test

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/yousysadmin/whoosh/internal/runner"
	"github.com/yousysadmin/whoosh/transport/sshtest"
)

// TestCluster_Capture runs a command on a local target and returns its trimmed stdout, the path the deploy lifecycle
// uses to read the commit SHA off a host.
func TestCluster_Capture(t *testing.T) {
	c := runner.NewCluster(runner.Options{}, io.Discard)
	defer c.Close()

	got, err := c.Capture(context.Background(), runner.Target{Host: "local", Local: true}, "echo '  deadbeef  '")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if got != "deadbeef" {
		t.Fatalf("Capture = %q, want %q (trimmed)", got, "deadbeef")
	}
}

// TestCluster_CloseAfterFailedDial guards against a regression where a failed SSH dial left a typed-nil *ssh.Client in
// the Conn interface, so Close() passed its non-nil check and then panicked dereferencing the nil connection.
func TestCluster_CloseAfterFailedDial(t *testing.T) {
	// Reserve a port, then release it so nothing is listening - dialing it gives a fast "connection refused".
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	c := runner.NewCluster(runner.Options{StrictHostKey: false}, io.Discard)
	targets := []runner.Target{{Host: "127.0.0.1", Port: port}}

	results := c.Run(context.Background(), targets, func(string) string { return "echo hi" }, 0, false)
	if !runner.Failed(results) {
		t.Fatalf("expected a dial failure, got %+v", results)
	}

	// Must not panic for a connection that never established.
	c.Close()
}

// A per-target StrictHostKey override flips host-key verification for that dial only: against an empty known_hosts a
// strict dial is rejected, but a target that overrides StrictHostKey:false connects.
// (Two clusters, since a cluster caches one connection per host for its lifetime.)
func TestCluster_PerTargetHostKeyOverride(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	kh := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(kh, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	opts := runner.Options{StrictHostKey: true, KnownHostsFile: kh}
	base := runner.Target{Host: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: srv.IdentityFile}

	// Strict (target inherits the cluster setting): host unknown -> handshake fails.
	strict := runner.NewCluster(opts, io.Discard)
	defer strict.Close()
	if res := strict.Run(context.Background(), []runner.Target{base}, func(string) string { return "true" }, 0, true); !runner.Failed(res) {
		t.Fatal("strict dial against empty known_hosts should fail")
	}

	// Same options, but the target overrides StrictHostKey:false -> connects.
	skip := base
	skip.StrictHostKey = new(false)
	override := runner.NewCluster(opts, io.Discard)
	defer override.Close()
	if res := override.Run(context.Background(), []runner.Target{skip}, func(string) string { return "true" }, 0, true); runner.Failed(res) {
		t.Fatalf("per-target StrictHostKey:false should connect, got: %+v", res)
	}
}

// A strict target must not reuse a pooled connection an earlier task opened with verification disabled: the pool is
// keyed by host + effective strictness, so the strict run performs its own (failing) verification instead of silently
// riding the unverified connection.
func TestCluster_StrictDoesNotReuseInsecureConn(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	kh := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(kh, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	opts := runner.Options{StrictHostKey: true, KnownHostsFile: kh}
	base := runner.Target{Host: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: srv.IdentityFile}

	c := runner.NewCluster(opts, io.Discard)
	defer c.Close()

	// First task skips verification and connects.
	skip := base
	skip.StrictHostKey = new(false)
	if res := c.Run(context.Background(), []runner.Target{skip}, func(string) string { return "true" }, 0, true); runner.Failed(res) {
		t.Fatalf("insecure dial should connect, got: %+v", res)
	}

	// A later strict task on the same host must dial (and verify) itself - against an empty known_hosts that fails.
	if res := c.Run(context.Background(), []runner.Target{base}, func(string) string { return "true" }, 0, true); !runner.Failed(res) {
		t.Fatal("strict target reused the unverified pooled connection")
	}
}
