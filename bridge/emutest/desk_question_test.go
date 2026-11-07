package emutest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/sanitize"
)

// questionFirst is a question card's first line (parent 7.7).
const questionFirst = "QUESTION - ANSWER AT KEYBOARD"

// wantNoVerdict fails t if a hint cell offers a verdict, or BOOT's fills.
func wantNoVerdict(t *testing.T, tree Node) {
	t.Helper()
	hints := tree.Hints()
	if len(hints) != 2 {
		t.Fatalf("the band has %d hint cells, want 2", len(hints))
	}
	for _, h := range hints {
		if strings.Contains(h.Text, "APPROVE") || strings.Contains(h.Text, "DENY") || h.Fill.W != 0 {
			t.Fatalf("a question card's hint cell says %q with a fill of %d px; want no verdict offered", h.Text, h.Fill.W)
		}
	}
}

// TestDeskQuestionCardRefusesVerdict: the card of an agent blocked on phase
// 0's question prompt says QUESTION - ANSWER AT KEYBOARD first and offers no
// approve or deny (parent 7.7), only jump (R6.3): no hint cell names a
// verdict; a BOOT hold past the approve threshold doesn't fill BOOT's cell
// and sends nothing; PWR goes back to the list instead of denying; and a
// longer hold opens the agent's menu, whose first tile, JUMP, focuses the
// agent's pane. fakeherdr gets no send_keys at all. Golden: the card (19.4).
func TestDeskQuestionCardRefusesVerdict(t *testing.T) {
	q, _ := claudePrompt(t, "question")
	b := StartBridge(t, deskScenario(t, deskAgent{pane: "w1:p1", name: "palette", status: "blocked", prompt: "question"}))
	in := deskPaired(t, b, "!1 *0 >0", true)
	openCard(t, in, questionFirst)
	wantBody(t, in, questionFirst, sanitize.Text(q.Title, sanitize.Line)) // the summary, as the bridge sends it
	wantNoVerdict(t, in.Tree())
	in.MatchDeskGolden("card_question")

	// Past the approve threshold, short of the menu's 1.5 s.
	h := in.Hold("boot")
	time.Sleep(1150 * time.Millisecond)
	snap := h.Snap()
	h.Release()
	wantNoVerdict(t, snap.Tree)
	time.Sleep(time.Second)
	wantBody(t, in, questionFirst) // still the card

	in.Press("pwr", 100) // back, not deny
	in.WaitText("OPEN", 5*time.Second)

	openCard(t, in, questionFirst)
	in.Press("boot", 2300) // the agent's menu opens at 1.5 s, and the hold ends there
	in.WaitText("SELECT", 5*time.Second)
	in.Press("pwr", 100) // its first tile: JUMP
	for deadline := time.Now().Add(5 * time.Second); len(b.Requests(herdr.MethodFocus)) == 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no agent.focus from JUMP; fakeherdr's log:\n%s", b.HerdrLog)
		}
	}
	var focus herdr.Target
	if err := json.Unmarshal(b.Requests(herdr.MethodFocus)[0].Params, &focus); err != nil || focus.Target != "w1:p1" {
		t.Fatalf("agent.focus %s (%v); want the target w1:p1", b.Requests(herdr.MethodFocus)[0].Params, err)
	}
	if sent := b.SentKeys(); len(sent) != 0 {
		t.Fatalf("a question card sent %+v; want nothing", sent)
	}
}
