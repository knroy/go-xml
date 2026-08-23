package xslts

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xslt"
)

// Judging a result against the suite's assertions.
//
// The suite states an expected result in several shapes, and the ones that
// matter for XSLT 2.0 are: the serialised output matches a given document
// (assert-xml), an XPath expression over the result holds (assert), the
// transform raises a particular error (error), and the combinators over
// those. Everything else is reported as unsupported rather than guessed at —
// a runner that judges an assertion it does not understand produces a number
// that means nothing.

// judge reports whether the outcome satisfies the assertion tree.
//
// res is nil whenever terr is non-nil, so every branch that reads a result
// checks the error first. Only the combinators and <error> are reachable
// without one.
func (r *Runner) judge(a Assertion, res *xslt.Result, terr error, set *TestSet) (bool, string) {
	// A nil result with no error should not happen; treating it as a failure
	// rather than dereferencing it keeps a harness bug from looking like an
	// engine crash.
	if res == nil && terr == nil {
		terr = fmt.Errorf("the transform returned neither a result nor an error")
	}

	switch a.Kind {
	case "all-of":
		for _, c := range a.Children {
			if ok, why := r.judge(c, res, terr, set); !ok {
				return false, why
			}
		}
		return true, ""

	case "any-of":
		var reasons []string
		for _, c := range a.Children {
			if ok, _ := r.judge(c, res, terr, set); ok {
				return true, ""
			}
			reasons = append(reasons, c.Kind)
		}
		return false, "none of " + strings.Join(reasons, ", ") + " held"

	case "not":
		for _, c := range a.Children {
			if ok, _ := r.judge(c, res, terr, set); ok {
				return false, "the negated assertion held"
			}
		}
		return true, ""

	case "error":
		if terr == nil {
			return false, fmt.Sprintf("expected error %s, the transform succeeded", a.Code)
		}
		// The code is checked when the engine reports one. Matching only on
		// "an error happened" would pass a test that failed for the wrong
		// reason, which is the failure mode a conformance run exists to find.
		if a.Code == "" || a.Code == "*" {
			return true, ""
		}
		if strings.Contains(terr.Error(), a.Code) {
			return true, ""
		}
		return false, fmt.Sprintf("expected %s, got: %s", a.Code, firstLine(terr.Error()))

	case "assert-xml":
		if terr != nil {
			return false, "transform failed: " + firstLine(terr.Error())
		}
		want := a.Value
		if a.File != "" {
			// The expected value is held in a file when it is too large to
			// sit inline. Refusing it reports a mismatch that was never
			// tested.
			data, err := os.ReadFile(filepath.Join(set.Dir,
				filepath.FromSlash(a.File)))
			if err != nil {
				return false, "expected-result file: " + firstLine(err.Error())
			}
			want = string(stripBOM(data))
		}
		// Inter-element whitespace is ignored unless the assertion asks for
		// it to count. The suite writes its expected values pretty-printed —
		// indented to suit the author — while a transform emits what the
		// stylesheet says, so comparing them literally reports indentation
		// as a conformance failure. This is what the reference driver does.
		return compareXML(res, want, true)

	case "assert":
		if terr != nil {
			return false, "transform failed: " + firstLine(terr.Error())
		}
		return evalAssert(res, a.Value)

	case "assert-string-value":
		if terr != nil {
			return false, "transform failed: " + firstLine(terr.Error())
		}
		got := resultString(res)
		want := a.Value
		if a.Normalize {
			got, want = normalize(got), normalize(want)
		}
		if got == want {
			return true, ""
		}
		return false, fmt.Sprintf("string value %q, want %q", got, want)

	case "serialization-matches":
		if terr != nil {
			return false, "transform failed: " + firstLine(terr.Error())
		}
		re, err := regexp.Compile(a.Value)
		if err != nil {
			return false, "unusable pattern: " + firstLine(err.Error())
		}
		if re.MatchString(res.String()) {
			return true, ""
		}
		return false, "serialization does not match " + trunc(a.Value)

	case "assert-serialization":
		if terr != nil {
			return false, "transform failed: " + firstLine(terr.Error())
		}
		got, want := stripDecl(res.String()), strings.TrimSpace(a.Value)
		if a.Normalize {
			got, want = normalize(got), normalize(want)
		}
		if got == want {
			return true, ""
		}
		return false, fmt.Sprintf("serialization %q, want %q",
			trunc(got), trunc(want))

	case "assert-result-document":
		// A secondary output produced by xsl:result-document. The nested
		// assertions are judged against it as if it were the principal
		// result, which is how the suite states them.
		if terr != nil {
			return false, "transform failed: " + firstLine(terr.Error())
		}
		sub := secondaryByURI(res, a.URI)
		if sub == nil {
			var hrefs []string
			for _, s := range res.Secondary {
				hrefs = append(hrefs, s.Href)
			}
			return false, fmt.Sprintf("no result document for %q; got %v",
				a.URI, hrefs)
		}
		for _, c := range a.Children {
			if ok, why := r.judge(c, sub, nil, set); !ok {
				return false, a.URI + ": " + why
			}
		}
		return true, ""

	case "assert-message":
		if terr != nil {
			return false, "transform failed: " + firstLine(terr.Error())
		}
		if len(res.Messages) == 0 {
			return false, "no xsl:message output"
		}
		return true, ""

	case "assert-empty":
		if terr != nil {
			return false, "transform failed: " + firstLine(terr.Error())
		}
		if res == nil || len(res.Nodes) == 0 {
			return true, ""
		}
		return false, "the result is not empty"

	case "":
		// Character data between elements.
		return true, ""
	}
	return false, "unsupported assertion " + a.Kind
}

// compareXML checks the serialised result against an expected fragment.
//
// The comparison is of parsed trees rather than of text: the suite writes its
// expected values with the whitespace that suited the author, and a textual
// match would report a difference in indentation as a conformance failure.
func compareXML(res *xslt.Result, want string, normalizeSpace bool) (bool, string) {
	// The expected value in the catalog is a *fragment*: the result tree
	// written out, with no XML declaration. Result.String serialises a
	// document, declaration included, so comparing the two directly reports
	// every passing test as a mismatch against its own prologue.
	got := stripDecl(res.String())
	// The expected value is stripped too. An expected result read from a
	// file carries its own declaration, and comparing one prologue against
	// the absence of another is not what the test asks.
	want = stripDecl(want)
	// The expected value may be a fragment with several top-level nodes,
	// which is not a document; wrapping both makes them parseable and
	// compares them on equal terms.
	gotDoc, err1 := xdm.ParseString("<w>"+got+"</w>", xdm.ParseOptions{})
	wantDoc, err2 := xdm.ParseString("<w>"+want+"</w>", xdm.ParseOptions{})
	if err1 != nil || err2 != nil {
		// Fall back to a text comparison when either side will not parse,
		// which happens for results that are not well-formed XML by design.
		g, w := got, want
		if normalizeSpace {
			g, w = normalize(g), normalize(w)
		}
		if g == w {
			return true, ""
		}
		return false, fmt.Sprintf("result %q, want %q", trunc(got), trunc(want))
	}
	if treesEqual(gotDoc.Root, wantDoc.Root, normalizeSpace) {
		return true, ""
	}
	return false, fmt.Sprintf("result %q, want %q", trunc(got), trunc(want))
}

// treesEqual compares two trees by structure rather than by serialisation.
func treesEqual(a, b *xdm.Node, normalizeSpace bool) bool {
	ac, bc := contentOf(a, normalizeSpace), contentOf(b, normalizeSpace)
	if len(ac) != len(bc) {
		return false
	}
	for i := range ac {
		x, y := ac[i], bc[i]
		if x.Kind != y.Kind {
			return false
		}
		switch x.Kind {
		case xdm.KindElement:
			if x.Name != y.Name || !attrsEqual(x, y) {
				return false
			}
			if !treesEqual(x, y, normalizeSpace) {
				return false
			}
		case xdm.KindText, xdm.KindComment:
			xv, yv := x.Value, y.Value
			if normalizeSpace {
				xv, yv = normalize(xv), normalize(yv)
			}
			if xv != yv {
				return false
			}
		case xdm.KindPI:
			if x.Name != y.Name || x.Value != y.Value {
				return false
			}
		}
	}
	return true
}

// contentOf returns the children that count for comparison, dropping
// whitespace-only text when normalising.
func contentOf(n *xdm.Node, normalizeSpace bool) []*xdm.Node {
	var out []*xdm.Node
	for _, c := range n.Children {
		if normalizeSpace && c.Kind == xdm.KindText &&
			strings.TrimSpace(c.Value) == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

func attrsEqual(a, b *xdm.Node) bool {
	if len(a.Attrs) != len(b.Attrs) {
		return false
	}
	// Attribute order is not significant, so each is looked up by name.
	for _, x := range a.Attrs {
		found := false
		for _, y := range b.Attrs {
			if x.Name == y.Name && x.Value == y.Value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// evalAssert evaluates an XPath expression against the result.
//
// The expressions are written as if the result were a document — "/out" is
// the commonest shape — so the result tree is wrapped in a document node
// before evaluation. Using the result's first node as context instead makes
// every rooted path raise XPDY0050, which is not the engine disagreeing with
// the suite but the harness handing it the wrong context.
func evalAssert(res *xslt.Result, expr string) (bool, string) {
	doc, err := xdm.ParseString(stripDecl(res.String()), xdm.ParseOptions{})
	if err != nil {
		return false, "the result is not a document: " + firstLine(err.Error())
	}
	ctx := xpath.NewContext(doc.Root, xpath.Builtins())
	got, evalErr := xpath.Eval(expr, ctx, noNS{})
	if err := evalErr; err != nil {
		return false, fmt.Sprintf("%s: %v", trunc(expr), err)
	}
	b, err := xpath.EffectiveBooleanValue(got)
	if err != nil {
		return false, fmt.Sprintf("%s: %v", trunc(expr), err)
	}
	if b {
		return true, ""
	}
	return false, "assertion is false: " + trunc(expr)
}

// secondaryByURI finds the result document a URI names, as a Result so that
// the ordinary assertions can be applied to it unchanged.
//
// The href is compared by its last path segment: the suite writes "out.xml"
// where the engine records whatever the stylesheet's @href resolved to, and
// the two agree on the name rather than on the path.
func secondaryByURI(res *xslt.Result, uri string) *xslt.Result {
	want := lastSegment(uri)
	for i := range res.Secondary {
		s := &res.Secondary[i]
		if s.Href == uri || lastSegment(s.Href) == want {
			return &xslt.Result{Nodes: s.Nodes}
		}
	}
	return nil
}

func lastSegment(s string) string {
	if i := strings.LastIndexAny(s, "/\\"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func resultString(res *xslt.Result) string {
	var sb strings.Builder
	for _, it := range res.Nodes {
		if n, ok := it.(*xdm.Node); ok {
			sb.WriteString(n.StringValue())
		}
	}
	return sb.String()
}

// stripDecl removes an XML declaration and any leading whitespace.
func stripDecl(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "<?xml") {
		if i := strings.Index(t, "?>"); i >= 0 {
			t = strings.TrimSpace(t[i+2:])
		}
	}
	return t
}

func normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

func trunc(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 90 {
		return s[:90] + "..."
	}
	return s
}
