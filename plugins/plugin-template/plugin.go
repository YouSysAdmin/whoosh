// Package plugintemplate is a scaffold for a whoosh plugin: every SDK extension point (github.com/yousysadmin/whoosh)
// implemented and tested, with TODO markers where your logic goes.
// Copy the directory, rename the module and the plugin, delete the files/features you don't need, and start developing.
//
// One extension point per file - delete a file, delete its registration below, and the capability is gone:
//
//	plugin.go     registration (Register/RegisterDefault), Versioner, Configure (decode -> validate -> register)
//	params.go     the three config surfaces (params: / actions: / with:) and their layering helpers
//	client.go     the placeholder for the external system your plugin talks to (built once, shared by everything)
//	actions.go    action tasks: `render` (HostFileWriter bridge) and `exec` (HostCommandRunner bridge)
//	inventory.go  startup feature: discover hosts and append them to the inventory (cfg.Hosts)
//	context.go    startup feature: fetch values once, expose as {{ .ns.key }} / $NS_KEY, register them as secrets
//	setup.go      startup feature: contribute a host-side script task, wire it via hooks, func-hooks, or a custom phase
//	commands.go   a `whoosh <stage> plugin-template:status` CLI subcommand (Commander)
//
// The full config surface this scaffold understands:
//
//	plugins:
//	  - name: plugin-template
//	    params:                                # global config -> client.go (validated offline)
//	      endpoint: "https://api.example.com"
//	      token: '{{ env "EXAMPLE_TOKEN" }}'   # registered with whoosh.AddSecret - masked everywhere
//	    actions:
//	      - name: plugin-template:inventory    # opt-in startup: discover hosts
//	        params: { roles: [app], deploy: true }
//	      - name: plugin-template:context      # opt-in startup: {{ .template.db_url }} / $TEMPLATE_DB_URL
//	        params: { keys: [db_url, api_key] }
//	      - name: plugin-template:setup        # opt-in startup: host-side setup task wired into the lifecycle
//	        params: { phase: "deploy:updated", when: "before", roles: [app] }
//	      - name: plugin-template:render       # optional defaults for the render action
//	        params: { path: ".env.generated" }
//
//	tasks:
//	  generate-env:
//	    roles: [app]
//	    action: plugin-template:render         # with: wins over the actions: entry, which wins over params:
//	    with: { key: db_url }
//	  warmup:
//	    roles: [app]
//	    action: plugin-template:exec
//	    with: { cmd: "curl -fsS http://localhost:8080/healthz" }
//
// Build a binary that includes it with `whoosh build` (see the README).
package plugintemplate

import (
	"fmt"

	"github.com/yousysadmin/whoosh"
)

const (
	// pluginName is the registered name (the Deployfile `plugins: - name:` value). It MUST be the namespace prefix of
	// every action and feature (the segment before the first ":") - the executor's per-stage skip logic keys on it.
	pluginName    = "plugin-template"
	pluginVersion = "0.1.0"
)

// init registers the plugin, a binary that imports this package (via `whoosh build --with ...`) makes it available.
// Swap for whoosh.RegisterDefault to make it always-on (loads in every stage without a `plugins:` entry).
func init() {
	whoosh.Register(pluginName, func() whoosh.Plugin { return &plugin{} })
}

// plugin carries everything Configure resolves for the features and actions: the decoded global params, the shared
// client, and each feature's raw `actions:` params (kept raw so a task's `with:` can be layered over them).
type plugin struct {
	global   globalParams
	client   *client
	features map[string]map[string]any
}

// Version reports the plugin's version (whoosh.Versioner, optional) - shown by `whoosh plugins` / `whoosh version`.
// Queried on a bare instance (no Configure), so it must be side-effect free.
func (p *plugin) Version() string { return pluginVersion }

// Configure is the Plugin interface. It runs once at load, BEFORE the config is resolved, so it does three things and
// nothing else: decode+validate the spec (all of it - this is what `whoosh validate` exercises offline), build shared
// state (the client), and register contributions into reg. Anything needing the resolved config belongs in a startup.
func (p *plugin) Configure(spec whoosh.PluginSpec, reg *whoosh.Registry) error {
	// 1. Global params -> shared client. Secrets are registered immediately so even a --dry-run plan masks them.
	if err := whoosh.DecodeParams(spec.Params, &p.global); err != nil {
		return fmt.Errorf("%s params: %w", pluginName, err)
	}
	if err := p.global.validate(); err != nil {
		return err
	}
	if p.global.Token != "" {
		whoosh.AddSecret(p.global.Token)
	}
	var err error
	if p.client, err = newClient(p.global); err != nil {
		return err
	}

	// 2. Index the actions: entries. An entry either opts a startup feature in, or supplies defaults for an action,
	// unknown names error here so a typo fails `whoosh validate`, not the deploy. On duplicates the last entry wins.
	p.features = map[string]map[string]any{}
	for _, a := range spec.Actions {
		switch a.Name {
		case FeatureInventory, FeatureContext, FeatureSetup, ActionRender, ActionExec:
			p.features[a.Name] = a.Params
		default:
			return fmt.Errorf("%s: unknown action %q (want one of %s, %s, %s, %s, %s)", pluginName,
				a.Name, FeatureInventory, FeatureContext, FeatureSetup, ActionRender, ActionExec)
		}
	}

	// 3a. Actions register unconditionally, so ad-hoc `action:`/`with:` tasks work with zero plugin config, a matching
	// actions: entry only supplies defaults.
	if err := reg.AddAction(ActionRender, p.render); err != nil {
		return err
	}
	if err := reg.AddAction(ActionExec, p.exec); err != nil {
		return err
	}

	// 3b. Startup features run only when listed. Their params are decoded HERE (not in the startup) so bad config
	// fails offline, the decoded values are captured by the returned StartupFunc closures.
	if raw, ok := p.features[FeatureInventory]; ok {
		fp, err := decodeInventoryParams(raw)
		if err != nil {
			return err
		}
		reg.AddStartup(p.inventoryStartup(fp))
	}
	if raw, ok := p.features[FeatureContext]; ok {
		fp, err := decodeContextParams(raw)
		if err != nil {
			return err
		}
		reg.AddStartup(p.contextStartup(fp))
	}
	if raw, ok := p.features[FeatureSetup]; ok {
		fp, err := decodeSetupParams(raw)
		if err != nil {
			return err
		}
		reg.AddStartup(p.setupStartup(fp))
	}
	return nil
}
