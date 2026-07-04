package executor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/runner"
)

// step is one command or script in a task. build yields the per-host shell command actually sent to the host (env/dir
// baked in), display yields the clean, user-facing form to echo (the rendered command without the env-export preamble).
// display is nil for scripts, which are announced by name instead.
type step struct {
	label    string
	isScript bool
	build    func(host string) (string, error)
	display  func(host string) (string, error)
}

// shown returns the command to echo for this step: the clean display form when available, else the full built command
// (scripts, only echoed under --verbose).
func (s step) shown(host string) (string, error) {
	if s.display != nil {
		return s.display(host)
	}
	return s.build(host)
}

// taskSteps assembles a task's cmds (first) and scripts (second) into an ordered list of steps.
// File scripts are read from the operator's machine once here.
func (e *Executor) taskSteps(task *ast.Task) ([]step, error) {
	var steps []step
	for i, raw := range task.Cmds {
		raw := raw
		steps = append(steps, step{
			label: fmt.Sprintf("cmd %d", i+1),
			build: func(host string) (string, error) {
				rendered, err := e.render(raw, host)
				if err != nil {
					return "", err
				}
				env, err := e.execEnv(host, task)
				if err != nil {
					return "", err
				}
				dir, err := e.taskDir(task, host)
				if err != nil {
					return "", err
				}
				return wrapRemote(rendered, dir, env), nil
			},
			// Echo the rendered command itself, not the env-export wrapper.
			display: func(host string) (string, error) { return e.render(raw, host) },
		})
	}
	for _, sc := range task.Scripts {
		sc := sc
		if sc.Script != "" {
			steps = append(steps, step{
				label:    scriptLabel(sc),
				isScript: true,
				build: func(host string) (string, error) {
					content, err := e.render(sc.Script, host)
					if err != nil {
						return "", err
					}
					env, err := e.execEnv(host, task)
					if err != nil {
						return "", err
					}
					dir, err := e.taskDir(task, host)
					if err != nil {
						return "", err
					}
					return buildScriptCommand(sc.Interpreter, content, dir, env), nil
				},
			})
			continue
		}
		content, err := e.readScriptFile(sc.Path)
		if err != nil {
			return nil, err
		}
		// Render the file as a template when asked (explicit flag or .tmpl suffix).
		templated := sc.Template || strings.HasSuffix(sc.Path, ".tmpl")
		steps = append(steps, step{
			label:    scriptLabel(sc),
			isScript: true,
			build: func(host string) (string, error) {
				body := string(content)
				if templated {
					rendered, err := e.render(body, host)
					if err != nil {
						return "", err
					}
					body = rendered
				}
				env, err := e.execEnv(host, task)
				if err != nil {
					return "", err
				}
				dir, err := e.taskDir(task, host)
				if err != nil {
					return "", err
				}
				return buildScriptCommand(sc.Interpreter, body, dir, env), nil
			},
		})
	}
	return steps, nil
}

func (e *Executor) runRemote(ctx context.Context, name string, task *ast.Task) error {
	hosts := e.targetsForTask(task)
	if len(hosts) == 0 {
		slog.Warn("no hosts match task", "task", name)
		return nil
	}
	targets := e.taskTargets(task, hosts)
	steps, err := e.taskSteps(task)
	if err != nil {
		return err
	}

	for _, st := range steps {
		rendered := make(map[string]string, len(hosts))
		for _, h := range hosts {
			cmd, err := st.build(h.Address)
			if err != nil {
				return err
			}
			rendered[h.Address] = cmd
		}

		if e.dryRun {
			e.announceStep(st)
			for _, h := range hosts {
				line, err := e.dryRunLine(st, h.Address, rendered[h.Address])
				if err != nil {
					return err
				}
				if !e.logDryRun(h.Address, line) {
					fmt.Fprintf(e.out, "[dry-run] %s: %s\n", h.Address, line)
				}
			}
			continue
		}
		e.announceStep(st)
		// Echo the command we send to each host so the console and the --log-file transcript show what ran, not just its
		// output. cmd steps are echoed always; scripts (whose full rendered body is large) only when verbose. e.out redacts,
		// so secrets - including values marked via envSecret / sensitive - are masked here too.
		if !st.isScript || e.verbose {
			for _, h := range hosts {
				shown, err := st.shown(h.Address)
				if err != nil {
					return err
				}
				if !e.logExec(h.Address, shown) {
					fmt.Fprintf(e.out, "%s $ %s\n", runner.HostLabel(h.Address, e.color), shown)
				}
			}
		}

		results := e.cluster.Run(ctx, targets, func(h string) string { return rendered[h] }, e.concurrency, !task.ContinueOnError)
		if runner.Failed(results) {
			if !task.ContinueOnError {
				return firstError(results)
			}
			// Non-fatal mode: surface each failed host so a sweep's failures (e.g. an unreachable host, which streams no stderr)
			// aren't silently dropped.
			for _, r := range results {
				if r.Err != nil {
					slog.Warn("host command failed (continuing)", "task", name, "host", r.Host, "error", r.Err)
				}
			}
		}
	}
	return nil
}

func (e *Executor) runLocal(ctx context.Context, task *ast.Task) error {
	steps, err := e.taskSteps(task)
	if err != nil {
		return err
	}
	for _, st := range steps {
		// dir/env are baked into the command, so run a bare shell here.
		cmd, err := st.build("local")
		if err != nil {
			return err
		}
		if e.dryRun {
			e.announceStep(st)
			line, err := e.dryRunLine(st, "local", cmd)
			if err != nil {
				return err
			}
			if !e.logDryRun("local", line) {
				fmt.Fprintf(e.out, "[dry-run][local] %s\n", line)
			}
			continue
		}
		e.announceStep(st)
		if !st.isScript || e.verbose {
			shown, err := st.shown("local")
			if err != nil {
				return err
			}
			if !e.logExec("local", shown) {
				fmt.Fprintf(e.out, "%s $ %s\n", runner.HostLabel("local", e.color), shown)
			}
		}
		// Tag local task output with a "[local]" host prefix like the cluster does for remote/local:true hosts - colored
		// in raw mode, a structured record (host "local") in log mode - so every command's output is attributed to a host.
		// Flush the writer's trailing partial line as soon as the command finishes.
		var lw *runner.LineWriter
		if e.logMode {
			lw = runner.NewLogWriter("local", e.logLine)
		} else {
			lw = runner.NewPrefixWriter(e.out, runner.HostLabel("local", e.color)+" ")
		}
		err = runLocalShell(ctx, cmd, lw)
		lw.Close()
		if err != nil {
			if task.ContinueOnError {
				slog.Warn("continuing past error", "error", err)
				continue
			}
			return err
		}
	}
	return nil
}

// announceStep notes a script step (cmd steps are quiet unless verbose/dry-run).
func (e *Executor) announceStep(st step) {
	if st.isScript {
		slog.Info("script", "name", st.label)
	}
}

// dryRunLine picks what the dry-run plan shows for one step on one host, mirroring the live run's echo policy: the
// clean rendered command for cmds (no env-export/cd preamble) and just the script's name for scripts. --verbose
// upgrades both to the full built command actually sent to the host.
func (e *Executor) dryRunLine(st step, host, built string) (string, error) {
	if e.verbose {
		return built, nil
	}
	if st.isScript {
		return "script " + st.label, nil
	}
	return st.shown(host)
}
