package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// Checked is what Verify found in a complete release.
type Checked struct {
	Version       string // herdr-pager's
	LibsshVersion string
}

var (
	archiveRE = regexp.MustCompile(`^` + Project + `_(.+)_darwin_arm64\.tar\.gz$`)
	libsshRE  = regexp.MustCompile(`^libssh-(.+)-source\.tar\.gz$`)
	sumLineRE = regexp.MustCompile(`^([0-9a-fA-F]{64}) [ *](.+)$`)
)

// Verify checks the release's files in dir (S5P-1): exactly the archive,
// libssh's source, THIRD_PARTY.md and SHA256SUMS; each file's sha256 as
// SHA256SUMS lists it; the archive holding the bridge, the firmware (the
// manifest and the images it names, the app at the release's version), the
// notices and the licences; and libssh's source holding its full source and
// build files. It returns every problem it finds, or none; err is for a dir
// it can't read.
func Verify(dir string) (Checked, []string, error) {
	var c Checked
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	ents, err := os.ReadDir(dir)
	if err != nil {
		return c, nil, err
	}
	inDir := map[string]bool{}
	for _, e := range ents {
		if !e.Type().IsRegular() {
			add("%s isn't a file", e.Name())
			continue
		}
		inDir[e.Name()] = true
	}

	sums := map[string]string{}
	if !inDir[SumsName] {
		add("%s is missing", SumsName)
	} else {
		b, err := os.ReadFile(filepath.Join(dir, SumsName))
		if err != nil {
			return c, nil, err
		}
		for i, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
			m := sumLineRE.FindStringSubmatch(line)
			switch {
			case m == nil:
				add("%s line %d isn't sha256sum's form: %q", SumsName, i+1, line)
			case strings.ContainsAny(m[2], `/\`):
				add("%s lists %q, which isn't a file in the release's folder", SumsName, m[2])
			case sums[m[2]] != "":
				add("%s lists %s twice", SumsName, m[2])
			default:
				sums[m[2]] = strings.ToLower(m[1])
			}
		}
	}

	// Every name in the folder or SHA256SUMS.
	all := map[string]bool{}
	for n := range inDir {
		all[n] = true
	}
	for n := range sums {
		all[n] = true
	}

	// Which archive and which libssh source.
	pick := func(re *regexp.Regexp, what string) (name, version string) {
		var names []string
		for _, n := range slices.Sorted(maps.Keys(all)) {
			if re.MatchString(n) {
				names = append(names, n)
			}
		}
		switch len(names) {
		case 0:
			add("no %s", what)
			return "", ""
		case 1:
			return names[0], re.FindStringSubmatch(names[0])[1]
		default:
			add("more than one %s: %s", what, strings.Join(names, ", "))
			return "", ""
		}
	}
	archive, version := pick(archiveRE, "archive ("+ArchiveName("<version>")+")")
	source, libsshVersion := pick(libsshRE, "libssh source ("+LibsshSourceName("<version>")+"), which the release must carry (LGPL-2.1, ADR 0004)")
	c.Version, c.LibsshVersion = version, libsshVersion

	// Exactly the release's files, each as SHA256SUMS says.
	want := map[string]bool{ThirdParty: true, SumsName: true}
	for _, n := range []string{archive, source} {
		if n != "" {
			want[n] = true
		}
	}
	for _, n := range slices.Sorted(maps.Keys(all)) {
		if !want[n] && !archiveRE.MatchString(n) && !libsshRE.MatchString(n) {
			add("%s isn't one of the release's files", n)
		}
	}
	read := map[string][]byte{}
	for _, n := range slices.Sorted(maps.Keys(want)) {
		if !inDir[n] {
			if n != SumsName {
				add("%s is missing", n)
			}
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return c, nil, err
		}
		read[n] = b
		if n == SumsName {
			continue
		}
		listed, ok := sums[n]
		if !ok {
			if inDir[SumsName] {
				add("%s doesn't list %s", SumsName, n)
			}
			continue
		}
		if got := sha256.Sum256(b); hex.EncodeToString(got[:]) != listed {
			add("%s doesn't match SHA256SUMS: its sha256 is %x; SHA256SUMS says %s", n, got, listed)
		}
	}

	if b, ok := read[archive]; ok {
		for _, p := range checkArchive(b, version, read[ThirdParty]) {
			add("%s: %s", archive, p)
		}
	}
	if b, ok := read[source]; ok {
		if err := checkLibsshArchive(b, libsshVersion); err != nil {
			add("%s: %v", source, err)
		}
	}
	return c, problems, nil
}

// tarFile is a regular file in a gzipped tar.
type tarFile struct {
	mode int64
	data []byte
}

// readTarGz reads the regular files of a gzipped tar under root/, by their
// paths below it.
func readTarGz(b []byte, root string) (map[string]tarFile, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("not gzip: %w", err)
	}
	tr := tar.NewReader(zr)
	files := map[string]tarFile{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, fmt.Errorf("not a tar archive: %w", err)
		}
		rel, ok := strings.CutPrefix(h.Name, root+"/")
		if !ok && h.Name != root {
			return nil, fmt.Errorf("%s is outside its folder, %s/", h.Name, root)
		}
		switch h.Typeflag {
		case tar.TypeDir:
		case tar.TypeReg:
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			files[rel] = tarFile{mode: h.Mode, data: data}
		default:
			return nil, fmt.Errorf("%s isn't a file or a folder", h.Name)
		}
	}
}

// checkArchive checks the archive of version, whose THIRD_PARTY.md should be
// the release's, thirdParty (nil if the release has none).
func checkArchive(b []byte, version string, thirdParty []byte) []string {
	files, err := readTarGz(b, Root(version))
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	if f, ok := files[BridgePath]; !ok {
		add("no %s", BridgePath)
	} else if err := checkBridge(f.data); err != nil {
		add("%s %v", BridgePath, err)
	} else if f.mode&0o111 == 0 {
		add("%s isn't executable (mode %o)", BridgePath, f.mode)
	}

	for _, n := range []string{"LICENSE", ThirdParty} {
		if f, ok := files[n]; !ok || len(f.data) == 0 {
			add("no %s", n)
		}
	}
	if f, ok := files[ThirdParty]; ok && thirdParty != nil && !bytes.Equal(f.data, thirdParty) {
		add("its %s isn't the release's", ThirdParty)
	}
	licences := 0
	for n, f := range files {
		if strings.HasPrefix(n, LicensesDir+"/") && len(f.data) > 0 {
			licences++
		}
	}
	if licences == 0 {
		add("no third-party licences in %s/", LicensesDir)
	}

	mf, ok := files[FirmwareDir+"/"+ManifestName]
	if !ok {
		add("no firmware: %s/%s is missing", FirmwareDir, ManifestName)
		return problems
	}
	var m Manifest
	if err := json.Unmarshal(mf.data, &m); err != nil {
		add("the firmware's %s: %v", ManifestName, err)
	} else if want := NewManifest(version); !reflect.DeepEqual(m, want) {
		add("the firmware's %s isn't the pager's at %s: %s", ManifestName, version, mf.data)
	}
	for _, img := range Images {
		f, ok := files[FirmwareDir+"/"+img.Name]
		if !ok {
			add("no firmware image %s/%s", FirmwareDir, img.Name)
			continue
		}
		if err := checkImage(img, f.data, version); err != nil {
			add("the firmware: %v", err)
		}
	}
	return problems
}

// checkLibsshArchive checks libssh's source, at version.
func checkLibsshArchive(b []byte, version string) error {
	files, err := readTarGz(b, LibsshRoot(version))
	if err != nil {
		return err
	}
	src := map[string][]byte{}
	for n, f := range files {
		src[n] = f.data
	}
	got, err := CheckLibsshSource(src)
	if err != nil {
		return err
	}
	if got != version {
		return fmt.Errorf("it holds libssh %s, not %s", got, version)
	}
	return nil
}
