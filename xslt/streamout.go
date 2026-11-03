package xslt

// Streamed principal output (full streaming, component C12; DESIGN section
// 1.7, "Streamed output"): TransformOptions.Output.
//
// The principal result's builder streams (xdmbuild/sink.go): it hands this
// writer each node as it completes, and the start and end of each element it
// can open. The writer is the serializer, split at the element: openTag and
// closeTag around children written one at a time, with the state the whole-
// tree serializer keeps on its stack kept on a stack here. The bytes are the
// tree's, because each call does what the tree's walk does at that point,
// and a choice the walk makes by looking ahead is not made early:
//
//   - the output method chosen from the result waits for its first element
//     (methodOf); until then, and for good when a parameter needs the whole
//     result (a separator, normalization, a doctype or standalone, json,
//     adaptive, HTML5's prefix normalization, a parameter document,
//     build-tree="no"), nothing is written and End serializes the result as
//     the tree does;
//   - under indent="yes" an element's children are indented unless one of
//     them is text that is not whitespace, which is known only once it
//     closes: an element at the top is opened only once such a child is in
//     it (the element is held, written whole, otherwise), and everything
//     below an opened element is written without indentation, as the tree
//     writes it;
//   - an html head, whose added meta is indented by its children, is held,
//     and so is a meta that head replaces.
//
// The items at the top of the result go through sequence normalisation on
// the way, a run of atomic values joined by single spaces; a document node
// built from a sequence (ToDocument) joins and merges as ToTree does.
//
// What is written is no longer held, so its nodes are handed back to the
// result-node count (MaxNodes): elements and their attributes, and text
// nodes, each as the builder charged it. The cap then bounds what the
// transform holds, not what it wrote. A buffered result keeps the
// cumulative count.

import (
	"bufio"
	"bytes"
	"io"
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmsink"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xdmbuild"
)

// traceOutOpen, when set, is told of each element or document node a
// streamed result opens (writes the start of before its end), for the tests.
var traceOutOpen func()

// streamOut writes a streamed principal result (xdmsink.Writer).
type streamOut struct {
	rt      *runtime
	w       stringWriter
	bw      *bufio.Writer // w, when it is a buffer of ours
	opts    OutputSettings
	charMap map[rune]string
	never   bool
	s       *serializer
	text    bool
	frames  []outFrame
	// The last item written at the top of the result, or of the sequence
	// document being written, was an atomic value.
	rootAtomic, docAtomic, inDoc bool
}

// outFrame is an open element, or document node (doc), and what closing it
// restores.
type outFrame struct {
	n      *xdm.Node
	doc    bool
	base   int
	st     elemState
	indent bool
}

// newStreamOut returns the writer of a principal result streamed to w under
// opts and charMap, the stylesheet's own.
func newStreamOut(rt *runtime, w io.Writer, opts OutputSettings, charMap map[rune]string) *streamOut {
	o := &streamOut{rt: rt, opts: opts, charMap: charMap}
	switch sw := w.(type) {
	case *bytes.Buffer:
		o.w = sw
	case *strings.Builder:
		o.w = sw
	case *bufio.Writer:
		o.w = sw
	default:
		o.bw = writerPool.Get().(*bufio.Writer)
		o.bw.Reset(w)
		o.w = o.bw
	}
	m := strings.ToLower(opts.Method)
	o.never = opts.ParameterDocument != "" || !opts.buildsTree() || opts.ItemSeparator != nil ||
		opts.NormalizationForm != "" && opts.NormalizationForm != "none" ||
		opts.Standalone != "" || opts.DocTypeSystem != "" || opts.DocTypePublic != "" ||
		m == "json" || m == "adaptive"
	return o
}

// flush hands the buffered bytes on, and the buffer back. Safe to call more
// than once.
func (o *streamOut) flush() error {
	if o.bw == nil {
		return nil
	}
	err := o.bw.Flush()
	o.bw.Reset(nil)
	writerPool.Put(o.bw)
	o.bw = nil
	return err
}

func (o *streamOut) Begin(seq xdm.Sequence) (ok, never bool) {
	if o.never {
		return false, true
	}
	opts := o.opts
	if opts.Method == "" {
		m, decided := methodOf(seq, opts.Version10Implicit)
		if !decided {
			return false, false
		}
		opts.Method = m
	}
	method := strings.ToLower(opts.Method)
	if opts.indentByMethod && (method == "html" || method == "xhtml") {
		opts.Indent = true
	}
	if method == "html" || method == "xhtml" {
		v := opts.HTMLVersion
		if v == "" && method == "html" {
			v = opts.Version
		}
		if strings.HasPrefix(v, "5") {
			return false, true // prefix normalization reads the whole tree
		}
	}
	switch method {
	case "json", "adaptive":
		return false, true
	}
	if checkOutputSettings(opts, nil) != nil {
		return false, true // End raises it, as the tree's serialization does
	}
	o.s = newSerializer(o.w, opts, o.charMap)
	if method == "text" {
		o.text = true
		if opts.ByteOrderMark {
			o.s.writeString("\uFEFF")
		}
	} else {
		o.s.beginMarkup(nil)
	}
	return true, false
}

func (o *streamOut) Open(n *xdm.Node, depth int, kind xdm.NodeKind, text string) bool {
	if n == nil {
		o.inDoc, o.docAtomic, o.rootAtomic = true, false, false
		return true
	}
	if n.Kind() == xdm.KindDocument {
		o.rootAtomic = false
		o.frames = append(o.frames, outFrame{n: n, doc: true})
		return true
	}
	s := o.s
	if !o.text {
		if s.pendingDoctype || s.html && (strings.EqualFold(n.Name().Local, "head") ||
			strings.EqualFold(n.Name().Local, "meta")) {
			return false
		}
		if s.opts.Indent && !s.suppressed(n) && !o.unindented(n, kind, text) {
			return false
		}
	}
	if depth == 0 {
		if o.inDoc {
			o.docAtomic = false
		} else {
			o.rootAtomic = false
		}
	}
	f := outFrame{n: n, base: len(s.ns), indent: s.opts.Indent}
	if !o.text {
		f.st, _ = s.openTag(n, depth, f.base, true)
	}
	s.opts.Indent = false
	o.frames = append(o.frames, f)
	if traceOutOpen != nil {
		traceOutOpen()
	}
	return true
}

// unindented reports whether n, an element at the top whose child of kind
// (holding text) is about to be appended, has a child that keeps its content
// from being indented: text that is not whitespace, or under the html method
// a comment or processing instruction. Its last child is the only one the
// question has not been put for already.
func (o *streamOut) unindented(n *xdm.Node, kind xdm.NodeKind, text string) bool {
	html := o.s.html && !o.s.xhtml
	if kind == xdm.KindText && !xdm.IsXMLWhitespace(text) ||
		html && (kind == xdm.KindComment || kind == xdm.KindPI) {
		return true
	}
	c := n.LastChild()
	return c != nil && (c.Kind() == xdm.KindText && !xdm.IsXMLWhitespace(c.Value()) ||
		html && (c.Kind() == xdm.KindComment || c.Kind() == xdm.KindPI))
}

func (o *streamOut) Child(c *xdm.Node, depth int) {
	switch {
	case o.text:
		o.textNode(c)
	case depth == 0:
		o.s.node(c, 0)
	default:
		o.s.nodeNoIndent(c)
	}
	o.refund(c)
}

// textNode writes c under the text method: its text.
func (o *streamOut) textNode(c *xdm.Node) {
	switch c.Kind() {
	case xdm.KindText:
		o.s.writeString(o.s.mapChars(c.Value()))
	case xdm.KindElement, xdm.KindDocument:
		for d := range c.Descendants() {
			if d.Kind() == xdm.KindText {
				o.s.writeString(o.s.mapChars(d.Value()))
			}
		}
	}
}

func (o *streamOut) Close(n *xdm.Node, depth int) {
	if n == nil {
		o.inDoc = false
		return
	}
	k := len(o.frames) - 1
	f := o.frames[k]
	o.frames = o.frames[:k]
	if f.doc {
		return
	}
	s := o.s
	s.opts.Indent = f.indent
	if !o.text {
		s.closeTag(n, f.st)
		s.ns = s.ns[:f.base]
	}
	xpathleaf.Refund(o.rt.ctx, int64(1+n.NumAttrs()), 0)
}

func (o *streamOut) Item(it xdm.Item, doc, own bool) {
	s := o.s
	switch v := it.(type) {
	case *xdm.Atomic:
		str := v.String()
		prev := &o.rootAtomic
		if doc {
			prev = &o.docAtomic
		}
		if *prev {
			str = " " + str
		}
		*prev = true
		if o.text {
			s.writeString(s.mapChars(str))
		} else {
			s.escapeText(str)
		}
	case *xdm.Node:
		if doc {
			if v.Kind() == xdm.KindText && v.Value() == "" {
				return // ToTree drops it, and the run of values goes on
			}
			o.docAtomic = false
		} else {
			o.rootAtomic = false
		}
		switch {
		case o.text:
			s.textItem(v)
		case doc:
			s.node(v, 0)
		default:
			s.topItem(v)
		}
		if own {
			o.refund(v)
		}
	case *xdm.ArrayItem:
		if doc {
			return // ToTree has no rendering for it
		}
		for _, m := range xdm.Flatten(xdm.Sequence{v}) {
			o.Item(m, false, false)
		}
	default:
		if doc {
			return
		}
		if err := checkOutputSettings(s.opts, xdm.Sequence{it}); err != nil {
			s.fail(err)
		}
	}
}

func (o *streamOut) End(seq xdm.Sequence) error {
	var err error
	if o.s == nil {
		err = serializeTo(o.w, seq, o.opts, o.charMap)
	} else {
		err = o.s.err
	}
	if ferr := o.flush(); err == nil {
		err = ferr
	}
	return err
}

// refund hands back the nodes of n, written, as the builder charged them.
func (o *streamOut) refund(n *xdm.Node) {
	if k := countCharged(n); k > 0 {
		xpathleaf.Refund(o.rt.ctx, k, 0)
	}
}

// countCharged counts n's subtree as the builder charges what it constructs:
// an element and each of its attributes, and a text node. A copy was charged
// its namespace declarations too, and comments and instructions nothing, so
// this never hands back more than was charged.
func countCharged(n *xdm.Node) int64 {
	switch n.Kind() {
	case xdm.KindText:
		return 1
	case xdm.KindElement, xdm.KindDocument:
		k := int64(n.NumAttrs())
		if n.Kind() == xdm.KindElement {
			k++
		}
		for c := range n.Children() {
			k += countCharged(c)
		}
		return k
	}
	return 0
}

// inPlaceDocument is xdmbuild.InPlaceDocument for a document node about to
// be appended to out, which streams it when out is the streamed principal
// result's top.
func inPlaceDocument(rt *runtime, out *outputBuilder) (*outputBuilder, *xdm.Node) {
	p := xsltPolicy{nodes: rt.nodeBudget()}
	if b, root := xdmsink.Document(out, true, p); b != nil {
		return b.(*outputBuilder), root
	}
	return xdmbuild.InPlaceDocument(p)
}

// documentBuilder is newOutputBuilder for the content of a document node,
// made by ToDocument, about to be appended to out: streamed when out is the
// streamed principal result's top.
func documentBuilder(rt *runtime, out *outputBuilder) *outputBuilder {
	if b, _ := xdmsink.Document(out, false, xsltPolicy{nodes: rt.nodeBudget()}); b != nil {
		return b.(*outputBuilder)
	}
	return newOutputBuilder(rt)
}

// holdOpen keeps the element sub builds from being written before it ends,
// when its instruction changes it after its content: inherit-namespaces="no"
// undeclares on its children, and validation replaces it with its typed copy.
func holdOpen(sub *outputBuilder, noInherit bool, v validationSpec) {
	if noInherit || !v.isDefault() {
		xdmsink.Hold(sub)
	}
}
