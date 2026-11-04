package xslt

// xsl:sequence and xsl:variable in a rule for a text, comment or PI node
// (lane L3: si-value-of-100, su-absorbing-016). The rule runs on a copy of
// the leaf, which stands in for the node: an instruction that could hand
// the copy on as the node is refused (motionless, noIdent). One whose
// select can return only atomic values -- string(), concat(...), a
// stylesheet function declared to return atomic values -- hands on no node,
// so the copy is never taken for the original.

import (
	"strings"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/xdm"
)

// leafAtomic reports whether el, an xsl:sequence or xsl:variable, returns
// only atomic values: a select atomicValued says so, and no content.
func leafAtomic(el *xdm.Node) bool {
	src := el.AttrValue("select")
	if src == "" {
		return false
	}
	for ch := range el.Children() {
		switch ch.Kind() {
		case xdm.KindElement:
			return false
		case xdm.KindText:
			if strings.TrimSpace(ch.Value()) != "" {
				return false
			}
		}
	}
	e := parseAt(el, src)
	return e != nil && atomicValued(e)
}

// laxAtomize marks s, and every branch of a choice, as the split of an
// xsl:comment or xsl:processing-instruction select: its value is made by
// constructedText, which atomizes with xdm.Atomize, so a typed match is
// atomized so too (no FOTY0012 where the tree raises none).
func (s *streamSplit) laxAtomize() {
	s.laxAtom = true
	if s.cond != nil {
		s.then.laxAtomize()
		s.els.laxAtomize()
	}
}

// groundAtomized is groundAtomized of the cursor for s: unchecked for an
// xsl:comment or xsl:processing-instruction (laxAtom); and with keepEmpty a
// match whose typed value is empty is the copy itself, which simple content
// atomizes to nothing but which separates the text nodes beside it, as the
// element does in the tree (streamcopy_l3text.go).
func (s *streamSplit) groundAtomized(c *xdmstream.Cursor) (xdm.Sequence, error) {
	if !s.laxAtom && !s.keepEmpty {
		return groundAtomized(c)
	}
	g, err := c.Ground()
	if err != nil {
		return nil, err
	}
	var items xdm.Sequence
	if s.laxAtom {
		items = xdm.Atomize(xdm.One(g))
	} else if items, err = xdm.AtomizeChecked(xdm.One(g)); err != nil {
		return nil, err
	}
	if len(items) == 0 && s.keepEmpty {
		return xdm.One(g), nil
	}
	return items, nil
}
