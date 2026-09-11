//go:build ignore

// Command conformance-docs regenerates the conformance summary in
// docs/conformance-gaps.md from tests/conformance/results.json.
//
//	go run tests/conformance-docs.go          # rewrite the document
//	go run tests/conformance-docs.go -check   # fail if it would change
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
	check := flag.Bool("check", false, "do not write; exit non-zero if the document is out of date")
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

	if *check {
		old, err := os.ReadFile(docPath)
		if err != nil {
			die(err)
		}
		next, err := conformance.Replace(string(old), results)
		if err != nil {
			die(fmt.Errorf("%s: %w", docPath, err))
		}
		if next != string(old) {
			die(fmt.Errorf("docs/conformance-gaps.md is out of date with tests/conformance/results.json\n"+
				"    run: go run tests/conformance-docs.go\n"+
				"    the generated total from the JSON is %d", results.Total()))
		}
		fmt.Printf("docs/conformance-gaps.md is current; W3C disagreements total %d\n", results.Total())
		return
	}

	changed, err := conformance.Apply(docPath, results)
	if err != nil {
		die(err)
	}
	if changed {
		fmt.Printf("wrote docs/conformance-gaps.md (total %d)\n", results.Total())
	} else {
		fmt.Printf("docs/conformance-gaps.md unchanged (total %d)\n", results.Total())
	}
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
