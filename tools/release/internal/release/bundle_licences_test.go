package release

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestTheLicencesCoverTheSetupPagesBundle reads the repository itself. The
// setup page vendors ESP Web Tools' browser build, which bundles npm
// packages and keeps none of their licence comments, and pager-bridge embeds
// it. So every package in its lock file (but @types/*, which ship no code)
// has its licence text in licenses/, named by a row of licenses/README.md,
// and a row in THIRD_PARTY.md, with its version: task 6.2's "ESP Web Tools
// in THIRD_PARTY.md", found missing before 7.3's export (2026-11-07).
func TestTheLicencesCoverTheSetupPagesBundle(t *testing.T) {
	repo := filepath.Join("..", "..", "..", "..")
	locks, err := filepath.Glob(filepath.Join(repo, "bridge", "internal", "setup", "web", "esp-web-tools", "*", "package-lock.json"))
	if err != nil || len(locks) != 1 {
		t.Fatalf("the vendored ESP Web Tools' lock file: %v %v; want one", locks, err)
	}
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	data, err := os.ReadFile(locks[0])
	if err == nil {
		err = json.Unmarshal(data, &lock)
	}
	if err != nil || len(lock.Packages) == 0 {
		t.Fatalf("%s: %v, %d packages", locks[0], err, len(lock.Packages))
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	readme, third := read(LicensesSource+"/README.md"), read(ThirdParty)
	rowOf := func(doc, name, version string) []string {
		for _, line := range strings.Split(doc, "\n") {
			cells := strings.Split(line, "|")
			if len(cells) < 4 || !strings.Contains(line, "`"+name+"`") {
				continue
			}
			if strings.Contains(line, "`"+name+"` "+version) || slices.Contains(trimmed(cells), version) {
				return cells[1:]
			}
		}
		return nil
	}
	for key, p := range lock.Packages {
		name := "esp-web-tools"
		if key != "" {
			name = key[strings.LastIndex(key, "node_modules/")+len("node_modules/"):]
		}
		if strings.HasPrefix(name, "@types/") {
			continue
		}
		row := rowOf(readme, name, p.Version)
		if row == nil {
			t.Errorf("%s/README.md has no row for `%s` %s", LicensesSource, name, p.Version)
		} else {
			file := strings.Trim(strings.TrimSpace(row[0]), "`")
			if b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(path.Join(LicensesSource, file)))); err != nil || len(strings.TrimSpace(string(b))) == 0 {
				t.Errorf("`%s`'s licence file %s: %v, %d bytes", name, file, err, len(b))
			}
		}
		if rowOf(third, name, p.Version) == nil {
			t.Errorf("%s has no row for `%s` %s", ThirdParty, name, p.Version)
		}
	}
}

// trimmed is cells, each without its spaces.
func trimmed(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = strings.TrimSpace(c)
	}
	return out
}
