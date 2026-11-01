package xpath

import (
	"strings"
	"testing"
)

// A sequence constructor or a "!" that copies its operand into a larger one
// is charged for the copies, so doubling it in a fold hits the item budget.
// The 23 doublings build 16.7M items against the 5M limit.
func TestSequenceCopiesAreCharged(t *testing.T) {
	for _, q := range []string{
		`fold-left(1 to 23, (1,2), function($s,$i){($s,$s)})`,
		`fold-left(1 to 23, (1,2), function($s,$i){$s ! (.,.)})`,
	} {
		c, err := CompileWith(q, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Eval(NewContext(nil, Builtins()))
		if err == nil || !strings.Contains(err.Error(), "XPDY0130") {
			t.Errorf("%s: got %v, want XPDY0130", q, err)
		}
	}
	// A legal modest sequence still evaluates, with and without the floor.
	for _, q := range []string{`count((1 to 1000, 1 to 1000))`, `count(((1,2),(3,4)))`, `count((1 to 1000) ! (., .))`} {
		c, _ := CompileWith(q, CompileOptions{Version: XPath31})
		if _, err := c.Eval(NewContext(nil, Builtins())); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
}
