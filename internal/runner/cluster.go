package runner

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"

	"github.com/yousysadmin/whoosh/internal/errors"
	"github.com/yousysadmin/whoosh/internal/transport/local"
	"github.com/yousysadmin/whoosh/transport/ssh"
)

// Cluster runs commands across a set of targets, reusing one connection per host for its lifetime.
// Output is streamed to a shared writer, prefixed by host and serialized so concurrent hosts don't interleave mid-line.
// Remote targets dial SSH, local targets use the local shell.
type Cluster struct {
	opts Options
	out  io.Writer

	// log, when set, receives each complete output line instead of out getting host-prefixed text - command output is
	// then emitted as structured log records (see SetLineHandler).
	log LineHandler
	// color colorizes the host prefix on the raw stream (green for stdout, red for stderr). Off by default.
	color bool

	outMu sync.Mutex // serializes writes to out

	connMu sync.Mutex
	conns  map[string]*connEntry
}

type connEntry struct {
	once sync.Once
	conn Conn
	err  error
}

// NewCluster creates a cluster that streams command output to out.
func NewCluster(opts Options, out io.Writer) *Cluster {
	return &Cluster{opts: opts, out: out, conns: make(map[string]*connEntry)}
}

// SetLineHandler routes command output through h - one call per complete line, tagged with the host - instead of
// streaming host-prefixed text to the cluster's writer. Set it once before running, nil restores raw streaming.
func (c *Cluster) SetLineHandler(h LineHandler) { c.log = h }

// SetColor enables colorizing the host prefix on the raw stream (green for stdout, red for stderr). Set it once before
// running, it has no effect in line-handler (structured log) mode.
func (c *Cluster) SetColor(on bool) { c.color = on }

// SetOut redirects raw-mode output to w (writer() reads c.out per call, so it takes effect for the next Run). The
// executor uses it to capture a silent_output task's output into a buffer, then restores the original writer. No effect
// in line-handler (structured log) mode, where c.out is unused.
func (c *Cluster) SetOut(w io.Writer) { c.out = w }

// writer builds the per-host stdout/stderr writer: a log writer when a handler is set, else a host-prefixed writer to
// out serialized against the shared mutex.
func (c *Cluster) writer(host string) *LineWriter {
	if c.log != nil {
		return NewLogWriter(host, c.log)
	}
	return newPrefixWriter(c.out, HostLabel(host, c.color)+" ", &c.outMu)
}

// Run executes a command on each target concurrently (up to concurrency at once), where cmdFor yields the per-host
// command string. Connections are opened lazily and cached. With failFast, the first error cancels remaining work.
func (c *Cluster) Run(ctx context.Context, targets []Target, cmdFor func(host string) string, concurrency int, failFast bool) []Result {
	return Fanout(ctx, targets, concurrency, failFast, func(ctx context.Context, t Target) error {
		conn, err := c.conn(ctx, t)
		if err != nil {
			return &errors.UnreachableError{Err: err} // dial failed: host unreachable
		}
		stdout := c.writer(t.Host)
		stderr := c.writer(t.Host)
		defer stdout.Flush()
		defer stderr.Flush()
		err = conn.Run(ctx, cmdFor(t.Host), stdout, stderr)
		if err == nil {
			return nil
		}
		return classify(t, err)
	})
}

// classify maps a run error to its typed category.
// A collateral context cancellation (a sibling failed under failFast, or an operator Ctrl-C) is neither unreachable
// nor a command failure - it stays raw so firstError can prefer a real error over it. A remote error that isn't a
// command exit (non-zero status) means the connection dropped mid-command - the host went unreachable. Anything else
// is a command that ran and exited non-zero (a remote SSH exit or a local failure).
func classify(t Target, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if !t.Local && !ssh.IsExitError(err) {
		return &errors.UnreachableError{Err: err}
	}
	return &errors.CommandError{Host: t.Host, Err: err}
}

// Capture runs cmd on a single target and returns its trimmed stdout, reusing the cached connection.
// Unlike Run it buffers stdout instead of streaming, for commands whose output the caller needs as a value (e.g. a git
// SHA). stderr is still streamed to the cluster writer so a failure stays visible.
// Errors are classified like Run's: a failed dial is an UnreachableError, a non-zero exit a CommandError.
func (c *Cluster) Capture(ctx context.Context, t Target, cmd string) (string, error) {
	conn, err := c.conn(ctx, t)
	if err != nil {
		return "", &errors.UnreachableError{Err: err}
	}
	var stdout bytes.Buffer
	stderr := c.writer(t.Host)
	defer stderr.Flush()
	if err := conn.Run(ctx, cmd, &stdout, stderr); err != nil {
		return "", classify(t, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// conn returns the cached connection for the target, opening it once.
// Local targets get a local transport, remote targets dial SSH.
// Remote connections are pooled per host *and* effective host-key strictness, so a task requiring verification never
// reuses a connection another task opened with verification disabled (and vice versa) - at most two connections per
// host, and only when tasks disagree.
func (c *Cluster) conn(ctx context.Context, t Target) (Conn, error) {
	// Per-target host-key override (e.g. a task skipping known_hosts for ephemeral hosts), otherwise the cluster's shared
	// setting applies.
	strict := c.opts.StrictHostKey
	if t.StrictHostKey != nil {
		strict = *t.StrictHostKey
	}
	key := t.Host
	if !t.Local {
		if strict {
			key += "\x00strict"
		} else {
			key += "\x00insecure"
		}
	}

	c.connMu.Lock()
	e, ok := c.conns[key]
	if !ok {
		e = &connEntry{}
		c.conns[key] = e
	}
	c.connMu.Unlock()

	e.once.Do(func() {
		if t.Local {
			e.conn = local.New()
			return
		}
		opts := c.opts
		opts.StrictHostKey = strict
		// Assign through a concrete variable: a failed Dial returns a nil *ssh.Client, and storing that directly in the Conn
		// interface would make e.conn a non-nil interface wrapping a nil pointer - which then passes the nil check in Close()
		// and panics. On error leave e.conn nil.
		client, err := ssh.Dial(ctx, ssh.Target{
			Host:         t.Host,
			Port:         t.Port,
			User:         t.User,
			IdentityFile: t.IdentityFile,
			Passphrase:   t.Passphrase,
		}, opts)
		if err != nil {
			e.err = err
			return
		}
		e.conn = client
	})
	return e.conn, e.err
}

// Close tears down all cached connections and the shared bastion connection, if any.
func (c *Cluster) Close() {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	for _, e := range c.conns {
		if e.conn != nil {
			_ = e.conn.Close()
		}
	}
	c.conns = make(map[string]*connEntry)
	if c.opts.Bastion != nil {
		_ = c.opts.Bastion.Close()
	}
}
