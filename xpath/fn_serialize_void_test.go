package xpath

import (
	"strings"
	"testing"
)

// TestSerializeHTMLVoidElements pins that the html output method writes
// HTML's empty-element syntax rather than XML's.
//
// HTML has no self-closing syntax. A void element takes no end tag, and every
// other empty element takes an explicit one -- an HTML parser reads "<div/>"
// as an unclosed "<div>" and swallows the rest of the document into it. This
// serialiser had no void-element table at all and wrote "/>" for every empty
// element under every method, so serialize(<br/>, map{"method":"html"})
// returned "<br/>": a "/" no HTML parser acts on, from a method whose whole
// purpose is to be read by one. xsl:result-document, which carries the table
// in xslt/serialize.go, answered "<br>" for the same document -- one request,
// two answers, decided by which spelling the caller used.
func TestSerializeHTMLVoidElements(t *testing.T) {
	for _, tc := range []struct {
		name, expr, want string
	}{
		// A void element takes no end tag and no slash.
		{"void", `parse-xml('<br/>')`, "<br>"},
		// A non-void empty element still takes a full end tag: this is the
		// half that "<br/>" and "<div/>" share a bug in but not an answer.
		{"nonvoid", `parse-xml('<div/>')`, "<div></div>"},
		// Attributes are written as usual, and the tag still ends bare.
		{"void with attributes",
			`parse-xml('<img src="a.png" alt="x"/>')`,
			`<img src="a.png" alt="x">`},
		// The two rules meeting inside one document, which is where a
		// serialiser that applied only one of them shows it.
		{"mixed", `parse-xml('<p><br/>x<span/></p>')`,
			"<p><br>x<span></span></p>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalSerialize(t,
				`serialize(`+tc.expr+`, map{'method':'html'})`)
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			if got != tc.want {
				t.Errorf("serialize html = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSerializeHTMLVoidElementsAreVersioned pins that html-version selects
// which void-element list applies, rather than one merged list answering
// both versions.
//
// Seven names are version-specific: basefont, frame and isindex are void in
// HTML 4 and absent from HTML 5, while keygen, source, track and wbr are void
// in HTML 5 and unknown to HTML 4. A serialiser with one combined list writes
// "<frame>" unclosed under HTML 5, leaving an element an HTML 5 parser reads
// as still open. xslt/serialize.go's isVoidElement splits them for exactly
// this reason; this is its twin, and html-version was on the accept-and-drop
// list, so there was nothing to split on.
func TestSerializeHTMLVoidElementsAreVersioned(t *testing.T) {
	for _, tc := range []struct {
		element, version, want string
	}{
		// HTML 4 only. With no version given the map form's default is 5
		// (F&O 3.1 fn:serialize, the parameter table: "html-version
		// xs:decimal? 5"), so frame is not void there.
		{"frame", "", "<frame></frame>"},
		{"frame", "4.0", "<frame>"},
		{"frame", "5.0", "<frame></frame>"},
		// HTML 5 only.
		{"wbr", "", "<wbr>"},
		{"wbr", "4.0", "<wbr></wbr>"},
		{"wbr", "5.0", "<wbr>"},
		// Void in both, so the version must not move it.
		{"br", "4.0", "<br>"},
		{"br", "5.0", "<br>"},
	} {
		name := tc.element + "/" + tc.version
		t.Run(name, func(t *testing.T) {
			params := `map{'method':'html'`
			if tc.version != "" {
				params += `,'html-version':'` + tc.version + `'`
			}
			params += `}`
			got, err := evalSerialize(t,
				`serialize(parse-xml('<`+tc.element+`/>'), `+params+`)`)
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			if got != tc.want {
				t.Errorf("serialize html-version=%q = %q, want %q",
					tc.version, got, tc.want)
			}
		})
	}
}

// TestSerializeXMLKeepsSelfClosing pins that the fix is scoped to the html
// method: the xml method's empty-element syntax is "<br/>", and a
// void-element table applied to it would have rewritten every XML document
// that happens to use an HTML name. The xhtml method has its own rule; see
// TestSerializeXHTMLEmptyElements.
func TestSerializeXMLKeepsSelfClosing(t *testing.T) {
	for _, method := range []string{"xml"} {
		got, err := evalSerialize(t,
			`serialize(parse-xml('<br/>'), map{'method':'`+method+`'})`)
		if err != nil {
			t.Fatalf("serialize %s: %v", method, err)
		}
		if got != "<br/>" {
			t.Errorf("serialize %s = %q, want %q", method, got, "<br/>")
		}
	}
}

// TestSerializeXHTMLEmptyElements pins Serialization 3.1 §6 for the xhtml
// method: a void element is written "<br />", any other empty element with
// a full end tag, never "<p/>". Void names count only for the XHTML
// namespace, or no namespace under HTML5 -- xslt/serialize.go's rule.
func TestSerializeXHTMLEmptyElements(t *testing.T) {
	const doc = `parse-xml('<html xmlns="http://www.w3.org/1999/xhtml"><br/><p/></html>')`
	const elemParams = `parse-xml('<output:serialization-parameters ` +
		`xmlns:output="http://www.w3.org/2010/xslt-xquery-serialization">` +
		`<output:method value="xhtml"/><output:omit-xml-declaration value="yes"/>` +
		`</output:serialization-parameters>')/*`
	const xhtmlOut = `<html xmlns="http://www.w3.org/1999/xhtml"><br /><p></p></html>`
	for _, tc := range []struct{ name, expr, want string }{
		// The map form defaults html-version to 5, which also asks for the
		// bare XHTML5 doctype (§6.1.7); the element form keeps xsl:output's
		// xhtml default, below 5.
		{"map form", `serialize(` + doc + `, map{'method':'xhtml'})`, "<!DOCTYPE html>\n" + xhtmlOut},
		{"element form", `serialize(` + doc + `, ` + elemParams + `)`, xhtmlOut},
		{"no namespace html5", `serialize(parse-xml('<r><br/><p/></r>'), ` +
			`map{'method':'xhtml','html-version':5})`, `<r><br /><p></p></r>`},
		{"no namespace html4", `serialize(parse-xml('<r><br/></r>'), ` +
			`map{'method':'xhtml','html-version':4})`, `<r><br></br></r>`},
		// The map form defaults html-version to 5, so a no-namespace void
		// name is recognised as HTML there too.
		{"no namespace map default", `serialize(parse-xml('<r><br/></r>'), ` +
			`map{'method':'xhtml'})`, `<r><br /></r>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalSerialize(t, tc.expr)
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			if got != tc.want {
				t.Errorf("serialize xhtml = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSerializeHTMLVersionDefault pins where the html-version default comes
// from. For the map form F&O 3.1 fn:serialize fixes it: "The default value is
// used when the map contains no entry for the parameter in question, and also
// when an entry is present, with the empty sequence as its value", and the
// table gives "html-version xs:decimal? 5". For the element form and the
// one-argument call the values of absent parameters are implementation-defined,
// and there the html method takes 5 as well, the same default xsl:output has.
// An explicit html-version 4 still gives HTML 4's void list.
func TestSerializeHTMLVersionDefault(t *testing.T) {
	const elemParams = `parse-xml('<output:serialization-parameters ` +
		`xmlns:output="http://www.w3.org/2010/xslt-xquery-serialization">` +
		`<output:method value="html"/></output:serialization-parameters>')/*`
	for _, tc := range []struct{ name, params, want string }{
		{"map absent", `map{'method':'html'}`, "<frame></frame>"},
		{"map empty", `map{'method':'html','html-version':()}`, "<frame></frame>"},
		{"map 4", `map{'method':'html','html-version':4}`, "<frame>"},
		{"element form", elemParams, "<frame></frame>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalSerialize(t,
				`serialize(parse-xml('<frame/>'), `+tc.params+`)`)
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			if got != tc.want {
				t.Errorf("serialize %s = %q, want %q", tc.params, got, tc.want)
			}
		})
	}
}

// TestSerializeXHTML5Doctype pins Serialization 3.1 §6.1.7: under
// html-version 5 with no doctype-system, the xhtml method writes
// "<!DOCTYPE " + the local name + ">" before a document element that is an
// HTML element named html without regard to case -- the XHTML namespace or
// no namespace -- and nothing before any other element.
func TestSerializeXHTML5Doctype(t *testing.T) {
	const ns = `xmlns="http://www.w3.org/1999/xhtml"`
	for _, tc := range []struct{ name, doc, params, want string }{
		{"xhtml namespace", `<html ` + ns + `/>`, `'html-version':5`,
			`<!DOCTYPE html>` + "\n" + `<html ` + ns + `></html>`},
		{"map default is 5", `<html ` + ns + `/>`, ``,
			`<!DOCTYPE html>` + "\n" + `<html ` + ns + `></html>`},
		{"case kept", `<HtMl/>`, `'html-version':5`,
			`<!DOCTYPE HtMl>` + "\n" + `<HtMl></HtMl>`},
		{"prefix dropped", `<h:html xmlns:h="http://www.w3.org/1999/xhtml"/>`,
			`'html-version':5`, "<!DOCTYPE html>\n"},
		{"html 4", `<html ` + ns + `/>`, `'html-version':4`, `<html ` + ns + `></html>`},
		{"not html", `<body ` + ns + `/>`, `'html-version':5`, `<body ` + ns + `></body>`},
		{"foreign namespace", `<html xmlns="urn:x"/>`, `'html-version':5`,
			`<html xmlns="urn:x"></html>`},
		{"doctype-system wins", `<html/>`, `'html-version':5,'doctype-system':'s.dtd'`,
			`<!DOCTYPE html SYSTEM "s.dtd">` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := `map{'method':'xhtml'`
			if tc.params != "" {
				params += `,` + tc.params
			}
			got, err := evalSerialize(t,
				`serialize(parse-xml('`+tc.doc+`'), `+params+`})`)
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			if !strings.HasPrefix(got, tc.want) {
				t.Errorf("serialize %s = %q, want prefix %q", params, got, tc.want)
			}
		})
	}
}

// TestSerializeHTMLDoctypeNeedsHTML5 pins two conditions on the html method's
// bare "<!DOCTYPE html>" (Serialization 3.1 §7.4.6): the requested HTML
// version is 5, so html-version 4 writes none; and "any text node preceding
// that element in document order contains only whitespace characters", which
// an item-separator ahead of the element is, once normalisation makes it text.
func TestSerializeHTMLDoctypeNeedsHTML5(t *testing.T) {
	for _, tc := range []struct{ name, expr, want string }{
		{"html 5", `serialize(parse-xml('<html/>'), map{'method':'html'})`,
			"<!DOCTYPE html>\n<html></html>"},
		{"html 4", `serialize(parse-xml('<html/>'), map{'method':'html','html-version':4})`,
			"<html></html>"},
		{"separator text before", `serialize((parse-xml('<!--c--><x/>')/comment(), ` +
			`parse-xml('<html/>')/*), map{'method':'html','item-separator':'-'})`,
			"<!--c-->-<html></html>"},
		{"blank separator before", `serialize((parse-xml('<!--c--><x/>')/comment(), ` +
			`parse-xml('<html/>')/*), map{'method':'html','item-separator':' '})`,
			"<!--c--> <!DOCTYPE html>\n<html></html>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalSerialize(t, tc.expr)
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			if got != tc.want {
				t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
			}
		})
	}
}
