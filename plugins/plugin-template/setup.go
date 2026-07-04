package plugintemplate

// The setup feature: a startup hook that contributes a host-side TASK and wires it into the deploy lifecycle - the
// pattern behind the rbenv plugin. This is how a plugin runs real work on the deploy hosts: the executor runs the
// task's script over SSH on the role-matched hosts, with the plugin's config passed through the task environment.
//
// It demonstrates every lifecycle wiring the SDK offers:
//
//   - cfg.AddTask                    - contribute the task (also runnable ad-hoc: `whoosh <stage> plugin-template:setup`)
//   - cfg.AddHookBefore/After        - anchor it before/after a phase (params: phase/when)
//   - cfg.AddPhase                   - OR splice a named custom phase running it (params: custom_phase)
//   - cfg.AddHookFuncBefore/After    - func-hooks: the plugin's own Go code at a phase, no task/shell involved
//
// Opt-in:
//
//	plugins:
//	  - name: plugin-template
//	    actions:
//	      - name: plugin-template:setup
//	        params: { phase: "deploy:updated", when: "before", roles: [app] }

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"strings"

	"github.com/yousysadmin/whoosh"
)

// FeatureSetup is the feature's name in the `actions:` list, taskSetup is the contributed task's name.
const (
	FeatureSetup = pluginName + ":setup"
	taskSetup    = pluginName + ":setup"
)

// setupScript is the host-side script the contributed task runs. It is configured entirely through environment
// variables (set in the task's Envs below), so the script stays static and testable.
//
//go:embed templates/setup.sh
var setupScript string

// setupParams places and scopes the contributed task.
type setupParams struct {
	// Roles restricts the task to hosts filling these roles (empty = all deployable hosts).
	Roles []string `yaml:"roles"`
	// Phase / When anchor the task as a hook: When is "before" (default) or "after", Phase defaults to
	// deploy:updated (release built and linked, not yet live).
	Phase string `yaml:"phase"`
	When  string `yaml:"when"`
	// CustomPhase, when set, wires the task as a NEW named phase spliced after deploy:published instead of a hook -
	// other tasks can then hook onto that name. Mutually exclusive with phase/when.
	CustomPhase string `yaml:"custom_phase"`
	// Envs is extra environment for the script (merged under the plugin's own control vars).
	Envs map[string]string `yaml:"envs"`
}

// decodeSetupParams decodes+validates the feature's params at Configure time (offline).
func decodeSetupParams(raw map[string]any) (setupParams, error) {
	var fp setupParams
	if err := whoosh.DecodeParams(raw, &fp); err != nil {
		return fp, fmt.Errorf("%s params: %w", FeatureSetup, err)
	}
	if fp.When != "" && !strings.EqualFold(fp.When, "before") && !strings.EqualFold(fp.When, "after") {
		return fp, fmt.Errorf("%s: when must be \"before\" or \"after\", got %q", FeatureSetup, fp.When)
	}
	if fp.CustomPhase != "" && (fp.Phase != "" || fp.When != "") {
		return fp, fmt.Errorf("%s: custom_phase and phase/when are mutually exclusive", FeatureSetup)
	}
	if fp.Phase == "" {
		fp.Phase = whoosh.PhaseUpdated
	}
	if fp.When == "" {
		fp.When = "before"
	}
	return fp, nil
}

// setupStartup returns the StartupFunc that contributes the task and wires it into the lifecycle.
func (p *plugin) setupStartup(fp setupParams) whoosh.StartupFunc {
	return func(_ context.Context, cfg *whoosh.DeployFile) error {
		// The task environment is the config channel to the script: start from the user's extra envs, then set the
		// plugin's own control vars on top (the plugin always wins). Env values are shell-expanded on the host.
		env := map[string]string{}
		for k, v := range fp.Envs {
			env[k] = v
		}
		env["PLUGIN_TEMPLATE_ENDPOINT"] = p.global.Endpoint

		cfg.AddTask(taskSetup, &whoosh.Task{
			Desc:  "TODO: describe what the setup task does",
			Dir:   ".", // run from $HOME - the hook is movable to phases where the release dirs don't exist yet
			Roles: fp.Roles,
			Envs:  env,
			Scripts: []whoosh.Script{{
				Name:   taskSetup,
				Script: setupScript,
			}},
		})

		// Wiring style 1: a custom phase - a new named lifecycle point running our task, spliced after
		// deploy:published. Other Deployfile hooks can target it by name.
		if fp.CustomPhase != "" {
			cfg.AddPhase(whoosh.CustomPhase{
				Name:  fp.CustomPhase,
				After: whoosh.PhasePublished,
				Task:  taskSetup,
			})
			return nil
		}

		// Wiring style 2 (default): a plain hook before/after an existing phase.
		if strings.EqualFold(fp.When, "after") {
			cfg.AddHookAfter(fp.Phase, taskSetup)
		} else {
			cfg.AddHookBefore(fp.Phase, taskSetup)
		}

		// Func-hooks: plugin Go code at a phase, with the deploy's console writer - no task, no shell. They fire
		// only during the deploy lifecycle (not for `config`/`run`). Delete if you don't need operator-side code
		// at lifecycle points.
		cfg.AddHookFuncBefore(whoosh.PhaseStarting, func(_ context.Context, out io.Writer) error {
			fmt.Fprintf(out, "%s: setup will run %s %s\n", pluginName, fp.When, fp.Phase)
			return nil
		})
		cfg.AddHookFuncAfter(whoosh.PhaseFinished, func(_ context.Context, out io.Writer) error {
			fmt.Fprintf(out, "%s: deploy finished\n", pluginName)
			return nil
		})
		return nil
	}
}
