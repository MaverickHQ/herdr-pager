package emutest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestDeskUnreadCardDeniesOnly: the card of an agent blocked on phase 0's
// shell prompt with a ╌ in its command, which the keymap can't read, fails
// closed (the owner's decision, 2026-11-01; the security pass): COMMAND NOT
// READ first, DENY under PWR, and APPROVE: over KEYBOARD under BOOT. A BOOT
// hold past 2 s sends nothing and leaves the card open; PWR denies, and
// fakeherdr gets the keymap's deny keys. Golden: the card (19.4), linted.
func TestDeskUnreadCardDeniesOnly(t *testing.T) {
	shell, file := claudePrompt(t, "shell")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	cut := shell.Command[:len(shell.Command)/2] + "╌" + shell.Command[len(shell.Command)/2:]
	screen := strings.Replace(string(raw), "\n "+shell.Command+"\n", "\n "+cut+"\n", 1)
	if screen == string(raw) {
		t.Fatalf("the shell capture has no line %q", shell.Command)
	}
	unread := filepath.Join(t.TempDir(), "shell-unread.txt")
	if err := os.WriteFile(unread, []byte(screen), 0o600); err != nil {
		t.Fatal(err)
	}

	b := StartBridge(t, deskScenario(t, deskAgent{pane: "w1:p1", name: "api-refactor", status: "blocked", detection: unread}))
	in := deskPaired(t, b, "!1 *0 >0", true)
	openCard(t, in, "DENY")
	wantBody(t, in, "COMMAND NOT READ", shell.Title)
	in.HintCell("APPROVE:\nKEYBOARD")
	in.MatchDeskGolden("card_unread")

	in.Press("boot", 2500)
	time.Sleep(time.Second)
	if sent := b.SentKeys(); len(sent) != 0 {
		t.Fatalf("a 2.5 s hold on a card that failed closed sent %+v; want nothing", sent)
	}
	in.HintCell("APPROVE:\nKEYBOARD") // still the card

	in.Press("pwr", 100)
	sent := b.WaitSentKeys(1, 5*time.Second)
	want := claudeKeys(t, "deny")
	if len(sent) != 1 || sent[0].Target != "w1:p1" || !slices.Equal(sent[0].Keys, want) {
		t.Fatalf("fakeherdr's send_keys: %+v; want one, to w1:p1, of %q", sent, want)
	}
}
