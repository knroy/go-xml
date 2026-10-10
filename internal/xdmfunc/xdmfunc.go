// Package xdmfunc lets the packages of this module mark the function items
// they make, without adding exported API to xdm. An item no package here made
// was built by a host, and calling it runs host code (see xpath's callItem).
// Package xdm sets the hooks in its init, so they are non-nil wherever xdm is
// linked in. The arguments typed any are *xdm.FunctionItem: this package
// cannot import xdm, which imports it.
package xdmfunc

var (
	// Mark marks fn as made by this module.
	Mark func(fn any)
	// Native reports whether fn was marked.
	Native func(fn any) bool
)
