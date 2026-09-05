package xpath

import "sync"

// boundedCache is a memo table that never holds more than max entries.
//
// The package has three of these — compiled regexps, compiled character atoms,
// and parsed UCA collations — and all three exist for the same reason: the key
// space is not fixed by the stylesheet. "matches($s, $node/@pattern)" takes its
// pattern from document data, so the set of keys a long-running process sees is
// chosen by whoever supplies the documents, and an unbounded memo table would
// retain one compiled artefact per distinct key it had ever seen.
//
// Eviction is wholesale: when the table is full it is emptied and refilled from
// scratch. That is deliberate and is not a placeholder for an LRU. A true LRU
// has to record a use on every *read*, which means taking a write lock on the
// hot path; here the working set is normally a handful of stylesheet patterns
// that are re-cached immediately after a clear, so the recency bookkeeping
// would cost more than the misses it avoids. The same reasoning is recorded at
// xslt/resolver.go's document cache.
//
// Correctness never depends on a hit: every value is reproducible from its key,
// so a clear is a performance event and nothing more.
//
// The lock is a plain Mutex rather than an RWMutex even though reads vastly
// outnumber writes once the working set is warm. An RWMutex would let those
// reads proceed in parallel, but the critical section here is a single map
// lookup — tens of nanoseconds — and RWMutex.RLock is itself two atomic
// operations on one shared cache line, so at this critical-section length the
// reader-parallelism it buys does not pay for the extra atomic traffic. The
// contended case that would justify an RWMutex is a long read, which this is
// not. Mutex is also the smaller and simpler primitive, which matters for a
// type instantiated three times over three different value shapes.
type boundedCache[K comparable, V any] struct {
	mu    sync.Mutex
	items map[K]V
	max   int
}

// Get returns the memoised value for k.
func (c *boundedCache[K, V]) Get(k K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[k]
	return v, ok
}

// Put memoises v under k, clearing the table wholesale first if it is full.
//
// The check and the insert happen under one lock hold, which is the whole point
// of the type. The previous form of this code checked an atomic size counter,
// cleared, and then inserted into a sync.Map as three separate steps; concurrent
// callers interleaved between the check and the insert and drove the table past
// its bound. The overshoot was bounded by the number of goroutines in flight
// rather than by the volume of input — measured at a peak of 1655 entries
// against a bound of 1024 with 200 concurrent callers, and 3454 with 800 — so it
// was a violated bound rather than unbounded growth, but a bound that only holds
// on one goroutine is not a bound.
func (c *boundedCache[K, V]) Put(k K, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[K]V)
	}
	if len(c.items) >= c.max {
		c.items = make(map[K]V)
	}
	c.items[k] = v
}

// Len reports the number of entries held.
func (c *boundedCache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}
