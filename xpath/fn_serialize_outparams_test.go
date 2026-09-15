package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestSerializeDoctypeParameters pins fn:serialize to doctype-system and
// doctype-public.
//
// XSLT 2.0 section 20, testdata/xslt30-test/specs/xslt-rec20.xml:26420-26431:
// "The value of the doctype-system attribute provides the value of the
// doctype-system parameter to the serialization method", and the same for
// doctype-public. Both names sat in an accept-and-ignore arm, so
// serialize(<a/>, map{"doctype-system":"a.dtd"}) returned "<a/>" and the
// caller's DTD reference vanished without an error.
func TestSerializeDoctypeParameters(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params string
		want   string
	}{
		{
			"system only",
			`'doctype-system':'a.dtd'`,
			`<!DOCTYPE a SYSTEM "a.dtd">` + "\n" + `<a/>`,
		},
		{
			"public and system",
			`'doctype-system':'a.dtd','doctype-public':'-//X//EN'`,
			`<!DOCTYPE a PUBLIC "-//X//EN" "a.dtd">` + "\n" + `<a/>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalSerialize(t,
				`serialize(parse-xml('<a/>'), map{'method':'xml',`+tc.params+`})`)
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	// The element form must answer the same request the same way.
	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	c := ctx.WithVar(xdm.QName{Local: "p"}, xdm.One(paramsElement(
		map[string]string{"doctype-system": "a.dtd"})))
	seq, err := Eval(`serialize(parse-xml('<a/>'), $p)`, c, nil)
	if err != nil {
		t.Fatalf("element form: %v", err)
	}
	want := `<!DOCTYPE a SYSTEM "a.dtd">` + "\n" + `<a/>`
	if got := seq[0].(*xdm.Atomic).String(); got != want {
		t.Errorf("element form = %q, want %q", got, want)
	}
}

// TestSerializeNormalizationForm keeps fn:serialize aligned with xsl:output:
// normalization applies to ordinary text, but not to a character-map result.
func TestSerializeNormalizationForm(t *testing.T) {
	got, err := evalSerialize(t,
		`serialize(parse-xml('<a>c&#807;</a>'), map{'normalization-form':'NFC'})`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "ç") {
		t.Errorf("normalization-form=NFC gave %q, want composed text", got)
	}
	if _, err := evalSerialize(t,
		`serialize(parse-xml('<a/>'), map{'normalization-form':'bogus'})`); err == nil {
		t.Error("unsupported normalization-form was accepted")
	}
}

// TestSerializeEscapeURIAttributes pins escape-uri-attributes, whose default
// is yes.
//
// xslt-rec20.xml:26434-26439: "The value of the escape-uri-attributes
// attribute provides the value of the escape-uri-attributes parameter to the
// serialization method. The default value is yes." The parameter was accepted
// and ignored and the attribute writer called escapeAttr with no URI
// handling, while xslt/serialize.go percent-escaped the same attribute -- so
// the two serialisers disagreed on identical input.
func TestSerializeEscapeURIAttributes(t *testing.T) {
	// Default: on, without the parameter being mentioned at all.
	got, err := evalSerialize(t,
		`serialize(parse-xml('<a href="h&#233;.html"/>'), map{'method':'html'})`)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if !strings.Contains(got, "%C3%A9") {
		t.Errorf("the default did not percent-escape a URI attribute: %q", got)
	}

	// An explicit no turns it off, which is the only way to tell the default
	// apart from a serialiser that always escapes.
	off, err := evalSerialize(t,
		`serialize(parse-xml('<a href="h&#233;.html"/>'),`+
			` map{'method':'html','escape-uri-attributes':false()})`)
	if err != nil {
		t.Fatalf("serialize with escaping off: %v", err)
	}
	if strings.Contains(off, "%C3%A9") {
		t.Errorf("escape-uri-attributes=no still escaped: %q", off)
	}
	if !strings.Contains(off, "é") {
		t.Errorf("the unescaped value was lost: %q", off)
	}

	// Only URI-valued attributes are touched. A title is not one, so the
	// same character survives unescaped beside an href that does not.
	plain, err := evalSerialize(t,
		`serialize(parse-xml('<a title="h&#233;.html"/>'), map{'method':'html'})`)
	if err != nil {
		t.Fatalf("serialize non-URI attribute: %v", err)
	}
	if strings.Contains(plain, "%C3%A9") {
		t.Errorf("a non-URI attribute was percent-escaped: %q", plain)
	}

	// The xml method escapes nothing: the rule belongs to html and xhtml.
	asXML, err := evalSerialize(t,
		`serialize(parse-xml('<a href="h&#233;.html"/>'), map{'method':'xml'})`)
	if err != nil {
		t.Fatalf("serialize as xml: %v", err)
	}
	if strings.Contains(asXML, "%C3%A9") {
		t.Errorf("the xml method percent-escaped an attribute: %q", asXML)
	}
}

// TestSerializeSuppressIndentation pins suppress-indentation in BOTH forms.
//
// The parameter names elements whose content is written as it stands. The
// named element is still indented where it sits; only what is inside it is
// left alone. QT3 serialize-xml-008 (element form) and serialize-xml-108 (map
// form) assert exactly that pair: with suppress-indentation="p",
// matches($result,'\n\s+<p>') is true and matches($result,'\n\s+<code>') is
// false.
//
// The map form had the name on its accept-and-ignore list while the element
// form acted on it, so the same request answered differently depending on
// which spelling was used. The element form was not right either: it
// collapsed any non-empty list to "suppress the whole document", which
// stops <p> itself being indented and fails the first assertion.
func TestSerializeSuppressIndentation(t *testing.T) {
	const doc = `<doc><title>T</title><p><code>c</code></p></doc>`

	// Without the parameter, <code> is indented: this is the control that
	// proves the assertions below are about the parameter doing something.
	none, err := evalSerialize(t,
		`serialize(parse-xml('`+doc+`'), map{'method':'xml','indent':true()})`)
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if !strings.Contains(none, "\n    <code>") {
		t.Fatalf("control did not indent <code>, so the test proves nothing: %q", none)
	}

	mapForm, err := evalSerialize(t,
		`serialize(parse-xml('`+doc+`'),`+
			` map{'method':'xml','indent':true(),'suppress-indentation':QName('','p')})`)
	if err != nil {
		t.Fatalf("map form: %v", err)
	}
	assertSuppressed(t, "map form", mapForm)

	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	c := ctx.WithVar(xdm.QName{Local: "p"}, xdm.One(paramsElement(
		map[string]string{"indent": "yes", "suppress-indentation": "p"})))
	seq, err := Eval(`serialize(parse-xml('`+doc+`'), $p)`, c, nil)
	if err != nil {
		t.Fatalf("element form: %v", err)
	}
	elemForm := seq[0].(*xdm.Atomic).String()
	assertSuppressed(t, "element form", elemForm)

	// The defect was that the two forms disagreed, so the property worth
	// holding is that they agree.
	if mapForm != elemForm {
		t.Errorf("map form = %q, element form = %q, want the same", mapForm, elemForm)
	}
}

// assertSuppressed checks the two halves QT3 serialize-xml-008 and -108 ask
// for: the named element is still indented, its child is not.
func assertSuppressed(t *testing.T, form, got string) {
	t.Helper()
	if !strings.Contains(got, "\n  <p>") {
		t.Errorf("%s: the named element lost its own indentation: %q", form, got)
	}
	if strings.Contains(got, "\n    <code>") {
		t.Errorf("%s: content inside the named element was indented: %q", form, got)
	}
}

// TestSerializeSuppressIndentationOnlyNamedElements pins that the parameter
// suppresses the named elements and nothing else -- the reading a single
// document-wide bool could not express.
func TestSerializeSuppressIndentationOnlyNamedElements(t *testing.T) {
	got, err := evalSerialize(t,
		`serialize(parse-xml('<doc><keep><x/></keep><p><code>c</code></p></doc>'),`+
			` map{'method':'xml','indent':true(),'suppress-indentation':QName('','p')})`)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if !strings.Contains(got, "\n    <x/>") {
		t.Errorf("an element that was not named lost its indentation: %q", got)
	}
	if strings.Contains(got, "\n    <code>") {
		t.Errorf("the named element's content was indented: %q", got)
	}
}

// TestSerializeIncludeContentType pins the parameter fn:serialize accepted and
// discarded. xsl:output has honoured it throughout -- the head branch of
// writeElement in xslt/serialize.go -- so the two serializers were answering
// the same request differently, which is the defect shape this library keeps
// producing wherever one feature has two parameter-parsing paths.
func TestSerializeIncludeContentType(t *testing.T) {
	const doc = `parse-xml('<html><head/><body/></html>')`
	for _, tc := range []struct {
		name, query string
		wantMeta    bool
	}{
		{"default", `serialize(` + doc + `, map{'method':'html'})`, true},
		{"map false", `serialize(` + doc + `, map{'method':'html','include-content-type':false()})`, false},
		{"map true", `serialize(` + doc + `, map{'method':'html','include-content-type':true()})`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalSerialize(t, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if has := strings.Contains(got, "http-equiv"); has != tc.wantMeta {
				t.Errorf("serialize gave %q; content-type meta present=%v, want %v",
					got, has, tc.wantMeta)
			}
		})
	}
}
