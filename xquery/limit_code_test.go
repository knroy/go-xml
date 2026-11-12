package xquery_test

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xpath"
	"github.com/knroy/go-xml/v2/xquery"
)

// A valid input refused for a processor limit raises err:XPDY0130 (XPath 3.1
// §2.3.1), so a catch clause naming that code catches it. FORX0002, which the
// regex limits used to raise, and the code-less flatten refusal both escaped
// such a clause.
func TestResourceRefusalsCatch(t *testing.T) {
	prev := xpath.BacktrackingRegexEnabled()
	xpath.SetBacktrackingRegex(true)
	t.Cleanup(func() { xpath.SetBacktrackingRegex(prev) })
	for _, body := range []string{
		`matches("` + strings.Repeat("a", 60) + `", "(a*)*\1b")`,
		`matches(concat("b", string-join((1 to 50000) ! "c", "")), "(a*)b.*\1")`,
		`matches("a", string-join((1 to 150) ! "\pL{1000}", ""))`,
		`array:flatten(fold-left(1 to 25, [], function($a, $i) { [$a, $a] }))`,
	} {
		src := `try { ` + body + ` } catch err:XPDY0130 { "caught" }`
		got, err := run(t, src, xquery.Options{})
		if err != nil || got != "caught" {
			t.Errorf("%.80s: got %q, %.200v; want caught", body, got, err)
		}
	}
}
