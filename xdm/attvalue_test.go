package xdm

import (
	"io"
	"strings"
	"testing"
)

// chunkReader hands out at most n bytes per Read, so that the normalizer is
// exercised across every place a read boundary can fall. A delimiter split
// across two reads is the failure mode this filter is most exposed to.
type chunkReader struct {
	s    string
	i, n int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.i >= len(c.s) {
		return 0, io.EOF
	}
	k := c.n
	if k > len(p) {
		k = len(p)
	}
	if c.i+k > len(c.s) {
		k = len(c.s) - c.i
	}
	copy(p, c.s[c.i:c.i+k])
	c.i += k
	return k, nil
}

// attValues parses src, read n bytes at a time, and returns the attribute
// values of every element in document order, joined by "|".
func attValues(t *testing.T, src string, n int) string {
	t.Helper()
	tree, err := Parse(&chunkReader{s: src, n: n}, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	var vals []string
	var walk func(*Node)
	walk = func(n *Node) {
		for _, a := range n.Attrs {
			vals = append(vals, a.Value)
		}
		for _, c := range n.ChildElements() {
			walk(c)
		}
	}
	walk(tree.Root)
	return strings.Join(vals, "|")
}

// TestAttributeValueNormalization is XML 1.0 section 3.3.3 as the tokeniser
// applies it: a literal TAB, LF or CR in an attribute value becomes one
// space, a character reference to one does not, and nothing outside a value
// is touched. Every read size from 1 to 20 bytes is tried, so that a value,
// a CR-LF or a delimiter split across reads is covered.
func TestAttributeValueNormalization(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"newline", "<a s=\"x\ny\"/>", "x y"},
		{"tab", "<a s=\"x\ty\"/>", "x y"},
		{"cr lf is one space", "<a s=\"x\r\ny\"/>", "x y"},
		{"lone cr", "<a s=\"x\ry\"/>", "x y"},
		{"single quotes", "<a s='x\ny'/>", "x y"},
		{"char ref survives", "<a s=\"x&#10;y\"/>", "x\ny"},
		{"hex char ref survives", "<a s=\"x&#xA;y&#9;&#xD;\"/>", "x\ny\t\r"},
		{"between attributes", "<a\ns=\"1\"\nt=\"2\"/>", "1|2"},
		{"comment", "<!-- a\nb --><c s=\"m\nn\"/>", "m n"},
		{"comment with quote", "<!-- \" --><c s=\"m\nn\"/>", "m n"},
		{"cdata", "<r><a><![CDATA[q\"\nz]]></a><b s=\"m\nn\"/></r>", "m n"},
		{"pi", "<?pi x=\"a\nb\"?><c s=\"m\nn\"/>", "m n"},
		{"internal subset", "<!DOCTYPE r [<!ENTITY e \"a\nb\">]><r s=\"m\nn\"/>", "m n"},
		{"system id holding gt", "<!DOCTYPE r SYSTEM \"a>b\"><r s=\"m\nn\"/>", "m n"},
		// The byte pre-pass this replaced lost its place on these: a quote
		// or "[" in a comment in the internal subset left every later
		// attribute value unnormalized.
		{"subset comment with apostrophe", "<!DOCTYPE r [<!-- don't -->]><r s=\"m\nn\"/>", "m n"},
		{"subset comment with bracket", "<!DOCTYPE r [<!-- [ -->]><r s=\"m\nn\"/>", "m n"},
		{"utf-8 neighbour", "<a s=\"é\ny\"/>", "é y"},
		// XML 1.1 §2.11 makes NEL, U+2028 and CR NEL line ends, folded
		// before §3.3.3 applies, so each is one space. Under 1.0 NEL and
		// U+2028 are ordinary characters; the CR before a NEL is a line end.
		{"1.1 nel", "<?xml version=\"1.1\"?><a s=\"1\r\u00852\" t=\"1\u00852\" u=\"1\u20282\" v=\"1\r\n\u00852\"/>", "1 2|1 2|1 2|1  2"},
		{"1.1 nel by reference", "<?xml version=\"1.1\"?><a s=\"1&#x85;2&#x2028;3\"/>", "1\u00852\u20283"},
		{"1.0 nel", "<a s=\"1\r\u00852\" t=\"1\u00852\u20283\"/>", "1 \u00852|1\u00852\u20283"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for n := 1; n <= 20; n++ {
				if got := attValues(t, c.src, n); got != c.want {
					t.Fatalf("read size %d: got %q, want %q", n, got, c.want)
				}
			}
			if got := attValues(t, c.src, len(c.src)+1); got != c.want {
				t.Fatalf("whole: got %q, want %q", got, c.want)
			}
		})
	}
}

// TestPositionsCountAttributeNewlines: the newlines in a multi-line attribute
// value are lines of the source, so a node after one is reported on its real
// line. The byte pre-pass that used to normalize values rewrote them in the
// retained source too, and reported <b/> below on line 2.
func TestPositionsCountAttributeNewlines(t *testing.T) {
	tree, err := ParseString("<a x=\"1\n2\n3\">\n<b/></a>", ParseOptions{TrackPositions: true})
	if err != nil {
		t.Fatal(err)
	}
	line, col, ok := tree.Root.ChildElements()[0].ChildElements()[0].Position()
	if !ok || line != 4 || col != 1 {
		t.Fatalf("got line %d col %d ok %v, want line 4 col 1", line, col, ok)
	}
}

// TestParseNormalizesAttributeValues checks the rewrite through the parser,
// which is where it has to hold: this is what boolean-082 in the XSLT suite
// asserts, a style attribute wrapped across two lines.
func TestParseNormalizesAttributeValues(t *testing.T) {
	tree, err := ParseString("<td style=\"color: #336699; font-weight:\nbold\"/>",
		ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	el := tree.Root.ChildElements()[0]
	if got, want := el.Attrs[0].Value, "color: #336699; font-weight: bold"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// The reference is not normalized, which is the half a naive
	// strings.Replace on the decoded value would get wrong.
	tree, err = ParseString("<a s=\"x&#10;y\"/>", ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.Root.ChildElements()[0].Attrs[0].Value; got != "x\ny" {
		t.Fatalf("character reference not preserved: got %q", got)
	}
}

// TestEntityReplacementInAttributeValue is XML 1.0 §3.3.3's recursion: an
// entity reference in an attribute value is replaced by its replacement text
// normalized the same way, so the literal white space in that text becomes a
// space, while a character the replacement text holds as a reference is kept.
// The rows are the section's own example, whose declarations put a CR and an
// LF into replacement text through references in the entity value, and
// Appendix D's "&#38;#..." form, which leaves a reference in replacement text.
// Each runs on both entity paths: dec.Entity, and the re-parse a markup
// entity forces.
func TestEntityReplacementInAttributeValue(t *testing.T) {
	const decls = `<!ENTITY d "&#xD;"><!ENTITY a "&#xA;"><!ENTITY da "&#xD;&#xA;">` +
		`<!ENTITY ref "&#38;#xA;"><!ENTITY amp2 "&#38;#38;"><!ENTITY n "x&a;y">` +
		"<!ENTITY lit \"p\tq\nr\">"
	cases := []struct{ name, attr, want string }{
		{"literal line ends", "\n\nxyz", "  xyz"},
		{"section 3.3.3 entities", "&d;&d;A&a;&#x20;&a;B&da;", "  A   B  "},
		{"section 3.3.3 references", "&#xd;&#xd;A&#xa;&#xa;B&#xd;&#xa;", "\r\rA\n\nB\r\n"},
		{"reference in replacement text", "&ref;", "\n"},
		{"appendix D ampersand", "&amp2;", "&"},
		{"nested", "&n;", "x y"},
		{"literal white space in entity value", "&lit;", "p q r"},
	}
	for _, markup := range []bool{false, true} {
		extra := ""
		if markup {
			extra = `<!ENTITY m "<b/>">`
		}
		for _, c := range cases {
			src := "<!DOCTYPE r [" + decls + extra + "]><r v=\"" + c.attr + "\">&a;</r>"
			tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
			if err != nil {
				t.Fatalf("%s (markup path %v): %v", c.name, markup, err)
			}
			r := tree.Root.ChildElements()[0]
			if got := r.Attrs[0].Value; got != c.want {
				t.Errorf("%s (markup path %v): got %q, want %q", c.name, markup, got, c.want)
			}
			// In content the replacement text is not normalized.
			if got := r.StringValue(); got != "\n" {
				t.Errorf("%s (markup path %v): content %q, want %q", c.name, markup, got, "\n")
			}
		}
	}
}

// TestAttributeDefaultNormalization: an ATTLIST default is an attribute value
// like any other (XML 1.0 §3.3.2), so §3.3.3 applies to it — character and
// entity references are replaced, literal white space becomes a space, and a
// non-CDATA type collapses — and the well-formedness constraints on
// references in it hold (§3.1 "No < in Attribute Values", §4.1 "Entity
// Declared", "No External Entity References").
func TestAttributeDefaultNormalization(t *testing.T) {
	const ents = `<!ENTITY d "&#xD;"><!ENTITY a "&#xA;"><!ENTITY da "&#xD;&#xA;">` +
		`<!ENTITY ref "&#38;#xA;"><!ENTITY n "x&a;y">`
	ok := []struct{ name, decl, want string }{
		{"literal white space", "<!ATTLIST r v CDATA \"x\ny\tz\">", "x y z"},
		{"character references", `<!ATTLIST r v CDATA "&#x20;&#xA;&#9;&#60;">`, " \n\t<"},
		{"section 3.3.3 entities", `<!ATTLIST r v CDATA "&d;&d;A&a;&#x20;&a;B&da;">`, "  A   B  "},
		{"reference in replacement text", `<!ATTLIST r v CDATA "&ref;">`, "\n"},
		{"nested", `<!ATTLIST r v CDATA "&n;">`, "x y"},
		{"predefined", `<!ATTLIST r v CDATA "&amp;&lt;&quot;">`, `&<"`},
		{"fixed", `<!ATTLIST r v CDATA #FIXED "p&a;q">`, "p q"},
		{"non-CDATA collapse", `<!ATTLIST r v NMTOKENS "  p&a;&a;q  ">`, "p q"},
	}
	for _, c := range ok {
		tree, err := ParseString("<!DOCTYPE r ["+ents+c.decl+"]><r/>", ParseOptions{AllowDOCTYPE: true})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		v := tree.Root.ChildElements()[0].Attr("", "v")
		if v == nil || v.Value != c.want {
			t.Errorf("%s: got %v, want %q", c.name, v, c.want)
		}
	}
	bad := []struct{ name, subset, want string }{
		{"literal <", `<!ATTLIST r v CDATA "a<b">`, "contains <"},
		{"bare &", `<!ATTLIST r v CDATA "a & b">`, "begins no reference"},
		{"undeclared", `<!ATTLIST r v CDATA "&u;">`, `entity "u" is not declared before`},
		{"declared after", `<!ATTLIST r v CDATA "&late;"><!ENTITY late "x">`, `entity "late" is not declared before`},
		{"declared in a PI", `<?p <!ENTITY e "x">?><!ATTLIST r v CDATA "&e;">`, `entity "e" is not declared before`},
		{"external", `<!ENTITY x SYSTEM "x.ent"><!ATTLIST r v CDATA "&x;">`, `external entity "x"`},
		{"< in replacement text", `<!ENTITY m "<b/>"><!ATTLIST r v CDATA "&m;">`, "contains <"},
		{"< nested", `<!ENTITY m "<b/>"><!ENTITY o "&m;"><!ATTLIST r v CDATA "&o;">`, "contains <"},
	}
	for _, c := range bad {
		_, err := ParseString("<!DOCTYPE r ["+c.subset+"]><r/>", ParseOptions{AllowDOCTYPE: true})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one containing %q", c.name, err, c.want)
		}
	}
	// A default supplies an omitted value only.
	tree, err := ParseString("<!DOCTYPE r [<!ATTLIST r v CDATA \"x\ny\">]><r v=\"w\"/>", ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.Root.ChildElements()[0].Attr("", "v").Value; got != "w" {
		t.Errorf("written value = %q, want w", got)
	}
}

// TestDTDLineEnds: §2.11's line-end handling covers the internal subset too,
// so an entity value or attribute default holds the line ends content does —
// under XML 1.1 NEL, U+2028 and CR NEL are each one LF, and in an attribute
// value one space; under 1.0 NEL and U+2028 are characters, and the CR before
// a NEL is a line end of its own.
func TestDTDLineEnds(t *testing.T) {
	const subset = "<!ENTITY e \"1\u00852\u20283\r\u00854\"><!ATTLIST r d CDATA \"1\u00852\u20283\r\u00854\">"
	cases := []struct{ decl, content, attr, def string }{
		{`<?xml version="1.1"?>`, "1\n2\n3\n4", "1 2 3 4", "1 2 3 4"},
		{`<?xml version="1.0"?>`, "1\u00852\u20283\n\u00854", "1\u00852\u20283 \u00854", "1\u00852\u20283 \u00854"},
	}
	for _, c := range cases {
		src := c.decl + "<!DOCTYPE r [" + subset + "]><r a=\"&e;\">&e;</r>"
		tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
		if err != nil {
			t.Fatalf("%s: %v", c.decl, err)
		}
		r := tree.Root.ChildElements()[0]
		if got := r.StringValue(); got != c.content {
			t.Errorf("%s content: got %q, want %q", c.decl, got, c.content)
		}
		if got := r.Attr("", "a").Value; got != c.attr {
			t.Errorf("%s attribute: got %q, want %q", c.decl, got, c.attr)
		}
		if got := r.Attr("", "d").Value; got != c.def {
			t.Errorf("%s default: got %q, want %q", c.decl, got, c.def)
		}
	}
}

// TestDTDCharRefsAreLegalChars: WFC Legal Character (§4.1) holds for every
// character reference, in an entity value and an attribute default as in
// content, by the document's version: 1.1 admits #x1-#x1F through a reference
// and 1.0 does not; neither admits #x0. An entity value is checked whether or
// not the entity is used.
func TestDTDCharRefsAreLegalChars(t *testing.T) {
	const v11 = `<?xml version="1.1"?>`
	cases := []struct {
		name, src string
		ok        bool
	}{
		{"1.0 entity value #x1", `<!DOCTYPE r [<!ENTITY e "&#x1;">]><r/>`, false},
		{"1.1 entity value #x1", v11 + `<!DOCTYPE r [<!ENTITY e "&#x1;">]><r/>`, true},
		{"1.1 entity value #x0", v11 + `<!DOCTYPE r [<!ENTITY e "&#0;">]><r/>`, false},
		{"1.0 entity value surrogate", `<!DOCTYPE r [<!ENTITY e "&#xD800;">]><r/>`, false},
		{"1.0 entity value bad syntax", `<!DOCTYPE r [<!ENTITY e "&#xZ;">]><r/>`, false},
		{"1.0 default #x1", `<!DOCTYPE r [<!ATTLIST r a CDATA "&#1;">]><r/>`, false},
		{"1.1 default #x1", v11 + `<!DOCTYPE r [<!ATTLIST r a CDATA "&#1;">]><r/>`, true},
		{"1.1 default #x0", v11 + `<!DOCTYPE r [<!ATTLIST r a CDATA "&#x0;">]><r/>`, false},
		{"1.0 reference in replacement text", `<!DOCTYPE r [<!ENTITY e "&#38;#1;">]><r>&e;</r>`, false},
		{"1.0 legal", `<!DOCTYPE r [<!ENTITY e "&#9;&#x10FFFF;"><!ATTLIST r a CDATA "&#xA;">]><r>&e;</r>`, true},
	}
	for _, c := range cases {
		_, err := ParseString(c.src, ParseOptions{AllowDOCTYPE: true})
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok %v", c.name, err, c.ok)
		}
		if err != nil && !c.ok && !strings.Contains(err.Error(), "character") {
			t.Errorf("%s: err = %v, want a character-reference error", c.name, err)
		}
	}
}

// TestCommentAndPILineEnds: under XML 1.1, §2.11's NEL, U+2028 and CR NEL
// line ends are folded in comments and PIs as in text — in the prolog, the
// content and the epilog alike. Under 1.0 NEL and U+2028 are characters.
func TestCommentAndPILineEnds(t *testing.T) {
	for _, c := range []struct{ decl, body, want string }{
		{`<?xml version="1.1"?>`, "a\u0085b\u2028c\r\u0085d", "a\nb\nc\nd"},
		{`<?xml version="1.0"?>`, "a\u0085b\u2028c", "a\u0085b\u2028c"},
	} {
		body := c.body
		src := c.decl + "<!--" + body + "--><r><!--" + body + "--><?p " + body + "?></r><?q " + body + "?>"
		tree, err := ParseString(src, ParseOptions{})
		if err != nil {
			t.Fatalf("%s: %v", c.decl, err)
		}
		root := tree.Root
		r := root.ChildElements()[0]
		nodes := []*Node{root.Children[0], r.Children[0], r.Children[1], root.Children[2]}
		for i, n := range nodes {
			if n.Value != c.want {
				t.Errorf("%s node %d (%v): got %q, want %q", c.decl, i, n.Kind, n.Value, c.want)
			}
		}
	}
}

// TestUndeclaredEntityWFCOrVC is XML 1.0 §4.1's split between WFC and VC:
// Entity Declared. With no DTD, with only an internal subset free of
// parameter-entity references, or with standalone="yes", a reference to an
// undeclared entity is a well-formedness error. Otherwise the declaration may
// be in an external subset or parameter entity a non-validating processor has
// not read, the error is a validity error that it does not report, and the
// reference contributes nothing — in content, in an attribute value and in a
// default alike. An entity that is declared but cannot be read stays an error.
func TestUndeclaredEntityWFCOrVC(t *testing.T) {
	const doc = `<r a="1&u;2">x&u;y</r>`
	cases := []struct {
		name, prolog string
		ok           bool
	}{
		{"no DTD", ``, false},
		{"internal subset only", `<!DOCTYPE r [<!ENTITY e "v">]>`, false},
		{"external subset", `<!DOCTYPE r SYSTEM "r.dtd">`, true},
		{"external subset and internal", `<!DOCTYPE r SYSTEM "r.dtd" [<!ENTITY e "v">]>`, true},
		{"parameter-entity reference", `<!DOCTYPE r [<!ENTITY % p ""> %p; ]>`, true},
		{"standalone with external subset", `<?xml version="1.0" standalone="yes"?><!DOCTYPE r SYSTEM "r.dtd">`, false},
		{"standalone no", `<?xml version="1.0" standalone="no"?><!DOCTYPE r SYSTEM "r.dtd">`, true},
		{"percent in a literal is no reference", `<!DOCTYPE r [<!ENTITY e "%p;">]>`, false},
	}
	for _, c := range cases {
		tree, err := ParseString(c.prolog+doc, ParseOptions{AllowDOCTYPE: true})
		if !c.ok {
			if err == nil || !strings.Contains(err.Error(), "&u;") {
				t.Errorf("%s: err = %v, want the undeclared reference refused", c.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		r := tree.Root.ChildElements()[0]
		if got := r.StringValue() + "|" + r.Attr("", "a").Value; got != "xy|12" {
			t.Errorf("%s: got %q, want the reference dropped (xy|12)", c.name, got)
		}
	}

	// Defaults follow the same rule, declaration order included.
	if _, err := ParseString(`<!DOCTYPE r [<!ATTLIST r d CDATA "1&u;2">]><r/>`, ParseOptions{AllowDOCTYPE: true}); err == nil {
		t.Error("an undeclared entity in a default was accepted under the WFC")
	}
	tree, err := ParseString(`<!DOCTYPE r SYSTEM "r.dtd" [<!ATTLIST r d CDATA "1&u;&late;2"><!ENTITY late "L">]><r/>`,
		ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.Root.ChildElements()[0].Attr("", "d").Value; got != "1L2" {
		t.Errorf("default = %q, want 1L2", got)
	}

	// Declared but external, with no resolver: still refused.
	if _, err := ParseString(`<!DOCTYPE r SYSTEM "r.dtd" [<!ENTITY x SYSTEM "x.ent">]><r>&x;</r>`,
		ParseOptions{AllowDOCTYPE: true}); err == nil {
		t.Error("a declared external entity was dropped instead of refused")
	}
}

// TestCRNELUnderXML10: XML 1.0 §2.11 knows CR LF and CR as line ends but not
// NEL, so in CR NEL the CR is a line end (LF, or a space in an attribute
// value) and the NEL an ordinary character — in text, CDATA, attribute
// values, comments, PIs and the DOCTYPE alike.
func TestCRNELUnderXML10(t *testing.T) {
	const s = "a\r\u0085b"
	src := `<?xml version="1.0"?><!DOCTYPE r [<!ENTITY e "` + s + `">]><!--` + s + `--><r a="` + s + `">` +
		s + `<![CDATA[` + s + `]]><?p ` + s + `?></r><?q ` + s + `?>`
	tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatal(err)
	}
	const lf, sp = "a\n\u0085b", "a \u0085b"
	r := tree.Root.ChildElements()[0]
	for _, c := range []struct {
		what, got, want string
	}{
		{"prolog comment", tree.Root.Children[0].Value, lf},
		{"attribute", r.Attr("", "a").Value, sp},
		{"text and CDATA", r.Children[0].Value, lf + lf},
		{"PI", r.Children[1].Value, lf},
		{"epilog PI", tree.Root.Children[2].Value, lf},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.what, c.got, c.want)
		}
	}
	tree, err = ParseString(`<!DOCTYPE r [<!ENTITY e "`+s+`">]><r a="&e;">&e;</r>`, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatal(err)
	}
	r = tree.Root.ChildElements()[0]
	if got := r.StringValue(); got != lf {
		t.Errorf("entity in content: got %q, want %q", got, lf)
	}
	if got := r.Attr("", "a").Value; got != sp {
		t.Errorf("entity in attribute: got %q, want %q", got, sp)
	}
}
