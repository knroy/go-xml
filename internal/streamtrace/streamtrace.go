// Package streamtrace counts what the streamed execution of
// xsl:source-document did, so the differential tests can say how many
// containers took the streamed path, by which strategy, how many fell back to
// the tree, and whether a guard ever fired. It also holds the switch that
// turns streaming off for a whole run (tests/streamdiff's third leg).
//
// It is compiled into the engine but only tests read it; the counters are
// process-wide atomics, so a reader takes a Snapshot before and after the
// work it wants to attribute and subtracts. Nothing here may change a result.
package streamtrace

import "sync/atomic"

// Strategy is how one streamed container run was executed.
type Strategy int

const (
	// SelectFolded is a value-of, copy-of or sequence whose select is a
	// count, sum, min or max of a record path, folded burst by burst.
	SelectFolded Strategy = iota
	// SelectCollected is any other such select: each burst's items are
	// collected and the rest of the select evaluated once over them.
	SelectCollected
	// LoopForEach and LoopIterate are the loop shape: the body runs per item.
	LoopForEach
	LoopIterate
	NumStrategies
)

var names = [NumStrategies]string{"fold", "collect", "for-each", "iterate"}

func (s Strategy) String() string { return names[s] }

var (
	off       atomic.Bool
	runs      [NumStrategies]atomic.Int64
	fallbacks atomic.Int64
	guards    atomic.Int64
)

// Off reports whether streaming is switched off: every container takes the
// tree path, as with xslt's noBursts.
func Off() bool { return off.Load() }

// SetOff switches streaming off (true) or back on.
func SetOff(b bool) { off.Store(b) }

// Run counts one container run on the streamed path.
func Run(s Strategy) { runs[s].Add(1) }

// Fallback counts one streamed run handed back to the tree after an error.
func Fallback() { fallbacks.Add(1) }

// Guard counts one hit of a runtime guard: a streamed run that met
// something the static gate should have kept off the streamed path. No guard
// is wired yet (C2 adds them); the count must stay 0, and tests/streamdiff
// fails when it is not.
func Guard() { guards.Add(1) }

// Counts is a reading of the counters.
type Counts struct {
	Runs      [NumStrategies]int64
	Fallbacks int64
	Guards    int64
}

// Snapshot reads the counters.
func Snapshot() Counts {
	var c Counts
	for i := range runs {
		c.Runs[i] = runs[i].Load()
	}
	c.Fallbacks, c.Guards = fallbacks.Load(), guards.Load()
	return c
}

// Sub is c minus an earlier reading d: what happened in between.
func (c Counts) Sub(d Counts) Counts {
	for i := range c.Runs {
		c.Runs[i] -= d.Runs[i]
	}
	c.Fallbacks -= d.Fallbacks
	c.Guards -= d.Guards
	return c
}

// Streamed is the number of container runs on the streamed path.
func (c Counts) Streamed() int64 {
	var n int64
	for _, r := range c.Runs {
		n += r
	}
	return n
}
