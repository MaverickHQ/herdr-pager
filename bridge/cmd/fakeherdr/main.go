// Command fakeherdr is a fake herdr, for the emulator harness and the board's
// measurements (phase 2 R5, design S2-7, S2-8; lane e2e, task 4.28). It
// serves herdr's socket with internal/herdrtest, which answers the five
// methods the bridge may call in herdr 0.9.3's shapes, with agents from a
// scenario file that change over time, or that churn at a fixed interval. It
// logs every request it gets on stdout, one JSON line each: its time, method
// and params.
//
// Usage:
//
//	fakeherdr --socket <path> [--scenario <file>] [--churn <interval>] [--agents <n>]
//
// A scenario (TOML) lists herdr's agents at the start, then changes to them
// at times after the start:
//
//	[[agent]]
//	pane      = "w1:p1"        # pane_id: required
//	kind      = "claude"       # herdr's agent kind; none if left out
//	name      = "api-refactor" # herdr's name; none if left out
//	title     = "Bash command" # terminal_title_stripped; none if left out
//	status    = "working"      # idle, working, blocked, done or unknown
//	detection = "prompt.txt"   # a file holding agent.read's text, relative to the scenario
//
//	[[change]]
//	at        = "5s"           # after the start
//	pane      = "w1:p1"        # the agent it changes, or adds
//	status    = "blocked"      # and any other field of [[agent]] it sets
//	gone      = true           # instead: the agent goes
//
// state_change_seq is one counter for all agents, as herdr's is: each agent
// takes the next value at the start and at each change of its status; a
// change to done also sets completion_seq to it.
//
// With --churn, every interval each agent's status moves on one step:
// working, blocked, done, idle, then working again. Its agents are the
// scenario's, or else --agents of them (3 by default), of kind claude, in
// panes w1:p1, w1:p2…, each starting at the next step.
//
// It serves only the socket it's given: --socket is required. It stops on
// SIGINT or SIGTERM, removing the socket.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdrtest"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/protocol"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// statuses are herdr's agent statuses, and churn's cycle is the first four.
var statuses = []string{"working", "blocked", "done", "idle", "unknown"}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("fakeherdr", flag.ContinueOnError)
	flags.SetOutput(stderr)
	socket := flags.String("socket", "", "the socket to serve (required)")
	scenario := flags.String("scenario", "", "a scenario file (TOML): agents, and changes over time")
	churn := flags.Duration("churn", 0, "move every agent's status on at this interval")
	n := flags.Int("agents", 3, "how many agents to serve without a scenario")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *socket == "" || flags.NArg() > 0 || *churn < 0 || *n < 0 {
		fmt.Fprintln(stderr, "usage: fakeherdr --socket <path> [--scenario <file>] [--churn <interval>] [--agents <n>]")
		return 2
	}

	w := &world{detection: map[string]string{}}
	var changes []change
	if *scenario != "" {
		var err error
		if changes, err = w.load(*scenario); err != nil {
			fmt.Fprintf(stderr, "fakeherdr: %v\n", err)
			return 1
		}
	} else {
		kind := "claude"
		for i := range *n {
			status := statuses[i%4]
			w.set(spec{Pane: fmt.Sprintf("w1:p%d", i+1), Kind: &kind, Status: &status})
		}
	}

	srv, err := herdrtest.Start(*socket)
	if err != nil {
		fmt.Fprintf(stderr, "fakeherdr: %v\n", err)
		return 1
	}
	defer srv.Close()
	srv.LogRequests(stdout)
	w.publish(srv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var tick <-chan time.Time
	if *churn > 0 {
		t := time.NewTicker(*churn)
		defer t.Stop()
		tick = t.C
	}
	start := time.Now()
	for {
		var next <-chan time.Time
		if len(changes) > 0 {
			next = time.After(time.Until(start.Add(changes[0].at)))
		}
		select {
		case <-ctx.Done():
			return 0
		case <-next:
			w.apply(changes[0])
			changes = changes[1:]
		case <-tick:
			w.churn()
		}
		w.publish(srv)
	}
}

// A spec is an agent as a scenario writes it: [[agent]], or the fields a
// [[change]] sets.
type spec struct {
	Pane      string  `toml:"pane"`
	Kind      *string `toml:"kind"`
	Name      *string `toml:"name"`
	Title     *string `toml:"title"`
	Status    *string `toml:"status"`
	Detection *string `toml:"detection"`
}

// A change is a [[change]]: when, and what.
type change struct {
	at   time.Duration
	spec spec
	gone bool
}

// A world is herdr's agents, in order, their detection text, and the seq.
type world struct {
	agents    []herdr.AgentInfo
	detection map[string]string // by pane
	seq       uint64
}

// load reads a scenario: its agents into w, and its changes, in time order.
func (w *world) load(path string) ([]change, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Agent  []spec `toml:"agent"`
		Change []struct {
			At string `toml:"at"`
			spec
			Gone bool `toml:"gone"`
		} `toml:"change"`
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	dir := filepath.Dir(path)
	read := func(s *spec) error {
		if !protocol.IsPane(s.Pane) {
			return fmt.Errorf("pane %q isn't a pane id like w1:p3", s.Pane)
		}
		if s.Status != nil && !slices.Contains(statuses, *s.Status) {
			return fmt.Errorf("%s: status %q isn't one of %s", s.Pane, *s.Status, strings.Join(statuses, ", "))
		}
		if s.Detection != nil {
			file := *s.Detection
			if !filepath.IsAbs(file) {
				file = filepath.Join(dir, file)
			}
			text, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("%s: detection: %w", s.Pane, err)
			}
			*s.Detection = string(text)
		}
		return nil
	}
	for i := range f.Agent {
		if err := read(&f.Agent[i]); err != nil {
			return nil, fmt.Errorf("%s: agent %d: %w", path, i+1, err)
		}
		if f.Agent[i].Status == nil {
			return nil, fmt.Errorf("%s: agent %d: no status", path, i+1)
		}
		w.set(f.Agent[i])
	}
	var changes []change
	for i, c := range f.Change {
		at, err := time.ParseDuration(c.At)
		if err == nil && at < 0 {
			err = errors.New("before the start")
		}
		if err != nil {
			return nil, fmt.Errorf("%s: change %d: at %q: %v", path, i+1, c.At, err)
		}
		if err := read(&c.spec); err != nil {
			return nil, fmt.Errorf("%s: change %d: %w", path, i+1, err)
		}
		changes = append(changes, change{at: at, spec: c.spec, gone: c.Gone})
	}
	slices.SortStableFunc(changes, func(a, b change) int { return int(a.at - b.at) })
	return changes, nil
}

// set adds the agent s describes, or changes the one in its pane by the
// fields s sets. A new status takes the next seq.
func (w *world) set(s spec) {
	i := slices.IndexFunc(w.agents, func(a herdr.AgentInfo) bool { return a.PaneID == s.Pane })
	if i < 0 {
		ws, _, _ := strings.Cut(s.Pane, ":")
		w.agents = append(w.agents, herdr.AgentInfo{
			TerminalID: "term_" + strings.ReplaceAll(s.Pane, ":", "_"), PaneID: s.Pane,
			WorkspaceID: ws, TabID: ws + ":t1", Status: "idle", Revision: 1,
		})
		i = len(w.agents) - 1
	}
	a := &w.agents[i]
	if s.Kind != nil {
		a.Agent = s.Kind
	}
	if s.Name != nil {
		a.Name = s.Name
	}
	if s.Title != nil {
		a.TitleStripped = s.Title
	}
	if s.Detection != nil {
		w.detection[s.Pane] = *s.Detection
	}
	if s.Status != nil {
		w.status(a, *s.Status)
	}
}

// status gives a status, and the next seq; done also gets a completion_seq.
func (w *world) status(a *herdr.AgentInfo, status string) {
	w.seq++
	a.Status, a.StateChangeSeq = status, w.seq
	if status == "done" {
		seq := w.seq
		a.CompletionSeq = &seq
	}
}

// apply makes change c.
func (w *world) apply(c change) {
	if c.gone {
		w.agents = slices.DeleteFunc(w.agents, func(a herdr.AgentInfo) bool { return a.PaneID == c.spec.Pane })
		delete(w.detection, c.spec.Pane)
		return
	}
	w.set(c.spec)
}

// churn moves each agent's status on one step of statuses' first four.
func (w *world) churn() {
	for i := range w.agents {
		next := (slices.Index(statuses[:4], w.agents[i].Status) + 1) % 4
		w.status(&w.agents[i], statuses[next])
	}
}

// publish gives srv the world as it is now.
func (w *world) publish(srv *herdrtest.Server) {
	srv.SetAgents(w.agents)
	for pane, text := range w.detection {
		srv.SetDetection(pane, text)
	}
}
