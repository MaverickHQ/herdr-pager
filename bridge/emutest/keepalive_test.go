package emutest

import (
	"strings"
	"testing"
	"time"
)

// The link's two timers (parent design §SSH link; net/ssh_link.c): the pager
// pings the bridge every 60 s, and drops a link that has sent it nothing for
// 150 s. Both run in real time, so these tests take minutes; they run in
// parallel, after the package's other tests.

// TestPingKeepsTheSessionAlive: the pager's ping keeps its session open past a
// bridge idle_timeout of 75 s: no idle close, and still the first dial
// (open-items O-10). About 90 s.
func TestPingKeepsTheSessionAlive(t *testing.T) {
	t.Parallel()
	b := StartBridgeWith(t, quietScenario, `idle_timeout = "75s"`)
	in := paired(t, b)
	// Past the idle timeout: nothing pages, so the ping, at 60 s, is the pager's only line since hello.
	time.Sleep(80 * time.Second)
	if strings.Contains(b.Log.String(), "session: idle") {
		t.Fatalf("the bridge closed the session as idle:\n%s", b.Log)
	}
	if n := in.Count(`dialling the bridge`); n != 1 {
		t.Fatalf("the pager dialled the bridge %d times; want the first only", n)
	}
}

// TestSilentBridgeIsDropped: a bridge that stops answering, its connection
// still open (serve paused), is dropped once 150 s pass without a byte from
// it; when serve runs again, the pager dials it back (open-items O-10). About
// 3 minutes.
func TestSilentBridgeIsDropped(t *testing.T) {
	t.Parallel()
	b := StartBridge(t, quietScenario)
	in := paired(t, b)
	b.PauseServe() // the snapshot, just now, is the last the pager hears
	paused := time.Now()
	waitCount(t, in, `silent for 150 s: dropping the link`, 1, 170*time.Second)
	after := time.Since(paused)
	t.Logf("the pager dropped the link %.1f s after serve stopped answering", after.Seconds())
	if after < 135*time.Second {
		t.Fatalf("the pager dropped the link %.1f s after the bridge went silent; want about 150", after.Seconds())
	}
	b.ResumeServe()
	waitCount(t, in, `dialling the bridge`, 2, maxBackoff)
	// The screen has been dark since 30 s in, and isn't redrawn while it is: a press wakes it (and does
	// nothing else), and the band it draws shows the link as it is now.
	in.Press("pwr", 100)
	in.WaitText("o lcd349-emu", 15*time.Second)
}

// waitCount waits until the instance's serial log has n lines matching re.
// Unlike WaitLog, a long wait isn't tried twice.
func waitCount(t *testing.T, in *Instance, re string, n int, timeout time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(timeout); in.Count(re) < n; time.Sleep(time.Second) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in the serial log within %s", re, timeout)
		}
	}
}
