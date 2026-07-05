package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

func TestConfigCmd_MasksSSHIdentitySecrets(t *testing.T) {
	const passphrase = "idtok_not-a-known-pattern_778899"
	cfgYAML := `version: "1"
app:
  name: myapp
  deploy_to: /srv/myapp
env_files: [ ./dev.env ]
ssh:
  identities:
    app_hosts:
      content: |
        -----BEGIN OPENSSH PRIVATE KEY-----
        FAKEKEYBODY443322
        -----END OPENSSH PRIVATE KEY-----
      passphrase: '{{ envSecret "WHOOSH_TEST_KEY_PASS" }}'
    worker_hosts:
      path: ~/.ssh/id_worker
`
	out, err := runStageCmd(t, cfgYAML, map[string]string{"dev.env": "WHOOSH_TEST_KEY_PASS=" + passphrase + "\n"}, "config")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	for _, secret := range []string{"FAKEKEYBODY443322", passphrase} {
		if strings.Contains(out, secret) {
			t.Errorf("config dump leaks %q:\n%s", secret, out)
		}
	}
	for _, want := range []string{"app_hosts", "worker_hosts", "id_worker", "[MASKED]"} {
		if !strings.Contains(out, want) {
			t.Errorf("config dump should contain %q:\n%s", want, out)
		}
	}
}

func TestValidateCmd_SSHIdentityTemplates(t *testing.T) {
	// A broken passphrase template is a load-time error every command catches offline.
	bad := `version: "1"
app:
  name: myapp
  deploy_to: /srv/myapp
ssh:
  identities:
    app:
      content: FAKE
      passphrase: "{{ .no_such_var }}"
`
	_, err := runStageCmd(t, bad, nil, "validate")
	if err == nil || !strings.Contains(err.Error(), "ssh.identities.app.passphrase") {
		t.Fatalf("undefined var in passphrase should fail validate naming the field, got: %v", err)
	}
}

func TestValidateCmd_SSHIdentitiesStayOffline(t *testing.T) {
	// validate never builds the agent, so a key path that does not exist (yet) must still validate - key files are
	// only read by commands that connect.
	cfg := `version: "1"
app:
  name: myapp
  deploy_to: /srv/myapp
ssh:
  identities:
    later:
      path: /nonexistent/provisioned-at-deploy-time
`
	if _, err := runStageCmd(t, cfg, nil, "validate"); err != nil {
		t.Fatalf("validate should not read identity key files, got: %v", err)
	}
}

func TestConfigCmd_MasksIdentityFilePassphrase(t *testing.T) {
	const passphrase = "iftok_not-a-known-pattern_224466"
	cfgYAML := `version: "1"
app:
  name: myapp
  deploy_to: /srv/myapp
env_files: [ ./dev.env ]
ssh:
  identity_file: ~/.ssh/deploy
  identity_file_passphrase: '{{ envSecret "WHOOSH_TEST_IF_PASS" }}'
hosts:
  - address: db1.example.com
    identity_file: ~/.ssh/db_key
    identity_file_passphrase: hostsecret_882233
`
	out, err := runStageCmd(t, cfgYAML, map[string]string{"dev.env": "WHOOSH_TEST_IF_PASS=" + passphrase + "\n"}, "config")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	for _, secret := range []string{passphrase, "hostsecret_882233"} {
		if strings.Contains(out, secret) {
			t.Errorf("config dump leaks %q:\n%s", secret, out)
		}
	}
	for _, want := range []string{"identity_file_passphrase: '[MASKED]'", "db1.example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("config dump should contain %q:\n%s", want, out)
		}
	}
}

func TestValidateCmd_IdentityFilePassphrase(t *testing.T) {
	// A broken passphrase template is a load-time error caught offline, naming the field.
	bad := `version: "1"
app:
  name: myapp
  deploy_to: /srv/myapp
ssh:
  identity_file: ~/.ssh/deploy
  identity_file_passphrase: "{{ .no_such_var }}"
`
	_, err := runStageCmd(t, bad, nil, "validate")
	if err == nil || !strings.Contains(err.Error(), "ssh.identity_file_passphrase") {
		t.Fatalf("undefined var in passphrase should fail validate naming the field, got: %v", err)
	}

	// A passphrase with nothing to decrypt is dead config.
	orphan := `version: "1"
app:
  name: myapp
  deploy_to: /srv/myapp
ssh:
  identity_file_passphrase: pw
`
	_, err = runStageCmd(t, orphan, nil, "validate")
	if err == nil || !strings.Contains(err.Error(), "requires ssh.identity_file") {
		t.Fatalf("passphrase without identity_file should fail validate, got: %v", err)
	}
}

func TestBuiltinAgent_EncryptedIdentityFilePassphrase(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_enc")
	out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "sesame", "-C", "t", "-f", keyPath, "-q").CombinedOutput()
	if err != nil {
		t.Skipf("ssh-keygen unavailable: %v (%s)", err, out)
	}

	cfg := &ast.DeployFile{}
	cfg.SSH.IdentityFile = keyPath
	cfg.SSH.IdentityFilePassphrase = "sesame"
	if _, err := builtinAgent(cfg); err != nil {
		t.Errorf("encrypted identity_file with correct passphrase should load: %v", err)
	}

	cfg.SSH.IdentityFilePassphrase = ""
	_, err = builtinAgent(cfg)
	if err == nil || !strings.Contains(err.Error(), "set passphrase") {
		t.Errorf("encrypted identity_file without passphrase should be a config error, got: %v", err)
	}
}

func TestBuiltinAgent_NilWithoutIdentities(t *testing.T) {
	ag, err := builtinAgent(&ast.DeployFile{})
	if err != nil || ag != nil {
		t.Fatalf("builtinAgent with no identities = (%v, %v), want (nil, nil)", ag, err)
	}
}

func TestBuiltinAgent_BadKeyIsConfigError(t *testing.T) {
	cfg := &ast.DeployFile{}
	cfg.SSH.Identities = map[string]ast.SSHIdentity{"gone": {Path: "/nonexistent/key"}}
	_, err := builtinAgent(cfg)
	if err == nil || !strings.Contains(err.Error(), `"gone"`) {
		t.Fatalf("builtinAgent error = %v, want a config error naming the identity", err)
	}
}
