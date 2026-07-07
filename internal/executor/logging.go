package executor

import (
	"fmt"
	"log/slog"

	"github.com/yousysadmin/whoosh/internal/masking"
	"github.com/yousysadmin/whoosh/internal/runner"
)

// logLine emits one line of command output as a structured record. It is the runner.LineHandler the cluster (and the
// local shell, via NewLogWriter) route output through in log mode. The line is redacted exactly as the raw stream
// would be, so secrets stay masked in the shipped logs.
func (e *Executor) logLine(host, line string) {
	args := e.outputArgs(host, "output", line)
	if e.capture != nil { // silent_output: defer until the task's success/failure is known
		e.capture.add("output", args)
		return
	}
	slog.Info("output", args...)
}

// logExec shows the command about to run on host. In log mode it emits an "exec" record and returns true, in raw mode
// it returns false so the caller writes its host-prefixed "$" line to the console as before.
func (e *Executor) logExec(host, command string) bool {
	if !e.logMode {
		return false
	}
	args := e.outputArgs(host, "command", command)
	if e.capture != nil { // silent_output: defer until the task's success/failure is known
		e.capture.add("exec", args)
		return true
	}
	slog.Info("exec", args...)
	return true
}

// logDryRun shows one dry-run plan line. In log mode it emits a "dry-run" record (so a --log-format json stream stays
// one valid JSON stream) and returns true, in raw mode it returns false so the caller prints its "[dry-run]" line as
// before. No capture branch: silent_output capture is skipped under dry-run.
func (e *Executor) logDryRun(host, command string) bool {
	if !e.logMode {
		return false
	}
	slog.Info("dry-run", e.outputArgs(host, "command", command)...)
	return true
}

// echoExec shows the command about to run on host on whichever sink is active: an "exec" record in log mode (deferred
// under silent_output capture), else a raw host-prefixed "$" line on e.out, which redacts secrets. An empty host
// prints a bare "$ cmd" line - the built-in deploy commands run one literal command across every host, so there is no
// per-host form to prefix.
func (e *Executor) echoExec(host, command string) {
	if e.logExec(host, command) {
		return
	}
	if host == "" {
		fmt.Fprintf(e.out, "$ %s\n", command)
		return
	}
	fmt.Fprintf(e.out, "%s $ %s\n", runner.HostLabel(host, e.color), command)
}

// echoDryRun shows one dry-run plan line for host on whichever sink is active: a "dry-run" record in log mode, else a
// raw "[dry-run]" line on e.out. An empty host drops the "host:" part - operator-side action plans have no host.
func (e *Executor) echoDryRun(host, line string) {
	if e.logDryRun(host, line) {
		return
	}
	if host == "" {
		fmt.Fprintf(e.out, "[dry-run] %s\n", line)
		return
	}
	fmt.Fprintf(e.out, "[dry-run] %s: %s\n", host, line)
}

// echoDryRunLocal shows one dry-run plan line for a local (operator machine) task: the same "dry-run" record with
// host "local" in log mode, else the raw "[dry-run][local]" line. It is a separate variant instead of a
// host == "local" branch in echoDryRun because tasks are routed to the local runner by task.Local, not by host name -
// an inventory host that happens to be addressed "local" runs remotely and keeps its normal "[dry-run] local: cmd"
// form.
func (e *Executor) echoDryRunLocal(line string) {
	if e.logDryRun("local", line) {
		return
	}
	fmt.Fprintf(e.out, "[dry-run][local] %s\n", line)
}

// outputArgs builds the slog attributes shared by command-output and exec records: the running task (when known), the
// source host, then one key/value payload. The payload value is redacted.
func (e *Executor) outputArgs(host, key, val string) []any {
	args := make([]any, 0, 6)
	if e.logTask != "" {
		args = append(args, "task", e.logTask)
	}
	if host != "" {
		args = append(args, "host", host)
	}
	return append(args, key, masking.String(val))
}
