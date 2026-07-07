// Package deploy orchestrates the release lifecycle: it builds a timestamped release from git on every host, links
// shared files/dirs into it, atomically swaps the current symlink, and prunes old releases.
package deploy

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/yousysadmin/whoosh/internal/deploy/hooks"
	"github.com/yousysadmin/whoosh/internal/deploy/scm"
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/errors"
	"github.com/yousysadmin/whoosh/internal/executor"
	"github.com/yousysadmin/whoosh/internal/operator"
	"github.com/yousysadmin/whoosh/internal/paths"
)

// replaceablePhases are the phases whose built-in command a task may take over via `replace:`.
// Only rollback is app-specific enough to override safely - the release-management phases (lock, release creation,
// symlink swap, cleanup) stay whoosh's.
var replaceablePhases = map[string]bool{ast.PhaseRollback: true}

// builtinPhases are the lifecycle phases, in run order. A custom phase anchors (before/after) on one of these.
var builtinPhases = []string{
	ast.PhaseStarting, ast.PhaseCheck, ast.PhaseInit, ast.PhaseStarted, ast.PhaseUpdating,
	ast.PhaseSymlink, ast.PhaseUpdated, ast.PhasePublishing, ast.PhasePublished,
	ast.PhaseFinishing, ast.PhaseFinished,
}

func isBuiltinPhase(name string) bool {
	for _, p := range builtinPhases {
		if p == name {
			return true
		}
	}
	return false
}

// step is one entry of the lifecycle: a phase name and its built-in command (or a no-op for a marker / pure hook-anchor phase).
type step struct {
	phase string
	fn    func() error
}

// noopStep backs a marker or hook-anchor phase: no command, just a hook point.
func noopStep() error { return nil }

// Deployer runs the release lifecycle for one stage.
type Deployer struct {
	cfg    *ast.DeployFile
	ex     *executor.Executor
	layout paths.Layout
	git    scm.Git
	hooks  *hooks.Runner

	policy   string            // on_unreachable: abort (default) or skip
	required map[string]bool   // hosts whose unreachability is always fatal
	primary  string            // lock-holder host (implicitly required)
	skipped  []string          // hosts dropped this run under `skip`
	replace  map[string]string // phase -> task name overriding its built-in command
	prevSHA  string            // previously deployed revision, read off the primary host at deploy start
}

// New builds a Deployer over a configured executor.
// It errors if a task's `replace:` names an unreplaceable phase or two tasks replace the same phase.
func New(cfg *ast.DeployFile, ex *executor.Executor) (*Deployer, error) {
	required := map[string]bool{}
	for _, h := range cfg.Hosts {
		if h.IsRequired() {
			required[h.Address] = true
		}
	}
	replace := map[string]string{}
	for name, t := range cfg.Tasks {
		if t == nil || t.Replace == "" {
			continue
		}
		if !replaceablePhases[t.Replace] {
			return nil, fmt.Errorf("task %q: replace %q is not supported (only %q can be replaced)", name, t.Replace, ast.PhaseRollback)
		}
		if other, dup := replace[t.Replace]; dup {
			return nil, fmt.Errorf("tasks %q and %q both replace %q", other, name, t.Replace)
		}
		replace[t.Replace] = name
	}
	// Validate custom phases: a unique, non-built-in name, anchored (exactly one of before/after) on a built-in phase,
	// naming an existing task if it runs one.
	seenPhase := map[string]bool{}
	for _, p := range cfg.CustomPhases {
		switch {
		case p.Name == "":
			return nil, fmt.Errorf("custom phase: name is required")
		case isBuiltinPhase(p.Name), p.Name == ast.PhaseFailed, p.Name == ast.PhaseRollback:
			return nil, fmt.Errorf("custom phase %q: name collides with a built-in phase", p.Name)
		case seenPhase[p.Name]:
			return nil, fmt.Errorf("custom phase %q: defined more than once", p.Name)
		case (p.Before == "") == (p.After == ""):
			return nil, fmt.Errorf("custom phase %q: set exactly one of before/after", p.Name)
		}
		seenPhase[p.Name] = true
		anchor := p.Before
		if anchor == "" {
			anchor = p.After
		}
		if !isBuiltinPhase(anchor) {
			return nil, fmt.Errorf("custom phase %q: anchor %q is not a built-in phase", p.Name, anchor)
		}
		if p.Task != "" {
			if t, ok := cfg.Tasks[p.Task]; !ok || t == nil {
				return nil, fmt.Errorf("custom phase %q: task %q not found", p.Name, p.Task)
			}
		}
	}
	return &Deployer{
		cfg:      cfg,
		ex:       ex,
		layout:   paths.For(cfg.App.DeployTo),
		git:      scm.Git{Repo: cfg.App.Repo, Branch: cfg.App.Branch},
		hooks:    hooks.New(cfg.Hooks, cfg.HookFuncsBefore, cfg.HookFuncsAfter, ex.RunTaskInPhase, ex.Out()),
		policy:   cfg.OnUnreachable,
		required: required,
		replace:  replace,
	}, nil
}

// Deploy runs the full lifecycle.
func (d *Deployer) Deploy(ctx context.Context) error {
	if d.cfg.App.Repo == "" {
		return fmt.Errorf("app.repo is required to deploy")
	}
	hosts := d.ex.Hosts()
	if len(hosts) == 0 {
		return fmt.Errorf("no hosts match for stage %q", d.cfg.Stage)
	}
	// The lock holder is implicitly required: losing it would strand the lock, so it must never be dropped by `skip`.
	d.primary = hosts[0].Address
	d.skipped = nil

	now := time.Now().UTC()
	ts := now.Format("20060102150405")
	revTime := now.Format(time.RFC3339)
	release := d.layout.Release(ts)
	d.ex.SetReleaseContext(release, ts)

	// Best-effort: read the SHA the live release was deployed from off the primary host, so hooks and tasks (starting
	// with the deploy:starting ones) can use {{.previous_commit_hash}} / $PREVIOUS_COMMIT_HASH. Empty on a fresh deploy.
	// An unreachable host only warns here - the lock step right after produces the real failure. Skipped in dry-run.
	d.prevSHA = ""
	if !d.ex.DryRun() {
		prev, err := d.ex.Capture(ctx, hosts[0], currentRevisionCmd(d.layout))
		if err != nil {
			slog.Warn("previous revision unavailable", "error", err)
		} else if isCommitSHA(prev) {
			d.prevSHA = prev
			d.ex.SetPreviousCommitHash(prev)
		}
	}

	slog.Info("deploying", "app", d.cfg.App.Name,
		"stage", d.cfg.Stage, "repo", d.cfg.App.Repo,
		"branch", d.cfg.App.Branch, "dir", d.cfg.App.DeployTo,
		"release", ts, "hosts", len(hosts),
	)

	// Lock on the primary host only, so a failed multi-host acquisition can't strand a lock we'd then mistake for someone
	// else's. The deferred unlock runs only if we actually acquired the lock.
	primary := hosts[:1]
	var locked bool
	defer func() {
		if locked {
			_ = d.ex.RunOn(context.Background(), primary, unlockCmd(d.layout))
		}
	}()

	steps := []step{
		{ast.PhaseStarting, func() error {
			if err := d.ex.RunOn(ctx, primary, lockCmd(d.layout, operator.Name()+" @ "+ts)); err != nil {
				return fmt.Errorf("%w (clear a stale lock with: whoosh %s deploy:unlock)", err, d.cfg.Stage)
			}
			locked = true
			return nil
		}},
		{ast.PhaseCheck, func() error {
			return d.runStep(ctx, ensureStructureCmd(d.layout, d.cfg.LinkedFiles, d.cfg.LinkedDirs))
		}},
		// Provisioning anchor: the dir tree now exists, so init hooks can install software / prepare the host (set dir: on
		// those tasks - the release dir doesn't exist yet) before the release is built.
		{ast.PhaseInit, noopStep},
		{ast.PhaseStarted, noopStep},
		{ast.PhaseUpdating, func() error {
			if err := d.runStep(ctx, d.git.EnsureMirror(d.layout.RepoPath)); err != nil {
				return err
			}
			// With the mirror updated, read the deployed commit SHA off a live host and publish it to the context, so
			// hooks/tasks from here on can use {{.commit_hash}} / $COMMIT_HASH. Skipped in dry-run (nothing is run).
			if !d.ex.DryRun() {
				sha, err := d.ex.Capture(ctx, d.ex.Hosts()[0], d.git.Revision(d.layout.RepoPath))
				if err != nil {
					return fmt.Errorf("resolve commit hash: %w", err)
				}
				d.ex.SetCommitHash(sha)
				// With both revisions known and the mirror fresh, capture what changed and publish it as
				// {{.changelog}} / $DEPLOY_CHANGELOG. Best-effort: e.g. a force-push can drop the previous SHA from
				// the mirror - warn and deploy on with an empty changelog.
				if d.prevSHA != "" && d.prevSHA == sha {
					slog.Info("no new commits since the previous release", "revision", sha)
				} else if d.prevSHA != "" && isCommitSHA(sha) {
					log, err := d.ex.Capture(ctx, d.ex.Hosts()[0], changelogCmd(d.layout.RepoPath, d.prevSHA, sha))
					if err != nil {
						slog.Warn("changelog unavailable", "error", err)
					} else {
						d.ex.SetChangelog(log)
					}
				}
			}
			if err := d.runStep(ctx, d.git.CreateRelease(d.layout.RepoPath, release)); err != nil {
				return err
			}
			return d.runStep(ctx, writeRevisionCmd(d.layout.RepoPath, release, d.git.Branch, revTime))
		}},
		{ast.PhaseSymlink, func() error {
			return d.runStep(ctx, symlinkSharedScript(d.layout, release, d.cfg.LinkedFiles, d.cfg.LinkedDirs))
		}},
		{ast.PhaseUpdated, noopStep},
		{ast.PhasePublishing, func() error {
			return d.runStep(ctx, publishCmd(release, d.layout.CurrentPath))
		}},
		{ast.PhasePublished, noopStep},
		{ast.PhaseFinishing, func() error {
			if err := d.runStep(ctx, logRevisionCmd(d.layout, release, d.git.Branch, ts, operator.Name())); err != nil {
				return err
			}
			return d.runStep(ctx, cleanupCmd(d.layout.ReleasesPath, d.cfg.App.KeepReleases))
		}},
		{ast.PhaseFinished, noopStep},
	}
	steps = d.insertCustomPhases(ctx, steps)

	var runErr error
	for _, s := range steps {
		d.phase(s.phase)
		if err := d.hooks.Before(ctx, s.phase); err != nil {
			runErr = err
			break
		}
		if err := s.fn(); err != nil {
			runErr = fmt.Errorf("%s: %w", s.phase, err)
			break
		}
		if err := d.hooks.After(ctx, s.phase); err != nil {
			runErr = err
			break
		}
	}
	if runErr != nil {
		d.onFailure(runErr)
		return runErr
	}

	if len(d.skipped) > 0 {
		// Deployed on the reachable hosts, but some were skipped.
		// Not a failure (so no deploy:failed hook), but surfaced as a non-zero exit for CI.
		slog.Warn("deployed with unreachable hosts skipped", "app", d.cfg.App.Name, "stage", d.cfg.Stage, "skipped", d.skipped)
		return &errors.SkippedHostsError{Stage: d.cfg.Stage, Hosts: d.skipped}
	}

	slog.Info("deployed", "app", d.cfg.App.Name, "stage", d.cfg.Stage)
	return nil
}

// insertCustomPhases splices cfg.CustomPhases (validated in New) into the lifecycle: each is placed before/after its
// built-in anchor, in declaration order per anchor.
// A phase with a Task runs it via RunTaskInPhase (so the task sees the phase as {{.phase}}/$DEPLOY_PHASE), otherwise it
// is a pure hook anchor. The main loop fires the phase's before/after hooks by name, like any built-in phase.
func (d *Deployer) insertCustomPhases(ctx context.Context, base []step) []step {
	if len(d.cfg.CustomPhases) == 0 {
		return base
	}
	before := map[string][]ast.CustomPhase{}
	after := map[string][]ast.CustomPhase{}
	for _, p := range d.cfg.CustomPhases {
		if p.Before != "" {
			before[p.Before] = append(before[p.Before], p)
		} else {
			after[p.After] = append(after[p.After], p)
		}
	}
	mk := func(p ast.CustomPhase) step {
		fn := noopStep
		if p.Task != "" {
			task, name := p.Task, p.Name
			fn = func() error { return d.ex.RunTaskInPhase(ctx, task, name) }
		}
		return step{phase: p.Name, fn: fn}
	}
	out := make([]step, 0, len(base)+len(d.cfg.CustomPhases))
	for _, s := range base {
		for _, p := range before[s.phase] {
			out = append(out, mk(p))
		}
		out = append(out, s)
		for _, p := range after[s.phase] {
			out = append(out, mk(p))
		}
	}
	return out
}

// runStep runs a built-in phase command on the current live hosts, applying the on_unreachable policy.
// Under "abort" (default) any error fails the deploy.
// Under "skip" it drops unreachable, non-required hosts (recording them) and fails only on a command failure, an
// unreachable required host, or if no host remains.
func (d *Deployer) runStep(ctx context.Context, command string) error {
	hosts := d.ex.Hosts()
	if len(hosts) == 0 {
		return fmt.Errorf("no reachable hosts remain")
	}
	if d.policy != ast.OnUnreachableSkip {
		return d.ex.RunOn(ctx, hosts, command)
	}

	for _, r := range d.ex.RunOnReport(ctx, hosts, command) {
		if r.Err == nil {
			continue
		}
		if !errors.IsUnreachable(r.Err) {
			return fmt.Errorf("%s: %w", r.Host, r.Err) // command failure -> always fatal
		}
		if d.isRequired(r.Host) {
			return fmt.Errorf("required host %s unreachable: %w", r.Host, r.Err)
		}
		slog.Warn("host unreachable, skipping", "host", r.Host, "error", r.Err)
		d.ex.MarkUnreachable(r.Host)
		d.skipped = append(d.skipped, r.Host)
	}
	if len(d.ex.Hosts()) == 0 {
		return fmt.Errorf("all hosts became unreachable")
	}
	return nil
}

// isRequired reports whether an unreachable host must fail the deploy: explicitly marked required, or the lock-holding
// primary.
func (d *Deployer) isRequired(host string) bool {
	return host == d.primary || d.required[host]
}

// onFailure runs the deploy:failed hook tasks (best-effort) so a failed deploy can notify.
// The failure message is exposed to those tasks as {{.error}} / $DEPLOY_ERROR.
// A fresh context is used because the deploy's may be cancelled.
func (d *Deployer) onFailure(err error) {
	if len(d.cfg.Hooks.After[ast.PhaseFailed]) == 0 && len(d.cfg.HookFuncsAfter[ast.PhaseFailed]) == 0 {
		return
	}
	d.ex.SetError(err.Error())
	if hookErr := d.hooks.After(context.Background(), ast.PhaseFailed); hookErr != nil {
		slog.Warn("deploy:failed hook error", "error", hookErr)
	}
}

// Check validates connectivity and ensures the directory tree exists.
func (d *Deployer) Check(ctx context.Context) error {
	hosts := d.ex.Hosts()
	if len(hosts) == 0 {
		return fmt.Errorf("no hosts match for stage %q", d.cfg.Stage)
	}
	d.phase(ast.PhaseCheck)
	if err := d.ex.RunOn(ctx, hosts, ensureStructureCmd(d.layout, d.cfg.LinkedFiles, d.cfg.LinkedDirs)); err != nil {
		return err
	}
	if !d.ex.DryRun() {
		slog.Info("structure ok", "stage", d.cfg.Stage, "linked_dirs", len(d.cfg.LinkedDirs), "linked_files", len(d.cfg.LinkedFiles), "hosts", len(hosts))
	}
	return nil
}

// Rollback runs the deploy:rollback phase: before-hooks, the rollback step, then after-hooks.
// The step is the built-in current-symlink swap (when cleanup is set, the rolled-back release is removed) unless a task
// overrides it via `replace: deploy:rollback` - e.g. an aws:ec2:asg:rollback action - in which case that task runs
// instead and `cleanup` does not apply.
// `after` hooks run after the step, so (for the built-in swap) `current` already points at the restored release.
func (d *Deployer) Rollback(ctx context.Context, cleanup bool) error {
	d.phase(ast.PhaseRollback)
	if err := d.hooks.Before(ctx, ast.PhaseRollback); err != nil {
		return err
	}
	if task, ok := d.replace[ast.PhaseRollback]; ok {
		if err := d.ex.RunTaskInPhase(ctx, task, ast.PhaseRollback); err != nil {
			return err
		}
	} else {
		hosts := d.ex.Hosts()
		if len(hosts) == 0 {
			return fmt.Errorf("no hosts match for stage %q", d.cfg.Stage)
		}
		if err := d.ex.RunOn(ctx, hosts, rollbackScript(d.layout, cleanup)); err != nil {
			return err
		}
	}
	return d.hooks.After(ctx, ast.PhaseRollback)
}

// Releases lists the releases present on each host, marking the current one.
func (d *Deployer) Releases(ctx context.Context) error {
	hosts := d.ex.Hosts()
	if len(hosts) == 0 {
		return fmt.Errorf("no hosts match for stage %q", d.cfg.Stage)
	}
	return d.ex.RunOn(ctx, hosts, releasesScript(d.layout))
}

// Unlock removes a (possibly stale) deploy lock on the primary host.
func (d *Deployer) Unlock(ctx context.Context) error {
	hosts := d.ex.Hosts()
	if len(hosts) == 0 {
		return fmt.Errorf("no hosts match for stage %q", d.cfg.Stage)
	}
	if err := d.ex.RunOn(ctx, hosts[:1], unlockCmd(d.layout)); err != nil {
		return err
	}
	if !d.ex.DryRun() {
		slog.Info("lock cleared", "stage", d.cfg.Stage)
	}
	return nil
}

func (d *Deployer) phase(name string) { slog.Info("phase", "phase", name) }
