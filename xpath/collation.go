package xpath

import (
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/knroy/go-xml/xdm"
)

// CodepointCollation is the one collation this implementation provides. It is
// the only collation the spec requires every processor to support, and it
// compares strings by Unicode codepoint.
const CodepointCollation = "http://www.w3.org/2005/xpath-functions/collation/codepoint"

// checkCollationArg validates the trailing collation argument that ten F&O
// functions accept — fn:compare, fn:contains, fn:min and the rest.
//
// A language-sensitive collation would order accented and cased letters by the
// conventions of that language. Only codepoint order is implemented, so naming
// any other collation is an error rather than a silent fallback: a stylesheet
// that asks for Swedish ordering and quietly receives ASCII ordering produces a
// wrongly-ordered result with nothing to indicate it happened. This mirrors how
// xsl:sort/@collation is handled at compile time.
//
// The argument is positional, so i is the index the caller reserves for it;
// a call made at the shorter arity simply has no argument there.
func checkCollationArg(fn string, args []xdm.Sequence, i int) error {
	if i >= len(args) {
		return nil
	}
	// The parameter is declared xs:string, not xs:string?, so an explicitly
	// supplied empty sequence is a type error rather than "use the default":
	// index-of((1,2,3), 1, ()) is XPTY0004. Omitting the argument is fine.
	uri, err := argStringRequired(args, i)
	if err != nil {
		return err
	}
	_, err = ResolveCollation(uri)
	if err != nil {
		return fmt.Errorf("fn:%s: %w", fn, err)
	}
	return nil
}

// HTMLASCIICaseInsensitive is the collation that compares ASCII letters
// without regard to case. It is defined by the spec and is the one
// non-codepoint collation that needs no locale data.
const HTMLASCIICaseInsensitive = "http://www.w3.org/2005/xpath-functions/collation/html-ascii-case-insensitive"

// Collation compares two strings. Only the operations XPath actually needs are
// exposed, because a general Compare is not enough: fn:contains under a
// case-insensitive collation is not "compare the folded strings", it is
// "does the folded needle occur in the folded haystack".
type Collation interface {
	Compare(a, b string) int
	Contains(s, sub string) bool
	StartsWith(s, prefix string) bool
	EndsWith(s, suffix string) bool
	IndexOf(s, sub string) int
}

type codepointCollation struct{}

func (codepointCollation) Compare(a, b string) int     { return strings.Compare(a, b) }
func (codepointCollation) Contains(s, sub string) bool { return strings.Contains(s, sub) }
func (codepointCollation) StartsWith(s, p string) bool { return strings.HasPrefix(s, p) }
func (codepointCollation) EndsWith(s, suf string) bool { return strings.HasSuffix(s, suf) }
func (codepointCollation) IndexOf(s, sub string) int   { return strings.Index(s, sub) }

// Key returns a string that is identical for values this collation calls
// equal, which is what lets a caller group or index by collation without
// comparing every pair. For codepoint the value is its own key.
func (codepointCollation) Key(s string) string { return s }

// asciiCaseInsensitive folds only A-Z, deliberately: the spec defines this
// collation over ASCII, so folding Unicode case as well would make it a
// different, locale-sensitive comparison.
type asciiCaseInsensitive struct{}

func asciiFold(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

func (asciiCaseInsensitive) Compare(a, b string) int {
	return strings.Compare(asciiFold(a), asciiFold(b))
}
func (asciiCaseInsensitive) Contains(s, sub string) bool {
	return strings.Contains(asciiFold(s), asciiFold(sub))
}
func (asciiCaseInsensitive) StartsWith(s, p string) bool {
	return strings.HasPrefix(asciiFold(s), asciiFold(p))
}
func (asciiCaseInsensitive) EndsWith(s, suf string) bool {
	return strings.HasSuffix(asciiFold(s), asciiFold(suf))
}

// Key folds the ASCII case, so "THOUGH" and "though" index together.
func (asciiCaseInsensitive) Key(s string) string { return asciiFold(s) }

func (asciiCaseInsensitive) IndexOf(s, sub string) int {
	return strings.Index(asciiFold(s), asciiFold(sub))
}

// ResolveCollation returns the collation a URI names.
//
// A relative URI is accepted when its tail matches a known collation, because
// the QT3 suite and some stylesheets write "collation/codepoint" rather than
// the full URI. Resolving it against the static base URI would be more correct
// still, but the base is not threaded into every function that takes a
// collation argument, and matching the tail covers the forms that occur.
func ResolveCollation(uri string) (Collation, error) {
	uri = strings.TrimSpace(uri)
	switch {
	case uri == "" || uri == CodepointCollation:
		return codepointCollation{}, nil
	case uri == HTMLASCIICaseInsensitive:
		return asciiCaseInsensitive{}, nil
	}
	// A host application may define its own. The spec leaves the set of
	// supported collations implementation-defined beyond the two above, and
	// without this an embedder had no way to add one. A registration wins over
	// the built-in UCA support below, which is what makes "override" mean
	// something.
	if c, ok := lookupRegisteredCollation(uri); ok {
		return c, nil
	}
	// The UCA collation URI family. It is checked after the registered table
	// so a host can substitute its own implementation, and before the
	// relative-form fallbacks because those match on a URI tail.
	if strings.HasPrefix(uri, UCACollation) {
		c, err := parseUCACollation(uri)
		if err != nil {
			return nil, fmt.Errorf("FOCH0002: collation %q is not supported: %w", uri, err)
		}
		return c, nil
	}
	// Tolerate the relative forms.
	if strings.HasSuffix(CodepointCollation, "/"+strings.TrimPrefix(uri, "/")) {
		return codepointCollation{}, nil
	}
	if strings.HasSuffix(HTMLASCIICaseInsensitive, "/"+strings.TrimPrefix(uri, "/")) {
		return asciiCaseInsensitive{}, nil
	}
	return nil, fmt.Errorf(
		"FOCH0002: collation %q is not supported: only codepoint, "+
			"html-ascii-case-insensitive and the UCA collation URI family "+
			"are implemented", uri)
}

// collationArg validates the trailing collation argument and returns the
// collation it names, so a caller can actually use it rather than only
// checking that it is supported.
func collationArg(fn string, args []xdm.Sequence, i int) (Collation, error) {
	return collationArgCtx(nil, fn, args, i)
}

// collationArgCtx is collationArg with the context supplying the default.
//
// Omitting the collation argument does not mean the codepoint collation: it
// means the default collation from the static context, which
// [xsl:]default-collation sets. Hard-coding codepoint here made
// default-collation affect only the functions that read it directly, so
// fn:contains and fn:compare kept comparing by codepoint inside a stylesheet
// that had asked for a case-insensitive default.
func collationArgCtx(ctx *Context, fn string, args []xdm.Sequence, i int) (Collation, error) {
	if i >= len(args) {
		if ctx != nil && ctx.collation != nil {
			return ctx.collation, nil
		}
		return codepointCollation{}, nil
	}
	// The parameter is declared xs:string, not xs:string?, so an explicitly
	// supplied empty sequence is a type error rather than "use the default":
	// index-of((1,2,3), 1, ()) is XPTY0004. Omitting the argument is fine,
	// which is the branch above.
	uri, err := argStringRequired(args, i)
	if err != nil {
		return nil, err
	}
	c, err := ResolveCollation(uri)
	if err != nil {
		return nil, fmt.Errorf("fn:%s: %w", fn, err)
	}
	return c, nil
}

// RegisterCollation makes a collation available under a URI.
//
// The spec requires exactly two collations — codepoint and the HTML ASCII
// case-insensitive one — and leaves the rest implementation-defined. This is
// how a host application supplies its own: a locale-aware comparison, or the
// case-blind collation the W3C test catalog defines for its own use.
//
// Registering a URI that is already known replaces it, which is what makes a
// host override sensible. Registration is expected during setup, before any
// evaluation; it is guarded so that a late one cannot race a lookup, but it is
// not a way to change collations while expressions are running.
func RegisterCollation(uri string, c Collation) {
	collationMu.Lock()
	defer collationMu.Unlock()
	if registeredCollations == nil {
		registeredCollations = map[string]Collation{}
	}
	registeredCollations[uri] = c
}

func lookupRegisteredCollation(uri string) (Collation, bool) {
	collationMu.RLock()
	defer collationMu.RUnlock()
	c, ok := registeredCollations[uri]
	return c, ok
}

var (
	collationMu          sync.RWMutex
	registeredCollations map[string]Collation
)

// UCACollation is the URI family defined by XSLT 3.0 section 5.3.3 and F&O
// 5.3.4 for the Unicode Collation Algorithm. The base URI selects the root
// (DUCET) collation; query parameters tailor it.
const UCACollation = "http://www.w3.org/2013/collation/UCA"

// ucaCollation is a UCA collation at a chosen strength and locale.
//
// It is a real Unicode Collation Algorithm implementation, not a fold: the
// weights come from golang.org/x/text/collate, which carries the CLDR tables.
// That distinction matters because an approximation would get equality right
// and ordering silently wrong, and a sort that quietly misorders is
// undetectable to the caller. Every ordering this type reports is the one the
// UCA specifies for the parameters it accepted; the parameters it cannot
// honour are refused in parseUCACollation rather than ignored.
//
// The collate.Collator it wraps reuses an internal buffer, so it is not safe
// for concurrent use. CompareString does not touch that buffer, but Key does,
// so the mutex guards the whole type rather than only the key path.
type ucaCollation struct {
	mu sync.Mutex
	c  *collate.Collator
	// buf is reused across Key calls, which is what the mutex protects.
	buf collate.Buffer
}

func (u *ucaCollation) Compare(a, b string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.c.CompareString(a, b)
}

// Key returns the UCA sort key. Two strings the collation calls equal produce
// the same key, which is what lets fn:distinct-values and xsl:for-each-group
// index by collation rather than compare every pair.
func (u *ucaCollation) Key(s string) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	k := u.c.KeyFromString(&u.buf, s)
	out := string(k)
	u.buf.Reset()
	return out
}

// The substring operations are defined over collation units. Implementing them
// properly needs the collation element iterator, which x/text does not expose,
// so a naive scan over byte offsets would answer questions like
// "does 'straße' contain 'ss'" wrongly under a collation that says it does.
//
// Rather than guess, these search for a substring whose collation key matches,
// scanning candidate ranges by rune boundary. That is correct for the
// contiguous case, which is what every use of fn:contains under a collation
// actually asks, and it never reports a match the collation would deny.
func (u *ucaCollation) matchAt(s string, start int, sub string) (int, bool) {
	// An empty needle matches at once, as it does for every collation.
	if sub == "" {
		return start, true
	}
	for end := start; end <= len(s); end++ {
		if end < len(s) && !utf8.RuneStart(s[end]) {
			continue
		}
		if end == start {
			continue
		}
		if u.Compare(s[start:end], sub) == 0 {
			return end, true
		}
	}
	return 0, false
}

func (u *ucaCollation) IndexOf(s, sub string) int {
	for start := 0; start <= len(s); start++ {
		if start < len(s) && !utf8.RuneStart(s[start]) {
			continue
		}
		if _, ok := u.matchAt(s, start, sub); ok {
			return start
		}
	}
	return -1
}

func (u *ucaCollation) Contains(s, sub string) bool { return u.IndexOf(s, sub) >= 0 }

func (u *ucaCollation) StartsWith(s, prefix string) bool {
	_, ok := u.matchAt(s, 0, prefix)
	return ok
}

func (u *ucaCollation) EndsWith(s, suffix string) bool {
	if suffix == "" {
		return true
	}
	for start := 0; start <= len(s); start++ {
		if start < len(s) && !utf8.RuneStart(s[start]) {
			continue
		}
		if u.Compare(s[start:], suffix) == 0 {
			return true
		}
	}
	return false
}

// parseUCACollation builds the collation a UCA URI names, or reports why it
// cannot.
//
// XSLT 3.0 section 5.3.3 defines the parameters and the fallback rule: with
// fallback=yes (the default) a processor may ignore a parameter it does not
// support, and with fallback=no it must reject the URI. This implementation is
// stricter than the fallback rule permits for the parameters that change the
// *order* — caseFirst, alternate, reorder, maxVariable — because honouring the
// rule there would mean returning a collation that answers ordering questions
// wrongly with nothing to indicate it. Parameters that only affect which
// strings compare equal, and which this implementation does support, are
// applied; the rest are refused whatever fallback says.
func parseUCACollation(uri string) (Collation, error) {
	rest := strings.TrimPrefix(uri, UCACollation)
	if rest != "" && !strings.HasPrefix(rest, "?") {
		return nil, fmt.Errorf("not a UCA collation URI")
	}
	rest = strings.TrimPrefix(rest, "?")

	lang := ""
	strength := "tertiary"
	numeric := false
	fallback := true
	var unsupported []string

	if rest != "" {
		// The parameters are separated by ";" in the form the specification
		// gives, but "&" is the ordinary URI query separator and appears in
		// the wild, so both are accepted.
		for _, kv := range strings.FieldsFunc(rest, func(r rune) bool {
			return r == ';' || r == '&'
		}) {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return nil, fmt.Errorf("UCA parameter %q has no value", kv)
			}
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			switch k {
			case "lang":
				lang = v
			case "strength":
				switch v {
				case "primary", "1":
					strength = "primary"
				case "secondary", "2":
					strength = "secondary"
				case "tertiary", "3":
					strength = "tertiary"
				case "quaternary", "4", "identical", "5":
					// x/text has no separate quaternary level; identical
					// ordering is what Force gives on top of tertiary.
					strength = "identical"
				default:
					return nil, fmt.Errorf("UCA strength=%q is not one of primary, secondary, tertiary, quaternary, identical", v)
				}
			case "numeric":
				numeric = v == "yes"
			case "fallback":
				switch v {
				case "yes":
					fallback = true
				case "no":
					fallback = false
				default:
					return nil, fmt.Errorf("UCA fallback=%q is not yes or no", v)
				}
			case "normalization":
				// x/text normalises as the UCA requires, so normalization=yes
				// is what it already does and normalization=no cannot be
				// honoured — but the difference only shows for input that is
				// not in NFD, and asking for less normalisation never changes
				// which strings this collation calls equal for input that is.
				if v != "yes" && v != "no" {
					return nil, fmt.Errorf("UCA normalization=%q is not yes or no", v)
				}
			case "caseLevel":
				// caseLevel=yes inserts a case level between secondary and
				// tertiary. At tertiary strength case already distinguishes,
				// so the request is satisfied; at a lower strength it is not.
				if v == "yes" && strength != "tertiary" && strength != "identical" {
					unsupported = append(unsupported, k)
				}
			case "caseFirst", "alternate", "maxVariable", "reorder", "backwards", "version":
				// These change the ordering, and x/text exposes no way to ask
				// for them. Ignoring one would produce a wrong order silently.
				unsupported = append(unsupported, k)
			default:
				unsupported = append(unsupported, k)
			}
		}
	}

	if len(unsupported) > 0 {
		// Reported whatever fallback says: see the doc comment. The message
		// names the parameters so the caller can tell a tailoring this
		// implementation lacks from a URI it did not understand at all.
		return nil, fmt.Errorf(
			"UCA collation parameter(s) %s change the collation order and are "+
				"not implemented", strings.Join(unsupported, ", "))
	}

	tag := language.Und
	if lang != "" {
		t, err := language.Parse(lang)
		if err != nil {
			if !fallback {
				return nil, fmt.Errorf("UCA lang=%q is not a valid language tag: %w", lang, err)
			}
			t = language.Und
		}
		// A tag with no collation data falls back to the root collation. With
		// fallback=no that is exactly the case the parameter exists to
		// forbid; with fallback=yes it is what the specification permits, and
		// the root collation is a real UCA ordering rather than a guess.
		if _, _, conf := ucaMatcher.Match(t); conf == language.No && !fallback {
			return nil, fmt.Errorf("UCA lang=%q: no collation data for that language", lang)
		}
		tag = t
	}

	var opts []collate.Option
	switch strength {
	case "primary":
		// Primary strength ignores both case and diacritics: only base
		// letters distinguish.
		opts = append(opts, collate.IgnoreDiacritics, collate.IgnoreCase)
	case "secondary":
		// Secondary strength keeps diacritics and ignores case.
		opts = append(opts, collate.IgnoreCase)
	case "identical":
		opts = append(opts, collate.Force)
	}
	if numeric {
		opts = append(opts, collate.Numeric)
	}
	return &ucaCollation{c: collate.New(tag, opts...)}, nil
}

// ucaMatcher is built once: constructing a matcher walks the full list of
// supported tags, which is wasted work on every collation lookup.
var ucaMatcher = language.NewMatcher(collate.Supported())
