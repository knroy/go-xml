package xsd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// A Resolver turns a schemaLocation into schema source.
//
// The location is a hint, not an identifier: §4.3.2 lets a processor use a
// catalogue, a preloaded schema, or nothing at all. Making resolution an
// interface rather than a built-in fetch is what lets a caller decide, since
// following a location means fetching whatever a document names.
type Resolver interface {
	// Resolve returns the contents of the schema document at location,
	// which is resolved relative to base when it is not absolute. The
	// namespace is the one the reference declared, or empty for an include.
	//
	// Returning a nil reader and a nil error means "no schema document" —
	// which for an include is not an error (§4.2.1), and the caller
	// distinguishes the cases.
	Resolve(namespace, location, base string) (io.ReadCloser, string, error)
}

// noResolverConfigured is the default where no location has been granted.
//
// It answers every request with an error rather than with (nil, nil). The
// contract reads a nil reader and a nil error as "no schema document", which
// for an xs:include is not a failure — the include is dropped and assembly
// succeeds. That is right for a caller who deliberately hardened, and wrong as
// a default: a schema that silently lost half its components validates
// documents against the half that is left. So the refusal is loud.
type noResolverConfigured struct{}

// errNoResolver marks the refusal as a configuration fault rather than a
// location that merely could not be found.
//
// The distinction decides whether assembly carries on. §4.2.1 makes an
// unresolvable include a hint that missed — "no corresponding inclusion is
// performed" — and dropping it is right when a resolver looked and came back
// empty. It is wrong when no resolver was ever configured: the schema then
// loses components for a reason that has nothing to do with the schema, and
// reports success. queueRef tests for this and reports it.
var errNoResolver = errors.New("no Resolver is configured")

// errRefusedByPolicy marks a refusal the configuration made deliberately, as
// against a location that was looked for and not found.
//
// It travels with errNoResolver for the same reason: §4.2.1 lets an
// unresolvable include be dropped, and doing so is right for a location that
// simply is not there — a remote URL with no network resolver is the usual
// case, and the W3C suite's own metadata schema depends on it being tolerated.
// It is wrong for a location the configuration refused on purpose. Dropping
// that one means a caller who set a Root gets a schema quietly missing whatever
// sat outside it, and is told the load succeeded.
var errRefusedByPolicy = errors.New("refused by the resolver's configuration")

// Resolve implements Resolver.
func (noResolverConfigured) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	if location == "" {
		return nil, "", nil
	}
	return nil, "", fmt.Errorf(
		"schemaLocation %q cannot be resolved: %w (Options.Resolver); pass "+
			"%s to say what this schema may read", location, errNoResolver,
		resolverChoices)
}

// multiRootFileResolver reads from any of several directories.
//
// LoadFiles is given a list of paths that need not share a parent, so the
// grant its default makes is the set of the directories the caller named. A
// location is offered to each in turn and the first that admits it wins.
type multiRootFileResolver struct {
	roots []*FileResolver
}

// Resolve implements Resolver.
func (r multiRootFileResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	if location == "" {
		return nil, "", nil
	}
	var firstErr error
	for _, fr := range r.roots {
		rc, resolved, err := fr.Resolve(namespace, location, base)
		if err == nil {
			return rc, resolved, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, "", firstErr
}

// RootedFileResolver returns the resolver LoadFile and LoadFiles install when
// Options.Resolver is nil: file reads confined to the directories of the named
// schema documents, the first that can answer winning. A caller that wraps the
// default -- a CatalogResolver whose fallback should reach exactly what a plain
// load would -- passes it the same paths.
func RootedFileResolver(paths []string) Resolver {
	seen := map[string]bool{}
	var rs []*FileResolver
	for _, p := range paths {
		d := filepath.Dir(p)
		if seen[d] {
			continue
		}
		seen[d] = true
		rs = append(rs, &FileResolver{Root: d})
	}
	if len(rs) == 1 {
		return rs[0]
	}
	return multiRootFileResolver{roots: rs}
}

// FileResolver resolves a schemaLocation against the filesystem.
//
// It is the default because it is the case that cannot surprise anyone: a
// schema that includes a file beside it keeps working, and nothing leaves the
// machine.
type FileResolver struct {
	// Root, when set, confines resolution to a directory. A location that
	// escapes it — through "..", a symlink, or an absolute path — is
	// refused. Leaving it empty permits any readable path, which is the
	// right default for a command-line tool and the wrong one for a server.
	Root string
}

// Resolve implements Resolver.
func (r *FileResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	if location == "" {
		return nil, "", nil
	}
	if isRemote(location) {
		return nil, "", fmt.Errorf(
			"schemaLocation %q is a remote URL and network resolution is not "+
				"enabled; %s", location, remoteHint)
	}

	// A location is a URI reference, so a file: URL and a bare path both
	// have to work.
	p := location
	if u, err := url.Parse(location); err == nil && u.Scheme == "file" {
		// A file: URL may carry an authority, and only an empty one or
		// "localhost" names this machine. Anything else names a remote host,
		// and taking u.Path alone would discard it and silently read the
		// same-named local file instead. relaxng.FileResolver refuses the
		// same way.
		if u.Host != "" && u.Host != "localhost" {
			return nil, "", fmt.Errorf(
				"schemaLocation %q names the remote host %q; only local files "+
					"are permitted", location, u.Host)
		}
		p = u.Path
	}

	if !filepath.IsAbs(p) && base != "" {
		p = filepath.Join(filepath.Dir(base), p)
	}
	p = filepath.Clean(p)

	if r.Root != "" {
		root, err := filepath.Abs(r.Root)
		if err != nil {
			return nil, "", err
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, "", err
		}
		// The root is resolved; the final component of the path deliberately is
		// NOT. os.Root below resolves every component against the root's own
		// descriptor at open time, and that is what makes containment hold
		// against a link swapped in after this check. Pre-resolving the leaf
		// here would hand os.Root a path with the link already followed, which
		// reintroduces exactly the window this shape exists to close. This now
		// matches xslt/resolver.go readConfined; see the note there.
		//
		// The parent directory IS resolved, and only so that like is compared
		// with like: on macOS /var is itself a link to /private/var, so a root
		// that resolved and a path that did not would never share a prefix.
		//
		// Until 2026-09-10 this resolved both sides and opened the resolved
		// path, recorded in docs/security.md as an accepted risk on the grounds
		// that exploiting the remainder needs write access inside the root.
		// That position is withdrawn: two rooted resolvers enforcing one
		// property by two mechanisms cost more to keep explaining than to
		// unify. The string check below is retained as the DIAGNOSIS — it is
		// what produces the errRefusedByPolicy message naming the root, and
		// what §4.2.1 needs in order to tell a deliberate refusal from a miss.
		// os.Root is the ENFORCEMENT.
		if x, err := filepath.EvalSymlinks(root); err == nil {
			root = x
		}
		if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
			abs = filepath.Join(dir, filepath.Base(abs))
		}
		// The separator matters: without it, a Root of "/srv/a" would
		// also admit "/srv/anything".
		rel, err := filepath.Rel(root, abs)
		if err != nil || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, "", fmt.Errorf(
				"schemaLocation %q resolves outside the permitted root: %w",
				location, errRefusedByPolicy)
		}
		rt, err := os.OpenRoot(root)
		if err != nil {
			return nil, "", err
		}
		f, err := rt.Open(filepath.ToSlash(rel))
		rt.Close()
		if err != nil {
			// A miss and an escape must stay distinguishable. §4.2.1 lets an
			// include that was looked for and not found be dropped, and the
			// W3C suite depends on that; a path os.Root refused for leaving
			// the root is a decision and must surface as src-resolve. Only
			// the second is errRefusedByPolicy — see assemble.go queueRef.
			if errors.Is(err, fs.ErrNotExist) {
				return nil, "", err
			}
			// os.Root's own error is wrapped rather than replaced. It is the
			// evidence that containment was enforced HERE, at open time, and
			// not merely by the string comparison above — which is the whole
			// difference between this and the check-then-open shape it
			// replaced, and the only thing a test can hold on to, since both
			// shapes refuse every statically visible vector alike.
			return nil, "", fmt.Errorf(
				"schemaLocation %q resolves outside the permitted root: %w: %w",
				location, errRefusedByPolicy, err)
		}
		return f, filepath.Join(root, rel), nil
	}

	f, err := os.Open(p)
	if err != nil {
		return nil, "", err
	}
	return f, p, nil
}

// isRemote reports whether a location names something to be fetched over the
// network rather than read from disk.
func isRemote(location string) bool {
	u, err := url.Parse(location)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ftp":
		return true
	}
	return false
}

// The resolvers a refusal names, and where it points for remote locations.
// HTTPResolver lives in package xsdnet, so that this package does not link
// net/http.
const (
	resolverChoices = "a FileResolver, a MapResolver or an HTTPResolver"
	remoteHint      = "see HTTPResolver"
)

// MapResolver resolves from an in-memory table, for callers that know every
// schema in advance.
//
// It is the resolver to reach for in a server: nothing is fetched, nothing is
// read from disk, and a schema naming a location that is not in the table is an
// error rather than a request.
type MapResolver struct {
	// ByLocation maps a schemaLocation to schema source.
	ByLocation map[string]string
	// ByNamespace maps a target namespace to schema source, used when an
	// import gives a namespace but no location.
	ByNamespace map[string]string

	mu sync.RWMutex
}

// Resolve implements Resolver.
func (r *MapResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if location != "" {
		if src, ok := r.ByLocation[location]; ok {
			return io.NopCloser(strings.NewReader(src)), location, nil
		}
	}
	if src, ok := r.ByNamespace[namespace]; ok {
		return io.NopCloser(strings.NewReader(src)), namespace, nil
	}
	return nil, "", nil
}
