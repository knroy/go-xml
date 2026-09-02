package xpath

import "github.com/knroy/go-xml/xdm"

// FreeVariables reports every variable this expression reads without binding.
//
// It is the variable half of StaticCalls, and exists for the same reason:
// variables resolve at evaluation time here, so XPST0008 — a static error — is
// only raised for a reference something actually evaluates. A host that has to
// report it at compile time collects the names from here and checks them
// against what it knows is in scope.
//
// A name is free when no enclosing "for", "let", "some", "every" or inline
// function binds it. That is what makes the answer usable as an error rather
// than as a hint: "let $b := $a return $b" reports $a and not $b, where a
// lexical scan of the same text cannot tell the two apart.
//
// The order is the order of first occurrence, so that a caller reporting one
// name reports the leftmost.
func (c *Compiled) FreeVariables() []xdm.QName {
	if c == nil || c.expr == nil {
		return nil
	}
	var out []xdm.QName
	seen := map[string]bool{}
	walkFreeVars(c.expr, nil, &out, seen)
	return out
}

// bound is the set of names in scope at a point in the walk.
//
// It is a slice rather than a map because it is small — the binding depth of
// an expression, not the size of the expression — and because a slice is
// copied for a nested scope by appending, which a map would need cloning for.
type bound []xdm.QName

func (b bound) has(n xdm.QName) bool {
	for _, x := range b {
		if x.URI == n.URI && x.Local == n.Local {
			return true
		}
	}
	return false
}

// walkFreeVars appends the free variables of e to out.
//
// The scoping rules of the binding forms are the point of the function, so
// each is written out rather than folded together. "for" and "let" bind each
// clause's variable for the clauses that follow it and for the return: "let $a
// := 1, $b := $a return $b" is legal, and $a in the second binding is not
// free. A quantified expression binds the same way for its test. An inline
// function binds its parameters for its body only.
func walkFreeVars(e Expr, in bound, out *[]xdm.QName, seen map[string]bool) {
	switch v := e.(type) {
	case nil:
		return
	case *VarRef:
		if in.has(v.Name) {
			return
		}
		key := v.Name.Clark()
		if seen[key] {
			return
		}
		seen[key] = true
		*out = append(*out, v.Name)
	case *BinaryOp:
		walkFreeVars(v.Left, in, out, seen)
		walkFreeVars(v.Right, in, out, seen)
	case *UnaryOp:
		walkFreeVars(v.Operand, in, out, seen)
	case *SequenceExpr:
		for _, x := range v.Items {
			walkFreeVars(x, in, out, seen)
		}
	case *IfExpr:
		walkFreeVars(v.Cond, in, out, seen)
		walkFreeVars(v.Then, in, out, seen)
		walkFreeVars(v.Else, in, out, seen)
	case *FilterExpr:
		walkFreeVars(v.Base, in, out, seen)
		for _, p := range v.Predicates {
			walkFreeVars(p, in, out, seen)
		}
	case *PathExpr:
		for _, s := range v.Steps {
			walkFreeVars(s, in, out, seen)
		}
	case *Step:
		for _, p := range v.Predicates {
			walkFreeVars(p, in, out, seen)
		}
	case *ForExpr:
		inner := in
		for _, b := range v.Bindings {
			walkFreeVars(b.Seq, inner, out, seen)
			inner = append(inner[:len(inner):len(inner)], b.Var)
		}
		walkFreeVars(v.Return, inner, out, seen)
	case *LetExpr:
		inner := in
		for _, b := range v.Bindings {
			walkFreeVars(b.Seq, inner, out, seen)
			inner = append(inner[:len(inner):len(inner)], b.Var)
		}
		walkFreeVars(v.Return, inner, out, seen)
	case *QuantifiedExpr:
		inner := in
		for _, b := range v.Bindings {
			walkFreeVars(b.Seq, inner, out, seen)
			inner = append(inner[:len(inner):len(inner)], b.Var)
		}
		walkFreeVars(v.Test, inner, out, seen)
	case *InlineFunctionExpr:
		inner := in
		for _, p := range v.Params {
			inner = append(inner[:len(inner):len(inner)], p.Name)
		}
		walkFreeVars(v.Body, inner, out, seen)
	case *InstanceOfExpr:
		walkFreeVars(v.Operand, in, out, seen)
	case *CastExpr:
		walkFreeVars(v.Operand, in, out, seen)
	case *TreatExpr:
		walkFreeVars(v.Operand, in, out, seen)
	case *FuncCall:
		for _, a := range v.Args {
			walkFreeVars(a, in, out, seen)
		}
	case *DynamicCall:
		walkFreeVars(v.Target, in, out, seen)
		for _, a := range v.Args {
			walkFreeVars(a, in, out, seen)
		}
	case *MapConstructor:
		for _, k := range v.Keys {
			walkFreeVars(k, in, out, seen)
		}
		for _, x := range v.Values {
			walkFreeVars(x, in, out, seen)
		}
	case *ArrayConstructor:
		for _, m := range v.Members {
			walkFreeVars(m, in, out, seen)
		}
	case *LookupExpr:
		walkFreeVars(v.Base, in, out, seen)
		walkFreeVars(v.Expr, in, out, seen)
	case *SimpleMap:
		walkFreeVars(v.Left, in, out, seen)
		walkFreeVars(v.Right, in, out, seen)
	case *StringConcat:
		walkFreeVars(v.Left, in, out, seen)
		walkFreeVars(v.Right, in, out, seen)
	}
}
