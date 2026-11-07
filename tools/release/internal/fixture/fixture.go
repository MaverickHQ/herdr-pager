// Package fixture makes stand-ins for a release's inputs, for tools/release's
// tests: ESP-IDF images with just enough header to pass the packer's checks, an
// ESP-IDF build directory, a Mach-O header for the bridge, and a repository
// with its licence files. None of them runs.
package fixture

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// The ESP32-S3's chip ID in an ESP-IDF image header (esp_image_header_t), and
// the magic of the app's description (esp_app_desc_t).
const (
	ChipESP32S3  = 0x0009
	ChipESP32    = 0x0000
	appDescMagic = 0xABCD5432
)

// Image is an ESP-IDF image: a 24-byte header with the image magic and chipID,
// then one segment holding payload.
func Image(chipID uint16, payload []byte) []byte {
	h := make([]byte, 24)
	h[0] = 0xE9 // ESP_IMAGE_HEADER_MAGIC
	h[1] = 1    // one segment
	h[2] = 2    // DIO
	binary.LittleEndian.PutUint16(h[12:], chipID)
	seg := make([]byte, 8)
	binary.LittleEndian.PutUint32(seg[4:], uint32(len(payload)))
	return append(append(h, seg...), payload...)
}

// App is an app image whose description (at 0x20, the start of its first
// segment, as ESP-IDF places it) says version and project.
func App(chipID uint16, version, project string) []byte {
	desc := make([]byte, 256)
	binary.LittleEndian.PutUint32(desc[0:], appDescMagic)
	copy(desc[16:48], version)
	copy(desc[48:80], project)
	return Image(chipID, append(desc, bytes.Repeat([]byte{0xA5}, 512)...))
}

// Bootloader is a bootloader image for the ESP32-S3.
func Bootloader() []byte { return Image(ChipESP32S3, bytes.Repeat([]byte{0x5A}, 256)) }

// PartitionTable is a partition table: its first entry's magic, then 0xFF.
func PartitionTable() []byte {
	pt := bytes.Repeat([]byte{0xFF}, 0xC00)
	pt[0], pt[1] = 0xAA, 0x50
	copy(pt[2:], "fixture partition table")
	return pt
}

// Mach-O CPU types.
const (
	CPUArm64 = 0x0100000C
	CPUAmd64 = 0x01000007
)

// MachO is a 64-bit Mach-O executable's header for cpu, with no load
// commands, then a marker so two binaries differ.
func MachO(cpu uint32) []byte {
	h := make([]byte, 32)
	binary.LittleEndian.PutUint32(h[0:], 0xFEEDFACF) // MH_MAGIC_64
	binary.LittleEndian.PutUint32(h[4:], cpu)
	binary.LittleEndian.PutUint32(h[12:], 2) // MH_EXECUTE
	return append(h, []byte("fixture pager-bridge")...)
}

// FlasherArgs is flasher_args.json as ESP-IDF v5.5 writes it for the pager's
// layout (one large app partition): the bootloader at 0x0, the partition table
// at 0x8000 and the app at 0x10000.
const FlasherArgs = `{
    "write_flash_args" : [ "--flash_mode", "dio",
                           "--flash_size", "16MB",
                           "--flash_freq", "80m" ],
    "flash_settings" : {
        "flash_mode": "dio",
        "flash_size": "16MB",
        "flash_freq": "80m"
    },
    "flash_files" : {
        "0x0" : "bootloader/bootloader.bin",
        "0x10000" : "herdr-pager.bin",
        "0x8000" : "partition_table/partition-table.bin"
    },
    "bootloader" : { "offset" : "0x0", "file" : "bootloader/bootloader.bin", "encrypted" : "false" },
    "app" : { "offset" : "0x10000", "file" : "herdr-pager.bin", "encrypted" : "false" },
    "partition-table" : { "offset" : "0x8000", "file" : "partition_table/partition-table.bin", "encrypted" : "false" },
    "extra_esptool_args" : {
        "after"  : "hard_reset",
        "before" : "default_reset",
        "stub"   : true,
        "chip"   : "esp32s3"
    }
}
`

// Inputs are a release's inputs on disk.
type Inputs struct {
	Bridge   string // the bridge's binary
	Firmware string // the ESP-IDF build directory
	Libssh   string // the david-cermak/libssh component, as the component manager fetched it
	Repo     string // the repository: LICENSE, THIRD_PARTY.md, tools/release/licenses/
}

// LibsshFiles are the files of New's libssh component, a stand-in for
// managed_components/david-cermak__libssh: its manifest and build file, its
// port, and libssh's own source with its licence and build file.
var LibsshFiles = map[string]string{
	"idf_component.yml":              "dependencies:\n  espressif/sock_utils:\n    version: '*'\nlicense: LGPL-2.1\nurl: https://github.com/david-cermak/libssh\nversion: 0.12.2\n",
	"CMakeLists.txt":                 "idf_component_register(SRCS port/src/sshd.c)\n",
	"install.sh":                     "#!/bin/sh\necho fixture\n",
	"port/src/sshd.c":                "/* fixture port */\n",
	"libssh-mirror/COPYING":          "GNU LESSER GENERAL PUBLIC LICENSE\nVersion 2.1, February 1999\nfixture\n",
	"libssh-mirror/CMakeLists.txt":   "cmake_minimum_required(VERSION 3.14.0)\nproject(libssh VERSION 0.12.2 LANGUAGES C)\n",
	"libssh-mirror/src/session.c":    "/* fixture libssh session */\n",
	"libssh-mirror/include/libssh.h": "/* fixture */\n",
}

// Licences are the third-party licence texts New puts in the repository's
// tools/release/licenses/.
var Licences = map[string]string{
	"Go.txt":   "Copyright 2009 The Go Authors.\nfixture licence text\n",
	"LVGL.txt": "MIT licence\nCopyright (c) 2021 LVGL Kft\nfixture licence text\n",
}

// New writes a complete set of inputs for version under a temporary
// directory: an arm64 bridge, an ESP-IDF build of the pager whose app says
// version, the libssh component, and a repository with its licences.
func New(t *testing.T, version string) Inputs {
	t.Helper()
	root := t.TempDir()
	in := Inputs{
		Bridge:   filepath.Join(root, "pager-bridge"),
		Firmware: filepath.Join(root, "build"),
		Libssh:   filepath.Join(root, "managed_components", "david-cermak__libssh"),
		Repo:     filepath.Join(root, "repo"),
	}
	for name, text := range LibsshFiles {
		Write(t, filepath.Join(in.Libssh, filepath.FromSlash(name)), []byte(text))
	}
	if err := os.Chmod(filepath.Join(in.Libssh, "install.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	Write(t, in.Bridge, MachO(CPUArm64))
	Write(t, filepath.Join(in.Firmware, "flasher_args.json"), []byte(FlasherArgs))
	Write(t, filepath.Join(in.Firmware, "bootloader", "bootloader.bin"), Bootloader())
	Write(t, filepath.Join(in.Firmware, "partition_table", "partition-table.bin"), PartitionTable())
	Write(t, filepath.Join(in.Firmware, "herdr-pager.bin"), App(ChipESP32S3, version, "herdr-pager"))
	Write(t, filepath.Join(in.Repo, "LICENSE"), []byte("Apache License\nVersion 2.0, January 2004\nfixture\n"))
	Write(t, filepath.Join(in.Repo, "THIRD_PARTY.md"), []byte("# Third-party software\n\nfixture\n"))
	for name, text := range Licences {
		Write(t, filepath.Join(in.Repo, "tools", "release", "licenses", name), []byte(text))
	}
	return in
}

// Write writes data to path, making its directories.
func Write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Entry is one member of a gzipped tar, as the tests read and write them
// (with their own code, not the tools').
type Entry struct {
	Name string
	Type byte
	Mode int64
	Data []byte
}

// ReadTarGz reads the gzipped tar at path, in order.
func ReadTarGz(t *testing.T, path string) []Entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("%s isn't gzip: %v", path, err)
	}
	tr := tar.NewReader(zr)
	var es []Entry
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return es
		}
		if err != nil {
			t.Fatalf("%s isn't a tar archive: %v", path, err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		es = append(es, Entry{Name: h.Name, Type: h.Typeflag, Mode: h.Mode, Data: data})
	}
}

// WriteTarGz writes es to path as a gzipped tar.
func WriteTarGz(t *testing.T, path string, es []Entry) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range es {
		h := &tar.Header{Name: e.Name, Typeflag: e.Type, Mode: e.Mode, Size: int64(len(e.Data))}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	Write(t, path, buf.Bytes())
}

// SHA256SUMS is sha256sum's output for the named files in dir.
func SHA256SUMS(t *testing.T, dir string, names ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, n := range names {
		fmt.Fprintf(&b, "%x  %s\n", sha256.Sum256(Read(t, filepath.Join(dir, n))), n)
	}
	return b.Bytes()
}

// Read reads path.
func Read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
