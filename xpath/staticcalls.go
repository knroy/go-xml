package xpath

import "github.com/knroy/go-xml/xdm"

// StaticCall is one function call or named function reference written in an
// expression, reported by Compiled.StaticCalls.
type StaticCall struct {
	Name  xdm.QName
	Arity int
	// Ref marks a "name#arity" reference rather than a call. The two differ
	// in what a missing function means to the host language, so the caller is
	// told which it found.
	Ref bool
}

// StaticCalls reports every statically named function this expression calls or
// references, at every depth.
//
// Function resolution in this engine happens at evaluation time, which is what
// lets a stylesheet's own xsl:function declarations be visible without a
// separate binding pass. The cost is that XPST0017 -- a *static* error -- is
// only raised for a call that is actually evaluated, and a great deal of a
// stylesheet never is: an unreferenced variable, a template never matched. A
// host that has to report the error at compile time collects the names from
// here and resolves them itself once every declaration is in.
//
// Nothing is filtered: names in every namespace are reported, including ones
// the host will want to excuse. It cannot be decided here, because which
// functions exist is a property of the host's library and not of the grammar.
func (c *Compiled) StaticCalls() []StaticCall {
	if c == nil || c.expr == nil {
		return nil
	}
	var out []StaticCall
	walkCalls(c.expr, &out)
	return out
}

// walkCalls appends the calls in e, and in every expression beneath it, to out.
//
// A type annotation is descended into as well: an inline function's declared
// parameter and return types carry no expressions, but the *body* of one does,
// and the body hangs off the same node.
func walkCalls(e Expr, out *[]StaticCall) {
	switch v := e.(type) {
	case nil:
		return
	case *FuncCall:
		*out = append(*out, StaticCall{Name: v.Name, Arity: len(v.Args)})
		for _, a := range v.Args {
			walkCalls(a, out)
		}
	case *NamedFunctionRef:
		*out = append(*out, StaticCall{Name: v.Name, Arity: v.Arity, Ref: true})
	case *BinaryOp:
		walkCalls(v.Left, out)
		walkCalls(v.Right, out)
	case *UnaryOp:
		walkCalls(v.Operand, out)
	case *SequenceExpr:
		for _, x := range v.Items {
			walkCalls(x, out)
		}
	case *IfExpr:
		walkCalls(v.Cond, out)
		walkCalls(v.Then, out)
		walkCalls(v.Else, out)
	case *FilterExpr:
		walkCalls(v.Base, out)
		for _, p := range v.Predicates {
			walkCalls(p, out)
		}
	case *PathExpr:
		for _, s := range v.Steps {
			walkCalls(s, out)
		}
	case *Step:
		for _, p := range v.Predicates {
			walkCalls(p, out)
		}
	case *ForExpr:
		for _, b := range v.Bindings {
			walkCalls(b.Seq, out)
		}
		walkCalls(v.Return, out)
	case *LetExpr:
		for _, b := range v.Bindings {
			walkCalls(b.Seq, out)
		}
		walkCalls(v.Return, out)
	case *QuantifiedExpr:
		for _, b := range v.Bindings {
			walkCalls(b.Seq, out)
		}
		walkCalls(v.Test, out)
	case *InstanceOfExpr:
		walkCalls(v.Operand, out)
	case *CastExpr:
		walkCalls(v.Operand, out)
	case *TreatExpr:
		walkCalls(v.Operand, out)
	case *InlineFunctionExpr:
		walkCalls(v.Body, out)
	case *DynamicCall:
		walkCalls(v.Target, out)
		for _, a := range v.Args {
			walkCalls(a, out)
		}
	case *MapConstructor:
		for _, k := range v.Keys {
			walkCalls(k, out)
		}
		for _, x := range v.Values {
			walkCalls(x, out)
		}
	case *ArrayConstructor:
		for _, m := range v.Members {
			walkCalls(m, out)
		}
	case *LookupExpr:
		walkCalls(v.Base, out)
		walkCalls(v.Expr, out)
	case *SimpleMap:
		walkCalls(v.Left, out)
		walkCalls(v.Right, out)
	case *StringConcat:
		walkCalls(v.Left, out)
		walkCalls(v.Right, out)
	}
}
