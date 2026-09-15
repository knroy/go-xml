package xpath

import (
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A parameter the spec declares WITHOUT "?" must raise XPTY0004 when it is
// given the empty sequence. Nothing in the call path enforces that on its
// own: registerFn records only a name and an arity, Function carries no
// parameter types, and builtinSignatures is consulted by "instance of
// function(...)" tests alone. The declared type is therefore enforced solely
// by which helper the body calls -- argString and argNumber both answer
// ("", nil) / (nil, nil) for an empty argument and leave the cardinality to
// the call site.
//
// fn:format-integer was already correct and is the reference: its $picture is
// declared exactly as format-date's is, and format-integer(1, ()) raised
// XPTY0004 while format-date(d, ()) returned "". Each row below pins the
// refusal, and every row carries a control proving the genuinely optional
// forms of the same function still work -- the fixes must not turn a declared
// "T?" into an error.
func TestRequiredParametersRejectEmptySequence(t *testing.T) {
	tests := []struct {
		name string
		// declared is the parameter's signature in F&O 3.1, with the section
		// it is taken from: it is the entire justification for the row.
		declared string
		empty    string // the required parameter given (): must be XPTY0004
		control  string // a form that must keep working
	}{
		{
			"fn:round $precision", "xs:integer (4.4.4)",
			`round(1.5, ())`,
			`round(1.55, 1)`,
		},
		{
			"fn:round-half-to-even $precision", "xs:integer (4.4.5)",
			`round-half-to-even(1.5, ())`,
			`round-half-to-even(1.55, 1)`,
		},
		{
			"fn:lang $node", "node() (13.4)",
			`lang("en", ())`,
			`lang("en", /p)`,
		},
		{
			"fn:error $description", "xs:string (3.1.1)",
			`fn:error(QName('http://www.w3.org/2005/xqt-errors','err:FOER0000'), ())`,
			// The control for fn:error is that it still raises its own code
			// rather than a type error; asserted separately below.
			`fn:error()`,
		},
		{
			"fn:trace $label", "xs:string (3.2.1)",
			`trace(42, ())`,
			`trace(42, "label")`,
		},
		{
			"fn:format-date $picture", "xs:string (9.8.2)",
			`format-date(xs:date('2020-01-01'), ())`,
			`format-date(xs:date('2020-01-01'), '[Y]')`,
		},
		{
			"fn:format-dateTime $picture", "xs:string (9.8.1)",
			`format-dateTime(xs:dateTime('2020-01-01T10:00:00'), ())`,
			`format-dateTime(xs:dateTime('2020-01-01T10:00:00'), '[Y]')`,
		},
		{
			"fn:format-time $picture", "xs:string (9.8.3)",
			`format-time(xs:time('10:00:00'), ())`,
			`format-time(xs:time('10:00:00'), '[H]')`,
		},
		{
			"fn:resolve-uri $base", "xs:string (6.1)",
			`resolve-uri("http://x/a", ())`,
			`resolve-uri("a", "http://x/b/")`,
		},
	}

	doc, err := xdm.ParseString(`<p xml:lang="en">x</p>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext(doc.Root, Builtins())
			ctx.Version = XPath31
			ctx.LibraryVersion = XPath31
			_, err := Eval(tt.empty, ctx, nil)
			if err == nil {
				t.Fatalf("%s was accepted; %s is declared without \"?\", so "+
					"the empty sequence must be XPTY0004 rather than a call "+
					"with a substituted default", tt.empty, tt.declared)
			}
			if code := xdm.ErrorCode(err); code != "XPTY0004" {
				t.Errorf("%s gave code %q (%v), want XPTY0004 (declared %s)",
					tt.empty, code, err, tt.declared)
			}

			// The control: the optional forms must be untouched by the fix.
			ctx = NewContext(doc.Root, Builtins())
			ctx.Version = XPath31
			ctx.LibraryVersion = XPath31
			_, err = Eval(tt.control, ctx, nil)
			if tt.name == "fn:error $description" {
				// fn:error always raises; the control is that it still
				// raises its OWN code and not a type error.
				if code := xdm.ErrorCode(err); code != "FOER0000" {
					t.Errorf("control %s gave code %q (%v), want FOER0000",
						tt.control, code, err)
				}
				return
			}
			if err != nil {
				t.Errorf("control %s was refused: %v", tt.control, err)
			}
		})
	}
}

// The optional parameters of the same functions, which are declared WITH "?"
// and must keep accepting the empty sequence. These are the rows a
// too-eager fix breaks: making every parameter required would turn each of
// these into XPTY0004.
func TestOptionalParametersStillAcceptEmptySequence(t *testing.T) {
	doc, err := xdm.ParseString(`<p xml:lang="en">x</p>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, tt := range []struct{ declared, expr string }{
		{"fn:round $arg as xs:numeric? (4.4.4)", `round(())`},
		{"fn:round-half-to-even $arg as xs:numeric? (4.4.5)", `round-half-to-even(())`},
		{"fn:lang $testlang as xs:string? (13.4)", `lang(())`},
		{"fn:trace $value as item()* (3.2.1)", `trace((), "label")`},
		{"fn:format-date $value as xs:date? (9.8.2)", `format-date((), '[Y]')`},
		{"fn:format-dateTime $value as xs:dateTime? (9.8.1)", `format-dateTime((), '[Y]')`},
		{"fn:format-time $value as xs:time? (9.8.3)", `format-time((), '[H]')`},
		{"fn:format-date $language/$calendar/$place as xs:string? (9.8.2)",
			`format-date(xs:date('2020-01-01'), '[Y]', (), (), ())`},
		{"fn:resolve-uri $relative as xs:string? (6.1)", `resolve-uri(())`},
		{"fn:resolve-uri one-argument form (6.1)", `resolve-uri("http://x/a")`},
	} {
		ctx := NewContext(doc.Root, Builtins())
		ctx.Version = XPath31
		ctx.LibraryVersion = XPath31
		if _, err := Eval(tt.expr, ctx, nil); err != nil {
			t.Errorf("%s was refused: %v (declared %s)", tt.expr, err, tt.declared)
		}
	}
}

// fn:round's precision defect was the most damaging of the set and gets its
// own assertion on the VALUE, not just the error code. roundWithPrecision did
// "if p != nil" with no else, so a nil precision left "requested" at 0: the
// call neither errored nor returned empty, it silently substituted precision
// 0, and round(1.55, ()) returned exactly what round(1.55) returns. A test
// that only checked for "an error" would still pass if the fix instead made
// the function return the empty sequence, so the accepted forms are pinned to
// their values here.
func TestRoundPrecisionNotSilentlyZero(t *testing.T) {
	for _, tt := range []struct{ expr, want string }{
		{`round(1.55, 1)`, "1.6"},
		{`round(1.55)`, "2"},
		{`round(1.5)`, "2"},
		{`round-half-to-even(1.55, 1)`, "1.6"},
		{`round-half-to-even(2.5)`, "2"},
	} {
		ctx := NewContext(nil, Builtins())
		ctx.Version = XPath31
		ctx.LibraryVersion = XPath31
		seq, err := Eval(tt.expr, ctx, nil)
		if err != nil {
			t.Errorf("%s: %v", tt.expr, err)
			continue
		}
		if len(seq) != 1 {
			t.Errorf("%s returned %d items, want 1", tt.expr, len(seq))
			continue
		}
		a, ok := seq[0].(*xdm.Atomic)
		if !ok {
			t.Errorf("%s: not an atomic value", tt.expr)
			continue
		}
		if got := a.String(); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.expr, got, tt.want)
		}
	}
}
