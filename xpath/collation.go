package xpath

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"

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

// collationMatchRange locates the substring of s that the collation says
// equals sub, and returns its byte range.
//
// It exists because fn:substring-before and fn:substring-after need the *end*
// of the match, not only its start, and under a collation the match need not
// be len(sub) bytes long: with alternate=blanked the needle "--d-e-" matches
// the three bytes "d-e" of "abc--d-e-fghi", so the caller cannot recover the
// end by adding the needle's own length. Doing that produced substrings that
// were merely wrong for most inputs and panicked when the needle was longer
// than the span it matched.
//
// A collation that offers no MatchRange is one whose equality is codepoint
// equality as far as this package can tell, so the match is exactly as long as
// the needle.
func collationMatchRange(c Collation, s, sub string) (start, end int, ok bool) {
	if m, is := c.(interface {
		MatchRange(s, sub string) (int, int, bool)
	}); is {
		return m.MatchRange(s, sub)
	}
	i := c.IndexOf(s, sub)
	if i < 0 {
		return 0, 0, false
	}
	return i, i + len(sub), true
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
		c, err := ucaCollationFor(uri)
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

// ucaCollation is a UCA collation at a chosen strength, locale and tailoring.
//
// It is a real Unicode Collation Algorithm implementation, not a fold: the
// weights come from golang.org/x/text/collate, which carries the CLDR tables.
// That distinction matters because an approximation would get equality right
// and ordering silently wrong, and a sort that quietly misorders is
// undetectable to the caller.
//
// Everything is decided by comparing sort keys rather than by
// collate.CompareString. Two reasons. First, Key and Compare must agree:
// fn:collation-key promises that comparing keys orders the same way the
// collation does, and collation-key("abc",C) lt collation-key("ABC",C) is a
// test case, so deriving both from one function is the only way to be sure.
// Second, x/text's streaming comparison disagrees with its own key for
// alternate=blanked — CompareString("-", "") reports 1 where the keys are both
// empty — so the key is the trustworthy of the two.
//
// The collate.Collator reuses an internal buffer for key generation, so it is
// not safe for concurrent use; the mutex guards it.
type ucaCollation struct {
	mu sync.Mutex
	// base carries the primary and secondary levels, and the tertiary level
	// too unless caseFirst is in play — see caseFirst below.
	base *collate.Collator
	// tertiary is nil unless caseFirst is set. When it is set, base has case
	// folded away so that this implementation can insert its own case level,
	// and tertiary is the untailored collator whose key breaks the remaining
	// ties that are tertiary but not about case.
	tertiary *collate.Collator
	// caseFirst is "", "lower" or "upper". x/text has no kf setting, so an
	// explicit request is honoured by comparing a case signature between the
	// secondary and tertiary levels rather than by tailoring the table.
	caseFirst string
	// stripMarks removes combining marks before keying. It is set only when
	// the case level is wanted at a strength that ignores diacritics, to work
	// around x/text emitting a case weight for each combining mark: the case
	// level of "DATABASE" and "DÃTABASE" then differs by the tilde's weight
	// even though neither string's *letters* differ in case. Dropping the
	// marks cannot change a weight at a strength that already ignores them.
	stripMarks bool
	// identical appends the NFD form of the string to the key, which is what
	// TR10 section 4.3 says strength=identical does. x/text's ks=identic does
	// not survive alternate=blanked — the blanked variable elements are
	// removed before the identity level is reached — and compare-042 asserts
	// that a blanked space is still significant at identical strength.
	identical bool

	buf collate.Buffer
}

// key builds the full sort key for s: the levels x/text produces, then the
// case level if caseFirst asked for one, then the tertiary tiebreak, then the
// identity level.
func (u *ucaCollation) key(s string) []byte {
	in := s
	if u.stripMarks {
		in = stripCombining(in)
	}
	u.mu.Lock()
	k := append([]byte(nil), u.base.KeyFromString(&u.buf, in)...)
	u.buf.Reset()
	if u.tertiary != nil {
		// The separator keeps a long case level from colliding with a short
		// one followed by a large tertiary weight.
		k = append(k, 0, 0)
		k = append(k, caseSignature(in, u.caseFirst == "upper")...)
		k = append(k, 0, 0)
		k = append(k, u.tertiary.KeyFromString(&u.buf, in)...)
		u.buf.Reset()
	}
	u.mu.Unlock()
	if u.identical {
		k = append(k, 0, 0, 0)
		k = append(k, norm.NFD.String(s)...)
	}
	return k
}

// caseSignature is one byte per cased rune, ordered so that the class named by
// caseFirst sorts first. Uncased runes contribute nothing, so "a-b" and "ab"
// have the same signature and the case level cannot override a punctuation
// difference that the primary level already decided.
func caseSignature(s string, upperFirst bool) []byte {
	first, second := byte(1), byte(2)
	if upperFirst {
		first, second = second, first
	}
	var out []byte
	for _, r := range norm.NFD.String(s) {
		switch {
		case unicode.IsUpper(r), unicode.IsTitle(r):
			out = append(out, second)
		case unicode.IsLower(r):
			out = append(out, first)
		}
	}
	return out
}

// stripCombining drops the non-spacing marks from the NFD form. See
// ucaCollation.stripMarks for why.
func stripCombining(s string) string {
	d := norm.NFD.String(s)
	if !strings.ContainsFunc(d, func(r rune) bool { return unicode.Is(unicode.Mn, r) }) {
		return d
	}
	out := make([]rune, 0, len(d))
	for _, r := range d {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

func (u *ucaCollation) Compare(a, b string) int {
	return bytes.Compare(u.key(a), u.key(b))
}

// Key returns the UCA sort key. Two strings the collation calls equal produce
// the same key, which is what lets fn:distinct-values and xsl:for-each-group
// index by collation rather than compare every pair.
func (u *ucaCollation) Key(s string) string { return string(u.key(s)) }

// The substring operations are defined over collation units. Implementing them
// properly needs the collation element iterator, which x/text does not expose,
// so a naive scan over byte offsets would answer questions like
// "does 'straße' contain 'ss'" wrongly under a collation that says it does.
//
// Rather than guess, these search for a substring whose collation key matches,
// scanning candidate ranges by rune boundary. That is correct for the
// contiguous case, which is what every use of fn:contains under a collation
// actually asks, and it never reports a match the collation would deny.
//
// The empty candidate is deliberately in the scan. Under alternate=blanked a
// needle of "--***-*---" has an empty key, so it matches everywhere — which is
// exactly what contains("Eureka!", "--***-*---") is specified to report — and
// a scan that skipped the zero-length range would miss it.
func (u *ucaCollation) matchAt(s string, start int, sub string) (int, bool) {
	if u.Compare("", sub) == 0 {
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

// MatchRange reports the span of s that equals sub, as the smallest span at
// the earliest position where a match exists.
//
// "Smallest" matters at both ends when characters are ignorable. Under
// alternate=blanked the needle "--d-e-" has the key "de", so in
// "abc--d-e-fghi" every span from "--d-e" through "--d-e-" compares equal to
// it, and so does the shorter "d-e" starting two bytes later. The spec's own
// examples pin the answer down: substring-before there is "abc--" and
// substring-after is "-fghi", which is the "d-e" span. Reporting the first
// start that matched instead gave "abc" and "-fghi" — an inconsistent pair
// that overlapped the leading hyphens into neither result.
//
// So the end comes from the shortest match, found by scanning forward, and the
// start is then advanced as far as it can go while the span still matches,
// which drops exactly the leading ignorables.
func (u *ucaCollation) MatchRange(s, sub string) (int, int, bool) {
	for start := 0; start <= len(s); start++ {
		if start < len(s) && !utf8.RuneStart(s[start]) {
			continue
		}
		end, ok := u.matchAt(s, start, sub)
		if !ok {
			continue
		}
		for start < end {
			next := start + 1
			for next < end && !utf8.RuneStart(s[next]) {
				next++
			}
			if u.Compare(s[next:end], sub) != 0 {
				break
			}
			start = next
		}
		return start, end, true
	}
	return 0, 0, false
}

func (u *ucaCollation) StartsWith(s, prefix string) bool {
	_, ok := u.matchAt(s, 0, prefix)
	return ok
}

func (u *ucaCollation) EndsWith(s, suffix string) bool {
	if u.Compare("", suffix) == 0 {
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
// support, and with fallback=no it must reject the URI. A *malformed* value is
// an error either way — fallback licenses ignoring a tailoring the processor
// lacks, not accepting nonsense — so caseLevel=unknown is refused whatever
// fallback says, while reorder, which this implementation genuinely cannot do,
// is refused only under fallback=no.
//
// Most of the tailoring is expressed by building a BCP-47 tag carrying the
// LDML collation keys (ks, ka, kc, kb, kn) and letting collate.New apply them,
// because x/text reads those from the tag even though it exposes no Option for
// several of them. caseFirst is the exception: x/text has no kf, so it is
// implemented here as a case level of this package's own — see ucaCollation.
func parseUCACollation(uri string) (Collation, error) {
	rest := strings.TrimPrefix(uri, UCACollation)
	if rest != "" && !strings.HasPrefix(rest, "?") {
		return nil, fmt.Errorf("not a UCA collation URI")
	}
	rest = strings.TrimPrefix(rest, "?")

	lang := ""
	strength := "tertiary"
	numeric := false
	caseLevel := false
	backwards := false
	fallback := true
	caseFirst := ""
	alternate := ""
	// unsupported collects tailorings this implementation cannot perform at
	// all, as opposed to values it does not recognise. They are tolerated
	// under fallback=yes, which is what the fallback rule is for.
	var unsupported []string

	// yesNo reads the yes/no parameters. A value that is neither is a
	// malformed URI rather than an unsupported tailoring.
	yesNo := func(k, v string) (bool, error) {
		switch v {
		case "yes":
			return true, nil
		case "no":
			return false, nil
		}
		return false, fmt.Errorf("UCA %s=%q is not yes or no", k, v)
	}

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
			var err error
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
				case "quaternary", "4":
					strength = "quaternary"
				case "identical", "5":
					strength = "identical"
				default:
					return nil, fmt.Errorf("UCA strength=%q is not one of primary, secondary, tertiary, quaternary, identical", v)
				}
			case "numeric":
				numeric, err = yesNo(k, v)
			case "caseLevel":
				caseLevel, err = yesNo(k, v)
			case "backwards":
				backwards, err = yesNo(k, v)
			case "fallback":
				fallback, err = yesNo(k, v)
			case "normalization":
				// x/text normalises to NFD as the UCA requires, so
				// normalization=yes is what it already does.
				// normalization=no asks it to skip that, which x/text has no
				// way to express — but skipping normalisation can only change
				// the answer for input that is not already in a normal form,
				// so honouring the request is a no-op for well-formed input.
				if _, err = yesNo(k, v); err != nil {
					return nil, err
				}
			case "caseFirst":
				switch v {
				case "upper", "lower":
					caseFirst = v
				default:
					return nil, fmt.Errorf("UCA caseFirst=%q is not upper or lower", v)
				}
			case "alternate":
				switch v {
				case "non-ignorable", "shifted", "blanked":
					alternate = v
				default:
					return nil, fmt.Errorf("UCA alternate=%q is not non-ignorable, shifted or blanked", v)
				}
			case "maxVariable":
				// The variable top is fixed by the table x/text ships, so the
				// set of characters alternate= applies to cannot be narrowed
				// or widened. Naming the parameter at all is only meaningful
				// alongside alternate, where getting it wrong changes which
				// strings compare equal.
				switch v {
				case "space", "punct", "symbol", "currency":
					unsupported = append(unsupported, k)
				default:
					return nil, fmt.Errorf("UCA maxVariable=%q is not space, punct, symbol or currency", v)
				}
			case "reorder":
				// collate.Reorder panics: x/text needs fractional weights it
				// does not have. There is no approximation worth offering.
				unsupported = append(unsupported, k)
			case "version":
				// The UCA version is whatever the CLDR tables compiled into
				// x/text are. Asking for a different one cannot be honoured,
				// and quietly answering from the wrong version is the kind of
				// silent wrongness fallback=no exists to prevent.
				unsupported = append(unsupported, k)
			default:
				return nil, fmt.Errorf("UCA parameter %q is not defined by the specification", k)
			}
			if err != nil {
				return nil, err
			}
		}
	}

	if len(unsupported) > 0 && !fallback {
		return nil, fmt.Errorf(
			"UCA collation parameter(s) %s are not implemented and "+
				"fallback=no forbids ignoring them", strings.Join(unsupported, ", "))
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

	// CLDR marks fr-CA as sorting accents from the end of the string, and
	// x/text ships the fr-CA table but reads backwards only from the tag's kb
	// key, never from the locale's own default. Without this, fr-CA ordered
	// accents exactly like fr, which is the one thing the locale exists to do
	// differently.
	if !backwards && isFrenchCanadian(tag) {
		backwards = true
	}

	// The case level is what makes case significant below tertiary strength.
	// caseFirst implies it: asking which case sorts first is meaningless if
	// case is not compared at all.
	if caseFirst != "" {
		caseLevel = true
	}

	keys := map[string]string{}
	switch strength {
	case "primary":
		keys["ks"] = "level1"
	case "secondary":
		keys["ks"] = "level2"
	case "tertiary":
		keys["ks"] = "level3"
	case "quaternary":
		keys["ks"] = "level4"
	case "identical":
		keys["ks"] = "identic"
	}
	switch alternate {
	case "shifted":
		keys["ka"] = "shifted"
	case "blanked":
		keys["ka"] = "blanked"
	case "non-ignorable":
		keys["ka"] = "noignore"
	}
	if numeric {
		keys["kn"] = "true"
	}
	if backwards {
		keys["kb"] = "true"
	}

	c := &ucaCollation{caseFirst: caseFirst}

	if caseFirst != "" {
		// The case comparison is this package's own, so x/text must not also
		// apply one: base folds case away, leaving the case level entirely to
		// caseSignature, and tertiary supplies the non-case tertiary
		// distinctions that would otherwise be lost with it.
		baseKeys := cloneKeys(keys)
		baseKeys["kc"] = "false"
		if strength == "tertiary" || strength == "quaternary" || strength == "identical" {
			// IgnoreCase would also drop the tertiary level, so ask for
			// secondary strength here and restore the tertiary level through
			// the separate collator below.
			baseKeys["ks"] = "level2"
		}
		base, err := newUCACollator(tag, baseKeys)
		if err != nil {
			return nil, err
		}
		tert, err := newUCACollator(tag, keys)
		if err != nil {
			return nil, err
		}
		c.base, c.tertiary = base, tert
	} else {
		if caseLevel {
			keys["kc"] = "true"
		}
		base, err := newUCACollator(tag, keys)
		if err != nil {
			return nil, err
		}
		c.base = base
	}

	// See ucaCollation.stripMarks: the workaround is needed only where a case
	// level is compared at a strength that has already discarded diacritics.
	c.stripMarks = caseLevel && (strength == "primary")

	// x/text's identity level does not survive blanking, so supply it here
	// instead. See ucaCollation.identical.
	c.identical = strength == "identical" && alternate == "blanked"

	return c, nil
}

func cloneKeys(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// newUCACollator applies the LDML collation keys to the tag and builds the
// collator. The keys go through the tag rather than through collate.Option
// because x/text reads ka, kc and kb only from the tag.
func newUCACollator(tag language.Tag, keys map[string]string) (*collate.Collator, error) {
	// Sorted so the tag is deterministic, which keeps the collator the same
	// object shape run to run and makes a failure reproducible.
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	t := tag
	for _, k := range names {
		var err error
		if t, err = t.SetTypeForKey(k, keys[k]); err != nil {
			return nil, fmt.Errorf("UCA tailoring %s=%s cannot be expressed: %w", k, keys[k], err)
		}
	}
	return collate.New(t), nil
}

// isFrenchCanadian reports whether the tag selects the fr-CA collation, whose
// CLDR default is backwards accents. Matching on the base language and region
// rather than on the tag string keeps fr-CA-u-... and fr-Latn-CA working.
func isFrenchCanadian(t language.Tag) bool {
	base, _ := t.Base()
	region, _ := t.Region()
	return base.String() == "fr" && region.String() == "CA"
}

// ucaMatcher is built once: constructing a matcher walks the full list of
// supported tags, which is wasted work on every collation lookup.
var ucaMatcher = language.NewMatcher(collate.Supported())

// ucaCollationFor is parseUCACollation memoised on the URI.
//
// ResolveCollation runs on every call of every function that takes a collation
// argument, and building a UCA collation parses a language tag and constructs
// one or two collate.Collators, each of which walks the CLDR match tables.
// Inside a predicate that is per-item work for a result that depends only on
// the URI. The cached collation is shared, which is safe because ucaCollation
// guards its collator with a mutex and is otherwise immutable.
//
// Failures are cached too: a stylesheet that names an unsupported collation
// names it on every iteration, and re-deriving the same error is the same
// waste.
//
// The cache is bounded and cleared wholesale when full, for the reasons set
// out at regexCacheMax: a collation URI can be built from document data —
// concat($base, $node/@lang) — so the set of URIs is not fixed by the
// stylesheet, and a true LRU would cost a lock on every read to protect a
// working set that is normally one or two entries.
const ucaCacheMax = 256

func ucaCollationFor(uri string) (Collation, error) {
	if v, ok := ucaCache.Load(uri); ok {
		e := v.(ucaCacheEntry)
		return e.c, e.err
	}
	c, err := parseUCACollation(uri)
	if ucaCacheSize.Load() >= ucaCacheMax {
		// Two goroutines can both decide to clear; that is harmless, since
		// every entry is reproducible from its key.
		ucaCache.Range(func(k, _ any) bool {
			ucaCache.Delete(k)
			return true
		})
		ucaCacheSize.Store(0)
	}
	if _, loaded := ucaCache.LoadOrStore(uri, ucaCacheEntry{c, err}); !loaded {
		ucaCacheSize.Add(1)
	}
	return c, err
}

type ucaCacheEntry struct {
	c   Collation
	err error
}

var (
	ucaCache     sync.Map // URI -> ucaCacheEntry
	ucaCacheSize atomic.Int64
)
