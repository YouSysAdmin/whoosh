package ssh_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh/transport/ssh"
	"github.com/yousysadmin/whoosh/transport/sshtest"
	gossh "golang.org/x/crypto/ssh"
)

// genAgentKey returns a builtin agent holding one fresh ed25519 key, plus that key's public half.
func genAgentKey(t *testing.T) (gossh.PublicKey, *bytes.Buffer) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := gossh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	sshPub, err := gossh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	return sshPub, bytes.NewBuffer(pem.EncodeToMemory(block))
}

// TestDialBuiltinAgent proves the builtin agent authenticates on its own: the server only accepts the agent's key,
// there is no identity file on the target and no system agent.
func TestDialBuiltinAgent(t *testing.T) {
	pub, keyPEM := genAgentKey(t)
	ag, err := ssh.NewAgent([]ssh.Identity{{Name: "e2e", Content: keyPEM.String()}})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	srv, err := sshtest.Start(sshtest.WithAuthorizedKeys(pub))
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	t.Setenv("SSH_AUTH_SOCK", "")
	conn, err := ssh.Dial(context.Background(), ssh.Target{
		Host: srv.Host, Port: srv.Port, User: "deploy",
	}, ssh.Options{StrictHostKey: false, Agent: ag})
	if err != nil {
		t.Fatalf("dial with builtin agent: %v", err)
	}
	defer conn.Close()

	var stdout, stderr bytes.Buffer
	if err := conn.Run(context.Background(), "echo hi", &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stdout.String(), "hi") {
		t.Errorf("stdout = %q, want it to contain %q", stdout.String(), "hi")
	}
}

// TestDialBuiltinAgent_WrongKeyRejected proves WithAuthorizedKeys actually rejects: an agent holding a different key
// fails the handshake.
func TestDialBuiltinAgent_WrongKeyRejected(t *testing.T) {
	authorizedPub, _ := genAgentKey(t)
	_, otherKeyPEM := genAgentKey(t)
	ag, err := ssh.NewAgent([]ssh.Identity{{Name: "other", Content: otherKeyPEM.String()}})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	srv, err := sshtest.Start(sshtest.WithAuthorizedKeys(authorizedPub))
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	t.Setenv("SSH_AUTH_SOCK", "")
	conn, err := ssh.Dial(context.Background(), ssh.Target{
		Host: srv.Host, Port: srv.Port, User: "deploy",
	}, ssh.Options{StrictHostKey: false, Agent: ag})
	if err == nil {
		conn.Close()
		t.Fatal("dial with an unauthorized key should fail the handshake")
	}
}

// TestForwarding_BuiltinAgentWithoutSystemSocket pins the new behavior: forward_agent with a builtin agent needs no
// SSH_AUTH_SOCK - the builtin keyring itself is forwarded.
func TestForwarding_BuiltinAgentWithoutSystemSocket(t *testing.T) {
	pub, keyPEM := genAgentKey(t)
	ag, err := ssh.NewAgent([]ssh.Identity{{Name: "fwd", Content: keyPEM.String()}})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	srv, err := sshtest.Start(sshtest.WithAuthorizedKeys(pub))
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	t.Setenv("SSH_AUTH_SOCK", "")
	conn, err := ssh.Dial(context.Background(), ssh.Target{
		Host: srv.Host, Port: srv.Port, User: "deploy",
	}, ssh.Options{StrictHostKey: false, Agent: ag, ForwardAgent: true})
	if err != nil {
		t.Fatalf("dial with forward_agent and builtin agent should not need SSH_AUTH_SOCK: %v", err)
	}
	defer conn.Close()

	var stdout, stderr bytes.Buffer
	if err := conn.Run(context.Background(), "echo hi", &stdout, &stderr); err != nil {
		t.Fatalf("Run with forwarding enabled: %v", err)
	}
}

// TestDialEncryptedIdentityFile proves an encrypted per-target identity file authenticates when Target.Passphrase
// decrypts it, and fails with a clear message when the passphrase is missing or wrong.
func TestDialEncryptedIdentityFile(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := gossh.MarshalPrivateKeyWithPassphrase(priv, "test", []byte("sesame"))
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_enc")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	sshPub, err := gossh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	srv, err := sshtest.Start(sshtest.WithAuthorizedKeys(sshPub))
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	t.Setenv("SSH_AUTH_SOCK", "")
	conn, err := ssh.Dial(context.Background(), ssh.Target{
		Host: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: keyPath, Passphrase: "sesame",
	}, ssh.Options{StrictHostKey: false})
	if err != nil {
		t.Fatalf("dial with encrypted identity file and passphrase: %v", err)
	}
	defer conn.Close()
	var stdout, stderr bytes.Buffer
	if err := conn.Run(context.Background(), "echo hi", &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v", err)
	}

	_, err = ssh.Dial(context.Background(), ssh.Target{
		Host: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: keyPath,
	}, ssh.Options{StrictHostKey: false})
	if err == nil || !strings.Contains(err.Error(), "set passphrase") {
		t.Errorf("missing passphrase error = %v, want it to say 'set passphrase'", err)
	}

	_, err = ssh.Dial(context.Background(), ssh.Target{
		Host: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: keyPath, Passphrase: "wrong",
	}, ssh.Options{StrictHostKey: false})
	if err == nil || !strings.Contains(err.Error(), "decrypt failed") {
		t.Errorf("wrong passphrase error = %v, want it to say 'decrypt failed'", err)
	}
}

// TestDialUnencryptedIdentityWithStrayPassphrase pins that a passphrase on an unencrypted key is ignored.
func TestDialUnencryptedIdentityWithStrayPassphrase(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	conn, err := ssh.Dial(context.Background(), ssh.Target{
		Host: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: srv.IdentityFile, Passphrase: "stray",
	}, ssh.Options{StrictHostKey: false})
	if err != nil {
		t.Fatalf("stray passphrase on an unencrypted key should be ignored: %v", err)
	}
	_ = conn.Close()
}

// TestForwarding_ForwardKeyBeatsBuiltinAgent pins the precedence order: a configured forward_key is used even when a
// builtin agent is present, so a broken forward_key surfaces instead of being silently shadowed.
func TestForwarding_ForwardKeyBeatsBuiltinAgent(t *testing.T) {
	pub, keyPEM := genAgentKey(t)
	ag, err := ssh.NewAgent([]ssh.Identity{{Name: "fwd", Content: keyPEM.String()}})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	srv, err := sshtest.Start(sshtest.WithAuthorizedKeys(pub))
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	conn, err := ssh.Dial(context.Background(), ssh.Target{
		Host: srv.Host, Port: srv.Port, User: "deploy",
	}, ssh.Options{StrictHostKey: false, Agent: ag, ForwardKey: "/nonexistent/forward_key"})
	if err == nil {
		conn.Close()
		t.Fatal("a broken forward_key should fail the dial, not fall back to the builtin agent")
	}
	if !strings.Contains(err.Error(), "forward_key") {
		t.Errorf("error = %v, want the forward_key failure", err)
	}
}
