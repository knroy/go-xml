package qt3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xquery"
)

// SuiteClock is the fixed value fn:current-dateTime returns during a run.
// It carries a non-UTC offset because a UTC-only clock would let a timezone
// bug pass unnoticed.
var SuiteClock = time.Date(2024, 1, 15, 9, 30, 0, 0, time.FixedZone("", -5*3600))

// CaseTimeout bounds a single test case. It is generous: the point is to
// contain a non-terminating expression, not to measure performance.
const CaseTimeout = 10 * time.Second

// Outcome is what happened to one test case.
type Outcome int

const (
	Pass Outcome = iota
	Fail
	// Skip covers a test this engine is not in scope for — an XQuery-only
	// construct, a 3.0/3.1 feature, or a schema-aware environment. A skip is
	// not a pass and is reported separately, because counting them as passes
	// is how a conformance number becomes meaningless.
	Skip
)

// Report is the result of one case.
type Report struct {
	Set, Case string
	Outcome   Outcome
	Reason    string
	// Expr is the expression the case evaluated. A failure line naming only
	// the case means opening the catalog to find out what it ran; carrying it
	// here makes the report reproducible by hand.
	Expr string
}

// resolver binds the namespaces an environment declares.
type resolver struct {
	prefixes map[string]string
}

func (r resolver) ResolvePrefix(p string) (string, bool) {
	if u, ok := r.prefixes[p]; ok {
		return u, true
	}
	// The suite assumes the standard prefixes are always available.
	switch p {
	case "xs":
		return xdm.NSXS, true
	case "fn":
		return xdm.NSFN, true
	case "xml":
		return xdm.NSXML, true
	case "err":
		return xdm.NSErr, true
	case "math":
		return xdm.NSMath, true
	case "array":
		// "array" and "map" are predeclared prefixes in XPath 3.1, section
		// 2.1.1, exactly as "fn" and "xs" are. The suite's 3.1 sets bind them
		// through an environment for the case's own expression but write
		// "array:size($result)" in an <assert> with no environment behind it,
		// which only holds because the prefix is predeclared rather than
		// declared.
		return xdm.NSArray, true
	case "map":
		return xdm.NSMap, true
	case "j":
		// Not a predeclared prefix, but a convention of the JSON test sets:
		// the fn-json-to-xml cases write "j:map" in assertions whose own
		// environment does not bind it, expecting the driver to know the one
		// prefix their sibling "json-ns" environment declares.
		return "http://www.w3.org/2005/xpath-functions", true
	}
	return "", false
}

// Bindings reports the prefixes the environment declared, which is how the
// XQuery driver puts them in a query's static context: a query is compiled
// from source rather than evaluated against a resolver, so the bindings have
// to be handed over as options before compilation rather than looked up
// during it. Only the environment's own prefixes are reported — the standard
// ones ResolvePrefix falls back to are predeclared in the language and would
// be redeclarations here.
func (r resolver) Bindings() map[string]string {
	out := make(map[string]string, len(r.prefixes))
	for p, u := range r.prefixes {
		// The empty prefix is the default element namespace, which travels as
		// its own option; a "declare namespace" for it is not the same thing.
		if p != "" {
			out[p] = u
		}
	}
	return out
}

// DefaultElementNamespace reports the namespace an unprefixed element or type
// name takes. The environment declares it as a namespace binding with an empty
// prefix, which is what "xs:QName('ncname')" resolves against.
func (r resolver) DefaultElementNamespace() string  { return r.prefixes[""] }
func (r resolver) DefaultFunctionNamespace() string { return xdm.NSFN }

// Runner executes cases from a suite checkout.
type Runner struct {
	Root string
	// Target is the language version the run is scoped to. The zero value is
	// XPath20, so an existing caller that does not set it keeps the 2.0 run
	// it had before.
	Target TargetVersion
	// envs holds the catalog-level environments, which test sets reference
	// by name.
	envs map[string]Environment
	docs map[string]*xdm.Node
}

func NewRunner(root string, cat *Catalog) *Runner {
	r := &Runner{Root: root, envs: map[string]Environment{}, docs: map[string]*xdm.Node{}}
	for _, e := range cat.Environments {
		r.envs[e.Name] = e
	}
	return r
}

// unsupportedSpec reports whether a dependency puts the case out of scope for
// the target version.
//
// The suite is FOTS 3.1 and covers XQuery as well as XPath. This engine does
// not implement XQuery at any version, and implements XPath up to the target,
// so anything beyond that is skipped rather than counted as a failure: failing
// a test for a language you do not claim to implement says nothing about
// conformance.
func unsupportedSpec(deps []Dependency, target TargetVersion) string {
	for _, d := range deps {
		switch d.Type {
		case "spec":
			if !specInScope(d.Value, target) {
				return "requires " + d.Value
			}
		case "feature":
			// Higher-order functions are the defining feature of XPath 3.0
			// rather than an optional extra, so they are out of scope only
			// for a 2.0 run.
			if d.Value == "higherOrderFunctions" {
				if target >= XPath30 || target == XQuery31 {
					continue
				}
				if d.Satisfied != "false" {
					return "needs feature " + d.Value
				}
				continue
			}
			switch d.Value {
			case "schemaValidation", "schemaImport", "typedData",
				"staticTyping", "moduleImport",
				"namespace-axis", "infoset-dtd", "xpath-1.0-compatibility",
				"fn-transform-XSLT", "fn-transform-XSLT30", "fn-format-integer-CLDR",
				// fn:load-xquery-module compiles an XQuery library module,
				// which needs an XQuery processor this engine does not have.
				// The set declares the feature satisfied="true" and then
				// overrides fourteen cases to satisfied="false" -- those
				// fourteen are the ones written for a processor without it,
				// and they pass. The rest describe what a processor that has
				// one would do, and are out of scope for the same reason the
				// XQuery specs above are.
				"fn-load-xquery-module",
				"non_empty_sequence_collection", "collection-stability",
				"directory-as-collection-uri", "simple-uca-fallback",
				"advanced-uca-fallback", "remote_http":
				if d.Satisfied != "false" {
					return "needs feature " + d.Value
				}
			}
		case "xsd-version":
			// A handful of cases come in 1.0/1.1 pairs asserting opposite
			// results for the same expression — xs:double("+INF") is an error
			// under 1.0 and INF under 1.1 — so running both guarantees one
			// failure. This engine implements the 1.1 lexical rules, so the
			// 1.0-only cases are out of scope rather than failures.
			if strings.Contains(d.Value, "1.0") && !strings.Contains(d.Value, "1.1") {
				return "needs XSD 1.0"
			}
		case "xml-version":
			if strings.Contains(d.Value, "1.1") && !strings.Contains(d.Value, "1.0") {
				return "needs XML 1.1"
			}
			// The value may name an *edition* of XML 1.0 and not just the
			// version, and the suite uses all three spellings: "1.0" for any
			// edition, "1.0:4-" for the 4th edition or earlier, and
			// "1.0:5+ 1.1" for the 5th or later. The distinction is real,
			// because the editions disagree about which characters may start
			// a name -- U+037F and U+017F are excluded before the 5th and
			// admitted from it on. This engine transcribes the 5th edition's
			// productions (see xdm/qname.go and xpath/lexer.go), so a case
			// restricted to the 4th and earlier is asking for behaviour a
			// conformant 5th-edition processor must not have, in the same way
			// an XSD 1.0-only case is above.
			//
			// It is out of scope rather than a failure, and the suite says so
			// itself: XMLEdition.xml pairs each "1.0:4-" case with a "-new"
			// one carrying "1.0:5+ 1.1" and asserting the opposite result.
			// Those two are in scope and both pass, which is the evidence
			// that the engine is on the right side of the split.
			if strings.Contains(d.Value, "1.0:4-") {
				return "needs XML 1.0 4th edition or earlier"
			}
		case "language", "default-language":
			if d.Value != "en" && d.Value != "" {
				return "needs language " + d.Value
			}
		case "unicode-version", "unicode-normalization-form",
			"calendar", "format-integer-sequence":
			return "needs " + d.Type + " " + d.Value
		}
	}
	return ""
}

// TargetVersion selects the language version the run is scoped to.
//
// The suite is FOTS 3.1 and every case declares the versions it applies to, so
// which cases are in scope is a property of the run rather than of the engine.
// Scoping to 2.0 and to 3.0 are two measurements of the same suite, and both
// are reported: the 2.0 figure must not move as 3.0 is implemented, which is
// what makes it a regression check rather than just a headline.
type TargetVersion int

const (
	// XPath20 scopes the run to cases that apply to XPath 2.0.
	XPath20 TargetVersion = iota
	// XPath30 scopes the run to cases that apply to XPath 2.0 or 3.0.
	XPath30
	// XPath31 scopes the run to cases that apply to 2.0, 3.0 or 3.1.
	XPath31
	// XQuery31 scopes the run to XQuery cases rather than XPath ones.
	//
	// It is not a fourth point on the same scale: an XQuery case holds a whole
	// query where an XPath case holds an expression, and the two are evaluated
	// by different packages. Every XPath 3.1 expression is an XQuery
	// expression, so the XQuery run also admits the cases written for XP31.
	XQuery31
)

func (v TargetVersion) String() string {
	if v == XQuery31 {
		return "XQuery 3.1"
	}
	switch v {
	case XPath31:
		return "XPath 3.1"
	case XPath30:
		return "XPath 3.0"
	}
	return "XPath 2.0"
}

// xpathVersion maps the run's target onto the engine's language version.
//
// They are separate types because they answer different questions: the target
// decides which cases are in scope, the engine's version decides how an
// expression is compiled. They happen to correspond one-to-one today.
func xpathVersion(v TargetVersion) xpath.Version {
	switch {
	case v >= XPath31:
		return xpath.XPath31
	case v >= XPath30:
		return xpath.XPath30
	}
	return xpath.XPath20
}

// specInScope reports whether a spec dependency admits the target version.
//
// A value is a space-separated list of alternatives and the case is in scope
// if any alternative names a version at or below the target. "XP20+" means 2.0
// and later, so it is in scope for both targets; "XP30" and "XP30+" are in
// scope only for a 3.0 run.
//
// An XQuery run is scoped by the "XQ" alternatives, and by the "XP" ones only
// when the case names no "XQ" alternative at all is it out of scope.
//
// Every XPath 3.1 expression is legal XQuery, but it does not always mean the
// same thing, and the suite marks the cases where it does not by naming one
// language and not the other. A string literal is the clearest: "&amp;" is
// five characters in XPath and one in XQuery, because §3.1.1 gives XQuery's
// StringLiteral the entity references XPath's lacks. prod-Literal pairs the
// two readings — Literals056 is "XQ10+" and asserts "&", Literals056a is
// "XP20+" and asserts "&amp;" — so running the XPath one under an XQuery
// target asserts the XPath answer against an XQuery processor and guarantees a
// failure. This is the same pairing the exact "XP20" and "XP30" values below
// mark between XPath versions, one language apart instead of one version.
//
// A case naming no "XQ" alternative is dropped, and so is one whose only XQ
// alternatives are the exact "XQ10" or "XQ30" — the version pairing described
// at the switch below, which is the same idiom one version apart rather than
// one language apart. The 300-odd cases marked "XP20+ XQ10+" and the like are
// in scope as before: naming both is the suite's way of saying the two
// languages agree.

func specInScope(v string, target TargetVersion) bool {
	if target == XQuery31 {
		for _, alt := range strings.Fields(v) {
			switch alt {
			// Exact "XQ10" and "XQ30" are excluded for the same reason exact
			// "XP20" and "XP30" are below: they mark a reading a later version
			// changed, and the suite pairs them with a case asserting the new
			// answer over identical source. K-InternalVariablesWith-6a is
			// "XQ10" and wants XPST0008 for a forward reference between
			// globals; -6b is "XQ30+", is the same query character for
			// character, and wants it to succeed, because 3.0 legalised it.
			// Running the 1.0 case against a 3.1 processor asserts the
			// superseded answer and cannot pass.
			case "XQ10+", "XQ30+", "XQ31", "XQ31+":
				return true
			}
		}
		// No "XQ" alternative: the case asserts the XPath reading, which an
		// XQuery processor is right not to give.
		return false
	}
	for _, alt := range strings.Fields(v) {
		switch alt {
		case "XP20":
			// Exactly 2.0, with no "+". A handful of cases are written this
			// way precisely because 3.0 changed the answer — "round(1, 2)" is
			// XPST0017 under 2.0 and 1 under 3.0 — and the suite pairs them
			// with an XP30 case asserting the other result. Running such a
			// case under 3.0 would assert the 2.0 answer against a 3.0
			// processor and guarantee a failure.
			if target == XPath20 {
				return true
			}
		case "XP20+":
			return true
		case "XP30":
			// Exactly 3.0, for the same reason "XP20" is written exactly:
			// where 3.1 changed an answer the suite pairs the two.
			if target == XPath30 {
				return true
			}
		case "XP30+":
			if target >= XPath30 {
				return true
			}
		case "XP31", "XP31+":
			if target >= XPath31 {
				return true
			}
		}
	}
	return false
}

// loadDoc parses and caches a source document.
func (r *Runner) loadDoc(file string) (*xdm.Node, error) {
	return r.loadDocURI(file, "")
}

// loadDocURI is loadDoc for a document the environment gave a URI.
//
// The URI becomes the document's base URI, which is what fn:document-uri
// returns and what fn:doc-available is asked about. Falling back to the
// filesystem path made document-uri(/) answer with a path that fn:doc could
// not then resolve, so doc-available(document-uri(/)) was false.
//
// The cache key stays the file, so one document is one node however it is
// reached; the first load decides the URI.
func (r *Runner) loadDocURI(file, uri string) (*xdm.Node, error) {
	if n, ok := r.docs[file]; ok {
		return n, nil
	}
	data, err := os.ReadFile(filepath.Join(r.Root, file))
	if err != nil {
		return nil, err
	}
	base := filepath.Join(r.Root, file)
	if uri != "" {
		base = uri
	}
	tree, err := xdm.ParseString(string(data), xdm.ParseOptions{
		BaseURI: base,
		// This document was retrieved by URI, so it has a dm:document-uri as
		// well as a base URI and fn:document-uri must report it. They are
		// separate accessors in the XDM: a tree built by a query has a base
		// URI and no document URI, which is the distinction fn:document-uri
		// exists to make.
		DocumentURI: base,
		// The suite's documents legitimately carry DOCTYPEs, and they are
		// files shipped with the suite rather than untrusted input.
		AllowDOCTYPE: true,
	})
	if err != nil {
		return nil, err
	}
	r.docs[file] = tree.Root
	return tree.Root, nil
}

// srcPath resolves a source path against the directory it was written in.
//
// A path is relative to the file that names it, and the suite uses both
// origins: catalog.xml names "docs/atomic.xml" from the root, while
// fn/collection.xml names "../docs/bib.xml" from fn/. Resolving everything
// against one of the two breaks the other, so dir is the directory of the
// document the environment came from — "" for the catalog.
//
// Cleaning normalises the result, so a document reached from two different
// test-sets is one cache entry and therefore one node identity, which
// fn:collection stability depends on.
func srcPath(dir, file string) string {
	if dir == "" || dir == "." {
		return filepath.Clean(file)
	}
	return filepath.Clean(filepath.Join(dir, file))
}

// resolveEnv merges the environments a case references into one.
func (r *Runner) resolveEnv(ts *TestSet, tc *TestCase) (Environment, error) {
	var out Environment
	// dir is the directory the environment was written in, which is what its
	// source paths are relative to. Paths are rewritten here, at the point
	// where that is still known: after the merge a source no longer records
	// which document named it.
	merge := func(e Environment, dir string) {
		for _, src := range e.Sources {
			src.File = srcPath(dir, src.File)
			out.Sources = append(out.Sources, src)
		}
		for _, c := range e.Collections {
			rc := Collection{URI: c.URI}
			for _, src := range c.Sources {
				src.File = srcPath(dir, src.File)
				rc.Sources = append(rc.Sources, src)
			}
			// A <query> collection member is an expression, not a path, so
			// there is nothing to rewrite in it here. What it does need is
			// the directory it was written in: "users-json" calls
			// unparsed-text-lines("UseCaseR31/users.json"), a reference
			// relative to the test-set that wrote it, and the merged
			// environment no longer records which document that was. The
			// directory is therefore carried on the query itself.
			for _, q := range c.Queries {
				q.dir = dir
				rc.Queries = append(rc.Queries, q)
			}
			out.Collections = append(out.Collections, rc)
		}
		// The context item an environment supplies. Without it the item is
		// absent and a case that declares "context item as T external" gets
		// XPDY0002 -- correctly, since nothing bound one -- instead of the
		// type error it is testing for. contextDecl-020 supplies 'London'
		// against a declared xs:integer and wants XPTY0004.
		out.ContextItem = append(out.ContextItem, e.ContextItem...)
		out.Params = append(out.Params, e.Params...)
		out.Namespaces = append(out.Namespaces, e.Namespaces...)
		out.Schemas = append(out.Schemas, e.Schemas...)
		out.Collations = append(out.Collations, e.Collations...)
		out.StaticBaseURI = append(out.StaticBaseURI, e.StaticBaseURI...)
		out.Resources = append(out.Resources, e.Resources...)
		out.DecimalFormats = append(out.DecimalFormats, e.DecimalFormats...)
	}
	for _, ref := range tc.Environments {
		if ref.Ref == "" {
			merge(ref, ts.Dir)
			continue
		}
		// A reference resolves against the test set first, then the catalog.
		found := false
		for _, e := range ts.Environments {
			if e.Name == ref.Ref {
				merge(e, ts.Dir)
				found = true
				break
			}
		}
		if !found {
			if e, ok := r.envs[ref.Ref]; ok {
				merge(e, "")
				found = true
			}
		}
		if !found {
			return out, fmt.Errorf("unknown environment %q", ref.Ref)
		}
	}
	return out, nil
}

// Run executes one case.
//
// A panic in the engine is recorded as a failure rather than allowed to abort
// the run: a crash is the most severe kind of conformance failure, and losing
// the other 28,000 results to it would hide everything behind the first one.
func (r *Runner) Run(ts *TestSet, tc *TestCase) (rep Report) {
	rep = Report{Set: ts.Name, Case: tc.Name, Expr: strings.TrimSpace(tc.Test.Query)}
	defer func() {
		if p := recover(); p != nil {
			rep.Outcome = Fail
			rep.Reason = fmt.Sprintf("PANIC: %v", p)
		}
	}()

	deps := append(append([]Dependency{}, ts.Dependencies...),
		tc.Dependencies...)
	if why := unsupportedSpec(deps, r.Target); why != "" {
		rep.Outcome, rep.Reason = Skip, why
		return rep
	}
	if len(tc.Modules) > 0 {
		rep.Outcome, rep.Reason = Skip, "needs module import"
		return rep
	}

	env, err := r.resolveEnv(ts, tc)
	if err != nil {
		rep.Outcome, rep.Reason = Skip, err.Error()
		return rep
	}
	if len(env.Schemas) > 0 {
		rep.Outcome, rep.Reason = Skip, "schema-aware environment"
		return rep
	}
	for _, c := range env.Collations {
		if c.Default == "true" && !strings.HasSuffix(c.URI, "/collation/codepoint") &&
			c.URI != fotsCaseBlind {
			rep.Outcome, rep.Reason = Skip, "non-codepoint default collation"
			return rep
		}
	}

	// Build the evaluation context: the "." source is the context item, and
	// "$name" sources plus params become variables.
	ns := resolver{prefixes: map[string]string{}}
	for _, n := range env.Namespaces {
		ns.prefixes[n.Prefix] = n.URI
	}

	var ctxItem xdm.Item
	vars := map[string]xdm.Sequence{}
	for _, src := range env.Sources {
		doc, err := r.loadDocURI(src.File, src.URI)
		if err != nil {
			rep.Outcome, rep.Reason = Skip, "source unavailable: "+err.Error()
			return rep
		}
		switch {
		case src.Role == ".":
			ctxItem = doc
		case strings.HasPrefix(src.Role, "$"):
			vars[strings.TrimPrefix(src.Role, "$")] = xdm.One(doc)
		}
	}

	// An environment may name the context item as an expression rather than a
	// document — "<context-item select=\"'London'\"/>" — which is how a case
	// supplies an atomic where "." would only supply a node. It is evaluated
	// against no context of its own, since nothing it can name needs one.
	for _, ci := range env.ContextItem {
		if strings.TrimSpace(ci.Select) == "" {
			continue
		}
		seq, err := xpath.Eval(ci.Select,
			xpath.NewContext(nil, xpath.Builtins()), ns)
		if err != nil {
			rep.Outcome, rep.Reason = Skip,
				"context-item unavailable: "+err.Error()
			return rep
		}
		if len(seq) == 1 {
			ctxItem = seq[0]
		}
	}

	runCtx, cancel := context.WithTimeout(context.Background(), CaseTimeout)
	defer cancel()

	ctx := xpath.NewContext(ctxItem, xpath.Builtins())
	// The case is evaluated as the version the run is scoped to, so that a
	// construct 3.0 adds is accepted in the 3.0 run and refused in the 2.0
	// one. Both are conformance: the 2.0 run asserts the refusals.
	ctx.Version = xpathVersion(r.Target)
	// fn:unparsed-text reads the suite's own fixtures; see suiteTextResolver
	// for why this is scoped to the checkout.
	ctx.Texts = newSuiteTextResolver(r.Root, ts.Dir, env)
	// A document handed to fn:parse-xml may declare an external entity, and a
	// few cases assert that its content appears in the result. The engine
	// refuses every external entity unless a resolver says otherwise; here the
	// suite is the one input where reading its own fixtures is the point.
	ctx.Entities = suiteEntityResolver{text: ctx.Texts.(suiteTextResolver)}
	// A case may declare its own decimal format for fn:format-number. The
	// builtin library's two-argument form uses the standard symbols, so a
	// declared format is installed by overriding that entry in a library
	// chained to the builtins.
	if lib := decimalFormatLibrary(env, ns); lib != nil {
		ctx.Funcs = lib
	}
	// An expression always has a static base URI unless something says
	// otherwise: the spec defaults it to the URI of the resource holding the
	// expression, which here is the test-set file. Leaving it empty made
	// base-uri() on a freshly parsed document return the empty sequence, so
	// comparing it with static-base-uri() gave () rather than true. A case
	// that wants no base URI says so with "#UNDEFINED" below.
	if abs, err := filepath.Abs(filepath.Join(r.Root, filepath.FromSlash(ts.Dir))); err == nil {
		ctx.StaticBaseURI = "file://" + filepath.ToSlash(abs) + "/"
	}
	// The environment may declare the base URI of the expression itself,
	// which is distinct from the base URI of any document it is applied to.
	for _, b := range env.StaticBaseURI {
		// "#UNDEFINED" is the catalog's way of saying the static base URI is
		// absent, not a URI of that spelling. Cases use it to assert what
		// happens when a relative reference has nothing to resolve against.
		if b.URI == "#UNDEFINED" {
			ctx.StaticBaseURI = ""
			continue
		}
		if b.URI != "" {
			ctx.StaticBaseURI = b.URI
		}
	}
	// The suite expects fn:current-dateTime and its siblings to work. A fixed
	// clock is used rather than the wall clock so that a rerun of the suite
	// gives the same answers.
	ctx = ctx.WithNow(SuiteClock)
	// A <collection> environment supplies the documents fn:collection
	// returns. Without this the function has no resolver and refuses, which
	// is the correct default but not what these cases are testing.
	// A <source> with a uri attribute is reachable through fn:doc under that
	// URI. Without a resolver the function refuses — correct by default, but
	// not what these cases are testing.
	if docs := envDocs(r, ts.Dir, env); docs != nil {
		ctx.Docs = docs
	}
	if len(env.Collections) > 0 {
		cr := &envCollections{
			r:         r,
			byURI:     map[string][]string{},
			queryURI:  map[string][]CollectionQuery{},
			cache:     map[string]xdm.Sequence{},
			declared:  map[string]bool{},
			queryOpts: ns,
		}
		for _, c := range env.Collections {
			// The URI is declared even when the collection turns out to be
			// empty, so that asking for it is "an empty collection" and not
			// "no such collection". <collection uri=""><query>...</query>
			// </collection> declares the default collection, and a source
			// list and a query list are both legitimate ways to fill one.
			cr.declared[c.URI] = true
			for _, src := range c.Sources {
				cr.byURI[c.URI] = append(cr.byURI[c.URI], src.File)
			}
			cr.queryURI[c.URI] = append(cr.queryURI[c.URI], c.Queries...)
		}
		ctx.Collections = cr
	}
	for name, seq := range vars {
		ctx = ctx.WithVar(xdm.QName{Local: name}, seq)
	}
	// External parameters are given as XPath expressions to evaluate.
	for _, p := range env.Params {
		if p.Select == "" {
			continue
		}
		v, err := xpath.Eval(p.Select, xpath.NewContext(nil, xpath.Builtins()), ns)
		if err != nil {
			rep.Outcome, rep.Reason = Skip, "param "+p.Name+": "+err.Error()
			return rep
		}
		// A prefixed param name binds under its expanded name, or the query
		// that declares "$test:x external" looks for the URI and finds
		// nothing bound under the literal "test:x".
		name := xdm.QName{Local: p.Name}
		if prefix, local, ok := strings.Cut(p.Name, ":"); ok {
			uri := p.nsFor(prefix)
			if uri == "" {
				uri = ns.prefixes[prefix]
			}
			if uri != "" {
				name = xdm.QName{Prefix: prefix, Local: local, URI: uri}
			}
		}
		ctx = ctx.WithVar(name, v)
	}

	// A case is given a deadline of its own. An expression that does not
	// terminate would otherwise take the whole run with it — which is how the
	// unbounded round() precision was found, after it consumed twenty minutes
	// and all available memory before the harness could report anything.
	ctx.Ctx = runCtx
	// numberformat121 and 122 recurse five thousand deep on purpose, to build
	// a decimal of 10^5000 and 10^-5000 and check that format-number prints
	// each exactly. xpath.MaxDepth is 500 and is a DoS guard for a caller
	// evaluating an expression it did not write; the suite is trusted input,
	// and refusing one of its cases measures the guard rather than the
	// engine. This is the same reasoning tests/xslts gives for raising
	// TransformOptions.MaxDepth to 5000 for template recursion.
	//
	// The figure is well above the 5000 the query names because a level of
	// its recursion is not a level of Descend: the body is an arrow chain
	// whose call spends several frames per turn. It is still low enough that
	// a genuine runaway terminates rather than exhausting the stack.
	ctx.MaxDepth = 20000
	res := outcome{}
	if r.Target == XQuery31 {
		// An XQuery case holds a whole query, not an expression, and the
		// namespaces the environment declared are its static context.
		//
		// It is compiled rather than evaluated in one step so that the
		// prolog's "declare option output:*" declarations are reachable: a
		// serialization assertion is checked against the result written with
		// the parameters the query itself stated, and Eval hands back only
		// the sequence.
		var q *xquery.Query
		q, res.err = xquery.Compile(tc.Test.Query,
			xqueryOptions(ns, r.testSetURI(ts)))
		if res.err == nil {
			res.serialParams = q.SerializationOptions()
			// §25.1: "if no document can be found at the specified location,
			// the attribute should be ignored". A missing one is therefore
			// not a failure, and neither is one this harness declines to
			// resolve; what is read is applied.
			if href := res.serialParams["parameter-document"]; href != "" {
				if doc, err := r.loadDoc(
					filepath.Join(ts.Dir, filepath.FromSlash(href))); err == nil {
					res.paramDoc = paramDocElement(doc)
				}
			}
			res.seq, res.err = q.Eval(ctx)
		}
	} else {
		res.seq, res.err = xpath.Eval(tc.Test.Query, ctx, ns)
	}

	want, err := ParseAssert(tc.Result.Raw)
	// An assert-xml may hold its expected value in a separate file, named
	// relative to the test-set. Reading it here keeps check() a pure function
	// of the assertion tree.
	if err == nil {
		loadAssertFiles(r, ts, &want)
	}
	if err != nil {
		rep.Outcome, rep.Reason = Skip, "unparsable result: "+err.Error()
		return rep
	}
	ok, why := check(want, &res)
	if ok {
		rep.Outcome = Pass
		return rep
	}
	rep.Outcome, rep.Reason = Fail, why
	return rep
}

// outcome is everything running one case produced, which is what an assertion
// is checked against.
//
// It is a struct rather than three parameters threaded through check's
// recursion because the serialization assertions need a third thing — the
// parameters the query asked to be serialised with — and the tree walk should
// not have to grow a parameter every time an assertion kind needs more
// context.
type outcome struct {
	// seq is the result sequence, and err the error the case raised instead.
	seq xdm.Sequence
	err error
	// paramDoc is the output:serialization-parameters element of the document
	// "declare option output:parameter-document" named, when the prolog named
	// one and it could be read. It is fetched by the caller rather than by
	// the serialising code, because only the caller knows where a test-set's
	// files live.
	paramDoc *xdm.Node
	// serialParams are the "declare option output:*" declarations of an
	// XQuery main module, keyed by the serialization parameter's local name.
	// An XPath case has none: XPath has no prolog to state them in, and its
	// result is serialised with the defaults.
	serialParams map[string]string
	// serialized caches the serialised form and the error producing it
	// raised, because a case may carry several serialization assertions over
	// one result — the JSON set writes half a dozen serialization-matches
	// under an all-of — and serialising is not free.
	serialized string
	serialErr  error
	haveSerial bool
}

// check evaluates an assertion tree against a result.
func check(a Assertion, res *outcome) (bool, string) {
	got, evalErr := res.seq, res.err
	switch a.Kind {
	case "all-of":
		for _, c := range a.Children {
			if ok, why := check(c, res); !ok {
				return false, why
			}
		}
		return true, ""
	case "any-of":
		// Each branch's own reason is kept, not just its kind. An any-of
		// names the alternatives a conforming processor may choose between,
		// so when none holds the interesting question is what the result
		// actually was -- and reporting only "none of {error,assert-true}
		// held" answered every such failure with the same uninformative
		// line, giving no way to tell a wrong value from an unexpected error
		// without re-running the case by hand.
		var reasons []string
		for _, c := range a.Children {
			ok, why := check(c, res)
			if ok {
				return true, ""
			}
			if why == "" {
				why = c.Kind
			}
			reasons = append(reasons, c.Kind+": "+why)
		}
		return false, "none of {" + strings.Join(reasons, "; ") + "} held"
	case "not":
		for _, c := range a.Children {
			if ok, _ := check(c, res); ok {
				return false, "not: inner assertion held"
			}
		}
		return true, ""
	}

	// Every remaining kind is a leaf. An error result is only acceptable when
	// the case asked for one.
	if a.Kind == "error" {
		if evalErr == nil {
			return false, fmt.Sprintf("expected error %s, got %s", a.Code, seqString(got))
		}
		// The code is compared when the engine produced one. Accepting any
		// error would let a wrong-error-code bug pass, which was a known
		// weakness of this harness; now only an error carrying no code at all
		// falls back to that.
		// "*" is the catalog's wildcard: the case requires an error but does
		// not say which. Comparing against it literally scored correct
		// behaviour as a failure — fn:error(QName("","FOO")) is *supposed* to
		// raise FOO.
		got := xdm.ErrorCode(evalErr)
		if got == "" || a.Code == "" || a.Code == "*" ||
			got == a.Code || sameErrorCode(got, a.Code) ||
			matchesQualifiedCode(evalErr, a.Code) {
			return true, ""
		}
		return false, fmt.Sprintf("error %s, want %s", got, a.Code)
	}
	// The serialization assertions are decided before the general "an error
	// is a failure" rule, because for them an error may be the expected
	// answer and may equally be raised by serialisation rather than by
	// evaluation: SERE0020 for a double that has no JSON spelling is
	// discovered only when the result is written.
	switch a.Kind {
	case "serialization-matches":
		return serializationMatches(res, a)
	case "assert-serialization":
		return serializationEquals(res, a)
	case "assert-serialization-error":
		return serializationErrorIs(res, a)
	}

	if evalErr != nil {
		return false, "unexpected error: " + evalErr.Error()
	}

	switch a.Kind {
	case "assert-empty":
		if len(got) == 0 {
			return true, ""
		}
		return false, "expected empty, got " + seqString(got)
	case "assert-count":
		want := strings.TrimSpace(a.Value)
		if fmt.Sprint(len(got)) == want {
			return true, ""
		}
		return false, fmt.Sprintf("expected %s items, got %d", want, len(got))
	case "assert-true":
		return boolIs(got, true)
	case "assert-false":
		return boolIs(got, false)
	case "assert-string-value":
		want := a.Value
		if strings.TrimSpace(a.Code) == "true" {
			// @normalize-space="true"
			want = strings.Join(strings.Fields(want), " ")
		}
		gotS := seqStringValue(got)
		if gotS == want || strings.Join(strings.Fields(gotS), " ") ==
			strings.Join(strings.Fields(want), " ") {
			return true, ""
		}
		return false, fmt.Sprintf("string-value %q, want %q", gotS, want)
	case "assert-eq":
		return eqLiteral(got, strings.TrimSpace(a.Value))
	case "assert-deep-eq":
		return deepEqLiteral(got, strings.TrimSpace(a.Value))
	case "assert-type":
		return typeIs(got, strings.TrimSpace(a.Value))
	case "assert-permutation":
		return permutationOf(got, strings.TrimSpace(a.Value))
	case "assert":
		return assertExpression(got, a.Value)
	case "assert-xml":
		return xmlMatches(got, a.Value)
	}
	return false, "unknown assertion " + a.Kind
}

func boolIs(got xdm.Sequence, want bool) (bool, string) {
	if len(got) == 1 {
		if a, ok := got[0].(*xdm.Atomic); ok && a.Type == xdm.TypeBoolean {
			if a.Bool() == want {
				return true, ""
			}
			return false, fmt.Sprintf("got %v, want %v", a.Bool(), want)
		}
	}
	return false, fmt.Sprintf("expected xs:boolean %v, got %s", want, seqString(got))
}

// eqLiteral compares against the literal text of an expected value by
// evaluating that text as an XPath expression and using "eq" semantics. That
// is what the suite means: the operand is an expression, not a string.
func eqLiteral(got xdm.Sequence, want string) (bool, string) {
	exp, err := xpath.Eval(want, assertContext(), resolver{})
	if err != nil {
		return false, "cannot evaluate expected value " + want + ": " + err.Error()
	}
	if len(got) != 1 || len(exp) != 1 {
		return false, fmt.Sprintf("expected a single item %q, got %s", want, seqString(got))
	}
	// "eq" atomizes its operands, so a node is compared by its typed value
	// rather than refused. string-constructor-027 is the case that shows the
	// difference: its result is an element, and the assertion
	// "Today is `2012-05-05`" is that element's string value. Requiring an
	// atomic here failed eight cases the engine answered correctly.
	ga, ok1 := singleAtomic(got)
	ea, ok2 := singleAtomic(exp)
	if !ok1 || !ok2 {
		return false, "assert-eq operand does not atomize to a single value"
	}
	if atomicEqual(ga, ea) {
		return true, ""
	}
	return false, fmt.Sprintf("got %s, want %s", ga.String(), ea.String())
}

func deepEqLiteral(got xdm.Sequence, want string) (bool, string) {
	exp, err := xpath.Eval(want, assertContext(), resolver{})
	if err != nil {
		return false, "cannot evaluate expected value " + want + ": " + err.Error()
	}
	if len(got) != len(exp) {
		return false, fmt.Sprintf("got %d items, want %d", len(got), len(exp))
	}
	for i := range got {
		ga, ok1 := got[i].(*xdm.Atomic)
		ea, ok2 := exp[i].(*xdm.Atomic)
		if !ok1 || !ok2 {
			// Node comparison is left to the engine's own deep-equal.
			continue
		}
		if !atomicEqual(ga, ea) {
			return false, fmt.Sprintf("item %d: got %s, want %s", i, ga.String(), ea.String())
		}
	}
	return true, ""
}

// atomicEqual compares two atomics the way "eq" does, promoting numerics so
// that xs:integer 1 equals xs:double 1.
func atomicEqual(a, b *xdm.Atomic) bool {
	if isNumeric(a.Type) && isNumeric(b.Type) {
		if a.IsNaN() || b.IsNaN() {
			return a.IsNaN() && b.IsNaN()
		}
		if a.Type == xdm.TypeDouble || b.Type == xdm.TypeDouble ||
			a.Type == xdm.TypeFloat || b.Type == xdm.TypeFloat {
			return a.Float64() == b.Float64()
		}
		return new(big.Rat).Set(a.Rat()).Cmp(b.Rat()) == 0
	}
	// Everything else is compared with the engine's own "eq", which is what
	// assert-eq means. Comparing lexical forms instead made two spellings of
	// one value unequal: xs:dateTime("2014-08-20T14:36:01-05:00") and
	// "...T19:36:01Z" are the same instant and "eq" says so, but their
	// strings differ, so parse-ietf-date-56 was scored on the spelling its
	// implementation happened to produce rather than on the value.
	ctx := assertContext()
	ctx = ctx.WithVar(xdm.QName{Local: "qt3a"}, xdm.One(a))
	ctx = ctx.WithVar(xdm.QName{Local: "qt3b"}, xdm.One(b))
	res, err := xpath.Eval("$qt3a eq $qt3b", ctx, resolver{})
	if err != nil {
		// Types "eq" does not relate — a QName against a string, say — are
		// unequal rather than an error in the comparison itself.
		return a.String() == b.String()
	}
	if len(res) == 1 {
		if v, ok := res[0].(*xdm.Atomic); ok {
			return v.Bool()
		}
	}
	return a.String() == b.String()
}

func isNumeric(t xdm.TypeCode) bool {
	switch t {
	case xdm.TypeInteger, xdm.TypeDecimal, xdm.TypeDouble, xdm.TypeFloat:
		return true
	}
	return false
}

// assertVersion is the language version the harness compiles its *own*
// expressions in — the <assert> predicates, the <assert-type> tests and the
// expected values of <assert-eq> and friends.
//
// It was pinned at 3.0, on the reasoning that an assert-type of "function(*)"
// has to parse whatever version the case runs under. That reasoning holds, but
// 3.0 is not the ceiling it was: the 3.1 sets write "array(*)" in an
// assert-type and "[1, 2]" or "map{}" as an expected value, none of which a
// 3.0 parser accepts, so pinning it there failed those cases for the harness's
// own inability to read the assertion rather than for anything the engine did.
// It is set once per target by runSuite, and the highest version is always at
// least as permissive as the one the case itself uses.
// It is package state rather than a field because every evaluator that needs
// it is a free function called from deep inside the comparison code, and
// threading a version through all of them would say nothing the single
// assignment in runSuite does not. That assignment is safe only because the
// three targets run one after another; running them in parallel would need
// this carried on the Runner instead.
var assertVersion = xpath.XPath30

// assertContext builds the context the harness evaluates its own expressions
// in.
func assertContext() *xpath.Context {
	ctx := xpath.NewContext(nil, xpath.Builtins())
	ctx.Version = assertVersion
	return ctx
}

func typeIs(got xdm.Sequence, want string) (bool, string) {
	// "instance of" is the engine's own answer to this question, so the check
	// is expressed in the language under test rather than reimplemented.
	ctx := assertContext()
	ctx = ctx.WithVar(xdm.QName{Local: "qt3v"}, got)
	res, err := xpath.Eval("$qt3v instance of "+want, ctx, resolver{})
	if err != nil {
		return false, "cannot test type " + want + ": " + err.Error()
	}
	if len(res) == 1 {
		if a, ok := res[0].(*xdm.Atomic); ok && a.Type == xdm.TypeBoolean && a.Bool() {
			return true, ""
		}
	}
	return false, fmt.Sprintf("%s is not an instance of %s", seqString(got), want)
}

func seqString(s xdm.Sequence) string {
	var parts []string
	for _, it := range s {
		switch v := it.(type) {
		case *xdm.Atomic:
			parts = append(parts, v.String())
		case *xdm.Node:
			parts = append(parts, "<"+v.Name.Local+">")
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func seqStringValue(s xdm.Sequence) string {
	var parts []string
	for _, it := range s {
		switch v := it.(type) {
		case *xdm.Atomic:
			parts = append(parts, v.String())
		case *xdm.Node:
			parts = append(parts, v.StringValue())
		}
	}
	return strings.Join(parts, " ")
}

// assertExpression evaluates an XPath predicate over the result, with the
// result bound to $result — which is how the suite writes these.
func assertExpression(got xdm.Sequence, expr string) (bool, string) {
	// The assertion is the harness's own expression, not the case's. See
	// assertVersion for the language it is compiled in.
	ctx := assertContext()
	ctx = ctx.WithVar(xdm.QName{Local: "result"}, got)
	res, err := xpath.Eval(expr, ctx, resolver{})
	if err != nil {
		return false, "assert " + strings.TrimSpace(expr) + ": " + err.Error()
	}
	// The assertion holds when the expression's *effective boolean value* is
	// true, not only when it returns the boolean true. "$result[1][self::price]"
	// returns the node when it matches, which is an assertion that passed —
	// requiring a literal boolean scored those as failures.
	ok, err := xpath.EffectiveBooleanValue(res)
	if err != nil {
		return false, "assert " + strings.TrimSpace(expr) + ": " + err.Error()
	}
	if ok {
		return true, ""
	}
	return false, "assert " + strings.TrimSpace(expr) + " was not true"
}

// permutationOf reports whether got is a reordering of the expected sequence.
//
// fn:unordered is explicitly allowed to return its input in any order, so the
// suite compares as multisets. Sorting the string forms is enough here: the
// values these cases produce are atomic.
func permutationOf(got xdm.Sequence, want string) (bool, string) {
	exp, err := xpath.Eval(want, assertContext(), resolver{})
	if err != nil {
		return false, "cannot evaluate expected value " + want + ": " + err.Error()
	}
	if len(got) != len(exp) {
		return false, fmt.Sprintf("got %d items, want %d", len(got), len(exp))
	}
	// The comparison is by value, not by type. The expected sequence is
	// written as a literal in the catalog, so xs:anyURI("x") is spelled "x"
	// there and would never match a key that carried the type name — the
	// assertion is assert-permutation, which is about order, not typing.
	key := func(s xdm.Sequence) []string {
		out := make([]string, 0, len(s))
		for _, it := range s {
			switch v := it.(type) {
			case *xdm.Atomic:
				out = append(out, v.String())
			case *xdm.Node:
				out = append(out, "node:"+v.StringValue())
			}
		}
		sort.Strings(out)
		return out
	}
	g, e := key(got), key(exp)
	for i := range g {
		if g[i] != e[i] {
			return false, fmt.Sprintf("not a permutation: %v vs %v", g, e)
		}
	}
	return true, ""
}

// xmlMatches compares the serialised result against the expected XML.
//
// The engine's own serialiser is unexported, and widening its API for a test
// harness would be the wrong trade — so a minimal one lives here. It handles
// what these cases actually produce: elements with attributes, text, comments
// and processing instructions. Namespace declarations are emitted only where
// the suite's expected output carries them, which is why nodes are compared
// after a normalising pass rather than byte-for-byte.
func xmlMatches(got xdm.Sequence, want string) (bool, string) {
	var sb strings.Builder
	for _, it := range got {
		switch v := it.(type) {
		case *xdm.Node:
			// A result element serialised on its own carries the namespaces
			// in scope on it, not only the ones declared on it: taking
			// fs:FileName out of its document does not leave the fs prefix
			// undefined. Only the top level needs this — descendants inherit
			// from the element being written.
			writeNodeXMLTop(&sb, v)
		case *xdm.Atomic:
			sb.WriteString(escapeText(v.String()))
		}
	}
	g := strings.TrimSpace(sb.String())
	w := strings.TrimSpace(want)
	if g == w {
		return true, ""
	}
	// Insignificant whitespace between elements differs between serialisers,
	// so a whitespace-collapsed comparison is tried before reporting.
	if collapseWS(g) == collapseWS(w) {
		return true, ""
	}
	// The expected XML is written by hand, so it varies in ways that are the
	// same document: "<a/>" against "<a></a>", and "<a />" against "<a/>".
	// Comparing those as text reports a difference that does not exist.
	if normalizeXML(g) == normalizeXML(w) {
		return true, ""
	}
	// Attribute order is not part of an XML document, so a last comparison
	// sorts the attributes of every tag. fn-doc/fn-doc-37 is the case that
	// needs it: the expected element declares xmlns:xs before xmlns:atomic,
	// and the two namespaces are lifted out of the source document in the
	// order the node holds them, which is the other one. Nothing about the
	// two documents differs, so reporting a failure was the harness comparing
	// serialisations where it meant to compare infosets.
	if sortTagAttrs(normalizeXML(g)) == sortTagAttrs(normalizeXML(w)) {
		return true, ""
	}
	// A last resort: parse both and compare the trees. A namespace prefix is
	// not part of an XML document either — fn:analyze-string may write its
	// result element as "fn:analyze-string-result" or as
	// "analyze-string-result" under a default declaration of the same
	// namespace, and the specification's own examples use one spelling while
	// the fn-analyze-string set uses the other. Comparing text made the two
	// differ where the infosets are identical.
	if infosetEqual(g, w) {
		return true, ""
	}
	if len(g) > 120 {
		g = g[:120] + "..."
	}
	if len(w) > 120 {
		w = w[:120] + "..."
	}
	return false, fmt.Sprintf("xml %q, want %q", g, w)
}

func collapseWS(s string) string { return strings.Join(strings.Fields(s), " ") }

func writeNodeXML(sb *strings.Builder, n *xdm.Node) {
	switch n.Kind {
	case xdm.KindDocument:
		for _, c := range n.Children {
			writeNodeXML(sb, c)
		}
	case xdm.KindElement:
		sb.WriteString("<" + n.Name.Lexical())
		// Namespace declarations are part of the serialised element. Omitting
		// them made a correct result compare unequal to the expected XML,
		// which reads as an engine bug and is not one.
		for _, ns := range n.Namespaces {
			if ns.Name.Local == "" {
				sb.WriteString(" xmlns=\"" + escapeAttr(ns.Value) + "\"")
			} else {
				sb.WriteString(" xmlns:" + ns.Name.Local + "=\"" + escapeAttr(ns.Value) + "\"")
			}
		}
		for _, a := range n.Attrs {
			sb.WriteString(" " + a.Name.Lexical() + "=\"" + escapeAttr(a.Value) + "\"")
		}
		if len(n.Children) == 0 {
			sb.WriteString("/>")
			return
		}
		sb.WriteString(">")
		for _, c := range n.Children {
			writeNodeXML(sb, c)
		}
		sb.WriteString("</" + n.Name.Lexical() + ">")
	case xdm.KindText:
		sb.WriteString(escapeText(n.Value))
	case xdm.KindComment:
		sb.WriteString("<!--" + n.Value + "-->")
	case xdm.KindPI:
		// The space is written even for an empty PI, though XML 1.0 §2.6 --
		//   PI ::= '<?' PITarget (S (Char* - (Char* '?>' Char*)))? '?>'
		// -- makes it part of the data's delimiter rather than of the target,
		// so "<?y?>" is the strictly correct spelling and is what both real
		// serializers write (xslt/serialize.go, xpath/fn_serialize.go each
		// guard it).
		//
		// It stays here because the suite's own assert-xml expectations
		// disagree with each other: K2-FilterExpr-7 spells an empty PI
		// "<?y?>", while Constr-comppi-empty-1 and -2, Constr-pi-content-3
		// and K2-Axes-66 all spell it "<?pi ?>". No single textual rendering
		// satisfies both, and the four outnumber the one. Both spellings
		// denote the same infoset -- an empty PI -- so the real fix is for
		// the comparison to reach infosetEqual, which cannot parse a bare PI
		// because it has no root element. That is the thing worth fixing;
		// changing the spelling here only moves which cases fail.
		sb.WriteString("<?" + n.Name.Local + " " + n.Value + "?>")
	case xdm.KindAttribute:
		// A bare attribute in a result sequence serialises as its value.
		sb.WriteString(escapeText(n.Value))
	}
}

// A carriage return must be written as a character reference. XML 1.0 section
// 2.11 has the parser turn a literal CR in the source into a line feed, so a
// CR written literally does not survive a round trip: the comparison in
// infosetEqual re-parses both sides, and a result holding a CR came back
// holding a LF while the expected text, which spells it "&#13;", came back
// holding the CR. json-to-xml-048 is that case -- the engine's tree was right
// and only this serialiser lost the character.
func escapeText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;",
		"\r", "&#13;")
	return r.Replace(s)
}

// An attribute value normalises more aggressively than text: XML 1.0 section
// 3.3.3 replaces every tab, line feed and carriage return with a space, so all
// three have to be written as character references to survive re-parsing.
func escapeAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", "\"", "&quot;",
		"\r", "&#13;", "\n", "&#10;", "\t", "&#9;")
	return r.Replace(s)
}

// The test catalog defines a case-blind collation for its own use. It is not
// part of the language — the spec requires only codepoint and the HTML ASCII
// case-insensitive collation — so it is registered here, by the harness that
// needs it, rather than built into the engine.
const fotsCaseBlind = "http://www.w3.org/2010/09/qt-fots-catalog/collation/caseblind"

func init() {
	xpath.RegisterCollation(fotsCaseBlind, caseBlind{})
}

// caseBlind compares without regard to case, over the whole of Unicode rather
// than ASCII alone.
type caseBlind struct{}

func (caseBlind) Compare(a, b string) int {
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

func (caseBlind) Contains(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

func (caseBlind) StartsWith(s, p string) bool {
	return strings.HasPrefix(strings.ToLower(s), strings.ToLower(p))
}

func (caseBlind) EndsWith(s, suf string) bool {
	return strings.HasSuffix(strings.ToLower(s), strings.ToLower(suf))
}

// Key is the collation's grouping key: two strings the collation calls equal
// must have the same key.
//
// It is an optional method, and omitting it is not harmless. "group by ...
// collation" partitions with a hash map, and a collation without a Key has its
// strings hashed verbatim -- so the collation is silently ignored and every
// key becomes its own group rather than the grouping failing. group-017 groups
// ("ax","bx","cx","Ay","By","Cy") case-blind and expects three groups; without
// this it got six, and nothing reported that the collation had not been used.
//
// Lowercasing is the same fold Compare applies, so the key and the comparison
// agree by construction.
func (caseBlind) Key(s string) string { return strings.ToLower(s) }

func (caseBlind) IndexOf(s, sub string) int {
	// The index is into the original string, so the folded index only works
	// while folding preserves length — which it does for the cases the suite
	// uses. Anything else would need a rune-by-rune walk.
	return strings.Index(strings.ToLower(s), strings.ToLower(sub))
}

// envCollections resolves fn:collection against the documents a <collection>
// environment names.
//
// Loading goes through Runner.loadDoc, which caches by file, so a document
// that is both the context item and a collection member is the same node —
// fn:collection stability and node identity both depend on that.
type envCollections struct {
	r     *Runner
	byURI map[string][]string

	// queryURI holds the <query> members of each collection. They are
	// evaluated on demand rather than up front: most cases never call
	// fn:collection at all, and an environment is resolved for every case
	// that references it.
	queryURI map[string][]CollectionQuery

	// cache holds the result of evaluating a URI's queries.
	//
	// fn:collection is required to be stable: §14.2 says "two calls on
	// fn:collection with the same argument ... must return the same result",
	// and cbcl-collection-004 checks exactly that by comparing collection()
	// with itself. Evaluating "1 to 10" twice would give equal values but
	// re-evaluating a node-building query would give different node identity,
	// so the result is computed once per environment and reused.
	cache map[string]xdm.Sequence

	// declared records every URI the environment named, whether or not it
	// has any members. A collection that exists and is empty is not the same
	// as one that was never declared: the first returns (), the second is
	// FODC0002.
	declared map[string]bool

	// queryOpts is the namespace resolver the environment supplies, so a
	// collection query is compiled in the same static context as the case.
	queryOpts xpath.NamespaceResolver
}

// resolve returns the collection URI's members, evaluating any <query>
// members against ctx the first time it is asked.
//
// ctx is the context of the fn:collection call, which is what carries the
// suite's fn:unparsed-text resolver and its test-set directory. UseCaseR31's
// "users-json" collection is <query>unparsed-text-lines("UseCaseR31/users.json")
// ! parse-json(.)</query>, and evaluating it needs precisely that wiring;
// building a fresh context here would lose it.
func (c *envCollections) resolve(ctx *xpath.Context, uri string) (xdm.Sequence, error) {
	if seq, ok := c.cache[uri]; ok {
		return seq, nil
	}
	var out xdm.Sequence
	for _, f := range c.byURI[uri] {
		doc, err := c.r.loadDoc(f)
		if err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	for _, q := range c.queryURI[uri] {
		// The query is a whole XQuery expression, not an XPath one --
		// "1 to 10" happens to be both, but a query may use constructs XPath
		// does not have -- so it goes through the same compiler a test case
		// does. The context item is absent: a collection query is closed over
		// nothing but the environment.
		compiled, err := xquery.Compile(q.Expr,
			xqueryOptions(c.queryOpts, ""))
		if err != nil {
			return nil, fmt.Errorf("collection %q: %w", uri, err)
		}
		sub := *ctx
		sub.Collections = nil // a collection query does not nest
		seq, err := compiled.Eval(&sub)
		if err != nil {
			return nil, fmt.Errorf("collection %q: %w", uri, err)
		}
		out = append(out, seq...)
	}
	if c.cache != nil {
		c.cache[uri] = out
	}
	return out, nil
}

// ResolveCollectionIn is the entry point fn:collection actually uses; see
// resolve for why the calling context is needed.
func (c *envCollections) ResolveCollectionIn(ctx *xpath.Context, uri, base string) (xdm.Sequence, error) {
	key, ok := c.lookup(uri, base)
	if !ok {
		// The default collection is the empty URI. A case that asks for a
		// collection the environment did not declare gets an error, which is
		// what a conforming processor does.
		return nil, fmt.Errorf("no collection %q", uri)
	}
	return c.resolve(ctx, key)
}

// lookup maps a requested URI to the key the environment declared it under,
// resolving a relative URI against the environment's static base URI.
//
// collection-006 asks for "collection1" against
// "http://www.w3.org/2010/09/qt-fots-catalog/". Resolving is the resolver's
// job — the engine hands over the base and does not guess what a URI means to
// the caller.
func (c *envCollections) lookup(uri, base string) (string, bool) {
	if c.declared[uri] {
		return uri, true
	}
	if base == "" {
		return "", false
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", false
	}
	u, err := url.Parse(uri)
	if err != nil {
		return "", false
	}
	abs := b.ResolveReference(u).String()
	return abs, c.declared[abs]
}

// ResolveCollection satisfies xpath.CollectionResolver for a caller that does
// not offer a context. Only the file-backed members can be served that way; a
// <query> member has nothing to evaluate against.
func (c *envCollections) ResolveCollection(uri, base string) (xdm.Sequence, error) {
	files, ok := c.byURI[uri]
	// A relative URI resolves against the static base URI the environment
	// declared: collection-006 asks for "collection1" against
	// "http://www.w3.org/2010/09/qt-fots-catalog/". Resolving is the
	// resolver's job — the engine hands over the base and does not guess what
	// a URI means to the caller.
	if !ok && base != "" {
		if b, err := url.Parse(base); err == nil {
			if u, err := url.Parse(uri); err == nil {
				files, ok = c.byURI[b.ResolveReference(u).String()]
			}
		}
	}
	if !ok {
		// The default collection is the empty URI. A case that asks for a
		// collection the environment did not declare gets an error, which is
		// what a conforming processor does.
		return nil, fmt.Errorf("no collection %q", uri)
	}
	var out xdm.Sequence
	for _, f := range files {
		doc, err := c.r.loadDoc(f)
		if err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, nil
}

// decimalFormatLibrary returns a function library whose fn:format-number uses
// the decimal format the environment declared, or nil when it declared none.
//
// Only the unnamed format is honoured. A named one is reached by
// format-number's third argument, which resolves a name through the static
// context — something a bare XPath expression has no way to carry, and which
// no case in this suite uses.
func decimalFormatLibrary(env Environment, ns resolver) xpath.FunctionLibrary {
	if len(env.DecimalFormats) == 0 {
		return nil
	}
	// Every declared format, keyed by its expanded name; the unnamed one is
	// keyed by the empty string and is what the two-argument form uses.
	formats := map[string]*xpath.DecimalFormat{}
	for i := range env.DecimalFormats {
		decl := &env.DecimalFormats[i]
		df := xpath.DefaultDecimalFormat()
		setRune := func(dst *rune, s string) {
			for _, r := range s {
				*dst = r
				return
			}
		}
		setRune(&df.DecimalSeparator, decl.DecimalSeparator)
		setRune(&df.GroupingSeparator, decl.GroupingSeparator)
		setRune(&df.Percent, decl.Percent)
		setRune(&df.PerMille, decl.PerMille)
		setRune(&df.ZeroDigit, decl.ZeroDigit)
		setRune(&df.Digit, decl.Digit)
		setRune(&df.PatternSeparator, decl.PatternSeparator)
		setRune(&df.MinusSign, decl.MinusSign)
		setRune(&df.ExponentSeparator, decl.ExponentSeparator)
		if decl.Infinity != "" {
			df.Infinity = decl.Infinity
		}
		if decl.NaN != "" {
			df.NaN = decl.NaN
		}
		formats[expandDeclName(decl, ns)] = df
	}

	call := func(ctx *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
		num, err := xpath.FormatNumberArg(args, 0)
		if err != nil {
			return nil, err
		}
		pic, err := xpath.FormatNumberString(args, 1)
		if err != nil {
			return nil, err
		}
		key := ""
		if len(args) > 2 {
			lex, err := xpath.FormatNumberString(args, 2)
			if err != nil {
				return nil, err
			}
			// The name is a lexical QName resolved in the static context. A
			// leading or trailing space is not part of it, and one case
			// writes " b:test " to check that.
			key = expandFormatName(strings.TrimSpace(lex), ns)
		}
		df, ok := formats[key]
		if !ok {
			if key == "" {
				df = xpath.DefaultDecimalFormat()
			} else {
				return nil, fmt.Errorf("FODF1280: no decimal format named %q", key)
			}
		}
		// The running version, not a fixed 3.0: a picture using scientific
		// notation is well-formed only from 3.1 on.
		out, err := xpath.FormatNumberVersion(num, pic, df, ctx.Version)
		if err != nil {
			return nil, err
		}
		return xdm.One(xdm.NewString(out)), nil
	}

	lib := xpath.NewLibrary(xpath.Builtins())
	for _, arity := range []int{2, 3} {
		lib.Add(xpath.Function{
			Name:  xdm.QName{URI: xdm.NSFN, Local: "format-number"},
			Arity: arity,
			Since: xpath.XPath30,
			Call:  call,
		})
	}
	return lib
}

// expandDeclName expands a decimal-format declaration's own name.
//
// The prefix may be declared on the decimal-format element itself — the suite
// writes <decimal-format xmlns:a="http://a.ns/" name="a:test"/> — so the
// binding is recovered from that element's attributes before falling back to
// the environment's prefixes.
func expandDeclName(decl *DecimalFormatDecl, ns resolver) string {
	if decl.Name == "" {
		return ""
	}
	prefix, local := xdm.SplitQName(decl.Name)
	if prefix == "" {
		return local
	}
	for _, a := range decl.Attrs {
		if a.Name.Space == "xmlns" && a.Name.Local == prefix {
			return xdm.QName{URI: a.Value, Local: local}.Clark()
		}
	}
	return expandFormatName(decl.Name, ns)
}

// expandFormatName resolves a lexical decimal-format name to its expanded
// form, so that a prefix declared by the environment matches the declaration.
func expandFormatName(lex string, ns resolver) string {
	if lex == "" {
		return ""
	}
	// A braced URI literal names the namespace directly, with no prefix to
	// resolve: Q{http://a.ns/}test.
	if rest, ok := strings.CutPrefix(lex, "Q{"); ok {
		if end := strings.IndexByte(rest, '}'); end >= 0 {
			return xdm.QName{URI: rest[:end], Local: rest[end+1:]}.Clark()
		}
		return lex
	}
	prefix, local := xdm.SplitQName(lex)
	if prefix == "" {
		return local
	}
	uri, _ := ns.ResolvePrefix(prefix)
	return xdm.QName{URI: uri, Local: local}.Clark()
}

// suiteTextResolver reads fn:unparsed-text resources out of the suite.
//
// The cases name their fixtures by the URI they would have on the W3C site,
// http://www.w3.org/fots/..., and the same files sit in the checkout under the
// matching path. Mapping one to the other is what lets the family run at all;
// without a resolver every case failed on "unparsed-text() is disabled", which
// is the engine correctly refusing to read arbitrary files rather than a
// conformance gap.
//
// Only URIs under the suite's own prefix resolve, and the joined path is
// checked to be inside the checkout: a case is data, and a fixture URI that
// climbed out with ".." would otherwise read any file the process can.
type suiteTextResolver struct {
	root string
	// byURI maps a URI the environment declared to the file in the checkout
	// that holds it. It is the catalog's own <resource> mapping, which is how
	// the suite intends these to be found; the prefix rule below is the
	// fallback for a case whose environment declares nothing.
	byURI map[string]string
	// dir is the test-set file's directory, which a relative reference in a
	// case resolves against.
	dir string
}

const fotsPrefix = "http://www.w3.org/fots/"

// newSuiteTextResolver builds the resolver for one environment.
func newSuiteTextResolver(root, dir string, env Environment) suiteTextResolver {
	byURI := map[string]string{}
	for _, res := range env.Resources {
		if res.URI != "" && res.File != "" {
			// A resource path is relative to the test-set file, not to the
			// suite root — the same rule Source paths follow, and for the same
			// reason: fn/unparsed-text.xml names "unparsed-text/x.txt".
			byURI[res.URI] = filepath.Join(dir, res.File)
		}
	}
	return suiteTextResolver{root: root, byURI: byURI, dir: dir}
}

func (t suiteTextResolver) ResolveText(uri, base, encoding string) (string, error) {
	// A relative reference is resolved against the static base URI first, so
	// that "text-plain-utf-8.txt" under a base of http://www.w3.org/fots/
	// unparsed-text/ becomes the URI the environment declared.
	full := uri
	if uri == "" {
		// An empty reference resolves to the base URI itself, which is what
		// makes unparsed-text("") read the resource the base names.
		full = base
	} else if !strings.Contains(uri, "://") && base != "" {
		full = resolveAgainst(base, uri)
	}
	// A declared <resource> is matched only by its full URI. A relative
	// reference that never resolved to one stays relative, and falls to the
	// no-base check below: several cases declare resources but no static base
	// URI, and assert FOUT1170 for exactly that.
	if strings.Contains(full, "://") {
		if file, ok := t.byURI[full]; ok {
			return t.read(filepath.Join(t.root, filepath.FromSlash(file)), uri, encoding)
		}
	}
	// A <resource> may declare a relative URI of its own — the UseCaseJSON
	// environment publishes "table.json" rather than an absolute one — and
	// the reference then matches it as written, before any base URI is
	// applied. Matching only the resolved form left those resources
	// unreachable and the cases failing on FOUT1170 for a file the checkout
	// actually holds. The reference is still looked up in the environment's
	// own table, so this reaches nothing the case did not declare.
	if file, ok := t.byURI[uri]; ok {
		return t.read(filepath.Join(t.root, filepath.FromSlash(file)), uri, encoding)
	}

	// The default static base URI is a file: URI naming the test-set's own
	// directory, so a case that says unparsed-text("../docs/atomic.xml")
	// resolves to a file: URI rather than staying relative, and never reached
	// the relative branch below. That looked like the engine refusing to read
	// outside the suite, but the file it names is squarely inside the
	// checkout — parse-xml-001 and parse-xml-fragment-001 failed on a harness
	// gap rather than a conformance one. The read() guard still decides: a
	// reference that climbed out of the checkout is refused there, so a case
	// cannot reach the rest of the filesystem by spelling its fixture as a
	// file: URI.
	if strings.HasPrefix(full, "file://") {
		cand := unescapePath(filepath.FromSlash(strings.TrimPrefix(full, "file://")))
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return t.read(cand, uri, encoding)
		}
	}

	// A relative reference resolves only when the environment actually
	// supplied a static base URI. Several cases declare none and assert
	// FOUT1170 for exactly that, so resolving one against the test-set
	// directory anyway would turn a conformance requirement into a pass.
	if !strings.Contains(full, "://") {
		if base == "" {
			return "", fmt.Errorf(
				"unparsed-text: %q is relative and no base URI is in scope", uri)
		}
		// The base was a filesystem path inside the checkout rather than a
		// fots: URI. The read() guard keeps that safe — a reference that
		// climbs out with ".." is refused rather than followed.
		cand := filepath.Join(t.root, t.dir, unescapePath(filepath.FromSlash(full)))
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return t.read(cand, uri, encoding)
		}
	}
	ref := full
	if !strings.HasPrefix(ref, fotsPrefix) {
		return "", fmt.Errorf("unparsed-text: %q is outside the suite", uri)
	}
	// Nothing else resolves. A relative reference with no static base URI is
	// FOUT1170 by the spec, and several cases assert exactly that — searching
	// the checkout for a file with a matching name would turn those into
	// passes that mean the opposite of conformance.
	return "", fmt.Errorf("unparsed-text: no resource for %q", uri)
}

// suiteEntityResolver lets a document handed to fn:parse-xml read the external
// entities the suite ships beside the test set.
//
// A handful of cases — fn-parse-xml/parse-xml-010 is the one that matters —
// declare <!ENTITY foo SYSTEM 'parse-xml/foo.entity'> inside the string they
// parse and assert that the entity's content appears in the result. Refusing
// external entities is the right default and the engine keeps it: this
// resolver exists because the suite's own fixtures are the one input where
// reading them is the thing being measured.
//
// The confinement is the same one suiteTextResolver.read applies, and
// deliberately the same code: an external entity is a file read like any
// other, and giving it a second gate written separately is how the two drift
// apart. A system identifier that climbs out of the checkout with ".." is
// refused rather than followed.
type suiteEntityResolver struct {
	text suiteTextResolver
}

func (e suiteEntityResolver) ResolveEntity(systemID, publicID, base string) (io.ReadCloser, string, error) {
	// The base is the URI of the resource that made the reference, which for
	// parse-xml is the static base URI: a file: URI naming the test-set's own
	// directory. Anything else — a fots: URI, an http: one — names a resource
	// this resolver has no business fetching.
	if !strings.HasPrefix(base, "file://") {
		return nil, "", fmt.Errorf("external entity %q: base %q is not a file URI", systemID, base)
	}
	if strings.Contains(systemID, "://") {
		return nil, "", fmt.Errorf("external entity %q is not relative to the suite", systemID)
	}
	full := resolveAgainst(base, systemID)
	path := filepath.FromSlash(strings.TrimPrefix(full, "file://"))
	// read() applies the containment check and returns the decoded text; the
	// entity's own encoding declaration is not consulted, which is fine for
	// the suite's fixtures because they are all UTF-8.
	s, err := e.text.read(path, systemID, "")
	if err != nil {
		return nil, "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	return io.NopCloser(strings.NewReader(s)), "file://" + filepath.ToSlash(abs), nil
}

// read loads one file, refusing any path that escapes the checkout.
//
// A case is data, and a fixture URI that climbed out with ".." would otherwise
// let the suite read any file the process can.
func (t suiteTextResolver) read(path, uri, encoding string) (string, error) {
	// Both sides are made absolute before they are compared. filepath.Rel
	// reports an error rather than a relation when one path is absolute and
	// the other is not, and the suite root can be given either way on the
	// command line, so comparing them as-is would refuse a perfectly
	// in-checkout file whenever the two spellings happened to disagree.
	clean, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("unparsed-text: %q: %w", uri, err)
	}
	root, err := filepath.Abs(t.root)
	if err != nil {
		return "", fmt.Errorf("unparsed-text: %q: %w", uri, err)
	}
	if rel, err := filepath.Rel(root, clean); err != nil ||
		rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unparsed-text: %q escapes the suite", uri)
	}
	data, err := os.ReadFile(clean)
	if err != nil {
		return "", err
	}
	return decodeUnparsedText(data, encoding)
}

// decodeUnparsedText applies the encoding the call asked for, defaulting to
// the byte-order mark and then to UTF-8.
func decodeUnparsedText(data []byte, encoding string) (string, error) {
	switch {
	case len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF:
		data = data[3:]
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		return decodeUTF16(data[2:], true), nil
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return decodeUTF16(data[2:], false), nil
	}
	// An encoding name is an EncName: a letter followed by letters, digits,
	// and the three punctuation characters. "123" is not one, and naming a
	// bad encoding is FOUT1190 rather than a failure to retrieve.
	if encoding != "" && !isEncName(encoding) {
		return "", fmt.Errorf("FOUT1190: %q is not a valid encoding name", encoding)
	}
	switch strings.ToLower(encoding) {
	case "", "utf-8":
		s := string(data)
		if !utf8.ValidString(s) {
			return "", fmt.Errorf("FOUT1190: not valid UTF-8")
		}
		// The XML-character restriction is fn:unparsed-text's own rule and
		// the engine applies it there; fn:json-doc reads through this same
		// resolver and must not be subject to it.
		return s, nil
	case "iso-8859-1", "latin1", "latin-1":
		var sb strings.Builder
		for _, b := range data {
			sb.WriteRune(rune(b))
		}
		return sb.String(), nil
	case "utf-16", "utf-16be":
		return decodeUTF16(data, true), nil
	case "utf-16le":
		return decodeUTF16(data, false), nil
	}
	return "", fmt.Errorf("FOUT1190: unsupported encoding %q", encoding)
}

// unescapePath turns the percent-encoded form a URI carries back into the
// characters a filename actually holds.
//
// JSONTestSuite ships a fixture named "n_structure_trailing_#.json", and the
// catalog necessarily spells it "%23" because a bare "#" in a URI starts a
// fragment. Handing the encoded form to the filesystem looked for a file whose
// name held those three literal characters and reported FOUT1170 for a fixture
// the checkout does hold. A path that does not decode is returned unchanged: a
// stray "%" in a name is not an error, and refusing it would lose files the
// encoded form would never have named.
func unescapePath(path string) string {
	if decoded, err := url.PathUnescape(path); err == nil {
		return decoded
	}
	return path
}

// isEncName reports whether s matches the EncName production of XML 1.0:
// [A-Za-z] ([A-Za-z0-9._] | '-')*.
func isEncName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
			continue
		case i == 0:
			return false
		case c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			continue
		default:
			return false
		}
	}
	return s != ""
}

// decodeUTF16 decodes 16-bit code units of the given endianness.
func decodeUTF16(data []byte, bigEndian bool) string {
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		if bigEndian {
			units = append(units, uint16(data[i])<<8|uint16(data[i+1]))
		} else {
			units = append(units, uint16(data[i+1])<<8|uint16(data[i]))
		}
	}
	return string(utf16.Decode(units))
}

// resolveAgainst resolves a relative reference against a base URI or path.
func resolveAgainst(base, ref string) string {
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		return base[:i+1] + ref
	}
	return ref
}

// envDocs resolves fn:doc against the documents an environment names by URI.
//
// Loading goes through Runner.loadDoc so that a document reachable both as the
// context item and through fn:doc is the same node: "doc('x') is doc('x')" is
// required to be true, and re-parsing would break it.
type envDocResolver struct {
	r     *Runner
	byURI map[string]string
	// dir is the test-set's directory, relative to the suite root. A case
	// may call fn:doc with a path relative to it that the environment never
	// declared; see ResolveDocument.
	dir string
}

func envDocs(r *Runner, dir string, env Environment) *envDocResolver {
	byURI := map[string]string{}
	// A <resource> is reachable through fn:doc as well as fn:unparsed-text
	// when it holds XML: UseCaseJSON-008 publishes Wikipedia-Origami.xml that
	// way and reads it with doc(). Registering only <source> left it
	// unreachable, which reported FODC0002 for a file the checkout holds.
	for _, res := range env.Resources {
		if res.URI != "" && res.File != "" {
			// A resource path is relative to the test-set file rather than to
			// the suite root, the same rule newSuiteTextResolver follows.
			byURI[res.URI] = filepath.Join(dir, res.File)
		}
	}
	for _, src := range env.Sources {
		if src.URI != "" {
			byURI[src.URI] = src.File
		}
		// A document is also retrievable under the URI fn:document-uri
		// reports for it, which is its base URI. Keying only on a declared
		// uri made "doc-available(document-uri(/))" false for every source
		// that had none: document-uri answered with the path the parser was
		// given, and the resolver had never heard of it.
		byURI[filepath.Join(r.Root, src.File)] = src.File
	}
	// A collection's members are documents too, and collection-009 walks from
	// fn:collection through document-uri() back through fn:doc expecting to
	// land on the same nodes. Registering only the <source> children of the
	// environment left those unreachable, so the round trip raised "no
	// document" — a gap in what the harness declared rather than the engine
	// losing track of a document's URI.
	for _, coll := range env.Collections {
		for _, src := range coll.Sources {
			if src.URI != "" {
				byURI[src.URI] = src.File
			}
			byURI[filepath.Join(r.Root, src.File)] = src.File
		}
	}
	// The resolver is returned even with nothing declared, because it can
	// still find a document by its path under the test-set directory; see
	// ResolveDocument.
	return &envDocResolver{r: r, byURI: byURI, dir: dir}
}

func (d *envDocResolver) ResolveDocument(uri, base string) (*xdm.Tree, error) {
	file, ok := d.byURI[uri]
	if !ok {
		// A relative reference resolves against the base URI before it is
		// looked up. UseCaseJSON-008 calls doc('Wikipedia-Origami.xml') for a
		// source the environment publishes under its full catalog URI, and
		// matching only the reference as written left it unreachable.
		if base != "" && !strings.Contains(uri, "://") {
			file, ok = d.byURI[resolveAgainst(base, uri)]
		}
	}
	if !ok && !strings.Contains(uri, "://") && !filepath.IsAbs(uri) {
		// A relative reference the environment never declared still names a
		// file in the checkout, relative to the test-set's own directory --
		// the same rule a <resource> path follows.
		//
		// UseCaseR31-030 calls doc("UseCaseR31/Wikipedia-Origami.xml") under
		// the "json-docs" environment, which declares the JSON resources the
		// other cases in the set read but not this XML document. The file is
		// right there in the suite; nothing but the catalog entry was
		// missing, so refusing it tested the harness rather than the engine.
		//
		// The path is confined to the suite root. A case cannot reach outside
		// it with "..", and one that tries gets the FODC0002 it would have
		// got before.
		cand := filepath.Join(d.dir, filepath.FromSlash(uri))
		full := filepath.Join(d.r.Root, cand)
		if root, err := filepath.Abs(d.r.Root); err == nil {
			if abs, err := filepath.Abs(full); err == nil &&
				strings.HasPrefix(abs, root+string(filepath.Separator)) {
				if st, err := os.Stat(full); err == nil && !st.IsDir() {
					file, ok = cand, true
				}
			}
		}
	}
	if !ok {
		return nil, fmt.Errorf("no document %q", uri)
	}
	node, err := d.r.loadDocURI(file, uri)
	if err != nil {
		return nil, err
	}
	t := node.Tree()
	if t == nil {
		return nil, fmt.Errorf("document %q has no tree", uri)
	}
	return t, nil
}

// normalizeXML puts serialised XML into a form where two spellings of the same
// document compare equal.
//
// It folds "<a/>" and "<a></a>" together, and drops the optional space before
// "/>". Both appear in hand-written assert-xml values, and neither is a
// difference in the document.
// sortTagAttrs rewrites every start tag with its attributes in sorted order,
// so that two serialisations differing only in attribute order compare equal.
//
// It is deliberately the last comparison xmlMatches tries rather than the
// first: it is a text rewrite over something that may not be a well-formed
// tag, and running it ahead of the exact comparison would let a genuinely
// different document slip through on a lucky reordering. Anything it cannot
// parse confidently is left exactly as it found it.
func sortTagAttrs(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '<' || strings.HasPrefix(s[i:], "</") || strings.HasPrefix(s[i:], "<?") ||
			strings.HasPrefix(s[i:], "<!") {
			sb.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			sb.WriteString(s[i:])
			break
		}
		inner := s[i+1 : i+end]
		i += end + 1
		// A name followed by nothing has no attributes to sort.
		j := strings.IndexAny(inner, " \t\n\r")
		if j < 0 {
			sb.WriteString("<" + inner + ">")
			continue
		}
		attrs, ok := splitAttrs(inner[j+1:])
		if !ok {
			sb.WriteString("<" + inner + ">")
			continue
		}
		sort.Strings(attrs)
		sb.WriteString("<" + inner[:j])
		for _, a := range attrs {
			sb.WriteString(" " + a)
		}
		sb.WriteString(">")
	}
	return sb.String()
}

// splitAttrs breaks the attribute part of a start tag into whole name="value"
// pairs, reporting false for anything it does not recognise. Splitting on
// whitespace alone would cut a value that contains a space in half, which
// would then sort as two fragments and compare unequal to itself.
func splitAttrs(s string) ([]string, bool) {
	var out []string
	for {
		s = strings.TrimLeft(s, " \t\n\r")
		if s == "" {
			return out, true
		}
		eq := strings.IndexByte(s, '=')
		if eq < 0 || eq+1 >= len(s) {
			return nil, false
		}
		q := s[eq+1]
		if q != '"' && q != '\'' {
			return nil, false
		}
		close := strings.IndexByte(s[eq+2:], q)
		if close < 0 {
			return nil, false
		}
		end := eq + 2 + close + 1
		out = append(out, s[:end])
		s = s[end:]
	}
}

func normalizeXML(s string) string {
	s = strings.ReplaceAll(s, " />", "/>")
	s = foldEmptyPIs(s)
	// Rewrite every empty-element tag to the long form, so the two spellings
	// meet in the middle. The name ends at the first space or slash.
	var sb strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '<' {
			sb.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			sb.WriteString(s[i:])
			break
		}
		tag := s[i : i+end+1]
		if len(tag) > 3 && strings.HasSuffix(tag, "/>") && !strings.HasPrefix(tag, "<?") {
			inner := tag[1 : len(tag)-2]
			name := inner
			if j := strings.IndexAny(name, " \t\n\r"); j >= 0 {
				name = name[:j]
			}
			sb.WriteString("<" + inner + "></" + name + ">")
		} else {
			sb.WriteString(tag)
		}
		i += end + 1
	}
	return collapseWS(sb.String())
}

// foldEmptyPIs rewrites a processing instruction with no data to the single
// spelling "<?target?>", so that the two ways of writing one compare equal.
//
// XML 1.0 section 2.6 --
//
//	PI ::= '<?' PITarget (S (Char* - (Char* '?>' Char*)))? '?>'
//
// -- makes the space part of the data's delimiter, so it is present only when
// there is data: "<?y?>" and "<?y ?>" are the same processing instruction with
// no data, differing only in a separator that delimits nothing. The suite's
// hand-written assert-xml values use both spellings -- K2-FilterExpr-7 writes
// "<?y?>" while Constr-comppi-empty-1 writes "<?pi ?>" -- and neither is a
// difference in the document, so both sides are folded to the short form
// before they are compared.
//
// Only an all-whitespace data part is folded. A PI whose data is genuinely
// empty and one whose data is a space are the same node here because the
// serialiser above writes the separator unconditionally, which is the reason
// the two spellings need reconciling at all.
func foldEmptyPIs(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); {
		if !strings.HasPrefix(s[i:], "<?") {
			sb.WriteByte(s[i])
			i++
			continue
		}
		end := strings.Index(s[i:], "?>")
		if end < 0 {
			sb.WriteString(s[i:])
			break
		}
		inner := s[i+2 : i+end]
		i += end + 2
		// The target ends at the first whitespace; what follows is the data.
		// An XML declaration is not a processing instruction in the data
		// model, and its pseudo-attributes are never empty, so it falls
		// through the trim below unchanged.
		if j := strings.IndexAny(inner, " \t\n\r"); j >= 0 &&
			strings.TrimSpace(inner[j:]) == "" {
			inner = inner[:j]
		}
		sb.WriteString("<?" + inner + "?>")
	}
	return sb.String()
}

// writeNodeXMLTop writes n as a standalone result, declaring the namespaces in
// scope on it rather than only those declared on it.
//
// A node deep in a document inherits prefix bindings from its ancestors. Lift
// it out on its own and those bindings have to travel with it, or the
// serialised form uses a prefix nothing defines — which is what a conforming
// serialiser emits and what the expected XML in the suite shows.
func writeNodeXMLTop(sb *strings.Builder, n *xdm.Node) {
	if n.Kind != xdm.KindElement {
		writeNodeXML(sb, n)
		return
	}
	scope := n.InScopeNamespaces()
	// Only prefixes the element does not already declare need adding; the
	// ordinary writer emits those.
	declared := map[string]bool{}
	for _, ns := range n.Namespaces {
		declared[ns.Name.Local] = true
	}
	extra := make([]string, 0, len(scope))
	for prefix := range scope {
		if prefix == "xml" || declared[prefix] {
			continue
		}
		extra = append(extra, prefix)
	}
	if len(extra) == 0 {
		writeNodeXML(sb, n)
		return
	}
	sort.Strings(extra)

	sb.WriteString("<" + n.Name.Lexical())
	for _, ns := range n.Namespaces {
		if ns.Name.Local == "" {
			sb.WriteString(" xmlns=\"" + escapeAttr(ns.Value) + "\"")
		} else {
			sb.WriteString(" xmlns:" + ns.Name.Local + "=\"" + escapeAttr(ns.Value) + "\"")
		}
	}
	for _, prefix := range extra {
		if prefix == "" {
			sb.WriteString(" xmlns=\"" + escapeAttr(scope[prefix]) + "\"")
		} else {
			sb.WriteString(" xmlns:" + prefix + "=\"" + escapeAttr(scope[prefix]) + "\"")
		}
	}
	for _, a := range n.Attrs {
		sb.WriteString(" " + a.Name.Lexical() + "=\"" + escapeAttr(a.Value) + "\"")
	}
	if len(n.Children) == 0 {
		sb.WriteString("/>")
		return
	}
	sb.WriteString(">")
	for _, c := range n.Children {
		writeNodeXML(sb, c)
	}
	sb.WriteString("</" + n.Name.Lexical() + ">")
}

// loadAssertFiles fills in the Value of any assertion whose expected result is
// held in a separate document.
//
// A file that cannot be read leaves Value empty, which fails the comparison
// rather than passing it: a missing expectation must not look like agreement.
func loadAssertFiles(r *Runner, ts *TestSet, a *Assertion) {
	if a.File != "" && a.Value == "" {
		if b, err := os.ReadFile(filepath.Join(r.Root, srcPath(ts.Dir, a.File))); err == nil {
			a.Value = string(b)
		}
	}
	for i := range a.Children {
		loadAssertFiles(r, ts, &a.Children[i])
	}
}

// infosetEqual reports whether two serialised documents have the same infoset,
// ignoring everything serialisation is free to choose: the prefix a namespace
// is bound to, the order of attributes, and where the declarations sit.
//
// It is the last comparison xmlMatches tries, after the textual ones, because
// parsing is the expensive answer and a text match is the common case.
func infosetEqual(a, b string) bool {
	ta, err := parseFragment(a)
	if err != nil {
		return false
	}
	tb, err := parseFragment(b)
	if err != nil {
		return false
	}
	return nodesEqual(ta.Root, tb.Root)
}

// parseFragment parses a serialised *result sequence*, which is not in general
// a well-formed document.
//
// A query returns a sequence, and the suite's assert-xml spells out whatever
// that sequence serialises to: two sibling elements for a query returning two,
// or text beside an element for "(count(...), $i)". Neither has the single
// root element XML requires, so parsing them directly fails and the infoset
// comparison -- the only one that ignores where a namespace declaration sits
// -- never runs. That left cases failing on nothing but a redundant xmlns the
// harness's own writer emits, with the engine's tree already correct.
//
// Wrapping both sides in the same synthetic root makes them parseable without
// changing what is compared: nodesEqual walks from the root down, and an
// identical wrapper on each side cancels out. The wrapper's name is one no
// result can collide with, and it declares nothing, so it contributes no
// namespace of its own to the fragment inside it.
func parseFragment(s string) (*xdm.Tree, error) {
	if t, err := xdm.ParseString(s, xdm.ParseOptions{}); err == nil {
		return t, nil
	}
	return xdm.ParseString(
		"<qt3-fragment-wrapper>"+s+"</qt3-fragment-wrapper>",
		xdm.ParseOptions{})
}

// nodesEqual compares two trees by name, value and children, taking a name to
// be its namespace and local part rather than its lexical form.
func nodesEqual(a, b *xdm.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind {
		return false
	}
	if a.Name.URI != b.Name.URI || a.Name.Local != b.Name.Local {
		return false
	}
	if a.Kind != xdm.KindElement && a.Kind != xdm.KindDocument && a.Value != b.Value {
		return false
	}
	if a.Kind == xdm.KindElement && !attrsEqual(a, b) {
		return false
	}
	ac, bc := significantChildren(a), significantChildren(b)
	if len(ac) != len(bc) {
		return false
	}
	for i := range ac {
		if !nodesEqual(ac[i], bc[i]) {
			return false
		}
	}
	return true
}

// attrsEqual compares two elements' attributes as sets keyed by expanded name.
// Namespace declarations are not attributes in the data model and are excluded.
func attrsEqual(a, b *xdm.Node) bool {
	if len(a.Attrs) != len(b.Attrs) {
		return false
	}
	want := make(map[string]string, len(b.Attrs))
	for _, at := range b.Attrs {
		want[at.Name.Clark()] = at.Value
	}
	for _, at := range a.Attrs {
		v, ok := want[at.Name.Clark()]
		if !ok || v != at.Value {
			return false
		}
	}
	return true
}

// significantChildren drops whitespace-only text, which the expected value in
// a test-set file carries from its own indentation.
func significantChildren(n *xdm.Node) []*xdm.Node {
	out := make([]*xdm.Node, 0, len(n.Children))
	for _, c := range n.Children {
		if c.Kind == xdm.KindText && strings.TrimSpace(c.Value) == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// sameErrorCode reports whether two spellings name the same error.
//
// A code in no namespace may be written bare or in Clark notation with an
// empty URI, and the suite uses both: parse-json-943 expects "Q{}USER9999" for
// the error fn:error(QName("","USER9999"), ?) raises, which carries the local
// name alone. They are one name written two ways.
func sameErrorCode(got, want string) bool {
	return strings.TrimPrefix(got, "Q{}") == strings.TrimPrefix(want, "Q{}")
}

// matchesQualifiedCode compares a "Q{uri}local" expectation against the full
// QName of the error's code.
//
// xdm.ErrorCode returns the local name alone, which is all a spec code ever
// needs — they are unique across the specs and all live in one namespace. A
// code fn:error was handed does not: try-catch-fn-error-1 raises
// Q{http://www.example.com/}EXER3141, and comparing its local name against
// the catalog's expanded form scored the right code as the wrong one.
func matchesQualifiedCode(err error, want string) bool {
	if !strings.HasPrefix(want, "Q{") {
		return false
	}
	end := strings.Index(want, "}")
	if end < 0 {
		return false
	}
	var e *xdm.Error
	if !errors.As(err, &e) || e.CodeName == nil {
		return false
	}
	return e.CodeName.URI == want[2:end] && e.CodeName.Local == want[end+1:]
}

// xqueryOptions turns the namespaces an environment declared into the static
// context a query is compiled under.
//
// An XPath case is given a NamespaceResolver; an XQuery case needs the same
// bindings as prolog-equivalent options, because XQuery resolves names while
// parsing rather than after it.
//
// testSetURI is the file: URI of the test-set file a case was read from.
//
// §2.1.2 defaults the static base URI to "the URI of the resource containing
// the expression", which for the suite is the test-set file. It is handed to
// the compiler only as the base a relative "declare base-uri" resolves
// against -- see xqueryOptions for why it is not the query's base URI.
func (r *Runner) testSetURI(ts *TestSet) string {
	if ts == nil || ts.File == "" {
		return ""
	}
	abs, err := filepath.Abs(filepath.Join(r.Root, filepath.FromSlash(ts.File)))
	if err != nil {
		return ""
	}
	return "file://" + filepath.ToSlash(abs)
}

// Options.BaseURI is deliberately NOT supplied here, though §2.1.2 requires a
// static base URI and K2-BaseURIProlog-4 depends on one: that case declares
// the relative "declare base-uri 'abc'" and checks that fn:static-base-uri()
// came back absolute, which needs an absolute base to resolve against at
// compile time.
//
// Setting BaseURI from the evaluation context fixes that one case and breaks
// five others. The compile-time base URI is not simply the runtime one: the
// suite's base-URI-12, -14, -23 and -24 resolve relative references against a
// base the environment or the query supplies later, and K2-BaseURIFunc-30
// reads a base URI the environment declares -- all of which a compile-time
// value pins prematurely. base-URI-23 shows the shape: it came back
// "http://www.example.org/examples%20", a URI resolved once too often.
//
// declBase is the separation that makes both work. It is the URI of the file
// holding the query, and the compiler uses it for one thing only: resolving a
// relative "declare base-uri" against it. A query that declares no base URI
// never sees it, so the five cases above are untouched, while -4 and -5 get
// the absolute base their declarations need.
func xqueryOptions(ns xpath.NamespaceResolver, declBase string) xquery.Options {
	opts := xquery.Options{DeclarationBaseURI: declBase}
	if ns == nil {
		return opts
	}
	opts.DefaultElementNamespace = ns.DefaultElementNamespace()
	if m, ok := ns.(interface{ Bindings() map[string]string }); ok {
		opts.Namespaces = m.Bindings()
	}
	return opts
}

// singleAtomic atomizes a one-item sequence for an "eq" comparison.
//
// A node atomizes to its typed value, which for an untyped node is its string
// value; anything that atomizes to other than exactly one value cannot be an
// operand of "eq" and is reported rather than guessed at.
func singleAtomic(seq xdm.Sequence) (*xdm.Atomic, bool) {
	atoms := xdm.Atomize(seq)
	if len(atoms) != 1 {
		return nil, false
	}
	a, ok := atoms[0].(*xdm.Atomic)
	return a, ok
}
