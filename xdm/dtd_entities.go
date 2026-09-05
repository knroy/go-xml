package xdm

import (
	"bytes"
	"fmt"
	"io"
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
	// unparsed holds the external entities declared with an NDATA notation,
	// which fn:unparsed-entity-uri reports. They are recorded, never read.
	unparsed map[string]unparsedEntity
	// total is the expanded size of everything resolved so far, so that a
	// bomb divided among many entities cannot slip under the per-entity cap.
	total int
	// resolver, when non-nil, permits external entities to be read. It is
	// nil by default and is NOT implied by AllowDOCTYPE — see dtd_external.go
	// for why those two are separate gates.
	resolver EntityResolver
	// externalDecl records what each external entity names and the base its
	// system identifier resolves against, so that a declaration read from an
	// external subset resolves relative to that subset.
	externalDecl map[string]externalEntityDecl
	// externalText memoises fetched replacement text, and externalBase the
	// URI it came from. The memo bounds fetches as well as cost: without it
	// a reference in a loop would be a fetch in a loop.
	externalText map[string]string
	externalBase map[string]string
	// fetching marks the external entities currently being read, so a cycle
	// through them is an error rather than an unbounded chain of fetches.
	fetching map[string]bool
	// fetches counts external resources read, bounded by maxExternalFetches.
	fetches int
	// externalDepth is the current nesting of external subset inclusion.
	externalDepth int
	// subsetText is the declaration text this table was read from. It is
	// retained so that an external subset's attribute defaults and declared
	// types can be read from the merged text after parameter entities have
	// been substituted — the point at which they first exist.
	subsetText string
	// docBase is the URI external system identifiers in the internal subset
	// resolve against.
	docBase string

	// baseSpans records which byte ranges of the substituted source came
	// from which external entity, so the second parse can set per-node base
	// URIs. Populated by substituteMarkupEntities; empty otherwise.
	baseSpans []entityBaseSpan

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
	return parseEntityDecls(subset, "")
}

// parseEntityDecls reads entity declarations, recording base as the URI that
// external system identifiers in them resolve against. Declarations read from
// an external subset carry that subset's URI, per XML section 4.4.3.
func parseEntityDecls(subset, base string) *entityTable {
	t := newEntityTable(base)
	t.subsetText = subset
	return t.parseDecls(subset, base)
}

// newEntityTable returns an empty table whose external system identifiers
// resolve against base.
func newEntityTable(base string) *entityTable {
	return &entityTable{
		raw:          map[string]string{},
		expanded:     map[string]string{},
		external:     map[string]bool{},
		externalBase: map[string]string{},
		docBase:      base,
	}
}

func (t *entityTable) parseDecls(subset, base string) *entityTable {
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
			// The system identifier is recorded whether or not a resolver
			// was supplied. With none it is never used and the reference is
			// refused exactly as before; with one it is what gets fetched.
			// Recording it unconditionally keeps the two paths reading the
			// same declaration rather than parsing it twice differently.
			if d, ok := externalDeclOf(fields, base); ok {
				if t.externalDecl == nil {
					t.externalDecl = map[string]externalEntityDecl{}
				}
				// First declaration wins, as for internal entities.
				if _, dup := t.externalDecl[name]; !dup {
					t.externalDecl[name] = d
				}
			}
			// An unparsed entity is an external one with an NDATA notation,
			// and unlike a parsed external entity it is never fetched: the
			// data model records its system identifier and notation so that
			// fn:unparsed-entity-uri can return them. Nothing here reads the
			// resource, so this does not widen what AllowDOCTYPE admits.
			if u := unparsedEntityOf(fields); u != nil {
				if t.unparsed == nil {
					t.unparsed = map[string]unparsedEntity{}
				}
				if _, dup := t.unparsed[name]; !dup {
					t.unparsed[name] = *u
				}
			}
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
		// With no resolver this is refused, exactly as before. With one the
		// text is fetched and then expanded on the same terms as internal
		// replacement text — the fetched bytes are already charged to the
		// budget by fetchExternal, before this expansion runs.
		if t.resolver == nil {
			return "", fmt.Errorf(
				"entity %q is declared SYSTEM or PUBLIC; set ExternalEntities "+
					"to a resolver to permit reading it", name)
		}
		text, err := t.resolveExternalText(name)
		if err != nil {
			return "", err
		}
		t.expanded[name] = ""
		out, err := t.expand(text, 0, map[string]bool{name: true})
		if err != nil {
			delete(t.expanded, name)
			return "", err
		}
		t.expanded[name] = out
		return out, nil
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
			"entity expansion exceeds %d bytes in total: %w",
			maxTotalEntityBytes, ErrResourceLimit)
	}
	t.expanded[name] = out
	return out, nil
}

func (t *entityTable) expand(s string, depth int, seen map[string]bool) (string, error) {
	if depth > maxEntityDepth {
		return "", fmt.Errorf("entity expansion exceeds %d levels: %w",
			maxEntityDepth, ErrResourceLimit)
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
			if t.resolver == nil {
				return "", fmt.Errorf(
					"entity %q is declared SYSTEM or PUBLIC; external entities are "+
						"never resolved", name)
			}
			text, err := t.resolveExternalText(name)
			if err != nil {
				return "", err
			}
			seen[name] = true
			sub, err := t.expand(text, depth+1, seen)
			delete(seen, name)
			if err != nil {
				return "", err
			}
			sb.WriteString(sub)
			if sb.Len() > maxEntityBytes {
				return "", fmt.Errorf(
					"entity expansion exceeds %d bytes: %w",
					maxEntityBytes, ErrResourceLimit)
			}
			i += j + 1
			continue
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
				"entity expansion exceeds %d bytes: %w",
				maxEntityBytes, ErrResourceLimit)
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
	// declaration. What it cannot see is an *external* entity's text.
	//
	// When externals are refused that does not matter, since the text is
	// never read. When a resolver permits them it matters entirely: the text
	// is unknown until it is fetched, and an external entity is used
	// overwhelmingly to factor out a FRAGMENT — copy-13.xml's ent21.xml is a
	// whole element. So a declared external entity forces the rewrite path,
	// which is the only path that parses replacement text as markup.
	// Fetching here to look would charge the budget for entities the
	// document may never reference, which is the same map-order hazard the
	// deferred reset above exists to avoid.
	if t.resolver != nil && len(t.externalDecl) > 0 {
		return true
	}
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
	// inTag and attrQuote track whether the scanner is inside a start-tag and,
	// within one, inside a quoted attribute value. That context changes what a
	// substitution may write, not merely where it happens: see the escaping
	// below.
	var inTag bool
	var attrQuote byte
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
			// The tag/attribute state is advanced on the document's OWN
			// bytes only. Replacement text is never inspected for it: a
			// quote inside an entity is data by XML section 4.4.5, so
			// letting it change the state is precisely the bug this tracking
			// exists to prevent.
			switch {
			case attrQuote != 0:
				if c == attrQuote {
					attrQuote = 0
				}
			case inTag:
				switch c {
				case '"', '\'':
					attrQuote = c
				case '>':
					inTag = false
				}
			case c == '<':
				// A "<" that opens a comment, CDATA section or PI never
				// reaches here — unscannedRegion consumed it above. An end
				// tag has no attributes, but tracking it as a tag costs
				// nothing and keeps the state machine total.
				inTag = true
			}
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
			// A REFUSED FETCH is reported here, not deferred. The document
			// fails either way — the reference is left as written and the
			// decoder rejects it — but the two errors say very different
			// things: "invalid character entity" reads as a malformed
			// document, while a refusal names the resource and the reason it
			// was denied. When the reason is a containment or scheme check,
			// that difference is the difference between a mystery and an
			// audit trail.
			if t.external[name] {
				return "", err
			}
			// An entity that cannot be expanded is left as written, so the
			// decoder reports the reference. Its error names the entity and
			// the position, which is more use than one from here.
			sb.WriteString(src[i : i+j+1])
			i += j + 1
			written += j + 1
			continue
		}
		// XML section 4.4.5, "Included in Literal": a reference inside an
		// attribute value has its replacement text included as if it were
		// literal characters, so a quote in that text is data and does NOT
		// end the attribute. The rewrite splices text into the source, where
		// a bare quote would end it — DocBook's entities.ent declares
		// <!ENTITY primary 'normalize-space(concat(primary/@sortas, " ",
		// primary))'> and every stylesheet that uses it inside a double-
		// quoted attribute would otherwise become malformed. Escaping the
		// three characters that are markup in an attribute value restores the
		// literal reading; the decoder turns them back on the second parse.
		// "<" is escaped too rather than left to corrupt the tag, matching
		// the well-formedness constraint that forbids it there.
		if attrQuote != 0 {
			rep = escapeAttrLiteral(rep)
		}
		count++
		if count > maxEntityCount {
			return "", fmt.Errorf(
				"document references more than %d entities: %w",
				maxEntityCount, ErrResourceLimit)
		}
		written += len(rep)
		if written > maxTotalEntityBytes {
			return "", fmt.Errorf(
				"entity expansion exceeds %d bytes: %w",
				maxTotalEntityBytes, ErrResourceLimit)
		}
		// Where this replacement lands is recorded so the second parse can
		// give the nodes it produces the base URI of the entity they were
		// written in rather than of the including document. See
		// entityBaseSpan.
		at := sb.Len()
		if t.external[name] {
			t.baseSpans = append(t.baseSpans, entityBaseSpan{
				start: at, end: at + len(rep), base: t.externalBase[name],
			})
			if inner, ok := t.externalText[name]; ok {
				t.baseSpans = append(t.baseSpans,
					t.externalSpansIn(inner, at, 1)...)
			}
		} else if src, ok := t.raw[name]; ok {
			t.baseSpans = append(t.baseSpans, t.externalSpansIn(src, at, 1)...)
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
//
// Comments and processing instructions are skipped whole. XML 1.0 §2.8 admits
// both to the internal subset — markupdecl is "elementdecl | AttlistDecl |
// EntityDecl | NotationDecl | PI | Comment" — and neither one's content is
// markup, so an apostrophe, a quote or a bracket written inside one is text and
// not structure. Reading them as structure was wrong in both directions:
// "<!-- it's here -->" opened a quote that never closed and the scan ran off
// the end, returning 0 and making the caller treat the subset itself as
// content; "<!-- ] -->" closed the bracket early and ended the subset in the
// middle of the declarations. A PI misparses identically — "<?p it's here ?>"
// swallowed the rest of the document — which made a well-formed file fail to
// parse with "unexpected EOF".
//
// A CDATA section needs no handling: CDSect appears only in `content`
// (XML 1.0 §2.4, §3.1), never in intSubset, so one written here is a malformed
// document that the decoder rejects on its own.
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
		// A comment or PI can only begin where markup can, which is
		// outside a quoted literal — the test above has already excluded
		// that. "<!--" is tried before "<?" only because they cannot both
		// match; the order carries no meaning.
		if c == '<' {
			var open, close string
			switch {
			case strings.HasPrefix(src[j:], "<!--"):
				open, close = "<!--", "-->"
			case strings.HasPrefix(src[j:], "<?"):
				open, close = "<?", "?>"
			}
			if open != "" {
				end := strings.Index(src[j+len(open):], close)
				if end < 0 {
					// Unterminated: the document is malformed and the
					// decoder will say so. Consuming the rest is the safe
					// reading, as resuming inside the construct would scan
					// its text as structure.
					return 0
				}
				j += len(open) + end + len(close) - 1
				continue
			}
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

// unparsedEntity is an external entity declared with an NDATA notation.
//
// It is the one kind of entity a document may name without the processor ever
// reading it: the reference appears in an attribute of type ENTITY, and a
// stylesheet asks for its system identifier through fn:unparsed-entity-uri.
type unparsedEntity struct {
	// systemID is the entity's system identifier, as written.
	systemID string
	// publicID is its public identifier, empty for a SYSTEM declaration.
	publicID string
	// notation is the NDATA name.
	notation string
}

// unparsedEntityOf reads an entity declaration's fields, returning the
// unparsed entity it declares or nil when it declares a parsed one.
//
// The two forms are
//
//	name SYSTEM "uri" NDATA notation
//	name PUBLIC "public" "uri" NDATA notation
//
// and it is the NDATA keyword that makes an external entity unparsed.
func unparsedEntityOf(fields []string) *unparsedEntity {
	ndata := -1
	for i, f := range fields {
		if f == "NDATA" {
			ndata = i
			break
		}
	}
	if ndata < 0 || ndata+1 >= len(fields) {
		return nil
	}
	u := unparsedEntity{notation: fields[ndata+1]}
	switch fields[1] {
	case "SYSTEM":
		if ndata > 2 {
			u.systemID = unquote(fields[2])
		}
	case "PUBLIC":
		if ndata > 3 {
			u.publicID = unquote(fields[2])
			u.systemID = unquote(fields[3])
		}
	}
	return &u
}

// UnparsedEntity returns the system identifier and notation of an unparsed
// entity declared in a document's internal subset.
//
// An unparsed entity is the one kind a processor never reads: it is declared
// SYSTEM or PUBLIC with an NDATA notation, referenced from an attribute of
// type ENTITY, and its identifier is data for the application rather than
// something to fetch. fn:unparsed-entity-uri and fn:unparsed-entity-public-id
// return exactly these.
//
// The declarations are re-read from the retained DOCTYPE text rather than
// carried on every tree, since a document with unparsed entities is rare and
// the lookup happens at most once per call.
func (t *Tree) UnparsedEntity(name string) (systemID, publicID, notation string, ok bool) {
	if t == nil {
		return "", "", "", false
	}
	// The external subset is consulted as well as the directive, and after
	// it, so that the internal subset still wins where both declare a name —
	// XML section 4.2. parseInternalEntities keeps the first declaration of
	// each name, so concatenating in this order gives that rule for free.
	subset := t.DocType
	if t.externalSubset != "" {
		subset += "\n" + t.externalSubset
	}
	if subset == "" {
		return "", "", "", false
	}
	tbl := parseInternalEntities(subset)
	if tbl == nil {
		return "", "", "", false
	}
	u, found := tbl.unparsed[name]
	if !found {
		return "", "", "", false
	}
	return u.systemID, u.publicID, u.notation, true
}

// escapeAttrLiteral escapes the characters that would be markup inside an
// attribute value, so that replacement text spliced there is read as literal
// characters — XML section 4.4.5, "Included in Literal".
//
// Only three are escaped. "&" is deliberately not: on the rewrite path expand
// leaves "&amp;" as written for the second parse to decode (see the
// t.reparsed branch there), so escaping "&" here would turn it into "&amp;amp;"
// and change what the document says. That leaves a decoded "&#38;" as a bare
// "&", which is the same reading the content path already gives it.
func escapeAttrLiteral(s string) string {
	if !strings.ContainsAny(s, `"'<`) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			sb.WriteString("&#34;")
		case '\'':
			sb.WriteString("&#39;")
		case '<':
			sb.WriteString("&#60;")
		default:
			sb.WriteByte(s[i])
		}
	}
	return sb.String()
}

// chargeReferences charges every entity reference in src against the shared
// expansion budget, and reports an error when the document's references add up
// to more than maxTotalEntityBytes.
//
// This exists because the budget is otherwise charged in the wrong unit. The
// t.total accounting in resolve runs once per DISTINCT ENTITY, at the point
// the entity is first expanded and memoised — so a document declaring one
// 64 KB entity and referencing it a hundred thousand times charges the budget
// 64 KB while the decoder substitutes 6.5 GB into the tree. Neither MaxBytes
// nor MaxNodes catches that: the input stays small (a reference is three
// bytes) and the result is one text node however long it is. Measured before
// this check existed, 356 KB of input expanded to 6.5 GB in 19 seconds.
//
// substituteMarkupEntities has always charged per reference — it accumulates
// "written" as it splices — which is why the same document was refused on the
// rewrite path and accepted on the plain one. This gives the plain path the
// same accounting, so which path a document takes no longer decides whether
// its expansion is bounded.
//
// Only the document's own bytes are scanned, with the internal subset skipped
// and the three regions XML does not recognise a reference in passed over, for
// the same reasons substituteMarkupEntities does it: a reference inside a
// CDATA section, comment or PI is not a reference and must not be charged.
func (t *entityTable) chargeReferences(src string) error {
	total := t.total
	for i := endOfInternalSubset(src); i < len(src); {
		if skip := unscannedRegion(src, i); skip > i {
			i = skip
			continue
		}
		if src[i] != '&' {
			i++
			continue
		}
		j := strings.IndexByte(src[i:], ';')
		if j < 0 {
			i++
			continue
		}
		name := src[i+1 : i+j]
		i += j + 1
		// A character reference and the five predefined entities expand to a
		// single character each and are the decoder's business, not the
		// table's.
		if name == "" || name[0] == '#' {
			continue
		}
		if _, ok := predefinedRune(name); ok {
			continue
		}
		// An entity that does not resolve is not charged: the decoder reports
		// the reference as an error, and charging it here would replace that
		// specific message with a budget one.
		rep, err := t.resolve(name)
		if err != nil {
			continue
		}
		total += len(rep)
		if total > maxTotalEntityBytes {
			return fmt.Errorf(
				"entity expansion exceeds %d bytes in total: %w",
				maxTotalEntityBytes, ErrResourceLimit)
		}
	}
	return nil
}

// entityChargeReader charges entity references against the shared expansion
// budget as the document streams past, refusing before the decoder expands
// them rather than after.
//
// The ordering is the whole point. A post-parse check reports the same
// verdict, but only once encoding/xml has substituted every reference and
// coalesced the result into a single character-data token: measured, one
// 64 KB entity referenced 100,000 times reached 14.3 GB of live heap before
// the check ran, which refuses the document without bounding what refusing it
// cost. Charging here caps peak memory at the budget itself.
//
// Only the bytes after the internal subset are scanned, and the three regions
// XML does not recognise a reference in are skipped, on the same terms as
// substituteMarkupEntities. A reference split across two Read calls is held in
// pend until the rest of it arrives, so the chunking of the underlying reader
// cannot hide one.
type entityChargeReader struct {
	r     io.Reader
	t     *entityTable
	total int
	// pend holds a partial reference carried over from the previous read.
	pend []byte
	// started reports whether the internal subset has been passed.
	started bool
	// seen accumulates enough of the document to locate the end of the
	// internal subset, and is released once that is found.
	seen []byte
	// backlog holds bytes read before the entity table was installed. The
	// decoder buffers ahead of the DOCTYPE it is still parsing, so those
	// bytes carry references that must not escape the charge.
	backlog []byte
	err     error
}

func (c *entityChargeReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.r.Read(p)
	if n > 0 {
		// Bytes are buffered even before the table is known, because the
		// decoder reads ahead: by the time the DOCTYPE has been parsed and
		// the table installed, a good deal of the content has already
		// streamed past, and those references have to be charged too.
		// Buffering stops as soon as the table arrives, which arm drains.
		if c.t == nil {
			c.backlog = append(c.backlog, p[:n]...)
			return n, err
		}
		if cerr := c.charge(p[:n]); cerr != nil {
			c.err = cerr
			return n, cerr
		}
	}
	return n, err
}

// arm installs the entity table and charges everything read before it was
// known.
//
// Draining here rather than on the next Read is what makes the control
// enforceable. A single read can deliver the DOCTYPE and the whole body
// together — a large entity declaration guarantees it, because the decoder's
// read-ahead window is sized in bytes and one big declaration fills it along
// with everything after — and then no further Read ever comes: the backlog
// would be dropped and the budget never charged, while the decoder went on to
// expand every reference in it. The table becomes known at exactly one point
// in the parse, and the backlog is sitting here at that moment, so that is
// where it is charged: before expansion, which is the whole point of charging
// at all rather than measuring afterwards.
func (c *entityChargeReader) arm(t *entityTable) error {
	c.t = t
	if len(c.backlog) == 0 {
		return nil
	}
	b := c.backlog
	c.backlog = nil
	if err := c.charge(b); err != nil {
		c.err = err
		return err
	}
	return nil
}

// charge scans one chunk, accumulating the expanded length of every reference
// it completes.
func (c *entityChargeReader) charge(b []byte) error {
	if !c.started {
		// The subset declares the entities and may name them in default
		// values; charging there would count declarations rather than uses.
		c.seen = append(c.seen, b...)
		end := endOfInternalSubset(string(c.seen))
		if end == 0 || end >= len(c.seen) {
			// Not past the subset yet. Bounded by MaxBytes on the reader
			// beneath, so this cannot grow without limit.
			return nil
		}
		b = c.seen[end:]
		c.started = true
		c.seen = nil
	}
	buf := b
	if len(c.pend) > 0 {
		buf = append(c.pend, b...)
		c.pend = nil
	}
	for i := 0; i < len(buf); {
		if buf[i] != '&' {
			i++
			continue
		}
		j := bytes.IndexByte(buf[i:], ';')
		if j < 0 {
			// The reference is cut short by the end of the chunk. Keep it
			// for the next read rather than losing the charge. A run with no
			// ";" at all is not a reference and must not be retained without
			// bound, so only a plausible name length is carried.
			if len(buf)-i <= maxEntityNameLen {
				c.pend = append(c.pend[:0], buf[i:]...)
			}
			break
		}
		name := string(buf[i+1 : i+j])
		i += j + 1
		if name == "" || name[0] == '#' {
			continue
		}
		if _, ok := predefinedRune(name); ok {
			continue
		}
		rep, err := c.t.resolve(name)
		if err != nil {
			// Left for the decoder to report: its error names the entity and
			// the position, which is more use than one from here.
			continue
		}
		c.total += len(rep)
		if c.total > maxTotalEntityBytes {
			return fmt.Errorf(
				"entity expansion exceeds %d bytes in total: %w",
				maxTotalEntityBytes, ErrResourceLimit)
		}
	}
	return nil
}

// maxEntityNameLen bounds what a partial reference at the end of a chunk may
// carry over. An XML name has no length limit, but one long enough to exceed
// this is not a name any real document uses, and holding an unbounded run of
// bytes that merely began with "&" would be its own small leak.
const maxEntityNameLen = 1024
