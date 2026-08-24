// Package hooks runs the user-defined tasks wired to deploy phases.
// A Deployfile declares hooks as before/after maps keyed by phase name, the deploy lifecycle calls Before and After
// around each phase. (Entries keyed by a task name instead are fired by the executor around that task, not here.)
package hooks

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

// TaskRunner runs a named Deployfile task for a given deploy phase.
// It is satisfied by executor.Executor.RunTaskInPhase, keeping this package free of an executor dependency.
// The phase is exposed to the task as {{.phase}} / $DEPLOY_PHASE.
type TaskRunner func(ctx context.Context, name, phase string) error

// Runner executes the hooks for a deployment: the user's task hooks (by name) and any plugins-registered func hooks (Go
// code run with the console writer).
type Runner struct {
	hooks   ast.Hooks
	beforeF map[string][]ast.HookFunc
	afterF  map[string][]ast.HookFunc
	run     TaskRunner
	out     io.Writer
}

// New creates a Runner over the config's task hooks plus any plugins func hooks.
func New(h ast.Hooks, beforeF, afterF map[string][]ast.HookFunc, run TaskRunner, out io.Writer) *Runner {
	return &Runner{hooks: h, beforeF: beforeF, afterF: afterF, run: run, out: out}
}

// Before runs the task hooks registered before the phase (in order), then the plugins func hooks for the phase.
func (r *Runner) Before(ctx context.Context, phase string) error {
	if err := r.runAll(ctx, "before", phase, r.hooks.Before[phase]); err != nil {
		return err
	}
	return r.runFuncs(ctx, "before", phase, r.beforeF[phase])
}

// After runs the task hooks registered after the phase (in order), then the plugins func hooks for the phase.
func (r *Runner) After(ctx context.Context, phase string) error {
	if err := r.runAll(ctx, "after", phase, r.hooks.After[phase]); err != nil {
		return err
	}
	return r.runFuncs(ctx, "after", phase, r.afterF[phase])
}

// NotifyFailed fires the after deploy:failed hooks best-effort - the shared failure tail of the deploy lifecycle and
// a standalone task command: nothing runs when no failed hooks are registered, setError exposes the failure to the
// hook tasks as {{.error}} / $DEPLOY_ERROR, a fresh background context is used because the caller's may be cancelled,
// and a hook error is only logged - the caller still returns the original failure.
func (r *Runner) NotifyFailed(setError func(string), err error) {
	if len(r.hooks.After[ast.PhaseFailed]) == 0 && len(r.afterF[ast.PhaseFailed]) == 0 {
		return
	}
	setError(err.Error())
	if hookErr := r.After(context.Background(), ast.PhaseFailed); hookErr != nil {
		slog.Warn("deploy:failed hook error", "error", hookErr)
	}
}

// runFuncs runs the plugins func hooks for a phase with the console writer.
// A returned error aborts the deployment, like a failing task hook.
func (r *Runner) runFuncs(ctx context.Context, when, phase string, fns []ast.HookFunc) error {
	for _, fn := range fns {
		if err := fn(ctx, r.out); err != nil {
			return fmt.Errorf("%s %s hook (func): %w", when, phase, err)
		}
	}
	return nil
}

func (r *Runner) runAll(ctx context.Context, when, phase string, tasks []string) error {
	for _, name := range tasks {
		if err := r.run(ctx, name, phase); err != nil {
			return fmt.Errorf("%s %s hook %q: %w", when, phase, name, err)
		}
	}
	return nil
}
