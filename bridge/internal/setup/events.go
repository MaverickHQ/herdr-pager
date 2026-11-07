package setup

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"sync"
)

// The page's live output (design §The setup page): one event stream, GET
// /events, with three channels. Each event is one line of text, sent as
// `event: <channel>` and `data: {"text": …}`; the page shows it only as
// text (requirement 2.5).
const (
	chanStep   = "step"   // each step's progress: provision's output, the page's own lines
	chanBridge = "bridge" // the bridge's log (task 3.7)
	chanPager  = "pager"  // the pager's own serial lines (task 3.6)
)

// An event is one line on one channel.
type event struct {
	channel string
	text    string
}

// eventBacklog is how many events a tab may fall behind by; past it, it
// misses its oldest lines rather than holding up a pairing, and so always
// gets the newest.
const eventBacklog = 256

// A hub sends each event to every open stream.
type hub struct {
	mu   sync.Mutex
	subs map[chan event]struct{}
}

// subscribe is a stream's events, until unsubscribe.
func (h *hub) subscribe() (events <-chan event, unsubscribe func()) {
	c := make(chan event, eventBacklog)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[chan event]struct{}{}
	}
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	return c, func() {
		h.mu.Lock()
		delete(h.subs, c)
		h.mu.Unlock()
	}
}

// publish sends e to every stream without waiting for any. A stream that's
// eventBacklog behind loses its oldest event to make room.
func (h *hub) publish(e event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs {
		for sent := false; !sent; {
			select {
			case c <- e:
				sent = true
			default:
				select {
				case <-c:
				default:
				}
			}
		}
	}
}

// writeEvent writes e in the event stream's format.
func writeEvent(w io.Writer, e event) error {
	data, err := json.Marshal(struct {
		Text string `json:"text"`
	}{e.text})
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "event: "+e.channel+"\ndata: "+string(data)+"\n\n")
	return err
}

// step shows text, one line, on the step channel, and in the page's log.
func (h *Handler) step(text string) {
	h.hub.publish(event{chanStep, text})
	if h.log != nil {
		h.logMu.Lock()
		io.WriteString(h.log, text+"\n")
		h.logMu.Unlock()
	}
}

// pagerLine shows one of the pager's own serial lines (provision.Options.Log)
// on the pager channel, as plain text.
func (h *Handler) pagerLine(line string) { h.hub.publish(event{chanPager, plainText(line)}) }

// terminalCodes are ANSI escape sequences: ESP-IDF colours its log lines.
var terminalCodes = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")

// plainText is line without terminal codes or control characters, but its
// tabs: what a device sends is shown as it reads in a terminal.
func plainText(line string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || (r >= 0x20 && r != 0x7f && !(r >= 0x80 && r < 0xa0)) {
			return r
		}
		return -1
	}, terminalCodes.ReplaceAllString(line, ""))
}

// stepWriter is an io.Writer for provision.Options.Out: each line written
// to it is a step.
func (h *Handler) stepWriter() io.Writer { return &lineWriter{emit: h.step} }

// A lineWriter calls emit with each whole line written to it.
type lineWriter struct {
	mu   sync.Mutex
	buf  []byte
	emit func(string)
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		l.emit(string(bytes.TrimSuffix(l.buf[:i], []byte("\r"))))
		l.buf = l.buf[i+1:]
	}
}
