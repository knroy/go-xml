package xdm

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// XInclude processing, per XML Inclusions (XInclude) Version 1.0 (Second
// Edition), W3C Recommendation 15 November 2006.
//
// XInclude is a *document-level* transformation rather than a parsing feature:
// section 4 defines it over an already-built information set ("the source
// infoset") and produces another one. That is why it lives here as a pass over
// a finished Tree rather than as a hook inside the parser. A pass has three
// practical advantages over weaving it into the tokeniser: the included
// document is parsed by the very same Parse with the very same limits, an
// xi:fallback subtree is already built and can simply be moved, and a cycle is
// detected by looking at a stack of URIs rather than at a partially built tree.
//
// It is entirely opt-in. Nothing in this package calls it; a caller that wants
// inclusions runs ProcessXInclude explicitly and supplies the resolver that
// does the reading. That is deliberate and matches how every other read this
// library performs is gated: xdm has no filesystem and no network of its own,
// so the confinement — permitted schemes, permitted directories, symlink
// resolution — is the resolver's alone, and xslt.FileResolver implements it
// with exactly the same resolvePath that gates fn:doc, xsl:include and
// external entities. XInclude therefore cannot become a wider hole than
// fn:doc already is, because it reaches the filesystem through the same gate.

// NSXInclude is the XInclude namespace. XInclude 1.0 section 3: "elements in
// the XInclude namespace ... http://www.w3.org/2001/XInclude".
const NSXInclude = "http://www.w3.org/2001/XInclude"

// IncludeResolver reads the resource an xi:include names.
//
// It is separate from EntityResolver even though both read a URI, because the
// two answer different questions and a caller must be able to permit one
// without the other. An external entity is named by a document's own DOCTYPE
// and is refused by default as the XXE surface; an inclusion is named by an
// element the caller can see in the document it handed over. Folding them into
// one interface would mean enabling inclusions silently enabled entity reads.
//
// The returned uri is the URI of the resource actually read, which is what
// anything *inside* the included resource resolves against — a resolver that
// follows a redirect, or that canonicalises a path, must report where it
// landed rather than where it was asked to look. XInclude 1.0 section 4.5.1
// makes that base the one the included subtree carries.
//
// The encoding argument carries the xi:include encoding attribute, which
// section 3.1 says "specifies the encoding of the resource" and applies only
// to parse="text". It is empty for an XML inclusion, where the encoding is the
// resource's own business and is discovered by the XML parser from a BOM or a
// declaration — section 4.4 is explicit that encoding "is ignored" there.
type IncludeResolver interface {
	ResolveInclude(href, base, encoding string) (data []byte, uri string, err error)
}

// Limits on one XInclude pass.
//
// Both bound work an *including document* can ask for, so both are needed even
// though a cycle is detected separately: a cycle is a repeat of a URI, while a
// fan-out of a thousand distinct small files repeats nothing and still costs a
// thousand parses. The nesting bound exists for the same reason the parser has
// MaxDepth — a chain of includes recurses in Go.
const (
	// maxIncludeFetches bounds how many resources one pass may read in total,
	// counted across the whole recursion rather than per document.
	maxIncludeFetches = 200

	// maxIncludeDepth bounds how deeply inclusions may nest.
	maxIncludeDepth = 40
)

// XIncludeOptions configures ProcessXInclude.
type XIncludeOptions struct {
	// Resolver reads the resources. A nil Resolver makes every href fail,
	// which is not the same as doing nothing: a failed inclusion still uses
	// its xi:fallback, and is still a fatal error when it has none. That is
	// the correct reading of section 4.3, and it means "no resolver" behaves
	// as a resolver that refuses everything rather than as a silent no-op.
	Resolver IncludeResolver

	// Parse carries the options an *included* XML resource is parsed with.
	// The including document's own limits are the natural choice and the
	// caller supplies them: an inclusion is a part of the document as far as
	// the data model is concerned, so it should not be able to sidestep a
	// bound the including document was held to. BaseURI and DocumentURI are
	// overwritten per resource and anything set here for them is ignored.
	Parse ParseOptions
}

// ProcessXInclude performs XInclude processing on tree in place.
//
// The tree is modified rather than copied. XInclude 1.0 section 4 is written
// as a transformation from one infoset to another, and building a second tree
// would be the more literal reading — but every node in this package carries a
// pointer to its Tree and its document order, so a copy would have to rebuild
// both anyway, and the caller's other references to the tree would then point
// at the *unincluded* document. Modifying in place and re-finalising is the
// behaviour a caller loading a document actually wants.
//
// The tree is re-finalised before returning, so document order is correct over
// the merged content. Callers must not hold node identities across this call.
func ProcessXInclude(tree *Tree, opts XIncludeOptions) error {
	if tree == nil || tree.Root == nil {
		return nil
	}
	base := tree.Root.BaseURI
	if base == "" {
		base = tree.Root.DocumentURI
	}
	p := &includeProc{opts: opts}
	// The including document is on the stack from the outset, so that an
	// inclusion naming the document it appears in is caught as the cycle it
	// is rather than read a second time. XInclude 1.0 section 4.5: "an
	// inclusion loop ... is a fatal error".
	if base != "" {
		p.stack = append(p.stack, base)
	}
	if err := p.expandChildren(tree.Root, base, 0); err != nil {
		return err
	}
	tree.Finalize()
	return nil
}

// fatalInclude marks an error that xi:fallback must NOT recover from.
//
// XInclude draws the line at what the fallback is *for*: section 4.3 gives it
// to a resource that "cannot be fetched", which is a condition of the world
// rather than a defect in the document or a refusal by the processor. A loop
// is fatal by section 4.5 whatever the document says next, and the two
// resource bounds are this implementation refusing to spend more — letting a
// fallback past either would mean a document could get a different answer by
// being expensive, and a fallback chain would become a way to keep asking
// after being told no.
type fatalInclude struct{ err error }

func (e fatalInclude) Error() string { return e.err.Error() }
func (e fatalInclude) Unwrap() error { return e.err }

func fatalIncludeError(err error) bool {
	var f fatalInclude
	return errors.As(err, &f)
}

// includeProc carries the state of one pass.
type includeProc struct {
	opts    XIncludeOptions
	stack   []string // URIs currently being included, innermost last
	fetches int
}

// expandChildren walks n's children, replacing each xi:include it finds.
//
// The walk is iterative over a rebuilt child slice rather than recursive over
// the original, because an inclusion replaces one child with zero or more and
// mutating a slice while ranging over it is how that goes wrong quietly.
//
// base is the base URI in force for n itself; each child may narrow it with
// its own xml:base, which the parser has already resolved into Node.BaseURI.
func (p *includeProc) expandChildren(n *Node, base string, depth int) error {
	var out []*Node
	changed := false
	for _, c := range n.Children {
		if c.Kind == KindElement && c.Name.URI == NSXInclude {
			switch c.Name.Local {
			case "include":
				repl, err := p.expandInclude(c, depth)
				if err != nil {
					return err
				}
				for _, r := range repl {
					r.Parent = n
					r.tree = n.tree
					out = append(out, r)
				}
				changed = true
				continue
			case "fallback":
				// XInclude 1.0 section 3.2: xi:fallback "is only meaningful
				// as the child of an include element". One found anywhere
				// else is a fatal error, and saying so is better than
				// silently copying an element whose whole purpose was to be
				// consumed by a parent that is not there.
				return fmt.Errorf("xi:fallback outside xi:include is a fatal error")
			}
		}
		// A non-include element may still contain one, and its own xml:base
		// governs how that one's href resolves.
		if c.Kind == KindElement {
			cb := c.BaseURI
			if cb == "" {
				cb = base
			}
			if err := p.expandChildren(c, cb, depth); err != nil {
				return err
			}
		}
		out = append(out, c)
	}
	if changed {
		n.Children = out
		// Adjacent text nodes can appear where an inclusion sat between two
		// of them, or where parse="text" produced one beside an existing
		// one. The data model does not permit two adjacent text children —
		// XDM section 6.1 requires text node siblings to be merged — so a
		// document that kept them would answer a different node count than
		// the same content written literally.
		mergeAdjacentText(n)
	}
	return nil
}

// expandInclude computes the replacement for one xi:include element.
func (p *includeProc) expandInclude(inc *Node, depth int) ([]*Node, error) {
	if depth >= maxIncludeDepth {
		return nil, fatalInclude{fmt.Errorf("xi:include nesting exceeds %d levels", maxIncludeDepth)}
	}

	// Section 3.2: an include element may have "zero or one fallback"
	// children. Two is a fatal error, and it is checked before the resource
	// is read so that the defect is reported on its own terms rather than
	// only when the inclusion happens to fail.
	seen := 0
	for _, c := range inc.Children {
		if c.Kind == KindElement && c.Name.URI == NSXInclude {
			if c.Name.Local != "fallback" {
				// Section 3.1: the content of xi:include is "(fallback?)",
				// so any other XInclude-namespace child is a fatal error.
				return nil, fmt.Errorf(
					"xi:%s is not permitted as a child of xi:include", c.Name.Local)
			}
			seen++
		}
	}
	if seen > 1 {
		return nil, fmt.Errorf("xi:include has more than one xi:fallback")
	}

	href := inc.AttrValue("href")
	if href != "" {
		if err := ValidateXIncludeHref(href); err != nil {
			return nil, err
		}
	}
	parse := inc.AttrValue("parse")
	if parse == "" {
		// XInclude 1.0 section 3.1: parse "has a default value of xml".
		parse = "xml"
	}
	if parse != "xml" && parse != "text" {
		// Section 3.1 makes an unrecognised value a fatal error rather than
		// something to fall back from: the attribute is the processor's
		// instruction, not the resource's condition, so a fallback would be
		// answering the wrong question.
		return nil, fmt.Errorf("xi:include parse=%q must be \"xml\" or \"text\"", parse)
	}
	xptr := inc.AttrValue("xpointer")

	// The base against which href is resolved is the base URI of the
	// xi:include element itself — section 4.1.1, "the value of the href
	// attribute is ... resolved against the base URI of the include
	// element". The parser has already folded every enclosing xml:base into
	// Node.BaseURI, so this is a lookup rather than a walk.
	base := elementBase(inc)

	// Section 3.1: "If the href attribute is absent, the value ... is the
	// URI of the document containing the include element", i.e. the
	// including document itself. Combined with the cycle stack that makes a
	// bare <xi:include/> with no xpointer a loop, which is what section 4.5
	// says it is.
	target := href
	if target == "" {
		target = base
	}

	// Section 4.4: the encoding attribute applies to a text inclusion only,
	// and "is ignored" for parse="xml" — an XML resource says how it is
	// encoded in its own declaration, and letting an attribute of the
	// *including* document override that would make one document decide how
	// another one is read.
	encoding := ""
	if parse == "text" {
		encoding = inc.AttrValue("encoding")
	}

	var nodes []*Node
	var err error
	switch {
	case href == "" && parse == "xml" && xptr != "":
		// An href-less include with an xpointer addresses a SUBRESOURCE of
		// the document the include element sits in. Section 4.5's loop rule
		// is about including a document in itself — "the inclusion history
		// ... contains the resource being included" — and a subresource is
		// not that document: the selection terminates, because what it
		// yields is a part of a tree that already exists.
		//
		// Reading the file again would be wrong twice over. It would report
		// a loop for something that is not one, which is what DocBook's own
		// xinclude.003 through .018 do — every one of them opens with an
		// <xi:include xpointer="xpath(...)"/> that quotes a later include's
		// own pointer back at the reader. And it would address a *reparse*
		// rather than this tree, so an xpointer naming content an earlier
		// inclusion brought in would not find it.
		nodes, err = p.selectLocal(inc, xptr, base)
	case href == "" && parse == "text":
		// parse="text" with no href asks for the including document as
		// characters. That is not a subresource selection and the resource
		// genuinely has to be read, so it goes down the ordinary path and
		// the loop rule does not apply — a text inclusion cannot recurse.
		nodes, err = p.fetchText(target, base, encoding)
	default:
		nodes, err = p.fetch(target, base, parse, xptr, encoding, depth)
	}
	if err == nil {
		return nodes, nil
	}

	// Section 4.3: "If the resource ... cannot be fetched ... the processor
	// must recover by using the fallback element". The failure is reported
	// only when there is nothing to recover with.
	//
	// A FATAL error is not such a failure and must not be laundered into a
	// successful transform by a fallback: section 4.5 makes a loop fatal
	// outright, and the two resource bounds are refusals by this processor
	// rather than conditions of the resource. Falling back on them would mean
	// a document could quietly get a different result by being expensive, and
	// a fallback chain would become a way to keep asking after being told no.
	if fatalIncludeError(err) {
		return nil, err
	}
	if fb := fallbackOf(inc); fb != nil {
		// The fallback's own content may itself contain xi:include elements
		// — section 3.2 says the fallback is "an inclusion which is used
		// when the original inclusion fails", and an inclusion is processed
		// like any other content. The depth is carried through so that a
		// fallback chain cannot be used to sidestep the nesting bound.
		fbBase := elementBase(fb)
		if err := p.expandChildren(fb, fbBase, depth+1); err != nil {
			return nil, err
		}
		kids := fb.Children
		fb.Children = nil
		return kids, nil
	}
	// Section 4.3: "if the fallback element is absent, it is a fatal error."
	return nil, fmt.Errorf("xi:include of %q failed and has no xi:fallback: %w", target, err)
}

// selectLocal resolves an xpointer against the tree the include element is
// already part of, for an inclusion that names no href.
//
// The selection is COPIED rather than moved. Section 4.5.1 replaces the
// include element with the included content, and moving a node that is still
// reachable from elsewhere in the same document would put one node in two
// places — which the data model does not have, and which would make the
// subtree's parent link a lie. A copy is also the only reading that makes
// sense when the pointer selects an ancestor of the include element itself.
//
// The nodes are given the include element's base, because they are being
// written where the include element sat, not where they were read.
func (p *includeProc) selectLocal(inc *Node, xptr, base string) ([]*Node, error) {
	root := inc.Root()
	picked, err := selectXPointer(root, xptr)
	if err != nil {
		return nil, err
	}
	if len(picked) == 0 {
		return nil, fmt.Errorf("xpointer %q selected nothing in the including document", xptr)
	}
	out := make([]*Node, 0, len(picked))
	for _, n := range picked {
		if n == inc || isAncestorOf(n, inc) {
			// Copying an ancestor of the include element would embed the
			// include element inside its own replacement, which is a loop by
			// another name — section 4.5's rule applied to a subresource.
			return nil, fatalInclude{fmt.Errorf(
				"xpointer %q selects the include element or an ancestor of it", xptr)}
		}
		c := copySubtree(n, base)
		out = append(out, c)
	}
	return out, nil
}

// fetchText reads a resource as characters.
//
// It is separate from fetch because a text inclusion shares none of fetch's
// XML work — no parse, no recursion, no cycle stack, no base fixup — and it
// is reached both from the ordinary path and from the href-less one.
func (p *includeProc) fetchText(target, base, encoding string) ([]*Node, error) {
	if p.fetches >= maxIncludeFetches {
		return nil, fatalInclude{fmt.Errorf("document performs more than %d inclusions", maxIncludeFetches)}
	}
	if p.opts.Resolver == nil {
		return nil, fmt.Errorf("no include resolver: inclusions are not permitted")
	}
	p.fetches++
	data, _, err := p.opts.Resolver.ResolveInclude(target, base, textEncodingMarker(encoding))
	if err != nil {
		return nil, err
	}
	// Section 3.1 and 4.4: a text inclusion contributes "a single text
	// information item whose character code property is the character
	// sequence" of the resource. It is NOT parsed, so markup in it is data —
	// which is the whole point of parse="text", and is why an included
	// fragment of source code does not have to be escaped.
	//
	// The decode is the resolver's: it holds the bytes, and it is the same
	// decode fn:unparsed-text already implements.
	return []*Node{{Kind: KindText, Value: string(data)}}, nil
}

// textEncodingMarker makes the resolver's encoding argument non-empty for a
// text inclusion even when the include element named no encoding.
//
// The interface uses an empty encoding to mean "this is an XML inclusion, hand
// the bytes over undecoded". A text inclusion with no encoding attribute still
// wants the text decode — the resolver then applies the same defaulting
// fn:unparsed-text does, reading a declaration or assuming UTF-8 — so it needs
// a value that is not the empty string. "UTF-8" is what F&O defaults to, and
// the resolver's own XML-declaration override still outranks it.
func textEncodingMarker(encoding string) string {
	if encoding == "" {
		return "UTF-8"
	}
	return encoding
}

// isAncestorOf reports whether a is an ancestor of n.
func isAncestorOf(a, n *Node) bool {
	for cur := n.Parent; cur != nil; cur = cur.Parent {
		if cur == a {
			return true
		}
	}
	return false
}

// copySubtree deep-copies a node, giving the copy the base URI it must carry
// where it is going.
//
// Attributes and namespaces are copied too: a copy that shared them would have
// two elements owning one attribute node, and the attribute's Parent could
// name only one of them.
func copySubtree(n *Node, base string) *Node {
	c := &Node{
		Kind:           n.Kind,
		Name:           n.Name,
		Value:          n.Value,
		BaseURI:        base,
		TypeAnnotation: n.TypeAnnotation,
		UnionMember:    n.UnionMember,
		IsID:           n.IsID,
		IsIDREFS:       n.IsIDREFS,
		IsNilled:       n.IsNilled,
	}
	if n.BaseURI != "" {
		// The node stated a base of its own, which travels with it.
		c.BaseURI = n.BaseURI
	}
	for _, a := range n.Attrs {
		c.AddAttr(&Node{Kind: KindAttribute, Name: a.Name, Value: a.Value,
			IsID: a.IsID, IsIDREFS: a.IsIDREFS, TypeAnnotation: a.TypeAnnotation})
	}
	for _, ns := range n.Namespaces {
		c.AddNamespace(ns.Name.Local, ns.Value)
	}
	for _, k := range n.Children {
		c.AppendChild(copySubtree(k, c.BaseURI))
	}
	return c
}

// fetch reads one resource and turns it into the nodes that replace the
// xi:include element.
func (p *includeProc) fetch(target, base, parse, xptr, encoding string, depth int) ([]*Node, error) {
	if parse == "text" {
		return p.fetchText(target, base, encoding)
	}
	if p.fetches >= maxIncludeFetches {
		return nil, fatalInclude{fmt.Errorf("document performs more than %d inclusions", maxIncludeFetches)}
	}
	if p.opts.Resolver == nil {
		return nil, fmt.Errorf("no include resolver: inclusions are not permitted")
	}
	p.fetches++

	data, uri, err := p.opts.Resolver.ResolveInclude(target, base, encoding)
	if err != nil {
		return nil, err
	}

	// A cycle is checked on the URI the resolver reports rather than on the
	// href as written, so that two spellings of one file are the same node
	// on the stack. Section 4.5 makes an inclusion loop a fatal error, and
	// without this the recursion below simply does not terminate.
	for _, u := range p.stack {
		if u == uri {
			return nil, fatalInclude{fmt.Errorf("xi:include loop: %q includes itself", uri)}
		}
	}

	popts := p.opts.Parse
	popts.BaseURI = uri
	// The included document was retrieved by URI, so it has a
	// dm:document-uri of its own — but that property belongs to a *document
	// node*, and after inclusion there is no document node for it: section
	// 4.5.1 says an included document's document information item is
	// discarded and its children take its place. Leaving it empty is
	// therefore the accurate answer rather than a lost one.
	popts.DocumentURI = ""
	sub, err := ParseString(string(data), popts)
	if err != nil {
		return nil, fmt.Errorf("parsing included %s: %w", uri, err)
	}

	// Recurse before selecting, so that an xpointer may address content that
	// an inner inclusion brought in. The stack grows for the duration.
	p.stack = append(p.stack, uri)
	err = p.expandChildren(sub.Root, uri, depth+1)
	p.stack = p.stack[:len(p.stack)-1]
	if err != nil {
		return nil, err
	}

	var picked []*Node
	if xptr == "" {
		// Section 4.5.1: the document information item is discarded and the
		// "children property" of the included document supplies the
		// replacement — so the document element comes through along with any
		// comments and processing instructions around it. A document node
		// cannot appear as a child of an element, which is why it is dropped
		// rather than copied.
		picked = sub.Root.Children
	} else {
		picked, err = selectXPointer(sub.Root, xptr)
		if err != nil {
			return nil, err
		}
		if len(picked) == 0 {
			// Section 4.4: an xpointer that "does not identify any
			// resource" is an error, recoverable through fallback — which
			// is what returning an error here achieves, since the caller
			// consults xi:fallback on any error from this function.
			return nil, fmt.Errorf("xpointer %q selected nothing in %s", xptr, uri)
		}
	}

	// Base URI fixup, section 4.5.5. The included elements are about to sit
	// in a document retrieved from a *different* URI, so a relative
	// reference written inside them would resolve against the wrong place
	// unless their base is recorded explicitly. The specification says to
	// add an xml:base attribute to each top-level included element whose
	// base differs from that of the include element.
	//
	// An element that ALREADY carries an xml:base keeps it. The specification
	// text says the existing attribute "is replaced by the new attribute",
	// but the XSLT 3.0 test suite's base-uri-052 asserts the opposite
	// behaviour and notes in its own source that Xerces does not replace it —
	// and a replacement would in any case throw away information the document
	// deliberately stated. Node.BaseURI has already folded such an attribute
	// against the included document's URI, so keeping it is also the answer
	// that leaves fn:base-uri consistent with what the attribute says.
	//
	// This is the only place the tree is given an attribute it did not have,
	// and it is exactly what base-uri-052 tests.
	fixupBase(picked, base)

	// Detach from the sub-tree so the nodes belong to the including document
	// alone; the caller re-parents them and ProcessXInclude re-finalises.
	for _, n := range picked {
		n.Parent = nil
	}
	return picked, nil
}

// fixupBase records the base URI of included top-level nodes, per section
// 4.5.5, so that a relative reference inside them still resolves.
//
// includeBase is the base URI in force at the xi:include element. A node whose
// own base already equals it needs no attribute: the value it would carry is
// the one it inherits anyway, and adding a redundant xml:base changes what the
// document looks like when it is serialised for no gain.
func fixupBase(nodes []*Node, includeBase string) {
	for _, n := range nodes {
		if n.Kind != KindElement {
			// Only element information items have an xml:base to carry.
			// A comment or PI included alongside the document element has a
			// base URI in the data model, but nothing can be written on it
			// and nothing resolves a relative reference from it.
			continue
		}
		if n.BaseURI == "" || n.BaseURI == includeBase {
			continue
		}
		if xb := n.Attr(NSXML, "base"); xb != nil {
			// The element states its own base. Xerces leaves such an
			// attribute alone rather than replacing it, and base-uri-052
			// asserts that behaviour — but "leaving it alone" has a
			// consequence the attribute alone does not show: a RELATIVE
			// xml:base now sits in a different document, so it resolves
			// against the include element's base rather than the included
			// document's, and the node's computed base must be recomputed to
			// match what the attribute will mean where the node now lives.
			//
			// This is the whole of base-uri-052's fifth assertion. dir/data2.xml
			// holds <para xml:base="dir5/data.xml">; parsed on its own that
			// is dir/dir5/data.xml, but included into a document based at
			// fn/base-uri/ the surviving attribute reads fn/base-uri/dir5/data.xml,
			// which is what the case expects. Leaving BaseURI as parsed made
			// fn:base-uri disagree with the document's own serialisation.
			n.BaseURI = resolveBase(includeBase, xb.Value)
			// The subtree below inherited the old resolution, so it is
			// rebased too; nothing else in the tree can see the change.
			rebaseDescendants(n, n.BaseURI)
			continue
		}
		n.AddAttr(&Node{
			Kind:  KindAttribute,
			Name:  QName{Prefix: "xml", Local: "base", URI: NSXML},
			Value: n.BaseURI,
		})
	}
}

// rebaseDescendants recomputes Node.BaseURI below n after n's own base
// changed, by re-resolving each descendant's xml:base against the new one.
//
// Only elements that actually carry an xml:base need touching: the parser sets
// BaseURI on an element only where an attribute or an external entity gave it
// one, and everything else inherits by walking up at the time of asking. The
// walk therefore stops recursing where nothing changed, which is almost
// everywhere.
func rebaseDescendants(n *Node, base string) {
	for _, c := range n.Children {
		if c.Kind != KindElement {
			continue
		}
		if xb := c.Attr(NSXML, "base"); xb != nil {
			c.BaseURI = resolveBase(base, xb.Value)
			rebaseDescendants(c, c.BaseURI)
			continue
		}
		if c.BaseURI != "" {
			// Set by something other than an attribute — an external entity
			// the included document read. That URI is absolute and does not
			// depend on where the subtree ends up, so it stands and governs
			// everything below it.
			rebaseDescendants(c, c.BaseURI)
			continue
		}
		rebaseDescendants(c, base)
	}
}

// fallbackOf returns the xi:fallback child of an xi:include, or nil.
//
// XInclude 1.0 section 3.2 permits at most one, and more than one is a fatal
// error — but this is a lookup used on the failure path, and reporting "two
// fallbacks" instead of the failure that actually occurred would bury the
// cause. validateInclude checks the cardinality up front instead.
func fallbackOf(inc *Node) *Node {
	for _, c := range inc.Children {
		if c.Kind == KindElement && c.Name.URI == NSXInclude && c.Name.Local == "fallback" {
			return c
		}
	}
	return nil
}

// elementBase returns the base URI in force at n, walking to an ancestor when
// n itself carries none.
//
// The parser sets Node.BaseURI on an element only where an xml:base or an
// external entity gave it one, so an ordinary element deep in a document has
// the field empty and inherits from above.
func elementBase(n *Node) string {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.BaseURI != "" {
			return cur.BaseURI
		}
		if cur.Kind == KindDocument && cur.DocumentURI != "" {
			return cur.DocumentURI
		}
	}
	return ""
}

// mergeAdjacentText joins text children that ended up next to each other.
//
// XDM section 6.1: "the children of a document or element node ... must not
// contain two consecutive text nodes". Inclusion is one of the few operations
// that can produce them — a parse="text" inclusion written between two lines
// of text leaves three text nodes where the data model says there is one — and
// leaving them would make count(text()) answer differently for content that is
// indistinguishable once serialised. An empty text node is dropped for the
// same reason: the data model has no zero-length text node.
func mergeAdjacentText(n *Node) {
	var out []*Node
	for _, c := range n.Children {
		if c.Kind == KindText {
			if c.Value == "" {
				continue
			}
			if len(out) > 0 && out[len(out)-1].Kind == KindText {
				prev := out[len(out)-1]
				prev.Value += c.Value
				continue
			}
		}
		out = append(out, c)
	}
	n.Children = out
}

// selectXPointer applies an xpointer attribute to an included document.
//
// Only the two schemes XInclude 1.0 section 4.2 requires a conforming
// processor to support are implemented:
//
//   - a SHORTHAND pointer, which "identifies the element ... whose ID is the
//     same as the shorthand pointer" (XPointer Framework section 3.2);
//   - the ELEMENT scheme, a child sequence such as element(/1/2), and its
//     form rooted at an ID, element(intro/2).
//
// The xmlns() scheme is parsed and its bindings are accepted so that a pointer
// written as a scheme sequence does not fail on the wrapper, but xpointer()
// and xpath() — full XPath in an attribute — are deliberately NOT implemented.
// They are not required by XInclude, they would pull the whole XPath evaluator
// into this package and invert its dependency direction, and a scheme sequence
// that names an unsupported scheme is *defined* to fall through to the next
// one, so refusing them is conforming behaviour rather than a gap.
func selectXPointer(root *Node, ptr string) ([]*Node, error) {
	ptr = strings.TrimSpace(ptr)
	if ptr == "" {
		return nil, fmt.Errorf("empty xpointer")
	}
	// A shorthand pointer is an NCName with no parenthesis anywhere: the
	// XPointer Framework distinguishes the two forms by exactly that.
	if !strings.Contains(ptr, "(") {
		if n := elementByID(root, ptr); n != nil {
			return []*Node{n}, nil
		}
		return nil, fmt.Errorf("no element with ID %q", ptr)
	}
	// A scheme-based pointer is a sequence of scheme(data) parts, tried in
	// order until one identifies a subresource — XPointer Framework section
	// 3.3, "the pointer parts are evaluated in the order they occur".
	var lastErr error
	for _, part := range schemeParts(ptr) {
		switch part.scheme {
		case "element":
			n, err := elementScheme(root, part.data)
			if err != nil {
				lastErr = err
				continue
			}
			return []*Node{n}, nil
		case "xmlns":
			// A namespace binding for later parts. Nothing implemented here
			// consumes one — the element scheme has no names in it — so it
			// is skipped rather than rejected, which is what "a pointer part
			// that does not identify a subresource" calls for.
			continue
		default:
			lastErr = fmt.Errorf("xpointer scheme %q is not supported", part.scheme)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("xpointer %q identifies nothing", ptr)
	}
	return nil, lastErr
}

type xptrPart struct{ scheme, data string }

// schemeParts splits a scheme-based pointer into its parts.
//
// The data of a part runs to its BALANCED closing parenthesis, and a "^" may
// escape a parenthesis or another circumflex inside it — XPointer Framework
// section 4.2. A naive split on the first ")" cuts element(/1/2) correctly and
// cuts a pointer holding a parenthesis in the wrong place, so the balance is
// counted rather than assumed.
func schemeParts(ptr string) []xptrPart {
	var parts []xptrPart
	i := 0
	for i < len(ptr) {
		for i < len(ptr) && (ptr[i] == ' ' || ptr[i] == '\t' || ptr[i] == '\n' || ptr[i] == '\r') {
			i++
		}
		open := strings.IndexByte(ptr[i:], '(')
		if open < 0 {
			break
		}
		scheme := strings.TrimSpace(ptr[i : i+open])
		// A scheme name may be prefixed; only the local part selects the
		// scheme, since the prefix would have to be bound by an xmlns part
		// and none of the supported schemes is in a namespace.
		if c := strings.IndexByte(scheme, ':'); c >= 0 {
			scheme = scheme[c+1:]
		}
		j := i + open + 1
		depth := 1
		var sb strings.Builder
		for j < len(ptr) && depth > 0 {
			switch ptr[j] {
			case '^':
				if j+1 < len(ptr) {
					sb.WriteByte(ptr[j+1])
					j += 2
					continue
				}
				j++
			case '(':
				depth++
				sb.WriteByte('(')
				j++
			case ')':
				depth--
				if depth > 0 {
					sb.WriteByte(')')
				}
				j++
			default:
				sb.WriteByte(ptr[j])
				j++
			}
		}
		parts = append(parts, xptrPart{scheme: scheme, data: sb.String()})
		i = j
	}
	return parts
}

// elementScheme resolves an element() child sequence.
//
// XPointer element() scheme: the data is either a child sequence "/1/2/3"
// counting element children from one, or an NCName naming an element by ID
// optionally followed by such a sequence.
func elementScheme(root *Node, data string) (*Node, error) {
	data = strings.TrimSpace(data)
	if data == "" {
		return nil, fmt.Errorf("empty element() pointer")
	}
	var cur *Node
	steps := strings.Split(data, "/")
	if steps[0] != "" {
		// Rooted at an ID rather than at the document.
		cur = elementByID(root, steps[0])
		if cur == nil {
			return nil, fmt.Errorf("no element with ID %q", steps[0])
		}
	} else {
		cur = root
	}
	for _, s := range steps[1:] {
		if s == "" {
			return nil, fmt.Errorf("malformed element() pointer %q", data)
		}
		idx := 0
		for _, r := range s {
			if r < '0' || r > '9' {
				return nil, fmt.Errorf("malformed element() pointer %q", data)
			}
			idx = idx*10 + int(r-'0')
		}
		kids := cur.ChildElements()
		if idx < 1 || idx > len(kids) {
			return nil, fmt.Errorf("element() pointer %q: no child %d", data, idx)
		}
		cur = kids[idx-1]
	}
	if cur.Kind != KindElement {
		return nil, fmt.Errorf("element() pointer %q does not select an element", data)
	}
	return cur, nil
}

// elementByID finds the element whose ID is id.
//
// xml:id is honoured unconditionally, and a DTD-declared ID attribute is
// honoured through Node.IsID, which the DTD machinery sets. A plain attribute
// merely *named* "id" is deliberately not treated as one: without a DTD or a
// schema saying so it is an ordinary attribute, and guessing would make an
// inclusion resolve differently depending on data the document never declared.
func elementByID(n *Node, id string) *Node {
	if n.Kind == KindElement {
		for _, a := range n.Attrs {
			if a.Value != id {
				continue
			}
			if a.IsID || (a.Name.URI == NSXML && a.Name.Local == "id") {
				return n
			}
		}
	}
	for _, c := range n.Children {
		if f := elementByID(c, id); f != nil {
			return f
		}
	}
	return nil
}

// ValidateXIncludeHref reports whether an href is one this package will accept
// before a resolver is consulted.
//
// XInclude 1.0 section 4.1.1 forbids a fragment identifier in href: "the value
// of the href attribute must not contain a fragment identifier", because the
// xpointer attribute is where a subresource is named. It is a fatal error
// rather than a fallback condition, since it is a defect in the including
// document rather than a property of the resource.
func ValidateXIncludeHref(href string) error {
	if strings.Contains(href, "#") {
		return fmt.Errorf(
			"xi:include href %q must not contain a fragment identifier; "+
				"use the xpointer attribute", href)
	}
	if _, err := url.Parse(href); err != nil {
		return fmt.Errorf("xi:include href %q is not a valid URI reference: %w", href, err)
	}
	return nil
}
