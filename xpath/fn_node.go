package xpath

import (
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// registerNumericFuncs adds the numeric functions.
func registerNumericFuncs(l *Library) {
	l.registerFn("number", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		var a *xdm.Atomic
		if len(args) == 0 {
			s, err := contextString(ctx)
			if err != nil {
				return nil, err
			}
			a = xdm.NewUntypedAtomic(s)
		} else {
			atoms := xdm.Atomize(args[0])
			if len(atoms) == 0 {
				return numSeq(math.NaN()), nil
			}
			if len(atoms) > 1 {
				return nil, xdm.ErrType("number(): expected at most one item")
			}
			a = atoms[0].(*xdm.Atomic)
		}
		// fn:number is the one place a failed numeric conversion yields NaN
		// instead of an error: it exists precisely to be lenient, unlike
		// "cast as xs:double".
		conv, err := CastAtomic(a, xdm.TypeDouble)
		if err != nil {
			return numSeq(math.NaN()), nil
		}
		return xdm.One(conv), nil
	})

	l.registerFn("abs", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argNumber(args, 0)
		if err != nil || a == nil {
			return xdm.Empty, err
		}
		if a.Rat() != nil {
			r := new(big.Rat).Abs(a.Rat())
			if a.Type == xdm.TypeInteger {
				return xdm.One(xdm.NewIntegerFromRat(r)), nil
			}
			return xdm.One(xdm.NewDecimal(r)), nil
		}
		return xdm.One(makeFloat(math.Abs(a.Float64()), a.Type)), nil
	})

	l.registerFn("ceiling", []int{1}, numericRounder(math.Ceil, func(r *big.Rat) *big.Rat {
		q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
		if rem.Sign() > 0 {
			q.Add(q, big.NewInt(1))
		}
		return new(big.Rat).SetInt(q)
	}))

	l.registerFn("floor", []int{1}, numericRounder(math.Floor, func(r *big.Rat) *big.Rat {
		q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
		if rem.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		}
		return new(big.Rat).SetInt(q)
	}))

	// fn:round takes one argument in XPath 2.0. The precision argument is a 3.0
	// addition, and accepting it here made round(1, 2) return 1 rather than
	// reporting that no such function exists.
	l.registerFn("round", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		return roundWithPrecision(args, false)
	})
	l.registerFn("round-half-to-even", []int{1, 2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		return roundWithPrecision(args, true)
	})
}

// numericRounder builds ceiling/floor, which must stay exact for decimal and
// integer input rather than round-tripping through float64.
func numericRounder(ffn func(float64) float64, rfn func(*big.Rat) *big.Rat) func(*Context, []xdm.Sequence) (xdm.Sequence, error) {
	return func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		a, err := argNumber(args, 0)
		if err != nil || a == nil {
			return xdm.Empty, err
		}
		switch a.Type {
		case xdm.TypeInteger:
			return xdm.One(a), nil // already integral
		case xdm.TypeDecimal:
			return xdm.One(xdm.NewDecimal(rfn(a.Rat()))), nil
		default:
			return xdm.One(makeFloat(ffn(a.Float64()), a.Type)), nil
		}
	}
}

func roundWithPrecision(args []xdm.Sequence, halfToEven bool) (xdm.Sequence, error) {
	a, err := argNumber(args, 0)
	if err != nil || a == nil {
		return xdm.Empty, err
	}
	places := 0
	if len(args) > 1 {
		p, err := argNumber(args, 1)
		if err != nil {
			return nil, err
		}
		if p != nil {
			// The precision is clamped rather than used as given. It reaches
			// exactRound as an exponent of ten, and a value like 4294967296
			// would ask for a bignum with four billion digits — the process
			// stops responding rather than returning a wrong answer, which is
			// how this was found. Clamping is safe because no xs:decimal has
			// anywhere near this many significant digits: rounding to more
			// places than a value has is the identity, and rounding to fewer
			// than -maxRoundPlaces gives zero either way.
			places = clampPlaces(p.Int64())
		}
	}

	switch a.Type {
	case xdm.TypeInteger:
		if places >= 0 {
			return xdm.One(a), nil
		}
		return xdm.One(xdm.NewIntegerFromRat(exactRound(a.Rat(), places, halfToEven))), nil
	case xdm.TypeDecimal:
		return xdm.One(xdm.NewDecimal(exactRound(a.Rat(), places, halfToEven))), nil
	}

	f := a.Float64()
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return xdm.One(a), nil
	}
	// A double carries about 17 significant digits, so rounding to more places
	// than that is the identity — and asking for many more overflows the
	// shift: math.Pow(10, 300) is finite but f*shift is not, and Inf/Inf is
	// NaN. That turned round-half-to-even(3.567812E+3, 4294967296) into NaN
	// rather than leaving the value alone.
	const floatDigits = 20
	if places > floatDigits {
		return xdm.One(a), nil
	}
	if places < -floatDigits {
		return xdm.One(makeFloat(math.Copysign(0, f), a.Type)), nil
	}
	shift := math.Pow(10, float64(places))
	scaled := f * shift
	var rounded float64
	if halfToEven {
		rounded = math.RoundToEven(scaled)
	} else {
		rounded = math.Floor(scaled + 0.5)
	}
	result := rounded / shift
	// Rounding a negative value to zero yields *negative* zero, which the
	// arithmetic above loses: floor(-0.2 + 0.5) is floor(0.3), a positive
	// zero. IEEE 754 keeps the two apart and so does the spec, and the sign
	// survives serialisation — "-0" is a different string from "0".
	if result == 0 && math.Signbit(f) {
		result = math.Copysign(0, -1)
	}
	return xdm.One(makeFloat(result, a.Type)), nil
}

// registerNodeFuncs adds the node-property functions.
func registerNodeFuncs(l *Library) {
	l.registerFn("name", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		n, err := argNodeOrContext(ctx, args, 0)
		if err != nil || n == nil {
			return strSeq(""), err
		}
		// fn:name returns the lexical QName, prefix included; fn:local-name
		// does not. Document and text nodes have no name, giving "".
		switch n.Kind {
		case xdm.KindElement, xdm.KindAttribute, xdm.KindPI, xdm.KindNamespace:
			return strSeq(n.Name.Lexical()), nil
		}
		return strSeq(""), nil
	})

	l.registerFn("local-name", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		n, err := argNodeOrContext(ctx, args, 0)
		if err != nil || n == nil {
			return strSeq(""), err
		}
		return strSeq(n.Name.Local), nil
	})

	l.registerFn("namespace-uri", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		n, err := argNodeOrContext(ctx, args, 0)
		if err != nil || n == nil {
			return xdm.One(xdm.NewAnyURI("")), err
		}
		return xdm.One(xdm.NewAnyURI(n.Name.URI)), nil
	})

	l.registerFn("root", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		n, err := argNodeOrContext(ctx, args, 0)
		if err != nil || n == nil {
			return xdm.Empty, err
		}
		return xdm.One(n.Root()), nil
	})

	l.registerFn("node-name", []int{1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		n, err := argNodeOrContext(ctx, args, 0)
		if err != nil || n == nil {
			return xdm.Empty, err
		}
		switch n.Kind {
		case xdm.KindElement, xdm.KindAttribute, xdm.KindPI, xdm.KindNamespace:
			return xdm.One(xdm.NewQNameValue(n.Name)), nil
		}
		return xdm.Empty, nil
	})

	l.registerFn("document-uri", []int{1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		n, err := argNodeOrContext(ctx, args, 0)
		if err != nil || n == nil {
			return xdm.Empty, err
		}
		// Defined only for a document node, and empty for one that was not
		// retrieved by URI. Returning "" instead would make a stylesheet
		// believe it had a URI it can resolve against.
		//
		// The node itself has to be the document: walking to its root gave an
		// element the URI of the document containing it, which is fn:base-uri's
		// answer rather than this one.
		if n.Kind != xdm.KindDocument || n.BaseURI == "" {
			return xdm.Empty, nil
		}
		return xdm.One(xdm.NewAnyURI(n.BaseURI)), nil
	})

	l.registerFn("base-uri", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		n, err := argNodeOrContext(ctx, args, 0)
		if err != nil || n == nil {
			return xdm.Empty, err
		}
		return xdm.One(xdm.NewAnyURI(n.BaseURI)), nil
	})

	l.registerFn("lang", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		want, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		n, err := argNodeOrContext(ctx, args, 1)
		if err != nil || n == nil {
			return boolSeq(false), err
		}
		return boolSeq(langMatches(n, want)), nil
	})

	l.registerFn("nilled", []int{1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		n, err := argNodeOrContext(ctx, args, 0)
		if err != nil || n == nil {
			return xdm.Empty, err
		}
		if n.Kind != xdm.KindElement {
			return xdm.Empty, nil
		}
		a := n.Attr(xdm.NSXSI, "nil")
		return boolSeq(a != nil && (a.Value == "true" || a.Value == "1")), nil
	})
}

// langMatches implements fn:lang: the nearest xml:lang in scope must equal the
// requested tag or have it as a prefix followed by "-".
func langMatches(n *xdm.Node, want string) bool {
	for cur := n; cur != nil; cur = cur.Parent {
		a := cur.Attr(xdm.NSXML, "lang")
		if a == nil {
			continue
		}
		have := strings.ToLower(a.Value)
		w := strings.ToLower(want)
		return have == w || strings.HasPrefix(have, w+"-")
	}
	return false
}

// registerContextFuncs adds functions that read the dynamic context.
func registerContextFuncs(l *Library) {
	l.registerFn("implicit-timezone", []int{0}, func(ctx *Context, _ []xdm.Sequence) (xdm.Sequence, error) {
		d := &xdm.Duration{
			Negative: ctx.ImplicitTimezone < 0,
			Seconds:  new(big.Rat).SetInt64(int64(abs(ctx.ImplicitTimezone)) * 60),
		}
		return xdm.One(xdm.NewDuration(d, xdm.TypeDayTimeDuration)), nil
	})

	l.registerFn("static-base-uri", []int{0}, func(ctx *Context, _ []xdm.Sequence) (xdm.Sequence, error) {
		// The base URI of the *expression*, not of the context node. The two
		// differ whenever a stylesheet is applied to a document from
		// somewhere else, which is the ordinary case; falling back to the
		// node's is better than answering nothing when no static base URI has
		// been supplied.
		if ctx.StaticBaseURI != "" {
			return xdm.One(xdm.NewAnyURI(ctx.StaticBaseURI)), nil
		}
		if n, ok := ctx.Item.(*xdm.Node); ok && n.BaseURI != "" {
			return xdm.One(xdm.NewAnyURI(n.BaseURI)), nil
		}
		return xdm.Empty, nil
	})

	l.registerFn("doc", []int{1}, fnDoc)
	l.registerFn("doc-available", []int{1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// "Available" means a document came back. Deriving the answer from
		// whether fnDoc errored was not the same thing once fn:doc(()) became
		// a legal empty result rather than a type error.
		// A type error in the argument is still an error: fn:doc-available
		// answers whether the *document* is available, not whether the call
		// was well formed. Only a retrieval failure becomes false.
		if len(args) > 0 && len(args[0]) > 0 {
			if _, err := argStringRequired(args, 0); err != nil {
				return nil, err
			}
		}
		seq, err := fnDoc(ctx, args)
		return boolSeq(err == nil && len(seq) > 0), nil
	})

	// fn:error raises the error its QName argument names, so a stylesheet can
	// signal a specific spec condition rather than a generic failure. The
	// QName was previously ignored and every call reported FOER0000, which
	// made "fn:error(QName(..., 'err:FORG0009'))" indistinguishable from any
	// other error() call.
	l.registerFn("error", []int{0, 1, 2, 3}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		code := "FOER0000"
		// error(()) is XPTY0004. The parameter is xs:QName?, so the empty
		// sequence is in its type, but the one-argument signature requires
		// the argument to identify an error — passing nothing identifies
		// nothing, and the zero-argument form is what means "no code".
		if len(args) == 1 && len(args[0]) == 0 {
			return nil, xdm.ErrType(
				"fn:error: the single-argument form requires an error code")
		}
		if len(args) > 0 && len(args[0]) > 0 {
			// The first parameter is xs:QName?, so anything else is a type
			// error. Ignoring a non-QName silently turned
			// error('Wrong Argument Type') — which passes a string where a
			// code belongs — into the generic FOER0000 rather than XPTY0004.
			a, ok := args[0][0].(*xdm.Atomic)
			if !ok || a.Type != xdm.TypeQName {
				return nil, xdm.ErrType(
					"fn:error: the first argument is xs:QName?, got %s",
					args[0][0].TypeName())
			}
			if q := a.QName(); q != nil && q.Local != "" {
				code = q.Local
			}
		}
		msg := "error() called"
		if len(args) > 1 {
			if s, err := argString(args, 1); err == nil && s != "" {
				msg = s
			}
		}
		return nil, xdm.Errorf(code, "%s", msg)
	})

	l.registerFn("trace", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The value passes through unchanged; the label is ignored rather
		// than written to stderr, since a library should not print.
		return args[0], nil
	})
}

// fnDoc loads a document.
//
// It fails closed: with no resolver configured, every URI is refused rather
// than being fetched. A stylesheet that can open arbitrary URIs is a
// file-disclosure and SSRF vector, and validation stylesheets legitimately
// need only the code lists shipped beside them.
func fnDoc(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
	// The argument is declared xs:string?, so an empty sequence is not an
	// error: fn:doc(()) is the empty sequence, and fn:doc-available(()) is
	// false.
	if len(args) > 0 && len(args[0]) == 0 {
		return xdm.Empty, nil
	}
	// The parameter is xs:string?, so a non-string is a type error rather
	// than a URI to stringify: fn:doc-available(xs:integer(2)) is XPTY0004,
	// not false.
	uri, err := argStringRequired(args, 0)
	if err != nil {
		return nil, err
	}
	// An unusable URI is FODC0005 and is reported before access is attempted:
	// ":/" is not a URI at all, so whether a resolver is configured does not
	// come into it.
	if err := validAnyURI(uri); err != nil || strings.HasPrefix(strings.TrimSpace(uri), ":") {
		return nil, fmt.Errorf("FODC0005: %q is not a valid URI", uri)
	}
	if ctx.Docs == nil {
		return nil, fmt.Errorf("FODC0002: document access is disabled (no resolver configured): %q", uri)
	}
	base := ""
	if n, ok := ctx.Item.(*xdm.Node); ok {
		base = n.BaseURI
	}
	tree, err := ctx.Docs.ResolveDocument(uri, base)
	if err != nil {
		return nil, fmt.Errorf("FODC0002: cannot retrieve %q: %w", uri, err)
	}
	return xdm.One(tree.Root), nil
}

// maxRoundPlaces bounds the precision argument of fn:round and
// fn:round-half-to-even.
//
// The largest xs:double has about 309 digits before the point and 1074 after,
// so a thousand places either side is already far past the point where any
// finite value can be affected. Beyond it the operation is the identity in one
// direction and zero in the other, and both are reached without building the
// scale factor at all.
const maxRoundPlaces = 4096

func clampPlaces(n int64) int {
	if n > maxRoundPlaces {
		return maxRoundPlaces
	}
	if n < -maxRoundPlaces {
		return -maxRoundPlaces
	}
	return int(n)
}
