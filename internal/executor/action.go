package executor

import (
	"context"
	"fmt"
	"log/slog"
	pathpkg "path"
	"strings"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/plugins"
	"github.com/yousysadmin/whoosh/internal/runner"
	"github.com/yousysadmin/whoosh/internal/varstmpl"
)

// runAction runs an action task operator-side by looking up the plugins-registered action by its global name.
// It does not target servers or use SSH.
func (e *Executor) runAction(ctx context.Context, task *ast.Task) error {
	with, err := e.renderParams(task.With)
	if err != nil {
		return fmt.Errorf("action %q: %w", task.Action, err)
	}
	if e.dryRun {
		e.echoDryRun("", fmt.Sprintf("action %s with %v", task.Action, with))
		return nil
	}
	if e.reg == nil {
		return fmt.Errorf("action %q: no plugins loaded", task.Action)
	}
	fn, ok := e.reg.Action(task.Action)
	if !ok {
		return fmt.Errorf("unknown action %q (no loaded plugins registers it)", task.Action)
	}
	// Provide a host-file writer and a host-command runner bound to the task's hosts, so an action can render generated
	// content (e.g. an SSM env file) onto them or run commands (e.g. systemctl) on them rather than the operator machine.
	// Actions that don't need them ignore them.
	hosts := e.targetsForTask(task)
	ctx = plugins.WithHostFileWriter(ctx, &hostFileWriter{e: e, task: task, hosts: hosts})
	ctx = plugins.WithHostCommandRunner(ctx, &hostCommandRunner{e: e, task: task, hosts: hosts})
	ctx = plugins.WithHostCommandCapturer(ctx, &hostCommandCapturer{e: e, task: task, hosts: hosts})
	return fn(ctx, with, e.out)
}

// hostCommandRunner runs a command on an action task's hosts (plugins.HostCommandRunner).
type hostCommandRunner struct {
	e     *Executor
	task  *ast.Task
	hosts []ast.Host
}

// RunCommand runs cmd on every host the task targets, in parallel and fail-fast, echoing it per host like a task cmd.
func (r *hostCommandRunner) RunCommand(ctx context.Context, cmd string) error {
	if len(r.hosts) == 0 {
		slog.Warn("no hosts match the action task; nothing to run", "cmd", cmd)
		return nil
	}
	// Echo the command per host so the console and the --log-file transcript show what was sent.
	for _, h := range r.hosts {
		r.e.echoExec(h.Address, cmd)
	}
	results := r.e.cluster.Run(ctx, Targets(r.hosts), func(string) string { return cmd }, r.e.concurrency, true)
	if runner.Failed(results) {
		return firstError(results)
	}
	return nil
}

// hostCommandCapturer captures command output from the first host an action task targets (plugins.HostCommandCapturer).
type hostCommandCapturer struct {
	e     *Executor
	task  *ast.Task
	hosts []ast.Host
}

// CaptureCommand runs cmd on the first target host and returns its trimmed stdout, echoing the command like a task cmd.
func (c *hostCommandCapturer) CaptureCommand(ctx context.Context, cmd string) (string, error) {
	if len(c.hosts) == 0 {
		return "", fmt.Errorf("no hosts match the action task; nothing to capture")
	}
	target := c.e.taskTargets(c.task, c.hosts[:1])[0]
	c.e.echoExec(c.hosts[0].Address, cmd)
	return c.e.cluster.Capture(ctx, target, cmd)
}

// hostFileWriter renders a file onto an action task's hosts (plugins.HostFileWriter).
type hostFileWriter struct {
	e     *Executor
	task  *ast.Task
	hosts []ast.Host
}

// WriteFile writes content to path on every host the task targets, creating the parent and setting mode 0600.
// A relative path resolves against the task's directory (the release dir). It fails on the first host error.
func (w *hostFileWriter) WriteFile(ctx context.Context, path string, content []byte) error {
	if len(w.hosts) == 0 {
		slog.Warn("no hosts match the action task; nothing to render", "path", path)
		return nil
	}
	cmds := make(map[string]string, len(w.hosts))
	for _, h := range w.hosts {
		full := path
		if !strings.HasPrefix(full, "/") {
			dir, err := w.e.taskDir(w.task, h.Address)
			if err != nil {
				return err
			}
			full = pathpkg.Join(dir, path)
		}
		cmds[h.Address] = writeFileCmd(full, content)
		// Per-host progress, treated like command output: a structured record in log mode, else a green-prefixed raw line.
		msg := fmt.Sprintf("rendering %s (%d bytes)", full, len(content))
		if w.e.logMode {
			w.e.logLine(h.Address, msg)
		} else {
			fmt.Fprintf(w.e.out, "%s %s\n", runner.HostLabel(h.Address, w.e.color), msg)
		}
	}
	results := w.e.cluster.Run(ctx, Targets(w.hosts), func(h string) string { return cmds[h] }, w.e.concurrency, true)
	if runner.Failed(results) {
		return firstError(results)
	}
	return nil
}

// renderParams deep-renders the string values in an action task's `with:` map as Go templates (so e.g. name: "{{
// .asg_name }}" resolves from vars / the deploy context), leaving numbers, bools, and nesting intact.
// Action tasks run operator-side, so there is no host - {{.host}} renders empty. Like every task-time render, {{ env
// "X" }} sees the resolved global envs (dry-run stays lenient when they need run-time state).
func (e *Executor) renderParams(in map[string]any) (map[string]any, error) {
	c := e.baseContext("")
	ge, err := e.globalEnv("")
	if err != nil {
		if !e.dryRun {
			return nil, err
		}
	} else {
		c.GlobalEnvValues = ge
	}
	return varstmpl.RenderParams(in, c, !e.dryRun)
}
