package provision

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
)

// TestProvisionPassesThePagersLog checks Options.Log (phase 5 requirement
// 2.2, design §The setup page, Live output): it gets each of the pager's own
// serial lines while the port is open, without its line ending, and never a
// provisioning line, whole or damaged. A line holding a password the bridge
// sent, as a console that echoes its input would send back, is hidden.
func TestProvisionPassesThePagersLog(t *testing.T) {
	peer, tty := openPeer(t)
	long := "I (9) long: " + strings.Repeat("x", 5000)
	backlog := bootLog +
		`@P {"t":"prov_hel` + "\r\n" + // a provisioning line cut short
		long + "\r\n" +
		"\x1b[0;32mI (31) pager: in colour\x1b[0m\r\n"
	device{hello: "to-tool/valid/prov_hello.txt", result: "to-tool/valid/prov_result-ok.txt", backlog: backlog, echo: "> "}.play(t, peer)
	hostKey, path := bridge(t)
	o := options(t, tty, hostKey, path, &bytes.Buffer{})
	var mu sync.Mutex
	var got []string
	o.Log = func(line string) {
		mu.Lock()
		got = append(got, line)
		mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Run(ctx, o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if devs, err := devices.Load(path); err != nil || len(devs) != 1 {
		t.Fatalf("devices.toml holds %d devices (%v); want pager-01", len(devs), err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{
		"ESP-ROM:esp32s3-20210327",
		"rst:0x1 (POWERON),boot:0x8 (SPI_FAST_FLASH_BOOT)",
		"I (27) boot: ESP-IDF v5.5.5 2nd stage bootloader",
		"\x1b[0;32mI (31) pager: in colour\x1b[0m",
		"I (1234) pager: a log line, which the tool ignores",
		"(a line holding a Wi-Fi password was hidden)",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("Log didn't get %q", want)
		}
	}
	if i := slices.IndexFunc(got, func(l string) bool { return strings.HasPrefix(l, "I (9) long: ") }); i < 0 || len(got[i]) != maxRead || got[i] != long[:maxRead] {
		t.Errorf("Log didn't get the long line cut to its first %d bytes", maxRead)
	}
	for _, l := range got {
		switch {
		case strings.HasPrefix(l, Prefix) || strings.HasPrefix(l, "> "+Prefix):
			t.Errorf("Log got a provisioning line: %.80q", l)
		case strings.ContainsAny(l, "\r\n"):
			t.Errorf("Log got a line with its line ending: %q", l)
		case strings.Contains(l, "correct-horse-battery"):
			t.Errorf("Log got a line holding the password: %q", l)
		}
	}
}
