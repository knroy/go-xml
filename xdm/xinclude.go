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

	// maxIncludeDepth bounds how deeply inclusions may nest. Like the fetch
	// count it is a resource budget, never a stand-in for the loop rule: a
	// loop is caught by URI on p.stack at any depth, including depth zero,
	// and exceeding this bound reports "resource limit exceeded" so that a
	// caller can tell a defective document from an expensive one.
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

// ProcessXInclude performs XInclude processing on tree and returns the result
// as a new tree.
//
// XInclude 1.0 section 4 is written as a transformation from one infoset to
// another, and that is what this does: the result is built top-down, in
// document order, as any tree is, and tree itself is left as it was. Callers
// use the returned tree; node identities in tree do not carry over.
func ProcessXInclude(tree *Tree, opts XIncludeOptions) (*Tree, error) {
	if tree == nil || tree.Root == nil {
		return tree, nil
	}
	// The including document's own URI is on the stack from the start, so
	// that a document including itself is a loop at the first step rather
	// than one level later.
	base := tree.Root.baseURI
	if base == "" {
		base = tree.Root.documentURI
	}
	p := &includeProc{opts: opts, budget: &entityBudget{}}
	if base != "" {
		p.stack = append(p.stack, base)
	}
	return p.processTree(tree, base, 0)
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
	opts  XIncludeOptions
	stack []string // URIs currently being included, innermost last
	// fetches counts resources read across the WHOLE pass, which is why it
	// lives here rather than being recomputed per document.
	fetches int
	// budget is the entity-expansion spend shared by every document this pass
	// parses, for exactly the same reason fetches is: an inclusion is part of
	// the document that asked for it, not a document of its own with its own
	// allowance. Without this, each ParseString below minted a fresh budget
	// and maxTotalEntityBytes bounded 200 included documents separately
	// instead of together — 95 KB of source expanded to 149 MB with
	// MaxBytes 8192 and MaxNodes 50, neither of which can see an expansion.
	budget *entityBudget
	// done maps each element of a document being processed whose content
	// processing has finished to its processed copy, which an href-less
	// xpointer selects from in its place. See selectLocal.
	done map[*Node]*Node
}

// processTree builds the included form of t: a new tree with every xi:include
// replaced by what it includes.
func (p *includeProc) processTree(t *Tree, base string, depth int) (*Tree, error) {
	out := NewTree()
	out.CopySourceFrom(t)
	out.Root.baseURI = t.Root.baseURI
	out.Root.documentURI = t.Root.documentURI
	out.Root.CopyTypingFrom(t.Root)
	if err := p.copyChildren(out.Root, t.Root, base, depth); err != nil {
		return nil, err
	}
	out.Finalize()
	return out, nil
}

// copyChildren appends to dst copies of src's children, replacing each
// xi:include it finds by what it includes.
//
// base is the base URI in force for src itself; each child may narrow it with
// its own xml:base, which the parser has already resolved into the node's
// base URI.
func (p *includeProc) copyChildren(dst, src *Node, base string, depth int) error {
	for _, c := range src.children {
		if c.kind == KindElement && c.name.URI == NSXInclude {
			switch c.name.Local {
			case "include":
				if err := p.include(dst, c, depth); err != nil {
					return err
				}
				continue
			case "fallback":
				// Section 3.2: xi:fallback is only meaningful as a child of
				// xi:include, and anywhere else it is a fatal error rather
				// than ordinary content.
				return fmt.Errorf("xi:fallback outside xi:include is a fatal error")
			}
		}
		if c.kind != KindElement {
			appendLeafKeep(dst, c)
			continue
		}
		cc := appendElementKeep(dst, c)
		cb := c.baseURI
		if cb == "" {
			cb = base
		}
		if err := p.copyChildren(cc, c, cb, depth); err != nil {
			return err
		}
		// An href-less inclusion selects from the including document as far
		// as it has been processed: see selectLocal.
		if p.done == nil {
			p.done = map[*Node]*Node{}
		}
		p.done[c] = cc
	}
	return nil
}

// appendElementKeep appends a copy of el without its children: name, base URI,
// typing, position, namespace declarations and attributes.
func appendElementKeep(dst, el *Node) *Node {
	c := dst.AppendShallowCopy(el)
	CopyPosition(c, el)
	for _, ns := range el.namespaces {
		c.AddNamespace(ns.name.Local, ns.value)
	}
	for _, a := range el.attrs {
		CopyPosition(c.AppendShallowCopy(a), a)
	}
	return c
}

// appendLeafKeep appends a copy of a text, comment or processing-instruction
// node. Text merges into a text node just before it, and empty text is
// dropped: an inclusion can leave two text nodes side by side, or an empty
// one, and the result infoset holds neither.
func appendLeafKeep(dst, n *Node) *Node {
	if n.kind == KindText {
		if n.value == "" {
			return nil
		}
		if k := len(dst.children); k > 0 && dst.children[k-1].kind == KindText {
			last := dst.children[k-1]
			last.AppendValue(n.value)
			return last
		}
	}
	c := dst.AppendShallowCopy(n)
	CopyPosition(c, n)
	return c
}

// include appends to dst what one xi:include element includes.
//
// The inclusion is built in a holder first and only appended once it has
// succeeded: a failure anywhere in it must leave dst as it was, so that the
// fallback can take its place.
func (p *includeProc) include(dst, inc *Node, depth int) error {
	holder, err := p.expandInclude(inc, depth)
	if err != nil {
		return err
	}
	for _, n := range holder.children {
		appendSubtreeKeep(dst, n)
	}
	return nil
}

// appendSubtreeKeep appends a deep copy of n, merging text as appendLeafKeep
// does.
func appendSubtreeKeep(dst, n *Node) {
	if n.kind != KindElement {
		appendLeafKeep(dst, n)
		return
	}
	c := appendElementKeep(dst, n)
	for _, k := range n.children {
		appendSubtreeKeep(c, k)
	}
}

// newHolder returns the document node an inclusion is built under before it
// is appended where the include element was. It belongs to a tree so that the
// positions of what it holds survive the move.
func newHolder() *Node { return NewTree().Root }

// expandInclude computes, in a holder, the replacement for one xi:include
// element.
func (p *includeProc) expandInclude(inc *Node, depth int) (*Node, error) {
	if depth >= maxIncludeDepth {
		return nil, fatalInclude{fmt.Errorf(
			"resource limit exceeded: xi:include nesting exceeds %d levels: %w",
			maxIncludeDepth, ErrResourceLimit)}
	}

	// Section 3.2: an include element may have "zero or one fallback"
	// children. Two is a fatal error, and it is checked before the resource
	// is read so that the defect is reported on its own terms rather than
	// only when the inclusion happens to fail.
	seen := 0
	for _, c := range inc.children {
		if c.kind == KindElement && c.name.URI == NSXInclude {
			if c.name.Local != "fallback" {
				// Section 3.1: the content of xi:include is "(fallback?)",
				// so any other XInclude-namespace child is a fatal error.
				return nil, fmt.Errorf(
					"xi:%s is not permitted as a child of xi:include", c.name.Local)
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

	var holder *Node
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
		holder, err = p.selectLocal(inc, xptr, base)
	case href == "" && parse == "text":
		// parse="text" with no href asks for the including document as
		// characters. That is not a subresource selection and the resource
		// genuinely has to be read, so it goes down the ordinary path and
		// the loop rule does not apply — a text inclusion cannot recurse.
		holder, err = p.fetchText(target, base, encoding)
	default:
		holder, err = p.fetch(target, base, parse, xptr, encoding, depth)
	}
	if err == nil {
		return holder, nil
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
		holder := newHolder()
		if err := p.copyChildren(holder, fb, fbBase, depth+1); err != nil {
			return nil, err
		}
		return holder, nil
	}
	// Section 4.3: "if the fallback element is absent, it is a fatal error."
	return nil, fmt.Errorf("xi:include of %q failed and has no xi:fallback: %w", target, err)
}

// selectLocal resolves an xpointer against the including document, for an
// inclusion that names no href.
//
// The selection is made in the document as far as inclusion has processed it:
// an element whose content is complete is seen with its inclusions done, as
// it will appear in the result, and everything else as it was read -- which
// is what an xpointer naming content an earlier inclusion brought in needs.
// See viewOf.
//
// The selection is COPIED. Section 4.5.1 replaces the include element with
// the included content, and the content selected is still where it was. A
// copy is also the only reading that makes sense when the pointer selects an
// ancestor of the include element itself.
//
// The nodes are given the include element's base, because they are being
// written where the include element sat, not where they were read.
func (p *includeProc) selectLocal(inc *Node, xptr, base string) (*Node, error) {
	root := inc.Root()
	picked, err := selectXPointerIn(root, xptr, p.viewOf)
	if err != nil {
		return nil, err
	}
	if len(picked) == 0 {
		return nil, fmt.Errorf("xpointer %q selected nothing in the including document", xptr)
	}
	holder := newHolder()
	for _, n := range picked {
		// Section 4.5.1: an inclusion of the include element itself, or of
		// one of its ancestors, is an inclusion loop and a fatal error. It
		// cannot be caught by the URI stack, because the URI is the same
		// document's and there is no second fetch to compare against.
		if n == inc || isAncestorOf(n, inc) {
			return nil, fatalInclude{fmt.Errorf(
				"xpointer %q selects the include element or an ancestor of it", xptr)}
		}
		copySubtree(holder, n, base)
	}
	return holder, nil
}

// viewOf is the node a local xpointer sees in place of n: the processed copy
// of an element whose content inclusion has finished with, and n otherwise.
func (p *includeProc) viewOf(n *Node) *Node {
	if c, ok := p.done[n]; ok {
		return c
	}
	return n
}

// fetchText reads a resource as characters.
func (p *includeProc) fetchText(target, base, encoding string) (*Node, error) {
	if p.fetches >= maxIncludeFetches {
		return nil, fatalInclude{fmt.Errorf(
			"resource limit exceeded: document performs more than %d inclusions: %w",
			maxIncludeFetches, ErrResourceLimit)}
	}
	if p.opts.Resolver == nil {
		return nil, fmt.Errorf("no include resolver: inclusions are not permitted")
	}
	p.fetches++
	data, _, err := p.opts.Resolver.ResolveInclude(target, base, textEncodingMarker(encoding))
	if err != nil {
		return nil, err
	}
	holder := newHolder()
	holder.AppendText(string(data))
	return holder, nil
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
	for cur := n.parent; cur != nil; cur = cur.parent {
		if cur == a {
			return true
		}
	}
	return false
}

// copySubtree appends a deep copy of n to dst, giving the copy the base URI it
// must carry where it is going: an element keeps its own, and every other node
// takes the base of the element it is copied under.
func copySubtree(dst, n *Node, base string) {
	c := dst.AppendShallowCopy(n)
	c.baseURI = base
	if n.baseURI != "" {
		c.baseURI = n.baseURI
	}
	for _, a := range n.attrs {
		c.AppendShallowCopy(a).baseURI = ""
	}
	for _, ns := range n.namespaces {
		c.AddNamespace(ns.name.Local, ns.value)
	}
	for _, k := range n.children {
		copySubtree(c, k, c.baseURI)
	}
}

// fetch reads one resource and returns, in a holder, the nodes that replace
// the xi:include element.
func (p *includeProc) fetch(target, base, parse, xptr, encoding string, depth int) (*Node, error) {
	if parse == "text" {
		return p.fetchText(target, base, encoding)
	}
	if p.fetches >= maxIncludeFetches {
		return nil, fatalInclude{fmt.Errorf(
			"resource limit exceeded: document performs more than %d inclusions: %w",
			maxIncludeFetches, ErrResourceLimit)}
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
			return nil, fatalInclude{fmt.Errorf(
				"circular xi:include loop: %q includes itself", uri)}
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
	// The included document expands its entities against the SAME budget as
	// the including one. See includeProc.budget.
	popts.entityBudget = p.budget
	sub, err := ParseString(string(data), popts)
	if err != nil {
		err = fmt.Errorf("parsing included %s: %w", uri, err)
		// A resource-limit refusal is this processor declining to spend more,
		// not a condition of the resource, so it is fatal on the same terms
		// as the fetch and nesting bounds above. Left recoverable, a chain of
		// xi:fallback elements is a way to keep asking after being told no:
		// measured, eight sibling includes each falling back to another
		// entity bomb expanded 6,291,456 bytes — six times the ceiling — and
		// the document was accepted, the refusal swallowed at every level.
		if errors.Is(err, ErrResourceLimit) {
			return nil, fatalInclude{err}
		}
		return nil, err
	}

	// Process the included document before selecting, so that an xpointer
	// may address content that an inner inclusion brought in. The stack
	// grows for the duration.
	p.stack = append(p.stack, uri)
	done, err := p.processTree(sub, uri, depth+1)
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
		picked = done.Root.children
	} else {
		picked, err = selectXPointer(done.Root, xptr)
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

	holder := newHolder()
	for _, n := range picked {
		copyFixedUp(holder, n, base)
	}
	return holder, nil
}

// copyFixedUp appends a copy of n, a top-level included node, with the base
// URI fixup of section 4.5.5 applied.
//
// The included elements are about to sit in a document retrieved from a
// *different* URI, so a relative reference written inside them would resolve
// against the wrong place unless their base is recorded explicitly. The
// specification says to add an xml:base attribute to each top-level included
// element whose base differs from that of the include element.
//
// An element that ALREADY carries an xml:base keeps it. The specification text
// says the existing attribute "is replaced by the new attribute", but the XSLT
// 3.0 test suite's base-uri-052 asserts the opposite behaviour and notes in
// its own source that Xerces does not replace it — and a replacement would in
// any case throw away information the document deliberately stated.
//
// includeBase is the base URI in force at the xi:include element. A node whose
// own base already equals it needs no attribute: the value it would carry is
// the one it inherits anyway, and adding a redundant xml:base changes what the
// document looks like when it is serialised for no gain.
func copyFixedUp(dst, n *Node, includeBase string) {
	// Only element information items have an xml:base to carry. A comment or
	// PI included alongside the document element has a base URI in the data
	// model, but nothing can be written on it and nothing resolves a relative
	// reference from it.
	if n.kind != KindElement || n.baseURI == "" || n.baseURI == includeBase {
		appendSubtreeKeep(dst, n)
		return
	}
	if xb := n.Attr(NSXML, "base"); xb != nil {
		// The element states its own base. Leaving it alone has a consequence
		// the attribute alone does not show: a RELATIVE xml:base now sits in
		// a different document, so it resolves against the include element's
		// base rather than the included document's, and the node's computed
		// base must be recomputed to match what the attribute will mean where
		// the node now lives. This is the whole of base-uri-052's fifth
		// assertion: dir/data2.xml holds <para xml:base="dir5/data.xml">;
		// included into a document based at fn/base-uri/ the surviving
		// attribute reads fn/base-uri/dir5/data.xml, which is what the case
		// expects.
		c := appendElementKeep(dst, n)
		c.baseURI = resolveBase(includeBase, xb.value)
		copyRebased(c, n, c.baseURI)
		return
	}
	c := appendElementKeep(dst, n)
	c.AppendAttr(QName{Prefix: "xml", Local: "base", URI: NSXML}, n.baseURI)
	for _, k := range n.children {
		appendSubtreeKeep(c, k)
	}
}

// copyRebased appends to dst, the copy of n whose base was just recomputed,
// copies of n's children with each descendant's xml:base re-resolved against
// the new base.
//
// Only elements that actually carry an xml:base change: an element whose base
// was set by something other than an attribute -- an external entity the
// included document read -- keeps that absolute URI, and it governs
// everything below it.
func copyRebased(dst, n *Node, base string) {
	for _, k := range n.children {
		if k.kind != KindElement {
			appendSubtreeKeep(dst, k)
			continue
		}
		c := appendElementKeep(dst, k)
		switch xb := k.Attr(NSXML, "base"); {
		case xb != nil:
			c.baseURI = resolveBase(base, xb.value)
			copyRebased(c, k, c.baseURI)
		case k.baseURI != "":
			copyRebased(c, k, k.baseURI)
		default:
			copyRebased(c, k, base)
		}
	}
}

// fallbackOf returns the xi:fallback child of an xi:include, or nil.
//
// XInclude 1.0 section 3.2 permits at most one, and more than one is a fatal
// error — but this is a lookup used on the failure path, and reporting "two
// fallbacks" instead of the failure that actually occurred would bury the
// cause. validateInclude checks the cardinality up front instead.
func fallbackOf(inc *Node) *Node {
	for _, c := range inc.children {
		if c.kind == KindElement && c.name.URI == NSXInclude && c.name.Local == "fallback" {
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
	for cur := n; cur != nil; cur = cur.parent {
		if cur.baseURI != "" {
			return cur.baseURI
		}
		if cur.kind == KindDocument && cur.documentURI != "" {
			return cur.documentURI
		}
	}
	return ""
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
	return selectXPointerIn(root, ptr, nil)
}

// selectXPointerIn is selectXPointer over a view of the tree: view, when not
// nil, gives the node seen in place of each child as the walk reaches it.
func selectXPointerIn(root *Node, ptr string, view func(*Node) *Node) ([]*Node, error) {
	ptr = strings.TrimSpace(ptr)
	if ptr == "" {
		return nil, fmt.Errorf("empty xpointer")
	}
	// A shorthand pointer is an NCName with no parenthesis anywhere: the
	// XPointer Framework distinguishes the two forms by exactly that.
	if !strings.Contains(ptr, "(") {
		if n := elementByIDIn(root, ptr, view); n != nil {
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
			n, err := elementScheme(root, part.data, view)
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
func elementScheme(root *Node, data string, view func(*Node) *Node) (*Node, error) {
	data = strings.TrimSpace(data)
	if data == "" {
		return nil, fmt.Errorf("empty element() pointer")
	}
	var cur *Node
	steps := strings.Split(data, "/")
	if steps[0] != "" {
		// Rooted at an ID rather than at the document.
		cur = elementByIDIn(root, steps[0], view)
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
		var kids []*Node
		for _, c := range cur.children {
			if view != nil {
				c = view(c)
			}
			if c.kind == KindElement {
				kids = append(kids, c)
			}
		}
		if idx < 1 || idx > len(kids) {
			return nil, fmt.Errorf("element() pointer %q: no child %d", data, idx)
		}
		cur = kids[idx-1]
	}
	if cur.kind != KindElement {
		return nil, fmt.Errorf("element() pointer %q does not select an element", data)
	}
	return cur, nil
}

// ElementByID finds the element whose ID is id.
//
// It is exported because a bare-name fragment identifier means the same thing
// wherever it appears: XPointer Framework section 3.2 defines the shorthand
// pointer as selecting the element with a matching ID, and xsl:source-document
// resolves its href fragment by that rule (see xslt/sourcedoc.go).
//
// xml:id is honoured unconditionally, and a DTD-declared ID attribute is
// honoured through Node.IsID, which the DTD machinery sets. A plain attribute
// merely *named* "id" is deliberately not treated as one: without a DTD or a
// schema saying so it is an ordinary attribute, and guessing would make an
// inclusion resolve differently depending on data the document never declared.
func ElementByID(n *Node, id string) *Node { return elementByIDIn(n, id, nil) }

func elementByIDIn(n *Node, id string, view func(*Node) *Node) *Node {
	if n.kind == KindElement {
		for _, a := range n.attrs {
			if a.value != id {
				continue
			}
			if a.isID || (a.name.URI == NSXML && a.name.Local == "id") {
				return n
			}
		}
	}
	for _, c := range n.children {
		if view != nil {
			c = view(c)
		}
		if f := elementByIDIn(c, id, view); f != nil {
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
