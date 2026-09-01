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
	if !mayConstruct(src) {
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

// mayConstruct reports whether src might contain a constructor, so that it is
// parsed here rather than handed straight to xpath.
//
// It is deliberately conservative in the direction that costs nothing: a false
// positive sends the text through this package's own body parser, which falls
// back to xpath for every item that is not a constructor, so the only cost is
// one extra pass. A false negative would hand a constructor to a parser that
// cannot read it, so the test errs towards yes.
func mayConstruct(src string) bool {
	if strings.ContainsAny(src, "<") {
		return true
	}
	for _, kw := range []string{"element", "attribute", "document", "text",
		"comment", "processing-instruction", "namespace"} {
		if i := strings.Index(src, kw); i >= 0 {
			rest := strings.TrimLeft(src[i+len(kw):], " \t\r\n")
			if strings.HasPrefix(rest, "{") {
				return true
			}
			// "element foo {": a name between the keyword and the brace.
			if j := strings.IndexAny(rest, "{("); j >= 0 && rest[j] == '{' &&
				strings.TrimSpace(rest[:j]) != "" {
				return true
			}
		}
	}
	return false
}

// parseFunctionDecl reads "declare [annotations] function name(params) [as
// type] ({ body } | external)".
func (p *parser) parseFunctionDecl() error {
	private, err := p.parseAnnotations()
	if err != nil {
		return err
	}
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
		if strings.TrimSpace(src) == "" {
			// An empty body is the empty sequence, which is legal and which
			// the expression parser has no spelling for.
			d.body = nil
			d.expr = nil
			break
		}
		if !mayConstruct(src) {
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
func (p *parser) parseAnnotations() (private bool, err error) {
	for {
		p.skipSpaceAndComments()
		if !p.consume("%") {
			return private, nil
		}
		prefix, local, err := p.parseEQName()
		if err != nil {
			return false, err
		}
		name, err := p.resolveDeclaredName(prefix, local, true)
		if err != nil {
			return false, err
		}
		switch {
		case name.URI == xdm.NSFN && (name.Local == "private" || name.Local == "public"):
			private = name.Local == "private"
		case name.URI == xdm.NSFN, name.URI == xdm.NSXS, name.URI == xdm.NSXSI,
			name.URI == xdm.NSXML:
			return false, p.errorf(
				"XQST0045: %q is a reserved namespace for an annotation", name.URI)
		}
		p.skipSpaceAndComments()
		// An annotation may carry a parenthesised list of literals, which
		// nothing here interprets.
		if p.consume("(") {
			depth := 1
			for !p.eof() && depth > 0 {
				switch p.src[p.pos] {
				case '\'', '"':
					end, err := skipString(p.src, p.pos)
					if err != nil {
						return false, err
					}
					p.pos = end
				case '(':
					depth++
				case ')':
					depth--
				}
				p.pos++
			}
		}
	}
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
		return expr.compiled.Eval(ctx)
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
	byName := make(map[string]*varDecl, len(q.vars))
	for _, d := range q.vars {
		byName[d.name.Clark()] = d
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
		for _, ref := range d.references() {
			if dep, ok := byName[ref]; ok && dep != d {
				if err := visit(dep); err != nil {
					return err
				}
			} else if ok {
				return fmt.Errorf(
					"XQST0054: the variable %s depends on itself",
					d.name.Lexical())
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

// evalVar produces one global variable's value.
//
// An external variable is bound by the caller: if the context already has a
// value under that name, that value wins over any default the declaration
// carries, and if it does not and there is no default, XPDY0002 says the
// query cannot run.
func (q *Query) evalVar(d *varDecl, ctx *xpath.Context) (xdm.Sequence, error) {
	if d.external {
		if v, ok := ctx.LookupVar(d.name); ok {
			return d.typ.convert(v, "the external variable $"+d.name.Lexical())
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
	// XPTY0004 for a value that does not match "as"; the conversion rules
	// apply, so an untypedAtomic is cast rather than refused.
	return d.typ.convert(v, "the variable $"+d.name.Lexical())
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
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	// Sorted so that a query with a cycle reports the same variable every
	// run: map iteration order would otherwise make the message vary.
	sort.Strings(out)
	return out
}

// scanVarRefs adds the Clark name of every variable reference in src to set.
//
// Names are recorded unresolved-then-resolved through the module's own
// prefixes at the call site, because a dependency only matters when it names
// a variable this module declares, and those are the only names in the map
// this feeds.
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
			if j > i+1 {
				set[src[i+1:j]] = true
			}
			i = j - 1
		}
	}
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
