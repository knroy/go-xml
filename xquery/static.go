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
	// xqVersion is the version of XQuery named by the module's version
	// declaration (§4.1), or defaultXQVersion when it declared none.
	//
	// It is here rather than on the parser because it outlives parsing. A
	// module's declared version decides the *static* answer to a handful of
	// questions -- an unprefixed option name, whether a cast's unknown target
	// type is XPST0051 or XQST0052 -- and also a *dynamic* one: whether a
	// circularity that runs through a function body is the static XQST0054 or
	// the dynamic XQDY0054, which cannot be settled until the query runs. The
	// static context is the one thing both the parser and the evaluator hold:
	// Query.sc and evalContext.sc are the same pointer the parser used, so
	// recording it here makes the version reachable from both without a second
	// channel that could disagree with the first.
	//
	// It is copied by child() along with everything else, and is never
	// rebound: a version declaration may appear only once, and only before the
	// prolog, so there is no scope in which it could change.
	xqVersion XQVersion

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

	// declBase is the URI of the resource the module was read from, and is
	// used for one thing only: resolving a relative "declare base-uri"
	// against it, as §4.5 requires.
	//
	// It is deliberately not baseURI. The two answer different questions --
	// "what does a prolog declaration resolve against" and "what does the
	// query run under" -- and conflating them is what made every earlier
	// attempt at K2-BaseURIProlog-4 a net loss: seeding baseURI so that a
	// relative declaration could be made absolute also stamped that value on
	// constructed elements and on fn:static-base-uri when the query declared
	// nothing, which base-URI-12/14/23/24 and K2-BaseURIFunc-30 all detect.
	// Kept apart, seeding this one changes nothing except the resolution a
	// declaration asks for.
	declBase string

	// defaultCollation is what a comparison uses when the call names none.
	// The empty string means the codepoint collation, which is the default
	// everywhere and what xpath uses for the zero value.
	defaultCollation string

	// ordering is the ordering mode of §4.6. It is recorded and not acted on:
	// unordered mode *permits* a processor to return a sequence in any order,
	// and document order is one of the orders it permits.
	ordering Ordering

	// emptyOrder decides where the empty sequence sorts in an "order by"
	// without its own "empty greatest|least". It is here rather than on the
	// FLWOR clause because the prolog sets a module-wide default.
	emptyOrder EmptyOrder

	// ctorNS records only those bindings that came from a namespace
	// declaration attribute on an enclosing direct element constructor.
	//
	// It is a strict subset of ns, and the distinction matters because the two
	// kinds of declaration reach the constructed tree differently. A prolog
	// "declare namespace foo" resolves names and stops there: Constr-inscope-13
	// declares foo, never uses it, and expects a bare <new/>. An xmlns:p
	// written on a constructor is a namespace declaration attribute, and
	// §3.9.1.3 puts its binding into the in-scope namespaces of the element
	// *and of every element constructed within it* -- so the inner <e/> of
	// K2-InScopePrefixesFunc-18 reports p even though nothing in it uses p,
	// and even before it is attached to anything.
	ctorNS map[string]string

	// preserveNS and inheritNS are the two independent halves of
	// copy-namespaces (§4.8), which xdmbuild's Policy models the same way.
	// Both default to the specification's preserve/inherit.
	preserveNS, inheritNS bool
}

// Ordering is the ordering mode of §4.6.
type Ordering int

const (
	// Ordered requires the document order a path expression would give. It
	// is the default.
	Ordered Ordering = iota
	// Unordered permits any order.
	Unordered
)

// EmptyOrder decides where the empty sequence sorts in an "order by" that
// does not say (§4.7).
type EmptyOrder int

const (
	// EmptyGreatest sorts the empty sequence last ascending. It is this
	// implementation's default, which the specification leaves open.
	EmptyGreatest EmptyOrder = iota
	// EmptyLeast sorts it first.
	EmptyLeast
)

// newStaticContext returns the context a query starts with, before its prolog
// has said anything.
//
// The defaults are the specification's: boundary whitespace is stripped,
// construction preserves types, unprefixed elements are in no namespace, and
// unprefixed functions are in the fn: namespace. The predeclared prefixes of
// §4.1 are bound.
//
// There are eight of them, not five. 3.1 added math, map and array along with
// the functions in those namespaces, and a query is entitled to write
// "math:pi()" or "array:size($a)" with no declaration of its own — which is
// how nearly every example in the function specification is written.
func newStaticContext() *staticContext {
	return &staticContext{
		// A module that has not yet been read has declared no version, and
		// §4.1 leaves such a module's version implementation-defined. This
		// engine implements 3.1. parseVersionDecl overwrites this when the
		// module does name a version.
		xqVersion: defaultXQVersion,
		ns: map[string]string{
			"xml":   xdm.NSXML,
			"xs":    xdm.NSXS,
			"xsi":   xdm.NSXSI,
			"fn":    xdm.NSFN,
			"local": nsLocal,
			"math":  xdm.NSMath,
			"map":   xdm.NSMap,
			"array": xdm.NSArray,
			// err is not in §4.1's list of predeclared prefixes, but §3.16
			// binds it: "The err prefix is bound to the namespace
			// http://www.w3.org/2005/xqt-errors in the static context of
			// every module", which is what makes "catch err:FODC0002" and
			// "$err:code" work without the query declaring anything. A
			// prolog may rebind it, which is why it sits in the map rather
			// than being special-cased at lookup.
			"err": xdm.NSErr,
		},
		defaultFunctionNS: xdm.NSFN,
		boundarySpace:     StripSpace,
		construction:      PreserveTypes,
		preserveNS:        true,
		inheritNS:         true,
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
	c.ctorNS = make(map[string]string, len(sc.ctorNS)+1)
	for k, v := range sc.ctorNS {
		c.ctorNS[k] = v
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
