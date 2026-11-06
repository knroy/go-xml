package xpath

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/knroy/go-xml/v2/xdm"
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

// TestBackrefAnalysisBounded: the fixed-width analysis rescans the head per
// referenced group and per nesting level. A pattern built to make that
// quadratic is refused as a resource limit instead of burning seconds; the
// same group referenced many times is analysed once. Ordinary patterns are
// covered by TestBackrefFixedWidth and its neighbours.
func TestBackrefAnalysisBounded(t *testing.T) {
	var g, r strings.Builder
	for i := 1; i <= 4000; i++ {
		g.WriteString("(a)")
		fmt.Fprintf(&r, `\%d`, i)
	}
	cases := []struct {
		name    string
		pattern string
		refused bool
	}{
		{"flat groups", g.String() + r.String(), true},
		{"nested groups", strings.Repeat("(", 300) + "[" + strings.Repeat("b", 4000) + "]" +
			strings.Repeat(")", 300) + r.String()[:len(`\1\2\3\4\5\6\7\8\9`)], true},
		{"one group", "(a)[" + strings.Repeat("b", 16000) + "]" + strings.Repeat(`\1`, 16000), false},
	}
	for _, c := range cases {
		done := make(chan error, 1)
		go func() { _, err := buildBackrefRegexp(c.pattern, "", XPath31); done <- err }()
		select {
		case err := <-done:
			if got := errors.Is(err, xdm.ErrResourceLimit); got != c.refused {
				t.Errorf("%s: err = %v, want refused = %v", c.name, err, c.refused)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: analysis still running after 2s", c.name)
		}
	}
}
