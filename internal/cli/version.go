package cli

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/plugins"
	"github.com/yousysadmin/whoosh/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the whoosh version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", resolveVersion())
			if infos := plugins.RegisteredInfo(); len(infos) > 0 {
				parts := make([]string, len(infos))
				for i, p := range infos {
					if p.Version == "" {
						parts[i] = p.Name
					} else {
						parts[i] = p.Name + " " + p.Version
					}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "plugins: %s\n", strings.Join(parts, ", "))
			}
		},
	}
}

// resolveVersion prefers the ldflags value, falling back to VCS info embedded by the Go toolchain (go install of a
// tagged module).
func resolveVersion() string {
	// version.Version is rewritten at link time by -ldflags "-X …/version.Version=…" (goreleaser, the Makefile,
	// `whoosh build --app-version`). Static analysis can't see that override and folds its "dev" initializer,
	// flagging this check as a constant condition - it isn't.
	//goland:noinspection GoBoolExpressions
	if version.Version != "" && version.Version != "dev" {
		return version.Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version.Version
}
