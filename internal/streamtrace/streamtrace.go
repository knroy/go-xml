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
	// Core is a container run by the streamed core (xslt/streamexec.go):
	// its plan is a tree of sNodes, counted by Kind.
	Core
	NumStrategies
)

var names = [NumStrategies]string{"fold", "collect", "for-each", "iterate", "core"}

func (s Strategy) String() string { return names[s] }

// Kind is a kind of sNode of the streamed core's plan (DESIGN section 1.6),
// plus Select, a value-of, copy-of, sequence or attribute whose select is
// split (the consuming operand of DESIGN's sElement).
type Kind int

const (
	KindTree Kind = iota
	KindGround
	KindSeq
	KindElement
	KindChoose
	KindForEach
	KindWherePop
	KindSourceDoc
	KindSelect
	NumKinds
)

var kindNames = [NumKinds]string{"tree", "ground", "seq", "element", "choose", "for-each",
	"where-pop", "source-doc", "select"}

func (k Kind) String() string { return kindNames[k] }

var (
	off       atomic.Bool
	runs      [NumStrategies]atomic.Int64
	kinds     [NumKinds]atomic.Int64
	fallbacks atomic.Int64
	guards    atomic.Int64
	reruns    atomic.Int64
)

// Off reports whether streaming is switched off: every container takes the
// tree path, as with xslt's noBursts.
func Off() bool { return off.Load() }

// SetOff switches streaming off (true) or back on.
func SetOff(b bool) { off.Store(b) }

// Run counts one container run on the streamed path.
func Run(s Strategy) { runs[s].Add(1) }

// Fallback counts one select of a streamed run evaluated on the tree after
// an error, the run going on.
func Fallback() { fallbacks.Add(1) }

// Rerun counts one container the streamed core ran again on the tree.
func Rerun() { reruns.Add(1) }

// Node counts n sNodes of kind k in a container run by the core.
func Node(k Kind, n int64) { kinds[k].Add(n) }

// Guard counts one hit of a runtime guard: a streamed run that met
// something the static gate should have kept off the streamed path. The
// posture guard of xdm's views counts here (PostureError); the count must
// stay 0, and tests/streamdiff fails when it is not.
func Guard() { guards.Add(1) }

// Counts is a reading of the counters.
type Counts struct {
	Runs      [NumStrategies]int64
	Kinds     [NumKinds]int64
	Fallbacks int64
	Guards    int64
	Reruns    int64
}

// Snapshot reads the counters.
func Snapshot() Counts {
	var c Counts
	for i := range runs {
		c.Runs[i] = runs[i].Load()
	}
	for i := range kinds {
		c.Kinds[i] = kinds[i].Load()
	}
	c.Fallbacks, c.Guards, c.Reruns = fallbacks.Load(), guards.Load(), reruns.Load()
	return c
}

// Sub is c minus an earlier reading d: what happened in between.
func (c Counts) Sub(d Counts) Counts {
	for i := range c.Runs {
		c.Runs[i] -= d.Runs[i]
	}
	for i := range c.Kinds {
		c.Kinds[i] -= d.Kinds[i]
	}
	c.Fallbacks -= d.Fallbacks
	c.Guards -= d.Guards
	c.Reruns -= d.Reruns
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

// PostureError is the panic a posture guard raises: a streamed node was
// asked for what its posture does not have (Op, such as "children", of a
// node of kind Kind). The streamed executor recovers it and runs the
// container on the tree instead; Guard has counted it.
type PostureError struct{ Op, Kind string }

func (e *PostureError) Error() string {
	return "streaming: " + e.Op + " of a streamed " + e.Kind + " was asked for at its start tag (posture guard)"
}
