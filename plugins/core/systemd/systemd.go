// Package systemd is the standard `systemd` plugin: it manages systemd units on the deploy hosts through six actions -
// systemd:start, systemd:stop, systemd:restart, systemd:enable, systemd:disable, and systemd:daemon-reload.
// It is registered as a default-on plugin (whoosh.RegisterDefault), so the actions are usable without listing the
// plugin under plugins:, disable it per stage with enabled: false.
//
// Two ways to use it:
//
// Ad-hoc, from any task (units and options come from the task's with:, layered over the plugin's params):
//
//	tasks:
//	  restart-services:
//	    action: systemd:restart
//	    with:
//	      system_unit_files: [app, sidekiq.target]
//	      sudo: true
//
// Auto-wired as a deploy hook - a plugins: entry whose actions: params set phase: contributes a hidden task invoking
// the action and anchors it before (default) or after that phase:
//
//	plugins:
//	  - name: systemd
//	    params:                       # global defaults for every systemd action
//	      sudo: true
//	    actions:
//	      - name: systemd:restart
//	        params:
//	          system_unit_files: [app]
//	          daemon_reload: true     # `systemctl daemon-reload` first (fresh unit files)
//	          phase: "deploy:finished"
//	          when: "before"          # "before" (default) or "after"
//	          roles: [web]            # restrict the hook task to hosts with these roles
//
// Params (task with: wins over the action's actions: params, which win over the plugin's global params:):
//
//	system_unit_files: []   # units managed via the system manager
//	user_unit_files: []     # units managed via `systemctl --user` (never sudo'd - the user manager belongs to the SSH user)
//	sudo: false             # prefix system-manager commands with `sudo -n` (non-interactive, needs NOPASSWD for systemctl)
//	daemon_reload: false    # run daemon-reload (per scope in use) before the verb
//	now: false              # enable/disable only: --now (also start/stop the units)
//	no_block: false         # --no-block: enqueue the job without waiting for it to finish
//	roles: []               # plugin actions: only - Roles on the contributed hook task
//	phase: ""               # plugin actions: only - phase to anchor the hook task to (empty = no hook)
//	when: "before"          # plugin actions: only - "before" or "after" the phase
//
// systemd:daemon-reload as a standalone action ignores the unit lists and takes its own scope params:
// system: true (default) reloads the system manager, user: true reloads the user manager.
//
// `systemctl --user` over SSH needs a running user manager (loginctl enable-linger) and XDG_RUNTIME_DIR set for
// non-interactive sessions.
package systemd

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/yousysadmin/whoosh"
)

const (
	pluginName    = "systemd"
	pluginVersion = "1.0.0"
)

// The action names, all under the plugin's namespace (the executor's per-stage skip logic keys on the segment before
// the first ":", which must equal the plugin name).
const (
	actionStart        = "systemd:start"
	actionStop         = "systemd:stop"
	actionRestart      = "systemd:restart"
	actionEnable       = "systemd:enable"
	actionDisable      = "systemd:disable"
	actionDaemonReload = "systemd:daemon-reload"
)

// verbs maps each action name to the systemctl verb it runs.
var verbs = map[string]string{
	actionStart:        "start",
	actionStop:         "stop",
	actionRestart:      "restart",
	actionEnable:       "enable",
	actionDisable:      "disable",
	actionDaemonReload: "daemon-reload",
}

// unitName is the accepted unit-name shape: systemd-legal characters only (covers app, sidekiq.target,
// worker@1.service), so a validated name is safe to embed in the built shell command.
var unitName = regexp.MustCompile(`^[A-Za-z0-9@:._\-]+$`)

func init() {
	whoosh.RegisterDefault(pluginName, func() whoosh.Plugin { return &plugin{} })
}

type plugin struct{}

// Version reports the plugin's version (whoosh.Versioner), shown by `whoosh plugins` / `whoosh version`.
func (p *plugin) Version() string { return pluginVersion }

// params is one systemd action's config - decoded from the plugin's global params:, an actions: entry's params, and a
// task's with:, merged in that order (most specific wins).
type params struct {
	SystemUnitFiles []string `yaml:"system_unit_files"`
	UserUnitFiles   []string `yaml:"user_unit_files"`
	Sudo            bool     `yaml:"sudo"`
	DaemonReload    bool     `yaml:"daemon_reload"`
	Now             bool     `yaml:"now"`
	NoBlock         bool     `yaml:"no_block"`
	// System / User scope the standalone daemon-reload action (system defaults to true).
	System *bool `yaml:"system"`
	User   bool  `yaml:"user"`
	// Roles / Phase / When are plugin-spec only: they shape the contributed hook task and are rejected in a task's with:.
	Roles []string `yaml:"roles"`
	Phase string   `yaml:"phase"`
	When  string   `yaml:"when"`
}

// hookSpec is one auto-wired hook collected at Configure time: a hidden task invoking action with with, anchored at
// phase.
type hookSpec struct {
	task   string
	action string
	phase  string
	after  bool
	roles  []string
	with   map[string]any
}

// actions carries the per-action defaults the registered ActionFuncs close over.
type actions struct {
	global   map[string]any            // the plugin's params: block
	defaults map[string]map[string]any // action name -> merge(global, that actions: entry's params)
}

// Configure validates the spec (global params + each actions: entry), registers the six actions, and - for every
// actions: entry with a phase: - a startup hook contributing the hidden hook task.
func (p *plugin) Configure(spec whoosh.PluginSpec, reg *whoosh.Registry) error {
	var gp params
	if err := whoosh.DecodeParams(spec.Params, &gp); err != nil {
		return fmt.Errorf("systemd params: %w", err)
	}
	if err := gp.validate(""); err != nil {
		return err
	}

	n := &actions{global: spec.Params, defaults: map[string]map[string]any{}}
	var hooks []hookSpec
	taskNames := map[string]bool{}
	for i, a := range spec.Actions {
		verb, ok := verbs[a.Name]
		if !ok {
			return fmt.Errorf("systemd: unknown action %q (want one of %s)", a.Name, strings.Join(actionNames(), ", "))
		}
		merged := merge(spec.Params, a.Params)
		var fp params
		if err := whoosh.DecodeParams(merged, &fp); err != nil {
			return fmt.Errorf("systemd: %s params: %w", a.Name, err)
		}
		if err := fp.validate(verb); err != nil {
			return err
		}
		// The entry's params are the action's defaults for ad-hoc with: too, on duplicate entries the last wins.
		n.defaults[a.Name] = merged

		if fp.Phase == "" {
			continue
		}
		name := a.Name + "@" + fp.Phase
		if taskNames[name] {
			name = fmt.Sprintf("%s#%d", name, i)
		}
		taskNames[name] = true
		hooks = append(hooks, hookSpec{
			task:   name,
			action: a.Name,
			phase:  fp.Phase,
			after:  strings.EqualFold(fp.When, "after"),
			roles:  fp.Roles,
			with:   stripSpecOnly(merged),
		})
	}

	for name := range verbs {
		if err := reg.AddAction(name, n.run(name)); err != nil {
			return err
		}
	}
	reg.AddStartup(func(_ context.Context, cfg *whoosh.DeployFile) error {
		for _, h := range hooks {
			cfg.AddTask(h.task, &whoosh.Task{
				Desc:   fmt.Sprintf("systemd: %s (hook at %s)", verbs[h.action], h.phase),
				Hidden: true,
				Silent: true,
				Roles:  h.roles,
				Action: h.action,
				With:   h.with,
			})
			if h.after {
				cfg.AddHookAfter(h.phase, h.task)
			} else {
				cfg.AddHookBefore(h.phase, h.task)
			}
		}
		return nil
	})
	return nil
}

// run builds the ActionFunc for one action: layer the task's with: over the action's defaults, validate, build the
// systemctl commands, and run each on the task's hosts via the executor-supplied HostCommandRunner (fail-fast, in
// order).
func (n *actions) run(action string) whoosh.ActionFunc {
	verb := verbs[action]
	return func(ctx context.Context, with map[string]any, _ io.Writer) error {
		for _, k := range []string{"roles", "phase", "when"} {
			if _, ok := with[k]; ok {
				return fmt.Errorf("systemd: %s: %q is a plugin-level param, not valid in a task's with:", action, k)
			}
		}
		base := n.defaults[action]
		if base == nil {
			base = n.global
		}
		var p params
		if err := whoosh.DecodeParams(merge(base, with), &p); err != nil {
			return fmt.Errorf("systemd: %s: %w", action, err)
		}
		if err := p.validate(verb); err != nil {
			return err
		}
		cmds, err := p.commands(verb)
		if err != nil {
			return err
		}
		runner := whoosh.HostCommandRunnerFrom(ctx)
		if runner == nil {
			return fmt.Errorf("systemd: %s: no host command runner in context (the action must run as a whoosh task)", action)
		}
		for _, cmd := range cmds {
			if err := runner.RunCommand(ctx, cmd); err != nil {
				return err
			}
		}
		return nil
	}
}

// validate checks the decoded params for the given verb ("" = the plugin's global params:, where verb-specific rules
// don't apply yet).
func (p params) validate(verb string) error {
	for _, u := range append(append([]string{}, p.SystemUnitFiles...), p.UserUnitFiles...) {
		if !unitName.MatchString(u) {
			return fmt.Errorf("systemd: invalid unit name %q", u)
		}
	}
	if p.When != "" && !strings.EqualFold(p.When, "before") && !strings.EqualFold(p.When, "after") {
		return fmt.Errorf("systemd: when must be \"before\" or \"after\", got %q", p.When)
	}
	if verb != "" {
		if p.Now && verb != "enable" && verb != "disable" {
			return fmt.Errorf("systemd: %s: now is only valid for enable/disable", verb)
		}
		if verb != "daemon-reload" && (p.System != nil || p.User) {
			return fmt.Errorf("systemd: %s: system/user are daemon-reload params", verb)
		}
	}
	return nil
}

// commands renders the ordered systemctl commands for the verb: an optional daemon-reload per scope in use, then the
// verb on the system units (sudo'd when asked) and on the user units (never sudo'd).
func (p params) commands(verb string) ([]string, error) {
	if verb == "daemon-reload" {
		var cmds []string
		if p.System == nil || *p.System {
			cmds = append(cmds, p.sysPrefix()+"systemctl daemon-reload")
		}
		if p.User {
			cmds = append(cmds, "systemctl --user daemon-reload")
		}
		if len(cmds) == 0 {
			return nil, fmt.Errorf("systemd: daemon-reload: nothing to do (system: false and user: false)")
		}
		return cmds, nil
	}
	if len(p.SystemUnitFiles)+len(p.UserUnitFiles) == 0 {
		return nil, fmt.Errorf("systemd: %s: no units configured (set system_unit_files and/or user_unit_files)", verb)
	}
	var cmds []string
	if p.DaemonReload {
		if len(p.SystemUnitFiles) > 0 {
			cmds = append(cmds, p.sysPrefix()+"systemctl daemon-reload")
		}
		if len(p.UserUnitFiles) > 0 {
			cmds = append(cmds, "systemctl --user daemon-reload")
		}
	}
	var flags string
	if p.Now {
		flags += " --now"
	}
	if p.NoBlock {
		flags += " --no-block"
	}
	if len(p.SystemUnitFiles) > 0 {
		cmds = append(cmds, p.sysPrefix()+"systemctl "+verb+flags+" "+quoteUnits(p.SystemUnitFiles))
	}
	if len(p.UserUnitFiles) > 0 {
		cmds = append(cmds, "systemctl --user "+verb+flags+" "+quoteUnits(p.UserUnitFiles))
	}
	return cmds, nil
}

// sysPrefix is the system-manager command prefix: `sudo -n` when asked (non-interactive, so a missing NOPASSWD rule
// fails fast instead of hanging on a password prompt).
func (p params) sysPrefix() string {
	if p.Sudo {
		return "sudo -n "
	}
	return ""
}

// quoteUnits single-quotes each (already validated) unit name for the shell command.
func quoteUnits(units []string) string {
	quoted := make([]string, len(units))
	for i, u := range units {
		quoted[i] = "'" + u + "'"
	}
	return strings.Join(quoted, " ")
}

// merge layers over on top of base: nested maps merge recursively, scalars and slices replace. Neither input is
// mutated.
func merge(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if bm, ok := out[k].(map[string]any); ok {
			if om, ok := v.(map[string]any); ok {
				out[k] = merge(bm, om)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// stripSpecOnly copies m without the plugin-spec-only keys, so a contributed hook task's with: passes the action's
// own with: validation.
func stripSpecOnly(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch k {
		case "roles", "phase", "when":
			continue
		}
		out[k] = v
	}
	return out
}

// actionNames returns the six action names, sorted for stable error messages.
func actionNames() []string {
	return []string{actionDaemonReload, actionDisable, actionEnable, actionRestart, actionStart, actionStop}
}
