package dtd

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// FileResolver reads an external subset from the filesystem, confined to a
// directory.
//
// It is off by default in the only sense that matters: LoadOptions.Resolver is
// nil unless a caller sets it, so nothing here runs for a document that merely
// arrived. Supplying one hands control of what this process reads to whoever
// wrote the DOCTYPE, which is why Root is not optional in practice — see the
// note on it. This mirrors xsd.FileResolver and xslt.FileResolver rather than
// reusing either, because dtd sits beneath both and importing one would invert
// the dependency.
type FileResolver struct {
	// Root confines every read. A system identifier that resolves outside
	// it, whether by "..", by an absolute path, or through a symlink, is
	// refused before the file is opened.
	//
	// An empty Root means the process's working directory, which is almost
	// never what a caller wants for an untrusted document — set it.
	Root string

	// MaxBytes bounds one file this resolver reads. Zero means
	// DefaultMaxResolverBytes; a negative value means no limit, for a DTD
	// the caller produced itself.
	//
	// It is a second bound rather than a duplicate of
	// LoadOptions.MaxExternalBytes: that one bounds the whole load and is
	// applied after the read, while this bounds what one call puts in memory
	// at all. The loader's LimitReader already caps the read, so this exists
	// for callers who use the resolver directly.
	MaxBytes int64
}

// DefaultMaxResolverBytes bounds one file a FileResolver reads when MaxBytes
// is zero: 4 MB, matching DefaultMaxExternalBytes so that neither limit is
// silently the tighter one.
const DefaultMaxResolverBytes = 4 << 20

// ResolveExternal implements Resolver.
//
// The returned URI is the file: URI of what was actually read, because that is
// what a parameter-entity module inside the fetched text resolves against —
// XML 1.0 §4.4.3.
func (r *FileResolver) ResolveExternal(systemID, publicID, base string) (io.ReadCloser, string, error) {
	root, rel, err := r.resolvePath(systemID, base)
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(root, rel)
	max := r.MaxBytes
	if max == 0 {
		max = DefaultMaxResolverBytes
	}
	// Opened through os.Root, so containment is enforced by the kernel at the
	// moment of the open rather than by the string comparison resolvePath took
	// beforehand. A symlink swapped in between the two — which resolvePath
	// cannot see, having already returned — is refused here instead of
	// followed. resolvePath keeps the earlier check because it decides WHICH
	// path is being named and produces the error that names Root; this is the
	// enforcement. Matches xslt/resolver.go readConfined and xsd.FileResolver.
	rt, err := os.OpenRoot(root)
	if err != nil {
		return nil, "", err
	}
	f, err := rt.Open(filepath.ToSlash(rel))
	rt.Close()
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	var data []byte
	if max < 0 {
		data, err = io.ReadAll(f)
	} else {
		// One byte over, so that a file exactly at the limit is
		// distinguishable from one over it: truncating silently would hand
		// back a partial DTD, which is the failure this package refuses.
		data, err = io.ReadAll(io.LimitReader(f, max+1))
		if err == nil && int64(len(data)) > max {
			return nil, "", fmt.Errorf(
				"%s is larger than the %d byte limit (FileResolver.MaxBytes)",
				path, max)
		}
	}
	if err != nil {
		return nil, "", err
	}
	// Read fully rather than handing back the open file: the loader charges
	// the bytes against its budget, and a resolver that streamed would leave
	// a descriptor open for the length of a load that may fail.
	return io.NopCloser(bytes.NewReader(data)), fileURIOf(path), nil
}

// resolvePath turns a system identifier into a root and a path relative to it,
// which the caller opens through os.Root rather than by name.
//
// A system identifier is a URI, not a path — XML 1.0 §4.2.2 — so it is parsed
// as one before the filesystem sees it. That is what makes "sub/mod.ent" work
// the same on Windows as on Unix, and what makes "file:///C:/dtd/r.dtd" name
// drive C rather than a host called "C:".
func (r *FileResolver) resolvePath(systemID, base string) (root, rel string, err error) {
	if systemID == "" {
		return "", "", fmt.Errorf("empty system identifier")
	}
	// Refuse a non-file scheme before touching the filesystem, so http://
	// produces a clear refusal rather than a confusing "no such file". This
	// is the SSRF gate: this type has no network and must never look as
	// though it might.
	if u, err := url.Parse(systemID); err == nil && u.Scheme != "" && u.Scheme != "file" {
		return "", "", fmt.Errorf(
			"scheme %q is not permitted (this resolver reads local files only)",
			u.Scheme)
	}
	p := fileURIToPath(systemID)
	// A fragment selects within a resource rather than naming another one,
	// and is no part of any filename.
	if i := strings.IndexByte(p, '#'); i >= 0 {
		p = p[:i]
	}
	if !filepath.IsAbs(p) && base != "" {
		p = filepath.Join(filepath.Dir(fileURIToPath(base)), p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", "", err
	}
	// The parent is resolved, the final component deliberately is not: the
	// caller opens through os.Root, which resolves that component against the
	// root's descriptor at open time. Resolving it here would follow the link
	// first and hand os.Root a path with nothing left to refuse — the check
	// would pass and the escape would already have happened. The parent is
	// resolved only so both sides compare alike, since on macOS /var is itself
	// a link to /private/var.
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		abs = filepath.Join(dir, filepath.Base(abs))
	}
	root, err = filepath.Abs(r.Root)
	if err != nil {
		return "", "", err
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	rel, err = filepath.Rel(root, abs)
	if err != nil || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%q is outside the permitted directory %q", abs, root)
	}
	return root, rel, nil
}

// fileURIToPath turns a file: URI into a filesystem path, leaving anything
// that is not one alone.
//
// The Windows case is the whole reason this is not a TrimPrefix. A Windows
// path arrives as "/C:/dtd/r.dtd" — an empty authority followed by a
// drive-letter path — and the leading slash belongs to the URI, not to the
// path. "/home/u/r.dtd" keeps its own. RFC 8089.
func fileURIToPath(s string) string {
	if !strings.HasPrefix(s, "file:") {
		// A bare path on Windows may still be written with backslashes, and
		// url.Parse would read one as an escape-free path character. Leaving
		// it to filepath is correct on both platforms.
		return filepath.FromSlash(s)
	}
	u, err := url.Parse(s)
	if err != nil {
		return filepath.FromSlash(strings.TrimPrefix(s, "file://"))
	}
	p := u.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// fileURIOf spells an absolute filesystem path as a file: URI.
//
// The three-slash form is not cosmetic: "file://" + "C:/dtd/r.dtd" makes "C:"
// the AUTHORITY rather than the drive, and parsing it back yields host "C:"
// with the drive letter gone, so every module resolved against it names a file
// that is not there. RFC 8089 gives a local path an empty authority.
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
	// url.URL does the escaping, so a directory containing a space survives
	// the round trip.
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}

// MapResolver resolves from an in-memory table, for callers that know every
// resource in advance — a build tool with its DTD modules embedded, or a test.
//
// It reads nothing, so it is the one resolver that is safe to hand an
// untrusted document without further thought: a system identifier that is not
// a key is refused rather than searched for.
type MapResolver struct {
	// Docs maps a system identifier to its text.
	Docs map[string]string
}

// ResolveExternal implements Resolver.
//
// Lookup is by the identifier as written and then by its last path segment, so
// that a module referenced as "ent/iso-lat1.ent" from inside a subset found at
// "dtd/docbook.dtd" is found under either spelling. There is no filesystem
// here, so neither form can escape anywhere.
func (r *MapResolver) ResolveExternal(systemID, publicID, base string) (io.ReadCloser, string, error) {
	if src, ok := r.Docs[systemID]; ok {
		return io.NopCloser(strings.NewReader(src)), systemID, nil
	}
	if i := strings.LastIndexByte(systemID, '/'); i >= 0 {
		if src, ok := r.Docs[systemID[i+1:]]; ok {
			return io.NopCloser(strings.NewReader(src)), systemID, nil
		}
	}
	if publicID != "" {
		if src, ok := r.Docs[publicID]; ok {
			return io.NopCloser(strings.NewReader(src)), systemID, nil
		}
	}
	return nil, "", fmt.Errorf("no entry for %q", systemID)
}
