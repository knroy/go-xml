package xquery

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// Schema import is XQuery 3.1 §4.11. A schema import adds the components of
// one target namespace to the *in-scope schema definitions* of the static
// context, which is what "validate", "instance of my:type", "element(*,
// my:type)", "schema-element(my:e)" and "castable as my:t" are all judged
// against.
//
// Three properties of the specification shape what follows, and none of them
// is visible in the grammar.
//
// The first is that the "at" locations are HINTS, exactly as they are for a
// module import. §4.11: "the location URIs ... are hints". The TARGET
// NAMESPACE identifies what is being imported, so the store is keyed by
// namespace, a registered schema wins over a location, and a processor that
// cannot fetch a location is still conforming.
//
// The second is that the components must reach the static context BEFORE the
// query body is parsed. This is the property that made the feature look large
// and is in fact what makes it small: XQuery resolves type names while
// parsing, not after, so "8 cast as hat:hatsize" is decided by whether the
// static context knows that name at the moment the parser reads it. The
// imports are therefore followed at the end of the prolog and before the
// body, which is where parseProlog already sits. Following them where the
// MODULE loader runs -- after the body, because a module contributes
// FUNCTIONS and VARIABLES, which are resolved late -- would leave every type
// name in the body XPST0051 against a schema that had in fact been imported.
//
// The third is that the components are reached through xpath.SchemaTypes,
// which already exists and which the xslt package already implements over an
// *xsd.Schema for xsl:import-schema. That interface is the whole of the "PSVI
// plumbing between xsd and xquery" the design notes budgeted for: the seam is
// already cut, on the xpath side, because xsd cannot import xpath (a schema's
// assertions and selectors are XPath expressions, so the dependency runs the
// other way). What this file adds is the loader and the store; the lookups
// themselves are a few lines over the schema's own tables, on staticContext.

// A SchemaResolver locates the source of a schema document.
//
// It is xsd.Resolver rather than a new interface of this package's own. The
// two would have had identical shapes, and a caller that already holds a
// resolver for xsd.Load or for xslt's SchemaResolver would have had to wrap it
// to pass it here. Sharing the type also means an imported schema's own
// xs:include and xs:import are followed by the SAME resolver the query's
// import was granted, rather than by a second one that could disagree about
// what this process may read.
type SchemaResolver = xsd.Resolver

// errNoSchemaResolver marks a refusal as a configuration fault rather than a
// schema that was looked for and not found.
//
// It is the counterpart of errNoModuleResolver, and exists for the same
// reason: "you did not configure a resolver" and "your schema store has no
// such namespace" are different facts about the host, and only the first is
// something the host can fix. The XQST0059 the caller sees is the same either
// way, because that is the code §4.11 gives for a schema import that cannot be
// satisfied.
var errNoSchemaResolver = errors.New("no SchemaResolver is configured")

// noSchemaResolver is the default, and it fetches nothing.
//
// This is the house pattern and it is load-bearing rather than tidy. A query
// is untrusted input and an "at" location is a string the query's author
// chose, so defaulting to a file resolver would mean that compiling a query
// grants its author the run of the filesystem -- the same reach xsd.Load
// closed off when it stopped defaulting to a rooted FileResolver, and the same
// one Options.ModuleResolver refuses by default. With no resolver configured
// an "at" location is NEVER opened: not tried and failed, not opened, so a
// missing file and a present one are indistinguishable from outside.
type noSchemaResolver struct{}

// Resolve implements xsd.Resolver by refusing.
func (noSchemaResolver) Resolve(namespace, location, base string) (
	io.ReadCloser, string, error) {
	return nil, "", fmt.Errorf(
		"no schema for namespace %q: %w (Options.SchemaResolver); register "+
			"the schema in Options.Schemas, or pass a resolver to say what "+
			"this query may read", namespace, errNoSchemaResolver)
}

// DefaultMaxSchemaBytes bounds the total schema source text one compilation
// may read when Options.MaxSchemaBytes is zero.
//
// A schema document may include and import further schema documents, so the
// graph is attacker-shaped in exactly the way a module graph and an xs:include
// graph are, and the bound is the same one and for the same reason:
// DefaultMaxModuleBytes, at the same value. The count of documents reached
// through a schema's OWN references is bounded separately, by xsd's own
// limits, which the assembler applies as it follows them -- so this budget
// bounds the bytes the QUERY's own imports pull in, cumulatively across the
// compilation rather than per import, because a budget spent one import at a
// time is not spent at all.
const DefaultMaxSchemaBytes = 16 << 20

// A Schema is a schema registered with the compilation directly, rather than
// fetched through a resolver.
//
// This is the store §4.11 leaves to the implementation, and it is the only way
// to import a schema without granting the query any reach at all: the caller
// supplies the components, so nothing is opened and nothing is fetched. It is
// also what an import with no "at" clause resolves against, since such an
// import names a namespace and nothing else.
type Schema struct {
	// Namespace is the target namespace this schema supplies. An import of
	// this namespace resolves to this entry.
	//
	// It is matched against the import's target namespace rather than against
	// the schema's own targetNamespace attribute: a caller registering a
	// Schema has said which namespace it answers for, and Components is
	// already-assembled rather than a document to re-read.
	Namespace string

	// Components is the assembled schema. A caller that has one from
	// xsd.Load, xsd.LoadFile or xslt's Stylesheet.Schema can register it
	// directly, which is what makes a host able to share one schema between a
	// stylesheet and a query without loading it twice and risking the two
	// disagreeing.
	//
	// It may be nil, which registers the namespace as known and empty. That
	// is not a useless entry: §4.11 makes importing a namespace legal whether
	// or not the processor has components for it, and a nil entry is how a
	// host says "this namespace is expected and I have nothing for it"
	// without the import failing.
	Components *xsd.Schema

	// Source is schema document text to load, used when Components is nil.
	// It is the convenient half of the store: a caller with a schema document
	// in a string need not call xsd.Load itself.
	Source string

	// BaseURI is what Source's own relative references resolve against. It
	// may be empty.
	BaseURI string
}

// schemaImport is one "import schema" declaration, as written.
type schemaImport struct {
	// ns is the target namespace, which may legitimately be empty: an import
	// of no namespace asks for the components that are in no namespace, and
	// is written "import schema default element namespace ''" or with no
	// prefix at all. A PREFIX bound to an empty namespace is XQST0057 and is
	// refused by checkImportSyntax before this is built.
	ns string
	// hints are the "at" locations, in the order written.
	hints []string
}

// schemaLoader carries the state of one compilation's schema loading.
//
// It is per-compilation rather than per-import because the budget is: a query
// importing three schemas has spent one allowance, and an allowance reset per
// import would bound nothing.
type schemaLoader struct {
	opts Options
	// merged is the single schema every import folds into. §2.1.1 makes the
	// in-scope schema definitions ONE set rather than one per import, so two
	// imports of different namespaces contribute to the same table and a name
	// is looked up once.
	merged *xsd.Schema
	// bytes is the source budget spent so far.
	bytes int64
	// registered is Options.Schemas keyed by namespace, consulted before the
	// resolver because a schema the host supplied needs no reach at all.
	registered map[string]Schema
}

// newSchemaLoader prepares a loader, applying the defaults for the bound and
// for the resolver.
func newSchemaLoader(opts Options) *schemaLoader {
	if opts.SchemaResolver == nil {
		// Closed by default. See noSchemaResolver: this is the difference
		// between "a query may name a schema the host registered" and "a
		// query may read a file of its choosing".
		opts.SchemaResolver = noSchemaResolver{}
	}
	if opts.MaxSchemaBytes == 0 {
		opts.MaxSchemaBytes = DefaultMaxSchemaBytes
	}
	l := &schemaLoader{opts: opts, registered: map[string]Schema{}}
	for _, s := range opts.Schemas {
		// A namespace registered twice keeps the first. §4.11 forbids the
		// QUERY from importing one namespace twice (XQST0058); two
		// registrations of one namespace is the host's business, and the
		// first is what an import of it resolves to.
		if _, seen := l.registered[s.Namespace]; !seen {
			l.registered[s.Namespace] = s
		}
	}
	return l
}

// load follows one "import schema" declaration, folding what it finds into the
// compilation's merged schema.
func (l *schemaLoader) load(imp schemaImport, base string) error {
	if l.merged == nil {
		// NewSchema is not an empty schema: it carries the built-in types and
		// the built-in declarations for xml:lang, xml:space, xml:base and
		// xml:id. That is exactly what an import of the XML namespace with no
		// location asks a processor to supply (XSD Part 1 §F.1), and it is
		// why an import that resolves to nothing still leaves a schema
		// behind rather than nil.
		l.merged = xsd.NewSchema()
	}

	// The store is consulted first and the resolver second, so that a host
	// can shadow any location a query might name for a namespace.
	if reg, ok := l.registered[imp.ns]; ok {
		if reg.Components != nil {
			mergeXSDSchema(l.merged, reg.Components)
			return nil
		}
		if reg.Source == "" {
			// Registered as known and empty. See Schema.Components.
			return nil
		}
		if err := l.spend(int64(len(reg.Source)), imp.ns); err != nil {
			return err
		}
		baseURI := reg.BaseURI
		if baseURI == "" {
			baseURI = base
		}
		loaded, err := l.parse(strings.NewReader(reg.Source), baseURI, imp.ns)
		if err != nil {
			return err
		}
		mergeXSDSchema(l.merged, loaded)
		return nil
	}

	// §4.11 hands the resolver the target namespace and the location hints.
	// The hints are tried in the order written, and the namespace alone is
	// tried when there are none -- a resolver backed by a catalogue can
	// answer from the namespace, which is the case an import with no "at"
	// clause is written for.
	locations := imp.hints
	if len(locations) == 0 {
		locations = []string{""}
	}
	var lastErr error
	for _, loc := range locations {
		rc, resolved, err := l.opts.SchemaResolver.Resolve(imp.ns, loc, base)
		if err != nil {
			lastErr = err
			continue
		}
		if rc == nil {
			continue
		}
		loaded, err := l.read(rc, resolved, base, imp.ns)
		rc.Close()
		if err != nil {
			// A budget refusal is not "this hint missed": it is the
			// compilation running out of allowance, and trying the next hint
			// would spend more of an allowance that is already gone. It
			// propagates rather than being collected as lastErr.
			if errors.Is(err, xdm.ErrResourceLimit) {
				return err
			}
			lastErr = err
			continue
		}
		mergeXSDSchema(l.merged, loaded)
		return nil
	}

	// §4.11 gives XQST0059 for a schema import that cannot be satisfied:
	// "It is a static error if an implementation is unable to process a
	// schema or module declaration". The resolver's own complaint is wrapped
	// rather than replaced, so that errNoSchemaResolver survives errors.Is
	// and a host can tell "you configured nothing" from "that file is not
	// there".
	if lastErr != nil {
		return fmt.Errorf(
			"XQST0059: no schema found for namespace %q: %w", imp.ns, lastErr)
	}
	return fmt.Errorf("XQST0059: no schema found for namespace %q", imp.ns)
}

// read reads one schema document from a resolver, spending the byte budget,
// and assembles it.
func (l *schemaLoader) read(rc io.ReadCloser, resolved, base, ns string) (
	*xsd.Schema, error) {
	// The read is bounded BEFORE it happens rather than measured after:
	// reading a schema and then noticing it was too big has already paid the
	// cost the bound exists to refuse. LimitReader is given one byte more
	// than the remaining allowance so that "exactly at the limit" and "over
	// it" are distinguishable.
	remaining := l.opts.MaxSchemaBytes - l.bytes
	if remaining < 0 {
		remaining = 0
	}
	data, err := io.ReadAll(io.LimitReader(rc, remaining+1))
	if err != nil {
		return nil, fmt.Errorf(
			"XQST0059: the schema for namespace %q could not be read: %w",
			ns, err)
	}
	if err := l.spend(int64(len(data)), ns); err != nil {
		return nil, err
	}
	uri := resolved
	if uri == "" {
		uri = base
	}
	return l.parse(strings.NewReader(string(data)), uri, ns)
}

// parse turns schema document text into an assembled schema.
func (l *schemaLoader) parse(r io.Reader, baseURI, ns string) (*xsd.Schema, error) {
	tree, err := xdm.Parse(r, xdm.ParseOptions{})
	if err != nil {
		return nil, fmt.Errorf(
			"XQST0059: the schema for namespace %q could not be parsed: %w",
			ns, err)
	}
	opts := xsd.Options{
		// The imported schema's own xs:include and xs:import are followed by
		// the same resolver the query's import was granted, never by a wider
		// one. A nil resolver here would let xsd apply ITS default, and this
		// package's default is the stricter of the two: nothing is opened.
		Resolver: l.opts.SchemaResolver,
	}
	// A schema document may declare with vc:minVersion that it is written for
	// XSD 1.1. Read under 1.0 the whole document is conditionally excluded and
	// contributes nothing -- silently, since that exclusion is what the
	// attribute asks a 1.0 processor to do.
	if v, ok := xsd.DocumentRequiresVersion(tree.Root); ok {
		opts.Version = v
	}
	loaded, err := xsd.Load(tree.Root, baseURI, opts)
	if err != nil {
		return nil, fmt.Errorf(
			"XQST0059: the schema for namespace %q could not be loaded: %w",
			ns, err)
	}
	return loaded, nil
}

// spend charges bytes against the compilation's schema budget.
//
// It refuses rather than truncates, on the same reasoning as the module
// loader's spend: a truncated schema document is a schema whose components are
// partly missing, and compiling against a partial static context is exactly
// the failure this package is arranged to avoid. The import would appear to
// succeed and the query would be judged against half a schema.
//
// The error wraps xdm.ErrResourceLimit and is deliberately NOT XQST0059:
// XQST0059 says something untrue about the schema store -- the schema may well
// be there and be perfectly valid -- and a host that retried on it would
// retry forever. See docs/security.md on this distinction.
func (l *schemaLoader) spend(n int64, ns string) error {
	l.bytes += n
	if l.bytes > l.opts.MaxSchemaBytes {
		return fmt.Errorf(
			"a query may read at most %d bytes of schema documents "+
				"(Options.MaxSchemaBytes); the import of %q exceeded it, "+
				"raise it if this is legitimate: %w",
			l.opts.MaxSchemaBytes, ns, xdm.ErrResourceLimit)
	}
	return nil
}

// mergeXSDSchema folds one schema's global components into another.
//
// §2.1.1 makes the in-scope schema definitions one set, so several imports of
// different namespaces produce one table. A name already present is left alone
// rather than overwritten: the first import wins, which is the same rule
// xslt's mergeSchema applies to several xsl:import-schema declarations.
//
// The version travels with the components, and 1.1 wins over 1.0 rather than
// the last import winning: the versions are not symmetric, 1.1 being a
// superset, so a schema holding any component that needs 1.1 rules needs them
// applied. dst is built by xsd.NewSchema, which has no document to read a
// version off and starts at 1.0, and the validator asks THAT schema which
// version is in force.
func mergeXSDSchema(dst, src *xsd.Schema) {
	if src == nil {
		return
	}
	if src.Version > dst.Version {
		dst.Version = src.Version
	}
	// The type environment travels with the components, as it does in xslt's
	// mergeSchema. A definition without its derivation facts is a type that
	// no longer knows what it derives from.
	//
	// NOT YET OBSERVABLE, and deliberately shipped without a test that claims
	// otherwise: nothing in xquery reads these facts. Its by-name consumers
	// are in xpath -- schemaSubsumes, derivedSubtypeOfThroughSchema,
	// schemaTypeNameMatches -- which call the process-global xdm functions
	// with no schema in hand, and cannot be handed one, because xsd imports
	// xpath (assertions and selectors contain XPath) and the dependency
	// cannot run both ways. Routing those through an environment is the
	// read-path migration; a test asserting this line works would pass with
	// or without it until then.
	dst.TypeEnv().Merge(src.TypeEnv())
	for name, t := range src.Types {
		if _, ok := dst.Types[name]; !ok {
			dst.Types[name] = t
		}
	}
	for name, d := range src.Elements {
		if _, ok := dst.Elements[name]; !ok {
			dst.Elements[name] = d
		}
	}
	for name, d := range src.Attributes {
		if _, ok := dst.Attributes[name]; !ok {
			dst.Attributes[name] = d
		}
	}
}

// loadSchemaImport follows ONE "import schema" and installs what it finds in
// the static context, at the point in the prolog where the declaration was
// read.
//
// The ordering is the whole feature. A type name resolves against the static
// context at the moment the parser reads it, so a schema installed later than
// the name is written is a schema the name cannot see. Installing at the end
// of the prolog was enough for the query BODY, but not for the prolog's own
// declarations: a function signature is parsed where it stands, so
//
//	import schema namespace s="...";
//	declare function local:f($a as s:dateOrDateTime) { ... };
//
// reported XPST0051 for a type the very previous line had imported --
// Castable-UnionType-36 and its neighbours. §4.11 requires imports to precede
// variable and function declarations in the prolog, so loading eagerly can
// never see an import that a declaration ahead of it should not have had.
//
// One loader is kept for the whole prolog rather than rebuilt per import, so
// each import consults the resolver exactly once. The merged schema is
// re-installed after every import because the loader folds into the same
// *xsd.Schema, and a query with no import never builds a loader at all -- the
// bound and the closed-by-default resolver stay invisible to it.
func (p *parser) loadSchemaImport(imp schemaImport, base string) error {
	if p.schemaLoader == nil {
		p.schemaLoader = newSchemaLoader(p.opts)
	}
	if err := p.schemaLoader.load(imp, base); err != nil {
		return err
	}
	p.sc.schema = p.schemaLoader.merged
	return nil
}
