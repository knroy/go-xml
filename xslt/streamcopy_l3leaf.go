package xslt

// xsl:sequence and xsl:variable in a rule for a text, comment or PI node
// (lane L3: si-value-of-100, su-absorbing-016). The rule runs on a copy of
// the leaf, which stands in for the node: an instruction that could hand
// the copy on as the node is refused (motionless, noIdent). One whose
// select can return only atomic values -- string(), concat(...), a
// stylesheet function declared to return atomic values -- hands on no node,
// so the copy is never taken for the original.

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
)

// leafAtomic reports whether el, an xsl:sequence or xsl:variable, returns
// only atomic values: a select atomicValued says so, and no content.
func leafAtomic(el *xdm.Node) bool {
	src := el.AttrValue("select")
	if src == "" {
		return false
	}
	for ch := range el.Children() {
		switch ch.Kind() {
		case xdm.KindElement:
			return false
		case xdm.KindText:
			if strings.TrimSpace(ch.Value()) != "" {
				return false
			}
		}
	}
	e := parseAt(el, src)
	return e != nil && atomicValued(e)
}
