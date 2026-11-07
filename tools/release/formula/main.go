// Command formula writes herdr-pager's Homebrew formula for a release's
// archive (phase 5 task 4.3, requirement 3.2, design D5P-7):
//
//	cd tools/release && go run ./formula \
//	    --archive <dir>/herdr-pager_1.0.0_darwin_arm64.tar.gz \
//	    --template ../../contrib/homebrew/herdr-pager.rb.tmpl > herdr-pager.rb
//
// It fills the template with the archive's version (from its name), its URL
// on the release (tag v<version>) and its sha256, and prints the formula. The
// release workflow runs it and keeps the formula as an artefact; the formula
// reaches the tap only by the owner's approval (task 9.4).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/MaverickHQ/herdr-pager/tools/release/internal/release"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

const usage = `usage: formula --archive <file> --template <file>

Prints the Homebrew formula for the release's archive (herdr-pager_<version>_darwin_arm64.tar.gz),
filled in from the template (contrib/homebrew/herdr-pager.rb.tmpl).
`

func run(args []string, stdout, stderr io.Writer) int {
	var archive, tmpl string
	fs := flag.NewFlagSet("formula", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	fs.StringVar(&archive, "archive", "", "")
	fs.StringVar(&tmpl, "template", "", "")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || archive == "" || tmpl == "" {
		fs.Usage()
		return 2
	}
	t, err := os.ReadFile(tmpl)
	if err != nil {
		fmt.Fprintf(stderr, "formula: %v\n", err)
		return 1
	}
	rb, err := release.Formula(archive, t)
	if err != nil {
		fmt.Fprintf(stderr, "formula: %v\n", err)
		return 1
	}
	if _, err := stdout.Write(rb); err != nil {
		fmt.Fprintf(stderr, "formula: %v\n", err)
		return 1
	}
	return 0
}
