package cli

import (
	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/masking"
	"github.com/yousysadmin/whoosh/internal/plugins"
)

// registerPluginCmds adds a stage-action subcommand for each command a plugin contributes (e.g. print-hosts-table's
// deploy:hosts). declared are the Deployfile's plugin specs parsed by newStageCmd (nil without a loadable
// Deployfile), layered with the default-on plugins and filtered by enabled/only/except - so a default plugin's
// commands register even without (or with an invalid) Deployfile.
// Discovery is offline - the plugins are instantiated without Configure (no network), so it's safe on the
// startup/help/completion path.
// A plugins command whose name collides with a built-in action or an already-registered subcommand (a task) is skipped,
// so built-ins and tasks win.
func registerPluginCmds(stageCmd *cobra.Command, stage string, declared []ast.PluginSpec, gf *globalFlags) {
	cfg := &ast.DeployFile{Stage: stage, Plugins: plugins.DefaultSpecs(declared)}
	selectPluginsForStage(cfg)
	for _, c := range plugins.Commands(cfg.Plugins) {
		if reservedActions[c.Name] || hasSubcommand(stageCmd, c.Name) {
			continue
		}
		stageCmd.AddCommand(newPluginCmd(stage, c, gf))
	}
}

// newPluginCmd wraps a plugin command as a cobra subcommand: it loads the full config (plugins + startup hooks, so
// servers/inventory are resolved) and runs the command's Run with the console writer, mirroring the built-in stage
// commands.
func newPluginCmd(stage string, c plugins.Command, gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:          c.Name,
		Short:        c.Short,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, reg, err := loadConfig(cmd.Context(), cmd, gf, stage)
			if err != nil {
				return err
			}
			// Redact secrets like every other output path (config, run, the executor).
			out := masking.NewWriter(cmd.OutOrStdout())
			defer out.Flush()
			return c.Run(cmd.Context(), cfg, reg, out, args)
		},
	}
}

// hasSubcommand reports whether cmd already has a subcommand with this name.
func hasSubcommand(cmd *cobra.Command, name string) bool {
	for _, sub := range cmd.Commands() {
		if sub.Name() == name {
			return true
		}
	}
	return false
}
