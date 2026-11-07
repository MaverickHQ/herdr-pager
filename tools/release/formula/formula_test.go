package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MaverickHQ/herdr-pager/tools/release/internal/fixture"
)

// The repository's template, which the release workflow fills.
var template = filepath.Join("..", "..", "..", "contrib", "homebrew", "herdr-pager.rb.tmpl")

func runFormula(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// archive writes a stand-in archive called name and returns its path and
// sha256.
func archive(t *testing.T, name string) (string, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	data := []byte("a stand-in for " + name)
	fixture.Write(t, p, data)
	return p, fmt.Sprintf("%x", sha256.Sum256(data))
}

func TestFormulaPinsTheArchive(t *testing.T) {
	path, sum := archive(t, "herdr-pager_9.8.7_darwin_arm64.tar.gz")
	code, rb, stderr := runFormula(t, "--archive", path, "--template", template)
	if code != 0 {
		t.Fatalf("formula: exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"class HerdrPager < Formula\n",
		// The archive the release workflow published, by its URL and sha256.
		`url "https://github.com/MaverickHQ/herdr-pager/releases/download/v9.8.7/herdr-pager_9.8.7_darwin_arm64.tar.gz"` + "\n",
		`sha256 "` + sum + `"` + "\n",
		`version "9.8.7"` + "\n",
		`license "Apache-2.0"` + "\n",
		"depends_on arch: :arm64\n",
		// bin and share as the archive lays them out, so the bridge finds
		// ../share/herdr-pager/firmware from its executable.
		`bin.install "bin/pager-bridge"` + "\n",
		`share.install "share/herdr-pager"` + "\n",
		`doc.install "THIRD_PARTY.md", "licenses"` + "\n",
		// brew services start herdr-pager runs serve at login, its log
		// under Homebrew's var/log.
		"service do\n",
		`run [opt_bin/"pager-bridge", "serve"]` + "\n",
		`log_path var/"log/herdr-pager.log"` + "\n",
		`error_log_path var/"log/herdr-pager.log"` + "\n",
		// brew test: the installed bridge says the formula's version.
		`assert_match "pager-bridge #{version}", shell_output("#{bin}/pager-bridge --version")` + "\n",
	} {
		if !strings.Contains(rb, want) {
			t.Errorf("the formula doesn't hold %q:\n%s", want, rb)
		}
	}
	if strings.Contains(rb, "{{") || strings.Contains(rb, "}}") {
		t.Errorf("the formula still holds a template action:\n%s", rb)
	}
	if strings.Count(rb, "sha256 ") != 1 || strings.Count(rb, "url ") != 1 {
		t.Errorf("the formula pins more than one download:\n%s", rb)
	}
}

func TestFormulaRefuses(t *testing.T) {
	good := fixture.Read(t, template)
	cases := []struct {
		name     string
		archive  string
		template []byte
		say      string
	}{
		{
			name:     "a source build's archive",
			archive:  "herdr-pager_9.8.7-dev_darwin_arm64.tar.gz",
			template: good,
			say:      "9.8.7-dev isn't a published version",
		},
		{
			name:     "a file that isn't the archive",
			archive:  "libssh-0.12.2-source.tar.gz",
			template: good,
			say:      "isn't herdr-pager_<version>_darwin_arm64.tar.gz",
		},
		{
			name:     "a template that doesn't pin the sha256",
			archive:  "herdr-pager_9.8.7_darwin_arm64.tar.gz",
			template: bytes.Replace(good, []byte("{{.SHA256}}"), []byte("TODO"), 1),
			say:      "doesn't pin the archive",
		},
		{
			name:     "a template that doesn't name the URL",
			archive:  "herdr-pager_9.8.7_darwin_arm64.tar.gz",
			template: bytes.Replace(good, []byte("{{.URL}}"), []byte("https://example.com/x.tar.gz"), 1),
			say:      "doesn't pin the archive",
		},
		{
			name:     "a template with an unknown field",
			archive:  "herdr-pager_9.8.7_darwin_arm64.tar.gz",
			template: append(bytes.Clone(good), []byte("# {{.Checksum}}\n")...),
			say:      "Checksum",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path, _ := archive(t, c.archive)
			tmpl := filepath.Join(t.TempDir(), "herdr-pager.rb.tmpl")
			fixture.Write(t, tmpl, c.template)
			code, stdout, stderr := runFormula(t, "--archive", path, "--template", tmpl)
			if code != 1 || stdout != "" || !strings.Contains(stderr, c.say) {
				t.Errorf("formula: exit %d, stdout %d bytes, stderr %q; want 1, nothing, saying %q", code, len(stdout), stderr, c.say)
			}
		})
	}
}

func TestFormulaUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"--archive", "x"}, {"--template", "x"}, {"--archive", "x", "--template", "y", "z"}} {
		if code, _, stderr := runFormula(t, args...); code != 2 || !strings.Contains(stderr, "usage: formula") {
			t.Errorf("formula %q: exit %d, stderr %q; want 2 and the usage", args, code, stderr)
		}
	}
}
