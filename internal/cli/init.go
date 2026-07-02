package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/deployfile"
)

func newInitCmd() *cobra.Command {
	var stages []string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold a Whooshfile and per-stage files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd, stages)
		},
	}
	cmd.Flags().StringSliceVar(&stages, "stages", []string{"staging", "production"},
		"stages to scaffold under "+deployfile.StageDirs[0]+"/")
	return cmd
}

func runInit(cmd *cobra.Command, stages []string) error {
	out := cmd.OutOrStdout()

	if err := writeIfAbsent(out, deployfile.DefaultDeployfiles[0], deployfile.DeployfileScaffold()); err != nil {
		return err
	}

	stageDir := deployfile.StageDirs[0]
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return fmt.Errorf("create %s/: %w", stageDir, err)
	}
	for _, stage := range stages {
		path := filepath.Join(stageDir, stage+".yml")
		if err := writeIfAbsent(out, path, deployfile.StageScaffold(stage)); err != nil {
			return err
		}
	}

	// Scripts directory referenced by task `scripts:` entries.
	scriptsDir := filepath.Join(stageDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		return fmt.Errorf("create %s/: %w", scriptsDir, err)
	}
	if err := writeIfAbsent(out, filepath.Join(scriptsDir, "example.sh"), deployfile.ExampleScript()); err != nil {
		return err
	}

	fmt.Fprintln(out, "\nEdit the files above, then run: whoosh <stage> deploy")
	return nil
}

// writeIfAbsent writes data to path unless the file already exists.
func writeIfAbsent(out io.Writer, path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(out, "  skip   %s (already exists)\n", path)
		return nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Fprintf(out, "  create %s\n", path)
	return nil
}
