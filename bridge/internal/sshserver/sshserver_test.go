package sshserver

import (
	"bytes"
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/clock"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devsim"
)

func signer(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestServeStopsAStalledHandshake: a connection still in its handshake when
// ctx ends is closed, so Serve, which waits for every connection, returns
// promptly (the owner's decision, 2026-10-27, open-items O-47).
func TestServeStopsAStalledHandshake(t *testing.T) {
	s := &Server{
		HostKey: signer(t),
		Devices: func() []devices.Device { return nil },
		Handle:  func(context.Context, devices.Device, io.ReadWriter) {},
		Clock:   clock.Real{},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, l) }()
	stalled, err := net.Dial("tcp", l.Addr().String()) // it never sends its version line
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	time.Sleep(100 * time.Millisecond) // accepted, and in its handshake
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve hadn't returned 1 s after ctx ended, with a handshake stalled")
	}
}

// TestServeWaitsForSessions checks that Serve returns only once every
// session's Handle has: a session still running when ctx ends keeps Serve
// from returning until it ends (lane e2e's finding 5; the owner's decision,
// 2026-10-27, open-items O-34). serve's own shutdown bound stays its own.
func TestServeWaitsForSessions(t *testing.T) {
	host, dev := signer(t), signer(t)
	started, release := make(chan struct{}), make(chan struct{})
	s := &Server{
		HostKey: host,
		Devices: func() []devices.Device { return []devices.Device{{Name: "pager-01", PublicKey: dev.PublicKey()}} },
		Handle: func(context.Context, devices.Device, io.ReadWriter) {
			close(started)
			<-release // a session slow to end, whatever ctx says
		},
		Clock: clock.Real{},
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, l) }()

	d, err := devsim.Dial(l.Addr().String(), dev, host.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the session never started")
	}
	cancel()
	select {
	case err := <-served:
		close(release)
		t.Fatalf("Serve returned (%v) while a session was still running", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve didn't return once the session had ended")
	}
}

// testServer is a server for devs on the real clock, logging into log.
func testServer(t *testing.T, log io.Writer, devs ...ssh.Signer) *Server {
	t.Helper()
	return &Server{
		HostKey: signer(t),
		Devices: func() []devices.Device {
			var ds []devices.Device
			for i, d := range devs {
				ds = append(ds, devices.Device{Name: fmt.Sprintf("pager-%02d", i+1), PublicKey: d.PublicKey()})
			}
			return ds
		},
		Handle: func(context.Context, devices.Device, io.ReadWriter) {},
		Clock:  clock.Real{},
		Log:    slog.New(slog.NewTextHandler(log, nil)),
	}
}

// serve runs s on a new listener (wrap, if not nil, wraps it) until the test
// ends, and returns its address and what Serve returned, once it has.
func serve(t *testing.T, s *Server, wrap func(net.Listener) net.Listener) (string, <-chan error) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if wrap != nil {
		l = wrap(l)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-served:
		case <-time.After(5 * time.Second):
			t.Error("Serve hadn't returned 5 s after ctx ended")
		}
	})
	return addr, served
}

// emfile fails its first fails Accepts as a process out of file descriptors
// does (EMFILE), then accepts.
type emfile struct {
	net.Listener
	fails atomic.Int32
}

func (l *emfile) Accept() (net.Conn, error) {
	if l.fails.Add(-1) >= 0 {
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: os.NewSyscallError("accept", syscall.EMFILE)}
	}
	return l.Listener.Accept()
}

// TestServeOutlivesAcceptErrors: an Accept that fails for want of file
// descriptors (EMFILE: a flood of connections can cause it) is tried again
// after a backoff, so Serve keeps serving and serve doesn't exit (the
// security pass, 2026-11-01).
func TestServeOutlivesAcceptErrors(t *testing.T) {
	dev := signer(t)
	s := testServer(t, io.Discard, dev)
	started := make(chan struct{}, 1)
	s.Handle = func(context.Context, devices.Device, io.ReadWriter) { started <- struct{}{} }
	addr, served := serve(t, s, func(l net.Listener) net.Listener {
		e := &emfile{Listener: l}
		e.fails.Store(5)
		return e
	})
	d, err := devsim.Dial(addr, dev, s.HostKey.PublicKey())
	if err != nil {
		select {
		case err := <-served:
			t.Fatalf("Serve returned %v on EMFILE", err)
		default:
		}
		t.Fatal(err)
	}
	defer d.Close()
	select {
	case <-started:
	case err := <-served:
		t.Fatalf("Serve returned %v on EMFILE", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the session never started")
	}
}

// banner reads what the server sends a client that sends nothing: its SSH
// version line while it holds the connection in its handshake, or nothing
// before EOF if it closed it at its accept.
func banner(t *testing.T, c net.Conn) string {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("reading the server's version line: %v", err)
	}
	return string(buf[:n])
}

// TestServeCapsHandshakesPerAddress: at most MaxHandshakesPerAddr
// connections from one address are in their handshake at once; one more is
// closed at its accept, before the server sends a byte, and a slot frees
// when a connection leaves its handshake (the security pass, 2026-11-01).
func TestServeCapsHandshakesPerAddress(t *testing.T) {
	var log syncBuffer
	addr, _ := serve(t, testServer(t, &log), nil)
	var held []net.Conn
	defer func() {
		for _, c := range held {
			c.Close()
		}
	}()
	for i := 0; i < MaxHandshakesPerAddr; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
		if b := banner(t, c); !strings.HasPrefix(b, "SSH-2.0-") {
			t.Fatalf("connection %d of %d: got %q, want the server's version line", i+1, MaxHandshakesPerAddr, b)
		}
	}
	extra, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	if b := banner(t, extra); b != "" {
		t.Fatalf("connection %d from one address: got %q, want it closed at its accept", MaxHandshakesPerAddr+1, b)
	}
	held[0].Close() // its handshake fails, and its slot frees
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(banner(t, c), "SSH-2.0-") {
			held = append(held, c)
			break
		}
		c.Close()
		if time.Now().After(deadline) {
			t.Fatal("no slot freed 5 s after a connection in its handshake closed")
		}
	}
	if !strings.Contains(log.String(), "too many connections in their handshake") {
		t.Errorf("the closed connection wasn't logged:\n%s", log.String())
	}
}

// closer is a connection that records its Close.
type closer struct{ closed atomic.Bool }

func (c *closer) Close() error { c.closed.Store(true); return nil }

// TestHandshakesCapsTheTotal: at most MaxHandshakes connections are in their
// handshake at once, from any addresses; with every address holding one, a
// new address gets none, and a release frees a slot.
func TestHandshakesCapsTheTotal(t *testing.T) {
	var h handshakes
	var first *slot
	for i := 0; i < MaxHandshakes; i++ {
		s, ok := h.acquire(fmt.Sprintf("192.0.2.%d", i), &closer{})
		if !ok {
			t.Fatalf("handshake %d of %d refused", i+1, MaxHandshakes)
		}
		first = cmp.Or(first, s)
	}
	if _, ok := h.acquire("198.51.100.1", &closer{}); ok {
		t.Fatalf("handshake %d allowed with every address holding one; the cap is %d", MaxHandshakes+1, MaxHandshakes)
	}
	h.release(first)
	if _, ok := h.acquire("198.51.100.1", &closer{}); !ok {
		t.Fatal("a released slot wasn't free again")
	}
}

// TestHandshakesEvictTheBusiestAddress: when every slot is taken, a
// connection from an address holding fewer slots than the busiest one by
// more than one closes the busiest address's oldest connection and takes its
// slot, so 8 addresses holding 4 each can't keep a pager out; the busiest
// address loses only down to the newcomer's share, and a pager holding one
// slot is never the one closed (the security pass's review, 2026-11-01).
func TestHandshakesEvictTheBusiestAddress(t *testing.T) {
	var h handshakes
	conns := map[string][]*closer{}
	take := func(addr string) (*slot, bool) {
		c := &closer{}
		s, ok := h.acquire(addr, c)
		if ok {
			conns[addr] = append(conns[addr], c)
		}
		return s, ok
	}
	for i := 0; i < MaxHandshakes/MaxHandshakesPerAddr; i++ {
		for range MaxHandshakesPerAddr {
			if _, ok := take(fmt.Sprintf("192.0.2.%d", i)); !ok {
				t.Fatal("a slot refused before the cap")
			}
		}
	}
	pager, ok := take("198.51.100.7")
	if !ok {
		t.Fatalf("the cap held by %d addresses of %d each kept a new address out", MaxHandshakes/MaxHandshakesPerAddr, MaxHandshakesPerAddr)
	}
	closed := 0
	for addr, cs := range conns {
		for i, c := range cs {
			if c.closed.Load() {
				closed++
				if i != 0 || addr == "198.51.100.7" {
					t.Errorf("closed %s's connection %d; want the busiest address's oldest", addr, i+1)
				}
			}
		}
	}
	if closed != 1 {
		t.Fatalf("%d connections closed to admit one; want 1", closed)
	}
	// A busy address taking another slot back: only from one busier by more
	// than one, never from the pager.
	for range 40 {
		take("192.0.2.0")
		take("192.0.2.1")
	}
	for _, c := range conns["198.51.100.7"] {
		if c.closed.Load() {
			t.Fatal("the pager's connection, its address's only one, was closed")
		}
	}
	h.release(pager)
	h.release(pager) // twice is once
	if _, ok := take("198.51.100.7"); !ok {
		t.Fatal("the pager's released slot wasn't free again")
	}
}

// TestServeQuietensUnauthenticatedLogLines: a refused key, and a connection
// with no session in time, are each logged at most once a second, the next
// line saying how many were held back, so an unauthenticated client can't
// flood the log (the security pass, 2026-11-01, as parent 13.6 does for a
// session's own messages).
func TestServeQuietensUnauthenticatedLogLines(t *testing.T) {
	var log syncBuffer
	s := testServer(t, &log)
	clk := &frozenClock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), scale: 1}
	s.Clock = clk
	addr, _ := serve(t, s, nil)
	for i := 0; i < 5; i++ {
		if _, err := devsim.Dial(addr, signer(t), s.HostKey.PublicKey()); err == nil {
			t.Fatal("an unpaired key was let in")
		}
	}
	if n := strings.Count(log.String(), "key refused"); n != 1 {
		t.Errorf("%d refused keys logged in one second, want 1:\n%s", n, log.String())
	}
	clk.set(clk.Now().Add(time.Second))
	if _, err := devsim.Dial(addr, signer(t), s.HostKey.PublicKey()); err == nil {
		t.Fatal("an unpaired key was let in")
	}
	if !strings.Contains(log.String(), "held_back=4") {
		t.Errorf("the next second's line doesn't say 4 were held back:\n%s", log.String())
	}
}

// TestServeQuietensConnectionsWithoutASession: a connection closed for no
// session in time is logged at most once a second too.
func TestServeQuietensConnectionsWithoutASession(t *testing.T) {
	var log syncBuffer
	s := testServer(t, &log)
	s.Clock = &frozenClock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), scale: 1000}
	addr, _ := serve(t, s, nil)
	var stalled []net.Conn // each closed after HandshakeTimeout/1000
	for i := 0; i < MaxHandshakesPerAddr; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		stalled = append(stalled, c)
	}
	for _, c := range stalled {
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		io.Copy(io.Discard, c) // until the server closes it
	}
	if n := strings.Count(log.String(), "no session within 15 s"); n != 1 {
		t.Errorf("%d connections without a session logged in one second, want 1:\n%s", n, log.String())
	}
}

// frozenClock is a clock whose time moves only when the test sets it; its
// waits are the real ones, scale times shorter.
type frozenClock struct {
	mu    sync.Mutex
	now   time.Time
	scale time.Duration
}

func (f *frozenClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *frozenClock) set(t time.Time) {
	f.mu.Lock()
	f.now = t
	f.mu.Unlock()
}

func (f *frozenClock) After(d time.Duration) <-chan time.Time { return time.After(d / f.scale) }

// syncBuffer is a log's buffer that goroutines may write while a test reads.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
