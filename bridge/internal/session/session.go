// Package session runs one device's session (parent design §Bridge
// components, session): hello within 5 s, then one message at a time, with
// the verb allowlist, the 512-byte line limit, the rate limit and the
// five-strike close. This file is the session itself (lane e2e); verbs.go
// holds the verbs that act on an agent (lane verdicts).
package session

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/clock"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/keymap"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/risk"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/version"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/watch"
)

// Deps are what every session shares.
type Deps struct {
	Herdr   *herdr.Client
	Watch   *watch.Watcher
	Keymaps keymap.Set
	Rules   risk.Rules
	Locks   *Locks
	Clock   clock.Clock
	Log     *slog.Logger
	Host    string // the bridge's host name for its hello: 1 to 64 bytes of display text
	// IdleTimeout ends a session that sends nothing for this long
	// (config.toml's idle_timeout; 180 s if zero). The device pings every
	// 60 s.
	IdleTimeout time.Duration
}

// HelloTimeout ends a session whose hello doesn't come within it.
const HelloTimeout = 5 * time.Second

// DefaultIdleTimeout is Deps.IdleTimeout's default.
const DefaultIdleTimeout = 180 * time.Second

// A Session is one device's session.
type Session struct {
	deps Deps
	rows int // the most lines of a card the device draws, from its hello
}

// New returns a session using d.
func New(d Deps) *Session { return &Session{deps: d} }

// Run speaks the pager protocol on in and out until the session ends or ctx
// does: the device's hello, the bridge's, then the latest snapshot and each
// one after it.
func (s *Session) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := read(ctx, in)

	var first input
	select {
	case <-ctx.Done():
		return nil
	case <-s.deps.Clock.After(HelloTimeout):
		s.deps.Log.Warn("session: no hello within 5 s; session closed")
		return errors.New("session: no hello")
	case first = <-lines:
	}
	if first.err != nil {
		return s.ended(first.err)
	}
	m, err := protocol.ParseDevice(first.line)
	hello, ok := m.(*protocol.DeviceHello)
	if err != nil || !ok {
		s.deps.Log.Warn("session: the first message isn't a hello; session closed", "t", typeOf(first.line))
		return errors.New("session: the first line isn't a hello")
	}
	s.rows = hello.Lines // a pager from before 2026-11-02 draws 3 (O-90's second review)
	// The bridge's version, as --version prints it (phase 5 task 1.3).
	if err := s.send(out, &protocol.BridgeHello{Bridge: version.String(), Host: s.deps.Host, Now: s.now()}); err != nil {
		return err
	}

	snaps, stop := s.deps.Watch.Subscribe()
	defer stop()
	invalid := 0
	limit := bucket{tokens: RateLimit, last: s.deps.Clock.Now()}
	var warned time.Time
	idleTimeout := cmp.Or(s.deps.IdleTimeout, DefaultIdleTimeout)
	idle := s.deps.Clock.After(idleTimeout)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-idle:
			s.deps.Log.Info("session: idle; session closed", "after", idleTimeout)
			return nil
		case snap := <-snaps:
			snap.Now = s.now()
			if err := s.send(out, &snap); err != nil {
				return err
			}
		case in := <-lines:
			if in.err != nil {
				return s.ended(in.err)
			}
			idle = s.deps.Clock.After(idleTimeout)
			if now := s.deps.Clock.Now(); !limit.take(now) {
				if err := s.drop(out, in.line, now, &warned); err != nil {
					return err
				}
				continue
			}
			m, err := protocol.ParseDevice(in.line)
			if err != nil {
				// Logged by its type and what's wrong, never its content;
				// no reply (parent 13.1, as amended in phase 2's grilling).
				invalid++
				s.deps.Log.Warn("session: invalid message", "t", typeOf(in.line), "err", err, "count", invalid)
				if invalid >= MaxInvalid {
					s.deps.Log.Warn("session: too many invalid messages; session closed")
					return errors.New("session: too many invalid messages")
				}
				continue
			}
			if reply := s.handle(ctx, m); reply != nil {
				if err := s.send(out, reply); err != nil {
					return err
				}
			}
		}
	}
}

// MaxInvalid invalid messages in one session close it (parent 13.1).
const MaxInvalid = 5

// RateLimit is how many messages a second a session takes (parent 13.6): a
// token bucket that holds this many and refills at this rate.
const RateLimit = 10

type bucket struct {
	tokens float64
	last   time.Time
}

func (b *bucket) take(now time.Time) bool {
	b.tokens = min(RateLimit, b.tokens+now.Sub(b.last).Seconds()*RateLimit)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// drop answers a line over the rate limit, which is never acted on (parent
// 13.6): a request is told why with `rate_limited`, and a line that isn't
// one gets nothing (nor is it counted as invalid). The log says so at most
// once a second, at warned, so a flood can't flood the log.
func (s *Session) drop(out io.Writer, line []byte, now time.Time, warned *time.Time) error {
	if now.Sub(*warned) >= time.Second {
		s.deps.Log.Warn("session: over the rate limit; messages dropped", "limit", RateLimit)
		*warned = now
	}
	m, err := protocol.ParseDevice(line)
	if err != nil {
		return nil
	}
	id, ok := idOf(m)
	if !ok {
		return nil
	}
	return s.send(out, &protocol.Result{ID: id, Err: "rate_limited"})
}

// idOf is the id of a device's request.
func idOf(m protocol.Message) (uint32, bool) {
	switch m := m.(type) {
	case *protocol.Ping:
		return m.ID, true
	case *protocol.List:
		return m.ID, true
	case *protocol.Show:
		return m.ID, true
	case *protocol.Approve:
		return m.ID, true
	case *protocol.Deny:
		return m.ID, true
	case *protocol.Interrupt:
		return m.ID, true
	case *protocol.Jump:
		return m.ID, true
	}
	return 0, false
}

// ended is how the session ends when reading stops: a line over the limit
// is logged.
func (s *Session) ended(err error) error {
	if errors.Is(err, errTooLong) {
		s.deps.Log.Warn("session: a line over 512 bytes; session closed")
		return err
	}
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// typeOf is a line's `t`, for logs: only a short lowercase word, so nothing
// else the device wrote can reach a log; "?" otherwise.
func typeOf(line []byte) string {
	var head struct {
		T string `json:"t"`
	}
	if json.Unmarshal(line, &head) != nil || !typeRE.MatchString(head.T) {
		return "?"
	}
	return head.T
}

var typeRE = regexp.MustCompile(`^[a-z_]{1,16}$`)

// handle answers one message from the device, or returns nil for none.
func (s *Session) handle(ctx context.Context, m protocol.Message) protocol.Message {
	switch m := m.(type) {
	case *protocol.Ping:
		return &protocol.Pong{ID: m.ID, Now: s.now()}
	case *protocol.List:
		snap, ok := s.deps.Watch.Latest()
		if !ok {
			return nil // the first poll's snapshot comes by itself
		}
		snap.Now = s.now()
		return &snap
	case *protocol.Show:
		return s.show(ctx, m)
	case *protocol.Approve:
		return s.approve(ctx, m)
	case *protocol.Deny:
		return s.deny(ctx, m)
	case *protocol.Interrupt:
		return s.interrupt(ctx, m)
	case *protocol.Jump:
		return s.jump(ctx, m)
	}
	return nil
}

func (s *Session) now() int64 { return s.deps.Clock.Now().Unix() }

// send writes m as one line. A message the contract would reject is logged
// by its type and why (never its content) and not sent.
func (s *Session) send(out io.Writer, m protocol.Message) error {
	line, err := protocol.Encode(m)
	if err != nil {
		s.deps.Log.Error("session: a message breaks the protocol; not sent", "t", m.Type(), "err", err)
		return nil
	}
	_, err = out.Write(append(line, '\n'))
	return err
}

// errTooLong is a device line over protocol.MaxDeviceLine bytes, not
// counting its '\n' (parent 13.5).
var errTooLong = errors.New("session: a line over 512 bytes")

// An input is one line from the device, without its '\n'; or, last, why
// reading stopped.
type input struct {
	line []byte
	err  error
}

// read delivers each line from in until in ends, a line is too long, or ctx
// ends.
func read(ctx context.Context, in io.Reader) <-chan input {
	inputs := make(chan input)
	go func() {
		r := bufio.NewReaderSize(in, protocol.MaxDeviceLine+1) // the line and its '\n'
		for {
			var next input
			line, err := r.ReadSlice('\n')
			switch {
			case err == nil:
				next.line = bytes.Clone(line[:len(line)-1])
			case errors.Is(err, bufio.ErrBufferFull):
				next.err = errTooLong
			default:
				next.err = err
			}
			select {
			case inputs <- next:
			case <-ctx.Done():
				return
			}
			if next.err != nil {
				return
			}
		}
	}()
	return inputs
}

// Locks is the per-pane lock every session shares, so that two verbs on one
// agent never both send keys, and the seq each pane's keys were last sent
// at, so that one state of an agent is answered once: verdicts are never
// queued or replayed (parent design §Threat model, T6).
type Locks struct {
	mu       sync.Mutex
	held     map[string]bool   // the panes a verb holds now
	answered map[string]uint64 // by pane, the seq its keys were last sent at
}

// TryLock locks pane unless another verb holds it, and returns its unlock; ok
// is false, and nothing is locked, if one does. It never waits.
func (l *Locks) TryLock(pane string) (unlock func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[pane] {
		return nil, false
	}
	if l.held == nil {
		l.held = map[string]bool{}
	}
	l.held[pane] = true
	return func() {
		l.mu.Lock()
		delete(l.held, pane)
		l.mu.Unlock()
	}, true
}

// answeredAt says whether pane's keys were last sent at seq. herdr's
// state_change_seq is one counter for all its agents, so a pane never comes
// back to a seq: once answered, that state of the agent is done with.
func (l *Locks) answeredAt(pane string, seq uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	last, ok := l.answered[pane]
	return ok && last == seq
}

// answer records that pane's keys were sent at seq.
func (l *Locks) answer(pane string, seq uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.answered == nil {
		l.answered = map[string]uint64{}
	}
	l.answered[pane] = seq
}
