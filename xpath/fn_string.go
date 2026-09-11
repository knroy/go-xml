package xpath

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/knroy/go-xml/xdm"
)

// registerStringFuncs adds the fn: string functions.
//
// A recurring hazard here is that XPath indexes strings in *characters*, not
// bytes, and positions are 1-based. Go slices bytes, so every function that
// takes a position converts to a rune slice first. Using byte offsets would
// work for ASCII and silently corrupt any document with accented characters,
// which for an invoice validator is most of them.
func registerStringFuncs(l *Library) {
	l.registerFn("string", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		if len(args) == 0 {
			s, err := contextString(ctx)
			return strSeq(s), err
		}
		// fn:string of the empty sequence is "", not the empty sequence.
		if len(args[0]) == 0 {
			return strSeq(""), nil
		}
		it, err := args[0].Single()
		if err != nil {
			return nil, err
		}
		switch v := it.(type) {
		case *xdm.Node:
			return strSeq(v.StringValue()), nil
		case *xdm.Atomic:
			return strSeq(v.String()), nil
		case *xdm.FunctionItem, *xdm.MapItem, *xdm.ArrayItem:
			// Maps and arrays are function items in the 3.1 data model, so
			// they inherit "has no string value" and with it FOTY0014. They
			// used to fall through to the default branch and report the
			// generic XPTY0004, which is the code for a *wrong* type rather
			// than for a type that simply has no string value.
			return nil, xdm.Errorf("FOTY0014",
				"fn:string is not defined on %s", it.TypeName())
		default:
			// An Opaque carries engine-internal state and has no string
			// value. A stylesheet that names the internal namespace can
			// reach one, so this must be an error rather than a panic.
			return nil, fmt.Errorf(
				"XPTY0004: fn:string is not defined on %s", it.TypeName())
		}
	})

	// fn:concat is the one variadic function in the library: the spec gives it
	// a signature of two-or-more arguments rather than a fixed set. Lookup is
	// keyed by (name, arity), so "variadic" here means registering each arity,
	// and the range has to be wide enough that no reasonable expression falls
	// off the end — it was capped at 10, so a thirteen-argument concat was
	// reported as an unknown function.
	concatArities := make([]int, 0, concatMaxArity-1)
	for n := 2; n <= concatMaxArity; n++ {
		concatArities = append(concatArities, n)
	}
	l.registerFn("concat", concatArities, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		var sb strings.Builder
		for i := range args {
			// fn:concat takes xs:anyAtomicType, not xs:string.
			s, err := argAnyAtomicString(args, i)
			if err != nil {
				return nil, err
			}
			// Charged as it builds, not after: this is the doubling
			// chain's own step, and charging the finished string would
			// mean the allocation had already happened.
			if err := ctx.countBytes(len(s)); err != nil {
				return nil, err
			}
			sb.WriteString(s)
		}
		return strSeq(sb.String()), nil
	})

	stringJoin := func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The separator is declared xs:string, not xs:string?, so an empty
		// sequence is a type error rather than an empty separator. The
		// one-argument form of 3.0 has no separator at all, which is the
		// empty string rather than a missing argument.
		sep := ""
		if len(args) > 1 {
			var err error
			if sep, err = argStringRequired(args, 1); err != nil {
				return nil, err
			}
		}
		// The sequence is declared xs:string*, so calling String() on each
		// item accepted far too much: string-join(1 to 5, "") gave "12345"
		// where the spec wants XPTY0004. stringArgValue applies the same
		// conversion rule as every other xs:string parameter — the
		// string-like types and untypedAtomic, nothing else.
		atoms := xdm.Atomize(args[0])
		parts := make([]string, 0, len(atoms))
		for _, it := range atoms {
			a := it.(*xdm.Atomic)
			// 3.1 widened the first parameter to xs:anyAtomicType* (bug
			// 29184), so an integer or a date is joined via its string
			// value rather than rejected. Earlier versions keep the strict
			// xs:string* signature, which their own cases pin: under 2.0
			// and 3.0, string-join(1 to 5, "") must still be XPTY0004.
			if ctx != nil && ctx.Version.atLeast31() {
				parts = append(parts, a.String())
				continue
			}
			v, err := stringArgValue(a, 0)
			if err != nil {
				return nil, err
			}
			parts = append(parts, v)
		}
		for _, p := range parts {
			if err := ctx.countBytes(len(p) + len(sep)); err != nil {
				return nil, err
			}
		}
		return strSeq(strings.Join(parts, sep)), nil
	}
	l.registerFn("string-join", []int{2}, stringJoin)
	// The one-argument form joins with no separator, and is 3.0 only.
	l.registerFnSince(XPath30, "string-join", []int{1}, stringJoin)

	l.registerFn("string-length", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argOrContextString(ctx, args, 0)
		if err != nil {
			return nil, err
		}
		return intSeq(int64(utf8.RuneCountInString(s))), nil
	})

	l.registerFn("normalize-space", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argOrContextString(ctx, args, 0)
		if err != nil {
			return nil, err
		}
		return stringResult(ctx, strings.Join(strings.Fields(s), " "))
	})

	// fn:upper-case and fn:lower-case are defined in terms of Unicode's *full*
	// case mapping, which can change a string's length: "ß" upper-cases to
	// "SS" and "ﬁ" to "FI". strings.ToUpper applies the simple mapping, which
	// leaves both unchanged, so cases.Upper is used instead.
	upper := cases.Upper(language.Und)
	lower := cases.Lower(language.Und)

	l.registerFn("upper-case", []int{1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		return stringResult(ctx, upper.String(s))
	})

	l.registerFn("lower-case", []int{1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		return stringResult(ctx, lower.String(s))
	})

	l.registerFn("contains", []int{2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		coll, err := collationArgCtx(ctx, "contains", args, 2)
		if err != nil {
			return nil, err
		}
		a, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		b, err := argString(args, 1)
		if err != nil {
			return nil, err
		}
		// Every string contains the empty string, including the empty string.
		return boolSeq(coll.Contains(a, b)), nil
	})

	l.registerFn("starts-with", []int{2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		coll, err := collationArgCtx(ctx, "starts-with", args, 2)
		if err != nil {
			return nil, err
		}
		a, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		b, err := argString(args, 1)
		if err != nil {
			return nil, err
		}
		return boolSeq(coll.StartsWith(a, b)), nil
	})

	l.registerFn("ends-with", []int{2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		coll, err := collationArgCtx(ctx, "ends-with", args, 2)
		if err != nil {
			return nil, err
		}
		a, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		b, err := argString(args, 1)
		if err != nil {
			return nil, err
		}
		return boolSeq(coll.EndsWith(a, b)), nil
	})

	l.registerFn("substring-before", []int{2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		coll, err := collationArgCtx(ctx, "substring-before", args, 2)
		if err != nil {
			return nil, err
		}
		a, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		b, err := argString(args, 1)
		if err != nil {
			return nil, err
		}
		start, _, ok := collationMatchRange(coll, a, b)
		if !ok {
			return strSeq(""), nil
		}
		return strSeq(a[:start]), nil
	})

	l.registerFn("substring-after", []int{2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		coll, err := collationArgCtx(ctx, "substring-after", args, 2)
		if err != nil {
			return nil, err
		}
		a, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		b, err := argString(args, 1)
		if err != nil {
			return nil, err
		}
		// The end of the match, not start+len(b): under a collation the
		// matched span can be longer or shorter than the needle.
		_, end, ok := collationMatchRange(coll, a, b)
		if !ok {
			return strSeq(""), nil
		}
		return strSeq(a[end:]), nil
	})

	l.registerFn("substring", []int{2, 3}, fnSubstring)

	l.registerFn("translate", []int{3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// Only the first argument is xs:string?. The two mapping arguments are
		// xs:string, so an empty sequence there is a type error rather than an
		// empty map.
		src, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		from, err := argStringRequired(args, 1)
		if err != nil {
			return nil, err
		}
		to, err := argStringRequired(args, 2)
		if err != nil {
			return nil, err
		}
		out := translate(src, from, to)
		if err := ctx.countBytes(len(out)); err != nil {
			return nil, err
		}
		return strSeq(out), nil
	})

	l.registerFn("codepoints-to-string", []int{1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		var sb strings.Builder
		for _, it := range xdm.Atomize(args[0]) {
			// The parameter is xs:integer*, so a string is a type error
			// rather than a lexical form to parse: casting first reported
			// FORG0001 for codepoints-to-string('hello'), which says the
			// value was wrong for a conversion that should never have been
			// attempted. Only untypedAtomic converts, as it does everywhere.
			src := it.(*xdm.Atomic)
			if src.Type != xdm.TypeInteger && src.Type != xdm.TypeUntypedAtomic {
				return nil, xdm.ErrType(
					"fn:codepoints-to-string: expected xs:integer, got %s",
					src.TypeName())
			}
			a, err := CastAtomic(src, xdm.TypeInteger)
			if err != nil {
				return nil, err
			}
			// $arg is xs:integer*, which is unbounded, so a codepoint can
			// exceed int64 -- and big.Int.Int64 is undefined out of range,
			// not saturating. The wrap is silent and can land on a *valid*
			// codepoint: codepoints-to-string(2^64+65) came back as "A".
			// FitsInt64 first, so an out-of-range value is refused by its own
			// digits rather than by whatever its low 64 bits happen to spell.
			if !a.FitsInt64() {
				return nil, fmt.Errorf(
					"FOCH0001: %s is not a valid XML character", a.String())
			}
			cp := a.Int64()
			if !isXMLChar(cp) {
				return nil, fmt.Errorf(
					"FOCH0001: %d is not a valid XML character", cp)
			}
			if err := ctx.countBytes(utf8.RuneLen(rune(cp))); err != nil {
				return nil, err
			}
			sb.WriteRune(rune(cp))
		}
		return strSeq(sb.String()), nil
	})

	l.registerFn("string-to-codepoints", []int{1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		// The item count is known before the loop -- one per rune -- so the
		// whole result is reserved once rather than charged per append. A
		// string near MaxBytes yields a sequence of the same order of
		// magnitude in items, and reserving first is what refuses it without
		// building the slice.
		out, err := makeSequence(ctx, utf8.RuneCountInString(s))
		if err != nil {
			return nil, err
		}
		for _, r := range s {
			out = append(out, xdm.NewInteger(int64(r)))
		}
		return out, nil
	})

	l.registerFn("compare", []int{2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		coll, err := collationArgCtx(ctx, "compare", args, 2)
		if err != nil {
			return nil, err
		}
		// An empty argument yields the empty sequence, not 0.
		if len(args[0]) == 0 || len(args[1]) == 0 {
			return xdm.Empty(), nil
		}
		a, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		b, err := argString(args, 1)
		if err != nil {
			return nil, err
		}
		return intSeq(int64(coll.Compare(a, b))), nil
	})

	l.registerFn("encode-for-uri", []int{1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		return stringResult(ctx, encodeForURI(s))
	})
}

// fnSubstring implements fn:substring.
//
// The spec defines it in terms of rounding and a half-open interval that
// tolerates out-of-range and fractional positions rather than erroring:
// substring("hello", 0) is "hello", substring("hello", -5, 3) is "", and NaN
// positions yield "". Implementing it as a naive slice with bounds checks gets
// the edge cases wrong, so the arithmetic follows the spec's formula directly.
func fnSubstring(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
	src, err := argString(args, 0)
	if err != nil {
		return nil, err
	}
	startA, err := argNumber(args, 1)
	if err != nil {
		return nil, err
	}
	if startA == nil {
		// $start is declared xs:double, not xs:double?, so an empty
		// sequence is a type error rather than an empty result.
		return nil, fmt.Errorf(
			"XPTY0004: an empty sequence is not allowed as the second " +
				"argument of fn:substring()")
	}
	start := roundHalfEven(startA.Float64())

	runes := []rune(src)
	n := float64(len(runes))

	// The selected range is [start, end) in 1-based character positions.
	end := n + 1
	if len(args) > 2 {
		lenA, err := argNumber(args, 2)
		if err != nil {
			return nil, err
		}
		if lenA == nil {
			// $length is xs:double too, and likewise not nullable.
			return nil, fmt.Errorf(
				"XPTY0004: an empty sequence is not allowed as the third " +
					"argument of fn:substring()")
		}
		l := roundHalfEven(lenA.Float64())
		if isNaNf(l) || isNaNf(start) {
			return strSeq(""), nil
		}
		end = start + l
	}
	if isNaNf(start) {
		return strSeq(""), nil
	}
	// start and length are doubles, so both can be infinite. Each is checked
	// for NaN above, but their *sum* is NaN when the infinities have opposite
	// signs — substring("12345", -1 div 0E0, 1 div 0E0) is the suite's case —
	// and NaN survives the clamps below because every comparison against it is
	// false. That reached the slice as a negative bound and panicked.
	if isNaNf(end) {
		return strSeq(""), nil
	}

	lo, hi := start, end
	if lo < 1 {
		lo = 1
	}
	if hi > n+1 {
		hi = n + 1
	}
	if hi <= lo {
		return strSeq(""), nil
	}
	// string(runes[...]) allocates -- unlike substring-before and
	// substring-after, which slice the argument and share its backing array --
	// so the result is charged. It cannot exceed its input, which makes it the
	// bounded-output case stringResult is written for.
	return stringResult(ctx, string(runes[int(lo)-1:int(hi)-1]))
}

// translate maps characters of src through the from/to correspondence.
// Characters in from with no counterpart in to are deleted, and only the first
// occurrence of a character in from counts.
func translate(src, from, to string) string {
	fromR, toR := []rune(from), []rune(to)
	idx := make(map[rune]int, len(fromR))
	for i, r := range fromR {
		if _, seen := idx[r]; !seen {
			idx[r] = i
		}
	}
	var sb strings.Builder
	for _, r := range src {
		i, ok := idx[r]
		if !ok {
			sb.WriteRune(r)
			continue
		}
		if i < len(toR) {
			sb.WriteRune(toR[i])
		}
		// else: deleted
	}
	return sb.String()
}

// encodeForURI percent-encodes everything outside the unreserved set of
// RFC 3986. net/url's escapers do not match this set exactly, so the rule is
// spelled out here.
func encodeForURI(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var sb strings.Builder
	for _, b := range []byte(s) {
		switch {
		case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9',
			b == '-', b == '_', b == '.', b == '~':
			sb.WriteByte(b)
		default:
			sb.WriteByte('%')
			sb.WriteByte(hexDigits[b>>4])
			sb.WriteByte(hexDigits[b&0x0f])
		}
	}
	return sb.String()
}

// isXMLChar reports whether a codepoint may appear in an XML document, at the
// version of XML this engine implements.
//
// The excluded ranges are not arbitrary: U+0000, the surrogate block (which has
// no meaning outside UTF-16 encoding), and the two permanently unassigned
// characters at the end of the BMP. Writing an excluded codepoint with
// WriteRune silently produced U+FFFD instead of failing, so a stylesheet
// building a string from computed codepoints got a replacement character where
// it expected an error.
//
// The C0 controls other than TAB, LF and CR are admitted, because this engine
// implements XML 1.1: [2] Char there is [#x1-#xD7FF] | [#xE000-#xFFFD] |
// [#x10000-#x10FFFF], where XML 1.0 starts the first range at #x20. XSLT 3.0
// 4.1 makes the choice ours -- "Implementations may support any version ... it
// is thus implementation-defined which versions and editions of XML and XML
// Namespaces are supported" -- and tests/xslts/deps.go already claims XML_1.1,
// so refusing #x8 here contradicted a promise the harness makes on our behalf.
// Saxon 9.8 passes xml-to-json-D015, -D017 and -D018 on the same reading; the
// three cases construct a backspace, a bell and a form feed by codepoint.
//
// The version does still decide whether such a character can be WRITTEN DOWN,
// and that decision lives in the serializer, not here: XDM makes no distinction
// between an XML 1.0 and an XML 1.1 tree (XSLT 3.0 4.1), so a C0 control is a
// string value at either version and only becomes SERE0006 when an XML 1.0
// serialization is asked to spell it. See xslt/serialize.go.
func isXMLChar(c int64) bool {
	switch {
	case c >= 0x1 && c <= 0xD7FF:
		return true
	case c >= 0xE000 && c <= 0xFFFD:
		return true
	case c >= 0x10000 && c <= 0x10FFFF:
		return true
	}
	return false
}

// concatMaxArity bounds the arities fn:concat is registered for.
//
// The function is variadic in the spec, so any bound is arbitrary; this one is
// far past what a stylesheet writes by hand and keeps the library a fixed
// size. An expression needing more can nest calls.
//
// 64 was too low: the suite takes fn:concat#99 to check that a reference to a
// variadic function resolves at an arity nobody would write by hand.
const concatMaxArity = 100
