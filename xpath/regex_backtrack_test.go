package xpath

import (
	"strings"
	"testing"
	"time"
)

// withBacktracking runs f with the switch on and restores it afterwards.
//
// The switch is process-wide, so these tests cannot run in parallel with
// anything that depends on the default. They do not call t.Parallel().
func withBacktracking(t *testing.T, f func()) {
	t.Helper()
	prev := BacktrackingRegexEnabled()
	SetBacktrackingRegex(true)
	defer SetBacktrackingRegex(prev)
	f()
}

// evalString evaluates expr and returns its string value and any error.
//
// evalOne in limits_test.go reports the error through t.Errorf and returns "",
// which suits a test asserting a value. Several tests here assert that an
// expression FAILS — a budget exhaustion must surface as an error rather than
// as a false — so they need the error in hand instead.
func evalString(t *testing.T, expr string) (string, error) {
	t.Helper()
	seq, err := Eval(expr, NewContext(nil, Builtins()), nil)
	if err != nil {
		return "", err
	}
	if len(seq) != 1 {
		return "", nil
	}
	return seq[0].(interface{ String() string }).String(), nil
}

// TestBacktrackingIsOffByDefault is the load-bearing test in this file.
//
// Everything else here describes what the engine does when it is asked to;
// this one describes what the package does when nobody asks. A pattern that
// only the backtracker can decide must still be FORX0002, because a server
// evaluating a pattern that came from document data must not inherit an
// exponential matcher by virtue of a dependency upgrade.
func TestBacktrackingIsOffByDefault(t *testing.T) {
	if BacktrackingRegexEnabled() {
		t.Fatal("the backtracking matcher is enabled by default")
	}
	_, err := CompileRegexp(`(['"])(.*?)\1`, "")
	if err == nil {
		t.Fatal("a variable-width backreference compiled with the switch off")
	}
	if !strings.Contains(err.Error(), "FORX0002") {
		t.Errorf("got %v, want FORX0002", err)
	}
}

// TestFixedWidthPathIsNotRerouted checks that turning the switch on does not
// take the fixed-width patterns away from the exact, linear-time path.
//
// The fixed-width analysis is not an approximation the backtracker improves
// on — it is exact and it runs in RE2's time — so routing it through here
// would be a pure loss. The check is on the type rather than on the answer,
// because both engines give the same answer and only the type distinguishes
// which one gave it.
func TestFixedWidthPathIsNotRerouted(t *testing.T) {
	withBacktracking(t, func() {
		br, err := buildBackrefRegexp(`(a)(b)\1`, "", XPath20)
		if err != nil {
			t.Fatalf("the fixed-width path refused a fixed-width pattern: %v", err)
		}
		if br == nil {
			t.Fatal("the fixed-width path declined a pattern it can decide")
		}
		if !br.MatchString("aba") || br.MatchString("abb") {
			t.Error("the fixed-width path gave the wrong answer")
		}
	})
}

// TestBacktrackMatches covers the general backreference cases the fixed-width
// analysis refuses. Every case here is one the XSLT suite's regex-055 asserts.
func TestBacktrackMatches(t *testing.T) {
	cases := []struct {
		pat, in string
		want    bool
	}{
		// A backreference in the middle of the pattern, not at its end.
		{`(da)( )(kommen)\2(sie)`, "kikikeriki!! Tak, tak, tak! - da kommen sie.", true},
		{`(da)( )(kommen)\2(die)`, "kikikeriki!! Tak, tak, tak! - da kommen sie.", false},
		// A quantifier on the referenced group, which leaves RE2 free to
		// report either repetition and so makes capture-and-compare unsound.
		{`(ki){2}ke.*\1`, "kikikeriki!! Tak, tak, tak! - da kommen sie.", true},
		// An alternation whose branches agree in width but not in content.
		{`(ki|ke)\1`, "kikikeriki!! Tak, tak, tak! - da kommen sie.", true},
		// A variable-width expression between the group and the reference.
		{`(!).*\1`, "kikikeriki!! Tak, tak, tak! - da kommen sie.", true},
		// A group that matched the empty string, referenced.
		{`()\1`, "", true},
		// Variable-width groups on both sides.
		{`([0-9]+)([a-z]+)\1`, "123kikikeriki123", true},
		{`([0-9]+)([a-z]+)\1`, "123kikikeriki456", false},
		{`( )\1`, " ", false},
	}
	withBacktracking(t, func() {
		for _, c := range cases {
			bt, err := compileBacktrack(c.pat, "", XPath20)
			if err != nil {
				t.Errorf("%q: compile: %v", c.pat, err)
				continue
			}
			got := bt.MatchString(c.in)
			if e := bt.Err(); e != nil {
				t.Errorf("%q: %v", c.pat, e)
				continue
			}
			if got != c.want {
				t.Errorf("matches(%q, %q) = %v, want %v", c.in, c.pat, got, c.want)
			}
		}
	})
}

// TestBacktrackGreedyRefNumber is the "\12" rule: group 12 when twelve groups
// precede the reference, and group 1 followed by a literal "2" otherwise.
//
// The two readings give opposite answers on the same input, so getting it
// wrong is not a near miss.
func TestBacktrackGreedyRefNumber(t *testing.T) {
	cases := []struct {
		pat, in string
		want    bool
	}{
		// Twelve groups: "\12" is group 12, which captured "6".
		{`(1)(2)(3)(ki)(k)(i)(ke)(ri)(ki)(4)(5)(6)\12`, "123kikikeriki4566", true},
		// One group: "\12" is group 1 then "2", so the input needs the "2".
		{`(ki){2}ke.*\12`, "kikikeriki2!! Tak, tak, tak! - da kommen sie.", true},
		{`(ki){2}ke.*\12`, "kikikeriki!! Tak, tak, tak! - da kommen sie.", false},
	}
	withBacktracking(t, func() {
		for _, c := range cases {
			bt, err := compileBacktrack(c.pat, "", XPath20)
			if err != nil {
				t.Errorf("%q: compile: %v", c.pat, err)
				continue
			}
			if got := bt.MatchString(c.in); got != c.want {
				t.Errorf("matches(%q, %q) = %v, want %v", c.in, c.pat, got, c.want)
			}
		}
	})
}

// TestBacktrackLazyQuantifier is regex-019: the lazy group must stop at the
// first quote the backreference can match, not run to the last.
func TestBacktrackLazyQuantifier(t *testing.T) {
	withBacktracking(t, func() {
		bt, err := compileBacktrack(`(['"])(.*?)\1`, "", XPath20)
		if err != nil {
			t.Fatal(err)
		}
		got := bt.ReplaceAllString(`He said, "I don't" eat 'grass'`, "[${2}]")
		if want := "He said, [I don't] eat [grass]"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// TestBacktrackForwardReferenceIsRefused covers the rule that cost this
// implementation nineteen suite tests before it was found.
//
// Appendix F constrains a backreference to name a group that *precedes* it, so
// a forward reference and a reference to the group it sits inside are both
// malformed patterns rather than patterns that match nothing. Resolving the
// numbers after the parse, when every group is known, accepted all of these.
func TestBacktrackForwardReferenceIsRefused(t *testing.T) {
	patterns := []string{
		`\1`,
		`\1(abc)`,
		`\1([a-c]*)`,
		`\10((((((((((a))))))))))`,
		`(foo)(\300)`,
		`(\2b*?([a-c]))*`,
		`(x(a)\3(\2|b))+`,
		// A reference to the group it is inside: group 1 is not closed here.
		`^(a\1?){4}$`,
		`\1\d(ab)`,
		`((\3|b)\2(a)x)+`,
		// The same rule for a MULTI-digit reference, which reaches the greedy
		// split rather than the closed-group test. "\10" inside the tenth
		// group named a group that was open, and splitting it into "\1" plus
		// a literal "0" turned a malformed pattern into a quiet false.
		`(a)(b)(c)(d)(e)(f)(g)(h)(i)(j\10)`,
		`(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)(k\11)`,
		`(a)(b)(c)(d)(e)(f)(g)(h)(i)(j)(k)(l)((m)(n)(o)(p)(q)\13)`,
	}
	withBacktracking(t, func() {
		for _, p := range patterns {
			bt, err := compileBacktrack(p, "", XPath20)
			if err == nil {
				t.Errorf("%q compiled to %v, want FORX0002", p, bt != nil)
				continue
			}
			if !strings.Contains(err.Error(), "FORX0002") {
				t.Errorf("%q: got %v, want FORX0002", p, err)
			}
		}
	})
}

// TestBacktrackBudgetTerminates is the requirement that this engine cannot be
// made to hang.
//
// "(a*)*\1b" against a run of "a"s is the classic exponential shape: the outer
// star can split the run in exponentially many ways and every one of them
// fails. The engine must stop, and it must stop with an *error* — a false here
// would be a claim about a pattern it never finished evaluating.
func TestBacktrackBudgetTerminates(t *testing.T) {
	withBacktracking(t, func() {
		bt, err := compileBacktrack(`(a*)*\1b`, "", XPath20)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		var got bool
		go func() {
			got = bt.MatchString(strings.Repeat("a", 60))
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(60 * time.Second):
			t.Fatal("the budget did not stop an exponential match")
		}
		if bt.Err() == nil {
			t.Fatalf("exhausting the budget returned %v with no error", got)
		}
		if !strings.Contains(bt.Err().Error(), "FORX0002") {
			t.Errorf("got %v, want FORX0002", bt.Err())
		}
	})
}

// TestBacktrackBudgetSurfacesThroughFnMatches checks that the budget error
// reaches the caller rather than being swallowed into a false.
func TestBacktrackBudgetSurfacesThroughFnMatches(t *testing.T) {
	withBacktracking(t, func() {
		_, err := evalString(t, `matches('`+strings.Repeat("a", 60)+`', '(a*)*\1b')`)
		if err == nil {
			t.Fatal("fn:matches answered an exponential pattern instead of failing")
		}
		if !strings.Contains(err.Error(), "FORX0002") {
			t.Errorf("got %v, want FORX0002", err)
		}
	})
}

// TestBacktrackHonestPatternsAreCheap records what the suite's worst pattern
// actually costs, so that a change which makes the engine exponential on an
// honest pattern shows up here rather than as a timeout somewhere else.
//
// regex-032 is fifteen lazy groups and a \14 against 180 characters — the shape
// that looks exponential and is not, because laziness makes each group stop at
// the first space instead of exploring every split.
func TestBacktrackHonestPatternsAreCheap(t *testing.T) {
	pattern := strings.Repeat(`(.*?\s)`, 15) + `.*?\14.*?`
	input := "The quick brown fox jumped over the lazy dog and immediately " +
		"went into violent convulsions. The quick brown fox jumped over the " +
		"lazy dog and immediately went into violent convulsions."
	withBacktracking(t, func() {
		bt, err := compileBacktrack(pattern, "", XPath20)
		if err != nil {
			t.Fatal(err)
		}
		m := bt.newMachine([]rune(input))
		if bt.findFrom(m, 0) == nil {
			t.Fatal("regex-032 should match")
		}
		// The measured figure is 525. The bound is loose enough that an
		// unrelated change to the step accounting does not fail it, and tight
		// enough that a regression to exponential behaviour does.
		if m.steps > 10_000 {
			t.Errorf("regex-032 took %d steps, want well under the budget", m.steps)
		}
	})
}

// TestBacktrackFindAllSubmatchIndex covers what xsl:analyze-string needs, which
// backrefRegexp never provided: every match with its group offsets, in bytes.
func TestBacktrackFindAllSubmatchIndex(t *testing.T) {
	withBacktracking(t, func() {
		bt, err := compileBacktrack(`(ki|ke)\1`, "", XPath20)
		if err != nil {
			t.Fatal(err)
		}
		in := "kikikeriki"
		locs := bt.FindAllStringSubmatchIndex(in, -1)
		if len(locs) != 1 {
			t.Fatalf("got %d matches, want 1: %v", len(locs), locs)
		}
		if got := in[locs[0][0]:locs[0][1]]; got != "kiki" {
			t.Errorf("match = %q, want %q", got, "kiki")
		}
		if got := in[locs[0][2]:locs[0][3]]; got != "ki" {
			t.Errorf("group 1 = %q, want %q", got, "ki")
		}
	})
}

// TestBacktrackOffsetsAreBytesNotRunes guards the boundary between the engine,
// which works in runes because a backreference compares characters, and its
// callers, which index strings in bytes.
func TestBacktrackOffsetsAreBytesNotRunes(t *testing.T) {
	withBacktracking(t, func() {
		bt, err := compileBacktrack(`(.)\1`, "", XPath20)
		if err != nil {
			t.Fatal(err)
		}
		in := "αβγγδ"
		locs := bt.FindAllStringSubmatchIndex(in, -1)
		if len(locs) != 1 {
			t.Fatalf("got %d matches, want 1", len(locs))
		}
		if got := in[locs[0][0]:locs[0][1]]; got != "γγ" {
			t.Errorf("match = %q, want %q", got, "γγ")
		}
	})
}

// TestBacktrackReusesCharacterClassSemantics is the check that this engine did
// not grow its own copy of the class rules.
//
// Every leaf here is compiled by translatePattern and RE2, so a class rule the
// rest of the package implements applies without being written twice. If the
// two ever diverged, the divergence would be a silent wrong answer rather than
// a visible failure, which is why it is asserted rather than assumed.
func TestBacktrackReusesCharacterClassSemantics(t *testing.T) {
	cases := []struct {
		pat, in string
		want    bool
	}{
		// Class subtraction.
		{`([a-z-[aeiou]])\1`, "xx", true},
		{`([a-z-[aeiou]])\1`, "ee", false},
		// \d is Unicode-wide in XML Schema, not ASCII: these are Arabic-Indic.
		{`(\d)\1`, "٤٤", true},
		// \i and \c are the XML name-character classes.
		{`(\i)\1`, "__", true},
		{`(\i)\1`, "11", false},
		// A Unicode block from Appendix G.
		{`(\p{IsGreek})\1`, "αα", true},
		{`(\p{IsGreek})\1`, "aa", false},
	}
	withBacktracking(t, func() {
		for _, c := range cases {
			bt, err := compileBacktrack(c.pat, "", XPath20)
			if err != nil {
				t.Errorf("%q: compile: %v", c.pat, err)
				continue
			}
			if got := bt.MatchString(c.in); got != c.want {
				t.Errorf("matches(%q, %q) = %v, want %v", c.in, c.pat, got, c.want)
			}
		}
	})
}

// TestBacktrackCacheIsKeyedOnTheMode checks that toggling the switch does not
// serve a compilation made under the other setting.
//
// The compiled patterns share regexCache, and the key that would be natural —
// flags plus pattern — names two different things depending on the mode. The
// symptom of getting this wrong is that whichever setting ran first wins for
// the rest of the process.
func TestBacktrackCacheIsKeyedOnTheMode(t *testing.T) {
	const pattern = `(['"])(.*?)\1`
	if _, err := CompileRegexp(pattern, ""); err == nil {
		t.Fatal("the pattern compiled with the switch off")
	}
	withBacktracking(t, func() {
		re, err := CompileRegexp(pattern, "")
		if err != nil {
			t.Fatalf("the pattern was refused with the switch on: %v", err)
		}
		if _, ok := re.(*btRegexp); !ok {
			t.Errorf("got %T, want the backtracking engine", re)
		}
	})
	if _, err := CompileRegexp(pattern, ""); err == nil {
		t.Fatal("the pattern still compiled after the switch went off again")
	}
}

// TestBacktrackTokenize covers fn:tokenize, which had no backreference path at
// all before this — the RE2 compile failed and that was the end of it.
func TestBacktrackTokenize(t *testing.T) {
	withBacktracking(t, func() {
		got, err := evalString(t, `string-join(tokenize('a11b22c', '(\d)\1'), '|')`)
		if err != nil {
			t.Fatal(err)
		}
		if want := "a|b|c"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// TestBacktrackEmptyStringReplaceIsAnError keeps fn:replace's FORX0003 rule,
// which belongs to the function rather than to the engine underneath it.
func TestBacktrackEmptyStringReplaceIsAnError(t *testing.T) {
	withBacktracking(t, func() {
		_, err := evalString(t, `replace('aa', '()\1', 'x')`)
		if err == nil {
			t.Fatal("a pattern matching the empty string was accepted")
		}
		if !strings.Contains(err.Error(), "FORX0003") {
			t.Errorf("got %v, want FORX0003", err)
		}
	})
}

// TestBacktrackCaseInsensitive checks the "i" flag reaches both the leaves,
// which RE2 folds, and the backreference comparison, which this engine folds.
func TestBacktrackCaseInsensitive(t *testing.T) {
	withBacktracking(t, func() {
		bt, err := compileBacktrack(`(a.*?)\1`, "i", XPath20)
		if err != nil {
			t.Fatal(err)
		}
		if !bt.MatchString("xAbaB") {
			t.Error(`(a.*?)\1 with "i" should match "xAbaB"`)
		}
	})
}
