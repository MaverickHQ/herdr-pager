package emutest

import (
	"slices"
	"testing"
	"time"
)

// TestDeskToastsAndConfirms: PWR held 2 s toggles silent mode, with a toast
// each way and SILENT in the band while it's on (parent 6.7). BOOT and PWR
// held together for 10 s ask FACTORY RESET?, RESET in BOOT's cell and CANCEL
// in PWR's; PWR cancels, and the pager stays paired (parent 2.2). A verdict's
// result is a toast: X and the reason when it fails (herdr gone with the
// card open), OK when the keys went (parent 8.5). Goldens: each toast and
// the confirm (19.4).
func TestDeskToastsAndConfirms(t *testing.T) {
	b := StartBridge(t, deskScenario(t, deskAgent{pane: "w1:p1", name: "api-refactor", status: "blocked", prompt: "shell"}))
	in := deskPaired(t, b, "!1 *0 >0", true)

	// Silent on, then off.
	in.Press("pwr", 2300)
	in.WaitText("SILENT ON", 5*time.Second)
	in.MatchDeskGolden("toast_silent_on")
	in.WaitText("OPEN", 5*time.Second)
	in.WaitText("SILENT", time.Second) // the band's flag
	in.Press("pwr", 2300)
	in.WaitText("SILENT OFF", 5*time.Second)
	in.MatchDeskGolden("toast_silent_off")
	in.WaitText("OPEN", 5*time.Second)

	// The factory-reset confirm, cancelled.
	in.Press("boot+pwr", 10500)
	in.WaitText("FACTORY RESET?", 5*time.Second)
	if h := in.Tree().Hints(); len(h) != 2 || h[0].Text != "CANCEL" || h[1].Text != "RESET" {
		t.Fatalf("the confirm's hint cells, PWR then BOOT: %+v; want CANCEL, then RESET", h)
	}
	in.MatchDeskGolden("confirm_reset")
	in.Press("pwr", 100)
	in.WaitText("OPEN", 5*time.Second)
	wantRows(t, in, "[sel] ! api-refactor claude") // still paired, the list as it was

	// X: herdr goes with the card open, so the approve can't be checked.
	openCard(t, in, "DENY")
	b.StopHerdr()
	in.WaitText("HERDR OFFLINE", 5*time.Second)
	in.Press("boot", 1200)
	in.WaitText("X HERDR OFFLINE", 5*time.Second)
	in.MatchDeskGolden("toast_x")
	if sent := b.SentKeys(); len(sent) != 0 {
		t.Fatalf("fakeherdr got %+v while it was stopped", sent)
	}

	// OK: herdr is back, and the approve goes through.
	b.StartHerdr()
	in.WaitText("OPEN", 10*time.Second)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if _, ok := offlineLabel(in); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("HERDR OFFLINE still shown 5 s after fakeherdr came back")
		}
	}
	openCard(t, in, "DENY")
	in.Press("boot", 1200)
	sent := b.WaitSentKeys(1, 5*time.Second)
	if want := claudeKeys(t, "approve"); len(sent) != 1 || sent[0].Target != "w1:p1" || !slices.Equal(sent[0].Keys, want) {
		t.Fatalf("fakeherdr's send_keys: %+v; want one, to w1:p1, of %q", sent, want)
	}
	in.WaitText("OK", 2*time.Second)
	in.MatchDeskGolden("toast_ok")
}
