package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A call site caches its resolved function (FuncCall.resolve). These tests
// hold the cache to the uncached answer at every input the lookup reads:
// the library's contents and parent, the library version, the static host,
// and a library this package cannot see into.

var tName = xdm.QName{URI: "urn:t", Local: "f"}

func ccConst(name xdm.QName, v string) Function {
	return Function{Name: name, Arity: 0,
		Call: func(*Context, []xdm.Sequence) (xdm.Sequence, error) {
			return xdm.One(xdm.NewString(v)), nil
		}}
}

func ccEval(t *testing.T, c *Compiled, ctx *Context) (string, error) {
	t.Helper()
	seq, err := c.Eval(ctx)
	if err != nil {
		return "", err
	}
	if len(seq) != 1 {
		t.Fatalf("got %d items", len(seq))
	}
	return seq[0].(*xdm.Atomic).String(), nil
}

func ccMust(t *testing.T, c *Compiled, ctx *Context, want string) {
	t.Helper()
	got, err := ccEval(t, c, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func ccCompile(t *testing.T, src string) *Compiled {
	t.Helper()
	c, err := CompileVersion(src, nil, XPath31)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A host that adds a function after the call site has been used: the
// replacement, and a new entry shadowing a builtin, are both seen.
func TestCallCacheSeesLibraryAdd(t *testing.T) {
	lib := NewLibrary(Builtins())
	lib.Add(ccConst(tName, "one"))
	ctx := NewContext(nil, lib)
	c := ccCompile(t, "Q{urn:t}f()")
	ccMust(t, c, ctx, "one")
	ccMust(t, c, ctx, "one")
	lib.Add(ccConst(tName, "two"))
	ccMust(t, c, ctx, "two")

	// A variable argument, so the call is not folded at compile time.
	up := ccCompile(t, "upper-case($x)")
	ctx = ctx.WithVar(xdm.QName{Local: "x"}, xdm.One(xdm.NewString("a")))
	ccMust(t, up, ctx, "A")
	lib.Add(Function{Name: xdm.QName{URI: xdm.NSFN, Local: "upper-case"}, Arity: 1,
		Call: func(*Context, []xdm.Sequence) (xdm.Sequence, error) {
			return xdm.One(xdm.NewString("shadowed")), nil
		}})
	ccMust(t, up, ctx, "shadowed")
}

// The same, for a change further down the chain than the context's own
// library, and for a parent swapped out from under it.
func TestCallCacheSeesChainChanges(t *testing.T) {
	base := NewLibrary(Builtins())
	base.Add(ccConst(tName, "base"))
	top := NewLibrary(base)
	ctx := NewContext(nil, top)
	c := ccCompile(t, "Q{urn:t}f()")
	ccMust(t, c, ctx, "base")
	base.Add(ccConst(tName, "base2"))
	ccMust(t, c, ctx, "base2")

	other := NewLibrary(Builtins())
	other.Add(ccConst(tName, "other"))
	top.Parent = other
	ccMust(t, c, ctx, "other")
}

// fn:head is a 3.0 function: a call site that resolved it under a 3.1 library
// must still be XPST0017 under a 2.0 one, and the other way round.
func TestCallCacheKeepsVersionGate(t *testing.T) {
	c, err := CompileVersion("string(head(('x', 'y')))", nil, XPath20)
	if err != nil {
		t.Fatal(err)
	}
	ctx31 := NewContext(nil, Builtins())
	ctx31.LibraryVersion = XPath31
	ctx20 := NewContext(nil, Builtins())
	for i := 0; i < 2; i++ {
		ccMust(t, c, ctx31, "x")
		if _, err := ccEval(t, c, ctx20); err == nil ||
			!strings.Contains(err.Error(), "XPST0017") {
			t.Fatalf("round %d: 2.0 library resolved fn:head (err %v)", i, err)
		}
	}
}

// scopedLib answers by the calling package, as XSLT's package-scoped library
// does, and declares itself stable so the cache applies to it.
type scopedLib struct{ inner FunctionLibrary }

func (s scopedLib) WrappedLibrary() any { return s.inner }
func (s scopedLib) Lookup(n xdm.QName, a int) (Function, bool) {
	return s.inner.Lookup(n, a)
}
func (s scopedLib) LookupFrom(ctx *Context, n xdm.QName, a int) (Function, bool) {
	if n.Equal(tName) && ctx.StaticHost == "hidden" {
		return Function{}, false
	}
	return s.inner.Lookup(n, a)
}

// One call site compiled once and evaluated from two packages keeps answering
// per package: the static host is part of the key.
func TestCallCacheKeysOnStaticHost(t *testing.T) {
	lib := NewLibrary(Builtins())
	lib.Add(ccConst(tName, "seen"))
	ctx := NewContext(nil, scopedLib{inner: lib})
	c := ccCompile(t, "Q{urn:t}f()")
	open, hidden := c.WithStaticHost("open"), c.WithStaticHost("hidden")
	for i := 0; i < 2; i++ {
		ccMust(t, open, ctx, "seen")
		if _, err := ccEval(t, hidden, ctx); err == nil ||
			!strings.Contains(err.Error(), "XPST0017") {
			t.Fatalf("round %d: hidden package resolved the call (err %v)", i, err)
		}
	}
}

// countingLib is a library this package cannot vouch for: its answers may
// change with nothing in the chain changing.
type countingLib struct {
	inner FunctionLibrary
	n     *int
}

func (c countingLib) Lookup(n xdm.QName, a int) (Function, bool) {
	*c.n++
	if n.Equal(tName) {
		return ccConst(tName, strings.Repeat("x", *c.n)), true
	}
	return c.inner.Lookup(n, a)
}

func TestCallCacheSkipsUnknownLibraries(t *testing.T) {
	n := 0
	ctx := NewContext(nil, countingLib{inner: Builtins(), n: &n})
	c := ccCompile(t, "Q{urn:t}f()")
	ccMust(t, c, ctx, "x")
	ccMust(t, c, ctx, "xx")
}

// The point of the cache: a stable chain is looked up once per call site.
func TestCallCacheHitsOnStableChain(t *testing.T) {
	lib := NewLibrary(Builtins())
	lib.Add(ccConst(tName, "v"))
	ctx := NewContext(nil, scopedLib{inner: lib})
	c := ccCompile(t, "Q{urn:t}f()")
	call := c.expr.(*FuncCall)
	ccMust(t, c, ctx, "v")
	first := call.resolved.Load()
	if first == nil {
		t.Fatal("a stable chain was not cached")
	}
	for i := 0; i < 3; i++ {
		ccMust(t, c, ctx, "v")
	}
	if call.resolved.Load() != first {
		t.Fatal("a cache hit replaced the entry")
	}
}
