package cli

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"

	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

// rootWithLogFlags builds a root command carrying the --log-* persistent flags and a child, returning the child
// (logStr/logBool read from cmd.Root()).
func rootWithLogFlags(t *testing.T) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "whoosh"}
	root.PersistentFlags().String("log-format", "text", "")
	root.PersistentFlags().Bool("log-color", true, "")
	sub := &cobra.Command{Use: "child"}
	root.AddCommand(sub)
	return sub
}

func TestLogStr_Priority(t *testing.T) {
	// Flag unchanged + config set -> config value wins.
	sub := rootWithLogFlags(t)
	if got := logStr(sub, "log-format", "json"); got != "json" {
		t.Errorf("unchanged flag: got %q, want config value json", got)
	}

	// Flag explicitly set -> flag wins over config.
	sub = rootWithLogFlags(t)
	_ = sub.Root().PersistentFlags().Set("log-format", "text")
	if got := logStr(sub, "log-format", "json"); got != "text" {
		t.Errorf("changed flag: got %q, want flag value text", got)
	}

	// No config and unchanged -> the flag default.
	sub = rootWithLogFlags(t)
	if got := logStr(sub, "log-format", ""); got != "text" {
		t.Errorf("no config: got %q, want flag default text", got)
	}
}

// rootWithAllLogFlags builds a root command carrying every --log-* persistent flag with its production default, for
// exercising applyLogConfig end-to-end.
func rootWithAllLogFlags(t *testing.T) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "whoosh"}
	pf := root.PersistentFlags()
	pf.String("log-level", "info", "")
	pf.String("log-format", "text", "")
	pf.String("log-output", "stdout", "")
	pf.Bool("log-color", true, "")
	pf.String("log-file", "", "")
	pf.String("log-file-format", "text", "")
	sub := &cobra.Command{Use: "child"}
	root.AddCommand(sub)
	return sub
}

// applyLogConfig materializes explicitly-set --log-* flags into cfg.Log, so downstream readers (e.g. a plugin command
// choosing table vs JSON output) see the effective format - not just what the Deployfile declared. An unset flag must
// leave the Deployfile value untouched (and not bake in the flag default).
func TestApplyLogConfig_MaterializesFlags(t *testing.T) {
	// --log-format json set explicitly -> materialized into cfg.Log.Format.
	sub := rootWithAllLogFlags(t)
	if err := sub.Root().PersistentFlags().Set("log-format", "json"); err != nil {
		t.Fatal(err)
	}
	lc := ast.Log{}
	if err := applyLogConfig(sub, &lc); err != nil {
		t.Fatalf("applyLogConfig: %v", err)
	}
	if lc.Format != "json" {
		t.Errorf("Format = %q, want json (explicit flag must be materialized)", lc.Format)
	}

	// No flag, empty config -> Format stays "" (the default text is left implicit, never baked in).
	sub = rootWithAllLogFlags(t)
	lc = ast.Log{}
	if err := applyLogConfig(sub, &lc); err != nil {
		t.Fatalf("applyLogConfig: %v", err)
	}
	if lc.Format != "" {
		t.Errorf("Format = %q, want empty (unset flag must not bake in the default)", lc.Format)
	}

	// Deployfile value present, no flag -> preserved.
	sub = rootWithAllLogFlags(t)
	lc = ast.Log{Format: "json"}
	if err := applyLogConfig(sub, &lc); err != nil {
		t.Fatalf("applyLogConfig: %v", err)
	}
	if lc.Format != "json" {
		t.Errorf("Format = %q, want json (Deployfile value must be preserved)", lc.Format)
	}
}

// Debug log level implies verbose output: a debug run should show everything, including the full built commands.
func TestEffectiveVerbose(t *testing.T) {
	// Defaults (info level, no --verbose) -> false.
	sub := rootWithAllLogFlags(t)
	if effectiveVerbose(sub, false, ast.Log{}) {
		t.Error("defaults: want false")
	}

	// --verbose set -> true regardless of level.
	sub = rootWithAllLogFlags(t)
	if !effectiveVerbose(sub, true, ast.Log{}) {
		t.Error("--verbose: want true")
	}

	// --log-level=debug flag -> implied verbose.
	sub = rootWithAllLogFlags(t)
	_ = sub.Root().PersistentFlags().Set("log-level", "debug")
	if !effectiveVerbose(sub, false, ast.Log{}) {
		t.Error("--log-level=debug: want true")
	}

	// Deployfile log: level debug, flag unchanged -> implied verbose.
	sub = rootWithAllLogFlags(t)
	if !effectiveVerbose(sub, false, ast.Log{Level: "DEBUG"}) {
		t.Error("Deployfile debug level: want true")
	}

	// Explicit --log-level=info overrides a Deployfile debug level -> false.
	sub = rootWithAllLogFlags(t)
	_ = sub.Root().PersistentFlags().Set("log-level", "info")
	if effectiveVerbose(sub, false, ast.Log{Level: "debug"}) {
		t.Error("explicit info over Deployfile debug: want false")
	}
}

func TestColorOutput_NonTerminalIsPlain(t *testing.T) {
	sub := rootWithLogFlags(t)
	sub.Root().SetOut(&bytes.Buffer{}) // a buffer is never a terminal
	// --log-color defaults on, but a non-terminal destination must still resolve to no color so a redirected or teed
	// transcript never gets ANSI codes.
	if colorOutput(sub, ast.Log{}) {
		t.Fatal("colorOutput must be false for a non-terminal destination, even with --log-color on")
	}
}

func TestLogBool_Priority(t *testing.T) { // Config set (false), flag unchanged -> config wins.
	sub := rootWithLogFlags(t)
	if got := logBool(sub, "log-color", new(false)); got != false {
		t.Errorf("unchanged flag: got %v, want config value false", got)
	}

	// Flag explicitly set -> flag wins over config.
	sub = rootWithLogFlags(t)
	_ = sub.Root().PersistentFlags().Set("log-color", "false")
	if got := logBool(sub, "log-color", new(true)); got != false {
		t.Errorf("changed flag: got %v, want flag value false", got)
	}

	// No config (nil) and unchanged -> the flag default (true).
	sub = rootWithLogFlags(t)
	if got := logBool(sub, "log-color", nil); got != true {
		t.Errorf("no config: got %v, want flag default true", got)
	}
}
