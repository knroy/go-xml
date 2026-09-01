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
		{`import module namespace m = "urn:x"; 1`, "import"},
		{`typeswitch(1) case xs:integer return 1 default return 2`, "typeswitch"},
		{`try { 1 } catch * { 2 }`, "try"},
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

// The prolog was refused by name until it was implemented. These two are what
// that refusal used to cover, kept as a check that the boundary moved rather
// than as a regression test for a message.
func TestPrologIsAccepted(t *testing.T) {
	for _, src := range []string{
		`declare namespace p = "urn:x"; <p:a/>`,
		`xquery version "3.1"; 1`,
		`declare variable $x := 2; $x`,
		`declare function local:f($n as xs:integer) as xs:integer { $n * 2 };
		 local:f(21)`,
	} {
		if _, err := run(t, src, xquery.Options{}); err != nil {
			t.Errorf("%s: %v", src, err)
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
