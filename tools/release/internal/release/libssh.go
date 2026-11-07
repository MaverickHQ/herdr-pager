package release

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// The libssh licence the firmware can carry (ADR 0004), and where its build
// file says libssh's version.
const libsshLicence = "LGPL-2.1"

var libsshProject = regexp.MustCompile(`(?m)^project\(libssh VERSION ([0-9][0-9A-Za-z.+-]*)`)

// CheckLibsshSource checks that files, by their slash-separated paths in the
// david-cermak/libssh component, are libssh's full source with its build
// files: the component's manifest (LGPL-2.1, its version) and build file,
// and libssh's licence, build file (the same version) and C sources. It
// returns libssh's version.
func CheckLibsshSource(files map[string][]byte) (string, error) {
	manifest, ok := files["idf_component.yml"]
	if !ok {
		return "", errors.New("no idf_component.yml: not an ESP-IDF component as the component manager fetches it")
	}
	version := topLevel(manifest, "version")
	if version == "" {
		return "", errors.New("idf_component.yml says no version")
	}
	if l := topLevel(manifest, "license"); l != libsshLicence {
		return "", fmt.Errorf("idf_component.yml says licence %q, not %s", l, libsshLicence)
	}
	for _, need := range []string{"CMakeLists.txt", "libssh-mirror/COPYING", "libssh-mirror/CMakeLists.txt"} {
		if _, ok := files[need]; !ok {
			return "", fmt.Errorf("no %s", need)
		}
	}
	m := libsshProject.FindSubmatch(files["libssh-mirror/CMakeLists.txt"])
	if m == nil {
		return "", errors.New("libssh-mirror/CMakeLists.txt says no libssh version")
	}
	if string(m[1]) != version {
		return "", fmt.Errorf("its libssh is %s, but idf_component.yml says %s", m[1], version)
	}
	for name := range files {
		if dir, file := path.Split(name); dir == "libssh-mirror/src/" && strings.HasSuffix(file, ".c") {
			return version, nil
		}
	}
	return "", errors.New("no C sources in libssh-mirror/src/")
}

// topLevel is the value of a top-level key: value line in a YAML file,
// unquoted.
func topLevel(yaml []byte, key string) string {
	for _, line := range strings.Split(string(yaml), "\n") {
		if v, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
