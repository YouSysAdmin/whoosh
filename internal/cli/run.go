package cli

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/executor"
	"github.com/yousysadmin/whoosh/internal/masking"
	"github.com/yousysadmin/whoosh/internal/paths"
	"github.com/yousysadmin/whoosh/internal/runner"
	"github.com/yousysadmin/whoosh/internal/varstmpl"
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
			envs, err := renderRunEnvs(cfg, releaseDir, gf.dryRun)
			if err != nil {
				return err
			}
			command := executor.WrapCommand(args[0], releaseDir, envs)
			out := masking.NewWriter(cmd.OutOrStdout())
			defer out.Flush()

			if gf.dryRun {
				// Show the operator's command as typed, the full wrapped form (cd + env exports) only under --verbose,
				// like the live echo.
				shown := args[0]
				if effectiveVerbose(cmd, gf.verbose, cfg.Log) {
					shown = command
				}
				fmt.Fprintf(out, "[dry-run] would run on %d host(s):\n", len(hosts))
				for _, h := range hosts {
					fmt.Fprintf(out, "  %s: %s\n", h.Address, shown)
				}
				return nil
			}

			sshOpts, err := sshOptions(cfg)
			if err != nil {
				return err
			}
			results := runner.RunCommand(cmd.Context(), executor.Targets(hosts), sshOpts, command, out, colorOutput(cmd, cfg.Log), gf.conc, false)
			return reportResults(results)
		},
	}
}

// renderRunEnvs Go-templates the global `envs:` values for an ad-hoc run, like the executor does for task cmds, so
// e.g. `TOK: '{{ env "TOK" }}'` exports the resolved value rather than the literal template. One command goes to all
// hosts, so the context is host-less (release_path falls back to the live `current`). Dry-run renders leniently.
func renderRunEnvs(cfg *ast.DeployFile, releaseDir string, dryRun bool) (map[string]string, error) {
	if len(cfg.Envs) == 0 {
		return nil, nil
	}
	ctx := loadTimeContext(cfg)
	ctx.Config, _ = cfg.AsMap()
	ctx.Imports = cfg.Imports
	ctx.ReleasePath = releaseDir
	out := make(map[string]string, len(cfg.Envs))
	for k, v := range cfg.Envs {
		rv, err := varstmpl.RenderWith(v, ctx, !dryRun)
		if err != nil {
			return nil, fmt.Errorf("env %q: %w", k, err)
		}
		out[k] = rv
	}
	return out, nil
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
