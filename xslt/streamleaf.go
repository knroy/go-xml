package xslt

import "github.com/knroy/go-xml/v2/xdm"

// Kept leaves and climbing (full-streaming gap lane G4).
//
// A text, comment or processing-instruction match of a split's P that is
// kept as a node (keepLeaves) is a copy the cursor makes (Cursor.Leaf): the
// original to order, identity and generate-id, complete, under the copies a
// view has of its ancestors. A loop's body runs on it off the cursor, as on
// a grounded copy.

// offEach hands it, a match read whole, to each with no instruction
// intercepted.
func (run *streamRun) offEach(each func(xdm.Item) error, it xdm.Item) error {
	return run.offCursor(func() error { return each(it) })
}

// leafApplyRefusal says why an apply-templates select, split as s, cannot
// stream: its matches are dispatched as elements at their start tags.
func leafApplyRefusal(s *streamSplit) string {
	if s.keepLeaves {
		return "apply-templates select: text, comment or PI matches"
	}
	return ""
}
