package local_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/yousysadmin/whoosh/internal/transport/local"
)

// A canceled context surfaces as context.Canceled (not the raw "signal: killed" from CommandContext's kill), and Run
// returns promptly instead of waiting for the command to finish - so the CLI can recognize an operator interrupt.
func TestRun_CancelReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	// The trailing "; true" makes this a compound command, forcing every shell to fork sleep as a child (a lone
	// command is exec'd in-place by macOS bash, which would mask the orphaned-grandchild hang this test guards).
	go func() { done <- local.New().Run(ctx, "sleep 30; true", io.Discard, io.Discard) }()

	time.Sleep(100 * time.Millisecond) // let the shell start
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return promptly after cancel")
	}
}

// A normal command still returns its real result (no false cancellation).
func TestRun_Success(t *testing.T) {
	if err := local.New().Run(t.Context(), "true", io.Discard, io.Discard); err != nil {
		t.Fatalf("true should succeed, got %v", err)
	}
}

// A background child that keeps the output pipe open does not fail a command that exited 0.
func TestRun_BackgroundChildHoldsPipe(t *testing.T) {
	var out bytes.Buffer
	if err := local.New().Run(t.Context(), "echo started; sleep 5 &", &out, &out); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	if !strings.Contains(out.String(), "started") {
		t.Errorf("output = %q, want it to contain started", out.String())
	}
}
