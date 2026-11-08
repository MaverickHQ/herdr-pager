package version

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A published version's number (requirement 3.3): plain semantic versioning,
// as the release tags it (v1.0.0 is "1.0.0").
// publishedNumber is a published version's number: major.minor.patch, and
// for a pre-release such as a release candidate, a SemVer suffix (1.0.0-rc.1).
var publishedNumber = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$`)

// TestAPublishedNumber is publishedNumber's own cases.
func TestAPublishedNumber(t *testing.T) {
	for v, want := range map[string]bool{
		"1.0.0": true, "1.0.0-rc.1": true, "12.3.45-beta": true,
		"1.0": false, "v1.0.0": false, "1.0.0-": false, "1.0.0-rc..1": false, "1.0.0+build": false, "1.0.0-dev ": false,
	} {
		if got := publishedNumber.MatchString(v); got != want {
			t.Errorf("publishedNumber(%q) = %v; want %v", v, got, want)
		}
	}
}

// TestVersionFileHoldsAPublishedNumber reads VERSION as the firmware's CMake
// and the release workflow do: one line, a published version's number.
func TestVersionFileHoldsAPublishedNumber(t *testing.T) {
	data, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "\n") || strings.Count(string(data), "\n") != 1 {
		t.Errorf("VERSION is %q; want one line, ending in a newline", data)
	}
	line := strings.TrimSuffix(string(data), "\n")
	if !publishedNumber.MatchString(line) {
		t.Errorf("VERSION says %q; want a published version's number, such as 1.0.0", line)
	}
	if Number() != line {
		t.Errorf("Number() = %q; want VERSION's %q", Number(), line)
	}
}

// TestADevBuildSaysDev: a build that isn't a published version (no -ldflags)
// says so (task 1.3).
func TestADevBuildSaysDev(t *testing.T) {
	if published != "" {
		t.Skipf("built as published version %q", published)
	}
	if got, want := String(), Number()+"-dev"; got != want {
		t.Errorf("String() = %q; want %q", got, want)
	}
}

// TestAReleaseBuildSaysItsVersion: the release sets the published version
// with -ldflags, and that's what the bridge says.
func TestAReleaseBuildSaysItsVersion(t *testing.T) {
	saved := published
	t.Cleanup(func() { published = saved })
	published = "9.8.7"
	if got := String(); got != "9.8.7" {
		t.Errorf("String() = %q; want the published 9.8.7", got)
	}
}
