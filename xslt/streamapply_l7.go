package xslt

import "github.com/knroy/go-xml/v2/xdm"

// mapContent reports whether n is an xsl:map: what an xsl:apply-templates in
// its content returns must be maps (mapInstr.Execute raises XTTE3375 on
// anything else, naming only the item's type). A built-in rule's node --
// the source's own in the tree, a copy of it off the stream (streamRun.keep)
// -- is then that error in both modes, with one message, and is never kept,
// so a sequence-typed variable around the xsl:map does not see the source's
// identity. si-map-008/-009 (as="map(*)" around xsl:map around the apply).
func mapContent(n *xdm.Node) bool { return isXSL(n, "map") }
