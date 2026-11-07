package setup

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/provision"
)

// The pager's log as it restarts after pairing and joins (firmware/main/
// platform/pump.c, and the kit's lcd349_wifi; reason 1 is the kit's
// LCD349_WIFI_REASON_AUTH, 2 its NOT_FOUND).
const (
	logConnecting = "I (412) lcd349_wifi: connecting to saved network 1\r\n"
	logNotJoined  = "W (5412) lcd349_wifi: not connected (reason 1)\r\n"
	logAtOnce     = "I (5413) pump: wifi: join refused; trying again at once\r\n" // a refusal it tries again at once: maybe brief
	logRefused    = "I (9120) pump: wifi: join refused (reason 1); next try in 1000 ms\r\n"
	logNotFound   = "I (3120) pump: wifi: join failed (reason 2); next try in 1000 ms\r\n"
	logJoined     = "I (6020) lcd349_wifi: connected, RSSI -48 dBm\r\n"
)

// TestThePagerSaysWhetherItJoined is the owner's recheck of checkpoint 7.2
// (A), 2026-11-07: a wrong Wi-Fi password paired fine (the password is tried
// only when the pager joins), the pager restarted and couldn't join, and the
// page went on waiting for it. After pairing, POST /api/pair/join reads the
// pager's USB log until it says how its join went, and answers in the page's
// words: joined; a password refused (after the pager's own three refusals at
// once, O-49); no saved network it can see, which is how a 5 GHz-only
// network looks too, so the two are said together (the owner's session,
// 2026-11-07); or nothing yet. The page keeps the networks' names, never
// their passwords, so it can fill them in to change them, and changing them
// is pairing again under the same name: one entry for that key, not two.
func TestThePagerSaysWhetherItJoined(t *testing.T) {
	paired := func(t *testing.T, ports ...string) (*pairPage, *fakePager) {
		t.Helper()
		pager := newFakePager(t, "to-tool/valid/prov_result-ok.txt")
		p := newPairPage(t, append([]string{pager.tty}, ports...)...)
		p.pair(pager.tty)
		p.tab.step(t, "The pager confirmed with BOOT")
		if status, v := p.confirm(pagerFP, pageNets); status != http.StatusOK {
			t.Fatalf("POST /api/pair/confirm: %d %v; want 200", status, v)
		}
		return p, pager
	}
	type want struct {
		result, network string
		says            []string
	}
	check := func(t *testing.T, p *pairPage, status int, v map[string]any, w want) {
		t.Helper()
		if status != http.StatusOK || field(v, "result") != w.result || field(v, "network") != w.network || !containsAll(field(v, "says"), w.says) {
			t.Errorf("POST /api/pair/join: %d %v; want 200, result %q, network %q, saying %q", status, v, w.result, w.network, w.says)
		}
		p.tab.step(t, w.says...)
	}

	for _, c := range []struct {
		name, log string
		want      want
	}{
		{"a wrong password", logConnecting + strings.Repeat(logNotJoined+logAtOnce, 3) + logNotJoined + logRefused,
			want{"refused", "HomeNet24", []string{"HomeNet24 refused the password", "Change the Wi-Fi networks"}}},
		{"a brief refusal, then joined", logConnecting + logNotJoined + logAtOnce + logJoined,
			want{"joined", "HomeNet24", []string{"pager-01 joined HomeNet24"}}},
		{"no network it can see", logNotFound,
			want{"not_found", "", []string{"can’t see", "5 GHz", "Maximize Compatibility", "out of range", "Change the Wi-Fi networks"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, pager := paired(t)
			pager.say(t, "ESP-ROM:esp32s3-20210327\r\n"+c.log) // it restarted
			status, v := p.post("/api/pair/join", map[string]any{})
			check(t, p, status, v, c.want)
			p.tab.wait(t, "pager", strings.TrimSpace(strings.SplitN(c.log, "\r\n", 2)[0])) // the log is on the page too
			p.noPassword()
		})
	}

	t.Run("nothing said yet", func(t *testing.T) {
		p, _ := paired(t)
		type answer struct {
			status int
			v      map[string]any
		}
		got := make(chan answer, 1)
		at := p.clock.Now().Add(joinWait)
		go func() {
			status, v := p.post("/api/pair/join", map[string]any{})
			got <- answer{status, v}
		}()
		if !p.clock.waitTimer(at) {
			t.Fatalf("POST /api/pair/join set no %v wait", joinWait)
		}
		p.clock.Advance(joinWait)
		a := <-got
		check(t, p, a.status, a.v, want{"unknown", "", []string{"hasn’t said", "Change the Wi-Fi networks"}})
	})

	t.Run("before any pairing", func(t *testing.T) {
		p := newPairPage(t)
		if status, v := p.post("/api/pair/join", map[string]any{}); status != http.StatusConflict || !strings.Contains(field(v, "error"), "Pair") {
			t.Errorf("POST /api/pair/join before any pairing: %d %v; want 409 saying to pair first", status, v)
		}
	})

	t.Run("changing the networks: the names kept, one entry", func(t *testing.T) {
		again := newFakePager(t, "to-tool/valid/prov_result-ok.txt") // the same pager, after PAIR AGAIN: the same key
		p, _ := paired(t, again.tty)
		networks := func() []string {
			t.Helper()
			status, v := p.get("/api/devices")
			for _, d := range anyList(v["devices"]) {
				if d, _ := d.(map[string]any); d["name"] == "pager-01" {
					return anyStrings(d["networks"])
				}
			}
			t.Fatalf("GET /api/devices: %d %v; want pager-01", status, v)
			return nil
		}
		if got := networks(); !slices.Equal(got, []string{"HomeNet24", "Sam's iPhone"}) {
			t.Errorf("GET /api/devices: pager-01's networks %q; want the names it was paired with, in order", got)
		}

		p.pair(again.tty) // the page's name, kept
		changed := []provision.Network{{SSID: "HomeNet24", Pass: "ringo-starr-77"}}
		if status, v := p.confirm(pagerFP, changed); status != http.StatusOK || field(v, "paired") != "pager-01" {
			t.Fatalf("pairing again with the networks changed: %d %v", status, v)
		}
		if devs, err := devices.Load(p.devices); err != nil || len(devs) != 1 || devs[0].Name != "pager-01" || devs[0].Fingerprint() != pagerFP {
			t.Errorf("devices.toml after changing the networks holds %+v (%v); want only pager-01, with its key", devs, err)
		}
		if got := networks(); !slices.Equal(got, []string{"HomeNet24"}) {
			t.Errorf("GET /api/devices after changing the networks: pager-01's networks %q; want [HomeNet24]", got)
		}
		p.noPassword()
	})
}
