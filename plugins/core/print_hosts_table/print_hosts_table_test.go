package print_hosts_table

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh"
)

// The registered name must match the documented one: `plugins: [{name: print-hosts-table, enabled: false}]` only
// disables this default-on plugin when the names agree (DefaultSpecs skips a declared spec by name), and configuring
// it under any other name fails plugins.Load with "unknown plugin".
func TestRegisteredUnderDocumentedName(t *testing.T) {
	if pluginName != "print-hosts-table" {
		t.Fatalf("pluginName = %q, want %q (the documented name)", pluginName, "print-hosts-table")
	}
}

// The plugin reports a version via whoosh.Versioner, shown by `whoosh plugins` / `whoosh version`.
func TestVersion(t *testing.T) {
	var p whoosh.Plugin = &plugin{}
	v, ok := p.(whoosh.Versioner)
	if !ok {
		t.Fatal("plugin does not implement whoosh.Versioner")
	}
	if v.Version() != pluginVersion || pluginVersion == "" {
		t.Fatalf("Version() = %q, want %q", v.Version(), pluginVersion)
	}
}

// The plugin's startup hook registers a func-hook before deploy:starting (and no task), and that func prints the
// resolved hosts table to the console.
func TestInstall_RegistersPrintFuncHook(t *testing.T) {
	reg, err := whoosh.Load([]whoosh.PluginSpec{{Name: pluginName}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := &whoosh.DeployFile{Hosts: []whoosh.Host{
		{Address: "web1.example.com", Roles: []string{"app"}},
		{Address: "db1.example.com", Roles: []string{"db"}},
	}}
	if err := reg.RunStartup(context.Background(), cfg); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}

	// A func-hook is wired before deploy:starting, no echo task is contributed.
	fns := cfg.HookFuncsBefore[whoosh.PhaseStarting]
	if len(fns) != 1 {
		t.Fatalf("before %s func hooks = %d, want 1", whoosh.PhaseStarting, len(fns))
	}
	if len(cfg.Tasks) != 0 {
		t.Errorf("no task should be added, got %v", cfg.Tasks)
	}

	// Invoking it prints the resolved inventory table.
	var buf bytes.Buffer
	if err := fns[0](context.Background(), &buf); err != nil {
		t.Fatalf("hook func: %v", err)
	}
	for _, want := range []string{"web1.example.com", "db1.example.com", "app", "db"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("printed table missing %q in:\n%s", want, buf.String())
		}
	}
}

// The plugin contributes the deploy:hosts CLI command (whoosh.Commander), declared on a bare instance (no Configure),
// and its Run prints the hosts table.
func TestCommands_DeployHosts(t *testing.T) {
	cmds := (&plugin{}).Commands()
	if len(cmds) != 1 || cmds[0].Name != "deploy:hosts" {
		t.Fatalf("Commands() = %+v, want one deploy:hosts", cmds)
	}

	cfg := &whoosh.DeployFile{Hosts: []whoosh.Host{
		{Address: "web1.example.com", Roles: []string{"app"}},
		{Address: "db1.example.com", Roles: []string{"db"}},
	}}
	var buf bytes.Buffer
	if err := cmds[0].Run(context.Background(), cfg, nil, &buf, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"web1.example.com", "db1.example.com", "app", "db"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("deploy:hosts output missing %q in:\n%s", want, buf.String())
		}
	}
}

func TestHostsTable(t *testing.T) {
	out := hostsTable([]whoosh.Host{
		{Address: "10.0.0.1", Roles: []string{"app", "web"}},                                      // no source -> "config"
		{Address: "10.0.0.2", Roles: []string{"db"}, Deploy: new(false)},                          // no source -> "config"
		{Address: "10.0.0.3", Roles: []string{"app"}, Primary: true, Source: "aws:ec2:inventory"}, // discovered, primary
		{Address: "localhost", Local: true},
	})

	for _, want := range []string{"HOST", "ROLES", "DEPLOY", "PRIMARY", "TRANSPORT", "SOURCE", "10.0.0.1", "app,web", "localhost", "local", "aws:ec2:inventory"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q in:\n%s", want, out)
		}
	}
	lineFor := func(host string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, host) {
				return l
			}
		}
		return ""
	}
	if l := lineFor("10.0.0.1"); !strings.Contains(l, "yes") {
		t.Errorf("deploy-enabled row = %q, want 'yes'", l)
	}
	if l := lineFor("10.0.0.2"); !strings.Contains(l, "no") {
		t.Errorf("deploy:false row = %q, want 'no'", l)
	}
	// A host with no Source shows "config"; a discovered host shows its plugin source.
	if l := lineFor("10.0.0.1"); !strings.Contains(l, "config") {
		t.Errorf("config-host row = %q, want 'config' source", l)
	}
	if l := lineFor("10.0.0.3"); !strings.Contains(l, "aws:ec2:inventory") {
		t.Errorf("discovered-host row = %q, want 'aws:ec2:inventory' source", l)
	}
	// The primary-marked host shows "yes" in the PRIMARY column, an unmarked one stays empty there.
	if l := lineFor("10.0.0.3"); !strings.Contains(l, "yes") {
		t.Errorf("primary row = %q, want 'yes'", l)
	}
}
