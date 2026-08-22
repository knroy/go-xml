package xdm

import (
	"fmt"
	"strconv"
	"strings"
)

// Internal general entities declared in a DOCTYPE's internal subset.
//
// This exists for one real construct: a schema that composes a value from
// named fragments, as the W3C's own RFC 3986 type library does — fifty
// entities building a URI regex out of its grammar productions. Without them
// the document is simply unparseable, because the entity references have no
// definition anywhere else.
//
// **Only internal entities are read.** An entity declared SYSTEM or PUBLIC
// names something outside the document, and fetching it is XXE — the attack
// AllowDOCTYPE exists to gate. Those are recorded as refused rather than
// resolved, so a reference to one is an error and never a silent fetch.
//
// Expansion is bounded twice over, because nested entities are how
// billion-laughs works: each entity's replacement text is expanded at most
// maxEntityDepth levels deep, and the total expanded size of any one entity is
// capped at maxEntityBytes. Either limit turns the reference into an error
// rather than a hang.
const (
	// maxEntityDepth bounds nesting. The RFC 3986 library nests about six
	// deep; a hundred is far past anything hand-written and far short of
	// what an exponential bomb needs.
	maxEntityDepth = 100
	// maxEntityBytes bounds one entity's fully expanded replacement text.
	//
	// Measured against the largest legitimate use known: the W3C's RFC 3987
	// type library expands "IRI-reference" to 9,569 bytes out of 68 entities.
	// 64 KB leaves that six times over and still refuses a five-level
	// billion-laughs, which reaches 100,000 bytes — an earlier 1 MB cap let
	// that one through, which is the whole reason this number is measured
	// rather than picked.
	maxEntityBytes = 64 << 10
	// maxTotalEntityBytes bounds every expansion in one document together.
	// A bomb split across many entities, each under the per-entity cap, would
	// otherwise still add up.
	maxTotalEntityBytes = 1 << 20
	// maxEntityCount bounds how many an internal subset may declare.
	maxEntityCount = 10000
)

// entityTable holds the internal general entities a DOCTYPE declared.
type entityTable struct {
	raw      map[string]string // name -> replacement text, unexpanded
	expanded map[string]string // name -> fully expanded, memoised
	external map[string]bool   // names declared SYSTEM or PUBLIC
	// total is the expanded size of everything resolved so far, so that a
	// bomb divided among many entities cannot slip under the per-entity cap.
	total int
	// reparsed says the expansion feeds a second *parse* rather than the
	// decoder's substitution map.
	//
	// It decides whether "&amp;" in replacement text is decoded here. Through
	// dec.Entity it must be, because the decoder substitutes that map's values
	// without re-scanning them, so "&amp;" would otherwise reach a value as
	// five characters. Through the rewrite it must not be, because the
	// rewritten source *is* scanned again and the decoder will decode it —
	// doing it here as well turns "&amp;lt;" into "<", manufacturing markup
	// out of data the document escaped.
	reparsed bool
}

// parseInternalEntities reads the <!ENTITY ...> declarations out of a DOCTYPE
// internal subset.
//
// A parameter entity — "<!ENTITY % name ...>" — is skipped. Parameter entities
// are expanded inside the DTD itself rather than in content, and reading one
// would mean interpreting the subset as a grammar rather than scanning it.
func parseInternalEntities(subset string) *entityTable {
	t := &entityTable{
		raw:      map[string]string{},
		expanded: map[string]string{},
		external: map[string]bool{},
	}
	rest := subset
	for len(t.raw)+len(t.external) < maxEntityCount {
		i := strings.Index(rest, "<!ENTITY")
		if i < 0 {
			break
		}
		rest = rest[i+len("<!ENTITY"):]
		// The declaration ends at the first ">" *outside* a quoted value.
		// Scanning for a bare ">" truncates any entity whose replacement text
		// contains one — which is every entity that holds markup, and the
		// reason <!ENTITY e "<b/>"> was read as the value "<b/".
		end := endOfDeclaration(rest)
		if end < 0 {
			break
		}
		body := strings.TrimSpace(rest[:end])
		rest = rest[end+1:]

		// A parameter entity declaration begins with "%".
		if strings.HasPrefix(body, "%") {
			continue
		}
		fields := attListFields(body)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if !isEntityName(name) {
			continue
		}
		// SYSTEM and PUBLIC name something outside this document. Recording
		// the name rather than dropping it means a reference to it is
		// reported as refused instead of as merely undeclared, which tells a
		// caller the difference between a typo and a blocked fetch.
		if fields[1] == "SYSTEM" || fields[1] == "PUBLIC" {
			t.external[name] = true
			continue
		}
		val := fields[1]
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') {
			// XML §4.2: where more than one declaration names the same
			// entity, the *first* binds and the rest are ignored. The RFC
			// 3986 type library relies on this — it declares sub-delims three
			// times, the first correctly escaped for a regex and the later
			// ones showing the unescaped grammar for a reader. Keeping the
			// last produced a pattern with bare "(" and "+" in it.
			if _, dup := t.raw[name]; !dup {
				t.raw[name] = unquote(val)
			}
		}
	}
	if len(t.raw) == 0 && len(t.external) == 0 {
		return nil
	}
	return t
}

// endOfDeclaration returns the offset of the ">" that closes a declaration,
// skipping any inside a quoted value, or -1 if there is none.
func endOfDeclaration(s string) int {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '>':
			return i
		}
	}
	return -1
}

func isEntityName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !isNameStartRune(r) {
			return false
		}
		if i > 0 && !isNameRune(r) {
			return false
		}
	}
	return true
}

// resolve returns the fully expanded replacement text for name.
//
// It implements xml.Decoder's Entity lookup, which takes a map rather than a
// function, so the expansion is done once up front for every declared entity
// and the result handed over as a plain map.
func (t *entityTable) resolve(name string) (string, error) {
	if s, ok := t.expanded[name]; ok {
		return s, nil
	}
	if t.external[name] {
		return "", fmt.Errorf(
			"entity %q is declared SYSTEM or PUBLIC; external entities are "+
				"never resolved", name)
	}
	raw, ok := t.raw[name]
	if !ok {
		return "", fmt.Errorf("entity %q is not declared", name)
	}
	// A placeholder guards against a cycle: an entity that refers to itself,
	// directly or through others, would otherwise recurse forever.
	t.expanded[name] = ""
	out, err := t.expand(raw, 0, map[string]bool{name: true})
	if err != nil {
		delete(t.expanded, name)
		return "", err
	}
	t.total += len(out)
	if t.total > maxTotalEntityBytes {
		delete(t.expanded, name)
		return "", fmt.Errorf(
			"entity expansion exceeds %d bytes in total", maxTotalEntityBytes)
	}
	t.expanded[name] = out
	return out, nil
}

func (t *entityTable) expand(s string, depth int, seen map[string]bool) (string, error) {
	if depth > maxEntityDepth {
		return "", fmt.Errorf("entity expansion exceeds %d levels", maxEntityDepth)
	}
	var sb strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != '&' {
			sb.WriteByte(c)
			i++
			continue
		}
		j := strings.IndexByte(s[i:], ';')
		if j < 0 {
			sb.WriteByte(c)
			i++
			continue
		}
		name := s[i+1 : i+j]
		// A character reference is decoded here. The decoder would do it for
		// text it reads directly, but replacement text arriving through
		// dec.Entity is substituted rather than re-scanned, so "&#xA0;" in an
		// entity would otherwise reach the value literally.
		// A character reference is always decoded, on both paths.
		//
		// Through dec.Entity it must be, since the decoder does not re-scan
		// replacement text. Through the rewrite it must be too, and for a
		// different reason: a character reference may form part of a *name*,
		// as <!ENTITY dii "<&#xE14;&#xE35;/>"> does, and a name is not a
		// place a reference can survive to be decoded later. Leaving it
		// encoded produces "<&#xE14;" — not an element at all.
		//
		// This is the opposite of what the five predefined entities need
		// below, and the difference is exactly that: a character reference
		// denotes a character, while "&amp;" denotes escaped data whose
		// escaping the second parse will undo.
		if name != "" && name[0] == '#' {
			if r, ok := decodeCharRef(name); ok {
				sb.WriteRune(r)
				i += j + 1
				continue
			}
			sb.WriteString(s[i : i+j+1])
			i += j + 1
			continue
		}
		// The five predefined entities are decoded here for the same reason
		// as a character reference: the decoder substitutes replacement text
		// rather than re-scanning it, so "&amp;" inside an entity would reach
		// the value as the five characters "&amp;" instead of "&".
		if name == "" {
			sb.WriteString(s[i : i+j+1])
			i += j + 1
			continue
		}
		if r, ok := predefinedRune(name); ok {
			if t.reparsed {
				// The second parse decodes it, so it is left alone. Decoding
				// here too would turn "&amp;lt;" into "<".
				sb.WriteString(s[i : i+j+1])
			} else {
				sb.WriteRune(r)
			}
			i += j + 1
			continue
		}
		if seen[name] {
			return "", fmt.Errorf("entity %q refers to itself", name)
		}
		if t.external[name] {
			return "", fmt.Errorf(
				"entity %q is declared SYSTEM or PUBLIC; external entities are "+
					"never resolved", name)
		}
		raw, ok := t.raw[name]
		if !ok {
			return "", fmt.Errorf("entity %q is not declared", name)
		}
		seen[name] = true
		sub, err := t.expand(raw, depth+1, seen)
		delete(seen, name)
		if err != nil {
			return "", err
		}
		sb.WriteString(sub)
		if sb.Len() > maxEntityBytes {
			return "", fmt.Errorf(
				"entity expansion exceeds %d bytes", maxEntityBytes)
		}
		i += j + 1
	}
	return sb.String(), nil
}

// predefinedRune returns the character one of XML's five predefined entities
// stands for.
func predefinedRune(name string) (rune, bool) {
	switch name {
	case "amp":
		return '&', true
	case "lt":
		return '<', true
	case "gt":
		return '>', true
	case "quot":
		return '"', true
	case "apos":
		return '\'', true
	}
	return 0, false
}

// entityMap expands every declared entity and returns the map the decoder
// wants.
//
// An entity that cannot be expanded is left out rather than reported here: a
// document may declare an entity it never references, and refusing to parse it
// for that would be stricter than the spec. A *reference* to a missing name is
// still an error, raised by the decoder.
func (t *entityTable) entityMap() map[string]string {
	out := make(map[string]string, len(t.raw))
	for name := range t.raw {
		if s, err := t.resolve(name); err == nil {
			out[name] = s
		}
	}
	return out
}

// decodeCharRef turns "#38" or "#x26" into its rune.
func decodeCharRef(name string) (rune, bool) {
	digits, base := name[1:], 10
	if len(digits) > 1 && (digits[0] == 'x' || digits[0] == 'X') {
		digits, base = digits[1:], 16
	}
	if digits == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(digits, base, 32)
	if err != nil || n < 0 || n > 0x10FFFF {
		return 0, false
	}
	return rune(n), true
}

// hasMarkup reports whether any declared entity's replacement text contains
// markup rather than only characters.
//
// This decides whether a document needs the substitution pass below, and the
// answer is almost always no: an entity holding "&#xA0;" or a boilerplate
// phrase is text, and text is what encoding/xml's dec.Entity handles well.
func (t *entityTable) hasMarkup() bool {
	// Resolving for this test caches the expansion under the rewrite's rules,
	// which differ from dec.Entity's. The cache is therefore discarded on the
	// way out: a document that turns out not to need the rewrite must not
	// then be handed values memoised for it.
	t.reparsed = true
	defer func() {
		t.reparsed = false
		t.expanded = map[string]string{}
		// The total is reset with the cache. This test resolves *every*
		// declared entity, including ones the document never references, and
		// charging those against the shared budget would let a subset full of
		// large unused declarations exhaust it — so a legitimate reference
		// then failed with an error about the wrong thing. The bound that
		// matters is on what a document actually expands, which the
		// substitution and the decoder each charge for themselves.
		t.total = 0
	}()
	for name := range t.raw {
		// The raw text is checked first, and the expansion only when it might
		// change the answer. Resolving every declaration to ask this question
		// makes the answer depend on map order: a subset whose large entities
		// happen to be visited first exhausts the budget, resolve fails, and
		// an entity that does hold markup is never reached — so the same
		// document parses differently from run to run.
		if declaresMarkup(t.raw[name]) {
			return true
		}
	}
	// Nesting needs no separate check: markup reaches an entity only from a
	// declaration that contains it, and the loop above sees every
	// declaration. What it cannot see is an *external* entity's text, which
	// is never resolved at all.
	return false
}

// declaresMarkup reports whether replacement text yields a "<" that starts
// markup.
//
// A literal "<" does, and so does a character reference to it: expansion
// turns "&#x3C;b/&#x3E;" into "<b/>", which XML then parses as an element.
// "&lt;" does not — that is an escaped character and stays one. libxml2 draws
// the line in the same place, which is worth knowing because the three look
// interchangeable and are not.
func declaresMarkup(raw string) bool {
	if strings.ContainsRune(raw, '<') {
		return true
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '&' || i+1 >= len(raw) || raw[i+1] != '#' {
			continue
		}
		j := strings.IndexByte(raw[i:], ';')
		if j < 0 {
			continue
		}
		if r, ok := decodeCharRef(raw[i+1 : i+j]); ok && r == '<' {
			return true
		}
		i += j
	}
	return false
}

// substituteMarkupEntities replaces entity references in a document with their
// replacement text, so that markup inside an entity is parsed as markup.
//
// encoding/xml cannot do this. Its dec.Entity is a map to *strings*, and the
// decoder substitutes the value as character data without re-scanning it — so
// <!ENTITY e "<b/>"> followed by &e; yields the four characters "<b/>" rather
// than an element. XML says the replacement text is parsed, which is what
// makes an entity a way to factor out a fragment rather than only a phrase.
//
// So a document that needs it is rewritten before the decoder sees it. This
// runs only when hasMarkup says some entity holds markup, because the rewrite
// costs a copy of the document and the common case does not need one.
//
// The expansion bounds are the table's own — the per-entity cap, the total
// cap, the depth limit — so this adds no new limit to get wrong, and a
// billion-laughs is refused here for the same reason and by the same code as
// everywhere else. What it does add is a bound on how many references one
// document may substitute, since each is cheap individually and a document
// made of nothing else would otherwise be unbounded.
func (t *entityTable) substituteMarkupEntities(src string) (string, error) {
	t.reparsed = true
	// The internal subset declares the entities and may mention them in
	// default values; substituting there would change the declarations
	// themselves. Everything after it is content.
	start := endOfInternalSubset(src)

	var sb strings.Builder
	sb.Grow(len(src))
	sb.WriteString(src[:start])

	var count, written int
	for i := start; i < len(src); {
		// CDATA sections, comments and processing instructions are regions
		// where XML does *not* recognise an entity reference: inside them
		// "&e;" is five characters and nothing else. Copying them across
		// untouched is not a nicety — a scanner without this state rewrites
		// them, and replacement text that closes the region and opens a new
		// one ("]]><evil/><![CDATA[") becomes document structure. That turns
		// entity text into markup, which is the thing this whole file exists
		// to bound.
		if skip := unscannedRegion(src, i); skip > i {
			sb.WriteString(src[i:skip])
			written += skip - i
			i = skip
			continue
		}
		c := src[i]
		if c != '&' {
			sb.WriteByte(c)
			i++
			written++
			continue
		}
		j := strings.IndexByte(src[i:], ';')
		if j < 0 {
			sb.WriteByte(c)
			i++
			written++
			continue
		}
		name := src[i+1 : i+j]
		// A character reference and the five predefined entities are left
		// alone: the decoder handles both, and rewriting them here would
		// turn "&amp;lt;" into something the document did not say.
		if name == "" || name[0] == '#' {
			sb.WriteString(src[i : i+j+1])
			i += j + 1
			written += j + 1
			continue
		}
		if _, ok := predefinedRune(name); ok {
			sb.WriteString(src[i : i+j+1])
			i += j + 1
			written += j + 1
			continue
		}
		rep, err := t.resolve(name)
		if err != nil {
			// An entity that cannot be expanded is left as written, so the
			// decoder reports the reference. Its error names the entity and
			// the position, which is more use than one from here.
			sb.WriteString(src[i : i+j+1])
			i += j + 1
			written += j + 1
			continue
		}
		count++
		if count > maxEntityCount {
			return "", fmt.Errorf(
				"document references more than %d entities", maxEntityCount)
		}
		written += len(rep)
		if written > maxTotalEntityBytes {
			return "", fmt.Errorf(
				"entity expansion exceeds %d bytes", maxTotalEntityBytes)
		}
		sb.WriteString(rep)
		i += j + 1
	}
	return sb.String(), nil
}

// endOfInternalSubset returns the offset just past the DOCTYPE declaration, or
// zero when there is none.
//
// The scan tracks quotes and nesting because an internal subset may contain
// both: a default value may hold "]" and a declaration may hold ">".
func endOfInternalSubset(src string) int {
	i := strings.Index(src, "<!DOCTYPE")
	if i < 0 {
		return 0
	}
	var quote byte
	depth := 0
	for j := i; j < len(src); j++ {
		c := src[j]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '>':
			if depth == 0 {
				return j + 1
			}
		}
	}
	return 0
}

// unscannedRegion reports the end of the CDATA section, comment or processing
// instruction beginning at i, or i itself when none begins there.
//
// These are the three constructs XML defines as not recognising an entity
// reference. A rewrite that does not know about them does two wrong things at
// once: it expands a reference the document meant literally, and — worse — it
// lets replacement text close the region and reopen it, so an entity's
// contents become markup. Both are silent.
//
// An unterminated region runs to the end of the source. That is malformed XML
// and the decoder will say so on the second parse; consuming the rest here is
// the safe reading, since the alternative is to resume scanning inside a
// construct whose end was never found.
func unscannedRegion(src string, i int) int {
	rest := src[i:]
	for _, r := range []struct{ open, close string }{
		{"<![CDATA[", "]]>"},
		{"<!--", "-->"},
		{"<?", "?>"},
	} {
		if !strings.HasPrefix(rest, r.open) {
			continue
		}
		end := strings.Index(rest[len(r.open):], r.close)
		if end < 0 {
			return len(src)
		}
		return i + len(r.open) + end + len(r.close)
	}
	return i
}
