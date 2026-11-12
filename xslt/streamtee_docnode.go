package xslt

// What the document node's stand-in answers in a motionless instruction
// (lane L1).
//
// At the document node the core's focus is a stand-in: an empty document
// node (streamexec.go runStream, streammain.go transformStream). Its names
// are the document's whatever its content (splitViewFns[vNone]). Three more
// things are the document's without its content, and an instruction beside
// the consumer reads them (si-choose-009, si-on-empty-025/026,
// si-on-non-empty-025/026, stream-005, accumulator-087s):
//
//   - base-uri() and document-uri(), of the focus: the stand-in is given the
//     document's at the start of the run (streamStandIn), the URIs the tree's
//     parse gives it (FileResolver.parseOptions: both the file's URI; a
//     validated copy's document URI is empty, as the views' document node
//     has it, streamDocAs). A principal input's stand-in had them already.
//   - ". instance of T" where T is item(), node() or document-node() with no
//     content test: true of any document node.
//   - a self or ancestor-or-self step from the document node, with a test
//     and no predicate: the document node itself or nothing, as the test
//     says of its kind, which the stand-in shares; the path must then go on
//     to a step that makes values of it (its names), not return the stand-in.
//
// Only in an instruction's motionless part (vc.pos): there the focus is the
// run's stand-in. The split's own checks evaluate the outer on an empty
// document of their own (evalAt), which has no URIs.

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// docURIOK reports whether x is base-uri() or document-uri() of the
// document node's stand-in in a motionless instruction.
func docURIOK(vc viewCheck, x *xpath.FuncCall, f vfocus) bool {
	if !vc.pos || f != vNone || x.Name.URI != fnNS || x.Name.Local != "base-uri" && x.Name.Local != "document-uri" {
		return false
	}
	return len(x.Args) == 0 || len(x.Args) == 1 && isContextItem(x.Args[0])
}

// docInstanceOK reports whether x is ". instance of T" at the document node,
// T true of any document node whatever its content.
func docInstanceOK(vc viewCheck, x *xpath.InstanceOfExpr, f vfocus) bool {
	if !vc.pos || f != vNone || !isContextItem(x.Operand) {
		return false
	}
	switch strings.TrimRight(x.Type.String(), "?*+") {
	case "item()", "node()", "document-node()":
		return true
	}
	return false
}

// docSelfStep reports whether st, a step from the document node, returns it
// or nothing by its kind alone: self or ancestor-or-self, no predicate, a
// test that reads nothing of its content.
func docSelfStep(st *xpath.Step) bool {
	if st.Axis != xpath.AxisSelf && st.Axis != xpath.AxisAncestorOrSelf || len(st.Predicates) > 0 {
		return false
	}
	switch t := st.Test.(type) {
	case *xpath.NameTest:
		return true
	case *xpath.KindTest:
		return t.Content == nil
	}
	return false
}

// streamStandIn is the stand-in for the document node of the run of a
// container reading href: an empty document node with the document's URIs.
func streamStandIn(br burstResolver, href, base string, vin *streamValidation) *xdm.Node {
	root := xdm.NewTree().Root
	if u, _ := br.wantsBursts(href, base); u != "" {
		root.SetBaseURI(u)
		if vin == nil || vin.asRead {
			root.SetDocumentURI(u)
		}
	}
	return root
}

// --- Unparsed entities before the consumer of a document-node rule ------------
//
// sf-unparsed-entity-05: rule "/" reads unparsed-entity-uri(x) in the
// instructions before its consumer. The stand-in is given the DOCTYPE as the
// parse meets the document element (watchDoc, streamcopy_l3doc.go); a
// container reads the document through first when nothing consumes, but a
// rule's consumer comes after those instructions. So the rule's run first
// moves the cursor to the document element's start tag (toRoot), which is
// where watchDoc's observer is told of it: the DOCTYPE is parsed by then. On
// the way it passes the comments and processing instructions before the
// document element (XML 1.0 [22]: "Misc*" may come before and after the
// doctypedecl, so the DOCTYPE is not known at the first of them). They are
// read past, so the plan's consumers at the document node may not be ones
// that would read them (earlyDocOK): a select whose matches are elements,
// not the context's copy or string.

// earlyDocOK says why x, a document-node rule's plan that reads the
// document's unparsed entities before its consumer (docEarly), may not read
// past the leaves before the document element ("" when it may).
func (x *streamExec) earlyDocOK() string {
	for _, op := range x.top {
		s := op.split
		if op.apply != applyNone || op.fork || op.group || op.tee != nil || op.pop != nil || s == nil ||
			s.cond != nil || s.ctxItem || len(s.steps) == 0 {
			return "unparsed entities before a consumer that may read the document node's leaves"
		}
		if leaf, _ := testKinds(s.steps[len(s.steps)-1].test); leaf {
			return "unparsed entities before a consumer that may read the document node's leaves"
		}
	}
	return ""
}

// toRoot moves the cursor of run, at the document node, past the comments
// and processing instructions before the document element, to its start
// tag, which is left to be read (peeked).
func (run *streamRun) toRoot() error {
	for {
		ev, err := run.c.Peek()
		if err != nil {
			return err
		}
		if ev.Kind == xdmstream.StartElement || ev.Kind == xdmstream.EndDocument {
			return nil
		}
		if _, err := run.c.Next(); err != nil {
			return err
		}
	}
}

// rootEvents reports whether op, a copy-of at the document node whose split
// gives up at the document element (noRoot), never does: the instruction
// writes into an element (inElement), so the document element is copied as
// events (copiesReadOut, streamcopy_l3.go), with no xml:base rebased on a
// parentless copy and no node at the top; a principal input's plan may then
// keep it (mayGiveUp).
func rootEvents(op *streamOp) bool {
	i, ok := op.instr.(*copyOfInstr)
	s := op.split
	return ok && op.copies && s != nil && s.cond == nil && s.noRoot && !s.hold && !s.nests && s.tail == nil &&
		s.tailText == "" && !s.group && s.fold == nil && i.validation.mode == validateStrip && eventCopier(i, false) &&
		inElement(op.el)
}
