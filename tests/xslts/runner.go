package xslts

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xsd"
	"github.com/knroy/go-xml/xslt"
)

// Runner executes the suite against this engine.
// Target is the version of XSLT a run measures conformance against.
//
// The suite is one catalog covering every version, so which cases are in
// scope is a property of the run rather than of the suite: a case declaring
// XSLT30+ is out of scope for a 2.0 run and is the point of a 3.0 one. Two
// runs over the same catalog is how a change is shown not to have cost
// ground at 2.0 while gaining it at 3.0.
type Target int

const (
	// XSLT20 measures what an XSLT 2.0 processor should pass.
	XSLT20 Target = iota
	// XSLT30 measures what an XSLT 3.0 processor should pass.
	XSLT30
)

// String implements fmt.Stringer.
func (t Target) String() string {
	if t == XSLT30 {
		return "XSLT 3.0"
	}
	return "XSLT 2.0"
}

type Runner struct {
	// Root is the suite checkout.
	Root string
	// Target is the version measured. The zero value is XSLT 2.0, so an
	// existing caller keeps the run it had.
	Target Target
	// Timeout bounds one transform. A stylesheet that does not terminate is a
	// failure of that test rather than of the run.
	Timeout time.Duration
}

// Outcome is what happened to one test.
type Outcome struct {
	Set, Name string
	Pass      bool
	Skipped   bool
	// Why explains a skip or a failure.
	Why string
}

// Summary counts a run.
type Summary struct {
	Total, Passed, Failed, Skipped int
	// Failures holds the failing outcomes, for reporting.
	Failures []Outcome
	// SkipReasons counts why tests were left out, so that the scope of a run
	// is visible rather than implied by the total.
	SkipReasons map[string]int
	// BySet counts each test-set separately. A single percentage hides which
	// features work: a set failing 96 of 100 is an unimplemented feature,
	// while one failing 5 of 300 is a handful of edge cases, and the two want
	// entirely different work.
	BySet map[string]*SetStats
}

// SetStats counts one test-set.
type SetStats struct{ Passed, Failed, Skipped int }

// Run executes every test set in the catalog.
// suiteEntityResolver permits the suite's documents to read external
// entities, confined to the suite directory.
//
// The suite is trusted input and several of its tests are ABOUT external
// entities — an external DTD subset supplying declarations, an external
// parameter entity, a fragment factored into its own file. Nothing outside
// r.Root is reachable: FileResolver rejects a non-file scheme before touching
// the filesystem and resolves symlinks before the containment check, and this
// is the same confinement every other suite read already goes through.
//
// Production callers get none of this. ExternalEntities is off by default on
// FileResolver and on xdm.ParseOptions alike, and neither is implied by
// AllowDOCTYPE, so enabling it here changes nothing for anyone else.
func (r *Runner) entityResolver() *xslt.FileResolver {
	return &xslt.FileResolver{
		Roots:            []string{r.Root},
		AllowDOCTYPE:     true,
		ExternalEntities: true,
	}
}

func (r *Runner) Run() (*Summary, error) {
	// Every base URI in a run derives from the root, and fn:base-uri and
	// fn:resolve-uri require an absolute one: a relative root made the whole
	// base-uri set fail with FORG0002 on a path that was perfectly correct,
	// which measured the harness rather than the engine.
	if abs, err := filepath.Abs(r.Root); err == nil {
		r.Root = abs
	}

	data, err := os.ReadFile(filepath.Join(r.Root, "catalog.xml"))
	if err != nil {
		return nil, err
	}
	var cat Catalog
	if err := decode(data, &cat); err != nil {
		return nil, fmt.Errorf("catalog.xml: %w", err)
	}

	sum := &Summary{SkipReasons: map[string]int{}, BySet: map[string]*SetStats{}}
	for _, ref := range cat.TestSets {
		set, err := r.loadSet(ref)
		if err != nil {
			// A test-set that will not parse is reported rather than skipped
			// silently: it is a gap in the run, and a run whose scope is
			// unclear is worse than one with a lower number.
			sum.Total++
			sum.Failed++
			sum.Failures = append(sum.Failures, Outcome{
				Set: ref.Name, Name: "(test-set)",
				Why: "could not read: " + err.Error(),
			})
			continue
		}
		for i := range set.Cases {
			out := r.runCase(set, &set.Cases[i])
			sum.Total++
			st := sum.BySet[out.Set]
			if st == nil {
				st = &SetStats{}
				sum.BySet[out.Set] = st
			}
			switch {
			case out.Skipped:
				sum.Skipped++
				sum.SkipReasons[out.Why]++
				st.Skipped++
			case out.Pass:
				sum.Passed++
				st.Passed++
			default:
				sum.Failed++
				sum.Failures = append(sum.Failures, out)
				st.Failed++
			}
		}
	}
	return sum, nil
}

func (r *Runner) loadSet(ref TestSetRef) (*TestSet, error) {
	path := filepath.Join(r.Root, filepath.FromSlash(ref.File))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var set TestSet
	if err := decode(data, &set); err != nil {
		return nil, err
	}
	// The catalog's own namespace declarations bind the prefix in an
	// <initial-template> name; encoding/xml does not resolve a QName that
	// appears in an attribute value, so they are recovered separately.
	if err := resolveInitialTemplateNames(data, &set); err != nil {
		return nil, err
	}
	// Likewise for the bindings on <result>, which innerxml drops.
	if err := resolveResultNamespaces(data, &set); err != nil {
		return nil, err
	}
	// Stylesheet and source paths are relative to the test-set file rather
	// than to the suite root, so the directory travels with the parsed set.
	set.Dir = filepath.Dir(path)
	set.Path = path
	if set.Name == "" {
		set.Name = ref.Name
	}
	return &set, nil
}

// runCase runs one test and judges the result.
func (r *Runner) runCase(set *TestSet, tc *TestCase) Outcome {
	out := Outcome{Set: set.Name, Name: tc.Name}
	if ok, why := inScope(set, tc, r.Target); !ok {
		out.Skipped, out.Why = true, why
		return out
	}

	assert, err := ParseAssert([]byte(tc.Result.Inner), tc.Result.NS)
	if err != nil {
		out.Why = "unreadable result: " + err.Error()
		return out
	}

	res, terr := r.transformSafely(set, tc)

	// A failure that is really an engine limitation the suite does not
	// declare as a feature is reported as out of scope rather than as a
	// conformance failure. Counting it as failing overstates the gap: the
	// engine is not disagreeing with the specification, it is refusing input
	// it documents that it refuses.
	if terr != nil {
		if why := outOfScopeError(terr); why != "" {
			out.Skipped, out.Why = true, why
			return out
		}
	}

	// Serialization errors are raised while writing the result, not while
	// building it, so a transform that succeeded may still fail to
	// serialise. The suite asserts those with assert-serialization-error,
	// and judging them needs the error in hand — so the result is written to
	// a discard writer here and any failure is promoted to the transform
	// error. Only tests that actually assert a serialization error do this,
	// because for every other test the serialised text is obtained lazily by
	// whichever assertion needs it.
	if terr == nil && res != nil && wantsSerializationError(assert) {
		if err := res.Serialize(io.Discard); err != nil {
			terr = err
		}
	}

	// The schema the environment declares is part of the static context the
	// assertions are evaluated in, exactly as it is for the stylesheet.
	ok, why := r.judge(assert, res, terr, set, envSchema(set, r.environment(set, tc)))
	out.Pass, out.Why = ok, why
	return out
}

// wantsSerializationError reports whether the assertion tree asks for an
// error raised during serialisation.
func wantsSerializationError(a Assertion) bool {
	if a.Kind == "assert-serialization-error" {
		return true
	}
	// A serialization error may also be asserted as an ordinary <error> with
	// a serialization code -- output-0710 wants SENR0001 that way, and there
	// is no other spelling of "the transform succeeds but the result cannot
	// be written". The SE prefix is the serialization spec's own; no other
	// error family in the suite uses it.
	if a.Kind == "error" && strings.HasPrefix(a.Code, "SE") {
		return true
	}
	for _, c := range a.Children {
		if wantsSerializationError(c) {
			return true
		}
	}
	return false
}

// outOfScopeError recognises the documented refusals, which are choices this
// engine makes rather than places it falls short.
//
// The list is deliberately short and each entry names a decision recorded in
// the source. Anything not listed is a failure, so a new limitation cannot
// quietly move into this category.
func outOfScopeError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "Decoder.CharsetReader is nil"):
		// xdm/parse.go leaves CharsetReader nil so that a document in an
		// encoding this package cannot verify is refused rather than routed
		// through a converter it does not control.
		return "document is not UTF-8 or UTF-16"
	}
	return ""
}

// transformSafely runs one test and turns a panic into a failure.
//
// A panic is a bug worth fixing rather than tolerating — it is a denial of
// service in a request handler — but it must not end the run: fifteen
// thousand tests reduced to one stack trace hides every other result. So the
// panic is recorded as this test's failure and the run continues, and the
// message says "panic" so it is not mistaken for an ordinary mismatch.
func (r *Runner) transformSafely(set *TestSet, tc *TestCase) (res *xslt.Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			res, err = nil, fmt.Errorf("panic: %v", p)
		}
	}()
	return r.transform(set, tc)
}

// transform compiles the stylesheet and runs it, returning the result or the
// error the engine reported.
func (r *Runner) transform(set *TestSet, tc *TestCase) (*xslt.Result, error) {
	sheets := tc.Test.Stylesheets
	if len(sheets) == 0 {
		// A test-set may declare one stylesheet in an environment and share
		// it across every case, which is how the larger generated sets are
		// written.
		if env := r.environment(set, tc); env != nil {
			sheets = env.Stylesheets
		}
	}
	if len(sheets) == 0 {
		// A package test names its principal module as <package> rather than
		// <stylesheet>: an xsl:package IS the stylesheet when it is the one
		// being run, and the element says which of the two roles the file
		// plays rather than what kind of module it is.
		for _, pk := range tc.Test.Packages {
			if pk.Role == "principal" {
				sheets = append(sheets,
					StylesheetRef{File: pk.File, Role: "principal"})
			}
		}
	}
	if len(sheets) == 0 {
		return nil, fmt.Errorf("the test names no stylesheet")
	}
	// The principal stylesheet is the one with no role, or the first.
	var main string
	for _, s := range sheets {
		if s.Role == "" || s.Role == "principal" {
			main = s.File
			break
		}
	}
	if main == "" {
		main = sheets[0].File
	}
	sheetPath := filepath.Join(set.Dir, filepath.FromSlash(main))
	sheetSrc, err := os.ReadFile(sheetPath)
	if err != nil {
		return nil, err
	}
	// The same file: URI form that is passed to Compile below. These two
	// have to agree: the parser stamps this base URI on every element, and
	// an expression resolves against the base URI of the element it is
	// written on in preference to the module's. A bare path here and a URI
	// there left the two disagreeing, and the element's bare path won.
	sheetDoc, err := xdm.ParseString(string(stripBOM(sheetSrc)),
		xdm.ParseOptions{AllowDOCTYPE: true, BaseURI: fileURI(sheetPath),
			// The stylesheet was itself retrieved from a URI, so the tree
			// document('') hands back has a dm:document-uri. Leaving it
			// empty made document-uri(document('')) empty for a document
			// that plainly has one.
			DocumentURI:      fileURI(sheetPath),
			ExternalEntities: r.entityResolver()})
	if err != nil {
		return nil, err
	}
	// A <param static="yes"> is supplied to the *compiler*, not to the
	// transform: a static parameter is bound before static analysis begins,
	// which is the whole point of it, so by the time Transform runs its
	// value has already been used to decide which parts of the stylesheet
	// exist.
	var staticParams map[string]xdm.Sequence
	for _, p := range tc.Test.Params {
		if p.Static != "yes" && p.Static != "true" {
			continue
		}
		v, err := xpath.Eval(p.Select, xpath.NewContext(nil, xpath.Builtins()),
			catalogNS{})
		if err != nil {
			return nil, fmt.Errorf("static parameter %s: %w", p.Name, err)
		}
		if staticParams == nil {
			staticParams = map[string]xdm.Sequence{}
		}
		staticParams[p.Name] = v
	}

	// A 2.0 run must behave as an XSLT 2.0 processor, which means refusing
	// the constructs 3.0 added — xsl:package above all, whose own @version
	// says nothing about whether it is one.
	maxVersion := 2.0
	if r.Target == XSLT30 {
		maxVersion = 0
	}
	ss, err := xslt.Compile(sheetDoc.Root, xslt.CompileOptions{
		MaxVersion:   maxVersion,
		StaticParams: staticParams,
		// The suite's stylesheets import schemas by relative path, and call
		// document() on the test-set's own files. Both resolvers are rooted
		// at the test-set directory: the tests are trusted input, and
		// confining them there is what keeps that true.
		SchemaResolver: envSchemaResolver{set: set, env: r.environment(set, tc)},
		// xsl:use-package names a package rather than locating one, so the
		// resolver matches on the name and version the environment declares.
		PackageResolver: envPackageResolver{set: set, tc: tc,
			env: r.environment(set, tc), entities: r.entityResolver()},
		// The suite's stylesheets include one another by relative path, and
		// a resolver rooted at the test-set directory is what makes that
		// work without opening the filesystem generally.
		// Rooted at the suite rather than the test-set directory: tests
		// legitimately reference documents in sibling directories, and
		// confining each set to itself refuses those as if the engine could
		// not read them. The suite is trusted input; the root is what keeps
		// the run from reaching outside it.
		Resolver: r.entityResolver(),
		// A file: URI rather than a bare path. fn:static-base-uri and
		// fn:resolve-uri are defined over URIs, and an absolute filesystem
		// path is still a *relative* URI reference because it has no scheme
		// — so a bare path made every stylesheet resolving against its own
		// base fail with FORG0002 on a base that was perfectly correct. The
		// resolvers strip the scheme back off before touching the
		// filesystem, so joining with filepath still works.
		BaseURI: fileURI(sheetPath),
	})
	if err != nil {
		return nil, err
	}

	src, srcPath, err := r.principalSource(set, tc)
	if err != nil {
		return nil, err
	}

	docs := &xslt.FileResolver{
		Roots:        []string{r.Root},
		AllowDOCTYPE: true,
		UnparsedText: true,
		// A source document may itself declare external entities; the
		// principal source is read with them enabled, and a document the
		// stylesheet loads later has the same claim on them.
		ExternalEntities: true,
	}
	// The harness parses the principal source itself, because it has to
	// schema-annotate the tree before the transform starts. Telling the
	// document resolver about that tree is what makes
	// "doc(document-uri(.)) is ." true, which section 16.1 requires and
	// accessor-008 tests: without it doc() parses the same file a second
	// time and answers a node with a different identity.
	if srcPath != "" && src != nil && src.Kind == xdm.KindDocument {
		docs.Preload(fileURI(srcPath), &xdm.Tree{Root: src})
	}
	// Every other source the environment declares with a validation is
	// preloaded the same way. An environment may name documents the
	// stylesheet reaches through fn:doc rather than as the principal input —
	// validation-20 declares two, and validation-2001 loads both by URI — and
	// the specification's validation attribute applies to them exactly as it
	// does to the principal source. Leaving them to the resolver parsed the
	// file fresh and untyped, so fn:nilled and a schema-element() pattern had
	// no annotation to read and every such template failed to match.
	r.preloadSources(set, tc, docs)
	opts := xslt.TransformOptions{
		// The suite's documents legitimately carry DOCTYPE declarations,
		// which the resolver refuses by default because following one is
		// the XXE entry point. The suite is trusted input read from a
		// checkout, so it is enabled here and nowhere else.
		Documents: docs,
		// fn:unparsed-text is off by default because it hands a stylesheet
		// the raw bytes of any file the resolver will open. The suite is
		// trusted input read from a checkout and several tests exercise the
		// function directly, so it is granted here -- through the SAME
		// FileResolver, so the read is confined to r.Root on exactly the
		// terms every other suite read is, with no second gate written
		// separately. Production callers get UnparsedText=false.
		Texts: docs,
	}
	// A collection is declared by the environment, not discovered on the
	// filesystem, so only a test whose environment names one gets a resolver.
	// Leaving the field nil elsewhere keeps fn:collection refusing, which is
	// what the tests asserting that refusal expect.
	if env := r.environment(set, tc); env != nil && len(env.Collections) > 0 {
		opts.Collections = &catalogCollections{set: set, decls: env.Collections}
	}
	// <output file="..."/> is how a test case supplies the base output URI.
	// It is relative to the test-set directory, and setting it is what makes
	// fn:current-output-uri answer anything but the empty sequence. The
	// literal "#absent" is the catalog's way of asking for no base output URI
	// at all -- current-output-uri-013 and -015 exist to distinguish that
	// from a URI the driver picked for itself.
	if tc.Test.Output != nil && tc.Test.Output.File != "#absent" {
		opts.BaseOutputURI = fileURI(
			filepath.Join(set.Dir, filepath.FromSlash(tc.Test.Output.File)))
	}
	if tc.Test.InitialTemplate != nil {
		opts.InitialTemplate = tc.Test.InitialTemplate.Name
		// The catalog binds its own prefixes, so the name is resolved
		// there rather than against the stylesheet. A stylesheet is free
		// to spell a different namespace with the same prefix, and
		// resolving twice picked a template the catalog never named.
		opts.InitialTemplateURI = tc.Test.InitialTemplate.URI
		// <initial-template> may carry its own <param> children, which are
		// the arguments of that one call rather than the stylesheet's global
		// parameters. A tunnel="yes" one passes through to whatever the
		// template calls in turn.
		for _, p := range tc.Test.InitialTemplate.Params {
			v, err := xpath.Eval(p.Select,
				xpath.NewContext(nil, xpath.Builtins()), catalogNS{})
			if err != nil {
				return nil, fmt.Errorf("initial-template parameter %s: %w",
					p.Name, err)
			}
			key := xdm.QName{URI: p.URI, Local: p.Local()}.Clark()
			if p.Tunnel == "yes" || p.Tunnel == "true" {
				if opts.InitialTemplateTunnelParams == nil {
					opts.InitialTemplateTunnelParams = map[string]xdm.Sequence{}
				}
				opts.InitialTemplateTunnelParams[key] = v
				continue
			}
			if opts.InitialTemplateParams == nil {
				opts.InitialTemplateParams = map[string]xdm.Sequence{}
			}
			opts.InitialTemplateParams[key] = v
		}
	}
	if tc.Test.InitialMode != nil {
		opts.InitialMode = tc.Test.InitialMode.Name
		// <initial-mode> carries <param> children the same way
		// <initial-template> does: section 2.3.3 gives an apply-templates
		// invocation its own tunnel and non-tunnel parameter sets.
		for _, p := range tc.Test.InitialMode.Params {
			v, err := xpath.Eval(p.Select,
				xpath.NewContext(nil, xpath.Builtins()), catalogNS{})
			if err != nil {
				return nil, fmt.Errorf("initial-mode parameter %s: %w",
					p.Name, err)
			}
			key := xdm.QName{URI: p.URI, Local: p.Local()}.Clark()
			if p.Tunnel == "yes" || p.Tunnel == "true" {
				if opts.InitialModeTunnelParams == nil {
					opts.InitialModeTunnelParams = map[string]xdm.Sequence{}
				}
				opts.InitialModeTunnelParams[key] = v
				continue
			}
			if opts.InitialModeParams == nil {
				opts.InitialModeParams = map[string]xdm.Sequence{}
			}
			opts.InitialModeParams[key] = v
		}
		if sel := tc.Test.InitialMode.Select; sel != "" {
			v, err := xpath.Eval(sel,
				xpath.NewContext(nil, xpath.Builtins()), catalogNS{})
			if err != nil {
				return nil, fmt.Errorf("initial-mode selection %s: %w", sel, err)
			}
			opts.InitialMatchSelection = v
		}
	}
	for _, p := range tc.Test.Params {
		// The static ones went to the compiler; supplying them again here
		// would be supplying a value for a parameter that is no longer a
		// parameter.
		if p.Static == "yes" || p.Static == "true" {
			continue
		}
		v, err := xpath.Eval(p.Select, xpath.NewContext(nil, xpath.Builtins()),
			catalogNS{})
		if err != nil {
			return nil, fmt.Errorf("parameter %s: %w", p.Name, err)
		}
		if opts.Params == nil {
			opts.Params = map[string]xdm.Sequence{}
		}
		opts.Params[p.Name] = v
	}

	// call-template-1002 is a legitimately 1001-deep tail recursion, and
	// DefaultMaxDepth is 1000. The default is a DoS guard for callers running
	// untrusted stylesheets and is right for them; the suite is trusted
	// input, and refusing one of its tests measures the guard rather than the
	// engine. Raised only here, and only far enough that a runaway still
	// terminates rather than exhausting the stack.
	opts.MaxDepth = 5000

	timeout := r.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return ss.Transform(ctx, src, opts)
}

// noNS resolves no prefixes, which is what a parameter expression written in
// the catalog has available.
type noNS struct{}

func (noNS) ResolvePrefix(string) (string, bool) { return "", false }
func (noNS) DefaultElementNamespace() string     { return "" }
func (noNS) DefaultFunctionNamespace() string    { return xdm.NSFN }

// catalogNS resolves the prefixes a catalog's <param>/@select may use.
//
// encoding/xml discards the namespace declarations written on the catalog
// element itself, so the bindings cannot be recovered from the parsed
// structure. The suite declares xs and fn on the elements that need them and
// uses no others in a parameter's select expression, so binding those two is
// enough -- and binding them is what lets static-009a write
// xs:untypedAtomic(23) as a parameter value at all.
type catalogNS struct{}

func (catalogNS) ResolvePrefix(p string) (string, bool) {
	switch p {
	case "xs":
		return xdm.NSXS, true
	case "fn":
		return xdm.NSFN, true
	}
	return "", false
}
func (catalogNS) DefaultElementNamespace() string  { return "" }
func (catalogNS) DefaultFunctionNamespace() string { return xdm.NSFN }

// preloadSources validates the environment's non-principal sources and hands
// the resulting trees to the document resolver.
//
// A <source> with a uri and a validation is a document the stylesheet loads
// itself, by that URI, and expects to find already annotated. Parsing and
// annotating it here — then preloading it under the URI the environment
// declares — is what makes fn:doc return the tree the environment described
// rather than a fresh untyped parse of the same bytes. Node identity comes
// along with it, for the same reason the principal source is preloaded.
//
// The principal source is skipped: it is preloaded by the caller, already
// annotated, and parsing it a second time here would replace the tree the
// transform is about to run on with a different one.
//
// A source that will not parse is skipped rather than reported, exactly as
// annotate skips a schema it cannot load: the transform then reads the file
// through the resolver as it did before, which is the behaviour this replaces
// rather than a new failure.
func (r *Runner) preloadSources(set *TestSet, tc *TestCase, docs *xslt.FileResolver) {
	env := r.environment(set, tc)
	if env == nil {
		return
	}
	for _, s := range env.Sources {
		if s.Role == "." || s.File == "" || s.URI == "" {
			continue
		}
		switch s.Validation {
		case "strict", "lax":
		default:
			// Only a validated source needs the harness to intervene. An
			// unvalidated one is exactly what the resolver would produce on
			// its own, and preloading it would add a second parse for no
			// difference in the tree.
			continue
		}
		p := filepath.Join(set.Dir, filepath.FromSlash(s.File))
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		tree, err := xdm.ParseString(string(stripBOM(data)),
			xdm.ParseOptions{AllowDOCTYPE: true, BaseURI: fileURI(p),
				DocumentURI:      fileURI(p),
				ExternalEntities: r.entityResolver()})
		if err != nil {
			continue
		}
		if err := r.annotate(set, env, s, tree.Root); err != nil {
			continue
		}
		// Under the path the declared URI resolves to. The suite writes the
		// uri relative to the test-set directory and the stylesheet names it
		// the same way, so both arrive at the same file.
		docs.Preload(fileURI(filepath.Join(set.Dir, filepath.FromSlash(s.URI))), tree)
	}
}

// principalSource loads the document the transform starts on.
//
// A test with no source runs from an initial template, which is how a
// stylesheet that generates its own content is tested. Returning nil for that
// case is correct rather than an error.
// The second result is the filesystem path the document came from, empty for
// an inline or computed source. It is used to seed the document resolver's
// cache so that fn:doc of that URI answers this very tree — see
// FileResolver.Preload.
func (r *Runner) principalSource(set *TestSet, tc *TestCase) (*xdm.Node, string, error) {
	env := r.environment(set, tc)
	if env == nil {
		return nil, "", nil
	}
	for _, s := range env.Sources {
		if s.Role != "." {
			continue
		}
		// A source may name the initial context node with an XPath
		// expression instead of, or on top of, a file: role="." select="/doc"
		// starts the transform at an element rather than at the document
		// node, and select="parse-xml('<root/>')" builds the document with no
		// file at all. Ignoring @select started every such case at the wrong
		// node, or -- with nothing else in the environment -- at no node,
		// which surfaced as "source document is nil".
		if s.Select != "" {
			n, err := r.selectedSource(set, env, s)
			return n, "", err
		}
		if s.Content != "" {
			// An inline source has no file of its own, but fn:doc inside the
			// stylesheet still resolves relative names — against the test-set
			// directory, which is where the documents it names live. Without
			// a base they resolve against the process's working directory,
			// which is the xslts package.
			tree, err := xdm.ParseString(s.Content, xdm.ParseOptions{
				AllowDOCTYPE: true,
				// A file: URI rather than a path. fn:base-uri and
				// fn:resolve-uri are defined over URIs, and a bare
				// filesystem path has no scheme, so it is not an
				// absolute URI however absolute the path is.
				// An inline source is a part of the test-set file, so the
				// XML base URI it inherits is that file's own -- which is
				// what backwards-041 asserts with
				// ends-with(base-uri(//item), '_backwards-test-set.xml').
				// A synthesised "inline.xml" beside it resolves relative
				// fn:doc names identically, but answers base-uri() wrongly.
				BaseURI: fileURI(set.Path),
				// An inline source may declare an external entity of its
				// own — copy-1401 writes <!ENTITY extEnt SYSTEM "ent22.xml">
				// in its <content> — and without a resolver the reference is
				// an undeclared-entity parse error rather than the content
				// of the file beside the test set.
				ExternalEntities: r.entityResolver(),
			})
			if err != nil {
				return nil, "", err
			}
			// The initial context is the *document* node: a stylesheet
			// matching "/" needs a root to match, and passing the document
			// element leaves it with none.
			if err := r.annotate(set, env, s, tree.Root); err != nil {
				return nil, "", err
			}
			return tree.Root, "", nil
		}
		if s.File != "" {
			p := filepath.Join(set.Dir, filepath.FromSlash(s.File))
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, "", err
			}
			tree, err := xdm.ParseString(string(stripBOM(data)),
				xdm.ParseOptions{AllowDOCTYPE: true, BaseURI: fileURI(p),
					// Retrieved by URI, so it has a dm:document-uri as well
					// as a base URI and fn:document-uri must report it. A
					// tree a stylesheet builds gets neither, which is the
					// distinction that function exists to make.
					DocumentURI:      fileURI(p),
					ExternalEntities: r.entityResolver()})
			if err != nil {
				return nil, "", err
			}
			if err := r.annotate(set, env, s, tree.Root); err != nil {
				return nil, "", err
			}
			return tree.Root, p, nil
		}
	}
	return nil, "", nil
}

// selectedSource evaluates a source's @select to get the initial context node.
//
// The expression is evaluated with the source's own document as the context
// item when it has one, and with no context item when it does not -- the
// latter is how parse-xml() builds a document from nothing.
func (r *Runner) selectedSource(set *TestSet, env *Environment, s Source) (*xdm.Node, error) {
	var doc *xdm.Node
	switch {
	case s.Content != "":
		tree, err := xdm.ParseString(s.Content, xdm.ParseOptions{
			AllowDOCTYPE:     true,
			BaseURI:          fileURI(set.Path),
			ExternalEntities: r.entityResolver(),
		})
		if err != nil {
			return nil, err
		}
		doc = tree.Root
	case s.File != "":
		p := filepath.Join(set.Dir, filepath.FromSlash(s.File))
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		tree, err := xdm.ParseString(string(stripBOM(data)),
			xdm.ParseOptions{AllowDOCTYPE: true, BaseURI: fileURI(p),
				DocumentURI:      fileURI(p),
				ExternalEntities: r.entityResolver()})
		if err != nil {
			return nil, err
		}
		doc = tree.Root
	}
	if doc != nil {
		if err := r.annotate(set, env, s, doc); err != nil {
			return nil, err
		}
	}
	// A source's @select is the harness's own setup expression, not the
	// stylesheet's, so it is evaluated against the harness library: several
	// environments build their initial context with parse-xml() even when the
	// stylesheet under test is XSLT 2.0. See xpath.RegisterHarnessFuncs.
	ctx := xpath.NewContext(doc, harnessLibrary())
	seq, err := xpath.Eval(s.Select, ctx, noNS{})
	if err != nil {
		return nil, fmt.Errorf("source @select %q: %w", s.Select, err)
	}
	if len(seq) == 0 {
		return nil, fmt.Errorf("source @select %q selected nothing", s.Select)
	}
	n, ok := seq[0].(*xdm.Node)
	if !ok {
		return nil, fmt.Errorf("source @select %q did not select a node", s.Select)
	}
	return n, nil
}

// environment finds the environment a case runs in, following a ref into the
// test set's own definitions.
func (r *Runner) environment(set *TestSet, tc *TestCase) *Environment {
	for i := range tc.Environments {
		e := &tc.Environments[i]
		if e.Ref == "" {
			return e
		}
		for j := range set.Environments {
			if set.Environments[j].Name == e.Ref {
				return &set.Environments[j]
			}
		}
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// fileURI turns an absolute filesystem path into a file: URI.
//
// The distinction matters to fn:base-uri and fn:resolve-uri, which are defined
// over URIs: "/a/b.xml" is an absolute *path* but a relative *URI reference*,
// having no scheme, and FORG0002 is the correct answer for it. Only the
// documents the suite parses get one; the resolvers are given paths, because
// they join with filepath and a URI turns into a directory called "file:".
func fileURI(path string) string {
	if path == "" || strings.HasPrefix(path, "file:") {
		return path
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	return "file://" + filepath.ToSlash(path)
}

// annotate validates a source against the environment's schema so that the
// tree carries type annotations.
//
// A source declared validation="strict" is meant to reach the transform
// already validated: that is what makes "$v instance of my:partNumberType"
// answer true for a value read out of it, and what the whole notation, type
// and import-schema group of tests depends on. Loading the document without
// validating it leaves every node untyped, so those tests measured nothing
// but the absence of annotations.
//
// A validation failure is not reported. The suite includes documents that are
// deliberately invalid, and refusing to run them would turn a test about what
// the stylesheet does into an error about its input. Whatever annotations the
// validator managed to stamp are kept.
func (r *Runner) annotate(set *TestSet, env *Environment, s Source, root *xdm.Node) error {
	switch s.Validation {
	case "strict", "lax":
	default:
		return nil
	}
	schema := envSchema(set, env)
	if schema == nil {
		return nil
	}
	// The validator stamps annotations as it goes, so the error is discarded
	// rather than propagated, for the reason given above.
	_ = schema.Validate(root, xsd.ValidateOptions{Annotate: true})
	return nil
}

// envSchema loads and merges every schema an environment declares.
//
// The environment's <schema> elements are what both the source validation in
// annotate and the static context an assertion is evaluated in need: the
// suite states the schema once, on the environment, and the stylesheet
// imports the same file by relative path. Loading it in one place is what
// keeps the two from disagreeing about which components are in scope.
//
// A schema that fails to load is skipped rather than reported. The suite
// includes schemas this validator does not accept, and refusing the whole
// test for one of them measures the loader rather than the transform.
//
// nil is returned when the environment declares none, or when nothing loaded
// — callers treat that as "no schema in the static context", which is the
// same answer they gave before any schema was consulted at all.
func envSchema(set *TestSet, env *Environment) *xsd.Schema {
	if env == nil || len(env.Schemas) == 0 {
		return nil
	}
	schema := xsd.NewSchema()
	for _, sc := range env.Schemas {
		if sc.File == "" {
			continue
		}
		p := filepath.Join(set.Dir, filepath.FromSlash(sc.File))
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		tree, err := xdm.ParseString(string(stripBOM(data)), xdm.ParseOptions{})
		if err != nil {
			continue
		}
		loaded, err := xsd.Load(tree.Root, p, xsd.Options{Resolver: &xsd.FileResolver{}})
		if err != nil || loaded == nil {
			continue
		}
		mergeInto(schema, loaded)
	}
	if len(schema.Elements) == 0 && len(schema.Types) == 0 {
		return nil
	}
	return schema
}

// mergeInto folds one schema's global components into another.
func mergeInto(dst, src *xsd.Schema) {
	for n, t := range src.Types {
		if _, ok := dst.Types[n]; !ok {
			dst.Types[n] = t
		}
	}
	for n, d := range src.Elements {
		if _, ok := dst.Elements[n]; !ok {
			dst.Elements[n] = d
		}
	}
	for n, a := range src.Attributes {
		if _, ok := dst.Attributes[n]; !ok {
			dst.Attributes[n] = a
		}
	}
}

// catalogCollections resolves fn:collection from the environment's
// declarations.
//
// The suite states a collection's membership explicitly rather than leaving it
// to be discovered, so this reads the declaration rather than the directory:
// two environments may name overlapping sets of the same files, and only the
// declaration says which documents belong to which URI.
type catalogCollections struct {
	set   *TestSet
	decls []Collection
}

// ResolveCollection returns the documents in the named collection.
func (c *catalogCollections) ResolveCollection(uri, base string) (xdm.Sequence, error) {
	// The URI is matched on its last path segment rather than in full. The
	// stylesheet writes it relative to itself and the engine has already
	// resolved it against the stylesheet's base, while the catalog writes it
	// relative to the test-set file; comparing the resolved form against the
	// declared form would never match even though both name one collection.
	want := collectionKey(uri)
	for _, d := range c.decls {
		if collectionKey(d.URI) != want {
			continue
		}
		var out xdm.Sequence
		for _, s := range d.Sources {
			// A source may name a fragment, which selects one element out of
			// the document rather than the document node. Splitting it here
			// keeps the filename openable.
			file, frag := splitFragment(s.File)
			if file == "" {
				continue
			}
			p := filepath.Join(c.set.Dir, filepath.FromSlash(file))
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			tree, err := xdm.ParseString(string(stripBOM(data)),
				xdm.ParseOptions{AllowDOCTYPE: true, BaseURI: fileURI(p)})
			if err != nil {
				return nil, err
			}
			if frag == "" {
				out = append(out, tree.Root)
				continue
			}
			if n := findByID(tree.Root, frag); n != nil {
				out = append(out, n)
			}
		}
		return out, nil
	}
	// A collection URI the environment does not declare is empty rather than
	// an error: the environment is the whole statement of what exists, so a
	// name absent from it names nothing.
	return nil, nil
}

// collectionKey reduces a collection URI to the part two spellings of it
// share: the final path segment, without any fragment.
func collectionKey(uri string) string {
	uri, _ = splitFragment(uri)
	return lastSegment(uri)
}

// splitFragment separates a URI reference from its fragment identifier.
func splitFragment(uri string) (string, string) {
	if i := strings.IndexByte(uri, '#'); i >= 0 {
		return uri[:i], uri[i+1:]
	}
	return uri, ""
}

// findByID returns the element carrying the given xml:id or ID-typed value.
func findByID(n *xdm.Node, id string) *xdm.Node {
	if n == nil {
		return nil
	}
	if n.Kind == xdm.KindElement {
		for _, a := range n.Attrs {
			if a.Name.Local == "id" && a.Value == id {
				return n
			}
		}
	}
	for _, ch := range n.Children {
		if got := findByID(ch, id); got != nil {
			return got
		}
	}
	return nil
}

// pathSchemaResolver resolves xsl:import-schema against the filesystem after
// turning a file: base URI back into a path.
//
// The stylesheet's base URI is a URI, because fn:static-base-uri and
// fn:resolve-uri are defined over URIs and a bare path has no scheme. The
// schema resolver, though, joins the base with filepath.Dir, which reads
// "file:" as a directory name. Stripping the scheme here keeps both true
// rather than making one of them wrong.
type pathSchemaResolver struct{}

func (pathSchemaResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	return (&xsd.FileResolver{}).Resolve(namespace, location, uriToPath(base))
}

// uriToPath turns a file: URI back into a filesystem path, leaving anything
// else alone.
func uriToPath(s string) string {
	for _, p := range []string{"file://", "file:"} {
		if strings.HasPrefix(s, p) {
			return filepath.FromSlash(strings.TrimPrefix(s, p))
		}
	}
	return s
}

// envSchemaResolver prefers the schemas an environment declares with
// role="stylesheet-import" over the schema-location hint written on
// xsl:import-schema.
//
// schema-location is a hint: XSLT 2.0 section 3.14 and XML Schema alike let a
// processor use a schema it already has for the namespace instead. Several
// tests rely on that — import-schema-186 names testSchemaInline.xsd, whose
// target namespace is not the one the declaration imports, and only the
// environment's schema002.xsd actually declares the components the stylesheet
// then names.
type envSchemaResolver struct {
	set *TestSet
	env *Environment
}

func (e envSchemaResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	if e.env != nil && namespace != "" {
		for _, sc := range e.env.Schemas {
			if sc.File == "" {
				continue
			}
			p := filepath.Join(e.set.Dir, filepath.FromSlash(sc.File))
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if schemaTargetNamespace(data) != namespace {
				continue
			}
			return io.NopCloser(bytes.NewReader(data)), p, nil
		}
	}
	return pathSchemaResolver{}.Resolve(namespace, location, base)
}

// schemaTargetNamespace reads the targetNamespace attribute off a schema
// document without parsing it fully, which is all the resolver needs to decide
// whether a candidate file answers the namespace being imported.
func schemaTargetNamespace(data []byte) string {
	tree, err := xdm.ParseString(string(stripBOM(data)), xdm.ParseOptions{})
	if err != nil || tree.Root == nil {
		return ""
	}
	for _, el := range tree.Root.ChildElements() {
		if el.IsElement(xsd.NSSchema, "schema") {
			return el.AttrValue("targetNamespace")
		}
	}
	return ""
}

// harnessLibrary is the function library the harness evaluates its OWN
// expressions with -- environment @select setup expressions and the like.
// It is the XPath 2.0 builtins plus the few XPath 3.0 functions the W3C test
// catalogue writes its setup in. A stylesheet under test never sees it.
func harnessLibrary() *xpath.Library {
	lib := xpath.NewLibrary(xpath.Builtins())
	xpath.RegisterHarnessFuncs(lib)
	return lib
}
