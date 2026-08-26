package xpath

// Version selects the version of the XPath language an expression is written
// in.
//
// The two versions are not a superset relationship in every detail. XPath 3.0
// adds constructs — non-capturing groups and reluctant quantifiers in regular
// expressions, the "q" flag, "let", function items — but a 2.0 processor is
// required to *reject* those, not to accept them quietly. A stylesheet that
// relies on one and is run as 2.0 must fail here exactly as it would on any
// other conforming processor, rather than working here and failing there.
//
// The zero value is XPath20, so every existing caller keeps the behaviour it
// had before this type existed. Parse compiles 2.0; ParseVersion opts in.
type Version int

const (
	// XPath20 is XPath 2.0, as defined by the 2010 Recommendation.
	XPath20 Version = iota
	// XPath30 is XPath 3.0, as defined by the 2014 Recommendation. It admits
	// everything in 2.0 with the same meaning, plus the 3.0 additions.
	XPath30
)

// atLeast30 reports whether v admits the XPath 3.0 additions.
//
// Written as a method rather than a bare ">= XPath30" comparison at each use
// so that adding 3.1 later does not mean auditing every comparison for whether
// it meant "exactly 3.0" or "3.0 and later".
func (v Version) atLeast30() bool { return v >= XPath30 }

// String implements fmt.Stringer.
func (v Version) String() string {
	switch v {
	case XPath30:
		return "XPath 3.0"
	default:
		return "XPath 2.0"
	}
}
