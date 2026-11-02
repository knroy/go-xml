package xslt

// Burst-mode execution of xsl:source-document streamable="yes".
//
// The narrowest common streaming pattern -- a streamable xsl:source-document
// whose body, under literal result elements at most, is one xsl:for-each or
// xsl:iterate over a child path of the document, reading only the item it is
// given -- is executed without building the document. xdm.ParseBursts hands
// over one selected element at a time, in a small tree of its own holding
// the element and copies of its ancestors, and the loop's body runs on it
// before the next one is read.
//
// That is correct only where the body cannot tell a burst from the whole
// tree, so the plan is made from the stylesheet source and fails closed:
// anything planBursts does not positively recognise leaves the instruction
// on the ordinary path, which reads the whole tree. The rules, and why each
// is needed, are in docs: DESIGN.md of the stream lane. In brief, a burst has
// no siblings, no following or preceding nodes, a root that holds only the
// one element, and ancestors that hold only it; and nodes of different
// bursts are different trees. So the body may not reach the root, may not
// use a sibling, following or preceding axis, may reach an ancestor only for
// its name or attributes, may not observe node identity (is, <<, >>, union,
// intersect, except, generate-id, fn:path), may not ask for last(), and may
// carry nothing but atomic values from one item to the next.
//
// The body may instead be one xsl:value-of, xsl:copy-of or xsl:sequence
// whose select reads one record path, as sum(doc/rec/@v) does (stage 1b).
// The path is evaluated in each burst, and the rest of the select -- written
// around it, with the path's place taken by a variable -- once over all their
// items in order, which is what the path would have returned from the whole
// tree. count, sum, min and max of the path fold each burst's items instead
// of keeping them; any other select must atomize the items as each burst is
// read, or have atomic values for items. One that would keep a burst's
// nodes, or copies of them, is left to the tree, which then costs no more.
// The select is admitted only where its other parts read no focus, and the
// items are used only in ways a burst cannot tell from the tree
// (burstSplit). Any error is handed to the tree path, which reads the
// document again and reports it as it would have.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/knroy/go-xml/v2/internal/xpathleaf"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// noBursts turns burst execution off, and traceBursts, when set, is told of
// each run; both are for the tests that compare the two paths.
var (
	noBursts    bool
	traceBursts func(href string)
	// traceBurstFallback is told of each error that sends a value-of,
	// copy-of or sequence target back to the tree.
	traceBurstFallback func(err error)
)

// burstPlan is a streamable xsl:source-document that can run in bursts.
type burstPlan struct {
	// path is the select expression's child steps, from the document node.
	path []xdm.QName
	// target is the xsl:for-each or xsl:iterate element, and instr its
	// compiled form; sel is its select expression, evaluated against each
	// burst's document node to apply the last step's predicates.
	target *xdm.Node
	instr  Instruction
	sel    *xpath.Compiled
	// filtered says the last step has predicates. Without them each burst's
	// element is the item, and sel need not be evaluated.
	filtered bool

	// For a value-of, copy-of or sequence target, items is the record path
	// of its select, evaluated against each burst's document node, and outer
	// the select with $burstItems in the path's place. reducer, when set,
	// names the count, sum, min or max of the path that folds the bursts
	// instead: fold is that call over ($burstAcc, $burstItems), and folded
	// the select with $burstValue in its place.
	items, outer *xpath.Compiled
	reducer      string
	fold, folded *xpath.Compiled
	// atomize says the path's items are consumed only as atomic values,
	// so each burst's are atomized at once and its tree let go.
	atomize bool
}

// The variables the parts of a split select are bound to, in a namespace no
// stylesheet can write.
var (
	burstItems = xdm.QName{URI: "urn:x-go-xml:burst", Local: "items"}
	burstAcc   = xdm.QName{URI: "urn:x-go-xml:burst", Local: "acc"}
	burstValue = xdm.QName{URI: "urn:x-go-xml:burst", Local: "value"}
)

// noBurstFold makes a reducer keep every item, as any other select does;
// the tests compare the two.
var noBurstFold bool

// burstFeed is a burst plan being executed: the loop instruction it drives,
// and read, which calls each with every item the loop's select would have
// returned, in order.
type burstFeed struct {
	instr Instruction
	read  func(each func(xdm.Item) error) error
	// eval is the select of a value-of, copy-of or sequence target.
	eval func(rt *runtime) (xdm.Sequence, error)
}

// burstResolver is a document resolver that can parse a document in bursts.
type burstResolver interface {
	// wantsBursts reports whether uri should be parsed in bursts rather than
	// resolved to a tree -- false when the resolver already holds the tree,
	// which fn:doc must go on returning -- and the document URI the tree
	// would have had.
	wantsBursts(uri, base string) (docURI string, ok bool)
	// parseBursts is xdm.ParseBursts over the document uri names, read with
	// the options and limits ResolveDocument would read it with. An error
	// from fn is returned as it is.
	parseBursts(ctx *xpath.Context, uri, base string, path []xdm.QName, fn func(*xdm.Node) error) error
}

// planBursts decides, from the stylesheet source, whether the streamable
// xsl:source-document sd can run in bursts. shared says sd's module is not
// the whole stylesheet (streamtrust.go).
func planBursts(sd *xdm.Node, shared bool) *burstPlan {
	for _, a := range []string{"validation", "type", "use-accumulators"} {
		if sd.Attr("", a) != nil {
			return nil
		}
	}
	// The §19.8 verdict first: the body must be guaranteed-streamable, from
	// constructs the analysis fully models, resting on no declaration that
	// was not itself verified. Everything below narrows that to what bursts
	// can execute.
	if !burstVerdict(sd, shared) {
		return nil
	}
	el := sd
	var target *xdm.Node
	for target == nil {
		kid := onlyElementChild(el)
		switch {
		case kid == nil:
			return nil
		case isXSL(kid, "for-each") || isXSL(kid, "iterate"):
			target = kid
		case isXSL(kid, "value-of") || isXSL(kid, "copy-of") || isXSL(kid, "sequence"):
			if !selectTargetOK(kid) {
				return nil
			}
			s := splitSelect(parseAt(kid, kid.AttrValue("select")), isXSL(kid, "value-of"))
			if s == nil {
				return nil
			}
			return &burstPlan{path: s.path, target: kid}
		case kid.Name().URI == xdm.NSXSL || !plainWrapper(kid):
			return nil
		default:
			el = kid
		}
	}
	for _, c := range target.ChildElements() {
		if isXSL(c, "sort") {
			return nil
		}
	}
	path, filtered := burstPath(target)
	if path == nil || !burstBody(target) {
		return nil
	}
	return &burstPlan{path: path, target: target, filtered: filtered}
}

// burstVerdict reports whether the §19.8 verdict on sd is one execution may
// trust: known, unverified clear, and grounded.
func burstVerdict(sd *xdm.Node, shared bool) bool {
	t := newDeclTrust(documentElementOf(sd), shared)
	p, known, unverified := sequenceConstructorVerdict(sd, postureStriding, t.sets, t.funcs, t)
	return known && !unverified && p.streamable() && p.posture == postureGrounded
}

// selectTargetOK admits a value-of, copy-of or sequence whose attributes
// change nothing bursts would have to do differently: a select, and none of
// validation, type, copy-accumulators or a separator computed from the focus.
func selectTargetOK(el *xdm.Node) bool {
	if el.Attr("", "select") == nil {
		return false
	}
	for a := range el.Attrs() {
		if a.Name().URI != "" {
			continue
		}
		switch a.Name().Local {
		case "select", "copy-namespaces", "version", "xpath-default-namespace",
			"default-collation", "exclude-result-prefixes",
			"extension-element-prefixes", "expand-text", "use-when":
		case "separator", "disable-output-escaping":
			if strings.ContainsAny(a.Value(), "{}") {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// burstSplit is a select expression taken apart around its record path.
type burstSplit struct {
	// path is the element names the bursts are cut at.
	path []xdm.QName
	// items is the record path, put puts an expression in its place, and
	// outer holds the whole expression, whatever put has made of it.
	items xpath.Expr
	put   func(xpath.Expr)
	outer *xpath.Expr
	// call is the count, sum, min or max whose first argument is the
	// record path, and putCall puts an expression in its place.
	call    *xpath.FuncCall
	putCall func(xpath.Expr)
	// atomize says the expression consuming the record path atomizes it
	// and keeps none of its nodes (burstAtomizers), or only counts them.
	atomize bool
}

// splitSelect finds the one record path in e and checks that everything
// around it either reads no focus -- it is evaluated once, with no document
// to read -- or consumes the path's items in a way that cannot tell the
// items of separate bursts from the tree's: as values, by position in the
// whole sequence, or by navigating within each item as burstSafe allows.
// Node identity, reordering and user code are refused, and so is anything
// that keeps every item when it need not (distinct-values, reverse, sort,
// last()). It returns nil when e is not admitted.
//
// It also refuses a select that would keep the record path's nodes until
// the end: a node of a burst holds the burst's whole tree, several times
// what the same element costs in the document's tree, so keeping them
// would make streaming dearer than the tree it replaces. The path's items
// must be folded (count, sum, min, max), atomized by the expression that
// consumes them (burstAtomizers; xsl:value-of, valueOf, when the path is
// its whole select and ends in attributes), or atomic values already. Copies made by a last
// copy-of() are refused as well: kept to the end, each one a tree of its
// own, they peaked above the tree path's RSS (4.2 GB against 4.0 GB, 100 MB).
func splitSelect(e xpath.Expr, valueOf bool) *burstSplit {
	if e == nil {
		return nil
	}
	root := e
	s := &burstSplit{outer: &root}
	if !s.walk(e, func(x xpath.Expr) { root = x }) || s.items == nil {
		return nil
	}
	// xsl:value-of joins adjacent text nodes with no separator, so only
	// attributes are as good as their values to it.
	if valueOf && root == s.items && endsInAttribute(s.items) {
		s.atomize = true
	}
	if s.call == nil && !s.atomize && !endsInValue(s.items) {
		return nil
	}
	return s
}

// endsInAttribute reports whether the record path p ends in a step on the
// attribute axis.
func endsInAttribute(p xpath.Expr) bool {
	var last xpath.Expr = p
	if pe, ok := p.(*xpath.PathExpr); ok {
		last = pe.Steps[len(pe.Steps)-1]
	}
	st, ok := last.(*xpath.Step)
	return ok && st.Axis == xpath.AxisAttribute
}

// endsInValue reports whether the record path p ends in an expression
// that makes atomic values: one tailValue admits other than copy-of().
func endsInValue(p xpath.Expr) bool {
	pe, ok := p.(*xpath.PathExpr)
	if !ok {
		return false
	}
	last := pe.Steps[len(pe.Steps)-1]
	if f, ok := last.(*xpath.FilterExpr); ok {
		last = f.Base
	}
	switch x := last.(type) {
	case *xpath.Step:
		return false
	case *xpath.FuncCall:
		return x.Name.URI != fnNS || x.Name.Local != "copy-of"
	}
	return true
}

// burstReducers fold with constant state, and burstConsumers take their
// arguments whole and observe of a node no more than burstSafe allows.
var (
	burstReducers  = map[string]int{"count": 1, "sum": 2, "min": 2, "max": 2}
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
		"ceiling": true, "abs": true, "round-half-to-even": true,
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
		"ceiling": true, "abs": true, "round-half-to-even": true,
		"format-number": true, "index-of": true,
	}
	burstOps = map[string]bool{
		"=": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true,
		"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true,
		"+": true, "-": true, "*": true, "div": true, "idiv": true, "mod": true,
		"and": true, "or": true,
	}
)

// walk checks e, which put replaces, for splitSelect.
func (s *burstSplit) walk(e xpath.Expr, put func(xpath.Expr)) bool {
	if path := recordPath(e); path != nil {
		if s.items != nil {
			return false // a second record path: two passes over the stream
		}
		s.path, s.items, s.put = path, e, put
		return true
	}
	switch x := e.(type) {
	case *xpath.FuncCall:
		switch x.Name.URI {
		case fnNS:
			if !burstConsumers[x.Name.Local] {
				return false
			}
		case xdm.NSXS, xdm.NSMath:
		default:
			return false
		}
		if n, ok := burstReducers[x.Name.Local]; ok && x.Name.URI == fnNS &&
			len(x.Args) >= 1 && len(x.Args) <= n && recordPath(x.Args[0]) != nil {
			s.call, s.putCall = x, put
		}
		if x.Name.URI == xdm.NSXS || x.Name.URI == fnNS && burstAtomizers[x.Name.Local] {
			s.atomizes(x.Args...)
		}
		for k := range x.Args {
			if !s.slot(x.Args[k], func(y xpath.Expr) { x.Args[k] = y }) {
				return false
			}
		}
		return true
	case *xpath.BinaryOp:
		if x.Op != "and" && x.Op != "or" {
			s.atomizes(x.Left, x.Right)
		}
		return burstOps[x.Op] &&
			s.slot(x.Left, func(y xpath.Expr) { x.Left = y }) &&
			s.slot(x.Right, func(y xpath.Expr) { x.Right = y })
	case *xpath.UnaryOp:
		return s.slot(x.Operand, func(y xpath.Expr) { x.Operand = y })
	case *xpath.CastExpr:
		s.atomizes(x.Operand)
		return s.slot(x.Operand, func(y xpath.Expr) { x.Operand = y })
	case *xpath.InstanceOfExpr:
		return s.slot(x.Operand, func(y xpath.Expr) { x.Operand = y })
	case *xpath.TreatExpr:
		return s.slot(x.Operand, func(y xpath.Expr) { x.Operand = y })
	case *xpath.IfExpr:
		return s.slot(x.Cond, func(y xpath.Expr) { x.Cond = y }) &&
			s.slot(x.Then, func(y xpath.Expr) { x.Then = y }) &&
			s.slot(x.Else, func(y xpath.Expr) { x.Else = y })
	case *xpath.SequenceExpr:
		for k := range x.Items {
			if !s.slot(x.Items[k], func(y xpath.Expr) { x.Items[k] = y }) {
				return false
			}
		}
		return true
	case *xpath.FilterExpr:
		// The predicates see each item, positioned in the whole sequence.
		return allBurstSafe(x.Predicates, false) &&
			s.slot(x.Base, func(y xpath.Expr) { x.Base = y })
	case *xpath.SimpleMap:
		return burstSafe(x.Right, false) &&
			s.slot(x.Left, func(y xpath.Expr) { x.Left = y })
	case *xpath.QuantifiedExpr:
		return s.bindings(x.Bindings) && burstSafe(x.Test, false) && focusFree(x.Test)
	case *xpath.ForExpr:
		return s.bindings(x.Bindings) && burstSafe(x.Return, false) && focusFree(x.Return)
	}
	return false
}

// atomizes notes that the operands es, of an expression that atomizes them,
// include the record path itself.
func (s *burstSplit) atomizes(es ...xpath.Expr) {
	for _, e := range es {
		if recordPath(e) != nil {
			s.atomize = true
		}
	}
}

func (s *burstSplit) bindings(bs []xpath.Binding) bool {
	for k := range bs {
		if !s.slot(bs[k].Seq, func(y xpath.Expr) { bs[k].Seq = y }) {
			return false
		}
	}
	return true
}

// slot checks an operand: one that reads no focus is evaluated as it is,
// and any other must hold the record path.
func (s *burstSplit) slot(e xpath.Expr, put func(xpath.Expr)) bool {
	if focusFree(e) && burstSafe(e, false) {
		return true
	}
	return s.walk(e, put)
}

// recordPath returns the element names to cut bursts at when e is a record
// path, nil otherwise. A record path is child steps by name from the
// document node, the last of them perhaps with predicates that do not
// depend on position (as burstPath requires), followed by steps that stay
// within the element -- down, or to its attributes, with any predicates
// burstSafe allows -- and perhaps one last expression that makes atomic
// values or copies. Each burst then holds what the path returns from one
// element, in document order, and the bursts in turn hold all of it.
func recordPath(e xpath.Expr) []xdm.QName {
	var steps []xpath.Expr
	switch x := e.(type) {
	case *xpath.Step:
		steps = []xpath.Expr{x}
	case *xpath.PathExpr:
		steps = x.Steps
		if _, ok := firstOf(steps).(*xpath.ContextItem); ok && !x.Root {
			steps = steps[1:]
		}
	default:
		return nil
	}
	var path []xdm.QName
	k := 0
	for ; k < len(steps); k++ {
		st, ok := steps[k].(*xpath.Step)
		if !ok || st.Axis != xpath.AxisChild {
			break
		}
		nt, ok := st.Test.(*xpath.NameTest)
		if !ok || nt.AnyURI || nt.AnyLocal {
			break
		}
		if len(st.Predicates) > 0 {
			// Positional predicates count within the element above,
			// which is then the burst.
			for _, p := range st.Predicates {
				if !booleanShaped(p) || !burstSafe(p, true) {
					goto tail
				}
			}
			path = append(path, xdm.QName{URI: nt.Name.URI, Local: nt.Name.Local})
			k++
			break
		}
		path = append(path, xdm.QName{URI: nt.Name.URI, Local: nt.Name.Local})
	}
tail:
	if len(path) == 0 || !pathTail(steps[k:]) {
		return nil
	}
	return path
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

// plainWrapper reports whether a literal result element around the loop
// writes nothing but itself: no value template, which would be evaluated
// against the document node the bursts never build, and no xsl: attribute
// that changes what it constructs.
func plainWrapper(el *xdm.Node) bool {
	for a := range el.Attrs() {
		if !lreAttrOK(a) || strings.ContainsAny(a.Value(), "{}") {
			return false
		}
	}
	return true
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

// burstPath returns the element names of the loop's select, a path of child
// steps from the document node, or nil. Only the last step may have
// predicates, and they must not depend on position: a burst's element is
// the first and only one of its name in its tree.
func burstPath(target *xdm.Node) (path []xdm.QName, filtered bool) {
	e := parseAt(target, target.AttrValue("select"))
	var steps []xpath.Expr
	switch x := e.(type) {
	case *xpath.PathExpr:
		steps = x.Steps
	case *xpath.Step:
		steps = []xpath.Expr{x}
	default:
		return nil, false
	}
	for k, s := range steps {
		st, ok := s.(*xpath.Step)
		if !ok || st.Axis != xpath.AxisChild {
			return nil, false
		}
		nt, ok := st.Test.(*xpath.NameTest)
		if !ok || nt.AnyURI || nt.AnyLocal {
			return nil, false
		}
		if len(st.Predicates) > 0 && k < len(steps)-1 {
			return nil, false
		}
		for _, p := range st.Predicates {
			if !booleanShaped(p) || !burstSafe(p, true) {
				return nil, false
			}
		}
		filtered = len(st.Predicates) > 0
		path = append(path, xdm.QName{URI: nt.Name.URI, Local: nt.Name.Local})
	}
	return path, filtered
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
	return e
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
	"map": true, "map-entry": true,
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
	if el != target && !burstInstructions[el.Name().Local] {
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
			e := parseAt(el, a.Value())
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
		e := parseAt(el, s)
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
		return e != nil && focusFree(e)
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
			return false
		}
		switch x.Name.Local {
		case "count", "sum", "avg", "min", "max", "string", "number", "concat",
			"string-join", "data", "boolean", "not", "exists", "empty",
			"string-length", "normalize-space", "upper-case", "lower-case",
			"substring", "substring-before", "substring-after", "translate",
			"round", "floor", "ceiling", "abs", "name", "local-name",
			"namespace-uri", "position", "true", "false", "contains",
			"starts-with", "ends-with", "format-number", "distinct-values",
			"string-to-codepoints", "codepoints-to-string":
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
		return false
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

// noteBurstLoop records instr as the compiled loop of the burst plan being
// compiled, when n is its loop element.
func (c *compiler) noteBurstLoop(n *xdm.Node, instr Instruction, sel *xpath.Compiled) {
	if c.burst != nil && c.burst.target == n {
		c.burst.instr, c.burst.sel = instr, sel
	}
}

// noteBurstSelect records instr as the compiled target of the burst plan
// being compiled, when n is its value-of, copy-of or sequence, and compiles
// the parts of its select, sel, written where ns resolves names. The select
// is parsed again as compileExpr parsed it, so the parts are taken from the
// tree sel was optimised from.
func (c *compiler) noteBurstSelect(n *xdm.Node, instr Instruction, sel *xpath.Compiled,
	ns xpath.NamespaceResolver) {

	p := c.burst
	if p == nil || p.target != n || sel.CompatMode() {
		return
	}
	// fn:current() is the document node, which bursts do not have.
	for _, call := range sel.StaticCalls() {
		if call.Name.URI == fnNS && call.Name.Local == "current" {
			return
		}
	}
	parse := func() *burstSplit {
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
		return splitSelect(e, isXSL(n, "value-of"))
	}
	part := func(e xpath.Expr) *xpath.Compiled {
		return xpathleaf.WithExpr(sel, e).(*xpath.Compiled)
	}
	s := parse()
	if s == nil {
		return
	}
	s.put(&xpath.VarRef{Name: burstItems})
	p.items, p.outer, p.atomize = part(s.items), part(*s.outer), s.atomize
	if f := parse(); f != nil && f.call != nil {
		f.putCall(&xpath.VarRef{Name: burstValue})
		p.reducer, p.folded = f.call.Name.Local, part(*f.outer)
		args := append([]xpath.Expr{&xpath.SequenceExpr{Items: []xpath.Expr{
			&xpath.VarRef{Name: burstAcc}, &xpath.VarRef{Name: burstItems}}}}, f.call.Args[1:]...)
		p.fold = part(&xpath.FuncCall{Name: f.call.Name, Args: args})
	}
	p.instr, p.sel = instr, sel
}

// executeBursts runs i in bursts when its plan and its resolver allow, and
// reports whether it did.
func (i *sourceDocumentInstr) executeBursts(rt *runtime, out *outputBuilder, href string) (bool, error) {
	if noBursts || validSourceDocumentURI(href) != nil || strings.ContainsRune(href, '#') {
		return false, nil
	}
	// The resolvers Transform wraps around the caller's are looked through
	// here, doing for this read what each does for a tree: readDocResolver
	// records it for XTDE1500 and answers from its own memory when the
	// transformation has read the document already, and stripSpaceResolver
	// strips whitespace, which is done per burst instead.
	base := i.base(rt)
	pkg := packageOf(rt.ctx)
	var (
		rd    *readDocResolver
		strip *Stylesheet
		br    burstResolver
	)
	for d := rt.ctx.Env().Docs; br == nil; {
		switch v := d.(type) {
		case *readDocResolver:
			if _, seen := v.docs[docKey{href, base, pkg}]; seen {
				return false, nil
			}
			rd, d = v, v.inner
		case *stripSpaceResolver:
			strip, d = v.sheet, v.inner
		case burstResolver:
			br = v
		default:
			return false, nil
		}
	}
	docURI, ok := br.wantsBursts(href, base)
	if !ok {
		return false, nil
	}
	// Tree mode records the document only once it has loaded; a document
	// that fails to parse must not count as read here either (unread).
	unread := func() {}
	if rd != nil && docURI != "" {
		if !rd.read[docURI] {
			unread = func() { delete(rd.read, docURI) }
		}
		rd.read[docURI] = true
		if rd.written[docURI] {
			return true, fmt.Errorf(
				"XTDE1500: this transformation reads %q, which xsl:result-document "+
					"has already written", docURI)
		}
	}
	if traceBursts != nil {
		traceBursts(href)
	}
	// The body's own focus is the document node, which the bursts do not
	// build. Nothing planBursts admits reads it, so an empty document node
	// with the document's URIs stands in for it.
	stand := xdm.NewTree()
	sub := rt.withCurrent(stand.Root, 1, 1).clearCurrentRule()
	plan := i.bursts
	// records calls fn with each burst's document node and element,
	// whitespace stripped as the tree would have been.
	records := func(fn func(root, rec *xdm.Node) error) error {
		return br.parseBursts(rt.ctx, href, base, plan.path, func(rec *xdm.Node) error {
			root := rec.Tree().Root
			if strip != nil {
				root, rec = strip.stripWhitespaceForFrom(pkg, root, rec)
			}
			return fn(root, rec)
		})
	}
	if plan.items != nil {
		sub.bursts = &burstFeed{instr: plan.instr, eval: func(rt *runtime) (xdm.Sequence, error) {
			seq, err := plan.evalBursts(rt, records)
			if err == nil {
				return seq, nil
			}
			// Whatever went wrong -- the select, a limit, the document --
			// the tree path reads the document again and fails, or not,
			// exactly as it would have: an error found in a burst may not
			// be the one the whole tree meets first.
			if traceBurstFallback != nil {
				traceBurstFallback(err)
			}
			unread()
			root, err := i.load(rt, href)
			if err != nil {
				return nil, err
			}
			rt.streamedTrees[root] = true
			return plan.sel.Eval(rt.withCurrent(root, 1, 1).ctx)
		}}
		return true, execSequence(i.body, sub, out)
	}
	sub.bursts = &burstFeed{instr: plan.instr, read: func(each func(xdm.Item) error) error {
		var bodyErr error
		err := records(func(root, rec *xdm.Node) error {
			if !plan.filtered {
				if err := each(rec); err != nil {
					bodyErr = err
					return err
				}
				return nil
			}
			items, err := plan.sel.Eval(sub.withCurrent(root, 1, 1).ctx)
			if err != nil {
				bodyErr = err
				return err
			}
			for _, it := range items {
				if err := each(it); err != nil {
					bodyErr = err
					return err
				}
			}
			return nil
		})
		if err != nil && err != bodyErr {
			unread()
			return fmt.Errorf("FODC0002: cannot retrieve %q: %w", href, err)
		}
		return err
	}}
	return true, execSequence(i.body, sub, out)
}

// errBurstGiveUp ends a fold that cannot be exact; the tree path takes over.
var errBurstGiveUp = errors.New("bursts: the fold cannot be exact")

// evalBursts evaluates the select of a value-of, copy-of or sequence target
// over the bursts records reads. The item and byte budgets are held across
// all of it, as they are across one evaluation of the select over the tree.
func (p *burstPlan) evalBursts(rt *runtime,
	records func(fn func(root, rec *xdm.Node) error) error) (xdm.Sequence, error) {

	ctx := rt.ctx.HoldItemBudget().HoldByteBudget()
	fold := p.reducer != "" && !noBurstFold
	var (
		all, acc xdm.Sequence
		n        int64
		typ      string
	)
	err := records(func(root, _ *xdm.Node) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		items, err := p.items.Eval(withFocusCurrent(ctx, root, 1, 1))
		switch {
		case err != nil || len(items) == 0:
			return err
		case !fold && p.atomize:
			all = append(all, xdm.Atomize(items)...)
		case !fold:
			all = append(all, items...)
		case p.reducer == "count":
			n += int64(len(items))
		default:
			// min and max over one type compare one way throughout; over
			// several, promotion makes the answer depend on the order of
			// the comparisons, which only the whole sequence has.
			if p.reducer != "sum" && !sameAtomicType(items, &typ) {
				return errBurstGiveUp
			}
			acc, err = p.fold.Eval(ctx.WithVar(burstAcc, acc).WithVar(burstItems, items))
			return err
		}
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case !fold:
		return p.outer.Eval(ctx.WithVar(burstItems, all))
	case p.reducer == "count":
		acc = xdm.One(xdm.NewInteger(n))
	case acc == nil:
		if acc, err = p.fold.Eval(ctx.WithVar(burstAcc, nil).WithVar(burstItems, nil)); err != nil {
			return nil, err
		}
	}
	return p.folded.Eval(ctx.WithVar(burstValue, acc))
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
	if f := rt.bursts; f != nil && f.instr == instr {
		return f
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

// parseBursts implements burstResolver. It reads the file as parseUncached
// does -- the same confinement, options, entity allowance and byte limit, and
// the same errors -- but in bursts, and caches nothing: there is no tree.
func (r *FileResolver) parseBursts(ctx *xpath.Context, uri, base string,
	steps []xdm.QName, fn func(*xdm.Node) error) error {

	path, err := r.resolvePath(uri, base)
	if err != nil {
		return err
	}
	opts := r.parseOptions(path, false).WithEntityBudget(ctx.EntityBudget())
	var fnErr error
	err = r.openConfined(path, func(f *os.File) error {
		max, lim := r.byteLimit()
		tooBig := fmt.Errorf("%s exceeds the %d byte limit", path, max)
		var src io.Reader = f
		if max >= 0 {
			if fi, err := f.Stat(); err == nil && fi.Size() > max {
				return tooBig
			}
			src = io.LimitReader(f, lim)
		}
		cr := &countedReader{r: src}
		err := xdm.ParseBursts(cr, opts, steps, func(n *xdm.Node) error {
			fnErr = fn(n)
			return fnErr
		})
		if err != nil && err == fnErr {
			return err
		}
		if max >= 0 && cr.n > max {
			return tooBig
		}
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		return nil
	})
	return err
}
