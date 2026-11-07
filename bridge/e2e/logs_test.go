package e2e

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/captures"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devsim"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdrtest"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/sanitize"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/session"
)

// The suite's record, for the check TestMain makes once every test has run
// (TestLogsCarryNoAgentText): every log a test captured from a bridge, and
// every key a test made or paired.
var suite struct {
	mu   sync.Mutex
	logs []capturedLog
	keys []ssh.PublicKey
}

// A capturedLog is a bridge's log in one test.
type capturedLog struct {
	test string
	log  *syncBuffer
}

// captureLog returns a new log for a bridge that t starts, in the suite's
// record.
func captureLog(t *testing.T) *syncBuffer {
	l := &syncBuffer{}
	suite.mu.Lock()
	defer suite.mu.Unlock()
	suite.logs = append(suite.logs, capturedLog{t.Name(), l})
	return l
}

// rememberKey puts a key that a test made or paired in the suite's record.
func rememberKey(key ssh.PublicKey) {
	suite.mu.Lock()
	defer suite.mu.Unlock()
	suite.keys = append(suite.keys, key)
}

func suiteLogs() []capturedLog {
	suite.mu.Lock()
	defer suite.mu.Unlock()
	return append([]capturedLog(nil), suite.logs...)
}

// needles is everything the bridge must never log (parent 11.5, 3.7), as it
// could be written there: the pieces of every agent name and title, and of
// every line of pane text, that any fake herdr in this process was given;
// and every key a test made or paired (keyText). Each is there as written,
// and as slog's handlers quote it.
func needles() []string {
	set := map[string]bool{}
	add := func(s string) {
		set[s] = true
		q := strconv.Quote(s)
		set[q[1:len(q)-1]] = true
	}
	names, panes := herdrtest.AllText()
	for _, n := range names {
		for _, p := range pieces(n, sanitize.Name, 5) {
			add(p)
		}
	}
	for _, text := range panes {
		for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
			for _, p := range pieces(line, sanitize.Line, 8) {
				add(p)
			}
		}
	}
	suite.mu.Lock()
	for _, k := range suite.keys {
		for _, s := range keyText(k) {
			add(s)
		}
	}
	suite.mu.Unlock()
	all := make([]string, 0, len(set))
	for s := range set {
		all = append(all, s)
	}
	return all
}

// pieces is how text could reach a log: its core (trimmed to a letter or
// digit at each end, its spaces folded); the first and last 16 characters of
// a core over 24, which a head-and-tail cut keeps; and the text sanitized at
// limit, as a snapshot or a card shows it. A piece shorter than min bytes, or
// with fewer than three letters (a time, a number), could turn up in a log by
// chance, so it's left out.
func pieces(text string, limit, min int) []string {
	core := strings.Join(strings.Fields(strings.TrimFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})), " ")
	all := []string{core, sanitize.Text(text, limit)}
	if r := []rune(core); len(r) > 24 {
		all = append(all, string(r[:16]), string(r[len(r)-16:]))
	}
	var kept []string
	for _, p := range all {
		letters := 0
		for _, r := range p {
			if unicode.IsLetter(r) {
				letters++
			}
		}
		if len(p) >= min && letters >= 3 {
			kept = append(kept, p)
		}
	}
	return kept
}

// keyText is how key could be written into a log other than as its
// fingerprint: the end of its authorized_keys form, past what every key of
// its type shares; and for an ed25519 key, its 32 bytes in base64 and hex.
func keyText(key ssh.PublicKey) []string {
	wire := base64.StdEncoding.EncodeToString(key.Marshal())
	text := []string{wire[len(wire)-40:]}
	if k, ok := key.(ssh.CryptoPublicKey); ok {
		if ed, ok := k.CryptoPublicKey().(ed25519.PublicKey); ok {
			text = append(text, base64.RawStdEncoding.EncodeToString(ed), base64.RawURLEncoding.EncodeToString(ed), hex.EncodeToString(ed))
		}
	}
	return text
}

// leaks returns each line of logs that holds a needle: its test, the longest
// needle in it, and the line.
func leaks(logs []capturedLog, needles []string) []string {
	var found []string
	for _, l := range logs {
		text := l.log.String()
		var in []string // the needles somewhere in this log
		for _, n := range needles {
			if strings.Contains(text, n) {
				in = append(in, n)
			}
		}
		if len(in) == 0 {
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			longest := ""
			for _, n := range in {
				if len(n) > len(longest) && strings.Contains(line, n) {
					longest = n
				}
			}
			if longest != "" {
				found = append(found, fmt.Sprintf("%s: the log carries %q: %s", l.test, longest, line))
			}
		}
	}
	return found
}

// TestLogsCarryNoAgentText checks parent 11.5 and 3.7: no prompt text, agent
// name or title, and no key, reaches the bridge's log; it logs pane ids,
// verbs, results, codes and fingerprints. TestMain makes the check for the
// whole suite once every test has run: it greps every log the suite captured
// for every needle. This test takes one bridge down the paths it logs, with a
// blocked agent's prompt on screen and its name in herdr's own error, and
// checks what TestMain's check rests on: this log is in the suite's record;
// the needles catch a log line that carries the agent's name, its title, its
// prompt's command (raw, or as a card shows it) or the device's key; and none
// of them is in this log.
func TestLogsCarryNoAgentText(t *testing.T) {
	m, err := captures.Load("claude")
	if err != nil {
		t.Fatal(err)
	}
	var c captures.Capture
	for _, c = range m.Captures {
		if c.Captured && c.PromptType == "shell" {
			break
		}
	}
	agent, err := herdrtest.Agent(filepath.Join(m.Dir, c.Agent))
	if err != nil {
		t.Fatal(err)
	}
	agent.Name = str("ledger-migration")
	screen, err := m.Read(c.File)
	if err != nil {
		t.Fatal(err)
	}
	if agent.TitleStripped == nil || c.Command == "" || !strings.Contains(string(screen), c.Command) {
		t.Fatalf("%s: want a title, and the command %q on screen", c.File, c.Command)
	}
	b := startBridge(t, []herdr.AgentInfo{agent})
	b.herdr.SetDetection(agent.PaneID, string(screen))
	key := b.pair("pager-01")
	dev := b.connectKey("pager-01", key)
	recv[*protocol.Snap](t, dev)

	// The device asks for the card, sends a message of an unknown type that
	// quotes the command, and goes over the rate limit.
	dev.Send(`{"t":"show","id":1,"pane":"` + agent.PaneID + `","pg":-1}`)
	for !answers(recvAny(t, dev), 1) {
	}
	quoted, _ := json.Marshal(map[string]string{"t": "send_text", "text": c.Command, "name": *agent.Name})
	dev.Send(string(quoted))
	for id := 2; id <= 2+session.RateLimit; id++ {
		dev.Send(fmt.Sprintf(`{"t":"ping","id":%d}`, id))
	}
	if !b.waitLog("rate limit") {
		t.Errorf("the session didn't log the rate limit:\n%s", b.log)
	}
	// An unpaired key is refused.
	if _, err := dialRaw(t, b, "pager", "", ssh.PublicKeys(newKey(t))); err == nil {
		t.Error("an unpaired key authenticated")
	}
	// herdr answers an error that names the agent, then answers again.
	b.herdr.SetListReply(`{"id":"pager-bridge:agent.list","error":{"code":"server_unavailable","message":"agent ` + *agent.Name + ` is busy"}}`)
	b.tick()
	if !b.waitLog("code=server_unavailable") {
		t.Errorf("herdr's error isn't logged by its code:\n%s", b.log)
	}
	b.herdr.SetListReply("")
	b.tick()
	if !b.waitLog("herdr: back") {
		t.Errorf("herdr's return isn't logged:\n%s", b.log)
	}

	// What TestMain's check rests on.
	recorded := false
	for _, l := range suiteLogs() {
		recorded = recorded || l.log == b.log
	}
	if !recorded {
		t.Error("this test's bridge log isn't in the suite's record")
	}
	all := needles()
	for _, carried := range []struct{ what, text string }{
		{"the agent's name", *agent.Name},
		{"its title", *agent.TitleStripped},
		{"its prompt's command", c.Command},
		{"its prompt's command as a card shows it", sanitize.Text(c.Command, sanitize.Line)},
		{"the device's key", strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key.PublicKey())))},
	} {
		if carried.text == "" {
			t.Logf("%s: none until lane cards' sanitizer is built", carried.what)
			continue
		}
		synthetic := &syncBuffer{}
		slog.New(slog.NewTextHandler(synthetic, nil)).Info("session: shown", "pane", agent.PaneID, "text", carried.text)
		if len(leaks([]capturedLog{{"synthetic", synthetic}}, all)) == 0 {
			t.Errorf("a log line carrying %s isn't caught: %s", carried.what, synthetic)
		}
	}
	for _, leak := range leaks([]capturedLog{{t.Name(), b.log}}, all) {
		t.Error(leak)
	}
}

// recvAny returns the device's next line from the bridge, parsed.
func recvAny(t *testing.T, d *devsim.Device) protocol.Message {
	t.Helper()
	line, err := d.Recv(5 * time.Second)
	if err != nil {
		t.Fatalf("waiting for a reply: %v", err)
	}
	m, err := protocol.ParseBridge([]byte(line))
	if err != nil {
		t.Fatalf("the bridge sent %s, which breaks the protocol: %v", line, err)
	}
	return m
}
