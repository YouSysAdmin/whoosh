// Package entrypoint is the whoosh binary entrypoint, kept separate from the SDK.
package entrypoint

import "github.com/yousysadmin/whoosh/internal/cli"

// Main runs the whoosh CLI.
func Main() { cli.Execute() }
