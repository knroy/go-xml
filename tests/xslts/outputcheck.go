package xslts

// The byte check of streamed principal output (TransformOptions.Output).
//
// With GOXSLT_OUTPUT_CHECK=<file>, each case that ran is run a second time
// with its principal result streamed to a buffer, and the bytes and the error
// are compared with what Result.Serialize writes (and reports) for the first
// run. One line per case goes to <file>: the key, "same" or "diff" or
// "errdiff", and how many elements the second run wrote the start of before
// their end (0: the result was held, and written once the transform ended).
// A case that differs is run a third time without Output, and is "unstable"
// instead when that run differs from the first as well: its result changes
// from run to run (the clock, generate-id's counter, a namespace URI made
// per compilation). A case that differs is written out in full to
// <file>.diff.

import (
	"bytes"
	"fmt"
	"os"
	"sync"

	"github.com/knroy/go-xml/v2/internal/xdmsink"
	"github.com/knroy/go-xml/v2/xslt"
)

var outputCheck = struct {
	sync.Mutex
	f, diff *os.File
}{}

func (r *Runner) checkOutput(set *TestSet, tc *TestCase, res *xslt.Result, terr error) {
	path := os.Getenv("GOXSLT_OUTPUT_CHECK")
	if path == "" {
		return
	}
	var want bytes.Buffer
	werr := terr
	if werr == nil && res != nil {
		werr = res.Serialize(&want)
	}
	var got bytes.Buffer
	before := xdmsink.Opened.Load()
	r.output = &got
	_, gerr := r.transformSafely(set, tc)
	r.output = nil
	opened := xdmsink.Opened.Load() - before
	status := "same"
	switch {
	case (werr == nil) != (gerr == nil) || werr != nil && werr.Error() != gerr.Error():
		status = "errdiff"
	case werr == nil && !bytes.Equal(want.Bytes(), got.Bytes()):
		status = "diff"
	}
	if status != "same" {
		var again bytes.Buffer
		res2, aerr := r.transformSafely(set, tc)
		if aerr == nil && res2 != nil {
			aerr = res2.Serialize(&again)
		}
		if (aerr == nil) != (werr == nil) || aerr != nil && aerr.Error() != werr.Error() ||
			!bytes.Equal(again.Bytes(), want.Bytes()) {
			status = "unstable"
		}
	}
	key := set.Name + "/" + tc.Name
	outputCheck.Lock()
	defer outputCheck.Unlock()
	if outputCheck.f == nil {
		var err error
		if outputCheck.f, err = os.Create(path); err != nil {
			panic(err)
		}
		if outputCheck.diff, err = os.Create(path + ".diff"); err != nil {
			panic(err)
		}
	}
	fmt.Fprintf(outputCheck.f, "%s\t%s\t%d\n", key, status, opened)
	if status != "same" {
		fmt.Fprintf(outputCheck.diff, "== %s %s\n-- tree (err %v)\n%s\n-- output (err %v)\n%s\n",
			key, status, werr, want.String(), gerr, got.String())
	}
}
