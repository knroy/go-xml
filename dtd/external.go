package dtd

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/knroy/go-xml/v2/internal/xmldecode"
	xml "github.com/knroy/go-xml/v2/internal/xmltok"
	"github.com/knroy/go-xml/v2/xdm"
)

// The external subset.
//
// This is the XXE boundary for this package, so the constraints come before
// the code.
//
// A DOCTYPE may name a second half of its DTD in another resource —
// <!DOCTYPE r SYSTEM "r.dtd"> — and XML 1.0 §2.8 makes that half every bit as
// binding as the internal one. Reading it turns validating an untrusted
// document into a file read, and with a network-capable resolver into an SSRF
// primitive. So it is never read unless the caller says so, in the same shape
// the rest of this repository uses: a nil Resolver on LoadOptions means
// nothing is fetched, exactly as xsd.Options.Resolver and
// xdm.ParseOptions.ExternalEntities do.
//
// Four properties hold, each enforced somewhere named:
//
//  1. DEFAULT CLOSED. LoadOptions.Resolver is nil in the zero value, and
//     Load then refuses a DOCTYPE that names an external subset rather than
//     validating against half a DTD. See the note on ErrNoResolver: the
//     refusal is loud because the quiet alternative is the exact defect this
//     repository's governing invariant forbids — "I could not read the
//     constraints" must never become "the constraints hold".
//
//  2. NO NETWORK, NO FILESYSTEM, HERE. This package constructs no path and
//     opens no socket. FileResolver, which a caller opts into, is the only
//     component that touches a disk, and it is confined to a Root.
//
//  3. BOUNDED, ON ONE BUDGET. Fetched bytes are charged to the same
//     expansion budget that bounds internal parameter entities, at the
//     moment they are read and before any expansion. A billion-laughs bomb
//     assembled half in the internal subset and half in the external one
//     therefore meets one budget rather than two — see
//     TestBombSplitAcrossSubsetsIsRefused.
//
//  4. NO SILENT PARTIAL DTD. Every failure below returns an error. There is
//     no path on which a subset that could not be read, or could not be
//     bounded, produces a *DTD that a caller would then validate against.

const (
	// DefaultMaxExternalDocuments bounds how many external resources one DTD
	// may pull in when LoadOptions.MaxExternalDocuments is zero.
	//
	// DTD modularisation in the wild runs to a handful of .ent and .mod
	// files per subset; DocBook, the largest in common use, is under fifty.
	// 64 leaves real DTDs room while bounding the fan-out of one written to
	// be expensive. It follows xsd.DefaultMaxDocuments in shape — a
	// resource bound named as an Options field, with zero meaning the
	// default — and differs in magnitude because a DTD is not a schema
	// assembly.
	DefaultMaxExternalDocuments = 64

	// DefaultMaxExternalBytes bounds the total bytes read from external
	// resources when LoadOptions.MaxExternalBytes is zero: 4 MB.
	//
	// It is a bound on the whole load rather than on one file, because a
	// resolver handing back a hundred 512 KB files is the same memory
	// exhaustion as one handing back a 50 MB file, and only a total sees
	// both. TEI Lite's subset is 79 KB and DocBook's is around 400 KB, so
	// 4 MB carries real DTDs an order of magnitude over.
	DefaultMaxExternalBytes = 4 << 20

	// DefaultMaxEntityBytes bounds the total EXPANDED size of parameter
	// entities when LoadOptions.MaxEntityBytes is zero: 1 MB.
	//
	// This is a different quantity from MaxExternalBytes and is measured for
	// a different reason. MaxExternalBytes bounds what was read; this bounds
	// what substitution produced, which is where the exponential of a
	// billion-laughs lives. Both are charged to the same counter — see
	// loader.charge — so a bomb split across the two subsets meets whichever
	// binds first rather than getting a fresh allowance per subset.
	DefaultMaxEntityBytes = 1 << 20

	// DefaultMaxParameterDepth bounds how deep parameter-entity
	// substitution may nest when LoadOptions.MaxParameterDepth is zero.
	//
	// A cycle is caught by name before this is reached (see loader.expand),
	// so this is the bound on legitimate but pathological nesting rather
	// than the recursion guard. DTD modularisation is two or three deep in
	// practice; 40 is far above anything real and far below a stack
	// problem.
	DefaultMaxParameterDepth = 40

	// DefaultMaxConditionalDepth bounds nesting of INCLUDE/IGNORE sections
	// when LoadOptions.MaxConditionalDepth is zero.
	//
	// XML 1.0 §3.4 permits them to nest arbitrarily, and DocBook nests
	// three or four deep. A document may not spend the stack doing so.
	DefaultMaxConditionalDepth = 40

	// maxDepthCeiling is the most either depth option may raise its bound
	// to; a larger value is clamped to it. Each level is a Go recursion of
	// loader.expand (and loader.conditional), a few hundred bytes of stack
	// plus the strings.Builder it fills, so 1000 levels stay three orders
	// of magnitude under Go's 1 GB stack limit, whose overflow is fatal
	// rather than recoverable. That is why neither option has an unlimited
	// setting.
	maxDepthCeiling = 1000
)

// depthBound resolves a recursion-depth option: zero or negative means def,
// and a value above maxDepthCeiling is clamped to it.
func depthBound(v, def int) int {
	if v <= 0 {
		return def
	}
	return min(v, maxDepthCeiling)
}

// ErrNoResolver reports that a DOCTYPE named an external subset and no
// Resolver was configured to read it.
//
// It is an ERROR rather than "validate against the internal subset only", and
// the choice is deliberate. A DTD is a closed description: the external subset
// routinely holds every <!ELEMENT> in the language, with the internal one
// holding a handful of overrides. Validating against the internal half alone
// would report the document valid — or report every element undeclared, which
// a caller silences with AllowUndeclared — and in neither case has anything
// been proven about the document. That is the defect class this repository's
// governing invariant names: a budget or a policy may decline to answer, but
// must never turn "I could not prove the constraint" into "the constraint
// holds".
//
// A caller who genuinely wants the internal-subset-only reading can still have
// it, and has to ask for it: Parse (which fetches nothing, and never did) is
// unchanged, and LoadOptions.InternalSubsetOnly makes Load say the same thing
// explicitly. What is refused is getting that reading by accident.
var ErrNoResolver = errors.New("no Resolver is configured")

// A Resolver turns the system identifier of an external subset, or of a
// parameter entity declared inside one, into its text.
//
// It is the caller's, deliberately: this package has no filesystem and no
// network, so every decision about what may be read — which schemes, which
// directories, how symlinks resolve — is made in code the caller owns and can
// audit. A resolver MUST refuse anything it is not certain of; returning an
// error makes the reference fail, which is the safe outcome.
//
// systemID is the identifier exactly as the DTD wrote it, usually relative.
// base is the absolute URI of the resource that contains the reference, which
// for a parameter entity declared in an external subset is that SUBSET's URI
// and not the document's — XML 1.0 §4.4.3.
//
// It returns the resource's content and the absolute URI it resolved to. That
// URI becomes the base for anything the fetched text itself references, so a
// resolver must return the URI it actually read, not the one it was asked for.
type Resolver interface {
	ResolveExternal(systemID, publicID, base string) (io.ReadCloser, string, error)
}

// LoadOptions configures Load.
//
// The zero value fetches nothing, which is the setting for a document that
// arrived over the wire.
type LoadOptions struct {
	// Resolver is how an external subset's system identifier becomes bytes.
	//
	// Nil — the zero value — means nothing is fetched, and a DOCTYPE naming
	// an external subset is refused with ErrNoResolver rather than validated
	// against half a DTD. It is off by default because a resolver hands
	// control of what this process reads to whoever wrote the DOCTYPE, which
	// for an untrusted document is the attacker. Pass a FileResolver rooted
	// at the directory the DTD really lives in to say what may be read.
	Resolver Resolver

	// BaseURI is the URI the DOCTYPE's own system identifier resolves
	// against, normally the document's. Empty is permitted; a resolver that
	// needs one will say so.
	BaseURI string

	// InternalSubsetOnly validates against the internal subset alone,
	// without fetching anything, and without the ErrNoResolver refusal.
	//
	// It exists so that the old behaviour is still reachable, but only by
	// asking for it. The resulting DTD has HasExternalSubset set, so a
	// caller can still see that it is partial, and AllowUndeclared is the
	// companion that makes a partial subset usable.
	InternalSubsetOnly bool

	// MaxExternalDocuments bounds how many external resources one load may
	// read — the subset itself and every parameter-entity module it pulls
	// in. Zero means DefaultMaxExternalDocuments; a negative value means no
	// limit, for a DTD the caller produced itself.
	MaxExternalDocuments int

	// MaxExternalBytes bounds the total bytes read from external resources.
	// Zero means DefaultMaxExternalBytes; a negative value means no limit.
	MaxExternalBytes int64

	// MaxEntityBytes bounds the total expanded size of parameter entities,
	// across both subsets. Zero means DefaultMaxEntityBytes; a negative
	// value means no limit.
	//
	// This is the billion-laughs bound. It is charged on the same counter as
	// the bytes read, so a bomb whose halves live in different subsets meets
	// one budget rather than one per subset.
	MaxEntityBytes int64

	// MaxParameterDepth bounds how deep parameter-entity references may
	// nest, a reference inside a replacement text being one level. Zero or
	// a negative value means DefaultMaxParameterDepth (40). There is no
	// unlimited setting because substitution recurses: a value above 1000
	// is clamped to 1000, which keeps the recursion far from a fatal Go
	// stack overflow. A cycle is refused by name at any depth, so raising
	// this admits only longer legitimate chains; each level costs a
	// substitution charged to MaxEntityBytes.
	MaxParameterDepth int

	// MaxConditionalDepth bounds how deep INCLUDE sections may nest (XML
	// 1.0 §3.4 sets no limit). Zero or a negative value means
	// DefaultMaxConditionalDepth (40); a value above 1000 is clamped to
	// 1000, for the same stack-safety reason as MaxParameterDepth. An
	// IGNORE section is not expanded and does not count.
	MaxConditionalDepth int
}

// Load reads a DOCTYPE, fetches the external subset it names, and returns the
// declarations of both halves.
//
// The argument is the directive text as encoding/xml hands it over, the same
// as Parse takes: "DOCTYPE name SYSTEM "..." [...]" including the brackets.
//
// Precedence is XML 1.0 §2.8: the internal subset is read FIRST and its
// declarations bind. Where both subsets declare the same element, attribute or
// parameter entity, the internal one wins and the external one is IGNORED —
// not an error, which is what §2.8 says and what makes the "internal subset as
// a set of overrides" idiom work.
//
// Everything this can decline to do, it declines loudly. There is no return
// path on which a subset that could not be read produces a DTD.
func Load(directive string, opts LoadOptions) (*DTD, error) {
	if strings.TrimSpace(directive) == "" {
		return nil, nil
	}
	head, internal := splitSubset(directive)
	systemID, publicID, hasExternal := externalSubsetOf(head)

	if hasExternal && !opts.InternalSubsetOnly && opts.Resolver == nil {
		// Loud, per the note on ErrNoResolver: half a DTD proves nothing,
		// and reporting success on it is how "could not check" becomes
		// "checked and fine".
		return nil, fmt.Errorf(
			"the DOCTYPE names the external subset %q, which cannot be read: "+
				"%w (LoadOptions.Resolver); pass a FileResolver to say what "+
				"this document may read, or set InternalSubsetOnly to "+
				"validate against the internal subset alone",
			systemID, ErrNoResolver)
	}

	l := newLoader(opts)
	l.noFetch = opts.InternalSubsetOnly
	// XML 1.0 §2.8: the internal subset is read first. Its parameter-entity
	// declarations are therefore in scope while the external subset is being
	// expanded, and they take precedence over any the external subset makes.
	// It is expanded on every path, including a DOCTYPE with no external
	// subset: "%e;" between declarations stands for the declarations its
	// replacement text holds, and those bind like any written inline. An
	// external parameter entity referenced there is fetched like any other,
	// so with no Resolver it is ErrNoResolver (see fetch).
	expandedInternal, err := l.expandSubset(internal, opts.BaseURI, false)
	if err != nil {
		return nil, err
	}
	if !hasExternal || opts.InternalSubsetOnly {
		d, err := parseSubsets(expandedInternal, "", opts)
		if err != nil {
			return nil, err
		}
		if d != nil {
			d.HasExternalSubset = hasExternal
			d.name = doctypeName(head)
			d.v11 = l.v11
			d.nesting = l.nesting
		}
		return d, nil
	}
	text, resolved, err := l.fetch(systemID, publicID, opts.BaseURI)
	if err != nil {
		return nil, err
	}
	// A parameter-entity reference at declaration level, and a conditional
	// section, are both legal only here — XML 1.0 §2.8 and §3.4 — so the
	// external subset is expanded with those enabled and the internal one
	// without.
	expandedExternal, err := l.expandSubset(text, resolved, true)
	if err != nil {
		return nil, err
	}

	// The internal text comes first in the concatenation because
	// declarations() preserves order and every merge below is
	// first-declaration-wins. That single ordering is what implements §2.8's
	// precedence for elements, attributes and entities alike.
	d, err := parseSubsets(expandedInternal+"\n"+expandedExternal, "", opts)
	if err != nil {
		return nil, err
	}
	if d != nil {
		d.HasExternalSubset = true
		d.ExternalSubset = expandedExternal
		d.name = doctypeName(head)
		d.v11 = l.v11
		d.nesting = l.nesting
	}
	return d, nil
}

// parseSubsets runs the declaration reader over already-expanded subset text.
//
// It exists so that Load's three paths — no external subset, internal only,
// and both merged — all reach the same parser, rather than one of them
// growing a second implementation that drifts.
func parseSubsets(subset, _ string, opts LoadOptions) (*DTD, error) {
	if strings.TrimSpace(subset) == "" && opts.InternalSubsetOnly {
		// A DOCTYPE that declares nothing is still a DOCTYPE; Parse's own
		// contract is that an empty directive means no constraints, and an
		// empty subset with a DOCTYPE means an empty ruleset. Deferring to
		// declarationsInto keeps the two the same.
		return newDTD(), nil
	}
	d := newDTD()
	if err := declarationsInto(d, subset); err != nil {
		return nil, err
	}
	return d, nil
}

// loader carries the one budget every read and every expansion is charged to.
//
// One counter rather than several is the whole point of the type. The bomb
// this defends against is not "a large file" or "a deep entity" but the
// product of the two, and a design with a per-file limit and a separate
// per-entity limit permits exactly the product. See the shared t.total in
// xdm/dtd_external.go, which this follows.
type loader struct {
	resolver  Resolver
	maxDocs   int
	maxBytes  int64
	maxExpand int64
	// maxParams and maxSections are LoadOptions.MaxParameterDepth and
	// MaxConditionalDepth as resolved by depthBound.
	maxParams   int
	maxSections int

	docs  int
	bytes int64
	// expanded counts substituted bytes. It is separate from bytes only so
	// that a refusal can name which limit it hit; both are charged on every
	// fetch, because a fetched module is both read and substituted.
	expanded int64

	// params holds the parameter entities in scope, first declaration
	// winning. The internal subset populates it before the external subset
	// is looked at, which is how §2.8's precedence reaches parameter
	// entities specifically.
	params map[string]paramEntity

	// noFetch leaves an external parameter-entity reference as written
	// instead of reading it, for LoadOptions.InternalSubsetOnly, which
	// promises to fetch nothing.
	noFetch bool

	// inDecl is set while the body of a markup declaration other than
	// <!ENTITY> is expanded: the context in which a replacement text must
	// nest properly with the declaration and its groups. An entity value
	// is a literal, where any text may be included.
	inDecl bool

	// nesting holds the PE nesting validity errors found while expanding,
	// first occurrence of each only, for Validate to report. See
	// nestingViolation.
	nesting []string
	// nestingSeen is the set of nesting, for violation's once-per-message
	// rule. Scanning the list instead cost the square of the messages.
	nestingSeen map[string]struct{}

	// sections counts the INCLUDE sections currently being expanded, for
	// maxSections.
	sections int

	// v11 is the system identifier of the first external resource read
	// whose text declaration says version="1.1", or "". See fetch.
	v11 string

	// active names the parameter entities currently being substituted. A
	// name already in it is a cycle — "%a;" inside the replacement text of
	// "a", directly or through any chain — and is refused rather than
	// recursed into. XML 1.0 §4.1 forbids recursion outright, so this is a
	// well-formedness error and not merely a budget.
	active map[string]bool
}

// paramEntity is one <!ENTITY % name ...> declaration.
type paramEntity struct {
	text     string
	external bool
	systemID string
	publicID string
	// base is the URI the systemID resolves against: the subset the
	// declaration was read from, not the document. XML 1.0 §4.4.3.
	base string
}

func newLoader(opts LoadOptions) *loader {
	l := &loader{
		resolver:  opts.Resolver,
		maxDocs:   opts.MaxExternalDocuments,
		maxBytes:  opts.MaxExternalBytes,
		maxExpand: opts.MaxEntityBytes,
		params:    map[string]paramEntity{},
		active:    map[string]bool{},

		maxParams:   depthBound(opts.MaxParameterDepth, DefaultMaxParameterDepth),
		maxSections: depthBound(opts.MaxConditionalDepth, DefaultMaxConditionalDepth),
	}
	if l.maxDocs == 0 {
		l.maxDocs = DefaultMaxExternalDocuments
	}
	if l.maxBytes == 0 {
		l.maxBytes = DefaultMaxExternalBytes
	}
	if l.maxExpand == 0 {
		l.maxExpand = DefaultMaxEntityBytes
	}
	return l
}

// room reports how many more bytes may be read, and whether a limit applies.
func (l *loader) room() (int64, bool) {
	unlimited := l.maxBytes < 0 && l.maxExpand < 0
	if unlimited {
		return 0, false
	}
	r := int64(1) << 40
	if l.maxBytes >= 0 && l.maxBytes-l.bytes < r {
		r = l.maxBytes - l.bytes
	}
	if l.maxExpand >= 0 && l.maxExpand-l.expanded < r {
		r = l.maxExpand - l.expanded
	}
	if r < 0 {
		r = 0
	}
	return r, true
}

// chargeExpansion adds n substituted bytes to the shared budget.
//
// It is called for an INTERNAL parameter entity, which costs no fetch and
// would otherwise be unbounded: a subset made of internal parameter entities
// referring to one another expands exponentially while reading nothing. This
// is the half of the billion-laughs defence that the fetch bound does not
// cover.
func (l *loader) chargeExpansion(n int) error {
	if l.maxExpand < 0 {
		return nil
	}
	l.expanded += int64(n)
	if l.expanded > l.maxExpand {
		return fmt.Errorf(
			"parameter-entity expansion exceeds %d bytes in total: %w",
			l.maxExpand, xdm.ErrResourceLimit)
	}
	return nil
}

// fetch reads one external resource and charges it to the budget.
//
// The charge happens BEFORE the text is scanned or expanded, and against the
// same counters that bound internal expansion. That ordering is the whole
// safety argument for the size bound: an oversized resource is refused on the
// strength of its own length, without the expander ever being handed it, and a
// chain of small resources that would each pass still trips the shared cap on
// the way down. The read itself is capped, so a resolver returning an endless
// stream cannot hang the load before the check runs.
func (l *loader) fetch(systemID, publicID, base string) (text, resolved string, err error) {
	if l.resolver == nil {
		// Unreachable from Load, which refuses earlier; kept because a
		// parameter entity declared SYSTEM must fail closed on every path,
		// not only the one that happens to be checked.
		return "", "", fmt.Errorf(
			"external resource %q cannot be read: %w", systemID, ErrNoResolver)
	}
	if l.maxDocs >= 0 && l.docs >= l.maxDocs {
		return "", "", fmt.Errorf(
			"the DTD reads more than %d external resources: %w",
			l.maxDocs, xdm.ErrResourceLimit)
	}
	l.docs++

	rc, got, err := l.resolver.ResolveExternal(systemID, publicID, base)
	if err != nil {
		return "", "", fmt.Errorf("resolving external subset %q: %w", systemID, err)
	}
	if rc == nil {
		// A nil reader with a nil error would otherwise be an empty DTD,
		// which is the silent-partial-DTD outcome under another name.
		return "", "", fmt.Errorf(
			"resolver returned no content for external subset %q", systemID)
	}
	defer rc.Close()

	limit, bounded := l.room()
	var data []byte
	if bounded {
		// One extra byte is read so that a resource exactly at the limit is
		// distinguishable from one over it. Without it a resource of
		// precisely the remaining budget would be truncated silently, and a
		// truncated DTD is a partial DTD.
		data, err = io.ReadAll(io.LimitReader(rc, limit+1))
	} else {
		data, err = io.ReadAll(rc)
	}
	if err != nil {
		return "", "", fmt.Errorf("reading external subset %q: %w", systemID, err)
	}
	if bounded && int64(len(data)) > limit {
		return "", "", fmt.Errorf(
			"external subset %q exceeds the remaining %d byte budget: %w",
			systemID, limit, xdm.ErrResourceLimit)
	}
	l.bytes += int64(len(data))
	// An external entity carries its own encoding (XML 1.0 §4.3.3), and a
	// UTF-16 one begins with a byte order mark. It is decoded the way xdm
	// decodes a document, by the same code, and the mark is dropped.
	dec, err := xmldecode.Reader(bufio.NewReader(bytes.NewReader(data)))
	if err == nil {
		data, err = io.ReadAll(dec)
	}
	if err != nil {
		return "", "", fmt.Errorf("decoding external resource %q: %w", systemID, err)
	}
	// Charged before expansion, per the note above. Both counters move: the
	// bytes were read, and they are also about to be substituted into the
	// subset, so a bomb cannot launder its size by moving a level into a
	// file. The expansion charge is the DECODED size, since UTF-16 text can
	// grow by half as UTF-8 and the budget bounds what the expander is handed.
	if err := l.chargeExpansion(len(data)); err != nil {
		return "", "", err
	}
	if got == "" {
		got = systemID
	}
	text, version := stripTextDecl(string(data))
	// XML 1.1 §4.3.4: "If an entity (including the document entity) is not
	// labeled with a version number, it is treated as if labeled as version
	// 1.0", and a 1.1 entity may be included only by a 1.1 document. The
	// document is not in view here (Load takes the DOCTYPE alone), so a 1.1
	// entity is recorded for Validate, which has the document, and a version
	// neither 1.0 nor 1.1 is refused now, as xdm refuses it.
	switch version {
	case "", "1.0":
	case "1.1":
		if l.v11 == "" {
			l.v11 = systemID
		}
	default:
		return "", "", fmt.Errorf("external resource %q declares unsupported XML version %q (XML §4.3.4)", systemID, version)
	}
	return text, got, nil
}

// expandSubset substitutes parameter-entity references in one subset and, when
// the subset is external, resolves its conditional sections — both in one
// pass, in document order, which is the order XML 1.0 gives them meaning in.
// A parameter entity is in scope from its declaration on (§4.1 WFC/VC Entity
// Declared), a declaration inside an INCLUDE section is an ordinary
// declaration, and one inside an IGNORE section is not read at all (§3.4).
// Collecting declarations over the whole subset first got all three wrong: it
// could not see inside "<![", and would have seen into an IGNORE if it had.
//
// declLevel says which subset this is. XML 1.0 §2.8 draws a real line: in the
// INTERNAL subset a parameter-entity reference may appear only where a whole
// markup declaration could not — that is, not inside one and not in place of
// one — while in the EXTERNAL subset a reference may expand to whole
// declarations, which is the mechanism DTD modularisation is built on.
// Conditional sections (§3.4) are external-subset-only for the same reason.
func (l *loader) expandSubset(subset, base string, declLevel bool) (string, error) {
	return l.expand(subset, base, 0, declLevel)
}

// collectParams records the parameter-entity declarations a subset makes.
//
// First declaration wins, and the map is shared across both subsets, so the
// internal subset's declarations survive an external subset that redeclares
// them — XML 1.0 §2.8 again, and the reason this is one map rather than one
// per subset.
func (l *loader) collectParams(subset, base string) {
	for _, decl := range declarations(subset) {
		if !strings.HasPrefix(decl, "ENTITY") {
			continue
		}
		body := strings.TrimSpace(decl[len("ENTITY"):])
		if !strings.HasPrefix(body, "%") {
			continue
		}
		f := attFields(strings.TrimSpace(body[1:]))
		if len(f) < 2 {
			continue
		}
		name := f[0]
		if !isEntityName(name) {
			continue
		}
		if _, dup := l.params[name]; dup {
			continue
		}
		switch f[1] {
		case "SYSTEM":
			if len(f) >= 3 {
				l.params[name] = paramEntity{
					external: true, systemID: unquote(f[2]), base: base}
			}
		case "PUBLIC":
			if len(f) >= 4 {
				l.params[name] = paramEntity{
					external: true, publicID: unquote(f[2]),
					systemID: unquote(f[3]), base: base}
			}
		default:
			if v := f[1]; len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
				l.params[name] = paramEntity{text: unquote(v), base: base}
			}
		}
	}
}

// expand substitutes "%name;" references.
//
// Two things make this more than a string replace:
//
//   - The "%" that introduces a parameter-entity DECLARATION is not a
//     reference. Scanning it as one is not a harmless no-op: the search for
//     the closing ";" runs past the end of "<!ENTITY % e SYSTEM 'e.ent'>" and
//     swallows the "%e;" that follows it, which is exactly the shape DTD
//     modularisation has. Whole declarations and comments are therefore
//     copied across untouched — except in the external subset, where a
//     reference INSIDE a declaration is legal and is handled by expanding the
//     declaration's own body.
//
//   - Recursion is refused by name, not merely bounded. XML 1.0 §4.1 makes a
//     recursive entity a well-formedness error, and l.active detects a cycle
//     of any length on the first revisit rather than after maxParams
//     rounds of doubling.
func (l *loader) expand(subset, base string, depth int, declLevel bool) (string, error) {
	if depth > l.maxParams {
		return "", fmt.Errorf(
			"parameter entities nested more than %d deep: %w",
			l.maxParams, xdm.ErrResourceLimit)
	}
	if !strings.Contains(subset, "%") && !(declLevel && strings.Contains(subset, "<![")) {
		return subset, nil
	}
	var sb strings.Builder
	for i := 0; i < len(subset); {
		if declLevel && strings.HasPrefix(subset[i:], "<![") {
			n, err := l.conditional(&sb, subset[i:], base, depth, declLevel)
			if err != nil {
				return "", err
			}
			i += n
			continue
		}
		if end := endOfConstruct(subset, i); end > i {
			construct := subset[i:end]
			// A parameter entity comes into scope as its declaration is
			// passed, never earlier; first declaration wins.
			l.collectParams(construct, base)
			// In the external subset a reference may appear inside a
			// declaration — "<!ELEMENT r %model;>" is the idiom — so the
			// body is expanded while the delimiters are kept. In the
			// internal subset §2.8 forbids that, and the declaration is
			// copied whole.
			if declLevel && strings.HasPrefix(construct, "<!") &&
				!strings.HasPrefix(construct, "<!--") &&
				strings.Contains(construct, "%") {
				// A declaration whose '>' is not in the raw text — it is in
				// a replacement text, which is a nesting error — runs to the
				// end of the subset, and has no '>' to strip.
				body, closer := construct[2:], ""
				if strings.HasSuffix(body, ">") {
					body, closer = body[:len(body)-1], ">"
				}
				outer := l.inDecl
				l.inDecl = !strings.HasPrefix(body, "ENTITY")
				inner, err := l.expand(body, base, depth+1, declLevel)
				l.inDecl = outer
				if err != nil {
					return "", err
				}
				sb.WriteString("<!" + inner + closer)
			} else {
				sb.WriteString(construct)
			}
			i = end
			continue
		}
		if subset[i] != '%' {
			sb.WriteByte(subset[i])
			i++
			continue
		}
		j := strings.IndexByte(subset[i:], ';')
		if j < 0 {
			// No ';' remains, so no reference in the tail can close, and no
			// declaration in it can hold one either. Copying the tail whole
			// keeps this linear: stepping one byte and rescanning for ';' at
			// every later '%' cost the square of the subset.
			sb.WriteString(subset[i:])
			break
		}
		name := subset[i+1 : i+j]
		p, ok := l.params[name]
		if !ok || !isEntityName(name) {
			// An undeclared reference is left as written rather than being
			// an error: the internal subset of a real document routinely
			// contains a "%" that is not a reference at all, and refusing
			// there would reject documents this package is only asked to
			// validate.
			sb.WriteString(subset[i : i+j+1])
			i += j + 1
			continue
		}
		if l.active[name] {
			return "", fmt.Errorf(
				"parameter entity %%%s; is recursive", name)
		}
		if p.external && l.noFetch {
			sb.WriteString(subset[i : i+j+1])
			i += j + 1
			continue
		}
		text, refBase := p.text, p.base
		if p.external {
			var err error
			text, refBase, err = l.fetch(p.systemID, p.publicID, p.base)
			if err != nil {
				return "", err
			}
		}
		l.active[name] = true
		text, err := l.expand(text, refBase, depth+1, declLevel)
		delete(l.active, name)
		if err != nil {
			return "", err
		}
		// The EXPANDED length is what is charged, and it is charged to the
		// loader rather than to this builder. Both halves of that matter.
		//
		// Charging the raw replacement text instead would charge a
		// billion-laughs ladder a few dozen bytes a rung while it doubles:
		// "%a8;%a8;..." is thirty characters however large %a8; expands to,
		// and it is the expansion that costs the memory. Charging a
		// per-call builder instead of the loader would give every nested
		// call and every subset a fresh allowance, which is the same defect
		// one level up — a bomb split across the two subsets, or across two
		// declarations, would meet the budget twice over and pass.
		//
		// A reference is charged at least its own length, so one that
		// expands to nothing is not free: a ladder of rungs "%aN;%aN;" over
		// an empty a0 doubles its calls a rung while producing no bytes, and
		// without this floor the budget never moved.
		//
		// An internal entity is charged at least its raw replacement text
		// too, which was scanned to produce text: at declaration level an
		// IGNORE section reduces any amount of it to nothing, and a ladder
		// of references to one scanned seconds of text per few bytes
		// charged. A fetched one was charged its raw text by fetch, and
		// p.text is empty for it.
		if err := l.chargeExpansion(max(len(text), len(p.text), j+1)); err != nil {
			return "", err
		}
		if l.inDecl {
			if vc := nestingViolation(text); vc != "" {
				l.violation("the replacement text of %%%s; is not properly nested (XML 1.0 %s)", name, vc)
			}
		}
		sb.WriteString(text)
		i += j + 1
	}
	return sb.String(), nil
}

// conditional resolves the INCLUDE or IGNORE section that begins s, writes an
// INCLUDE's expanded contents to sb, and returns how many bytes of s it
// consumed.
//
// XML 1.0 §3.4:
//
//	conditionalSect ::= includeSect | ignoreSect
//	includeSect     ::= '<![' S? 'INCLUDE' S? '[' extSubsetDecl ']]>'
//	ignoreSect      ::= '<![' S? 'IGNORE'  S? '[' ignoreSectContents ']]>'
//
// They are external-subset-only, and the keyword is usually a parameter
// entity — "<![%draft;[ ... ]]>" with "%draft;" declared as "INCLUDE" or
// "IGNORE" — so the keyword is expanded before it is read.
//
// Nesting is the part that has to be right: the contents of an IGNORE section
// are not parsed as declarations, but its "<![" and "]]>" delimiters still
// have to be counted, or the first "]]>" of an inner section would end the
// outer one and the declarations after it would be read when they should not
// be. §3.4 says exactly that.
func (l *loader) conditional(sb *strings.Builder, s, base string, depth int, declLevel bool) (int, error) {
	rest := s[len("<!["):]
	// The keyword is read up to its '[', expanding references as they come,
	// so that a '[' supplied by the keyword's own entity is found. That is
	// §3.4 VC Proper Conditional Section/PE Nesting — the "<![", "[" and
	// "]]>" must all be in one replacement text — and a validity error, so
	// the section is still read.
	var kwb strings.Builder
	open, extra := 0, ""
	// semi is the first ';' at or after the last search, len(rest) when
	// there is none. Searching at every byte cost the square of the
	// keyword; searching only at a '%' it has passed keeps the searches
	// disjoint, as in expand.
	semi := -1
	for open < len(rest) && rest[open] != '[' {
		if rest[open] == '%' && semi < open {
			if semi = strings.IndexByte(rest[open:], ';'); semi < 0 {
				semi = len(rest)
			} else {
				semi += open
			}
		}
		if j := semi - open; rest[open] == '%' && semi < len(rest) && j > 0 {
			t, err := l.expand(rest[open:open+j+1], base, depth+1, declLevel)
			if err != nil {
				return 0, err
			}
			open += j + 1
			if k := strings.IndexByte(t, '['); k >= 0 {
				l.violation("a conditional section's '[' is in the replacement text of %s (XML 1.0 §3.4 VC Proper Conditional Section/PE Nesting)", rest[open-j-1:open])
				kwb.WriteString(t[:k])
				extra = t[k+1:]
				open--
				break
			}
			kwb.WriteString(t)
			continue
		}
		kwb.WriteByte(rest[open])
		open++
	}
	if open >= len(rest) {
		return 0, fmt.Errorf("a conditional section is missing its '['")
	}
	kw := strings.Trim(kwb.String(), " \t\r\n")
	if kw != "INCLUDE" && kw != "IGNORE" {
		// Not a conditional section — a CDATA section cannot appear in a
		// DTD, but an unexpanded "%kw;" can when the entity was never
		// declared. Copying it through leaves the document less
		// constrained rather than wrongly refused, which is the rule the
		// rest of this package follows.
		sb.WriteString("<![")
		return len("<!["), nil
	}
	body := rest[open+1:]
	end := matchingSectionEnd(body)
	if end < 0 {
		return 0, fmt.Errorf("a conditional section is not closed by ']]>'")
	}
	// An IGNORE contributes nothing. Its contents are not even parsed, per
	// §3.4 — only its delimiters were counted, which matchingSectionEnd did.
	if kw == "INCLUDE" {
		if l.sections >= l.maxSections {
			return 0, fmt.Errorf(
				"conditional sections nested more than %d deep: %w",
				l.maxSections, xdm.ErrResourceLimit)
		}
		// The contents of an INCLUDE are ordinary declarations, and may
		// contain further conditional sections of their own.
		l.sections++
		inner, err := l.expand(extra+body[:end], base, depth, declLevel)
		l.sections--
		if err != nil {
			return 0, err
		}
		sb.WriteString(inner)
	}
	return len("<![") + open + 1 + end + len("]]>"), nil
}

// violation records a PE nesting validity error, once per message.
func (l *loader) violation(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if _, dup := l.nestingSeen[msg]; dup {
		return
	}
	if l.nestingSeen == nil {
		l.nestingSeen = map[string]struct{}{}
	}
	l.nestingSeen[msg] = struct{}{}
	l.nesting = append(l.nesting, msg)
}

// nestingViolation reports which nesting constraint the replacement text of a
// parameter entity referenced inside a markup declaration breaks, or "".
//
// XML 1.0 §2.8 VC Proper Declaration/PE Nesting: "if either the first
// character or the last character of a markup declaration ... is contained in
// the replacement text for a parameter-entity reference, both MUST be
// contained in the same replacement text." Inside a declaration's body that
// means the text holds no '<' or '>' outside a literal.
//
// §3.2.1 VC Proper Group/PE Nesting: "if either of the opening or closing
// parentheses in a choice, seq, or Mixed construct is contained in the
// replacement text for a parameter entity, both MUST be contained in the same
// replacement text": its parentheses balance.
func nestingViolation(text string) string {
	depth := 0
	for i := 0; i < len(text); i++ {
		switch c := text[i]; c {
		case '"', '\'':
			j := strings.IndexByte(text[i+1:], c)
			if j < 0 {
				return ""
			}
			i += j + 1
		case '(':
			depth++
		case ')':
			if depth--; depth < 0 {
				return "§3.2.1 VC Proper Group/PE Nesting"
			}
		case '<', '>':
			return "§2.8 VC Proper Declaration/PE Nesting"
		}
	}
	if depth != 0 {
		return "§3.2.1 VC Proper Group/PE Nesting"
	}
	return ""
}

// matchingSectionEnd returns the offset of the "]]>" that closes the section
// whose contents begin at the start of s, counting nested "<![" as §3.4
// requires. It returns -1 when the section is unclosed.
func matchingSectionEnd(s string) int {
	depth := 0
	for i := 0; i+2 <= len(s); {
		switch {
		case strings.HasPrefix(s[i:], "<!["):
			depth++
			i += len("<![")
		case strings.HasPrefix(s[i:], "]]>"):
			if depth == 0 {
				return i
			}
			depth--
			i += len("]]>")
		default:
			i++
		}
	}
	return -1
}

// endOfConstruct returns the end of the markup declaration, comment or
// processing instruction beginning at i, or i itself when none begins there.
//
// A conditional section's "<![" is deliberately NOT a construct: its contents
// are declarations that must be expanded, and skipping it whole would leave
// every parameter entity inside an INCLUDE unsubstituted.
func endOfConstruct(s string, i int) int {
	rest := s[i:]
	if strings.HasPrefix(rest, "<![") {
		return i
	}
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

// endOfDeclaration returns the offset of the '>' that ends the declaration
// whose body begins at the start of s, or -1.
//
// A quoted literal and a parenthesised model may both contain '>', which is
// why this is not IndexByte: "<!ATTLIST a b CDATA '>'>" is one declaration.
func endOfDeclaration(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\'':
			j := strings.IndexByte(s[i+1:], c)
			if j < 0 {
				return -1
			}
			i += j + 1
		case '(':
			depth++
		case ')':
			depth--
		case '>':
			if depth <= 0 {
				return i
			}
		}
	}
	return -1
}

// stripTextDecl removes an external subset's text declaration, and returns
// the version it declares ("" when it names none).
//
// XML 1.0 §4.3.1: "<?xml version='1.0' encoding='...'?>" at the very start of
// an external parsed entity is a text declaration and is not part of the
// entity's content. Leaving it would put a processing instruction where the
// declaration reader would try to read one as markup.
func stripTextDecl(s string) (text, version string) {
	rest := strings.TrimLeft(s, " \t\r\n")
	if !strings.HasPrefix(rest, "<?xml") {
		return s, ""
	}
	after := rest[len("<?xml"):]
	// <?xml-stylesheet?> is content, not a text declaration: the target
	// merely begins with "xml".
	if after != "" && !isSpace(rune(after[0])) && !strings.HasPrefix(after, "?>") {
		return s, ""
	}
	end := strings.Index(rest, "?>")
	if end < 0 {
		return s, ""
	}
	// [77] TextDecl: VersionInfo is optional and comes first when present.
	version, _, _, _ = xml.ParseXMLDecl(strings.Trim(rest[len("<?xml"):end], " \t\r\n"))
	return rest[end+len("?>"):], version
}

// externalSubsetOf returns the external subset a DOCTYPE head names.
//
// The head must already have had its internal subset cut off, since that
// subset contains SYSTEM and PUBLIC keywords of its own and scanning the whole
// directive would find those instead of the DOCTYPE's.
func externalSubsetOf(head string) (systemID, publicID string, ok bool) {
	f := attFields(head)
	// f[0] is "DOCTYPE", f[1] the root element name.
	for i := 2; i < len(f); i++ {
		switch f[i] {
		case "SYSTEM":
			if i+1 < len(f) {
				return unquote(f[i+1]), "", true
			}
			return "", "", false
		case "PUBLIC":
			if i+2 < len(f) {
				return unquote(f[i+2]), unquote(f[i+1]), true
			}
			return "", "", false
		}
	}
	return "", "", false
}

// isEntityName reports whether s is plausibly an entity name, which is what
// separates a real "%name;" reference from a stray "%" followed by a ";"
// somewhere later in the subset.
func isEntityName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '-', r == '_', r == ':', r >= 0x80:
		default:
			return false
		}
	}
	return true
}
