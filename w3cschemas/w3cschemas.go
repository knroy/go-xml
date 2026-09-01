// Package w3cschemas bundles the W3C schemas that schemas in the wild refer
// to by URL, so that loading one does not depend on reaching w3.org.
//
// It exists because those references are published as absolute http:// URLs
// and fetching them is unreliable by design: the W3C throttles the requests.
// The XSLT 3.0 schema imports the XSD 1.1 schema for schemas that way, and the
// W3C's own copy in the XSLT test suite was edited in 2021 to a relative path,
// the comment there giving the reason as "W3C web site throttling". Anything
// that resolves such a reference over the network inherits that unreliability;
// a catalog does not.
//
// This is a separate module from github.com/knroy/go-xml so that the schemas,
// which are W3C documents under W3C terms, stay out of an MIT-licensed module.
// Depending on it is an explicit choice. See NOTICE for what is bundled, where
// each file came from, and under what terms.
//
//	r, err := w3cschemas.Catalog()
//	if err != nil { ... }
//	s, err := xsd.Load(root, base, xsd.Options{
//		Version:  xsd.Version11,
//		Resolver: r,
//	})
//
// Catalog returns a resolver that answers only what is bundled: a reference to
// anything else is an error rather than a request. To also read schemas beside
// the one being loaded, set a fallback:
//
//	r.SetFallback(&xsd.FileResolver{Root: "schemas"})
package w3cschemas

import (
	"embed"
	"io/fs"

	"github.com/knroy/go-xml/xsd"
)

//go:embed schemas/*.xsd
var files embed.FS

// FS returns the bundled schema documents, rooted so that each is at its
// conventional file name -- XMLSchema.xsd, xml.xsd.
//
// It is exported for a caller assembling a catalog of its own, or overriding
// one entry while keeping the rest.
func FS() fs.FS {
	sub, err := fs.Sub(files, "schemas")
	if err != nil {
		// The embed pattern is a compile-time constant and the
		// directory is in this module, so this cannot fail at runtime.
		panic(err)
	}
	return sub
}

// Catalog returns a resolver holding every bundled schema, registered under
// its target namespace and every schemaLocation spelling it is referred to by.
//
// Nothing is fetched and nothing is read from disk: a reference to a schema
// that is not bundled is an error rather than a request. A caller that wants
// otherwise sets a fallback on the returned resolver.
func Catalog() (*xsd.CatalogResolver, error) {
	r := xsd.NewCatalogResolver()
	if err := r.AddFromFS(FS(), xsd.W3CEntries()); err != nil {
		return nil, err
	}
	return r, nil
}
