package emutest

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/version"
)

// openMenu holds BOOT on the list past 1.5 s: the selected agent's menu (parent 9.1).
func openMenu(t *testing.T, in *Instance) {
	t.Helper()
	in.Press("boot", 1700)
	in.WaitText("SELECT", 5*time.Second)
}

// press presses button briefly, then waits for the menu's tiles to be want.
func press(t *testing.T, in *Instance, button string, want ...string) {
	t.Helper()
	in.Press(button, 100)
	in.WaitTiles(5*time.Second, want...)
}

// TestDeskMenusAndInterruptConfirm: on the list, a 1.5 s BOOT hold opens the
// selected agent's menu, a strip of tiles with the first inverted (parent
// 9.1; parent design §UI); BOOT steps, PWR selects, a 1.5 s hold closes it
// (9.2). A working agent's menu offers INTERRUPT, which asks INTERRUPT
// <name>? with YES in the PWR cell and NO in the BOOT cell: BOOT sends
// nothing, PWR sends the keymap's interrupt keys (9.5). SETTINGS opens the
// global menu, whose fifth to seventh tiles scroll into view, and DEVICE INFO shows the
// name, the firmware, both full fingerprints and the IP (9.6). Goldens: the
// agent menu, the confirm, the global menu, scrolled, and device info (19.4).
func TestDeskMenusAndInterruptConfirm(t *testing.T) {
	b := StartBridge(t, deskScenario(t, deskAgent{pane: "w1:p1", name: "api-refactor", status: "working"}))
	in, devFP := pairEmulator(t, b, "")
	in.WaitText("o lcd349-emu", 60*time.Second)
	in.WaitText("ALL CLEAR >1", 10*time.Second)
	in.WaitText("OPEN", 5*time.Second)

	openMenu(t, in)
	in.WaitTiles(5*time.Second, "[sel] JUMP", "INTERRUPT", "SETTINGS")
	in.HintCell("SELECT")
	in.HintCell("NEXT\nHOLD: EXIT")
	in.MatchDeskGolden("menu_agent")
	in.Press("boot", 1700) // closes it
	in.WaitText("OPEN", 5*time.Second)

	// The interrupt confirm: BOOT says no.
	openMenu(t, in)
	press(t, in, "boot", "JUMP", "[sel] INTERRUPT", "SETTINGS")
	in.Press("pwr", 100)
	in.WaitText("INTERRUPT api-refactor?", 5*time.Second)
	if h := in.Tree().Hints(); len(h) != 2 || h[0].Text != "YES" || h[1].Text != "NO" {
		t.Fatalf("the confirm's hint cells, PWR then BOOT: %+v; want YES, then NO", h)
	}
	in.MatchDeskGolden("confirm_interrupt")
	in.Press("boot", 100)
	in.WaitText("OPEN", 5*time.Second)
	if sent := b.SentKeys(); len(sent) != 0 {
		t.Fatalf("NO sent %+v; want nothing", sent)
	}

	// PWR says yes: the keymap's interrupt keys, to the agent's pane.
	openMenu(t, in)
	press(t, in, "boot", "JUMP", "[sel] INTERRUPT", "SETTINGS")
	in.Press("pwr", 100)
	in.WaitText("INTERRUPT api-refactor?", 5*time.Second)
	in.Press("pwr", 100)
	sent := b.WaitSentKeys(1, 5*time.Second)
	want := claudeKeys(t, "interrupt")
	if len(sent) != 1 || sent[0].Target != "w1:p1" || !slices.Equal(sent[0].Keys, want) {
		t.Fatalf("fakeherdr's send_keys: %+v; want one, to w1:p1, of %q", sent, want)
	}
	in.WaitText("OK", 2*time.Second)
	in.WaitText("OPEN", 5*time.Second)

	// The global menu, from the agent's SETTINGS; its fifth to seventh tiles
	// scroll into view.
	openMenu(t, in)
	press(t, in, "boot", "JUMP", "[sel] INTERRUPT", "SETTINGS")
	press(t, in, "boot", "JUMP", "INTERRUPT", "[sel] SETTINGS")
	press(t, in, "pwr", "[sel] SILENT", "VOLUME", "DONE TONE", "BRIGHTNESS")
	in.MatchDeskGolden("menu_global")
	press(t, in, "boot", "SILENT", "[sel] VOLUME", "DONE TONE", "BRIGHTNESS")
	press(t, in, "boot", "SILENT", "VOLUME", "[sel] DONE TONE", "BRIGHTNESS")
	// DONE TONE turns the done tone off and on (the acceptance day,
	// 2026-10-25).
	in.Press("pwr", 100)
	in.WaitText("DONE TONE OFF", 5*time.Second)
	in.Press("pwr", 100)
	in.WaitText("DONE TONE ON", 5*time.Second)
	press(t, in, "boot", "SILENT", "VOLUME", "DONE TONE", "[sel] BRIGHTNESS")
	press(t, in, "boot", "VOLUME", "DONE TONE", "BRIGHTNESS", "[sel] COLOUR")
	// COLOUR switches the palette at once, to the backlit green and back
	// (the owner's choice at the desk checkpoint, 2026-10-24); each passes
	// the kit's contrast rule.
	in.Press("pwr", 100)
	in.WaitText("COLOUR GREEN", 5*time.Second)
	in.MatchDeskGolden("menu_global_green")
	in.Press("pwr", 100)
	in.WaitText("COLOUR ORANGE", 5*time.Second)
	in.Lint()
	press(t, in, "boot", "DONE TONE", "BRIGHTNESS", "COLOUR", "[sel] DEVICE INFO")
	press(t, in, "boot", "BRIGHTNESS", "COLOUR", "DEVICE INFO", "[sel] PAIR AGAIN")
	in.MatchDeskGolden("menu_global_scrolled")
	press(t, in, "boot", "[sel] SILENT", "VOLUME", "DONE TONE", "BRIGHTNESS")
	press(t, in, "boot", "SILENT", "[sel] VOLUME", "DONE TONE", "BRIGHTNESS")
	press(t, in, "boot", "SILENT", "VOLUME", "[sel] DONE TONE", "BRIGHTNESS")
	press(t, in, "boot", "SILENT", "VOLUME", "DONE TONE", "[sel] BRIGHTNESS")
	press(t, in, "boot", "VOLUME", "DONE TONE", "BRIGHTNESS", "[sel] COLOUR")
	press(t, in, "boot", "DONE TONE", "BRIGHTNESS", "COLOUR", "[sel] DEVICE INFO")

	// Device info: the fingerprints in full, as provision printed them.
	in.Press("pwr", 100)
	in.WaitText("DEV "+devFP, 5*time.Second)
	hostFP := ssh.FingerprintSHA256(b.HostKey)
	in.WaitText("HOST "+hostFP, 5*time.Second)
	// The kit's emulator gives the station the fixed address 10.0.2.15; the
	// pager restarted after pairing, and dials the bridge's port (O-77).
	in.WaitText("IP 10.0.2.15  BRIDGE 127.0.0.1:"+strconv.Itoa(b.Port), 5*time.Second)
	// The emulator's build isn't a published version (phase 5 task 1.3); the fingerprints and the port are masked.
	wantBody(t, in, "emu-01", "FW "+version.Number()+"-dev  STARTED: RESTART")
	in.HintCell("BACK")
	in.MatchDeskGolden("info")
	in.Press("pwr", 100)
	in.WaitText("OPEN", 5*time.Second)
	if sent := b.SentKeys(); len(sent) != 1 {
		t.Fatalf("fakeherdr got %d send_keys, want the interrupt's 1", len(sent))
	}
}
