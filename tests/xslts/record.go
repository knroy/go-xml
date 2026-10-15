package xslts

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/v2/internal/record"
	"github.com/knroy/go-xml/v2/xslt"
)

// recordCase writes what a case produced when GOXSLT_RECORD_DIR is set (see
// internal/record): the error, or the serialised principal result followed by
// each secondary result in order. It only reads the result; a panic while
// rendering is recorded rather than allowed to end the run.
func (r *Runner) recordCase(set *TestSet, tc *TestCase, res *xslt.Result, terr error) {
	if !record.Enabled() {
		return
	}
	suite := "xslt" + strings.NewReplacer("XSLT ", "", ".", "").Replace(r.Target.String())
	record.Write(suite, set.Name+"/"+tc.Name, []byte(renderResult(res, terr)))
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
