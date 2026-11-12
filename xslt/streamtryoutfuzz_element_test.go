package xslt

// fuzzTryElement, a share of the time, makes the body of a try of the
// xsl:try grammar (streamtryoutfuzz_test.go) one element around the rest, so
// that the try's one top-level item is an element built in place
// (xdmbuild/tryfwd.go start): with attributes before its content, or one
// after it (XTDE0410, which a catch may take); or an html element, which
// decides the output method when the try is the result's first output
// (a rollback leaves it to the catch); or attributes first, which go on to
// the element the try writes to while it is empty (a rollback takes them
// off).

import "math/rand"

func fuzzTryElement(r *rand.Rand, inner string) string {
	switch r.Intn(8) {
	case 0:
		return `<tw>` + inner + `</tw>`
	case 1:
		return `<tw a="1"><xsl:attribute name="b" select="'2'"/>` + inner + `<tz/></tw>`
	case 2:
		return `<tw><tz/>` + inner + `<xsl:attribute name="late" select="'x'"/></tw>`
	case 3:
		return `<html><br/>` + inner + `<p>p<br/></p></html>`
	case 4:
		return `<xsl:attribute name="fa" select="'1'"/><xsl:attribute name="at" select="'2'"/>` + inner
	}
	return inner
}
