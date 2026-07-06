package ast

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMerge_SSHBastion(t *testing.T) {
	base := &DeployFile{SSH: SSH{Bastion: &Bastion{Address: "jump.base", User: "base"}}}

	if out := Merge(base, &DeployFile{}); out.SSH.Bastion == nil || out.SSH.Bastion.Address != "jump.base" {
		t.Errorf("unset override should keep the base bastion, got %+v", out.SSH.Bastion)
	}

	// Pointer-wins: the stage bastion replaces the base one wholesale, unset fields do not inherit.
	ov := &DeployFile{SSH: SSH{Bastion: &Bastion{Address: "jump.stage"}}}
	out := Merge(base, ov)
	if out.SSH.Bastion.Address != "jump.stage" {
		t.Errorf("override bastion should win, got %+v", out.SSH.Bastion)
	}
	if out.SSH.Bastion.User != "" {
		t.Errorf("override bastion should replace wholesale, kept base user %q", out.SSH.Bastion.User)
	}

	// A stage without a bastion on top of a base without one stays nil.
	if out := Merge(&DeployFile{}, &DeployFile{}); out.SSH.Bastion != nil {
		t.Errorf("merge invented a bastion: %+v", out.SSH.Bastion)
	}
}

func TestValidate_Bastion(t *testing.T) {
	base := DeployFile{App: App{Name: "a", DeployTo: "/srv/a"}, Version: "1"}

	c := base
	c.SSH.Bastion = &Bastion{User: "jump"}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "ssh.bastion.address is required") {
		t.Errorf("bastion without address should fail validate, got: %v", err)
	}

	c = base
	c.SSH.Bastion = &Bastion{Address: "jump.example.com", IdentityFilePassphrase: "pw"}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "ssh.bastion.identity_file_passphrase") {
		t.Errorf("bastion passphrase without identity_file should fail validate, got: %v", err)
	}

	c = base
	c.SSH.Bastion = &Bastion{Address: "jump.example.com", IdentityFile: "~/.ssh/jump", IdentityFilePassphrase: "pw"}
	if err := c.Validate(); err != nil {
		t.Errorf("complete bastion should validate, got: %v", err)
	}
}

func TestMarshalYAML_RedactsBastionPassphrase(t *testing.T) {
	cfg := DeployFile{
		SSH: SSH{Bastion: &Bastion{Address: "jump.example.com", User: "jump", IdentityFile: "~/.ssh/jump", IdentityFilePassphrase: "jumphunter2"}},
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dump := string(data)
	if strings.Contains(dump, "jumphunter2") {
		t.Errorf("config dump leaks the bastion passphrase:\n%s", dump)
	}
	for _, want := range []string{"address: jump.example.com", "user: jump", "identity_file: ~/.ssh/jump", "[MASKED]"} {
		if !strings.Contains(dump, want) {
			t.Errorf("config dump missing %q:\n%s", want, dump)
		}
	}
}
