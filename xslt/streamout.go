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
//     adaptive, a parameter document, build-tree="no"), nothing is written
//     and End serializes the result as the tree does;
//   - HTML5 streams too: prefix normalization is decided element by element
//     as each start tag is written (serializer.elemPrefix), and its bare
//     doctype, owed until the first element, is written or not by that
//     element's start tag (openTag), the whitespace, comments and
//     instructions before it written as they come;
//   - under indent="yes" an element's children are indented unless one of
//     them is text that is not whitespace (or the element has no element
//     child), which is known only once it closes, and an element that is
//     not indented writes everything below it without indentation. So an
//     element whose indentation is not yet decided is written, with its
//     content, into a buffer (indentRec), every indent the serializer adds
//     there recorded: a text child decides it is not indented, and its
//     indents are taken out; its end decides by the tree's rule. The
//     outermost one, at the top, writes the buffer out once it is decided.
//     The result is then held as bytes, not as a tree, and only up to that
//     decision;
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
	"encoding/binary"
	"io"
	"slices"
	"strings"

	"github.com/knroy/go-xml/v2/internal/htmlser"
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
	// rec holds what the frames whose indentation is undecided wrote, from
	// the outermost of them (frames[recAt]) on; recAt is -1 when none is.
	rec   indentRec
	recAt int
	// pv holds what is written under an xsl:try's checkpoints, cps of them
	// open (streamtry_out.go); pvOwed the result nodes written there, handed
	// back to MaxNodes when it is written out.
	pv     chunkBuf
	cps    int
	pvOwed int64
}

// indentRec is the buffer an undecided element is written into, and the
// indents in it (serializer.indent records them).
type indentRec struct {
	buf chunkBuf
	// spans are the indents written in buf, n of them, each as two uvarints:
	// its start less the end of the one before (end), and its length. dels
	// are ranges [from, to) of them in elements decided not to be indented.
	// (A few bytes an indent: a result can hold one per element.)
	spans  []byte
	n, end int
	dels   [][2]int
	// owed are the result nodes written into buf, handed back to MaxNodes
	// only when buf is written out: until then the result holds them, as
	// it held them as a tree before.
	owed int64
}

// span records the indent just written at buf[start:].
func (r *indentRec) span(start int) {
	r.spans = binary.AppendUvarint(r.spans, uint64(start-r.end))
	r.spans = binary.AppendUvarint(r.spans, uint64(r.buf.n-start))
	r.end = r.buf.n
	r.n++
}

// drop takes out the indents from the from'th on.
func (r *indentRec) drop(from int) {
	if from < r.n {
		r.dels = append(r.dels, [2]int{from, r.n})
	}
}

// outFrame is an open element, or document node (doc), and what closing it
// restores.
type outFrame struct {
	n      *xdm.Node
	doc    bool
	base   int
	st     elemState
	indent bool
	// und says the element's indentation is not decided yet; spans is
	// where its content's indents start in rec, and elem that it has an
	// element child.
	und   bool
	spans int
	elem  bool
	// inline says the child last written is an inline html element
	// (htmlser.SkipIndentBefore reads the previous sibling, which the
	// sink has taken back).
	inline bool
}

// newStreamOut returns the writer of a principal result streamed to w under
// opts and charMap, the stylesheet's own.
func newStreamOut(rt *runtime, w io.Writer, opts OutputSettings, charMap map[rune]string) *streamOut {
	o := &streamOut{rt: rt, opts: opts, charMap: charMap, recAt: -1}
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
	switch method {
	case "json", "adaptive":
		return false, true
	}
	if checkOutputSettings(opts, nil) != nil {
		return false, true // End raises it, as the tree's serialization does
	}
	o.s = newSerializer(o.dst(), opts, o.charMap) // pv when begun under an xsl:try's checkpoint
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
		if s.html && (strings.EqualFold(n.Name().Local, "head") ||
			strings.EqualFold(n.Name().Local, "meta")) {
			return false
		}
	}
	und := !o.text && s.opts.Indent && !s.suppressed(n) && !o.unindented(n, kind, text)
	if depth == 0 {
		if o.inDoc {
			o.docAtomic = false
		} else {
			o.rootAtomic = false
		}
	}
	f := outFrame{n: n, base: len(s.ns), indent: s.opts.Indent, und: und}
	if !o.text {
		p := o.undecided()
		if p != nil {
			p.elem = true
			s.skipIndent = o.skipBefore(p, n)
			p.inline = s.html && htmlser.Inline(n, s.xhtml, s.html5)
		}
		f.st, _ = s.openTag(n, depth, f.base, true)
		s.skipIndent = false
		if und && p == nil {
			o.recAt = len(o.frames)
			s.rec, s.w = &o.rec, &o.rec.buf
		}
		f.spans = o.rec.n
	}
	if !und {
		s.opts.Indent = false
	}
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
	s := o.s
	switch {
	case o.text:
		o.textNode(c)
	case depth == 0:
		s.node(c, 0)
	case o.undecided() != nil:
		p := o.undecided()
		k := c.Kind()
		if k == xdm.KindText && !xdm.IsXMLWhitespace(c.Value()) ||
			s.html && !s.xhtml && (k == xdm.KindComment || k == xdm.KindPI) {
			o.flatten()
			s.nodeNoIndent(c)
			break
		}
		if k == xdm.KindElement {
			p.elem = true
		}
		s.skipIndent = o.skipBefore(p, c)
		s.node(c, depth)
		s.skipIndent = false
		p.inline = s.html && htmlser.Inline(c, s.xhtml, s.html5)
	default:
		s.nodeNoIndent(c)
	}
	o.refund(c)
}

// skipBefore is serializer.skipBeforeChild for c (nil: the end tag), a
// child of f's element, whose previous sibling is gone.
func (o *streamOut) skipBefore(f *outFrame, c *xdm.Node) bool {
	s := o.s
	if !s.html {
		return false
	}
	if c == nil {
		c = f.n
	}
	return f.inline || htmlser.Inline(c, s.xhtml, s.html5)
}

// undecided returns the innermost frame when its indentation is undecided.
func (o *streamOut) undecided() *outFrame {
	if k := len(o.frames) - 1; k >= 0 && o.frames[k].und {
		return &o.frames[k]
	}
	return nil
}

// flatten decides that the innermost frame, undecided, is not indented:
// the indents written in its content are taken out, and the rest of it is
// written without.
func (o *streamOut) flatten() {
	k := len(o.frames) - 1
	f := &o.frames[k]
	o.rec.drop(f.spans)
	f.und = false
	o.s.opts.Indent = false
	if k == o.recAt {
		o.emit()
	}
}

// emit writes the buffer of the outermost undecided frame, now decided, out
// without the indents taken out, and ends the recording.
func (o *streamOut) emit() {
	s := o.s
	s.rec, s.w = nil, o.dst()
	r := &o.rec
	if o.cps > 0 {
		// A checkpoint may hold the start of dels: sort a copy.
		r.dels = slices.Clone(r.dels)
	}
	slices.SortFunc(r.dels, func(a, b [2]int) int { return a[0] - b[0] })
	at, pos, end, d, upTo := 0, 0, 0, 0, 0
	for i := range r.n {
		gap, k := binary.Uvarint(r.spans[pos:])
		ln, k2 := binary.Uvarint(r.spans[pos+k:])
		pos += k + k2
		start := end + int(gap)
		end = start + int(ln)
		for d < len(r.dels) && r.dels[d][0] <= i {
			upTo = max(upTo, r.dels[d][1])
			d++
		}
		if i < upTo {
			r.buf.writeRange(s, at, start)
			at = end
		}
	}
	r.buf.writeRange(s, at, r.buf.n)
	owed := r.owed
	if o.cps > 0 {
		// A checkpoint may hold the record: start a new one.
		o.rec = indentRec{}
	} else {
		r.buf.reset()
		r.spans, r.n, r.end, r.dels = r.spans[:0], 0, 0, r.dels[:0]
		r.owed = 0
	}
	o.recAt = -1
	o.handBack(owed)
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
	if f.und {
		// The tree's rule, writeElement's: indented when it has an element
		// child (no text child came, or it would have been flattened).
		if f.elem && mayIndentType(n) {
			if !o.skipBefore(&f, nil) {
				s.indent(depth)
			}
		} else {
			o.rec.drop(f.spans)
		}
	}
	s.opts.Indent = f.indent
	if !o.text {
		s.closeTag(n, f.st)
		s.ns = s.ns[:f.base]
	}
	o.handBack(int64(1 + n.NumAttrs()))
	if k == o.recAt {
		o.emit()
	}
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

// TextOK implements xdmsink.TextWriter.
func (o *streamOut) TextOK() bool { return o.s != nil && o.text && o.cps == 0 && o.recAt < 0 }

// WriteText implements xdmsink.TextWriter: as textNode and textItem write a
// text node's value under the text method.
func (o *streamOut) WriteText(p []byte, top, doc bool) {
	switch {
	case top:
		o.rootAtomic = false
	case doc:
		o.docAtomic = false
	}
	if len(o.s.charMap) == 0 {
		o.s.writeBytes(p)
	} else {
		o.s.writeString(o.s.mapChars(string(p)))
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
	o.handBack(countCharged(n))
}

// handBack hands k written nodes back to MaxNodes, or, while they are in
// the undecided buffer, owes them.
func (o *streamOut) handBack(k int64) {
	switch {
	case k <= 0:
	case o.recAt >= 0:
		o.rec.owed += k
	case o.cps > 0:
		o.pvOwed += k
	default:
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

// chunkBuf is the undecided buffer: 64 KiB chunks, so that it holds what
// was written once, never a doubled copy as it grows.
type chunkBuf struct {
	chunks [][]byte
	n      int
}

const chunkSize = 64 << 10

func (c *chunkBuf) Len() int { return c.n }

func (c *chunkBuf) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		k := c.last()
		m := min(len(p), chunkSize-len(c.chunks[k]))
		c.chunks[k] = append(c.chunks[k], p[:m]...)
		p = p[m:]
	}
	c.n += n
	return n, nil
}

func (c *chunkBuf) WriteString(str string) (int, error) {
	n := len(str)
	for len(str) > 0 {
		k := c.last()
		m := min(len(str), chunkSize-len(c.chunks[k]))
		c.chunks[k] = append(c.chunks[k], str[:m]...)
		str = str[m:]
	}
	c.n += n
	return n, nil
}

// last returns the index of a chunk with room in it, the last.
func (c *chunkBuf) last() int {
	k := len(c.chunks) - 1
	if k < 0 || len(c.chunks[k]) == chunkSize {
		c.chunks = append(c.chunks, make([]byte, 0, chunkSize))
		k++
	}
	return k
}

// writeRange writes bytes [a, b) to s.
func (c *chunkBuf) writeRange(s *serializer, a, b int) {
	off := 0
	for _, ch := range c.chunks {
		end := off + len(ch)
		if lo, hi := max(a, off), min(b, end); lo < hi {
			s.writeBytes(ch[lo-off : hi-off])
		}
		if end >= b {
			return
		}
		off = end
	}
}

func (c *chunkBuf) reset() {
	clear(c.chunks)
	c.chunks, c.n = c.chunks[:0], 0
}
