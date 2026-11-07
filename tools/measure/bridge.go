package measure

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// bridgeSHA256 is the fixed bridge's hash (design D2-14).
const bridgeSHA256 = "845a870ee3ace13f54b7a1526cc3991c44cc92d9dbce68782fcc96c69fa3f586"

// BridgeBin is PAGER_BRIDGE_BIN, or ~/.run-phase/bin/pager-bridge: the one
// bridge the owner allowed through the Mac's firewall (design D2-14). It's
// never built here, and it must be the binary D2-14 names.
func BridgeBin(t testing.TB) string {
	t.Helper()
	bin := os.Getenv("PAGER_BRIDGE_BIN")
	if bin == "" {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".run-phase", "bin", "pager-bridge")
	}
	f, err := os.Open(bin)
	if err != nil {
		t.Fatalf("the fixed bridge (design D2-14): %v", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != bridgeSHA256 {
		t.Fatalf("%s has sha256 %s, not D2-14's %s: the firewall allows only that one", bin, sum, bridgeSHA256)
	}
	return bin
}

var (
	fakeOnce sync.Once
	fakeBin  string
	fakeErr  error
)

// FakeHerdrBin builds bridge/cmd/fakeherdr into OutDir, once per test
// binary. It's a fake herdr on a Unix socket, not what the board dials.
func FakeHerdrBin(t testing.TB) string {
	t.Helper()
	fakeOnce.Do(func() {
		fakeBin = filepath.Join(OutDir(t), "fakeherdr")
		cmd := exec.Command("go", "build", "-o", fakeBin, "./cmd/fakeherdr")
		cmd.Dir = filepath.Join(RepoDir(), "bridge")
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeErr = fmt.Errorf("go build fakeherdr: %v\n%s", err, out)
		}
	})
	if fakeErr != nil {
		t.Fatal(fakeErr)
	}
	return fakeBin
}

// syncBuf is a buffer two processes' output can share.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// mask hides the networks' names and passwords in text.
func mask(text string, nets []Network) string {
	var secrets []string
	for _, n := range nets {
		secrets = append(secrets, n.SSID, n.Pass)
	}
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	for _, s := range secrets {
		if len(s) >= 3 {
			text = strings.ReplaceAll(text, s, "[network]")
		}
	}
	return text
}

// Bridge is the fixed pager-bridge and fakeherdr, with their own HOME: a
// temp directory, so nothing reaches the owner's config or herdr.
type Bridge struct {
	Home, Dir, Socket string
	Log               *syncBuf // serve's output, every run in order
	HerdrLog          *syncBuf // fakeherdr's

	t             testing.TB
	bin, scenario string
	serve, herdr  *exec.Cmd
	nets          []Network
}

// checkPort is the board checks' bridge port.
const checkPort = 2278

// NewBridge sets up the bridge's directory (config.toml: listen on
// 0.0.0.0:2278, fakeherdr's socket; then init makes the host key) and
// starts fakeherdr with the scenario (fakeherdr's TOML). Serve starts with
// StartServe. Their logs go to OutDir/<test>.bridge.log when t ends.
func NewBridge(t testing.TB, scenario string, nets []Network) *Bridge {
	t.Helper()
	b := &Bridge{t: t, bin: BridgeBin(t), Log: &syncBuf{}, HerdrLog: &syncBuf{}, nets: nets}
	home, err := os.MkdirTemp("", "hpb") // short: macOS limits socket paths to 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	b.Home, b.Dir, b.Socket = home, filepath.Join(home, ".config", "herdr-pager"), filepath.Join(home, "herdr.sock")
	if err := os.MkdirAll(b.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Not 2277: the owner's bridge keeps that port, and can keep serving
	// through the checks (open-items O-42). provision tells the board this
	// port, and the firewall allows the binary, whatever port it listens on.
	config := fmt.Sprintf("listen = \"0.0.0.0:%d\"\nherdr_socket = %q\n", checkPort, b.Socket)
	if err := os.WriteFile(filepath.Join(b.Dir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := b.Command("init").CombinedOutput(); err != nil {
		t.Fatalf("pager-bridge init: %v\n%s", err, mask(string(out), nets))
	}
	b.scenario = filepath.Join(home, "scenario.toml")
	if err := os.WriteFile(b.scenario, []byte(scenario), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		b.StopServe()
		b.StopHerdr()
		logs := "== serve\n" + b.Log.String() + "\n== fakeherdr\n" + b.HerdrLog.String()
		os.WriteFile(filepath.Join(OutDir(t), strings.ReplaceAll(t.Name(), "/", "_")+".bridge.log"), []byte(mask(logs, nets)), 0o644)
		os.RemoveAll(home)
	})
	b.StartHerdr()
	return b
}

func (b *Bridge) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "HERDR_") {
			env = append(env, kv)
		}
	}
	return append(env, "HOME="+b.Home)
}

// Command is the fixed pager-bridge with args, run with the bridge's HOME.
func (b *Bridge) Command(args ...string) *exec.Cmd {
	cmd := exec.Command(b.bin, args...)
	cmd.Env = b.env()
	return cmd
}

func stop(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
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

// SetScenario replaces fakeherdr's scenario, for its next start. A file
// another field names (detection) can be written beside it with WriteFile.
func (b *Bridge) SetScenario(scenario string) {
	b.t.Helper()
	b.WriteFile("scenario.toml", scenario)
}

// WriteFile writes a file into the bridge's HOME, beside the scenario.
func (b *Bridge) WriteFile(name, data string) {
	b.t.Helper()
	if err := os.WriteFile(filepath.Join(b.Home, name), []byte(data), 0o600); err != nil {
		b.t.Fatal(err)
	}
}

// RestartHerdr stops fakeherdr, then starts it again (with the scenario as
// it is now: its times count from this start), and returns when its socket
// came back.
func (b *Bridge) RestartHerdr() time.Time {
	b.t.Helper()
	b.StopHerdr()
	b.StartHerdr()
	return time.Now()
}

// StartHerdr starts fakeherdr on the bridge's socket and waits for the socket.
func (b *Bridge) StartHerdr() {
	b.t.Helper()
	b.herdr = exec.Command(FakeHerdrBin(b.t), "--socket", b.Socket, "--scenario", b.scenario)
	b.herdr.Env = b.env()
	b.herdr.Stdout, b.herdr.Stderr = b.HerdrLog, b.HerdrLog
	if err := b.herdr.Start(); err != nil {
		b.t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(b.Socket); err == nil {
			return
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("fakeherdr made no socket within 5 s:\n%s", b.HerdrLog)
		}
	}
}

// StopHerdr stops fakeherdr.
func (b *Bridge) StopHerdr() {
	stop(b.herdr)
	b.herdr = nil
}

// StartServe starts pager-bridge serve and waits until it listens.
func (b *Bridge) StartServe() {
	b.t.Helper()
	before := strings.Count(b.Log.String(), `msg="serve: listening"`)
	b.serve = b.Command("serve")
	b.serve.Stdout, b.serve.Stderr = b.Log, b.Log
	if err := b.serve.Start(); err != nil {
		b.t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); strings.Count(b.Log.String(), `msg="serve: listening"`) == before; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			b.t.Fatalf("serve didn't listen within 10 s:\n%s", mask(b.Log.String(), b.nets))
		}
	}
}

// StopServe stops pager-bridge serve.
func (b *Bridge) StopServe() {
	stop(b.serve)
	b.serve = nil
}

// Connected reports whether status --json lists name as connected.
func (b *Bridge) Connected(name string) bool {
	out, err := b.Command("status", "--json").Output()
	if err != nil {
		return false
	}
	var st struct {
		Connected []string `json:"connected"`
	}
	return json.Unmarshal(out, &st) == nil && slices.Contains(st.Connected, name)
}

// WaitConnected waits until name is connected, and returns when status first said so.
func (b *Bridge) WaitConnected(name string, timeout time.Duration) time.Time {
	b.t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(250 * time.Millisecond) {
		if b.Connected(name) {
			return time.Now()
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("%s wasn't connected within %v; serve's log:\n%s", name, timeout, mask(b.Log.String(), b.nets))
		}
	}
}

// Provisioning is pager-bridge provision, running.
type Provisioning struct {
	Out  *syncBuf
	t    testing.TB
	done chan error
	nets []Network
}

// Provision starts pager-bridge provision on port for the networks, their
// passwords on its stdin, dialling its default host: this Mac's
// <LocalHostName>.local. It runs unattended, with --yes, which skips only
// the terminal's question: BOOT on the board still confirms (ADR 0011).
func (b *Bridge) Provision(port, name string) *Provisioning {
	b.t.Helper()
	args := []string{"provision", "--port", port, "--name", name, "--wifi-pass-stdin", "--yes"}
	var stdin strings.Builder
	for _, n := range b.nets {
		args = append(args, "--wifi-ssid", n.SSID)
		stdin.WriteString(n.Pass + "\n")
	}
	p := &Provisioning{Out: &syncBuf{}, t: b.t, done: make(chan error, 1), nets: b.nets}
	cmd := b.Command(args...)
	cmd.Stdin = strings.NewReader(stdin.String())
	cmd.Stdout, cmd.Stderr = p.Out, p.Out
	if err := cmd.Start(); err != nil {
		b.t.Fatal(err)
	}
	go func() { p.done <- cmd.Wait() }()
	b.t.Cleanup(func() { cmd.Process.Kill() })
	return p
}

// Output is provision's output so far, masked.
func (p *Provisioning) Output() string { return mask(p.Out.String(), p.nets) }

// Fingerprints waits for provision to print both keys' fingerprints, and returns the pager's and the bridge's.
func (p *Provisioning) Fingerprints(timeout time.Duration) (device, bridge string) {
	p.t.Helper()
	rx := regexp.MustCompile(`The pager's key:  (SHA256:\S+) .*\nThe bridge's key: (SHA256:\S+) `)
	for deadline := time.Now().Add(timeout); ; time.Sleep(100 * time.Millisecond) {
		if m := rx.FindStringSubmatch(p.Out.String()); m != nil {
			return m[1], m[2]
		}
		select {
		case err := <-p.done:
			p.t.Fatalf("provision ended (%v) before the fingerprints:\n%s", err, p.Output())
		default:
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("provision printed no fingerprints within %v:\n%s", timeout, p.Output())
		}
	}
}

// Wait waits for provision to exit, and returns how it ended.
func (p *Provisioning) Wait(timeout time.Duration) error {
	p.t.Helper()
	select {
	case err := <-p.done:
		return err
	case <-time.After(timeout):
		p.t.Fatalf("provision didn't end within %v:\n%s", timeout, p.Output())
		return nil
	}
}
