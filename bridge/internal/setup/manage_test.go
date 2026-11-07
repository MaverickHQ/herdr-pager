package setup

import (
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/doctor"
)

// A fakeBrew is brew on the far side of Options.RunBrew: it answers each
// command it knows, and fails the test on any other.
type fakeBrew struct {
	t       *testing.T
	answers map[string]brewAnswer // by the command's words, joined with spaces
	ran     [][]string
}

type brewAnswer struct {
	out string
	err error
}

func (b *fakeBrew) run(argv ...string) ([]byte, error) {
	b.ran = append(b.ran, argv)
	a, ok := b.answers[strings.Join(argv, " ")]
	if !ok {
		b.t.Errorf("the page ran %q, which isn't one of brew services' fixed commands", argv)
		return nil, errors.New("not allowed")
	}
	return []byte(a.out), a.err
}

var notFound = &exec.Error{Name: "brew", Err: exec.ErrNotFound}

// TestSetupManages is the page's Manage view (requirement 2.3; design §The
// setup page, Manage): the paired devices, revoked and paired again through
// the page; the pager's fw, from its hello, against the shipped firmware's
// version; run at login on and off through brew services, with fixed
// arguments; and the doctor's checks.
func TestSetupManages(t *testing.T) {
	t.Run("revoke, then pair again through the page", func(t *testing.T) {
		first, again := newFakePager(t, "to-tool/valid/prov_result-ok.txt"), newFakePager(t, "to-tool/valid/prov_result-ok.txt")
		p := newPairPage(t, first.tty, again.tty)
		p.pair(first.tty)
		if status, v := p.confirm(pagerFP, pageNets); status != http.StatusOK {
			t.Fatalf("pairing: %d %v", status, v)
		}

		status, v := p.get("/api/devices")
		devs, _ := v["devices"].([]any)
		if status != http.StatusOK || len(devs) != 1 {
			t.Fatalf("GET /api/devices: %d %v; want pager-01", status, v)
		}
		d, _ := devs[0].(map[string]any)
		if field(d, "name") != "pager-01" || field(d, "fingerprint") != pagerFP || d["connected"] != false || v["running"] != false {
			t.Errorf("GET /api/devices: %v; want pager-01, %s, not connected, no bridge running", v, pagerFP)
		}

		if status, v := p.post("/api/devices/revoke", map[string]string{"name": "pager-01"}); status != http.StatusOK || field(v, "revoked") != "pager-01" {
			t.Errorf("revoking pager-01: %d %v; want 200", status, v)
		}
		p.unchangedDevices("after the revoke")
		p.tab.step(t, "Revoked pager-01")
		if status, v := p.post("/api/devices/revoke", map[string]string{"name": "pager-01"}); status != http.StatusNotFound {
			t.Errorf("revoking pager-01 again: %d %v; want 404", status, v)
		}
		if status, v := p.get("/api/devices"); status != http.StatusOK || len(anyList(v["devices"])) != 0 {
			t.Errorf("GET /api/devices after the revoke: %d %v; want none", status, v)
		}

		// Pair again: the pager chose Pair again, on the same port or another.
		p.pair(again.tty)
		if status, v := p.confirm(pagerFP, pageNets); status != http.StatusOK {
			t.Fatalf("pairing again: %d %v", status, v)
		}
		if devs, err := devices.Load(p.devices); err != nil || len(devs) != 1 || devs[0].Name != "pager-01" || devs[0].Fingerprint() != pagerFP {
			t.Errorf("devices.toml after pairing again holds %+v (%v); want pager-01", devs, err)
		}
		p.noPassword()
	})

	t.Run("the pager's fw against the shipped version", func(t *testing.T) {
		for _, c := range []struct {
			name, shipped string // "" for no firmware folder
			update        bool
		}{
			{"the same", "0.1.0", false},
			{"another", "1.0.0", true},
			{"no firmware shipped", "", false},
		} {
			pager := newFakePager(t, "to-tool/valid/prov_result-ok.txt")
			p := newPairPageWith(t, func(o *Options) {
				if c.shipped != "" {
					o.Firmware = shipped(t, c.shipped)
				}
			}, pager.tty)
			offer := p.pair(pager.tty) // the fixture pager's hello says fw 0.1.0
			fw, _ := offer["firmware"].(map[string]any)
			if field(fw, "pager") != "0.1.0" || field(fw, "shipped") != c.shipped || fw["update"] != c.update {
				t.Errorf("%s: POST /api/pair's firmware is %v; want the pager's 0.1.0, shipped %q, update %v", c.name, offer["firmware"], c.shipped, c.update)
			}
		}
	})

	t.Run("run at login, on and off, through brew services", func(t *testing.T) {
		started := "==> Successfully started `herdr-pager` (label: homebrew.mxcl.herdr-pager)"
		for _, c := range []struct {
			name    string
			on      bool
			answers map[string]brewAnswer
			status  int
			ran     [][]string
			says    string // in the answer, and on the step channel
		}{
			{"on", true, map[string]brewAnswer{"brew services start herdr-pager": {started + "\n", nil}},
				http.StatusOK, [][]string{{"brew", "services", "start", "herdr-pager"}}, started},
			{"off", false, map[string]brewAnswer{"brew services stop herdr-pager": {"Stopping `herdr-pager`... (might take a while)\n==> Successfully stopped `herdr-pager`\n", nil}},
				http.StatusOK, [][]string{{"brew", "services", "stop", "herdr-pager"}}, "Successfully stopped"},
			{"brew not on PATH", true,
				map[string]brewAnswer{"brew services start herdr-pager": {"", notFound}, "/opt/homebrew/bin/brew services start herdr-pager": {started + "\n", nil}},
				http.StatusOK, [][]string{{"brew", "services", "start", "herdr-pager"}, {"/opt/homebrew/bin/brew", "services", "start", "herdr-pager"}}, started},
			{"no Homebrew", true,
				map[string]brewAnswer{"brew services start herdr-pager": {"", notFound}, "/opt/homebrew/bin/brew services start herdr-pager": {"", notFound}},
				http.StatusConflict, [][]string{{"brew", "services", "start", "herdr-pager"}, {"/opt/homebrew/bin/brew", "services", "start", "herdr-pager"}}, "docs/bridge.md"},
			{"brew fails", true, map[string]brewAnswer{"brew services start herdr-pager": {"Error: Formula `herdr-pager` is not installed.\n", errors.New("exit status 1")}},
				http.StatusUnprocessableEntity, [][]string{{"brew", "services", "start", "herdr-pager"}}, "is not installed"},
		} {
			brew := &fakeBrew{t: t, answers: c.answers}
			p := newPairPageWith(t, func(o *Options) { o.RunBrew = brew.run })
			status, v := p.post("/api/login", map[string]bool{"on": c.on})
			said := field(v, "error") + strings.Join(anyStrings(v["output"]), "\n")
			if status != c.status || !strings.Contains(said, c.says) {
				t.Errorf("%s: POST /api/login: %d %v; want %d, saying %q", c.name, status, v, c.status, c.says)
			}
			if !reflect.DeepEqual(brew.ran, c.ran) {
				t.Errorf("%s: ran %q; want exactly %q", c.name, brew.ran, c.ran)
			}
			p.tab.step(t, c.says)
		}
		// Only POST changes it: a GET runs nothing.
		brew := &fakeBrew{t: t}
		p := newPairPageWith(t, func(o *Options) { o.RunBrew = brew.run })
		if resp := p.do(p.request(http.MethodGet, "/api/login", nil)); resp.StatusCode == http.StatusOK {
			t.Errorf("GET /api/login: %d; want it not to run brew", resp.StatusCode)
		}
		if len(brew.ran) != 0 {
			t.Errorf("a GET ran %q", brew.ran)
		}
	})

	t.Run("the bridge's checks are the doctor's", func(t *testing.T) {
		report := doctor.Report{OK: false, Checks: []doctor.Check{
			{ID: doctor.CheckHerdr, Name: "herdr's socket", OK: true, State: "ok", Detail: "herdr answers"},
			{ID: doctor.CheckFirewall, Name: "the firewall", OK: false, State: "blocking", Detail: "it blocks pager-bridge", Fix: "allow it",
				Commands: []string{"sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp /x"}},
		}}
		calls := 0
		p := newPairPageWith(t, func(o *Options) { o.Doctor = func() doctor.Report { calls++; return report } })
		status, v := p.get("/api/doctor")
		want, _ := json.Marshal(report)
		var wantV map[string]any
		json.Unmarshal(want, &wantV)
		if status != http.StatusOK || !reflect.DeepEqual(v, wantV) || calls != 1 {
			t.Errorf("GET /api/doctor: %d %v (doctor ran %d times); want 200 and the doctor's report %s", status, v, calls, want)
		}
	})
}

func anyList(v any) []any {
	l, _ := v.([]any)
	return l
}
