package emutest

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	ground = color.RGBA{0x8B, 0xAC, 0x0F, 0xFF}
	ink    = color.RGBA{0x0F, 0x38, 0x0F, 0xFF}
)

func solid(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func writeTestPNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestGoldenCompareHonoursMasks(t *testing.T) {
	golden := filepath.Join(t.TempDir(), "screen.png")
	writeTestPNG(t, golden, solid(64, 32, ground))
	mask := image.Rect(10, 5, 30, 15) // the clock, say: x 10-29, y 5-14
	out := OutDir(t)

	got := solid(64, 32, ground)
	got.Set(10, 5, ink)  // the mask's first pixel
	got.Set(29, 14, ink) // and its last (a rectangle's max is exclusive)
	if err := CheckGolden(got, golden, out, mask); err != nil {
		t.Fatalf("differences only inside the mask: %v", err)
	}

	got.Set(30, 14, ink) // one pixel right of the mask
	err := CheckGolden(got, golden, out, mask)
	if err == nil {
		t.Fatal("one pixel outside the mask differs, but the images compared equal")
	}
	if !strings.Contains(err.Error(), "1 pixel") {
		t.Errorf("error %q doesn't count 1 pixel", err)
	}
	if n, _, err := Diff(got, solid(64, 32, ground), []image.Rectangle{mask}); err != nil || n != 1 {
		t.Errorf("Diff counts %d differing pixels (err %v), want exactly 1", n, err)
	}

	wantDir, _ := filepath.Abs("out")
	diffPath := filepath.Join(out, "screen_diff.png")
	if !strings.HasPrefix(diffPath, wantDir+string(filepath.Separator)) {
		t.Errorf("diff %s isn't under bridge/emutest/out (%s)", diffPath, wantDir)
	}
	f, err := os.Open(diffPath)
	if err != nil {
		t.Fatalf("no diff image: %v", err)
	}
	defer f.Close()
	diff, err := png.Decode(f)
	if err != nil {
		t.Fatalf("diff image: %v", err)
	}
	if c := color.RGBAModel.Convert(diff.At(30, 14)).(color.RGBA); c != DiffMark {
		t.Errorf("diff at the differing pixel is %v, want %v", c, DiffMark)
	}
	for _, p := range []image.Point{{10, 5}, {29, 14}, {0, 0}} {
		if c := color.RGBAModel.Convert(diff.At(p.X, p.Y)).(color.RGBA); c == DiffMark {
			t.Errorf("diff marks %v, which matches or is masked", p)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "screen_actual.png")); err != nil {
		t.Errorf("no actual image beside the diff: %v", err)
	}
}

// A golden that doesn't exist yet is written from the screenshot, and the
// test fails once, so a new golden is always looked at before it counts.
func TestGoldenMissingIsWrittenAndFailsOnce(t *testing.T) {
	golden := filepath.Join(t.TempDir(), "pairing", "new.png")
	shot := solid(64, 32, ground)
	shot.Set(3, 4, ink)

	err := CheckGolden(shot, golden, OutDir(t))
	if !errors.Is(err, ErrGoldenWritten) {
		t.Fatalf("a missing golden: got %v, want ErrGoldenWritten", err)
	}
	if err := CheckGolden(shot, golden, OutDir(t)); err != nil {
		t.Fatalf("the written golden doesn't match the screenshot it came from: %v", err)
	}
}

// A screenshot of another size never matches, even under a mask that covers
// all of it, and is kept for a look.
func TestGoldenOfAnotherSizeDiffers(t *testing.T) {
	golden := filepath.Join(t.TempDir(), "screen.png")
	writeTestPNG(t, golden, solid(64, 32, ground))
	out := OutDir(t)

	err := CheckGolden(solid(64, 33, ground), golden, out, image.Rect(0, 0, 64, 33))
	if err == nil {
		t.Fatal("a 64x33 screenshot matched a 64x32 golden")
	}
	if _, err := os.Stat(filepath.Join(out, "screen_actual.png")); err != nil {
		t.Errorf("no actual image kept: %v", err)
	}
}
