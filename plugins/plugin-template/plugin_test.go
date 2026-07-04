package plugintemplate

// Tests for every extension point, all SSH-free: startup features run against a hand-built *whoosh.DeployFile, and
// actions run against fake host bridges. Keep this file green as you replace the TODOs - it is your wiring safety
// net while the plugin grows.

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh"
)

// load configures the plugin from spec and returns the populated registry.
func load(t *testing.T, spec whoosh.PluginSpec) *whoosh.Registry {
	t.Helper()
	reg, err := whoosh.Load([]whoosh.PluginSpec{spec})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return reg
}

// startup runs the registered startup hooks against a fresh config and returns it.
func startup(t *testing.T, reg *whoosh.Registry) *whoosh.DeployFile {
	t.Helper()
	cfg := &whoosh.DeployFile{Stage: "test"}
	if err := reg.RunStartup(context.Background(), cfg); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}
	return cfg
}

// fakeRunner / fakeWriter stand in for the executor-supplied host bridges.
type fakeRunner struct{ cmds []string }

func (f *fakeRunner) RunCommand(_ context.Context, cmd string) error {
	f.cmds = append(f.cmds, cmd)
	return nil
}

type fakeWriter struct{ files map[string]string }

func (f *fakeWriter) WriteFile(_ context.Context, path string, content []byte) error {
	if f.files == nil {
		f.files = map[string]string{}
	}
	f.files[path] = string(content)
	return nil
}

// bridgedCtx returns a ctx carrying both fakes, like the executor does for a real action task.
func bridgedCtx(r *fakeRunner, w *fakeWriter) context.Context {
	ctx := whoosh.WithHostCommandRunner(context.Background(), r)
	return whoosh.WithHostFileWriter(ctx, w)
}

func TestRegisteredUnderDocumentedName(t *testing.T) {
	if !whoosh.IsRegistered(pluginName) {
		t.Fatalf("plugin %q not registered", pluginName)
	}
}

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

// Configure validates the whole spec offline - this is what `whoosh validate` exercises.
func TestConfigure_Validation(t *testing.T) {
	cases := []struct {
		name    string
		spec    whoosh.PluginSpec
		wantErr string
	}{
		{
			name:    "unknown feature",
			spec:    whoosh.PluginSpec{Name: pluginName, Actions: []whoosh.PluginActionSpec{{Name: pluginName + ":nope"}}},
			wantErr: "unknown action",
		},
		{
			name:    "context without keys",
			spec:    whoosh.PluginSpec{Name: pluginName, Actions: []whoosh.PluginActionSpec{{Name: FeatureContext}}},
			wantErr: "'keys' is required",
		},
		{
			name: "setup bad when",
			spec: whoosh.PluginSpec{Name: pluginName, Actions: []whoosh.PluginActionSpec{{
				Name: FeatureSetup, Params: map[string]any{"when": "sometimes"},
			}}},
			wantErr: "when must be",
		},
		{
			name: "setup custom_phase excludes phase",
			spec: whoosh.PluginSpec{Name: pluginName, Actions: []whoosh.PluginActionSpec{{
				Name: FeatureSetup, Params: map[string]any{"custom_phase": "x:warmup", "phase": whoosh.PhaseUpdated},
			}}},
			wantErr: "mutually exclusive",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := whoosh.Load([]whoosh.PluginSpec{tc.spec})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

// The global token is registered as a secret at Configure time, so whoosh masks it everywhere.
func TestConfigure_TokenIsMasked(t *testing.T) {
	load(t, whoosh.PluginSpec{Name: pluginName, Params: map[string]any{"token": "supersecret-token-1"}})
	if got := whoosh.Masking("x supersecret-token-1 y"); strings.Contains(got, "supersecret-token-1") {
		t.Fatalf("token not masked: %q", got)
	}
}

// render: fetch once operator-side, mask the value, write it on the hosts via the file-writer bridge.
// The actions: entry supplies defaults, with: wins.
func TestAction_Render(t *testing.T) {
	reg := load(t, whoosh.PluginSpec{
		Name:   pluginName,
		Params: map[string]any{"endpoint": "https://api.example.com"},
		Actions: []whoosh.PluginActionSpec{{
			Name:   ActionRender,
			Params: map[string]any{"path": ".env.generated"},
		}},
	})
	fn, ok := reg.Action(ActionRender)
	if !ok {
		t.Fatalf("action %q not registered", ActionRender)
	}

	w := &fakeWriter{}
	var out bytes.Buffer
	if err := fn(bridgedCtx(&fakeRunner{}, w), map[string]any{"key": "db_url"}, &out); err != nil {
		t.Fatalf("render: %v", err)
	}
	content, ok := w.files[".env.generated"] // path came from the actions: defaults
	if !ok || !strings.Contains(content, "db_url") {
		t.Fatalf("files = %v", w.files)
	}
	if got := whoosh.Masking(content); strings.Contains(got, strings.TrimSpace(content)) {
		t.Errorf("fetched value not masked: %q", got)
	}

	// Required params and the missing-bridge case fail with clear errors.
	if err := fn(bridgedCtx(&fakeRunner{}, w), nil, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "required") {
		t.Errorf("missing key: err = %v", err)
	}
	if err := fn(context.Background(), map[string]any{"key": "k", "path": "p"}, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "no host file writer") {
		t.Errorf("no writer: err = %v", err)
	}
}

// exec: run a command on the task's hosts via the command-runner bridge.
func TestAction_Exec(t *testing.T) {
	reg := load(t, whoosh.PluginSpec{Name: pluginName})
	fn, ok := reg.Action(ActionExec)
	if !ok {
		t.Fatalf("action %q not registered", ActionExec)
	}

	r := &fakeRunner{}
	if err := fn(bridgedCtx(r, &fakeWriter{}), map[string]any{"cmd": "uname -a"}, io.Discard); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(r.cmds) != 1 || r.cmds[0] != "uname -a" {
		t.Fatalf("runner cmds = %v", r.cmds)
	}

	if err := fn(bridgedCtx(r, &fakeWriter{}), nil, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "'cmd' is required") {
		t.Errorf("missing cmd: err = %v", err)
	}
	if err := fn(context.Background(), map[string]any{"cmd": "true"}, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "no host command runner") {
		t.Errorf("no runner: err = %v", err)
	}
}

// inventory: the startup appends the discovered hosts with the configured shape and our Source stamp.
func TestStartup_Inventory(t *testing.T) {
	off := false
	reg := load(t, whoosh.PluginSpec{
		Name:   pluginName,
		Params: map[string]any{"endpoint": "https://api.example.com"},
		Actions: []whoosh.PluginActionSpec{{
			Name:   FeatureInventory,
			Params: map[string]any{"roles": []string{"app"}, "user": "deploy", "deploy": false},
		}},
	})
	cfg := startup(t, reg)

	if len(cfg.Hosts) == 0 {
		t.Fatal("no hosts discovered")
	}
	for _, h := range cfg.Hosts {
		if h.Source != FeatureInventory {
			t.Errorf("host %s source = %q, want %q", h.Address, h.Source, FeatureInventory)
		}
		if len(h.Roles) != 1 || h.Roles[0] != "app" || h.User != "deploy" {
			t.Errorf("host shape not applied: %+v", h)
		}
		if h.Deploy == nil || *h.Deploy != off {
			t.Errorf("host %s deploy flag not applied: %+v", h.Address, h.Deploy)
		}
	}

	// Without an endpoint the feature fails at startup with a clear error.
	reg = load(t, whoosh.PluginSpec{
		Name:    pluginName,
		Actions: []whoosh.PluginActionSpec{{Name: FeatureInventory}},
	})
	if err := reg.RunStartup(context.Background(), &whoosh.DeployFile{}); err == nil ||
		!strings.Contains(err.Error(), "endpoint is not configured") {
		t.Errorf("no endpoint: err = %v", err)
	}
}

// context: the startup fetches each key, injects it under the namespace, and masks the value.
func TestStartup_Context(t *testing.T) {
	reg := load(t, whoosh.PluginSpec{
		Name:   pluginName,
		Params: map[string]any{"endpoint": "https://api.example.com"},
		Actions: []whoosh.PluginActionSpec{{
			Name:   FeatureContext,
			Params: map[string]any{"keys": []string{"db_url"}, "namespace": "tpl"},
		}},
	})
	cfg := startup(t, reg)

	val := cfg.Imports["tpl"]["db_url"]
	if val == "" {
		t.Fatalf("import not injected: %v", cfg.Imports)
	}
	if got := whoosh.Masking(val); strings.Contains(got, val) {
		t.Errorf("imported value not masked: %q", got)
	}
}

// setup: the startup contributes the script task and wires it - hook by default, custom phase on request - plus the
// two func-hooks.
func TestStartup_Setup(t *testing.T) {
	reg := load(t, whoosh.PluginSpec{
		Name: pluginName,
		Actions: []whoosh.PluginActionSpec{{
			Name:   FeatureSetup,
			Params: map[string]any{"when": "after", "roles": []string{"app"}, "envs": map[string]string{"EXTRA": "1"}},
		}},
	})
	cfg := startup(t, reg)

	task := cfg.Tasks[taskSetup]
	if task == nil || len(task.Scripts) != 1 || task.Scripts[0].Script == "" {
		t.Fatalf("setup task missing or scriptless: %+v", task)
	}
	if task.Envs["EXTRA"] != "1" {
		t.Errorf("user env not merged: %v", task.Envs)
	}
	if _, ok := task.Envs["PLUGIN_TEMPLATE_ENDPOINT"]; !ok {
		t.Errorf("plugin control var not set: %v", task.Envs)
	}
	if got := cfg.Hooks.After[whoosh.PhaseUpdated]; len(got) != 1 || got[0] != taskSetup {
		t.Errorf("after %s hooks = %v", whoosh.PhaseUpdated, got)
	}
	if len(cfg.HookFuncsBefore[whoosh.PhaseStarting]) != 1 || len(cfg.HookFuncsAfter[whoosh.PhaseFinished]) != 1 {
		t.Errorf("func-hooks not registered: before=%d after=%d",
			len(cfg.HookFuncsBefore[whoosh.PhaseStarting]), len(cfg.HookFuncsAfter[whoosh.PhaseFinished]))
	}

	// custom_phase wiring replaces the hook with a spliced phase running the task.
	reg = load(t, whoosh.PluginSpec{
		Name: pluginName,
		Actions: []whoosh.PluginActionSpec{{
			Name:   FeatureSetup,
			Params: map[string]any{"custom_phase": "template:warmup"},
		}},
	})
	cfg = startup(t, reg)
	if len(cfg.CustomPhases) != 1 || cfg.CustomPhases[0].Name != "template:warmup" ||
		cfg.CustomPhases[0].Task != taskSetup || cfg.CustomPhases[0].After != whoosh.PhasePublished {
		t.Errorf("custom phase = %+v", cfg.CustomPhases)
	}
	if len(cfg.Hooks.Before)+len(cfg.Hooks.After) != 0 {
		t.Errorf("custom_phase should not also wire hooks: %+v", cfg.Hooks)
	}
}

// The CLI command reads the resolved config (it runs on a bare instance - no Configure state).
func TestCommands_Status(t *testing.T) {
	cmds := (&plugin{}).Commands()
	if len(cmds) != 1 || cmds[0].Name != pluginName+":status" {
		t.Fatalf("Commands() = %+v, want one %s:status", cmds, pluginName)
	}
	cfg := &whoosh.DeployFile{
		Stage: "prod",
		Hosts: []whoosh.Host{
			{Address: "h1", Source: whoosh.HostSourceConfig},
			{Address: "h2", Source: FeatureInventory},
		},
	}
	var out bytes.Buffer
	if err := cmds[0].Run(context.Background(), cfg, nil, &out, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "stage prod") || !strings.Contains(out.String(), "1 discovered") {
		t.Errorf("status output:\n%s", out.String())
	}
}
