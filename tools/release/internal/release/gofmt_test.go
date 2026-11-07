package release

import (
	"bytes"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every Go file in tools/release is as gofmt writes it.
func TestSourcesAreGofmted(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out, err := format.Source(src)
		if err != nil {
			t.Errorf("%s: %v", p, err)
		} else if !bytes.Equal(out, src) {
			t.Errorf("%s isn't gofmt'd", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
