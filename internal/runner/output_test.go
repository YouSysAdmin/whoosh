package runner

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// A line without a newline is emitted in prefixed chunks once it reaches maxLine.
func TestLineWriter_LongLine(t *testing.T) {
	var out bytes.Buffer
	w := NewPrefixWriter(&out, "h ")
	_, _ = w.Write(bytes.Repeat([]byte("x"), maxLine+10))
	if got, want := out.String(), "h "+strings.Repeat("x", maxLine)+"\n"; got != want {
		t.Errorf("emitted %d bytes, want %d", len(got), len(want))
	}

	w.Flush()
	if !strings.HasSuffix(out.String(), "h "+strings.Repeat("x", 10)+"\n") {
		t.Error("tail not flushed")
	}
}

// A target skipped after a fail-fast cancel stays a cancellation and names the failure that caused it.
func TestFanout_SkippedNamesCause(t *testing.T) {
	boom := errors.New("boom")
	targets := []Target{{Host: "a"}, {Host: "b"}}
	results := Fanout(t.Context(), targets, 1, true, func(_ context.Context, tg Target) error {
		if tg.Host == "a" {
			return boom
		}

		return nil
	})

	if !errors.Is(results[0].Err, boom) {
		t.Fatalf("a = %v, want boom", results[0].Err)
	}

	if err := results[1].Err; !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "a failed: boom") {
		t.Errorf("b = %v, want a cancellation naming a's failure", err)
	}
}
