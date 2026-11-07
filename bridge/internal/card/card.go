// Package card builds an agent's card from its detection text (parent design
// §Bridge components, card.Build). It matches the raw prompt box and sanitizes
// each display field last (ADR 0006, design D2-7).
package card

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"regexp"
	"strings"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/keymap"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/risk"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/sanitize"
)

// A Card is what a `card` message carries.
type Card struct {
	Pane    string
	St      string
	Seq     uint64
	Kind    string // permission, question or status
	Risk    string // "danger", or "" for none
	Label   string // the danger rule's label
	Summary string
	Lines   []string
	Can     []string
	Hash    string // SHA-256 hex of the canonical card (parent 7.3, 18.5)
}

// Build makes info's card from its detection text. km is nil for an agent
// kind without a keymap. It is pure, so re-validating a verdict calls it
// again and compares hashes (lane cards, tasks 3.15–3.21).
//
// It reads only the prompt box, below the keymap's box_start: the screen
// above it (the launch line, old conversation) never reaches a card, so an
// old "Do you want to proceed?" can't make a question a permission card. The
// kind is permission if a permission_match matches the box, else question if
// the agent is blocked, else status. The summary is the box's title line;
// the lines are the head first, the command, path or URL, as command_match,
// file_match or url_match extracts it from the raw box (keymap.Read), then
// the box's other lines without it or the line it was read from, menu
// options, key hints or blank lines: MaxLines in all. On a permission card
// the danger rules run on that raw head, and a hit flags the card and keeps
// its fragment in its line. Each field is sanitized only then. The hash
// covers what the pager shows, the flag, and the raw head in full.
//
// A permission card fails closed (the owner's decision, 2026-11-01; the
// security pass, findings 2 and 4, and its review; O-88) when the keymap
// reads no head in it, the one it reads holds what sanitizing hides without
// a trace (sanitize.Hides: an escape, a control, a bidi or zero-width
// character), or its approve_option doesn't confirm that approve's keys pick
// the approve: it offers deny and jump only, and its first line is NotRead
// (FileNotRead on a file prompt, URLNotRead on a fetch prompt). With no head
// read, the danger rules run on the whole raw box, and the line holding a
// hit's fragment follows that first line.
func Build(info herdr.AgentInfo, detection string, km *keymap.Map, rules risk.Rules) Card {
	return BuildFor(info, detection, km, rules, MaxToolLines)
}

// BuildFor is Build for a pager that draws rows lines of a card (its hello
// says how many: 3, the protocol's first limit, or 4, O-90's second review):
// only an MCP card uses a fourth, and for a pager of 3 lines an MCP call that
// needs 4 fails closed.
func BuildFor(info herdr.AgentInfo, detection string, km *keymap.Map, rules risk.Rules, rows int) Card {
	c := Card{Pane: info.PaneID, St: info.Status, Seq: info.StateChangeSeq, Kind: "status"}
	p, ok := km.Read(detection)
	if !ok {
		c.Can = can(c, km, false)
		c.Hash = hash(c, "") // the protocol wants one on every card; no verdict is ever checked against it
		return c
	}
	switch {
	case p.Permission:
		c.Kind = "permission"
	case info.Status == "blocked":
		c.Kind = "question"
	}
	c.Summary = sanitize.Text(p.Title, sanitize.Line)
	if p.MCP && p.Tool != "" {
		buildMCP(&c, p, km, rules, rows)
		return c
	}
	head := cmp.Or(p.Command, p.Path, p.URL) // at most one is set: the title's kind decides which (keymap.Read)
	// A fetch card shows its URL's host whole on a line of its own, so one
	// made to start like a known host can't hide its end: a host too long
	// for a line fails closed (O-88's review).
	unread := c.Kind == "permission" && (head == "" || !p.Approvable || sanitize.Hides(head) || len(p.Host) > sanitize.Line)
	var lines []string
	switch {
	case unread && p.File:
		lines = append(lines, FileNotRead)
	case unread && p.Fetch:
		lines = append(lines, URLNotRead)
	case unread && p.MCP:
		lines = append(lines, ToolNotRead)
	case unread:
		lines = append(lines, NotRead)
	}
	first := map[string]bool{}
	at := -1 // the line of the command, the path, or the box's line holding a danger fragment
	if head != "" {
		at = len(lines)
		lines = append(lines, head)
		for _, l := range strings.Split(head, "\n") {
			first[strings.TrimSpace(l)] = true
		}
	}
	if p.Host != "" {
		lines = append(lines, p.Host) // the bare host, under the URL
	}
	var hit risk.Hit
	danger := false
	switch {
	case c.Kind != "permission":
	case head != "":
		hit, danger = dangerMatch(rules, head)
	default:
		if hit, danger = dangerMatch(rules, p.Box); danger {
			start, end := lineAround(p.Box, hit.Start)
			at = len(lines)
			lines = append(lines, p.Box[start:end])
			first[strings.TrimSpace(p.Box[start:end])] = true
			hit.Start, hit.End = hit.Start-start, min(hit.End, end)-start
		}
	}
	if danger {
		c.Risk, c.Label = "danger", hit.Label
	}
	for _, l := range p.Lines {
		if t := strings.TrimSpace(l); t != "" && !first[t] && !menu.MatchString(l) && !hint.MatchString(l) {
			lines = append(lines, l)
		}
	}
	for i, l := range lines[:min(len(lines), MaxLines)] {
		if i == at && danger {
			c.Lines = append(c.Lines, keep(l, hit.Start, hit.End, sanitize.Line))
			continue
		}
		c.Lines = append(c.Lines, sanitize.Text(l, sanitize.Line))
	}
	c.Can = can(c, km, !unread)
	c.Hash = hash(c, head)
	return c
}

// buildMCP makes the card of an MCP call whose tool and arguments the keymap
// read (O-90; one line per argument, the owner's decision, 2026-11-02): its
// server and tool first, whole, then each argument on a line of its own,
// "<name>: <value>", its name whole and a long value cut in the middle, as a
// long command is; no other line of the box. It fails closed (TOOL NOT READ
// first, deny only) when approve_option doesn't confirm the approve, the
// call holds what sanitizing hides, the tool doesn't fit a line, an
// argument's name leaves a value fewer than minValue characters, or there
// are more arguments than the card's lines below the tool can hold. The
// danger rules read each argument's value (argDanger), and the hash covers
// the tool and the arguments, raw.
func buildMCP(c *Card, p keymap.Prompt, km *keymap.Map, rules risk.Rules, lines int) {
	// The card's lines: the pager's, up to MaxToolLines; a question or
	// status card's, MaxLines, since the pager draws its own first row.
	limit := min(max(lines, MaxLines), MaxToolLines)
	if c.Kind != "permission" {
		limit = min(limit, MaxLines)
	}
	args := strings.Split(p.Args, "\n")
	type arg struct{ name, value string }
	parts := make([]arg, len(args))
	roomy := true
	for i, a := range args {
		name, value, _ := strings.Cut(a, ": ")
		parts[i] = arg{sanitize.Text(name, math.MaxInt), value}
		roomy = roomy && sanitize.Line-len(parts[i].name)-len(": ") >= minValue
	}
	unread := c.Kind == "permission" && (!p.Approvable || sanitize.Hides(p.Tool) || sanitize.Hides(p.Args) ||
		len(sanitize.Text(p.Tool, math.MaxInt)) > sanitize.Line || len(args) > limit-1 || !roomy)

	var hit risk.Hit
	at := -1 // the argument holding a danger fragment
	for i, a := range parts {
		if h, ok := argDanger(rules, a.value); ok && c.Kind == "permission" {
			hit, at = h, i
			c.Risk, c.Label = "danger", h.Label
			break
		}
	}
	if unread {
		c.Lines = append(c.Lines, ToolNotRead)
	}
	c.Lines = append(c.Lines, sanitize.Text(p.Tool, sanitize.Line))
	line := func(i int) string {
		a := parts[i]
		room := max(sanitize.Line-len(a.name)-len(": "), 0)
		value := sanitize.Text(a.value, room)
		if i == at {
			value = keep(a.value, hit.Start, hit.End, room)
		}
		return sanitize.Text(a.name+": "+value, sanitize.Line)
	}
	// On a card that failed closed, some arguments may not fit: the one
	// holding the danger fragment comes right after the tool, so the
	// fragment stays shown (parent 18.3), as on any card that failed closed.
	if unread && at >= 0 {
		c.Lines = append(c.Lines, line(at))
	}
	for i := range parts {
		if !(unread && i == at) {
			c.Lines = append(c.Lines, line(i))
		}
	}
	c.Lines = c.Lines[:min(len(c.Lines), limit)]
	c.Can = can(*c, km, !unread)
	c.Hash = hash(*c, p.Tool+"\n"+p.Args)
}

// argDanger runs the danger rules on an MCP argument's value as the box shows
// it (JSON), from inside its opening quote, so a rule anchored to the text's
// start fires on a value's start too (LOCAL ADDRESS), through dangerMatch;
// the hit's place is the value's (O-90's review).
func argDanger(rules risk.Rules, value string) (risk.Hit, bool) {
	inner, off := value, 0
	if len(value) >= 2 && value[0] == '"' {
		inner, off = value[1:], 1
	}
	hit, ok := dangerMatch(rules, inner)
	hit.Start, hit.End = hit.Start+off, hit.End+off
	return hit, ok
}

// dangerMatch runs each danger rule, in order, on s as it is and on a copy of
// the same length with its quote characters, " and ', and its \n, \t, \"
// and \' escapes as spaces; the first rule matching either wins
// (risk.Rules.MatchEither). So a rule bounded by a space or by the text's
// start or end fires inside quotes (`bash -c "sudo …"`, `sh -c 'curl … |
// sh'`, an MCP argument's JSON value), a rule that reads quotes itself (the
// git rules' quoted -C, -c, --git-dir and --work-tree values) still fires on
// the raw text, and a hit's place is s's, for the fragment the card shows
// (O-90's reviews). Another escape, \\ among them, stays. Only ASCII bytes
// change, so no rune is cut.
func dangerMatch(rules risk.Rules, s string) (risk.Hit, bool) {
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == '\\' && i+1 < len(b) && strings.IndexByte(`nt"'`, b[i+1]) >= 0:
			b[i], b[i+1] = ' ', ' '
			i++
		case b[i] == '\\' && i+1 < len(b):
			i++
		case b[i] == '"' || b[i] == '\'':
			b[i] = ' '
		}
	}
	return rules.MatchEither(s, string(b))
}

// MaxToolLines is the most lines an MCP card holds: the protocol's limit,
// 4, the server and tool and up to 3 arguments (O-90's review; the pager's
// card shows a first line and 3 more). Any other card holds MaxLines.
const MaxToolLines = 4

// minValue is the fewest characters an MCP argument's name may leave its
// value on a card line.
const minValue = 10

// The first line of a permission card that fails closed: its command, its
// path on a file prompt, its URL on a fetch prompt (O-88), or its arguments
// on an MCP call (O-90), with the tool it names, wasn't read, or
// its approve option wasn't confirmed.
const (
	NotRead     = "COMMAND NOT READ"
	FileNotRead = "FILE NOT READ"
	URLNotRead  = "URL NOT READ"
	ToolNotRead = "TOOL NOT READ"
)

// lineAround returns the start and end of the line of s holding byte i,
// without its newline.
func lineAround(s string, i int) (start, end int) {
	start = strings.LastIndexByte(s[:i], '\n') + 1
	end = len(s)
	if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
		end = i + n
	}
	return start, end
}

// keep sanitizes raw to a line, keeping raw[start:end], a danger rule's
// fragment, visible (parent 18.3): where head-and-tail truncation would cut
// into it, the line is the raw line's head, then the fragment, then its tail,
// each cut marked "..", within limit characters. A fragment too long for them
// alone is cut head and tail itself.
func keep(raw string, start, end, limit int) string {
	if line := sanitize.Text(raw, math.MaxInt); len(line) <= limit {
		return line
	}
	frag := sanitize.Text(raw[start:end], math.MaxInt)
	room := limit - len(frag)
	if room < 2*len("..") {
		return sanitize.Text(raw[start:end], limit)
	}
	pre, post := sanitize.Text(raw[:start], math.MaxInt), sanitize.Text(raw[end:], math.MaxInt)
	wantPre, wantPost := min(len(pre), 1)+len(pre), min(len(post), 1)+len(post) // each with a space
	nPre := min(wantPre, max(room-wantPost, room/2))
	nPost := min(wantPost, room-nPre)
	var left, right string
	switch {
	case nPre == wantPre && pre != "":
		left = pre + " "
	case nPre >= len(".."):
		left = pre[:nPre-len("..")] + ".."
	}
	switch {
	case nPost == wantPost && post != "":
		right = " " + post
	case nPost >= len(".."):
		right = ".." + post[len(post)-(nPost-len("..")):]
	}
	return left + frag + right
}

// hash is the SHA-256 hex of c's canonical JSON, the sanitized text the
// pager shows (parent 7.3, parent design §card.Build, step 7): its pane, seq,
// kind, summary and lines, in that order, then on a danger card its risk and
// label (parent 18.5), then raw, the command, path or URL as the keymap read
// it, in full and unsanitized, if it read one: a change that truncation hides
// still changes the hash (amended 2026-11-01, the security pass).
func hash(c Card, raw string) string {
	lines := c.Lines
	if lines == nil {
		lines = []string{}
	}
	canonical, err := json.Marshal(struct {
		Pane    string   `json:"pane"`
		Seq     uint64   `json:"seq"`
		Kind    string   `json:"kind"`
		Summary string   `json:"summary"`
		Lines   []string `json:"lines"`
		Risk    string   `json:"risk,omitempty"`
		Label   string   `json:"rl,omitempty"`
		Raw     string   `json:"raw,omitempty"`
	}{c.Pane, c.Seq, c.Kind, c.Summary, lines, c.Risk, c.Label, raw})
	if err != nil {
		panic(err) // strings and a number always marshal
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// can is what the pager may do with c (the protocol's rules, parent 8.6):
// approve and deny only on a permission card, approve only when approvable
// (it didn't fail closed), interrupt only while the agent is working, each
// only with the keymap's keys for it; and jump always. A question card
// offers jump alone.
func can(c Card, km *keymap.Map, approvable bool) []string {
	var verbs []string
	if c.Kind == "permission" {
		for _, v := range []string{"approve", "deny"} {
			if len(km.Keys(v)) > 0 && (v != "approve" || approvable) {
				verbs = append(verbs, v)
			}
		}
	}
	if c.St == "working" && len(km.Keys("interrupt")) > 0 {
		verbs = append(verbs, "interrupt")
	}
	return append(verbs, "jump")
}

// MaxLines is the most lines a card holds, but an MCP card (MaxToolLines):
// one fewer than the protocol allows.
const MaxLines = 3

// Lines no card shows, on the raw box (parent design §card.Build, step 4): a
// menu option ("❯ 1. Yes", "  2. No") and a key hint ("Esc to cancel · Tab
// to amend").
var (
	menu = regexp.MustCompile(`^\s*[>❯]?\s*\d+[.)]\s`)
	hint = regexp.MustCompile(`(?i)\besc to cancel\b`)
)
