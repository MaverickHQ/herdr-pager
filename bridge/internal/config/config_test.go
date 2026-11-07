package config

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// configDir holds the bridge's file fixtures (testdata/config/README.md).
const configDir = "../../testdata/config"

// TestConfigLoadAppliesDefaults checks config.toml's defaults (parent design
// §Data models): an empty file gives listen 0.0.0.0:2277 and herdr's socket
// from $HERDR_SOCKET_PATH, else ~/.config/herdr/herdr.sock; a set value
// overrides its default; and there's no poll interval to set.
func TestConfigLoadAppliesDefaults(t *testing.T) {
	home := t.TempDir()
	noEnv := Env{Home: home, Getenv: func(string) string { return "" }}
	withEnv := Env{Home: home, Getenv: func(k string) string {
		if k == "HERDR_SOCKET_PATH" {
			return "/tmp/hp-env/herdr.sock"
		}
		return ""
	}}
	defaults := Config{
		Listen:      "0.0.0.0:2277",
		HerdrSocket: filepath.Join(home, ".config", "herdr", "herdr.sock"),
		IdleTimeout: 180 * time.Second,
		LogLevel:    "info",
	}

	empty := filepath.Join(configDir, "config-empty.toml")
	if c, err := Load(empty, noEnv); err != nil || c != defaults {
		t.Errorf("an empty file: %+v, %v; want %+v", c, err, defaults)
	}
	fromEnv := defaults
	fromEnv.HerdrSocket = "/tmp/hp-env/herdr.sock"
	if c, err := Load(empty, withEnv); err != nil || c != fromEnv {
		t.Errorf("an empty file with $HERDR_SOCKET_PATH: %+v, %v; want %+v", c, err, fromEnv)
	}
	set := Config{Listen: "127.0.0.1:2299", HerdrSocket: "/tmp/hp-test/herdr.sock", IdleTimeout: 90 * time.Second, LogLevel: "debug"}
	if c, err := Load(filepath.Join(configDir, File), withEnv); err != nil || c != set {
		t.Errorf("every value set: %+v, %v; want %+v (the file over $HERDR_SOCKET_PATH)", c, err, set)
	}

	if _, err := Load(filepath.Join(home, File), noEnv); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file: %v; want fs.ErrNotExist (serve tells the owner to run init)", err)
	}

	for _, c := range []struct {
		file string
		line int
	}{
		{"poll-interval.toml", 2}, // there's no poll_interval setting
		{"bad-listen.toml", 2},
		{"bad-idle-timeout.toml", 3},
		{"relative-socket.toml", 1},
		{"bad-log-level.toml", 2},
	} {
		path := filepath.Join(configDir, "config-invalid", c.file)
		_, err := Load(path, noEnv)
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
