package xsd

import (
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"strings"
	"sync"
)

// A CatalogResolver answers schemaLocation from an in-memory table keyed by
// what a reference *means* rather than by how it is spelled.
//
// MapResolver already resolves from memory, but it matches a location
// literally, and the well-known schemas are referred to by many spellings.
// The XSD 1.1 schema for schemas alone is written as
// http://www.w3.org/TR/xmlschema11-1/XMLSchema.xsd, as
// http://www.w3.org/2001/XMLSchema.xsd, as a bare relative XMLSchema.xsd, and
// as an xs:import carrying the namespace and no location at all. A table
// keyed on the literal string answers one of those and misses the rest.
//
// So an entry is registered once, against a namespace and any number of
// aliases, and a lookup tries the location, then the location resolved against
// the base URI, then the namespace. Nothing is fetched and nothing is read
// from disk at resolve time: a reference to something not in the table is an
// error rather than a request, which is the property that makes this the
// resolver to reach for in a server.
//
// The zero value is not usable; call NewCatalogResolver.
type CatalogResolver struct {
	mu       sync.RWMutex
	byAlias  map[string]*catalogDoc
	byNS     map[string]*catalogDoc
	fallback Resolver
}

// A catalogDoc is one bundled document and the name resolution reports for it.
//
// The name matters as much as the bytes. The assembler keys the documents it
// has read on the location the resolver returned, so a catalog answering with
// whatever spelling the reference happened to use would make two documents of
// one file the moment a schema set reached it two ways. The XSLT 3.0 schema
// does that immediately: it imports the xml: namespace with no location at
// all, and the schema for schemas it also imports names
// http://www.w3.org/2001/xml.xsd for the same document -- which without this
// produced "duplicate attribute declaration space" and three more like it.
// Reporting one canonical name per entry, whatever spelling found it, is what
// makes those the same document again.
type catalogDoc struct {
	src  []byte
	name string
}

// NewCatalogResolver returns an empty catalog.
func NewCatalogResolver() *CatalogResolver {
	return &CatalogResolver{
		byAlias: map[string]*catalogDoc{},
		byNS:    map[string]*catalogDoc{},
	}
}

// Add registers one schema document under a target namespace and any number of
// location aliases.
//
// The namespace may be empty, for a document that is only ever named by
// location. An alias may be an absolute URI or a bare relative reference; both
// are matched, and a relative one also matches wherever it lands after being
// resolved against the referring document's base URI.
//
// Registering the same alias twice replaces it, so a caller may override a
// bundled entry with its own copy.
func (r *CatalogResolver) Add(namespace string, src []byte, aliases ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// The canonical name is the first absolute alias, falling back to the
	// first alias of any kind and then to the namespace. Every lookup that
	// finds this entry reports that one name, so the assembler sees one
	// document however the reference was spelled.
	doc := &catalogDoc{src: src}
	for _, a := range aliases {
		if strings.Contains(a, ":") {
			doc.name = a
			break
		}
	}
	if doc.name == "" && len(aliases) > 0 {
		doc.name = aliases[0]
	}
	if doc.name == "" {
		doc.name = namespace
	}

	if namespace != "" {
		r.byNS[namespace] = doc
	}
	for _, a := range aliases {
		r.byAlias[a] = doc
		// A reference is resolved against its base before it reaches a
		// resolver, so an absolute alias is also matched by its last
		// path segment -- which is how a relative "XMLSchema.xsd"
		// beside the referring document finds the canonical entry.
		if seg := lastSegment(a); seg != "" && seg != a {
			if _, taken := r.byAlias[seg]; !taken {
				r.byAlias[seg] = doc
			}
		}
	}
}

// SetFallback names a resolver to consult when the catalog has no entry.
//
// Nil, the default, makes a miss an error, which is what a server wants. A
// command-line tool that should still read a schema beside the one it was
// given sets a FileResolver here; one that may reach the network sets an
// HTTPResolver, and thereby says so deliberately.
func (r *CatalogResolver) SetFallback(f Resolver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallback = f
}

// Resolve implements Resolver.
func (r *CatalogResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	r.mu.RLock()
	if doc, ok := r.lookup(namespace, location, base); ok {
		r.mu.RUnlock()
		return io.NopCloser(strings.NewReader(string(doc.src))), doc.name, nil
	}
	f := r.fallback
	r.mu.RUnlock()

	if f != nil {
		return f.Resolve(namespace, location, base)
	}
	// An include with no location is not an error (§4.2.1) and the caller
	// distinguishes it, so say nothing rather than inventing a failure.
	if location == "" && namespace == "" {
		return nil, "", nil
	}
	what := location
	if what == "" {
		what = "namespace " + namespace
	}
	return nil, "", fmt.Errorf(
		"no catalog entry for %s, and no fallback resolver is set", what)
}

// lookup runs the three lookups in order of how specific they are. The caller
// holds at least a read lock.
func (r *CatalogResolver) lookup(namespace, location, base string) (*catalogDoc, bool) {
	if location != "" {
		if doc, ok := r.byAlias[location]; ok {
			return doc, true
		}
		if abs := resolveAgainst(base, location); abs != "" && abs != location {
			if doc, ok := r.byAlias[abs]; ok {
				return doc, true
			}
		}
		if seg := lastSegment(location); seg != "" && seg != location {
			if doc, ok := r.byAlias[seg]; ok {
				return doc, true
			}
		}
	}
	// The namespace is tried last because a location, where one is given,
	// says which document is wanted and the namespace only says what it
	// should define. An xs:import naming a namespace and no location has
	// nothing else to go on, which is the case this exists for.
	if namespace != "" {
		if doc, ok := r.byNS[namespace]; ok {
			return doc, true
		}
	}
	return nil, false
}

// resolveAgainst resolves a reference against a base URI, returning "" when
// either is unusable. It is deliberately quiet: a base that is not a URI is a
// reason to fall through to the other lookups, not to fail the resolve.
func resolveAgainst(base, ref string) string {
	if base == "" {
		return ""
	}
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return b.ResolveReference(u).String()
}

// lastSegment returns the final path segment of a URI reference.
func lastSegment(s string) string {
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	return path.Base(s)
}

// AddFromFS registers every file named by aliases from an fs.FS, mapping each
// to the namespace given for it.
//
// It is the bridge from a directory of schemas a caller already has -- checked
// in, embedded with go:embed, or unpacked at startup -- to a catalog, without
// this package needing to carry the documents itself.
//
// entries maps a file path within fsys to the target namespace of the schema
// in it and the aliases it should answer to. A file that is missing is an
// error naming it, because a catalog that silently has fewer entries than the
// caller asked for fails later and somewhere less obvious.
func (r *CatalogResolver) AddFromFS(fsys fs.FS, entries []CatalogEntry) error {
	for _, e := range entries {
		src, err := fs.ReadFile(fsys, e.Path)
		if err != nil {
			return fmt.Errorf("catalog entry %q: %w", e.Path, err)
		}
		r.Add(e.Namespace, src, e.Aliases...)
	}
	return nil
}

// A CatalogEntry names one schema document to load into a catalog: where to
// read it from, what namespace it defines, and the locations it answers to.
type CatalogEntry struct {
	// Path locates the document within the fs.FS.
	Path string
	// Namespace is the schema's target namespace, or empty for a document
	// only ever named by location.
	Namespace string
	// Aliases are the schemaLocation spellings this document answers.
	Aliases []string
}

// W3CEntries describes the schemas that the well-known W3C vocabularies are
// referred to by, with every spelling this package has seen in the wild.
//
// It carries no schema content. A caller supplies the documents -- from a
// checkout, a go:embed, or the companion module -- and this says what to
// register them as, so that the aliasing is stated once here rather than
// rediscovered by everyone who needs it.
//
// The paths are the conventional file names; a caller whose files are named
// differently can copy this and edit it, since it is data rather than
// behaviour.
func W3CEntries() []CatalogEntry {
	return []CatalogEntry{
		{
			Path:      "XMLSchema.xsd",
			Namespace: NSSchema,
			Aliases: []string{
				// The XSD 1.1 schema for schemas. The XSLT 3.0 schema
				// imports the first of these; the W3C's own copy in
				// the XSLT test suite was edited to the relative form
				// in 2021, the comment there giving the reason as
				// "W3C web site throttling".
				"http://www.w3.org/TR/xmlschema11-1/XMLSchema.xsd",
				"http://www.w3.org/2009/XMLSchema/XMLSchema.xsd",
				"http://www.w3.org/2001/XMLSchema.xsd",
				"XMLSchema.xsd",
			},
		},
		{
			Path:      "xml.xsd",
			Namespace: NSXML,
			Aliases: []string{
				"http://www.w3.org/2001/xml.xsd",
				"http://www.w3.org/2009/01/xml.xsd",
				"http://www.w3.org/XML/1998/namespace.xsd",
				"xml.xsd",
			},
		},
	}
}
