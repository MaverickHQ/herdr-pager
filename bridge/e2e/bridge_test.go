package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/bridge"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devsim"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdrtest"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/keymap"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/risk"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/sshserver"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/watch"
)

// A testBridge is pager-bridge serve, built by serve's own constructor
// (internal/bridge) on a fake clock: an SSH server on 127.0.0.1:0, the
// watcher polling a fake herdr, and a session per device.
type testBridge struct {
	t       *testing.T
	herdr   *herdrtest.Server
	clock   *fakeClock
	addr    string
	hostKey ssh.PublicKey
	log     *syncBuffer
	logger  *slog.Logger
	dir     string // the bridge's config directory, and the fake herdr's socket

	mu      sync.Mutex
	devices []devices.Device
	file    *sshserver.DevicesFile // devices.toml, once devicesFromFile is called
}

// startBridge starts a fake herdr answering with agents, then a bridge
// watching it. Both stop when the test ends.
func startBridge(t *testing.T, agents []herdr.AgentInfo) *testBridge {
	t.Helper()
	dir, err := os.MkdirTemp("", "hp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	fake, err := herdrtest.Start(filepath.Join(dir, "herdr.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fake.Close() })
	fake.SetAgents(agents)

	b := &testBridge{t: t, herdr: fake, clock: newFakeClock(), log: captureLog(t), dir: dir}
	log := slog.New(slog.NewTextHandler(b.log, nil))
	b.logger = log
	keymaps, err := keymap.Load("")
	if err != nil {
		t.Fatal(err)
	}
	rules, err := risk.Load("")
	if err != nil {
		t.Fatal(err)
	}
	// serve's wiring (internal/bridge), on the fake clock and herdr.
	br := bridge.New(bridge.Config{
		HerdrSocket: fake.Socket, Keymaps: keymaps, Rules: rules,
		Host: "test-host", Clock: b.clock, Log: log,
	})
	w := br.Watch
	hostKey := newKey(t)
	b.hostKey = hostKey.PublicKey()
	srv := br.Server(hostKey, b.pairedDevices, nil)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b.addr = l.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); w.Run(ctx) }()
	go func() { defer wg.Done(); srv.Serve(ctx, l) }()
	t.Cleanup(func() { cancel(); wg.Wait() })
	return b
}

func (b *testBridge) pairedDevices() []devices.Device {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file != nil {
		return b.file.Devices()
	}
	return append([]devices.Device(nil), b.devices...)
}

// devicesFromFile makes the bridge read its devices from devices.toml in its
// directory, as serve does, and returns the file's path. The file starts
// missing: no devices.
func (b *testBridge) devicesFromFile() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.file = &sshserver.DevicesFile{Path: filepath.Join(b.dir, devices.File), Log: b.logger}
	return b.file.Path
}

// pair adds a device named name to the bridge's devices, as provisioning
// does, and returns its key.
func (b *testBridge) pair(name string) ssh.Signer {
	key := newKey(b.t)
	b.pairKey(name, key)
	return key
}

// pairKey lists key as device name's, of whatever type: devices.Load allows
// only ed25519, so the server's own check is tested by listing others here.
func (b *testBridge) pairKey(name string, key ssh.Signer) {
	rememberKey(key.PublicKey())
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key.PublicKey())))
	b.mu.Lock()
	defer b.mu.Unlock()
	b.devices = append(b.devices, devices.Device{Name: name, Key: line, PublicKey: key.PublicKey(), Added: start.UTC()})
}

// tick moves the clock on by one poll interval and returns once the watcher
// has polled the fake herdr again.
func (b *testBridge) tick() {
	b.t.Helper()
	polls := func() (n int) {
		for _, m := range b.herdr.Methods() {
			if m == herdr.MethodList {
				n++
			}
		}
		return n
	}
	before := polls()
	b.clock.Advance(watch.Interval)
	for deadline := time.Now().Add(5 * time.Second); polls() == before; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			b.t.Fatal("the watcher didn't poll after a tick")
		}
	}
}

// waitLog waits up to 5 s for the bridge's log to hold s.
func (b *testBridge) waitLog(s string) bool {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if strings.Contains(b.log.String(), s) {
			return true
		}
	}
	return false
}

// list replaces the bridge's devices with devs.
func (b *testBridge) list(devs ...devices.Device) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.devices = devs
}

// dial connects a simulated pager with key, pinned to the bridge's host key.
func (b *testBridge) dial(key ssh.Signer) *devsim.Device {
	b.t.Helper()
	d, err := devsim.Dial(b.addr, key, b.hostKey)
	if err != nil {
		b.t.Fatal(err)
	}
	b.t.Cleanup(func() { d.Close() })
	return d
}

// connect pairs a device named name, dials, and says hello; it returns once
// the bridge's hello has come.
func (b *testBridge) connect(name string) *devsim.Device {
	b.t.Helper()
	return b.connectKey(name, b.pair(name))
}

// connectKey dials with key and says hello as name; it returns once the
// bridge's hello has come.
func (b *testBridge) connectKey(name string, key ssh.Signer) *devsim.Device {
	b.t.Helper()
	d := b.dial(key)
	if err := d.Send(`{"t":"hello","v":1,"fw":"0.1.0","name":"` + name + `"}`); err != nil {
		b.t.Fatal(err)
	}
	recv[*protocol.BridgeHello](b.t, d)
	return d
}

func newKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	rememberKey(s.PublicKey())
	return s
}

// fixture returns one line of the protocol fixtures (testdata/protocol/),
// without its '\n'.
func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "protocol", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(data), "\n")
}

// agentsFixture returns the agents of a saved agent.list reply under
// testdata/herdr/.
func agentsFixture(t *testing.T, name string) []herdr.AgentInfo {
	t.Helper()
	agents, err := herdrtest.Agents(filepath.Join("..", "testdata", "herdr", name))
	if err != nil {
		t.Fatal(err)
	}
	return agents
}

// recv returns the device's next line from the bridge, parsed as the
// contract (testdata/protocol/) says, and as a T.
func recv[T protocol.Message](t *testing.T, d *devsim.Device) T {
	t.Helper()
	var zero T
	line, err := d.Recv(5 * time.Second)
	if err != nil {
		t.Fatalf("waiting for %T: %v", zero, err)
	}
	m, err := protocol.ParseBridge([]byte(line))
	if err != nil {
		t.Fatalf("the bridge sent %s, which breaks the protocol: %v", line, err)
	}
	got, ok := m.(T)
	if !ok {
		t.Fatalf("the bridge sent %s; want a %T", line, zero)
	}
	return got
}

// printable says whether s is 1 to max bytes of printable ASCII.
func printable(s string, max int) bool {
	if len(s) < 1 || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func str(s string) *string { return &s }

func num(n uint64) *uint64 { return &n }

// A syncBuffer is a log that many goroutines write.
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
