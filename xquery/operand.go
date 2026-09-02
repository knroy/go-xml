package xquery

import (
	"strconv"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
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
// The way out is the one every other split in this package uses: keep the
// half this parser owns, and let xpath compile an expression around it. Here
// it is done for every XQuery-only primary in the expression at once. Each
// one's source span is replaced by a call to "local:xq-stepN()", a zero-arity
// function this package installs, and the rewritten source — now pure XPath —
// goes to xpath, which sees a function call exactly where the primary was.
// Substituting at *primary* position is what makes this precedence-
// preserving: a function call is a PrimaryExpr, the same grammatical slot the
// constructor and the FLWOR occupied, so every operator around it binds as it
// did before.
//
// A call rather than a variable, because a variable is bound once and the
// primary may stand somewhere that is evaluated many times, each with its own
// focus: the right operand of "/", the body of a "for" that a path drives,
// the inside of a predicate. "$e/(for $i in ... order by $i return name(.))"
// is the shape that forces it — the FLWOR is a step, "." is the node the step
// is applied to, and a variable bound before the path ran raised XPDY0002
// because at that moment there was no focus at all. The call is evaluated
// where it stands, as often as it stands there, with whatever focus xpath has
// set.
//
// This is not a general re-parse of XQuery in this package. It only finds the
// primaries, and it finds them with the same lexical test needsXQueryParser
// uses to decide that xpath cannot read the expression at all, so the two
// cannot disagree about where one is.

// stepFn names the zero-arity function one substituted primary is called
// through. The reserved local-function namespace keeps the name from
// colliding with anything the query itself declares.
func stepFn(i int) string { return "xq-step" + strconv.Itoa(i) }

// liftedOperand is one XQuery-only primary lifted out of an expression so
// that xpath could compile the rest around it.
type liftedOperand struct{ n node }

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
	// compileExpr substitutes again where xpath still refuses the source,
	// and its numbering restarts at zero. Here that would rebind the
	// variables this pass just chose, so the source it is given must have
	// nothing left to lift; substituteOperands scans to the end, so it does.
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
func (p *parser) substituteOperands(src string) ([]liftedOperand, string, error) {
	var ops []liftedOperand
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
		if !p.startsOperand(src, i, prev) {
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
					prev = operatorPrev
				} else {
					prev = src[i-1]
				}
				continue
			}
			if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
				// "-" and "*" each spell both a name character and a binary
				// operator, and what tells the two apart is whether the byte
				// before them abuts: "a-b" is one name and "a - b" is a
				// subtraction. Reporting the operator reading is what lets
				// "1 * <a/>" and "3 - <a/>" substitute their right operand,
				// which the name reading refused. prev is whitespace-stripped
				// and cannot answer this, so the source is looked at.
				switch {
				case (c == '-' || c == '*') && isOperandBreak(src, i):
					prev = operatorPrev
				case c == ':' && isSeparatorColon(src, i):
					// A colon that separates a map entry's key from its value
					// is operand position: "map {"k": element e {}}" opens a
					// computed constructor after it. startsOperand refuses a
					// keyword after a colon because of "foo:bar" and
					// "child::node()", where the colon is part of a name or
					// an axis; this one is neither, so it is reported as the
					// operator position it is. Serialization-json-65 and its
					// neighbours put an element constructor in a map value
					// and could not be read at all without this.
					prev = operatorPrev
				default:
					prev = c
				}
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
		out.WriteString("local:" + stepFn(len(ops)) + "()")
		ops = append(ops, liftedOperand{n: n})
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

// isSeparatorColon reports whether the ":" at src[i] separates two
// expressions rather than belonging to a name or an axis.
//
// Three colons appear in the grammar and only one of them is a separator. A
// prefixed name -- "fn:count", "xs:integer" -- has its colon abutting the
// name byte before it. An axis step -- "child::node()" -- spells its colon
// twice. What is left is the map constructor's "key : value" and the
// conditional-free ":=" of a "let", and in both of those an expression may
// begin after the colon. ":=" is excluded explicitly, since the "=" makes it
// an operator whose right side this function is not being asked about.
func isSeparatorColon(src string, i int) bool {
	if i > 0 && (isNameByte(src[i-1]) || src[i-1] == ':') {
		return false
	}
	if i+1 < len(src) && (src[i+1] == ':' || src[i+1] == '=') {
		return false
	}
	return true
}

// isOperandBreak reports whether the "-" or "*" at src[i] is a binary
// operator rather than a character continuing the name before it.
//
// A name byte immediately before it — no whitespace — means it continues a
// name: "a-b" is one NCName and "*" after a name byte cannot occur at all.
// Anything else is operator position, including the digit that ends a numeric
// literal, which is a name byte but can never be part of a name because a
// name may not begin with one.
func isOperandBreak(src string, i int) bool {
	return i == 0 || !isNameByte(src[i-1])
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
func (p *parser) startsOperand(src string, i int, prev byte) bool {
	if src[i] == '`' {
		// A string constructor is a PrimaryExpr and its opener is unambiguous:
		// XPath has no backtick, so "``[" can only be one wherever it stands.
		return strings.HasPrefix(src[i:], "``[")
	}
	if src[i] == '<' {
		return startsMarkup(src, i, prev)
	}
	if !isNameStartByte(src[i]) {
		return false
	}
	// A word is only a keyword where an operand may begin. After a name, a
	// "$", a "@" or a "::" it is part of a name or a step.
	if prev != 0 && (isNameByte(prev) || prev == '$' || prev == ':' ||
		prev == '@' || prev == ')' || prev == ']' ||
		prev == '"' || prev == '\'' || prev == '*') {
		return false
	}
	j := i
	for j < len(src) && isNameByte(src[j]) {
		j++
	}
	// A "/" is different from the rest: a step may be a PrimaryExpr, so
	// "/ordered{bid}" is a path whose one step is an ordered expression, and
	// PathExpr-21 says so outright. But a name after a "/" is usually a name
	// test, and every keyword here is also a legal element name, so only the
	// spellings that cannot be a name test are taken: a keyword followed by
	// "{" is a constructor, since a name test may not be followed by a brace.
	// The others -- "for", "function", "try" and their kin -- are left alone
	// after a "/", where the step reading is the right one.
	if prev == '/' {
		switch src[i:j] {
		case "element", "attribute", "document", "text", "comment",
			"processing-instruction", "namespace", "ordered", "unordered":
			if k := skipSpaceFrom(src, j); k >= len(src) || src[k] != '{' {
				return false
			}
		default:
			return false
		}
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
	case "function":
		// An inline function is an operand this package owns only when its
		// body needs the XQuery parser, and only parsing it says whether it
		// does. Lifting one out matters more than lifting out a constructor:
		// substituting the body would evaluate it outside the scope the
		// parameters are bound in. See inlineFunc.
		sub := &parser{src: src, pos: i, sc: p.sc, version: p.version,
			depth: p.depth + 1}
		_, ok, _ := sub.parseInlineFunc()
		return ok
	case "try", "switch", "typeswitch", "validate":
		// None is reserved, so each only commits where what follows it can
		// only be the construct — the same test parseXQueryOnly makes.
		// The probe inherits the static context and the version, because
		// deciding whether this is a switch means parsing one, and that
		// reaches compileExpr for the operand and every branch. A parser
		// without an sc dereferences nil there rather than reporting that
		// the keyword was not a switch after all; the depth is carried too
		// so that a probe cannot outrun the nesting limit the real parse
		// would hit.
		sub := &parser{src: src, pos: i, sc: p.sc, version: p.version,
			depth: p.depth + 1}
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
	ops  []liftedOperand
	rest *compiledExpr
}

func (n *operandExpr) sequence(ctx *evalContext) (xdm.Sequence, error) {
	xp, err := n.rest.bind(ctx)
	if err != nil {
		return nil, err
	}
	return n.rest.compiled.Eval(bindLifted(xp, n.ops, ctx))
}

// bindLifted makes the lifted operands reachable from the rewritten
// expression, by installing one zero-arity function per operand over the
// library already in the context.
//
// The library is chained rather than replaced, so every builtin and every
// function the query declared still resolves; only the invented names are
// new. Each closure carries the static context the primary was written in,
// which is what a constructor inside it needs, and takes its focus from the
// context xpath hands the call — which is the whole reason these are calls.
func bindLifted(xp *xpath.Context, ops []liftedOperand,
	ctx *evalContext) *xpath.Context {

	lib := xpath.NewLibrary(xp.Funcs)
	for i, op := range ops {
		item := op.n
		lib.Add(xpath.Function{
			Name:  xdm.QName{URI: nsLocal, Local: stepFn(i)},
			Arity: 0,
			Call: func(c *xpath.Context, _ []xdm.Sequence) (xdm.Sequence, error) {
				return (&enclosed{items: []node{item}}).sequence(
					&evalContext{xp: c, sc: ctx.sc})
			},
		})
	}
	sub := *xp
	sub.Funcs = lib
	return &sub
}

func (n *operandExpr) eval(out *builderRef, ctx *evalContext) error {
	seq, err := n.sequence(ctx)
	if err != nil {
		return err
	}
	return appendSequence(out, seq, ctx.sc)
}
