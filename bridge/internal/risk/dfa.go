package risk

import (
	"regexp/syntax"
	"slices"
	"sync"
	"unicode"
	"unicode/utf8"
)

// A matcher finds a rule's pattern in a text in time linear in the text. Go's
// regexp is linear too, but its NFA spends 2 to 5 ms on 8 KB against the
// default rules (task 3.14 measured it), so a matcher runs the pattern's RE2
// program as a DFA instead: a few nanoseconds a rune. It builds the DFA's
// states as it's made, up to max (maxStates, or less in a test), and any more
// as texts need them, as RE2 does. So a match against the default rules
// builds none, and takes the same steps for the same text whatever came
// before, which the fuzzer's coverage relies on. It runs the program forward
// to find where the earliest match ends, then the reversed program back from
// there to find where the longest match ending there starts.
type matcher struct {
	fwd, rev *dfa
}

func newMatcher(pattern string, max int) (*matcher, error) {
	re, err := syntax.Parse(pattern, syntax.Perl) // as regexp.Compile parses it
	if err != nil {
		return nil, err
	}
	re = re.Simplify()
	fwd, err := syntax.Compile(re)
	if err != nil {
		return nil, err
	}
	rev, err := syntax.Compile(reverse(re))
	if err != nil {
		return nil, err
	}
	return &matcher{fwd: newDFA(fwd, false, max), rev: newDFA(rev, true, max)}, nil
}

// find returns where the pattern matches t, in bytes: of the matches that end
// first, the longest.
func (m *matcher) find(t *text) (start, end int, ok bool) {
	e, ok := m.fwd.earliestEnd(t)
	if !ok {
		return 0, 0, false
	}
	return int(t.offs[m.rev.longestStart(t, e)]), int(t.offs[e]), true
}

// A text is decoded once for every rule: its runes, as regexp reads them
// (utf8.RuneError for each invalid byte); the index of each into a state's
// on (the rune if it's ASCII, else utf8.RuneSelf), so the loops needn't
// branch on it; and the offset of each, with the text's length last.
type text struct {
	s     string
	runes []rune
	syms  []uint8
	offs  []int32
}

var texts = sync.Pool{New: func() any { return new(text) }}

// decode returns s decoded, from texts; put it back when done.
func decode(s string) *text {
	t := texts.Get().(*text)
	runes := slices.Grow(t.runes[:0], len(s))[:len(s)] // a rune takes a byte at least
	syms := slices.Grow(t.syms[:0], len(s))[:len(s)]
	offs := slices.Grow(t.offs[:0], len(s)+1)[:len(s)+1]
	n := 0
	for i, r := range s {
		runes[n], syms[n], offs[n] = r, utf8.RuneSelf, int32(i)
		if r < utf8.RuneSelf {
			syms[n] = uint8(r)
		}
		n++
	}
	offs[n] = int32(len(s))
	t.s, t.runes, t.syms, t.offs = s, runes[:n], syms[:n], offs[:n+1]
	return t
}

func (t *text) put() {
	if cap(t.runes) <= 64<<10 { // a text that long needn't stay
		t.s = ""
		texts.Put(t)
	}
}

// reverse returns a regexp matching each text re matches, reversed.
func reverse(re *syntax.Regexp) *syntax.Regexp {
	r := *re
	r.Sub = make([]*syntax.Regexp, len(re.Sub))
	for i, sub := range re.Sub {
		r.Sub[i] = reverse(sub)
	}
	switch re.Op {
	case syntax.OpConcat:
		slices.Reverse(r.Sub)
	case syntax.OpLiteral:
		r.Rune = slices.Clone(re.Rune)
		slices.Reverse(r.Rune)
	case syntax.OpBeginLine:
		r.Op = syntax.OpEndLine
	case syntax.OpEndLine:
		r.Op = syntax.OpBeginLine
	case syntax.OpBeginText:
		r.Op = syntax.OpEndText
	case syntax.OpEndText:
		r.Op = syntax.OpBeginText
	}
	return &r
}

// The context of a position, which decides the empty-width assertions (^ $
// \A \z \b \B), is the class of the rune on each side of it.
const (
	ctxEdge    = iota // no rune: the start or end of the text
	ctxNewline        // \n
	ctxWord           // [0-9A-Za-z_]
	ctxOther
)

// ctxRune is a rune of each context class.
var ctxRune = [...]rune{ctxEdge: -1, ctxNewline: '\n', ctxWord: 'a', ctxOther: ' '}

func ctxOf(r rune) uint8 {
	switch {
	case r < 0:
		return ctxEdge
	case r == '\n':
		return ctxNewline
	case syntax.IsWordChar(r):
		return ctxWord
	}
	return ctxOther
}

// maxStates bounds a DFA's states (the default rules' largest has 115). A
// program with more builds the rest as texts need them, and its cache starts
// again when it fills.
const maxStates = 512

// A dfa is a program run as a DFA. Runes fall into classes that every
// instruction and assertion treats alike, so a state's transitions are
// indexed by class, and on an ASCII rune by the rune itself. A program is
// flat if every non-ASCII rune is in one class.
type dfa struct {
	prog     *syntax.Prog
	anchored bool   // a match starts only where the run starts
	bounds   []rune // the first rune of each class, ascending
	high     int32  // the class of utf8.RuneSelf, the first non-ASCII rune
	flat     bool   // high is the last class: every non-ASCII rune is in it
	invalid  int32  // the class of utf8.RuneError

	mu     sync.Mutex // guards what follows
	max    int
	states map[string]*state
	starts [4]*state // by the context before the run starts
	mark   []uint32  // per instruction: the closure that last visited it
	gen    uint32
	stack  []uint32
	key    []byte
}

// A state is the threads waiting at a position, each an instruction to read
// the next rune; the context of the rune before the position; and whether a
// match ended just before that rune.
type state struct {
	insts   []uint32
	prev    uint8
	matched bool
	end     int8 // 0 unknown, 1 no match ends at the end of the text, 2 one does
	// The transitions: on each ASCII rune, then on any other if the program
	// is flat; and on each class.
	on   [utf8.RuneSelf + 1]*state
	next []*state
}

func newDFA(prog *syntax.Prog, anchored bool, max int) *dfa {
	// A class starts where any instruction's rune ranges start or end, and
	// where the context changes.
	b := []rune{0, '\n', '\n' + 1, '0', '9' + 1, 'A', 'Z' + 1, '_', '_' + 1, 'a', 'z' + 1, utf8.RuneSelf}
	for _, inst := range prog.Inst {
		switch inst.Op {
		case syntax.InstRune, syntax.InstRune1, syntax.InstRuneAny, syntax.InstRuneAnyNotNL:
		default:
			continue
		}
		if len(inst.Rune) == 1 {
			r := inst.Rune[0]
			b = append(b, r, r+1)
			if syntax.Flags(inst.Arg)&syntax.FoldCase != 0 {
				for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
					b = append(b, f, f+1)
				}
			}
			continue
		}
		for i := 0; i+1 < len(inst.Rune); i += 2 {
			b = append(b, inst.Rune[i], inst.Rune[i+1]+1)
		}
	}
	slices.Sort(b)
	b = slices.Compact(b)
	b = slices.DeleteFunc(b, func(r rune) bool { return r > unicode.MaxRune })
	d := &dfa{prog: prog, anchored: anchored, bounds: b, max: max, states: map[string]*state{}, mark: make([]uint32, len(prog.Inst))}
	d.high, d.invalid = d.class(utf8.RuneSelf), d.class(utf8.RuneError)
	d.flat = d.high == int32(len(b)-1)
	d.build()
	return d
}

// build makes every state a run can reach, and all their transitions, while
// they fit in d.max with room for one more state's.
func (d *dfa) build() {
	var queue []*state
	for before := range uint8(len(d.starts)) {
		queue = append(queue, d.start(before))
	}
	built := map[*state]bool{}
	for len(queue) > 0 && len(d.states)+len(d.bounds) <= d.max {
		s := queue[0]
		queue = queue[1:]
		if built[s] {
			continue
		}
		built[s] = true
		for c := range int32(len(d.bounds)) {
			queue = append(queue, d.onClass(s, c))
		}
		for r := range rune(utf8.RuneSelf) {
			s.on[r] = s.next[d.class(r)]
		}
		if d.flat {
			s.on[utf8.RuneSelf] = s.next[d.high]
		}
		d.atEnd(s)
	}
}

func (d *dfa) class(r rune) int32 {
	i, found := slices.BinarySearch(d.bounds, r)
	if !found {
		i--
	}
	return int32(i)
}

// on returns s's transition on r, once s.on[sym] (r's text.syms), the fast
// path that the loops inline, has missed.
func (d *dfa) on(s *state, r rune) *state {
	if uint32(r) < utf8.RuneSelf {
		n := d.onClass(s, d.class(r))
		s.on[r] = n
		return n
	}
	// Most programs are flat (all but SQL's, among the default rules, which
	// have the case-folds of s and k).
	if d.flat {
		n := d.onClass(s, d.high)
		s.on[utf8.RuneSelf] = n
		return n
	}
	if r == utf8.RuneError {
		return d.onClass(s, d.invalid)
	}
	return d.onClass(s, d.class(r))
}

func (d *dfa) onClass(s *state, c int32) *state {
	if n := s.next[c]; n != nil {
		return n
	}
	return d.step(s, c)
}

// earliestEnd returns where the earliest match in t ends, as an index into
// t.offs.
func (d *dfa) earliestEnd(t *text) (int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.start(ctxEdge)
	for i, sym := range t.syms {
		n := s.on[sym]
		if n == nil {
			n = d.on(s, t.runes[i])
		}
		if n.matched {
			return i, true
		}
		s = n
	}
	return len(t.runes), d.atEnd(s)
}

// longestStart runs the reversed program back from end, where a match ends,
// and returns where the longest match ending there starts. Both are indexes
// into t.offs.
func (d *dfa) longestStart(t *text, end int) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	after := uint8(ctxEdge)
	if end < len(t.runes) {
		after = ctxOf(t.runes[end])
	}
	start := end // only if the program is wrong: a match ends at end
	s, i := d.start(after), end
	for ; i > 0 && len(s.insts) > 0; i-- {
		n := s.on[t.syms[i-1]]
		if n == nil {
			n = d.on(s, t.runes[i-1])
		}
		if n.matched {
			start = i
		}
		s = n
	}
	if i == 0 && d.atEnd(s) {
		start = 0
	}
	return start
}

func (d *dfa) start(before uint8) *state {
	if d.starts[before] == nil {
		s := d.intern([]uint32{uint32(d.prog.Start)}, before, false)
		d.starts[before] = s
	}
	return d.starts[before]
}

// step builds s's transition on class c.
func (d *dfa) step(s *state, c int32) *state {
	r := d.bounds[c] // stands for the whole class
	insts, matched := d.closure(s, syntax.EmptyOpContext(ctxRune[s.prev], r), r)
	n := d.intern(insts, ctxOf(r), matched)
	s.next[c] = n
	return n
}

// atEnd reports whether a match ends at the end of the text, after s.
func (d *dfa) atEnd(s *state) bool {
	if s.end == 0 {
		s.end = 1
		if _, matched := d.closure(s, syntax.EmptyOpContext(ctxRune[s.prev], -1), -1); matched {
			s.end = 2
		}
	}
	return s.end == 2
}

// closure follows s's threads (and a new one at the program's start, unless
// anchored) through the instructions that read no rune, where flags allow,
// and returns the instructions after those that read r (none for -1), and
// whether a thread reached a match.
func (d *dfa) closure(s *state, flags syntax.EmptyOp, r rune) (next []uint32, matched bool) {
	if d.gen++; d.gen == 0 {
		clear(d.mark)
		d.gen = 1
	}
	stack := append(d.stack[:0], s.insts...)
	if !d.anchored {
		stack = append(stack, uint32(d.prog.Start))
	}
	for len(stack) > 0 {
		pc := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if d.mark[pc] == d.gen {
			continue
		}
		d.mark[pc] = d.gen
		inst := &d.prog.Inst[pc]
		switch inst.Op {
		case syntax.InstAlt, syntax.InstAltMatch:
			stack = append(stack, inst.Arg, inst.Out)
		case syntax.InstCapture, syntax.InstNop:
			stack = append(stack, inst.Out)
		case syntax.InstEmptyWidth:
			if syntax.EmptyOp(inst.Arg)&^flags == 0 {
				stack = append(stack, inst.Out)
			}
		case syntax.InstMatch:
			matched = true
		case syntax.InstRune:
			if r >= 0 && inst.MatchRune(r) {
				next = append(next, inst.Out)
			}
		case syntax.InstRune1:
			if r == inst.Rune[0] {
				next = append(next, inst.Out)
			}
		case syntax.InstRuneAny:
			if r >= 0 {
				next = append(next, inst.Out)
			}
		case syntax.InstRuneAnyNotNL:
			if r >= 0 && r != '\n' {
				next = append(next, inst.Out)
			}
		}
	}
	d.stack = stack
	slices.Sort(next)
	return slices.Compact(next), matched
}

func (d *dfa) intern(insts []uint32, prev uint8, matched bool) *state {
	key := append(d.key[:0], prev, 0)
	if matched {
		key[1] = 1
	}
	for _, pc := range insts {
		key = append(key, byte(pc), byte(pc>>8), byte(pc>>16), byte(pc>>24))
	}
	d.key = key
	if s, ok := d.states[string(key)]; ok {
		return s
	}
	if len(d.states) >= d.max {
		d.states = map[string]*state{}
		d.starts = [4]*state{}
	}
	s := &state{insts: insts, prev: prev, matched: matched, next: make([]*state, len(d.bounds))}
	d.states[string(key)] = s
	return s
}
