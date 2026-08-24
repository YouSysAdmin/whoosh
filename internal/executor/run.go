package executor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/runner"
)

// step is one command or script in a task. build yields the per-host command pair: the full shell command actually
// sent to the host (env/dir baked in) plus, for cmd steps, the clean user-facing form to echo (the rendered command
// without the env-export preamble). Scripts carry no display form, they are announced by name instead.
type step struct {
	label    string
	isScript bool
	build    func(host string) (built, error)
}

// built is one step rendered for one host.
type built struct {
	cmd     string // the full shell command sent to the host
	display string // the clean echo form (cmd steps only)
}

// taskSteps assembles a task's cmds (first) and scripts (second) into an ordered list of steps.
// File scripts are read from the operator's machine once here.
func (e *Executor) taskSteps(task *ast.Task) ([]step, error) {
	var steps []step
	for i, raw := range task.Cmds {
		steps = append(steps, step{
			label: fmt.Sprintf("cmd %d", i+1),
			build: func(host string) (built, error) {
				rendered, err := e.render(raw, host)
				if err != nil {
					return built{}, err
				}
				env, err := e.execEnv(host, task)
				if err != nil {
					return built{}, err
				}
				dir, err := e.taskDir(task, host)
				if err != nil {
					return built{}, err
				}
				// The echo shows the rendered command itself, not the env-export wrapper.
				return built{cmd: wrapRemote(rendered, dir, env), display: rendered}, nil
			},
		})
	}
	for _, sc := range task.Scripts {
		// Inline scripts are always templated, a file script only when asked (explicit flag or .tmpl suffix).
		content, templated := sc.Script, true
		if sc.Script == "" {
			data, err := e.readScriptFile(sc.Path)
			if err != nil {
				return nil, err
			}
			content, templated = string(data), sc.Template || strings.HasSuffix(sc.Path, ".tmpl")
		}
		steps = append(steps, step{
			label:    scriptLabel(sc),
			isScript: true,
			build: func(host string) (built, error) {
				body := content
				if templated {
					rendered, err := e.render(body, host)
					if err != nil {
						return built{}, err
					}
					body = rendered
				}
				env, err := e.execEnv(host, task)
				if err != nil {
					return built{}, err
				}
				dir, err := e.taskDir(task, host)
				if err != nil {
					return built{}, err
				}
				return built{cmd: buildScriptCommand(sc.Interpreter, body, dir, env)}, nil
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
	steps, err := e.taskSteps(task)
	if err != nil {
		return err
	}

	for _, st := range steps {
		// Recomputed per step: the on_unreachable policy below may drop hosts mid-task.
		targets := e.taskTargets(task, hosts)
		rendered := make(map[string]built, len(hosts))
		for _, h := range hosts {
			b, err := st.build(h.Address)
			if err != nil {
				return err
			}
			rendered[h.Address] = b
		}

		e.announceStep(st)
		if e.dryRun {
			for _, h := range hosts {
				e.echoDryRun(h.Address, e.stepLine(st, rendered[h.Address]))
			}
			continue
		}
		// Echo the command we send to each host so the console and the --log-file transcript show what ran, not just its
		// output. cmd steps are echoed always (clean display form); verbose upgrades the echo to the full built command
		// (env exports, cd) and also echoes scripts, whose full rendered body is large. The echo redacts, so secrets -
		// including values marked via envSecret / sensitive - are masked here too.
		if !st.isScript || e.verbose {
			for _, h := range hosts {
				e.echoExec(h.Address, e.stepLine(st, rendered[h.Address]))
			}
		}

		// Under on_unreachable: skip, let every host finish the step (like the built-in phase steps via RunOnReport) so
		// the policy can judge each host's own result instead of a sibling's cancellation.
		failFast := !task.ContinueOnError && !e.skipUnreachable()
		results := e.cluster.Run(ctx, targets, func(h string) string { return rendered[h].cmd }, e.concurrency, failFast)
		if runner.Failed(results) {
			if !task.ContinueOnError {
				hosts, err = e.applyUnreachablePolicy(name, hosts, results)
				if err != nil {
					return err
				}
				if len(hosts) == 0 {
					slog.Warn("all task hosts unreachable, skipping remaining steps", "task", name)
					return nil
				}
				continue
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
		b, err := st.build("local")
		if err != nil {
			return err
		}
		e.announceStep(st)
		if e.dryRun {
			e.echoDryRunLocal(e.stepLine(st, b))
			continue
		}
		if !st.isScript || e.verbose {
			e.echoExec("local", e.stepLine(st, b))
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
		err = runLocalShell(ctx, b.cmd, lw)
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

// stepLine picks what to show for one built step, shared by the live echo and the dry-run plan: under --verbose
// the full built command actually sent to the host (env exports, cd), otherwise "script <name>" for scripts and the
// clean display form for cmds (no env-export or cd preamble). The live echo never takes the script branch - callers
// echo scripts only under --verbose - which keeps live and dry-run output on a single policy.
func (e *Executor) stepLine(st step, b built) string {
	if e.verbose {
		return b.cmd
	}
	if st.isScript {
		return "script " + st.label
	}
	return b.display
}
