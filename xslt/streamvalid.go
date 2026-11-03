package xslt

// Validation of what a streamed container constructs (lane g1, the result
// side of the V bucket).

import "github.com/knroy/go-xml/v2/xdm"

// resultValidation reports whether name, an attribute of el, is
// the validation or type of a constructor that validates what it built:
// xsl:copy-of, xsl:element, xsl:document, a literal result element
// (xsl:validation, xsl:type), and xsl:copy of an element or document.
//
// xsl:copy of an attribute assesses the source node itself, and the
// validator's typed copy (xsd typedCopy) copies the whole tree the node is
// in: for a node of the stream that is a view, whose document node has no
// content to give (posture guard: si-copy-116/-117, xsl:copy
// select="/*/*/@version" type="xs:decimal"). Whether the item is an
// attribute is not known here -- a loop over attributes plans its body at an
// element's focus -- so on xsl:copy only strip and preserve, which assess
// nothing, are admitted.
func resultValidation(el *xdm.Node, name xdm.QName) bool {
	if name.Local != "validation" && name.Local != "type" {
		return false
	}
	if el.Name().URI != xdm.NSXSL {
		return name.URI == xdm.NSXSL
	}
	switch el.Name().Local {
	case "copy-of", "element", "document":
		return name.URI == ""
	case "copy":
		v := el.AttrValue(name.Local)
		return name.URI == "" && name.Local == "validation" && (v == "strip" || v == "preserve")
	}
	return false
}
