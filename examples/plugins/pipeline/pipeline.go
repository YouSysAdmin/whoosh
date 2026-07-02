// Package pipeline is an example whoosh plugins that adds a step to the deploy
// pipeline. In its startup hook it contributes a Task (running a command and an
// embedded shell script on the target hosts) and wires that task to run after the
// release goes live, via an after-hook on the built-in deploy:published phase.
//
// It shows four things a plugin can do through the public SDK alone:
//   - add a task to the pipeline      (cfg.AddTask)
//   - run commands on the hosts       (Task.Cmds - Go-templated)
//   - ship and run a script           (Task.Scripts, content embedded via //go:embed)
//   - hook the task into a phase       (cfg.AddHookAfter)
//
// Build a whoosh that includes it:
//
//	whoosh build --with github.com/yousysadmin/whoosh-examples/pipeline -o ./whoosh
//
// Then just deploy - no Deployfile changes needed, the healthcheck runs after every publish:
//
//	whoosh production deploy
package pipeline

import (
	"context"
	_ "embed"

	"github.com/yousysadmin/whoosh"
)

const (
	pluginName      = "example-pipeline"
	taskHealthcheck = "example-healthcheck"
)

// healthcheckScript is shipped inside the plugins binary and run on each host as the task's script (the executor
// streams it over SSH, no upload needed).
//
//go:embed healthcheck.sh
var healthcheckScript string

func init() {
	whoosh.Register(pluginName, func() whoosh.Plugin { return &pipelinePlugin{} })
}

type pipelinePlugin struct{}

// Configure registers the startup hook that contributes the task and wires it in.
// This plugins takes no params and registers no actions.
func (p *pipelinePlugin) Configure(_ whoosh.PluginSpec, reg *whoosh.Registry) error {
	reg.AddStartup(p.install)
	return nil
}

// install runs once at load, against the resolved config for the stage.
func (p *pipelinePlugin) install(_ context.Context, cfg *whoosh.DeployFile) error {
	// Contribute a task.
	// Cmds run first (Go-templated against the deploy context: {{.app_name}}, {{.release_path}}, {{.host}}, ...), then
	// Scripts.
	// By default a task runs on the deployable hosts over SSH, set Local: true to run it on the operator machine instead
	// (e.g. to call an external API).
	cfg.AddTask(taskHealthcheck, &whoosh.Task{
		Desc: "Example post-publish healthcheck (added by " + pluginName + ")",
		Cmds: []string{
			`echo "[example-pipeline] {{.app_name}} live at {{.release_path}} on {{.host}}"`,
		},
		Scripts: []whoosh.Script{
			{Name: "healthcheck", Script: healthcheckScript},
		},
	})

	// Run it right after the new release becomes live. deploy:published is a built-in marker phase,
	// AddHookBefore/AddHookAfter accept any phase name (built-in or a custom phase - see the example-phase plugins).
	cfg.AddHookAfter("deploy:published", taskHealthcheck)
	return nil
}
