package ssh_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh/transport/ssh"
	"github.com/yousysadmin/whoosh/transport/sshtest"
)

func writeKey(t *testing.T) string {
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

// TestForwarding_CommandRunsWhenHostIgnoresRequest guards the regression where a host that doesn't honor agent
// forwarding made every command fail with "forwarding request denied".
// The request is now fire-and-forget, so the command runs regardless.
// The in-process test server ignores the forwarding request, mirroring a host with AllowAgentForwarding off.
func TestForwarding_CommandRunsWhenHostIgnoresRequest(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	conn, err := ssh.Dial(context.Background(), ssh.Target{
		Host: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: srv.IdentityFile,
	}, ssh.Options{StrictHostKey: false, ForwardKey: writeKey(t)})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	var stdout, stderr bytes.Buffer
	if err := conn.Run(context.Background(), "echo hi", &stdout, &stderr); err != nil {
		t.Fatalf("Run with forwarding enabled should succeed even if host ignores it: %v", err)
	}
	if !strings.Contains(stdout.String(), "hi") {
		t.Errorf("stdout = %q, want it to contain %q", stdout.String(), "hi")
	}
}
