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
}

// Compile compiles a query.
//
// What is implemented so far is the constructor half of XQuery 3.1: direct
// and computed constructors, with expressions inside them handled by the
// xpath package. A query that is only an expression compiles too, since every
// XPath 3.1 expression is an XQuery expression. FLWOR, the prolog and the
// XQuery-only expressions are not yet implemented and are reported as such
// rather than mis-parsed.
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

	p := &parser{src: src, sc: sc, version: xpath.XPath31}
	body, err := p.parseQueryBody()
	if err != nil {
		return nil, err
	}
	return &Query{body: body, sc: sc, src: src}, nil
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

// String returns the query's source.
func (q *Query) String() string { return q.src }

// parseQueryBody parses the body of a main module.
//
// A query body is one expression, but an expression may be a comma-separated
// sequence and may be a constructor, so this reads whichever it finds. The
// prolog is not implemented yet; a query that has one is refused by name
// rather than parsed as though the declarations were an expression.
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
func (p *parser) skipSpaceAndComments() {
	for {
		p.skipSpace()
		if !p.lookingAt("(:") {
			return
		}
		end, err := skipComment(p.src, p.pos)
		if err != nil {
			// Leave it: the expression parser will report it in context.
			return
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
	for _, kw := range []string{
		"xquery version", "module namespace", "declare ", "import ",
	} {
		if hasKeywordPrefix(p.src[p.pos:], kw) {
			return p.errorf(
				"XQST0031: the XQuery prolog is not implemented yet (%q)",
				strings.TrimSpace(kw))
		}
	}
	for _, kw := range []string{
		"switch", "typeswitch", "try ", "validate", "ordered", "unordered"} {
		if hasKeywordPrefix(p.src[p.pos:], kw) {
			return p.errorf(
				"XPST0003: %q is not implemented yet",
				strings.TrimSpace(kw))
		}
	}
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
