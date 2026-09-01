package xpath

import (
	"strings"
	"testing"
)

// The XSD-flavour pattern parser is recursive descent over groups, and had no
// depth bound: regExp -> branch -> piece -> atom -> regExp once per "(", so a
// pattern facet deep enough exhausted the goroutine stack. That is a fatal
// error rather than a catchable one, so an untrusted schema killed the
// process. A 6 MB schema did it at Go's default 1 GB stack.
//
// The XPath-flavour checker is an iterative scanner and was never affected,
// which is why a pattern arriving as document data could not reach this.
func TestDeepSchemaPatternIsRefusedNotFatal(t *testing.T) {
	for _, depth := range []int{maxSchemaRegexpDepth + 1, 400000} {
		pat := strings.Repeat("(", depth) + "a" + strings.Repeat(")", depth)
		err := validateSchemaRegexp(pat, false)
		if err == nil {
			t.Fatalf("depth %d was accepted; the bound did not apply", depth)
		}
		if !strings.Contains(err.Error(), "nesting exceeds") {
			t.Fatalf("depth %d refused for the wrong reason: %v", depth, err)
		}
	}
}

// Real patterns nest a few levels at most; the bound must not touch them.
func TestOrdinarySchemaPatternsStillValidate(t *testing.T) {
	for _, pat := range []string{
		"[a-z]+", "(ab|cd)*", "a(b(c))d", `\d{3}-\d{4}`, "((((a))))",
		strings.Repeat("(", 100) + "a" + strings.Repeat(")", 100),
	} {
		if err := validateSchemaRegexp(pat, false); err != nil {
			t.Errorf("%q: %v", pat, err)
		}
	}
}
