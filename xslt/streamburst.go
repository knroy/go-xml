package xslt

// The rules the burst plans were made of, which the streamed core
// (streamexec.go) and the split (streamsplit.go) still use.
//
// The burst plans themselves -- one xsl:for-each or xsl:iterate over a record
// path, or one value-of, copy-of or sequence whose select reads one, run
// record by record through xdm.ParseBursts -- were retired into the core at
// M1.6: the core takes every container they took, and re-runs any that fails
// on the tree. What is left here:
//
//   - burstBody and the checks under it (burstSafe, pathSafe, callSafe,
//     avtSafe, iterateParamOK, carriedAtomic): whether a loop body reads of
//     its item only what a copy of the item, under shallow copies of its
//     ancestors, has -- no root, no sibling, following or preceding axis, an
//     ancestor only for its name or attributes, no node identity, no last(),
//     nothing but atomic values carried from one item to the next. The core
//     runs such a body on a grounded copy of each match (matchGround).
//   - the consumer tables (burstConsumers, burstAtomizers, burstOps) and the
//     small expression predicates the split's checks are built from;
//   - the interception hooks: burstFeed, which drives a consuming
//     instruction from the cursor (burstsFor, evalSelect), and burstResolver,
//     the resolvers that can stream a document (FileResolver).

import (
	"io"
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// noBursts turns streamed execution off, and traceBursts, when set, is told
// of each streamed run; both are for the tests that compare the two paths.
var (
	noBursts    bool
	traceBursts func(href string)
	// traceBurstFallback is told of each select that gives up on the
	// cursor and is evaluated on the tree instead.
	traceBurstFallback func(err error)
)

// burstFeed drives a consuming instruction from the stream: read calls each
// with every item the instruction's select would have returned, in order
// (a loop, a streamed copy-of), or eval returns its select's value.
type burstFeed struct {
	instr Instruction
	read  func(each func(xdm.Item) error) error
	eval  func(rt *runtime) (xdm.Sequence, error)
	// run, on a feed with no instr, is the streamed core's run, which
	// makes the feeds of its consuming instructions (streamexec.go).
	run *streamRun
	// apply, with run, drives an xsl:apply-templates, xsl:next-match or
	// xsl:apply-imports from the cursor (streamapply.go); group an
	// xsl:for-each-group (streamgroup.go).
	apply, group *streamOp
	// readOut, when set, is read for an instruction that writes what it
	// copies to out (xsl:copy-of, xsl:sequence; streamcopy_l3.go).
	readOut func(out *outputBuilder, each func(xdm.Item) error) error
	// treeApply drives an xsl:apply-templates over fresh copies: eval
	// reads them, the tree applies (streamcopy_l3apply.go).
	treeApply bool
	// keep, on a treeApply feed, is its run: while the copies are read and
	// applied, its observer keeps their originals' accumulator values
	// (streamaccum_l6.go), as executeCopies does.
	keep *streamRun
	// offBody, for an xsl:for-each-group whose population eval builds, says
	// its grouping and body run off the stream (streamgroup_pruned.go).
	offBody bool
	// jsonOut, for an xsl:value-of whose select is xml-to-json(.) alone,
	// writes the JSON text into out as it is made (streamfn_json.go); done
	// false when out cannot take it, nothing done.
	jsonOut func(rt *runtime, out *outputBuilder) (done bool, err error)
	// size, set by a sized feed's read before it hands its first item, is
	// the context size of an xsl:for-each or xsl:iterate it drives
	// (streamgroup_copies_mm7.go).
	size int
}

// burstResolver is a document resolver that can stream a document.
type burstResolver interface {
	// wantsBursts reports whether uri should be streamed rather than
	// resolved to a tree -- false when the resolver already holds the tree,
	// which fn:doc must go on returning -- and the document URI the tree
	// would have had.
	wantsBursts(uri, base string) (docURI string, ok bool)
	// streamDoc reads the document uri names on a cursor fn reads, with the
	// options and limits ResolveDocument would read it with (streamexec.go).
	streamDoc(ctx *xpath.Context, uri, base string, fn func(*xdmstream.Cursor) error) error
}

// burstVerdict reports whether the §19.8 verdict on sd is one execution may
// trust: known, unverified clear, and grounded (streamplan.go).
func burstVerdict(sd *xdm.Node, shared bool) bool {
	return buildStreamPlan(nil, sd, shared).trusted()
}

// endsInAttribute reports whether the path p ends in a step on the
// attribute axis.
func endsInAttribute(p xpath.Expr) bool {
	var last xpath.Expr = p
	if pe, ok := p.(*xpath.PathExpr); ok {
		last = pe.Steps[len(pe.Steps)-1]
	}
	st, ok := last.(*xpath.Step)
	return ok && st.Axis == xpath.AxisAttribute
}

// burstConsumers take their arguments whole and observe of a node no more
// than burstSafe allows.
var (
	burstConsumers = map[string]bool{
		"count": true, "sum": true, "avg": true, "min": true, "max": true,
		"exists": true, "empty": true, "boolean": true, "not": true,
		"head": true, "tail": true, "exactly-one": true, "zero-or-one": true,
		"one-or-more": true, "deep-equal": true, "data": true, "copy-of": true,
		"string": true, "number": true, "string-length": true,
		"normalize-space": true, "string-join": true, "concat": true,
		"contains": true, "starts-with": true, "ends-with": true,
		"matches": true, "compare": true, "upper-case": true, "lower-case": true,
		"substring": true, "substring-before": true, "substring-after": true,
		"translate": true, "tokenize": true, "round": true, "floor": true,
		"ceiling": true, "abs": true, "round-half-to-even": true, "replace": true,
		"format-number": true, "subsequence": true, "insert-before": true,
		"remove": true, "index-of": true, "unordered": true, "name": true, "local-name": true,
		"namespace-uri": true, "node-name": true,
	}
	// burstAtomizers turn every node of their arguments into its typed
	// value before using it (count, exists and empty use one item per
	// node, which an atomized untyped node still is), and return no node.
	burstAtomizers = map[string]bool{
		"count": true, "exists": true, "empty": true, "sum": true, "avg": true,
		"min": true, "max": true, "data": true, "string": true, "number": true,
		"string-length": true, "normalize-space": true, "string-join": true,
		"concat": true, "contains": true, "starts-with": true, "ends-with": true,
		"matches": true, "compare": true, "upper-case": true, "lower-case": true,
		"substring": true, "substring-before": true, "substring-after": true,
		"translate": true, "tokenize": true, "round": true, "floor": true,
		"ceiling": true, "abs": true, "round-half-to-even": true, "replace": true,
		"format-number": true, "index-of": true,
	}
	burstOps = map[string]bool{
		"=": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true,
		"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true,
		"+": true, "-": true, "*": true, "div": true, "idiv": true, "mod": true,
		"and": true, "or": true,
	}
)

// outerInner reports whether f is fn:outermost or fn:innermost.
func outerInner(f *xpath.FuncCall) bool {
	return f.Name.URI == fnNS && len(f.Args) == 1 &&
		(f.Name.Local == "outermost" || f.Name.Local == "innermost")
}

// pathTail reports whether steps stay within the nodes they start from:
// down or to attributes, with any predicates burstSafe allows, and perhaps
// one last expression that makes values (tailValue).
func pathTail(steps []xpath.Expr) bool {
	for j, s := range steps {
		st, ok := s.(*xpath.Step)
		if !ok {
			if j != len(steps)-1 || !tailValue(s) {
				return false
			}
			continue
		}
		switch st.Axis {
		case xpath.AxisChild, xpath.AxisAttribute, xpath.AxisSelf,
			xpath.AxisDescendant, xpath.AxisDescendantOrSelf:
		default:
			return false
		}
		if !allBurstSafe(st.Predicates, false) {
			return false
		}
	}
	return true
}

func firstOf(es []xpath.Expr) xpath.Expr {
	if len(es) == 0 {
		return nil
	}
	return es[0]
}

// tailValue reports whether x, the last step of a record path, makes items
// that do not depend on the tree its context node is in: atomic values, or
// copies. Its focus is each node the path has reached, so position() would
// count across the whole path.
func tailValue(x xpath.Expr) bool {
	switch y := x.(type) {
	case *xpath.FilterExpr:
		return tailValue(y.Base) && allBurstSafe(y.Predicates, false)
	case *xpath.FuncCall:
		if y.Name.URI == fnNS && y.Name.Local == "copy-of" && len(y.Args) == 0 {
			return true
		}
	}
	return atomicValued(x) && burstSafe(x, true)
}

// onlyElementChild is el's one element child, when el has exactly one and
// nothing else but white space.
func onlyElementChild(el *xdm.Node) *xdm.Node {
	var only *xdm.Node
	for ch := range el.Children() {
		switch ch.Kind() {
		case xdm.KindElement:
			if only != nil {
				return nil
			}
			only = ch
		case xdm.KindText:
			if strings.TrimSpace(ch.Value()) != "" {
				return nil
			}
		case xdm.KindComment, xdm.KindPI:
		default:
			return nil
		}
	}
	return only
}

// lreAttrOK admits the xsl: attributes of a literal result element that do
// not change what it constructs.
func lreAttrOK(a *xdm.Node) bool {
	if a.Name().URI != xdm.NSXSL {
		return true
	}
	switch a.Name().Local {
	case "exclude-result-prefixes", "extension-element-prefixes", "version",
		"xpath-default-namespace", "expand-text", "default-collation", "use-when":
		return true
	}
	return false
}

// parseAt parses src as an expression written on el, or returns nil.
func parseAt(el *xdm.Node, src string) xpath.Expr {
	if src == "" {
		return nil
	}
	e, err := xpath.ParseVersion(src, newNSResolver(el, xpathDefaultNamespace(el)), xpathVersionAt(el))
	if err != nil {
		return nil
	}
	return mapCalls(e, el) // streamfork.go
}

// booleanShaped reports whether a predicate is certainly not numeric, so not
// positional: a comparison, a boolean connective or function, or a path.
func booleanShaped(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.BinaryOp:
		switch x.Op {
		case "=", "!=", "<", "<=", ">", ">=", "eq", "ne", "lt", "le", "gt", "ge", "and", "or":
			return true
		}
	case *xpath.FuncCall:
		if x.Name.URI == fnNS {
			switch x.Name.Local {
			case "not", "boolean", "exists", "empty", "true", "false",
				"contains", "starts-with", "ends-with", "matches":
				return true
			}
		}
	case *xpath.Step:
		return true
	case *xpath.PathExpr:
		// A path's last step can be any expression, a number among them
		// (@k/xs:integer(.)), which would make the predicate positional.
		if len(x.Steps) == 0 {
			return true // "/"
		}
		_, ok := x.Steps[len(x.Steps)-1].(*xpath.Step)
		return ok
	}
	return false
}

// burstBody reports whether everything inside the loop element is something
// bursts execute exactly as the whole tree would.
func burstBody(target *xdm.Node) bool {
	ok := true
	walkElements(target, func(el *xdm.Node) bool {
		if !ok {
			return false
		}
		if !burstElement(el, target) {
			ok = false
			return false
		}
		for ch := range el.Children() {
			// A text value template would need its own check; a text node
			// with a brace is refused rather than told apart from one.
			if ch.Kind() == xdm.KindText && strings.ContainsAny(ch.Value(), "{}") {
				ok = false
				return false
			}
		}
		return true
	})
	return ok
}

// burstInstructions are the XSLT instructions a burst body may hold. Each
// one's expressions are checked on their own; what is missing either reads
// beyond the item (apply-templates and call-template reach code not checked
// here, xsl:number counts preceding nodes, xsl:key indexes the whole tree)
// or has effects the order of reading could show (xsl:message,
// xsl:result-document), or simply is not needed for the first stage.
var burstInstructions = map[string]bool{
	"for-each": true, "iterate": true, "if": true, "choose": true, "when": true,
	"otherwise": true, "value-of": true, "copy-of": true, "sequence": true,
	"text": true, "attribute": true, "element": true, "copy": true,
	"variable": true, "comment": true, "processing-instruction": true,
	"namespace": true, "param": true, "next-iteration": true,
	"with-param": true, "break": true, "on-completion": true, "sort": true,
	"map": true, "map-entry": true, "fork": true,
}

func burstElement(el, target *xdm.Node) bool {
	if el.Name().URI != xdm.NSXSL {
		for a := range el.Attrs() {
			if !lreAttrOK(a) || !avtSafe(el, a.Value()) {
				return false
			}
		}
		return true
	}
	if el != target && !burstInstructions[el.Name().Local] && !burstMessage(el) {
		return false
	}
	for a := range el.Attrs() {
		if a.Name().URI != "" {
			continue
		}
		switch a.Name().Local {
		case "select", "test":
			if el == target {
				continue // burstPath has checked it
			}
			e := identityOperands(parseAt(el, a.Value())) // streamground_ident.go
			if e == nil || !burstSafe(e, false) {
				return false
			}
		case "use-attribute-sets", "validation", "type":
			return false
		default:
			if !avtSafe(el, a.Value()) {
				return false
			}
		}
	}
	if el.Parent() == target && isXSL(el, "param") && isXSL(target, "iterate") {
		return iterateParamOK(el)
	}
	if isXSL(el, "with-param") && isXSL(el.Parent(), "next-iteration") {
		return carriedAtomic(el)
	}
	return true
}

// avtSafe checks the expressions of an attribute value template.
func avtSafe(el *xdm.Node, src string) bool {
	exprs, ok := avtExpressions(src)
	if !ok {
		return false
	}
	for _, s := range exprs {
		e := identityOperands(parseAt(el, s)) // streamground_ident.go
		if e == nil || !burstSafe(e, false) {
			return false
		}
	}
	return true
}

// iterateParamOK admits an xsl:param of the loop whose default does not read
// the focus. The default is evaluated with the xsl:iterate's own focus,
// which is the document node -- and bursts have no document node to give
// it.
func iterateParamOK(p *xdm.Node) bool {
	if onlyElementChild(p) != nil || strings.TrimSpace(p.StringValue()) != "" {
		return false
	}
	if s := p.AttrValue("select"); s != "" {
		e := parseAt(p, s)
		return e != nil && constructorFocusFree(e) // streamsplit_fns.go
	}
	return true
}

// carriedAtomic reports whether the value xsl:next-iteration carries to the
// next item can hold no node: a node of this burst carried into the next
// would be a node of a tree that no longer matches the document. The
// parameter's declared type decides when it is atomic; otherwise the
// expression must be one that only makes atomic values.
func carriedAtomic(wp *xdm.Node) bool {
	sel := wp.AttrValue("select")
	if sel == "" {
		// A sequence constructor makes a new temporary tree: it is a copy.
		return wp.Attr("", "select") == nil
	}
	if p := iterateParamNamed(wp); p != nil {
		if st, err := xpath.ParseSequenceType(p.AttrValue("as"), newNSResolver(p, "")); err == nil &&
			p.AttrValue("as") != "" && (st.HasAtomicType || st.IsNumericType) {
			return true
		}
	}
	e := parseAt(wp, sel)
	return e != nil && atomicValued(e)
}

// iterateParamNamed finds the xsl:param of the innermost enclosing
// xsl:iterate that wp supplies.
func iterateParamNamed(wp *xdm.Node) *xdm.Node {
	want, _ := paramName(wp)
	it := enclosingIterate(wp)
	if it == nil {
		return nil
	}
	for _, c := range it.ChildElements() {
		if isXSL(c, "param") {
			if q, _ := paramName(c); q == want {
				return c
			}
		}
	}
	return nil
}

// atomicValued reports, conservatively, whether e can only return atomic
// values.
func atomicValued(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Literal:
		return true
	case *xpath.BinaryOp:
		switch x.Op {
		case "is", "<<", ">>", "union", "intersect", "except":
			return false
		}
		return true
	case *xpath.UnaryOp:
		return true
	case *xpath.CastExpr:
		return true
	case *xpath.InstanceOfExpr:
		return true
	case *xpath.IfExpr:
		return atomicValued(x.Then) && atomicValued(x.Else)
	case *xpath.SequenceExpr:
		for _, it := range x.Items {
			if !atomicValued(it) {
				return false
			}
		}
		return true
	case *xpath.FuncCall:
		if x.Name.URI == xdm.NSXS {
			return true
		}
		if x.Name.URI != fnNS {
			return sheetCallAtomic(x) // streamsplit_calls.go
		}
		switch x.Name.Local {
		case "count", "sum", "avg", "min", "max", "string", "number", "concat",
			"string-join", "data", "boolean", "not", "exists", "empty",
			"string-length", "normalize-space", "upper-case", "lower-case",
			"substring", "substring-before", "substring-after", "translate",
			"round", "floor", "ceiling", "abs", "name", "local-name",
			"namespace-uri", "position", "true", "false", "contains",
			"starts-with", "ends-with", "format-number", "distinct-values",
			"string-to-codepoints", "codepoints-to-string", "tokenize":
			return true
		}
	}
	return false
}

// burstFnBanned are the fn functions a burst would answer differently: they
// reach the root or the whole document, observe node identity or position
// among all the items, or read the document's DTD.
var burstFnBanned = map[string]bool{
	"root": true, "id": true, "idref": true, "element-with-id": true,
	"key": true, "generate-id": true, "path": true, "last": true,
	"unparsed-entity-uri": true, "unparsed-entity-public-id": true,
	"accumulator-before": true, "accumulator-after": true,
	"function-lookup": true, "transform": true, "load-xquery-module": true,
	"apply": true, "current-group": true, "current-grouping-key": true,
	"current-merge-group": true, "current-merge-key": true,
}

// nameOnlyFns read nothing of a node but its name, its URIs, or whether it
// is there, so an ancestor -- which a burst copies without its other
// children -- answers them as the whole tree's would.
var nameOnlyFns = map[string]bool{
	"name": true, "local-name": true, "namespace-uri": true, "node-name": true,
	"count": true, "exists": true, "empty": true,
}

// burstSafe reports whether e reads nothing a burst lacks. noPos refuses
// fn:position as well, for a predicate of the loop's own select, where it
// would count within the burst.
func burstSafe(e xpath.Expr, noPos bool) bool {
	switch x := e.(type) {
	case *xpath.Literal, *xpath.VarRef, *xpath.ContextItem:
		return true
	case *xpath.Step:
		return pathSafe([]xpath.Expr{x}, noPos, false)
	case *xpath.PathExpr:
		return !x.Root && pathSafe(x.Steps, noPos, false)
	case *xpath.FilterExpr:
		return burstSafe(x.Base, noPos) && allBurstSafe(x.Predicates, noPos)
	case *xpath.BinaryOp:
		switch x.Op {
		case "is", "<<", ">>", "union", "intersect", "except":
			return false
		}
		return burstSafe(x.Left, noPos) && burstSafe(x.Right, noPos)
	case *xpath.UnaryOp:
		return burstSafe(x.Operand, noPos)
	case *xpath.StringConcat:
		return burstSafe(x.Left, noPos) && burstSafe(x.Right, noPos) // concat(a, b)
	case *xpath.FuncCall:
		return callSafe(x, noPos)
	case *xpath.IfExpr:
		return allBurstSafe([]xpath.Expr{x.Cond, x.Then, x.Else}, noPos)
	case *xpath.SequenceExpr:
		return allBurstSafe(x.Items, noPos)
	case *xpath.LetExpr:
		return allBurstSafe(append(bindingExprs(x.Bindings), x.Return), noPos)
	case *xpath.ForExpr:
		return allBurstSafe(append(bindingExprs(x.Bindings), x.Return), noPos)
	case *xpath.QuantifiedExpr:
		return allBurstSafe(append(bindingExprs(x.Bindings), x.Test), noPos)
	case *xpath.InstanceOfExpr:
		return burstSafe(x.Operand, noPos)
	case *xpath.CastExpr:
		return burstSafe(x.Operand, noPos)
	case *xpath.TreatExpr:
		return burstSafe(x.Operand, noPos)
	case *xpath.SimpleMap:
		return burstSafe(x.Left, noPos) && burstSafe(x.Right, noPos)
	case *xpath.MapConstructor:
		return allBurstSafe(x.Keys, noPos) && allBurstSafe(x.Values, noPos)
	case *xpath.ArrayConstructor:
		return allBurstSafe(x.Members, noPos)
	case *xpath.LookupExpr:
		return x.Base != nil && burstSafe(x.Base, noPos) && (x.Expr == nil || burstSafe(x.Expr, noPos))
	case *xpath.NamedFunctionRef:
		return namedRefSafe(x) // streamfn_refs.go
	case *xpath.DynamicCall:
		return dynCallBurstSafe(x, noPos) // streamsplit_dyncall.go
	}
	return false
}

func allBurstSafe(es []xpath.Expr, noPos bool) bool {
	for _, e := range es {
		if !burstSafe(e, noPos) {
			return false
		}
	}
	return true
}

func callSafe(x *xpath.FuncCall, noPos bool) bool {
	switch x.Name.URI {
	case fnNS:
		if burstFnBanned[x.Name.Local] || noPos && x.Name.Local == "position" {
			return false
		}
		if nameOnlyFns[x.Name.Local] && len(x.Args) == 1 {
			if p, ok := x.Args[0].(*xpath.PathExpr); ok && !p.Root && pathSafe(p.Steps, noPos, true) {
				return true
			}
			if s, ok := x.Args[0].(*xpath.Step); ok && pathSafe([]xpath.Expr{s}, noPos, true) {
				return true
			}
		}
	case xdm.NSXS, xdm.NSMath, xdm.NSMap, xdm.NSArray:
	default:
		return sheetCallSafe(x, noPos) // streamsplit_calls.go
	}
	return allBurstSafe(x.Args, noPos)
}

// pathSafe checks the steps of a relative path. A step up -- parent,
// ancestor -- reaches a copy that has its name and attributes but not its
// other children, so after one only further steps up are allowed, and then
// either an attribute step to end the path or, when upEnd says the path is
// the argument of a name-only function, nothing more.
func pathSafe(steps []xpath.Expr, noPos, upEnd bool) bool {
	up := false
	for k, s := range steps {
		st, ok := s.(*xpath.Step)
		if !ok {
			if up || !burstSafe(s, noPos) {
				return false
			}
			continue
		}
		switch st.Axis {
		case xpath.AxisParent, xpath.AxisAncestor, xpath.AxisAncestorOrSelf:
			if len(st.Predicates) > 0 {
				return false
			}
			up = true
			continue
		case xpath.AxisAttribute:
			if up && k != len(steps)-1 {
				return false
			}
		case xpath.AxisChild, xpath.AxisDescendant, xpath.AxisDescendantOrSelf,
			xpath.AxisSelf, xpath.AxisNamespace:
			if up {
				return false
			}
		default:
			return false
		}
		if !allBurstSafe(st.Predicates, noPos) {
			return false
		}
	}
	if up {
		last, _ := steps[len(steps)-1].(*xpath.Step)
		return upEnd || last != nil && last.Axis == xpath.AxisAttribute
	}
	return true
}

// reparse parses the source of sel, written where ns resolves names,
// exactly as compileExpr parsed it, or returns nil.
func reparse(sel *xpath.Compiled, ns xpath.NamespaceResolver) xpath.Expr {
	o := exprOptions(ns)
	var (
		e   xpath.Expr
		err error
	)
	if o.RefFloor != 0 && o.RefFloor != o.Version {
		e, err = xpath.ParseVersionRefFloor(sel.Source(), ns, o.Version, o.RefFloor)
	} else {
		e, err = xpath.ParseVersion(sel.Source(), ns, o.Version)
	}
	if err != nil {
		return nil
	}
	return e
}

// sameAtomicType reports whether items atomize to values of one type, the
// type of *first, which it sets from the first value seen. xs:untypedAtomic
// counts as the xs:double min and max make of it.
func sameAtomicType(items xdm.Sequence, first *string) bool {
	for _, it := range xdm.Atomize(items) {
		a, ok := it.(*xdm.Atomic)
		if !ok {
			return false
		}
		t := a.TypeName()
		if a.Type == xdm.TypeUntypedAtomic {
			t = "xs:double"
		}
		if *first == "" {
			*first = t
		} else if t != *first {
			return false
		}
	}
	return true
}

// evalSelect evaluates sel, the select of instr, from the bursts when instr
// is the target of the plan running.
func (rt *runtime) evalSelect(instr Instruction, sel *xpath.Compiled) (xdm.Sequence, error) {
	if f := rt.burstsFor(instr); f != nil && f.eval != nil {
		return f.eval(rt)
	}
	return sel.Eval(rt.ctx)
}

// burstsFor returns the feed that drives instr, or nil when instr evaluates
// its select as usual.
func (rt *runtime) burstsFor(instr Instruction) *burstFeed {
	if f := rt.bursts; f != nil {
		if f.instr == instr {
			return f
		}
		if f.run != nil {
			return f.run.feed(instr, rt)
		}
	}
	return nil
}

// wantsBursts implements burstResolver.
func (r *FileResolver) wantsBursts(uri, base string) (string, bool) {
	path, err := r.resolvePath(uri, base)
	if err != nil {
		return "", true // parseBursts reports the error as ResolveDocument would
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, cached := r.cache[path]
	return fileURIOf(path), !cached
}

// openedResolver is the burstResolver of a StreamingDocumentResolver, for one
// read of one document. Only OpenDocument knows the document's URI, which
// wantsBursts is asked for before the read, so wantsBursts opens it, once,
// and streamDoc reads that reader. release closes a reader nothing read:
// every caller defers it.
type openedResolver struct {
	r      StreamingDocumentResolver
	opened bool
	rc     io.ReadCloser
	docURI string
}

// wantsBursts implements burstResolver: false, and the tree, when the
// document does not open or has no URI (ResolveDocument then reports it).
func (o *openedResolver) wantsBursts(uri, base string) (string, bool) {
	if !o.opened {
		o.opened = true
		var err error
		if o.rc, o.docURI, err = o.r.OpenDocument(uri, base); err != nil || o.docURI == "" {
			o.release()
			o.docURI = ""
		}
	}
	return o.docURI, o.docURI != ""
}

// streamDoc implements burstResolver, with the zero ParseOptions' limits.
func (o *openedResolver) streamDoc(ctx *xpath.Context, uri, base string, fn func(*xdmstream.Cursor) error) error {
	rc := o.rc
	o.rc = nil
	if rc == nil {
		var err error
		if rc, _, err = o.r.OpenDocument(uri, base); err != nil {
			return err
		}
	}
	return streamOpened(ctx, rc, uri, xdm.ParseOptions{BaseURI: o.docURI, DocumentURI: o.docURI}, -1, -1, fn)
}

// release closes the reader wantsBursts opened, if streamDoc has not.
func (o *openedResolver) release() {
	if o.rc != nil {
		o.rc.Close()
		o.rc = nil
	}
}

// releaseBursts is release for any burstResolver.
func releaseBursts(br burstResolver) {
	if o, ok := br.(*openedResolver); ok {
		o.release()
	}
}
