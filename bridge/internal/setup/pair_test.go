package setup

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/config"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/provision"
)

// The provisioning fixtures (testdata/provisioning/README.md), and the full
// fingerprint of the device key in their prov_hello.
const (
	provFixtures = "../../testdata/provisioning"
	pagerFP      = "SHA256://a7FlX9Y4t0WSS+W8ANyY4w+5+axSEVbGzNXp6txjY"
)

// The networks the page posts, the second a near miss of a hotspot the Mac
// has saved with a curly apostrophe; and their passwords, which must reach
// only the pager. All made up.
var (
	pageNets = []provision.Network{
		{SSID: "HomeNet24", Pass: "tangerine-otter-42"},
		{SSID: "Sam's iPhone", Pass: "ringo-starr-77"},
	}
	macNets   = []string{"HomeNet24", "Sam’s iPhone"}
	passwords = []string{"tangerine-otter-42", "ringo-starr-77", "short77"}
)

func provFixture(t *testing.T, file string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(provFixtures, file))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A fakePager is the pager on a pty's far side (seam S2-5, as provision's
// tests play it; ADR 0011): it says hello every 20 ms until it gets a
// prov_offer, shows it (confirming), and is confirmed with BOOT; it then
// answers the prov_set with the result fixture.
type fakePager struct {
	tty  string
	sent chan []byte // each provisioning line the bridge sends it
	peer *os.File    // its own side: say writes to it once its script is over

	mu  sync.Mutex
	got bytes.Buffer // every byte the bridge has sent it
}

// A pagerScript is what a fakePager does past the hello.
type pagerScript struct {
	result string // the fixture it answers the prov_set with
	// offerResult, when set, answers the prov_offer in place of BOOT (PWR:
	// cancelled), and noBoot leaves it unanswered: nobody presses BOOT.
	offerResult string
	noBoot      bool
	// again, with offerResult or noBoot, goes back to saying hello as soon as
	// the offer is answered or shown, so the page can pair through it again.
	again bool
	// backlog is written first; echo, when set, writes back the prov_set
	// after that prefix, as a console that echoes its input would.
	backlog, echo string
	// silent never says hello: a pager that isn't in pairing mode, logging.
	silent bool
}

func newFakePager(t *testing.T, result string) *fakePager {
	t.Helper()
	return newFakePagerScript(t, pagerScript{result: result})
}

// newFakePagerWith is newFakePager, writing backlog first, and echoing the
// prov_set after echo when it isn't "".
func newFakePagerWith(t *testing.T, result, backlog, echo string) *fakePager {
	t.Helper()
	return newFakePagerScript(t, pagerScript{result: result, backlog: backlog, echo: echo})
}

func newFakePagerScript(t *testing.T, s pagerScript) *fakePager {
	t.Helper()
	peer, tt, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := provision.OpenPort(tt.Name()) // raw, as a serial port is; held open
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		raw.Close()
		tt.Close()
		peer.Close()
	})
	fixture := func(file string) []byte {
		if file == "" {
			return nil
		}
		return provFixture(t, file)
	}
	hello, confirming, confirmed := fixture("to-tool/valid/prov_hello.txt"), fixture("to-tool/valid/prov_hello-confirming.txt"), fixture("to-tool/valid/prov_hello-confirmed.txt")
	offerResult, result := fixture(s.offerResult), fixture(s.result)
	p := &fakePager{tty: tt.Name(), sent: make(chan []byte, 8), peer: peer}
	offers, sets := make(chan []byte, 4), make(chan []byte, 1)
	go func() {
		sc := bufio.NewScanner(peer)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			p.mu.Lock()
			p.got.Write(append(line, '\n'))
			p.mu.Unlock()
			if !bytes.HasPrefix(line, []byte(provision.Prefix)) {
				continue
			}
			p.sent <- line
			switch m, _ := provision.ParseTool(line); m.(type) {
			case *provision.HostOffer:
				offers <- line
			case *provision.Set:
				sets <- line
			}
		}
	}()
	go func() { // the only writer, so its lines go in order
		peer.Write([]byte(s.backlog))
		if s.silent {
			peer.Write([]byte("I (1234) pager: a log line\r\n"))
			return
		}
		for {
			if _, err := peer.Write(append([]byte("I (1234) pager: a log line\r\n"), hello...)); err != nil {
				return
			}
			select {
			case <-offers:
			case <-time.After(20 * time.Millisecond):
				continue
			}
			peer.Write(confirming)
			switch {
			case offerResult != nil:
				peer.Write(offerResult)
			case s.noBoot:
			default:
				peer.Write(confirmed) // BOOT
				select {
				case set := <-sets:
					if s.echo != "" {
						peer.Write([]byte(s.echo + string(set) + "\r\n"))
					}
					peer.Write(result)
				case <-time.After(10 * time.Second):
				}
				return
			}
			if !s.again {
				return
			}
		}
	}()
	return p
}

// say writes s to the port as the pager, after its script has answered the
// prov_set (its writer has stopped by then): what it logs once it restarts.
func (p *fakePager) say(t *testing.T, s string) {
	t.Helper()
	if _, err := p.peer.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
}

// received is every byte the bridge has sent the pager so far.
func (p *fakePager) received() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.got.Bytes()...)
}

// offered checks that the next line the bridge sent the pager is the
// prov_offer, holding nothing secret, and returns it.
func (p *fakePager) offered(t *testing.T) *provision.HostOffer {
	t.Helper()
	select {
	case line := <-p.sent:
		m, err := provision.ParseTool(line)
		offer, ok := m.(*provision.HostOffer)
		if err != nil || !ok {
			t.Fatalf("the bridge sent the pager %s (%v); want a prov_offer", line, err)
		}
		for _, pw := range passwords {
			if bytes.Contains(line, []byte(pw)) {
				t.Errorf("the prov_offer holds a password: %s", line)
			}
		}
		return offer
	case <-time.After(5 * time.Second):
		t.Fatal("the bridge sent the pager no prov_offer")
	}
	return nil
}

// nothingSent checks that the bridge has sent the pager no more
// provisioning lines.
func (p *fakePager) nothingSent(t *testing.T, when string) {
	t.Helper()
	select {
	case line := <-p.sent:
		t.Errorf("%s: the bridge sent the pager %s", when, line)
	case <-time.After(200 * time.Millisecond):
	}
}

// noPasswordYet is requirement 2.9's byte search: nothing the pager has
// been sent holds a password.
func (p *fakePager) noPasswordYet(t *testing.T, when string) {
	t.Helper()
	got := p.received()
	for _, pw := range passwords {
		if bytes.Contains(got, []byte(pw)) {
			t.Errorf("%s: the pager has been sent the password %q", when, pw)
		}
	}
}

// A pairPage is the setup page with a bridge's directory in a temporary
// home, a tab holding its event stream, and every byte it has answered.
type pairPage struct {
	*guarded
	t       *testing.T
	clock   *fakeClock
	home    string
	devices string // devices.toml
	hostFP  string // the bridge's host key's full fingerprint
	hostKey string // as prov_set carries it
	log     syncBuffer
	tab     *eventTap

	mu     sync.Mutex
	bodies [][]byte
}

func newPairPage(t *testing.T, ports ...string) *pairPage {
	t.Helper()
	return newPairPageWith(t, nil, ports...)
}

// newPairPageWith is newPairPage, with edit's changes to the page's options.
func newPairPageWith(t *testing.T, edit func(*Options), ports ...string) *pairPage {
	t.Helper()
	home := t.TempDir()
	dir := config.Dir(home)
	key, err := config.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := &pairPage{
		t: t, clock: newFakeClock(), home: home, devices: filepath.Join(dir, devices.File),
		hostFP: ssh.FingerprintSHA256(key), hostKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))),
	}
	if err := devices.Create(p.devices); err != nil {
		t.Fatal(err)
	}
	opts := Options{
		Clock:         p.clock,
		Home:          home,
		Ports:         func() ([]string, error) { return ports, nil },
		LocalHostName: func() (string, error) { return "studio-mac", nil },
		KnownNetworks: func() []string { return macNets },
		Log:           &p.log,
	}
	if edit != nil {
		edit(&opts)
	}
	p.guarded = newGuardedWith(t, opts)
	p.tab = p.openEvents()
	return p
}

// post sends a JSON body to path from the page's own tab, and returns the
// status and the decoded answer. Every body is kept for the byte search.
func (p *pairPage) post(path string, body any) (int, map[string]any) {
	p.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		p.t.Fatal(err)
	}
	req := p.request(http.MethodPost, path, nil)
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", "application/json")
	return p.answer(p.do(req))
}

func (p *pairPage) get(path string) (int, map[string]any) {
	p.t.Helper()
	return p.answer(p.do(p.request(http.MethodGet, path, nil)))
}

func (p *pairPage) answer(resp *http.Response) (int, map[string]any) {
	p.t.Helper()
	data, _ := io.ReadAll(resp.Body)
	p.mu.Lock()
	p.bodies = append(p.bodies, data)
	p.mu.Unlock()
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		p.t.Errorf("%s %s: status %d, not JSON: %q", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, data)
	}
	return resp.StatusCode, v
}

// pair is step one, POST /api/pair, which must succeed.
func (p *pairPage) pair(port string) map[string]any {
	p.t.Helper()
	status, v := p.post("/api/pair", map[string]string{"port": port, "name": "pager-01"})
	if status != http.StatusOK {
		p.t.Fatalf("POST /api/pair: %d %v; want 200", status, v)
	}
	return v
}

func netsJSON(nets []provision.Network) []map[string]string {
	var out []map[string]string
	for _, n := range nets {
		out = append(out, map[string]string{"ssid": n.SSID, "pass": n.Pass})
	}
	return out
}

// confirm is step two, POST /api/pair/confirm, with the pager's fingerprint
// as the page showed it.
func (p *pairPage) confirm(fp string, nets []provision.Network) (int, map[string]any) {
	p.t.Helper()
	return p.post("/api/pair/confirm", map[string]any{"fingerprint": fp, "wifi": netsJSON(nets)})
}

// unchangedDevices checks devices.toml still lists no device.
func (p *pairPage) unchangedDevices(when string) {
	p.t.Helper()
	if devs, err := devices.Load(p.devices); err != nil || len(devs) != 0 {
		p.t.Errorf("%s: devices.toml holds %d devices (%v); want none", when, len(devs), err)
	}
}

// noPassword is the byte search (requirement 2.6): no password in any
// response, in the event stream, in the page's log, or in any file under
// the home.
func (p *pairPage) noPassword() {
	p.t.Helper()
	p.mu.Lock()
	places := map[string][]byte{"the event stream": p.tab.bytes(), "the page's log": []byte(p.log.String())}
	for i, b := range p.bodies {
		places[fmt.Sprintf("response %d", i+1)] = b
	}
	p.mu.Unlock()
	filepath.WalkDir(p.home, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			data, _ := os.ReadFile(path)
			places[path] = data
		}
		return nil
	})
	for where, data := range places {
		for _, pw := range passwords {
			if bytes.Contains(data, []byte(pw)) {
				p.t.Errorf("%s holds the password %q", where, pw)
			}
		}
	}
}

// An eventTap reads a tab's event stream: every byte, and each event.
type eventTap struct {
	mu     sync.Mutex
	raw    bytes.Buffer
	events []event
	more   chan struct{}
}

func (p *pairPage) openEvents() *eventTap {
	p.t.Helper()
	req := p.request(http.MethodGet, "/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		p.t.Fatalf("GET /events: %d", resp.StatusCode)
	}
	return tapEvents(p.t, resp)
}

// tapEvents reads the event stream that resp, GET /events, carries.
func tapEvents(t *testing.T, resp *http.Response) *eventTap {
	tap := &eventTap{more: make(chan struct{}, 1)}
	t.Cleanup(func() { resp.Body.Close() })
	go func() {
		br := bufio.NewReader(resp.Body)
		var channel string
		for {
			line, err := br.ReadString('\n')
			tap.mu.Lock()
			tap.raw.WriteString(line)
			line = strings.TrimSuffix(line, "\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				channel = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				var d struct{ Text string }
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &d) != nil {
					d.Text = "NOT JSON: " + line
				}
				tap.events = append(tap.events, event{channel, d.Text})
			case line == "":
				channel = ""
			}
			tap.mu.Unlock()
			select {
			case tap.more <- struct{}{}:
			default:
			}
			if err != nil {
				return
			}
		}
	}()
	return tap
}

func (e *eventTap) bytes() []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]byte(nil), e.raw.Bytes()...)
}

// step waits up to 10 s for a step event holding each of says, and returns
// its index in the stream.
func (e *eventTap) step(t *testing.T, says ...string) int {
	t.Helper()
	i, _ := e.wait(t, "step", says...)
	return i
}

// wait waits up to 10 s for an event on channel holding each of says, and
// returns its index in the stream, and its text.
func (e *eventTap) wait(t *testing.T, channel string, says ...string) (int, string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		e.mu.Lock()
		for i, ev := range e.events {
			if ev.channel == channel && containsAll(ev.text, says) {
				e.mu.Unlock()
				return i, ev.text
			}
		}
		var seen []string
		for _, ev := range e.events {
			seen = append(seen, ev.channel+": "+ev.text)
		}
		e.mu.Unlock()
		select {
		case <-e.more:
		case <-deadline:
			t.Fatalf("no %s event says %q; the stream has:\n%s", channel, says, strings.Join(seen, "\n"))
		}
	}
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// A syncBuffer is a bytes.Buffer that the page and the test share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// field is v's string at a dotted path, "" if there's none.
func field(v map[string]any, path string) string {
	var cur any = v
	for _, k := range strings.Split(path, ".") {
		m, _ := cur.(map[string]any)
		cur = m[k]
	}
	s, _ := cur.(string)
	return s
}

// TestPairingThroughThePage is S5P-4's pairing (requirements 2.1, 2.6, 2.9;
// design §The setup page, Pairing; ADR 0011), in its two steps: POST
// /api/pair opens the pager's port, sends it the prov_offer (nothing
// secret) and answers both full fingerprints; the pager shows them and
// confirms with BOOT; POST /api/pair/confirm carries the networks and their
// passwords, and only then does the bridge send prov_set. Nothing the pager
// is sent before both confirmations holds a password. The step channel
// shows the pager's BOOT, the near-miss warning, both fingerprints and the
// result, and no password is in any response, event, log line or file.
func TestPairingThroughThePage(t *testing.T) {
	t.Run("in two steps: the fingerprints, then the networks", func(t *testing.T) {
		pager := newFakePager(t, "to-tool/valid/prov_result-ok.txt")
		p := newPairPage(t, pager.tty)

		if status, v := p.get("/api/ports"); status != http.StatusOK || !slices.Contains(anyStrings(v["ports"]), pager.tty) {
			t.Errorf("GET /api/ports: %d %v; want 200 with %s", status, v, pager.tty)
		}

		offer := p.pair(pager.tty)
		if got := field(offer, "pager.fingerprint"); got != pagerFP {
			t.Errorf("POST /api/pair: the pager's fingerprint %q; want %s", got, pagerFP)
		}
		if got := field(offer, "bridge.fingerprint"); got != p.hostFP {
			t.Errorf("POST /api/pair: the bridge's fingerprint %q; want %s", got, p.hostFP)
		}
		if field(offer, "pager.fw") != "0.1.0" || field(offer, "host_name") != "studio-mac" || field(offer, "name") != "pager-01" {
			t.Errorf("POST /api/pair: %v; want the pager's fw 0.1.0, host_name studio-mac, name pager-01", offer)
		}
		p.tab.step(t, pagerFP)
		p.tab.step(t, p.hostFP)

		// The pager has the prov_offer: the name, the address, the bridge's
		// key and the Mac's name, for it to show. Nothing secret.
		sent := pager.offered(t)
		if sent.Name != "pager-01" || sent.Host != "studio-mac.local" || sent.Port != 2277 || sent.HostKey != p.hostKey || sent.HostName != "studio-mac" {
			t.Errorf("the prov_offer is %+v; want pager-01, studio-mac.local:2277, the bridge's key and studio-mac", sent)
		}
		booted := p.tab.step(t, "The pager confirmed with BOOT")
		pager.nothingSent(t, "after BOOT, before the page's confirm")
		pager.noPasswordYet(t, "after BOOT, before the page's confirm")
		p.unchangedDevices("before the networks")

		status, v := p.confirm(pagerFP, pageNets)
		if status != http.StatusOK || field(v, "paired") != "pager-01" {
			t.Fatalf("POST /api/pair/confirm: %d %v; want 200, paired pager-01", status, v)
		}

		// The pager got the prov_set, with both networks and their
		// passwords in order, and the offer's key: they reach the pager, and
		// only the pager.
		var set *provision.Set
		select {
		case line := <-pager.sent:
			m, err := provision.ParseTool(line)
			if err != nil {
				t.Fatalf("the bridge sent an invalid prov_set: %v", err)
			}
			set = m.(*provision.Set)
		default:
			t.Fatal("the bridge sent the pager no prov_set")
		}
		if !slices.Equal(set.WiFi, pageNets) || set.HostKey != p.hostKey {
			t.Errorf("the prov_set is %+v; want the two networks in order and the bridge's key", set)
		}
		pager.nothingSent(t, "after the prov_set")

		devs, err := devices.Load(p.devices)
		if err != nil || len(devs) != 1 || devs[0].Name != "pager-01" || devs[0].Fingerprint() != pagerFP {
			t.Fatalf("devices.toml holds %+v (%v); want pager-01 with %s", devs, err, pagerFP)
		}

		// The step channel: BOOT, then the near-miss warning, before the
		// prov_set went, and the result.
		warned := p.tab.step(t, `"Sam's iPhone"`, `"Sam’s iPhone"`, "exact name")
		sending := p.tab.step(t, "Both confirmed", "sending the pager its Wi-Fi networks")
		if booted > sending || warned > sending {
			t.Errorf("BOOT (event %d) and the near-miss warning (event %d) must come before the prov_set (event %d)", booted, warned, sending)
		}
		p.tab.step(t, "Paired pager-01")
		p.noPassword()
	})

	t.Run("cancelled before the networks: no password reaches the pager", func(t *testing.T) {
		pager := newFakePagerScript(t, pagerScript{noBoot: true, again: true})
		p := newPairPage(t, pager.tty)
		p.pair(pager.tty)
		pager.offered(t)
		if status, v := p.post("/api/pair/cancel", map[string]string{}); status != http.StatusOK {
			t.Errorf("POST /api/pair/cancel: %d %v; want 200", status, v)
		}
		p.tab.step(t, "Pairing cancelled", "no Wi-Fi password was sent")
		if status, v := p.confirm(pagerFP, pageNets); status != http.StatusConflict {
			t.Errorf("confirm after a cancel: %d %v; want 409", status, v)
		}
		pager.nothingSent(t, "after a cancel")
		// The port was let go: the page can pair through it again.
		p.pair(pager.tty)
		pager.offered(t)
		p.post("/api/pair/cancel", map[string]string{})
		pager.nothingSent(t, "after a second cancel")
		pager.noPasswordYet(t, "after two cancels")
		p.unchangedDevices("after a cancel")
		p.noPassword()
	})

	t.Run("the pager cancels (PWR): the page says so, devices.toml unchanged", func(t *testing.T) {
		pager := newFakePagerScript(t, pagerScript{offerResult: "to-tool/valid/prov_result-cancelled.txt"})
		p := newPairPage(t, pager.tty)
		p.pair(pager.tty)
		pager.offered(t)
		p.tab.step(t, "cancelled", "PWR", "devices.toml is unchanged")
		p.unchangedDevices("after the pager cancelled")
		// It's over: there's nothing left to confirm, and no password goes.
		if status, v := p.confirm(pagerFP, pageNets); status != http.StatusConflict {
			t.Errorf("a confirm after the pager cancelled: %d %v; want 409", status, v)
		}
		pager.nothingSent(t, "after the pager cancelled")
		pager.noPasswordYet(t, "after the pager cancelled")
		p.noPassword()
	})

	t.Run("an older pager refuses the offer: flash the shipped firmware first", func(t *testing.T) {
		pager := newFakePagerScript(t, pagerScript{offerResult: "to-tool/valid/prov_result-invalid.txt"})
		p := newPairPage(t, pager.tty)
		p.pair(pager.tty)
		pager.offered(t)
		p.tab.step(t, "older", "Flash the firmware shipped with this bridge first")
		if status, v := p.confirm(pagerFP, pageNets); status != http.StatusConflict {
			t.Errorf("a confirm after the offer was refused: %d %v; want 409", status, v)
		}
		pager.noPasswordYet(t, "after the offer was refused")
		p.unchangedDevices("after the offer was refused")
	})

	t.Run("no hello in 120 s: the page says what to do, in its words, not the CLI's", func(t *testing.T) {
		// The owner's last recheck of 7.2 (open-items O-96): a paired pager,
		// not yet in pairing mode when the page opened its port.
		pager := newFakePagerScript(t, pagerScript{silent: true})
		p := newPairPage(t, pager.tty)
		type answer struct {
			status int
			v      map[string]any
		}
		done := make(chan answer, 1)
		go func() {
			status, v := p.post("/api/pair", map[string]string{"port": pager.tty, "name": "pager-01"})
			done <- answer{status, v}
		}()
		if !p.clock.waitTimer(p.clock.Now().Add(provision.HelloTimeout)) {
			t.Fatal("no 120 s wait for the pager's hello")
		}
		p.clock.Advance(provision.HelloTimeout)
		a := <-done
		msg, _ := a.v["error"].(string)
		if a.status != http.StatusUnprocessableEntity {
			t.Errorf("POST /api/pair with no hello: %d %v; want 422", a.status, a.v)
		}
		for _, s := range []string{"2 minutes", "Pair again", "PAIR VIA USB", "Start again", "Nothing was sent to the pager"} {
			if !strings.Contains(msg, s) {
				t.Errorf("POST /api/pair with no hello: %q doesn't say %q", msg, s)
			}
		}
		for _, s := range []string{"provision", "--port", "120 s"} {
			if strings.Contains(msg, s) {
				t.Errorf("POST /api/pair with no hello: %q says %q, the CLI's words", msg, s)
			}
		}
		pager.nothingSent(t, "with no hello")
		p.unchangedDevices("with no hello")
	})

	t.Run("confirmed on the page before BOOT: the networks wait for it", func(t *testing.T) {
		pager := newFakePagerScript(t, pagerScript{noBoot: true, again: true})
		p := newPairPage(t, pager.tty)
		p.pair(pager.tty)
		pager.offered(t)
		confirmed := make(chan int, 1)
		go func() {
			status, _ := p.confirm(pagerFP, pageNets)
			confirmed <- status
		}()
		// Nobody presses BOOT: provision's 40 s run out on the page's clock,
		// and no password was ever sent.
		if !p.clock.waitTimer(p.clock.Now().Add(provision.ConfirmedTimeout)) {
			t.Fatal("no 40 s wait for the pager's BOOT")
		}
		pager.nothingSent(t, "confirmed on the page, before BOOT")
		pager.noPasswordYet(t, "confirmed on the page, before BOOT")
		p.clock.Advance(provision.ConfirmedTimeout)
		if status := <-confirmed; status != http.StatusUnprocessableEntity {
			t.Errorf("POST /api/pair/confirm with no BOOT: %d; want 422", status)
		}
		p.tab.step(t, "Pairing failed", "BOOT", "no Wi-Fi password was sent")
		pager.nothingSent(t, "after 40 s with no BOOT")
		pager.noPasswordYet(t, "after 40 s with no BOOT")
		p.unchangedDevices("after 40 s with no BOOT")
		p.pair(pager.tty) // the port was let go
		p.noPassword()
	})

	t.Run("settings the pager won't take: nothing sent, the pairing waits", func(t *testing.T) {
		pager := newFakePager(t, "to-tool/valid/prov_result-ok.txt")
		p := newPairPage(t, pager.tty)
		p.pair(pager.tty)
		pager.offered(t)
		bad := []provision.Network{{SSID: "HomeNet24", Pass: "short77"}}
		status, v := p.confirm(pagerFP, bad)
		if status != http.StatusBadRequest || !strings.Contains(field(v, "error"), "8 to 63") {
			t.Errorf("a 7-character password: %d %v; want 400, saying a passphrase is 8 to 63 characters", status, v)
		}
		status, v = p.confirm(pagerFP, nil)
		if status != http.StatusBadRequest {
			t.Errorf("no networks: %d %v; want 400", status, v)
		}
		pager.nothingSent(t, "after settings the pager won't take")
		pager.noPasswordYet(t, "after settings the pager won't take")
		if status, v := p.confirm(pagerFP, pageNets[:1]); status != http.StatusOK {
			t.Errorf("then good settings: %d %v; want 200", status, v)
		}
		p.noPassword()
	})

	t.Run("confirming another pager's fingerprint, or none", func(t *testing.T) {
		pager := newFakePager(t, "to-tool/valid/prov_result-ok.txt")
		p := newPairPage(t, pager.tty)
		if status, v := p.confirm(pagerFP, pageNets); status != http.StatusConflict {
			t.Errorf("confirm with no pairing: %d %v; want 409", status, v)
		}
		p.pair(pager.tty)
		pager.offered(t)
		for _, fp := range []string{"", p.hostFP, "SHA256:nnZy+qtxcxg/Zh8yqXEPGC3UY7hc+v1MZG/RBhe62uk"} {
			if status, v := p.confirm(fp, pageNets); status != http.StatusConflict {
				t.Errorf("confirm with the fingerprint %q: %d %v; want 409", fp, status, v)
			}
		}
		pager.nothingSent(t, "after confirms of other fingerprints")
		pager.noPasswordYet(t, "after confirms of other fingerprints")
		p.unchangedDevices("after confirms of other fingerprints")
		p.noPassword()
	})

	t.Run("refused: another port, a second pairing, a body that isn't JSON", func(t *testing.T) {
		pager := newFakePager(t, "to-tool/valid/prov_result-ok.txt")
		other := newFakePager(t, "to-tool/valid/prov_result-ok.txt") // a pty the page doesn't list
		p := newPairPage(t, pager.tty)
		for _, port := range []string{other.tty, "/etc/hosts", "", pager.tty + "/../" + filepath.Base(pager.tty)} {
			if status, v := p.post("/api/pair", map[string]string{"port": port, "name": "pager-01"}); status != http.StatusBadRequest {
				t.Errorf("POST /api/pair through %q: %d %v; want 400", port, status, v)
			}
		}
		other.nothingSent(t, "a port the page doesn't list")
		if status, v := p.post("/api/pair", map[string]string{"port": pager.tty, "name": "pager 01"}); status != http.StatusBadRequest {
			t.Errorf("POST /api/pair with the name \"pager 01\": %d %v; want 400", status, v)
		}

		p.pair(pager.tty)
		pager.offered(t)
		if status, v := p.post("/api/pair", map[string]string{"port": pager.tty, "name": "pager-01"}); status != http.StatusConflict {
			t.Errorf("a second POST /api/pair while one waits: %d %v; want 409", status, v)
		}
		// A body that isn't the page's JSON: the answer never quotes it.
		req := p.request(http.MethodPost, "/api/pair/confirm", nil)
		req.Body = io.NopCloser(strings.NewReader(`{"fingerprint":"` + pagerFP + `","wifi":[{"ssid":"HomeNet24","pass":"tangerine-otter-42"x}]}`))
		if status, v := p.answer(p.do(req)); status != http.StatusBadRequest {
			t.Errorf("a confirm that isn't JSON: %d %v; want 400", status, v)
		}
		// The page's guard (task 3.2): a POST without the page's Origin.
		req = p.request(http.MethodPost, "/api/pair/confirm", func(r *http.Request) { r.Header.Del("Origin") })
		if resp := p.do(req); resp.StatusCode != http.StatusForbidden {
			t.Errorf("a confirm with no Origin: %d; want 403", resp.StatusCode)
		}
		pager.nothingSent(t, "after the refusals")
		pager.noPasswordYet(t, "after the refusals")
		p.noPassword()
	})
}

func anyStrings(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, x := range l {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}
