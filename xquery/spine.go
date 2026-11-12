package xquery

import (
	"reflect"

	"github.com/knroy/go-xml/v2/xpath"
)

// spineDepth is how many levels of an expression's tree count as one level of
// recursion depth, on top of the levels its calls cost. It is xpath's figure
// for an inline function body, applied to every expression this package
// evaluates.
//
// xpath bounds one expression's operator chain at 10,000 terms and MaxDepth
// bounds call nesting, but evaluation descends a chain by recursion, so a
// declared function whose recursive call sits at the bottom of a 10,000-term
// chain holds 10,000 operator frames per level: 500 levels of that overflow
// the Go stack, which is fatal. Charging height/spineDepth extra levels for
// each expression while it runs composes the two bounds, so the frames held at
// once are at most about MaxDepth * spineDepth whatever the chains, and an
// expression below spineDepth -- every real one; the longest chain measured
// in any corpus is 190 -- costs nothing extra.
const spineDepth = 200

// xpathPkg is the package exprHeight walks the types of.
var xpathPkg = reflect.TypeFor[xpath.Compiled]().PkgPath()

// exprHeight is the height of e's tree, counted in nested xpath nodes, which
// is what evaluating it recurses through.
//
// The tree's node types are xpath's, many of them unexported, so it is walked
// by reflection: through pointers, interfaces, structs, slices and arrays of
// xpath's own types only, which keeps a cached resolution or a literal's
// value out of the walk. The walk keeps its own stack, since the height is
// what it measures, and visits each pointer once.
func exprHeight(e xpath.Expr) int {
	type item struct {
		v reflect.Value
		d int
	}
	stack := []item{{reflect.ValueOf(e), 0}}
	seen := map[uintptr]bool{}
	h := 0
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		v := it.v
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				stack = append(stack, item{v.Elem(), it.d})
			}
		case reflect.Pointer:
			if v.IsNil() || !ownType(v.Type()) || seen[v.Pointer()] {
				continue
			}
			seen[v.Pointer()] = true
			h = max(h, it.d+1)
			stack = append(stack, item{v.Elem(), it.d + 1})
		case reflect.Struct:
			if !ownType(v.Type()) {
				continue
			}
			for i := range v.NumField() {
				if ownType(v.Field(i).Type()) {
					stack = append(stack, item{v.Field(i), it.d})
				}
			}
		case reflect.Slice, reflect.Array:
			for i := range v.Len() {
				stack = append(stack, item{v.Index(i), it.d})
			}
		}
	}
	return h
}

// ownType reports whether t is, or holds, one of xpath's own types: a value
// exprHeight descends into.
func ownType(t reflect.Type) bool {
	for {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			t = t.Elem()
			continue
		case reflect.Interface, reflect.Struct:
			return t.PkgPath() == xpathPkg
		}
		return false
	}
}

// spineLevels is the extra recursion depth evaluating c costs; see spineDepth.
func spineLevels(c *xpath.Compiled) int32 {
	return int32(exprHeight(c.Expr()) / spineDepth)
}
