// Package emutest is the pager's emulator harness (phase 2 design D2-8, ADR
// 0007). It drives the kit's lcd349 CLI against the real firmware (built for
// the emulator), and compares screenshots with goldens in firmware/scenarios/.
//
// Tests that need the emulator call Start, which skips them unless
// PAGER_EMUTEST=1, so the bridge's ordinary suite never needs the firmware
// built. Instances live under LCD349_INSTANCES when it is set (the runner
// gives each lane its own), else the kit's default directory.
package emutest

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// EnvVar enables the emulator tests when set to 1.
const EnvVar = "PAGER_EMUTEST"

// DefaultClock starts every instance's RTC at the same time, so screens repeat.
const DefaultClock = "2026-10-22T09:00:00Z"

const (
	cmdTimeout   = 3 * time.Minute
	startTimeout = 20 * time.Second
)

// Enabled reports whether PAGER_EMUTEST=1.
func Enabled() bool { return os.Getenv(EnvVar) == "1" }

// Require skips t unless PAGER_EMUTEST=1.
func Require(t testing.TB) {
	t.Helper()
	if !Enabled() {
		t.Skip("the emulator harness runs only with " + EnvVar + "=1")
	}
}

// FirmwareDir is the firmware project, found from this package's directory
// (go test runs a package's tests there).
func FirmwareDir() string {
	dir, err := filepath.Abs(filepath.Join("..", "..", "firmware"))
	if err != nil {
		panic(err)
	}
	return dir
}

// lcd runs the kit's CLI with a fixed argument vector, and returns its
// combined output; a non-zero exit is an error carrying that output.
func lcd(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lcd349", args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("lcd349 %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return out, nil
}

var (
	buildOnce sync.Once
	buildErr  error
)

// Build builds the firmware for the emulator, once per test binary.
func Build(t testing.TB) {
	t.Helper()
	buildOnce.Do(func() { _, buildErr = lcd("build", "-C", FirmwareDir()) })
	if buildErr != nil {
		t.Fatal(buildErr)
	}
}

// Options set an instance's first boot.
type Options struct {
	Clock   string // the RTC at first boot (RFC 3339); default DefaultClock
	Storage string // boot with this storage directory (seeded state); default: empty storage
	Battery bool   // on battery at first boot; default USB power
}

// Instance is one running emulator instance of the firmware. Its methods fail
// the test that started it, so call them from that test, not a subtest.
type Instance struct {
	ID     string
	Dir    string // the instance's directory (control.sock, serial.log)
	Serial string // its virtual serial port, a pty
	t      testing.TB
}

// Info is an instance as `lcd349 list --json` reports it.
type Info struct {
	ID       string `json:"id"`
	Dir      string `json:"dir"`
	Serial   string `json:"serial"`
	State    string `json:"state"`
	LastExit *struct {
		Meaning   string   `json:"meaning"`
		LastLines []string `json:"last_lines"`
	} `json:"last_exit"`
}

// List returns the running instances.
func List() ([]Info, error) {
	out, err := lcd("list", "--json")
	if err != nil {
		return nil, err
	}
	var list []Info
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("lcd349 list --json: %w", err)
	}
	return list, nil
}

var instanceSeq atomic.Int32

// Start builds the firmware if need be, starts a headless instance, waits
// until it runs and has a serial port, and stops it when t ends. It skips t
// unless PAGER_EMUTEST=1.
func Start(t testing.TB, opt Options) *Instance {
	t.Helper()
	Require(t)
	Build(t)
	// Instance ids are 1-12 lower-case letters, digits and dashes.
	in := &Instance{ID: fmt.Sprintf("pg%d-%d", os.Getpid()%100000, instanceSeq.Add(1)), t: t}
	clock := opt.Clock
	if clock == "" {
		clock = DefaultClock
	}
	args := []string{"run", "-C", FirmwareDir(), "--headless", "--detach", "--no-build", "--id", in.ID, "--clock", clock}
	if opt.Storage != "" {
		args = append(args, "--storage", opt.Storage)
	} else {
		args = append(args, "--wipe")
	}
	if opt.Battery {
		args = append(args, "--battery")
	} else {
		args = append(args, "--usb")
	}
	if _, err := lcd(args...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(in.Stop)

	deadline := time.Now().Add(startTimeout)
	for {
		info, err := in.Info()
		if err == nil && info.State == "running" && info.Serial != "" {
			in.Dir, in.Serial = info.Dir, info.Serial
			return in
		}
		if err == nil && info.LastExit != nil && info.State == "crashed" {
			t.Fatalf("instance %s crashed at boot:\n%s", in.ID, strings.Join(info.LastExit.LastLines, "\n"))
		}
		if time.Now().After(deadline) {
			t.Fatalf("instance %s didn't start within %v (state %q, err %v)", in.ID, startTimeout, info.State, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Info returns the instance as `lcd349 list --json` reports it.
func (in *Instance) Info() (Info, error) {
	list, err := List()
	if err != nil {
		return Info{}, err
	}
	for _, info := range list {
		if info.ID == in.ID {
			return info, nil
		}
	}
	return Info{}, fmt.Errorf("instance %s isn't listed", in.ID)
}

// Press presses "boot", "pwr" or "boot+pwr" for ms milliseconds.
func (in *Instance) Press(button string, ms int) {
	in.t.Helper()
	if _, err := lcd("press", in.ID, button, "--ms", strconv.Itoa(ms)); err != nil {
		in.t.Fatal(err)
	}
}

// WaitLog waits for a serial line matching the extended regexp re (from the
// last press, touch, power or state set). A wait that fails is retried once:
// the emulator has been seen to lose a log line (design §Risks), and a
// pairing's restart drops a wait in progress. Since ADR 0011 that restart
// comes just after BOOT's press returns, once provision has sent the
// prov_set, so the first wait after a pairing often spans it.
func (in *Instance) WaitLog(re string, timeout time.Duration) {
	in.t.Helper()
	in.wait("--log", re, timeout)
}

// WaitText waits for text to be visible on screen, retrying once like WaitLog.
func (in *Instance) WaitText(text string, timeout time.Duration) {
	in.t.Helper()
	in.wait("--text", text, timeout)
}

func (in *Instance) wait(flag, what string, timeout time.Duration) {
	in.t.Helper()
	args := []string{"wait", in.ID, flag, what, "--timeout", strconv.FormatInt(timeout.Milliseconds(), 10)}
	_, err := lcd(args...)
	if err != nil {
		in.t.Logf("retrying a wait once: %v", err)
		_, err = lcd(args...)
	}
	if err != nil {
		in.t.Fatal(err)
	}
}

// Shot takes a screenshot (640x172, landscape).
func (in *Instance) Shot() image.Image {
	in.t.Helper()
	path := filepath.Join(in.t.TempDir(), "shot.png")
	if _, err := lcd("shot", in.ID, "-o", path); err != nil {
		in.t.Fatal(err)
	}
	img, err := readPNG(path)
	if err != nil {
		in.t.Fatal(err)
	}
	return img
}

// MatchGolden takes a screenshot and compares it with the golden name under
// firmware/scenarios/ (Golden), outside masks.
func (in *Instance) MatchGolden(name string, masks ...image.Rectangle) {
	in.t.Helper()
	MatchGolden(in.t, in.Shot(), Golden(name), masks...)
}

// Lint fails the test if the screen breaks one of the kit's design rules.
func (in *Instance) Lint() {
	in.t.Helper()
	if _, err := lcd("lint", in.ID); err != nil {
		in.t.Fatal(err)
	}
}

// SetState sets the simulated world, such as SetState("source=battery").
func (in *Instance) SetState(kv ...string) {
	in.t.Helper()
	if _, err := lcd(append([]string{"state", in.ID, "set"}, kv...)...); err != nil {
		in.t.Fatal(err)
	}
}

// Stop stops the instance and removes its directory. Start registers it as a cleanup.
func (in *Instance) Stop() {
	if _, err := lcd("stop", in.ID); err != nil {
		in.t.Logf("stopping %s: %v", in.ID, err)
	}
}
