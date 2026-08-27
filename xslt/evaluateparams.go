package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// bindWithParams binds the variables the @with-params map supplies into rt.
//
// Section 10.4.2 lets xsl:evaluate name its parameters dynamically as well as
// statically: @with-params is an expression of type map(xs:QName, item()*),
// and where a name appears both there and on an xsl:with-param child the map
// wins. The caller therefore binds the children first and this last, so the
// map's binding simply shadows the child's.
func (i *evaluateInstr) bindWithParams(rt, sub *runtime) (*runtime, error) {
	if i.withParams == nil {
		return sub, nil
	}
	seq, err := i.withParams.Eval(rt.ctx)
	if err != nil {
		return nil, fmt.Errorf("in xsl:evaluate/@with-params: %w", err)
	}
	if len(seq) != 1 {
		return nil, fmt.Errorf("XTTE3165: the with-params attribute of "+
			"xsl:evaluate selected %d items, not one map", len(seq))
	}
	m, ok := seq[0].(*xdm.MapItem)
	if !ok {
		return nil, fmt.Errorf("XTTE3165: the with-params attribute of " +
			"xsl:evaluate is not a map")
	}
	err = m.Entries(func(key *xdm.Atomic, val xdm.Sequence) error {
		qn := key.QName()
		if qn == nil {
			// The declared key type is xs:QName, and a key of any other type
			// leaves the parameter unnameable rather than merely unused.
			return fmt.Errorf("XTTE3165: a key of the with-params map of "+
				"xsl:evaluate is %s, not xs:QName", key.TypeName())
		}
		sub = sub.withVar(*qn, val)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sub, nil
}

// resolverFor returns the static context the target expression compiles in.
//
// 10.4.1: with no @namespace-context that is the context of the xsl:evaluate
// element itself. With one, the attribute's value is a single node whose
// in-scope namespaces replace the statically known namespaces, and whose
// binding for the default namespace becomes the default namespace for
// elements and types — which is the one case where a default namespace
// reaches the target expression at all.
func (i *evaluateInstr) resolverFor(rt *runtime) (*nsResolver, error) {
	// 10.4.1: the in-scope schema definitions are the ones xsl:import-schema
	// brought in only when @schema-aware is yes; otherwise the target
	// expression sees the built-in types alone, so a name from an imported
	// schema must fail to resolve there.
	schema := i.ns.schema
	if i.schemaAware != nil {
		v, err := i.schemaAware.eval(rt)
		if err != nil {
			return nil, err
		}
		if !stylesheetYes(v) {
			schema = nil
		}
	} else {
		schema = nil
	}
	if i.nsContext == nil {
		if schema == i.ns.schema {
			return i.ns, nil
		}
		sub := *i.ns
		sub.schema = schema
		return &sub, nil
	}
	seq, err := i.nsContext.Eval(rt.ctx)
	if err != nil {
		return nil, err
	}
	if len(seq) != 1 {
		return nil, fmt.Errorf("XTTE3170: the namespace-context attribute of "+
			"xsl:evaluate selected %d items, not one node", len(seq))
	}
	node, ok := seq[0].(*xdm.Node)
	if !ok {
		return nil, fmt.Errorf("XTTE3170: the namespace-context attribute of " +
			"xsl:evaluate did not select a node")
	}
	bindings := node.InScopeNamespaces()
	sub := *i.ns
	sub.schema = schema
	sub.bindings = bindings
	// InScopeNamespaces keys the default declaration by the empty prefix, and
	// the resolver keeps it in its own field instead.
	sub.defaultNS = bindings[""]
	return &sub, nil
}
