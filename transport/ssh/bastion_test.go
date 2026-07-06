package ssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/yousysadmin/whoosh/transport/sshtest"
)

// startPair launches two in-process servers acting as the bastion and the target.
func startPair(t *testing.T) (bastion, target *sshtest.Server) {
	t.Helper()
	bastion, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start bastion: %v", err)
	}
	t.Cleanup(bastion.Close)
	target, err = sshtest.Start()
	if err != nil {
		t.Fatalf("start target: %v", err)
	}
	t.Cleanup(target.Close)
	return bastion, target
}

// TestDialThroughBastion covers the happy path: the target is dialed through a direct-tcpip channel on the
// bastion connection and commands run against the target as usual.
func TestDialThroughBastion(t *testing.T) {
	bastionSrv, targetSrv := startPair(t)

	b := NewBastion(Target{Host: bastionSrv.Host, Port: bastionSrv.Port, IdentityFile: bastionSrv.IdentityFile})
	defer b.Close()

	c, err := Dial(context.Background(),
		Target{Host: targetSrv.Host, Port: targetSrv.Port, IdentityFile: targetSrv.IdentityFile},
		Options{Bastion: b})
	if err != nil {
		t.Fatalf("dial through bastion: %v", err)
	}
	defer c.Close()

	var out, errOut bytes.Buffer
	if err := c.Run(context.Background(), "echo tunneled", &out, &errOut); err != nil {
		t.Fatalf("run through bastion: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "tunneled" {
		t.Errorf("output = %q, want %q", got, "tunneled")
	}
}

// TestDialThroughBastion_Concurrent exercises the shared bastion connection: several targets dialed at once
// must all get their own channel over the single lazily-opened bastion client.
func TestDialThroughBastion_Concurrent(t *testing.T) {
	bastionSrv, _ := startPair(t)

	targets := make([]*sshtest.Server, 3)
	for i := range targets {
		srv, err := sshtest.Start()
		if err != nil {
			t.Fatalf("start target %d: %v", i, err)
		}
		t.Cleanup(srv.Close)
		targets[i] = srv
	}

	b := NewBastion(Target{Host: bastionSrv.Host, Port: bastionSrv.Port, IdentityFile: bastionSrv.IdentityFile})
	defer b.Close()

	var wg sync.WaitGroup
	errs := make([]error, len(targets))
	for i, srv := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := Dial(context.Background(),
				Target{Host: srv.Host, Port: srv.Port, IdentityFile: srv.IdentityFile},
				Options{Bastion: b})
			if err != nil {
				errs[i] = err
				return
			}
			defer c.Close()
			var out, errOut bytes.Buffer
			if err := c.Run(context.Background(), fmt.Sprintf("echo host-%d", i), &out, &errOut); err != nil {
				errs[i] = err
				return
			}
			if got, want := strings.TrimSpace(out.String()), fmt.Sprintf("host-%d", i); got != want {
				errs[i] = fmt.Errorf("output = %q, want %q", got, want)
			}
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("target %d: %v", i, err)
		}
	}
}

// TestDialThroughBastion_Unreachable pins the failure labeling and the dial-once cache: every host dialed
// through a dead bastion fails with the bastion named, without re-dialing it.
func TestDialThroughBastion_Unreachable(t *testing.T) {
	// A port that is guaranteed closed: bind a listener and shut it down.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	b := NewBastion(Target{Host: "127.0.0.1", Port: port, IdentityFile: writeTestKey(t)})
	defer b.Close()

	_, err = Dial(context.Background(), Target{Host: "192.0.2.1", IdentityFile: writeTestKey(t)}, Options{Bastion: b})
	if err == nil {
		t.Fatal("dial through a dead bastion succeeded")
	}
	if !strings.Contains(err.Error(), "bastion 127.0.0.1") {
		t.Errorf("error does not name the bastion: %v", err)
	}

	_, err2 := Dial(context.Background(), Target{Host: "192.0.2.2", IdentityFile: writeTestKey(t)}, Options{Bastion: b})
	if err2 == nil {
		t.Fatal("second dial through a dead bastion succeeded")
	}
	if !b.dialed {
		t.Error("bastion not marked dialed after failure")
	}
	if b.err == nil {
		t.Error("bastion failure not cached")
	}
}

// TestDialThroughBastion_AuthRejected labels an auth failure on the hop with the bastion host.
func TestDialThroughBastion_AuthRejected(t *testing.T) {
	// The bastion only authorizes a key the client does not hold.
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := gossh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	bastionSrv, err := sshtest.Start(sshtest.WithAuthorizedKeys(authorized))
	if err != nil {
		t.Fatalf("start bastion: %v", err)
	}
	t.Cleanup(bastionSrv.Close)

	b := NewBastion(Target{Host: bastionSrv.Host, Port: bastionSrv.Port, IdentityFile: writeTestKey(t)})
	defer b.Close()

	_, err = Dial(context.Background(), Target{Host: "192.0.2.1", IdentityFile: writeTestKey(t)}, Options{Bastion: b})
	if err == nil {
		t.Fatal("dial through an auth-rejecting bastion succeeded")
	}
	if !strings.Contains(err.Error(), "bastion "+bastionSrv.Host) {
		t.Errorf("error does not name the bastion: %v", err)
	}
}

// TestBastionClose covers the nil receiver, the never-dialed case, and the double close.
func TestBastionClose(t *testing.T) {
	var nilBastion *Bastion
	if err := nilBastion.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}

	b := NewBastion(Target{Host: "127.0.0.1"})
	if err := b.Close(); err != nil {
		t.Errorf("never-dialed Close: %v", err)
	}

	bastionSrv, targetSrv := startPair(t)
	b = NewBastion(Target{Host: bastionSrv.Host, Port: bastionSrv.Port, IdentityFile: bastionSrv.IdentityFile})
	c, err := Dial(context.Background(),
		Target{Host: targetSrv.Host, Port: targetSrv.Port, IdentityFile: targetSrv.IdentityFile},
		Options{Bastion: b})
	if err != nil {
		t.Fatalf("dial through bastion: %v", err)
	}
	c.Close()
	if err := b.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestDialThroughBastion_HostKeys verifies the bastion hop honors the same host-key options as targets:
// a strict accept_new dial records both the bastion key and the target key.
func TestDialThroughBastion_HostKeys(t *testing.T) {
	bastionSrv, targetSrv := startPair(t)
	khFile := filepath.Join(t.TempDir(), "known_hosts")

	b := NewBastion(Target{Host: bastionSrv.Host, Port: bastionSrv.Port, IdentityFile: bastionSrv.IdentityFile})
	defer b.Close()

	c, err := Dial(context.Background(),
		Target{Host: targetSrv.Host, Port: targetSrv.Port, IdentityFile: targetSrv.IdentityFile},
		Options{Bastion: b, StrictHostKey: true, AcceptNew: true, KnownHostsFile: khFile})
	if err != nil {
		t.Fatalf("strict accept_new dial through bastion: %v", err)
	}
	c.Close()

	data, err := os.ReadFile(khFile)
	if err != nil {
		t.Fatalf("known_hosts not created: %v", err)
	}
	for _, srv := range []*sshtest.Server{bastionSrv, targetSrv} {
		want := knownhosts.Normalize(net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port)))
		if !strings.Contains(string(data), want) {
			t.Errorf("known_hosts %q does not mention %q", data, want)
		}
	}
}
