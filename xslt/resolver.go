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
	"unicode/utf8"

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

	// UnparsedText permits fn:unparsed-text to read files through this
	// resolver, confined to Roots on the same terms as everything else.
	//
	// It is separate from every other flag here and off by default, because
	// it is the widest of them. ResolveDocument hands the stylesheet a
	// parsed XML document, so a file that is not well-formed XML discloses
	// nothing; unparsed-text hands back the raw bytes of any file inside
	// Roots, so a root containing one XML data file and one private key
	// leaks the key. A caller who wants fn:doc does not thereby want that,
	// and folding the two together would silently widen every existing
	// caller of NewFileResolver.
	UnparsedText bool

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
	// A stylesheet module keeps its source positions; see load.
	tree, err := r.loadTracked(path, true)
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

// Preload records a tree that has already been parsed as the answer for uri,
// so that a later fn:doc or fn:document naming the same resource hands back
// the very same nodes rather than a second parse of the same bytes.
//
// Node identity is the point. XSLT 2.0 section 16.1 requires two retrievals of
// one absolute URI to return the same node, and the test the specification is
// written for is "fn:doc(fn:document-uri($arg)) is $arg". A caller that parses
// the principal source itself — every conformance harness does, because it has
// to annotate the tree before the transform sees it — supplies a document node
// the resolver has never heard of, and doc() of its own document-uri then
// parses the file again and answers a different node. Preloading closes that
// gap without weakening the containment check: the uri still has to resolve to
// a path inside a permitted root, and an unresolvable one is a no-op.
func (r *FileResolver) Preload(uri string, tree *xdm.Tree) {
	if tree == nil {
		return
	}
	path, err := r.resolvePath(uri, "")
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = map[string]*xdm.Tree{}
	}
	r.cache[path] = tree
}

// load parses a file, caching the result.
//
// The cache matters for correctness as well as speed: fn:doc is defined to
// return the *same* node for the same URI within one execution, so that
// "doc('x') is doc('x')" is true. Re-parsing would break node identity.
func (r *FileResolver) load(path string) (*xdm.Tree, error) {
	return r.loadTracked(path, false)
}

// loadTracked is load, saying whether the parsed tree should remember where
// each element was written.
//
// Positions are kept for stylesheet modules and not for source documents. A
// module needs them because XSLT 3.0 §8.3 publishes the line an error was
// raised on to an xsl:catch clause as $err:line-number, and there is nothing
// else that can answer. A source document does not: retaining the text for
// every document a transform reads costs memory on all of them to serve
// gx:line-number(), which almost nothing calls. catalog-007 is the case that
// settles it -- it reads several thousand stylesheets through fn:document,
// and tracking positions on all of them pushed the run past its deadline.
func (r *FileResolver) loadTracked(path string, trackPos bool) (*xdm.Tree, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if t, ok := r.cache[path]; ok {
		// A cached tree serves a request that does not need more than it
		// has. Only a module asking for positions of a tree parsed without
		// them has to go back to the file: node identity matters for fn:doc,
		// which is the untracked side, and a module is compiled once before
		// any expression can hold a node from it.
		if !trackPos || t.HasPositions() {
			return t, nil
		}
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
		BaseURI: fileURIOf(path),
		// This document *was* retrieved by URI, so it has a dm:document-uri
		// as well as a base URI, and fn:document-uri must report it. The two
		// carry the same string here only because the retrieval succeeded
		// from that URI; a temporary tree gets a base URI from its
		// stylesheet and no document URI at all, which is the distinction
		// fn:document-uri exists to make.
		DocumentURI:    fileURIOf(path),
		AllowDOCTYPE:   r.AllowDOCTYPE,
		TrackPositions: trackPos,
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

// ResolveText implements xpath.TextResolver for fn:unparsed-text.
//
// The path goes through the same resolvePath as every other read this type
// performs, so the confinement is one implementation rather than two: a
// non-file scheme is rejected before the filesystem is touched, symlinks are
// resolved before the containment check, and a path outside every root is
// refused. UnparsedText only decides *whether* to ask; it does not relax
// where the answer may come from.
//
// The encoding argument is honoured only for the encodings this package can
// decode without pulling in a converter. XSLT 2.0 section 16.2 requires an
// error for an encoding that is not supported, and reporting one is better
// than silently returning mojibake -- a stylesheet that reads a Shift-JIS
// file and gets bytes reinterpreted as UTF-8 produces wrong output with no
// indication anything went wrong.
func (r *FileResolver) ResolveText(uri, base, encoding string) (string, error) {
	if !r.UnparsedText {
		return "", fmt.Errorf("unparsed-text() is not enabled on this resolver")
	}
	path, err := r.resolvePath(uri, base)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		// US-ASCII is a subset of UTF-8, so the same decode is correct for
		// it, and a byte above 0x7F is caught by the validity check below
		// either way.
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "iso_8859-1", "cp819":
		// ISO-8859-1 maps each byte to the code point of the same value, so
		// the decode is a byte-by-byte widening and needs no tables. It is
		// worth having: it is the one non-Unicode encoding the suite's own
		// files are written in, and refusing it made unparsed-text() report
		// FOUT1190 for a file it could read perfectly well.
		runes := make([]rune, len(data))
		for i, b := range data {
			runes[i] = rune(b)
		}
		data = []byte(string(runes))
	default:
		return "", fmt.Errorf("FOUT1190: encoding %q is not supported", encoding)
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	// A BOM is an encoding signature rather than a character of the content,
	// so leaving it in makes string-length() one too large and makes a
	// comparison against the file's first line fail for a reason nothing in
	// the stylesheet can see.
	//
	// F&O requires FOUT1190 when the resource cannot be decoded, and W3C bug
	// 29302 settled that a character not permitted in XML is the same error:
	// the draft's FOUT1170 did not survive, and no case in the suite expects
	// it. unparsed-text-lines-006 asks for FOUT1190 on a file holding a NUL,
	// and -004 catches errors="*:FOUT1190" for the same file. Returning a Go
	// string holding invalid UTF-8 would push the failure downstream into the
	// serialiser, where it reads as a bug in this engine rather than as a
	// property of the input.
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("FOUT1190: %s is not valid UTF-8", path)
	}
	for _, c := range text {
		if !isXMLChar(c) {
			return "", fmt.Errorf(
				"FOUT1190: %s contains U+%04X, which is not a legal XML character",
				path, c)
		}
	}
	return text, nil
}

// isXMLChar reports whether c is in the XML 1.0 Char production.
//
// The check belongs here rather than being left to the serialiser because
// fn:unparsed-text can put the text into a *string*, where a control
// character is not an error until something tries to serialise it -- and by
// then the stylesheet has already computed with it.
func isXMLChar(c rune) bool {
	switch {
	case c == 0x9, c == 0xA, c == 0xD:
		return true
	case c >= 0x20 && c <= 0xD7FF:
		return true
	case c >= 0xE000 && c <= 0xFFFD:
		return true
	case c >= 0x10000 && c <= 0x10FFFF:
		return true
	}
	return false
}
