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
		for _, as := range sets {
			if err := expandAttributeSets(rt, as.uses, out, active); err != nil {
				return err
			}
			if err := execSequence(as.body, rt, out); err != nil {
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
		if err := execSequence(i.body, rt, sub); err != nil {
			return err
		}
		uri = stringJoin(sub.sequence(), "")
	}
	uri = strings.TrimSpace(uri)

	if uri == "" {
		return fmt.Errorf("XTDE0930: xsl:namespace must not create a binding to an empty URI")
	}
	if prefix == "xmlns" {
		return fmt.Errorf("XTDE0920: xsl:namespace must not bind the prefix \"xmlns\"")
	}
	if out.open == nil {
		return fmt.Errorf("XTDE0410: xsl:namespace cannot be used outside an element")
	}
	out.open.AddNamespace(prefix, uri)
	return nil
}

func (c *compiler) compileNamespace(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	nameAVT, err := requiredAVT(n, "name", ns)
	if err != nil {
		return nil, err
	}
	instr := &namespaceInstr{name: nameAVT}
	if sel := n.AttrValue("select"); sel != "" {
		if instr.sel, err = xpath.Compile(sel, ns); err != nil {
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
		if err := execSequence(i.body, rt, sub); err != nil {
			return err
		}
		seq = sub.sequence()
	}

	sorted, err := applySorts(rt, seq, i.sorts)
	if err != nil {
		return err
	}
	for _, it := range sorted {
		switch v := it.(type) {
		case *xdm.Node:
			out.appendNode(v)
		case *xdm.Atomic:
			out.appendValue(v)
		}
	}
	return nil
}

func (c *compiler) compilePerformSort(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	instr := &performSortInstr{}
	var err error
	if sel := n.AttrValue("select"); sel != "" {
		if instr.sel, err = xpath.Compile(sel, ns); err != nil {
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
