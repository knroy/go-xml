package xslt

import (
	"fmt"
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
	if href == "" {
		return "", fmt.Errorf("empty href")
	}

	// Reject anything that names a non-file scheme before touching the
	// filesystem, so that an http:// URI produces a clear refusal rather than
	// a confusing "no such file".
	if u, err := url.Parse(href); err == nil && u.Scheme != "" && u.Scheme != "file" {
		return "", fmt.Errorf("scheme %q is not permitted (only local files)", u.Scheme)
	}
	href = strings.TrimPrefix(href, "file://")

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
		rel, err := filepath.Rel(root, abs)
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
	tree, err := xdm.ParseString(string(data), xdm.ParseOptions{BaseURI: path})
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	// Cleared wholesale rather than evicted one at a time: there is no useful
	// recency signal here, and the same choice is made for the regex cache.
	if len(r.cache) >= resolverCacheMax {
		r.cache = map[string]*xdm.Tree{}
	}
	r.cache[path] = tree
	return tree, nil
}
