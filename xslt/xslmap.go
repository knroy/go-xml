package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// xsl:map and xsl:map-entry, XSLT 3.0 section 14.
//
// These are the instruction spelling of a map constructor. The map itself is
// XPath 3.1's, already implemented in xdm — nothing here reimplements maps,
// and the two instructions between them do very little beyond arranging for
// the right sequences to reach xdm.MapBuilder.
//
// The division of labour is the specification's: xsl:map-entry returns a
// singleton map, and xsl:map merges whatever maps its body produced. Building
// entries directly into an enclosing xsl:map would be simpler but wrong, since
// an xsl:map-entry may appear inside an xsl:for-each, or be produced by a
// called template, and the enclosing instruction sees only the resulting
// sequence.

// mapInstr implements xsl:map.
type mapInstr struct {
	body []Instruction
}

// Execute merges the maps the body produced, rejecting duplicate keys.
//
// The spec defines the result as map:merge($maps, map{"duplicates":"reject"}),
// and the merge is done here rather than by calling that function because the
// error code differs: a duplicate is XTDE3365 in this context.
func (i *mapInstr) Execute(rt *runtime, out *outputBuilder) error {
	sub := newOutputBuilder()
	if err := execSequence(i.body, rt.temporaryOutput(), sub); err != nil {
		return err
	}

	b := xdm.NewMapBuilder()
	for _, it := range sub.sequence() {
		m, ok := it.(*xdm.MapItem)
		if !ok {
			// "A type error occurs if the result of evaluating the sequence
			// constructor is not an instance of the required type map(*)*."
			// In practice this catches the sequence constructor that builds
			// elements or text, which is the usual mistake — maps-006 wraps
			// an xsl:map in a literal element and expects to be told.
			return fmt.Errorf(
				"XTTE3375: the content of xsl:map must be a sequence of maps, but it produced %s",
				it.TypeName())
		}
		var dup *xdm.Atomic
		if err := m.Entries(func(key *xdm.Atomic, value xdm.Sequence) error {
			if _, exists, err := b.Lookup(key); err != nil {
				return err
			} else if exists {
				dup = key
				return nil
			}
			return b.Set(key, value)
		}); err != nil {
			return err
		}
		if dup != nil {
			return fmt.Errorf(
				"XTDE3365: duplicate key %q among the maps produced by xsl:map", dup.String())
		}
	}
	return appendMap(out, b.Build())
}

// appendMap adds a map to the result, or reports that it cannot be.
//
// A map is not a node and has no textual form, so there is nowhere for it to
// go inside an element under construction: 5.7.1 has no rule that would turn
// one into content. maps-006 wraps an xsl:map in a literal result element for
// exactly this reason and expects XTDE0450. At the top of a sequence the map
// is an ordinary item, which is what makes xsl:variable over an xsl:map work.
func appendMap(out *outputBuilder, m *xdm.MapItem) error {
	if out.open != nil {
		return fmt.Errorf(
			"XTDE0450: a map cannot be added to the content of element %s",
			out.open.Name.Lexical())
	}
	out.items = append(out.items, m)
	return nil
}

// mapEntryInstr implements xsl:map-entry, which returns a singleton map.
type mapEntryInstr struct {
	key *xpath.Compiled
	sel *xpath.Compiled
	// body is the sequence constructor, used when there is no select.
	body []Instruction
}

func (i *mapEntryInstr) Execute(rt *runtime, out *outputBuilder) error {
	keySeq, err := i.key.Eval(rt.ctx)
	if err != nil {
		return err
	}
	// The key is "converted to the required type xs:anyAtomicType by applying
	// the function conversion rules", which atomizes it and then requires
	// exactly one value.
	atomized, err := xdm.AtomizeChecked(keySeq)
	if err != nil {
		return err
	}
	if len(atomized) != 1 {
		return fmt.Errorf(
			"XPTY0004: the key of xsl:map-entry must be a single atomic value, not %d items",
			len(atomized))
	}
	key, ok := atomized[0].(*xdm.Atomic)
	if !ok {
		return fmt.Errorf("XPTY0004: the key of xsl:map-entry must be an atomic value")
	}
	// "If the supplied key (after conversion) is of type xs:untypedAtomic, it
	// is cast to xs:string." Without this a key read from an unvalidated
	// document would not compare equal to the string literal the stylesheet
	// looks it up with, which is how nearly every map keyed off the source
	// document is written.
	if key.Type == xdm.TypeUntypedAtomic {
		key = xdm.NewString(key.String())
	}

	var value xdm.Sequence
	if i.sel != nil {
		if value, err = i.sel.Eval(rt.ctx); err != nil {
			return err
		}
	} else {
		sub := newOutputBuilder()
		if err := execSequence(i.body, rt.temporaryOutput(), sub); err != nil {
			return err
		}
		value = sub.sequence()
	}

	b := xdm.NewMapBuilder()
	if err := b.Set(key, value); err != nil {
		return err
	}
	return appendMap(out, b.Build())
}

// compileMap compiles xsl:map and xsl:map-entry.
func (c *compiler) compileMap(n *xdm.Node, ns xpath.NamespaceResolver) (Instruction, error) {
	if n.Name.Local == "map" {
		body, err := c.compileSequence(n, n)
		if err != nil {
			return nil, err
		}
		return &mapInstr{body: body}, nil
	}

	key, err := requiredExpr(n, "key", ns)
	if err != nil {
		return nil, err
	}
	instr := &mapEntryInstr{key: key}
	if a := n.Attr("", "select"); a != nil {
		if instr.sel, err = compileExpr(a.Value, ns); err != nil {
			return nil, err
		}
	}
	if instr.body, err = c.compileSequence(n, n); err != nil {
		return nil, err
	}
	if instr.sel != nil && len(instr.body) > 0 {
		// XTSE3280 is specific to xsl:map-entry: "It is a static error if the
		// select attribute is present unless the element has no children
		// other than xsl:fallback elements." compileSequence has already
		// dropped the xsl:fallback children, so anything left is a violation.
		return nil, fmt.Errorf(
			"XTSE3280: xsl:map-entry has both a select attribute and content")
	}
	return instr, nil
}
