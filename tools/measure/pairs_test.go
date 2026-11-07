package measure

import (
	"strings"
	"testing"
	"time"
)

// quietScenario is fakeherdr's: one working agent, which pages nothing.
const quietScenario = `
[[agent]]
pane = "w1:p1"
kind = "claude"
name = "api-refactor"
status = "working"
`

// pairBoard flashes the test build onto the erased board, so it boots
// unpaired, and pairs it as board-01 with a new bridge (fakeherdr running
// scenario). The fixed bridge's provision runs through the harness's pty,
// with test-wifi.toml's networks and the Mac's .local name (provision's
// default host); an @T press of BOOT confirms, as the owner's press would.
// Serve starts once provision has paired it; pairBoard returns when the
// board has restarted paired, and when provision ended. The erased board
// makes its device key on its first boot, and only once its radio is on
// (parent design §Provisioning, T12): its log shows that order.
func pairBoard(t *testing.T, name, scenario string) (*Bridge, *Serial, time.Time) {
	t.Helper()
	port := Port(t)
	nets := Wifi(t)
	FlashErased(t, Test, port)
	b := NewBridge(t, scenario, nets)
	s := Open(t, port, name)
	s.Wait(`prov: device key: the radio is on`, 30*time.Second)
	s.Wait(`prov: new device key: stored`, 10*time.Second)
	s.Wait(`pump: boot: unpaired`, 10*time.Second)

	p := b.Provision(s.Proxy(), "board-01")
	dev, host := p.Fingerprints(60 * time.Second)
	t.Logf("provision: the pager's key %s, the bridge's %s", dev, host)
	time.Sleep(time.Second) // the confirm screen is up
	s.Send("@T press BOOT 100")
	if err := p.Wait(60 * time.Second); err != nil || !strings.Contains(p.Out.String(), "Paired board-01.") {
		t.Fatalf("provision: %v\n%s", err, p.Output())
	}
	s.EndProxy()
	paired := time.Now()
	b.StartServe()
	s.Wait(`pump: boot: paired`, 60*time.Second)
	return b, s, paired
}

// TestBoardPairsAndConnects is the board check board_pairs_and_connects
// (task 10.5; parent 2.3-2.6, 2.9, 3.1): the board pairs over USB
// (pairBoard), restarts, joins the Wi-Fi, dials the bridge on the Mac, and
// its log shows the list (the bridge's snapshot) arriving.
func TestBoardPairsAndConnects(t *testing.T) {
	Require(t)
	b, s, paired := pairBoard(t, "pairs_and_connects", quietScenario)
	connected := b.WaitConnected("board-01", 90*time.Second)
	t.Logf("the bridge saw board-01 connected %.1f s after provision ended", connected.Sub(paired).Seconds())
	snap := s.Wait(`@L \d+ line snap`, 15*time.Second)
	t.Logf("the list reached the board: %s", snap.Text)
}
