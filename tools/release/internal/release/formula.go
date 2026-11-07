package release

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Homepage is the public repository. A release's files are published at
// Homepage/releases/download/v<version>/<name>.
const Homepage = "https://github.com/MaverickHQ/herdr-pager"

// DownloadURL is where the release of version publishes the file name.
func DownloadURL(version, name string) string {
	return Homepage + "/releases/download/v" + version + "/" + name
}

// FormulaData is what the formula's template is filled with
// (contrib/homebrew/herdr-pager.rb.tmpl).
type FormulaData struct {
	Version string // the published version
	URL     string // the archive's download URL
	SHA256  string // the archive's sha256, in hex
}

// Formula fills the Homebrew formula's template tmpl for the archive at
// archivePath (design D5P-7): its version from its name, its URL on the
// release, its sha256. It refuses a source build's archive, and a template
// whose formula wouldn't pin the archive by both.
func Formula(archivePath string, tmpl []byte) ([]byte, error) {
	name := filepath.Base(archivePath)
	m := archiveRE.FindStringSubmatch(name)
	if m == nil {
		return nil, fmt.Errorf("%s isn't %s", name, ArchiveName("<version>"))
	}
	version := m[1]
	if err := CheckVersion(version); err != nil {
		return nil, err
	}
	if strings.HasSuffix(version, "-dev") {
		return nil, fmt.Errorf("%s isn't a published version: a formula installs a release's archive", version)
	}
	data, err := os.ReadFile(archivePath)
	if err != nil {
		return nil, err
	}
	d := FormulaData{
		Version: version,
		URL:     DownloadURL(version, name),
		SHA256:  fmt.Sprintf("%x", sha256.Sum256(data)),
	}
	t, err := template.New(filepath.Base(name)).Option("missingkey=error").Parse(string(tmpl))
	if err != nil {
		return nil, fmt.Errorf("the template: %w", err)
	}
	var out bytes.Buffer
	if err := t.Execute(&out, d); err != nil {
		return nil, fmt.Errorf("the template: %w", err)
	}
	rb := out.String()
	if !strings.Contains(rb, `url "`+d.URL+`"`) || !strings.Contains(rb, `sha256 "`+d.SHA256+`"`) {
		return nil, fmt.Errorf("the template's formula doesn't pin the archive: it needs url %q and sha256 %q", d.URL, d.SHA256)
	}
	return out.Bytes(), nil
}
