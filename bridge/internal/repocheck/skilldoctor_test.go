package repocheck

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"
)

// doctorStates returns, by check id, the states internal/doctor's checks can
// give: the string literals passed to pass(c, …) and fail(c, …) in the
// function that makes c as Check{ID: <id>}.
func doctorStates(t *testing.T) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(root, "internal", "doctor", "doctor.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	consts := map[string]string{}
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.CONST {
			for _, s := range g.Specs {
				v := s.(*ast.ValueSpec)
				for i, name := range v.Names {
					if i < len(v.Values) {
						if lit, ok := v.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							consts[name.Name], _ = strconv.Unquote(lit.Value)
						}
					}
				}
			}
		}
	}
	ids := map[string]string{}      // the check id of each function that makes c
	own := map[string][]string{}    // the states each function gives
	passes := map[string][]string{} // the functions each one hands c on to
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		id := ""
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if cl, ok := n.(*ast.CompositeLit); ok {
				if typ, ok := cl.Type.(*ast.Ident); ok && typ.Name == "Check" {
					for _, e := range cl.Elts {
						if kv, ok := e.(*ast.KeyValueExpr); ok {
							if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "ID" {
								if v, ok := kv.Value.(*ast.Ident); ok {
									id = consts[v.Name]
								}
							}
						}
					}
				}
			}
			return true
		})
		if id != "" {
			ids[fn.Name.Name] = id
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			name, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			if (name.Name == "pass" || name.Name == "fail") && len(call.Args) >= 2 {
				if lit, ok := call.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					s, _ := strconv.Unquote(lit.Value)
					own[fn.Name.Name] = append(own[fn.Name.Name], s)
				}
			} else if arg, ok := call.Args[0].(*ast.Ident); ok && arg.Name == "c" {
				passes[fn.Name.Name] = append(passes[fn.Name.Name], name.Name)
			}
			return true
		})
	}
	states := map[string][]string{}
	for fn, id := range ids {
		seen := map[string]bool{}
		var collect func(fn string)
		collect = func(fn string) {
			if seen[fn] {
				return
			}
			seen[fn] = true
			for _, s := range own[fn] {
				if !slices.Contains(states[id], s) {
					states[id] = append(states[id], s)
				}
			}
			for _, next := range passes[fn] {
				collect(next)
			}
		}
		collect(fn)
	}
	return states
}

// TestTheSkillNamesEveryDoctorState: the Claude Code skill explains each of
// the doctor's states (phase 5 requirement 5.2) from a table per check in
// plugin/skills/herdr-pager/references/doctor.md. A state the doctor gives and
// the table lacks leaves the skill guessing; a row for a state the doctor no
// longer gives is advice for nothing. Lane plugin's review found four such
// drifts in the screens by hand (phase 5's traceability, gap 5).
func TestTheSkillNamesEveryDoctorState(t *testing.T) {
	code := doctorStates(t)
	if len(code) != 6 {
		t.Fatalf("found the states of %d checks in doctor.go, %v; want the 6 checks", len(code), code)
	}

	path := filepath.Join(root, "..", "plugin", "skills", "herdr-pager", "references", "doctor.md")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	heading := regexp.MustCompile("^## `([a-z]+)`")
	row := regexp.MustCompile("^\\| `([a-z-]+)` \\|")
	doc := map[string][]string{}
	id := ""
	for s := bufio.NewScanner(f); s.Scan(); {
		if m := heading.FindStringSubmatch(s.Text()); m != nil {
			id = m[1]
		} else if m := row.FindStringSubmatch(s.Text()); m != nil && id != "" {
			doc[id] = append(doc[id], m[1])
		}
	}

	for id, states := range code {
		if _, ok := doc[id]; !ok {
			t.Errorf("references/doctor.md has no table for the %q check", id)
			continue
		}
		for _, s := range states {
			if !slices.Contains(doc[id], s) {
				t.Errorf("references/doctor.md §%s doesn't explain the state %q, which doctor.go gives", id, s)
			}
		}
	}
	for id, states := range doc {
		for _, s := range states {
			if !slices.Contains(code[id], s) {
				t.Errorf("references/doctor.md §%s explains the state %q, which doctor.go never gives", id, s)
			}
		}
	}
}
