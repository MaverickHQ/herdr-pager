// Package devices is devices.toml: the paired pagers, each by its full public
// key (parent 12, 13.8). `init` and `provision` write it, `devices` lists and
// revokes from it, and the SSH server authenticates against it.
package devices

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/crypto/ssh"
)

// File is devices.toml's name in the bridge's directory (mode 0600, written
// atomically: parent 12.3).
const File = "devices.toml"

// A Device is one paired pager.
type Device struct {
	Name      string        // name
	Key       string        // key: the full authorized_keys line
	PublicKey ssh.PublicKey // Key, parsed
	Added     time.Time     // added
}

// KeyType is the key's type, "ssh-ed25519".
func (d Device) KeyType() string { return d.PublicKey.Type() }

// Fingerprint is the key's full SHA256 fingerprint, "SHA256:…" (a label for
// people: keys are compared in full, parent 13.8).
func (d Device) Fingerprint() string { return ssh.FingerprintSHA256(d.PublicKey) }

// A LineError is a devices.toml that won't load, at the line to fix.
type LineError struct {
	Path   string
	Line   int // from 1
	Reason string
}

func (e *LineError) Error() string { return fmt.Sprintf("%s:%d: %s", e.Path, e.Line, e.Reason) }

// A device's name: the provisioning contract's rule (testdata/provisioning/).
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,23}$`)

// Load reads devices.toml. A missing file is no devices. Every key must be a
// full ed25519 public key (design D4), and no name or key may appear twice.
func Load(path string) ([]Device, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parse(path, data)
}

// parse reads data as the devices.toml at path.
func parse(path string, data []byte) ([]Device, error) {
	var f struct {
		Device []struct {
			Name  string    `toml:"name"`
			Key   string    `toml:"key"`
			Added time.Time `toml:"added"`
		} `toml:"device"`
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		var de *toml.DecodeError
		var se *toml.StrictMissingError
		switch {
		case errors.As(err, &de):
			row, _ := de.Position()
			return nil, &LineError{path, row, de.Error()}
		case errors.As(err, &se) && len(se.Errors) > 0:
			row, _ := se.Errors[0].Position()
			return nil, &LineError{path, row, "unknown key " + strings.Join(se.Errors[0].Key(), ".")}
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	devs := make([]Device, 0, len(f.Device))
	names, keys := map[string]bool{}, map[string]bool{}
	for i, e := range f.Device {
		fail := func(field, reason string) error {
			return &LineError{path, line(data, i, field), fmt.Sprintf("device %d: %s", i+1, reason)}
		}
		if !nameRE.MatchString(e.Name) {
			return nil, fail("name", "name: 1 to 24 of A-Z a-z 0-9 . _ -, starting with a letter or digit")
		}
		if names[e.Name] {
			return nil, fail("name", "another device is named "+e.Name)
		}
		pub, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(e.Key))
		switch {
		case err != nil || len(bytes.TrimSpace(rest)) > 0:
			return nil, fail("key", "key: not one full public key line")
		case pub.Type() != ssh.KeyAlgoED25519:
			return nil, fail("key", "key: an "+pub.Type()+" key; device keys are ssh-ed25519")
		case keys[string(pub.Marshal())]:
			return nil, fail("key", "key: another device has this key")
		case e.Added.IsZero():
			return nil, fail("added", "added: missing")
		}
		names[e.Name], keys[string(pub.Marshal())] = true, true
		devs = append(devs, Device{Name: e.Name, Key: e.Key, PublicKey: pub, Added: e.Added})
	}
	return devs, nil
}

// line finds where device i (from 0) sets field, or else its [[device]]
// header, in a file laid out as `init` and `provision` write it.
func line(data []byte, i int, field string) int {
	n, header := -1, 0
	for ln, l := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "[[device]]"):
			n++
			if n > i {
				return header
			}
			header = ln + 1
		case n == i && strings.HasPrefix(t, field) && strings.HasPrefix(strings.TrimSpace(t[len(field):]), "="):
			return ln + 1
		}
	}
	return header
}

// header starts every devices.toml.
const header = "# The paired pagers, each by its full public key. `pager-bridge provision`\n" +
	"# adds them, and `pager-bridge devices revoke <name>` removes them.\n"

// Create writes a devices.toml with no devices at path (parent 1.1), unless
// one is there: it never touches the paired devices (parent 1.3).
func Create(path string) error {
	return write(path, []byte(header), func(tmp, path string) error {
		if err := os.Link(tmp, path); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		return nil
	})
}

// write puts data at path atomically, mode 0600: a temporary file in path's
// directory, moved into place by put, so a reader sees one whole file (parent
// 12.3).
func write(path string, data []byte, put func(tmp, path string) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".devices-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // the temporary name: a no-op once renamed
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return put(tmp.Name(), path)
}

// lock takes an exclusive flock on path's directory, waiting for any other
// Add or Revoke, in this process or another, to finish, and returns its
// unlock. The directory is locked, not the file, since each write replaces
// the file, and no lock file is left behind (the security pass, 2026-11-01).
func lock(path string) (unlock func(), err error) {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(dir.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		dir.Close()
		return nil, fmt.Errorf("%s: locking its directory: %w", path, err)
	}
	return func() { dir.Close() }, nil // closing it releases the lock
}

// Add adds d, or updates the entry with d's key (parent 16.3), atomically.
// The new file must load: a device Load would refuse, such as one whose key
// isn't ed25519, is never written. It holds the directory's lock from its
// read to its write.
func Add(path string, d Device) error {
	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	devs, err := Load(path)
	if err != nil {
		return err
	}
	if d.PublicKey == nil {
		return fmt.Errorf("%s: not adding %s: no key", path, d.Name)
	}
	if i := slices.IndexFunc(devs, func(e Device) bool { return sameKey(e.PublicKey, d.PublicKey) }); i >= 0 {
		devs[i] = d
	} else {
		devs = append(devs, d)
	}
	data := encode(devs)
	if _, err := parse(path, data); err != nil {
		if le := (*LineError)(nil); errors.As(err, &le) {
			err = errors.New(le.Reason) // its line is in a file never written
		}
		return fmt.Errorf("%s: not adding %s: %w", path, d.Name, err)
	}
	return write(path, data, os.Rename)
}

// Revoke removes the device named name, atomically (parent 12.2). A name
// that isn't there is an error, and the file stays as it was. It holds the
// directory's lock from its read to its write, as Add does.
func Revoke(path, name string) error {
	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	devs, err := Load(path)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(devs, func(d Device) bool { return d.Name == name })
	if i < 0 {
		return fmt.Errorf("%s: no device named %q", path, name)
	}
	return write(path, encode(slices.Delete(devs, i, i+1)), os.Rename)
}

// encode lays devs out as devices.toml, in the layout Load's line finder
// expects (parent design §Data models).
func encode(devs []Device) []byte {
	var b strings.Builder
	b.WriteString(header)
	for _, d := range devs {
		fmt.Fprintf(&b, "\n[[device]]\nname  = %s\nkey   = %s\nadded = %s\n",
			quote(d.Name), quote(d.Key), d.Added.UTC().Format(time.RFC3339))
	}
	return []byte(b.String())
}

// quote is s as a TOML basic string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Match returns the device whose key is byte for byte key (parent 3.2).
func Match(devs []Device, key ssh.PublicKey) (Device, bool) {
	for _, d := range devs {
		if sameKey(d.PublicKey, key) {
			return d, true
		}
	}
	return Device{}, false
}

// sameKey compares two keys in full, by their wire bytes (parent 13.8).
func sameKey(a, b ssh.PublicKey) bool {
	return a != nil && b != nil && bytes.Equal(a.Marshal(), b.Marshal())
}
