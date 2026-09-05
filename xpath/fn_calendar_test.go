package xpath

import "testing"

// calendarNS is a static namespace context for the calendar tests.
type calendarNS map[string]string

func (n calendarNS) ResolvePrefix(prefix string) (string, bool) {
	uri, ok := n[prefix]
	return uri, ok
}
func (calendarNS) DefaultElementNamespace() string { return "" }
func (calendarNS) DefaultFunctionNamespace() string {
	return "http://www.w3.org/2005/xpath-functions"
}

// TestFormatDateCalendarResolution pins the three ways a $calendar argument can
// name a namespace, which F&O 9.8.4.3 keeps apart:
//
//	"The calendar value if present must be a valid EQName ... If it is a
//	lexical QName then it is expanded into an expanded QName using the
//	statically known namespaces; if it has no prefix then it represents an
//	expanded-QName in no namespace. If the expanded QName is in no namespace,
//	then it must identify a calendar with a designator specified below
//	(dynamic error: [err:FOFD1340]). If the expanded QName is in a namespace
//	then it identifies the calendar in an implementation-defined way."
//
// So a prefix must be *resolved*, not merely parsed: whether the name lands in
// a namespace decides whether an unknown calendar is an error or a fallback,
// and a prefix with no binding has no expansion at all.
func TestFormatDateCalendarResolution(t *testing.T) {
	const xs = "http://www.w3.org/2001/XMLSchema"
	ns := calendarNS{"xs": xs,
		"cal": "http://calendar.example.com/non-existent-calendar"}

	cases := []struct {
		name     string
		calendar string
		wantErr  string // "" means the call must succeed
	}{
		// A prefix that is bound expands into a namespace, where which
		// calendar it names is implementation-defined. This one names none
		// that exists, so the fallback applies and the call succeeds --
		// QT3 format-date-en154 asserts exactly this.
		{"bound prefix", "'cal:CB'", ""},
		// A braced URI literal is in a namespace without needing a
		// resolver at all; QT3 format-date-en153.
		{"braced uri", "'Q{urn:example:x}CB'", ""},
		// A prefix with no binding has no expansion, so the argument is not
		// a usable calendar name. It must not be mistaken for an extension
		// in some namespace and silently accepted.
		{"unbound prefix", "'zz:thing'", "FOFD1340"},
		// No prefix means no namespace, where the name must be one of the
		// designators; QT3 format-date-en156.
		{"no namespace, unknown", "'ZODIAC'", "FOFD1340"},
		// Not an EQName at all; QT3 format-date-en157 and en158.
		{"malformed, empty prefix", "':w'", "FOFD1340"},
		{"malformed, bad local", "'Q{}1'", "FOFD1340"},
		// A supported designator in no namespace formats normally.
		{"no namespace, supported", "'ISO'", ""},
		{"braced empty, supported", "'Q{}ISO'", ""},
	}

	for _, fn := range []string{"format-date", "format-dateTime", "format-time"} {
		value := "xs:date('2006-03-01')"
		picture := "'[Y]'"
		switch fn {
		case "format-dateTime":
			value = "xs:dateTime('2006-03-01T12:00:00')"
		case "format-time":
			value, picture = "xs:time('12:00:00')", "'[H]'"
		}
		for _, c := range cases {
			t.Run(fn+"/"+c.name, func(t *testing.T) {
				expr := fn + "(" + value + ", " + picture + ", 'en', " + c.calendar + ", ())"
				ctx := NewContext(nil, Builtins())
				ctx.Version = XPath31
				_, err := Eval(expr, ctx, ns)
				switch {
				case c.wantErr == "" && err != nil:
					t.Fatalf("%s: unexpected error: %v", expr, err)
				case c.wantErr != "" && err == nil:
					t.Fatalf("%s: expected %s, got success", expr, c.wantErr)
				case c.wantErr != "" && !contains(err.Error(), c.wantErr):
					t.Fatalf("%s: expected %s, got %v", expr, c.wantErr, err)
				}
			})
		}
	}
}

// TestFormatDateCalendarPrefixNeedsStaticNamespaces pins the reason the
// resolution has to happen at evaluation time rather than in the parser: the
// calendar reaches the function as a string value, so the same expression text
// means different things under different static namespace contexts.
func TestFormatDateCalendarPrefixNeedsStaticNamespaces(t *testing.T) {
	const expr = `format-date(xs:date('2006-03-01'), '[Y]', 'en', 'cal:CB', ())`
	const xs = "http://www.w3.org/2001/XMLSchema"

	bound := calendarNS{"xs": xs, "cal": "http://calendar.example.com/none"}
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	if _, err := Eval(expr, ctx, bound); err != nil {
		t.Fatalf("with cal: bound, expected success, got %v", err)
	}

	unbound := calendarNS{"xs": xs}
	ctx = NewContext(nil, Builtins())
	ctx.Version = XPath31
	_, err := Eval(expr, ctx, unbound)
	if err == nil {
		t.Fatal("with cal: unbound, expected FOFD1340, got success")
	}
	if !contains(err.Error(), "FOFD1340") {
		t.Fatalf("with cal: unbound, expected FOFD1340, got %v", err)
	}
}
