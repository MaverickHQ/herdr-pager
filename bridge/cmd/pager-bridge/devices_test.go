package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/status"
)

// configFixtures holds the bridge's file fixtures (testdata/config/README.md).
const configFixtures = "../../testdata/config"

// pairTwo gives home's bridge directory the fixture devices.toml: pager-01
// and pager-02.
func pairTwo(t *testing.T, home string) string {
	t.Helper()
	dir := bridgeDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(configFixtures, "devices.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "devices.toml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// rows splits the lines of a table that start with a device's name into
// their columns, by name.
func rows(out string, names ...string) map[string][]string {
	r := map[string][]string{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		for _, n := range names {
			if len(f) > 0 && f[0] == n {
				r[n] = f
			}
		}
	}
	return r
}

// TestDevicesListShowsFullFingerprints checks `devices list` (parent 12.1):
// each device's name, key type, full SHA256 fingerprint, pairing date and
// whether it's connected, from devices.toml and status.json. With status.json
// older than 5 s, or missing, no bridge is running and none is connected.
func TestDevicesListShowsFullFingerprints(t *testing.T) {
	// The fixture status.json: pager-01 connected, written at 18:10:00Z.
	fixture, running, err := status.Read(configFixtures, time.Date(2026, 10, 23, 18, 10, 0, 0, time.UTC))
	if err != nil || !running {
		t.Fatalf("the fixture status.json: running %v, %v", running, err)
	}
	for _, c := range []struct {
		name      string
		age       time.Duration // of status.json; -1 for none
		connected [2]string     // pager-01's, pager-02's
	}{
		{"a fresh status.json", 0, [2]string{"yes", "no"}},
		{"a status.json 6 s old", 6 * time.Second, [2]string{"no", "no"}},
		{"no status.json", -1, [2]string{"no", "no"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := newHome(t)
			dir := pairTwo(t, home)
			if c.age >= 0 {
				if err := status.Write(dir, fixture, time.Now().Add(-c.age)); err != nil {
					t.Fatal(err)
				}
			}
			stdout, stderr, code := pagerBridge(t, "devices", "list")
			if code != 0 || stderr != "" {
				t.Fatalf("exit %d, stderr %q; want exit 0 and no stderr", code, stderr)
			}
			t.Log("\n" + stdout)
			got := rows(stdout, "pager-01", "pager-02")
			for i, want := range [][]string{
				{"pager-01", "ssh-ed25519", "SHA256://a7FlX9Y4t0WSS+W8ANyY4w+5+axSEVbGzNXp6txjY", "2026-10-22T09:30:00Z", c.connected[0]},
				{"pager-02", "ssh-ed25519", "SHA256:gX1c8/ZMiowHYSu8PWSbHJMDfUs8t944xZ5Nx2lT27I", "2026-10-23T18:05:00Z", c.connected[1]},
			} {
				if strings.Join(got[want[0]], " ") != strings.Join(want, " ") {
					t.Errorf("device %d's row is %q; want %q, in:\n%s", i+1, got[want[0]], want, stdout)
				}
			}
			if notRunning := strings.Contains(stdout, "No bridge is running"); notRunning != (c.age != 0) {
				t.Errorf("stdout says no bridge is running: %v; want %v:\n%s", notRunning, c.age != 0, stdout)
			}
		})
	}

	t.Run("no devices", func(t *testing.T) {
		newHome(t)
		stdout, stderr, code := pagerBridge(t, "devices", "list")
		if code != 0 || stderr != "" || !strings.Contains(stdout, "No paired devices") {
			t.Errorf("exit %d, stdout %q, stderr %q; want exit 0 and \"No paired devices\"", code, stdout, stderr)
		}
	})
}

// names lists devs' names, space-separated.
func names(devs []devices.Device) string {
	var n []string
	for _, d := range devs {
		n = append(n, d.Name)
	}
	return strings.Join(n, " ")
}

// TestDevicesRevokeIsAtomic checks `devices revoke <name>` (parent 12.2,
// 12.3): the entry is gone and the other stays as it was; devices.toml stays
// 0600, with no temporary file left beside it; and a load at any moment, as
// serve's every 500 ms, sees the old file or the new one, never a partial
// one. An unknown name is an error that changes nothing.
func TestDevicesRevokeIsAtomic(t *testing.T) {
	home := newHome(t)
	dir := pairTwo(t, home)
	path := filepath.Join(dir, "devices.toml")
	two, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := pagerBridge(t, "devices", "revoke", "pager-09")
	if now, _ := os.ReadFile(path); code != 1 || !strings.Contains(stderr, "pager-09") || string(now) != string(two) {
		t.Errorf("revoking pager-09: exit %d, stdout %q, stderr %q, devices.toml changed: %v; want exit 1 naming pager-09, and no change",
			code, stdout, stderr, string(now) != string(two))
	}

	stop, done := make(chan struct{}), make(chan struct{})
	var old, new int
	var bad []string
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			devs, err := devices.Load(path)
			switch n := names(devs); {
			case err != nil:
				bad = append(bad, err.Error())
			case n == "pager-01 pager-02":
				old++
			case n == "pager-01":
				new++
			default:
				bad = append(bad, n)
			}
		}
	}()
	for i := 0; i < 20; i++ {
		restore := filepath.Join(dir, ".restore")
		if err := os.WriteFile(restore, two, 0o600); err == nil {
			err = os.Rename(restore, path)
		}
		if err != nil {
			t.Error(err)
			break
		}
		if stdout, stderr, code = pagerBridge(t, "devices", "revoke", "pager-02"); code != 0 {
			t.Errorf("revoking pager-02: exit %d, stderr %q", code, stderr)
			break
		}
	}
	close(stop)
	<-done
	t.Logf("%d loads saw both devices and %d saw pager-01 alone; the last revoke printed %q", old, new, stdout)
	if len(bad) > 0 || old == 0 || new == 0 {
		t.Errorf("concurrent loads saw %q; want only the old file or the new one, and each at least once", bad)
	}

	devs, err := devices.Load(path)
	if err != nil || len(devs) != 1 {
		t.Fatalf("devices.toml after revoking pager-02 loads %q, %v; want pager-01 alone", names(devs), err)
	}
	if d := devs[0]; d.Name != "pager-01" || d.Key != "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAnULfN18UYEHFPLj/PhfEMf7NvNdFoELzBJBtMsCVgj pager-01" ||
		!d.Added.Equal(time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("pager-01 is now %q %q %v; want it as it was", d.Name, d.Key, d.Added)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode() != 0o600 {
		t.Errorf("devices.toml: %v, %v; want mode 0600", fi.Mode(), err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the bridge's directory holds %v; want devices.toml alone", entries)
	}
	if !strings.Contains(stdout, "pager-02") || !strings.Contains(stdout, "SHA256:gX1c8/ZMiowHYSu8PWSbHJMDfUs8t944xZ5Nx2lT27I") {
		t.Errorf("revoke printed %q; want pager-02 and its full fingerprint", stdout)
	}
}
