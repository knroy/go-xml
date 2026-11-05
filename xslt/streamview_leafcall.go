package xslt

// A stylesheet function called on a leaf's copy (lane MM4, su-absorbing-016):
// <xsl:template match="TITLE/text()"><xsl:sequence select="f:f(.)"/>, f's
// body string-length($input). The rule runs on a copy of the leaf; a call
// not marked as reading only its arguments' values (sheetCall) was refused,
// though its body, inlined exactly (inlineCall, streamcall.go: every
// conversion of §10.3.3 one that cannot fail and changes nothing), is the
// call: when that body reads of the leaf only what the copy has, so does
// the call. One level: a call in the inlined body is checked as a call.

import "github.com/knroy/go-xml/v2/xpath"

// leafInlinedSafe reports whether x, a call on a stylesheet function with
// the focus a leaf, is safe as its inlined body is.
func (vc viewCheck) leafInlinedSafe(x *xpath.FuncCall, f vfocus) bool {
	if f != vLeaf || vc.inlined || vc.globals == nil {
		return false
	}
	body := inlineCall(x, collectStreamFuncs(documentElementOf(vc.globals)))
	if body == nil {
		return false
	}
	vc.inlined = true
	return vc.safe(body, f, false)
}
