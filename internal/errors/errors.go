// Package errors defines whoosh's typed errors and their process exit codes.
// An error that implements Error carries a Code(), cli.Execute maps it to the exit status so a caller (CI) can tell a
// config error from an unreachable host from a failed command.
// The package also re-exports the stdlib errors helpers (New/Is/As) so callers import a single errors package.
package errors

import stderrors "errors"

// Error is an application error that carries a process exit code.
type Error interface {
	error
	Code() int
}

// Exit codes, grouped by category.
// These are a stable contract - CI may key off them - so keep existing values fixed and only add new ones.
const (
	CodeOK      = 0 // success
	CodeUnknown = 1 // any error that does not implement Error
	CodeUsage   = 2 // bad CLI invocation

	CodeInterrupted = 130 // terminated by an operator signal (Ctrl-C / SIGTERM)

	CodeConfig = 10 // Deployfile parse/merge/validate/version failure
	CodeLocked = 11 // the deploy lock is held by another run

	CodeUnreachable  = 20 // a (required) host could not be reached
	CodeSkippedHosts = 21 // deployed, but some hosts were skipped (on_unreachable: skip)

	CodeCommandFailed = 30 // a remote/local command ran and exited non-zero
	CodePlugin        = 40 // a plugin/cloud action failed
)

// Code returns the process exit code for err: err.Code() when it (or anything it wraps) implements Error, CodeOK for a
// nil error, else CodeUnknown.
func Code(err error) int {
	if err == nil {
		return CodeOK
	}
	if e, ok := stderrors.AsType[Error](err); ok {
		return e.Code()
	}
	return CodeUnknown
}

func New(text string) error         { return stderrors.New(text) }
func Is(err, target error) bool     { return stderrors.Is(err, target) }
func As(err error, target any) bool { return stderrors.As(err, target) }
func Join(errs ...error) error      { return stderrors.Join(errs...) }
func Unwrap(err error) error        { return stderrors.Unwrap(err) }
