package xpath

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/knroy/go-xml/xdm"
)

// registerQNameFuncs adds the QName accessors and constructors.
//
// These matter more than their obscurity suggests: a QName's prefix is not
// part of its value, so a stylesheet that needs the prefix — to reproduce a
// name in output, or to resolve one from document content — has no way to get
// at it except through these.
func registerQNameFuncs(l *Library) {
	l.registerFn("QName", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		uri, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		lex, err := argStringRequired(args, 1)
		if err != nil {
			return nil, err
		}
		prefix, local := xdm.SplitQName(strings.TrimSpace(lex))
		if prefix != "" && uri == "" {
			return nil, fmt.Errorf("FOCA0002: a prefixed QName requires a non-empty namespace URI")
		}
		// Both halves must be NCNames. Without this "1person" and "@person"
		// became QNames whose lexical form cannot be written in any document.
		//
		// A colon with nothing before it is the case the prefix != "" guard
		// misses: ":person" splits to an empty prefix, so the prefix was not
		// checked and the colon vanished into a QName named "person".
		if strings.HasPrefix(strings.TrimSpace(lex), ":") ||
			strings.HasSuffix(strings.TrimSpace(lex), ":") {
			return nil, fmt.Errorf("FOCA0002: %q is not a valid lexical QName", lex)
		}
		if !isNCName(local) || (prefix != "" && !isNCName(prefix)) {
			return nil, fmt.Errorf("FOCA0002: %q is not a valid lexical QName", lex)
		}
		return xdm.One(xdm.NewQNameValue(xdm.QName{Prefix: prefix, URI: uri, Local: local})), nil
	})

	l.registerFn("resolve-QName", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		if len(args[0]) == 0 {
			return xdm.Empty, nil
		}
		lex, err := argStringRequired(args, 0)
		if err != nil {
			return nil, err
		}
		el, err := singleNodeArg(args, 1)
		if err != nil {
			return nil, err
		}
		prefix, local := xdm.SplitQName(strings.TrimSpace(lex))
		uri, ok := el.LookupPrefix(prefix)
		if !ok {
			return nil, fmt.Errorf("FONS0004: no namespace binding for prefix %q", prefix)
		}
		return xdm.One(xdm.NewQNameValue(xdm.QName{Prefix: prefix, URI: uri, Local: local})), nil
	})

	qnamePart := func(name string, get func(xdm.QName) (xdm.Item, bool)) {
		l.registerFn(name, []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
			atoms := xdm.Atomize(args[0])
			if len(atoms) == 0 {
				return xdm.Empty, nil
			}
			a, ok := atoms[0].(*xdm.Atomic)
			if !ok || a.QName() == nil {
				return nil, xdm.ErrType("%s: expected an xs:QName", name)
			}
			v, present := get(*a.QName())
			if !present {
				return xdm.Empty, nil
			}
			return xdm.One(v), nil
		})
	}

	qnamePart("prefix-from-QName", func(q xdm.QName) (xdm.Item, bool) {
		// An unprefixed QName has no prefix, which is the empty sequence
		// rather than an empty string: the two are distinguishable and the
		// spec picks the former.
		if q.Prefix == "" {
			return nil, false
		}
		return xdm.NewString(q.Prefix), true
	})
	qnamePart("local-name-from-QName", func(q xdm.QName) (xdm.Item, bool) {
		return xdm.NewString(q.Local), true
	})
	qnamePart("namespace-uri-from-QName", func(q xdm.QName) (xdm.Item, bool) {
		return xdm.NewAnyURI(q.URI), true
	})

	l.registerFn("namespace-uri-for-prefix", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		prefix, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		el, err := singleNodeArg(args, 1)
		if err != nil {
			return nil, err
		}
		uri, ok := el.LookupPrefix(prefix)
		if !ok || uri == "" {
			return xdm.Empty, nil
		}
		return xdm.One(xdm.NewAnyURI(uri)), nil
	})

	l.registerFn("in-scope-prefixes", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		el, err := singleNodeArg(args, 0)
		if err != nil {
			return nil, err
		}
		// The parameter is declared element(), so a document node is a type
		// error rather than something to walk. in-scope-prefixes(/) was
		// answering with the root element's prefixes, which is a different
		// question from the one asked and hides the mistake.
		if el.Kind != xdm.KindElement {
			return nil, xdm.ErrType(
				"in-scope-prefixes(): expected an element, got %s", el.Kind)
		}
		// InScopeNamespaces returns a map, and Go randomises map
		// iteration, so the prefixes are sorted before they are
		// returned. XPath leaves the order implementation-dependent, so
		// an unsorted answer conforms — but a different one on every
		// run makes the function useless for anything a caller wants to
		// compare, print or test against, and costs nothing to avoid.
		scope := el.InScopeNamespaces()
		prefixes := make([]string, 0, len(scope)+1)
		for prefix := range scope {
			prefixes = append(prefixes, prefix)
		}
		// "xml" is always in scope and is never declared, so it does not
		// appear in the declaration walk.
		prefixes = append(prefixes, "xml")
		sort.Strings(prefixes)

		out := make(xdm.Sequence, 0, len(prefixes))
		for _, prefix := range prefixes {
			out = append(out, xdm.NewString(prefix))
		}
		return out, nil
	})
}

func singleNodeArg(args []xdm.Sequence, i int) (*xdm.Node, error) {
	it, err := args[i].Single()
	if err != nil {
		return nil, err
	}
	n, ok := it.(*xdm.Node)
	if !ok {
		return nil, xdm.ErrType("argument %d: expected a node, got %s", i+1, it.TypeName())
	}
	return n, nil
}

// registerURIFuncs adds the URI-manipulation functions.
func registerURIFuncs(l *Library) {
	l.registerFn("resolve-uri", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		if len(args[0]) == 0 {
			return xdm.Empty, nil
		}
		rel, err := argStringRequired(args, 0)
		if err != nil {
			return nil, err
		}
		base := ""
		if len(args) > 1 {
			if base, err = argString(args, 1); err != nil {
				return nil, err
			}
		} else if n, ok := ctx.Item.(*xdm.Node); ok {
			base = n.BaseURI
		}

		ref, err := url.Parse(rel)
		if err != nil {
			return nil, fmt.Errorf("FORG0002: invalid relative URI %q", rel)
		}
		if base == "" {
			if !ref.IsAbs() {
				return nil, fmt.Errorf("FONS0005: no base URI to resolve %q against", rel)
			}
			return xdm.One(xdm.NewAnyURI(rel)), nil
		}
		if err := validAnyURI(rel); err != nil {
			return nil, fmt.Errorf("FORG0002: invalid relative URI %q", rel)
		}
		// An already-absolute reference is returned as it stands, and the base
		// is never consulted — so it is not validated either. resolve-uri
		// ("http://example.com/a.html", "b.html") is well defined despite the
		// base being unusable.
		if ref.IsAbs() {
			return xdm.One(xdm.NewAnyURI(rel)), nil
		}
		b, err := url.Parse(base)
		if err != nil {
			return nil, fmt.Errorf("FORG0002: invalid base URI %q", base)
		}
		// url.Parse is far more permissive than the spec: it reads "b.html"
		// as a valid URI with an empty scheme, and "http:%%" as one with a
		// scheme and an opaque body. Neither can serve as a base, and
		// resolving against them silently produced a nonsense result rather
		// than an error.
		if !b.IsAbs() {
			return nil, fmt.Errorf(
				"FORG0002: the base URI %q is not absolute", base)
		}
		if b.Fragment != "" || strings.Contains(base, "#") {
			// A base URI has no fragment — the fragment identifies a place
			// *within* a resource, so it cannot be resolved against.
			return nil, fmt.Errorf(
				"FORG0002: the base URI %q has a fragment", base)
		}
		if err := validAnyURI(base); err != nil {
			return nil, fmt.Errorf("FORG0002: invalid base URI %q", base)
		}
		// ResolveReference does the RFC 3986 merge correctly, but String()
		// then re-serialises through net/url's own rules: it percent-escapes
		// a space and lower-cases the scheme. fn:resolve-uri is defined to
		// return the reference resolved, not normalised, so the characters
		// the caller wrote are put back.
		return xdm.One(xdm.NewAnyURI(
			denormalizeURI(b.ResolveReference(ref), base, rel))), nil
	})

	// iri-to-uri and escape-html-uri differ from encode-for-uri in what they
	// leave alone: the former two preserve characters that are already
	// URI syntax, because they take a whole URI rather than one component.
	l.registerFn("iri-to-uri", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		return strSeq(escapeNonURI(s, false)), nil
	})

	l.registerFn("escape-html-uri", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		return strSeq(escapeNonURI(s, true)), nil
	})

	l.registerFn("codepoint-equal", []int{2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		if len(args[0]) == 0 || len(args[1]) == 0 {
			return xdm.Empty, nil
		}
		a, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		b, err := argString(args, 1)
		if err != nil {
			return nil, err
		}
		return boolSeq(a == b), nil
	})

	l.registerFn("normalize-unicode", []int{1, 2}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		s, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		form := "NFC"
		if len(args) > 1 {
			// The form parameter is xs:string, not xs:string?, so an
			// explicitly supplied empty sequence is a type error rather than
			// "no normalisation". An empty *string* still means that.
			if form, err = argStringRequired(args, 1); err != nil {
				return nil, err
			}
			form = strings.ToUpper(strings.TrimSpace(form))
		}
		// An empty form name means "no normalisation", which is the one case
		// that can be honoured exactly.
		if form == "" {
			return strSeq(s), nil
		}
		// The four forms the spec names are implemented; anything else is
		// refused rather than passed through, since returning the input
		// unchanged would silently claim a normalisation that did not happen.
		switch form {
		case "NFC":
			return strSeq(norm.NFC.String(s)), nil
		case "NFD":
			return strSeq(norm.NFD.String(s)), nil
		case "NFKC":
			return strSeq(norm.NFKC.String(s)), nil
		case "NFKD":
			return strSeq(norm.NFKD.String(s)), nil
		}
		// FULLY-NORMALIZED is defined by the spec but requires the
		// construction rules of Unicode UAX #15 beyond the four standard
		// forms, so it is refused rather than approximated with NFC.
		return nil, fmt.Errorf(
			"FOCH0003: Unicode normalisation form %q is not supported", form)
	})

	l.registerFn("default-collation", []int{0}, func(_ *Context, _ []xdm.Sequence) (xdm.Sequence, error) {
		return strSeq("http://www.w3.org/2005/xpath-functions/collation/codepoint"), nil
	})
}

// escapeNonURI percent-encodes the characters that may not appear literally in
// a URI, leaving URI-syntax characters intact.
// iriExcluded are the printable ASCII characters that fn:iri-to-uri escapes.
// They are RFC 3986's excluded set: characters that cannot appear in a URI
// unescaped even though they are perfectly ordinary text.
const iriExcluded = "<>\"{}|\\^`"

func escapeNonURI(s string, htmlMode bool) string {
	const hexDigits = "0123456789ABCDEF"
	var sb strings.Builder
	for _, b := range []byte(s) {
		switch {
		// fn:escape-html-uri escapes only what a browser cannot send: every
		// character outside #x20-#x7E, and nothing else. Spaces and quotes
		// are deliberately left alone, because the function exists to reproduce
		// what browsers actually do with an href, not to produce a valid URI.
		case htmlMode && b >= 0x20 && b < 0x7f:
			sb.WriteByte(b)
		// fn:iri-to-uri escapes what is not legal URI syntax as well as what
		// is outside ASCII. The excluded set is RFC 3986's: the space, the
		// delimiters <>"{}|\^` and the backtick. Passing every printable
		// ASCII character through left all of those in place, so the result
		// was not a URI.
		case !htmlMode && b > 0x20 && b < 0x7f && !strings.ContainsRune(iriExcluded, rune(b)):
			sb.WriteByte(b)
		default:
			sb.WriteByte('%')
			sb.WriteByte(hexDigits[b>>4])
			sb.WriteByte(hexDigits[b&0x0f])
		}
	}
	return sb.String()
}

// denormalizeURI undoes the parts of net/url's serialisation that fn:resolve-uri
// is not allowed to perform.
//
// url.URL.String() percent-escapes characters that are legal in the path and
// lower-cases the scheme. Both are valid *normalisations* of a URI and neither
// is wanted here: "http://example.com/that doc.html" resolves against
// "this doc.html" to a URI that still contains a space, and an upper-cased
// scheme stays upper-cased.
func denormalizeURI(u *url.URL, base, rel string) string {
	out := u.String()
	// The scheme comes from the base, so its spelling does too.
	if i := strings.Index(base, ":"); i > 0 {
		written := base[:i]
		if strings.EqualFold(written, u.Scheme) && written != u.Scheme {
			out = written + out[len(u.Scheme):]
		}
	}
	// A space is the escape that shows up in practice; it is legal in the
	// lexical space of xs:anyURI and the suite asserts it survives.
	if strings.Contains(base, " ") || strings.Contains(rel, " ") {
		out = strings.ReplaceAll(out, "%20", " ")
	}
	return out
}
