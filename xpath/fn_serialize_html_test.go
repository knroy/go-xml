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

// TestSerializeIncludeContentTypeReplacesMeta: with include-content-type yes (the
// default), the html and xhtml methods add a content-type meta to <head> and
// discard the head's own (Serialization 3.1 §7.4.13, §6.1.14). The html method
// added its meta but kept the document's, leaving two that disagreed; the
// xhtml method added none. With "no" the document's metas stay untouched.
func TestSerializeIncludeContentTypeReplacesMeta(t *testing.T) {
	const doc = `<html><head><meta charset="UTF-8"/>` +
		`<meta http-equiv=" content-TYPE " content="text/html; charset=ISO-8859-1"/>` +
		`<title>t</title></head><body/></html>`
	for _, method := range []string{"html", "xhtml"} {
		for _, version := range []string{"", ", 'html-version': 5"} {
			for _, ict := range []string{"true", "false"} {
				got, err := evalSerialize(t, `serialize(parse-xml('`+doc+`'), map{'method': '`+
					method+`', 'include-content-type': `+ict+`()`+version+`})`)
				if err != nil {
					t.Fatalf("%s %s %s: %v", method, version, ict, err)
				}
				own := strings.Contains(got, `<meta charset="UTF-8"`) ||
					strings.Contains(got, "ISO-8859-1")
				added := strings.Count(got, `content="text/html; charset=UTF-8"`)
				if ict == "true" && (own || added != 1) {
					t.Errorf("%s%s yes: want only the added meta, got:\n%s", method, version, got)
				}
				if ict == "false" && (!strings.Contains(got, `<meta charset="UTF-8"`) ||
					!strings.Contains(got, "ISO-8859-1") || added != 0) {
					t.Errorf("%s%s no: want both document metas and none added, got:\n%s", method, version, got)
				}
			}
		}
	}
}

// TestSerializeMetaHonoursMediaType: the meta the html and xhtml methods add
// carries the media-type parameter and the encoding (§7.4.13: "The content
// type MUST be set to the value given for the media-type parameter"), as
// xsl:output's does. fn:serialize wrote text/html; charset=UTF-8 whatever it
// was asked. Both the map and the parameter-element forms are read.
func TestSerializeMetaHonoursMediaType(t *testing.T) {
	const doc = `parse-xml('<html><head/></html>')`
	for _, c := range []struct{ query, want string }{
		{`serialize(` + doc + `, map{'method':'html','media-type':'text/foo'})`,
			`<meta http-equiv="Content-Type" content="text/foo; charset=UTF-8">`},
		{`serialize(` + doc + `, map{'method':'xhtml','media-type':'a/b"c','encoding':'iso-8859-1'})`,
			`<meta http-equiv="Content-Type" content="a/b&quot;c; charset=iso-8859-1" />`},
		{`serialize(` + doc + `, map{'method':'html'})`,
			`<meta http-equiv="Content-Type" content="text/html; charset=UTF-8">`},
		{`serialize(` + doc + `, parse-xml('<output:serialization-parameters ` +
			`xmlns:output="http://www.w3.org/2010/xslt-xquery-serialization">` +
			`<output:method value="html"/><output:media-type value="text/foo"/>` +
			`</output:serialization-parameters>')/*)`,
			`<meta http-equiv="Content-Type" content="text/foo; charset=UTF-8">`},
	} {
		got, err := evalSerialize(t, c.query)
		if err != nil {
			t.Fatalf("%s: %v", c.query, err)
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s:\nwant %s in %s", c.query, c.want, got)
		}
	}
}
