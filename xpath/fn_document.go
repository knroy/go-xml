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
	// The base for relative references: with a second argument it is that
	// node's base URI, and without one it is the expression's static base
	// URI, which is the stylesheet module the call is written in.
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
		base = inheritedBaseURI(n)
		// XTDE1162 names this case exactly: "the second argument to the
		// function is a node that has no base URI". Falling back to the
		// static base URI would resolve the reference against the stylesheet,
		// which is the one thing supplying a $base-node says not to do.
		if base == "" {
			return nil, fmt.Errorf(
				"XTDE1162: the second argument of document() is a node with " +
					"no base URI")
		}
	}
	// Without a $base-node the relative URI resolves against "the base URI
	// from the static context (this will usually be the base URI of the
	// stylesheet module)", not against the context item.
	//
	// Using the context node's base URI resolved against the *source*
	// document instead, so document('x.xml') written in an included module
	// looked for x.xml beside the input rather than beside the module — and
	// an included module in a subdirectory could never reach its own files.
	if base == "" {
		base = ctx.StaticBaseURI
	}

	if len(args) == 0 || len(args[0]) == 0 {
		return xdm.Empty(), nil
	}

	// Each item contributes a URI: a node by its string value, an atomic by
	// its lexical form. A node also carries its own base URI, and section 16.1
	// says a relative reference taken from a node resolves "against the base
	// URI of $base-node if supplied, or against the base URI of the node that
	// contained it otherwise". The node's own base is therefore a *fallback*
	// for the two-argument form, not an override of it: an explicit
	// $base-node is the whole point of the second argument, and letting the
	// item node win meant document(filename, document('a/b/c.xml')) resolved
	// against the source document rather than against the loaded one.
	explicitBase := len(args) > 1
	type request struct {
		uri, base string
	}
	var reqs []request
	for _, it := range args[0] {
		switch v := it.(type) {
		case *xdm.Node:
			b := base
			// The base URI *in force at* the node, not the field: only an
			// element carrying xml:base has one of its own, so an attribute
			// (document(@href) is the ordinary spelling) and every element
			// the parser did not stamp read back empty and fell through to
			// the stylesheet's own directory.
			if !explicitBase {
				nb := inheritedBaseURI(v)
				// A relative reference held in a node resolves against that
				// node's base URI, and a parentless node has none -- the
				// example XTDE1162 gives. The static base URI is not a
				// fallback here: 16.1 makes the containing node's base the
				// one that applies, so its absence is the error rather than
				// a cue to resolve somewhere else.
				if nb == "" && isRelativeRef(v.StringValue()) {
					return nil, fmt.Errorf(
						"XTDE1162: document(%q) takes its relative reference "+
							"from a node with no base URI", v.StringValue())
				}
				if nb != "" {
					b = nb
				}
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
		// A fragment identifier that is not legal for an XML media type is
		// XTRE1160, and it is decidable from the URI alone -- so it is
		// reported before the resource is fetched rather than after, which
		// is what lets it be diagnosed for a URI this engine will not
		// retrieve at all.
		if !FragmentIsValidXMLName(uri) {
			return nil, fmt.Errorf(
				"XTRE1160: %q has a fragment identifier that is not valid "+
					"for an XML media type", uri)
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

// FragmentIsValidXMLName reports whether the fragment identifier in uri, if
// there is one, conforms to the rules for the XML media types.
//
// XSLT 2.0 16.1 makes it a recoverable dynamic error (XTRE1160) if "the
// fragment identifier does not conform to the rules for fragment identifiers
// for that media type". For text/xml and application/xml those rules (RFC
// 7303) admit a bare name, which must be an XML Name, or an XPointer -- and an
// XPointer scheme part is itself a QName. So a fragment of "123456789" is not
// a legal one for XML however the resource is fetched, which is what lets the
// error be raised without retrieving anything: error-1160a names a w3.org URL
// this engine will not fetch at all, and diagnosing the fragment is the only
// way to reach the right answer offline.
//
// It is exported because xslt/rtfuncs.go registers its own fn:document#1,
// which shadows the one here for arity 1 and needs the same rule.
//
// A uri with no fragment, or an empty one, is not an error here: only a
// present and malformed fragment is.
func FragmentIsValidXMLName(uri string) bool {
	i := strings.IndexByte(uri, '#')
	if i < 0 {
		return true
	}
	frag := uri[i+1:]
	if frag == "" {
		return true
	}
	// An XPointer -- "element(...)", "xpointer(...)", "scheme(...)" -- is
	// admitted without further checking: parsing one needs the document, and
	// the shorthand bare-name form is the only one this can judge on the URI
	// alone.
	if strings.ContainsAny(frag, "()") {
		return true
	}
	return xdm.IsNCName(frag)
}

// isRelativeRef reports whether a URI reference needs a base to resolve.
//
// Only a reference with a scheme stands on its own; XTDE1162 is about the
// rest. An empty reference is document(""), which names the containing
// stylesheet module and is handled separately.
func isRelativeRef(ref string) bool { return IsRelativeReference(ref) }

// IsRelativeReference is isRelativeRef for xslt, which registers its own
// fn:document#1 and needs the same judgement.
func IsRelativeReference(ref string) bool {
	if ref == "" {
		return false
	}
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		if c == ':' {
			return i == 0
		}
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			return true
		}
	}
	return true
}
