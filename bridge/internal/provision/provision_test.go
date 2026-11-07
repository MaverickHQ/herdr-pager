package provision

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
)

// The fixture keys' full fingerprints (testdata/provisioning/README.md).
const (
	deviceFP = "SHA256://a7FlX9Y4t0WSS+W8ANyY4w+5+axSEVbGzNXp6txjY"
	hostFP   = "SHA256:dsn3PXzY9otII88ZqYD/4URNANyDaFaYUI57pc+mMe0"
)

// fixture is a provisioning fixture's line, its newline included.
func fixture(t *testing.T, file string) []byte {
	t.Helper()
	return append(readLine(t, file), '\n')
}

// A device is the fake serial peer's script (seam S2-5): the pager on the far
// side of the port, speaking the provisioning fixtures (ADR 0011). It says
// hello until it gets a prov_offer, shows it (confirming), and then either
// answers the offer (offerResult) or is confirmed with BOOT (confirmed), and
// answers the prov_set that follows with result.
type device struct {
	hello string // the fixture it sends every 20 ms until it gets a prov_offer; "" for none
	// offerResult, when set, answers the prov_offer in place of BOOT: PWR
	// (cancelled), no button in 30 s (timeout), an older pager's invalid,
	// or busy.
	offerResult string
	// boot, when set, holds BOOT back until it closes; noBoot means nobody
	// presses it, and the pager says nothing more.
	boot   <-chan struct{}
	noBoot bool
	// afterBoot, when set, is what the pager says after BOOT in place of
	// waiting for the prov_set: its 30 s running out.
	afterBoot string
	result    string // the fixture it answers a prov_set with; "" for none
	// start, when set, holds back the hellos until it closes, sending only
	// the boot log the reset that opening the port causes prints.
	start <-chan struct{}
	// backlog, when set, is written first: what a pager's USB console can
	// hold when nothing read it for a while.
	backlog string
	// echo, when set, writes back the prov_set it got after this prefix, as
	// a console that echoes its input would.
	echo string
}

// bootLog is what an ESP32-S3 prints as it resets: log lines to the tool.
const bootLog = "ESP-ROM:esp32s3-20210327\r\nrst:0x1 (POWERON),boot:0x8 (SPI_FAST_FLASH_BOOT)\r\nI (27) boot: ESP-IDF v5.5.5 2nd stage bootloader\r\n"

// A received is what the fake device has been sent: every line, log or
// provisioning, as it arrived.
type received struct {
	mu    sync.Mutex
	lines [][]byte
}

func (r *received) add(line []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
}

// all is every byte received so far, its lines joined.
func (r *received) all() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Join(r.lines, []byte("\n"))
}

// play runs d on the pty's peer side. It returns the provisioning lines the
// tool sends, as they arrive: the prov_offer, then the prov_set.
func (d device) play(t *testing.T, peer *os.File) <-chan []byte {
	sent, _ := d.playAll(t, peer)
	return sent
}

// playAll is play, also returning every byte the device is sent.
func (d device) playAll(t *testing.T, peer *os.File) (<-chan []byte, *received) {
	t.Helper()
	var hello []byte
	if d.hello != "" {
		hello = fixture(t, d.hello)
	}
	line := func(file string) []byte {
		if file == "" {
			return nil
		}
		return fixture(t, file)
	}
	offerResult, afterBoot, result := line(d.offerResult), line(d.afterBoot), line(d.result)
	confirming, confirmed := fixture(t, "to-tool/valid/prov_hello-confirming.txt"), fixture(t, "to-tool/valid/prov_hello-confirmed.txt")
	logLine := []byte("I (1234) pager: a log line, which the tool ignores\r\n")

	sent := make(chan []byte, 16)
	got := &received{}
	offers, sets := make(chan []byte, 1), make(chan []byte, 1)
	go func() {
		sc := bufio.NewScanner(peer)
		n := 0
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			got.add(line)
			if !bytes.HasPrefix(line, []byte(Prefix)) {
				continue
			}
			sent <- line
			switch n++; n {
			case 1:
				offers <- line
			case 2:
				sets <- line
			}
		}
	}()
	go func() {
		if d.backlog != "" {
			peer.Write([]byte(d.backlog))
		}
		if d.start != nil {
			peer.Write([]byte(bootLog))
			<-d.start
		}
		for {
			if _, err := peer.Write(append(logLine, hello...)); err != nil {
				return // the test is over
			}
			select {
			case <-offers:
			case <-time.After(20 * time.Millisecond):
				continue
			}
			break
		}
		peer.Write(confirming)
		switch {
		case offerResult != nil:
			peer.Write(offerResult)
			return
		case d.noBoot:
			return
		case d.boot != nil:
			<-d.boot
		}
		peer.Write(confirmed)
		if afterBoot != nil {
			peer.Write(afterBoot)
			return
		}
		select {
		case set := <-sets:
			if d.echo != "" {
				peer.Write([]byte(d.echo + string(set) + "\r\n"))
			}
			if result != nil {
				peer.Write(result)
			}
		case <-time.After(10 * time.Second):
		}
	}()
	return sent, got
}

// yes is Options.Confirm for an owner who says the fingerprints match.
func yes(context.Context) (bool, error) { return true, nil }

// bridge is a bridge's side of provisioning: its host key (the fixtures')
// and a devices.toml with no devices.
func bridge(t *testing.T) (hostKey ssh.PublicKey, devicesPath string) {
	t.Helper()
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIITD0UXh5rvWrJ9ECHIk1KDIInpniagO0TKg6w/6c/tO"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), devices.File)
	if err := devices.Create(path); err != nil {
		t.Fatal(err)
	}
	return key, path
}

// options are the flags that give the fixture prov_set.txt: pager-01 on one
// network, dialling studio-mac.local:2277.
func options(t *testing.T, port string, hostKey ssh.PublicKey, devicesPath string, out *bytes.Buffer) Options {
	return Options{
		Port:          port,
		Name:          "pager-01",
		Networks:      []Network{{SSID: "HomeNet-2G", Pass: "correct-horse-battery"}},
		Host:          "studio-mac.local",
		BridgePort:    2277,
		HostKey:       hostKey,
		Devices:       devicesPath,
		Out:           out,
		LocalHostName: func() (string, error) { return "studio-mac", nil },
		Confirm:       yes,
	}
}

// TestProvisionOk pairs with a fake device that accepts (parent 2.3, 2.4,
// 2.6; ADR 0011): the tool sends the fixture prov_offer after the device's
// hello, and the fixture prov_set after its BOOT and the owner's yes; it
// prints both full fingerprints, and adds the device to devices.toml on its
// ok.
func TestProvisionOk(t *testing.T) {
	peer, tty := openPeer(t)
	sent := device{hello: "to-tool/valid/prov_hello.txt", result: "to-tool/valid/prov_result-ok.txt"}.play(t, peer)
	hostKey, path := bridge(t)
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	before := time.Now().Truncate(time.Second)
	if err := Run(ctx, options(t, tty, hostKey, path, &out)); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	t.Log("\n" + out.String())

	for _, file := range []string{"prov_offer.txt", "prov_set.txt"} {
		select {
		case line := <-sent:
			if want := readLine(t, "to-device/valid/"+file); !bytes.Equal(line, want) {
				t.Errorf("the tool sent\n%s\nwant the fixture %s\n%s", line, file, want)
			}
		case <-time.After(time.Second):
			t.Errorf("the tool sent no line like %s", file)
		}
	}
	for _, fp := range []string{deviceFP, hostFP} {
		if !strings.Contains(out.String(), fp) {
			t.Errorf("the output doesn't give the full fingerprint %s", fp)
		}
	}

	devs, err := devices.Load(path)
	if err != nil || len(devs) != 1 {
		t.Fatalf("devices.toml holds %d devices (%v); want pager-01", len(devs), err)
	}
	d := devs[0]
	key := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAnULfN18UYEHFPLj/PhfEMf7NvNdFoELzBJBtMsCVgj pager-01"
	if d.Name != "pager-01" || d.Key != key || d.Fingerprint() != deviceFP {
		t.Errorf("devices.toml has %q %q %s; want pager-01 %q %s", d.Name, d.Key, d.Fingerprint(), key, deviceFP)
	}
	if d.Added.Before(before) || d.Added.After(time.Now()) {
		t.Errorf("added %v; want the time it paired", d.Added)
	}
}

// TestProvisionSkipsDamagedLines checks what the desk checkpoint found
// (2026-10-24): a pager's USB console that nothing read for a while delivers
// a backlog with lines cut short or run together, so a provisioning line that
// isn't one JSON value is passed over like a log line, and the next whole one
// is read; the pager repeats its hello every 2 s.
func TestProvisionSkipsDamagedLines(t *testing.T) {
	peer, tty := openPeer(t)
	backlog := `@P {"t":"prov_hel` + "\r\n" + // cut short
		`@P {"t":"prov_hello","fw":"0.1.0-dev","pub@P {"t":"prov_hello"` + "\r\n" // two run together
	device{hello: "to-tool/valid/prov_hello.txt", result: "to-tool/valid/prov_result-ok.txt", backlog: backlog}.play(t, peer)
	hostKey, path := bridge(t)
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Run(ctx, options(t, tty, hostKey, path, &out)); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if devs, err := devices.Load(path); err != nil || len(devs) != 1 || devs[0].Name != "pager-01" {
		t.Fatalf("devices.toml holds %d devices (%v); want pager-01", len(devs), err)
	}
}

// TestProvisionRefusesOtherKeyTypes checks that only an ed25519 device key is
// accepted (parent 2.4, phase 2 R4.2): an ecdsa or rsa key in the device's
// hello is an error naming its type, before any prov_set is sent, even with
// a device that would answer ok; and devices.toml's own writer refuses such
// a key, so it can't be written by any path.
func TestProvisionRefusesOtherKeyTypes(t *testing.T) {
	for _, c := range []struct{ file, keyType string }{
		{"prov_hello-ecdsa-key.txt", "ecdsa-sha2-nistp256"},
		{"prov_hello-rsa-key.txt", "ssh-rsa"},
	} {
		t.Run(c.keyType, func(t *testing.T) {
			peer, tty := openPeer(t)
			hello := "to-tool/invalid/" + c.file
			sent := device{hello: hello, result: "to-tool/valid/prov_result-ok.txt"}.play(t, peer)
			hostKey, _ := bridge(t)
			path, before, mtime := pairedTwo(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			err := Run(ctx, options(t, tty, hostKey, path, &bytes.Buffer{}))
			t.Log(err)
			for _, s := range []string{"pubkey", "an " + c.keyType + " key", "only ssh-ed25519", "devices.toml is unchanged"} {
				if err == nil || !strings.Contains(err.Error(), s) {
					t.Errorf("Run: %v; want an error that says %q", err, s)
				}
			}
			select {
			case <-sent:
				t.Error("the tool sent a prov_set")
			case <-time.After(200 * time.Millisecond):
			}
			unchanged(t, path, before, mtime)

			// The key itself, offered straight to devices.toml's writer.
			var h struct{ PubKey string }
			if err := json.Unmarshal(bytes.TrimPrefix(readLine(t, hello), []byte(Prefix)), &h); err != nil {
				t.Fatal(err)
			}
			key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(h.PubKey))
			if err != nil {
				t.Fatal(err)
			}
			err = devices.Add(path, devices.Device{Name: "pager-03", Key: h.PubKey + " pager-03", PublicKey: key, Added: time.Now().UTC().Truncate(time.Second)})
			t.Log(err)
			if err == nil || !strings.Contains(err.Error(), c.keyType) {
				t.Errorf("devices.Add of an %s key: %v; want an error naming its type", c.keyType, err)
			}
			unchanged(t, path, before, mtime)
		})
	}
}

// A clock is a fake clock that the test moves on.
type clock struct {
	mu     sync.Mutex
	now    time.Time
	timers []timer
	waits  chan time.Duration // each wait as it starts
}

type timer struct {
	at time.Time
	c  chan time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC), waits: make(chan time.Duration, 8)}
}

func (c *clock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := timer{c.now.Add(d), make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	c.waits <- d
	return t.c
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// advance moves the clock on by d, firing the timers then due.
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	keep := c.timers[:0]
	for _, t := range c.timers {
		if t.at.After(c.now) {
			keep = append(keep, t)
		} else {
			t.c <- c.now
		}
	}
	c.timers = keep
}

// TestProvisionWaitsForPairAgain checks that provision opens the port and
// then waits for the device's hello (parent 2.10, phase 2 R4.4): opening the
// port resets the board, and the owner then chooses Pair again, so the first
// hello comes 3 s later, after the boot log, and pairing succeeds. With no
// hello in 120 s it fails, telling the owner to choose Pair again; at 119 s
// it's still waiting. Both on a fake clock.
func TestProvisionWaitsForPairAgain(t *testing.T) {
	t.Run("a hello 3 s after the port opens", func(t *testing.T) {
		peer, tty := openPeer(t)
		start := make(chan struct{})
		device{hello: "to-tool/valid/prov_hello.txt", result: "to-tool/valid/prov_result-ok.txt", start: start}.play(t, peer)
		hostKey, path := bridge(t)
		var out bytes.Buffer
		o := options(t, tty, hostKey, path, &out)
		clk := newClock()
		o.After, o.Now = clk.After, clk.Now
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		errc := make(chan error, 1)
		go func() { errc <- Run(ctx, o) }()
		if d := <-clk.waits; d != 120*time.Second {
			t.Fatalf("the first wait is %v; want 120 s for the hello", d)
		}
		clk.advance(3 * time.Second)
		close(start)
		if err := <-errc; err != nil {
			t.Fatalf("Run: %v\n%s", err, out.String())
		}
		if !strings.Contains(out.String(), "Pair again") {
			t.Errorf("the output doesn't tell the owner to choose Pair again:\n%s", out.String())
		}
		devs, err := devices.Load(path)
		if err != nil || len(devs) != 1 || !devs[0].Added.Equal(time.Date(2026, 10, 22, 10, 0, 3, 0, time.UTC)) {
			t.Errorf("devices.toml holds %+v (%v); want pager-01, added at the fake clock's 10:00:03", devs, err)
		}
	})

	t.Run("no hello in 120 s", func(t *testing.T) {
		peer, tty := openPeer(t)
		sent := device{start: make(chan struct{})}.play(t, peer) // only the boot log
		hostKey, _ := bridge(t)
		path, before, mtime := pairedTwo(t)
		o := options(t, tty, hostKey, path, &bytes.Buffer{})
		clk := newClock()
		o.After, o.Now = clk.After, clk.Now
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		errc := make(chan error, 1)
		go func() { errc <- Run(ctx, o) }()
		<-clk.waits
		clk.advance(119 * time.Second)
		select {
		case err := <-errc:
			t.Fatalf("Run gave up at 119 s: %v", err)
		case <-time.After(200 * time.Millisecond):
		}
		clk.advance(time.Second)
		err := <-errc
		t.Log(err)
		for _, s := range []string{"120 s", "Pair again", "devices.toml is unchanged"} {
			if err == nil || !strings.Contains(err.Error(), s) {
				t.Errorf("Run: %v; want an error that says %q", err, s)
			}
		}
		if nh := (*NoHelloError)(nil); !errors.As(err, &nh) {
			t.Errorf("Run: %T; want a *NoHelloError, which the setup page words for itself", err)
		}
		unchanged(t, path, before, mtime)
		select {
		case <-sent:
			t.Error("the tool sent a prov_set")
		default:
		}
	})
}

// TestReprovisionKeepsOneEntry checks re-provisioning a device whose key is
// already paired (parent 16.3): its entry is updated in place, never
// duplicated, under the same name or a new one, with the new pairing date;
// the other devices stay as they were. A name already paired with another
// key is refused before any prov_set is sent, since the device would
// otherwise save settings the bridge then won't accept.
func TestReprovisionKeepsOneEntry(t *testing.T) {
	repaired := time.Date(2026, 10, 24, 8, 15, 0, 0, time.UTC)
	pager02 := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBFlTBpNN2IZ/XfcnYg5nSzboXnFnSWwfn9NLno05IkE pager-02"
	for _, c := range []struct{ name, as string }{
		{"the same name", "pager-01"},
		{"a new name", "desk-01"},
	} {
		t.Run(c.name, func(t *testing.T) {
			peer, tty := openPeer(t)
			device{hello: "to-tool/valid/prov_hello.txt", result: "to-tool/valid/prov_result-ok.txt"}.play(t, peer)
			hostKey, _ := bridge(t)
			path, _, _ := pairedTwo(t) // pager-01 has the fixture device's key
			// 0644, as a hand edit might leave it: the add makes it 0600.
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
			o := options(t, tty, hostKey, path, &bytes.Buffer{})
			o.Name, o.Now = c.as, func() time.Time { return repaired }
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := Run(ctx, o); err != nil {
				t.Fatalf("Run: %v", err)
			}
			private(t, path)
			devs, err := devices.Load(path)
			if err != nil || len(devs) != 2 {
				t.Fatalf("devices.toml holds %d devices (%v); want 2, the re-paired one once", len(devs), err)
			}
			want := []struct {
				name, key string
				added     time.Time
			}{
				{c.as, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAnULfN18UYEHFPLj/PhfEMf7NvNdFoELzBJBtMsCVgj " + c.as, repaired},
				{"pager-02", pager02, time.Date(2026, 10, 23, 18, 5, 0, 0, time.UTC)},
			}
			for i, w := range want {
				if d := devs[i]; d.Name != w.name || d.Key != w.key || !d.Added.Equal(w.added) {
					t.Errorf("device %d is %s %q %v; want %s %q %v", i+1, d.Name, d.Key, d.Added, w.name, w.key, w.added)
				}
			}
		})
	}

	t.Run("a name paired with another key", func(t *testing.T) {
		peer, tty := openPeer(t)
		sent := device{hello: "to-tool/valid/prov_hello.txt", result: "to-tool/valid/prov_result-ok.txt"}.play(t, peer)
		hostKey, _ := bridge(t)
		path, before, mtime := pairedTwo(t)
		o := options(t, tty, hostKey, path, &bytes.Buffer{})
		o.Name = "pager-02" // the fixture device's key is pager-01's
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err := Run(ctx, o)
		t.Log(err)
		for _, s := range []string{"pager-02", "another key", "devices revoke pager-02", "devices.toml is unchanged"} {
			if err == nil || !strings.Contains(err.Error(), s) {
				t.Errorf("Run: %v; want an error that says %q", err, s)
			}
		}
		select {
		case <-sent:
			t.Error("the tool sent a prov_set")
		case <-time.After(200 * time.Millisecond):
		}
		unchanged(t, path, before, mtime)
	})
}

// A wait names one of the tool's waits: its length, and which of the waits
// of that length it is, from 1 (BOOT's 40 s is the first, the result's the
// second).
type wait struct {
	d   time.Duration
	nth int
}

// after is a fake clock's After: the waits in fire time out at once, and
// every other never does.
func after(fire ...wait) func(time.Duration) <-chan time.Time {
	var mu sync.Mutex
	seen := map[time.Duration]int{}
	return func(d time.Duration) <-chan time.Time {
		mu.Lock()
		seen[d]++
		w := wait{d, seen[d]}
		mu.Unlock()
		c := make(chan time.Time, 1)
		if slices.Contains(fire, w) {
			c <- time.Time{}
		}
		return c
	}
}

// pairedTwo is a devices.toml with the fixture's two devices, and how it
// was before provisioning: its bytes and modification time.
func pairedTwo(t *testing.T) (path string, before []byte, mtime time.Time) {
	t.Helper()
	data, err := os.ReadFile("../../testdata/config/devices.toml")
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), devices.File)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, data, fi.ModTime()
}

// private checks that the devices.toml at path, after an add, is mode 0600
// with no temporary file left beside it (parent 12.3): the add replaced it
// whole, rather than writing into it.
func private(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode() != 0o600 {
		t.Errorf("devices.toml after the add is mode %v; want 0600", fi.Mode())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".devices-*")); len(left) > 0 {
		t.Errorf("temporary files left beside devices.toml: %v", left)
	}
}

// unchanged checks that the file at path is as it was.
func unchanged(t *testing.T, path string, before []byte, mtime time.Time) {
	t.Helper()
	data, err := os.ReadFile(path)
	fi, err2 := os.Stat(path)
	if err != nil || err2 != nil || !bytes.Equal(data, before) || !fi.ModTime().Equal(mtime) {
		t.Errorf("devices.toml changed (%v, %v)", err, err2)
	}
}

// TestProvisionFailureLeavesNoChange checks that every way pairing can fail
// is an error that says what happened and that devices.toml is unchanged,
// and leaves it so (parent 2.5–2.7; ADR 0011): each err the device can
// answer, to the offer or to the prov_set; no BOOT within the tool's 40 s;
// no answer to the prov_set within 40 s; and an invalid key in the device's
// hello, when nothing is sent at all. Only after BOOT and the owner's yes is
// a prov_set sent. The CLI exits non-zero on any error (task 2.10).
func TestProvisionFailureLeavesNoChange(t *testing.T) {
	const offerOnly, both = 1, 2 // the lines the tool sends
	for _, c := range []struct {
		name, hello  string
		dev          device
		fire         []wait // the waits that time out
		lines        int    // what the tool sends: nothing, the offer, or the offer and the prov_set
		says         []string
		confirmAsked bool
	}{
		{"cancelled", "prov_hello.txt", device{offerResult: "prov_result-cancelled.txt"}, nil, offerOnly, []string{"cancelled", "PWR"}, false},
		{"no BOOT in the device's 30 s", "prov_hello.txt", device{offerResult: "prov_result-timeout.txt"}, nil, offerOnly, []string{"BOOT", "30 s"}, false},
		{"an older pager's invalid", "prov_hello.txt", device{offerResult: "prov_result-invalid.txt"}, nil, offerOnly, []string{"refused the offer", "older", "Flash the firmware shipped with this bridge first"}, false},
		{"busy", "prov_hello.txt", device{offerResult: "prov_result-busy.txt"}, nil, offerOnly, []string{"another pairing"}, false},
		{"no BOOT in 40 s", "prov_hello.txt", device{noBoot: true}, []wait{{ConfirmedTimeout, 1}}, offerOnly, []string{"BOOT", "40 s", "no Wi-Fi password was sent"}, false},
		{"the prov_set refused", "prov_hello.txt", device{result: "prov_result-invalid.txt"}, nil, both, []string{"refused the settings", "invalid"}, true},
		{"storage", "prov_hello.txt", device{result: "prov_result-storage.txt"}, nil, both, []string{"couldn't save"}, true},
		{"no answer in 40 s", "prov_hello.txt", device{}, []wait{{ResultTimeout, 2}}, both, []string{"no answer", "40 s"}, true},
		{"a truncated key", "../invalid/prov_hello-truncated-key.txt", device{}, nil, 0, []string{"pubkey", "32 bytes"}, false},
		{"a key with a comment", "../invalid/prov_hello-key-comment.txt", device{}, nil, 0, []string{"pubkey", "comment"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			peer, tty := openPeer(t)
			d := c.dev
			d.hello = "to-tool/valid/" + c.hello
			for _, f := range []*string{&d.offerResult, &d.result} {
				if *f != "" {
					*f = "to-tool/valid/" + *f
				}
			}
			sent := d.play(t, peer)
			hostKey, _ := bridge(t)
			path, before, mtime := pairedTwo(t)
			var out bytes.Buffer
			o := options(t, tty, hostKey, path, &out)
			o.After = after(c.fire...)
			asked := false
			o.Confirm = func(context.Context) (bool, error) { asked = true; return true, nil }
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			err := Run(ctx, o)
			t.Log(err)
			if err == nil {
				t.Fatalf("Run succeeded; want an error\n%s", out.String())
			}
			for _, s := range append(c.says, "devices.toml is unchanged") {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("the error doesn't say %q", s)
				}
			}
			unchanged(t, path, before, mtime)
			if asked != c.confirmAsked {
				t.Errorf("the owner was asked: %v; want %v", asked, c.confirmAsked)
			}
			var lines []string
			for done := false; !done; {
				select {
				case l := <-sent: // the peer may read it after Run returns
					lines = append(lines, typeOf(l))
				case <-time.After(200 * time.Millisecond):
					done = true
				}
			}
			want := []string{"prov_offer", "prov_set"}[:c.lines]
			if !slices.Equal(lines, want) {
				t.Errorf("the tool sent %q; want %q", lines, want)
			}
		})
	}
}

// TestNothingSecretBeforeBothConfirmations is requirement 2.9's byte search
// (ADR 0011): nothing the pager is sent holds a Wi-Fi password until both
// the pager's BOOT and the owner's yes have come. Before BOOT it has the
// prov_offer only; after BOOT, while the owner is asked, nothing more; only
// the yes sends the prov_set. A no, or the pager's 30 s running out while
// the owner is asked, ends the pairing with no password sent at all.
func TestNothingSecretBeforeBothConfirmations(t *testing.T) {
	const pass = "correct-horse-battery"
	noPassword := func(t *testing.T, got *received, when string) {
		t.Helper()
		if bytes.Contains(got.all(), []byte(pass)) {
			t.Errorf("%s: the pager has been sent the password:\n%s", when, got.all())
		}
	}
	nothingMore := func(t *testing.T, sent <-chan []byte, when string) {
		t.Helper()
		select {
		case l := <-sent:
			t.Errorf("%s: the tool sent the pager %s", when, l)
		case <-time.After(200 * time.Millisecond):
		}
	}

	t.Run("BOOT, then yes", func(t *testing.T) {
		peer, tty := openPeer(t)
		boot := make(chan struct{})
		sent, got := device{hello: "to-tool/valid/prov_hello.txt", boot: boot, result: "to-tool/valid/prov_result-ok.txt"}.playAll(t, peer)
		hostKey, path := bridge(t)
		var out syncBuffer
		o := options(t, tty, hostKey, path, nil)
		o.Out = &out
		asked, answer := make(chan struct{}), make(chan bool)
		o.Confirm = func(ctx context.Context) (bool, error) {
			close(asked)
			select {
			case ok := <-answer:
				return ok, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		errc := make(chan error, 1)
		go func() { errc <- Run(ctx, o) }()

		// The offer: the bridge's key and the Mac's name, nothing secret.
		select {
		case l := <-sent:
			if want := readLine(t, "to-device/valid/prov_offer.txt"); !bytes.Equal(l, want) {
				t.Fatalf("the first line sent is\n%s\nwant the fixture prov_offer.txt", l)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no prov_offer:\n%s", out.String())
		}
		nothingMore(t, sent, "before BOOT")
		noPassword(t, got, "before BOOT")
		select {
		case <-asked:
			t.Fatal("the owner was asked before the pager's BOOT")
		default:
		}

		// BOOT: the owner is asked, and the pager still has nothing secret.
		close(boot)
		select {
		case <-asked:
		case <-time.After(5 * time.Second):
			t.Fatalf("the owner wasn't asked after BOOT:\n%s", out.String())
		}
		nothingMore(t, sent, "after BOOT, before the owner's yes")
		noPassword(t, got, "after BOOT, before the owner's yes")

		// Yes: only now the prov_set, with the password.
		answer <- true
		if err := <-errc; err != nil {
			t.Fatalf("Run: %v\n%s", err, out.String())
		}
		if l := <-sent; typeOf(l) != "prov_set" || !bytes.Contains(l, []byte(pass)) {
			t.Errorf("after both confirmations the tool sent %s; want the prov_set with the password", l)
		}
		if devs, err := devices.Load(path); err != nil || len(devs) != 1 {
			t.Errorf("devices.toml holds %d devices (%v); want pager-01", len(devs), err)
		}
	})

	for _, c := range []struct {
		name      string
		afterBoot string // what the pager says while the owner is asked
		answer    bool
		says      []string
	}{
		{"BOOT, then no", "", false, []string{"weren't confirmed on this Mac", "no Wi-Fi password was sent", "PWR"}},
		{"BOOT, then the pager's 30 s run out", "to-tool/valid/prov_result-timeout.txt", true, []string{"stopped waiting", "no Wi-Fi password was sent"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			peer, tty := openPeer(t)
			sent, got := device{hello: "to-tool/valid/prov_hello.txt", afterBoot: c.afterBoot, result: "to-tool/valid/prov_result-ok.txt"}.playAll(t, peer)
			hostKey, _ := bridge(t)
			path, before, mtime := pairedTwo(t)
			o := options(t, tty, hostKey, path, &bytes.Buffer{})
			o.Confirm = func(ctx context.Context) (bool, error) {
				if c.afterBoot != "" {
					<-ctx.Done() // the owner hasn't answered when the pager stops waiting
					return false, ctx.Err()
				}
				return c.answer, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := Run(ctx, o)
			t.Log(err)
			for _, s := range append(c.says, "devices.toml is unchanged") {
				if err == nil || !strings.Contains(err.Error(), s) {
					t.Errorf("Run: %v; want an error that says %q", err, s)
				}
			}
			<-sent // the offer
			nothingMore(t, sent, "after the pairing ended")
			noPassword(t, got, "after the pairing ended")
			unchanged(t, path, before, mtime)
		})
	}

	t.Run("Run needs Confirm", func(t *testing.T) {
		hostKey, path := bridge(t)
		o := options(t, "/dev/null", hostKey, path, &bytes.Buffer{})
		o.Confirm = nil
		if err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "Confirm") {
			t.Errorf("Run with no Confirm: %v; want an error naming it", err)
		}
	})
}

// A syncBuffer is a bytes.Buffer that Run and the test share.
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
