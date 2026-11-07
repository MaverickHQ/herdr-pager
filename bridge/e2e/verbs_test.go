package e2e

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/captures"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devsim"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdrtest"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
)

// capture returns the first of phase 0's Claude Code captures, read through
// the manifest, that match says is the one wanted: its manifest entry, its
// agent (the agent.get taken with it) moved to pane, and its detection text.
func capture(t *testing.T, pane string, match func(captures.Capture, herdr.AgentInfo) bool) (captures.Capture, herdr.AgentInfo, string) {
	t.Helper()
	m, err := captures.Load("claude")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Captures {
		if !c.Captured {
			continue
		}
		agent, err := herdrtest.Agent(filepath.Join(m.Dir, c.Agent))
		if err != nil {
			t.Fatal(err)
		}
		if !match(c, agent) {
			continue
		}
		screen, err := m.Read(c.File)
		if err != nil {
			t.Fatal(err)
		}
		agent.PaneID, agent.TerminalID = pane, "term_"+pane
		return c, agent, string(screen)
	}
	t.Fatal("no capture in the manifest matches")
	return captures.Capture{}, herdr.AgentInfo{}, ""
}

// isShellPrompt picks phase 0's shell prompt, for capture: a permission
// prompt for a shell command.
func isShellPrompt(c captures.Capture, _ herdr.AgentInfo) bool {
	return c.Kind == "permission" && c.PromptType == "shell"
}

// isWorking picks phase 0's working agent, for capture.
func isWorking(c captures.Capture, a herdr.AgentInfo) bool {
	return c.Kind == "status" && a.Status == "working"
}

// reply sends line and returns the bridge's answer to request id (await).
func reply(t *testing.T, d *devsim.Device, line string, id uint32) protocol.Message {
	t.Helper()
	if err := d.Send(line); err != nil {
		t.Fatal(err)
	}
	return await(t, d, id)
}

// await returns the bridge's answer to request id, skipping any snapshot
// that comes first.
func await(t *testing.T, d *devsim.Device, id uint32) protocol.Message {
	t.Helper()
	for {
		if m := recvAny(t, d); answers(m, id) {
			return m
		}
	}
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// show asks for pane's card as request id, and returns it.
func show(t *testing.T, d *devsim.Device, pane string, id uint32) *protocol.Card {
	t.Helper()
	m := reply(t, d, fmt.Sprintf(`{"t":"show","id":%d,"pane":"%s","pg":-1}`, id, pane), id)
	c, ok := m.(*protocol.Card)
	if !ok {
		t.Fatalf("show %s: %+v; want a card", pane, m)
	}
	return c
}

// verdict is a verdict line, approve or deny, on card c as request id.
func verdict(verb string, id uint32, c *protocol.Card) string {
	return fmt.Sprintf(`{"t":"%s","id":%d,"pane":"%s","seq":%d,"hash":"%s"}`, verb, id, c.Pane, c.Seq, c.Hash)
}

// result returns m as a result, or fails.
func result(t *testing.T, m protocol.Message) *protocol.Result {
	t.Helper()
	r, ok := m.(*protocol.Result)
	if !ok {
		t.Fatalf("%+v; want a result", m)
	}
	return r
}

// shellPrompt starts a bridge whose fake herdr has phase 0's shell prompt
// (through the manifest) on w1:p3, and connects a device, past its first
// snapshot.
func shellPrompt(t *testing.T) (*testBridge, *devsim.Device, herdr.AgentInfo) {
	t.Helper()
	_, agent, screen := capture(t, "w1:p3", isShellPrompt)
	b := startBridge(t, []herdr.AgentInfo{agent})
	b.herdr.SetDetection(agent.PaneID, screen)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)
	return b, dev, agent
}

// TestApproveSendsKeymapKeys checks parent 8.3 and 8.5, and the bridge's leg
// of N2: an approve on the card the device was shown sends herdr exactly the
// Claude Code keymap's approve keys, ["1"] (bridge/keymaps/claude.toml, from
// phase 0's KEYS.md: "1. Yes"), once, to that pane, and nothing the device
// sent (the fixtures' extra `keys` field is ignored); the device gets `ok`;
// and the bridge logs the verdict. From the approve's send to its result, on
// real loopback SSH and the in-process fake herdr, takes at most 200 ms.
func TestApproveSendsKeymapKeys(t *testing.T) {
	b, dev, agent := shellPrompt(t)
	c := show(t, dev, agent.PaneID, 9)

	line := strings.TrimSuffix(verdict("approve", 10, c), "}") + `,"keys":["y","enter"]}` // as approve-extra-field.ndjson
	began := time.Now()
	r := result(t, reply(t, dev, line, 10))
	took := time.Since(began)
	t.Logf("approve to result: %v", took)
	if !r.OK || r.Err != "" {
		t.Fatalf("approve: %+v; want ok", r)
	}
	if want := []herdrtest.Sent{{Pane: "w1:p3", Keys: []string{"1"}}}; !reflect.DeepEqual(b.herdr.Sent(), want) {
		t.Errorf("herdr was sent %+v; want %+v", b.herdr.Sent(), want)
	}
	if took > 200*time.Millisecond {
		t.Errorf("approve to result took %v; at most 200 ms (N2's bridge leg)", took)
	}
	if !b.waitLog("verb=approve") {
		t.Errorf("the bridge didn't log the approve:\n%s", b.log)
	}
}

// TestSecondVerdictOnACardSendsNothing checks that a card is answered once
// (parent 8.4, the threat model's T6: never replayed): after an approve has
// sent its keys, a second approve, or a deny, on the same card (its seq and
// hash) gets `stale` while herdr still shows that card, and the keys go once.
// Group 13's review (lane verdicts' finding 1).
func TestSecondVerdictOnACardSendsNothing(t *testing.T) {
	b, dev, agent := shellPrompt(t)
	c := show(t, dev, agent.PaneID, 9)

	if r := result(t, reply(t, dev, verdict("approve", 10, c), 10)); !r.OK {
		t.Fatalf("approve: %+v; want ok", r)
	}
	for i, verb := range []string{"approve", "deny"} {
		id := uint32(11 + i)
		if r := result(t, reply(t, dev, verdict(verb, id, c), id)); r.OK || r.Err != "stale" {
			t.Errorf("a second %s on the card: %+v; want result stale", verb, r)
		}
	}
	if want := []herdrtest.Sent{{Pane: "w1:p3", Keys: []string{"1"}}}; !reflect.DeepEqual(b.herdr.Sent(), want) {
		t.Errorf("herdr was sent %+v; want %+v, once", b.herdr.Sent(), want)
	}
}

// TestApproveDangerCard checks parent 18.3–18.4 and 8.5 at the bridge: phase
// 0's danger capture (through the manifest) gets a permission card flagged
// danger, with its label, and an approve at its seq and hash sends the
// Claude Code keymap's approve keys once, as for any permission card: the
// 2 s hold is the device's, and the bridge holds no verdict back for the
// risk. A second approve is stale.
func TestApproveDangerCard(t *testing.T) {
	_, agent, screen := capture(t, "w1:p3", func(c captures.Capture, a herdr.AgentInfo) bool {
		return c.Kind == "danger" && a.Status == "blocked"
	})
	b := startBridge(t, []herdr.AgentInfo{agent})
	b.herdr.SetDetection(agent.PaneID, screen)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)

	c := show(t, dev, agent.PaneID, 9)
	if c.Kind != "permission" || c.Risk != "danger" || c.RL == "" || !slices.Contains(c.Can, "approve") {
		t.Fatalf("card: kind %s, risk %q %q, can %v; want a permission card flagged danger, with its label, that can be approved", c.Kind, c.Risk, c.RL, c.Can)
	}
	if r := result(t, reply(t, dev, verdict("approve", 10, c), 10)); !r.OK {
		t.Fatalf("approve: %+v; want ok", r)
	}
	if r := result(t, reply(t, dev, verdict("approve", 11, c), 11)); r.OK || r.Err != "stale" {
		t.Errorf("a second approve: %+v; want result stale", r)
	}
	if want := []herdrtest.Sent{{Pane: "w1:p3", Keys: []string{"1"}}}; !reflect.DeepEqual(b.herdr.Sent(), want) {
		t.Errorf("herdr was sent %+v; want %+v, once", b.herdr.Sent(), want)
	}
}

// TestDenySendsDenyKeys checks parent 8.5 for deny: a deny on the card the
// device was shown sends herdr exactly the Claude Code keymap's deny keys,
// ["esc"] (bridge/keymaps/claude.toml, from phase 0's KEYS.md: "Esc to
// cancel"), once, to that pane; the device gets `ok`; and the bridge logs
// the verdict.
func TestDenySendsDenyKeys(t *testing.T) {
	b, dev, agent := shellPrompt(t)
	c := show(t, dev, agent.PaneID, 9)

	if r := result(t, reply(t, dev, verdict("deny", 11, c), 11)); !r.OK || r.Err != "" {
		t.Fatalf("deny: %+v; want ok", r)
	}
	if want := []herdrtest.Sent{{Pane: "w1:p3", Keys: []string{"esc"}}}; !reflect.DeepEqual(b.herdr.Sent(), want) {
		t.Errorf("herdr was sent %+v; want %+v", b.herdr.Sent(), want)
	}
	if !b.waitLog("verb=deny") {
		t.Errorf("the bridge didn't log the deny:\n%s", b.log)
	}
}

// TestUnreadCommandOffersDenyOnly checks the owner's fail-closed decision
// (2026-11-01; the security pass, findings 2 and 4) on the wire: phase 0's
// shell prompt with a ╌ in its command, which the keymap can't read, is a
// permission card that says COMMAND NOT READ first and offers deny and jump
// only. An approve carrying its own seq and hash, as a pager that broke the
// rules would send, gets `not_allowed` and sends nothing; a deny sends the
// deny keys.
func TestUnreadCommandOffersDenyOnly(t *testing.T) {
	c, agent, screen := capture(t, "w1:p3", isShellPrompt)
	screen = strings.Replace(screen, "\n "+c.Command+"\n", "\n "+c.Command+" ╌ echo\n", 1)
	b := startBridge(t, []herdr.AgentInfo{agent})
	b.herdr.SetDetection(agent.PaneID, screen)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)

	card := show(t, dev, agent.PaneID, 9)
	if card.Kind != "permission" || !slices.Equal(card.Can, []string{"deny", "jump"}) || len(card.Lines) == 0 || card.Lines[0] != "COMMAND NOT READ" {
		t.Fatalf("the card: kind %s, can %v, lines %q; want a permission card, deny and jump only, COMMAND NOT READ first", card.Kind, card.Can, card.Lines)
	}
	if r := result(t, reply(t, dev, verdict("approve", 10, card), 10)); r.OK || r.Err != "not_allowed" {
		t.Errorf("approve: %+v; want result not_allowed", r)
	}
	if sent := b.herdr.Sent(); len(sent) > 0 {
		t.Fatalf("herdr was sent %+v on an approve; want nothing", sent)
	}
	if r := result(t, reply(t, dev, verdict("deny", 11, card), 11)); !r.OK || r.Err != "" {
		t.Fatalf("deny: %+v; want ok", r)
	}
	if want := []herdrtest.Sent{{Pane: "w1:p3", Keys: []string{"esc"}}}; !reflect.DeepEqual(b.herdr.Sent(), want) {
		t.Errorf("herdr was sent %+v; want %+v", b.herdr.Sent(), want)
	}
}

// A request is one request the fake herdr logged (herdrtest's LogRequests).
type request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// requests returns the requests in log, in order, without the watcher's
// agent.list polls.
func requests(t *testing.T, log *syncBuffer) []request {
	t.Helper()
	var all []request
	for _, line := range strings.FieldsFunc(log.String(), func(r rune) bool { return r == '\n' }) {
		var r request
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("the fake herdr's request log: %v: %s", err, line)
		}
		if r.Method != "agent.list" {
			all = append(all, r)
		}
	}
	return all
}

// staleVerdicts approves and denies card c on dev, ids 10 and 11, and checks
// each gets `stale` and that no keys were sent; that the bridge re-read the
// agent for each with an agent.get and an agent.read whose params are the
// show's own (shown: the show's two requests), and called nothing else; and
// that it logs each verdict as stale.
func staleVerdicts(t *testing.T, b *testBridge, dev *devsim.Device, log *syncBuffer, shown []request, c *protocol.Card) {
	t.Helper()
	for i, verb := range []string{"approve", "deny"} {
		id := uint32(10 + i)
		if r := result(t, reply(t, dev, verdict(verb, id, c), id)); r.OK || r.Err != "stale" {
			t.Errorf("%s: %+v; want result stale", verb, r)
		}
	}
	if sent := b.herdr.Sent(); len(sent) > 0 {
		t.Errorf("herdr was sent %+v; want nothing", sent)
	}
	all := requests(t, log)
	if want := append(slices.Clone(shown), shown...); !reflect.DeepEqual(all[len(shown):], want) {
		t.Errorf("the verdicts asked herdr %s; want each to re-read with the show's requests, %s, and nothing else", all[len(shown):], shown)
	}
	if !b.waitLog("verb=approve") || !b.waitLog("verb=deny") || !b.waitLog("stale") {
		t.Errorf("the bridge didn't log the stale verdicts:\n%s", b.log)
	}
}

// TestStaleByStatusSendsNothing checks parent 8.3 and 8.4 for the status: the
// agent the device was shown blocked is working by the time its approve or
// deny comes, at the same seq and with the same screen (so only the status
// tells). Each verdict gets `stale` and no keys are sent. The bridge re-reads
// the agent for each verdict with agent.get and with agent.read, using the
// show's own detection parameters (pane, source detection, format text).
func TestStaleByStatusSendsNothing(t *testing.T) {
	b, dev, agent := shellPrompt(t)
	log := &syncBuffer{}
	b.herdr.LogRequests(log)
	c := show(t, dev, agent.PaneID, 9)
	shown := requests(t, log)
	if len(shown) != 2 || shown[0].Method != "agent.get" || shown[1].Method != "agent.read" {
		t.Fatalf("the show asked herdr %s; want an agent.get and an agent.read", shown)
	}

	agent.Status = "working"
	b.herdr.SetAgents([]herdr.AgentInfo{agent})
	staleVerdicts(t, b, dev, log, shown, c)
}

// TestStaleBySeqSendsNothing checks parent 8.3 and 8.4 for the seq. The
// agent the device was shown is still blocked on the same screen, but at a
// later seq (a second prompt that reads the same, as phase 0's shell-repeat
// is): an approve or deny on the card it was shown gets `stale`, and no keys
// are sent. The bridge re-read the agent as the show did. And a verdict
// whose seq isn't the card's, though its hash is the card's as herdr shows it
// now, is stale too.
func TestStaleBySeqSendsNothing(t *testing.T) {
	b, dev, agent := shellPrompt(t)
	log := &syncBuffer{}
	b.herdr.LogRequests(log)
	c := show(t, dev, agent.PaneID, 9)
	shown := requests(t, log)

	agent.StateChangeSeq++
	b.herdr.SetAgents([]herdr.AgentInfo{agent})
	staleVerdicts(t, b, dev, log, shown, c)

	now := show(t, dev, agent.PaneID, 12)
	if now.Seq != agent.StateChangeSeq {
		t.Fatalf("the card now has seq %d; want %d", now.Seq, agent.StateChangeSeq)
	}
	wrong := *now
	wrong.Seq--
	for i, verb := range []string{"approve", "deny"} {
		id := uint32(13 + i)
		if r := result(t, reply(t, dev, verdict(verb, id, &wrong), id)); r.OK || r.Err != "stale" {
			t.Errorf("%s with seq %d and the hash of the card at seq %d: %+v; want result stale", verb, wrong.Seq, now.Seq, r)
		}
	}
	if sent := b.herdr.Sent(); len(sent) > 0 {
		t.Errorf("herdr was sent %+v; want nothing", sent)
	}
}

// TestStaleByHashSendsNothing checks parent 8.3 and 8.4 for the card's hash.
// The agent the device was shown is still blocked at the same seq, but the
// command in its prompt box now reads differently (a variant of phase 0's
// shell capture, written by hand): an approve or deny on the card it was
// shown gets `stale`, and no keys are sent. The bridge re-read the agent as
// the show did. And a verdict whose hash differs from the card's in one
// digit, with herdr's screen unchanged, is stale too.
func TestStaleByHashSendsNothing(t *testing.T) {
	shell, agent, screen := capture(t, "w1:p3", isShellPrompt)
	b := startBridge(t, []herdr.AgentInfo{agent})
	b.herdr.SetDetection(agent.PaneID, screen)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)
	log := &syncBuffer{}
	b.herdr.LogRequests(log)
	c := show(t, dev, agent.PaneID, 9)
	shown := requests(t, log)

	line := "\n " + shell.Command + "\n" // the command's line in the box
	if strings.Count(screen, line) != 1 {
		t.Fatalf("%s: the command %q isn't one line of the screen", shell.File, shell.Command)
	}
	b.herdr.SetDetection(agent.PaneID, strings.Replace(screen, line, "\n "+shell.Command+"; rm notes.txt\n", 1))
	staleVerdicts(t, b, dev, log, shown, c)

	b.herdr.SetDetection(agent.PaneID, screen)
	if again := show(t, dev, agent.PaneID, 12); again.Hash != c.Hash {
		t.Fatalf("the screen as it was: hash %s; want the card's first hash, %s", again.Hash, c.Hash)
	}
	wrong := *c // the card's hash with its last digit changed
	wrong.Hash = c.Hash[:63] + "0"
	if c.Hash[63] == '0' {
		wrong.Hash = c.Hash[:63] + "1"
	}
	for i, verb := range []string{"approve", "deny"} {
		id := uint32(13 + i)
		if r := result(t, reply(t, dev, verdict(verb, id, &wrong), id)); r.OK || r.Err != "stale" {
			t.Errorf("%s with the card's hash but one digit: %+v; want result stale", verb, r)
		}
	}
	if sent := b.herdr.Sent(); len(sent) > 0 {
		t.Errorf("herdr was sent %+v; want nothing", sent)
	}
}

// A gate stands in front of a fake herdr, on its socket path, and passes
// each request through to it, except that once armed it holds the next
// agent.get until released, and that it answers a method itself once told
// how. It lets a test stop a verdict inside the bridge, or have herdr fail.
type gate struct {
	armed   chan struct{} // holds a token while armed
	held    chan struct{} // gets one when a request is held
	release chan struct{} // closed to let the held request through

	mu      sync.Mutex
	answers map[string][]byte // the gate's own reply line, by method
	methods []string          // every request's method, in order
}

// answer makes the gate answer every request for method with the reply line
// in file (under testdata/herdr/socket/), instead of herdr.
func (g *gate) answer(t *testing.T, method, file string) {
	t.Helper()
	line, err := os.ReadFile(filepath.Join("..", "testdata", "herdr", "socket", file))
	if err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.answers[method] = line
}

// calls is how many requests for method the gate has seen.
func (g *gate) calls(method string) (n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, m := range g.methods {
		if m == method {
			n++
		}
	}
	return n
}

// newGate moves b's fake herdr to another socket in its folder and puts a
// gate on its old path, where the bridge calls it.
func newGate(t *testing.T, b *testBridge) *gate {
	t.Helper()
	real := filepath.Join(filepath.Dir(b.herdr.Socket), "real.sock")
	if err := os.Rename(b.herdr.Socket, real); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", b.herdr.Socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	g := &gate{armed: make(chan struct{}, 1), held: make(chan struct{}, 1), release: make(chan struct{}), answers: map[string][]byte{}}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go g.pass(conn, real)
		}
	}()
	return g
}

// arm makes the gate hold the next agent.get.
func (g *gate) arm() { g.armed <- struct{}{} }

func (g *gate) pass(conn net.Conn, real string) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req request
	json.Unmarshal(line, &req)
	g.mu.Lock()
	g.methods = append(g.methods, req.Method)
	own := g.answers[req.Method]
	g.mu.Unlock()
	if own != nil {
		conn.Write(own)
		return
	}
	if req.Method == "agent.get" {
		select {
		case <-g.armed:
			g.held <- struct{}{}
			<-g.release
		default:
		}
	}
	up, err := net.Dial("unix", real)
	if err != nil {
		return
	}
	defer up.Close()
	up.Write(line)
	io.Copy(conn, up)
}

// TestConcurrentVerdictsOneWins checks parent 8.3 and the threat model's T6:
// two pagers approve the same agent's card at once. The first approve is
// held inside the bridge, at its re-read of the agent (a gate in front of the
// fake herdr), while the second comes: the second gets `stale` at once,
// without waiting; then the first goes on and gets `ok`. The keymap's keys
// are sent once. Then the agent moves on to a second prompt, and the second
// pager's approve on its card is sent: the pane's lock was let go.
func TestConcurrentVerdictsOneWins(t *testing.T) {
	_, agent, screen := capture(t, "w1:p3", isShellPrompt)
	b := startBridge(t, []herdr.AgentInfo{agent})
	b.herdr.SetDetection(agent.PaneID, screen)
	first, second := b.connect("pager-01"), b.connect("pager-02")
	recv[*protocol.Snap](t, first)
	recv[*protocol.Snap](t, second)
	g := newGate(t, b)
	c := show(t, first, agent.PaneID, 9)
	if c2 := show(t, second, agent.PaneID, 9); c2.Hash != c.Hash || c2.Seq != c.Seq {
		t.Fatalf("the two pagers were shown different cards: %+v and %+v", c, c2)
	}

	g.arm()
	if err := first.Send(verdict("approve", 10, c)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-g.held:
	case <-time.After(5 * time.Second):
		t.Fatal("the first approve never re-read the agent")
	}
	if r := result(t, reply(t, second, verdict("approve", 10, c), 10)); r.OK || r.Err != "stale" {
		t.Errorf("the second approve, while the first is in flight: %+v; want result stale", r)
	}
	close(g.release)
	if r := result(t, await(t, first, 10)); !r.OK {
		t.Errorf("the first approve: %+v; want ok", r)
	}
	if want := []herdrtest.Sent{{Pane: "w1:p3", Keys: []string{"1"}}}; !reflect.DeepEqual(b.herdr.Sent(), want) {
		t.Errorf("herdr was sent %+v; want the approve keys once, %+v", b.herdr.Sent(), want)
	}

	agent.StateChangeSeq++ // the next prompt, as phase 0's shell-repeat
	b.herdr.SetAgents([]herdr.AgentInfo{agent})
	next := show(t, second, agent.PaneID, 11)
	if r := result(t, reply(t, second, verdict("approve", 12, next), 12)); !r.OK {
		t.Errorf("the second pager's approve on the next prompt: %+v; want ok", r)
	}
	if n := len(b.herdr.Sent()); n != 2 {
		t.Errorf("herdr was sent keys %d times; want 2, once per prompt", n)
	}
}

// TestSendKeysRefused checks parent 8.4 and 8.5 for a send herdr refuses
// (herdr 0.9.3's errors, from the socket fixtures): after one send_keys call,
// never retried, `agent_not_ready` (the pane is no longer that agent) answers
// `stale`, logged why=agent_not_ready, so the pager says SCREEN CHANGED and
// asks for the card as it is now (the owner's decision, 2026-10-27,
// open-items O-46); `invalid_key`, a keymap bug, answers `herdr_error`, with
// herdr's code logged.
func TestSendKeysRefused(t *testing.T) {
	b, dev, agent := shellPrompt(t)
	g := newGate(t, b)
	for i, tc := range []struct{ file, want string }{
		{"error-agent-not-ready.json", "stale"},
		{"error-invalid-key.json", "herdr_error"},
	} {
		g.answer(t, "agent.send_keys", "agent.send_keys/"+tc.file)
		c := show(t, dev, agent.PaneID, uint32(20+2*i))
		before := g.calls("agent.send_keys")
		if r := result(t, reply(t, dev, verdict("approve", uint32(21+2*i), c), uint32(21+2*i))); r.OK || r.Err != tc.want {
			t.Errorf("herdr answers %s: %+v; want result %s", tc.file, r, tc.want)
		}
		if n := g.calls("agent.send_keys") - before; n != 1 {
			t.Errorf("herdr answers %s: %d send_keys calls; want 1, never retried", tc.file, n)
		}
	}
	if !b.waitLog("why=agent_not_ready") || !b.waitLog("code=invalid_key") {
		t.Errorf("the bridge didn't log why:\n%s", b.log)
	}
}

// TestQuestionCardRefusesVerdict checks parent 7.7 and 8.6: phase 0's
// question and plan-approval prompts (through the manifest) are blocked
// agents whose cards are questions, offering only jump. An approve or deny
// carrying such a card's own seq and hash, as a pager that broke the rules
// would send, gets `stale` (parent design §Verdict handling: a card that
// isn't a permission card), and no keys are sent: the keymap's "1" or esc
// would answer the question.
func TestQuestionCardRefusesVerdict(t *testing.T) {
	_, question, asks := capture(t, "w1:p4", func(c captures.Capture, _ herdr.AgentInfo) bool {
		return c.Kind == "question" && c.PromptType == "question"
	})
	_, plan, plans := capture(t, "w1:p5", func(c captures.Capture, _ herdr.AgentInfo) bool {
		return c.Kind == "question" && c.PromptType == "plan-approval"
	})
	b := startBridge(t, []herdr.AgentInfo{question, plan})
	b.herdr.SetDetection(question.PaneID, asks)
	b.herdr.SetDetection(plan.PaneID, plans)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)

	id := uint32(30)
	for _, agent := range []herdr.AgentInfo{question, plan} {
		c := show(t, dev, agent.PaneID, id)
		if c.Kind != "question" || c.St != "blocked" || !slices.Equal(c.Can, []string{"jump"}) {
			t.Errorf("%s: kind %s, st %s, can %v; want a blocked agent's question card, jump only", agent.PaneID, c.Kind, c.St, c.Can)
		}
		for _, verb := range []string{"approve", "deny"} {
			id++
			if r := result(t, reply(t, dev, verdict(verb, id, c), id)); r.OK || r.Err != "stale" {
				t.Errorf("%s: %s on its question card: %+v; want result stale", agent.PaneID, verb, r)
			}
		}
		id++
	}
	if sent := b.herdr.Sent(); len(sent) > 0 {
		t.Errorf("herdr was sent %+v; want nothing", sent)
	}
}

// TestSnapshotCanNeverApprove checks parent 8.1 and 8.6: an agent at each of
// the captured prompts is in herdr (one capture of each, through the
// manifest: permission, danger, question, plan-approval, working and done,
// and the tool prompts: fetch, Read and MCP calls, O-88 and O-90), with a blocked agent
// of a kind with no keymap and an idle one. The prompts are split into
// batches that fit one snapshot (protocol.MaxAgents, with those two), each
// in a bridge of its own, so another capture never outgrows it (O-88's
// review). In the snapshot each bridge pushes, and in the one a `list` asks
// for, every entry's `can` offers show and jump and never approve or deny.
// Only a card offers them: the card of each blocked agent at a permission
// prompt offers deny, and approve too unless the manifest marks it unread.
func TestSnapshotCanNeverApprove(t *testing.T) {
	m, err := captures.Load("claude")
	if err != nil {
		t.Fatal(err)
	}
	var all []captures.Capture // one capture of each prompt
	prompts := map[string]bool{}
	for _, c := range m.Captures {
		if c.Captured && !prompts[c.Prompt] {
			prompts[c.Prompt] = true
			all = append(all, c)
		}
	}
	// wrong says whether a snapshot entry's can breaks the rule.
	wrong := func(can []string) bool {
		return slices.Contains(can, "approve") || slices.Contains(can, "deny") || !slices.Contains(can, "show") || !slices.Contains(can, "jump")
	}
	statuses := map[string]bool{}
	cards, batches := 0, 0
	for batch := range slices.Chunk(all, protocol.MaxAgents-2) {
		batches++
		var agents []herdr.AgentInfo
		var screens []string
		permission := map[string]bool{} // panes at a permission prompt
		unread := map[string]bool{}     // of them, those whose card fails closed
		for _, c := range batch {
			pane := fmt.Sprintf("w1:p%d", len(agents)+1)
			_, agent, screen := capture(t, pane, func(d captures.Capture, _ herdr.AgentInfo) bool { return d.File == c.File })
			agents, screens = append(agents, agent), append(screens, screen)
			permission[pane] = c.Kind == "permission" || c.Kind == "danger"
			unread[pane] = c.Unread
		}
		codex := agents[0]
		codex.PaneID, codex.TerminalID, codex.Agent = "w2:p1", "term_w2:p1", str("codex")
		idle := agents[0]
		idle.PaneID, idle.TerminalID, idle.Status = "w2:p2", "term_w2:p2", "idle"
		agents, screens = append(agents, codex, idle), append(screens, screens[0], "")
		b := startBridge(t, agents)
		for i, a := range agents {
			b.herdr.SetDetection(a.PaneID, screens[i])
		}
		dev := b.connect("pager-01")

		pushed := recv[*protocol.Snap](t, dev)
		listed, ok := reply(t, dev, `{"t":"list","id":50}`, 0).(*protocol.Snap)
		if !ok {
			t.Fatal("a list got no snapshot")
		}
		for _, snap := range []*protocol.Snap{pushed, listed} {
			if len(snap.Agents) != len(agents) {
				t.Fatalf("batch %d: a snapshot with %d agents; want %d", batches, len(snap.Agents), len(agents))
			}
			for _, a := range snap.Agents {
				statuses[a.St] = true
				if wrong(a.Can) {
					t.Errorf("batch %d: %s (%s, %s): can %v; want show and jump, never approve or deny", batches, a.Pane, a.Kind, a.St, a.Can)
				}
			}
		}
		for i, a := range agents {
			if !permission[a.PaneID] || a.Status != "blocked" {
				continue
			}
			cards++
			if c := show(t, dev, a.PaneID, uint32(60+i)); slices.Contains(c.Can, "approve") == unread[a.PaneID] || !slices.Contains(c.Can, "deny") {
				t.Errorf("batch %d: %s: its card's can is %v; want deny, and approve unless unread (%v)", batches, a.PaneID, c.Can, unread[a.PaneID])
			}
		}
	}
	t.Logf("%d prompts in %d batches", len(all), batches)
	// What the check rests on: it flags the protocol fixtures' snapshot that
	// offers approve (read as plain JSON, since the contract rejects it).
	var bad struct {
		Agents []struct{ Can []string } `json:"agents"`
	}
	if err := json.Unmarshal([]byte(fixture(t, "to-device/invalid/snap-can-approve.ndjson")), &bad); err != nil || len(bad.Agents) == 0 || !wrong(bad.Agents[0].Can) {
		t.Errorf("the check doesn't flag snap-can-approve.ndjson's can (%v, %v)", bad.Agents, err)
	}
	for _, st := range []string{"blocked", "working", "done", "idle"} {
		if !statuses[st] {
			t.Errorf("no %s agent in the snapshots; the test wants every status", st)
		}
	}
	if cards == 0 {
		t.Error("no blocked agent at a permission prompt")
	}
}

// TestJumpFocusesOnly checks parent 9.4: a `jump` (the protocol fixtures'
// line, w1:p3) calls herdr's agent.focus for that pane, and nothing else: no
// re-read and no keys. The device gets `ok`. Jump needs no keymap: a codex
// agent's pane is focused the same way. A pane herdr doesn't know gets
// `unknown_pane`.
func TestJumpFocusesOnly(t *testing.T) {
	_, agent, screen := capture(t, "w1:p3", isShellPrompt)
	codex := agent
	codex.PaneID, codex.TerminalID, codex.Agent = "w2:p1", "term_w2:p1", str("codex")
	b := startBridge(t, []herdr.AgentInfo{agent, codex})
	b.herdr.SetDetection(agent.PaneID, screen)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)
	log := &syncBuffer{}
	b.herdr.LogRequests(log)

	if r := result(t, reply(t, dev, fixture(t, "to-bridge/valid/jump.ndjson"), 13)); !r.OK || r.Err != "" {
		t.Errorf("jump w1:p3: %+v; want ok", r)
	}
	if r := result(t, reply(t, dev, `{"t":"jump","id":14,"pane":"w2:p1"}`, 14)); !r.OK || r.Err != "" {
		t.Errorf("jump to the codex agent: %+v; want ok", r)
	}
	want := []request{
		{"agent.focus", json.RawMessage(`{"target":"w1:p3"}`)},
		{"agent.focus", json.RawMessage(`{"target":"w2:p1"}`)},
	}
	if got := requests(t, log); !reflect.DeepEqual(got, want) {
		t.Errorf("the jumps asked herdr %s; want %s", got, want)
	}
	if r := result(t, reply(t, dev, `{"t":"jump","id":15,"pane":"w9:p9"}`, 15)); r.OK || r.Err != "unknown_pane" {
		t.Errorf("jump to a pane herdr doesn't know: %+v; want result unknown_pane", r)
	}
	if sent := b.herdr.Sent(); len(sent) > 0 {
		t.Errorf("herdr was sent %+v; want nothing", sent)
	}
}

// TestInterruptRules checks parent 9.5 as phase 2 narrows it (R6.3): an
// interrupt of phase 0's working agent, at its seq, sends herdr exactly the
// Claude Code keymap's interrupt keys, ["esc"] (bridge/keymaps/claude.toml:
// never ctrl+c), and gets `ok`. An interrupt of a blocked agent (phase 0's
// shell prompt, where esc would be a deny that skipped the card's checks),
// a done one (phase 0's interrupted capture) or an idle one gets `stale`, as
// does one at a seq the working agent has moved past; and none sends keys.
// A working agent of a kind with no keymap can't be interrupted:
// `not_allowed`.
func TestInterruptRules(t *testing.T) {
	_, working, busy := capture(t, "w3:p1", isWorking)
	_, blocked, prompt := capture(t, "w1:p3", isShellPrompt)
	_, done, after := capture(t, "w2:p1", func(c captures.Capture, a herdr.AgentInfo) bool {
		return c.Kind == "status" && a.Status == "done"
	})
	idle := working
	idle.PaneID, idle.TerminalID, idle.Status = "w2:p2", "term_w2:p2", "idle"
	codex := working
	codex.PaneID, codex.TerminalID, codex.Agent = "w2:p3", "term_w2:p3", str("codex")
	b := startBridge(t, []herdr.AgentInfo{working, blocked, done, idle, codex})
	b.herdr.SetDetection(working.PaneID, busy)
	b.herdr.SetDetection(blocked.PaneID, prompt)
	b.herdr.SetDetection(done.PaneID, after)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)
	interrupt := func(id uint32, a herdr.AgentInfo, seq uint64) *protocol.Result {
		t.Helper()
		return result(t, reply(t, dev, fmt.Sprintf(`{"t":"interrupt","id":%d,"pane":"%s","seq":%d}`, id, a.PaneID, seq), id))
	}

	for i, a := range []herdr.AgentInfo{blocked, done, idle} {
		if r := interrupt(uint32(70+i), a, a.StateChangeSeq); r.OK || r.Err != "stale" {
			t.Errorf("interrupt of the %s agent: %+v; want result stale", a.Status, r)
		}
	}
	if r := interrupt(73, working, working.StateChangeSeq-1); r.OK || r.Err != "stale" {
		t.Errorf("interrupt of the working agent at an earlier seq: %+v; want result stale", r)
	}
	if r := interrupt(74, codex, codex.StateChangeSeq); r.OK || r.Err != "not_allowed" {
		t.Errorf("interrupt of a working codex agent: %+v; want result not_allowed", r)
	}
	if sent := b.herdr.Sent(); len(sent) > 0 {
		t.Fatalf("herdr was sent %+v; want nothing yet", sent)
	}
	if r := interrupt(75, working, working.StateChangeSeq); !r.OK || r.Err != "" {
		t.Errorf("interrupt of the working agent: %+v; want ok", r)
	}
	if want := []herdrtest.Sent{{Pane: "w3:p1", Keys: []string{"esc"}}}; !reflect.DeepEqual(b.herdr.Sent(), want) {
		t.Errorf("herdr was sent %+v; want %+v", b.herdr.Sent(), want)
	}
	// An interrupt logs as one, not as a verdict (lane verdicts' finding 4;
	// the owner's decision, 2026-10-27, open-items O-17).
	if !b.waitLog(`msg="session: interrupt sent" verb=interrupt pane=w3:p1`) || !b.waitLog(`msg="session: interrupt stale; nothing sent" verb=interrupt`) {
		t.Errorf("the bridge didn't log the interrupts as interrupts:\n%s", b.log)
	}
	if strings.Contains(b.log.String(), "verdict sent verb=interrupt") || strings.Contains(b.log.String(), `verdict stale; nothing sent" verb=interrupt`) {
		t.Errorf("an interrupt is logged as a verdict:\n%s", b.log)
	}
}

// TestNothingQueuedAfterClose checks parent 8.7 and parent design D2 (no
// verdict is queued or replayed). A pager approves phase 0's shell prompt,
// and its approve is held inside the bridge at its re-read of the agent (7.7's
// gate) while the pager's connection drops. Once the gate lets the re-read
// through, no keys are sent: the verdict died with its session. The pager
// connects again with the same key and gets the bridge's hello and a
// snapshot, and nothing else: no result for the old approve, and still no
// keys.
func TestNothingQueuedAfterClose(t *testing.T) {
	_, agent, screen := capture(t, "w1:p3", isShellPrompt)
	b := startBridge(t, []herdr.AgentInfo{agent})
	b.herdr.SetDetection(agent.PaneID, screen)
	key := b.pair("pager-01")
	dev := b.connectKey("pager-01", key)
	recv[*protocol.Snap](t, dev)
	g := newGate(t, b)
	c := show(t, dev, agent.PaneID, 9)

	g.arm()
	if err := dev.Send(verdict("approve", 10, c)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-g.held:
	case <-time.After(5 * time.Second):
		t.Fatal("the approve never re-read the agent")
	}
	dev.Close()
	time.Sleep(200 * time.Millisecond) // the bridge tears the connection down: on loopback, well within this
	close(g.release)

	again := b.connectKey("pager-01", key)
	recv[*protocol.Snap](t, again)
	if line, err := again.Recv(300 * time.Millisecond); !errors.Is(err, devsim.ErrTimeout) {
		t.Errorf("after the snapshot, the bridge sent %q (%v); want nothing", line, err)
	}
	if n := g.calls("agent.send_keys"); n > 0 || len(b.herdr.Sent()) > 0 {
		t.Errorf("herdr was asked to send keys %d times, and took %+v; want none", n, b.herdr.Sent())
	}
	// The approve its session outlived is logged as that, not as herdr out
	// of reach (lane verdicts' finding 5).
	if !b.waitLog("session: closed; nothing sent") {
		t.Errorf("the approve after its session closed isn't logged as such:\n%s", b.log)
	}
	if strings.Contains(b.log.String(), "code=unreachable") {
		t.Errorf("the closed session's herdr call is logged as herdr out of reach:\n%s", b.log)
	}
}

// TestUnknownKindNotAllowed checks parent 8.6: phase 0's shell prompt on
// screen, but in an agent of a kind with no keymap (codex), and in one herdr
// gives no kind. Each card offers only jump, and an approve or deny on it
// gets `not_allowed`: the bridge has no keys for it. No keys are sent.
func TestUnknownKindNotAllowed(t *testing.T) {
	_, codex, screen := capture(t, "w2:p1", isShellPrompt)
	codex.Agent = str("codex")
	none := codex
	none.PaneID, none.TerminalID, none.Agent = "w2:p2", "term_w2:p2", nil
	b := startBridge(t, []herdr.AgentInfo{codex, none})
	b.herdr.SetDetection(codex.PaneID, screen)
	b.herdr.SetDetection(none.PaneID, screen)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)

	id := uint32(40)
	for _, agent := range []herdr.AgentInfo{codex, none} {
		c := show(t, dev, agent.PaneID, id)
		if c.St != "blocked" || !slices.Equal(c.Can, []string{"jump"}) {
			t.Errorf("%s: st %s, can %v; want a blocked agent's card, jump only", agent.PaneID, c.St, c.Can)
		}
		for _, verb := range []string{"approve", "deny"} {
			id++
			if r := result(t, reply(t, dev, verdict(verb, id, c), id)); r.OK || r.Err != "not_allowed" {
				t.Errorf("%s: %s: %+v; want result not_allowed", agent.PaneID, verb, r)
			}
		}
		id++
	}
	if sent := b.herdr.Sent(); len(sent) > 0 {
		t.Errorf("herdr was sent %+v; want nothing", sent)
	}
}

// TestShowReturnsCard checks parent 7.1 at S2-1: a device's `show` (the
// protocol fixtures' lines) gets the agent's card, built from a fresh
// agent.get and agent.read. For phase 0's shell prompt it is a permission
// card with the manifest's title as its summary and its command first, which
// can be approved, denied or jumped to; for its working agent, a status card
// that can be interrupted or jumped to. Each carries the request's id, the
// agent's pane, status and seq, and a SHA-256 hash. A pane herdr doesn't know
// gets `unknown_pane`, and a show while herdr is gone `herdr_offline`.
// Nothing sends keys.
func TestShowReturnsCard(t *testing.T) {
	shell, blocked, prompt := capture(t, "w1:p3", isShellPrompt)
	_, working, busy := capture(t, "w3:p1", isWorking)
	b := startBridge(t, []herdr.AgentInfo{blocked, working})
	b.herdr.SetDetection(blocked.PaneID, prompt)
	b.herdr.SetDetection(working.PaneID, busy)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)

	before := len(b.herdr.Methods())
	m := reply(t, dev, fixture(t, "to-bridge/valid/show.ndjson"), 9) // w1:p3
	c, ok := m.(*protocol.Card)
	if !ok {
		t.Fatalf("show w1:p3: %+v; want a card", m)
	}
	if c.Pane != "w1:p3" || c.St != "blocked" || c.Seq != blocked.StateChangeSeq || c.Kind != "permission" || c.Risk != "" || c.RL != "" {
		t.Errorf("card: pane %s, st %s, seq %d, kind %s, risk %q %q; want w1:p3, blocked, %d, permission, no risk",
			c.Pane, c.St, c.Seq, c.Kind, c.Risk, c.RL, blocked.StateChangeSeq)
	}
	if c.Summary != shell.Title || len(c.Lines) == 0 || c.Lines[0] != shell.Command {
		t.Errorf("card: summary %q, lines %q; want %q, the command %q first (the manifest)", c.Summary, c.Lines, shell.Title, shell.Command)
	}
	if !slices.Equal(c.Can, []string{"approve", "deny", "jump"}) || !sha256Hex.MatchString(c.Hash) {
		t.Errorf("card: can %v, hash %q; want [approve deny jump] and SHA-256 hex", c.Can, c.Hash)
	}
	if calls := b.herdr.Methods()[before:]; !slices.Contains(calls, "agent.get") || !slices.Contains(calls, "agent.read") {
		t.Errorf("show called herdr's %q; want a fresh agent.get and agent.read", calls)
	}

	m = reply(t, dev, fixture(t, "to-bridge/valid/show-never-paged.ndjson"), 15) // w3:p1
	if c, ok := m.(*protocol.Card); !ok {
		t.Errorf("show w3:p1: %+v; want a card", m)
	} else if c.Pane != "w3:p1" || c.St != "working" || c.Seq != working.StateChangeSeq || c.Kind != "status" ||
		!slices.Equal(c.Can, []string{"interrupt", "jump"}) || !sha256Hex.MatchString(c.Hash) {
		t.Errorf("working card: pane %s, st %s, seq %d, kind %s, can %v, hash %q; want w3:p1, working, %d, status, [interrupt jump], SHA-256 hex",
			c.Pane, c.St, c.Seq, c.Kind, c.Can, c.Hash, working.StateChangeSeq)
	}

	if r, ok := reply(t, dev, `{"t":"show","id":20,"pane":"w9:p9","pg":-1}`, 20).(*protocol.Result); !ok || r.OK || r.Err != "unknown_pane" {
		t.Errorf("show of a pane herdr doesn't know: %+v; want result unknown_pane", r)
	}
	b.herdr.Close()
	if r, ok := reply(t, dev, `{"t":"show","id":21,"pane":"w1:p3","pg":-1}`, 21).(*protocol.Result); !ok || r.OK || r.Err != "herdr_offline" {
		t.Errorf("show with herdr gone: %+v; want result herdr_offline", r)
	}
	if sent := b.herdr.Sent(); len(sent) > 0 {
		t.Errorf("show sent keys: %+v", sent)
	}
}

// TestVerdictsOnUnknownPaneOrHerdrGone checks that approve, deny and
// interrupt answer as show does when herdr can't say (parent 4.4, design
// §Bridge components): a pane herdr doesn't know gets `unknown_pane`, logged
// by herdr's code, and any verb while herdr is gone `herdr_offline`. Nothing
// sends keys.
func TestVerdictsOnUnknownPaneOrHerdrGone(t *testing.T) {
	_, blocked, prompt := capture(t, "w1:p3", isShellPrompt)
	_, working, busy := capture(t, "w3:p1", isWorking)
	b := startBridge(t, []herdr.AgentInfo{blocked, working})
	b.herdr.SetDetection(blocked.PaneID, prompt)
	b.herdr.SetDetection(working.PaneID, busy)
	dev := b.connect("pager-01")
	recv[*protocol.Snap](t, dev)
	c := show(t, dev, blocked.PaneID, 30)
	gone := *c
	gone.Pane = "w9:p9"
	lines := func(cd *protocol.Card, a herdr.AgentInfo, id uint32) map[string]string {
		return map[string]string{
			"approve":   verdict("approve", id, cd),
			"deny":      verdict("deny", id+1, cd),
			"interrupt": fmt.Sprintf(`{"t":"interrupt","id":%d,"pane":"%s","seq":%d}`, id+2, a.PaneID, a.StateChangeSeq),
		}
	}
	unknown := working
	unknown.PaneID = "w9:p9"

	for _, step := range []struct {
		why, want string
		id        uint32
		lines     map[string]string
	}{
		{"a pane herdr doesn't know", "unknown_pane", 31, lines(&gone, unknown, 31)},
		{"herdr gone", "herdr_offline", 41, lines(c, working, 41)},
	} {
		if step.want == "herdr_offline" {
			b.herdr.Close()
		}
		for i, verb := range []string{"approve", "deny", "interrupt"} {
			id := step.id + uint32(i)
			if r := result(t, reply(t, dev, step.lines[verb], id)); r.OK || r.Err != step.want {
				t.Errorf("%s on %s: %+v; want result %s", verb, step.why, r, step.want)
			}
		}
	}
	if !b.waitLog("code=agent_not_found") {
		t.Errorf("the unknown pane isn't logged by herdr's code:\n%s", b.log)
	}
	if sent := b.herdr.Sent(); len(sent) > 0 {
		t.Errorf("keys were sent: %+v", sent)
	}
}
