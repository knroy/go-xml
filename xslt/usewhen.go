package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// Conditional element inclusion, section 3.12.
//
// An element carrying use-when whose effective boolean value is false "together
// with all the nodes having that element as an ancestor, is effectively
// excluded from the stylesheet module", and the module "has the same effect as
// if the node were not there". That last clause is the operative one: no static
// or dynamic error may be reported for an excluded element, so exclusion has to
// happen before compilation looks at anything, not as a check inside it. A
// stylesheet may legitimately guard an xsl:import-schema, or an instruction
// this engine does not implement, behind a use-when that is false.
//
// The static context is severely restricted, and deliberately so: the
// expression is evaluated "without any dependency on information contained in
// the stylesheet itself or in any source document". There is no context item,
// there are no variables, and stylesheet functions are explicitly excluded.
// Only the core function library plus element-available, function-available,
// type-available and system-property are in scope.

// applyUseWhen removes the elements a false use-when excludes.
//
// It runs over the parsed stylesheet before compilation, and it is the whole
// implementation of section 3.12: everything downstream then sees a tree with
// the excluded elements simply absent.
func applyUseWhen(doc *xdm.Node) error {
	root := firstElement(doc)
	if root == nil {
		return nil
	}
	// xsl:stylesheet is treated specially: excluding it excludes its children
	// but not the element itself, so that one condition at the top of a module
	// can govern every declaration in it.
	if isXSL(root, "stylesheet") || isXSL(root, "transform") {
		keep, err := elementIncluded(root)
		if err != nil {
			return err
		}
		if !keep {
			root.Children = nil
			return nil
		}
	}
	return pruneChildren(root)
}

// pruneChildren removes excluded elements from n's children, recursively.
func pruneChildren(n *xdm.Node) error {
	var kept []*xdm.Node
	for _, c := range n.Children {
		if c.Kind != xdm.KindElement {
			kept = append(kept, c)
			continue
		}
		keep, err := elementIncluded(c)
		if err != nil {
			return err
		}
		if !keep {
			continue
		}
		if err := pruneChildren(c); err != nil {
			return err
		}
		kept = append(kept, c)
	}
	n.Children = kept
	return nil
}

// elementIncluded evaluates the use-when attribute on el, if it has one.
//
// The attribute is spelled "use-when" on an XSLT element and "xsl:use-when" on
// anything else, including a literal result element — the unprefixed name on a
// literal result element would be an ordinary attribute of the output.
func elementIncluded(el *xdm.Node) (bool, error) {
	var expr string
	if el.Name.URI == xdm.NSXSL {
		if a := el.Attr("", "use-when"); a != nil {
			expr = a.Value
		}
	}
	if expr == "" {
		if a := el.Attr(xdm.NSXSL, "use-when"); a != nil {
			expr = a.Value
		}
	}
	if expr == "" {
		return true, nil
	}

	// The in-scope namespaces are the containing element's, and the default
	// element namespace comes from xpath-default-namespace if present. Both
	// are properties of where the attribute is written, which is why the
	// resolver is built from el rather than from the module.
	def := xpathDefaultNamespace(el)
	ns := &nsResolver{
		bindings:  el.InScopeNamespaces(),
		defaultNS: def,
		// No schema: the in-scope type definitions are "the type definitions
		// that would be available in the absence of any xsl:import-schema
		// declaration", so a use-when may not name an imported type even in a
		// module that imports one.
	}

	// use-when is compiled in the version its own element declares, like
	// every other expression in the stylesheet. It is evaluated before
	// anything else -- exclusion has to happen before compilation can object
	// to what it excluded -- so it cannot go through compileExpr, and reads
	// the version directly.
	compiled, err := xpath.CompileVersion(expr, ns, xpathVersionAt(el))
	if err != nil {
		// An error in the use-when expression itself is reported: it is the
		// one error the exclusion rule does not suppress.
		return false, fmt.Errorf("in %s/@use-when: %w", el.Name.Lexical(), err)
	}

	// No context item, no variables, and the static function library only.
	// A use-when that calls a stylesheet function must fail rather than
	// resolve, which is what makes function-available() answer false for one.
	ctx := xpath.NewContext(nil, useWhenFuncs(ns.bindings))
	ctx.StaticBaseURI = el.BaseURI

	v, err := compiled.Eval(ctx)
	if err != nil {
		return false, fmt.Errorf("in %s/@use-when: %w", el.Name.Lexical(), err)
	}
	b, err := xpath.EffectiveBooleanValue(v)
	if err != nil {
		return false, fmt.Errorf("in %s/@use-when: %w", el.Name.Lexical(), err)
	}
	return b, nil
}

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
	// No schema: 3.12 makes the in-scope type definitions of a use-when
	// those available "in the absence of any xsl:import-schema".
	registerStaticFuncs(l, fnRes, typeRes, nil)
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
