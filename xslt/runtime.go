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

	// funcResults memoises the results of stylesheet functions declared
	// new-each-time="no". Section 10.3 makes that a promise that two calls
	// with the same arguments return the SAME result, which for a function
	// building nodes means the same nodes rather than merely equal ones --
	// so the promise is only kept by evaluating once and reusing. Keyed by
	// functionCallKey; see apply.go.
	funcResults map[string]xdm.Sequence
	ctx         *xpath.Context

	// deferredErr holds the failure of a global whose evaluation is not by
	// itself the transform's failure -- an abstract variable, whose body
	// raises XTDE3052. The error is kept against the name so that a
	// reference to it raises what the reference deserves, and a transform
	// that never mentions the name raises nothing. See evalGlobals.
	deferredErr []deferredGlobal

	// globalCtx is the context as it stood once the global variables were
	// bound, holding those and nothing local. xsl:attribute-set bodies are
	// evaluated against it; see the comment where it is set.
	globalCtx *xpath.Context

	// keyIndex caches xsl:key lookups per document. Building an index is a
	// full document scan, so it is done once per (key, document) pair on
	// first use rather than eagerly for every declared key.
	keyIndex map[keyCacheKey]map[string]xdm.Sequence

	// globalActive names the global variables whose initialiser is currently
	// being evaluated, by declared local name.
	//
	// A global is not in scope within its own binding, so a reference to one
	// from inside its own evaluation resolves to nothing and is reported as
	// XPST0008. That is the right answer for a reference written in the
	// variable's own select expression, but not for one reached indirectly:
	// error-0640e's key match pattern reads $p while $p's initialiser is
	// building that very key's index, which is a circularity -- XTDE0640 --
	// rather than an undeclared name. Pattern matching recovers from an
	// ordinary error, so without knowing which names are mid-evaluation the
	// cycle was swallowed and the transform ran to completion.
	globalActive map[string]bool

	// keyBuilding marks the (key, document) pairs whose index is currently
	// being built. keyIndex is only written once a build has *finished*, so
	// a key whose match or use expression calls key() for a name already
	// under construction would re-enter the builder and recurse until the
	// depth guard fired — reporting XPDY0001 where XTDE0640 is due. 5.7:
	// "it is a non-recoverable dynamic error if the use or match attribute
	// of an xsl:key declaration contains a call to the key function".
	keyBuilding map[keyCacheKey]bool

	// accumValues caches each accumulator's value at every node of a tree,
	// and accumBuilding guards the circular case. Both mirror keyIndex and
	// keyBuilding, and for the same reason: computing one value means
	// walking everything before it, so the walk is done once per pair.
	accumValues   map[accumCacheKey]*accumulatorValues
	accumBuilding map[accumCacheKey]bool
	// accumOrigin maps a node produced by a copy-accumulators="yes" copy to
	// the node it was copied from, which is the only thing that can say what
	// an accumulator's value at the copy should be. It is a map on the
	// runtime rather than a field on the node so that copying leaves the
	// tree itself untouched.
	accumOrigin map[*xdm.Node]*xdm.Node

	// treeAccums records, per document root, which accumulators 18.2.2 makes
	// applicable to that tree. Only a document read by an
	// xsl:merge-source/@for-each-source populates it — that is the one place
	// this engine can say the set is anything narrower than "all of them" —
	// and a root that is absent from the map is unrestricted.
	//
	// The map is shared with every derived runtime because the runtime struct
	// is copied by value: a document loaded inside an xsl:merge must stay
	// restricted for the whole of the action that reads it.
	treeAccums map[*xdm.Node]*modeAccumulators

	// streamedTrees records the roots that xsl:source-document was asked to
	// read in streamed mode, which XTDE3362 bars a non-streamable accumulator
	// from being read over.
	streamedTrees map[*xdm.Node]bool

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

	// baseOutputURI is TransformOptions.BaseOutputURI, kept for resolving a
	// relative xsl:result-document/@href and for the value
	// fn:current-output-uri reports while the principal result is being
	// written.
	baseOutputURI string

	// secondary collects xsl:result-document outputs. Like messages it is a
	// pointer, because the runtime struct is copied on every focus change:
	// a plain slice would leave a result-document written inside a template
	// appending to a copy that the caller never sees.
	secondary *[]SecondaryResult

	// baseURIUsed records that an xsl:result-document claimed the base output
	// URI — the one an absent or empty @href names. The principal result tree
	// has that URI too, so a stylesheet that writes to both is producing two
	// documents at one URI, which is XTDE1490. It is a pointer for the same
	// reason secondary is: the runtime is copied on every focus change.
	baseURIUsed *bool

	// readDocs is the set of absolute URIs the transformation has read, for
	// XTDE1500. A pointer for the same reason secondary and baseURIUsed are:
	// the runtime is copied on every focus change, and a document read in one
	// template must be visible to an xsl:result-document in another.
	readDocs *map[string]bool

	// messages collects xsl:message output rather than writing to stderr.
	//
	// It is a pointer to a slice because the runtime struct is copied on
	// every focus change and template dispatch; a plain slice would leave
	// each copy appending to its own, and messages emitted inside a template
	// would never reach the caller.
	messages *[]string

	// warnings collects the recoverable-condition warnings xsl:mode asks for,
	// and is a pointer for the same reason messages is.
	warnings *[]string

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
	// item is the item the rule matched, kept so xsl:next-match can tell
	// whether it is still looking at it. Section 6.7 makes the current
	// template rule and the context item two separate conditions, and an
	// instruction that changes the focus without ending the rule leaves the
	// first satisfied and the second not; see nextMatchInstr.Execute.
	item xdm.Item
}

type keyCacheKey struct {
	name string
	tree *xdm.Tree
	// pkg is the package whose declarations of the name built this index.
	// 3.5.5 makes a key local to its package, so two packages declaring one
	// name index the same tree differently and an index cached on the name
	// alone returned whichever package asked first to both. override-misc-004
	// declares "k" over the element's content in the used package and over
	// its name in the using one.
	pkg int
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
	var item xdm.Item
	if rt.ctx != nil {
		item = rt.ctx.Item
	}
	n.sel = selection{
		template: t, next: next, mode: mode,
		params: params, tunnels: tunnels, item: item,
	}
	return &n
}

func (rt *runtime) withVar(name xdm.QName, val xdm.Sequence) *runtime {
	n := *rt
	n.ctx = rt.ctx.WithVar(name, val)
	return &n
}

// --- Output construction ----------------------------------------------------

// The result-tree builder now lives in xdmbuild, which knows nothing of
// XSLT: the construction rules in XSLT 3.0 §5.7.1 and XQuery 3.1 §3.9.1.3
// are the same text, and the handful of places the two languages differ
// arrive through xdmbuild.Policy. xsltPolicy below is that half.

// --- Instruction execution helpers -----------------------------------------

// execSequence runs a sequence constructor into out.
func execSequence(body []Instruction, rt *runtime, out *outputBuilder) error {
	// A constructor holding xsl:on-empty or xsl:on-non-empty cannot be run
	// left to right: whether either fires depends on what the whole
	// constructor produced, so it goes to the section 8.4.4 algorithm
	// instead. See onempty.go.
	if hasConditionalContent(body) {
		return execConditionalSequence(body, rt, out)
	}
	for _, instr := range body {
		if err := rt.ctx.Err(); err != nil {
			return err
		}
		// A variable declared mid-sequence is in scope for the instructions
		// that follow it, so it rebinds the runtime for the rest of the loop
		// rather than only for its own execution.
		if v, ok := unwrapInstr(instr).(*varInstr); ok {
			if v.unused {
				// Nothing after this declaration can name the variable, so
				// section 5.2's permission not to evaluate it applies and
				// the binding is skipped entirely. Forcing it here made
				// param-0301 report XTDE0640 for a circularity its own
				// comment says must not be reported, because the value the
				// variable would have taken is never demanded.
				continue
			}
			val, err := evalVariable(v.v, rt)
			if err != nil {
				return err
			}
			rt = rt.withVar(v.v.Name, val)
			continue
		}
		if err := instr.Execute(rt, out); err != nil {
			// Where the error was raised, for $err:line-number. Only the
			// innermost instruction to see it records anything; see
			// srcpos.go.
			return stampPosition(err, instr)
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
	return v.asType.convertAs(seq, "$"+v.Name.Lexical(), "XTTE0570")
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
		if v.asType != nil {
			return nil, nil
		}
		return xdm.One(xdm.NewString("")), nil
	}
	// Building a variable's content is temporary output state.
	sub := rt.temporaryOutput()
	out := newOutputBuilder()
	if err := execSequence(v.Body, sub, out); err != nil {
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
	if v.asType != nil {
		return out.Sequence(), nil
	}
	// Content otherwise builds a temporary tree rooted at a document node,
	// whose base URI is the one in force at the declaration. Leaving it empty
	// made fn:base-uri return nothing for every node in a temporary tree.
	tree, err := out.ToDocument()
	if err != nil {
		return nil, err
	}
	tree.BaseURI = v.baseURI
	// The document node's base is known only now, after its content was
	// built, so the children are rebased against it here rather than as they
	// were appended. Without this a copied element with a relative xml:base
	// keeps the base of the document it came from, and a copied element with
	// none keeps it too, instead of inheriting the temporary tree's.
	for _, ch := range tree.Children {
		rebase(ch, tree.BaseURI)
	}
	return xdm.One(tree), nil
}

// clearCurrentRule returns a runtime with the current template rule cleared.
//
// Section 5.2's table names what clears it: "xsl:for-each,
// xsl:for-each-group, and xsl:analyze-string, and calls on stylesheet
// functions. Also cleared while evaluating global variables or default values
// of stylesheet parameters, and the sequence constructors contained in
// xsl:key and xsl:sort."
//
// It exists for XTDE0560, which is an error "if xsl:apply-imports or
// xsl:next-match is evaluated when the current template rule is null". Both
// instructions resume the search that the current rule interrupted, and once
// an xsl:for-each has changed the focus there is no such search to resume —
// the node being processed is no longer the one any template rule matched.
func (rt *runtime) clearCurrentRule() *runtime {
	sub := *rt
	sub.sel = selection{mode: rt.sel.mode, tunnels: rt.sel.tunnels}
	return &sub
}

// temporaryOutput returns a runtime in temporary output state.
//
// XSLT 3.0 section 19.1 lists which instructions switch it on: "xsl:variable,
// xsl:param, xsl:with-param, xsl:function, xsl:key, xsl:sort,
// xsl:accumulator-rule, and xsl:merge-key always evaluate the instructions in
// their contained sequence constructor in temporary output state." Each of
// those calls this before executing its body, so that an xsl:result-document
// anywhere beneath is XTDE1480 however deep the call chain.
//
// The copy is by value because the state is a property of the evaluation, not
// of the runtime: the caller's own state must be unchanged when the body
// returns.
func (rt *runtime) temporaryOutput() *runtime {
	sub := *rt
	sub.temporary = true
	return &sub
}

// temporaryOutputBefore30 is temporaryOutput for the six instructions XSLT
// 2.0 put in that list and XSLT 3.0 took out again: xsl:attribute,
// xsl:comment, xsl:processing-instruction, xsl:namespace, xsl:value-of and
// xsl:message.
//
// All six build a string rather than a tree, so there was never a final
// result tree for a nested xsl:result-document to be written to -- which is
// what XTDE1480 is about. 3.0 decided the restriction bought nothing, since
// the nested instruction's own output goes to its own destination, and
// result-document-1130 is the stylesheet that walks all of them.
func (rt *runtime) temporaryOutputBefore30() *runtime {
	if rt.sheet != nil && (rt.sheet.maxVersion == 0 || rt.sheet.maxVersion >= 3.0) {
		return rt
	}
	return rt.temporaryOutput()
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

// constructedText renders a sequence constructor's result as the string
// content of an attribute, comment, processing instruction or namespace node.
//
// This is section 5.7.2 verbatim: zero-length text nodes are dropped,
// adjacent text nodes are merged, the sequence is atomized, and the resulting
// strings are joined with the separator. The merge step is what makes the
// separator behave as the specification's own example describes — five text
// nodes concatenate to "12345" while five atomic values become "1 2 3 4 5" —
// so it cannot be skipped by joining the raw items.
func constructedText(seq xdm.Sequence, sep string) string {
	var parts []string
	inText := false
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			if v.Kind == xdm.KindText {
				if v.Value == "" {
					continue
				}
				if inText {
					parts[len(parts)-1] += v.Value
					continue
				}
				parts = append(parts, v.Value)
				inText = true
				continue
			}
			// The sequence is atomized and every atomic value is then cast
			// to a string (XSLT 2.0 section 11.4.3). For an UNTYPED node the
			// typed value is its string value, so reading StringValue()
			// directly was right; for a schema-annotated node it is not. An
			// attribute annotated xs:integer whose lexical form is "003" has
			// the typed value 3, and casting that to a string gives "3" —
			// the canonical form — not the "003" that was written. Reading
			// the string value skipped atomization entirely and so always
			// produced the lexical form.
			for _, a := range xdm.Atomize(xdm.Sequence{v}) {
				if at, ok := a.(*xdm.Atomic); ok {
					parts = append(parts, at.String())
				}
			}
			inText = false
		case *xdm.Atomic:
			parts = append(parts, v.String())
			inText = false
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
		sheet:       s,
		maxDepth:    maxDepth,
		keyIndex:    map[keyCacheKey]map[string]xdm.Sequence{},
		keyBuilding: map[keyCacheKey]bool{},

		accumValues:   map[accumCacheKey]*accumulatorValues{},
		accumBuilding: map[accumCacheKey]bool{},
		accumOrigin:   map[*xdm.Node]*xdm.Node{},
		treeAccums:    map[*xdm.Node]*modeAccumulators{},
		streamedTrees: map[*xdm.Node]bool{},
		tunnel:        map[string]xdm.Sequence{},
		funcResults:   map[string]xdm.Sequence{},
		messages:      new([]string),
		warnings:      new([]string),
		secondary:     new([]SecondaryResult),
		baseURIUsed:   new(bool),
		baseOutputURI: opts.BaseOutputURI,
	}

	// A transform started from a named template has no source document, and
	// root is then a nil *xdm.Node. Handing that straight to NewContext puts
	// a non-nil interface holding a nil pointer in Context.Item: the "is
	// there a focus" tests all read true, and the first axis step to
	// dereference it panics instead of raising XPDY0002. The nil is widened
	// to a genuinely nil interface so that absence of a context item is
	// represented the one way the rest of the engine checks for it.
	var item xdm.Item
	if root != nil {
		item = root
	}
	// xsl:global-context-item use="absent" declares that the transformation
	// reads no global context item, so the globals are evaluated without one
	// however the transform was invoked. 3.10 leaves it open whether
	// supplying an item anyway is itself an error, and this takes the option
	// of not making it one -- but ignoring the DECLARATION is a different
	// thing from ignoring the item, and doing both left the declaration
	// meaning nothing. glob-cxt-item-003 declares use="absent" over a global
	// selecting /doc and requires that global to fail; it accepts XPDY0002,
	// which is what a global with no focus raises on its own.
	if g := s.globalContextItem; g != nil && g.decl.use == "absent" {
		item = nil
	}
	xctx := xpath.NewContext(item, s.funcs)
	// The regular-expression dialect follows the processor, not the module.
	// A pattern is a string read by fn:matches at the point of call rather
	// than by the parser, so a version="2.0" stylesheet run by a 3.0
	// processor may legitimately write "(?:...)" -- the regex-syntax set is
	// exactly that, 2.0 stylesheets scoped XSLT30+. Raising RegexVersion
	// rather than Version keeps every other 3.0 construct gated on the
	// module's own declaration, which is what the syntax rules require.
	if s.maxVersion == 0 || s.maxVersion >= 3.0 {
		xctx.RegexVersion = xpath.XPath31
		// Which functions exist follows the processor for the same reason:
		// calling one is ordinary syntax at every version, and only the name
		// has to resolve. A 3.0 processor running a version="2.0" stylesheet
		// must find fn:path for it, which accessor-050 and its siblings
		// require. The module's own version still governs the grammar.
		xctx.LibraryVersion = xpath.XPath31
	}
	xctx.Ctx = ctx
	xctx.Docs = opts.Documents
	xctx.Collections = opts.Collections
	xctx.Texts = opts.Texts
	// fn:json-to-xml with validate=true needs the schema layer to type the
	// tree it builds, and reaches it through this hook rather than by
	// importing xsd from xpath, which the dependency direction forbids. It is
	// installed unconditionally: whether the processor *can* validate is a
	// property of the processor, and this one always can — F&O 3.1 §17.5.3
	// reserves FOJS0004 for a processor that cannot. Whether the stylesheet
	// may then write "instance of element(j:map, j:mapType)" is the separate
	// question xsl:import-schema answers.
	xctx.Validator = jsonTreeValidator{}
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
	// A variable reference resolves against the package the expression was
	// written in, so that two packages' globals of one name stay distinct.
	// See globalBindingName.
	rt.ctx.QualifyVar = func(c *xpath.Context, name xdm.QName) xdm.QName {
		return rt.qualifyGlobal(c, name)
	}
	// A reference to a global whose evaluation was deferred and failed
	// raises that failure, rather than the XPST0008 an unbound name would
	// otherwise report. See runtime.deferredErr.
	rt.ctx.MissingVar = func(c *xpath.Context, name xdm.QName) error {
		return rt.deferredError(c, name)
	}

	// The key() and current() functions need the runtime, so they are bound
	// per transform rather than living in the shared builtin library.
	lib := xpath.NewLibrary(s.funcs)
	registerRuntimeFuncs(lib, rt)
	registerOutputFuncs(lib)
	// The grouping, merge and position accessors go in here too, rather than
	// after the globals are bound, because a global may hold a *reference* to
	// one: for-each-group-078 writes `<xsl:variable name="f"
	// select="current-group#0"/>`, and a named function reference resolves
	// against the library in force where it is written. Registered later,
	// that was XPST0017 for a function this engine has. They read their state
	// through variable bindings that no global has yet, so one *called* from
	// a global still reports the XTDE1061 it should.
	registerGroupingFuncs(lib)
	registerMergeFuncs(lib)
	registerFormatNumber(lib, s)
	registerPositionFuncs(lib)
	rt.ctx.Funcs = packageScopedLibrary{inner: lib, sheet: s}

	// Global variables are evaluated in dependency order rather than
	// declaration order. Section 9.5 puts no ordering constraint on
	// declarations, so a global may legitimately be declared above the one it
	// refers to; evaluating in declaration order made that a spurious
	// "undeclared variable" instead of working. A variable is evaluated when
	// something needs it, and the ones nothing needs are evaluated at the end
	// so that their errors are still reported.
	// Bind the runtime before the globals are evaluated, not after. A global
	// variable's select expression may call a stylesheet function, and
	// xsl:function reaches the runtime through this binding — evaluating the
	// globals first left such a call reporting that it was made outside a
	// transform.
	rt.ctx = rt.ctx.WithVar(runtimeVar,
		xdm.One(&xdm.Opaque{Label: "runtime", Value: rt}))

	if err := rt.evalGlobals(s, opts); err != nil {
		return nil, err
	}
	// Section 10.2: only top-level variables and parameters are in scope
	// within an xsl:attribute-set declaration — a set is a declaration, not
	// part of the template that uses it, so a local variable at the point of
	// use must not be visible inside it. Snapshotting the context here, once
	// the globals are bound and before any template has pushed a local scope,
	// is what lets the set body be evaluated in that scope.
	rt.globalCtx = rt.ctx
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
		// Keyed by the BINDING name, not the declared one: two packages may
		// each declare a global of the same name and both are live, so the
		// bare name marked one done and skipped the other entirely. See
		// globalBindingName and use-package-175.
		key := globalBindingName(g.Name, g.pkg).Clark()
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
		// The DECLARED local name, which is what a reference inside a match
		// pattern is written with and all recoverPatternError can compare.
		if rt.globalActive == nil {
			rt.globalActive = map[string]bool{}
		}
		rt.globalActive[g.Name.Local] = true
		defer delete(rt.globalActive, g.Name.Local)

		// A static declaration was bound before static analysis began. Its
		// value cannot depend on anything the run supplies, and a static
		// xsl:param has already had its one chance to be set — through
		// CompileOptions.StaticParams, which is where the caller supplies a
		// value that has to be in hand before the stylesheet is analysed.
		if g.isStatic {
			rt.bindGlobal(g, g.staticValue)
			return nil
		}
		// A caller names a parameter by its declared name, not by the
		// package-qualified one the binding uses.
		if supplied, ok := opts.Params[g.Name.Clark()]; ok {
			rt.bindGlobal(g, supplied)
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
				// A global variable is not in the scope of its own binding:
				// XSLT 3.0 §9.1 gives the scope of a global xsl:variable as
				// every stylesheet module in the package *except* the
				// variable's own select expression and sequence constructor.
				// A reference to the name from there is therefore a
				// reference to nothing at all, and the static error for an
				// unbound variable reference is XPST0008 rather than the
				// XTDE0640 that a genuine circularity between two distinct
				// declarations raises.
				//
				// higher-order-functions-070 writes the case that makes the
				// distinction visible: $gcd's select is an inline function
				// whose body calls $gcd, which "would make sense" as
				// recursion and is still an error because the name is not
				// in scope. The recursion below would never reach it -
				// bind() has already marked this one active - so it is
				// reported here directly.
				return fmt.Errorf(
					"XPST0008: undeclared variable $%s: a global variable is "+
						"not in scope within its own binding",
					g.Name.Lexical())
			}
			if err := bind(d); err != nil {
				return err
			}
		}

		val, err := evalVariable(g, globalRuntimeFor(rt, g))
		// globalRefs orders the obvious dependencies, but it only reads the
		// select expression: a reference reached through a sequence
		// constructor, a match pattern or the body of a stylesheet function
		// is invisible to it, and shows up here as XPST0008 for a name that
		// is in fact a declared global. Which of the two things that means
		// is decided by the state of the name it could not resolve.
		for err != nil {
			dep, ok := unresolvedGlobal(err, byName)
			if !ok {
				break
			}
			if state[dep.Name.Clark()] == active {
				// The name is a global already under evaluation further up
				// this same call chain, so its value depends on itself.
				// Section 3.10 makes a circularity in a stylesheet
				// XTDE0640, and reporting the reference as undeclared hid
				// the cycle behind a static-error code.
				return fmt.Errorf(
					"XTDE0640: global variable $%s depends on itself",
					dep.Name.Lexical())
			}
			// Not a cycle, merely an order globalRefs could not see. Bind
			// the dependency and evaluate this variable again; bind() is
			// idempotent through the done state, so the retry converges —
			// each pass either finishes or moves one more name to done.
			if berr := bind(dep); berr != nil {
				return berr
			}
			val, err = evalVariable(g, globalRuntimeFor(rt, g))
		}
		if err != nil {
			// A global xsl:param with an "as" type, no explicit default and
			// no supplied value takes the empty sequence as its default.
			// Section 10.1.1: if the empty sequence is not a valid instance
			// of the required type the parameter is treated as required, so
			// the caller supplying nothing is XTDE0610 rather than the type
			// error the conversion itself reports. This is the same rule
			// that governs template parameters in runTemplate.
			if g.IsParam && g.asType != nil && !hasExplicitDefault(g) {
				return fmt.Errorf("%s: no value was supplied for parameter $%s, "+
					"and the empty sequence is not a valid instance of %s",
					missingParamCode(rt.sheet),
					g.Name.Lexical(), g.asType.source())
			}
			// Only a failure of the *type conversion itself* becomes
			// XTTE0600. Evaluating the default can fail for reasons that
			// have nothing to do with the declared type — a schema
			// validation error inside the default's sequence constructor
			// carries its own code, and rebranding that as a type error
			// reported XTTE0600 where the suite expects XTTE1510.
			if g.IsParam && g.asType != nil &&
				strings.HasPrefix(err.Error(), "XTTE0570") {
				return fmt.Errorf("evaluating global $%s: %w",
					g.Name.Lexical(), recodeError(err, "XTTE0600"))
			}
			return fmt.Errorf("evaluating global $%s: %w", g.Name.Lexical(), err)
		}
		rt.bindGlobal(g, val)
		return nil
	}

	for _, g := range s.globals {
		// A deferred global is bound like any other, but its failure is not
		// the transform's failure: 3.5.3.2 makes XTDE3052 the error for an
		// invocation that "is evaluated", so a variable nothing refers to
		// must raise nothing. The value is simply left unbound, and a
		// reference to it -- which is an invocation, and so is exactly the
		// case the error is for -- raises when it is evaluated.
		//
		// accept-042 hides an abstract v1 and never mentions it, while the
		// v1-proxy that selects it is public and equally unreferenced;
		// accept-043b and -043c refer to the proxy and still expect
		// XTDE3052. Binding eagerly failed the first pair and skipping
		// entirely failed the second, because an unbound name reports
		// XPST0008 rather than the error the reference deserves.
		if err := bind(g); err != nil {
			if g.deferred {
				rt.deferredErr = append(rt.deferredErr, deferredGlobal{
					name:     globalBindingName(g.Name, g.pkg),
					declared: g.Name,
					err:      err,
				})
				continue
			}
			return err
		}
	}
	return nil
}

// unresolvedGlobal reports whether err is an XPST0008 naming a variable that
// the stylesheet does in fact declare globally, and if so returns that
// declaration.
//
// The name is recovered from the message rather than from a typed error
// because XPST0008 is raised in xpath, where nothing knows what an XSLT
// global is. A message that is not this shape, or that names something no
// global declares, yields false and is left to be reported as it stands.
func unresolvedGlobal(err error, byName map[string]*Variable) (*Variable, bool) {
	const marker = "XPST0008: undeclared variable $"
	msg := err.Error()
	i := strings.Index(msg, marker)
	if i < 0 {
		return nil, false
	}
	name := msg[i+len(marker):]
	if j := strings.IndexAny(name, " :\t\n"); j >= 0 {
		name = name[:j]
	}
	if name == "" {
		return nil, false
	}
	g, ok := byName[xdm.QName{Local: name}.Clark()]
	return g, ok
}

// qualifyGlobal is the VarQualifier that resolves a reference against the
// package it was written in.
//
// The reference and the binding need not carry the same package. A component
// of a used package is visible to the using package, so template "b" of
// use-package-175 -- declared in package B -- refers to a $v that B's copy of
// D declares. The reference carries B and the binding carries D.
//
// The search therefore starts at the referencing package and then tries the
// packages it uses, nearest first, which is the order visibility flows in.
// Falling off the end leaves the name unqualified, which is the top-level
// package's binding and the answer for an expression carrying no package.
func (rt *runtime) qualifyGlobal(c *xpath.Context, name xdm.QName) xdm.QName {
	pkg := packageOf(c)
	if pkg == 0 {
		return name
	}
	if q := globalBindingName(name, pkg); rt.hasGlobal(q) {
		return q
	}
	for _, used := range rt.sheet.packageUses[pkg] {
		if q := globalBindingName(name, used); rt.hasGlobal(q) {
			return q
		}
	}
	return name
}

// hasGlobal reports whether the stylesheet declares a global that binds under
// this exact name, which is what tells a qualified guess from a miss.
func (rt *runtime) hasGlobal(q xdm.QName) bool {
	for _, g := range rt.sheet.globals {
		if globalBindingName(g.Name, g.pkg) == q {
			return true
		}
	}
	return false
}

// bindGlobal puts a global's value in scope under the name its package gives
// it, and under the plain name as well while nothing else has claimed that.
//
// The plain binding is what an expression carrying no package resolves
// against -- a pattern, or a global of the top-level package naming one it
// uses. A later binding of the plain name legitimately shadows it; the
// qualified name is what keeps two packages' copies distinct.
func (rt *runtime) bindGlobal(g *Variable, val xdm.Sequence) {
	rt.ctx = rt.ctx.WithVar(globalBindingName(g.Name, g.pkg), val)
	if g.pkg != 0 {
		if _, taken := rt.ctx.LookupVar(g.Name); !taken {
			rt.ctx = rt.ctx.WithVar(g.Name, val)
		}
	}
}

// deferredError answers the failure recorded for a global that a reference
// has just failed to resolve, or nil where the name is unbound for some other
// reason and the ordinary XPST0008 is right.
//
// The reference is qualified the same way resolution qualifies it, so that a
// package's copy of a name answers for that package.
func (rt *runtime) deferredError(c *xpath.Context, name xdm.QName) error {
	if len(rt.deferredErr) == 0 {
		return nil
	}
	want := rt.qualifyGlobal(c, name)
	for _, d := range rt.deferredErr {
		if d.name == want || d.name == name {
			return d.err
		}
	}
	// The reference and the record need not agree on the package: the
	// reference is written in the using package and the declaration belongs
	// to the used one, and qualifyGlobal only bridges the two for a name
	// that IS bound -- which a deferred global, by construction, is not. The
	// declared name is what both agree on.
	for _, d := range rt.deferredErr {
		if d.declared == name {
			return d.err
		}
	}
	return nil
}

// deferredGlobal is a global whose evaluation failed and whose failure is
// owed to a reference rather than to the transform. See runtime.deferredErr.
type deferredGlobal struct {
	// name is the binding name, which is what a resolvable reference would
	// have found.
	name xdm.QName
	// declared is the name as written, which is what a reference from
	// another package agrees with; see deferredError.
	declared xdm.QName
	err      error
}

// globalBindingName is the name a global variable is bound under.
//
// Two packages may each declare a variable of the same name, and a diamond
// -- one package used by two routes, each overriding the same variable --
// puts two live bindings of one name in one stylesheet. The runtime binds
// into a flat, name-keyed scope, so the later binding shadowed the earlier
// and both routes read one value: use-package-175 gave "bbbbb" for the
// branch that had overridden the variable to "ccccc".
//
// A global declared in a used package is therefore bound under a name
// qualified by that package, which no stylesheet can write and so cannot
// collide with a declared one. The top-level package keeps the plain name:
// its globals are what an external caller supplies parameters for, and what
// an expression carrying no package resolves against.
func globalBindingName(name xdm.QName, pkg int) xdm.QName {
	if pkg == 0 {
		return name
	}
	return xdm.QName{
		URI:   fmt.Sprintf("%s%d/%s", packageVarNS, pkg, name.URI),
		Local: name.Local,
	}
}

// packageVarNS is the namespace a used package's globals are bound in. Like
// originalNS it is a URI no stylesheet can write.
const packageVarNS = "http://go-xml.invalid/xslt/package/"

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
		// The value comes from a sequence constructor. Its references were
		// collected at compile time, where the namespace bindings were still
		// available -- see bodyVariableRefs.
		return g.bodyRefs
	}
	// bodyRefs is non-empty here only for a global whose select expression
	// calls an xsl:function: foldFunctionRefsIntoGlobals put that function's
	// own references there, and they are dependencies of this global just as
	// much as the names it writes itself.
	return append(variableRefsIn(g.Select.Source(), g.selectNS), g.bodyRefs...)
}

// variableRefsIn returns the variable names src refers to, expanded through
// ns, which maps a prefix to the URI bound to it where src was written.
func variableRefsIn(src string, ns map[string]string) []string {
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
			ref := src[i+1 : j]
			if prefix, local, ok := strings.Cut(ref, ":"); ok {
				// A prefixed reference is expanded through the bindings in
				// force where it was written. Left unexpanded it matched no
				// global and fell back to declaration order, which is wrong
				// for $xsl:original: the renamed original is emitted after
				// the overriding declaration that refers to it.
				if uri, has := ns[prefix]; has {
					out = append(out, xdm.QName{URI: uri, Local: local}.Clark())
					i = j - 1
					continue
				}
			}
			out = append(out, xdm.QName{Local: ref}.Clark())
		}
		i = j - 1
	}
	return out
}

// bodyVariableRefs returns the globals a sequence constructor names, expanded
// to Clark names against the bindings in force where each was written.
//
// It walks the declaration's own subtree at compile time rather than the
// compiled []Instruction, because an Instruction's only method is Execute:
// recovering the expressions from it would mean a type switch over every
// instruction kind, and every new instruction would silently omit itself.
// The element tree still carries both the expression text and the namespace
// bindings it was written under, which is what expanding a prefix needs.
//
// Every attribute is scanned, not a list of the ones known to hold
// expressions. The scan exists to *order* bindings, and naming one variable
// too many only evaluates something earlier than it strictly had to be --
// harmless -- while naming one too few restores the bug this exists to fix.
// An attribute value template puts an expression inside braces in an
// attribute that holds no expression otherwise, so a list would have to
// enumerate those too.
func bodyVariableRefs(el *xdm.Node) []string {
	// A name the subtree binds for itself is not a dependency on a global of
	// that name, and treating it as one invents a circularity. param-0301 is
	// exactly that case: a global $x calls a function whose body declares a
	// local $x -- a circularity the specification requires to go unreported,
	// because nothing ever evaluates it.
	//
	// Collecting the bound names in a first pass over the whole subtree,
	// rather than tracking scope as the walk descends, is deliberately
	// blunt. Over-suppressing costs an ordering, which the runtime's
	// bind-on-demand retry then recovers; under-suppressing invents a cycle
	// that fails the transform outright.
	bound := map[string]bool{}
	var names func(n *xdm.Node)
	names = func(n *xdm.Node) {
		if n.Kind == xdm.KindElement && isXSL(n, "variable") ||
			n.Kind == xdm.KindElement && isXSL(n, "param") {
			if a := n.Attr("", "name"); a != nil {
				if qn, err := resolveQNameAttr(n, a.Value); err == nil {
					bound[qn.Clark()] = true
				}
			}
		}
		for _, c := range n.Children {
			names(c)
		}
	}
	for _, c := range el.Children {
		names(c)
	}

	seen := map[string]bool{}
	var out []string
	var walk func(n *xdm.Node)
	walk = func(n *xdm.Node) {
		if n.Kind == xdm.KindElement {
			ns := n.InScopeNamespaces()
			for _, a := range n.Attrs {
				for _, ref := range variableRefsIn(a.Value, ns) {
					if !seen[ref] && !bound[ref] {
						seen[ref] = true
						out = append(out, ref)
					}
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, c := range el.Children {
		walk(c)
	}
	return out
}

// globalRuntimeFor is the runtime a global variable's initialiser is evaluated
// in, which differs from the transform's only in its focus.
//
// 5.4.3: "For a global variable or the default value of a stylesheet
// parameter, the expression or sequence constructor specifying the variable
// value is evaluated with a singleton focus as follows: If the declaration
// appears within the top-level package (including within an xsl:override
// element in the top-level package), then the focus is based on the global
// context item if supplied, or absent otherwise. If the declaration appears
// within a library package, then the focus is absent."
//
// So the global context item reaches the top-level package's globals and
// nothing else. package-912's library package initialises a public variable
// with count(//*), which without this took the transform's source document as
// its focus and quietly returned a number where XPDY0002 was due.
func globalRuntimeFor(rt *runtime, g *Variable) *runtime {
	if g.pkg == 0 {
		return rt
	}
	// A declaration inside an xsl:override belongs to the package containing
	// the xsl:override, which is what overridingPackage already records on
	// the variable: a variable whose pkg is the top-level one reaches this
	// function only through the arm above.
	return rt.withFocus(nil, 0, 0)
}

// bodyFunctionCalls returns the Clark names of the functions a declaration's
// subtree calls, so a chain of calls can be followed when ordering globals.
//
// Like bodyVariableRefs the scan is lexical and deliberately generous: a name
// that is not really a function call costs a lookup in a map that does not
// hold it. What it must not do is miss one, because a missed call is a
// dependency that goes back to being ordered by luck.
func bodyFunctionCalls(el *xdm.Node) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(n *xdm.Node)
	walk = func(n *xdm.Node) {
		if n.Kind == xdm.KindElement {
			ns := n.InScopeNamespaces()
			for _, a := range n.Attrs {
				for _, c := range functionCallsIn(a.Value, ns) {
					if !seen[c] {
						seen[c] = true
						out = append(out, c)
					}
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(el)
	return out
}

// functionCallsIn returns the names written as "prefix:local(" in src,
// expanded through ns.
//
// Only a prefixed name is collected. An unprefixed call is a built-in, whose
// body is not a stylesheet's to depend on, and collecting it would mean
// matching every fn: name in every attribute for nothing.
func functionCallsIn(src string, ns map[string]string) []string {
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
		if !isNameStartByte(c) {
			continue
		}
		j := i
		for j < len(src) && isNameByte(src[j]) {
			j++
		}
		name := src[i:j]
		// Skip whitespace between the name and the parenthesis: "f:x (1)" is
		// a call, and XPath permits the space.
		k := j
		for k < len(src) && (src[k] == ' ' || src[k] == '\t' ||
			src[k] == '\n' || src[k] == '\r') {
			k++
		}
		if k < len(src) && src[k] == '(' {
			if prefix, local, ok := strings.Cut(name, ":"); ok {
				if uri, has := ns[prefix]; has {
					out = append(out, xdm.QName{URI: uri, Local: local}.Clark())
				}
			}
		}
		i = j - 1
	}
	return out
}

// isNameStartByte is the ASCII subset of the characters an NCName may begin
// with. isNameByte, which pattern30.go already defines for the same kind of
// lexical scan, covers the rest.
func isNameStartByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
