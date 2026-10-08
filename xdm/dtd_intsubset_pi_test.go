package xdm

import (
	"strings"
	"testing"
)

// TestInternalSubsetPIIsNotStructure covers a processing instruction in the
// internal subset. XML 1.0 §2.8 admits a PI to intSubset, and §2.6 says its
// content is not markup, so a quote, an apostrophe, a "]" or a ">" written
// inside one is text. Scanning it as structure ended the subset in the wrong
// place, and the entity references in the body were then rewritten against a
// boundary that fell inside the declarations.
//
// A CDATA section is deliberately absent: CDSect belongs to `content`, not to
// intSubset, so one written here is malformed and the decoder rejects it.
//
// The tokeniser's directive scanner skips a PI as a unit too, so an apostrophe
// or a bare ">" in one no longer opens a quote or ends the DOCTYPE early.
func TestInternalSubsetPIIsNotStructure(t *testing.T) {
	cases := []struct{ name, src string }{
		{"quote-and-bracket", `<!DOCTYPE d [<?p x "]>" y ?><!ENTITY e "X">]><d>&e;</d>`},
		{"bracket", `<!DOCTYPE d [<?p a ] b ?><!ENTITY e "X">]><d>&e;</d>`},
		{"apostrophe", `<!DOCTYPE d [<?p it's ?><!ENTITY e "X">]><d>&e;</d>`},
		{"bare gt", `<!DOCTYPE d [<?p a > b ?><!ENTITY e "X">]><d>&e;</d>`},
		{"comment with apostrophe, bracket and gt", `<!DOCTYPE d [<!-- it's ] > --><!ENTITY e "X">]><d>&e;</d>`},
		{"pi then quoted bracket", `<!DOCTYPE d [<?p it's ?><!ENTITY f "]>"><!ENTITY e "X">]><d>&e;</d>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The boundary is where the root element begins.
			want := -1
			for i := 0; i+3 <= len(c.src); i++ {
				if c.src[i:i+3] == "<d>" {
					want = i
					break
				}
			}
			if got := endOfInternalSubset(c.src); got != want {
				t.Errorf("endOfInternalSubset = %d, want %d (subset ends at the root element)", got, want)
			}
			tree, err := ParseString(c.src, ParseOptions{AllowDOCTYPE: true})
			if err != nil {
				t.Fatalf("a PI in the internal subset is well-formed XML: %v", err)
			}
			if got := tree.Root.ChildElements()[0].StringValue(); got != "X" {
				t.Errorf("root content = %q, want %q (the entity should still expand)", got, "X")
			}
		})
	}
}

// TestDeclarationsInsideUnparsedRegions: a declaration written inside a PI, a
// comment or a quoted literal is text, not a declaration (XML 1.0 §2.5, §2.6),
// so it must not declare an entity, an attribute default or a content model.
// Every reader of the subset goes through markupDecls, which skips all three.
func TestDeclarationsInsideUnparsedRegions(t *testing.T) {
	parse := func(src string) (*Tree, error) {
		return ParseString(src, ParseOptions{AllowDOCTYPE: true})
	}
	if _, err := parse(`<!DOCTYPE r [<?p <!ENTITY e "evil">?>]><r>&e;</r>`); err == nil {
		t.Error("an entity declared only inside a PI was usable")
	}
	tree, err := parse(`<!DOCTYPE r [<?p <!ENTITY e "evil">?><!ENTITY e "good">]><r>&e;</r>`)
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.Root.ChildElements()[0].StringValue(); got != "good" {
		t.Errorf("entity = %q, want the real declaration's %q", got, "good")
	}
	tree, err = parse(`<!DOCTYPE r [<?p <!ATTLIST r a CDATA "x">?><!ATTLIST r b CDATA "x>y">]><r/>`)
	if err != nil {
		t.Fatal(err)
	}
	r := tree.Root.ChildElements()[0]
	if r.Attr("", "a") != nil {
		t.Error("an ATTLIST inside a PI supplied a default")
	}
	if b := r.Attr("", "b"); b == nil {
		t.Error("default holding > was lost")
	} else if b.Value != "x>y" {
		t.Errorf("default holding > = %q, want %q", b.Value, "x>y")
	}
	tree, err = parse("<!DOCTYPE r [<?p <!ELEMENT r (s)>?>]><r> <s/> </r>")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(tree.Root.ChildElements()[0].Children); n != 3 {
		t.Errorf("r has %d children, want 3: an ELEMENT inside a PI made its white space ignorable", n)
	}

	// Comments in the internal subset are dropped by the tokeniser, so the
	// comment case is checked on subset text, as an external subset arrives.
	var got []string
	for body := range markupDecls(`<!-- <!ENTITY e "evil"> --><!ENTITY f '<!ENTITY g "x">'>`+
		`<?p <!ENTITY h "y">?><![INCLUDE[<!ENTITY i "z">]]>`, "ENTITY") {
		got = append(got, body)
	}
	if want := []string{` f '<!ENTITY g "x">'`, ` i "z"`}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("markupDecls = %q, want %q", got, want)
	}
}
