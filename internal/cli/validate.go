package cli

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/plugins"
)

// newValidateCmd validates the stage's configuration *offline*: it discovers and parses the Deployfile(s), merges
// shared + stage, runs the schema checks (deployfile.Load -> Validate), renders the active plugins' param templates,
// and render-checks every user template (all inside loadOffline, shared with every other command).
// It deliberately does NOT load plugins or run their startup, so it makes no SSH or cloud calls and fetches no dynamic
// inventory - a fast CI / pre-commit gate.
// (For the fully-resolved view including discovered hosts, use `config`/`deploy:hosts`, which do connect.)
func newValidateCmd(stage string, gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the stage's configuration (offline, no host or cloud access)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Offline load: discover + parse + merge + schema Validate, then render the active plugins' param templates.
			// No plugins load, no network/SSH.
			cfg, path, err := loadOffline(cmd, gf, stage)
			if err != nil {
				return err
			}
			// A plugins name that isn't compiled in would only fail later at load, flag it here so validate catches a typo (e.g.
			// `name: awss`).
			for _, p := range cfg.Plugins {
				if !plugins.IsRegistered(p.Name) {
					return fmt.Errorf("unknown plugin %q (not built into this binary)", p.Name)
				}
			}
			slog.Info("configuration is valid", "stage", stage, "deployfile", path, "tasks", len(cfg.Tasks))
			return nil
		},
	}
}
