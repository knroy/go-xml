package xpath

import (
	"errors"
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
	registerUnparsedText(l, XPath20)

	// fn:document is XSLT's own document loader, and differs from fn:doc in
	// ways a stylesheet depends on: a sequence of URIs, a base-supplying
	// second argument, and deduplication by identity. See fn_document.go.
	l.registerFn("document", []int{1, 2}, fnDocument)

	registerFormatDateTime(l)
}

// registerUnparsedText adds fn:unparsed-text and its siblings.
//
// XSLT 2.0 defines them and XPath 3.0 promotes them into the core function
// library, so they are registered twice: unversioned for a stylesheet, and as
// 3.0 additions for a bare XPath expression. See registerFormatDateTimeSince,
// which has the same shape for the same reason.
func registerUnparsedText(l *Library, since Version) {
	l.registerFnSince(since, "unparsed-text", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		text, err := unparsedText(ctx, args)
		if err != nil {
			return nil, err
		}
		return xdm.One(xdm.NewString(text)), nil
	})

	l.registerFnSince(since, "unparsed-text-available", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// Defined as "true if a call on unparsed-text with the same
		// arguments would succeed", so it is answered by attempting the read
		// rather than by inspecting the URI. With no resolver it is false,
		// which is what the refusal tests expect.
		if _, err := unparsedText(ctx, args); err != nil {
			return boolSeq(false), nil
		}
		return boolSeq(true), nil
	})

	// fn:unparsed-text-lines is 3.0 only: XSLT 2.0 has no such function, so
	// it is registered at the later of the two versions whichever way this is
	// called.
	lineVersion := since
	if !lineVersion.atLeast30() {
		lineVersion = XPath30
	}
	l.registerFnSince(lineVersion, "unparsed-text-lines", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		text, err := unparsedText(ctx, args)
		if err != nil {
			return nil, err
		}
		return xdm.Sequence(splitTextLines(text)), nil
	})
}

// unparsedText performs the read shared by the three functions.
func unparsedText(ctx *Context, args []xdm.Sequence) (string, error) {
	s, enc, err := unparsedTextArgs(args)
	if err != nil {
		return "", err
	}
	// No resolver means the function is off, which is the default. The
	// message names the reason rather than the URI: a stylesheet that gets
	// this back has not been granted file reads at all, and saying "cannot
	// retrieve x" would suggest the file was the problem.
	if ctx.Texts == nil {
		return "", fmt.Errorf(
			"FOUT1170: unparsed-text() is disabled (it reads arbitrary files)")
	}
	text, err := ctx.Texts.ResolveText(s, ctx.StaticBaseURI, enc)
	if err != nil {
		return "", fmt.Errorf("FOUT1170: cannot retrieve %q: %w", s, err)
	}
	return text, nil
}

// splitTextLines splits on the line endings fn:unparsed-text-lines recognises.
//
// The spec defines the result as the sequence obtained by splitting on \n,
// \r\n or \r, with a trailing line ending producing no final empty string —
// "a\n" is one line, not two.
func splitTextLines(text string) []xdm.Item {
	if text == "" {
		return nil
	}
	// Normalise the two-character ending first so the split is over single
	// characters, then treat a lone \r as an ending too.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSuffix(text, "\n")
	parts := strings.Split(text, "\n")
	out := make([]xdm.Item, 0, len(parts))
	for _, p := range parts {
		out = append(out, xdm.NewString(p))
	}
	return out
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

	// A function item has no defined equality, so comparing one is FOTY0015
	// rather than an answer. Asserting *xdm.Atomic below panicked on it,
	// which a stylesheet could reach from document data.
	if _, ok := x.(*xdm.FunctionItem); ok {
		return false, xdm.Errorf("FOTY0015",
			"fn:deep-equal: a function item cannot be compared")
	}
	if _, ok := y.(*xdm.FunctionItem); ok {
		return false, xdm.Errorf("FOTY0015",
			"fn:deep-equal: a function item cannot be compared")
	}

	xn, xIsNode := x.(*xdm.Node)
	yn, yIsNode := y.(*xdm.Node)
	if xIsNode != yIsNode {
		return false, nil
	}
	if !xIsNode {
		xa, xok := x.(*xdm.Atomic)
		ya, yok := y.(*xdm.Atomic)
		if !xok || !yok {
			// Neither a node nor an atomic value nor anything handled above:
			// compare by identity rather than asserting a type it does not
			// have.
			return x == y, nil
		}
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
	registerFormatDateTimeSince(l, XPath20)
}

// registerFormatDateTimeSince is registerFormatDateTime with a version.
//
// XSLT 2.0 defines fn:format-dateTime and its siblings, and XPath 3.0 promotes
// them into the core function library. So they are registered twice: once
// unversioned for a stylesheet, which has had them since 2.0, and once as 3.0
// additions for a bare XPath expression, which must not see them under 2.0.
func registerFormatDateTimeSince(l *Library, since Version) {
	format := func(name string) {
		// Two arities only: the picture-only form and the full
		// (value, picture, language, calendar, place) form. There is no
		// three- or four-argument signature, so format-date(d, p, "") is
		// XPST0017 rather than a call with a defaulted calendar.
		l.registerFnSince(since, name, []int{2, 5}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
			if err := checkFormatDateArgs(args); err != nil {
				return nil, err
			}
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

// checkFormatDateArgs validates the language, calendar and place arguments of
// the five-argument form.
//
// The calendar is a lexical QName — "AD", "ISO", or an implementation's own
// name — so a value that is not one is FOFD1340. The place is a string, and a
// non-string there is a type error rather than a picture error.
func checkFormatDateArgs(args []xdm.Sequence) error {
	if len(args) < 5 {
		return nil
	}
	// The place argument is declared xs:string?, so anything else is
	// XPTY0004 — and it is checked before the picture, since an argument
	// that does not typecheck is an error whatever the picture says.
	if len(args[4]) > 0 {
		atoms := xdm.Atomize(args[4])
		if len(atoms) > 0 {
			a, ok := atoms[0].(*xdm.Atomic)
			if !ok || (a.Type != xdm.TypeString && a.Type != xdm.TypeUntypedAtomic &&
				a.Type != xdm.TypeAnyURI) {
				return xdm.ErrType("XPTY0004: the place argument must be an xs:string")
			}
		}
	}
	if len(args[3]) > 0 {
		cal, err := argString(args, 3)
		if err != nil {
			return err
		}
		if cal != "" && !isCalendarName(cal) {
			return fmt.Errorf("FOFD1340: %q is not a valid calendar name", cal)
		}
	}
	return nil
}

// isCalendarName reports whether s is lexically a calendar name.
//
// The grammar is an EQName: a QName, or a braced URI literal followed by an
// NCName. What it must not be is an arbitrary string — ":w" has an empty
// prefix and "Q{}1" a local part starting with a digit, and both are
// FOFD1340.
func isCalendarName(s string) bool {
	if rest, ok := strings.CutPrefix(s, "Q{"); ok {
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return false
		}
		return isNCName(rest[end+1:])
	}
	prefix, local := xdm.SplitQName(s)
	if prefix != "" && !isNCName(prefix) {
		return false
	}
	if strings.Contains(s, ":") && prefix == "" {
		return false
	}
	return isNCName(local)
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
	//
	// The split is on the *last* comma rather than the first, because a comma
	// is also a perfectly ordinary grouping-separator inside a digit pattern:
	// "[Y9,999,*]" is the pattern "9,999" with the width "*", not the pattern
	// "9" with the width "999,*". Splitting on the first comma read the
	// grouping separator as part of the width and rejected the picture. A
	// trailing comma that does not introduce a valid width is left in the
	// pattern, where the digit-pattern grammar judges it.
	// Where more than one comma could be the split, the last one that yields a
	// valid width wins; if none does, the last comma is still the split, so
	// its malformed width is reported rather than quietly swallowed into the
	// pattern. "[f1,0-3]" is FOFD1340, not the picture "1,0-3".
	pres, width := rest, ""
	if last := strings.LastIndexByte(rest, ','); last >= 0 {
		pres, width = rest[:last], rest[last+1:]
		for i := last; i >= 0; i-- {
			if rest[i] != ',' {
				continue
			}
			if w := rest[i+1:]; checkWidthModifier(w) == nil {
				pres, width = rest[:i], w
				break
			}
		}
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
	// The fractional-seconds component pads on the right rather than the
	// left, so its digit pattern reads the other way round: "[f99#]" is two
	// mandatory digits then an optional one, which the integer grammar would
	// reject as an optional sign following a mandatory one. It is exempt.
	if comp != 'f' {
		if err := checkPresentationModifier(pres); err != nil {
			return "", err
		}
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
		// The truncation applies to any numeric presentation, not only a
		// pattern of bare digits: "[Y#0,2-5]" is still a decimal pattern, and
		// requiring isDigitPattern left its optional-digit form untruncated.
		if max := effectiveMaxWidth(pres, width); max > 0 && max < 18 &&
			!isNamePresentation(pres) && containsDigit(pres) {
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
		return fractionalSeconds(dt, pres, width)
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

// checkPresentationModifier rejects a malformed decimal-digit-pattern.
//
// A presentation modifier is either one of the named sequences — "N", "n",
// "Nn", "a", "A", "i", "I", "w", "W", "Ww" — or a decimal-digit-pattern, and
// the pattern has the same grammar fn:format-integer uses: optional-digit
// signs precede mandatory ones, at least one mandatory sign is present, and a
// grouping separator sits neither at an edge nor beside another. "[Y999#]" and
// "[Y9,999,*]" are FOFD1340 rather than pictures to render.
func checkPresentationModifier(pres string) error {
	if pres == "" || isNamePresentation(pres) {
		return nil
	}
	switch pres {
	case "a", "A", "i", "I", "w", "W", "Ww":
		return nil
	}
	// A modifier with no digit at all is not a digit pattern, and is left to
	// the component to interpret or reject.
	if !containsDigit(pres) {
		return nil
	}
	if _, _, _, err := parseDigitPattern(pres); err != nil {
		// parseDigitPattern reports the format-integer code; the date and time
		// functions use their own for the same condition.
		return errors.New(strings.Replace(err.Error(), "FODF1310", "FOFD1340", 1))
	}
	return nil
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

	// A digit pattern may carry grouping separators — "[Y9,999]" writes 2012
	// as "2,012" — which digitFamilyOf rejects outright because it admits
	// only bare digits. Where the picture is a grouped pattern, the
	// format-integer grammar already knows how to read and apply it, so the
	// component borrows it rather than growing a second implementation.
	if zero, mandatory, groups, err := parseDigitPattern(pres); err == nil && len(groups) > 0 {
		s := fmt.Sprintf("%0*d", mandatory, n)
		if ordinal {
			s += ordinalSuffixFor(n)
		}
		if min := minWidth(width); min > len([]rune(s)) {
			s = strings.Repeat("0", min-len([]rune(s))) + s
		}
		if max := maxWidth(width); max > 0 && len([]rune(s)) > max {
			r := []rune(s)
			s = string(r[len(r)-max:])
		}
		return inDigitFamily(applyIntegerGrouping(s, groups), zero)
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
	// A single one-digit token places no constraint at all: the rule is about
	// *leading* zeroes, and one character has nothing to lead. "[m0]" on
	// minute 15 is "15", not the "5" a width of one would truncate it to,
	// and the same holds for a token written in another digit family.
	if r := []rune(pres); len(r) > 1 && r[0] == zero {
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
func fractionalSeconds(dt *xdm.DateTime, pres, width string) (string, error) {
	// The presentation modifier is a digit pattern, mirrored: a fraction is
	// read away from the point, so its optional digits trail the mandatory
	// ones. "[f99#]" is two required places and a third if the value has it;
	// "[f#99]" puts an optional digit where a required one must come first,
	// which is FOFD1340 rather than a picture with a literal "#" in it.
	fp, err := parseFracPattern(pres)
	if err != nil {
		return "", err
	}
	zero := fp.zero
	min, max := fp.mandatory, fp.mandatory+fp.optional
	// A single "1" is the component's *default* presentation rather than a
	// request for one digit, exactly as it is for the timezone hours: "[f]"
	// and "[f1]" show the digits the value actually has.
	if fp.mandatory == 1 && fp.optional == 0 && (pres == "1" || pres == "") {
		min, max = 0, 0
	}
	if width != "" {
		// A width modifier whose minimum exceeds its maximum describes no
		// width at all, whether or not the picture also states one.
		if wmin, wmax := minWidth(width), maxWidth(width); wmax > 0 && wmin > wmax {
			return "", fmt.Errorf(
				"FOFD1340: the width modifier %q has a minimum above its maximum", width)
		} else if !fp.stated {
			// The width modifier applies only when the presentation modifier
			// did not state a digit pattern of its own. W3C bug 29788 settled
			// that the pattern wins where both are given, so "[f111,2-2]" on
			// .123 is "123" rather than the "12" the width alone would cut.
			min, max = wmin, wmax
		}
	}

	frac := new(big.Rat).Sub(dt.Second,
		new(big.Rat).SetInt(new(big.Int).Quo(dt.Second.Num(), dt.Second.Denom())))

	// The fraction is *truncated* at the maximum width, not rounded: the
	// digits of a fractional second are the digits the value has, and the
	// component shows as many of them as the width allows. [f,2-2] on .456 is
	// "45", which the suite asserts in six places — rounding it to "46" would
	// report a time that did not occur.
	places := max
	if places <= 0 {
		// No maximum: render enough places to hold the value exactly, since a
		// fraction stored as a rational always terminates in decimal here.
		places = 9
	}
	// One extra place, then drop it: FloatString rounds, so the truncation is
	// done by asking for more than is wanted and cutting.
	s := frac.FloatString(places + 1)
	if i := strings.IndexByte(s, '.'); i >= 0 && len(s)-i-1 > places {
		s = s[:len(s)-1]
	}
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
	return applyFracPicture(translateDigits(s, zero), pres, zero), nil
}

// fracPattern is a parsed fractional-seconds digit pattern.
type fracPattern struct {
	zero      rune
	mandatory int
	optional  int
	// stated records whether the picture wrote a digit pattern at all, which
	// the defaulted counts below can no longer be read off.
	stated bool
}

// parseFracPattern reads the presentation modifier of an [f] component.
//
// It is the integer digit pattern with the optional and mandatory digits the
// other way round, because a fraction is written away from the decimal point:
// the leading places are the ones that must be there, and "#" may only trail.
// Anything that is neither a digit nor "#" is literal text the picture writes
// between the digits, which applyFracPicture puts back.
func parseFracPattern(pres string) (fracPattern, error) {
	fp := fracPattern{zero: '0'}
	if pres == "" {
		fp.mandatory = 1
		return fp, nil
	}
	// A presentation modifier that names a numbering sequence rather than a
	// digit pattern ("[fI]", "[fw]") is not this component's business; leave
	// it to the caller's existing handling by reporting no digits.
	seenZero := false
	for _, r := range pres {
		switch {
		case r == '#':
			fp.optional++
		case unicode.IsDigit(r):
			if fp.optional > 0 {
				return fracPattern{}, fmt.Errorf(
					"FOFD1340: a mandatory digit follows an optional one in %q", pres)
			}
			z := r - rune(digitValueOf(r))
			if !seenZero {
				fp.zero, seenZero = z, true
			} else if z != fp.zero {
				return fracPattern{}, fmt.Errorf(
					"FOFD1340: mixed digit families in %q", pres)
			}
			fp.mandatory++
		default:
			// Literal separator text; applyFracPicture writes it back.
		}
	}
	// A bare "1" is the component's default presentation rather than a
	// pattern the picture chose, which matters because a width modifier
	// applies only where the picture stated no pattern of its own: "[f,4-4]"
	// arrives here as "1" after defaulting, and its width must still be
	// honoured.
	fp.stated = (fp.mandatory > 0 || fp.optional > 0) && pres != "1"
	if fp.mandatory == 0 && fp.optional == 0 {
		fp.mandatory = 1
	}
	return fp, nil
}

// applyFracPicture interleaves the literal text a picture writes between its
// digits, so "[f0'0'0]" on .135 gives "1'3'5".
//
// A picture with no literal text is returned unchanged, which is the common
// case and the one that must stay free of surprises.
func applyFracPicture(s, pres string, zero rune) string {
	runes := []rune(pres)
	hasLiteral := false
	for _, r := range runes {
		// "#" is an optional-digit sign, not literal text: the digits it
		// stands for are already in s, so it must not be written through.
		if !unicode.IsDigit(r) && r != '#' {
			hasLiteral = true
			break
		}
	}
	if !hasLiteral {
		return s
	}
	var sb strings.Builder
	digits := []rune(s)
	i := 0
	for _, r := range runes {
		if unicode.IsDigit(r) || r == '#' {
			if i < len(digits) {
				sb.WriteRune(digits[i])
				i++
			}
			continue
		}
		// Literal text is written only while digits remain to separate.
		if i < len(digits) {
			sb.WriteRune(r)
		}
	}
	// Any digits the picture had no place for still follow.
	for ; i < len(digits); i++ {
		sb.WriteRune(digits[i])
	}
	return sb.String()
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
		// "[ZZ]" asks for the military letter, and J is the one that names
		// the absence of a timezone. Every other picture renders nothing.
		if comp == 'Z' && pres == "Z" {
			return "J"
		}
		return ""
	}
	off := dt.TZOffset
	prefix := ""
	if comp == 'z' {
		prefix = "GMT"
	}
	// "[ZZ]" asks for the military timezone letter: A..I and K..M for the
	// eastern offsets, N..Y for the western, Z for UTC, and J for no
	// timezone. J is unreachable here, since an absent timezone returned
	// above. An offset that is not a whole number of hours has no letter, so
	// it falls through to the numeric form.
	if comp == 'Z' && pres == "Z" {
		if letter, ok := militaryTZ(off); ok {
			return letter
		}
	}
	// The separator between hours and minutes is whatever the picture writes
	// between its two digit groups, and the digits themselves come from the
	// picture's family: "[Z00~00]" separates with "~", and "[Z\u0660\u0660:\u0660\u0660]"
	// writes Arabic-Indic digits.
	sep, zero := tzPictureShape(pres)
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
		if tzHourDigits(pres) == 1 && strings.HasPrefix(pres, "0") {
			return prefix + sign + translateDigits(fmt.Sprintf("%d%s%02d", hours, sep, mins), zero)
		}
		return prefix + sign + translateDigits(fmt.Sprintf("%02d%s%02d", hours, sep, mins), zero)
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
	return prefix + sign + translateDigits(hs, zero)
}

// tzHourDigits counts the digits of the hours group of a timezone picture,
// which is the run before the separator: "[Z0:01]" asks for one, so the hours
// are written unpadded.
//
// The caller pairs this with a check that the group actually starts with the
// zero-digit. A picture with no digits at all — the bare "[Z]" — takes the
// component's default presentation and keeps the padded form; counting its
// zero digits as "one" made every default picture unpad its hours.
func tzHourDigits(pres string) int {
	n := 0
	for _, r := range pres {
		if unicode.IsDigit(r) {
			n++
			continue
		}
		if n > 0 {
			return n
		}
	}
	return n
}

// militaryTZ returns the single-letter military designation of a whole-hour
// offset, as "[ZZ]" asks for.
//
// The eastern offsets take A..I then K..M — J is skipped, and names the
// absence of a timezone — and the western take N..Y. UTC is Z.
func militaryTZ(off int) (string, bool) {
	if off%60 != 0 {
		return "", false
	}
	h := off / 60
	switch {
	case h == 0:
		return "Z", true
	case h >= 1 && h <= 9:
		return string(rune('A' + h - 1)), true
	case h >= 10 && h <= 12:
		// K is 10: 'A'+9 is 'J', which is skipped.
		return string(rune('A' + h)), true
	case h <= -1 && h >= -12:
		return string(rune('N' - h - 1)), true
	}
	return "", false
}

// tzPictureShape reads the separator and digit family a timezone picture asks
// for.
//
// A picture of two digit groups separates them with whatever stands between:
// "[Z00~00]" wants "~" where the default is ":". The family is that of the
// first digit, so a picture written in Arabic-Indic digits produces them.
func tzPictureShape(pres string) (sep string, zero rune) {
	sep, zero = ":", '0'
	runes := []rune(pres)
	start := -1
	for i, r := range runes {
		if unicode.IsDigit(r) {
			if start < 0 {
				start = i
				zero = r - rune(digitValueOf(r))
			}
			continue
		}
		// The first non-digit after a run of digits is the separator, when
		// more digits follow it.
		if start >= 0 {
			for _, later := range runes[i:] {
				if unicode.IsDigit(later) {
					return string(r), zero
				}
			}
			return sep, zero
		}
	}
	return sep, zero
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
	// fn:parse-xml and fn:parse-xml-fragment are XPath 3.0 functions, so the
	// builtin library has them marked Since XPath30. The harness registers
	// them unversioned because it writes its own assertions in the 3.0
	// language while the subject may be 2.0.
	registerParseXML(l, XPath20)
}

// registerParseXML adds fn:parse-xml and fn:parse-xml-fragment.
func registerParseXML(l *Library, since Version) {
	// fn:parse-xml builds a document node from a string of XML. The result is
	// a document, not the element: the spec returns document-node(), and the
	// callers that matter (an environment's initial context item) navigate
	// from the root.
	l.registerFnSince(since, "parse-xml", []int{1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
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

	// fn:parse-xml-fragment parses an external general parsed entity: a
	// sequence of top-level nodes rather than one document element. The
	// result is still a document node, but its children may be several
	// elements, or none.
	//
	// It is implemented by wrapping the fragment in a synthetic element and
	// lifting that element's children out, which is what makes a fragment
	// with two top-level elements parse at all.
	l.registerFnSince(since, "parse-xml-fragment", []int{1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		if len(args) > 0 && len(args[0]) == 0 {
			return xdm.Empty(), nil
		}
		s, err := argStringRequired(args, 0)
		if err != nil {
			return nil, err
		}
		frag, perr := parseXMLFragment(s, ctx.StaticBaseURI)
		if perr != nil {
			return nil, perr
		}
		return xdm.One(frag), nil
	})
}

// parseXMLFragment parses a string as an external general parsed entity.
//
// The declaration, if any, is an XML *text* declaration rather than an XML
// declaration: it is stripped before wrapping, since it may not appear inside
// an element.
func parseXMLFragment(s, base string) (*xdm.Node, error) {
	body := s
	if strings.HasPrefix(body, "<?xml") {
		end := strings.Index(body, "?>")
		if end < 0 {
			return nil, xdm.Errorf("FODC0006",
				"fn:parse-xml-fragment: unterminated text declaration")
		}
		decl := body[:end]
		// This is a text declaration, not an XML declaration: the two differ
		// in that "encoding" is mandatory here and "standalone" is forbidden.
		// A fragment carrying an XML declaration is therefore not a
		// well-formed external entity.
		if !strings.Contains(decl, "encoding") {
			return nil, xdm.Errorf("FODC0006",
				"fn:parse-xml-fragment: a text declaration must specify an encoding")
		}
		if strings.Contains(decl, "standalone") {
			return nil, xdm.Errorf("FODC0006",
				"fn:parse-xml-fragment: a text declaration may not specify standalone")
		}
		body = body[end+2:]
	}
	// The wrapper name must be a legal XML name — a control character is not,
	// and made every fragment fail to parse. It is chosen to be one no
	// fragment would use, and it never appears in the result: its children are
	// lifted onto the document node below.
	tree, err := xdm.ParseString("<parse-xml-fragment-wrapper>"+body+"</parse-xml-fragment-wrapper>", xdm.ParseOptions{
		AllowDOCTYPE: true,
		BaseURI:      base,
	})
	if err != nil {
		return nil, xdm.Errorf("FODC0006",
			"fn:parse-xml-fragment: argument is not a well-formed XML fragment: %v", err)
	}
	// Lift the wrapper's children onto the document node, so the result is a
	// document whose children are the fragment's top-level nodes.
	root := tree.Root
	var wrapper *xdm.Node
	for _, c := range root.Children {
		if c.Kind == xdm.KindElement {
			wrapper = c
			break
		}
	}
	if wrapper == nil {
		return root, nil
	}
	root.Children = wrapper.Children
	for _, c := range root.Children {
		c.Parent = root
	}
	return root, nil
}
