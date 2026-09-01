package xquery

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// One of the XQuery-only expression forms nested inside an XPath expression.
//
// "(typeswitch ($x) case ... default return ...) eq 1" and
// "upper-case(typeswitch (...) ...)" are both ordinary XQuery, and neither can
// reach the parser in exprsingle.go, because the whole run of text is handed
// to xpath as a substring and the construct is not at its head.
//
// The fix has to keep the architecture: xpath must not learn the syntax, and
// the XQuery parser must not learn XPath's. So the construct is lifted out of
// the substring before it is handed over, and a variable reference is put in
// its place. "(typeswitch ($x) ...) eq 1" becomes "($xq:lifted-0) eq 1", with
// the typeswitch parsed here and bound to that variable when the expression
// runs. Everything around it is still XPath and is still parsed by the
// conformant parser.
//
// The substitution is sound because what is lifted is a whole ExprSingle
// enclosed in parentheses, whose value the surrounding expression uses exactly
// as it would a variable's: an ExprSingle in parentheses has no focus of its
// own to lose, and a variable reference is a PrimaryExpr, which is what a
// parenthesised expression is too. What it does *not* survive is a construct
// that reads the focus set by something outside it — "a/(typeswitch
// (self::node()) ...)" — because the lifted expression is evaluated before the
// step establishes the focus. Those are left where they are, unlifted, and
// fail as they did; there are two of them in the suite.

// liftPrefix names the variables this rewriting introduces. The namespace is
// this package's own so that nothing a query can write collides with it: a
// query cannot bind a prefix to it, because it never needs to.
const liftNS = "urn:x-go-xml:xquery:lifted"

// lifted is one construct lifted out of an expression.
type lifted struct {
	name xdm.QName
	node node
}

// liftedExpr is an xpath expression with lifted constructs bound around it.
type liftedExpr struct {
	compiled *compiledExpr
	lifted   []lifted
}

func (n *liftedExpr) eval(out *builderRef, ctx *evalContext) error {
	seq, err := n.sequence(ctx)
	if err != nil {
		return err
	}
	return appendSequence(out, seq)
}

// sequence evaluates the lifted constructs, binds each to its variable, and
// then evaluates the rewritten expression against that.
//
// The bindings are made in order and each is visible to the next, which costs
// nothing and is what a reader would expect; in practice a lifted construct
// never refers to another, because they were siblings in the original text.
func (n *liftedExpr) sequence(ctx *evalContext) (xdm.Sequence, error) {
	xp := ctx.xp
	for _, l := range n.lifted {
		seq, err := asEnclosed(l.node).sequence(&evalContext{xp: xp, sc: ctx.sc})
		if err != nil {
			return nil, err
		}
		xp = xp.WithVar(l.name, seq)
	}
	return n.compiled.compiled.Eval(xp)
}

// compileMaybeLifting compiles an expression, first lifting out any
// XQuery-only construct nested inside it.
//
// It is compileExpr's wrapper rather than a change to it, so that the ordinary
// path — an expression with none of these in it, which is nearly all of them —
// does one scan and is otherwise untouched.
func (p *parser) compileMaybeLifting(src string) (node, error) {
	rewritten, lifts, err := p.liftNested(src)
	if err != nil {
		return nil, err
	}
	c, err := p.compileExpr(rewritten)
	if err != nil {
		return nil, err
	}
	if len(lifts) == 0 {
		return &enclosed{expr: c}, nil
	}
	return &liftedExpr{compiled: c, lifted: lifts}, nil
}

// liftNested finds every parenthesised XQuery-only construct in src, parses it,
// and replaces its text with a variable reference.
//
// Only a construct that fills a whole parenthesised group is lifted. That is
// the shape the suite writes and it is the one that is safe: the group's
// boundaries say exactly where the construct ends, so nothing has to guess
// where an ExprSingle stops inside text this package is not parsing.
func (p *parser) liftNested(src string) (string, []lifted, error) {
	if !mightNest(src) {
		return src, nil, nil
	}
	var out strings.Builder
	var lifts []lifted
	i := 0
	for i < len(src) {
		switch src[i] {
		case '\'', '"':
			end, err := skipString(src, i)
			if err != nil {
				// Let the expression parser report it in context.
				return src, nil, nil
			}
			out.WriteString(src[i : end+1])
			i = end + 1
			continue
		case '(':
			if i+1 < len(src) && src[i+1] == ':' {
				end, err := skipComment(src, i)
				if err != nil {
					return src, nil, nil
				}
				out.WriteString(src[i : end+1])
				i = end + 1
				continue
			}
			end, err := findParen(src, i)
			if err != nil {
				return src, nil, nil
			}
			inner := src[i+1 : end]
			n, ok, err := p.liftOne(inner)
			if err != nil {
				return "", nil, err
			}
			if !ok {
				// Not a construct, but it may hold one: descend, so that
				// "upper-case((typeswitch ...))" is reached.
				sub, subLifts, err := p.liftNested(inner)
				if err != nil {
					return "", nil, err
				}
				lifts = append(lifts, subLifts...)
				out.WriteString("(" + sub + ")")
				i = end + 1
				continue
			}
			name := xdm.QName{URI: liftNS,
				Local: fmt.Sprintf("lifted-%d", len(lifts))}
			lifts = append(lifts, lifted{name: name, node: n})
			// Q{uri}local is the one spelling of a variable name that needs
			// no prefix binding, so the rewritten text is self-contained.
			out.WriteString("($Q{" + liftNS + "}" + name.Local + ")")
			i = end + 1
			continue
		}
		out.WriteByte(src[i])
		i++
	}
	return out.String(), lifts, nil
}

// liftOne parses inner as a whole XQuery-only construct, reporting whether it
// was one.
//
// The whole of inner must be the construct: "typeswitch (1) case ... default
// return 2" is lifted, and "1 + 2" is not, but so is "typeswitch (...) ..., 3",
// which is a sequence whose first item is one. The trailing text is what
// distinguishes them, and a construct that does not consume all of inner is
// left alone rather than half-lifted.
func (p *parser) liftOne(inner string) (node, bool, error) {
	sub := &parser{src: inner, sc: p.sc, version: p.version}
	sub.skipSpaceAndComments()
	if !sub.startsXQueryOnly() {
		return nil, false, nil
	}
	n, ok, err := sub.parseXQueryOnly()
	if err != nil || !ok {
		return nil, false, err
	}
	sub.skipSpaceAndComments()
	if !sub.eof() {
		// Something followed it inside the same parentheses. Rather than
		// guess, leave the whole group to the expression parser, which will
		// report the syntax error against the text as written.
		return nil, false, nil
	}
	return n, true, nil
}

// startsXQueryOnly reports whether one of the XQuery-only forms begins here,
// without parsing it.
//
// It is the cheap half of the test parseXQueryOnly makes, used so that
// liftOne does not build a sub-parser for every parenthesised group in every
// expression in the query.
func (p *parser) startsXQueryOnly() bool {
	if p.lookingAt("``[") || p.lookingAt("(#") {
		return true
	}
	switch p.peekKeyword() {
	case "try", "switch", "typeswitch", "ordered", "unordered", "validate":
		return true
	}
	return false
}

// mightNest reports whether src could hold one of these constructs at all.
//
// Every expression in the query passes through here, so the common answer has
// to be cheap: a single scan for the keywords, before any parenthesis matching
// or sub-parsing happens. A false positive costs one wasted walk; a false
// negative is impossible, because a construct cannot be written without its
// keyword.
func mightNest(src string) bool {
	if strings.Contains(src, "``[") || strings.Contains(src, "(#") {
		return true
	}
	for _, kw := range []string{"try", "switch", "typeswitch", "ordered",
		"unordered", "validate"} {
		if strings.Contains(src, kw) {
			return true
		}
	}
	return false
}
