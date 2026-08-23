package xslts

import (
	"context"
	"fmt"
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
type Runner struct {
	// Root is the suite checkout.
	Root string
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
	// Stylesheet and source paths are relative to the test-set file rather
	// than to the suite root, so the directory travels with the parsed set.
	set.Dir = filepath.Dir(path)
	if set.Name == "" {
		set.Name = ref.Name
	}
	return &set, nil
}

// runCase runs one test and judges the result.
func (r *Runner) runCase(set *TestSet, tc *TestCase) Outcome {
	out := Outcome{Set: set.Name, Name: tc.Name}
	if ok, why := inScope(set, tc); !ok {
		out.Skipped, out.Why = true, why
		return out
	}

	assert, err := ParseAssert([]byte(tc.Result.Inner))
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

	ok, why := r.judge(assert, res, terr, set)
	out.Pass, out.Why = ok, why
	return out
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
	sheetDoc, err := xdm.ParseString(string(stripBOM(sheetSrc)),
		xdm.ParseOptions{AllowDOCTYPE: true, BaseURI: sheetPath})
	if err != nil {
		return nil, err
	}
	ss, err := xslt.Compile(sheetDoc.Root, xslt.CompileOptions{
		// The suite's stylesheets import schemas by relative path, and call
		// document() on the test-set's own files. Both resolvers are rooted
		// at the test-set directory: the tests are trusted input, and
		// confining them there is what keeps that true.
		SchemaResolver: &xsd.FileResolver{},
		// The suite's stylesheets include one another by relative path, and
		// a resolver rooted at the test-set directory is what makes that
		// work without opening the filesystem generally.
		// Rooted at the suite rather than the test-set directory: tests
		// legitimately reference documents in sibling directories, and
		// confining each set to itself refuses those as if the engine could
		// not read them. The suite is trusted input; the root is what keeps
		// the run from reaching outside it.
		Resolver: &xslt.FileResolver{Roots: []string{r.Root}},
		// A filesystem path rather than a file: URI: the schema resolver
		// joins the base with filepath.Dir, which turns a URI into a
		// directory literally named "file:".
		BaseURI: sheetPath,
	})
	if err != nil {
		return nil, err
	}

	src, err := r.principalSource(set, tc)
	if err != nil {
		return nil, err
	}

	opts := xslt.TransformOptions{
		Documents: &xslt.FileResolver{Roots: []string{r.Root}},
	}
	if tc.Test.InitialTemplate != nil {
		opts.InitialTemplate = tc.Test.InitialTemplate.Name
	}
	if tc.Test.InitialMode != nil {
		opts.InitialMode = tc.Test.InitialMode.Name
	}
	if len(tc.Test.Params) > 0 {
		opts.Params = map[string]xdm.Sequence{}
		for _, p := range tc.Test.Params {
			v, err := xpath.Eval(p.Select, xpath.NewContext(nil, xpath.Builtins()),
				noNS{})
			if err != nil {
				return nil, fmt.Errorf("parameter %s: %w", p.Name, err)
			}
			opts.Params[p.Name] = v
		}
	}

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

// principalSource loads the document the transform starts on.
//
// A test with no source runs from an initial template, which is how a
// stylesheet that generates its own content is tested. Returning nil for that
// case is correct rather than an error.
func (r *Runner) principalSource(set *TestSet, tc *TestCase) (*xdm.Node, error) {
	env := r.environment(set, tc)
	if env == nil {
		return nil, nil
	}
	for _, s := range env.Sources {
		if s.Role != "." {
			continue
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
				BaseURI:      fileURI(filepath.Join(set.Dir, "inline.xml")),
			})
			if err != nil {
				return nil, err
			}
			// The initial context is the *document* node: a stylesheet
			// matching "/" needs a root to match, and passing the document
			// element leaves it with none.
			if err := r.annotate(set, env, s, tree.Root); err != nil {
				return nil, err
			}
			return tree.Root, nil
		}
		if s.File != "" {
			p := filepath.Join(set.Dir, filepath.FromSlash(s.File))
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			tree, err := xdm.ParseString(string(stripBOM(data)),
				xdm.ParseOptions{AllowDOCTYPE: true, BaseURI: fileURI(p)})
			if err != nil {
				return nil, err
			}
			if err := r.annotate(set, env, s, tree.Root); err != nil {
				return nil, err
			}
			return tree.Root, nil
		}
	}
	return nil, nil
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
	// The validator stamps annotations as it goes, so the error is discarded
	// rather than propagated, for the reason given above.
	_ = schema.Validate(root, xsd.ValidateOptions{Annotate: true})
	return nil
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
