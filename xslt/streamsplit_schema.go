package xslt

// Lane MM8: a min or max below the document node over schema-typed values
// (streamable-055).
//
// A fold of min or max gives up (errSplitGiveUp) when its values are of
// several types, and below the document node there is no tree to fall back
// to, so the memory rule refuses it (streamSplit.memory). The values are of
// one type when the matches are elements named E (the path's last step, no
// tail) and the document is
//
//   - not validated: every value is xs:untypedAtomic; or
//   - validated strictly, or against a type (validation="strict", type=):
//     every element is assessed with a governing declaration from the
//     schema, since the schema has no wildcard (nor open content) that
//     could admit one unassessed, so an E element's type is the type of one
//     of E's declarations -- global or local -- or one derived from it by
//     restriction (xsi:type). Each of them is a list of one built-in atomic
//     item type X, which every restriction of a list keeps (XSD 1.1 Part 2
//     §4.1.2.2, {item type definition}): each value is an X.
//
// A lax validation may leave an E element unassessed beside one that is
// assessed, and a document the harness typed (asRead) carries its own
// schema's types: schemaFoldStreams refuses those runs before anything is
// read, the container then running on the tree.

import (
	"github.com/knroy/go-xml/v2/internal/xsdstream"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
	"github.com/knroy/go-xml/v2/xsd"
)

// schemaOne reports whether s, a min or max below the document node, folds
// values of one type under sch (above), and records E in names.
func (s *streamSplit) schemaOne(sch *xsd.Schema, names map[xdm.QName]bool) bool {
	if sch == nil || s.cond != nil || len(s.steps) == 0 || s.tail != nil || s.tailText != "" || s.strVal {
		return false
	}
	nt, ok := s.steps[len(s.steps)-1].test.(*xpath.NameTest)
	if !ok || nt.AnyURI || nt.AnyLocal || schemaItemType(sch, nt.Name) == nil {
		return false
	}
	names[xdm.QName{URI: nt.Name.URI, Local: nt.Name.Local}] = true
	return true
}

// schemaItemType is X, the built-in atomic item type every element named n
// has values of under sch (above), or nil.
func schemaItemType(sch *xsd.Schema, n xdm.QName) *xsd.SimpleType {
	w := schemaWalk{seen: map[any]bool{}}
	for _, d := range sch.Elements {
		w.elem(d)
	}
	for q, t := range sch.Types {
		if q.URI != xdm.NSXS { // the built-ins: xs:anyType's wildcard is none of the schema's
			w.typ(t)
		}
	}
	for _, g := range sch.ModelGroups {
		w.group(g.Group)
	}
	if w.open {
		return nil
	}
	var x *xsd.SimpleType
	for _, d := range w.decls {
		if d.Name.URI != n.URI || d.Name.Local != n.Local {
			continue
		}
		it := listItem(d)
		if it == nil || x != nil && it != x {
			return nil
		}
		x = it
	}
	return x
}

// listItem is the built-in atomic item type of d's list type, or nil.
func listItem(d *xsd.ElementDecl) *xsd.SimpleType {
	if len(d.Alternatives) > 0 {
		return nil
	}
	st, ok := d.Type.(*xsd.SimpleType)
	if ct, isCT := d.Type.(*xsd.ComplexType); isCT && ct.Content == xsd.ContentSimple {
		st, ok = ct.SimpleContent, ct.SimpleContent != nil
	}
	if !ok || st.Variety != xsd.VarietyList || st.ItemType == nil {
		return nil
	}
	it := st.ItemType
	if it.Variety != xsd.VarietyAtomic || it.Name.URI != xdm.NSXS {
		return nil
	}
	return it
}

// schemaWalk collects every element declaration of a schema, and whether
// any content model admits an element through a wildcard (open).
type schemaWalk struct {
	seen  map[any]bool
	decls []*xsd.ElementDecl
	open  bool
}

func (w *schemaWalk) elem(d *xsd.ElementDecl) {
	if d == nil || w.seen[d] {
		return
	}
	w.seen[d] = true
	w.decls = append(w.decls, d)
	w.typ(d.Type)
}

func (w *schemaWalk) typ(t xsd.Type) {
	ct, ok := t.(*xsd.ComplexType)
	if !ok || w.seen[ct] {
		return
	}
	w.seen[ct] = true
	if ct.OpenContent != nil {
		w.open = true
	}
	if ct.Particle != nil {
		w.particle(ct.Particle)
	}
	// A base type is global, walked as one of sch.Types.
}

func (w *schemaWalk) particle(p *xsd.Particle) {
	switch t := p.Term.(type) {
	case *xsd.ElementDecl:
		w.elem(t)
	case *xsd.ModelGroup:
		w.group(t)
	case *xsd.Wildcard:
		w.open = true
	}
}

func (w *schemaWalk) group(g *xsd.ModelGroup) {
	if g == nil || w.seen[g] {
		return
	}
	w.seen[g] = true
	for _, p := range g.Particles {
		w.particle(p)
	}
}

// schemaNames is x.schemaFold, made.
func (x *streamExec) schemaNames() map[xdm.QName]bool {
	if x.schemaFold == nil {
		x.schemaFold = map[xdm.QName]bool{}
	}
	return x.schemaFold
}

// noteSchemaFolds records the names x's folds rely on.
func (s *Stylesheet) noteSchemaFolds(x *streamExec) {
	for n := range x.schemaFold {
		if s.schemaFolds == nil {
			s.schemaFolds = map[xdm.QName]bool{}
		}
		s.schemaFolds[n] = true
	}
}

// schemaFoldStreams reports whether a container may stream a document
// validated as sv (nil: not at all) when plans fold on schema types: not
// laxly, and a document the harness typed only where its schema gives each
// name's elements the item types the stylesheet's does.
//
// ponytail: the harness's schema is walked per run; cache it per schema if
// a large one streams often.
func (s *Stylesheet) schemaFoldStreams(sv *streamValidation) bool {
	if len(s.schemaFolds) == 0 || sv == nil {
		return true
	}
	if sv.schema != nil && sv.mode.Root == xsdstream.Lax {
		return false
	}
	if sv.pre != nil {
		pre, ok := sv.pre.(*xsd.Schema)
		if !ok || s.schema == nil {
			return false
		}
		for n := range s.schemaFolds {
			x, y := schemaItemType(pre, n), schemaItemType(s.schema, n)
			if x == nil || y == nil || x.Name != y.Name {
				return false
			}
		}
	}
	return true
}
