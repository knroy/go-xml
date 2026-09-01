package xquery

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xdmbuild"
	"github.com/knroy/go-xml/xpath"
)

// Options configure how a query is compiled.
//
// The zero value is the specification's defaults: boundary whitespace is
// stripped, construction preserves types, an unprefixed element name is in no
// namespace, and an unprefixed function name is in the fn: namespace.
type Options struct {
	// BaseURI is the static base URI. It is stamped on constructed elements
	// and is what a relative reference resolves against.
	BaseURI string

	// BoundarySpace decides whether whitespace that only separates markup
	// survives into a constructed element. The zero value strips it, which is
	// what a query with no "declare boundary-space" gets.
	BoundarySpace BoundarySpace

	// Construction decides whether a copied node keeps its type annotation.
	// The zero value preserves it.
	Construction Construction

	// DefaultElementNamespace is applied to an unprefixed element name. It is
	// never applied to an attribute name.
	DefaultElementNamespace string

	// Namespaces are prefix bindings available to the query, as though its
	// prolog had declared them. The five predeclared prefixes of §4.1 are
	// bound already and do not need to appear here.
	Namespaces map[string]string
}

// A Query is a compiled query, safe for concurrent use.
//
// Compiling separates what can be decided from the text — namespaces, the
// shape of every constructor, and every expression in it — from what depends
// on the input. Nothing about a Query changes when it runs, so one may be
// evaluated from several goroutines at once.
type Query struct {
	body []node
	sc   *staticContext
	src  string

	// vars and funcs are the prolog's variable and function declarations, in
	// source order. Both are order-independent by §4.14 and §4.15, so the
	// order here is for error reporting rather than for evaluation.
	vars  []*varDecl
	funcs []*funcDecl

	// contextItem is "declare context item", when the prolog made one.
	contextItem *contextItemDecl

	// formats are the decimal formats the prolog declared, keyed by Clark
	// name with the empty key for the default.
	formats map[string]*xpath.DecimalFormat

	// lib is the function library the query's own declared functions live in,
	// built once at compile time and chained to the builtins. It is built
	// here rather than per evaluation because a Query is immutable and safe
	// for concurrent use, and building it once is what keeps it so.
	lib *xpath.Library
}

// Compile compiles a query.
//
// What is implemented is the prolog, the constructors and the XQuery-only
// expression forms: direct and computed constructors, try/catch, switch,
// typeswitch, ordered and unordered, the extension expression, the string
// constructor, and validate, which parses and then refuses because the
// in-scope schema definitions are empty until schema import exists. A query
// that is only an expression compiles too, since every XPath 3.1 expression
// is an XQuery expression. FLWOR and the two imports are not yet implemented
// and are reported as such rather than mis-parsed.
func Compile(src string, opts Options) (*Query, error) {
	sc := newStaticContext()
	sc.baseURI = opts.BaseURI
	sc.boundarySpace = opts.BoundarySpace
	sc.construction = opts.Construction
	sc.defaultElementNS = opts.DefaultElementNamespace
	for prefix, uri := range opts.Namespaces {
		if err := sc.bind(prefix, uri); err != nil {
			return nil, err
		}
	}

	p := &parser{src: src, sc: sc, version: xpath.XPath31,
		declaredNS: map[string]bool{}}
	// The version declaration, the prolog and the body are read in that order
	// because each changes how the next is read: a version declaration can
	// refuse the whole query, and every prolog declaration is applied to the
	// static context that the body's names then resolve against.
	if err := p.parseVersionDecl(); err != nil {
		return nil, err
	}
	if err := p.parseProlog(); err != nil {
		return nil, err
	}
	body, err := p.parseQueryBody()
	if err != nil {
		return nil, err
	}
	q := &Query{body: body, sc: sc, src: src, vars: p.vars, funcs: p.funcs,
		contextItem: p.contextItem, formats: p.formats}
	q.lib = q.registerFunctions(nil)
	return q, nil
}

// Eval compiles and runs a query in one step.
func Eval(src string, ctx *xpath.Context, opts Options) (xdm.Sequence, error) {
	q, err := Compile(src, opts)
	if err != nil {
		return nil, err
	}
	return q.Eval(ctx)
}

// Eval runs the query and returns its result sequence.
//
// ctx supplies the context item, the variable bindings and the function
// library, exactly as it does for an XPath expression. A nil function library
// is legal and means the query may not call anything.
func (q *Query) Eval(ctx *xpath.Context) (xdm.Sequence, error) {
	if ctx == nil {
		ctx = xpath.NewContext(nil, xpath.Builtins())
	}
	ctx, err := q.prepare(ctx)
	if err != nil {
		return nil, err
	}
	out := xdmbuild.New(policy{sc: q.sc})
	ref := &builderRef{b: out}
	ec := &evalContext{xp: ctx, sc: q.sc}
	for _, n := range q.body {
		if err := n.eval(ref, ec); err != nil {
			return nil, err
		}
	}
	return out.Sequence(), nil
}

// prepare installs the module's static context on the evaluation context:
// the declared functions, the declared global variables and the declared
// context item.
//
// It runs per evaluation rather than per compilation because the values are
// dynamic — a global's initialiser may call fn:current-dateTime, or read a
// document, or depend on an external variable the caller bound differently
// this time — while the plan that produces them is not.
//
// The function library is chained onto whatever the caller supplied rather
// than replacing it, so a host that registered extension functions keeps them
// and the query's own declarations sit in front.
func (q *Query) prepare(ctx *xpath.Context) (*xpath.Context, error) {
	sub := *ctx
	if len(q.funcs) > 0 || len(q.formats) > 0 {
		if ctx.Funcs == nil || ctx.Funcs == xpath.FunctionLibrary(q.lib.Parent) {
			sub.Funcs = q.lib
		} else {
			// The caller's library is not the one the query was compiled
			// against, so a fresh chain is built over theirs. This is the
			// only per-evaluation allocation the prolog costs, and only for a
			// query that declares functions and a caller that supplied a
			// library of their own.
			sub.Funcs = q.registerFunctions(ctx.Funcs)
		}
	}
	// The base URI and the default collation are stamped on each compiled
	// expression by compileExpr, because xpath models both as static
	// properties of an expression. Setting the context's base URI as well is
	// what fn:static-base-uri and fn:doc's relative resolution read.
	if q.sc.baseURI != "" {
		sub.StaticBaseURI = q.sc.baseURI
	}
	out := &sub
	if q.contextItem != nil {
		var err error
		if out, err = q.bindContextItem(out); err != nil {
			return nil, err
		}
	}
	return q.bindVariables(out)
}

// bindContextItem applies "declare context item" (§4.16).
//
// An external declaration takes the caller's item when there is one and falls
// back to its default when there is not. A non-external one replaces the
// caller's item outright, which is what a query that computes its own context
// is asking for. XPDY0002 is the error when neither supplies one.
func (q *Query) bindContextItem(ctx *xpath.Context) (*xpath.Context, error) {
	d := q.contextItem
	sub := *ctx
	if d.external && ctx.Item != nil {
		// Only the type check applies: the value is the caller's.
		if _, err := d.typ.convert(xdm.Sequence{ctx.Item},
			"the context item"); err != nil {
			return nil, err
		}
		return ctx, nil
	}
	if d.init == nil {
		if ctx.Item != nil {
			return ctx, nil
		}
		return nil, fmt.Errorf(
			"XPDY0002: no value was supplied for the declared context item")
	}
	// The initialiser is evaluated with the globals already in scope, so a
	// "declare context item := $doc" can name one. bindVariables runs after
	// this, so the initialiser sees only what the caller bound — which is the
	// dependency order §4.16 gives, since the context item is part of the
	// dynamic context the variables are initialised against.
	seq, err := d.init.compiled.Eval(&sub)
	if err != nil {
		return nil, err
	}
	seq, err = d.typ.convert(seq, "the context item")
	if err != nil {
		return nil, err
	}
	switch len(seq) {
	case 0:
		sub.Item = nil
	case 1:
		sub.Item = seq[0]
	default:
		return nil, fmt.Errorf(
			"XPTY0004: the context item must be a single item, not %d", len(seq))
	}
	sub.Position, sub.Size = 1, 1
	return &sub, nil
}

// String returns the query's source.
func (q *Query) String() string { return q.src }

// parseQueryBody parses the body of a main module.
//
// A query body is one expression, but an expression may be a comma-separated
// sequence and may be a constructor, so this reads whichever it finds. It is
// reached with the prolog already consumed and applied.
func (p *parser) parseQueryBody() ([]node, error) {
	p.skipSpaceAndComments()
	if err := p.refuseUnimplemented(); err != nil {
		return nil, err
	}
	var out []node
	for {
		p.skipSpaceAndComments()
		if p.eof() {
			return out, nil
		}
		n, err := p.parseItem()
		if err != nil {
			return nil, err
		}
		out = append(out, n)
		p.skipSpaceAndComments()
		if !p.consume(",") {
			break
		}
	}
	p.skipSpaceAndComments()
	if !p.eof() {
		return nil, p.errorf("XPST0003: unexpected %q",
			firstToken(p.src[p.pos:]))
	}
	return out, nil
}

// parseItem parses one item of a query body: a constructor, or an expression
// handed to xpath.
func (p *parser) parseItem() (node, error) {
	// FLWOR and the quantified expressions are read here rather than handed
	// to xpath, because both admit syntax XPath's grammar does not: every
	// clause but "for" and "let", and a type declaration on a bound variable.
	// A plain "for $x in E return F" would compile either way; taking it here
	// keeps one implementation rather than two that must agree.
	p.skipSpaceAndComments()
	start := p.pos
	n, err := p.parseBareItem()
	if err != nil {
		return nil, err
	}
	// A path step or a predicate may follow the construct — "<e/>/name()",
	// "(for $x in E return F)[1]". The construct is parsed here and the step
	// compiled by xpath over its value, because the two halves need different
	// parsers and neither can read the other's.
	n, err = p.withTrailingPath(n)
	if err != nil {
		return nil, err
	}
	// An operator may follow it instead — "<a>10000</a> = 10000". That does
	// not bind tightly enough for withTrailingPath's rewrite, so the item is
	// re-read from its start with every XQuery-only primary in it lifted into
	// a variable, and xpath compiles the operators over those. See operand.go.
	if op, ok, err := p.retryAsOperandSubst(start); ok || err != nil {
		return op, err
	}
	return n, nil
}

// retryAsOperandSubst re-reads the item beginning at start as an expression
// whose XQuery-only primaries are substituted, when what follows the item is
// an operator rather than the end of it.
//
// The item is over at a top-level comma, at a closing bracket this did not
// open, and at a clause keyword — the same boundary scanExprSingleSource
// draws, and the one every caller of parseItem relies on. Anything else after
// it is an operator whose left operand is what was just parsed, so the item
// was not the whole expression and has to be read again as one.
func (p *parser) retryAsOperandSubst(start int) (node, bool, error) {
	save := p.pos
	p.skipSpaceAndComments()
	if p.eof() {
		p.pos = save
		return nil, false, nil
	}
	switch p.src[p.pos] {
	case ',', ')', ']', '}':
		p.pos = save
		return nil, false, nil
	}
	p.pos = start
	src, err := p.scanExprSingleSource()
	if err != nil || strings.TrimSpace(src) == "" {
		p.pos = save
		return nil, false, nil
	}
	end := p.pos
	sub := &parser{src: src, sc: p.sc, version: p.version, depth: p.depth}
	items, ok, err := sub.parseOperandSubst()
	if err != nil || !ok {
		p.pos = save
		return nil, false, err
	}
	p.pos = end
	return &enclosed{items: items}, true, nil
}

// parseBareItem parses one item without the path that may follow it.
func (p *parser) parseBareItem() (node, error) {
	if p.looksLikeFLWOR() {
		f, err := p.parseFLWOR()
		if err != nil {
			return nil, err
		}
		return &flworNode{f}, nil
	}
	if p.looksLikeQuantified() {
		q, err := p.parseQuantified()
		if err != nil {
			return nil, err
		}
		return &quantifiedNode{q}, nil
	}
	// The other XQuery-only forms — try/catch, switch, typeswitch, ordered
	// and unordered, the extension expression and the string constructor —
	// each begin with a keyword or a delimiter the expression parser beneath
	// would read as something else: "try" as a function call, "``[" as two
	// empty string literals and a predicate.
	if n, ok, err := p.parseXQueryOnly(); ok || err != nil {
		return n, err
	}
	switch {
	case p.lookingAt("<!--"):
		return p.parseDirComment()
	case p.lookingAt("<?"):
		return p.parseDirPI()
	case p.lookingAt("<"):
		return p.parseDirElement()
	}
	if n, ok, err := p.parseComputed(); ok || err != nil {
		return n, err
	}
	return p.parseExprItem()
}

// parseExprItem takes the run of source up to the next top-level comma and
// hands it to xpath.
//
// The comma has to be found here rather than by the expression parser because
// a query body's items are separated by one, and a constructor between two of
// them is not something xpath can read.
func (p *parser) parseExprItem() (node, error) {
	start := p.pos
	depth := 0
	for !p.eof() {
		switch p.src[p.pos] {
		case '\'', '"':
			end, err := skipString(p.src, p.pos)
			if err != nil {
				return nil, err
			}
			p.pos = end + 1
			continue
		case '(':
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == ':' {
				end, err := skipComment(p.src, p.pos)
				if err != nil {
					return nil, err
				}
				p.pos = end + 1
				continue
			}
			depth++
		case ')', ']', '}':
			if depth == 0 {
				// A closing bracket this scan never opened ends the item: it
				// belongs to the parenthesis or the call this expression is
				// an argument of.
				goto done
			}
			depth--
		case '[', '{':
			depth++
		case ',':
			if depth == 0 {
				goto done
			}
		}
		p.pos++
	}
done:
	src := strings.TrimSpace(p.src[start:p.pos])
	if src == "" {
		return nil, p.errorf("XPST0003: expected an expression")
	}
	if needsXQueryParser(src) {
		// A constructor or a FLWOR somewhere inside the expression, rather
		// than at its start: "(<a/>, <b/>)" and "count(for $x in E group by
		// $k return $x)" are both expressions xpath cannot read whole, and
		// neither begins with the construct that makes it so.
		c, err := p.parseFromSource(src)
		if err != nil {
			return nil, err
		}
		return &enclosed{items: c.items}, nil
	}
	c, err := p.compileExpr(src)
	if err != nil {
		return nil, err
	}
	return &enclosed{expr: c}, nil
}

// skipSpaceAndComments consumes whitespace and XQuery comments, which may
// appear anywhere whitespace may and which nest.
//
// It reports whether anything was consumed, which the prolog needs: "declare
// namespace" and "declarenamespace" differ only in that a separator was
// there, and a comment is a legal separator — "declare(:x:)namespace" is a
// namespace declaration.
func (p *parser) skipSpaceAndComments() bool {
	start := p.pos
	for {
		p.skipSpace()
		if !p.lookingAt("(:") {
			return p.pos > start
		}
		end, err := skipComment(p.src, p.pos)
		if err != nil {
			// Leave it: the expression parser will report it in context.
			return p.pos > start
		}
		p.pos = end + 1
	}
}

// refuseUnimplemented reports a clear error for the parts of XQuery this
// package does not implement yet.
//
// A prolog parsed as though it were an expression produces an error naming a
// token rather than the feature, which sends the reader looking for a syntax
// mistake that is not there. Naming the feature is the more useful failure
// while the implementation is incomplete.
func (p *parser) refuseUnimplemented() error {
	if hasKeywordPrefix(p.src[p.pos:], "module namespace") {
		return p.errorf(
			"XQST0059: a library module needs module resolution, " +
				"which is not implemented yet")
	}
	// Nothing else is refused by name any more: FLWOR, the quantified
	// expressions, the prolog and the XQuery-only expression forms are all
	// implemented, so a query using one gets a real error from the construct
	// that is actually wrong rather than a blanket refusal.
	return nil
}

// hasKeywordPrefix reports whether s begins with a keyword, not merely with
// its letters: "letter" does not begin with "let".
func hasKeywordPrefix(s, kw string) bool {
	if !strings.HasPrefix(s, kw) {
		return false
	}
	if strings.HasSuffix(kw, " ") {
		return true
	}
	rest := s[len(kw):]
	return rest == "" || !isNameByte(rest[0])
}

func isNameByte(c byte) bool {
	return c == '-' || c == '_' || c == '.' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') || c >= 0x80
}

// firstToken returns enough of s to name what was unexpected.
func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\r\n"); i > 0 {
		s = s[:i]
	}
	if len(s) > 24 {
		s = s[:24] + "..."
	}
	return s
}

// unimplemented is the error for a construct the grammar has and this package
// does not yet parse.
func unimplemented(what string) error {
	return fmt.Errorf("XPST0003: %s is not implemented yet", what)
}
