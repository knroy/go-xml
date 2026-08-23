package xpath

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// fnDocument implements the XSLT 2.0 fn:document.
//
// It is not fn:doc with a plural argument, and the differences all matter:
//
//   - the first argument is a *sequence*, and each item is either a URI string
//     or a node whose string value is one, so document(//@href) is the ordinary
//     way to load every document a document points at;
//   - a second argument supplies the base URI, taken from that node's base
//     rather than from the expression's static base — which is how a stylesheet
//     resolves references relative to the data instead of relative to itself;
//   - the result is a node *set*: duplicates by absolute URI collapse, and what
//     comes back is in document order.
//
// The deduplication is required rather than an optimisation. Section 16.1 says
// two calls naming the same absolute URI return the *same* node, so a stylesheet
// comparing identity, or counting the result of document((a,b)) where a and b
// name one file, depends on it.
func fnDocument(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
	// The base for relative references. With a second argument it is that
	// node's base URI; without one it is the context item's, falling back to
	// the expression's static base as fn:doc does.
	base := ""
	if len(args) > 1 {
		if len(args[1]) == 0 {
			// An empty second argument leaves nothing to resolve against.
			// Section 16.1 makes this an error rather than a silent fallback.
			return nil, fmt.Errorf(
				"XTDE1162: the second argument of document() is an empty sequence")
		}
		n, err := singleNodeArg(args, 1)
		if err != nil {
			return nil, err
		}
		base = n.BaseURI
	} else {
		if n, ok := ctx.Item.(*xdm.Node); ok {
			base = n.BaseURI
		}
	}
	if base == "" {
		base = ctx.StaticBaseURI
	}

	if len(args) == 0 || len(args[0]) == 0 {
		return xdm.Empty, nil
	}

	// Each item contributes a URI: a node by its string value, an atomic by
	// its lexical form. A node also carries its own base URI, which overrides
	// the one computed above — document(@href) resolves against the element
	// the attribute is on, wherever that element came from.
	type request struct {
		uri, base string
	}
	var reqs []request
	for _, it := range args[0] {
		switch v := it.(type) {
		case *xdm.Node:
			b := v.BaseURI
			if b == "" {
				b = base
			}
			reqs = append(reqs, request{v.StringValue(), b})
		default:
			for _, a := range xdm.Atomize(xdm.Sequence{it}) {
				reqs = append(reqs, request{a.(*xdm.Atomic).String(), base})
			}
		}
	}

	if ctx.Docs == nil {
		return nil, fmt.Errorf(
			"FODC0002: document access is disabled (no resolver configured)")
	}

	seen := map[*xdm.Node]bool{}
	var out xdm.Sequence
	for _, r := range reqs {
		uri := strings.TrimSpace(r.uri)
		// document("") is the stylesheet itself, which this engine does not
		// keep a tree for once compiled. Reporting it is better than resolving
		// the empty string against the base and loading something unrelated.
		if uri == "" && r.base == "" {
			return nil, fmt.Errorf("FODC0005: document() was given an empty URI")
		}
		if err := validAnyURI(uri); err != nil || strings.HasPrefix(uri, ":") {
			return nil, fmt.Errorf("FODC0005: %q is not a valid URI", uri)
		}
		tree, err := ctx.Docs.ResolveDocument(uri, r.base)
		if err != nil {
			return nil, fmt.Errorf("FODC0002: cannot retrieve %q: %w", uri, err)
		}
		if tree == nil || tree.Root == nil {
			continue
		}
		// Identity, not URI string, is what deduplicates: a resolver that
		// caches returns the same tree for two spellings of one absolute URI,
		// and that is the case the specification is about.
		if seen[tree.Root] {
			continue
		}
		seen[tree.Root] = true
		out = append(out, tree.Root)
	}

	return out, nil
}
