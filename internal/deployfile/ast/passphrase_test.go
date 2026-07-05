package ast

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMerge_SSHIdentityFilePassphrase(t *testing.T) {
	base := &DeployFile{SSH: SSH{IdentityFile: "~/.ssh/base", IdentityFilePassphrase: "basepw"}}
	if out := Merge(base, &DeployFile{}); out.SSH.IdentityFilePassphrase != "basepw" {
		t.Errorf("unset override should keep the base passphrase, got %q", out.SSH.IdentityFilePassphrase)
	}
	ov := &DeployFile{SSH: SSH{IdentityFilePassphrase: "stagepw"}}
	if out := Merge(base, ov); out.SSH.IdentityFilePassphrase != "stagepw" {
		t.Errorf("override passphrase should win, got %q", out.SSH.IdentityFilePassphrase)
	}
}

func TestApplyDefaults_PassphraseInheritedWithIdentityFile(t *testing.T) {
	cfg := &DeployFile{
		SSH: SSH{IdentityFile: "~/.ssh/global", IdentityFilePassphrase: "globalpw"},
		Hosts: []Host{
			{Address: "inherits"},
			{Address: "own-key", IdentityFile: "~/.ssh/own"},
			{Address: "own-pass", IdentityFilePassphrase: "ownpw"},
			{Address: "localhost", Local: true},
		},
	}
	cfg.ApplyDefaults()

	if h := cfg.Hosts[0]; h.IdentityFile != "~/.ssh/global" || h.IdentityFilePassphrase != "globalpw" {
		t.Errorf("host without identity_file should inherit file and passphrase together, got %+v", h)
	}
	// The passphrase belongs to its key: a host with its own identity_file must not get the global passphrase.
	if h := cfg.Hosts[1]; h.IdentityFilePassphrase != "" {
		t.Errorf("host with its own identity_file inherited the global passphrase: %q", h.IdentityFilePassphrase)
	}
	if h := cfg.Hosts[2]; h.IdentityFile != "~/.ssh/global" || h.IdentityFilePassphrase != "ownpw" {
		t.Errorf("host with its own passphrase should keep it while inheriting the file, got %+v", h)
	}
	if h := cfg.Hosts[3]; h.IdentityFile != "" || h.IdentityFilePassphrase != "" {
		t.Errorf("local host should not inherit SSH settings, got %+v", h)
	}

	// Idempotent across the double run, including a host appended between runs (dynamic inventory).
	cfg.Hosts = append(cfg.Hosts, Host{Address: "late"})
	cfg.ApplyDefaults()
	if h := cfg.Hosts[1]; h.IdentityFilePassphrase != "" {
		t.Errorf("second run leaked the global passphrase onto an own-key host: %q", h.IdentityFilePassphrase)
	}
	if h := cfg.Hosts[4]; h.IdentityFile != "~/.ssh/global" || h.IdentityFilePassphrase != "globalpw" {
		t.Errorf("host appended before the second run should inherit like the first, got %+v", h)
	}
}

func TestValidate_IdentityFilePassphrase(t *testing.T) {
	base := DeployFile{App: App{Name: "a", DeployTo: "/srv/a"}, Version: "1"}

	c := base
	c.SSH.IdentityFilePassphrase = "pw"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "requires ssh.identity_file") {
		t.Errorf("global passphrase without identity_file should fail validate, got: %v", err)
	}

	c = base
	c.SSH = SSH{IdentityFile: "~/.ssh/key", IdentityFilePassphrase: "pw"}
	if err := c.Validate(); err != nil {
		t.Errorf("passphrase with identity_file should validate, got: %v", err)
	}

	c = base
	c.Hosts = []Host{{Address: "h1", IdentityFilePassphrase: "pw"}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "hosts[0].identity_file_passphrase") {
		t.Errorf("host passphrase without any identity_file should fail validate, got: %v", err)
	}

	// The global identity_file makes a host passphrase valid: ApplyDefaults (which runs before Validate in the load
	// pipeline) copies the file onto the host.
	c = base
	c.SSH.IdentityFile = "~/.ssh/key"
	c.Hosts = []Host{{Address: "h1", IdentityFilePassphrase: "pw"}}
	c.ApplyDefaults()
	if err := c.Validate(); err != nil {
		t.Errorf("host passphrase with an inherited identity_file should validate, got: %v", err)
	}
}

func TestMarshalYAML_RedactsIdentityFilePassphrase(t *testing.T) {
	cfg := DeployFile{
		SSH: SSH{
			User:                   "deploy",
			IdentityFile:           "~/.ssh/global",
			IdentityFilePassphrase: "globalhunter2",
			Identities:             map[string]SSHIdentity{"a": {Path: "~/.ssh/id_a", Passphrase: "idhunter2"}},
		},
		Hosts: []Host{{Address: "db1", Roles: []string{"db"}, IdentityFile: "~/.ssh/db", IdentityFilePassphrase: "hosthunter2"}},
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dump := string(data)
	for _, secret := range []string{"globalhunter2", "hosthunter2", "idhunter2"} {
		if strings.Contains(dump, secret) {
			t.Errorf("config dump leaks %q:\n%s", secret, dump)
		}
	}
	// The redaction must not eat the neighbors.
	for _, want := range []string{"user: deploy", "identity_file: ~/.ssh/global", "address: db1", "db", "[MASKED]", "path: ~/.ssh/id_a"} {
		if !strings.Contains(dump, want) {
			t.Errorf("config dump missing %q:\n%s", want, dump)
		}
	}
}
