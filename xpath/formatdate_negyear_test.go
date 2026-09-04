package xpath

import "testing"

// formatDT evaluates a two-argument format-dateTime call under XPath 3.1.
func formatDT(t *testing.T, value, picture string) string {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	expr := `format-dateTime(xs:dateTime("` + value + `"), "` + picture + `")`
	seq, err := Eval(expr, ctx, nil)
	if err != nil {
		t.Fatalf("Eval(%s): %v", expr, err)
	}
	if len(seq) != 1 {
		t.Fatalf("Eval(%s): got %d items, want 1", expr, len(seq))
	}
	return seq[0].(interface{ String() string }).String()
}

// TestFormatDateTimeYearIsAbsoluteValue is a characterization test: it records
// that [Y] on a BCE year is CORRECT as written, not a dropped sign.
//
// It reads as a bug -- xs:dateTime("-1000000-06-15T12:00:00Z") formats under
// [Y] as "1000000" with no minus -- and it has been reported as one. It is
// not. The F&O 3.0 component table defines the specifier as
//
//	Y   year (absolute value)
//
// and gives the sign its own specifier on the next rows of the same table:
//
//	E   era: the name of a baseline for the numbering of years
//
// So the minus is not lost, it is not [Y]'s to render; a picture that wants
// the era asks for it, and "[Y] [E]" answers "1000000 bc". Making [Y] emit a
// leading minus would put the sign in twice for every picture that already
// names [E], and would contradict the table.
//
// This test exists so that the next reader of the "if y < 0 { y = -y }" line
// finds the rule rather than the surprise.
func TestFormatDateTimeYearIsAbsoluteValue(t *testing.T) {
	const bce = "-1000000-06-15T12:00:00Z"
	tests := []struct{ name, value, picture, want string }{
		{"[Y] is the absolute value", bce, "[Y]", "1000000"},
		{"[E] carries the sign", bce, "[Y] [E]", "1000000 bc"},
		{"[E] may precede the year", bce, "[E] [Y]", "bc 1000000"},
		{"a CE year reads the same way", "0044-03-15T12:00:00Z", "[Y] [E]", "44 ad"},
		{"a BCE year near zero", "-0044-03-15T12:00:00Z", "[Y] [E]", "44 bc"},
		// The absolute value is what the width modifier then truncates, so a
		// BCE year keeps the low-order digits of its magnitude like any other.
		{"width truncates the magnitude", "-0044-03-15T12:00:00Z", "[Y01]", "44"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDT(t, tt.value, tt.picture); got != tt.want {
				t.Errorf("format-dateTime(%s, %q) = %q, want %q",
					tt.value, tt.picture, got, tt.want)
			}
		})
	}
}
