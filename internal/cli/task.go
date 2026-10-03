package cli

import (
	"cmp"
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/deploy/hooks"
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/executor"
)

// newTaskCmd builds the command that runs a single named Deployfile task.
// A hidden task is still registered (so it can be run directly, or as a dep/hook) but omitted from the CLI listing.
func newTaskCmd(stage, name, desc string, hidden bool, gf *globalFlags) *cobra.Command {
	short := cmp.Or(desc, fmt.Sprintf("Run the %q task", name))

	return &cobra.Command{
		Use:    name,
		Short:  short,
		Hidden: hidden,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, reg, err := loadConfig(cmd.Context(), cmd, gf, stage)
			if err != nil {
				return err
			}

			ex, err := newExecutor(cmd, cfg, reg, gf)
			if err != nil {
				return err
			}

			defer ex.Close()
			err = ex.RunTask(cmd.Context(), name)
			if err != nil && !gf.dryRun {
				notifyTaskFailure(cmd.Context(), cfg, ex, name, err)
			}

			return err
		},
	}
}

// notifyTaskFailure fires the after deploy:failed hooks when a top-level task command fails, mirroring the deploy
// lifecycle's onFailure, so a pipeline run outside a deploy (e.g. an ASG refresh) still notifies. Best-effort: hook
// errors are only logged, the command still returns the task's own error. The hooks run even when ctx is cancelled.
// A task opts out with notify_failure: false.
func notifyTaskFailure(ctx context.Context, cfg *ast.DeployFile, ex *executor.Executor, name string, err error) {
	if t := cfg.Tasks[name]; t != nil && t.NotifyFailure != nil && !*t.NotifyFailure {
		return
	}

	r := hooks.New(cfg.Hooks, cfg.HookFuncsBefore, cfg.HookFuncsAfter, ex.RunTaskInPhase, ex.Out())
	r.NotifyFailed(ctx, ex.SetError, err)
}
