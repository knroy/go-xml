// Package streamtrace counts what the streamed execution of
// xsl:source-document did, so the differential tests can say how many
// containers took the streamed path, by which strategy, how many fell back to
// the tree, and whether a guard ever fired. It also holds the switches that
// turn streaming off for a whole run (tests/streamdiff's third leg) and that
// stop a container that fails while streaming from running again on the
// tree (its fourth, the norerun leg).
//
// It is compiled into the engine but only tests read it; the counters are
// process-wide atomics, so a reader takes a Snapshot before and after the
// work it wants to attribute and subtracts. Nothing here may change a result.
package streamtrace

import "sync/atomic"

// Strategy is how one streamed container run was executed.
type Strategy int

const (
	// Core is a container run by the streamed core (xslt/streamexec.go):
	// its plan is a tree of sNodes, counted by Kind. (The burst plans' four
	// strategies, fold, collect, for-each and iterate, were retired into it.)
	Core Strategy = iota
	// Main is a principal input streamed by xslt's TransformReader
	// (xslt/streammain.go): the initial mode's dispatch of the document
	// node, run on the cursor as the core runs an apply op.
	Main
	// Merge is an xsl:merge whose streamable sources are each read on a
	// cursor of their own (xslt/streammerge.go).
	Merge
	NumStrategies
)

var names = [NumStrategies]string{"core", "main", "merge"}

func (s Strategy) String() string { return names[s] }

// Kind is a kind of sNode of the streamed core's plan (DESIGN section 1.6),
// plus Select, a value-of, copy-of, sequence or attribute whose select is
// split (the consuming operand of DESIGN's sElement), and Apply, an
// xsl:apply-templates, xsl:next-match or xsl:apply-imports run on the cursor
// (DESIGN's sApply).
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
	KindApply
	// KindGroup is a streamed xsl:for-each-group (xslt/streamgroup.go).
	KindGroup
	// KindFork is an xsl:fork or xsl:map whose prongs each read the
	// document, on a cursor of their own (xslt/streamfork.go).
	KindFork
	NumKinds
)

var kindNames = [NumKinds]string{"tree", "ground", "seq", "element", "choose", "for-each",
	"where-pop", "source-doc", "select", "apply", "group", "fork"}

func (k Kind) String() string { return kindNames[k] }

var (
	off       atomic.Bool
	noRerun   atomic.Bool
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

// NoRerun reports whether re-runs are switched off: a container that fails
// while streaming returns its streamed error instead of running again on the
// tree. Rerun still counts it.
func NoRerun() bool { return noRerun.Load() }

// SetNoRerun switches re-runs off (true) or back on.
func SetNoRerun(b bool) { noRerun.Store(b) }

// Run counts one container run on the streamed path.
func Run(s Strategy) { runs[s].Add(1) }

// Fallback counts one select of a streamed run evaluated on the tree after
// an error, the run going on.
func Fallback() { fallbacks.Add(1) }

// Rerun counts one container the streamed core ran again on the tree (or,
// under NoRerun, would have).
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
