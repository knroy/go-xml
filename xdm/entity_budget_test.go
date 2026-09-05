package xdm

import (
	"runtime"
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

// The budget must hold when the DOCTYPE and the body arrive in the same read.
//
// entityChargeReader buffers into backlog while the table is nil, and drained
// it on the NEXT Read. A large entity declaration guarantees there is no next
// read: encoding/xml's read-ahead window is sized in bytes, so one big
// declaration fills it along with the entire body, the decoder never reads
// again, and the backlog was dropped with charge() never called. The budget
// was bypassed with no threshold at all — a 10 kB entity already blew the 1 MB
// bound threefold, and a 1 MB request body reached 1.4 GB of allocation, which
// is a single-request OOM reachable with AllowDOCTYPE alone.
//
// The counts here are deliberately modest: the point is that NO size is safe,
// not that a huge one is unsafe.
func TestEntityBudgetHoldsWhenDoctypeAndBodyShareOneRead(t *testing.T) {
	build := func(entLen, refs int) string {
		var sb strings.Builder
		sb.WriteString(`<!DOCTYPE r [<!ENTITY e "` + strings.Repeat("A", entLen) + `">]><r>`)
		for i := 0; i < refs; i++ {
			sb.WriteString("&e;")
		}
		sb.WriteString("</r>")
		return sb.String()
	}

	// 10 kB x 400 = 4 MB expanded, four times the 1 MB budget, from an 11 kB
	// source. The declaration is large enough that the decoder buffers the
	// whole document before delivering the DOCTYPE token.
	for _, entLen := range []int{10000, 60000, 1000000} {
		doc := build(entLen, 400)
		var m0, m1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m0)
		tr, err := ParseString(doc, ParseOptions{AllowDOCTYPE: true})
		runtime.ReadMemStats(&m1)
		if err == nil {
			t.Fatalf("entity=%d: %d bytes of source expanded to %d bytes and was "+
				"ACCEPTED past a %d byte budget",
				entLen, len(doc), len(tr.Root.StringValue()), maxTotalEntityBytes)
		}
		if !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("entity=%d: refused for the wrong reason: %v", entLen, err)
		}
		// Refusing must also be cheap. Charging before expansion is the whole
		// point of the control: a refusal that first allocates what it is
		// refusing bounds nothing. 1 MB x 400 references allocated 1423 MB
		// before the fix.
		if got := m1.TotalAlloc - m0.TotalAlloc; got > uint64(64<<20)+uint64(4*entLen) {
			t.Fatalf("entity=%d: refusing allocated %d bytes, which is not a bound",
				entLen, got)
		}
	}

	// A document well inside the budget still parses, so the fix is not
	// simply refusing everything that arrives in one read.
	small := build(100, 100)
	tr, err := ParseString(small, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("a 10 kB expansion was refused: %v", err)
	}
	if got := len(tr.Root.StringValue()); got != 100*100 {
		t.Fatalf("expanded to %d bytes, want %d", got, 100*100)
	}
}

// endOfInternalSubset locates where the subset ends so that charge() counts
// USES of an entity and not its DECLARATION. It tracked quotes and brackets
// but had no comment state, and XML 1.0 §2.8 lets the internal subset hold
// comments while §2.5 says their content is not markup — so an apostrophe or a
// bracket written as prose inside one was read as structure.
//
// Both failures matter, in opposite directions: an unbalanced quote made the
// scan run off the end and return 0, so the caller charged the subset itself
// as content; a "]" ended the subset early, so the declarations were scanned
// as content. Neither was reachable end-to-end while the backlog bypass above
// dominated every case, which is why the two are fixed and tested together.
func TestEndOfInternalSubsetSkipsComments(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want int
	}{
		{`<!DOCTYPE d [ <!-- it's here --> <!ENTITY e "X"> ]><d>&e;</d>`, 51},
		{`<!DOCTYPE d [ <!-- ] --> <!ENTITY e "X"> ]><d>&e;</d>`, 43},
		{`<!DOCTYPE d [ <!-- [ --> <!ENTITY e "X"> ]><d>&e;</d>`, 43},
		{`<!DOCTYPE d [ <!-- > --> <!ENTITY e "X"> ]><d>&e;</d>`, 43},
		{`<!DOCTYPE d [ <!-- " --> <!ENTITY e "X"> ]><d>&e;</d>`, 43},
		// No comment: the behaviour that already worked must not change.
		{`<!DOCTYPE d [ <!ENTITY e "X"> ]><d>&e;</d>`, 32},
		{`<!DOCTYPE d SYSTEM "x.dtd"><d/>`, 27},
		{`<d/>`, 0},
		// A "]" inside a declaration's quoted literal is still not structure.
		{`<!DOCTYPE d [ <!ENTITY e "]"> ]><d>&e;</d>`, 32},
	} {
		if got := endOfInternalSubset(tc.src); got != tc.want {
			t.Errorf("endOfInternalSubset(%q) = %d, want %d", tc.src, got, tc.want)
		}
	}
}

// The two defects interact: with the backlog charged, the subset scan is what
// decides which bytes get charged, so a comment in the subset must not shift
// that boundary. A comment holding an apostrophe used to send the scan off the
// end, returning 0, and the whole subset — declaration text included — was
// then charged as content.
func TestEntityBudgetWithCommentInSubset(t *testing.T) {
	payload := strings.Repeat("A", 10000)

	// Over budget: 10 kB x 400 = 4 MB, with a comment carrying an apostrophe
	// and a bracket in the subset.
	var over strings.Builder
	over.WriteString(`<!DOCTYPE r [ <!-- it's ] here --> <!ENTITY e "` + payload + `"> ]><r>`)
	for i := 0; i < 400; i++ {
		over.WriteString("&e;")
	}
	over.WriteString("</r>")
	if _, err := ParseString(over.String(), ParseOptions{AllowDOCTYPE: true}); err == nil {
		t.Fatal("4 MB of expansion accepted past a 1 MB budget")
	}

	// Under budget, same shape: the comment must not cause a false refusal by
	// making the declaration itself count as a reference.
	var under strings.Builder
	under.WriteString(`<!DOCTYPE r [ <!-- it's ] here --> <!ENTITY e "` + payload + `"> ]><r>`)
	for i := 0; i < 10; i++ {
		under.WriteString("&e;")
	}
	under.WriteString("</r>")
	tr, err := ParseString(under.String(), ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("100 kB of expansion refused under a 1 MB budget: %v", err)
	}
	if got := len(tr.Root.StringValue()); got != 10*10000 {
		t.Fatalf("expanded to %d bytes, want %d", got, 10*10000)
	}
}
