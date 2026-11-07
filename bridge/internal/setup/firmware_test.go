package setup

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The images in these tests: made-up bytes, distinct for each.
var (
	bootloader = []byte("bootloader image\x00\x01")
	partitions = []byte("\xaa\x50partition table")
	app        = []byte("the app image\x02\x03")
)

func writeFiles(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// shippedManifest is the manifest the release's archive holds in
// share/herdr-pager/firmware/ (tools/release, task 4.1), at version.
func shippedManifest(version string) []byte {
	return []byte(`{
  "name": "herdr-pager",
  "version": "` + version + `",
  "builds": [
    {
      "chipFamily": "ESP32-S3",
      "parts": [
        {"path": "bootloader.bin", "offset": 0},
        {"path": "partition-table.bin", "offset": 32768},
        {"path": "herdr-pager.bin", "offset": 65536}
      ]
    }
  ]
}
`)
}

// shipped is a release's share/herdr-pager/firmware/, at version.
func shipped(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, map[string][]byte{
		"manifest.json":       shippedManifest(version),
		"bootloader.bin":      bootloader,
		"partition-table.bin": partitions,
		"herdr-pager.bin":     app,
		"notes.txt":           []byte("not an image the manifest names"),
	})
	return dir
}

// buildDir is a source build's ESP-IDF build directory, as idf.py leaves it
// (firmware/build): flasher_args.json, project_description.json and the
// images they name, no manifest.
func buildDir(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, map[string][]byte{
		"flasher_args.json": []byte(`{
    "write_flash_args" : [ "--flash_mode", "dio", "--flash_size", "16MB", "--flash_freq", "80m" ],
    "flash_files" : {
        "0x0" : "bootloader/bootloader.bin",
        "0x10000" : "herdr-pager.bin",
        "0x8000" : "partition_table/partition-table.bin"
    },
    "app" : { "offset" : "0x10000", "file" : "herdr-pager.bin", "encrypted" : "false" },
    "extra_esptool_args" : { "after" : "hard_reset", "before" : "default_reset", "stub" : true, "chip" : "esp32s3" }
}
`),
		"project_description.json": []byte(`{
    "version":            "1.3",
    "project_name":       "herdr-pager",
    "project_version":    "` + version + `",
    "target":             "esp32s3"
}
`),
		"bootloader/bootloader.bin":           bootloader,
		"partition_table/partition-table.bin": partitions,
		"herdr-pager.bin":                     app,
		"herdr-pager.elf":                     []byte("an ELF, not an image to flash"),
	})
	return dir
}

// firmwareInfo is GET /api/firmware's answer.
type firmwareInfo struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
	Dir       string `json:"dir"`
	Problem   string `json:"problem"`
	Fix       string `json:"fix"`
}

// page is a guarded page with opts, and its tab's GETs.
func page(t *testing.T, opts Options) *guarded {
	t.Helper()
	h, err := NewHandler(opts)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	_, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	return &guarded{t: t, h: h, srv: srv, port: port, token: h.Token()}
}

func (g *guarded) get(path string) (int, http.Header, []byte) {
	g.t.Helper()
	resp := g.do(g.request(http.MethodGet, path, nil))
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

func (g *guarded) firmware() firmwareInfo {
	g.t.Helper()
	code, _, body := g.get("/api/firmware")
	if code != http.StatusOK {
		g.t.Fatalf("GET /api/firmware: %d %s", code, body)
	}
	var info firmwareInfo
	if err := json.Unmarshal(body, &info); err != nil {
		g.t.Fatalf("GET /api/firmware: %v: %s", err, body)
	}
	return info
}

// manifest is what GET /firmware/manifest.json serves, decoded.
type manifest struct {
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

// flashes checks that the page serves a manifest for ESP Web Tools at
// version, whose parts, fetched relative to it as ESP Web Tools does, are the
// three images at 0x0, 0x8000 and 0x10000.
func (g *guarded) flashes(version string) manifest {
	g.t.Helper()
	code, hdr, body := g.get("/firmware/manifest.json")
	if code != http.StatusOK || !strings.HasPrefix(hdr.Get("Content-Type"), "application/json") {
		g.t.Fatalf("GET /firmware/manifest.json: %d, %q: %s", code, hdr.Get("Content-Type"), body)
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		g.t.Fatalf("the manifest: %v: %s", err, body)
	}
	if m.Name != "herdr-pager" || m.Version != version || len(m.Builds) != 1 || m.Builds[0].ChipFamily != "ESP32-S3" {
		g.t.Fatalf("the manifest: %+v; want herdr-pager %s, one ESP32-S3 build", m, version)
	}
	want := map[int64][]byte{0x0: bootloader, 0x8000: partitions, 0x10000: app}
	if len(m.Builds[0].Parts) != len(want) {
		g.t.Errorf("the manifest has %d parts; want %d", len(m.Builds[0].Parts), len(want))
	}
	for _, p := range m.Builds[0].Parts {
		code, hdr, body := g.get("/firmware/" + p.Path)
		if code != http.StatusOK || !bytes.Equal(body, want[p.Offset]) {
			g.t.Errorf("part %s at %#x: %d, %q; want its image", p.Path, p.Offset, code, body)
		}
		if ct := hdr.Get("Content-Type"); ct != "application/octet-stream" {
			g.t.Errorf("part %s: Content-Type %q; want application/octet-stream", p.Path, ct)
		}
	}
	return m
}

// TestSetupServesTheShippedFirmware is requirement 2.4's firmware (design
// D5P-5): the page flashes the images shipped with the bridge, from
// --firmware <dir> or, by default, ../share/herdr-pager/firmware from its
// executable; says how to get them when they aren't there; and gives the
// manifest's version, which it compares with the pager's.
func TestSetupServesTheShippedFirmware(t *testing.T) {
	t.Run("a release's firmware", func(t *testing.T) {
		dir := shipped(t, "1.2.3")
		g := page(t, Options{Firmware: dir})
		info := g.firmware()
		if !info.Available || info.Version != "1.2.3" || info.Dir != dir || info.Problem != "" || info.Fix != "" {
			t.Errorf("/api/firmware %+v; want available, version 1.2.3, dir %s", info, dir)
		}
		g.flashes("1.2.3")
		// The manifest as shipped, byte for byte.
		if _, _, body := g.get("/firmware/manifest.json"); !bytes.Equal(body, shippedManifest("1.2.3")) {
			t.Errorf("served the manifest as %s; want the file", body)
		}
		// Nothing the manifest doesn't name.
		for _, path := range []string{"/firmware/notes.txt", "/firmware/", "/firmware/nowhere.bin", "/firmware/../manifest.json",
			"/firmware/%2e%2e/notes.txt", "/firmware/./herdr-pager.bin", "/firmware//herdr-pager.bin"} {
			if code, _, body := g.get(path); code == http.StatusOK {
				t.Errorf("GET %s: served %q; want only the manifest's images", path, body)
			}
		}
		// A rebuild while the page runs: the page serves what's there now.
		writeFiles(t, dir, map[string][]byte{"manifest.json": shippedManifest("1.2.4")})
		if v := g.firmware().Version; v != "1.2.4" {
			t.Errorf("after the manifest changed, version %q; want 1.2.4", v)
		}
	})

	t.Run("a source build's own build directory", func(t *testing.T) {
		dir := buildDir(t, "1.0.0-dev")
		g := page(t, Options{Firmware: dir})
		if info := g.firmware(); !info.Available || info.Version != "1.0.0-dev" {
			t.Errorf("/api/firmware %+v; want available, version 1.0.0-dev", info)
		}
		m := g.flashes("1.0.0-dev")
		var paths []string
		for _, p := range m.Builds[0].Parts {
			paths = append(paths, p.Path)
		}
		if want := []string{"bootloader/bootloader.bin", "partition_table/partition-table.bin", "herdr-pager.bin"}; !reflect.DeepEqual(paths, want) {
			t.Errorf("parts %q; want %q, by offset", paths, want)
		}
		if code, _, _ := g.get("/firmware/herdr-pager.elf"); code == http.StatusOK {
			t.Error("served herdr-pager.elf, which flasher_args.json doesn't name")
		}
	})

	t.Run("no firmware: the page says how to get it", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "share", "herdr-pager", "firmware")
		for _, dir := range []string{missing, t.TempDir()} {
			g := page(t, Options{Firmware: dir})
			info := g.firmware()
			if info.Available || info.Version != "" || info.Dir != dir {
				t.Errorf("/api/firmware %+v; want unavailable, in %s", info, dir)
			}
			if !strings.Contains(info.Problem, dir) {
				t.Errorf("problem %q doesn't name %s", info.Problem, dir)
			}
			for _, s := range []string{"brew reinstall herdr-pager", "--firmware", "idf.py"} {
				if !strings.Contains(info.Fix, s) {
					t.Errorf("fix %q doesn't say %q", info.Fix, s)
				}
			}
			if code, _, _ := g.get("/firmware/manifest.json"); code != http.StatusNotFound {
				t.Errorf("GET /firmware/manifest.json with no firmware: %d; want 404", code)
			}
		}
		// No --firmware at all.
		if info := page(t, Options{}).firmware(); info.Available || info.Fix == "" {
			t.Errorf("/api/firmware with no directory %+v; want unavailable, with a fix", info)
		}
	})

	t.Run("firmware the page can't flash", func(t *testing.T) {
		// cannot checks a release's folder with files changed (nil removes
		// one) is refused, the problem naming problem.
		cannot := func(name string, files map[string][]byte, problem string) {
			t.Run(name, func(t *testing.T) {
				dir := shipped(t, "1.2.3")
				for f, data := range files {
					if data == nil {
						os.Remove(filepath.Join(dir, f))
						delete(files, f)
					}
				}
				writeFiles(t, dir, files)
				g := page(t, Options{Firmware: dir})
				info := g.firmware()
				if info.Available || !strings.Contains(info.Problem, problem) || info.Fix == "" {
					t.Errorf("/api/firmware %+v; want unavailable, the problem naming %q, and a fix", info, problem)
				}
				if code, _, _ := g.get("/firmware/manifest.json"); code != http.StatusNotFound {
					t.Errorf("GET /firmware/manifest.json: %d; want 404", code)
				}
			})
		}
		// manifest is the shipped manifest with old replaced by new.
		manifest := func(old, new string) map[string][]byte {
			return map[string][]byte{"manifest.json": bytes.Replace(shippedManifest("1.2.3"), []byte(old), []byte(new), 1)}
		}
		cannot("an image missing", map[string][]byte{"herdr-pager.bin": nil}, "herdr-pager.bin")
		cannot("a manifest that isn't JSON", map[string][]byte{"manifest.json": []byte("{")}, "manifest.json")
		cannot("a part outside the folder", manifest(`"herdr-pager.bin"`, `"../herdr-pager.bin"`), "../herdr-pager.bin")
		cannot("an absolute part", manifest(`"herdr-pager.bin"`, `"/etc/hosts"`), "/etc/hosts")
		cannot("a part that's the manifest", manifest(`"herdr-pager.bin"`, `"manifest.json"`), "manifest.json")
		cannot("another chip", manifest(`"ESP32-S3"`, `"ESP32"`), "ESP32-S3")
		cannot("another project", manifest(`"name": "herdr-pager"`, `"name": "other"`), "other")
		cannot("no version", manifest(`"version": "1.2.3"`, `"version": ""`), "version")
		cannot("a version the pager can't say", manifest(`"version": "1.2.3"`, `"version": "1.2 beta"`), "1.2 beta")
		cannot("a build for another chip", map[string][]byte{
			"manifest.json":     nil,
			"flasher_args.json": []byte(`{"flash_files": {"0x0": "bootloader.bin"}, "extra_esptool_args": {"chip": "esp32"}}`),
		}, "esp32")
	})

	t.Run("the default: ../share/herdr-pager/firmware from the executable", func(t *testing.T) {
		// Homebrew links bin/pager-bridge to its keg; the firmware is the keg's.
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		keg := filepath.Join(root, "Cellar", "herdr-pager", "1.0.0")
		writeFiles(t, keg, map[string][]byte{"bin/pager-bridge": []byte("#!")})
		if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "bin", "pager-bridge")
		if err := os.Symlink(filepath.Join(keg, "bin", "pager-bridge"), link); err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(keg, "share", "herdr-pager", "firmware")
		for _, exe := range []string{filepath.Join(keg, "bin", "pager-bridge"), link} {
			if got := DefaultFirmware(exe); got != want {
				t.Errorf("DefaultFirmware(%s) = %s; want %s", exe, got, want)
			}
		}
		// An unpacked archive: herdr-pager-<version>/bin/pager-bridge.
		if got, want := DefaultFirmware("/tmp/herdr-pager-1.0.0/bin/pager-bridge"), "/tmp/herdr-pager-1.0.0/share/herdr-pager/firmware"; got != want {
			t.Errorf("DefaultFirmware of an unpacked archive = %s; want %s", got, want)
		}
	})
}
