package xpath

import (
	"fmt"
	"strings"
	"sync"

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
	// without this an embedder had no way to add one.
	if c, ok := lookupRegisteredCollation(uri); ok {
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
		"FOCH0002: collation %q is not supported: only codepoint and "+
			"html-ascii-case-insensitive are implemented", uri)
}

// collationArg validates the trailing collation argument and returns the
// collation it names, so a caller can actually use it rather than only
// checking that it is supported.
func collationArg(fn string, args []xdm.Sequence, i int) (Collation, error) {
	if i >= len(args) {
		return codepointCollation{}, nil
	}
	uri, err := argString(args, i)
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
