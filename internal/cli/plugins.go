package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/plugins"
)

// newPluginsCmd lists the plugins compiled into this binary.
// A custom build (built with `whoosh build`) can include third-party plugins, so this is how an operator confirms what
// a given binary actually contains.
func newPluginsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plugins",
		Short: "List the plugins compiled into this binary",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			infos := plugins.RegisteredInfo()
			if len(infos) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no plugins compiled in)")
				return
			}
			for _, p := range infos {
				if p.Version == "" {
					fmt.Fprintln(cmd.OutOrStdout(), p.Name)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", p.Name, p.Version)
				}
			}
		},
	}
}
