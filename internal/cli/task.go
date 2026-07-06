package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/deploy/hooks"
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/executor"
)

// newTaskCmd builds the command that runs a single named Deployfile task.
// A hidden task is still registered (so it can be run directly, or as a dep/hook) but omitted from the CLI listing.
func newTaskCmd(stage, name, desc string, hidden bool, gf *globalFlags) *cobra.Command {
	short := desc
	if short == "" {
		short = fmt.Sprintf("Run the %q task", name)
	}
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
			sshOpts, err := sshOptions(cfg)
			if err != nil {
				return err
			}
			ex := executor.New(cfg, executor.Options{
				SSH:         sshOpts,
				Out:         cmd.OutOrStdout(),
				DryRun:      gf.dryRun,
				Verbose:     effectiveVerbose(cmd, gf.verbose, cfg.Log),
				Roles:       gf.roles,
				Limit:       gf.limit,
				Concurrency: gf.conc,
				Registry:    reg,
				Color:       colorOutput(cmd, cfg.Log),
			})
			defer ex.Close()
			err = ex.RunTask(cmd.Context(), name)
			if err != nil && !gf.dryRun {
				notifyTaskFailure(cfg, ex, name, err)
			}
			return err
		},
	}
}

// notifyTaskFailure fires the after deploy:failed hooks when a top-level task command fails, mirroring the deploy
// lifecycle's onFailure, so a pipeline run outside a deploy (e.g. an ASG refresh) still notifies. Best-effort: hook
// errors are only logged, the command still returns the task's own error. A fresh context is used because the
// command's may be cancelled. A task opts out with notify_failure: false.
func notifyTaskFailure(cfg *ast.DeployFile, ex *executor.Executor, name string, err error) {
	if t := cfg.Tasks[name]; t != nil && t.NotifyFailure != nil && !*t.NotifyFailure {
		return
	}
	if len(cfg.Hooks.After[ast.PhaseFailed]) == 0 && len(cfg.HookFuncsAfter[ast.PhaseFailed]) == 0 {
		return
	}
	ex.SetError(err.Error())
	r := hooks.New(cfg.Hooks, cfg.HookFuncsBefore, cfg.HookFuncsAfter, ex.RunTaskInPhase, ex.Out())
	if hookErr := r.After(context.Background(), ast.PhaseFailed); hookErr != nil {
		slog.Warn("deploy:failed hook error", "error", hookErr)
	}
}
