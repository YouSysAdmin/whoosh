package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// genKeyPEM returns an ed25519 private key in OpenSSH PEM form, encrypted when a passphrase is given.
func genKeyPEM(t *testing.T, passphrase string) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "test")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "test", []byte(passphrase))
	}
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(block)
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewAgent_File(t *testing.T) {
	path := writeFile(t, t.TempDir(), "id_ed25519", genKeyPEM(t, ""))
	ag, err := NewAgent([]Identity{{Name: "a", Path: path}})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	keys, err := ag.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("agent holds %d keys, want 1", len(keys))
	}
	if !strings.HasPrefix(keys[0].Comment, "a:") {
		t.Errorf("key comment = %q, want the identity label prefix", keys[0].Comment)
	}
}

func TestNewAgent_InlineContent(t *testing.T) {
	ag, err := NewAgent([]Identity{{Name: "inline", Content: string(genKeyPEM(t, ""))}})
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if keys, _ := ag.List(); len(keys) != 1 {
		t.Fatalf("agent holds %d keys, want 1", len(keys))
	}
}

func TestNewAgent_EncryptedContent(t *testing.T) {
	enc := string(genKeyPEM(t, "sesame"))

	if _, err := NewAgent([]Identity{{Name: "e", Content: enc, Passphrase: "sesame"}}); err != nil {
		t.Errorf("correct passphrase should load the key: %v", err)
	}
	if _, err := NewAgent([]Identity{{Name: "e", Content: enc, Passphrase: "wrong"}}); err == nil {
		t.Error("wrong passphrase should fail")
	}
	_, err := NewAgent([]Identity{{Name: "e", Content: enc}})
	if err == nil || !strings.Contains(err.Error(), "set passphrase") {
		t.Errorf("missing passphrase error = %v, want it to say 'set passphrase'", err)
	}
	if err != nil && !strings.Contains(err.Error(), `"e"`) {
		t.Errorf("error %v should name the identity", err)
	}
}

func TestNewAgent_EncryptedFileWithoutPassphraseFails(t *testing.T) {
	path := writeFile(t, t.TempDir(), "id_enc", genKeyPEM(t, "sesame"))
	if _, err := NewAgent([]Identity{{Name: "e", Path: path}}); err == nil {
		t.Fatal("encrypted explicit file without passphrase should be a hard error")
	}
}

func TestNewAgent_MissingFile(t *testing.T) {
	_, err := NewAgent([]Identity{{Name: "gone", Path: filepath.Join(t.TempDir(), "nope")}})
	if err == nil || !strings.Contains(err.Error(), `"gone"`) {
		t.Fatalf("error = %v, want a hard error naming the identity", err)
	}
}

func TestNewAgent_Directory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "id_a", genKeyPEM(t, ""))
	writeFile(t, dir, "id_a.pub", []byte("ssh-ed25519 AAAA test"))
	writeFile(t, dir, "known_hosts", []byte("host ssh-ed25519 AAAA"))
	writeFile(t, dir, "config", []byte("Host *"))
	writeFile(t, dir, "junk.txt", []byte("not a key"))
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, sub, "id_b", genKeyPEM(t, ""))

	ag, err := NewAgent([]Identity{{Name: "dir", Path: dir}})
	if err != nil {
		t.Fatalf("NewAgent non-recursive: %v", err)
	}
	if keys, _ := ag.List(); len(keys) != 1 {
		t.Errorf("non-recursive scan loaded %d keys, want 1 (top-level only)", len(keys))
	}

	ag, err = NewAgent([]Identity{{Name: "dir", Path: dir, Recursive: true}})
	if err != nil {
		t.Fatalf("NewAgent recursive: %v", err)
	}
	if keys, _ := ag.List(); len(keys) != 2 {
		t.Errorf("recursive scan loaded %d keys, want 2", len(keys))
	}
}

func TestNewAgent_DirectorySkipsEncrypted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "id_plain", genKeyPEM(t, ""))
	writeFile(t, dir, "id_enc", genKeyPEM(t, "sesame"))

	// No passphrase: the encrypted key is skipped with a warning, never a hard error.
	ag, err := NewAgent([]Identity{{Name: "dir", Path: dir}})
	if err != nil {
		t.Fatalf("scan with an encrypted key should not fail: %v", err)
	}
	if keys, _ := ag.List(); len(keys) != 1 {
		t.Errorf("scan loaded %d keys, want 1 (encrypted one skipped)", len(keys))
	}

	// With the passphrase both keys load.
	ag, err = NewAgent([]Identity{{Name: "dir", Path: dir, Passphrase: "sesame"}})
	if err != nil {
		t.Fatalf("scan with passphrase: %v", err)
	}
	if keys, _ := ag.List(); len(keys) != 2 {
		t.Errorf("scan with passphrase loaded %d keys, want 2", len(keys))
	}
}

func TestNewAgent_NoUsableKeys(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "junk", []byte("not a key"))
	_, err := NewAgent([]Identity{{Name: "dir", Path: dir}})
	if err == nil || !strings.Contains(err.Error(), "no usable private keys") {
		t.Fatalf("error = %v, want the zero-keys error", err)
	}
}

func TestNewAgent_ExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, home, "id_home", genKeyPEM(t, ""))
	ag, err := NewAgent([]Identity{{Name: "h", Path: "~/id_home"}})
	if err != nil {
		t.Fatalf("NewAgent with ~ path: %v", err)
	}
	if keys, _ := ag.List(); len(keys) != 1 {
		t.Errorf("agent holds %d keys, want 1", len(keys))
	}
}

// TestAuthMethods_BuiltinAgentSuppressesSystemAgent pins the decided behavior: with a builtin agent the system
// ssh-agent is not offered, even when a live SSH_AUTH_SOCK exists.
func TestAuthMethods_BuiltinAgentSuppressesSystemAgent(t *testing.T) {
	sockDir, err := os.MkdirTemp("", "ag")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	sock := filepath.Join(sockDir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	t.Setenv("SSH_AUTH_SOCK", sock)

	ag, err := NewAgent([]Identity{{Name: "a", Content: string(genKeyPEM(t, ""))}})
	if err != nil {
		t.Fatal(err)
	}
	methods, err := authMethods("", "", ag)
	if err != nil {
		t.Fatalf("authMethods: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("authMethods offered %d methods, want 1 (builtin agent only)", len(methods))
	}

	// Without the builtin agent the system agent is still picked up.
	methods, err = authMethods("", "", nil)
	if err != nil {
		t.Fatalf("authMethods without builtin agent: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("authMethods offered %d methods, want 1 (system agent)", len(methods))
	}
}

func TestAuthMethods_NoAuthErrorMentionsIdentities(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	_, err := authMethods("", "", nil)
	if err == nil || !strings.Contains(err.Error(), "ssh.identities") {
		t.Fatalf("error = %v, want it to mention ssh.identities", err)
	}
}
