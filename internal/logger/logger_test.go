package logger

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// New fans a record out to every sink, and each sink applies its own level and format: an INFO line reaches both, a
// DEBUG line only the debug sink.
func TestNewFanOut(t *testing.T) {
	dir := t.TempDir()
	textPath := filepath.Join(dir, "text.log") // info, text
	jsonPath := filepath.Join(dir, "json.log") // debug, json

	l, err := New(
		Sink{Level: "info", Output: textPath, Format: "text"},
		Sink{Level: "debug", Output: jsonPath, Format: "json"},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	l.Info("hello", "k", "v")
	l.Debug("verbose", "n", 1)

	text := readFile(t, textPath)
	js := readFile(t, jsonPath)

	if !strings.Contains(text, "hello") {
		t.Errorf("text sink missing the info line: %q", text)
	}
	if !strings.Contains(js, "hello") {
		t.Errorf("json sink missing the info line: %q", js)
	}
	// The debug line must reach only the debug sink.
	if strings.Contains(text, "verbose") {
		t.Errorf("info text sink should not carry the debug line: %q", text)
	}
	if !strings.Contains(js, "verbose") {
		t.Errorf("debug json sink missing the debug line: %q", js)
	}
	// The json sink is really JSON.
	if !strings.Contains(js, `"msg":"hello"`) {
		t.Errorf("json sink is not JSON-formatted: %q", js)
	}
}

func TestNewRequiresSink(t *testing.T) {
	if _, err := New(); err == nil {
		t.Error("New() with no sinks should error")
	}
}

func TestNewInvalidFormat(t *testing.T) {
	if _, err := New(Sink{Output: "stdout", Format: "xml"}); err == nil {
		t.Error("New() with an unknown format should error")
	}
}

// A file sink with Color set must NOT emit ANSI escapes: a file is not a terminal, so buildHandler falls back to the
// plain text handler. This guards the regression where coloring a file would corrupt the log.
func TestColorSinkToFileHasNoEscapes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "color.log")
	l, err := New(Sink{Level: "info", Output: path, Format: "text", Color: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l.Info("plain")

	if got := readFile(t, path); strings.Contains(got, "\033[") {
		t.Errorf("file sink should not contain ANSI escapes, got %q", got)
	}
}

// A sink with Writer set fans into that writer directly (Output ignored), so a caller can share one open file between
// the narrative sink and another writer.
func TestSinkWriterSharesDestination(t *testing.T) {
	var buf bytes.Buffer
	l, err := New(Sink{Level: "info", Writer: &buf, Format: "text", Color: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l.Info("shared")

	got := buf.String()
	if !strings.Contains(got, "shared") {
		t.Errorf("Writer sink did not receive the line: %q", got)
	}
	// A non-*os.File writer can never be a terminal, so Color is ignored.
	if strings.Contains(got, "\033[") {
		t.Errorf("Writer sink should not be colorized: %q", got)
	}
}

// InitLogger builds a single sink and installs it as the slog default.
func TestInitLoggerSetsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "default.log")
	if _, err := InitLogger("info", path, "json", false); err != nil {
		t.Fatalf("InitLogger: %v", err)
	}
	slog.Info("via-default", "k", "v")
	if got := readFile(t, path); !strings.Contains(got, "via-default") {
		t.Errorf("default logger did not write to the sink: %q", got)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func TestIsTerminal(t *testing.T) {
	if IsTerminal(&bytes.Buffer{}) {
		t.Error("a bytes.Buffer must not be reported as a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) {
		t.Error("a regular file must not be reported as a terminal")
	}
}
