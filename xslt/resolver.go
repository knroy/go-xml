package xslt

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/knroy/go-xml/xdm"
)

// FileResolver loads stylesheet modules and documents from the filesystem,
// confined to a set of allowed directories.
//
// The confinement is the point. A stylesheet that can call document() on any
// path is a file-disclosure primitive, and one that can reach http:// is an
// SSRF primitive — but a blanket deny is not workable either, because real
// rule sets load code lists that ship beside the stylesheet. Naming the
// directories makes the trust boundary explicit and auditable.
type FileResolver struct {
	// Roots are the directories a relative or absolute path may resolve
	// inside. A path escaping all of them is refused.
	Roots []string

	// AllowDOCTYPE permits a DOCTYPE declaration in the documents this
	// resolver parses. It is off by default, which is what keeps a
	// stylesheet from reaching a document that expands entities or names an
	// external one — the XXE entry point. A caller whose inputs are trusted,
	// a conformance suite among them, can turn it on.
	AllowDOCTYPE bool

	// ExternalEntities permits the documents this resolver parses to read
	// external entities and an external DTD subset, using this same resolver
	// — so they are confined to Roots on exactly the terms everything else
	// is, with the same scheme rejection and the same symlink handling.
	//
	// It is separate from AllowDOCTYPE and off by default. AllowDOCTYPE
	// admits declarations that cost nothing outside the document; this
	// admits reads of other files, which is the XXE surface proper. A caller
	// that wants DTD-declared entities does not thereby want file reads, and
	// making one imply the other would silently widen every existing caller.
	ExternalEntities bool

	// There is deliberately no network option. Adding one would mean this
	// type could not be recommended without caveats, and a validator has no
	// need to fetch rule sets at transform time.

	mu    sync.Mutex
	cache map[string]*xdm.Tree
}

// resolverCacheMax bounds the parsed documents a resolver retains.
//
// The cache is keyed by path under fixed roots, so it cannot grow without
// limit from a single stylesheet — but a stylesheet chooses which documents to
// fetch, and a directory of many files would have it retain every one for the
// life of the process. The bound is generous enough that a normal validation
// run never reaches it and cheap enough that reaching it costs one reparse.
const resolverCacheMax = 256

// NewFileResolver returns a resolver confined to the given directories.
func NewFileResolver(roots ...string) (*FileResolver, error) {
	r := &FileResolver{cache: map[string]*xdm.Tree{}}
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("resolver root %q: %w", root, err)
		}
		// Resolve symlinks now, so that a link inside a root cannot be used
		// to reach outside it after the containment check passes.
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			// A root that does not exist yet is kept as-is; the per-file
			// check below will fail cleanly.
			resolved = abs
		}
		r.Roots = append(r.Roots, resolved)
	}
	return r, nil
}

// resolvePath turns an href into a filesystem path inside an allowed root.
func (r *FileResolver) resolvePath(href, base string) (string, error) {
	// An empty URI reference denotes the document the reference appears in,
	// so with a base in hand it resolves to that base rather than being an
	// error. This is what makes doc('') return the stylesheet itself, which
	// XSLT 2.0 section 16.1 requires.
	if href == "" {
		if base == "" {
			return "", fmt.Errorf("empty href")
		}
		href = base
	}

	// Reject anything that names a non-file scheme before touching the
	// filesystem, so that an http:// URI produces a clear refusal rather than
	// a confusing "no such file".
	if u, err := url.Parse(href); err == nil && u.Scheme != "" && u.Scheme != "file" {
		return "", fmt.Errorf("scheme %q is not permitted (only local files)", u.Scheme)
	}
	href = strings.TrimPrefix(href, "file://")

	// A fragment identifier selects within a document rather than naming a
	// different one, so it is removed before the filesystem sees it — it is
	// not part of any filename. XSLT 2.0 section 16.1 says as much: two
	// references differing only in their fragment retrieve one document.
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}

	p := href
	if !filepath.IsAbs(p) && base != "" {
		baseDir := filepath.Dir(strings.TrimPrefix(base, "file://"))
		p = filepath.Join(baseDir, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	// Resolve symlinks before the containment check: without this, a symlink
	// inside a root pointing at /etc/passwd would pass.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}

	for _, root := range r.Roots {
		// The root is made absolute and symlink-resolved on the same terms
		// as the path. Comparing an absolute path against a root the caller
		// wrote relatively makes filepath.Rel fail, and every file is then
		// refused as outside a directory it is plainly inside — a caller who
		// passes "./schemas" gets a resolver that resolves nothing.
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
			absRoot = resolved
		}
		rel, err := filepath.Rel(absRoot, abs)
		if err != nil {
			continue
		}
		// filepath.Rel returns a ".."-prefixed path when abs is outside root.
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("%q is outside the permitted directories %v", abs, r.Roots)
}

// ResolveModule implements ModuleResolver for xsl:include and xsl:import.
func (r *FileResolver) ResolveModule(href, base string) (*xdm.Node, string, error) {
	path, err := r.resolvePath(href, base)
	if err != nil {
		return nil, "", err
	}
	tree, err := r.load(path)
	if err != nil {
		return nil, "", err
	}
	return tree.Root, path, nil
}

// ResolveDocument implements xpath.DocumentResolver for fn:doc and
// fn:document.
func (r *FileResolver) ResolveDocument(uri, base string) (*xdm.Tree, error) {
	path, err := r.resolvePath(uri, base)
	if err != nil {
		return nil, err
	}
	return r.load(path)
}

// load parses a file, caching the result.
//
// The cache matters for correctness as well as speed: fn:doc is defined to
// return the *same* node for the same URI within one execution, so that
// "doc('x') is doc('x')" is true. Re-parsing would break node identity.
func (r *FileResolver) load(path string) (*xdm.Tree, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if t, ok := r.cache[path]; ok {
		return t, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// A file: URI rather than the bare path. This base URI is stamped on
	// every element of the module and is what fn:document, fn:resolve-uri
	// and fn:static-base-uri resolve against, and those are defined over
	// URIs: an absolute filesystem path has no scheme, so it is still a
	// *relative* URI reference and resolving against it drops everything
	// before the last separator. resolvePath strips the scheme back off, so
	// the filesystem sees the same path either way.
	opts := xdm.ParseOptions{
		BaseURI:      fileURIOf(path),
		AllowDOCTYPE: r.AllowDOCTYPE,
	}
	if r.ExternalEntities {
		opts.ExternalEntities = r
	}
	tree, err := xdm.ParseString(string(data), opts)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	// The cache is created on first use. A FileResolver is documented as
	// usable as a plain literal — &FileResolver{Roots: ...} — so a nil map
	// here is the ordinary case, not a caller's mistake, and writing to one
	// panics.
	//
	// Cleared wholesale rather than evicted one at a time: there is no useful
	// recency signal here, and the same choice is made for the regex cache.
	if r.cache == nil || len(r.cache) >= resolverCacheMax {
		r.cache = map[string]*xdm.Tree{}
	}
	r.cache[path] = tree
	return tree, nil
}

// fileURIOf renders an absolute filesystem path as a file: URI.
//
// The resolvers strip the scheme again before touching the filesystem; the
// URI form exists because base URIs are resolved by the URI rules, which need
// a scheme to treat the base as absolute.
func fileURIOf(path string) string {
	if path == "" || strings.HasPrefix(path, "file:") {
		return path
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return path
		}
		path = abs
	}
	return "file://" + filepath.ToSlash(path)
}

// ResolveEntity implements xdm.EntityResolver, so that a document this
// resolver parses may read external entities — but only from inside Roots.
//
// Every constraint the rest of this type enforces applies here unchanged,
// because the path goes through the same resolvePath: a non-file scheme is
// rejected before the filesystem is touched, symlinks are resolved before the
// containment check, and a path outside every root is refused. There is
// nothing entity-specific about the confinement, which is the point — an
// external entity is a file read like any other, and it gets the same gate
// rather than a second one written separately and drifting.
//
// The base is the URI of the resource that made the reference, which for an
// entity declared in an external DTD subset is that subset rather than the
// document. Resolving against it is XML 1.0 section 4.4.3, and it is why a
// modular DTD in a subdirectory finds its siblings.
//
// The returned URI is the file: URI of what was actually read, since that is
// what anything inside the fetched text resolves against.
func (r *FileResolver) ResolveEntity(systemID, publicID, base string) (io.ReadCloser, string, error) {
	path, err := r.resolvePath(systemID, base)
	if err != nil {
		return nil, "", err
	}
	// Read fully rather than handing back an open file: the caller charges
	// the bytes against its expansion budget, and a resolver that streams
	// would leave a descriptor open for the length of a parse that may fail.
	// xdm bounds what it reads regardless, so this cannot be made to read an
	// unbounded file by the document.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return io.NopCloser(bytes.NewReader(data)), fileURIOf(path), nil
}
