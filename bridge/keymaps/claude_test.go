package keymaps

import (
	"bytes"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/captures"
)

// rawKeymap is one [kind] table of a keymap file, its patterns as written.
type rawKeymap struct {
	AgentVersion    string   `toml:"agent_version"`
	Captured        string   `toml:"captured"`
	Approve         []string `toml:"approve"`
	Deny            []string `toml:"deny"`
	Interrupt       []string `toml:"interrupt"`
	PermissionMatch []string `toml:"permission_match"`
	CommandMatch    string   `toml:"command_match"`
	IgnoreMatch     []string `toml:"ignore_match"`
	BoxStart        string   `toml:"box_start"`
	DropLines       []string `toml:"drop_lines"`
	FileMatch       string   `toml:"file_match"`
	FilePrompt      string   `toml:"file_prompt"`
	ApproveOption   string   `toml:"approve_option"`
	URLPrompt       string   `toml:"url_prompt"`
	URLMatch        string   `toml:"url_match"`
	MCPPrompt       string   `toml:"mcp_prompt"`
	MCPMatch        string   `toml:"mcp_match"`
	QuestionMatch   string   `toml:"question_match"`
}

// firstGroup returns the first of re's groups that matched s, non-empty and
// trimmed, as the bridge's keymap reads a head; "" if none.
func firstGroup(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	for _, g := range m[min(len(m), 1):] {
		if g = strings.TrimSpace(g); g != "" {
			return g
		}
	}
	return ""
}

// TestClaudeKeymapApproveOptionAndFilePromptFitTheCaptures checks the fields
// the security pass added (2026-11-01, findings 2 and 4) against the
// captures, through their manifest: approve_option, which confirms that
// option 1 (approve's key) is a plain Yes, matches the box of every captured
// permission prompt, danger included, so each still offers approve;
// file_prompt matches the box of the file prompts and of no other; and
// command_match, anchored to the box since the review (2026-11-01), gives
// each Bash prompt's command from its box and nothing from any other. The
// two O-88 added: url_prompt matches the fetch prompts' boxes and no other,
// and url_match gives each readable one's URL (group 2) and the host Claude
// Code names (group 1), and nothing from any other box, the long URL's
// included.
func TestClaudeKeymapApproveOptionAndFilePromptFitTheCaptures(t *testing.T) {
	data, err := FS.ReadFile("claude.toml")
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]rawKeymap
	if err := toml.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	km := file["claude"]
	if km.ApproveOption == "" || km.FilePrompt == "" || km.URLPrompt == "" || km.URLMatch == "" || km.MCPPrompt == "" || km.MCPMatch == "" || km.QuestionMatch == "" {
		t.Fatalf("approve_option %q, file_prompt %q, url_prompt %q, url_match %q: each must be set", km.ApproveOption, km.FilePrompt, km.URLPrompt, km.URLMatch)
	}
	boxStart := regexp.MustCompile(km.BoxStart)
	approve := regexp.MustCompile(km.ApproveOption)
	filePrompt := regexp.MustCompile(km.FilePrompt)
	commandMatch := regexp.MustCompile(km.CommandMatch)
	urlPrompt := regexp.MustCompile(km.URLPrompt)
	urlMatch := regexp.MustCompile(km.URLMatch)
	mcpPrompt := regexp.MustCompile(km.MCPPrompt)
	mcpMatch := regexp.MustCompile(km.MCPMatch)
	question := regexp.MustCompile(km.QuestionMatch)
	m, err := captures.Load("claude")
	if err != nil {
		t.Fatal(err)
	}
	permissions := 0
	for _, c := range m.Captures {
		if !c.Captured || c.Kind == "status" {
			continue
		}
		raw, err := m.Read(c.File)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		starts := boxStart.FindAllStringIndex(text, -1)
		box := text[starts[len(starts)-1][0]:]
		box = box[strings.IndexByte(box, '\n')+1:]
		permission := c.Kind == "permission" || c.Kind == "danger"
		if permission {
			permissions++
			if !approve.MatchString(box) {
				t.Errorf("%s: approve_option doesn't match its box, so it would offer no approve", c.File)
			}
			// question_match (O-90's review): exactly one line asks.
			n := 0
			for _, l := range strings.Split(box, "\n") {
				if question.MatchString(l) {
					n++
				}
			}
			if n != 1 {
				t.Errorf("%s: question_match matches %d lines of its box, want 1", c.File, n)
			}
		}
		if got, want := filePrompt.MatchString(box), c.Path != ""; got != want {
			t.Errorf("%s: file_prompt matches its box: %v, want %v", c.File, got, want)
		}
		// command_match, anchored to the box: the manifest's command from each
		// Bash prompt's box, and nothing from any other.
		var got string
		if m := commandMatch.FindStringSubmatch(box); m != nil {
			got = strings.TrimSpace(m[1])
		}
		if got != c.Command {
			t.Errorf("%s: command_match on its box gives %q, want %q", c.File, got, c.Command)
		}
		// url_prompt and url_match (O-88): the fetch prompts' boxes and no
		// other; group 2, the URL, as the manifest gives it, and group 1 the
		// host Claude Code names, which the bridge checks the URL against.
		if got, want := urlPrompt.MatchString(box), c.PromptType == "web-fetch"; got != want {
			t.Errorf("%s: url_prompt matches its box: %v, want %v", c.File, got, want)
		}
		var host, url string
		if m := urlMatch.FindStringSubmatch(box); m != nil {
			host, url = m[1], m[2]
		}
		if url != c.URL || c.URL != "" && host != "example.com" {
			t.Errorf("%s: url_match on its box gives host %q, URL %q; want example.com, %q", c.File, host, url, c.URL)
		}
		// mcp_prompt and mcp_match (O-90): the MCP calls' boxes and no
		// other; group 1 the server and the tool, group 2 the argument lines.
		if got, want := mcpPrompt.MatchString(box), c.PromptType == "mcp-tool"; got != want {
			t.Errorf("%s: mcp_prompt matches its box: %v, want %v", c.File, got, want)
		}
		var tool, args string
		if m := mcpMatch.FindStringSubmatch(box); m != nil {
			tool, args = m[1], strings.TrimSpace(m[2])
		}
		if tool != c.Tool || args != c.Args {
			t.Errorf("%s: mcp_match on its box gives tool %q, args %q; want %q, %q", c.File, tool, args, c.Tool, c.Args)
		}
	}
	if permissions == 0 {
		t.Fatal("no captured permission prompt")
	}
}

// rule is a line drawn only with box-drawing dashes.
var rule = regexp.MustCompile(`^[─╌]+$`)

// TestClaudeKeymapFieldsFitTheCaptures checks the fields the foundation added
// to phase 0's keymap (design D2-7, ADR 0006) against the captures, through
// their manifest: box_start matches once in every captured prompt, and the
// box below it starts with the prompt's title line; drop_lines takes out the
// box's tip and rule lines and nothing the card needs; file_match gives the
// manifest's path for the file prompts (a Read's too, since O-88) and
// nothing for any other capture; and phase 0's own fields are unchanged
// (permission_match has WebFetch's question too, since O-88).
func TestClaudeKeymapFieldsFitTheCaptures(t *testing.T) {
	data, err := FS.ReadFile("claude.toml")
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]rawKeymap
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		t.Fatal(err)
	}
	km, ok := file["claude"]
	if !ok {
		t.Fatal("no [claude] table")
	}

	phase0 := rawKeymap{
		AgentVersion: "2.1.306", Captured: "2026-10-21",
		Approve: []string{"1"}, Deny: []string{"esc"}, Interrupt: []string{"esc"},
		// WebFetch's question since O-88 (2026-11-01, the tool prompts).
		PermissionMatch: []string{`Do you want to (proceed\?|make this edit to |create )`, `Do you want to allow Claude to fetch this content\?`},
		// Anchored to the box's start and end since the security pass's
		// review (2026-11-01): phase 0's was `Bash command\n[^╌]*╌+\n([^╌]+)\n╌`.
		CommandMatch: `^ Bash command\n[^╌]*╌+\n([^╌]+)\n╌+\n[^╌]*$`,
		IgnoreMatch:  []string{},
	}
	got := km
	got.BoxStart, got.DropLines, got.FileMatch = "", nil, ""
	got.FilePrompt, got.ApproveOption = "", "" // the security pass's (TestClaudeKeymapApproveOptionAndFilePromptFitTheCaptures)
	got.URLPrompt, got.URLMatch = "", ""       // O-88's (the same test)
	got.MCPPrompt, got.MCPMatch = "", ""       // O-90's (the same test)
	got.QuestionMatch = ""                     // O-90's review (the same test)
	if !reflect.DeepEqual(got, phase0) {
		t.Errorf("phase 0's fields changed:\n got %+v\nwant %+v", got, phase0)
	}

	if km.BoxStart == "" || len(km.DropLines) == 0 || km.FileMatch == "" {
		t.Fatalf("box_start %q, drop_lines %q, file_match %q: each must be set", km.BoxStart, km.DropLines, km.FileMatch)
	}
	boxStart := regexp.MustCompile(km.BoxStart)
	fileMatch := regexp.MustCompile(km.FileMatch)
	var drops []*regexp.Regexp
	for _, d := range km.DropLines {
		drops = append(drops, regexp.MustCompile(d))
	}
	dropped := func(line string) bool {
		for _, d := range drops {
			if d.MatchString(line) {
				return true
			}
		}
		return false
	}

	m, err := captures.Load("claude")
	if err != nil {
		t.Fatal(err)
	}
	prompts, paths := 0, 0
	defer func() {
		t.Logf("%d prompt captures checked, %d of them file prompts", prompts, paths)
		if prompts == 0 || paths == 0 {
			t.Error("no prompt or no file prompt was checked")
		}
	}()
	for _, c := range m.Captures {
		if !c.Captured {
			continue
		}
		raw, err := m.Read(c.File)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if c.Kind == "status" {
			if p := fileMatch.FindStringSubmatch(text); p != nil {
				t.Errorf("%s: file_match finds %q in a capture with no prompt", c.File, p[1])
			}
			continue
		}

		prompts++
		if c.Path != "" {
			paths++
		}
		starts := boxStart.FindAllStringIndex(text, -1)
		if len(starts) != 1 {
			t.Errorf("%s: box_start matches %d times, want once", c.File, len(starts))
			continue
		}
		// The box is every line below the one where box_start's match begins.
		box := text[starts[0][0]:]
		box = box[strings.IndexByte(box, '\n')+1:]
		lines := strings.Split(box, "\n")
		if title := strings.TrimSpace(lines[0]); title != c.Title {
			t.Errorf("%s: the box starts with %q, want the title %q", c.File, title, c.Title)
		}

		for _, l := range lines {
			isTip := strings.Contains(l, "Tip:")
			switch {
			case dropped(l) && strings.TrimSpace(l) == c.Title:
				t.Errorf("%s: drop_lines drops the title %q", c.File, l)
			case dropped(l) && (c.Command != "" && strings.TrimSpace(l) == c.Command || c.Path != "" && strings.TrimSpace(l) == c.Path):
				t.Errorf("%s: drop_lines drops the command or path %q", c.File, l)
			case dropped(l) && !isTip && !rule.MatchString(l):
				t.Errorf("%s: drop_lines drops %q, neither a tip nor a rule", c.File, l)
			case !dropped(l) && (isTip || rule.MatchString(l)):
				t.Errorf("%s: drop_lines keeps %q", c.File, l)
			}
		}

		// file_match's head is its first group that matched: an edit's or a
		// create's path, or a Read's (O-88).
		p := firstGroup(fileMatch, box)
		switch {
		case p != c.Path && c.Path != "":
			t.Errorf("%s: file_match gives %q, want %q", c.File, p, c.Path)
		case p != "" && c.Path == "":
			t.Errorf("%s: file_match finds %q in a prompt with no path", c.File, p)
		}
		if p := fileMatch.FindStringSubmatch(text); c.Path == "" && p != nil {
			t.Errorf("%s: file_match finds %q in the screen", c.File, p)
		}
	}
}
