package setup

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/provision"
)

// After pairing (the owner's recheck of checkpoint 7.2, (A), 2026-11-07): a
// wrong Wi-Fi password pairs fine, because the pager tries it only when it
// joins, once it has restarted. POST /api/pair/join reads the pager's USB
// log, on the port the page's last pairing used, until it says how its join
// went, and answers in the page's words, so the page can offer Change the
// Wi-Fi networks rather than wait for a pager that can't call. A pager that
// can't see a network can't tell why: a 5 GHz-only network, one out of
// range and one named differently look the same to it, so they're said
// together (the owner's session, 2026-11-07).

// joinWait is how long POST /api/pair/join reads the pager's log for how its
// join went.
const joinWait = 90 * time.Second

// reopenWait is the wait between tries to open the pager's port while it
// restarts: its USB port may go away and come back.
const reopenWait = 500 * time.Millisecond

// The results of a join, as POST /api/pair/join answers them.
const (
	joinJoined   = "joined"
	joinRefused  = "refused"   // a saved network refused its password
	joinNotFound = "not_found" // no saved network it can see
	joinFailed   = "failed"    // it found one, and couldn't join it
	joinUnknown  = "unknown"   // its log hasn't said, in joinWait
)

// The pager's log lines that say how its join went: firmware/main/platform/
// pump.c's, and the kit's lcd349_wifi's. A reason is the kit's
// lcd349_wifi_reason_t (2 is NOT_FOUND). A refusal it tries again at once
// may be brief (O-45), so only the one after those, with its reason, counts,
// as it does on the pager's own screen (O-49).
const (
	saysTrying  = "lcd349_wifi: connecting to saved network " // then its number, from 1
	saysJoined  = "lcd349_wifi: connected,"
	saysRefused = "wifi: join refused (reason "
	saysFailed  = "wifi: join failed (reason "
	notFoundWhy = 2
)

// joinLine is what one line of the pager's log says about its join: a
// result, or the saved network it's trying (from 1), or neither.
func joinLine(line string) (result string, network int) {
	switch {
	case strings.Contains(line, saysTrying):
		return "", leadingInt(line[strings.Index(line, saysTrying)+len(saysTrying):])
	case strings.Contains(line, saysJoined):
		return joinJoined, 0
	case strings.Contains(line, saysRefused):
		return joinRefused, 0
	case strings.Contains(line, saysFailed):
		if leadingInt(line[strings.Index(line, saysFailed)+len(saysFailed):]) == notFoundWhy {
			return joinNotFound, 0
		}
		return joinFailed, 0
	}
	return "", 0
}

// leadingInt is the number s starts with; 0 for none.
func leadingInt(s string) int {
	n := 0
	for i := 0; i < len(s) && i < 4 && s[i] >= '0' && s[i] <= '9'; i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// joinSays says result in the page's words, for device, which tried the
// network named ssid ("" when its log didn't say which).
func joinSays(device, ssid, result string) string {
	network := ssid
	if network == "" {
		network = "a saved network"
	}
	switch result {
	case joinJoined:
		return fmt.Sprintf("%s joined %s. Next it calls the bridge.", device, network)
	case joinRefused:
		if ssid == "" {
			network = "A saved network"
		}
		return fmt.Sprintf("%s refused the password %s was given, so it shows WRONG WIFI PASSWORD. Change the Wi-Fi networks, and type the password again.", network, device)
	case joinNotFound:
		return fmt.Sprintf("%s can’t see any of its networks. One that’s 5 GHz only (on an iPhone hotspot, turn on Maximize Compatibility), one out of range and one named differently all look the same to it. Change the Wi-Fi networks if a name is wrong.", device)
	case joinFailed:
		return fmt.Sprintf("%s found %s but couldn’t join it: it may be at the edge of its range. It keeps trying; if it never calls the bridge, Change the Wi-Fi networks.", device, network)
	}
	return fmt.Sprintf("%s hasn’t said over USB whether it joined. If it doesn’t call the bridge soon, Change the Wi-Fi networks.", device)
}

// A lastPairing is the pairing that last succeeded on this page.
type lastPairing struct{ port, name string }

// A joinWatch is a POST /api/pair/join reading the pager's port.
type joinWatch struct {
	cancel context.CancelFunc
	done   chan struct{} // closed once its port is closed
}

// stopJoin ends the join watch, if any, and waits for its port to close, so
// a pairing can open it.
func (h *Handler) stopJoin() {
	h.pairMu.Lock()
	jw := h.joinW
	h.joinW = nil
	h.pairMu.Unlock()
	if jw != nil {
		jw.cancel()
		<-jw.done
	}
}

// pairJoin is POST /api/pair/join.
func (h *Handler) pairJoin(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if !readJSON(w, r, &req) {
		return
	}
	h.stopJoin() // one at a time: the newest
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer context.AfterFunc(h.life, cancel)()
	jw := &joinWatch{cancel: cancel, done: make(chan struct{})}
	h.pairMu.Lock()
	last := h.last
	switch {
	case last.port == "":
		h.pairMu.Unlock()
		jsonError(w, http.StatusConflict, "No pager has been paired on this page yet. Pair it first.")
		return
	case h.pair != nil:
		h.pairMu.Unlock()
		jsonError(w, http.StatusConflict, "A pairing is under way: its pager hasn't joined yet.")
		return
	}
	h.joinW = jw
	nets := h.netNames[last.name]
	h.pairMu.Unlock()
	defer func() {
		h.pairMu.Lock()
		if h.joinW == jw {
			h.joinW = nil
		}
		h.pairMu.Unlock()
		close(jw.done)
	}()

	result, network := h.watchJoin(ctx, last.port)
	if result == "" {
		jsonError(w, http.StatusConflict, "Stopped reading the pager's log.")
		return
	}
	ssid := ""
	if network >= 1 && network <= len(nets) {
		ssid = nets[network-1]
	}
	says := joinSays(last.name, ssid, result)
	h.step(says)
	writeJSON(w, struct {
		Result  string `json:"result"`
		Network string `json:"network,omitempty"` // the network it tried, when its log says which
		Says    string `json:"says"`
	}{result, ssid, says})
}

// watchJoin reads the pager's log on path until it says how its join went,
// for up to joinWait, opening the port again whenever it goes. It returns
// the result ("" once ctx ends) and the saved network it last tried.
func (h *Handler) watchJoin(ctx context.Context, path string) (result string, network int) {
	deadline := h.clock.After(joinWait)
	for {
		if port, err := provision.OpenPort(path); err == nil {
			result, network = h.readJoin(ctx, port, deadline, network)
			port.Close()
			if result != "" || ctx.Err() != nil {
				return result, network
			}
		}
		select {
		case <-ctx.Done():
			return "", network
		case <-deadline:
			return joinUnknown, network
		case <-h.clock.After(reopenWait):
		}
	}
}

// readJoin is watchJoin's read of one open port: it shows each log line on
// the pager channel, and returns "" when the port goes, the pager
// restarting.
func (h *Handler) readJoin(ctx context.Context, port *provision.Port, deadline <-chan time.Time, network int) (string, int) {
	lines, stop := make(chan string), make(chan struct{})
	defer close(stop) // the caller closes the port, which ends the read
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(port)
		for sc.Scan() {
			select {
			case lines <- strings.TrimSuffix(sc.Text(), "\r"):
			case <-stop:
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return "", network
		case <-deadline:
			return joinUnknown, network
		case line, ok := <-lines:
			if !ok {
				return "", network
			}
			if strings.HasPrefix(line, provision.Prefix) {
				continue
			}
			h.pagerLine(line)
			result, n := joinLine(line)
			if n > 0 {
				network = n
			}
			if result != "" {
				return result, network
			}
		}
	}
}
