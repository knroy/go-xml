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
	uses []xdm.QName
	body []Instruction
	// importPrecedence orders declarations of the same name across modules.
	importPrecedence int
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

	as := &attributeSet{name: qn, importPrecedence: precedence}
	for _, u := range strings.Fields(el.AttrValue("use-attribute-sets")) {
		uq, err := resolveQNameAttr(el, u)
		if err != nil {
			return err
		}
		as.uses = append(as.uses, uq)
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
func applyAttributeSets(rt *runtime, names []xdm.QName, out *outputBuilder) error {
	return expandAttributeSets(rt, names, out, map[string]bool{})
}

func expandAttributeSets(rt *runtime, names []xdm.QName, out *outputBuilder,
	active map[string]bool) error {

	for _, n := range names {
		key := n.Clark()
		if active[key] {
			return fmt.Errorf("XTSE0720: circular reference in xsl:attribute-set %q",
				n.Lexical())
		}
		sets, ok := rt.sheet.attributeSets[key]
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
func parseUseAttributeSets(el *xdm.Node) ([]xdm.QName, error) {
	raw := el.AttrValue("use-attribute-sets")
	if a := el.Attr(xdm.NSXSL, "use-attribute-sets"); a != nil {
		raw = a.Value
	}
	var out []xdm.QName
	for _, n := range strings.Fields(raw) {
		qn, err := resolveQNameAttr(el, n)
		if err != nil {
			return nil, err
		}
		out = append(out, qn)
	}
	return out, nil
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
	if !isLexicalAnyURI(uri) {
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
