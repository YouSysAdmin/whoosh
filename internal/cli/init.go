package cli

import (
	"fmt"
	"log/slog"
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
		RunE: func(_ *cobra.Command, _ []string) error {
			return runInit(stages)
		},
	}
	cmd.Flags().StringSliceVar(&stages, "stages", []string{"staging", "production"},
		"stages to scaffold under "+deployfile.StageDirs[0]+"/")
	return cmd
}

func runInit(stages []string) error {
	if err := writeIfAbsent(deployfile.DefaultDeployfiles[0], deployfile.DeployfileScaffold()); err != nil {
		return err
	}

	stageDir := deployfile.StageDirs[0]
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return fmt.Errorf("create %s/: %w", stageDir, err)
	}
	for _, stage := range stages {
		path := filepath.Join(stageDir, stage+".yml")
		if err := writeIfAbsent(path, deployfile.StageScaffold(stage)); err != nil {
			return err
		}
	}

	// Scripts directory referenced by task `scripts:` entries.
	scriptsDir := filepath.Join(stageDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		return fmt.Errorf("create %s/: %w", scriptsDir, err)
	}
	if err := writeIfAbsent(filepath.Join(scriptsDir, "example.sh"), deployfile.ExampleScript()); err != nil {
		return err
	}

	slog.Info("init complete - edit the generated files", "next", "whoosh <stage> deploy")
	return nil
}

// writeIfAbsent writes data to path unless the file already exists.
func writeIfAbsent(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		slog.Info("skipped, already exists", "path", path)
		return nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	slog.Info("created", "path", path)
	return nil
}
