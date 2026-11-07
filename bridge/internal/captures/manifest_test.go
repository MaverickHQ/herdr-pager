package captures

import (
	"os"
	"path/filepath"
	"testing"
)

// TestManifestCoversEveryFixture checks Claude Code's manifest against its
// folder: every detection capture, phase 0's and the tool prompts' (O-88),
// has exactly one entry, and every captured entry names a detection file and
// an agent.get file that exist.
func TestManifestCoversEveryFixture(t *testing.T) {
	m, err := Load("claude")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, set := range []string{"detection", "tool-prompts/detection"} {
		f, err := filepath.Glob(filepath.Join(m.Dir, set, "*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(f) == 0 {
			t.Fatalf("no captures in %s", set)
		}
		files = append(files, f...)
	}
	if len(files) == 0 {
		t.Fatalf("no detection captures in %s", m.Dir)
	}
	entries := map[string]int{}
	for _, c := range m.Captures {
		if !c.Captured {
			if c.File != "" || c.Agent != "" {
				t.Errorf("%s: not captured, yet names %q and %q", c.Prompt, c.File, c.Agent)
			}
			continue
		}
		entries[c.File]++
		for _, f := range []string{c.File, c.Agent} {
			if _, err := os.Stat(filepath.Join(m.Dir, f)); err != nil {
				t.Errorf("%s: %v", c.Prompt, err)
			}
		}
	}
	for _, f := range files {
		rel, _ := filepath.Rel(m.Dir, f)
		if n := entries[filepath.ToSlash(rel)]; n != 1 {
			t.Errorf("%s has %d manifest entries, want 1", rel, n)
		}
	}
}

// TestManifestChecksEachEntry: Load refuses an entry whose values don't fit
// its kind and prompt type. A fetch prompt names the URL url_match reads,
// unless it's marked unread: a permission prompt whose head the keymap
// can't read, so its card fails closed, names none. A Read prompt names its
// path, as an edit or create prompt does (O-88). An MCP call names its tool
// (the server and the tool, as the box shows them) and its arguments, unless
// it's unread (O-90).
func TestManifestChecksEachEntry(t *testing.T) {
	base := Capture{File: "a", Agent: "b", Captured: true, Kind: "permission", Prompt: "p"}
	with := func(f func(*Capture)) Capture { c := base; f(&c); return c }
	fetch := func(c *Capture) { c.PromptType, c.Title = "web-fetch", "Fetch" }
	mcp := func(c *Capture) { c.PromptType, c.Title = "mcp-tool", "Tool use" }
	for name, c := range map[string]Capture{
		"a fetch prompt and its URL":          with(func(c *Capture) { fetch(c); c.URL = "https://example.com/" }),
		"an unread fetch prompt, with no URL": with(func(c *Capture) { fetch(c); c.Unread = true }),
		"a Read prompt and its path":          with(func(c *Capture) { c.PromptType, c.Title, c.Path = "file-read", "Read file", "/etc/shells" }),
		"an MCP call, its tool and arguments": with(func(c *Capture) { mcp(c); c.Tool, c.Args = "recon — Echo Tool", `text: "hi"` }),
		"an unread MCP call":                  with(func(c *Capture) { mcp(c); c.Unread = true }),
	} {
		if err := check(c, map[string]bool{}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, c := range map[string]Capture{
		"a fetch prompt with no URL, not unread": with(fetch),
		"an unread prompt naming its URL":        with(func(c *Capture) { fetch(c); c.URL, c.Unread = "https://example.com/", true }),
		"a URL on a shell prompt": with(func(c *Capture) {
			c.PromptType, c.Title, c.Command, c.URL = "shell", "Bash command", "ls", "https://example.com/"
		}),
		"an unread question":                        with(func(c *Capture) { c.Kind, c.PromptType, c.Title, c.Unread = "question", "question", "Colour", true }),
		"a Read prompt with no path":                with(func(c *Capture) { c.PromptType, c.Title = "file-read", "Read file" }),
		"an MCP call with no arguments, not unread": with(func(c *Capture) { mcp(c); c.Tool = "recon — Echo Tool" }),
		"an MCP call with no tool, not unread":      with(func(c *Capture) { mcp(c); c.Args = `text: "hi"` }),
		"an unread MCP call naming its tool":        with(func(c *Capture) { mcp(c); c.Tool, c.Unread = "recon — Echo Tool", true }),
		"a tool on a shell prompt": with(func(c *Capture) {
			c.PromptType, c.Title, c.Command, c.Tool = "shell", "Bash command", "ls", "recon — Echo Tool"
		}),
	} {
		if err := check(c, map[string]bool{}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
