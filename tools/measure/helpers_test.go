package measure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The harness's own logic, which needs no board: these run in a plain go test.

func TestP95NearestRank(t *testing.T) {
	// Of 20, the nearest rank is the 19th: the second largest, whatever the order.
	xs := []float64{20, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 19, 18}
	if got := p95(xs); got != 19 {
		t.Errorf("p95 of 1..20 = %v, want 19", got)
	}
	if xs[0] != 20 {
		t.Error("p95 sorted its argument")
	}
	if got := p95([]float64{7}); got != 7 {
		t.Errorf("p95 of one = %v, want 7", got)
	}
}

func TestReadWifi(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-wifi.toml")
	data := `# a comment
[[network]]
ssid = "Home-2G"
password = 'pa"ss'

[other]
ssid = "not a network"

[[network]]
ssid = "Phone \"hotspot\""
password = ""

[[network]]
password = "no ssid: left out"
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	nets, err := readWifi(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Network{{"Home-2G", `pa"ss`}, {`Phone "hotspot"`, ""}}
	if len(nets) != len(want) || nets[0] != want[0] || nets[1] != want[1] {
		t.Errorf("readWifi = %q, want %q", nets, want)
	}

	// Anything but a regular file of mode 0600 is refused, and the error doesn't quote the file.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readWifi(path); err == nil || strings.Contains(err.Error(), "Home-2G") {
		t.Errorf("mode 0644: err %v, want a refusal that doesn't quote the file", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.toml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readWifi(link); err == nil {
		t.Error("a symlink was read")
	}

	// A value that isn't a quoted string is an error naming the line, not the value.
	if err := os.WriteFile(path, []byte("[[network]]\nssid = Secret-Net\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readWifi(path); err == nil || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), "Secret-Net") {
		t.Errorf("unquoted value: err %v, want one naming line 2 without the value", err)
	}
}

func TestMask(t *testing.T) {
	nets := []Network{{"Home", "Home-password"}, {"ab", "xy"}}
	// The longer secret goes first, so no piece of it is left; secrets under 3 bytes aren't masked.
	if got, want := mask("joined Home with Home-password; ab xy", nets), "joined [network] with [network]; ab xy"; got != want {
		t.Errorf("mask = %q, want %q", got, want)
	}
	s := &Serial{secret: []string{"Home-password", "Home"}}
	if got := s.Mask("Home-password"); got != "[network]" {
		t.Errorf("Serial.Mask = %q, want [network]", got)
	}
}

func TestFlipsScenario(t *testing.T) {
	got := flips("AGENT", 2, 15*time.Second, 12*time.Second)
	want := "AGENT" +
		"\n[[change]]\nat = \"15s\"\npane = \"w1:p1\"\nstatus = \"blocked\"\n" +
		"\n[[change]]\nat = \"21s\"\npane = \"w1:p1\"\nstatus = \"working\"\n" +
		"\n[[change]]\nat = \"27s\"\npane = \"w1:p1\"\nstatus = \"blocked\"\n" +
		"\n[[change]]\nat = \"33s\"\npane = \"w1:p1\"\nstatus = \"working\"\n"
	if got != want {
		t.Errorf("flips =\n%s\nwant\n%s", got, want)
	}
}
