package cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/yousysadmin/whoosh/internal/errors"
)

// interrupted should fire only for an operator signal: the root context canceled AND the surfaced error a
// context.Canceled. A server-dropped connection (UnreachableError) or a cancellation without a signal must not count.
func TestInterrupted(t *testing.T) {
	cases := []struct {
		name    string
		rootErr error
		err     error
		want    bool
	}{
		{"operator interrupt (wrapped, as firstError surfaces it)", context.Canceled, fmt.Errorf("10.0.0.1: %w", context.Canceled), true},
		{"cancellation but no signal fired", nil, fmt.Errorf("h: %w", context.Canceled), false},
		{"server-dropped connection during a signal", context.Canceled, &errors.UnreachableError{Err: errors.New("connection lost")}, false},
		{"real failure during a signal", context.Canceled, errors.New("boom"), false},
		{"nothing", nil, nil, false},
	}
	for _, c := range cases {
		if got := interrupted(c.rootErr, c.err); got != c.want {
			t.Errorf("%s: interrupted(%v, %v) = %v, want %v", c.name, c.rootErr, c.err, got, c.want)
		}
	}
}
