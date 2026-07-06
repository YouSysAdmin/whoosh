package sshtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	gssh "github.com/gliderlabs/ssh"
	"golang.org/x/crypto/ssh"
)

// Server is a running in-process SSH server.
type Server struct {
	Host         string // always 127.0.0.1
	Port         int
	IdentityFile string // private key path accepted by the server

	ln  net.Listener
	srv *gssh.Server
	dir string
}

// Option adjusts the server before it starts listening.
type Option func(*config)

type config struct {
	authorizedKeys []gssh.PublicKey
}

// WithAuthorizedKeys restricts public-key auth to these keys. Without it the server accepts any key.
func WithAuthorizedKeys(keys ...gssh.PublicKey) Option {
	return func(c *config) { c.authorizedKeys = append(c.authorizedKeys, keys...) }
}

// Start launches the server on a random localhost port.
func Start(opts ...Option) (*Server, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}

	dir, err := os.MkdirTemp("", "sshtest-")
	if err != nil {
		return nil, err
	}

	keyPath, err := writeClientKey(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	hostSigner, err := newSigner()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	srv := &gssh.Server{
		Handler:          handleSession,
		PublicKeyHandler: authHandler(cfg.authorizedKeys),
		// direct-tcpip lets the server act as a jump host in bastion tests. The callback must allow the
		// forward explicitly - DirectTCPIPHandler rejects every request when it is nil.
		LocalPortForwardingCallback: func(gssh.Context, string, uint32) bool { return true },
		ChannelHandlers: map[string]gssh.ChannelHandler{
			"session":      gssh.DefaultSessionHandler,
			"direct-tcpip": gssh.DirectTCPIPHandler,
		},
	}
	srv.AddHostKey(hostSigner)
	go func() { _ = srv.Serve(ln) }()

	return &Server{
		Host:         "127.0.0.1",
		Port:         ln.Addr().(*net.TCPAddr).Port,
		IdentityFile: keyPath,
		ln:           ln,
		srv:          srv,
		dir:          dir,
	}, nil
}

// Close stops the server and removes its temp files.
func (s *Server) Close() {
	_ = s.srv.Close()
	_ = s.ln.Close()
	_ = os.RemoveAll(s.dir)
}

// authHandler accepts any public key by default, or only the authorized ones when the list is non-empty.
func authHandler(authorized []gssh.PublicKey) gssh.PublicKeyHandler {
	if len(authorized) == 0 {
		return func(gssh.Context, gssh.PublicKey) bool { return true }
	}
	return func(_ gssh.Context, key gssh.PublicKey) bool {
		for _, a := range authorized {
			if gssh.KeysEqual(key, a) {
				return true
			}
		}
		return false
	}
}

// handleSession runs the requested command through the local shell and relays stdout/stderr and the exit code back to
// the client.
func handleSession(s gssh.Session) {
	cmd := exec.Command("/bin/sh", "-c", s.RawCommand())
	cmd.Stdout = s
	cmd.Stderr = s.Stderr()
	err := cmd.Run()
	code := 0
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		code = ee.ExitCode()
	}
	_ = s.Exit(code)
}

// writeClientKey generates an ed25519 key, writes it to dir in OpenSSH PEM form (parseable by the whoosh client), and
// returns its path.
func writeClientKey(dir string) (string, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, "deployer-test")
	if err != nil {
		return "", err
	}
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		return "", err
	}
	return keyPath, nil
}

func newSigner() (ssh.Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return ssh.NewSignerFromKey(priv)
}
