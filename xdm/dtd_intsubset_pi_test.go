package xdm

import "testing"

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
