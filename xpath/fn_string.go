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
	l.registerFn("concat", concatArities, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		var sb strings.Builder
		for i := range args {
			// fn:concat takes xs:anyAtomicType, not xs:string.
			s, err := argAnyAtomicString(args, i)
			if err != nil {
				return nil, err
			}
			sb.WriteString(s)
		}
		return strSeq(sb.String()), nil
	})

	l.registerFn("string-join", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The separator is declared xs:string, not xs:string?, so an empty
		// sequence is a type error rather than an empty separator.
		sep, err := argStringRequired(args, 1)
		if err != nil {
			return nil, err
		}
		// The sequence is declared xs:string*, so calling String() on each
		// item accepted far too much: string-join(1 to 5, "") gave "12345"
		// where the spec wants XPTY0004. stringArgValue applies the same
		// conversion rule as every other xs:string parameter — the
		// string-like types and untypedAtomic, nothing else.
		atoms := xdm.Atomize(args[0])
		parts := make([]string, 0, len(atoms))
		for _, it := range atoms {
			v, err := stringArgValue(it.(*xdm.Atomic), 0)
			if err != nil {
				return nil, err
			}
			parts = append(parts, v)
		}
		return strSeq(strings.Join(parts, sep)), nil
	})

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
		return strSeq(strings.Join(strings.Fields(s), " ")), nil
	})

	// fn:upper-case and fn:lower-case are defined in terms of Unicode's *full*
	// case mapping, which can change a string's length: "ß" upper-cases to
	// "SS" and "ﬁ" to "FI". strings.ToUpper applies the simple mapping, which
	// leaves both unchanged, so cases.Upper is used instead.
	upper := cases.Upper(language.Und)
	lower := cases.Lower(language.Und)

	l.registerFn("upper-case", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		return strSeq(upper.String(s)), nil
	})

	l.registerFn("lower-case", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		return strSeq(lower.String(s)), nil
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
		i := coll.IndexOf(a, b)
		if i < 0 {
			return strSeq(""), nil
		}
		return strSeq(a[:i]), nil
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
		i := coll.IndexOf(a, b)
		if i < 0 {
			return strSeq(""), nil
		}
		return strSeq(a[i+len(b):]), nil
	})

	l.registerFn("substring", []int{2, 3}, fnSubstring)

	l.registerFn("translate", []int{3}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
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
		return strSeq(translate(src, from, to)), nil
	})

	l.registerFn("codepoints-to-string", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
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
			cp := a.Int64()
			if !isXMLChar(cp) {
				return nil, fmt.Errorf(
					"FOCH0001: %d is not a valid XML character", cp)
			}
			sb.WriteRune(rune(cp))
		}
		return strSeq(sb.String()), nil
	})

	l.registerFn("string-to-codepoints", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		var out xdm.Sequence
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

	l.registerFn("encode-for-uri", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		return strSeq(encodeForURI(s)), nil
	})
}

// fnSubstring implements fn:substring.
//
// The spec defines it in terms of rounding and a half-open interval that
// tolerates out-of-range and fractional positions rather than erroring:
// substring("hello", 0) is "hello", substring("hello", -5, 3) is "", and NaN
// positions yield "". Implementing it as a naive slice with bounds checks gets
// the edge cases wrong, so the arithmetic follows the spec's formula directly.
func fnSubstring(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
	src, err := argString(args, 0)
	if err != nil {
		return nil, err
	}
	startA, err := argNumber(args, 1)
	if err != nil {
		return nil, err
	}
	if startA == nil {
		return strSeq(""), nil
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
			return strSeq(""), nil
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
	return strSeq(string(runes[int(lo)-1 : int(hi)-1])), nil
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

// isXMLChar reports whether a codepoint may appear in an XML 1.0 document.
//
// The excluded ranges are not arbitrary: most C0 controls, the surrogate block
// (which has no meaning outside UTF-16 encoding), and the two permanently
// unassigned characters at the end of the BMP. Writing an excluded codepoint
// with WriteRune silently produced U+FFFD instead of failing, so a stylesheet
// building a string from computed codepoints got a replacement character where
// it expected an error.
func isXMLChar(c int64) bool {
	switch {
	case c == 0x9, c == 0xA, c == 0xD:
		return true
	case c >= 0x20 && c <= 0xD7FF:
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
const concatMaxArity = 64
