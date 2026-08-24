package xdm

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/url"
	"strings"
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
	// Position tracking needs the source text to count lines in, and the
	// reader is consumed by the decoder. Tee it into a buffer rather than
	// reading it all up front, so that a parse failing early on a huge
	// document does not first pull the whole thing into memory.
	// UTF-16 is decoded to UTF-8 first. XML 1.0 §4.3.3 makes both encodings
	// mandatory, and encoding/xml reads only UTF-8 — so without this a
	// UTF-16 document fails with "invalid UTF-8" rather than being read.
	// This happens before the tee, so that position tracking counts lines
	// in the text the decoder actually sees.
	decoded, err := decodeReader(r)
	if err != nil {
		return nil, fmt.Errorf("parse XML: %w", err)
	}
	r = decoded

	// The byte limit wraps the reader, so it bounds what is read rather
	// than what a caller remembered to check. One byte over the limit is
	// read deliberately: hitting it is then distinguishable from a document
	// that happens to be exactly the maximum size.
	maxBytes := opts.MaxBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}
	var counted *countingReader
	if maxBytes > 0 {
		counted = &countingReader{r: io.LimitReader(r, maxBytes+1), max: maxBytes}
		r = counted
	}

	trackPos := opts.TrackPositions
	var srcBuf strings.Builder
	// The source is kept when positions are tracked, and also when a DOCTYPE
	// is permitted: an entity whose replacement text holds markup forces a
	// re-parse of the substituted source, and by the time that is known the
	// reader is partly consumed and the decoder has buffered ahead into it.
	//
	// The cost is a second copy of the document for the length of the parse,
	// paid by every caller that sets AllowDOCTYPE rather than only those who
	// turn out to need it. Dropping it once the DOCTYPE is read would mean
	// replacing the decoder mid-stream, which loses its lookahead — so the
	// copy stays. entitiesExpanded marks the second parse, which has no
	// entities left to find and so needs no copy at all.
	keepSrc := trackPos || (opts.AllowDOCTYPE && !opts.entitiesExpanded)
	if keepSrc {
		r = io.TeeReader(r, &srcBuf)
	}

	dec := xml.NewDecoder(r)
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

	tree := NewTree()
	tree.Root.BaseURI = opts.BaseURI
	tree.Root.DocumentURI = opts.DocumentURI
	cur := tree.Root
	depth := 0
	sawRoot := false
	// Attribute defaults declared by an ATTLIST in the internal subset. Kept
	// as a slice because a document rarely declares more than a handful, and
	// the common case is none at all.
	var attDefaults []attDefault
	var attTypes []attDeclaredType

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
		// xml.EndElement below. Token does NOT reject a duplicated attribute
		// — verified against the standard library — so nothing is lost there
		// either, and adding the check here would be new strictness rather
		// than parity: it was tried, and it rejected our own serialiser's
		// output for an element that undeclares the default namespace.
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse XML: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > maxDepth {
				return nil, fmt.Errorf("parse XML: nesting exceeds %d levels", maxDepth)
			}
			if cur == tree.Root {
				if sawRoot {
					return nil, fmt.Errorf("parse XML: multiple root elements")
				}
				sawRoot = true
			}
			if len(attDefaults) > 0 {
				t = applyAttDefaults(t, attDefaults)
			}
			el := buildElement(t, cur, encodeOffset(start, trackPos))
			if len(attTypes) > 0 {
				applyAttTypes(el, attTypes)
			}
			// Attributes and namespaces are nodes too, and a document made
			// of elements carrying many attributes allocates most of its
			// memory in them, so they count against the limit.
			nodes += 1 + len(el.Attrs) + len(el.Namespaces)
			if maxNodes > 0 && nodes > maxNodes {
				return nil, fmt.Errorf(
					"parse XML: document exceeds %d nodes", maxNodes)
			}
			cur.AppendChild(el)
			cur = el

		case xml.EndElement:
			if cur.Parent == nil {
				return nil, fmt.Errorf("parse XML: unbalanced end element %q", t.Name.Local)
			}
			// RawToken does not pair tags, so the pairing is checked here on
			// the lexical name — which is what XML §3 requires anyway: the
			// end tag must repeat the start tag's QName character for
			// character, not merely resolve to the same expanded name.
			if got, want := lexicalName(t.Name), cur.Name.Lexical(); got != want {
				return nil, fmt.Errorf(
					"parse XML: element %q closed by end element %q", want, got)
			}
			if opts.StripSpace != nil {
				stripWhitespaceChildren(cur, opts.StripSpace)
			}
			cur = cur.Parent
			depth--

		case xml.CharData:
			// CharData is only meaningful inside an element; whitespace at the
			// document level is legal and carries no information.
			if cur == tree.Root {
				if strings.TrimSpace(string(t)) != "" {
					return nil, fmt.Errorf("parse XML: character data outside root element")
				}
				continue
			}
			appendText(cur, string(t))

		case xml.Comment:
			cur.AppendChild(&Node{Kind: KindComment, Value: string(t)})

		case xml.ProcInst:
			if strings.EqualFold(t.Target, "xml") {
				continue // the XML declaration is not a PI node in the XDM
			}
			cur.AppendChild(&Node{
				Kind:  KindPI,
				Name:  QName{Local: t.Target},
				Value: string(t.Inst),
			})

		case xml.Directive:
			d := strings.TrimSpace(string(t))
			if strings.HasPrefix(d, "DOCTYPE") && !opts.AllowDOCTYPE {
				return nil, fmt.Errorf("parse XML: DOCTYPE declaration rejected " +
					"(set AllowDOCTYPE to permit; it enables XXE and entity expansion)")
			}
			// An ATTLIST may give an attribute a #FIXED or literal default,
			// which a processor is required to add to every matching element
			// — including a namespace declaration, since "xmlns:p CDATA
			// #FIXED '...'" is how a DTD supplies a binding. Without this the
			// prefix is simply absent from the tree.
			//
			// Only defaults are read. Nothing here expands an entity,
			// resolves an external identifier, or reads a file, so this does
			// not widen what AllowDOCTYPE admits.
			if strings.HasPrefix(d, "DOCTYPE") {
				// Retained so a caller can validate against the document's
				// own DTD; see Tree.DocType.
				tree.DocType = d
				defs, types := parseAttList(d)
				attDefaults = append(attDefaults, defs...)
				attTypes = append(attTypes, types...)
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
					ents = newEntityTable(opts.BaseURI)
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
					// Retained so fn:unparsed-entity-uri can see declarations
					// that live outside the directive. The subset a document
					// is governed by is not always the text it was written
					// with.
					tree.externalSubset = ents.subsetText
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
					}
				} else {
					ents = parseEntityDecls(d, opts.BaseURI)
				}
				if ents != nil && !opts.entitiesExpanded {
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
					if dec.Entity == nil {
						dec.Entity = map[string]string{}
					}
					for k, v := range ents.entityMap() {
						dec.Entity[k] = v
					}
				}
			}
		}
	}

	if !sawRoot {
		return nil, fmt.Errorf("parse XML: no root element")
	}
	if cur != tree.Root {
		return nil, fmt.Errorf("parse XML: unexpected EOF, %q left open", cur.Name.Local)
	}

	if trackPos {
		// The decoder stops reading at the end of the root element, so the
		// tee holds everything up to there — which is all any offset can
		// point into.
		tree.src = srcBuf.String()
	}
	tree.Finalize()
	return tree, nil
}

// ParseString is Parse over a string, which is what most tests and the
// stylesheet compiler want.
func ParseString(s string, opts ParseOptions) (*Tree, error) {
	return Parse(strings.NewReader(s), opts)
}

// buildElement converts a StartElement into an element node with its namespace
// and attribute nodes separated.
//
// The token comes from Decoder.RawToken, so Name.Space holds the PREFIX the
// author wrote rather than a resolved URI, and resolution is done here against
// the namespace nodes in scope. That is the whole reason RawToken is used: the
// namespace-aware Decoder.Token discards the prefix and reports only the URI,
// which cannot be inverted — a document binding one URI to two prefixes has no
// way to say which one an element was written with, and guessing renamed
// a:foo to a2:foo and unprefixed <out> to <my:out> on serialisation.
//
// xmlns declarations arrive as ordinary attributes with Space "xmlns" (or
// Local "xmlns" for the default). Those must become namespace nodes rather
// than attributes: the attribute axis must not return them.
func buildElement(t xml.StartElement, parent *Node, offset int32) *Node {
	// Parent is linked here, before resolution below, because resolvePrefix
	// walks ancestors: an element using a prefix declared on an ancestor
	// would otherwise resolve to nothing.
	el := &Node{
		Kind:    KindElement,
		tree:    parent.tree,
		BaseURI: parent.BaseURI,
		Parent:  parent,
		offset:  offset,
	}

	for _, a := range t.Attr {
		switch {
		case a.Name.Space == "xmlns":
			el.AddNamespace(a.Name.Local, a.Value)
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			el.AddNamespace("", a.Value)
		case a.Name.Space == NSXMLNS:
			// applyAttDefaults and callers that hand-build a token may use
			// the resolved xmlns URI instead of the "xmlns" prefix.
			el.AddNamespace(a.Name.Local, a.Value)
		default:
			attr := &Node{
				Kind:  KindAttribute,
				Name:  QName{Prefix: a.Name.Space, Local: a.Name.Local},
				Value: a.Value,
			}
			// xml:base changes the base URI for the subtree.
			//
			// Its value is a URI *reference*, so a relative one is resolved
			// against the base already in force — the parent's, which is the
			// document's own location at the top. Storing it raw made
			// fn:base-uri return "sub/" for xml:base="sub/" instead of the
			// document's directory joined with it, and made every nested
			// xml:base lose everything its ancestors contributed.
			if a.Name.Space == "xml" && a.Name.Local == "base" {
				el.BaseURI = resolveBase(el.BaseURI, a.Value)
			}
			el.AddAttr(attr)
		}
	}

	el.Name = QName{
		Prefix: t.Name.Space,
		Local:  t.Name.Local,
		URI:    resolvePrefix(el, t.Name.Space, true),
	}
	for _, a := range el.Attrs {
		// An unprefixed attribute is in no namespace: the default namespace
		// declaration applies to elements only (Namespaces in XML 1.0 §6.2).
		if a.Name.Prefix != "" {
			a.Name.URI = resolvePrefix(el, a.Name.Prefix, false)
		}
	}
	return el
}

// resolvePrefix maps a prefix to the URI bound to it in scope at el.
//
// isElement selects whether an unbound (empty) prefix picks up the default
// namespace declaration: it does for an element name and never for an
// attribute name (Namespaces in XML 1.0 §6.2).
//
// An unbindable prefix resolves to the empty URI rather than failing the
// parse. Rejecting it here would turn a namespace-ill-formed document into a
// parse error in a package that is also asked to read stylesheet fragments and
// re-parsed entity expansions, and the XSLT and XSD layers above report their
// own diagnostics for a name they cannot resolve.
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
	for cur := el; cur != nil; cur = cur.Parent {
		for _, ns := range cur.Namespaces {
			if ns.Name.Local == prefix {
				// An empty value undeclares the namespace
				// (xmlns="" or, in XML Names 1.1, xmlns:p="").
				return ns.Value
			}
		}
	}
	return ""
}

// appendText adds character data, merging into a preceding text node.
//
// The XDM requires that no two text nodes be adjacent. encoding/xml splits
// character data at entity references and buffer boundaries, so without
// merging, "a&amp;b" would produce three text nodes and fn:count(text()) would
// return 3 instead of 1.
func appendText(parent *Node, s string) {
	if s == "" {
		return
	}
	if n := len(parent.Children); n > 0 {
		if last := parent.Children[n-1]; last.Kind == KindText {
			last.Value += s
			return
		}
	}
	parent.AppendChild(&Node{Kind: KindText, Value: s})
}

// stripWhitespaceChildren removes whitespace-only text children of el when the
// predicate selects el's name.
//
// This runs at EndElement, once all children are present, because a text node
// is only strippable if it is whitespace in its entirety and merging (above)
// may not be complete until the element closes.
func stripWhitespaceChildren(el *Node, strip func(QName) bool) {
	if !strip(el.Name) {
		return
	}
	// xml:space="preserve" overrides stripping for this element's content.
	if a := el.Attr(NSXML, "space"); a != nil && a.Value == "preserve" {
		return
	}
	kept := el.Children[:0]
	for _, c := range el.Children {
		if c.Kind == KindText && IsXMLWhitespace(c.Value) {
			continue
		}
		kept = append(kept, c)
	}
	el.Children = kept
}

// encodeOffset converts a decoder byte offset into the representation the
// Node.offset field uses: one greater than the true offset, so that a node
// built without one reads as "unknown" rather than as line 1.
//
// Offsets beyond what an int32 holds are dropped rather than truncated: a
// wrapped offset would name a confidently wrong line, and no position at all
// is the honest answer for a document over 2GB.
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
		return n, fmt.Errorf("document exceeds %d bytes", c.max)
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
	return Parse(strings.NewReader(expanded), sub)
}

// resolveBase resolves an xml:base value against the base already in force.
//
// An absolute reference replaces the base outright; a relative one is merged
// with it by the ordinary RFC 3986 rules. A base that is not usable as one is
// left alone rather than reported: parsing is not the place to raise a URI
// error, and fn:base-uri and fn:resolve-uri report it themselves when the
// value is actually used.
func resolveBase(base, ref string) string {
	if ref == "" {
		return base
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if r.IsAbs() || base == "" {
		return ref
	}
	b, err := url.Parse(base)
	if err != nil || !b.IsAbs() {
		return ref
	}
	return b.ResolveReference(r).String()
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
		b, err := io.ReadAll(input)
		if err != nil {
			return nil, err
		}
		for i, c := range b {
			if c > 0x7f {
				return nil, fmt.Errorf(
					"declared encoding %s but byte %d at offset %d is not ASCII",
					charset, c, i)
			}
		}
		return bytes.NewReader(b), nil
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
