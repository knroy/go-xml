package xpath

import (
	"fmt"
	"math/big"
	"time"

	"github.com/knroy/go-xml/xdm"
)

// registerDateFuncs adds the date/time accessor and adjustment functions.
func registerDateFuncs(l *Library) {
	// The component accessors. Each returns the empty sequence for an empty
	// argument, which is what makes them safe to apply to an optional element.
	dateComponent := func(name string, get func(*xdm.DateTime) xdm.Item) {
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
			if a.DateTimeVal() == nil {
				return nil, xdm.ErrType("%s(): expected a date/time value, got %s", name, a.TypeName())
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
			dt := a.DateTimeVal()
			if dt == nil {
				return nil, xdm.ErrType("%s(): expected a date/time value", name)
			}
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
	durComponent("days-from-duration", func(d *xdm.Duration) xdm.Item {
		return xdm.NewInteger(ratTrunc(d.SignedSeconds(), 86400))
	})
	durComponent("hours-from-duration", func(d *xdm.Duration) xdm.Item {
		return xdm.NewInteger(ratTrunc(d.SignedSeconds(), 3600) % 24)
	})
	durComponent("minutes-from-duration", func(d *xdm.Duration) xdm.Item {
		return xdm.NewInteger(ratTrunc(d.SignedSeconds(), 60) % 60)
	})
	durComponent("seconds-from-duration", func(d *xdm.Duration) xdm.Item {
		secs := d.SignedSeconds()
		whole := ratTrunc(secs, 1)
		frac := new(big.Rat).Sub(secs, new(big.Rat).SetInt64(whole))
		return xdm.NewDecimal(new(big.Rat).Add(new(big.Rat).SetInt64(whole%60), frac))
	})
}

// ratTrunc returns r divided by unit, truncated toward zero.
func ratTrunc(r *big.Rat, unit int64) int64 {
	q := new(big.Rat).Quo(r, new(big.Rat).SetInt64(unit))
	return new(big.Int).Quo(q.Num(), q.Denom()).Int64()
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
					mins := secs.Num().Int64() / 60
					if mins < -14*60 || mins > 14*60 {
						return nil, fmt.Errorf("FODT0003: timezone offset %d minutes is out of range", mins)
					}
					targetTZ = int(mins)
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
