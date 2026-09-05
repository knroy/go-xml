package xslt

import (
	"bytes"
	"fmt"
	"io"
	"math"
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

	// MaxBytes bounds a single file this resolver reads, whichever way the
	// stylesheet asks for it: fn:doc and xsl:import, an external entity,
	// fn:unparsed-text, and XInclude parse="text" all go through one read.
	// Zero means DefaultMaxResourceBytes; a negative value means no limit,
	// for a caller reading files it produced itself.
	//
	// It is one field rather than one per call path on purpose. The confinement
	// above is a property of the *file*, not of the function that names it —
	// every root is readable by every path, so a stylesheet refused a 200 MB
	// file through unparsed-text would simply ask for it through doc(), and two
	// numbers would only mean the effective limit is the larger of them while
	// looking like it were the smaller. One number is the honest statement of
	// what a resolver will put in memory for one resource.
	//
	// The bound is needed here and not only downstream. xdm.ParseOptions.MaxBytes
	// bounds the *parse*, but readConfined has the whole file in memory before
	// the parser is handed anything, and fn:unparsed-text and XInclude
	// parse="text" never reach a parser at all. Once a caller enables a
	// FileResolver the stylesheet chooses which permitted file is read, so an
	// unbounded read makes any large readable file a memory-exhaustion
	// primitive.
	MaxBytes int64

	// There is deliberately no network option. Adding one would mean this
	// type could not be recommended without caveats, and a validator has no
	// need to fetch rule sets at transform time.

	mu    sync.Mutex
	cache map[string]*xdm.Tree
	// inflight tracks a path being read and parsed right now, so that
	// concurrent callers wait for that one parse rather than each doing their
	// own. Node identity requires it: fn:doc is defined to return the *same*
	// node for the same URI, and two goroutines that each parsed and each
	// published would hand out two trees for one document.
	inflight map[string]*loadCall
}

// loadCall is one in-progress parse. done is closed when tree and err are set.
type loadCall struct {
	done chan struct{}
	tree *xdm.Tree
	err  error
}

// resolverCacheMax bounds the parsed documents a resolver retains.
//
// The cache is keyed by path under fixed roots, so it cannot grow without
// limit from a single stylesheet — but a stylesheet chooses which documents to
// fetch, and a directory of many files would have it retain every one for the
// life of the process. The bound is generous enough that a normal validation
// run never reaches it and cheap enough that reaching it costs one reparse.
const resolverCacheMax = 256

// DefaultMaxResourceBytes bounds one file read through a FileResolver when
// MaxBytes is zero: 64 MB.
//
// It is deliberately the same number as xdm.DefaultMaxBytes. A document read
// through fn:doc is handed straight to that parser, so a different figure here
// would mean one of the two limits never binds — a smaller one would make the
// parser's limit unreachable and a larger one would make this one decorative.
// Matching it means the file is refused at the read, before the bytes are
// spent, and the parser's identical limit remains the backstop for input that
// arrives some other way. 64 MB is far above any real stylesheet, code list or
// text resource while still bounding what a single resolved reference can
// allocate.
const DefaultMaxResourceBytes int64 = 64 << 20

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

// readConfined reads a file that resolvePath has already placed inside a root,
// re-checking containment at open time rather than trusting the earlier check.
//
// resolvePath calls filepath.EvalSymlinks and then compares against the roots,
// which is correct for the threat this library states: a hostile *document*
// cannot change the filesystem between the two. It is not correct against an
// attacker who can, because the path checked and the path opened are resolved
// at different moments, and a symlink swapped in between makes them different
// files.
//
// os.Root closes that window. Opening through it resolves each path component
// relative to the root's own descriptor and refuses to traverse out of it, so
// containment is enforced by the kernel at the moment of the open instead of by
// a string comparison beforehand. A symlink swapped in after resolvePath
// returned is refused rather than followed.
//
// The earlier check is kept: it produces the error message that names the
// permitted directories, and it is what decides *which* root a path belongs to.
// This is the enforcement, not the diagnosis.
func (r *FileResolver) readConfined(path string) ([]byte, error) {
	for _, root := range r.Roots {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
			absRoot = resolved
		}
		// The path is made absolute on the same terms as the root. Its
		// symlinks are deliberately NOT resolved here: that is the whole
		// point -- os.Root below decides what the components mean, at open
		// time. But the *root* must be compared like with like, and on macOS
		// /var is itself a link to /private/var, so a root that resolved and
		// a path that did not would never share a prefix.
		absPath, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		if dir, err := filepath.EvalSymlinks(filepath.Dir(absPath)); err == nil {
			absPath = filepath.Join(dir, filepath.Base(absPath))
		}
		rel, err := filepath.Rel(absRoot, absPath)
		if err != nil || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		rt, err := os.OpenRoot(absRoot)
		if err != nil {
			continue
		}
		f, err := rt.Open(filepath.ToSlash(rel))
		if err != nil {
			rt.Close()
			return nil, err
		}
		data, err := r.readLimited(f, path)
		f.Close()
		rt.Close()
		return data, err
	}
	// No root contains it. A resolver with no roots at all is the documented
	// unconfined case, and reaching here means resolvePath admitted a path
	// this loop did not, so fall back rather than refuse a file the caller
	// was told was readable.
	if len(r.Roots) == 0 {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return r.readLimited(f, path)
	}
	return nil, fmt.Errorf("%q is outside the permitted directories %v",
		path, r.Roots)
}

// readLimited reads f whole, refusing a file larger than the resolver's byte
// budget rather than truncating it.
//
// Truncation is not an option: a half-read stylesheet is a different, smaller
// stylesheet that may well parse, and a half-read text resource is a string the
// stylesheet computes with. Both are silently wrong output, which is the one
// outcome this library declines to produce. The refusal names the limit so that
// a caller who wants the file raises MaxBytes rather than guessing.
func (r *FileResolver) readLimited(f io.Reader, path string) ([]byte, error) {
	max := r.MaxBytes
	if max == 0 {
		max = DefaultMaxResourceBytes
	}
	if max < 0 {
		return io.ReadAll(f)
	}
	// One byte over the limit is read deliberately, so that a file of exactly
	// the maximum size is accepted and one byte more is distinguishable from
	// it. The increment saturates: max+1 overflows to a negative limit at
	// math.MaxInt64, and io.LimitReader reads a negative limit as "nothing
	// left", so the largest limit a caller can name would silently return an
	// empty file. An empty read is worse than a refusal, because nothing
	// downstream can tell it from a genuinely empty resource.
	lim := max
	if lim < math.MaxInt64 {
		lim++
	}
	data, err := io.ReadAll(io.LimitReader(f, lim))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s exceeds the %d byte limit", path, max)
	}
	return data, nil
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
	href = fileURIToPath(href)

	// A fragment identifier selects within a document rather than naming a
	// different one, so it is removed before the filesystem sees it — it is
	// not part of any filename. XSLT 2.0 section 16.1 says as much: two
	// references differing only in their fragment retrieve one document.
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}

	p := href
	if !filepath.IsAbs(p) && base != "" {
		baseDir := filepath.Dir(fileURIToPath(base))
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
	// Through publish, not straight into the map: the bound on the cache is a
	// property of the cache, so every path that writes to it has to obey it.
	// This one is a host-application call rather than anything a document or a
	// stylesheet can reach, so an unbounded Preload was an invariant violation
	// and not an attack surface -- but a host that preloads a document per
	// request would still have grown the map for the life of the process.
	r.publish(path, tree)
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
	// The lock covers the cache and the in-flight table, not the read and the
	// parse. Holding it across those made concurrent transforms sharing one
	// resolver load modules one at a time on a cold cache -- the mutex was
	// protecting cache correctness but covering I/O as well.
	//
	// Releasing it alone would not be enough, because node identity is part of
	// the contract: fn:doc must return the same node for the same URI, and two
	// goroutines that each parsed and each published would break that. So a
	// path being loaded is announced in inflight, and a second caller waits on
	// it instead of starting its own parse.
	//
	// The key includes trackPos. A tree parsed without positions cannot serve
	// a module that needs them, so the two are separate pieces of work; the
	// cache lookup below keeps the existing rule that a tracked tree may serve
	// an untracked request.
	key := path
	if trackPos {
		key = path + "\x00pos"
	}
	for {
		r.mu.Lock()
		if t, ok := r.cache[path]; ok {
			// A cached tree serves a request that does not need more than it
			// has. Only a module asking for positions of a tree parsed without
			// them has to go back to the file: node identity matters for
			// fn:doc, which is the untracked side, and a module is compiled
			// once before any expression can hold a node from it.
			if !trackPos || t.HasPositions() {
				r.mu.Unlock()
				return t, nil
			}
		}
		if c, ok := r.inflight[key]; ok {
			// Someone else is already reading this path. Wait for their
			// result rather than duplicating the work and the tree.
			r.mu.Unlock()
			<-c.done
			if c.err != nil {
				return nil, c.err
			}
			// Re-check the cache rather than trusting c.tree directly: the
			// loop re-applies the positions rule above, so a waiter that needs
			// positions is not handed a tree parsed without them.
			continue
		}
		call := &loadCall{done: make(chan struct{})}
		if r.inflight == nil {
			r.inflight = map[string]*loadCall{}
		}
		r.inflight[key] = call
		r.mu.Unlock()

		tree, err := r.parseUncached(path, trackPos)

		r.mu.Lock()
		if err == nil {
			r.publish(path, tree)
		}
		delete(r.inflight, key)
		r.mu.Unlock()

		call.tree, call.err = tree, err
		close(call.done)
		return tree, err
	}
}

// publish stores a parsed tree. The caller holds r.mu.
func (r *FileResolver) publish(path string, tree *xdm.Tree) {
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
}

// parseUncached reads and parses a file. It holds no lock: this is the work
// that used to run under r.mu.
func (r *FileResolver) parseUncached(path string, trackPos bool) (*xdm.Tree, error) {
	data, err := r.readConfined(path)
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
	return tree, nil
}

// fileURIToPath turns a file: URI back into a filesystem path, and leaves
// anything that is not one alone.
//
// It is the inverse of fileURIOf, and it exists because stripping the "file://"
// prefix textually is not: on Windows that leaves "/C:/dir/s.xsl" from the
// three-slash form, which is not a path any filesystem call accepts, and it
// leaves a percent-escape unescaped, so a directory with a space in its name
// became one with "%20" in it.
func fileURIToPath(s string) string {
	if !strings.HasPrefix(s, "file:") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil {
		// Not a URI after all; the textual strip is the best that can be done
		// and is what this did before.
		return strings.TrimPrefix(s, "file://")
	}
	p := u.Path
	// A Windows path arrives as "/C:/dir/s.xsl" -- an empty authority followed
	// by a drive-letter path. The leading slash belongs to the URI, not to the
	// path, and only there: "/home/u" keeps its own.
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// fileURIOf renders an absolute filesystem path as a file: URI.
//
// The resolvers strip the scheme again before touching the filesystem; the
// URI form exists because base URIs are resolved by the URI rules, which need
// a scheme to treat the base as absolute.
//
// The leading slash matters on Windows, where an absolute path is C:\dir\s.xsl
// and has none of its own: "file://" + "C:/dir/s.xsl" makes C: the authority
// rather than the drive, and parsing it back yields host "C:" and a path with
// the drive letter gone. RFC 8089 gives a local path an empty authority, which
// is the three-slash form.
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
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return "file://" + slashed
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
	// The read is bounded by MaxBytes, so a document cannot name an entity
	// whose file is larger than this resolver will hold.
	data, err := r.readConfined(path)
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
	data, err := r.readConfined(path)
	if err != nil {
		return "", err
	}
	return decodeText(data, encoding, path)
}

// decodeText turns the bytes of a text resource into a Go string, honouring
// the encoding the caller named and the one the resource declares.
//
// It is factored out of ResolveText because XInclude's parse="text" is defined
// by the same rules — XInclude 1.0 section 4.4 defers to the encoding
// determination F&O describes — and two copies of an encoding table is two
// places for a decode to be wrong in different ways.
//
// The path argument appears only in error messages, so that a refusal names
// the file that could not be read rather than leaving the caller to guess.
func decodeText(data []byte, encoding, path string) (string, error) {
	// F&O 3.0 section 14.8.2: "The encoding of the external resource is
	// determined as follows: external encoding information is used if
	// available, otherwise if the media type of the resource is text/xml or
	// application/xml [...] then the encoding is recognized as specified in
	// [XML 1.0], otherwise the value of the $encoding argument is used if
	// present". A file read off the filesystem carries no external encoding
	// information, so for an XML resource the declaration outranks the
	// argument, and when the argument is absent it is the only thing that
	// stands between the reader and a mis-decode. catalog-005b reads
	// character-map-010.xsl, which declares iso-8859-1 and holds bytes that
	// are not valid UTF-8; without this the read fails with FOUT1190 for a
	// file whose own first line says how to decode it.
	if declared := xmlDeclEncoding(data); declared != "" {
		encoding = declared
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
	// F&O draws the line between the two decode errors at whether the call
	// named an encoding. FOUT1190 is "cannot be decoded using the specified
	// encoding", so it belongs to a read that was told how to read and found
	// the bytes did not fit; FOUT1200 is raised when "$encoding is absent and
	// the processor cannot infer the encoding using external information and
	// the encoding is not UTF-8", which is exactly a plain read of a file
	// that turns out not to be UTF-8. W3C bug 29302 settled that a character
	// XML does not permit is the same error as an undecodable one -- the
	// draft's FOUT1170 did not survive -- so both checks below follow the
	// same rule. unparsed-text-lines-006 asks for FOUT1190 on a file holding
	// a NUL and -004 catches errors="*:FOUT1190" for it; both name
	// iso-8859-1, so both still get FOUT1190. Returning a Go string holding
	// invalid UTF-8 would push the failure downstream into the serialiser,
	// where it reads as a bug in this engine rather than as a property of
	// the input.
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("FOUT1190: %s is not valid UTF-8", path)
	}
	// Whether the text may hold a character XML does not permit is the
	// calling function's rule rather than this resolver's, and fn:unparsed-text
	// applies it on the way out. fn:json-doc reads through the same resolver
	// and must not apply it: a JSON text may hold U+FFFF, and an unescaped
	// control character in one is FOJS0001 from the JSON parser rather than a
	// decoding error raised before the parser runs.
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

// xmlDeclEncoding returns the encoding named by the XML declaration at the
// start of data, or "" when there is no declaration or it names no encoding.
//
// Only the ASCII-compatible single-byte case is recognised, which is the case
// the rule is for: a UTF-16 resource carries a BOM and is identified by that,
// and a declaration written in an encoding that is not ASCII-compatible
// cannot be read without already knowing the encoding. The scan is bounded by
// the declaration's own terminator, so a file with no declaration costs a few
// bytes of comparison.
func xmlDeclEncoding(data []byte) string {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !bytes.HasPrefix(data, []byte("<?xml")) {
		return ""
	}
	end := bytes.Index(data, []byte("?>"))
	if end < 0 {
		return ""
	}
	decl := string(data[:end])
	i := strings.Index(decl, "encoding")
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(decl[i+len("encoding"):])
	if !strings.HasPrefix(rest, "=") {
		return ""
	}
	rest = strings.TrimSpace(rest[1:])
	if rest == "" {
		return ""
	}
	quote := rest[0]
	if quote != '\'' && quote != '"' {
		return ""
	}
	j := strings.IndexByte(rest[1:], quote)
	if j < 0 {
		return ""
	}
	return rest[1 : 1+j]
}

// ResolveInclude implements xdm.IncludeResolver, so that a document this
// resolver loads may pull in other resources through XInclude.
//
// Every constraint the rest of this type enforces applies here unchanged,
// because the path goes through the same resolvePath as fn:doc, xsl:include,
// fn:unparsed-text and external entities: a non-file scheme — http, https,
// ftp, anything — is rejected before the filesystem is touched, symlinks are
// resolved before the containment check, and a path outside every root is
// refused. There is deliberately nothing XInclude-specific about the
// confinement. A second gate written here would be a second thing to keep
// correct, and the first time the two drifted one of them would be the hole.
//
// XInclude is therefore no wider a primitive than fn:doc already is: it reads
// the same files, from the same roots, with the same refusals. What it adds is
// only *who asks* — an element in the source document rather than an
// expression in the stylesheet — and the source document is exactly the party
// this library's threat model already treats as hostile, which is why reusing
// the confinement is the whole answer rather than merely part of it.
//
// Unlike ResolveText there is no flag of its own guarding this. The gate sits
// one level up: nothing in this library performs XInclude processing unless
// the caller asks for it, and a caller that has asked has already named the
// roots. A second switch here would let a caller enable XInclude and then be
// quietly surprised that it did nothing.
//
// The returned URI is the file: URI of what was actually read, since that is
// what a relative reference inside the included resource resolves against and
// what XInclude's base URI fixup records.
func (r *FileResolver) ResolveInclude(href, base, encoding string) ([]byte, string, error) {
	path, err := r.resolvePath(href, base)
	if err != nil {
		return nil, "", err
	}
	data, err := r.readConfined(path)
	if err != nil {
		return nil, "", err
	}
	if encoding == "" {
		// An XML inclusion: hand the bytes over untouched and let the XML
		// parser read the declaration or the BOM, which is what XInclude 1.0
		// section 4.4 requires — the encoding attribute "is ignored" for
		// parse="xml". Decoding here would decode twice.
		return data, fileURIOf(path), nil
	}
	// A text inclusion. Section 4.4 defers to the same encoding rules
	// fn:unparsed-text follows, and this type already implements them once —
	// the XML-declaration override and the BOM strip included — so the decode
	// is shared rather than written a second time and left to drift.
	// decodeText also refuses an encoding it cannot decode exactly, which
	// matters more here than for unparsed-text: mis-decoded bytes become
	// *nodes* of the document, and silently wrong output is precisely what
	// this library declines to produce.
	text, err := decodeText(data, encoding, path)
	if err != nil {
		return nil, "", err
	}
	return []byte(text), fileURIOf(path), nil
}
