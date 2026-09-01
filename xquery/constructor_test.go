package xquery_test

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xquery"
)

// run compiles and evaluates a query, rendering its result.
func run(t *testing.T, src string, opts xquery.Options) (string, error) {
	t.Helper()
	q, err := xquery.Compile(src, opts)
	if err != nil {
		return "", err
	}
	seq, err := q.Eval(xpath.NewContext(nil, xpath.Builtins()))
	if err != nil {
		return "", err
	}
	return render(seq), nil
}

func TestDirectElementConstructors(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`<a/>`, `<a/>`},
		{`<a></a>`, `<a/>`},
		{`<a>text</a>`, `<a>text</a>`},
		{`<a><b/><c/></a>`, `<a><b/><c/></a>`},
		{`<a><b><c/></b></a>`, `<a><b><c/></b></a>`},
		{`<a>{1+1}</a>`, `<a>2</a>`},
		{`<a>x{1}y</a>`, `<a>x1y</a>`},
		{`<a>{1,2,3}</a>`, `<a>1 2 3</a>`},
		{`<a>{()}</a>`, `<a/>`},
		{`<a>{<b/>}</a>`, `<a><b/></a>`},
	} {
		got, err := run(t, c.src, xquery.Options{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

func TestAttributes(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`<a b="1"/>`, `<a b="1"/>`},
		{`<a b="1" c="2"/>`, `<a b="1" c="2"/>`},
		{`<a b="x{1+1}y"/>`, `<a b="x2y"/>`},
		// Each enclosed expression is atomised and space-joined, and adjacent
		// parts are concatenated with no separator: §3.9.1.1's worked example.
		{`<a b="[{1, 5 to 7, 9}]"/>`, `<a b="[1 5 6 7 9]"/>`},
		{`<a b="{()}"/>`, `<a b=""/>`},
		// A doubled quote of the kind that opened the value stands for one.
		{`<a b="say ""hi"""/>`, `<a b="say &quot;hi&quot;"/>`},
		{`<a b='it''s'/>`, `<a b="it's"/>`},
		// Doubled braces are an escaped brace.
		{`<a b="{{x}}"/>`, `<a b="{x}"/>`},
	} {
		got, err := run(t, c.src, xquery.Options{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

// A namespace declaration governs how the element's OWN name resolves, though
// the name is written before the declaration. That is why the attribute list
// is scanned before anything in the start tag is resolved.
func TestNamespaceDeclarationsResolveTheElementsOwnName(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`<p:a xmlns:p="urn:x"/>`, `<p:a xmlns:p="urn:x"/>`},
		{`<a xmlns:p="urn:x"><p:b/></a>`, `<a xmlns:p="urn:x"><p:b/></a>`},
		{`<a xmlns="urn:d"/>`, `<a xmlns="urn:d"/>`},
		// A prefix declared on an inner element goes out of scope with it.
		{`<a xmlns:p="urn:x"><b xmlns:q="urn:y"><q:c/></b></a>`,
			`<a xmlns:p="urn:x"><b xmlns:q="urn:y"><q:c/></b></a>`},
	} {
		got, err := run(t, c.src, xquery.Options{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

// §3.9.1.4. Whitespace that only separates markup goes; whitespace that came
// from a reference or a CDATA section, or that sits next to real text, stays.
func TestBoundaryWhitespace(t *testing.T) {
	for _, c := range []struct {
		src        string
		strip      string
		preserve   string
	}{
		{`<a> {1} </a>`, `<a>1</a>`, `<a> 1 </a>`},
		{`<a>   </a>`, `<a/>`, `<a>   </a>`},
		{`<a> z {1}</a>`, `<a> z 1</a>`, `<a> z 1</a>`},
		{`<a><b/> <c/></a>`, `<a><b/><c/></a>`, `<a><b/> <c/></a>`},
		// A character reference is not whitespace for this purpose, so the
		// space survives under either policy.
		{`<a>&#x20;{1}</a>`, `<a> 1</a>`, `<a> 1</a>`},
	} {
		got, err := run(t, c.src, xquery.Options{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
		} else if got != c.strip {
			t.Errorf("strip %s\n got %s\nwant %s", c.src, got, c.strip)
		}
		got, err = run(t, c.src,
			xquery.Options{BoundarySpace: xquery.PreserveSpace})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
		} else if got != c.preserve {
			t.Errorf("preserve %s\n got %s\nwant %s", c.src, got, c.preserve)
		}
	}
}

func TestComputedConstructors(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`element foo {1}`, `<foo>1</foo>`},
		{`element foo {<b/>}`, `<foo><b/></foo>`},
		{`element {"foo"} {1}`, `<foo>1</foo>`},
		{`element foo {attribute b {1}}`, `<foo b="1"/>`},
		{`text {"hi"}`, `hi`},
		{`text {1,2}`, `1 2`},
		{`comment {"c"}`, `<!--c-->`},
		{`document {<a/>}`, `<a/>`},
		{`processing-instruction p {"d"}`, `<?p d?>`},
	} {
		got, err := run(t, c.src, xquery.Options{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

func TestDirectCommentAndPI(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`<!-- hi -->`, `<!-- hi -->`},
		{`<?target data?>`, `<?target data?>`},
		{`<a><!-- c --></a>`, `<a><!-- c --></a>`},
		{`<a><![CDATA[<raw> & stuff]]></a>`, `<a>&lt;raw&gt; &amp; stuff</a>`},
	} {
		got, err := run(t, c.src, xquery.Options{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

func TestReferences(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`<a>&lt;&gt;&amp;</a>`, `<a>&lt;&gt;&amp;</a>`},
		{`<a>&#65;</a>`, `<a>A</a>`},
		{`<a>&#x41;</a>`, `<a>A</a>`},
		{`<a b="&quot;"/>`, `<a b="&quot;"/>`},
	} {
		got, err := run(t, c.src, xquery.Options{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.src, got, c.want)
		}
	}
}

// Every expression the xpath package can compile is an XQuery expression.
func TestPlainExpressions(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`1+1`, `2`},
		{`"a"`, `a`},
		{`(1,2,3)`, `123`},
		{`concat("a","b")`, `ab`},
		{`1, 2`, `12`},
	} {
		got, err := run(t, c.src, xquery.Options{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %s want %s", c.src, got, c.want)
		}
	}
}

func TestStaticErrors(t *testing.T) {
	for _, c := range []struct{ src, code string }{
		// The end tag must match the start tag lexically.
		{`<a></b>`, "XQST0118"},
		// A namespace declaration's value must be a literal.
		{`<a xmlns:p="{1}"/>`, "XQST0022"},
		// One prefix, twice, on one element.
		{`<a xmlns:p="urn:x" xmlns:p="urn:y"/>`, "XQST0071"},
		// Reserved prefixes and namespaces.
		{`<a xmlns:xmlns="urn:x"/>`, "XQST0070"},
		{`<a xmlns:p="http://www.w3.org/2000/xmlns/"/>`, "XQST0070"},
		// The same attribute twice.
		{`<a b="1" b="2"/>`, "XQST0040"},
		// An unbound prefix.
		{`<p:a/>`, "XPST0081"},
		// A lone brace in content has to be doubled.
		{`<a>}</a>`, "XPST0003"},
		// XQuery has no namespace axis.
		{`namespace::*`, "XPST0003"},
	} {
		_, err := run(t, c.src, xquery.Options{})
		if err == nil {
			t.Errorf("%s: want %s, got no error", c.src, c.code)
			continue
		}
		if !strings.Contains(err.Error(), c.code) {
			t.Errorf("%s: want %s, got %v", c.src, c.code, err)
		}
	}
}

// XQuery raises XQDY0025 for a duplicate attribute where XSLT silently keeps
// the last. This is the one place the shared builder's behaviour forks, and
// the policy is what selects it.
func TestDuplicateAttributeIsAnError(t *testing.T) {
	_, err := run(t, `element a {attribute b {1}, attribute b {2}}`,
		xquery.Options{})
	if err == nil || !strings.Contains(err.Error(), "XQDY0025") {
		t.Errorf("want XQDY0025, got %v", err)
	}
}

func TestUnimplementedIsNamed(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`for $x in 1 return $x`, "for"},
		{`let $x := 1 return $x`, "let"},
		{`declare namespace p = "urn:x"; 1`, "prolog"},
		{`xquery version "3.1"; 1`, "prolog"},
	} {
		_, err := run(t, c.src, xquery.Options{})
		if err == nil {
			t.Errorf("%s: want an error naming %q", c.src, c.want)
			continue
		}
		if !strings.Contains(err.Error(), c.want) ||
			!strings.Contains(err.Error(), "not implemented") {
			t.Errorf("%s: want a clear %q error, got %v", c.src, c.want, err)
		}
	}
}

// A compiled query is immutable, so one may be run from several goroutines.
func TestQueryIsConcurrencySafe(t *testing.T) {
	q, err := xquery.Compile(`<a>{1 to 5}</a>`, xquery.Options{})
	if err != nil {
		t.Fatal(err)
	}
	const n = 32
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			seq, err := q.Eval(xpath.NewContext(nil, xpath.Builtins()))
			if err != nil {
				errs <- err
				return
			}
			if got := render(seq); got != `<a>1 2 3 4 5</a>` {
				t.Errorf("got %s", got)
			}
			errs <- nil
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

// The XQuery-only expression forms of §§3.11.6, 3.12, 3.14-3.17.
//
// Each is checked for the thing that distinguishes it from what the
// expression parser beneath would otherwise make of the same text, rather than
// for breadth: the suite supplies breadth.
func TestXQueryOnlyExpressions(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		// A catch clause selects on the code, and binds the err: variables
		// whether or not the query declared the prefix — §3.16 predeclares it.
		{`try { 1 div 0 } catch * { "caught" }`, "caught"},
		{`try { 1 idiv 0 } catch err:FOAR0001 { "div" } catch * { "other" }`, "div"},
		{`try { 1 idiv 0 } catch *:FOAR0001 { "wild" }`, "wild"},
		{`try { 1 idiv 0 } catch err:* { "anyerr" }`, "anyerr"},
		{`try { 1 idiv 0 } catch * { string($err:code) }`,
			"err:FOAR0001"},
		// A try whose body succeeds returns the body, and no clause runs.
		{`try { 41 + 1 } catch * { 0 }`, "42"},
		// A code that matches no clause propagates rather than being lost.
		{`try { try { 1 idiv 0 } catch err:XPTY0004 { "wrong" } } ` +
			`catch * { "outer" }`, "outer"},

		// switch compares atomised values with deep-equal's rule, so an
		// operand of a different type is a non-match rather than a type error.
		{`switch (2) case 1 return "a" case 2 return "b" default return "c"`, "b"},
		{`switch ("x") case 1 return "a" default return "d"`, "d"},
		{`switch (()) case 1 return "a" default return "empty"`, "empty"},
		// Several case operands may share one return clause.
		{`switch (3) case 2 case 3 return "b" default return "c"`, "b"},

		// typeswitch matches a whole SequenceType, occurrence included.
		{`typeswitch (5) case $i as xs:integer return $i + 1 ` +
			`default return 0`, "6"},
		{`typeswitch ((1,2)) case $i as xs:integer return "one" ` +
			`default return "many"`, "many"},
		{`typeswitch ("a") case xs:integer|xs:date return "n" ` +
			`case xs:string return "s" default return "no"`, "s"},
		{`typeswitch (<a/>) case element(a) return "elem" default return "no"`,
			"elem"},
		// The default clause may bind a variable too.
		{`typeswitch (7) case $i as xs:string return "s" ` +
			`default $d return $d * 2`, "14"},

		// ordered and unordered return their operand untouched.
		{`ordered { (1,2,3) }`, "123"},
		{`unordered { 4 }`, "4"},

		// An unrecognised pragma is ignored and the enclosed expression is
		// what the extension expression means.
		{`(# xml:unknown some content #) { 42 }`, "42"},

		// The string constructor is a string, and its interpolations are
		// atomised and joined with single spaces.
		{"``[hello]``", "hello"},
		{"``[a `{1+1}` b]``", "a 2 b"},
		{"``[`{(1,2)}`]``", "1 2"},
		// Braces and quotes need no escaping inside one, which is the point.
		{"``[{\"a\"}]``", `{"a"}`},
	} {
		got, err := run(t, c.src, xquery.Options{})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
}

// An extension expression with no enclosed expression is XQST0079 rather than
// a silently ignored pragma: §3.17 makes the enclosed expression the only
// thing a processor recognising no pragma can evaluate.
func TestPragmaWithoutBodyIsAnError(t *testing.T) {
	_, err := run(t, `(# xml:unknown x #)`, xquery.Options{})
	if err == nil || !strings.Contains(err.Error(), "XQST0079") {
		t.Errorf("want XQST0079, got %v", err)
	}
}
