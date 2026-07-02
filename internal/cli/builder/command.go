package builder

import "github.com/spf13/cobra"

func NewCommand() *cobra.Command {
	var opts buildOptions
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Compile a custom whoosh binary including the given plugin modules",
		Long: `Compile a whoosh binary that bundles plugins each --with module.

  whoosh build --whoosh-version v1.4.0 --with github.com/acme/whoosh-datadog
  whoosh build --replace github.com/yousysadmin/whoosh=. --with github.com/acme/x=../x`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error { return runBuild(opts) },
	}
	f := cmd.Flags()
	f.StringArrayVar(&opts.withs, "with", nil, "plugin module to include: module[@version] (repeatable)")
	f.StringArrayVar(&opts.replaces, "replace", nil, "go.mod replace: old[@v]=new[@v] or old=./local/path (repeatable)")
	f.StringVarP(&opts.output, "output", "o", "whoosh", "output binary path")
	f.StringVar(&opts.whooshVersion, "whoosh-version", "latest", "whoosh module version to build against")
	f.StringVar(&opts.appVersion, "app-version", "", "version string embedded in the binary (defaults to --whoosh-version)")
	f.StringVar(&opts.tags, "tags", "", `extra go build tags`)
	f.BoolVar(&opts.noStandard, "no-standard", false, "omit whoosh bundled (standard) plugins")
	f.StringVar(&opts.goBin, "go", "go", "path to the go toolchain")
	f.BoolVar(&opts.keep, "keep", false, "keep the temporary build directory")
	f.BoolVar(&opts.verbose, "verbose", false, "print the go commands as they run")
	return cmd
}
