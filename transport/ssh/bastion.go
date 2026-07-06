package ssh

import (
	"context"
	"fmt"
	"net"
	"sync"
)

// Bastion is a jump host every target connection is tunneled through (like OpenSSH ProxyJump, single hop).
// One Bastion is shared by all dials in a run: the first dial opens the SSH connection to the bastion -
// authenticated like any target (its own IdentityFile, else the builtin agent, else SSH_AUTH_SOCK) with the
// same host-key options - and each target then gets its own direct-tcpip channel over it.
type Bastion struct {
	target Target

	mu     sync.Mutex
	dialed bool
	client *Client
	err    error
}

// NewBastion declares the jump host. The connection is opened lazily by the first Dial that uses it.
func NewBastion(t Target) *Bastion { return &Bastion{target: t} }

// connect dials the bastion once and caches the client or the failure (a dead bastion fails every host the
// same way a dead host fails once - it is not re-dialed). opts is the run's Options with Bastion cleared to
// end the recursion and agent forwarding disabled for the hop (OpenSSH -J does not forward to the jump host).
func (b *Bastion) connect(ctx context.Context, opts Options) (*Client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.dialed {
		return b.client, b.err
	}
	b.dialed = true
	opts.Bastion = nil
	opts.ForwardAgent = false
	opts.ForwardKey = ""
	b.client, b.err = Dial(ctx, b.target, opts)
	if b.err != nil {
		b.err = fmt.Errorf("bastion %s: %w", b.target.Host, b.err)
	}
	return b.client, b.err
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

// Close tears down the bastion connection. Safe on a nil receiver and idempotent. A dial after Close fails
// instead of reopening the connection.
func (b *Bastion) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client == nil {
		return nil
	}
	err := b.client.Close()
	b.client = nil
	b.err = fmt.Errorf("bastion %s: connection closed", b.target.Host)
	return err
}
