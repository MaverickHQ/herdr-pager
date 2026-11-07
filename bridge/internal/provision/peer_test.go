package provision

import (
	"os"
	"testing"

	"github.com/creack/pty"
)

// openPeer opens a pseudo-terminal pair for a fake serial peer (seam S2-5):
// the test plays the device on peer, and provision opens tty by its name as
// the serial port. The tty starts raw, as a serial port does: nothing the
// peer writes is echoed back to it before provision opens the port.
func openPeer(t *testing.T) (peer *os.File, tty string) {
	t.Helper()
	p, tt, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		tt.Close()
		p.Close()
	})
	if _, err := makeRaw(int(tt.Fd())); err != nil {
		t.Fatal(err)
	}
	return p, tt.Name()
}
