package xslt

import (
	"strings"
	"testing"
)

// htmlIndentSheet builds a stylesheet whose only job is to emit body verbatim
// under the named output method with indent="yes".
func htmlIndentSheet(method, extra, body string) string {
	ns := ""
	if method == "xhtml" {
		ns = ` xmlns="http://www.w3.org/1999/xhtml"`
	}
	return `<xsl:stylesheet version="2.0" ` +
		`xmlns:xsl="http://www.w3.org/1999/XSL/Transform"` + ns + `>` +
		`<xsl:output method="` + method + `" indent="yes" ` +
		`omit-xml-declaration="yes"` + extra + `/>` +
		`<xsl:template match="/">` + body + `</xsl:template></xsl:stylesheet>`
}

// TestHTMLIndentDoesNotEnterPreOrTextarea is the load-bearing test in this
// file.
//
// The html and xhtml output methods with indent="yes" were adding newlines and
// leading spaces inside <pre> and <textarea>. Serialization 3.1 §5 lets a
// serialiser add whitespace "only where the effect is not significant" -- the
// clause the indentation code cites for its own licence -- and inside these
// elements it is significant to every user agent that renders them. A
// highlighted code block came out as
//
//	<pre>
//	  <span>func</span>
//	  <span> main</span>
//	</pre>
//
// which renders as three lines where the source said "func main", and inside a
// <textarea> the inserted whitespace becomes part of the string the control
// submits.
//
// The bug only reached element-only content: an element with a
// non-whitespace text child is already spared by the mixed-content rule, so
// <pre>a\nb</pre> was never at risk. Element-only content is what a syntax
// highlighter or a DocBook-style stylesheet emits, which is why it was worth
// finding.
//
// The assertions are on the OUTPUT TEXT. Nothing here errored before the fix;
// it produced plausible-looking markup that rendered differently.
func TestHTMLIndentDoesNotEnterPreOrTextarea(t *testing.T) {
	body := `<html><body>` +
		`<pre class="p"><span>func</span><span> main</span><span>()</span></pre>` +
		`<textarea><span>a</span><span>b</span></textarea>` +
		`<listing><span>x</span><span>y</span></listing>` +
		`<xmp><span>x</span><span>y</span></xmp>` +
		`<plaintext><span>x</span><span>y</span></plaintext>` +
		`<p><span>a</span><span>b</span></p>` +
		`</body></html>`

	for _, method := range []string{"html", "xhtml"} {
		out := run(t, htmlIndentSheet(method, "", body), `<r/>`)

		// The five whitespace-preserving elements come out on one line, with
		// their children flush against the tags.
		for _, want := range []string{
			`<pre class="p"><span>func</span><span> main</span><span>()</span></pre>`,
			`<textarea><span>a</span><span>b</span></textarea>`,
			`<listing><span>x</span><span>y</span></listing>`,
			`<xmp><span>x</span><span>y</span></xmp>`,
			`<plaintext><span>x</span><span>y</span></plaintext>`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("method=%s: want %s in output, got:\n%s",
					method, want, out)
			}
		}

		// Indentation is still on everywhere else. Without this the test
		// would also pass with indentation turned off altogether, which
		// would be a different and worse bug.
		if !strings.Contains(out, "<p>\n") {
			t.Errorf("method=%s: <p> should still be indented, got:\n%s",
				method, out)
		}
		if !strings.Contains(out, "<body>\n") {
			t.Errorf("method=%s: <body> should still be indented, got:\n%s",
				method, out)
		}
	}
}

// TestXMLIndentStillEntersPre pins the exclusion, so that nobody later
// "completes" the fix by applying it to every method.
//
// The xml method serialises a tree carrying no HTML semantics. There <pre> is
// an ordinary element name that happens to be spelled like HTML's, and
// indenting inside it is correct -- the string value of an xml:space-less
// element is not something the serialiser is asked to preserve. A caller who
// does mean HTML's <pre> under the xml method says so with
// suppress-indentation or xml:space="preserve", and the second half of this
// test checks both of those still work.
func TestXMLIndentStillEntersPre(t *testing.T) {
	body := `<doc><pre><span>a</span><span>b</span></pre></doc>`
	out := run(t, htmlIndentSheet("xml", "", body), `<r/>`)
	if strings.Contains(out, "<pre><span>") {
		t.Errorf("the xml method must still indent inside <pre>, got:\n%s", out)
	}
	if !strings.Contains(out, "<pre>\n") {
		t.Errorf("the xml method must still indent inside <pre>, got:\n%s", out)
	}

	// suppress-indentation is the caller's way of saying so, and still is.
	out = run(t, htmlIndentSheet("xml", ` suppress-indentation="pre"`, body),
		`<r/>`)
	if !strings.Contains(out, `<pre><span>a</span><span>b</span></pre>`) {
		t.Errorf("suppress-indentation=\"pre\" should suppress, got:\n%s", out)
	}

	// So is xml:space="preserve".
	out = run(t, htmlIndentSheet("xml", "",
		`<doc><pre xml:space="preserve"><span>a</span><span>b</span></pre></doc>`),
		`<r/>`)
	if !strings.Contains(out, `<span>a</span><span>b</span>`) ||
		strings.Contains(out, "<span>a</span>\n") {
		t.Errorf("xml:space=\"preserve\" should suppress, got:\n%s", out)
	}
}

// TestHTMLPreIndentIsCaseInsensitiveButXHTMLIsNot follows the rule the rest of
// the serialiser already applies to element names.
//
// HTML's names are case-insensitive, so the html method must recognise <PRE>;
// the xhtml method produces XML, where <PRE> and <pre> are different elements
// and only the second is HTML's. suppressed() already draws this line for the
// suppress-indentation parameter, and the built-in set has to draw it in the
// same place or the two would disagree about the same document.
func TestHTMLPreIndentIsCaseInsensitiveButXHTMLIsNot(t *testing.T) {
	body := `<html><body><PRE><span>a</span><span>b</span></PRE></body></html>`

	out := run(t, htmlIndentSheet("html", "", body), `<r/>`)
	if !strings.Contains(out, `<PRE><span>a</span><span>b</span></PRE>`) {
		t.Errorf("the html method should recognise <PRE>, got:\n%s", out)
	}

	out = run(t, htmlIndentSheet("xhtml", "", body), `<r/>`)
	if !strings.Contains(out, "<PRE>\n") {
		t.Errorf("the xhtml method should treat <PRE> as an ordinary "+
			"element, got:\n%s", out)
	}
}

// TestHTMLPreIndentSuppressionCoversTheSubtree checks that suppression reaches
// grandchildren, not just the immediate children.
//
// The existing suppress-indentation path says so explicitly -- "the point is
// that the content comes out as it went in, and re-indenting a grandchild
// disturbs it exactly as much as re-indenting a child" -- and a built-in
// entry that only reached one level would leave the nested case broken, which
// is exactly the shape a highlighter emits.
func TestHTMLPreIndentSuppressionCoversTheSubtree(t *testing.T) {
	body := `<html><body><pre><span><b>a</b><i>b</i></span><span>c</span></pre>` +
		`</body></html>`
	out := run(t, htmlIndentSheet("html", "", body), `<r/>`)
	want := `<pre><span><b>a</b><i>b</i></span><span>c</span></pre>`
	if !strings.Contains(out, want) {
		t.Errorf("suppression should cover the whole subtree, want %s, got:\n%s",
			want, out)
	}
}
