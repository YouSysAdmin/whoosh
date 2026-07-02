// Package local provides a command transport that runs on the operator's own machine via /bin/sh -c, with no SSH
// connection. It mirrors ssh.Client's Run/Close shape so the two are interchangeable behind runner.Conn.
package local

import (
	"context"
	"io"
	"os/exec"
)

// Client runs commands locally.
type Client struct{}

// New returns a local transport.
func New() *Client { return &Client{} }

// Run executes command through the local shell, streaming output to the writers.
func (c *Client) Run(ctx context.Context, command string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	// On cancellation/timeout CommandContext kills the process, surfacing as "signal: killed".
	// Report the context error instead, so an operator Ctrl-C reads as a cancellation - matching the SSH transport (which
	// returns ctx.Err() too), so the CLI can recognize it as an interrupt rather than a failure.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// Close is a no-op, there is no connection to tear down.
func (c *Client) Close() error { return nil }
