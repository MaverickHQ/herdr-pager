package setup

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The firmware the page flashes (requirement 2.4, design D5P-5): the images
// shipped with the bridge, so the page always flashes the firmware that
// matches it. It's read from its folder at each request, so a rebuild while
// the page runs is what the page flashes next.
//
// The folder is either:
//   - a release's share/herdr-pager/firmware (tools/release): ESP Web Tools'
//     manifest.json and the images it names, served as they are; or
//   - a source build's ESP-IDF build directory (firmware/build): no
//     manifest, but flasher_args.json, whose flash_files are the images and
//     their offsets, and project_description.json, whose project_version is
//     the version. The page makes the manifest from them.
//
// Either way it serves only the images the manifest names, and only a
// herdr-pager build for the ESP32-S3 with a version the pager's fw could
// say.
const manifestFile = "manifest.json"

// updateFile is the manifest for Manage's Update (requirement 2.4: an update
// keeps the pairing): manifest.json with new_install_prompt_erase, so ESP
// Web Tools asks whether to erase. The pager doesn't speak Improv, so to ESP
// Web Tools every install is a new one, and without that key a new install
// always erases: right for a first install, which uses manifest.json.
const updateFile = "update.json"

// The product's name, the firmware project's (firmware/CMakeLists.txt).
const project = "herdr-pager"

// The characters hello's fw may hold, and at most 31 (firmware/CMakeLists.txt,
// tools/release): the version the page compares with the pager's.
var versionRE = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,31}$`)

// espManifest is ESP Web Tools' manifest, as tools/release writes it.
type espManifest struct {
	Name    string     `json:"name"`
	Version string     `json:"version"`
	Builds  []espBuild `json:"builds"`
}

type espBuild struct {
	ChipFamily string    `json:"chipFamily"`
	Parts      []espPart `json:"parts"`
}

type espPart struct {
	Path   string `json:"path"` // relative to the manifest
	Offset int64  `json:"offset"`
}

// A firmware is what the folder holds that the page can flash.
type firmware struct {
	version  string   // the manifest's: what the pager's fw is compared with
	manifest []byte   // what GET /firmware/manifest.json serves
	parts    []string // the images it names, slash-separated, relative to the folder
}

// loadFirmware reads the firmware in dir, or says why the page can't flash
// it.
func loadFirmware(dir string) (firmware, error) {
	if dir == "" {
		return firmware{}, errors.New("the bridge wasn't given a firmware folder")
	}
	man := filepath.Join(dir, manifestFile)
	raw, err := os.ReadFile(man)
	switch {
	case err == nil:
		var m espManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return firmware{}, fmt.Errorf("%s won't load: %v", man, err)
		}
		fw, err := checkManifest(dir, m)
		if err != nil {
			return firmware{}, fmt.Errorf("%s: %w", man, err)
		}
		fw.manifest = raw
		return fw, nil
	case !errors.Is(err, fs.ErrNotExist):
		return firmware{}, err
	}

	args := filepath.Join(dir, "flasher_args.json")
	raw, err = os.ReadFile(args)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return firmware{}, fmt.Errorf("no firmware in %s: neither a release's %s nor an ESP-IDF build's flasher_args.json", dir, manifestFile)
	case err != nil:
		return firmware{}, err
	}
	m, err := buildManifest(dir, raw)
	if err != nil {
		return firmware{}, fmt.Errorf("%s: %w", args, err)
	}
	fw, err := checkManifest(dir, m)
	if err != nil {
		return firmware{}, fmt.Errorf("%s: %w", args, err)
	}
	if fw.manifest, err = json.MarshalIndent(m, "", "  "); err != nil {
		return firmware{}, err
	}
	return fw, nil
}

// buildManifest is the manifest for the ESP-IDF build in dir, whose
// flasher_args.json is raw.
func buildManifest(dir string, raw []byte) (espManifest, error) {
	var args struct {
		FlashFiles map[string]string `json:"flash_files"`
		Extra      struct {
			Chip string `json:"chip"`
		} `json:"extra_esptool_args"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return espManifest{}, fmt.Errorf("it won't load: %v", err)
	}
	if args.Extra.Chip != "esp32s3" {
		return espManifest{}, fmt.Errorf("it's a build for %q, not the pager's esp32s3 (idf.py -C firmware set-target esp32s3)", args.Extra.Chip)
	}
	descPath := filepath.Join(dir, "project_description.json")
	descRaw, err := os.ReadFile(descPath)
	if err != nil {
		return espManifest{}, fmt.Errorf("the build has no version: %v", err)
	}
	var desc struct {
		Name    string `json:"project_name"`
		Version string `json:"project_version"`
	}
	if err := json.Unmarshal(descRaw, &desc); err != nil {
		return espManifest{}, fmt.Errorf("%s won't load: %v", descPath, err)
	}
	b := espBuild{ChipFamily: "ESP32-S3"}
	for off, file := range args.FlashFiles {
		n, err := strconv.ParseInt(off, 0, 64)
		if err != nil {
			return espManifest{}, fmt.Errorf("its offset %q for %s isn't a number", off, file)
		}
		b.Parts = append(b.Parts, espPart{Path: file, Offset: n})
	}
	slices.SortFunc(b.Parts, func(a, b espPart) int { return cmp.Compare(a.Offset, b.Offset) })
	return espManifest{Name: desc.Name, Version: desc.Version, Builds: []espBuild{b}}, nil
}

// checkManifest checks that m is herdr-pager's, for the ESP32-S3, with a
// version, and that each image it names is a file in dir.
func checkManifest(dir string, m espManifest) (firmware, error) {
	switch {
	case m.Name != project:
		return firmware{}, fmt.Errorf("it's %q's firmware, not %s's", m.Name, project)
	case m.Version == "":
		return firmware{}, errors.New("it gives no version")
	case !versionRE.MatchString(m.Version):
		return firmware{}, fmt.Errorf("its version %q isn't one the pager could report", m.Version)
	case len(m.Builds) != 1 || m.Builds[0].ChipFamily != "ESP32-S3":
		return firmware{}, errors.New("it isn't one build, for the pager's ESP32-S3")
	case len(m.Builds[0].Parts) == 0:
		return firmware{}, errors.New("it names no images")
	}
	fw := firmware{version: m.Version}
	for _, p := range m.Builds[0].Parts {
		if p.Path == "" || p.Path != path.Clean(p.Path) || !filepath.IsLocal(filepath.FromSlash(p.Path)) || strings.Contains(p.Path, `\`) || p.Path == manifestFile || p.Path == updateFile {
			return firmware{}, fmt.Errorf("its image %q isn't a file in its folder", p.Path)
		}
		if p.Offset < 0 {
			return firmware{}, fmt.Errorf("its image %s has the offset %d", p.Path, p.Offset)
		}
		if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p.Path))); err != nil || !fi.Mode().IsRegular() {
			return firmware{}, fmt.Errorf("its image %s isn't there", p.Path)
		}
		fw.parts = append(fw.parts, p.Path)
	}
	return fw, nil
}

// firmwareFix is how to get the firmware, for the page to show.
const firmwareFix = "Installed with Homebrew: `brew reinstall herdr-pager` puts the firmware back. " +
	"From a release's archive: run the pager-bridge in its bin folder, beside its share folder. " +
	"From source: build the firmware (`idf.py -C firmware set-target esp32s3 build`), then run `pager-bridge setup --firmware firmware/build`."

// firmwareInfo is GET /api/firmware: whether the page can flash, and the
// version it would, which the page compares with the pager's fw.
func (h *Handler) firmwareInfo(w http.ResponseWriter, r *http.Request) {
	info := struct {
		Available bool   `json:"available"`
		Version   string `json:"version,omitempty"`
		Dir       string `json:"dir"`
		Problem   string `json:"problem,omitempty"`
		Fix       string `json:"fix,omitempty"`
	}{Dir: h.firmware}
	fw, err := loadFirmware(h.firmware)
	if err != nil {
		info.Problem, info.Fix = err.Error(), firmwareFix
	} else {
		info.Available, info.Version = true, fw.version
	}
	writeJSON(w, info)
}

// firmwareFile is GET /firmware/{path...}: the manifest, for ESP Web Tools,
// and the images it names, relative to it. Nothing else in the folder.
func (h *Handler) firmwareFile(w http.ResponseWriter, r *http.Request) {
	fw, err := loadFirmware(h.firmware)
	if err != nil {
		http.Error(w, "No firmware to flash: "+err.Error(), http.StatusNotFound)
		return
	}
	name := r.PathValue("path")
	switch name {
	case manifestFile:
		w.Header().Set("Content-Type", "application/json")
		w.Write(fw.manifest)
		return
	case updateFile:
		var m map[string]any
		if err := json.Unmarshal(fw.manifest, &m); err != nil {
			http.Error(w, "The manifest won't load: "+err.Error(), http.StatusNotFound)
			return
		}
		m["new_install_prompt_erase"] = true
		writeJSON(w, m)
		return
	}
	if !slices.Contains(fw.parts, name) {
		http.NotFound(w, r)
		return
	}
	f, err := os.OpenInRoot(h.firmware, filepath.FromSlash(name))
	if err != nil {
		http.Error(w, "The image won't open: "+err.Error(), http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", time.Time{}, f)
}

// DefaultFirmware is the firmware's folder for the bridge at exe, as a
// release lays it out: ../share/herdr-pager/firmware from its bin folder.
// It follows exe's links first, so Homebrew's bin/pager-bridge finds its
// own keg's firmware.
func DefaultFirmware(exe string) string {
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Join(filepath.Dir(filepath.Dir(exe)), "share", "herdr-pager", "firmware")
}

// writeJSON writes v as the response, JSON.
func writeJSON(w http.ResponseWriter, v any) { writeJSONStatus(w, http.StatusOK, v) }

// writeJSONStatus writes v as the response, JSON, with status.
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(append(data, '\n'))
}
