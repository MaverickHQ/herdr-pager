package devices

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// newDevice is a device named name with a new ed25519 key.
func newDevice(t *testing.T, name string) Device {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) + " " + name
	return Device{Name: name, Key: line, PublicKey: key, Added: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
}

// TestDevicesAddAndRevokeTakeTurns: Adds and Revokes run at once (two
// provisions, or a provision and a revoke) each read, change and write the
// whole file under a lock, so none loses another's change (the security
// pass, 2026-11-01). The lock leaves no file behind.
func TestDevicesAddAndRevokeTakeTurns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, File)
	const n = 16
	var devs []Device
	for i := range n {
		devs = append(devs, newDevice(t, fmt.Sprintf("pager-%02d", i+1)))
	}
	var wg sync.WaitGroup
	for _, d := range devs {
		wg.Go(func() {
			if err := Add(path, d); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, err := Load(path)
	if err != nil || len(got) != n {
		t.Fatalf("after %d Adds at once, devices.toml holds %d devices (%v); want all %d", n, len(got), err, n)
	}

	for _, d := range devs[:n/2] {
		wg.Go(func() {
			if err := Revoke(path, d.Name); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, err = Load(path)
	var names []string
	for _, d := range got {
		names = append(names, d.Name)
	}
	slices.Sort(names)
	var want []string
	for _, d := range devs[n/2:] {
		want = append(want, d.Name)
	}
	if err != nil || !slices.Equal(names, want) {
		t.Fatalf("after %d Revokes at once, devices.toml holds %q (%v); want %q", n/2, names, err, want)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the directory holds %v; want devices.toml alone", entries)
	}
}

// configDir holds the bridge's file fixtures (testdata/config/README.md).
const configDir = "../../testdata/config"

// TestDevicesLoadReadsFullKeys loads the fixture devices.toml: each device's
// name, key type, full public key and date; then a missing file (no devices)
// and each malformed file (an error naming its line).
func TestDevicesLoadReadsFullKeys(t *testing.T) {
	devs, err := Load(filepath.Join(configDir, File))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name, key, fingerprint string
		added                  time.Time
	}{
		{"pager-01", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAnULfN18UYEHFPLj/PhfEMf7NvNdFoELzBJBtMsCVgj pager-01",
			"SHA256://a7FlX9Y4t0WSS+W8ANyY4w+5+axSEVbGzNXp6txjY", time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)},
		{"pager-02", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBFlTBpNN2IZ/XfcnYg5nSzboXnFnSWwfn9NLno05IkE pager-02",
			"SHA256:gX1c8/ZMiowHYSu8PWSbHJMDfUs8t944xZ5Nx2lT27I", time.Date(2026, 10, 23, 18, 5, 0, 0, time.UTC)},
	}
	if len(devs) != len(want) {
		t.Fatalf("loaded %d devices, want %d", len(devs), len(want))
	}
	for i, w := range want {
		d := devs[i]
		if d.Name != w.name || d.Key != w.key || !d.Added.Equal(w.added) {
			t.Errorf("device %d: %q %q %v; want %q %q %v", i, d.Name, d.Key, d.Added, w.name, w.key, w.added)
		}
		if d.KeyType() != "ssh-ed25519" || d.Fingerprint() != w.fingerprint {
			t.Errorf("%s: %s %s; want ssh-ed25519 %s", w.name, d.KeyType(), d.Fingerprint(), w.fingerprint)
		}
		blob, _ := base64.StdEncoding.DecodeString(strings.Fields(w.key)[1])
		if d.PublicKey == nil || !bytes.Equal(d.PublicKey.Marshal(), blob) {
			t.Errorf("%s: the parsed key isn't the full key", w.name)
		}
	}

	devs, err = Load(filepath.Join(t.TempDir(), File))
	if err != nil || len(devs) != 0 {
		t.Errorf("a missing file: %v, %v; want no devices and no error", devs, err)
	}

	for _, c := range []struct {
		file string
		line int
	}{
		{"syntax.toml", 7},        // an unterminated string
		{"truncated-key.toml", 8}, // pager-02's key is cut short
		{"rsa-key.toml", 3},       // device keys are ed25519 only
		{"duplicate-name.toml", 7},
		{"no-added.toml", 1}, // no line has the missing date, so its [[device]]
		{"unknown-key.toml", 4},
	} {
		path := filepath.Join(configDir, "devices-invalid", c.file)
		_, err := Load(path)
		t.Log(err)
		var le *LineError
		switch {
		case !errors.As(err, &le):
			t.Errorf("%s: %v; want a *LineError", c.file, err)
		case le.Path != path || le.Line != c.line:
			t.Errorf("%s: %v; want line %d", c.file, err, c.line)
		case !strings.HasPrefix(err.Error(), path+":"):
			t.Errorf("%s: %q doesn't start with the file's name", c.file, err)
		}
	}
}
