// Package phase is an example whoosh plugins that defines a custom deploy phase.
// In its startup hook it contributes a task and splices a new phase (deploy:migrate) into the lifecycle right after the
// built-in deploy:published phase, running that task at that point.
//
// A custom phase is also a hook anchor: other tasks/plugins can run before or after it, e.g. in a Deployfile:
//
//	hooks:
//	  before:
//	    deploy:migrate: [notify-db-team]
//
// Custom phases can equally be declared in the Deployfile without a plugin:
//
//	custom_phases:
//	  - name: deploy:migrate
//	    after: deploy:published
//	    task: run-migrations
//
// Build a whoosh that includes this plugins:
//
//	whoosh build --with github.com/yousysadmin/whoosh-examples/phase -o ./whoosh
package phase

import (
	"context"

	"github.com/yousysadmin/whoosh"
)

const (
	pluginName  = "example-phase"
	taskMigrate = "example-migrate"
	phaseName   = "deploy:migrate"
)

func init() {
	whoosh.Register(pluginName, func() whoosh.Plugin { return &phasePlugin{} })
}

type phasePlugin struct{}

// Configure registers the startup hook that adds the task and the custom phase.
func (p *phasePlugin) Configure(_ whoosh.PluginSpec, reg *whoosh.Registry) error {
	reg.AddStartup(p.install)
	return nil
}

// install runs once at load. It adds the task the phase will run, then declares the phase.
// The task sees the phase via {{.phase}} / $DEPLOY_PHASE.
func (p *phasePlugin) install(_ context.Context, cfg *whoosh.DeployFile) error {
	cfg.AddTask(taskMigrate, &whoosh.Task{
		Desc: "Example migration step, run in the custom " + phaseName + " phase",
		Cmds: []string{
			`echo "[example-phase] migrating {{.app_name}} in phase {{.phase}}"`,
		},
	})
	// Insert a named phase after the release is live. Set exactly one of Before/ After to a built-in phase.
	// With no Task it would be a pure hook anchor.
	cfg.AddPhase(whoosh.CustomPhase{
		Name:  phaseName,
		After: "deploy:published",
		Task:  taskMigrate,
	})
	return nil
}
