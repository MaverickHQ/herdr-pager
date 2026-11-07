package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/config"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
)

// newHome gives the test its own empty HOME, in a short temporary path.
func newHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("", "hp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	return home
}

// bridgeDir is the bridge's directory under home.
func bridgeDir(home string) string { return filepath.Join(home, ".config", "herdr-pager") }

// TestInitCreatesIdentityWithModes checks what init creates (parent 1.1, 1.2,
// 13.9), whatever the owner's umask: the directory 0700; an ed25519 host key
// 0600 and its .pub 0644; config.toml with its defaults and an empty
// devices.toml, each 0600; and that it prints the host key's full SHA256
// fingerprint and the listen address.
func TestInitCreatesIdentityWithModes(t *testing.T) {
	for _, mask := range []int{0o022, 0o077, 0o000} {
		t.Run(fmt.Sprintf("umask %03o", mask), func(t *testing.T) {
			home := newHome(t)
			old := syscall.Umask(mask)
			stdout, stderr, code := pagerBridge(t, "init")
			syscall.Umask(old)
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}

			dir := bridgeDir(home)
			for _, f := range []struct {
				name string
				mode os.FileMode
			}{
				{".", os.ModeDir | 0o700},
				{"host_ed25519", 0o600},
				{"host_ed25519.pub", 0o644},
				{"config.toml", 0o600},
				{"devices.toml", 0o600},
			} {
				fi, err := os.Stat(filepath.Join(dir, f.name))
				switch {
				case err != nil:
					t.Error(err)
				case fi.Mode() != f.mode:
					t.Errorf("%s: mode %v; want %v", f.name, fi.Mode(), f.mode)
				}
			}

			pubLine, err := os.ReadFile(filepath.Join(dir, "host_ed25519.pub"))
			if err != nil {
				t.Fatal(err)
			}
			pub, _, _, rest, err := ssh.ParseAuthorizedKey(pubLine)
			if err != nil || len(bytes.TrimSpace(rest)) > 0 || pub.Type() != "ssh-ed25519" {
				t.Fatalf("host_ed25519.pub is %q (%v); want one ssh-ed25519 public key line", pubLine, err)
			}
			pem, err := os.ReadFile(filepath.Join(dir, "host_ed25519"))
			if err != nil {
				t.Fatal(err)
			}
			signer, err := ssh.ParsePrivateKey(pem)
			if err != nil || !bytes.Equal(signer.PublicKey().Marshal(), pub.Marshal()) {
				t.Errorf("host_ed25519 (%v) isn't the private half of host_ed25519.pub", err)
			}

			fp := ssh.FingerprintSHA256(pub)
			if !strings.Contains(stdout, fp) || !strings.Contains(stdout, "0.0.0.0:2277") {
				t.Errorf("stdout doesn't give the full fingerprint %s and the listen address 0.0.0.0:2277:\n%s", fp, stdout)
			}

			c, err := config.Load(filepath.Join(dir, "config.toml"), config.Env{Home: home, Getenv: func(string) string { return "" }})
			want := config.Config{
				Listen:      "0.0.0.0:2277",
				HerdrSocket: filepath.Join(home, ".config", "herdr", "herdr.sock"),
				IdleTimeout: 180 * time.Second,
				LogLevel:    "info",
			}
			if err != nil || c != want {
				t.Errorf("config.toml loads as %+v, %v; want the defaults %+v", c, err, want)
			}
			if devs, err := devices.Load(filepath.Join(dir, "devices.toml")); err != nil || len(devs) != 0 {
				t.Errorf("devices.toml loads as %v, %v; want no devices", devs, err)
			}
		})
	}
}

// A file is what init must leave alone: its bytes, mode and modification
// time, so a second run rewrites nothing.
type file struct {
	data  string
	mode  os.FileMode
	mtime time.Time
}

// snapshot reads every file in dir.
func snapshot(t *testing.T, dir string) map[string]file {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]file{}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		fi, err2 := os.Stat(path)
		if err != nil || err2 != nil {
			t.Fatal(err, err2)
		}
		files[e.Name()] = file{string(data), fi.Mode(), fi.ModTime()}
	}
	return files
}

// sameFiles reports how got differs from want.
func sameFiles(t *testing.T, when string, got, want map[string]file) {
	t.Helper()
	for name, w := range want {
		switch g, ok := got[name]; {
		case !ok:
			t.Errorf("%s: %s is gone", when, name)
		case g.data != w.data && name == "host_ed25519":
			t.Errorf("%s: %s changed", when, name) // a private key stays out of the test's output
		case g.data != w.data:
			t.Errorf("%s: %s changed:\n%s\nwas:\n%s", when, name, g.data, w.data)
		case g.mode != w.mode || !g.mtime.Equal(w.mtime):
			t.Errorf("%s: %s was rewritten: %v %v; was %v %v", when, name, g.mode, g.mtime, w.mode, w.mtime)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s: %s is new", when, name)
		}
	}
}

// TestInitTwiceIsByteIdentical checks that a second init changes no file and
// prints the same fingerprint (parent 1.3): not the host key, not a
// config.toml the owner edited (whose listen address it then prints), and
// not the paired devices. It only recreates a file that's missing, such as
// the .pub, from the key that's there.
func TestInitTwiceIsByteIdentical(t *testing.T) {
	home := newHome(t)
	dir := bridgeDir(home)
	first, stderr, code := pagerBridge(t, "init")
	if code != 0 {
		t.Fatalf("first init: exit %d, stderr %q", code, stderr)
	}
	before := snapshot(t, dir)
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(before["host_ed25519.pub"].data))
	if err != nil {
		t.Fatal(err)
	}
	fp := ssh.FingerprintSHA256(pub)

	second, stderr, code := pagerBridge(t, "init")
	if code != 0 {
		t.Fatalf("second init: exit %d, stderr %q", code, stderr)
	}
	sameFiles(t, "after a second init", snapshot(t, dir), before)
	if !strings.Contains(first, fp) || second != first {
		t.Errorf("the second init printed:\n%s\nthe first:\n%s\nwant both to give %s", second, first, fp)
	}

	// The owner's settings and paired devices stay as they are.
	edited := "listen = \"127.0.0.1:2299\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	paired, err := os.ReadFile(filepath.Join("..", "..", "testdata", "config", "devices.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "devices.toml"), paired, 0o600); err != nil {
		t.Fatal(err)
	}
	before = snapshot(t, dir)
	stdout, stderr, code := pagerBridge(t, "init")
	if code != 0 {
		t.Fatalf("init over an edited config: exit %d, stderr %q", code, stderr)
	}
	sameFiles(t, "after init over an edited config.toml and two paired devices", snapshot(t, dir), before)
	if !strings.Contains(stdout, fp) || !strings.Contains(stdout, "127.0.0.1:2299") {
		t.Errorf("init over an edited config.toml printed:\n%s\nwant %s and 127.0.0.1:2299", stdout, fp)
	}

	// A missing .pub comes back from the key, the same.
	if err := os.Remove(filepath.Join(dir, "host_ed25519.pub")); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := pagerBridge(t, "init"); code != 0 {
		t.Fatalf("init without the .pub: exit %d, stderr %q", code, stderr)
	}
	after := snapshot(t, dir)
	if g, w := after["host_ed25519.pub"], before["host_ed25519.pub"]; g.data != w.data || g.mode != 0o644 {
		t.Errorf("the recreated .pub is %q %v; want %q 0644", g.data, g.mode, w.data)
	}
	delete(after, "host_ed25519.pub")
	delete(before, "host_ed25519.pub")
	sameFiles(t, "after init without the .pub", after, before)
}

// TestInitWarnsWithoutHerdrSocket checks that init still succeeds when herdr
// isn't reachable at the configured socket, and warns naming the path it
// tried (parent 1.5): no socket at the default path or at
// $HERDR_SOCKET_PATH, a socket nobody listens on, a file that isn't a
// socket. With a fake herdr listening it doesn't warn, and it sends that
// herdr nothing: it calls no method (parent 13.2).
func TestInitWarnsWithoutHerdrSocket(t *testing.T) {
	sockDir, err := os.MkdirTemp("", "hp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })

	listening := filepath.Join(sockDir, "herdr.sock")
	l, err := net.Listen("unix", listening)
	if err != nil {
		t.Fatal(err)
	}
	sent := make(chan int64, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			sent <- 0 // init never connected
			return
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, _ := io.Copy(io.Discard, conn)
		conn.Close()
		sent <- n
	}()

	stale := filepath.Join(sockDir, "stale.sock")
	sl, err := net.ListenUnix("unix", &net.UnixAddr{Name: stale, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	sl.SetUnlinkOnClose(false)
	sl.Close()
	notSocket := filepath.Join(sockDir, "file.sock")
	if err := os.WriteFile(notSocket, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name, env string // env is $HERDR_SOCKET_PATH, or "" for the default path
		warn      bool
	}{
		{"no socket at the default path", "", true},
		{"no socket at $HERDR_SOCKET_PATH", filepath.Join(sockDir, "none.sock"), true},
		{"nothing listening", stale, true},
		{"not a socket", notSocket, true},
		{"herdr listening", listening, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := newHome(t)
			path := filepath.Join(home, ".config", "herdr", "herdr.sock")
			if c.env != "" {
				t.Setenv("HERDR_SOCKET_PATH", c.env)
				path = c.env
			}
			stdout, stderr, code := pagerBridge(t, "init")
			if code != 0 || !strings.Contains(stdout, "SHA256:") || !strings.Contains(stdout, "0.0.0.0:2277") {
				t.Fatalf("exit %d, stdout %q, stderr %q; want exit 0 with the fingerprint and listen address", code, stdout, stderr)
			}
			t.Log(stderr)
			switch warned := strings.Contains(stderr, "warning") && strings.Contains(stderr, path); {
			case c.warn && !warned:
				t.Errorf("stderr is %q; want a warning naming %s", stderr, path)
			case !c.warn && stderr != "":
				t.Errorf("stderr is %q; want nothing, with herdr listening", stderr)
			}
		})
	}

	l.Close()
	if n := <-sent; n != 0 {
		t.Errorf("init sent herdr %d bytes; want none", n)
	}
}
