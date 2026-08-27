package xslt

import (
	"fmt"
	"sort"
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
	// The cut is placed immediately after the last skipped element rather
	// than at the next one: the text between an xsl:param and the instruction
	// that follows it is part of the sequence constructor, and starting at
	// the following element silently swallowed it.
	seen, start := 0, 0
	for i, ch := range el.Children {
		if ch.Kind != xdm.KindElement {
			continue
		}
		if seen == fromElem {
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
	nodes = mergeAcrossComments(nodes)
	if err := checkOnEmptyPlacement(nodes); err != nil {
		return nil, err
	}
	var out []Instruction
	for i, n := range nodes {
		instr, err := c.compileNode(n, nsScope)
		if err != nil {
			return nil, err
		}
		if instr == nil {
			continue
		}
		// Section 5.2 makes the evaluation of a variable lazy: a processor
		// "is not required to evaluate a variable if the value is not used".
		// The distinction is observable through XTDE0640, and param-0301 is
		// written to prove it — a local variable bound to a global that is
		// mid-evaluation, and never referenced, is not a circularity because
		// nothing ever forces it.
		//
		// The decision has to be made here, at compile time: an Instruction
		// exposes only Execute, so execSequence cannot inspect what the
		// instructions after a variable refer to. What it can be told is
		// whether any of them mention the name, which is decided from the
		// source elements that produced them.
		if v, ok := instr.(*varInstr); ok && n.Kind == xdm.KindElement {
			// A declaration that names itself is XPST0008 — a *static*
			// error, so it is due whether or not the value is ever demanded,
			// and skipping the evaluation would silently accept it.
			// error-XPST0008b is exactly that stylesheet.
			v.unused = !nameReferencedIn(nodes[i+1:], v.v.Name) &&
				!nameReferencedIn([]*xdm.Node{n}, v.v.Name)
		}
		out = append(out, instr)
	}
	return out, nil
}

// nameReferencedIn reports whether any of the given source subtrees could
// refer to the variable name.
//
// The test is deliberately one-sided. A "yes" costs nothing — the variable is
// evaluated, which is what always happened — while a "no" skips an evaluation,
// so the answer must be "yes" whenever there is any doubt. Every construct
// that can name a variable does so with "$" followed by the name somewhere in
// an attribute value or in text, so a scan for that token over the whole
// subtree cannot miss one; it only over-reports, for instance on a "$b" that
// happens to sit inside a string literal.
func nameReferencedIn(nodes []*xdm.Node, name xdm.QName) bool {
	// Only the local part is matched, and any prefix bound to the variable's
	// namespace would spell the reference differently, so a name in a
	// namespace is never treated as unreferenced.
	if name.URI != "" {
		return true
	}
	want := "$" + name.Local
	var scan func(*xdm.Node) bool
	scan = func(n *xdm.Node) bool {
		if n == nil {
			return false
		}
		switch n.Kind {
		case xdm.KindText, xdm.KindComment:
			if mentions(n.Value, want) {
				return true
			}
		case xdm.KindElement:
			for _, a := range n.Attrs {
				if mentions(a.Value, want) {
					return true
				}
			}
		}
		for _, ch := range n.Children {
			if scan(ch) {
				return true
			}
		}
		return false
	}
	for _, n := range nodes {
		if scan(n) {
			return true
		}
	}
	return false
}

// mentions reports whether s contains want as a variable reference, that is,
// not immediately followed by another name character which would make it a
// different and longer name.
func mentions(s, want string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], want)
		if j < 0 {
			return false
		}
		end := i + j + len(want)
		if end >= len(s) || !isNameContinuation(rune(s[end])) {
			return true
		}
		i = end
	}
}

// isNameContinuation reports whether r may continue an NCName. Only the ASCII
// range is decided here; anything above it is treated as a continuation, which
// keeps the over-reporting direction that nameReferencedIn requires.
func isNameContinuation(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '-' || r == '_' || r == '.' || r == ':':
		return true
	case r >= 0x80:
		return true
	}
	return false
}

// compileNode compiles one node of a sequence constructor.
func (c *compiler) compileNode(n *xdm.Node, nsScope *xdm.Node) (Instruction, error) {
	switch n.Kind {
	case xdm.KindText:
		// Whitespace-only text between instructions is discarded; anything
		// else is a literal text node. Without this every indented stylesheet
		// would emit its own indentation into the result.
		if xdm.IsXMLWhitespace(n.Value) && !stylesheetTextPreserved(n) {
			return nil, nil
		}
		return c.compileText(n)

	case xdm.KindComment:
		// Comments in the stylesheet are not copied to the output.
		return nil, nil

	case xdm.KindPI:
		return nil, nil

	case xdm.KindElement:
		// An element whose effective version is below 2.0 puts XSLT 1.0
		// backwards-compatible behaviour in force for what is written on it.
		// This processor implements that behaviour, so there is nothing to
		// refuse here: XTDE0160 applies only to a processor that does not,
		// and the mode is carried into the expressions by the namespace
		// resolver each of them is compiled through. See compatModeAt.
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
	// Section 18.2: an element in a designated extension namespace is an
	// instruction, not a literal result element. This processor implements no
	// extension instructions, so every one of them is "not available" and
	// fallback is performed: the xsl:fallback children are evaluated, or
	// XTDE1450 is raised if there are none. XTDE1450 is a *dynamic* error, so
	// it must fire when the instruction is reached rather than at compile
	// time — a stylesheet may guard the instruction with element-available and
	// never evaluate it.
	if isExtensionInstruction(n) {
		body, ok, err := c.compileFallbackChildren(n)
		if err != nil {
			return nil, err
		}
		return &extensionInstr{name: n.Name, fallback: body, hasFallback: ok}, nil
	}
	sets, err := parseUseAttributeSets(n)
	if err != nil {
		return nil, err
	}
	instr := &literalElemInstr{name: n.Name, attrSets: sets, baseURI: n.BaseURI}
	if instr.validation, err = compileValidation(n, ""); err != nil {
		return nil, err
	}

	// Namespace declarations on the literal element are copied to the result,
	// minus the XSLT namespace itself and anything listed in
	// exclude-result-prefixes.
	// Keyed by namespace URI, not by prefix: the attribute names prefixes, but
	// section 11.1.3 excludes "the namespace" each names. A module that binds
	// two prefixes to one URI, or rebinds a prefix further down, otherwise
	// gets an exclusion that follows the spelling instead of the namespace.
	excluded := map[string]bool{}
	// Exclusion is decided per URI, but a prefix is what an attribute name
	// carries, and a module may bind several prefixes to one URI. When an
	// attribute's own prefix is one the stylesheet named explicitly, and
	// another prefix for the same URI was not named, the unnamed one is the
	// author's surviving spelling and the attribute should be written with
	// it. excludedPrefixes records the ones named by name; "#all" is not
	// recorded here because it names no prefix in particular and leaves no
	// unnamed alternative to prefer.
	excludedPrefixes := map[string]bool{}
	// XTSE0808: every prefix named here must be bound. An unbound one is a
	// typo that would otherwise exclude nothing and go unnoticed.
	// The list is interpreted against the element it is written on: "#all"
	// names every prefix in scope *there*, which for a list on xsl:stylesheet
	// is not the same set as the prefixes a literal element further down
	// declares for itself. Resolving it against the literal element instead
	// excluded declarations the stylesheet never asked to exclude.
	addExcluded := func(at *xdm.Node, list string) error {
		for _, p := range strings.Fields(list) {
			if p == "#all" {
				for _, uri := range at.InScopeNamespaces() {
					excluded[uri] = true
				}
				continue
			}
			if p == "#default" {
				p = ""
			}
			uri, ok := at.LookupPrefix(p)
			if !ok {
				return fmt.Errorf(
					"XTSE0808: exclude-result-prefixes names %q, which is "+
						"not a namespace prefix in scope", p)
			}
			excluded[uri] = true
			excludedPrefixes[p] = true
		}
		return nil
	}
	// exclude-result-prefixes applies to the element it is written on and to
	// everything inside it, so an ancestor's list is collected too — a
	// stylesheet routinely writes one on xsl:stylesheet and expects it to
	// cover every literal element in the module.
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement {
			continue
		}
		if cur == n || cur.Name.URI == xdm.NSXSL {
			if err := addExcluded(cur, cur.AttrValue("exclude-result-prefixes")); err != nil {
				return nil, err
			}
		}
		if v := cur.Attr(xdm.NSXSL, "exclude-result-prefixes"); v != nil {
			if err := addExcluded(cur, v.Value); err != nil {
				return nil, err
			}
		}
	}
	// Section 11.1 copies the namespace *nodes* of the literal result
	// element, which is every binding in scope on it — not only those
	// declared on the element itself. A stylesheet that declares a prefix on
	// xsl:stylesheet and uses it inside a literal element expects the result
	// to carry the declaration, and the difference is observable through the
	// namespace axis of the constructed tree.
	scope := n.InScopeNamespaces()
	prefixes := make([]string, 0, len(scope))
	for p := range scope {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	for _, p := range prefixes {
		uri := scope[p]
		if uri == xdm.NSXML {
			continue
		}
		if uri == xdm.NSXSL || excluded[uri] {
			// Section 11.1.4: "a namespace node whose string value is a
			// target namespace URI is copied to the result tree, whether or
			// not the URI identifies an excluded namespace". Whether a URI is
			// a target cannot be decided here — xsl:namespace-alias is a
			// top-level declaration that may be compiled after this literal
			// element, and may live in an imported module — so the excluded
			// bindings are carried through and filtered when the instruction
			// runs, by which time the alias map is complete.
			instr.excludedNamespaces = append(instr.excludedNamespaces,
				nsBinding{prefix: p, uri: uri})
			continue
		}
		instr.namespaces = append(instr.namespaces, nsBinding{prefix: p, uri: uri})
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
		// The parser recovers an attribute's prefix by scanning the in-scope
		// bindings for one matching its URI, innermost first, so a prefix
		// that exclusion is about to drop can win over one that survives. A
		// namespace an attribute needs cannot actually be excluded — the
		// result would not be readable back — so the declaration reappears
		// under the dropped spelling. Re-point the name at a surviving
		// prefix for the same URI when the stylesheet named this one.
		an := a.Name
		if an.URI != "" && excludedPrefixes[an.Prefix] {
			if p, ok := survivingPrefix(scope, an.URI, excludedPrefixes); ok {
				an.Prefix = p
			}
		}
		instr.attrs = append(instr.attrs, attrTemplate{name: an, value: avt})
	}

	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	instr.body = body
	return instr, nil
}

// survivingPrefix returns a prefix bound to uri in scope that the stylesheet
// did not name in an exclude-result-prefixes list, if there is one.
//
// Ties are broken by sorting so that a module binding several surviving
// prefixes to one URI compiles to the same output on every run; ranging over
// the scope map directly made the chosen spelling depend on map order.
func survivingPrefix(scope map[string]string, uri string, excludedPrefixes map[string]bool) (string, bool) {
	var cands []string
	for p, u := range scope {
		if u == uri && p != "" && !excludedPrefixes[p] {
			cands = append(cands, p)
		}
	}
	if len(cands) == 0 {
		return "", false
	}
	sort.Strings(cands)
	return cands[0], true
}

// compileXSLInstruction dispatches on the instruction name.
func (c *compiler) compileXSLInstruction(n *xdm.Node) (Instruction, error) {
	ns := newNSResolver(n, "")

	switch n.Name.Local {
	case "value-of":
		return c.compileValueOf(n, ns)
	case "text":
		// A child of xsl:text is a text value template on the same terms as
		// one in a sequence constructor, so it is compiled through the same
		// path rather than taken literally. The node stands in for the
		// element's whole content, which xsl:text is defined to concatenate.
		return c.compileText(&xdm.Node{
			Kind: xdm.KindText, Value: n.StringValue(), Parent: n,
		})
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
		instr := &commentInstr{}
		if sel := n.AttrValue("select"); sel != "" {
			var err error
			if instr.sel, err = compileExpr(sel, ns); err != nil {
				return nil, err
			}
			return instr, nil
		}
		body, err := c.compileSequence(n, n)
		if err != nil {
			return nil, err
		}
		instr.body = body
		return instr, nil
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
		return &copyOfInstr{
			sel:              sel,
			noNamespaces:     noAttr(n, "copy-namespaces"),
			copyAccumulators: yesAttr(n, "copy-accumulators"),
			validation:       spec,
			baseURI:          n.BaseURI,
		}, nil
	case "evaluate":
		return c.compileEvaluate(n, ns)
	case "iterate":
		return c.compileIterate(n, ns)
	case "break":
		return c.compileBreak(n, ns)
	case "next-iteration":
		return c.compileNextIteration(n, ns)
	case "map", "map-entry":
		return c.compileMap(n, ns)
	case "on-empty", "on-non-empty", "where-populated":
		return c.compileOnEmpty(n, ns)
	case "fork":
		return c.compileFork(n)
	case "sequence":
		// XSLT 3.0 made the select attribute optional: "the items comprising
		// the result sequence are evaluated either using the select
		// attribute, or using the contained sequence constructor", and with
		// neither the result is the empty sequence.
		//
		// This follows the processor, not the module. sequence-0132 is a
		// version="2.0" module scoped XSLT20+ that omits select and expects
		// XTTE0570 from the type check afterwards -- so the instruction has
		// to have compiled first, at both versions.
		if n.AttrValue("select") == "" && processorAtLeast30() {
			body, err := c.compileSequence(n, n)
			if err != nil {
				return nil, err
			}
			return &sequenceInstr{body: body}, nil
		}
		sel, err := requiredExpr(n, "select", ns)
		if err != nil {
			return nil, err
		}
		// XTSE3185: with select present the only permitted children are
		// xsl:fallback, which a 2.0 or 3.0 processor ignores. The content
		// model admits a sequence constructor here at 3.0, so the two forms
		// have to be kept apart by this rule rather than by the grammar.
		for _, ch := range n.ChildElements() {
			if !isXSL(ch, "fallback") {
				return nil, fmt.Errorf(
					"XTSE3185: xsl:sequence has a select attribute and a %s "+
						"child", ch.Name.Lexical())
			}
		}
		return &sequenceInstr{sel: sel}, nil
	case "message":
		return c.compileMessage(n, ns)
	case "try":
		return c.compileTry(n, ns)
	case "merge":
		return c.compileMerge(n, ns)
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
		spec, err := compileValidation(n, "")
		if err != nil {
			return nil, err
		}
		return &documentInstr{body: body, validation: spec}, nil
	case "result-document":
		return c.compileResultDocument(n, ns)
	case "source-document":
		return c.compileSourceDocument(n)
	case "fallback":
		// xsl:fallback is instantiated only when its containing instruction is
		// one the processor does not recognise. Every instruction that reaches
		// here is recognised, so the fallback is correctly *not* run — and,
		// unlike the misplaced elements below, a stray xsl:fallback is legal
		// and simply produces nothing. Saxon-HE 12.4 agrees: both engines
		// render "A<xsl:fallback>B</xsl:fallback>C" as "AC".
		return nil, nil
	case "param", "sort", "with-param", "when", "otherwise",
		"matching-substring", "non-matching-substring", "output-character",
		"catch", "merge-source", "merge-key", "merge-action":
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
	// Section 3.9: an element in the XSLT namespace that this version does not
	// define, appearing in a sequence constructor where forwards-compatible
	// behaviour is enabled, is not an error provided it supplies an
	// xsl:fallback. The result is the concatenation of the fallback bodies,
	// and siblings of the xsl:fallback children are ignored "even if they are
	// valid XSLT 2.0 instructions" — which is why only the fallbacks are
	// compiled and the rest of the content is dropped.
	if forwardsMode(n) {
		body, ok, err := c.compileFallbackChildren(n)
		if err != nil {
			return nil, err
		}
		if ok {
			return &blockInstr{body: body}, nil
		}
		// With no xsl:fallback, "a static error is reported in the same way as
		// if forwards-compatible behaviour were not enabled" — XTSE0010.
	}
	return nil, fmt.Errorf(
		"xsl:%s is not an XSLT 2.0 element (XTSE0010)", n.Name.Local)
}

// extensionInstr stands for an extension instruction the processor does not
// implement. Every extension instruction reaches this state, because no
// extension namespace is recognised.
type extensionInstr struct {
	name        xdm.QName
	fallback    []Instruction
	hasFallback bool
}

func (i *extensionInstr) Execute(rt *runtime, out *outputBuilder) error {
	if !i.hasFallback {
		return fmt.Errorf(
			"XTDE1450: extension instruction %s is not available and has no "+
				"xsl:fallback children", i.name.Lexical())
	}
	return execSequence(i.fallback, rt, out)
}

// compileFallbackChildren compiles the bodies of el's xsl:fallback children,
// reporting whether it had any.
func (c *compiler) compileFallbackChildren(el *xdm.Node) ([]Instruction, bool, error) {
	var body []Instruction
	found := false
	for _, ch := range el.ChildElements() {
		if !isXSL(ch, "fallback") {
			continue
		}
		found = true
		instrs, err := c.compileSequence(ch, ch)
		if err != nil {
			return nil, false, err
		}
		body = append(body, instrs...)
	}
	return body, found, nil
}

func (c *compiler) compileValueOf(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	instr := &valueOfInstr{}
	// @separator is an attribute value template — the syntax summary writes
	// it as { string } — so a separator such as "{item[1]};{item[2]}" is
	// evaluated rather than emitted literally. The attribute is looked up
	// rather than tested for emptiness because separator="" is a meaningful
	// request for no separator at all.
	if a := n.Attr("", "separator"); a != nil {
		sep, err := compileAVT(a.Value, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:value-of/@separator: %w", err)
		}
		instr.separator, instr.hasSeparator = sep, true
	}
	if sel := n.AttrValue("select"); sel != "" {
		comp, err := compileExpr(sel, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:value-of/@select: %w", err)
		}
		instr.sel = comp
		// XTSE0870: select and content are mutually exclusive.
		if hasRealContent(n) {
			return nil, fmt.Errorf(
				"XTSE0870: xsl:value-of has both a select attribute and content")
		}
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
		comp, err := compileExpr(sel, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:apply-templates/@select: %w", err)
		}
		instr.sel = comp
	}
	// An absent @mode means the default mode, which [xsl:]default-mode may
	// have moved off the unnamed one. Reading it here rather than defaulting
	// instr.mode to "" is what makes "<out xsl:default-mode='a'>" govern the
	// xsl:apply-templates written inside it.
	// Whether an atomic value may be dispatched on follows the PROCESSOR's
	// version. Every XTTE0520 case in the suite -- error-0520a through d and
	// type-0159 -- is scoped XSLT20, so the error is what a 2.0 processor
	// owes; match-256 is a version="2.0" module scoped XSLT30+ that selects
	// an integer and expects it to be matched against, not refused.
	instr.atomicOK = patternsAllow30(ns) || processorAtLeast30()
	// @default-mode on the instruction itself takes precedence over one
	// inherited from an ancestor, being nearer.
	if dm, err := defaultModeAt(n); err != nil {
		return nil, err
	} else {
		instr.mode = dm
	}
	if m := strings.TrimSpace(n.AttrValue("mode")); m != "" {
		// Expanded to match the form the template rules are indexed under;
		// the pseudo-modes are not names and stay as written.
		switch {
		case m == "#current":
			instr.mode = m
		case m == "#unnamed":
			instr.mode = ""
		case m == "#default":
			// #default names whatever [xsl:]default-mode is in scope here,
			// which is the unnamed mode when none is. Resolving it now makes
			// it a static property of the instruction, which is what section
			// 6.6.2 says it is.
			dm, err := defaultModeAt(n)
			if err != nil {
				return nil, err
			}
			instr.mode = dm
		default:
			// Section 6.5: the value "must either be a QName ... or the token
			// #default ... or the token #current". The summary does not
			// bracket the type, so it is not an attribute value template, and
			// a curly-bracket value is simply outside the lexical space —
			// which is XTSE0020. Left unchecked, mode="{$x}" was resolved to
			// a mode literally named "{$x}", so a stylesheet expecting an
			// error selected no template rules and quietly produced nothing.
			if !isLexicalQName(m) {
				return nil, fmt.Errorf(
					"XTSE0020: xsl:apply-templates/@mode=%q is neither a "+
						"QName nor #default nor #current", m)
			}
			qn, err := resolveQNameAttr(n, m)
			if err != nil {
				return nil, err
			}
			instr.mode = xdm.QName{URI: qn.URI, Local: qn.Local}.Clark()
		}
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
	instr := &callTemplateInstr{name: qn, params: params, compat: compatModeAt(n)}
	// XTSE0680 is checked after every module has compiled, because the
	// template being called may be declared below this call or in a module
	// imported afterwards. The call is recorded here, where the source
	// element is still to hand for the error message.
	c.calls = append(c.calls, instr)
	return instr, nil
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
	// An absent data-type means the XSLT 2.0 default: compare the values by
	// their own type, using the XPath "lt" operator, rather than converting
	// everything to a string first. The empty string records that, so that
	// data-type="text" — which really does mean "compare as strings" — stays
	// distinguishable from the default.
	s := &sortKey{order: "ascending"}

	// The key is either the select expression or the element's content.
	// XTSE1015 forbids both, and with neither the default is select=".".
	sel := n.AttrValue("select")
	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	switch {
	case sel != "" && len(body) > 0:
		return nil, fmt.Errorf(
			"XTSE1015: an xsl:sort element with a select attribute must be empty")
	case sel == "" && len(body) > 0:
		s.body = body
	default:
		if sel == "" {
			sel = "." // sorting on the item itself is the default
		}
		comp, err := compileExpr(sel, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:sort/@select: %w", err)
		}
		s.sel = comp
	}

	// order, data-type, case-order and lang are all attribute value
	// templates. They cannot vary per item — the whole sort is one ordering —
	// but they can be computed from a parameter, so a literal value is
	// resolved now and anything with braces in it is deferred to run time.
	if v := n.AttrValue("order"); v != "" {
		a, err := compileAVT(v, ns)
		if err != nil {
			return nil, fmt.Errorf("xsl:sort/@order: %w", err)
		}
		if a.isLit {
			if err := checkSortOrder(a.literal); err != nil {
				return nil, err
			}
			s.order = a.literal
		} else {
			s.orderAVT = a
		}
	}
	if v := n.AttrValue("data-type"); v != "" {
		a, err := compileAVT(v, ns)
		if err != nil {
			return nil, fmt.Errorf("xsl:sort/@data-type: %w", err)
		}
		if a.isLit {
			s.dataType = a.literal
		} else {
			s.dataTypeAVT = a
		}
	}
	if v := n.AttrValue("case-order"); v != "" {
		a, err := compileAVT(v, ns)
		if err != nil {
			return nil, fmt.Errorf("xsl:sort/@case-order: %w", err)
		}
		if a.isLit {
			if err := checkCaseOrder(a.literal); err != nil {
				return nil, err
			}
			s.caseOrder = a.literal
		} else {
			s.caseOrderAVT = a
		}
	}
	// @lang orders accented and cased letters by the conventions of that
	// language: Swedish puts "ä" after "z", where codepoint order puts it
	// next to "a". A literal tag builds its collator now, so an unrecognised
	// one is reported before the transform runs.
	if v := strings.TrimSpace(n.AttrValue("lang")); v != "" {
		a, err := compileAVT(v, ns)
		if err != nil {
			return nil, fmt.Errorf("xsl:sort/@lang: %w", err)
		}
		if a.isLit {
			coll, err := newCollator(a.literal)
			if err != nil {
				// A literal @lang is not an attribute value template, so a
				// bad value here is the static error XTSE0020 ("an attribute
				// ... contains a value that is not one of the permitted
				// values for that attribute"), not the dynamic XTDE0030 that
				// the AVT branch below reports. The two codes differ only in
				// whether the value was written or computed.
				return nil, fmt.Errorf("XTSE0020: %w", err)
			}
			s.coll = coll
		} else {
			s.langAVT = a
		}
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
			// An unrecognised collation URI is XTDE1035, which section 13.1.3
			// makes a *dynamic* error. Refusing it here turned it into a
			// compile-time failure, so a stylesheet whose sort is never
			// reached was rejected outright. Leaving it to collAVT means the
			// error is raised when the sort actually runs.
			if c, err := xpath.ResolveCollation(a.literal); err == nil {
				s.strColl = c
			}
		}
	} else if ns.collation != "" {
		// No @collation: the default collation in force where the xsl:sort
		// was written applies.
		if c, err := xpath.ResolveCollation(ns.collation); err == nil {
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
		return nil, fmt.Errorf("XTSE0010: xsl:choose requires at least one xsl:when")
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
	instr := &elementInstr{name: nameAVT, scope: n, attrSets: sets, baseURI: n.BaseURI}
	if instr.validation, err = compileValidation(n, ""); err != nil {
		return nil, err
	}
	// namespace="" is not the same as no namespace attribute: the first puts
	// the name in no namespace, the second lets the prefix decide. Testing
	// the *value* rather than the attribute's presence conflated them.
	if a := n.Attr("", "namespace"); a != nil {
		avt, err := compileAVT(a.Value, ns)
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
	if sepAttr := n.Attr("", "separator"); sepAttr != nil {
		sep, err := compileAVT(sepAttr.Value, ns)
		if err != nil {
			return nil, err
		}
		instr.separator, instr.hasSeparator = sep, true
	}
	if instr.validation, err = compileValidation(n, ""); err != nil {
		return nil, err
	}
	// namespace="" is not the same as no namespace attribute: the first puts
	// the name in no namespace, the second lets the prefix decide. Testing
	// the *value* rather than the attribute's presence conflated them.
	if a := n.Attr("", "namespace"); a != nil {
		avt, err := compileAVT(a.Value, ns)
		if err != nil {
			return nil, err
		}
		instr.namespace = avt
	}
	if sel := n.AttrValue("select"); sel != "" {
		comp, err := compileExpr(sel, ns)
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
	instr := &piInstr{name: nameAVT}
	if sel := n.AttrValue("select"); sel != "" {
		if instr.sel, err = compileExpr(sel, ns); err != nil {
			return nil, err
		}
		return instr, nil
	}
	if instr.body, err = c.compileSequence(n, n); err != nil {
		return nil, err
	}
	return instr, nil
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
	instr := &copyInstr{
		attrSets:     sets,
		noNamespaces: noAttr(n, "copy-namespaces"),
		noInherit:    noAttr(n, "inherit-namespaces"),
		body:         body,
		validation:   spec,
	}
	// XSLT 3.0 added select: "the selected item is the item selected by
	// evaluating the expression in the select attribute if present, or the
	// context item otherwise". It is the difference between copying what the
	// template is processing and copying something the instruction names,
	// and it is what makes xsl:copy usable inside xsl:where-populated.
	if v := n.AttrValue("select"); v != "" {
		sel, err := compileExpr(v, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:copy/@select: %w", err)
		}
		instr.sel = sel
	}
	return instr, nil
}

func (c *compiler) compileMessage(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	instr := &messageInstr{xslt30: c.opts.MaxVersion == 0 || c.opts.MaxVersion >= 3.0}
	if v := n.AttrValue("error-code"); v != "" {
		avt, err := compileAVT(v, ns)
		if err != nil {
			return nil, err
		}
		instr.errorCode, instr.errorCodeNS = avt, n
	}
	if v := n.AttrValue("terminate"); v != "" {
		avt, err := compileAVT(v, ns)
		if err != nil {
			return nil, err
		}
		instr.terminate = avt
	}
	if sel := n.AttrValue("select"); sel != "" {
		comp, err := compileExpr(sel, ns)
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
	comp, err := compileExpr(v, ns)
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
		a, err := compileAVT(v, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:result-document/@format: %w", err)
		}
		instr.format = a
	}
	// Serialisation attributes written on the instruction itself override
	// whatever the selected definition supplies, so they are kept separately
	// and applied over it at run time. Each is an attribute value template.
	instr.overrides = n
	instr.overrideAVTs = map[string]*avt{}
	for _, a := range n.Attrs {
		if a.Name.URI != "" || a.Name.Local == "href" || a.Name.Local == "format" {
			continue
		}
		// validation and type are not serialisation parameters. They ask for
		// the result tree to be assessed, and treating them as overrides
		// would have put validation="strict" into the output settings, where
		// nothing reads it.
		if a.Name.Local == "validation" || a.Name.Local == "type" {
			continue
		}
		t, err := compileAVT(a.Value, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:result-document/@%s: %w", a.Name.Local, err)
		}
		instr.overrideAVTs[a.Name.Local] = t
	}
	// §19.2.2: a result document is a document node, and validation on it is
	// document-node validation — the sole element child is assessed, and the
	// document-level constraints are applied over it.
	spec, err := compileValidation(n, "")
	if err != nil {
		return nil, err
	}
	instr.validation = spec

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
	case "catch":
		return "xsl:try"
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

// checkSortOrder validates xsl:sort/@order, whose only values are the two the
// specification names.
func checkSortOrder(v string) error {
	if v != "ascending" && v != "descending" {
		return fmt.Errorf("XTDE0030: invalid xsl:sort/@order %q", v)
	}
	return nil
}

// checkCaseOrder validates xsl:sort/@case-order.
func checkCaseOrder(v string) error {
	if v != "upper-first" && v != "lower-first" {
		return fmt.Errorf("XTDE0030: invalid xsl:sort/@case-order %q", v)
	}
	return nil
}

// stylesheetTextPreserved reports whether a whitespace-only text node in the
// stylesheet survives the preprocessing of section 4.2.
//
// The default is that it does not. It survives only under xml:space="preserve"
// — and even then, section 4.2 lists two overriding cases where the node goes
// regardless: a whitespace node whose parent is one of a fixed set of XSLT
// elements whose content is entirely element children, and a whitespace node
// immediately followed by xsl:param or xsl:sort. Both exist because those
// positions can never carry meaningful text, so preserving there would inject
// indentation into every stylesheet that formats them across lines.
func stylesheetTextPreserved(n *xdm.Node) bool {
	parent := n.Parent
	if parent == nil {
		return false
	}
	if parent.Name.URI == xdm.NSXSL && whitespaceStrippingParents[parent.Name.Local] {
		return false
	}
	// The *following* sibling is what matters: text laid out before an
	// xsl:sort or xsl:param is indentation, whereas text after the last one
	// is content of the sequence constructor.
	for i, ch := range parent.Children {
		if ch != n {
			continue
		}
		for _, sib := range parent.Children[i+1:] {
			if sib.Kind != xdm.KindElement {
				continue
			}
			if sib.Name.URI == xdm.NSXSL &&
				(sib.Name.Local == "param" || sib.Name.Local == "sort") {
				return false
			}
			break
		}
		break
	}
	// The nearest ancestor bearing xml:space decides. "default" written
	// inside a "preserve" region turns stripping back on, which is why the
	// walk stops at the first attribute found rather than at the first
	// "preserve".
	for cur := parent; cur != nil; cur = cur.Parent {
		if cur.Kind != xdm.KindElement {
			continue
		}
		if a := cur.Attr(xdm.NSXML, "space"); a != nil {
			return a.Value == "preserve"
		}
	}
	return false
}

// whitespaceStrippingParents are the XSLT elements section 4.2 names as
// discarding whitespace-only children whatever xml:space says.
var whitespaceStrippingParents = map[string]bool{
	"analyze-string":  true,
	"apply-imports":   true,
	"apply-templates": true,
	"attribute-set":   true,
	"call-template":   true,
	"character-map":   true,
	"choose":          true,
	"next-match":      true,
	"stylesheet":      true,
	"transform":       true,
}

// mergeAcrossComments implements the first two steps of XSLT 2.0 section 4.2:
// comments and processing instructions are removed from the stylesheet tree,
// and text nodes that become adjacent as a result are merged. Only then is the
// whitespace-only test applied.
//
// The order matters. In expression-2101 a comment sits between "\n    " and
// "\n    [", neither of which survives on its own as a whitespace-only node;
// merged they form "\n    \n    [", which is not whitespace-only and is kept.
func mergeAcrossComments(nodes []*xdm.Node) []*xdm.Node {
	has := false
	for _, n := range nodes {
		if n.Kind == xdm.KindComment || n.Kind == xdm.KindPI {
			has = true
			break
		}
	}
	if !has {
		return nodes
	}
	var kept []*xdm.Node
	for _, n := range nodes {
		if n.Kind == xdm.KindComment || n.Kind == xdm.KindPI {
			continue
		}
		if n.Kind == xdm.KindText && len(kept) > 0 && kept[len(kept)-1].Kind == xdm.KindText {
			prev := kept[len(kept)-1]
			merged := &xdm.Node{Kind: xdm.KindText, Value: prev.Value + n.Value, Parent: prev.Parent}
			kept[len(kept)-1] = merged
			continue
		}
		kept = append(kept, n)
	}
	return kept
}

// --- xsl:evaluate -----------------------------------------------------------

// evaluateInstr implements xsl:evaluate, section 10.4: an XPath expression
// computed as a string at run time and then evaluated.
//
// Everything about it is decided by the two static-context rules of 10.4.1
// that a 2.0 processor can honour without new machinery. The namespaces, the
// base URI and the default collation are those in force at the instruction, so
// the resolver built for it at compile time is exactly the static context the
// target expression needs — @default-collation on the instruction itself
// overrides the inherited one, which is what separates collations-0128's three
// templates from one another.
//
// The second rule is subtractive: "this set deliberately excludes XSLT-defined
// functions in the standard function namespace including for example, key,
// current-group, and system-property". That exclusion is not decoration. It is
// the whole subject of system-property-022, where calling system-property from
// inside xsl:evaluate must fail — and 10.4's XTDE3160 is the code for it,
// because a call to a function outside the static context is a static error
// when the target expression is analysed.
type evaluateInstr struct {
	// xpathExpr computes the target expression's source text.
	xpathExpr *xpath.Compiled
	// contextItem computes the focus for the target expression, or is nil
	// when @context-item is absent and the target expression runs with no
	// focus at all.
	contextItem *xpath.Compiled
	// ns is the static context: namespaces, base URI and default collation as
	// they stand at the xsl:evaluate element.
	ns *nsResolver
	// params are the xsl:with-param children, the only variables in scope
	// within the target expression.
	params []*Variable
	// as is @as, or nil for the item()* that permits anything.
	as *sequenceType
	// withParams computes the @with-params map, whose keys name parameters
	// that are only known dynamically. See bindWithParams.
	withParams *xpath.Compiled
	// baseURI is @base-uri, an AVT whose effective value replaces the static
	// base URI of the target expression. Nil when the attribute is absent,
	// leaving the base URI of the xsl:evaluate element in force.
	baseURI *avt
	// nsContext is @namespace-context, whose single node supplies the
	// statically known namespaces of the target expression. See resolverFor.
	nsContext *xpath.Compiled
	// schemaAware is @schema-aware, an AVT. Only when its effective value is
	// yes do the schemas xsl:import-schema brought in reach the target
	// expression; otherwise it sees the built-in types alone.
	schemaAware *avt
}

// xsltOnlyFunctions is appendix G's list: the functions XSLT defines in the
// standard function namespace, which section 10.4.1 excludes from the target
// expression's static context.
//
// fn:format-number is deliberately absent from it. The exclusion is of the
// functions that "depend on the dynamic context of the calling instruction",
// and a decimal format is a static declaration rather than part of that
// context — so the function is callable, it simply resolves its format name
// against nothing. evaluate-011 requires exactly that: it names an undeclared
// format and expects FODF1280, the runtime error for a name the function
// cannot find, not the static XTDE3160 for a function that is not there.
//
// It is deliberately not runtimeFuncNames. That map exists to tell a static
// check which names are *declared* despite being absent from the compile-time
// library, and it is missing the XSLT-defined functions that need no runtime —
// unparsed-entity-uri and unparsed-entity-public-id among them. Reusing it
// here would have let those through.
var xsltOnlyFunctions = map[string]bool{
	"current":                   true,
	"current-group":             true,
	"current-grouping-key":      true,
	"document":                  true,
	"element-available":         true,
	"format-date":               true,
	"format-dateTime":           true,
	"format-time":               true,
	"function-available":        true,
	"generate-id":               true,
	"key":                       true,
	"regex-group":               true,
	"system-property":           true,
	"type-available":            true,
	"unparsed-entity-public-id": true,
	"unparsed-entity-uri":       true,
	"unparsed-text":             true,
	"unparsed-text-available":   true,
}

// restrictedLibrary hides from a function library the functions that section
// 10.4.1 keeps out of the target expression's static context: the XSLT-defined
// ones that read the calling instruction's dynamic context, and the ones the
// stylesheet declares for itself.
//
// A stylesheet function is private to the stylesheet in the sense 10.4.1
// means — evaluate-045 calls f:square(5) from inside xsl:evaluate and requires
// the whole transform to fail with XTDE3160. Hiding it needs Declares rather
// than a Lookup on sheetFuncs, because that library chains to the builtins and
// would answer yes for every function in existence.
type restrictedLibrary struct {
	inner      xpath.FunctionLibrary
	sheetFuncs *xpath.Library
}

func (r restrictedLibrary) Lookup(name xdm.QName, arity int) (xpath.Function, bool) {
	if name.URI == xdm.NSFN && xsltOnlyFunctions[name.Local] {
		return xpath.Function{}, false
	}
	if r.sheetFuncs != nil && r.sheetFuncs.Declares(name, arity) {
		return xpath.Function{}, false
	}
	if r.inner == nil {
		return xpath.Function{}, false
	}
	return r.inner.Lookup(name, arity)
}

// compileEvaluate compiles xsl:evaluate.
func (c *compiler) compileEvaluate(n *xdm.Node, ns *nsResolver) (Instruction, error) {
	xp, err := requiredExpr(n, "xpath", ns)
	if err != nil {
		return nil, err
	}
	instr := &evaluateInstr{xpathExpr: xp, ns: ns}

	if a := n.Attr("", "context-item"); a != nil {
		ci, err := compileExpr(a.Value, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:evaluate/@context-item: %w", err)
		}
		instr.contextItem = ci
	}
	if a := n.Attr("", "as"); a != nil {
		st, err := compileSequenceType(a.Value, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:evaluate/@as: %w", err)
		}
		instr.as = st
	}
	if a := n.Attr("", "with-params"); a != nil {
		wp, err := compileExpr(a.Value, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:evaluate/@with-params: %w", err)
		}
		instr.withParams = wp
	}
	if a := n.Attr("", "namespace-context"); a != nil {
		nc, err := compileExpr(a.Value, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:evaluate/@namespace-context: %w", err)
		}
		instr.nsContext = nc
	}
	if a := n.Attr("", "schema-aware"); a != nil {
		sa, err := compileAVT(a.Value, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:evaluate/@schema-aware: %w", err)
		}
		instr.schemaAware = sa
	}
	if a := n.Attr("", "base-uri"); a != nil {
		b, err := compileAVT(a.Value, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:evaluate/@base-uri: %w", err)
		}
		instr.baseURI = b
	}
	params, _, err := c.compileParamsAndSorts(n, ns)
	if err != nil {
		return nil, err
	}
	instr.params = params
	return instr, nil
}

func (i *evaluateInstr) Execute(rt *runtime, out *outputBuilder) error {
	src, err := i.xpathExpr.EvalString(rt.ctx)
	if err != nil {
		return err
	}

	// The statically known namespaces come from @namespace-context when it is
	// present and from the xsl:evaluate element otherwise; see resolverFor.
	ns, err := i.resolverFor(rt)
	if err != nil {
		return err
	}
	// A static error in the target expression is XTDE3160 whatever its XPath
	// code would have been: 10.4 defines the error by *when* it happens, not
	// by which rule was broken. The version is the module's, exactly as a
	// statically written expression gets it.
	comp, err := xpath.CompileVersion(src, ns, ns.xpathVersion)
	if err != nil {
		// An xdm.Error, not a wrap: ErrorCode reports the innermost code it
		// can find, so wrapping would leave the failure carrying the target
		// expression's own static code. That code is what xsl:try consults,
		// and a static one is not recoverable -- so evaluate-033, which
		// catches this very failure, saw it escape the catch it was written
		// for.
		return xdm.Errorf("XTDE3160", "the target expression of xsl:evaluate "+
			"is not a valid XPath expression: %s", err)
	}
	// 10.4.1: the static base URI is the effective value of @base-uri when
	// present, and the base URI of the xsl:evaluate element otherwise.
	base := i.ns.baseURI
	if i.baseURI != nil {
		b, err := i.baseURI.eval(rt)
		if err != nil {
			return err
		}
		base = b
	}
	if base != "" {
		comp = comp.WithStaticBaseURI(base)
	}
	// 10.4.1: "Default collation: the same as the default collation defined at
	// this point in the stylesheet". compileExpr applies this for a statically
	// written expression; the target expression is compiled here instead, so
	// it has to be applied here too — and without it collations-0128 compares
	// under the codepoint collation and answers false three times.
	if i.ns.collation != "" {
		if coll, cerr := xpath.ResolveCollation(i.ns.collation); cerr == nil {
			comp = comp.WithDefaultCollation(coll)
		}
	}

	sub := rt
	for _, p := range i.params {
		v, err := evalVariable(p, rt)
		if err != nil {
			return fmt.Errorf("evaluating parameter $%s of xsl:evaluate: %w",
				p.Name.Lexical(), err)
		}
		sub = sub.withVar(p.Name, v)
	}
	sub, err = i.bindWithParams(rt, sub)
	if err != nil {
		return err
	}

	// 10.4.2: with no @context-item the target expression has no focus, and
	// with one it has a singleton focus on the single item that expression
	// returns. More than one item is XTTE3210.
	var focus xdm.Item
	if i.contextItem != nil {
		seq, err := i.contextItem.Eval(rt.ctx)
		if err != nil {
			return err
		}
		if len(seq) > 1 {
			return fmt.Errorf("XTTE3210: the context-item attribute of "+
				"xsl:evaluate selected %d items, not one", len(seq))
		}
		if len(seq) == 1 {
			focus = seq[0]
		}
	}
	if focus != nil {
		sub = sub.withFocus(focus, 1, 1)
	} else {
		sub = sub.withFocus(nil, 0, 0)
	}

	// The XSLT-specific dynamic context is absent inside the target
	// expression, and so is every XSLT-defined function.
	ctx := sub.ctx
	inner := *ctx
	inner.Funcs = restrictedLibrary{inner: ctx.Funcs, sheetFuncs: rt.sheet.funcs}
	seq, err := comp.Eval(&inner)
	if err != nil {
		// A call to a function the restricted library hides surfaces here as
		// XPST0017 rather than at compile time, because this engine resolves
		// function names when it evaluates them. It is still the static error
		// 10.4.1 describes, so it carries 10.4's code.
		if strings.Contains(err.Error(), "XPST0017") {
			// Flattened rather than wrapped, for the reason above.
			return xdm.Errorf("XTDE3160", "the target expression of "+
				"xsl:evaluate is not valid in its static context: %s", err)
		}
		return err
	}
	if i.as != nil {
		seq, err = i.as.convertAs(seq, "result of xsl:evaluate", "XTTE0505")
		if err != nil {
			return err
		}
	}
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			out.appendNode(v)
		case *xdm.Atomic:
			out.appendValue(v)
		}
	}
	return nil
}

// --- xsl:iterate ------------------------------------------------------------

// iterateInstr implements xsl:iterate, section 8.4.
//
// It is xsl:for-each with a memory: the body runs once per item as for-each's
// does, but xsl:next-iteration may hand a new set of parameter values to the
// next turn, so the loop can carry a running total that for-each cannot. The
// two escape hatches are xsl:break, which stops the loop and contributes its
// own body to the result, and xsl:on-completion, which runs only when the
// sequence is exhausted without a break.
type iterateInstr struct {
	sel *xpath.Compiled
	// params are the xsl:param children: the loop's carried state, holding
	// their declared defaults on the first turn.
	params []*Variable
	body   []Instruction
	// onCompletion is the xsl:on-completion body, nil when absent.
	onCompletion []Instruction
	// hasOnCompletion distinguishes an absent xsl:on-completion from one with
	// an empty body, which matters only for reading the code.
	hasOnCompletion bool
	// onCompletionSelect is @select on xsl:on-completion, an alternative to
	// its sequence constructor.
	onCompletionSelect *xpath.Compiled
}

// breakSignal unwinds the body of an xsl:iterate when xsl:break runs.
//
// The signal travels as an error because that is the only channel an
// Instruction has: Execute returns nothing else, and every enclosing
// instruction already propagates errors unchanged, which is exactly the
// unwinding xsl:break needs. iterateInstr recognises it and stops; anything
// outside an xsl:iterate never sees one, because compileBreak is only reached
// from a body compiled here.
type breakSignal struct{}

func (breakSignal) Error() string { return "xsl:break" }

// nextIterationSignal unwinds to the end of one turn of the loop, carrying the
// parameter values for the next.
type nextIterationSignal struct{ params map[string]xdm.Sequence }

func (nextIterationSignal) Error() string { return "xsl:next-iteration" }

type breakInstr struct {
	sel  *xpath.Compiled
	body []Instruction
}

func (i *breakInstr) Execute(rt *runtime, out *outputBuilder) error {
	// Section 8.4: the select expression or sequence constructor "is
	// evaluated using the same context item, position, and size as the
	// xsl:break instruction itself, and the result is appended to the result
	// of the xsl:iterate instruction as a whole" — so it writes to the same
	// output builder before the loop unwinds, not after.
	if i.sel != nil {
		seq, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		appendSequence(seq, out)
	} else if err := execSequence(i.body, rt, out); err != nil {
		return err
	}
	return breakSignal{}
}

type nextIterationInstr struct{ params []*Variable }

func (i *nextIterationInstr) Execute(rt *runtime, out *outputBuilder) error {
	vals := map[string]xdm.Sequence{}
	for _, p := range i.params {
		v, err := evalVariable(p, rt)
		if err != nil {
			return fmt.Errorf("evaluating parameter $%s of xsl:next-iteration: %w",
				p.Name.Lexical(), err)
		}
		vals[p.Name.Clark()] = v
	}
	return nextIterationSignal{params: vals}
}

// appendSequence writes a sequence to an output builder, the same way
// xsl:sequence does.
func appendSequence(seq xdm.Sequence, out *outputBuilder) {
	for _, it := range seq {
		switch v := it.(type) {
		case *xdm.Node:
			out.appendNode(v)
		case *xdm.Atomic:
			out.appendValue(v)
		}
	}
}

func (i *iterateInstr) Execute(rt *runtime, out *outputBuilder) error {
	seq, err := i.sel.Eval(rt.ctx)
	if err != nil {
		return err
	}

	// The carried state starts at the declared defaults, which are evaluated
	// in the focus of the xsl:iterate instruction itself rather than of any
	// item, because on the first turn no item has been reached yet.
	carried := map[string]xdm.Sequence{}
	for _, p := range i.params {
		v, err := evalVariable(p, rt)
		if err != nil {
			return fmt.Errorf("evaluating parameter $%s of xsl:iterate: %w",
				p.Name.Lexical(), err)
		}
		carried[p.Name.Clark()] = v
	}
	// The declarations, by name, so that a value xsl:next-iteration supplies
	// can be converted to the type the matching xsl:param asked for.
	declared := make(map[string]*Variable, len(i.params))
	for _, p := range i.params {
		declared[p.Name.Clark()] = p
	}

	broke := false
	for idx, it := range seq {
		sub := rt.withCurrent(it, idx+1, len(seq))
		for _, p := range i.params {
			sub = sub.withVar(p.Name, carried[p.Name.Clark()])
		}
		err := execSequence(i.body, sub, out)
		if err != nil {
			if _, ok := err.(breakSignal); ok {
				broke = true
				break
			}
			if nx, ok := err.(nextIterationSignal); ok {
				// Only the parameters xsl:next-iteration named change; the
				// rest "unchanged from the previous iteration".
				//
				// The value is converted to the declared type of the matching
				// xsl:param, not carried as supplied. §10.1.1's function
				// conversion rules apply to a parameter however it is
				// supplied, and xsl:with-param is where a value most often
				// arrives needing them: iterate-042 builds an element and
				// binds it to a parameter declared as="xs:string", which the
				// rules atomise.
				for k, v := range nx.params {
					if p := declared[k]; p != nil {
						conv, err := p.asType.convertAs(v,
							"parameter $"+p.Name.Lexical()+" of xsl:iterate",
							"XTTE0590")
						if err != nil {
							return err
						}
						v = conv
					}
					carried[k] = v
				}
				continue
			}
			return err
		}
	}

	// "It is not evaluated if the evaluation is terminated using xsl:break."
	if broke || !i.hasOnCompletion {
		return nil
	}
	// "During evaluation of its select expression or sequence constructor the
	// context item, position, and size are absent (that is, any reference to
	// these values is an error)." That absence is the whole of number-1004:
	// xsl:number with no context node is XTTE0990.
	done := rt.withFocus(nil, 0, 0)
	for _, p := range i.params {
		done = done.withVar(p.Name, carried[p.Name.Clark()])
	}
	if i.onCompletionSelect != nil {
		vals, err := i.onCompletionSelect.Eval(done.ctx)
		if err != nil {
			return err
		}
		appendSequence(vals, out)
		return nil
	}
	return execSequence(i.onCompletion, done, out)
}

// compileIterate compiles xsl:iterate.
func (c *compiler) compileIterate(n *xdm.Node, ns *nsResolver) (Instruction, error) {
	sel, err := requiredExpr(n, "select", ns)
	if err != nil {
		return nil, err
	}
	instr := &iterateInstr{sel: sel}

	// xsl:param and xsl:on-completion are read directly by this compiler and
	// are not part of the sequence constructor. Section 8.4's content model
	// puts them first, and stripping them here is what leaves the body.
	var bodyNodes []*xdm.Node
	for _, ch := range n.Children {
		if ch.Kind == xdm.KindElement && ch.Name.URI == xdm.NSXSL {
			switch ch.Name.Local {
			case "param":
				if err := checkIterateParam(ch); err != nil {
					return nil, err
				}
				v, err := c.compileVariable(ch)
				if err != nil {
					return nil, err
				}
				instr.params = append(instr.params, v)
				continue
			case "on-completion":
				instr.hasOnCompletion = true
				if a := ch.Attr("", "select"); a != nil {
					sc, err := compileExpr(a.Value, newNSResolver(ch, ""))
					if err != nil {
						return nil, fmt.Errorf(
							"in xsl:on-completion/@select: %w", err)
					}
					instr.onCompletionSelect = sc
					continue
				}
				body, err := c.compileSequence(ch, ch)
				if err != nil {
					return nil, err
				}
				instr.onCompletion = body
				continue
			}
		}
		bodyNodes = append(bodyNodes, ch)
	}

	body, err := c.compileNodes(bodyNodes, n)
	if err != nil {
		return nil, err
	}
	instr.body = body
	return instr, nil
}

func (c *compiler) compileBreak(n *xdm.Node, ns *nsResolver) (Instruction, error) {
	instr := &breakInstr{}
	if a := n.Attr("", "select"); a != nil {
		sel, err := compileExpr(a.Value, ns)
		if err != nil {
			return nil, fmt.Errorf("in xsl:break/@select: %w", err)
		}
		instr.sel = sel
		return instr, nil
	}
	body, err := c.compileSequence(n, n)
	if err != nil {
		return nil, err
	}
	instr.body = body
	return instr, nil
}

func (c *compiler) compileNextIteration(n *xdm.Node, ns *nsResolver) (Instruction, error) {
	params, _, err := c.compileParamsAndSorts(n, ns)
	if err != nil {
		return nil, err
	}
	return &nextIterationInstr{params: params}, nil
}
