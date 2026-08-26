package xslt

import (
	"fmt"
	"sort"

	"unicode"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// DecimalFormat holds an xsl:decimal-format declaration.
//
// The type and the formatting engine live in the xpath package, because XPath
// 3.0 has fn:format-number without a stylesheet to declare a format. This
// alias keeps the name a stylesheet-facing one: xsl:decimal-format compiles to
// an xslt.DecimalFormat exactly as it did.
type DecimalFormat = xpath.DecimalFormat

// defaultDecimalFormat returns the format used when none is declared.
func defaultDecimalFormat() *DecimalFormat { return xpath.DefaultDecimalFormat() }

// DecimalFormat holds an xsl:decimal-format declaration.
//
// Every symbol is configurable because the instruction exists to serve
// locales: a German invoice writes 1.234,56 where an English one writes
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
			// Section 16.4.1 resolves a named decimal format attribute by
			// attribute: "the effective value of each attribute is taken
			// from an xsl:decimal-format declaration that has that name, and
			// that specifies an explicit value for the required attribute
			// ... If there is more than one such declaration, the one with
			// highest import precedence is used." So this declaration wins
			// only the attributes it actually states; everything it leaves
			// out keeps the imported declaration's value rather than
			// reverting to the default.
			mergeDecimalFormat(df, prev, stated)
			for a := range c.statedDecimalFormat[name] {
				stated[a] = true
			}
		case precedence < prevPrec:
			// An imported declaration cannot displace the importing module's
			// choices, but it still supplies the attributes that module left
			// unstated. The registered format is updated in place so the
			// higher-precedence declaration keeps its own values.
			prevStated := c.statedDecimalFormat[name]
			if prevStated == nil {
				prevStated = map[string]bool{}
				c.statedDecimalFormat[name] = prevStated
			}
			mergeDecimalFormat(prev, df, prevStated)
			for a := range stated {
				prevStated[a] = true
			}
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
		num, err := xpath.FormatNumberArg(args, 0)
		if err != nil {
			return nil, err
		}
		picture, err := xpath.FormatNumberString(args, 1)
		if err != nil {
			return nil, err
		}

		dfName := ""
		if len(args) > 2 {
			lex, err := xpath.FormatNumberString(args, 2)
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

		out, err := xpath.FormatNumber(num, picture, df)
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
