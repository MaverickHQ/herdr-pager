package e2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/captures"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdrtest"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
)

// TestNoTerminalTextOutsideCards checks parent 4.5 across a session. The fake
// herdr holds every prompt phase 0 captured, with its detection text (through
// the manifest); the device takes snapshots, asks for each agent's card (a
// card comes back for every one), for a fresh list, and pings. Terminal text
// reaches it only inside a card: no other message has a field that holds a
// line of a capture, or that a capture holds. An agent's title isn't
// terminal text here: since the desk checkpoint it can be the agent's name
// (herdr.DisplayName). Nor is its folder, from herdr's cwd, which a screen
// may show too, so it isn't checked.
func TestNoTerminalTextOutsideCards(t *testing.T) {
	m, err := captures.Load("claude")
	if err != nil {
		t.Fatal(err)
	}
	var agents []herdr.AgentInfo
	var screens, telltales []string // screens[i] is agents[i]'s detection text
	for i, c := range m.Captures {
		if !c.Captured {
			continue
		}
		a, err := herdrtest.Agent(filepath.Join(m.Dir, c.Agent))
		if err != nil {
			t.Fatal(err)
		}
		a.PaneID, a.TerminalID = fmt.Sprintf("w1:p%d", i+1), fmt.Sprintf("term_%d", i+1)
		text, err := m.Read(c.File)
		if err != nil {
			t.Fatal(err)
		}
		agents = append(agents, a)
		screens = append(screens, string(text))
		for _, l := range strings.Split(string(text), "\n") {
			if l = strings.TrimSpace(l); len(l) >= 8 && printable(l, len(l)) {
				telltales = append(telltales, l)
			}
		}
	}
	b := startBridge(t, agents)
	for i, a := range agents {
		b.herdr.SetDetection(a.PaneID, screens[i])
	}
	dev := b.connect("pager-01")

	cards := 0
	check := func(line string) protocol.Message {
		t.Helper()
		msg, err := protocol.ParseBridge([]byte(line))
		if err != nil {
			t.Fatalf("the bridge sent %s, which breaks the protocol: %v", line, err)
		}
		if _, ok := msg.(*protocol.Card); ok {
			cards++
			return msg
		}
		for _, f := range fields(msg) {
			for _, tell := range telltales {
				if strings.Contains(f, tell) {
					t.Errorf("a %s carries terminal text %q: %s", msg.Type(), tell, line)
				}
			}
			for _, text := range screens {
				if len(f) >= 8 && strings.Contains(text, f) {
					t.Errorf("a %s carries %q, from terminal text: %s", msg.Type(), f, line)
				}
			}
		}
		return msg
	}
	// request sends one request and returns its answer: a card, result or
	// pong with its id, or for a list (id 0 here) the next snapshot. Then a
	// fifth of a second passes, inside the rate limit.
	request := func(line string, id uint32) protocol.Message {
		t.Helper()
		if err := dev.Send(line); err != nil {
			t.Fatal(err)
		}
		for {
			reply, err := dev.Recv(5 * time.Second)
			if err != nil {
				t.Fatalf("waiting for the answer to %s: %v", line, err)
			}
			if m := check(reply); answers(m, id) {
				b.clock.Advance(200 * time.Millisecond)
				return m
			}
		}
	}

	reply, err := dev.Recv(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	check(reply)
	for i, a := range agents {
		if m := request(fmt.Sprintf(`{"t":"show","id":%d,"pane":"%s","pg":-1}`, i+1, a.PaneID), uint32(i+1)); m.Type() != "card" {
			t.Errorf("show %s: %+v; want its card", a.PaneID, m)
		}
	}
	request(`{"t":"list","id":900}`, 0)
	request(`{"t":"ping","id":901}`, 901)
	if cards != len(agents) {
		t.Errorf("%d cards came back for %d agents; want one each", cards, len(agents))
	}
	t.Logf("%d agents, %d telltale lines; %d cards came back", len(agents), len(telltales), cards)
}

func answers(m protocol.Message, id uint32) bool {
	switch m := m.(type) {
	case *protocol.Card:
		return m.ID == id
	case *protocol.Result:
		return m.ID == id
	case *protocol.Pong:
		return m.ID == id
	case *protocol.Snap:
		return id == 0
	}
	return false
}

// fields is every string a message other than a card carries.
func fields(m protocol.Message) []string {
	switch m := m.(type) {
	case *protocol.BridgeHello:
		return []string{m.Bridge, m.Host}
	case *protocol.Snap:
		var f []string
		for _, a := range m.Agents {
			f = append(f, a.Pane, a.Name, a.Kind, a.St)
		}
		return f
	case *protocol.Result:
		return []string{m.Err}
	}
	return nil
}
