package xsd

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/knroy/go-xml/v2/internal/fileuri"
	"github.com/knroy/go-xml/v2/internal/uripath"
	"github.com/knroy/go-xml/v2/xdm"
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
	// schema's own resolver is used, and when that is nil too, a
	// FileResolver rooted at the directories the schema was loaded from.
	//
	// Whatever the resolver, a location that is not remote must lie inside
	// those directories, or WithInstanceLocations refuses it before the
	// resolver is asked: an HTTPResolver with no Files reads local
	// locations through an unrooted FileResolver, and would otherwise open
	// any path the instance named. A caller following untrusted documents
	// should still supply a MapResolver, or an xsdnet.HTTPResolver whose
	// AllowHost decides which remote locations may be fetched.
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
//
// Each document must define the namespace it was named for: one named in an
// xsi:schemaLocation pair must have that pair's namespace as its target
// namespace, and one named by xsi:noNamespaceSchemaLocation must have none.
// The policy allows namespaces, so a document defining any other is refused.
func (s *Schema) WithInstanceLocations(root *xdm.Node, policy InstanceLocationPolicy, opts Options) (*Schema, error) {
	locs := instanceLocations(root, policy)
	if len(locs) == 0 {
		return s, nil
	}

	if opts.Resolver == nil {
		opts.Resolver = policy.Resolver
	}
	if opts.Resolver == nil {
		// The locations here come from the instance, which is the least
		// trusted input this package takes, so the unrooted default was
		// worst of all at this site: a document could name any readable
		// path and have it opened. The fallback is confined to the
		// directories the schema was loaded from, which is the same base
		// the locations resolve against below.
		//
		// A schema with no source paths grants nothing, and the check
		// below turns that into an error; refusing here as well keeps a
		// future caller from reaching an empty root set that admitted
		// everything by having nothing to test against.
		if len(s.sourcePaths) == 0 {
			opts.Resolver = noResolverConfigured{}
		} else {
			opts.Resolver = RootedFileResolver(s.sourcePaths)
		}
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
	//
	// Identity is asked of the filesystem only for a path inside a directory
	// the schema was loaded from, which is where its own documents are.
	// Anywhere else the location is the instance's choice, and statting it
	// and listing its directory would touch the host filesystem outside the
	// resolver — once per spelling, before any budget applies — so it is
	// compared by its cleaned text and left to the resolver.
	var canon canonCache
	dirs := make([]string, 0, len(s.sourcePaths))
	have := map[string]bool{}
	for _, p := range s.sourcePaths {
		have[canon.location(p)] = true
		have[filepath.Clean(p)] = true
		if d, err := filepath.Abs(filepath.Dir(p)); err == nil {
			dirs = append(dirs, d)
		}
	}
	wantNS := map[int]string{}
	for _, l := range locs {
		full, err := instanceLocalPath(l.location, base, dirs)
		if err != nil {
			return nil, err
		}
		if key := instanceLocationKey(&canon, dirs, full); have[key] {
			continue
		} else {
			have[key] = true
		}
		wantNS[len(paths)] = l.namespace
		paths = append(paths, full)
	}
	if len(paths) == len(s.sourcePaths) {
		// Every location named a document already loaded.
		return s, nil
	}
	return loadFiles(paths, opts, wantNS)
}

// instanceLocalPath returns the location LoadFiles is given for loc, an
// instance-supplied location, and refuses one that is not remote and does not
// lie inside dirs, the absolute directories the schema was loaded from.
//
// The check is made here rather than left to the resolver because the
// resolver may not be confined: HTTPResolver with no Files reads local
// locations through an unrooted FileResolver, and every path LoadFiles is
// given resolves against an empty base, so its refusal of a local location
// under a remote base never applies. A remote location is the resolver's to
// judge. A file: URL is passed on as written and checked as the path it names.
func instanceLocalPath(loc, base string, dirs []string) (string, error) {
	if isRemote(loc) {
		return loc, nil
	}
	p, full := loc, ""
	if u, err := url.Parse(loc); err == nil && u.Scheme == "file" {
		if u.Host != "" && u.Host != "localhost" {
			return "", fmt.Errorf(
				"schemaLocation %q names the remote host %q; only local "+
					"files are permitted", loc, u.Host)
		}
		p, full = fileuri.ToPath(loc), loc
	}
	// A network spelling is refused as written as well as once joined: on
	// Unix `\\host\share` is relative and the join hides it, and deciding it
	// on the string keeps the Windows rule exercised everywhere.
	foreign := uripath.IsForeignNetworkPath(p, dirs...)
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	if full == "" {
		full = p
	}
	if foreign || !insideDirs(p, dirs) {
		return "", fmt.Errorf(
			"schemaLocation %q in the instance resolves outside the "+
				"directories the schema was loaded from: %w", loc, errRefusedByPolicy)
	}
	return full, nil
}

// insideDirs reports whether p lies inside one of dirs once both are resolved,
// the final component of p included, so that a link inside a directory does
// not admit its target outside it. A network path is refused before anything
// touches the filesystem; see uripath.IsForeignNetworkPath.
func insideDirs(p string, dirs []string) bool {
	if uripath.IsForeignNetworkPath(p, dirs...) {
		return false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	abs = uripath.EvalExisting(abs)
	for _, d := range dirs {
		rel, err := filepath.Rel(uripath.EvalExisting(d), abs)
		if err == nil && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// instanceLocationKey is the deduplication key for an instance-supplied
// location: its filesystem identity when it lies inside one of dirs (absolute
// directories), and its cleaned text otherwise.
func instanceLocationKey(canon *canonCache, dirs []string, full string) string {
	if strings.Contains(full, "://") {
		return full
	}
	full = filepath.Clean(full)
	if abs, err := filepath.Abs(full); err == nil {
		for _, d := range dirs {
			rel, err := filepath.Rel(d, abs)
			if err == nil && rel != ".." &&
				!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return canon.location(full)
			}
		}
	}
	return full
}

// instanceLocation is a location an instance offers, with the namespace it
// offers it for ("" for xsi:noNamespaceSchemaLocation).
type instanceLocation struct {
	namespace, location string
}

// instanceLocations reads the locations an instance offers, keeping the ones
// the policy allows.
//
// The attributes may appear on any element, not only the root — a document may
// introduce a namespace partway down — so the whole tree is walked.
func instanceLocations(root *xdm.Node, policy InstanceLocationPolicy) []instanceLocation {
	var out []instanceLocation
	seen := map[string]bool{}

	var walk func(*xdm.Node)
	walk = func(n *xdm.Node) {
		if n.Kind() == xdm.KindElement {
			if a := n.Attr(NSInstance, "schemaLocation"); a != nil {
				// The value is a whitespace-separated list of
				// (namespace, location) pairs. An odd number of
				// entries is malformed; the spec says nothing
				// useful about it, so the trailing one is
				// dropped rather than paired with nothing.
				// splitFields: xsi:schemaLocation is a list type, and an
				// XSD list tokenizes on XML S alone. A no-break space is
				// data inside a token, so strings.Fields turned one URI
				// into two and paired the halves wrongly.
				f := splitFields(a.Value())
				for i := 0; i+1 < len(f); i += 2 {
					ns, loc := f[i], f[i+1]
					if policy.AllowNamespace == nil ||
						!policy.AllowNamespace(ns) {
						continue
					}
					if !seen[loc] {
						seen[loc] = true
						out = append(out, instanceLocation{ns, loc})
					}
				}
			}
			if a := n.Attr(NSInstance, "noNamespaceSchemaLocation"); a != nil &&
				policy.AllowNoNamespace {
				for _, loc := range splitFields(a.Value()) {
					if !seen[loc] {
						seen[loc] = true
						out = append(out, instanceLocation{"", loc})
					}
				}
			}
		}
		for c := range n.Children() {
			walk(c)
		}
	}
	walk(root)
	return out
}
