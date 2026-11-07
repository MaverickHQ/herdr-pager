package setup

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A served is the setup page served by Serve on its own listener, with a
// fake clock, as `pager-bridge setup` runs it.
type served struct {
	t      *testing.T
	h      *Handler
	clock  *fakeClock
	addr   string // 127.0.0.1:<port>
	cancel context.CancelFunc
	done   chan error // Serve's return
}

func newServed(t *testing.T) *served {
	t.Helper()
	clk := newFakeClock()
	h, err := NewHandler(Options{Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &served{t: t, h: h, clock: clk, addr: ln.Addr().String(), cancel: cancel, done: make(chan error, 1)}
	go func() { s.done <- h.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			t.Error("Serve didn't return after its context ended")
		}
	})
	return s
}

// get sends GET path as the page's tab does, with the cookie.
func (s *served) get(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, "http://"+s.addr+path, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(s.addr)
	req.AddCookie(&http.Cookie{Name: cookieName(port), Value: s.h.Token()})
	c := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	return c.Do(req)
}

// openTab opens the page's event stream, as a tab does, and returns its
// response once the headers have come: the server counts it by then.
func (s *served) openTab() *http.Response {
	s.t.Helper()
	resp, err := s.get("/events")
	if err != nil {
		s.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		s.t.Fatalf("GET /events: status %d, Content-Type %q; want 200, text/event-stream", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return resp
}

// closeTab closes a tab's event stream, and waits until the server has
// counted it closed and open streams are left.
func (s *served) closeTab(tab *http.Response, open int) {
	s.t.Helper()
	tab.Body.Close()
	for deadline := time.Now().Add(5 * time.Second); s.h.openStreams() != open; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			s.t.Fatalf("%d streams open after a tab closed; want %d", s.h.openStreams(), open)
		}
	}
}

// countdown waits for the 5-minute timer that starts now.
func (s *served) countdown() {
	s.t.Helper()
	if !s.clock.waitTimer(s.clock.Now().Add(5 * time.Minute)) {
		s.t.Fatalf("no 5-minute timer from %v", s.clock.Now())
	}
}

// running checks the server still serves and Serve hasn't returned.
func (s *served) running(when string) {
	s.t.Helper()
	select {
	case err := <-s.done:
		s.t.Fatalf("%s: Serve returned (%v); want it running", when, err)
	case <-time.After(20 * time.Millisecond):
	}
	resp, err := s.get("/")
	if err != nil {
		s.t.Fatalf("%s: the page doesn't answer: %v", when, err)
	}
	resp.Body.Close()
}

// stopped checks Serve returned nil within 5 s (real time), and that
// nothing listens on the page's address.
func (s *served) stopped(when string) {
	s.t.Helper()
	select {
	case err := <-s.done:
		if err != nil {
			s.t.Errorf("%s: Serve returned %v; want nil", when, err)
		}
	case <-time.After(5 * time.Second):
		s.t.Fatalf("%s: Serve is still running", when)
	}
	if conn, err := net.DialTimeout("tcp", s.addr, time.Second); err == nil {
		conn.Close()
		s.t.Errorf("%s: something still listens on %s", when, s.addr)
	}
	s.done <- nil // for the cleanup
}

// TestSetupStopsAfterTheLastTab is the page's lifetime (requirement 2.5,
// design §The setup page): each tab holds an event stream open; the server
// stops 5 minutes after the last one closes, and never while one is open.
func TestSetupStopsAfterTheLastTab(t *testing.T) {
	t.Run("not while a tab is open; 5 minutes after it closes", func(t *testing.T) {
		s := newServed(t)
		tab := s.openTab()
		s.clock.Advance(time.Hour)
		s.running("an hour with a tab open")

		s.closeTab(tab, 0)
		s.countdown()
		s.clock.Advance(5*time.Minute - time.Second)
		s.running("4:59 after the tab closed")
		s.clock.Advance(time.Second)
		s.stopped("5 minutes after the tab closed")
	})

	t.Run("a tab opened in the 5 minutes keeps it", func(t *testing.T) {
		s := newServed(t)
		tab := s.openTab()
		s.clock.Advance(time.Minute) // so the countdown isn't the start's, due at the same moment
		s.closeTab(tab, 0)
		s.countdown()
		s.clock.Advance(3 * time.Minute)
		tab = s.openTab() // a reload, or a new tab
		s.clock.Advance(time.Hour)
		s.running("an hour after a tab opened in the countdown")

		s.closeTab(tab, 0)
		s.countdown()
		s.clock.Advance(5 * time.Minute)
		s.stopped("5 minutes after the second tab closed")
	})

	t.Run("the last of two tabs", func(t *testing.T) {
		s := newServed(t)
		one, two := s.openTab(), s.openTab()
		s.closeTab(one, 1)
		s.clock.Advance(time.Hour)
		s.running("an hour after the first of two tabs closed")

		s.closeTab(two, 0)
		s.countdown()
		s.clock.Advance(5 * time.Minute)
		s.stopped("5 minutes after the last tab closed")
	})

	t.Run("a request that isn't a tab keeps nothing open", func(t *testing.T) {
		s := newServed(t)
		tab := s.openTab()
		s.clock.Advance(time.Minute)
		s.closeTab(tab, 0)
		s.countdown()
		s.clock.Advance(4 * time.Minute)
		s.running("4 minutes after the tab closed") // a GET of the page itself
		s.clock.Advance(time.Minute)
		s.stopped("5 minutes after the tab closed, a GET between")
	})

	t.Run("no tab ever: 5 minutes after it started", func(t *testing.T) {
		s := newServed(t)
		s.countdown()
		s.clock.Advance(5*time.Minute - time.Second)
		s.running("4:59 after it started")
		s.clock.Advance(time.Second)
		s.stopped("5 minutes with no tab")
	})

	t.Run("stopped (Ctrl-C) with a tab open", func(t *testing.T) {
		s := newServed(t)
		tab := s.openTab()
		read := make(chan error, 1)
		go func() {
			_, err := io.Copy(io.Discard, tab.Body)
			read <- err
		}()
		s.cancel()
		s.stopped("its context ended")
		select {
		case <-read: // the stream ended, as the tab sees it
		case <-time.After(5 * time.Second):
			t.Error("the tab's stream is still open after the server stopped")
		}
	})
}
