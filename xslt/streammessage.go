package xslt

// Lane g5: xsl:message in streamed bodies (bucket E).
//
// A message has no effect on the result: its text is appended to
// rt.messages, in the order the instructions run, which is the tree's. Two
// things make the stream's messages the tree's:
//   - A container re-run on the tree drops the messages the failed streamed
//     run appended (executeStream, one hook), so none is told twice; the
//     prongs of a tee each keep their own and they are appended in prong
//     order, up to the first that failed (streamRun.fork, one hook).
//   - XSLT 3.0 swallows an error of the message's content (instructions.go).
//     On the stream that is xsl:try with an empty catch-all: an error with a
//     code is swallowed and, when the content read the context, the rest of
//     it is skipped (streamTry.caught); an error with none -- the parse's, a
//     limit's, a plan fault -- is not the content's and is raised, as the
//     tree raises it before the transformation runs.

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// messageOp plans el, an xsl:message at focus f: motionless (its select and
// content read only the view), or consuming (its select is split, or its
// content holds the one consumer).
func (b *streamBuilder) messageOp(el *xdm.Node, f vfocus, consuming bool) bool {
	// The tree raises an accumulator's error at its first read, the stream
	// when it reaches the node (streamaccum.go): a swallowed read differs.
	if subtreeMentions(el, "accumulator-") {
		return b.refuse("xsl:message reading an accumulator")
	}
	for a := range el.Attrs() {
		n := a.Name()
		if n.URI != "" {
			continue
		}
		switch n.Local {
		case "select":
			if !consuming && !b.expr(el, a.Value(), f, false) {
				return false
			}
		case "terminate", "error-code":
			if !b.avt(el, a.Value(), f) {
				return false
			}
		default:
			if !streamAttrsOK[n.Local] {
				return b.refuse("attribute " + n.Local + " of xsl:message")
			}
			if !b.avt(el, a.Value(), f) {
				return false
			}
		}
	}
	if !consuming {
		return b.content(el, f)
	}
	if el.Attr("", "select") == nil {
		b.kinds[streamtrace.KindElement]++
		return b.seq(el, f)
	}
	op := &streamOp{x: b.x, el: el, doc: f == vNone, inGroup: b.grp, key: b.key}
	b.x.ops[el] = op
	*b.ops = append(*b.ops, op)
	b.kinds[streamtrace.KindSelect]++
	return true
}

// messageFailed is told that the content of a message failed with err:
// whether err is raised, or swallowed as the tree swallows it, the rest of
// the context skipped first when the content read it (t, taken before).
func (t streamTry) messageFailed(err error) error {
	if t.run == nil {
		return nil
	}
	if !catchable(err) {
		return err
	}
	return t.caught()
}

// messagesMark is how many messages rt holds; dropMessages drops the ones
// after mark, those of a streamed run the tree runs again.
func (rt *runtime) messagesMark() int { return len(*rt.messages) }

func (rt *runtime) dropMessages(mark int) { *rt.messages = (*rt.messages)[:mark] }

// subtreeMentions reports whether s occurs in an attribute or a text node of el or
// below it.
func subtreeMentions(el *xdm.Node, s string) bool {
	found := false
	walkElements(el, func(e *xdm.Node) bool {
		for a := range e.Attrs() {
			found = found || strings.Contains(a.Value(), s)
		}
		for ch := range e.Children() {
			found = found || ch.Kind() == xdm.KindText && strings.Contains(ch.Value(), s)
		}
		return !found
	})
	return found
}

// burstMessage admits xsl:message in a grounded body (burstElement): it runs
// on the copy as the tree runs it on the node, and reads no accumulator.
func burstMessage(el *xdm.Node) bool {
	return el.Name().Local == "message" && !subtreeMentions(el, "accumulator-")
}

// prongMessages sets aside, per prong of a fork, the messages each turn of
// it appends to rt's, and puts them back in prong order when done: those of
// the prongs up to the first that failed (errs, read then), as the tree's
// run of the prongs one after another would have told them.
type prongMessages struct {
	rt   *runtime
	errs []error
	segs [][]string
	at   int
}

func (rt *runtime) prongMessages(n int, errs []error) *prongMessages {
	return &prongMessages{rt: rt, errs: errs, segs: make([][]string, n)}
}

// turn is told prong k goes on (in) or stops.
func (p *prongMessages) turn(k int, in bool) {
	if in {
		p.at = len(*p.rt.messages)
		return
	}
	p.segs[k] = append(p.segs[k], (*p.rt.messages)[p.at:]...)
	p.rt.dropMessages(p.at)
}

func (p *prongMessages) done() {
	for k, seg := range p.segs {
		*p.rt.messages = append(*p.rt.messages, seg...)
		if p.errs[k] != nil {
			return
		}
	}
}
