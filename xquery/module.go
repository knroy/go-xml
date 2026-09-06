package xquery

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// Module import is XQuery 3.1 §4.12. A library module declares a target
// namespace and contributes its public functions and variables to whatever
// imports it; a main module reaches them by importing that namespace.
//
// Three properties of the specification shape everything below, and none of
// them is obvious from the grammar.
//
// The first is that the "at" locations are HINTS, not identifiers. §4.12: "the
// location hints ... may be used by the implementation to locate the module,
// but an implementation is free to ignore them." The target namespace is what
// identifies a module. So the store is keyed by namespace, a registered module
// wins over a location, and a processor that cannot fetch a location is still
// conforming — it owes XQST0059, which says "no module found", and not a
// complaint about the hint.
//
// The second is that a CYCLE IS NOT AN ERROR at XQuery 3.0 or later. This is
// the finding that decided the design, and it is easy to get backwards.
// XQuery 1.0 §4.11 raised XQST0093 for any cycle of module imports. Erratum 8
// against 1.0 narrowed it, and XQuery 3.0 removed the static rule outright:
// modules may be mutually recursive, and the only error left is a circularity
// among the *variable initialisers*, which is XQDY0054 and dynamic.
//
// The suite settles it without ambiguity. errata8-002 and errata8-002a carry
// the IDENTICAL pair of mutually importing modules and differ only in their
// spec dependency: at XQ10 the answer is XQST0093, at XQ30+ the answer is the
// value 10. Likewise modules-28 (XQ10, XQST0093 or XQST0054) against
// modules-28a (XQ30+, XQDY0054). So the import graph is walked without
// refusing a revisit, and a cycle is reported only if the *values* actually
// need each other — which is the same rule bindVariables already applies
// within one module, extended across the module boundary.
//
// The third is that visibility is per-module. §4.15 makes a %private function
// or variable visible only inside the module that declares it, so an import
// contributes the public half and the private half stays behind. That is why
// an imported module's declarations are filtered rather than concatenated.

// A ModuleResolver locates the source of a library module.
//
// The target namespace identifies the module; the location hints are the "at"
// clause of the import, in the order written, and may be empty. §4.12 lets a
// processor use a catalogue, a preloaded module, or nothing at all — which is
// why this is an interface rather than a built-in fetch. Following a location
// means reading whatever the query names, and only the caller can say whether
// that is allowed.
type ModuleResolver interface {
	// Resolve returns the source of the library module whose target
	// namespace is namespace. The hints are the import's "at" locations,
	// resolved relative to base when they are not absolute.
	//
	// Returning a nil reader and a nil error means "no such module", which
	// the caller reports as XQST0059. It is not a way to succeed quietly:
	// a query whose import found nothing has an incomplete static context,
	// and evaluating against that is exactly what this package refuses to
	// do.
	Resolve(namespace string, hints []string, base string) (io.ReadCloser, string, error)
}

// errNoModuleResolver marks a refusal as a configuration fault rather than a
// module that was looked for and not found.
//
// The two are told apart for the same reason xsd's errNoResolver exists: a
// location that could not be reached because nothing was ever configured to
// reach it is not evidence about the module, and the error says so. The
// XQST0059 the caller sees is the same either way, because that is the code
// §4.12 gives, but the wrapped sentinel lets a host tell "you did not
// configure a resolver" from "your module store has no such namespace".
var errNoModuleResolver = errors.New("no ModuleResolver is configured")

// noModuleResolver is the default, and it fetches nothing.
//
// This is the house pattern, and it is load-bearing rather than tidy. A query
// is untrusted input; an "at" location is a string the query's author chose.
// Defaulting to a file resolver would mean that evaluating a query grants its
// author the run of the filesystem, which is precisely the reach xsd.Load
// closed off when it stopped defaulting to a rooted FileResolver. So with no
// resolver configured, an "at" location is NEVER opened — not tried and
// failed, not opened, so a missing file and a present one are indistinguishable
// from outside — and the import fails with XQST0059.
type noModuleResolver struct{}

// Resolve implements ModuleResolver by refusing.
func (noModuleResolver) Resolve(namespace string, hints []string, base string) (
	io.ReadCloser, string, error) {
	return nil, "", fmt.Errorf(
		"no module for namespace %q: %w (Options.ModuleResolver); register "+
			"the module in Options.Modules, or pass a resolver to say what "+
			"this query may read", namespace, errNoModuleResolver)
}

// DefaultMaxModules bounds how many library modules one compilation may load
// when Options.MaxModules is zero.
//
// A module may import modules, so the graph is attacker-shaped in the same way
// a schema's include graph is: one small module that imports two others, each
// importing two more, costs nothing to write and everything to load. This is
// the same bound and the same reasoning as xsd's DefaultMaxDocuments, at the
// same value, because it guards the same shape of fan-out. Anything a real
// query imports is two orders of magnitude below it.
const DefaultMaxModules = 512

// DefaultMaxModuleBytes bounds the total source text one compilation may read
// from its resolver when Options.MaxModuleBytes is zero.
//
// The module count alone does not bound the work: 512 modules of a gigabyte
// each is within DefaultMaxModules and is not within anything worth calling a
// budget. So the bytes are counted too, cumulatively across the whole
// compilation rather than per module, because a budget spent one module at a
// time is not spent at all.
//
// 16 MB is far above any hand-written module — the largest library module in
// this tree's corpora is under 100 KB — and far below what would matter to the
// process.
const DefaultMaxModuleBytes = 16 << 20

// A Module is a library module registered with the compilation directly,
// rather than fetched through a resolver.
//
// This is the store §4.12 leaves to the implementation, and it is the only way
// to import a module without granting the query any reach at all: the caller
// supplies the source, so nothing is opened and nothing is fetched. It is also
// what an import with no "at" clause resolves against, since such an import
// names a namespace and nothing else.
type Module struct {
	// Namespace is the module's target namespace. It must match the "module
	// namespace" declaration in Source, which is checked on load: a store
	// keyed by a namespace the module does not declare would answer imports
	// with the wrong module. XQST0059 is the error, since from the import's
	// point of view no module with that namespace was found.
	Namespace string

	// Source is the library module's text.
	Source string

	// BaseURI is the static base URI of this module, used for the module's
	// own relative resolution. It may be empty.
	BaseURI string
}

// moduleKey identifies a loaded module. The target namespace alone is the key,
// because §4.12 makes the namespace the identity and the location a hint: two
// imports of one namespace through different hints are one module, and
// importing it twice must not initialise its variables twice.
type moduleKey string

// libModule is a compiled library module.
//
// It is deliberately close to Query — the same declarations, the same static
// context, compiled by the same parser — because a library module differs from
// a main module in exactly two things: it has a target namespace, and it has
// no query body. Everything the prolog can express is the same, so the same
// code reads it.
type libModule struct {
	ns      string
	sc      *staticContext
	src     string
	vars    []*varDecl
	funcs   []*funcDecl
	formats map[string]*xpath.DecimalFormat

	// imports are the target namespaces this module itself imports, in source
	// order. They are recorded rather than followed at parse time so that the
	// loader owns the walk and the budget: a module that follows its own
	// imports has no way to count them.
	imports []moduleImport
}

// moduleImport is one "import module" declaration, as written.
type moduleImport struct {
	ns    string
	hints []string
	// prefix is the prefix the import binds, empty for the prefixless form.
	prefix string
}

// moduleLoader carries the state of one compilation's module loading: what has
// been loaded, what is being loaded, and how much budget is left.
//
// It is per-compilation rather than per-Query because the budget is: a query
// that imports a module that imports two more has spent three modules of one
// allowance, and an allowance reset per import would bound nothing.
type moduleLoader struct {
	opts Options
	// loaded is every module keyed by target namespace. A namespace already
	// here is not loaded again, which is what makes a cyclic import graph
	// terminate without being an error — see the type comment on this file.
	loaded map[moduleKey]*libModule
	// loading is the set of namespaces whose text has been read but whose
	// imports have not yet been followed. It is what a revisit lands in
	// during a cycle, and landing there is NOT an error at 3.0 or later.
	loading map[moduleKey]bool
	// order is the namespaces in the order they finished loading, which is a
	// topological order for an acyclic graph and a deterministic one for a
	// cyclic graph. Variable initialisation follows it.
	order []moduleKey
	// count and bytes are the budget spent so far.
	count int
	bytes int64
	// registered is Options.Modules keyed by namespace, for the store lookup
	// that happens before the resolver is consulted.
	registered map[string]Module
}

// newModuleLoader prepares a loader, applying the defaults for the bounds and
// for the resolver.
func newModuleLoader(opts Options) *moduleLoader {
	if opts.ModuleResolver == nil {
		// Closed by default. See noModuleResolver: this is the difference
		// between "a query may name a module the host registered" and "a
		// query may read a file of its choosing".
		opts.ModuleResolver = noModuleResolver{}
	}
	if opts.MaxModules == 0 {
		opts.MaxModules = DefaultMaxModules
	}
	if opts.MaxModuleBytes == 0 {
		opts.MaxModuleBytes = DefaultMaxModuleBytes
	}
	l := &moduleLoader{
		opts:       opts,
		loaded:     map[moduleKey]*libModule{},
		loading:    map[moduleKey]bool{},
		registered: make(map[string]Module, len(opts.Modules)),
	}
	for _, m := range opts.Modules {
		// A later registration of one namespace replaces an earlier one,
		// which is the ordinary map rule and the only one that does not need
		// an error code the specification has not defined for it.
		l.registered[m.Namespace] = m
	}
	return l
}

// load loads the module for one namespace, following its own imports.
//
// A namespace already loaded, or already being loaded, returns immediately.
// The second case is the cycle, and it returns WITHOUT an error at XQuery 3.0
// and later: §4.12 permits mutually recursive modules, and errata8-002a is the
// case that pins it — the same pair of modules that is XQST0093 at 1.0
// evaluates to 10 at 3.0+. The 1.0 rule is applied by the caller, which knows
// the importing module's declared version; see checkModuleCycle.
func (l *moduleLoader) load(imp moduleImport, base string, ver XQVersion) (
	*libModule, error) {
	key := moduleKey(imp.ns)
	// The in-progress check comes FIRST. A module is published into loaded
	// before its own imports are followed -- that is what lets a cycle find
	// it -- so a revisit during a cycle is present in BOTH maps, and testing
	// loaded first made the 1.0 branch below unreachable. Sabotage caught
	// this: forcing every cycle to be fatal left errata8-002a passing.
	if l.loading[key] {
		// A cycle. At 1.0 this is XQST0093; at 3.0+ it is not an error at
		// all, and the partially loaded module is returned so that the
		// importing module can be compiled against it. Nothing is missing
		// from it that a name resolution needs: its own declarations were
		// recorded before its imports were followed.
		if !ver.atLeast30() {
			return nil, fmt.Errorf(
				"XQST0093: the module %q takes part in a cycle of module "+
					"imports, which XQuery 1.0 forbids", imp.ns)
		}
		return l.loaded[key], nil
	}
	if m, ok := l.loaded[key]; ok {
		return m, nil
	}

	src, baseURI, err := l.fetch(imp, base)
	if err != nil {
		return nil, err
	}

	l.loading[key] = true
	defer delete(l.loading, key)

	m, err := l.compile(imp.ns, src, baseURI, ver)
	if err != nil {
		return nil, err
	}
	// The module is published before its imports are followed, so that a
	// module reached again through a cycle finds it. Its own declarations are
	// complete at this point; only the modules it imports are not.
	l.loaded[key] = m

	for _, sub := range m.imports {
		if _, err := l.load(sub, baseURI, m.sc.xqVersion); err != nil {
			return nil, err
		}
	}
	l.order = append(l.order, key)
	return m, nil
}

// fetch returns the source text of one module, spending the budget.
//
// The registered store is consulted first and the resolver second, because a
// module the host supplied needs no reach at all and a location hint does.
// That order also means a host can shadow a location with a known-good module,
// which is the whole point of a store.
func (l *moduleLoader) fetch(imp moduleImport, base string) (string, string, error) {
	l.count++
	if l.count > l.opts.MaxModules {
		// The budget declines to answer; it does not answer "no module". A
		// query whose import was refused for want of budget must FAIL, and
		// must fail as a resource limit rather than as XQST0059, because
		// XQST0059 says something untrue about the module store — the module
		// may well be there. See docs/security.md on this distinction.
		return "", "", fmt.Errorf(
			"a query may import at most %d modules (Options.MaxModules); "+
				"raise it if this is legitimate: %w",
			l.opts.MaxModules, xdm.ErrResourceLimit)
	}
	if m, ok := l.registered[imp.ns]; ok {
		if err := l.spend(int64(len(m.Source)), imp.ns); err != nil {
			return "", "", err
		}
		baseURI := m.BaseURI
		if baseURI == "" {
			baseURI = base
		}
		return m.Source, baseURI, nil
	}
	rc, uri, err := l.opts.ModuleResolver.Resolve(imp.ns, imp.hints, base)
	if err != nil {
		// §4.12 gives XQST0059 for a module that cannot be found. The
		// resolver's own complaint is wrapped rather than replaced, because
		// "no resolver is configured" and "that file does not exist" are
		// different facts about the host and a caller may need to tell them
		// apart — errNoModuleResolver survives errors.Is through this.
		return "", "", fmt.Errorf(
			"XQST0059: no library module found for namespace %q: %w",
			imp.ns, err)
	}
	if rc == nil {
		return "", "", fmt.Errorf(
			"XQST0059: no library module found for namespace %q", imp.ns)
	}
	defer rc.Close()
	// The read is bounded before it happens, not after: reading a module and
	// then noticing it was too big has already paid the cost the bound
	// exists to refuse. LimitReader is given one byte more than the
	// remaining allowance so that "exactly at the limit" and "over it" are
	// distinguishable.
	remaining := l.opts.MaxModuleBytes - l.bytes
	if remaining < 0 {
		remaining = 0
	}
	data, err := io.ReadAll(io.LimitReader(rc, remaining+1))
	if err != nil {
		return "", "", fmt.Errorf(
			"XQST0059: the library module for namespace %q could not be "+
				"read: %w", imp.ns, err)
	}
	if err := l.spend(int64(len(data)), imp.ns); err != nil {
		return "", "", err
	}
	if uri == "" {
		uri = base
	}
	return string(data), uri, nil
}

// spend charges bytes against the compilation's byte budget.
//
// It refuses rather than truncates. A truncated module is a module whose
// declarations are partly missing, and compiling against a partial static
// context is the failure this whole package is arranged to avoid: the import
// would appear to succeed and the query would evaluate against half a library.
func (l *moduleLoader) spend(n int64, ns string) error {
	l.bytes += n
	if l.bytes > l.opts.MaxModuleBytes {
		return fmt.Errorf(
			"the modules imported by this query exceed %d bytes "+
				"(Options.MaxModuleBytes), reached at namespace %q: %w",
			l.opts.MaxModuleBytes, ns, xdm.ErrResourceLimit)
	}
	return nil
}

// compile parses one library module's text.
//
// It is the main-module path minus the query body and plus the target
// namespace check. The version declaration is read first, exactly as it is for
// a main module, because a library module may declare its own version and is
// then judged by it — which is what makes errata8-002 and errata8-002a two
// different answers to one pair of modules.
func (l *moduleLoader) compile(ns, src, baseURI string, importerVer XQVersion) (
	*libModule, error) {
	sc := newStaticContext()
	sc.baseURI = baseURI
	sc.declBase = baseURI

	src = normalizeLineEndings(src)
	p := &parser{src: src, sc: sc, version: sc.xqVersion.xpathVersion(),
		declaredNS: map[string]bool{}, inLibrary: true}
	if err := p.parseVersionDecl(); err != nil {
		return nil, err
	}
	declared, err := p.parseModuleDecl()
	if err != nil {
		return nil, err
	}
	if declared != ns {
		// §4.12: the import names a target namespace, and the module found
		// must be a module of that namespace. One that is not is not the
		// module asked for, so from the import's point of view none was
		// found. XQST0059 is the code, and modules-bad-ns is the case: the
		// same file registered under two namespaces answers only for the one
		// it declares.
		return nil, fmt.Errorf(
			"XQST0059: the module found for namespace %q declares the "+
				"target namespace %q instead", ns, declared)
	}
	if err := p.parseProlog(); err != nil {
		return nil, err
	}
	// §4.12: every function and variable a library module declares must be in
	// that module's own target namespace. A module declaring something
	// elsewhere would contribute a name its importer could not have expected
	// from that import, so it is XQST0048 and the module is rejected rather
	// than partly used. modules-17 is a module whose variable is in another
	// namespace entirely.
	for _, d := range p.vars {
		if d.name.URI != ns {
			return nil, fmt.Errorf(
				"XQST0048: the variable %s is not in the target namespace "+
					"%q of the module that declares it",
				d.name.Lexical(), ns)
		}
	}
	for _, d := range p.funcs {
		if d.name.URI != ns {
			return nil, fmt.Errorf(
				"XQST0048: the function %s is not in the target namespace "+
					"%q of the module that declares it",
				d.name.Lexical(), ns)
		}
	}
	p.skipSpaceAndComments()
	if p.pos < len(p.src) {
		// [4] LibraryModule ::= ModuleDecl Prolog: a library module has no
		// query body, so text left over after the prolog is a grammar error
		// rather than an expression. K2-ModuleProlog-1 is a library module
		// with a body and asks for XPST0003.
		return nil, p.errorf(
			"XPST0003: a library module has no query body")
	}
	// Each declaration is stamped with the static context it was written in,
	// so that its initialiser or body resolves names against its OWN module's
	// prolog once it has been imported somewhere else. See varDecl.home.
	for _, d := range p.vars {
		d.home = sc
	}
	for _, d := range p.funcs {
		d.home = sc
	}
	m := &libModule{ns: ns, sc: sc, src: src, vars: p.vars, funcs: p.funcs,
		formats: p.formats, imports: p.moduleImports}
	return m, nil
}

// imported reports whether this module imported the target namespace ns, and
// so may see its public declarations.
//
// §4.12 scopes an import to the module that writes it: a module reaches only
// what it imported itself, not what its importer happened to import as well.
// cbcl-module-003 is the case -- a module reading $foo:test having imported no
// foo -- and it asserts XPST0008.
func (m *libModule) imported(ns string) bool {
	if ns == m.ns {
		return true
	}
	for _, imp := range m.imports {
		if imp.ns == ns {
			return true
		}
	}
	return false
}

// visible reports whether a declaration is contributed to an importing module.
//
// §4.15: "a private function or variable is visible only within the module in
// which it is declared." So an import takes the public half and leaves the
// private half behind, which is what modules-pub-priv-1 and -2 assert as a
// pair: the public defs:g is callable and the private defs:f is XPST0017.
//
// A declaration with no annotation is public — §4.15 makes %public the
// default, so the absence of an annotation is not the absence of visibility.
func (m *libModule) visibleVars() []*varDecl {
	out := make([]*varDecl, 0, len(m.vars))
	for _, d := range m.vars {
		if !d.private {
			out = append(out, d)
		}
	}
	return out
}

func (m *libModule) visibleFuncs() []*funcDecl {
	out := make([]*funcDecl, 0, len(m.funcs))
	for _, d := range m.funcs {
		if !d.private {
			out = append(out, d)
		}
	}
	return out
}

// modules returns the loaded modules in initialisation order.
//
// The order is the one load finished them in, which is a topological order of
// the import graph wherever the graph has one: a module's imports are followed
// before it is appended, so an imported module precedes its importer. Where
// the graph is cyclic there is no topological order to have, and the order is
// merely deterministic — which is enough, because a genuine circularity among
// the values is caught by the variable binder as XQDY0054 rather than by the
// ordering. See bindVariables.
func (l *moduleLoader) modules() []*libModule {
	out := make([]*libModule, 0, len(l.order))
	for _, k := range l.order {
		if m := l.loaded[k]; m != nil {
			out = append(out, m)
		}
	}
	// A module reached only through a cycle is loaded but never appended,
	// because the recursion returned early at the revisit. Those are added
	// after, in namespace order so that the result does not depend on map
	// iteration.
	seen := make(map[moduleKey]bool, len(out))
	for _, k := range l.order {
		seen[k] = true
	}
	var rest []moduleKey
	for k := range l.loaded {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i] < rest[j] })
	for _, k := range rest {
		out = append(out, l.loaded[k])
	}
	return out
}

// importedNames collects the public declarations of every loaded module and
// checks them for the conflicts §4.12 names.
//
// XQST0049 is two variables of the same name and XQST0034 is two functions of
// the same name and arity, in each case across the whole set of modules in
// scope. The codes are the same ones a single module's duplicate declaration
// raises, which is deliberate on the specification's part: §4.12 says the
// imported declarations are added to the importing module's static context, so
// a clash between two imports is the same clash as a clash within one module.
func checkImportedNames(mods []*libModule, ownVars []*varDecl, ownFuncs []*funcDecl) error {
	vars := map[string]string{}
	for _, d := range ownVars {
		vars[d.name.Clark()] = "the importing module"
	}
	funcs := map[string]string{}
	for _, d := range ownFuncs {
		funcs[funcKey(d)] = "the importing module"
	}
	for _, m := range mods {
		for _, d := range m.visibleVars() {
			k := d.name.Clark()
			if prev, ok := vars[k]; ok {
				return fmt.Errorf(
					"XQST0049: the variable %s is declared by %s and by the "+
						"module %q", d.name.Lexical(), prev, m.ns)
			}
			vars[k] = fmt.Sprintf("the module %q", m.ns)
		}
		for _, d := range m.visibleFuncs() {
			k := funcKey(d)
			if prev, ok := funcs[k]; ok {
				return fmt.Errorf(
					"XQST0034: the function %s#%d is declared by %s and by "+
						"the module %q", d.name.Lexical(), len(d.params),
					prev, m.ns)
			}
			funcs[k] = fmt.Sprintf("the module %q", m.ns)
		}
	}
	return nil
}

// funcKey identifies a function by name and arity, which is what XQST0034
// compares: two functions of one name and different arities are distinct.
func funcKey(d *funcDecl) string {
	return fmt.Sprintf("%s#%d", d.name.Clark(), len(d.params))
}

// MapModuleResolver answers module imports from an in-memory table keyed by
// target namespace.
//
// It exists so that a host can supply modules without granting any reach:
// nothing is opened and nothing is fetched, and a namespace not in the table
// is XQST0059. Options.Modules is the shorter way to say the same thing for a
// fixed set; this is for a host that builds the set at run time.
type MapModuleResolver struct {
	// Modules maps a target namespace to the module's source text.
	Modules map[string]string
}

// Resolve implements ModuleResolver.
//
// The location hints are ignored, deliberately. §4.12 permits it — they are
// hints — and honouring them here would mean the table's keys were not the
// only thing this resolver answers for, which is the property that makes it
// safe to hand an untrusted query.
func (r MapModuleResolver) Resolve(namespace string, hints []string, base string) (
	io.ReadCloser, string, error) {
	src, ok := r.Modules[namespace]
	if !ok {
		return nil, "", nil
	}
	return io.NopCloser(strings.NewReader(src)), base, nil
}

// loadModules follows a main module's imports and returns every library module
// reached, in initialisation order.
//
// The importing module's own static context supplies the base URI that a
// location hint resolves against, and its declared version decides whether a
// cycle is XQST0093 — see load, and the finding at the top of this file.
func loadModules(imports []moduleImport, opts Options, sc *staticContext) (
	[]*libModule, error) {
	if len(imports) == 0 {
		// The common case costs nothing: a query with no import never builds
		// a loader and never consults a resolver, so the bounds and the
		// closed-by-default resolver are invisible to it.
		return nil, nil
	}
	l := newModuleLoader(opts)
	for _, imp := range imports {
		if _, err := l.load(imp, sc.baseURI, sc.xqVersion); err != nil {
			return nil, err
		}
	}
	return l.modules(), nil
}
