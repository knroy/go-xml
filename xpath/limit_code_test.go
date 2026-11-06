package xpath

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A valid input refused for a processor limit is XPDY0130 (XPath 3.1 §2.3.1:
// "limitations may exist on the maximum numbers or sizes of various objects.
// An error must be raised if such a limitation is exceeded [err:XPDY0130]"),
// not FORX0002, which F&O reserves for a syntactically invalid pattern. The
// code is what a catch clause selects on; xquery's TestResourceRefusalsCatch
// drives the same cases through try/catch.
func TestResourceRefusalsAreXPDY0130(t *testing.T) {
	enableBacktracking(t)
	for _, c := range resourceRefusalCases {
		err := evalErrVersion(t, testDoc, c.expr, XPath31)
		if got := xdm.ErrorCode(err); got != "XPDY0130" {
			t.Errorf("%s: ErrorCode = %q, want XPDY0130 (err %.200v)", c.name, got, err)
		}
		if !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("%s: %.200v does not wrap xdm.ErrResourceLimit", c.name, err)
		}
	}
}

var resourceRefusalCases = []struct{ name, expr string }{
	{"backtrack step budget", `matches("` + strings.Repeat("a", 60) + `", "` + pathological + `")`},
	{"backtrack depth budget", `matches(concat("b", string-join((1 to 50000) ! "c", "")), "(a*)b.*\1")`},
	{"program size", `matches("a", string-join((1 to 150) ! "\pL{1000}", ""))`},
	{"array flatten steps", `Q{http://www.w3.org/2005/xpath-functions/array}flatten(fold-left(1 to 25, [], function($a, $i) { [$a, $a] }))`},
}

// The backreference analysis bound is a limit on a valid pattern too.
func TestBackrefAnalysisRefusalIsXPDY0130(t *testing.T) {
	var g, r strings.Builder
	for i := 1; i <= 4000; i++ {
		g.WriteString("(a)")
		r.WriteString(`\` + strconv.Itoa(i))
	}
	_, err := buildBackrefRegexp(g.String()+r.String(), "", XPath31)
	if got := xdm.ErrorCode(err); got != "XPDY0130" {
		t.Fatalf("ErrorCode = %q, want XPDY0130 (err %v)", got, err)
	}
}
