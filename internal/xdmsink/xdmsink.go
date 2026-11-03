// Package xdmsink lets XSLT stream the principal result it builds with
// xdmbuild to a serializer as the result is made (TransformOptions.Output),
// without adding exported API to xdmbuild. Package xdmbuild sets the hooks in
// its init. The arguments typed any are *xdmbuild.Builder.
//
// A builder that streams calls its Writer before each new child of the node it
// builds and before each new item of its sequence. At that point every node
// before the new one is complete, so the builder writes them (Child, Item) and
// takes them back out of its tree; the elements on the way to the node being
// built are written as far as their start tags (Open), and finished (Close)
// when they close. The tree then holds the elements still open and the node
// being built, not the result.
//
// A builder made any other way (a variable's, an xsl:try body's, a sort's) has
// no Writer: what it builds is held until it is appended to a streaming
// builder, where it arrives complete. An xsl:try's buffer (Try) is the one
// exception: it hands its complete items to the builder it writes for as
// they are made, under a checkpoint that a caught error rolls back.
package xdmsink

import (
	"sync/atomic"

	"github.com/knroy/go-xml/v2/xdm"
)

// Opened counts the elements and document nodes streaming builders have
// opened (written the start of before their end), for the differentials.
var Opened atomic.Int64

// Forwarded counts the items xsl:try buffers (Try) have handed on before
// their try ended, for the differentials.
var Forwarded atomic.Int64

// Writer is the serializer side of a streamed principal result.
type Writer interface {
	// Begin reports whether the serializer can start writing, given seq,
	// the result so far: ok when it can, never when it never will (the
	// output parameters need the whole result), neither when the result
	// does not yet decide (an output method chosen by its first element).
	// Until Begin is ok nothing is written, and End's seq is the result.
	Begin(seq xdm.Sequence) (ok, never bool)
	// Open writes the start of n, an element whose content is being built
	// at depth (0 at the top of the result) or a document node at the top
	// (depth -1), and reports whether it did. kind and text are those of
	// the child about to be appended (text for a text node only). false
	// holds n: it is written whole once it closes.
	Open(n *xdm.Node, depth int, kind xdm.NodeKind, text string) bool
	// Child writes c, a complete child of the innermost open node, whose
	// children are at depth.
	Child(c *xdm.Node, depth int)
	// Close writes the end of n, opened at depth, all of whose children
	// have been written.
	Close(n *xdm.Node, depth int)
	// Item writes it, a complete item at the top of the principal result,
	// or (doc) of the sequence a document node at its top is built from.
	// own is false for a node the builder did not make (a node of another
	// tree appended as it is).
	Item(it xdm.Item, doc, own bool)
	// End finishes the output: seq is the whole result when Begin was
	// never ok, and nil otherwise. It returns the serialization error.
	End(seq xdm.Sequence) error
}

// Checkpointer is a Writer whose output can be made provisional: what it
// writes after Mark is held until the matching Commit, and Rollback puts
// it, and its own state, back as they were at Mark. Marks nest; each is
// committed or rolled back, innermost first. A Writer that is not one
// keeps an xsl:try's output held to the try's end.
type Checkpointer interface {
	Mark() any
	Commit(mark any)
	Rollback(mark any)
}

// Hooks, set by package xdmbuild.
var (
	// Attach makes b, a builder of the principal result with nothing in it
	// yet, stream to w.
	Attach func(b any, w Writer)
	// Hold keeps b's element from being opened: it is written whole when it
	// closes. For an element its instruction changes after its content
	// (inherit-namespaces="no", validation).
	Hold func(b any)
	// Document returns a builder for the content of a document node that
	// will be appended to out, streaming as part of out when out is a
	// streaming builder at the top of the principal result, and otherwise
	// nil. inPlace builds the content as the document node's children
	// (xdmbuild.InPlaceDocument, whose root it returns too); otherwise it is
	// a sequence that Builder.ToDocument turns into the document.
	Document func(out any, inPlace bool, p any) (b any, root *xdm.Node)
	// Finish writes what out, a streaming builder, has not written, seq
	// being the result of the transform (out's own sequence, or an initial
	// function's raw result), and returns the serialization error.
	Finish func(out any, seq xdm.Sequence) error
	// Stamp returns a count that changes whenever out, a streaming builder,
	// writes or is appended to; 0 for any other builder.
	Stamp func(out any) int64
	// Items returns how many items out's sequence has had, those written
	// included.
	Items func(out any) int
	// Try returns the builder an xsl:try writes its body into when out
	// streams, or nil: a sequence builder (policy p) that hands each item
	// it completes on to out, as appending its sequence to out at the end
	// would, while out's output since is provisional (rollback, true for
	// rollback-output="yes") or written through (false: given only when
	// every error the body raises is caught by the try, so none can reach
	// an outer one). What it has not handed on is its Sequence, which the
	// try appends to out when it succeeds.
	Try func(out any, p any, rollback bool) any
	// TryEnd ends b, a Try buffer: commit (the body succeeded, or stopped
	// with a signal whose output stands) or roll back what it handed on.
	// It returns the result nodes rolling back left charged that tree mode
	// would not have charged, for the caller to hand back.
	TryEnd func(b any, commit bool) int64
)
