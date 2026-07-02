package ssh_test

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yousysadmin/whoosh/transport/ssh"
	"github.com/yousysadmin/whoosh/transport/sshtest"
)

// frozenProxy forwards TCP between a client and target.
// Once frozen it keeps both sockets open but stops forwarding bytes in either direction - simulating a host that
// vanishes without closing the connection (power loss, network partition), the case where an in-flight command would
// otherwise block indefinitely.
type frozenProxy struct {
	ln     net.Listener
	target string
	frozen atomic.Bool
}

func newFrozenProxy(t *testing.T, target string) *frozenProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := &frozenProxy{ln: ln, target: target}
	go p.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return p
}

func (p *frozenProxy) port() int { return p.ln.Addr().(*net.TCPAddr).Port }
func (p *frozenProxy) freeze()   { p.frozen.Store(true) }

func (p *frozenProxy) serve() {
	for {
		client, err := p.ln.Accept()
		if err != nil {
			return
		}
		server, err := net.Dial("tcp", p.target)
		if err != nil {
			_ = client.Close()
			continue
		}
		go p.pipe(server, client)
		go p.pipe(client, server)
	}
}

// pipe copies src->dst until an error, while frozen it drains src but forwards nothing, so the peer sees silence while
// the socket stays open.
func (p *frozenProxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 && !p.frozen.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// TestKeepalive_DropsVanishedHost reproduces the incident where a host shut down mid-deploy and the command hung until
// Ctrl-C. Keepalive must detect the dead peer and drop the connection so Run returns an error promptly.
func TestKeepalive_DropsVanishedHost(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	proxy := newFrozenProxy(t, net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port)))

	conn, err := ssh.Dial(context.Background(), ssh.Target{
		Host: "127.0.0.1", Port: proxy.port(), User: "deploy", IdentityFile: srv.IdentityFile,
	}, ssh.Options{
		StrictHostKey:     false,
		KeepaliveInterval: 100 * time.Millisecond,
		KeepaliveMaxFails: 3,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	runErr := make(chan error, 1)
	go func() {
		// Separate sinks: SSH writes stdout/stderr concurrently. io.Discard is safe.
		runErr <- conn.Run(context.Background(), "sleep 5", io.Discard, io.Discard)
	}()

	time.Sleep(150 * time.Millisecond) // let the command get going
	proxy.freeze()                     // host vanishes - silence, no close

	select {
	case err := <-runErr:
		if err == nil {
			t.Fatal("expected Run to fail once the host vanished")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("Run hung after the host vanished - keepalive did not drop the connection")
	}
}

// TestKeepalive_HealthyConnectionSurvives guards against false positives: a live host answers keepalives, so a command
// spanning many ping intervals must not be dropped - Run completing without error proves the connection stayed up.
func TestKeepalive_HealthyConnectionSurvives(t *testing.T) {
	srv, err := sshtest.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Close()

	conn, err := ssh.Dial(context.Background(), ssh.Target{
		Host: srv.Host, Port: srv.Port, User: "deploy", IdentityFile: srv.IdentityFile,
	}, ssh.Options{
		StrictHostKey:     false,
		KeepaliveInterval: 50 * time.Millisecond, // ~20 pings during the command
		KeepaliveMaxFails: 2,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// A drop from a false-positive keepalive would surface as a Run error.
	if err := conn.Run(context.Background(), "sleep 1", io.Discard, io.Discard); err != nil {
		t.Fatalf("healthy connection should not be dropped by keepalive: %v", err)
	}
}
