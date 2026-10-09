package qt3

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/v2/internal/record"
	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xslt"
)

// recordCase writes what a case evaluated to when GOXSLT_RECORD_DIR is set
// (see internal/record): the error, or each item's type and the adaptive
// serialisation of the sequence, plus the serialised text an assertion asked
// for. It only reads the outcome, and a panic while rendering is recorded
// rather than allowed to reach Run's recover and fail the case.
func recordCase(target TargetVersion, ts *TestSet, tc *TestCase, res *outcome, pass bool) {
	if !record.Enabled() {
		return
	}
	record.Write(suiteName(target), ts.Name+"/"+tc.Name, []byte(renderOutcome(res, pass)))
}

func recordPanic(target TargetVersion, ts *TestSet, tc *TestCase, p any) {
	if record.Enabled() {
		record.Write(suiteName(target), ts.Name+"/"+tc.Name, []byte(fmt.Sprintf("PANIC: %v\n", p)))
	}
}

func suiteName(target TargetVersion) string {
	return "qt3-" + strings.ToLower(strings.NewReplacer(" ", "", ".", "").Replace(target.String()))
}

func renderOutcome(res *outcome, pass bool) (s string) {
	defer func() {
		if p := recover(); p != nil {
			s = fmt.Sprintf("record panic: %v", p)
		}
	}()
	var b strings.Builder
	fmt.Fprintf(&b, "pass: %v\n", pass)
	if res.err != nil {
		fmt.Fprintf(&b, "error: %v\n", res.err)
	} else {
		b.WriteString("types:")
		for _, it := range res.seq {
			switch v := it.(type) {
			case *xdm.Atomic:
				b.WriteString(" " + v.TypeName())
			case *xdm.Node:
				b.WriteString(" " + v.Kind().String())
			case *xdm.MapItem:
				b.WriteString(" map")
			case *xdm.ArrayItem:
				b.WriteString(" array")
			default:
				b.WriteString(" function")
			}
		}
		b.WriteString("\nadaptive:\n")
		if err := xslt.Serialize(&b, res.seq, xslt.OutputSettings{Method: "adaptive"}, nil); err != nil {
			fmt.Fprintf(&b, "\nadaptive error: %v", err)
		}
		b.WriteString("\n")
	}
	if res.haveSerial {
		fmt.Fprintf(&b, "serialized (err %v):\n%s\n", res.serialErr, res.serialized)
	}
	return b.String()
}
