// The pattern of lane cli's bridge/internal/provision/host.go (phase 2, task
// 2.12): the launch takes its argument vector from a function value, which
// the one function it's passed to calls with constants only.
package allowed

import (
	"os"
	"os/exec"
	"strings"
)

func LocalHostName() (string, error) {
	return localHostName(func(argv ...string) ([]byte, error) {
		return exec.Command(argv[0], argv[1:]...).Output()
	}, os.Hostname)
}

func localHostName(run func(argv ...string) ([]byte, error), hostname func() (string, error)) (string, error) {
	if out, err := run("/usr/sbin/scutil", "--get", "LocalHostName"); err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	return hostname()
}
