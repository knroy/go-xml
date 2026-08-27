package xslt

import (
	"fmt"

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
}

func (c *compiler) compileSourceDocument(n *xdm.Node) (Instruction, error) {
	hrefSrc := n.AttrValue("href")
	if hrefSrc == "" {
		return nil, fmt.Errorf(
			"XTSE0010: xsl:source-document requires an href attribute")
	}
	href, err := compileAVT(hrefSrc, newNSResolver(n, ""))
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
		streamed: isYes(n.AttrValue("streamable"))}, nil
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
	docs := rt.ctx.Docs
	if docs == nil {
		return nil, fmt.Errorf(
			"FODC0002: document access is disabled (no resolver configured): %q",
			href)
	}
	base := rt.ctx.StaticBaseURI
	if base == "" {
		if n, ok := rt.ctx.Item.(*xdm.Node); ok {
			base = n.BaseURI
		}
	}
	tree, err := docs.ResolveDocument(href, base)
	if err != nil {
		return nil, fmt.Errorf("FODC0002: cannot retrieve %q: %w", href, err)
	}
	if i.validation.isDefault() {
		return tree.Root, nil
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
	return copied.Root, nil
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
