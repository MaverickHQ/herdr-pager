package herdr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A peer is a herdr socket for the client's tests: each connection gets the
// next reply line from replies, as herdr's own wire line, and the peer keeps
// what each connection sent. A nil reply is never sent: herdr hangs.
type peer struct {
	t       *testing.T
	socket  string
	mu      sync.Mutex
	replies [][]byte
	sent    [][]byte // what each connection sent, in order
	extra   []bool   // whether it sent anything after its first line
}

func startPeer(t *testing.T) *peer {
	t.Helper()
	dir, err := os.MkdirTemp("", "hp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := &peer{t: t, socket: filepath.Join(dir, "herdr.sock")}
	l, err := net.Listen("unix", p.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go p.answer(conn)
		}
	}()
	return p
}

func (p *peer) answer(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, more := r.ReadByte()
	p.mu.Lock()
	p.sent = append(p.sent, line)
	p.extra = append(p.extra, more == nil)
	reply := p.replies[0]
	p.replies = p.replies[1:]
	p.mu.Unlock()
	if reply == nil {
		time.Sleep(3 * time.Second)
		return
	}
	conn.Write(reply)
}

// reply queues the reply in testdata/herdr/socket/name for the next
// connection; "" queues none.
func (p *peer) reply(name string) {
	p.t.Helper()
	var line []byte
	if name != "" {
		line = fixture(p.t, name)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.replies = append(p.replies, line)
}

// replyLine queues line, as it is, for the next connection.
func (p *peer) replyLine(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.replies = append(p.replies, []byte(line))
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(socketDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestHerdrClientShapes checks the client against herdr 0.9.3's socket
// (phase 2 R5.1, R5.2, design D2-11): one request per connection and one line
// each way, each request the schema's line byte for byte, `params` always
// sent; agent.get, agent.focus and agent.read unwrapped; the title from
// terminal_title_stripped; a missing name shown as "<kind> <pane id>"; agents
// keyed by terminal_id and addressed by pane_id; an error envelope as its
// code; and no reply within 2 s a timeout.
func TestHerdrClientShapes(t *testing.T) {
	p := startPeer(t)
	c := &Client{SocketPath: p.socket}
	ctx := context.Background()

	p.reply("agent.list/ok-unmanaged-inferred.json")
	agents, err := c.ListAgents(ctx)
	if err != nil || len(agents) != 2 {
		t.Fatalf("ListAgents: %d agents, %v; want the fixture's 2", len(agents), err)
	}
	for i, want := range []struct{ terminal, pane, title, name string }{
		{"term_65e59fc19e7cb1", "w1:p1", "Touch approved.txt command", "recon"},
		{"term_0000000000test", "w2:p4", "Touch approved.txt command", "Touch approved.txt command"}, // no name: the title (the desk checkpoint)
	} {
		a := agents[i]
		if a.TerminalID != want.terminal || a.PaneID != want.pane || a.TitleStripped == nil || *a.TitleStripped != want.title || a.DisplayName() != want.name {
			t.Errorf("agent %d: terminal %s, pane %s, title %v, name %q; want %+v", i, a.TerminalID, a.PaneID, a.TitleStripped, a.DisplayName(), want)
		}
	}

	p.reply("agent.get/ok.json")
	info, err := c.GetAgent(ctx, "w1:p1")
	if err != nil || info.PaneID != "w1:p1" || info.Status != "blocked" || info.StateChangeSeq != 3 {
		t.Errorf("GetAgent: %+v, %v; want w1:p1 blocked at seq 3 (agent.get/ok.json)", info, err)
	}

	p.reply("agent.read/ok.json")
	text, err := c.ReadDetection(ctx, "w1:p1")
	capture, _ := os.ReadFile("../../testdata/herdr/claude/detection/shell-0s.txt")
	if err != nil || text != string(capture) {
		t.Errorf("ReadDetection: %d bytes, %v; want the shell-0s capture's %d", len(text), err, len(capture))
	}

	p.reply("agent.send_keys/ok.json")
	if err := c.SendKeys(ctx, "w1:p1", []string{"1"}); err != nil {
		t.Errorf("SendKeys: %v", err)
	}

	p.reply("agent.focus/ok.json")
	if err := c.Focus(ctx, "w1:p1"); err != nil {
		t.Errorf("Focus: %v", err)
	}

	for i, want := range []string{"agent.list", "agent.get", "agent.read", "agent.send_keys", "agent.focus"} {
		if i >= len(p.sent) {
			t.Fatalf("%d connections; want one per call", len(p.sent))
		}
		if got, line := p.sent[i], fixture(t, want+"/request.json"); !bytes.Equal(got, line) {
			t.Errorf("%s: the client sent\n%s\nwant\n%s", want, got, line)
		}
		if p.extra[i] {
			t.Errorf("%s: the client sent more than one line on its connection", want)
		}
	}

	for _, c2 := range []struct {
		reply, code string
		call        func() error
	}{
		{"agent.get/error-agent-not-found.json", "agent_not_found", func() error { _, err := c.GetAgent(ctx, "w1:p9"); return err }},
		{"agent.read/error-agent-not-found.json", "agent_not_found", func() error { _, err := c.ReadDetection(ctx, "w1:p9"); return err }},
		{"agent.send_keys/error-agent-not-ready.json", "agent_not_ready", func() error { return c.SendKeys(ctx, "w1:p1", []string{"1"}) }},
		{"agent.focus/error-agent-not-found.json", "agent_not_found", func() error { return c.Focus(ctx, "w1:p9") }},
		{"agent.list/error-server-unavailable.json", "server_unavailable", func() error { _, err := c.ListAgents(ctx); return err }},
	} {
		p.reply(c2.reply)
		var herr *Error
		if err := c2.call(); !errors.As(err, &herr) || herr.Code != c2.code {
			t.Errorf("%s: %v; want herdr error %q", c2.reply, err, c2.code)
		}
	}

	p.reply("")
	began := time.Now()
	_, err = c.ListAgents(ctx)
	took := time.Since(began)
	if !errors.Is(err, os.ErrDeadlineExceeded) || took < Timeout || took > Timeout+time.Second {
		t.Errorf("no reply: %v after %v; want a timeout after %v", err, took, Timeout)
	}
}

// TestHerdrCodeOfAnyOtherShapeIsOther checks that only a code of herdr's
// shape, lower case and underscores up to 32 bytes, is logged as it came: any
// other is `other`, in Code and in the error's text, so a changed herdr can't
// put a pane's target or an agent's text in the log (the owner's decision,
// 2026-10-27, open-items O-19).
func TestHerdrCodeOfAnyOtherShapeIsOther(t *testing.T) {
	p := startPeer(t)
	c := &Client{SocketPath: p.socket}
	for _, code := range []string{"agent not found", "w1:p9", strings.Repeat("a", 300), "Agent_Not_Found", "agent_not_found"} {
		quoted, err := json.Marshal(code)
		if err != nil {
			t.Fatal(err)
		}
		p.replyLine(fmt.Sprintf(`{"id":"pager-bridge:agent.get","error":{"code":%s,"message":"agent target w1:p9 not found"}}`+"\n", quoted))
		_, err = c.GetAgent(context.Background(), "w1:p9")
		want := "other"
		if code == "agent_not_found" {
			want = code
		}
		if got := Code(err); got != want || err == nil || err.Error() != "herdr: "+want {
			t.Errorf("herdr's code %.40q: Code %q, error %v; want %q in both", code, got, err, want)
		}
	}
}

// TestCancelledCallEndsAtOnce checks that a call whose context ends while
// herdr hasn't answered returns then, not at its 2 s deadline, and says it
// was cancelled (lane e2e's finding 4: the context bounded only the dial).
func TestCancelledCallEndsAtOnce(t *testing.T) {
	p := startPeer(t)
	c := &Client{SocketPath: p.socket}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	p.reply("")
	began := time.Now()
	_, err := c.ListAgents(ctx)
	took := time.Since(began)
	if !errors.Is(err, context.Canceled) || took > 500*time.Millisecond {
		t.Errorf("cancelled at 100 ms: %v after %v; want context.Canceled within 500 ms", err, took)
	}
}
