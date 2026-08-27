package xslt

import "strings"

// sameResource reports whether two base URIs name the same resource for the
// purpose of document(”).
//
// A plain string comparison after trimming an empty fragment is enough: both
// sides come from the same tree, so either the base was never moved -- and the
// strings are identical -- or an xml:base resolved it somewhere else, and any
// difference at all means a different resource. Normalising further would risk
// treating two genuinely different URIs as one, which is the failure this
// guards against rather than the one it would fix.
//
// An empty module base is the case where the stylesheet was compiled without
// one. There is then nothing to disagree with, and returning the module keeps
// document(”) working for a stylesheet built from a string.
func sameResource(a, b string) bool {
	if b == "" {
		return true
	}
	return strings.TrimSuffix(a, "#") == strings.TrimSuffix(b, "#")
}
