// Command pack makes a published version's files (phase 5 tasks 4.1 and 4.2,
// requirement 3.1, design D5P-5), as internal/release describes them: the
// archive (the bridge, the firmware's images with ESP Web Tools' manifest
// beside it in share/herdr-pager/firmware/, THIRD_PARTY.md and the licences),
// libssh's source, THIRD_PARTY.md and SHA256SUMS. The release workflow runs
// it on CI's builds; the owner runs it on local builds to try the setup page
// on the board (task 7.2).
//
//	cd tools/release && go run ./pack --version 1.0.0 --bridge <pager-bridge> \
//	    --firmware <ESP-IDF build dir> \
//	    --libssh <firmware/managed_components/david-cermak__libssh> \
//	    --repo ../.. --out <new folder>
//
// It checks what it packs, and writes nothing if anything is wrong: the
// bridge must be a macOS arm64 executable; the firmware must be the pager's
// ESP-IDF build for the ESP32-S3 with its flash layout (0x0, 0x8000, 0x10000);
// the app must say it's herdr-pager at --version, the version the pager
// reports and the manifest says; and the libssh component must hold libssh's
// source and build files. It prints the paths of the files it wrote.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/MaverickHQ/herdr-pager/tools/release/internal/release"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

const usage = `usage: pack --version <version> --bridge <file> --firmware <dir> --libssh <dir> --repo <dir> --out <dir>

Writes a release's files to <out>: herdr-pager_<version>_darwin_arm64.tar.gz,
libssh-<libssh version>-source.tar.gz, THIRD_PARTY.md and SHA256SUMS.
  --version   the version: the firmware's own (1.0.0 for a published build, 1.0.0-dev from source)
  --bridge    pager-bridge, built for macOS arm64
  --firmware  the firmware's ESP-IDF build directory (flasher_args.json and its images)
  --libssh    the libssh component the firmware was built with (managed_components/david-cermak__libssh)
  --repo      the repository: LICENSE, THIRD_PARTY.md and tools/release/licenses/
  --out       a new or empty folder for the release's files
`

func run(args []string, stdout, stderr io.Writer) int {
	var o release.PackOptions
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	fs.StringVar(&o.Version, "version", "", "")
	fs.StringVar(&o.Bridge, "bridge", "", "")
	fs.StringVar(&o.Firmware, "firmware", "", "")
	fs.StringVar(&o.Libssh, "libssh", "", "")
	fs.StringVar(&o.Repo, "repo", "", "")
	fs.StringVar(&o.Out, "out", "", "")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || o.Version == "" || o.Bridge == "" || o.Firmware == "" || o.Libssh == "" || o.Repo == "" || o.Out == "" {
		fs.Usage()
		return 2
	}
	paths, err := release.Pack(o)
	if err != nil {
		fmt.Fprintf(stderr, "pack: %v\n", err)
		return 1
	}
	for _, p := range paths {
		fmt.Fprintln(stdout, p)
	}
	return 0
}
