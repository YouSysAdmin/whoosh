// Package ssh provides the SSH transport for whoosh: dialing target hosts, running commands with streamed output, and
// fanning a command out across a set of hosts in parallel.
package ssh

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// DefaultPort is used when a Target does not specify one.
const DefaultPort = 22

// DefaultConnectTimeout bounds the TCP+handshake phase.
const DefaultConnectTimeout = 15 * time.Second

// Keepalive defaults bound how long a host that vanishes mid-command (powered off, network partition) goes undetected.
// A keepalive request is sent every interval, after MaxFails consecutive misses the connection is dropped, which makes
// any in-flight command return an error instead of blocking indefinitely (a TCP read on a silently-dead peer has no
// deadline of its own).
const (
	DefaultKeepaliveInterval = 10 * time.Second
	DefaultKeepaliveMaxFails = 3
)

// Target identifies a single host to connect to.
type Target struct {
	Host         string
	Port         int
	User         string
	IdentityFile string
	Passphrase   string // optional, decrypts an encrypted IdentityFile
}

func (t Target) addr() string {
	port := t.Port
	if port == 0 {
		port = DefaultPort
	}

	return net.JoinHostPort(t.Host, strconv.Itoa(port))
}

// Options holds connection settings shared by all targets in a run.
type Options struct {
	StrictHostKey  bool   // verify the host key against KnownHostsFile (false accepts any key)
	KnownHostsFile string // known_hosts path used for verification

	// AcceptNew, together with StrictHostKey, trusts a host seen for the first time (OpenSSH StrictHostKeyChecking=accept-new):
	// its key is appended to KnownHostsFile - created along with its parent dir when missing - and the connection proceeds.
	// A key that conflicts with an existing entry still fails. No effect without StrictHostKey.
	AcceptNew      bool
	ConnectTimeout time.Duration // bound on the TCP connect + SSH handshake (zero uses the default)

	// KeepaliveInterval/KeepaliveMaxFails detect a host that vanishes mid-command.
	// Zero uses the defaults, a negative interval disables keepalive.
	KeepaliveInterval time.Duration
	KeepaliveMaxFails int

	// ForwardAgent forwards the operator's local ssh-agent (SSH_AUTH_SOCK) to each host, so commands there (notably git)
	// authenticate with the operator's keys.
	ForwardAgent bool

	// ForwardKey forwards only the key at this path via an in-memory agent (the key is never written to the host).
	// Takes precedence over ForwardAgent.
	ForwardKey string

	// Agent is the builtin in-memory ssh-agent (from NewAgent). When set, its keys are offered to every host and the
	// system agent (SSH_AUTH_SOCK) is not consulted for authentication. With ForwardAgent it is also the agent forwarded
	// to the hosts (ForwardKey still takes precedence).
	Agent agent.Agent

	// Bastion, when set, tunnels every connection through this jump host (one shared SSH connection, one
	// direct-tcpip channel per target). Nil connects directly.
	Bastion *Bastion
}

// Client is a live SSH connection to one host.
type Client struct {
	conn      *ssh.Client
	host      string
	forward   bool          // agent forwarding is enabled for this connection
	done      chan struct{} // closed on Close, stops the keepalive loop
	gone      chan struct{} // closed once the connection has ended, for any reason
	closeOnce sync.Once
}

// Dial opens an SSH connection to the target.
func Dial(ctx context.Context, t Target, opts Options) (*Client, error) {
	auth, authCleanup, err := authMethods(t.IdentityFile, t.Passphrase, opts.Agent)
	if err != nil {
		return nil, err
	}

	// Auth only happens during the handshake, so the agent socket (if one was opened) is released as soon as the
	// dial resolves.
	defer authCleanup()
	hostKey, known, err := hostKeyCallback(opts)
	if err != nil {
		return nil, err
	}

	user := cmp.Or(t.User, os.Getenv("USER"))

	timeout := cmp.Or(opts.ConnectTimeout, DefaultConnectTimeout)

	// One budget covers the whole connect: TCP dial (or the bastion channel open) and the SSH handshake share the
	// deadline instead of each getting the full timeout.
	deadline := time.Now().Add(timeout)

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: hostKey,
		Timeout:         timeout,
	}

	var netConn net.Conn
	if opts.Bastion != nil {
		// The deadline ctx caps the channel open (and a first-use bastion connect) at the shared budget. Cancelling
		// after the open is safe - an established direct-tcpip channel outlives its dial context.
		dctx, cancel := context.WithDeadline(ctx, deadline)
		// The error is already labeled with the bastion host, connectError would unwrap past the label.
		netConn, err = opts.Bastion.dialThrough(dctx, t.addr(), opts)
		cancel()
		if err != nil {
			return nil, err
		}
	} else {
		d := net.Dialer{Deadline: deadline}
		netConn, err = d.DialContext(ctx, "tcp", t.addr())
		if err != nil {
			return nil, connectError(err)
		}
	}

	cfg.HostKeyAlgorithms = knownHostAlgorithms(known, t.addr(), netConn.RemoteAddr())
	stop := handshakeWatchdog(ctx, netConn, time.Until(deadline))
	conn, chans, reqs, err := ssh.NewClientConn(netConn, t.addr(), cfg)
	if interrupted := stop(); interrupted {
		// The watchdog closed netConn, so even a nominally successful handshake is on a dead connection.
		if conn != nil {
			_ = conn.Close()
		}

		_ = netConn.Close()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("ssh handshake: %w", ctx.Err())
		}

		return nil, fmt.Errorf("ssh handshake: timeout after %s", timeout)
	}

	if err != nil {
		_ = netConn.Close()

		return nil, fmt.Errorf("ssh handshake: %w", err)
	}

	client := &Client{conn: ssh.NewClient(conn, chans, reqs), host: t.Host, done: make(chan struct{}), gone: make(chan struct{})}
	go func() {
		_ = client.conn.Wait()
		close(client.gone)
	}()

	if opts.ForwardAgent || opts.ForwardKey != "" {
		if err := setupForwarding(client.conn, opts); err != nil {
			_ = client.Close()

			return nil, err
		}

		client.forward = true
	}

	client.startKeepalive(opts)

	return client, nil
}

// startKeepalive launches the background liveness check unless disabled (negative interval).
// Closing the connection on detected death unblocks any in-flight Run.
func (c *Client) startKeepalive(opts Options) {
	interval := cmp.Or(opts.KeepaliveInterval, DefaultKeepaliveInterval)

	if interval < 0 {
		return // explicitly disabled
	}

	maxFails := opts.KeepaliveMaxFails
	if maxFails <= 0 {
		maxFails = DefaultKeepaliveMaxFails
	}

	go c.keepalive(interval, maxFails)
}

// keepalive pings the host every interval and closes the connection after maxFails consecutive misses, so a vanished
// host surfaces as an error on the in-flight command rather than a hang.
func (c *Client) keepalive(interval time.Duration, maxFails int) {
	tick := time.Tick(interval)
	fails := 0
	for {
		select {
		case <-c.done:
			return
		case <-tick:
			if c.ping(interval) {
				fails = 0
				continue
			}

			if fails++; fails >= maxFails {
				_ = c.conn.Close() // makes a blocked sess.Wait() return an error

				return
			}
		}
	}
}

// ping sends one keepalive request and reports whether the host answered within timeout.
// The request itself can block on a dead connection, so it is bounded. When the timeout branch wins, the sender
// goroutine stays parked in SendRequest until the connection closes (the keepalive loop closes it after maxFails
// misses), so a wedged host accumulates at most a few of these transiently - never a permanent leak.
func (c *Client) ping(timeout time.Duration) bool {
	res := make(chan error, 1)
	go func() {
		_, _, err := c.conn.SendRequest("keepalive@openssh.com", true, nil)
		res <- err
	}()
	select {
	case err := <-res:
		return err == nil
	case <-time.After(timeout):
		return false
	case <-c.done:
		return true // closing anyway, don't record a failure
	}
}

// agentForwardRequest is the session request that asks the host to forward its agent-auth socket back to us (the same
// name OpenSSH uses).
const agentForwardRequest = "auth-agent-req@openssh.com"

// setupForwarding routes agent-auth requests from the host to an in-memory keyring holding ForwardKey, the builtin
// agent, or the operator's local ssh-agent - in that precedence order.
func setupForwarding(conn *ssh.Client, opts Options) error {
	if opts.ForwardKey != "" {
		kr, err := keyringFromFile(opts.ForwardKey)
		if err != nil {
			return err
		}

		return agent.ForwardToAgent(conn, kr)
	}

	if opts.Agent != nil {
		return agent.ForwardToAgent(conn, opts.Agent)
	}

	sock, err := localAgentSocket()
	if err != nil {
		return err
	}

	return agent.ForwardToRemote(conn, sock)
}

// keyringFromFile builds an in-memory agent holding the private key at path.
// The key is loaded locally and presented over the agent protocol - it is never written to the remote host.
func keyringFromFile(path string) (agent.Agent, error) {
	key, err := loadKeyFile("forward_key", path, "")
	if err != nil {
		if isEncryptedKeyError(err) {
			return nil, fmt.Errorf("%w (encrypted keys must use forward_agent)", err)
		}

		return nil, err
	}

	kr := agent.NewKeyring()
	if err := addKey(kr, "forward_key", path, key); err != nil {
		return nil, fmt.Errorf("load forward_key %s: %w", path, err)
	}

	return kr, nil
}

// localAgentSocket returns the operator's ssh-agent socket, or an error when no agent is available.
func localAgentSocket() (string, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return "", errors.New("forward_agent set but no ssh-agent available (SSH_AUTH_SOCK is unset)")
	}

	return sock, nil
}

// IsExitError reports whether err is a remote command exit - the command ran and returned a non-zero status - as
// opposed to a connection/transport failure (a dropped or never-established connection).
// Callers use it to tell a genuine command failure apart from an unreachable host.
func IsExitError(err error) bool {
	_, ok := errors.AsType[*ssh.ExitError](err)

	return ok
}

// handshakeWatchdog bounds the SSH handshake by closing netConn when ctx is cancelled or timeout elapses first.
// ssh.NewClientConn has no deadline of its own (ClientConfig.Timeout only applies inside x/crypto's ssh.Dial), so a
// host that accepts TCP but never answers SSH would otherwise hang forever - and keepalive starts only after the
// handshake. Closing the conn is used instead of SetDeadline because bastion-tunneled conns do not support deadlines.
// The returned stop func must be called exactly once after NewClientConn returns; it reports whether the watchdog
// already closed the connection (so the caller can attribute the failure to the timeout or cancellation).
func handshakeWatchdog(ctx context.Context, netConn net.Conn, timeout time.Duration) (stop func() bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	stopClose := context.AfterFunc(ctx, func() { _ = netConn.Close() })

	return func() bool {
		fired := !stopClose()
		cancel()

		return fired
	}
}

// connectError reduces a net dial error to a concise reason.
// The net layer formats dial failures as "dial tcp <host>:<port>: <reason>", the host is already supplied by the caller
// (the orchestration layer labels each host), so repeating it is just noise.
// The underlying error stays wrapped: a dial aborted by an expired context maps to a net timeout that still satisfies
// errors.Is(err, context.DeadlineExceeded), and Bastion.connect relies on that identity to avoid caching the abort.
func connectError(err error) error {
	if nerr, ok := errors.AsType[net.Error](err); ok && nerr.Timeout() {
		if op, ok := errors.AsType[*net.OpError](err); ok && op.Err != nil {
			return fmt.Errorf("connection timed out: %w", op.Err)
		}

		return fmt.Errorf("connection timed out: %w", err)
	}

	// Unwrap *net.OpError to drop the "dial tcp <addr>:" prefix, leaving the underlying cause (e.g.
	// "connect: connection refused").
	if op, ok := errors.AsType[*net.OpError](err); ok && op.Err != nil {
		return op.Err
	}

	return err
}

// Run executes cmd on the host, streaming stdout/stderr to the given writers.
// It returns the command's exit error (an *ssh.ExitError for non-zero exits).
func (c *Client) Run(ctx context.Context, cmd string, stdout, stderr io.Writer) error {
	sess, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}

	defer func() { _ = sess.Close() }()

	sess.Stdout = stdout
	sess.Stderr = stderr

	if c.forward {
		// Request agent forwarding without waiting for a reply, exactly as OpenSSH `ssh -A` does.
		// If the host allows forwarding it serves the auth-agent channel registered at dial, if it refuses (e.g.
		// AllowAgentForwarding no), the request is simply ignored and the command still runs - rather than aborting commands
		// that don't need the agent at all. A dead connection surfaces at Start below.
		_, _ = sess.SendRequest(agentForwardRequest, false, nil)
	}

	if err := sess.Start(cmd); err != nil {
		return fmt.Errorf("start command: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()

	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()

		return ctx.Err()
	case err := <-done:
		return err
	}
}

// ended reports whether the connection is gone (closed by Close, by keepalive, or by the server).
func (c *Client) ended() bool {
	select {
	case <-c.gone:
		return true
	default:
		return false
	}
}

// Close tears down the connection and stops the keepalive loop.
// It tolerates a nil client/connection so a failed Dial that never produced a live connection is safe to close, and is
// safe to call more than once.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}

	if c.done != nil {
		c.closeOnce.Do(func() { close(c.done) })
	}

	return c.conn.Close()
}

// authMethods assembles the auth methods for a dial. The returned cleanup closes the system ssh-agent connection
// opened here, if any - auth callbacks only run during the handshake, so the caller closes it once the handshake is
// over (either way) instead of leaking one socket per dial. cleanup is never nil.
func authMethods(identityFile, passphrase string, ag agent.Agent) (methods []ssh.AuthMethod, cleanup func(), err error) {
	cleanup = func() {}

	if identityFile != "" {
		signer, err := loadIdentity(identityFile, passphrase)
		if err != nil {
			return nil, cleanup, err
		}

		methods = append(methods, ssh.PublicKeys(signer))
	}

	// The builtin agent replaces the system agent: its keys are the operator's declared identities, so falling back to
	// SSH_AUTH_SOCK on top of them would defeat the point of pinning the keys in config.
	if ag != nil {
		return append(methods, ssh.PublicKeysCallback(ag.Signers)), cleanup, nil
	}

	var agentErr error
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			// Non-fatal while another method exists, but keep the cause: a stale socket must not silently
			// degrade auth, and with no other method it becomes the error the user sees.
			agentErr = err
			slog.Debug("ssh-agent unreachable", "sock", sock, "error", err)
		} else {
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
			cleanup = func() { _ = conn.Close() }
		}
	}

	if len(methods) == 0 {
		if agentErr != nil {
			return nil, cleanup, fmt.Errorf("no SSH auth available: ssh-agent (SSH_AUTH_SOCK) unreachable: %w (set ssh.identity_file / ssh.identities or restart the agent)", agentErr)
		}

		return nil, cleanup, errors.New("no SSH auth available: set ssh.identity_file / ssh.identities or start an ssh-agent (SSH_AUTH_SOCK)")
	}

	return methods, cleanup, nil
}

func loadIdentity(path, passphrase string) (ssh.Signer, error) {
	key, err := loadKeyFile("identity", path, passphrase)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, fmt.Errorf("parse identity %s: %w", path, err)
	}

	return signer, nil
}

// hostKeyCallback returns the callback that verifies host keys and, when verifying, the plain known_hosts callback
// knownHostAlgorithms probes.
func hostKeyCallback(opts Options) (ssh.HostKeyCallback, ssh.HostKeyCallback, error) {
	if !opts.StrictHostKey {
		return ssh.InsecureIgnoreHostKey(), nil, nil
	}

	khFile := opts.KnownHostsFile
	if khFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil, err
		}

		khFile = filepath.Join(home, ".ssh", "known_hosts")
	} else {
		khFile = expandHome(khFile)
	}

	if opts.AcceptNew {
		if err := ensureKnownHosts(khFile); err != nil {
			return nil, nil, fmt.Errorf("known_hosts %s: %w", khFile, err)
		}
	}

	cb, err := knownhosts.New(khFile)
	if err != nil {
		return nil, nil, fmt.Errorf("known_hosts %s: %w (set ssh.strict_host_key: false to skip verification)", khFile, err)
	}

	if opts.AcceptNew {
		return acceptNewCallback(khFile, cb), cb, nil
	}

	return cb, cb, nil
}

// probeKey never matches a known_hosts entry, so verifying it lists the keys recorded for a host.
var probeKey = func() ssh.PublicKey {
	k, err := ssh.NewPublicKey(ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)))
	if err != nil {
		panic(err)
	}

	return k
}()

// knownHostAlgorithms returns the host key algorithms of the keys known_hosts records for the host, so the server is
// asked for a key that can match (an ed25519 only entry fails against the default order, which prefers ECDSA and RSA).
// It returns nil, the library default, when nothing is recorded or known is nil.
func knownHostAlgorithms(known ssh.HostKeyCallback, hostname string, remote net.Addr) []string {
	if known == nil {
		return nil
	}

	kerr, ok := errors.AsType[*knownhosts.KeyError](known(hostname, remote, probeKey))
	if !ok {
		return nil
	}

	var algos []string
	for _, w := range kerr.Want {
		names := []string{w.Key.Type()}
		if names[0] == ssh.KeyAlgoRSA {
			names = []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
		}

		for _, n := range names {
			if !slices.Contains(algos, n) {
				algos = append(algos, n)
			}
		}
	}

	return algos
}

// ensureKnownHosts creates the known_hosts file (and its parent dir) when missing, so accept_new works on a fresh
// machine where nothing has populated ~/.ssh yet - knownhosts.New errors on a nonexistent file.
func ensureKnownHosts(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}

	return f.Close()
}

// acceptNewMu guards appends to known_hosts files: parallel dials each verify against a snapshot of the file
// (knownhosts.New reads it once), so without the recorded map two first contacts with the same host would both append.
// The map is keyed by host (not by full entry) so two parallel first contacts presenting different keys are detected
// as a conflict instead of both being recorded and trusted.
var acceptNewMu struct {
	sync.Mutex
	recorded map[string]string // "path\n" + normalized host -> accepted known_hosts line
}

// acceptNewCallback wraps a knownhosts callback with trust-on-first-use: an unknown host's key is appended to path and
// accepted, while a key conflicting with an existing entry (or any other verification failure) is still rejected -
// including a key that conflicts with one a parallel dial just recorded but this dial's snapshot has not seen yet.
func acceptNewCallback(path string, cb ssh.HostKeyCallback) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := cb(hostname, remote, key)
		kerr, ok := errors.AsType[*knownhosts.KeyError](err)
		if !ok || len(kerr.Want) > 0 {
			return err // nil (host already known), a conflicting key, or an unrelated failure
		}

		host := knownhosts.Normalize(hostname)
		line := knownhosts.Line([]string{host}, key)
		hostKey := path + "\n" + host

		acceptNewMu.Lock()
		defer acceptNewMu.Unlock()
		if prev, ok := acceptNewMu.recorded[hostKey]; ok {
			if prev == line {
				return nil // a parallel dial already recorded the same key
			}

			return fmt.Errorf("ssh: host key mismatch for %s: key %s conflicts with the one recorded by a parallel connection", hostname, ssh.FingerprintSHA256(key))
		}

		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("known_hosts %s: record new host key: %w", path, err)
		}

		_, werr := f.WriteString(line + "\n")
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}

		if werr != nil {
			return fmt.Errorf("known_hosts %s: record new host key: %w", path, werr)
		}

		if acceptNewMu.recorded == nil {
			acceptNewMu.recorded = map[string]string{}
		}

		acceptNewMu.recorded[hostKey] = line
		slog.Info("accepted new host key", "host", hostname, "fingerprint", ssh.FingerprintSHA256(key), "known_hosts", path)

		return nil
	}
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}

	return p
}
