package xpath

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// Sequential \pL{1000} passes Go's nested-repeat check, so a 904-byte pattern
// compiled to a 5 MB program and an 18 KB one needed 462 MB to compile.
func TestRegexProgramSizeIsBounded(t *testing.T) {
	big := strings.Repeat(`\pL{1000}`, 150)
	_, err := CompileRegexp(big, "")
	if !errors.Is(err, xdm.ErrResourceLimit) || !strings.Contains(err.Error(), "FORX0002") {
		t.Fatalf("150 x \\pL{1000}: want FORX0002 + ErrResourceLimit, got %v", err)
	}
	if _, err := CompileBounded(big); !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("CompileBounded: want ErrResourceLimit, got %v", err)
	}
	if _, err := CompileRegexp(strings.Repeat(`\pL{1000}`, 50), ""); err != nil {
		t.Fatalf("50 x \\pL{1000} is under the bound and must compile: %v", err)
	}
}

// replace() was charged after it had built the result: 500 KB of input with a
// 4 KB replacement allocated 12.8 GB before the budget refused it.
func TestReplaceChargesBeforeBuilding(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	_, err := Eval(`replace(string-join(for $i in 1 to 300000 return 'a', ''), '.', `+
		`string-join(for $i in 1 to 4096 return 'b', ''))`, ctx, nil)
	runtime.ReadMemStats(&m1)
	if !errors.Is(err, xdm.ErrResourceLimit) {
		t.Fatalf("want ErrResourceLimit, got %v", err)
	}
	if mb := (m1.TotalAlloc - m0.TotalAlloc) >> 20; mb > 2000 {
		t.Errorf("replace allocated %d MB before refusing", mb)
	}
}

// A width modifier's minimum drove strings.Repeat with nothing charging it:
// [D01,1000000000] allocated 2.8 GB.
func TestPictureWidthIsBounded(t *testing.T) {
	ctx := NewContext(nil, Builtins())
	ctx = ctx.WithEnv(func(e *Env) { e.LibraryVersion = XPath31 })
	_, err := Eval(`format-date(xs:date('2000-01-01'), '[D01,100000000]')`, ctx, nil)
	if !errors.Is(err, xdm.ErrResourceLimit) || !strings.Contains(err.Error(), "FOFD1340") {
		t.Fatalf("want FOFD1340 + ErrResourceLimit, got %v", err)
	}
	if got := mustEval(t, ctx, `format-date(xs:date('2000-01-01'), '[D01,5]')`); got != "00001" {
		t.Errorf("ordinary width changed: %q", got)
	}
}

// The caches bound entries, not bytes, so a long key is not cached at all.
func TestLongKeysAreNotCached(t *testing.T) {
	long := strings.Repeat("a", 1100)
	n := regexCache.Len()
	if _, err := compileXPathRegexp(long, "", XPath20); err != nil {
		t.Fatal(err)
	}
	if regexCache.Len() != n {
		t.Errorf("regexCache cached a %d-byte pattern", len(long))
	}
	m := ucaCache.Len()
	_, _ = ucaCollationFor("http://www.w3.org/2013/collation/UCA?" + long)
	if ucaCache.Len() != m {
		t.Errorf("ucaCache cached a failed %d-byte URI", len(long))
	}
	k := charAtomCache.Len()
	p := &btParser{}
	_, _ = p.compileCharAtom(strings.Repeat("[a]", 400))
	if charAtomCache.Len() != k {
		t.Errorf("charAtomCache cached a long atom")
	}
}
