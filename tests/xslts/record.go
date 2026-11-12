package xslts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/knroy/go-xml/v2/internal/record"
	"github.com/knroy/go-xml/v2/internal/streamtrace"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xslt"
)

// GOXSLT_STREAM_OFF=1 sends every streamable xsl:source-document to the tree
// for the whole run: the third leg of tests/streamdiff. GOXSLT_STREAM_NORERUN=1
// makes a container that fails while streaming return its streamed error, not
// run again on the tree: its fourth, the norerun leg.
func init() {
	if os.Getenv("GOXSLT_STREAM_OFF") == "1" {
		streamtrace.SetOff(true)
	}
	if os.Getenv("GOXSLT_STREAM_NORERUN") == "1" {
		streamtrace.SetNoRerun(true)
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
	line := key
	for _, n := range append(append(c.Runs[:], c.Fallbacks, c.Guards, c.Reruns), c.Kinds[:]...) {
		line += fmt.Sprintf("\t%d", n)
	}
	appendStreams(suite, line)
}

// appendStreams appends line to <GOXSLT_RECORD_DIR>/streams/<name>.tsv.
func appendStreams(name, line string) {
	dir := filepath.Join(os.Getenv("GOXSLT_RECORD_DIR"), "streams")
	err := os.MkdirAll(dir, 0o755)
	var f *os.File
	if err == nil {
		f, err = os.OpenFile(filepath.Join(dir, name+".tsv"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	}
	if err == nil {
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

// recordVerdict appends, for a judged case, one line to
// <GOXSLT_RECORD_DIR>/streams/<suite>-verdicts.tsv: the key, "pass" or
// "fail", and the error code the judge reads from terr ("ok" for none, "-"
// for an error with no code). tests/streamdiff's norerun leg compares the
// streamed error codes with the tree's by it.
func (r *Runner) recordVerdict(set *TestSet, tc *TestCase, terr error, pass bool) {
	if !record.Enabled() {
		return
	}
	suite := "xslt" + strings.NewReplacer("XSLT ", "", ".", "").Replace(r.Target.String())
	v := "fail"
	if pass {
		v = "pass"
	}
	appendStreams(suite+"-verdicts", fmt.Sprintf("%s/%s\t%s\t%s", set.Name, tc.Name, v, judgedCode(terr)))
}

// judgedCode is the code judge reads from terr: the leading one, else
// xdm.ErrorCode's.
func judgedCode(terr error) string {
	if terr == nil {
		return "ok"
	}
	if code, _, ok := strings.Cut(terr.Error(), ":"); ok && isErrorCode(code) {
		return code
	}
	if code := xdm.ErrorCode(terr); code != "" {
		return code
	}
	return "-"
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
