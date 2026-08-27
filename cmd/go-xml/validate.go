package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/knroy/go-xml/relaxng"
	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xsd"
)

// runValidate implements "go-xml validate".
//
// It is a subcommand rather than a flag on the transform path because the two
// take different arguments and mean different things: a transform produces a
// document, a validation produces a verdict. Sharing one flag set would leave
// most of it inapplicable whichever mode the caller chose.
func runValidate(args []string) error {
	fs := flag.NewFlagSet("go-xml validate", flag.ContinueOnError)
	var (
		xsdPaths = fs.String("xsd", "",
			"comma-separated XML Schema documents to validate against")
		rngPath = fs.String("rng", "",
			"RELAX NG schema to validate against")
		version = fs.String("xsd-version", "1.0",
			"XSD version to apply: 1.0 or 1.1")
		xpathVersion = fs.String("xpath-version", "2.0",
			"XPath version for XSD 1.1 assertions and type alternatives: "+
				"2.0, 3.0 or 3.1. 2.0 is what the specification requires; "+
				"raising it makes a schema this engine's rather than portable")
		allowDoctype = fs.Bool("allow-doctype", false,
			"permit a DOCTYPE in the instance documents and expand the entities "+
				"it declares internally")
		root = fs.String("root", "",
			"confine schema include/import to this directory; empty permits any "+
				"readable path, which suits a command line and not a server")
		maxErrors = fs.Int("max-errors", 0,
			"stop after this many failures per document; 0 uses the default")
		quiet = fs.Bool("quiet", false,
			"report only the verdict per document, not each failure")
		keepGoing = fs.Bool("keep-going", false,
			"with several inputs, report every invalid document instead of "+
				"stopping at the first")
	)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"usage: go-xml validate -xsd SCHEMA.xsd [flags] INPUT.xml [INPUT.xml ...]\n"+
				"       go-xml validate -rng SCHEMA.rng [flags] INPUT.xml [INPUT.xml ...]\n\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Exactly one of -xsd and -rng is required; a document is checked against one
schema language at a time, since the two report failures in different terms.

Line and column are reported for every failure, so the instance is parsed with
position tracking on.

Exit status: 0 if every document is valid, 1 otherwise.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	switch {
	case *xsdPaths == "" && *rngPath == "":
		fs.Usage()
		return errors.New("one of -xsd or -rng is required")
	case *xsdPaths != "" && *rngPath != "":
		return errors.New("-xsd and -rng are mutually exclusive: " +
			"a document is validated against one schema language at a time")
	}

	inputs := fs.Args()
	if len(inputs) == 0 {
		return errors.New("no instance documents given")
	}

	validate, err := schemaValidator(*xsdPaths, *rngPath, *version, *xpathVersion,
		*root, *maxErrors)
	if err != nil {
		return err
	}

	// Positions are tracked unconditionally here. The cost is about a tenth
	// more memory, and a validator that cannot say which line failed makes
	// the caller find it by hand — which is the whole job.
	popts := xdm.ParseOptions{AllowDOCTYPE: *allowDoctype, TrackPositions: true}

	var invalid int
	for _, in := range inputs {
		err := validateOne(in, popts, validate, *quiet)
		if err == nil {
			fmt.Printf("%s: valid\n", in)
			continue
		}
		invalid++
		if !*keepGoing && len(inputs) > 1 {
			return fmt.Errorf("%s: %w", in, err)
		}
		fmt.Printf("%s: INVALID\n", in)
		if !*quiet {
			for _, line := range strings.Split(err.Error(), "\n") {
				fmt.Printf("  %s\n", strings.TrimSpace(line))
			}
		}
		if !*keepGoing {
			break
		}
	}
	if invalid > 0 {
		return fmt.Errorf("%d of %d document(s) invalid", invalid, len(inputs))
	}
	return nil
}

// schemaValidator compiles the schema once and returns the check to run per
// document, so that a run over many instances pays for the schema once.
func schemaValidator(xsdPaths, rngPath, version, xpathVersion, root string,
	maxErrors int) (
	func(*xdm.Node) error, error) {

	if rngPath != "" {
		data, err := os.ReadFile(rngPath)
		if err != nil {
			return nil, err
		}
		abs, _ := filepath.Abs(rngPath)
		tree, err := xdm.ParseString(string(data), xdm.ParseOptions{BaseURI: abs})
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", rngPath, err)
		}
		schema, err := relaxng.CompileWithOptions(tree.Root, relaxng.Options{
			Resolver: &rngFileResolver{root: root, base: abs},
			BaseURI:  abs,
		})
		if err != nil {
			return nil, fmt.Errorf("compiling %s: %w", rngPath, err)
		}
		return func(doc *xdm.Node) error { return schema.Validate(doc) }, nil
	}

	var v xsd.Version
	switch version {
	case "1.0":
		v = xsd.Version10
	case "1.1":
		v = xsd.Version11
	default:
		return nil, fmt.Errorf("-xsd-version %q: expected 1.0 or 1.1", version)
	}

	xv, err := parseXPathVersion(xpathVersion)
	if err != nil {
		return nil, err
	}
	// Unlike the transform's flag, this one has a real default rather than
	// "derive it": a schema document states no XPath version anywhere, so
	// there is nothing to derive it from.
	var xvv xpath.Version
	if xv != nil {
		xvv = *xv
	}

	paths := strings.Split(xsdPaths, ",")
	schema, err := xsd.LoadFiles(paths, xsd.Options{
		Resolver:     &xsd.FileResolver{Root: root},
		Version:      v,
		XPathVersion: xvv,
	})
	if err != nil {
		return nil, fmt.Errorf("loading schema: %w", err)
	}
	opts := xsd.ValidateOptions{MaxErrors: maxErrors}
	return func(doc *xdm.Node) error { return schema.Validate(doc, opts) }, nil
}

func validateOne(path string, popts xdm.ParseOptions, validate func(*xdm.Node) error,
	quiet bool) error {

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	abs, _ := filepath.Abs(path)
	popts.BaseURI = abs
	popts.DocumentURI = abs
	tree, err := xdm.ParseString(string(data), popts)
	if err != nil {
		return fmt.Errorf("parsing: %w", err)
	}
	return validate(tree.Root)
}

// rngFileResolver loads the schemas named by externalRef and include.
//
// relaxng ships no file resolver of its own — the package deliberately has no
// filesystem — so containment is implemented here, on the same terms as
// xsd.FileResolver: an empty root permits any readable path, which suits a
// command line, and a named root refuses anything that escapes it through
// "..", a symlink, or an absolute path.
type rngFileResolver struct {
	root string
	base string
}

func (r *rngFileResolver) ResolveSchema(href string) (*xdm.Node, error) {
	if i := strings.Index(href, "://"); i >= 0 && !strings.HasPrefix(href, "file://") {
		return nil, fmt.Errorf("scheme %q is not permitted (only local files)",
			href[:i])
	}
	p := strings.TrimPrefix(href, "file://")
	if !filepath.IsAbs(p) {
		p = filepath.Join(filepath.Dir(r.base), p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, err
	}
	// Symlinks are resolved before the containment check, so a link inside
	// the root cannot be used to reach outside it.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if r.root != "" {
		rootAbs, err := filepath.Abs(r.root)
		if err != nil {
			return nil, err
		}
		if resolved, err := filepath.EvalSymlinks(rootAbs); err == nil {
			rootAbs = resolved
		}
		rel, err := filepath.Rel(rootAbs, abs)
		if err != nil || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%q is outside %s", href, rootAbs)
		}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	tree, err := xdm.ParseString(string(data), xdm.ParseOptions{BaseURI: abs})
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", href, err)
	}
	return tree.Root, nil
}
