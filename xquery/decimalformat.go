package xquery

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// parseDecimalFormatDecl reads "declare [default] decimal-format [name]
// key=value ..." (§4.18).
//
// The declaration exists only to be read back by fn:format-number's third
// argument, and this package supplies that function itself: xpath's own
// fn:format-number always uses the default format, because a bare XPath
// expression has no prolog to declare another. The XSLT layer overrides it for
// the same reason and by the same mechanism.
func (p *parser) parseDecimalFormatDecl(isDefault bool) error {
	if !isDefault {
		p.pos += len("decimal-format")
		p.skipSpaceAndComments()
	}
	df := *xpath.DefaultDecimalFormat()
	var name xdm.QName
	if !isDefault {
		prefix, local, err := p.parseEQName()
		if err != nil {
			return err
		}
		if name, err = p.resolveDeclaredName(prefix, local, false); err != nil {
			return err
		}
		df.Name = name
	}
	// XQST0114: a property may not be given twice in one declaration, so the
	// keys seen are tracked rather than simply overwritten.
	seen := map[string]bool{}
	for {
		p.skipSpaceAndComments()
		key := p.peekKeyword()
		if key == "" {
			break
		}
		if !isDecimalFormatProperty(key) {
			break
		}
		p.pos += len(key)
		p.skipSpaceAndComments()
		if !p.consume("=") {
			return p.errorf("XPST0003: expected %q after %q", "=", key)
		}
		p.skipSpaceAndComments()
		val, err := p.parseURILiteral()
		if err != nil {
			return err
		}
		if seen[key] {
			return p.errorf("XQST0114: %q is given twice in one "+
				"decimal-format declaration", key)
		}
		seen[key] = true
		if err := applyDecimalFormatProperty(&df, key, val); err != nil {
			return p.errorf("%v", err)
		}
	}
	// XQST0098: the characters that mark the parts of a picture must be
	// distinct from one another, or a picture string is ambiguous. The zero
	// digit stands for the whole run of ten digits it starts, so every one of
	// them collides.
	if err := checkDecimalFormatDistinct(&df); err != nil {
		return p.errorf("%v", err)
	}
	key := name.Clark()
	if isDefault {
		key = ""
	}
	if p.formats == nil {
		p.formats = map[string]*xpath.DecimalFormat{}
	}
	// XQST0111: two declarations may not name the same format. Unlike XSLT
	// there is no import precedence to break the tie.
	if _, dup := p.formats[key]; dup {
		if isDefault {
			return p.errorf(
				"XQST0111: the default decimal format is declared twice")
		}
		return p.errorf("XQST0111: the decimal format %q is declared twice",
			name.Lexical())
	}
	p.formats[key] = &df
	return nil
}

func isDecimalFormatProperty(key string) bool {
	switch key {
	case "decimal-separator", "grouping-separator", "infinity", "minus-sign",
		"NaN", "percent", "per-mille", "zero-digit", "digit",
		"pattern-separator", "exponent-separator":
		return true
	}
	return false
}

// applyDecimalFormatProperty sets one property, checking that a property
// declared to be a single character is one.
//
// "decimal-separator = '..'" is XQST0097, not a two-character separator: a
// picture string is scanned character by character, and a two-character
// separator has no meaning in that scan.
func applyDecimalFormatProperty(df *xpath.DecimalFormat, key, val string) error {
	one := func() (rune, error) {
		r, size := utf8.DecodeRuneInString(val)
		if size == 0 || size != len(val) {
			return 0, fmt.Errorf(
				"XQST0097: %q must be a single character, not %q", key, val)
		}
		return r, nil
	}
	switch key {
	case "infinity":
		df.Infinity = val
		return nil
	case "NaN":
		df.NaN = val
		return nil
	}
	r, err := one()
	if err != nil {
		return err
	}
	switch key {
	case "decimal-separator":
		df.DecimalSeparator = r
	case "grouping-separator":
		df.GroupingSeparator = r
	case "minus-sign":
		df.MinusSign = r
	case "percent":
		df.Percent = r
	case "per-mille":
		df.PerMille = r
	case "digit":
		df.Digit = r
	case "pattern-separator":
		df.PatternSeparator = r
	case "exponent-separator":
		df.ExponentSeparator = r
	case "zero-digit":
		// The zero digit must begin a run of ten consecutive digits in
		// Unicode, because the format uses it to number every digit position:
		// declaring '5' would make "5678:" the digits, which is not a
		// numbering system. XQST0097 is the code.
		if !isDecimalDigitZero(r) {
			return fmt.Errorf(
				"XQST0097: %q must be the zero of a decimal digit family, not %q",
				key, val)
		}
		df.ZeroDigit = r
	}
	return nil
}

// isDecimalDigitZero reports whether r is the first of a run of ten digits, in
// the sense the Unicode general category Nd and the Numeric_Value property
// together give. ASCII '0' is the case that matters; the rest are the other
// decimal digit families Unicode defines.
func isDecimalDigitZero(r rune) bool {
	for _, zero := range []rune{
		0x0030, 0x0660, 0x06F0, 0x07C0, 0x0966, 0x09E6, 0x0A66, 0x0AE6,
		0x0B66, 0x0BE6, 0x0C66, 0x0CE6, 0x0D66, 0x0DE6, 0x0E50, 0x0ED0,
		0x0F20, 0x1040, 0x1090, 0x17E0, 0x1810, 0x1946, 0x19D0, 0x1A80,
		0x1A90, 0x1B50, 0x1BB0, 0x1C40, 0x1C50, 0xA620, 0xA8D0, 0xA900,
		0xA9D0, 0xA9F0, 0xAA50, 0xABF0, 0xFF10,
		0x104A0, 0x11066, 0x110F0, 0x11136, 0x111D0, 0x112F0, 0x114D0,
		0x11650, 0x116C0, 0x11730, 0x118E0, 0x11C50, 0x16A60, 0x16B50,
		0x1D7CE, 0x1D7D8, 0x1D7E2, 0x1D7EC, 0x1D7F6,
	} {
		if r == zero {
			return true
		}
	}
	return false
}

// checkDecimalFormatDistinct enforces XQST0098.
//
// The rule is that the decimal-separator, grouping-separator, percent,
// per-mille, digit, pattern-separator and exponent-separator characters must
// all differ from one another and from every digit of the zero-digit's family.
// The minus sign is deliberately absent from the list: it may coincide with
// another symbol, because it appears only in a position no other symbol can.
func checkDecimalFormatDistinct(df *xpath.DecimalFormat) error {
	named := []struct {
		what string
		r    rune
	}{
		{"decimal-separator", df.DecimalSeparator},
		{"grouping-separator", df.GroupingSeparator},
		{"percent", df.Percent},
		{"per-mille", df.PerMille},
		{"digit", df.Digit},
		{"pattern-separator", df.PatternSeparator},
		{"exponent-separator", df.ExponentSeparator},
	}
	for i, a := range named {
		// The exponent-separator is exempt from clashing with the digits: a
		// picture may legitimately use a letter that is not one.
		if a.r >= df.ZeroDigit && a.r < df.ZeroDigit+10 &&
			a.what != "exponent-separator" {
			return fmt.Errorf(
				"XQST0098: %s (%q) is one of the digits of the zero-digit family",
				a.what, string(a.r))
		}
		for _, b := range named[i+1:] {
			if a.r == b.r {
				return fmt.Errorf("XQST0098: %s and %s are both %q",
					a.what, b.what, string(a.r))
			}
		}
	}
	return nil
}

// registerFormatNumber installs an fn:format-number that can see the formats
// this query declared.
//
// xpath's own always uses the default format, because a bare expression has
// no prolog; the third argument names a format that only the query knows
// about, so the function has to be replaced rather than configured.
func (q *Query) registerFormatNumber(lib *xpath.Library) {
	if len(q.formats) == 0 {
		return
	}
	call := func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
		var num *xdm.Atomic
		if atoms := xdm.Atomize(args[0]); len(atoms) == 1 {
			if a, ok := atoms[0].(*xdm.Atomic); ok {
				num = a
			}
		} else if len(atoms) > 1 {
			return nil, fmt.Errorf(
				"XPTY0004: fn:format-number takes at most one number")
		}
		pic, err := joinAtomized(args[1])
		if err != nil {
			return nil, err
		}
		df := q.formats[""]
		if df == nil {
			df = xpath.DefaultDecimalFormat()
		}
		if len(args) > 2 {
			lex, err := joinAtomized(args[2])
			if err != nil {
				return nil, err
			}
			name, err := q.sc.resolveFormatName(lex)
			if err != nil {
				return nil, err
			}
			// FODF1280 is the error for naming a format that was never
			// declared, which is dynamic because the name is a value.
			f, ok := q.formats[name.Clark()]
			if !ok {
				return nil, fmt.Errorf(
					"FODF1280: no decimal format is named %q", lex)
			}
			df = f
		}
		// The picture-string grammar is version dependent -- 3.0 added the
		// exponent separator, 3.1 the sign in the exponent part -- and the
		// version to judge it by is the module's, not this call site's. It
		// was a bare xpath.XPath31 literal while nothing recorded the
		// module's version; now that something does, it follows the module.
		out, err := xpath.FormatNumberVersion(
			num, pic, df, q.sc.xqVersion.xpathVersion())
		if err != nil {
			return nil, err
		}
		return xdm.One(xdm.NewString(out)), nil
	}
	for _, arity := range []int{2, 3} {
		lib.Add(xpath.Function{
			Name:  xdm.QName{URI: xdm.NSFN, Local: "format-number"},
			Arity: arity,
			Call:  call,
		})
	}
}

// resolveFormatName resolves the lexical QName fn:format-number's third
// argument carries, against the module's own prefixes.
func (sc *staticContext) resolveFormatName(lex string) (xdm.QName, error) {
	prefix, local := "", lex
	if i := strings.IndexByte(lex, ':'); i >= 0 {
		prefix, local = lex[:i], lex[i+1:]
	}
	if !xdm.IsNCName(local) || (prefix != "" && !xdm.IsNCName(prefix)) {
		return xdm.QName{}, fmt.Errorf("FODF1280: %q is not a lexical QName", lex)
	}
	if prefix == "" {
		return xdm.QName{Local: local}, nil
	}
	uri, ok := sc.ns[prefix]
	if !ok {
		// A prefix the module level does not have may still have been bound
		// by an xmlns declaration attribute on a direct element constructor,
		// which §3.9.1.3 puts into the statically known namespaces of that
		// element's content -- and §4.7 resolves this name against those.
		// The call is a closure over the Query, so the constructor's own
		// context is long gone by the time it runs; ctorPrefixes is what the
		// module recorded of it. eqname-007 is the case:
		// "<a xmlns:ex='...'>{format-number(..., 'ex:format')}</a>", whose ex
		// we reported as unbound though the constructor enclosing the call
		// binds it.
		uri, ok = sc.ctorPrefixes[prefix]
	}
	if !ok {
		return xdm.QName{}, fmt.Errorf(
			"FODF1280: the prefix %q is not bound to a namespace", prefix)
	}
	return xdm.QName{URI: uri, Local: local}, nil
}
