package xslt

// The whole-corpus acceptance check for the §19.8 streamability analysis.
//
// The suites in tests/xslts measure the analysis against stylesheets written
// to probe it. This test measures it against stylesheets written to do a job:
// the DocBook xslTNG and XSpec sources under testdata/, some four hundred
// files, none of them about streaming at all. checkStreamability must refuse
// none of them.
//
// The direction is what makes it worth running. A missing XTSE3430 costs a
// suite case, which §19.10 makes optional anyway; a spurious one refuses to
// compile a working stylesheet, and no suite count would show it. Any
// refusal here is a regression whatever the suite counts say.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// streamCorpusDirs names the real-world stylesheet trees, relative to the
// repository's testdata directory.
var streamCorpusDirs = []string{"xsltng", "xspec"}

func TestStreamabilityAcceptsRealWorldCorpora(t *testing.T) {
	base := corpusBase(t)
	var files []string
	for _, dir := range streamCorpusDirs {
		root := filepath.Join(base, dir)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			switch strings.ToLower(filepath.Ext(p)) {
			case ".xsl", ".xslt":
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	if len(files) == 0 {
		t.Skip("no real-world corpora under testdata; set GOXSLT_TESTDATA")
	}

	refusals := 0
	for _, p := range files {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		tree, err := xdm.Parse(f, xdm.ParseOptions{})
		f.Close()
		if err != nil {
			// A file this parser cannot read is not evidence about
			// streamability either way.
			continue
		}
		if e := checkStreamability(tree.Root); e != nil {
			refusals++
			t.Errorf("checkStreamability refused a working stylesheet %s: %v", p, e)
		}
	}
	t.Logf("stream corpus: %d stylesheets, %d XTSE3430 refusals", len(files), refusals)
}

// corpusBase locates the repository's testdata directory. The worktrees this
// analysis is developed in do not carry one, so GOXSLT_TESTDATA names it.
func corpusBase(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("GOXSLT_TESTDATA"); d != "" {
		return d
	}
	return filepath.Join("..", "testdata")
}
