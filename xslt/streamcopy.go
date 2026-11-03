package xslt

// xsl:copy with a consuming select (lane g1, bucket CP).
//
// §11.9.1: the select names the one item copied, and the sequence
// constructor is evaluated with it as the singleton focus. That is an
// xsl:for-each over at most one item: the select is planned as a loop's
// (a bare path on the cursor), the body runs at the match's start tag and
// may read its content. The analysis scores it so (copyInstruction,
// §19.8.4.12: the select's sweep folded into the instruction's), and does
// not count the content as a consuming higher-order operand, which the
// Rec's general rules would: the catalog expects si-copy-028 and the other
// si-copy-A cases to run, and Saxon 9.8 passes si-copy 106 of 110
// (report/submission/Saxon_9.8.xml). The catalog's verdict is followed.

import (
	"fmt"

	"github.com/knroy/go-xml/v2/xdm"
)

// copySelectOp plans el, an xsl:copy whose select consumes, as a loop.
func (b *streamBuilder) copySelectOp(el *xdm.Node, f vfocus) bool {
	return b.loopOp(el, f)
}

// executeFed runs i, whose select the stream feeds f: the first item is
// copied, its body read off the cursor; any after it are only counted, for
// the tree's XTTE3180 (which a container re-run reports in the tree's order
// when the body failed first).
func (i *copyInstr) executeFed(rt *runtime, out *outputBuilder, f *burstFeed) error {
	shallow := *i
	shallow.sel = nil
	n := 0
	err := f.read(func(it xdm.Item) error {
		if n++; n > 1 {
			return nil
		}
		return shallow.Execute(rt.withCurrent(it, 1, 1).clearCurrentRule(), out)
	})
	if err == nil && n > 1 {
		err = fmt.Errorf("XTTE3180: xsl:copy/@select selected %d items, and xsl:copy "+
			"copies a single item", n)
	}
	return err
}
