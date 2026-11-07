package measure

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The app task's first line after boot: the console task starts with it.
const bootDone = `pump: boot: (paired|unpaired)`

// A restart: the ROM's first line.
var romBanner = regexp.MustCompile(`^ESP-ROM:|^rst:0x`)

var logButton = regexp.MustCompile(`@L (\d+) button (BOOT|PWR) (down|up)`)

// edgeMs is the core's time of a button edge in the app core's log.
func edgeMs(t *testing.T, l Line) int64 {
	t.Helper()
	m := logButton.FindStringSubmatch(l.Text)
	if m == nil {
		t.Fatalf("not a button edge: %q", l.Text)
	}
	ms, _ := strconv.ParseInt(m[1], 10, 64)
	return ms
}

// projectVer is the firmware's PROJECT_VER in the builds these checks make:
// bridge/internal/version/VERSION's number with "-dev", since they never pass
// the release's -DPAGER_VERSION (firmware/CMakeLists.txt, phase 5 task 1.3).
func projectVer(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(RepoDir(), "bridge", "internal", "version", "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	number := strings.TrimSpace(string(data))
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(number) {
		t.Fatalf("bridge/internal/version/VERSION says %q; want a version's number", number)
	}
	return number + "-dev"
}

// fwVersion is the version a variant reports in hello, prov_hello, device
// info and its boot line: a test build's ends "+test", so a board left with
// press injection says so (open-items O-42).
func fwVersion(t *testing.T, v Variant) string {
	if v == Test {
		return projectVer(t) + "+test"
	}
	return projectVer(t)
}

// TestBuildsTellTheirVariant builds both variants, without the board, and
// reads their ELFs (open-items O-42, O-43). The release build holds its plain
// version and none of the hooks' text; the test build holds both, so each
// check is seen to be able to fail.
func TestBuildsTellTheirVariant(t *testing.T) {
	Require(t)
	elves := map[Variant][]byte{}
	for _, v := range []Variant{Release, Test} {
		data, err := os.ReadFile(Build(t, v))
		if err != nil {
			t.Fatal(err)
		}
		elves[v] = data
	}
	hook := []byte("@T press ")
	if !bytes.Contains(elves[Test], hook) {
		t.Errorf("the test build has no %q: the check below proves nothing", hook)
	}
	if bytes.Contains(elves[Release], hook) {
		t.Errorf("the release build holds the hooks' %q", hook)
	}
	for _, v := range []Variant{Release, Test} {
		if want := append([]byte(fwVersion(t, v)), 0); !bytes.Contains(elves[v], want) {
			t.Errorf("the %s build doesn't report version %q", v, want[:len(want)-1])
		}
	}
	if bytes.Contains(elves[Release], []byte("+test")) {
		t.Error(`the release build's text holds "+test"`)
	}
}

// TestBoardTestHooks is the board check board_test_hooks (task 10.4, design
// D2-4). A release build has no hooks compiled in and ignores @T lines: no
// log, no restart. In a test build, "@T press BOOT 1000" on the serial port
// becomes a 1 s BOOT hold in the app core's timestamped log: a BOOT down
// edge, then a BOOT up edge 1000 ms later by the core's clock. It leaves the
// test build on the board.
func TestBoardTestHooks(t *testing.T) {
	Require(t)
	port := Port(t)

	t.Run("release ignores @T", func(t *testing.T) {
		elf := Flash(t, Release, port)
		if HasSymbol(t, elf, "testhooks_line") {
			t.Fatal("the release build defines testhooks_line: the hooks aren't compiled out")
		}
		s := Open(t, port, "test_hooks_release")
		s.Wait(`herdr-pager `+regexp.QuoteMeta(fwVersion(t, Release))+`: lcd349_init`, 30*time.Second)
		s.Wait(bootDone, 30*time.Second)
		s.Send("@T press BOOT 1000")
		for _, l := range s.Collect(3 * time.Second) {
			if logButton.MatchString(l.Text) || romBanner.MatchString(l.Text) {
				t.Fatalf("the release build reacted to @T: %q", l.Text)
			}
		}
	})

	t.Run("test build injects a hold", func(t *testing.T) {
		elf := Flash(t, Test, port)
		if !HasSymbol(t, elf, "testhooks_line") {
			t.Fatal("the test build doesn't define testhooks_line")
		}
		s := Open(t, port, "test_hooks_test")
		s.Wait(`herdr-pager `+regexp.QuoteMeta(fwVersion(t, Test))+`: lcd349_init`, 30*time.Second)
		s.Wait(bootDone, 30*time.Second)
		s.Send("@T press BOOT 1000")
		down := s.Wait(`@L \d+ button BOOT down`, 5*time.Second)
		up := s.Wait(`@L \d+ button BOOT up`, 5*time.Second)
		held := edgeMs(t, up) - edgeMs(t, down)
		if held < 1000 || held > 1050 {
			t.Fatalf("the core saw BOOT held %d ms, want 1000 (to 1050)", held)
		}
		t.Logf("BOOT held %d ms by the core's clock; the edges reached the Mac %v apart", held, up.At.Sub(down.At).Round(time.Millisecond))
	})
}
