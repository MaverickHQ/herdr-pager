// Package watch polls herdr's agent.list every second, records when each
// agent's seq was first seen, and publishes a snapshot when anything changes
// (parent 4.1, parent design §Bridge components, watch). A slow subscriber
// never blocks the poller: each holds only the latest snapshot.
package watch

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/clock"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/keymap"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/sanitize"
)

// Interval is how often the watcher polls herdr; it isn't configurable
// (phase 2 R5.3).
const Interval = time.Second

// Config is what a watcher watches with.
type Config struct {
	Herdr   *herdr.Client
	Keymaps keymap.Set // which kinds have a keymap, for each agent's `can`
	Clock   clock.Clock
	Log     *slog.Logger
}

// A Watcher polls one herdr.
type Watcher struct {
	cfg Config

	// Only the poller touches these.
	seen    map[string]firstSeen // by terminal_id
	offline string               // why herdr is offline (its code), or ""

	mu     sync.Mutex
	latest *protocol.Snap
	subs   map[chan protocol.Snap]bool
}

// firstSeen is an agent's current seq and when the bridge first saw it.
type firstSeen struct {
	seq   uint64
	since int64
}

// New returns a watcher with cfg.
func New(cfg Config) *Watcher {
	return &Watcher{cfg: cfg, seen: map[string]firstSeen{}, subs: map[chan protocol.Snap]bool{}}
}

// Run polls at once, then every Interval, until ctx ends. Each poll is due
// an Interval after the last one was, not after it ended; polls missed while
// the Mac slept are skipped, not caught up.
func (w *Watcher) Run(ctx context.Context) error {
	next := w.cfg.Clock.Now()
	for {
		w.poll(ctx)
		next = next.Add(Interval)
		if now := w.cfg.Clock.Now(); next.Before(now) {
			next = now
		}
		select {
		case <-ctx.Done():
			return nil
		case <-w.cfg.Clock.After(next.Sub(w.cfg.Clock.Now())):
		}
	}
}

func (w *Watcher) poll(ctx context.Context) {
	agents, err := w.cfg.Herdr.ListAgents(ctx)
	if ctx.Err() != nil {
		return
	}
	var tabs map[string]string
	if err == nil {
		tabs = w.tabNames(ctx)
	}
	now := w.cfg.Clock.Now().Unix()
	if err != nil {
		// Devices see herdr offline (parent 4.4); the log says why, once
		// each time the reason changes, by its code (phase 2 R5.4).
		if code := herdr.Code(err); code != w.offline {
			w.cfg.Log.Warn("herdr: offline", "code", code, "socket", w.cfg.Herdr.SocketPath)
			w.offline = code
		}
		w.publish(protocol.Snap{Now: now, Herdr: "offline"})
		return
	}
	if w.offline != "" {
		w.cfg.Log.Info("herdr: back")
		w.offline = ""
	}
	snap := protocol.Snap{Now: now, Herdr: "ok"}
	seen := map[string]firstSeen{}
	for _, a := range agents {
		if !protocol.IsPane(a.PaneID) {
			w.cfg.Log.Warn("herdr: an agent without a pane id the protocol carries; left out")
			continue
		}
		// An agent is keyed by its terminal, which stays when its pane moves
		// (phase 2 R5.2). Its since is when its current seq was first seen;
		// that covers done too, since completion_seq isn't a counter.
		key := cmp.Or(a.TerminalID, a.PaneID)
		first, ok := w.seen[key]
		if !ok || first.seq != a.StateChangeSeq {
			first = firstSeen{a.StateChangeSeq, now}
		}
		seen[key] = first
		kind, name := names(a, tabs[a.TabID])
		e := protocol.Agent{
			Pane: a.PaneID, Name: name, Kind: kind, At: sanitize.Short(a.Folder(), sanitize.At), St: a.Status, Seq: a.StateChangeSeq,
			Since: first.since, Can: w.can(a),
		}
		if a.CompletionSeq != nil {
			e.DSeq = *a.CompletionSeq
		}
		snap.Agents = append(snap.Agents, e)
	}
	w.seen = seen
	sortByUrgency(snap.Agents)
	if len(snap.Agents) > protocol.MaxAgents {
		snap.More = len(snap.Agents) - protocol.MaxAgents
		snap.Agents = snap.Agents[:protocol.MaxAgents]
	}
	w.publish(snap)
}

// urgency orders the statuses, most urgent first (parent 5.1; the bridge
// can't know which done agents the device has seen).
var urgency = map[string]int{"blocked": 0, "done": 1, "unknown": 2, "working": 3, "idle": 4}

// sortByUrgency sorts agents most urgent first, each status's oldest change
// first: by since, then by seq (one counter for all agents, so a lower seq is
// an older change).
func sortByUrgency(agents []protocol.Agent) {
	slices.SortStableFunc(agents, func(a, b protocol.Agent) int {
		return cmp.Or(
			cmp.Compare(urgency[a.St], urgency[b.St]),
			cmp.Compare(a.Since, b.Since),
			cmp.Compare(a.Seq, b.Seq),
			cmp.Compare(a.Pane, b.Pane),
		)
	})
}

// can is what a snapshot offers for a: show and jump, and interrupt for a
// working agent whose kind has a keymap with interrupt keys (phase 2 R6.3).
// Never approve or deny: only a card offers those (parent 8.1).
func (w *Watcher) can(a herdr.AgentInfo) []string {
	can := []string{"show", "jump"}
	if a.Status == "working" && a.Agent != nil {
		if km := w.cfg.Keymaps[*a.Agent]; km != nil && len(km.Interrupt) > 0 {
			can = append(can, "interrupt")
		}
	}
	return can
}

// tabNames returns each tab's name as herdr's tab bar shows it, by tab id:
// "<workspace number>.<tab number> <label>", or the numbers alone when the
// label is just the tab's number (the desk checkpoint, 2026-10-24). If either
// list fails, there are none, and the names fall back (names).
func (w *Watcher) tabNames(ctx context.Context) map[string]string {
	tabs, err := w.cfg.Herdr.ListTabs(ctx)
	if err != nil {
		return nil
	}
	spaces, err := w.cfg.Herdr.ListWorkspaces(ctx)
	if err != nil {
		return nil
	}
	number := map[string]uint{}
	for _, s := range spaces {
		number[s.WorkspaceID] = s.Number
	}
	out := map[string]string{}
	for _, t := range tabs {
		ws, ok := number[t.WorkspaceID]
		if !ok {
			continue
		}
		n := fmt.Sprintf("%d.%d", ws, t.Number)
		if l := strings.TrimSpace(t.Label); l != "" && l != strconv.FormatUint(uint64(t.Number), 10) {
			n += " " + l
		}
		out[t.TabID] = n
	}
	return out
}

// names returns a's kind and display name, sanitized (parent 4.3, 13.4): its
// herdr tab's name when there is one (tab, from tabNames), else herdr's name
// for it, its terminal title or "<kind> <pane id>" (phase 2 R5.2;
// herdr.AgentInfo.DisplayName), cut at its end. An agent herdr gives no kind
// shows "?".
func names(a herdr.AgentInfo, tab string) (kind, name string) {
	if a.Agent != nil {
		kind = sanitize.Text(*a.Agent, sanitize.Kind)
	}
	if kind == "" {
		kind = "?"
	}
	name = sanitize.Short(cmp.Or(tab, a.DisplayName()), sanitize.Name)
	if name == "" {
		// Nothing of it can be shown. Both parts are printable ASCII
		// already: the kind sanitized, the pane id matched against the
		// protocol's form.
		name = fit(kind+" "+a.PaneID, sanitize.Name)
	}
	return kind, name
}

// fit cuts ASCII s to n bytes, keeping its head and tail around "..".
func fit(s string, n int) string {
	if len(s) <= n {
		return s
	}
	head := (n - 2) / 2
	return s[:head] + ".." + s[len(s)-(n-2-head):]
}

// publish makes snap the latest snapshot and offers it to every subscriber,
// unless it shows nothing the latest didn't (parent 4.1).
func (w *Watcher) publish(snap protocol.Snap) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.latest != nil && same(*w.latest, snap) {
		return
	}
	w.latest = &snap
	for ch := range w.subs {
		offer(ch, snap)
	}
}

// same says whether a and b show the same thing: herdr's state, the agents
// in order with every field, and how many more there are. Their times don't
// count; nor does since, which moves only with an agent's seq.
func same(a, b protocol.Snap) bool {
	if a.Herdr != b.Herdr || a.More != b.More || len(a.Agents) != len(b.Agents) {
		return false
	}
	for i, x := range a.Agents {
		y := b.Agents[i]
		if x.Pane != y.Pane || x.Name != y.Name || x.Kind != y.Kind || x.At != y.At || x.St != y.St || x.Seq != y.Seq || x.DSeq != y.DSeq || !slices.Equal(x.Can, y.Can) {
			return false
		}
	}
	return true
}

// offer puts snap in ch's one slot, replacing a snapshot not yet taken.
func offer(ch chan protocol.Snap, snap protocol.Snap) {
	select {
	case <-ch:
	default:
	}
	ch <- snap
}

// Latest returns the latest snapshot, if there is one yet.
func (w *Watcher) Latest() (protocol.Snap, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.latest == nil {
		return protocol.Snap{}, false
	}
	return *w.latest, true
}

// Subscribe returns a channel holding the latest snapshot, and a function
// that ends the subscription. The channel holds the current snapshot at once
// if there is one.
func (w *Watcher) Subscribe() (<-chan protocol.Snap, func()) {
	ch := make(chan protocol.Snap, 1)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.subs[ch] = true
	if w.latest != nil {
		ch <- *w.latest
	}
	return ch, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.subs, ch)
	}
}
