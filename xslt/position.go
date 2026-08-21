package xslt

import (
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// registerPositionFuncs adds the source-position accessors.
//
// A Schematron report says *which* element failed, as an XPath, but a person
// fixing the document wants the line to jump to. The path is the precise
// answer and the line is the usable one, so both are offered rather than one
// replacing the other.
//
// They return the empty sequence when the position is unknown — the document
// was parsed without TrackPositions, or the node was built by the transform
// rather than read from the source. Returning 0 would be indistinguishable
// from a real answer in the report.
func registerPositionFuncs(l *xpath.Library) {
	// arg returns the node the accessor should report on: the argument if one
	// was given, otherwise the context node, matching how fn:name and the
	// other node accessors behave.
	arg := func(ctx *xpath.Context, args []xdm.Sequence) *xdm.Node {
		if len(args) > 0 {
			if len(args[0]) == 0 {
				return nil
			}
			n, _ := args[0][0].(*xdm.Node)
			return n
		}
		n, _ := ctx.Item.(*xdm.Node)
		return n
	}

	add := func(local string, pick func(line, col int) int64) {
		for _, arity := range []int{0, 1} {
			l.Add(xpath.Function{
				Name: xdm.QName{URI: xdm.NSGoxslt, Local: local}, Arity: arity,
				Call: func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
					n := arg(ctx, args)
					line, col, ok := n.Position()
					if !ok {
						return xdm.Empty, nil
					}
					return xdm.One(xdm.NewInteger(pick(line, col))), nil
				},
			})
		}
	}

	add("line-number", func(line, _ int) int64 { return int64(line) })
	add("column-number", func(_, col int) int64 { return int64(col) })
}
