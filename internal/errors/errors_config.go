package errors

import "fmt"

// ConfigError is a Deployfile parse/merge/validation failure - anything that makes the resolved configuration unusable.
// It maps to CodeConfig.
type ConfigError struct {
	Msg string
	Err error // optional underlying cause
}

func (e *ConfigError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}
func (e *ConfigError) Unwrap() error { return e.Err }
func (e *ConfigError) Code() int     { return CodeConfig }

// Config builds a ConfigError from a printf-style message.
func Config(format string, a ...any) *ConfigError {
	return &ConfigError{Msg: fmt.Sprintf(format, a...)}
}

// VersionError reports that a Deployfile's version: is outside the range this build supports. It maps to CodeConfig.
// Exactly one of TooOld/TooNew is set.
type VersionError struct {
	Have   string // the version found in the Deployfile
	Min    string // oldest version still supported
	Max    string // newest version this build implements
	TooOld bool   // Have < Min
	TooNew bool   // Have > Max
}

func (e *VersionError) Error() string {
	if e.TooNew {
		return fmt.Sprintf("Deployfile version %s needs a newer whoosh (this build supports up to %s), upgrade the tool", e.Have, e.Max)
	}
	return fmt.Sprintf("Deployfile version %s is no longer supported, this whoosh needs version >= %s", e.Have, e.Min)
}
func (e *VersionError) Code() int { return CodeConfig }
