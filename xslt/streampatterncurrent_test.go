package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// patternAt parses a bare stylesheet and returns an element to resolve
// namespaces and the XPath version against.
func patternAt(t *testing.T) *xdm.Node {
	t.Helper()
	sheet := `<xsl:stylesheet version="3.0"
		xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:template match="X"/></xsl:stylesheet>`
	stree, err := xdm.ParseString(sheet, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	var at *xdm.Node
	walkElements(stree.Root, func(el *xdm.Node) bool {
		if isXSL(el, "template") {
			at = el
		}
		return true
	})
	if at == nil {
		t.Fatal("no template element")
	}
	return at
}

// The XSLT 3.0 pattern grammar admits "." as an alternative, so
// ".[starts-with(., ':B:')]" is a legal pattern. It parses to a FilterExpr
// over a ContextItem rather than to a Step, and §19.8.10 applies to its
// predicates exactly as it does to a step's.
func TestContextItemPatternMotionlessness(t *testing.T) {
	at := patternAt(t)

	// streamable-142, whose own stylesheet comments the rule "NOT
	// MOTIONLESS". "." matches a node of any kind, so the context item may
	// have children; atomising it is absorbing, hence consuming.
	t.Run("an absorbing predicate is free-ranging", func(t *testing.T) {
		free, known := patternIsFreeRanging(".[starts-with(., ':B:')]", at)
		if !known || !free {
			t.Errorf("a predicate that atomises \".\" is absorbing, so the "+
				"pattern is not motionless; got free=%v known=%v", free, known)
		}
	})

	// THE ACCEPTANCE DIRECTION. Reading an attribute of the context node
	// costs nothing -- §19.8.10 lists "@price[starts-with(., '$')]" among
	// its motionless patterns for the same reason -- so a ".[@a='x']" rule
	// must still compile.
	t.Run("an attribute predicate stays motionless", func(t *testing.T) {
		free, known := patternIsFreeRanging(".[@a = 'x']", at)
		if !known || free {
			t.Errorf("reading an attribute of the matched node is "+
				"motionless; got free=%v known=%v", free, known)
		}
		if err := compileModeSheet(t, modeSheet(
			`<xsl:template match=".[@a = 'x']" mode="s"/>`)); err != nil {
			t.Fatalf("a motionless \".\" pattern is a valid streamable "+
				"template rule, but it was refused: %v", err)
		}
	})

	t.Run("the whole stylesheet is refused", func(t *testing.T) {
		err := compileModeSheet(t, modeSheet(
			`<xsl:template match=".[starts-with(., ':B:')]" mode="s"/>`))
		if err == nil || !strings.Contains(err.Error(), "XTSE3430") {
			t.Fatalf("want XTSE3430 for a free-ranging \".\" pattern, got: %v", err)
		}
	})
}

// §19.8.9.3: "The use of the current function within a pattern is supported
// with similar restrictions. In this case the context posture is always
// striding." fn:current() denotes the node the pattern as a whole matches, so
// whether atomising it reads from the stream depends on that node's kind --
// which is the whole of the difference between stream-204 and stream-200.
func TestCurrentFunctionInPattern(t *testing.T) {
	at := patternAt(t)

	// stream-204. The matched node is an element, which can have children,
	// so "$parts = current()" atomises it: absorbing, hence consuming.
	t.Run("current() on an element is free-ranging", func(t *testing.T) {
		free, known := patternIsFreeRanging(
			"part-name[$selected-parts = current()]", at)
		if !known || !free {
			t.Errorf("current() denotes an element here, so atomising it is "+
				"absorbing; got free=%v known=%v", free, known)
		}
	})

	// THE ACCEPTANCE DIRECTION, and the one four valid stylesheets
	// (stream-200 through stream-203) turn on. The matched node is a text
	// node, which cannot have children, so §19.8.1 downgrades the absorption
	// usage to inspection and the predicate is motionless.
	t.Run("current() on a text node stays motionless", func(t *testing.T) {
		free, known := patternIsFreeRanging(
			"part-name/text()[$selected-parts = current()]", at)
		if !known || free {
			t.Errorf("current() denotes a text node here, which has no "+
				"children, so the predicate is motionless; got free=%v "+
				"known=%v", free, known)
		}
	})

	// Outside a pattern the rule needs the context posture of the outermost
	// containing expression, which this analysis does not track, so a call
	// is withheld rather than judged.
	t.Run("outside a pattern the call is withheld", func(t *testing.T) {
		a := &analyzer{ctxPosture: postureStriding, ctxAllowsChildren: true, known: true}
		expr, err := xpath.ParseVersion("current()", nil, 31)
		if err != nil {
			t.Fatalf("parsing: %v", err)
		}
		a.expr(expr)
		if a.known {
			t.Error("fn:current() outside a pattern is not modelled and must " +
				"leave known clear")
		}
	})
}
