// Package cli wires up the whoosh command-line interface.
//
// The first argument is the stage, the second is the action, e.g. "whoosh production deploy".
// The stage is dynamic (any file under deploy/), so it is resolved from the arguments and registered as a command at
// startup. "init" is the one stage-less command.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/cli/builder"
	"github.com/yousysadmin/whoosh/internal/deployfile"
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/errors"
	"github.com/yousysadmin/whoosh/internal/masking"
	"github.com/yousysadmin/whoosh/internal/paths"
	"github.com/yousysadmin/whoosh/internal/plugins"
	"github.com/yousysadmin/whoosh/internal/varstmpl"
)

// globalFlags are shared by every stage action.
type globalFlags struct {
	deployfile string
	verbose    bool
	dryRun     bool
	roles      []string
	limit      []string
	conc       int
}

// Execute runs the CLI and exits non-zero on error.
// The error is logged through slog (like the rest of whoosh's narrative) rather than printed by cobra.
// A SIGINT/SIGTERM cancels the command's context, so in-flight SSH sessions are killed and deferred cleanup (e.g.
// releasing the deploy lock) still runs - rather than the OS abruptly terminating the process.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRootCmd(os.Args[1:]).ExecuteContext(ctx); err != nil {
		// An operator interrupt (Ctrl-C / SIGTERM) cancels the root context and the work unwinds with a context.
		// Canceled - that is intentional, not a failure, so report it calmly and exit 130.
		// A server-dropped connection surfaces as a different error (UnreachableError) and still reports as a failure below.
		if interrupted(ctx.Err(), err) {
			slog.Warn("interrupted")
			os.Exit(errors.CodeInterrupted)
		}
		slog.Error("command failed", "error", err)
		// Exit with the error's category code (config/unreachable/command/...) so a caller can distinguish failures, a plain
		// error falls back to 1.
		os.Exit(errors.Code(err))
	}
}

// interrupted reports whether err is the result of an operator signal: the root context was canceled (Ctrl-C / SIGTERM
// fired) and the error is the resulting cancellation - as opposed to a real failure or a server-dropped connection
// (which is an UnreachableError and leaves the root context intact).
func interrupted(rootErr, err error) bool {
	return rootErr != nil && err != nil && errors.Is(err, context.Canceled)
}

func newRootCmd(args []string) *cobra.Command {
	lf := &logFlags{}
	// Fresh logging state for this invocation (the CLI normally runs once, tests may call Execute repeatedly).
	if logState.file != nil {
		_ = logState.file.Close()
	}
	logState.base, logState.file = nil, nil
	root := &cobra.Command{
		Use:   "whoosh",
		Short: "Deployment tools, driven by a Deployfile",
		Long: "whoosh deploys applications over SSH using a release/symlink layout.\n\n" +
			"Run actions against a stage:  whoosh <stage> <action>\n" +
			"  whoosh production deploy\n" +
			"  whoosh staging config\n\n" +
			"Scaffold a new project:       whoosh init",
		SilenceUsage: true,
		// Errors are surfaced via slog in Execute, not printed by cobra.
		SilenceErrors: true,
		// Install the slog logger before any command runs.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := setupLogging(cmd, lf.level, lf.format, lf.output, lf.color, lf.file, lf.fileFormat); err != nil {
				return err
			}
			applyMasking(lf.level)
			return nil
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&lf.level, "log-level", "info", "log level: debug, info, warn, error")
	pf.StringVar(&lf.format, "log-format", "text", "log format: text or json")
	pf.StringVar(&lf.output, "log-output", "stdout", "log output: stdout, stderr, or a file path")
	pf.BoolVar(&lf.color, "log-color", true, "colorize text logs (terminal only)")
	pf.StringVar(&lf.file, "log-file", "", "also write a deploy log to this file (in addition to --log-output)")
	pf.StringVar(&lf.fileFormat, "log-file-format", "text", "format for --log-file: text (full transcript incl. command output) or json (narrative only)")

	root.AddCommand(newInitCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newPluginsCmd())
	root.AddCommand(builder.NewCommand())

	// The stage is data, not a fixed command, so register it dynamically from the arguments and hang the action
	// subcommands off it.
	if stage, ok := detectStage(args); ok {
		root.AddCommand(newStageCmd(stage))
	}
	return root
}

// reservedFirstArgs are top-level tokens that are never stage names.
var reservedFirstArgs = map[string]bool{
	"init":       true,
	"help":       true,
	"completion": true,
	"version":    true,
	"plugins":    true,
	"build":      true,
}

// completionDrivers are the tokens cobra's shell-completion harness puts first (`whoosh __complete <real args...>`).
// They are skipped during stage detection so the stage is found in the actual command line being completed - otherwise
// `whoosh <stage> <TAB>` wouldn't register the stage and couldn't offer its tasks.
var completionDrivers = map[string]bool{
	"__complete":       true,
	"__completeNoDesc": true,
}

// valuedRootFlags are the root persistent flags that take a value as a separate argument.
// When one appears before the stage in its space-separated form, the following token is the flag's value, not the stage
// - so it must be skipped.
// The --flag=value form is a single token and needs no special handling. --log-color is a bool flag and never consumes
// a separate value, so it is not listed here.
var valuedRootFlags = map[string]bool{
	"--log-level":       true,
	"--log-format":      true,
	"--log-output":      true,
	"--log-file":        true,
	"--log-file-format": true,
}

// detectStage returns the first positional argument, treated as the stage name, unless it is a flag or a reserved
// command. Global stage flags are expected after the action (e.g.
// "whoosh staging deploy --verbose"), root flags placed before the stage are tolerated in either "--flag value" or
// "--flag=value" form.
func detectStage(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "" {
			continue
		}
		// Skip cobra's completion driver token so the stage is detected from the real command line being completed (`whoosh
		// __complete <stage> ...`), letting `whoosh <stage> <TAB>` register the stage and offer its tasks.
		if completionDrivers[a] {
			continue
		}
		if strings.HasPrefix(a, "-") {
			// A space-separated root flag consumes the next token as its value.
			if valuedRootFlags[a] {
				i++
			}
			continue
		}
		if reservedFirstArgs[a] {
			return "", false
		}
		return a, true
	}
	return "", false
}

// newStageCmd builds the command for a stage and attaches its actions.
// The shared globalFlags are registered as persistent flags so they work after any action.
func newStageCmd(stage string) *cobra.Command {
	gf := &globalFlags{}
	cmd := &cobra.Command{
		Use:          stage,
		Short:        fmt.Sprintf("Run actions against the %q stage", stage),
		SilenceUsage: true,
	}

	pf := cmd.PersistentFlags()
	pf.StringVar(&gf.deployfile, "deployfile", "", "path to the Deployfile (default: auto-discover)")
	pf.BoolVarP(&gf.verbose, "verbose", "v", false, "verbose output")
	pf.BoolVar(&gf.dryRun, "dry-run", false, "print the plan without executing")
	pf.StringSliceVar(&gf.roles, "roles", nil, "limit to these roles")
	pf.StringSliceVarP(&gf.limit, "host", "H", nil, "restrict to these hosts (repeatable / comma-separated)")
	pf.IntVar(&gf.conc, "concurrency", 0, "max hosts to run a command on at once (0 = all)")

	cmd.AddCommand(newConfigCmd(stage, gf))
	cmd.AddCommand(newValidateCmd(stage, gf))
	cmd.AddCommand(newRunCmd(stage, gf))
	cmd.AddCommand(newDeployCmd(stage, gf))
	cmd.AddCommand(newDeployCheckCmd(stage, gf))
	cmd.AddCommand(newRollbackCmd(stage, gf))
	cmd.AddCommand(newReleasesCmd(stage, gf))
	cmd.AddCommand(newUnlockCmd(stage, gf))
	registerTaskCmds(cmd, stage, gf)
	// Plugin-contributed actions (e.g. print-hosts-table's deploy:hosts).
	// Added last so a built-in or a task of the same name wins.
	registerPluginCmds(cmd, stage, gf)
	return cmd
}

// reservedActions are action names that take precedence over Deployfile tasks of the same name.
var reservedActions = map[string]bool{
	"config":          true,
	"validate":        true,
	"run":             true,
	"deploy":          true,
	"deploy:check":    true,
	"deploy:rollback": true,
	"deploy:unlock":   true,
	"releases":        true,
}

// registerTaskCmds discovers task names from the shared Deployfile merged with the stage file and adds a subcommand for
// each, so "whoosh <stage> <task>" works - including tasks defined only in deploy/<stage>.yml.
// Discovery is best-effort: if no Deployfile is found, only the built-in actions are present.
func registerTaskCmds(stageCmd *cobra.Command, stage string, gf *globalFlags) {
	path, err := deployfile.Discover(".", "")
	if err != nil {
		return
	}
	tasks, err := deployfile.TasksForStage(path, stage)
	if err != nil {
		return
	}
	for name, t := range tasks {
		if t == nil || reservedActions[name] {
			continue
		}
		// A task gated out of this stage (only/except) is still registered so a hook referencing it resolves and an explicit
		// run logs a skip - but omit it from this stage's listing, like a hidden task.
		hidden := t.Hidden || !t.ActiveForStage(stage)
		stageCmd.AddCommand(newTaskCmd(stage, name, t.Desc, hidden, gf))
	}
}

// loadTimeContext builds the template context available at config-load time: the stage's vars plus the static config
// (app, stage, paths) and the env_files values for the env/envSecret funcs - not run-time values such as release_path
// or commit_hash, which don't exist yet.
func loadTimeContext(cfg *ast.DeployFile) varstmpl.Context {
	layout := paths.For(cfg.App.DeployTo)
	return varstmpl.Context{
		Vars:          cfg.Vars,
		AppName:       cfg.App.Name,
		Repo:          cfg.App.Repo,
		Branch:        cfg.App.Branch,
		Stage:         cfg.Stage,
		DeployTo:      layout.DeployTo,
		ReleasesPath:  layout.ReleasesPath,
		SharedPath:    layout.SharedPath,
		RepoPath:      layout.RepoPath,
		CurrentPath:   layout.CurrentPath,
		EnvFileValues: cfg.EnvFileValues,
	}
}

// renderVars renders the string values in vars: as Go templates, once at load time, so e.g.
// `app_version: '{{ env "APP_VERSION" }}'` resolves from the process env / env_files before anything consumes the var.
// The context is the static load-time one (loadTimeContext) minus the vars themselves - a var cannot reference another
// var, run-time keys render empty, and strict rendering surfaces anything else undefined.
func renderVars(cfg *ast.DeployFile) error {
	if len(cfg.Vars) == 0 {
		return nil
	}
	ctx := loadTimeContext(cfg)
	ctx.Vars = nil
	rendered, err := varstmpl.RenderParams(cfg.Vars, ctx, true)
	if err != nil {
		return fmt.Errorf("vars: %w", err)
	}
	cfg.Vars = rendered
	return nil
}

// renderSSHSecrets renders the SSH auth secrets as Go templates at load time, so a passphrase can come from the
// environment (e.g. `passphrase: '{{ envSecret "KEY_PASS" }}'`): each identity's path, content, and passphrase, the
// global ssh.identity_file_passphrase, and each host's identity_file_passphrase. The context is the static load-time
// one (loadTimeContext) with the already-rendered vars, strict rendering surfaces an undefined var.
// The rendered secrets are registered with the masker, so they never reach logs or error output.
// Hosts a plugin discovers later arrive after this pass and keep their passphrase verbatim - only the inherited
// global one is templated (ApplyDefaults copies the rendered value onto them).
func renderSSHSecrets(cfg *ast.DeployFile) error {
	ctx := loadTimeContext(cfg)
	renderSecret := func(label string, value *string) error {
		if *value == "" {
			return nil
		}
		rendered, err := varstmpl.RenderWith(*value, ctx, true)
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		*value = rendered
		masking.AddSecret(rendered)
		return nil
	}
	if err := renderSecret("ssh.identity_file_passphrase", &cfg.SSH.IdentityFilePassphrase); err != nil {
		return err
	}
	for i := range cfg.Hosts {
		if err := renderSecret(fmt.Sprintf("hosts[%d].identity_file_passphrase", i), &cfg.Hosts[i].IdentityFilePassphrase); err != nil {
			return err
		}
	}
	for _, name := range sortedKeys(cfg.SSH.Identities) {
		id := cfg.SSH.Identities[name]
		for _, f := range []struct {
			label string
			value *string
		}{
			{"path", &id.Path},
			{"content", &id.Content},
			{"passphrase", &id.Passphrase},
		} {
			if *f.value == "" {
				continue
			}
			rendered, err := varstmpl.RenderWith(*f.value, ctx, true)
			if err != nil {
				return fmt.Errorf("ssh.identities.%s.%s: %w", name, f.label, err)
			}
			*f.value = rendered
		}
		if id.Content != "" {
			masking.AddSecret(id.Content)
		}
		if id.Passphrase != "" {
			masking.AddSecret(id.Passphrase)
		}
		cfg.SSH.Identities[name] = id
	}
	return nil
}

// renderPluginParams renders each plugin's params as Go templates before the plugins load, so they can be
// parameterized per stage via vars - e.g. `credentials_from_host: { host: "{{ .bastion }}" }` with `bastion` set (and
// overridden) in the stage files.
// Plugins load at startup, before any release exists, so the context is the load-time one (loadTimeContext) - the
// stage's (already rendered) vars, the static config, and env/env_files - not run-time values such as release_path or
// commit_hash. Strict rendering surfaces an undefined var.
func renderPluginParams(cfg *ast.DeployFile) error {
	if len(cfg.Plugins) == 0 {
		return nil
	}
	ctx := loadTimeContext(cfg)
	for i := range cfg.Plugins {
		p := &cfg.Plugins[i]
		rendered, err := varstmpl.RenderParams(p.Params, ctx, true)
		if err != nil {
			return fmt.Errorf("plugins %q params: %w", p.Name, err)
		}
		p.Params = rendered
		for j := range p.Actions {
			ra, err := varstmpl.RenderParams(p.Actions[j].Params, ctx, true)
			if err != nil {
				return fmt.Errorf("plugins %q action %q params: %w", p.Name, p.Actions[j].Name, err)
			}
			p.Actions[j].Params = ra
		}
	}
	return nil
}

// selectPluginsForStage splits cfg.Plugins into the ones to load (kept) and records the names of the rest in
// cfg.SkippedPlugins.
// A plugins is dropped when it is disabled (enabled: false) or inactive for the stage (only/except).
// Filtering before rendering means a skipped plugin's templated params (whose vars may be undefined in this stage) are
// never evaluated.
func selectPluginsForStage(cfg *ast.DeployFile) {
	active := make([]ast.PluginSpec, 0, len(cfg.Plugins))
	var skipped []string
	for _, p := range cfg.Plugins {
		if p.IsEnabled() && p.ActiveForStage(cfg.Stage) {
			active = append(active, p)
		} else {
			skipped = append(skipped, p.Name)
		}
	}
	cfg.Plugins = active
	cfg.SkippedPlugins = skipped
}

// loadOffline is the offline part of config loading, shared by loadConfig and the validate command: discover + parse +
// merge + schema-validate the Deployfile, then select the stage's active plugins and render their param templates.
// It makes no network/SSH calls and does not load plugins or run startup hooks.
// It returns the resolved config and the Deployfile path.
func loadOffline(cmd *cobra.Command, gf *globalFlags, stage string) (*ast.DeployFile, string, error) {
	path, err := deployfile.Discover(".", gf.deployfile)
	if err != nil {
		return nil, "", err
	}
	cfg, err := deployfile.Load(path, stage)
	if err != nil {
		return nil, "", err
	}
	// Re-apply logging now that the Deployfile's log: config is known (CLI --log-* flags still take priority over it).
	if err := applyLogConfig(cmd, &cfg.Log); err != nil {
		return nil, "", err
	}
	// Resolve templated vars first, so plugin params (below) and everything downstream see final values.
	if err := renderVars(cfg); err != nil {
		return nil, "", err
	}
	// Resolve ssh secret templates next (they may reference vars), so a broken identity or passphrase template fails
	// offline and the secret values are registered with the masker before anything can print them.
	if err := renderSSHSecrets(cfg); err != nil {
		return nil, "", err
	}
	// Add always-on (default) plugins not already declared, then drop plugins not active for this stage (and record them,
	// so action tasks bound to them are skipped rather than failing), then render the survivors.
	cfg.Plugins = plugins.DefaultSpecs(cfg.Plugins)
	selectPluginsForStage(cfg)
	if err := renderPluginParams(cfg); err != nil {
		return nil, "", err
	}
	// Render-check every user template (envs, cmds, dir, with, scripts) as the last load step, so any command fails
	// up front on a config mistake - before plugins load (which may dial SSH/cloud for credentials or inventory).
	if err := reportTemplateFindings(cmd.OutOrStdout(), checkTemplates(cfg)); err != nil {
		return nil, "", err
	}
	return cfg, path, nil
}

// loadConfig loads the resolved config for the stage (loadOffline, which ends with the template check), then loads
// the declared plugins and runs their startup hooks (which may populate Servers). The returned registry supplies
// plugins actions to action tasks.
func loadConfig(ctx context.Context, cmd *cobra.Command, gf *globalFlags, stage string) (*ast.DeployFile, *plugins.Registry, error) {
	cfg, _, err := loadOffline(cmd, gf, stage)
	if err != nil {
		return nil, nil, err
	}
	reg, err := plugins.Load(cfg.Plugins)
	if err != nil {
		return nil, nil, err
	}
	if err := reg.RunStartup(ctx, cfg); err != nil {
		return nil, nil, fmt.Errorf("plugins startup: %w", err)
	}
	cfg.ApplyDefaults()
	return cfg, reg, nil
}
