package xpath

import (
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// register31Funcs adds the XPath 3.1 functions that need nothing from the map
// and array libraries.
//
// Each is marked Since XPath31, so a 3.0 expression calling one gets XPST0017
// — the same "unknown function" every other processor raises — rather than a
// working answer.
func register31Funcs(l *Library) {
	registerParseIETFDate(l)
	registerContainsToken(l)
	registerCollationKey(l)
	registerRandomNumberGenerator(l)
	registerNumericConstructor(l)
	registerApply(l)
	registerLoadXQueryModule(l)
	registerDefaultLanguage(l)
	registerTransform(l)
}

// --- fn:transform -----------------------------------------------------------

// registerTransform adds fn:transform, F&O 3.1 section 14.7.1.
//
// The function runs a transformation described by an options map and returns
// its results as a map. Doing that needs an XSLT processor, and this package
// does not depend on xslt — the layering is one-directional and stays that
// way. So what is registered here is a stub that declines, which is the
// honest answer for a caller evaluating a bare XPath expression: there is no
// transformation to run.
//
// xslt/fntransform.go overrides it for the duration of a transform, the same
// way key() and current() are bound per transform. A stylesheet therefore has
// fn:transform and a plain xpath.Eval caller does not, which is the
// separation every other XSLT-only function here already draws.
//
// FOXT0004 is the code the specification gives for a processor that cannot
// run the transformation: "the implementation does not support the
// transformation". The two cases in scope assert it, both declaring the
// fn-transform-XSLT feature unsatisfied.
func registerTransform(l *Library) {
	l.registerFnSince(XPath31, "transform", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The argument is still checked: a call passing something that is not
		// a map is wrong whatever the implementation supports.
		it, err := args[0].Single()
		if err != nil {
			return nil, err
		}
		if _, ok := it.(*xdm.MapItem); !ok {
			return nil, xdm.ErrType(
				"fn:transform: the options must be a map, got %s", it.TypeName())
		}
		return nil, xdm.Errorf("FOXT0004",
			"fn:transform: this implementation does not support the requested transformation")
	})
}

// --- fn:default-language ----------------------------------------------------

// registerDefaultLanguage adds fn:default-language, F&O 3.1 section 13.4.
//
// The function reports the default language component of the dynamic context,
// which is the language fn:format-date, fn:format-time and fn:format-integer
// fall back on when their own language argument is absent or empty. This
// engine's formatters have exactly one set of names and ordinals — English —
// so the honest report is "en", and the suite's consistency cases check
// precisely that: format-integer(17, "Ww") must equal the same call with
// default-language() passed explicitly.
//
// The result is annotated xs:language rather than left as a plain xs:string.
// The signature's return type is xs:language, and the suite asserts it with
// assert-type, so returning an unannotated string would produce the right
// characters and still fail.
func registerDefaultLanguage(l *Library) {
	l.registerFnSince(XPath31, "default-language", []int{0}, func(_ *Context, _ []xdm.Sequence) (xdm.Sequence, error) {
		return xdm.One(xdm.NewString("en").WithDerived("language")), nil
	})
}

// --- fn:load-xquery-module --------------------------------------------------

// registerLoadXQueryModule adds fn:load-xquery-module, F&O 3.1 section 14.6.1.
//
// The function compiles an XQuery library module and hands back its variables
// and functions. This engine implements XPath and XSLT and has no XQuery
// processor, which the specification anticipates: FOQM0006 is defined as
// "the implementation does not support the load-xquery-module function", and
// raising it is the conforming answer rather than a gap.
//
// Every code this can raise describes the missing processor, so it reports
// that and nothing else. The suite's apparent contradictions all dissolve once
// the feature dependency is read: the set declares
// fn-load-xquery-module satisfied="true" and overrides fourteen cases to
// satisfied="false", and only those fourteen describe a processor without one.
//
// So -001/-002 want FOQM0001 for an empty URI and -003/-004 want FOQM0002 for
// a module that cannot be located, but all four are written for an
// implementation that has a processor and are out of scope here. The cases
// that do apply — -901, -902, -903 and function-lookup-764 — accept FOQM0006
// throughout, and -901/-902 accept either that or FOQM0001. Reporting the
// absent processor uniformly satisfies all of them, and it is the only claim
// this function can make truthfully: it never looked for the module, so it
// cannot say the module was not found.
func registerLoadXQueryModule(l *Library) {
	l.registerFnSince(XPath31, "load-xquery-module", []int{1, 2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// Not even the empty-URI check runs first. Rejecting the argument
		// would claim the call got far enough to inspect it, and
		// function-lookup-761 passes an integer where a string is declared
		// and still wants FOQM0006 — the absent processor outranks the
		// argument. -901 and -902 accept FOQM0001 here as well, so nothing
		// is lost by reporting the one true thing.
		return nil, xdm.Errorf("FOQM0006",
			"fn:load-xquery-module: this implementation has no XQuery processor")
	})
}

// --- fn:apply ---------------------------------------------------------------

// registerApply adds fn:apply($function, $array), F&O 3.1 section 16.3.4.
//
// It calls a function with the members of an array as its arguments, which is
// the only way to make a call whose arity is not known until run time: every
// other spelling fixes the number of arguments in the expression itself.
//
// An arity mismatch is FOAP0001 rather than the XPTY0004 an ordinary call
// raises. The distinction is worth keeping: with fn:apply the count comes from
// a value rather than from the source text, so the caller may not be able to
// see the mismatch by reading the expression.
func registerApply(l *Library) {
	l.registerFnSince(XPath31, "apply", []int{2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		fnItem, err := args[0].Single()
		if err != nil {
			return nil, err
		}
		// functionItemView rather than a type assertion: a map and an array
		// are function items too, and "map?next => apply([])" applies one.
		// Asserting *xdm.FunctionItem refused them.
		fn := functionItemView(fnItem)
		if fn == nil {
			return nil, xdm.ErrType(
				"fn:apply: the first argument must be a function, got %s", fnItem.TypeName())
		}
		arrItem, err := args[1].Single()
		if err != nil {
			return nil, err
		}
		arr, ok := arrItem.(*xdm.ArrayItem)
		if !ok {
			return nil, xdm.ErrType(
				"fn:apply: the second argument must be an array, got %s", arrItem.TypeName())
		}
		members := arr.Members()
		if fn.Arity != len(members) {
			return nil, xdm.Errorf("FOAP0001",
				"fn:apply: %s takes %d argument(s), but the array has %d member(s)",
				fn.String(), fn.Arity, len(members))
		}
		return fn.Invoke(ctx, members)
	})
}

// --- fn:collation-key -------------------------------------------------------

// registerCollationKey adds fn:collation-key($key, $collation?) as
// xs:base64Binary.
//
// The result is only required to be a value that is equal for strings the
// collation calls equal, and ordered the way the collation orders them. Every
// Collation here already computes such a key for fn:distinct-values and
// xsl:for-each-group; this exposes it, base64-encoded because the signature
// says xs:base64Binary.
//
// Base64 is order-preserving over the octets it encodes only because the keys
// compared are of the same length or share a prefix — which is not generally
// true, so the codepoint collation's key is the string's own UTF-8 and the
// suite's ordering tests ("abc" lt "ABC" under caseFirst=lower) hold through
// the UCA sort key, whose octets are ordered by construction.
func registerCollationKey(l *Library) {
	l.registerFnSince(XPath31, "collation-key", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The parameter is xs:string, so a number is XPTY0004 rather than
		// something to stringify: collation-key(123) is an error the suite
		// asserts, where fn:string() would have accepted it.
		key, err := argStringRequired(args, 0)
		if err != nil {
			return nil, err
		}
		coll, err := collationArgCtx(ctx, "collation-key", args, 1)
		if err != nil {
			return nil, err
		}
		return xdm.One(xdm.NewBinary(
			base64.StdEncoding.EncodeToString([]byte(collationKeyOf(coll, key))),
			xdm.TypeBase64Binary)), nil
	})
}

// collationKeyOf returns the collation's own key for a string.
//
// Key is not part of the Collation interface, because most callers only ever
// compare. Every implementation here has one, but a host application may
// register a collation that does not, and that must not panic: without a key
// the best available answer is the string itself, which is right whenever the
// collation is equality-compatible with codepoint and wrong only in a way the
// host chose by not supplying a key.
func collationKeyOf(c Collation, s string) string {
	if k, ok := c.(interface{ Key(string) string }); ok {
		return k.Key(s)
	}
	return s
}

// --- fn:contains-token ------------------------------------------------------

// registerContainsToken adds
// fn:contains-token($input as xs:string*, $token as xs:string, $collation?).
//
// The input is a *sequence* of strings, each of which is split on whitespace;
// the function is true when any resulting token equals the trimmed $token
// under the collation. The token is trimmed but not split, so a $token holding
// two words can never match — " abc  " matches the token "abc", while
// "abc def" matches nothing.
func registerContainsToken(l *Library) {
	l.registerFnSince(XPath31, "contains-token", []int{2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		coll, err := collationArgCtx(ctx, "contains-token", args, 2)
		if err != nil {
			return nil, err
		}
		token, err := argStringRequired(args, 1)
		if err != nil {
			return nil, err
		}
		// A token that is empty once trimmed can match nothing, because
		// splitting on whitespace never yields an empty token. Returning
		// early also keeps the collation from being asked to compare "" with
		// every token, which a case-blind collation would answer false for
		// anyway but a host collation might not.
		token = trimXMLSpace(token)
		if token == "" {
			return boolSeq(false), nil
		}
		// The first argument is xs:string*, so each item is atomised and
		// compared in turn rather than being joined: the tokens of one item
		// never run into the next.
		atoms, err := xdm.AtomizeChecked(args[0])
		if err != nil {
			return nil, err
		}
		for i := range atoms {
			s, err := stringArgValue(atoms[i].(*xdm.Atomic), 0)
			if err != nil {
				return nil, err
			}
			for _, t := range splitXMLSpace(s) {
				if coll.Compare(t, token) == 0 {
					return boolSeq(true), nil
				}
			}
		}
		return boolSeq(false), nil
	})
}

// isXMLSpace reports whether r is one of the four characters XML calls
// whitespace.
//
// Deliberately not unicode.IsSpace: the suite asserts that a form feed and a
// no-break space are *not* separators, and Go's IsSpace calls both of them
// whitespace. Using it would have made "abcdef" a single token where the
// spec makes it two, and "abc def" two where the spec makes it one.
func isXMLSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func trimXMLSpace(s string) string { return strings.TrimFunc(s, isXMLSpace) }

// splitXMLSpace splits on runs of XML whitespace, dropping empty tokens.
func splitXMLSpace(s string) []string {
	return strings.FieldsFunc(s, isXMLSpace)
}

// --- fn:parse-ietf-date -----------------------------------------------------

// registerParseIETFDate adds
// fn:parse-ietf-date($value as xs:string?) as xs:dateTime?.
//
// The grammar in F&O 3.1 §9.10 covers RFC 5322, RFC 1123 and asctime forms at
// once, and is looser than any of them: the day name is optional and ignored
// even when it contradicts the date, the year may appear either before the
// time or after the timezone, and hyphens may stand in for spaces between the
// date fields.
func registerParseIETFDate(l *Library) {
	l.registerFnSince(XPath31, "parse-ietf-date", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The argument is xs:string?, so the empty sequence passes straight
		// through rather than being an error or an empty string.
		if len(args) == 0 || len(args[0]) == 0 {
			return xdm.Empty(), nil
		}
		s, err := argStringRequired(args, 0)
		if err != nil {
			return nil, err
		}
		dt, err := parseIETFDate(s)
		if err != nil {
			return nil, err
		}
		return xdm.One(xdm.NewDateTime(dt, xdm.TypeDateTime)), nil
	})
}

// ietfScanner walks the input a token at a time.
//
// The grammar is not regular in a way that a single regular expression
// captures comfortably — the year floats between two positions and the
// separators are optional — so it is parsed by hand. The scanner keeps the
// case-folded text, since every keyword in the grammar is case-insensitive.
type ietfScanner struct {
	s   string
	pos int
}

// skipSpace advances over XML whitespace. The grammar writes "S" between most
// fields and allows it to be empty in several of them, so this is called
// rather than required almost everywhere.
func (p *ietfScanner) skipSpace() {
	for p.pos < len(p.s) && isXMLSpace(rune(p.s[p.pos])) {
		p.pos++
	}
}

func (p *ietfScanner) eof() bool { return p.pos >= len(p.s) }

func (p *ietfScanner) peek() byte {
	if p.eof() {
		return 0
	}
	return p.s[p.pos]
}

// acceptByte consumes c if it is next.
func (p *ietfScanner) acceptByte(c byte) bool {
	if p.peek() == c {
		p.pos++
		return true
	}
	return false
}

// word consumes a run of ASCII letters, returning it lowercased.
func (p *ietfScanner) word() string {
	start := p.pos
	for p.pos < len(p.s) && isASCIILetter(p.s[p.pos]) {
		p.pos++
	}
	return strings.ToLower(p.s[start:p.pos])
}

// digits consumes a run of ASCII digits.
func (p *ietfScanner) digits() string {
	start := p.pos
	for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
		p.pos++
	}
	return p.s[start:p.pos]
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// errIETF builds the one error this function raises. Every malformed input is
// FORG0010 regardless of which field went wrong, so the message carries the
// detail and the code stays fixed.
func errIETF(format string, a ...any) error {
	return xdm.Errorf("FORG0010", "fn:parse-ietf-date: "+format, a...)
}

var ietfMonths = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

// ietfDayNames is the set of day names the grammar admits, in both the
// abbreviated and the full spelling.
//
// The value is never used: the spec says the day name is not checked against
// the date, so "Sun, 20 Aug 2014" — a Wednesday — is accepted. Only membership
// matters, to tell a day name apart from a month name at the start of the
// asctime form and to reject "Boy" and "Manchester".
var ietfDayNames = map[string]bool{
	"mon": true, "monday": true,
	"tue": true, "tuesday": true,
	"wed": true, "wednesday": true,
	"thu": true, "thursday": true,
	"fri": true, "friday": true,
	"sat": true, "saturday": true,
	"sun": true, "sunday": true,
}

// ietfZones maps the obsolete timezone abbreviations to an offset in minutes
// east of UTC.
//
// RFC 5322 declares these obsolete precisely because they are ambiguous — CET
// and IST name different offsets in different places — and the F&O grammar
// admits only this fixed list. "CET" is therefore FORG0010, which the suite
// asserts, even though it is a perfectly real timezone.
var ietfZones = map[string]int{
	"ut": 0, "utc": 0, "gmt": 0,
	"est": -5 * 60, "edt": -4 * 60,
	"cst": -6 * 60, "cdt": -5 * 60,
	"mst": -7 * 60, "mdt": -6 * 60,
	"pst": -8 * 60, "pdt": -7 * 60,
}

// ietfDate holds the fields as they are found, before they are assembled.
//
// The year is separate from the rest because it may arrive either before the
// time or at the very end, and "not yet seen" has to be distinguishable from
// any value it could legitimately take.
type ietfDate struct {
	day, month int
	year       int
	haveYear   bool

	hour, minute int
	second       *big.Rat

	tzMinutes int
	haveTZ    bool
	// tzNormalized records whether the offset should be folded into UTC.
	// A zone *name* denotes an instant, so "14:36:01 EST" is the same value
	// as 19:36:01Z and the suite compares it as such. A numeric offset is
	// retained as written, except that the suite's expectations show the
	// two- and four-digit forms normalising to Z while the odd-digit forms
	// ("-5", "-500") keep their offset — see parseIETFTimezone.
	tzNormalized bool
}

// parseIETFDate implements the grammar of F&O 3.1 §9.10.
//
// The two shapes it accepts differ only in where the year sits:
//
//	[dayname] day month year time [zone]      (RFC 5322 / RFC 1123)
//	[dayname] month day time [zone] year      (asctime)
//
// so the parser decides between them on whether a month name or a day number
// comes first, and then reads the remaining fields in the order that shape
// fixes.
func parseIETFDate(input string) (*xdm.DateTime, error) {
	p := &ietfScanner{s: input}
	var d ietfDate

	p.skipSpace()
	if p.eof() {
		return nil, errIETF("the input is empty")
	}

	// A leading word is either a day name — optional, and discarded — or the
	// month, which starts the asctime form. Anything else is malformed:
	// "Manchester, 20 Aug ..." and "Boy 20 Aug ..." are both rejected here.
	asctime := false
	if isASCIILetter(p.peek()) {
		w := p.word()
		switch {
		case ietfDayNames[w]:
			// The comma after a day name is optional, but it must come before
			// the space that follows: "Wed,20 Aug" is rejected because the
			// grammar writes the separator as ("," S) rather than ("," S?).
			hadComma := p.acceptByte(',')
			if hadComma && !p.eof() && !isXMLSpace(rune(p.peek())) {
				return nil, errIETF("a comma after the day name must be followed by a space")
			}
			p.skipSpace()
			// The asctime form may still follow the day name, as in
			// "Wed Aug 20 19:36:01 2014".
			if !p.eof() && isASCIILetter(p.peek()) {
				save := p.pos
				m := p.word()
				if _, ok := ietfMonths[m]; !ok {
					return nil, errIETF("%q is not a month name", m)
				}
				d.month = ietfMonths[m]
				asctime = true
				_ = save
			}
		case ietfMonths[w] != 0:
			d.month = ietfMonths[w]
			asctime = true
		default:
			return nil, errIETF("%q is neither a day name nor a month name", w)
		}
	}

	var err error
	if asctime {
		// "Aug 20 ..." — the day follows the month, separated by a space or a
		// hyphen. "Aug,20" and "Aug20" are both rejected: the grammar allows
		// only S or the hyphen here.
		if err = p.ietfDateSep(); err != nil {
			return nil, err
		}
		if d.day, err = p.ietfDayNumber(); err != nil {
			return nil, err
		}
	} else {
		// "20 Aug 2014 ..." — day, month and year in that order.
		if d.day, err = p.ietfDayNumber(); err != nil {
			return nil, err
		}
		if err = p.ietfDateSep(); err != nil {
			return nil, err
		}
		if !isASCIILetter(p.peek()) {
			return nil, errIETF("expected a month name after the day")
		}
		m := p.word()
		if _, ok := ietfMonths[m]; !ok {
			return nil, errIETF("%q is not a month name", m)
		}
		d.month = ietfMonths[m]
		if err = p.ietfDateSep(); err != nil {
			return nil, err
		}
		if d.year, err = p.ietfYear(); err != nil {
			return nil, err
		}
		d.haveYear = true
	}

	p.skipSpace()
	if err = p.ietfTime(&d); err != nil {
		return nil, err
	}

	// The timezone is optional and may be a name or a numeric offset. It
	// binds tightly enough that no space is required before it, which is why
	// "19:36:01GMT" parses.
	if err = p.ietfTimezone(&d); err != nil {
		return nil, err
	}

	// In the asctime form the year comes last, after the timezone.
	if !d.haveYear {
		p.skipSpace()
		if p.eof() {
			return nil, errIETF("the year is missing")
		}
		if d.year, err = p.ietfYear(); err != nil {
			return nil, err
		}
		d.haveYear = true
	}

	p.skipSpace()
	if !p.eof() {
		// Trailing text is an error rather than something to ignore, which is
		// what rejects "... GMT Manchester".
		return nil, errIETF("unexpected trailing text %q", p.s[p.pos:])
	}
	return d.build()
}

// ietfDateSep consumes the separator between two date fields.
//
// The grammar writes it as (S* "-" S*) | S+ : a hyphen may replace the space
// entirely and may itself be surrounded by spaces, so "20-Aug-2014",
// "20 - Aug - 2014" and "20 Aug 2014" are all the same date. What it will not
// accept is nothing at all, which is what makes "20Aug" and "Aug20" errors.
func (p *ietfScanner) ietfDateSep() error {
	had := false
	for p.pos < len(p.s) && isXMLSpace(rune(p.s[p.pos])) {
		p.pos++
		had = true
	}
	if p.acceptByte('-') {
		had = true
		p.skipSpace()
	}
	if !had {
		return errIETF("a space or hyphen is required between the date fields")
	}
	return nil
}

// ietfDayNumber reads the day of the month: one or two digits, no more.
//
// Three digits is an error rather than a large day that later validation
// would catch, because "020" must fail even though 20 would be a valid day.
func (p *ietfScanner) ietfDayNumber() (int, error) {
	ds := p.digits()
	if len(ds) == 0 || len(ds) > 2 {
		return 0, errIETF("the day of the month must be one or two digits, got %q", ds)
	}
	return atoiIETF(ds), nil
}

// ietfYear reads the year: two digits or four, and nothing else.
//
// A two-digit year is 1900+YY, which is what the suite asserts — "14" is 1914,
// not 2014. That is the RFC 5322 rule for the obsolete two-digit form, and
// while it dates the value oddly it is what the grammar specifies. Three
// digits ("114", "014") is an error rather than a year, which is why the
// length is checked rather than the value.
func (p *ietfScanner) ietfYear() (int, error) {
	ds := p.digits()
	switch len(ds) {
	case 2:
		return 1900 + atoiIETF(ds), nil
	case 4:
		return atoiIETF(ds), nil
	}
	return 0, errIETF("the year must be two or four digits, got %q", ds)
}

// ietfTime reads "H:MM[:SS[.frac]]".
//
// The hour may be one or two digits — "9:36:01" and "4:36:01" both appear in
// the suite — but the minute and second must be exactly two, which is what
// rejects "19:3:01" and "19:36:0.1".
func (p *ietfScanner) ietfTime(d *ietfDate) error {
	hs := p.digits()
	if len(hs) == 0 || len(hs) > 2 {
		return errIETF("the hour must be one or two digits, got %q", hs)
	}
	d.hour = atoiIETF(hs)
	if !p.acceptByte(':') {
		return errIETF("expected \":\" after the hour")
	}
	ms := p.digits()
	if len(ms) != 2 {
		return errIETF("the minute must be exactly two digits, got %q", ms)
	}
	d.minute = atoiIETF(ms)

	d.second = new(big.Rat)
	if !p.acceptByte(':') {
		// Seconds are optional: "19:36 GMT" is a valid time.
		return nil
	}
	ss := p.digits()
	if len(ss) != 2 {
		return errIETF("the second must be exactly two digits, got %q", ss)
	}
	sec := new(big.Rat).SetInt64(int64(atoiIETF(ss)))
	if p.acceptByte('.') {
		fs := p.digits()
		if len(fs) == 0 {
			// "19:36:01." is malformed: the point promises digits.
			return errIETF("expected digits after the decimal point in the seconds")
		}
		frac, ok := new(big.Rat).SetString("0." + fs)
		if !ok {
			return errIETF("%q is not a valid fractional second", fs)
		}
		sec.Add(sec, frac)
	}
	d.second = sec
	return nil
}

// ietfTimezone reads an optional timezone: a name, or a numeric offset
// optionally followed by a parenthesised comment.
func (p *ietfScanner) ietfTimezone(d *ietfDate) error {
	// No space is required before the zone, so the offset is looked for at
	// the current position first: "19:36:01-05:00" and "19:36:01 -05:00" are
	// the same value.
	if c := p.peek(); c == '+' || c == '-' {
		return p.ietfNumericZone(d)
	}
	if isASCIILetter(p.peek()) {
		return p.ietfNamedZone(d)
	}

	save := p.pos
	p.skipSpace()
	switch c := p.peek(); {
	case c == '+' || c == '-':
		// An offset after a space. The asctime year is also introduced by a
		// space and cannot start with a sign, so there is no ambiguity.
		return p.ietfNumericZone(d)
	case isASCIILetter(p.peek()):
		return p.ietfNamedZone(d)
	}
	// Neither: there is no timezone, and the space belongs to whatever
	// follows. Rewinding leaves it for the year to consume.
	p.pos = save
	return nil
}

// ietfNamedZone reads one of the obsolete timezone abbreviations.
//
// A named zone denotes an instant rather than a local time with an offset, so
// the result is normalised to UTC: "14:36:01 EST" and "19:36:01Z" are the same
// xs:dateTime, and the suite compares them with eq.
func (p *ietfScanner) ietfNamedZone(d *ietfDate) error {
	w := p.word()
	off, ok := ietfZones[w]
	if !ok {
		// Includes "CET", which RFC 5322 leaves out of the obsolete list and
		// the F&O grammar therefore does not admit.
		return errIETF("%q is not a recognised timezone", w)
	}
	// A zone name runs up against whatever follows it with no separator of its
	// own, so "GMT2014" would otherwise read as the zone GMT followed by the
	// year. The grammar requires a space there, and the suite asserts that
	// running them together is an error.
	if !p.eof() && !isXMLSpace(rune(p.peek())) {
		return errIETF("a timezone name must be followed by a space")
	}
	d.tzMinutes = off
	d.haveTZ = true
	// The offset is kept as the zone names it rather than folded into UTC.
	// The fn-parse-ietf-date set compares with "eq", which cannot tell
	// 19:36:01Z from 14:36:01-05:00 — they are the same instant — so it does
	// not pin this either way. The specification's own examples do:
	// fo-test-fn-parse-ietf-date-003 asserts deep-eq against
	// "2013-06-06T11:54:45-05:00", and deep-eq compares the timezone.
	// A named zone denotes a local time with a known offset, not an instant
	// stripped of it.
	// A comment is allowed only after a numeric offset, so "19:36(EST)" is an
	// error even though "19:36 -05:00(EST)" is fine.
	return nil
}

// ietfNumericZone reads "+HH[[:]MM]" and any comment that follows it.
//
// The digit counts the grammar allows are 1, 2, 3 and 4, and they do not mean
// what a reader might guess. Two digits are hours; four are hours and minutes;
// but one digit ("-5") and three ("-500") are hours and hours-plus-minutes
// written short, and the suite shows those two *keeping* their offset in the
// result where the even-digit forms normalise to Z. That is not a rule stated
// anywhere in the prose — it falls out of the reference implementation the
// tests were written against — so it is encoded here as observed rather than
// derived.
func (p *ietfScanner) ietfNumericZone(d *ietfDate) error {
	neg := p.peek() == '-'
	p.pos++
	ds := p.digits()

	var hours, minutes int
	// A colon may separate the hours from the minutes, and may be present
	// with nothing after it: "-05:" is the same as "-05".
	switch len(ds) {
	case 1, 2:
		hours = atoiIETF(ds)
		// One digit alone keeps its offset; two digits, or one followed by an
		// explicit ":MM", normalise to UTC. So "-5" stays -05:00 while "-05"
		// and "-5:00" both come out as Z.
		d.tzNormalized = len(ds) == 2
		if p.acceptByte(':') {
			ms := p.digits()
			switch len(ms) {
			case 0:
				// "-05:" — a trailing colon with no minutes.
			case 2:
				minutes = atoiIETF(ms)
				d.tzNormalized = true
			default:
				return errIETF("the timezone minutes must be two digits, got %q", ms)
			}
		}
	case 3:
		hours = atoiIETF(ds[:1])
		minutes = atoiIETF(ds[1:])
	case 4:
		hours = atoiIETF(ds[:2])
		minutes = atoiIETF(ds[2:])
		d.tzNormalized = true
	default:
		return errIETF("the timezone offset must have one to four digits, got %q", ds)
	}

	if minutes > 59 {
		return errIETF("the timezone minutes must be less than 60, got %d", minutes)
	}
	off := hours*60 + minutes
	// The XML Schema range for a timezone is ±14:00, and the suite asserts
	// that -15:00 is rejected rather than clamped.
	if off > 14*60 {
		return errIETF("the timezone offset must be within 14 hours of UTC")
	}
	if neg {
		off = -off
	}
	d.tzMinutes = off
	d.haveTZ = true

	return p.ietfComment()
}

// ietfComment consumes an optional parenthesised timezone name.
//
// The name inside is discarded but still checked: the grammar admits only a
// timezone name there, so "(CET)" and "()" are errors while "(EST)" and
// "(GMT)" are ignored even when they contradict the offset that precedes them.
func (p *ietfScanner) ietfComment() error {
	save := p.pos
	p.skipSpace()
	if !p.acceptByte('(') {
		p.pos = save
		return nil
	}
	p.skipSpace()
	w := p.word()
	if _, ok := ietfZones[w]; !ok {
		return errIETF("%q is not a recognised timezone name in a comment", w)
	}
	p.skipSpace()
	if !p.acceptByte(')') {
		return errIETF("an unclosed timezone comment")
	}
	return nil
}

// atoiIETF converts a run of digits the scanner has already validated.
//
// It cannot fail: the caller has checked both that the string is non-empty and
// that its length is small, so overflow is impossible and there is no error to
// return.
func atoiIETF(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// build turns the collected fields into an xs:dateTime.
//
// The value is assembled as a lexical form and handed to xdm.ParseDateTime
// rather than written into a DateTime directly, so that the calendar checks
// live in one place: 29 February in a non-leap year and a day of 32 or 0 are
// rejected by the same code that rejects them in a cast, and the 24:00
// rollover to the next day is applied by the same code too.
func (d *ietfDate) build() (*xdm.DateTime, error) {
	if d.hour > 24 || d.minute > 59 {
		return nil, errIETF("the time %02d:%02d is out of range", d.hour, d.minute)
	}
	// 24:00 is admitted, and only as exactly midnight — "24:00:01" is not a
	// time. ParseDateTime enforces that too, but its message would name a
	// lexical form the caller never wrote.
	if d.hour == 24 && (d.minute != 0 || d.second.Sign() != 0) {
		return nil, errIETF("24:00:00 is the only time permitted in hour 24")
	}
	if d.second.Cmp(big.NewRat(60, 1)) >= 0 {
		return nil, errIETF("the seconds must be less than 60")
	}

	// An absent timezone is Z. The spec says the result has a timezone even
	// when the input does not name one, which is why this is not simply left
	// off: "Wed, 20 Aug 2014 19:36:01" equals the same instant written with
	// GMT, and the suite compares the two.
	off := 0
	if d.haveTZ {
		off = d.tzMinutes
	}

	// A retained offset keeps the local time as written; a normalised one is
	// shifted so the result reads in UTC. Both denote the same instant, so
	// this only decides which of two equal lexical forms is produced — but
	// the suite asserts the form as well as the value.
	//
	// The shift is applied to the fields before the lexical form is built,
	// rather than to the parsed value afterwards, because carrying a minute
	// past its range needs the day, month and year to carry with it and
	// xdm.DateTime exposes no arithmetic that does so.
	lexTZ := off
	if d.tzNormalized && off != 0 {
		if err := d.shiftMinutes(-off); err != nil {
			return nil, err
		}
		lexTZ = 0
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%04d-%02d-%02dT%02d:%02d:%s",
		d.year, d.month, d.day, d.hour, d.minute, formatIETFSeconds(d.second))
	sb.WriteString(formatTZOffset(lexTZ))

	dt, err := xdm.ParseDateTime(sb.String(), xdm.TypeDateTime)
	if err != nil {
		// Reached for a date that is well-formed but not a real day, such as
		// 29 February 2014 or a day of 32.
		return nil, errIETF("%s is not a valid date", sb.String())
	}
	return dt, nil
}

// shiftMinutes moves the value by delta minutes, carrying into the date.
//
// The date is validated first, because the carry has to know how long the
// month is and asking that of 31 February gives an answer that is quietly
// wrong rather than an error. An invalid date is reported here with the same
// code the later parse would have raised.
func (d *ietfDate) shiftMinutes(delta int) error {
	if d.month < 1 || d.month > 12 || d.day < 1 || d.day > daysInMonthIETF(d.year, d.month) {
		return errIETF("%04d-%02d-%02d is not a valid date", d.year, d.month, d.day)
	}
	// Work in minutes since midnight so that a shift of more than a day, or
	// one that lands exactly on a boundary, needs no special case.
	total := d.hour*60 + d.minute + delta
	dayShift := total / (24 * 60)
	total %= 24 * 60
	if total < 0 {
		total += 24 * 60
		dayShift--
	}
	d.hour, d.minute = total/60, total%60

	for dayShift > 0 {
		d.day++
		if d.day > daysInMonthIETF(d.year, d.month) {
			d.day = 1
			d.month++
			if d.month > 12 {
				d.month, d.year = 1, d.year+1
			}
		}
		dayShift--
	}
	for dayShift < 0 {
		d.day--
		if d.day < 1 {
			d.month--
			if d.month < 1 {
				d.month, d.year = 12, d.year-1
			}
			d.day = daysInMonthIETF(d.year, d.month)
		}
		dayShift++
	}
	return nil
}

// daysInMonthIETF is the length of a month in the proleptic Gregorian
// calendar. xdm has the same function, but unexported; duplicating four lines
// is preferable to widening that package's surface for one caller.
func daysInMonthIETF(y, m int) int {
	switch m {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
			return 29
		}
		return 28
	}
	return 0
}

// formatTZOffset writes a timezone offset in the lexical form XML Schema uses.
func formatTZOffset(minutes int) string {
	if minutes == 0 {
		return "Z"
	}
	sign := "+"
	if minutes < 0 {
		sign = "-"
		minutes = -minutes
	}
	return fmt.Sprintf("%s%02d:%02d", sign, minutes/60, minutes%60)
}

// formatIETFSeconds writes the seconds with a fractional part only when there
// is one, since "01.0" and "01" are the same value but not the same lexical
// form and the canonical one is shorter.
func formatIETFSeconds(sec *big.Rat) string {
	if sec.IsInt() {
		return fmt.Sprintf("%02d", sec.Num().Int64())
	}
	// Nine digits is past the precision any input in the grammar can carry
	// and is trimmed back to what was actually written.
	s := sec.FloatString(9)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if len(s) > 0 && s[1] == '.' {
		s = "0" + s
	}
	return s
}

// --- fn:random-number-generator ---------------------------------------------

// registerRandomNumberGenerator adds
// fn:random-number-generator([$seed as xs:anyAtomicType?]) as
// map(xs:string, item()).
//
// The map has three entries: "number", an xs:double in [0,1); "next", a
// zero-arity function returning the next such map; and "permute", a function
// that shuffles a sequence. The generator is a pure value rather than a
// stateful object — asking the same map for "number" twice gives the same
// number, and a fresh one comes only from calling "next" — which is what lets
// the suite call ?permute twice on one map and get the same permutation.
//
// The result is required to be deterministic: the spec says two calls with the
// same seed in the same execution scope must produce the same generator, and
// the suite checks that random-number-generator() with no seed matches
// random-number-generator(()) exactly. An unseeded call therefore has a fixed
// seed rather than a time- or entropy-derived one.
func registerRandomNumberGenerator(l *Library) {
	l.registerFnSince(XPath31, "random-number-generator", []int{0, 1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		seed, err := randomSeedFrom(args)
		if err != nil {
			return nil, err
		}
		return randomGeneratorMap(seed)
	})
}

// randomSeedFrom derives the 64-bit seed from the optional argument.
//
// The seed is xs:anyAtomicType?, so anything atomic may arrive: the suite
// passes integers, a string, an xs:double NaN and a dateTime. Hashing the
// value's string form covers all of them uniformly, and an absent or empty
// seed uses the same fixed constant so that the no-argument and empty-sequence
// forms agree, which the suite asserts.
func randomSeedFrom(args []xdm.Sequence) (uint64, error) {
	if len(args) == 0 || len(args[0]) == 0 {
		return 0x9E3779B97F4A7C15, nil
	}
	atoms, err := xdm.AtomizeChecked(args[0])
	if err != nil {
		return 0, err
	}
	if len(atoms) == 0 {
		return 0x9E3779B97F4A7C15, nil
	}
	it, err := atoms.Single()
	if err != nil {
		return 0, err
	}
	// The lexical form rather than the value: it is defined for every atomic
	// type, where a numeric conversion would not be, and two seeds that print
	// the same are the same seed.
	return hashSeedString(it.(*xdm.Atomic).String()), nil
}

// hashSeedString is FNV-1a, chosen because it is short, has no dependencies
// and spreads similar strings — "0" and "1", which the suite requires to give
// different permutations — into distant seeds.
func hashSeedString(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// splitMix64 advances the generator state and returns a well-mixed value.
//
// SplitMix64 is used rather than Go's math/rand because the sequence must be
// fixed by this code: math/rand's algorithm is not part of its compatibility
// promise, and a stylesheet whose output changed with the Go version would be
// a genuine bug. It also has no allocation and no lock, so a generator map is
// cheap to build.
func splitMix64(state uint64) (uint64, uint64) {
	state += 0x9E3779B97F4A7C15
	z := state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return state, z ^ (z >> 31)
}

// randomDouble converts a mixed 64-bit value to an xs:double in [0,1).
//
// Only the top 53 bits are used, which is exactly the precision of a double's
// significand: taking all 64 and dividing would round to 1.0 for the largest
// values, and the suite asserts the result is strictly less than 1.
func randomDouble(v uint64) float64 {
	return float64(v>>11) / float64(uint64(1)<<53)
}

// randomGeneratorMap builds the generator map for a given state.
func randomGeneratorMap(state uint64) (xdm.Sequence, error) {
	next, mixed := splitMix64(state)

	m := xdm.NewMap()
	m, err := m.Put(xdm.NewString("number"), xdm.One(xdm.NewDouble(randomDouble(mixed))))
	if err != nil {
		return nil, err
	}

	// "next" is a zero-arity function rather than the map itself, so that a
	// generator is not an infinite structure: the successor is built only when
	// it is asked for.
	m, err = m.Put(xdm.NewString("next"), xdm.One(&xdm.FunctionItem{
		Arity:     0,
		Signature: []string{"map(xs:string, item())"},
		Invoke: func(_ any, _ []xdm.Sequence) (xdm.Sequence, error) {
			return randomGeneratorMap(next)
		},
	}))
	if err != nil {
		return nil, err
	}

	m, err = m.Put(xdm.NewString("permute"), xdm.One(&xdm.FunctionItem{
		Arity:     1,
		Signature: []string{"item()*", "item()*"},
		Invoke: func(_ any, args []xdm.Sequence) (xdm.Sequence, error) {
			if len(args) == 0 {
				return xdm.Empty(), nil
			}
			return permuteWith(mixed, args[0]), nil
		},
	}))
	if err != nil {
		return nil, err
	}
	return xdm.One(m), nil
}

// permuteWith returns a shuffled copy of seq.
//
// A Fisher-Yates shuffle driven by the generator's own state, so the
// permutation is a function of the generator rather than of a fresh source of
// randomness: calling ?permute twice on one map gives the same answer, which
// the suite checks, while ?next()?permute gives a different one.
func permuteWith(state uint64, seq xdm.Sequence) xdm.Sequence {
	// A copy, because the argument's backing array belongs to the caller and
	// shuffling in place would reorder the sequence they still hold.
	out := make(xdm.Sequence, len(seq))
	copy(out, seq)
	for i := len(out) - 1; i > 0; i-- {
		var v uint64
		state, v = splitMix64(state)
		j := int(v % uint64(i+1))
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// --- fn:deep-equal over maps and arrays -------------------------------------

// deepEqualMapArray compares two items when either is a map or an array,
// reporting whether it handled the pair at all.
//
// XPath 3.1 extends fn:deep-equal to both kinds. Two arrays are equal when
// they have the same number of members and corresponding members are
// deep-equal as sequences; two maps when they have the same number of entries
// and every key of one is present in the other with a deep-equal value. Map
// entries are compared by key rather than pairwise in order, because a map has
// no order — map{1:"a",2:"b"} and map{2:"b",1:"a"} are the same map.
//
// A map or an array compared against anything else is simply unequal rather
// than an error, which is the same rule the atomic branch follows for values
// of incomparable types.
func deepEqualMapArray(ctx *Context, x, y xdm.Item) (eq, handled bool, err error) {
	xm, xIsMap := x.(*xdm.MapItem)
	ym, yIsMap := y.(*xdm.MapItem)
	xa, xIsArr := x.(*xdm.ArrayItem)
	ya, yIsArr := y.(*xdm.ArrayItem)

	if !xIsMap && !yIsMap && !xIsArr && !yIsArr {
		return false, false, nil
	}
	switch {
	case xIsMap && yIsMap:
		e, err := deepEqualMaps(ctx, xm, ym)
		return e, true, err
	case xIsArr && yIsArr:
		e, err := deepEqualArrays(ctx, xa, ya)
		return e, true, err
	}
	// One of the two is a map or an array and the other is not, so they
	// cannot be equal; handled is true so the caller does not fall through to
	// the identity comparison.
	return false, true, nil
}

func deepEqualArrays(ctx *Context, a, b *xdm.ArrayItem) (bool, error) {
	if a.Len() != b.Len() {
		return false, nil
	}
	am, bm := a.Members(), b.Members()
	for i := range am {
		// Members are sequences, so this is the sequence comparison rather
		// than the item one: an array of one two-item member is not equal to
		// an array of two one-item members.
		eq, err := deepEqual(ctx, am[i], bm[i])
		if err != nil || !eq {
			return false, err
		}
	}
	return true, nil
}

func deepEqualMaps(ctx *Context, a, b *xdm.MapItem) (bool, error) {
	if a.Len() != b.Len() {
		return false, nil
	}
	// Equal sizes plus every key of a present in b with an equal value is
	// enough: b can have no key outside a without exceeding a's size.
	equal := true
	err := a.Entries(func(key *xdm.Atomic, value xdm.Sequence) error {
		if !equal {
			return nil
		}
		other, ok, err := b.Get(key)
		if err != nil {
			// The key came out of a map, so it is a valid key; an error here
			// would mean the two maps disagree about what a key is, which
			// makes them unequal rather than erroneous.
			equal = false
			return nil
		}
		if !ok {
			equal = false
			return nil
		}
		eq, err := deepEqual(ctx, value, other)
		if err != nil {
			return err
		}
		equal = eq
		return nil
	})
	if err != nil {
		return false, err
	}
	return equal, nil
}

// --- xs:numeric -------------------------------------------------------------

// registerNumericConstructor adds the xs:numeric() constructor.
//
// It is not registered alongside the other atomic-type constructors because it
// is not one: xs:numeric is a union, and its constructor is defined as a cast
// to xs:double rather than to a type of its own. So xs:numeric('12') is the
// double 12, and the suite asserts it is an instance of xs:double — where
// "17 cast as xs:numeric" keeps its xs:integer type, because the *cast* is the
// identity on a value that is already numeric while the constructor is not.
func registerNumericConstructor(l *Library) {
	l.Add(Function{
		Name:  xdm.QName{URI: xdm.NSXS, Local: "numeric"},
		Arity: 1,
		Since: XPath31,
		Call: func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
			// Like every other constructor, an empty argument gives the empty
			// sequence rather than raising.
			if len(args) == 0 || len(args[0]) == 0 {
				return xdm.Empty(), nil
			}
			atoms, err := xdm.AtomizeChecked(args[0])
			if err != nil {
				return nil, err
			}
			if len(atoms) == 0 {
				return xdm.Empty(), nil
			}
			it, err := atoms.Single()
			if err != nil {
				return nil, err
			}
			out, err := CastAtomic(it.(*xdm.Atomic), xdm.TypeDouble)
			if err != nil {
				return nil, err
			}
			return xdm.One(out), nil
		},
	})
}
