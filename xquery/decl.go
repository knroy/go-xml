package xquery

import (
	"fmt"
	"sort"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xdmbuild"
	"github.com/knroy/go-xml/xpath"
)

// varDecl is one "declare variable" (§4.14).
//
// A declared variable is not a value but a recipe. It cannot be evaluated
// while the prolog is being parsed, because a variable is in scope throughout
// the module in either textual direction and its initialiser may name one
// declared later. It cannot be evaluated at the top of every query run
// either, in declaration order, for the same reason. So the initialisers are
// compiled here and run in dependency order at the start of evaluation.
type varDecl struct {
	name     xdm.QName
	typ      *sequenceType
	init     *compiledExpr
	external bool
	// body is set instead of init when the initialiser is a constructor,
	// which the expression parser cannot read; see parseDeclBody.
	body []node
}

// nsXQueryOptions is the namespace §4.16 reserves for option declarations and
// for the annotations the specification may define in future revisions. It has
// no constant in xdm because nothing outside the prolog names it.
const nsXQueryOptions = "http://www.w3.org/2012/xquery"

// funcDecl is one "declare function" (§4.15).
type funcDecl struct {
	name    xdm.QName
	params  []funcParam
	returns *sequenceType
	// body is the function's own body, parsed by this package because it may
	// be or contain a constructor. A nil body with external set is a function
	// the query declared and the host is expected to supply; there is no way
	// to supply one yet, so calling it is a dynamic error.
	body     []node
	expr     *compiledExpr
	external bool
	// private records the %private annotation. In a main module it has no
	// effect — there is nothing outside the module to hide from — and it is
	// kept so that a later module implementation has it.
	private bool
}

type funcParam struct {
	name xdm.QName
	typ  *sequenceType
}

// contextItemDecl is "declare context item" (§4.16).
type contextItemDecl struct {
	typ      *sequenceType
	init     *compiledExpr
	external bool
}

// parseVarDecl reads "declare variable $name [as type] (:= expr | external
// [:= expr])".
//
// The initialiser is compiled but not run. Two variables may not share a name
// — XQST0049 — which is checked here because both declarations are in this
// module and nothing later can change the answer.
func (p *parser) parseVarDecl() error {
	p.pos += len("variable")
	p.skipSpaceAndComments()
	if !p.consume("$") {
		return p.errorf("XPST0003: expected %q after %q", "$", "declare variable")
	}
	prefix, local, err := p.parseEQName()
	if err != nil {
		return err
	}
	name, err := p.resolveDeclaredName(prefix, local, false)
	if err != nil {
		return err
	}
	if err := checkReservedNamespace(name, "variable"); err != nil {
		return err
	}
	for _, prev := range p.vars {
		if prev.name.Clark() == name.Clark() {
			return p.errorf("XQST0049: the variable %q is declared twice",
				name.Lexical())
		}
	}
	d := &varDecl{name: name}
	p.skipSpaceAndComments()
	if p.consumeKeyword("as") {
		p.skipSpaceAndComments()
		if d.typ, err = p.parseSequenceType(); err != nil {
			return err
		}
		p.skipSpaceAndComments()
	}
	switch {
	case p.consume(":="):
		p.skipSpaceAndComments()
		if d.init, d.body, err = p.parseDeclBody(); err != nil {
			return err
		}
	case p.consumeKeyword("external"):
		d.external = true
		p.skipSpaceAndComments()
		if p.consume(":=") {
			// 3.0 lets an external variable carry a default, used when the
			// host supplies no value.
			p.skipSpaceAndComments()
			if d.init, d.body, err = p.parseDeclBody(); err != nil {
				return err
			}
		}
	default:
		return p.errorf(
			"XPST0003: expected %q or %q in a variable declaration",
			":=", "external")
	}
	p.vars = append(p.vars, d)
	return nil
}

// parseDeclBody parses the right-hand side of a variable declaration or the
// initialiser of a context item.
//
// A constructor cannot be handed to xpath, so an initialiser that is or
// contains one is parsed here and kept as a node list; anything else is one
// expression that xpath compiles. Which it is can be decided by looking at
// the text: only a constructor puts a "<" or a computed-constructor keyword
// where an expression starts.
func (p *parser) parseDeclBody() (*compiledExpr, []node, error) {
	src, err := p.scanDeclExpr()
	if err != nil {
		return nil, nil, err
	}
	if !needsXQueryParser(src) {
		c, err := p.compileExpr(src)
		return c, nil, err
	}
	sub := &parser{src: src, sc: p.sc, version: p.version,
		declaredNS: p.declaredNS, funcs: p.funcs, vars: p.vars}
	body, err := sub.parseBodyItems()
	if err != nil {
		return nil, nil, err
	}
	return nil, body, nil
}

// parseFunctionDecl reads "declare [annotations] function name(params) [as
// type] ({ body } | external)".
func (p *parser) parseFunctionDecl() error {
	private, conflict, err := p.parseAnnotationList()
	if err != nil {
		return err
	}
	if conflict {
		return p.errorf(
			"XQST0106: a function may carry only one of %s and %s",
			"%public", "%private")
	}
	return p.parseFunctionDeclBody(private)
}

// parseFunctionDeclBody reads a function declaration whose annotations have
// already been consumed, which is how the prolog handles an AnnotatedDecl: it
// must read the annotations before it can tell a variable declaration from a
// function one, and cannot then hand them back.
func (p *parser) parseFunctionDeclBody(private bool) error {
	p.skipSpaceAndComments()
	if !p.consumeKeyword("function") {
		return p.errorf("XPST0003: expected %q", "function")
	}
	p.skipSpaceAndComments()
	prefix, local, err := p.parseEQName()
	if err != nil {
		return err
	}
	// §4.15: an unprefixed declared function name takes the default function
	// namespace, and a function may not be declared in no namespace at all —
	// XQST0060 — because an unprefixed *call* would then be ambiguous with a
	// builtin.
	name, err := p.resolveDeclaredName(prefix, local, true)
	if err != nil {
		return err
	}
	if name.URI == "" {
		return p.errorf(
			"XQST0060: a declared function must be in a namespace (%q)", local)
	}
	if err := checkReservedNamespace(name, "function"); err != nil {
		return err
	}
	// XPST0003: a function may not be *declared* under a name the grammar
	// reserves, for the same reason it may not be *called* under one. The
	// declaration is not merely unreachable — "declare function element() {}"
	// cannot be parsed as a declaration at all, because "element(" begins a
	// kind test wherever it appears. The check is here rather than left to
	// xpath because a declared name never passes through the expression
	// parser, which is the only place that knows the list.
	if prefix == "" && isReservedFunctionName(local) {
		return p.errorf(
			"XPST0003: %q is reserved and may not name a function", local)
	}
	d := &funcDecl{name: name, private: private}
	p.skipSpaceAndComments()
	if !p.consume("(") {
		return p.errorf("XPST0003: expected %q after the function name", "(")
	}
	if d.params, err = p.parseParamList(); err != nil {
		return err
	}
	p.skipSpaceAndComments()
	if p.consumeKeyword("as") {
		p.skipSpaceAndComments()
		if d.returns, err = p.parseSequenceType(); err != nil {
			return err
		}
		p.skipSpaceAndComments()
	}
	switch {
	case p.lookingAt("{"):
		end, err := findEnclosed(p.src, p.pos)
		if err != nil {
			return err
		}
		src := p.src[p.pos+1 : end]
		p.pos = end + 1
		if isBlank(src) {
			// 3.1 made "{}" a legal function body meaning the empty sequence,
			// which the expression parser has no spelling for. A body holding
			// only a comment is the same thing — the comment is not content.
			d.body = nil
			d.expr = nil
			break
		}
		if !needsXQueryParser(src) {
			if d.expr, err = p.compileExpr(src); err != nil {
				return err
			}
			break
		}
		sub := &parser{src: src, sc: p.sc, version: p.version,
			declaredNS: p.declaredNS, funcs: p.funcs, vars: p.vars}
		if d.body, err = sub.parseBodyItems(); err != nil {
			return err
		}
	case p.consumeKeyword("external"):
		d.external = true
	default:
		return p.errorf("XPST0003: expected %q or %q for the function body",
			"{", "external")
	}

	// XQST0034: two functions may not share a name and arity. Unlike XSLT
	// there is no import precedence to break the tie, so any repeat is an
	// error.
	for _, prev := range p.funcs {
		if prev.name.Clark() == d.name.Clark() && len(prev.params) == len(d.params) {
			return p.errorf(
				"XQST0034: the function %s#%d is declared twice",
				d.name.Lexical(), len(d.params))
		}
	}
	p.funcs = append(p.funcs, d)
	return nil
}

// parseAnnotations reads the annotation list that may precede "function",
// reporting whether %private was among them.
//
// Only %public and %private are defined by the specification, and only for
// module scoping. An annotation in a namespace of the caller's own is legal
// and ignored; one in the fn: namespace or another reserved one is XQST0045.
//
// conflict reports that a declaration carried more than one of %public and
// %private, which §4.15 forbids. The rule is that at most *one* of the pair
// may appear, so %public %public breaks it as surely as %public %private:
// modules-pub-priv-31 and -32 repeat one annotation and are errors too.
//
// It is returned rather than raised here because the error code names the
// declaration: XQST0106 for a function and XQST0116 for a variable, and
// which one this is is not known until the keyword after the annotations has
// been read.
func (p *parser) parseAnnotations() (private bool, err error) {
	private, _, err = p.parseAnnotationList()
	return private, err
}

// parseAnnotationList is parseAnnotations plus the public/private conflict.
func (p *parser) parseAnnotationList() (private, conflict bool, err error) {
	scoping := 0
	for {
		p.skipSpaceAndComments()
		if !p.consume("%") {
			return private, scoping > 1, nil
		}
		// Annotation ::= "%" EQName ... — the grammar's whitespace rules let
		// anything separate the two, so "% Q{...}x" is one annotation and not
		// a syntax error.
		p.skipSpaceAndComments()
		prefix, local, err := p.parseEQName()
		if err != nil {
			return false, false, err
		}
		name, err := p.resolveDeclaredName(prefix, local, false)
		if err != nil {
			return false, false, err
		}
		if prefix == "" && !strings.HasPrefix(local, "Q{") {
			// §4.15: "if the QName is unprefixed, it is in the namespace
			// http://www.w3.org/2005/xpath-functions" — the *default function
			// namespace does not apply*, which annotation-28 is written to
			// prove: under "declare default function namespace
			// 'http://example.com'", %x is still fn:x and so XQST0045, not a
			// vendor annotation in example.com that would be ignored.
			name = xdm.QName{URI: xdm.NSFN, Local: local}
		}
		switch {
		case (name.URI == xdm.NSFN || name.URI == nsXQueryOptions) &&
			(name.Local == "private" || name.Local == "public"):
			// %public and %private are defined in both the fn: namespace,
			// where an unprefixed annotation lands, and in the option
			// namespace http://www.w3.org/2012/xquery, which is how
			// modules-pub-priv-30 writes the second of the pair. Both are
			// otherwise reserved by the case below, so this has to come
			// first or %xq:public is XQST0045 rather than the annotation it
			// is. Only these two locals escape: %xq:x stays reserved, which
			// annotation-26 and annotation-27 check.
			private = name.Local == "private"
			scoping++
		case name.URI == xdm.NSFN, name.URI == xdm.NSXS, name.URI == xdm.NSXSI,
			name.URI == xdm.NSXML, name.URI == xdm.NSMath,
			name.URI == xdm.NSArray, name.URI == xdm.NSMap,
			name.URI == nsXQueryOptions:
			// §4.15 reserves every predefined namespace for annotations, not
			// just the four the XPath data model needs: math, array, map and
			// the option namespace http://www.w3.org/2012/xquery stand on the
			// same footing as fn, xs, xsi and xml, so %math:x is XQST0045
			// rather than a vendor annotation that is legal and ignored.
			return false, false, p.errorf(
				"XQST0045: %q is a reserved namespace for an annotation", name.URI)
		}
		p.skipSpaceAndComments()
		// Annotation ::= "%" EQName ("(" Literal ("," Literal)* ")")?
		//
		// Nothing here interprets the values, but the list is parsed rather
		// than skipped because the grammar admits only literals: %eg:x(1+2)
		// is XPST0003, and bracket-counting accepted it along with any other
		// expression, which annotation-8 is written to catch.
		if p.consume("(") {
			for {
				p.skipSpaceAndComments()
				if err := p.skipAnnotationLiteral(); err != nil {
					return false, false, err
				}
				p.skipSpaceAndComments()
				if p.consume(",") {
					continue
				}
				if !p.consume(")") {
					return false, false, p.errorf(
						"XPST0003: expected %q or %q in an annotation", ",", ")")
				}
				break
			}
		}
	}
}

// skipAnnotationLiteral consumes one Literal — a string, or a number with an
// optional sign — and refuses anything else. It only has to recognise them,
// since an annotation's values are not interpreted.
func (p *parser) skipAnnotationLiteral() error {
	if p.eof() {
		return p.errorf("XPST0003: expected a literal in an annotation")
	}
	if c := p.src[p.pos]; c == '\'' || c == '"' {
		end, err := skipString(p.src, p.pos)
		if err != nil {
			return err
		}
		// skipString reports the index *of* the closing quote, not the one
		// past it.
		p.pos = end + 1
		return nil
	}
	start := p.pos
	if !p.eof() && (p.src[p.pos] == '-' || p.src[p.pos] == '+') {
		p.pos++
	}
	for !p.eof() {
		c := p.src[p.pos]
		// One pass takes the digits, the decimal point and an exponent with
		// its own sign, which together spell every numeric literal.
		if c >= '0' && c <= '9' || c == '.' || c == 'e' || c == 'E' ||
			((c == '-' || c == '+') && p.pos > start &&
				(p.src[p.pos-1] == 'e' || p.src[p.pos-1] == 'E')) {
			p.pos++
			continue
		}
		break
	}
	if p.pos == start {
		return p.errorf("XPST0003: expected a literal in an annotation")
	}
	return nil
}

// parseParamList reads a function's parameter list, with p.pos just past the
// opening parenthesis.
func (p *parser) parseParamList() ([]funcParam, error) {
	var out []funcParam
	p.skipSpaceAndComments()
	if p.consume(")") {
		return nil, nil
	}
	for {
		p.skipSpaceAndComments()
		if !p.consume("$") {
			return nil, p.errorf("XPST0003: expected %q before a parameter name", "$")
		}
		prefix, local, err := p.parseEQName()
		if err != nil {
			return nil, err
		}
		name, err := p.resolveDeclaredName(prefix, local, false)
		if err != nil {
			return nil, err
		}
		// XQST0039: two parameters of one function may not share a name,
		// because the second binding would silently shadow the first.
		for _, prev := range out {
			if prev.name.Clark() == name.Clark() {
				return nil, p.errorf(
					"XQST0039: the parameter %q appears twice", name.Lexical())
			}
		}
		param := funcParam{name: name}
		p.skipSpaceAndComments()
		if p.consumeKeyword("as") {
			p.skipSpaceAndComments()
			if param.typ, err = p.parseSequenceType(); err != nil {
				return nil, err
			}
			p.skipSpaceAndComments()
		}
		out = append(out, param)
		if p.consume(",") {
			continue
		}
		if p.consume(")") {
			return out, nil
		}
		return nil, p.errorf("XPST0003: expected %q or %q in a parameter list",
			",", ")")
	}
}

// isReservedFunctionName reports whether an unprefixed name is one the
// grammar reserves, at any version.
//
// These are the kind tests, the sequence-type keywords and the three
// expressions whose keyword is followed by a parenthesis. All of them are
// unprefixed by construction: "my:element" is an ordinary name, because a
// prefix puts it somewhere the grammar's own keywords cannot be.
func isReservedFunctionName(name string) bool {
	switch name {
	case "attribute", "comment", "document-node", "element", "empty-sequence",
		"function", "if", "item", "map", "array", "namespace-node", "node",
		"processing-instruction", "schema-attribute", "schema-element",
		"switch", "text", "typeswitch":
		return true
	}
	return false
}

// checkReservedNamespace refuses a declaration in a namespace §4.15 reserves.
//
// Declaring fn:count or xs:integer would shadow a builtin or a constructor
// function with something the specification says is not the query's to
// define. XQST0045 is the code for both a function and a variable.
func checkReservedNamespace(name xdm.QName, what string) error {
	switch name.URI {
	case xdm.NSFN, xdm.NSXS, xdm.NSXSI, xdm.NSXML,
		"http://www.w3.org/2005/xpath-functions/math",
		"http://www.w3.org/2005/xpath-functions/array",
		"http://www.w3.org/2005/xpath-functions/map":
		return fmt.Errorf("XQST0045: a %s may not be declared in %q",
			what, name.URI)
	}
	return nil
}

// isBlank reports whether src holds nothing but whitespace and comments,
// which is the empty sequence rather than a missing expression.
func isBlank(src string) bool {
	p := &parser{src: src}
	p.skipSpaceAndComments()
	return p.eof()
}

// parseBodyItems parses a whole expression body — the comma-separated items a
// query body or a function body holds — over this parser's whole source.
func (p *parser) parseBodyItems() ([]node, error) {
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

// --- Evaluation -------------------------------------------------------------

// register turns the declared functions into an xpath.Library chained to the
// builtins, so that a call written in any expression of the query — including
// one inside another declared function — resolves to it.
//
// Chaining rather than copying is what makes a declared function able to
// shadow nothing and extend everything: Lookup falls through to the parent for
// every name the query did not declare, which is all 437 builtins.
func (q *Query) registerFunctions(parent xpath.FunctionLibrary) *xpath.Library {
	if parent == nil {
		parent = xpath.Builtins()
	}
	lib := xpath.NewLibrary(parent)
	q.registerFormatNumber(lib)
	for _, d := range q.funcs {
		lib.Add(xpath.Function{
			Name:      d.name,
			Arity:     len(d.params),
			Call:      q.callDeclared(d),
			Signature: declaredSignature(d),
		})
	}
	return lib
}

// declaredSignature records the declared types so that a typed function test
// — "local:f#1 instance of function(xs:integer) as xs:integer" — has something
// to judge. An undeclared type is item()*, which is what the specification
// says a parameter with no "as" has.
func declaredSignature(d *funcDecl) []string {
	sig := make([]string, 0, len(d.params)+1)
	sig = append(sig, typeSource(d.returns))
	for _, pm := range d.params {
		sig = append(sig, typeSource(pm.typ))
	}
	return sig
}

func typeSource(t *sequenceType) string {
	if t == nil {
		return "item()*"
	}
	return t.src
}

// callDeclared builds the xpath.Function body for a declared function.
//
// The arguments arrive already evaluated, which is what an XQuery function
// call means — there is no lazy evaluation to preserve — so the work is to
// convert each to its declared type, bind it, run the body and convert the
// result.
//
// The context the body runs in deliberately has no context item. §4.15: "the
// static context for the function body does not include a context item", so a
// function whose body writes "." must fail rather than see the caller's. That
// is what distinguishes a declared function from a macro.
func (q *Query) callDeclared(d *funcDecl) func(*xpath.Context, []xdm.Sequence) (xdm.Sequence, error) {
	return func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
		if d.external {
			return nil, fmt.Errorf(
				"XPST0017: the external function %s#%d has no implementation",
				d.name.Lexical(), len(d.params))
		}
		if ctx.Depth > xpath.MaxDepth {
			return nil, fmt.Errorf(
				"FOAR0002: recursion too deep in %s", d.name.Lexical())
		}
		sub := *ctx
		sub.Depth = ctx.Depth + 1
		// The function's own scope: no context item, and only its parameters
		// plus the module's global variables in scope. Clearing the item is
		// the §4.15 rule above; clearing Position and Size with it keeps
		// fn:position() from reading a value the body has no business seeing.
		sub.Item = nil
		sub.Position, sub.Size = 0, 0
		inner := &sub
		for i, pm := range d.params {
			var v xdm.Sequence
			if i < len(args) {
				v = args[i]
			}
			conv, err := pm.typ.convert(v, fmt.Sprintf(
				"parameter $%s of %s", pm.name.Lexical(), d.name.Lexical()))
			if err != nil {
				return nil, err
			}
			inner = inner.WithVar(pm.name, conv)
		}
		out, err := q.evalBody(d.body, d.expr, inner)
		if err != nil {
			return nil, err
		}
		// XPTY0004 is the code for a result that does not match the declared
		// return type; the conversion rules apply, so an xs:integer returned
		// where xs:double is declared is promoted rather than refused.
		return d.returns.convert(out, "the result of "+d.name.Lexical())
	}
}

// evalBody runs either a compiled expression or a parsed node list, which are
// the two shapes a function body and a variable initialiser can have.
func (q *Query) evalBody(body []node, expr *compiledExpr, ctx *xpath.Context) (xdm.Sequence, error) {
	if expr != nil {
		// Through bind, for the reason enclosed.sequence does the same: an
		// XQuery-only primary compileExpr lifted out of a function body or a
		// variable initialiser is bound to a variable that only bind knows
		// about.
		xp, err := expr.bind(&evalContext{xp: ctx, sc: q.sc})
		if err != nil {
			return nil, err
		}
		return expr.compiled.Eval(xp)
	}
	if body == nil {
		return nil, nil
	}
	out := xdmbuild.New(policy{sc: q.sc})
	ref := &builderRef{b: out}
	ec := &evalContext{xp: ctx, sc: q.sc}
	for _, n := range body {
		if err := n.eval(ref, ec); err != nil {
			return nil, err
		}
	}
	return out.Sequence(), nil
}

// bindVariables evaluates the module's global variables and binds them.
//
// The order is the one §4.14 forces and no other: a variable's initialiser may
// name a variable declared after it, so the declarations form a dependency
// graph that has to be walked rather than a list that can be run down. A cycle
// in that graph is XQST0054, which is a *static* error even though it is found
// here, because it is a property of the text.
//
// The dependencies are read off the compiled initialisers by name. An
// initialiser that names a variable the module does not declare is not a
// dependency at all — it is a reference to one the caller supplied, or an
// error the expression itself will raise.
func (q *Query) bindVariables(ctx *xpath.Context) (*xpath.Context, error) {
	if len(q.vars) == 0 {
		return ctx, nil
	}
	// Keyed by the name *as it would be written*, because the dependency scan
	// is lexical and has no static context to resolve a prefix with. Within
	// one module the prefix identifies the namespace exactly — XQST0033
	// forbids binding one twice — so this is as precise as the Clark name
	// would be, and it is what the scan can produce.
	byName := make(map[string]*varDecl, len(q.vars))
	for _, d := range q.vars {
		byName[d.name.Lexical()] = d
		if d.name.Prefix == "" && d.name.URI != "" {
			// A name written "Q{uri}local" has no prefix to be found under,
			// so its local name is registered as well: a reference to it must
			// carry that prefix, and there is none to carry.
			byName[d.name.Local] = d
		}
	}

	// state is per-variable: 0 unvisited, 1 in progress, 2 done. The
	// in-progress mark is what detects the cycle, and it has to be a third
	// state rather than a boolean, because reaching a *finished* variable
	// twice is ordinary sharing and reaching an unfinished one is the error.
	const (
		unvisited = iota
		inProgress
		done
	)
	state := make(map[string]int, len(q.vars))
	out := ctx

	var visit func(d *varDecl) error
	visit = func(d *varDecl) error {
		key := d.name.Clark()
		switch state[key] {
		case done:
			return nil
		case inProgress:
			return fmt.Errorf(
				"XQST0054: the variable %s depends on itself", d.name.Lexical())
		}
		state[key] = inProgress
		// The dependency graph runs through functions as well as variables:
		// "declare variable $v := f(); declare function f() { $v };" is
		// XQST0054 even though no variable names another. Every variable a
		// declared function can *transitively* reach is therefore a
		// dependency of anything that calls it, which is what reachableVars
		// computes.
		for _, ref := range q.reachableVars(d.references(), d.calls()) {
			if dep, ok := byName[ref]; ok {
				if dep == d {
					return fmt.Errorf(
						"XQST0054: the variable %s depends on itself",
						d.name.Lexical())
				}
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		v, err := q.evalVar(d, out)
		if err != nil {
			return err
		}
		out = out.WithVar(d.name, v)
		state[key] = done
		return nil
	}

	// The declarations are visited in source order so that an error is
	// reported for the first one that has it, which is what a reader expects.
	for _, d := range q.vars {
		if err := visit(d); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// reachableVars closes the dependency set over the declared functions.
//
// A variable's initialiser names some variables directly and calls some
// functions; each of those functions names further variables and calls
// further functions, and so on. The specification's rule is the transitive
// one — XQST0054 fires on a cycle in the graph of variables *and* functions
// together — so the closure is taken rather than only the direct edges.
//
// Function recursion is not itself an error, so a function already visited is
// simply not re-entered: only a variable that reaches itself is a cycle worth
// reporting, and bindVariables makes that judgement.
func (q *Query) reachableVars(vars, calls []string) []string {
	seenFn := map[string]bool{}
	out := map[string]bool{}
	for _, v := range vars {
		out[v] = true
	}
	queue := append([]string(nil), calls...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seenFn[name] {
			continue
		}
		seenFn[name] = true
		for _, fd := range q.funcs {
			if fd.name.Local != localOf(name) || !sameFuncName(fd.name, name) {
				continue
			}
			for _, v := range fd.references() {
				out[v] = true
			}
			queue = append(queue, fd.calls()...)
		}
	}
	res := make([]string, 0, len(out))
	for k := range out {
		res = append(res, k)
	}
	sort.Strings(res)
	return res
}

// sameFuncName matches a declared function against a call written in the
// source, which carries the prefix rather than the resolved URI.
//
// Comparing on the lexical prefix is deliberate: the scan that produced the
// call names has no static context to resolve them with, and a module's own
// prefixes are one-to-one with its URIs by XQST0033, so the prefix identifies
// the namespace exactly as well within one module.
func sameFuncName(declared xdm.QName, called string) bool {
	if i := strings.IndexByte(called, ':'); i >= 0 {
		return declared.Prefix == called[:i] && declared.Local == called[i+1:]
	}
	return declared.Prefix == "" && declared.Local == called
}

func localOf(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// calls returns the names of the functions an initialiser or body calls, as
// written.
//
// Like references, this is lexical, and for the same reason: it feeds a graph
// whose only question is which of *this module's* declarations are reachable,
// and a name that matches none of them is discarded.
func (d *varDecl) calls() []string {
	set := map[string]bool{}
	if d.init != nil {
		scanCalls(d.init.src, set)
	}
	for _, n := range d.body {
		collectNodeCalls(n, set)
	}
	return sortedKeys(set)
}

func (d *funcDecl) references() []string {
	set := map[string]bool{}
	if d.expr != nil {
		scanVarRefs(d.expr.src, set)
	}
	for _, n := range d.body {
		collectNodeSources(n, set)
	}
	// A parameter shadows a global of the same name, so a body that only
	// names its own parameters depends on nothing.
	for _, pm := range d.params {
		delete(set, pm.name.Lexical())
		delete(set, pm.name.Local)
	}
	return sortedKeys(set)
}

func (d *funcDecl) calls() []string {
	set := map[string]bool{}
	if d.expr != nil {
		scanCalls(d.expr.src, set)
	}
	for _, n := range d.body {
		collectNodeCalls(n, set)
	}
	return sortedKeys(set)
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// scanCalls adds the name of every function call in src to set.
//
// A call is a name immediately followed by "(", with strings and comments
// skipped as everywhere else. The space before the parenthesis is not
// permitted by the grammar for a function call, which is what keeps "if (" and
// the kind tests out of the set without listing them.
func scanCalls(src string, set map[string]bool) {
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\'', '"':
			end, err := skipString(src, i)
			if err != nil {
				return
			}
			i = end
		case '(':
			if i+1 < len(src) && src[i+1] == ':' {
				end, err := skipComment(src, i)
				if err != nil {
					return
				}
				i = end
				continue
			}
			j := i
			for j > 0 && (isNameByte(src[j-1]) || src[j-1] == ':') {
				j--
			}
			if j < i {
				set[src[j:i]] = true
			}
		}
	}
}

// collectNodeCalls is collectNodeSources for function calls.
func collectNodeCalls(n node, set map[string]bool) {
	each := func(exprs []*compiledExpr, kids ...[]node) {
		for _, e := range exprs {
			if e != nil {
				scanCalls(e.src, set)
			}
		}
		for _, ks := range kids {
			for _, k := range ks {
				collectNodeCalls(k, set)
			}
		}
	}
	switch v := n.(type) {
	case *enclosed:
		each([]*compiledExpr{v.expr}, v.items)
	case *element:
		exprs := []*compiledExpr{v.nameExpr}
		kids := [][]node{v.content}
		for i := range v.attrs {
			exprs = append(exprs, v.attrs[i].nameExpr)
			kids = append(kids, v.attrs[i].value)
		}
		each(exprs, kids...)
	case *comment:
		each(nil, v.content)
	case *pi:
		each([]*compiledExpr{v.targetExpr}, v.content)
	case *textNode:
		each(nil, v.content)
	case *document:
		each(nil, v.content)
	}
}

// evalVar produces one global variable's value.
//
// An external variable is bound by the caller: if the context already has a
// value under that name, that value wins over any default the declaration
// carries, and if it does not and there is no default, XPDY0002 says the
// query cannot run.
func (q *Query) evalVar(d *varDecl, ctx *xpath.Context) (xdm.Sequence, error) {
	if d.external {
		if v, ok := ctx.LookupVar(d.name); ok {
			return d.typ.match(v, "the external variable $"+d.name.Lexical())
		}
		if d.init == nil && d.body == nil {
			return nil, fmt.Errorf(
				"XPDY0002: no value was supplied for the external variable $%s",
				d.name.Lexical())
		}
	}
	v, err := q.evalBody(d.body, d.init, ctx)
	if err != nil {
		return nil, err
	}
	// XPTY0004 for a value that does not match "as". A variable declaration
	// matches rather than converts; see sequenceType.match.
	return d.typ.match(v, "the variable $"+d.name.Lexical())
}

// references returns the Clark names of the variables an initialiser mentions.
//
// It is read off the *source* rather than the compiled AST, deliberately. The
// xpath package exposes its AST but not a generic walk over it, and a walker
// written here would be a second copy of a forty-case type switch that has to
// be kept in step with a package this one must not couple itself to; a node
// kind it failed to descend into would silently drop a dependency, which is
// the one failure mode that produces a wrong answer rather than an error.
//
// A lexical scan errs the other way. It skips string literals and comments —
// so "$x" written inside a string is not a dependency, which is what makes a
// spurious XQST0054 impossible — and otherwise over-approximates: a "$x" in a
// range variable's own scope is counted, which only forces an ordering that
// was already legal. Over-approximating orders more than it must; it never
// initialises a variable too late.
func (d *varDecl) references() []string {
	set := map[string]bool{}
	if d.init != nil {
		scanVarRefs(d.init.src, set)
	}
	for _, n := range d.body {
		collectNodeSources(n, set)
	}
	// Sorted so that a query with a cycle reports the same variable every
	// run: map iteration order would otherwise make the message vary.
	return sortedKeys(set)
}

// scanVarRefs adds the name of every variable reference in src to set.
//
// A name in *binding* position is deliberately excluded. "declare variable
// $x := let $x := 1 return 1;" is legal and has no cycle: the inner $x is a
// new variable that shadows the global, and the initialiser does not depend
// on the global at all. Counting it would turn a legal query into XQST0054,
// which is the one direction over-approximation is not free in — so the scan
// looks back at the keyword before each "$" and skips the ones that introduce
// a binding rather than read one.
//
// Over-approximating the other way stays harmless: a "$x" that is a genuine
// reference but shadowed produces an ordering constraint that was already
// satisfiable.
func scanVarRefs(src string, set map[string]bool) {
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\'', '"':
			end, err := skipString(src, i)
			if err != nil {
				return
			}
			i = end
		case '(':
			if i+1 < len(src) && src[i+1] == ':' {
				end, err := skipComment(src, i)
				if err != nil {
					return
				}
				i = end
			}
		case '$':
			j := i + 1
			for j < len(src) && (isNameByte(src[j]) || src[j] == ':') {
				j++
			}
			if j > i+1 && !bindsVariable(src[:i]) {
				set[src[i+1:j]] = true
			}
			i = j - 1
		}
	}
}

// bindsVariable reports whether the text immediately before a "$" is a
// keyword that introduces a binding for the name that follows.
//
// These are the six places XQuery and XPath bind a variable by name: "for",
// "let", "some", "every", "count" and "group by"'s "by". A "$" after any
// other token — after "return", after an operator, at the start of an
// expression — is a reference.
func bindsVariable(before string) bool {
	before = strings.TrimRight(before, " \t\r\n")
	for _, kw := range []string{"for", "let", "some", "every", "count", "by"} {
		if !strings.HasSuffix(before, kw) {
			continue
		}
		// The keyword must be a whole one: "myfor $x" binds nothing, and
		// neither does a name ending in "by".
		rest := before[:len(before)-len(kw)]
		if rest == "" {
			return true
		}
		if r := rest[len(rest)-1]; !isNameByte(r) {
			return true
		}
	}
	// A comma continues a binding list: "for $a in x, $b in y" binds $b too.
	// Only inside one, which this cannot see — so it is admitted whenever
	// some binding keyword appears anywhere earlier, which over-excludes only
	// names that are also bound somewhere in the same expression.
	if strings.HasSuffix(before, ",") {
		return bindsSomewhere(before)
	}
	return false
}

// bindsSomewhere reports whether src contains a binding keyword at all,
// which is what makes a "," before a "$" possibly a binding-list separator.
func bindsSomewhere(src string) bool {
	for _, kw := range []string{"for ", "let ", "for$", "let$",
		"some ", "every ", "count "} {
		if strings.Contains(src, kw) {
			return true
		}
	}
	return false
}

// collectNodeSources gathers the variable references of every expression
// inside a parsed constructor, which is where a node-shaped initialiser keeps
// them.
func collectNodeSources(n node, set map[string]bool) {
	switch v := n.(type) {
	case *enclosed:
		if v.expr != nil {
			scanVarRefs(v.expr.src, set)
		}
		for _, it := range v.items {
			collectNodeSources(it, set)
		}
	case *element:
		if v.nameExpr != nil {
			scanVarRefs(v.nameExpr.src, set)
		}
		for i := range v.attrs {
			if v.attrs[i].nameExpr != nil {
				scanVarRefs(v.attrs[i].nameExpr.src, set)
			}
			for _, part := range v.attrs[i].value {
				collectNodeSources(part, set)
			}
		}
		for _, c := range v.content {
			collectNodeSources(c, set)
		}
	case *comment:
		for _, c := range v.content {
			collectNodeSources(c, set)
		}
	case *pi:
		if v.targetExpr != nil {
			scanVarRefs(v.targetExpr.src, set)
		}
		for _, c := range v.content {
			collectNodeSources(c, set)
		}
	case *textNode:
		for _, c := range v.content {
			collectNodeSources(c, set)
		}
	case *document:
		for _, c := range v.content {
			collectNodeSources(c, set)
		}
	}
}

// stripAnnotations removes the annotations an expression may carry, having
// first checked each one.
//
// §3.1.7 admits an Annotation before an InlineFunctionExpr's "function", and
// §2.5.5.4 admits a list of them before a FunctionTest's — "%eg:x function(*)"
// is a function test that further narrows what it matches. Neither is XPath
// syntax: XPath has no annotations at all, so an expression carrying one
// cannot be handed over as it stands.
//
// They are removed rather than passed on because their only effect is the one
// this processor does not have. §4.15 leaves the meaning of every annotation
// but %public and %private implementation-defined, and the specification says
// of an annotation assertion only that it "can only further restrict the set
// of functions matched"; restricting nothing is a conforming choice, and it is
// the one the suite's annotation-assertion cases are written to allow (they
// assert false, which the arity and type parts of the test already give).
// %public and %private are scoping, which a main module has no use for, and
// mixing them in an assertion list is explicitly permitted.
//
// What is not implementation-defined is which names may be used at all, so
// each annotation is still parsed and checked: a reserved namespace is
// XQST0045 and a non-literal argument is XPST0003, exactly as in the prolog.
func (p *parser) stripAnnotations(src string) (string, error) {
	if !strings.Contains(src, "%") {
		return src, nil
	}
	var out strings.Builder
	copied := 0
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '(':
			if i+1 < len(src) && src[i+1] == ':' {
				end, err := skipComment(src, i)
				if err != nil {
					// Let the expression parser report it in its own words.
					return src, nil
				}
				i = end
			}
		case '\'', '"':
			end, err := skipString(src, i)
			if err != nil {
				return src, nil
			}
			i = end
		case '%':
			// A sub-parser gives the annotation grammar, the name resolution
			// and the reserved-namespace check from the one implementation
			// that already has them. It reads the whole run of annotations,
			// so "%eg:x %eg:y function(*)" costs one call.
			sub := &parser{src: src, pos: i, sc: p.sc, version: p.version}
			if _, err := sub.parseAnnotations(); err != nil {
				return "", err
			}
			out.WriteString(src[copied:i])
			// A space stands in for what was removed: "%eg:x(1)function(*)"
			// is legal and the two names must not run together.
			out.WriteByte(' ')
			copied = sub.pos
			i = sub.pos - 1
		}
	}
	if copied == 0 {
		return src, nil
	}
	out.WriteString(src[copied:])
	return out.String(), nil
}
