package relaxng

import (
	"fmt"
	"strings"
	"testing"
)

// When a cached definition closes a bare cycle through several definitions
// still being compiled, the least name is reported on every compile. A0
// through A9 each ref the next bare; A9 compiles C under <element b> first
// (C reaches every Ai bare, lazily there), then reaches the cached C bare
// while all ten are expanding. Each closes a cycle, and ranging C's bare
// set named any of them.
func TestCachedBareCycleReportsLeastName(t *testing.T) {
	var b, refs strings.Builder
	b.WriteString(`<grammar` + rngNS + `><start><element name="r"><ref name="A0"/></element></start>`)
	for i := 0; i < 9; i++ {
		fmt.Fprintf(&b, `<define name="A%d"><ref name="A%d"/></define>`, i, i+1)
	}
	b.WriteString(`<define name="A9"><choice><element name="b"><ref name="C"/></element><ref name="C"/></choice></define>`)
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&refs, `<ref name="A%d"/>`, i)
	}
	b.WriteString(`<define name="C"><choice>` + refs.String() + `</choice></define></grammar>`)
	const want = `definition "A0" refers to itself without an intervening <element>`
	for i := 0; i < 50; i++ {
		_, err := compileWith(t, b.String(), Options{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("compile %d: got %v, want %q", i, err, want)
		}
	}
}
