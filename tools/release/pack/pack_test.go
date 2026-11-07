package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/tools/release/internal/fixture"
)

// entry is one member of a tar archive, as the test reads it (with its own
// reader, not the tool's).
type entry struct {
	typ  byte
	mode int64
	data []byte
}

func readArchive(t *testing.T, path string) map[string]entry {
	t.Helper()
	got := map[string]entry{}
	for _, e := range fixture.ReadTarGz(t, path) {
		if _, dup := got[e.Name]; dup {
			t.Errorf("%s holds %s twice", path, e.Name)
		}
		got[e.Name] = entry{typ: e.Type, mode: e.Mode, data: e.Data}
	}
	return got
}

func runPack(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func packArgs(in fixture.Inputs, version, out string) []string {
	return []string{"--version", version, "--bridge", in.Bridge, "--firmware", in.Firmware, "--libssh", in.Libssh, "--repo", in.Repo, "--out", out}
}

func TestPackLaysOutTheArchive(t *testing.T) {
	in := fixture.New(t, "9.8.7")
	out := filepath.Join(t.TempDir(), "dist")
	code, stdout, stderr := runPack(t, packArgs(in, "9.8.7", out)...)
	if code != 0 {
		t.Fatalf("pack: exit %d, stderr %q", code, stderr)
	}
	path := filepath.Join(out, "herdr-pager_9.8.7_darwin_arm64.tar.gz")
	if first, _, _ := strings.Cut(stdout, "\n"); first != path {
		t.Errorf("pack printed %q; want the archive's path first, %q", stdout, path)
	}
	got := readArchive(t, path)

	const root = "herdr-pager-9.8.7/"
	const fw = root + "share/herdr-pager/firmware/"
	files := map[string][]byte{
		root + "bin/pager-bridge":  fixture.Read(t, in.Bridge),
		fw + "bootloader.bin":      fixture.Read(t, filepath.Join(in.Firmware, "bootloader", "bootloader.bin")),
		fw + "partition-table.bin": fixture.Read(t, filepath.Join(in.Firmware, "partition_table", "partition-table.bin")),
		fw + "herdr-pager.bin":     fixture.Read(t, filepath.Join(in.Firmware, "herdr-pager.bin")),
		root + "LICENSE":           fixture.Read(t, filepath.Join(in.Repo, "LICENSE")),
		root + "THIRD_PARTY.md":    fixture.Read(t, filepath.Join(in.Repo, "THIRD_PARTY.md")),
		root + "licenses/Go.txt":   []byte(fixture.Licences["Go.txt"]),
		root + "licenses/LVGL.txt": []byte(fixture.Licences["LVGL.txt"]),
		fw + "manifest.json":       nil, // checked below
	}
	dirs := []string{root, root + "bin/", root + "licenses/", root + "share/", root + "share/herdr-pager/", fw}

	var want []string
	for name := range files {
		want = append(want, name)
	}
	want = append(want, dirs...)
	var names []string
	for name := range got {
		names = append(names, name)
	}
	slices.Sort(want)
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Errorf("the archive holds\n  %s\nwant\n  %s", strings.Join(names, "\n  "), strings.Join(want, "\n  "))
	}

	for _, d := range dirs {
		if e, ok := got[d]; ok && (e.typ != tar.TypeDir || e.mode != 0o755) {
			t.Errorf("%s: type %q, mode %o; want a directory, 0755", d, e.typ, e.mode)
		}
	}
	for name, data := range files {
		e, ok := got[name]
		if !ok {
			continue
		}
		wantMode := int64(0o644)
		if name == root+"bin/pager-bridge" {
			wantMode = 0o755
		}
		if e.typ != tar.TypeReg || e.mode != wantMode {
			t.Errorf("%s: type %q, mode %o; want a regular file, %o", name, e.typ, e.mode, wantMode)
		}
		if data != nil && !bytes.Equal(e.data, data) {
			t.Errorf("%s isn't its input's copy (%d bytes; want %d)", name, len(e.data), len(data))
		}
	}

	// The ESP Web Tools manifest: the three images at their offsets, paths
	// relative to the manifest, and the version the pager's own fw says.
	var m struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Builds  []struct {
			ChipFamily string `json:"chipFamily"`
			Parts      []struct {
				Path   string `json:"path"`
				Offset int64  `json:"offset"`
			} `json:"parts"`
		} `json:"builds"`
	}
	if err := json.Unmarshal(got[fw+"manifest.json"].data, &m); err != nil {
		t.Fatalf("manifest.json: %v\n%s", err, got[fw+"manifest.json"].data)
	}
	if m.Name != "herdr-pager" || m.Version != "9.8.7" {
		t.Errorf("manifest: name %q, version %q; want herdr-pager, 9.8.7", m.Name, m.Version)
	}
	if len(m.Builds) != 1 || m.Builds[0].ChipFamily != "ESP32-S3" {
		t.Fatalf("manifest's builds: %+v; want one, for ESP32-S3", m.Builds)
	}
	type part struct {
		path   string
		offset int64
	}
	var parts []part
	for _, p := range m.Builds[0].Parts {
		parts = append(parts, part{p.Path, p.Offset})
	}
	wantParts := []part{{"bootloader.bin", 0x0}, {"partition-table.bin", 0x8000}, {"herdr-pager.bin", 0x10000}}
	if !slices.Equal(parts, wantParts) {
		t.Errorf("manifest's parts: %+v; want %+v", parts, wantParts)
	}
}

// The packer writes the release's whole set of files (requirement 3.1): the
// archive, libssh's source as the component manager fetched it (LGPL-2.1,
// ADR 0004), THIRD_PARTY.md, and SHA256SUMS over the other three.
func TestPackWritesTheReleaseSet(t *testing.T) {
	in := fixture.New(t, "9.8.7")
	out := filepath.Join(t.TempDir(), "dist")
	code, stdout, stderr := runPack(t, packArgs(in, "9.8.7", out)...)
	if code != 0 {
		t.Fatalf("pack: exit %d, stderr %q", code, stderr)
	}
	set := []string{"herdr-pager_9.8.7_darwin_arm64.tar.gz", "libssh-0.12.2-source.tar.gz", "THIRD_PARTY.md", "SHA256SUMS"}
	var printed []string
	for _, name := range set {
		printed = append(printed, filepath.Join(out, name))
	}
	if want := strings.Join(printed, "\n") + "\n"; stdout != want {
		t.Errorf("pack printed\n%s\nwant\n%s", stdout, want)
	}
	var found []string
	ents, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		found = append(found, e.Name())
		if info, err := e.Info(); err != nil || info.Mode() != 0o644 {
			t.Errorf("%s: mode %v; want 0644", e.Name(), info.Mode())
		}
	}
	if !slices.Equal(found, slices.Sorted(slices.Values(set))) {
		t.Errorf("--out holds %v; want %v", found, set)
	}

	if got, want := fixture.Read(t, filepath.Join(out, "THIRD_PARTY.md")), fixture.Read(t, filepath.Join(in.Repo, "THIRD_PARTY.md")); !bytes.Equal(got, want) {
		t.Error("THIRD_PARTY.md isn't the repository's")
	}
	sums := fixture.Read(t, filepath.Join(out, "SHA256SUMS"))
	if want := fixture.SHA256SUMS(t, out, "THIRD_PARTY.md", "herdr-pager_9.8.7_darwin_arm64.tar.gz", "libssh-0.12.2-source.tar.gz"); !bytes.Equal(sums, want) {
		t.Errorf("SHA256SUMS:\n%s\nwant (sha256sum's form, sorted by name):\n%s", sums, want)
	}

	// libssh's source: every file of the component as fetched, under one
	// folder, executables still executable.
	src := readArchive(t, filepath.Join(out, "libssh-0.12.2-source.tar.gz"))
	const root = "libssh-0.12.2/"
	want := []string{root}
	for name := range fixture.LibsshFiles {
		want = append(want, root+name)
		for d := path.Dir(name); d != "."; d = path.Dir(d) {
			want = append(want, root+d+"/")
		}
	}
	slices.Sort(want)
	want = slices.Compact(want)
	names := slices.Sorted(maps.Keys(src))
	if !slices.Equal(names, want) {
		t.Errorf("libssh's source holds\n  %s\nwant\n  %s", strings.Join(names, "\n  "), strings.Join(want, "\n  "))
	}
	for name, text := range fixture.LibsshFiles {
		e := src[root+name]
		wantMode := int64(0o644)
		if name == "install.sh" {
			wantMode = 0o755
		}
		if e.typ != tar.TypeReg || e.mode != wantMode || string(e.data) != text {
			t.Errorf("libssh's source: %s: type %q, mode %o, %d bytes; want its file, mode %o", name, e.typ, e.mode, len(e.data), wantMode)
		}
	}
}

// The packer writes only into a new or empty folder, so --out holds the
// release's files and nothing else.
func TestPackWantsAnEmptyOut(t *testing.T) {
	in := fixture.New(t, "9.8.7")
	out := t.TempDir()
	fixture.Write(t, filepath.Join(out, "old.tar.gz"), []byte("an earlier pack"))
	code, stdout, stderr := runPack(t, packArgs(in, "9.8.7", out)...)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "isn't empty") {
		t.Errorf("pack into a folder with a file: exit %d, stdout %q, stderr %q; want 1, saying it isn't empty", code, stdout, stderr)
	}
	if left, _ := filepath.Glob(filepath.Join(out, "*")); len(left) != 1 {
		t.Errorf("--out holds %v; want only the earlier file", left)
	}
}

// Two packs of the same inputs are the same bytes, whenever and wherever they
// run: no time, owner or path of the machine that packed them is in the
// archive.
func TestPackIsReproducible(t *testing.T) {
	in := fixture.New(t, "9.8.7")
	var archives [][]byte
	var sources [][]byte
	for i := range 2 {
		if i == 1 {
			// The second pack's inputs were written at another time.
			then := time.Now().Add(-90 * time.Minute)
			err := filepath.WalkDir(filepath.Dir(in.Bridge), func(p string, _ fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				return os.Chtimes(p, then, then)
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		out := t.TempDir()
		if code, _, stderr := runPack(t, packArgs(in, "9.8.7", out)...); code != 0 {
			t.Fatalf("pack: exit %d, stderr %q", code, stderr)
		}
		archives = append(archives, fixture.Read(t, filepath.Join(out, "herdr-pager_9.8.7_darwin_arm64.tar.gz")))
		sources = append(sources, fixture.Read(t, filepath.Join(out, "libssh-0.12.2-source.tar.gz")))
	}
	if !bytes.Equal(archives[0], archives[1]) {
		t.Error("two packs of the same inputs differ")
	}
	if !bytes.Equal(sources[0], sources[1]) {
		t.Error("two packs of the same libssh source differ")
	}
	for _, b := range [][]byte{archives[0], sources[0]} {
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if zr.Name != "" || !zr.ModTime.IsZero() {
			t.Errorf("the gzip header names %q, time %v; want neither", zr.Name, zr.ModTime)
		}
		tr := tar.NewReader(zr)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" {
				t.Errorf("%s: owner %d:%d (%q:%q); want 0:0, unnamed", h.Name, h.Uid, h.Gid, h.Uname, h.Gname)
			}
		}
	}
}

// The packer refuses inputs that would make a wrong archive, and leaves no
// archive behind.
func TestPackRefuses(t *testing.T) {
	cases := []struct {
		name    string
		version string // --version; the app says 9.8.7
		change  func(t *testing.T, in fixture.Inputs)
		say     string // in stderr
	}{
		{
			name:    "the app says another version",
			version: "9.8.8",
			say:     `the app says version "9.8.7"`,
		},
		{
			name:    "a dev build packed as a published version",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, filepath.Join(in.Firmware, "herdr-pager.bin"), fixture.App(fixture.ChipESP32S3, "9.8.7-dev", "herdr-pager"))
			},
			say: `the app says version "9.8.7-dev"`,
		},
		{
			name:    "another project's app",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, filepath.Join(in.Firmware, "herdr-pager.bin"), fixture.App(fixture.ChipESP32S3, "9.8.7", "hello_world"))
			},
			say: `project "hello_world"`,
		},
		{
			name:    "an app for another chip",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, filepath.Join(in.Firmware, "herdr-pager.bin"), fixture.App(fixture.ChipESP32, "9.8.7", "herdr-pager"))
			},
			say: "isn't for the ESP32-S3",
		},
		{
			name:    "a file that isn't a bootloader image",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, filepath.Join(in.Firmware, "bootloader", "bootloader.bin"), []byte("not an image"))
			},
			say: "bootloader.bin isn't an ESP-IDF image",
		},
		{
			name:    "a file that isn't a partition table",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, filepath.Join(in.Firmware, "partition_table", "partition-table.bin"), bytes.Repeat([]byte{0xFF}, 0xC00))
			},
			say: "partition-table.bin isn't a partition table",
		},
		{
			name:    "a flash layout with another image",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, filepath.Join(in.Firmware, "ota_data_initial.bin"), bytes.Repeat([]byte{0xFF}, 0x2000))
				fixture.Write(t, filepath.Join(in.Firmware, "flasher_args.json"), []byte(strings.Replace(fixture.FlasherArgs,
					`"0x0" : "bootloader/bootloader.bin",`, `"0x0" : "bootloader/bootloader.bin", "0xd000" : "ota_data_initial.bin",`, 1)))
			},
			say: "flash layout",
		},
		{
			name:    "the app at another offset",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, filepath.Join(in.Firmware, "flasher_args.json"), []byte(strings.NewReplacer(
					`"0x10000" : "herdr-pager.bin"`, `"0x20000" : "herdr-pager.bin"`,
					`"offset" : "0x10000"`, `"offset" : "0x20000"`).Replace(fixture.FlasherArgs)))
			},
			say: "flash layout",
		},
		{
			name:    "a build for another chip",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, filepath.Join(in.Firmware, "flasher_args.json"), []byte(strings.Replace(fixture.FlasherArgs,
					`"chip"   : "esp32s3"`, `"chip"   : "esp32"`, 1)))
			},
			say: `chip "esp32"`,
		},
		{
			name:    "an encrypted app",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, filepath.Join(in.Firmware, "flasher_args.json"), []byte(strings.Replace(fixture.FlasherArgs,
					`"file" : "herdr-pager.bin", "encrypted" : "false"`, `"file" : "herdr-pager.bin", "encrypted" : "true"`, 1)))
			},
			say: "encrypted",
		},
		{
			name:    "no ESP-IDF build",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				if err := os.Remove(filepath.Join(in.Firmware, "flasher_args.json")); err != nil {
					t.Fatal(err)
				}
			},
			say: "flasher_args.json",
		},
		{
			name:    "a bridge for Intel Macs",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, in.Bridge, fixture.MachO(fixture.CPUAmd64))
			},
			say: "isn't a macOS arm64 executable",
		},
		{
			name:    "a bridge for Linux",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, in.Bridge, append([]byte("\x7fELF\x02\x01\x01"), make([]byte, 64)...))
			},
			say: "isn't a macOS arm64 executable",
		},
		{
			name:    "no third-party licences",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				if err := os.RemoveAll(filepath.Join(in.Repo, "tools", "release", "licenses")); err != nil {
					t.Fatal(err)
				}
			},
			say: "licenses",
		},
		{
			name:    "an empty licence",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, filepath.Join(in.Repo, "tools", "release", "licenses", "Go.txt"), nil)
			},
			say: "Go.txt is empty",
		},
		{
			name:    "a folder that isn't a component",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				if err := os.Remove(filepath.Join(in.Libssh, "idf_component.yml")); err != nil {
					t.Fatal(err)
				}
			},
			say: "idf_component.yml",
		},
		{
			name:    "libssh's source without its licence",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				if err := os.Remove(filepath.Join(in.Libssh, "libssh-mirror", "COPYING")); err != nil {
					t.Fatal(err)
				}
			},
			say: "libssh-mirror/COPYING",
		},
		{
			name:    "a component whose libssh is another version",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, filepath.Join(in.Libssh, "libssh-mirror", "CMakeLists.txt"), []byte("project(libssh VERSION 0.11.1 LANGUAGES C)\n"))
			},
			say: "0.12.2",
		},
		{
			name:    "a component that isn't LGPL-2.1",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				fixture.Write(t, filepath.Join(in.Libssh, "idf_component.yml"), []byte(strings.Replace(fixture.LibsshFiles["idf_component.yml"], "LGPL-2.1", "MIT", 1)))
			},
			say: "LGPL-2.1",
		},
		{
			name:    "no THIRD_PARTY.md",
			version: "9.8.7",
			change: func(t *testing.T, in fixture.Inputs) {
				if err := os.Remove(filepath.Join(in.Repo, "THIRD_PARTY.md")); err != nil {
					t.Fatal(err)
				}
			},
			say: "THIRD_PARTY.md",
		},
		{
			name:    "a version hello's fw can't carry",
			version: "9.8/7",
			say:     `version "9.8/7"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixture.New(t, "9.8.7")
			if c.change != nil {
				c.change(t, in)
			}
			out := filepath.Join(t.TempDir(), "dist")
			code, stdout, stderr := runPack(t, packArgs(in, c.version, out)...)
			if code != 1 || !strings.Contains(stderr, c.say) {
				t.Errorf("pack: exit %d, stderr %q; want 1, saying %q", code, stderr, c.say)
			}
			if stdout != "" {
				t.Errorf("pack printed %q on a refusal", stdout)
			}
			if left, _ := filepath.Glob(filepath.Join(out, "*")); len(left) != 0 {
				t.Errorf("pack left %v", left)
			}
		})
	}
}

func TestPackUsage(t *testing.T) {
	in := fixture.New(t, "9.8.7")
	full := packArgs(in, "9.8.7", t.TempDir())
	for i := 0; i < len(full); i += 2 {
		args := slices.Delete(slices.Clone(full), i, i+2)
		code, _, stderr := runPack(t, args...)
		if code != 2 || !strings.Contains(stderr, "usage: pack") {
			t.Errorf("without %s: exit %d, stderr %q; want 2 and the usage", full[i], code, stderr)
		}
	}
}
