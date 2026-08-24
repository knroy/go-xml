package xpath

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"github.com/knroy/go-xml/xdm"
)

// registerMiscFuncs adds the remaining F&O functions.
func registerMiscFuncs(l *Library) {
	l.registerFn("deep-equal", []int{2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The collation applies to every string comparison the traversal
		// makes. It was validated nowhere and applied nowhere, so
		// deep-equal(("a","A"), ("A","a"), <case-insensitive>) was false.
		coll, err := collationArgCtx(ctx, "deep-equal", args, 2)
		if err != nil {
			return nil, err
		}
		eq, err := deepEqual(withCollation(ctx, coll), args[0], args[1])
		if err != nil {
			return nil, err
		}
		return boolSeq(eq), nil
	})

	l.registerFn("unordered", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The function permits an implementation to return items in whatever
		// order is cheapest. Returning them unchanged is conformant and keeps
		// results reproducible, which matters more here than the notional
		// optimisation.
		return args[0], nil
	})

	l.registerFn("id", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		return lookupByID(ctx, args, true)
	})
	l.registerFn("idref", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		return lookupByID(ctx, args, false)
	})

	l.registerFn("dateTime", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		da := xdm.Atomize(args[0])
		ta := xdm.Atomize(args[1])
		if len(da) == 0 || len(ta) == 0 {
			return xdm.Empty(), nil
		}
		d := da[0].(*xdm.Atomic).DateTimeVal()
		t := ta[0].(*xdm.Atomic).DateTimeVal()
		if d == nil || t == nil {
			return nil, xdm.ErrType("dateTime(): expected an xs:date and an xs:time")
		}
		// The two operands must not disagree about the timezone; if only one
		// carries it, it is adopted.
		if d.HasTZ && t.HasTZ && d.TZOffset != t.TZOffset {
			return nil, fmt.Errorf("FORG0008: date and time have different timezones")
		}
		out := xdm.DateTime{
			Year: d.Year, Month: d.Month, Day: d.Day,
			Hour: t.Hour, Minute: t.Minute, Second: t.Second,
		}
		switch {
		case d.HasTZ:
			out.HasTZ, out.TZOffset = true, d.TZOffset
		case t.HasTZ:
			out.HasTZ, out.TZOffset = true, t.TZOffset
		}
		return xdm.One(xdm.NewDateTime(&out, xdm.TypeDateTime)), nil
	})

	l.registerFn("collection", []int{0, 1}, fnCollection)

}

// RegisterXSLTFuncs adds the functions that XSLT 2.0 defines but XPath 2.0 does
// not.
//
// fn:unparsed-text, fn:format-date and friends are in the XSLT specification,
// not the XPath one — a bare XPath 2.0 processor is required to report
// XPST0017 for them. This engine is an XSLT engine, so they must exist when a
// stylesheet is running, and must not when a plain XPath expression is being
// evaluated against the XPath library alone. Keeping them out of Builtins and
// adding them here is what makes both true.
func RegisterXSLTFuncs(l *Library) {
	l.registerFn("unparsed-text", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, enc, err := unparsedTextArgs(args)
		if err != nil {
			return nil, err
		}
		// No resolver means the function is off, which is the default. The
		// message names the reason rather than the URI: a stylesheet that
		// gets this back has not been granted file reads at all, and saying
		// "cannot retrieve x" would suggest the file was the problem.
		if ctx.Texts == nil {
			return nil, fmt.Errorf(
				"FOUT1170: unparsed-text() is disabled (it reads arbitrary files)")
		}
		text, err := ctx.Texts.ResolveText(s, ctx.StaticBaseURI, enc)
		if err != nil {
			return nil, fmt.Errorf("FOUT1170: cannot retrieve %q: %w", s, err)
		}
		return xdm.One(xdm.NewString(text)), nil
	})
	l.registerFn("unparsed-text-available", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// Defined as "true if a call on unparsed-text with the same
		// arguments would succeed", so it is answered by attempting the read
		// rather than by inspecting the URI. With no resolver it is false,
		// which is what the refusal tests expect.
		s, enc, err := unparsedTextArgs(args)
		if err != nil || ctx.Texts == nil {
			return boolSeq(false), nil
		}
		if _, err := ctx.Texts.ResolveText(s, ctx.StaticBaseURI, enc); err != nil {
			return boolSeq(false), nil
		}
		return boolSeq(true), nil
	})

	// fn:document is XSLT's own document loader, and differs from fn:doc in
	// ways a stylesheet depends on: a sequence of URIs, a base-supplying
	// second argument, and deduplication by identity. See fn_document.go.
	l.registerFn("document", []int{1, 2}, fnDocument)

	registerFormatDateTime(l)
}

// unparsedTextArgs pulls the href and the optional encoding out of a call on
// fn:unparsed-text or fn:unparsed-text-available.
func unparsedTextArgs(args []xdm.Sequence) (href, encoding string, err error) {
	href, err = argStringRequired(args, 0)
	if err != nil {
		return "", "", err
	}
	if len(args) > 1 && len(args[1]) > 0 {
		encoding, err = argStringRequired(args, 1)
		if err != nil {
			return "", "", err
		}
	}
	return href, encoding, nil
}

// lookupByID implements fn:id and fn:idref.
//
// Without a DTD or schema this engine cannot know which attributes are of type
// ID, so it uses the conventional xml:id and an unprefixed "id" attribute.
// That is a documented approximation rather than a silent one: a stylesheet
// relying on DTD-declared ID attributes gets nothing back, which shows up as
// an empty result rather than a wrong one.
func lookupByID(ctx *Context, args []xdm.Sequence, wantID bool) (xdm.Sequence, error) {
	var root *xdm.Node
	if len(args) > 1 {
		n, err := singleNodeArg(args, 1)
		if err != nil {
			return nil, err
		}
		root = n.Root()
	} else {
		n, err := contextNodeArg(ctx)
		if err != nil {
			return nil, err
		}
		root = n.Root()
	}

	// fn:id splits each argument on whitespace: its argument is defined as a
	// sequence of IDREFS values, and an IDREFS value is a whitespace-separated
	// list. fn:idref is not — its argument is a sequence of xs:string, matched
	// whole. So idref('a c') looks for the single name "a c", which is not a
	// valid xs:IDREF and therefore matches nothing, where id('a c') looks for
	// "a" and "c". Splitting both made idref find the union of the tokens.
	want := map[string]bool{}
	for _, it := range xdm.Atomize(args[0]) {
		v := it.(*xdm.Atomic).String()
		if !wantID {
			want[v] = true
			continue
		}
		for _, f := range strings.Fields(v) {
			want[f] = true
		}
	}
	if len(want) == 0 {
		return xdm.Empty(), nil
	}

	var out xdm.Sequence
	var walk func(*xdm.Node)
	walk = func(n *xdm.Node) {
		if n.Kind == xdm.KindElement {
			// An element whose own type is derived from xs:ID (or xs:IDREF /
			// xs:IDREFS) carries the identity in its content, not in an
			// attribute. Section 15.5.2 of the data model treats the two the
			// same way, and a schema is free to declare either: match-212's
			// <id-elem-only> is declared type="xs:ID" and id('unique') has to
			// find it. Only attributes were being looked at, so a
			// schema-validated document indexed by element content matched
			// nothing.
			// n.IsID as well as the annotation: XSLT's
			// input-type-annotations="strip" clears TypeAnnotation but is
			// required to LEAVE is-id/is-idrefs untouched, and fn:id and
			// fn:idref are defined over those properties rather than over
			// the annotation. Testing only the annotation made both
			// functions find nothing in a stripped document.
			if wantID && (n.IsID || isIDAnnotation(n.TypeAnnotation)) {
				if want[strings.TrimSpace(n.StringValue())] {
					out = append(out, n)
				}
			}
			if !wantID && (n.IsIDREFS || isIDREFAnnotation(n.TypeAnnotation)) {
				for _, v := range strings.Fields(n.StringValue()) {
					if want[v] {
						out = append(out, n)
						break
					}
				}
			}
			for _, a := range n.Attrs {
				// A validated document says which attributes are of type
				// xs:ID, and that is what the specification asks for. The
				// name-based test below is the fallback for a document that
				// was never validated, not a substitute: an attribute
				// annotated ID counts however it is spelled, and one merely
				// called "id" in a validated document does not.
				isIDAttr := a.IsID || isIDAnnotation(a.TypeAnnotation) ||
					(a.Name.URI == xdm.NSXML && a.Name.Local == "id") ||
					(a.Name.URI == "" && a.Name.Local == "id")
				isRefAttr := a.IsIDREFS || isIDREFAnnotation(a.TypeAnnotation) ||
					(a.Name.URI == "" &&
						(a.Name.Local == "idref" || a.Name.Local == "idrefs"))

				// The value of an ID attribute is of a type derived from
				// xs:NCName, so its whitespace is collapsed before it is
				// compared: key241.xml writes xml:id="id3 " and the
				// stylesheet asks for id(' id3'). The search terms were
				// already split on whitespace above; this is the other half.
				if wantID && isIDAttr && want[strings.TrimSpace(a.Value)] {
					out = append(out, n)
				}
				if !wantID && isRefAttr {
					for _, v := range strings.Fields(a.Value) {
						if want[v] {
							// fn:idref returns the nodes that *hold* the
							// reference, which for an IDREF-typed attribute
							// is the attribute itself and not the element
							// carrying it. The suite pins this:
							// "idref('a1')/name()" is expected to yield the
							// attribute names "ref refs", where returning the
							// elements yielded their element names instead.
							out = append(out, a)
							break
						}
					}
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return xdm.SortDocumentOrder(out), nil
}

// deepEqual implements fn:deep-equal.
//
// Two sequences are deep-equal when they have the same length and each pair of
// items is deep-equal: atomic values by value comparison, nodes by kind, name,
// and recursively by children and attributes. Comments and processing
// instructions are ignored when comparing element content, which is why this
// cannot simply compare serialised forms.
func deepEqual(ctx *Context, a, b xdm.Sequence) (bool, error) {
	if len(a) != len(b) {
		return false, nil
	}
	for i := range a {
		eq, err := deepEqualItem(ctx, a[i], b[i])
		if err != nil || !eq {
			return false, err
		}
	}
	return true, nil
}

func deepEqualItem(ctx *Context, x, y xdm.Item) (bool, error) {
	// An Opaque is engine-internal state, not a value the data model defines
	// equality over. A stylesheet naming the internal namespace can put one
	// here, so it is compared by identity rather than asserted to be atomic.
	_, xOpaque := x.(*xdm.Opaque)
	_, yOpaque := y.(*xdm.Opaque)
	if xOpaque || yOpaque {
		return xOpaque && yOpaque && x == y, nil
	}

	xn, xIsNode := x.(*xdm.Node)
	yn, yIsNode := y.(*xdm.Node)
	if xIsNode != yIsNode {
		return false, nil
	}
	if !xIsNode {
		xa, ya := x.(*xdm.Atomic), y.(*xdm.Atomic)
		// NaN is deep-equal to NaN here, unlike under "eq". The spec makes
		// this exception so that two identical sequences compare equal.
		if xa.IsNaN() && ya.IsNaN() {
			return true, nil
		}
		// A collation, when one is in force, decides string equality. Only
		// the string types use it; everything else compares by value.
		if coll := ctx.collation; coll != nil &&
			isStringLike(xa.Type) && isStringLike(ya.Type) {
			return coll.Compare(xa.Str(), ya.Str()) == 0, nil
		}
		eq, err := compareValues(ctx, xa, ya, "eq", false)
		if err != nil {
			// Values of incomparable types are simply unequal, not an error:
			// deep-equal is a predicate over arbitrary sequences.
			return false, nil
		}
		return eq, nil
	}
	return deepEqualNode(ctx, xn, yn)
}

func deepEqualNode(ctx *Context, a, b *xdm.Node) (bool, error) {
	if a.Kind != b.Kind {
		return false, nil
	}
	switch a.Kind {
	case xdm.KindDocument:
		return deepEqualContent(ctx, a, b)

	case xdm.KindElement:
		if a.Name.URI != b.Name.URI || a.Name.Local != b.Name.Local {
			return false, nil
		}
		if len(a.Attrs) != len(b.Attrs) {
			return false, nil
		}
		// Attribute order is not significant, so each is matched by name.
		for _, aa := range a.Attrs {
			ba := b.Attr(aa.Name.URI, aa.Name.Local)
			if ba == nil || !deepEqualText(ctx, aa.Value, ba.Value) {
				return false, nil
			}
		}
		return deepEqualContent(ctx, a, b)

	case xdm.KindAttribute, xdm.KindNamespace:
		return a.Name.URI == b.Name.URI && a.Name.Local == b.Name.Local &&
			deepEqualText(ctx, a.Value, b.Value), nil

	case xdm.KindPI:
		// A processing instruction's content is not text in the sense the
		// collation governs, so it keeps codepoint comparison.
		return a.Name.Local == b.Name.Local && a.Value == b.Value, nil

	case xdm.KindText:
		return deepEqualText(ctx, a.Value, b.Value), nil

	case xdm.KindComment:
		return a.Value == b.Value, nil
	}
	return false, nil
}

// deepEqualText compares two string values under the collation in force.
//
// fn:deep-equal compares the string values of text and attribute nodes "using
// the collation" — so under a case-blind collation two elements whose text
// differs only in case are deep-equal. Comparing with == ignored the
// collation argument for exactly the nodes the function is usually asked
// about.
func deepEqualText(ctx *Context, a, b string) bool {
	if ctx != nil && ctx.collation != nil {
		return ctx.collation.Compare(a, b) == 0
	}
	return a == b
}

// deepEqualContent compares children, ignoring comments and PIs and merging
// adjacent text, which is what the spec's "children, excluding comments and
// processing instructions" rule amounts to.
func deepEqualContent(ctx *Context, a, b *xdm.Node) (bool, error) {
	ac, bc := significantChildren(a), significantChildren(b)
	if len(ac) != len(bc) {
		return false, nil
	}
	for i := range ac {
		eq, err := deepEqualNode(ctx, ac[i], bc[i])
		if err != nil || !eq {
			return false, err
		}
	}
	return true, nil
}

func significantChildren(n *xdm.Node) []*xdm.Node {
	var out []*xdm.Node
	for _, c := range n.Children {
		switch c.Kind {
		case xdm.KindComment, xdm.KindPI:
			continue
		case xdm.KindText:
			// Merge with a preceding text node so that a tree built by a
			// transform compares equal to a parsed one.
			if k := len(out); k > 0 && out[k-1].Kind == xdm.KindText {
				merged := *out[k-1]
				merged.Value += c.Value
				out[k-1] = &merged
				continue
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

// registerFormatDateTime adds fn:format-dateTime, fn:format-date and
// fn:format-time.
//
// The picture language is a sequence of [component]modifier markers with
// literal text between them. Only the components and modifiers that appear in
// practice are implemented; an unrecognised component is reported rather than
// emitted literally, so a stylesheet does not silently produce a date with a
// marker still in it.
func registerFormatDateTime(l *Library) {
	format := func(name string) {
		l.registerFn(name, []int{2, 3, 4, 5}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
			atoms := xdm.Atomize(args[0])
			if len(atoms) == 0 {
				return xdm.Empty(), nil
			}
			a, ok := atoms[0].(*xdm.Atomic)
			if !ok || a.DateTimeVal() == nil {
				return nil, xdm.ErrType("%s: expected a date/time value", name)
			}
			pic, err := argString(args, 1)
			if err != nil {
				return nil, err
			}
			out, err := formatDateTimePicture(a.DateTimeVal(), pic, name)
			if err != nil {
				return nil, err
			}
			// Section 16.5.2: when the requested language is not supported
			// the result must say which language was used instead. English
			// is the only one implemented, so any other request that would
			// have produced language-dependent text is flagged.
			if lang, ok := requestedLanguage(args); ok && !supportedLanguage(lang) &&
				pictureUsesNames(pic) {
				out = "[Language: en]" + out
			}
			return strSeq(out), nil
		})
	}
	format("format-dateTime")
	format("format-date")
	format("format-time")
}

// formatDateTimePicture renders a date/time through a picture string.
//
// fn names the calling function, which decides which components the value can
// supply: fn:format-date has no clock and fn:format-time has no calendar, and
// section 16.5.1 makes a marker naming an absent component XTDE1350 rather
// than something to render as blank.
func formatDateTimePicture(dt *xdm.DateTime, pic string, fn string) (string, error) {
	var sb strings.Builder
	runes := []rune(pic)

	for i := 0; i < len(runes); i++ {
		if runes[i] != '[' {
			if runes[i] == ']' {
				// "]]" is an escaped literal bracket.
				if i+1 < len(runes) && runes[i+1] == ']' {
					sb.WriteRune(']')
					i++
					continue
				}
				return "", fmt.Errorf("FOFD1340: unmatched ']' in picture %q", pic)
			}
			sb.WriteRune(runes[i])
			continue
		}
		// "[[" is an escaped literal bracket.
		if i+1 < len(runes) && runes[i+1] == '[' {
			sb.WriteRune('[')
			i++
			continue
		}
		end := i + 1
		for end < len(runes) && runes[end] != ']' {
			end++
		}
		if end >= len(runes) {
			return "", fmt.Errorf("FOFD1340: unclosed '[' in picture %q", pic)
		}
		marker := strings.TrimSpace(string(runes[i+1 : end]))
		i = end

		text, err := formatComponent(dt, marker, fn)
		if err != nil {
			return "", err
		}
		sb.WriteString(text)
	}
	return sb.String(), nil
}

// dateComponents are the picture components that need a calendar date, and
// timeComponents those that need a clock. A marker naming a component the
// value cannot supply is XTDE1350; a timezone marker is exempt because
// section 16.5.1 says a value with no timezone simply drops it.
const dateComponents = "YMDdFWwEC"
const timeComponents = "HhPmsf"

// formatComponent renders one [component]presentation marker.
func formatComponent(dt *xdm.DateTime, marker string, fn string) (string, error) {
	if marker == "" {
		return "", fmt.Errorf("FOFD1340: empty component in picture")
	}
	comp := marker[0]
	// Whitespace anywhere inside a variable marker is ignored, so it is
	// stripped wholesale rather than only at the ends: "[M 01]" and "[M01]"
	// are the same marker.
	rest := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, marker[1:])

	switch fn {
	case "format-date":
		if strings.IndexByte(timeComponents, comp) >= 0 {
			return "", fmt.Errorf("FOFD1350: component %q is not available in a date", string(comp))
		}
	case "format-time":
		if strings.IndexByte(dateComponents, comp) >= 0 {
			return "", fmt.Errorf("FOFD1350: component %q is not available in a time", string(comp))
		}
	}

	// A width modifier follows a comma, and everything before it is the one
	// or two presentation modifiers.
	pres, width := rest, ""
	if i := strings.IndexByte(rest, ','); i >= 0 {
		width = rest[i+1:]
		pres = rest[:i]
	}
	// The second presentation modifier, when present, is the last character
	// and is either "t" (traditional numbering) or "o" (ordinal form).
	ordinal, traditional := false, false
	if n := len(pres); n > 1 || (n == 1 && (pres == "o" || pres == "t")) {
		switch pres[len(pres)-1] {
		case 'o':
			ordinal = true
			pres = pres[:len(pres)-1]
		case 't':
			// Traditional numbering coincides with the default for the
			// languages implemented here, so for most components the
			// modifier only needs stripping; the timezone is the exception.
			traditional = true
			pres = pres[:len(pres)-1]
		}
	}
	if pres == "" {
		pres = defaultPresentation(comp)
	}
	if err := checkWidthModifier(width); err != nil {
		return "", err
	}

	num := func(n int64) (string, error) {
		return padNumber(n, pres, width, ordinal), nil
	}

	switch comp {
	case 'Y':
		// A maximum width on the year means "keep the low-order digits":
		// with max-width 2, 2003 is "03".
		y := int64(dt.Year)
		if y < 0 {
			y = -y
		}
		if max := effectiveMaxWidth(pres, width); max > 0 && max < 18 && isDigitPattern(pres) {
			mod := int64(1)
			for i := 0; i < max; i++ {
				mod *= 10
			}
			y %= mod
		}
		return padNumber(y, pres, width, ordinal), nil
	case 'M':
		if isNamePresentation(pres) {
			return applyNameCase(monthNameOf(dt), pres, width), nil
		}
		return num(int64(dt.Month))
	case 'D':
		return num(int64(dt.Day))
	case 'd':
		return num(int64(dayOfYear(dt)))
	case 'W':
		return num(int64(weekOfYear(dt)))
	case 'w':
		return num(int64(weekOfMonth(dt)))
	case 'H':
		return num(int64(dt.Hour))
	case 'h':
		// 12-hour clock: midnight and noon are both "12".
		h := dt.Hour % 12
		if h == 0 {
			h = 12
		}
		return num(int64(h))
	case 'm':
		return num(int64(dt.Minute))
	case 's':
		whole := new(big.Int).Quo(dt.Second.Num(), dt.Second.Denom()).Int64()
		return num(whole)
	case 'f':
		return fractionalSeconds(dt, pres, width), nil
	case 'P':
		half := "am"
		if dt.Hour >= 12 {
			half = "pm"
		}
		if !isNamePresentation(pres) {
			pres = "n"
		}
		return applyNameCase(half, pres, width), nil
	case 'F':
		if !isNamePresentation(pres) {
			// A numeric presentation of the day of the week is ISO's
			// numbering, Monday = 1.
			d := int64(weekdayIndex(dt))
			if d == 0 {
				d = 7
			}
			return padNumber(d, pres, width, ordinal), nil
		}
		return applyNameCase(weekdayName(dt, pres), pres, width), nil
	case 'E':
		// The only era this implementation knows is the Common Era, whose
		// baseline splits at year zero.
		era := "AD"
		if dt.Year <= 0 {
			era = "BC"
		}
		if !isNamePresentation(pres) {
			pres = "n"
		}
		return applyNameCase(era, pres, width), nil
	case 'C':
		// Only the ISO calendar is implemented, so it is the only name that
		// can be reported.
		if !isNamePresentation(pres) {
			pres = "n"
		}
		return applyNameCase("ISO", pres, width), nil
	case 'Z', 'z':
		return formatTZMarker(dt, comp, pres, width, traditional), nil
	}
	return "", fmt.Errorf("FOFD1340: unsupported picture component %q", string(comp))
}

// defaultPresentation is the presentation modifier a component takes when the
// picture supplies none, from the table in section 16.5.1.
func defaultPresentation(comp byte) string {
	switch comp {
	case 'F', 'P', 'C', 'E':
		return "n"
	case 'm', 's':
		return "01"
	}
	return "1"
}

// checkWidthModifier rejects a width modifier whose syntax is wrong, which is
// XTDE1340. Both bounds must be "*" or an integer greater than zero.
func checkWidthModifier(width string) error {
	if width == "" {
		return nil
	}
	parts := strings.SplitN(width, "-", 2)
	for _, part := range parts {
		if part == "*" {
			continue
		}
		if part == "" {
			return fmt.Errorf("FOFD1340: malformed width modifier %q", width)
		}
		n := 0
		for _, c := range part {
			if c < '0' || c > '9' {
				return fmt.Errorf("FOFD1340: malformed width modifier %q", width)
			}
			n = n*10 + int(c-'0')
		}
		if n == 0 {
			return fmt.Errorf("FOFD1340: width modifier %q must be greater than zero", width)
		}
	}
	return nil
}

// minWidth reads the minimum from a width modifier, or 0 when it sets none.
func minWidth(width string) int {
	if width == "" {
		return 0
	}
	part := width
	if i := strings.IndexByte(width, '-'); i >= 0 {
		part = width[:i]
	}
	if part == "" || part == "*" {
		return 0
	}
	n := 0
	for _, c := range part {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// padNumber renders n per a presentation modifier and width modifier.
//
// A decimal-digit-pattern such as "01" or "0001" sets the minimum width to its
// own length — "[M01]" means a two-digit month, so January is "01" and not
// "1". An explicit width modifier takes precedence over the pattern.
func padNumber(n int64, pres, width string, ordinal bool) string {
	switch pres {
	case "i":
		return strings.ToLower(romanNum(n))
	case "I":
		return romanNum(n)
	case "w", "W", "Ww":
		return spellDateNumber(n, pres, ordinal)
	case "a", "A":
		return alphaNum(n, pres == "A")
	}

	var s string
	zero, digits := digitFamilyOf(pres)
	if digits {
		s = fmt.Sprintf("%0*d", len([]rune(pres)), n)
	} else {
		s = strconv.FormatInt(n, 10)
	}
	if ordinal {
		s += ordinalSuffixFor(n)
	}
	if min := minWidth(width); min > len([]rune(s)) {
		// Decimal representations are padded with leading zeroes; the digits
		// already there stay where they are.
		s = strings.Repeat("0", min-len([]rune(s))) + s
	}
	if max := effectiveMaxWidth(pres, width); max > 0 && len([]rune(s)) > max && digits {
		r := []rune(s)
		s = string(r[len(r)-max:])
	}
	if digits {
		s = inDigitFamily(s, zero)
	}
	return s
}

// effectiveMaxWidth is the maximum width in force for a component, which the
// width modifier states outright and a leading-zero format token implies.
//
// Section 16.5.1: "A format token containing leading zeroes, such as 001, sets
// the minimum and maximum width to the number of digits appearing in the
// format token; if a width modifier is also present, then the width modifier
// takes precedence." Only the maximum was missing here, so [Y01] printed the
// full year 2003 where the suite wants 03.
func effectiveMaxWidth(pres, width string) int {
	if width != "" {
		return maxWidth(width)
	}
	zero, ok := digitFamilyOf(pres)
	if !ok {
		return 0
	}
	// A single one-digit token such as "1" places no constraint at all; only
	// a token that begins with the family's zero does.
	if r := []rune(pres); len(r) > 0 && r[0] == zero {
		return len(r)
	}
	return 0
}

// alphaNum renders n in the alphabetic sequence a, b, ... z, aa, ab, which is
// the "a"/"A" format token of xsl:number.
func alphaNum(n int64, upper bool) string {
	if n <= 0 {
		return strconv.FormatInt(n, 10)
	}
	base := byte('a')
	if upper {
		base = 'A'
	}
	var out []byte
	for n > 0 {
		n--
		out = append([]byte{base + byte(n%26)}, out...)
		n /= 26
	}
	return string(out)
}

// ordinalSuffixFor is the English ordinal suffix for a number in digits.
func ordinalSuffixFor(n int64) string {
	if n < 0 {
		n = -n
	}
	// 11, 12 and 13 take "th" despite ending in 1, 2 and 3, and that repeats
	// in every hundred.
	switch n % 100 {
	case 11, 12, 13:
		return "th"
	}
	switch n % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

// weekdayIndex is the day of the week with Sunday = 0.
func weekdayIndex(dt *xdm.DateTime) int {
	// 1970-01-01 was a Thursday, which anchors the modulo.
	days := daysFromCivilLocal(dt.Year, dt.Month, dt.Day)
	return int(((days+4)%7 + 7) % 7)
}

// weekOfYear is the ISO 8601 week number: weeks start on Monday and week 1 is
// the one containing the year's first Thursday.
func weekOfYear(dt *xdm.DateTime) int {
	// Shift to the Thursday of this week; the year that Thursday falls in is
	// the ISO week-numbering year, and the week number follows from its day
	// of the year.
	iso := (weekdayIndex(dt) + 6) % 7 // Monday = 0
	days := daysFromCivilLocal(dt.Year, dt.Month, dt.Day) - int64(iso) + 3
	// Recover the calendar year of that Thursday by walking from the value's
	// own year, which is at most one out in either direction.
	y := dt.Year
	for daysFromCivilLocal(y+1, 1, 1) <= days {
		y++
	}
	for daysFromCivilLocal(y, 1, 1) > days {
		y--
	}
	return int((days-daysFromCivilLocal(y, 1, 1))/7) + 1
}

// weekOfMonth is the week within the month, numbered on the same basis as the
// week of the year: the week containing the month's first Thursday is week 1.
func weekOfMonth(dt *xdm.DateTime) int {
	iso := (weekdayIndex(dt) + 6) % 7
	days := daysFromCivilLocal(dt.Year, dt.Month, dt.Day) - int64(iso) + 3
	first := daysFromCivilLocal(dt.Year, dt.Month, 1)
	firstISO := int64((weekdayIndex(&xdm.DateTime{Year: dt.Year, Month: dt.Month, Day: 1}) + 6) % 7)
	firstThu := first - firstISO + 3
	return int((days-firstThu)/7) + 1
}

// isDigitPattern reports whether pres is a decimal-digit-pattern: a run of
// digits whose length is the minimum field width.
func isDigitPattern(pres string) bool {
	_, ok := digitFamilyOf(pres)
	return ok
}

// digitFamilyOf reports whether pres is a decimal-digit-pattern and, if so,
// which Unicode digit family it is written in.
//
// Section 16.5.1 defines the pattern as "a sequence of characters that are
// classified as digits in the Unicode database", and adds that "all the digits
// must be from the same digit family, that is, they must be [decimal] digits
// whose Unicode code points are consecutive and start with zero". The family's
// zero is the value returned, because every digit of the output is that zero
// plus the digit's value: a picture written with Thai digits formats the year
// in Thai digits.
func digitFamilyOf(pres string) (zero rune, ok bool) {
	if pres == "" {
		return 0, false
	}
	first := true
	for _, r := range pres {
		if !unicode.IsDigit(r) {
			return 0, false
		}
		// The family's zero is the code point that many below r as r's own
		// numeric value. For ASCII that is '0'; for Thai it is U+0E50.
		z := r - rune(digitValueOf(r))
		if first {
			zero, first = z, false
		} else if z != zero {
			return 0, false
		}
	}
	return zero, true
}

// digitValueOf is the numeric value of a Unicode decimal digit, found by
// walking back to the start of its contiguous family. The standard library
// exposes no accessor for the Nd numeric value, and every decimal digit family
// is by definition ten consecutive code points beginning at zero.
func digitValueOf(r rune) int {
	v := 0
	for v < 10 && unicode.IsDigit(r-rune(v)-1) {
		v++
	}
	return v
}

// inDigitFamily rewrites an ASCII decimal string into the digit family whose
// zero is the given rune. It is the identity for the ASCII family.
func inDigitFamily(s string, zero rune) string {
	if zero == '0' {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(zero + (r - '0'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// fractionalSeconds renders the digits after the decimal point.
//
// Padding for this component is by *appending* zeroes rather than prepending
// them, because the digits are read left to right from the point: a minimum
// width of 3 turns ".5" into "500", not "005".
func fractionalSeconds(dt *xdm.DateTime, pres, width string) string {
	// A leading-zero format token fixes the width outright; a width modifier
	// overrides it. Without either, the value's own digits stand.
	min, max := 0, 0
	if isDigitPattern(pres) {
		min, max = len([]rune(pres)), len([]rune(pres))
	}
	if width != "" {
		min, max = minWidth(width), maxWidth(width)
	}

	frac := new(big.Rat).Sub(dt.Second,
		new(big.Rat).SetInt(new(big.Int).Quo(dt.Second.Num(), dt.Second.Denom())))

	// Rounding happens at the maximum width, because that is the width the
	// output is allowed to occupy: [f,2-2] on .456 is "46", not "45".
	places := max
	if places <= 0 {
		// No maximum: render enough places to hold the value exactly, since a
		// fraction stored as a rational always terminates in decimal here.
		places = 9
	}
	s := frac.FloatString(places)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[i+1:]
	} else {
		s = ""
	}
	// Trailing zeroes carry no information, so they go — but never below the
	// minimum width, which is exactly what padding is for. [f,1-4] on .456
	// is "456" rather than "4560".
	s = strings.TrimRight(s, "0")
	if min > len(s) {
		s += strings.Repeat("0", min-len(s))
	}
	if s == "" {
		s = "0"
	}
	return s
}

var weekdayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday",
	"Thursday", "Friday", "Saturday"}

// weekdayName returns the day's name in its canonical case; applyNameCase
// puts it in the case the picture asked for.
func weekdayName(dt *xdm.DateTime, pres string) string {
	// 1970-01-01 was a Thursday, which anchors the modulo.
	days := daysFromCivilLocal(dt.Year, dt.Month, dt.Day)
	idx := int(((days+4)%7 + 7) % 7)
	return weekdayNames[idx]
}

// monthNameOf returns the month's name in its canonical case.
func monthNameOf(dt *xdm.DateTime) string {
	if dt.Month < 1 || dt.Month > 12 {
		return ""
	}
	return monthNames[dt.Month-1]
}

// monthNames and weekdayNames are English. Section 16.5 of the XSLT
// specification makes the languages a processor supports
// implementation-defined, and requires only that the choice be documented —
// which the README does.
var monthNames = []string{
	"January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December",
}

func dayOfYear(dt *xdm.DateTime) int {
	return int(daysFromCivilLocal(dt.Year, dt.Month, dt.Day) -
		daysFromCivilLocal(dt.Year, 1, 1) + 1)
}

// daysFromCivilLocal mirrors the algorithm in the xdm package; duplicated here
// rather than exported because it is an implementation detail of formatting.
func daysFromCivilLocal(y, m, d int) int64 {
	if m <= 2 {
		y--
	}
	era := y
	if y < 0 {
		era = y - 399
	}
	era /= 400
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return int64(era*146097 + doe - 719468)
}

// formatTZMarker renders the [Z] and [z] components.
//
// The width modifier drives the shape of the offset. Its full representation
// is a sign, the hours, and — only when the offset is not a whole number of
// hours — a colon and the minutes; a minimum width of 5 or more is what asks
// for the minutes to be spelled out unconditionally.
func formatTZMarker(dt *xdm.DateTime, comp byte, pres, width string, traditional bool) string {
	if !dt.HasTZ {
		return ""
	}
	off := dt.TZOffset
	prefix := ""
	if comp == 'z' {
		prefix = "GMT"
	}
	// The specification permits, but does not require, writing UTC as "Z"
	// under "[Z]". The conformance suite settles the choice: the plain
	// picture spells "+00:00", and only the traditional modifier "[Z...t]"
	// asks for the single letter.
	if comp == 'Z' && off == 0 && traditional && !isNamePresentation(pres) {
		return "Z"
	}
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	hours, mins := off/60, off%60

	// An offset with minutes always spells them. Otherwise the shape is set
	// by how much room the picture asks for: with no width modifier and the
	// default presentation the padded "hh:mm" form is used, but a picture
	// that constrains the width to the hours alone — "[z,2-2]" or "[z0]" —
	// gets just the hours, because the specification says a value too long
	// for the maximum width should use a shorter representation.
	digits := 0
	if isDigitPattern(pres) {
		digits = len(pres)
	}
	max := maxWidth(width)
	full := mins != 0
	if !full {
		switch {
		case max > 0 && max < 5:
			// The maximum leaves no room for ":mm".
		case digits > 0 && digits < 3 && pres != "1":
			// "[z0]" and "[z01]" ask for the hours only. The bare "1" is the
			// component's *default* presentation rather than a request, so
			// it does not constrain the width the way "0" does.
		default:
			full = true
		}
	}
	if full {
		// The hours take the width the presentation modifier asks for, so
		// "[z0]" gives "GMT-9:30" where the default gives "GMT-09:30". The
		// minutes stay two digits: they are a fraction of an hour, not a
		// number in their own right.
		//
		// Only an explicit "0" counts. "1" is the component's *default*
		// presentation rather than a request for one digit — the same
		// distinction the hours-only branch below already draws — so "[z]"
		// and "[z,2-2]" keep the padded form. format-date-018 wants the
		// unpadded hours here and format-date-017 wants them padded.
		if digits == 1 && pres == "0" {
			return fmt.Sprintf("%s%s%d:%02d", prefix, sign, hours, mins)
		}
		return fmt.Sprintf("%s%s%02d:%02d", prefix, sign, hours, mins)
	}
	hs := strconv.FormatInt(int64(hours), 10)
	// The minimum width applies to what is actually being written. In the
	// "hh:mm" branch above that is five characters, so only a minimum of 3 or
	// more said anything about the hours; here the hours are the whole value,
	// and "[z,2-2]" is asking for two digits of them. Requiring >= 3 in this
	// branch too left "GMT-5" where format-date-017/018 want "GMT-05".
	if digits == 2 || minWidth(width) >= 2 {
		hs = fmt.Sprintf("%02d", hours)
	}
	return prefix + sign + hs
}

func romanNum(n int64) string {
	if n <= 0 || n > 3999 {
		return strconv.FormatInt(n, 10)
	}
	vals := []int64{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	var sb strings.Builder
	for i, v := range vals {
		for n >= v {
			sb.WriteString(syms[i])
			n -= v
		}
	}
	return sb.String()
}

// fnCollection implements fn:collection.
//
// It fails closed for the same reason fn:doc does: a collection URI that can
// name a directory is a file-disclosure vector, and a stylesheet has no
// business enumerating one unless the caller said so. With no resolver
// configured every URI is refused, including the default collection.
//
// The refusal is FODC0002 rather than an empty sequence deliberately. A
// stylesheet that iterates a collection and finds nothing cannot tell "there
// were no documents" from "collections are switched off", and silently
// processing zero documents is the worse of the two failures — it looks like
// success. Returning the error means a misconfiguration is reported instead of
// producing an empty result that appears legitimate.
func fnCollection(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
	// fn:collection() with no argument is the default collection, which the
	// resolver sees as the empty URI. The one-argument form is declared
	// xs:string?, so an empty sequence means the default collection too
	// rather than being a type error.
	uri := ""
	if len(args) > 0 && len(args[0]) != 0 {
		s, err := argStringRequired(args, 0)
		if err != nil {
			return nil, err
		}
		uri = s
		// An unusable URI is reported before access is attempted, matching
		// fn:doc: whether a resolver is configured does not come into it.
		if err := validAnyURI(uri); err != nil ||
			strings.HasPrefix(strings.TrimSpace(uri), ":") {
			return nil, fmt.Errorf("FODC0004: %q is not a valid collection URI", uri)
		}
	}
	if ctx.Collections == nil {
		return nil, fmt.Errorf("FODC0002: collections are not configured")
	}
	// A relative URI resolves against the *static* base URI — the base of the
	// expression itself — not against the context item's document. Passing
	// the item's base made collection("collection1") ask the resolver for
	// "collection1" with whatever document happened to be in focus, which is
	// not what the expression named.
	//
	// The context item's base URI is the fallback, for a caller who set no
	// static base but whose document has one.
	base := ctx.StaticBaseURI
	if base == "" {
		if n, ok := ctx.Item.(*xdm.Node); ok {
			base = n.BaseURI
		}
	}
	seq, err := ctx.Collections.ResolveCollection(uri, base)
	if err != nil {
		return nil, fmt.Errorf("FODC0002: cannot retrieve collection %q: %w", uri, err)
	}
	return seq, nil
}

// The presentation modifier of a named component: which case, and how wide.
//
// "[FN]" is MONDAY, "[FNn]" is Monday, "[Fn]" is monday — the case of the
// modifier's own letters says which. The width modifier then decides
// abbreviation: "[FNn,*-3]" is Mon, because a maximum width of 3 asks for the
// shortest form that fits. Dropping the width, as this did, made every
// abbreviated date come out in full.

// isNamePresentation reports whether a modifier asks for a name rather than a
// number. "N", "n" and "Nn" are the three spellings.
func isNamePresentation(pres string) bool {
	switch pres {
	case "N", "n", "Nn":
		return true
	}
	return false
}

// applyNameCase renders a name in the case the modifier asks for, truncated
// to the width modifier's maximum when it names one.
func applyNameCase(name, pres, width string) string {
	switch pres {
	case "N":
		name = strings.ToUpper(name)
	case "n":
		name = strings.ToLower(name)
	case "Nn":
		name = titleCase(name)
	}
	if max := maxWidth(width); max > 0 {
		if r := []rune(name); len(r) > max {
			name = abbreviateName(name, max)
		}
	}
	return name
}

// conventionalAbbreviations lists the English short forms that are in
// customary use, longest first, for names where crude truncation would give
// something nobody writes: "Tuesday" is shortened to "Tues" or "Tue", never
// to "Tuesd".
//
// Lookup is by the lower-cased full name so that the table is consulted
// before the case modifier has been applied.
var conventionalAbbreviations = map[string][]string{
	"monday":    {"Mon"},
	"tuesday":   {"Tues", "Tue"},
	"wednesday": {"Weds", "Wed"},
	"thursday":  {"Thurs", "Thur", "Thu"},
	"friday":    {"Fri"},
	"saturday":  {"Sat"},
	"sunday":    {"Sun"},
	"january":   {"Jan"},
	"february":  {"Feb"},
	"march":     {"Mar"},
	"april":     {"Apr"},
	"may":       {"May"},
	"june":      {"Jun"},
	"july":      {"Jul"},
	"august":    {"Aug"},
	"september": {"Sept", "Sep"},
	"october":   {"Oct"},
	"november":  {"Nov"},
	"december":  {"Dec"},
}

// abbreviateName shortens a name to at most max characters, preferring a
// conventional abbreviation over right-truncation.
//
// The longest conventional form that fits is the one chosen, because the
// width modifier states a maximum rather than an exact length and a reader
// asking for five characters would rather have "Thurs" than "Thur".
func abbreviateName(name string, max int) string {
	// The table is keyed on the canonical name, so the case the picture asked
	// for has to be re-applied to whatever comes back.
	key := strings.ToLower(name)
	for _, cand := range conventionalAbbreviations[key] {
		if len([]rune(cand)) <= max {
			return matchCaseOf(cand, name)
		}
	}
	return string([]rune(name)[:max])
}

// matchCaseOf puts abbr into the same case pattern as the already-cased name
// it abbreviates.
func matchCaseOf(abbr, name string) string {
	switch {
	case name == strings.ToUpper(name) && name != strings.ToLower(name):
		return strings.ToUpper(abbr)
	case name == strings.ToLower(name):
		return strings.ToLower(abbr)
	}
	return abbr
}

// titleCase upper-cases the first letter and lower-cases the rest, which is
// what "Nn" means. strings.Title is deprecated and word-splits, which is
// wrong here: "am" must become "Am", not "AM".
func titleCase(s string) string {
	r := []rune(strings.ToLower(s))
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// maxWidth reads the maximum from a width modifier, or 0 when it sets none.
//
// The syntax is min-max, either side optionally "*" meaning unbounded: "*-3"
// caps at three characters, "3-*" sets a floor and no cap, "3" sets both.
func maxWidth(width string) int {
	if width == "" {
		return 0
	}
	part := width
	if i := strings.IndexByte(width, '-'); i >= 0 {
		part = strings.TrimSpace(width[i+1:])
	}
	if part == "" || part == "*" {
		return 0
	}
	n := 0
	for _, c := range part {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// isIDAnnotation reports whether a type annotation names xs:ID or a type
// derived from it.
//
// fn:id is defined over attributes "of type xs:ID", which includes every
// restriction of it — a schema that names its own IDs is the ordinary case,
// not an exotic one.
func isIDAnnotation(annotation string) bool {
	return annotationDerivesFrom(annotation, "ID")
}

// isIDREFAnnotation reports whether an annotation names xs:IDREF or xs:IDREFS,
// or a type derived from either.
func isIDREFAnnotation(annotation string) bool {
	return annotationDerivesFrom(annotation, "IDREF") ||
		annotationDerivesFrom(annotation, "IDREFS")
}

// annotationDerivesFrom walks the derivation chain a schema recorded.
//
// The walk is bounded for the same reason the one in typeexpr.go is: this runs
// once per attribute of every node, and a schema whose derivations formed a
// cycle would otherwise not terminate.
func annotationDerivesFrom(annotation, base string) bool {
	for i := 0; i < 32 && annotation != ""; i++ {
		if annotation == base {
			return true
		}
		annotation = xdm.DerivedBase(annotation)
	}
	return false
}

// Number-to-words for the "w", "W" and "Ww" format tokens of a date picture.
//
// Section 16.5.1 defers these to the rules for xsl:number format tokens, and
// section 12.3 in turn makes the actual words implementation-defined and
// language-sensitive. English is what is implemented; a request for a
// language that is not supported falls back to it, which is what the
// specification prescribes.

var dateSmallWords = []string{
	"zero", "one", "two", "three", "four", "five", "six", "seven", "eight",
	"nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
	"sixteen", "seventeen", "eighteen", "nineteen",
}

var dateTensWords = []string{
	"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy",
	"eighty", "ninety",
}

// dateScaleWords are the powers of a thousand, in the short scale.
var dateScaleWords = []string{"", " thousand", " million", " billion", " trillion"}

// dateIrregularOrdinals are the words whose ordinal is not "word" + "th".
var dateIrregularOrdinals = map[string]string{
	"one": "first", "two": "second", "three": "third", "five": "fifth",
	"eight": "eighth", "nine": "ninth", "twelve": "twelfth",
}

// spellDateNumber writes n in English words, cased as the presentation
// modifier asks and in ordinal form when the second modifier said so.
func spellDateNumber(n int64, pres string, ordinal bool) string {
	s := spellCardinal(n)
	if ordinal {
		s = spellOrdinalOf(s)
	}
	switch pres {
	case "W":
		s = strings.ToUpper(s)
	case "Ww":
		s = titleCaseWords(s)
	}
	if ordinal {
		// The ordinal forms run their words together: "twentyfirst", and
		// "OneThousandNineHundredandNinetieth" with the conjunction left
		// lower case. That is the convention the conformance suite fixes for
		// English, and the specification leaves the choice to the processor.
		s = strings.ReplaceAll(s, "-", "")
		if pres == "W" {
			s = strings.ReplaceAll(s, " ", "")
		} else if pres == "Ww" {
			s = strings.ReplaceAll(s, "And", "and")
			s = strings.ReplaceAll(s, " ", "")
		} else {
			s = strings.ReplaceAll(s, " ", "")
		}
	} else {
		// Cardinals keep their word boundaries; the hyphen of a compound ten
		// becomes a space in the run-together cases only.
		if pres == "W" || pres == "Ww" {
			s = strings.ReplaceAll(s, "-", " ")
		}
		if pres == "Ww" {
			s = strings.ReplaceAll(s, "And", "and")
		}
	}
	return s
}

// titleCaseWords upper-cases the first letter of every word, leaving the rest
// lower case.
func titleCaseWords(s string) string {
	r := []rune(strings.ToLower(s))
	up := true
	for i, c := range r {
		if up && unicode.IsLetter(c) {
			r[i] = unicode.ToUpper(c)
			up = false
		} else if c == ' ' || c == '-' {
			up = true
		}
	}
	return string(r)
}

// spellCardinal writes n in English words, with the British "and" before a
// final group below one hundred: "one thousand nine hundred and ninety".
func spellCardinal(n int64) string {
	if n < 0 {
		return "minus " + spellCardinal(-n)
	}
	if n == 0 {
		return "zero"
	}
	// Beyond the named scales there is no agreed English word, so the number
	// falls back to digits rather than inventing one.
	if n >= 1000000000000000 {
		return strconv.FormatInt(n, 10)
	}

	// Split into groups of three from the least significant end, so each
	// group is spoken with its own scale word.
	var groups []int64
	for v := n; v > 0; v /= 1000 {
		groups = append(groups, v%1000)
	}

	var parts []string
	for i := len(groups) - 1; i >= 0; i-- {
		g := groups[i]
		if g == 0 {
			continue
		}
		// The conjunction goes before the final group when that group is
		// below a hundred and something precedes it: "two thousand and one",
		// but "two thousand one hundred".
		if i == 0 && len(parts) > 0 && g < 100 {
			parts = append(parts, "and")
		}
		parts = append(parts, spellDateUnder1000(g)+dateScaleWords[i])
	}
	return strings.Join(parts, " ")
}

func spellDateUnder1000(n int64) string {
	switch {
	case n < 20:
		return dateSmallWords[n]
	case n < 100:
		s := dateTensWords[n/10]
		if n%10 != 0 {
			s += "-" + dateSmallWords[n%10]
		}
		return s
	default:
		s := dateSmallWords[n/100] + " hundred"
		if n%100 != 0 {
			s += " and " + spellDateUnder1000(n%100)
		}
		return s
	}
}

// spellOrdinalOf converts the last word of a spelled number to its ordinal.
//
// Only the last word changes: "one hundred and twenty-first" ends in the
// ordinal and everything before it stays cardinal. A trailing scale word is
// itself ordinalised — "two thousand" becomes "two thousandth".
func spellOrdinalOf(s string) string {
	i := strings.LastIndexAny(s, " -")
	head, last := "", s
	if i >= 0 {
		head, last = s[:i+1], s[i+1:]
	}
	if o, ok := dateIrregularOrdinals[last]; ok {
		return head + o
	}
	// A word ending in "y" forms its ordinal in "ieth": twenty, twentieth.
	if strings.HasSuffix(last, "y") {
		return head + last[:len(last)-1] + "ieth"
	}
	return head + last + "th"
}

// requestedLanguage reads the $language argument, reporting whether one was
// supplied at all: an omitted or empty argument means the processor picks,
// which is never a fallback.
func requestedLanguage(args []xdm.Sequence) (string, bool) {
	if len(args) < 3 || len(args[2]) == 0 {
		return "", false
	}
	s, err := argString(args, 2)
	if err != nil || strings.TrimSpace(s) == "" {
		return "", false
	}
	return strings.TrimSpace(s), true
}

// supportedLanguage reports whether the language tag selects the one language
// whose names and number words are implemented.
//
// Only the primary subtag matters: "en-GB" and "en-US" both select English,
// and neither is a fallback.
func supportedLanguage(lang string) bool {
	primary := lang
	if i := strings.IndexByte(lang, '-'); i >= 0 {
		primary = lang[:i]
	}
	return strings.EqualFold(primary, "en")
}

// pictureUsesNames reports whether the picture asks for anything whose text is
// language-dependent — a name, or a number spelled out in words.
//
// A picture made only of digits renders the same in every language, so
// announcing a language substitution for it would be noise rather than the
// information section 16.5.2 asks for.
func pictureUsesNames(pic string) bool {
	runes := []rune(pic)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '[' {
			continue
		}
		if i+1 < len(runes) && runes[i+1] == '[' {
			i++
			continue
		}
		end := i + 1
		for end < len(runes) && runes[end] != ']' {
			end++
		}
		if end >= len(runes) {
			return false
		}
		marker := string(runes[i+1 : end])
		i = end
		if marker == "" {
			continue
		}
		comp := marker[0]
		pres := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, marker[1:])
		if j := strings.IndexByte(pres, ','); j >= 0 {
			pres = pres[:j]
		}
		if pres == "" {
			pres = defaultPresentation(comp)
		}
		// The components that are always words, plus any component whose
		// presentation modifier asks for a name or for words.
		if strings.IndexByte("FPCE", comp) >= 0 || isNamePresentation(pres) {
			return true
		}
		switch strings.TrimRight(pres, "ot") {
		case "w", "W", "Ww":
			return true
		}
	}
	return false
}

// RegisterHarnessFuncs adds the XPath 3.0 functions that the W3C conformance
// harness needs in order to SET UP a test, as distinct from the functions a
// stylesheet under test may call.
//
// It exists for the same reason ParseExtended does, and observes the same
// boundary. Several XSLT test-set environments describe their initial context
// with an XPath 3.0 expression even when the stylesheet they then run is an
// XSLT 2.0 one — id-043's environment is
//
//	<source select="parse-xml('&lt;root/>')" role="."/>
//
// while id-043.xsl itself declares version="2.0". The setup expression is the
// harness's own XPath, not the test subject's, so it is written in the 3.0
// language by design.
//
// These functions are deliberately NOT in Builtins(). An XPath 2.0 processor
// is required to report XPST0017 for fn:parse-xml, and a stylesheet compiled
// against the builtin library still does, which is what the suite asserts
// elsewhere. Registering into a chained Library keeps the two populations
// separate in the same way RegisterXSLTFuncs does for the XSLT-only functions.
func RegisterHarnessFuncs(l *Library) {
	// fn:parse-xml builds a document node from a string of XML. The result is
	// a document, not the element: the spec returns document-node(), and the
	// callers that matter (an environment's initial context item) navigate
	// from the root.
	l.registerFn("parse-xml", []int{1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The parameter is xs:string?, so an empty sequence is the empty
		// sequence rather than a parse of "".
		if len(args) > 0 && len(args[0]) == 0 {
			return xdm.Empty(), nil
		}
		s, err := argStringRequired(args, 0)
		if err != nil {
			return nil, err
		}
		// The base URI of the constructed document is the static base URI of
		// the call, per the spec. With none available the document simply has
		// no base URI, which is what document-uri() then reports as empty.
		tree, perr := xdm.ParseString(s, xdm.ParseOptions{
			AllowDOCTYPE: true,
			BaseURI:      ctx.StaticBaseURI,
		})
		if perr != nil {
			// FODC0006 is the code the spec gives for a string that is not a
			// well-formed document, rather than a generic failure.
			return nil, xdm.Errorf("FODC0006",
				"fn:parse-xml: argument is not a well-formed XML document: %v", perr)
		}
		return xdm.One(tree.Root), nil
	})
}
