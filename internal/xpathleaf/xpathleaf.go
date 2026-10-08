// Package xpathleaf lets a host package inside this module mark a function it
// registers with package xpath as a leaf, without adding exported API to
// xpath. A leaf never calls back into user code, never keeps the context past
// its return and never writes to it, so a call to one counts recursion depth
// on the caller's context in place instead of copying it (see xpath's
// Function.leaf).
package xpathleaf

// Mark marks fn, which must be a *xpath.Function, as a leaf. Package xpath
// sets it in its init, so it is non-nil wherever xpath is linked in.
var Mark func(fn any)
