package xslt

import (
	"fmt"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
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
	// read is shared with the runtime rather than copied, because the
	// resolver is installed once per transform and the runtime is copied on
	// every focus change. A document read inside a template has to be
	// visible to an xsl:result-document evaluated anywhere else.
	read map[string]bool
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
	t, err := r.inner.ResolveDocument(uri, base)
	r.record(t)
	return t, err
}

// ResolveDocumentIn implements xpath.ContextDocumentResolver, so that wrapping
// a resolver that strips whitespace per package does not lose that behaviour.
// A resolver that does not implement the richer interface is still reached
// through ResolveDocument, which is what the fallback below does.
func (r *readDocResolver) ResolveDocumentIn(
	ctx *xpath.Context, uri, base string) (*xdm.Tree, error) {

	if cr, ok := r.inner.(xpath.ContextDocumentResolver); ok {
		t, err := cr.ResolveDocumentIn(ctx, uri, base)
		r.record(t)
		return t, err
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
