package xslt

// Lane g5: the folds of a streamed split that cannot fail (bucket TRY).
//
// Under xsl:try the stream's first error must be the tree's (readyTries). The
// tree evaluates a reducer's argument, every item's predicates and tail,
// before it folds; the stream folds each item as it comes. The two meet the
// same first error when the fold cannot fail on an item whose predicates and
// tail have succeeded: sum, avg, min and max fail only on an item that is not
// numeric (nor a duration), so they cannot when every item is certainly
// numeric, or is an untyped value a predicate has already converted to a
// number -- "." compared with a number by a general comparison casts it to
// xs:double; a numeric constructor of "." casts it to a type whose lexical
// space is within xs:double's -- so its cast to xs:double, the fold's, succeeds.

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// foldCannotFail reports whether s's fold cannot fail on an item its tail
// and predicates gave.
func (s *streamSplit) foldCannotFail() bool {
	if !splitFolds[s.red] {
		return false
	}
	if s.tail == nil {
		// The matches themselves, atomized: untyped (the stream is never
		// validated), checked by a predicate of P's last step.
		last := s.steps[len(s.steps)-1]
		for _, p := range last.preds {
			if p.e != nil && numericCheck(p.e.Expr()) {
				return true
			}
		}
		return false
	}
	e := s.tail.Expr()
	if pe, ok := e.(*xpath.PathExpr); ok && len(pe.Steps) > 0 {
		e = pe.Steps[len(pe.Steps)-1]
	}
	if numericValued(e) {
		return true
	}
	var preds []xpath.Expr
	switch x := e.(type) {
	case *xpath.FilterExpr:
		if !untypedValued(x.Base) {
			return false
		}
		preds = x.Predicates
	case *xpath.Step:
		preds = x.Predicates
	}
	for _, p := range preds {
		if numericCheck(p) {
			return true
		}
	}
	return false
}

// numericValued reports whether e certainly gives numbers: arithmetic,
// fn:number, the numeric functions, numeric constructors and casts, a
// filter of one, the right of a simple map ending in one, or a choice of two.
func numericValued(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.BinaryOp:
		switch x.Op {
		case "+", "-", "*", "div", "idiv", "mod":
			// A date minus a date is a duration, which sum and avg take too,
			// but min and max over mixed kinds fail: only operands that are
			// numeric or untyped (cast to xs:double) qualify.
			return numericOperand(x.Left) && numericOperand(x.Right)
		}
	case *xpath.UnaryOp:
		return numericOperand(x.Operand)
	case *xpath.FuncCall:
		switch x.Name.URI {
		case fnNS:
			switch x.Name.Local {
			case "number", "abs", "round", "floor", "ceiling", "round-half-to-even":
				return x.Name.Local == "number" || len(x.Args) >= 1 && numericOperand(x.Args[0])
			}
		case xdm.NSXS:
			return numericType[x.Name.Local]
		}
	case *xpath.CastExpr:
		return !x.Castable && x.Type.HasAtomicType && numericCode[x.Type.AtomicType]
	case *xpath.FilterExpr:
		return numericValued(x.Base)
	case *xpath.SimpleMap:
		return numericValued(x.Right)
	case *xpath.IfExpr:
		return numericValued(x.Then) && numericValued(x.Else)
	}
	return false
}

// numericOperand: a number, or what an arithmetic operator casts to
// xs:double (an untyped value: an attribute, a node's data, a literal number).
func numericOperand(e xpath.Expr) bool {
	if l, ok := e.(*xpath.Literal); ok {
		return l.Val != nil && numericCode[l.Val.Type]
	}
	return numericValued(e) || untypedValued(e)
}

// untypedValued: nodes of the stream, or their data: untyped when atomized.
func untypedValued(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Step, *xpath.ContextItem:
		return true
	case *xpath.PathExpr:
		return !x.Root && len(x.Steps) > 0 && untypedValued(x.Steps[len(x.Steps)-1])
	case *xpath.FuncCall:
		return x.Name.URI == fnNS && (x.Name.Local == "data" || x.Name.Local == "copy-of") &&
			(len(x.Args) == 0 || len(x.Args) == 1 && isContextItem(x.Args[0]))
	}
	return false
}

// numericCheck reports whether a predicate, when it holds, has converted
// "." to a number: ". op N" by a general comparison, or "T(.) op N" for a
// numeric constructor T, N a numeric literal (either side).
func numericCheck(p xpath.Expr) bool {
	x, ok := p.(*xpath.BinaryOp)
	if !ok {
		return false
	}
	general := false
	switch x.Op {
	case "=", "!=", "<", "<=", ">", ">=":
		general = true
	case "eq", "ne", "lt", "le", "gt", "ge":
	default:
		return false
	}
	side := func(a, b xpath.Expr) bool {
		l, ok := b.(*xpath.Literal)
		if !ok || l.Val == nil || !numericCode[l.Val.Type] {
			return false
		}
		if isContextItem(a) {
			return general
		}
		f, ok := a.(*xpath.FuncCall)
		return ok && f.Name.URI == xdm.NSXS && numericType[f.Name.Local] && len(f.Args) == 1 && isContextItem(f.Args[0])
	}
	return side(x.Left, x.Right) || side(x.Right, x.Left)
}

var numericType = map[string]bool{"integer": true, "decimal": true, "double": true, "float": true}

var numericCode = map[xdm.TypeCode]bool{xdm.TypeInteger: true, xdm.TypeDecimal: true, xdm.TypeDouble: true, xdm.TypeFloat: true}

// numberCompare reports whether p is ". op N" by a general comparison, N a
// numeric literal: it cannot fail once "." has been converted to a number.
func numberCompare(p xpath.Expr) bool {
	x, ok := p.(*xpath.BinaryOp)
	if !ok {
		return false
	}
	switch x.Op {
	case "=", "!=", "<", "<=", ">", ">=":
		return numericCheck(p)
	}
	return false
}
