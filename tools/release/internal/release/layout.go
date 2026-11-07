// Package release is a published version's artefacts (phase 5 group 4,
// requirement 3.1, design D5P-2 and D5P-5): what a release holds, the packer
// that makes it, and the checker that checks it.
//
// A release is exactly these files:
//
//	herdr-pager_<version>_darwin_arm64.tar.gz   the archive, below
//	libssh-<libssh version>-source.tar.gz       the david-cermak/libssh component as the component
//	                                            manager fetched it: libssh's source, its ESP-IDF port
//	                                            and their build files (LGPL-2.1, ADR 0004)
//	THIRD_PARTY.md                              the third-party notices
//	SHA256SUMS                                  sha256sum's lines for the other three, sorted by name
//
// The archive, herdr-pager_<version>_darwin_arm64.tar.gz, holds one folder,
// herdr-pager-<version>/, laid out as an install prefix, so the formula
// installs bin/ and share/ as they are and the bridge finds its firmware at
// ../share/herdr-pager/firmware from its own executable:
//
//	LICENSE                              herdr-pager's licence (Apache-2.0)
//	THIRD_PARTY.md                       the third-party notices
//	licenses/                            the third-party licence texts (tools/release/licenses/)
//	bin/pager-bridge                     the bridge, macOS arm64
//	share/herdr-pager/firmware/
//	    manifest.json                    ESP Web Tools' manifest
//	    bootloader.bin                   at 0x0
//	    partition-table.bin              at 0x8000
//	    herdr-pager.bin                  at 0x10000, the app
package release

import (
	"fmt"
	"regexp"
)

// Project is the product's name: the archive's, the folder's and the firmware
// project's (firmware/CMakeLists.txt's project()).
const Project = "herdr-pager"

// Paths inside the archive's folder.
const (
	BridgePath   = "bin/pager-bridge"
	FirmwareDir  = "share/herdr-pager/firmware"
	ManifestName = "manifest.json"
	LicensesDir  = "licenses"
)

// LicensesSource is where the packer takes the third-party licence texts
// from, in the repository.
const LicensesSource = "tools/release/licenses"

// The release's other files.
const (
	ThirdParty = "THIRD_PARTY.md"
	SumsName   = "SHA256SUMS"
)

// ArchiveName is the archive's file name for version.
func ArchiveName(version string) string { return Project + "_" + version + "_darwin_arm64.tar.gz" }

// Root is the archive's one top-level folder for version.
func Root(version string) string { return Project + "-" + version }

// LibsshSourceName is the file name of libssh's source at version.
func LibsshSourceName(version string) string { return "libssh-" + version + "-source.tar.gz" }

// LibsshRoot is the one top-level folder of libssh's source at version.
func LibsshRoot(version string) string { return "libssh-" + version }

// Image is one of the images the pager is flashed with.
type Image struct {
	Role   string // its key in ESP-IDF's flasher_args.json
	Name   string // its name in the archive and the manifest
	Offset int64
}

// Images is the pager's flash layout, as ESP-IDF builds it for one large app
// partition (firmware/sdkconfig.defaults.esp32s3): exactly these three, no
// more. A build with another layout is refused, not packed with a manifest
// that would flash it wrong.
var Images = []Image{
	{Role: "bootloader", Name: "bootloader.bin", Offset: 0x0},
	{Role: "partition-table", Name: "partition-table.bin", Offset: 0x8000},
	{Role: "app", Name: "herdr-pager.bin", Offset: 0x10000},
}

// Manifest is ESP Web Tools' manifest: what the setup page flashes, and the
// version it compares with the pager's fw.
type Manifest struct {
	Name    string  `json:"name"`
	Version string  `json:"version"`
	Builds  []Build `json:"builds"`
}

// Build is one chip's images in a Manifest.
type Build struct {
	ChipFamily string `json:"chipFamily"`
	Parts      []Part `json:"parts"`
}

// Part is one image in a Build; its path is relative to the manifest.
type Part struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
}

// NewManifest is the manifest for version's firmware.
func NewManifest(version string) Manifest {
	b := Build{ChipFamily: "ESP32-S3"}
	for _, img := range Images {
		b.Parts = append(b.Parts, Part{Path: img.Name, Offset: img.Offset})
	}
	return Manifest{Name: Project, Version: version, Builds: []Build{b}}
}

// The characters hello's fw may hold (firmware/CMakeLists.txt), and at most
// 31 of them: the app description's version field is 32 bytes with its NUL.
var versionRE = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,31}$`)

// CheckVersion refuses a version the pager couldn't report as its fw.
func CheckVersion(v string) error {
	if !versionRE.MatchString(v) {
		return fmt.Errorf("version %q: want 1 to 31 of 0-9 A-Z a-z . + - (what the pager's fw can say)", v)
	}
	return nil
}
