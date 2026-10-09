package xslt

import (
	"container/list"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

// nestedCacheSize bounds the stylesheets one outer stylesheet keeps compiled
// for fn:transform. DocBook's fp:run-transforms names a handful; a caller
// cycling through more than this just recompiles, as every call did before.
const nestedCacheSize = 16

// nestedCache honours fn:transform's cache option (F&O 3.1 16.3.2: "true
// indicates an expectation that the same stylesheet is likely to be used for
// more than one transformation", default true). It lives on the outer
// Stylesheet, so it is shared by every transform of that stylesheet and
// guarded by mu; the zero value is ready to use.
//
// Only successful compilations are stored. A failure is recompiled, and so
// re-reported with its own error, on every call. A hit skips re-reading a
// stylesheet-location, which F&O permits: fn:transform is nondeterministic
// precisely because "external documents ... change between one invocation
// and the next".
type nestedCache struct {
	mu    sync.Mutex
	order list.List // of *nestedEntry, most recently used first
	byKey map[nestedKey]*list.Element
	// compiles counts stored compilations, for tests.
	compiles int
}

type nestedEntry struct {
	key   nestedKey
	sheet *Stylesheet
}

// nestedKey is everything the compiled stylesheet depends on. node is the
// stylesheet-node, keyed by identity: XDM trees are immutable, and holding
// the pointer in the key keeps the tree alive, so the address cannot be
// reused by another tree while the entry exists. docs and pkgs are the
// resolvers the nested compilation inherits, likewise held by the key.
type nestedKey struct {
	opts       string
	node       *xdm.Node
	docs, pkgs any
}

func (c *nestedCache) get(k nestedKey) *Stylesheet {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[k]
	if !ok {
		return nil
	}
	c.order.MoveToFront(e)
	return e.Value.(*nestedEntry).sheet
}

func (c *nestedCache) put(k nestedKey, s *Stylesheet) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.compiles++
	if e, ok := c.byKey[k]; ok {
		// Two concurrent misses on one key both compiled; keep the later.
		e.Value.(*nestedEntry).sheet = s
		c.order.MoveToFront(e)
		return
	}
	if c.byKey == nil {
		c.byKey = map[nestedKey]*list.Element{}
	}
	c.byKey[k] = c.order.PushFront(&nestedEntry{k, s})
	if c.order.Len() > nestedCacheSize {
		old := c.order.Back()
		c.order.Remove(old)
		delete(c.byKey, old.Value.(*nestedEntry).key)
	}
}

// cachedNestedStylesheet is nestedStylesheet behind rt.cache.
func cachedNestedStylesheet(ctx *xpath.Context, rt transformCaller, opts *xdm.MapItem, useCache bool) (*Stylesheet, error) {
	if rt.cache == nil || !useCache {
		return nestedStylesheet(ctx, rt, opts)
	}
	k, ok := nestedCacheKey(ctx, rt, opts)
	if !ok {
		return nestedStylesheet(ctx, rt, opts)
	}
	if s := rt.cache.get(k); s != nil {
		return s, nil
	}
	s, err := nestedStylesheet(ctx, rt, opts)
	if err == nil {
		rt.cache.put(k, s)
	}
	return s, err
}

// nestedCacheKey builds the key for the options, or reports false when the
// call must not be cached: an option nestedStylesheet would
// reject (it then reports the error itself), a static parameter that is not
// atomic, or a resolver that cannot safely be a map key.
func nestedCacheKey(ctx *xpath.Context, rt transformCaller, opts *xdm.MapItem) (nestedKey, bool) {
	// The compilation sees the caller's resolver only through
	// moduleResolverFor, beneath the per-transform wrappers, so that is the
	// identity keyed. Without one, stylesheet-location is read through the
	// wrapped resolver, which records the read for XTDE1500; a hit would
	// skip that, so such a call is not cached.
	mr := moduleResolverFor(rt.opts.Documents)
	if mr == nil && transformHasAny(opts, "stylesheet-location") {
		return nestedKey{}, false
	}
	if !keyable(mr) || !keyable(rt.pkgs) {
		return nestedKey{}, false
	}
	k := nestedKey{docs: mr, pkgs: rt.pkgs}
	var b strings.Builder
	field := func(s string) { fmt.Fprintf(&b, "%d:%s;", len(s), s) }
	field(ctx.StaticBaseURI)
	for _, name := range []string{"stylesheet-base-uri", "stylesheet-location",
		"stylesheet-text", "package-name", "package-version"} {
		v, ok, err := transformString(opts, name)
		if err != nil {
			return nestedKey{}, false
		}
		if ok {
			field(name)
			field(v)
		}
	}
	if seq, ok := transformOption(opts, "stylesheet-node"); ok {
		it, err := seq.Single()
		if err != nil {
			return nestedKey{}, false
		}
		if k.node, ok = it.(*xdm.Node); !ok {
			return nestedKey{}, false
		}
	}
	v, err := transformXSLTVersion(opts)
	if err != nil {
		return nestedKey{}, false
	}
	field(fmt.Sprint(v))
	static, err := transformParams(opts, "static-params")
	if err != nil {
		return nestedKey{}, false
	}
	for _, name := range slices.Sorted(maps.Keys(static)) {
		field(name)
		fmt.Fprintf(&b, "%d;", len(static[name]))
		for _, it := range static[name] {
			a, ok := it.(*xdm.Atomic)
			if !ok {
				return nestedKey{}, false
			}
			// Type, annotation and canonical lexical form identify an atomic
			// value exactly; a QName also needs its URI and prefix.
			field(fmt.Sprintf("%d/%s/%s", a.Type, a.Derived(), a.DerivedMember()))
			if q := a.QName(); q != nil {
				field(q.Clark() + " " + q.Prefix)
			} else {
				field(a.String())
			}
		}
	}
	k.opts = b.String()
	return k, true
}

// keyable reports whether a resolver can be compared as a map key: nil or a
// pointer. Any other dynamic type might hold a slice or map and panic.
func keyable(v any) bool {
	if v == nil {
		return true
	}
	return reflect.TypeOf(v).Kind() == reflect.Pointer
}
