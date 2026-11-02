package xslts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/knroy/go-xml/v2/internal/record"
	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xslt"
)

// GOXSLT_STREAM_OFF=1 sends every streamable xsl:source-document to the tree
// for the whole run: the third leg of tests/streamdiff.
func init() {
	if os.Getenv("GOXSLT_STREAM_OFF") == "1" {
		streamtrace.SetOff(true)
	}
}

// streamMark is the streamtrace reading taken as the current case started.
var streamMark streamtrace.Counts

func markStreams() { streamMark = streamtrace.Snapshot() }

// recordCase writes what a case produced when GOXSLT_RECORD_DIR is set (see
// internal/record): the error, or the serialised principal result followed by
// each secondary result in order. It only reads the result; a panic while
// rendering is recorded rather than allowed to end the run.
func (r *Runner) recordCase(set *TestSet, tc *TestCase, res *xslt.Result, terr error) {
	if !record.Enabled() {
		return
	}
	suite := "xslt" + strings.NewReplacer("XSLT ", "", ".", "").Replace(r.Target.String())
	key := set.Name + "/" + tc.Name
	record.Write(suite, key, []byte(renderResult(res, terr)))
	writeStreams(suite, key)
}

// writeStreams appends, for a case that ran a container on the streamed path,
// one line to <GOXSLT_RECORD_DIR>/streams/<suite>.tsv: the key, its runs per
// streamtrace strategy in Strategy order (core, main), its fallbacks to the tree, its guard hits, its containers re-run on
// the tree, and the core's sNodes per streamtrace Kind in Kind order. It is a
// subdirectory so that tests/recdiff, which reads every <dir>/*.tsv as a
// recording, never sees it.
func writeStreams(suite, key string) {
	c := streamtrace.Snapshot().Sub(streamMark)
	if c.Streamed() == 0 && c.Guards == 0 {
		return
	}
	dir := filepath.Join(os.Getenv("GOXSLT_RECORD_DIR"), "streams")
	err := os.MkdirAll(dir, 0o755)
	var f *os.File
	if err == nil {
		f, err = os.OpenFile(filepath.Join(dir, suite+".tsv"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	}
	if err == nil {
		line := key
		for _, n := range append(append(c.Runs[:], c.Fallbacks, c.Guards, c.Reruns), c.Kinds[:]...) {
			line += fmt.Sprintf("\t%d", n)
		}
		_, err = fmt.Fprintln(f, line)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "record streams:", err)
		os.Exit(3)
	}
}

func renderResult(res *xslt.Result, terr error) (s string) {
	defer func() {
		if p := recover(); p != nil {
			s = fmt.Sprintf("record panic: %v", p)
		}
	}()
	if terr != nil {
		return fmt.Sprintf("error: %v\n", terr)
	}
	if res == nil {
		return "no result\n"
	}
	var b strings.Builder
	if err := res.Serialize(&b); err != nil {
		fmt.Fprintf(&b, "\nserialize error: %v", err)
	}
	for i := range res.Secondary {
		fmt.Fprintf(&b, "\n--- secondary %q\n", res.Secondary[i].Href)
		if err := res.Secondary[i].Serialize(&b, nil); err != nil {
			fmt.Fprintf(&b, "\nserialize error: %v", err)
		}
	}
	return b.String()
}
