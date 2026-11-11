package xpath

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// Env.MaxDigits refuses a cast from over-long text with FOCA0003 and an
// arithmetic result over the bound with FOAR0002, both as resource limits.
func TestMaxDigits(t *testing.T) {
	run := func(q string, digits int) error {
		c, err := CompileWith(q, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatal(err)
		}
		ctx := NewContext(nil, Builtins()).WithEnv(func(e *Env) { e.MaxDigits = digits })
		ctx = ctx.WithVar(xdm.QName{Local: "n"}, xdm.One(xdm.NewString(strings.Repeat("9", 101))))
		_, err = c.Eval(ctx)
		return err
	}
	for _, tc := range []struct{ q, code string }{
		{`xs:integer($n)`, "FOCA0003"},
		{`$n cast as xs:decimal`, "FOCA0003"},
		{`let $x := xs:integer(substring($n, 1, 60)) return $x * $x`, "FOAR0002"},
		// A duration component and fractional seconds go through big.Rat
		// as a decimal does, so the same bound applies to their text.
		{`xs:dayTimeDuration('PT' || $n || 'S')`, "FOCA0003"},
		{`('P' || $n || 'D') cast as xs:duration`, "FOCA0003"},
		{`xs:time('12:00:00.' || $n)`, "FOCA0003"},
		{`('2000-01-01T12:00:00.' || $n) cast as xs:dateTime`, "FOCA0003"},
	} {
		err := run(tc.q, 100)
		if err == nil || !strings.Contains(err.Error(), tc.code) || !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("%s: got %v, want %s as a resource limit", tc.q, err, tc.code)
		}
	}
	if err := run(`xs:integer(substring($n, 1, 100))`, 100); err != nil {
		t.Errorf("a number at the bound: %v", err)
	}
	if err := run(`xs:dayTimeDuration('PT' || substring($n, 1, 90) || 'S')`, 100); err != nil {
		t.Errorf("a duration under the bound: %v", err)
	}
	if err := run(`xs:integer($n)`, -1); err != nil {
		t.Errorf("unbounded: %v", err)
	}
}
