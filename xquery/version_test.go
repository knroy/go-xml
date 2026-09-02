package xquery

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xpath"
)

// TestVersionDeclRecorded checks that the version a module declares reaches
// the static context, and that a module declaring none gets the default.
//
// The version used to be read and thrown away, which left every rule that
// differs between 1.0 and 3.1 with no way to ask which one applied. These
// assertions are the floor the routed decision points stand on: if the version
// is not recorded, none of them can be right by anything but coincidence.
func TestVersionDeclRecorded(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want XQVersion
		xp   xpath.Version
	}{
		{"declared 1.0", `xquery version "1.0"; 1`, XQuery10, xpath.XPath20},
		{"declared 3.0", `xquery version "3.0"; 1`, XQuery30, xpath.XPath30},
		{"declared 3.1", `xquery version "3.1"; 1`, XQuery31, xpath.XPath31},
		// §4.1 leaves the version of a module with no version declaration
		// implementation-defined. This engine implements 3.1.
		{"undeclared", `1`, XQuery31, xpath.XPath31},
		// The encoding-only form was added in 3.0 and names no version, so
		// the module keeps the default rather than being read as 3.0.
		{"encoding only", `xquery encoding "utf-8"; 1`, XQuery31, xpath.XPath31},
		// "xquery" is a legal name, and a body beginning with one is not a
		// version declaration.
		{"body named xquery", `1`, XQuery31, xpath.XPath31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := Compile(tc.src, Options{})
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.src, err)
			}
			if got := q.sc.xqVersion; got != tc.want {
				t.Errorf("xqVersion = %v, want %v", got, tc.want)
			}
			// The expression language follows: XQuery 1.0 is defined over
			// XPath 2.0, 3.0 over 3.0, 3.1 over 3.1.
			if got := tc.want.xpathVersion(); got != tc.xp {
				t.Errorf("xpathVersion() = %v, want %v", got, tc.xp)
			}
		})
	}
}

// TestVersionDeclUnsupported checks XQST0031, the error for a version this
// processor does not implement.
//
// It is not a syntax error: the declaration is well formed and says what it
// means, and the processor is refusing the language rather than the text.
func TestVersionDeclUnsupported(t *testing.T) {
	for _, src := range []string{
		`xquery version "2.0"; 1`,
		`xquery version "4.0"; 1`,
		`xquery version "3.2"; 1`,
		`xquery version ""; 1`,
		`xquery version "1"; 1`,
	} {
		_, err := Compile(src, Options{})
		if err == nil {
			t.Errorf("Compile(%q): want XQST0031, got no error", src)
			continue
		}
		if !strings.Contains(err.Error(), "XQST0031") {
			t.Errorf("Compile(%q): want XQST0031, got %v", src, err)
		}
	}
}

// TestOptionDeclPrefixByVersion is the option declaration's version split.
//
// XQuery 1.0 §4.16: "The QName must have a prefix; if it does not, a static
// error is raised [err:XPST0081]." XQuery 3.0 §4.19 drops the sentence and
// puts an unprefixed name in no namespace, where it matches no option this
// processor acts on and is ignored.
func TestOptionDeclPrefixByVersion(t *testing.T) {
	const bare = `declare option myopt "option value"; true()`
	const prefixed = `declare namespace p = "http://example.com/";
		declare option p:myopt "option value"; true()`

	if _, err := Compile(`xquery version "1.0"; `+bare, Options{}); err == nil {
		t.Error("1.0 bare option: want XPST0081, got no error")
	} else if !strings.Contains(err.Error(), "XPST0081") {
		t.Errorf("1.0 bare option: want XPST0081, got %v", err)
	}
	// A prefixed name is legal at every version, so the 1.0 rule must not
	// have become a blanket refusal of option declarations.
	if _, err := Compile(`xquery version "1.0"; `+prefixed, Options{}); err != nil {
		t.Errorf("1.0 prefixed option: %v", err)
	}
	for _, v := range []string{`xquery version "3.0"; `, `xquery version "3.1"; `, ``} {
		if _, err := Compile(v+bare, Options{}); err != nil {
			t.Errorf("Compile(%q + bare option): %v", v, err)
		}
	}
}

// TestCastTargetErrorByVersion is the cast target's version split, which the
// xpath parser already had a gate for and which the recorded version now
// feeds.
//
// A SingleType naming a type that is in scope nowhere is XPST0051 in XPath at
// every version and in XQuery 1.0; XQuery 3.0 §3.13.2 gives the cast case an
// error of its own, XQST0052. The suite states both halves over identical
// source: "'string' cast as xs:untyped" is XPST0051 under XQ10
// (K-SeqExprCast-5) and XQST0052 under XQ30+ (K-SeqExprCast-5a).
func TestCastTargetErrorByVersion(t *testing.T) {
	const cast = `"string" cast as xs:untyped`
	for _, tc := range []struct {
		decl string
		want string
	}{
		{`xquery version "1.0"; `, "XPST0051"},
		{`xquery version "3.0"; `, "XQST0052"},
		{`xquery version "3.1"; `, "XQST0052"},
		{``, "XQST0052"},
	} {
		_, err := Compile(tc.decl+cast, Options{})
		if err == nil {
			t.Errorf("Compile(%q + cast): want %s, got no error", tc.decl, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Compile(%q + cast): want %s, got %v", tc.decl, tc.want, err)
		}
	}
}

// TestVariableCycleErrorByVersion is the XQST0054 / XQDY0054 split.
//
// XQuery 1.0 §4.14: "It is a static error [err:XQST0054] if a variable depends
// on itself", over a dependency relation that includes function calls. XQuery
// 3.0 §4.16 narrows the static error to a cycle whose every edge is a direct
// variable reference and adds the dynamic XQDY0054 for a cycle that passes
// through a function body, because that body is not necessarily entered.
//
// The query below is K-InternalVariablesWith-19a's shape: $a's only route to
// $b is through local:f, and $b names $a directly. The loop therefore has one
// function-mediated edge, which is exactly the edge 3.0 exempted from the
// static error -- so 3.0 and later report the *dynamic* XQDY0054, and 1.0
// reports the *static* XQST0054 over the same text.
//
// The assertion is on the code, not on whether the query has an answer:
// which of the two errors a processor raises is the whole of the difference
// the 3.0 split introduced, and it is the thing the recorded version now
// decides.
func TestVariableCycleErrorByVersion(t *testing.T) {
	const src = `declare variable $a := local:f();
		declare variable $b := $a;
		declare function local:f() { if (false()) then $b else 22 };
		$a`

	for _, tc := range []struct {
		decl string
		want string
	}{
		// XQuery 1.0 §4.14: "It is a static error [err:XQST0054] if a
		// variable depends on itself", over a dependency relation that
		// includes function calls.
		{`xquery version "1.0"; `, "XQST0054"},
		// XQuery 3.0 §4.16 narrows the static error to a cycle whose every
		// edge is a direct variable reference, and adds XQDY0054 for a cycle
		// that passes through a function body.
		{`xquery version "3.0"; `, "XQDY0054"},
		{`xquery version "3.1"; `, "XQDY0054"},
		// A module that declares no version is judged as 3.1, so it keeps the
		// behaviour this engine had before the version was recorded.
		{``, "XQDY0054"},
	} {
		q, err := Compile(tc.decl+src, Options{})
		if err != nil {
			t.Errorf("Compile(%q + cycle): %v", tc.decl, err)
			continue
		}
		_, err = q.Eval(nil)
		if err == nil {
			t.Errorf("Eval(%q + cycle): want %s, got no error", tc.decl, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Eval(%q + cycle): want %s, got %v", tc.decl, tc.want, err)
		}
	}
}
