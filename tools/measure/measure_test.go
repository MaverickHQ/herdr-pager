package measure

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The measurements N1, N2, N3 and N7 (requirements §NFR, design S2-8). Each
// pairs the board afresh (pairBoard), times both ends on the Mac, writes its
// numbers to OutDir/<n>.json for task 10.12, and fails past its bound.

// record writes v as OutDir/<name>.json.
func record(t *testing.T, name string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(OutDir(t), name+".json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("recorded %s: %s", path, data)
}

// p95 is the nearest-rank 95th percentile of xs.
func p95(xs []float64) float64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[int(math.Ceil(0.95*float64(len(s))))-1]
}

func round(xs []float64) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = math.Round(x*1000) / 1000
	}
	return out
}

// connectedBoard is pairBoard, once the bridge sees the board and the list has reached it.
func connectedBoard(t *testing.T, name, scenario string) (*Bridge, *Serial) {
	t.Helper()
	b, s, _ := pairBoard(t, name, scenario)
	b.WaitConnected("board-01", 90*time.Second)
	s.Wait(`@L \d+ line snap`, 15*time.Second)
	return b, s
}

// TestN7BootToConnected (task 10.6): 5 boots by harness reset; each ≤ 15 s
// from the reset to the first snapshot in the app core's log, the list on
// the screen (parent N7; open-items O-12). The link's time is logged beside it.
func TestN7BootToConnected(t *testing.T) {
	Require(t)
	_, s := connectedBoard(t, "n7", quietScenario)
	var secs, links []float64
	for i := range 5 {
		time.Sleep(3 * time.Second)
		reset := s.Reset()
		s.Wait(`pump: boot: paired`, 30*time.Second)
		link := s.Wait(`@L \d+ link connected`, 60*time.Second)
		snap := s.Wait(`@L \d+ line snap`, 15*time.Second)
		links = append(links, link.At.Sub(reset).Seconds())
		secs = append(secs, snap.At.Sub(reset).Seconds())
		t.Logf("boot %d: the list %.2f s after the reset (the link %.2f s)", i+1, secs[i], links[i])
	}
	worst := slices.Max(secs)
	record(t, "n7", map[string]any{"what": "N7 boot to the first snapshot, 5 boots by harness reset", "seconds": round(secs),
		"link_seconds": round(links), "max_s": round([]float64{worst})[0], "limit_s": 15, "pass": worst <= 15})
	if worst > 15 {
		t.Errorf("N7: a boot took %.2f s to show the list, over 15 s", worst)
	}
}

// flips is fakeherdr's scenario for N1 and N2: agent, working at first,
// turns blocked at first+i*every for each trial, and working again half an
// interval later.
func flips(agent string, trials int, first, every time.Duration) string {
	var b strings.Builder
	b.WriteString(agent)
	for i := range trials {
		at := first + time.Duration(i)*every
		fmt.Fprintf(&b, "\n[[change]]\nat = %q\npane = \"w1:p1\"\nstatus = \"blocked\"\n", at.String())
		fmt.Fprintf(&b, "\n[[change]]\nat = %q\npane = \"w1:p1\"\nstatus = \"working\"\n", (at + every/2).String())
	}
	return b.String()
}

// TestN1PageLatency (task 10.7): 20 blocked flips by fakeherdr; p95 ≤ 6 s
// from the flip (fakeherdr's start, on the Mac, plus the flip's time in the
// scenario) to the board's page line as it reaches the Mac.
func TestN1PageLatency(t *testing.T) {
	Require(t)
	b, s := connectedBoard(t, "n1", quietScenario)
	const trials = 20
	first, every := 15*time.Second, 12*time.Second
	b.SetScenario(flips(quietScenario, trials, first, every))
	start := b.RestartHerdr()
	var secs []float64
	for i := range trials {
		flip := start.Add(first + time.Duration(i)*every)
		l, ok := s.WaitFor(`@L \d+ page blocked`, time.Until(flip)+every/2)
		if !ok {
			t.Fatalf("N1 trial %d: no page within %v of the flip", i+1, every/2)
		}
		secs = append(secs, l.At.Sub(flip).Seconds())
		t.Logf("trial %d: paged %.3f s after the flip", i+1, secs[i])
	}
	p := p95(secs)
	record(t, "n1", map[string]any{"what": "N1 page latency, 20 blocked flips", "seconds": round(secs),
		"p95_s": round([]float64{p})[0], "limit_s": 6, "pass": p <= 6})
	if p > 6 {
		t.Errorf("N1: p95 %.3f s, over 6 s", p)
	}
}

// sendKeys is the Mac time of each agent.send_keys in fakeherdr's request log.
func (b *Bridge) sendKeys() []time.Time {
	var out []time.Time
	for _, line := range strings.Split(b.HerdrLog.String(), "\n") {
		var r struct {
			Time   time.Time `json:"time"`
			Method string    `json:"method"`
		}
		if json.Unmarshal([]byte(line), &r) == nil && r.Method == "agent.send_keys" {
			out = append(out, r.Time)
		}
	}
	return out
}

// promptAgent is fakeherdr's agent on Claude Code's shell permission prompt
// (phase 0's capture shell-0s): its card offers approve, from the keymap.
const promptAgent = `
[[agent]]
pane = "w1:p1"
kind = "claude"
name = "api-refactor"
title = "Bash command"
detection = "prompt.txt"
status = "working"
`

// TestN2VerdictLatency (task 10.8): 20 approvals by @T holds; p95 ≤ 1.5 s
// from the hold's completion (its write on the Mac plus the 1 s hold) to
// fakeherdr's send_keys. Each trial: the agent pages, PWR opens its card,
// and after the card's guard BOOT is held 1 s.
func TestN2VerdictLatency(t *testing.T) {
	Require(t)
	b, s := connectedBoard(t, "n2", quietScenario)
	prompt, err := os.ReadFile(filepath.Join(RepoDir(), "bridge", "testdata", "herdr", "claude", "detection", "shell-0s.txt"))
	if err != nil {
		t.Fatal(err)
	}
	b.WriteFile("prompt.txt", string(prompt))
	const trials = 20
	first, every := 15*time.Second, 16*time.Second
	b.SetScenario(flips(promptAgent, trials, first, every))
	b.RestartHerdr()
	var secs []float64
	for i := range trials {
		s.Wait(`@L \d+ page blocked`, first+every)
		time.Sleep(time.Second)
		s.Send("@T press PWR 100") // opens the selected (blocked) agent's card
		// The card's guard (500 ms, router.c) runs from its text's arrival, not
		// the press: a slow show (a Wi-Fi hiccup once took 708 ms) put a 1 s
		// sleep's hold inside it, and the pager rightly ignored the hold.
		s.Wait(`@L \d+ line card`, 10*time.Second)
		time.Sleep(700 * time.Millisecond)
		before := len(b.sendKeys())
		done := s.Send("@T press BOOT 1000").Add(time.Second)
		var sent time.Time
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if keys := b.sendKeys(); len(keys) > before {
				sent = keys[before]
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("N2 trial %d: no send_keys within 10 s of the hold", i+1)
			}
		}
		secs = append(secs, sent.Sub(done).Seconds())
		t.Logf("trial %d: send_keys %.3f s after the hold's completion", i+1, secs[i])
	}
	p := p95(secs)
	record(t, "n2", map[string]any{"what": "N2 verdict latency, 20 @T holds", "seconds": round(secs),
		"p95_s": round([]float64{p})[0], "limit_s": 1.5, "pass": p <= 1.5})
	if p > 1.5 {
		t.Errorf("N2: p95 %.3f s, over 1.5 s", p)
	}
}

// TestN3Recovery (task 10.9): 5 bridge restarts, then 5 fakeherdr restarts.
// A bridge restart: the link back ≤ 35 s after serve listens again, and the
// list ≤ 2 s after the link. A fakeherdr restart: the list ≤ 2 s after its
// socket is back (the bridge polls each second).
func TestN3Recovery(t *testing.T) {
	Require(t)
	b, s := connectedBoard(t, "n3", quietScenario)
	var reconnect, bridgeSnap, herdrSnap []float64
	for i := range 5 {
		b.StopServe()
		s.Wait(`@L \d+ link (down|connecting)`, 30*time.Second)
		time.Sleep(2 * time.Second)
		b.StartServe()
		back := time.Now()
		c := s.Wait(`@L \d+ link connected`, 60*time.Second)
		snap := s.Wait(`@L \d+ line snap`, 10*time.Second)
		reconnect = append(reconnect, c.At.Sub(back).Seconds())
		bridgeSnap = append(bridgeSnap, snap.At.Sub(c.At).Seconds())
		t.Logf("bridge restart %d: link back %.3f s after serve listened; list %.3f s after", i+1, reconnect[i], bridgeSnap[i])
	}
	for i := range 5 {
		b.StopHerdr()
		s.Wait(`@L \d+ line snap`, 10*time.Second) // HERDR OFFLINE
		time.Sleep(2 * time.Second)
		b.StartHerdr()
		back := time.Now()
		snap := s.Wait(`@L \d+ line snap`, 35*time.Second)
		herdrSnap = append(herdrSnap, snap.At.Sub(back).Seconds())
		t.Logf("fakeherdr restart %d: list %.3f s after its socket was back", i+1, herdrSnap[i])
	}
	rc, bs, hs := slices.Max(reconnect), slices.Max(bridgeSnap), slices.Max(herdrSnap)
	pass := rc <= 35 && bs <= 2 && hs <= 2
	record(t, "n3", map[string]any{"what": "N3 recovery, 5 bridge restarts and 5 fakeherdr restarts",
		"bridge_reconnect_s": round(reconnect), "bridge_snapshot_s": round(bridgeSnap), "herdr_snapshot_s": round(herdrSnap),
		"limits": "reconnect ≤ 35 s; snapshot ≤ 2 s", "pass": pass})
	if !pass {
		t.Errorf("N3: worst reconnect %.3f s (≤ 35), snapshot after the bridge %.3f s, after fakeherdr %.3f s (≤ 2)", rc, bs, hs)
	}
}
