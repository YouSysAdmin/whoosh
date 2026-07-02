package executor

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

// runTask resolves and runs a task: it guards against dependency cycles, skips a task gated out of this stage (or whose
// plugins is inactive), runs the task's deps first, then dispatches to the right executor path (action / capture /
// local / remote).
func (e *Executor) runTask(ctx context.Context, name string, stack map[string]bool) error {
	task, ok := e.cfg.Tasks[name]
	if !ok || task == nil {
		return fmt.Errorf("unknown task %q", name)
	}
	if stack[name] {
		return fmt.Errorf("task dependency cycle through %q", name)
	}
	stack[name] = true
	defer delete(stack, name)

	// Tag this task's command output (in log mode) for the duration of its run, restoring the caller's on return so a
	// dep/hook nested below reverts to the parent task afterward. Sequential within the executor goroutine, so no lock.
	prevLogTask := e.logTask
	e.logTask = name
	defer func() { e.logTask = prevLogTask }()

	// A task gated out of this stage by its only/except filters is skipped entirely (deps included) rather than run -
	// whether invoked directly, as a dep, or from a hook - mirroring the plugins stage gate below.
	if !task.ActiveForStage(e.cfg.Stage) {
		slog.Info("skipping task: not active for this stage",
			"task", name, "stage", e.cfg.Stage)
		return nil
	}

	// An action task whose plugins is inactive for this stage is skipped entirely (deps included) rather than failing - so
	// e.g. AWS tasks no-op in a stage where the aws plugins is disabled.
	if task.Action != "" && e.skipped[actionNamespace(task.Action)] {
		slog.Info("skipping action task: plugins not active for this stage",
			"task", name, "action", task.Action, "stage", e.cfg.Stage)
		return nil
	}

	// Before hooks run ahead of the task's deps and body, after hooks once the body succeeds, so a hook brackets every
	// invocation of the task wherever it runs - a direct call, a dep, or a phase hook - not only a deploy phase. Hooks
	// share the dependency stack, so a hook that loops back to its own task is caught as a cycle rather than recursing.
	if err := e.runHooks(ctx, "before", name, e.cfg.Hooks.Before[name], stack); err != nil {
		return err
	}

	for _, dep := range task.Deps {
		if err := e.runTask(ctx, dep, stack); err != nil {
			return err
		}
	}

	e.announce(name, task)
	var runErr error
	// silent_output buffers this task's command output and shows it only if the task fails (skipped under --dry-run,
	// which prints plans, and when already capturing - a dispatch never re-enters runTask, so this is just a guard).
	if task.SilentOutput && !e.dryRun && e.capture == nil {
		sink := e.beginCapture()
		runErr = e.dispatchTask(ctx, name, task)
		e.endCapture(sink, runErr)
	} else {
		runErr = e.dispatchTask(ctx, name, task)
	}
	if runErr != nil {
		return runErr
	}

	return e.runHooks(ctx, "after", name, e.cfg.Hooks.After[name], stack)
}

// dispatchTask routes a task to the right executor path (action / capture / local / remote).
func (e *Executor) dispatchTask(ctx context.Context, name string, task *ast.Task) error {
	switch {
	case task.Action != "":
		return e.runAction(ctx, task)
	case task.Output != "":
		return e.runCapture(ctx, name, task)
	case task.Local:
		return e.runLocal(ctx, task)
	default:
		return e.runRemote(ctx, name, task)
	}
}

// runHooks runs the task hooks registered before/after the named task, in listed order. It shares the caller's
// dependency stack so a hook that re-enters the task it wraps is rejected as a cycle.
func (e *Executor) runHooks(ctx context.Context, when, task string, hooks []string, stack map[string]bool) error {
	for _, hook := range hooks {
		if err := e.runTask(ctx, hook, stack); err != nil {
			return fmt.Errorf("%s %q hook %q: %w", when, task, hook, err)
		}
	}
	return nil
}

func (e *Executor) announce(name string, task *ast.Task) {
	if task.Silent {
		return
	}
	if task.Desc != "" {
		slog.Info("task", "name", name, "desc", task.Desc)
	} else {
		slog.Info("task", "name", name)
	}
}
