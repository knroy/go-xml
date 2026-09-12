package xslt

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The void-element tables exist twice -- here and in xpath/fn_serialize.go --
// because fn:serialize and xsl:result-document are two spellings of one
// serialisation request, and an element written "<br>" by one and "<br/>" by
// the other would make the answer depend on which spelling the caller used.
//
// The comment on each copy says "if you touch either copy, touch both". A
// comment cannot fail, so this test is what actually holds them together: it
// reads both files and compares the tables as text. It is deliberately a
// source comparison rather than a value comparison, because the maps are
// unexported on both sides and exporting them to test them would widen an API
// for no other reason.
//
// If this fails, the fix is to make the two tables equal again, not to relax
// the test. If a shared definition is ever introduced -- nothing in the import
// graph forbids it, since xslt already imports xpath -- delete this test along
// with the second copy.
func TestVoidElementTablesMatchTheXPathSerialiser(t *testing.T) {
	// The test runs with its own package directory as the working
	// directory, so the sibling package is one level up.
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join("..", rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		return string(b)
	}
	xsltSrc := read(filepath.Join("xslt", "serialize.go"))
	xpathSrc := read(filepath.Join("xpath", "fn_serialize.go"))

	name := regexp.MustCompile(`"([a-z0-9]+)"\s*:\s*true`)

	// entries returns the element names of one table, sorted, so that a
	// reordering or a whitespace change is not reported as a difference.
	entries := func(src, table, where string) []string {
		start := strings.Index(src, "var "+table+" = map[string]bool{")
		if start < 0 {
			t.Fatalf("%s: no table named %q; if it was renamed or removed, "+
				"the other copy and this test must follow", where, table)
		}
		end := strings.Index(src[start:], "\n}")
		if end < 0 {
			t.Fatalf("%s: table %q is not closed", where, table)
		}
		var got []string
		for _, m := range name.FindAllStringSubmatch(src[start:start+end], -1) {
			got = append(got, m[1])
		}
		if len(got) == 0 {
			t.Fatalf("%s: table %q parsed to nothing, so this test would "+
				"pass vacuously", where, table)
		}
		sort.Strings(got)
		return got
	}

	for _, table := range []string{
		"voidElements", "html4VoidElements", "html5VoidElements",
	} {
		mine := entries(xsltSrc, table, "xslt/serialize.go")
		theirs := entries(xpathSrc, table, "xpath/fn_serialize.go")
		if strings.Join(mine, " ") != strings.Join(theirs, " ") {
			t.Errorf("%s has diverged between the two serialisers:\n"+
				"  xslt/serialize.go:     %v\n"+
				"  xpath/fn_serialize.go: %v\n"+
				"the same element must take the same end tag through "+
				"xsl:result-document and fn:serialize", table, mine, theirs)
		}
	}
}
