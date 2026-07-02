package cli

import "github.com/spf13/cobra"

func newRollbackCmd(stage string, gf *globalFlags) *cobra.Command {
	var cleanup bool
	cmd := &cobra.Command{
		Use:   "deploy:rollback",
		Short: "Repoint current at the previous release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, closeFn, err := newDeployer(cmd, stage, gf)
			if err != nil {
				return err
			}
			defer closeFn()
			return d.Rollback(cmd.Context(), cleanup)
		},
	}
	cmd.Flags().BoolVar(&cleanup, "cleanup", false, "remove the rolled-back release after switching")
	return cmd
}

func newReleasesCmd(stage string, gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "releases",
		Short: "List releases on each host",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, closeFn, err := newDeployer(cmd, stage, gf)
			if err != nil {
				return err
			}
			defer closeFn()
			return d.Releases(cmd.Context())
		},
	}
}

func newUnlockCmd(stage string, gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy:unlock",
		Short: "Clear a stale deploy lock",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, closeFn, err := newDeployer(cmd, stage, gf)
			if err != nil {
				return err
			}
			defer closeFn()
			return d.Unlock(cmd.Context())
		},
	}
}
