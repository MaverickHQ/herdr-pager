package emutest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/setup"
)

// A setupPage is the setup page (phase 5 design D5P-3), served in this test
// on 127.0.0.1 for the harness's bridge directory, and a browser's view of
// it: the cookie the token is traded for, and the page's Origin on each POST.
type setupPage struct {
	t      *testing.T
	base   string // http://127.0.0.1:<port>
	client *http.Client
	log    syncBuffer // the page's log (setup.Options.Log)

	mu     sync.Mutex
	bodies [][]byte // every answer, for the byte search
}

// startPage serves the setup page for b's directory, with port as the only
// USB serial port, and opens it as `pager-bridge setup` does: the URL with
// the token, traded for the page's cookie.
func startPage(t *testing.T, b *Bridge, port string) *setupPage {
	t.Helper()
	return startPageNamed(t, b, port, "emu-mac")
}

// startPageNamed is startPage on a Mac whose LocalHostName is hostName.
func startPageNamed(t *testing.T, b *Bridge, port, hostName string) *setupPage {
	t.Helper()
	p := &setupPage{t: t}
	h, err := setup.NewHandler(setup.Options{
		Home:          b.Home,
		Getenv:        func(string) string { return "" },
		Ports:         func() ([]string, error) { return []string{port}, nil },
		LocalHostName: func() (string, error) { return hostName, nil },
		KnownNetworks: func() []string { return nil },
		Log:           &p.log,
	})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := setup.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- h.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("the setup page's Serve: %v", err)
		}
	})
	p.base = "http://" + ln.Addr().String()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	p.client = &http.Client{Jar: jar, Timeout: 2 * time.Minute}
	resp, err := p.client.Get(p.base + "/?t=" + url.QueryEscape(h.Token()))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("herdr-pager setup")) {
		t.Fatalf("opening the page: %d, %.80q; want 200 and the page", resp.StatusCode, page)
	}
	return p
}

// openTab holds the page's event stream open, as a tab does, and returns
// what it receives.
func (p *setupPage) openTab() *syncBuffer {
	p.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/events", nil)
	if err != nil {
		p.t.Fatal(err)
	}
	client := &http.Client{Jar: p.client.Jar} // no timeout: it streams
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		cancel()
		p.t.Fatalf("GET /events: %v %v", resp, err)
	}
	stream := &syncBuffer{}
	done := make(chan struct{})
	go func() { io.Copy(stream, resp.Body); close(done) }()
	p.t.Cleanup(func() { cancel(); resp.Body.Close(); <-done })
	return stream
}

// post sends body to path as the page's script does, with its Origin, and
// returns the status and the decoded answer. It never fails the test, so a
// goroutine may call it.
func (p *setupPage) post(path string, body any) (int, map[string]any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, p.base+path, bytes.NewReader(data))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", p.base)
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	p.mu.Lock()
	p.bodies = append(p.bodies, raw)
	p.mu.Unlock()
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return resp.StatusCode, nil, fmt.Errorf("POST %s: %d, not JSON: %q", path, resp.StatusCode, raw)
	}
	return resp.StatusCode, v, nil
}

// field is v's string at the path of keys.
func field(v map[string]any, keys ...string) string {
	var cur any = v
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[k]
	}
	s, _ := cur.(string)
	return s
}

// TestSetupPairsTheEmulatedPager (S5P-5; requirements 2.1, 2.2, 2.6, 2.9;
// ADR 0011): the emulated pager, flashed and never paired, is paired
// through the setup page's API over its virtual serial port, as the page's
// Pair step does it. POST /api/pair gives both full fingerprints and sends
// the pager the prov_offer, nothing secret; the pager shows the same two,
// and its BOOT confirms (golden: pairing/confirmed.png); then POST
// /api/pair/confirm, the page's confirmation, sends the network and its
// password, which the pager takes without a second press. Then devices.toml
// holds it, and the pager restarts, joins the network and its link to the
// harness's bridge comes up. The page's step and pager channels show the
// pairing, and the password is in nothing the page answered, streamed or
// logged.
// TestTheConfirmScreensKeepALongMacNameToOneLine: a Mac's LocalHostName can
// be 63 characters, such as a CI runner's. The pairing screens' titles name
// it, and stay one line, cut with dots, so the bridge's fingerprint under them
// is never drawn over (CI's first run, 2026-11-07: the title wrapped onto the
// HOST line). The goldens are the short name's, with only the title masked.
func TestTheConfirmScreensKeepALongMacNameToOneLine(t *testing.T) {
	long := "Mac-mini-6A1F2C3D-0B1C-4D2E-9F0E-26249A0B1C2D-of-the-build-farm"
	if len(long) != 63 {
		t.Fatalf("the name is %d characters; want macOS's longest, 63", len(long))
	}
	b := StartBridge(t, quietScenario)
	in := Start(t, Options{})
	in.AirNetworks(EmuNetwork)
	in.WaitText("PAIR VIA USB", 10*time.Second)

	page := startPageNamed(t, b, in.Serial, long)
	status, offer, err := page.post("/api/pair", map[string]string{"port": in.Serial, "name": "emu-01", "host": "127.0.0.1"})
	if err != nil || status != http.StatusOK {
		t.Fatalf("POST /api/pair: %d %v %v; want 200", status, offer, err)
	}
	hostFP := ssh.FingerprintSHA256(b.HostKey)
	in.WaitText("HOST "+hostFP, 30*time.Second)
	in.MatchGoldenBlanked("pairing/confirm.png", maskPairingTitle, maskPairingDev)

	in.Press("boot", 100)
	in.WaitText("DEV "+field(offer, "pager", "fingerprint"), 5*time.Second)
	in.MatchGoldenBlanked("pairing/confirmed.png", maskPairingTitle, maskPairingDev)
}

func TestSetupPairsTheEmulatedPager(t *testing.T) {
	b := StartBridge(t, quietScenario)
	in := Start(t, Options{})
	in.AirNetworks(EmuNetwork)
	in.WaitText("PAIR VIA USB", 10*time.Second)

	page := startPage(t, b, in.Serial)
	tab := page.openTab()

	// Step one: the port, the name and the address the pager is to call.
	status, offer, err := page.post("/api/pair", map[string]string{"port": in.Serial, "name": "emu-01", "host": "127.0.0.1"})
	if err != nil || status != http.StatusOK {
		t.Fatalf("POST /api/pair: %d %v %v; want 200", status, offer, err)
	}
	hostFP, devFP := ssh.FingerprintSHA256(b.HostKey), field(offer, "pager", "fingerprint")
	if got := field(offer, "bridge", "fingerprint"); got != hostFP {
		t.Fatalf("POST /api/pair gives the bridge's key as %q; want %s", got, hostFP)
	}
	if !strings.HasPrefix(devFP, "SHA256:") || field(offer, "pager", "type") != ssh.KeyAlgoED25519 {
		t.Fatalf("POST /api/pair gives the pager's key as %v; want its full ed25519 fingerprint", offer["pager"])
	}
	if field(offer, "host") != "127.0.0.1" || field(offer, "pager", "fw") == "" {
		t.Errorf("POST /api/pair answers %v; want host 127.0.0.1 and the pager's fw from its hello", offer)
	}
	waitFor(t, tab, `event: step\ndata: \{"text":"The pager's key: +`+regexp.QuoteMeta(devFP), 10*time.Second, "the step channel")
	// The pager has the prov_offer, nothing secret: it shows the same two
	// fingerprints and the Mac's name, and devices.toml doesn't hold it.
	in.WaitText("PAIR WITH emu-mac?", 30*time.Second)
	in.WaitText("HOST "+hostFP, 5*time.Second)
	in.WaitText("DEV "+devFP, 5*time.Second)
	if data, _ := os.ReadFile(filepath.Join(b.Dir, "devices.toml")); bytes.Contains(data, []byte("emu-01")) {
		t.Fatalf("devices.toml holds emu-01 before the networks were sent:\n%s", data)
	}

	// BOOT confirms on the pager: it keeps both fingerprints up and waits
	// for the networks, and the page says so. Still nothing secret has gone.
	in.Press("boot", 100)
	in.WaitText("NOW CONFIRM ON emu-mac", 5*time.Second)
	in.WaitText("DEV "+devFP, time.Second)
	in.MatchGoldenBlanked("pairing/confirmed.png", maskPairingDev)
	waitFor(t, tab, `event: step\ndata: \{"text":"The pager confirmed with BOOT`, 10*time.Second, "the step channel")
	if strings.Contains(tab.String(), "Both confirmed") {
		t.Fatal("the networks went before the page's confirm")
	}

	// Step two, the page's confirmation: the network and its password,
	// which go now, and the pager takes without a second press.
	status, a, err := page.post("/api/pair/confirm", map[string]any{
		"fingerprint": devFP,
		"wifi":        []map[string]string{{"ssid": EmuNetwork.SSID, "pass": EmuNetwork.Pass}},
	})
	if err != nil || status != http.StatusOK || field(a, "paired") != "emu-01" || field(a, "fingerprint") != devFP {
		t.Fatalf("POST /api/pair/confirm: %d %v %v; want 200, paired emu-01 with %s; the page's log:\n%s", status, a, err, devFP, &page.log)
	}
	devices, err := os.ReadFile(filepath.Join(b.Dir, "devices.toml"))
	if err != nil || !bytes.Contains(devices, []byte(`"emu-01"`)) {
		t.Fatalf("devices.toml doesn't hold emu-01 (%v):\n%s", err, devices)
	}

	// The link: restarted paired, the pager joins the network and its
	// session with the bridge comes up (o), with the fake herdr's agents.
	in.WaitText("o lcd349-emu", 60*time.Second)
	in.WaitText("ALL CLEAR >1", 10*time.Second)

	// What the page showed: the pairing on the step channel, and the
	// pager's own serial lines on the pager channel.
	waitFor(t, tab, `event: step\ndata: \{"text":"Paired emu-01\.`, 10*time.Second, "the step channel")
	waitFor(t, tab, `event: pager\ndata: \{"text":"[^"]`, 10*time.Second, "the pager channel")

	// The byte search (requirement 2.6): the password went only to the pager.
	pass := []byte(EmuNetwork.Pass)
	page.mu.Lock()
	for i, body := range page.bodies {
		if bytes.Contains(body, pass) {
			t.Errorf("the page's answer %d holds the password: %q", i, body)
		}
	}
	page.mu.Unlock()
	if strings.Contains(tab.String(), EmuNetwork.Pass) {
		t.Error("the page's event stream holds the password")
	}
	if strings.Contains(page.log.String(), EmuNetwork.Pass) {
		t.Error("the page's log holds the password")
	}
	filepath.WalkDir(b.Home, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if data, _ := os.ReadFile(path); bytes.Contains(data, pass) {
				t.Errorf("%s holds the password", path)
			}
		}
		return nil
	})
}
