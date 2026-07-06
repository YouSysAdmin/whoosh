package cli

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
	"github.com/yousysadmin/whoosh/internal/logger"
	"github.com/yousysadmin/whoosh/internal/masking"
)

// logFlags configure the process-wide slog logger.
type logFlags struct {
	level      string
	format     string
	output     string
	color      bool
	file       string
	fileFormat string
}

// logState retains what is needed to (re)configure logging idempotently: the initial flag-based setup happens in
// PersistentPreRunE, then setupLogging may run again to layer in the Deployfile's `log:` config.
// The CLI runs once per process, so package scope is fine (newRootCmd resets it).
var logState struct {
	base io.Writer // root stdout before any --log-file tee
	file *os.File  // currently open log file, if any
}

// setupLogging installs the slog logger from resolved log settings, and when a file is set, captures it too.
// The console always gets the primary sink (output, colored on a terminal).
// The file additionally receives whoosh's slog narrative, and for a *text* file it also gets the raw host command
// output - which streams on stdout, NOT through slog - tee'd in, so the file is a complete deploy transcript.
// A JSON file stays narrative-only: interleaving raw command bytes would break its JSON lines.
// It is safe to call again (after loading the Deployfile's log config): the previously opened file is closed and the
// tee is reset to the captured base first, so re-applying never double-tees.
func setupLogging(cmd *cobra.Command, level, format, output string, color bool, file, fileFormat string) error {
	root := cmd.Root()
	if logState.base == nil {
		logState.base = root.OutOrStdout()
	}
	if logState.file != nil {
		_ = logState.file.Close()
		logState.file = nil
	}
	root.SetOut(logState.base)

	sinks := []logger.Sink{{Level: level, Output: output, Format: format, Color: color}}
	if file != "" {
		f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		logState.file = f
		// Fan the narrative into the same open file (never colorized).
		sinks = append(sinks, logger.Sink{Level: level, Writer: f, Format: fileFormat})
		if !strings.EqualFold(fileFormat, "json") {
			// Tee command output (stdout) into the file: every command writer derives from the root command's OutOrStdout, so
			// all hosts' output (and dry-run plans, config dumps, ...) lands in the transcript too.
			root.SetOut(io.MultiWriter(logState.base, f))
		}
	}

	l, err := logger.New(sinks...)
	if err != nil {
		return err
	}
	slog.SetDefault(l)
	return nil
}

// applyMasking turns redaction off at debug level (the operator wants raw output).
func applyMasking(level string) {
	masking.SetEnabled(!strings.EqualFold(level, "debug"))
}

// applyLogConfig re-installs logging after the Deployfile loads, layering its `log:` config under the --log-* flags: a
// flag the operator set explicitly wins, otherwise the Deployfile value applies, otherwise the flag default.
// It also reflects each explicitly-set --log-* flag back into *lc, so downstream consumers that read cfg.Log (e.g. a
// plugin command choosing table vs JSON output) see the effective settings, not just what the Deployfile declared.
// Only flags the operator set (fs.Changed) are materialized, so unset flags leave the Deployfile values intact and
// defaults ("text"/false) are never baked in.
func applyLogConfig(cmd *cobra.Command, lc *ast.Log) error {
	fs := cmd.Root().PersistentFlags()
	if fs.Changed("log-level") {
		lc.Level, _ = fs.GetString("log-level")
	}
	if fs.Changed("log-format") {
		lc.Format, _ = fs.GetString("log-format")
	}
	if fs.Changed("log-output") {
		lc.Output, _ = fs.GetString("log-output")
	}
	if fs.Changed("log-file") {
		lc.File, _ = fs.GetString("log-file")
	}
	if fs.Changed("log-file-format") {
		lc.FileFormat, _ = fs.GetString("log-file-format")
	}
	if fs.Changed("log-color") {
		v, _ := fs.GetBool("log-color")
		lc.Color = &v
	}

	level := logStr(cmd, "log-level", lc.Level)
	if err := setupLogging(cmd,
		level,
		logStr(cmd, "log-format", lc.Format),
		logStr(cmd, "log-output", lc.Output),
		logBool(cmd, "log-color", lc.Color),
		logStr(cmd, "log-file", lc.File),
		logStr(cmd, "log-file-format", lc.FileFormat),
	); err != nil {
		return err
	}
	applyMasking(level)
	return nil
}

// effectiveVerbose reports whether verbose output is enabled: the --verbose flag, or an effective log level of debug
// (flag > Deployfile `log:` > flag default) - a debug run should show everything, including the full built commands.
// Call it after loadConfig so lc reflects the Deployfile's log config.
func effectiveVerbose(cmd *cobra.Command, verbose bool, lc ast.Log) bool {
	return verbose || strings.EqualFold(logStr(cmd, "log-level", lc.Level), "debug")
}

// colorOutput reports whether the raw command-output stream (host prefixes and echoed commands) should be colorized:
// the resolved --log-color preference AND a terminal destination, so a redirected, piped, or --log-file transcript
// never receives ANSI codes.
func colorOutput(cmd *cobra.Command, lc ast.Log) bool {
	return logBool(cmd, "log-color", lc.Color) && logger.IsTerminal(cmd.OutOrStdout())
}

// logStr resolves a string log setting: the flag value when the operator set it, otherwise the Deployfile value when
// non-empty, otherwise the flag default.
func logStr(cmd *cobra.Command, flag, cfgVal string) string {
	fs := cmd.Root().PersistentFlags()
	if cfgVal == "" || fs.Changed(flag) {
		v, _ := fs.GetString(flag)
		return v
	}
	return cfgVal
}

// logBool resolves a bool log setting with the same precedence (a nil cfg pointer means the Deployfile did not set it).
func logBool(cmd *cobra.Command, flag string, cfgVal *bool) bool {
	fs := cmd.Root().PersistentFlags()
	if cfgVal == nil || fs.Changed(flag) {
		v, _ := fs.GetBool(flag)
		return v
	}
	return *cfgVal
}
