package xdm

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// bombDoc is a document whose entity expansion is large but stays UNDER the
// 1 MB per-document ceiling: "c" expands to 16 KB, referenced 48 times for
// 786,432 bytes out of about 400 bytes of source. Every copy of it is a
// document this package is willing to parse on its own.
//
// tail is spliced in before the closing tag so a document can include others.
func bombDoc(tail string) string {
	return `<?xml version="1.0"?>
<!DOCTYPE d [
  <!ENTITY a "` + strings.Repeat("A", 64) + `">
  <!ENTITY b "` + strings.Repeat("&a;", 16) + `">
  <!ENTITY c "` + strings.Repeat("&b;", 16) + `">
]>
<part>` + strings.Repeat("&c;", 48) + tail + `</part>`
}

// bombBytes is what one bombDoc expands to.
const bombBytes = 64 * 16 * 16 * 48

// maxTotalEntityBytes is documented as bounding "every expansion in one
// document together". It stopped doing that as soon as a document could pull
// other documents in: ProcessXInclude parses each included resource with
// ParseString, ParseString built a fresh entityTable, and a fresh table
// started the byte count again from zero. The include FETCH counter was
// shared — it lives on the one includeProc — so only the byte budget reset.
//
// Measured before the fix, with MaxBytes 8192 and MaxNodes 50 explicitly set:
// 95,444 bytes of source across 200 documents expanded to 156,499,968 bytes
// and allocated 577 MB — 1640x amplification, past the ceiling by 149x.
// Neither knob can see it. MaxBytes bounds the source text of each parse, and
// a reference is three bytes; MaxNodes bounds the node count, and an expansion
// is one text node however long it is.
func TestXIncludeSharesTheEntityBudget(t *testing.T) {
	// Twenty documents, each individually legal, in a tree of includes. Two
	// of them together are already over the ceiling, so a shared budget must
	// refuse the set.
	const n = 20
	files := map[string]string{}
	for i := 0; i < n; i++ {
		var kids strings.Builder
		for k := 1; k <= 3; k++ {
			if c := i*3 + k; c < n {
				fmt.Fprintf(&kids,
					`<xi:include xmlns:xi="http://www.w3.org/2001/XInclude" href="p%d.xml"/>`, c)
			}
		}
		files[fmt.Sprintf("mem:/p%d.xml", i)] = bombDoc(kids.String())
	}

	src := `<?xml version="1.0"?><root xmlns:xi="http://www.w3.org/2001/XInclude">` +
		`<xi:include href="mem:/p0.xml"/></root>`

	tree := parseWithBase(t, "mem:/root.xml", src)
	_, err := ProcessXInclude(tree, XIncludeOptions{
		Resolver: &mapResolver{files: files},
		// The knobs a caller would reach for are set as tightly as the
		// documents allow, to pin that they are NOT what refuses this.
		Parse: ParseOptions{AllowDOCTYPE: true, MaxBytes: 8192, MaxNodes: 50},
	})
	if err == nil {
		t.Fatalf("a chain of %d included documents expanded to %d bytes "+
			"and was accepted; the %d byte budget did not bind across the "+
			"include boundary", n, len(tree.Root.StringValue()), maxTotalEntityBytes)
	}
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("refused, but not as a resource limit: %v", err)
	}
	if !strings.Contains(err.Error(), "entity expansion exceeds") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
}

// The control. A bound that refuses everything is not a bound, so a
// legitimate multi-document inclusion — several included documents that use
// entities and together stay well under the ceiling — must still work.
func TestXIncludeLegitimateMultiDocumentStillWorks(t *testing.T) {
	const n = 5
	const per = 1000
	files := map[string]string{}
	var inc strings.Builder
	for i := 0; i < n; i++ {
		files[fmt.Sprintf("mem:/q%d.xml", i)] = `<?xml version="1.0"?>` +
			`<!DOCTYPE d [<!ENTITY e "` + strings.Repeat("B", 100) + `">]>` +
			`<part>` + strings.Repeat("&e;", per/100) + `</part>`
		fmt.Fprintf(&inc, `<xi:include href="mem:/q%d.xml"/>`, i)
	}

	src := `<?xml version="1.0"?><root xmlns:xi="http://www.w3.org/2001/XInclude">` +
		inc.String() + `</root>`

	tree := parseWithBase(t, "mem:/root.xml", src)
	tree, err := ProcessXInclude(tree, XIncludeOptions{
		Resolver: &mapResolver{files: files},
		Parse:    ParseOptions{AllowDOCTYPE: true},
	})
	if err != nil {
		t.Fatalf("%d documents expanding %d bytes in total — well inside the "+
			"%d byte budget — were refused: %v", n, n*per, maxTotalEntityBytes, err)
	}
	if got, want := len(tree.Root.StringValue()), n*per; got != want {
		t.Fatalf("expanded to %d bytes, want %d", got, want)
	}
}

// One document's expansion is charged where it always was, so sharing the
// counter did not move the single-document bound. This is the case that
// always worked and must go on working.
func TestEntityBudgetStillBindsOneDocument(t *testing.T) {
	if _, err := ParseString(bombDoc(""), ParseOptions{AllowDOCTYPE: true}); err != nil {
		t.Fatalf("a %d byte expansion, inside the budget, was refused: %v",
			bombBytes, err)
	}
	// Two of them in one document is over the ceiling and always was.
	two := `<?xml version="1.0"?>
<!DOCTYPE d [
  <!ENTITY a "` + strings.Repeat("A", 64) + `">
  <!ENTITY b "` + strings.Repeat("&a;", 16) + `">
  <!ENTITY c "` + strings.Repeat("&b;", 16) + `">
]>
<part>` + strings.Repeat("&c;", 96) + `</part>`
	if _, err := ParseString(two, ParseOptions{AllowDOCTYPE: true}); err == nil {
		t.Fatalf("a %d byte expansion in one document was accepted", 2*bombBytes)
	} else if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("refused, but not as a resource limit: %v", err)
	}
}

// A budget refusal is this processor declining to spend more, not a condition
// of the resource, so xi:fallback must NOT recover from it — otherwise a
// fallback chain is a way to keep asking after being told no, and a document
// gets a different result by being expensive. The fetch and nesting bounds are
// already fatal on exactly these terms; this puts the byte budget with them.
func TestXIncludeBudgetRefusalIsNotRecoverable(t *testing.T) {
	const n = 8
	files := map[string]string{}
	for i := 0; i < n; i++ {
		files[fmt.Sprintf("mem:/r%d.xml", i)] = bombDoc("")
	}
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb,
			`<xi:include href="mem:/r%d.xml"><xi:fallback>`+
				`<xi:include href="mem:/r%d.xml"/></xi:fallback></xi:include>`,
			i, (i+1)%n)
	}
	src := `<?xml version="1.0"?><root xmlns:xi="http://www.w3.org/2001/XInclude">` +
		sb.String() + `</root>`

	tree := parseWithBase(t, "mem:/root.xml", src)
	_, err := ProcessXInclude(tree, XIncludeOptions{
		Resolver: &mapResolver{files: files},
		Parse:    ParseOptions{AllowDOCTYPE: true},
	})
	if err == nil {
		t.Fatalf("%d bombs behind fallbacks expanded to %d bytes and were "+
			"accepted: the budget refusal was laundered by xi:fallback",
			n, len(tree.Root.StringValue()))
	}
	if !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("refused, but not as a resource limit: %v", err)
	}
}

// An href-less xpointer inclusion copies an element as already expanded, so
// a level of two includes of the level before doubles the result: 30 levels,
// about 2.5 KB, asked for 2^31 copies, with no resolver and inside every
// fetch and depth bound. The inclusions now share the pass's node budget,
// opts.Parse.MaxNodes.
func TestXIncludeLocalInclusionsShareTheNodeBudget(t *testing.T) {
	doc := func(levels int) string {
		var b strings.Builder
		b.WriteString(`<r xmlns:xi="http://www.w3.org/2001/XInclude"><l0>x</l0>`)
		for i := 1; i <= levels; i++ {
			fmt.Fprintf(&b, `<l%d><xi:include xpointer="element(/1/%d)"/><xi:include xpointer="element(/1/%d)"/></l%d>`, i, i, i, i)
		}
		b.WriteString(`</r>`)
		return b.String()
	}
	po := ParseOptions{BaseURI: "file:///t/doc.xml", MaxNodes: 1000}
	tree, err := ParseString(doc(30), po)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ProcessXInclude(tree, XIncludeOptions{Parse: po})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrResourceLimit) || !strings.Contains(err.Error(), "inclusions exceed 1000 nodes") {
			t.Fatalf("30 doubling levels under MaxNodes 1000: err = %v, want the node budget", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("30 doubling levels did not finish in 5s")
	}

	// Four levels under the default budget are expanded as before.
	tree, err = ParseString(doc(4), ParseOptions{BaseURI: "file:///t/doc.xml"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := ProcessXInclude(tree, XIncludeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	l0 := 0
	for n := range out.Root.Descendants() {
		if n.Kind() == KindElement && n.Name().Local == "l0" {
			l0++
		}
	}
	if l0 != 31 {
		t.Errorf("four levels: %d copies of l0, want 31", l0)
	}
}

// The node budget cannot see text: one text node of 8000 bytes, doubled per
// level by href-less inclusions, held 65 MB under MaxBytes 16384 in about
// 25,000 nodes. The inclusions now share a byte budget, opts.Parse.MaxBytes.
func TestXIncludeLocalInclusionsShareTheByteBudget(t *testing.T) {
	doc := func(levels int) string {
		var b strings.Builder
		b.WriteString(`<r xmlns:xi="http://www.w3.org/2001/XInclude"><a xml:id="l0">` + strings.Repeat("x", 8000) + `</a>`)
		for i := 1; i <= levels; i++ {
			fmt.Fprintf(&b, `<a xml:id="l%d"><xi:include xpointer="l%d"/><xi:include xpointer="l%d"/></a>`, i, i-1, i-1)
		}
		b.WriteString(`</r>`)
		return b.String()
	}
	po := ParseOptions{MaxBytes: 16384}
	tree, err := ParseString(doc(12), po)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ProcessXInclude(tree, XIncludeOptions{Parse: po})
	if !errors.Is(err, ErrResourceLimit) || !strings.Contains(err.Error(), "16384 bytes") {
		t.Fatalf("12 doubling levels of 8000 bytes under MaxBytes 16384: err = %v, want the byte budget", err)
	}
	// One level, 16,000 bytes copied, fits under the default.
	tree, err = ParseString(doc(1), ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessXInclude(tree, XIncludeOptions{}); err != nil {
		t.Fatal(err)
	}
}

// A shorthand xpointer is resolved by walking the document from its root, so
// N includes naming an element after them cost N^2/2 visits for a node charge
// of 2 each. The walks now share the pass's node budget as a visit count.
func TestXIncludeLocalSearchesShareTheNodeBudget(t *testing.T) {
	doc := func(n int) string {
		return `<r xmlns:xi="http://www.w3.org/2001/XInclude">` +
			strings.Repeat(`<xi:include xpointer="z"/>`, n) + `<t xml:id="z"/></r>`
	}
	po := ParseOptions{MaxNodes: 10000}
	tree, err := ParseString(doc(400), po)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ProcessXInclude(tree, XIncludeOptions{Parse: po})
	if !errors.Is(err, ErrResourceLimit) || !strings.Contains(err.Error(), "searches") {
		t.Fatalf("400 searches of a 400-node document under MaxNodes 10000: err = %v, want the visit budget", err)
	}
	tree, err = ParseString(doc(20), po)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ProcessXInclude(tree, XIncludeOptions{Parse: po})
	if err != nil {
		t.Fatal(err)
	}
	if n := out.Root.FirstChild(); n == nil {
		t.Fatal("no result")
	}
}
