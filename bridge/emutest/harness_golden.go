package emutest

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DiffMark is the colour a diff image gives each differing pixel. The rest of
// the diff is the golden, faded, so the marks stand out.
var DiffMark = color.RGBA{0xFF, 0x00, 0xFF, 0xFF}

// ErrGoldenWritten is returned when a golden didn't exist and was written
// from the screenshot: look at it, then run again (as the kit's scenarios do).
var ErrGoldenWritten = errors.New("golden written")

// OutDir is the test's output directory, bridge/emutest/out/<test name>/
// (gitignored), created if need be.
func OutDir(t testing.TB) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("out", safeName(t.Name())))
	if err == nil {
		err = os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		}
		return '_'
	}, s)
}

// Golden is the path of a golden under firmware/scenarios/, such as
// Golden("pairing/boot_unpaired.png").
func Golden(name string) string {
	return filepath.Join(FirmwareDir(), "scenarios", filepath.FromSlash(name))
}

// Diff compares got with want pixel by pixel, skipping every pixel inside a
// mask (the clock, ages). It returns how many pixels differ, and an image of
// want, faded, with each differing pixel in DiffMark.
func Diff(got, want image.Image, masks []image.Rectangle) (int, *image.RGBA, error) {
	b := want.Bounds()
	if got.Bounds() != b {
		return 0, nil, fmt.Errorf("the screenshot is %v, the golden %v", got.Bounds(), b)
	}
	diff := image.NewRGBA(b)
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if !masked(image.Pt(x, y), masks) && !sameColour(got.At(x, y), want.At(x, y)) {
				diff.SetRGBA(x, y, DiffMark)
				n++
				continue
			}
			diff.SetRGBA(x, y, fade(want.At(x, y)))
		}
	}
	return n, diff, nil
}

func masked(p image.Point, masks []image.Rectangle) bool {
	for _, m := range masks {
		if p.In(m) {
			return true
		}
	}
	return false
}

func sameColour(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func fade(c color.Color) color.RGBA {
	g := color.GrayModel.Convert(c).(color.Gray).Y
	v := 0x80 + g/2
	return color.RGBA{v, v, v, 0xFF}
}

// CheckGolden compares got with the golden PNG at golden, outside masks. On a
// mismatch it writes <name>_actual.png and <name>_diff.png into out and
// returns an error that counts the differing pixels and names both files. A
// golden that doesn't exist yet is written from got, with ErrGoldenWritten.
func CheckGolden(got image.Image, golden, out string, masks ...image.Rectangle) error {
	want, err := readPNG(golden)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			return err
		}
		if err := writePNG(golden, got); err != nil {
			return err
		}
		return fmt.Errorf("%w: %s; check it, then run again", ErrGoldenWritten, golden)
	}
	if err != nil {
		return err
	}
	n, diff, err := Diff(got, want, masks)
	if err == nil && n == 0 {
		return nil
	}
	base := strings.TrimSuffix(filepath.Base(golden), filepath.Ext(golden))
	actual := filepath.Join(out, base+"_actual.png")
	if werr := os.MkdirAll(out, 0o755); werr != nil {
		return werr
	}
	if werr := writePNG(actual, got); werr != nil {
		return werr
	}
	if err != nil {
		return fmt.Errorf("%s: %w; the screenshot is %s", golden, err, actual)
	}
	diffPath := filepath.Join(out, base+"_diff.png")
	if werr := writePNG(diffPath, diff); werr != nil {
		return werr
	}
	unit := "pixels"
	if n == 1 {
		unit = "pixel"
	}
	return fmt.Errorf("%s: %d %s differ outside the masks; see %s and %s", golden, n, unit, actual, diffPath)
}

// MatchGolden fails t unless got matches the golden at golden outside masks.
func MatchGolden(t testing.TB, got image.Image, golden string, masks ...image.Rectangle) {
	t.Helper()
	if err := CheckGolden(got, golden, OutDir(t), masks...); err != nil {
		t.Fatal(err)
	}
}

func readPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return img, nil
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
