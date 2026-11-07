package setup

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// IdleAfter is how long the page runs with no tab open: from its last tab's
// close, or from its start if no tab ever opens (requirement 2.5).
const IdleAfter = 5 * time.Minute

// streams counts the page's open event streams, one for each tab. Every
// open and close moves gen on and closes changed, so the countdown sees it.
type streams struct {
	mu       sync.Mutex
	open     int
	gen      uint64
	changed  chan struct{}
	stopping chan struct{} // closed when Serve stops: every stream ends
	stopOnce sync.Once
}

func (s *streams) init() {
	s.changed = make(chan struct{})
	s.stopping = make(chan struct{})
}

// moved notes an open or a close; the caller holds mu.
func (s *streams) moved() {
	s.gen++
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *streams) stop() { s.stopOnce.Do(func() { close(s.stopping) }) }

// openStreams is how many tabs have the page's event stream open.
func (h *Handler) openStreams() int {
	h.tabs.mu.Lock()
	defer h.tabs.mu.Unlock()
	return h.tabs.open
}

// openStream counts a tab's stream open, and returns what counts it closed.
func (h *Handler) openStream() (closed func()) {
	h.tabs.mu.Lock()
	h.tabs.open++
	h.tabs.moved()
	h.tabs.mu.Unlock()
	return func() {
		h.tabs.mu.Lock()
		h.tabs.open--
		h.tabs.moved()
		h.tabs.mu.Unlock()
	}
}

// events is the page's event stream, GET /events: a tab holds it open for
// as long as it's open (design §The setup page), and gets every event from
// then on (events.go). It's subscribed and counted open before its headers
// go, so a tab that has them is counted, and misses nothing.
func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	events, unsubscribe := h.hub.subscribe()
	defer unsubscribe()
	defer h.openStream()()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, ": herdr-pager setup\n\n")
	rc := http.NewResponseController(w)
	rc.Flush()
	for {
		select {
		case <-r.Context().Done(): // the tab closed
			return
		case <-h.tabs.stopping:
			return
		case e := <-events:
			if writeEvent(w, e) != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

// untilIdle returns when no stream has been open for IdleAfter on h's
// clock, or when ctx ends. A countdown counts only if nothing opened or
// closed while it ran.
func (h *Handler) untilIdle(ctx context.Context) {
	for {
		h.tabs.mu.Lock()
		open, gen, changed := h.tabs.open, h.tabs.gen, h.tabs.changed
		h.tabs.mu.Unlock()
		var countdown <-chan time.Time
		if open == 0 {
			countdown = h.clock.After(IdleAfter)
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		case <-countdown:
			h.tabs.mu.Lock()
			idle := h.tabs.open == 0 && h.tabs.gen == gen
			h.tabs.mu.Unlock()
			if idle {
				return
			}
		}
	}
}

// Serve serves the page on ln until ctx ends (Ctrl-C), or until no tab has
// been open for IdleAfter. Either way it returns nil, having ended every
// stream and closed ln; it returns an error only if ln fails.
func (h *Handler) Serve(ctx context.Context, ln net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go h.tailBridgeLogs(ctx, h.newTails()) // from each file's end, before any request
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	idle := make(chan struct{})
	go func() {
		h.untilIdle(ctx)
		close(idle)
	}()

	var err error
	select {
	case err = <-served:
	case <-idle:
	}
	h.tabs.stop()
	h.stopLife() // a pairing waiting for its hello or its confirm ends
	shut, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if srv.Shutdown(shut) != nil {
		srv.Close()
	}
	h.pairingEnded()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
