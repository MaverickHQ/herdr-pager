package emutest

import (
	"testing"
	"time"
)

// The walking skeleton for S2-7: a fresh, unpaired instance boots through the
// app core and the action executor to the boot screen (parent 2.1).
func TestHarnessBootUnpairedShowsPairViaUSB(t *testing.T) {
	in := Start(t, Options{})
	in.WaitText("PAIR VIA USB", 5*time.Second)
	in.MatchGolden("pairing/boot_unpaired.png")
	in.Lint()
}
