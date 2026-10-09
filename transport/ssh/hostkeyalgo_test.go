package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// The algorithms offered to the server follow the key types known_hosts records for the host.
func TestKnownHostAlgorithms(t *testing.T) {
	edPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	edKey, err := ssh.NewPublicKey(edPub)
	if err != nil {
		t.Fatal(err)
	}

	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	rsaKey, err := ssh.NewPublicKey(&rsaPriv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	khFile := filepath.Join(t.TempDir(), "known_hosts")
	lines := knownhosts.Line([]string{"ed.example"}, edKey) + "\n" + knownhosts.Line([]string{"rsa.example"}, rsaKey) + "\n"
	if err := os.WriteFile(khFile, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}

	cb, err := knownhosts.New(khFile)
	if err != nil {
		t.Fatal(err)
	}

	remote := &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 22}
	cases := map[string][]string{
		"ed.example:22":    {ssh.KeyAlgoED25519},
		"rsa.example:22":   {ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA},
		"other.example:22": nil,
	}
	for host, want := range cases {
		if got := knownHostAlgorithms(cb, host, remote); !slices.Equal(got, want) {
			t.Errorf("knownHostAlgorithms(%s) = %v, want %v", host, got, want)
		}
	}

	if got := knownHostAlgorithms(nil, "ed.example:22", remote); got != nil {
		t.Errorf("without known_hosts = %v, want nil", got)
	}
}
