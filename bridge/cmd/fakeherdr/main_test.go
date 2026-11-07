package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
)

// runMainEnv makes the test binary run fakeherdr itself instead of the tests.
const runMainEnv = "FAKEHERDR_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A fakeProc is fakeherdr running as a process, and a herdr client of it that
// records each method it calls.
type fakeProc struct {
	cmd    *exec.Cmd
	client *herdr.Client
	lines  chan string // stdout, line by line
	calls  []string    // the methods called, in order
	stderr lockedBuffer
}

// A lockedBuffer is a process's stderr as it comes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startFake runs fakeherdr with args after --socket, on a socket in a short
// temp folder, and returns once the socket is there; it's killed when the
// test ends.
func startFake(t *testing.T, args ...string) *fakeProc {
	t.Helper()
	dir, err := os.MkdirTemp("", "hp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "herdr.sock")
	f := &fakeProc{client: &herdr.Client{SocketPath: socket}, lines: make(chan string, 1024)}
	f.cmd = exec.Command(os.Args[0], append([]string{"--socket", socket}, args...)...)
	f.cmd.Env = append(os.Environ(), runMainEnv+"=1")
	f.cmd.Stderr = &f.stderr
	stdout, err := f.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		for s := bufio.NewScanner(stdout); s.Scan(); {
			f.lines <- s.Text()
		}
	}()
	t.Cleanup(func() {
		f.cmd.Process.Kill()
		f.cmd.Wait()
	})
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(socket); err == nil {
			return f
		}
		if time.Now().After(deadline) {
			t.Fatalf("fakeherdr made no socket within 5 s; stderr: %s", f.stderr.String())
		}
	}
}

// list calls agent.list and returns the agents by pane.
func (f *fakeProc) list(t *testing.T) map[string]herdr.AgentInfo {
	t.Helper()
	f.calls = append(f.calls, herdr.MethodList)
	agents, err := f.client.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("agent.list: %v", err)
	}
	byPane := map[string]herdr.AgentInfo{}
	for _, a := range agents {
		byPane[a.PaneID] = a
	}
	return byPane
}

// listUntil calls agent.list until ok says its agents are as wanted, for up
// to 5 s, and returns the last.
func (f *fakeProc) listUntil(t *testing.T, ok func(map[string]herdr.AgentInfo) bool) map[string]herdr.AgentInfo {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		agents := f.list(t)
		if ok(agents) || time.Now().After(deadline) {
			return agents
		}
	}
}

// TestFakeHerdrServesAScenario checks phase 2 R5 (design S2-7, S2-8):
// fakeherdr, as a program, serves herdr's socket from a scenario file:
// agent.list answers with the scenario's agents, which change at their times;
// agent.read answers each agent's detection text from its file (relative to
// the scenario, or absolute); agent.get, agent.send_keys and agent.focus
// answer in herdr's shapes; and every request is logged on stdout as one JSON
// line. With --churn, its agents change status at that interval; without
// --socket it's a usage error.
func TestFakeHerdrServesAScenario(t *testing.T) {
	dir := t.TempDir()
	capture, err := filepath.Abs("../../testdata/herdr/claude/detection/shell-0s.txt")
	if err != nil {
		t.Fatal(err)
	}
	captured, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte("Do you want to proceed?\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scenario := filepath.Join(dir, "scenario.toml")
	if err := os.WriteFile(scenario, []byte(fmt.Sprintf(`# Two agents; one blocks at 300 ms, the other goes at 600 ms.
[[agent]]
pane      = "w1:p1"
kind      = "claude"
name      = "recon"
title     = "Bash command"
status    = "working"
detection = "prompt.txt"

[[agent]]
pane      = "w1:p2"
kind      = "claude"
status    = "idle"
detection = %q

[[change]]
at     = "300ms"
pane   = "w1:p1"
status = "blocked"

[[change]]
at   = "600ms"
pane = "w1:p2"
gone = true
`, capture)), 0o600); err != nil {
		t.Fatal(err)
	}

	f := startFake(t, "--scenario", scenario)
	ctx := context.Background()
	agents := f.list(t)
	p1, p2 := agents["w1:p1"], agents["w1:p2"]
	switch {
	case len(agents) != 2:
		t.Fatalf("at the start: %d agents; want 2: %+v", len(agents), agents)
	case p1.Status != "working" || p1.Name == nil || *p1.Name != "recon" || p1.Agent == nil || *p1.Agent != "claude" ||
		p1.TitleStripped == nil || *p1.TitleStripped != "Bash command" || p1.TerminalID == "" || p1.StateChangeSeq == 0:
		t.Errorf("at the start, w1:p1: %+v; want recon, claude, Bash command, working, a terminal id and a seq", p1)
	case p2.Status != "idle" || p2.Name != nil:
		t.Errorf("at the start, w1:p2: %+v; want idle, with no name", p2)
	}
	for pane, want := range map[string]string{"w1:p1": "Do you want to proceed?\n", "w1:p2": string(captured)} {
		f.calls = append(f.calls, herdr.MethodRead)
		if text, err := f.client.ReadDetection(ctx, pane); err != nil || text != want {
			t.Errorf("agent.read %s: %d bytes, %v; want the %d bytes of its detection file", pane, len(text), err, len(want))
		}
	}

	agents = f.listUntil(t, func(a map[string]herdr.AgentInfo) bool { return a["w1:p1"].Status == "blocked" })
	if a := agents["w1:p1"]; a.Status != "blocked" || a.StateChangeSeq <= p1.StateChangeSeq {
		t.Errorf("after 300 ms, w1:p1: %s, seq %d; want blocked, with a seq after %d", a.Status, a.StateChangeSeq, p1.StateChangeSeq)
	}
	if agents = f.listUntil(t, func(a map[string]herdr.AgentInfo) bool { return len(a) == 1 }); len(agents) != 1 {
		t.Errorf("after 600 ms: %d agents; want w1:p2 gone", len(agents))
	}

	f.calls = append(f.calls, herdr.MethodGet, herdr.MethodSendKeys, herdr.MethodFocus, herdr.MethodGet)
	if a, err := f.client.GetAgent(ctx, "w1:p1"); err != nil || a.Status != "blocked" {
		t.Errorf("agent.get w1:p1: %+v, %v; want it blocked", a, err)
	}
	if err := f.client.SendKeys(ctx, "w1:p1", []string{"1"}); err != nil {
		t.Errorf("agent.send_keys w1:p1 [1]: %v; want ok", err)
	}
	if err := f.client.Focus(ctx, "w1:p1"); err != nil {
		t.Errorf("agent.focus w1:p1: %v; want ok", err)
	}
	if _, err := f.client.GetAgent(ctx, "w1:p2"); herdr.Code(err) != "agent_not_found" {
		t.Errorf("agent.get of the agent that went: %v; want agent_not_found", err)
	}

	var logged []string
	for len(logged) < len(f.calls) {
		select {
		case line := <-f.lines:
			var req struct {
				Time   time.Time       `json:"time"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal([]byte(line), &req); err != nil || req.Time.IsZero() || len(req.Params) == 0 {
				t.Fatalf("stdout line %q (%v); want one JSON request with its time, method and params", line, err)
			}
			logged = append(logged, req.Method)
			if req.Method == herdr.MethodSendKeys && string(req.Params) != `{"target":"w1:p1","keys":["1"]}` {
				t.Errorf("logged send_keys params %s; want the keys sent", req.Params)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("logged %d requests, %q; want %d", len(logged), logged, len(f.calls))
		}
	}
	if !slices.Equal(logged, f.calls) {
		t.Errorf("logged %q; want each request in order: %q", logged, f.calls)
	}

	t.Run("churn", func(t *testing.T) {
		f := startFake(t, "--churn", "100ms", "--agents", "3")
		first := f.list(t)
		if len(first) != 3 {
			t.Fatalf("--agents 3: %d agents", len(first))
		}
		later := f.listUntil(t, func(a map[string]herdr.AgentInfo) bool {
			for pane, x := range first {
				if a[pane].Status == x.Status || a[pane].StateChangeSeq <= x.StateChangeSeq {
					return false
				}
			}
			return true
		})
		for pane, x := range first {
			if y := later[pane]; y.Status == x.Status || y.StateChangeSeq <= x.StateChangeSeq {
				t.Errorf("--churn 100ms: %s stayed %s (seq %d → %d); want every agent changed, with a later seq", pane, x.Status, x.StateChangeSeq, y.StateChangeSeq)
			}
		}
	})

	t.Run("usage", func(t *testing.T) {
		cmd := exec.Command(os.Args[0], "--scenario", scenario)
		cmd.Env = append(os.Environ(), runMainEnv+"=1")
		var exit *exec.ExitError
		if err := cmd.Run(); !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Errorf("without --socket: %v; want exit 2", err)
		}
	})
}
