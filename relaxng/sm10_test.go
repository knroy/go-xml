package relaxng

import (
	"strings"
	"testing"
)

// SECURITY (run-5). Stacked postfix operators and a datatype except chain
// nested without charging maxCompactDepth, so a long run was a fatal stack
// overflow rather than the depth error. Short runs still parse.
func TestSM10CompactPostfixAndExceptChargeDepth(t *testing.T) {
	const n = 5000
	for name, src := range map[string]string{
		"postfix": "start = empty" + strings.Repeat("?", n),
		"except":  "start = string" + strings.Repeat(" - string", n),
	} {
		if _, err := ParseCompact(src); err == nil || !strings.Contains(err.Error(), "deep") {
			t.Errorf("%s nested %d deep: got %v, want the depth error", name, n, err)
		}
	}
	for _, src := range []string{"start = empty?*+", "start = string - string - string"} {
		if _, err := ParseCompact(src); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
}
