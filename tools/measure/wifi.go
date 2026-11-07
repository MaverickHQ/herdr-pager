package measure

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A Network is a Wi-Fi network from test-wifi.toml. Neither field is ever
// printed: the harness masks both in everything it logs.
type Network struct{ SSID, Pass string }

// wifiPath is PAGER_TEST_WIFI, or ~/.config/herdr-pager/test-wifi.toml (the
// kit's boardtest reads the same file).
func wifiPath() string {
	if p := os.Getenv("PAGER_TEST_WIFI"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr-pager", "test-wifi.toml")
}

// Wifi reads test-wifi.toml's [[network]] tables, in order, refusing the file
// unless it's a regular file of mode 0600. Its errors never quote the file.
func Wifi(t testing.TB) []Network {
	t.Helper()
	nets, err := readWifi(wifiPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) == 0 {
		t.Fatalf("%s has no [[network]] with an ssid", wifiPath())
	}
	return nets
}

// readWifiIfAny is Wifi for masking: nothing if the file can't be read.
func readWifiIfAny() []Network {
	nets, _ := readWifi(wifiPath())
	return nets
}

func readWifi(path string) ([]Network, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("the Wi-Fi file: %w", err)
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
		return nil, errors.New("the Wi-Fi file " + path + " must be a regular file of mode 0600")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("the Wi-Fi file: %w", err)
	}
	var nets []Network
	in := false
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case line == "[[network]]":
			nets, in = append(nets, Network{}), true
		case strings.HasPrefix(line, "["):
			in = false
		case in:
			key, val, ok := strings.Cut(line, "=")
			if !ok {
				return nil, fmt.Errorf("the Wi-Fi file, line %d: not key = value", i+1)
			}
			s, err := tomlString(strings.TrimSpace(val))
			if err != nil {
				return nil, fmt.Errorf("the Wi-Fi file, line %d: %v", i+1, err)
			}
			switch strings.TrimSpace(key) {
			case "ssid":
				nets[len(nets)-1].SSID = s
			case "password":
				nets[len(nets)-1].Pass = s
			}
		}
	}
	var out []Network
	for _, n := range nets {
		if n.SSID != "" {
			out = append(out, n)
		}
	}
	return out, nil
}

// tomlString decodes a basic ("...") or literal ('...') TOML string.
func tomlString(v string) (string, error) {
	switch {
	case len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'':
		return v[1 : len(v)-1], nil
	case len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"':
		s, err := strconv.Unquote(v)
		if err != nil {
			return "", errors.New("a string the reader can't decode")
		}
		return s, nil
	}
	return "", errors.New("not a quoted string")
}
