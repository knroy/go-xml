package xpath

import (
	"runtime"
	"testing"
	"unsafe"

	"github.com/knroy/go-xml/v2/xdm"
)

// array:append, array:insert-before and array:join must copy the members once,
// not once in Members() and again into the result.
func TestArrayBuildersCopyMembersOnce(t *testing.T) {
	const n, runs = 10000, 20
	members := make([]xdm.Sequence, n)
	for i := range members {
		members[i] = xdm.One(xdm.NewInteger(int64(i)))
	}
	arr := xdm.One(xdm.NewArray(members...))
	oneCopy := uint64(n * unsafe.Sizeof(xdm.Sequence(nil)))
	const ns = "Q{http://www.w3.org/2005/xpath-functions/array}"
	for _, q := range []string{
		ns + "append($a, 1)", ns + "insert-before($a, 5, 1)", ns + "join(($a))",
	} {
		c, err := CompileWith(q, CompileOptions{Version: XPath31})
		if err != nil {
			t.Fatal(err)
		}
		ctx := NewContext(nil, Builtins()).WithVar(xdm.QName{Local: "a"}, arr)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		for i := 0; i < runs; i++ {
			if _, err := c.Eval(ctx); err != nil {
				t.Fatal(err)
			}
		}
		runtime.ReadMemStats(&after)
		per := (after.TotalAlloc - before.TotalAlloc) / runs
		if per > oneCopy*3/2 {
			t.Errorf("%s allocated %d bytes per call; one copy of the members is %d", q, per, oneCopy)
		}
	}
}
