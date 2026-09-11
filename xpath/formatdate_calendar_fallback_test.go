package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// F&O 3.0 section 9.8.4.3 draws a line the implementation had collapsed.
//
// A $calendar in no namespace "must identify a calendar with a designator
// specified below (dynamic error: err:FOFD1340)" -- the designators being the
// table of AD, AH, CB, ISO, OS and the rest that the section tabulates. A name
// that is not in that table is the error.
//
// A name that IS in the table but that the processor cannot compute is a
// different thing entirely, and not an error: "If the fallback representation
// uses a different calendar from that requested, the output string must
// identify the calendar actually used, for example by prefixing the string
// with [Calendar: X]".
//
// Both conditions raised FOFD1340, so a request for a perfectly valid calendar
// failed instead of falling back. The W3C suite asserts the fallback directly:
// format-date-en-033 formats 2006-03-01 with calendar "CB" (Cooch Behar) and
// expects "[Calendar: AD]03".
func TestFormatDateUnsupportedCalendarFallsBackWithMarker(t *testing.T) {
	const d = `xs:date("2006-03-01")`

	t.Run("tabulated but not computed takes the fallback", func(t *testing.T) {
		// The W3C suite case format-date-en-033, verbatim.
		if got := calFmt(t, `format-date(`+d+`, "[M01]", "en", "CB", ())`); got != "[Calendar: AD]03" {
			t.Errorf("calendar CB = %q, want %q", got, "[Calendar: AD]03")
		}
		// Old Style is the Julian calendar, tabulated and equally uncomputed.
		if got := calFmt(t, `format-date(`+d+`, "[M01]", "en", "OS", ())`); got != "[Calendar: AD]03" {
			t.Errorf("calendar OS = %q, want %q", got, "[Calendar: AD]03")
		}
	})

	t.Run("an extension calendar in a namespace also falls back", func(t *testing.T) {
		// A name in some namespace identifies a calendar in an
		// implementation-defined way; this implementation defines none, so
		// the substitution has to be admitted just the same.
		got := calFmt(t, `format-date(`+d+`, "[M01]", "en", "Q{http://example.com/cal}foo", ())`)
		if got != "[Calendar: AD]03" {
			t.Errorf("extension calendar = %q, want %q", got, "[Calendar: AD]03")
		}
	})

	t.Run("a calendar that is computed carries no marker", func(t *testing.T) {
		// The W3C suite case format-date-037 asserts the absence directly.
		for _, cal := range []string{`"AD"`, `"ISO"`, `"Q{}ISO"`, `()`} {
			got := calFmt(t, `format-date(`+d+`, "[M01]", "en", `+cal+`, ())`)
			if got != "03" {
				t.Errorf("calendar %s = %q, want %q", cal, got, "03")
			}
		}
	})

	t.Run("a name that is no designator at all is still FOFD1340", func(t *testing.T) {
		// "ZODIAC" is a legal NCName and not in the table, which is exactly
		// the condition the section gives the error code for.
		_, err := calEval(`format-date(` + d + `, "[M01]", "en", "ZODIAC", ())`)
		if err == nil || !strings.Contains(err.Error(), "FOFD1340") {
			t.Errorf("calendar ZODIAC error = %v, want FOFD1340", err)
		}
	})
}

func calEval(expr string) (string, error) {
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	seq, err := Eval(expr, ctx, nil)
	if err != nil {
		return "", err
	}
	return seq[0].(*xdm.Atomic).String(), nil
}

func calFmt(t *testing.T, expr string) string {
	t.Helper()
	got, err := calEval(expr)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return got
}
