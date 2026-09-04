package xpath

import (
	"math/big"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// formatWithPlace evaluates a five-argument format-dateTime call.
//
// The five-argument form is an XPath 3.0 addition, so the default 2.0 context
// reports XPST0017 for it. These tests are about $place, which only exists in
// that form, so they raise the version rather than assert an arity error.
func formatWithPlace(t *testing.T, expr string) string {
	t.Helper()
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	seq, err := Eval(expr, ctx, nil)
	if err != nil {
		t.Fatalf("Eval(%s): %v", expr, err)
	}
	if len(seq) != 1 {
		t.Fatalf("Eval(%s): got %d items, want 1", expr, len(seq))
	}
	return seq[0].(interface{ String() string }).String()
}

// TestFormatDateTimePlaceZoneNameIsExact is the load-bearing test for the
// $place zone lookup.
//
// applyPlace has to put the value on the timeline before it can ask a zone
// which offset applied at that moment, and the second count it uses for that
// must be the value's own. Taking it through big.Float instead does not merely
// blur a distant instant: big.Float.Int64 saturates at MaxInt64 and reports
// the clamp only through an Accuracy result that the call discarded. Every
// value past the int64 boundary therefore had its zone looked up at the same
// saturated instant -- 292277026596-12-04T15:30:07Z -- and whatever name and
// offset happened to apply *there* was attached to the value in hand.
//
// The window below is where that mattered. dateTimeFromSeconds guards the
// shifted count (utc + offset) with its own IsInt64 test, so for most
// overflowing values it refused afterwards and applyPlace fell back to
// returning the value unnamed. But a negative offset pulls the shifted count
// back under MaxInt64, so for a value overflowing by less than the offset the
// downstream guard passed and the fabricated name survived into the output.
//
// These are assertions about the rendered text, because that is the whole
// failure mode: a wrong zone name raises no error and reads as a plausible
// result.
func TestFormatDateTimePlaceZoneNameIsExact(t *testing.T) {
	const pic = `"[Y] [H01]:[m01]:[s01] [ZN] [Z]"`
	const nyc = `"America/New_York"`
	call := func(v string) string {
		return `format-dateTime(xs:dateTime("` + v + `"), ` + pic + `, (), (), ` + nyc + `)`
	}

	tests := []struct {
		name  string
		value string
		want  string
	}{
		// The normal path, which must keep working: an ordinary instant is
		// moved into the zone and named. September is daylight time, January
		// is standard time, and the two must not be confused.
		{
			name:  "ordinary instant in daylight time",
			value: "2020-09-13T12:26:40Z",
			want:  "2020 08:26:40 EDT -04:00",
		},
		{
			name:  "ordinary instant in standard time",
			value: "2020-01-13T12:26:40Z",
			want:  "2020 07:26:40 EST -05:00",
		},
		// A fractional second must not disturb the whole-second lookup.
		{
			name:  "fractional seconds",
			value: "2020-09-13T12:26:40.000000001Z",
			want:  "2020 08:26:40 EDT -04:00",
		},
		// Far from the epoch but still inside int64 seconds: the zone
		// database has no rule this far out and reports the location's mean
		// time, which is a real answer and must be preserved.
		//
		// The LMT offset below is New York's pre-standard-time mean solar
		// offset as the host's tzdata records it. It is the one expectation
		// here that a tzdata revision could legitimately move; a failure on
		// this case alone, with the rest passing, means the database changed
		// rather than the arithmetic.
		{
			name:  "year one million",
			value: "1000000-06-15T12:00:00Z",
			want:  "1000000 08:00:00 EDT -04:00",
		},
		{
			name:  "far negative year",
			value: "-1000000-06-15T12:00:00Z",
			want:  "1000000 07:04:00 LMT -04:56",
		},
		// The last instant whose UTC second count fits int64. This is the
		// saturation target itself, so it is the one value the broken code got
		// right, and it pins the boundary from below.
		{
			name:  "last second inside int64",
			value: "292277026596-12-04T15:00:00Z",
			want:  "292277026596 10:00:00 EST -05:00",
		},
		// One second past the boundary. The old code saturated to MaxInt64,
		// read EST/-05:00 off the clamped instant, and -- because -05:00 pulls
		// the shifted count back inside int64 -- passed the downstream guard,
		// emitting "10:30:08 EST -05:00" for a value five hours away from any
		// instant New York ever saw. No name is available here, so [ZN] must
		// fall back to the numeric offset of the value as it stands.
		{
			name:  "one second past int64 overflows the zone lookup",
			value: "292277026596-12-04T15:30:08Z",
			want:  "292277026596 15:30:08 GMT +00:00",
		},
		{
			name:  "well inside the saturation window",
			value: "292277026596-12-04T18:00:00Z",
			want:  "292277026596 18:00:00 GMT +00:00",
		},
		// Far past the boundary, where the downstream guard already refused
		// and the behaviour is unchanged. Kept so a future rewrite of either
		// guard cannot quietly start naming these.
		{
			name:  "far past int64",
			value: "1000000000000-06-15T12:00:00Z",
			want:  "1000000000000 12:00:00 GMT +00:00",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatWithPlace(t, call(tc.value)); got != tc.want {
				t.Errorf("format-dateTime(%s) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

// TestSplitSecondPrecondition pins the range splitSecond is allowed to assume.
//
// splitSecond narrows the whole part to int64 on the strength of Second being
// in [0,60). Nothing in its signature enforces that, so the guarantee lives in
// the constructors -- and this test is what notices if one of them stops
// providing it. It walks the paths that write the field: the parsers, the
// arithmetic that can borrow across a minute, and timezone adjustment.
func TestSplitSecondPrecondition(t *testing.T) {
	sixty := big.NewRat(60, 1)
	inRange := func(t *testing.T, label string, sec *big.Rat) {
		t.Helper()
		if sec == nil {
			t.Fatalf("%s: Second is nil", label)
		}
		if sec.Sign() < 0 || sec.Cmp(sixty) >= 0 {
			t.Errorf("%s: Second = %s, want [0,60)", label, sec)
		}
	}

	t.Run("parser", func(t *testing.T) {
		for _, s := range []string{
			"2020-01-01T00:00:00Z",
			"2020-01-01T23:59:59.999999999Z",
			"2020-01-01T24:00:00Z",
			"2020-01-01T00:00:59.5Z",
			"-4000-01-01T12:00:00.000001Z",
			"292277026596-12-04T15:30:08Z",
		} {
			dt, err := xdm.ParseDateTime(s, xdm.TypeDateTime)
			if err != nil {
				t.Fatalf("ParseDateTime(%q): %v", s, err)
			}
			inRange(t, s, dt.Second)
			whole, frac := splitSecond(dt.Second)
			if whole < 0 || whole > 59 {
				t.Errorf("%s: splitSecond whole = %d, want 0..59", s, whole)
			}
			if frac.Sign() < 0 || frac.Cmp(big.NewRat(1, 1)) >= 0 {
				t.Errorf("%s: splitSecond frac = %s, want [0,1)", s, frac)
			}
		}
	})

	t.Run("parser rejects out of range", func(t *testing.T) {
		// The invariant is upheld by refusal, not by clamping: a 60 that were
		// silently accepted would reach splitSecond as a valid-looking value.
		for _, s := range []string{
			"2020-01-01T00:00:60Z",
			"2020-01-01T00:00:61Z",
			"2020-01-01T00:00:99.9Z",
		} {
			if _, err := xdm.ParseDateTime(s, xdm.TypeDateTime); err == nil {
				t.Errorf("ParseDateTime(%q) succeeded, want a range error", s)
			}
		}
	})

	t.Run("arithmetic and adjustment", func(t *testing.T) {
		ctx := NewContext(nil, Builtins())
		ctx.Version = XPath31
		cases := []struct{ expr, want string }{
			// Carrying forward over a minute boundary.
			{`seconds-from-dateTime(xs:dateTime("2020-01-01T00:00:30.75Z") + xs:dayTimeDuration("PT29.5S"))`, "0.25"},
			// Borrowing backwards across one, which is the path that would
			// produce a negative Second if the remainder were not corrected.
			{`seconds-from-dateTime(xs:dateTime("2020-01-01T00:00:00.5Z") - xs:dayTimeDuration("PT0.75S"))`, "59.75"},
			{`seconds-from-dateTime(xs:dateTime("2020-01-01T00:00:30.75Z") - xs:dayTimeDuration("PT59.99S"))`, "30.76"},
			// A half-hour zone moves the minute but must leave the second be.
			{`seconds-from-dateTime(adjust-dateTime-to-timezone(xs:dateTime("2020-01-01T00:00:30.75Z"), xs:dayTimeDuration("-PT5H30M")))`, "30.75"},
		}
		for _, c := range cases {
			seq, err := Eval(c.expr, ctx, nil)
			if err != nil {
				t.Fatalf("Eval(%s): %v", c.expr, err)
			}
			got := seq[0].(interface{ String() string }).String()
			if got != c.want {
				t.Errorf("%s = %q, want %q", c.expr, got, c.want)
			}
			// The rendered decimal is the field itself, so parsing it back
			// checks the stored value rather than a formatting of it.
			r, ok := new(big.Rat).SetString(got)
			if !ok {
				t.Fatalf("%s: cannot read %q as a rational", c.expr, got)
			}
			inRange(t, c.expr, r)
		}
	})
}

// TestFormatDateTimeSecondsComponent covers the [s] and [f] components that
// splitSecond feeds, so that a change to the helper cannot pass unnoticed by
// altering only the rendered seconds.
func TestFormatDateTimeSecondsComponent(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	cases := []struct{ expr, want string }{
		{`format-dateTime(xs:dateTime("2020-01-01T10:20:30.125Z"), "[s01].[f001]")`, "30.125"},
		{`format-dateTime(xs:dateTime("2020-01-01T10:20:00.5Z"), "[s01].[f001]")`, "00.500"},
		{`format-dateTime(xs:dateTime("2020-01-01T10:20:59.999Z"), "[s01].[f001]")`, "59.999"},
		{`format-dateTime(xs:dateTime("2020-01-01T10:20:07Z"), "[s01].[f001]")`, "07.000"},
		// A fraction with more digits than the picture asks for is cut, not
		// carried into the whole part.
		{`format-dateTime(xs:dateTime("2020-01-01T10:20:59.9999Z"), "[s01].[f001]")`, "59.999"},
	}
	for _, c := range cases {
		seq, err := Eval(c.expr, ctx, nil)
		if err != nil {
			t.Fatalf("Eval(%s): %v", c.expr, err)
		}
		got := seq[0].(interface{ String() string }).String()
		if got != c.want {
			t.Errorf("%s = %q, want %q", strings.TrimPrefix(c.expr, "format-dateTime"), got, c.want)
		}
	}
}
