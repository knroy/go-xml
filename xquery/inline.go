package xquery

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xdmbuild"
)

// inlineFunc is an InlineFunctionExpr whose body this package had to parse.
//
// The expression parser already has the whole construct, and every inline
// function whose body is ordinary XPath is left to it. This exists for the
// one shape it cannot read: a body holding a constructor, a FLWOR clause or
// any other XQuery-only form.
//
// It exists as a node rather than as an operand substitution because the two
// are not interchangeable here. Substitution lifts the offending sub-
// expression out, binds its value to a generated variable and leaves a
// reference behind — which works wherever the sub-expression's value does not
// depend on where it stood. A function body is exactly the place where it
// does: "function($a) { <x>{$a}</x> }" substituted becomes "function($a) {
// local:xq-step0() }" with the constructor evaluated outside, where $a is not
// bound, and the result is XPST0008 for a variable the query plainly binds.
// So the body has to stay inside the function, which means this package has
// to build the function item itself.
type inlineFunc struct {
	params  []funcParam
	returns *sequenceType
	body    []node
	expr    *compiledExpr
}

// parseInlineFunc reads "function (ParamList) [as SequenceType] { ... }".
//
// It returns ok false without moving the position when what follows is not an
// inline function this package needs to own — either not a "function (" at
// all, or one whose body the expression parser can read on its own. Leaving
// the ordinary ones to xpath is deliberate: they already work, and every
// construct handled in two places is a construct that can disagree with
// itself.
func (p *parser) parseInlineFunc() (node, bool, error) {
	start := p.pos
	if !p.consumeKeyword("function") {
		p.pos = start
		return nil, false, nil
	}
	p.skipSpaceAndComments()
	if !p.consume("(") {
		p.pos = start
		return nil, false, nil
	}
	params, err := p.parseParamList()
	if err != nil {
		p.pos = start
		return nil, false, nil
	}
	n := &inlineFunc{params: params}
	p.skipSpaceAndComments()
	if p.consumeKeyword("as") {
		p.skipSpaceAndComments()
		if n.returns, err = p.parseSequenceType(); err != nil {
			p.pos = start
			return nil, false, nil
		}
		p.skipSpaceAndComments()
	}
	if !p.lookingAt("{") {
		p.pos = start
		return nil, false, nil
	}
	end, err := findEnclosed(p.src, p.pos)
	if err != nil {
		p.pos = start
		return nil, false, nil
	}
	src := p.src[p.pos+1 : end]
	if !needsXQueryParser(src) {
		// Ordinary XPath. Hand the whole construct back so that the
		// expression parser reads it, signature and all.
		p.pos = start
		return nil, false, nil
	}
	p.pos = end + 1
	if isBlank(src) {
		// 3.1's empty body, which means the empty sequence. It cannot reach
		// here — needsXQueryParser says no about whitespace — but the case is
		// written out so that the body below never sees an empty item list
		// and mistakes it for a parse that produced nothing.
		return n, true, nil
	}
	sub := &parser{src: src, sc: p.sc, version: p.version,
		declaredNS: p.declaredNS, funcs: p.funcs, vars: p.vars,
		depth: p.depth + 1}
	if n.body, err = sub.parseBodyItems(); err != nil {
		return nil, true, err
	}
	return n, true, nil
}

// eval implements node: the function item is a value like any other, so the
// contribution to the enclosing constructor is that single item.
func (n *inlineFunc) eval(out *builderRef, ctx *evalContext) error {
	seq, err := n.sequence(ctx)
	if err != nil {
		return err
	}
	return appendSequence(out, seq, ctx.sc)
}

// sequence builds the function item.
//
// The closure is the point. The captured context is the one in scope where
// the expression was written, so a body naming a variable bound outside still
// sees it when the function is called somewhere else entirely. The focus is
// not captured: §3.1.7 gives an inline function's body no context item,
// position or size, so "." inside one is XPDY0002 even where the expression
// that wrote it had a focus.
func (n *inlineFunc) sequence(ctx *evalContext) (xdm.Sequence, error) {
	noFocus := *ctx.xp
	noFocus.Item, noFocus.Position, noFocus.Size = nil, 0, 0
	captured := &noFocus
	sc := ctx.sc

	item := &xdm.FunctionItem{
		Arity:     len(n.params),
		Signature: inlineSignature(n),
	}
	item.Invoke = func(callCtx any, args []xdm.Sequence) (xdm.Sequence, error) {
		if len(args) != len(n.params) {
			return nil, fmt.Errorf(
				"XPTY0004: inline function expects %d argument(s), got %d",
				len(n.params), len(args))
		}
		// The body runs in the captured scope rather than the caller's,
		// which is what makes this a closure. callCtx names the context the
		// call was made from and is deliberately unused: the only thing it
		// could contribute is the per-evaluation resource accounting, and
		// this package cannot reach the unexported fields that carry it.
		_ = callCtx
		sub := captured
		for i, pm := range n.params {
			v := args[i]
			conv, err := pm.typ.convert(v, fmt.Sprintf(
				"parameter $%s of an inline function", pm.name.Lexical()))
			if err != nil {
				return nil, err
			}
			sub = sub.WithVar(pm.name, conv)
		}
		out, err := n.evalBody(&evalContext{xp: sub, sc: sc})
		if err != nil {
			return nil, err
		}
		return n.returns.convert(out, "the result of an inline function")
	}
	return xdm.One(item), nil
}

// evalBody runs the parsed body and collects what it built.
func (n *inlineFunc) evalBody(ctx *evalContext) (xdm.Sequence, error) {
	if n.expr != nil {
		return n.expr.eval(ctx)
	}
	if n.body == nil {
		return nil, nil
	}
	out := xdmbuild.New(policy{sc: ctx.sc})
	ref := &builderRef{b: out}
	for _, item := range n.body {
		if err := item.eval(ref, ctx); err != nil {
			return nil, err
		}
	}
	return out.Sequence(), nil
}

// inlineSignature records the declared types so that a typed function test
// has something to judge. An undeclared type is item()*, which is what the
// specification says a parameter with no "as" has.
func inlineSignature(n *inlineFunc) []string {
	sig := make([]string, 0, len(n.params)+1)
	sig = append(sig, typeSource(n.returns))
	for _, pm := range n.params {
		sig = append(sig, typeSource(pm.typ))
	}
	return sig
}
