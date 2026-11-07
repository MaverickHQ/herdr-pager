// Command verify checks a release's files before they're public, and after
// (phase 5 task 4.2, seam S5P-1, requirement 3.1):
//
//	cd tools/release && go run ./verify <dir>
//
// <dir> holds exactly what internal/release says a release is: the archive,
// libssh's source, THIRD_PARTY.md and SHA256SUMS. It fails on a missing or
// extra file, a checksum that doesn't match SHA256SUMS, an archive without
// its bridge, firmware, notices or licences, or no libssh source, and prints
// every problem it finds. It runs on a dry run's artefacts (task 8.4) and on
// the published release's (task 9.5). The build attestations aren't files in
// <dir>: `gh attestation verify` checks those.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/MaverickHQ/herdr-pager/tools/release/internal/release"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stderr, "usage: verify <dir>\n\nChecks the release's files in <dir>: the archive, libssh's source, THIRD_PARTY.md and SHA256SUMS.\n")
		return 2
	}
	c, problems, err := release.Verify(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return 1
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(stderr, "verify: %s\n", p)
		}
		return 1
	}
	fmt.Fprintf(stdout, "verify: herdr-pager %s: the archive (the bridge, the firmware, the notices and licences), libssh %s's source, %s and %s check out\n",
		c.Version, c.LibsshVersion, release.ThirdParty, release.SumsName)
	return 0
}
