package provision

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestNearMisses checks the acceptance day's finding (2026-10-25): an iPhone
// names its hotspot like Sam’s iPhone, with a curly apostrophe, and a name
// typed with a straight one never matches. A --wifi-ssid the Mac hasn't
// saved, though it has saved one that differs only in its apostrophes,
// quotes, dashes, case or spaces, is a near miss; an exact match, or a
// network the Mac doesn't know at all, isn't.
func TestNearMisses(t *testing.T) {
	known := []string{"HomeNet24", "Sam’s iPhone", "Café – Upstairs", "office  5G"}
	for _, c := range []struct {
		name  string
		ssids []string
		want  []NearMiss
	}{
		{"a straight apostrophe", []string{"HomeNet24", "Sam's iPhone"}, []NearMiss{{"Sam's iPhone", "Sam’s iPhone"}}},
		{"case and spaces", []string{"homenet24 ", "Office 5g"}, []NearMiss{{"homenet24 ", "HomeNet24"}, {"Office 5g", "office  5G"}}},
		{"a dash", []string{"Café - Upstairs"}, []NearMiss{{"Café - Upstairs", "Café – Upstairs"}}},
		{"exact names", []string{"HomeNet24", "Sam’s iPhone"}, nil},
		{"a network the Mac doesn't know", []string{"Hotel Guest"}, nil},
	} {
		if got := NearMisses(c.ssids, known); !slices.Equal(got, c.want) {
			t.Errorf("%s: %q; want %q", c.name, got, c.want)
		}
	}
	if got := NearMisses([]string{"Sam's iPhone"}, nil); got != nil {
		t.Errorf("with nothing known: %q; want none", got)
	}
}

// TestKnownNetworks checks the lookup: `networksetup
// -listpreferredwirelessnetworks en0` (a header, then one tab-indented name
// a line), else en1 when en0 isn't the Wi-Fi interface, each a fixed
// argument vector; nothing when neither answers.
func TestKnownNetworks(t *testing.T) {
	en0 := []string{"/usr/sbin/networksetup", "-listpreferredwirelessnetworks", "en0"}
	en1 := []string{"/usr/sbin/networksetup", "-listpreferredwirelessnetworks", "en1"}
	list := func(dev string) string {
		return "Preferred networks on " + dev + ":\n\tHomeNet24\n\tSam’s iPhone\n"
	}
	notWiFi := func(dev string) string {
		return dev + " is not a Wi-Fi interface.\n** Error: Error obtaining wireless information.\n"
	}
	failed := errors.New("exit status 10")
	for _, c := range []struct {
		name    string
		answers map[string]string // by device; absent: the command fails
		want    []string
		ran     [][]string
	}{
		{"en0 is Wi-Fi", map[string]string{"en0": list("en0")}, []string{"HomeNet24", "Sam’s iPhone"}, [][]string{en0}},
		{"en1 is Wi-Fi", map[string]string{"en0": notWiFi("en0"), "en1": list("en1")}, []string{"HomeNet24", "Sam’s iPhone"}, [][]string{en0, en1}},
		{"en0 fails", map[string]string{"en1": list("en1")}, []string{"HomeNet24", "Sam’s iPhone"}, [][]string{en0, en1}},
		{"neither", map[string]string{"en0": notWiFi("en0"), "en1": notWiFi("en1")}, nil, [][]string{en0, en1}},
		{"none saved", map[string]string{"en0": "Preferred networks on en0:\n"}, nil, [][]string{en0}},
	} {
		var ran [][]string
		got := knownNetworks(func(argv ...string) ([]byte, error) {
			ran = append(ran, argv)
			out, ok := c.answers[argv[len(argv)-1]]
			if !ok {
				return nil, failed
			}
			return []byte(out), nil
		})
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %q; want %q", c.name, got, c.want)
		}
		if !slices.EqualFunc(ran, c.ran, slices.Equal) {
			t.Errorf("%s: ran %q; want exactly %q", c.name, ran, c.ran)
		}
	}
}

// TestProvisionWarnsOnNearMiss: provision says so before it opens the port,
// naming the Mac's spelling, so the owner can stop before choosing Pair again
// on the pager; with the exact names it says nothing.
func TestProvisionWarnsOnNearMiss(t *testing.T) {
	hostKey, path := bridge(t)
	for _, c := range []struct {
		name string
		ssid string
		warn bool
	}{
		{"a near miss", "Sam's iPhone", true},
		{"the exact name", "Sam’s iPhone", false},
	} {
		var out bytes.Buffer
		o := options(t, "/dev/no-such-port", hostKey, path, &out)
		o.Networks = []Network{{SSID: "HomeNet24", Pass: "home-pass"}, {SSID: c.ssid, Pass: "hotspot-pass"}}
		o.KnownNetworks = func() []string { return []string{"HomeNet24", "Sam’s iPhone"} }
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := Run(ctx, o)
		cancel()
		if err == nil {
			t.Fatalf("%s: Run opened /dev/no-such-port", c.name)
		}
		warned := strings.Contains(out.String(), `"Sam's iPhone"`) && strings.Contains(out.String(), `--wifi-ssid "Sam’s iPhone"`)
		if warned != c.warn {
			t.Errorf("%s: the output\n%s\nwarns: %v; want %v", c.name, out.String(), warned, c.warn)
		}
		if strings.Contains(out.String(), "hotspot-pass") {
			t.Errorf("%s: the output shows a password", c.name)
		}
	}
}
