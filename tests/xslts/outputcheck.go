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
//
// The second run writes the secondary results to TransformOptions.SecondaryOutput
// too, and each writer's bytes are compared with SecondaryResult.Serialize's
// for the first run's result of the same URI, the set of URIs with them; a
// serialization error of a secondary result is the transform's error under
// SecondaryOutput, and so is compared as one. The line's fourth field is the
// number of secondary results the case wrote.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
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
	want, werr := rendered(res, terr)
	var got bytes.Buffer
	sec := secondaryWriters{}
	before := xdmsink.Opened.Load()
	r.output, r.secondary = &got, sec.open
	gres, gerr := r.transformSafely(set, tc)
	r.output, r.secondary = nil, nil
	opened := xdmsink.Opened.Load() - before
	if gerr == nil && gres != nil && len(gres.Secondary) > 0 {
		gerr = fmt.Errorf("Result.Secondary holds %d results under SecondaryOutput", len(gres.Secondary))
	}
	if gerr == nil {
		gerr = sec.closedOnce()
	}
	gotText := sec.render(got.String())
	status := "same"
	switch {
	case (werr == nil) != (gerr == nil) || werr != nil && werr.Error() != gerr.Error():
		status = "errdiff"
	case werr == nil && want != gotText:
		status = "diff"
	}
	if status != "same" {
		res2, aerr := r.transformSafely(set, tc)
		again, aerr := rendered(res2, aerr)
		if (aerr == nil) != (werr == nil) || aerr != nil && aerr.Error() != werr.Error() || again != want {
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
	fmt.Fprintf(outputCheck.f, "%s\t%s\t%d\t%d\n", key, status, opened, len(sec))
	if status != "same" {
		fmt.Fprintf(outputCheck.diff, "== %s %s\n-- tree (err %v)\n%s\n-- output (err %v)\n%s\n",
			key, status, werr, want, gerr, gotText)
	}
}

// rendered is res's principal result serialized, then each secondary result
// by URI, as secondaryWriters.render has them; the error is terr, else the
// first secondary result's serialization error, else the principal's.
func rendered(res *xslt.Result, terr error) (string, error) {
	if terr != nil || res == nil {
		return "", terr
	}
	sec := secondaryWriters{}
	for _, sr := range res.Secondary {
		key := sr.BaseURI
		if key == "" {
			key = sr.Href
		}
		b := &secondaryBuf{}
		if err := sr.Serialize(b, nil); err != nil {
			return "", err
		}
		b.closed = 1
		sec[key] = b
	}
	var p bytes.Buffer
	if err := res.Serialize(&p); err != nil {
		return "", err
	}
	return sec.render(p.String()), nil
}

// secondaryWriters is a SecondaryOutput keeping each result by its URI.
type secondaryWriters map[string]*secondaryBuf

type secondaryBuf struct {
	bytes.Buffer
	closed int
}

func (b *secondaryBuf) Close() error { b.closed++; return nil }

func (s secondaryWriters) open(href string) (io.WriteCloser, error) {
	if s[href] != nil {
		return nil, fmt.Errorf("outputcheck: %s opened twice", href)
	}
	b := &secondaryBuf{}
	s[href] = b
	return b, nil
}

func (s secondaryWriters) closedOnce() error {
	for k, b := range s {
		if b.closed != 1 {
			return fmt.Errorf("outputcheck: %s closed %d times", k, b.closed)
		}
	}
	return nil
}

// render is principal, then each result by URI, in URI order.
func (s secondaryWriters) render(principal string) string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(principal)
	for _, k := range keys {
		fmt.Fprintf(&b, "\n--- %s\n%s", k, s[k].String())
	}
	return b.String()
}
