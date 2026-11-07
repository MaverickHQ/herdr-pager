package e2e

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdrtest"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/watch"
)

// TestHerdrOfflineThenRecovers checks parent 4.4 and phase 2 R5.4: a missing
// socket, a refused connection, no reply within 2 s, an agent.list that
// won't decode, and herdr's own error each send devices one offline
// snapshot and are logged with their code; once herdr answers again, a
// normal snapshot follows within 2 s.
func TestHerdrOfflineThenRecovers(t *testing.T) {
	agents := []herdr.AgentInfo{{TerminalID: "term_a", PaneID: "w1:p1", Agent: str("claude"), Status: "blocked", StateChangeSeq: 3}}
	b := startBridge(t, agents)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)
	socket := b.herdr.Socket
	restart := func() {
		fake, err := herdrtest.Start(socket)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { fake.Close() })
		fake.SetAgents(agents)
		b.herdr = fake
	}

	for _, c := range []struct {
		why, code       string
		cause, recovers func()
	}{
		{"the socket is missing", "socket_missing", func() { b.herdr.Close() }, restart},
		{"the socket refuses", "socket_refused", func() {
			b.herdr.Close()
			l, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			l.(*net.UnixListener).SetUnlinkOnClose(false)
			l.Close() // the file stays, with nothing listening
		}, func() { os.Remove(socket); restart() }},
		{"herdr never answers", "timeout", func() { b.herdr.Hang(true) }, func() { b.herdr.Hang(false) }},
		{"agent.list won't decode", "bad_reply",
			func() { b.herdr.SetListReply(`{"id":"pager-bridge:agent.list","result":{"type":"agent_list"}}`) },
			func() { b.herdr.SetListReply("") }},
		{"herdr answers an error", "server_unavailable",
			func() {
				b.herdr.SetListReply(strings.TrimSpace(readFile(t, "../testdata/herdr/socket/agent.list/error-server-unavailable.json")))
			},
			func() { b.herdr.SetListReply("") }},
	} {
		c.cause()
		b.clock.Advance(watch.Interval)
		snap := recv[*protocol.Snap](t, dev) // a timeout takes the client's 2 s, in real time
		if snap.Herdr != "offline" || len(snap.Agents) != 0 {
			t.Errorf("%s: herdr %q with %d agents; want offline with none", c.why, snap.Herdr, len(snap.Agents))
		}
		if !b.waitLog("code=" + c.code) {
			t.Errorf("%s: the log doesn't give the code %s:\n%s", c.why, c.code, b.log)
		}

		c.recovers()
		back := false
		for waited := time.Duration(0); waited <= 2*time.Second && !back; waited += 100 * time.Millisecond {
			if line, err := dev.Recv(100 * time.Millisecond); err == nil {
				m, err := protocol.ParseBridge([]byte(line))
				snap, ok := m.(*protocol.Snap)
				back = err == nil && ok && snap.Herdr == "ok" && len(snap.Agents) == 1
			}
			if !back {
				b.clock.Advance(100 * time.Millisecond)
			}
		}
		if !back {
			t.Fatalf("%s, then herdr answered: no normal snapshot within 2 s", c.why)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
