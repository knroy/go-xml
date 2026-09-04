package xpath

import (
	"errors"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A parameter the spec declares as a singleton must refuse a two-item
// argument. Taking atoms[0] instead answered for the first item, which is a
// wrong answer rather than a refused one: format-number((1,2), '0') gave "1".
//
// Each case gives the same function zero, one and two items, so the
// zero-and-one rows pin the behaviour the fix must NOT change while the
// two-item row pins the fix itself. The error code is asserted, not merely
// the presence of an error -- XPTY0004 is what the function conversion rules
// give, and a case that failed for some other reason would otherwise pass.
func TestSingletonParametersRejectTwoItems(t *testing.T) {
	tests := []struct {
		name string
		// declared is the parameter's signature in the spec, recorded here
		// because it is the whole justification for the two-item row.
		declared string
		zero     string // an empty argument: must not error
		one      string // one item: must not error
		two      string // two items: must be XPTY0004
	}{
		{
			"format-number $value", "numeric?",
			`format-number((), '0')`,
			`format-number(1, '0')`,
			`format-number((1,2), '0')`,
		},
		{
			"format-number $picture", "xs:string",
			`format-number(1, '0')`,
			`format-number(1, '0')`,
			`format-number(1, ('0','0'))`,
		},
		{
			"format-integer $value", "xs:integer?",
			`format-integer((), '1')`,
			`format-integer(1, '1')`,
			`format-integer((1,2), '1')`,
		},
		{
			"format-date $value", "xs:date?",
			`format-date((), '[Y]')`,
			`format-date(xs:date('2020-01-01'), '[Y]')`,
			`format-date((xs:date('2020-01-01'),xs:date('2021-01-01')), '[Y]')`,
		},
		{
			"format-dateTime $place", "xs:string?",
			`format-dateTime(xs:dateTime('2020-01-01T00:00:00'), '[Y]', (), (), ())`,
			`format-dateTime(xs:dateTime('2020-01-01T00:00:00'), '[Y]', (), (), 'UTC')`,
			`format-dateTime(xs:dateTime('2020-01-01T00:00:00'), '[Y]', (), (), ('UTC','UTC'))`,
		},
		{
			"dateTime $arg1", "xs:date?",
			`dateTime((), xs:time('00:00:00'))`,
			`dateTime(xs:date('2020-01-01'), xs:time('00:00:00'))`,
			`dateTime((xs:date('2020-01-01'),xs:date('2021-01-01')), xs:time('00:00:00'))`,
		},
		{
			"dateTime $arg2", "xs:time?",
			`dateTime(xs:date('2020-01-01'), ())`,
			`dateTime(xs:date('2020-01-01'), xs:time('00:00:00'))`,
			`dateTime(xs:date('2020-01-01'), (xs:time('00:00:00'),xs:time('01:00:00')))`,
		},
		{
			"fn:error $code", "xs:QName?",
			// error(()) is FOER0000 from 3.1, so the zero row is covered by
			// the dedicated test below rather than here; repeat the one-item
			// expression so this row still exercises the accepted shape.
			`fn:error(QName('http://www.w3.org/2005/xqt-errors','err:FOER0000'))`,
			`fn:error(QName('http://www.w3.org/2005/xqt-errors','err:FOER0000'))`,
			`fn:error((QName('http://www.w3.org/2005/xqt-errors','err:FOER0000'),QName('http://www.w3.org/2005/xqt-errors','err:FOER0000')))`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// fn:error always raises, so its accepted rows are judged only on
			// the code they raise, not on succeeding.
			isError := strings.HasPrefix(tt.name, "fn:error")

			for _, row := range []struct {
				label, expr string
			}{{"zero", tt.zero}, {"one", tt.one}} {
				ctx := NewContext(nil, Builtins())
				ctx.Version = XPath31
				_, err := Eval(row.expr, ctx, nil)
				if isError {
					if code := xdm.ErrorCode(err); code != "FOER0000" {
						t.Errorf("%s item(s) %q: got %v (code %q), want FOER0000",
							row.label, row.expr, err, code)
					}
					continue
				}
				if err != nil {
					t.Errorf("%s item(s) %q was refused: %v (declared %s)",
						row.label, row.expr, err, tt.declared)
				}
			}

			ctx := NewContext(nil, Builtins())
			ctx.Version = XPath31
			_, err := Eval(tt.two, ctx, nil)
			if err == nil {
				t.Fatalf("two items %q was accepted; %s is a singleton, so it "+
					"must be XPTY0004 rather than a call on the first item",
					tt.two, tt.declared)
			}
			if code := xdm.ErrorCode(err); code != "XPTY0004" {
				t.Errorf("two items %q gave code %q (%v), want XPTY0004",
					tt.two, code, err)
			}
		})
	}
}

// The helpers themselves, at their three cardinalities. The table above goes
// through Eval and so proves the call sites are wired up; this proves the
// helpers' own contract, including that "required" differs from "optional"
// exactly on the empty sequence.
func TestArgAtomicCardinalityHelpers(t *testing.T) {
	empty := xdm.Empty()
	one := xdm.One(xdm.NewInteger(1))
	two := xdm.Sequence{xdm.NewInteger(1), xdm.NewInteger(2)}

	t.Run("optional", func(t *testing.T) {
		if a, err := argAtomicOptional([]xdm.Sequence{empty}, 0, "f"); err != nil || a != nil {
			t.Errorf("empty = (%v, %v), want (nil, nil)", a, err)
		}
		if a, err := argAtomicOptional([]xdm.Sequence{one}, 0, "f"); err != nil || a == nil {
			t.Errorf("one = (%v, %v), want an atomic and no error", a, err)
		}
		_, err := argAtomicOptional([]xdm.Sequence{two}, 0, "f")
		if code := xdm.ErrorCode(err); code != "XPTY0004" {
			t.Errorf("two gave code %q (%v), want XPTY0004", code, err)
		}
		// An absent argument is the empty sequence, not an index panic.
		if a, err := argAtomicOptional(nil, 0, "f"); err != nil || a != nil {
			t.Errorf("absent = (%v, %v), want (nil, nil)", a, err)
		}
	})

	t.Run("required", func(t *testing.T) {
		_, err := argAtomicRequired([]xdm.Sequence{empty}, 0, "f")
		if code := xdm.ErrorCode(err); code != "XPTY0004" {
			t.Errorf("empty gave code %q (%v), want XPTY0004", code, err)
		}
		if a, err := argAtomicRequired([]xdm.Sequence{one}, 0, "f"); err != nil || a == nil {
			t.Errorf("one = (%v, %v), want an atomic and no error", a, err)
		}
		_, err = argAtomicRequired([]xdm.Sequence{two}, 0, "f")
		if code := xdm.ErrorCode(err); code != "XPTY0004" {
			t.Errorf("two gave code %q (%v), want XPTY0004", code, err)
		}
	})

	// A function item cannot be atomized, so both helpers report FOTY0013
	// rather than dropping it the way plain Atomize does. This is why they
	// use AtomizeChecked.
	t.Run("function item is FOTY0013", func(t *testing.T) {
		fn := xdm.Sequence{&xdm.FunctionItem{Name: xdm.QName{Local: "f"}, Arity: 1}}
		for _, c := range []struct {
			name string
			err  error
		}{
			{"optional", second(argAtomicOptional([]xdm.Sequence{fn}, 0, "f"))},
			{"required", second(argAtomicRequired([]xdm.Sequence{fn}, 0, "f"))},
		} {
			if code := xdm.ErrorCode(c.err); code != "FOTY0013" {
				t.Errorf("%s gave code %q (%v), want FOTY0013", c.name, code, c.err)
			}
		}
	})
}

func second(_ *xdm.Atomic, err error) error { return err }

// fn:error's empty-sequence behaviour is version-gated, so it gets its own
// test rather than a row in the table: XPTY0004 up to 3.0, FOER0000 from 3.1.
// Migrating the $code argument to the cardinality helper must not disturb it.
func TestErrorEmptyCodeUnchanged(t *testing.T) {
	for _, tt := range []struct {
		version Version
		want    string
	}{
		{XPath30, "XPTY0004"},
		{XPath31, "FOER0000"},
	} {
		ctx := NewContext(nil, Builtins())
		ctx.Version = tt.version
		_, err := Eval(`fn:error(())`, ctx, nil)
		if code := xdm.ErrorCode(err); code != tt.want {
			t.Errorf("error(()) at %v gave code %q (%v), want %q",
				tt.version, code, err, tt.want)
		}
	}
}

// A resource refusal must be distinguishable from a semantic fault WITHOUT
// losing the code that callers and the suites already match on. Both halves
// are asserted together, because wrapping that replaced the code would still
// satisfy errors.Is and wrapping that dropped the sentinel would still
// satisfy the code check.
func TestResourceLimitSentinelKeepsItsCode(t *testing.T) {
	tests := []struct {
		name     string
		eval     func() error
		wantCode string
		wantText string
	}{
		{
			"parse depth", func() error {
				_, err := Parse(strings.Repeat("(", 2000)+"1"+
					strings.Repeat(")", 2000), nil)
				return err
			},
			"XPST0003", "nesting exceeds",
		},
		{
			"item budget", func() error {
				ctx := NewContext(nil, Builtins())
				ctx.Version = XPath31
				_, err := Eval(
					`count(for $a in 1 to 3000, $b in 1 to 3000 return 1)`, ctx, nil)
				return err
			},
			"XPDY0130", "items",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.eval()
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if !errors.Is(err, xdm.ErrResourceLimit) {
				t.Errorf("errors.Is(%v, ErrResourceLimit) = false; a caller "+
					"cannot tell this refusal from a malformed expression", err)
			}
			if code := xdm.ErrorCode(err); code != tt.wantCode {
				t.Errorf("code = %q, want %q (wrapping must add the sentinel, "+
					"not replace the code)", code, tt.wantCode)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("message %q no longer contains %q; tests that match "+
					"on message text would break", err, tt.wantText)
			}
		})
	}
}

// A semantic fault must NOT carry the sentinel, or the distinction the
// sentinel exists to draw is worthless.
func TestSemanticErrorIsNotAResourceLimit(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx.Version = XPath31
	_, err := Eval(`format-number((1,2), '0')`, ctx, nil)
	if err == nil {
		t.Fatal("expected a type error")
	}
	if errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("a type error %v reports as a resource limit", err)
	}
}
