package plugins

import (
	"context"
	"testing"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

func TestDefaultSpecs(t *testing.T) {
	RegisterDefault("test-default-on", func() Plugin { return nil })

	// An unlisted default-on plugins gets a bare spec appended; declared plugins are preserved in order.
	got := DefaultSpecs([]ast.PluginSpec{{Name: "declared"}})
	if got[0].Name != "declared" {
		t.Fatalf("declared spec not preserved first: %v", specNames(got))
	}
	if !containsSpec(got, "test-default-on") {
		t.Fatalf("default-on plugins not appended: %v", specNames(got))
	}

	// A default plugins the Deployfile lists (e.g. to disable it) is NOT duplicated, and its spec is kept verbatim so
	// enabled:false is honored.
	got = DefaultSpecs([]ast.PluginSpec{{Name: "test-default-on", Enabled: new(false)}})
	n := 0
	for _, s := range got {
		if s.Name == "test-default-on" {
			n++
			if s.Enabled == nil || *s.Enabled {
				t.Errorf("listed spec not preserved: %+v", s)
			}
		}
	}
	if n != 1 {
		t.Errorf("test-default-on appears %d times, want 1", n)
	}
}

type versionedTestPlugin struct{}

func (versionedTestPlugin) Configure(ast.PluginSpec, *Registry) error { return nil }
func (versionedTestPlugin) Version() string                           { return "9.9.9" }

type plainTestPlugin struct{}

func (plainTestPlugin) Configure(ast.PluginSpec, *Registry) error { return nil }

// RegisteredInfo reports each plugin's version via the optional Versioner interface (empty when not implemented),
// sorted by name.
func TestRegisteredInfo(t *testing.T) {
	Register("zzz-versioned-test", func() Plugin { return versionedTestPlugin{} })
	Register("aaa-plain-test", func() Plugin { return plainTestPlugin{} })

	infos := RegisteredInfo()
	got := map[string]string{}
	var names []string
	for _, p := range infos {
		got[p.Name] = p.Version
		names = append(names, p.Name)
	}
	if got["zzz-versioned-test"] != "9.9.9" {
		t.Errorf("versioned plugin: got %q, want 9.9.9", got["zzz-versioned-test"])
	}
	if v, ok := got["aaa-plain-test"]; !ok || v != "" {
		t.Errorf("plain plugin: got %q (present=%v), want an empty version", v, ok)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("RegisteredInfo not sorted by name: %v", names)
		}
	}
}

type ctxTestRunner struct{ cmds []string }

func (r *ctxTestRunner) RunCommand(_ context.Context, cmd string) error {
	r.cmds = append(r.cmds, cmd)
	return nil
}

// The HostCommandRunner rides the action's ctx like the HostFileWriter: nil when absent (an action invoked outside the
// executor), the carried value otherwise.
func TestHostCommandRunnerCtx(t *testing.T) {
	if got := HostCommandRunnerFrom(context.Background()); got != nil {
		t.Fatalf("bare ctx: got %v, want nil", got)
	}
	r := &ctxTestRunner{}
	ctx := WithHostCommandRunner(context.Background(), r)
	got := HostCommandRunnerFrom(ctx)
	if got == nil {
		t.Fatal("runner not carried by ctx")
	}
	if err := got.RunCommand(ctx, "systemctl start app"); err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if len(r.cmds) != 1 || r.cmds[0] != "systemctl start app" {
		t.Fatalf("carried runner not invoked: %v", r.cmds)
	}
}

func containsSpec(specs []ast.PluginSpec, name string) bool {
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}

func specNames(specs []ast.PluginSpec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.Name
	}
	return out
}
