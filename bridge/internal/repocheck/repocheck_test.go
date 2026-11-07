package repocheck

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// root is the bridge's module, from this package.
const root = "../.."

// bridgePackages returns, by directory under the module, the parsed non-test
// files of cmd/pager-bridge and of every package of this module it imports,
// directly or not: what pager-bridge is built from. Test tools (fakeherdr,
// emutest) and test-only packages aren't in it.
func bridgePackages(t *testing.T) (*token.FileSet, map[string][]*ast.File) {
	t.Helper()
	module := modulePath(t)
	fset := token.NewFileSet()
	pkgs := map[string][]*ast.File{}
	var visit func(dir string)
	visit = func(dir string) {
		if _, seen := pkgs[dir]; seen {
			return
		}
		pkgs[dir] = nil
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		var files []*ast.File
		for _, e := range entries {
			if name := e.Name(); strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
				src, err := os.ReadFile(filepath.Join(root, dir, name))
				if err != nil {
					t.Fatal(err)
				}
				f, err := parser.ParseFile(fset, filepath.Join("bridge", dir, name), src, parser.ParseComments)
				if err != nil {
					t.Fatal(err)
				}
				files = append(files, f)
			}
		}
		pkgs[dir] = files
		for _, f := range files {
			for _, imp := range f.Imports {
				if p, _ := strconv.Unquote(imp.Path.Value); strings.HasPrefix(p, module+"/") {
					visit(strings.TrimPrefix(p, module+"/"))
				}
			}
		}
	}
	visit("cmd/pager-bridge")
	return fset, pkgs
}

// modulePath is the module line of the bridge's go.mod.
func modulePath(t *testing.T) string {
	t.Helper()
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for s := bufio.NewScanner(f); s.Scan(); {
		if m, ok := strings.CutPrefix(s.Text(), "module "); ok {
			return strings.TrimSpace(m)
		}
	}
	t.Fatal("go.mod has no module line")
	return ""
}

// parseDir parses every Go file in dir, one package of testdata.
func parseDir(t *testing.T, dir string) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, e := range entries {
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	return fset, files
}

// marked returns the file:line of each line in files whose comment says
// "refused".
func marked(fset *token.FileSet, files []*ast.File) []string {
	var lines []string
	for _, f := range files {
		for _, g := range f.Comments {
			for _, c := range g.List {
				if strings.HasPrefix(strings.TrimPrefix(c.Text, "// "), "refused") {
					p := fset.Position(c.Pos())
					lines = append(lines, p.Filename+":"+strconv.Itoa(p.Line))
				}
			}
		}
	}
	slices.Sort(lines)
	return lines
}

// where returns the file:line of each of problems, which start with it.
func where(problems []string) []string {
	var lines []string
	for _, p := range problems {
		file, rest, _ := strings.Cut(p, ":")
		line, _, _ := strings.Cut(rest, ":")
		lines = append(lines, file+":"+line)
	}
	slices.Sort(lines)
	return slices.Compact(lines)
}

// TestNoSubprocessWithDerivedArguments checks parent 13.3: the bridge's only
// process launches are `scutil --get LocalHostName` (lane cli's
// LocalHostName), since the acceptance day (2026-10-25) `networksetup
// -listpreferredwirelessnetworks` on en0 or en1 (provision's KnownNetworks),
// and, from phase 5 (design D5P-3 §Never), the doctor's `socketfilterfw` and
// `brew services` and run at login's `brew services start|stop herdr-pager`,
// each with an argument vector of constants; and the setup page's `open` of
// its own URL (TestOpenOpensOnlyThePage). So no agent- or device-derived
// string can reach a subprocess. The check reads the source of what
// pager-bridge is built from; it accepts a launch only when it can show every
// vector the launch can get is one of allowedVectors, and refuses every launch
// in its own refused examples, and none in its allowed ones (testdata/launch).
func TestNoSubprocessWithDerivedArguments(t *testing.T) {
	fset, files := parseDir(t, "testdata/launch/allowed")
	if problems := checkLaunches(fset, "testdata/launch/allowed", files); len(problems) > 0 {
		t.Errorf("testdata/launch/allowed: the check refused %q; want none", problems)
	}
	fset, files = parseDir(t, "testdata/launch/refused")
	refused := checkLaunches(fset, "testdata/launch/refused", files)
	if got, want := where(refused), marked(fset, files); !slices.Equal(got, want) {
		t.Errorf("testdata/launch/refused: the check refused %q; want %q", got, want)
	}
	t.Logf("testdata/launch/refused:\n%s", strings.Join(refused, "\n"))

	fset, pkgs := bridgePackages(t)
	for _, dir := range []string{"cmd/pager-bridge", "internal/sshserver", "internal/session", "internal/herdr", "internal/watch"} {
		if len(pkgs[dir]) == 0 {
			t.Fatalf("pager-bridge is built from %d packages, without %s: the walk is broken", len(pkgs), dir)
		}
	}
	launches := 0
	for dir, files := range pkgs {
		launches += countLaunches(fset, files)
		if problems := checkLaunches(fset, dir, files); len(problems) > 0 {
			t.Errorf("%s: %s", dir, strings.Join(problems, "\n"))
		}
	}
	t.Logf("pager-bridge: %d packages, %d process launches", len(pkgs), launches)
}

// TestOpenOpensOnlyThePage checks the one launch phase 5 adds whose vector
// isn't all constants: the setup page's `open <its own URL>` (design D5P-3
// §Never, task 3.11). The check allows it only as
// exec.Command("/usr/bin/open", pageURL(…)), or CommandContext, in
// internal/setup, where pageURL is that package's own function, which builds
// the URL from 127.0.0.1, the listener's port and the token. It allows
// testdata/launch/page as internal/setup and refuses each of its launches as
// any other package; as internal/setup, it refuses exactly the marked lines of
// testdata/launch/pagerefused (a literal, open by name, a variable, a wrapper,
// another function, a parameter, a function value, extra arguments, a
// shadowing pageURL, a method, another program) and testdata/launch/pagevar
// (pageURL as a variable).
func TestOpenOpensOnlyThePage(t *testing.T) {
	fset, files := parseDir(t, "testdata/launch/page")
	t.Run("page as "+pagePackage, func(t *testing.T) {
		if problems := checkLaunches(fset, pagePackage, files); len(problems) > 0 {
			t.Errorf("testdata/launch/page as %s: the check refused %q; want none", pagePackage, problems)
		}
	})
	for _, dir := range []string{"testdata/launch/page", "internal/doctor", "internal/setup/web", "cmd/pager-bridge"} {
		t.Run("page as "+dir, func(t *testing.T) {
			if got, want := where(checkLaunches(fset, dir, files)), marked(fset, files); !slices.Equal(got, want) {
				t.Errorf("testdata/launch/page as %s: the check refused %q; want %q", dir, got, want)
			}
		})
	}
	for _, dir := range []string{"testdata/launch/pagerefused", "testdata/launch/pagevar"} {
		t.Run(filepath.Base(dir)+" as "+pagePackage, func(t *testing.T) {
			fset, files := parseDir(t, dir)
			refused := checkLaunches(fset, pagePackage, files)
			if got, want := where(refused), marked(fset, files); !slices.Equal(got, want) {
				t.Errorf("%s as %s: the check refused %q; want %q", dir, pagePackage, got, want)
			}
			t.Logf("%s as %s:\n%s", dir, pagePackage, strings.Join(refused, "\n"))
		})
	}
}

// TestFirewallAsksOnlyAboutTheBridge checks the doctor's one launch with an
// argument that isn't a literal (the owner's checkpoint 7.2, 2026-11-07):
// `socketfilterfw --getappblocked <the bridge's own path>`, the path as the
// direct result of ownPath, internal/doctor's own function. As
// internal/doctor, the check refuses exactly the marked launches of
// testdata/launch/firewall; as any other package, the unmarked ones too.
func TestFirewallAsksOnlyAboutTheBridge(t *testing.T) {
	fset, files := parseDir(t, "testdata/launch/firewall")
	refused := checkLaunches(fset, ownPathPackage, files)
	if got, want := where(refused), marked(fset, files); !slices.Equal(got, want) {
		t.Errorf("testdata/launch/firewall as %s: the check refused %q; want %q", ownPathPackage, got, want)
	}
	t.Logf("testdata/launch/firewall as %s:\n%s", ownPathPackage, strings.Join(refused, "\n"))
	if got, want := len(checkLaunches(fset, pagePackage, files)), len(marked(fset, files))+2; got != want {
		t.Errorf("testdata/launch/firewall as %s: the check refused %d launches; want %d (ownPath is the doctor's alone)", pagePackage, got, want)
	}
}

// TestNoCustomCryptography checks parent 13.7: what pager-bridge is built
// from imports no crypto package but golang.org/x/crypto/ssh, crypto/ed25519,
// crypto/sha256 and crypto/rand. The check refuses exactly the refused
// imports of testdata/crypto.
func TestNoCustomCryptography(t *testing.T) {
	fset, files := parseDir(t, "testdata/crypto")
	if got, want := where(checkCrypto(fset, files)), marked(fset, files); !slices.Equal(got, want) {
		t.Errorf("testdata/crypto: the check refused %q; want %q", got, want)
	}

	fset, pkgs := bridgePackages(t)
	for dir, files := range pkgs {
		if problems := checkCrypto(fset, files); len(problems) > 0 {
			t.Errorf("%s: %s", dir, strings.Join(problems, "\n"))
		}
	}
}

// allowedVectors are the argument vectors a launch may have (parent 13.3):
// scutil's, by name or by path; networksetup's list of the Mac's saved Wi-Fi
// networks, for provision's near-miss warning (the owner's, on the acceptance
// day, 2026-10-25); and, from phase 5, the doctor's socketfilterfw and the
// login item's brew services, with fixed arguments (phase 5 design D5P-3
// §Never). Each word is matched as a string literal at the launch or at the
// calls that fill it, so a named constant (doctor's Formula) won't do. The
// setup page's open isn't here: opensPage is its one form.
var allowedVectors = [][]string{
	{"scutil", "--get", "LocalHostName"},
	{"/usr/sbin/scutil", "--get", "LocalHostName"},
	{"/usr/sbin/networksetup", "-listpreferredwirelessnetworks", "en0"},
	{"/usr/sbin/networksetup", "-listpreferredwirelessnetworks", "en1"},
	// The doctor's firewall check, about the bridge's own path (phase 5
	// design D5P-3 §Never, task 3.1): the global state, the block all state,
	// and its verdict on that path, ownPath's direct result (the owner's
	// checkpoint 7.2, 2026-11-07: --listapps never lists an unsigned binary).
	{"/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate"},
	{"/usr/libexec/ApplicationFirewall/socketfilterfw", "--getblockall"},
	{"/usr/libexec/ApplicationFirewall/socketfilterfw", "--getappblocked", ownPathArg},
	// The doctor's login-item check (D5P-3 §Never, task 3.1), by name, and by
	// path for a PATH without brew (a launchd job's).
	{"brew", "services", "list", "--json"},
	{"/opt/homebrew/bin/brew", "services", "list", "--json"},
	// Run at login on and off, from the page's Manage view (D5P-3 §Never,
	// task 3.8), by name and by path.
	{"brew", "services", "start", "herdr-pager"},
	{"/opt/homebrew/bin/brew", "services", "start", "herdr-pager"},
	{"brew", "services", "stop", "herdr-pager"},
	{"/opt/homebrew/bin/brew", "services", "stop", "herdr-pager"},
}

// allowedCrypto are the only crypto packages the bridge may import (parent
// 13.7). crypto/subtle is the standard library's constant-time comparison,
// which the setup page's guard uses for its token (phase 5 task 3.2): not
// custom cryptography but the way to avoid writing one (open-items O-93).
var allowedCrypto = []string{"golang.org/x/crypto/ssh", "crypto/ed25519", "crypto/sha256", "crypto/rand", "crypto/subtle"}

// checkCrypto returns, as "file:line: why", each crypto import in files
// that parent 13.7 doesn't allow.
func checkCrypto(fset *token.FileSet, files []*ast.File) []string {
	var problems []string
	for _, f := range files {
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			crypto := p == "crypto" || strings.HasPrefix(p, "crypto/") || p == "golang.org/x/crypto" || strings.HasPrefix(p, "golang.org/x/crypto/")
			if crypto && !slices.Contains(allowedCrypto, p) {
				problems = append(problems, fmt.Sprintf("%s: imports %s; only %s are allowed (parent 13.7)",
					fset.Position(imp.Pos()), p, strings.Join(allowedCrypto, ", ")))
			}
		}
	}
	return problems
}

// launchImports are the packages that can launch a process. x/sys/unix is
// the module's own dependency (lane cli's termios), so it's watched too.
var launchImports = []string{"os/exec", "os", "syscall", "golang.org/x/sys/unix"}

// importNames returns the name each of launchImports has in f, if f imports
// it, and a problem for each that f imports with a dot, which hides its calls.
func importNames(fset *token.FileSet, f *ast.File) (map[string]string, []string) {
	names := map[string]string{}
	var problems []string
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if !slices.Contains(launchImports, p) {
			continue
		}
		names[p] = filepath.Base(p)
		if imp.Name != nil {
			names[p] = imp.Name.Name
		}
		if names[p] == "." {
			problems = append(problems, fmt.Sprintf("%s: imports %s with a dot, which hides its launches", fset.Position(imp.Pos()), p))
		}
	}
	return names, problems
}

// qualified returns pkg and name for an expression pkg.name.
func qualified(e ast.Expr) (pkg, name string) {
	if s, ok := e.(*ast.SelectorExpr); ok {
		if x, ok := s.X.(*ast.Ident); ok {
			return x.Name, s.Sel.Name
		}
	}
	return "", ""
}

// parentsOf maps each node of f to the node around it.
func parentsOf(f *ast.File) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) > 0 {
			parents[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	})
	return parents
}

// The setup page's `open <its own URL>` (phase 5 design D5P-3 §Never, task
// 3.11): pageOpener with the direct result of a call of pageURLFunc, only in
// pagePackage (by directory under the module), where pageURLFunc is that
// package's own function. No vector of open is in allowedVectors, so open
// with a literal is refused: the page's URL has a random port and token, so
// no literal can be it, and a literal could open any file or app.
const (
	pagePackage = "internal/setup"
	pageOpener  = "/usr/bin/open"
	pageURLFunc = "pageURL"
)

// openRule is what a refusal of a launch of open adds.
var openRule = fmt.Sprintf("open's one allowed launch is exec.Command(%q, %s(…)) in %s, with %[2]s that package's own function (phase 5 design D5P-3 §Never)",
	pageOpener, pageURLFunc, pagePackage)

// opensPage reports whether a launch with args (CommandContext's without its
// context) is the setup page's `open <its own URL>`: in pagePackage, exactly
// pageOpener and the direct result of a call of pageURLFunc, the package's
// own function (not a method, a variable, or a parameter or local that
// shadows it). The check trusts what pageURL builds (127.0.0.1, the
// listener's port and the token): that's its package's reviewed code and
// tests.
func (pkg launchPkg) opensPage(args []ast.Expr, ellipsis bool) bool {
	if pkg.dir != pagePackage || ellipsis || len(args) != 2 {
		return false
	}
	if v, ok := constants(args[:1], false); !ok || v[0] != pageOpener {
		return false
	}
	return pkg.callsOwn(args[1], pageURLFunc)
}

// callsOwn reports whether e is a direct call of name, the package's own
// function (not a method, a variable, or a parameter or local that shadows
// it).
func (pkg launchPkg) callsOwn(e ast.Expr, name string) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	decl := pkg.funcs[name]
	if !ok || id.Name != name || decl == nil {
		return false
	}
	// The parser resolves a name to its declaration within a file, and
	// leaves unresolved one that's declared at the package level in another
	// file. So a parameter or local of that name resolves to itself, not to
	// decl.
	return id.Obj == nil || id.Obj.Decl == decl
}

// The doctor's question to the firewall about the bridge's own path (the
// owner's checkpoint 7.2, 2026-11-07): a launch's last argument may be the
// direct result of a call of ownPathFunc, in ownPathPackage only, where it's
// that package's own function, returning the path System resolved from
// os.Executable. It stands in a vector as ownPathArg, which allowedVectors
// holds only after socketfilterfw --getappblocked; constants refuses a
// literal holding NUL, so no literal can be ownPathArg.
const (
	ownPathPackage = "internal/doctor"
	ownPathFunc    = "ownPath"
	ownPathArg     = "\x00ownPath(…)"
)

// argConstants is constants, and, in ownPathPackage, constants followed by
// a direct call of ownPathFunc, which stands as ownPathArg.
func (pkg launchPkg) argConstants(args []ast.Expr, ellipsis bool) ([]string, bool) {
	if v, ok := constants(args, ellipsis); ok {
		return v, true
	}
	if pkg.dir != ownPathPackage || ellipsis || len(args) == 0 || !pkg.callsOwn(args[len(args)-1], ownPathFunc) {
		return nil, false
	}
	v, ok := constants(args[:len(args)-1], false)
	if !ok {
		return nil, false
	}
	return append(v, ownPathArg), true
}

// launchesOpen reports whether args name open as the program, by name or by
// path.
func launchesOpen(args []ast.Expr) bool {
	v, ok := constants(args[:min(1, len(args))], false)
	return ok && len(v) == 1 && path.Base(v[0]) == "open"
}

// A launchPkg is one package's files, as checkLaunches reads them.
type launchPkg struct {
	dir     string // under the module
	files   []*ast.File
	parents map[ast.Node]ast.Node    // across its files
	funcs   map[string]*ast.FuncDecl // its functions (not methods), by name
}

// checkLaunches returns, as "file:line: why", each process launch in files
// (one package) whose argument vectors it can't show are all allowed. A
// launch is a call of exec.Command or exec.CommandContext; os.StartProcess,
// syscall.Exec, syscall.ForkExec, unix.Exec, an exec.Cmd built by hand (not
// through a *exec.Cmd), and exec.Command or exec.CommandContext used other
// than in a call (which hides the calls) are always refused. A launch's
// vector is shown when its arguments are constants, or constants and one
// parameter of the function around it that's only ever called with
// constants: a function of the package called by name, or a function value
// passed straight to one that calls it by its parameter (lane cli's
// LocalHostName). One more launch is allowed, depending on dir, the
// package's directory under the module: the setup page's open, as opensPage
// says. Anything else is refused.
func checkLaunches(fset *token.FileSet, dir string, files []*ast.File) []string {
	pkg := launchPkg{dir: dir, files: files, parents: map[ast.Node]ast.Node{}, funcs: map[string]*ast.FuncDecl{}}
	for _, f := range files {
		for n, p := range parentsOf(f) {
			pkg.parents[n] = p
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
				pkg.funcs[fd.Name.Name] = fd
			}
		}
	}
	var problems []string
	for _, f := range files {
		names, dots := importNames(fset, f)
		problems = append(problems, dots...)
		is := func(path string, pkg string) bool { return pkg != "" && names[path] == pkg }
		ast.Inspect(f, func(n ast.Node) bool {
			why := ""
			switch n := n.(type) {
			case *ast.CallExpr:
				p, fn := qualified(n.Fun)
				switch {
				case is("os/exec", p) && (fn == "Command" || fn == "CommandContext"):
					args := n.Args
					if fn == "CommandContext" && len(args) > 0 {
						args = args[1:]
					}
					if pkg.opensPage(args, n.Ellipsis.IsValid()) {
						break
					}
					vectors, err := pkg.vectorsOf(n, args, n.Ellipsis.IsValid())
					why = err
					for _, v := range vectors {
						if why == "" && !slices.ContainsFunc(allowedVectors, func(a []string) bool { return slices.Equal(a, v) }) {
							why = fmt.Sprintf("launches %q, not one of allowedVectors", v)
						}
					}
					if why != "" && launchesOpen(args) {
						why += "; " + openRule
					}
				case is("os", p) && fn == "StartProcess", is("syscall", p) && (fn == "Exec" || fn == "ForkExec" || fn == "StartProcess"),
					is("golang.org/x/sys/unix", p) && fn == "Exec":
					why = p + "." + fn + ": launch only with exec.Command and constants"
				}
			case *ast.SelectorExpr:
				p, name := qualified(n)
				switch parent := pkg.parents[n]; {
				case is("os/exec", p) && (name == "Command" || name == "CommandContext"):
					if call, ok := parent.(*ast.CallExpr); !ok || call.Fun != n {
						why = "exec." + name + " used other than in a call, so its calls can't all be seen"
					}
				case is("os/exec", p) && name == "Cmd":
					if _, ok := parent.(*ast.StarExpr); !ok {
						why = "an exec.Cmd built by hand"
					}
				}
			}
			if why != "" {
				problems = append(problems, fmt.Sprintf("%s: %s", fset.Position(n.Pos()), why))
			}
			return true
		})
	}
	return problems
}

// vectorsOf returns every argument vector the launch with args can get, or
// why it can't show them.
func (pkg launchPkg) vectorsOf(launch ast.Node, args []ast.Expr, ellipsis bool) ([][]string, string) {
	if v, ok := pkg.argConstants(args, ellipsis); ok {
		return [][]string{v}, ""
	}
	param := paramUsed(args, ellipsis)
	var fn ast.Node
	for p := pkg.parents[launch]; p != nil && fn == nil; p = pkg.parents[p] {
		switch p.(type) {
		case *ast.FuncLit, *ast.FuncDecl:
			fn = p
		}
	}
	if param == "" || fn == nil {
		return nil, "an argument that isn't a constant"
	}
	var ft *ast.FuncType
	switch fn := fn.(type) {
	case *ast.FuncLit:
		ft = fn.Type
	case *ast.FuncDecl:
		ft = fn.Type
	}
	idx, variadic := paramIndex(ft, param)
	if idx < 0 {
		return nil, "an argument that isn't a constant or a parameter"
	}
	lists, why := pkg.callLists(fn, idx, variadic)
	if why != "" {
		return nil, why
	}
	var vectors [][]string
	for _, list := range lists {
		v, ok := bind(args, ellipsis, param, list, variadic)
		if !ok {
			return nil, fmt.Sprintf("its arguments don't fit the constants %q it's called with", list)
		}
		vectors = append(vectors, v)
	}
	return vectors, ""
}

// callLists returns the constants each call of fn passes for its parameter
// idx (from idx on, if it's variadic), or why it can't show them all.
func (pkg launchPkg) callLists(fn ast.Node, idx int, variadic bool) ([][]string, string) {
	switch fn := fn.(type) {
	case *ast.FuncDecl:
		if fn.Name.IsExported() {
			return nil, fn.Name.Name + " is exported, so its callers can't all be seen"
		}
		return pkg.callsOf(fn.Name.Name, fn.Name, pkg.files, idx, variadic)
	case *ast.FuncLit:
		call, ok := pkg.parents[fn].(*ast.CallExpr)
		if !ok {
			return nil, "a function value that isn't passed straight to a function of the package"
		}
		g, ok := call.Fun.(*ast.Ident)
		j := slices.Index(call.Args, ast.Expr(fn))
		if !ok || pkg.funcs[g.Name] == nil || j < 0 {
			return nil, "a function value that isn't passed straight to a function of the package"
		}
		gd := pkg.funcs[g.Name]
		r, _ := paramName(gd.Type, j)
		if r == "" {
			return nil, "a function value passed where " + g.Name + " has no parameter"
		}
		return pkg.callsOf(r, nil, []ast.Node{gd.Body}, idx, variadic)
	}
	return nil, "no function around it"
}

// callsOf returns the constants that each call by name, within scopes,
// passes from argument idx on (variadic) or at idx. Every use of name but
// decl must be such a call, and there must be one.
func (pkg launchPkg) callsOf(name string, decl *ast.Ident, scopes any, idx int, variadic bool) ([][]string, string) {
	var nodes []ast.Node
	switch s := scopes.(type) {
	case []*ast.File:
		for _, f := range s {
			nodes = append(nodes, f)
		}
	case []ast.Node:
		nodes = s
	}
	var lists [][]string
	why := ""
	for _, scope := range nodes {
		ast.Inspect(scope, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || id.Name != name || id == decl || why != "" {
				return true
			}
			if sel, ok := pkg.parents[id].(*ast.SelectorExpr); ok && sel.Sel == id {
				return true // x.name: another name
			}
			call, ok := pkg.parents[id].(*ast.CallExpr)
			if !ok || call.Fun != id {
				why = fmt.Sprintf("%s is used other than in calls, so its calls can't all be seen", name)
				return true
			}
			args := call.Args
			if variadic && idx <= len(args) {
				args = args[idx:]
			} else if !variadic && idx < len(args) {
				args = args[idx : idx+1]
			} else {
				args = nil
			}
			list, ok := pkg.argConstants(args, call.Ellipsis.IsValid())
			if !ok {
				why = fmt.Sprintf("a call of %s passes something other than constants", name)
				return true
			}
			lists = append(lists, list)
			return true
		})
	}
	if why == "" && len(lists) == 0 {
		why = name + " is never called, so what it's called with can't be seen"
	}
	return lists, why
}

// constants returns args as strings, if each is a string literal.
func constants(args []ast.Expr, ellipsis bool) ([]string, bool) {
	if ellipsis {
		return nil, false
	}
	var v []string
	for _, a := range args {
		lit, ok := a.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return nil, false
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil || strings.Contains(s, "\x00") {
			return nil, false
		}
		v = append(v, s)
	}
	return v, true
}

// paramUsed returns the one name that args use besides string literals, as
// p, p[i], or last and spread, p... or p[i:]...; or "" if they use more, or
// anything else.
func paramUsed(args []ast.Expr, ellipsis bool) string {
	name := ""
	use := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		if ok && (name == "" || name == id.Name) {
			name = id.Name
			return true
		}
		return false
	}
	for i, a := range args {
		last := i == len(args)-1
		switch a := a.(type) {
		case *ast.BasicLit:
			if a.Kind != token.STRING {
				return ""
			}
		case *ast.Ident:
			if !use(a) {
				return ""
			}
		case *ast.IndexExpr:
			if lit, ok := a.Index.(*ast.BasicLit); !ok || lit.Kind != token.INT || !use(a.X) {
				return ""
			}
		case *ast.SliceExpr:
			lit, ok := a.Low.(*ast.BasicLit)
			if !last || !ellipsis || a.High != nil || a.Slice3 || (a.Low != nil && (!ok || lit.Kind != token.INT)) || !use(a.X) {
				return ""
			}
		default:
			return ""
		}
	}
	return name
}

// paramIndex returns the position of parameter name in ft, and whether it's
// variadic; -1 if ft has none by that name.
func paramIndex(ft *ast.FuncType, name string) (int, bool) {
	i := 0
	for _, field := range ft.Params.List {
		_, variadic := field.Type.(*ast.Ellipsis)
		for _, n := range field.Names {
			if n.Name == name {
				return i, variadic
			}
			i++
		}
	}
	return -1, false
}

// paramName returns the name of ft's parameter at position idx, "" if none.
func paramName(ft *ast.FuncType, idx int) (string, bool) {
	i := 0
	for _, field := range ft.Params.List {
		for _, n := range field.Names {
			if i == idx {
				return n.Name, true
			}
			i++
		}
	}
	return "", false
}

// bind returns the vector args make when param holds list: the whole list
// if variadic, else its one string.
func bind(args []ast.Expr, ellipsis bool, param string, list []string, variadic bool) ([]string, bool) {
	var v []string
	for i, a := range args {
		spread := ellipsis && i == len(args)-1
		switch a := a.(type) {
		case *ast.BasicLit:
			s, _ := strconv.Unquote(a.Value)
			v = append(v, s)
		case *ast.Ident:
			switch {
			case variadic && spread:
				v = append(v, list...)
			case !variadic && !spread && len(list) == 1:
				v = append(v, list[0])
			default:
				return nil, false
			}
		case *ast.IndexExpr:
			k, err := strconv.Atoi(a.Index.(*ast.BasicLit).Value)
			if !variadic || spread || err != nil || k >= len(list) {
				return nil, false
			}
			v = append(v, list[k])
		case *ast.SliceExpr:
			k := 0
			if a.Low != nil {
				k, _ = strconv.Atoi(a.Low.(*ast.BasicLit).Value)
			}
			if !variadic || !spread || k > len(list) {
				return nil, false
			}
			v = append(v, list[k:]...)
		default:
			return nil, false
		}
	}
	return v, len(v) > 0
}

// countLaunches counts the calls of exec.Command and exec.CommandContext in
// files.
func countLaunches(fset *token.FileSet, files []*ast.File) int {
	n := 0
	for _, f := range files {
		names, _ := importNames(fset, f)
		ast.Inspect(f, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				if p, fn := qualified(call.Fun); p != "" && p == names["os/exec"] && (fn == "Command" || fn == "CommandContext") {
					n++
				}
			}
			return true
		})
	}
	return n
}
