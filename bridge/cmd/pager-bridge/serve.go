package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/bridge"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/clock"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/config"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/keymap"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/risk"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/sshserver"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/status"
)

var serveCmd = command{"serve", []string{
	"serve", "Serve paired pagers over SSH, logging to stderr, until SIGINT or SIGTERM.",
}, runServe}

// The files serve reads in the bridge's directory, besides config.toml and
// devices.toml: the host key init made, and the user's keymaps and danger
// rules, if any (parent design §Data models).
const (
	hostKeyFile = "host_ed25519"
	keymapsFile = "keymaps.toml"
	riskFile    = "risk.toml"
)

// shutdownWait bounds how long serve waits, once told to stop, for its
// sessions and its poll of herdr to end, so that it exits within 2 s (parent
// 11.1): a poll that herdr doesn't answer would take the herdr client's 2 s.
const shutdownWait = time.Second

// A bridgeSetup is what serve and stdio load before they run.
type bridgeSetup struct {
	dir     string // the bridge's directory
	cfg     config.Config
	rules   risk.Rules
	keymaps keymap.Set
	log     *slog.Logger // to stderr, at config.toml's log_level
}

// loadBridge parses command cmd's flags (it takes none) and loads, in order,
// config.toml, the danger rules (a user rule whose own cases fail stops it,
// naming its line: parent 18.2) and the keymaps, each from the embedded
// defaults and the user's overrides file. On a failure it says why on stderr
// and returns nil and the exit code.
func loadBridge(c *cli, cmd string, args []string) (*bridgeSetup, int) {
	flags := flag.NewFlagSet(cmd, flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	if err := flags.Parse(args); err != nil {
		return nil, 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(c.stderr, "pager-bridge %s: takes no arguments\n", cmd)
		return nil, 2
	}
	fail := func(err error) (*bridgeSetup, int) {
		fmt.Fprintf(c.stderr, "pager-bridge %s: %v\n", cmd, err)
		return nil, 1
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fail(err)
	}
	b := &bridgeSetup{dir: config.Dir(home)}
	b.cfg, err = config.Load(filepath.Join(b.dir, config.File), config.Env{Home: home, Getenv: os.Getenv})
	if errors.Is(err, fs.ErrNotExist) {
		err = fmt.Errorf("%w; run pager-bridge init first", err)
	}
	if err != nil {
		return fail(err)
	}
	if b.rules, err = risk.Load(filepath.Join(b.dir, riskFile)); err != nil {
		return fail(err)
	}
	if b.keymaps, err = keymap.Load(filepath.Join(b.dir, keymapsFile)); err != nil {
		return fail(err)
	}
	level := slog.LevelInfo
	if b.cfg.LogLevel == "debug" {
		level = slog.LevelDebug
	}
	b.log = slog.New(slog.NewTextHandler(c.stderr, &slog.HandlerOptions{Level: level}))
	return b, 0
}

// start builds the bridge from what was loaded, on the real clock.
func (b *bridgeSetup) start() *bridge.Bridge {
	return bridge.New(bridge.Config{
		HerdrSocket: b.cfg.HerdrSocket, Keymaps: b.keymaps, Rules: b.rules, IdleTimeout: b.cfg.IdleTimeout,
		Host: helloHost(), Clock: clock.Real{}, Log: b.log,
	})
}

// stopWithin waits for wg, at most shutdownWait, once a command has been told
// to stop.
func stopWithin(wg *sync.WaitGroup, log *slog.Logger, cmd string) {
	stopped := make(chan struct{})
	go func() { wg.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(shutdownWait):
		log.Warn(cmd+": exiting before its poll of herdr ended", "after", shutdownWait)
	}
}

// runServe serves paired pagers over SSH on the configured address, polls
// herdr every second, keeps status.json current, and exits 0 within 2 s of
// SIGINT or SIGTERM (parent 11.1, design D2-12; lane e2e).
//
// It loads everything before it listens: config.toml, the danger rules and
// the keymaps (loadBridge), the host key and devices.toml. It needs no sshd,
// Remote Login or root (parent 1.4): its SSH server is its own, on the user's
// port.
func runServe(c *cli, args []string) int {
	b, failed := loadBridge(c, "serve", args)
	if b == nil {
		return failed
	}
	dir, cfg, log := b.dir, b.cfg, b.log
	hostKey, err := readHostKey(filepath.Join(dir, hostKeyFile))
	if err != nil {
		return serveFailed(c, err)
	}
	devicesPath := filepath.Join(dir, devices.File)
	if _, err := devices.Load(devicesPath); err != nil {
		return serveFailed(c, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	l, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return serveFailed(c, err)
	}

	br := b.start()
	w := br.Watch
	paired := &sshserver.DevicesFile{Path: devicesPath, Log: log}
	open := &openSessions{changed: make(chan struct{}, 1)}
	srv := br.Server(hostKey, paired.Devices, open.add)
	log.Info("serve: listening", "addr", l.Addr().String(), "host_key", ssh.FingerprintSHA256(hostKey.PublicKey()), "herdr_socket", cfg.HerdrSocket)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	served := make(chan error, 1)
	snaps, unsubscribe := w.Subscribe()
	defer unsubscribe()
	st := status.Status{Listening: l.Addr().String(), Herdr: "offline", Socket: cfg.HerdrSocket}
	wg.Add(3)
	go func() { defer wg.Done(); w.Run(ctx) }()
	go func() { defer wg.Done(); served <- srv.Serve(ctx, l) }()
	go func() { defer wg.Done(); keepStatus(ctx, dir, st, snaps, open, paired, log) }()
	code := 0
	select {
	case <-ctx.Done():
		log.Info("serve: stopping")
	case err := <-served:
		if ctx.Err() == nil {
			log.Error("serve: the listener failed", "err", err)
			code = 1
		}
	}
	cancel()
	stopWithin(&wg, log, "serve")
	return code
}

// serveFailed reports why serve can't start, and its exit code.
func serveFailed(c *cli, err error) int {
	fmt.Fprintf(c.stderr, "pager-bridge serve: %v\n", err)
	return 1
}

// keepStatus keeps status.json in dir current until ctx ends (design D2-12):
// st with herdr's state from each snapshot, the paired devices (and why
// devices.toml won't load, while it won't), and those with a session open.
// It writes it every second, and at once when herdr's state or the connected
// devices change; when ctx ends it removes it, so `status` says at once that
// no bridge runs.
func keepStatus(ctx context.Context, dir string, st status.Status, snaps <-chan protocol.Snap, open *openSessions, paired *sshserver.DevicesFile, log *slog.Logger) {
	defer os.Remove(filepath.Join(dir, status.File))
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case snap := <-snaps:
			st.Herdr = snap.Herdr
		case <-open.changed:
		case <-tick.C:
		}
		st.Devices, st.Connected = pairedNames(paired.Devices()), open.names()
		st.DevicesError = paired.Broken()
		err := status.Write(dir, st, time.Now())
		if err != nil && !failing {
			log.Error("serve: can't write status.json", "err", err)
		}
		failing = err != nil
	}
}

// openSessions are the devices with a session open, for status.json.
type openSessions struct {
	mu      sync.Mutex
	count   map[string]int // open sessions by device name
	changed chan struct{}  // holds a value once count has changed
}

// add counts n more of device name's sessions open.
func (o *openSessions) add(name string, n int) {
	o.mu.Lock()
	if o.count == nil {
		o.count = map[string]int{}
	}
	if o.count[name] += n; o.count[name] <= 0 {
		delete(o.count, name)
	}
	o.mu.Unlock()
	select {
	case o.changed <- struct{}{}:
	default:
	}
}

// names returns the devices with a session open, sorted.
func (o *openSessions) names() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Sorted(maps.Keys(o.count))
}

// readHostKey reads the bridge's ed25519 host key, which init made at path.
func readHostKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w; run pager-bridge init first", err)
	}
	if err != nil {
		return nil, err
	}
	key, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if t := key.PublicKey().Type(); t != ssh.KeyAlgoED25519 {
		return nil, fmt.Errorf("%s: an %s key; the host key is ssh-ed25519", path, t)
	}
	return key, nil
}

// helloHost is the Mac's name for the bridge's hello: its host name without
// ".local", in printable ASCII, at most 64 bytes.
func helloHost() string {
	name, _ := os.Hostname()
	name = strings.TrimSuffix(name, ".local")
	var host []byte
	for i := 0; i < len(name) && len(host) < 64; i++ {
		if name[i] > ' ' && name[i] <= '~' {
			host = append(host, name[i])
		}
	}
	if len(host) == 0 {
		return "mac"
	}
	return string(host)
}
