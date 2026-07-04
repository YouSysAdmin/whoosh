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
	target := runner.Target{Host: "local", Local: true}
	if !task.Local {
		servers := e.targetsForTask(task)
		if len(servers) == 0 {
			slog.Warn("no hosts match task", "task", name)
			return nil
		}
		target = e.taskTargets(task, servers[:1])[0]
	}

	steps, err := e.taskSteps(task)
	if err != nil {
		return err
	}

	var out strings.Builder
	for _, st := range steps {
		cmd, err := st.build(target.Host)
		if err != nil {
			return err
		}
		e.announceStep(st)
		if e.dryRun {
			line, err := e.dryRunLine(st, target.Host, cmd)
			if err != nil {
				return err
			}
			if !e.logDryRun(target.Host, "capture: "+line) {
				fmt.Fprintf(e.out, "[dry-run] capture %s: %s\n", target.Host, line)
			}
			continue
		}
		s, err := e.cluster.Capture(ctx, target, cmd)
		if err != nil {
			if task.ContinueOnError {
				slog.Warn("continuing past error", "task", name, "error", err)
				continue
			}
			return fmt.Errorf("%s: %w", target.Host, err)
		}
		out.WriteString(s)
		out.WriteByte('\n')
	}

	val, err := parseOutput(task.Output, strings.TrimSpace(out.String()))
	if err != nil {
		return fmt.Errorf("task %q: %w", name, err)
	}
	e.setTaskState(name, val)
	return nil
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

// setTaskState records a task's parsed output in the shared state map.
// Task execution is sequential, but the lock keeps the map safe regardless.
func (e *Executor) setTaskState(name string, v any) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	e.base.Tasks[name] = v
}
