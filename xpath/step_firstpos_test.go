package xpath

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// A step whose first predicate is an integer literal stops at that position
// rather than building every match; the result must not change.
func TestStepLiteralPositionStopsEarly(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("<r>")
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&sb, `<l id="%d"/>`, i)
		if i == 3 {
			sb.WriteString(`<x id="x"/>`)
		}
	}
	sb.WriteString("</r>")
	tr, err := xdm.ParseString(sb.String(), xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	eval := func(src string) string {
		ctx := NewContext(tr.Root, Builtins())
		v, err := MustCompile(src, nil).Eval(ctx)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		var ids []string
		for _, it := range v {
			ids = append(ids, it.(*xdm.Node).AttrValue("id"))
		}
		return strings.Join(ids, ",")
	}
	for src, want := range map[string]string{
		"/r/l[1]":                      "1",
		"/r/l[3]":                      "3",
		"/r/l[0]":                      "",
		"/r/l[1.5]":                    "",
		"/r/l[2001]":                   "",
		"/r/l[last()]":                 "2000",
		"/r/l[3]/following-sibling::x": "x",
		"/r/x/preceding-sibling::l[1]": "3",
		"/r/x/preceding-sibling::l[3]": "1",
		"/r/x/preceding-sibling::*[4]": "",
		"/r/x/following-sibling::l[1]": "4",
		"/r/l[2][1]":                   "2",
		"/r/l[2][2]":                   "",
		"/r/l[1][@id='1']":             "1",
		"/r/l[2][@id='1']":             "",
		"/r/l[@id > 1][1]":             "2",
		"/r/*[4]":                      "x",
		"(/r/l)[1]":                    "1",
		"/r/l[position() = 2]":         "2",
	} {
		if got := eval(src); got != want {
			t.Errorf("%s = %q, want %q", src, got, want)
		}
	}
	bytes := func(src string) uint64 {
		c := MustCompile(src, nil)
		ctx := NewContext(tr.Root, Builtins())
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		for i := 0; i < 20; i++ {
			c.Eval(ctx)
		}
		runtime.ReadMemStats(&b)
		return b.TotalAlloc - a.TotalAlloc
	}
	if first, last := bytes("/r/l[1]"), bytes("/r/l[2000]"); first*4 > last {
		t.Errorf("/r/l[1] allocated %d bytes, /r/l[2000] %d: the first did not stop early", first, last)
	}
}
