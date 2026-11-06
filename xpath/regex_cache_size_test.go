package xpath

import (
	"strings"
	"testing"
)

// A pattern under the key limit can still compile to megabytes, so the cache
// keeps a program only up to maxCachedProgram instructions.
func TestLargeProgramIsNotCached(t *testing.T) {
	big := strings.Repeat(`\p{L}{1000}`, 40) + "x"
	if len(big) > maxCachedKey {
		t.Fatalf("test pattern is %d bytes, over the key limit", len(big))
	}
	n := regexCache.Len()
	if _, err := compileXPathRegexp(big, "", XPath20); err != nil {
		t.Fatal(err)
	}
	if regexCache.Len() != n {
		t.Errorf("a %d-byte pattern with a huge program was cached", len(big))
	}
	if _, err := compileXPathRegexp(`[a-z]+\d{3}`, "", XPath20); err != nil {
		t.Fatal(err)
	}
	if regexCache.Len() != n+1 {
		t.Errorf("a small pattern was not cached")
	}
}

// An anchored program under 1000 instructions also gets a onepass copy, which
// copies a class's rune table into every instruction that uses it, so a class
// shared across x{n} is charged at each use there. Charged once,
// ^\p{L}{990}$ was cached at about 8 MB an entry.
func TestOnepassClassCopiesAreCharged(t *testing.T) {
	n := regexCache.Len()
	if _, err := compileXPathRegexp(`^\p{L}{990}$`, "", XPath20); err != nil {
		t.Fatal(err)
	}
	if regexCache.Len() != n {
		t.Errorf("an anchored pattern whose onepass copies retain megabytes was cached")
	}
	if _, err := compileXPathRegexp(`^\p{L}{3}$`, "", XPath20); err != nil {
		t.Fatal(err)
	}
	if regexCache.Len() != n+1 {
		t.Errorf("a small anchored class pattern was not cached")
	}
}
