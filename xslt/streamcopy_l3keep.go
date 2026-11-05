package xslt

// What of each member a group-by in xsl:fork copies (streamcopy_l3group.go)
// the grouping and its body can read, when that is less than the member:
// A3-5, a body reading only count(current-group()) copied every member
// whole. The tree's grouping still runs over the copies, so keys, bodies
// and errors come in its order with its values; a copy only leaves out
// what nothing evaluates.
//
// A member is read in two ways: as the context item (each member in the
// group-by key, the group's first in the body and its sorts), and through
// current-group(). Every such read must be a path whose first step from the
// member is an attribute or a child, by name: the copy keeps those
// attributes, and those children whole. count(), exists() and empty() of
// current-group() itself read nothing of a member. Any other read -- "."
// as a value, string() and its kin with no argument, current(),
// current-group() other than so, a wildcard or kind test, xsl:copy, an
// attribute set, a nested grouping -- leaves the members whole.

import (
	"reflect"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// memberKeep is what a pruned member copy keeps: these attributes of the
// member, and these children with their content.
type memberKeep struct {
	attrs, kids []xdm.QName
}

// has reports whether names holds the expanded name of q.
func hasName(names []xdm.QName, q xdm.QName) bool {
	for _, n := range names {
		if n.URI == q.URI && n.Local == q.Local {
			return true
		}
	}
	return false
}

// prunedMembers returns what of each member el, a copy-free xsl:for-each-group
// (copyFreeBody) with group-by, reads, or nil when that cannot be told.
func prunedMembers(el *xdm.Node) *memberKeep {
	if el.Attr("", "group-by") == nil {
		return nil // a held population sorted by xsl:sort may group by a pattern, not read here
	}
	w := keepWalk{k: &memberKeep{}, ok: true}
	var check func(n *xdm.Node)
	check = func(n *xdm.Node) {
		if !w.ok || n.Kind() != xdm.KindElement {
			return
		}
		xsl := n.Name().URI == xdm.NSXSL
		if xsl && n != el && (n.Name().Local == "copy" || n.Name().Local == "for-each-group" || n.Name().Local == "merge") {
			w.ok = false
			return
		}
		for a := range n.Attrs() {
			if l := a.Name().Local; l == "use-attribute-sets" || xsl && strings.HasPrefix(l, "_") {
				w.ok = false // an attribute set's body, or a shadow attribute's expression, is not read here
				return
			}
		}
		if !nodeExprs(n, func(a *xdm.Node, e xpath.Expr) bool {
			if n != el || a.Name().Local != "select" { // the population is read off the cursor
				w.expr(e, true)
			}
			return w.ok
		}) {
			w.ok = false
			return
		}
		for ch := range n.Children() {
			check(ch)
		}
	}
	check(el)
	if !w.ok {
		return nil
	}
	return w.k
}

// keepWalk gathers into k what expressions read of a member; ok turns false
// at a read it cannot name.
type keepWalk struct {
	k  *memberKeep
	ok bool
}

// ctxFns are the functions that, with no argument, read their context item
// whole.
var ctxFns = map[string]bool{"string": true, "data": true, "number": true, "string-length": true,
	"normalize-space": true, "nilled": true, "has-children": true}

// expr walks e, whose focus is a member when member is set.
func (w *keepWalk) expr(e xpath.Expr, member bool) {
	if !w.ok || e == nil {
		return
	}
	switch x := e.(type) {
	case *xpath.ContextItem:
		if member {
			w.ok = false
		}
		return
	case *xpath.FuncCall:
		if x.Name.URI == fnNS {
			switch l := x.Name.Local; {
			case l == "current-group" || l == "current":
				w.ok = false // the members themselves
				return
			case (l == "count" || l == "exists" || l == "empty") && len(x.Args) == 1 && isCurrentGroup(x.Args[0]):
				return
			case ctxFns[l] && len(x.Args) == 0 && member:
				w.ok = false
				return
			}
		}
	case *xpath.PathExpr:
		w.path(x.Steps, member)
		return
	case *xpath.Step:
		w.path([]xpath.Expr{x}, member)
		return
	case *xpath.FilterExpr:
		w.expr(x.Base, member)
		for _, p := range x.Predicates {
			w.expr(p, false)
		}
		return
	case *xpath.SimpleMap:
		w.expr(x.Left, member)
		w.expr(x.Right, false)
		return
	case *xpath.LookupExpr:
		if x.Base == nil && member {
			w.ok = false
			return
		}
	}
	for _, c := range exprKids(e) {
		w.expr(c, member)
	}
}

// path walks a path's steps, the first's focus a member when member is set.
// What a step selects from a member is kept; what lies past it is inside
// what is kept, read freely.
func (w *keepWalk) path(steps []xpath.Expr, member bool) {
	i := 0
	switch h := steps[0].(type) {
	case *xpath.ContextItem:
		i = 1 // ./x: x from the focus
	case *xpath.FuncCall:
		if isCurrentGroup(h) {
			i, member = 1, true
			break
		}
		w.expr(h, member)
		i, member = 1, false
	case *xpath.Step:
	default:
		w.expr(h, member)
		i, member = 1, false
	}
	if member {
		if i >= len(steps) {
			w.ok = false
			return
		}
		st, ok := steps[i].(*xpath.Step)
		nt, named := (*xpath.NameTest)(nil), false
		if ok {
			nt, named = st.Test.(*xpath.NameTest)
		}
		if !named || nt.AnyURI || nt.AnyLocal {
			w.ok = false
			return
		}
		switch st.Axis {
		case xpath.AxisAttribute:
			if !hasName(w.k.attrs, nt.Name) {
				w.k.attrs = append(w.k.attrs, nt.Name)
			}
		case xpath.AxisChild:
			if !hasName(w.k.kids, nt.Name) {
				w.k.kids = append(w.k.kids, nt.Name)
			}
		default:
			w.ok = false
			return
		}
	}
	for _, s := range steps[i:] {
		if st, ok := s.(*xpath.Step); ok {
			for _, p := range st.Predicates {
				w.expr(p, false)
			}
		} else {
			w.expr(s, false)
		}
	}
}

// exprKids returns the expressions directly inside e, as exprWalk reaches
// them (streamfork.go).
func exprKids(e xpath.Expr) []xpath.Expr {
	const pkg = "github.com/knroy/go-xml/v2/xpath"
	var out []xpath.Expr
	var walk func(v reflect.Value, top bool)
	walk = func(v reflect.Value, top bool) {
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return
			}
			if x, ok := v.Interface().(xpath.Expr); ok && !top {
				out = append(out, x)
				return
			}
			if v.Kind() == reflect.Interface || v.Type().Elem().PkgPath() == pkg {
				walk(v.Elem(), false)
			}
		case reflect.Struct:
			if v.Type().PkgPath() != pkg {
				return
			}
			for i := range v.NumField() {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i), false)
				}
			}
		case reflect.Slice:
			for i := range v.Len() {
				walk(v.Index(i), false)
			}
		}
	}
	walk(reflect.ValueOf(e), true)
	return out
}
