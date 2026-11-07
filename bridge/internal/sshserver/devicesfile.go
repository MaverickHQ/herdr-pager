package sshserver

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"sync"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
)

// A DevicesFile is devices.toml as serve sees it (design D2-12): each call to
// Devices stats the file and re-reads it only when it changed, as after
// `devices revoke` or `provision` renamed a new one over it.
type DevicesFile struct {
	Path string
	Log  *slog.Logger

	mu     sync.Mutex
	info   fs.FileInfo // the version last read, nil for none
	devs   []devices.Device
	broken string // why the file won't load (or stat), "" while it loads or is missing
}

// Devices returns the paired devices. A missing file is none, and so is a
// file that won't load or stat: devices.toml fails closed, so a hand edit
// that breaks it can't keep a revoked device paired (the owner's decision,
// 2026-10-27, open-items O-51). Each failure is logged once, until it
// changes; serve refuses to start with a broken file, so only a hand edit
// gets here.
func (f *DevicesFile) Devices() []devices.Device {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, err := os.Stat(f.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		f.info, f.devs, f.broken = nil, nil, ""
		return nil
	case err != nil:
		if msg := err.Error(); msg != f.broken {
			f.Log.Error("sshserver: can't stat devices.toml; no device can connect until it loads", "err", err)
			f.broken = msg
		}
		f.info, f.devs = nil, nil
		return nil
	case f.info != nil && os.SameFile(info, f.info) && info.ModTime().Equal(f.info.ModTime()) && info.Size() == f.info.Size():
		return f.devs
	}
	f.info = info
	devs, err := devices.Load(f.Path)
	if err != nil {
		f.Log.Error("sshserver: devices.toml won't load; no device can connect until it does", "err", err)
		f.devs, f.broken = nil, err.Error()
		return nil
	}
	f.devs, f.broken = devs, ""
	return devs
}

// Broken is why devices.toml wouldn't load at the last call to Devices, or
// "" if it loaded or was missing.
func (f *DevicesFile) Broken() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.broken
}
