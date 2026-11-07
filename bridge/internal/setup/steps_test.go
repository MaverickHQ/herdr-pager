package setup

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/config"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/doctor"
)

// TestSetupPageSteps is what the page's steps need beyond pairing and
// Manage (requirements 2.1, 2.4): the bridge's keys, made if missing and
// kept if not; the Wi-Fi step's near-miss warning, from the names alone; an
// update that asks before it erases, so it can keep the pairing; and the
// checks' results on the step channel.
func TestSetupPageSteps(t *testing.T) {
	t.Run("the bridge's keys, made if missing", func(t *testing.T) {
		home := t.TempDir()
		p := &pairPage{guarded: newGuardedWith(t, Options{Home: home}), t: t}
		status, v := p.post("/api/keys", map[string]string{})
		key, err := config.HostKey(config.Dir(home))
		if err != nil {
			t.Fatalf("POST /api/keys: %d %v; no host key after it: %v", status, v, err)
		}
		if _, err := os.Stat(filepath.Join(config.Dir(home), config.File)); status != http.StatusOK || err != nil {
			t.Errorf("POST /api/keys: %d %v, config.toml: %v; want 200 and both files", status, v, err)
		}
		if field(v, "fingerprint") != ssh.FingerprintSHA256(key) || field(v, "type") != ssh.KeyAlgoED25519 {
			t.Errorf("POST /api/keys answers %v; want the host key's fingerprint %s, ed25519", v, ssh.FingerprintSHA256(key))
		}
		// Again: the same key, kept.
		if _, v := p.post("/api/keys", map[string]string{}); field(v, "fingerprint") != ssh.FingerprintSHA256(key) {
			t.Errorf("a second POST /api/keys answers %v; want the same key, kept", v)
		}
		// With no home, nothing is made anywhere.
		p = &pairPage{guarded: newGuarded(t), t: t}
		if status, _ := p.post("/api/keys", map[string]string{}); status != http.StatusConflict {
			t.Errorf("POST /api/keys with no home: %d; want 409", status)
		}
		// A GET changes nothing (3.2: no route changes anything on a GET).
		home = t.TempDir()
		g := newGuardedWith(t, Options{Home: home})
		g.do(g.request(http.MethodGet, "/api/keys", nil))
		if _, err := os.Stat(config.Dir(home)); !os.IsNotExist(err) {
			t.Errorf("GET /api/keys made %s (%v); want nothing made", config.Dir(home), err)
		}
	})

	t.Run("the Wi-Fi step's near-miss warning, from the names alone", func(t *testing.T) {
		p := &pairPage{guarded: newGuardedWith(t, Options{KnownNetworks: func() []string { return macNets }}), t: t}
		status, v := p.post("/api/wifi/check", map[string][]string{"ssids": {"HomeNet24", "Sam's iPhone", "Elsewhere"}})
		want := map[string]any{
			"known":       []any{"HomeNet24", "Sam’s iPhone"},
			"near_misses": []any{map[string]any{"given": "Sam's iPhone", "known": "Sam’s iPhone"}},
		}
		if status != http.StatusOK || !reflect.DeepEqual(v, want) {
			got, _ := json.Marshal(v)
			t.Errorf("POST /api/wifi/check: %d %s; want %v", status, got, want)
		}
		p = &pairPage{guarded: newGuardedWith(t, Options{KnownNetworks: func() []string { return nil }}), t: t}
		if status, v := p.post("/api/wifi/check", map[string][]string{"ssids": {"HomeNet24"}}); status != http.StatusOK ||
			!reflect.DeepEqual(v, map[string]any{"known": []any{}, "near_misses": []any{}}) {
			t.Errorf("with none saved: %d %v; want empty lists", status, v)
		}
		if status, _ := p.post("/api/wifi/check", "not the page's JSON"); status != http.StatusBadRequest {
			t.Errorf("POST /api/wifi/check with a body that isn't the page's: %d; want 400", status)
		}
	})

	t.Run("an update asks before it erases", func(t *testing.T) {
		g := page(t, Options{Firmware: shipped(t, "1.0.0")})
		install := g.flashes("1.0.0")
		status, header, body := g.get("/firmware/update.json")
		var update map[string]any
		if err := json.Unmarshal(body, &update); status != http.StatusOK || err != nil {
			t.Fatalf("GET /firmware/update.json: %d %v: %q", status, err, body)
		}
		if ct := header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("GET /firmware/update.json: Content-Type %q; want application/json", ct)
		}
		if update["new_install_prompt_erase"] != true {
			t.Errorf("the update's manifest is %s; want new_install_prompt_erase true", body)
		}
		var installRaw map[string]any
		_, _, raw := g.get("/firmware/manifest.json")
		if err := json.Unmarshal(raw, &installRaw); err != nil {
			t.Fatal(err)
		}
		if _, asks := installRaw["new_install_prompt_erase"]; asks {
			t.Errorf("the install's manifest asks before it erases; a first install always erases")
		}
		delete(update, "new_install_prompt_erase")
		if !reflect.DeepEqual(update, installRaw) {
			t.Errorf("the update's manifest is %v; want the install's %v (%v), and the prompt", update, installRaw, install)
		}
		// Its parts are the install's, served from beside it.
		for _, part := range install.Builds[0].Parts {
			if status, _, _ := g.get("/firmware/" + part.Path); status != http.StatusOK {
				t.Errorf("GET /firmware/%s: %d; want the update's part", part.Path, status)
			}
		}
		// With no firmware, no update either.
		if status, _, _ := page(t, Options{}).get("/firmware/update.json"); status != http.StatusNotFound {
			t.Errorf("GET /firmware/update.json with no firmware: %d; want 404", status)
		}
	})

	t.Run("the checks' results on the step channel", func(t *testing.T) {
		report := doctor.Report{Checks: []doctor.Check{
			{ID: doctor.CheckHerdr, Name: "herdr's socket", OK: true, State: "ok", Detail: "it answers"},
			{ID: doctor.CheckFirewall, Name: "The firewall", State: "blocking", Detail: "on, and it blocks the bridge",
				Fix: "Allow it.", Commands: []string{"sudo socketfilterfw --add /x/pager-bridge"}},
		}}
		p := newPairPageWith(t, func(o *Options) { o.Doctor = func() doctor.Report { return report } })
		if status, _ := p.get("/api/doctor"); status != http.StatusOK {
			t.Fatalf("GET /api/doctor: %d; want 200", status)
		}
		p.tab.step(t, "ok", "herdr's socket", "it answers")
		p.tab.step(t, "FAIL", "The firewall", "on, and it blocks the bridge")
		p.tab.step(t, "Fix: Allow it.")
		p.tab.step(t, "sudo socketfilterfw --add /x/pager-bridge")
		if got := p.log.String(); !containsAll(got, []string{"The firewall", "Fix: Allow it."}) {
			t.Errorf("the page's log has %q; want the checks too", got)
		}
	})
}
