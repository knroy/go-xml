package xpath

import (
	"strings"
	"testing"
)

// TestRegexCacheRefusesClassHeavyProgram: 504 copies of \w are a 1 KB key but
// a Unicode-wide class each, about 4 MB retained; such a program is
// recompiled per use rather than pinned in the process-wide cache. An
// ordinary pattern with a few classes is still cached.
func TestRegexCacheRefusesClassHeavyProgram(t *testing.T) {
	for _, c := range []struct {
		pat    string
		cached bool
	}{
		{strings.Repeat(`\w`, 504) + "sa3x", false},
		{`^\w+@\w+\.\w+$`, true},
	} {
		if _, err := compileXPathRegexp(c.pat, "", XPath31); err != nil {
			t.Fatalf("%.20q: %v", c.pat, err)
		}
		key := XPath31.String() + "\x00\x00" + c.pat
		if _, ok := regexCache.Get(key); ok != c.cached {
			t.Errorf("%.20q: cached = %v, want %v", c.pat, ok, c.cached)
		}
	}
}
