package emutest

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// batteryLabel returns the visible label that says ON BATTERY, if any.
func batteryLabel(in *Instance) (label Node, ok bool) {
	in.Tree().Walk(func(n Node) {
		if !ok && n.Visible && n.Text != nil && strings.Contains(*n.Text, "ON BATTERY") {
			label, ok = n, true
		}
	})
	return label, ok
}

// waitNoBattery waits until no label says ON BATTERY.
func waitNoBattery(t *testing.T, in *Instance, timeout time.Duration, why string) {
	t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(100 * time.Millisecond) {
		if _, ok := batteryLabel(in); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ON BATTERY still shown %v after %s", timeout, why)
		}
	}
}

// TestDeskOnBatteryFlag: the kit's power source reaches the band (R1.1). An
// instance run with --battery shows ON BATTERY once it's paired, unmasked in
// goldens (it's a state, not the time); on USB the flag goes, and it comes
// back when the source changes to the battery again. Golden (19.4).
func TestDeskOnBatteryFlag(t *testing.T) {
	b := StartBridge(t, deskScenario(t, deskAgent{pane: "w1:p1", name: "api-refactor", status: "working"}))
	in := pairedOn(t, b, Options{Battery: true})
	in.WaitText("ALL CLEAR >1", 10*time.Second)
	in.WaitText("ON BATTERY", 5*time.Second)
	label, _ := batteryLabel(in)
	if slices.Contains(label.Tags, MaskTag) {
		t.Fatalf("ON BATTERY is in the label %q, which goldens mask: they wouldn't show the state", *label.Text)
	}
	in.WaitIdle(1000, 10*time.Second)
	in.MatchDeskGolden("on_battery")

	in.SetState("source=usb")
	waitNoBattery(t, in, 5*time.Second, "the source changed to USB")
	wantRows(t, in, "[sel] > api-refactor claude")

	in.SetState("source=battery")
	in.WaitText("ON BATTERY", 5*time.Second)
}
