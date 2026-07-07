// Package executor runs Deployfile tasks: it resolves task dependencies, renders each command against the deploy
// context, and executes it either locally or across the hosts matching the task's roles (reusing SSH connections).
package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/yousysadmin/whoosh/internal/deployfile"
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/masking"
	"github.com/yousysadmin/whoosh/internal/operator"
	"github.com/yousysadmin/whoosh/internal/paths"
	"github.com/yousysadmin/whoosh/internal/plugins"
	"github.com/yousysadmin/whoosh/internal/runner"
	"github.com/yousysadmin/whoosh/internal/varstmpl"
)

// Executor holds the state shared across task runs for one stage.
type Executor struct {
	cfg         *ast.DeployFile
	out         io.Writer
	dryRun      bool
	verbose     bool
	roles       []string // global role filter (intersected with task roles)
	limit       []string // global host filter
	concurrency int      // max hosts running a command at once (0 = unbounded)
	scriptsDir  string
	env         map[string]string // default env exported for every task command
	cluster     *runner.Cluster
	reg         *plugins.Registry
	base        varstmpl.Context
	stateMu     sync.Mutex // guards writes to base.Tasks (task state)

	// logMode routes command output and the echoed commands through slog as structured records instead of streaming
	// them raw (cfg.Log.raw_remote_log: false). logTask names the task currently producing output; it is set/restored
	// in runTask (sequential) and read by the cluster's line handler from the per-host goroutines it brackets.
	logMode bool
	logTask string
	// color colorizes the host prefix on the raw stream (green) - the echoed command here, and command output via the
	// cluster. Resolved by the CLI (color enabled and a terminal destination).
	color bool
	// capture, when non-nil, diverts the running task's output into a buffer instead of the console (a silent_output
	// task); endCapture flushes it only if the task failed. Set/cleared in runTask around the dispatch.
	capture *captureSink

	// skipped is the set of plugins names inactive for this stage, an action task whose action namespace is in it is
	// skipped (logged) rather than failing.
	skipped map[string]bool

	unreachableMu sync.Mutex
	unreachable   map[string]bool // hosts dropped by on_unreachable: skip
}

// Options configure an Executor.
// SSH carries the connection settings (ignored for local targets), Out is where command output and dry-run plans are
// written, Roles/Limit are the global --roles/--limit filters, Concurrency bounds how many hosts run a command at once
// (0 = unbounded), Registry supplies plugins-registered actions (nil when no plugins are loaded).
type Options struct {
	SSH         runner.Options
	Out         io.Writer
	DryRun      bool
	Verbose     bool
	Roles       []string
	Limit       []string
	Concurrency int
	Registry    *plugins.Registry
	// Color colorizes the host prefix on the raw command-output stream (green for stdout / the echoed command, red for
	// stderr). The CLI sets it when --log-color is on and the destination is a terminal.
	Color bool
}

// New builds an executor for the resolved config.
// Call Close when done to release connections and flush buffered output.
func New(cfg *ast.DeployFile, opts Options) *Executor {
	// Redact secrets from everything written to out - command output, dry-run plans, and local task output all flow
	// through here (or the cluster below).
	rout := masking.NewWriter(opts.Out)
	layout := paths.For(cfg.App.DeployTo)
	// Expose the whole resolved config to templates as {{.config}}.
	// Marshaling a valid config can't fail, on the off chance it does, templates simply see a nil config rather than the
	// executor failing to build.
	cfgMap, _ := cfg.AsMap()
	base := varstmpl.Context{
		Vars:         cfg.Vars,
		AppName:      cfg.App.Name,
		Repo:         cfg.App.Repo,
		Branch:       cfg.App.Branch,
		KeepReleases: cfg.App.KeepReleases,
		Stage:        cfg.Stage,
		Deployer:     operator.Name(),
		DeployTo:     layout.DeployTo,
		ReleasesPath: layout.ReleasesPath,
		SharedPath:   layout.SharedPath,
		RepoPath:     layout.RepoPath,
		CurrentPath:  layout.CurrentPath,
		Config:       cfgMap,
		// Standalone task runs operate on the live release.
		ReleasePath: layout.CurrentPath,
		// Run-scoped state from tasks declaring `output:` (the live map, renders copy the surrounding context shallowly but
		// share this map by reference).
		Tasks: map[string]any{},
		// Values a plugin injected at load (e.g. SSM params): {{ .ssm.<key> }}.
		Imports: cfg.Imports,
		// env_files values, consulted by {{ env }}/{{ envSecret }} when the process var is unset - consistent with
		// env_files being the base layer of the task shell env (execEnv).
		EnvFileValues: cfg.EnvFileValues,
	}
	e := &Executor{
		cfg:         cfg,
		out:         rout,
		dryRun:      opts.DryRun,
		verbose:     opts.Verbose,
		roles:       opts.Roles,
		limit:       opts.Limit,
		concurrency: opts.Concurrency,
		scriptsDir:  deployfile.ScriptsLocation(cfg),
		env:         cfg.Envs,
		cluster:     runner.NewCluster(opts.SSH, rout),
		reg:         opts.Registry,
		base:        base,
		skipped:     namespaceSet(cfg.SkippedPlugins),
		unreachable: map[string]bool{},
		logMode:     !cfg.Log.RawOutput(),
		color:       opts.Color,
	}
	if e.logMode {
		// Route command output (cluster stdout/stderr, including local: true hosts) through the structured logger.
		e.cluster.SetLineHandler(e.logLine)
	} else {
		// Raw streaming: colorize the host prefix (no effect in log mode).
		e.cluster.SetColor(e.color)
	}
	return e
}

// namespaceSet builds a lookup of plugins names whose action tasks are skipped.
func namespaceSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// actionNamespace returns the plugins namespace of an action name - the segment before the first colon
// ("aws:ec2:ami:create" -> "aws").
func actionNamespace(action string) string {
	if i := strings.IndexByte(action, ':'); i > 0 {
		return action[:i]
	}
	return action
}

// Close releases pooled SSH connections and flushes any buffered output.
func (e *Executor) Close() {
	e.cluster.Close()
	if rw, ok := e.out.(*masking.Writer); ok {
		_ = rw.Flush()
	}
}

// Out returns the writer command output is streamed to.
func (e *Executor) Out() io.Writer { return e.out }

// DryRun reports whether commands are printed instead of executed.
func (e *Executor) DryRun() bool { return e.dryRun }

// Hosts returns the stage hosts eligible for execution: those with deploy enabled (the default), after applying the
// global --roles/--host filters. The deploy lifecycle runs its built-in steps on all of these.
// Hosts with deploy:false are inventory-only and excluded here.
func (e *Executor) Hosts() []ast.Host {
	hosts := ast.FilterDeployable(e.cfg.Hosts)
	hosts = ast.FilterByRoles(hosts, e.roles)
	hosts = ast.FilterByAddresses(hosts, e.limit)
	return e.filterExcluded(hosts)
}

// RunOn executes a literal command on each host (no templating), reusing connections.
// In dry-run mode it prints the command per host instead. It fails on the first host error.
func (e *Executor) RunOn(ctx context.Context, hosts []ast.Host, command string) error {
	results := e.runOn(ctx, hosts, command, true)
	if runner.Failed(results) {
		return firstError(results)
	}
	return nil
}

// RunOnReport runs a literal command on each host and returns every host's result without failing fast, so the caller
// can apply a per-host policy (the deploy lifecycle's on_unreachable). In dry-run it prints the plan and returns nil.
// Use RunOn when any failure should abort.
func (e *Executor) RunOnReport(ctx context.Context, hosts []ast.Host, command string) []runner.Result {
	return e.runOn(ctx, hosts, command, false)
}

// runOn is the shared body of RunOn/RunOnReport: the dry-run plan, the verbose echo, then the fanout with the given
// fail-fast policy. Dry-run (and an empty host list) returns nil results, like the live echo, the built-in command is
// dumped per host only under --verbose (the phase narrative already names each step).
func (e *Executor) runOn(ctx context.Context, hosts []ast.Host, command string, failFast bool) []runner.Result {
	if len(hosts) == 0 {
		return nil
	}
	if e.dryRun {
		if e.verbose {
			for _, h := range hosts {
				e.echoDryRun(h.Address, command)
			}
		}
		return nil
	}
	if e.verbose {
		e.echoExec("", command)
	}
	return e.cluster.Run(ctx, Targets(hosts), func(string) string { return command }, e.concurrency, failFast)
}

// SetReleaseContext points release_path/release_timestamp at an in-progress release.
// The deploy lifecycle calls this before running release-scoped tasks.
func (e *Executor) SetReleaseContext(releasePath, timestamp string) {
	e.base.ReleasePath = releasePath
	e.base.ReleaseTimestamp = timestamp
}

// SetCommitHash records the deployed commit SHA, exposed to subsequent tasks and hooks as {{.commit_hash}} /
// $COMMIT_HASH.
// The deploy lifecycle calls this once the mirror is updated, so it is unknown (empty) for standalone task runs.
func (e *Executor) SetCommitHash(hash string) { e.base.CommitHash = hash }

// SetPreviousCommitHash records the SHA the live release was deployed from, exposed as {{.previous_commit_hash}} /
// $PREVIOUS_COMMIT_HASH. The deploy lifecycle sets it at deploy start, so it is empty for standalone task runs and on
// a fresh deploy.
func (e *Executor) SetPreviousCommitHash(hash string) { e.base.PreviousCommitHash = hash }

// SetChangelog records the commits between the previous and the new revision (one per line,
// <sha>|<author>|<email>|<subject>), exposed as {{.changelog}} / $DEPLOY_CHANGELOG. The deploy lifecycle sets it at
// deploy:updating, so it is empty before that and for standalone task runs.
func (e *Executor) SetChangelog(log string) { e.base.Changelog = log }

// Capture runs command on a single host and returns its trimmed stdout, reusing the pooled connection.
// The deploy lifecycle uses it to read a value off a host (e.g. the deployed commit SHA) into the template context.
func (e *Executor) Capture(ctx context.Context, host ast.Host, command string) (string, error) {
	return e.cluster.Capture(ctx, Targets([]ast.Host{host})[0], command)
}

// RunTask runs a named Deployfile task, including its dependencies.
func (e *Executor) RunTask(ctx context.Context, name string) error {
	return e.runTask(ctx, name, map[string]bool{})
}

// RunTaskInPhase runs a task with the deploy phase exposed to it as {{.phase}} / $DEPLOY_PHASE for the duration of the
// run (then restored). The deploy lifecycle uses it to run hook tasks, so one task/script can branch on the phase.
// Hook runs are sequential, so mutating the shared phase here is safe.
func (e *Executor) RunTaskInPhase(ctx context.Context, name, phase string) error {
	prev := e.base.Phase
	e.base.Phase = phase
	defer func() { e.base.Phase = prev }()
	return e.runTask(ctx, name, map[string]bool{})
}

// SetError records a failure message exposed to deploy:failed hook tasks as {{.error}} / $DEPLOY_ERROR.
func (e *Executor) SetError(msg string) { e.base.DeployError = msg }

// firstError returns the most informative error among the results: a real command/host failure takes precedence over a
// context cancellation.
// Under failFast the first failure cancels the siblings, so several results carry context.Canceled - collateral, not
// the root cause (and target #0, often the bastion, can be one of them).
// Returning a cancellation only when every error is one (e.g. an operator Ctrl-C) keeps the surfaced error pointed at
// the real cause.
func firstError(results []runner.Result) error {
	var canceled error
	for _, r := range results {
		if r.Err == nil {
			continue
		}
		if errors.Is(r.Err, context.Canceled) || errors.Is(r.Err, context.DeadlineExceeded) {
			if canceled == nil {
				canceled = fmt.Errorf("%s: %w", r.Host, r.Err)
			}
			continue
		}
		return fmt.Errorf("%s: %w", r.Host, r.Err)
	}
	return canceled
}
