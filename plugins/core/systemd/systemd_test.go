package systemd

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh"
)

// fakeRunner captures the commands an action asks the host runner to run.
type fakeRunner struct{ cmds []string }

func (f *fakeRunner) RunCommand(_ context.Context, cmd string) error {
	f.cmds = append(f.cmds, cmd)
	return nil
}

// runAction configures the plugin from spec, invokes the named action with `with`, and returns the commands it ran.
func runAction(t *testing.T, spec whoosh.PluginSpec, action string, with map[string]any) ([]string, error) {
	t.Helper()
	reg, err := whoosh.Load([]whoosh.PluginSpec{spec})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	fn, ok := reg.Action(action)
	if !ok {
		t.Fatalf("action %q not registered", action)
	}
	f := &fakeRunner{}
	err = fn(whoosh.WithHostCommandRunner(context.Background(), f), with, io.Discard)
	return f.cmds, err
}

// The registered name must match the documented one: `plugins: [{name: systemd, enabled: false}]` only disables this
// default-on plugin when the names agree (DefaultSpecs skips a declared spec by name).
func TestRegisteredUnderDocumentedName(t *testing.T) {
	if pluginName != "systemd" {
		t.Fatalf("pluginName = %q, want %q (the documented name)", pluginName, "systemd")
	}
	if !whoosh.IsRegistered(pluginName) {
		t.Fatal("plugin not registered")
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

// Every action name's namespace (the segment before the first ":") must equal the plugin name - the executor's
// per-stage skip logic keys on it.
func TestActionNamespaceMatchesPluginName(t *testing.T) {
	for name := range verbs {
		if ns, _, _ := strings.Cut(name, ":"); ns != pluginName {
			t.Errorf("action %q namespace = %q, want %q", name, ns, pluginName)
		}
	}
}

// Configure validates the spec offline (so `whoosh validate` catches these without connecting anywhere).
func TestConfigure_Validation(t *testing.T) {
	cases := []struct {
		name    string
		spec    whoosh.PluginSpec
		wantErr string
	}{
		{
			name:    "unknown action",
			spec:    whoosh.PluginSpec{Name: pluginName, Actions: []whoosh.PluginActionSpec{{Name: "systemd:reboot"}}},
			wantErr: "unknown action",
		},
		{
			name: "bad when",
			spec: whoosh.PluginSpec{Name: pluginName, Actions: []whoosh.PluginActionSpec{{
				Name:   actionStart,
				Params: map[string]any{"system_unit_files": []string{"app"}, "phase": "deploy:finished", "when": "sometimes"},
			}}},
			wantErr: "when must be",
		},
		{
			name:    "unit with shell metacharacters",
			spec:    whoosh.PluginSpec{Name: pluginName, Params: map[string]any{"system_unit_files": []string{"app; rm -rf /"}}},
			wantErr: "invalid unit name",
		},
		{
			name:    "empty unit name",
			spec:    whoosh.PluginSpec{Name: pluginName, Params: map[string]any{"user_unit_files": []string{""}}},
			wantErr: "invalid unit name",
		},
		{
			name: "now on restart",
			spec: whoosh.PluginSpec{Name: pluginName, Actions: []whoosh.PluginActionSpec{{
				Name:   actionRestart,
				Params: map[string]any{"system_unit_files": []string{"app"}, "now": true},
			}}},
			wantErr: "now is only valid for enable/disable",
		},
		{
			name: "daemon-reload scope on start",
			spec: whoosh.PluginSpec{Name: pluginName, Actions: []whoosh.PluginActionSpec{{
				Name:   actionStart,
				Params: map[string]any{"system_unit_files": []string{"app"}, "user": true},
			}}},
			wantErr: "daemon-reload params",
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

// commands renders the ordered systemctl invocations: optional daemon-reload per scope first, then the verb on the
// system units (sudo'd when asked, `sudo -n` so a missing NOPASSWD fails fast) and on the user units (never sudo'd).
func TestCommands(t *testing.T) {
	cases := []struct {
		name string
		p    params
		verb string
		want []string
	}{
		{
			name: "system only",
			p:    params{SystemUnitFiles: []string{"app", "sidekiq.target"}},
			verb: "start",
			want: []string{"systemctl start 'app' 'sidekiq.target'"},
		},
		{
			name: "sudo, both scopes, daemon_reload",
			p:    params{SystemUnitFiles: []string{"app"}, UserUnitFiles: []string{"agent"}, Sudo: true, DaemonReload: true},
			verb: "restart",
			want: []string{
				"sudo -n systemctl daemon-reload",
				"systemctl --user daemon-reload",
				"sudo -n systemctl restart 'app'",
				"systemctl --user restart 'agent'",
			},
		},
		{
			name: "enable --now --no-block",
			p:    params{SystemUnitFiles: []string{"worker@1.service"}, Now: true, NoBlock: true},
			verb: "enable",
			want: []string{"systemctl enable --now --no-block 'worker@1.service'"},
		},
		{
			name: "daemon-reload defaults to system",
			p:    params{Sudo: true},
			verb: "daemon-reload",
			want: []string{"sudo -n systemctl daemon-reload"},
		},
		{
			name: "daemon-reload both scopes",
			p:    params{User: true},
			verb: "daemon-reload",
			want: []string{"systemctl daemon-reload", "systemctl --user daemon-reload"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.p.commands(tc.verb)
			if err != nil {
				t.Fatalf("commands: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("commands = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCommands_Errors(t *testing.T) {
	if _, err := (params{}).commands("start"); err == nil || !strings.Contains(err.Error(), "no units configured") {
		t.Errorf("empty units: err = %v, want no units configured", err)
	}
	off := false
	if _, err := (params{System: &off}).commands("daemon-reload"); err == nil || !strings.Contains(err.Error(), "nothing to do") {
		t.Errorf("daemon-reload with both scopes off: err = %v, want nothing to do", err)
	}
}

// A task's with: is layered over the matching actions: entry's params, which are layered over the plugin's global
// params: (most specific wins).
func TestAction_ParamLayering(t *testing.T) {
	spec := whoosh.PluginSpec{
		Name:   pluginName,
		Params: map[string]any{"sudo": true},
		Actions: []whoosh.PluginActionSpec{{
			Name:   actionStart,
			Params: map[string]any{"system_unit_files": []string{"app"}},
		}},
	}
	// The actions: entry provides the units, the global params: the sudo default.
	cmds, err := runAction(t, spec, actionStart, nil)
	if err != nil {
		t.Fatalf("action: %v", err)
	}
	if want := []string{"sudo -n systemctl start 'app'"}; !reflect.DeepEqual(cmds, want) {
		t.Fatalf("cmds = %q, want %q", cmds, want)
	}

	// with: wins over both layers.
	cmds, err = runAction(t, spec, actionStart, map[string]any{"system_unit_files": []string{"cron"}, "sudo": false})
	if err != nil {
		t.Fatalf("action with overrides: %v", err)
	}
	if want := []string{"systemctl start 'cron'"}; !reflect.DeepEqual(cmds, want) {
		t.Fatalf("cmds = %q, want %q", cmds, want)
	}

	// An action without an actions: entry falls back to the global params.
	cmds, err = runAction(t, spec, actionStop, map[string]any{"user_unit_files": []string{"agent"}})
	if err != nil {
		t.Fatalf("action without entry: %v", err)
	}
	if want := []string{"systemctl --user stop 'agent'"}; !reflect.DeepEqual(cmds, want) {
		t.Fatalf("cmds = %q, want %q", cmds, want)
	}
}

// with: values are validated at run time too (they arrive after Configure).
func TestAction_WithValidation(t *testing.T) {
	spec := whoosh.PluginSpec{Name: pluginName}
	if _, err := runAction(t, spec, actionStart, map[string]any{"phase": "deploy:finished"}); err == nil ||
		!strings.Contains(err.Error(), "plugin-level param") {
		t.Errorf("phase in with: err = %v, want plugin-level param", err)
	}
	if _, err := runAction(t, spec, actionStart, map[string]any{"system_unit_files": []string{"$(reboot)"}}); err == nil ||
		!strings.Contains(err.Error(), "invalid unit name") {
		t.Errorf("bad unit in with: err = %v, want invalid unit name", err)
	}
	if _, err := runAction(t, spec, actionStart, nil); err == nil ||
		!strings.Contains(err.Error(), "no units configured") {
		t.Errorf("no units: err = %v, want no units configured", err)
	}
}

// Outside the executor there is no host runner in ctx - the action fails with a clear error instead of pretending to
// have run anything.
func TestAction_NoRunnerInCtx(t *testing.T) {
	reg, err := whoosh.Load([]whoosh.PluginSpec{{Name: pluginName}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	fn, _ := reg.Action(actionStart)
	err = fn(context.Background(), map[string]any{"system_unit_files": []string{"app"}}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no host command runner") {
		t.Fatalf("err = %v, want no host command runner", err)
	}
}

// An actions: entry with a phase: contributes a hidden task invoking the action (with the spec-only keys stripped from
// its with:) and anchors it before (default) or after that phase.
func TestStartup_HooksWiring(t *testing.T) {
	reg, err := whoosh.Load([]whoosh.PluginSpec{{
		Name:   pluginName,
		Params: map[string]any{"sudo": true},
		Actions: []whoosh.PluginActionSpec{
			{Name: actionStop, Params: map[string]any{
				"system_unit_files": []string{"app"}, "phase": whoosh.PhasePublishing,
			}},
			{Name: actionStart, Params: map[string]any{
				"system_unit_files": []string{"app"}, "phase": whoosh.PhaseFinished, "when": "after", "roles": []string{"web"},
			}},
		},
	}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := &whoosh.DeployFile{}
	if err := reg.RunStartup(context.Background(), cfg); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}

	stop := cfg.Tasks[actionStop+"@"+whoosh.PhasePublishing]
	if stop == nil || !stop.Hidden || stop.Action != actionStop {
		t.Fatalf("stop hook task missing or malformed: %+v", stop)
	}
	if got := cfg.Hooks.Before[whoosh.PhasePublishing]; len(got) != 1 || got[0] != actionStop+"@"+whoosh.PhasePublishing {
		t.Errorf("before %s hooks = %v", whoosh.PhasePublishing, got)
	}

	startName := actionStart + "@" + whoosh.PhaseFinished
	start := cfg.Tasks[startName]
	if start == nil || !start.Hidden || start.Action != actionStart {
		t.Fatalf("start hook task missing or malformed: %+v", start)
	}
	if !reflect.DeepEqual(start.Roles, []string{"web"}) {
		t.Errorf("start task roles = %v, want [web]", start.Roles)
	}
	// The spec-only keys are stripped from the task's with:, the rest (units + inherited sudo) is kept.
	for _, k := range []string{"phase", "when", "roles"} {
		if _, ok := start.With[k]; ok {
			t.Errorf("with: still carries spec-only key %q: %v", k, start.With)
		}
	}
	if start.With["sudo"] != true || start.With["system_unit_files"] == nil {
		t.Errorf("with: lost merged params: %v", start.With)
	}
	if got := cfg.Hooks.After[whoosh.PhaseFinished]; len(got) != 1 || got[0] != startName {
		t.Errorf("after %s hooks = %v", whoosh.PhaseFinished, got)
	}
}

// Without a phase: nothing is contributed - the actions: entry only sets that action's defaults.
func TestStartup_NoPhaseNoHook(t *testing.T) {
	reg, err := whoosh.Load([]whoosh.PluginSpec{{
		Name:    pluginName,
		Actions: []whoosh.PluginActionSpec{{Name: actionRestart, Params: map[string]any{"system_unit_files": []string{"app"}}}},
	}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := &whoosh.DeployFile{}
	if err := reg.RunStartup(context.Background(), cfg); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}
	if len(cfg.Tasks) != 0 {
		t.Errorf("no task should be added, got %v", cfg.Tasks)
	}
	if len(cfg.Hooks.Before)+len(cfg.Hooks.After) != 0 {
		t.Errorf("no hooks should be added, got %+v", cfg.Hooks)
	}
}

// Two entries for the same action at the same phase (before + after) get distinct task names.
func TestStartup_DuplicateEntriesGetUniqueTaskNames(t *testing.T) {
	reg, err := whoosh.Load([]whoosh.PluginSpec{{
		Name: pluginName,
		Actions: []whoosh.PluginActionSpec{
			{Name: actionRestart, Params: map[string]any{"system_unit_files": []string{"a"}, "phase": whoosh.PhaseFinished}},
			{Name: actionRestart, Params: map[string]any{"system_unit_files": []string{"b"}, "phase": whoosh.PhaseFinished, "when": "after"}},
		},
	}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := &whoosh.DeployFile{}
	if err := reg.RunStartup(context.Background(), cfg); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}
	if len(cfg.Tasks) != 2 {
		t.Fatalf("tasks = %d (%v), want 2", len(cfg.Tasks), cfg.Tasks)
	}
	if len(cfg.Hooks.Before[whoosh.PhaseFinished]) != 1 || len(cfg.Hooks.After[whoosh.PhaseFinished]) != 1 {
		t.Errorf("hooks = %+v, want one before and one after", cfg.Hooks)
	}
}
