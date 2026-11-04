package xslt

import (
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
)

// performSortOp plans an xsl:perform-sort whose select consumes, when the
// analysis assessed the select grounded: copies or values (copy-of(item),
// tokenize(.) -- si-perform-sort-001, -002). The select is read off the
// cursor as xsl:sequence's (selectOp); the sort keys then run on what it
// returned, items that are not the stream's, by the tree's code
// (performSortInstr.Execute: applySorts). An xsl:sort's other attributes are
// evaluated in the instruction's focus, so only literal ones are taken.
func (b *streamBuilder) performSortOp(el *xdm.Node, f vfocus) bool {
	if !b.groundedSelect(el) { // streamgroup.go
		return b.refuse("consuming select of xsl:perform-sort")
	}
	for _, s := range el.ChildElements() {
		if !isXSL(s, "sort") {
			return b.refuse("xsl:perform-sort content beside xsl:sort")
		}
		for a := range s.Attrs() {
			if a.Name().Local != "select" && strings.ContainsAny(a.Value(), "{}") {
				return b.refuse("xsl:sort attribute value template")
			}
		}
		if s.FirstChild() != nil {
			return b.refuse("xsl:sort with a sequence constructor")
		}
	}
	return b.selectOp(el, f)
}
