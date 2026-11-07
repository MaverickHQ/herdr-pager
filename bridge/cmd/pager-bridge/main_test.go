package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runMainEnv makes the test binary run the CLI itself instead of the tests,
// so tests drive pager-bridge as a process.
const runMainEnv = "PAGER_BRIDGE_TEST_RUN_MAIN"

// TestMain gives every test a temporary HOME and no herdr socket variable,
// so nothing can reach the owner's config or herdr.
func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	home, err := os.MkdirTemp("", "hp")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Unsetenv("HERDR_SOCKET_PATH")
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// pagerBridge runs the CLI as a process with args and stdin closed, and
// returns its stdout, stderr and exit code.
func pagerBridge(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return out.String(), errOut.String(), code
}

// TestHelpListsEveryCommand checks that help names every command (parent
// 11.1–11.3, 12.1, 12.2), and that a missing or unknown command prints the
// same usage as an error.
func TestHelpListsEveryCommand(t *testing.T) {
	commands := []string{"init", "serve", "stdio", "status", "provision", "devices list", "devices revoke",
		"setup [--firmware <dir>]", "doctor [--json]"} // phase 5 task 1.4: requirements 2.1, 2.8; tasks 3.1, 3.4
	for _, help := range []string{"help", "-h", "--help"} {
		stdout, stderr, code := pagerBridge(t, help)
		if code != 0 || stderr != "" {
			t.Errorf("%s: exit %d, stderr %q; want exit 0 and no stderr", help, code, stderr)
		}
		for _, c := range commands {
			if !strings.Contains(stdout, "\n  pager-bridge "+c) {
				t.Errorf("%s doesn't list %q:\n%s", help, c, stdout)
			}
		}
	}
	// A bad flag makes each exit at once with its own flag error, so this checks
	// only that they're dispatched: run bare, setup serves its page until Ctrl-C
	// and opens a browser, and doctor reads the Mac's firewall and brew (open-items
	// O-93).
	for _, c := range []string{"setup", "doctor"} {
		if _, stderr, _ := pagerBridge(t, c, "--no-such-flag"); strings.Contains(stderr, "no command") {
			t.Errorf("%s isn't dispatched: %q", c, stderr)
		}
	}
	if stdout, _, _ := pagerBridge(t, "help"); !strings.Contains(stdout, "\n  pager-bridge --version\n") {
		t.Errorf("help doesn't list --version:\n%s", stdout)
	}
	for _, args := range [][]string{{}, {"frobnicate"}} {
		stdout, stderr, code := pagerBridge(t, args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "\n  pager-bridge devices revoke") {
			t.Errorf("%q: exit %d, stdout %q, stderr %q; want exit 2 and the usage on stderr", args, code, stdout, stderr)
		}
	}
}

// TestVersionFlag checks --version (phase 5 task 1.3, requirement 3.3): a
// build that isn't a published version says VERSION's number with "-dev"; a
// build given the published version by -ldflags, as the release builds it,
// says that version.
func TestVersionFlag(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "version", "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	number := strings.TrimSpace(string(data))
	for _, flag := range []string{"--version", "-version"} {
		stdout, stderr, code := pagerBridge(t, flag)
		if want := "pager-bridge " + number + "-dev\n"; code != 0 || stdout != want || stderr != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit 0 and %q", flag, code, stdout, stderr, want)
		}
	}

	if testing.Short() {
		t.Skip("-short: not building a release binary")
	}
	bin := filepath.Join(t.TempDir(), "pager-bridge")
	build := exec.Command("go", "build", "-o", bin,
		"-ldflags", "-X github.com/MaverickHQ/herdr-pager/bridge/internal/version.published=9.8.7", ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil || string(out) != "pager-bridge 9.8.7\n" {
		t.Errorf("the release build's --version: %q, %v; want %q", out, err, "pager-bridge 9.8.7\n")
	}
}
