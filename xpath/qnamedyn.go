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
		return xdm.One(a), nil
	}
	switch a.Type {
	case xdm.TypeString, xdm.TypeUntypedAtomic, xdm.TypeAnyURI:
	default:
		return nil, xdm.ErrType(
			"xs:QName() takes a string, got %s", a.Type)
	}
	q, err := resolveLexicalQName(strings.TrimSpace(a.String()), e.ns)
	if err != nil {
		return nil, err
	}
	return xdm.One(xdm.NewQNameValue(q)), nil
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
