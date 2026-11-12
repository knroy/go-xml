package xslt

// Lane MM8: xsl:source-document with an ID fragment (docbook-003,
// href="NEWS.xml#V1.79.1_Tools").
//
// The tree's context is the first element, in document order, with an ID
// attribute (or xml:id) of that value, else the document node (fragmentOf).
// Streamed, the cursor is read to that element, which is grounded (a copy
// with its ancestors as a view has them: names, attributes, namespaces,
// base URI), the rest of the document read for its errors, and the body run
// on the copy as the tree runs it on the element. The element's subtree is
// what is held, never more than the tree's records.
//
// The body must not tell the copy from the element: copyFreeBody (no axis
// out of the copy but down and to attributes, no root, identity, document,
// key, accumulator, template or stylesheet function; a node returned only
// as a copy). When no element has the ID the context is the document node,
// known only at the end and needing all of it: the run ends with
// errFragmentAbsent, having written nothing, and the container runs on the
// tree, which reads the document again (a file: the same document).
// A validated document (whose IDs the validation types) and stripped
// annotations stay on the tree.

import (
	"errors"
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
)

// errFragmentAbsent ends a fragment run that met no element with the ID.
var errFragmentAbsent = errors.New("source-document: no element has the fragment's ID; the context is the document node")

// fragmentStreams reports whether the run of i on href streams its fragment,
// and returns the ID: an NCName after the one '#'.
func (i *sourceDocumentInstr) fragmentStreams(rt *runtime, href string) (string, bool) {
	k := strings.IndexByte(href, '#')
	if k < 0 || !i.fragOK || rt.sheet.stripTypeAnnotations || !i.validation.isDefault() {
		return "", false
	}
	frag := href[k+1:]
	return frag, frag != "" && xdm.IsNCName(frag)
}

// fragment reads run's cursor to the element with ID frag and runs i's body
// on its grounded copy, in rt, into out; then reads the rest.
func (run *streamRun) fragment(rt *runtime, i *sourceDocumentInstr, out *outputBuilder, frag string) error {
	all := xdmstream.Filter{}
	for {
		ev, err := run.c.NextTag(&all)
		if err != nil {
			return err
		}
		switch {
		case ev.Kind == xdmstream.EndDocument:
			return errFragmentAbsent
		case ev.Kind != xdmstream.StartElement || !hasID(ev.Node, frag):
			continue
		}
		g, err := run.c.Ground()
		if err != nil {
			return err
		}
		run.used = true
		if err := run.drain(); err != nil {
			return err // the tree parses the whole document first
		}
		sub := rt.withCurrent(g, 1, 1).clearCurrentRule()
		sub.bursts = nil // the body runs as the tree's, on the copy
		return execSequence(i.body, sub, out)
	}
}

// hasID reports whether el has an ID attribute, or xml:id, of value id, as
// xdm.ElementByID tests it.
func hasID(el *xdm.Node, id string) bool {
	for a := range el.Attrs() {
		if a.Value() == id && (a.IsID() || a.Name().URI == xdm.NSXML && a.Name().Local == "id") {
			return true
		}
	}
	return false
}

// fragmentBody reports whether nothing in the body of el, an
// xsl:source-document, can tell a copy of its context from the node.
func fragmentBody(el *xdm.Node, tabs *moduleTabs) bool {
	for ch := range el.Children() {
		if !copyFreeBody(ch, tabs) {
			return false
		}
	}
	return true
}
