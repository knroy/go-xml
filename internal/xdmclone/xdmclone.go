// Package xdmclone lets the schema, XSLT and XQuery packages copy an xdm
// subtree in bulk, record by record, without adding exported API to xdm.
// Package xdm sets the hooks in its init, so they are non-nil wherever xdm is
// linked in. The arguments typed any are *xdm.Node: this package cannot
// import xdm, which imports it.
package xdmclone

// Options says what a Clone copies besides the subtree's records.
type Options struct {
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
	// Layer, when set, is a typing layer NewLayer made over top's subtree:
	// each node the layer has typed is copied with the layer's typing
	// instead of its own.
	Layer any
}

// Clone copies the subtree rooted at top, a *xdm.Node, in bulk: its records
// are copied and renumbered, while the tree's text, names and namespace
// frames are shared with it; source positions are not copied. The copy is
// what a top-down copy through the append API makes of the same subtree: a
// new document tree with
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

// NewLayer returns a typing layer over the subtree rooted at top, a *xdm.Node,
// or nil for a namespace node. A schema assessment writes the typing it gives
// the subtree's nodes to the layer instead of to their tree, which may be
// shared, and Clone with Options.Layer then makes the typed copy in one pass.
//
// The layer's methods are Node's typing setters and readers taking the node
// as their first argument (SetAssessedTyping, SetTypeAnnotationResolved,
// SetTypeEnv, ApplyTyping, TypingOf), plus CopyTyping(dst, src), which gives
// dst the typing and type environment src has in the layer. A node outside
// the subtree is read and written as itself. Package xsd states them as an
// interface.
var NewLayer func(top any) any
