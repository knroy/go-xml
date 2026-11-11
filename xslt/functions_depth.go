package xslt

// The recursion depth a call on a stylesheet function is charged (round
// SM-b), as xpath charges an inline function's (InlineFunctionExpr.Eval).
//
// A call costs one level of the XPath call depth (FuncCall.Eval), but the
// body is evaluated by recursion down its expressions' trees: a recursive
// call at the bottom of a 10,000-term operator chain holds 10,000 frames per
// level, and well inside MaxDepth that overflowed the Go stack, which is
// fatal. Charging height/bodySpineDepth extra levels per call composes the
// two bounds, so the frames held at once stay about MaxDepth*bodySpineDepth
// whatever the chains; a body below bodySpineDepth costs nothing extra.

import (
	"reflect"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// bodySpineDepth is xpath's spineDepth: how many levels of an expression
// tree count as one level of call depth.
const bodySpineDepth = 200

// charged is f.call charged the extra depth of el's body, el the
// xsl:function; f.call itself when there is none.
func (f *userFunction) charged(el *xdm.Node) func(*xpath.Context, []xdm.Sequence) (xdm.Sequence, error) {
	extra := int32(bodyHeight(el) / bodySpineDepth)
	if extra == 0 {
		return f.call
	}
	return func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
		c := *ctx
		c.Depth += extra - 1
		d, err := c.Descend() // the limit's own test and XPDY0001
		if err != nil {
			return nil, err
		}
		return f.call(d, args)
	}
}

// bodyHeight is the height of the tallest expression tree written in el. A
// tree's height is at most its source's length, so only an expression long
// enough to reach bodySpineDepth is parsed: nothing extra for the usual one.
func bodyHeight(el *xdm.Node) int {
	h := 0
	walkElements(el, func(n *xdm.Node) bool {
		for a := range n.Attrs() {
			v := a.Value()
			if len(v) < bodySpineDepth {
				continue
			}
			srcs := []string{v}
			if n.Name().URI != xdm.NSXSL || a.Name().URI != "" || !exprAttrs[a.Name().Local] {
				if !strings.ContainsAny(v, "{}") {
					continue
				}
				srcs, _ = avtExpressions(v)
			}
			for _, src := range srcs {
				if e := parseAt(n, src); e != nil {
					h = max(h, exprHeight(reflect.ValueOf(&e).Elem()))
				}
			}
		}
		for ch := range n.Children() {
			if ch.Kind() == xdm.KindText && len(ch.Value()) >= bodySpineDepth && strings.ContainsAny(ch.Value(), "{}") {
				if srcs, ok := avtExpressions(ch.Value()); ok { // a text value template
					for _, src := range srcs {
						if e := parseAt(n, src); e != nil {
							h = max(h, exprHeight(reflect.ValueOf(&e).Elem()))
						}
					}
				}
			}
		}
		return true
	})
	return h
}

// exprHeight is the number of expression nodes on the longest path down v,
// walked as exprWalk walks it (streamfork.go).
func exprHeight(v reflect.Value) int {
	const pkg = "github.com/knroy/go-xml/v2/xpath"
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return 0
		}
		return 1 + exprHeight(v.Elem())
	case reflect.Pointer:
		if !v.IsNil() && v.Type().Elem().PkgPath() == pkg {
			return exprHeight(v.Elem())
		}
	case reflect.Struct:
		if v.Type().PkgPath() != pkg {
			return 0
		}
		h := 0
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				h = max(h, exprHeight(v.Field(i)))
			}
		}
		return h
	case reflect.Slice:
		h := 0
		for i := range v.Len() {
			h = max(h, exprHeight(v.Index(i)))
		}
		return h
	}
	return 0
}
