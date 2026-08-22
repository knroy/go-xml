package xpath

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// registerMiscFuncs adds the remaining F&O functions.
func registerMiscFuncs(l *Library) {
	l.registerFn("deep-equal", []int{2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The collation applies to every string comparison the traversal
		// makes. It was validated nowhere and applied nowhere, so
		// deep-equal(("a","A"), ("A","a"), <case-insensitive>) was false.
		coll, err := collationArg("deep-equal", args, 2)
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
			return xdm.Empty, nil
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
		return nil, fmt.Errorf(
			"FOUT1170: unparsed-text() is disabled (it reads arbitrary files)")
	})
	l.registerFn("unparsed-text-available", []int{1, 2}, func(_ *Context, _ []xdm.Sequence) (xdm.Sequence, error) {
		return boolSeq(false), nil
	})

	registerFormatDateTime(l)
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

	// The argument is a whitespace-separated list of names.
	want := map[string]bool{}
	for _, it := range xdm.Atomize(args[0]) {
		for _, f := range strings.Fields(it.(*xdm.Atomic).String()) {
			want[f] = true
		}
	}
	if len(want) == 0 {
		return xdm.Empty, nil
	}

	var out xdm.Sequence
	var walk func(*xdm.Node)
	walk = func(n *xdm.Node) {
		if n.Kind == xdm.KindElement {
			for _, a := range n.Attrs {
				isIDAttr := (a.Name.URI == xdm.NSXML && a.Name.Local == "id") ||
					(a.Name.URI == "" && a.Name.Local == "id")
				isRefAttr := a.Name.URI == "" &&
					(a.Name.Local == "idref" || a.Name.Local == "idrefs")

				if wantID && isIDAttr && want[a.Value] {
					out = append(out, n)
				}
				if !wantID && isRefAttr {
					for _, v := range strings.Fields(a.Value) {
						if want[v] {
							out = append(out, n)
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
			if ba == nil || ba.Value != aa.Value {
				return false, nil
			}
		}
		return deepEqualContent(ctx, a, b)

	case xdm.KindAttribute, xdm.KindNamespace:
		return a.Name.URI == b.Name.URI && a.Name.Local == b.Name.Local &&
			a.Value == b.Value, nil

	case xdm.KindPI:
		return a.Name.Local == b.Name.Local && a.Value == b.Value, nil

	case xdm.KindText, xdm.KindComment:
		return a.Value == b.Value, nil
	}
	return false, nil
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
				return xdm.Empty, nil
			}
			a, ok := atoms[0].(*xdm.Atomic)
			if !ok || a.DateTimeVal() == nil {
				return nil, xdm.ErrType("%s: expected a date/time value", name)
			}
			pic, err := argString(args, 1)
			if err != nil {
				return nil, err
			}
			out, err := formatDateTimePicture(a.DateTimeVal(), pic)
			if err != nil {
				return nil, err
			}
			return strSeq(out), nil
		})
	}
	format("format-dateTime")
	format("format-date")
	format("format-time")
}

// formatDateTimePicture renders a date/time through a picture string.
func formatDateTimePicture(dt *xdm.DateTime, pic string) (string, error) {
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

		text, err := formatComponent(dt, marker)
		if err != nil {
			return "", err
		}
		sb.WriteString(text)
	}
	return sb.String(), nil
}

// formatComponent renders one [component]presentation marker.
func formatComponent(dt *xdm.DateTime, marker string) (string, error) {
	if marker == "" {
		return "", fmt.Errorf("FOFD1340: empty component in picture")
	}
	comp := marker[0]
	pres := strings.TrimSpace(marker[1:])
	// A width modifier follows a comma; only the minimum width is honoured.
	if i := strings.IndexByte(pres, ','); i >= 0 {
		pres = pres[:i]
	}
	if pres == "" {
		pres = "1"
	}

	switch comp {
	case 'Y':
		return padNumber(int64(dt.Year), pres), nil
	case 'M':
		return padNumber(int64(dt.Month), pres), nil
	case 'D':
		return padNumber(int64(dt.Day), pres), nil
	case 'd':
		return padNumber(int64(dayOfYear(dt)), pres), nil
	case 'H':
		return padNumber(int64(dt.Hour), pres), nil
	case 'h':
		// 12-hour clock: midnight and noon are both "12".
		h := dt.Hour % 12
		if h == 0 {
			h = 12
		}
		return padNumber(int64(h), pres), nil
	case 'm':
		return padNumber(int64(dt.Minute), pres), nil
	case 's':
		whole := new(big.Int).Quo(dt.Second.Num(), dt.Second.Denom()).Int64()
		return padNumber(whole, pres), nil
	case 'f':
		return fractionalSeconds(dt, pres), nil
	case 'P':
		if dt.Hour < 12 {
			return "am", nil
		}
		return "pm", nil
	case 'F':
		return weekdayName(dt, pres), nil
	case 'Z', 'z':
		return formatTZMarker(dt, comp), nil
	}
	return "", fmt.Errorf("FOFD1340: unsupported picture component %q", string(comp))
}

// padNumber renders n per a presentation modifier.
//
// A decimal-digit-pattern such as "01" or "0001" sets the minimum width to its
// own length — "[M01]" means a two-digit month, so January is "01" and not
// "1". The pattern is any run of digits ending in "1"; "1" alone means no
// padding.
func padNumber(n int64, pres string) string {
	switch pres {
	case "i":
		return strings.ToLower(romanNum(n))
	case "I":
		return romanNum(n)
	case "N", "n", "Nn":
		// Name forms are meaningful only for components the caller handles
		// separately (month, weekday); for a bare number there is no name.
		return strconv.FormatInt(n, 10)
	}
	if isDigitPattern(pres) {
		return fmt.Sprintf("%0*d", len(pres), n)
	}
	return strconv.FormatInt(n, 10)
}

// isDigitPattern reports whether pres is a decimal-digit-pattern: a run of
// digits whose length is the minimum field width.
func isDigitPattern(pres string) bool {
	if pres == "" {
		return false
	}
	for i := 0; i < len(pres); i++ {
		if pres[i] < '0' || pres[i] > '9' {
			return false
		}
	}
	return true
}

func fractionalSeconds(dt *xdm.DateTime, pres string) string {
	digits := 1
	if isDigitPattern(pres) {
		digits = len(pres)
	}
	frac := new(big.Rat).Sub(dt.Second,
		new(big.Rat).SetInt(new(big.Int).Quo(dt.Second.Num(), dt.Second.Denom())))
	s := frac.FloatString(digits)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[i+1:]
	}
	return strings.Repeat("0", digits)
}

var weekdayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday",
	"Thursday", "Friday", "Saturday"}

func weekdayName(dt *xdm.DateTime, pres string) string {
	// 1970-01-01 was a Thursday, which anchors the modulo.
	days := daysFromCivilLocal(dt.Year, dt.Month, dt.Day)
	idx := int(((days+4)%7 + 7) % 7)
	name := weekdayNames[idx]
	if pres == "N" {
		return strings.ToUpper(name)
	}
	if len(pres) > 1 && strings.Trim(pres, "n") == "" {
		return name
	}
	return name
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

func formatTZMarker(dt *xdm.DateTime, comp byte) string {
	if !dt.HasTZ {
		return ""
	}
	off := dt.TZOffset
	if comp == 'Z' && off == 0 {
		return "Z"
	}
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	s := fmt.Sprintf("%s%02d:%02d", sign, off/60, off%60)
	if comp == 'z' {
		return "GMT" + s
	}
	return s
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
	base := ""
	if n, ok := ctx.Item.(*xdm.Node); ok {
		base = n.BaseURI
	}
	seq, err := ctx.Collections.ResolveCollection(uri, base)
	if err != nil {
		return nil, fmt.Errorf("FODC0002: cannot retrieve collection %q: %w", uri, err)
	}
	return seq, nil
}
