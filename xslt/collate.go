package xslt

import (
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

// newCollator builds a collator for a language tag as written in
// xsl:sort/@lang, which uses the same BCP 47 form as xml:lang.
func newCollator(lang string) (*collator, error) {
	tag, err := language.Parse(strings.TrimSpace(lang))
	if err != nil {
		return nil, fmt.Errorf("xsl:sort/@lang=%q is not a valid language tag: %w", lang, err)
	}
	// A tag with no collation data silently falls back to root collation,
	// which is codepoint-like and not what the author asked for. Matching
	// against the tags collate actually supports keeps that from passing
	// unnoticed: asking for Klingon ordering and getting ASCII is the same
	// silent-wrong-order failure that refusing @lang used to prevent.
	if _, _, conf := collateMatcher.Match(tag); conf == language.No {
		return nil, fmt.Errorf(
			"xsl:sort/@lang=%q: no collation data for that language", lang)
	}
	return &collator{c: collate.New(tag), tag: tag}, nil
}
