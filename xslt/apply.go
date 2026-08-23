package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// applyTemplatesInstr implements xsl:apply-templates.
type applyTemplatesInstr struct {
	sel    *xpath.Compiled
	mode   string
	params []*Variable
	sorts  []*sortKey
}

func (i *applyTemplatesInstr) Execute(rt *runtime, out *outputBuilder) error {
	var seq xdm.Sequence
	if i.sel != nil {
		v, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		seq = v
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
			// Atomic values in the selection are copied through, since there
			// is no template to match them against.
			if a, isAtomic := it.(*xdm.Atomic); isAtomic {
				out.appendValue(a)
			}
			continue
		}
		sub := rt.withCurrent(node, idx+1, size)
		if err := applyToNode(sub, node, i.mode, params, tunnels, out); err != nil {
			return err
		}
	}
	return nil
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
		return applyBuiltInRule(rt, node, mode, tunnels, out)
	}
	// Record the selection state so xsl:next-match and xsl:apply-imports in
	// the body can resume from here.
	sub := rt.withSelection(t, next, mode, params, tunnels)
	return runTemplate(sub, t, params, tunnels, out)
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

// findTemplateBelowPrecedence resumes the scan at the first template whose
// import precedence is strictly lower than p.
//
// This is xsl:apply-imports, which differs from xsl:next-match in exactly this
// respect: next-match takes the next template by any criterion, while
// apply-imports skips the whole current precedence level and goes to the
// stylesheet that was imported.
func (s *Stylesheet) findTemplateBelowPrecedence(node *xdm.Node, mode string,
	ctx *xpath.Context, p int) (*Template, int, error) {

	start := 0
	for start < len(s.templates) && s.templates[start].importPrecedence >= p {
		start++
	}
	return s.findTemplateFrom(node, mode, ctx, start)
}

// matchesMode reports whether the template applies in the given mode.
func (t *Template) matchesMode(mode string) bool {
	if len(t.Mode) == 0 {
		return mode == ""
	}
	for _, m := range t.Mode {
		if m == mode || m == "#all" {
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
func applyBuiltInRule(rt *runtime, node *xdm.Node, mode string,
	tunnels map[string]xdm.Sequence, out *outputBuilder) error {

	switch node.Kind {
	case xdm.KindDocument, xdm.KindElement:
		if err := rt.descend(); err != nil {
			return err
		}
		defer rt.ascend()
		size := len(node.Children)
		for idx, ch := range node.Children {
			sub := rt.withCurrent(ch, idx+1, size)
			if err := applyToNode(sub, ch, mode, nil, tunnels, out); err != nil {
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

	sub := rt
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
		if v, ok := params[key]; ok {
			sub = sub.withVar(p.Name, v)
			continue
		}
		if p.Tunnel {
			if v, ok := sub.tunnel[key]; ok {
				sub = sub.withVar(p.Name, v)
				continue
			}
		}
		if p.Required {
			return fmt.Errorf("XTDE0700: required parameter $%s was not supplied to template %s",
				p.Name.Lexical(), templateLabel(t))
		}
		// An unsupplied optional parameter takes its default value.
		val, err := evalVariable(p, sub)
		if err != nil {
			return err
		}
		sub = sub.withVar(p.Name, val)
	}

	return execSequence(t.Body, sub, out)
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
		// A function parameter whose supplied value will not convert to its
		// declared type is XTTE0590.
		v, err := p.AsType.convertAs(args[i], "parameter $"+p.Name.Lexical()+
			" of "+f.name.Lexical(), "XTTE0590")
		if err != nil {
			return nil, err
		}
		sub = sub.withVar(p.Name, v)
	}
	// A function body has no context item: referring to "." inside one is an
	// error, which is what stops functions from depending on hidden state.
	sub = sub.withFocus(nil, 0, 0)

	if err := sub.descend(); err != nil {
		return nil, err
	}
	defer sub.ascend()

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
		return fmt.Errorf("%s requires a node as the context item", name)
	}

	var (
		t   *Template
		nxt int
		err error
	)
	if i.applyImports {
		t, nxt, err = rt.sheet.findTemplateBelowPrecedence(
			node, rt.sel.mode, rt.ctx, rt.sel.template.importPrecedence)
	} else {
		t, nxt, err = rt.sheet.findTemplateFrom(node, rt.sel.mode, rt.ctx, rt.sel.next)
	}
	if err != nil {
		return err
	}

	// Parameters given here replace the originals for the resumed call; those
	// not mentioned carry over, which is what makes next-match usable as a
	// pass-through.
	params, tunnels, err := evalParams(rt, i.params)
	if err != nil {
		return err
	}
	if params == nil {
		params = rt.sel.params
	}
	if tunnels == nil {
		tunnels = rt.sel.tunnels
	}

	if t == nil {
		// Falling off the end of the template list lands on the built-in rule.
		return applyBuiltInRule(rt, node, rt.sel.mode, tunnels, out)
	}
	if err := rt.descend(); err != nil {
		return err
	}
	defer rt.ascend()
	sub := rt.withSelection(t, nxt, rt.sel.mode, params, tunnels)
	return runTemplate(sub, t, params, tunnels, out)
}
