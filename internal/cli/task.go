package cli

import (
	"fmt"

	"github.com/spf13/cobra"

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
			ex := executor.New(cfg, executor.Options{
				SSH:         sshOptions(cfg),
				Out:         cmd.OutOrStdout(),
				DryRun:      gf.dryRun,
				Verbose:     gf.verbose,
				Roles:       gf.roles,
				Limit:       gf.limit,
				Concurrency: gf.conc,
				Registry:    reg,
				Color:       colorOutput(cmd, cfg.Log),
			})
			defer ex.Close()
			return ex.RunTask(cmd.Context(), name)
		},
	}
}
