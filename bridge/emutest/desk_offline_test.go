package emutest

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// offlineLabel returns the visible label that says HERDR OFFLINE, if any.
func offlineLabel(in *Instance) (label Node, ok bool) {
	in.Tree().Walk(func(n Node) {
		if !ok && n.Visible && n.Text != nil && strings.Contains(*n.Text, "HERDR OFFLINE") {
			label, ok = n, true
		}
	})
	return label, ok
}

// TestDeskHerdrOffline: fakeherdr stops. The bridge says herdr is offline,
// and the pager shows HERDR OFFLINE in the band, unmasked in goldens (it's a
// state, not the time), over the list as it was: frozen, not emptied (parent
// 4.4). When fakeherdr is back, the flag goes. Golden: the frozen list (19.4).
func TestDeskHerdrOffline(t *testing.T) {
	b := StartBridge(t, deskScenario(t,
		deskAgent{pane: "w1:p1", name: "api-refactor", status: "working"},
		deskAgent{pane: "w1:p2", name: "db-migration", status: "blocked", prompt: "shell"},
	))
	in := deskPaired(t, b, "!1 *0 >1", true)
	rows := []string{"[sel] ! db-migration claude", "> api-refactor claude"}
	wantRows(t, in, rows...)

	b.StopHerdr()
	in.WaitText("HERDR OFFLINE", 5*time.Second)
	label, _ := offlineLabel(in)
	if slices.Contains(label.Tags, MaskTag) {
		t.Fatalf("HERDR OFFLINE is in the label %q, which goldens mask: they wouldn't show the state", *label.Text)
	}
	wantRows(t, in, rows...)
	in.WaitText("!1 *0 >1", time.Second)
	in.MatchDeskGolden("herdr_offline")

	b.StartHerdr()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if _, ok := offlineLabel(in); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("HERDR OFFLINE still shown 5 s after fakeherdr came back")
		}
	}
	wantRows(t, in, rows...)
}
