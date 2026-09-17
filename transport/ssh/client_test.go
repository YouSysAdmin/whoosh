package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// timeoutErr is a net.Error reporting a timeout, as the net layer produces for an i/o timeout.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return false }

func TestConnectError(t *testing.T) {
	addr := &net.TCPAddr{IP: net.IPv4(10, 4, 20, 66), Port: 22}

	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "timeout collapses to a concise message",
			err:  &net.OpError{Op: "dial", Net: "tcp", Addr: addr, Err: timeoutErr{}},
			want: "connection timed out: i/o timeout",
		},
		{
			name: "other op errors drop the dial-addr prefix",
			err:  &net.OpError{Op: "dial", Net: "tcp", Addr: addr, Err: errors.New("connect: connection refused")},
			want: "connect: connection refused",
		},
		{
			name: "non-net errors pass through",
			err:  errors.New("boom"),
			want: "boom",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := connectError(c.err).Error()
			if got != c.want {
				t.Errorf("connectError = %q, want %q", got, c.want)
			}
		})
	}
}

// deadlineTimeoutErr mimics the net layer's mapping of a context deadline abort: a timeout error whose chain still
// reaches context.DeadlineExceeded.
type deadlineTimeoutErr struct{}

func (deadlineTimeoutErr) Error() string   { return "i/o timeout" }
func (deadlineTimeoutErr) Timeout() bool   { return true }
func (deadlineTimeoutErr) Temporary() bool { return false }
func (deadlineTimeoutErr) Unwrap() error   { return context.DeadlineExceeded }

// TestConnectError_KeepsDeadlineIdentity pins the Bastion.connect contract: a dial aborted by an expired context must
// stay recognizable as context.DeadlineExceeded through connectError, or the abort would be cached as a permanent
// bastion failure.
func TestConnectError_KeepsDeadlineIdentity(t *testing.T) {
	addr := &net.TCPAddr{IP: net.IPv4(10, 4, 20, 66), Port: 22}
	err := connectError(&net.OpError{Op: "dial", Net: "tcp", Addr: addr, Err: deadlineTimeoutErr{}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("connectError lost context.DeadlineExceeded: %v", err)
	}
}

// writeTestKey writes an unencrypted ed25519 private key (PKCS#8 PEM) and returns its path.
func writeTestKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path
}

func TestKeyringFromFile(t *testing.T) {
	path := writeTestKey(t)
	kr, err := keyringFromFile(path)
	if err != nil {
		t.Fatalf("keyringFromFile: %v", err)
	}
	keys, err := kr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("keyring holds %d keys, want 1", len(keys))
	}
}

func TestKeyringFromFile_BadPath(t *testing.T) {
	if _, err := keyringFromFile(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for missing key file")
	}
}

func TestKeyringFromFile_NotAKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk")
	if err := os.WriteFile(path, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := keyringFromFile(path); err == nil {
		t.Fatal("expected parse error for non-key file")
	}
}

func TestLocalAgentSocket_Missing(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	if _, err := localAgentSocket(); err == nil {
		t.Fatal("expected error when SSH_AUTH_SOCK is unset")
	}
}

func TestLocalAgentSocket_Present(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	got, err := localAgentSocket()
	if err != nil {
		t.Fatalf("localAgentSocket: %v", err)
	}
	if got != "/tmp/agent.sock" {
		t.Errorf("socket = %q, want /tmp/agent.sock", got)
	}
}

// stalledListener accepts TCP connections and never speaks, simulating a host whose sshd is wedged.
func stalledListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	return ln
}

func TestDial_HandshakeTimeout(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	ln := stalledListener(t)
	addr := ln.Addr().(*net.TCPAddr)

	start := time.Now()
	_, err := Dial(t.Context(),
		Target{Host: addr.IP.String(), Port: addr.Port, User: "test", IdentityFile: writeTestKey(t)},
		Options{ConnectTimeout: 200 * time.Millisecond, KeepaliveInterval: -1})
	if err == nil {
		t.Fatal("expected handshake timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error = %q, want a timeout mention", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Dial took %s, the handshake was not bounded", elapsed)
	}
}

func TestDial_HandshakeCtxCancel(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	ln := stalledListener(t)
	addr := ln.Addr().(*net.TCPAddr)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_, err := Dial(ctx,
		Target{Host: addr.IP.String(), Port: addr.Port, User: "test", IdentityFile: writeTestKey(t)},
		Options{ConnectTimeout: time.Minute, KeepaliveInterval: -1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
