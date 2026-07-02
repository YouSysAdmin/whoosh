package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/yousysadmin/whoosh/internal/errors"
	"github.com/yousysadmin/whoosh/internal/runner"
)

// A real command failure must win over the context-cancellations that failFast produces on sibling hosts - even when a
// cancelled host (e.g. the bastion) is first in target order.
func TestFirstError_PrefersRealFailureOverCancellation(t *testing.T) {
	real := errors.New("Process exited with status 1")
	results := []runner.Result{
		{Host: "10.4.20.204", Err: context.Canceled},                               // collateral, target #0
		{Host: "10.4.20.75", Err: &errors.UnreachableError{Err: context.Canceled}}, // collateral, wrapped
		{Host: "10.4.20.66", Err: real},                                            // the actual failure
	}
	err := firstError(results)
	if err == nil || !errors.Is(err, real) {
		t.Fatalf("want the real failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "10.4.20.66") {
		t.Errorf("error should name the failing host, got %v", err)
	}
}

// When every error is a cancellation (e.g. an operator Ctrl-C), report one.
func TestFirstError_AllCancellations(t *testing.T) {
	results := []runner.Result{
		{Host: "a", Err: context.Canceled},
		{Host: "b", Err: &errors.UnreachableError{Err: context.Canceled}},
	}
	if err := firstError(results); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("want a cancellation when that's all there is, got %v", err)
	}
}

func TestFirstError_None(t *testing.T) {
	if err := firstError([]runner.Result{{Host: "a"}, {Host: "b"}}); err != nil {
		t.Errorf("no errors should yield nil, got %v", err)
	}
}
