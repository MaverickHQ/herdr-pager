package status

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// configDir holds the bridge's file fixtures (testdata/config/README.md).
const configDir = "../../testdata/config"

// TestStatusWriteReadAndStaleness checks the serve ↔ CLI contract (design
// D2-12): a write is 0600 and atomic, so a concurrent read sees the old file
// or the new one; a read returns what serve wrote; a missing file, or one
// older than 5 s, reads as not running.
func TestStatusWriteReadAndStaleness(t *testing.T) {
	written := time.Date(2026, 10, 23, 18, 10, 0, 0, time.UTC)
	fixture := Status{
		Written: written, Listening: "0.0.0.0:2277", Herdr: "ok", Socket: "/tmp/hp-test/herdr.sock",
		Devices: []string{"pager-01", "pager-02"}, Connected: []string{"pager-01"},
	}

	t.Run("read the fixture, fresh and stale", func(t *testing.T) {
		for _, c := range []struct {
			age     time.Duration
			running bool
		}{
			{0, true}, {2 * time.Second, true}, {5 * time.Second, true},
			{5*time.Second + time.Millisecond, false}, {time.Hour, false},
		} {
			s, running, err := Read(configDir, written.Add(c.age))
			want := Status{}
			if c.running {
				want = fixture
			}
			if err != nil || running != c.running || !reflect.DeepEqual(s, want) {
				t.Errorf("%v old: %+v, %v, %v; want %+v, %v", c.age, s, running, err, want, c.running)
			}
		}
	})

	t.Run("missing", func(t *testing.T) {
		s, running, err := Read(t.TempDir(), written)
		if err != nil || running || !reflect.DeepEqual(s, Status{}) {
			t.Errorf("no status.json: %+v, %v, %v; want not running and no error", s, running, err)
		}
	})

	t.Run("written 0600 and read back", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, File)
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		s := fixture
		s.Connected = []string{} // nobody: written as [], never null
		if err := Write(dir, s, written); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("status.json's mode: %v, %v; want 0600", info.Mode().Perm(), err)
		}
		got, running, err := Read(dir, written.Add(time.Second))
		if err != nil || !running || !reflect.DeepEqual(got, s) {
			t.Errorf("read back: %+v, %v, %v; want %+v, running", got, running, err, s)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 {
			t.Errorf("the folder holds %d files after a write, want status.json alone", len(entries))
		}
	})

	t.Run("a concurrent read sees the old or the new file", func(t *testing.T) {
		dir := t.TempDir()
		a, b := fixture, fixture
		a.Connected, b.Connected = []string{"pager-01"}, []string{"pager-02"}
		if err := Write(dir, a, written); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 300 {
				s := a
				if i%2 == 0 {
					s = b
				}
				if err := Write(dir, s, written); err != nil {
					t.Error(err)
					return
				}
			}
		}()
		for range 1000 {
			s, running, err := Read(dir, written)
			if err != nil || !running || !(reflect.DeepEqual(s, a) || reflect.DeepEqual(s, b)) {
				t.Fatalf("a read during writes: %+v, %v, %v; want one of the two whole files", s, running, err)
			}
		}
		wg.Wait()
	})
}
