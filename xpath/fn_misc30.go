package xpath

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// registerMisc30Funcs adds the remaining XPath 3.0 additions that need no
// machinery of their own.
func registerMisc30Funcs(l *Library) {
	// xs:error is the constructor for the empty type. It has no instances, so
	// every call is FORG0001; it exists to be *called*, as a way to raise that
	// error from a place where only an expression is allowed.
	//
	// Exactly one arity, like every other atomic-type constructor: xs:error()
	// and xs:error((), ()) are XPST0017, and the suite asserts both.
	l.Add(Function{
		Name:  xdm.QName{URI: xdm.NSXS, Local: "error"},
		Arity: 1,
		Since: XPath30,
		Call: func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
			// Like every other constructor, an empty argument gives the empty
			// sequence rather than raising: "xs:error(())" is the empty
			// sequence, which is why it is castable as xs:error? and not as
			// xs:error. Only a non-empty argument reaches the error, since
			// the type has no instances to produce.
			if len(args) > 0 && len(args[0]) == 0 {
				return xdm.Empty(), nil
			}
			return nil, fmt.Errorf(
				"FORG0001: xs:error has no instances, so every call raises this error")
		},
	})

	// fn:environment-variable($name) as xs:string?
	//
	// The empty sequence when the variable is not set, which is also the
	// answer when the implementation declines to expose the environment at
	// all — the spec makes availability implementation-dependent, so a
	// missing variable and a withheld one are indistinguishable by design.
	l.registerFnSince(XPath30, "environment-variable", []int{1}, func(_ *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		name, err := argStringRequired(args, 0)
		if err != nil {
			return nil, err
		}
		v, ok := os.LookupEnv(name)
		if !ok {
			return xdm.Empty(), nil
		}
		return strSeq(v), nil
	})

	// fn:available-environment-variables() as xs:string*
	l.registerFnSince(XPath30, "available-environment-variables", []int{0}, func(_ *Context, _ []xdm.Sequence) (xdm.Sequence, error) {
		env := os.Environ()
		names := make([]string, 0, len(env))
		for _, kv := range env {
			if i := strings.IndexByte(kv, '='); i > 0 {
				names = append(names, kv[:i])
			}
		}
		// Sorted so the result is stable between runs; the spec fixes no
		// order, and an unstable one would make a test that compares two
		// calls flap.
		sort.Strings(names)
		out := make(xdm.Sequence, 0, len(names))
		for _, n := range names {
			out = append(out, xdm.NewString(n))
		}
		return out, nil
	})

	// fn:uri-collection([$uri]) as xs:anyURI*
	//
	// The URIs of the documents fn:collection would return, rather than the
	// documents themselves. It is answered from the same resolver, so a
	// caller that has not configured one gets the same refusal.
	l.registerFnSince(XPath30, "uri-collection", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		seq, err := fnCollection(ctx, args)
		if err != nil {
			return nil, err
		}
		out := make(xdm.Sequence, 0, len(seq))
		for _, it := range seq {
			n, ok := it.(*xdm.Node)
			if !ok {
				continue
			}
			out = append(out, xdm.NewAnyURI(n.BaseURI))
		}
		return out, nil
	})

	// fn:element-with-id is fn:id with the one difference the function was
	// introduced to make.
	//
	// F&O 14.7.2: "Whereas fn:id, for legacy reasons, returns the element that
	// has the is-id property, [...] it would be more appropriate to return its
	// PARENT, that being the element that is uniquely identified by the ID. A
	// new function element-with-id is being introduced with the desired
	// behavior."
	//
	// So the two agree exactly when the ID is carried by an ATTRIBUTE — the
	// element having the attribute is already the element the ID identifies —
	// and differ when a schema declares an ELEMENT to be of type xs:ID. There
	// the ID-bearing element is a child holding the identifier, and what the
	// identifier names is the thing it sits inside. match-054 validates
	// <row><id>C</id><value>GAMMA</value></row> with <id> declared xs:ID and
	// matches element-with-id('C'), which must select the <row>.
	//
	// A document with no schema-declared IDs cannot tell the difference: its
	// only IDs are xml:id and DTD-declared ones, and both are attributes.
	l.registerFnSince(XPath30, "element-with-id", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		seq, err := lookupByID(ctx, args, true)
		if err != nil {
			return nil, err
		}
		out := make(xdm.Sequence, 0, len(seq))
		for _, it := range seq {
			n, ok := it.(*xdm.Node)
			if !ok || n.Kind != xdm.KindElement {
				continue
			}
			// The element was returned for its OWN is-id property rather than
			// for an attribute's, so it is the identifier and its parent is
			// what the identifier names. lookupByID appends the element
			// itself in both cases, so the property is asked again here to
			// tell them apart.
			if n.IsID || isIDAnnotation(n.TypeAnnotation) {
				// A parentless ID element identifies nothing containing it.
				// Returning it instead would be fn:id's answer, which is the
				// one this function exists not to give.
				if p := n.Parent; p != nil && p.Kind == xdm.KindElement {
					out = append(out, p)
				}
				continue
			}
			out = append(out, n)
		}
		return out, nil
	})
}
