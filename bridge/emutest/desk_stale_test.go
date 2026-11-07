package emutest

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

// staleAt is when fakeherdr gives the agent a new seq: well after its card
// is open (about 7 s after the start).
const staleAt = 15 * time.Second

// TestDeskStaleShowsScreenChanged: fakeherdr gives the blocked agent a new
// state_change_seq after its card is shown (the same status again: a new
// prompt in the same place). The pager pages again for the new seq; a 1 s
// hold on the old card then sends no keys: the bridge finds the seq changed,
// the pager says SCREEN CHANGED and asks for the card as it is now (parent
// 8.4). The verdict is never resent: only a hold on the fresh card sends the
// keymap's keys. Golden: SCREEN CHANGED (19.4).
func TestDeskStaleShowsScreenChanged(t *testing.T) {
	start := time.Now()
	scenario := deskScenario(t, deskAgent{pane: "w1:p1", name: "api-refactor", status: "blocked", prompt: "shell"}) +
		fmt.Sprintf("[[change]]\nat = %q\npane = \"w1:p1\"\nstatus = \"blocked\"\n", staleAt)
	b := StartBridge(t, scenario)
	in := deskPaired(t, b, "!1 *0 >0", true)
	_, next := in.Tones(0)
	openCard(t, in, "DENY")
	if opened := time.Since(start); opened > staleAt-2*time.Second {
		t.Fatalf("the card opened %v after the start, too close to the change at %v: raise staleAt", opened, staleAt)
	}

	if tone := in.WaitTone(next, staleAt+10*time.Second); tone.Hz != 880 { // the new seq pages
		t.Fatalf("the new seq's page is a %d Hz tone, want the blocked tone's 880 Hz", tone.Hz)
	}
	in.WaitIdle(1000, 10*time.Second)
	in.HintCell("HOLD:\nAPPROVE") // still the card shown before the change
	in.Press("boot", 1200)
	in.WaitText("SCREEN CHANGED", 5*time.Second)
	in.MatchDeskGolden("stale_screen_changed")
	if sent := b.SentKeys(); len(sent) != 0 {
		t.Fatalf("a stale card's approve sent %+v; want nothing", sent)
	}

	in.WaitText("DENY", 5*time.Second) // the card as it is now
	time.Sleep(cardGuard)
	if sent := b.SentKeys(); len(sent) != 0 {
		t.Fatalf("the stale verdict was resent: %+v", sent)
	}
	in.Press("boot", 1200)
	sent := b.WaitSentKeys(1, 5*time.Second)
	want := claudeKeys(t, "approve")
	if len(sent) != 1 || sent[0].Target != "w1:p1" || !slices.Equal(sent[0].Keys, want) {
		t.Fatalf("fakeherdr's send_keys: %+v; want one, to w1:p1, of %q", sent, want)
	}
	in.WaitText("OK", 2*time.Second)
}
