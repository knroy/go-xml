package xpath

import "testing"

// Each general comparison operator applies its own value comparison to every
// pair, and the range shortcut is handed the same one; the operator-to-value-
// comparison mapping is a switch, and a wrong arm would show here.
func TestGeneralComparisonOperators(t *testing.T) {
	for _, c := range []struct {
		expr string
		want bool
	}{
		{"(1, 2) = (2, 3)", true},
		{"(1, 2) = (3, 4)", false},
		{"(1, 1) != (1, 1)", false},
		{"(1, 1) != (1, 2)", true},
		{"(3, 4) < (1, 2)", false},
		{"(3, 4) < (1, 3)", false},
		{"(3, 4) < (1, 5)", true},
		{"(3, 4) <= (1, 3)", true},
		{"(3, 4) <= (1, 2)", false},
		{"(1, 2) > (2, 3)", false},
		{"(1, 3) > (2, 3)", true},
		{"(1, 2) >= (2, 3)", true},
		{"(1, 2) >= (3, 4)", false},
		{"5 = (1 to 10)", true},
		{"11 = (1 to 10)", false},
		{"1 != (1 to 1)", false},
		{"0 < (1 to 10)", true},
		{"10 < (1 to 10)", false},
		{"10 <= (1 to 10)", true},
		{"1 > (1 to 10)", false},
		{"2 > (1 to 10)", true},
		{"0 >= (1 to 10)", false},
		{"'b' > ('a', 'c')", true},
	} {
		v, err := MustCompile(c.expr, nil).Eval(NewContext(nil, Builtins()))
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		got, err := EffectiveBooleanValue(v)
		if err != nil || got != c.want {
			t.Errorf("%s = %v (%v), want %v", c.expr, got, err, c.want)
		}
	}
}
