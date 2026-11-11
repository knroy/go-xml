package dtd

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
)

// SECURITY. The scans below were each quadratic in one declaration or one
// value; at these sizes they took tens of seconds and now take milliseconds.
// finishes fails a test whose work does not complete within a bound far above
// the linear cost, so a reintroduced rescan fails rather than merely slows.
func finishes(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not finish in 10s; a scan is quadratic again", what)
	}
}

// §3.3.1 No Duplicate Tokens and first-definition-wins were checked by
// scanning: 1.35 s for a 40,000-token enumeration, 1.7 s for a 40,000
// attribute ATTLIST, four times that per doubling.
func TestAttlistDuplicateChecksAreLinear(t *testing.T) {
	const n = 200000
	var enum, attrs strings.Builder
	for i := range n {
		fmt.Fprintf(&enum, "|t%d", i)
		fmt.Fprintf(&attrs, " a%d CDATA #IMPLIED", i)
	}
	var d *DTD
	finishes(t, "a wide ATTLIST", func() {
		var err error
		d, err = Parse(`<!DOCTYPE r [<!ATTLIST r e (t` + enum.String() + `|t7) #IMPLIED` +
			attrs.String() + ` a7 ID #IMPLIED b ID #IMPLIED c ID #IMPLIED>]>`)
		if err != nil {
			t.Error(err)
		}
	})
	if d == nil {
		t.FailNow()
	}
	// e, n attributes, b and c: the second a7 is ignored, not checked.
	if got := len(d.Attributes["r"]); got != n+3 {
		t.Errorf("%d attributes bound, want %d", got, n+3)
	}
	want := []string{
		"attribute e of r lists t7 more than once (XML §3.3.1 No Duplicate Tokens)",
		"attribute c of r is a second ID attribute, after b (XML §3.3.1 One ID per Element Type)",
	}
	if strings.Join(d.declErrs, "\n") != strings.Join(want, "\n") {
		t.Errorf("declErrs = %q, want %q", d.declErrs, want)
	}
}

// The keyword of a conditional section searched for ';' at every byte.
func TestConditionalKeywordScanIsLinear(t *testing.T) {
	l := newLoader(LoadOptions{})
	in := "<![" + strings.Repeat("x%", 1<<20)
	finishes(t, "a long conditional keyword", func() {
		var sb strings.Builder
		if _, err := l.conditional(&sb, in, "", 0, true); err == nil ||
			!strings.Contains(err.Error(), "missing its '['") {
			t.Errorf("error = %v, want the missing '[' reported", err)
		}
	})
}

// An internal parameter entity holding an IGNORE section expands to nothing
// at declaration level, so a reference was charged three bytes for scanning
// all of it. Its raw replacement text is charged now.
func TestIgnoredReplacementTextIsCharged(t *testing.T) {
	ent := `<!ENTITY % a "<![IGNORE[` + strings.Repeat("x", 1000) + `]]>">`
	load := func(refs int) error {
		opts := mapOpts(map[string]string{"r.dtd": strings.Repeat("%a;", refs) + "<!ELEMENT r EMPTY>"})
		opts.MaxEntityBytes = 50000
		_, err := Load(`<!DOCTYPE r SYSTEM "r.dtd" [`+ent+`]>`, opts)
		return err
	}
	if err := load(10); err != nil {
		t.Fatalf("ten references within the budget: %v", err)
	}
	if err := load(100); !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("error = %v, want 100 KB of scanned replacement text refused with xdm.ErrResourceLimit", err)
	}
}

// Each nesting message was compared with every earlier one.
func TestNestingViolationsDeduplicateLinearly(t *testing.T) {
	const n = 100000
	var internal, external strings.Builder
	for i := range n {
		fmt.Fprintf(&internal, `<!ENTITY %% e%d "(">`, i)
		fmt.Fprintf(&external, "%%e%d;", i)
	}
	opts := mapOpts(map[string]string{"r.dtd": "<!ATTLIST r %e0;" + external.String() + ">"})
	opts.MaxEntityBytes = 4 << 20
	var d *DTD
	finishes(t, "many nesting violations", func() {
		var err error
		if d, err = Load(`<!DOCTYPE r SYSTEM "r.dtd" [`+internal.String()+`]>`, opts); err != nil {
			t.Error(err)
		}
	})
	if d != nil && len(d.nesting) != n {
		t.Errorf("%d nesting messages, want %d (once per entity)", len(d.nesting), n)
	}
}

// normalizedDefault searched for ';' at every byte.
func TestNormalizedDefaultIsLinear(t *testing.T) {
	in := strings.Repeat("&\t", 1<<20) + "&amp;&#x41;&lt"
	want := strings.Repeat("& ", 1<<20) + "&A&lt"
	finishes(t, "a long default", func() {
		if got := normalizedDefault(in); got != want {
			t.Errorf("normalizedDefault: got %d bytes, want %d", len(got), len(want))
		}
	})
	if got := normalizedDefault("&a&b;&amp;"); got != "&a&b;&" {
		t.Errorf("normalizedDefault = %q", got)
	}
}

// A content model the edge budget refuses was rebuilt for every element
// carrying it, a full budget of allocation each time.
func TestRefusedContentModelIsCompiledOnce(t *testing.T) {
	d, err := Parse(`<!DOCTYPE r [<!ELEMENT d (r)*><!ELEMENT a EMPTY>
	<!ELEMENT r (a?` + strings.Repeat(",a?", 400) + `)>]>`)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := xdm.ParseString("<d>"+strings.Repeat("<r/>", 200)+"</d>", xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	err = Validate(tree.Root, d, Options{MaxContentModelEdges: 1 << 16})
	runtime.ReadMemStats(&m1)
	var es *Errors
	if !errors.As(err, &es) || len(es.Errors) != 100 ||
		!strings.Contains(es.Errors[0].Message, "content model of r") {
		t.Fatalf("error = %v, want the refused model reported per element", err)
	}
	if got := m1.TotalAlloc - m0.TotalAlloc; got > 64<<20 {
		t.Errorf("%d MB allocated for 200 elements, want the refused model compiled once", got>>20)
	}
}

// The enumeration was joined for every failing occurrence, even past
// MaxErrors, and searched linearly for every one.
func TestEnumerationPreparedOncePerDeclaration(t *testing.T) {
	var enum strings.Builder
	for i := range 5000 {
		fmt.Fprintf(&enum, "|token%d", i)
	}
	d, err := Parse(`<!DOCTYPE d [<!ELEMENT d (r)*><!ELEMENT r EMPTY>
	<!ATTLIST r x (t` + enum.String() + `) #IMPLIED y CDATA #REQUIRED z CDATA #FIXED "a&#x41;b">]>`)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := xdm.ParseString("<d>"+strings.Repeat(`<r x="no" y="" z="aAb"/>`, 2000)+`<r x="t" z="a"/></d>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	err = Validate(tree.Root, d, Options{MaxErrors: -1})
	runtime.ReadMemStats(&m1)
	var es *Errors
	if !errors.As(err, &es) || len(es.Errors) != 2002 {
		t.Fatalf("error = %v, want 2002 errors", err)
	}
	// Declaration order, as before: y is required, then z's value.
	last := es.Errors[2000:]
	if !strings.Contains(last[0].Message, "required attribute y") ||
		!strings.Contains(last[1].Message, `#FIXED "a&#x41;b" but is "a"`) {
		t.Errorf("last errors = %v %v", last[0], last[1])
	}
	runtime.ReadMemStats(&m0)
	_ = Validate(tree.Root, d, Options{})
	runtime.ReadMemStats(&m1)
	// One 60 KB join with the fix; 2000 of them, 120 MB, without.
	if got := m1.TotalAlloc - m0.TotalAlloc; got > 16<<20 {
		t.Errorf("%d MB allocated, want the enumeration joined once", got>>20)
	}
}
