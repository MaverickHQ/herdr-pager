// Package sanitize turns terminal text into what a pager may show: printable
// ASCII within a limit (parent 13.4, parent design §Sanitizer). It runs last,
// on each display field; matching never sees its output (ADR 0006).
package sanitize

import "strings"

// The limits, in characters.
const (
	Name = 24 // an agent's display name
	Kind = 10 // an agent kind
	At   = 16 // where an agent works: its folder's name
	Line = 40 // a card's summary and each of its lines
)

// Text returns in as printable ASCII of at most limit characters, truncated
// head and tail with ".." (lane cards, tasks 3.1–3.7).
func Text(in string, limit int) string {
	return truncate(clean(in), limit)
}

// Short returns in as printable ASCII of at most limit characters, cut at its
// end, at the last space, hyphen or underscore in its second half if there's
// one, then "..": a name that reads from its start (the desk checkpoint,
// 2026-10-24; a command or path keeps Text's head and tail).
func Short(in string, limit int) string {
	s := clean(in)
	if len(s) <= limit || limit <= len(marker) {
		return truncate(s, limit)
	}
	keep := s[:limit-len(marker)]
	atWordEnd := strings.ContainsRune(" -_", rune(s[len(keep)]))
	if i := strings.LastIndexAny(keep, " -_"); !atWordEnd && i >= len(keep)/2 {
		keep = keep[:i]
	}
	return strings.TrimRight(keep, " -_") + marker
}

// clean is Text's steps 1 to 5: printable ASCII on one line.
func clean(in string) string {
	var b strings.Builder
	for _, r := range stripEscapes(in) {
		switch {
		case control(r) || invisible(r):
		case r < 0x80:
			b.WriteRune(r)
		default:
			b.WriteString(transliterate(r))
		}
	}
	// Step 5: only ' ' and '\n' are whitespace by now, and a field is one line.
	return strings.Join(strings.Fields(b.String()), " ")
}

// marker stands for what truncation cut out.
const marker = ".."

// truncate cuts s, ASCII by now, to limit characters, keeping its head and
// its tail around the marker; the head takes the odd character. A limit too
// small for the marker keeps the head alone (step 6).
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	if limit <= len(marker) {
		return s[:max(limit, 0)]
	}
	head := (limit - len(marker) + 1) / 2
	tail := limit - len(marker) - head
	return s[:head] + marker + s[len(s)-tail:]
}

// transliterations is step 4's table: the symbols agents draw with, as
// ASCII. Box drawing is boxDrawing's.
var transliterations = map[rune]string{
	'❯': ">", '›': ">", '»': ">",
	'…': "..",
	'–': "-", '—': "-",
	'‘': "'", '’': "'",
	'“': `"`, '”': `"`,
}

// transliterate returns non-ASCII r as ASCII: its transliteration, or "?"
// (step 4).
func transliterate(r rune) string {
	if s, ok := transliterations[r]; ok {
		return s
	}
	if r >= 0x2500 && r <= 0x257F {
		return boxDrawing(r)
	}
	return "?"
}

// boxDrawing returns a rune of the box-drawing block as "-" for a horizontal
// line, "|" for a vertical one, and "+" for the rest: corners, tees, crosses,
// arcs and diagonals.
func boxDrawing(r rune) string {
	switch r {
	case '─', '━', '┄', '┅', '┈', '┉', '╌', '╍', '═', '╴', '╶', '╸', '╺', '╼', '╾':
		return "-"
	case '│', '┃', '┆', '┇', '┊', '┋', '╎', '╏', '║', '╵', '╷', '╹', '╻', '╽', '╿':
		return "|"
	}
	return "+"
}

// Hides reports whether s holds what the sanitizer takes out without a
// trace: ESC, a C0 or C1 control but '\n' and '\t', DEL, or a bidi or
// zero-width character. Text holding one can show less than it runs (a
// terminated escape string hides its payload), so a card whose command or
// path does fails closed (the security pass's review, 2026-11-01).
func Hides(s string) bool {
	for _, r := range s {
		if control(r) && r != '\t' || invisible(r) {
			return true
		}
	}
	return false
}

// control reports the C0 controls but '\n', DEL and the C1 controls (step 2).
func control(r rune) bool {
	return r < 0x20 && r != '\n' || r >= 0x7F && r <= 0x9F
}

// invisible reports the bidi embeddings, overrides and isolates and the
// zero-width characters, which change how text reads without showing
// (step 3).
func invisible(r rune) bool {
	switch {
	case r >= 0x202A && r <= 0x202E, // LRE, RLE, PDF, LRO, RLO
		r >= 0x2066 && r <= 0x2069, // LRI, RLI, FSI, PDI
		r >= 0x200B && r <= 0x200D, // ZWSP, ZWNJ, ZWJ
		r == 0xFEFF, r == 0x2060:   // ZWNBSP, WJ
		return true
	}
	return false
}

const (
	bel = 0x07
	esc = 0x1b
)

// stripEscapes removes the escape sequences of ECMA-48 (CSI, OSC, DCS, SOS,
// PM, APC and the short escapes), with their parameters and payloads (step
// 1). herdr already strips them; this is defence in depth. In decoded text a
// C1 code point (U+0080 to U+009F) begins nothing, so the 8-bit forms are
// left to step 2, which removes each as one control; and a control string
// with no terminator loses only its introducer. Neither can hide the rest of
// a field, such as the end of a command (amended 2026-11-01, the security
// pass).
//
// It runs in time linear in the text: once a control string's scan finds no
// terminator to the end, no later one can find one either (what it would scan
// is part of what was scanned), so later introducers aren't scanned again
// (the security pass's review, 2026-11-01: text made only of introducers
// took quadratic time).
func stripEscapes(in string) string {
	rs := []rune(in)
	out := make([]rune, 0, len(rs))
	unterminated := false // a control string's scan found no terminator to the end
	for i := 0; i < len(rs); {
		n := escapeLen(rs[i:], &unterminated)
		if n == 0 {
			out = append(out, rs[i])
			n = 1
		}
		i += n
	}
	return string(out)
}

// escapeLen is the length in runes of the escape sequence at the start of
// rs, or 0 if none starts there. unterminated is stripEscapes': once set, a
// control string is its introducer alone, unscanned.
func escapeLen(rs []rune, unterminated *bool) int {
	if rs[0] != esc {
		return 0
	}
	if len(rs) == 1 {
		return 1
	}
	switch r := rs[1]; {
	case r == '[':
		return 2 + csiLen(rs[2:])
	case r == 'P', r == 'X', r == ']', r == '^', r == '_':
		if *unterminated {
			return 2
		}
		n := stringLen(rs[2:])
		*unterminated = n == 0
		return 2 + n // 2 alone for one with no terminator
	case r >= 0x20 && r <= 0x2f: // intermediates, then a final byte
		n := 1
		for n < len(rs) && rs[n] >= 0x20 && rs[n] <= 0x2f {
			n++
		}
		if n < len(rs) && rs[n] >= 0x30 && rs[n] <= 0x7e {
			n++
		}
		return n
	case r >= 0x30 && r <= 0x7e: // one final byte
		return 2
	}
	return 1 // a lone ESC
}

// csiLen is the length of a control sequence's body: parameter and
// intermediate bytes, then a final byte. A sequence cut short ends before the
// rune that breaks it.
func csiLen(rs []rune) int {
	n := 0
	for n < len(rs) && rs[n] >= 0x20 && rs[n] <= 0x3f {
		n++
	}
	if n < len(rs) && rs[n] >= 0x40 && rs[n] <= 0x7e {
		n++
	}
	return n
}

// stringLen is the length of a control string's body up to and including its
// terminator, BEL or ST (ESC \); 0 if it has none, so that only its
// introducer goes.
func stringLen(rs []rune) int {
	for n := 0; n < len(rs); n++ {
		switch rs[n] {
		case bel:
			return n + 1
		case esc:
			if n+1 < len(rs) && rs[n+1] == '\\' {
				return n + 2
			}
		}
	}
	return 0
}
