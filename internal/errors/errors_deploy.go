package errors

import (
	"fmt"
	"strings"
)

// LockedError reports that the deploy lock on the primary host is held by another run. It maps to CodeLocked.
type LockedError struct {
	Stage string
	Host  string
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("stage %q is locked by another deploy on %s, run deploy:unlock if it is stale", e.Stage, e.Host)
}
func (e *LockedError) Code() int { return CodeLocked }

// SkippedHostsError reports that a deploy completed on the reachable hosts while one or more unreachable hosts were
// skipped under on_unreachable: skip. It makes the CLI exit non-zero without being treated as a deploy failure.
type SkippedHostsError struct {
	Stage string
	Hosts []string
}

func (e *SkippedHostsError) Error() string {
	return fmt.Sprintf("stage %q deployed, but %d host(s) were unreachable and skipped: %s",
		e.Stage, len(e.Hosts), strings.Join(e.Hosts, ", "))
}
func (e *SkippedHostsError) Code() int { return CodeSkippedHosts }
