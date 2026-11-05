package xslt

// Lane MM8: a built-in copy of a leaf at the top of a streamed principal
// result.
//
// The tree's built-in shallow-copy (copy, deep-copy) of a text, comment or
// processing instruction at the top of the principal result appends the
// source's own node (builtInShallowCopy): Result.Nodes holds it, with the
// source document's parent, siblings and root. A mode whose rules may do
// that (linkMain) streams with the document built as it is read, into a tree
// of its own (mainBuild, an Observer of the cursor, which is told of every
// node): the tree's records, no more. Such a leaf is appended as its node in
// that tree (keep), which is complete when the transform ends, the document
// read to its end, as the tree's source is when Result.Nodes is read.
//
// Nothing reads the built tree during the run: it is reached only through
// the principal result, which the transform writes and never reads. Where
// the built tree would not be the tree's source, the run does not stream
// (mainBuildStreams): the document stripped of whitespace (xsl:strip-space),
// or validated as it is read, whose nodes the observer sees before either.
// A leaf kept by a prong of xsl:fork is read off a cursor of its own, which
// the observer does not see: linkMain refuses that (forkTop).

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
)

// noteMainBuild records that mode m builds its document (mainBuilds).
func (s *Stylesheet) noteMainBuild(m string) string {
	if s.mainBuilds == nil {
		s.mainBuilds = map[string]bool{}
	}
	s.mainBuilds[m] = true
	return ""
}

// mainBuildStreams reports whether a run in mode m may stream under opts:
// one that builds its document does only where the built tree is the
// tree's source.
func (s *Stylesheet) mainBuildStreams(m string, opts TransformOptions) bool {
	return !s.mainBuilds[m] || opts.SourceValidation == 0
}

// inFork reports whether el is inside an xsl:fork.
func inFork(el *xdm.Node) bool {
	for n := el.Parent(); n != nil && n.Kind() == xdm.KindElement; n = n.Parent() {
		if n.Name().URI == xdm.NSXSL && n.Name().Local == "fork" {
			return true
		}
	}
	return false
}

// mainBuild is the document of a principal input, built as the cursor reads
// it. open are the elements being built, the document node first; src is
// the parse's node of the last leaf the cursor read, and leaf its node here.
type mainBuild struct {
	open      []*xdm.Node
	src, leaf *xdm.Node
	// kept maps the copies keep appended at the top of a sequence to
	// their built nodes (note).
	kept     map[*xdm.Node]*xdm.Node
	stripped bool
	// written says the principal result is serialized to
	// TransformOptions.Output, and Result.Nodes is nil: nothing reads a
	// kept leaf's identity, so its copy serves and nothing is built.
	written bool
}

// buildMain makes the tree of m's document, a document node with root's
// base and document URIs, built off m's cursor from its first event. With
// stripped (xsl:strip-space) it is the tree's stripped copy
// (stripWhitespaceForFrom), whose tree keeps the DOCTYPE only; otherwise the
// parse's, with its external subset and XML version (note).
func buildMain(m *mainInput, stripped bool) *mainBuild {
	t := xdm.NewTree()
	t.Root.SetBaseURI(m.root.BaseURI())
	t.Root.SetDocumentURI(m.root.DocumentURI())
	b := &mainBuild{open: []*xdm.Node{t.Root}, stripped: stripped}
	m.c.Observe(func(n *xdm.Node, depth int, end bool, _ uint64) {
		switch {
		case n == nil:
			t.Finalize()
		case n.Kind() == xdm.KindElement && end:
			b.open = b.open[:len(b.open)-1]
		case n.Kind() == xdm.KindElement:
			if depth == 1 {
				// The DOCTYPE, read before the document element: the
				// unparsed entities the tree's root answers for.
				top := n
				for top.Parent() != nil {
					top = top.Parent()
				}
				if stripped {
					t.DocType = top.Tree().DocType
				} else {
					t.CopyDTDFrom(top.Tree())
					t.XMLVersion = top.Tree().XMLVersion // xdm parse.go: set from here
				}
			}
			e := b.open[len(b.open)-1].AppendShallowCopy(n)
			for prefix, uri := range n.DeclaredNamespaces() {
				e.AddNamespace(prefix, uri)
			}
			for a := range n.Attrs() {
				e.AppendAttr(a.Name(), strings.Clone(a.Value())).CopyTypingFrom(a)
			}
			b.open = append(b.open, e)
		default:
			p, v := b.open[len(b.open)-1], strings.Clone(n.Value())
			var l *xdm.Node
			switch n.Kind() {
			case xdm.KindText:
				l = p.AppendText(v)
			case xdm.KindComment:
				l = p.AppendComment(v)
			default:
				l = p.AppendPI(n.Name().Local, v)
			}
			b.src, b.leaf = n, l
		}
	})
	return b
}

// note records cp, the copy keep appends at the top of a sequence of n, a
// leaf of the stream the cursor c is at, as standing for n's built node; it
// reports whether n is that node's (the last leaf the observer built).
func (b *mainBuild) note(c *xdmstream.Cursor, n, cp *xdm.Node) bool {
	if b.written {
		return true
	}
	ev := c.At()
	if ev.Kind < xdmstream.Text || ev.Kind > xdmstream.PI || ev.Node != b.src || b.leaf == nil ||
		b.leaf.Kind() != n.Kind() || b.leaf.Value() != n.Value() || b.leaf.Name() != n.Name() {
		return false
	}
	if b.kept == nil {
		b.kept = map[*xdm.Node]*xdm.Node{}
	}
	b.kept[cp] = b.leaf
	return true
}

// swap puts in seq, the principal result, each built node in its copy's
// place, the document read to its end. Copies in a variable's value or
// elsewhere stay copies, as keep has always made them there.
func (b *mainBuild) swap(seq xdm.Sequence) {
	for i, it := range seq {
		if n, ok := it.(*xdm.Node); ok {
			if l := b.kept[n]; l != nil {
				seq[i] = l
			}
		}
	}
}
