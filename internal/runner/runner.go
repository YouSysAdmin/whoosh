// Package runner executes commands across a set of targets, independent of transport.
// Each target is either remote (SSH) or local (the operator's machine), both satisfy Conn, so the deploy lifecycle,
// task execution, roles, parallelism and output handling are identical in either mode.
package runner

import (
	"context"
	"io"
	"sync"

	"github.com/yousysadmin/whoosh/transport/ssh"
)

// Conn is a command transport: run a command, then close. Both *ssh.Client and *local.Client satisfy it.
type Conn interface {
	Run(ctx context.Context, cmd string, stdout, stderr io.Writer) error
	Close() error
}

// Options are the SSH connection settings, ignored for local targets.
type Options = ssh.Options

// Target identifies where to run a command.
// When Local is set, the command runs via the local shell and the SSH fields are ignored.
type Target struct {
	Host         string // host address (IP or DNS name)
	Port         int    // SSH port
	User         string // SSH login user
	IdentityFile string // private key file used to authenticate
	Passphrase   string // optional, decrypts an encrypted IdentityFile
	Local        bool   // run via the local shell instead of SSH
	// StrictHostKey, when non-nil, overrides the cluster's StrictHostKey for this host's dial (a task may skip known_hosts
	// verification for ephemeral hosts).
	// Connections are pooled per host and effective strictness, so a target requiring verification never reuses a
	// connection opened with verification disabled (and vice versa).
	StrictHostKey *bool
}

// Result is the outcome of running work against one host.
type Result struct {
	Host string // the host the work ran against
	Err  error  // nil on success, the failure (or context cancellation) otherwise
}

// Fanout runs fn against each target concurrently, up to `concurrency` at a time.
// When failFast is set, the first error cancels the shared context so in-flight work stops and pending work is skipped.
// Results are returned in target order.
func Fanout(ctx context.Context, targets []Target, concurrency int, failFast bool, fn func(context.Context, Target) error) []Result {
	if concurrency <= 0 {
		concurrency = len(targets)
	}
	if concurrency <= 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]Result, len(targets))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, t Target) {
			defer wg.Done()
			defer func() { <-sem }()

			results[i] = Result{Host: t.Host}
			if ctx.Err() != nil {
				results[i].Err = ctx.Err()
				return
			}
			if err := fn(ctx, t); err != nil {
				results[i].Err = err
				if failFast {
					cancel()
				}
			}
		}(i, t)
	}
	wg.Wait()
	return results
}

// RunCommand runs the same command across targets, streaming output to out prefixed by host (colorized when color is
// set: green for stdout, red for stderr).
// It is a one-shot convenience over Cluster: connections are opened and closed within the call.
func RunCommand(ctx context.Context, targets []Target, opts Options, cmd string, out io.Writer, color bool, concurrency int, failFast bool) []Result {
	cl := NewCluster(opts, out)
	cl.SetColor(color)
	defer cl.Close()
	return cl.Run(ctx, targets, func(string) string { return cmd }, concurrency, failFast)
}

// Failed reports whether any result carries an error.
func Failed(results []Result) bool {
	for _, r := range results {
		if r.Err != nil {
			return true
		}
	}
	return false
}
