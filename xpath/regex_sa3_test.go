package xpath

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestRegexCacheRefusesClassHeavyProgram: 504 copies of \w are a 1 KB key but
// a Unicode-wide class each, about 4 MB retained. Such a program was once
// recompiled per use; it is now cached, and its class tables count toward
// the cache's byte bound (see TestRegexCacheIsBoundedByWeight). An ordinary
// pattern with a few classes is cached too.
func TestRegexCacheRefusesClassHeavyProgram(t *testing.T) {
	for _, c := range []struct {
		pat    string
		cached bool
	}{
		{strings.Repeat(`\w`, 504) + "sa3x", true},
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

// TestSMB5BackrefAdmissionIsLinear: admission is decided once on the parsed
// tree, so patterns that made the old per-group string rescans quadratic
// (thousands of referenced groups, deep nesting around a wide class) compile
// promptly and are not refused as a resource limit.
func TestSMB5BackrefAdmissionIsLinear(t *testing.T) {
	var g, r strings.Builder
	for i := 1; i <= 4000; i++ {
		g.WriteString("(a)")
		fmt.Fprintf(&r, `\%d`, i)
	}
	cases := []struct{ name, pattern, input string }{
		{"flat groups", g.String() + r.String(), strings.Repeat("a", 8000)},
		{"nested groups", strings.Repeat("(", 300) + "[" + strings.Repeat("b", 4000) + "]" +
			strings.Repeat(")", 300) + r.String()[:len(`\1\2\3\4\5\6\7\8\9`)], strings.Repeat("b", 10)},
		{"one group", "(a)[" + strings.Repeat("b", 16000) + "]" + strings.Repeat(`\1`, 16000), "ab" + strings.Repeat("a", 16000)},
	}
	for _, c := range cases {
		done := make(chan error, 1)
		go func() {
			re, err := buildBackrefRegexp(c.pattern, "", XPath31)
			if err == nil && !re.MatchString(c.input) {
				err = errors.New("no match")
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: admission still running after 2s", c.name)
		}
	}
}
