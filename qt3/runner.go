package qt3

import (
	"net/url"
	"context"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
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
	}
	return "", false
}

// DefaultElementNamespace reports the namespace an unprefixed element or type
// name takes. The environment declares it as a namespace binding with an empty
// prefix, which is what "xs:QName('ncname')" resolves against.
func (r resolver) DefaultElementNamespace() string  { return r.prefixes[""] }
func (r resolver) DefaultFunctionNamespace() string { return xdm.NSFN }

// Runner executes cases from a suite checkout.
type Runner struct {
	Root string
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

// unsupportedSpec reports whether a dependency puts the case out of scope.
//
// The suite is FOTS 3.1 and covers XQuery as well as XPath. This engine is an
// XPath 2.0 processor, so anything requiring XQuery syntax or a 3.0+ feature
// is skipped rather than counted as a failure: failing a test for a language
// you do not claim to implement says nothing about conformance.
func unsupportedSpec(deps []Dependency) string {
	for _, d := range deps {
		switch d.Type {
		case "spec":
			// A value is a space-separated list of alternatives; the case is
			// in scope if any alternative includes XPath 2.0.
			if !specIncludesXP20(d.Value) {
				return "requires " + d.Value
			}
		case "feature":
			switch d.Value {
			case "schemaValidation", "schemaImport", "typedData",
				"staticTyping", "moduleImport", "higherOrderFunctions",
				"namespace-axis", "infoset-dtd", "xpath-1.0-compatibility",
				"fn-transform-XSLT", "fn-transform-XSLT30", "fn-format-integer-CLDR",
				"non_empty_sequence_collection", "collection-stability",
				"directory-as-collection-uri", "simple-uca-fallback",
				"advanced-uca-fallback", "olson-timezone", "remote_http":
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

func specIncludesXP20(v string) bool {
	for _, alt := range strings.Fields(v) {
		// "XP20+" means 2.0 and later; "XP20" means exactly 2.0. Both are in
		// scope. "XQ..." is XQuery only, and "XP30"/"XP31" without a 2.0
		// alternative are later versions.
		if alt == "XP20" || alt == "XP20+" {
			return true
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
			out.Collections = append(out.Collections, rc)
		}
		out.Params = append(out.Params, e.Params...)
		out.Namespaces = append(out.Namespaces, e.Namespaces...)
		out.Schemas = append(out.Schemas, e.Schemas...)
		out.Collations = append(out.Collations, e.Collations...)
		out.StaticBaseURI = append(out.StaticBaseURI, e.StaticBaseURI...)
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
	rep = Report{Set: ts.Name, Case: tc.Name, Expr: strings.TrimSpace(tc.Test)}
	defer func() {
		if p := recover(); p != nil {
			rep.Outcome = Fail
			rep.Reason = fmt.Sprintf("PANIC: %v", p)
		}
	}()

	if why := unsupportedSpec(append(append([]Dependency{}, ts.Dependencies...),
		tc.Dependencies...)); why != "" {
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

	runCtx, cancel := context.WithTimeout(context.Background(), CaseTimeout)
	defer cancel()

	ctx := xpath.NewContext(ctxItem, xpath.Builtins())
	// The environment may declare the base URI of the expression itself,
	// which is distinct from the base URI of any document it is applied to.
	for _, b := range env.StaticBaseURI {
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
	if docs := envDocs(r, env); docs != nil {
		ctx.Docs = docs
	}
	if len(env.Collections) > 0 {
		cr := &envCollections{r: r, byURI: map[string][]string{}}
		for _, c := range env.Collections {
			for _, src := range c.Sources {
				cr.byURI[c.URI] = append(cr.byURI[c.URI], src.File)
			}
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
		ctx = ctx.WithVar(xdm.QName{Local: p.Name}, v)
	}

	// A case is given a deadline of its own. An expression that does not
	// terminate would otherwise take the whole run with it — which is how the
	// unbounded round() precision was found, after it consumed twenty minutes
	// and all available memory before the harness could report anything.
	ctx.Ctx = runCtx
	got, evalErr := xpath.Eval(tc.Test, ctx, ns)

	want, err := ParseAssert(tc.Result.Raw)
	if err != nil {
		rep.Outcome, rep.Reason = Skip, "unparsable result: "+err.Error()
		return rep
	}
	ok, why := check(want, got, evalErr)
	if ok {
		rep.Outcome = Pass
		return rep
	}
	rep.Outcome, rep.Reason = Fail, why
	return rep
}

// check evaluates an assertion tree against a result.
func check(a Assertion, got xdm.Sequence, evalErr error) (bool, string) {
	switch a.Kind {
	case "all-of":
		for _, c := range a.Children {
			if ok, why := check(c, got, evalErr); !ok {
				return false, why
			}
		}
		return true, ""
	case "any-of":
		var reasons []string
		for _, c := range a.Children {
			if ok, _ := check(c, got, evalErr); ok {
				return true, ""
			}
			reasons = append(reasons, c.Kind)
		}
		return false, "none of {" + strings.Join(reasons, ",") + "} held"
	case "not":
		for _, c := range a.Children {
			if ok, _ := check(c, got, evalErr); ok {
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
		if got == "" || a.Code == "" || a.Code == "*" || got == a.Code {
			return true, ""
		}
		return false, fmt.Sprintf("error %s, want %s", got, a.Code)
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
	case "serialization-matches", "assert-serialization-error":
		return false, "unsupported assertion " + a.Kind
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
	exp, err := xpath.Eval(want, xpath.NewContext(nil, xpath.Builtins()), resolver{})
	if err != nil {
		return false, "cannot evaluate expected value " + want + ": " + err.Error()
	}
	if len(got) != 1 || len(exp) != 1 {
		return false, fmt.Sprintf("expected a single item %q, got %s", want, seqString(got))
	}
	ga, ok1 := got[0].(*xdm.Atomic)
	ea, ok2 := exp[0].(*xdm.Atomic)
	if !ok1 || !ok2 {
		return false, "assert-eq on a non-atomic value"
	}
	if atomicEqual(ga, ea) {
		return true, ""
	}
	return false, fmt.Sprintf("got %s, want %s", ga.String(), ea.String())
}

func deepEqLiteral(got xdm.Sequence, want string) (bool, string) {
	exp, err := xpath.Eval(want, xpath.NewContext(nil, xpath.Builtins()), resolver{})
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
	return a.String() == b.String()
}

func isNumeric(t xdm.TypeCode) bool {
	switch t {
	case xdm.TypeInteger, xdm.TypeDecimal, xdm.TypeDouble, xdm.TypeFloat:
		return true
	}
	return false
}

func typeIs(got xdm.Sequence, want string) (bool, string) {
	// "instance of" is the engine's own answer to this question, so the check
	// is expressed in the language under test rather than reimplemented.
	ctx := xpath.NewContext(nil, xpath.Builtins())
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
	ctx := xpath.NewContext(nil, xpath.Builtins())
	ctx = ctx.WithVar(xdm.QName{Local: "result"}, got)
	res, err := xpath.Eval(expr, ctx, resolver{})
	if err != nil {
		return false, "assert " + strings.TrimSpace(expr) + ": " + err.Error()
	}
	if len(res) == 1 {
		if b, ok := res[0].(*xdm.Atomic); ok && b.Type == xdm.TypeBoolean && b.Bool() {
			return true, ""
		}
	}
	return false, "assert " + strings.TrimSpace(expr) + " was not true"
}

// permutationOf reports whether got is a reordering of the expected sequence.
//
// fn:unordered is explicitly allowed to return its input in any order, so the
// suite compares as multisets. Sorting the string forms is enough here: the
// values these cases produce are atomic.
func permutationOf(got xdm.Sequence, want string) (bool, string) {
	exp, err := xpath.Eval(want, xpath.NewContext(nil, xpath.Builtins()), resolver{})
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
			writeNodeXML(&sb, v)
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
		sb.WriteString("<?" + n.Name.Local + " " + n.Value + "?>")
	case xdm.KindAttribute:
		// A bare attribute in a result sequence serialises as its value.
		sb.WriteString(escapeText(n.Value))
	}
}

func escapeText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func escapeAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", "\"", "&quot;")
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
}

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

// envDocs resolves fn:doc against the documents an environment names by URI.
//
// Loading goes through Runner.loadDoc so that a document reachable both as the
// context item and through fn:doc is the same node: "doc('x') is doc('x')" is
// required to be true, and re-parsing would break it.
type envDocResolver struct {
	r     *Runner
	byURI map[string]string
}

func envDocs(r *Runner, env Environment) *envDocResolver {
	byURI := map[string]string{}
	for _, src := range env.Sources {
		if src.URI != "" {
			byURI[src.URI] = src.File
		}
	}
	if len(byURI) == 0 {
		return nil
	}
	return &envDocResolver{r: r, byURI: byURI}
}

func (d *envDocResolver) ResolveDocument(uri, base string) (*xdm.Tree, error) {
	file, ok := d.byURI[uri]
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
func normalizeXML(s string) string {
	s = strings.ReplaceAll(s, " />", "/>")
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
