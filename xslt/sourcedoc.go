package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// xsl:source-document, section 18.1.
//
// The instruction reads a document by URI and evaluates its body with that
// document's root as the context item. That is the whole of it: the spec
// defines the result as "the same as the result of the following
// (non-streaming) process", and then describes exactly that.
//
// The streamable attribute is therefore not part of the semantics. It asks the
// processor to evaluate the body in a streamed manner, which is a statement
// about memory rather than about the answer — a stylesheet that is
// guaranteed-streamable produces the same result either way, and one that is
// not is explicitly left to the processor. This engine does not stream, so it
// reads the document and evaluates the body, which is what a conforming
// non-streaming processor does with streamable="yes".
//
// use-accumulators is accepted and ignored for the same kind of reason: it
// names which accumulators apply to the document, and an engine that applies
// none is not made wrong by being told which ones to apply.

type sourceDocumentInstr struct {
	href       *avt
	validation validationSpec
	body       []Instruction
	// streamed is @streamable="yes". The evaluation is the same either way,
	// but XTDE3362 bars a non-streamable accumulator from being read over a
	// document the stylesheet asked to stream, so the request is recorded.
	streamed bool
	// baseURI is the base URI of the xsl:source-document element itself,
	// which is the stylesheet module's own unless an xml:base on the element
	// or an ancestor overrides it. 18.1 resolves @href "as for the doc
	// function", and fn:doc resolves against the static base URI of the
	// expression -- a per-element property, not a per-module one. Taking the
	// module's base instead ignored xml:base, which non-stream-004 catches.
	baseURI string
}

func (c *compiler) compileSourceDocument(n *xdm.Node) (Instruction, error) {
	hrefSrc := n.AttrValue("href")
	if hrefSrc == "" {
		return nil, fmt.Errorf(
			"XTSE0010: xsl:source-document requires an href attribute")
	}
	ns := newNSResolver(n, "")
	href, err := compileAVT(hrefSrc, ns)
	if err != nil {
		return nil, fmt.Errorf("in xsl:source-document/@href: %w", err)
	}
	spec, err := compileValidation(n, "")
	if err != nil {
		return nil, err
	}
	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	return &sourceDocumentInstr{href: href, validation: spec, body: body,
		streamed: isYes(n.AttrValue("streamable")), baseURI: ns.baseURI}, nil
}

func (i *sourceDocumentInstr) Execute(rt *runtime, out *outputBuilder) error {
	href, err := i.href.eval(rt)
	if err != nil {
		return err
	}
	root, err := i.load(rt, href)
	if err != nil {
		return err
	}
	// The body sees the document root as a singleton focus, exactly as
	// xsl:for-each over a one-item sequence would. The current template rule
	// is cleared for the same reason it is there: the body is a new
	// selection, so xsl:next-match inside it has nothing to match against.
	if i.streamed {
		rt.streamedTrees[root] = true
	}
	sub := rt.withCurrent(root, 1, 1).clearCurrentRule()
	return execSequence(i.body, sub, out)
}

// load retrieves the document and applies the instruction's validation.
//
// 18.1.1 gives validation and type "the same effect as the corresponding
// attributes of the xsl:copy-of instruction when applied to a document node",
// and xsl:copy-of validates a *copy*. The distinction is not cosmetic here:
// the resolver caches its trees, so annotating or stripping the retrieved
// document in place would change what a later fn:doc of the same URI — or a
// second xsl:source-document over it — sees.
func (i *sourceDocumentInstr) load(rt *runtime, href string) (*xdm.Node, error) {
	// A URI that is not a URI at all is FODC0005, and it is reported before
	// any retrieval is attempted -- so it does not depend on whether a
	// resolver is configured, and it is not the FODC0002 that a resource
	// which merely could not be fetched raises. non-stream-006 asks for
	// "c:\my\doc\books.xml": a Windows filename, whose backslashes are not
	// legal URI characters, so it is rejected here rather than being read as
	// a "c" scheme.
	if err := validSourceDocumentURI(href); err != nil {
		return nil, err
	}
	docs := rt.ctx.Docs
	if docs == nil {
		return nil, fmt.Errorf(
			"FODC0002: document access is disabled (no resolver configured): %q",
			href)
	}
	// 18.1 resolves @href as fn:doc does, against the static base URI of the
	// expression -- the base URI of the xsl:source-document element, which an
	// xml:base on it or an ancestor may move.
	base := i.baseURI
	if base == "" {
		base = rt.ctx.StaticBaseURI
	}
	if base == "" {
		if n, ok := rt.ctx.Item.(*xdm.Node); ok {
			base = n.BaseURI
		}
	}
	tree, err := resolveDocumentIn(rt.ctx, href, base)
	if err != nil {
		return nil, fmt.Errorf("FODC0002: cannot retrieve %q: %w", href, err)
	}
	if i.validation.isDefault() {
		return fragmentOf(rt.sheet.stripInputAnnotations(tree.Root), href)
	}
	copied := xdm.NewTree()
	copied.Root.BaseURI = tree.Root.BaseURI
	for _, ch := range tree.Root.Children {
		copied.Root.AppendChild(deepCopy(ch))
	}
	copied.Finalize()
	if err := i.validation.assess(rt, copied.Root); err != nil {
		return nil, err
	}
	// Stripping happens after assessment, not instead of it. 3.5 scopes
	// input-type-annotations to the source trees of 4.4, and "any document
	// read using xsl:stream" — this instruction's former name — is one of
	// them. validation="strict" still has to run: an invalid document is an
	// error whether or not the annotations it would have produced survive.
	// Only the annotations go, which is what makes
	// "data(.) instance of xs:decimal" false over a validated document.
	return fragmentOf(rt.sheet.stripInputAnnotations(copied.Root), href)
}

// fragmentOf applies the fragment identifier of href, if there is one, to the
// document that href retrieved.
//
// The resolver deliberately drops the fragment before it reaches the
// filesystem -- a fragment names a part of a resource, not a different
// resource, so two hrefs differing only in their fragment must retrieve one
// document (XSLT 2.0 section 16.1, and see resolver.go). Applying it is
// therefore this instruction's job, and until now nothing did it: the whole
// document was returned and the fragment silently discarded, which is what
// docbook-004 caught.
//
// 18.1 says of xsl:source-document/@href that "the process of obtaining a
// document node given a URI is the same as for the doc function", and the
// media type here is an XML one, whose fragment identifiers RFC 7303 defines
// as a bare name -- an XML Name selecting the element with that ID -- or an
// XPointer. Only the bare-name form is honoured: it is the shorthand pointer
// of XPointer Framework section 3.2, and it is the form the suite uses.
//
// A fragment that selects nothing falls back to the document node rather than
// raising. The error XSLT gives for a fragment is XTRE1160, and that is for
// one that is malformed for the media type, not for one that is well formed
// and matches no element; the specification leaves the latter
// implementation-defined, and returning the document is the reading that
// cannot turn a working stylesheet into a failing one.
func fragmentOf(root *xdm.Node, href string) (*xdm.Node, error) {
	i := strings.IndexByte(href, '#')
	if i < 0 {
		return root, nil
	}
	frag := href[i+1:]
	if frag == "" || !xdm.IsNCName(frag) {
		return root, nil
	}
	if n := xdm.ElementByID(root, frag); n != nil {
		return n, nil
	}
	return root, nil
}

// isDefault reports whether the spec asks for nothing: no type, and the
// default validation mode, which leaves the document exactly as it was read.
//
// A document that has just been parsed carries no annotations, so strip and
// preserve are both no-ops on it as well — but only strip is the default, and
// spelling preserve out is what a stylesheet does when it has arranged for the
// annotations to be there. Treating it as a no-op would be right today and
// wrong the moment a resolver hands back an annotated tree, which the
// conformance harness's preloaded sources already do.
func (s validationSpec) isDefault() bool {
	return s.typeName == nil && s.mode == validateStrip
}

// validSourceDocumentURI rejects an @href that is not a usable URI reference,
// with the FODC0005 that F&O reserves for exactly that: "the resource ... is
// not a valid URI". It is a distinct condition from FODC0002, which is what a
// well-formed URI that could not be retrieved raises -- the difference being
// whether retrieval was ever attempted.
//
// Two forms are caught. A "%" that does not introduce a two-digit escape is
// not a legal percent-encoding, and a backslash is not a legal URI character
// at all: it appears in an href only when a native Windows filename has been
// written where a URI belongs ("c:\my\doc\books.xml"). That case matters
// beyond tidiness, because the leading "c:" otherwise parses as a URI scheme,
// and the failure is then reported as an unsupported scheme -- a retrieval
// error for something that was never a URI. The check is on the string, not on
// the host filesystem, so it behaves identically on Windows, macOS and Linux:
// a Windows path is not a URI anywhere, including on Windows.
func validSourceDocumentURI(href string) error {
	if strings.ContainsRune(href, '\\') {
		return fmt.Errorf(
			"FODC0005: %q is not a valid URI: a backslash is not a legal URI "+
				"character (a filesystem path is not a URI reference)", href)
	}
	for i := 0; i < len(href); i++ {
		if href[i] != '%' {
			continue
		}
		if i+2 >= len(href) || !isHexByte(href[i+1]) || !isHexByte(href[i+2]) {
			return fmt.Errorf("FODC0005: %q is not a valid URI: %% must "+
				"introduce a two-digit escape", href)
		}
		i += 2
	}
	return nil
}
