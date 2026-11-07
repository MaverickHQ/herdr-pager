package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/provision"
)

// provFixtures is the provisioning contract (testdata/provisioning/README.md).
const provFixtures = "../../testdata/provisioning"

// pagerBridgeIn runs the CLI as a process like pagerBridge, with stdin.
func pagerBridgeIn(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return out.String(), errOut.String(), code
}

// pagerBridgeOpenStdin runs the CLI as a process whose stdin is a pipe
// holding stdin and never closed, as a terminal or a script's open pipe is.
// It returns what the command left unread. A command that waits on stdin is
// killed after 10 s, with exit -1.
func pagerBridgeOpenStdin(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int, unread string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := w.WriteString(stdin); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	cmd.Stdin = r // the pipe itself, as fd 0
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	w.Close()
	left, _ := io.ReadAll(r)
	return out.String(), errOut.String(), code, string(left)
}

// TestCommandsNeverReadStdin checks that every command is non-interactive
// (parent 11.6): init, status and devices list|revoke finish with stdin
// held open and leave it unread, as they do with it closed (every other
// test); provision reads exactly its password lines, one per network, and
// pairs without waiting for the end of stdin.
func TestCommandsNeverReadStdin(t *testing.T) {
	const typed = "y\n" // what someone might type at a prompt
	home := newHome(t)
	for _, args := range [][]string{
		{"init"},
		{"status"}, // lane e2e's; only whether it reads stdin is checked here
		{"devices", "list"},
		{"devices", "revoke", "pager-02"},
	} {
		if args[0] == "devices" {
			pairTwo(t, home)
		}
		_, stderr, code, unread := pagerBridgeOpenStdin(t, typed, args...)
		t.Logf("%s: exit %d", strings.Join(args, " "), code)
		if code == -1 || unread != typed {
			t.Errorf("%s: exit %d, stderr %q, left %q of stdin unread; want it finished, with stdin unread", strings.Join(args, " "), code, stderr, unread)
		}
		if args[0] != "status" && code != 0 {
			t.Errorf("%s: exit %d, stderr %q; want 0", strings.Join(args, " "), code, stderr)
		}
	}

	tty, _ := fakePager(t)
	_, stderr, code, unread := pagerBridgeOpenStdin(t, "correct-horse-battery\n"+typed,
		"provision", "--port", tty, "--name", "pager-01", "--host", "studio-mac.local", "--wifi-ssid", "HomeNet-2G", "--wifi-pass-stdin", "--yes")
	if code != 0 || unread != typed {
		t.Errorf("provision: exit %d, stderr %q, left %q of stdin unread; want exit 0 with only the password line read", code, stderr, unread)
	}
}

// provLine is a provisioning fixture's line, its newline included.
func provLine(t *testing.T, file string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(provFixtures, file))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// fakePager opens a pty for `provision --port` and plays a pager that
// accepts on its far side (seam S2-5; ADR 0011): it sends the fixture hello
// until a prov_offer comes, is confirmed with BOOT at once, and answers the
// prov_set ok. It returns the tty's name and the prov_set it gets.
func fakePager(t *testing.T) (tty string, got <-chan *provision.Set) {
	t.Helper()
	tty, _, got = fakePagerLines(t)
	return tty, got
}

// fakePagerLines is fakePager, also returning every provisioning line the
// pager is sent, in order.
func fakePagerLines(t *testing.T) (tty string, lines <-chan []byte, got <-chan *provision.Set) {
	t.Helper()
	peer, tt, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := provision.OpenPort(tt.Name()) // raw, as a serial port is
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		raw.Close()
		tt.Close()
		peer.Close()
	})
	sets := make(chan *provision.Set, 1)
	all := make(chan []byte, 8)
	offered, set := make(chan struct{}, 1), make(chan struct{}, 1)
	go func() {
		sc := bufio.NewScanner(peer)
		for sc.Scan() {
			if bytes.HasPrefix(sc.Bytes(), []byte(provision.Prefix)) {
				all <- append([]byte(nil), sc.Bytes()...)
			}
			switch m, _ := provision.ParseTool(sc.Bytes()); m := m.(type) {
			case *provision.HostOffer:
				offered <- struct{}{}
			case *provision.Set:
				sets <- m
				set <- struct{}{}
				return
			}
		}
	}()
	go func() { // the only writer, so the lines go in order
		hello := provLine(t, "to-tool/valid/prov_hello.txt")
		for {
			if _, err := peer.Write(hello); err != nil {
				return
			}
			select {
			case <-offered:
			case <-time.After(20 * time.Millisecond):
				continue
			}
			break
		}
		peer.Write(provLine(t, "to-tool/valid/prov_hello-confirming.txt")) // BOOT, at once
		peer.Write(provLine(t, "to-tool/valid/prov_hello-confirmed.txt"))
		select {
		case <-set:
			peer.Write(provLine(t, "to-tool/valid/prov_result-ok.txt"))
		case <-time.After(10 * time.Second):
		}
	}()
	return tt.Name(), all, sets
}

// A fakeTTY is the terminal provision asks on: what it's written, and the
// owner's answer to read.
type fakeTTY struct {
	io.Reader
	mu      sync.Mutex
	written bytes.Buffer
	asked   chan struct{} // closed on the first write: the question
	once    sync.Once
}

func (f *fakeTTY) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.once.Do(func() { close(f.asked) })
	return f.written.Write(p)
}

func (f *fakeTTY) Close() error { return nil }

func (f *fakeTTY) String() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.written.String()
}

// TestProvisionAsksOnTheTerminal checks provision's second confirmation
// (ADR 0011): once the pager has confirmed with BOOT, it asks on the
// terminal, not stdin (which carries the passwords), whether the
// fingerprints match the pager's screen, and sends the prov_set only on a
// yes. Until the answer, the pager has the prov_offer only. A no, or no
// terminal at all, sends no password and leaves devices.toml unchanged;
// --yes skips the question, and never opens the terminal.
func TestProvisionAsksOnTheTerminal(t *testing.T) {
	args := []string{"provision", "--name", "pager-01", "--host", "studio-mac.local", "--wifi-ssid", "HomeNet-2G", "--wifi-pass-stdin"}
	const pass = "correct-horse-battery"
	setup := func(t *testing.T) string {
		t.Helper()
		home := newHome(t)
		if _, stderr, code := pagerBridge(t, "init"); code != 0 {
			t.Fatalf("init: exit %d, %s", code, stderr)
		}
		return home
	}
	devicesIn := func(home string) int {
		devs, _ := devices.Load(filepath.Join(bridgeDir(home), "devices.toml"))
		return len(devs)
	}

	for _, c := range []struct {
		answer string
		paired bool
	}{{"y\n", true}, {"YES\n", true}, {"n\n", false}, {"\n", false}} {
		t.Run(fmt.Sprintf("%q", c.answer), func(t *testing.T) {
			home := setup(t)
			port, lines, _ := fakePagerLines(t)
			r, w := io.Pipe()
			tty := &fakeTTY{Reader: r, asked: make(chan struct{})}
			var stdout, stderr syncBuf
			cl := &cli{stdin: strings.NewReader(pass + "\n"), stdout: &stdout, stderr: &stderr,
				tty: func() (io.ReadWriteCloser, error) { return tty, nil }}
			code := make(chan int, 1)
			go func() { code <- run(cl, append(args, "--port", port)) }()

			select {
			case <-tty.asked:
			case <-time.After(5 * time.Second):
				t.Fatalf("no question on the terminal:\n%s%s", stdout.String(), stderr.String())
			}
			if q := tty.String(); !strings.Contains(q, "Do these match the pager's screen? [y/N]") {
				t.Errorf("the terminal was asked %q", q)
			}
			// Asked after BOOT: the pager has the offer, and nothing secret.
			if l := <-lines; !bytes.HasPrefix(l, []byte(`@P {"t":"prov_offer"`)) || bytes.Contains(l, []byte(pass)) {
				t.Errorf("before the answer the pager was sent %s; want the prov_offer only", l)
			}
			select {
			case l := <-lines:
				t.Errorf("before the answer the pager was sent %s", l)
			case <-time.After(200 * time.Millisecond):
			}

			w.Write([]byte(c.answer))
			got := <-code
			t.Logf("exit %d\n%s%s", got, stdout.String(), stderr.String())
			if c.paired {
				if got != 0 || devicesIn(home) != 1 {
					t.Errorf("exit %d with %d devices; want 0, pager-01 paired", got, devicesIn(home))
				}
				if l := <-lines; !bytes.HasPrefix(l, []byte(`@P {"t":"prov_set"`)) {
					t.Errorf("after the yes the pager was sent %s; want the prov_set", l)
				}
				return
			}
			if got != 1 || devicesIn(home) != 0 || !strings.Contains(stderr.String(), "no Wi-Fi password was sent") {
				t.Errorf("exit %d with %d devices, stderr %q; want 1, none, saying no password was sent", got, devicesIn(home), stderr.String())
			}
			select {
			case l := <-lines:
				t.Errorf("after the no the pager was sent %s", l)
			case <-time.After(200 * time.Millisecond):
			}
		})
	}

	t.Run("no terminal", func(t *testing.T) {
		home := setup(t)
		port, lines, _ := fakePagerLines(t)
		var stdout, stderr syncBuf
		cl := &cli{stdin: strings.NewReader(pass + "\n"), stdout: &stdout, stderr: &stderr,
			tty: func() (io.ReadWriteCloser, error) { return nil, errors.New("open /dev/tty: device not configured") }}
		got := run(cl, append(args, "--port", port))
		if got != 1 || !strings.Contains(stderr.String(), "no terminal") || !strings.Contains(stderr.String(), "pager-bridge setup") {
			t.Errorf("exit %d, stderr %q; want 1, saying there's no terminal and naming the setup page", got, stderr.String())
		}
		select {
		case l := <-lines:
			t.Errorf("with no terminal the pager was sent %s", l)
		case <-time.After(200 * time.Millisecond):
		}
		if devicesIn(home) != 0 {
			t.Error("devices.toml changed")
		}
	})

	t.Run("--yes", func(t *testing.T) {
		home := setup(t)
		port, _, _ := fakePagerLines(t)
		var stdout, stderr syncBuf
		cl := &cli{stdin: strings.NewReader(pass + "\n"), stdout: &stdout, stderr: &stderr,
			tty: func() (io.ReadWriteCloser, error) { t.Error("--yes opened the terminal"); return nil, errors.New("no") }}
		if got := run(cl, append(args, "--port", port, "--yes")); got != 0 || devicesIn(home) != 1 {
			t.Errorf("--yes: exit %d with %d devices, stderr %q; want 0, paired", got, devicesIn(home), stderr.String())
		}
		if !strings.Contains(stdout.String(), "--yes: not asking") {
			t.Errorf("--yes doesn't say it skipped the question:\n%s", stdout.String())
		}
	})
}

// A syncBuf is a bytes.Buffer that a command and the test share.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestProvisionFourNetworksFromStdin pairs over the CLI with four networks
// (parent 2.3, 16.1, 11.5): each --wifi-ssid's password is the stdin line in
// the same place, the prov_set carries them in that order, and no password
// is ever printed: not on stdout or stderr, not in devices.toml, not when one
// is refused. There's no flag that takes a password.
func TestProvisionFourNetworksFromStdin(t *testing.T) {
	m, err := provision.ParseTool(bytes.TrimSuffix(provLine(t, "to-device/valid/prov_set-4-networks.txt"), []byte("\n")))
	if err != nil {
		t.Fatal(err)
	}
	nets := m.(*provision.Set).WiFi // a passphrase, an iPhone hotspot, a 64-hex key, an open network
	args := []string{"provision", "--name", "pager-01", "--host", "studio-mac.local", "--wifi-pass-stdin", "--yes"}
	var stdin string
	for _, n := range nets {
		args = append(args, "--wifi-ssid", n.SSID)
		stdin += n.Pass + "\n"
	}
	// noPassword checks that no password shows in what the owner sees.
	noPassword := func(t *testing.T, where, s string) {
		t.Helper()
		for _, n := range nets {
			if n.Pass != "" && strings.Contains(s, n.Pass) {
				t.Errorf("%s holds the password of %q", where, n.SSID)
			}
		}
	}

	home := newHome(t)
	if _, stderr, code := pagerBridge(t, "init"); code != 0 {
		t.Fatalf("init: exit %d, %s", code, stderr)
	}
	tty, sets := fakePager(t)
	stdout, stderr, code := pagerBridgeIn(t, stdin, append(args, "--port", tty)...)
	t.Logf("stdout:\n%s\nstderr:\n%s", stdout, stderr)
	if code != 0 {
		t.Fatalf("provision: exit %d", code)
	}
	noPassword(t, "stdout", stdout)
	noPassword(t, "stderr", stderr)

	select {
	case set := <-sets:
		if !slices.Equal(set.WiFi, nets) {
			t.Errorf("the prov_set's networks are %d, not the four in order", len(set.WiFi))
		}
		pub, _ := os.ReadFile(filepath.Join(bridgeDir(home), "host_ed25519.pub"))
		if set.HostKey+"\n" != string(pub) {
			t.Errorf("the prov_set carries the host key %s; want the offer's, %s", set.HostKey, pub)
		}
	default:
		t.Fatal("the pager got no prov_set")
	}
	data, err := os.ReadFile(filepath.Join(bridgeDir(home), "devices.toml"))
	if err != nil {
		t.Fatal(err)
	}
	noPassword(t, "devices.toml", string(data))
	devs, err := devices.Load(filepath.Join(bridgeDir(home), "devices.toml"))
	if err != nil || len(devs) != 1 || devs[0].Name != "pager-01" || devs[0].Fingerprint() != "SHA256://a7FlX9Y4t0WSS+W8ANyY4w+5+axSEVbGzNXp6txjY" {
		t.Errorf("devices.toml holds %v (%v); want pager-01 with the fixture device's key", devs, err)
	}
	if !strings.Contains(stdout, "SHA256://a7FlX9Y4t0WSS+W8ANyY4w+5+axSEVbGzNXp6txjY") {
		t.Error("stdout doesn't give the pager's full fingerprint")
	}
	if pub, err := os.ReadFile(filepath.Join(bridgeDir(home), "host_ed25519.pub")); err == nil {
		key, _, _, _, _ := ssh.ParseAuthorizedKey(pub)
		if key == nil || !strings.Contains(stdout, ssh.FingerprintSHA256(key)) {
			t.Error("stdout doesn't give the bridge's full fingerprint")
		}
	}

	// A flag can't carry a password, and the refusal doesn't echo it.
	stdout, stderr, code = pagerBridgeIn(t, "", "provision", "--port", tty, "--name", "pager-01", "--wifi-ssid", "HomeNet-2G", "--wifi-pass", "correct-horse-battery")
	t.Log(strings.SplitN(stderr, "\n", 2)[0])
	if code != 2 {
		t.Errorf("--wifi-pass: exit %d; want 2, an unknown flag", code)
	}
	noPassword(t, "stdout after --wifi-pass", stdout)
	noPassword(t, "stderr after --wifi-pass", stderr)

	// A password the pager would refuse is refused by its place, not quoted.
	bad := strings.Replace(stdin, "hotspot-pass-8", "hotspot", 1) // 7 characters
	stdout, stderr, code = pagerBridgeIn(t, bad, append(args, "--port", tty)...)
	t.Log(stderr)
	if code != 1 || !strings.Contains(stderr, "wifi[1].pass") || strings.Contains(stdout+stderr, "hotspot\n") {
		t.Errorf("a 7-character password: exit %d, stderr %q; want exit 1 naming wifi[1].pass, never the password", code, stderr)
	}
	noPassword(t, "stderr after a refused password", stderr)

	// Fewer stdin lines than networks.
	_, stderr, code = pagerBridgeIn(t, "correct-horse-battery\n", append(args, "--port", tty)...)
	t.Log(stderr)
	if code != 1 || !strings.Contains(stderr, "stdin") {
		t.Errorf("one password for four networks: exit %d, stderr %q; want exit 1 about stdin", code, stderr)
	}
	noPassword(t, "stderr after too few passwords", stderr)
}
