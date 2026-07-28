package cli

import (
	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/deploy"
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/executor"
	"github.com/yousysadmin/whoosh/internal/plugins"
)

func newDeployCmd(stage string, gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy",
		Short: "Build a new release and publish it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, closeFn, err := newDeployer(cmd, stage, gf)
			if err != nil {
				return err
			}
			defer closeFn()
			return d.Deploy(cmd.Context())
		},
	}
}

func newDeployCheckCmd(stage string, gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "deploy:check",
		Short: "Validate connectivity and ensure the directory tree exists",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, closeFn, err := newDeployer(cmd, stage, gf)
			if err != nil {
				return err
			}
			defer closeFn()
			return d.Check(cmd.Context())
		},
	}
}

// newExecutor wires an executor over a loaded config from the command's flags - the single place the CLI assembles
// executor.Options, shared by the deploy commands and named-task runs so the two paths cannot drift.
func newExecutor(cmd *cobra.Command, cfg *ast.DeployFile, reg *plugins.Registry, gf *globalFlags) (*executor.Executor, error) {
	sshOpts, err := sshOptions(cfg)
	if err != nil {
		return nil, err
	}
	return executor.New(cfg, executor.Options{
		SSH:         sshOpts,
		Out:         cmd.OutOrStdout(),
		DryRun:      gf.dryRun,
		Verbose:     effectiveVerbose(cmd, gf.verbose, cfg.Log),
		Roles:       gf.roles,
		Limit:       gf.limit,
		Concurrency: gf.conc,
		Registry:    reg,
		Color:       colorOutput(cmd, cfg.Log),
	}), nil
}

// newDeployer loads config and wires an executor + Deployer, returning a cleanup function that releases SSH
// connections.
func newDeployer(cmd *cobra.Command, stage string, gf *globalFlags) (*deploy.Deployer, func(), error) {
	cfg, reg, err := loadConfig(cmd.Context(), cmd, gf, stage)
	if err != nil {
		return nil, nil, err
	}
	ex, err := newExecutor(cmd, cfg, reg, gf)
	if err != nil {
		return nil, nil, err
	}
	d, err := deploy.New(cfg, ex)
	if err != nil {
		ex.Close()
		return nil, nil, err
	}
	return d, ex.Close, nil
}
