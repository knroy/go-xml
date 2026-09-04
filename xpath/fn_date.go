package xpath

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/knroy/go-xml/xdm"
)

// dateAccessorType is the type an accessor's single argument is declared as,
// read off the function's own name.
//
// F&O 3.0 §9.5 declares each component accessor over exactly one of the three
// calendar types — fn:month-from-date($arg as xs:date?), and correspondingly
// for -from-dateTime and -from-time — so the name determines what the
// function conversion rules must cast an xs:untypedAtomic argument to.
func dateAccessorType(name string) xdm.TypeCode {
	switch {
	case strings.HasSuffix(name, "-from-dateTime"):
		return xdm.TypeDateTime
	case strings.HasSuffix(name, "-from-date"):
		return xdm.TypeDate
	case strings.HasSuffix(name, "-from-time"):
		return xdm.TypeTime
	}
	return xdm.TypeDateTime
}

// dateAccessorArg applies the function conversion rules to a component
// accessor's argument.
//
// XPath 3.0 §3.1.5.2 states the rules for a parameter whose declared type is
// an atomic type: "If the expected type is an atomic type ... each item in the
// given sequence that is of type xs:untypedAtomic is cast to the expected
// atomic type." An unvalidated document's content is xs:untypedAtomic, so
// month-from-date(@date) on such a document must cast to xs:date and then take
// the component, not reject the argument (app-UseCaseR/rdb-queries-results-q9).
//
// The cast is confined to xs:untypedAtomic. The same clause promotes only
// numerics and anyURI; an xs:string is neither cast nor promoted to xs:date,
// so month-from-date("2008-06-01") stays the XPTY0004 the suite expects.
func dateAccessorArg(a *xdm.Atomic, want xdm.TypeCode, name string) (*xdm.Atomic, error) {
	if a.Type == xdm.TypeUntypedAtomic {
		conv, err := CastAtomic(a, want)
		if err != nil {
			return nil, xdm.ErrType(
				"%s(): %q is not a valid %s", name, a.String(), want.String())
		}
		a = conv
	}
	if a.DateTimeVal() == nil {
		return nil, xdm.ErrType(
			"%s(): expected a date/time value, got %s", name, a.TypeName())
	}
	return a, nil
}

// registerDateFuncs adds the date/time accessor and adjustment functions.
func registerDateFuncs(l *Library) {
	// The component accessors. Each returns the empty sequence for an empty
	// argument, which is what makes them safe to apply to an optional element.
	dateComponent := func(name string, get func(*xdm.DateTime) xdm.Item) {
		want := dateAccessorType(name)
		l.registerFn(name, []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
			atoms := xdm.Atomize(args[0])
			if len(atoms) == 0 {
				return xdm.Empty(), nil
			}
			it, err := atoms.Single()
			if err != nil {
				return nil, err
			}
			a := it.(*xdm.Atomic)
			a, err = dateAccessorArg(a, want, name)
			if err != nil {
				return nil, err
			}
			return xdm.One(get(a.DateTimeVal())), nil
		})
	}

	dateComponent("year-from-date", func(d *xdm.DateTime) xdm.Item { return xdm.NewInteger(int64(d.Year)) })
	dateComponent("month-from-date", func(d *xdm.DateTime) xdm.Item { return xdm.NewInteger(int64(d.Month)) })
	dateComponent("day-from-date", func(d *xdm.DateTime) xdm.Item { return xdm.NewInteger(int64(d.Day)) })
	dateComponent("year-from-dateTime", func(d *xdm.DateTime) xdm.Item { return xdm.NewInteger(int64(d.Year)) })
	dateComponent("month-from-dateTime", func(d *xdm.DateTime) xdm.Item { return xdm.NewInteger(int64(d.Month)) })
	dateComponent("day-from-dateTime", func(d *xdm.DateTime) xdm.Item { return xdm.NewInteger(int64(d.Day)) })
	dateComponent("hours-from-dateTime", func(d *xdm.DateTime) xdm.Item { return xdm.NewInteger(int64(d.Hour)) })
	dateComponent("minutes-from-dateTime", func(d *xdm.DateTime) xdm.Item { return xdm.NewInteger(int64(d.Minute)) })
	dateComponent("seconds-from-dateTime", func(d *xdm.DateTime) xdm.Item { return xdm.NewDecimal(d.Second) })
	dateComponent("hours-from-time", func(d *xdm.DateTime) xdm.Item { return xdm.NewInteger(int64(d.Hour)) })
	dateComponent("minutes-from-time", func(d *xdm.DateTime) xdm.Item { return xdm.NewInteger(int64(d.Minute)) })
	dateComponent("seconds-from-time", func(d *xdm.DateTime) xdm.Item { return xdm.NewDecimal(d.Second) })

	// Timezone accessors return a dayTimeDuration, or the empty sequence when
	// the value carries no timezone — which is exactly how a stylesheet tests
	// for one.
	tzAccessor := func(name string) {
		want := dateAccessorType(name)
		l.registerFn(name, []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
			atoms := xdm.Atomize(args[0])
			if len(atoms) == 0 {
				return xdm.Empty(), nil
			}
			it, err := atoms.Single()
			if err != nil {
				return nil, err
			}
			a := it.(*xdm.Atomic)
			a, err = dateAccessorArg(a, want, name)
			if err != nil {
				return nil, err
			}
			dt := a.DateTimeVal()
			if !dt.HasTZ {
				return xdm.Empty(), nil
			}
			d := &xdm.Duration{
				Negative: dt.TZOffset < 0,
				Seconds:  new(big.Rat).SetInt64(int64(abs(dt.TZOffset)) * 60),
			}
			return xdm.One(xdm.NewDuration(d, xdm.TypeDayTimeDuration)), nil
		})
	}
	tzAccessor("timezone-from-date")
	tzAccessor("timezone-from-dateTime")
	tzAccessor("timezone-from-time")

	// Duration component accessors.
	durComponent := func(name string, get func(*xdm.Duration) xdm.Item) {
		l.registerFn(name, []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
			atoms := xdm.Atomize(args[0])
			if len(atoms) == 0 {
				return xdm.Empty(), nil
			}
			it, err := atoms.Single()
			if err != nil {
				return nil, err
			}
			a := it.(*xdm.Atomic)
			if a.DurationVal() == nil {
				return nil, xdm.ErrType("%s(): expected a duration, got %s", name, a.TypeName())
			}
			return xdm.One(get(a.DurationVal())), nil
		})
	}

	durComponent("years-from-duration", func(d *xdm.Duration) xdm.Item {
		return xdm.NewInteger(int64(d.SignedMonths() / 12))
	})
	durComponent("months-from-duration", func(d *xdm.Duration) xdm.Item {
		return xdm.NewInteger(int64(d.SignedMonths() % 12))
	})
	// Every component but the day count is reduced modulo its own unit, so
	// the answer is small however large the duration is. ratTrunc used to
	// narrow to int64 BEFORE that modulo, which threw away exactly the bits
	// the modulo needed: hours-from-duration of PT10000000000000000000H
	// answered 0, and days-from-duration of P10000000000000000000D came back
	// negative. The quotient now stays a big.Int until after the reduction.
	durComponent("days-from-duration", func(d *xdm.Duration) xdm.Item {
		// The day count has no modulo to shrink it, so it is returned as
		// the arbitrary-precision xs:integer it is rather than narrowed.
		return xdm.NewIntegerFromRat(
			new(big.Rat).SetInt(ratTrunc(d.SignedSeconds(), 86400)))
	})
	durComponent("hours-from-duration", func(d *xdm.Duration) xdm.Item {
		return xdm.NewInteger(bigMod(ratTrunc(d.SignedSeconds(), 3600), 24))
	})
	durComponent("minutes-from-duration", func(d *xdm.Duration) xdm.Item {
		return xdm.NewInteger(bigMod(ratTrunc(d.SignedSeconds(), 60), 60))
	})
	durComponent("seconds-from-duration", func(d *xdm.Duration) xdm.Item {
		secs := d.SignedSeconds()
		whole := ratTrunc(secs, 1)
		// The fraction is what the truncation dropped, so it is taken
		// against the exact quotient, not against the reduced one.
		frac := new(big.Rat).Sub(secs, new(big.Rat).SetInt(whole))
		return xdm.NewDecimal(new(big.Rat).Add(
			new(big.Rat).SetInt64(bigMod(whole, 60)), frac))
	})
}

// ratTrunc returns r divided by unit, truncated toward zero.
//
// The quotient is exact: narrowing it here would discard the high bits that a
// caller's modulo still needs.
func ratTrunc(r *big.Rat, unit int64) *big.Int {
	q := new(big.Rat).Quo(r, new(big.Rat).SetInt64(unit))
	return new(big.Int).Quo(q.Num(), q.Denom())
}

// bigMod reduces n modulo unit, keeping the sign of n as Go's % does.
//
// The result is bounded by unit, so returning an int64 is safe whatever n is.
func bigMod(n *big.Int, unit int64) int64 {
	return new(big.Int).Rem(n, big.NewInt(unit)).Int64()
}

// registerTimezoneAdjust adds the three fn:adjust-*-to-timezone functions.
//
// They exist because an XML Schema date carries an optional timezone, so
// "the same instant in another zone" and "the same wall-clock reading with a
// different zone label" are different operations. These implement the former:
// the value is shifted so it denotes the same instant, then relabelled.
//
// With one argument the implicit timezone from the dynamic context is used;
// with two, an empty second argument *removes* the timezone rather than
// defaulting it, which is how a stylesheet strips a zone it does not want.
func registerTimezoneAdjust(l *Library) {
	adjust := func(name string, t xdm.TypeCode) {
		l.registerFn(name, []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
			atoms := xdm.Atomize(args[0])
			if len(atoms) == 0 {
				return xdm.Empty(), nil
			}
			it, err := atoms.Single()
			if err != nil {
				return nil, err
			}
			a := it.(*xdm.Atomic)
			src := a.DateTimeVal()
			if src == nil {
				conv, err := CastAtomic(a, t)
				if err != nil {
					return nil, err
				}
				a, src = conv, conv.DateTimeVal()
			}
			if src == nil {
				return nil, xdm.ErrType("%s: expected a date/time value, got %s", name, a.TypeName())
			}

			// Resolve the target offset, distinguishing "argument absent"
			// (use the implicit timezone) from "argument is the empty
			// sequence" (remove the timezone).
			targetTZ := ctx.ImplicitTimezone
			removeTZ := false
			if len(args) > 1 {
				tzAtoms := xdm.Atomize(args[1])
				if len(tzAtoms) == 0 {
					removeTZ = true
				} else {
					d := tzAtoms[0].(*xdm.Atomic).DurationVal()
					if d == nil {
						conv, err := CastAtomic(tzAtoms[0].(*xdm.Atomic), xdm.TypeDayTimeDuration)
						if err != nil {
							return nil, err
						}
						d = conv.DurationVal()
					}
					secs := d.SignedSeconds()
					if !secs.IsInt() {
						return nil, fmt.Errorf("FODT0003: timezone offset must be a whole number of minutes")
					}
					// The range check comes BEFORE the narrowing. Dividing
					// a narrowed second count by 60 let an offset of
					// 129127208515966879312 seconds — four trillion years —
					// wrap into +05:00 and be accepted as an ordinary zone.
					bigMins := new(big.Int).Quo(secs.Num(), big.NewInt(60))
					if bigMins.CmpAbs(big.NewInt(14*60)) > 0 {
						return nil, fmt.Errorf(
							"FODT0003: timezone offset %s minutes is out of range", bigMins)
					}
					targetTZ = int(bigMins.Int64())
				}
			}

			out := *src
			switch {
			case removeTZ:
				out.HasTZ, out.TZOffset = false, 0
			case !src.HasTZ:
				// An unzoned value is simply labelled with the target zone;
				// its wall-clock reading does not move, because there was no
				// instant to preserve.
				out.HasTZ, out.TZOffset = true, targetTZ
			default:
				// A zoned value is shifted so it denotes the same instant.
				shifted, err := shiftToTimezone(src, targetTZ)
				if err != nil {
					return nil, err
				}
				out = *shifted
			}
			// xs:date has no time part to carry, so an adjustment that would
			// change the clock only changes the label.
			if t == xdm.TypeDate {
				out.Hour, out.Minute = 0, 0
				out.Second = new(big.Rat)
			}
			return xdm.One(xdm.NewDateTime(&out, t)), nil
		})
	}

	adjust("adjust-dateTime-to-timezone", xdm.TypeDateTime)
	adjust("adjust-date-to-timezone", xdm.TypeDate)
	adjust("adjust-time-to-timezone", xdm.TypeTime)
}

// shiftToTimezone re-expresses a zoned date/time in another timezone so that
// it denotes the same instant.
//
// The conversion goes through the UTC-normalised timeline rather than adding
// the offset difference to the fields directly, because an hour shift can roll
// the date across a month or year boundary and the field arithmetic for that
// is exactly what dateTimeFromSeconds already implements.
func shiftToTimezone(src *xdm.DateTime, targetTZ int) (*xdm.DateTime, error) {
	utc := src.ToSeconds(0)
	return dateTimeFromSeconds(utc, true, targetTZ)
}

// registerCurrentDateTime adds fn:current-dateTime, fn:current-date and
// fn:current-time.
//
// All three read the single timestamp captured in the dynamic context rather
// than the system clock, so every call within one transform agrees. Without a
// clock set they raise rather than inventing one, which keeps a stylesheet
// from silently depending on wall time in a context that was meant to be
// deterministic.
func registerCurrentDateTime(l *Library) {
	current := func(name string, t xdm.TypeCode) {
		l.registerFn(name, []int{0}, func(ctx *Context, _ []xdm.Sequence) (xdm.Sequence, error) {
			if !ctx.HasNow {
				return nil, fmt.Errorf(
					"FODC0001: %s() is unavailable (no transform clock configured)", name)
			}
			dt := dateTimeFromGoTime(ctx.Now, ctx.ImplicitTimezone)
			if t == xdm.TypeDate {
				dt.Hour, dt.Minute = 0, 0
				dt.Second = new(big.Rat)
			}
			return xdm.One(xdm.NewDateTime(dt, t)), nil
		})
	}
	current("current-dateTime", xdm.TypeDateTime)
	current("current-date", xdm.TypeDate)
	current("current-time", xdm.TypeTime)
}

// dateTimeFromGoTime converts a Go time into an XDM date/time carrying the
// given offset, with nanosecond precision preserved as an exact fraction.
func dateTimeFromGoTime(t time.Time, tzMinutes int) *xdm.DateTime {
	t = t.UTC().Add(time.Duration(tzMinutes) * time.Minute)
	sec := new(big.Rat).SetInt64(int64(t.Second()))
	if ns := t.Nanosecond(); ns != 0 {
		sec.Add(sec, new(big.Rat).SetFrac64(int64(ns), 1_000_000_000))
	}
	return &xdm.DateTime{
		Year: t.Year(), Month: int(t.Month()), Day: t.Day(),
		Hour: t.Hour(), Minute: t.Minute(), Second: sec,
		HasTZ: true, TZOffset: tzMinutes,
	}
}
