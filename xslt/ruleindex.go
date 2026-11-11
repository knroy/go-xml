package xslt

import (
	"sort"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// ruleIndex narrows template dispatch to the rules that could match a node.
//
// For each mode it holds, per node kind and per element/attribute name, the
// indexes into Stylesheet.templates of the rules whose pattern may match such
// a node, in the templates' own (precedence, priority, declaration) order.
// Merging a node's lists (ruleCands) from index start visits the same matching
// rules in the same order as the linear scan, so the winner, the resume index
// handed to xsl:next-match and any pattern error are all unchanged.
//
// A rule is left out of a list only when Pattern.mayMatch is false for every
// node the list serves. mayMatch is the pure node-test check Pattern.matches
// makes before it evaluates anything, so a rule left out would have answered
// (false, nil) with no side effect.
type ruleIndex map[string]*modeRules

type modeRules struct {
	// empty records that no template rule is in the mode at all.
	empty  bool
	byKind [xdm.KindNamespace + 1][]int
	// byName holds only the rules keyed on that name; the rules that match
	// the name's kind whatever the name stay in byKind, and dispatch merges
	// the two, so a wildcard rule is stored once per mode, not once per name.
	byName map[ruleName][]int
	// all is the index of the mode="#all" rules, shared by every mode so they
	// are stored once, not once per mode; nil on that shared index itself.
	all *modeRules
}

type ruleName struct {
	kind       xdm.NodeKind
	uri, local string
}

// fanoutFactor bounds the entries one xsl:template declaration may add to
// the index: a rule naming m modes whose branches need e entries each mode
// costs m*e, and once that passes fanoutFactor*(m+e), a multiple of what it
// took to write, its modes are left out of the index and dispatch in them
// takes the linear scan. Every other mode stays indexed and the index stays
// linear in the stylesheet, not in the product of one rule's @mode and
// @match.
// ponytail: whole modes fall back; sharing one list per mode set would keep
// them indexed if a real stylesheet ever needs it.
const fanoutFactor = 16

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
	for m := range s.modeNoMatch {
		modes[m] = true
	}
	all := &modeRules{byName: map[ruleName][]int{}, empty: true}
	idx := ruleIndex{}
	for mode := range modes {
		idx[mode] = &modeRules{byName: map[ruleName][]int{}, empty: true, all: all}
	}
	type keys struct {
		kinds [xdm.KindNamespace + 1]bool
		names []ruleName
	}
	tk := make([]keys, len(s.templates))
	// The branches of one union rule share its @mode (compileTemplate), so
	// the cost is summed over the whole declaration.
	cost := map[int]int{}
	for i, t := range s.templates {
		tk[i].kinds, tk[i].names = ruleKeys(t)
		cost[t.unionGroup] += len(tk[i].names) + 1
	}
	var unindexed []string
	for i, t := range s.templates {
		if m, e := len(t.Mode), cost[t.unionGroup]; m > 1 && m*e > fanoutFactor*(m+e) {
			unindexed = append(unindexed, t.Mode...)
			continue
		}
		in := t.Mode
		if len(in) == 0 {
			in = []string{""}
		}
		for _, m := range in {
			mr := all
			switch m {
			case "#all": // XTSE0550: it stands alone
			case "#default":
				mr = idx[""]
			default:
				mr = idx[m]
			}
			mr.add(i, &tk[i].kinds, tk[i].names)
		}
	}
	if !all.empty {
		for _, mr := range idx {
			mr.empty = false
		}
	}
	for _, m := range unindexed {
		if m == "#default" {
			m = ""
		}
		delete(idx, m)
	}
	s.rules = idx
}

// add files rule i under kinds and names. Rules are added in index order,
// so every list stays sorted; a repeat of i (a duplicate @mode token or name)
// is dropped.
func (mr *modeRules) add(i int, kinds *[xdm.KindNamespace + 1]bool, names []ruleName) {
	mr.empty = false
	for k := range mr.byKind {
		if kinds[k] {
			if l := mr.byKind[k]; len(l) == 0 || l[len(l)-1] != i {
				mr.byKind[k] = append(l, i)
			}
		}
	}
	for _, n := range names {
		if l := mr.byName[n]; len(l) == 0 || l[len(l)-1] != i {
			mr.byName[n] = append(l, i)
		}
	}
}

// ruleCands is the candidate rules for one node: up to four ascending lists
// (the mode's name and kind lists, then the #all index's) that next merges
// in place, so dispatch allocates nothing.
type ruleCands [4][]int

// from drops every candidate below start.
func (c *ruleCands) from(start int) {
	if start == 0 {
		return
	}
	for k, l := range c {
		c[k] = l[sort.SearchInts(l, start):]
	}
}

// next pops the lowest remaining candidate, once even when several lists
// hold it.
func (c *ruleCands) next() (int, bool) {
	least := -1
	for _, l := range c {
		if len(l) > 0 && (least < 0 || l[0] < least) {
			least = l[0]
		}
	}
	if least < 0 {
		return 0, false
	}
	for k, l := range c {
		if len(l) > 0 && l[0] == least {
			c[k] = l[1:]
		}
	}
	return least, true
}

// none reports whether no candidate is left.
func (c *ruleCands) none() bool {
	return len(c[0])+len(c[1])+len(c[2])+len(c[3]) == 0
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

// candidates returns the rules in mode that may match node, and false when
// the mode or node is one the index does not cover.
func (s *Stylesheet) candidates(node *xdm.Node, mode string) (ruleCands, bool) {
	var c ruleCands
	if node == nil || s.rules == nil {
		return c, false
	}
	mr := s.rules[mode]
	k := node.Kind()
	if mr == nil || int(k) >= len(mr.byKind) {
		return c, false
	}
	c[1] = mr.byKind[k]
	if mr.all != nil {
		c[3] = mr.all.byKind[k]
	}
	if k != xdm.KindElement && k != xdm.KindAttribute {
		return c, true
	}
	named := len(mr.byName) > 0
	allNamed := mr.all != nil && len(mr.all.byName) > 0
	if named || allNamed {
		qn := node.Name()
		key := ruleName{k, qn.URI, qn.Local}
		if named {
			c[0] = mr.byName[key]
		}
		if allNamed {
			c[2] = mr.all.byName[key]
		}
	}
	return c, true
}
