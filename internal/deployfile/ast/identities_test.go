package ast

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMerge_SSHIdentitiesPerKeyReplace(t *testing.T) {
	base := &DeployFile{SSH: SSH{Identities: map[string]SSHIdentity{
		"shared": {Path: "~/.ssh/base_key"},
		"kept":   {Path: "~/.ssh/kept_key"},
	}}}
	ov := &DeployFile{SSH: SSH{Identities: map[string]SSHIdentity{
		"shared": {Content: "PEM", Passphrase: "pw"},
		"extra":  {Path: "~/.ssh/extra_key"},
	}}}
	out := Merge(base, ov)
	ids := out.SSH.Identities
	if len(ids) != 3 {
		t.Fatalf("merged identities = %v, want 3 entries", ids)
	}
	// An override key replaces the base's whole value, not a field-level merge.
	if got := ids["shared"]; got.Content != "PEM" || got.Path != "" {
		t.Errorf("shared = %+v, want the override value verbatim", got)
	}
	if ids["kept"].Path != "~/.ssh/kept_key" || ids["extra"].Path != "~/.ssh/extra_key" {
		t.Errorf("kept/extra entries wrong: %v", ids)
	}
}

func TestMerge_SSHIdentitiesUnsetOverrideKeepsBase(t *testing.T) {
	base := &DeployFile{SSH: SSH{Identities: map[string]SSHIdentity{"a": {Path: "p"}}}}
	out := Merge(base, &DeployFile{})
	if len(out.SSH.Identities) != 1 || out.SSH.Identities["a"].Path != "p" {
		t.Fatalf("merged identities = %v, want base preserved", out.SSH.Identities)
	}
}

func TestValidate_SSHIdentities(t *testing.T) {
	base := DeployFile{App: App{Name: "a", DeployTo: "/srv/a"}, Version: "1"}
	cases := []struct {
		name    string
		id      SSHIdentity
		wantErr string
	}{
		{"path only", SSHIdentity{Path: "~/.ssh/id_rsa"}, ""},
		{"content only", SSHIdentity{Content: "PEM"}, ""},
		{"both", SSHIdentity{Path: "p", Content: "PEM"}, "exactly one"},
		{"neither", SSHIdentity{Passphrase: "pw"}, "exactly one"},
		{"recursive with content", SSHIdentity{Content: "PEM", Recursive: true}, "'recursive' requires 'path'"},
		{"recursive with path", SSHIdentity{Path: "~/.ssh", Recursive: true}, ""},
	}
	for _, c := range cases {
		cfg := base
		cfg.SSH.Identities = map[string]SSHIdentity{"x": c.id}
		err := cfg.Validate()
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: error = %v, want it to mention %q", c.name, err, c.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), "ssh.identities.x") {
			t.Errorf("%s: error %v should name the identity", c.name, err)
		}
	}
}

func TestSSHIdentity_MarshalYAMLRedactsSecrets(t *testing.T) {
	cfg := DeployFile{SSH: SSH{Identities: map[string]SSHIdentity{
		"app": {Content: "-----BEGIN OPENSSH PRIVATE KEY-----\nsecretbody\n-----END OPENSSH PRIVATE KEY-----", Passphrase: "hunter2"},
		"dir": {Path: "~/.ssh", Recursive: true},
	}}}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dump := string(data)
	for _, secret := range []string{"secretbody", "hunter2"} {
		if strings.Contains(dump, secret) {
			t.Errorf("config dump leaks %q:\n%s", secret, dump)
		}
	}
	for _, want := range []string{"app", "dir", "[MASKED]", "recursive: true", "path: ~/.ssh"} {
		if !strings.Contains(dump, want) {
			t.Errorf("config dump missing %q:\n%s", want, dump)
		}
	}
}
