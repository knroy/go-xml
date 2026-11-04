package xslt

// A consuming part of an instruction's attribute value template (lane K2):
// xsl:value-of and xsl:attribute separator="{head(//AUTHOR)}", xsl:element
// and xsl:attribute name="{...}" or namespace="{...}".

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
)

// avtNamed are the attributes of each instruction that are value templates
// the instruction's own Execute evaluates (avt.eval, through
// evalStreamExpr), and that compile with noteStreamAVT.
var avtNamed = map[string][]string{
	"value-of":  {"separator"},
	"attribute": {"name", "namespace", "separator"},
	"element":   {"name", "namespace"},
}

// instrAVTOp plans el when one part of one of its value templates is its
// consumer, as avtOps does for a literal result element: that part is an
// expression op, split as the instruction compiles, evaluated where the
// tree's Execute evaluates it; everything else of el -- its other
// attributes, its select, its content -- motionless. done says el is such
// an instruction.
func (b *streamBuilder) instrAVTOp(el *xdm.Node, f vfocus) (done, ok bool) {
	names := avtNamed[el.Name().Local]
	if el.Name().URI != xdm.NSXSL || names == nil {
		return false, false
	}
	var op *streamOp
	var opAttr string
	for _, n := range names {
		v := el.AttrValue(n)
		if !strings.ContainsAny(v, "{}") {
			continue
		}
		exprs, ok := avtExpressions(v)
		if !ok {
			return false, false
		}
		for _, src := range exprs {
			if e := parseAt(el, src); e != nil && (viewCheck{pos: true, noIdent: b.noIdent, up: b.up, key: b.key, acc: !b.noIdent && !b.grp, after: b.after}).safe(e, f, false) {
				continue
			}
			if op != nil {
				return true, b.refuse("two consuming attribute value templates")
			}
			op = &streamOp{x: b.x, el: el, doc: f == vNone, inGroup: b.grp, key: b.key, avt: src}
			opAttr = n
		}
	}
	if op == nil {
		return false, false
	}
	// The other attributes, as attrs checks them; the consumer's own value
	// template has its other parts motionless (checked above).
	for a := range el.Attrs() {
		name := a.Name()
		if name.URI != "" || name.Local == opAttr {
			continue
		}
		if !streamAttrsOK[name.Local] && !resultValidation(el, name) && !b.useSets(el, a, f) {
			return true, b.refuse("attribute " + name.Local + " of " + elName(el))
		}
		switch name.Local {
		case "select":
			if !b.expr(el, a.Value(), f, false) {
				return true, false
			}
		case "as":
		default:
			if !b.avt(el, a.Value(), f) {
				return true, false
			}
		}
	}
	*b.ops = append(*b.ops, op)
	b.x.exprOps = append(b.x.exprOps, op)
	b.kinds[streamtrace.KindSelect]++
	b.kinds[streamtrace.KindElement]++
	n := len(*b.ops)
	if !b.seq(el, f) {
		return true, false
	}
	if len(*b.ops) > n {
		return true, b.refuse("two consuming instructions in one sequence constructor")
	}
	return true, true
}
