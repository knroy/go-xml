// Package genid carries the fn:generate-id spelling of a node from package
// xdm, which owns the node's identity, to package xpath, which exports the
// function, without adding exported API to xdm. It cannot name *xdm.Node,
// because xdm imports it.
package genid

// Of returns the generate-id string of n, which must be a non-nil *xdm.Node.
// Package xdm sets it in its init, so it is non-nil wherever xdm is linked in.
var Of func(n any) string
