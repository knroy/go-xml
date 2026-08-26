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
	// The one-argument form takes the value that failed, and the zero- and
	// two-argument forms exist so that xs:error#0 and xs:error#2 are function
	// items a test can take a reference to.
	for _, arity := range []int{0, 1, 2} {
		l.Add(Function{
			Name:  xdm.QName{URI: xdm.NSXS, Local: "error"},
			Arity: arity,
			Since: XPath30,
			Call: func(_ *Context, _ []xdm.Sequence) (xdm.Sequence, error) {
				return nil, fmt.Errorf(
					"FORG0001: xs:error has no instances, so every call raises this error")
			},
		})
	}

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

	// fn:element-with-id is fn:id restricted to element nodes.
	//
	// In a document with no schema-declared ID attributes the two agree,
	// because the only IDs are xml:id and DTD-declared ones, which are always
	// on elements.
	l.registerFnSince(XPath30, "element-with-id", []int{1, 2}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		seq, err := lookupByID(ctx, args, true)
		if err != nil {
			return nil, err
		}
		out := make(xdm.Sequence, 0, len(seq))
		for _, it := range seq {
			if n, ok := it.(*xdm.Node); ok && n.Kind == xdm.KindElement {
				out = append(out, n)
			}
		}
		return out, nil
	})
}
