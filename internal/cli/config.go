package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/yousysadmin/whoosh/internal/masking"
)

func newConfigCmd(stage string, gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the resolved configuration for the stage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := loadConfig(cmd.Context(), cmd, gf, stage)
			if err != nil {
				return err
			}

			b, err := yaml.Marshal(cfg)
			if err != nil {
				return fmt.Errorf("marshal config: %w", err)
			}

			// The resolved vars/params may carry envSecret values - redact the dump like every other output path.
			out := masking.NewWriter(cmd.OutOrStdout())
			if _, err := fmt.Fprintf(out, "# resolved config for stage %q\n%s", stage, b); err != nil {
				return err
			}

			return out.Flush()
		},
	}
}
