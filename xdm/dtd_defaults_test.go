package xdm

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// An ATTLIST default is supplied to every matching element, including a
// namespace declaration — "xmlns:p CDATA #FIXED '...'" is how a DTD supplies
// a binding, and without it the prefix is absent from the tree.
func TestAttListDefaults(t *testing.T) {
	const src = `<!DOCTYPE svg[
<!ELEMENT svg EMPTY>
<!ATTLIST svg
          xmlns CDATA #IMPLIED
          xmlns:xlink CDATA #FIXED "http://www.w3.org/1999/xlink">
]>
<svg xmlns="http://www.w3.org/2000/svg"/>`

	tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	root := tree.Root.ChildElements()[0]
	scope := root.InScopeNamespaces()
	if got := scope["xlink"]; got != "http://www.w3.org/1999/xlink" {
		t.Errorf("xlink binding = %q, want the DTD-declared URI", got)
	}
	// The declaration written on the element is still there.
	if got := scope[""]; got != "http://www.w3.org/2000/svg" {
		t.Errorf("default namespace = %q, want the svg URI", got)
	}
}

func TestAttListPlainDefault(t *testing.T) {
	tree, err := ParseString(
		`<!DOCTYPE r [<!ATTLIST r lang CDATA "en">]><r/>`,
		ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r := tree.Root.ChildElements()[0]
	if got := r.AttrValue("lang"); got != "en" {
		t.Errorf("lang = %q, want the declared default", got)
	}
}

// A value written on the element wins: a default supplies what was omitted, it
// does not overwrite what was given.
func TestAttListDefaultDoesNotOverride(t *testing.T) {
	tree, err := ParseString(
		`<!DOCTYPE r [<!ATTLIST r lang CDATA "en">]><r lang="fr"/>`,
		ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := tree.Root.ChildElements()[0].AttrValue("lang"); got != "fr" {
		t.Errorf("lang = %q, want the document's own value", got)
	}
}

// #REQUIRED and #IMPLIED declare no value, so nothing is added.
func TestAttListNoDefaultDeclared(t *testing.T) {
	tree, err := ParseString(
		`<!DOCTYPE r [<!ATTLIST r a CDATA #REQUIRED b CDATA #IMPLIED>]><r/>`,
		ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n := len(attrsOf(tree.Root.ChildElements()[0])); n != 0 {
		t.Errorf("got %d attributes, want none", n)
	}
}

// The default applies only to the element it names.
func TestAttListDefaultIsPerElement(t *testing.T) {
	tree, err := ParseString(
		`<!DOCTYPE r [<!ATTLIST a x CDATA "1">]><r><a/><b/></r>`,
		ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	kids := tree.Root.ChildElements()[0].ChildElements()
	if got := kids[0].AttrValue("x"); got != "1" {
		t.Errorf("a/@x = %q, want 1", got)
	}
	if len(attrsOf(kids[1])) != 0 {
		t.Error("b should carry no defaulted attribute")
	}
}

// Reading ATTLIST defaults must not start expanding entities: that is the
// attack surface AllowDOCTYPE exists to gate, and none of it is opened here.
func TestAttListDefaultsExpandNothing(t *testing.T) {
	cases := []struct{ name, src string }{
		// A default whose text names an *external* entity is inserted
		// literally. Expanding it would be an XXE with extra steps.
		{"entity in default",
			`<!DOCTYPE r [<!ATTLIST r x CDATA #FIXED "&file;">` +
				`<!ENTITY file SYSTEM "file:///etc/passwd">]><r/>`},
		// A parameter entity is not read, so its external file is not either.
		{"parameter entity",
			`<!DOCTYPE r [<!ENTITY % p SYSTEM "file:///etc/passwd">%p;]><r/>`},
	}
	for _, c := range cases {
		tree, err := ParseString(c.src, ParseOptions{AllowDOCTYPE: true})
		if err != nil {
			continue // refusing outright is also fine
		}
		for _, a := range attrsOf(tree.Root.ChildElements()[0]) {
			if len(a.Value()) > 0 && a.Value()[0] != '&' {
				t.Errorf("%s: attribute %q = %q, which looks expanded",
					c.name, a.Name().Local, a.Value())
			}
		}
	}
}

// An entity reference in content is still rejected with AllowDOCTYPE set: the
// five predefined entities are the only ones that exist.
func TestDOCTYPEStillRefusesEntities(t *testing.T) {
	for _, src := range []string{
		// External: fetching this is XXE, which is the attack AllowDOCTYPE
		// exists to gate. It stays refused however the flag is set.
		`<!DOCTYPE r [<!ENTITY x SYSTEM "file:///etc/passwd">]><r>&x;</r>`,
		`<!DOCTYPE r [<!ENTITY x PUBLIC "-//x" "http://127.0.0.1/">]><r>&x;</r>`,
		// Undeclared: a conforming parser must not invent one.
		`<!DOCTYPE r [<!ENTITY a "z">]><r>&nope;</r>`,
	} {
		if _, err := ParseString(src, ParseOptions{AllowDOCTYPE: true}); err == nil {
			t.Errorf("should have been refused: %s", src)
		}
	}
}

// A malformed subset must not panic or hang; skipping what it cannot read is
// the required behaviour.
func TestAttListMalformed(t *testing.T) {
	for _, src := range []string{
		`<!DOCTYPE r [<!ATTLIST]><r/>`,
		`<!DOCTYPE r [<!ATTLIST r]><r/>`,
		`<!DOCTYPE r [<!ATTLIST r x CDATA #FIXED]><r/>`,
		`<!DOCTYPE r [<!ATTLIST r x CDATA "unterminated]><r/>`,
		`<!DOCTYPE r [<!ATTLIST r x (a|b) "a">]><r/>`,
	} {
		_, _ = ParseString(src, ParseOptions{AllowDOCTYPE: true})
	}
}

// TestNonCDATAAttributeCollapse pins XML 1.0 §3.3.3: once the declaration has
// been read, a non-CDATA value loses leading, trailing and repeated spaces,
// while a character-referenced tab and a CDATA value are left alone. It is
// Canonical XML 1.0 example 3.4's normNames/normId case.
func TestNonCDATAAttributeCollapse(t *testing.T) {
	tr, err := ParseString(`<!DOCTYPE d [<!ATTLIST d n NMTOKENS #IMPLIED i ID #IMPLIED c CDATA #IMPLIED`+
		` x NOTATION (a|b) #IMPLIED e (p|q) 'p'>]><d n="  A   B  " i=" id1 " c="  A   B  " x=" a " t="&#9;A"/>`,
		ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, a := range attrsOf(kids(tr.Root)[0]) {
		got[a.Name().Local] = a.Value()
	}
	want := map[string]string{"n": "A B", "i": "id1", "c": "  A   B  ", "x": "a", "e": "p", "t": "\tA"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// TestDefaultedNamespaceOnPrefixedElement: an ATTLIST for "p:doc" defaulting
// xmlns:p must bind the prefix the element's own name uses. It matched on the
// local name only, so the element failed the Prefix Declared check.
func TestDefaultedNamespaceOnPrefixedElement(t *testing.T) {
	tr, err := ParseString(`<!DOCTYPE p:doc [<!ATTLIST p:doc xmlns:p CDATA #FIXED "urn:p" p:a CDATA "v">]><p:doc/>`,
		ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatal(err)
	}
	d := kids(tr.Root)[0]
	if d.Name().URI != "urn:p" || len(attrsOf(d)) != 1 || attrsOf(d)[0].Name().URI != "urn:p" || attrsOf(d)[0].Value() != "v" {
		t.Fatalf("got name %v attrs %v", d.Name(), attrsOf(d))
	}
}

// XML 1.0 §3.3: when an attribute is declared more than once, the first
// declaration binds and later ones are ignored. Both defaults used to be added,
// and the element then failed as carrying a duplicate attribute (W3C xmlconf
// valid-sa-045, valid-not-sa-026, sa04, not-sa04).
func TestAttListFirstDeclarationBinds(t *testing.T) {
	cases := []struct{ name, subset, elem, attr, want string }{
		{"second default ignored", `<!ATTLIST r a CDATA "v1"><!ATTLIST r a CDATA "z1">`, `<r/>`, "a", "v1"},
		{"later default for a declared attribute ignored, new one kept",
			`<!ATTLIST r a1 CDATA "w1"><!ATTLIST r a1 CDATA "x1" a2 CDATA "x2">`, `<r/>`, "a1", "w1"},
		{"new attribute in a later list still defaulted",
			`<!ATTLIST r a1 CDATA "w1"><!ATTLIST r a1 CDATA "x1" a2 CDATA "x2">`, `<r/>`, "a2", "x2"},
		{"#IMPLIED first means no default", `<!ATTLIST r a CDATA #IMPLIED><!ATTLIST r a CDATA "z1">`, `<r/>`, "a", ""},
		{"first type binds: CDATA is not collapsed", `<!ATTLIST r a CDATA #IMPLIED><!ATTLIST r a NMTOKENS #IMPLIED>`,
			`<r a=" x  y "/>`, "a", " x  y "},
		{"same attribute on another element is separate", `<!ATTLIST r a CDATA "v1"><!ATTLIST s a CDATA "z1">`,
			`<r><s/></r>`, "a", "v1"},
	}
	for _, c := range cases {
		tr, err := ParseString(`<!DOCTYPE r [`+c.subset+`]>`+c.elem, ParseOptions{AllowDOCTYPE: true})
		if err != nil {
			t.Errorf("%s: parse: %v", c.name, err)
			continue
		}
		if got := tr.Root.ChildElements()[0].AttrValue(c.attr); got != c.want {
			t.Errorf("%s: %s = %q, want %q", c.name, c.attr, got, c.want)
		}
	}
	tr, err := ParseString(`<!DOCTYPE r [<!ATTLIST r a CDATA "v1"><!ATTLIST s a CDATA "z1">]><r><s/></r>`,
		ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.Root.ChildElements()[0].ChildElements()[0].AttrValue("a"); got != "z1" {
		t.Errorf("s/@a = %q, want z1: the first-binds rule is per element", got)
	}
}

// A default is declared once and applies to every element. Its value used to
// be copied into the tree for each element that received it, so 2 MB of input
// retained 1.9 GB (250 attributes of 8,000 bytes on 1,000 elements). The
// value is stored once and aliased: here 20 defaults of 4,000 bytes on 200
// elements need 80 KB, and 16 MB when copied.
func TestAttListDefaultValuesAreShared(t *testing.T) {
	const attrs, elems, size = 20, 200, 4000
	var dtd, doc strings.Builder
	dtd.WriteString("<!ELEMENT r ANY><!ELEMENT e EMPTY>")
	for i := 0; i < attrs; i++ {
		// Half are NMTOKENS, whose value is collapsed per element.
		typ, val := "CDATA", strings.Repeat("x", size)
		if i%2 == 1 {
			typ, val = "NMTOKENS", strings.Repeat("x ", size/2-1)+"xx"
		}
		fmt.Fprintf(&dtd, `<!ATTLIST e a%d %s "%s">`, i, typ, val)
	}
	doc.WriteString("<!DOCTYPE r [" + dtd.String() + "]><r>")
	for i := 0; i < elems; i++ {
		doc.WriteString("<e/>")
	}
	doc.WriteString("</r>")
	src := doc.String()
	var tree *Tree
	var err error
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	tree, err = ParseString(src, ParseOptions{AllowDOCTYPE: true})
	runtime.GC()
	runtime.ReadMemStats(&m1)
	if err != nil {
		t.Fatal(err)
	}
	if got := int64(m1.HeapAlloc) - int64(m0.HeapAlloc); got > 4<<20 {
		t.Errorf("tree retains %d KB, want under 4096 KB", got>>10)
	}
	n := 0
	for r := range tree.Root.Children() {
		for e := range r.Children() {
			for a := range e.Attrs() {
				if len(a.Value()) != size {
					t.Fatalf("attribute %s has %d bytes, want %d", a.Name().Local, len(a.Value()), size)
				}
				n++
			}
		}
	}
	if n != attrs*elems {
		t.Errorf("%d defaulted attributes, want %d", n, attrs*elems)
	}
	runtime.KeepAlive(tree)
}

// Each element was matched against every ATTLIST declaration, building the
// element's lexical name for each: 1.4 MB of declarations and elements took
// 11.8 s. The declarations are now indexed by element name, so the cost per
// element does not depend on how many others are declared. Measured: 5 MB
// for 2,000 declarations and 2,000 prefixed elements, against 188 MB scanning
// them all.
func TestAttListDefaultsAreIndexedByElement(t *testing.T) {
	const n = 2000
	var dtd, doc strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&dtd, `<!ATTLIST p:an-element-with-a-rather-long-name-%d d CDATA "v%d">`, i, i)
	}
	doc.WriteString("<!DOCTYPE r [" + dtd.String() + `]><r xmlns:p="urn:p">`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&doc, `<p:an-element-with-a-rather-long-name-%d/>`, i)
	}
	doc.WriteString("</r>")
	var tree *Tree
	var err error
	b := allocated(func() { tree, err = ParseString(doc.String(), ParseOptions{AllowDOCTYPE: true}) })
	if err != nil {
		t.Fatal(err)
	}
	if b > 24<<20 {
		t.Errorf("parse allocated %d MB, want under 24", b>>20)
	} else {
		t.Logf("parse allocated %d KB", b>>10)
	}
	i := 0
	for r := range tree.Root.Children() {
		for e := range r.Children() {
			if got := e.Attr("", "d"); got == nil || got.Value() != fmt.Sprintf("v%d", i) {
				t.Fatalf("element %d: default d = %v", i, got)
			}
			i++
		}
	}
	if i != n {
		t.Errorf("%d elements, want %d", i, n)
	}
}
