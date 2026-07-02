package errors_test

import (
	"fmt"
	"testing"

	"github.com/yousysadmin/whoosh/internal/errors"
)

// Code resolves the exit code from a typed error, sees through wrapping, and falls back to OK for nil / Unknown for a
// plain error.
func TestCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, errors.CodeOK},
		{"plain", errors.New("boom"), errors.CodeUnknown},
		{"config", &errors.ConfigError{Msg: "bad"}, errors.CodeConfig},
		{"version", &errors.VersionError{Have: "2", Max: "1.0.0", TooNew: true}, errors.CodeConfig},
		{"locked", &errors.LockedError{Stage: "prod", Host: "h"}, errors.CodeLocked},
		{"skipped", &errors.SkippedHostsError{Stage: "prod", Hosts: []string{"h"}}, errors.CodeSkippedHosts},
		{"unreachable", &errors.UnreachableError{Err: errors.New("x")}, errors.CodeUnreachable},
		{"command", &errors.CommandError{Host: "h", Err: errors.New("exit 7")}, errors.CodeCommandFailed},
		{"wrapped command", fmt.Errorf("phase: %w", &errors.CommandError{Host: "h", Err: errors.New("exit 7")}), errors.CodeCommandFailed},
	}
	for _, c := range cases {
		if got := errors.Code(c.err); got != c.want {
			t.Errorf("%s: Code() = %d, want %d", c.name, got, c.want)
		}
	}
}

// IsUnreachable matches an UnreachableError directly and through wrapping, but not a CommandError or a plain error.
func TestIsUnreachable(t *testing.T) {
	u := &errors.UnreachableError{Err: errors.New("boom")}
	if !errors.IsUnreachable(u) {
		t.Error("direct UnreachableError not detected")
	}
	if !errors.IsUnreachable(fmt.Errorf("ctx: %w", u)) {
		t.Error("wrapped UnreachableError not detected")
	}
	if errors.IsUnreachable(&errors.CommandError{Host: "h", Err: errors.New("x")}) {
		t.Error("CommandError wrongly classified unreachable")
	}
	if errors.IsUnreachable(nil) {
		t.Error("nil wrongly classified unreachable")
	}
}

// The pass-through errors keep the wrapped message verbatim (so console output and message-pinned tests are unchanged)
// and Unwrap sees the cause.
func TestPassThroughMessage(t *testing.T) {
	cause := errors.New("Process exited with status 7")
	for _, e := range []error{
		&errors.UnreachableError{Err: cause},
		&errors.CommandError{Host: "h", Err: cause},
	} {
		if e.Error() != cause.Error() {
			t.Errorf("%T.Error() = %q, want %q", e, e.Error(), cause.Error())
		}
		if !errors.Is(e, cause) {
			t.Errorf("%T does not unwrap to its cause", e)
		}
	}
}
