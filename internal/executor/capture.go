package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/runner"
)

// runCapture runs an `output:` task on a single target, buffers its combined stdout, parses it per the declared format,
// and stores it in run-scoped state under the task's name (readable by later tasks as {{ .tasks.<name> }}).
// Output tasks run on one target - local, or the first matching host - since a multi-host value would be ambiguous.
func (e *Executor) runCapture(ctx context.Context, name string, task *ast.Task) error {
	targets := []runner.Target{{Host: "local", Local: true}}
	if !task.Local {
		servers := e.targetsForTask(task)
		if len(servers) == 0 {
			// Store the format's zero value (like dry-run does) so a later {{ .tasks.<name> }} still renders instead
			// of failing strict rendering far from the cause.
			slog.Warn("no hosts match task", "task", name)
			val, err := parseOutput(task.Output, "")
			if err != nil {
				return fmt.Errorf("task %q: %w", name, err)
			}

			e.setTaskState(name, val)

			return nil
		}

		targets = e.taskTargets(task, servers)
	}

	steps, err := e.taskSteps(task)
	if err != nil {
		return err
	}

	// Under on_unreachable: skip an unreachable host is dropped and the capture moves on to the next one.
	var raw string
	for _, target := range targets {
		raw, err = e.captureOn(ctx, name, task, target, steps)
		if err == nil || target.Local || !e.dropUnreachable(ctx, name, target.Host, err) {
			break
		}
	}

	if err != nil {
		return err
	}

	val, err := parseOutput(task.Output, raw)
	if err != nil {
		return fmt.Errorf("task %q: %w", name, err)
	}

	e.setTaskState(name, val)

	return nil
}

// captureOn runs an output task's steps on one target and returns the collected, trimmed stdout.
func (e *Executor) captureOn(ctx context.Context, name string, task *ast.Task, target runner.Target, steps []step) (string, error) {
	var out strings.Builder
	for _, st := range steps {
		b, err := st.build(target.Host)
		if err != nil {
			return "", err
		}

		e.announceStep(st)
		if e.dryRun {
			line := e.stepLine(st, b)
			if !e.logDryRun(target.Host, "capture: "+line) {
				_, _ = fmt.Fprintf(e.out, "[dry-run] capture %s: %s\n", target.Host, line)
			}

			continue
		}

		s, err := e.cluster.Capture(ctx, target, b.cmd)
		if err != nil {
			if task.ContinueOnError {
				slog.Warn("continuing past error", "task", name, "error", err)
				continue
			}

			return "", fmt.Errorf("%s: %w", target.Host, err)
		}

		out.WriteString(s)
		out.WriteByte('\n')
	}

	return strings.TrimSpace(out.String()), nil
}

// parseOutput converts a task's captured stdout to its declared form: json into a map/list/scalar, lines into a
// []string, text (the default) into the trimmed string.
// An empty input (e.g. dry-run) yields the format's zero value so chains stay renderable.
func parseOutput(format, raw string) (any, error) {
	switch format {
	case "json":
		if raw == "" {
			return map[string]any{}, nil
		}

		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("parse json output: %w", err)
		}

		return v, nil
	case "lines":
		if raw == "" {
			return []string{}, nil
		}

		return strings.Split(raw, "\n"), nil
	default: // "text"
		return raw, nil
	}
}

// setTaskState records a task's parsed output in the shared state map. Like every base mutation it runs on the
// task goroutine between fanouts, never while host goroutines render.
func (e *Executor) setTaskState(name string, v any) {
	e.base.Tasks[name] = v
	// Rendered global env values may bake in {{.tasks.*}} state, so cached contexts are stale now.
	e.invalidateRenderCache()
}
