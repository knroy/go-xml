// Package xdmlimit carries a resource-limit refusal that package xdm can only
// raise as a panic, from under builders with no error return (a tree's text
// store running out of blocks), to the entry points of xdm, xpath, xquery,
// xslt and xsd, which return it as their error, without adding exported API
// to xdm. encoding/json's internal error panic is the same pattern.
package xdmlimit

// refusal is the value Refuse panics with. It is an error, so that a panic
// no entry point recovered still prints, and matches, the refusal.
type refusal struct{ error }

// Refuse panics with err, for Recover to return.
func Refuse(err error) { panic(refusal{err}) }

// Recover, deferred, sets *err to the error of a Refuse panic. Any other
// panic goes on unchanged.
func Recover(err *error) {
	if p := recover(); p != nil {
		r, ok := p.(refusal)
		if !ok {
			panic(p)
		}
		*err = r.error
	}
}
