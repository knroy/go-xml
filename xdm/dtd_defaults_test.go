package xdm

import "testing"

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
	if n := len(tree.Root.ChildElements()[0].Attrs); n != 0 {
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
	if len(kids[1].Attrs) != 0 {
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
		for _, a := range tree.Root.ChildElements()[0].Attrs {
			if len(a.Value) > 0 && a.Value[0] != '&' {
				t.Errorf("%s: attribute %q = %q, which looks expanded",
					c.name, a.Name.Local, a.Value)
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
