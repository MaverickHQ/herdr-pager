// Package captures reads an agent kind's captures from phase 0 through their
// manifest (testdata/herdr/<kind>/manifest.toml, design D2-7), so that tests
// classify a capture by what the manifest says it is, never by its file name.
//
// It is test support: only tests import it, and it finds testdata/ from its
// own source file, so a test in any package can call Load.
package captures

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/pelletier/go-toml/v2"
)

// A Capture is one entry of a manifest; the manifest's header says what each
// field means.
type Capture struct {
	File       string `toml:"file"`  // the detection read, relative to the manifest's folder
	Agent      string `toml:"agent"` // the agent.get taken with it
	Captured   bool   `toml:"captured"`
	Kind       string `toml:"kind"`        // permission, danger, question or status
	Prompt     string `toml:"prompt"`      // shared by the captures of one prompt
	PromptType string `toml:"prompt_type"` // shell, file-edit, file-create, file-read, web-fetch, mcp-tool, question, plan-approval, folder-trust or none
	Title      string `toml:"title"`       // the prompt box's title line
	Command    string `toml:"command"`     // shell prompts
	Path       string `toml:"path"`        // file prompts
	URL        string `toml:"url"`         // fetch prompts, unless unread
	Tool       string `toml:"tool"`        // MCP calls, unless unread: the server and the tool, as the box names them
	Args       string `toml:"args"`        // MCP calls, unless unread: the argument lines, each trimmed
	Unread     bool   `toml:"unread"`      // a permission prompt the keymap can't read in full: its card fails closed
	Label      string `toml:"label"`       // danger prompts
}

// A Manifest is one agent kind's captures.
type Manifest struct {
	Dir      string    `toml:"-"` // testdata/herdr/<kind>
	Captures []Capture `toml:"capture"`
}

var (
	kinds       = []string{"permission", "danger", "question", "status"}
	promptTypes = []string{"shell", "file-edit", "file-create", "file-read", "web-fetch", "mcp-tool", "question", "plan-approval", "folder-trust", "none"}
)

// Root is the bridge's testdata/herdr folder.
func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "herdr")
}

// Load reads and checks kind's manifest: no unknown keys, each value one the
// header allows, and each entry carrying exactly the expected values its kind
// and prompt type call for.
func Load(kind string) (*Manifest, error) {
	m := &Manifest{Dir: filepath.Join(Root(), kind)}
	path := filepath.Join(m.Dir, "manifest.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	for i, c := range m.Captures {
		if err := check(c, seen); err != nil {
			return nil, fmt.Errorf("%s: capture %d (%s): %w", path, i+1, c.Prompt, err)
		}
	}
	return m, nil
}

func check(c Capture, seen map[string]bool) error {
	file := c.PromptType == "file-edit" || c.PromptType == "file-create" || c.PromptType == "file-read"
	fetch := c.PromptType == "web-fetch"
	mcp := c.PromptType == "mcp-tool"
	switch {
	case !slices.Contains(kinds, c.Kind):
		return fmt.Errorf("kind %q isn't one of %v", c.Kind, kinds)
	case !slices.Contains(promptTypes, c.PromptType):
		return fmt.Errorf("prompt_type %q isn't one of %v", c.PromptType, promptTypes)
	case c.Prompt == "":
		return errors.New("no prompt")
	case c.Captured && (c.File == "" || c.Agent == ""):
		return errors.New("captured, but no file or agent")
	case !c.Captured && (c.File != "" || c.Agent != ""):
		return errors.New("not captured, yet names a file")
	case c.Captured && seen[c.File]:
		return fmt.Errorf("%s has another entry", c.File)
	case (c.Kind == "status") != (c.PromptType == "none"):
		return errors.New("a status capture, and only one, has prompt_type none")
	case c.Captured && c.Kind != "status" && c.Title == "":
		return errors.New("a captured prompt needs its title")
	case (c.PromptType == "shell") != (c.Command != ""):
		return errors.New("a shell prompt, and only one, has a command")
	case file != (c.Path != ""):
		return errors.New("a file prompt, and only one, has a path")
	case (c.Kind == "danger") != (c.Label != ""):
		return errors.New("a danger prompt, and only one, has a label")
	case c.Unread && c.Kind != "permission" && c.Kind != "danger":
		return errors.New("only a permission prompt can be unread")
	case c.Unread && (c.Command != "" || c.Path != "" || c.URL != "" || c.Tool != "" || c.Args != ""):
		return errors.New("an unread prompt names no command, path, URL, tool or arguments")
	case !fetch && c.URL != "", fetch && !c.Unread && c.URL == "":
		return errors.New("a fetch prompt, and only one, has a URL, unless it's unread")
	case !mcp && (c.Tool != "" || c.Args != ""), mcp && !c.Unread && (c.Tool == "" || c.Args == ""):
		return errors.New("an MCP call, and only one, has a tool and arguments, unless it's unread")
	}
	seen[c.File] = true
	return nil
}

// Read returns a file of the manifest's folder, such as a capture's File or
// Agent.
func (m *Manifest) Read(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(m.Dir, name))
}
