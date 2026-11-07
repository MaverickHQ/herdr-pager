package emutest

import (
	"fmt"
	"testing"
	"time"
)

// doneAt is when fakeherdr ends the agent's turn: well after the list is up.
const doneAt = 15 * time.Second

// TestDeskDoneTone: an agent that finishes its turn plays the done tone once
// (parent 6.2; pulled into the desk pager on the acceptance day, 2026-10-25),
// and its row shows the done glyph. The tone's first note is 660 Hz; nothing
// more plays while the agent stays done.
func TestDeskDoneTone(t *testing.T) {
	start := time.Now()
	scenario := deskScenario(t, deskAgent{pane: "w1:p1", name: "api-refactor", status: "working"}) +
		fmt.Sprintf("[[change]]\nat = %q\npane = \"w1:p1\"\nstatus = \"done\"\n", doneAt)
	b := StartBridge(t, scenario)
	in := deskPaired(t, b, "ALL CLEAR >1", false)
	if up := time.Since(start); up > doneAt-2*time.Second {
		t.Fatalf("the list was up %v after the start, too close to the change at %v: raise doneAt", up, doneAt)
	}
	_, next := in.Tones(0)

	in.WaitText("!0 *1 >0", doneAt+10*time.Second)
	if tone := in.WaitTone(next, 5*time.Second); tone.Hz != 660 {
		t.Fatalf("the finished turn's first tone is %d Hz, want the done tone's 660 Hz", tone.Hz)
	}
	time.Sleep(5 * time.Second)
	events, _ := in.Tones(next)
	n := 0
	for _, e := range events {
		if !e.Stop && e.Hz == 660 {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the done tone played %d times, want once: %+v", n, events)
	}
}
