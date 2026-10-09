package xdmbuild

import (
	"net/url"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
)

// DeepCopy clones a subtree, detached from its original parent.
//
// The type annotation travels with the copy. validation="preserve" is defined
// as keeping the types the source carried, and dropping them here left a
// preserved copy untyped, so "$v instance of element(e, xs:anyURI)" answered
// false for a node that had just been copied from a validated document.
// Stripping is done by the validation spec, which is the thing that knows
// whether the instruction asked for it.
func DeepCopy(n *xdm.Node) *xdm.Node { return xdm.Copy(n) }

// ResolveAgainst resolves a possibly-relative reference against a base URI,
// returning the reference unchanged when the base is unusable.
func ResolveAgainst(base, ref string) string {
	if ref == "" || base == "" {
		return ref
	}
	b, err := url.Parse(base)
	if err != nil || !b.IsAbs() {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	resolved := b.ResolveReference(r)
	if resolved.RawPath != "" {
		resolved.Path, resolved.RawPath = resolved.RawPath, ""
	}
	out := resolved.String()
	// net/url percent-escapes on the way out anything it does not consider
	// legal in a path, and a system identifier is a URI reference the
	// document author wrote, not text to be escaped. The case that matters
	// is the backslash: a DTD naming "images\repository\pic.jpg" came back
	// as "images%5Crepository%5Cpic.jpg", and unparsed-entity-50 is a
	// stylesheet that splits the returned path on "\" to get the filename —
	// after escaping there is no separator left to split on, so it keeps the
	// whole directory chain.
	//
	// Setting RawPath does not help: EscapedPath validates it against Path
	// and discards any RawPath it would itself have escaped, so the value
	// has to be put back after String() has run. Only escapes this function
	// introduced are undone — a %5C the author wrote survives, because it
	// was never a literal backslash in ref to begin with.
	if strings.ContainsAny(ref, "\\") && !strings.Contains(ref, "%5C") &&
		!strings.Contains(ref, "%5c") {
		out = strings.ReplaceAll(out, "%5C", "\\")
	}
	return out
}
