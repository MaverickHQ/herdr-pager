package setup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// appendTo appends text to the file at path, making it if it isn't there.
func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// TestSetupTailsTheBridgeLog is the page's bridge channel (requirement 2.2,
// design §The setup page, Live output): each line appended to the bridge's
// log file, as brew services or the launchd plist writes it, reaches every
// tab as text, from the moment the page starts. A file that isn't there yet
// is read from its start once it is; one that's emptied or rotated, from
// its new start. A debug line, which may hold card text, never does
// (requirement 2.7).
func TestSetupTailsTheBridgeLog(t *testing.T) {
	dir := t.TempDir()
	brewLog, launchdLog := filepath.Join(dir, "herdr-pager.log"), filepath.Join(dir, "bridge.log")
	appendTo(t, brewLog, "time=2026-11-03T08:00:00.000Z level=INFO msg=\"before the page\"\n")

	clk := newFakeClock()
	h, err := NewHandler(Options{Clock: clk, BridgeLogs: []string{brewLog, launchdLog}})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	s := &served{t: t, h: h, clock: clk, addr: ln.Addr().String()}
	tab := tapEvents(t, s.openTab())

	// poll lets the page read its files once more.
	poll := func() {
		t.Helper()
		if !clk.waitTimer(clk.Now().Add(TailEvery)) {
			t.Fatalf("no %v timer for reading the log", TailEvery)
		}
		clk.Advance(TailEvery)
	}
	bridgeSays := func(text string) {
		t.Helper()
		if _, got := tab.wait(t, chanBridge, text); got != text {
			t.Errorf("the bridge channel shows %q; want %q", got, text)
		}
	}

	info := "time=2026-11-03T08:00:01.000Z level=INFO msg=\"session up\" device=pager-01"
	appendTo(t, brewLog, info+"\n"+
		"time=2026-11-03T08:00:02.000Z level=DEBUG msg=\"card\" text=\"rm -rf build\"\n"+
		"\x1b[31mpanic: in red\x1b[0m\n"+
		"time=2026-11-03T08:00:03.000Z level=WARN msg=\"half a line")
	poll()
	bridgeSays(info)
	bridgeSays("panic: in red")

	appendTo(t, brewLog, ", then the rest\"\n")
	poll()
	bridgeSays("time=2026-11-03T08:00:03.000Z level=WARN msg=\"half a line, then the rest\"")

	appendTo(t, launchdLog, "time=2026-11-03T08:00:04.000Z level=INFO msg=\"from launchd's file\"\n")
	poll()
	bridgeSays("time=2026-11-03T08:00:04.000Z level=INFO msg=\"from launchd's file\"")

	if err := os.WriteFile(brewLog, []byte("time=2026-11-03T08:00:05.000Z level=INFO msg=rotated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	poll()
	bridgeSays("time=2026-11-03T08:00:05.000Z level=INFO msg=rotated")

	// A flood between two reads, more than the page reads at once: it
	// shows the newest lines, each whole.
	var flood strings.Builder
	for i := range 2000 {
		fmt.Fprintf(&flood, "time=2026-11-03T08:00:06.000Z level=INFO msg=\"flood %04d\"\n", i)
	}
	appendTo(t, brewLog, flood.String()+"the newest\n")
	poll()
	bridgeSays("the newest")

	time.Sleep(50 * time.Millisecond) // anything more would have come by now
	tab.mu.Lock()
	defer tab.mu.Unlock()
	for _, ev := range tab.events {
		if ev.channel == chanBridge && !strings.HasPrefix(ev.text, "time=") && ev.text != "panic: in red" && ev.text != "the newest" {
			t.Errorf("the bridge channel shows %q, not a whole line", ev.text)
		}
		if ev.channel == chanBridge && (strings.Contains(ev.text, "before the page") || strings.Contains(ev.text, "flood 0000") ||
			strings.Contains(ev.text, "DEBUG") || strings.Contains(ev.text, "rm -rf")) {
			t.Errorf("the bridge channel shows %q", ev.text)
		}
	}
}
