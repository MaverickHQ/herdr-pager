package emutest

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// quietScenario is fakeherdr's: two agents that page nothing, so no flash or
// tone lands in a screenshot.
const quietScenario = `
[[agent]]
pane = "w1:p1"
kind = "claude"
name = "api-refactor"
status = "working"

[[agent]]
pane = "w1:p2"
kind = "claude"
name = "tests"
status = "idle"
`

// Masks, in landscape pixels (the 28 px status band, then the 144 px body).
var (
	// The pairing screen's title names the Mac (scutil's LocalHostName): not in a golden.
	maskPairingTitle = image.Rect(0, 28, 640, 60)
	// The device's fingerprint: its key is made at random on first boot.
	maskPairingDev = image.Rect(0, 112, 640, 172)
	// The status band's text holds the clock, set from the bridge's time; the test reads that text instead.
	// It ends where the status row does, so the hint cells stay in the goldens.
	maskStatus = image.Rect(0, 0, statusRight, 27)
	// The list's rows are lane goldens' (screen_list), with their own goldens.
	maskBody = image.Rect(0, 28, 640, 172)
)

// pairEmulator pairs a fresh instance with b over its virtual serial port, as
// the owner would: provision, both fingerprints checked on screen, BOOT. It
// returns the instance once provision has succeeded, and the device's fingerprint.
func pairEmulator(t *testing.T, b *Bridge, confirmGolden string) (*Instance, string) {
	t.Helper()
	in, devFP, _ := pairEmulatorWith(t, b, Options{}, confirmGolden)
	return in, devFP
}

// pairEmulatorWith is pairEmulator for an instance that boots as opt says,
// returning provision too.
func pairEmulatorWith(t *testing.T, b *Bridge, opt Options, confirmGolden string) (*Instance, string, *Provisioning) {
	t.Helper()
	in := Start(t, opt)
	in.AirNetworks(EmuNetwork)
	in.WaitText("PAIR VIA USB", 10*time.Second)
	p := b.Provision(in.Serial, "emu-01", "127.0.0.1", EmuNetwork)
	devFP, hostFP := p.Fingerprints(30 * time.Second)
	if want := ssh.FingerprintSHA256(b.HostKey); hostFP != want {
		t.Fatalf("provision printed the bridge's key as %s, want %s", hostFP, want)
	}
	in.WaitText("HOST "+hostFP, 10*time.Second)
	in.WaitText("DEV "+devFP, 5*time.Second)
	// Before pairing, none of the status applies: the band is blank, as on the boot screen.
	if st := in.Status(); len(st) != 0 {
		t.Fatalf("the pairing confirmation's band says %q; want it blank", st)
	}
	if confirmGolden != "" {
		in.MatchGoldenBlanked(confirmGolden, maskPairingTitle, maskPairingDev) // the band too: it's blank
		in.Lint()
	}
	in.PressRestarting("boot", 100)
	if err := p.Wait(30 * time.Second); err != nil {
		t.Fatalf("provision: %v\n%s", err, p.Out)
	}
	return in, devFP, p
}

// paired is a pager paired with b and connected: the list is up.
func paired(t *testing.T, b *Bridge) *Instance {
	t.Helper()
	return pairedOn(t, b, Options{})
}

// pairedOn is paired for an instance that boots as opt says, such as on
// battery, with pairEmulator's checks: both fingerprints, and a blank band.
func pairedOn(t *testing.T, b *Bridge, opt Options) *Instance {
	t.Helper()
	in, _, _ := pairEmulatorWith(t, b, opt, "")
	in.WaitText("o lcd349-emu", 60*time.Second)
	return in
}

// maxBackoff is the longest wait between dials, 30 s plus its 20 % jitter (parent 3.4), and a little.
const maxBackoff = 37 * time.Second

// TestPairingOverVirtualSerial: a fresh pager and pager-bridge provision over
// its virtual serial port. The pager shows both fingerprints (the ones
// provision prints), BOOT pairs it, provision adds it to devices.toml, and the
// pager restarts paired, joins the network, dials the bridge and shows the
// list (parent 2.1-2.6, 19.2).
func TestPairingOverVirtualSerial(t *testing.T) {
	b := StartBridge(t, quietScenario)
	in, _ := pairEmulator(t, b, "pairing/confirm.png")

	devices, err := os.ReadFile(filepath.Join(b.Dir, "devices.toml"))
	if err != nil || !strings.Contains(string(devices), `"emu-01"`) {
		t.Fatalf("devices.toml doesn't hold emu-01 (%v):\n%s", err, devices)
	}
	// Restarted paired: the network, the link up (o), and the snapshot's counts (one working agent).
	in.WaitText("o lcd349-emu", 60*time.Second)
	in.WaitText("ALL CLEAR >1", 10*time.Second)
	in.MatchGoldenBlanked("pairing/paired_list.png", maskStatus, maskBody)
	in.Lint()
}

// TestResetChordOnPairViaUSB: both buttons for 10 s on PAIR VIA USB ask
// FACTORY RESET? (open-items O-28), and the band stays blank under it, as on
// the pairing screens (O-32): no counts and no NO WIFI before pairing. PWR
// cancels, back to PAIR VIA USB.
func TestResetChordOnPairViaUSB(t *testing.T) {
	in := Start(t, Options{})
	in.WaitText("PAIR VIA USB", 10*time.Second)
	in.Press("boot+pwr", 10500)
	in.WaitText("FACTORY RESET?", 5*time.Second)
	if h := in.Tree().Hints(); len(h) != 2 || h[0].Text != "CANCEL" || h[1].Text != "RESET" {
		t.Fatalf("the question's hint cells, PWR then BOOT: %+v; want CANCEL, then RESET", h)
	}
	if st := in.Status(); len(st) != 0 {
		t.Fatalf("FACTORY RESET? over PAIR VIA USB: the band says %q; want it blank", st)
	}
	in.Press("pwr", 100)
	in.WaitText("PAIR VIA USB", 5*time.Second)
	if st := in.Status(); len(st) != 0 {
		t.Fatalf("PAIR VIA USB again: the band says %q; want it blank", st)
	}
}

// TestDeviceKeyIsMadeWithTheRadioOn: a fresh pager makes its device key only
// once its radio is on, the entropy source that makes the board's RNG truly
// random, and logs the path taken (parent design §Provisioning, T12; parent
// task 6.2's entropy check). The radio goes off again after: an unpaired pager
// has no network saved, and never starts its station with none.
func TestDeviceKeyIsMadeWithTheRadioOn(t *testing.T) {
	in := Start(t, Options{})
	in.WaitText("PAIR VIA USB", 10*time.Second)

	data, err := os.ReadFile(filepath.Join(in.Dir, "serial.log"))
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	radio := strings.Index(log, "prov: device key: the radio is on")
	key := strings.Index(log, "prov: new device key: stored")
	if radio < 0 || key < radio {
		t.Fatalf("the device key wasn't made after the radio came on (at bytes %d and %d):\n%s", radio, key, log)
	}
	// The kit's scan with no network saved ends after 1 s, and a retry of the join would come 1 s
	// after that (platform/pump.c), so 4 s is enough to see one.
	time.Sleep(4 * time.Second)
	if n := in.Count(`settings: wifi: [0-9]+ networks`); n != 0 {
		t.Fatalf("an unpaired pager started its station %d times with no network saved", n)
	}
}

// TestHostKeyChangedWaitsToRetry: the bridge comes back with another host
// key. The pager refuses it, shows HOST KEY CHANGED - RE-PAIR VIA USB, and
// doesn't dial again within the backoff's longest wait: it tries again only
// after 5 minutes, which the host tests check
// (test_host_key_changed_retries_every_five_minutes; parent 3.3, as the
// security pass amended it, 2026-11-01).
func TestHostKeyChangedWaitsToRetry(t *testing.T) {
	b := StartBridge(t, quietScenario)
	in := paired(t, b)
	b.ChangeHostKey()
	in.WaitText("HOST KEY CHANGED", 30*time.Second)
	in.MatchGoldenBlanked("pairing/host_key_changed.png", maskStatus)
	in.Lint()

	dials := in.Count(`dialling the bridge`)
	time.Sleep(maxBackoff)
	if n := in.Count(`dialling the bridge`); n != dials {
		t.Fatalf("the pager dialled %d more times after HOST KEY CHANGED", n-dials)
	}
	if n := in.Count(`HOST KEY CHANGED`); n != 1 {
		t.Fatalf("the pager refused the host key %d times, want once", n)
	}
}

// TestRevokedShowsNotAuthorised: `pager-bridge devices revoke` while the
// pager is connected. The bridge drops its session within 2 s (D2-12), the
// pager redials after its first backoff (1 s, up to 1.2 s with jitter) and is
// refused: NOT AUTHORISED (parent 3.5, 12.2).
func TestRevokedShowsNotAuthorised(t *testing.T) {
	const budget = 2*time.Second + 1200*time.Millisecond
	const slack = time.Second // the dial itself, and the harness's polling
	b := StartBridge(t, quietScenario)
	in := paired(t, b)
	start := time.Now()
	if out, err := b.Command("devices", "revoke", "emu-01").CombinedOutput(); err != nil {
		t.Fatalf("pager-bridge devices revoke: %v\n%s", err, out)
	}
	in.WaitText("NOT AUTHORISED", 10*time.Second)
	took := time.Since(start)
	if took > budget+slack {
		t.Fatalf("NOT AUTHORISED came %v after the revoke, want within %v (and %v of slack)", took, budget, slack)
	}
	t.Logf("NOT AUTHORISED %v after the revoke", took.Round(time.Millisecond))
	in.MatchGoldenBlanked("pairing/not_authorised.png", maskStatus)
	in.Lint()
}

// TestReconnectsAfterBridgeRestart: serve stops while the pager is connected,
// and starts again 3 s later. The pager shows the link down, redials with
// backoff, and is back, with a fresh snapshot, within N3's 35 s (parent 3.4).
func TestReconnectsAfterBridgeRestart(t *testing.T) {
	b := StartBridge(t, quietScenario)
	in := paired(t, b)
	dials := in.Count(`dialling the bridge`)
	b.StopServe()
	in.WaitText("~ lcd349-emu", 10*time.Second)
	time.Sleep(3 * time.Second)
	b.StartServe()
	back := time.Now()
	in.WaitText("o lcd349-emu", 35*time.Second)
	in.WaitText("ALL CLEAR >1", 5*time.Second)
	t.Logf("connected again %v after serve restarted, after %d failed dials",
		time.Since(back).Round(time.Millisecond), in.Count(`dialling the bridge`)-dials-1)
}

// TestNoBridgeSaysWhy: the owner's report (open-items O-77). On a phone's
// hotspot without the Mac, a pager switched on there showed only the band's
// "~" and the network over an empty body, and PWR and BOOT seemed to do
// nothing. Here serve stops, as the Mac's absence would, and the pager is
// switched off and on: the body says NO BRIDGE ON THIS NETWORK once a dial
// fails, and asks whether the Mac is on it; PWR shows device info, which says
// it was switched on and where it dials, and PWR again comes back. Golden.
func TestNoBridgeSaysWhy(t *testing.T) {
	b := StartBridge(t, quietScenario)
	in := paired(t, b)
	in.WaitText("ALL CLEAR >1", 10*time.Second)
	b.StopServe()
	in.WaitText("LAST SEEN", 15*time.Second)
	for _, op := range []string{"off", "on"} {
		if out, err := lcd("power", in.ID, op); err != nil {
			t.Fatalf("lcd349 power %s: %v\n%s", op, err, out)
		}
	}
	in.WaitText("NO BRIDGE ON THIS NETWORK", 30*time.Second)
	in.WaitText("Is the Mac on lcd349-emu too?", time.Second)
	if h := in.Tree().Hints(); len(h) != 2 || h[0].Text != "INFO" || h[1].Text != "HOLD: MENU" {
		t.Fatalf("the hint cells, PWR then BOOT: %+v; want INFO, then HOLD: MENU", h)
	}
	in.MatchGoldenBlanked("pairing/no_bridge.png", in.Masks()...)
	in.Lint()

	in.Press("pwr", 100)
	in.WaitText("STARTED: POWER ON", 5*time.Second)
	in.WaitText("BRIDGE 127.0.0.1:"+strconv.Itoa(b.Port), time.Second)
	in.Press("pwr", 100)
	in.WaitText("NO BRIDGE ON THIS NETWORK", 5*time.Second)
}

// TestNoWifiShowsStateAndPagesNothing: a pager paired with a network that
// isn't in range. The emulator's sockets would reach the bridge anyway (they
// ignore the simulated Wi-Fi), so it's the product that must not start the
// link: no dial, NO WIFI in the band, and so no snapshot and no page, though
// the bridge has a blocked agent (parent 16.4).
func TestNoWifiShowsStateAndPagesNothing(t *testing.T) {
	b := StartBridge(t, `
[[agent]]
pane = "w1:p1"
kind = "claude"
name = "api-refactor"
status = "blocked"
`)
	in := Start(t, Options{}) // no networks in range
	in.WaitText("PAIR VIA USB", 10*time.Second)
	p := b.Provision(in.Serial, "emu-01", "127.0.0.1", EmuNetwork)
	p.Fingerprints(30 * time.Second)
	in.WaitText("PAIR WITH", 10*time.Second)
	in.PressRestarting("boot", 100)
	if err := p.Wait(30 * time.Second); err != nil {
		t.Fatalf("provision: %v\n%s", err, p.Out)
	}
	in.WaitText("~ NO WIFI", 30*time.Second)
	// Past the Wi-Fi's scan (about 1.4 s in the emulator) and a page's 3 s debounce, had anything come.
	time.Sleep(5 * time.Second)
	if n := in.Count(`dialling the bridge`); n != 0 {
		t.Fatalf("the pager dialled the bridge %d times with no Wi-Fi", n)
	}
	if strings.Contains(b.Log.String(), "emu-01") {
		t.Fatalf("the bridge saw the pager:\n%s", b.Log)
	}
	in.WaitText("!0 *0 >0", time.Second) // no snapshot, so nothing to page from
	in.WaitText("~ NO WIFI", time.Second)
	in.WaitText("Looking for a saved network", time.Second) // the empty list says why (O-77)
	// The clock is the instance's RTC, from DefaultClock: nothing syncs it without a link, but it
	// still turns, so its label (the clock, the link and the network, read above) is masked. The body
	// is the empty list's NO WIFI, the same every time, so the golden shows it (open-items O-86).
	in.MatchGoldenBlanked("pairing/no_wifi.png", in.Masks()...)
	in.Lint()
}

// TestConnectingSaysWhere: on a network, before any dial has failed, the empty
// list says CONNECTING and the address it dials (open-items O-77, O-86). The
// pager is paired to dial 192.0.2.1 (TEST-NET-1, RFC 5737), which never
// answers, so the first dial waits out its 10 s and the screen holds still.
// Golden.
func TestConnectingSaysWhere(t *testing.T) {
	b := StartBridge(t, quietScenario)
	in := Start(t, Options{})
	in.AirNetworks(EmuNetwork)
	in.WaitText("PAIR VIA USB", 10*time.Second)
	p := b.Provision(in.Serial, "emu-01", "192.0.2.1", EmuNetwork)
	p.Fingerprints(30 * time.Second)
	in.WaitText("PAIR WITH", 10*time.Second)
	in.PressRestarting("boot", 100)
	if err := p.Wait(30 * time.Second); err != nil {
		t.Fatalf("provision: %v\n%s", err, p.Out)
	}
	in.WaitText("to the bridge at 192.0.2.1", 30*time.Second)
	in.WaitText("CONNECTING", time.Second)
	in.MatchGoldenBlanked("pairing/connecting.png", in.Masks()...)
	in.Lint()
}

// TestUnreadableDeviceKeyIsKept: a stored device key the kit can't read (here
// a string where the key's blob belongs) isn't replaced by a new one, which
// would lose a pairing without a word (open-items O-07). The pager makes no
// key, leaves its storage as it was, and shows NO DEVICE KEY; the way out is
// the factory-reset chord.
func TestUnreadableDeviceKeyIsKept(t *testing.T) {
	dir := t.TempDir()
	path := SeedStorage(t, dir, "pager", KV{Key: "dev_key", Type: KVStr, Data: []byte("not a key")})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	in := Start(t, Options{Storage: dir})
	in.WaitText("NO DEVICE KEY", 10*time.Second) // after the device key is read, or made
	if n := in.Count(`new device key`); n != 0 {
		t.Fatalf("the pager made a new device key over the one it couldn't read (%d lines)", n)
	}
	in.WaitText("Hold BOOT and PWR 10 s to reset", 2*time.Second) // the way out, not provision
	in.MatchGoldenBlanked("pairing/no_device_key.png")
	in.Lint()
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, before) {
		t.Fatalf("the pager changed its storage (%v):\nbefore %q\nafter  %q", err, before, after)
	}
}

// TestPairedWithoutKeyShowsNoDeviceKey: a paired pager whose stored device key
// can't be read shows NO DEVICE KEY, as an unpaired one does, and never dials:
// its network joins, but there's no key to dial with, and it makes none
// (open-items O-07). Its storage stays as it was until the factory-reset
// chord, the way out: FACTORY RESET? over it with a blank band, then RESET
// erases everything, and the pager starts again unpaired with a new key.
func TestPairedWithoutKeyShowsNoDeviceKey(t *testing.T) {
	dir := t.TempDir()
	str := func(k, v string) KV { return KV{Key: k, Type: KVStr, Data: []byte(v)} }
	path := SeedStorage(t, dir, "pager",
		str("name", "emu-01"), str("host", "127.0.0.1"), str("port", "2277"), str("host_name", "studio-mac"),
		str("host_key", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIITD0UXh5rvWrJ9ECHIk1KDIInpniagO0TKg6w/6c/tO"),
		str("wifi_n", "1"), str("wifi0_ssid", EmuNetwork.SSID), str("wifi0_pass", EmuNetwork.Pass),
		str("dev_key", "not a key")) // a string where the key's blob belongs: unreadable as a key
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	in := Start(t, Options{Storage: dir})
	in.AirNetworks(EmuNetwork)
	in.WaitText("NO DEVICE KEY", 10*time.Second)
	if n := in.Count(`settings: wifi: 1 networks`); n != 1 {
		t.Fatalf("the paired pager started its stored network %d times; want once", n)
	}
	time.Sleep(6 * time.Second) // the join takes about 1 s; a dial would follow it at once
	if n := in.Count(`dialling the bridge`); n != 0 {
		t.Fatalf("the pager dialled the bridge %d times with no device key", n)
	}
	if n := in.Count(`new device key`); n != 0 {
		t.Fatalf("the pager made a new device key over the one it couldn't read (%d lines)", n)
	}
	if st := in.Status(); len(st) != 0 {
		t.Fatalf("NO DEVICE KEY's band says %q; want it blank", st)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, before) {
		t.Fatalf("the pager changed its storage (%v)", err)
	}

	in.Press("boot+pwr", 10500)
	in.WaitText("FACTORY RESET?", 5*time.Second)
	if st := in.Status(); len(st) != 0 {
		t.Fatalf("FACTORY RESET? over NO DEVICE KEY: the band says %q; want it blank", st)
	}
	in.PressRestarting("boot", 100) // RESET
	in.WaitText("PAIR VIA USB", 20*time.Second)
	for deadline := time.Now().Add(10 * time.Second); in.Count(`new device key: stored`) == 0; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("after the factory reset, the pager made no new device key")
		}
	}
}

// TestWifiJoinRetriesWithBackoff: a paired pager whose network isn't in
// range tries the join again 1, 2, 4 and 8 s after each failure (parent design
// §Error handling's network backoff; platform/pump.c), and once it has joined,
// a network lost later is tried again from 1 s. In the emulator each try is a
// 1 s scan (the kit's SCAN_MS), and the retry is made at the pump's next
// once-a-second check, so tries come 2-3, 3-4, 5-6 and 9-10 s apart.
func TestWifiJoinRetriesWithBackoff(t *testing.T) {
	b := StartBridge(t, quietScenario)
	in := Start(t, Options{}) // no networks in range
	in.WaitText("PAIR VIA USB", 10*time.Second)
	p := b.Provision(in.Serial, "emu-01", "127.0.0.1", EmuNetwork)
	p.Fingerprints(30 * time.Second)
	in.WaitText("PAIR WITH", 10*time.Second)
	in.PressRestarting("boot", 100)
	if err := p.Wait(30 * time.Second); err != nil {
		t.Fatalf("provision: %v\n%s", err, p.Out)
	}

	// The boot's join, then four retries.
	// The upper bound has 0.4 s for a busy host's timing: on CI's shared Mac,
	// the v1.0.0-rc.1 tag's run saw the 8 s retry 10.17 s on. It stays below
	// where the next step's would start, and the lower bound, which proves each
	// wait, is exact.
	at := joinTries(t, in, 5, 40*time.Second)
	for i, retry := range []float64{1, 2, 4, 8} {
		gap := at[i+1].Sub(at[i]).Seconds()
		if lo, hi := retry+0.9, retry+2.5; gap < lo || gap > hi {
			t.Fatalf("try %d came %.2f s after the one before; want %.1f-%.1f (a %.0f s wait, then a 1 s scan)",
				i+2, gap, lo, hi, retry)
		}
	}

	// Joined, then the network goes: the backoff starts again at 1 s, so two
	// tries come within 6 s (with the backoff where it was, the next would be 16 s on).
	in.AirNetworks(EmuNetwork)
	in.WaitText("lcd349-emu", 15*time.Second)
	n := in.Count(joinTry)
	in.AirNetworks()
	time.Sleep(6 * time.Second)
	if got := in.Count(joinTry) - n; got < 2 {
		t.Fatalf("%d tries in the 6 s after the joined network went; want 2 or more, from 1 s", got)
	}
}

// TestRefusedJoinTriedAgainAtOnce: a join the access point refuses (here the
// network on the air has another password) is tried again at once, three
// refusals in a row, then with the backoff, so a boot that meets two refusals
// still shows the list within N7's 15 s (the owner's decision, 2026-10-27,
// open-items O-45).
func TestRefusedJoinTriedAgainAtOnce(t *testing.T) {
	in := pairedOutOfRange(t)
	in.AirNetworks(wrongPassword)

	const refused = `wifi: join refused`
	for deadline := time.Now().Add(60 * time.Second); in.Count(refused) < 4; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d refused joins in 60 s; want 4", in.Count(refused))
		}
	}
	if n := in.Count(refused + `; trying again at once`); n != 3 {
		t.Fatalf("%d of the first 4 refused joins were tried again at once; want 3, then the backoff", n)
	}
}

// TestWrongPasswordShowsWifiFailed: a network that refuses every join (its
// password isn't the one saved) shows WIFI <ssid> FAILED in NO WIFI's place
// after three refusals in a row, until a join goes through (parent design
// §Error handling; the owner's decision, 2026-10-27, open-items O-49).
func TestWrongPasswordShowsWifiFailed(t *testing.T) {
	in := pairedOutOfRange(t)
	failed := "WIFI " + EmuNetwork.SSID + " FAILED"
	in.AirNetworks(wrongPassword)
	in.WaitText(failed, 30*time.Second)
	in.AirNetworks(EmuNetwork) // the password put right
	in.WaitText("o "+EmuNetwork.SSID, 60*time.Second)
	if st := strings.Join(in.Status(), " "); strings.Contains(st, "FAILED") {
		t.Fatalf("joined, and the band still says %q", st)
	}
}

// wrongPassword is EmuNetwork on the air with a password the pager hasn't saved:
// the kit's emulator refuses every join to it (LCD349_WIFI_REASON_AUTH).
var wrongPassword = Network{SSID: EmuNetwork.SSID, Pass: EmuNetwork.Pass + "-not"}

// pairedOutOfRange pairs a fresh pager with EmuNetwork saved but not on the
// air, and returns once the paired boot has tried to join it.
func pairedOutOfRange(t *testing.T) *Instance {
	t.Helper()
	b := StartBridge(t, quietScenario)
	in := Start(t, Options{}) // no networks in range
	in.WaitText("PAIR VIA USB", 10*time.Second)
	p := b.Provision(in.Serial, "emu-01", "127.0.0.1", EmuNetwork)
	p.Fingerprints(30 * time.Second)
	in.WaitText("PAIR WITH", 10*time.Second)
	in.PressRestarting("boot", 100)
	if err := p.Wait(30 * time.Second); err != nil {
		t.Fatalf("provision: %v\n%s", err, p.Out)
	}
	joinTries(t, in, 1, 20*time.Second)
	return in
}

// joinTry is the pump's log line for each try of the Wi-Fi join (settings_start_wifi).
const joinTry = `settings: wifi: [0-9]+ networks`

// joinTries waits for n tries of the join in the instance's serial log, and
// returns their times as the log stamps them (milliseconds since boot).
func joinTries(t *testing.T, in *Instance, n int, timeout time.Duration) []time.Time {
	t.Helper()
	line := regexp.MustCompile(`\((\d+)\) ` + joinTry)
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(filepath.Join(in.Dir, "serial.log"))
		if err != nil {
			t.Fatal(err)
		}
		var at []time.Time
		for _, m := range line.FindAllStringSubmatch(string(data), -1) {
			ms, _ := strconv.ParseInt(m[1], 10, 64)
			at = append(at, time.UnixMilli(ms))
		}
		if len(at) >= n {
			return at[:n]
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d tries of the join in %s, want %d", len(at), timeout, n)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestPairAgainOverTheVirtualSerial: a paired pager, and provision started
// first, waiting for it (as the owner is told to, since opening a board's port
// resets it). The menu's Pair again starts the pager's prov_hello lines, and
// pairing completes with the same device key (parent 2.10, D2-13).
func TestPairAgainOverTheVirtualSerial(t *testing.T) {
	b := StartBridge(t, "# no agents: BOOT held on the list opens the global menu\n")
	in, firstFP := pairEmulator(t, b, "")
	in.WaitText("o lcd349-emu", 60*time.Second)

	p := b.Provision(in.Serial, "emu-01", "127.0.0.1", EmuNetwork)
	waitFor(t, p.Out, `Waiting up to \d+ s`, 10*time.Second, "provision")
	// The global menu: SILENT, VOLUME, DONE TONE, BRIGHTNESS, COLOUR, DEVICE INFO, PAIR AGAIN (app/menu.c).
	in.Press("boot", 1600)
	in.WaitText("SELECT", 5*time.Second)
	for range 6 {
		in.Press("boot", 100)
	}
	in.Press("pwr", 100)
	in.WaitText("YES", 5*time.Second) // PAIR AGAIN? with YES under BOOT
	in.Press("boot", 100)

	devFP, _ := p.Fingerprints(30 * time.Second)
	if devFP != firstFP {
		t.Fatalf("Pair again offered the device key %s, want the one it had, %s", devFP, firstFP)
	}
	in.WaitText("DEV "+devFP, 10*time.Second)
	in.PressRestarting("boot", 100)
	if err := p.Wait(30 * time.Second); err != nil {
		t.Fatalf("provision: %v\n%s", err, p.Out)
	}
	in.WaitText("o lcd349-emu", 60*time.Second)
}

// TestNoWifiPasswordInLogs: after a pairing (8.3's), no log holds the Wi-Fi
// password: not the instance's (its serial log, its supervisor's), not
// serve's, not provision's output (parent 2.3, 11.5). The instance's storage
// is where the password belongs, so it isn't searched.
func TestNoWifiPasswordInLogs(t *testing.T) {
	b := StartBridge(t, quietScenario)
	in, _, p := pairEmulatorWith(t, b, Options{}, "")
	in.WaitText("o lcd349-emu", 60*time.Second)
	in.WaitText("ALL CLEAR >1", 10*time.Second)

	logs := map[string]string{"serve's log": b.Log.String(), "provision's output": p.Out.String()}
	entries, err := os.ReadDir(in.Dir)
	if err != nil {
		t.Fatal(err)
	}
	searched := 0
	for _, e := range entries {
		if e.IsDir() || !strings.Contains(e.Name(), ".log") {
			continue // storage/; devstate.json (the simulated access points, the test's own); the socket; the framebuffer
		}
		data, err := os.ReadFile(filepath.Join(in.Dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		logs["the instance's "+e.Name()] = string(data)
		searched++
	}
	if !strings.Contains(logs["the instance's serial.log"], "prov_result") {
		t.Fatalf("the instance's serial.log isn't the pairing's (searched %d files in %s)", searched, in.Dir)
	}
	for name, text := range logs {
		if strings.Contains(text, EmuNetwork.Pass) {
			t.Errorf("%s holds the Wi-Fi password", name)
		}
	}
	t.Logf("searched serve's log and %d of the instance's files", searched)
}
