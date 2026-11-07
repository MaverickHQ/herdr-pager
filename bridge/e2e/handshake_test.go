package e2e

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/sshserver"
)

// closedWithin reads conn until it fails, for up to d of real time, and says
// whether the bridge closed it (anything but a timeout).
func closedWithin(conn net.Conn, d time.Duration) bool {
	conn.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 256)
	for {
		if _, err := conn.Read(buf); err != nil {
			var ne net.Error
			return !(errors.As(err, &ne) && ne.Timeout())
		}
	}
}

// TestConnectionWithoutSessionClosed checks parent 3.9 (the owner's
// decision, 2026-10-27, open-items O-47): a connection has 15 s on the
// bridge's clock from its accept to its session's exec. One that never sends
// its version line, and one that authenticates but never asks for a session,
// are each still open just before then, and closed at 15 s.
func TestConnectionWithoutSessionClosed(t *testing.T) {
	b := startBridge(t, nil)
	key := b.pair("pager-01")

	t.Run("no version line", func(t *testing.T) {
		silent, err := net.DialTimeout("tcp", b.addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer silent.Close()
		if !b.clock.waitTimer(b.clock.Now().Add(sshserver.HandshakeTimeout)) {
			t.Fatal("the bridge set no 15 s timer for a new connection")
		}
		b.clock.Advance(sshserver.HandshakeTimeout - time.Millisecond)
		if closedWithin(silent, 200*time.Millisecond) {
			t.Fatal("the connection was closed before its 15 s")
		}
		b.clock.Advance(time.Millisecond)
		if !closedWithin(silent, 2*time.Second) {
			t.Fatal("a connection that sent nothing was still open after 15 s")
		}
	})

	t.Run("no session", func(t *testing.T) {
		nc, err := net.DialTimeout("tcp", b.addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer nc.Close()
		at := b.clock.Now().Add(sshserver.HandshakeTimeout)
		c, chans, reqs, err := ssh.NewClientConn(nc, b.addr, &ssh.ClientConfig{
			User: "pager", Auth: []ssh.AuthMethod{ssh.PublicKeys(key)}, HostKeyCallback: ssh.FixedHostKey(b.hostKey),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		go ssh.DiscardRequests(reqs)
		go func() {
			for ch := range chans {
				ch.Reject(ssh.Prohibited, "none")
			}
		}()
		ended := make(chan error, 1)
		go func() { ended <- c.Wait() }()
		if !b.clock.waitTimer(at) {
			t.Fatal("the bridge set no 15 s timer for a new connection")
		}
		b.clock.Advance(sshserver.HandshakeTimeout)
		select {
		case err := <-ended:
			if err != nil && !errors.Is(err, io.EOF) {
				t.Logf("the connection ended: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("an authenticated connection that never asked for a session was still open after 15 s")
		}
	})

	if s := b.log.String(); strings.Count(s, "sshserver: no session within 15 s; connection closed") != 2 {
		t.Errorf("the bridge didn't log the closes:\n%s", s)
	}
}
