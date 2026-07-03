package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/yousysadmin/whoosh/transport/sshtest"
)

// TestDialAcceptNew covers the trust-on-first-use path end to end: a strict dial with accept_new against a known_hosts
// path whose file (and parent dir) don't exist yet must connect, create the file, and record the host key - and that
// recorded entry must then satisfy a plain strict dial (accept_new off).
func TestDialAcceptNew(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()
	target := Target{Host: srv.Host, Port: srv.Port, IdentityFile: srv.IdentityFile}
	khFile := filepath.Join(t.TempDir(), "fresh", "known_hosts")

	c, err := Dial(context.Background(), target, Options{StrictHostKey: true, AcceptNew: true, KnownHostsFile: khFile})
	if err != nil {
		t.Fatalf("first dial with accept_new: %v", err)
	}
	c.Close()

	data, err := os.ReadFile(khFile)
	if err != nil {
		t.Fatalf("known_hosts not created: %v", err)
	}
	wantHost := knownhosts.Normalize(net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port)))
	if !strings.Contains(string(data), wantHost) {
		t.Fatalf("known_hosts %q does not mention %q", data, wantHost)
	}

	// The recorded entry verifies the host in plain strict mode.
	c2, err := Dial(context.Background(), target, Options{StrictHostKey: true, KnownHostsFile: khFile})
	if err != nil {
		t.Fatalf("strict re-dial against recorded key: %v", err)
	}
	c2.Close()
}

// TestDialAcceptNew_ChangedKeyFails pins the security guarantee: accept_new only trusts unknown hosts - a key that
// conflicts with an existing known_hosts entry is rejected, never overwritten.
func TestDialAcceptNew_ChangedKeyFails(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()
	target := Target{Host: srv.Host, Port: srv.Port, IdentityFile: srv.IdentityFile}

	// A known_hosts entry for the server's host:port carrying a different key.
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	otherKey, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap key: %v", err)
	}
	addr := knownhosts.Normalize(net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port)))
	khFile := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(khFile, []byte(knownhosts.Line([]string{addr}, otherKey)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Dial(context.Background(), target, Options{StrictHostKey: true, AcceptNew: true, KnownHostsFile: khFile})
	if err == nil {
		t.Fatal("dial succeeded against a conflicting recorded host key")
	}
	before, _ := os.ReadFile(khFile)
	if strings.Count(string(before), "\n") != 1 {
		t.Errorf("known_hosts gained entries on a conflicting key:\n%s", before)
	}
}

// TestDialStrict_MissingKnownHostsStillFails pins the default: without accept_new, strict mode keeps requiring an
// existing known_hosts file.
func TestDialStrict_MissingKnownHostsStillFails(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()
	target := Target{Host: srv.Host, Port: srv.Port, IdentityFile: srv.IdentityFile}
	khFile := filepath.Join(t.TempDir(), "known_hosts")

	_, err = Dial(context.Background(), target, Options{StrictHostKey: true, KnownHostsFile: khFile})
	if err == nil {
		t.Fatal("dial succeeded with a missing known_hosts and accept_new off")
	}
	if !strings.Contains(err.Error(), "known_hosts") {
		t.Errorf("error should name known_hosts, got: %v", err)
	}
	if _, statErr := os.Stat(khFile); statErr == nil {
		t.Error("known_hosts file was created without accept_new")
	}
}
