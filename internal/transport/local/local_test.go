package local_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/yousysadmin/whoosh/internal/transport/local"
)

// A canceled context surfaces as context.Canceled (not the raw "signal: killed" from CommandContext's kill), and Run
// returns promptly instead of waiting for the command to finish - so the CLI can recognize an operator interrupt.
func TestRun_CancelReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- local.New().Run(ctx, "sleep 30", io.Discard, io.Discard) }()

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
	if err := local.New().Run(context.Background(), "true", io.Discard, io.Discard); err != nil {
		t.Fatalf("true should succeed, got %v", err)
	}
}
