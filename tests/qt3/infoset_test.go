package qt3

import "testing"

// TestInfosetEqualRejects checks that the infoset comparison is not a blanket
// pass. It is the loosest of xmlMatches' comparisons, so it is the one that
// could hide a real difference.
func TestInfosetEqualRejects(t *testing.T) {
	same := [][2]string{
		{`<fn:a xmlns:fn="urn:x"><fn:b/></fn:a>`, `<a xmlns="urn:x"><b/></a>`},
		{`<a p="1" q="2"/>`, `<a q="2" p="1"/>`},
		{"<a>\n  <b/>\n</a>", `<a><b/></a>`},
	}
	for _, c := range same {
		if !infosetEqual(c[0], c[1]) {
			t.Errorf("infosetEqual(%q, %q) = false, want true", c[0], c[1])
		}
	}
	differ := [][2]string{
		{`<a xmlns="urn:x"/>`, `<a xmlns="urn:y"/>`}, // different namespace
		{`<a><b/></a>`, `<a><c/></a>`},               // different child name
		{`<a>text</a>`, `<a>other</a>`},              // different text
		{`<a p="1"/>`, `<a p="2"/>`},                 // different value
		{`<a p="1"/>`, `<a/>`},                       // missing attribute
		{`<a><b/><c/></a>`, `<a><c/><b/></a>`},       // child order matters
		{`<a><b/></a>`, `<a><b/><b/></a>`},           // child count
	}
	for _, c := range differ {
		if infosetEqual(c[0], c[1]) {
			t.Errorf("infosetEqual(%q, %q) = true, want false", c[0], c[1])
		}
	}
}
