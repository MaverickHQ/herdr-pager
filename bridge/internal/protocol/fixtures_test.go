package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureDir is the protocol's contract (phase 2 task 1.1).
const fixtureDir = "../../testdata/protocol"

// A fixtureRow is one row of the fixtures' README index.
type fixtureRow struct {
	file  string // relative to fixtureDir
	t     string // the line's "t", or "" where it has none
	field string // for an invalid line, the key it must be rejected at
}

// TestProtocolFixturesParse checks the contract both ways: every valid line
// parses as its own type, every invalid one is rejected at the key its README
// row names, every message type has both, and the boundary lines sit exactly
// at each direction's limit.
func TestProtocolFixturesParse(t *testing.T) {
	rows := readIndex(t)
	checkIndexCoversFiles(t, rows)

	directions := []struct {
		dir   string
		parse func([]byte) (Message, error)
		limit int
		types []string
	}{
		{"to-bridge", ParseDevice, MaxDeviceLine,
			[]string{"hello", "ping", "list", "show", "approve", "deny", "interrupt", "jump"}},
		{"to-device", ParseBridge, MaxBridgeLine,
			[]string{"hello", "snap", "card", "result", "pong"}},
	}
	for _, d := range directions {
		t.Run(d.dir, func(t *testing.T) {
			valid, invalid := map[string]bool{}, map[string]bool{}
			longestValid, overLimit := 0, false
			for _, r := range rows {
				if !strings.HasPrefix(r.file, d.dir+"/") {
					continue
				}
				line := readLine(t, r.file)
				if got := typeOf(line); got != r.t {
					t.Errorf("%s: README says t is %q, the line's is %q", r.file, r.t, got)
				}
				msg, err := d.parse(line)
				if strings.Contains(r.file, "/valid/") {
					valid[r.t] = true
					longestValid = max(longestValid, len(line))
					switch {
					case err != nil:
						t.Errorf("%s: rejected a valid line: %v", r.file, err)
					case msg == nil:
						t.Errorf("%s: no error, but no message either", r.file)
					case msg.Type() != r.t:
						t.Errorf("%s: parsed as %q, want %q", r.file, msg.Type(), r.t)
					}
					continue
				}
				invalid[r.t] = true
				overLimit = overLimit || len(line) == d.limit+1
				var perr *Error
				switch {
				case err == nil:
					t.Errorf("%s: accepted an invalid line (want it rejected at %q)", r.file, r.field)
				case !errors.As(err, &perr):
					t.Errorf("%s: error %v is not a *protocol.Error", r.file, err)
				case perr.Field != r.field:
					t.Errorf("%s: rejected at %q (%v), want %q", r.file, perr.Field, err, r.field)
				}
				if err != nil && msg != nil {
					t.Errorf("%s: rejected, yet returned a message", r.file)
				}
			}
			for _, typ := range d.types {
				if !valid[typ] {
					t.Errorf("no valid example of %q", typ)
				}
				if !invalid[typ] {
					t.Errorf("no invalid example of %q", typ)
				}
			}
			if longestValid != d.limit {
				t.Errorf("the longest valid line is %d bytes, want one of exactly %d", longestValid, d.limit)
			}
			if !overLimit {
				t.Errorf("no invalid line of exactly %d bytes", d.limit+1)
			}
		})
	}
}

// readIndex reads the fixture rows from the README's tables.
func readIndex(t *testing.T) []fixtureRow {
	t.Helper()
	f, err := os.Open(filepath.Join(fixtureDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []fixtureRow
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if !strings.HasPrefix(sc.Text(), "| `to-") {
			continue
		}
		cells := strings.Split(sc.Text(), "|")
		cell := func(i int) string {
			s := strings.Trim(strings.TrimSpace(cells[i]), "`")
			if s == "—" {
				return ""
			}
			return s
		}
		r := fixtureRow{file: cell(1), t: cell(2)}
		if strings.Contains(r.file, "/invalid/") {
			r.field = cell(3)
		}
		rows = append(rows, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

// checkIndexCoversFiles checks that the README lists every fixture once,
// and lists nothing that isn't there.
func checkIndexCoversFiles(t *testing.T, rows []fixtureRow) {
	t.Helper()
	listed := map[string]bool{}
	for _, r := range rows {
		if listed[r.file] {
			t.Errorf("README lists %s twice", r.file)
		}
		listed[r.file] = true
	}
	err := filepath.WalkDir(fixtureDir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(fixtureDir, path)
		rel = filepath.ToSlash(rel)
		if rel == "README.md" {
			return nil
		}
		if !listed[rel] {
			t.Errorf("README doesn't list %s", rel)
		}
		delete(listed, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for file := range listed {
		t.Errorf("README lists %s, which doesn't exist", file)
	}
}

// readLine returns a fixture's one line without its newline.
func readLine(t *testing.T, file string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, file))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) || bytes.Count(data, []byte("\n")) != 1 {
		t.Fatalf("%s: not exactly one line ending in a newline", file)
	}
	return data[:len(data)-1]
}

// typeOf returns a line's "t" when the line is a JSON object with a string
// "t", else "".
func typeOf(line []byte) string {
	var m map[string]any
	if json.Unmarshal(line, &m) != nil {
		return ""
	}
	s, _ := m["t"].(string)
	return s
}

// TestEncodeGivesValidLinesBack checks that what the bridge sends is what the
// fixtures show: every valid bridge line, parsed and encoded again, is the
// same bytes. card-escapes holds the escapes Go's encoder writes (open-items
// O-40). The boundary line is padded and pong-extra-field carries a key the
// bridge never sends, so neither comes back as written.
func TestEncodeGivesValidLinesBack(t *testing.T) {
	skip := map[string]bool{
		"to-device/valid/snap-8192-bytes.ndjson":  true,
		"to-device/valid/pong-extra-field.ndjson": true,
	}
	n := 0
	for _, r := range readIndex(t) {
		if !strings.HasPrefix(r.file, "to-device/valid/") || skip[r.file] {
			continue
		}
		line := readLine(t, r.file)
		msg, err := ParseBridge(line)
		if err != nil {
			t.Errorf("%s: %v", r.file, err)
			continue
		}
		got, err := Encode(msg)
		if err != nil {
			t.Errorf("%s: Encode: %v", r.file, err)
			continue
		}
		if !bytes.Equal(got, line) {
			t.Errorf("%s: encoded again as\n%s\nnot\n%s", r.file, got, line)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no valid bridge line")
	}
}
