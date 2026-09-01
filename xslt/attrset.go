package xslt

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// attributeSet is a compiled xsl:attribute-set.
//
// Attribute sets exist so that a group of attributes — a table cell's styling,
// say — can be declared once and applied to many elements. They compose: a set
// may use other sets, and the using set's own attributes win, which is what
// makes overriding one attribute of an inherited group possible.
type attributeSet struct {
	name xdm.QName
	// uses names the sets applied before this one's own attributes, so that
	// a later declaration of the same attribute replaces an earlier one.
	uses []attrSetRef
	body []Instruction
	// importPrecedence orders declarations of the same name across modules.
	importPrecedence int
	// pkg is the package that WROTE this declaration, which for a declaration
	// inside an xsl:override is the using package and not the used one whose
	// tree it was spliced into. The static cycle check of 10.2.2 is scoped by
	// it; see checkAttributeSetCycles.
	pkg int
}

func (c *compiler) compileAttributeSet(el *xdm.Node, precedence int) error {
	name := el.AttrValue("name")
	if name == "" {
		return fmt.Errorf("xsl:attribute-set requires a name attribute")
	}
	qn, err := resolveQNameAttr(el, name)
	if err != nil {
		return err
	}

	as := &attributeSet{
		name:             qn,
		importPrecedence: precedence,
		pkg:              overridingPackage(el, compilePackage),
	}
	for _, u := range strings.Fields(el.AttrValue("use-attribute-sets")) {
		uq, err := resolveQNameAttr(el, u)
		if err != nil {
			return err
		}
		as.uses = append(as.uses, attrSetRef{name: uq, pkg: as.pkg})
		c.usedAttributeSets = append(c.usedAttributeSets, uq)
	}

	// The content must be xsl:attribute instructions only; anything else
	// would silently contribute nothing, so it is rejected.
	for _, ch := range el.ChildElements() {
		if !isXSL(ch, "attribute") {
			return fmt.Errorf(
				"xsl:attribute-set %q may only contain xsl:attribute, found %s",
				name, ch.Name.Lexical())
		}
	}
	body, err := c.compileSequence(el, el)
	if err != nil {
		return err
	}
	if stub := abstractStubFor(el); stub != nil {
		body = stub
	}
	as.body = body

	// Several declarations of one name are merged, highest precedence last so
	// that it wins.
	key := qn.Clark()
	c.sheet.attributeSets[key] = append(c.sheet.attributeSets[key], as)
	return nil
}

// applyAttributeSets runs the named sets into the element under construction.
//
// Expansion is depth-first through "use-attribute-sets" so that a set's own
// attributes are written after the ones it inherits and therefore replace
// them. A cycle would recurse forever, so the chain being expanded is tracked.
func applyAttributeSets(rt *runtime, names []attrSetRef, out *outputBuilder) error {
	return expandAttributeSets(rt, names, out, map[string]bool{})
}

func expandAttributeSets(rt *runtime, names []attrSetRef, out *outputBuilder,
	active map[string]bool) error {

	for _, r := range names {
		n := r.name
		key := n.Clark()
		if active[key] {
			// A cycle within one package is XTSE0720 and was reported
			// statically, before anything ran; see
			// checkAttributeSetCycles. What is left to be found here is a
			// cycle that closes only across a package boundary, which
			// 10.2.2 explicitly puts outside static analysis -- "it is
			// possible to detect a cycle during the static analysis of a
			// package" -- and which is therefore the general dynamic
			// circularity error. override-as-003 closes such a cycle by
			// overriding one member of it from the using package.
			return fmt.Errorf(
				"XTDE0640: circular reference in xsl:attribute-set %q",
				n.Lexical())
		}
		sets, ok := attributeSetsFor(rt.sheet, key, r.pkg)
		if !ok {
			return fmt.Errorf("XTSE0710: no xsl:attribute-set named %q", n.Lexical())
		}
		active[key] = true
		// Only top-level variables and parameters are in scope inside an
		// attribute set: it is a declaration in its own right, so a local
		// variable live at the point of use must not be visible to it. The
		// focus is kept, because the set's body is still evaluated with the
		// context item the instruction that used it had.
		setRT := rt
		if rt.globalCtx != nil {
			r := *rt
			r.ctx = rt.globalCtx.WithFocus(rt.ctx.Item, rt.ctx.Position, rt.ctx.Size)
			setRT = &r
		}
		for _, as := range sets {
			if err := expandAttributeSets(setRT, as.uses, out, active); err != nil {
				return err
			}
			if err := execSequence(as.body, setRT, out); err != nil {
				return err
			}
		}
		delete(active, key)
	}
	return nil
}

// parseUseAttributeSets reads a use-attribute-sets attribute in either the
// no-namespace form (on xsl:element and xsl:copy) or the xsl: form (on a
// literal result element).
func parseUseAttributeSets(el *xdm.Node) ([]attrSetRef, error) {
	raw := el.AttrValue("use-attribute-sets")
	if a := el.Attr(xdm.NSXSL, "use-attribute-sets"); a != nil {
		raw = a.Value
	}
	pkg := overridingPackage(el, compilePackage)
	var out []attrSetRef
	for _, n := range strings.Fields(raw) {
		qn, err := resolveQNameAttr(el, n)
		if err != nil {
			return nil, err
		}
		out = append(out, attrSetRef{name: qn, pkg: pkg})
	}
	return out, nil
}

// attrSetRef is one name in a use-attribute-sets attribute, with the package
// the reference was written in.
//
// 3.5.5 makes a component's identity belong to its package, and an attribute
// set is a component. Two packages may each declare a private set of one
// name, and both are live at once, so the name alone does not say which is
// meant: override-as-005 writes an as-private in the using package and uses a
// public set of the used package whose own body invokes the used package's
// as-private. Resolved by name alone the two merged and the using package's
// attributes leaked into the used package's set.
type attrSetRef struct {
	name xdm.QName
	pkg  int
}

// attributeSetsFor answers the declarations a reference in package pkg binds
// to.
//
// A reference binds first to the declarations of its own package. Falling
// through to the ones declared elsewhere is what makes a set accepted from a
// used package usable: 3.6.3.4 binds a symbolic reference to the component
// the manifest supplied, which is a declaration of another package.
func attributeSetsFor(s *Stylesheet, key string, pkg int) ([]*attributeSet, bool) {
	all := s.attributeSets[key]
	if len(all) == 0 {
		return nil, false
	}
	var own []*attributeSet
	for _, as := range all {
		if as.pkg == pkg {
			own = append(own, as)
		}
	}
	if len(own) > 0 {
		return own, true
	}
	return all, true
}

// namespaceInstr implements xsl:namespace, which adds a namespace node to the
// element being constructed.
//
// This is not the same as declaring a namespace on a literal result element:
// it computes the prefix and URI at run time, which is how a stylesheet emits
// a binding whose URI comes from the source document.
type namespaceInstr struct {
	name *avt
	sel  *xpath.Compiled
	body []Instruction
}

func (i *namespaceInstr) Execute(rt *runtime, out *outputBuilder) error {
	prefix, err := i.name.eval(rt)
	if err != nil {
		return err
	}
	prefix = strings.TrimSpace(prefix)

	var uri string
	if i.sel != nil {
		seq, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		uri = stringJoin(seq, " ")
	} else {
		sub := newOutputBuilder()
		if err := execSequence(i.body, rt.temporaryOutputBefore30(), sub); err != nil {
			return err
		}
		uri = constructedText(sub.sequence(), " ")
	}
	// XTDE0930 is "results in a zero-length string", so the test is made on
	// the value the instruction actually produced. Trimming first turned a
	// whitespace-only result into an error it is not: on-empty-115b builds a
	// single space out of two empty text nodes joined by the simple-content
	// separator, and its xsl:attribute twin on-empty-115c requires that same
	// space to survive as bar=" ".
	if uri == "" {
		return fmt.Errorf("XTDE0930: xsl:namespace must not create a binding to an empty URI")
	}
	uri = strings.TrimSpace(uri)
	// XTDE0920: "if the effective value of the name attribute is neither a
	// zero-length string nor an NCName, or if it is xmlns". The zero-length
	// string is the default namespace, which is a legitimate thing to bind;
	// anything else that is not an NCName is not a prefix at all, and would
	// be written to the output as one.
	if prefix != "" && !xdm.IsNCName(prefix) {
		return fmt.Errorf(
			"XTDE0920: %q is not an NCName, so it cannot be a namespace prefix",
			prefix)
	}
	if prefix == "xmlns" {
		return fmt.Errorf("XTDE0920: xsl:namespace must not bind the prefix \"xmlns\"")
	}
	// XTDE0905: the string value must be "valid in the lexical space of the
	// data type xs:anyURI, or ... the string http://www.w3.org/2000/xmlns/".
	// The second half is a flat prohibition: that URI is the one the
	// Namespaces recommendation reserves for the xmlns attributes
	// themselves, so binding a prefix to it would produce a document no
	// parser could read back.
	if uri == "http://www.w3.org/2000/xmlns/" {
		return fmt.Errorf(
			"XTDE0905: xsl:namespace must not bind a prefix to " +
				"http://www.w3.org/2000/xmlns/")
	}
	// The lexical space of xs:anyURI is where the two XSD versions part.
	// XSD 1.0 defines it by reference to RFC 2396, so "####" is outside it
	// and XTDE0905 is right; XSD 1.1 3.3.17 redefined it to admit every
	// string, so no value can fall outside it and the error cannot arise.
	// The suite splits the same stylesheet on exactly that -- error-0905a
	// expects XTDE0905 with XSD_1.1 absent, error-0905b expects the namespace
	// node with it present (W3C bug 30180).
	//
	// The processor is the axis, not the stylesheet module: which XSD version
	// is in force is a property of the processor, and both cases run the same
	// version="2.0" stylesheet. XSLT 3.0 3.2 is what makes the answer differ
	// -- "XSLT 3.0 processors may optionally include types defined in XSD
	// 1.1" -- an option this engine takes and the 2.0 Recommendation does not
	// offer.
	//
	// The xmlns/ prohibition above is untouched: it is a flat rule about one
	// URI rather than a statement about a lexical space, which is why
	// error-0905c states no XSD dependency at all.
	if !sheetAtLeast30(rt.sheet) && !isLexicalAnyURI(uri) {
		return fmt.Errorf(
			"XTDE0905: %q is not in the lexical space of xs:anyURI", uri)
	}
	// XTDE0925: the xml prefix and the XML namespace are bound to each
	// other, and neither may be paired with anything else.
	switch {
	case prefix == "xml" && uri != xdm.NSXML:
		return fmt.Errorf(
			"XTDE0925: the xml prefix may only be bound to %s", xdm.NSXML)
	case prefix != "xml" && uri == xdm.NSXML:
		return fmt.Errorf(
			"XTDE0925: %s may only be bound to the xml prefix", xdm.NSXML)
	}
	// A parentless namespace node is a legal item in the data model, and a
	// sequence constructor may produce one: xsl:variable as="node()" with an
	// xsl:namespace body is the ordinary way to write one. XTDE0410 is about
	// ordering within element content, which the builder checks where there
	// is an element to check it against.
	return out.addNamespaceNode(prefix, uri)
}

// isLexicalAnyURI reports whether s is in the lexical space of xs:anyURI.
//
// XML Schema defines that space by reference to RFC 2396 as amended, which is
// permissive enough that almost any string is a valid relative reference. What
// it does *not* permit is the two cases checked here: a percent sign that does
// not introduce a two-digit hex escape, and more than one "#", since a URI
// reference has at most one fragment identifier. Those are the forms a
// stylesheet produces by accident — a half-built escape, or a placeholder such
// as "####" — rather than by intent.
func isLexicalAnyURI(s string) bool {
	if strings.Count(s, "#") > 1 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '%':
			if i+2 >= len(s) || !isHexByte(s[i+1]) || !isHexByte(s[i+2]) {
				return false
			}
			i += 2
		case ' ', '\t', '\n', '\r', '<', '>', '{', '}', '\\', '^', '`':
			// The characters RFC 2396 excludes outright, either as delimiters
			// or as unwise. Two of the "unwise" ones are not among them: a
			// double quote and a vertical bar. The suite builds namespace
			// URIs containing each and requires both to be accepted --
			// seqtor-038b and -038e assert xmlns:foo="||" -- and RFC 2396
			// lists them only as unwise rather than excluded from the lexical
			// space. Whitespace stays excluded, and seqtor-038c shows why:
			// with a space between the bars the same test allows XTDE0905,
			// because a space is what a stylesheet produces when it
			// concatenates two URIs by mistake.
			return false
		}
	}
	return true
}

func isHexByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func (c *compiler) compileNamespace(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	// A zero-length name is legal here and means the default namespace, so
	// the attribute is looked up rather than its value: requiredAVT cannot
	// tell name="" from an absent name, and refusing it rejected the one
	// spelling the specification gives for declaring xmlns.
	na := n.Attr("", "name")
	if na == nil {
		return nil, fmt.Errorf("xsl:namespace requires a name attribute")
	}
	nameAVT, err := compileAVT(na.Value, ns)
	if err != nil {
		return nil, err
	}
	instr := &namespaceInstr{name: nameAVT}
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

// performSortInstr implements xsl:perform-sort, which sorts a sequence and
// returns it rather than iterating over it.
//
// It exists because xsl:for-each/xsl:sort couples sorting to processing; when
// a stylesheet wants a sorted *value* — to bind to a variable, or to feed a
// positional predicate — perform-sort is the instruction that produces one.
type performSortInstr struct {
	sel   *xpath.Compiled
	sorts []*sortKey
	body  []Instruction
}

func (i *performSortInstr) Execute(rt *runtime, out *outputBuilder) error {
	var seq xdm.Sequence
	if i.sel != nil {
		v, err := i.sel.Eval(rt.ctx)
		if err != nil {
			return err
		}
		seq = v
	} else {
		sub := newOutputBuilder()
		if err := execSequence(i.body, rt.temporaryOutputBefore30(), sub); err != nil {
			return err
		}
		seq = sub.sequence()
	}

	sorted, err := applySorts(rt, seq, i.sorts)
	if err != nil {
		return err
	}
	// appendSequence rather than a switch of its own: xsl:perform-sort
	// returns the items it sorted, and a map or an array is as sortable as
	// anything else once a sort key has been written for it.
	return appendSequence(sorted, out)
}

func (c *compiler) compilePerformSort(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	instr := &performSortInstr{}
	var err error
	if sel := n.AttrValue("select"); sel != "" {
		if instr.sel, err = compileExpr(sel, ns); err != nil {
			return nil, err
		}
	}
	_, sorts, err := c.compileParamsAndSorts(n, ns)
	if err != nil {
		return nil, err
	}
	if len(sorts) == 0 {
		return nil, fmt.Errorf("xsl:perform-sort requires at least one xsl:sort")
	}
	instr.sorts = sorts

	if instr.sel == nil {
		if instr.body, err = c.compileNodes(nonSortChildren(n), n); err != nil {
			return nil, err
		}
	}
	return instr, nil
}

// checkAttributeSetRefs applies XTSE0710 once every module has compiled.
//
// The runtime already reports it when an undeclared set is actually used, but
// the error is static: a stylesheet naming a set that does not exist is wrong
// whether or not the instruction naming it is ever reached, and error-0710a
// declares one inside another attribute set that no template applies. The
// check is deferred rather than made as each reference compiles, because
// xsl:attribute-set is a top-level declaration and may name one written below
// it or in a module imported afterwards.
// checkAttributeSetCycles applies XTSE0720 once every module has compiled.
//
// 10.2.2: "An attribute set A is dependent on an attribute set B if A contains
// an attribute set invocation that is bound to B, or ... to an attribute set C
// that is dependent on B. A cycle exists if any attribute set is dependent on
// itself. Such a cycle is an error even if the attribute set is never
// invoked."
//
// The graph is walked per package, because that sentence is qualified: "it is
// possible to detect a cycle during the static analysis of a PACKAGE, before
// it is known how the package will be used". A cycle that closes only when an
// override from a using package is composed in is not visible to either
// package's static analysis, and is the dynamic XTDE0640 instead. A
// declaration written inside an xsl:override counts as the using package's,
// which is what keeps the two apart.
func (c *compiler) checkAttributeSetCycles() error {
	// Edges are collected per package: only a use that both ends of agree on
	// is an edge the static analysis of that package can see.
	uses := map[int]map[string][]string{}
	for key, sets := range c.sheet.attributeSets {
		for _, as := range sets {
			m := uses[as.pkg]
			if m == nil {
				m = map[string][]string{}
				uses[as.pkg] = m
			}
			for _, u := range as.uses {
				uk := u.name.Clark()
				for _, target := range c.sheet.attributeSets[uk] {
					if target.pkg == as.pkg {
						m[key] = append(m[key], uk)
						break
					}
				}
			}
		}
	}
	const (
		unvisited = 0
		active    = 1
		done      = 2
	)
	for pkg, m := range uses {
		_ = pkg
		state := map[string]int{}
		var visit func(string) error
		visit = func(n string) error {
			switch state[n] {
			case active:
				return fmt.Errorf(
					"XTSE0720: xsl:attribute-set %s is dependent on itself",
					clarkToEQName(n))
			case done:
				return nil
			}
			state[n] = active
			for _, next := range m[n] {
				if err := visit(next); err != nil {
					return err
				}
			}
			state[n] = done
			return nil
		}
		for n := range m {
			if err := visit(n); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *compiler) checkAttributeSetRefs() error {
	for _, n := range c.usedAttributeSets {
		if _, ok := c.sheet.attributeSets[n.Clark()]; !ok {
			return fmt.Errorf(
				"XTSE0710: use-attribute-sets names %q, but no "+
					"xsl:attribute-set is declared with that name",
				n.Lexical())
		}
	}
	return nil
}
