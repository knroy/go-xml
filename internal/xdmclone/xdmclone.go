// Package xdmclone lets the schema, XSLT and XQuery packages copy an xdm
// subtree in bulk, record by record, without adding exported API to xdm.
// Package xdm sets the hooks in its init, so they are non-nil wherever xdm is
// linked in. The arguments typed any are *xdm.Node: this package cannot
// import xdm, which imports it.
package xdmclone

// Options says what a Clone copies besides the subtree's records.
type Options struct {
	// Positions keeps the source positions, so a node of the copy reports
	// its original's line and column. It applies to the copy of a document
	// tree's root only; any other copy has none.
	Positions bool
	// Detached makes the copy what xdm.Copy makes: a parentless node in a
	// fragment of its own, without a document URI, the DTD, the XML version
	// or the nodes' type environments, and with no base URI on attributes.
	Detached bool
	// Drop, when set, leaves out every text, comment or processing
	// instruction node for which it reports true.
	Drop func(n any) bool
	// Add, when set, returns attributes to append to an element after its
	// own, each copied with its name, value, base URI, typing and type
	// environment.
	Add func(el any) []any
}

// Clone copies the subtree rooted at top, a *xdm.Node, in bulk: its records
// are copied and renumbered, while the tree's text, names, namespace frames
// and source text are shared with it. The copy is what a top-down copy
// through the append API makes of the same subtree: a new document tree with
// the original's DOCTYPE, XML version and document URI when top is the root
// of a (non-fragment) tree, and otherwise a parentless node in a fragment of
// its own, whose base URI is the one top had.
//
// A subtree still being built is copied as far as it has been built, and the
// copy is complete.
//
// It returns the map from a node of the subtree to its counterpart in the
// copy (nil for a node that was dropped or is not in the subtree), or nil
// when the subtree cannot be cloned this way -- an element would carry too
// many attributes -- and the caller copies it node by node instead.
var Clone func(top any, o Options) func(n any) any

// DropPositions forgets the source positions of the tree n belongs to.
var DropPositions func(n any)
