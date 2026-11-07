package e2e

import (
	"slices"
	"strings"
	"testing"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/captures"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
)

// TestCardLinesFollowTheHello checks O-90's second review: the bridge sends a
// card of more than 3 lines only to a pager whose hello says it draws them
// (`"lines":4`, the protocol fixtures' hello). The card of an MCP call with 3
// arguments is 4 lines, and offers approve, to such a pager; to one whose
// hello has no lines (hello-three-lines.ndjson: a pager from before), it
// fails closed in 3 lines, TOOL NOT READ, deny and jump only.
func TestCardLinesFollowTheHello(t *testing.T) {
	mcp, agent, screen := capture(t, "w1:p1", func(c captures.Capture, _ herdr.AgentInfo) bool { return c.Prompt == "mcp" })
	args := " " + strings.Join([]string{`owner: "octo"`, `repo: "pager"`, `branch: "main"`}, "\n ")
	call := strings.Replace(screen, "\n "+mcp.Args+"\n", "\n"+args+"\n", 1)
	if call == screen {
		t.Fatalf("the MCP capture has no line %q", mcp.Args)
	}
	b := startBridge(t, []herdr.AgentInfo{agent})
	b.herdr.SetDetection(agent.PaneID, call)

	for _, c := range []struct {
		hello string
		lines []string
		can   []string
	}{
		{fixture(t, "to-bridge/valid/hello.ndjson"),
			[]string{"recon - Echo Tool", `owner: "octo"`, `repo: "pager"`, `branch: "main"`}, []string{"approve", "deny", "jump"}},
		{fixture(t, "to-bridge/valid/hello-three-lines.ndjson"),
			[]string{"TOOL NOT READ", "recon - Echo Tool", `owner: "octo"`}, []string{"deny", "jump"}},
	} {
		name := "pager-01"
		d := b.dial(b.pair(name))
		if err := d.Send(c.hello); err != nil {
			t.Fatal(err)
		}
		recv[*protocol.BridgeHello](t, d)
		recv[*protocol.Snap](t, d)
		card := show(t, d, agent.PaneID, 7)
		if !slices.Equal(card.Lines, c.lines) || !slices.Equal(card.Can, c.can) {
			t.Errorf("hello %s: lines %q, can %q; want %q, %q", c.hello, card.Lines, card.Can, c.lines, c.can)
		}
		d.Close()
	}
}
