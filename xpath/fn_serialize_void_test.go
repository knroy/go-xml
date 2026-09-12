package xpath

import "testing"

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
		// HTML 4 only. Void with no version given, which defaults to 4.
		{"frame", "", "<frame>"},
		{"frame", "4.0", "<frame>"},
		{"frame", "5.0", "<frame></frame>"},
		// HTML 5 only.
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
// that happens to use an HTML name.
func TestSerializeXMLKeepsSelfClosing(t *testing.T) {
	for _, method := range []string{"xml", "xhtml"} {
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
