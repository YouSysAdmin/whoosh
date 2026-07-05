package ssh

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Agent aliases the ssh-agent interface, so consumers configure Options.Agent without importing x/crypto directly.
type Agent = agent.Agent

// maxKeyFileSize bounds what a directory scan reads per file - private keys are a few KiB, anything bigger is not one.
const maxKeyFileSize = 1 << 20

// Identity describes one private-key source for NewAgent.
type Identity struct {
	Name       string // label used in logs and error messages only
	Path       string // key file or directory (a leading ~/ expands to the home dir), exclusive with Content
	Content    string // inline PEM, exclusive with Path
	Passphrase string // optional, decrypts an encrypted key
	Recursive  bool   // descend into subdirectories when Path is a directory
}

// NewAgent builds an in-memory ssh-agent keyring holding all keys resolved from the identities.
// A key that fails to load from an explicit file or inline content is a hard error naming the identity. A directory
// scan skips unusable files instead (non-keys quietly, encrypted keys it cannot decrypt with a warning).
// The keyring is safe for concurrent use and for agent.ForwardToAgent.
func NewAgent(ids []Identity) (agent.Agent, error) {
	kr := agent.NewKeyring()
	total := 0
	for _, id := range ids {
		n, err := addIdentity(kr, id)
		if err != nil {
			return nil, err
		}
		slog.Debug("ssh identity loaded", "identity", id.Name, "keys", n)
		total += n
	}
	if total == 0 {
		return nil, fmt.Errorf("ssh identities: no usable private keys found")
	}
	return kr, nil
}

// addIdentity resolves one identity into the keyring and returns how many keys it contributed.
func addIdentity(kr agent.Agent, id Identity) (int, error) {
	if id.Content != "" {
		key, err := parsePrivateKey([]byte(id.Content), id.Passphrase)
		if err != nil {
			return 0, fmt.Errorf("ssh identity %q: %w", id.Name, err)
		}
		if err := addKey(kr, id.Name, "inline", key); err != nil {
			return 0, fmt.Errorf("ssh identity %q: %w", id.Name, err)
		}
		return 1, nil
	}

	path := expandHome(id.Path)
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("ssh identity %q: %w", id.Name, err)
	}
	if info.IsDir() {
		return addDir(kr, id, path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("ssh identity %q: %w", id.Name, err)
	}
	key, err := parsePrivateKey(raw, id.Passphrase)
	if err != nil {
		return 0, fmt.Errorf("ssh identity %q: %s: %w", id.Name, id.Path, err)
	}
	if err := addKey(kr, id.Name, path, key); err != nil {
		return 0, fmt.Errorf("ssh identity %q: %s: %w", id.Name, id.Path, err)
	}
	return 1, nil
}

// addDir loads every usable private key under dir. Obvious non-key files and anything that fails to parse are skipped
// with a debug log, an encrypted key the passphrase does not open is skipped with a warning - a scan never hard-fails
// on file content.
func addDir(kr agent.Agent, id Identity, dir string) (int, error) {
	count := 0
	loadFile := func(path string) {
		name := filepath.Base(path)
		if skipKeyFileName(name) {
			return
		}
		// Stat resolves symlinks, so a symlink to a key file still loads while sockets and other oddities are skipped.
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxKeyFileSize {
			slog.Debug("ssh identity scan: skipping non-key file", "identity", id.Name, "file", path)
			return
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			slog.Debug("ssh identity scan: skipping unreadable file", "identity", id.Name, "file", path, "error", err)
			return
		}
		key, err := parsePrivateKey(raw, id.Passphrase)
		if err != nil {
			if isEncryptedKeyError(err) {
				slog.Warn("ssh identity scan: skipping encrypted key", "identity", id.Name, "file", path)
			} else {
				slog.Debug("ssh identity scan: skipping non-key file", "identity", id.Name, "file", path)
			}
			return
		}
		if err := addKey(kr, id.Name, path, key); err != nil {
			slog.Warn("ssh identity scan: skipping key", "identity", id.Name, "file", path, "error", err)
			return
		}
		count++
	}

	if id.Recursive {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				loadFile(path)
			}
			return nil
		})
		if err != nil {
			return 0, fmt.Errorf("ssh identity %q: scan %s: %w", id.Name, id.Path, err)
		}
		return count, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("ssh identity %q: scan %s: %w", id.Name, id.Path, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			loadFile(filepath.Join(dir, e.Name()))
		}
	}
	return count, nil
}

// skipKeyFileName filters files that live next to keys but are never private keys themselves.
func skipKeyFileName(name string) bool {
	return strings.HasSuffix(name, ".pub") ||
		name == "config" ||
		strings.HasPrefix(name, "known_hosts") ||
		strings.HasPrefix(name, "authorized_keys")
}

// errEncryptedKey tags parse failures caused by encryption - a missing or wrong passphrase - so a directory scan can
// tell "this is a key we could not open" (worth a warning) apart from "this is not a key at all".
var errEncryptedKey = errors.New("key is encrypted")

// parsePrivateKey parses a PEM private key, decrypting it with the passphrase when needed.
func parsePrivateKey(raw []byte, passphrase string) (any, error) {
	key, err := ssh.ParseRawPrivateKey(raw)
	if err == nil {
		return key, nil
	}
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return nil, err
	}
	if passphrase == "" {
		return nil, fmt.Errorf("%w, set passphrase", errEncryptedKey)
	}
	key, err = ssh.ParseRawPrivateKeyWithPassphrase(raw, []byte(passphrase))
	if err != nil {
		return nil, fmt.Errorf("%w, decrypt failed: %v", errEncryptedKey, err)
	}
	return key, nil
}

// isEncryptedKeyError reports whether a parse failure means "the key is encrypted and we could not open it".
func isEncryptedKeyError(err error) bool {
	return errors.Is(err, errEncryptedKey)
}

// addKey registers a parsed key in the keyring, labeled identity:source for `ssh-add -l`-style listings on the far
// side of agent forwarding.
func addKey(kr agent.Agent, name, source string, key any) error {
	return kr.Add(agent.AddedKey{PrivateKey: key, Comment: name + ":" + source})
}
