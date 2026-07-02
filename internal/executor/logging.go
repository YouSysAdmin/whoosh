package executor

import (
	"log/slog"

	"github.com/yousysadmin/whoosh/internal/masking"
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

// logExec shows the command about to run on host. In log mode it emits an "exec" record and returns true; in raw mode
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
