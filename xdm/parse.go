package xdm

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strings"
)

// ParseOptions controls document construction.
type ParseOptions struct {
	// BaseURI is recorded on the document node and used to resolve relative
	// references in fn:document and xsl:include.
	BaseURI string

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
	if trackPos {
		r = io.TeeReader(r, &srcBuf)
	}

	dec := xml.NewDecoder(r)
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
	// CharsetReader is likewise left nil, which makes a document declaring
	// any encoding other than UTF-8 an error rather than routing it through
	// a converter this package does not control.

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
	cur := tree.Root
	depth := 0
	sawRoot := false
	// Attribute defaults declared by an ATTLIST in the internal subset. Kept
	// as a slice because a document rarely declares more than a handful, and
	// the common case is none at all.
	var attDefaults []attDefault

	for {
		// InputOffset after Token() is the position *after* the token, so the
		// start of the element must be taken before it is read.
		start := dec.InputOffset()
		tok, err := dec.Token()
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
				attDefaults = append(attDefaults, parseAttListDefaults(d)...)
				// Internal general entities are declared here and referenced
				// in content, so the table has to be installed before the
				// decoder reads any. encoding/xml consults dec.Entity lazily,
				// which makes that possible: the DOCTYPE is always the first
				// token.
				//
				// Only internal entities are expanded. One declared SYSTEM or
				// PUBLIC is recorded as refused, so referencing it is an
				// error rather than a fetch — this does not open XXE.
				if ents := parseInternalEntities(d); ents != nil {
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
// Go's decoder has already resolved Name.Space to a URI, but it reports xmlns
// declarations as ordinary attributes with Space "xmlns" (or Local "xmlns" for
// the default). Those must become namespace nodes rather than attributes: the
// attribute axis must not return them.
func buildElement(t xml.StartElement, parent *Node, offset int32) *Node {
	// Parent is linked here, before prefix recovery below, because
	// findPrefix walks ancestors: an element that inherits its prefix
	// declaration from an ancestor (rather than declaring it itself) would
	// otherwise find nothing and serialise without its prefix. AppendChild
	// sets the same link again, harmlessly.
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
			// Some decoder paths report the resolved xmlns URI instead.
			el.AddNamespace(a.Name.Local, a.Value)
		default:
			attr := &Node{
				Kind:  KindAttribute,
				Name:  QName{URI: a.Name.Space, Local: a.Name.Local},
				Value: a.Value,
			}
			// xml:base changes the base URI for the subtree.
			if a.Name.Space == NSXML && a.Name.Local == "base" {
				el.BaseURI = a.Value
			}
			el.AddAttr(attr)
		}
	}

	el.Name = QName{URI: t.Name.Space, Local: t.Name.Local}
	// Recover the prefix the author used, which the decoder discarded. It is
	// needed only for serialisation, so an unresolvable case falls back to the
	// default namespace rather than failing the parse.
	el.Name.Prefix = findPrefix(el, t.Name.Space)
	for _, a := range el.Attrs {
		if a.Name.URI != "" {
			a.Name.Prefix = findPrefix(el, a.Name.URI)
		}
	}
	return el
}

// findPrefix locates a prefix bound to uri in scope at el.
func findPrefix(el *Node, uri string) string {
	if uri == "" {
		return ""
	}
	if uri == NSXML {
		return "xml"
	}
	for cur := el; cur != nil; cur = cur.Parent {
		for _, ns := range cur.Namespaces {
			if ns.Value == uri {
				return ns.Name.Local
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
