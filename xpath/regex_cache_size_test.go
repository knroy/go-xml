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
