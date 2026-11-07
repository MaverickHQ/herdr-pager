// Package risk is the danger rules (parent 18, parent design §Danger rules):
// an embedded risk.toml, plus the user's overrides, each rule with its own
// test cases. Rules match the raw command or path, before sanitizing.
package risk

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"

	"github.com/pelletier/go-toml/v2"
)

// A Rule flags one kind of danger.
type Rule struct {
	Label        string // e.g. "FORCE PUSH"
	Pattern      *regexp.Regexp
	TestsMatch   []string
	TestsNoMatch []string

	m *matcher // Pattern's program as a DFA; Load builds it
}

// find returns where the rule's pattern matches t.
func (r *Rule) find(t *text) (start, end int, ok bool) {
	if r.m == nil { // a Rule that Load didn't build
		loc := r.Pattern.FindStringIndex(t.s)
		if loc == nil {
			return 0, 0, false
		}
		return loc[0], loc[1], true
	}
	return r.m.find(t)
}

// matches reports whether the rule's pattern matches s.
func (r *Rule) matches(s string) bool {
	t := decode(s)
	defer t.put()
	_, _, ok := r.find(t)
	return ok
}

// Rules are checked in order; the first match wins.
type Rules []Rule

// A Hit is a rule's match: its label, and where the matched fragment lies in
// the text, so truncation can keep it visible (parent 18.3). Of the rule's
// matches, the fragment is the longest of those that end first.
type Hit struct {
	Label      string
	Start, End int
}

//go:embed risk.toml
var defaults []byte

// MaxLabel is the longest label: the banner "DANGER: <label>" fills at most a
// 40-column row (the protocol's rl).
const MaxLabel = 32

// Load reads the embedded rules, then the overrides file at path if there is
// one ("" for none; a missing file is none). A user rule with a default's
// label replaces it; the others follow the defaults. A rule whose own cases
// fail, or that is otherwise invalid, is an error naming its file and line,
// and nothing is loaded.
func Load(overrides string) (Rules, error) {
	rules, err := parse("bridge/internal/risk/risk.toml", defaults)
	if err != nil {
		return nil, err
	}
	if overrides == "" {
		return rules, nil
	}
	data, err := os.ReadFile(overrides)
	if errors.Is(err, fs.ErrNotExist) {
		return rules, nil
	}
	if err != nil {
		return nil, fmt.Errorf("risk: %w", err)
	}
	user, err := parse(overrides, data)
	if err != nil {
		return nil, err
	}
next:
	for _, u := range user {
		for i := range rules {
			if rules[i].Label == u.Label {
				rules[i] = u
				continue next
			}
		}
		rules = append(rules, u)
	}
	return rules, nil
}

// file is a rules file as written.
type file struct {
	Rule []struct {
		Label        string   `toml:"label"`
		Pattern      string   `toml:"pattern"`
		TestsMatch   []string `toml:"tests_match"`
		TestsNoMatch []string `toml:"tests_no_match"`
	} `toml:"rule"`
}

// ruleHeader finds each [[rule]] line, so an error can name a rule's line.
var ruleHeader = regexp.MustCompile(`(?m)^[ \t]*\[\[[ \t]*rule[ \t]*\]\]`)

// parse reads a rules file strictly and checks each rule.
func parse(name string, data []byte) (Rules, error) {
	var f file
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		var strict *toml.StrictMissingError
		var de *toml.DecodeError
		switch {
		case errors.As(err, &strict) && len(strict.Errors) > 0:
			row, _ := strict.Errors[0].Position()
			return nil, fmt.Errorf("risk: %s:%d: unknown key %q", name, row, strict.Errors[0].Key()[len(strict.Errors[0].Key())-1])
		case errors.As(err, &de):
			row, _ := de.Position()
			return nil, fmt.Errorf("risk: %s:%d: %v", name, row, de)
		}
		return nil, fmt.Errorf("risk: %s: %w", name, err)
	}
	headers := ruleHeader.FindAllIndex(data, -1)
	rules := make(Rules, 0, len(f.Rule))
	seen := map[string]bool{}
	for i, r := range f.Rule {
		line := 0
		if i < len(headers) {
			line = 1 + bytes.Count(data[:headers[i][0]], []byte("\n"))
		}
		rule, err := check(r.Label, r.Pattern, r.TestsMatch, r.TestsNoMatch)
		if err == nil && seen[r.Label] {
			err = errors.New("another rule has this label")
		}
		if err != nil {
			return nil, fmt.Errorf("risk: %s:%d: rule %q: %w", name, line, r.Label, err)
		}
		seen[r.Label] = true
		rules = append(rules, rule)
	}
	return rules, nil
}

// check builds a rule and runs its own cases.
func check(label, pattern string, match, noMatch []string) (Rule, error) {
	switch {
	case label == "":
		return Rule{}, errors.New("no label")
	case len(label) > MaxLabel:
		return Rule{}, fmt.Errorf("the label is %d characters, over %d", len(label), MaxLabel)
	}
	for i := 0; i < len(label); i++ {
		if label[i] < 0x20 || label[i] > 0x7E {
			return Rule{}, errors.New("the label isn't printable ASCII")
		}
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Rule{}, fmt.Errorf("pattern: %w", err)
	}
	m, err := newMatcher(pattern, maxStates)
	if err != nil {
		return Rule{}, fmt.Errorf("pattern: %w", err)
	}
	switch {
	case len(match) == 0:
		return Rule{}, errors.New("no tests_match: a rule needs a case it flags")
	case len(noMatch) == 0:
		return Rule{}, errors.New("no tests_no_match: a rule needs a case it doesn't flag")
	}
	rule := Rule{Label: label, Pattern: re, TestsMatch: match, TestsNoMatch: noMatch, m: m}
	for _, c := range match {
		if !rule.matches(c) {
			return Rule{}, fmt.Errorf("its pattern doesn't match %q, one of its tests_match", c)
		}
	}
	for _, c := range noMatch {
		if rule.matches(c) {
			return Rule{}, fmt.Errorf("its pattern matches %q, one of its tests_no_match", c)
		}
	}
	return rule, nil
}

// MatchEither returns the first rule that matches text or alt, a copy of the
// same length (the card's copy with its quotes blanked, O-90's second
// review), with its fragment from whichever it matched, text first. Twice
// Match's time at most.
func (r Rules) MatchEither(text, alt string) (Hit, bool) {
	t, a := decode(text), decode(alt)
	defer t.put()
	defer a.put()
	for i := range r {
		if start, end, ok := r[i].find(t); ok {
			return Hit{Label: r[i].Label, Start: start, End: end}, true
		}
		if start, end, ok := r[i].find(a); ok {
			return Hit{Label: r[i].Label, Start: start, End: end}, true
		}
	}
	return Hit{}, false
}

// Match returns the first rule that matches text. It takes time linear in
// the text: under 1 ms for 8 KB (TestRiskMatchWithinBudget).
func (r Rules) Match(text string) (Hit, bool) {
	t := decode(text)
	defer t.put()
	for i := range r {
		if start, end, ok := r[i].find(t); ok {
			return Hit{Label: r[i].Label, Start: start, End: end}, true
		}
	}
	return Hit{}, false
}
