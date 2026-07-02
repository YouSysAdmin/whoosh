// Package logger builds slog handlers: a single default handler (InitLogger - the common case) or a logger that fans
// out across several sinks (New, via slog.NewMultiHandler). It also ships ColorHandler, a small colorized text handler.
//
// Levels: debug/info/warn/error. Format: text (with optional ANSI color) or json. Output: stdout/stderr or a file path.
// InitLogger installs its handler as slog's default, so the rest of the codebase just calls slog.Info / slog.Error /
// etc.
package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// ANSI color codes.
var (
	colorDebug = "\033[36m" // Cyan
	colorInfo  = "\033[32m" // Green
	colorWarn  = "\033[33m" // Yellow
	colorError = "\033[31m" // Red
	colorReset = "\033[0m"
)

// Sink describes one log destination: where to write, in what format, at what minimum level.
// The zero value is INFO text on stdout.
type Sink struct {
	// Level is DEBUG, INFO (default), WARN or ERROR.
	Level string
	// Output is "stdout" (default), "stderr", or a file path. Ignored when Writer is set.
	Output string
	// Writer, when non-nil, is used as the destination directly (Output ignored).
	// This lets a caller share one already-opened file between this sink and another writer - e.g. fan the narrative into
	// the same file that command output is tee'd to, so both land in one transcript.
	Writer io.Writer
	// Format is "text" (default) or "json".
	Format string
	// Color requests ANSI colors (text format only).
	// It is honored only when the resolved output is an interactive terminal - writing escapes into a file or pipe would
	// corrupt it - so a file sink with Color set still gets plain text.
	Color bool
}

// New builds a slog.Logger that fans out to every sink via slog.NewMultiHandler.
// A single sink yields a plain handler (no wrapper).
// Each sink keeps its own format and level, so you can e.g. show colored INFO on the console while writing DEBUG JSON
// to a file.
// New does NOT install the result as the default, call slog.SetDefault yourself, or use InitLogger for the single-sink
// default.
func New(sinks ...Sink) (*slog.Logger, error) {
	if len(sinks) == 0 {
		return nil, fmt.Errorf("logger: at least one sink is required")
	}
	handlers := make([]slog.Handler, 0, len(sinks))
	for _, s := range sinks {
		h, err := buildHandler(s)
		if err != nil {
			return nil, err
		}
		handlers = append(handlers, h)
	}
	if len(handlers) == 1 {
		return slog.New(handlers[0]), nil
	}
	return slog.New(slog.NewMultiHandler(handlers...)), nil
}

// InitLogger builds a single-sink logger and installs it as slog's default - equivalent to New(Sink{...}) followed by
// slog.SetDefault. This is the common case, reach for New when you want to fan out to more than one sink.
//
//	levelStr:   "DEBUG", "INFO" (default), "WARN", "ERROR"
//	outputDest: "stdout"/"stderr" or a file path
//	format:     "text" (default) or "json"
//	color:      ANSI color for text format (terminal only)
func InitLogger(levelStr, outputDest, format string, color bool) (*slog.Logger, error) {
	l, err := New(Sink{Level: levelStr, Output: outputDest, Format: format, Color: color})
	if err != nil {
		return nil, err
	}
	slog.SetDefault(l)
	return l, nil
}

// buildHandler turns one Sink into an slog.Handler.
func buildHandler(s Sink) (slog.Handler, error) {
	level := parseLevel(s.Level)
	out, err := resolveWriter(s)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{Level: level}

	format := s.Format
	if format == "" {
		format = "text"
	}
	switch strings.ToLower(format) {
	case "json":
		return slog.NewJSONHandler(out, opts), nil
	case "text":
		// Only colorize a real terminal: writing ANSI escapes into a file (--log-file app.log) or a pipe (whoosh ... | tee)
		// would corrupt it. A provided Writer (e.g. a shared transcript file) is never a terminal.
		if s.Color {
			if f, ok := out.(*os.File); ok && isTerminal(f) {
				return &ColorHandler{output: f, level: level}, nil
			}
		}
		return slog.NewTextHandler(out, opts), nil
	default:
		return nil, fmt.Errorf("invalid log format: %s", format)
	}
}

// resolveWriter returns the sink's destination: an explicit Writer if set, else the file resolved from Output.
func resolveWriter(s Sink) (io.Writer, error) {
	if s.Writer != nil {
		return s.Writer, nil
	}
	return openOutput(s.Output)
}

// parseLevel maps a level name to slog.Level (INFO when empty or unknown).
func parseLevel(s string) slog.Level {
	switch strings.ToUpper(s) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// openOutput resolves a sink's Output to a file: stdout (default), stderr, or a file opened for append (created if
// missing). It returns *os.File so the text handler can check whether the destination is a terminal before coloring.
func openOutput(dest string) (*os.File, error) {
	switch {
	case dest == "" || strings.EqualFold(dest, "stdout"):
		return os.Stdout, nil
	case strings.EqualFold(dest, "stderr"):
		return os.Stderr, nil
	default:
		return os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	}
}

// isTerminal reports whether f is an interactive terminal (a character device), so color is suppressed when output is
// redirected to a file or pipe. This avoids a dependency on x/term: a regular file or pipe is not a character device.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// IsTerminal reports whether w is an interactive terminal. It is true only for an *os.File that is a character device,
// so a file, a pipe, or a wrapping writer (e.g. an io.MultiWriter teeing to a log file) reads as non-terminal - callers
// use it to decide whether colorizing output is safe.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isTerminal(f)
}

// ColorHandler renders log entries with colored level prefixes.
type ColorHandler struct {
	output *os.File
	level  slog.Level
	attrs  []slog.Attr
	group  string
}

// Enabled reports whether the level is enabled.
func (c *ColorHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= c.level
}

// Handle writes the log entry with colored prefix.
func (c *ColorHandler) Handle(_ context.Context, r slog.Record) error {
	var buf bytes.Buffer

	// Select color based on level
	var color string
	switch r.Level {
	case slog.LevelDebug:
		color = colorDebug
	case slog.LevelInfo:
		color = colorInfo
	case slog.LevelWarn:
		color = colorWarn
	case slog.LevelError:
		color = colorError
	default:
		color = colorReset
	}

	// Timestamp
	buf.WriteString(r.Time.Format("2006-01-02T15:04:05.000Z07:00"))
	buf.WriteString(" ")

	// Colored level
	buf.WriteString(color)
	buf.WriteString("[")
	buf.WriteString(strings.ToUpper(r.Level.String()))
	buf.WriteString("]")
	buf.WriteString(colorReset)
	buf.WriteString(" ")

	// Message
	buf.WriteString(r.Message)

	// Static attributes (WithAttrs)
	for _, a := range c.attrs {
		a.Value = a.Value.Resolve()
		writeAttr(&buf, c.attrKey(a), a.Value)
	}

	// Record attributes
	r.Attrs(func(a slog.Attr) bool {
		a.Value = a.Value.Resolve()
		writeAttr(&buf, c.attrKey(a), a.Value)
		return true
	})

	buf.WriteString("\n")
	_, err := c.output.Write(buf.Bytes())
	return err
}

// WithAttrs returns a new handler with additional attributes.
func (c *ColorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Copy current handler
	newHandler := *c

	// Append new attrs
	newHandler.attrs = append(append([]slog.Attr{}, c.attrs...), attrs...)
	return &newHandler
}

// WithGroup returns a new handler with a group prefix.
func (c *ColorHandler) WithGroup(name string) slog.Handler {
	newHandler := *c
	if c.group != "" {
		newHandler.group = c.group + "." + name
	} else {
		newHandler.group = name
	}
	return &newHandler
}

// attrKey formats the attribute key with the group prefix.
func (c *ColorHandler) attrKey(a slog.Attr) string {
	if c.group != "" {
		return c.group + "." + a.Key
	}
	return a.Key
}

func writeAttr(buf *bytes.Buffer, key string, v slog.Value) {
	switch v.Kind() {
	case slog.KindGroup:
		for _, ga := range v.Group() {
			ga.Value = ga.Value.Resolve()
			fmt.Fprintf(buf, " %s.%s=%v", key, ga.Key, ga.Value.Any())
		}
	default:
		fmt.Fprintf(buf, " %s=%v", key, v.Any())
	}
}
