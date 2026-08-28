package xslt

import (
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// The static context a static expression is evaluated in, section 9.7.
//
// It is severely restricted, and deliberately so: the expression is evaluated
// "without any dependency on information contained in the stylesheet itself or
// in any source document". There is no context item, there is no schema, the
// only variables are the static ones declared above the expression in
// stylesheet tree order, and stylesheet functions are explicitly excluded.
// Only the core function library plus element-available, function-available,
// type-available and system-property are in scope.
//
// The traversal that applies this context — conditional element inclusion,
// shadow attributes and static variable declarations, which all share it — is
// in static.go.

// useWhenFuncs is the function library a use-when expression sees.
//
// It is the core library plus the four functions section 3.12 names, and
// nothing else — in particular no stylesheet functions, which is a rule with
// an observable consequence rather than an implementation detail:
// function-available() is required to return false for them.
// bindings are the namespace declarations in scope on the element carrying the
// use-when, which function-available and type-available need in order to
// expand the prefix of the lexical QName they are handed. Passing nil left
// every prefix unbound, so a name like exsl:node-set could not be resolved at
// all — and once an unbound prefix became XTDE1400 rather than false, that
// gap turned four passing tests into errors.
func useWhenFuncs(bindings map[string]string) *xpath.Library {
	l := xpath.NewLibrary(xpath.Builtins())
	xpath.RegisterXSLTFuncs(l)
	resolveIn := func(name string) (uri, local string, ok bool) {
		prefix, local := xdm.SplitQName(name)
		if u, found := bindings[prefix]; found {
			return u, local, true
		}
		return "", local, false
	}
	fnRes := func(name string) (uri, local string, ok bool) {
		if prefix, local := xdm.SplitQName(name); prefix == "" {
			// An unprefixed function name is in the default function
			// namespace, never in a default element namespace binding.
			return xdm.NSFN, local, true
		}
		return resolveIn(name)
	}
	typeRes := func(name string) (uri, local string, ok bool) {
		if prefix, local := xdm.SplitQName(name); prefix == "" {
			return "", local, true
		}
		return resolveIn(name)
	}
	// fn:element-available expands an unprefixed name with the default
	// namespace (5.1), and a name in no namespace is simply false rather
	// than unresolvable.
	elemRes := func(name string) (uri, local string, ok bool) {
		if u, l, found := resolveIn(name); found {
			return u, l, true
		}
		if prefix, local := xdm.SplitQName(name); prefix == "" {
			return "", local, true
		}
		_, local = xdm.SplitQName(name)
		return "", local, false
	}
	// No schema: 3.12 makes the in-scope type definitions of a use-when
	// those available "in the absence of any xsl:import-schema".
	registerStaticFuncs(l, fnRes, typeRes, elemRes, nil)
	return l
}

// xpathDefaultNamespace finds the [xsl:]xpath-default-namespace in force on el.
//
// The attribute is inherited: 5.2 makes it apply to "the element on which it
// appears and all its descendants", so an expression written on a template
// takes the value declared on xsl:stylesheet when the template declares none.
// Reading only el's own attributes made the two cases indistinguishable --
// use-when-0118 declares it on the module element and use-when-0125 on a
// sibling template, and both looked like "not declared" -- so the same answer
// came back for a name that is in the XSD namespace and one that is in no
// namespace at all.
func xpathDefaultNamespace(el *xdm.Node) string {
	for n := el; n != nil && n.Kind == xdm.KindElement; n = n.Parent {
		if a := n.Attr(xdm.NSXSL, "xpath-default-namespace"); a != nil {
			return a.Value
		}
		if n.Name.URI == xdm.NSXSL {
			if a := n.Attr("", "xpath-default-namespace"); a != nil {
				return a.Value
			}
		}
	}
	return ""
}
