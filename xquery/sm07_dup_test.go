package xquery

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// The XQST0049, XQST0034 and XQST0039 duplicate checks are lookups, not a
// scan of every earlier declaration: N declarations cost N, not N^2 Clark
// comparisons with two string allocations each.
func TestSM07DuplicateDeclarationChecksAreIndexed(t *testing.T) {
	for code, src := range map[string]string{
		"XQST0049": "declare variable $a := 1; declare variable $a := 2; 1",
		"XQST0034": "declare function local:f($x) { 1 }; declare function local:f($y) { 2 }; 1",
		"XQST0039": "declare function local:f($x, $x) { 1 }; 1",
	} {
		if _, err := Compile(src, Options{}); xdm.ErrorCode(err) != code {
			t.Errorf("%s: got %v", code, err)
		}
	}
	// Same name, different arity, is not a duplicate.
	if _, err := Compile("declare function local:f() { 1 }; declare function local:f($x) { 2 }; 1", Options{}); err != nil {
		t.Fatal(err)
	}
	const n = 3000
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "declare variable $v%d := 1;\ndeclare function local:f%d() { 1 };\n", i, i)
	}
	b.WriteString("1")
	src := b.String()
	var err error
	// The scan allocated two Clark strings per comparison, n^2 of them for
	// the functions alone: hundreds of MB here.
	if a := allocated(func() { _, err = Compile(src, Options{}) }); a > 64<<20 {
		t.Fatalf("%d declarations allocated %d bytes compiling; the duplicate "+
			"checks scan again", 2*n, a)
	}
	if err != nil {
		t.Fatal(err)
	}
}
