// Package sshserver is the bridge's SSH server (parent 3, parent design
// §Bridge components, sshserver): full public keys from devices.toml only, no
// password or keyboard-interactive auth, at most 3 tries; 15 s from accept to
// a session; session channels with exec or shell only; global requests
// answered false; a second session for a key replaces the first, on a new
// connection or the same one.
package sshserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"slices"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/clock"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
)

// A Server accepts paired devices.
type Server struct {
	HostKey ssh.Signer
	// Devices returns the current devices.toml.
	Devices func() []devices.Device
	// Handle runs one session on its channel.
	Handle func(ctx context.Context, d devices.Device, ch io.ReadWriter)
	Clock  clock.Clock
	Log    *slog.Logger

	mu   sync.Mutex
	live map[*ssh.ServerConn]devices.Device // each open connection's device

	shaking  handshakes // the connections in their handshake
	refused  quietLog   // keys refused
	crowded  quietLog   // connections closed at their accept: too many in their handshake
	sessless quietLog   // connections closed with no session in time
	failing  quietLog   // Accept's temporary failures
}

// CheckInterval is how often the server checks that each open connection's
// device is still paired, so `devices revoke` takes effect within 2 s
// (parent 12.2, phase 2 R5.6).
const CheckInterval = 500 * time.Millisecond

// HandshakeTimeout is how long a connection has, on the server's clock, from
// its accept to its session's exec (or shell) request; then it's closed
// (parent 3.9; the owner's decision, 2026-10-27, open-items O-47). A pager
// takes under 2 s, its own SSH timeout is 10 s, and a slow network's
// power-saving adds about 1 s a message.
const HandshakeTimeout = 15 * time.Second

// MaxHandshakes is how many connections may be in their SSH handshake at
// once, from any addresses, and MaxHandshakesPerAddr how many from one source
// address: one more is closed at its accept, before a byte is read or sent,
// unless it can take the slot of the busiest address's oldest connection
// (handshakes.acquire). Unauthenticated clients can't hold every file
// descriptor (the security pass, 2026-11-01). A pager has one connection, and
// dials again only after it has ended.
const (
	MaxHandshakes        = 32
	MaxHandshakesPerAddr = 4
)

// handshakes holds the connections in their handshake, by source address,
// each address's oldest first.
type handshakes struct {
	mu     sync.Mutex
	total  int
	byAddr map[string][]*slot
	next   uint64 // the next slot's number: older slots have lower ones
}

// A slot is one connection's place in its handshake.
type slot struct {
	addr string
	conn io.Closer
	n    uint64
	gone bool // released, or evicted
}

// acquire takes a slot for conn, from addr, or says there's none: addr holds
// MaxHandshakesPerAddr already, or every slot is taken and none can be freed.
// When every slot is taken, the busiest address (the oldest slot's, of the
// busiest) loses its oldest connection, closed, if it holds more than one
// slot more than addr does: so a few addresses can't hold every slot and keep
// out a pager, which holds one, and that one is never the one closed (the
// security pass's review, 2026-11-01).
func (h *handshakes) acquire(addr string, conn io.Closer) (*slot, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	mine := len(h.byAddr[addr])
	if mine >= MaxHandshakesPerAddr {
		return nil, false
	}
	if h.total >= MaxHandshakes {
		var busiest []*slot
		for _, ss := range h.byAddr {
			if len(ss) > len(busiest) || len(ss) == len(busiest) && ss[0].n < busiest[0].n {
				busiest = ss
			}
		}
		if len(busiest) <= mine+1 {
			return nil, false
		}
		old := busiest[0]
		h.drop(old)
		old.conn.Close() // its handshake fails, and its release does nothing
	}
	if h.byAddr == nil {
		h.byAddr = map[string][]*slot{}
	}
	s := &slot{addr: addr, conn: conn, n: h.next}
	h.next++
	h.byAddr[addr] = append(h.byAddr[addr], s)
	h.total++
	return s, true
}

// release frees s, whose connection has left its handshake. A slot already
// freed, by an eviction or a release, is freed once.
func (h *handshakes) release(s *slot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.drop(s)
}

// drop frees s, once; h.mu is held.
func (h *handshakes) drop(s *slot) {
	if s.gone {
		return
	}
	s.gone = true
	h.total--
	ss := slices.DeleteFunc(h.byAddr[s.addr], func(o *slot) bool { return o == s })
	if len(ss) == 0 {
		delete(h.byAddr, s.addr)
	} else {
		h.byAddr[s.addr] = ss
	}
}

// A quietLog logs its line at most once a second, adding how many it held
// back since the last (held_back), as a session's rate limit does (parent
// 13.6): for the lines an unauthenticated client can cause, so it can't
// flood the log (the security pass, 2026-11-01).
type quietLog struct {
	mu   sync.Mutex
	last time.Time
	held int
}

func (q *quietLog) log(l *slog.Logger, level slog.Level, now time.Time, msg string, args ...any) {
	q.mu.Lock()
	if !q.last.IsZero() && now.Sub(q.last) < time.Second {
		q.held++
		q.mu.Unlock()
		return
	}
	held := q.held
	q.last, q.held = now, 0
	q.mu.Unlock()
	if held > 0 {
		args = append(args, "held_back", held)
	}
	l.Log(context.Background(), level, msg, args...)
}

// acceptBackoff bounds the wait before Accept is tried again after a
// temporary failure: from 5 ms, doubling to 1 s, as net/http's server does.
const (
	acceptBackoffMin = 5 * time.Millisecond
	acceptBackoffMax = time.Second
)

// temporary says whether Accept's err will pass: the process is out of file
// descriptors or memory, or the connection went before it was accepted.
func temporary(err error) bool {
	for _, e := range []syscall.Errno{syscall.EMFILE, syscall.ENFILE, syscall.ENOBUFS, syscall.ENOMEM,
		syscall.ECONNABORTED, syscall.ECONNRESET, syscall.EINTR, syscall.EAGAIN} {
		if errors.Is(err, e) {
			return true
		}
	}
	var t interface{ Temporary() bool }
	return errors.As(err, &t) && t.Temporary()
}

// host is addr's IP, or all of it if it has no port.
func host(addr net.Addr) string {
	h, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return h
}

// keyExt is the Permissions extension that carries the authenticated key.
const keyExt = "pager-bridge-key"

// Serve accepts connections on l until ctx ends, then closes l and every
// connection, and returns once they have all ended. A temporary failure of
// Accept (EMFILE: out of file descriptors) is tried again after a backoff;
// any other ends Serve with it. A connection over MaxHandshakes or
// MaxHandshakesPerAddr is closed at its accept.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	// Public keys only: no password or keyboard-interactive callback, so
	// neither method is offered.
	cfg := &ssh.ServerConfig{PublicKeyCallback: s.authorize, MaxAuthTries: 3}
	cfg.AddHostKey(s.HostKey)

	s.mu.Lock()
	s.live = map[*ssh.ServerConn]devices.Device{}
	s.mu.Unlock()

	var wg sync.WaitGroup
	defer wg.Wait()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { <-ctx.Done(); l.Close() }()
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.checkDevices(ctx)
	}()
	var backoff time.Duration
	for {
		nc, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !temporary(err) {
				return err
			}
			backoff = min(max(2*backoff, acceptBackoffMin), acceptBackoffMax)
			s.failing.log(s.Log, slog.LevelWarn, s.Clock.Now(), "sshserver: accept failed; trying again", "err", err, "in", backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-s.Clock.After(backoff):
			}
			continue
		}
		backoff = 0
		addr := host(nc.RemoteAddr())
		sl, ok := s.shaking.acquire(addr, nc)
		if !ok {
			nc.Close()
			s.crowded.log(s.Log, slog.LevelWarn, s.Clock.Now(), "sshserver: too many connections in their handshake; connection closed", "addr", addr)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.serveConn(ctx, cfg, nc, sl)
		}()
	}
}

// authorize accepts an ed25519 key (design D4) that is byte for byte a
// paired device's (parent 3.2, 13.8). A refusal logs the key's full
// fingerprint and the source address, and nothing else the client sent
// (parent 3.7).
func (s *Server) authorize(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	if _, ok := devices.Match(s.Devices(), key); ok && key.Type() == ssh.KeyAlgoED25519 {
		return &ssh.Permissions{Extensions: map[string]string{keyExt: string(key.Marshal())}}, nil
	}
	s.refused.log(s.Log, slog.LevelWarn, s.Clock.Now(), "sshserver: key refused", "fingerprint", ssh.FingerprintSHA256(key), "addr", meta.RemoteAddr().String())
	return nil, errors.New("sshserver: not a paired device's key")
}

// serveConn serves nc, which holds the handshake slot sl until its handshake
// ends.
func (s *Server) serveConn(ctx context.Context, cfg *ssh.ServerConfig, nc net.Conn, sl *slot) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	begun := make(chan struct{})
	go s.deadline(ctx, nc, begun)
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	s.shaking.release(sl)
	if err != nil {
		nc.Close()
		return
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()
	go ssh.DiscardRequests(reqs)

	key, err := ssh.ParsePublicKey([]byte(conn.Permissions.Extensions[keyExt]))
	if err != nil {
		return
	}
	d, ok := devices.Match(s.Devices(), key)
	if !ok {
		return
	}
	s.mu.Lock()
	for older, od := range s.live { // a device has one session: the newest (parent 3.6)
		if _, same := devices.Match([]devices.Device{od}, d.PublicKey); same {
			s.Log.Info("sshserver: the device connected again; its older session closed", "device", d.Name)
			older.Close()
		}
	}
	s.live[conn] = d
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.live, conn)
		s.mu.Unlock()
	}()
	var wg sync.WaitGroup
	ss := &sessions{end: cancel, begun: begun}
	for nch := range chans {
		if nch.ChannelType() != "session" { // no forwarding, no x11 (parent 3.8)
			nch.Reject(ssh.UnknownChannelType, "only session channels")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.serveChannel(ctx, ss, d, ch, creqs)
		}()
	}
	cancel() // the connection is gone: its sessions end, and are waited for
	wg.Wait()
}

// sessions are one connection's: the newest runs, and starting another
// closes the one before it, as a device connecting again does (parent 3.6;
// the owner's decision, 2026-10-27, open-items O-34). The connection ends
// with its newest session.
type sessions struct {
	end   context.CancelFunc // ends the connection
	begun chan struct{}      // closed at the first exec: the connection's deadline is met
	once  sync.Once

	mu     sync.Mutex
	n      int                // sessions started on the connection
	cancel context.CancelFunc // the newest one's
}

// begin says a session's exec has come, before it's answered, so the
// connection's deadline is met however soon the client moves on.
func (ss *sessions) begin() { ss.once.Do(func() { close(ss.begun) }) }

// start begins a session under ctx, closing the newest before it (replaced
// says so). done ends the session, and the connection too if it's still the
// newest.
func (ss *sessions) start(ctx context.Context) (sctx context.Context, done func(), replaced bool) {
	sctx, cancel := context.WithCancel(ctx)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.cancel != nil {
		ss.cancel()
		replaced = true
	}
	ss.n++
	mine := ss.n
	ss.cancel = cancel
	return sctx, func() {
		cancel()
		ss.mu.Lock()
		newest := ss.n == mine
		ss.mu.Unlock()
		if newest {
			ss.end()
		}
	}, replaced
}

// deadline closes nc unless its first session begins within HandshakeTimeout
// of its accept, on the server's clock (parent 3.9). It closes it too if ctx
// ends first, so a handshake that stalls can't keep Serve from returning.
func (s *Server) deadline(ctx context.Context, nc net.Conn, begun <-chan struct{}) {
	select {
	case <-begun:
	case <-ctx.Done():
		nc.Close()
	case <-s.Clock.After(HandshakeTimeout):
		s.sessless.log(s.Log, slog.LevelInfo, s.Clock.Now(), "sshserver: no session within 15 s; connection closed", "addr", nc.RemoteAddr().String())
		nc.Close()
	}
}

// checkDevices closes, every CheckInterval until ctx ends, each connection
// whose device's key is no longer paired.
func (s *Server) checkDevices(ctx context.Context) {
	next := s.Clock.Now()
	for {
		next = next.Add(CheckInterval)
		if now := s.Clock.Now(); next.Before(now) {
			next = now
		}
		select {
		case <-ctx.Done():
			return
		case <-s.Clock.After(next.Sub(s.Clock.Now())):
		}
		devs := s.Devices()
		s.mu.Lock()
		for conn, d := range s.live {
			if _, ok := devices.Match(devs, d.PublicKey); !ok {
				s.Log.Info("sshserver: device no longer paired; session closed", "device", d.Name)
				conn.Close()
			}
		}
		s.mu.Unlock()
	}
}

// serveChannel runs the session on the channel's exec or shell request, and
// returns when it ends; any request after that one is refused.
func (s *Server) serveChannel(ctx context.Context, ss *sessions, d devices.Device, ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		ok := req.Type == "exec" || req.Type == "shell"
		if ok {
			ss.begin()
		}
		req.Reply(ok, nil)
		if !ok {
			continue
		}
		go ssh.DiscardRequests(reqs)
		sctx, done, replaced := ss.start(ctx)
		if replaced {
			s.Log.Info("sshserver: the device opened another session; its older session closed", "device", d.Name)
		}
		s.Handle(sctx, d, ch)
		done()
		return
	}
}
