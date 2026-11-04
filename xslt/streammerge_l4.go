package xslt

// Streamed xsl:merge, lane L4: an action asking for last(), an action
// telling messages, boolean keys and map-lookup keys.
//
// last(). The tree gives the action the number of groups, known only once
// every input has been read. Such a merge forms every group first, holding
// them (groupsFirst), and then runs the action on each: what it holds is
// the snapshots of all the selected items, which the tree holds too, beside
// the documents themselves (collect: each document whole, and a snapshot of
// each item) -- never more than the tree.
//
// xsl:message and xsl:assert. A failed run's messages are taken back
// before the tree runs again (effectsMark). A message told by the action
// comes after every message of the keys in the tree (the tree computes all
// keys first) but between them in the stream, so the action may tell
// messages only when no key, select or for-each expression can: none calls
// a function of no standard namespace, or a function item (mergeKeysSilent).

import (
	"regexp"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// mergeActionFacts is what mergeActionWalk found in an action.
type mergeActionFacts struct {
	ok, last, effects bool
}

// mergeActionCheck checks the merge action (mergeActionWalk).
func mergeActionCheck(action *xdm.Node) mergeActionFacts {
	var f mergeActionFacts
	f.ok = mergeActionWalk(action, map[string]bool{}, &f)
	return f
}

// heldMergeGroup is a group formed before any action runs (groupsFirst).
type heldMergeGroup struct {
	binding *mergeGroupBinding
	keys    []sortValue
}

// mergeAction runs the action on one group, at position pos of size groups
// (0: not known; the action does not ask).
func (i *mergeInstr) mergeAction(rt *runtime, out *outputBuilder, binding *mergeGroupBinding, keys []sortValue,
	nameSeq xdm.Sequence, pos, size int) error {
	all := binding.all()
	sub := rt.withCurrent(all[0], pos, size)
	sub = sub.withVar(currentMergeGroupVar, xdm.One(&xdm.Opaque{Value: binding}))
	sub = sub.withVar(currentMergeKeyVar, mergeKeySequence(keys))
	sub = sub.withVar(currentMergeSourcesVar, nameSeq)
	return execSequence(i.action, sub, out)
}

var (
	prefixedCall = regexp.MustCompile(`([A-Za-z_][\w.\-]*):[A-Za-z_][\w.\-]*\s*\(`)
	varCall      = regexp.MustCompile(`\$[\w.\-:]+\s*\(|\)\s*\(|#\s*\d|Q\{`)
)

// mergeKeysSilent reports whether no expression of i's merge sources and
// keys can tell a message: none calls a function outside the fn, xs, math,
// map and array namespaces, nor a function item.
func mergeKeysSilent(i *mergeInstr) bool {
	silent := func(el *xdm.Node) bool {
		for a := range el.Attrs() {
			v := a.Value()
			if varCall.MatchString(v) {
				return false
			}
			for _, m := range prefixedCall.FindAllStringSubmatch(v, -1) {
				uri, ok := el.LookupPrefix(m[1])
				if !ok {
					return false
				}
				switch uri {
				case fnNS, xdm.NSXS, xdm.NSMath, xdm.NSMap, xdm.NSArray:
				default:
					return false
				}
			}
		}
		return true
	}
	for _, src := range i.sources {
		if len(src.keyElems) == 0 || !silent(src.keyElems[0].Parent()) {
			return false
		}
		for _, k := range src.keyElems {
			if !silent(k) {
				return false
			}
		}
	}
	return true
}

// mergeKeyClassL4 is mergeKeyClass for the keys it adds: a boolean
// function's ("xs:boolean"), and a lookup $m(k) in a global map of integer
// literals ("xs:integer"); "" otherwise.
func mergeKeyClassL4(el *xdm.Node, e xpath.Expr) string {
	switch x := e.(type) {
	case *xpath.FuncCall:
		if x.Name.URI == xdm.NSMap && x.Name.Local == "get" && len(x.Args) == 2 {
			// $m(k), as parseAt makes a lookup in a variable typed a map.
			if v, ok := x.Args[0].(*xpath.VarRef); ok && v.Name.URI == "" && integerMapGlobal(documentElementOf(el), v.Name.Local) {
				return "xs:integer"
			}
			return ""
		}
		if x.Name.URI != fnNS {
			return ""
		}
		if (x.Name.Local == "accumulator-before" || x.Name.Local == "accumulator-after") && len(x.Args) == 1 {
			// An accumulator of the module with an atomic as type
			// (streammerge_accum.go): its values are of that type.
			lit, ok := x.Args[0].(*xpath.Literal)
			if !ok || lit.Val == nil {
				return ""
			}
			top := documentElementOf(el)
			d := accumDeclNamed(top, strings.TrimSpace(lit.Val.String()))
			if d == nil || reachesOtherModules(top) {
				return ""
			}
			if uri, ok := d.LookupPrefix("xs"); !ok || uri != xdm.NSXS {
				return ""
			}
			switch as := strings.TrimSpace(d.AttrValue("as")); as {
			case "xs:integer", "xs:decimal", "xs:date", "xs:dateTime", "xs:time":
				return as
			case "xs:string":
				return "string"
			}
			return ""
		}
		switch x.Name.Local {
		case "true", "false":
			if len(x.Args) == 0 {
				return "xs:boolean"
			}
		case "boolean", "not", "exists", "empty":
			if len(x.Args) == 1 {
				return "xs:boolean"
			}
		}
	case *xpath.DynamicCall:
		v, ok := x.Target.(*xpath.VarRef)
		if ok && len(x.Args) == 1 && v.Name.URI == "" && integerMapGlobal(documentElementOf(el), v.Name.Local) {
			return "xs:integer"
		}
	}
	return ""
}

// integerMapGlobal reports whether name is bound, in the module (the whole
// stylesheet), by one top-level xsl:variable only, to a map constructor
// whose values are integer literals: a lookup in it gives an xs:integer or
// nothing.
func integerMapGlobal(top *xdm.Node, name string) bool {
	if !xdm.IsNCName(name) {
		return false
	}
	var decl *xdm.Node
	n := 0
	walkElements(top, func(e *xdm.Node) bool {
		if isXSL(e, "import") || isXSL(e, "include") || isXSL(e, "use-package") {
			n = 2
		}
		if (isXSL(e, "variable") || isXSL(e, "param")) && strings.TrimSpace(e.AttrValue("name")) == name {
			n++
			decl = e
		}
		return n < 2
	})
	if n != 1 || !isXSL(decl, "variable") || decl.Parent() != top || hasContent(decl) {
		return false
	}
	m, ok := parseAt(decl, decl.AttrValue("select")).(*xpath.MapConstructor)
	if !ok || len(m.Values) == 0 {
		return false
	}
	for _, v := range m.Values {
		if !isIntLiteral(v) {
			return false
		}
	}
	return true
}

// mergePreRead is an error the streamed merge met before it read anything,
// in an evaluation the tree's merge makes first too, in the same order: the
// keys' attribute value templates (resolveKeys), the collection of the
// sources before the first streamed one, and that one's for-each-source
// (the tree's collect evaluates it before loading anything of it). It is
// the tree's first error exactly, so it is the merge's, with no re-run
// (merge-043).
type mergePreRead struct{ err error }

func (e mergePreRead) Error() string { return e.err.Error() }

// sawStreamed reports whether a source before s is streamed (read on a
// cursor).
func (i *mergeInstr) sawStreamed(s int) bool {
	for _, src := range i.sources[:s] {
		if src.streamed && src.sourceURIs != nil {
			return true
		}
	}
	return false
}
