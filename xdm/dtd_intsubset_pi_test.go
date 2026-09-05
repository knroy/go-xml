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
// A PI holding a bare ">" is absent for a different reason: encoding/xml ends
// the Directive at that ">" and emits the remaining "]>" as character data, so
// the document never reaches this package intact. That is a defect in the
// standard library's directive scanner, not in this one -- endOfInternalSubset
// gets the boundary right for it -- and there is nothing to assert here until
// the decoder delivers the DOCTYPE whole.
func TestInternalSubsetPIIsNotStructure(t *testing.T) {
	cases := []struct{ name, src string }{
		{"quote-and-bracket", `<!DOCTYPE d [<?p x "]>" y ?><!ENTITY e "X">]><d>&e;</d>`},
		{"bracket", `<!DOCTYPE d [<?p a ] b ?><!ENTITY e "X">]><d>&e;</d>`},
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
