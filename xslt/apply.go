package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// applyTemplatesInstr implements xsl:apply-templates.
type applyTemplatesInstr struct {
	sel *xpath.Compiled
	// mode is the mode to dispatch in. "#current" is kept as written and
	// resolved when the instruction runs, because it names whatever mode the
	// enclosing template rule was selected in — which is not known until
	// then.
	mode   string
	params []*Variable
	sorts  []*sortKey
	// atomicOK records that the instruction was written in an XSLT 3.0
	// module, where the selection may hold atomic values as well as nodes.
	// A 2.0 module must still get XTTE0520 for one: 2.0 has no pattern that
	// can match an atomic value, so accepting it would silently drop the
	// item the author meant to process.
	atomicOK bool
}

func (i *applyTemplatesInstr) Execute(rt *runtime, out *outputBuilder) error {
	var seq xdm.Sequence
	if i.sel != nil {
		v, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		seq = v
		// XTTE0520: in XSLT 2.0 the select expression must yield nodes. An
		// atomic value among them has no template to match it, and
		// processing it silently would drop it rather than report the
		// mistake. XSLT 3.0 added the ".[E]" pattern, which can match one,
		// so a 3.0 module dispatches on atomic values instead.
		if !i.atomicOK {
			for _, it := range seq {
				if _, ok := it.(*xdm.Node); !ok {
					return fmt.Errorf(
						"XTTE0520: xsl:apply-templates/@select returned an item "+
							"that is not a node (%s)", it.TypeName())
				}
			}
		}
	} else {
		// With no select, the default is the children of the context node —
		// which is what drives the recursive descent that XSLT is built on.
		node, ok := rt.ctx.Item.(*xdm.Node)
		if !ok {
			return fmt.Errorf("XTTE0510: xsl:apply-templates requires a node as the context item")
		}
		for _, ch := range node.Children {
			seq = append(seq, ch)
		}
	}

	if len(i.sorts) > 0 {
		var err error
		if seq, err = applySorts(rt, seq, i.sorts); err != nil {
			return err
		}
	}

	params, tunnels, err := evalParams(rt, i.params)
	if err != nil {
		return err
	}

	if err := rt.descend(); err != nil {
		return err
	}
	defer rt.ascend()

	size := len(seq)
	for idx, it := range seq {
		if err := rt.ctx.Err(); err != nil {
			return err
		}
		node, ok := it.(*xdm.Node)
		if !ok {
			// An atomic value is dispatched like a node, against the ".[E]"
			// patterns that can match one. Its built-in rule, section 6.7.1,
			// is to copy it to the result — which is also what a 2.0 module
			// did for the values it let through.
			sub := rt.withCurrent(it, idx+1, size)
			if err := applyToAtomic(sub, it, i.effectiveMode(rt),
				params, tunnels, out); err != nil {
				return err
			}
			continue
		}
		if err := rt.sheet.checkModeTyped(node, i.effectiveMode(rt)); err != nil {
			return err
		}
		sub := rt.withCurrent(node, idx+1, size)
		if err := applyToNode(sub, node, i.effectiveMode(rt), params, tunnels, out); err != nil {
			return err
		}
	}
	return nil
}

// effectiveMode resolves the two pseudo-modes.
//
// "#current" is the mode of the template rule currently executing. Inside an
// xsl:function or a named template invoked outside any rule there is none, and
// erratum XT.E19 settles that case as the unnamed mode rather than as an
// error — the runtime's selection state carries "" there, so it falls out.
func (i *applyTemplatesInstr) effectiveMode(rt *runtime) string {
	switch i.mode {
	case "#current":
		return rt.sel.mode
	case "#default":
		return ""
	}
	return i.mode
}

// applyToNode selects and runs the best-matching template for one node.
func applyToNode(rt *runtime, node *xdm.Node, mode string,
	params map[string]xdm.Sequence, tunnels map[string]xdm.Sequence,
	out *outputBuilder) error {

	t, next, err := rt.sheet.findTemplateFrom(node, mode, rt.ctx, 0)
	if err != nil {
		return err
	}
	if t == nil {
		rt.sheet.warnNoMatch(rt, node, mode)
		return applyBuiltInRule(rt, node, mode, params, tunnels, out)
	}
	if err := rt.sheet.warnMultipleMatch(
		rt, node, mode, t, next, rt.ctx); err != nil {
		return err
	}
	if err := rt.sheet.failMultipleMatch(
		node, mode, t, next, rt.ctx); err != nil {
		return err
	}
	// Record the selection state so xsl:next-match and xsl:apply-imports in
	// the body can resume from here.
	sub := rt.withSelection(t, next, mode, params, tunnels)
	// The node being processed is also what fn:current() returns for the
	// whole body. xsl:apply-templates binds it before it gets here, but the
	// very first invocation of the transform does not, and the fallback of
	// "current() is the context item" is wrong the moment evaluation descends
	// into a path step: in "$temp/a/b/id(@idref, current()//dummy)" the
	// fallback answered with the b the step was on rather than with the
	// template's own node, so the second argument selected nothing.
	//
	// Binding it here rather than at the call site covers every entry point
	// at once, and rebinding it to the same node the caller already bound
	// costs nothing.
	if node != nil {
		sub = sub.withCurrentNode(node)
	}
	return runTemplate(sub, t, params, tunnels, out)
}

// withCurrentNode records node as what fn:current() returns, without touching
// the focus. The focus is already node here; only the current-node binding may
// be missing.
func (rt *runtime) withCurrentNode(node *xdm.Node) *runtime {
	n := *rt
	n.ctx = rt.ctx.WithVar(currentVar, xdm.One(node))
	return &n
}

// findTemplate returns the highest-priority template matching node in mode.
//
// The template list is pre-sorted by (import precedence, priority,
// declaration order), so the first match is the winner and the scan stops
// there. Without the sort this would have to examine every template for every
// node.
func (s *Stylesheet) findTemplate(node *xdm.Node, mode string, ctx *xpath.Context) (*Template, error) {
	t, _, err := s.findTemplateFrom(node, mode, ctx, 0)
	return t, err
}

// findTemplateFrom resumes the scan at index start, returning the match and
// the index after it.
//
// The resume index is what xsl:next-match and xsl:apply-imports need: both
// re-run selection over the templates that lost the original conflict, and
// since the list is already sorted by (precedence, priority, declaration
// order), "the next best template" is simply the next match further down.
func (s *Stylesheet) findTemplateFrom(node *xdm.Node, mode string,
	ctx *xpath.Context, start int) (*Template, int, error) {

	for i := start; i < len(s.templates); i++ {
		t := s.templates[i]
		if !t.matchesMode(mode) {
			continue
		}
		ok, err := t.Match.Matches(node, ctx)
		if err != nil {
			return nil, 0, err
		}
		if ok {
			return t, i + 1, nil
		}
	}
	return nil, len(s.templates), nil
}

// findTemplateInImportTree finds the highest-priority template rule in the
// import tree of the module that declared the current one.
//
// This is xsl:apply-imports, which differs from xsl:next-match in exactly this
// respect: next-match takes the next template by any criterion, while
// apply-imports skips the whole current precedence level and goes to the
// stylesheet that was imported.
//
// Section 6.7 says apply-imports searches "the templates that were imported,
// directly or indirectly, by the stylesheet module containing the current
// template rule". That is narrower than "everything below the current
// precedence": two modules imported as SIBLINGS both rank below their
// importer, so a scan that only drops below the current precedence runs on
// into a sibling's import tree, which the current module never imported.
// import-1601 is exactly that shape — a template in a module importing
// nothing must fall back to the built-in rule, and instead reached a sibling's
// rules.
//
// The current module's import tree is the half-open precedence interval
// [low, p): compileModule numbers a module's whole import tree before the
// module itself takes a number, so those numbers are contiguous. The list is
// sorted by descending precedence, so the templates in range form one
// contiguous run and the scan can stop as soon as it falls below low.
func (s *Stylesheet) findTemplateInImportTree(node *xdm.Node, mode string,
	ctx *xpath.Context, low, p int) (*Template, int, error) {

	for i := 0; i < len(s.templates); i++ {
		t := s.templates[i]
		if t.importPrecedence >= p {
			continue
		}
		if t.importPrecedence < low {
			break
		}
		if !t.matchesMode(mode) {
			continue
		}
		ok, err := t.Match.Matches(node, ctx)
		if err != nil {
			return nil, 0, err
		}
		if ok {
			return t, i + 1, nil
		}
	}
	return nil, len(s.templates), nil
}

// matchesMode reports whether the template applies in the given mode.
func (t *Template) matchesMode(mode string) bool {
	if len(t.Mode) == 0 {
		return mode == ""
	}
	for _, m := range t.Mode {
		if m == "#all" {
			return true
		}
		// "#default" written on a template rule names the unnamed mode, so it
		// has to be compared against "" rather than against itself: a
		// stylesheet mixing mode="#default" with rules that omit @mode
		// otherwise puts them in two different modes.
		if m == "#default" {
			m = ""
		}
		if m == mode {
			return true
		}
	}
	return false
}

// applyBuiltInRule implements the built-in template rules, which apply when no
// user template matches.
//
// These are what make a stylesheet with a single template still produce
// output: element and document nodes recurse into their children, and text
// nodes copy themselves through. A stylesheet author who does not want the
// text leaking into the output has to override the text rule explicitly, which
// is a frequent source of surprise but is what the spec requires.
// params carries the non-tunnel parameters the rule was invoked with. Section
// 6.6: "If the built-in rule was invoked with parameters, those parameters are
// passed on in the implicit xsl:apply-templates instruction" — so a rule
// several levels down still sees a parameter supplied above an element the
// stylesheet wrote no rule for. Dropping them made those parameters take their
// defaults instead.
func applyBuiltInRule(rt *runtime, node *xdm.Node, mode string,
	params, tunnels map[string]xdm.Sequence, out *outputBuilder) error {

	// Section 6.6: xsl:mode/@on-no-match selects which set of built-in rules
	// is in force. text-only-copy is the default and the historical XSLT 1.0
	// and 2.0 behaviour, so it falls through to the switch below; the others
	// replace it wholesale.
	switch rt.sheet.modeNoMatch[mode] {
	case "deep-skip":
		// A document node is the exception: 6.7.4 gives it the rule
		//
		//   <xsl:template match="document-node()" mode="M">
		//     <xsl:apply-templates mode="#current"/>
		//   </xsl:template>
		//
		// and only "all items other than document nodes" do nothing. The
		// distinction is what spec bug #30219 changed, and mode-1437a is the
		// case it was changed for: with the document node skipped too,
		// nothing in the source was ever reached and a stylesheet whose only
		// rules match book and bktlong produced an empty result. Attributes
		// are not visited -- the rule has no select, so it selects children
		// only, and a document node has no attributes anyway.
		if node.Kind != xdm.KindDocument {
			return nil
		}
		return builtInDescend(rt, node, mode, params, tunnels, out, false)
	case "shallow-skip":
		// The node itself produces nothing, but its attributes and then its
		// children are still processed. The attributes are the part that
		// distinguishes this rule from text-only-copy: 6.7.5 writes it as
		//
		//   <xsl:template match="document-node()|element()" mode="M">
		//     <xsl:apply-templates select="@*" mode="#current"/>
		//     <xsl:apply-templates mode="#current"/>
		//   </xsl:template>
		//
		// where 6.7.1 gives text-only-copy an xsl:apply-templates with no
		// select at all -- children only, since an attribute is not a child.
		// Sharing one descent for both lost the attributes, and mode-0015
		// applies a rule matching @bar in a shallow-skip mode and expects it
		// to fire; it produced nothing at all.
		return builtInSkipDescend(rt, node, mode, params, tunnels, out)
	case "fail":
		// XTDE0555: with on-no-match="fail" the absence of a matching rule
		// is the error, so it is reported for the node that had none rather
		// than silently producing the default output.
		return fmt.Errorf(
			"XTDE0555: no template rule matches %s in mode %q, and the mode "+
				"declares on-no-match=\"fail\"", builtInNodeLabel(node), mode)
	case "deep-copy":
		// The whole subtree is copied, so there is no recursion: the copy
		// already carries the descendants a shallow rule would have
		// processed.
		out.appendNode(node)
		return nil
	case "shallow-copy", "copy":
		// "copy" is the XSLT 3.0 working-draft spelling that the suite still
		// carries; it means shallow-copy. The node is copied without its
		// content and the children are then processed into it, which is the
		// identity transform written as a mode declaration.
		return builtInShallowCopy(rt, node, mode, params, tunnels, out)
	}

	switch node.Kind {
	case xdm.KindDocument, xdm.KindElement:
		if err := rt.descend(); err != nil {
			return err
		}
		defer rt.ascend()
		size := len(node.Children)
		for idx, ch := range node.Children {
			sub := rt.withCurrent(ch, idx+1, size)
			if err := applyToNode(sub, ch, mode, params, tunnels, out); err != nil {
				return err
			}
		}
		return nil

	case xdm.KindText, xdm.KindAttribute:
		out.appendText(node.StringValue())
		return nil

	case xdm.KindComment, xdm.KindPI:
		// The built-in rule for comments and PIs produces nothing.
		return nil
	}
	return nil
}

// runTemplate executes a template with the supplied parameters.
func runTemplate(rt *runtime, t *Template,
	params map[string]xdm.Sequence, tunnels map[string]xdm.Sequence,
	out *outputBuilder) error {

	// An abstract template fails before anything else it declares is
	// applied. XTDE3052 is about the *invocation* -- "it is a dynamic error
	// if an invocation of an abstract component is evaluated" -- so the
	// error belongs to the call itself and not to whatever the missing body
	// would have needed. Checking parameters first reported XTDE0700 for the
	// declared but unsupplied $one of accept-906's t:abstract, which is a
	// complaint about a signature nothing was ever going to run.
	if t.abstract != "" {
		return fmt.Errorf(
			"XTDE3052: %s is abstract and has no implementation, so it "+
				"cannot be invoked", t.abstract)
	}

	// The xsl:context-item declaration is checked before anything else the
	// template does. A template that requires a context item and was called
	// without one must fail here rather than at whatever expression first
	// needs a focus, and a mistyped item must not be partly processed first.
	if t.contextItem != nil {
		var item xdm.Item
		if rt.ctx != nil && rt.ctx.Item != nil {
			item = rt.ctx.Item
		}
		if err := t.contextItem.check(item); err != nil {
			return err
		}
	}

	// 15.6: "All invocation constructs set the current merge group and current
	// merge key to absent." A template rule or named template invoked from
	// inside an xsl:merge-action is such a construct, so the two must not leak
	// into it — which is what makes current-merge-group() in a called template
	// the XTDE3480 that merge-087 and merge-088 require.
	sub := rt.clearMergeContext()
	// use="absent" means "the contained sequence constructor, and any
	// xsl:param elements, are evaluated with an absent focus". It is not an
	// error to call such a template with a focus -- the item is simply not
	// part of it -- so the focus is removed here rather than refused at the
	// call. A "." in the body is then XPDY0002, which is what makes the
	// declaration worth writing: it says the template does not read the item.
	if t.contextItem != nil && t.contextItem.use == "absent" &&
		sub.ctx != nil && sub.ctx.Item != nil {
		n := *sub
		c := sub.ctx.WithFocus(nil, 0, 0)
		n.ctx = c
		sub = &n
	}
	// Tunnel parameters flow through templates that do not declare them, so
	// they are merged into the runtime rather than matched against Params.
	if len(tunnels) > 0 {
		n := *sub
		merged := make(map[string]xdm.Sequence, len(sub.tunnel)+len(tunnels))
		for k, v := range sub.tunnel {
			merged[k] = v
		}
		for k, v := range tunnels {
			merged[k] = v
		}
		n.tunnel = merged
		sub = &n
	}

	for _, p := range t.Params {
		key := p.Name.Clark()
		// A tunnel parameter is supplied only through the tunnel. A
		// non-tunnel xsl:with-param of the same name does not bind it —
		// section 10.1.2 keeps the two channels separate — so a template
		// declaring tunnel="yes" keeps its default when the caller passes an
		// ordinary parameter of that name.
		if !p.Tunnel {
			if v, ok := params[key]; ok {
				v, err := bindParam(p, v, t)
				if err != nil {
					return err
				}
				sub = sub.withVar(p.Name, v)
				continue
			}
		} else if v, ok := sub.tunnel[key]; ok {
			v, err := bindParam(p, v, t)
			if err != nil {
				return err
			}
			sub = sub.withVar(p.Name, v)
			continue
		}
		if p.Required {
			return fmt.Errorf("XTDE0700: required parameter $%s was not supplied to template %s",
				p.Name.Lexical(), templateLabel(t))
		}
		// An unsupplied optional parameter takes its default value. Which
		// error a failure to convert that default raises depends on whether
		// the default was written down.
		//
		// Section 10.1.1 splits the two cases. With an explicit default — a
		// select attribute or a non-empty sequence constructor — a default
		// that will not convert to the required type is XTTE0600. With no
		// explicit default the default value is the empty sequence, and if
		// the empty sequence is not a valid instance of the required type
		// the parameter "is treated as a required parameter", so the caller
		// supplying nothing is XTDE0610 — not a type error at all. XSLT 3.0
		// renamed that code to XTDE0700; see missingParamCode.
		//
		// Both previously fell through to evalVariable's generic XTTE0570.
		val, err := evalVariable(p, sub)
		if err != nil {
			if p.asType != nil {
				if hasExplicitDefault(p) {
					// Only the type conversion itself is XTTE0600; an error
					// raised from inside the default's own evaluation keeps
					// the code it came with.
					if strings.HasPrefix(err.Error(), "XTTE0570") {
						return recodeError(err, "XTTE0600")
					}
					return err
				}
				return fmt.Errorf("%s: no value was supplied for parameter $%s of template %s, "+
					"and the empty sequence is not a valid instance of %s",
					missingParamCode(sub.sheet),
					p.Name.Lexical(), templateLabel(t), p.asType.source())
			}
			return err
		}
		sub = sub.withVar(p.Name, val)
	}

	if t.asType == nil {
		return execSequence(t.Body, sub, out)
	}

	// XTTE0505: with an "as" declaration the template's result is converted
	// to the required type, and a value that will not convert is an error.
	// The body builds into its own builder so that the conversion sees the
	// whole result rather than each instruction's contribution.
	tmp := newOutputBuilder()
	if err := execSequence(t.Body, sub, tmp); err != nil {
		return err
	}
	converted, err := t.asType.convertAs(tmp.sequence(),
		"result of template "+templateLabel(t), "XTTE0505")
	if err != nil {
		return err
	}
	// appendSequence rather than a switch of its own, so that a map, an array
	// or a function item survives. A template declared as="map(*)" returning
	// one had it dropped here, and the empty sequence left behind then failed
	// the CALLER's declaration -- higher-order-functions-071 reports
	// XTTE0570 against $result, several steps away from where the value
	// actually went missing.
	if err := appendSequence(converted, out); err != nil {
		return err
	}
	return nil
}

func templateLabel(t *Template) string {
	if t.HasName {
		return t.Name.Lexical()
	}
	if t.Match != nil {
		return "match=" + t.Match.String()
	}
	return "(anonymous)"
}

// evalParams evaluates xsl:with-param children, separating tunnel parameters.
func evalParams(rt *runtime, params []*Variable) (map[string]xdm.Sequence, map[string]xdm.Sequence, error) {
	if len(params) == 0 {
		return nil, nil, nil
	}
	normal := map[string]xdm.Sequence{}
	tunnel := map[string]xdm.Sequence{}
	for _, p := range params {
		v, err := evalVariable(p, rt)
		if err != nil {
			return nil, nil, fmt.Errorf("evaluating parameter $%s: %w", p.Name.Lexical(), err)
		}
		if p.Tunnel {
			tunnel[p.Name.Clark()] = v
		} else {
			normal[p.Name.Clark()] = v
		}
	}
	return normal, tunnel, nil
}

// callTemplateInstr implements xsl:call-template.
type callTemplateInstr struct {
	name   xdm.QName
	params []*Variable
	// compat records that the xsl:call-template was written in a 1.0 scope,
	// which exempts it from XTSE0680. See checkCallTemplateParams.
	compat bool
}

func (i *callTemplateInstr) Execute(rt *runtime, out *outputBuilder) error {
	t, ok := rt.sheet.named[i.name.Clark()]
	if !ok {
		return fmt.Errorf("XTSE0650: no template named %s", i.name.Lexical())
	}
	params, tunnels, err := evalParams(rt, i.params)
	if err != nil {
		return err
	}
	if err := rt.descend(); err != nil {
		return err
	}
	defer rt.ascend()
	// Unlike apply-templates, call-template does not change the focus: the
	// context node, position and size carry into the called template.
	return runTemplate(rt, t, params, tunnels, out)
}

// userFunction is a compiled xsl:function.
type userFunction struct {
	name    xdm.QName
	params  []*Variable
	body    []Instruction
	returns *sequenceType
}

// call adapts the function to the xpath.Function signature.
func (f *userFunction) call(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
	rt, ok := runtimeFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("xsl:function %s called outside a transform", f.name.Lexical())
	}
	if len(args) != len(f.params) {
		return nil, fmt.Errorf("XPST0017: %s expects %d arguments, got %d",
			f.name.Lexical(), len(f.params), len(args))
	}

	sub := rt
	for i, p := range f.params {
		// A declared parameter type converts the argument. Without this a
		// parameter declared "as=xs:decimal?" receives an untypedAtomic and
		// the body's arithmetic silently becomes double arithmetic.
		// A parameter to a *stylesheet function* whose supplied value will
		// not convert is XTTE0790, not XTTE0590. XTTE0590 covers the general
		// parameter case — an xsl:with-param supplied to a template — while
		// section 10.3's XTTE0790 is written specifically for "the value of a
		// parameter to a stylesheet function". Reporting 0590 here made
		// error-0790a report the template code for a function call.
		v, err := p.asType.convertAs(args[i], "parameter $"+p.Name.Lexical()+
			" of "+f.name.Lexical(), funcParamCode(rt.sheet))
		if err != nil {
			return nil, err
		}
		sub = sub.withVar(p.Name, v)
	}
	// A function body has no context item: referring to "." inside one is an
	// error, which is what stops functions from depending on hidden state.
	sub = sub.withFocus(nil, 0, 0)
	// Section 5.4's table lists what a call on a stylesheet function clears
	// as well as the focus: the current group, the current grouping key and
	// the current captured substrings.
	sub = sub.clearFunctionContext()

	if err := sub.descend(); err != nil {
		return nil, err
	}
	defer sub.ascend()

	// A stylesheet function's body builds a temporary tree, which is
	// temporary output state for XTDE1480.
	sub.temporary = true
	// Section 24.3: the current output URI is cleared while a stylesheet
	// function's body is evaluated.
	sub.ctx = sub.ctx.WithVar(outputURIVar, xdm.Empty())

	out := newOutputBuilder()
	if err := execSequence(f.body, sub, out); err != nil {
		return nil, err
	}
	// A stylesheet function whose result will not convert to its declared
	// type is XTTE0780.
	return f.returns.convertAs(out.sequence(), "result of "+f.name.Lexical(),
		"XTTE0780")
}

// nextMatchInstr implements xsl:next-match and xsl:apply-imports.
//
// Both re-dispatch the *same* node to a lower-ranked template, which is how a
// stylesheet layers behaviour: a template can add a wrapper and then delegate
// to whatever would have matched had it not existed. They differ only in where
// the resumed search starts, which is what applyImports selects.
type nextMatchInstr struct {
	applyImports bool
	params       []*Variable
}

func (i *nextMatchInstr) Execute(rt *runtime, out *outputBuilder) error {
	name := "xsl:next-match"
	if i.applyImports {
		name = "xsl:apply-imports"
	}
	if rt.sel.template == nil {
		return fmt.Errorf("XTDE0560: %s used outside a template rule", name)
	}
	node, ok := rt.ctx.Item.(*xdm.Node)
	if !ok {
		// XSLT 3.0 dispatches atomic values too, through the ".[E]" patterns,
		// so a rule matching one may delegate exactly as a node's rule does.
		return i.nextMatchAtomic(rt, out, name)
	}

	var (
		t   *Template
		nxt int
		err error
	)
	if i.applyImports {
		t, nxt, err = rt.sheet.findTemplateInImportTree(
			node, rt.sel.mode, rt.ctx,
			rt.sel.template.lowPrecedence, rt.sel.template.importPrecedence)
	} else {
		t, nxt, err = rt.sheet.findTemplateFrom(node, rt.sel.mode, rt.ctx, rt.sel.next)
	}
	if err != nil {
		return err
	}

	// Section 6.7: the instruction "may use xsl:with-param child elements to
	// pass parameters to the chosen template rule. It *also* passes on any
	// tunnel parameters." Those two and nothing else — an ordinary parameter
	// the current rule received does not carry over, so a parameter the
	// resumed template declares falls back to its own default.
	//
	// Carrying the non-tunnel parameters over made next-match a
	// pass-through for them, which is what a reader expects and not what the
	// specification says.
	params, tunnels, err := evalParams(rt, i.params)
	if err != nil {
		return err
	}
	if tunnels == nil {
		tunnels = rt.sel.tunnels
	}

	if t == nil {
		// Falling off the end of the template list lands on the built-in rule.
		return applyBuiltInRule(rt, node, rt.sel.mode, params, tunnels, out)
	}
	if err := rt.descend(); err != nil {
		return err
	}
	defer rt.ascend()
	sub := rt.withSelection(t, nxt, rt.sel.mode, params, tunnels)
	return runTemplate(sub, t, params, tunnels, out)
}

// builtInDescend applies the mode's rules to node's children, optionally
// copying text and attribute values through as the text-only rule does.
func builtInDescend(rt *runtime, node *xdm.Node, mode string,
	params, tunnels map[string]xdm.Sequence, out *outputBuilder,
	copyText bool) error {

	switch node.Kind {
	case xdm.KindDocument, xdm.KindElement:
		if err := rt.descend(); err != nil {
			return err
		}
		defer rt.ascend()
		size := len(node.Children)
		for idx, ch := range node.Children {
			sub := rt.withCurrent(ch, idx+1, size)
			if err := applyToNode(sub, ch, mode, params, tunnels, out); err != nil {
				return err
			}
		}
		return nil
	case xdm.KindText, xdm.KindAttribute:
		if copyText {
			out.appendText(node.StringValue())
		}
		return nil
	}
	return nil
}

// builtInSkipDescend is the on-no-match="shallow-skip" rule for a document or
// element node: apply templates to the attributes, then to the children,
// producing nothing for the node itself.
//
// A document node has no attributes, so the loop simply finds none there; the
// rule is written over both kinds because 6.7.5 words it that way, and the
// document case matters for the recursion rather than for the attributes.
// Every other kind of node produces the empty sequence, which is what falling
// off the switch does.
func builtInSkipDescend(rt *runtime, node *xdm.Node, mode string,
	params, tunnels map[string]xdm.Sequence, out *outputBuilder) error {

	if node.Kind != xdm.KindDocument && node.Kind != xdm.KindElement {
		return nil
	}
	if err := rt.descend(); err != nil {
		return err
	}
	defer rt.ascend()
	// Two separate xsl:apply-templates instructions, so position() restarts
	// at 1 for the children -- the same distinction 6.7.3's note draws about
	// the identity rule.
	for idx, a := range node.Attrs {
		an := rt.withCurrent(a, idx+1, len(node.Attrs))
		if err := applyToNode(an, a, mode, params, tunnels, out); err != nil {
			return err
		}
	}
	size := len(node.Children)
	for idx, ch := range node.Children {
		cn := rt.withCurrent(ch, idx+1, size)
		if err := applyToNode(cn, ch, mode, params, tunnels, out); err != nil {
			return err
		}
	}
	return nil
}

// builtInShallowCopy is the on-no-match="shallow-copy" rule: copy the node
// itself without its content, then process its children into the copy.
//
// An element's namespace nodes travel with it, exactly as xsl:copy carries
// them (11.9.1), because a copied element whose prefix was declared on an
// ancestor would otherwise lose the declaration its own name needs.
// Attributes do not: the rule processes them like any other child in the
// mode, and the built-in attribute rule is what puts their values back.
func builtInShallowCopy(rt *runtime, node *xdm.Node, mode string,
	params, tunnels map[string]xdm.Sequence, out *outputBuilder) error {

	switch node.Kind {
	case xdm.KindDocument:
		// 6.7.3 spells the rule out as <xsl:copy>...</xsl:copy>, and xsl:copy
		// over a document node constructs one rather than running the body
		// inline. Descending straight into the children lost that: the
		// difference is invisible in a result tree, where a document node's
		// children flatten into the parent either way, but a function
		// declared as="document-node()" gets its result rejected — which is
		// exactly what merge-096 does.
		sub := newOutputBuilder()
		if err := builtInDescend(rt, node, mode, params, tunnels, sub, false); err != nil {
			return err
		}
		doc, err := sub.toDocument()
		if err != nil {
			return err
		}
		// The DOCTYPE and the base URI travel with the copy for the same
		// reason they do in xsl:copy: the unparsed entity declarations and
		// anything resolved against the base would otherwise be lost.
		if src := node.Tree(); src != nil {
			if dst := doc.Tree(); dst != nil {
				dst.DocType = src.DocType
			}
		}
		if doc.BaseURI == "" {
			doc.BaseURI = node.BaseURI
		}
		out.appendNode(doc)
		return nil
	case xdm.KindElement:
		sub := out.startElement(node.Name)
		if out.open == nil && sub.open.BaseURI == "" {
			sub.open.BaseURI = node.BaseURI
		}
		copyNamespacesTo(sub, node)
		// The attributes are processed first so that they reach the element
		// before any child content closes it to them; section 6.7's rule
		// selects attributes as well as children.
		if err := rt.descend(); err != nil {
			return err
		}
		defer rt.ascend()
		for _, a := range node.Attrs {
			an := rt.withCurrent(a, 1, len(node.Attrs))
			if err := applyToNode(an, a, mode, params, tunnels, sub); err != nil {
				return err
			}
		}
		size := len(node.Children)
		for idx, ch := range node.Children {
			cn := rt.withCurrent(ch, idx+1, size)
			if err := applyToNode(cn, ch, mode, params, tunnels, sub); err != nil {
				return err
			}
		}
		return nil
	default:
		// Text, comments, processing instructions and attributes have no
		// content to descend into, so a shallow copy is the whole node.
		out.appendNode(node)
		return nil
	}
}

// builtInNodeLabel names a node for the on-no-match="fail" diagnostic.
func builtInNodeLabel(node *xdm.Node) string {
	switch node.Kind {
	case xdm.KindDocument:
		return "the document node"
	case xdm.KindElement:
		return "element " + node.Name.Lexical()
	case xdm.KindAttribute:
		return "attribute " + node.Name.Lexical()
	case xdm.KindText:
		return "a text node"
	case xdm.KindComment:
		return "a comment"
	case xdm.KindPI:
		return "processing instruction " + node.Name.Local
	}
	return "a node"
}
