package emutest

// The desk scenarios' rig (lane goldens, group 9): what a desk test reads from
// an instance besides its screenshot, through the instance's control socket
// (the kit's JSON-RPC, docs/emulator.md §The control protocol): the UI tree,
// and the tones played.
//
// Text that changes with the time (the clock, and the ages) is drawn by
// labels the firmware tags MaskTag, so a golden masks them wherever they sit:
// the band's clock floats after the counts, and its digits differ in width.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/captures"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/keymap"
)

// MaskTag is the kit UI tag (lcd349_ui_tag) on every label whose text changes
// with the time: goldens mask it.
const MaskTag = "golden-mask"

// rpcConn is a connection to an instance's control socket.
type rpcConn struct {
	conn net.Conn
	r    *bufio.Reader
}

func (in *Instance) dial() (*rpcConn, error) {
	conn, err := net.DialTimeout("unix", filepath.Join(in.Dir, "control.sock"), 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &rpcConn{conn: conn, r: bufio.NewReader(conn)}, nil
}

// call makes one JSON-RPC call on the instance's control socket, and decodes
// its result into result.
func (in *Instance) call(method string, params, result any) error {
	c, err := in.dial()
	if err != nil {
		return err
	}
	defer c.conn.Close()
	return c.call(method, params, result)
}

func (c *rpcConn) call(method string, params, result any) error {
	c.conn.SetDeadline(time.Now().Add(30 * time.Second))
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	if _, err := c.conn.Write(append(req, '\n')); err != nil {
		return err
	}
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &reply); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if reply.Error != nil {
		return fmt.Errorf("%s: %d %s", method, reply.Error.Code, reply.Error.Message)
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(reply.Result, result)
}

// Node is one object of the UI tree, as the kit's ui.tree reports it.
type Node struct {
	Path      string   `json:"path"`
	Class     string   `json:"class"`
	X         int      `json:"x"`
	Y         int      `json:"y"`
	W         int      `json:"w"`
	H         int      `json:"h"`
	Visible   bool     `json:"visible"`
	Text      *string  `json:"text"`
	TextColor string   `json:"text_color"`
	BgColor   string   `json:"bg_color"`
	Tags      []string `json:"tags"`
	Children  []Node   `json:"children"`
	TopLayer  *Node    `json:"top_layer"`
}

// Rect is the node's area on screen.
func (n Node) Rect() image.Rectangle { return image.Rect(n.X, n.Y, n.X+n.W, n.Y+n.H) }

// Walk calls fn on n and every node under it, the top layer last.
func (n Node) Walk(fn func(Node)) {
	fn(n)
	for _, c := range n.Children {
		c.Walk(fn)
	}
	if n.TopLayer != nil {
		n.TopLayer.Walk(fn)
	}
}

// Tree returns the instance's UI tree, the screen and its top layer.
func (in *Instance) Tree() Node {
	in.t.Helper()
	var res struct {
		Tree      Node `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := in.call("ui.tree", map[string]any{"depth": 64}, &res); err != nil {
		in.t.Fatal(err)
	}
	if res.Truncated {
		in.t.Fatal("the UI tree was truncated")
	}
	return res.Tree
}

// Masks are the areas of the visible objects tagged MaskTag: the clock and the ages.
func (in *Instance) Masks() []image.Rectangle {
	in.t.Helper()
	return in.masksOf(in.Tree())
}

// maskedText is all a label tagged MaskTag may say: the band's clock (when
// known), link, network and LAST SEEN; a list row's age; device info's
// fingerprints, and its IP line, whose bridge address has the emulator's
// bridge's port of the run (O-77); or nothing. A golden skips what's masked on both sides, so
// the firmware's tags alone would decide what the goldens check: a label
// tagged by mistake, such as the counts, would pass unseen.
var maskedText = regexp.MustCompile(`^(|(\d\d:\d\d)?  [~ox#?] .+|\d{1,3}[mhd]|(DEV|HOST) (SHA256:[A-Za-z0-9+/]{43}|-)|IP (\d{1,3}(\.\d{1,3}){3}|-)  BRIDGE \S*:\d{1,5})$`)

// masksOf is tree's Masks, after checking each masked label's text (maskedText).
func (in *Instance) masksOf(tree Node) []image.Rectangle {
	in.t.Helper()
	var bad []string
	tree.Walk(func(n Node) {
		if n.Visible && slices.Contains(n.Tags, MaskTag) && n.Text != nil && !maskedText.MatchString(*n.Text) {
			bad = append(bad, *n.Text)
		}
	})
	if len(bad) > 0 {
		in.t.Fatalf("labels tagged %s say %q: not a clock, an age or a fingerprint, so no golden would see them", MaskTag, bad)
	}
	return tree.Masks()
}

// Masks are the areas of the visible objects under n tagged MaskTag.
func (n Node) Masks() []image.Rectangle {
	var masks []image.Rectangle
	n.Walk(func(n Node) {
		if n.Visible && slices.Contains(n.Tags, MaskTag) {
			masks = append(masks, n.Rect())
		}
	})
	return masks
}

// updateGoldens (go test ./emutest/ -update-goldens) rewrites each desk
// golden a run reaches from its screenshot, and fails the test, so an update
// is never silent: look at the goldens, then run again without it.
var updateGoldens = flag.Bool("update-goldens", false, "rewrite the desk goldens the run reaches, and fail")

// MatchDeskGolden compares the screen with the golden desk/<name>.png,
// masking the clock and the ages (Masks) and the extra masks given; a new
// golden is written with them painted over. Then it lints the screen.
func (in *Instance) MatchDeskGolden(name string, extra ...image.Rectangle) {
	in.t.Helper()
	in.matchDesk(name, in.Shot(), append(in.Masks(), extra...))
	in.Lint()
}

// MatchDeskSnap is MatchDeskGolden for the screen as a Snap caught it
// (mid-hold): its masks are the snap's tree's, and its lint the snap's.
func (in *Instance) MatchDeskSnap(name string, s Snap, extra ...image.Rectangle) {
	in.t.Helper()
	in.matchDesk(name, s.Shot, append(in.masksOf(s.Tree), extra...))
	if s.Lint.Errors > 0 {
		var lines []string
		for _, f := range s.Lint.Findings {
			lines = append(lines, fmt.Sprintf("%s %s %s %q: %s", f.Severity, f.Rule, f.Path, f.Text, f.Message))
		}
		in.t.Fatalf("%s: the kit's lint found %d errors:\n  %s", name, s.Lint.Errors, strings.Join(lines, "\n  "))
	}
}

func (in *Instance) matchDesk(name string, shot image.Image, masks []image.Rectangle) {
	in.t.Helper()
	golden := Golden("desk/" + name + ".png")
	img := image.NewRGBA(shot.Bounds())
	draw.Draw(img, img.Bounds(), shot, shot.Bounds().Min, draw.Src)
	for _, m := range masks {
		draw.Draw(img, m, &image.Uniform{color.RGBA{0x80, 0x80, 0x80, 0xFF}}, image.Point{}, draw.Src)
	}
	if *updateGoldens {
		if err := writePNG(golden, img); err != nil {
			in.t.Fatal(err)
		}
		in.t.Errorf("%s rewritten (-update-goldens): check it, then run again", golden)
	} else {
		MatchGolden(in.t, img, golden, masks...)
	}
}

// The body's rows: 36 px each, from the band's bottom (design D2-10).
const (
	bodyY = 28
	rowH  = 36
)

// statusRight is the status row's right edge: the PWR hint cell starts at 483
// (platform/hints.h), and the row stops 12 px short of it (ui/screen_status.c).
const statusRight = 471

// Rows reads the list's visible rows from the UI tree, top to bottom: each
// row's labels left to right, joined by spaces, without the masked ones (the
// ages), and "[sel] " first on the selected row (drawn inverted: its labels'
// ground is the foreground colour).
func (in *Instance) Rows() []string {
	in.t.Helper()
	type label struct {
		x, y int
		text string
		sel  bool
	}
	byRow := map[int][]label{}
	in.Tree().Walk(func(n Node) {
		if !n.Visible || n.Text == nil || *n.Text == "" || n.Y < bodyY || slices.Contains(n.Tags, MaskTag) {
			return
		}
		r := (n.Y - bodyY) / rowH
		byRow[r] = append(byRow[r], label{n.X, n.Y, *n.Text, n.BgColor != "" && n.BgColor == fgColour})
	})
	var rows []string
	for r := 0; r < 4; r++ {
		ls := byRow[r]
		if len(ls) == 0 {
			continue
		}
		slices.SortFunc(ls, func(a, b label) int { return a.x - b.x })
		var parts []string
		sel := false
		for _, l := range ls {
			parts = append(parts, l.text)
			sel = sel || l.sel
		}
		row := strings.Join(parts, " ")
		if sel {
			row = "[sel] " + row
		}
		rows = append(rows, row)
	}
	return rows
}

// Tiles reads a menu's visible tiles from the UI tree, left to right: each
// tile's text, and "[sel] " first on the selected one (drawn inverted).
func (in *Instance) Tiles() []string {
	in.t.Helper()
	var labels []Node
	in.Tree().Walk(func(n Node) {
		if n.Visible && n.Text != nil && *n.Text != "" && n.Y >= bodyY {
			labels = append(labels, n)
		}
	})
	slices.SortStableFunc(labels, func(a, b Node) int { return a.X - b.X })
	var tiles []string
	for _, l := range labels {
		if l.BgColor == fgColour {
			tiles = append(tiles, "[sel] "+*l.Text)
		} else {
			tiles = append(tiles, *l.Text)
		}
	}
	return tiles
}

// WaitTiles waits for the menu's tiles (Tiles) to be want.
func (in *Instance) WaitTiles(timeout time.Duration, want ...string) {
	in.t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(50 * time.Millisecond) {
		got := in.Tiles()
		if slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			in.t.Fatalf("the menu's tiles:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
		}
	}
}

// fgColour is the theme's foreground (platform/theme.h), as ui.tree writes a colour.
const fgColour = "#0A0605"

// Tone is one tone the instance played (audio.events).
type Tone struct {
	Seq  uint64 `json:"seq"`
	TMs  int64  `json:"t_ms"`
	Hz   int    `json:"hz"`
	Ms   int    `json:"ms"`
	Stop bool   `json:"stop"`
}

// Tones returns the tones played from event since on, and the next event's number.
func (in *Instance) Tones(since uint64) ([]Tone, uint64) {
	in.t.Helper()
	var res struct {
		Events []Tone `json:"events"`
		Next   uint64 `json:"next"`
	}
	if err := in.call("audio.events", map[string]any{"since": since}, &res); err != nil {
		in.t.Fatal(err)
	}
	return res.Events, res.Next
}

// WaitTone waits for a tone (not a stop) from event since on, and returns it.
func (in *Instance) WaitTone(since uint64, timeout time.Duration) Tone {
	in.t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(50 * time.Millisecond) {
		events, _ := in.Tones(since)
		for _, e := range events {
			if !e.Stop && e.Hz > 0 {
				return e
			}
		}
		if time.Now().After(deadline) {
			in.t.Fatalf("no tone within %v", timeout)
		}
	}
}

// A Hold is a button held down, until Release. The kit ties a hold to the
// control connection that made it, so the connection stays open meanwhile.
type Hold struct {
	in     *Instance
	c      *rpcConn
	button string
}

// Hold presses button ("boot" or "pwr") and keeps it down.
func (in *Instance) Hold(button string) *Hold {
	in.t.Helper()
	c, err := in.dial()
	if err != nil {
		in.t.Fatal(err)
	}
	if err := c.call("input.press", map[string]any{"button": button, "state": "down"}, nil); err != nil {
		c.conn.Close()
		in.t.Fatal(err)
	}
	h := &Hold{in: in, c: c, button: button}
	in.t.Cleanup(func() { c.conn.Close() })
	return h
}

// Release lets the button go, once the release has been debounced.
func (h *Hold) Release() {
	h.in.t.Helper()
	defer h.c.conn.Close()
	if err := h.c.call("input.press", map[string]any{"button": h.button, "state": "up"}, nil); err != nil {
		h.in.t.Fatal(err)
	}
}

// A Snap is the screen at one moment, for a screen that won't stay (mid-hold):
// its screenshot, UI tree and lint, asked back to back.
type Snap struct {
	Shot image.Image
	Tree Node
	Lint struct {
		Errors   int `json:"errors"`
		Warnings int `json:"warnings"`
		Findings []struct {
			Rule     string `json:"rule"`
			Severity string `json:"severity"`
			Path     string `json:"path"`
			Text     string `json:"text"`
			Message  string `json:"message"`
		} `json:"findings"`
	}
}

// Snap takes a Snap while the button is held, on the hold's own connection:
// faster than the CLI's shot and lint, so the hold is still short of its next threshold.
func (h *Hold) Snap() Snap {
	h.in.t.Helper()
	var s Snap
	var shot struct {
		Data []byte `json:"data"` // a PNG
	}
	var tree struct {
		Tree      Node `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := h.c.call("screenshot", map[string]any{"raw": false}, &shot); err != nil {
		h.in.t.Fatal(err)
	}
	if err := h.c.call("ui.tree", map[string]any{"depth": 64}, &tree); err != nil {
		h.in.t.Fatal(err)
	}
	if err := h.c.call("ui.lint", map[string]any{}, &s.Lint); err != nil {
		h.in.t.Fatal(err)
	}
	if tree.Truncated {
		h.in.t.Fatal("the UI tree was truncated")
	}
	img, err := png.Decode(bytes.NewReader(shot.Data))
	if err != nil {
		h.in.t.Fatalf("the screenshot: %v", err)
	}
	s.Shot, s.Tree = img, tree.Tree
	return s
}

// Body returns the texts of the body's visible labels, top to bottom and left
// to right, without the masked ones.
func (in *Instance) Body() []string {
	in.t.Helper()
	var labels []Node
	in.Tree().Walk(func(n Node) {
		if n.Visible && n.Text != nil && *n.Text != "" && n.Y >= bodyY && !slices.Contains(n.Tags, MaskTag) {
			labels = append(labels, n)
		}
	})
	slices.SortStableFunc(labels, func(a, b Node) int {
		if a.Y != b.Y {
			return a.Y - b.Y
		}
		return a.X - b.X
	})
	var texts []string
	for _, l := range labels {
		texts = append(texts, *l.Text)
	}
	return texts
}

// Status returns the texts of the status band's visible labels, left of the
// hint cells and left to right, the masked one too: the counts (or the open
// card's agent), the flags, then the clock, the link, the network and LAST
// SEEN (screen_status.c). The goldens mask it; a test reads it here.
func (in *Instance) Status() []string {
	in.t.Helper()
	var labels []Node
	in.Tree().Walk(func(n Node) {
		if n.Visible && n.Text != nil && *n.Text != "" && n.Y < bodyY && n.X < statusRight {
			labels = append(labels, n)
		}
	})
	slices.SortStableFunc(labels, func(a, b Node) int { return a.X - b.X })
	var texts []string
	for _, l := range labels {
		texts = append(texts, *l.Text)
	}
	return texts
}

// HintCell returns the band's hint cell whose label starts with text, and its
// fill: the hold's progress bar inside it (screen_status.c).
func (in *Instance) HintCell(text string) (cell, fill Node) {
	in.t.Helper()
	cell, fill, ok := in.Tree().HintCell(text)
	if !ok {
		in.t.Fatalf("no hint cell says %q", text)
	}
	return cell, fill
}

// HintCell is Instance.HintCell in the tree n.
func (n Node) HintCell(text string) (cell, fill Node, ok bool) {
	for _, h := range n.Hints() {
		if strings.HasPrefix(h.Text, text) {
			return h.Cell, h.Fill, true
		}
	}
	return Node{}, Node{}, false
}

// A Hint is one of the band's hint cells: the cell, its label's text, and its fill.
type Hint struct {
	Cell, Fill Node
	Text       string
}

// Hints returns the band's hint cells in the tree n, left to right: PWR's,
// then BOOT's. A cell is a box in the band holding a label, then the fill
// (screen_status.c); the status row beside them holds two labels.
func (n Node) Hints() []Hint {
	var hints []Hint
	n.Walk(func(n Node) {
		if n.Y != 0 || len(n.Children) != 2 || n.Children[0].Text == nil || n.Children[1].Text != nil {
			return
		}
		hints = append(hints, Hint{Cell: n, Fill: n.Children[1], Text: *n.Children[0].Text})
	})
	slices.SortFunc(hints, func(a, b Hint) int { return a.Cell.X - b.Cell.X })
	return hints
}

// A deskAgent is one agent of a desk scenario, of kind claude.
type deskAgent struct {
	pane, name, status string
	prompt             string // the Claude manifest's prompt its detection text is a capture of ("shell"), or ""
	detection          string // a file of detection text written by the test, instead of prompt's capture
}

// claudePrompt is the first capture of prompt in phase 0's Claude manifest
// (testdata/herdr/claude/manifest.toml): tests pick captures through it,
// never by file name (design D2-7). It returns the entry and its file's path.
func claudePrompt(t testing.TB, prompt string) (captures.Capture, string) {
	t.Helper()
	m, err := captures.Load("claude")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Captures {
		if c.Prompt == prompt && c.Captured {
			return c, filepath.Join(m.Dir, c.File)
		}
	}
	t.Fatalf("the Claude manifest has no captured prompt %q", prompt)
	return captures.Capture{}, ""
}

// deskScenario is fakeherdr's scenario for agents.
func deskScenario(t testing.TB, agents ...deskAgent) string {
	t.Helper()
	var s strings.Builder
	for _, a := range agents {
		fmt.Fprintf(&s, "[[agent]]\npane = %q\nkind = \"claude\"\nname = %q\nstatus = %q\n", a.pane, a.name, a.status)
		switch {
		case a.detection != "":
			fmt.Fprintf(&s, "detection = %q\n", a.detection)
		case a.prompt != "":
			_, file := claudePrompt(t, a.prompt)
			fmt.Fprintf(&s, "detection = %q\n", file)
		}
		s.WriteString("\n")
	}
	return s.String()
}

// A herdrRequest is one line of fakeherdr's request log.
type herdrRequest struct {
	Time   time.Time       `json:"time"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Requests returns the requests fakeherdr has logged for method so far, in order.
func (b *Bridge) Requests(method string) []herdrRequest {
	var reqs []herdrRequest
	for _, line := range strings.Split(b.HerdrLog.String(), "\n") {
		var r herdrRequest
		if json.Unmarshal([]byte(line), &r) == nil && r.Method == method {
			reqs = append(reqs, r)
		}
	}
	return reqs
}

// SentKeys returns every agent.send_keys in fakeherdr's request log so far.
func (b *Bridge) SentKeys() []herdr.SendKeysParams {
	b.t.Helper()
	var sent []herdr.SendKeysParams
	for _, r := range b.Requests(herdr.MethodSendKeys) {
		var p herdr.SendKeysParams
		if err := json.Unmarshal(r.Params, &p); err != nil {
			b.t.Fatalf("send_keys params %s: %v", r.Params, err)
		}
		sent = append(sent, p)
	}
	return sent
}

// WaitSentKeys waits until fakeherdr has logged n agent.send_keys, and returns them.
func (b *Bridge) WaitSentKeys(n int, timeout time.Duration) []herdr.SendKeysParams {
	b.t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(50 * time.Millisecond) {
		if sent := b.SentKeys(); len(sent) >= n {
			return sent
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("fakeherdr got %d send_keys within %v, want %d; its log:\n%s", len(b.SentKeys()), timeout, n, b.HerdrLog)
		}
	}
}

// claudeKeys are the keys verb sends to a Claude Code agent, from the
// embedded keymap (bridge/keymaps/claude.toml, phase 0's).
func claudeKeys(t testing.TB, verb string) []string {
	t.Helper()
	set, err := keymap.Load("")
	if err != nil {
		t.Fatal(err)
	}
	kind := "claude"
	keys := set.For(&kind).Keys(verb)
	if len(keys) == 0 {
		t.Fatalf("the claude keymap has no %s keys", verb)
	}
	return keys
}

// WaitIdle waits until no frame has been drawn for ms milliseconds: a flash
// or a redraw has ended.
func (in *Instance) WaitIdle(ms int, timeout time.Duration) {
	in.t.Helper()
	in.wait("--idle", fmt.Sprint(ms), timeout)
}
