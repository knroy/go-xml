package xpath

import (
	"strings"
	"testing"
)

// TestSerializeHTMLIndentLeavesInlineElementsAlone: fn:serialize with the
// html or xhtml method and indent put <b> and <i> on lines of their own,
// which renders "bold it" with a space the document never had.
// Serialization 3.1 §7.4.3 and §6.1.4 forbid whitespace adjacent to an inline
// element; the layout is xsl:result-document's, and Saxon 12's.
func TestSerializeHTMLIndentLeavesInlineElementsAlone(t *testing.T) {
	for _, c := range []struct{ method, ns string }{
		{"html", ""},
		{"xhtml", ` xmlns="http://www.w3.org/1999/xhtml"`},
	} {
		got, err := evalSerialize(t, `serialize(parse-xml('<html`+c.ns+`><body><div>`+
			`<p><b>bold</b><i>it</i></p><span><div>x</div><div>y</div></span>`+
			`</div></body></html>'), map{'method': '`+c.method+`', 'indent': true()})`)
		if err != nil {
			t.Fatalf("%s: %v", c.method, err)
		}
		for _, want := range []string{
			"<p><b>bold</b><i>it</i></p>",
			"</p><span>\n        <div>x</div>\n        <div>y</div></span></div>",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: want %q in:\n%s", c.method, want, got)
			}
		}
	}
}
