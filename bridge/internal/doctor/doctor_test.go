package doctor

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/config"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/status"
)

// The bridge's own path in these tests: where Homebrew installs it.
const brewExe = "/opt/homebrew/Cellar/herdr-pager/1.0.0/bin/pager-bridge"

// A world is one Mac as the doctor sees it (S5P-6): a temporary home holding
// the bridge's directory, herdr's socket in a short temporary folder, the
// port config.toml names, and what each command prints. Nothing reaches the
// owner's home, herdr, firewall or Homebrew.
type world struct {
	t       *testing.T
	home    string
	dir     string // the bridge's directory
	socket  string // herdr's socket, as config.toml names it
	listen  string // config.toml's listen: 127.0.0.1 and a free port
	exe     string // the bridge's own path
	now     time.Time
	replies map[string]reply // a command line → what it prints
	calls   []string         // the command lines it ran
	dials   []string         // "network address", each it dialled
}

type reply struct {
	out string
	err error
}

// newWorld is a Mac with the bridge's keys and config.toml, herdr's socket
// not there, the port free, the firewall off, brew services running
// herdr-pager at login, nothing paired and no bridge running.
func newWorld(t *testing.T) *world {
	t.Helper()
	home := t.TempDir()
	dir := config.Dir(home)
	if _, err := config.Init(dir); err != nil {
		t.Fatal(err)
	}
	sockDir, err := os.MkdirTemp("", "hp") // short: a socket's path is at most 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	w := &world{
		t: t, home: home, dir: dir,
		socket:  filepath.Join(sockDir, "herdr.sock"),
		listen:  freeAddr(t),
		exe:     brewExe,
		now:     time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC),
		replies: map[string]reply{},
	}
	w.writeConfig()
	w.firewall("Firewall is disabled. (State = 0)\n", "")
	w.brew(brewList("started"))
	return w
}

// healthyWorld is newWorld with herdr answering, pager-01 paired, and a
// bridge running with it connected.
func healthyWorld(t *testing.T) *world {
	w := newWorld(t)
	w.herdrUp()
	w.pair("pager-01")
	w.bridgeRunning("pager-01")
	return w
}

// freeAddr is 127.0.0.1 and a port that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func (w *world) writeConfig() {
	w.t.Helper()
	cfg := fmt.Sprintf("listen = %q\nherdr_socket = %q\n", w.listen, w.socket)
	if err := os.WriteFile(filepath.Join(w.dir, config.File), []byte(cfg), 0o600); err != nil {
		w.t.Fatal(err)
	}
}

// herdrUp listens on herdr's socket, as herdr does.
func (w *world) herdrUp() {
	w.t.Helper()
	ln, err := net.Listen("unix", w.socket)
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
}

// staleSocket leaves herdr's socket file with nothing listening on it, as a
// herdr that crashed does.
func (w *world) staleSocket() {
	w.t.Helper()
	ln, err := net.Listen("unix", w.socket)
	if err != nil {
		w.t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
}

// holdPort listens on config.toml's address, as another process (or the
// bridge) does.
func (w *world) holdPort() {
	w.t.Helper()
	ln, err := net.Listen("tcp", w.listen)
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { ln.Close() })
}

// pair adds a device named name to devices.toml, with a new ed25519 key.
func (w *world) pair(name string) devices.Device {
	w.t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		w.t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		w.t.Fatal(err)
	}
	d := devices.Device{Name: name, Key: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), PublicKey: key, Added: w.now}
	if err := devices.Add(filepath.Join(w.dir, devices.File), d); err != nil {
		w.t.Fatal(err)
	}
	return d
}

// bridgeRunning writes status.json as a running bridge does, listening on
// config.toml's address, with connected's devices connected, and holds the
// port as it would.
func (w *world) bridgeRunning(connected ...string) {
	w.t.Helper()
	devs, err := devices.Load(filepath.Join(w.dir, devices.File))
	if err != nil {
		w.t.Fatal(err)
	}
	var names []string
	for _, d := range devs {
		names = append(names, d.Name)
	}
	s := status.Status{Listening: w.listen, Herdr: "ok", Socket: w.socket, Devices: names, Connected: connected}
	if err := status.Write(w.dir, s, w.now.Add(-time.Second)); err != nil {
		w.t.Fatal(err)
	}
	w.holdPort()
}

const fwTool = "/usr/libexec/ApplicationFirewall/socketfilterfw"

// firewall sets what socketfilterfw prints for its global state, its block
// all state and, if app isn't empty, its verdict on the bridge's own path,
// w.exe (--getappblocked), in macOS 26.6.2's words (the owner's checkpoint
// 7.2, 2026-11-07: --listapps never lists an unsigned binary allowed with
// --add and --unblockapp, so the doctor doesn't ask it).
func (w *world) firewall(state, app string) {
	w.replies[fwTool+" --getglobalstate"] = reply{out: state}
	w.replies[fwTool+" --getblockall"] = reply{out: "Firewall has block all state set to disabled. \n"}
	if app != "" {
		w.replies[fwTool+" --getappblocked "+w.exe] = reply{out: app}
	}
}

// permitted and blocked are --getappblocked's verdicts on path.
func permitted(path string) string { return "Incoming connection to " + path + " is permitted. \n" }
func blocked(path string) string   { return "Incoming connection to " + path + " is blocked. \n" }

// brew sets what `brew services list --json` prints, by brew on PATH.
func (w *world) brew(out string) { w.replies["brew services list --json"] = reply{out: out} }

// noBrew makes brew a command that isn't there, on PATH or in /opt/homebrew.
func (w *world) noBrew() {
	w.replies["brew services list --json"] = reply{err: &exec.Error{Name: "brew", Err: exec.ErrNotFound}}
	w.replies["/opt/homebrew/bin/brew services list --json"] = reply{err: &exec.Error{Name: "/opt/homebrew/bin/brew", Err: fs.ErrNotExist}}
}

// brewList is `brew services list --json` with herdr-pager in status, and
// another formula's service beside it.
func brewList(status string) string {
	return `[{"name":"postgresql@16","status":"started","user":"pat","file":"/opt/homebrew/opt/postgresql@16/homebrew.mxcl.postgresql@16.plist","exit_code":0},` +
		`{"name":"herdr-pager","status":"` + status + `","user":"pat","file":"/opt/homebrew/opt/herdr-pager/homebrew.mxcl.herdr-pager.plist","exit_code":0}]` + "\n"
}

// launchdAgent installs the launchd agent that runs this bridge, w.exe, at
// login (contrib/launchd, docs/bridge.md §Run it at login).
func (w *world) launchdAgent() { w.launchdAgentFor(w.exe) }

// launchdAgentFor installs the launchd agent running program serve at login,
// as contrib/launchd's template, filled in, has it.
func (w *world) launchdAgentFor(program string) {
	w.t.Helper()
	w.launchdPlist(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>dev.herdr-pager.bridge</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + program + `</string>
		<string>serve</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>HOME</key>
		<string>` + w.home + `</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`)
}

// launchdPlist writes the launchd agent's plist as data.
func (w *world) launchdPlist(data string) {
	w.t.Helper()
	dir := filepath.Join(w.home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dev.herdr-pager.bridge.plist"), []byte(data), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

// The command lines the doctor may run, each fixed (design D5P-3 §Never):
// the firewall's state and its block all state, and Homebrew's list of
// services, by brew on PATH or in /opt/homebrew; and, with its one argument
// that isn't a literal, the firewall's verdict on the bridge's own path.
var allowed = []string{
	fwTool + " --getglobalstate", fwTool + " --getblockall",
	"brew services list --json", "/opt/homebrew/bin/brew services list --json",
}

// fake is w's runner: what w.replies says each command prints. It fails
// the test on a command not in allowed.
func (w *world) fake(argv ...string) ([]byte, error) {
	line := strings.Join(argv, " ")
	w.calls = append(w.calls, line)
	if !slices.Contains(allowed, line) && line != fwTool+" --getappblocked "+w.exe {
		w.t.Errorf("the doctor ran %q, not one of its fixed commands", line)
	}
	rep, ok := w.replies[line]
	if !ok {
		return nil, fmt.Errorf("%s: no reply in this test", line)
	}
	return []byte(rep.out), rep.err
}

// dial is w's Dial. It reaches only w's own sockets and port: any other
// address is refused here, without touching the network, so the owner's
// bridge on port 2277 never sees a test.
func (w *world) dial(network, address string) (net.Conn, error) {
	w.dials = append(w.dials, network+" "+address)
	if (network == "unix" && strings.HasPrefix(address, os.TempDir())) || (network == "tcp" && address == w.listen) {
		return net.DialTimeout(network, address, time.Second)
	}
	return nil, &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
}

// run runs the doctor in w. Every check that fails must carry its fix, and
// it may run only its fixed commands.
func (w *world) run() Report {
	w.t.Helper()
	r := run(Env{Home: w.home, Getenv: func(string) string { return "" }, Now: w.now, Executable: w.exe, Dial: w.dial}, w.fake)
	var ids []string
	for _, c := range r.Checks {
		ids = append(ids, c.ID)
		if !c.OK && c.Fix == "" {
			w.t.Errorf("check %s fails (%s) with no fix", c.ID, c.Detail)
		}
		if c.OK && (c.Fix != "" || len(c.Commands) > 0) {
			w.t.Errorf("check %s passes, yet carries a fix: %q %q", c.ID, c.Fix, c.Commands)
		}
		if c.Name == "" || c.Detail == "" || c.State == "" {
			w.t.Errorf("check %s: a name %q, detail %q and state %q, none empty", c.ID, c.Name, c.Detail, c.State)
		}
	}
	if want := []string{"herdr", "keys", "port", "firewall", "login", "devices"}; !reflect.DeepEqual(ids, want) {
		w.t.Fatalf("checks %q; want %q, in that order", ids, want)
	}
	failed := slices.ContainsFunc(r.Checks, func(c Check) bool { return !c.OK })
	if r.OK == failed {
		w.t.Errorf("report ok = %v, with a check failing = %v", r.OK, failed)
	}
	return r
}

// check is the check id in r.
func check(t *testing.T, r Report, id string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %s", id)
	return Check{}
}

// passes checks c passed, in state.
func passes(t *testing.T, c Check, state string) {
	t.Helper()
	if !c.OK || c.State != state {
		t.Errorf("%s: ok %v, state %q (%s); want ok, %q", c.ID, c.OK, c.State, c.Detail, state)
	}
}

// fails checks c failed, in state, with a detail holding each of detail, a
// fix holding each of fix, and exactly commands to run.
func fails(t *testing.T, c Check, state string, detail, fix, commands []string) {
	t.Helper()
	if c.OK || c.State != state {
		t.Errorf("%s: ok %v, state %q (%s); want a failure, %q", c.ID, c.OK, c.State, c.Detail, state)
	}
	for _, s := range detail {
		if !strings.Contains(c.Detail, s) {
			t.Errorf("%s: detail %q doesn't say %q", c.ID, c.Detail, s)
		}
	}
	for _, s := range fix {
		if !strings.Contains(c.Fix, s) {
			t.Errorf("%s: fix %q doesn't say %q", c.ID, c.Fix, s)
		}
	}
	if !slices.Equal(c.Commands, commands) {
		t.Errorf("%s: commands %q; want %q", c.ID, c.Commands, commands)
	}
}

// TestDoctorReportsEachCheck is S5P-6: with fake command output and files,
// each of the Mac's checks (requirement 2.8): herdr's socket, the bridge's
// keys, port 2277, the firewall, the login item and the paired devices, each
// failure with its fix.
func TestDoctorReportsEachCheck(t *testing.T) {
	t.Run("a healthy Mac", func(t *testing.T) {
		w := newWorld(t)
		w.herdrUp()
		d := w.pair("pager-01")
		w.bridgeRunning("pager-01")
		r := w.run()
		if !r.OK {
			t.Errorf("report not ok: %+v", r.Checks)
		}
		passes(t, check(t, r, "herdr"), "ok")
		passes(t, check(t, r, "keys"), "ok")
		passes(t, check(t, r, "port"), "bridge")
		passes(t, check(t, r, "firewall"), "off")
		passes(t, check(t, r, "login"), "brew")
		devs := check(t, r, "devices")
		passes(t, devs, "connected")
		if want := []Device{{Name: "pager-01", Fingerprint: d.Fingerprint(), Connected: true}}; !reflect.DeepEqual(devs.Devices, want) {
			t.Errorf("devices %+v; want %+v", devs.Devices, want)
		}
		host, err := config.HostKey(w.dir)
		if err != nil {
			t.Fatal(err)
		}
		if keys := check(t, r, "keys"); !strings.Contains(keys.Detail, ssh.FingerprintSHA256(host)) {
			t.Errorf("keys: detail %q doesn't give the host key's fingerprint", keys.Detail)
		}
	})

	// herdr's socket.
	t.Run("herdr: no socket", func(t *testing.T) {
		w := newWorld(t)
		fails(t, check(t, w.run(), "herdr"), "missing", []string{w.socket}, []string{"herdr", "herdr_socket"}, nil)
	})
	t.Run("herdr: a socket nothing answers on", func(t *testing.T) {
		w := newWorld(t)
		w.staleSocket()
		fails(t, check(t, w.run(), "herdr"), "refused", []string{w.socket}, []string{"herdr"}, nil)
	})
	t.Run("herdr: a file that isn't a socket", func(t *testing.T) {
		w := newWorld(t)
		if err := os.WriteFile(w.socket, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		fails(t, check(t, w.run(), "herdr"), "not-socket", []string{w.socket}, []string{"herdr_socket"}, nil)
	})
	t.Run("herdr: the default socket, with no config.toml", func(t *testing.T) {
		w := newWorld(t)
		if err := os.Remove(filepath.Join(w.dir, config.File)); err != nil {
			t.Fatal(err)
		}
		def := filepath.Join(w.home, ".config", "herdr", "herdr.sock")
		r := w.run()
		fails(t, check(t, r, "herdr"), "missing", []string{def}, []string{"herdr"}, nil)
		// And the port is the default's, probed on the loopback address.
		port := check(t, r, "port")
		passes(t, port, "free")
		if !strings.Contains(port.Detail, config.DefaultListen) {
			t.Errorf("port: detail %q doesn't name %s", port.Detail, config.DefaultListen)
		}
		if !slices.Contains(w.dials, "tcp 127.0.0.1:2277") {
			t.Errorf("dialled %q; want tcp 127.0.0.1:2277 among them", w.dials)
		}
	})
	t.Run("herdr: $HERDR_SOCKET_PATH, with no herdr_socket", func(t *testing.T) {
		w := newWorld(t)
		w.socket = filepath.Join(filepath.Dir(w.socket), "elsewhere.sock")
		cfg := fmt.Sprintf("listen = %q\n", w.listen)
		if err := os.WriteFile(filepath.Join(w.dir, config.File), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		w.herdrUp()
		r := run(Env{Home: w.home, Now: w.now, Executable: w.exe, Dial: w.dial,
			Getenv: func(k string) string {
				if k == "HERDR_SOCKET_PATH" {
					return w.socket
				}
				return ""
			},
		}, w.fake)
		passes(t, check(t, r, "herdr"), "ok")
	})

	// The bridge's keys.
	t.Run("keys: no host key", func(t *testing.T) {
		w := healthyWorld(t)
		if err := os.Remove(filepath.Join(w.dir, config.HostKeyFile)); err != nil {
			t.Fatal(err)
		}
		fails(t, check(t, w.run(), "keys"), "missing", []string{config.HostKeyFile}, []string{"setup page"}, []string{"pager-bridge init"})
	})
	t.Run("keys: a host key that won't load", func(t *testing.T) {
		w := healthyWorld(t)
		if err := os.WriteFile(filepath.Join(w.dir, config.HostKeyFile), []byte("not a key\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fails(t, check(t, w.run(), "keys"), "invalid", []string{config.HostKeyFile}, []string{"pair", "again"}, nil)
	})
	t.Run("keys: no config.toml", func(t *testing.T) {
		w := healthyWorld(t)
		if err := os.Remove(filepath.Join(w.dir, config.File)); err != nil {
			t.Fatal(err)
		}
		fails(t, check(t, w.run(), "keys"), "missing", []string{config.File}, []string{"setup page"}, []string{"pager-bridge init"})
	})
	t.Run("keys: a config.toml that won't load", func(t *testing.T) {
		w := healthyWorld(t)
		if err := os.WriteFile(filepath.Join(w.dir, config.File), []byte("listen = \"nowhere\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fails(t, check(t, w.run(), "keys"), "invalid", []string{config.File + ":1", "listen"}, []string{"line"}, nil)
	})

	// Port 2277 (config.toml's listen).
	t.Run("port: free, no bridge running", func(t *testing.T) {
		w := newWorld(t)
		c := check(t, w.run(), "port")
		passes(t, c, "free")
		if !strings.Contains(c.Detail, w.listen) {
			t.Errorf("port: detail %q doesn't name %s", c.Detail, w.listen)
		}
	})
	t.Run("port: the bridge holds it", func(t *testing.T) {
		w := healthyWorld(t)
		c := check(t, w.run(), "port")
		passes(t, c, "bridge")
		if !strings.Contains(c.Detail, w.listen) {
			t.Errorf("port: detail %q doesn't name %s", c.Detail, w.listen)
		}
	})
	t.Run("port: another process holds it", func(t *testing.T) {
		w := newWorld(t)
		w.holdPort()
		_, port, _ := net.SplitHostPort(w.listen)
		fails(t, check(t, w.run(), "port"), "taken", []string{port}, []string{"stop"},
			[]string{"lsof -nP -iTCP:" + port + " -sTCP:LISTEN"})
	})
	t.Run("port: held, and status.json too old to be a running bridge's", func(t *testing.T) {
		w := healthyWorld(t)
		w.now = w.now.Add(status.MaxAge + time.Second)
		_, port, _ := net.SplitHostPort(w.listen)
		fails(t, check(t, w.run(), "port"), "taken", []string{port}, []string{"stop"},
			[]string{"lsof -nP -iTCP:" + port + " -sTCP:LISTEN"})
	})

	// The firewall, about the bridge's own path.
	allow := func(exe string) []string {
		q := exe
		if strings.ContainsAny(exe, " '") {
			q = "'" + strings.ReplaceAll(exe, "'", `'\''`) + "'"
		}
		return []string{"sudo " + fwTool + " --add " + q, "sudo " + fwTool + " --unblockapp " + q}
	}
	const on = "Firewall is enabled. (State = 1) \n" // the format in its binary: "Firewall is enabled. (State = %d) "
	other := "/Applications/Other.app"
	t.Run("firewall: off", func(t *testing.T) {
		w := healthyWorld(t)
		passes(t, check(t, w.run(), "firewall"), "off")
		if slices.ContainsFunc(w.calls, func(c string) bool { return strings.HasPrefix(c, fwTool+" --getapp") }) {
			t.Errorf("asked about the bridge while the firewall is off: %q", w.calls)
		}
	})
	t.Run("firewall: on and permitting the bridge", func(t *testing.T) {
		w := healthyWorld(t)
		w.firewall(on, permitted(w.exe))
		c := check(t, w.run(), "firewall")
		passes(t, c, "allowing")
		if !strings.Contains(c.Detail, w.exe) {
			t.Errorf("firewall: detail %q doesn't name %s", c.Detail, w.exe)
		}
	})
	t.Run("firewall: on and blocking the bridge's own path", func(t *testing.T) {
		w := healthyWorld(t)
		w.firewall(on, blocked(w.exe))
		fails(t, check(t, w.run(), "firewall"), "blocking", []string{w.exe, "blocks"}, []string{"administrator"}, allow(w.exe))
	})
	t.Run("firewall: on, and the bridge not part of it", func(t *testing.T) {
		w := healthyWorld(t)
		w.firewall(on, "The application "+w.exe+" is not part of the firewall \n")
		fails(t, check(t, w.run(), "firewall"), "blocking", []string{w.exe, "doesn't permit"}, []string{"administrator"}, allow(w.exe))
	})
	t.Run("firewall: permitting another path", func(t *testing.T) {
		w := healthyWorld(t)
		w.firewall(on, permitted(other))
		fails(t, check(t, w.run(), "firewall"), "blocking", []string{w.exe, "doesn't permit"}, nil, allow(w.exe))
	})
	t.Run("firewall: permitting, at a path with spaces and a quote", func(t *testing.T) {
		w := healthyWorld(t)
		w.exe = "/opt/herdr pager trial/it's/bin/pager-bridge"
		w.firewall(on, permitted(w.exe))
		passes(t, check(t, w.run(), "firewall"), "allowing")
	})
	t.Run("firewall: blocking, at a path that needs quoting", func(t *testing.T) {
		w := healthyWorld(t)
		w.exe = "/Applications/herdr pager/it's/pager-bridge"
		w.firewall(on, blocked(w.exe))
		fails(t, check(t, w.run(), "firewall"), "blocking", []string{w.exe}, nil, allow(w.exe))
	})
	t.Run("firewall: on, and the bridge's own path unknown", func(t *testing.T) {
		w := healthyWorld(t)
		w.exe = ""
		w.firewall(on, "")
		fails(t, check(t, w.run(), "firewall"), "unknown", []string{"own path"}, []string{"System Settings"}, nil)
	})
	t.Run("firewall: on, with block all set", func(t *testing.T) {
		w := healthyWorld(t)
		w.firewall(on, permitted(w.exe))
		w.replies[fwTool+" --getblockall"] = reply{out: "Firewall has block all state set to enabled. \n"}
		fails(t, check(t, w.run(), "firewall"), "block-all", nil, []string{"Block all incoming connections"}, nil)
	})
	t.Run("firewall: blocking all incoming connections", func(t *testing.T) {
		w := healthyWorld(t)
		w.firewall("Firewall is enabled. (State = 2) \n", "")
		fails(t, check(t, w.run(), "firewall"), "block-all", nil, []string{"Block all incoming connections"}, nil)
	})
	t.Run("firewall: its state unreadable", func(t *testing.T) {
		w := healthyWorld(t)
		w.replies[fwTool+" --getglobalstate"] = reply{err: errors.New("exit status 1")}
		fails(t, check(t, w.run(), "firewall"), "unknown", []string{"exit status 1"}, []string{"System Settings"}, nil)
	})
	t.Run("firewall: its verdict on the bridge unreadable", func(t *testing.T) {
		w := healthyWorld(t)
		w.firewall(on, "")
		w.replies[fwTool+" --getappblocked "+w.exe] = reply{err: errors.New("exit status 1")}
		fails(t, check(t, w.run(), "firewall"), "unknown", []string{"exit status 1"}, []string{"System Settings"}, nil)
	})

	// The login item: brew services, or the launchd agent.
	t.Run("login: brew services runs it", func(t *testing.T) {
		w := healthyWorld(t)
		passes(t, check(t, w.run(), "login"), "brew")
	})
	t.Run("login: brew services has it off", func(t *testing.T) {
		w := healthyWorld(t)
		w.brew(brewList("none"))
		fails(t, check(t, w.run(), "login"), "off", nil, []string{"setup page"}, []string{"brew services start herdr-pager"})
	})
	t.Run("login: brew services says it failed", func(t *testing.T) {
		w := healthyWorld(t)
		w.brew(strings.Replace(brewList("error"), `"error","user":"pat","file":"/opt/homebrew/opt/herdr-pager/homebrew.mxcl.herdr-pager.plist","exit_code":0`,
			`"error","user":"pat","file":"/opt/homebrew/opt/herdr-pager/homebrew.mxcl.herdr-pager.plist","exit_code":78`, 1))
		fails(t, check(t, w.run(), "login"), "error", []string{"78"}, []string{"log"}, []string{"brew services restart herdr-pager"})
	})
	t.Run("login: brew on PATH missing, Homebrew's own brew runs it", func(t *testing.T) {
		w := healthyWorld(t)
		w.noBrew()
		w.replies["/opt/homebrew/bin/brew services list --json"] = reply{out: brewList("started")}
		passes(t, check(t, w.run(), "login"), "brew")
	})
	t.Run("login: no Homebrew, the launchd agent installed", func(t *testing.T) {
		w := healthyWorld(t)
		w.noBrew()
		w.launchdAgent()
		passes(t, check(t, w.run(), "login"), "launchd")
	})
	t.Run("login: Homebrew without herdr-pager, the launchd agent installed", func(t *testing.T) {
		w := healthyWorld(t)
		w.brew("[]\n")
		w.launchdAgent()
		passes(t, check(t, w.run(), "login"), "launchd")
	})
	// Which bridge the launchd agent runs (the owner's second recheck of
	// checkpoint 7.2, 2026-11-07): the agent still ran rc.3's serve while
	// rc.4's doctor said "ok … runs the bridge at login". It must run this
	// bridge, by its path or a symlink to it; else the check says which one
	// it runs, and how to switch it.
	t.Run("login: the launchd agent runs another bridge", func(t *testing.T) {
		w := healthyWorld(t)
		w.noBrew()
		rc3 := filepath.Join(w.home, "herdr-pager-trial", "herdr-pager-1.0.0-rc.3", "bin", "pager-bridge")
		w.exe = filepath.Join(w.home, "herdr-pager-trial", "herdr-pager-1.0.0-rc.4", "bin", "pager-bridge")
		w.launchdAgentFor(rc3)
		fails(t, check(t, w.run(), "login"), "other", []string{"dev.herdr-pager.bridge", rc3 + " serve", "not this one", w.exe},
			[]string{"setup page", "Switch to this bridge"}, nil)
	})
	t.Run("login: the launchd agent runs this bridge, by a symlink", func(t *testing.T) {
		w := healthyWorld(t)
		w.noBrew()
		home, err := filepath.EvalSymlinks(w.home) // macOS's temporary folder is behind /var → /private/var
		if err != nil {
			t.Fatal(err)
		}
		w.exe = filepath.Join(home, "herdr-pager-1.0.0-rc.4", "bin", "pager-bridge")
		link := filepath.Join(home, "bin", "pager-bridge")
		for _, d := range []string{filepath.Dir(w.exe), filepath.Dir(link)} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(w.exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(w.exe, link); err != nil {
			t.Fatal(err)
		}
		w.launchdAgentFor(link)
		c := check(t, w.run(), "login")
		passes(t, c, "launchd")
		if !strings.Contains(c.Detail, w.exe) {
			t.Errorf("login: detail %q doesn't say it runs this bridge, %s", c.Detail, w.exe)
		}
	})
	t.Run("login: the launchd agent, which bridge it runs unreadable", func(t *testing.T) {
		w := healthyWorld(t)
		w.noBrew()
		w.launchdPlist("<plist/>\n")
		fails(t, check(t, w.run(), "login"), "other", []string{"dev.herdr-pager.bridge", "couldn't read"},
			[]string{"setup page", "Switch to this bridge"}, nil)
	})
	t.Run("login: neither", func(t *testing.T) {
		w := healthyWorld(t)
		w.noBrew()
		fails(t, check(t, w.run(), "login"), "none", nil, []string{"brew services start herdr-pager", "docs/bridge.md"}, nil)
	})
	t.Run("login: brew services fails, no launchd agent", func(t *testing.T) {
		w := healthyWorld(t)
		w.replies["brew services list --json"] = reply{err: errors.New("exit status 1")}
		fails(t, check(t, w.run(), "login"), "unknown", []string{"exit status 1"}, []string{"brew services start herdr-pager"}, nil)
	})
	t.Run("login: both, competing for the port", func(t *testing.T) {
		w := healthyWorld(t)
		w.launchdAgent()
		fails(t, check(t, w.run(), "login"), "both", []string{"dev.herdr-pager.bridge"}, []string{"one"},
			[]string{"launchctl bootout gui/$(id -u)/dev.herdr-pager.bridge", "rm ~/Library/LaunchAgents/dev.herdr-pager.bridge.plist"})
	})

	// The paired devices, and whether each is connected.
	t.Run("devices: none paired", func(t *testing.T) {
		w := newWorld(t)
		w.herdrUp()
		c := check(t, w.run(), "devices")
		fails(t, c, "none", nil, []string{"setup page"}, []string{"pager-bridge setup"})
		if len(c.Devices) != 0 {
			t.Errorf("devices %+v; want none", c.Devices)
		}
	})
	t.Run("devices: paired, no bridge running", func(t *testing.T) {
		w := newWorld(t)
		d := w.pair("pager-01")
		c := check(t, w.run(), "devices")
		fails(t, c, "not-running", []string{"no bridge is running"}, []string{"log"}, []string{"brew services restart herdr-pager"})
		if want := []Device{{Name: "pager-01", Fingerprint: d.Fingerprint()}}; !reflect.DeepEqual(c.Devices, want) {
			t.Errorf("devices %+v; want %+v", c.Devices, want)
		}
	})
	t.Run("devices: paired, no bridge running, run at login off", func(t *testing.T) {
		w := newWorld(t)
		w.pair("pager-01")
		w.brew(brewList("none"))
		fails(t, check(t, w.run(), "devices"), "not-running", nil, nil, []string{"brew services start herdr-pager"})
	})
	t.Run("devices: paired, no bridge running, the launchd agent", func(t *testing.T) {
		w := newWorld(t)
		w.pair("pager-01")
		w.noBrew()
		w.launchdAgent()
		fails(t, check(t, w.run(), "devices"), "not-running", nil, nil, []string{"launchctl kickstart -k gui/$(id -u)/dev.herdr-pager.bridge"})
	})
	t.Run("devices: paired, no bridge running, nothing at login", func(t *testing.T) {
		w := newWorld(t)
		w.pair("pager-01")
		w.noBrew()
		fails(t, check(t, w.run(), "devices"), "not-running", nil, nil, []string{"pager-bridge serve"})
	})
	t.Run("devices: one connected, one not", func(t *testing.T) {
		w := newWorld(t)
		w.herdrUp()
		d1, d2 := w.pair("pager-01"), w.pair("pager-02")
		w.bridgeRunning("pager-01")
		c := check(t, w.run(), "devices")
		fails(t, c, "disconnected", []string{"pager-02"}, []string{"Wi-Fi", "firewall"}, nil)
		if strings.Contains(c.Detail, "pager-01") {
			t.Errorf("devices: detail %q names pager-01, which is connected", c.Detail)
		}
		want := []Device{{Name: "pager-01", Fingerprint: d1.Fingerprint(), Connected: true}, {Name: "pager-02", Fingerprint: d2.Fingerprint()}}
		if !reflect.DeepEqual(c.Devices, want) {
			t.Errorf("devices %+v; want %+v", c.Devices, want)
		}
	})
	t.Run("devices: devices.toml won't load", func(t *testing.T) {
		w := healthyWorld(t)
		if err := os.WriteFile(filepath.Join(w.dir, devices.File), []byte("[[device]]\nname = 3\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fails(t, check(t, w.run(), "devices"), "error", []string{devices.File + ":"}, []string{"pair"}, nil)
	})
}

// TestDoctorPrintsItsReport checks what `pager-bridge doctor` prints: for
// people, each verdict and detail, the devices, and each failure's fix and
// commands; and with --json, the fields the setup page and the Claude Code
// skill read (requirement 2.8).
func TestDoctorPrintsItsReport(t *testing.T) {
	r := Report{Checks: []Check{
		{ID: CheckPort, Name: "Port 2277", OK: true, State: "free", Detail: "free for the bridge"},
		{ID: CheckFirewall, Name: "The firewall", State: "blocking", Detail: "on, and it blocks " + brewExe,
			Fix: "Allow it.", Commands: []string{"sudo a", "sudo b"}},
		{ID: CheckDevices, Name: "Paired pagers", State: "disconnected", Detail: "pager-02 isn't connected", Fix: "Check it's on.",
			Devices: []Device{{Name: "pager-01", Fingerprint: "SHA256:one", Connected: true}, {Name: "pager-02", Fingerprint: "SHA256:two"}}},
	}}
	var b strings.Builder
	if err := r.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	want := "ok    Port 2277      free for the bridge\n" +
		"FAIL  The firewall   on, and it blocks " + brewExe + "\n" +
		"      Fix: Allow it.\n" +
		"        sudo a\n" +
		"        sudo b\n" +
		"FAIL  Paired pagers  pager-02 isn't connected\n" +
		"      pager-01  connected      SHA256:one\n" +
		"      pager-02  not connected  SHA256:two\n" +
		"      Fix: Check it's on.\n" +
		"\n2 checks of 3 fail.\n"
	if b.String() != want {
		t.Errorf("printed:\n%s\nwant:\n%s", b.String(), want)
	}

	r.Checks = r.Checks[:1]
	r.OK = true
	b.Reset()
	r.WriteText(&b)
	if !strings.HasSuffix(b.String(), "\nEvery check passes.\n") {
		t.Errorf("printed %q; want it to end with every check passing", b.String())
	}

	// The JSON's fields, which the page and the skill read by name.
	data, err := json.Marshal(Report{Checks: []Check{
		{ID: CheckDevices, Name: "n", State: "s", Detail: "d", Fix: "f", Commands: []string{"c"}, Devices: []Device{{Name: "p", Fingerprint: "SHA256:x"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want = `{"ok":false,"checks":[{"id":"devices","name":"n","ok":false,"state":"s","detail":"d","fix":"f","commands":["c"],` +
		`"devices":[{"name":"p","fingerprint":"SHA256:x","connected":false}]}]}`
	if string(data) != want {
		t.Errorf("JSON %s; want %s", data, want)
	}
}
