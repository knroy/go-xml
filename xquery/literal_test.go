package xquery_test

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xquery"
)

// TestStringLiteralReferences covers the expansion in literal.go: §3.1.1 gives
// XQuery's StringLiteral the character and entity references XPath's lacks, so
// a literal handed to xpath as a substring has to arrive with them expanded.
func TestStringLiteralReferences(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`"&amp;"`, `&`},
		{`"&lt;"`, `<`},
		{`"&gt;"`, `>`},
		{`"&#8364;"`, `€`},
		{`"&#x20AC;"`, `€`},
		{`"&#0000045;"`, `-`},

		// The expanded character may be the quote that closes the literal.
		// Doubling is XQuery's escape for it and XPath reads it the same way,
		// which is the whole reason the expansion can be done on the source.
		{`"&quot;"`, `"`},
		{`'&apos;'`, `'`},
		{`"&apos;"`, `'`},
		{`'&quot;'`, `"`},

		// A reference beside ordinary text, and several in one literal.
		{`"a&amp;b"`, `a&b`},
		{`"&lt;&gt;&amp;"`, `<>&`},
		{`concat("&lt;", "x", "&gt;")`, `<x>`},

		// An "&" outside a literal is not a reference, and a literal with no
		// "&" is untouched. Both are the common case and must not change.
		{`"plain"`, `plain`},
		{`string-join(("a", "b"), "-")`, `a-b`},

		// The doubled quote already in the source is the escape, not two
		// literals: expanding around it must leave its meaning alone.
		{`"a""&amp;"`, `a"&`},
	} {
		got, err := run(t, c.src, xquery.Options{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s\n got %q\nwant %q", c.src, got, c.want)
		}
	}
}

// TestStringLiteralReferenceErrors checks that a malformed reference is
// refused rather than left as text.
//
// XPath has no reference syntax, so without this every one of these evaluates
// to the characters it is spelled with. XPST0003 is the code for a reference
// that is not well formed and XQST0090 for one naming a codepoint XML does not
// have — including the ones that overflow a rune, which must be range-checked
// before the conversion rather than truncated into a character they are not.
func TestStringLiteralReferenceErrors(t *testing.T) {
	for _, c := range []struct{ src, code string }{
		{`"a &;"`, "XPST0003"},
		{`"a &#;"`, "XPST0003"},
		{`"a &#x;"`, "XPST0003"},
		{`"a &#1233a98;"`, "XPST0003"},
		{`"a &#x543g3;"`, "XPST0003"},
		{`"a &LT;"`, "XPST0003"},
		{`"a &lte;"`, "XPST0003"},
		{`"a &"`, "XPST0003"},
		{`"&#X4A;"`, "XPST0003"},
		{`"&#-20;"`, "XPST0003"},
		{`"&#x+20;"`, "XPST0003"},
		{`"&#x00;"`, "XQST0090"},
		{`<p>FA&#4294967542;IL</p>`, "XQST0090"},
		{`<p>FA&#xFF000000F6;IL</p>`, "XQST0090"},
	} {
		_, err := run(t, c.src, xquery.Options{})
		if err == nil {
			t.Errorf("%s: no error, want %s", c.src, c.code)
			continue
		}
		if !strings.Contains(err.Error(), c.code) {
			t.Errorf("%s: %v, want %s", c.src, err, c.code)
		}
	}
}
