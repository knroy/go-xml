package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// compileSequence compiles the children of el as a sequence constructor.
func (c *compiler) compileSequence(el, nsScope *xdm.Node) ([]Instruction, error) {
	return c.compileNodes(el.Children, nsScope)
}

// compileSequenceFrom compiles the element children of el starting at the
// given element index, used to skip leading xsl:param declarations.
func (c *compiler) compileSequenceFrom(el, nsScope *xdm.Node, fromElem int) ([]Instruction, error) {
	// Convert the element index into a child index so that interleaved text
	// nodes are not dropped.
	seen, start := 0, 0
	for i, ch := range el.Children {
		if ch.Kind != xdm.KindElement {
			continue
		}
		if seen == fromElem {
			start = i
			break
		}
		seen++
		start = i + 1
	}
	if fromElem == 0 {
		start = 0
	}
	return c.compileNodes(el.Children[start:], nsScope)
}

func (c *compiler) compileNodes(nodes []*xdm.Node, nsScope *xdm.Node) ([]Instruction, error) {
	var out []Instruction
	for _, n := range nodes {
		instr, err := c.compileNode(n, nsScope)
		if err != nil {
			return nil, err
		}
		if instr != nil {
			out = append(out, instr)
		}
	}
	return out, nil
}

// compileNode compiles one node of a sequence constructor.
func (c *compiler) compileNode(n *xdm.Node, nsScope *xdm.Node) (Instruction, error) {
	switch n.Kind {
	case xdm.KindText:
		// Whitespace-only text between instructions is discarded; anything
		// else is a literal text node. Without this every indented stylesheet
		// would emit its own indentation into the result.
		if xdm.IsXMLWhitespace(n.Value) {
			return nil, nil
		}
		return &textInstr{text: n.Value}, nil

	case xdm.KindComment:
		// Comments in the stylesheet are not copied to the output.
		return nil, nil

	case xdm.KindPI:
		return nil, nil

	case xdm.KindElement:
		if n.Name.URI == xdm.NSXSL {
			return c.compileXSLInstruction(n)
		}
		return c.compileLiteralElement(n)
	}
	return nil, nil
}

// compileLiteralElement compiles a literal result element: an element in the
// stylesheet that is copied to the output with its attributes evaluated as
// attribute value templates.
func (c *compiler) compileLiteralElement(n *xdm.Node) (Instruction, error) {
	sets, err := parseUseAttributeSets(n)
	if err != nil {
		return nil, err
	}
	instr := &literalElemInstr{name: n.Name, attrSets: sets}

	// Namespace declarations on the literal element are copied to the result,
	// minus the XSLT namespace itself and anything listed in
	// exclude-result-prefixes.
	excluded := map[string]bool{}
	for _, p := range strings.Fields(n.AttrValue("exclude-result-prefixes")) {
		excluded[p] = true
	}
	if v := n.Attr(xdm.NSXSL, "exclude-result-prefixes"); v != nil {
		for _, p := range strings.Fields(v.Value) {
			excluded[p] = true
		}
	}
	for _, ns := range n.Namespaces {
		if ns.Value == xdm.NSXSL || excluded[ns.Name.Local] || excluded["#all"] {
			continue
		}
		instr.namespaces = append(instr.namespaces,
			nsBinding{prefix: ns.Name.Local, uri: ns.Value})
	}

	for _, a := range n.Attrs {
		// xsl:-prefixed attributes on a literal element are directives, not
		// output attributes.
		if a.Name.URI == xdm.NSXSL {
			continue
		}
		avt, err := compileAVT(a.Value, newNSResolver(n, ""))
		if err != nil {
			return nil, fmt.Errorf("in attribute %s: %w", a.Name.Lexical(), err)
		}
		instr.attrs = append(instr.attrs, attrTemplate{name: a.Name, value: avt})
	}

	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	instr.body = body
	return instr, nil
}

// compileXSLInstruction dispatches on the instruction name.
func (c *compiler) compileXSLInstruction(n *xdm.Node) (Instruction, error) {
	ns := newNSResolver(n, "")

	switch n.Name.Local {
	case "value-of":
		return c.compileValueOf(n, ns)
	case "text":
		return &textInstr{text: n.StringValue()}, nil
	case "apply-templates":
		return c.compileApplyTemplates(n, ns)
	case "call-template":
		return c.compileCallTemplate(n, ns)
	case "for-each":
		return c.compileForEach(n, ns)
	case "for-each-group":
		return c.compileForEachGroup(n, ns)
	case "if":
		return c.compileIf(n, ns)
	case "choose":
		return c.compileChoose(n, ns)
	case "variable":
		v, err := c.compileVariable(n)
		if err != nil {
			return nil, err
		}
		return &varInstr{v: v}, nil
	case "element":
		return c.compileElement(n, ns)
	case "attribute":
		return c.compileAttribute(n, ns)
	case "comment":
		body, err := c.compileSequence(n, n)
		if err != nil {
			return nil, err
		}
		return &commentInstr{body: body}, nil
	case "processing-instruction":
		return c.compilePI(n, ns)
	case "copy":
		return c.compileCopy(n, ns)
	case "copy-of":
		sel, err := requiredExpr(n, "select", ns)
		if err != nil {
			return nil, err
		}
		spec, err := compileValidation(n, "")
		if err != nil {
			return nil, err
		}
		return &copyOfInstr{sel: sel, validation: spec}, nil
	case "sequence":
		sel, err := requiredExpr(n, "select", ns)
		if err != nil {
			return nil, err
		}
		return &sequenceInstr{sel: sel}, nil
	case "message":
		return c.compileMessage(n, ns)
	case "analyze-string":
		return c.compileAnalyzeString(n, ns)
	case "number":
		return c.compileNumber(n, ns)
	case "namespace":
		return c.compileNamespace(n, ns)
	case "perform-sort":
		return c.compilePerformSort(n, ns)
	case "next-match":
		params, _, err := c.compileParamsAndSorts(n, ns)
		if err != nil {
			return nil, err
		}
		return &nextMatchInstr{params: params}, nil
	case "apply-imports":
		params, _, err := c.compileParamsAndSorts(n, ns)
		if err != nil {
			return nil, err
		}
		return &nextMatchInstr{applyImports: true, params: params}, nil
	case "document":
		body, err := c.compileSequence(n, n)
		if err != nil {
			return nil, err
		}
		return &blockInstr{body: body}, nil
	case "result-document":
		return c.compileResultDocument(n, ns)
	case "fallback":
		// xsl:fallback is instantiated only when its containing instruction is
		// one the processor does not recognise. Every instruction that reaches
		// here is recognised, so the fallback is correctly *not* run — and,
		// unlike the misplaced elements below, a stray xsl:fallback is legal
		// and simply produces nothing. Saxon-HE 12.4 agrees: both engines
		// render "A<xsl:fallback>B</xsl:fallback>C" as "AC".
		return nil, nil
	case "param", "sort", "with-param", "when", "otherwise",
		"matching-substring", "non-matching-substring", "output-character":
		// These are only meaningful inside a specific parent, and each parent
		// reads them directly from its children rather than compiling them as
		// instructions. Reaching this case therefore means the element is in
		// the wrong place — an xsl:when outside an xsl:choose, an xsl:sort
		// outside a sortable instruction.
		//
		// Returning nil here silently dropped it, so forgetting the enclosing
		// xsl:choose produced an empty result and no error. Saxon reports
		// XTSE0010 for all of these, and so does this.
		return nil, fmt.Errorf(
			"XTSE0010: xsl:%s is not allowed here; it belongs inside %s",
			n.Name.Local, enclosingElementFor(n.Name.Local))
	}
	return nil, fmt.Errorf("unsupported instruction xsl:%s", n.Name.Local)
}

func (c *compiler) compileValueOf(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	instr := &valueOfInstr{separator: " ", hasSeparator: false}
	if s := n.AttrValue("separator"); s != "" {
		instr.separator, instr.hasSeparator = s, true
	}
	if sel := n.AttrValue("select"); sel != "" {
		comp, err := xpath.Compile(sel, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:value-of/@select: %w", err)
		}
		instr.sel = comp
		return instr, nil
	}
	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	instr.body = body
	return instr, nil
}

func (c *compiler) compileApplyTemplates(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	instr := &applyTemplatesInstr{}
	if sel := n.AttrValue("select"); sel != "" {
		comp, err := xpath.Compile(sel, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:apply-templates/@select: %w", err)
		}
		instr.sel = comp
	}
	if m := strings.TrimSpace(n.AttrValue("mode")); m != "" {
		instr.mode = m
	}
	params, sorts, err := c.compileParamsAndSorts(n, ns)
	if err != nil {
		return nil, err
	}
	instr.params, instr.sorts = params, sorts
	return instr, nil
}

func (c *compiler) compileCallTemplate(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	name := n.AttrValue("name")
	if name == "" {
		return nil, fmt.Errorf("xsl:call-template requires a name attribute")
	}
	qn, err := resolveQNameAttr(n, name)
	if err != nil {
		return nil, err
	}
	params, _, err := c.compileParamsAndSorts(n, ns)
	if err != nil {
		return nil, err
	}
	return &callTemplateInstr{name: qn, params: params}, nil
}

// compileParamsAndSorts collects xsl:with-param and xsl:sort children.
func (c *compiler) compileParamsAndSorts(n *xdm.Node, ns xpath.NamespaceResolver) ([]*Variable, []*sortKey, error) {
	var params []*Variable
	var sorts []*sortKey
	for _, ch := range n.ChildElements() {
		switch {
		case isXSL(ch, "with-param"):
			v, err := c.compileVariable(ch)
			if err != nil {
				return nil, nil, err
			}
			params = append(params, v)
		case isXSL(ch, "sort"):
			s, err := c.compileSort(ch)
			if err != nil {
				return nil, nil, err
			}
			sorts = append(sorts, s)
		}
	}
	return params, sorts, nil
}

func (c *compiler) compileSort(n *xdm.Node) (*sortKey, error) {
	ns := newNSResolver(n, "")
	s := &sortKey{order: "ascending", dataType: "text"}

	sel := n.AttrValue("select")
	if sel == "" {
		sel = "." // sorting on the item itself is the default
	}
	comp, err := xpath.Compile(sel, ns)
	if err != nil {
		return nil, fmt.Errorf("in xsl:sort/@select: %w", err)
	}
	s.sel = comp

	// order, data-type and case-order are attribute value templates in the
	// spec, but a sort key whose direction varies per item is meaningless, so
	// only the static form is supported.
	if v := n.AttrValue("order"); v != "" {
		s.order = v
	}
	if v := n.AttrValue("data-type"); v != "" {
		s.dataType = v
	}
	if v := n.AttrValue("case-order"); v != "" {
		if v != "upper-first" && v != "lower-first" {
			return nil, fmt.Errorf("invalid xsl:sort/@case-order %q", v)
		}
		s.caseOrder = v
	}
	// @lang orders accented and cased letters by the conventions of that
	// language: Swedish puts "ä" after "z", where codepoint order puts it
	// next to "a". The collator is built at compile time so that an
	// unrecognised tag is reported before the transform runs.
	if v := strings.TrimSpace(n.AttrValue("lang")); v != "" {
		coll, err := newCollator(v)
		if err != nil {
			return nil, err
		}
		s.coll = coll
	}
	// @collation names a collation by URI. Codepoint and the ASCII
	// case-insensitive collation are implemented; a language-sensitive
	// ordering is spelled with @lang instead. Anything else is refused rather
	// than accepted and then sorted by codepoint anyway.
	if v := strings.TrimSpace(n.AttrValue("collation")); v != "" {
		// The attribute is an attribute value template, so a stylesheet may
		// compute the collation: collation="{$c}" is legal, and resolving it
		// here would refuse the literal braces as an unknown collation URI.
		// A literal value is still resolved now, so a typo is a compile-time
		// error rather than a surprise at run time.
		a, err := compileAVT(v, ns)
		if err != nil {
			return nil, fmt.Errorf("xsl:sort/@collation: %w", err)
		}
		s.collAVT = a
		if a.isLit {
			lit := a.literal
			c, err := xpath.ResolveCollation(lit)
			if err != nil {
				return nil, fmt.Errorf("xsl:sort/@collation: %w", err)
			}
			s.strColl = c
		}
	}
	return s, nil
}

func (c *compiler) compileForEach(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	sel, err := requiredExpr(n, "select", ns)
	if err != nil {
		return nil, err
	}
	_, sorts, err := c.compileParamsAndSorts(n, ns)
	if err != nil {
		return nil, err
	}
	// xsl:sort children are not part of the body.
	body, err := c.compileNodes(nonSortChildren(n), n)
	if err != nil {
		return nil, err
	}
	return &forEachInstr{sel: sel, sorts: sorts, body: body}, nil
}

func (c *compiler) compileIf(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	test, err := requiredExpr(n, "test", ns)
	if err != nil {
		return nil, err
	}
	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	return &ifInstr{test: test, body: body}, nil
}

func (c *compiler) compileChoose(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	instr := &chooseInstr{}
	for _, ch := range n.ChildElements() {
		switch {
		case isXSL(ch, "when"):
			test, err := requiredExpr(ch, "test", newNSResolver(ch, ""))
			if err != nil {
				return nil, err
			}
			body, err := c.compileSequence(ch, ch)
			if err != nil {
				return nil, err
			}
			instr.whens = append(instr.whens, chooseBranch{test: test, body: body})
		case isXSL(ch, "otherwise"):
			body, err := c.compileSequence(ch, ch)
			if err != nil {
				return nil, err
			}
			instr.otherwise = body
		}
	}
	if len(instr.whens) == 0 {
		return nil, fmt.Errorf("xsl:choose requires at least one xsl:when")
	}
	return instr, nil
}

func (c *compiler) compileElement(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	nameAVT, err := requiredAVT(n, "name", ns)
	if err != nil {
		return nil, err
	}
	sets, err := parseUseAttributeSets(n)
	if err != nil {
		return nil, err
	}
	instr := &elementInstr{name: nameAVT, scope: n, attrSets: sets}
	if instr.validation, err = compileValidation(n, ""); err != nil {
		return nil, err
	}
	if v := n.AttrValue("namespace"); v != "" {
		avt, err := compileAVT(v, ns)
		if err != nil {
			return nil, err
		}
		instr.namespace = avt
	}
	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	instr.body = body
	return instr, nil
}

func (c *compiler) compileAttribute(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	nameAVT, err := requiredAVT(n, "name", ns)
	if err != nil {
		return nil, err
	}
	instr := &attributeInstr{name: nameAVT, scope: n}
	if instr.validation, err = compileValidation(n, ""); err != nil {
		return nil, err
	}
	if v := n.AttrValue("namespace"); v != "" {
		avt, err := compileAVT(v, ns)
		if err != nil {
			return nil, err
		}
		instr.namespace = avt
	}
	if sel := n.AttrValue("select"); sel != "" {
		comp, err := xpath.Compile(sel, ns)
		if err != nil {
			return nil, err
		}
		instr.sel = comp
		return instr, nil
	}
	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	instr.body = body
	return instr, nil
}

func (c *compiler) compilePI(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	nameAVT, err := requiredAVT(n, "name", ns)
	if err != nil {
		return nil, err
	}
	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	return &piInstr{name: nameAVT, body: body}, nil
}

func (c *compiler) compileCopy(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	sets, err := parseUseAttributeSets(n)
	if err != nil {
		return nil, err
	}
	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	spec, err := compileValidation(n, "")
	if err != nil {
		return nil, err
	}
	return &copyInstr{attrSets: sets, body: body, validation: spec}, nil
}

func (c *compiler) compileMessage(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	instr := &messageInstr{}
	if v := n.AttrValue("terminate"); v != "" {
		avt, err := compileAVT(v, ns)
		if err != nil {
			return nil, err
		}
		instr.terminate = avt
	}
	if sel := n.AttrValue("select"); sel != "" {
		comp, err := xpath.Compile(sel, ns)
		if err != nil {
			return nil, err
		}
		instr.sel = comp
	}
	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	instr.body = body
	return instr, nil
}

// --- small helpers ----------------------------------------------------------

func requiredExpr(n *xdm.Node, attr string, ns xpath.NamespaceResolver) (*xpath.Compiled, error) {
	v := n.AttrValue(attr)
	if v == "" {
		return nil, fmt.Errorf("%s requires a %s attribute", n.Name.Lexical(), attr)
	}
	comp, err := xpath.Compile(v, ns)
	if err != nil {
		return nil, fmt.Errorf("in %s/@%s: %w", n.Name.Lexical(), attr, err)
	}
	return comp, nil
}

func requiredAVT(n *xdm.Node, attr string, ns xpath.NamespaceResolver) (*avt, error) {
	v := n.AttrValue(attr)
	if v == "" {
		return nil, fmt.Errorf("%s requires a %s attribute", n.Name.Lexical(), attr)
	}
	return compileAVT(v, ns)
}

// nonSortChildren returns the children of n excluding xsl:sort and
// xsl:with-param elements, which the enclosing instruction consumes itself.
func nonSortChildren(n *xdm.Node) []*xdm.Node {
	var out []*xdm.Node
	for _, ch := range n.Children {
		if ch.Kind == xdm.KindElement && ch.Name.URI == xdm.NSXSL &&
			(ch.Name.Local == "sort" || ch.Name.Local == "with-param") {
			continue
		}
		out = append(out, ch)
	}
	return out
}

// compileResultDocument compiles xsl:result-document.
//
// The serialisation settings are resolved here rather than at run time because
// @format names a declaration that exists at compile time; only @href is an
// attribute value template, since a stylesheet routinely computes one output
// file per input node.
func (c *compiler) compileResultDocument(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	instr := &resultDocumentInstr{}

	// The output definition is selected when the instruction runs rather than
	// here. xsl:output is a top-level declaration, and section 3.13 puts no
	// ordering constraint on top-level elements, so the unnamed definition
	// may be declared below the template that uses it — and an imported
	// module's definitions merge in later still. Copying c.sheet.output at
	// this point captured whatever had been seen so far, which for a
	// stylesheet that declares its output after its templates was nothing.
	if v := n.AttrValue("format"); v != "" {
		qn, err := resolveQNameAttr(n, v)
		if err != nil {
			return nil, err
		}
		instr.format = qn.Clark()
	}
	// Serialisation attributes written on the instruction itself override
	// whatever the selected definition supplies, so they are kept separately
	// and applied over it at run time.
	instr.overrides = n

	if v := n.AttrValue("href"); v != "" {
		a, err := compileAVT(v, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:result-document/@href: %w", err)
		}
		instr.href = a
	}

	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	instr.body = body
	return instr, nil
}

// enclosingElementFor names the parent an element belongs inside, so that the
// error says what to do rather than only what is wrong.
func enclosingElementFor(local string) string {
	switch local {
	case "when", "otherwise":
		return "xsl:choose"
	case "sort":
		return "xsl:apply-templates, xsl:for-each, xsl:for-each-group or xsl:perform-sort"
	case "with-param":
		return "xsl:apply-templates, xsl:call-template, xsl:apply-imports or xsl:next-match"
	case "param":
		return "xsl:template, xsl:function or xsl:stylesheet"
	case "matching-substring", "non-matching-substring":
		return "xsl:analyze-string"
	case "output-character":
		return "xsl:character-map"
	}
	return "its defining parent"
}

// isSupportedSortCollation reports whether xsl:sort/@collation names a
// collation this engine implements. The relative forms are accepted because
// stylesheets write them, and the case-insensitive one is now supported as
// well as codepoint.
func isSupportedSortCollation(uri string) bool {
	_, err := xpath.ResolveCollation(uri)
	return err == nil
}
