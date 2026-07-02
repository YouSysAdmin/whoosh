// Package local provides a command transport that runs on the operator's own machine via /bin/sh -c, with no SSH
// connection. It mirrors ssh.Client's Run/Close shape so the two are interchangeable behind runner.Conn.
package local

import (
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
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
	// Run the shell in its own process group and kill the whole group on cancel. Killing only the shell (the
	// CommandContext default) orphans its children - dash (Linux /bin/sh) forks even for a single command - and an
	// orphan inherits the output pipes, keeping Wait blocked until it exits on its own.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
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
