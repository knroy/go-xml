package xquery

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// BoundarySpace is the boundary-space policy of §4.3: whether whitespace that
// only separates markup survives into the constructed element.
type BoundarySpace int

const (
	// StripSpace discards boundary whitespace. It is the default, and the
	// value a query gets when it says nothing.
	StripSpace BoundarySpace = iota
	// PreserveSpace keeps it, as "declare boundary-space preserve" asks.
	PreserveSpace
)

// Construction is the construction mode of §4.10: whether a copied node keeps
// the type annotation it was validated with.
type Construction int

const (
	// PreserveTypes keeps annotations. It is the default.
	PreserveTypes Construction = iota
	// StripTypes replaces them with xs:untyped and xs:untypedAtomic.
	StripTypes
)

// staticContext is the part of XQuery's static context this package needs in
// order to parse.
//
// The specification's static context is larger than this — it also holds the
// in-scope schema definitions, the collations, the default collation and much
// else that only matters once a query runs. What is here is what parsing
// consumes, because namespaces in XQuery are resolved while parsing rather
// than after it, and because boundary-space policy decides what text a
// constructor even produces.
//
// It is copied on entry to an element constructor, so that namespace
// declarations on that element go out of scope with it. The copy is shallow
// and the map is cloned, which is the whole of the scoping discipline.
type staticContext struct {
	// ns maps a prefix to the URI bound to it. The XML prefix is bound from
	// the start, as §4.1 requires, and cannot be rebound.
	ns map[string]string

	// defaultElementNS is applied to an unprefixed element name, in a
	// constructor and in a name test alike. It is not applied to attribute
	// names, which are in no namespace unless prefixed.
	defaultElementNS string

	// defaultFunctionNS is applied to an unprefixed function name.
	defaultFunctionNS string

	// boundarySpace decides whether whitespace-only content between markup
	// survives. See stripBoundary.
	boundarySpace BoundarySpace

	// construction decides whether a copied node keeps its type annotation.
	construction Construction

	// baseURI is the static base URI, used to resolve relative references
	// and stamped on constructed elements.
	baseURI string
}

// newStaticContext returns the context a query starts with, before its prolog
// has said anything.
//
// The defaults are the specification's: boundary whitespace is stripped,
// construction preserves types, unprefixed elements are in no namespace, and
// unprefixed functions are in the fn: namespace. The five predeclared prefixes
// of §4.1 are bound.
func newStaticContext() *staticContext {
	return &staticContext{
		ns: map[string]string{
			"xml":   xdm.NSXML,
			"xs":    xdm.NSXS,
			"xsi":   xdm.NSXSI,
			"fn":    xdm.NSFN,
			"local": nsLocal,
		},
		defaultFunctionNS: xdm.NSFN,
		boundarySpace:     StripSpace,
		construction:      PreserveTypes,
	}
}

// nsLocal is the namespace of functions declared in a main module's prolog
// without a prefix of their own.
const nsLocal = "http://www.w3.org/2005/xquery-local-functions"

// child returns a copy that new namespace bindings can be added to without
// disturbing the parent.
//
// Element constructors nest, and a namespace declared on an inner element is
// out of scope on the way back out. Copying on the way in is cheaper than
// unwinding on the way out and cannot be got wrong by an early return.
func (sc *staticContext) child() *staticContext {
	c := *sc
	c.ns = make(map[string]string, len(sc.ns))
	for k, v := range sc.ns {
		c.ns[k] = v
	}
	return &c
}

// ResolvePrefix implements xpath.NamespaceResolver.
func (sc *staticContext) ResolvePrefix(prefix string) (string, bool) {
	uri, ok := sc.ns[prefix]
	return uri, ok
}

// DefaultElementNamespace implements xpath.NamespaceResolver.
func (sc *staticContext) DefaultElementNamespace() string {
	return sc.defaultElementNS
}

// DefaultFunctionNamespace implements xpath.NamespaceResolver.
func (sc *staticContext) DefaultFunctionNamespace() string {
	return sc.defaultFunctionNS
}

// bind adds a namespace declaration, applying the prohibitions of §3.9.1.2.
//
// The XML prefix and namespace are reserved in both directions: xml may only
// be bound to its own namespace, and no other prefix may be bound to it. The
// xmlns prefix may not be bound at all, and neither may the namespace that
// xmlns attributes are themselves in. Each is XQST0070, which is a static
// error because all of it is decidable from the text.
func (sc *staticContext) bind(prefix, uri string) error {
	switch {
	case prefix == "xmlns":
		return fmt.Errorf("XQST0070: the prefix %q cannot be declared", "xmlns")
	case uri == xdm.NSXMLNS:
		return fmt.Errorf("XQST0070: no prefix may be bound to %q", xdm.NSXMLNS)
	case prefix == "xml" && uri != xdm.NSXML:
		return fmt.Errorf("XQST0070: the prefix %q may only be bound to %q",
			"xml", xdm.NSXML)
	case prefix != "xml" && uri == xdm.NSXML:
		return fmt.Errorf("XQST0070: only the prefix %q may be bound to %q",
			"xml", xdm.NSXML)
	}
	sc.ns[prefix] = uri
	return nil
}

// resolveElementName resolves a QName written in element position.
//
// An unprefixed name takes the default element namespace, which is what makes
// "declare default element namespace" work and what distinguishes element
// names from attribute names. An unbound prefix is XPST0081.
func (sc *staticContext) resolveElementName(prefix, local string) (xdm.QName, error) {
	if prefix == "" {
		return xdm.QName{URI: sc.defaultElementNS, Local: local}, nil
	}
	uri, ok := sc.ns[prefix]
	if !ok {
		return xdm.QName{}, fmt.Errorf(
			"XPST0081: the prefix %q is not bound to a namespace", prefix)
	}
	return xdm.QName{Prefix: prefix, URI: uri, Local: local}, nil
}

// resolveAttributeName resolves a QName written in attribute position.
//
// An unprefixed attribute is in no namespace whatever the default element
// namespace says — §3.9.1.2 is explicit that the default does not apply here,
// and applying it would put every unprefixed attribute of a namespaced element
// into that namespace, which is not what the markup means.
func (sc *staticContext) resolveAttributeName(prefix, local string) (xdm.QName, error) {
	if prefix == "" {
		return xdm.QName{Local: local}, nil
	}
	uri, ok := sc.ns[prefix]
	if !ok {
		return xdm.QName{}, fmt.Errorf(
			"XPST0081: the prefix %q is not bound to a namespace", prefix)
	}
	return xdm.QName{Prefix: prefix, URI: uri, Local: local}, nil
}
