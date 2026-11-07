package provision

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

// LocalHostName is the Mac's name, for prov_set's host_name, and for its
// host, <name>.local, when there's no --host (parent 2.9, phase 2 R4.3):
// `scutil --get LocalHostName`, else the host name's first label. It's one of
// the bridge's two process launches (KnownNetworks is the other), with a fixed
// argument vector (parent 13.3).
func LocalHostName() (string, error) {
	return localHostName(func(argv ...string) ([]byte, error) {
		return exec.Command(argv[0], argv[1:]...).Output()
	}, os.Hostname)
}

// localHostName is LocalHostName, given how to run a command and read the
// host name.
func localHostName(run func(argv ...string) ([]byte, error), hostname func() (string, error)) (string, error) {
	if out, err := run("/usr/sbin/scutil", "--get", "LocalHostName"); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name, nil
		}
	}
	name, err := hostname()
	if err != nil {
		return "", errors.New("can't find the Mac's name (scutil and the host name both failed): give --host")
	}
	name, _, _ = strings.Cut(name, ".")
	return name, nil
}
