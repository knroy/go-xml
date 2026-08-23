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
	def := ""
	if a := el.Attr(xdm.NSXSL, "xpath-default-namespace"); a != nil {
		def = a.Value
	} else if a := el.Attr("", "xpath-default-namespace"); a != nil &&
		el.Name.URI == xdm.NSXSL {
		def = a.Value
	}
	ns := &nsResolver{
		bindings:  el.InScopeNamespaces(),
		defaultNS: def,
		// No schema: the in-scope type definitions are "the type definitions
		// that would be available in the absence of any xsl:import-schema
		// declaration", so a use-when may not name an imported type even in a
		// module that imports one.
	}

	compiled, err := xpath.Compile(expr, ns)
	if err != nil {
		// An error in the use-when expression itself is reported: it is the
		// one error the exclusion rule does not suppress.
		return false, fmt.Errorf("in %s/@use-when: %w", el.Name.Lexical(), err)
	}

	// No context item, no variables, and the static function library only.
	// A use-when that calls a stylesheet function must fail rather than
	// resolve, which is what makes function-available() answer false for one.
	ctx := xpath.NewContext(nil, useWhenFuncs())
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
func useWhenFuncs() *xpath.Library {
	l := xpath.NewLibrary(xpath.Builtins())
	xpath.RegisterXSLTFuncs(l)
	registerStaticFuncs(l, nil)
	return l
}
