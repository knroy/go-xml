package xdm

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/url"
	"strings"

	xml "github.com/knroy/go-xml/v2/internal/xmltok"
)

// ParseOptions controls document construction.
type ParseOptions struct {
	// BaseURI is recorded on the document node and used to resolve relative
	// references in fn:document and xsl:include.
	BaseURI string

	// DocumentURI is recorded on the document node as its dm:document-uri
	// property, which is what fn:document-uri returns. It is separate from
	// BaseURI because the two accessors are separate in the data model: see
	// Node.DocumentURI for why they cannot be the same field.
	//
	// It defaults to empty, which is the right answer for every caller that
	// is parsing something it did not retrieve by URI — a stylesheet string,
	// a re-parsed entity expansion, a test fixture. A caller that DID fetch
	// the document from a URI, and that registers it in a document pool so
	// that fn:doc of the same URI returns this same tree, sets it to that URI.
	DocumentURI string

	// StripSpace removes whitespace-only text nodes. XSLT applies this per
	// element name via xsl:strip-space, so the transform layer passes a
	// predicate; a plain bool here would not express "strip in these elements
	// only".
	StripSpace func(elem QName) bool

	// AllowDOCTYPE permits a DOCTYPE declaration. It defaults to false: a
	// DOCTYPE is the entry point for both XXE (parser-executed file:// and
	// http:// reads) and entity-expansion blowup, and a validator that
	// happily expands entities from untrusted input is a liability. Callers
	// that genuinely need DTD-declared entities opt in explicitly.
	AllowDOCTYPE bool

	// ExternalEntities permits external entities — those declared SYSTEM or
	// PUBLIC, and an external DTD subset — to be read, by supplying the
	// resolver that reads them.
	//
	// It is nil by default, and nil means every external entity is refused
	// exactly as before. It is deliberately SEPARATE from AllowDOCTYPE and
	// is not implied by it: AllowDOCTYPE admits a DOCTYPE and its internal
	// declarations, which cost nothing outside the document, while this
	// admits reads of other resources — the XXE surface proper. A caller
	// that wants entity declarations does not thereby want file reads.
	//
	// xdm has no filesystem and no network, so it can only read what a
	// resolver hands it. Confinement — permitted schemes, permitted
	// directories, symlink resolution — is entirely the resolver's, and
	// xslt.FileResolver implements it. Expansion remains bounded by this
	// package: fetched bytes are charged to the document's shared budget
	// before they are expanded, and the number and nesting of fetches are
	// capped. See xdm/dtd_external.go.
	ExternalEntities EntityResolver

	// TrackPositions records where each element starts, so that a validator
	// can report the line a failure occurred on. It retains the source text
	// for the life of the tree, which measures at about 10% more memory on a
	// typical invoice and no extra parse time. It is opt-in because that cost
	// buys nothing for a caller that never asks for a position.
	TrackPositions bool

	// MaxDepth bounds nesting. Deeply nested input is the cheapest way to
	// drive a recursive descent into stack exhaustion, so the limit is
	// enforced during construction rather than left to the runtime.
	MaxDepth int

	// MaxBytes bounds the source document. Zero means DefaultMaxBytes;
	// a negative value means no limit, for a caller reading input it
	// produced itself.
	MaxBytes int64

	// MaxNodes bounds the tree. Zero means DefaultMaxNodes; a negative
	// value means no limit.
	//
	// Both limits exist because neither alone is a memory bound. A node
	// costs a fixed ~200 bytes whatever it contains, so the heap a document
	// needs depends on how many nodes it has rather than how long it is:
	// a megabyte of "<a/>" is fifty times the memory of a megabyte of text.
	// MaxBytes bounds the read; MaxNodes bounds what the read can allocate.
	MaxNodes int

	// entitiesExpanded marks the second parse of a document whose entities
	// held markup: their references are already substituted, so the DOCTYPE's
	// entity declarations must not be applied again.
	//
	// It is unexported because it is not a choice a caller makes. Without it
	// the second parse would re-expand text that is already expanded, and an
	// entity whose replacement mentions another would double.
	entitiesExpanded bool

	// entityBases maps byte ranges of the substituted source to the external
	// entity each came from, so that a node built from entity text gets that
	// entity's URI as its base rather than the including document's.
	//
	// It is unexported for the same reason as entitiesExpanded: it describes
	// the source this parse was handed, not a choice a caller makes.
	entityBases []entityBaseSpan

	// entityBudget is the expansion spend this parse shares with the document
	// it is part of. Nil means this parse is a document in its own right and
	// starts with the full maxTotalEntityBytes.
	//
	// It exists because a parse is not always a document. ProcessXInclude
	// parses each included resource with ParseString, and every one of those
	// parses used to mint its own budget — so the ceiling that bounds one
	// document bounded each of two hundred of them separately, and 95 KB of
	// source expanded to 149 MB. Passing the outer budget down makes the
	// bound say what it is documented to say. The include fetch counter is
	// shared across the same boundary already, by living on the one
	// includeProc; this is the same sharing for the counter that has no such
	// object to live on.
	//
	// It is unexported for the same reason as the two fields above: it
	// describes this parse's place in a larger document, not a knob.
	entityBudget *entityBudget
}

// Limits applied when the corresponding ParseOptions field is zero.
const (
	// DefaultMaxDepth is the nesting limit.
	DefaultMaxDepth = 1000

	// DefaultMaxBytes is the source-size limit: 64 MB, far above any
	// schema or stylesheet and above most real instance documents, while
	// still bounding what a single parse can be asked to read.
	DefaultMaxBytes int64 = 64 << 20

	// DefaultMaxNodes is the node-count limit. At roughly 200 bytes a node
	// this bounds a tree to about 2 GB, which is the point of it: the
	// number is chosen to bound *memory*, and it is the limit that actually
	// binds on the documents designed to be expensive.
	DefaultMaxNodes = 10_000_000
)

// Parse builds an XDM tree from an XML document.
//
// It uses encoding/xml as a tokeniser only. The Go decoder's own namespace
// handling is not usable here: it resolves prefixes into Name.Space but
// discards the prefix and the declarations themselves, and XSLT needs both —
// namespace nodes are addressable on the namespace axis, and a literal result
// element must be serialised with the prefix the author wrote.
func Parse(r io.Reader, opts ParseOptions) (*Tree, error) {
	// The byte limit wraps the reader FIRST, ahead of every other wrapper,
	// so that it bounds the raw source as ParseOptions.MaxBytes says it
	// does: what is read, not what a caller remembered to check. One byte
	// over the limit is read deliberately: hitting it is then
	// distinguishable from a document that happens to be exactly the
	// maximum size.
	//
	// Ordering is load-bearing. The UTF-16 decoder beneath decodeReader
	// reads its whole input in one io.ReadAll — it has to, because the
	// encoding declaration it rewrites sits at the front of text a
	// streaming decoder would already have handed on — so wrapping the
	// limit outside it merely counted bytes that had already been pulled in
	// and decoded. Measured: an 8 MB UTF-16 document allocated 138 MB
	// against a MaxBytes of 1024 before being refused, which is a refusal
	// that costs more than accepting. Wrapped here the ReadAll hits the
	// limited reader and stops at the bound.
	// A reader that knows its length (strings.Reader, bytes.Reader) lets the
	// position-tracking copy below be sized once instead of grown by doubling.
	sizeHint := 0
	if l, ok := r.(interface{ Len() int }); ok {
		sizeHint = l.Len()
	}
	maxBytes := opts.MaxBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}
	if maxBytes > 0 {
		// maxBytes+1 overflows to a negative limit at math.MaxInt64, and
		// io.LimitReader treats that as "nothing left", so the largest limit a
		// caller can name refused every document with "no root element". The
		// saturating form keeps the over-read where it fits and drops it where
		// no document can reach the limit anyway.
		lim := maxBytes
		if lim < math.MaxInt64 {
			lim++
		}
		r = &countingReader{r: io.LimitReader(r, lim), max: maxBytes}
	}

	// Position tracking needs the source text to count lines in, and the
	// reader is consumed by the decoder. Tee it into a buffer rather than
	// reading it all up front, so that a parse failing early on a huge
	// document does not first pull the whole thing into memory.
	// UTF-16 is decoded to UTF-8 first. XML 1.0 §4.3.3 makes both encodings
	// mandatory, and encoding/xml reads only UTF-8 — so without this a
	// UTF-16 document fails with "invalid UTF-8" rather than being read.
	// This happens before the tee, so that position tracking counts lines
	// in the text the decoder actually sees.
	// The read windows are sized to a document known to be small: a 2 KB
	// document otherwise paid for three 4 KB buffers it never filled.
	window := readWindow(sizeHint)
	decoded, err := decodeReader(r, window)
	if err != nil {
		return nil, fmt.Errorf("parse XML: %w", err)
	}
	r = decoded

	// XML 1.0 section 2.11: line ends are normalized on input, before
	// parsing. See xdm/lineend.go. Attribute-value normalization (section
	// 3.3.3) is the tokeniser's, which still sees "&#10;" apart from a
	// newline the author typed.
	r = newLineEndReader(r, window)

	trackPos := opts.TrackPositions
	var srcBuf strings.Builder
	// The source is kept when positions are tracked, and also when a DOCTYPE
	// is permitted: an entity whose replacement text holds markup forces a
	// re-parse of the substituted source, and by the time that is known the
	// reader is partly consumed and the decoder has buffered ahead into it.
	//
	// The copy is needed only until the document element opens: a re-parse
	// can start only at the DOCTYPE, which must come first, so past that point
	// the tee stops and its copy is dropped unless positions are tracked.
	// entitiesExpanded marks the second parse, which has no entities left to
	// find and so needs no copy at all.
	// Only a tracked copy is kept to the end, so only it is sized up front;
	// never past MaxBytes, which the reader will refuse to go beyond.
	if trackPos && sizeHint > 0 && (maxBytes <= 0 || int64(sizeHint) <= maxBytes) {
		srcBuf.Grow(sizeHint)
	}
	var tee *srcTee
	if trackPos || (opts.AllowDOCTYPE && !opts.entitiesExpanded) {
		tee = &srcTee{r: r, buf: &srcBuf}
		r = tee
	}

	// The charge reader sits between the decoder and the source so that an
	// entity reference is charged against the expansion budget BEFORE the
	// decoder substitutes it. Checking afterwards reports the same verdict at
	// a cost that makes reporting it pointless: encoding/xml coalesces every
	// substitution in a run of character data into one token, so a document
	// whose references expand to gigabytes has allocated them all before any
	// post-parse check can look. It is installed unconditionally when a
	// DOCTYPE is permitted, and stays inert — one comparison against a nil
	// table per read — until the DOCTYPE actually declares entities.
	var charger *entityChargeReader
	if opts.AllowDOCTYPE && !opts.entitiesExpanded {
		charger = &entityChargeReader{r: r}
		r = charger
	}

	dec := xml.NewDecoderSize(r, window)
	dec.CharsetReader = charsetReader
	// Leave Strict on: a validator must not silently accept malformed input.
	dec.Strict = true
	// Entity is left nil so that only the five entities XML predefines —
	// &amp; &lt; &gt; &quot; &apos; — are recognised; encoding/xml handles
	// those itself. Setting it to xml.HTMLEntity, as this once did, defines
	// 252 HTML entities instead, so "&nbsp;" and "&copy;" expanded in a
	// document that declares no DTD at all. A conforming XML parser must
	// reject an undeclared entity, and silently inventing 252 of them is a
	// difference between what this validator accepts and what the document's
	// next consumer will.
	//
	// CharsetReader accepts only the encodings that need no converter at
	// all. US-ASCII is a strict subset of UTF-8, so its bytes are already
	// valid UTF-8 and the reader is returned unchanged after checking that
	// they really are seven-bit. ISO-8859-1 maps each byte to the code point
	// of the same value by definition, which is one conversion this package
	// can perform exactly and without a table.
	//
	// Everything else stays an error. Routing an arbitrary encoding through
	// a converter this package does not control would make what the
	// validator accepts depend on a decoder the caller cannot see.
	// (see charsetReader below)

	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	maxNodes := opts.MaxNodes
	if maxNodes == 0 {
		maxNodes = DefaultMaxNodes
	}
	nodes := 0

	// The records are appended in document order as the tokens arrive, so
	// a parse builds the tree in its final layout and never walks it again.
	tree := NewTree()
	var spaces spaceTable
	var run textRun
	tree.Root.SetBaseURI(opts.BaseURI)
	tree.Root.SetDocumentURI(opts.DocumentURI)
	cur := tree.Root
	depth := 0
	sawRoot := false
	sawDecl := false
	standalone := false // the XML declaration said standalone="yes"
	sawPrologToken := false
	sawDoctype := false
	// Attribute defaults declared by an ATTLIST in the internal subset. Kept
	// as a slice because a document rarely declares more than a handful, and
	// the common case is none at all.
	var attDefaults []attDefault
	var attTypes []attDeclaredType
	// Element names whose DTD content model is element-only. Whitespace-only
	// text in such an element is ignorable (XML §2.10) and is stripped
	// regardless of what the stylesheet declares (XSLT 2.0 §4.4).
	var elementOnly map[string]bool
	// stripSpaceAt reports whether whitespace-only text directly inside el
	// is dropped: ignorable whitespace in DTD element-only content first and
	// unconditionally, since the DTD-derived rule outranks the
	// stylesheet-declared one, then the caller's StripSpace rule. A text run
	// is complete when it is flushed, so the decision is made there and the
	// node never joins the tree.
	stripSpaceAt := func(el *Node) bool {
		return (elementOnly != nil && ignorableWhitespaceIn(el, elementOnly)) ||
			(opts.StripSpace != nil && stripsWhitespaceIn(el, opts.StripSpace))
	}

	for {
		// InputOffset after Token() is the position *after* the token, so the
		// start of the element must be taken before it is read.
		start := dec.InputOffset()
		// RawToken, not Token: Token resolves prefixes into Name.Space and
		// throws the prefix away, which is unrecoverable (see buildElement).
		// RawToken reports the name exactly as written.
		//
		// The one well-formedness check Token performs and RawToken does not
		// is matching each end tag against its start tag, and that is done at
		// xml.EndElement below. A duplicated attribute is refused by
		// validateStartElement (wellformed.go), keyed on expanded names with
		// namespace declarations kept out of the key.
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse XML: %w", err)
		}
		// Any other token ends a run of character data, and its node must
		// hold its value before anything -- whitespace stripping at an end
		// tag, a new sibling -- can look at it.
		if _, text := tok.(*xml.CharData); !text {
			run.flush(&spaces, stripSpaceAt)
		}

		// Each token points into the decoder and is copied out here; it is
		// valid only until the next RawToken.
		switch tok := tok.(type) {
		case *xml.StartElement:
			t := *tok
			sawPrologToken = true
			depth++
			if depth > maxDepth {
				return nil, fmt.Errorf("parse XML: nesting exceeds %d levels: %w",
					maxDepth, ErrResourceLimit)
			}
			if cur == tree.Root {
				if sawRoot {
					return nil, fmt.Errorf("parse XML: multiple root elements")
				}
				sawRoot = true
				// Past the prolog: no re-parse can start now, so the copy
				// kept for one is dropped; and with no entity table installed
				// by now none ever will be, so the charge reader has nothing
				// to buffer for.
				if tee != nil && !trackPos {
					tee.buf = nil
					srcBuf = strings.Builder{}
				}
				if charger != nil && charger.t == nil {
					charger.off = true
					charger.backlog = nil
				}
			}
			if len(attDefaults) > 0 {
				t = applyAttDefaults(t, attDefaults)
			}
			if len(attTypes) > 0 {
				t = normalizeAttTokens(t, attTypes)
			}
			if err := validateStartElement(t, cur, dec.IsVersion11()); err != nil {
				return nil, err
			}
			el, decls := buildElement(t, cur)
			if off := encodeOffset(start, trackPos); off != 0 {
				el.setOffset(off)
			}
			// A node written inside an external parsed entity takes its base
			// URI from that entity, not from its parent in the tree — XML
			// Base section 4.2 and the XDM base-uri accessor. The entity's
			// text has already been spliced into this source, so the only
			// record of where it came from is the byte offset.
			//
			// This runs after buildElement so that an xml:base ON the element
			// still resolves against the entity's URI, which is the base in
			// force where the attribute was written.
			if b := baseAt(opts.entityBases, int(start)); b != "" {
				if xb := el.Attr(NSXML, "base"); xb != nil {
					el.SetBaseURI(resolveBase(b, xb.Value()))
				} else {
					el.SetBaseURI(b)
				}
			}
			if len(attTypes) > 0 {
				applyAttTypes(el, attTypes)
			}
			// Attributes and namespaces are nodes too, and a document made
			// of elements carrying many attributes allocates most of its
			// memory in them, so they count against the limit.
			nodes += 1 + el.NumAttrs() + decls
			if maxNodes > 0 && nodes > maxNodes {
				return nil, fmt.Errorf(
					"parse XML: document exceeds %d nodes: %w",
					maxNodes, ErrResourceLimit)
			}
			cur = el

		case *xml.EndElement:
			t := *tok
			if cur == tree.Root {
				return nil, fmt.Errorf("parse XML: unbalanced end element %q", t.Name.Local)
			}
			// RawToken does not pair tags, so the pairing is checked here on
			// the lexical name — which is what XML §3 requires anyway: the
			// end tag must repeat the start tag's QName character for
			// character, not merely resolve to the same expanded name.
			if cn := cur.Name(); t.Name.Space == cn.Prefix && t.Name.Local == cn.Local {
				// The same QName, written the same way: nothing to build.
			} else if got, want := lexicalName(t.Name), cn.Lexical(); got != want {
				return nil, fmt.Errorf(
					"parse XML: element %q closed by end element %q", want, got)
			}
			// Ignorable whitespace goes first and unconditionally: the
			// DTD-derived rule outranks the stylesheet-declared one, so it
			// must not be gated on a strip-space declaration existing.
			tree.closeLast()
			cur = cur.Parent()
			depth--

		case *xml.CharData:
			t := *tok
			// CharData is only meaningful inside an element; whitespace at the
			// document level is legal and carries no information. It must be
			// written as such: [27] Misc admits no reference or CDATA section.
			if cur == tree.Root {
				if !dec.Literal() || !isXMLWhitespace(string(t), dec.IsVersion11()) {
					return nil, fmt.Errorf("parse XML: character data outside root element")
				}
				sawPrologToken = true
				continue
			}
			run.add(cur, t)

		case *xml.Comment:
			t := *tok
			sawPrologToken = true
			c := cur.appendChild(KindComment)
			c.v0, c.v1 = tree.text.addBytes(t, 0)

		case *xml.ProcInst:
			t := *tok
			if strings.EqualFold(t.Target, "xml") {
				if t.Target != "xml" {
					return nil, fmt.Errorf("parse XML: processing-instruction target %q is reserved", t.Target)
				}
				if sawDecl || sawPrologToken || sawRoot || sawDoctype {
					return nil, fmt.Errorf("parse XML: XML declaration must appear at the start of the document")
				}
				if err := validateXMLDecl(string(t.Inst)); err != nil {
					return nil, err
				}
				sawDecl = true
				standalone = standaloneYes.MatchString(string(t.Inst))
				continue // the XML declaration is not a PI node in the XDM
			}
			// Namespaces in XML §7: no PI target contains a colon.
			if strings.Contains(t.Target, ":") {
				return nil, fmt.Errorf("parse XML: processing-instruction target %q contains a colon", t.Target)
			}
			sawPrologToken = true
			pi := cur.appendChild(KindPI)
			pi.name = tree.intern(QName{Local: t.Target})
			pi.v0, pi.v1 = tree.text.addBytes(t.Inst, 0)
			// Same entity rule as for elements: a PI pulled in from an
			// external entity has that entity's URI as its base. This is
			// exactly what resolve-uri-021 asserts.
			if b := baseAt(opts.entityBases, int(start)); b != "" {
				pi.SetBaseURI(b)
			}

		case *xml.Directive:
			t := *tok
			d := strings.TrimSpace(string(t))
			if !isDOCTYPEDirective(d) {
				return nil, fmt.Errorf("parse XML: markup declaration %q is not a DOCTYPE", d)
			}
			if sawDoctype || sawRoot || cur != tree.Root {
				return nil, fmt.Errorf("parse XML: DOCTYPE declaration must appear once before the document element")
			}
			if !opts.AllowDOCTYPE {
				return nil, fmt.Errorf("parse XML: DOCTYPE declaration rejected " +
					"(set AllowDOCTYPE to permit its internal declarations; " +
					"reading external entities additionally requires " +
					"ExternalEntities)")
			}
			// An ATTLIST may give an attribute a #FIXED or literal default,
			// which a processor is required to add to every matching element
			// — including a namespace declaration, since "xmlns:p CDATA
			// #FIXED '...'" is how a DTD supplies a binding. Without this the
			// prefix is simply absent from the tree.
			//
			// Only defaults are read. A default's references to internal
			// entities are expanded once the entities are known (see
			// normalizeAttDefaults); nothing resolves an external identifier
			// or reads a file, so this does not widen what AllowDOCTYPE
			// admits.
			if isDOCTYPEDirective(d) {
				sawDoctype = true
				sawPrologToken = true
				// Retained so a caller can validate against the document's
				// own DTD; see Tree.DocType.
				tree.DocType = d
				// Without a resolver no parameter entity is read, so §5.1
				// stops entity and ATTLIST processing at the first
				// reference to one, unless the document is standalone.
				declText := d
				if opts.ExternalEntities == nil && !standalone {
					declText = declsBeforeUnreadPE(d)
				}
				defs, types := parseAttList(declText)
				attDefaults = append(attDefaults, defs...)
				attTypes = append(attTypes, types...)
				elementOnly = parseElementOnlyDecls(d)
				// Internal general entities are declared here and referenced
				// in content, so the table has to be installed before the
				// decoder reads any. encoding/xml consults dec.Entity lazily,
				// which makes that possible: the DOCTYPE is always the first
				// token.
				//
				// Only internal entities are expanded. One declared SYSTEM or
				// PUBLIC is recorded as refused, so referencing it is an
				// error rather than a fetch — this does not open XXE.
				subset := d
				var ents *entityTable
				// Everything that reads outside the document happens only
				// when a resolver was supplied. With none, this whole block
				// is skipped and the subset is read exactly as before.
				if opts.ExternalEntities != nil && !opts.entitiesExpanded {
					ents = newEntityTable(opts.BaseURI, opts.entityBudget)
					// The DOCTYPE follows the XML declaration, so by now the
					// decoder has read the version and §4.3.4 can be enforced
					// against it. See entityTable.checkEntityVersion.
					ents.version11 = dec.IsVersion11()
					ents.resolver = opts.ExternalEntities
					// A parameter entity in the INTERNAL subset is expanded
					// first, since it is how a document pulls a module of
					// declarations in: "<!ENTITY % ext SYSTEM 'e.ent'>%ext;"
					// declares nothing by itself, and the declarations only
					// exist once that reference is substituted.
					expandedSubset, err := ents.expandParameterEntities(d, opts.BaseURI, 0)
					if err != nil {
						return nil, fmt.Errorf("parse XML: %w", err)
					}
					subset = expandedSubset
					ents.parseDecls(subset, opts.BaseURI)
					ents.subsetText = subset
					if sys, pub, ok := externalSubsetOf(d); ok {
						if err := ents.loadExternalSubset(sys, pub, opts.BaseURI); err != nil {
							return nil, fmt.Errorf("parse XML: %w", err)
						}
					}
					if err := checkDTDComments(ents.subsetText); err != nil {
						return nil, fmt.Errorf("parse XML: external DTD: %w", err)
					}
					if err := checkEntityCharRefs(ents.subsetText, dec.IsVersion11()); err != nil {
						return nil, fmt.Errorf("parse XML: %w", err)
					}
					// Retained so fn:unparsed-entity-uri can see declarations
					// that live outside the directive. The subset a document
					// is governed by is not always the text it was written
					// with.
					tree.ownSource().externalSubset = ents.subsetText
					// Declarations pulled in from the external subset are read
					// before ents may be discarded below: loading one that
					// declared no entities still nils ents out, and the
					// <!ELEMENT> models it brought in would be lost with it.
					if ents.subsetText != "" {
						elementOnly = parseElementOnlyDecls(ents.subsetText)
					}
					if len(ents.raw) == 0 && len(ents.external) == 0 {
						ents = nil
					}
					// Substituting a parameter entity can bring in attribute
					// defaults and declared types that were not in the
					// directive as written, so those are re-read from the
					// expanded text rather than the original.
					if subset != d || ents != nil {
						text := subset
						if ents != nil {
							text = ents.subsetText
						}
						defs, types := parseAttList(text)
						attDefaults = defs
						attTypes = types
						elementOnly = parseElementOnlyDecls(text)
					}
				} else {
					ents = parseEntityDecls(declText, opts.BaseURI, opts.entityBudget)
				}
				if ents != nil {
					ents.version11 = dec.IsVersion11()
				}
				if err := checkEntityCharRefs(d, dec.IsVersion11()); err != nil {
					return nil, fmt.Errorf("parse XML: %w", err)
				}
				wfc := entityDeclaredIsWFC(d, standalone)
				if attDefaults, err = normalizeAttDefaults(attDefaults, ents, dec.IsVersion11(), wfc); err != nil {
					return nil, fmt.Errorf("parse XML: %w", err)
				}
				if !wfc {
					declared := ents
					dec.Undeclared = func(name string) bool { return !declared.declares(name) }
				}
				if ents != nil && !opts.entitiesExpanded {
					ents.version11 = dec.IsVersion11()
					ents.resolver = opts.ExternalEntities
					// An entity whose replacement text holds markup cannot go
					// through dec.Entity at all: encoding/xml substitutes that
					// map's values as character data and never re-scans them,
					// so <!ENTITY e "<b/>"> would reach the tree as the four
					// characters "<b/>". XML says the replacement text is
					// parsed, which is what makes an entity a way to factor
					// out a fragment rather than only a phrase.
					//
					// So such a document is rewritten and parsed again. The
					// check is cheap and almost always false, and the restart
					// happens at the DOCTYPE — before any content has been
					// built — so nothing is thrown away but the directive.
					if ents.hasMarkup() {
						// The decoder buffers ahead, so srcBuf holds an
						// unpredictable prefix and the reader holds the
						// remainder — but the decoder's own buffer holds the
						// piece between them. Recovering that is fragile, so
						// the source is re-read from the start instead: the
						// caller's reader is spent, and the tee has whatever
						// it consumed, which together with the rest of the
						// reader is the whole document only if nothing was
						// buffered. Reading the tee to completion first makes
						// it so.
						if _, err := io.Copy(io.Discard, r); err != nil {
							return nil, fmt.Errorf("parse XML: %w", err)
						}
						return parseExpanded(srcBuf.String(), ents, opts)
					}
					// Arm the charge reader now that the declarations are
					// known. The decoder has already buffered ahead — possibly
					// the entire document, if the declaration was large enough
					// to fill its read-ahead window along with the body — so
					// arming charges that backlog here rather than waiting for
					// a further Read that may never come. The remainder, if
					// any, streams through the reader. Either way every
					// reference in the document body is charged before the
					// decoder expands it.
					if charger != nil {
						if err := charger.arm(ents); err != nil {
							return nil, fmt.Errorf("parse XML: %w", err)
						}
					}
					if dec.Entity == nil {
						dec.Entity = map[string]string{}
					}
					m := ents.entityMap()
					for k, v := range m {
						dec.Entity[k] = v
					}
					dec.AttrEntity = ents.attrEntityMap(m)
				}
			}
		}
	}

	if !sawRoot {
		return nil, fmt.Errorf("parse XML: no root element")
	}
	if cur != tree.Root {
		return nil, fmt.Errorf("parse XML: unexpected EOF, %q left open", cur.Name().Local)
	}
	tree.closeAll()
	tree.frozen = true

	if trackPos {
		// The decoder stops reading at the end of the root element, so the
		// tee holds everything up to there — which is all any offset can
		// point into.
		tree.ownSource().src = srcBuf.String()
	}
	tree.XMLVersion = "1.0"
	if dec.IsVersion11() {
		tree.XMLVersion = "1.1"
	}
	return tree, nil
}

// readWindow is the read buffer size for an input of size bytes (0:
// unknown): the input's length, within [512, 4096]. 512 leaves room for
// decodeReader's look at the XML declaration.
func readWindow(size int) int {
	if size <= 0 {
		return 4096
	}
	return min(max(size+1, 512), 4096)
}

// ParseString is Parse over a string, which is what most tests and the
// stylesheet compiler want.
func ParseString(s string, opts ParseOptions) (*Tree, error) {
	return Parse(strings.NewReader(s), opts)
}

// buildElement appends to parent the element a StartElement opens, its
// namespace declarations and its attributes, and returns it with the number
// of declarations.
//
// The token comes from Decoder.RawToken, so Name.Space holds the PREFIX the
// author wrote rather than a resolved URI, and resolution is done here against
// the declarations in scope. That is the whole reason RawToken is used: the
// namespace-aware Decoder.Token discards the prefix and reports only the URI,
// which cannot be inverted — a document binding one URI to two prefixes has no
// way to say which one an element was written with, and guessing renamed
// a:foo to a2:foo and unprefixed <out> to <my:out> on serialisation.
//
// xmlns declarations arrive as ordinary attributes with Space "xmlns" (or
// Local "xmlns" for the default). Those become the element's namespace
// declarations rather than attributes: the attribute axis must not return
// them.
func buildElement(t xml.StartElement, parent *Node) (*Node, int) {
	tree := parent.tree
	el := parent.appendChild(KindElement)
	var arr [8]nsBinding
	decls := arr[:0]
	for _, a := range t.Attr {
		switch {
		case a.Name.Space == "xmlns":
			decls = append(decls, nsBinding{a.Name.Local, a.Value})
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			decls = append(decls, nsBinding{"", a.Value})
		case a.Name.Space == NSXMLNS:
			decls = append(decls, nsBinding{a.Name.Local, a.Value})
		}
	}
	if len(decls) > 0 {
		el.setFrame(decls)
	}
	// The element's own declarations are in place, so its name and its
	// attributes' names resolve against them as well as its ancestors'.
	el.name = tree.intern(QName{
		Prefix: t.Name.Space,
		Local:  t.Name.Local,
		URI:    resolvePrefix(el, t.Name.Space, true),
	})
	for _, a := range t.Attr {
		if a.Name.Space == "xmlns" || a.Name.Space == NSXMLNS ||
			(a.Name.Space == "" && a.Name.Local == "xmlns") {
			continue
		}
		q := QName{Prefix: a.Name.Space, Local: a.Name.Local}
		if q.Prefix != "" {
			q.URI = resolvePrefix(el, q.Prefix, false)
		}
		v := a.Value
		if a.Name.Space == "xml" {
			switch a.Name.Local {
			case "base":
				el.SetBaseURI(resolveBase(el.BaseURI(), a.Value))
			case "id":
				// xml:id is an ID by definition, and the xml:id
				// Recommendation requires its value to be normalised as one,
				// whether or not a DTD declares it.
				v = strings.Join(SplitXMLSpace(a.Value), " ")
			}
		}
		attr := el.appendAttrRaw()
		attr.name = tree.intern(q)
		if v != "" {
			attr.v0, attr.v1 = tree.text.add(v)
		}
	}
	return el, len(decls)
}

// appendAttrRaw appends an empty attribute record to el, which the parser has
// just made and given no children.
func (el *Node) appendAttrRaw() *Node {
	t := el.tree
	a := t.alloc()
	a.kind = uint8(KindAttribute)
	a.parent = el.self
	a.v1 = 0
	if k := el.attrCount() + 1; k < 0xFFFF && el.flags&fManyAttrs == 0 {
		el.nattr = uint16(k)
	} else {
		if src := t.ownSource(); src.attrCounts == nil {
			src.attrCounts = map[uint32]uint32{}
		}
		t.source.attrCounts[el.self] = k
		el.flags |= fManyAttrs
	}
	return a
}

// resolvePrefix returns the namespace URI bound to prefix at el, walking up
// the ancestors' declarations. An unprefixed attribute is in no namespace,
// whatever the default namespace is.
func resolvePrefix(el *Node, prefix string, isElement bool) string {
	if prefix == "" && !isElement {
		return ""
	}
	switch prefix {
	case "xml":
		return NSXML
	case "xmlns":
		return NSXMLNS
	}
	if el.tree.frames == nil {
		return "" // nothing in this tree declares a namespace
	}
	for cur := el; cur != nil; cur = cur.Parent() {
		for _, b := range cur.frame() {
			if b.prefix == prefix {
				return b.uri
			}
		}
	}
	return ""
}

// textRun accumulates one run of character data. The text node is appended
// when the run starts, so that it takes its place in document order, and its
// value is stored when the run ends.
type textRun struct {
	node *Node // the text node being built, nil between runs
	buf  []byte
}

func (r *textRun) add(parent *Node, b []byte) {
	if len(b) == 0 {
		return
	}
	if r.node == nil {
		r.node = parent.appendChild(KindText)
		r.buf = r.buf[:0]
	}
	r.buf = append(r.buf, b...)
}

// flush stores the value of the node being built, if any, and ends the run.
// A whitespace-only run inside an element strip selects is taken back out:
// it is the last record appended, so dropping it leaves the tree as if it
// had never been made.
func (r *textRun) flush(spaces *spaceTable, strip func(*Node) bool) {
	if r.node == nil {
		return
	}
	n := r.node
	r.node = nil
	t := n.tree
	if onlySpace(r.buf) && strip(n.Parent()) {
		t.rec(n.parent).v1 = n.prev
		t.n--
		return
	}
	n.v0, n.v1 = spaces.text(&t.text, r.buf)
}

// spaceTable shares one stored value among the whitespace-only text runs of a
// parse. An indented document repeats a handful of them -- a newline and the
// same indentation, over and over -- and storing each once saves the bytes of
// every repeat.
type spaceTable struct {
	m     map[string][2]uint32
	byLen [maxSpaceLen + 1][2]uint32
}

const (
	maxSpaceTable = 256
	maxSpaceLen   = 128
)

func (t *spaceTable) text(s *textStore, b []byte) (uint32, uint32) {
	if len(b) > maxSpaceLen || !onlySpace(b) {
		return s.addBytes(b, 0)
	}
	if r := t.byLen[len(b)]; r[1] != 0 && s.str(r[0], r[1]) == string(b) {
		return r[0], r[1]
	}
	if r, ok := t.m[string(b)]; ok {
		t.byLen[len(b)] = r
		return r[0], r[1]
	}
	off, n := s.addBytes(b, 0)
	r := [2]uint32{off, n}
	t.byLen[len(b)] = r
	if len(t.m) < maxSpaceTable {
		if t.m == nil {
			t.m = map[string][2]uint32{}
		}
		t.m[string(b)] = r
	}
	return off, n
}

func onlySpace(b []byte) bool {
	for _, c := range b {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return false
		}
	}
	return true
}

// stripsWhitespaceIn reports whether the predicate strips whitespace-only text
// children of el: it selects el's name, and el itself does not say
// xml:space="preserve".
func stripsWhitespaceIn(el *Node, strip func(QName) bool) bool {
	if !strip(el.Name()) {
		return false
	}
	a := el.Attr(NSXML, "space")
	return a == nil || a.Value() != "preserve"
}

func encodeOffset(off int64, track bool) int32 {
	if !track || off < 0 || off >= math.MaxInt32 {
		return 0
	}
	return int32(off + 1)
}

// countingReader fails the read that passes the byte limit, rather than
// truncating silently. A truncated document would either fail to parse with a
// confusing syntax error or, worse, parse as a smaller well-formed document
// than the one that was sent.
type countingReader struct {
	r   io.Reader
	n   int64
	max int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > c.max {
		return n, fmt.Errorf("document exceeds %d bytes: %w", c.max, ErrResourceLimit)
	}
	return n, err
}

// parseExpanded re-parses a document whose entities hold markup.
//
// It exists because encoding/xml cannot expand such an entity: dec.Entity maps
// a name to a string and the decoder substitutes that string as character
// data, without re-scanning it. An entity declared as "<b/>" therefore reaches
// the tree as four characters rather than as an element, which is not what XML
// says an entity is.
//
// The substitution is done on the source and the document parsed again. The
// second parse declares no entities — they are already substituted — so it
// cannot recurse back into here, and a document that references an entity the
// subset does not declare still fails in the decoder, where the error names
// the reference.
func parseExpanded(src string, ents *entityTable, opts ParseOptions) (*Tree, error) {
	expanded, err := ents.substituteMarkupEntities(src)
	if err != nil {
		return nil, fmt.Errorf("parse XML: %w", err)
	}
	// The expansion bounds have already been applied by the substitution, so
	// the re-parse is of text of a size this package has agreed to.
	sub := opts
	sub.entitiesExpanded = true
	sub.entityBases = ents.baseSpans
	return Parse(strings.NewReader(expanded), sub)
}

// resolveBase resolves an xml:base value against the base already in force.
//
// An absolute reference replaces the base outright; a relative one is merged
// with it by the ordinary RFC 3986 rules. Parsing is not the place to raise a
// URI error — fn:base-uri and fn:resolve-uri report one themselves when the
// value is actually used — so a base this cannot parse is not an error here.
// What it must not do is DISCARD one.
//
// Returning the bare reference when the base was unusable was the failure
// mode, and it is worse than it looks. A Windows base URI that had been
// concatenated rather than built — file://C:\dir\doc.xml — does not survive
// url.Parse at all (a backslash after the host reads as a port), so
// resolveBase(that, "deeper/") returned "deeper/": the element's base URI
// became a bare relative reference pointing at the process's working
// directory rather than at the document's. Nothing reported it. And because
// the result is non-empty, xpath's inheritedBaseURI stops walking there and
// never reaches an ancestor whose base IS usable, so one bad link poisons the
// whole subtree beneath it. A base URI decides where a relative reference
// resolves TO, so silently relocating it is the wrong failure.
//
// mergeRelative is what replaces the discard: when the base does not parse as
// an absolute URI it is still a string with path structure, and RFC 3986
// section 5.2.3's merge is defined on that structure alone. "sub/" and
// "deeper/" merge to "sub/deeper/" whether or not "sub/" has a scheme. The
// caller gets a base that still names the right place relative to whatever
// the unusable base named, instead of one that has forgotten it.
func resolveBase(base, ref string) string {
	if ref == "" {
		return base
	}
	if base == "" {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		// The reference itself is not a URI reference. There is nothing to
		// resolve against the base, and the raw value is what fn:base-uri
		// must report so that it can raise the error the caller will see.
		return ref
	}
	if r.IsAbs() {
		return ref
	}
	if b, err := url.Parse(base); err == nil && b.IsAbs() {
		return b.ResolveReference(r).String()
	}
	return mergeRelative(base, ref)
}

// mergeRelative applies RFC 3986 section 5.2.3's merge and 5.2.4's
// remove_dot_segments to two references neither of which need be absolute.
//
// It exists for the case url.ResolveReference refuses: a base with no usable
// scheme. That is not a case to be optimistic about — it means something
// upstream produced a base URI it should not have — but the containment
// question is "where does a relative reference under this base point", and
// dropping the base answers it with the working directory, which is both
// wrong and unbounded. Merging answers it with a location still underneath
// whatever the base named.
//
// A ref that begins with "/" is path-absolute and replaces the base's path
// outright, per 5.2.2; anything else is appended to the base's directory.
//
// "Directory" is taken at the last separator of EITHER kind. A base that did
// not parse is very often one written with backslashes — that is the whole
// reason this path exists — and file://C:\dir\doc.xml has no forward slash
// after the scheme's own, so cutting at "/" alone would take the directory to
// be "file:/" and produce file:/deeper/, which has lost the drive and the
// directories both. That is the original discard wearing a scheme.
func mergeRelative(base, ref string) string {
	if strings.HasPrefix(ref, "/") {
		return removeDotSegments(ref)
	}
	dir := ""
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		dir = base[:i+1]
	}
	// 5.2.4 runs over the join, because a ".." in the reference has to be
	// able to climb out of the base's directory — "a/b/c.xml" with "../d/"
	// is "a/d/". What is withheld from it is the scheme-and-authority
	// prefix, which a ".." may not climb past: 5.2.4's output path is never
	// allowed to begin with one, and letting it run would eat the drive
	// letter or the host. That prefix is left byte-for-byte, so a base this
	// function could not parse is not reinterpreted on the way past.
	keep, climb := splitClimbable(dir)
	return keep + removeDotSegments(climb+ref)
}

// splitClimbable divides a base's directory into the prefix a "../" may not
// climb past and the segments it may.
//
// It recognises the scheme and authority textually, because this function is
// reached precisely when url.Parse would not do it for us.
func splitClimbable(dir string) (keep, climb string) {
	if i := strings.Index(dir, "://"); i >= 0 {
		// Past the authority: the next separator of either kind starts the
		// path, and everything before it is untouchable.
		if j := strings.IndexAny(dir[i+3:], `/\`); j >= 0 {
			return dir[:i+3+j+1], dir[i+3+j+1:]
		}
		return dir, ""
	}
	if i := strings.Index(dir, ":"); i >= 0 {
		// A scheme with no authority — "file:/path", or the Windows base
		// "file://C:\dir\" once the two-slash form has been consumed above.
		if j := strings.IndexAny(dir[i+1:], `/\`); j >= 0 {
			return dir[:i+1+j+1], dir[i+1+j+1:]
		}
		return dir, ""
	}
	if strings.HasPrefix(dir, "/") {
		return "/", dir[1:]
	}
	return "", dir
}

// removeDotSegments is RFC 3986 section 5.2.4, which url.URL applies for
// itself but does not export. Without it a merged "a/b/../c" keeps the "..".
func removeDotSegments(p string) string {
	// The algorithm is defined on the path only; a merged relative reference
	// may still carry a query or fragment, which take no part in it.
	tail := ""
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p, tail = p[:i], p[i:]
	}
	var out []string
	rooted := strings.HasPrefix(p, "/")
	trailing := strings.HasSuffix(p, "/")
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
			// An empty segment is the one either side of a separator that
			// Split produces; the trailing slash is restored below.
		case "..":
			if n := len(out); n > 0 {
				out = out[:n-1]
			}
		default:
			out = append(out, seg)
		}
	}
	res := strings.Join(out, "/")
	if rooted {
		res = "/" + res
	}
	if trailing && res != "" && !strings.HasSuffix(res, "/") {
		res += "/"
	}
	return res + tail
}

// lexicalName reassembles the QName as written, given a RawToken name whose
// Space field holds the prefix.
func lexicalName(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

// charsetReader decodes the encodings this package can handle exactly.
//
// It is deliberately not a general converter. US-ASCII bytes are already
// valid UTF-8, so the reader is handed back once it is known to be
// seven-bit; ISO-8859-1 maps byte to code point by definition. Any other
// encoding is refused, because accepting it would mean this validator's
// answer depended on a decoder the caller never chose.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "us-ascii", "ascii", "iso-646", "us_ascii":
		// Checked as it streams, rather than by reading the rest of the
		// document into a second copy first.
		return &asciiReader{r: input, charset: charset}, nil
	case "iso-8859-1", "latin1", "iso8859-1", "iso_8859-1":
		b, err := io.ReadAll(input)
		if err != nil {
			return nil, err
		}
		var out bytes.Buffer
		out.Grow(len(b))
		for _, c := range b {
			out.WriteRune(rune(c))
		}
		return bytes.NewReader(out.Bytes()), nil
	}
	return nil, fmt.Errorf("unsupported encoding %q", charset)
}

// srcTee copies what is read from r into buf until buf is set to nil.
type srcTee struct {
	r   io.Reader
	buf *strings.Builder
}

func (t *srcTee) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if t.buf != nil {
		t.buf.Write(p[:n])
	}
	return n, err
}

// asciiReader passes seven-bit bytes through and fails at the first byte
// above 0x7f, naming its offset in the stream it was handed.
//
// It used to read the whole stream before returning anything, and its errors
// were then wrapped by the tokeniser as a failure to open the charset. A
// reader error is not wrapped, so the wording is supplied here, keeping the
// messages what they were. What streaming does change is precedence: in a
// document that is also malformed before its first non-ASCII byte, that
// error is now the one reported.
type asciiReader struct {
	r       io.Reader
	charset string
	off     int
}

func (a *asciiReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	for i, c := range p[:n] {
		if c > 0x7f {
			return i, fmt.Errorf(
				"xml: opening charset %q: declared encoding %s but byte %d at offset %d is not ASCII",
				a.charset, a.charset, c, a.off+i)
		}
	}
	a.off += n
	if err != nil && err != io.EOF {
		err = fmt.Errorf("xml: opening charset %q: %w", a.charset, err)
	}
	return n, err
}
