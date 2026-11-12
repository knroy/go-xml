package xslt

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// readDocResolver records the absolute URI of every document the
// transformation reads, so that xsl:result-document can refuse to write over
// one of them.
//
// XTDE1500: "It is a dynamic error for a stylesheet to write to an external
// resource and read from the same resource during a single transformation, if
// the same absolute URI is used to access the resource in both cases."
// Detecting that needs a record of the reads, and only the resolver sees them
// -- by the time a node reaches the stylesheet it is a tree, and the URI it
// came from is not a property the instruction that writes can ask about.
//
// The URI recorded is the one the loaded tree reports as its document URI, not
// the argument fn:doc was given: the spec's test is on the ABSOLUTE URI, and
// the argument is usually relative. error-1500a turns on exactly that -- it
// reads doc('error-1500a.xml') and writes to document-uri($a), which is the
// absolute form of the same thing.
type readDocResolver struct {
	inner xpath.DocumentResolver
	// written is the set of URIs xsl:result-document has produced, shared
	// with the runtime for the same reason read is. It is consulted rather
	// than added to here: this resolver is the read side of XTDE1500.
	written map[string]bool
	// read is shared with the runtime rather than copied, because the
	// resolver is installed once per transform and the runtime is copied on
	// every focus change. A document read inside a template has to be
	// visible to an xsl:result-document evaluated anywhere else.
	read map[string]bool
	// docs remembers each successful fn:doc / fn:document answer by its
	// arguments, so that a stylesheet calling doc() per node does not resolve
	// the path again every time (filepath.Abs and EvalSymlinks on the file
	// and on every root). Only over a FileResolver, whose answer for one path
	// is already fixed for the transform by its own cache: the confinement
	// check runs on the first call, and a later one returns the tree that
	// check admitted. Nil disables it.
	docs map[docKey]*xdm.Tree
}

// docKey is one fn:doc call's arguments, and the package it is written in,
// which decides the whitespace stripping applied (see stripSpaceResolver).
type docKey struct {
	uri, base string
	pkg       int
}

func (r *readDocResolver) record(t *xdm.Tree) {
	if t == nil || t.Root == nil {
		return
	}
	if u := t.Root.DocumentURI(); u != "" {
		addDoc(r.read, u)
	}
}

func (r *readDocResolver) ResolveDocument(uri, base string) (*xdm.Tree, error) {
	return r.cached(docKey{uri, base, 0}, func() (*xdm.Tree, error) {
		return r.inner.ResolveDocument(uri, base)
	})
}

// cached answers k from docs, or through load, which is remembered when it
// succeeds. A remembered tree needs no XTDE1500 check: it was recorded as
// read when it was loaded, so a write to it after that is refused by
// checkReadThenWrite, and one before it refused the load.
func (r *readDocResolver) cached(k docKey, load func() (*xdm.Tree, error)) (*xdm.Tree, error) {
	if t, ok := r.docs[k]; ok {
		return t, nil
	}
	t, err := load()
	r.record(t)
	if err == nil {
		if werr := r.checkWrittenThenRead(t); werr != nil {
			return nil, werr
		}
		if _, werr := r.markRead(k.uri, k.base, ""); werr != nil {
			return nil, werr
		}
		if r.docs != nil {
			r.docs[k] = t
		}
	}
	return t, err
}

// checkWrittenThenRead is XTDE1500 in the direction the write side cannot
// see: a resource this transformation has already produced with
// xsl:result-document is then read back.
//
// The error is symmetric in the spec -- "write to an external resource and
// read from the same resource during a single transformation, if the same
// absolute URI is used to access the resource in both cases" -- so it is
// neither read-then-write nor write-then-read but both. checkReadThenWrite in
// this file is the other half; between them the pair no longer depends on
// which instruction the stylesheet happens to reach first.
func (r *readDocResolver) checkWrittenThenRead(t *xdm.Tree) error {
	if t == nil || t.Root == nil || r.written == nil {
		return nil
	}
	u := t.Root.DocumentURI()
	if u == "" || !hasDoc(r.written, u) {
		return nil
	}
	return fmt.Errorf(
		"XTDE1500: this transformation reads %q, which xsl:result-document "+
			"has already written", u)
}

// markRead records a read of uri against base under doc, the document URI
// the resolver names it by, and under the absolute URI it was asked for, and
// is XTDE1500 if either was written. It returns what takes back the URIs it
// added, for a streamed read that then fails.
//
// FileResolver names a document by the real path it admitted, while an href
// resolves against its base as written: under a symbolically linked
// directory (macOS's /var, t.TempDir) the two spell one file differently.
// The URI as asked for is the spelling a write against the same base meets,
// so recording both lets them meet without the write side touching the
// filesystem (docIdentity).
func (r *readDocResolver) markRead(uri, base, doc string) (func(), error) {
	// Only a file: URI: the two spellings are a filesystem's, and a request
	// left relative (no usable base) is not an absolute URI to compare.
	asked := resolveAgainst(base, uri)
	if !strings.HasPrefix(asked, "file:") {
		asked = ""
	}
	var added []string
	for _, u := range []string{doc, asked} {
		if u == "" {
			continue
		}
		if !hasDoc(r.read, u) {
			added = append(added, u)
		}
		addDoc(r.read, u)
		if hasDoc(r.written, u) {
			return func() {}, fmt.Errorf(
				"XTDE1500: this transformation reads %q, which xsl:result-document "+
					"has already written", u)
		}
	}
	return func() {
		for _, u := range added {
			delDoc(r.read, u)
		}
	}, nil
}

// ResolveDocumentIn implements xpath.ContextDocumentResolver, so that wrapping
// a resolver that strips whitespace per package does not lose that behaviour.
// A resolver that does not implement the richer interface is still reached
// through ResolveDocument, which is what the fallback below does.
func (r *readDocResolver) ResolveDocumentIn(
	ctx *xpath.Context, uri, base string) (*xdm.Tree, error) {

	if cr, ok := r.inner.(xpath.ContextDocumentResolver); ok {
		return r.cached(docKey{uri, base, packageOf(ctx)}, func() (*xdm.Tree, error) {
			return resolveDocIn(cr, ctx, uri, base)
		})
	}
	return r.ResolveDocument(uri, base)
}

// NativeResolver implements xpathleaf.NativeResolver.
func (r *readDocResolver) NativeResolver() {}

// checkReadThenWrite is XTDE1500 for one xsl:result-document destination.
//
// Only a URI that was actually read counts. A resource the transformation
// merely could have read is not an error to write, and the spec's "if the same
// absolute URI is used to access the resource in both cases" makes the test a
// comparison of absolute URIs. One file: URI has two spellings, though, when
// a directory on its path is a symbolic link: FileResolver records what it
// reads under the real path, while an href resolves against a base URI as
// written (t.TempDir on macOS is under /var, a link to /private/var). So a
// read is recorded under the URI it was asked for as well (markRead).
func checkReadThenWrite(rt *runtime, href string) error {
	if href == "" || rt.readDocs == nil || !hasDoc(*rt.readDocs, href) {
		return nil
	}
	return fmt.Errorf(
		"XTDE1500: xsl:result-document writes to %q, which this "+
			"transformation has already read", href)
}

// docIdentity is u as the read and written sets compare it: a file: URI with
// its path cleaned; any other URI is itself. It is lexical on purpose. An
// href is the stylesheet's or the document's choice, and resolving its
// symbolic links ran lstat and readlink on any host path with no file access
// granted -- an oracle for whether two paths name one file, and on Windows a
// UNC path reaches the network. The symbolic-link spelling a read can differ
// by is recorded at read time instead (markRead).
func docIdentity(u string) string {
	if !strings.HasPrefix(u, "file:") {
		return u
	}
	p := fileURIToPath(u)
	if !filepath.IsAbs(p) {
		return u
	}
	return fileURIOf(filepath.Clean(p))
}

// addDoc records u in set, as written and by its identity; hasDoc and delDoc
// are its lookup and removal. The spelling as written keeps the exact
// comparison XTDE1490 makes (checkResultURI).
//
// ponytail: delDoc drops the identity even when another spelling of the same
// file was recorded too; only the re-run of a streamed container and an
// unread parse failure remove, and each re-records what it then reads.
func addDoc(set map[string]bool, u string) {
	set[u] = true
	set[docIdentity(u)] = true
}

func hasDoc(set map[string]bool, u string) bool {
	return set[u] || set[docIdentity(u)]
}

func delDoc(set map[string]bool, u string) {
	delete(set, u)
	delete(set, docIdentity(u))
}
