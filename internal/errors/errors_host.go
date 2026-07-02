package errors

// UnreachableError wraps an error that means a host could not be reached or its connection was lost mid-command - as
// opposed to a command that ran and exited non-zero. The deploy lifecycle's on_unreachable policy keys off it.
// It maps to CodeUnreachable.
// Error/Unwrap pass through to the wrapped error so the surfaced message is unchanged and Is/As see through it.
type UnreachableError struct{ Err error }

func (e *UnreachableError) Error() string { return e.Err.Error() }
func (e *UnreachableError) Unwrap() error { return e.Err }
func (e *UnreachableError) Code() int     { return CodeUnreachable }

// IsUnreachable reports whether err is, or wraps, an UnreachableError.
func IsUnreachable(err error) bool {
	var u *UnreachableError
	return As(err, &u)
}

// CommandError wraps an error from a command that ran and exited non-zero (a remote SSH exit or a local shell failure),
// as opposed to an unreachable host. It maps to CodeCommandFailed.
// Like UnreachableError it passes Error/Unwrap through, so the message and ssh.IsExitError detection are unchanged - it
// only adds a code.
type CommandError struct {
	Host string
	Err  error
}

func (e *CommandError) Error() string { return e.Err.Error() }
func (e *CommandError) Unwrap() error { return e.Err }
func (e *CommandError) Code() int     { return CodeCommandFailed }
