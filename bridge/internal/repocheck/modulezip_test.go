package repocheck

import (
	"strings"
	"testing"

	"golang.org/x/mod/zip"
)

// TestTheModuleZipLeavesNothingOut: `go install
// github.com/MaverickHQ/herdr-pager/bridge/cmd/pager-bridge@…` builds from the
// module's zip, not from a checkout, and a module zip leaves some files out:
// anything under a folder named vendor, nested modules, names it can't hold.
// A file the bridge embeds and the zip leaves out still builds, without it:
// the setup page's flasher was under web/vendor/ in v1.0.0 (phase 5's
// traceability, gap 1). The oracle is golang.org/x/mod/zip, the code the go
// command and the module proxy make the zip with.
func TestTheModuleZipLeavesNothingOut(t *testing.T) {
	files, err := zip.CheckDir(root)
	if err != nil && len(files.Omitted) == 0 && len(files.Invalid) == 0 && files.SizeError == nil {
		t.Fatal(err)
	}
	for _, f := range files.Omitted {
		t.Errorf("the module zip leaves out bridge/%s: %v", strings.TrimPrefix(f.Path, root+"/"), f.Err)
	}
	for _, f := range files.Invalid {
		t.Errorf("the module zip can't hold bridge/%s: %v", strings.TrimPrefix(f.Path, root+"/"), f.Err)
	}
	if files.SizeError != nil {
		t.Errorf("the module is too big for a zip: %v", files.SizeError)
	}
	if len(files.Valid) == 0 {
		t.Error("the module zip would hold no file at all")
	}
}
