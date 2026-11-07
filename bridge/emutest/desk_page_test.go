package emutest

import (
	"slices"
	"testing"
	"time"
)

// cardGuard is how long a card ignores a press that starts after it appears
// (parent 8.1: 500 ms), and a little, for the harness's polling.
const cardGuard = 700 * time.Millisecond

// openCard opens the selected agent's card with PWR (parent 7.1), waits for
// its hint text, and past the card's guard.
func openCard(t *testing.T, in *Instance, hint string) {
	t.Helper()
	in.Press("pwr", 100)
	in.WaitText(hint, 5*time.Second)
	time.Sleep(cardGuard)
}

// TestDeskBlockedPageThenHoldApproves: an agent blocked on phase 0's shell
// prompt pages (parent 6.1: the blocked tone, 880 Hz), its card opens with
// PWR, and a 1 s BOOT hold approves it (parent 8.1): fakeherdr's request log
// shows one send_keys, to that pane, of the keymap's approve keys (parent
// 8.5), and the pager shows OK (parent 8.5, 19.3).
func TestDeskBlockedPageThenHoldApproves(t *testing.T) {
	b := StartBridge(t, deskScenario(t, deskAgent{pane: "w1:p1", name: "api-refactor", status: "blocked", prompt: "shell"}))
	in := paired(t, b)
	in.WaitText("!1 *0 >0", 10*time.Second)
	if tone := in.WaitTone(0, 10*time.Second); tone.Hz != 880 {
		t.Fatalf("the page's first tone is %d Hz, want the blocked tone's 880 Hz", tone.Hz)
	}
	in.WaitIdle(1000, 10*time.Second)
	wantRows(t, in, "[sel] ! api-refactor claude")

	openCard(t, in, "DENY")
	if n := len(b.SentKeys()); n != 0 {
		t.Fatalf("fakeherdr got %d send_keys before the hold", n)
	}
	in.Press("boot", 1200)
	sent := b.WaitSentKeys(1, 5*time.Second)
	want := claudeKeys(t, "approve")
	if len(sent) != 1 || sent[0].Target != "w1:p1" || !slices.Equal(sent[0].Keys, want) {
		t.Fatalf("fakeherdr's send_keys: %+v; want one, to w1:p1, of %q", sent, want)
	}
	in.WaitText("OK", 2*time.Second)
	in.WaitText("OPEN", 5*time.Second) // the OK toast's 2 s are over: the list
	if n := len(b.SentKeys()); n != 1 {
		t.Fatalf("fakeherdr got %d send_keys, want 1", n)
	}
}
