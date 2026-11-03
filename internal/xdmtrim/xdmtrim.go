// Package xdmtrim lets a builder that takes written subtrees back out of a
// tree (xdmbuild's streamed output) release their text, without adding
// exported API to xdm. Package xdm sets the hooks in its init, so they are
// non-nil wherever xdm is linked in. The arguments typed any are *xdm.Node, standing for its tree (a fragment
// included, which Node.Tree does not name):
// this package cannot import xdm, which imports it.
package xdmtrim

var (
	// TextBlocks returns how many blocks n's tree's text store holds (at most 32
	// KiB each).
	TextBlocks func(n any) int
	// CompactText stores the values of the nodes of n's tree afresh and drops the
	// store's old blocks, so that the text of nodes taken back
	// (RemoveLastChild) is released. A string already handed out keeps
	// reading its old block, which only the garbage collector frees.
	CompactText func(n any)
)
