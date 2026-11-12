package xslt

// xsl:copy of the document node (lane L3).
//
// The copy of a document node takes the document's DOCTYPE (its unparsed
// entities), base URI and document URI, which xsl:copy reads of its source
// once its body has run (copyInstr). The stand-in for the streamed
// document node got them only from the first view a dispatch met
// (streamapply.go, element), so only a copy whose body was one
// apply-templates of the children could be planned. Here the stand-in is
// given them as the parse meets the document element (watchDoc), whatever
// reads it; and a copy whose body read nothing has the rest of the document
// read through before it takes them (streamDocRead): nothing else at the
// document node consumes.

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// --- The document node's properties ---------------------------------------------

// watchDoc gives the stand-in for the document node the document's DOCTYPE
// (and so its unparsed entities), base URI and document URI as the parse
// reads them, at the document element's start tag, before any consumer is
// told of it: xsl:copy of the document node reads them of the stand-in. It
// must be called before the cursor has read anything.
func (run *streamRun) watchDoc() error {
	if run.c.At().Kind != 0 {
		return errStreamPlan
	}
	done := false
	root := run.root
	run.c.Observe(func(n *xdm.Node, d int, end bool, _ uint64) {
		if done || end || d != 1 || n == nil || n.Kind() != xdm.KindElement {
			return
		}
		done = true
		t := n.Tree()
		if t == nil {
			return
		}
		root.Tree().CopyDTDFrom(t)
		if t.Root == nil {
			return
		}
		if root.BaseURI() == "" {
			root.SetBaseURI(t.Root.BaseURI())
		}
		if root.DocumentURI() == "" {
			root.SetDocumentURI(t.Root.DocumentURI())
		}
	})
	return nil
}

// readyDocCopy checks x's xsl:copy of the document node (copyDoc, and a
// motionless one): a motionless copy reads the rest of the document when it
// ends (streamDocRead), so nothing else at the document node may consume;
// and the observer that gives the properties to the stand-in leaves no fresh
// cursor for a fork's tee.
func (x *streamExec) readyDocCopy() string {
	if !x.docProps {
		return ""
	}
	if x.docEarly {
		if why := x.earlyDocOK(); why != "" { // streamtee_docnode.go
			return why
		}
	}
	if x.docMotionless && len(x.top) > 0 {
		return "a motionless xsl:copy of the document node beside a consuming instruction"
	}
	for _, op := range x.top {
		if op.fork {
			return "xsl:fork beside xsl:copy of the document node"
		}
	}
	return ""
}

// streamDocRead is called by xsl:copy of node, a document node, once its
// body has run and before it reads the document's properties: when node is
// the stand-in of the streamed run's document, and nothing has read the
// document yet (the body read nothing of it), the rest is read through, so
// that watchDoc has given the stand-in what the parse found. Nothing else
// at the document node consumes (readyDocCopy, and seq's one consumer).
func (rt *runtime) streamDocRead(node *xdm.Node) error {
	f := rt.bursts
	if f == nil || f.run == nil || f.run.off || node != f.run.root || !f.run.x.docProps {
		return nil
	}
	run := f.run
	if run.used || run.d != 0 {
		return nil
	}
	if err := run.drain(); err != nil {
		return err
	}
	run.used = true
	return nil
}

// isDot reports whether a select is "." itself.
func isDot(el *xdm.Node) bool {
	return strings.TrimSpace(el.AttrValue("select")) == "."
}

// docEntities reports whether e, refused as motionless at the focus f, is
// motionless once unparsed-entity-uri(x) and unparsed-entity-public-id(x)
// of the document node (§19.8.9: fn:unparsed-entity-uri(x, /)) are
// admitted: they read the DOCTYPE, which the stand-in is given as the parse
// meets the document element (watchDoc). So the expression must be
// evaluated after the document has been read: after a consumer in its
// sequence constructor that is an op itself, run whatever happens
// (b.docRead; one in an xsl:if may not run), or in a container nothing of which
// consumes at the document node (read through first: readyDocCopy, as a
// motionless copy of the document node).
func (b *streamBuilder) docEntities(e xpath.Expr, vc viewCheck, f vfocus, nodeOK bool) bool {
	if e == nil || f != vNone || b.noIdent {
		return false
	}
	vc.ents = true
	if !vc.safe(e, f, nodeOK) {
		return false
	}
	if !b.docRead {
		if b.x.rule {
			// A rule's body is not read through first: its run moves to the
			// document element first (streamtee_docnode.go).
			b.x.docEarly = true
		} else {
			b.x.docMotionless = true
		}
	}
	b.x.docProps = true
	return true
}
