package emutest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/sanitize"
)

// TestDeskMCPCard: the card of an agent blocked on an MCP call (the
// recon's capture, O-90) with three arguments in place of its one shows
// every one (the owner's decision, 2026-11-02, O-90's review): the server
// and tool first, in the body font, then the prompt box's title, then each
// argument on a line of its own, its name whole and a long value cut in the
// middle. The card uses the protocol's 4 lines, and the pager draws them
// all. HOLD: over APPROVE under BOOT. Golden: the card (19.4), linted.
func TestDeskMCPCard(t *testing.T) {
	mcp, file := claudePrompt(t, "mcp")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{`owner: "octo"`, `repo: "herdr-pager"`, "title: " + `"Fix the flaky emulator test on the CI runner"`}
	screen := strings.Replace(string(raw), "\n "+mcp.Args+"\n", "\n "+strings.Join(args, "\n ")+"\n", 1)
	if screen == string(raw) {
		t.Fatalf("the MCP capture has no line %q", mcp.Args)
	}
	call := filepath.Join(t.TempDir(), "mcp-three-arguments.txt")
	if err := os.WriteFile(call, []byte(screen), 0o600); err != nil {
		t.Fatal(err)
	}

	b := StartBridge(t, deskScenario(t, deskAgent{pane: "w1:p1", name: "api-refactor", status: "blocked", detection: call}))
	in := deskPaired(t, b, "!1 *0 >0", true)
	openCard(t, in, "DENY")
	long := `"Fix the flaky emulator test on the CI runner"`
	wantBody(t, in, "recon - Echo Tool", mcp.Title, `owner: "octo"`, `repo: "herdr-pager"`, "title: "+sanitize.Text(long, sanitize.Line-len("title: ")))
	in.HintCell("HOLD:\nAPPROVE")
	in.MatchDeskGolden("card_mcp")
}

// TestDeskMCPDangerCard: the tallest card (O-90's second review): an MCP
// call of three arguments, one a forced reset, so the danger banner, the
// server and tool, the title and the three arguments fill the body's 144 px
// (34 + 30 + 4 x 20). Every line shows, the last within the body; HOLD 2S:
// over APPROVE under BOOT. Golden: the card (19.4), linted.
func TestDeskMCPDangerCard(t *testing.T) {
	mcp, file := claudePrompt(t, "mcp")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{`owner: "octo"`, `repo: "herdr-pager"`, `command: "git reset --hard origin/main"`}
	screen := strings.Replace(string(raw), "\n "+mcp.Args+"\n", "\n "+strings.Join(args, "\n ")+"\n", 1)
	if screen == string(raw) {
		t.Fatalf("the MCP capture has no line %q", mcp.Args)
	}
	call := filepath.Join(t.TempDir(), "mcp-danger.txt")
	if err := os.WriteFile(call, []byte(screen), 0o600); err != nil {
		t.Fatal(err)
	}

	b := StartBridge(t, deskScenario(t, deskAgent{pane: "w1:p1", name: "api-refactor", status: "blocked", detection: call}))
	in := deskPaired(t, b, "!1 *0 >0", true)
	openCard(t, in, "DENY")
	wantBody(t, in, "DANGER: RESET HARD", "recon - Echo Tool", mcp.Title, `owner: "octo"`, `repo: "herdr-pager"`, `command: "git reset --hard origin/main"`)
	var last Node
	in.Tree().Walk(func(n Node) {
		if n.Visible && n.Text != nil && *n.Text == `command: "git reset --hard origin/main"` {
			last = n
		}
	})
	if last.H == 0 || last.Y+last.H > bodyY+144 {
		t.Fatalf("the last argument is at y %d..%d; want it within the body (y %d..%d)", last.Y, last.Y+last.H, bodyY, bodyY+144)
	}
	in.HintCell("HOLD 2S:\nAPPROVE")
	in.MatchDeskGolden("card_mcp_danger")
}
