package keymap

import (
	"crypto/sha256"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/captures"
)

// TestKeymapDiscovery (S2-4) checks every embedded keymap against its
// agent's captures, classified by their manifest, never by file name (parent
// 23.2, design D2-7):
//   - the kind's fixture folder and manifest exist;
//   - permission_match, on the prompt box only, matches every permission and
//     danger capture and no question capture; a status capture is never a
//     permission prompt;
//   - command_match gives the manifest's command for each shell prompt,
//     file_match its path for each file prompt (an edit, a create, a Read),
//     and url_match its URL for each fetch prompt it can read (O-88); none
//     finds one elsewhere, and the line a head is read from isn't repeated
//     among the box's other lines;
//   - the captures of one prompt read the same box, so their cards hash
//     equally (a prompt with one capture has nothing to compare);
//   - a capture the manifest marks "not captured" is skipped.
func TestKeymapDiscovery(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(set) == 0 {
		t.Fatal("no embedded keymap")
	}
	for kind, km := range set {
		t.Run(kind, func(t *testing.T) {
			m, err := captures.Load(kind)
			if err != nil {
				t.Fatalf("the %s keymap's fixtures: %v", kind, err)
			}
			hashes := map[string]map[[32]byte][]string{} // prompt → box hash → captures
			checked := map[string]int{}
			for _, c := range m.Captures {
				if !c.Captured {
					t.Logf("%s: not captured, skipped", c.Prompt)
					continue
				}
				raw, err := m.Read(c.File)
				if err != nil {
					t.Fatal(err)
				}
				p, ok := km.Read(string(raw))
				if c.Kind == "status" {
					if ok && p.Permission {
						t.Errorf("%s: a status capture reads as a permission prompt", c.File)
					}
					continue
				}
				if !ok {
					t.Errorf("%s: no prompt box", c.File)
					continue
				}
				checked[c.Kind]++

				wantPermission := c.Kind == "permission" || c.Kind == "danger"
				if p.Permission != wantPermission {
					t.Errorf("%s (%s): permission %v, want %v", c.File, c.Kind, p.Permission, wantPermission)
				}
				if p.Title != c.Title {
					t.Errorf("%s: title %q, want %q", c.File, p.Title, c.Title)
				}
				if p.Command != c.Command {
					t.Errorf("%s: command %q, want %q", c.File, p.Command, c.Command)
				}
				if p.Path != c.Path {
					t.Errorf("%s: path %q, want %q", c.File, p.Path, c.Path)
				}
				if p.URL != c.URL {
					t.Errorf("%s: URL %q, want %q", c.File, p.URL, c.URL)
				}
				// A read URL comes with the host Claude Code names, whose
				// line ("Claude wants to fetch content from <host>") the
				// card shows as the bare host instead.
				if u, err := url.Parse(c.URL); c.URL != "" && (err != nil || p.Host != u.Hostname()) || c.URL == "" && p.Host != "" {
					t.Errorf("%s: host %q, for the URL %q", c.File, p.Host, c.URL)
				}
				for _, l := range p.Lines {
					if p.Host != "" && strings.Contains(l, "fetch content from") {
						t.Errorf("%s: the host's line %q is among the box's lines", c.File, l)
					}
				}
				// An MCP call's tool and arguments (O-90); the tool's line,
				// which the card shows as the bare tool, isn't among the
				// box's lines either.
				if p.MCP != (c.PromptType == "mcp-tool") || p.Tool != c.Tool || p.Args != c.Args {
					t.Errorf("%s (%s): MCP %v, tool %q, args %q; want %q, %q", c.File, c.PromptType, p.MCP, p.Tool, p.Args, c.Tool, c.Args)
				}
				for _, l := range p.Lines {
					if p.Tool != "" && strings.HasSuffix(l, "(MCP)") {
						t.Errorf("%s: the tool's line %q is among the box's lines", c.File, l)
					}
				}
				if p.Fetch != (c.PromptType == "web-fetch") {
					t.Errorf("%s (%s): fetch %v", c.File, c.PromptType, p.Fetch)
				}
				// The box's line the head was read from, the first under
				// the title that holds it, isn't among its other lines
				// (O-88): the card shows the head once.
				if h := c.Command + c.Path + c.URL + c.Args; h != "" {
					for _, l := range strings.Split(p.Box, "\n")[1:] {
						if strings.Contains(l, h) {
							if slices.Contains(p.Lines, l) {
								t.Errorf("%s: the line the head %q was read from, %q, is among the box's lines", c.File, h, l)
							}
							break
						}
					}
				}

				h := sha256.Sum256([]byte(p.Title + "\n" + strings.Join(p.Lines, "\n")))
				if hashes[c.Prompt] == nil {
					hashes[c.Prompt] = map[[32]byte][]string{}
				}
				hashes[c.Prompt][h] = append(hashes[c.Prompt][h], c.File)
			}
			for prompt, byHash := range hashes {
				if len(byHash) != 1 {
					t.Errorf("%s: its captures read %d different boxes: %v", prompt, len(byHash), byHash)
				}
			}
			t.Logf("checked %v", checked)
			for _, k := range []string{"permission", "danger", "question"} {
				if checked[k] == 0 {
					t.Errorf("no %s capture was checked", k)
				}
			}
		})
	}
}

// fetchBox is a screen holding a WebFetch prompt box in Claude Code 2.1.316's
// shape (tool-prompts/detection/webfetch-0s.txt): host is the host Claude
// Code names, and url the text of the url line.
func fetchBox(host, url string) string {
	rule, dash := strings.Repeat("─", 40), strings.Repeat("╌", 40)
	return "> earlier\n" + rule + "\n Fetch\n Claude wants to fetch content from " + host + "\n" + dash +
		"\n url: " + url + "\n prompt: What is the title of this page?\n" + dash +
		"\n Do you want to allow Claude to fetch this content?\n ❯ 1. Yes\n   2. Yes, and don't ask again for " + host +
		"\n   3. No, and tell Claude what to do differently (esc)\n"
}

// TestKeymapReadsAURLOnlyForItsHost: on a fetch prompt (url_prompt), the
// claude keymap's url_match reads the URL only when it parses as an http or
// https URL with no user name, whose host is the one Claude Code names on the
// line above, its own parse of it (O-88): a URL whose host isn't, which a
// newline in it can cause (the parser drops it, the screen breaks the line
// there), reads none, so its card fails closed. So does a URL whose line, or
// the prompt line under it, isn't the only one of the box to start as it
// does: a newline in the URL could hide the rest of it behind a fake prompt
// line, its host unchanged (O-88's review). The host it names is read with
// the URL. A fetch prompt never reads a command or a path.
func TestKeymapReadsAURLOnlyForItsHost(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	km := set["claude"]
	for _, c := range []struct {
		name, host, url, want string
	}{
		{"the captured shape", "example.com", "https://example.com/", "https://example.com/"},
		{"http, a port, a path and a query", "example.com", "http://example.com:8080/a/b?c=d#e", "http://example.com:8080/a/b?c=d#e"},
		{"the host in capitals", "example.com", "https://EXAMPLE.com/", "https://EXAMPLE.com/"},
		{"another host", "evil.example", "https://example.com/", ""},
		{"a host moved by a user name", "example.com", "https://example.com@evil.example/", ""},
		{"a user name, the host named", "evil.example", "https://example.com@evil.example/", ""},
		{"a user name that looks like a host and port", "evil.com", "https://example.com:443@evil.com/", ""},
		{"a scheme that isn't http", "example.com", "ftp://example.com/", ""},
		{"a file URL", "example.com", "file:///etc/passwd", ""},
		{"a host that isn't ASCII", "xn--exmple-cua.com", "https://exämple.com/", ""},
		{"a space in the URL", "example.com", "https://example.com/a b", ""},
		{"no URL", "example.com", "", ""},
		{"a newline before a fake prompt line moves the host", "evil.example", "https://example.com\n prompt: @evil.example/", ""},
		{"a newline before a fake prompt line, the host unchanged", "example.com", "https://example.com/a\n prompt: b", ""},
		{"a second url line", "example.com", "https://example.com/\n url: https://example.com/b", ""},
	} {
		p, ok := km.Read(fetchBox(c.host, c.url))
		host := ""
		if c.want != "" {
			host = c.host
		}
		switch {
		case !ok:
			t.Errorf("%s: no prompt box", c.name)
		case !p.Fetch || !p.Permission:
			t.Errorf("%s: fetch %v, permission %v; want both", c.name, p.Fetch, p.Permission)
		case p.URL != c.want || p.Host != host:
			t.Errorf("%s: URL %q, host %q; want %q, %q", c.name, p.URL, p.Host, c.want, host)
		case p.Command != "" || p.Path != "":
			t.Errorf("%s: command %q, path %q; want neither", c.name, p.Command, p.Path)
		}
	}
}

// mcpBox is a screen holding an MCP call's prompt box in Claude Code
// 2.1.316's shape (tool-prompts/detection/mcp-0s.txt): tool is the line
// under the title, and args the lines between the two rules.
func mcpBox(tool, args string) string {
	rule, dash := strings.Repeat("─", 40), strings.Repeat("╌", 40)
	return "> earlier\n" + rule + "\n Tool use\n " + tool + "\n" + dash + "\n" + args + "\n" + dash +
		"\n About the recon — Echo Tool:\n │ Returns the text it is given.\n\n Do you want to proceed?\n ❯ 1. Yes" +
		"\n   2. Yes, and don't ask again for recon — Echo commands in /tmp/x\n   3. No\n\n Esc to cancel · Tab to amend\n"
}

// TestKeymapReadsAnMCPCall: on an MCP call's prompt (mcp_prompt, the title
// line "Tool use"), the claude keymap's mcp_match reads the server and the
// tool from the line under the title, which must end "(MCP)", and the
// arguments from the lines between the two rules, each " <name>: <value>"
// on one line (O-90). An argument that wraps (its value in a │ gutter), a
// line between the rules that isn't an argument, a third rule, or a tool
// line without "(MCP)" reads none, so the card fails closed. An MCP call
// never reads a command, a path or a URL.
func TestKeymapReadsAnMCPCall(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	km := set["claude"]
	const tool = "recon — Echo Tool: (MCP)"
	for _, c := range []struct {
		name, tool, args, wantTool, wantArgs string
	}{
		{"the captured shape", tool, ` text: "hello from the recon"`, "recon — Echo Tool", `text: "hello from the recon"`},
		{"two arguments", tool, " text: \"hi\"\n times: 2", "recon — Echo Tool", "text: \"hi\"\ntimes: 2"},
		{"no colon after the tool", "github — create_issue (MCP)", ` title: "x"`, "github — create_issue", `title: "x"`},
		{"an argument that wraps", tool, " text:\n   │ The quick brown fox\n   │  jumps over the lazy dog.", "", ""},
		{"a line that isn't an argument", tool, " text: \"hi\"\n and more", "", ""},
		{"a third rule", tool, " text: \"hi\"\n" + strings.Repeat("╌", 10) + "\n text: \"ho\"", "", ""},
		{"a tool line without (MCP)", "recon — Echo Tool:", ` text: "hi"`, "", ""},
		{"no arguments", tool, "", "", ""},
	} {
		p, ok := km.Read(mcpBox(c.tool, c.args))
		switch {
		case !ok:
			t.Errorf("%s: no prompt box", c.name)
		case !p.MCP || !p.Permission:
			t.Errorf("%s: MCP %v, permission %v; want both", c.name, p.MCP, p.Permission)
		case p.Tool != c.wantTool || p.Args != c.wantArgs:
			t.Errorf("%s: tool %q, args %q; want %q, %q", c.name, p.Tool, p.Args, c.wantTool, c.wantArgs)
		case p.Command != "" || p.Path != "" || p.URL != "":
			t.Errorf("%s: command %q, path %q, URL %q; want none", c.name, p.Command, p.Path, p.URL)
		}
	}
}

// TestKeymapUnknownKindHasNone: an agent whose kind herdr doesn't report, or
// whose kind has no keymap, gets none (parent 8.6): For returns nil, and a
// nil keymap reads no prompt and has no keys, so its card can offer only
// jump. A kind matches herdr's string exactly. A kind whose keymap has no
// permission_match (an override can add one) has no approve or deny keys
// either (parent design §Keymaps); its interrupt keys stay.
func TestKeymapUnknownKindHasNone(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	str := func(s string) *string { return &s }
	for _, kind := range []*string{nil, str(""), str("codex"), str("gemini"), str("Claude"), str("claude ")} {
		if km := set.For(kind); km != nil {
			t.Errorf("For(%v) = the %s keymap, want none", kind, km.Kind)
		}
	}

	m, err := captures.Load("claude")
	if err != nil {
		t.Fatal(err)
	}
	var shell []byte
	for _, c := range m.Captures {
		if c.Captured && c.PromptType == "shell" {
			if shell, err = m.Read(c.File); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	var none *Map
	if p, ok := none.Read(string(shell)); ok {
		t.Errorf("no keymap read a prompt: %+v", p)
	}
	for _, verb := range []string{"approve", "deny", "interrupt"} {
		if keys := none.Keys(verb); keys != nil {
			t.Errorf("no keymap: Keys(%q) = %q, want none", verb, keys)
		}
	}

	claude := set.For(str("claude"))
	if claude == nil || claude.Kind != "claude" {
		t.Fatalf("For(claude) = %+v, want the claude keymap", claude)
	}
	if _, ok := claude.Read(string(shell)); !ok {
		t.Error("the claude keymap read no prompt in a shell capture")
	}
	for verb, want := range map[string][]string{
		"approve": {"1"}, "deny": {"esc"}, "interrupt": {"esc"},
		"jump": nil, "show": nil, "": nil, "APPROVE": nil,
	} {
		if got := claude.Keys(verb); !reflect.DeepEqual(got, want) {
			t.Errorf("claude: Keys(%q) = %q, want %q", verb, got, want)
		}
	}

	set, err = Load(writeOverrides(t, "[codex]\napprove = [\"y\"]\ndeny = [\"n\"]\ninterrupt = [\"esc\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	codex := set.For(str("codex"))
	if codex == nil {
		t.Fatal("For(codex) = none, after an override added it")
	}
	for verb, want := range map[string][]string{"approve": nil, "deny": nil, "interrupt": {"esc"}} {
		if got := codex.Keys(verb); !reflect.DeepEqual(got, want) {
			t.Errorf("codex, without permission_match: Keys(%q) = %q, want %q", verb, got, want)
		}
	}
}

// writeOverrides writes a user's keymaps.toml in a temp folder.
func writeOverrides(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keymaps.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestKeymapRejectsInvalidOverride: an invalid ~/.config/herdr-pager/
// keymaps.toml stops loading with an error naming the file, the table and
// the key, and nothing is loaded; bad config never falls back silently
// (parent design §Keymaps, parent 23.2). The invalid ones: a key outside the
// allowed names, a pattern that doesn't compile, a pattern that matches the
// empty string (so it would match any text), a key the format doesn't have,
// and a file that isn't TOML. A valid override replaces only the keys it
// sets; a missing file is no override.
func TestKeymapRejectsInvalidOverride(t *testing.T) {
	for _, c := range []struct {
		name, body string
		want       []string // in the error, besides the file's path
	}{
		{"a key with a modifier herdr lacks", "[claude]\napprove = [\"ctrl+shift+x\"]\n", []string{"[claude]", "approve", `"ctrl+shift+x"`}},
		{"a capitalised key", "[claude]\ndeny = [\"Esc\"]\n", []string{"[claude]", "deny", `"Esc"`}},
		{"a function key", "[claude]\ninterrupt = [\"f1\"]\n", []string{"[claude]", "interrupt", `"f1"`}},
		{"text, not a key", "[claude]\napprove = [\"1\", \"yes\"]\n", []string{"[claude]", "approve", `"yes"`}},
		{"no keys", "[claude]\napprove = [\"\"]\n", []string{"[claude]", "approve", `""`}},

		{"a regex that doesn't compile", "[claude]\npermission_match = ['Do you (want']\n", []string{"[claude]", "permission_match", "missing closing )"}},
		{"a bad command_match", "[claude]\ncommand_match = '[unclosed'\n", []string{"[claude]", "command_match", "missing closing ]"}},
		{"a bad box_start", "[claude]\nbox_start = '(?P<x'\n", []string{"[claude]", "box_start", "invalid named capture"}},
		{"a bad drop_lines", "[claude]\ndrop_lines = ['^ *Tip: ', 'a{2,1}']\n", []string{"[claude]", "drop_lines", "invalid repeat count"}},

		{"an empty permission_match", "[claude]\npermission_match = ['']\n", []string{"[claude]", "permission_match", "matches the empty string"}},
		{"an optional permission_match", "[claude]\npermission_match = ['(Do you want to proceed\\?)?']\n", []string{"[claude]", "permission_match", "matches the empty string"}},
		{"a command_match of anything", "[claude]\ncommand_match = '(.*)'\n", []string{"[claude]", "command_match", "matches the empty string"}},
		{"an empty file_match", "[claude]\nfile_match = ''\n", []string{"[claude]", "file_match", "matches the empty string"}},
		{"an empty box_start", "[claude]\nbox_start = '(?m)^'\n", []string{"[claude]", "box_start", "matches the empty string"}},
		{"an ignore_match of every line", "[claude]\nignore_match = ['x*']\n", []string{"[claude]", "ignore_match", "matches the empty string"}},
		{"a new kind's empty drop_lines", "[codex]\ndrop_lines = ['']\n", []string{"[codex]", "drop_lines", "matches the empty string"}},

		{"a misspelt key", "[claude]\naprove = [\"1\"]\n", []string{"line 2", "claude.aprove"}},
		{"not TOML", "[claude\napprove = [\"1\"]\n", []string{"line 1"}},
		{"a string where keys go", "[claude]\napprove = \"1\"\n", []string{"line 2"}},
	} {
		path := writeOverrides(t, c.body)
		set, err := Load(path)
		if err == nil {
			t.Errorf("%s: loaded, want an error", c.name)
			continue
		}
		t.Logf("%s: %v", c.name, err)
		if set != nil {
			t.Errorf("%s: an error, yet a set was loaded", c.name)
		}
		for _, w := range append([]string{path}, c.want...) {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: error %q doesn't say %q", c.name, err, w)
			}
		}
	}

	embedded, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	set, err := Load(writeOverrides(t, "[claude]\napprove = [\"1\", \"enter\"]\n"))
	if err != nil {
		t.Fatalf("a valid override: %v", err)
	}
	want := *embedded["claude"]
	want.Approve = []string{"1", "enter"}
	if got := *set["claude"]; !reflect.DeepEqual(got, want) {
		t.Errorf("a valid override:\n got %+v\nwant %+v", got, want)
	}

	set, err = Load(filepath.Join(t.TempDir(), "keymaps.toml"))
	if err != nil {
		t.Fatalf("a missing overrides file: %v", err)
	}
	if !reflect.DeepEqual(set, embedded) {
		t.Errorf("a missing overrides file changed the keymaps: %+v", set)
	}
}
