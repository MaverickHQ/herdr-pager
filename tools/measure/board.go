// Package measure holds lane board's board checks and measurements (phase 2
// design D2-4, S2-8): Go tests that build and flash the firmware, talk to the
// board over its USB Serial/JTAG port, and timestamp every line on the Mac.
// They run only with PAGER_MEASURE=1 and the board on USB:
//
//	cd tools/measure && PAGER_MEASURE=1 go test -timeout 60m -run TestBoardTestHooks -v
//
// The kit's boardtest runs Unity apps and answers only their requests; these
// checks drive the pager itself: @T lines in, the app core's log out, and the
// bridge and fakeherdr on the Mac (bridge.go).
//
// Builds go to out-of-tree directories under OutDir, one per variant, so the
// firmware's own build/ and sdkconfig are never touched. The board lock
// (~/.lcd349-board.lock) is taken as the kit's boardtest takes it, unless
// LCD349_BOARD_LOCK_HELD=1 says the caller holds it (the runner does).
package measure

import (
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Require skips t unless PAGER_MEASURE=1.
func Require(t testing.TB) {
	t.Helper()
	if os.Getenv("PAGER_MEASURE") != "1" {
		t.Skip("a board check: set PAGER_MEASURE=1, with the board on USB")
	}
}

// RepoDir is the repository's root.
func RepoDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// FirmwareDir is the repository's firmware/ directory.
func FirmwareDir() string { return filepath.Join(RepoDir(), "firmware") }

// OutDir is where builds, logs and results go: PAGER_MEASURE_OUT, or
// ~/.run-phase/out/<the repository's folder name>-2 (outside the
// repository; phase 2's runs used it).
func OutDir(t testing.TB) string {
	t.Helper()
	dir := os.Getenv("PAGER_MEASURE_OUT")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		dir = filepath.Join(home, ".run-phase", "out", filepath.Base(RepoDir())+"-2")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Port is PAGER_BOARD_PORT, or the only /dev/cu.usbmodem*.
func Port(t testing.TB) string {
	t.Helper()
	if p := os.Getenv("PAGER_BOARD_PORT"); p != "" {
		return p
	}
	ports, _ := filepath.Glob("/dev/cu.usbmodem*")
	if len(ports) != 1 {
		t.Fatalf("want the board on USB as the only /dev/cu.usbmodem*, found %q (or set PAGER_BOARD_PORT)", ports)
	}
	return ports[0]
}

var lockOnce sync.Once

// Lock holds the board lock until the test binary exits, unless the caller
// holds it (LCD349_BOARD_LOCK_HELD=1).
func Lock(t testing.TB) {
	t.Helper()
	if os.Getenv("LCD349_BOARD_LOCK_HELD") == "1" {
		return
	}
	var err error
	lockOnce.Do(func() {
		home, _ := os.UserHomeDir()
		var f *os.File
		f, err = os.OpenFile(filepath.Join(home, ".lcd349-board.lock"), os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return
		}
		for {
			if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
				break
			}
			if !errors.Is(err, syscall.EWOULDBLOCK) {
				return
			}
			time.Sleep(2 * time.Second)
		}
		f.Truncate(0)
		f.WriteAt([]byte(fmt.Sprintf("tools/measure pid %d, since %s\n", os.Getpid(), time.Now().Format(time.RFC3339))), 0)
		// f stays open, so the lock is held until the process exits.
	})
	if err != nil {
		t.Fatalf("the board lock: %v", err)
	}
}

// A Variant is a firmware build: release, or test (with CONFIG_PAGER_TEST_HOOKS).
type Variant string

const (
	Release Variant = "release"
	Test    Variant = "test"
)

// Flash builds the variant into OutDir/build-<variant> and flashes it to
// port, with idf.py from PATH (the runner loads ESP-IDF's environment). It
// returns the ELF's path. idf.py's output goes to OutDir/build-<variant>.log.
// The variant's sdkconfig is made afresh from the firmware's defaults each
// time, so a check never runs a configuration the repository doesn't have.
func Flash(t testing.TB, v Variant, port string) string {
	t.Helper()
	return flash(t, v, port, "build", "flash")
}

// FlashErased is Flash after erasing the whole flash: storage too, so the
// board boots unpaired and makes a new device key.
func FlashErased(t testing.TB, v Variant, port string) string {
	t.Helper()
	return flash(t, v, port, "build", "erase-flash", "flash")
}

// Build builds the variant as Flash does, without the board: no port, no
// board lock. It returns the ELF's path.
func Build(t testing.TB, v Variant) string {
	t.Helper()
	return flash(t, v, "", "build")
}

// flash runs idf.py's steps for the variant; with no port it doesn't touch
// the board.
func flash(t testing.TB, v Variant, port string, steps ...string) string {
	t.Helper()
	if port != "" {
		Lock(t)
	}
	out := OutDir(t)
	bdir := filepath.Join(out, "build-"+string(v))
	if err := os.Remove(filepath.Join(bdir, "sdkconfig")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	defaults := "sdkconfig.defaults"
	if v == Test {
		defaults += ";sdkconfig.test"
	}
	args := []string{"-C", FirmwareDir(), "-B", bdir,
		"-D", "SDKCONFIG=" + filepath.Join(bdir, "sdkconfig"),
		"-D", "SDKCONFIG_DEFAULTS=" + defaults,
		"-D", "IDF_TARGET=esp32s3"}
	if port != "" {
		args = append(args, "-p", port)
	}
	args = append(args, steps...)
	logPath := bdir + ".log"
	logf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()
	cmd := exec.Command("idf.py", args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	start := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("idf.py %v (%s): %v; see %s", steps, v, err, logPath)
	}
	t.Logf("idf.py %v (%s) took %.0f s", steps, v, time.Since(start).Seconds())
	return filepath.Join(bdir, "herdr-pager.elf")
}

// HasSymbol reports whether the ELF at path defines the symbol name.
func HasSymbol(t testing.TB, path, name string) bool {
	t.Helper()
	f, err := elf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range syms {
		if s.Name == name && s.Section != elf.SHN_UNDEF {
			return true
		}
	}
	return false
}
