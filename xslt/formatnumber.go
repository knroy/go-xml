package xslt

import (
	"fmt"
	"math"
	"math/big"
	"sort"
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
func (c *compiler) compileDecimalFormat(el *xdm.Node, precedence int) error {
	df := defaultDecimalFormat()
	// Which attributes this particular declaration states. Section 16.4.1
	// compares declarations attribute by attribute: two declarations of the
	// same format conflict only where they both name an attribute and
	// disagree about it. Comparing the whole structure instead made a
	// declaration that sets only @decimal-separator conflict with one that
	// sets only @grouping-separator, because each carried the defaults for
	// everything it had not mentioned.
	stated := map[string]bool{}

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
		stated[attr] = true
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
		stated["infinity"] = true
	}
	if v := el.AttrValue("NaN"); v != "" {
		df.NaN = v
		stated["NaN"] = true
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
	//
	// Where they do not disagree they *combine*: one declaration may set the
	// decimal separator and another the grouping separator, and the format
	// ends up with both. That is why the previous declaration is merged in
	// rather than replaced.
	name := df.Name.Clark()
	if prev, dup := c.sheet.decimalFormats[name]; dup {
		switch prevPrec := c.decimalFormatPrecedence[name]; {
		case precedence > prevPrec:
			// This declaration has the higher import precedence, so it wins
			// outright. Section 16.4.1 makes conflicting declarations an
			// error only among those of the same precedence — overriding an
			// imported declaration is the ordinary reason to import at all.
		case precedence < prevPrec:
			// An imported declaration cannot displace the importing module's,
			// so this one is simply ignored.
			return nil
		default:
			// A conflict at this precedence is XTSE1290 — but only if no
			// module of higher precedence goes on to declare the same
			// format, which overrides both. The importing module is compiled
			// after the modules it imports, so that cannot be known yet: the
			// conflict is recorded and reported at the end of the
			// compilation, by checkDecimalFormatConflicts.
			if conflict, ok := conflictingAttr(c.statedDecimalFormat[name], stated, prev, df); ok {
				if c.decimalFormatConflicts == nil {
					c.decimalFormatConflicts = map[string]decimalFormatConflict{}
				}
				c.decimalFormatConflicts[name] = decimalFormatConflict{
					lexical:    df.Name.Lexical(),
					attr:       conflict,
					precedence: precedence,
				}
				break
			}
			mergeDecimalFormat(df, prev, stated)
			for a := range c.statedDecimalFormat[name] {
				stated[a] = true
			}
		}
	}

	c.sheet.decimalFormats[name] = df
	if c.statedDecimalFormat == nil {
		c.statedDecimalFormat = map[string]map[string]bool{}
	}
	c.statedDecimalFormat[name] = stated
	if c.decimalFormatPrecedence == nil {
		c.decimalFormatPrecedence = map[string]int{}
	}
	c.decimalFormatPrecedence[name] = precedence
	return nil
}

// decimalFormatAttrs pairs each xsl:decimal-format attribute name with a way
// to read its value out of a declaration, so that the merge and the conflict
// check can both work attribute by attribute rather than field by field.
var decimalFormatAttrs = []struct {
	name string
	get  func(*DecimalFormat) any
	set  func(*DecimalFormat, *DecimalFormat)
}{
	{"decimal-separator", func(d *DecimalFormat) any { return d.DecimalSeparator },
		func(d, src *DecimalFormat) { d.DecimalSeparator = src.DecimalSeparator }},
	{"grouping-separator", func(d *DecimalFormat) any { return d.GroupingSeparator },
		func(d, src *DecimalFormat) { d.GroupingSeparator = src.GroupingSeparator }},
	{"percent", func(d *DecimalFormat) any { return d.Percent },
		func(d, src *DecimalFormat) { d.Percent = src.Percent }},
	{"per-mille", func(d *DecimalFormat) any { return d.PerMille },
		func(d, src *DecimalFormat) { d.PerMille = src.PerMille }},
	{"zero-digit", func(d *DecimalFormat) any { return d.ZeroDigit },
		func(d, src *DecimalFormat) { d.ZeroDigit = src.ZeroDigit }},
	{"digit", func(d *DecimalFormat) any { return d.Digit },
		func(d, src *DecimalFormat) { d.Digit = src.Digit }},
	{"pattern-separator", func(d *DecimalFormat) any { return d.PatternSeparator },
		func(d, src *DecimalFormat) { d.PatternSeparator = src.PatternSeparator }},
	{"minus-sign", func(d *DecimalFormat) any { return d.MinusSign },
		func(d, src *DecimalFormat) { d.MinusSign = src.MinusSign }},
	{"infinity", func(d *DecimalFormat) any { return d.Infinity },
		func(d, src *DecimalFormat) { d.Infinity = src.Infinity }},
	{"NaN", func(d *DecimalFormat) any { return d.NaN },
		func(d, src *DecimalFormat) { d.NaN = src.NaN }},
}

// conflictingAttr finds an attribute that both declarations state and
// disagree about, which is the only thing XTSE1290 forbids.
func conflictingAttr(prevStated, stated map[string]bool, prev, df *DecimalFormat) (string, bool) {
	for _, a := range decimalFormatAttrs {
		if prevStated[a.name] && stated[a.name] && a.get(prev) != a.get(df) {
			return a.name, true
		}
	}
	return "", false
}

// mergeDecimalFormat copies into df every attribute the earlier declaration
// stated and this one did not, so the two combine into one format.
func mergeDecimalFormat(df, prev *DecimalFormat, stated map[string]bool) {
	for _, a := range decimalFormatAttrs {
		if !stated[a.name] {
			a.set(df, prev)
		}
	}
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
			// The name is a lexical QName whose prefix must be resolved
			// against the namespace context of the expression. That context
			// is not threaded through to a function call, so the prefix is
			// resolved against the prefixes the stylesheet declared anywhere
			// — which is enough whenever a prefix is bound consistently, and
			// that is the case a stylesheet actually writes.
			prefix, local := xdm.SplitQName(lex)
			uri := ""
			if prefix != "" {
				uri = s.prefixes[prefix]
			}
			dfName = xdm.QName{URI: uri, Local: local}.Clark()
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
	prefix, suffix  string
	minInt, minFrac int
	maxFrac         int
	// groupPositions are the digit offsets from the right at which a
	// grouping separator is inserted. A picture may place them irregularly —
	// "###,##0,00.00" groups at 2 and 4 — so a single "digits per group" is
	// not enough to express one.
	groupPositions []int
	// grouping is the repeating group size, used only when the positions form
	// a regular sequence N, 2N, 3N and the number may be longer than the
	// picture. 0 means the positions are irregular and are used as they are.
	grouping          int
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

	// Split positive and negative sub-pictures. "A picture-string must not
	// contain more than one pattern-separator-sign": a third sub-picture was
	// being discarded silently rather than reported.
	if strings.Count(pic, string(df.PatternSeparator)) > 1 {
		return "", fmt.Errorf(
			"XTDE1310: picture %q contains more than one pattern separator", pic)
	}
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
	if p.grouping > 0 || len(p.groupPositions) > 0 {
		intStr = applyGrouping(intStr, p.grouping, p.groupPositions, df.GroupingSeparator)
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
	if err := checkSubPicture(pic, runes, start, end, df); err != nil {
		return p, err
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
	hasDecimalSep := false
	for i, r := range digits {
		if r == df.DecimalSeparator {
			intPart, fracPart = digits[:i], digits[i+1:]
			hasDecimalSep = true
			break
		}
	}

	// Grouping size is the distance from the last grouping separator to the
	// end of the integer part, which is what makes "#,##,##0" (the Indian
	// lakh grouping) express a different size from "#,##0".
	// Every grouping separator's distance from the right-hand end of the
	// integer part is one grouping position. Counting only the last one
	// collapsed "###,##0,00" into a single repeating size and lost the
	// irregular grouping the picture asked for.
	//
	// The separators themselves are not digits, so the distance is measured
	// in digits seen after each one.
	digitsAfter := 0
	for i := len(intPart) - 1; i >= 0; i-- {
		if intPart[i] == df.GroupingSeparator {
			if digitsAfter > 0 {
				p.groupPositions = append(p.groupPositions, digitsAfter)
			}
			continue
		}
		digitsAfter++
	}
	// Positions at regular intervals N, 2N, 3N extend beyond the picture, so
	// that a number longer than the picture is still grouped every N digits.
	// Irregular positions do not extend: the picture states them exhaustively.
	if n := len(p.groupPositions); n > 0 {
		regular := true
		step := p.groupPositions[0]
		for k, pos := range p.groupPositions {
			if pos != step*(k+1) {
				regular = false
				break
			}
		}
		if regular {
			p.grouping = step
		}
	}

	for _, r := range intPart {
		if r == df.ZeroDigit {
			p.minInt++
		}
	}
	// Section 16.4.2: the minimum integer part size is normally the count of
	// zero-digit-signs, "but if the sub-picture contains no zero-digit-sign
	// and no decimal-separator-sign, it is set to one." That is what makes
	// format-number(0, '#') produce "0" rather than nothing at all; the
	// sub-picture "#.#" keeps a zero minimum, so it still gives ".5".
	if p.minInt == 0 && !hasDecimalSep {
		p.minInt = 1
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
// applyGrouping inserts separators into the integer digits.
//
// n is the repeating group size when the picture's grouping positions were
// regular, and positions lists them when they were not. A regular picture uses
// both: the listed positions and every multiple of n beyond them.
func applyGrouping(s string, n int, positions []int, sep rune) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return s
	}
	at := map[int]bool{}
	for _, p := range positions {
		at[p] = true
	}
	if n > 0 {
		for p := n; p < len(runes); p += n {
			at[p] = true
		}
	}
	var out []rune
	for i, r := range runes {
		// The offset from the right of the position *before* this digit is
		// what a grouping position names.
		if i > 0 && at[len(runes)-i] {
			out = append(out, sep)
		}
		out = append(out, r)
	}
	return string(out)
}

// checkSubPicture applies the section 16.4.2 rules a sub-picture must satisfy.
//
// Only one of these was enforced — that a sub-picture contains a digit — and
// the rest silently produced a number from a picture the spec says is an
// error. They are written out one rule per block, in the order the spec gives
// them, so that each can be read against the sentence it comes from.
//
// active characters are the digit, zero-digit, grouping and decimal signs;
// everything else in the sub-picture is passive.
func checkSubPicture(pic string, runes []rune, start, end int, df *DecimalFormat) error {
	active := func(r rune) bool {
		return r == df.Digit || r == df.ZeroDigit ||
			r == df.GroupingSeparator || r == df.DecimalSeparator
	}

	// "A sub-picture must not contain more than one decimal-separator-sign."
	if strings.Count(pic, string(df.DecimalSeparator)) > 1 {
		return fmt.Errorf(
			"XTDE1310: picture %q contains more than one decimal separator", pic)
	}

	// "A sub-picture must not contain more than one percent-sign or
	// per-mille-sign, and it must not contain one of each."
	nPercent := strings.Count(pic, string(df.Percent))
	nPerMille := strings.Count(pic, string(df.PerMille))
	if nPercent+nPerMille > 1 {
		return fmt.Errorf(
			"XTDE1310: picture %q contains more than one percent or per-mille sign", pic)
	}

	// "A sub-picture must not contain a passive character that is preceded by
	// an active character and that is followed by another active character."
	// The digit region runs from start to end, so a passive character inside
	// it is by definition surrounded by active ones.
	for i := start; i <= end; i++ {
		if !active(runes[i]) {
			return fmt.Errorf(
				"XTDE1310: picture %q contains %q between active characters",
				pic, string(runes[i]))
		}
	}

	// "A sub-picture must not contain a grouping-separator-sign adjacent to a
	// decimal-separator-sign."
	for i := 0; i+1 < len(runes); i++ {
		a, b := runes[i], runes[i+1]
		if (a == df.GroupingSeparator && b == df.DecimalSeparator) ||
			(a == df.DecimalSeparator && b == df.GroupingSeparator) {
			return fmt.Errorf(
				"XTDE1310: picture %q has a grouping separator adjacent to "+
					"the decimal separator", pic)
		}
	}

	// A grouping separator at the very end of the integer part has nothing to
	// group, which the adjacency rule does not cover when no decimal
	// separator follows it.
	if runes[end] == df.GroupingSeparator {
		return fmt.Errorf(
			"XTDE1310: picture %q ends the digit region with a grouping separator", pic)
	}

	// "The integer part of a sub-picture must not contain a zero-digit-sign
	// that is followed by a digit-sign", and "the fractional part ... must not
	// contain a digit-sign that is followed by a zero-digit-sign".
	digits := runes[start : end+1]
	intPart, fracPart := digits, []rune(nil)
	for i, r := range digits {
		if r == df.DecimalSeparator {
			intPart, fracPart = digits[:i], digits[i+1:]
			break
		}
	}
	seenZero := false
	for _, r := range intPart {
		switch r {
		case df.ZeroDigit:
			seenZero = true
		case df.Digit:
			if seenZero {
				return fmt.Errorf(
					"XTDE1310: the integer part of picture %q has a digit sign "+
						"after a zero-digit sign", pic)
			}
		}
	}
	seenDigit := false
	for _, r := range fracPart {
		switch r {
		case df.Digit:
			seenDigit = true
		case df.ZeroDigit:
			if seenDigit {
				return fmt.Errorf(
					"XTDE1310: the fractional part of picture %q has a "+
						"zero-digit sign after a digit sign", pic)
			}
		}
	}
	return nil
}

// decimalFormatConflict records an XTSE1290 that may yet be overridden by a
// declaration of higher import precedence.
type decimalFormatConflict struct {
	lexical    string
	attr       string
	precedence int
}

// checkDecimalFormatConflicts reports any conflict that no higher-precedence
// declaration resolved.
//
// It runs once the whole module graph has been compiled, because an importing
// module is compiled after the modules it imports and only then is it known
// whether it overrode the conflicting pair.
func (c *compiler) checkDecimalFormatConflicts() error {
	// Map order is not deterministic and the error names a format, so the
	// names are sorted to make the message reproducible.
	names := make([]string, 0, len(c.decimalFormatConflicts))
	for name := range c.decimalFormatConflicts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		conf := c.decimalFormatConflicts[name]
		if c.decimalFormatPrecedence[name] > conf.precedence {
			continue
		}
		return fmt.Errorf(
			"XTSE1290: conflicting xsl:decimal-format declarations for %q: "+
				"@%s is given two different values",
			conf.lexical, conf.attr)
	}
	return nil
}

// checkDecimalFormatSymbols reports XTSE1300 for any format whose symbols are
// not all distinct.
//
// Two symbols sharing a character make a picture string ambiguous, so the
// specification requires them to differ. The check is deferred to the end of
// compilation because a format is assembled from several declarations: one
// module may set only the decimal separator and another only the grouping
// separator, and neither is in error on its own even though the first one
// momentarily leaves the two equal at their defaults.
func (c *compiler) checkDecimalFormatSymbols() error {
	// Map order is not deterministic and the error names symbols, so the
	// formats are visited in a fixed order to make the message reproducible.
	names := make([]string, 0, len(c.sheet.decimalFormats))
	for name := range c.sheet.decimalFormats {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		df := c.sheet.decimalFormats[name]
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
	}
	return nil
}
