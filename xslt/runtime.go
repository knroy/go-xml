package xslt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// runtime holds per-transform state.
//
// One runtime is created per Transform call and is never shared, which is what
// lets a single compiled Stylesheet serve concurrent transforms: everything
// mutable — the variable stack, the key index, the recursion depth — lives
// here, and the Stylesheet itself is read-only.
type runtime struct {
	sheet *Stylesheet
	ctx   *xpath.Context

	// keyIndex caches xsl:key lookups per document. Building an index is a
	// full document scan, so it is done once per (key, document) pair on
	// first use rather than eagerly for every declared key.
	keyIndex map[keyCacheKey]map[string]xdm.Sequence

	// depth bounds apply-templates recursion, which the spec does not bound
	// and which a stylesheet with a cycle would otherwise run forever.
	depth int
	// maxDepth is the ceiling depth may reach, from TransformOptions.
	maxDepth int

	// temporary marks that the runtime is building a temporary tree — the
	// content of a variable, a function's body, or a grouping key — rather
	// than a final result tree.
	//
	// It exists for XTDE1480: xsl:result-document may not be evaluated in
	// temporary output state, because there is no final result tree for it
	// to be a sibling of. The flag is on the runtime rather than the output
	// builder because the state is inherited by everything the constructor
	// calls, however deeply.
	temporary bool

	// secondary collects xsl:result-document outputs. Like messages it is a
	// pointer, because the runtime struct is copied on every focus change:
	// a plain slice would leave a result-document written inside a template
	// appending to a copy that the caller never sees.
	secondary *[]SecondaryResult

	// messages collects xsl:message output rather than writing to stderr.
	//
	// It is a pointer to a slice because the runtime struct is copied on
	// every focus change and template dispatch; a plain slice would leave
	// each copy appending to its own, and messages emitted inside a template
	// would never reach the caller.
	messages *[]string

	// tunnel holds tunnel parameters, which pass through templates that do
	// not declare them.
	tunnel map[string]xdm.Sequence

	// sel records how the currently-executing template was selected, so that
	// xsl:next-match and xsl:apply-imports in its body can resume the search
	// where it left off rather than starting over and picking the same
	// template forever.
	sel selection
}

// selection is the template-selection state of the enclosing apply-templates.
type selection struct {
	// template is the one currently running; nil outside any match template.
	template *Template
	// next is the index into Stylesheet.templates at which to resume.
	next int
	// mode, params and tunnels are carried so a resumed dispatch behaves
	// like the original one.
	mode    string
	params  map[string]xdm.Sequence
	tunnels map[string]xdm.Sequence
}

type keyCacheKey struct {
	name string
	tree *xdm.Tree
}

// DefaultMaxDepth bounds template recursion when TransformOptions.MaxDepth is
// zero. It matches xdm.DefaultMaxDepth so that a document the parser accepts is
// one an identity transform can copy: the recursion counted here is the
// ordinary descent through the tree, not only a stylesheet calling itself.
const DefaultMaxDepth = 1000

func (rt *runtime) descend() error {
	rt.depth++
	if rt.maxDepth > 0 && rt.depth > rt.maxDepth {
		return fmt.Errorf("template recursion exceeded %d levels", rt.maxDepth)
	}
	return nil
}

func (rt *runtime) ascend() { rt.depth-- }

// withFocus returns a runtime whose XPath context has a new focus.
//
// The runtime struct is copied rather than mutated so that a nested
// instruction cannot disturb its caller's focus — a bug that manifests as
// sibling elements being processed against the wrong context node.
func (rt *runtime) withFocus(item xdm.Item, pos, size int) *runtime {
	n := *rt
	n.ctx = rt.ctx.WithFocus(item, pos, size)
	return &n
}

// withCurrent sets the focus and records it as the value fn:current returns.
//
// Only instructions that establish a new "node being processed" call this —
// xsl:for-each, xsl:apply-templates, xsl:for-each-group. Predicate and step
// evaluation use withFocus, which deliberately leaves current() alone.
func (rt *runtime) withCurrent(item xdm.Item, pos, size int) *runtime {
	n := *rt
	n.ctx = rt.ctx.WithFocus(item, pos, size)
	if item != nil {
		n.ctx = n.ctx.WithVar(currentVar, xdm.One(item))
	}
	return &n
}

// withSelection records the template-selection state for the body about to run.
func (rt *runtime) withSelection(t *Template, next int, mode string,
	params, tunnels map[string]xdm.Sequence) *runtime {
	n := *rt
	n.sel = selection{template: t, next: next, mode: mode, params: params, tunnels: tunnels}
	return &n
}

func (rt *runtime) withVar(name xdm.QName, val xdm.Sequence) *runtime {
	n := *rt
	n.ctx = rt.ctx.WithVar(name, val)
	return &n
}

// --- Output construction ----------------------------------------------------

// outputBuilder accumulates the result of a sequence constructor.
//
// XSLT output is a sequence of nodes and atomic values, not a string: an
// attribute added after an element has children is an error, adjacent text
// must be merged, and the result may be a temporary tree that later
// instructions navigate. Building a string directly would make all of that
// impossible.
type outputBuilder struct {
	items xdm.Sequence
	// open is the element currently being built, if any. Attributes and
	// namespaces are added to it until it is closed.
	open *xdm.Node
	// parent chains open elements so that nested construction works.
	parent *outputBuilder
	tree   *xdm.Tree
}

func newOutputBuilder() *outputBuilder {
	return &outputBuilder{tree: xdm.NewTree()}
}

// appendNode adds a node to the current output position.
//
// A node that already belongs to a tree is copied first. AppendChild rewrites
// the node's Parent and tree pointers and Finalize renumbers its document
// order, so adopting a source node in place *mutates the source document* —
// evaluating an unused variable containing xsl:sequence was enough to reorder
// the input, and two goroutines transforming a shared parsed tree raced on it.
// xsl:copy-of already copied; xsl:sequence and xsl:perform-sort did not, and
// the guard belongs here where every caller is covered.
func (b *outputBuilder) appendNode(n *xdm.Node) {
	n = detach(n)
	if b.open != nil {
		b.open.AppendChild(n)
		return
	}
	b.items = append(b.items, n)
}

// detach returns a node safe to re-parent: n itself when it is freshly
// constructed, a deep copy when it belongs to a tree already.
func detach(n *xdm.Node) *xdm.Node {
	if n == nil || (n.Tree() == nil && n.Parent == nil) {
		return n
	}
	return deepCopy(n)
}

// appendText adds text, merging with a preceding text node so that the XDM
// invariant of no adjacent text nodes holds in constructed trees too.
func (b *outputBuilder) appendText(s string) {
	if s == "" {
		return
	}
	if b.open != nil {
		if k := len(b.open.Children); k > 0 {
			if last := b.open.Children[k-1]; last.Kind == xdm.KindText {
				last.Value += s
				return
			}
		}
		b.open.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: s})
		return
	}
	if k := len(b.items); k > 0 {
		if last, ok := b.items[k-1].(*xdm.Node); ok && last.Kind == xdm.KindText {
			last.Value += s
			return
		}
	}
	b.items = append(b.items, &xdm.Node{Kind: xdm.KindText, Value: s})
}

// appendValue adds an atomic value to the output sequence.
func (b *outputBuilder) appendValue(a *xdm.Atomic) {
	// Inside an element being built, an atomic value becomes text; at the top
	// level it stays an atomic item, because xsl:sequence can return one.
	if b.open != nil {
		b.appendText(a.String())
		return
	}
	b.items = append(b.items, a)
}

// addAttribute attaches an attribute to the element under construction.
func (b *outputBuilder) addAttribute(name xdm.QName, value string) error {
	if b.open == nil {
		// A parentless attribute is a legal item in the data model, and a
		// sequence constructor may produce one: xsl:function as="attribute()"
		// with an xsl:attribute body is the ordinary way to write one, and
		// XTDE0410 is not about this at all. The error is about *ordering*
		// within element content — an attribute preceded by a node that is
		// neither an attribute nor a namespace — which is checked below where
		// there is an element to check it against.
		b.items = append(b.items, &xdm.Node{
			Kind: xdm.KindAttribute, Name: name, Value: value,
		})
		return nil
	}
	// Adding an attribute after children exist is an error the spec calls out,
	// because it usually means the stylesheet's instruction order is wrong.
	if len(b.open.Children) > 0 {
		return fmt.Errorf("XTDE0410: attribute %q added after the element already has children",
			name.Lexical())
	}
	// A repeated attribute replaces the earlier one rather than duplicating.
	for _, a := range b.open.Attrs {
		if a.Name.URI == name.URI && a.Name.Local == name.Local {
			a.Value = value
			return nil
		}
	}
	b.open.AddAttr(&xdm.Node{Kind: xdm.KindAttribute, Name: name, Value: value})
	return nil
}

// startElement opens a new element, returning a builder scoped to it.
func (b *outputBuilder) startElement(name xdm.QName) *outputBuilder {
	el := &xdm.Node{Kind: xdm.KindElement, Name: name}
	b.appendNode(el)
	return &outputBuilder{open: el, parent: b, tree: b.tree}
}

// sequence returns the accumulated items.
func (b *outputBuilder) sequence() xdm.Sequence { return b.items }

// toTree wraps the accumulated items in a document node, which is what a
// variable with content produces.
func (b *outputBuilder) toTree() *xdm.Node {
	tree := xdm.NewTree()
	for _, it := range b.items {
		if n, ok := it.(*xdm.Node); ok {
			tree.Root.AppendChild(detach(n))
		} else if a, ok := it.(*xdm.Atomic); ok {
			tree.Root.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: a.String()})
		}
	}
	tree.Finalize()
	return tree.Root
}

// --- Instruction execution helpers -----------------------------------------

// execSequence runs a sequence constructor into out.
func execSequence(body []Instruction, rt *runtime, out *outputBuilder) error {
	for _, instr := range body {
		if err := rt.ctx.Err(); err != nil {
			return err
		}
		// A variable declared mid-sequence is in scope for the instructions
		// that follow it, so it rebinds the runtime for the rest of the loop
		// rather than only for its own execution.
		if v, ok := instr.(*varInstr); ok {
			val, err := evalVariable(v.v, rt)
			if err != nil {
				return err
			}
			rt = rt.withVar(v.v.Name, val)
			continue
		}
		if err := instr.Execute(rt, out); err != nil {
			return err
		}
	}
	return nil
}

// evalVariable computes a variable's value from its select expression or its
// content.
func evalVariable(v *Variable, rt *runtime) (xdm.Sequence, error) {
	seq, err := evalVariableRaw(v, rt)
	if err != nil {
		return nil, err
	}
	// A variable or parameter whose value will not convert to its declared
	// type is XTTE0570, not the generic XPTY0004.
	return v.AsType.convertAs(seq, "$"+v.Name.Lexical(), "XTTE0570")
}

// evalVariableRaw computes the value before the "as" declaration is applied.
func evalVariableRaw(v *Variable, rt *runtime) (xdm.Sequence, error) {
	if v.Select != nil {
		return v.Select.Eval(rt.ctx)
	}
	if len(v.Body) == 0 {
		// A variable with neither select nor content is the empty string, not
		// the empty sequence: this is what makes <xsl:variable name="x"/>
		// usable as "".
		//
		// With an "as" declaration the rule is the other way. Section 9.3's
		// table gives "value is an empty sequence, provided the as attribute
		// permits an empty sequence" for that row, so a zero-length string
		// would fail every declaration that is not a string type — including
		// the document-node()? that the empty body was written for.
		if v.AsType != nil {
			return nil, nil
		}
		return xdm.One(xdm.NewString("")), nil
	}
	// Building a variable's content is temporary output state.
	sub := *rt
	sub.temporary = true
	out := newOutputBuilder()
	if err := execSequence(v.Body, &sub, out); err != nil {
		return nil, err
	}
	// Section 9.3's table: with an "as" attribute the value is the sequence
	// the constructor produced, adjusted to the required type. Only *without*
	// one is a document node built to hold it.
	//
	// The difference is observable and large. as="element()*" over a body of
	// three literal elements is those three elements; wrapping them in a
	// document node made the value a single node that does not match the
	// declared type at all, so the variable failed rather than binding.
	if v.AsType != nil {
		return out.sequence(), nil
	}
	// Content otherwise builds a temporary tree rooted at a document node.
	return xdm.One(out.toTree()), nil
}

// stringJoin renders a sequence as a separated string, which is what
// xsl:value-of and attribute value templates produce.
func stringJoin(seq xdm.Sequence, sep string) string {
	parts := make([]string, 0, len(seq))
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			parts = append(parts, v.StringValue())
		case *xdm.Atomic:
			parts = append(parts, v.String())
		}
	}
	return strings.Join(parts, sep)
}

// newRuntime builds a runtime for one transform.
func newRuntime(s *Stylesheet, ctx context.Context, root *xdm.Node, opts TransformOptions) (*runtime, error) {
	maxDepth := opts.MaxDepth
	if maxDepth == 0 {
		maxDepth = DefaultMaxDepth
	}
	rt := &runtime{
		sheet:     s,
		maxDepth:  maxDepth,
		keyIndex:  map[keyCacheKey]map[string]xdm.Sequence{},
		tunnel:    map[string]xdm.Sequence{},
		messages:  new([]string),
		secondary: new([]SecondaryResult),
	}

	xctx := xpath.NewContext(root, s.funcs)
	xctx.Ctx = ctx
	xctx.Docs = opts.Documents
	xctx.Collections = opts.Collections
	xctx.ImplicitTimezone = opts.ImplicitTimezone
	// The static base URI of every expression in the stylesheet. Without it
	// a relative reference in fn:doc or fn:resolve-uri has nothing to
	// resolve against when there is no context node — which is the case for
	// a transform started from a named template.
	xctx.StaticBaseURI = s.baseURI
	// One clock reading per transform, so fn:current-dateTime is stable
	// across every call the stylesheet makes.
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	xctx = xctx.WithNow(now)
	rt.ctx = xctx

	// The key() and current() functions need the runtime, so they are bound
	// per transform rather than living in the shared builtin library.
	lib := xpath.NewLibrary(s.funcs)
	registerRuntimeFuncs(lib, rt)
	rt.ctx.Funcs = lib

	// Global variables are evaluated in dependency order rather than
	// declaration order. Section 9.5 puts no ordering constraint on
	// declarations, so a global may legitimately be declared above the one it
	// refers to; evaluating in declaration order made that a spurious
	// "undeclared variable" instead of working. A variable is evaluated when
	// something needs it, and the ones nothing needs are evaluated at the end
	// so that their errors are still reported.
	if err := rt.evalGlobals(s, opts); err != nil {
		return nil, err
	}
	return rt, nil
}

// evalGlobals binds every global variable, resolving dependencies on demand.
func (rt *runtime) evalGlobals(s *Stylesheet, opts TransformOptions) error {
	byName := make(map[string]*Variable, len(s.globals))
	for _, g := range s.globals {
		if _, dup := byName[g.Name.Clark()]; !dup {
			byName[g.Name.Clark()] = g
		}
	}

	// state tracks which globals are done and which are being evaluated, so
	// that a cycle is reported rather than recursed into forever.
	const (
		pending = 0
		active  = 1
		done    = 2
	)
	state := make(map[string]int, len(s.globals))

	var bind func(g *Variable) error
	bind = func(g *Variable) error {
		key := g.Name.Clark()
		switch state[key] {
		case done:
			return nil
		case active:
			// XTDE0640: a global variable whose value depends on its own.
			return fmt.Errorf(
				"XTDE0640: global variable $%s depends on itself",
				g.Name.Lexical())
		}
		state[key] = active
		defer func() { state[key] = done }()

		if supplied, ok := opts.Params[key]; ok {
			rt.ctx = rt.ctx.WithVar(g.Name, supplied)
			return nil
		}
		if g.Required {
			return fmt.Errorf("XTDE0050: required parameter $%s was not supplied",
				g.Name.Lexical())
		}

		// Everything this variable refers to is bound first, so that its own
		// evaluation finds each one already in the context.
		for _, dep := range globalRefs(g) {
			d, ok := byName[dep]
			if !ok {
				continue
			}
			if d == g {
				// A variable whose select expression names itself is the
				// simplest circularity there is, and the recursion below
				// would never reach it: bind() has already marked this one
				// active, so the cycle is reported here directly.
				return fmt.Errorf(
					"XTDE0640: global variable $%s depends on itself",
					g.Name.Lexical())
			}
			if err := bind(d); err != nil {
				return err
			}
		}

		val, err := evalVariable(g, rt)
		if err != nil {
			return fmt.Errorf("evaluating global $%s: %w", g.Name.Lexical(), err)
		}
		rt.ctx = rt.ctx.WithVar(g.Name, val)
		return nil
	}

	for _, g := range s.globals {
		if err := bind(g); err != nil {
			return err
		}
	}
	return nil
}

// globalRefs returns the names of the variables a global's select expression
// refers to.
//
// The scan is lexical rather than over the parsed tree. What it is for is
// *ordering*: binding a dependency before the variable that needs it. Naming
// one variable too many only evaluates something earlier than strictly
// necessary, which is harmless, and naming one too few leaves the old
// behaviour, which the cycle check still catches. A visitor over every
// expression node would be more precise and buy nothing.
//
// Names are returned unprefixed-Clark, because that is how a global is keyed
// when its name is in no namespace — the overwhelmingly common case. A
// prefixed reference simply does not match and is left to declaration order.
func globalRefs(g *Variable) []string {
	if g.Select == nil {
		return nil
	}
	src := g.Select.Source()
	var out []string
	var quote byte
	for i := 0; i < len(src); i++ {
		c := src[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c != '$' {
			continue
		}
		j := i + 1
		for j < len(src) && (src[j] == ':' || src[j] == '-' || src[j] == '_' ||
			src[j] == '.' ||
			(src[j] >= 'a' && src[j] <= 'z') ||
			(src[j] >= 'A' && src[j] <= 'Z') ||
			(src[j] >= '0' && src[j] <= '9')) {
			j++
		}
		if j > i+1 {
			out = append(out, xdm.QName{Local: src[i+1 : j]}.Clark())
		}
		i = j - 1
	}
	return out
}
