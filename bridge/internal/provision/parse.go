package provision

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	versionRE = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,32}$`)
	nameRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,23}$`)
	hostRE    = regexp.MustCompile(`^[A-Za-z0-9.:-]{1,64}$`)
	hexKeyRE  = regexp.MustCompile(`^[0-9A-Fa-f]{64}$`)
	integerRE = regexp.MustCompile(`^(0|-?[1-9][0-9]*)$`)

	resultErrs = []string{"cancelled", "timeout", "invalid", "busy", "storage"}

	// Key types named in an error, so the owner sees which key the device
	// sent; any other type text is device-supplied and never printed.
	knownKeyTypes = []string{
		"ssh-rsa", "ssh-dss", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
		"sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com",
	}
)

// ParseDevice parses one provisioning line from the device, without its '\n'.
func ParseDevice(line []byte) (Message, error) {
	p, o, t, err := open(line)
	if err != nil {
		return nil, err
	}
	var m Message
	switch t {
	case "prov_hello":
		m = &Hello{FW: p.pattern(o, "fw", versionRE), PubKey: p.key(o, "pubkey"), State: p.oneOf(o, "state", "waiting", "confirming", "confirmed")}
	case "prov_result":
		r := &Result{OK: p.boolean(o, "ok")}
		_, hasErr := o.keys["err"]
		switch {
		case p.err != nil:
		case r.OK && hasErr:
			p.fail(o, "err", "set on a success")
		case !r.OK:
			r.Err = p.oneOf(o, "err", resultErrs...)
		}
		m = r
	default:
		return nil, &Error{"t", "not a line the device sends"}
	}
	return p.done(m)
}

// ParseTool parses one provisioning line from the tool, without its '\n'.
func ParseTool(line []byte) (Message, error) {
	p, o, t, err := open(line)
	if err != nil {
		return nil, err
	}
	switch t {
	case "prov_offer":
		return p.done(p.offer(o))
	case "prov_set":
		return p.done(p.set(o))
	}
	return nil, &Error{"t", "not a line the tool sends"}
}

// offer reads a prov_offer: everything but the networks.
func (p *parser) offer(o object) *HostOffer {
	h := &HostOffer{Name: p.pattern(o, "name", nameRE)}
	h.Host = p.pattern(o, "host", hostRE)
	h.Port = int(p.integer(o, "port", 1, 65535))
	h.HostKey = p.key(o, "host_key")
	h.HostName = p.str(o, "host_name")
	if p.err == nil && (len(h.HostName) < 1 || len(h.HostName) > MaxHost) {
		p.fail(o, "host_name", fmt.Sprintf("%d bytes; 1 to %d allowed", len(h.HostName), MaxHost))
	}
	return h
}

// set reads a prov_set: the confirmed offer's host key and the networks.
func (p *parser) set(o object) *Set {
	s := &Set{HostKey: p.key(o, "host_key")}
	nets := p.array(o, "wifi")
	if p.err == nil && (len(nets) < 1 || len(nets) > MaxWiFi) {
		p.fail(o, "wifi", fmt.Sprintf("%d networks; 1 to %d allowed", len(nets), MaxWiFi))
	}
	for i, raw := range nets {
		n := p.object(fmt.Sprintf("wifi[%d]", i), raw)
		w := Network{SSID: p.str(n, "ssid"), Pass: p.str(n, "pass")}
		if p.err == nil && (len(w.SSID) < 1 || len(w.SSID) > MaxSSID) {
			p.fail(n, "ssid", fmt.Sprintf("%d bytes; 1 to %d allowed", len(w.SSID), MaxSSID))
		}
		if reason := passProblem(w.Pass); p.err == nil && reason != "" {
			p.fail(n, "pass", reason)
		}
		s.WiFi = append(s.WiFi, w)
	}
	return s
}

// passProblem says why p isn't an open network's empty password, a WPA
// passphrase (8 to 63 printable ASCII characters) or a 64-hex-digit key. It
// never quotes p.
func passProblem(p string) string {
	switch {
	case p == "" || hexKeyRE.MatchString(p):
		return ""
	case len(p) < 8 || len(p) > 63:
		return "a passphrase is 8 to 63 characters, or 64 hex digits"
	case strings.IndexFunc(p, func(r rune) bool { return r < 0x20 || r > 0x7e }) >= 0:
		return "a passphrase is printable ASCII"
	}
	return ""
}

// keyProblem says why s isn't exactly "ssh-ed25519 <base64>", the base64
// holding the type and a 32-byte key and nothing more (design D4).
func keyProblem(s string) string {
	typ, b64, ok := strings.Cut(s, " ")
	switch {
	case !ok:
		return `not "ssh-ed25519 <base64>"`
	case typ != KeyType && slices.Contains(knownKeyTypes, typ):
		return "an " + typ + " key; only ssh-ed25519 is accepted"
	case typ != KeyType:
		return "not an ssh-ed25519 key; only ssh-ed25519 is accepted"
	case strings.Contains(b64, " "):
		return "a comment follows the key"
	}
	blob, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "the key isn't base64"
	}
	name, rest, ok := sshString(blob)
	if !ok || string(name) != KeyType {
		return "the key's data isn't an ssh-ed25519 key"
	}
	key, rest, ok := sshString(rest)
	if !ok || len(key) != keyBytes || len(rest) != 0 {
		return "the key isn't 32 bytes"
	}
	return ""
}

// sshString splits an SSH wire-format string (a 4-byte length, then that
// many bytes) off the front of b.
func sshString(b []byte) (s, rest []byte, ok bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	n := binary.BigEndian.Uint32(b)
	if uint64(n) > uint64(len(b)-4) {
		return nil, nil, false
	}
	return b[4 : 4+n], b[4+n:], true
}

// A parser reads the keys of one line. The first problem sticks: later reads
// return zero values, and done reports it.
type parser struct{ err error }

// An object is one decoded JSON object: its keys' raw values, and its path
// in the line ("" for the message itself, "wifi[0]" for a network).
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
func open(line []byte) (*parser, object, string, error) {
	if len(line) > MaxLine {
		return nil, object{}, "", &Error{"line", fmt.Sprintf("%d bytes; at most %d", len(line), MaxLine)}
	}
	body, ok := bytes.CutPrefix(line, []byte(Prefix))
	if !ok {
		return nil, object{}, "", &Error{"prefix", "not a provisioning line"}
	}
	if !json.Valid(body) {
		return nil, object{}, "", &Error{"json", "not one JSON value"}
	}
	p := &parser{}
	o := p.object("", body)
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

func (p *parser) pattern(o object, key string, re *regexp.Regexp) string {
	s := p.str(o, key)
	if p.err == nil && !re.MatchString(s) {
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

func (p *parser) key(o object, key string) string {
	s := p.str(o, key)
	if reason := keyProblem(s); p.err == nil && reason != "" {
		p.fail(o, key, reason)
	}
	return s
}

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
