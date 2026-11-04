package xslt

// An xsl:try reading accumulators (lane L4).
//
// The tree raises an accumulator's error at its first read, wherever in the
// document the walk fails; the stream raises it only once the observer has
// met it (§18.2.5 asks for the latter: a read fails when the value it reads
// depends on the failing computation). Inside a try the two differ when the
// error comes after a read: the tree catches it, the stream does not. They
// agree when the accumulator can fail only at its start: its initial value
// is computed before anything is read, so a read meets that error in both.
// tryReadsFallible admits the try when every accumulator it reads is such
// a one: an xs:integer whose rules match by names alone and step by an
// integer sum of $value and integer literals (a sum whose digits grow by at
// most one per node cannot reach MaxDigits).
//
// The declarations are read from the module: a plan is only built for a
// module that is the whole stylesheet (streamtrust.go), so they are the ones
// in force.

import (
	"regexp"
	"strings"

	"github.com/knroy/go-xml/v2/xdm"
	"github.com/knroy/go-xml/v2/xpath"
)

var accumLiteralCall = regexp.MustCompile(`accumulator-(?:before|after)\(\s*(?:'([^']*)'|"([^"]*)")\s*\)`)

// accumRulePattern is the text of a pattern of names, steps and unions.
var accumRulePattern = regexp.MustCompile(`^[\w.\-:*/| \t\r\n]+$`)

// tryReadsFallible reports whether el, an xsl:try, reads an accumulator
// whose walk may fail after its start, or one it cannot name.
func tryReadsFallible(el *xdm.Node) bool {
	if !readsAccumulator(el) {
		return false
	}
	var names []string
	ok := true
	scan := func(s string) {
		if !strings.Contains(s, "accumulator-") {
			return
		}
		ms := accumLiteralCall.FindAllStringSubmatch(s, -1)
		if len(ms) != strings.Count(s, "accumulator-") {
			ok = false
			return
		}
		for _, m := range ms {
			names = append(names, m[1]+m[2])
		}
	}
	for n := range el.Descendants() {
		if n.Kind() == xdm.KindText && strings.Contains(n.Value(), "accumulator-") {
			return true // a text value template
		}
		for a := range n.Attrs() {
			scan(a.Value())
		}
	}
	for a := range el.Attrs() {
		scan(a.Value())
	}
	if !ok {
		return true
	}
	top := documentElementOf(el)
	for _, name := range names {
		if d := accumDeclNamed(top, strings.TrimSpace(name)); d == nil || !accumFailsAtStartOnly(d) {
			return true
		}
	}
	return false
}

// accumDeclNamed returns the one top-level xsl:accumulator of the module
// named name, an NCName, or nil.
func accumDeclNamed(top *xdm.Node, name string) *xdm.Node {
	if !xdm.IsNCName(name) {
		return nil
	}
	var found *xdm.Node
	for ch := range top.Children() {
		if ch.Kind() == xdm.KindElement && isXSL(ch, "accumulator") && strings.TrimSpace(ch.AttrValue("name")) == name {
			if found != nil {
				return nil
			}
			found = ch
		}
	}
	return found
}

// accumFailsAtStartOnly reports whether the accumulator d can fail only in
// its initial value.
func accumFailsAtStartOnly(d *xdm.Node) bool {
	if strings.TrimSpace(d.AttrValue("as")) != "xs:integer" {
		return false
	}
	if uri, ok := d.LookupPrefix("xs"); !ok || uri != xdm.NSXS {
		return false
	}
	for r := range d.Children() {
		switch {
		case r.Kind() == xdm.KindText && strings.TrimSpace(r.Value()) == "",
			r.Kind() == xdm.KindComment, r.Kind() == xdm.KindPI:
			continue
		case r.Kind() != xdm.KindElement || !isXSL(r, "accumulator-rule") || hasContent(r):
			return false
		}
		for a := range r.Attrs() {
			switch n := a.Name(); {
			case n.URI != "":
			case n.Local == "match", n.Local == "select", n.Local == "phase":
			default:
				return false
			}
		}
		if !accumRulePattern.MatchString(r.AttrValue("match")) || r.Attr("", "select") == nil {
			return false
		}
		if e := parseAt(r, r.AttrValue("select")); e == nil || !integerSum(e) {
			return false
		}
	}
	return true
}

// integerSum reports whether e is a sum or difference of $value (an
// xs:integer) and integer literals.
func integerSum(e xpath.Expr) bool {
	switch x := e.(type) {
	case *xpath.Literal:
		return x.Val != nil && x.Val.Type == xdm.TypeInteger
	case *xpath.VarRef:
		return x.Name == xdm.QName{Local: "value"}
	case *xpath.BinaryOp:
		return (x.Op == "+" || x.Op == "-") && integerSum(x.Left) && integerSum(x.Right)
	case *xpath.SequenceExpr:
		return len(x.Items) == 1 && integerSum(x.Items[0])
	}
	return false
}
