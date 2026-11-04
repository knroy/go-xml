package xslt

// Validation of what a streamed container constructs (lane g1, the result
// side of the V bucket).

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

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
// element's focus -- so on xsl:copy strip and preserve, which assess
// nothing, are admitted, and the others only where copiesNoAttribute.
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
		return name.URI == "" && (name.Local == "validation" && (v == "strip" || v == "preserve") || copiesNoAttribute(el))
	}
	return false
}

// copiesNoAttribute reports whether the item xsl:copy el copies is never an
// attribute, read from how it is written: its select, or (with none or ".")
// the focus a source-document, xsl:for-each or xsl:iterate around it sets,
// is a path ending in a child or descendant step, or the document node.
// Then strict, lax and type validate the new node, as on xsl:element.
func copiesNoAttribute(el *xdm.Node) bool {
	for {
		if a := el.Attr("", "select"); a != nil {
			e := parseAt(el, a.Value())
			if e == nil {
				return false
			}
			if !isContextItem(e) {
				return noAttrPath(e)
			}
		} else if !isXSL(el, "copy") {
			return isXSL(el, "source-document")
		}
		if el = el.Parent(); el == nil || el.Kind() != xdm.KindElement {
			return false
		}
		for !isXSL(el, "for-each") && !isXSL(el, "iterate") && !isXSL(el, "source-document") {
			if isXSL(el, "template") || isXSL(el, "function") || el.Name().URI == xdm.NSXSL && focusSetters[el.Name().Local] {
				return false
			}
			if el = el.Parent(); el == nil || el.Kind() != xdm.KindElement {
				return false
			}
		}
	}
}

// focusSetters are the instructions other than xsl:for-each and xsl:iterate
// that change the focus of their content.
var focusSetters = map[string]bool{"for-each-group": true, "merge": true, "analyze-string": true,
	"sort": true, "perform-sort": true, "copy": true, "merge-source": true, "accumulator-rule": true}

// noAttrPath reports whether e is a path whose last step is a child or
// descendant step, or "/": nodes that are never attributes.
func noAttrPath(e xpath.Expr) bool {
	pe, ok := e.(*xpath.PathExpr)
	if !ok {
		return false
	}
	if len(pe.Steps) == 0 {
		return pe.Root
	}
	st, ok := pe.Steps[len(pe.Steps)-1].(*xpath.Step)
	return ok && (st.Axis == xpath.AxisChild || st.Axis == xpath.AxisDescendant)
}
