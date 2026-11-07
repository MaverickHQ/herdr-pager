package emutest

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// sortScenario is fakeherdr's: one agent in each state, listed in no order of
// urgency, so the pager's sort shows. The blocked one pages.
const sortScenario = `
[[agent]]
pane = "w1:p1"
kind = "claude"
name = "docs-site"
status = "idle"

[[agent]]
pane = "w1:p2"
kind = "claude"
name = "api-refactor"
status = "working"

[[agent]]
pane = "w1:p3"
kind = "claude"
name = "flaky-ci"
status = "unknown"

[[agent]]
pane = "w1:p4"
kind = "claude"
name = "release-notes"
status = "done"

[[agent]]
pane = "w1:p5"
kind = "claude"
name = "db-migration"
status = "blocked"
`

// deskPaired is a pager paired with b, connected, with the snapshot's counts
// in the band, and past the page that a blocked agent in it fires 3 s after
// the snapshot (parent 6.1), and its flash. It returns the instance.
func deskPaired(t *testing.T, b *Bridge, counts string, pages bool) *Instance {
	t.Helper()
	in := paired(t, b)
	in.WaitText(counts, 10*time.Second)
	if pages {
		in.WaitTone(0, 10*time.Second)
	}
	in.WaitIdle(1000, 10*time.Second) // the flash (3 inversions at 150 ms) is over
	return in
}

func wantRows(t *testing.T, in *Instance, want ...string) {
	t.Helper()
	if got := in.Rows(); !slices.Equal(got, want) {
		t.Fatalf("the list's rows:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestDeskListSorted: the list sorts blocked > done (unseen) > unknown >
// working > idle (parent 5.1); the band holds the counts, the clock, the link
// and the network on the left (parent 5.2) and the hint cells at the right,
// PWR then BOOT (design D2-10); each row a glyph, the name, the kind and the
// age (parent 5.3). Four rows fit: BOOT moves the selection down, and the view
// scrolls to keep it visible (parent 5.5). Goldens, clock and ages masked (19.4).
func TestDeskListSorted(t *testing.T) {
	b := StartBridge(t, sortScenario)
	in := deskPaired(t, b, "!1 *1 >1", true)
	in.WaitText("o lcd349-emu", time.Second)
	in.WaitText("OPEN", time.Second)
	in.WaitText("NEXT", time.Second)

	wantRows(t, in,
		"[sel] ! db-migration claude",
		"* release-notes claude",
		"? flaky-ci claude",
		"> api-refactor claude",
	)
	in.MatchDeskGolden("list_sorted")

	for range 4 {
		in.Press("boot", 100)
	}
	in.WaitIdle(300, 5*time.Second)
	wantRows(t, in,
		"* release-notes claude",
		"? flaky-ci claude",
		"> api-refactor claude",
		"[sel] . docs-site claude",
	)
	in.MatchDeskGolden("list_scrolled")
}
