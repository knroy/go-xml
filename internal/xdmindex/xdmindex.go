// Package xdmindex lets the XPath evaluator answer descendant::name from a
// per-tree element-name index without adding exported API to xdm. Package xdm
// sets the hook in its init, so it is non-nil wherever xdm is linked in. The
// arguments typed any are *xdm.Node: this package cannot import xdm, which
// imports it.
package xdmindex

// Named calls add, in document order, with each element descendant of n (a
// *xdm.Node) whose namespace URI is uri and local name is local, and reports
// true. It reports false, having called nothing, when n's tree has no index:
// only a parsed document, which is complete and never appended to, has one.
// The index is built on the first call for a tree.
var Named func(n any, uri, local string, add func(any)) bool
