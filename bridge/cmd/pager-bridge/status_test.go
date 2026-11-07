package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devsim"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdrtest"
)

// statusOut is `status --json` as the test reads it, field by field.
type statusOut struct {
	Running   bool     `json:"running"`
	Listening string   `json:"listening"`
	Herdr     string   `json:"herdr"`
	Socket    string   `json:"socket"`
	Devices   []string `json:"devices"`
	Connected []string `json:"connected"`
}

// statusNow runs `status --json` and `status`, and returns what each printed.
func statusNow(t *testing.T) (statusOut, string) {
	t.Helper()
	out, stderr, code := pagerBridge(t, "status", "--json")
	if code != 0 {
		t.Fatalf("status --json: exit %d, stderr %q", code, stderr)
	}
	var s statusOut
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		t.Fatalf("status --json printed %q: %v", out, err)
	}
	human, stderr, code := pagerBridge(t, "status")
	if code != 0 {
		t.Fatalf("status: exit %d, stderr %q", code, stderr)
	}
	return s, human
}

// statusWithin runs status until ok says its JSON is as wanted, for up to 3 s,
// and returns what it last printed.
func statusWithin(t *testing.T, ok func(statusOut) bool) (statusOut, string) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		s, human := statusNow(t)
		if ok(s) || time.Now().After(deadline) {
			return s, human
		}
	}
}

// statusLine returns the line of out that starts with prefix, after its
// indent.
func statusLine(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

// TestStatusReportsState checks parent 11.2 and design D2-12 (S2-5): status
// reads status.json, which serve keeps (mode 0600) and removes when it stops,
// and prints whether a bridge is listening and where, herdr's socket and its
// state, the paired devices and which are connected; --json prints the same
// as JSON. With no bridge running it says so, with devices.toml's devices,
// none connected, and herdr's state unknown.
func TestStatusReportsState(t *testing.T) {
	dir, socket := serveHome(t)
	fake, err := herdrtest.Start(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fake.Close() })
	host, keys := pairServe(t, dir, "pager-01", "pager-02")
	paired := []string{"pager-01", "pager-02"}

	stopped := statusOut{Herdr: "unknown", Socket: socket, Devices: paired, Connected: []string{}}
	s, human := statusNow(t)
	if !reflect.DeepEqual(s, stopped) {
		t.Errorf("before serve: status --json %+v; want %+v", s, stopped)
	}
	if !strings.Contains(statusLine(human, "Bridge:"), "not running") || !strings.Contains(statusLine(human, "pager-02"), "not connected") {
		t.Errorf("before serve: status printed\n%s\nwant the bridge not running, and pager-02 not connected", human)
	}

	p := startServe(t)
	dev, err := devsim.Dial(p.addr, keys[0], host)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	dev.Send(`{"t":"hello","v":1,"fw":"0.1.0","name":"pager-01"}`)
	if line, err := dev.Recv(5 * time.Second); err != nil || !strings.HasPrefix(line, `{"t":"hello"`) {
		t.Fatalf("the device got %q, %v; want the bridge's hello", line, err)
	}
	running := statusOut{Running: true, Listening: p.addr, Herdr: "ok", Socket: socket, Devices: paired, Connected: []string{"pager-01"}}
	s, human = statusWithin(t, func(s statusOut) bool { return reflect.DeepEqual(s, running) })
	if !reflect.DeepEqual(s, running) {
		t.Errorf("pager-01 connected: status --json %+v; want %+v", s, running)
	}
	t.Logf("pager-01 connected, status prints:\n%s", human)
	for prefix, want := range map[string][]string{
		"Bridge:":  {"listening on " + p.addr},
		"herdr:":   {"ok", socket},
		"Devices:": {"2 paired", "1 connected"},
		"pager-01": {"connected"},
		"pager-02": {"not connected"},
	} {
		got := statusLine(human, prefix)
		for _, w := range want {
			if !strings.Contains(got, w) || (prefix == "pager-01" && strings.Contains(got, "not")) {
				t.Errorf("pager-01 connected: status's %q line is %q; want %q in it\n%s", prefix, got, w, human)
			}
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, "status.json")); err != nil || fi.Mode() != 0o600 {
		t.Errorf("status.json: %v, %v; want mode 0600", fi, err)
	}

	fake.Close()
	if s, _ = statusWithin(t, func(s statusOut) bool { return s.Herdr == "offline" }); s.Herdr != "offline" {
		t.Errorf("herdr gone: status --json %+v; want herdr offline", s)
	}

	p.cmd.Process.Signal(syscall.SIGTERM)
	<-p.done
	s, human = statusNow(t)
	if !reflect.DeepEqual(s, stopped) || !strings.Contains(statusLine(human, "Bridge:"), "not running") {
		t.Errorf("serve stopped: status --json %+v, status\n%s\nwant %+v, and not running", s, human, stopped)
	}
}
