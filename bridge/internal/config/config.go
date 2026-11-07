// Package config is the bridge's directory and its config.toml (parent design
// §Data models). Its files: the host key, config.toml, devices.toml,
// keymaps.toml (optional overrides), risk.toml (optional) and status.json.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// File is config.toml's name in the bridge's directory.
const File = "config.toml"

// Config is config.toml with its defaults applied. There is no poll interval:
// the bridge polls herdr every second (phase 2 R5.3).
type Config struct {
	Listen      string        // listen: host:port
	HerdrSocket string        // herdr_socket: an absolute path
	IdleTimeout time.Duration // idle_timeout: a Go duration, above 0
	LogLevel    string        // log_level: info or debug
}

// The defaults (parent design §Data models). herdr's socket defaults to
// $HERDR_SOCKET_PATH, else ~/.config/herdr/herdr.sock.
const (
	DefaultListen      = "0.0.0.0:2277"
	DefaultIdleTimeout = 180 * time.Second
	DefaultLogLevel    = "info"
)

var logLevels = []string{"info", "debug"}

// Env is what the defaults depend on, passed in so that tests never read the
// real HOME or environment.
type Env struct {
	Home   string
	Getenv func(string) string
}

// A LineError is a config.toml that won't load, at the line to fix: serve
// exits non-zero naming the file, the setting and why (parent design §Error
// Handling).
type LineError struct {
	Path   string
	Line   int // from 1
	Reason string
}

func (e *LineError) Error() string { return fmt.Sprintf("%s:%d: %s", e.Path, e.Line, e.Reason) }

// Dir is the bridge's directory under home, ~/.config/herdr-pager (mode 0700,
// parent 13.9).
func Dir(home string) string { return filepath.Join(home, ".config", "herdr-pager") }

// Load reads config.toml at path and applies the defaults. A missing file is
// an error wrapping fs.ErrNotExist: `init` writes it.
func Load(path string, env Env) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var f struct {
		Listen      string `toml:"listen"`
		HerdrSocket string `toml:"herdr_socket"`
		IdleTimeout string `toml:"idle_timeout"`
		LogLevel    string `toml:"log_level"`
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		var de *toml.DecodeError
		var se *toml.StrictMissingError
		switch {
		case errors.As(err, &de):
			row, _ := de.Position()
			return Config{}, &LineError{path, row, de.Error()}
		case errors.As(err, &se) && len(se.Errors) > 0:
			row, _ := se.Errors[0].Position()
			return Config{}, &LineError{path, row, "unknown setting " + strings.Join(se.Errors[0].Key(), ".")}
		}
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	fail := func(key, reason string) (Config, error) {
		return Config{}, &LineError{path, line(data, key), key + ": " + reason}
	}

	c := Config{Listen: DefaultListen, IdleTimeout: DefaultIdleTimeout, LogLevel: DefaultLogLevel}
	if f.Listen != "" {
		if _, _, err := net.SplitHostPort(f.Listen); err != nil {
			return fail("listen", "not host:port")
		}
		c.Listen = f.Listen
	}
	switch {
	case f.HerdrSocket != "":
		c.HerdrSocket = f.HerdrSocket
		if rest, ok := strings.CutPrefix(f.HerdrSocket, "~/"); ok {
			c.HerdrSocket = filepath.Join(env.Home, rest)
		}
		if !filepath.IsAbs(c.HerdrSocket) {
			return fail("herdr_socket", "not an absolute path")
		}
	case env.Getenv("HERDR_SOCKET_PATH") != "":
		c.HerdrSocket = env.Getenv("HERDR_SOCKET_PATH")
	default:
		c.HerdrSocket = filepath.Join(env.Home, ".config", "herdr", "herdr.sock")
	}
	if f.IdleTimeout != "" {
		d, err := time.ParseDuration(f.IdleTimeout)
		if err != nil || d <= 0 {
			return fail("idle_timeout", `not a duration above 0, such as "180s"`)
		}
		c.IdleTimeout = d
	}
	if f.LogLevel != "" {
		if !slices.Contains(logLevels, f.LogLevel) {
			return fail("log_level", "not info or debug")
		}
		c.LogLevel = f.LogLevel
	}
	return c, nil
}

// line finds the line that sets key, or 1.
func line(data []byte, key string) int {
	for ln, l := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, key) && strings.HasPrefix(strings.TrimSpace(t[len(key):]), "=") {
			return ln + 1
		}
	}
	return 1
}
