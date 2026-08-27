package xpath

import (
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// dynamicQName is xs:QName() applied to an argument that is not a string
// literal.
//
// XPath 2.0 restricts the constructor to a literal, because the prefix has to
// be resolved and only a literal is folded while the namespace declarations of
// the expression are still reachable. XPath 3.0 section 19.1 lifts that: the
// prefix is resolved against the statically known namespaces of the
// *expression*, whatever produced the string. The resolver is therefore
// captured here at parse time and consulted when the value arrives, which is
// what keeps the answer a static property of where the call was written.
//
// notation-0001..0004 cast a computed string to xs:NOTATION and then to
// xs:QName, which is exactly this case.
type dynamicQName struct {
	arg Expr
	ns  NamespaceResolver
	// lexType and derived are set when this stands for the constructor of an
	// imported type whose value space is the QName one -- a subtype of
	// xs:NOTATION or of xs:QName. The facets the schema author wrote still
	// apply, and the result has to carry the derived type's annotation so
	// that "instance of" against that very type answers true.
	lexType string
	derived string
}

func (e *dynamicQName) String() string { return "xs:QName(" + e.arg.String() + ")" }

func (e *dynamicQName) Eval(ctx *Context) (xdm.Sequence, error) {
	v, err := e.arg.Eval(ctx)
	if err != nil {
		return nil, err
	}
	atoms := xdm.Atomize(v)
	if len(atoms) == 0 {
		return xdm.Empty(), nil
	}
	if len(atoms) > 1 {
		return nil, xdm.ErrType("xs:QName() takes one item, got %d", len(atoms))
	}
	a, ok := atoms[0].(*xdm.Atomic)
	if !ok {
		return nil, xdm.ErrType("xs:QName() takes an atomic value")
	}
	// A value that is already a QName -- or an xs:NOTATION, whose value space
	// is the QName one -- carries its own namespace binding and needs nothing
	// from the static context.
	if a.Type == xdm.TypeQName {
		return xdm.One(e.annotate(a)), nil
	}
	switch a.Type {
	case xdm.TypeString, xdm.TypeUntypedAtomic, xdm.TypeAnyURI:
	default:
		return nil, xdm.ErrType(
			"xs:QName() takes a string, got %s", a.Type)
	}
	lex := strings.TrimSpace(a.String())
	q, err := resolveLexicalQName(lex, e.ns)
	if err != nil {
		return nil, err
	}
	// The schema's own facets -- the enumeration of notations, above all --
	// are checked against the expanded name rather than the lexical form, so
	// that two prefixes for one namespace agree and one prefix for two
	// namespaces does not.
	if e.lexType != "" {
		clark := "{" + q.URI + "}" + q.Local
		if known, verr := schemaValueValid(e.lexType, e.ns, clark); known && verr != nil {
			return nil, xdm.Errorf("FORG0001", "%v", verr)
		}
	}
	return xdm.One(e.annotate(xdm.NewQNameValue(q))), nil
}

// annotate stamps the derived type's annotation onto a constructed value, so
// that the result of foo:nota(...) is an instance of foo:nota.
func (e *dynamicQName) annotate(a *xdm.Atomic) *xdm.Atomic {
	if e.derived == "" {
		return a
	}
	return a.WithDerived(e.derived)
}

// resolveLexicalQName turns "prefix:local" into a QName using ns.
func resolveLexicalQName(lex string, ns NamespaceResolver) (xdm.QName, error) {
	prefix, local := "", lex
	if i := strings.Index(lex, ":"); i >= 0 {
		prefix, local = lex[:i], lex[i+1:]
	}
	if local == "" || !isNCName(local) ||
		(strings.Contains(lex, ":") && !isNCName(prefix)) {
		return xdm.QName{}, xdm.Errorf("FORG0001",
			"xs:QName(%q) is not a valid lexical QName", lex)
	}
	if prefix == "" {
		return xdm.QName{Local: local}, nil
	}
	uri, ok := resolvePrefixOf(ns, prefix)
	if !ok {
		return xdm.QName{}, xdm.Errorf("FONS0004",
			"xs:QName(%q): no namespace is bound to prefix %q", lex, prefix)
	}
	return xdm.QName{URI: uri, Local: local, Prefix: prefix}, nil
}

func resolvePrefixOf(ns NamespaceResolver, prefix string) (string, bool) {
	if ns == nil {
		return "", false
	}
	return ns.ResolvePrefix(prefix)
}
