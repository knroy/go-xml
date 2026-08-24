package xdm

import (
	"strings"
	"testing"
)

// maxTotalEntityBytes is documented as bounding every expansion in one
// document together. It was charged once per DISTINCT ENTITY rather than once
// per REFERENCE, so one small entity referenced many times passed it: the
// budget was charged 65 kB while the decoder substituted 6.5 GB.
//
// Neither of the other limits caught it. MaxBytes bounds the INPUT, and a
// reference is three bytes; MaxNodes bounds the NODE COUNT, and the result is
// a single text node however long it is. Measured before the fix: 356 kB of
// input reached 14.3 GB of live heap in 16 seconds.
func TestEntityReferenceCountIsCharged(t *testing.T) {
	build := func(entLen, refs int) string {
		var sb strings.Builder
		sb.WriteString(`<!DOCTYPE r [<!ENTITY e "` + strings.Repeat("A", entLen) + `">]><r>`)
		for i := 0; i < refs; i++ {
			sb.WriteString("&e;")
		}
		sb.WriteString("</r>")
		return sb.String()
	}

	// Far past the 1 MB total: 65 kB x 100,000 references.
	doc := build(65000, 100000)
	if _, err := ParseString(doc, ParseOptions{AllowDOCTYPE: true}); err == nil {
		t.Fatalf("a document expanding to 6.5 GB was accepted")
	} else if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}

	// The same shape well under the budget still parses, so the bound is not
	// simply refusing every document that uses an entity twice.
	small := build(100, 100)
	tr, err := ParseString(small, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("a 10 kB expansion was refused: %v", err)
	}
	if got := len(tr.Root.StringValue()); got != 100*100 {
		t.Fatalf("expanded to %d bytes, want %d", got, 100*100)
	}
}

// The rewrite path (an entity holding markup) and the plain path must agree:
// before the fix the same document was refused on one and accepted on the
// other, which made the bound depend on an unrelated property of the subset.
func TestEntityBudgetAgreesAcrossBothPaths(t *testing.T) {
	var refs strings.Builder
	for i := 0; i < 100000; i++ {
		refs.WriteString("&e;")
	}
	payload := strings.Repeat("A", 500)

	plain := `<!DOCTYPE r [<!ENTITY e "` + payload + `">]><r>` + refs.String() + `</r>`
	markup := `<!DOCTYPE r [<!ENTITY z "<b/>"><!ENTITY e "` + payload + `">]><r>` +
		refs.String() + `</r>`

	_, plainErr := ParseString(plain, ParseOptions{AllowDOCTYPE: true})
	_, markupErr := ParseString(markup, ParseOptions{AllowDOCTYPE: true})
	if plainErr == nil || markupErr == nil {
		t.Fatalf("both paths must refuse: plain=%v markup=%v", plainErr, markupErr)
	}
}
