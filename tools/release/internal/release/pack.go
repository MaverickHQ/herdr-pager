package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/macho"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// PackOptions are the packer's inputs.
type PackOptions struct {
	Version  string // the version the archive is: the app's own, and the manifest's
	Bridge   string // the bridge's binary, macOS arm64
	Firmware string // the ESP-IDF build directory: flasher_args.json and the images it names
	Libssh   string // the david-cermak/libssh component, as the component manager fetched it
	Repo     string // the repository: LICENSE, THIRD_PARTY.md and tools/release/licenses/
	Out      string // the folder the release's files are written to: new, or empty
}

// member is one entry of a gzipped tar, under its folder.
type member struct {
	name string // slash-separated, relative to the folder; a directory's ends in /
	mode int64
	data []byte
}

// Every entry's time: fixed, so two packs of the same inputs are the same
// bytes (1980-01-01, the floor of the ZIP format many reproducible builds use).
var entryTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// Pack checks the inputs and writes the release's files, as the package
// comment lists them, to o.Out. It returns their paths: the archive,
// libssh's source, THIRD_PARTY.md and SHA256SUMS, in that order. It writes
// nothing if any input is wrong.
func Pack(o PackOptions) ([]string, error) {
	if err := CheckVersion(o.Version); err != nil {
		return nil, err
	}
	members, thirdParty, err := gather(o)
	if err != nil {
		return nil, err
	}
	src, err := readComponent(o.Libssh)
	if err != nil {
		return nil, err
	}
	srcFiles := map[string][]byte{}
	for name, m := range src {
		if !strings.HasSuffix(name, "/") {
			srcFiles[name] = m.data
		}
	}
	libsshVersion, err := CheckLibsshSource(srcFiles)
	if err != nil {
		return nil, fmt.Errorf("libssh's source, %s: %w", o.Libssh, err)
	}
	if ents, err := os.ReadDir(o.Out); err == nil && len(ents) > 0 {
		return nil, fmt.Errorf("--out %s isn't empty: the release's files go in a folder of their own", o.Out)
	}

	archive, err := tarGz(Root(o.Version), members)
	if err != nil {
		return nil, err
	}
	var srcMembers []member
	for _, name := range slices.Sorted(maps.Keys(src)) {
		srcMembers = append(srcMembers, src[name])
	}
	source, err := tarGz(LibsshRoot(libsshVersion), srcMembers)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		ArchiveName(o.Version):          archive,
		LibsshSourceName(libsshVersion): source,
		ThirdParty:                      thirdParty,
	}
	var sums bytes.Buffer
	for _, name := range slices.Sorted(maps.Keys(files)) {
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(files[name]), name)
	}
	files[SumsName] = sums.Bytes()

	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return nil, err
	}
	var written []string
	for _, name := range []string{ArchiveName(o.Version), LibsshSourceName(libsshVersion), ThirdParty, SumsName} {
		dst := filepath.Join(o.Out, name)
		if err := writeFile(dst, files[name]); err != nil {
			for _, w := range written {
				os.Remove(w)
			}
			return nil, err
		}
		written = append(written, dst)
	}
	return written, nil
}

// gather reads and checks the archive's inputs, and returns its members,
// sorted by name, and the repository's THIRD_PARTY.md: read once, so the
// archive's copy and the release's are the same bytes.
func gather(o PackOptions) ([]member, []byte, error) {
	bridge, err := os.ReadFile(o.Bridge)
	if err != nil {
		return nil, nil, fmt.Errorf("the bridge: %w", err)
	}
	if err := checkBridge(bridge); err != nil {
		return nil, nil, fmt.Errorf("%s %w", o.Bridge, err)
	}
	images, err := readFirmware(o.Firmware, o.Version)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := json.MarshalIndent(NewManifest(o.Version), "", "  ")
	if err != nil {
		return nil, nil, err
	}
	notices := map[string][]byte{}
	for _, name := range []string{"LICENSE", ThirdParty} {
		if notices[name], err = os.ReadFile(filepath.Join(o.Repo, name)); err != nil {
			return nil, nil, fmt.Errorf("the repository's %s: %w", name, err)
		}
	}
	licences, err := readLicences(filepath.Join(o.Repo, filepath.FromSlash(LicensesSource)))
	if err != nil {
		return nil, nil, err
	}

	ms := []member{
		{name: "LICENSE", mode: 0o644, data: notices["LICENSE"]},
		{name: ThirdParty, mode: 0o644, data: notices[ThirdParty]},
		{name: path.Dir(BridgePath) + "/", mode: 0o755},
		{name: BridgePath, mode: 0o755, data: bridge},
		{name: LicensesDir + "/", mode: 0o755},
	}
	ms = append(ms, licences...)
	for d := path.Dir(FirmwareDir); d != "."; d = path.Dir(d) {
		ms = append(ms, member{name: d + "/", mode: 0o755})
	}
	ms = append(ms,
		member{name: FirmwareDir + "/", mode: 0o755},
		member{name: FirmwareDir + "/" + ManifestName, mode: 0o644, data: append(manifest, '\n')})
	for _, img := range Images {
		ms = append(ms, member{name: FirmwareDir + "/" + img.Name, mode: 0o644, data: images[img.Name]})
	}
	slices.SortStableFunc(ms, func(a, b member) int { return strings.Compare(a.name, b.name) })
	return ms, notices[ThirdParty], nil
}

// checkBridge refuses anything but a macOS arm64 executable: the only
// platform the bridge supports (requirement 3.4).
func checkBridge(b []byte) error {
	f, err := macho.NewFile(bytes.NewReader(b))
	if err != nil || f.Cpu != macho.CpuArm64 || f.Type != macho.TypeExec {
		return errors.New("isn't a macOS arm64 executable")
	}
	return nil
}

// flashPart is one image in ESP-IDF's flasher_args.json.
type flashPart struct {
	Offset    string `json:"offset"`
	File      string `json:"file"`
	Encrypted string `json:"encrypted"`
}

// flasherArgs is the part of ESP-IDF's flasher_args.json the packer reads.
type flasherArgs struct {
	FlashFiles     map[string]string `json:"flash_files"`
	Bootloader     *flashPart        `json:"bootloader"`
	PartitionTable *flashPart        `json:"partition-table"`
	App            *flashPart        `json:"app"`
	Extra          struct {
		Chip string `json:"chip"`
	} `json:"extra_esptool_args"`
}

func (a flasherArgs) part(role string) *flashPart {
	switch role {
	case "bootloader":
		return a.Bootloader
	case "partition-table":
		return a.PartitionTable
	case "app":
		return a.App
	}
	return nil
}

// layoutWanted describes Images for a refusal.
func layoutWanted() string {
	var s []string
	for _, img := range Images {
		s = append(s, fmt.Sprintf("%s at %#x", img.Role, img.Offset))
	}
	return strings.Join(s, ", ")
}

// readFirmware reads and checks the ESP-IDF build in dir: the pager's flash
// layout, for the ESP32-S3, unencrypted, and an app that says it's
// herdr-pager at version. It returns the images by their names in the
// archive.
func readFirmware(dir, version string) (map[string][]byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "flasher_args.json"))
	if err != nil {
		return nil, fmt.Errorf("the firmware: no ESP-IDF build: %w", err)
	}
	var args flasherArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("the firmware's flasher_args.json: %w", err)
	}
	if args.Extra.Chip != "esp32s3" {
		return nil, fmt.Errorf("the firmware is built for chip %q, not esp32s3", args.Extra.Chip)
	}

	// flash_files is everything idf.py flash writes: it must be exactly the
	// three images, at the pager's offsets, or the manifest would flash
	// something else.
	files := map[int64]string{}
	for off, file := range args.FlashFiles {
		n, err := strconv.ParseInt(off, 0, 64)
		if err != nil {
			return nil, fmt.Errorf("the firmware's flasher_args.json: offset %q: %w", off, err)
		}
		files[n] = file
	}
	var found []string
	for _, n := range slices.Sorted(maps.Keys(files)) {
		found = append(found, fmt.Sprintf("%#x %s", n, files[n]))
	}
	badLayout := func(why string) error {
		return fmt.Errorf("the firmware's flash layout isn't the pager's (%s): %s; it flashes %s", layoutWanted(), why, strings.Join(found, ", "))
	}
	if len(files) != len(Images) {
		return nil, badLayout(fmt.Sprintf("%d images, not %d", len(files), len(Images)))
	}

	images := map[string][]byte{}
	for _, img := range Images {
		p := args.part(img.Role)
		if p == nil {
			return nil, badLayout("no " + img.Role)
		}
		off, err := strconv.ParseInt(p.Offset, 0, 64)
		if err != nil || off != img.Offset || files[off] != p.File {
			return nil, badLayout(fmt.Sprintf("the %s is %s at %s", img.Role, p.File, p.Offset))
		}
		if p.Encrypted != "false" {
			return nil, fmt.Errorf("the firmware's %s is encrypted (%q): the setup page flashes plain images", img.Role, p.Encrypted)
		}
		if !filepath.IsLocal(filepath.FromSlash(p.File)) {
			return nil, fmt.Errorf("the firmware's %s, %q, isn't inside the build directory", img.Role, p.File)
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p.File)))
		if err != nil {
			return nil, fmt.Errorf("the firmware's %s: %w", img.Role, err)
		}
		if err := checkImage(img, b, version); err != nil {
			return nil, err
		}
		images[img.Name] = b
	}
	return images, nil
}

// ESP-IDF's image formats (esp_image_format.h, esp_app_desc.h, esp_flash_partitions.h).
const (
	imageMagic   = 0xE9
	chipESP32S3  = 0x0009
	appDescAt    = 0x20 // the app's description begins its first segment: after the 24-byte image header and the 8-byte segment header
	appDescMagic = 0xABCD5432
	appDescSize  = 256
)

func checkImage(img Image, b []byte, version string) error {
	if img.Role == "partition-table" {
		if len(b) < 2 || b[0] != 0xAA || b[1] != 0x50 {
			return fmt.Errorf("%s isn't a partition table", img.Name)
		}
		return nil
	}
	if len(b) < 24 || b[0] != imageMagic {
		return fmt.Errorf("%s isn't an ESP-IDF image", img.Name)
	}
	if chip := binary.LittleEndian.Uint16(b[12:]); chip != chipESP32S3 {
		return fmt.Errorf("%s isn't for the ESP32-S3 (its chip ID is %d)", img.Name, chip)
	}
	if img.Role != "app" {
		return nil
	}
	if len(b) < appDescAt+appDescSize || binary.LittleEndian.Uint32(b[appDescAt:]) != appDescMagic {
		return fmt.Errorf("%s has no app description", img.Name)
	}
	desc := b[appDescAt:]
	if project := cString(desc[48:80]); project != Project {
		return fmt.Errorf("%s is project %q, not %s", img.Name, project, Project)
	}
	// The app's version is what the pager says as its fw, and the page
	// compares fw with the manifest's version (task 1.3).
	if v := cString(desc[16:48]); v != version {
		return fmt.Errorf("%s: the app says version %q, not %q (a published build passes -DPAGER_VERSION=<number> to idf.py)", img.Name, v, version)
	}
	return nil
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// readLicences reads the third-party licence texts in dir, sorted by name:
// at least one, none empty. Dotfiles (a Finder's .DS_Store) are left out.
func readLicences(dir string) ([]member, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("the third-party licences: %w", err)
	}
	var ms []member
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !e.Type().IsRegular() {
			return nil, fmt.Errorf("the third-party licences: %s/%s isn't a file", LicensesSource, e.Name())
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("the third-party licences: %w", err)
		}
		if len(b) == 0 {
			return nil, fmt.Errorf("the third-party licences: %s/%s is empty", LicensesSource, e.Name())
		}
		ms = append(ms, member{name: LicensesDir + "/" + e.Name(), mode: 0o644, data: b})
	}
	if len(ms) == 0 {
		return nil, fmt.Errorf("the third-party licences: %s holds none", LicensesSource)
	}
	return ms, nil
}

// tarGz is members under root/, a gzipped tar with no time, owner or name of
// the machine in it. The tar format is the plainest that holds each name
// (USTAR, or PAX for a long one), so it too depends only on the members.
func tarGz(root string, members []member) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	tw := tar.NewWriter(zw)
	all := append([]member{{name: "", mode: 0o755}}, members...)
	for _, m := range all {
		h := &tar.Header{
			Name:    root + "/" + m.name,
			Mode:    m.mode,
			ModTime: entryTime,
		}
		if m.name == "" || strings.HasSuffix(m.name, "/") {
			h.Typeflag = tar.TypeDir
		} else {
			h.Typeflag = tar.TypeReg
			h.Size = int64(len(m.data))
		}
		if err := tw.WriteHeader(h); err != nil {
			return nil, fmt.Errorf("%s: %w", h.Name, err)
		}
		if _, err := tw.Write(m.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeFile writes data to dst, mode 0644, by way of a temporary file in the
// same folder, so a failed write leaves nothing.
func writeFile(dst string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".pack-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// readComponent reads the component folder dir: every folder and regular
// file in it, by its slash-separated path, executables keeping 0755.
func readComponent(dir string) (map[string]member, error) {
	files := map[string]member{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		name := filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			files[name+"/"] = member{name: name + "/", mode: 0o755}
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			mode := int64(0o644)
			if info.Mode()&0o111 != 0 {
				mode = 0o755
			}
			files[name] = member{name: name, mode: mode, data: b}
		default:
			return fmt.Errorf("%s isn't a file or a folder", name)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("libssh's source: %w", err)
	}
	return files, nil
}
