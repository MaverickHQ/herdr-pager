package setup

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
)

// TestPairingReplacesANameTakenByAnotherKey is the owner's checkpoint 7.2
// (2026-11-07), item 3: pairing under a name already paired with another key
// showed the CLI's words (`pager-bridge devices revoke`, `--name`). The page
// gets the clash as data, in its own words, and can replace that device:
// its old key is revoked only when the page sends that key's fingerprint
// back (after the person confirms), and only if it's still that key.
func TestPairingReplacesANameTakenByAnotherKey(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	oldKey, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	oldFP := ssh.FingerprintSHA256(oldKey)

	clash := newFakePager(t, "to-tool/valid/prov_result-ok.txt")
	pager := newFakePager(t, "to-tool/valid/prov_result-ok.txt")
	p := newPairPage(t, clash.tty, pager.tty)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(oldKey)))
	if err := devices.Add(p.devices, devices.Device{Name: "pager-01", Key: line, PublicKey: oldKey, Added: p.clock.Now()}); err != nil {
		t.Fatal(err)
	}

	status, v := p.post("/api/pair", map[string]string{"port": clash.tty, "name": "pager-01"})
	if status != http.StatusConflict || field(v, "taken.name") != "pager-01" || field(v, "taken.fingerprint") != oldFP {
		t.Fatalf("POST /api/pair with a taken name: %d %v; want 409 with taken {pager-01, %s}", status, v, oldFP)
	}
	msg := field(v, "error")
	for _, cli := range []string{"--name", "pager-bridge devices revoke", "devices.toml"} {
		if strings.Contains(msg, cli) {
			t.Errorf("POST /api/pair with a taken name says %q: the CLI's words (%q), not the page's", msg, cli)
		}
	}
	if !strings.Contains(msg, "Replace") || !strings.Contains(msg, "another name") {
		t.Errorf("POST /api/pair with a taken name says %q; want it to offer Replace or another name", msg)
	}
	clash.nothingSent(t, "after a taken name")

	// A replace naming a key that isn't pager-01's revokes nothing.
	status, v = p.post("/api/pair", map[string]string{"port": pager.tty, "name": "pager-01", "replace": pagerFP})
	if status != http.StatusConflict {
		t.Errorf("POST /api/pair replacing another fingerprint: %d %v; want 409", status, v)
	}
	if devs, err := devices.Load(p.devices); err != nil || len(devs) != 1 || devs[0].Fingerprint() != oldFP {
		t.Fatalf("devices.toml after a replace of the wrong key: %+v (%v); want pager-01's old key kept", devs, err)
	}

	// Replace, confirmed: the old key goes, and the pager pairs as pager-01.
	status, v = p.post("/api/pair", map[string]string{"port": pager.tty, "name": "pager-01", "replace": oldFP})
	if status != http.StatusOK || field(v, "pager.fingerprint") != pagerFP {
		t.Fatalf("POST /api/pair replacing pager-01: %d %v; want 200 and the pager's fingerprint", status, v)
	}
	p.tab.step(t, "Replaced pager-01", oldFP)
	p.tab.step(t, "The pager confirmed with BOOT")
	if status, v := p.confirm(pagerFP, pageNets); status != http.StatusOK || field(v, "paired") != "pager-01" {
		t.Fatalf("POST /api/pair/confirm: %d %v; want 200, paired pager-01", status, v)
	}
	if devs, err := devices.Load(p.devices); err != nil || len(devs) != 1 || devs[0].Name != "pager-01" || devs[0].Fingerprint() != pagerFP {
		t.Fatalf("devices.toml holds %+v (%v); want only pager-01 with %s", devs, err, pagerFP)
	}
}
