package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// launchdPlist is the launchd user agent that runs serve at login (parent
// 11.4). It's a template: docs/bridge.md's install commands fill in its
// placeholders with sed.
var launchdPlist = filepath.Join("..", "..", "contrib", "launchd", "dev.herdr-pager.bridge.plist")

// bridgeGuide is the doc whose commands install launchdPlist.
var bridgeGuide = filepath.Join("..", "..", "docs", "bridge.md")

// placeholder is a value the plist leaves to its installer, such as __HOME__.
var placeholder = regexp.MustCompile(`__[A-Z]+__`)

// TestLaunchdPlistLints checks contrib/launchd's plist as shipped, and as
// docs/bridge.md installs it (__REPO__ and __HOME__ filled in): plutil -lint
// passes it, and launchd runs `<repo>/bin/pager-bridge serve` at login and
// again if it fails, with HOME set, logging to the file the guide names.
// plutil runs with a fixed argument vector.
func TestLaunchdPlistLints(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("plutil and launchd are macOS's")
	}
	template, err := os.ReadFile(launchdPlist)
	if err != nil {
		t.Fatal(err)
	}
	guide, err := os.ReadFile(bridgeGuide)
	if err != nil {
		t.Fatal(err)
	}
	// What the guide's sed does: each placeholder in the template must be one
	// the guide fills in.
	for _, p := range slices.Compact(slices.Sorted(slices.Values(placeholder.FindAllString(string(template), -1)))) {
		if !strings.Contains(string(guide), "s|"+p+"|") {
			t.Errorf("the plist has the placeholder %s, which docs/bridge.md's install commands don't fill in", p)
		}
	}
	repo, home := "/Volumes/Work/src/herdr pager", "/var/root"
	text := strings.NewReplacer("__REPO__", repo, "__HOME__", home).Replace(string(template))
	if left := placeholder.FindAllString(text, -1); len(left) > 0 {
		t.Errorf("installed, the plist still has the placeholders %q", left)
	}
	installed := filepath.Join(t.TempDir(), "dev.herdr-pager.bridge.plist")
	if err := os.WriteFile(installed, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ name, path, repo, home string }{
		{"as shipped", launchdPlist, "__REPO__", "__HOME__"},
		{"as installed", installed, repo, home},
	} {
		t.Run(c.name, func(t *testing.T) {
			if out, err := exec.Command("/usr/bin/plutil", "-lint", c.path).CombinedOutput(); err != nil {
				t.Fatalf("plutil -lint: %v\n%s", err, out)
			}
			out, err := exec.Command("/usr/bin/plutil", "-convert", "json", "-o", "-", c.path).Output()
			if err != nil {
				t.Fatalf("plutil -convert json: %v", err)
			}
			var job struct {
				Label                string
				ProgramArguments     []string
				RunAtLoad            bool
				KeepAlive            map[string]bool
				EnvironmentVariables map[string]string
				StandardErrorPath    string
			}
			if err := json.Unmarshal(out, &job); err != nil {
				t.Fatalf("the plist as JSON: %v\n%s", err, out)
			}
			if job.Label != "dev.herdr-pager.bridge" {
				t.Errorf("Label = %q, want dev.herdr-pager.bridge (the guide's launchctl commands name it)", job.Label)
			}
			if want := []string{c.repo + "/bin/pager-bridge", "serve"}; !slices.Equal(job.ProgramArguments, want) {
				t.Errorf("ProgramArguments = %q, want %q: the binary the guide builds, running serve", job.ProgramArguments, want)
			}
			if !job.RunAtLoad {
				t.Error("RunAtLoad isn't true, so serve wouldn't start at login")
			}
			if succeeded, ok := job.KeepAlive["SuccessfulExit"]; !ok || succeeded {
				t.Errorf("KeepAlive = %v, want SuccessfulExit false: restarted when it fails, left stopped when it exits 0 on SIGTERM", job.KeepAlive)
			}
			if got := job.EnvironmentVariables["HOME"]; got != c.home {
				t.Errorf("HOME = %q, want %q: serve finds its files under it", got, c.home)
			}
			if want := c.home + "/Library/Logs/herdr-pager/bridge.log"; job.StandardErrorPath != want {
				t.Errorf("StandardErrorPath = %q, want %q: serve logs to stderr, and the guide names this file", job.StandardErrorPath, want)
			}
		})
	}
}
