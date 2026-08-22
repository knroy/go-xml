// Command corpora loads production schemas and reports which ones fail.
//
// It is the safety net for new schema-validity rules: the W3C suite scores
// agreement with its own labels, so a rule that is merely *too strict* shows
// up only if the suite happens to contain a valid schema exercising it. Real
// schemas catch it. Re-run this after every schema-validity change.
//
//	corpora maindoc <dir>   # load each <dir>/maindoc/*.xsd on its own (UBL)
//	corpora walk    <dir>   # load every .xsd under <dir> on its own (CII)
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
		fmt.Fprintln(os.Stderr, "usage: corpora {maindoc|walk} <dir> [-11]")
		os.Exit(2)
	}
	mode, args := os.Args[1], os.Args[2:]
	switch mode {
	case "maindoc":
		runMainDoc(args)
	case "walk":
		runWalk(args)
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
			msg := err.Error()
			if i := strings.Index(msg, "\n"); i > 0 {
				msg = msg[:i]
			}
			fmt.Printf("LOADFAIL %s\n  %s\n", filepath.Base(m), msg)
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
			msg := err.Error()
			if i := strings.Index(msg, "\n"); i > 0 {
				msg = msg[:i]
			}
			fmt.Printf("FAIL\t%s\t%s\n", f, msg)
		} else {
			ok++
		}
	}
	fmt.Fprintf(os.Stderr, "schemas: %d loaded, %d failed\n", ok, failed)
}
