package xdm

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math"
	"net/url"
	"strings"
	"sync"
	"unsafe"

	"github.com/knroy/go-xml/v2/internal/xdmlimit"
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

	// Warn, when non-nil, receives the errors a parse reports without
	// failing. Nil, the default, reports nothing and changes nothing.
	//
	// Today these are the xml:id errors (xml:id §4, §6): a value that is
	// not an NCName once normalised, and a value another ID in the
	// document already holds, an xml:id or a DTD-declared ID attribute
	// (two DTD IDs alike are a validity error and are not reported). The
	// Recommendation says such errors "are not
	// fatal, but should be reported", so the parse goes on and the
	// attribute is still an ID. Each is an *Error with Code "XQDY0091",
	// the code XQuery gives an xml:id error, and a message naming the
	// value (at most 64 characters of it) and nothing of the host.
	//
	// At most 100 are delivered per parse, then one saying the rest were
	// suppressed, so a document cannot flood the callback.
	Warn func(error)

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

	// firstParse is what the first parse of a rewritten source read from its
	// DTD, handed to the second (see parseExpandedSteps) so that both decide
	// from the same declarations without reading the external subset again.
	// Unexported like entitiesExpanded, which it accompanies.
	firstParse *firstParseDecls

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

	// bursts, when set, makes this parse ParseBursts: see bursts.go.
	bursts *burster
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
	return parse(r, "", opts)
}

// parse is Parse. whole, when not "", is the text r reads (ParseString's
// string): if decoding leaves it unchanged it is the source kept for
// positions and for an entity re-parse, and no copy is made.
func parse(r io.Reader, whole string, opts ParseOptions) (_ *Tree, err error) {
	defer xdmlimit.Recover(&err)
	step, done := parseSteps(r, whole, opts)
	defer done()
	return step()
}

// parseSteps is parse in steps, for a stream client (bursts) that pauses
// the parse at an event (errPause): step parses until the client pauses,
// returning errPause, or the parse ends; the next call goes on from there,
// once the burster has been resumed (burster.resume). A client that never
// pauses has the whole parse done by one call. done releases the parse's
// read windows, once, when it is over.
func parseSteps(r io.Reader, whole string, opts ParseOptions) (step func() (*Tree, error), done func()) {
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
	// tokeniser's buffers be sized to it.
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

	// UTF-16 is decoded to UTF-8 first. XML 1.0 §4.3.3 makes both encodings
	// mandatory, and encoding/xml reads only UTF-8 — so without this a
	// UTF-16 document fails with "invalid UTF-8" rather than being read.
	// This happens before the tee, so that position tracking counts lines
	// in the text the decoder actually sees.
	// The three read windows are reused parse to parse (see readBufs).
	bufs := readBufPool.Get().(*readBufs)
	// The parse's state outlives a step: it is the read windows', so as not
	// to be allocated every parse.
	ps := &bufs.ps
	ps.opts = opts
	done = func() {
		if ps.innerDone != nil {
			ps.innerDone()
		}
		bufs.release()
	}
	bufs.br.Reset(r)
	decoded, err := decodeReader(bufs.br)
	if err != nil {
		return func() (*Tree, error) { return nil, fmt.Errorf("parse XML: %w", err) }, done
	}
	r = decoded

	// XML 1.0 section 2.11: line ends are normalized on input, before
	// parsing. See xdm/lineend.go. Attribute-value normalization (section
	// 3.3.3) is the tokeniser's, which still sees "&#10;" apart from a
	// newline the author typed.
	r = newLineEndReader(r, bufs.lineEnd)

	trackPos := opts.TrackPositions
	// The source is copied when a DOCTYPE is permitted: an entity whose
	// replacement text holds markup forces a re-parse of the substituted
	// source, and by the time that is known the reader is partly consumed and
	// the decoder has buffered ahead into it.
	//
	// The copy is needed only until the document element opens: a re-parse
	// can start only at the DOCTYPE, which must come first, so past that point
	// the tee stops copying and the copy is dropped. entitiesExpanded marks
	// the second parse, which has no entities left to find and so needs no
	// copy at all.
	//
	// Positions need only where the source's lines start, which the tee
	// records as the text goes by.
	if !verbatim(whole) {
		whole = ""
	}
	var tee *srcTee
	if whole == "" && (trackPos || (opts.AllowDOCTYPE && !opts.entitiesExpanded)) {
		tee = &srcTee{r: r}
		if opts.AllowDOCTYPE && !opts.entitiesExpanded {
			tee.buf = &ps.srcBuf
		}
		if trackPos {
			tee.lines = &lineIndex{}
		}
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

	ps.dec = *xml.NewDecoderBuffer(r, bufs.dec)
	dec := &ps.dec
	dec.Size = sizeHint
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

	// The records are appended in document order as the tokens arrive, so
	// a parse builds the tree in its final layout and never walks it again.
	tree := NewTree()
	tree.Root.SetBaseURI(opts.BaseURI)
	tree.Root.SetDocumentURI(opts.DocumentURI)
	ps.cur = tree.Root
	names := &ps.names
	bs := opts.bursts
	// stripSpaceAt reports whether whitespace-only text directly inside el
	// is dropped: ignorable whitespace in DTD element-only content first and
	// unconditionally, since the DTD-derived rule outranks the
	// stylesheet-declared one, then the caller's StripSpace rule. A text run
	// is complete when it is flushed, so the decision is made there and the
	// node never joins the tree.
	stripSpaceAt := func(el *Node) bool {
		// XML 1.0 §2.9, VC: Standalone Document Declaration: under
		// standalone="yes", no external markup declaration may declare
		// "element types with element content, if white space occurs
		// directly within any instance of those types". This is where such
		// white space is seen, preserved or not.
		if ps.standalone && declKindOf(el, ps.elemDecls)&(declElementOnly|declOutside) == declElementOnly|declOutside {
			ps.note(tree, el, "element %s has white space in element content an external markup declaration "+
				"declares, and the document says standalone=\"yes\" (XML 1.0 §2.9, VC: Standalone Document Declaration)",
				el.Name().Lexical())
		}
		return (ps.elemDecls != nil && ignorableWhitespaceIn(el, ps.elemDecls)) ||
			(ps.opts.StripSpace != nil && stripsWhitespaceIn(el, ps.opts.StripSpace)) ||
			(bs != nil && bs.strip != nil && bs.strip(el))
	}
	// charge counts n nodes the parse has built against MaxNodes.
	charge := func(n int) error {
		ps.nodes += n
		if maxNodes > 0 && ps.nodes > maxNodes {
			return fmt.Errorf("parse XML: document exceeds %d nodes: %w", maxNodes, ErrResourceLimit)
		}
		return nil
	}

	step = func() (*Tree, error) {
		if ps.inner != nil {
			return ps.inner()
		}
		if ps.ended {
			goto finished
		}
		for {
			var (
				start int64
				tok   xml.Token
				err   error
			)
			if ps.held != nil {
				start, tok, ps.held = ps.heldStart, ps.held, nil
				goto told
			}
			// InputOffset after Token() is the position *after* the token, so the
			// start of the element must be taken before it is read.
			start = dec.InputOffset()
			// RawToken, not Token: Token resolves prefixes into Name.Space and
			// throws the prefix away, which is unrecoverable (see buildElement).
			// RawToken reports the name exactly as written.
			//
			// The one well-formedness check Token performs and RawToken does not
			// is matching each end tag against its start tag, and that is done at
			// xml.EndElement below. A duplicated attribute is refused by
			// validateStartElement (wellformed.go), keyed on expanded names with
			// namespace declarations kept out of the key.
			tok, err = dec.RawToken()
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
				// A text run that is not all white space is a node. One that
				// is costs nothing: it lies between two nodes that are
				// counted, and whether it is stripped here or later (a tree
				// a stylesheet strips, against a stream it strips as it
				// reads) must not change what the limit refuses.
				if ps.run.flush(&ps.spaces, stripSpaceAt) {
					if err := charge(1); err != nil {
						return nil, err
					}
				}
				// trimLeaves has something to do only once a leaf has been
				// completed in cur, or while a batch waits (its own tests,
				// without the call at every start tag).
				if bs != nil && (bs.batch != nil || ps.cur.v1 != noIdx && tree.rec(ps.cur.v1).isLeaf()) {
					// The end tag of cur, written as its start tag was: the
					// EndElement case below then fails at nothing before it
					// tells the burster (xdmburst.EndAhead).
					bs.closing = false
					if e, ok := tok.(*xml.EndElement); ok && ps.cur != tree.Root {
						cn := ps.cur.Name()
						bs.closing = e.Name.Space == cn.Prefix && e.Name.Local == cn.Local
					}
					if err := bs.trimLeaves(ps.cur, ps.depth, &ps.spaces, names); err != nil {
						if err == errPause {
							ps.held, ps.heldStart = tok, start
						}
						return nil, err
					}
				}
			}
		told:

			// Each token points into the decoder and is copied out here; it is
			// valid only until the next RawToken.
			switch tok := tok.(type) {
			case *xml.StartElement:
				t := *tok
				// The attribute an external markup declaration defaulted or
				// had normalized, for §2.9 below. A burst records no notes,
				// so the held path loses nothing.
				var saDefault, saNorm string
				if ps.prologHeld {
					t, ps.prologHeld = ps.heldT, false
					goto prologTold
				}
				ps.sawPrologToken = true
				ps.depth++
				if ps.depth > maxDepth {
					return nil, fmt.Errorf("parse XML: nesting exceeds %d levels: %w",
						maxDepth, ErrResourceLimit)
				}
				if ps.cur == tree.Root {
					if ps.sawRoot {
						return nil, fmt.Errorf("parse XML: multiple root elements")
					}
					ps.sawRoot = true
					// Past the prolog: no re-parse can start now, so the copy
					// kept for one is dropped; and with no entity table installed
					// by now none ever will be, so the charge reader has nothing
					// to buffer for.
					if tee != nil {
						tee.buf = nil
						ps.srcBuf = strings.Builder{}
					}
					if charger != nil && charger.t == nil {
						charger.off = true
						charger.backlog = nil
					}
				}
				if len(ps.attDefaults) > 0 {
					if ps.defIdx == nil {
						ps.defIdx = indexDecls(len(ps.attDefaults), func(i int) string { return ps.attDefaults[i].element })
					}
					t, saDefault = applyAttDefaults(t, ps.attDefaults, ps.defIdx)
				}
				if len(ps.attTypes) > 0 {
					if ps.typeIdx == nil {
						ps.typeIdx = indexDecls(len(ps.attTypes), func(i int) string { return ps.attTypes[i].element })
					}
					t, saNorm = normalizeAttTokens(t, ps.attTypes, ps.typeIdx)
				}
				if err := validateStartElement(t, &ps.ns, dec.IsVersion11()); err != nil {
					return nil, err
				}
				if bs != nil && ps.cur == tree.Root {
					if err := bs.prolog(tree.Root); err != nil {
						if err == errPause {
							ps.held, ps.heldStart, ps.heldT, ps.prologHeld = tok, start, t, true
						}
						return nil, err
					}
				}
			prologTold:
				if bs != nil && ps.cur == tree.Root {
					if ps.spaces.home == 0 {
						// Before the first mark: no rewind releases it.
						tree.text.newBlock(make([]byte, 0, spaceHome))
						ps.spaces.home = int32(len(tree.text.blocks))
						ps.spaces.hi = max(ps.spaces.hi, ps.spaces.home)
					}
					bs.marks.reset(markOf(tree))
					bs.v11 = dec.IsVersion11()
					// Known from here, as an Observer of the parse may read.
					tree.XMLVersion = "1.0"
					if bs.v11 {
						tree.XMLVersion = "1.1"
					}
				}
				el, decls := buildElement(t, ps.cur, &ps.ns, names)
				if off := encodeOffset(start, trackPos); off != 0 {
					el.setOffset(off)
				}
				// XML 1.0 §2.9, VC: Standalone Document Declaration: under
				// standalone="yes" no external markup declaration may give
				// "attributes with default values, if elements to which these
				// attributes apply appear in the document without
				// specifications of values for these attributes", nor
				// "attributes with values subject to normalization, where the
				// attribute appears in the document with a value which will
				// change as a result of normalization". Only such
				// declarations set the two (declOrigins).
				if saDefault != "" {
					ps.note(tree, el, "attribute %s of %s takes its default from an external markup declaration, "+
						"and the document says standalone=\"yes\" (XML 1.0 §2.9, VC: Standalone Document Declaration)",
						saDefault, el.Name().Lexical())
				}
				if saNorm != "" {
					ps.note(tree, el, "attribute %s of %s is normalized by the type an external markup declaration "+
						"gives it, and the document says standalone=\"yes\" (XML 1.0 §2.9, VC: Standalone Document Declaration)",
						saNorm, el.Name().Lexical())
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
				if b := baseAt(ps.opts.entityBases, int(start)); b != "" {
					if xb := el.Attr(NSXML, "base"); xb != nil {
						el.SetBaseURI(resolveBase(b, xb.Value()))
					} else {
						el.SetBaseURI(b)
					}
				}
				if len(ps.attTypes) > 0 {
					applyAttTypes(el, ps.attTypes, ps.typeIdx)
				}
				if ps.opts.Warn != nil {
					ps.checkXMLID(el)
				}
				// Attributes and namespaces are nodes too, and a document made
				// of elements carrying many attributes allocates most of its
				// memory in them, so they count against the limit.
				if err := charge(1 + el.NumAttrs() + decls); err != nil {
					return nil, err
				}
				ps.cur = el
				if bs != nil {
					if err := bs.start(el, ps.depth); err != nil {
						return nil, err
					}
				}

			case *xml.EndElement:
				t := *tok
				if ps.cur == tree.Root {
					return nil, fmt.Errorf("parse XML: unbalanced end element %q", t.Name.Local)
				}
				// RawToken does not pair tags, so the pairing is checked here on
				// the lexical name — which is what XML §3 requires anyway: the
				// end tag must repeat the start tag's QName character for
				// character, not merely resolve to the same expanded name.
				if cn := ps.cur.Name(); t.Name.Space == cn.Prefix && t.Name.Local == cn.Local {
					// The same QName, written the same way: nothing to build.
				} else if got, want := lexicalName(t.Name), cn.Lexical(); got != want {
					return nil, fmt.Errorf(
						"parse XML: element %q closed by end element %q", want, got)
				}
				// Ignorable whitespace goes first and unconditionally: the
				// DTD-derived rule outranks the stylesheet-declared one, so it
				// must not be gated on a strip-space declaration existing.
				tree.closeLast()
				ps.ns.pop(len(ps.cur.frame()) - tree.takeAddedNS(ps.cur))
				closed := ps.cur
				ps.cur = ps.cur.Parent()
				ps.depth--
				if bs != nil {
					if err := bs.end(closed, ps.depth+1, &ps.spaces, names); err != nil {
						return nil, err
					}
				}

			case *xml.CharData:
				t := *tok
				// CharData is only meaningful inside an element; whitespace at the
				// document level is legal and carries no information. It must be
				// written as such: [27] Misc admits no reference or CDATA section.
				if ps.cur == tree.Root {
					if !dec.Literal() || !isXMLWhitespace(string(t), dec.IsVersion11()) {
						return nil, fmt.Errorf("parse XML: character data outside root element")
					}
					ps.sawPrologToken = true
					continue
				}
				if ps.elemDecls != nil && !dec.Literal() {
					ps.checkTextOrigin(tree, t, dec)
				}
				ps.run.add(ps.cur, t, dec)

			case *xml.Comment:
				t := *tok
				ps.sawPrologToken = true
				// Counted before a burst skips it, as a tree counts it.
				if err := charge(1); err != nil {
					return nil, err
				}
				if bs != nil && ps.cur == tree.Root && !bs.leaves {
					continue // a burst's tree holds no prolog (see bursts.go)
				}
				c := ps.cur.appendChild(KindComment)
				c.v0, c.v1 = tree.text.addBytes(t, 0)

			case *xml.ProcInst:
				t := *tok
				if strings.EqualFold(t.Target, "xml") {
					if t.Target != "xml" {
						return nil, fmt.Errorf("parse XML: processing-instruction target %q is reserved", t.Target)
					}
					if ps.sawDecl || ps.sawPrologToken || ps.sawRoot || ps.sawDoctype {
						return nil, fmt.Errorf("parse XML: XML declaration must appear at the start of the document")
					}
					if err := validateXMLDecl(string(t.Inst)); err != nil {
						return nil, err
					}
					ps.sawDecl = true
					ps.standalone = declStandaloneYes(string(t.Inst))
					continue // the XML declaration is not a PI node in the XDM
				}
				// Namespaces in XML §7: no PI target contains a colon.
				if strings.Contains(t.Target, ":") {
					return nil, fmt.Errorf("parse XML: processing-instruction target %q contains a colon", t.Target)
				}
				ps.sawPrologToken = true
				if err := charge(1); err != nil {
					return nil, err
				}
				if bs != nil && ps.cur == tree.Root && !bs.leaves {
					continue
				}
				pi := ps.cur.appendChild(KindPI)
				pi.name = tree.intern(QName{Local: t.Target})
				pi.v0, pi.v1 = tree.text.addBytes(t.Inst, 0)
				// Same entity rule as for elements: a PI pulled in from an
				// external entity has that entity's URI as its base. This is
				// exactly what resolve-uri-021 asserts.
				if b := baseAt(ps.opts.entityBases, int(start)); b != "" {
					pi.SetBaseURI(b)
				}

			case *xml.Directive:
				t := *tok
				d := strings.TrimSpace(string(t))
				if !isDOCTYPEDirective(d) {
					return nil, fmt.Errorf("parse XML: markup declaration %q is not a DOCTYPE", d)
				}
				if ps.sawDoctype || ps.sawRoot || ps.cur != tree.Root {
					return nil, fmt.Errorf("parse XML: DOCTYPE declaration must appear once before the document element")
				}
				if !ps.opts.AllowDOCTYPE {
					return nil, fmt.Errorf("parse XML: DOCTYPE declaration rejected " +
						"(set AllowDOCTYPE to permit its internal declarations; " +
						"reading external entities additionally requires " +
						"ExternalEntities)")
				}
				// §5.1: every processor checks the whole internal subset
				// for well-formedness, whatever it goes on to read of it.
				if err := checkDoctypeGrammar(string(t), false); err != nil {
					return nil, fmt.Errorf("parse XML: %w", err)
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
					ps.sawDoctype = true
					ps.sawPrologToken = true
					// Retained so a caller can validate against the document's
					// own DTD; see Tree.DocType.
					tree.DocType = d
					// Without a resolver no parameter entity is read, so §5.1
					// stops entity and ATTLIST processing at the first
					// reference to one, unless the document is standalone.
					declText := d
					if ps.opts.ExternalEntities == nil && !ps.standalone {
						declText = declsBeforeUnreadPE(d)
					}
					// The second parse of a rewritten source decides from the
					// declarations the first one read, external subset and
					// parameter entities included, rather than from the
					// internal subset alone. They are taken as they are:
					// the defaults are already normalized.
					first := ps.opts.firstParse
					if first != nil {
						ps.attDefaults, ps.attTypes, ps.elemDecls = first.attDefaults, first.attTypes, first.elemDecls
					} else {
						defs, types := parseAttList(declText, nil)
						ps.attDefaults = append(ps.attDefaults, defs...)
						ps.attTypes = append(ps.attTypes, types...)
						ps.elemDecls = parseElementDecls(d, nil)
					}
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
					if ps.opts.ExternalEntities != nil && !ps.opts.entitiesExpanded {
						ents = newEntityTable(ps.opts.BaseURI, ps.opts.entityBudget)
						// The DOCTYPE follows the XML declaration, so by now the
						// decoder has read the version and §4.3.4 can be enforced
						// against it. See entityTable.checkEntityVersion.
						ents.version11 = dec.IsVersion11()
						ents.resolver = ps.opts.ExternalEntities
						// A parameter entity in the INTERNAL subset is expanded
						// first, since it is how a document pulls a module of
						// declarations in: "<!ENTITY % ext SYSTEM 'e.ent'>%ext;"
						// declares nothing by itself, and the declarations only
						// exist once that reference is substituted.
						expandedSubset, err := ents.expandParameterEntities(d, ps.opts.BaseURI, 0)
						if err != nil {
							return nil, fmt.Errorf("parse XML: %w", err)
						}
						// The parameter entities' text is held to the
						// grammar too, by the rules for it (see
						// checkDoctypeGrammar).
						if expandedSubset != d {
							if err := checkDoctypeGrammar(expandedSubset, true); err != nil {
								return nil, fmt.Errorf("parse XML: in a parameter entity: %w", err)
							}
						}
						subset = expandedSubset
						ents.parseDecls(subset, ps.opts.BaseURI)
						if ps.standalone {
							ents.markPEDeclared(subset)
						}
						ents.subsetText = subset
						if sys, pub, ok := externalSubsetOf(d); ok {
							if err := ents.loadExternalSubset(sys, pub, ps.opts.BaseURI); err != nil {
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
							ps.elemDecls = parseElementDecls(ents.subsetText, nil)
						}
						full := ents.subsetText
						// Which declarations are external markup
						// declarations matters only to §2.9's VC, which
						// only standalone="yes" brings into play.
						var origin []bool
						if ps.standalone {
							origin = declOrigins(full, len(subset), ents.peSpans)
						}
						if len(ents.raw) == 0 && len(ents.external) == 0 {
							ents = nil
						}
						// Substituting a parameter entity can bring in attribute
						// defaults and declared types that were not in the
						// directive as written, so those are re-read from the
						// expanded text rather than the original.
						if subset != d || full != subset {
							text := full
							defs, types := parseAttList(text, origin)
							ps.attDefaults = defs
							ps.attTypes = types
							ps.elemDecls = parseElementDecls(text, origin)
						}
					} else if first != nil {
						ents = first.ents
						src := tree.ownSource()
						src.externalSubset = first.externalSubset
						src.dtdNotes = append(src.dtdNotes, first.notes...)
					} else {
						ents = parseEntityDecls(declText, ps.opts.BaseURI, ps.opts.entityBudget)
					}
					if ents != nil {
						ents.version11 = dec.IsVersion11()
					}
					if err := checkEntityCharRefs(d, dec.IsVersion11()); err != nil {
						return nil, fmt.Errorf("parse XML: %w", err)
					}
					wfc := entityDeclaredIsWFC(d, ps.standalone)
					// With every external entity read, an undeclared name is
					// declared nowhere: the document is well formed and
					// invalid (VC: Entity Declared; erratum E13 makes an
					// internal parameter-entity reference enough for that).
					// The reference, which the XDM has no node for, is
					// dropped and the fault recorded for dtd.Validate.
					read := ps.opts.ExternalEntities != nil
					if first == nil {
						var drop func(string)
						if !wfc && read {
							drop = func(name string) {
								ps.note(tree, tree.Root, "entity %s is referenced in an attribute default but declared in "+
									"neither subset, and the reference was dropped (XML 1.0 §4.1, VC: Entity Declared)", name)
							}
						}
						if ps.attDefaults, err = normalizeAttDefaults(ps.attDefaults, ents, dec.IsVersion11(), wfc, drop); err != nil {
							return nil, fmt.Errorf("parse XML: %w", err)
						}
					}
					// Each default lands in every matching element; store its
					// value once so that they all alias it.
					for _, d := range ps.attDefaults {
						tree.text.share(d.value)
					}
					if !wfc {
						// The same rule in content and attribute values. A
						// second parse has the first one's table, so it
						// knows every declaration that was read.
						declared := ents
						dec.Undeclared = func(name string) (string, bool) {
							if declared.declares(name) {
								return "", false
							}
							if read {
								ps.note(tree, ps.cur, "entity %s is referenced but declared in neither subset, "+
									"and the reference was dropped (XML 1.0 §4.1, VC: Entity Declared)", name)
								return "", true
							}
							return undeclaredNote(name, read), false
						}
					}
					if ents != nil && !ps.opts.entitiesExpanded {
						ents.version11 = dec.IsVersion11()
						ents.resolver = ps.opts.ExternalEntities
						ents.standalone = ps.standalone
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
							decls := &firstParseDecls{
								ents: ents, attDefaults: ps.attDefaults, attTypes: ps.attTypes,
								elemDecls: ps.elemDecls, externalSubset: tree.ownSource().externalSubset,
								notes: tree.ownSource().dtdNotes,
							}
							if whole != "" {
								ps.inner, ps.innerDone = parseExpandedSteps(whole, decls, ps.opts)
								return ps.inner()
							}
							ps.inner, ps.innerDone = parseExpandedSteps(ps.srcBuf.String(), decls, ps.opts)
							return ps.inner()
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
						if ps.elemDecls != nil {
							dec.EscapedEntity = ents.escapedSpaceMap(m)
						}
					}
				}
			}
		}

		if !ps.sawRoot {
			return nil, fmt.Errorf("parse XML: no root element")
		}
		if ps.cur != tree.Root {
			return nil, fmt.Errorf("parse XML: unexpected EOF, %q left open", ps.cur.Name().Local)
		}
		if bs != nil {
			// The comments and processing instructions after the document
			// element.
			if err := bs.trimLeaves(ps.cur, 0, &ps.spaces, names); err != nil {
				if err == errPause {
					ps.ended = true
				}
				return nil, err
			}
		}
	finished:
		tree.closeAll()
		tree.frozen = true
		tree.text.trim()
		// The indexes only building uses go; a tree renamed or cloned later
		// rebuilds the ones it needs.
		tree.nameIx, tree.nameHot = nil, nil
		tree.frameOne, tree.frameMany = nil, nil
		tree.open = ordStack{}

		if trackPos {
			// The decoder stops reading at the end of the root element, so the
			// tee has seen everything up to there — which is all any offset can
			// point into.
			lines := &lineIndex{}
			if whole != "" {
				lines.add(unsafe.Slice(unsafe.StringData(whole), len(whole)))
			} else {
				lines = tee.lines
			}
			// append's doubling leaves up to a third of the index unused, kept
			// for the life of the tree.
			if cap(lines.starts) > len(lines.starts) {
				lines.starts = append(make([]int32, 0, len(lines.starts)), lines.starts...)
			}
			tree.ownSource().lines = lines
		}
		tree.XMLVersion = "1.0"
		if dec.IsVersion11() {
			tree.XMLVersion = "1.1"
		}
		return tree, nil
	}
	return step, done
}

// readBufs are a parse's read windows: decodeReader's look at the byte order
// mark, line-end folding's and the tokeniser's. Nothing read through them
// outlives the parse -- the tree holds copies of what the tokens held -- so
// they are pooled rather than allocated per document, which for small
// documents was a sixth of what a parse allocated.
type readBufs struct {
	br           *bufio.Reader
	lineEnd, dec []byte
	ps           parseState
}

var readBufPool = sync.Pool{New: func() any {
	return &readBufs{
		br:      bufio.NewReaderSize(nil, 4096),
		lineEnd: make([]byte, 0, 4096),
		dec:     make([]byte, 0, 4096),
	}
}}

// parseState is what a parse keeps from step to step (parseSteps).
type parseState struct {
	dec   xml.Decoder
	opts  ParseOptions
	nodes int
	// The records are appended in document order as the tokens arrive, so
	// a parse builds the tree in its final layout and never walks it again.
	spaces           spaceTable
	run              textRun
	cur              *Node
	ns               nsScope
	names            nameCache
	depth            int
	sawRoot, sawDecl bool
	standalone       bool // the XML declaration said standalone="yes"
	sawPrologToken   bool
	sawDoctype       bool
	// Attribute defaults declared by an ATTLIST in the internal subset. Kept
	// as a slice because a document rarely declares more than a handful, and
	// the common case is none at all.
	attDefaults []attDefault
	attTypes    []attDeclaredType
	// The declarations by element name, built when the first element needs
	// them, after the DOCTYPE has settled both lists.
	defIdx, typeIdx declIndex
	// What the DTD's <!ELEMENT> declarations say, by element name (see
	// parseElementDecls). Whitespace-only text in element-only content is
	// ignorable (XML §2.10) and is stripped regardless of what the
	// stylesheet declares (XSLT 2.0 §4.4).
	elemDecls map[string]uint8
	srcBuf    strings.Builder
	// inner is the parse of the source rewritten when its entities hold
	// markup (parseExpandedSteps), which goes on in this parse's place.
	inner     func() (*Tree, error)
	innerDone func()
	// Where a pause left the parse (errPause): held is the token read
	// before the leaves ahead of it were all told, read from heldStart; with
	// prologHeld, the document element's start tag, heldT, paused at while
	// the prolog was told; ended, the document read to its end.
	held       xml.Token
	heldStart  int64
	heldT      xml.StartElement
	prologHeld bool
	ended      bool
	// ids and idWarnings are checkXMLID's: the ID values seen, each mapped
	// to whether an xml:id held it, and the warnings delivered. Used only
	// when opts.Warn is set.
	ids        map[string]bool
	idWarnings int
}

// maxIDWarnings bounds the xml:id warnings one parse delivers to
// ParseOptions.Warn; past it one summary is delivered and the rest dropped.
const maxIDWarnings = 100

// checkXMLID reports el's xml:id errors (xml:id §4, §6) to opts.Warn: a
// value that is not an NCName, or one already used in this document. The
// value is already normalised (buildElement), and stays an ID either way.
//
// The values are unique across every attribute of type ID, xml:id or
// DTD-declared alike (xml:id §4, XML 1.0 VC: ID; both from memory), so the
// DTD-typed IDs, already marked by applyAttTypes, share the seen-set. A
// duplicate is reported only when an xml:id is one of the two: two DTD IDs
// alike are XML 1.0's validity error, which a non-validating parse does not
// report, not an xml:id error.
//
// ponytail: the seen-set holds every ID value of the document, also under
// ParseBursts; it costs nothing unless Warn is set.
func (ps *parseState) checkXMLID(el *Node) {
	for a := range el.Attrs() {
		xmlID := a.Name().URI == NSXML && a.Name().Local == "id"
		if !xmlID && !a.IsID() {
			continue
		}
		v := a.Value()
		var err error
		if prev, dup := ps.ids[v]; xmlID && !IsNCName(v) {
			err = Errorf("XQDY0091", "xml:id value %.64q is not an NCName", v)
		} else if dup && (xmlID || prev) {
			err = Errorf("XQDY0091", "duplicate ID value %.64q", v)
		} else {
			if ps.ids == nil {
				ps.ids = map[string]bool{}
			}
			ps.ids[strings.Clone(v)] = prev || xmlID
			continue
		}
		switch ps.idWarnings++; {
		case ps.idWarnings <= maxIDWarnings:
			ps.opts.Warn(err)
		case ps.idWarnings == maxIDWarnings+1:
			ps.opts.Warn(Errorf("XQDY0091", "further xml:id errors suppressed after %d", maxIDWarnings))
		}
	}
}

// checkTextOrigin records the validity errors in character data t, read in
// ps.cur and not written literally, that the tree cannot show once only its
// characters are kept (XML 1.0 §3, VC: Element Valid):
//
//   - in element content, white space written as a CDATA section or a
//     character reference: "a character reference to white space is not
//     white space" there (erratum E15), and libxml2 likewise reports a CDATA
//     section, even an empty one;
//   - in an EMPTY element, a reference to an entity whose replacement text is
//     empty, or an empty CDATA section: EMPTY means "no content (not even
//     entity references, comments, PIs or white space)".
//
// Text that is not white space, in either, is a text node, which dtd.Validate
// already holds to the content model.
func (ps *parseState) checkTextOrigin(tree *Tree, t []byte, dec *xml.Decoder) {
	k := declKindOf(ps.cur, ps.elemDecls)
	switch {
	case k&declElementOnly != 0 && dec.Escaped() && onlySpace(t):
		ps.note(tree, ps.cur, "element %s has element content, where a CDATA section or a character reference "+
			"is not white space (XML 1.0 §3, VC: Element Valid; erratum E15)", ps.cur.Name().Lexical())
	case k&declEmpty != 0 && len(t) == 0:
		ps.note(tree, ps.cur, "element %s is declared EMPTY but has content: an entity reference or a CDATA "+
			"section (XML 1.0 §3, VC: Element Valid)", ps.cur.Name().Lexical())
	}
}

// release returns b to the pool, without the caller's reader.
func (b *readBufs) release() {
	b.br.Reset(nil)
	b.ps = parseState{} // nothing of the parse is kept by the pool
	readBufPool.Put(b)
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
	return parse(strings.NewReader(s), s, opts)
}

// verbatim reports whether decoding passes s through unchanged: no byte order
// mark, no UTF-16 and no carriage return for line-end normalisation to fold.
// It errs towards false, which costs only a copy.
func verbatim(s string) bool {
	if s == "" || s[0] == 0xFE || s[0] == 0xFF || s[0] == 0xEF || s[0] == 0 ||
		len(s) > 1 && s[1] == 0 {
		return false
	}
	return strings.IndexByte(s, '\r') < 0
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
// them. validateStartElement has pushed them onto ns, and every prefix on the
// tag is resolved there.
func buildElement(t xml.StartElement, parent *Node, ns *nsScope, names *nameCache) (*Node, int) {
	tree := parent.tree
	el := parent.appendChild(KindElement)
	decls := ns.ownDecls()
	if len(decls) > 0 {
		el.setFrame(decls)
	}
	// validateStartElement has refused an unbound prefix.
	uri, _ := ns.lookup(t.Name.Space)
	el.name = names.intern(tree, QName{Prefix: t.Name.Space, Local: t.Name.Local, URI: uri})
	old := -1 // how many names the table held, when this tag adds its own without the index
	if n := len(t.Attr) - len(decls); n >= bigTag {
		old = tree.reserveNames(n)
	}
	for _, a := range t.Attr {
		if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
			continue
		}
		// An unprefixed attribute is in no namespace, whatever the default
		// namespace is.
		q := QName{Prefix: a.Name.Space, Local: a.Name.Local}
		if q.Prefix != "" {
			q.URI, _ = ns.lookup(q.Prefix)
		}
		v := a.Value
		if a.Name.Space == "xml" {
			switch a.Name.Local {
			case "base":
				el.SetBaseURI(resolveBase(el.BaseURI(), strings.Clone(a.Value))) // out of the arena, as nsScope.declare
			case "id":
				// xml:id is an ID by definition, and the xml:id
				// Recommendation requires its value to be normalised as one,
				// whether or not a DTD declares it.
				v = strings.Join(SplitXMLSpace(a.Value), " ")
			}
		}
		attr := el.appendAttrRaw()
		if old >= 0 {
			attr.name = tree.internNew(q, old)
		} else {
			attr.name = names.intern(tree, q)
		}
		if v != "" {
			attr.v0, attr.v1 = tree.text.add(v)
		}
	}
	if old >= 0 {
		tree.endBulkNames()
	}
	return el, len(decls)
}

// nameCache is a parse's direct-mapped cache in front of Tree.intern, keyed
// on where a name's strings live rather than on what they say. The tokenizer
// interns the names it returns and a declaration's URI is one string for
// every element in its scope, so a name met again arrives in the same
// storage: comparing pointers and lengths finds it without hashing or
// comparing its text. Strings are immutable, so the same storage is the same
// text; a name in other storage only misses.
type nameCache [64]struct {
	q   QName
	idx uint32
}

func (c *nameCache) intern(t *Tree, q QName) uint32 {
	p := uintptr(unsafe.Pointer(unsafe.StringData(q.Local))) ^
		uintptr(unsafe.Pointer(unsafe.StringData(q.URI)))>>3
	e := &c[uint32(p)*0x9E3779B9>>26] // the top 6 bits: len(c) is 64
	if sameString(e.q.Local, q.Local) && sameString(e.q.Prefix, q.Prefix) && sameString(e.q.URI, q.URI) {
		return e.idx
	}
	e.q, e.idx = q, t.intern(q)
	return e.idx
}

// sameString reports whether a and b are the same storage, which makes them
// equal; equal strings in different storage report false.
func sameString(a, b string) bool {
	return len(a) == len(b) && (len(a) == 0 || unsafe.StringData(a) == unsafe.StringData(b))
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

// textRun accumulates one run of character data. The text node is appended
// when the run starts, so that it takes its place in document order, and its
// value is stored when the run ends.
type textRun struct {
	node   *Node // the text node being built, nil between runs
	buf    []byte
	stored bool // the run did not start with white space and is in the store
}

func (r *textRun) add(parent *Node, b []byte, dec *xml.Decoder) {
	if len(b) == 0 {
		return
	}
	if r.stored {
		r.node.AppendValue(string(b))
		return
	}
	if r.node == nil {
		r.node = parent.appendChild(KindText)
		r.buf = r.buf[:0]
		// A run that starts with anything but white space can be neither
		// stripped nor shared, so it goes straight to the store.
		if !onlySpace(b) {
			if len(b) >= textOwnFrom && cap(b)-len(b) <= len(b)/8 && (dec.Size <= 0 || dec.Size-int(dec.InputOffset()) < len(b)) {
				// Long enough for a block of its own, not much larger than
				// its value, and with less input left than its length, so
				// that no later value will want the buffer grown for this
				// one: the decoder's buffer becomes the block, rather than
				// being copied into one. A document of many long values
				// keeps the buffer and copies, or each would regrow it.
				r.node.v0, r.node.v1 = r.node.tree.text.keep(dec.TakeText())
			} else {
				r.node.v0, r.node.v1 = r.node.tree.text.addBytes(b, 0)
			}
			r.stored = true
			return
		}
	}
	r.buf = append(r.buf, b...)
}

// flush stores the value of the node being built, if any, and ends the run.
// A whitespace-only run inside an element strip selects is taken back out:
// it is the last record appended, so dropping it leaves the tree as if it
// had never been made. It reports whether the run held anything but white
// space.
func (r *textRun) flush(spaces *spaceTable, strip func(*Node) bool) bool {
	if r.node == nil {
		return false
	}
	n := r.node
	r.node = nil
	if r.stored {
		r.stored = false
		return true
	}
	t := n.tree
	space := onlySpace(r.buf)
	if space && strip(n.Parent()) {
		t.rec(n.parent).v1 = n.prev
		t.n--
		return false
	}
	n.v0, n.v1 = spaces.text(&t.text, r.buf)
	return !space
}

// spaceTable shares one stored value among the whitespace-only text runs of a
// parse. An indented document repeats a handful of them -- a newline and the
// same indentation, over and over -- and storing each once saves the bytes of
// every repeat.
type spaceTable struct {
	m     map[string][2]uint32
	byLen *[maxSpaceLen + 1][2]uint32 // made past few, so a small tree does not pay for it
	few   [spaceFew][2]uint32         // the first distinct values, until byLen is made
	nFew  uint8
	// noMap keeps m unmade: ParseBursts empties the table at a burst when
	// its values are not all in home.
	noMap bool
	// home is the block the shared values are stored in while it has room,
	// + 1 (0: none): ParseBursts makes it before its first mark, so that no
	// rewind releases them. hi is the highest block any value is in, + 1.
	home, hi int32
}

const (
	maxSpaceTable = 256
	maxSpaceLen   = 128
	spaceFew      = 4 // distinct values shared before the length table is made
)

// spaceHome is the size of a spaceTable's home block.
const spaceHome = 4 << 10

// store stores b, a value the table is to share.
func (t *spaceTable) store(s *textStore, b []byte) (uint32, uint32) {
	if t.home != 0 {
		k := t.home - 1
		if blk := s.blocks[k]; cap(blk)-len(blk) >= len(b) {
			s.blocks[k] = append(blk, b...)
			return uint32(k)<<textShift | uint32(len(blk)), uint32(len(b))
		}
	}
	off, n := s.addBytes(b, 0)
	t.hi = max(t.hi, int32(off>>textShift)+1)
	return off, n
}

// forget empties the table if any of its values is in a block from the
// block numbered from on, which a rewind is releasing. Its home stays if it
// is before from.
func (t *spaceTable) forget(from int) {
	if int(t.hi) <= from {
		return
	}
	home := t.home
	if int(home) > from {
		home = 0
	}
	*t = spaceTable{noMap: true, home: home, hi: home}
}

func (t *spaceTable) text(s *textStore, b []byte) (uint32, uint32) {
	if len(b) > maxSpaceLen || !onlySpace(b) {
		return s.addBytes(b, 0)
	}
	if t.byLen == nil {
		for _, r := range t.few[:t.nFew] {
			if s.str(r[0], r[1]) == string(b) {
				return r[0], r[1]
			}
		}
		if t.nFew < spaceFew {
			off, n := t.store(s, b)
			t.few[t.nFew] = [2]uint32{off, n}
			t.nFew++
			return off, n
		}
		t.byLen = new([maxSpaceLen + 1][2]uint32)
	}
	if r := t.byLen[len(b)]; r[1] != 0 && s.str(r[0], r[1]) == string(b) {
		return r[0], r[1]
	}
	if r, ok := t.m[string(b)]; ok {
		t.byLen[len(b)] = r
		return r[0], r[1]
	}
	off, n := t.store(s, b)
	r := [2]uint32{off, n}
	t.byLen[len(b)] = r
	if len(t.m) < maxSpaceTable && !t.noMap {
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

// firstParseDecls is the DTD as the first parse of a rewritten source read it:
// the entity table, the normalized attribute defaults, the declared types and
// element declarations, the external subset's text, and the validity faults
// recorded while reading them, all of which the second parse cannot rebuild
// from the internal subset alone.
type firstParseDecls struct {
	ents           *entityTable
	attDefaults    []attDefault
	attTypes       []attDeclaredType
	elemDecls      map[string]uint8
	externalSubset string
	notes          []dtdNote
}

// parseExpandedSteps re-parses a document whose entities hold markup, as
// parseSteps parses one.
//
// It exists because encoding/xml cannot expand such an entity: dec.Entity maps
// a name to a string and the decoder substitutes that string as character
// data, without re-scanning it. An entity declared as "<b/>" therefore reaches
// the tree as four characters rather than as an element, which is not what XML
// says an entity is.
//
// The substitution is done on the source and the document parsed again. The
// second parse declares no entities — they are already substituted — so it
// cannot recurse back into here. It decides from the first parse's
// declarations (first), so a reference to an entity declared nowhere is
// handled as the first parse would: dropped and recorded where every
// declaration was read, refused in the decoder, which names it, otherwise.
func parseExpandedSteps(src string, first *firstParseDecls, opts ParseOptions) (func() (*Tree, error), func()) {
	ents := first.ents
	expanded, err := ents.substituteMarkupEntities(src)
	if err != nil {
		return func() (*Tree, error) { return nil, fmt.Errorf("parse XML: %w", err) }, func() {}
	}
	// The expansion bounds have already been applied by the substitution, so
	// the re-parse is of text of a size this package has agreed to.
	sub := opts
	sub.entitiesExpanded = true
	sub.entityBases = ents.baseSpans
	sub.firstParse = first
	return parseSteps(strings.NewReader(expanded), "", sub)
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

// srcTee copies what is read from r into buf until buf is set to nil, and
// indexes its lines in lines when that is set.
type srcTee struct {
	r     io.Reader
	buf   *strings.Builder
	lines *lineIndex
}

func (t *srcTee) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if t.buf != nil {
		t.buf.Write(p[:n])
	}
	if t.lines != nil {
		t.lines.add(p[:n])
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
