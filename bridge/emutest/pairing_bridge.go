package emutest

// The harness's bridge and fake herdr (design D2-8): pager-bridge serve on
// 127.0.0.1 with a temp HOME, fakeherdr behind it, and pager-bridge provision
// over an instance's virtual serial port. Nothing here reaches the owner's
// herdr or config: HOME is a temp directory, the herdr socket is the fake's,
// and no HERDR_* variable is passed on.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/pem"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// hostKeySeed makes the bridge's host key the same in every run, so its
// fingerprint on the pairing screen can be in a golden.
var hostKeySeed = sha256.Sum256([]byte("herdr-pager emutest bridge host key"))

var (
	binOnce sync.Once
	binDir  string
	binErr  error
)

// Binaries builds pager-bridge and fakeherdr once per test binary, and
// returns the directory holding them.
func Binaries(t testing.TB) string {
	t.Helper()
	binOnce.Do(func() {
		if binDir, binErr = os.MkdirTemp("", "hpbin"); binErr != nil {
			return
		}
		for _, pkg := range []string{"pager-bridge", "fakeherdr"} {
			cmd := exec.Command("go", "build", "-o", filepath.Join(binDir, pkg), "./cmd/"+pkg)
			cmd.Dir = ".." // the bridge module
			if out, err := cmd.CombinedOutput(); err != nil {
				binErr = fmt.Errorf("go build ./cmd/%s: %w\n%s", pkg, err, out)
				return
			}
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binDir
}

// syncBuffer is a process's output, read while it runs.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitFor polls out until re matches it, and returns the match.
func waitFor(t testing.TB, out *syncBuffer, re string, timeout time.Duration, what string) []string {
	t.Helper()
	rx := regexp.MustCompile(re)
	for deadline := time.Now().Add(timeout); ; time.Sleep(50 * time.Millisecond) {
		if m := rx.FindStringSubmatch(out.String()); m != nil {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: no %q within %v; output:\n%s", what, re, timeout, out)
		}
	}
}

// Bridge is pager-bridge serve on 127.0.0.1 and fakeherdr, in a temp HOME.
type Bridge struct {
	Home     string        // HOME for every pager-bridge command
	Dir      string        // the bridge's directory, HOME/.config/herdr-pager
	Port     int           // serve's port on 127.0.0.1
	HostKey  ssh.PublicKey // the bridge's host key (fixed: hostKeySeed)
	Socket   string        // fakeherdr's socket
	Log      *syncBuffer   // every serve's stderr, in order
	HerdrLog *syncBuffer   // fakeherdr's requests, one JSON line each

	t        testing.TB
	bin      string
	scenario string
	serve    *exec.Cmd
	herdr    *exec.Cmd
}

// StartBridge sets up a bridge directory the way init leaves it, with a fixed
// host key and config.toml pointing at 127.0.0.1 and the fake herdr; then
// starts fakeherdr with the scenario (fakeherdr's TOML; "" for its default
// agents) and pager-bridge serve. Both stop when t ends.
func StartBridge(t testing.TB, scenario string) *Bridge {
	t.Helper()
	return StartBridgeWith(t, scenario, "")
}

// StartBridgeWith is StartBridge with more of config.toml: config's lines
// (TOML, such as idle_timeout = "75s") follow listen and herdr_socket.
func StartBridgeWith(t testing.TB, scenario, config string) *Bridge {
	t.Helper()
	Require(t)
	b := &Bridge{t: t, bin: Binaries(t), Log: &syncBuffer{}, HerdrLog: &syncBuffer{}}
	home, err := os.MkdirTemp("", "hp") // short: macOS limits socket paths to 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	// Every line the running pager sent must be one the bridge takes: the ping is checked nowhere else
	// (open-items O-39). Registered before serve starts, so it runs after serve stops.
	t.Cleanup(func() {
		var refused []string
		for _, l := range strings.Split(b.Log.String(), "\n") {
			if strings.Contains(l, "session: invalid message") {
				refused = append(refused, l)
			}
		}
		if len(refused) > 0 {
			t.Errorf("the bridge refused %d lines from the pager:\n%s", len(refused), strings.Join(refused, "\n"))
		}
	})
	b.Home, b.Dir, b.Socket = home, filepath.Join(home, ".config", "herdr-pager"), filepath.Join(home, "herdr.sock")
	if err := os.MkdirAll(b.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	priv := ed25519.NewKeyFromSeed(hostKeySeed[:])
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	b.HostKey = signer.PublicKey()
	b.Port = freePort(t)
	config = fmt.Sprintf("listen = \"127.0.0.1:%d\"\nherdr_socket = %q\n%s\n", b.Port, b.Socket, config)
	for name, data := range map[string][]byte{"host_ed25519": pem.EncodeToMemory(block), "config.toml": []byte(config)} {
		if err := os.WriteFile(filepath.Join(b.Dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := b.Command("init").CombinedOutput(); err != nil { // keeps both files; adds host_ed25519.pub
		t.Fatalf("pager-bridge init: %v\n%s", err, out)
	}
	if scenario != "" {
		b.scenario = filepath.Join(home, "scenario.toml")
		if err := os.WriteFile(b.scenario, []byte(scenario), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b.StartHerdr()
	b.StartServe()
	return b
}

// freePort is a port on 127.0.0.1 that nothing listens on now.
func freePort(t testing.TB) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// env is this process's environment with HOME set to the bridge's, and no HERDR_* variable.
func (b *Bridge) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "HERDR_") {
			env = append(env, kv)
		}
	}
	return append(env, "HOME="+b.Home)
}

// Command is pager-bridge with args, run with the bridge's HOME.
func (b *Bridge) Command(args ...string) *exec.Cmd {
	cmd := exec.Command(filepath.Join(b.bin, "pager-bridge"), args...)
	cmd.Env = b.env()
	return cmd
}

func stop(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	}
}

// StartHerdr starts fakeherdr on the bridge's socket, and waits for the socket.
func (b *Bridge) StartHerdr() {
	b.t.Helper()
	args := []string{"--socket", b.Socket}
	if b.scenario != "" {
		args = append(args, "--scenario", b.scenario)
	}
	b.herdr = exec.Command(filepath.Join(b.bin, "fakeherdr"), args...)
	b.herdr.Env = b.env()
	b.herdr.Stdout = b.HerdrLog
	b.herdr.Stderr = b.HerdrLog
	if err := b.herdr.Start(); err != nil {
		b.t.Fatal(err)
	}
	herdr := b.herdr
	b.t.Cleanup(func() { stop(herdr) })
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(b.Socket); err == nil {
			return
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("fakeherdr made no socket within 5 s:\n%s", b.HerdrLog)
		}
	}
}

// StopHerdr stops fakeherdr (it removes its socket).
func (b *Bridge) StopHerdr() {
	stop(b.herdr)
	b.herdr = nil
}

// StartServe starts pager-bridge serve and waits until it listens.
func (b *Bridge) StartServe() {
	b.t.Helper()
	before := strings.Count(b.Log.String(), `msg="serve: listening"`)
	b.serve = b.Command("serve")
	b.serve.Stderr = b.Log
	b.serve.Stdout = b.Log
	if err := b.serve.Start(); err != nil {
		b.t.Fatal(err)
	}
	serve := b.serve
	b.t.Cleanup(func() { stop(serve) })
	for deadline := time.Now().Add(10 * time.Second); strings.Count(b.Log.String(), `msg="serve: listening"`) == before; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			b.t.Fatalf("serve didn't listen within 10 s:\n%s", b.Log)
		}
	}
}

// StopServe stops pager-bridge serve.
func (b *Bridge) StopServe() {
	stop(b.serve)
	b.serve = nil
}

// PauseServe stops serve's process (SIGSTOP) without closing anything: its
// connections stay open, and it answers nothing until ResumeServe. It's
// resumed when t ends, so it can stop.
func (b *Bridge) PauseServe() {
	b.t.Helper()
	if err := b.serve.Process.Signal(syscall.SIGSTOP); err != nil {
		b.t.Fatal(err)
	}
	serve := b.serve
	b.t.Cleanup(func() { serve.Process.Signal(syscall.SIGCONT) })
}

// ResumeServe lets a paused serve run again (SIGCONT).
func (b *Bridge) ResumeServe() {
	b.t.Helper()
	if err := b.serve.Process.Signal(syscall.SIGCONT); err != nil {
		b.t.Fatal(err)
	}
}

// ChangeHostKey restarts serve with a different host key, as if the bridge's
// key had been replaced since the pager was paired.
func (b *Bridge) ChangeHostKey() {
	b.t.Helper()
	b.StopServe()
	seed := sha256.Sum256([]byte("herdr-pager emutest another host key"))
	block, err := ssh.MarshalPrivateKey(ed25519.NewKeyFromSeed(seed[:]), "")
	if err != nil {
		b.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.Dir, "host_ed25519"), pem.EncodeToMemory(block), 0o600); err != nil {
		b.t.Fatal(err)
	}
	b.StartServe()
}

// Count is how many of the instance's serial lines so far match re.
func (in *Instance) Count(re string) int {
	in.t.Helper()
	data, err := os.ReadFile(filepath.Join(in.Dir, "serial.log"))
	if err != nil {
		in.t.Fatal(err)
	}
	return len(regexp.MustCompile("(?m)"+re).FindAllIndex(data, -1))
}

// WaitLog waits for serve's log to match re.
func (b *Bridge) WaitLog(re string, timeout time.Duration) []string {
	b.t.Helper()
	return waitFor(b.t, b.Log, re, timeout, "serve's log")
}

// Network is a Wi-Fi network to provision: its password goes on provision's stdin.
type Network struct{ SSID, Pass string }

// Provisioning is pager-bridge provision, running.
type Provisioning struct {
	Out  *syncBuffer // its stdout and stderr
	t    testing.TB
	done chan error
}

// Provision starts pager-bridge provision against port (an instance's virtual
// serial port), dialling host, with the networks' passwords on stdin. It
// runs unattended, with --yes: the test is the owner who checks the
// fingerprints, and BOOT on the pager is still the test's to press (ADR
// 0011).
func (b *Bridge) Provision(port, name, host string, nets ...Network) *Provisioning {
	b.t.Helper()
	args := []string{"provision", "--port", port, "--name", name, "--wifi-pass-stdin", "--host", host, "--yes"}
	var stdin strings.Builder
	for _, n := range nets {
		args = append(args, "--wifi-ssid", n.SSID)
		stdin.WriteString(n.Pass + "\n")
	}
	p := &Provisioning{Out: &syncBuffer{}, t: b.t, done: make(chan error, 1)}
	cmd := b.Command(args...)
	cmd.Stdin = strings.NewReader(stdin.String())
	cmd.Stdout = p.Out
	cmd.Stderr = p.Out
	if err := cmd.Start(); err != nil {
		b.t.Fatal(err)
	}
	go func() { p.done <- cmd.Wait() }()
	b.t.Cleanup(func() { cmd.Process.Kill() })
	return p
}

// Fingerprints waits for provision to print both keys' fingerprints, and returns the pager's and the bridge's.
func (p *Provisioning) Fingerprints(timeout time.Duration) (device, bridge string) {
	p.t.Helper()
	m := waitFor(p.t, p.Out, `The pager's key:  (SHA256:\S+) .*\nThe bridge's key: (SHA256:\S+) `, timeout, "provision")
	return m[1], m[2]
}

// Wait waits for provision to exit, and returns how it ended.
func (p *Provisioning) Wait(timeout time.Duration) error {
	p.t.Helper()
	select {
	case err := <-p.done:
		return err
	case <-time.After(timeout):
		p.t.Fatalf("provision didn't end within %v:\n%s", timeout, p.Out)
		return nil
	}
}

// The emulator's simulated Wi-Fi: the network the harness provisions (the
// provisioning fixtures' prov_set-emu has the same).
var EmuNetwork = Network{SSID: "lcd349-emu", Pass: "emulator-pass"}

// AirNetworks sets the Wi-Fi networks in range of the instance.
func (in *Instance) AirNetworks(nets ...Network) {
	in.t.Helper()
	var js []string
	for _, n := range nets {
		js = append(js, fmt.Sprintf(`{"ssid":%q,"password":%q}`, n.SSID, n.Pass))
	}
	in.SetState("wifi.networks=[" + strings.Join(js, ",") + "]")
}

// PressRestarting is Press for a press that restarts the pager as it ends
// (BOOT confirming a pairing): the restart can close the control socket
// before the press's answer, which the CLI reports as EOF.
func (in *Instance) PressRestarting(button string, ms int) {
	in.t.Helper()
	if out, err := lcd("press", in.ID, button, "--ms", strconv.Itoa(ms)); err != nil && !strings.Contains(string(out), "EOF") {
		in.t.Fatal(err)
	}
}

// MatchGoldenBlanked is MatchGolden with each mask painted over in the
// screenshot first, so a golden written from it never holds what the masks
// hide (the Mac's name, a key made at random, the time). It waits for the
// display to settle first, as the desk tests do: a text wait reads the UI
// tree, which can change a frame before the panel shows it. The list's hints
// caught it once: the counts had arrived, but the shot still held the empty
// list's HOLD: MENU from the frame before (open-items O-33).
func (in *Instance) MatchGoldenBlanked(name string, masks ...image.Rectangle) {
	in.t.Helper()
	in.WaitIdle(300, 5*time.Second)
	shot := in.Shot()
	img := image.NewRGBA(shot.Bounds())
	draw.Draw(img, img.Bounds(), shot, shot.Bounds().Min, draw.Src)
	for _, m := range masks {
		draw.Draw(img, m, &image.Uniform{color.RGBA{0x80, 0x80, 0x80, 0xFF}}, image.Point{}, draw.Src)
	}
	MatchGolden(in.t, img, Golden(name), masks...)
}
