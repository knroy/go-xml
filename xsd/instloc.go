package xsd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/knroy/go-xml/xdm"
)

// Instance-supplied schema locations (§4.3.2, "How schema definitions are
// located on the Web").
//
// An instance document may carry xsi:schemaLocation and
// xsi:noNamespaceSchemaLocation, naming documents it says describe it. Every
// widely used processor follows them by default. This one does not, because
// following them lets whoever wrote the document choose the schema it is judged
// against — a document that fails validation can name a permissive schema and
// pass, which is the opposite of what validating it was for.
//
// The feature is therefore opt-in and gated twice: the caller has to ask for it
// at all, and then has to say which namespaces may be extended. Neither gate
// has a default that says yes.

// InstanceLocationPolicy decides whether an instance may name a schema document
// for a namespace, and is how a caller turns xsi:schemaLocation on.
//
// It is a policy rather than a boolean because "follow what the document says"
// is not a safe thing to grant wholesale. The useful grant is narrow: a caller
// that already trusts a set of namespaces — a fixed vocabulary it ships, a
// catalogue it controls — and wants the instance to say which *version* of them
// applies.
type InstanceLocationPolicy struct {
	// AllowNamespace reports whether the instance may supply a location for
	// a namespace. It is consulted for every entry, and an entry it refuses
	// is ignored rather than being an error: the location is a hint, and a
	// hint this processor declines to take is not a fault in the document.
	//
	// A nil AllowNamespace allows nothing, so the zero value of this type
	// is inert. That is deliberate — a policy that did nothing but exist
	// should not be the one that opens the door.
	AllowNamespace func(namespace string) bool

	// AllowNoNamespace permits xsi:noNamespaceSchemaLocation, which names a
	// document for the absent namespace. It is separate from
	// AllowNamespace because "" is not a namespace a caller thinks about,
	// and folding it in would grant it by accident.
	AllowNoNamespace bool

	// Resolver fetches the documents the instance names. When nil the
	// schema's own resolver is used — but note that a FileResolver will
	// read whatever path the instance gives it, relative to the base, so a
	// caller following untrusted documents should supply a MapResolver or
	// an HTTPResolver with AllowHost.
	Resolver Resolver

	// MaxDocuments bounds how many documents the instance may pull in.
	// Zero means DefaultMaxDocuments.
	MaxDocuments int
}

// WithInstanceLocations returns a schema extended by the documents an instance
// names in xsi:schemaLocation and xsi:noNamespaceSchemaLocation.
//
// The receiver is not modified: a Schema is immutable once loaded and safe to
// share, so extending one produces another. The cost is a fresh assembly per
// instance, which is why this is a separate call rather than something Validate
// does — a caller validating many documents against one schema should not pay
// for it, and most callers should not use this at all.
//
// Locations the policy refuses are ignored. §4.3.2 makes the attribute a hint,
// so declining to take it is not a fault in the document; a reference that
// really needed the components still fails, at the reference.
func (s *Schema) WithInstanceLocations(root *xdm.Node, policy InstanceLocationPolicy, opts Options) (*Schema, error) {
	locs := instanceLocations(root, policy)
	if len(locs) == 0 {
		return s, nil
	}

	if opts.Resolver == nil {
		opts.Resolver = policy.Resolver
	}
	if opts.Resolver == nil {
		opts.Resolver = &FileResolver{}
	}
	if opts.MaxDocuments == 0 {
		opts.MaxDocuments = policy.MaxDocuments
	}
	if opts.MaxDocuments == 0 {
		opts.MaxDocuments = DefaultMaxDocuments
	}
	opts.Version = s.Version

	// The documents are assembled together with the ones already loaded,
	// rather than merged afterwards, because a schema is not the union of
	// its documents: a type in one may extend a type in another, and the
	// fixups that resolve such a reference run once over the whole set.
	if len(s.sourcePaths) == 0 {
		return nil, fmt.Errorf(
			"this schema was not loaded from files, so it cannot be " +
				"extended by instance-supplied locations")
	}
	// A location in the instance is relative to the instance, which this
	// does not have — Validate is given a tree, not a path. Resolving
	// against the first schema document is the useful approximation for the
	// common case, where the two sit together; a caller whose instance
	// lives elsewhere should supply a MapResolver, which ignores base
	// entirely.
	base := filepath.Dir(s.sourcePaths[0])
	paths := append([]string(nil), s.sourcePaths...)

	// A document the schema already holds must not be listed again. An
	// instance routinely names its own schema — xsi:noNamespaceSchemaLocation
	// beside an xsi:schemaLocation for some other namespace is the ordinary
	// spelling — and loading it twice makes every global in it a duplicate
	// of itself, so the whole assembly fails with sch-props-correct.2.
	//
	// The comparison is by file identity rather than by path text, because
	// the instance's spelling of a location need not match the one the
	// schema was loaded under: "b.xsd" and "./b.xsd" and an absolute path
	// are three ways of naming one file.
	have := map[string]bool{}
	for _, p := range s.sourcePaths {
		have[canonicalLocation(p)] = true
	}
	for _, loc := range locs {
		full := loc
		if !filepath.IsAbs(loc) && !strings.Contains(loc, "://") {
			full = filepath.Join(base, loc)
		}
		if key := canonicalLocation(full); have[key] {
			continue
		} else {
			have[key] = true
		}
		paths = append(paths, full)
	}
	if len(paths) == len(s.sourcePaths) {
		// Every location named a document already loaded.
		return s, nil
	}
	return LoadFiles(paths, opts)
}

// instanceLocations reads the locations an instance offers, keeping the ones
// the policy allows.
//
// The attributes may appear on any element, not only the root — a document may
// introduce a namespace partway down — so the whole tree is walked.
func instanceLocations(root *xdm.Node, policy InstanceLocationPolicy) []string {
	var out []string
	seen := map[string]bool{}

	var walk func(*xdm.Node)
	walk = func(n *xdm.Node) {
		if n.Kind == xdm.KindElement {
			if a := n.Attr(NSInstance, "schemaLocation"); a != nil {
				// The value is a whitespace-separated list of
				// (namespace, location) pairs. An odd number of
				// entries is malformed; the spec says nothing
				// useful about it, so the trailing one is
				// dropped rather than paired with nothing.
				f := strings.Fields(a.Value)
				for i := 0; i+1 < len(f); i += 2 {
					ns, loc := f[i], f[i+1]
					if policy.AllowNamespace == nil ||
						!policy.AllowNamespace(ns) {
						continue
					}
					if !seen[loc] {
						seen[loc] = true
						out = append(out, loc)
					}
				}
			}
			if a := n.Attr(NSInstance, "noNamespaceSchemaLocation"); a != nil &&
				policy.AllowNoNamespace {
				for _, loc := range strings.Fields(a.Value) {
					if !seen[loc] {
						seen[loc] = true
						out = append(out, loc)
					}
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}
