package emutest

import (
	"image"
	"slices"
	"strings"
	"testing"
	"time"
)

// wantBody fails t unless the body's texts (Body) start with want.
func wantBody(t *testing.T, in *Instance, want ...string) []string {
	t.Helper()
	got := in.Body()
	if len(got) < len(want) || !slices.Equal(got[:len(want)], want) {
		t.Fatalf("the body says:\n  %s\nwant it to start:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	return got
}

// TestDeskPermissionCard: the card of an agent blocked on phase 0's shell
// prompt puts its command first, then the prompt box's title (parent 7.2,
// ADR 0006); DENY under PWR, and HOLD: over APPROVE under BOOT (parent
// design §UI). While BOOT is held, its cell fills from the left (design
// D2-10), and letting go before the second sends nothing (parent 8.1).
// Goldens: the card, and the card mid-hold (19.4).
func TestDeskPermissionCard(t *testing.T) {
	shell, _ := claudePrompt(t, "shell")
	b := StartBridge(t, deskScenario(t, deskAgent{pane: "w1:p1", name: "api-refactor", status: "blocked", prompt: "shell"}))
	in := deskPaired(t, b, "!1 *0 >0", true)
	openCard(t, in, "DENY")
	wantBody(t, in, shell.Command, shell.Title)
	in.HintCell("HOLD:\nAPPROVE")
	in.MatchDeskGolden("card_permission")

	// Mid-hold: the fill's edge moves every 100 ms, so the golden masks the
	// band of the cell it may be in when the snap is taken; the tree says
	// it's mid-way. The release comes well before the second.
	h := in.Hold("boot")
	time.Sleep(450 * time.Millisecond)
	snap := h.Snap()
	h.Release()
	cell, fill, ok := snap.Tree.HintCell("HOLD:\nAPPROVE")
	if !ok {
		t.Fatal("mid-hold, no hint cell says HOLD: APPROVE")
	}
	if fill.W <= 0 || fill.W >= cell.W || fill.X != cell.X {
		t.Fatalf("mid-hold, the BOOT cell (x %d, %d px wide) has a fill of %d px at x %d; want part of it, from its left", cell.X, cell.W, fill.W, fill.X)
	}
	edge := image.Rect(cell.X+cell.W*25/100, cell.Y, cell.X+cell.W*85/100, cell.Y+cell.H)
	in.MatchDeskSnap("card_permission_hold", snap, edge)
	if n := len(b.SentKeys()); n != 0 {
		t.Fatalf("fakeherdr got %d send_keys from a hold let go before the second", n)
	}
}
