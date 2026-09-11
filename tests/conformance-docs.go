//go:build ignore

// Command conformance-docs regenerates every published figure in this
// repository's documentation from tests/conformance/results.json and from the
// source tree.
//
//	go run tests/conformance-docs.go          # rewrite every generated region
//	go run tests/conformance-docs.go -check   # fail if any would change
//
// docs/stats.md is generated end to end: it is the one page carrying every
// figure with the command that produced it and the date it was measured. Every
// other figure lives in a small marked region inside hand-written prose, and
// only that region is rewritten.
//
// It is build-tagged `ignore` so that `go build ./...` and `go vet ./...` do
// not try to compile a main package into tests/, which holds suite harnesses
// and no command; the logic it calls lives in tests/conformance and is
// compiled, vetted and unit-tested there.
//
// It reads two files and writes one. It reaches no network: a figure that
// depends on something fetched at generation time is a figure nobody can
// reproduce from a clean checkout, which is the defect this whole mechanism
// exists to close.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/knroy/go-xml/tests/conformance"
)

func main() {
	check := flag.Bool("check", false, "do not write; exit non-zero if any generated region is out of date")
	counts := flag.Bool("counts", false, "print the tree-derived counts as \"id N\" lines and exit; written for tests/check.sh, which compares them with the grep the documentation quotes")
	flag.Parse()

	root, err := repoRoot()
	if err != nil {
		die(err)
	}
	resultsPath, docPath := conformance.DefaultPaths(root)

	results, err := conformance.Load(resultsPath)
	if err != nil {
		die(err)
	}
	// The figures derived from the tree are counted now, from the tree as
	// checked out. They are deliberately absent from the JSON: there is
	// nowhere to type a unit-test count, so the generator cannot publish one
	// the repository does not have.
	if err := results.Count(root); err != nil {
		die(err)
	}

	// -counts writes nothing. tests/check.sh uses it to ask the generator what
	// it counted, so that the Go walker and the grep pipeline the prose quotes
	// can be compared: two implementations of one claim, which is the only
	// arrangement under which "counted by <command>" stays true.
	if *counts {
		for _, t := range results.Tree {
			fmt.Printf("%s %d\n", t.ID, t.Value)
		}
		return
	}

	files, err := results.ApplyAll(root, !*check)
	if err != nil {
		die(err)
	}

	var stale []string
	for _, f := range files {
		if f.Changed {
			stale = append(stale, f.Path)
		}
	}

	if *check {
		if len(stale) > 0 {
			die(fmt.Errorf("these files are out of date with tests/conformance/results.json and the tree:\n"+
				"        %s\n"+
				"    run: go run tests/conformance-docs.go\n"+
				"    the generated W3C disagreement total from the JSON is %d",
				strings.Join(stale, "\n        "), results.Total()))
		}
		fmt.Printf("%d generated regions across %d files are current; W3C disagreements total %d\n",
			regionCount(results), len(files), results.Total())
		return
	}

	if len(stale) == 0 {
		fmt.Printf("every generated region is unchanged (total %d)\n", results.Total())
		return
	}
	for _, f := range stale {
		fmt.Printf("wrote %s\n", f)
	}
	fmt.Printf("(W3C disagreements total %d)\n", results.Total())
	_ = docPath
}

func regionCount(r *conformance.Results) int {
	n := 0
	for _, gs := range r.Regions() {
		n += len(gs)
	}
	return n
}

// repoRoot prefers git, and falls back to walking up for go.mod so that the
// generator still works in an exported tree with no .git.
func repoRoot() (string, error) {
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

func die(err error) {
	fmt.Fprintf(os.Stderr, "conformance-docs: %v\n", err)
	os.Exit(1)
}
