package xslt

import (
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// ruleIndex narrows template dispatch to the rules that could match a node.
//
// For each mode it holds, per node kind and per element/attribute name, the
// indexes into Stylesheet.templates of the rules whose pattern may match such
// a node, in the templates' own (precedence, priority, declaration) order.
// Scanning one of those lists from index start visits the same matching
// rules in the same order as the linear scan, so the winner, the resume index
// handed to xsl:next-match and any pattern error are all unchanged.
//
// A rule is left out of a list only when Pattern.mayMatch is false for every
// node the list serves. mayMatch is the pure node-test check Pattern.matches
// makes before it evaluates anything, so a rule left out would have answered
// (false, nil) with no side effect.
type ruleIndex map[string]*modeRules

type modeRules struct {
	byKind [xdm.KindNamespace + 1][]int
	byName map[ruleName][]int
}

type ruleName struct {
	kind       xdm.NodeKind
	uri, local string
}

// buildRuleIndex indexes s.templates, which must already be sorted.
func (s *Stylesheet) buildRuleIndex() {
	modes := map[string]bool{"": true}
	for _, t := range s.templates {
		for _, m := range t.Mode {
			if m == "#default" {
				m = ""
			}
			if m != "#all" {
				modes[m] = true
			}
		}
	}
	type keys struct {
		kinds [xdm.KindNamespace + 1]bool
		names []ruleName
	}
	tk := make([]keys, len(s.templates))
	for i, t := range s.templates {
		tk[i].kinds, tk[i].names = ruleKeys(t)
	}
	idx := ruleIndex{}
	for mode := range modes {
		mr := &modeRules{byName: map[ruleName][]int{}}
		for i, t := range s.templates {
			if !t.matchesMode(mode) {
				continue
			}
			kinds, names := tk[i].kinds, tk[i].names
			for k := range mr.byKind {
				if kinds[k] {
					mr.byKind[k] = append(mr.byKind[k], i)
				}
			}
			for _, n := range names {
				if l := mr.byName[n]; len(l) == 0 || l[len(l)-1] != i {
					mr.byName[n] = append(l, i)
				}
			}
		}
		// A name's list also needs every rule that matches its kind
		// whatever the name: merge the two, both already in order.
		for n, own := range mr.byName {
			mr.byName[n] = mergeSorted(own, mr.byKind[n.kind])
		}
		idx[mode] = mr
	}
	s.rules = idx
}

func mergeSorted(a, b []int) []int {
	out := make([]int, 0, len(a)+len(b))
	for len(a) > 0 && len(b) > 0 {
		if a[0] < b[0] {
			out, a = append(out, a[0]), a[1:]
		} else if b[0] < a[0] {
			out, b = append(out, b[0]), b[1:]
		} else {
			out, a, b = append(out, a[0]), a[1:], b[1:]
		}
	}
	return append(append(out, a...), b...)
}

// ruleKeys reports which nodes t's pattern may match on node test alone: any
// node of a kind set in kinds, or an element or attribute with one of names.
// It mirrors Pattern.mayMatch and nodeTestHolds; anything it cannot read is
// widened to every kind.
func ruleKeys(t *Template) (kinds [xdm.KindNamespace + 1]bool, names []ruleName) {
	all := func() {
		for k := range kinds {
			kinds[k] = true
		}
	}
	p := t.Match
	if p == nil || len(p.general) > 0 {
		all()
		return
	}
	for _, a := range p.alts {
		if a.call != nil {
			all()
			return
		}
		if len(a.steps) == 0 {
			continue // mayMatch never accepts through this alternative
		}
		s := a.steps[len(a.steps)-1]
		// nodeTestHolds admits only attributes on the attribute axis, only
		// namespace nodes on the namespace axis, and neither elsewhere.
		var axis []xdm.NodeKind
		switch {
		case s.attribute && s.namespace:
			continue
		case s.attribute:
			axis = []xdm.NodeKind{xdm.KindAttribute}
		case s.namespace:
			axis = []xdm.NodeKind{xdm.KindNamespace}
		default:
			axis = []xdm.NodeKind{xdm.KindDocument, xdm.KindElement,
				xdm.KindText, xdm.KindComment, xdm.KindPI}
		}
		switch nt := s.nodeTest.(type) {
		case *xpath.NameTest:
			// A name test matches only the axis's principal kind.
			principal := xdm.KindElement
			if s.attribute {
				principal = xdm.KindAttribute
			} else if s.namespace {
				kinds[xdm.KindNamespace] = true
				continue
			}
			if nt.AnyURI || nt.AnyLocal {
				kinds[principal] = true
				continue
			}
			names = append(names, ruleName{principal, nt.Name.URI, nt.Name.Local})
		case *xpath.KindTest:
			for _, k := range axis {
				if nt.Any || nt.Kind == k {
					kinds[k] = true
				}
			}
		default:
			for _, k := range axis {
				kinds[k] = true
			}
		}
	}
	return
}

// candidates returns the indexes of the rules in mode that may match node,
// and false when the mode or node is one the index does not cover.
func (s *Stylesheet) candidates(node *xdm.Node, mode string) ([]int, bool) {
	if node == nil || s.rules == nil {
		return nil, false
	}
	mr := s.rules[mode]
	if mr == nil || int(node.Kind()) >= len(mr.byKind) {
		return nil, false
	}
	if node.Kind() == xdm.KindElement || node.Kind() == xdm.KindAttribute {
		if l, ok := mr.byName[ruleName{node.Kind(), node.Name().URI, node.Name().Local}]; ok {
			return l, true
		}
	}
	return mr.byKind[node.Kind()], true
}
