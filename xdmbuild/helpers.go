package xdmbuild

import (
	"net/url"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// DeepCopy clones a subtree, detached from its original parent.
//
// The type annotation travels with the copy. validation="preserve" is defined
// as keeping the types the source carried, and dropping them here left a
// preserved copy untyped, so "$v instance of element(e, xs:anyURI)" answered
// false for a node that had just been copied from a validated document.
// Stripping is done by the validation spec, which is the thing that knows
// whether the instruction asked for it.
func DeepCopy(n *xdm.Node) *xdm.Node {
	c := &xdm.Node{
		Kind:           n.Kind,
		Name:           n.Name,
		Value:          n.Value,
		BaseURI:        n.BaseURI,
		TypeAnnotation: n.TypeAnnotation,
		// The union member travels with the annotation, because the two are
		// halves of one fact: the annotation names the union, and this names
		// the member that actually accepted the value. Atomisation reads the
		// member -- a union's own derivation chain runs to xs:anySimpleType
		// and stops -- so a copy that kept only the annotation atomised to
		// xs:untypedAtomic and lost every type the value really had.
		UnionMember: n.UnionMember,
		IsID:        n.IsID,
		IsIDREFS:    n.IsIDREFS,
		// dm:nilled travels with the annotation on a COPY. A copy of an
		// assessed element is an element that was assessed: validation-1202
		// copies a nilled element with validation="preserve" and requires
		// nilled() to stay true, and fn:copy-of and fn:snapshot in
		// validation-1203 require the same. Only a NEWLY CONSTRUCTED element
		// starts unnilled, which is xsl:copy's case in validation-1204 —
		// there the annotation is preserved but the element itself is new.
		IsNilled: n.IsNilled,
	}
	for _, ns := range n.Namespaces {
		c.AddNamespace(ns.Name.Local, ns.Value)
	}
	for _, a := range n.Attrs {
		c.AddAttr(&xdm.Node{Kind: xdm.KindAttribute, Name: a.Name,
			Value: a.Value, TypeAnnotation: a.TypeAnnotation,
			UnionMember: a.UnionMember,
			IsID:        a.IsID, IsIDREFS: a.IsIDREFS})
	}
	for _, ch := range n.Children {
		c.AppendChild(DeepCopy(ch))
	}
	return c
}

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
