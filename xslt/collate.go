package xslt

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// collator orders text by the conventions of a language: in Swedish "ä" sorts
// after "z" rather than next to "a", and codepoint order gets that wrong.
//
// The collate.Collator it wraps is stateful — it reuses an internal buffer —
// so it cannot be called from the comparison function of a concurrent sort,
// and a compiled stylesheet is explicitly safe to share across goroutines.
// Guarding it with a mutex and handing out precomputed keys keeps that
// promise: key() is the only entry point, and the sort then compares plain
// byte slices.
type collator struct {
	mu sync.Mutex
	c  *collate.Collator
	// buf is reused across key() calls, which is the whole reason the
	// collator wants exclusive access.
	buf collate.Buffer
	tag language.Tag
}

// key returns a sort key for s. Comparing two keys byte-wise gives the same
// ordering as asking the collator to compare the strings.
func (co *collator) key(s string) []byte {
	co.mu.Lock()
	defer co.mu.Unlock()
	k := co.c.KeyFromString(&co.buf, s)
	// The key points into buf, which the next call reuses, so it must be
	// copied before being stored alongside the value being sorted.
	out := make([]byte, len(k))
	copy(out, k)
	co.buf.Reset()
	return out
}

// collateMatcher is built once: constructing a matcher walks the full list of
// supported tags, which is wasted work on every xsl:sort compilation.
var collateMatcher = language.NewMatcher(collate.Supported())

// isXSLanguage reports whether s is in the value space of xs:language, whose
// lexical form is [a-zA-Z]{1,8}(-[a-zA-Z0-9]{1,8})*.
//
// This is deliberately not language.Parse: BCP 47 well-formedness is a
// stricter test than xs:language membership, and section 13.1.3 hangs two
// different outcomes on which of the two a value fails. "art-lojban" and
// "qqq" are perfectly good xs:language values that name no collation, and the
// spec requires those to be tolerated, not refused.
func isXSLanguage(s string) bool {
	if s == "" {
		return false
	}
	for i, part := range strings.Split(s, "-") {
		if len(part) < 1 || len(part) > 8 {
			return false
		}
		for _, r := range part {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			// Digits are admitted in every subtag but the first.
			case i > 0 && r >= '0' && r <= '9':
			default:
				return false
			}
		}
	}
	return true
}

// errLangUnsupported reports a language that is a legal xs:language but names
// no collation the implementation has. Section 13.1.3 (xslt-lcwd30.xml lines
// 18477-18482) requires the processor to behave as if @lang were omitted in
// that case, so callers treat this sentinel as "sort by codepoint" rather than
// propagating it.
var errLangUnsupported = errors.New("no collation data for that language")

// newCollator builds a collator for a language tag as written in
// xsl:sort/@lang, which uses the same BCP 47 form as xml:lang.
//
// Section 13.1.3 draws a line the two failure modes here must respect. The
// effective value of @lang "must either be a string in the value space of
// xs:language, or a zero-length string" — violating that is an error. But
// "[i]f a language is requested that is not supported, the processor may use
// a fallback language ...; failing this, the processor behaves as if the lang
// attribute were omitted" (xslt-lcwd30.xml:18477-18482). An unsupported
// language is therefore NOT an error, and refusing it made a conformant
// stylesheet fail to compile. xsl:number/@lang already had this right
// (12.3, xslt-lcwd30.xml:18063-18068); xsl:sort/@lang did not.
func newCollator(lang string) (*collator, error) {
	lang = strings.TrimSpace(lang)
	// Outside the value space: the "must" above is violated, so this stays an
	// error. Anything inside it is at worst unsupported.
	if !isXSLanguage(lang) {
		return nil, fmt.Errorf("xsl:sort/@lang=%q is not a valid language tag", lang)
	}
	tag, err := language.Parse(lang)
	if err != nil {
		// A legal xs:language that BCP 47 cannot parse ("xx-YY-ZZ") names no
		// collation this implementation has, which is the fallback case.
		return nil, errLangUnsupported
	}
	// The spec's own fallback — "removing successive hyphen-separated
	// suffixes until a supported language code is obtained" — is what the
	// matcher performs, so a tag it can place is used as matched.
	if _, _, conf := collateMatcher.Match(tag); conf == language.No {
		return nil, errLangUnsupported
	}
	return &collator{c: collate.New(tag), tag: tag}, nil
}
