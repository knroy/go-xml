package xquery

import (
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// Constructors and the other XQuery-only forms as operands of an ordinary
// expression.
//
// parseNestedExpr unwraps the three shapes where such a form is the whole of
// something this parser can find by bracket: the construct itself, a
// parenthesised sequence, and a call's argument list. What it cannot unwrap is
// an operator with one as an operand — "10000 = <a>10000</a>", "<a/> eq $x",
// "1 + (for $i in 1 to 2 return $i)[1]" — because the operator and its
// precedence belong to xpath and the operand belongs here, and neither parser
// can read the other's half.
//
// The way out is the one every other split in this package uses: compute the
// half this parser owns, bind it to a variable, and let xpath compile an
// expression over that variable. Here it is done for every XQuery-only
// primary in the expression at once. Each one's source span is replaced by
// "$local:xq-opN" and the rewritten source — now pure XPath — goes to xpath,
// which sees a variable reference exactly where the primary was. Substituting
// at *primary* position is what makes this precedence-preserving: a variable
// reference is a PrimaryExpr, the same grammatical slot the constructor and
// the FLWOR occupied, so every operator around it binds as it did before.
//
// This is not a general re-parse of XQuery in this package. It only finds the
// primaries, and it finds them with the same lexical test needsXQueryParser
// uses to decide that xpath cannot read the expression at all, so the two
// cannot disagree about where one is.

// opVar names the variable one substituted operand's value is bound to. The
// reserved local-function namespace keeps it from colliding with anything the
// query itself binds.
func opVar(i int) string { return "xq-op" + strconv.Itoa(i) }

// parseOperandSubst rewrites the expression at the cursor as an XPath
// expression over variables bound to the XQuery-only primaries in it,
// reporting whether it found any to substitute.
//
// It reports false — rather than an error — when the expression holds no such
// primary, or when one of them is not a self-contained span this can lift
// out. The caller then reports against its own expectations, so refusing here
// costs nothing that was not already refused.
func (p *parser) parseOperandSubst() ([]node, bool, error) {
	src := p.src[p.pos:]
	ops, rewritten, err := p.substituteOperands(src)
	if err != nil || len(ops) == 0 {
		return nil, false, err
	}
	c, err := p.compileExpr(rewritten)
	if err != nil {
		// The rewrite is only worth attempting; where it produces something
		// xpath still cannot read, the caller's own error is the better one,
		// because it names the construct rather than a variable this package
		// invented.
		return nil, false, nil
	}
	p.pos = len(p.src)
	return []node{&operandExpr{ops: ops, rest: c}}, true, nil
}

// substituteOperands scans src for XQuery-only primaries, parsing each and
// replacing its span with a variable reference.
//
// The scan tracks the last significant byte the way startsMarkup and
// scanExprSingleSource do, because that byte is what decides whether a word
// or a "<" is at operand position at all: after a name, a literal or a
// closing bracket, "<" is less-than and "element" is a name. Strings and
// comments are stepped over so that neither can be mistaken for source.
func (p *parser) substituteOperands(src string) ([]node, string, error) {
	var ops []node
	var out strings.Builder
	copied := 0
	prev := byte(0)

	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\'' || c == '"':
			end, err := skipString(src, i)
			if err != nil {
				return nil, "", nil
			}
			i = end + 1
			prev = '"'
			continue

		case c == '(' && i+1 < len(src) && src[i+1] == ':':
			end, err := skipComment(src, i)
			if err != nil {
				return nil, "", nil
			}
			i = end + 1
			continue
		}

		start := i
		if !startsOperand(src, i, prev) {
			if isNameStartByte(c) {
				// Step over the whole name, so that a keyword's tail is not
				// rescanned as though it began a word of its own.
				w := i
				for i < len(src) && isNameByte(src[i]) {
					i++
				}
				// A word operator is followed by an operand, not by more of a
				// name: "$x and <a/>" opens a constructor where "$x and" on
				// its own would not. prev is what decides that for the next
				// "<", so a word operator reports itself as an operator
				// rather than as the name byte it ends with.
				if wordOperators[src[w:i]] {
					prev = '+'
				} else {
					prev = src[i-1]
				}
				continue
			}
			if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
				prev = c
			}
			i++
			continue
		}

		// A primary this parser owns begins here. Parsing it with a
		// sub-parser over the same source is what finds where it ends: a
		// constructor's extent is decided by its markup and a FLWOR's by its
		// clauses, and neither can be found by counting brackets.
		sub := &parser{src: src, pos: i, sc: p.sc, version: p.version,
			depth: p.depth + 1}
		n, err := sub.parseBareItem()
		if err != nil {
			// Not something this can lift out after all. The expression is
			// left to the caller, whose error names the construct.
			return nil, "", nil
		}
		out.WriteString(src[copied:start])
		out.WriteString("$local:" + opVar(len(ops)))
		ops = append(ops, n)
		copied = sub.pos
		i = sub.pos
		prev = ')'
	}
	if len(ops) == 0 {
		return nil, "", nil
	}
	out.WriteString(src[copied:])
	return ops, out.String(), nil
}

// wordOperators are the operators spelled as words. After one of them an
// operand begins, so a "<" there is markup and not a comparison — which is
// the opposite of what the last byte of the word, a name byte, would say on
// its own. §3.5 and §3.6 list them; "to", "div", "idiv" and "mod" are the
// arithmetic and range ones, the rest are the comparisons and the boolean
// and sequence operators.
var wordOperators = map[string]bool{
	"and": true, "or": true, "to": true, "div": true, "idiv": true,
	"mod": true, "eq": true, "ne": true, "lt": true, "le": true,
	"gt": true, "ge": true, "is": true, "union": true, "intersect": true,
	"except": true, "return": true, "satisfies": true, "then": true,
	"else": true, "in": true,
}

// startsOperand reports whether an XQuery-only primary begins at src[i], with
// prev the last significant byte before it.
//
// It is the operand-position half of needsXQueryParser, asked about one
// offset rather than about the whole string: a direct constructor, a computed
// constructor, or a FLWOR or quantified expression whose clauses XPath's
// grammar does not have. prev == 0 — nothing before it — is the head of the
// expression, which parseNestedExpr already handles; it is still accepted
// here so that "<a/> = <b/>" substitutes both operands rather than one.
func startsOperand(src string, i int, prev byte) bool {
	if src[i] == '<' {
		return startsMarkup(src, i, prev)
	}
	if !isNameStartByte(src[i]) {
		return false
	}
	// A word is only a keyword where an operand may begin. After a name, a
	// "$", a "@", a "/" or a "::" it is part of a name or a step.
	if prev != 0 && (isNameByte(prev) || prev == '$' || prev == ':' ||
		prev == '@' || prev == '/' || prev == ')' || prev == ']' ||
		prev == '"' || prev == '\'' || prev == '*') {
		return false
	}
	j := i
	for j < len(src) && isNameByte(src[j]) {
		j++
	}
	switch src[i:j] {
	case "for", "let", "some", "every":
		// XPath has the untyped forms of all four, so the keyword alone
		// proves nothing; what proves it is a clause XPath's grammar lacks.
		return hasXQueryOnlyClause(src[j:])
	case "element", "attribute", "document", "text", "comment",
		"processing-instruction", "namespace", "ordered", "unordered":
		// A computed constructor is the keyword followed by a name or an
		// expression in braces. A kind test is followed by "(", and
		// "namespace::" is the axis, which is refused elsewhere.
		k := skipSpaceFrom(src, j)
		return k < len(src) && (src[k] == '{' || isNameStartByte(src[k]))
	case "try", "switch", "typeswitch", "validate":
		// None is reserved, so each only commits where what follows it can
		// only be the construct — the same test parseXQueryOnly makes.
		sub := &parser{src: src, pos: i}
		_, ok, _ := sub.parseXQueryOnly()
		return ok
	}
	return false
}

// operandExpr is an ordinary expression whose operands this package had to
// parse itself.
//
// The operators are still xpath's: rest is the expression with each such
// operand replaced by a variable reference, and evaluating it binds each
// parsed operand's value to the matching variable first. Nothing about
// precedence, atomization or the comparison rules changes — only where the
// operand values came from.
type operandExpr struct {
	ops  []node
	rest *compiledExpr
}

func (n *operandExpr) sequence(ctx *evalContext) (xdm.Sequence, error) {
	xp := ctx.xp
	for i, op := range n.ops {
		v, err := (&enclosed{items: []node{op}}).sequence(ctx)
		if err != nil {
			return nil, err
		}
		xp = xp.WithVar(xdm.QName{URI: nsLocal, Local: opVar(i)}, v)
	}
	return n.rest.compiled.Eval(xp)
}

func (n *operandExpr) eval(out *builderRef, ctx *evalContext) error {
	seq, err := n.sequence(ctx)
	if err != nil {
		return err
	}
	return appendSequence(out, seq, ctx.sc)
}
