package cli

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/executor"
	"github.com/yousysadmin/whoosh/internal/masking"
	"github.com/yousysadmin/whoosh/internal/paths"
	"github.com/yousysadmin/whoosh/internal/runner"
)

func newRunCmd(stage string, gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "run <command>",
		Short: "Run an ad-hoc command on the stage's hosts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := loadConfig(cmd.Context(), cmd, gf, stage)
			if err != nil {
				return err
			}

			hosts := selectHosts(cfg, gf.roles, gf.limit)
			if len(hosts) == 0 {
				return fmt.Errorf("no hosts match the given roles/host filters for stage %q", stage)
			}

			// Run ad-hoc commands like task cmds: inside the release dir (the live `current`) and with the global env, so e.g.
			// `bundle exec ...` resolves the same way deploy hooks do.
			releaseDir := paths.For(cfg.App.DeployTo).CurrentPath
			command := executor.WrapCommand(args[0], releaseDir, cfg.Envs)
			out := masking.NewWriter(cmd.OutOrStdout())
			defer out.Flush()

			if gf.dryRun {
				fmt.Fprintf(out, "[dry-run] would run on %d host(s):\n", len(hosts))
				for _, h := range hosts {
					fmt.Fprintf(out, "  %s: %s\n", h.Address, command)
				}
				return nil
			}

			results := runner.RunCommand(cmd.Context(), executor.Targets(hosts), sshOptions(cfg), command, out, colorOutput(cmd, cfg.Log), gf.conc, false)
			return reportResults(results)
		},
	}
}

// reportResults logs each per-host failure and returns an error if any host failed.
func reportResults(results []runner.Result) error {
	var first error
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			if first == nil {
				first = r.Err
			}
			slog.Error("host failed", "host", r.Host, "error", r.Err)
		}
	}
	if failed > 0 {
		// Wrap the first failure so its category exit code (command/unreachable) reaches cli.Execute, while still summarizing
		// the host count.
		return fmt.Errorf("%d of %d host(s) failed: %w", failed, len(results), first)
	}
	return nil
}
