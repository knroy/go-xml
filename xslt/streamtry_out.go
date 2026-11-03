package xslt

// xsl:try under streamed output (TransformOptions.Output; XSLT 3.0 §8.3).
//
// Tree mode builds a try's body in a builder of its own, appended to the
// output when the body succeeds and dropped when an error is caught. Under
// Output that builder (xdmsink.Try, xdmbuild/tryfwd.go) hands each item it
// completes on to the output as the append would, so a try around a loop no
// longer holds the loop's output as a tree to its end:
//
//   - rollback-output="yes": what is written from the first item handed on
//     is provisional. streamOut's checkpoint (Mark) keeps the serializer's
//     state -- its namespace scopes, the open elements and their indentation,
//     escaping and raw-text state, the undecided-indent record -- and writes
//     into pv, a chunked buffer, from then on; a caught error (or one going
//     past the try) puts it all back, success commits, and the outermost
//     commit writes pv out. So the try holds its output as bytes, at most
//     what its body writes, where it held it as a tree.
//   - rollback-output="no": items are written through when every error the
//     body can raise is caught by the try (a catch for "*"). Then an error
//     either reaches this try, which raises XTDE3530 exactly when the tree
//     does (the body produced an item: the buffer counts them, handed on or
//     not, as tree mode's buffer has them), or cannot be caught at all.
//     With narrower catches an error could reach an outer try that recovers
//     where tree mode never wrote this output, so the output is held, as
//     before.

import (
	"github.com/knroy/go-xml/v2/internal/xdmsink"
	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdmbuild"
)

// tryBuffer is the builder xsl:try i writes its body into, for out, which a
// failure discards (rollback-output="yes"). It is the one place a try
// buffers.
func tryBuffer(rt *runtime, i *tryInstr, out *outputBuilder) *outputBuilder {
	p := xsltPolicy{nodes: rt.nodeBudget()}
	if !i.noRollback || i.catchesAll() {
		if b := xdmsink.Try(out, p, !i.noRollback); b != nil {
			return b.(*outputBuilder)
		}
	}
	return xdmbuild.New(p)
}

// tryEnd ends sub, a try's buffer: commit keeps what it handed on, and
// otherwise that is rolled back.
func tryEnd(rt *runtime, sub *outputBuilder, commit bool) {
	if k := xdmsink.TryEnd(sub, commit); k > 0 {
		xpathleaf.Refund(rt.ctx, k, 0)
	}
}

// catchesAll reports whether a catch of i takes every error.
func (i *tryInstr) catchesAll() bool {
	for _, cl := range i.catches {
		for _, t := range cl.tests {
			if t.anyURI && t.anyLocal {
				return true
			}
		}
	}
	return false
}

// outMark is a checkpoint of a streamOut.
type outMark struct {
	s                     serializer
	frames                []outFrame
	rootAtomic, docAtomic bool
	inDoc                 bool
	rec                   indentRec
	recBuf, pv            bufMark
	recAt                 int
	pvOwed                int64
}

// bufMark is a position in a chunkBuf.
type bufMark struct{ chunks, last, n int }

func (c *chunkBuf) mark() bufMark {
	m := bufMark{chunks: len(c.chunks), n: c.n}
	if m.chunks > 0 {
		m.last = len(c.chunks[m.chunks-1])
	}
	return m
}

// truncate takes c back to m, a mark of c (or of the record c was copied
// from), with nothing written below it since.
func (c *chunkBuf) truncate(m bufMark) {
	clear(c.chunks[m.chunks:])
	c.chunks = c.chunks[:m.chunks]
	if m.chunks > 0 {
		c.chunks[m.chunks-1] = c.chunks[m.chunks-1][:m.last]
	}
	c.n = m.n
}

// dst is where the serializer writes when no element's indentation is
// undecided: the output, or pv under a checkpoint.
func (o *streamOut) dst() stringWriter {
	if o.cps > 0 {
		return &o.pv
	}
	return o.w
}

// Mark makes what is written from now on provisional (xdmsink.Checkpointer).
func (o *streamOut) Mark() any {
	if o.cps == 0 && o.s.w == o.w {
		o.s.w = &o.pv
	}
	o.cps++
	return &outMark{s: *o.s, frames: append([]outFrame(nil), o.frames...),
		rootAtomic: o.rootAtomic, docAtomic: o.docAtomic, inDoc: o.inDoc,
		rec: o.rec, recBuf: o.rec.buf.mark(), recAt: o.recAt, pv: o.pv.mark(), pvOwed: o.pvOwed}
}

// Commit keeps what was written since mark; the outermost writes it out.
func (o *streamOut) Commit(mark any) {
	o.cps--
	if o.cps > 0 {
		return
	}
	s := o.s
	w := s.w
	if w == &o.pv {
		w = o.w
	}
	s.w = o.w
	o.pv.writeRange(s, 0, o.pv.n)
	s.w = w
	o.pv.reset()
	if o.pvOwed > 0 {
		xpathleaf.Refund(o.rt.ctx, o.pvOwed, 0)
	}
	o.pvOwed = 0
}

// Rollback puts the writer back as it was at mark, dropping what was
// written since. The nodes written since stay charged, as tree mode's
// dropped buffer leaves them.
func (o *streamOut) Rollback(mark any) {
	m := mark.(*outMark)
	*o.s = m.s
	o.frames = append(o.frames[:0], m.frames...)
	o.rootAtomic, o.docAtomic, o.inDoc = m.rootAtomic, m.docAtomic, m.inDoc
	o.rec = m.rec
	o.rec.buf.truncate(m.recBuf)
	o.recAt = m.recAt
	o.pv.truncate(m.pv)
	o.pvOwed = m.pvOwed
	o.cps--
	if o.cps == 0 && o.s.w == &o.pv {
		o.s.w = o.w
	}
}
