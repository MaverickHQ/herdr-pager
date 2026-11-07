// Package keymap is each agent kind's keymap: the keys for each verb and the
// patterns that read its prompts (parent 23, parent design §Keymaps). The
// defaults are embedded from bridge/keymaps; ~/.config/herdr-pager/keymaps.toml
// may override them.
package keymap

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/MaverickHQ/herdr-pager/bridge/keymaps"
)

// A Map is one agent kind's keymap.
type Map struct {
	Kind            string // AgentInfo.agent, e.g. "claude"
	AgentVersion    string
	Captured        string
	Approve         []string
	Deny            []string
	Interrupt       []string
	PermissionMatch []*regexp.Regexp // matched against the prompt box only
	CommandMatch    *regexp.Regexp   // a shell prompt's command (group 1)
	FileMatch       *regexp.Regexp   // a file prompt's path (its first group that matched)
	FilePrompt      *regexp.Regexp   // matches a file prompt's box: its card names a path
	URLMatch        *regexp.Regexp   // a fetch prompt's host as the prompt names it (group 1) and URL (group 2)
	URLPrompt       *regexp.Regexp   // matches a fetch prompt's box: its card names a URL
	MCPMatch        *regexp.Regexp   // an MCP call's server and tool (group 1) and argument lines (group 2)
	MCPPrompt       *regexp.Regexp   // matches an MCP call's box: its card names the tool and its arguments
	ApproveOption   *regexp.Regexp   // matches the box when the option approve's keys pick is the approve
	QuestionMatch   *regexp.Regexp   // a line asking the prompt's question: approve needs exactly one in the box
	BoxStart        *regexp.Regexp   // the rule above the prompt box
	DropLines       []*regexp.Regexp // tips and rules inside the box
	IgnoreMatch     []*regexp.Regexp // lines that change while blocked
}

// A Set is every kind's keymap, by kind.
type Set map[string]*Map

// For returns the keymap of kind, herdr's AgentInfo.agent, or nil when herdr
// reports no kind or the kind has no keymap (parent 8.6).
func (s Set) For(kind *string) *Map {
	if kind == nil {
		return nil
	}
	return s[*kind]
}

// Load reads the embedded keymaps, then the overrides file at path if there is
// one ("" for none; a missing file is none). A key the overrides file sets
// replaces the embedded one, and a kind it adds is new. An invalid file is an
// error naming the file and the key, and nothing is loaded.
func Load(overrides string) (Set, error) {
	names, err := fs.Glob(keymaps.FS, "*.toml")
	if err != nil {
		return nil, err
	}
	tables := map[string]table{}
	for _, name := range names {
		data, err := keymaps.FS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		ts, err := parse("bridge/keymaps/"+name, data)
		if err != nil {
			return nil, err
		}
		for kind, t := range ts {
			if name != kind+".toml" {
				return nil, fmt.Errorf("keymap: bridge/keymaps/%s defines [%s]: each kind lives in <kind>.toml", name, kind)
			}
			tables[kind] = t
		}
	}
	if overrides != "" {
		data, err := os.ReadFile(overrides)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, fmt.Errorf("keymap: %w", err)
		default:
			ts, err := parse(overrides, data)
			if err != nil {
				return nil, err
			}
			for kind, o := range ts {
				tables[kind] = tables[kind].merge(o)
			}
		}
	}
	set := Set{}
	for kind, t := range tables {
		m, err := build(kind, t)
		if err != nil {
			return nil, fmt.Errorf("keymap: [%s] %w", kind, err)
		}
		set[kind] = m
	}
	return set, nil
}

// table is one [kind] table of a keymap file, as written; a key the file
// doesn't set is nil.
type table struct {
	AgentVersion    *string   `toml:"agent_version"`
	Captured        *string   `toml:"captured"`
	Approve         *[]string `toml:"approve"`
	Deny            *[]string `toml:"deny"`
	Interrupt       *[]string `toml:"interrupt"`
	PermissionMatch *[]string `toml:"permission_match"`
	CommandMatch    *string   `toml:"command_match"`
	FileMatch       *string   `toml:"file_match"`
	FilePrompt      *string   `toml:"file_prompt"`
	URLMatch        *string   `toml:"url_match"`
	URLPrompt       *string   `toml:"url_prompt"`
	MCPMatch        *string   `toml:"mcp_match"`
	MCPPrompt       *string   `toml:"mcp_prompt"`
	ApproveOption   *string   `toml:"approve_option"`
	QuestionMatch   *string   `toml:"question_match"`
	BoxStart        *string   `toml:"box_start"`
	DropLines       *[]string `toml:"drop_lines"`
	IgnoreMatch     *[]string `toml:"ignore_match"`
}

// merge returns t with each key o sets replaced by o's.
func (t table) merge(o table) table {
	set := func(dst **string, src *string) {
		if src != nil {
			*dst = src
		}
	}
	setList := func(dst **[]string, src *[]string) {
		if src != nil {
			*dst = src
		}
	}
	set(&t.AgentVersion, o.AgentVersion)
	set(&t.Captured, o.Captured)
	setList(&t.Approve, o.Approve)
	setList(&t.Deny, o.Deny)
	setList(&t.Interrupt, o.Interrupt)
	setList(&t.PermissionMatch, o.PermissionMatch)
	set(&t.CommandMatch, o.CommandMatch)
	set(&t.FileMatch, o.FileMatch)
	set(&t.FilePrompt, o.FilePrompt)
	set(&t.URLMatch, o.URLMatch)
	set(&t.URLPrompt, o.URLPrompt)
	set(&t.MCPMatch, o.MCPMatch)
	set(&t.MCPPrompt, o.MCPPrompt)
	set(&t.ApproveOption, o.ApproveOption)
	set(&t.QuestionMatch, o.QuestionMatch)
	set(&t.BoxStart, o.BoxStart)
	setList(&t.DropLines, o.DropLines)
	setList(&t.IgnoreMatch, o.IgnoreMatch)
	return t
}

// parse reads a keymap file's tables strictly, and checks each one: an
// unknown key, a bad key name or a bad pattern is an error naming the file.
func parse(file string, data []byte) (map[string]table, error) {
	var tables map[string]table
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&tables); err != nil {
		var strict *toml.StrictMissingError
		var de *toml.DecodeError
		switch {
		case errors.As(err, &strict) && len(strict.Errors) > 0:
			row, _ := strict.Errors[0].Position()
			return nil, fmt.Errorf("keymap: %s: line %d: unknown key %s", file, row, strings.Join(strict.Errors[0].Key(), "."))
		case errors.As(err, &de):
			row, _ := de.Position()
			return nil, fmt.Errorf("keymap: %s: line %d: %v", file, row, de)
		}
		return nil, fmt.Errorf("keymap: %s: %w", file, err)
	}
	for kind, t := range tables {
		if _, err := build(kind, t); err != nil {
			return nil, fmt.Errorf("keymap: %s: [%s] %w", file, kind, err)
		}
	}
	return tables, nil
}

// keyName is the keys a keymap may send (parent design §Keymaps).
var keyName = regexp.MustCompile(`^(esc|enter|tab|up|down|left|right|backspace|[0-9]|[a-z]|ctrl\+[a-z])$`)

// build checks a table's keys and compiles its patterns. No pattern may
// match the empty string: it would match any text.
func build(kind string, t table) (*Map, error) {
	var err error
	keys := func(key string, p *[]string) []string {
		if err != nil || p == nil {
			return nil
		}
		for _, k := range *p {
			if !keyName.MatchString(k) {
				err = fmt.Errorf("%s: %q isn't a key: want esc, enter, tab, up, down, left, right, backspace, 0-9, a-z or ctrl+a-z", key, k)
				return nil
			}
		}
		return *p
	}
	compile := func(key, expr string) *regexp.Regexp {
		if err != nil {
			return nil
		}
		re, e := regexp.Compile(expr)
		switch {
		case e != nil:
			err = fmt.Errorf("%s: %w", key, e)
		case re.MatchString(""):
			err = fmt.Errorf("%s: %q matches the empty string, so it would match any text", key, expr)
		}
		return re
	}
	one := func(key string, p *string) *regexp.Regexp {
		if p == nil {
			return nil
		}
		return compile(key, *p)
	}
	list := func(key string, p *[]string) []*regexp.Regexp {
		if p == nil {
			return nil
		}
		var res []*regexp.Regexp
		for _, expr := range *p {
			res = append(res, compile(key, expr))
		}
		return res
	}
	m := &Map{
		Kind:            kind,
		AgentVersion:    deref(t.AgentVersion),
		Captured:        deref(t.Captured),
		Approve:         keys("approve", t.Approve),
		Deny:            keys("deny", t.Deny),
		Interrupt:       keys("interrupt", t.Interrupt),
		PermissionMatch: list("permission_match", t.PermissionMatch),
		CommandMatch:    one("command_match", t.CommandMatch),
		FileMatch:       one("file_match", t.FileMatch),
		FilePrompt:      one("file_prompt", t.FilePrompt),
		URLMatch:        one("url_match", t.URLMatch),
		URLPrompt:       one("url_prompt", t.URLPrompt),
		MCPMatch:        one("mcp_match", t.MCPMatch),
		MCPPrompt:       one("mcp_prompt", t.MCPPrompt),
		ApproveOption:   one("approve_option", t.ApproveOption),
		QuestionMatch:   one("question_match", t.QuestionMatch),
		BoxStart:        one("box_start", t.BoxStart),
		DropLines:       list("drop_lines", t.DropLines),
		IgnoreMatch:     list("ignore_match", t.IgnoreMatch),
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Keys returns the keys for verb: "approve", "deny" or "interrupt". No
// keymap has keys, and a keymap without permission_match has no approve or
// deny keys: it can't tell a permission prompt (parent design §Keymaps).
func (m *Map) Keys(verb string) []string {
	if m == nil {
		return nil
	}
	switch {
	case verb == "approve" && len(m.PermissionMatch) > 0:
		return slices.Clone(m.Approve)
	case verb == "deny" && len(m.PermissionMatch) > 0:
		return slices.Clone(m.Deny)
	case verb == "interrupt":
		return slices.Clone(m.Interrupt)
	}
	return nil
}

// A Prompt is what a keymap reads in an agent's prompt box, from the raw
// detection text (ADR 0006): nothing in it is sanitized yet.
type Prompt struct {
	Title      string   // the box's first line, trimmed: a card's summary
	Lines      []string // the box's other lines, without the lines the head was read from, drop_lines and ignore_match
	Permission bool     // a permission_match pattern matches the box
	Command    string   // command_match's group 1, trimmed, on a prompt that isn't a file or fetch prompt; "" if none
	Path       string   // file_match's first group that matched, trimmed, on a file prompt; "" if none
	URL        string   // url_match's group 2, trimmed, on a fetch prompt, if it's an http or https URL of the host group 1 names (fetchURL); "" if not
	Host       string   // with URL, url_match's group 1: the host the prompt names, the bare host a fetch card shows; "" without URL
	Tool       string   // mcp_match's group 1, trimmed, on an MCP call: the server and the tool; "" if not read
	Args       string   // with Tool, mcp_match's group 2: the argument lines, each trimmed; "" if not read
	Box        string   // the box itself, its title line first, every line raw
	File       bool     // file_prompt matches the box: a prompt about a file, whose head file_match reads
	Fetch      bool     // url_prompt matches the box, and file_prompt doesn't: a prompt about a URL, whose head url_match reads
	MCP        bool     // mcp_prompt matches the box, and neither file_prompt nor url_prompt does: an MCP call, whose tool and arguments mcp_match reads
	Approvable bool     // approve_option matches the box: approve's keys pick the approve; false with no approve_option
}

// Read cuts the prompt box out of detection, the lines below the line where
// box_start's last match begins, and reads it. ok is false when there's no
// box, or no keymap.
func (m *Map) Read(detection string) (p Prompt, ok bool) {
	if m == nil || m.BoxStart == nil {
		return Prompt{}, false
	}
	starts := m.BoxStart.FindAllStringIndex(detection, -1)
	if len(starts) == 0 {
		return Prompt{}, false
	}
	box := detection[starts[len(starts)-1][0]:]
	nl := strings.IndexByte(box, '\n')
	if nl < 0 {
		return Prompt{}, false
	}
	box = box[nl+1:]
	p.Permission = anyMatch(m.PermissionMatch, box)
	p.Box = box
	// The kind first, from the box's title line; then only that kind's
	// pattern reads the head, so a file's content can't stand in for a
	// command, nor a command's description for a path (the security pass's
	// review, 2026-11-01), nor any of them for a URL (O-88) or an MCP call (O-90).
	p.File = m.FilePrompt != nil && m.FilePrompt.MatchString(box)
	p.Fetch = !p.File && m.URLPrompt != nil && m.URLPrompt.MatchString(box)
	p.MCP = !p.File && !p.Fetch && m.MCPPrompt != nil && m.MCPPrompt.MatchString(box)
	var at [][]int // where in box the head was read, and a fetch prompt's host or an MCP call's tool
	keep := func(h string, where ...[]int) string {
		for _, w := range where {
			if w != nil {
				at = append(at, w)
			}
		}
		return h
	}
	switch {
	case p.File:
		p.Path = keep(head(m.FileMatch, box))
	case p.Fetch:
		var linkAt, hostAt []int
		p.URL, p.Host, linkAt, hostAt = fetchURL(m.URLMatch, box)
		keep("", linkAt, hostAt)
	case p.MCP:
		var toolAt, argsAt []int
		p.Tool, p.Args, toolAt, argsAt = mcpCall(m.MCPMatch, box)
		keep("", toolAt, argsAt)
	default:
		p.Command = keep(head(m.CommandMatch, box))
	}
	// Approve needs approve_option to confirm option 1 is the approve, and,
	// with a question_match, exactly one line of the box asking the question:
	// text in the box that isn't Claude Code's own (an MCP server's description
	// between the last rule and the real question, O-90's review) can't add a
	// second one that approve_option might read.
	p.Approvable = m.ApproveOption != nil && m.ApproveOption.MatchString(box) &&
		(m.QuestionMatch == nil || linesMatching(m.QuestionMatch, box) == 1)

	// The lines: the title, then the others, but the lines the head and the
	// host were read from ("url: <url>", "Claude wants to fetch content from
	// <host>", "Read(<path>)"), which the card shows as its head and host.
	start := 0
	for i, l := range strings.Split(strings.TrimRight(box, "\n"), "\n") {
		end := start + len(l)
		read := false
		for _, w := range at {
			read = read || start < w[1] && w[0] <= end
		}
		switch {
		case i == 0:
			p.Title = strings.TrimSpace(l)
		case !read && !anyMatch(m.DropLines, l) && !anyMatch(m.IgnoreMatch, l):
			p.Lines = append(p.Lines, l)
		}
		start = end + 1
	}
	return p, true
}

func anyMatch(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// head returns the first of re's groups that matched s with more than
// spaces, trimmed, and where it is in s; "" and nil if none did. A pattern
// may read a head from either of two layouts, one group in each (file_match:
// an edit's or a create's path, or a Read's).
func head(re *regexp.Regexp, s string) (string, []int) {
	if re == nil {
		return "", nil
	}
	m := re.FindStringSubmatchIndex(s)
	for i := 2; i+1 < len(m); i += 2 {
		if m[i] >= 0 {
			if g := strings.TrimSpace(s[m[i]:m[i+1]]); g != "" {
				return g, m[i : i+2]
			}
		}
	}
	return "", nil
}

// fetchURL returns re's group 2 in s, the URL a fetch prompt shows, and its
// group 1, the host the prompt names, as Claude Code parsed the URL it will
// fetch, with where each is (O-88). It reads them only if the URL parses as
// an http or https URL with no user name, whose host, in ASCII, is that
// host; and if the URL's line, and the line under it, are each the only line
// of the box that starts as it does, up to its ": " (" url: ", " prompt: ").
// So a URL that the screen breaks before a line that looks like the next
// field, hiding its rest or moving its host, or that names one host to the
// parser and another to a reader ("https://good.com:443@evil.com/"), reads as
// none: its card fails closed.
func fetchURL(re *regexp.Regexp, s string) (link, host string, linkAt, hostAt []int) {
	if re == nil {
		return "", "", nil, nil
	}
	m := re.FindStringSubmatchIndex(s)
	if len(m) < 6 || m[2] < 0 || m[4] < 0 {
		return "", "", nil, nil
	}
	named, raw := s[m[2]:m[3]], strings.TrimSpace(s[m[4]:m[5]])
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.User != nil || u.Hostname() == "" ||
		!ascii(u.Hostname()) || !ascii(named) || !strings.EqualFold(u.Hostname(), named) {
		return "", "", nil, nil
	}
	line := s[strings.LastIndexByte(s[:m[4]], '\n')+1 : m[5]]
	next, _, _ := strings.Cut(s[m[5]+1:], "\n")
	for _, l := range []string{line, next} {
		if i := strings.Index(l, ": "); i < 0 || startsLines(s, l[:i+2]) != 1 {
			return "", "", nil, nil
		}
	}
	return raw, named, m[4:6], m[2:4]
}

// mcpCall returns re's group 1 in s, an MCP call's server and tool, and its
// group 2, the argument lines, each trimmed, with where each is (O-90); none
// unless both matched with more than spaces. The pattern decides the layout
// it reads (Claude Code's: the tool line ending "(MCP)" under the title, and
// one " <name>: <value>" line per argument between the two rules), so an
// argument that wraps, or a line between the rules that isn't an argument,
// reads none: its card fails closed.
func mcpCall(re *regexp.Regexp, s string) (tool, args string, toolAt, argsAt []int) {
	if re == nil {
		return "", "", nil, nil
	}
	m := re.FindStringSubmatchIndex(s)
	if len(m) < 6 || m[2] < 0 || m[4] < 0 {
		return "", "", nil, nil
	}
	tool = strings.TrimSpace(s[m[2]:m[3]])
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(s[m[4]:m[5]]), "\n") {
		lines = append(lines, strings.TrimSpace(l))
	}
	args = strings.Join(lines, "\n")
	if tool == "" || args == "" {
		return "", "", nil, nil
	}
	return tool, args, m[2:4], m[4:6]
}

// linesMatching counts the lines of s, each without its newline, that re
// matches.
func linesMatching(re *regexp.Regexp, s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if re.MatchString(l) {
			n++
		}
	}
	return n
}

// startsLines counts the lines of s that start with prefix.
func startsLines(s, prefix string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func ascii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
