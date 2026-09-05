package xdm

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// External entity resolution.
//
// This is the XXE boundary, so read the constraints before the code.
//
// An external entity — <!ENTITY e SYSTEM "other.xml"> — names a resource
// outside the document. Reading one is the attack AllowDOCTYPE exists to
// gate: it turns a parse of untrusted input into a file read, and with a
// network-capable resolver into an SSRF primitive. The default is therefore
// that they are never read, and that default is NOT changed by AllowDOCTYPE.
//
// A caller that genuinely needs them — a conformance suite, a build tool
// reading its own DTD modules — supplies an ExternalEntities resolver. Four
// properties hold, and each is enforced somewhere specific:
//
//  1. DEFAULT OFF, INDEPENDENT OF AllowDOCTYPE. The gate is a non-nil
//     ParseOptions.ExternalEntities. Its zero value is nil, so every existing
//     caller keeps today's behaviour with no code change, and a caller that
//     sets AllowDOCTYPE alone still refuses every external entity.
//
//  2. NO NETWORK. xdm imports no filesystem or network package and never
//     sees a scheme. The resolver is the caller's, and the resolver this
//     repository ships (xslt.FileResolver) rejects any non-file scheme
//     before touching the filesystem.
//
//  3. BOUNDED. Fetched bytes are charged to the document's shared expansion
//     budget at the moment they are read, BEFORE any expansion, so a chain
//     of external entities is bounded by the same maxTotalEntityBytes that
//     bounds internal ones. Fetch count and fetch depth are bounded
//     separately. A bomb assembled out of external files fails closed with
//     an error, never a hang.
//
//  4. NO PATH ESCAPE. Delegated to the resolver, which is the only component
//     that constructs a path. xdm cannot leak a path it never builds.
const (
	// maxExternalFetches bounds how many external resources one document may
	// pull in. DTD modularisation in the wild — an .ent per module, a
	// catalogue of character entities — runs to a handful; the largest in
	// the conformance suites is under ten. 64 leaves that many times over
	// while bounding fan-out from a document designed to be expensive.
	maxExternalFetches = 64

	// maxExternalFetchBytes bounds ONE fetched resource.
	//
	// It is deliberately larger than maxEntityBytes, which bounds an
	// EXPANDED entity — a different quantity measured for a different
	// reason. maxEntityBytes is small because expansion is where the
	// exponential lives: 64 KB refuses a five-level billion-laughs, and that
	// number was measured against exactly that. A fetched file expands by
	// nothing; its cost is linear in its own length, and real DTDs are
	// simply large — the TEI Lite subset in the conformance suite is 79 KB,
	// and DocBook's is larger still. Holding a fetch to the expansion cap
	// refuses ordinary documents while bounding nothing extra, since the
	// shared maxTotalEntityBytes still bounds the document as a whole and is
	// what a chain of fetches actually runs into.
	maxExternalFetchBytes = 512 << 10

	// maxExternalDepth bounds a chain of external entities pulling in
	// further external entities. It is deliberately much smaller than
	// maxEntityDepth: nesting external resources is a DTD-modularisation
	// idiom that is two or three deep in practice, and each level is a real
	// I/O operation rather than a string substitution.
	maxExternalDepth = 10
)

// EntityResolver fetches the resource an external entity or an external DTD
// subset names.
//
// It is the caller's, deliberately: xdm has no filesystem and no network, so
// every decision about what may be read — which schemes, which directories,
// how symlinks resolve — is made in code the caller owns and can audit. A
// resolver MUST refuse anything it is not certain of; returning an error
// makes the reference fail, which is the safe outcome.
//
// systemID is the system identifier exactly as the document wrote it, which
// is usually relative. base is the absolute URI of the entity that contains
// the reference, against which systemID is to be resolved — note that for an
// entity declared inside an external DTD subset this is the SUBSET's URI, not
// the document's, as XML requires.
//
// It returns the resource's content and the absolute URI it resolved to. That
// URI becomes the base for anything the fetched text itself references, so a
// resolver must return the URI it actually read, not the one it was asked
// for.
type EntityResolver interface {
	ResolveEntity(systemID, publicID, base string) (io.ReadCloser, string, error)
}

// fetchExternal reads one external resource and charges it to the budget.
//
// The charge happens BEFORE the content is expanded or scanned, and against
// the same t.total that bounds internal expansion. That ordering is the whole
// safety argument for the size bound: a 1 MB file is refused on the strength
// of its own length, without the expander ever being handed it, and a chain
// of small files that would individually pass still trips the shared cap on
// the way down. Reading is itself capped, so a resolver handing back an
// endless stream cannot hang the parse before the check runs.
func (t *entityTable) fetchExternal(systemID, publicID, base string) (string, string, error) {
	if t.resolver == nil {
		return "", "", fmt.Errorf(
			"entity with system id %q is external; external entities are "+
				"never resolved (set ParseOptions.ExternalEntities to permit)",
			systemID)
	}
	if t.fetches >= maxExternalFetches {
		return "", "", fmt.Errorf(
			"document fetches more than %d external entities: %w",
			maxExternalFetches, ErrResourceLimit)
	}
	t.fetches++
	rc, resolved, err := t.resolver.ResolveEntity(systemID, publicID, base)
	if err != nil {
		return "", "", fmt.Errorf("resolving external entity %q: %w", systemID, err)
	}
	defer rc.Close()

	// The read is bounded twice: by what the shared budget has left, and by
	// the per-entity cap. One extra byte is read so that a resource exactly
	// at the limit is distinguishable from one over it — without it a file
	// of precisely the remaining budget would be truncated silently, which
	// is worse than refusing it.
	room := maxTotalEntityBytes - t.total
	if room > maxExternalFetchBytes {
		room = maxExternalFetchBytes
	}
	if room < 0 {
		room = 0
	}
	data, err := io.ReadAll(io.LimitReader(rc, int64(room)+1))
	if err != nil {
		return "", "", fmt.Errorf("reading external entity %q: %w", systemID, err)
	}
	if len(data) > room {
		return "", "", fmt.Errorf(
			"external entity %q exceeds the remaining %d byte expansion budget: %w",
			systemID, room, ErrResourceLimit)
	}
	// Charged before expansion, per the note above.
	t.total += len(data)
	if resolved == "" {
		resolved = systemID
	}
	return string(data), resolved, nil
}

// resolveExternalText returns the replacement text of an external general
// entity, fetching it on first use and memoising the result.
//
// The memo is what keeps a document that references the same entity many
// times to one fetch, and it is also what makes the fetch budget meaningful:
// without it a reference in a loop would be a fetch in a loop.
func (t *entityTable) resolveExternalText(name string) (string, error) {
	if s, ok := t.externalText[name]; ok {
		return s, nil
	}
	d, ok := t.externalDecl[name]
	if !ok {
		return "", fmt.Errorf("entity %q is not declared", name)
	}
	if t.externalDepth >= maxExternalDepth {
		return "", fmt.Errorf(
			"external entities nested more than %d deep: %w",
			maxExternalDepth, ErrResourceLimit)
	}
	// A placeholder guards a cycle the same way resolve does for internal
	// entities: an external entity whose text references itself would
	// otherwise fetch forever.
	if t.externalText == nil {
		t.externalText = map[string]string{}
	}
	if t.fetching[name] {
		return "", fmt.Errorf("external entity %q refers to itself", name)
	}
	if t.fetching == nil {
		t.fetching = map[string]bool{}
	}
	t.fetching[name] = true
	defer delete(t.fetching, name)

	text, resolved, err := t.fetchExternal(d.systemID, d.publicID, d.base)
	if err != nil {
		return "", err
	}
	// An external parsed entity may begin with a text declaration —
	// <?xml version="1.0" encoding="..."?> — which is not part of its
	// replacement text. XML 1.0 section 4.3.1: it is stripped, or it would
	// reach the including document as a processing instruction in a place
	// no XML document may have one.
	text = stripTextDecl(text)

	// Anything the fetched text itself references resolves against the
	// entity's OWN URI, not the including document's. That is XML section
	// 4.4.3, and getting it wrong makes a nested include resolve against the
	// wrong directory.
	t.externalBase[name] = resolved
	t.externalText[name] = text
	return text, nil
}

// stripTextDecl removes an external entity's text declaration.
//
// It is only a text declaration if it is at the very start and is an XML
// declaration — "<?xml" followed by whitespace or the closing "?>". A
// processing instruction whose target merely begins with "xml", such as
// <?xml-stylesheet?>, is content and stays.
func stripTextDecl(s string) string {
	rest := strings.TrimLeft(s, " \t\r\n")
	if !strings.HasPrefix(rest, "<?xml") {
		return s
	}
	after := rest[len("<?xml"):]
	if after != "" && !isXMLSpaceByte(after[0]) && !strings.HasPrefix(after, "?>") {
		return s
	}
	end := strings.Index(rest, "?>")
	if end < 0 {
		return s
	}
	return rest[end+len("?>"):]
}

func isXMLSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// externalDecl is an external entity declaration: what it names, and the base
// URI its system identifier resolves against.
//
// The base is carried per declaration rather than taken from the document
// because a declaration inside an external DTD subset resolves against THAT
// subset, not against the document that included it.
type externalEntityDecl struct {
	systemID string
	publicID string
	base     string
}

// externalDeclOf reads an external entity declaration's system and public
// identifiers.
//
// The two forms are
//
//	name SYSTEM "uri"
//	name PUBLIC "public" "uri"
//
// An entity with an NDATA notation is unparsed and is never fetched, so it is
// not recorded here — unparsedEntityOf records those instead, for
// fn:unparsed-entity-uri to report.
func externalDeclOf(fields []string, base string) (externalEntityDecl, bool) {
	for _, f := range fields {
		if f == "NDATA" {
			return externalEntityDecl{}, false
		}
	}
	d := externalEntityDecl{base: base}
	switch fields[1] {
	case "SYSTEM":
		if len(fields) < 3 {
			return externalEntityDecl{}, false
		}
		d.systemID = unquote(fields[2])
	case "PUBLIC":
		if len(fields) < 4 {
			return externalEntityDecl{}, false
		}
		d.publicID = unquote(fields[2])
		d.systemID = unquote(fields[3])
	default:
		return externalEntityDecl{}, false
	}
	if d.systemID == "" {
		return externalEntityDecl{}, false
	}
	return d, true
}

// externalSubsetOf returns the external DTD subset a DOCTYPE names, or false
// when it names none.
//
// The declaration is "<!DOCTYPE root SYSTEM "uri" [...]>" or
// "<!DOCTYPE root PUBLIC "public" "uri" [...]>". Everything after the
// identifiers is the internal subset and is read separately; the two are
// merged with the internal subset taking precedence, since XML section 4.2
// makes the first declaration of a name bind and the internal subset is read
// first.
func externalSubsetOf(doctype string) (systemID, publicID string, ok bool) {
	// The internal subset is cut off first: it may itself contain SYSTEM and
	// PUBLIC keywords in its own declarations, and scanning the whole
	// directive would find those instead of the DOCTYPE's own.
	head := doctype
	if i := strings.IndexByte(head, '['); i >= 0 {
		head = head[:i]
	}
	fields := attListFields(head)
	// fields[0] is "DOCTYPE", fields[1] the root element name.
	for i := 2; i < len(fields); i++ {
		switch fields[i] {
		case "SYSTEM":
			if i+1 < len(fields) {
				return unquote(fields[i+1]), "", true
			}
			return "", "", false
		case "PUBLIC":
			if i+2 < len(fields) {
				return unquote(fields[i+2]), unquote(fields[i+1]), true
			}
			return "", "", false
		}
	}
	return "", "", false
}

// loadExternalSubset fetches the external DTD subset and merges the entity
// declarations it makes into the table.
//
// Three things make this more than a concatenation:
//
//   - Parameter entities. A subset is routinely a shell that does nothing but
//     "<!ENTITY % mod SYSTEM 'mod.ent'> %mod;", and the declarations only
//     exist once that reference is substituted. That substitution happens
//     here, bounded by the same budget and fetch count as everything else.
//
//   - Base URIs. A declaration read from the subset resolves its own system
//     identifier against the SUBSET's URI, not the document's, per XML
//     section 4.4.3. That is why parseEntityDecls takes a base.
//
//   - Precedence. The internal subset is read first and wins, per XML section
//     4.2: where both declare a name, the internal declaration binds.
func (t *entityTable) loadExternalSubset(systemID, publicID, base string) error {
	text, resolved, err := t.fetchExternal(systemID, publicID, base)
	if err != nil {
		return err
	}
	text = stripTextDecl(text)
	t.externalDepth++
	defer func() { t.externalDepth-- }()
	text, err = t.expandParameterEntities(text, resolved, 0)
	if err != nil {
		return err
	}
	// Retained for the attribute defaults and declared types the caller
	// reads out of it: those only exist once parameter entities have been
	// substituted, which is what the text above now is.
	t.subsetText += "\n" + text
	sub := parseEntityDecls(text, resolved)
	if sub == nil {
		return nil
	}
	t.mergeUnder(sub)
	return nil
}

// mergeUnder adds another table's declarations beneath this one's: a name this
// table already declares keeps its own definition.
//
// That is XML section 4.2's first-declaration-wins rule applied across the two
// subsets, and the direction matters — the internal subset is read first and
// therefore wins over the external one.
func (t *entityTable) mergeUnder(sub *entityTable) {
	for name, raw := range sub.raw {
		if t.external[name] {
			continue
		}
		if _, dup := t.raw[name]; !dup {
			t.raw[name] = raw
		}
	}
	for name := range sub.external {
		if _, dup := t.raw[name]; dup {
			continue
		}
		if !t.external[name] {
			t.external[name] = true
		}
	}
	for name, d := range sub.externalDecl {
		if _, dup := t.raw[name]; dup {
			continue
		}
		if t.externalDecl == nil {
			t.externalDecl = map[string]externalEntityDecl{}
		}
		if _, dup := t.externalDecl[name]; !dup {
			t.externalDecl[name] = d
		}
	}
	for name, u := range sub.unparsed {
		if t.unparsed == nil {
			t.unparsed = map[string]unparsedEntity{}
		}
		if _, dup := t.unparsed[name]; !dup {
			t.unparsed[name] = u
		}
	}
}

// expandParameterEntities substitutes parameter-entity references in a DTD
// subset, so that the declarations a referenced module makes become visible.
//
// A parameter entity is referenced as "%name;" and is the mechanism DTD
// modularisation is built on. Only references in the subset itself are
// substituted — a "%" in content means nothing and never reaches here.
//
// The recursion is bounded by maxExternalDepth on the way in, the shared byte
// budget on every fetch, and maxExternalFetches across the document. An
// internal parameter entity, which costs no fetch, is bounded by the byte
// budget alone, charged here at substitution time for exactly that reason.
func (t *entityTable) expandParameterEntities(subset, base string, depth int) (string, error) {
	if depth > maxExternalDepth {
		return "", fmt.Errorf(
			"parameter entities nested more than %d deep: %w",
			maxExternalDepth, ErrResourceLimit)
	}
	params, err := t.parseParameterDecls(subset, base)
	if err != nil {
		return "", err
	}
	if len(params) == 0 || !strings.Contains(subset, "%") {
		return subset, nil
	}
	var sb strings.Builder
	for i := 0; i < len(subset); {
		// A declaration is copied across whole. The "%" that marks a
		// parameter-entity DECLARATION is not a reference, and scanning it as
		// one is not merely a no-op: the search for the closing ";" runs past
		// the end of the declaration and swallows the real reference that
		// follows it, which is exactly the shape "<!ENTITY % e SYSTEM
		// 'e.ent'>%e;" has. Comments are skipped for the ordinary reason —
		// "%" inside one is text.
		if skip := endOfDTDConstruct(subset, i); skip > i {
			sb.WriteString(subset[i:skip])
			i = skip
			continue
		}
		if subset[i] != '%' {
			sb.WriteByte(subset[i])
			i++
			continue
		}
		j := strings.IndexByte(subset[i:], ';')
		if j < 0 {
			sb.WriteByte(subset[i])
			i++
			continue
		}
		name := subset[i+1 : i+j]
		p, ok := params[name]
		if !ok || !isEntityName(name) {
			sb.WriteString(subset[i : i+j+1])
			i += j + 1
			continue
		}
		text := p.text
		refBase := base
		if p.external {
			var resolved string
			text, resolved, err = t.fetchExternal(p.systemID, p.publicID, base)
			if err != nil {
				return "", err
			}
			text = stripTextDecl(text)
			refBase = resolved
		} else {
			// An internal parameter entity costs no fetch, so it is charged
			// here instead: without this a subset made of internal parameter
			// entities referring to one another would expand unbounded.
			t.total += len(text)
			if t.total > maxTotalEntityBytes {
				return "", fmt.Errorf(
					"entity expansion exceeds %d bytes in total: %w",
					maxTotalEntityBytes, ErrResourceLimit)
			}
		}
		// The substituted text may itself declare and reference further
		// parameter entities, which is how a chain of DTD modules works.
		text, err = t.expandParameterEntities(text, refBase, depth+1)
		if err != nil {
			return "", err
		}
		sb.WriteString(text)
		if sb.Len() > maxTotalEntityBytes {
			return "", fmt.Errorf(
				"entity expansion exceeds %d bytes in total: %w",
				maxTotalEntityBytes, ErrResourceLimit)
		}
		i += j + 1
	}
	return sb.String(), nil
}

// paramEntity is a parameter-entity declaration: internal replacement text, or
// an external identifier to fetch.
type paramEntity struct {
	text     string
	external bool
	systemID string
	publicID string
}

// parseParameterDecls reads the "<!ENTITY % name ...>" declarations out of a
// subset.
//
// These are the declarations parseEntityDecls deliberately skips: a parameter
// entity is expanded inside the DTD rather than in content, so it is only
// meaningful to the subset-level machinery here.
func (t *entityTable) parseParameterDecls(subset, base string) (map[string]paramEntity, error) {
	out := map[string]paramEntity{}
	rest := subset
	for len(out) < maxEntityCount {
		i := strings.Index(rest, "<!ENTITY")
		if i < 0 {
			break
		}
		rest = rest[i+len("<!ENTITY"):]
		end := endOfDeclaration(rest)
		if end < 0 {
			break
		}
		body := strings.TrimSpace(rest[:end])
		rest = rest[end+1:]
		if !strings.HasPrefix(body, "%") {
			continue
		}
		fields := attListFields(strings.TrimSpace(body[1:]))
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if !isEntityName(name) {
			continue
		}
		// First declaration wins here too.
		if _, dup := out[name]; dup {
			continue
		}
		switch fields[1] {
		case "SYSTEM", "PUBLIC":
			d, ok := externalDeclOf(fields, base)
			if !ok {
				continue
			}
			out[name] = paramEntity{
				external: true,
				systemID: d.systemID,
				publicID: d.publicID,
			}
		default:
			v := fields[1]
			if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
				out[name] = paramEntity{text: unquote(v)}
			}
		}
	}
	return out, nil
}

// endOfDTDConstruct returns the end of the markup declaration or comment
// beginning at i, or i itself when none begins there.
//
// Only whole constructs are skipped, so a parameter-entity reference between
// two declarations — the position where DTD modularisation puts one — is
// still seen.
func endOfDTDConstruct(s string, i int) int {
	rest := s[i:]
	if strings.HasPrefix(rest, "<!--") {
		end := strings.Index(rest[len("<!--"):], "-->")
		if end < 0 {
			return len(s)
		}
		return i + len("<!--") + end + len("-->")
	}
	if !strings.HasPrefix(rest, "<!") && !strings.HasPrefix(rest, "<?") {
		return i
	}
	end := endOfDeclaration(rest)
	if end < 0 {
		return len(s)
	}
	return i + end + 1
}

// entityBaseSpan marks a byte range of the substituted source that came from
// an external parsed entity, together with the URI that entity was read from.
//
// XML Base and the XDM both derive a node's base URI from the ENTITY it was
// written in, not from its parent in the tree: an element pulled in from
// "level1/element.xml" has that file as its base even though its parent is in
// the including document. The rewrite in substituteMarkupEntities splices the
// entity's text into the source, so after the splice the only record of where
// a byte came from is its position — hence a span.
//
// Spans nest: an external entity may itself reference another. They are
// recorded outermost first, so the LAST span containing an offset is the
// innermost entity, and that is the one whose URI applies.
type entityBaseSpan struct {
	start, end int
	base       string
}

// baseAt returns the base URI in force at a byte offset of the substituted
// source, or "" when the offset is in the document's own text.
func baseAt(spans []entityBaseSpan, off int) string {
	base := ""
	for _, s := range spans {
		if off >= s.start && off < s.end {
			base = s.base
		}
	}
	return base
}

// externalSpansIn locates the external entity references inside already
// expanded replacement text and reports where their contribution lands, so a
// nested entity's own base is recorded as well as its parent's.
//
// It re-walks the raw text rather than instrumenting expand, because expand is
// memoised: the same entity expanded twice must produce spans at both places,
// which a cache on the string cannot express.
func (t *entityTable) externalSpansIn(raw string, at int, depth int) []entityBaseSpan {
	if depth > maxEntityDepth {
		return nil
	}
	var out []entityBaseSpan
	pos := at
	for i := 0; i < len(raw); {
		if raw[i] != '&' {
			pos++
			i++
			continue
		}
		j := strings.IndexByte(raw[i:], ';')
		if j < 0 {
			pos++
			i++
			continue
		}
		name := raw[i+1 : i+j]
		// A character reference is decoded by expand into one rune; the five
		// predefined entities are left as written for the second parse to
		// decode. Both must be accounted for in the output position or every
		// span after them slides.
		if name == "" {
			pos++
			i++
			continue
		}
		if name[0] == '#' {
			if r, ok := decodeCharRef(name); ok {
				pos += utf8.RuneLen(r)
			} else {
				pos += j + 1
			}
			i += j + 1
			continue
		}
		if _, ok := predefinedRune(name); ok {
			pos += j + 1
			i += j + 1
			continue
		}
		rep, err := t.resolve(name)
		if err != nil {
			pos += j + 1
			i += j + 1
			continue
		}
		if t.external[name] {
			out = append(out, entityBaseSpan{
				start: pos, end: pos + len(rep), base: t.externalBase[name],
			})
			if inner, ok := t.externalText[name]; ok {
				out = append(out, t.externalSpansIn(inner, pos, depth+1)...)
			}
		} else if src, ok := t.raw[name]; ok {
			out = append(out, t.externalSpansIn(src, pos, depth+1)...)
		}
		pos += len(rep)
		i += j + 1
	}
	return out
}
