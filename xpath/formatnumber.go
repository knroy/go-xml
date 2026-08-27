package xpath

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// fn:format-number and the decimal-format machinery it reads.
//
// This lived in the xslt package, because XSLT 2.0 is where the function comes
// from and because the format it uses is declared by xsl:decimal-format. XPath
// 3.0 promotes it into the core function library, and a bare XPath expression
// has no stylesheet to declare a format — so the engine moves here and the
// XSLT layer keeps only the parts that read a stylesheet: compiling
// xsl:decimal-format declarations, merging them, and checking them for
// conflicts.
//
// The split is along the line the code already had. Everything below is a pure
// function of a number, a picture string and a DecimalFormat; nothing here
// knows what a stylesheet is.

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
	// ExponentSeparator is the character that introduces the exponent part of
	// a picture, which XPath 3.1 added along with scientific notation itself.
	// It is a declared symbol like every other one here because a locale that
	// writes 1,2346E4 needs to say so; the default "e" is what an undeclared
	// format uses.
	ExponentSeparator rune
}

// DefaultDecimalFormat returns the format used when none is declared.
func DefaultDecimalFormat() *DecimalFormat {
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
		ExponentSeparator: 'e',
	}
}

// FormatNumberArg is the lenient reading, in which a value that will not
// convert becomes NaN. XSLT 1.0 backwards-compatibility mode requires it:
// format-number('foo', '#') is the NaN symbol there, not an error.
//
// FormatNumberArgStrict is the XPath 3.0 reading, where the same value is
// XPTY0004.
func FormatNumberArg(args []xdm.Sequence, i int) (*xdm.Atomic, error) {
	return formatNumberArg(args, i, false)
}

// FormatNumberArgStrict rejects a first argument that does not match the
// declared xs:numeric? parameter.
func FormatNumberArgStrict(args []xdm.Sequence, i int) (*xdm.Atomic, error) {
	return formatNumberArg(args, i, true)
}

func formatNumberArg(args []xdm.Sequence, i int, strict bool) (*xdm.Atomic, error) {
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
	// A value that is not numeric and does not convert to one does not match
	// the declared xs:numeric? parameter: that is a type error, not a request
	// to format NaN. An xs:untypedAtomic is the exception — the function
	// conversion rules do turn one into a double, and a bad lexical form
	// there really is NaN.
	conv, err := CastAtomic(a, xdm.TypeDouble)
	if err != nil {
		// An xs:untypedAtomic really is NaN: the function conversion rules do
		// turn one into a double, and a bad lexical form there is NaN rather
		// than a type error. Anything else does not match the declared
		// xs:numeric? parameter at all.
		if !strict || a.Type == xdm.TypeUntypedAtomic {
			return xdm.NewDouble(math.NaN()), nil
		}
		return nil, xdm.ErrType(
			"format-number: expected a number, got %s", a.TypeName())
	}
	return conv, nil
}

func FormatNumberString(args []xdm.Sequence, i int) (string, error) {
	atoms := xdm.Atomize(args[i])
	if len(atoms) == 0 {
		return "", nil
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok {
		return "", xdm.ErrType("format-number: expected a string")
	}
	// The picture is declared xs:string. A number is not one, and the
	// function conversion rules do not turn it into one, so writing
	// format-number(931.45, 931.45) is a type error rather than a picture of
	// "931.45".
	if !isStringLike(a.Type) {
		return "", xdm.ErrType(
			"format-number: the picture must be a string, got %s", a.TypeName())
	}
	return a.String(), nil
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
	// fracGroupPositions are the digit offsets from the *left* of the
	// fractional part at which a separator is inserted. The fraction groups
	// outward from the decimal point, the opposite direction from the integer
	// part, so its positions cannot share that field.
	fracGroupPositions []int
	// minExp is the minimum width of the exponent's digits, and exponent
	// reports whether the picture had an exponent part at all. A picture may
	// ask for a zero-width exponent, so the presence of the part cannot be
	// inferred from minExp alone.
	exponent bool
	minExp   int
	// expSuffix is the passive text that followed the exponent's digits, kept
	// apart from the mantissa's own suffix because it belongs after the
	// exponent rather than between the mantissa and it.
	expSuffix string
	// intHasDigitSign records whether the integer part of the sub-picture held
	// an optional-digit-sign. With a minimum integer size of zero the mantissa
	// is scaled below 1, and this is what decides whether the leading zero is
	// written: "#.#e0" gives 0.2e0 but ".#e0" gives .2e0, and the only
	// difference between them is the "#".
	intHasDigitSign bool
}

// formatNumber2 implements fn:format-number.
//
// The picture language is small but has three rules that a naive
// implementation misses: the '#' and '0' digit characters mean *minimum*
// versus *optional* places rather than literal digits, grouping size comes
// from the position of the last separator rather than being fixed at three,
// and a picture may carry two sub-pictures separated by ';' where the second
// is used for negative numbers instead of prefixing a minus sign.
func FormatNumber(num *xdm.Atomic, pic string, df *DecimalFormat) (string, error) {
	return FormatNumberVersion(num, pic, df, XPath20)
}

// FormatNumberVersion is FormatNumber for a caller that knows the language
// version, which decides the error code for a malformed picture.
//
// XSLT 2.0 raises XTDE1310 and XPath 3.0 raises FODF1310 for the same
// condition — the picture's syntax — so the code is a property of the caller
// rather than of the check. Every message is written with the XSLT code and
// rewritten here, since the two differ only in that prefix.
func FormatNumberVersion(num *xdm.Atomic, pic string, df *DecimalFormat, v Version) (string, error) {
	out, err := formatNumberImpl(num, pic, df, v)
	if err != nil && v.atLeast30() {
		return "", errors.New(strings.Replace(err.Error(), "XTDE1310", "FODF1310", 1))
	}
	return out, err
}

func formatNumberImpl(num *xdm.Atomic, pic string, df *DecimalFormat, v Version) (string, error) {
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

	p, err := parsePicture(chosen, df, v)
	if err != nil {
		return "", err
	}

	f := num.Float64()
	if math.IsInf(f, 0) {
		sign := ""
		if f < 0 && !explicitNegative {
			sign = string(df.MinusSign)
		}
		return sign + p.prefix + df.Infinity + p.suffix, nil
	}

	// Scaling by percent or per-mille happens before rounding, so
	// format-number(0.5, '#0%') is "50%" rather than "0%".
	//
	// The scaling is done in the value's own arithmetic first, because a
	// double near its maximum overflows to infinity when multiplied — 1e308
	// as a percentage is Infinity, not the 310-digit integer exact rational
	// arithmetic would produce. Only a value that stays finite is carried on
	// as a rational, where the extra precision is what rounding needs.
	scale := 1.0
	switch {
	case p.percent:
		scale = 100
	case p.perMille:
		scale = 1000
	}
	if scale != 1 && (num.Type == xdm.TypeDouble || num.Type == xdm.TypeFloat) {
		if scaled := f * scale; math.IsInf(scaled, 0) {
			sign := ""
			if scaled < 0 && !explicitNegative {
				sign = string(df.MinusSign)
			}
			return sign + p.prefix + df.Infinity + p.suffix, nil
		}
	}
	var value *big.Rat
	if scale != 1 && (num.Type == xdm.TypeDouble || num.Type == xdm.TypeFloat) {
		// The scaled value is what a double multiplication produces, not the
		// exact product: format-number(x, '0.0…‰') has to agree with
		// format-number(x * 1000, '0.0…'), and only doing the arithmetic the
		// same way makes it. Rounding to the double first and converting
		// afterwards is what gives the same digits.
		value = formatRatOf(xdm.NewDouble(f * scale))
	} else {
		value = formatRatOf(num)
		if scale != 1 {
			value = new(big.Rat).Mul(value, new(big.Rat).SetInt64(int64(scale)))
		}
	}
	if negative {
		value = new(big.Rat).Abs(value)
	}

	// Scientific notation divides the value by a power of ten so that the
	// integer part comes out the width the mantissa's picture asks for, and
	// reports that power as the exponent.
	//
	// The chosen scaling is the spec's: with a minimum integer size of N > 0
	// the mantissa lands in [10^(N-1), 10^N), so "000.0e0" on 0.2 gives
	// 200.0e-3 rather than 2.0e-1 (numberformat255). A minimum size of zero
	// is the other case — the mantissa is put below 1 instead, which is why
	// "#.99e99" on 12345.678 is 0.12e05 and not 1.23e04 (numberformat131).
	exp := 0
	if p.exponent {
		exp = scaleForExponent(value, p.minInt)
		value = scaleByPowerOfTen(value, -exp)
		// With no mandatory integer digit the mantissa sits below 1, so every
		// significant digit it has is in the fraction. A picture that asks for
		// no fractional places at all — "#e0", "#.e0" — would then round the
		// whole value away to "0e0"; the rule that a formatted number carries
		// at least one digit means the fraction gets one place regardless
		// (numberformat231, numberformat135).
		if p.minInt == 0 && p.maxFrac < 1 {
			p.maxFrac = 1
		}
	}

	// The exponent is chosen from the value *before* rounding and is not
	// revisited afterwards. Rounding can carry the mantissa up into the next
	// power of ten — 0.99999999 against "0.0e0" scales to 9.9999999e-1 and
	// rounds to 10.0 — and the spec keeps that extra digit rather than
	// re-normalising to 1.0e0, so the answer really is "10.0e-1"
	// (numberformat304).
	rounded := roundToPlaces(value, p.maxFrac)
	intPart, fracPart := splitRat(rounded, p.maxFrac)

	// A mantissa scaled below 1 still writes its leading zero when the
	// picture's integer part had an optional-digit-sign to write it into. That
	// is the one place padInt's "drop the zero" rule has to be suspended: the
	// integer digit is not being omitted, it is the whole integer part of a
	// value below one.
	keepZero := p.exponent && p.minInt == 0 && p.intHasDigitSign
	intStr := padInt(intPart, p.minInt, df, keepZero)
	if p.grouping > 0 || len(p.groupPositions) > 0 {
		intStr = applyGrouping(intStr, p.grouping, p.groupPositions, df.GroupingSeparator)
	}
	// A mantissa below 1 that rounded up to exactly 1 has all of its
	// significance in the digit trimming is about to remove: ".#e0" on
	// 0.99999999 rounds to 1.0, and dropping the optional trailing zero would
	// leave "1e0" when the answer is "1.0e0" (numberformat301). Keeping one
	// place applies only where the picture put the digits in the fraction to
	// begin with.
	minFrac := p.minFrac
	if p.exponent && p.minInt == 0 && !p.intHasDigitSign && minFrac == 0 && intPart != "0" {
		minFrac = 1
	}
	fracStr := trimFraction(fracPart, minFrac, df)
	if len(p.fracGroupPositions) > 0 {
		fracStr = applyFracGrouping(fracStr, p.fracGroupPositions, df.GroupingSeparator)
	}

	// Section 16.4.5: the formatted number always has at least one digit. A
	// picture of optional digits only leaves both parts empty for a value of
	// zero, which would render as nothing at all. The digit goes into
	// whichever part the picture actually has: a picture with a decimal
	// separator puts it after the separator, so format-number(0, '#.#') is
	// ".0" and format-number(0, '###') is "0".
	if intStr == "" && fracStr == "" {
		if p.maxFrac > 0 {
			fracStr = string(df.ZeroDigit)
		} else {
			intStr = string(df.ZeroDigit)
		}
	}

	var sb strings.Builder
	// Section 16.4.3: with only one sub-picture, "the prefix for the negative
	// sub-picture is set by concatenating the minus-sign character and the
	// prefix for the positive sub-picture (if any), in that order" — so the
	// sign goes in front of the whole prefix, not between it and the digits.
	if negative && !explicitNegative {
		sb.WriteRune(df.MinusSign)
	}
	sb.WriteString(p.prefix)
	sb.WriteString(intStr)
	if fracStr != "" {
		sb.WriteRune(df.DecimalSeparator)
		sb.WriteString(fracStr)
	}
	sb.WriteString(p.suffix)
	if p.exponent {
		sb.WriteRune(df.ExponentSeparator)
		// The exponent's own minus sign is the format's, but its digits are
		// padded to the picture's width independently of the sign: "9.9999e99"
		// on 0.05 is 5.0000e-02, two digits after the sign rather than in
		// total (numberformat117).
		if exp < 0 {
			sb.WriteRune(df.MinusSign)
		}
		digits := strconv.Itoa(exp)
		digits = strings.TrimPrefix(digits, "-")
		for len(digits) < p.minExp {
			digits = "0" + digits
		}
		sb.WriteString(translateDigits(digits, df.ZeroDigit))
		sb.WriteString(p.expSuffix)
	}
	return sb.String(), nil
}

// formatRatOf converts a numeric atomic to a rational for formatting.
//
// Distinct from the arithmetic ratOf, which takes a double's exact binary
// value: this one goes through the shortest decimal representation, for the
// reason spelled out below.
func formatRatOf(a *xdm.Atomic) *big.Rat {
	if r := a.Rat(); r != nil {
		return new(big.Rat).Set(r)
	}
	// A double is formatted from its *shortest* decimal representation, not
	// from the exact binary value it holds. Section 16.4.2 defines the result
	// in terms of the value converted to a string, and xs:double's lexical
	// form is the shortest one that round-trips: format-number(1E23,
	// '####...') is "100000000000000000000000", where the exact binary value
	// would print 99999999999999991611392.
	f := a.Float64()
	r := new(big.Rat)
	if math.IsInf(f, 0) || math.IsNaN(f) {
		r.SetFloat64(0)
		return r
	}
	if _, ok := r.SetString(strconv.FormatFloat(f, 'g', -1, 64)); !ok {
		r.SetFloat64(f)
	}
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

// splitExponent separates a sub-picture's mantissa from its exponent part.
//
// The spec's rule is narrow, and the suite tests every corner of it: the
// exponent part exists only where the separator is followed by one or more
// members of the digit family *and nothing else*. So "9.9999e99" has one,
// while "9.9999eDog" does not — the "e" there is an ordinary passive suffix
// character and the number formats without scientific notation at all
// (numberformat113). Anything else after the digits, as in "9.9999e99end",
// is suffix rather than a reason to reject the picture (numberformat143).
//
// The separator is searched for from the left, so a second one lands inside
// the trailing text and is passive: "9.9999e99e" keeps its final "e"
// (numberformat144). But "9.99e99e99" is an error, because the text after the
// exponent digits then contains a separator followed by digits, which is a
// second exponent part rather than a suffix (numberformat108).
func splitExponent(runes []rune, df *DecimalFormat) (mantissa, exp []rune, has bool, err error) {
	for i, r := range runes {
		if r != df.ExponentSeparator {
			continue
		}
		rest := runes[i+1:]
		// The digit run immediately after the separator is the exponent's
		// width; the separator only introduces an exponent when at least one
		// digit follows it.
		n := 0
		for n < len(rest) && isDigitOfFamily(rest[n], df.ZeroDigit) {
			n++
		}
		if n == 0 {
			continue
		}
		tail := rest[n:]
		// A second separator-plus-digits in the tail is a second exponent
		// part, which the spec makes an error rather than passive text.
		for j, t := range tail {
			if t == df.ExponentSeparator && j+1 < len(tail) &&
				isDigitOfFamily(tail[j+1], df.ZeroDigit) {
				return nil, nil, false, fmt.Errorf(
					"XTDE1310: picture %q contains more than one exponent separator",
					string(runes))
			}
		}
		return runes[:i], rest[:n], true, nil
	}
	return runes, nil, false, nil
}

// parsePicture reads one sub-picture.
//
// v decides whether an exponent part is recognised at all: scientific notation
// arrived in XPath 3.1, and 3.0 is required to reject "9.9999e999" as a
// malformed picture rather than to format it (numberformat128). So under 3.0
// the separator is never split off, and the "e" is then caught by the passive
// character rule as it was before.
func parsePicture(pic string, df *DecimalFormat, v Version) (picture, error) {
	var p picture
	p.maxFrac = -1

	runes := []rune(pic)
	if v.atLeast31() {
		mantissa, exp, has, err := splitExponent(runes, df)
		if err != nil {
			return p, err
		}
		if has {
			// The suffix that followed the exponent digits is carried
			// separately: the mantissa's own parse must not see it, or its
			// digit-region scan would run past the exponent entirely.
			p.exponent = true
			p.minExp = len(exp)
			p.expSuffix = string(runes[len(mantissa)+1+len(exp):])
			runes = mantissa
			pic = string(runes)
		}
	}
	// Locate the digit region: the span containing digit, zero, grouping and
	// decimal characters. Everything before it is the prefix and everything
	// after is the suffix.
	start, end := -1, -1
	for i, r := range runes {
		if r == df.Digit || isDigitOfFamily(r, df.ZeroDigit) ||
			r == df.GroupingSeparator || r == df.DecimalSeparator {
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
	if strings.ContainsRune(p.prefix+p.suffix+p.expSuffix, df.Percent) {
		p.percent = true
	}
	if strings.ContainsRune(p.prefix+p.suffix+p.expSuffix, df.PerMille) {
		p.perMille = true
	}
	// "A sub-picture must not contain both an exponent-separator-sign and a
	// percent-sign or per-mille-sign": the two ask for incompatible scalings
	// of the same value, so the picture is rejected rather than one of them
	// silently winning (numberformat110).
	if p.exponent && (p.percent || p.perMille) {
		return p, fmt.Errorf(
			"XTDE1310: picture %q combines an exponent with a percent or "+
				"per-mille sign", pic)
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
		// The leading group must fit the interval too, or the separators are
		// stated exhaustively after all: "###,##,00" places them two apart
		// behind a three-digit group, so 123456789 is "12345,67,89" and not
		// the "1,23,45,67,89" a repeat every two would give.
		if regular && intDigitCount(intPart, df) > step*(n+1) {
			regular = false
		}
		if regular {
			p.grouping = step
		}
	}

	for _, r := range intPart {
		// Every member of the digit family counts, not just the zero-digit
		// itself: "001", "000" and "999" all specify three mandatory digits,
		// since the digits within a picture are interchangeable and only
		// their count carries meaning. Counting only the zero-digit made
		// format-number(42, '001') pad to two places and answer "421".
		if isDigitOfFamily(r, df.ZeroDigit) {
			p.minInt++
		}
		if r == df.Digit {
			p.intHasDigitSign = true
		}
	}
	// Section 16.4.2: the minimum integer part size is normally the count of
	// zero-digit-signs, "but if the sub-picture contains no zero-digit-sign
	// and no decimal-separator-sign, it is set to one." That is what makes
	// format-number(0, '#') produce "0" rather than nothing at all; the
	// sub-picture "#.#" keeps a zero minimum, so it still gives ".5".
	// Under an exponent the mantissa's scaling already guarantees a digit, and
	// forcing a minimum of one here would change which power of ten is chosen:
	// "#e0" on 0.2 is 0.2e0, so its minimum integer size stays zero even
	// though the sub-picture has no decimal separator (numberformat231).
	if p.minInt == 0 && !hasDecimalSep && !p.exponent {
		p.minInt = 1
	}
	if len(fracPart) > 0 {
		// A separator in the fractional part groups outward from the decimal
		// point: "#.##,##,##" separates after two digits and after four.
		digitsBefore := 0
		for _, r := range fracPart {
			if r == df.GroupingSeparator {
				if digitsBefore > 0 {
					p.fracGroupPositions = append(p.fracGroupPositions, digitsBefore)
				}
				continue
			}
			digitsBefore++
		}
		p.maxFrac = 0
		for _, r := range fracPart {
			switch {
			case isDigitOfFamily(r, df.ZeroDigit):
				p.minFrac++
				p.maxFrac++
			case r == df.Digit:
				p.maxFrac++
			}
		}
	} else {
		p.maxFrac = 0
	}
	return p, nil
}

// scaleForExponent returns the power of ten to divide a non-negative value by
// so that its integer part has exactly minInt digits.
//
// A minInt of zero means the mantissa belongs below 1 — the picture asked for
// no mandatory integer digit, and the spec then normalises into [0.1, 1)
// rather than [1, 10).
//
// Zero has no meaningful magnitude, so it keeps an exponent of zero and is
// printed with whatever digits the picture demands (numberformat321-327).
func scaleForExponent(value *big.Rat, minInt int) int {
	if value.Sign() == 0 {
		return 0
	}
	// The comparison is done on exact rationals rather than via a logarithm:
	// a float log10 of a value near a power of ten lands on the wrong side of
	// the boundary often enough to matter, and the whole point of carrying a
	// big.Rat this far is that the digits are exact.
	abs := new(big.Rat).Abs(value)
	exp := 0
	// upper is the first power of ten the mantissa must stay below, and lower
	// the one it must reach: [10^(minInt-1), 10^minInt) for a positive minInt,
	// and [0.1, 1) when it is zero.
	upper := powerOfTen(minInt)
	lower := powerOfTen(minInt - 1)
	for abs.Cmp(upper) >= 0 {
		abs = scaleByPowerOfTen(abs, -1)
		exp++
	}
	for abs.Cmp(lower) < 0 {
		abs = scaleByPowerOfTen(abs, 1)
		exp--
	}
	return exp
}

// powerOfTen returns 10^n as an exact rational, for negative n as well.
func powerOfTen(n int) *big.Rat {
	if n < 0 {
		return new(big.Rat).Inv(powerOfTen(-n))
	}
	return new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil))
}

// scaleByPowerOfTen multiplies a rational by 10^n.
func scaleByPowerOfTen(r *big.Rat, n int) *big.Rat {
	return new(big.Rat).Mul(r, powerOfTen(n))
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

// isDigitOfFamily reports whether r is one of the ten digits of the family
// whose zero is zero.
func isDigitOfFamily(r, zero rune) bool {
	return r >= zero && r < zero+10
}

// containsFamilyDigit reports whether s holds any digit of the family.
func containsFamilyDigit(s string, zero rune) bool {
	for _, r := range s {
		if isDigitOfFamily(r, zero) {
			return true
		}
	}
	return false
}

// padInt left-pads the integer digits to the minimum width, translating into
// the format's digit family.
func padInt(s string, minInt int, df *DecimalFormat, keepZero bool) string {
	if s == "0" && minInt == 0 && !keepZero {
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

// applyFracGrouping inserts separators into the fractional digits at the
// offsets a picture asked for, counted from the decimal point.
//
// A regular pattern repeats outward: "#.##,##,##" groups every two digits
// however long the fraction is. An irregular one applies only where written.
func applyFracGrouping(s string, positions []int, sep rune) string {
	if s == "" || len(positions) == 0 {
		return s
	}
	step := 0
	if regular := fracRegularInterval(positions); regular > 0 {
		step = regular
	}
	runes := []rune(s)
	var sb strings.Builder
	for i, r := range runes {
		if i > 0 {
			if step > 0 {
				if i%step == 0 {
					sb.WriteRune(sep)
				}
			} else {
				for _, p := range positions {
					if p == i {
						sb.WriteRune(sep)
					}
				}
			}
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// fracRegularInterval returns the repeating interval fractional grouping
// positions imply, or 0 when they are irregular.
func fracRegularInterval(positions []int) int {
	if len(positions) == 0 {
		return 0
	}
	step := positions[0]
	if step <= 0 {
		return 0
	}
	for i, p := range positions {
		if p != step*(i+1) {
			return 0
		}
	}
	return step
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
		return r == df.Digit || isDigitOfFamily(r, df.ZeroDigit) ||
			r == df.GroupingSeparator || r == df.DecimalSeparator
	}

	// "A sub-picture must contain at least one digit-sign or zero-digit-sign."
	// The grouping and decimal separators are active characters too, so a
	// sub-picture like "fred.ginger" gets past the digit-region scan without
	// carrying a single place to put a digit in.
	if !strings.ContainsRune(pic, df.Digit) && !containsFamilyDigit(pic, df.ZeroDigit) {
		return fmt.Errorf(
			"XTDE1310: picture %q contains no digit-sign or zero-digit-sign", pic)
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
	// decimal-separator-sign", nor two adjacent to each other: "#,,###" has a
	// separator with nothing between it and the next, which groups nothing.
	for i := 0; i+1 < len(runes); i++ {
		a, b := runes[i], runes[i+1]
		if a == df.GroupingSeparator && b == df.GroupingSeparator {
			return fmt.Errorf(
				"XTDE1310: picture %q has two adjacent grouping separators", pic)
		}
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

// registerFormatNumber adds fn:format-number for a bare XPath expression.
//
// Only the two-argument form: the third argument names an xsl:decimal-format,
// and an expression outside a stylesheet has none to name. The XSLT layer
// registers its own three-argument version over this one, which is why this is
// marked Since XPath30 and that one is not.
func registerFormatNumber(l *Library) {
	// Both arities. The three-argument form names a decimal format, and a
	// bare XPath expression has none declared — so only the absent name
	// resolves, and any other is FODF1280 rather than XPST0017. A host that
	// does have named formats registers its own over this one.
	l.registerFnSince(XPath30, "format-number", []int{2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		num, err := FormatNumberArgStrict(args, 0)
		if err != nil {
			return nil, err
		}
		pic, err := FormatNumberString(args, 1)
		if err != nil {
			return nil, err
		}
		if len(args) > 2 && len(args[2]) > 0 {
			name, err := FormatNumberString(args, 2)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(name) != "" {
				return nil, fmt.Errorf(
					"FODF1280: no decimal format named %q is declared", name)
			}
		}
		// The context's own version, not a fixed XPath30: scientific notation
		// in the picture is a 3.1 feature, and a 3.0 expression must still be
		// told that "9.9999e999" is malformed.
		out, err := FormatNumberVersion(num, pic, DefaultDecimalFormat(), ctx.Version)
		if err != nil {
			return nil, err
		}
		return strSeq(out), nil
	})
}

// intDigitCount counts the digit positions in a sub-picture's integer part,
// which is the picture's own width for deciding whether a grouping interval
// repeats.
func intDigitCount(intPart []rune, df *DecimalFormat) int {
	n := 0
	for _, r := range intPart {
		if r == df.Digit || isDigitOfFamily(r, df.ZeroDigit) {
			n++
		}
	}
	return n
}
