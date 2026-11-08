package repocheck

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// reaches returns every package of the bridge's module that from's non-test
// files import, directly or not, from included.
func reaches(t *testing.T, from string) []string {
	t.Helper()
	module := modulePath(t)
	seen := map[string]bool{}
	var visit func(dir string)
	visit = func(dir string) {
		if seen[dir] {
			return
		}
		seen[dir] = true
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, dir, name), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				if p, _ := strconv.Unquote(imp.Path.Value); strings.HasPrefix(p, module+"/") {
					visit(strings.TrimPrefix(p, module+"/"))
				}
			}
		}
	}
	visit(from)
	var dirs []string
	for d := range seen {
		dirs = append(dirs, d)
	}
	slices.Sort(dirs)
	return dirs
}

// agentReach are the bridge's packages through which code could reach an
// agent: herdr's socket and the calls on it, a keystroke, a card or a verdict
// (CLAUDE.md §Architecture; parent design §Components).
var agentReach = map[string]string{
	"internal/herdr":     "herdr's socket client (agent.send_keys, agent.focus)",
	"internal/session":   "the pager's verbs, each turned into an agent's input",
	"internal/keymap":    "what each verb types into an agent",
	"internal/card":      "the cards",
	"internal/watch":     "the poll of herdr's agents",
	"internal/bridge":    "the serve loop that wires them together",
	"internal/sshserver": "the pager's link, which carries cards and verdicts",
}

// TestTheSetupPageCantReachAnAgent: the setup page never approves, denies,
// interrupts or sends keys, and never shows a card (phase 5 requirement 2.7,
// parent 22.5). It holds by construction if nothing internal/setup is built
// from can reach herdr's agents: none of agentReach. The doctor's check of
// herdr's socket only connects and closes (internal/doctor, checkHerdr). The
// positive control: pager-bridge itself reaches each of them, so the walk
// finds such an import when there is one.
func TestTheSetupPageCantReachAnAgent(t *testing.T) {
	bridge := reaches(t, "cmd/pager-bridge")
	for pkg := range agentReach {
		if !slices.Contains(bridge, pkg) {
			t.Errorf("the walk from cmd/pager-bridge never reaches %s: the positive control fails, or %s moved", pkg, pkg)
		}
	}
	setup := reaches(t, "internal/setup")
	if !slices.Contains(setup, "internal/provision") {
		t.Errorf("the walk from internal/setup reached %q, without internal/provision: the walk is broken", setup)
	}
	for _, pkg := range setup {
		if why, ok := agentReach[pkg]; ok {
			t.Errorf("internal/setup reaches %s, %s (it reaches %q)", pkg, why, setup)
		}
	}
}
