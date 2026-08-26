package xpath

import (
	"fmt"
	"strconv"

	"github.com/knroy/go-xml/xdm"
)

// registerAnalyzeString adds fn:analyze-string and fn:generate-id.
//
// Both are XPath 3.0 additions to the core library. fn:generate-id was already
// available to a stylesheet, where XSLT has had it since 1.0; the
// implementation is shared rather than duplicated.
func registerAnalyzeString(l *Library) {
	// fn:analyze-string($input, $pattern[, $flags]) as element(fn:analyze-string-result)
	//
	// The result is an element tree rather than a sequence of strings: it has
	// to distinguish matched from unmatched substrings, and report the
	// captured groups within each match, which a flat sequence cannot.
	l.registerFnSince(XPath30, "analyze-string", []int{2, 3}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The input is xs:string?, and an empty sequence is treated as "".
		in, err := argString(args, 0)
		if err != nil {
			return nil, err
		}
		pattern, err := argStringRequired(args, 1)
		if err != nil {
			return nil, err
		}
		flags := ""
		if len(args) > 2 {
			if flags, err = argFlags(args, 2); err != nil {
				return nil, err
			}
		}
		re, err := compileXPathRegexp(pattern, flags, ctx.Version)
		if err != nil {
			return nil, err
		}
		// A pattern that matches the empty string would divide the input into
		// infinitely many empty matches.
		if re.MatchString("") {
			return nil, fmt.Errorf("FORX0003: pattern matches the empty string")
		}
		return xdm.One(analyzeString(in, re)), nil
	})

	l.registerFnSince(XPath30, "generate-id", []int{0, 1}, func(ctx *Context, args []xdm.Sequence) (xdm.Sequence, error) {
		// The parameter is node()?, so anything that is not a node — an
		// atomic value, a function item, or more than one item — does not
		// match the signature and is XPTY0004.
		if len(args) == 0 {
			n, err := contextNodeArg(ctx)
			if err != nil {
				return nil, err
			}
			return strSeq(GenerateID(n)), nil
		}
		// An empty argument is the zero-length string rather than an error.
		if len(args[0]) == 0 {
			return strSeq(""), nil
		}
		it, err := args[0].Single()
		if err != nil {
			return nil, err
		}
		n, ok := it.(*xdm.Node)
		if !ok {
			return nil, xdm.ErrType(
				"fn:generate-id: expected a node, got %s", it.TypeName())
		}
		return strSeq(GenerateID(n)), nil
	})
}

// GenerateID returns the unique identifier fn:generate-id gives a node.
//
// The spec requires only that it be stable for a node, distinct between
// nodes, and syntactically an XML name — so it is the node's document order
// index with a letter in front, which satisfies all three without a table.
//
// Exported because the XSLT layer has had this function since 1.0 and must
// agree with this one: two spellings of the same identity would let
// generate-id() differ between a stylesheet and an expression over the same
// node.
func GenerateID(it xdm.Item) string {
	n, ok := it.(*xdm.Node)
	if !ok {
		return ""
	}
	return "N" + strconv.Itoa(n.Order())
}

// analyzeString builds the fn:analyze-string-result tree.
func analyzeString(in string, re Regexp) *xdm.Node {
	// The result element and its descendants are in the fn: namespace, and
	// the root carries the declaration that binds it. Without the namespace
	// node the tree serialises with no binding at all, so a comparison
	// against the expected XML sees a different element.
	result := &xdm.Node{
		Kind: xdm.KindElement,
		Name: xdm.QName{Prefix: "fn", URI: xdm.NSFN, Local: "analyze-string-result"},
	}
	result.Namespaces = []*xdm.Node{{
		Kind:   xdm.KindNamespace,
		Name:   xdm.QName{Local: "fn"},
		Value:  xdm.NSFN,
		Parent: result,
	}}
	appendChild := func(parent, child *xdm.Node) {
		child.Parent = parent
		parent.Children = append(parent.Children, child)
	}
	text := func(s string) *xdm.Node {
		return &xdm.Node{Kind: xdm.KindText, Value: s}
	}

	last := 0
	for _, m := range re.FindAllStringSubmatchIndex(in, -1) {
		// Everything between the previous match and this one is a non-match.
		if m[0] > last {
			nm := &xdm.Node{Kind: xdm.KindElement,
				Name: xdm.QName{Prefix: "fn", URI: xdm.NSFN, Local: "non-match"}}
			appendChild(nm, text(in[last:m[0]]))
			appendChild(result, nm)
		}

		match := &xdm.Node{Kind: xdm.KindElement,
			Name: xdm.QName{Prefix: "fn", URI: xdm.NSFN, Local: "match"}}
		buildMatch(match, in, m, appendChild, text)
		appendChild(result, match)
		last = m[1]
	}
	if last < len(in) {
		nm := &xdm.Node{Kind: xdm.KindElement,
			Name: xdm.QName{Prefix: "fn", URI: xdm.NSFN, Local: "non-match"}}
		appendChild(nm, text(in[last:]))
		appendChild(result, nm)
	}
	return result
}

// groupSpan is where one capturing group matched, and its number.
type groupSpan struct{ start, end, nr int }

// buildMatch fills one fn:match element with its groups and the text between
// them.
//
// The groups are laid out in the order their opening parentheses appear, and a
// group that did not participate in the match contributes nothing. Text of the
// match that falls outside every group is emitted directly on fn:match, which
// is why this walks the match span rather than concatenating the groups.
func buildMatch(match *xdm.Node, in string, m []int,
	appendChild func(parent, child *xdm.Node), text func(string) *xdm.Node) {
	var groups []groupSpan
	for g := 1; g*2+1 < len(m); g++ {
		if m[g*2] >= 0 {
			groups = append(groups, groupSpan{m[g*2], m[g*2+1], g})
		}
	}

	pos := m[0]
	emitted := make([]bool, len(groups))
	for {
		// The next group that starts at or after pos, preferring the
		// outermost — the one that starts earliest, and among those the one
		// that ends latest.
		best := -1
		for i, g := range groups {
			if emitted[i] || g.start < pos {
				continue
			}
			if best < 0 || g.start < groups[best].start ||
				(g.start == groups[best].start && g.end > groups[best].end) {
				best = i
			}
		}
		if best < 0 {
			break
		}
		g := groups[best]
		if g.start > pos {
			appendChild(match, text(in[pos:g.start]))
		}
		el := &xdm.Node{Kind: xdm.KindElement,
			Name:  xdm.QName{Prefix: "fn", URI: xdm.NSFN, Local: "group"},
			Attrs: []*xdm.Node{{Kind: xdm.KindAttribute, Name: xdm.QName{Local: "nr"}, Value: strconv.Itoa(g.nr)}},
		}
		el.Attrs[0].Parent = el
		emitted[best] = true
		// Nested groups sit inside this one, so recurse over the sub-slice of
		// groups this one contains.
		fillNested(el, in, g.start, g.end, groups, emitted, appendChild, text)
		appendChild(match, el)
		pos = g.end
	}
	if pos < m[1] {
		appendChild(match, text(in[pos:m[1]]))
	}
}

// fillNested fills a group element with the groups nested inside it.
func fillNested(el *xdm.Node, in string, start, end int,
	groups []groupSpan, emitted []bool,
	appendChild func(parent, child *xdm.Node), text func(string) *xdm.Node) {
	pos := start
	for {
		best := -1
		for i, g := range groups {
			if emitted[i] || g.start < pos || g.end > end {
				continue
			}
			if best < 0 || g.start < groups[best].start ||
				(g.start == groups[best].start && g.end > groups[best].end) {
				best = i
			}
		}
		if best < 0 {
			break
		}
		g := groups[best]
		if g.start > pos {
			appendChild(el, text(in[pos:g.start]))
		}
		inner := &xdm.Node{Kind: xdm.KindElement,
			Name:  xdm.QName{Prefix: "fn", URI: xdm.NSFN, Local: "group"},
			Attrs: []*xdm.Node{{Kind: xdm.KindAttribute, Name: xdm.QName{Local: "nr"}, Value: strconv.Itoa(g.nr)}},
		}
		inner.Attrs[0].Parent = inner
		emitted[best] = true
		fillNested(inner, in, g.start, g.end, groups, emitted, appendChild, text)
		appendChild(el, inner)
		pos = g.end
	}
	if pos < end {
		appendChild(el, text(in[pos:end]))
	}
}
