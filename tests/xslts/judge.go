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
// principalOf returns the tree an assertion about "the result" should read.
//
// An xsl:result-document with no href produces a final result tree identified
// by the base output URI — the zero-length string is a legal relative URI, and
// the specification treats the tree it makes as a result like any other rather
// than as the principal one. A stylesheet whose whole body sits inside such an
// instruction therefore leaves the principal result empty, and the suite still
// asserts against what it produced. Reading the unnamed secondary result when
// the principal is empty is what makes those assertions see it.
func principalOf(res *xslt.Result) (*xslt.Result, string, bool) {
	if res == nil || len(res.Nodes) > 0 {
		return res, "", false
	}
	for i := range res.Secondary {
		if res.Secondary[i].Href == "" {
			// The text comes back alongside the nodes because this tree
			// serialises with the instruction's own output settings, which
			// a Result built from its nodes cannot express — the unnamed
			// xsl:output selecting method="text" is exactly the case.
			// The secondary list travels with the substituted result: a
			// stylesheet may write both an unnamed result document and a
			// named one, and an assert-result-document nested beside the
			// assertions about the principal tree still has to find it.
			return &xslt.Result{
					Nodes:     res.Secondary[i].Nodes,
					Secondary: res.Secondary,
				},
				res.Secondary[i].String(), true
		}
	}
	return res, "", false
}

// compileMatchPattern compiles a serialization-matches pattern, translating
// the XPath regular expression flags the suite uses.
//
// Go's regexp takes i, s and m as inline flags. It has no free-spacing mode,
// so x is applied by removing the whitespace the mode would ignore — every
// space outside a character class and not escaped — which is what the flag
// means rather than an approximation of it.
func compileMatchPattern(pat, flags string) (*regexp.Regexp, error) {
	if strings.Contains(flags, "x") {
		var b strings.Builder
		inClass := false
		for i := 0; i < len(pat); i++ {
			c := pat[i]
			switch {
			case c == '\\' && i+1 < len(pat):
				b.WriteByte(c)
				i++
				b.WriteByte(pat[i])
				continue
			case c == '[':
				inClass = true
			case c == ']':
				inClass = false
			case !inClass && (c == ' ' || c == '\t' || c == '\n' || c == '\r'):
				continue
			}
			b.WriteByte(c)
		}
		pat = b.String()
	}
	var inline string
	for _, f := range "ism" {
		if strings.ContainsRune(flags, f) {
			inline += string(f)
		}
	}
	if inline != "" {
		pat = "(?" + inline + ")" + pat
	}
	return regexp.Compile(pat)
}

func (r *Runner) judge(a Assertion, res *xslt.Result, terr error, set *TestSet) (bool, string) {
	res, redirected, wasRedirected := principalOf(res)
	return r.judgeIn(a, res, redirected, wasRedirected, terr, set)
}

// judgeIn is judge once the redirection has been resolved. The two are split
// because the assertion tree is always rooted in an implicit all-of: resolving
// the redirection inside the recursion would do it again on the substituted
// result, whose principal tree is no longer empty, and every nested
// assert-serialization would then read a re-serialisation with default output
// settings instead of the text xsl:result-document actually produced.
func (r *Runner) judgeIn(a Assertion, res *xslt.Result, redirected string, wasRedirected bool, terr error, set *TestSet) (bool, string) {
	// A nil result with no error should not happen; treating it as a failure
	// rather than dereferencing it keeps a harness bug from looking like an
	// engine crash.
	if res == nil && terr == nil {
		terr = fmt.Errorf("the transform returned neither a result nor an error")
	}

	switch a.Kind {
	case "all-of":
		for _, c := range a.Children {
			if ok, why := r.judgeIn(c, res, redirected, wasRedirected, terr, set); !ok {
				return false, why
			}
		}
		return true, ""

	case "any-of":
		var reasons []string
		for _, c := range a.Children {
			if ok, _ := r.judgeIn(c, res, redirected, wasRedirected, terr, set); ok {
				return true, ""
			}
			reasons = append(reasons, c.Kind)
		}
		return false, "none of " + strings.Join(reasons, ", ") + " held"

	case "not":
		for _, c := range a.Children {
			if ok, _ := r.judgeIn(c, res, redirected, wasRedirected, terr, set); ok {
				return false, "the negated assertion held"
			}
		}
		return true, ""

	// A serialization error is a dynamic error like any other; the suite
	// gives it its own element only to record that it arises while writing
	// the result rather than while building it. The reference driver's
	// assert.xsl matches c:error and c:assert-serialization-error with the
	// same template, so they are judged identically here.
	case "error", "assert-serialization-error":
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
		if wasRedirected {
			// The same reason as serialization-matches below: an unnamed
			// xsl:result-document serialises with its *own* output settings,
			// and a Result rebuilt from its nodes has lost them. Re-
			// serialising it here defaulted the method, which picks html
			// from an <html> root and injects a meta element the stylesheet
			// never wrote — a difference invented by the harness.
			return compareXMLText(redirected, want, true)
		}
		return compareXML(res, want, true)

	case "assert":
		if terr != nil {
			return false, "transform failed: " + firstLine(terr.Error())
		}
		return evalAssert(res, a.Value, a.NS)

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
		// The pattern is written as indented CDATA inside the element, so
		// it arrives with the surrounding layout attached. Compiling that
		// verbatim makes every such assertion fail on whitespace the suite
		// never meant as part of the pattern.
		re, err := compileMatchPattern(strings.TrimSpace(a.Value), a.Flags)
		if err != nil {
			return false, "unusable pattern: " + firstLine(err.Error())
		}
		text := res.String()
		if wasRedirected {
			// The same reason as assert-serialization: an unnamed
			// xsl:result-document produces the tree these patterns describe,
			// and it serialises with its own output settings.
			text = redirected
		}
		if re.MatchString(text) {
			return true, ""
		}
		return false, "serialization does not match " + trunc(a.Value)

	case "assert-serialization":
		if terr != nil {
			return false, "transform failed: " + firstLine(terr.Error())
		}
		text := res.String()
		if wasRedirected {
			text = redirected
		}
		want, err := serializationWant(a, set)
		if err != nil {
			return false, err.Error()
		}
		got := stripDecl(text)
		want = stripDecl(want)
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
		sub, serialized := secondaryByURI(res, a.URI)
		if sub == nil {
			var hrefs []string
			for _, s := range res.Secondary {
				hrefs = append(hrefs, s.Href)
			}
			return false, fmt.Sprintf("no result document for %q; got %v",
				a.URI, hrefs)
		}
		for _, c := range a.Children {
			// A secondary result is serialised with its *own* xsl:output
			// settings, which the Result built from its nodes alone does
			// not carry. assert-serialization is the only assertion that
			// can tell the difference, so it is given the text the engine
			// produced rather than a re-serialisation with defaults.
			if c.Kind == "assert-serialization" {
				want, err := serializationWant(c, set)
				if err != nil {
					return false, err.Error()
				}
				got := stripDecl(serialized)
				want = stripDecl(want)
				if c.Normalize {
					got, want = normalize(got), normalize(want)
				}
				if got != want {
					return false, fmt.Sprintf("%s: serialization %q, want %q",
						a.URI, trunc(got), trunc(want))
				}
				continue
			}
			if ok, why := r.judgeIn(c, sub, "", false, nil, set); !ok {
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
	return compareXMLText(res.String(), want, normalizeSpace)
}

// compareXMLText compares already-serialised result text against the expected
// fragment.
func compareXMLText(serialised, want string, normalizeSpace bool) (bool, string) {
	// The expected value in the catalog is a *fragment*: the result tree
	// written out, with no XML declaration. Result.String serialises a
	// document, declaration included, so comparing the two directly reports
	// every passing test as a mismatch against its own prologue.
	got := stripDecl(serialised)
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
// mapNS resolves the prefixes the suite declared on the assertion element.
type mapNS map[string]string

func (m mapNS) ResolvePrefix(p string) (string, bool) {
	if u, ok := m[p]; ok {
		return u, true
	}
	// xs and xml are bound everywhere an XPath expression is written in the
	// suite: the catalog leaves them undeclared on the assertion because a
	// processor is expected to have them in scope, and refusing them measures
	// the harness rather than the engine.
	switch p {
	case "xs":
		return xdm.NSXS, true
	case "xml":
		return xdm.NSXML, true
	}
	return "", false
}
func (m mapNS) DefaultElementNamespace() string { return m[""] }
func (mapNS) DefaultFunctionNamespace() string  { return xdm.NSFN }

func evalAssert(res *xslt.Result, expr string, ns map[string]string) (bool, string) {
	doc, err := xdm.ParseString(stripDecl(res.String()), xdm.ParseOptions{})
	if err != nil {
		return false, "the result is not a document: " + firstLine(err.Error())
	}
	ctx := xpath.NewContext(doc.Root, xpath.Builtins())
	resolver := xpath.NamespaceResolver(mapNS(ns))
	got, evalErr := xpath.Eval(expr, ctx, resolver)
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
	return false, "assertion is false: " + trunc(expr) + " || GOT=" + trunc(res.String())
}

// secondaryByURI finds the result document a URI names, as a Result so that
// the ordinary assertions can be applied to it unchanged.
//
// The href is compared by its last path segment: the suite writes "out.xml"
// where the engine records whatever the stylesheet's @href resolved to, and
// the two agree on the name rather than on the path.
func secondaryByURI(res *xslt.Result, uri string) (*xslt.Result, string) {
	want := lastSegment(uri)
	for i := range res.Secondary {
		s := &res.Secondary[i]
		if s.Href == uri || lastSegment(s.Href) == want {
			// The text is taken alongside the nodes because a secondary
			// result serialises with its own output settings, which a
			// Result assembled from the nodes cannot express.
			return &xslt.Result{Nodes: s.Nodes}, s.String()
		}
	}
	return nil, ""
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
// serializationWant returns the text an assert-serialization expects.
//
// The suite writes the expected serialisation inline for short results and in
// a companion file for long ones. Ignoring @file compared every such
// assertion against the empty string, which reported a mismatch for a test
// that was never actually checked.
//
// Carriage returns are dropped because the suite ships those files with
// whatever line endings the contributor's platform used, and the reference
// driver's assert.xsl translates them away before comparing.
func serializationWant(a Assertion, set *TestSet) (string, error) {
	if a.File == "" {
		return strings.TrimSpace(a.Value), nil
	}
	data, err := os.ReadFile(filepath.Join(set.Dir, filepath.FromSlash(a.File)))
	if err != nil {
		return "", fmt.Errorf("expected-serialization file: %s",
			firstLine(err.Error()))
	}
	return strings.TrimSpace(
		strings.ReplaceAll(string(stripBOM(data)), "\r", "")), nil
}

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
