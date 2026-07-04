package plugintemplate

// CLI subcommands (whoosh.Commander, optional): each Command becomes `whoosh <stage> <name>`.
// Commands() is queried on a BARE instance (no Configure, no network) so `--help` and completion stay fast - keep it
// side-effect free and read config only inside Run, which receives the fully resolved DeployFile and the registry.
// Built-in commands and task names win on a name collision, so keep command names namespaced.

import (
	"context"
	"fmt"
	"io"

	"github.com/yousysadmin/whoosh"
)

// Commands contributes `whoosh <stage> plugin-template:status`.
// TODO: replace with your real commands, or delete this file (and nothing else - the interface is optional).
func (p *plugin) Commands() []whoosh.Command {
	return []whoosh.Command{{
		Name:  pluginName + ":status",
		Short: "Show what the " + pluginName + " plugin contributes for this stage",
		Run: func(_ context.Context, cfg *whoosh.DeployFile, _ *whoosh.Registry, out io.Writer, _ []string) error {
			// Note: this instance is bare - p.global/p.client are zero. Read state from cfg (resolved config,
			// startup hooks already ran) instead.
			ours := 0
			for _, h := range cfg.Hosts {
				if h.Source == FeatureInventory {
					ours++
				}
			}
			fmt.Fprintf(out, "%s %s - stage %s: %d host(s) total, %d discovered by %s\n",
				pluginName, pluginVersion, cfg.Stage, len(cfg.Hosts), ours, FeatureInventory)
			if _, ok := cfg.Tasks[taskSetup]; ok {
				fmt.Fprintf(out, "setup task %q is contributed\n", taskSetup)
			}
			return nil
		},
	}}
}
