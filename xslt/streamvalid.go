package xslt

// Validation of what a streamed container constructs (lane g1, the result
// side of the V bucket).

import "github.com/knroy/go-xml/v2/xdm"

// resultValidation reports whether name, an attribute of el, is the
// validation or type of a constructor: xsl:copy-of, xsl:copy, xsl:element,
// xsl:document, a literal result element (xsl:validation, xsl:type).
func resultValidation(el *xdm.Node, name xdm.QName) bool {
	if name.Local != "validation" && name.Local != "type" {
		return false
	}
	if el.Name().URI != xdm.NSXSL {
		return name.URI == xdm.NSXSL
	}
	switch el.Name().Local {
	case "copy-of", "copy", "element", "document":
		return name.URI == ""
	}
	return false
}
