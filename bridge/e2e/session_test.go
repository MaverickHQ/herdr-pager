package e2e

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devsim"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
)

// closed says whether the bridge ends d's session within 5 s, reading past
// any lines it sends first.
func closed(d *devsim.Device) bool {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if _, err := d.Recv(time.Until(deadline)); err != nil {
			return errors.Is(err, io.EOF)
		}
	}
	return false
}

// open says whether d's session is still open: nothing comes, and it isn't
// closed, within 100 ms.
func open(d *devsim.Device) bool {
	_, err := d.Recv(100 * time.Millisecond)
	return errors.Is(err, devsim.ErrTimeout)
}

// TestHelloAndIdleTimeouts checks parent design §SSH server, on the fake
// clock: a session with no hello within 5 s is closed (open at 4.9 s); after
// the hello, 180 s with no message from the device closes it, and each
// message starts the 180 s again.
func TestHelloAndIdleTimeouts(t *testing.T) {
	b := startBridge(t, nil)

	mute := b.dial(b.pair("pager-01"))
	if !b.clock.waitTimer(b.clock.Now().Add(5 * time.Second)) {
		t.Fatal("the session set no 5 s timer for the hello")
	}
	b.clock.Advance(4900 * time.Millisecond)
	if !open(mute) {
		t.Error("closed before 5 s without a hello")
	}
	b.clock.Advance(100 * time.Millisecond)
	if !closed(mute) {
		t.Error("still open 5 s without a hello")
	}

	quiet := b.connect("pager-02")
	recv[*protocol.Snap](t, quiet)
	idleAt := func(from time.Time) {
		t.Helper()
		if !b.clock.waitTimer(from.Add(180 * time.Second)) {
			t.Fatal("the session set no 180 s idle timer")
		}
	}
	idleAt(b.clock.Now())
	b.clock.Advance(100 * time.Second)
	quiet.Send(`{"t":"ping","id":1}`)
	recv[*protocol.Pong](t, quiet)
	idleAt(b.clock.Now())
	b.clock.Advance(179900 * time.Millisecond)
	if !open(quiet) {
		t.Error("closed 179.9 s after its last message")
	}
	b.clock.Advance(100 * time.Millisecond)
	if !closed(quiet) {
		t.Error("still open 180 s after its last message")
	}
}

// TestRateLimit checks parent 13.6: a session's messages beyond 10 a second
// are dropped, never acted on, answered `rate_limited` and logged; a second
// later ten more are taken again.
func TestRateLimit(t *testing.T) {
	b := startBridge(t, nil)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)

	ping := func(from, to int) {
		for id := from; id <= to; id++ {
			if err := dev.Send(fmt.Sprintf(`{"t":"ping","id":%d}`, id)); err != nil {
				t.Fatal(err)
			}
		}
	}
	ping(1, 15)
	for id := uint32(1); id <= 10; id++ {
		if pong := recv[*protocol.Pong](t, dev); pong.ID != id {
			t.Errorf("pong %d; want %d", pong.ID, id)
		}
	}
	for id := uint32(11); id <= 15; id++ {
		if r := recv[*protocol.Result](t, dev); r.ID != id || r.OK || r.Err != "rate_limited" {
			t.Errorf("message %d over the limit: %+v; want rate_limited", id, r)
		}
	}
	if !strings.Contains(b.log.String(), "rate limit") {
		t.Errorf("the dropped messages aren't logged:\n%s", b.log)
	}

	b.clock.Advance(time.Second)
	ping(16, 25)
	for id := uint32(16); id <= 25; id++ {
		if pong := recv[*protocol.Pong](t, dev); pong.ID != id {
			t.Errorf("a second later: pong %d; want %d", pong.ID, id)
		}
	}
}

// TestOversizeAndInvalidClose checks parent 13.5 and 13.1: a line of 512
// bytes is answered, one of 513 (the newline not counted) closes the
// session; a message of an unknown type gets no reply and is logged by its
// type only, never its content; and five invalid messages close the session,
// four don't.
func TestOversizeAndInvalidClose(t *testing.T) {
	b := startBridge(t, nil)

	long := b.connect("pager-01")
	recv[*protocol.Snap](t, long)
	long.Send(fixture(t, "to-bridge/valid/ping-512-bytes.ndjson"))
	recv[*protocol.Pong](t, long)
	long.Send(fixture(t, "to-bridge/invalid/ping-513-bytes.ndjson"))
	if !closed(long) {
		t.Error("a 513-byte line didn't close the session")
	}

	bad := b.connect("pager-02")
	recv[*protocol.Snap](t, bad)
	invalid := []string{
		fixture(t, "to-bridge/invalid/unknown-type.ndjson"),
		fixture(t, "to-bridge/invalid/malformed.ndjson"),
		fixture(t, "to-bridge/invalid/no-type.ndjson"),
		fixture(t, "to-bridge/invalid/bridge-type.ndjson"),
		fixture(t, "to-bridge/invalid/show-short-pane.ndjson"),
	}
	for _, line := range invalid[:4] {
		bad.Send(line)
	}
	bad.Send(`{"t":"ping","id":5}`)
	if pong := recv[*protocol.Pong](t, bad); pong.ID != 5 {
		t.Errorf("after four invalid messages, a ping got pong %d; want 5 and no other reply", pong.ID)
	}
	bad.Send(invalid[4])
	if !closed(bad) {
		t.Error("five invalid messages didn't close the session")
	}

	log := b.log.String()
	if !strings.Contains(log, "t=send_text") {
		t.Errorf("the unknown type isn't logged by its type:\n%s", log)
	}
	for _, content := range []string{"yes", `"text"`, "w1:p3", `"p3"`} {
		if strings.Contains(log, content) {
			t.Errorf("the log carries %s, from an invalid message:\n%s", content, log)
		}
	}
}
