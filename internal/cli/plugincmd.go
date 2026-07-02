package cli

import (
	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/deployfile"
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/plugins"
)

// registerPluginCmds adds a stage-action subcommand for each command a plugin contributes (e.g. print-hosts-table's
// deploy:hosts).
// Discovery is offline - the plugins are instantiated without Configure (no network), so it's safe on the
// startup/help/completion path.
// A plugins command whose name collides with a built-in action or an already-registered subcommand (a task) is skipped,
// so built-ins and tasks win.
func registerPluginCmds(stageCmd *cobra.Command, stage string, gf *globalFlags) {
	for _, c := range plugins.Commands(activePluginSpecs(stage)) {
		if reservedActions[c.Name] || hasSubcommand(stageCmd, c.Name) {
			continue
		}
		stageCmd.AddCommand(newPluginCmd(stage, c, gf))
	}
}

// activePluginSpecs returns the plugins specs active for the stage, resolved offline and best-effort: the Deployfile's
// declared plugins (if it loads) plus the default-on plugins, filtered by enabled/only/except.
// On any error it falls back to just the default-on plugins, so a default plugin's commands still register even without
// (or with an invalid) Deployfile.
func activePluginSpecs(stage string) []ast.PluginSpec {
	var declared []ast.PluginSpec
	if path, err := deployfile.Discover(".", ""); err == nil {
		if cfg, err := deployfile.Load(path, stage); err == nil {
			declared = cfg.Plugins
		}
	}
	cfg := &ast.DeployFile{Stage: stage, Plugins: plugins.DefaultSpecs(declared)}
	selectPluginsForStage(cfg)
	return cfg.Plugins
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
			return c.Run(cmd.Context(), cfg, reg, cmd.OutOrStdout(), args)
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
