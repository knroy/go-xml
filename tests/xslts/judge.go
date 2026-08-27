package xslts

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xsd"
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
					BaseURI:   res.Secondary[i].BaseURI,
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

func (r *Runner) judge(a Assertion, res *xslt.Result, terr error, set *TestSet, schema *xsd.Schema) (bool, string) {
	res, redirected, wasRedirected := principalOf(res)
	// The result tree is built once, here, and handed to every assertion
	// underneath. Result.Tree re-parents the result's nodes into the fresh
	// document root it returns, so building it per assertion would steal them
	// out of the tree the previous assertion was evaluated against: an all-of
	// with several assert children saw every rooted path after the first
	// return nothing.
	return r.judgeIn(a, res, treeOf(res), redirected, wasRedirected, terr, set, schema)
}

// treeOf builds the document node an XPath assertion is evaluated against.
//
// This is Result.Tree with two adjustments the suite's assertions need, and a
// nil result tolerated because a failed transform has none and the assertions
// that read one check the error first.
//
// A result item may itself be a document node: xsl:copy applied to the root,
// which is what an identity transform does, copies the document node rather
// than the element under it. Appending that to a fresh root nests a document
// inside a document, and "/resource" then looks for an element among the
// root's children and finds a document node instead — every rooted path in
// such a test returned the empty sequence against a result that was correct.
// Splicing the children of a document item in puts the element where the
// assertion looks for it.
func treeOf(res *xslt.Result) *xdm.Node {
	if res == nil {
		return nil
	}
	// Result.Tree is used unchanged unless a nested document node forces the
	// rebuild below. Re-parenting is not free: the namespace axis is computed
	// from a node's ancestors when it is walked, so a node moved under a
	// synthetic root reports a different set of in-scope namespaces than the
	// one the engine gave it. element-0306 counts exactly that, and rebuilding
	// unconditionally cost it one namespace node.
	if !needsRebuild(res) {
		return res.Tree()
	}
	tree := xdm.NewTree()
	for _, it := range res.Nodes {
		switch v := it.(type) {
		case *xdm.Node:
			spliceInto(tree.Root, v)
		case *xdm.Atomic:
			tree.Root.AppendChild(&xdm.Node{
				Kind:  xdm.KindText,
				Value: v.String(),
			})
		}
	}
	tree.Finalize()
	return tree.Root
}

// judgeIn is judge once the redirection has been resolved. The two are split
// because the assertion tree is always rooted in an implicit all-of: resolving
// the redirection inside the recursion would do it again on the substituted
// result, whose principal tree is no longer empty, and every nested
// assert-serialization would then read a re-serialisation with default output
// settings instead of the text xsl:result-document actually produced.
func (r *Runner) judgeIn(a Assertion, res *xslt.Result, root *xdm.Node, redirected string, wasRedirected bool, terr error, set *TestSet, schema *xsd.Schema) (bool, string) {
	// A nil result with no error should not happen; treating it as a failure
	// rather than dereferencing it keeps a harness bug from looking like an
	// engine crash.
	if res == nil && terr == nil {
		terr = fmt.Errorf("the transform returned neither a result nor an error")
	}

	switch a.Kind {
	case "all-of":
		for _, c := range a.Children {
			if ok, why := r.judgeIn(c, res, root, redirected, wasRedirected, terr, set, schema); !ok {
				return false, why
			}
		}
		return true, ""

	case "any-of":
		// Each alternative's own reason is carried into the message. Naming
		// only the kinds ("none of error, error, assert held") said nothing
		// about WHY any of them failed, and a test whose result is an any-of
		// was unclassifiable from the dump alone: the reader could not tell
		// an engine bug from a harness one without re-running it by hand.
		var reasons []string
		for _, c := range a.Children {
			ok, why := r.judgeIn(c, res, root, redirected, wasRedirected, terr, set, schema)
			if ok {
				return true, ""
			}
			if why == "" {
				why = "did not hold"
			}
			reasons = append(reasons, c.Kind+": "+why)
		}
		return false, "none of the alternatives held [" +
			strings.Join(reasons, " | ") + "]"

	case "not":
		for _, c := range a.Children {
			if ok, _ := r.judgeIn(c, res, root, redirected, wasRedirected, terr, set, schema); ok {
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
		return evalAssert(res, root, a.Value, a.NS, schema)

	case "assert-string-value":
		if terr != nil {
			return false, "transform failed: " + firstLine(terr.Error())
		}
		got := resultString(res)
		want := a.Value
		// The W3C runner's assert.xsl normalizes both sides whenever the
		// assertion comes from the XSLT catalog namespace, not only when
		// @normalize-space asks for it — see its $isXSLT. Every assertion
		// this harness reads is an XSLT catalog one, so the expected value
		// is written as indented element content and arrives with the
		// surrounding layout attached; comparing that verbatim fails on
		// whitespace the suite never meant as part of the value.
		got, want = normalize(got), normalize(want)
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
		return false, serializationMismatch(got, want)

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
					return false, a.URI + ": " + serializationMismatch(got, want)
				}
				continue
			}
			// The serialised form of *this* result document, not a fresh
			// serialisation under the default settings. Only
			// assert-serialization was special-cased above, so a nested
			// serialization-matches re-serialised the tree with the wrong
			// output definition and compared the wrong bytes.
			if ok, why := r.judgeIn(c, sub, treeOf(sub), serialized, true, nil, set, schema); !ok {
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

	case "assert-warning":
		// The suite always writes this assertion empty: it asserts that the
		// processor raised some warning, never which one. A warning is not
		// required to be raised at all for most recoverable conditions, but
		// where the stylesheet asks for one — xsl:mode/@warning-on-no-match
		// and @warning-on-multiple-match — these cases expect it.
		if terr != nil {
			return false, "transform failed: " + firstLine(terr.Error())
		}
		if len(res.Warnings) == 0 {
			return false, "no warning was raised"
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
	return compareXMLText(xmlMethodString(res), want, normalizeSpace)
}

// xmlMethodString serialises a result as XML regardless of what the
// stylesheet's xsl:output asked for.
//
// assert-xml is a TREE assertion — the catalog writes <assert-xml file="..."/>,
// not assert-serialization — so the stylesheet's output method has no business
// in it. Serialising with the stylesheet's settings let the html method inject
// a content-type meta into <head> that the stylesheet never wrote, and the
// expected files, produced by a driver that serialises the tree as XML, do not
// carry it. The same reasoning is already written out at the wasRedirected
// branch above; it was only ever applied to the redirected path.
//
// It also keeps the comparison well-formed. The html method writes <meta>
// unclosed, which the XML parser on the "got" side cannot read, so the
// structural comparison silently degraded to a text one.
func xmlMethodString(res *xslt.Result) string {
	return xslt.SerializeAsXML(res)
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
			if !sameExpandedName(x.Name, y.Name) || !attrsEqual(x, y) {
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
			if !sameExpandedName(x.Name, y.Name) || x.Value != y.Value {
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

// sameExpandedName compares two names by namespace URI and local part only.
//
// xdm.QName carries the prefix, and comparing the struct compares that too.
// A prefix is not part of a name in the data model: when two prefixes are
// bound to one URI the parser keeps whichever declaration it saw last, so two
// serialisations that differ only in the ORDER of their xmlns declarations
// parse into names that compare unequal, and assert-xml reports a mismatch
// that does not exist. This relaxes the comparison on BOTH sides equally --
// it does not normalise the actual output to look like the expected one.
func sameExpandedName(a, b xdm.QName) bool {
	return a.URI == b.URI && a.Local == b.Local
}

func attrsEqual(a, b *xdm.Node) bool {
	if len(a.Attrs) != len(b.Attrs) {
		return false
	}
	// Attribute order is not significant, so each is looked up by name.
	for _, x := range a.Attrs {
		found := false
		for _, y := range b.Attrs {
			if sameExpandedName(x.Name, y.Name) && x.Value == y.Value {
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

// spliceInto appends n under parent, replacing every document node it meets
// by that node's children.
//
// The data model has no document node below the root: XDM says a document
// node inserted into a tree contributes its children, not itself. The engine
// builds one wherever xsl:document or an xsl:variable holding a document
// appears, and those survive into the result at any depth — xsl-document-0102
// puts one directly under <out>, so "/out/node()[1]" saw a single document
// node where the assertion expects the comment inside it.
//
// An element containing one is rebuilt rather than relinked, because the
// splice changes its child list and the result's own nodes must not be
// mutated: the failure message serialises them afterwards, and a shared
// subtree would be reported with the harness's edit in it. An element with no
// document node anywhere beneath it is passed through untouched, which is the
// overwhelmingly common case.
func spliceInto(parent, n *xdm.Node) {
	if n.Kind == xdm.KindDocument {
		for _, c := range n.Children {
			spliceInto(parent, c)
		}
		return
	}
	if !hasDocumentChild(n) {
		parent.AppendChild(n)
		return
	}
	// Every field of the node is carried across, not the handful the
	// splicing itself needs. This copy exists only to give the node a new
	// parent; an assertion evaluated against it must see the same node it
	// would have seen otherwise. Copying a chosen subset dropped the base
	// URI and the type annotation — and the namespace count in element-0306
	// went from three to two because the node was no longer the one the
	// engine built.
	copied := &xdm.Node{
		Kind:           n.Kind,
		Name:           n.Name,
		Value:          n.Value,
		Attrs:          n.Attrs,
		Namespaces:     n.Namespaces,
		BaseURI:        n.BaseURI,
		TypeAnnotation: n.TypeAnnotation,
		IsID:           n.IsID,
		IsIDREFS:       n.IsIDREFS,
	}
	parent.AppendChild(copied)
	for _, c := range n.Children {
		spliceInto(copied, c)
	}
}

// needsRebuild reports whether the result contains a document node in a
// position that Result.Tree would leave unreachable to a rooted path.
//
// A result item may itself be a document node — xsl:copy applied to the root,
// which is what an identity transform does, copies the document node rather
// than the element under it — and Result.Tree appends that to a fresh root,
// nesting a document inside a document. "/resource" then looks for an element
// among the root's children, finds a document node, and returns nothing
// against a result that was perfectly correct.
//
// Only that case is worth rebuilding for. Everything else keeps the engine's
// own tree, because rebuilding changes the namespace axis.
func needsRebuild(res *xslt.Result) bool {
	for _, it := range res.Nodes {
		n, ok := it.(*xdm.Node)
		if !ok {
			continue
		}
		if n.Kind == xdm.KindDocument || hasDocumentChild(n) {
			return true
		}
	}
	return false
}

// hasDocumentChild reports whether a document node appears anywhere below n.
func hasDocumentChild(n *xdm.Node) bool {
	for _, c := range n.Children {
		if c.Kind == xdm.KindDocument || hasDocumentChild(c) {
			return true
		}
	}
	return false
}

// schemaNS is mapNS carrying the environment's schema into the static context.
//
// It exists because the suite's assertions are written in the same static
// context as the stylesheet under test: validation-1601 and its siblings
// assert "(/) instance of document-node(schema-element(Q{...}doc))" about a
// result the stylesheet validated against the schema the environment
// declares. A bare prefix map cannot answer that — xpath.schemaDeclared only
// consults a resolver that implements xpath.SchemaTypes — so every such
// assertion failed with no schema in scope.
//
// The methods mirror xslt's own resolver over the same *xsd.Schema; only the
// four an assertion can reach are answered with anything but a zero value,
// because the assertion language names declarations and types, never casts to
// a schema simple type in a position this harness evaluates.
type schemaNS struct {
	mapNS
	schema *xsd.Schema
}

// LookupSchemaDeclaration implements xpath.SchemaTypes.
func (s schemaNS) LookupSchemaDeclaration(name xdm.QName, attribute bool) bool {
	if s.schema == nil {
		return false
	}
	if attribute {
		_, ok := s.schema.Attributes[name]
		return ok
	}
	_, ok := s.schema.Elements[name]
	return ok
}

// SchemaDeclarationType implements xpath.SchemaTypes.
//
// Only a named type is reported: a declaration using an inline anonymous type
// has no name for the node test to compare an annotation against, and
// inventing one would make the test fail for every node rather than match the
// right ones.
func (s schemaNS) SchemaDeclarationType(name xdm.QName, attribute bool) (string, bool) {
	if s.schema == nil {
		return "", false
	}
	var t xsd.Type
	if attribute {
		d, ok := s.schema.Attributes[name]
		if !ok || d == nil || d.Type == nil {
			return "", false
		}
		t = d.Type
	} else {
		d, ok := s.schema.Elements[name]
		if !ok || d == nil || d.Type == nil {
			return "", false
		}
		t = d.Type
	}
	local := t.TypeName().Local
	if local == "" {
		return "", false
	}
	return local, true
}

// SubstitutionGroupMembers implements xpath.SchemaTypes.
func (s schemaNS) SubstitutionGroupMembers(name xdm.QName) []xdm.QName {
	if s.schema == nil {
		return nil
	}
	head, ok := s.schema.Elements[name]
	if !ok {
		return nil
	}
	members := head.Substitutable()
	if len(members) == 0 {
		return nil
	}
	out := make([]xdm.QName, 0, len(members))
	for _, d := range members {
		out = append(out, xdm.QName{URI: d.Name.URI, Local: d.Name.Local})
	}
	return out
}

// LookupSchemaType implements xpath.SchemaTypes.
//
// A complex type, list or union is reported as known but non-atomic: the name
// resolves, which is what stops XPST0051, but no single primitive describes
// its values.
func (s schemaNS) LookupSchemaType(name xdm.QName) (xdm.TypeCode, bool, bool) {
	if s.schema == nil {
		return 0, false, false
	}
	t, ok := s.schema.Types[name]
	if !ok {
		return 0, false, false
	}
	st, ok := t.(*xsd.SimpleType)
	if !ok || st.Variety != xsd.VarietyAtomic || st.Primitive == nil {
		return 0, false, true
	}
	// The nearest built-in ancestor rather than the XSD primitive: a
	// restriction of xs:integer has xs:decimal as its primitive, so erasing
	// to the primitive would make "instance of xs:integer" false for a value
	// of the derived type.
	for cur := st; cur != nil; {
		if cur.Name.URI == xsd.NSSchema && cur.Name.Local != "" {
			if code, ok := xpath.BuiltinAtomicTypeCode(cur.Name.Local); ok {
				return code, true, true
			}
		}
		base, ok := cur.Base.(*xsd.SimpleType)
		if !ok || base == cur {
			break
		}
		cur = base
	}
	code, ok := xpath.BuiltinAtomicTypeCode(st.Primitive.Name.Local)
	if !ok {
		return 0, false, true
	}
	return code, true, true
}

// ValidateSchemaValue implements xpath.SchemaTypes.
func (s schemaNS) ValidateSchemaValue(name xdm.QName, value string) (bool, error) {
	if s.schema == nil || !s.schema.HasSimpleType(name) {
		return false, nil
	}
	return true, s.schema.ValidateValue(value, name)
}

func evalAssert(res *xslt.Result, root *xdm.Node, expr string, ns map[string]string, schema *xsd.Schema) (bool, string) {
	// The result tree itself, not a re-parse of its serialisation.
	//
	// Serialising and re-parsing asks the XML parser to accept whatever the
	// result's own output method produced, and two shapes of legal result are
	// not XML at all. An html or xhtml method writes <meta> and <img> without
	// a closing tag, so the round trip failed with "element <meta> closed by
	// </head>" for a result the stylesheet built correctly. A result that is
	// bare text, or several top-level nodes, is not a document either, and
	// failed with "character data outside root element".
	//
	// Result.Tree builds the document node directly from the result sequence,
	// which is the tree the assertion is written about. It admits both shapes
	// because it never goes through a serialiser, and it is what the suite's
	// own driver evaluates against.
	if root == nil {
		return false, "the transform produced no result tree"
	}
	ctx := xpath.NewContext(root, xpath.Builtins())
	resolver := xpath.NamespaceResolver(mapNS(ns))
	// The assertion is evaluated in the same static context the stylesheet
	// had, which includes the schema the environment declares. An assertion
	// naming schema-element(E) is XPST0008 without it — and because the
	// XPST0003 fallback in evalAssertExpr reports the 2.0 parser's message
	// rather than the extended parser's, that surfaced as a bogus syntax
	// error rather than as the missing schema it really was.
	if schema != nil {
		resolver = schemaNS{mapNS: mapNS(ns), schema: schema}
	}
	got, evalErr := evalAssertExpr(expr, ctx, resolver)
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

// evalAssertExpr evaluates one assertion expression.
//
// The suite's assertion language is not the language of the stylesheets it
// tests. An assertion may use XPath 3.0 — the braced URI literal Q{uri}local
// appears in attribute-0601, namespace-3301 and xpath-default-namespace-0107,
// and the simple map operator "!" in strip-space-003 and strip-space-005 —
// even where the stylesheet under test is XSLT 2.0 and the engine is right to
// refuse both inside it.
//
// So the 2.0 parser is tried first and keeps its full compile path, including
// the optimiser; only a syntax error falls back to xpath.ParseExtended, which
// adds those two constructs and nothing else. A genuine syntax error in an
// assertion still fails, under the extended parser's message rather than the
// 2.0 one — which names the same offset in the same expression.
func evalAssertExpr(expr string, ctx *xpath.Context, ns xpath.NamespaceResolver) (xdm.Sequence, error) {
	got, err := xpath.Eval(expr, ctx, ns)
	if err == nil || !strings.Contains(err.Error(), "XPST0003") {
		return got, err
	}
	e, perr := xpath.ParseExtended(expr, ns)
	if perr != nil {
		return nil, err
	}
	return e.Eval(ctx)
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
			// BaseURI travels with the nodes: the assertions inside an
			// assert-result-document ask about base-uri(/), and the
			// document node the Result manufactures is the only node that
			// can carry the result document's own URI.
			return &xslt.Result{Nodes: s.Nodes, BaseURI: s.BaseURI}, s.String()
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
	// The string value of the result *sequence*, which is what the suite's
	// own driver computes: it builds a document from the result and asks for
	// its string value. Walking res.Nodes and keeping only the nodes drops
	// every atomic value the stylesheet returned -- an xsl:sequence at the
	// top of a template routinely returns one -- and reported the empty
	// string for a transform that had produced the asserted text.
	return res.Tree().StringValue()
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
		strings.ReplaceAll(decodeExpected(stripBOM(data), a.Encoding), "\r", "")), nil
}

// decodeExpected converts an expected-result file's bytes to the UTF-8 the
// engine's serialisation is compared as.
//
// @encoding on an assert-serialization names the encoding the file was
// written in, and the comparison is defined on characters rather than bytes:
// select-6101 asserts that "&eacute;" serialises as the single byte xE9 under
// ISO-8859-1, and ships its expected result in that encoding. Reading it as
// UTF-8 turned that byte into the replacement character, so the assertion
// compared a valid result against a corrupted expectation.
//
// Only ISO-8859-1 is decoded, because it is the only encoding the suite names
// and because it is the one encoding whose decoding is a rune conversion —
// its code points are exactly U+0000 to U+00FF. Any other name, and the bytes
// are used as they are, which is right for UTF-8 and no worse than the
// previous behaviour for anything else.
func decodeExpected(data []byte, encoding string) string {
	switch strings.ToLower(encoding) {
	case "iso-8859-1", "latin1", "latin-1":
		runes := make([]rune, len(data))
		for i, b := range data {
			runes[i] = rune(b)
		}
		return string(runes)
	}
	return string(data)
}

// serializationMismatch reports an assert-serialization failure in a form that
// says what actually differs.
//
// trunc collapses runs of whitespace, which is right for an XPath expression
// echoed back but wrong here: a serialisation mismatch is very often a
// whitespace mismatch, and collapsing it printed two strings that read as
// byte-identical. expression-2101 differs from its expected result by one
// text node of indentation and was reported as "serialization X, want X",
// which sent one investigation after a line-ending difference that was not
// there — the expected side already has its carriage returns stripped.
//
// So the two are quoted with escapes intact and cut at the first byte that
// differs, which is the one piece of information a reader needs.
func serializationMismatch(got, want string) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	const window = 40
	from := i - window
	if from < 0 {
		from = 0
	}
	clip := func(s string) string {
		to := i + window
		if to > len(s) {
			to = len(s)
		}
		return fmt.Sprintf("%q", s[from:to])
	}
	return fmt.Sprintf("serialization differs at offset %d: got %s, want %s",
		i, clip(got), clip(want))
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
