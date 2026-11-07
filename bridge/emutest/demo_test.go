package emutest

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDemo records the README's animated demo: the real firmware in the
// emulator, against fakeherdr, paired on a made-up Wi-Fi network. It writes
// each screen it passes through to out/demo/, with how long the demo shows it,
// the button that led there and a caption, in reel.json; tools/media/compose.py
// turns them into docs/images/demo.gif and the social preview. It checks
// nothing that the desk tests don't, and runs only with PAGER_DEMO=1 (and
// PAGER_EMUTEST=1):
//
//	cd bridge && PAGER_EMUTEST=1 PAGER_DEMO=1 go test -run TestDemo -count=1 -timeout 10m ./emutest/
func TestDemo(t *testing.T) {
	if os.Getenv("PAGER_DEMO") != "1" {
		t.Skip("set PAGER_DEMO=1 and PAGER_EMUTEST=1 to record the README's demo")
	}
	dir := filepath.Join("out", "demo")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	reel := &demoReel{dir: dir, Buttons: map[string][2]int{}}

	// Four agents at work, then api-refactor blocks on phase 0's shell prompt,
	// and goes back to work once it's approved. fakeherdr's clock starts with
	// the bridge, so the block waits out the pairing.
	_, shell := claudePrompt(t, "shell")
	scenario := deskScenario(t,
		deskAgent{pane: "w1:p1", name: "api-refactor", status: "working"},
		deskAgent{pane: "w1:p2", name: "ci-pipeline", status: "working"},
		deskAgent{pane: "w1:p3", name: "docs-site", status: "idle"},
		deskAgent{pane: "w1:p4", name: "release-notes", status: "done"},
	) + fmt.Sprintf("[[change]]\nat = %q\npane = \"w1:p1\"\nstatus = \"blocked\"\ndetection = %q\n\n", demoBlockAt, shell) +
		fmt.Sprintf("[[change]]\nat = %q\npane = \"w1:p1\"\nstatus = \"working\"\n", demoWorkAt)
	b := StartBridge(t, scenario)
	start := time.Now()

	in := Start(t, Options{})
	in.AirNetworks(demoNetwork)
	in.WaitText("PAIR VIA USB", 10*time.Second)
	p := b.Provision(in.Serial, "pager-01", "127.0.0.1", demoNetwork)
	_, hostFP := p.Fingerprints(30 * time.Second)
	in.WaitText("HOST "+hostFP, 10*time.Second)
	in.PressRestarting("boot", 100)
	if err := p.Wait(30 * time.Second); err != nil {
		t.Fatalf("provision: %v\n%s", err, p.Out)
	}
	in.WaitText("o "+demoNetwork.SSID, 60*time.Second)
	in.WaitText("!0 *1 >2", 10*time.Second)
	in.WaitIdle(500, 5*time.Second)
	if lead, _ := time.ParseDuration(demoBlockAt); time.Since(start) > lead-15*time.Second {
		t.Fatalf("pairing took %v, too close to the block at %s: raise demoBlockAt and demoWorkAt", time.Since(start), demoBlockAt)
	}
	if h := in.Tree().Hints(); len(h) == 2 {
		reel.Buttons["pwr"] = [2]int{h[0].Cell.X, h[0].Cell.W}
		reel.Buttons["boot"] = [2]int{h[1].Cell.X, h[1].Cell.W}
	} else {
		t.Fatalf("the list has %d hint cells, want PWR's and BOOT's", len(h))
	}

	// The list, and BOOT moving down it.
	reel.add(t, in.Shot(), 2600, "", "Every herdr agent on one list, most urgent first")
	for range 2 {
		in.Press("boot", 100)
		in.WaitIdle(300, 5*time.Second)
		reel.add(t, in.Shot(), 1100, "boot", "BOOT moves to the next agent")
	}

	// The page: the blocked agent goes to the top, selected, with a tone. The
	// screen's flash isn't recorded: a shot during it catches a frame half drawn.
	in.WaitText("!1 *1 >1", 60*time.Second)
	in.WaitTone(0, 10*time.Second)
	in.WaitIdle(1000, 10*time.Second)
	reel.add(t, in.Shot(), 3000, "", "An agent needs you: it jumps to the top, and the pager beeps")

	// Its card, and a 1 s BOOT hold that approves it: the hint cell fills.
	openCard(t, in, "DENY")
	reel.add(t, in.Shot(), 3200, "pwr", "PWR opens its card: the agent's own prompt, command first")
	h := in.Hold("boot")
	held := time.Now()
	for _, at := range []time.Duration{250, 500, 750} {
		time.Sleep(time.Until(held.Add(at * time.Millisecond)))
		reel.add(t, h.Snap().Shot, 380, "boot-hold", "Hold BOOT for 1 s to approve; let go early and nothing is sent")
	}
	time.Sleep(time.Until(held.Add(1200 * time.Millisecond)))
	h.Release()
	in.WaitText("OK", 2*time.Second)
	reel.add(t, in.Shot(), 2000, "", "Checked against the agent's prompt, then the keys go to herdr")
	in.WaitText("!0 *1 >2", 60*time.Second)
	in.WaitIdle(500, 5*time.Second)
	reel.add(t, in.Shot(), 1800, "", "And the agent is back at work")

	// The agent's menu, then SETTINGS, the global menu, and COLOUR.
	in.Press("boot", 1700)
	in.WaitTiles(5*time.Second, "[sel] JUMP", "INTERRUPT", "SETTINGS")
	reel.add(t, in.Shot(), 2200, "boot-hold", "Hold BOOT for its menu: jump to its pane, or interrupt it")
	press(t, in, "boot", "JUMP", "[sel] INTERRUPT", "SETTINGS")
	reel.add(t, in.Shot(), 800, "boot", "BOOT: the next item")
	press(t, in, "boot", "JUMP", "INTERRUPT", "[sel] SETTINGS")
	reel.add(t, in.Shot(), 800, "boot", "BOOT: the next item")
	press(t, in, "pwr", "[sel] SILENT", "VOLUME", "DONE TONE", "BRIGHTNESS")
	reel.add(t, in.Shot(), 1600, "pwr", "PWR selects: the settings")
	for _, want := range [][]string{
		{"SILENT", "[sel] VOLUME", "DONE TONE", "BRIGHTNESS"},
		{"SILENT", "VOLUME", "[sel] DONE TONE", "BRIGHTNESS"},
		{"SILENT", "VOLUME", "DONE TONE", "[sel] BRIGHTNESS"},
		{"VOLUME", "DONE TONE", "BRIGHTNESS", "[sel] COLOUR"},
	} {
		press(t, in, "boot", want...)
		reel.add(t, in.Shot(), 450, "boot", "Silent mode, volume, the done tone, brightness, colour")
	}
	in.Press("pwr", 100)
	in.WaitText("COLOUR GREEN", 5*time.Second)
	in.WaitIdle(300, 5*time.Second)
	reel.add(t, in.Shot(), 2200, "pwr", "Two looks: a deep orange, or a backlit green")
	in.Press("boot", 1700)
	in.WaitText("OPEN", 5*time.Second)
	in.WaitIdle(300, 5*time.Second)
	reel.add(t, in.Shot(), 3000, "boot-hold", "The same list in green; hold BOOT again to leave a menu")

	data, err := json.MarshalIndent(reel, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reel.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d frames in %s", len(reel.Frames), dir)
}

// demoNetwork is the made-up network the demo pairs on: its name is in the band.
var demoNetwork = Network{SSID: "HomeNet24", Pass: "demo-pass"}

// When the demo's agent blocks, and goes back to work, after fakeherdr starts.
const demoBlockAt, demoWorkAt = "60s", "80s"

// A demoReel is the demo's screens in order, and where each button's hint cell
// is on screen (x and width), so the compositor can draw the buttons above them.
type demoReel struct {
	dir     string
	Buttons map[string][2]int `json:"buttons"`
	Frames  []demoFrame       `json:"frames"`
}

// A demoFrame is one screen of the demo.
type demoFrame struct {
	File    string `json:"file"`
	MS      int    `json:"ms"`              // how long the demo shows it
	Press   string `json:"press,omitempty"` // the button that led here: boot, pwr or boot-hold
	Caption string `json:"caption"`
}

func (r *demoReel) add(t *testing.T, shot image.Image, ms int, press, caption string) {
	t.Helper()
	name := fmt.Sprintf("%02d.png", len(r.Frames)+1)
	f, err := os.Create(filepath.Join(r.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, shot); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	r.Frames = append(r.Frames, demoFrame{File: name, MS: ms, Press: press, Caption: caption})
}
