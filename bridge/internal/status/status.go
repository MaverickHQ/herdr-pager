// Package status is status.json, what `serve` tells the CLI (design D2-12):
// serve writes it atomically, with mode 0600, every second and on change;
// `status` and `devices list` read it. A missing file, or one older than 5 s,
// means no bridge is running. There's no control socket.
package status

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// File is status.json's name in the bridge's directory.
const File = "status.json"

// MaxAge is how old status.json may be and still mean a bridge is running.
const MaxAge = 5 * time.Second

// Status is what a running bridge reports (parent 11.2, 12.1).
type Status struct {
	Written   time.Time `json:"written"`   // when serve wrote it
	Listening string    `json:"listening"` // the address serve listens on
	Herdr     string    `json:"herdr"`     // herdr's state: "ok" or "offline"
	Socket    string    `json:"socket"`    // herdr's socket path
	Devices   []string  `json:"devices"`   // the paired devices' names
	Connected []string  `json:"connected"` // the names with a session open
	// DevicesError is why devices.toml won't load, while it won't: then no
	// device is paired (open-items O-51).
	DevicesError string `json:"devices_error,omitempty"`
}

// Write replaces dir's status.json with s, written at now: a temporary file
// in dir (0600), renamed over the old one, so a reader sees one whole file.
func Write(dir string, s Status, now time.Time) error {
	s.Written = now.Round(0).UTC()
	if s.Devices == nil {
		s.Devices = []string{}
	}
	if s.Connected == nil {
		s.Connected = []string{}
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".status-*.json") // created 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, File))
}

// Read returns dir's status.json, and whether a bridge is running at now:
// the file exists and was written at most MaxAge before now (or after it, by
// as little). When none is running, s is empty, so nothing shows connected.
func Read(dir string, now time.Time) (s Status, running bool, err error) {
	data, err := os.ReadFile(filepath.Join(dir, File))
	if errors.Is(err, fs.ErrNotExist) {
		return Status{}, false, nil
	}
	if err != nil {
		return Status{}, false, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Status{}, false, fmt.Errorf("%s: %w", filepath.Join(dir, File), err)
	}
	if age := now.Sub(s.Written); age > MaxAge || age < -MaxAge {
		return Status{}, false, nil
	}
	return s, true, nil
}
