package plugins

import (
	"context"
	"io"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

// CommandFunc runs a plugin-provided CLI command, invoked as `whoosh <stage> <Name>`.
// It receives the fully-loaded config (servers/inventory resolved by startup hooks), the populated registry
// (to call a registered action if needed), the console writer, and the command's positional args.
type CommandFunc func(ctx context.Context, cfg *ast.DeployFile, reg *Registry, out io.Writer, args []string) error

// Command is a stage-action subcommand a plugin contributes (`whoosh <stage> <Name>`), declared via the Commander interface.
type Command struct {
	Name  string // the action name, e.g. "deploy:hosts"
	Short string // one-line help
	Run   CommandFunc
}

// Commander is the optional interface a plugin implements to contribute CLI commands.
// It is queried on a bare (unconfigured) plugin instance so the CLI can register the subcommands offline - no
// Configure, no network - which keeps `--help` and shell completion fast.
// A command's Run receives the fully-loaded config at execution time, so it doesn't depend on the plugin's configured
// state.
type Commander interface {
	Commands() []Command
}

// Commands instantiates each spec's plugins via its factory WITHOUT calling Configure (so no clients are built and no
// network is touched) and collects the commands it declares.
// A spec naming a plugin not built into this binary is skipped (the missing plugins surfaces at load time, not here).
// Used by the CLI to register plugins subcommands offline.
func Commands(specs []ast.PluginSpec) []Command {
	var out []Command
	for _, spec := range specs {
		f, ok := factories[spec.Name]
		if !ok {
			continue
		}
		if c, ok := f().(Commander); ok {
			out = append(out, c.Commands()...)
		}
	}
	return out
}
