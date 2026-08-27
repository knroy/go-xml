package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
)

// Abstract components and XTDE3052, XSLT 3.0 sections 3.5.2 and 3.5.3.2.
//
// A component whose visibility is abstract has no body: the package that
// declares it says only that it exists and what its signature is, and a
// package that uses it is expected to supply the implementation through
// xsl:override. A using package may also decline, either by accepting the
// component with visibility="hidden" or by not overriding it at all.
//
// Declining is legal. What is not legal is *invoking* what was declined, and
// the specification makes that a dynamic error rather than a static one:
//
//	[ERR XTDE3052] It is a dynamic error if an invocation of an abstract
//	component is evaluated.
//
// The note under it spells out the shape: "This can occur when a public
// component in the used package invokes an abstract component in the used
// package, and the using package provides no concrete implementation for the
// component in an xsl:override element."
//
// That timing is the whole point of the rule and the reason abstract
// components cannot simply be deleted from the tree. accept-045a and
// accept-045b run the SAME stylesheet, differing only in the value of a
// runtime parameter that decides whether the abstract template is reached:
// -045a must succeed and -045b must fail. A processor that removed the
// declaration would fail both statically, at compile time, with XTSE0650 for
// a template that no longer exists -- which is what this implementation did
// before, and why -045a passed only by accident while every case that
// actually invokes an abstract component reported the wrong code.
//
// So an abstract component is kept, and its body is replaced by a stub that
// raises XTDE3052 when it is evaluated. The signature stays, so a reference
// to it still binds and still type-checks; only reaching the body fails.

// abstractStub is the body given to an abstract component in place of the
// body it does not have.
//
// It carries the component's symbolic name so that the error names what was
// invoked rather than merely reporting that something abstract was reached:
// a package may leave several components abstract, and "an abstract component
// was invoked" would not say which.
type abstractStub struct {
	what string
}

// Execute raises XTDE3052. An abstract component's body is never reachable
// except by invoking it, so arriving here is exactly the condition the error
// describes.
func (a *abstractStub) Execute(rt *runtime, out *outputBuilder) error {
	return fmt.Errorf(
		"XTDE3052: %s is abstract and has no implementation, so it cannot "+
			"be invoked", a.what)
}

// abstractMarker is the attribute compileUsedPackage writes on a declaration
// whose body must become a stub.
//
// It is an attribute in a namespace no stylesheet can write, rather than a
// field threaded through the compiler, because the three declarations that
// need it -- xsl:template, xsl:function and xsl:attribute-set -- are compiled
// by three separate functions reached through compileTopLevel, and each
// already receives the element. Marking the element keeps the change to one
// line in each.
const abstractMarkerNS = "http://go-xml.invalid/xslt/abstract"

// markAbstract records that a declaration is an abstract component, so that
// the compiler gives it a stub body.
func markAbstract(el *xdm.Node, what string) {
	el.Attrs = append(el.Attrs, &xdm.Node{
		Kind:   xdm.KindAttribute,
		Name:   xdm.QName{URI: abstractMarkerNS, Local: "abstract"},
		Value:  what,
		Parent: el,
	})
}

// abstractStubFor answers the stub body a declaration needs, or nil where the
// declaration is not an abstract component.
func abstractStubFor(el *xdm.Node) []Instruction {
	for _, a := range el.Attrs {
		if a.Name.URI == abstractMarkerNS && a.Name.Local == "abstract" {
			return []Instruction{&abstractStub{what: a.Value}}
		}
	}
	return nil
}

// abstractNameOf answers the symbolic name recorded on an abstract
// declaration, or "" where the declaration is not abstract.
func abstractNameOf(el *xdm.Node) string {
	for _, a := range el.Attrs {
		if a.Name.URI == abstractMarkerNS && a.Name.Local == "abstract" {
			return a.Value
		}
	}
	return ""
}
