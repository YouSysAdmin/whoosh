package errors

import "fmt"

// PluginError is a plugin failure - loading, startup (cloud calls included), or a plugin action at run time.
// It maps to CodePlugin.
type PluginError struct {
	Msg string
	Err error // optional underlying cause
}

func (e *PluginError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}
func (e *PluginError) Unwrap() error { return e.Err }
func (e *PluginError) Code() int     { return CodePlugin }

// Plugin builds a PluginError from a printf-style message.
func Plugin(format string, a ...any) *PluginError {
	return &PluginError{Msg: fmt.Sprintf(format, a...)}
}
