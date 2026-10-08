package ssh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
)

// Bastion is a jump host every target connection is tunneled through (like OpenSSH ProxyJump, single hop).
// One Bastion is shared by all dials in a run: the first dial opens the SSH connection to the bastion -
// authenticated like any target (its own IdentityFile, else the builtin agent, else SSH_AUTH_SOCK) with the
// same host-key options - and each target then gets its own direct-tcpip channel over it.
// A dial with host-key verification never reuses a bastion connection opened without it, the two modes keep
// separate connections.
type Bastion struct {
	target Target

	mu     sync.Mutex
	closed bool
	strict bastionConn
	loose  bastionConn
}

// bastionConn is the cached outcome of one bastion dial.
type bastionConn struct {
	dialed bool
	client *Client
	err    error
}

// NewBastion declares the jump host. The connection is opened lazily by the first Dial that uses it.
func NewBastion(t Target) *Bastion { return &Bastion{target: t} }

// connect dials the bastion once per host-key mode and caches the client or the failure (a dead bastion fails every
// host the same way a dead host fails once - it is not re-dialed). A dial aborted by context cancellation is not
// cached, so a later call with a live context retries. opts is the run's Options with Bastion cleared to
// end the recursion and agent forwarding disabled for the hop (OpenSSH -J does not forward to the jump host).
func (b *Bastion) connect(ctx context.Context, opts Options) (*Client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, fmt.Errorf("bastion %s: connection closed", b.target.Host)
	}

	bc := &b.loose
	if opts.StrictHostKey {
		bc = &b.strict
	}

	if bc.dialed {
		return bc.client, bc.err
	}

	opts.Bastion = nil
	opts.ForwardAgent = false
	opts.ForwardKey = ""
	client, err := Dial(ctx, b.target, opts)
	if err != nil {
		err = fmt.Errorf("bastion %s: %w", b.target.Host, err)
		// A dial aborted by a cancelled or expired context says nothing about the bastion. Leave it un-dialed
		// so a later call with a live context - notably deploy:failed hooks after a fail-fast cancel - retries
		// instead of failing every remaining host.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
	}

	*bc = bastionConn{dialed: true, client: client, err: err}

	return client, err
}

// dialThrough opens a direct-tcpip channel to addr via the bastion, bounded by the connect timeout (a channel
// open on a wedged bastion has no deadline of its own).
func (b *Bastion) dialThrough(ctx context.Context, addr string, opts Options) (net.Conn, error) {
	client, err := b.connect(ctx, opts)
	if err != nil {
		return nil, err
	}

	timeout := opts.ConnectTimeout
	if timeout == 0 {
		timeout = DefaultConnectTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := client.conn.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("via bastion %s: %w", b.target.Host, err)
	}

	return conn, nil
}

// Close tears down the bastion connections. Safe on a nil receiver and idempotent. A dial after Close fails
// instead of reopening a connection - including on a bastion that was declared but never dialed, so nothing can
// open a connection no caller would ever close.
func (b *Bastion) Close() error {
	if b == nil {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	var errs []error
	for _, bc := range []*bastionConn{&b.strict, &b.loose} {
		if bc.client != nil {
			errs = append(errs, bc.client.Close())
		}

		*bc = bastionConn{}
	}

	b.closed = true

	return errors.Join(errs...)
}
