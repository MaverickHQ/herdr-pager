// Package version is herdr-pager's version: the one place both the bridge's
// and the firmware's come from (phase 5 task 1.3, requirement 3.3).
//
// VERSION, beside this file, holds the number of the published version the
// tree is building toward. It lives here, not at the repository's root,
// because go:embed reads only files inside its package; the firmware's CMake
// reads it from here (firmware/CMakeLists.txt).
//
// A build that isn't a published version says <number>-dev. The release
// builds the bridge with
//
//	-ldflags "-X github.com/MaverickHQ/herdr-pager/bridge/internal/version.published=<number>"
//
// and the firmware with -DPAGER_VERSION=<number>, from VERSION, so both say
// the published version.
package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var file string

// published is the published version, set by the release's -ldflags; empty
// in any other build.
var published string

// Number is VERSION's number.
func Number() string { return strings.TrimSpace(file) }

// String is the bridge's version: the published version in a release build,
// otherwise Number with "-dev".
func String() string {
	if published != "" {
		return published
	}
	return Number() + "-dev"
}
