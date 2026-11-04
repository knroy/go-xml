package xslt

// An xsl:fork whose prongs each consume the document, in the body of a
// streamed xsl:try (lane L1; si-fork-008).
//
// The tree runs the prongs one after another, so the error the try catches
// is the first in prong order, of the first prong that fails, at the place
// the tree's evaluation of that prong meets it; the tee keeps every prong's
// error and returns the first in prong order (streamfork.go). So the fork is
// exact in a try when each prong's consumers are (exactInTry), each
// grounding at once (groundNow), as a try's one consumer must.
//
// What the tree has done before any catch runs is parse the whole document:
// a parse or limit error is the document's, raised before the body, which no
// try catches (streamtry.go caught). A failed prong stops reading, and the
// prongs after it are stopped by the tee; so a prong that fails in a try
// reads on to the end of the document first, and an error met there (the
// document's) is the prong's instead. The prongs before it read to the end
// (each consumer reads its context to its end), so when the try catches,
// the document has been read through: caught has nothing left to skip on
// the cursor the tee took over. A fused fork (streamfuse.go) reads on the
// cursor itself, which caught skips as for any consumer.

import (
	"io"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
)

// prongsExactInTry is exactInTry for op, a fork: why one of its prongs'
// consumers could raise another error than the tree's ("" when none can).
func (op *streamOp) prongsExactInTry() string {
	for _, ops := range op.prongs {
		for _, in := range ops {
			if why := in.exactInTry(); why != "" {
				return why
			}
		}
	}
	return ""
}

// forkInTry readies op, a fork in a try's body: its prongs' consumers
// ground at once, and a failed prong reads on to the document's end.
func (op *streamOp) forkInTry() {
	op.inTry = true
	for _, ops := range op.prongs {
		for _, in := range ops {
			in.groundNow()
		}
	}
}

// readOn reads c, a prong's cursor, to the document's end once its prong has
// failed with err in a try, and returns the prong's error: the document's,
// when reading on meets one.
func readOn(c *xdmstream.Cursor, err error) error {
	if c.At().Kind == xdmstream.EndDocument {
		return err
	}
	for {
		ev, rerr := c.NextTag(&drainAll)
		if rerr == io.EOF {
			return err // the parse had ended
		}
		if rerr != nil {
			return rerr
		}
		if ev.Kind == xdmstream.EndDocument {
			return err
		}
	}
}
