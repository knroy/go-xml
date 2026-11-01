package xpath

import (
	"runtime"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// TestInlineFunctionCapturesOnlyWhatItReads: a closure whose body does not
// mention $big must not keep it alive, and one that does must still see it.
func TestInlineFunctionCapturesOnlyWhatItReads(t *testing.T) {
	heldAfterGC := func(src string) (uint64, xdm.Sequence) {
		c, err := CompileWith(src, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatal(err)
		}
		out, err := c.Eval(NewContext(nil, Builtins()))
		if err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc, out
	}
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	heldIgnoring, f1 := heldAfterGC(`let $big := (1 to 300000) ! string(.) return function() { 1 }`)
	if grown := int64(heldIgnoring) - int64(before.HeapAlloc); grown > 2<<20 {
		t.Fatalf("closure ignoring $big retains %d bytes", grown)
	}
	runtime.KeepAlive(f1)
	_, f2 := heldAfterGC(`let $big := (1 to 3) ! string(.), $k := 2 return function($i) { $big[$i] || $k }`)
	call := f2[0].(*xdm.FunctionItem)
	got, err := call.Invoke(NewContext(nil, Builtins()), []xdm.Sequence{xdm.One(xdm.NewInteger(2))})
	if err != nil || renderSeq(got) != "22" {
		t.Fatalf("closure over $big and $k gave %v, %v", got, err)
	}
}
