package xslt

import (
	"fmt"

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
	if u := t.Root.DocumentURI; u != "" {
		r.read[u] = true
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
	u := t.Root.DocumentURI
	if u == "" || !r.written[u] {
		return nil
	}
	return fmt.Errorf(
		"XTDE1500: this transformation reads %q, which xsl:result-document "+
			"has already written", u)
}

// ResolveDocumentIn implements xpath.ContextDocumentResolver, so that wrapping
// a resolver that strips whitespace per package does not lose that behaviour.
// A resolver that does not implement the richer interface is still reached
// through ResolveDocument, which is what the fallback below does.
func (r *readDocResolver) ResolveDocumentIn(
	ctx *xpath.Context, uri, base string) (*xdm.Tree, error) {

	if cr, ok := r.inner.(xpath.ContextDocumentResolver); ok {
		return r.cached(docKey{uri, base, packageOf(ctx)}, func() (*xdm.Tree, error) {
			return cr.ResolveDocumentIn(ctx, uri, base)
		})
	}
	return r.ResolveDocument(uri, base)
}

// checkReadThenWrite is XTDE1500 for one xsl:result-document destination.
//
// Only a URI that was actually read counts. A resource the transformation
// merely could have read is not an error to write, and the spec's "if the same
// absolute URI is used to access the resource in both cases" makes the test an
// exact string comparison of absolute URIs rather than anything about the
// underlying file. The paragraph after the error allows a processor to go
// further and detect two URIs naming one physical resource; this one does not,
// because the filesystem question it would have to ask is not one this package
// is in a position to answer portably.
func checkReadThenWrite(rt *runtime, href string) error {
	if href == "" || rt.readDocs == nil || !(*rt.readDocs)[href] {
		return nil
	}
	return fmt.Errorf(
		"XTDE1500: xsl:result-document writes to %q, which this "+
			"transformation has already read", href)
}
