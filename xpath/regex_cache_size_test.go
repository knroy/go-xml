package xpath

import (
	"strings"
	"testing"
)

// compilesFor reports the programs compileXPathRegexp builds for one call.
func compilesFor(t *testing.T, pattern string) int64 {
	t.Helper()
	before := regexCompiles.Load()
	if _, err := compileXPathRegexp(pattern, "", XPath20); err != nil {
		t.Fatal(err)
	}
	return regexCompiles.Load() - before
}

// A pattern is compiled once and then served from the cache. A program over
// the old 4096-instruction entry cap was not cached, so each call compiled it
// three times over (the onepass probe, regexp.Compile and the cache gate's
// probe); it is now cached by its weight. An unanchored pattern needs no
// onepass probe at all, and an anchored one needs exactly one.
func TestRegexCompilesOncePerPattern(t *testing.T) {
	for _, c := range []struct {
		pattern string
		first   int64
	}{
		{strings.Repeat(`\p{L}{1000}`, 40) + "once", 1}, // 40,000 instructions
		{`^\p{L}{100}once$`, 2},                         // anchored: onepass copies charged
		{strings.Repeat(`\w`, 504) + "once", 1},         // class-heavy, 1 KB key
		{`[a-z]+\d{3}once`, 1},
	} {
		if n := compilesFor(t, c.pattern); n != c.first {
			t.Errorf("%.24q: first call compiled %d programs, want %d", c.pattern, n, c.first)
		}
		if n := compilesFor(t, c.pattern); n != 0 {
			t.Errorf("%.24q: second call compiled %d programs, want 0 (cached)", c.pattern, n)
		}
	}
}

// The cache is bounded by the bytes its programs retain, not only by its
// entry count: 85 copies of \p{L}{1000} is about 1 KB of key and 3.4 MB of
// program, so 1024 of them would pin 3.5 GB under a count alone.
func TestRegexCacheIsBoundedByWeight(t *testing.T) {
	for i := 0; i < 60; i++ {
		p := strings.Repeat(`\p{L}{1000}`, 85) + strings.Repeat("x", i)
		if _, err := compileXPathRegexp(p, "", XPath20); err != nil {
			t.Fatal(err)
		}
		if w := regexCache.Weight(); w > regexCacheBytes {
			t.Fatalf("after %d heavy patterns the cache holds %d bytes, over %d", i+1, w, regexCacheBytes)
		}
	}
	if n := regexCache.Len(); n >= 60 {
		t.Errorf("60 patterns of about 3.4 MB each all stayed cached (%d entries)", n)
	}
}

// Where regexp also builds a onepass program it copies a class's rune table
// into every instruction that uses it. Charged once, ^\p{L}{990}$ was cached
// at about 8 MB an entry; CompileBounded refuses that one outright.
func TestOnepassClassCopiesAreCharged(t *testing.T) {
	if _, err := compileXPathRegexp(`^\p{L}{990}$`, "", XPath20); err == nil {
		t.Error(`^\p{L}{990}$ compiled; its onepass copies retain about 8 MB`)
	}
	if _, err := compileXPathRegexp(`\p{L}{990}`, "", XPath20); err != nil {
		t.Errorf(`unanchored \p{L}{990} builds no onepass copy and must compile: %v`, err)
	}
}
