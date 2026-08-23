package xslt

import (
	"fmt"
	"math"
	"math/big"
	"strings"
	"unicode"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// DecimalFormat holds an xsl:decimal-format declaration.
//
// Every symbol is configurable because the instruction exists to serve
// locales: a German invoice writes 1.234,56 where an English one writes
// 1,234.56, and the picture string is written once against whatever symbols
// the format declares.
type DecimalFormat struct {
	Name              xdm.QName
	DecimalSeparator  rune
	GroupingSeparator rune
	Percent           rune
	PerMille          rune
	ZeroDigit         rune
	Digit             rune
	PatternSeparator  rune
	MinusSign         rune
	Infinity          string
	NaN               string
}

// defaultDecimalFormat returns the format used when none is declared.
func defaultDecimalFormat() *DecimalFormat {
	return &DecimalFormat{
		DecimalSeparator:  '.',
		GroupingSeparator: ',',
		Percent:           '%',
		PerMille:          '‰',
		ZeroDigit:         '0',
		Digit:             '#',
		PatternSeparator:  ';',
		MinusSign:         '-',
		Infinity:          "Infinity",
		NaN:               "NaN",
	}
}

// compileDecimalFormat compiles an xsl:decimal-format declaration.
func (c *compiler) compileDecimalFormat(el *xdm.Node) error {
	df := defaultDecimalFormat()

	if n := el.AttrValue("name"); n != "" {
		qn, err := resolveQNameAttr(el, n)
		if err != nil {
			return err
		}
		df.Name = qn
	}

	// Each symbol attribute is a single character; anything else is a
	// declaration error rather than something to truncate silently.
	runeAttr := func(attr string, dst *rune) error {
		v := el.AttrValue(attr)
		if v == "" {
			return nil
		}
		r := []rune(v)
		if len(r) != 1 {
			return fmt.Errorf(
				"XTSE0020: xsl:decimal-format/@%s must be a single character, got %q",
				attr, v)
		}
		*dst = r[0]
		return nil
	}
	for _, spec := range []struct {
		attr string
		dst  *rune
	}{
		{"decimal-separator", &df.DecimalSeparator},
		{"grouping-separator", &df.GroupingSeparator},
		{"percent", &df.Percent},
		{"per-mille", &df.PerMille},
		{"zero-digit", &df.ZeroDigit},
		{"digit", &df.Digit},
		{"pattern-separator", &df.PatternSeparator},
		{"minus-sign", &df.MinusSign},
	} {
		if err := runeAttr(spec.attr, spec.dst); err != nil {
			return err
		}
	}
	if v := el.AttrValue("infinity"); v != "" {
		df.Infinity = v
	}
	if v := el.AttrValue("NaN"); v != "" {
		df.NaN = v
	}

	// XTSE1295: the zero-digit must be a digit whose numeric value is zero,
	// since every other digit of the family is derived from it by offset.
	if !unicode.IsDigit(df.ZeroDigit) || digitValue(df.ZeroDigit) != 0 {
		return fmt.Errorf(
			"XTSE1295: xsl:decimal-format/@zero-digit=%q is not a digit with "+
				"the value zero", string(df.ZeroDigit))
	}

	// XTSE1290: two xsl:decimal-format declarations for the same format at
	// the same import precedence may not disagree about an attribute.
	if prev, dup := c.sheet.decimalFormats[df.Name.Clark()]; dup {
		// Only *conflicting* values are an error. A module that declares the
		// same format as one it imports is repeating itself, not disagreeing,
		// and comparing whole structs made the default format conflict with
		// itself the moment any module declared it explicitly.
		if !sameDecimalFormat(prev, df) {
			return fmt.Errorf(
				"XTSE1290: conflicting xsl:decimal-format declarations for %q",
				df.Name.Lexical())
		}
	}

	// XTSE1300: the characters used in a picture string must be distinct.
	// Two symbols with the same character make a picture ambiguous, so the
	// specification requires them to differ rather than picking a winner.
	seen := map[rune]string{}
	for _, sym := range []struct {
		name string
		r    rune
	}{
		{"decimal-separator", df.DecimalSeparator},
		{"grouping-separator", df.GroupingSeparator},
		{"percent", df.Percent},
		{"per-mille", df.PerMille},
		{"zero-digit", df.ZeroDigit},
		{"digit", df.Digit},
		{"pattern-separator", df.PatternSeparator},
	} {
		if prev, dup := seen[sym.r]; dup {
			return fmt.Errorf(
				"XTSE1300: xsl:decimal-format/@%s and @%s are both %q",
				prev, sym.name, string(sym.r))
		}
		seen[sym.r] = sym.name
	}

	c.sheet.decimalFormats[df.Name.Clark()] = df
	return nil
}

// registerFormatNumber adds fn:format-number, which needs the stylesheet's
// decimal formats and so cannot live in the shared xpath builtin library.
func registerFormatNumber(l *xpath.Library, s *Stylesheet) {
	call := func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
		num, err := formatNumberArg(args, 0)
		if err != nil {
			return nil, err
		}
		picture, err := formatNumberString(args, 1)
		if err != nil {
			return nil, err
		}

		dfName := ""
		if len(args) > 2 {
			lex, err := formatNumberString(args, 2)
			if err != nil {
				return nil, err
			}
			// Only unprefixed format names are resolvable here, since the
			// stylesheet's namespace context is not available at call time.
			_, local := xdm.SplitQName(lex)
			dfName = xdm.QName{Local: local}.Clark()
		}
		df, ok := s.decimalFormats[dfName]
		if !ok {
			if dfName != "" {
				return nil, fmt.Errorf("XTDE1280: no xsl:decimal-format named %q", dfName)
			}
			df = defaultDecimalFormat()
		}

		out, err := formatNumber2(num, picture, df)
		if err != nil {
			return nil, err
		}
		return xdm.One(xdm.NewString(out)), nil
	}

	for _, arity := range []int{2, 3} {
		l.Add(xpath.Function{
			Name:  xdm.QName{URI: xdm.NSFN, Local: "format-number"},
			Arity: arity,
			Call:  call,
		})
	}
}

func formatNumberArg(args []xdm.Sequence, i int) (*xdm.Atomic, error) {
	atoms := xdm.Atomize(args[i])
	if len(atoms) == 0 {
		// An empty first argument formats as zero, per the spec's rule that
		// it is treated as NaN and NaN renders as the NaN symbol; returning
		// an error here would break every stylesheet that formats an optional
		// element.
		return xdm.NewDouble(math.NaN()), nil
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok {
		return nil, xdm.ErrType("format-number: expected a number")
	}
	if a.Type.IsNumeric() {
		return a, nil
	}
	conv, err := xpath.CastAtomic(a, xdm.TypeDouble)
	if err != nil {
		return xdm.NewDouble(math.NaN()), nil
	}
	return conv, nil
}

func formatNumberString(args []xdm.Sequence, i int) (string, error) {
	atoms := xdm.Atomize(args[i])
	if len(atoms) == 0 {
		return "", nil
	}
	return atoms[0].(*xdm.Atomic).String(), nil
}

// picture is a parsed format-number picture string.
type picture struct {
	prefix, suffix    string
	minInt, minFrac   int
	maxFrac           int
	grouping          int // digits per group, 0 for none
	percent, perMille bool
}

// formatNumber2 implements fn:format-number.
//
// The picture language is small but has three rules that a naive
// implementation misses: the '#' and '0' digit characters mean *minimum*
// versus *optional* places rather than literal digits, grouping size comes
// from the position of the last separator rather than being fixed at three,
// and a picture may carry two sub-pictures separated by ';' where the second
// is used for negative numbers instead of prefixing a minus sign.
func formatNumber2(num *xdm.Atomic, pic string, df *DecimalFormat) (string, error) {
	if num.IsNaN() {
		return df.NaN, nil
	}

	// Split positive and negative sub-pictures.
	subs := splitPicture(pic, df.PatternSeparator)
	negative := isNegative(num)
	chosen := subs[0]
	explicitNegative := len(subs) > 1
	if negative && explicitNegative {
		chosen = subs[1]
	}

	p, err := parsePicture(chosen, df)
	if err != nil {
		return "", err
	}

	f := num.Float64()
	if math.IsInf(f, 0) {
		sign := ""
		if f < 0 && !explicitNegative {
			sign = string(df.MinusSign)
		}
		return p.prefix + sign + df.Infinity + p.suffix, nil
	}

	// Scaling by percent or per-mille happens before rounding, so
	// format-number(0.5, '#0%') is "50%" rather than "0%".
	value := ratOf(num)
	switch {
	case p.percent:
		value = new(big.Rat).Mul(value, big.NewRat(100, 1))
	case p.perMille:
		value = new(big.Rat).Mul(value, big.NewRat(1000, 1))
	}
	if negative {
		value = new(big.Rat).Abs(value)
	}

	rounded := roundToPlaces(value, p.maxFrac)
	intPart, fracPart := splitRat(rounded, p.maxFrac)

	intStr := padInt(intPart, p.minInt, df)
	if p.grouping > 0 {
		intStr = applyGrouping(intStr, p.grouping, df.GroupingSeparator)
	}
	fracStr := trimFraction(fracPart, p.minFrac, df)

	var sb strings.Builder
	sb.WriteString(p.prefix)
	if negative && !explicitNegative {
		sb.WriteRune(df.MinusSign)
	}
	sb.WriteString(intStr)
	if fracStr != "" {
		sb.WriteRune(df.DecimalSeparator)
		sb.WriteString(fracStr)
	}
	sb.WriteString(p.suffix)
	return sb.String(), nil
}

// ratOf converts a numeric atomic to an exact rational.
func ratOf(a *xdm.Atomic) *big.Rat {
	if r := a.Rat(); r != nil {
		return new(big.Rat).Set(r)
	}
	r := new(big.Rat)
	r.SetFloat64(a.Float64())
	return r
}

func isNegative(a *xdm.Atomic) bool {
	if r := a.Rat(); r != nil {
		return r.Sign() < 0
	}
	return math.Signbit(a.Float64())
}

// splitPicture splits on the pattern separator, ignoring the separator when it
// is not present.
func splitPicture(pic string, sep rune) []string {
	parts := strings.Split(pic, string(sep))
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return parts
}

// parsePicture reads one sub-picture.
func parsePicture(pic string, df *DecimalFormat) (picture, error) {
	var p picture
	p.maxFrac = -1

	runes := []rune(pic)
	// Locate the digit region: the span containing digit, zero, grouping and
	// decimal characters. Everything before it is the prefix and everything
	// after is the suffix.
	start, end := -1, -1
	for i, r := range runes {
		if r == df.Digit || r == df.ZeroDigit || r == df.GroupingSeparator ||
			r == df.DecimalSeparator {
			if start < 0 {
				start = i
			}
			end = i
		}
	}
	if start < 0 {
		return p, fmt.Errorf("XTDE1310: picture %q contains no digit characters", pic)
	}

	p.prefix = string(runes[:start])
	p.suffix = string(runes[end+1:])

	// Percent and per-mille anywhere in the picture scale the value.
	if strings.ContainsRune(p.prefix+p.suffix, df.Percent) {
		p.percent = true
	}
	if strings.ContainsRune(p.prefix+p.suffix, df.PerMille) {
		p.perMille = true
	}

	digits := runes[start : end+1]
	intPart, fracPart := digits, []rune(nil)
	for i, r := range digits {
		if r == df.DecimalSeparator {
			intPart, fracPart = digits[:i], digits[i+1:]
			break
		}
	}

	// Grouping size is the distance from the last grouping separator to the
	// end of the integer part, which is what makes "#,##,##0" (the Indian
	// lakh grouping) express a different size from "#,##0".
	lastSep := -1
	for i, r := range intPart {
		if r == df.GroupingSeparator {
			lastSep = i
		}
	}
	if lastSep >= 0 {
		p.grouping = len(intPart) - lastSep - 1
	}

	for _, r := range intPart {
		if r == df.ZeroDigit {
			p.minInt++
		}
	}
	if len(fracPart) > 0 {
		p.maxFrac = 0
		for _, r := range fracPart {
			switch r {
			case df.ZeroDigit:
				p.minFrac++
				p.maxFrac++
			case df.Digit:
				p.maxFrac++
			}
		}
	} else {
		p.maxFrac = 0
	}
	return p, nil
}

// roundToPlaces rounds an exact rational to n decimal places, half away from
// zero — the rounding fn:format-number specifies.
func roundToPlaces(r *big.Rat, n int) *big.Rat {
	if n < 0 {
		n = 0
	}
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil))
	scaled := new(big.Rat).Mul(r, scale)

	num, den := scaled.Num(), scaled.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	twice := new(big.Int).Abs(new(big.Int).Mul(rem, big.NewInt(2)))
	if twice.Cmp(den) >= 0 {
		if scaled.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return new(big.Rat).Quo(new(big.Rat).SetInt(q), scale)
}

// splitRat separates a non-negative rational into integer and fractional
// digit strings, the fraction padded to n places.
func splitRat(r *big.Rat, n int) (string, string) {
	if n < 0 {
		n = 0
	}
	s := r.FloatString(n)
	s = strings.TrimPrefix(s, "-")
	intPart, fracPart, _ := strings.Cut(s, ".")
	return intPart, fracPart
}

// padInt left-pads the integer digits to the minimum width, translating into
// the format's digit family.
func padInt(s string, minInt int, df *DecimalFormat) string {
	if s == "0" && minInt == 0 {
		// "#" with no "0" means the integer part is omitted when it is zero,
		// so ".5" formats as ".5" rather than "0.5".
		s = ""
	}
	for len([]rune(s)) < minInt {
		s = "0" + s
	}
	return translateDigits(s, df.ZeroDigit)
}

// trimFraction removes optional trailing zeros beyond the minimum.
func trimFraction(s string, minFrac int, df *DecimalFormat) string {
	for len(s) > minFrac && strings.HasSuffix(s, "0") {
		s = s[:len(s)-1]
	}
	if s == "" {
		return ""
	}
	return translateDigits(s, df.ZeroDigit)
}

// translateDigits maps ASCII digits into the format's digit family, which for
// a zero-digit other than '0' is a different Unicode block entirely.
func translateDigits(s string, zero rune) string {
	if zero == '0' {
		return s
	}
	var sb strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			sb.WriteRune(zero + (r - '0'))
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// applyGrouping inserts the grouping separator every n digits from the right.
func applyGrouping(s string, n int, sep rune) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	runes := []rune(s)
	var out []rune
	for i, r := range runes {
		if i > 0 && (len(runes)-i)%n == 0 {
			out = append(out, sep)
		}
		out = append(out, r)
	}
	return string(out)
}

// sameDecimalFormat reports whether two declarations agree on every symbol.
//
// Name is excluded: two declarations of the same format necessarily share it,
// and comparing it adds nothing. Everything else is compared by value, which
// is what "conflicting values for the same attribute" means.
func sameDecimalFormat(a, b *DecimalFormat) bool {
	x, y := *a, *b
	x.Name, y.Name = xdm.QName{}, xdm.QName{}
	return x == y
}
