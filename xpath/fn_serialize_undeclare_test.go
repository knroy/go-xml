package xpath

import (
	"sort"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// undeclaringTree builds a parent that binds the prefix p and a child that
// undeclares it, which is the only shape the undeclare-prefixes parameter has
// anything to say about. It is built rather than parsed because an XML 1.0
// parser cannot read the xmlns:p="" that produces it.
func undeclaringTree() *xdm.Node {
	root := &xdm.Node{
		Kind: xdm.KindElement,
		Name: xdm.QName{Local: "root"},
	}
	root.Namespaces = []*xdm.Node{{
		Kind:   xdm.KindNamespace,
		Name:   xdm.QName{Local: "p"},
		Value:  "http://example.com/p",
		Parent: root,
	}}
	child := &xdm.Node{
		Kind:   xdm.KindElement,
		Name:   xdm.QName{Local: "child"},
		Parent: root,
	}
	child.Namespaces = []*xdm.Node{{
		Kind:   xdm.KindNamespace,
		Name:   xdm.QName{Local: "p"},
		Value:  "",
		Parent: child,
	}}
	root.Children = []*xdm.Node{child}
	return root
}

// paramsElement builds an output:serialization-parameters element holding the
// named parameters. XPath has no element constructors -- those are XQuery's --
// so the element form of the second argument has to be built and bound to a
// variable.
func paramsElement(params map[string]string) *xdm.Node {
	const ns = "http://www.w3.org/2010/xslt-xquery-serialization"
	root := &xdm.Node{
		Kind: xdm.KindElement,
		Name: xdm.QName{Prefix: "output", URI: ns, Local: "serialization-parameters"},
	}
	// Sorted so the element is the same one on every run; Go randomises map
	// iteration, and a parameter document is compared by what it holds.
	names := make([]string, 0, len(params))
	for k := range params {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		c := &xdm.Node{
			Kind:   xdm.KindElement,
			Name:   xdm.QName{Prefix: "output", URI: ns, Local: name},
			Parent: root,
		}
		c.Attrs = []*xdm.Node{{
			Kind:   xdm.KindAttribute,
			Name:   xdm.QName{Local: "value"},
			Value:  params[name],
			Parent: c,
		}}
		root.Children = append(root.Children, c)
	}
	return root
}

// TestSerializeUndeclarePrefixes pins fn:serialize to the undeclare-prefixes
// parameter.
//
// xslt/serialize.go honours it -- XSLT 3.0 §11.7, "Namespace undeclarations
// are generated automatically by the serializer if undeclare-prefixes='yes'
// is specified" -- while this serializer, its twin, had the parameter on its
// accept-and-ignore list and wrote xmlns:p="" unconditionally. The same
// stylesheet therefore got one answer from xsl:result-document and another
// from fn:serialize.
func TestSerializeUndeclarePrefixes(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr string
		want bool
	}{
		{"asked for, with 1.1",
			`serialize($n, map{'method':'xml','version':'1.1','undeclare-prefixes':true()})`,
			true},
		{"not asked for",
			`serialize($n, map{'method':'xml','version':'1.1'})`,
			false},
		{"asked for and declined",
			`serialize($n, map{'method':'xml','version':'1.1','undeclare-prefixes':false()})`,
			false},
		{"element form, asked for", `serialize($n, $yes)`, true},
		{"element form, declined", `serialize($n, $no)`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewContext(nil, Builtins())
			ctx.Version, ctx.LibraryVersion = XPath31, XPath31
			c := ctx.WithVar(xdm.QName{Local: "n"}, xdm.One(undeclaringTree()))
			c = c.WithVar(xdm.QName{Local: "yes"}, xdm.One(paramsElement(
				map[string]string{"version": "1.1", "undeclare-prefixes": "yes"})))
			c = c.WithVar(xdm.QName{Local: "no"}, xdm.One(paramsElement(
				map[string]string{"version": "1.1", "undeclare-prefixes": "no"})))
			seq, err := Eval(tc.expr, c, nil)
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			got := seq[0].(*xdm.Atomic).String()
			if has := strings.Contains(got, `xmlns:p=""`); has != tc.want {
				t.Errorf("%s = %q, xmlns:p=\"\" present = %v, want %v",
					tc.expr, got, has, tc.want)
			}
		})
	}
}

// TestSerializeUndeclarePrefixesNeedsXML11 pins SEPM0010.
//
// The xmlns:p="" syntax exists only in XML 1.1, so asking for undeclarations
// over 1.0 output is a request the output cannot express. xslt/serialize.go
// raises the same code for the same combination.
func TestSerializeUndeclarePrefixesNeedsXML11(t *testing.T) {
	for _, expr := range []string{
		`serialize($n, map{'method':'xml','undeclare-prefixes':true()})`,
		`serialize($n, map{'method':'xml','version':'1.0','undeclare-prefixes':true()})`,
		`serialize($n, $p)`,
	} {
		ctx := NewContext(nil, Builtins())
		ctx.Version, ctx.LibraryVersion = XPath31, XPath31
		c := ctx.WithVar(xdm.QName{Local: "n"}, xdm.One(undeclaringTree()))
		c = c.WithVar(xdm.QName{Local: "p"}, xdm.One(paramsElement(
			map[string]string{"undeclare-prefixes": "yes"})))
		_, err := Eval(expr, c, nil)
		if err == nil {
			t.Errorf("%s: want SEPM0010, got no error", expr)
			continue
		}
		if !strings.Contains(err.Error(), "SEPM0010") {
			t.Errorf("%s: want SEPM0010, got %v", expr, err)
		}
	}
}

// TestSerializeUndeclarePrefixesOnlyConstrainsXML pins the method gate.
//
// text, html and json write no namespace declaration at all, so
// undeclare-prefixes asks nothing of them and cannot be a request they fail to
// express. xslt/serialize.go returns before the SEPM0010 check for exactly
// those methods; raising it here would reject a stylesheet the XSLT serializer
// accepts.
func TestSerializeUndeclarePrefixesOnlyConstrainsXML(t *testing.T) {
	for _, method := range []string{"text", "html", "json"} {
		ctx := NewContext(nil, Builtins())
		ctx.Version, ctx.LibraryVersion = XPath31, XPath31
		expr := `serialize($n, map{'method':'` + method +
			`','undeclare-prefixes':true()})`
		arg := xdm.One(undeclaringTree())
		if method == "json" {
			arg = xdm.One(xdm.NewString("x"))
		}
		if _, err := Eval(expr,
			ctx.WithVar(xdm.QName{Local: "n"}, arg), nil); err != nil {
			t.Errorf("%s: want no error, got %v", expr, err)
		}
	}
}
