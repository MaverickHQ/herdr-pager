package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

var (
	paneRE    = regexp.MustCompile(`^w[0-9A-Za-z]+:p[0-9A-Za-z]+$`) // herdr counts past 9 with letters: w6:pE
	hashRE    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	versionRE = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,32}$`)
	nameRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,23}$`)
	integerRE = regexp.MustCompile(`^(0|-?[1-9][0-9]*)$`)

	statuses   = []string{"idle", "working", "blocked", "done", "unknown"}
	verbs      = []string{"show", "approve", "deny", "interrupt", "jump"}
	resultErrs = []string{"stale", "not_allowed", "unknown_pane", "herdr_offline", "herdr_error", "bad_request", "rate_limited"}
)

// ParseDevice parses one line from the device, without its '\n'.
func ParseDevice(line []byte) (Message, error) {
	p, o, t, err := open(line, MaxDeviceLine)
	if err != nil {
		return nil, err
	}
	var m Message
	switch t {
	case "hello":
		p.version(o)
		lines := int64(MinCardLines)
		if _, ok := o.keys["lines"]; ok {
			lines = p.integer(o, "lines", MinCardLines, MaxCardLines)
		}
		m = &DeviceHello{FW: p.pattern(o, "fw", versionRE, 32), Name: p.pattern(o, "name", nameRE, MaxNameLen), Lines: int(lines)}
	case "ping":
		m = &Ping{ID: p.id(o)}
	case "list":
		m = &List{ID: p.id(o)}
	case "show":
		m = &Show{ID: p.id(o), Pane: p.pane(o), PG: p.integer(o, "pg", -1, MaxSafeInt)}
	case "approve":
		m = &Approve{ID: p.id(o), Pane: p.pane(o), Seq: p.seq(o, "seq"), Hash: p.hash(o)}
	case "deny":
		m = &Deny{ID: p.id(o), Pane: p.pane(o), Seq: p.seq(o, "seq"), Hash: p.hash(o)}
	case "interrupt":
		m = &Interrupt{ID: p.id(o), Pane: p.pane(o), Seq: p.seq(o, "seq")}
	case "jump":
		m = &Jump{ID: p.id(o), Pane: p.pane(o)}
	default:
		return nil, &Error{"t", "not a message the device sends"}
	}
	return p.done(m)
}

// ParseBridge parses one line from the bridge, without its '\n'.
func ParseBridge(line []byte) (Message, error) {
	p, o, t, err := open(line, MaxBridgeLine)
	if err != nil {
		return nil, err
	}
	var m Message
	switch t {
	case "hello":
		p.version(o)
		m = &BridgeHello{Bridge: p.pattern(o, "bridge", versionRE, 32), Host: p.text(o, "host", 1, MaxHostLen), Now: p.time(o, "now")}
	case "snap":
		m = p.snap(o)
	case "card":
		m = p.card(o)
	case "result":
		m = p.result(o)
	case "pong":
		m = &Pong{ID: p.id(o), Now: p.time(o, "now")}
	default:
		return nil, &Error{"t", "not a message the bridge sends"}
	}
	return p.done(m)
}

func (p *parser) snap(o object) *Snap {
	s := &Snap{Now: p.time(o, "now"), Herdr: p.oneOf(o, "herdr", "ok", "offline"), More: int(p.integer(o, "more", 0, MaxSafeInt))}
	items := p.array(o, "agents")
	switch {
	case p.err != nil:
		return s
	case len(items) > MaxAgents:
		p.fail(o, "agents", fmt.Sprintf("%d agents; at most %d", len(items), MaxAgents))
	case s.Herdr == "offline" && len(items) > 0:
		p.fail(o, "agents", "agents while herdr is offline")
	case s.More > 0 && len(items) < MaxAgents:
		p.fail(o, "more", fmt.Sprintf("more with %d agents; only a full snapshot has more", len(items)))
	}
	seen := map[string]bool{}
	for i, raw := range items {
		e := p.object(fmt.Sprintf("%s[%d]", o.at("agents"), i), raw)
		a := Agent{
			Pane: p.pane(e), Name: p.text(e, "name", 1, MaxNameLen), Kind: p.text(e, "kind", 1, MaxKindLen),
			St: p.oneOf(e, "st", statuses...), Seq: p.seq(e, "seq"), DSeq: p.seq(e, "dseq"), Since: p.time(e, "since"),
			Can: p.verbs(e),
		}
		if _, ok := e.keys["at"]; ok {
			a.At = p.text(e, "at", 0, MaxAtLen)
		}
		if p.err != nil {
			break
		}
		if seen[a.Pane] {
			p.fail(e, "pane", "appears twice in one snapshot")
		}
		seen[a.Pane] = true
		if slices.Contains(a.Can, "approve") || slices.Contains(a.Can, "deny") {
			p.fail(e, "can", "a snapshot never offers approve or deny")
		}
		if slices.Contains(a.Can, "interrupt") && a.St != "working" {
			p.fail(e, "can", "interrupt only for a working agent")
		}
		s.Agents = append(s.Agents, a)
	}
	return s
}

func (p *parser) card(o object) *Card {
	c := &Card{
		ID: p.id(o), Pane: p.pane(o), St: p.oneOf(o, "st", statuses...), Seq: p.seq(o, "seq"), Hash: p.hash(o),
		Kind: p.oneOf(o, "kind", "permission", "question", "status"), Summary: p.text(o, "summary", 0, MaxTextLen),
	}
	c.Lines = p.strings(o, "lines")
	if p.err == nil && len(c.Lines) > MaxCardLines {
		p.fail(o, "lines", fmt.Sprintf("%d lines; at most %d", len(c.Lines), MaxCardLines))
	}
	for i, l := range c.Lines {
		if len(l) > MaxTextLen {
			p.failAt(fmt.Sprintf("%s[%d]", o.at("lines"), i), fmt.Sprintf("%d bytes; at most %d", len(l), MaxTextLen))
		}
	}
	_, risk := o.keys["risk"]
	_, rl := o.keys["rl"]
	switch {
	case risk && !rl:
		p.fail(o, "rl", "missing on a danger card")
	case rl && !risk:
		p.fail(o, "risk", "missing beside rl")
	case risk:
		c.Risk = p.oneOf(o, "risk", "danger")
		c.RL = p.text(o, "rl", 1, MaxLabelLen)
	}
	c.Can = p.verbs(o)
	if p.err != nil {
		return c
	}
	switch {
	case c.Kind != "permission" && (slices.Contains(c.Can, "approve") || slices.Contains(c.Can, "deny")):
		p.fail(o, "can", "approve and deny only on a permission card")
	case slices.Contains(c.Can, "interrupt") && c.St != "working":
		p.fail(o, "can", "interrupt only for a working agent")
	case c.Kind == "question" && !slices.Equal(c.Can, []string{"jump"}):
		p.fail(o, "can", "a question card offers only jump")
	}
	return c
}

func (p *parser) result(o object) *Result {
	r := &Result{ID: p.id(o), OK: p.boolean(o, "ok")}
	_, hasErr := o.keys["err"]
	switch {
	case p.err != nil:
	case r.OK && hasErr:
		p.fail(o, "err", "set on a success")
	case !r.OK:
		r.Err = p.oneOf(o, "err", resultErrs...)
	}
	return r
}

// A parser reads the keys of one line. The first problem sticks: later reads
// return zero values, and done reports it.
type parser struct{ err error }

// An object is one decoded JSON object: its keys' raw values, and its path
// in the line ("" for the message itself, "agents[3]" for an entry).
type object struct {
	path string
	keys map[string]json.RawMessage
}

func (o object) at(key string) string {
	if o.path == "" {
		return key
	}
	return o.path + "." + key
}

// open checks a line's framing and returns its message object and "t".
func open(line []byte, limit int) (*parser, object, string, error) {
	if len(line) > limit {
		return nil, object{}, "", &Error{"line", fmt.Sprintf("%d bytes; at most %d", len(line), limit)}
	}
	if !json.Valid(line) {
		return nil, object{}, "", &Error{"json", "not one JSON value"}
	}
	p := &parser{}
	o := p.object("", line)
	t := p.str(o, "t")
	if p.err != nil {
		return nil, object{}, "", p.err
	}
	return p, o, t, nil
}

func (p *parser) done(m Message) (Message, error) {
	if p.err != nil {
		return nil, p.err
	}
	return m, nil
}

func (p *parser) failAt(path, reason string) {
	if p.err == nil {
		p.err = &Error{path, reason}
	}
}

func (p *parser) fail(o object, key, reason string) { p.failAt(o.at(key), reason) }

// object decodes raw as an object at path, rejecting a key that appears twice.
func (p *parser) object(path string, raw []byte) object {
	o := object{path: path, keys: map[string]json.RawMessage{}}
	where := path
	if where == "" {
		where = "json"
	}
	if p.err != nil {
		return o
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		p.failAt(where, "not an object")
		return o
	}
	for dec.More() {
		tok, err := dec.Token()
		key, ok := tok.(string)
		var v json.RawMessage
		if err != nil || !ok || dec.Decode(&v) != nil {
			p.failAt(where, "not valid JSON")
			return o
		}
		if _, dup := o.keys[key]; dup {
			p.fail(o, key, "appears twice")
			return o
		}
		o.keys[key] = bytes.TrimSpace(v)
	}
	return o
}

// value returns a key's raw value, failing if it's missing or null.
func (p *parser) value(o object, key string) []byte {
	if p.err != nil {
		return nil
	}
	v, ok := o.keys[key]
	switch {
	case !ok:
		p.fail(o, key, "missing")
	case string(v) == "null":
		p.fail(o, key, "null")
	default:
		return v
	}
	return nil
}

func (p *parser) str(o object, key string) string {
	v := p.value(o, key)
	if v == nil {
		return ""
	}
	var s string
	if v[0] != '"' || json.Unmarshal(v, &s) != nil {
		p.fail(o, key, "not a string")
	}
	return s
}

// text is a string of min to max bytes. Its content isn't checked: the bridge
// sanitizes display text, and the device filters it again.
func (p *parser) text(o object, key string, min, max int) string {
	s := p.str(o, key)
	if p.err == nil && (len(s) < min || len(s) > max) {
		p.fail(o, key, fmt.Sprintf("%d bytes; %d to %d allowed", len(s), min, max))
	}
	return s
}

func (p *parser) pattern(o object, key string, re *regexp.Regexp, max int) string {
	s := p.str(o, key)
	if p.err == nil && (len(s) > max || !re.MatchString(s)) {
		p.fail(o, key, "not in its allowed form")
	}
	return s
}

func (p *parser) oneOf(o object, key string, values ...string) string {
	s := p.str(o, key)
	if p.err == nil && !slices.Contains(values, s) {
		p.fail(o, key, "not one of its allowed values")
	}
	return s
}

func (p *parser) pane(o object) string { return p.pattern(o, "pane", paneRE, MaxPaneLen) }

func (p *parser) hash(o object) string { return p.pattern(o, "hash", hashRE, 64) }

// integer is written as plain decimal digits, within min and max.
func (p *parser) integer(o object, key string, min, max int64) int64 {
	v := p.value(o, key)
	if v == nil {
		return 0
	}
	if !integerRE.Match(v) {
		p.fail(o, key, "not written as an integer")
		return 0
	}
	n, err := strconv.ParseInt(string(v), 10, 64)
	if err != nil || n < min || n > max {
		p.fail(o, key, fmt.Sprintf("outside %d to %d", min, max))
		return 0
	}
	return n
}

func (p *parser) id(o object) uint32 { return uint32(p.integer(o, "id", 0, 1<<32-1)) }

func (p *parser) seq(o object, key string) uint64 { return uint64(p.integer(o, key, 0, MaxSafeInt)) }

func (p *parser) time(o object, key string) int64 { return p.integer(o, key, 0, MaxSafeInt) }

func (p *parser) version(o object) {
	if p.integer(o, "v", Version, Version) != Version {
		p.fail(o, "v", "unsupported version")
	}
}

func (p *parser) boolean(o object, key string) bool {
	v := p.value(o, key)
	switch {
	case v == nil:
	case string(v) == "true":
		return true
	case string(v) != "false":
		p.fail(o, key, "not a boolean")
	}
	return false
}

func (p *parser) array(o object, key string) []json.RawMessage {
	v := p.value(o, key)
	if v == nil {
		return nil
	}
	var items []json.RawMessage
	if v[0] != '[' || json.Unmarshal(v, &items) != nil {
		p.fail(o, key, "not an array")
	}
	return items
}

func (p *parser) strings(o object, key string) []string {
	items := p.array(o, key)
	out := make([]string, 0, len(items))
	for i, it := range items {
		var s string
		if it[0] != '"' || json.Unmarshal(it, &s) != nil {
			p.failAt(fmt.Sprintf("%s[%d]", o.at(key), i), "not a string")
			return nil
		}
		out = append(out, s)
	}
	return out
}

// verbs is an agent's or a card's `can`: verbs only.
func (p *parser) verbs(o object) []string {
	can := p.strings(o, "can")
	for _, v := range can {
		if !slices.Contains(verbs, v) {
			p.fail(o, "can", "holds something that isn't a verb")
		}
	}
	return can
}
