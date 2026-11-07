package streamtrace

import (
	"regexp"
	"strings"
)

// FirstFailure is what a streamed input's validation error says between its
// subject and the failure it reports (xslt/streamvalidin.go): the first one
// the stream met, where the tree's text lists every failure of the document.
const FirstFailure = ", the first failure detected: "

var errCode = regexp.MustCompile(`\b[A-Z]{4}[0-9]{4}\b`)

// SameError reports whether streamed, the error text of a streamed run, is
// the tree's, tree, as the differential tests compare them: the same text,
// or, for the one family judged by code, the same code.
//
// The family is the invalidity of an input document found by validating it
// (xsl:source-document's validation="strict" or type=): XTTE1510 or
// XTTE1540, the streamed text marked with FirstFailure. The tree validates
// the whole document before its body runs and lists every failure; the
// stream stops at the first. XSLT 3.0 §2.14: "If more than one error arises,
// an implementation is not required to signal any errors other than the
// first one that it detects. It is implementation-dependent which of the
// several errors is signaled." Every other error is compared in full.
func SameError(streamed, tree string) bool {
	if streamed == tree {
		return true
	}
	if !strings.Contains(streamed, FirstFailure) {
		return false
	}
	code := errCode.FindString(streamed)
	return (code == "XTTE1510" || code == "XTTE1540") && errCode.FindString(tree) == code
}
