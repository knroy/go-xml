package xdm

import (
	"errors"
	"fmt"
	"strings"
	"testing"
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
	err := ProcessXInclude(tree, XIncludeOptions{
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
	if err := ProcessXInclude(tree, XIncludeOptions{
		Resolver: &mapResolver{files: files},
		Parse:    ParseOptions{AllowDOCTYPE: true},
	}); err != nil {
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
	err := ProcessXInclude(tree, XIncludeOptions{
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
