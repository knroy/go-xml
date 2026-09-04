package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// outputURIVar carries the current output URI down the XPath context.
//
// It cannot be read from the runtime the way key() and current() are. The
// runtime struct is copied by value on every focus change, and the copy
// stored in runtimeVar is the one made when the transform started, so a
// `temporary` flag set on a derived copy is invisible to a function that
// recovers the runtime from the context. Binding the value as an ordinary
// XPath variable makes it follow the same scoping the expression itself
// does, which is exactly the scoping section 19.1 describes: an inline
// function or a named function reference captures whatever was current where
// it was written.
//
// An absent binding and a bound empty sequence mean the same thing —
// temporary output state — so nothing has to bind it for the common case.
var outputURIVar = xdm.QName{URI: internalNS, Local: "output-uri"}

// Section 24.3 clears the current output URI across a dynamic function call,
// which is a property of the call rather than of any XSLT construct — so the
// name is registered with the evaluator that makes the call.
func init() { xpath.ClearedOnDynamicCall = append(xpath.ClearedOnDynamicCall, outputURIVar) }

// withOutputURI returns a runtime whose expressions see uri as the current
// output URI. An empty uri puts the runtime in temporary output state as far
// as fn:current-output-uri is concerned.
func (rt *runtime) withOutputURI(uri string) *runtime {
	n := *rt
	if uri == "" {
		n.ctx = rt.ctx.WithVar(outputURIVar, xdm.Empty())
	} else {
		n.ctx = rt.ctx.WithVar(outputURIVar, xdm.One(xdm.NewAnyURI(uri)))
	}
	return &n
}

// registerOutputFuncs adds fn:current-output-uri and the two-argument forms
// of the unparsed-entity functions.
//
// These are XSLT-defined functions in the standard function namespace, so
// which of them exist follows the processor rather than the module: a
// version="2.0" stylesheet run by a 3.0 processor must find them, which is
// what result-document-1006 and the whole fn/current-output-uri set require.
func registerOutputFuncs(l *xpath.Library) {
	// fn:current-output-uri returns the absolute URI of the destination the
	// final result tree being written goes to, or the empty sequence in
	// temporary output state — which covers a global variable, a stylesheet
	// function's body, a sort key and a pattern.
	l.Add(xpath.Function{
		Name: xdm.QName{URI: xdm.NSFN, Local: "current-output-uri"}, Arity: 0,
		Since: xpath.XPath31,
		Call: func(ctx *xpath.Context, _ []xdm.Sequence) (xdm.Sequence, error) {
			seq, ok := ctx.LookupVar(outputURIVar)
			if !ok {
				return xdm.Empty(), nil
			}
			return seq, nil
		},
	})

	// The two-argument forms name the document to look in explicitly, which
	// is what lets a stylesheet function — whose context item is its own
	// argument, not a node of the source — ask the question at all.
	for _, fn := range []struct {
		name   string
		public bool
	}{
		{"unparsed-entity-uri", false},
		{"unparsed-entity-public-id", true},
	} {
		public, fname := fn.public, fn.name
		l.Add(xpath.Function{
			Name: xdm.QName{URI: xdm.NSFN, Local: fn.name}, Arity: 2,
			Since: xpath.XPath31,
			Call: func(_ *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
				name, err := stringArg(args[0], "fn:"+fname)
				if err != nil {
					return nil, err
				}
				if len(args[1]) != 1 {
					return nil, fmt.Errorf(
						"XPTY0004: the second argument of %s() must be a single node",
						fname)
				}
				n, ok := args[1][0].(*xdm.Node)
				if !ok {
					return nil, fmt.Errorf(
						"XPTY0004: the second argument of %s() must be a node",
						fname)
				}
				// FODC0005 rather than XTDE1370: the node was supplied, so
				// there is no question of a missing focus — the argument
				// simply does not identify a document.
				if n.Root().Kind != xdm.KindDocument {
					return nil, fmt.Errorf(
						"FODC0005: the root of the tree containing the second "+
							"argument of %s() is not a document node", fname)
				}
				sys, pub, _, found := n.Tree().UnparsedEntity(name)
				if !found {
					return xdm.One(xdm.NewAnyURI("")), nil
				}
				if public {
					return xdm.One(xdm.NewString(pub)), nil
				}
				return xdm.One(xdm.NewAnyURI(resolveAgainst(n.Root().BaseURI, sys))), nil
			},
		})
	}
}
