package xdm

import (
	"strconv"
	"testing"
)

// Map benchmarks at the two sizes that matter: a thousand entries, which is
// where an O(n) copy per operation is still invisible, and a hundred thousand,
// where it is not. op:same-key-023 performs a map:put and a map:remove per key
// over a map of 421,875 entries, so the per-operation cost at the larger size
// is the figure that decides whether that case terminates.

func benchMap(b *testing.B, n int) (*MapItem, []*Atomic) {
	b.Helper()
	keys := make([]*Atomic, n)
	bld := NewMapBuilder()
	for i := range keys {
		keys[i] = NewString("k" + strconv.Itoa(i))
		if err := bld.Set(keys[i], One(NewInteger(int64(i)))); err != nil {
			b.Fatal(err)
		}
	}
	return bld.Build(), keys
}

func benchSizes(b *testing.B, f func(b *testing.B, n int)) {
	for _, n := range []int{1000, 100000} {
		b.Run("n="+strconv.Itoa(n), func(b *testing.B) { f(b, n) })
	}
}

// A put of a fresh key into an existing map, the map:put path.
func BenchmarkMapPut(b *testing.B) {
	benchSizes(b, func(b *testing.B, n int) {
		m, _ := benchMap(b, n)
		val := One(NewString("x"))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := m.Put(NewString("new"+strconv.Itoa(i)), val); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// A removal of one present key, the map:remove path.
func BenchmarkMapRemove(b *testing.B) {
	benchSizes(b, func(b *testing.B, n int) {
		m, keys := benchMap(b, n)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := m.Remove(keys[i%n]); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// A lookup of a present key, the map:get and $m($k) path.
func BenchmarkMapLookup(b *testing.B) {
	benchSizes(b, func(b *testing.B, n int) {
		m, keys := benchMap(b, n)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok, err := m.Get(keys[i%n]); err != nil || !ok {
				b.Fatal("lookup failed")
			}
		}
	})
}
