package e2e

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devsim"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
)

// writeDevices replaces devices.toml at path as `devices revoke` and
// `provision` do: a new file, renamed over the old one.
func writeDevices(t *testing.T, path string, devs map[string]ssh.Signer, names ...string) {
	t.Helper()
	var b strings.Builder
	for _, name := range names {
		line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(devs[name].PublicKey())))
		fmt.Fprintf(&b, "[[device]]\nname  = %q\nkey   = %q\nadded = 2026-10-22T10:00:00Z\n\n", name, line)
	}
	tmp := filepath.Join(filepath.Dir(path), ".devices.toml.tmp")
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// TestSecondSessionReplacesFirst checks parent 3.6: when a device connects
// again (as after a reboot or a dropped link), its older session is closed;
// the new one works, and another device's session is untouched.
func TestSecondSessionReplacesFirst(t *testing.T) {
	b := startBridge(t, nil)
	key := b.pair("pager-01")
	other := b.connect("pager-02")
	recv[*protocol.Snap](t, other)
	older := b.connectKey("pager-01", key)
	recv[*protocol.Snap](t, older)

	newer := b.connectKey("pager-01", key)
	recv[*protocol.Snap](t, newer)
	if !closed(older) {
		t.Error("the older session is still open")
	}
	for i, d := range map[string]*devsim.Device{"the newer session": newer, "the other device": other} {
		if err := d.Send(`{"t":"ping","id":3}`); err != nil {
			t.Fatalf("%s: %v", i, err)
		}
		if pong := recv[*protocol.Pong](t, d); pong.ID != 3 {
			t.Errorf("%s: pong %d", i, pong.ID)
		}
	}
}

// TestSecondSessionOnOneConnectionReplacesFirst checks parent 3.6 within one
// connection: a second session channel from the same key closes the older
// session, as a reconnect does, and the new one works; the connection stays
// up for it (lane e2e's finding 3; the owner's decision, 2026-10-27,
// open-items O-34).
func TestSecondSessionOnOneConnectionReplacesFirst(t *testing.T) {
	b := startBridge(t, nil)
	older := b.connect("pager-01")
	recv[*protocol.Snap](t, older)

	newer, err := older.Another()
	if err != nil {
		t.Fatal(err)
	}
	if err := newer.Send(`{"t":"hello","v":1,"fw":"0.1.0","name":"pager-01"}`); err != nil {
		t.Fatal(err)
	}
	recv[*protocol.BridgeHello](t, newer)
	recv[*protocol.Snap](t, newer)
	if !closed(older) {
		t.Error("the older session on the connection is still open")
	}
	if err := newer.Send(`{"t":"ping","id":3}`); err != nil {
		t.Fatal(err)
	}
	if pong := recv[*protocol.Pong](t, newer); pong.ID != 3 {
		t.Errorf("the newer session: pong %d", pong.ID)
	}
	if !b.waitLog("sshserver: the device opened another session; its older session closed") {
		t.Errorf("the replaced session isn't logged:\n%s", b.log)
	}
}

// TestBrokenDevicesFileClosesEverySession checks that devices.toml fails
// closed while serve runs (the owner's decision, 2026-10-27, open-items O-51):
// a hand edit that leaves it unloadable, here one that also drops pager-01,
// means no devices. Every session closes within 2 s, no key connects, and
// once the file loads again its devices connect as before.
func TestBrokenDevicesFileClosesEverySession(t *testing.T) {
	b := startBridge(t, nil)
	path := b.devicesFromFile()
	keys := map[string]ssh.Signer{"pager-01": newKey(t), "pager-02": newKey(t)}
	writeDevices(t, path, keys, "pager-01", "pager-02")
	one, two := b.connectKey("pager-01", keys["pager-01"]), b.connectKey("pager-02", keys["pager-02"])
	recv[*protocol.Snap](t, one)
	recv[*protocol.Snap](t, two)

	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(keys["pager-02"].PublicKey())))
	broken := fmt.Sprintf("[[device]]\nname  = \"pager-02\"\nkey   = %q\nadded = 2026-10-22T10:00:00Z\n\n[[device]\n", line)
	tmp := filepath.Join(filepath.Dir(path), ".devices.toml.tmp")
	if err := os.WriteFile(tmp, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	open := map[string]*devsim.Device{"pager-01": one, "pager-02": two}
	for waited := time.Duration(0); waited <= 2*time.Second && len(open) > 0; waited += 100 * time.Millisecond {
		for name, d := range open {
			if _, err := d.Recv(20 * time.Millisecond); errors.Is(err, io.EOF) {
				delete(open, name)
			}
		}
		b.clock.Advance(100 * time.Millisecond)
	}
	if len(open) > 0 {
		t.Fatalf("2 s after devices.toml broke, sessions were still open: %v", open)
	}
	if _, err := dialRaw(t, b, "pager", "", ssh.PublicKeys(keys["pager-02"])); err == nil {
		t.Error("pager-02 connected while devices.toml wouldn't load")
	}
	if !strings.Contains(b.log.String(), "devices.toml won't load") {
		t.Errorf("nothing logged that devices.toml won't load:\n%s", b.log.String())
	}

	writeDevices(t, path, keys, "pager-02")
	again := b.connectKey("pager-02", keys["pager-02"])
	recv[*protocol.Snap](t, again)
}

// TestRevokeClosesSession checks parent 12.2 and phase 2 R5.6: rewriting
// devices.toml without a device, as `devices revoke` does, closes that
// device's session within 2 s (the bridge stats the file every 500 ms); the
// other device's session lives on, and the revoked key can't connect again.
func TestRevokeClosesSession(t *testing.T) {
	b := startBridge(t, nil)
	path := b.devicesFromFile()
	keys := map[string]ssh.Signer{"pager-01": newKey(t), "pager-02": newKey(t)}
	writeDevices(t, path, keys, "pager-01", "pager-02")
	revoked, kept := b.connectKey("pager-01", keys["pager-01"]), b.connectKey("pager-02", keys["pager-02"])
	recv[*protocol.Snap](t, revoked)
	recv[*protocol.Snap](t, kept)

	writeDevices(t, path, keys, "pager-02")
	closed := false
	for waited := time.Duration(0); waited <= 2*time.Second && !closed; waited += 100 * time.Millisecond {
		_, err := revoked.Recv(50 * time.Millisecond)
		closed = errors.Is(err, io.EOF)
		if !closed {
			b.clock.Advance(100 * time.Millisecond)
		}
	}
	if !closed {
		t.Fatal("the revoked device's session was still open 2 s after devices.toml changed")
	}
	if err := kept.Send(`{"t":"ping","id":1}`); err != nil {
		t.Fatal(err)
	}
	if pong := recv[*protocol.Pong](t, kept); pong.ID != 1 {
		t.Errorf("the other device's ping got pong %d", pong.ID)
	}
	if _, err := dialRaw(t, b, "pager", "", ssh.PublicKeys(keys["pager-01"])); err == nil {
		t.Error("the revoked key connected again")
	}
}
