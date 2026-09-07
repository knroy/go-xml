// Command corpora loads production schemas and reports which ones fail.
//
// It is the safety net for new schema-validity rules: the W3C suite scores
// agreement with its own labels, so a rule that is merely *too strict* shows
// up only if the suite happens to contain a valid schema exercising it. Real
// schemas catch it. Re-run this after every schema-validity change.
//
//	corpora maindoc  <dir>    # load each <dir>/maindoc/*.xsd on its own (UBL)
//	corpora walk     <dir>    # load every .xsd under <dir> on its own (CII)
//	corpora vendored <dir>... # the same, over the schemas vendored in testdata/
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: corpora {maindoc|walk|vendored} <dir>... [-11]")
		os.Exit(2)
	}
	mode, args := os.Args[1], os.Args[2:]
	switch mode {
	case "maindoc":
		runMainDoc(args)
	case "walk":
		runWalk(args)
	case "vendored":
		// UBL and CII are licensed and cannot be vendored, so the
		// guard they provide is absent from every checkout that does
		// not already have them. These schemas are real, are already
		// here, and answer the same question: does a rule reject a
		// schema that people actually wrote?
		runVendored(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", mode)
		os.Exit(2)
	}
}

func runMainDoc(args []string) {
	root := args[0]
	version := xsd.Version10
	var instanceDirs []string
	for _, a := range args[1:] {
		if a == "-11" {
			version = xsd.Version11
			continue
		}
		instanceDirs = append(instanceDirs, a)
	}

	mains, _ := filepath.Glob(filepath.Join(root, "maindoc", "*.xsd"))
	sort.Strings(mains)

	// Each main document is loaded on its own: they declare different root
	// elements in different namespaces, and one schema holding all of them
	// would say nothing about whether any single one assembles.
	loaded := map[string]*xsd.Schema{}
	var failed int
	for _, m := range mains {
		s, err := xsd.LoadFiles([]string{m},
			xsd.Options{Version: version, Resolver: &xsd.FileResolver{},
				ParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}})
		if err != nil {
			failed++
			fmt.Printf("LOADFAIL %s\n  %s\n", filepath.Base(m), firstLine(err.Error()))
			continue
		}
		loaded[filepath.Base(m)] = s
	}
	fmt.Printf("schemas: %d loaded, %d failed\n", len(loaded), failed)

	if len(instanceDirs) == 0 {
		return
	}

	// An instance is matched to a schema by its root element's expanded
	// name, which is what a real validator would have to do too.
	var docs []string
	for _, d := range instanceDirs {
		found, _ := filepath.Glob(filepath.Join(d, "*.xml"))
		docs = append(docs, found...)
	}
	sort.Strings(docs)

	var ok, bad, unmatched int
	for _, path := range docs {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		tree, err := xdm.Parse(f, xdm.ParseOptions{})
		f.Close()
		if err != nil {
			continue
		}
		els := tree.Root.ChildElements()
		if len(els) == 0 {
			continue
		}
		name := xdm.QName{URI: els[0].Name.URI, Local: els[0].Name.Local}

		var chosen *xsd.Schema
		for _, s := range loaded {
			if _, has := s.Elements[name]; has {
				chosen = s
				break
			}
		}
		if chosen == nil {
			unmatched++
			fmt.Printf("NOSCHEMA %s ({%s}%s)\n",
				filepath.Base(path), name.URI, name.Local)
			continue
		}
		if err := chosen.Validate(tree.Root, xsd.ValidateOptions{MaxErrors: 3}); err != nil {
			bad++
			fmt.Printf("INVALID %s\n  %s\n", filepath.Base(path), err)
			continue
		}
		ok++
	}
	fmt.Printf("instances: %d valid, %d invalid, %d unmatched\n", ok, bad, unmatched)
}

func runWalk(args []string) {
	root := args[0]
	version := xsd.Version10
	for _, a := range args[1:] {
		if a == "-11" {
			version = xsd.Version11
		}
	}
	var files []string
	filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(p, ".xsd") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	var ok, failed int
	for _, f := range files {
		_, err := xsd.LoadFiles([]string{f},
			xsd.Options{Version: version, Resolver: &xsd.FileResolver{},
				ParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}})
		if err != nil {
			failed++
			fmt.Printf("FAIL\t%s\t%s\n", f, firstLine(err.Error()))
		} else {
			ok++
		}
	}
	fmt.Fprintf(os.Stderr, "schemas: %d loaded, %d failed\n", ok, failed)
}

// Schemas excluded from the vendored corpus, each with the reason it is not a
// standalone schema. They are COUNTED and NAMED in the summary rather than
// quietly dropped: the point of this corpus is that a rule which starts
// rejecting a real schema is visible, and an exclusion list that can grow
// without being read is the one way to make that number lie.
//
// Nothing here is excluded for failing. Each is excluded because loading it
// alone is not a question with a right answer -- it is a fragment, or it is
// test data whose whole purpose is to be rejected.
var vendoredExclude = map[string]string{
	// Deliberately invalid: the file name is the error code the case exists
	// to raise. Rejecting these is correct, so scoring them as loads would
	// reward the wrong behaviour.
	"XQST0012.xsd": "deliberately invalid test data (refs undeclared element)",
	"XQST0035.xsd": "deliberately invalid test data (duplicate declaration)",

	// Halves of a target namespace: hats:hatsize is declared by the sibling
	// qischema041a/042a, and these are imported alongside it by the cases
	// that use them. Alone they are incomplete by construction.
	"qischema041.xsd":    "fragment; hats:hatsize comes from qischema041a.xsd",
	"qischema041dup.xsd": "fragment; hats:hatsize comes from qischema041a.xsd",
	"qischema042.xsd":    "fragment; hats:hatsize comes from qischema042a.xsd",

	// Refs xs:schema without importing the XSD namespace; it is meant to be
	// loaded together with the schema-for-schemas, not on its own.
	"schema-for-xslt30.xsd": "fragment; refs xs:schema without importing it",
}

// runVendored loads every .xsd under the given roots on its own and reports
// how many assemble. See the comment on the "vendored" case in main.
func runVendored(roots []string) {
	var files []string
	for _, root := range roots {
		filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() && strings.HasSuffix(p, ".xsd") {
				files = append(files, p)
			}
			return nil
		})
	}
	sort.Strings(files)

	var ok, failed, excluded int
	for _, f := range files {
		if why, skip := vendoredExclude[filepath.Base(f)]; skip {
			excluded++
			fmt.Printf("EXCLUDED\t%s\t%s\n", f, why)
			continue
		}
		// XSD 1.1 is the version these are read at because it is a
		// superset here: every schema that assembles under 1.0 also
		// assembles under 1.1, and the schema-for-schemas in the XSLT
		// catalog is 1.1 by its own DOCTYPE. Reading real schemas at
		// the older version would score them against a question their
		// authors never answered.
		_, err := xsd.LoadFiles([]string{f},
			xsd.Options{Version: xsd.Version11, Resolver: &xsd.FileResolver{},
				ParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}})
		if err != nil {
			failed++
			fmt.Printf("FAIL\t%s\t%s\n", f, firstLine(err.Error()))
			continue
		}
		ok++
	}
	// Excluded is printed beside the score for the same reason the XSD
	// driver prints out-of-scope: the size of what is not being measured
	// has to stay as visible as what is.
	fmt.Fprintf(os.Stderr, "vendored schemas: %d loaded, %d failed, %d excluded (of %d)\n",
		ok, failed, excluded, len(files))
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i > 0 {
		return s[:i]
	}
	return s
}
