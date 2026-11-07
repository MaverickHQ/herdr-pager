package e2e

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/version"
)

// TestPairedDeviceGetsSnapshot is seam S2-1's walking skeleton (parent 3.1,
// 3.2, 4.2): a paired device dials the bridge over SSH and says hello; the
// bridge answers with its hello, then a snapshot of the fake herdr's agents.
// The clock never moves, so the snapshot comes within 1 s of the hello.
func TestPairedDeviceGetsSnapshot(t *testing.T) {
	b := startBridge(t, agentsFixture(t, "socket/agent.list/ok.json"))
	dev := b.dial(b.pair("pager-01"))
	if err := dev.Send(fixture(t, "to-bridge/valid/hello.ndjson")); err != nil {
		t.Fatal(err)
	}

	hello := recv[*protocol.BridgeHello](t, dev)
	if hello.Host != "test-host" || hello.Now != start.Unix() {
		t.Errorf("hello: host %q, now %d; want test-host, %d", hello.Host, hello.Now, start.Unix())
	}
	// The bridge's version, from the one place --version reads (phase 5 task 1.3).
	if hello.Bridge != version.String() {
		t.Errorf("hello: bridge %q; want the bridge's version %q", hello.Bridge, version.String())
	}
	snap := recv[*protocol.Snap](t, dev)
	if snap.Now-hello.Now > 1 {
		t.Errorf("the snapshot came %d s after the hello; at most 1", snap.Now-hello.Now)
	}
	if snap.Herdr != "ok" || snap.More != 0 || len(snap.Agents) != 1 {
		t.Fatalf("snapshot: herdr %q, more %d, %d agents; want ok, 0, the fixture's 1", snap.Herdr, snap.More, len(snap.Agents))
	}
	if a := snap.Agents[0]; a.Pane != "w1:p1" || a.St != "blocked" || a.Seq != 3 {
		t.Errorf("agent: pane %s, st %s, seq %d; want w1:p1, blocked, 3 (agent.list/ok.json)", a.Pane, a.St, a.Seq)
	}
}

// TestSnapshotEntryFields checks every field of a snapshot entry (parent
// 4.3): the pane id; the display name, sanitized and at most 24 characters
// (herdr's name, else its terminal title without Claude Code's leading status
// glyph, else "<kind> <pane id>"; the desk checkpoint, 2026-10-24); the kind; the status; the seq and
// dseq (herdr's completion_seq, else 0); since, when the bridge first saw the
// seq; and `can`, with interrupt only for a working agent whose kind has a
// keymap. And each snapshot carries the bridge's Unix time (parent 4.6).
func TestSnapshotEntryFields(t *testing.T) {
	agents := []herdr.AgentInfo{
		{TerminalID: "term_a", PaneID: "w1:p1", Agent: str("claude"), Name: str("recon"), Status: "blocked", StateChangeSeq: 3,
			Cwd: "/srv/owner/herdr-pager", ForegroundCwd: "/srv/owner/herdr-pager/bridge/"},
		{TerminalID: "term_b", PaneID: "w1:p2", Agent: str("claude"), Name: str("tests"), Status: "working", StateChangeSeq: 50, CompletionSeq: num(4)},
		{TerminalID: "term_c", PaneID: "w2:p4", Agent: str("claude"), Status: "done", StateChangeSeq: 22, CompletionSeq: num(3),
			TitleStripped: str("✳ Fix the flaky login test"), Cwd: "/tmp/hp-check"},
		{TerminalID: "term_d", PaneID: "w3:p4", Agent: str("codex"), Status: "working", StateChangeSeq: 47,
			TitleStripped: str("⠐ ")}, // nothing but a spinner: no title to show
		{TerminalID: "term_e", PaneID: "w4:p1", Agent: str("claude"), Status: "idle", StateChangeSeq: 2,
			Name: str("\x1b[1mapi​-refactor\x1b[0m of the payments service")},
	}
	b := startBridge(t, agents)
	dev := b.connect("pager-01")
	snap := recv[*protocol.Snap](t, dev)
	if snap.Now != start.Unix() || snap.Herdr != "ok" || len(snap.Agents) != len(agents) {
		t.Fatalf("snapshot: now %d, herdr %s, %d agents; want %d, ok, %d", snap.Now, snap.Herdr, len(snap.Agents), start.Unix(), len(agents))
	}

	got := byPane(snap)
	for _, want := range []struct {
		pane, name, kind, st string
		seq, dseq            uint64
		can                  []string
	}{
		{"w1:p1", "recon", "claude", "blocked", 3, 0, []string{"show", "jump"}},
		{"w1:p2", "tests", "claude", "working", 50, 4, []string{"show", "jump", "interrupt"}},
		{"w2:p4", "Fix the flaky login test", "claude", "done", 22, 3, []string{"show", "jump"}},
		{"w3:p4", "codex w3:p4", "codex", "working", 47, 0, []string{"show", "jump"}}, // codex has no keymap
		{"w4:p1", "api-refactor of the..", "claude", "idle", 2, 0, []string{"show", "jump"}},
	} {
		a, ok := got[want.pane]
		switch {
		case !ok:
			t.Errorf("%s: missing", want.pane)
			continue
		case a.St != want.st || a.Seq != want.seq || a.DSeq != want.dseq || a.Since != start.Unix() || !slices.Equal(a.Can, want.can):
			t.Errorf("%s: st %s, seq %d, dseq %d, since %d, can %v; want %s, %d, %d, %d, %v",
				want.pane, a.St, a.Seq, a.DSeq, a.Since, a.Can, want.st, want.seq, want.dseq, start.Unix(), want.can)
		case !printable(a.Name, 24) || !printable(a.Kind, 10):
			t.Errorf("%s: name %q, kind %q; want 1 to 24 and 1 to 10 bytes of printable ASCII", want.pane, a.Name, a.Kind)
		case a.Kind != want.kind:
			t.Errorf("%s: kind %q; want %q", want.pane, a.Kind, want.kind)
		case want.name != "" && a.Name != want.name:
			t.Errorf("%s: name %q; want %q", want.pane, a.Name, want.name)
		}
	}
	// at is where an agent works: its foreground folder's name, else its
	// pane's; none without either (the desk checkpoint, 2026-10-24).
	for pane, want := range map[string]string{"w1:p1": "bridge", "w2:p4": "hp-check", "w3:p4": ""} {
		if a := got[pane]; a.At != want {
			t.Errorf("%s: at %q; want %q", pane, a.At, want)
		}
	}
	// The escapes, the zero-width space and the length go; a name is cut at
	// its end, at a word, so it reads from its start (the desk checkpoint).
	if n := got["w4:p1"].Name; !strings.HasPrefix(n, "api-refactor of") || !strings.HasSuffix(n, "..") || len(n) > 24 {
		t.Errorf("w4:p1: name %q; want the name sanitized and cut at its end to 24", n)
	}

	agents[0].Status, agents[0].StateChangeSeq = "working", 60
	b.herdr.SetAgents(agents)
	b.clock.Advance(time.Second)
	snap = recv[*protocol.Snap](t, dev)
	a := byPane(snap)["w1:p1"]
	if snap.Now != start.Unix()+1 || a.St != "working" || a.Seq != 60 || a.Since != start.Unix()+1 || !slices.Equal(a.Can, []string{"show", "jump", "interrupt"}) {
		t.Errorf("a second later: now %d; w1:p1 st %s, seq %d, since %d, can %v; want %d; working, 60, %d, [show jump interrupt]",
			snap.Now, a.St, a.Seq, a.Since, a.Can, start.Unix()+1, start.Unix()+1)
	}
}

// TestSnapshotCapsAtSixteen checks the protocol's cap (parent design
// §Protocol, parent 13.5): of 20 agents a snapshot carries the 16 most
// urgent, blocked, then done, unknown, working and idle, each status's oldest
// change first, and counts the other 4 in `more`; and its line is at most
// 8,192 bytes, even with every field at its longest.
func TestSnapshotCapsAtSixteen(t *testing.T) {
	var agents []herdr.AgentInfo
	add := func(st string, seqs ...uint64) {
		for _, seq := range seqs {
			n := len(agents) + 1
			agents = append(agents, herdr.AgentInfo{
				TerminalID: fmt.Sprintf("term_%d", n), PaneID: fmt.Sprintf("w1234567890:p%d", n),
				Agent: str("claude"), Name: str(strings.Repeat("n", 40)), Status: st, StateChangeSeq: seq,
				CompletionSeq: num(9007199254740991),
			})
		}
	}
	add("blocked", 30, 10, 20)                  // p1–p3
	add("done", 5, 6)                           // p4, p5
	add("unknown", 7)                           // p6
	add("working", 45, 44, 43, 42, 41, 40)      // p7–p12
	add("idle", 57, 50, 56, 51, 55, 52, 54, 53) // p13–p20
	b := startBridge(t, agents)
	dev := b.connect("pager-01")

	line, err := dev.Recv(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(line) > protocol.MaxBridgeLine {
		t.Errorf("the snapshot is %d bytes; at most %d", len(line), protocol.MaxBridgeLine)
	}
	m, err := protocol.ParseBridge([]byte(line))
	if err != nil {
		t.Fatalf("the snapshot breaks the protocol: %v", err)
	}
	snap := m.(*protocol.Snap)
	var order []string
	for _, a := range snap.Agents {
		order = append(order, strings.TrimPrefix(a.Pane, "w1234567890:"))
	}
	want := []string{"p2", "p3", "p1", "p4", "p5", "p6", "p12", "p11", "p10", "p9", "p8", "p7", "p14", "p16", "p18", "p20"}
	if !slices.Equal(order, want) || snap.More != 4 {
		t.Errorf("snapshot: %v and more %d; want %v and more 4", order, snap.More, want)
	}
}

// TestBlockedFlipWithinBudget is the bridge's leg of N1 (parent 4.1), on the
// fake clock: an agent flips to blocked just after a poll, the worst case,
// and the snapshot showing it blocked reaches the device within 1.5 s.
func TestBlockedFlipWithinBudget(t *testing.T) {
	agents := []herdr.AgentInfo{{TerminalID: "term_a", PaneID: "w1:p1", Agent: str("claude"), Name: str("recon"), Status: "working", StateChangeSeq: 2}}
	b := startBridge(t, agents)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev) // the poll at the start: working

	agents[0].Status, agents[0].StateChangeSeq = "blocked", 3
	b.herdr.SetAgents(agents)
	const step, budget = 100 * time.Millisecond, 1500 * time.Millisecond
	for waited := time.Duration(0); waited <= budget; waited += step {
		if line, err := dev.Recv(100 * time.Millisecond); err == nil {
			m, err := protocol.ParseBridge([]byte(line))
			if snap, ok := m.(*protocol.Snap); err == nil && ok && len(snap.Agents) == 1 && snap.Agents[0].St == "blocked" {
				t.Logf("the flip reached the device %v after it happened", waited)
				return
			}
		}
		b.clock.Advance(step)
	}
	t.Errorf("no snapshot showed the agent blocked within %v of its flip", budget)
}

// TestNoSnapshotWithoutChange checks parent 4.1: polls that find nothing
// new send nothing, nor do changes the snapshot doesn't carry (herdr's
// revision, focus or title); a change of status, seq, completion_seq,
// display name or folder, or an agent appearing or going, sends one
// snapshot.
func TestNoSnapshotWithoutChange(t *testing.T) {
	agents := []herdr.AgentInfo{
		{TerminalID: "term_a", PaneID: "w1:p1", Agent: str("claude"), Name: str("recon"), Status: "working", StateChangeSeq: 2, TitleStripped: str("Working")},
		{TerminalID: "term_b", PaneID: "w1:p2", Agent: str("claude"), Name: str("tests"), Status: "idle", StateChangeSeq: 1},
	}
	b := startBridge(t, agents)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)

	quiet := func(why string) {
		t.Helper()
		b.tick()
		if line, err := dev.Recv(100 * time.Millisecond); err == nil {
			t.Errorf("%s: the bridge sent %s; want nothing", why, line)
		}
	}
	sends := func(why string) {
		t.Helper()
		b.herdr.SetAgents(agents)
		b.tick()
		recv[*protocol.Snap](t, dev)
		if line, err := dev.Recv(100 * time.Millisecond); err == nil {
			t.Errorf("%s: a second line, %s; want one snapshot", why, line)
		}
	}

	for range 3 {
		quiet("nothing changed")
	}
	agents[0].Revision, agents[0].Focused, agents[0].TitleStripped = 7, true, str("Still working")
	b.herdr.SetAgents(agents)
	quiet("only the revision, focus and title changed")

	agents[0].Status, agents[0].StateChangeSeq = "blocked", 3
	sends("a status and seq change")
	quiet("nothing changed after it")
	agents[1].CompletionSeq = num(5)
	sends("a completion_seq change")
	agents[1].Name = str("tests-2")
	sends("a name change")
	agents[1].ForegroundCwd = "/srv/sam/src/other-repo" // the folder, which the snapshot carries since D2-15
	sends("a folder change")
	agents = append(agents, herdr.AgentInfo{TerminalID: "term_c", PaneID: "w2:p1", Agent: str("claude"), Status: "working", StateChangeSeq: 4})
	sends("an agent appearing")
	agents = agents[:2]
	sends("an agent going")
	quiet("nothing changed after it")
}

// TestSinceStableWhileSeqUnchanged checks parent 4.3's since: the bridge's
// time when it first saw an agent's current seq. It stays put across polls
// and other agents' changes, moves when the seq does (done included: the
// bridge records "done since" itself, since herdr's completion_seq isn't a
// counter), and follows the agent by terminal_id when its pane moves.
func TestSinceStableWhileSeqUnchanged(t *testing.T) {
	agents := []herdr.AgentInfo{
		{TerminalID: "term_a", PaneID: "w1:p1", Agent: str("claude"), Status: "working", StateChangeSeq: 2},
		{TerminalID: "term_b", PaneID: "w1:p2", Agent: str("claude"), Status: "idle", StateChangeSeq: 1},
	}
	b := startBridge(t, agents)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)
	at := func(s int64) int64 { return start.Unix() + s }
	// change ticks to the second before s, sets the agents, ticks once more,
	// and returns what that poll's snapshot says of term_a, now at pane.
	change := func(s int64, pane string) protocol.Agent {
		t.Helper()
		for b.clock.Now().Unix() < at(s)-1 {
			b.tick()
		}
		b.herdr.SetAgents(agents)
		b.tick()
		return byPane(recv[*protocol.Snap](t, dev))[pane]
	}

	agents[1].Status, agents[1].StateChangeSeq = "working", 3
	if a := change(3, "w1:p1"); a.Since != at(0) {
		t.Errorf("another agent changed at 3 s: since %d; want %d, the start", a.Since, at(0))
	}
	agents[0].Status, agents[0].StateChangeSeq, agents[0].CompletionSeq = "done", 5, num(9)
	if a := change(5, "w1:p1"); a.Since != at(5) {
		t.Errorf("done at 5 s: since %d; want %d", a.Since, at(5))
	}
	agents[1].Status, agents[1].StateChangeSeq = "idle", 6
	if a := change(8, "w1:p1"); a.Since != at(5) {
		t.Errorf("done, at 8 s: since %d; want %d, when it became done", a.Since, at(5))
	}
	agents[0].PaneID = "w2:p1"
	if a := change(10, "w2:p1"); a.Since != at(5) {
		t.Errorf("its pane moved at 10 s: since %d; want %d", a.Since, at(5))
	}
}

// TestSlowSubscriberDoesNotBlock checks parent 4.1 with a device that never
// reads: once its SSH window (2 MB) is full, the bridge can't write to it,
// yet a second device still gets each snapshot as soon as it's polled. The
// agents change on every poll until more than 3 MB has gone to each device.
func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	var agents []herdr.AgentInfo
	for i := range protocol.MaxAgents {
		agents = append(agents, herdr.AgentInfo{
			TerminalID: fmt.Sprintf("term_%d", i), PaneID: fmt.Sprintf("w%013d:p%015d", i, i),
			Agent: str("claude"), Name: str(strings.Repeat("n", 24)), Status: "working", StateChangeSeq: uint64(i),
		})
	}
	b := startBridge(t, agents)
	slow := b.connect("slow-01") // never read again
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)

	sent := 0
	for seq := uint64(100); sent < 3<<20; seq++ {
		if seq%60 == 0 { // both ping every 60 s, as pagers do, so neither goes idle
			slow.Send(`{"t":"ping","id":1}`)
			dev.Send(`{"t":"ping","id":1}`)
			recv[*protocol.Pong](t, dev)
		}
		agents[0].StateChangeSeq = seq
		b.herdr.SetAgents(agents)
		b.tick()
		line, err := dev.Recv(time.Second)
		if err != nil {
			t.Fatalf("after %d bytes to each device, the snapshot for seq %d didn't come within 1 s: %v", sent, seq, err)
		}
		sent += len(line) + 1
	}
}

func byPane(s *protocol.Snap) map[string]protocol.Agent {
	m := map[string]protocol.Agent{}
	for _, a := range s.Agents {
		m[a.Pane] = a
	}
	return m
}

// TestSnapshotNamesSessionsByTheirHerdrTab checks the names the owner asked
// for at the desk checkpoint (2026-10-24), to match each row to herdr's tab
// bar: herdr's workspace and tab numbers and the tab's label ("4.3
// build-session"), ahead of herdr's name and the terminal title; a tab
// labelled with its number alone shows the numbers; a name over 24 is cut at
// its end, at a space or hyphen, never in the middle; and an agent whose tab
// tab.list doesn't know keeps its title.
func TestSnapshotNamesSessionsByTheirHerdrTab(t *testing.T) {
	agents := []herdr.AgentInfo{
		{TerminalID: "term_a", PaneID: "w2:p1", WorkspaceID: "w2", TabID: "w2:t3", Agent: str("claude"), Name: str("tests"),
			TitleStripped: str("✳ Run the suite"), Status: "blocked", StateChangeSeq: 3},
		{TerminalID: "term_b", PaneID: "w2:p5", WorkspaceID: "w2", TabID: "w2:t5", Agent: str("claude"), Status: "idle", StateChangeSeq: 4},
		{TerminalID: "term_c", PaneID: "w6:pE", WorkspaceID: "w6", TabID: "w6:t7", Agent: str("claude"), Status: "idle", StateChangeSeq: 5},
		{TerminalID: "term_d", PaneID: "w9:p1", WorkspaceID: "w9", TabID: "w9:t1", Agent: str("claude"),
			TitleStripped: str("✳ Draft the release notes for v0.1.0 today"), Status: "idle", StateChangeSeq: 6},
	}
	b := startBridge(t, agents)
	b.herdr.SetTabs(
		[]herdr.TabInfo{
			{TabID: "w2:t3", WorkspaceID: "w2", Number: 3, Label: "build-session"},
			{TabID: "w2:t5", WorkspaceID: "w2", Number: 5, Label: "5"},
			{TabID: "w6:t7", WorkspaceID: "w6", Number: 7, Label: "live-coding-session-for-the-take-home"},
		},
		[]herdr.WorkspaceInfo{{WorkspaceID: "w2", Number: 4, Label: "herdr-pager"}, {WorkspaceID: "w6", Number: 5, Label: "fireworks"}},
	)
	b.clock.Advance(time.Second)
	dev := b.connect("pager-01")
	got := byPane(recv[*protocol.Snap](t, dev))
	for pane, want := range map[string]string{
		"w2:p1": "4.3 build-session",   // the tab, ahead of herdr's name "tests"
		"w2:p5": "4.5",                 // a label that's only the tab's number
		"w6:pE": "5.7 live-coding..",   // cut at a hyphen, not mid-word
		"w9:p1": "Draft the release..", // no tab known: the title, cut at a space
	} {
		if a := got[pane]; a.Name != want {
			t.Errorf("%s: name %q; want %q", pane, a.Name, want)
		}
	}
}
