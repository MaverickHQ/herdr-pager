package emutest

import (
	"testing"
	"time"
)

// The states of the firmware's screens that no other scenario lints (task
// 9.10, parent 19.4, 19.5): every screen is linted in at least one state
// elsewhere; these are the rest.

// TestDeskNoAgents: on a live link with no agents, the list says NO AGENTS.
// Golden (19.4).
func TestDeskNoAgents(t *testing.T) {
	b := StartBridge(t, "# no agents\n")
	in := deskPaired(t, b, "!0 *0 >0", false)
	in.WaitText("NO AGENTS", 5*time.Second)
	in.MatchDeskGolden("list_empty")
}

// TestDeskStatusCardPairAgainAndLinkDown: a working agent's card is a status
// card, with no verdicts (parent 7.1): the bridge's status card carries no
// summary or lines, so the pager says NOTHING NEEDS YOU, what the agent is doing
// and its kind, with BACK and HOLD: MENU (open-items O-82); the global menu's PAIR AGAIN asks
// PAIR AGAIN?, YES in BOOT's cell and NO in PWR's, and PWR keeps the pairing
// (parent 2.10); with serve stopped, the list stays, its band saying how old
// it is (parent 16.4). Goldens: the status card and the confirm; each linted.
func TestDeskStatusCardPairAgainAndLinkDown(t *testing.T) {
	b := StartBridge(t, deskScenario(t, deskAgent{pane: "w1:p1", name: "api-refactor", status: "working"}))
	in := deskPaired(t, b, "ALL CLEAR >1", false)

	in.Press("pwr", 100)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if _, _, list := in.Tree().HintCell("OPEN"); !list {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no card 5 s after PWR")
		}
	}
	in.WaitIdle(500, 5*time.Second)
	wantNoVerdict(t, in.Tree())
	in.MatchDeskGolden("card_status")
	in.Press("pwr", 100)
	in.WaitText("OPEN", 5*time.Second)

	openMenu(t, in)
	in.WaitTiles(5*time.Second, "[sel] JUMP", "INTERRUPT", "SETTINGS")
	press(t, in, "boot", "JUMP", "[sel] INTERRUPT", "SETTINGS")
	press(t, in, "boot", "JUMP", "INTERRUPT", "[sel] SETTINGS")
	press(t, in, "pwr", "[sel] SILENT", "VOLUME", "DONE TONE", "BRIGHTNESS")
	for i := 0; i < 6; i++ {
		in.Press("boot", 100)
	}
	in.WaitTiles(5*time.Second, "BRIGHTNESS", "COLOUR", "DEVICE INFO", "[sel] PAIR AGAIN")
	in.Press("pwr", 100)
	in.WaitText("PAIR AGAIN?", 5*time.Second)
	if h := in.Tree().Hints(); len(h) != 2 || h[0].Text != "NO" || h[1].Text != "YES" {
		t.Fatalf("the confirm's hint cells, PWR then BOOT: %+v; want NO, then YES", h)
	}
	in.MatchDeskGolden("confirm_pair_again")
	in.Press("pwr", 100)
	in.WaitText("OPEN", 5*time.Second)
	wantRows(t, in, "[sel] > api-refactor claude")

	b.StopServe()
	in.WaitText("LAST SEEN", 15*time.Second)
	wantRows(t, in, "[sel] > api-refactor claude")
	in.Lint()
}
