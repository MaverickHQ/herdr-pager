package emutest

import (
	"slices"
	"testing"
	"time"
)

// TestDeskDangerCardNeedsTwoSeconds: the card of an agent blocked on phase
// 0's danger prompt opens with the danger rule's banner, DANGER: <label>,
// inverted, as the body's first row, above the command (parent 18.3), and
// HOLD 2S: over APPROVE under BOOT. A 1.5 s hold sends nothing and leaves the
// card open; a hold past 2 s approves (parent 18.4). Golden: the card (19.4).
func TestDeskDangerCardNeedsTwoSeconds(t *testing.T) {
	danger, _ := claudePrompt(t, "danger")
	b := StartBridge(t, deskScenario(t, deskAgent{pane: "w1:p1", name: "release", status: "blocked", prompt: "danger"}))
	in := deskPaired(t, b, "!1 *0 >0", true)
	openCard(t, in, "DENY")
	banner := "DANGER: " + danger.Label
	wantBody(t, in, banner, danger.Command, danger.Title)
	var first Node
	in.Tree().Walk(func(n Node) {
		if n.Visible && n.Text != nil && *n.Text == banner {
			first = n
		}
	})
	if first.Y < bodyY || first.Y+first.H > bodyY+rowH || first.BgColor != fgColour {
		t.Fatalf("the banner is at y %d..%d on %q; want it inverted (on %s), in the body's first row (y %d..%d)",
			first.Y, first.Y+first.H, first.BgColor, fgColour, bodyY, bodyY+rowH)
	}
	in.HintCell("HOLD 2S:\nAPPROVE")
	in.MatchDeskGolden("card_danger")

	in.Press("boot", 1500)
	time.Sleep(time.Second)
	if sent := b.SentKeys(); len(sent) != 0 {
		t.Fatalf("a 1.5 s hold on a danger card sent %+v; want nothing", sent)
	}
	in.HintCell("HOLD 2S:\nAPPROVE") // still the card, with no verdict sent

	in.Press("boot", 2300)
	sent := b.WaitSentKeys(1, 5*time.Second)
	want := claudeKeys(t, "approve")
	if len(sent) != 1 || sent[0].Target != "w1:p1" || !slices.Equal(sent[0].Keys, want) {
		t.Fatalf("fakeherdr's send_keys: %+v; want one, to w1:p1, of %q", sent, want)
	}
	in.WaitText("OK", 2*time.Second)
}
