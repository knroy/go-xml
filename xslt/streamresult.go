package xslt

// Streamed xsl:result-document (full streaming, gap lane G6; XSLT 3.0
// §19.8.4.35, §24.1).
//
// The instruction is a wrapper, as xsl:document is: its href and
// serialization attributes are attribute value templates that must be
// motionless on the view, and its body is a sequence constructor whose one
// consumer reads the cursor. It runs its own Execute (resultdoc.go), which
// builds the secondary result in a builder of its own, so the result, its
// URI and its errors (XTDE1480, XTDE1490, XTDE1500, the output settings')
// are the tree's.
//
// Two things the stream adds:
//
//   - A failed run re-runs the container on the tree, which would produce
//     every secondary result the failed run made a second time (XTDE1490):
//     secondaryMark takes them back first.
//   - At the top of the body the tree keeps a node xsl:sequence returns as it
//     is (SecondaryResult.Nodes holds it); a node of the stream there (a
//     view, a grounded copy) is not that node, so the container runs on the
//     tree instead (errKeptStreamNode), as a container's own result does.
//     A principal input has no tree: linkMain refuses such a body.

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// resultDocAttrs checks the attributes of el, an xsl:result-document, at
// focus f: every one but the standard attributes is an attribute value
// template (href, format, the serialization parameters) or holds none
// (use-character-maps, validation, type).
func (b *streamBuilder) resultDocAttrs(el *xdm.Node, f vfocus) bool {
	for a := range el.Attrs() {
		if n := a.Name(); n.URI != "" || standardAttributes[n.Local] {
			continue
		}
		if !b.avt(el, a.Value(), f) {
			return false
		}
	}
	return true
}

// resultDocOp plans el, an xsl:result-document that consumes: its body, or
// one part of one of its attribute value templates (as avtOps does for a
// literal result element), evaluated as the tree evaluates it: the href,
// then the serialization attributes, before the body, which is then
// motionless.
func (b *streamBuilder) resultDocOp(el *xdm.Node, f vfocus) bool {
	var op *streamOp
	for a := range el.Attrs() {
		if n := a.Name(); n.URI != "" || standardAttributes[n.Local] || !strings.ContainsAny(a.Value(), "{}") {
			continue
		}
		exprs, ok := avtExpressions(a.Value())
		if !ok {
			return b.refuse("attribute value template " + a.Value())
		}
		for _, src := range exprs {
			if e := parseAt(el, src); e != nil && (viewCheck{pos: true, noIdent: b.noIdent, key: b.key, acc: !b.noIdent && !b.grp, after: b.after}).safe(e, f, false) {
				continue
			}
			if op != nil {
				return b.refuse("two consuming attribute value templates")
			}
			op = &streamOp{x: b.x, el: el, doc: f == vNone, inGroup: b.grp, key: b.key, avt: src}
		}
	}
	b.kinds[streamtrace.KindElement]++
	if op == nil {
		return b.seq(el, f)
	}
	*b.ops = append(*b.ops, op)
	b.x.exprOps = append(b.x.exprOps, op)
	b.kinds[streamtrace.KindSelect]++
	return b.content(el, f)
}

// secondaryMark returns what takes back the secondary results made after
// it: their entries, and the URIs they recorded for XTDE1490 and XTDE1500
// (each was new when recorded, or the instruction would have raised
// XTDE1490).
func (rt *runtime) secondaryMark() func() {
	n := len(*rt.secondary)
	return func() {
		for _, s := range (*rt.secondary)[n:] {
			delete(*rt.resultHrefs, s.Href)
			if rt.writtenDocs != nil && s.BaseURI != "" {
				delDoc(*rt.writtenDocs, s.BaseURI)
			}
		}
		*rt.secondary = (*rt.secondary)[:n]
	}
}

// keptStreamNode is resultDocumentInstr's check of its body's sequence: in
// a streamed run, a node of the stream at its top sends the container to
// the tree.
func keptStreamNode(rt *runtime, seq xdm.Sequence) error {
	if rt.bursts == nil || !keepsStreamNode(seq) {
		return nil
	}
	if rt.bursts.run.i == nil {
		return errMainKept // a principal input: no tree to run on
	}
	return errKeptStreamNode
}
