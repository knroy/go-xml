package xsd

import (
	"fmt"
	"regexp"

	"github.com/knroy/go-xml/xpath"
)

// compilePattern compiles an XML Schema pattern facet.
//
// The regular expression flavour is the one Part 2 Appendix F defines, which
// the xpath package already translates to RE2 for fn:matches: it handles the
// multi-character escapes \i and \c, the block and category escapes \p{...},
// and character class subtraction, none of which RE2 accepts directly.
//
// The one thing that must not be shared is anchoring. fn:matches is a
// containment test — "matches('abc', 'b')" is true — while a pattern facet is
// anchored: the whole value must match. Reusing the translation unanchored
// would accept every value that merely contains a match, which is a silent
// widening of every pattern in every schema. The translated expression is
// therefore wrapped so that it must span the whole string.
func compilePattern(src string) (*Pattern, error) {
	translated, err := xpath.TranslateSchemaRegexp(src)
	if err != nil {
		return nil, fmt.Errorf("pattern %q: %w", src, err)
	}
	// \A and \z anchor at the true ends of the text. ^ and $ would also
	// match at internal line boundaries under some flag settings, which
	// would reintroduce exactly the widening this wrapping exists to
	// prevent. The non-capturing group keeps a top-level alternation —
	// "a|b" — from binding more loosely than the anchors.
	re, err := regexp.Compile(`\A(?:` + translated + `)\z`)
	if err != nil {
		return nil, fmt.Errorf("pattern %q: %w", src, err)
	}
	return &Pattern{Source: src, re: re}, nil
}
