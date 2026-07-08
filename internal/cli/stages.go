package cli

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/deployfile"
)

// newStagesCmd lists the stages available next to the Deployfile (the plain files in the stage dirs), each with the
// stage file's root `description:`. The listing is a data dump, so it streams to stdout, not through slog.
func newStagesCmd() *cobra.Command {
	var deployfilePath string
	cmd := &cobra.Command{
		Use:          "stages",
		Short:        "List the available stages",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := deployfile.Discover(".", deployfilePath)
			if err != nil {
				return err
			}
			stages, err := deployfile.ListStages(filepath.Dir(path))
			if err != nil {
				return err
			}
			if len(stages) == 0 {
				slog.Warn("no stage files found", "deployfile", path, "dirs", deployfile.StageDirs)
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, s := range stages {
				fmt.Fprintf(w, "%s\t%s\n", s.Name, s.Description)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&deployfilePath, "deployfile", "", "path to the Deployfile (default: auto-discover)")
	return cmd
}

// completeStages offers the available stage names (with their descriptions) when completing the root command's first
// positional argument, so `whoosh <TAB>` lists the stages alongside the built-in commands. Best-effort: with no (or an
// unreadable) Deployfile it offers nothing.
func completeStages(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	path, err := deployfile.Discover(".", "")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	stages, err := deployfile.ListStages(filepath.Dir(path))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, s := range stages {
		if !strings.HasPrefix(s.Name, toComplete) {
			continue
		}
		if s.Description != "" {
			out = append(out, s.Name+"\t"+s.Description)
		} else {
			out = append(out, s.Name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
