package xpath

import (
	"strings"
	"testing"
)

// A backreference to a fixed-width group is resolved exactly: the group has
// only one possible submatch, so RE2's single answer cannot be the wrong one.
func TestBackrefFixedWidth(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`matches("aa", "(a)\1")`, "true"},
		{`matches("ab", "(a)\1")`, "false"},
		// fn:matches is a containment test, so a match anywhere counts.
		{`matches("xxaayy", "(a)\1")`, "true"},
		{`matches("abab", "(a)\1")`, "false"},
		// A class is fixed width.
		{`matches("Mum", "([md])[aeiou]\1", "i")`, "true"},
		{`matches("Mud", "([md])[aeiou]\1", "i")`, "false"},
		// Several groups, referenced in order.
		{`matches("abcabc", "(a)(b)(c)\1\2\3")`, "true"},
		{`matches("abcabd", "(a)(b)(c)\1\2\3")`, "false"},
		// A quantified backreference: each copy is the same fixed width, so
		// consuming as many as fit is the unique maximal match.
		{`matches("A", "([A-Z])\1*")`, "true"},
		{`matches("AAA", "([A-Z])\1*")`, "true"},
		// An anchored pattern must consume the whole input.
		{`matches("aa", "^(a)\1$")`, "true"},
		{`matches("aab", "^(a)\1$")`, "false"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// The "i" flag has to reach the comparison as well as the automaton: RE2 folds
// inside the match, but a backreference is checked by string comparison.
func TestBackrefCaseFolding(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`matches("aA", "(a)\1", "i")`, "true"},
		{`matches("Aa", "(a)\1", "i")`, "true"},
		{`matches("aA", "(a)\1")`, "false"},
		{`matches("ab", "(a)\1", "i")`, "false"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// The refusal boundary is the part that keeps this correct. A group whose
// width can vary has more than one possible submatch, RE2 returns only the
// greedy one, and comparing against it would answer confidently and wrongly —
// "(a*)\1" against "aa" is a match, by the split "a"+"a", but the greedy
// assignment leaves nothing for the backreference.
//
// Refusing is the whole reason this needs no option to switch on: an engine
// that answers correctly or says it cannot is safe to have on always.
func TestBackrefVariableWidthRefused(t *testing.T) {
	for _, expr := range []string{
		`matches("aa", "(a*)\1")`,
		`matches("aa", "(a+)\1")`,
		`matches("aa", "(a?)\1")`,
		`matches("aa", "(a{1,2})\1")`,
		// An alternation whose branches differ in width is variable too.
		`matches("aa", "(a|bb)\1")`,
		// A backreference that is not last would need the comparison to feed
		// back into the automaton, which is the backtracking this avoids.
		`matches("aab", "(a)\1b")`,
		// A group that does not exist.
		`matches("aa", "(a)\2")`,
	} {
		err := evalErr(t, testDoc, expr)
		if !strings.Contains(err.Error(), "FORX0002") {
			t.Errorf("%s = %v, want FORX0002", expr, err)
		}
	}
}

// An alternation whose branches agree in width is still fixed.
func TestBackrefAlternationSameWidth(t *testing.T) {
	if got := evalStr(t, testDoc, `matches("ab ab", "((a|x)b) \1")`); got != "true" {
		t.Errorf("same-width alternation = %q, want true", got)
	}
}

// "\11" is group 11 when eleven groups exist, and group 1 followed by a
// literal "1" otherwise. Both spellings appear in the suite.
func TestBackrefGreedyNumbering(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`matches("#abc#1", "^(#)abc\11$")`, "true"},
		{`matches("#abc#2", "^(#)abc\11$")`, "false"},
	}
	for _, c := range cases {
		if got := evalStr(t, testDoc, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// The XML Schema pattern facet has no backreference at all — Appendix F's atom
// production has no form for one — so the schema flavour must keep refusing
// them however well the XPath flavour copes.
func TestBackrefNotInSchemaFlavour(t *testing.T) {
	for _, v11 := range []bool{false, true} {
		if _, err := TranslateSchemaRegexpVersion(`(a)\1`, v11); err == nil {
			t.Errorf("v11=%v: the schema flavour must refuse a backreference", v11)
		}
	}
}

// A pathological pattern must stay linear: this is the property RE2 is kept
// for, and the fixed-width restriction is what preserves it.
func TestBackrefStaysLinear(t *testing.T) {
	long := strings.Repeat("a", 20000)
	// Each candidate position costs one comparison of a fixed-width group, so
	// the whole search is linear in the input.
	if got := evalStr(t, testDoc,
		`matches("`+long+`b", "^([a-z])\1*$")`); got != "false" {
		t.Errorf("long input = %q, want false", got)
	}
}
