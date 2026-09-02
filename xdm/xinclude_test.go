package xdm

import (
	"fmt"
	"strings"
	"testing"
)

// mapResolver serves inclusions from an in-memory map, so that the semantics
// of the pass can be tested without a filesystem. It resolves a relative href
// against the base by the same rules as the real one — enough of them, at
// least, that "dir/a.xml" including "b.xml" finds "dir/b.xml".
type mapResolver struct {
	files map[string]string
	// reads records every resolved URI, so a test can assert that a resource
	// was read once rather than once per reference.
	reads []string
}

func (m *mapResolver) ResolveInclude(href, base, encoding string) ([]byte, string, error) {
	uri := href
	if !strings.HasPrefix(href, "mem:") {
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			uri = base[:i+1] + href
		}
	}
	// Normalise one level of "../" so a nested include can point back up.
	for {
		i := strings.Index(uri, "/../")
		if i < 0 {
			break
		}
		j := strings.LastIndexByte(uri[:i], '/')
		if j < 0 {
			break
		}
		uri = uri[:j] + uri[i+3:]
	}
	m.reads = append(m.reads, uri)
	s, ok := m.files[uri]
	if !ok {
		return nil, "", fmt.Errorf("no such resource %q", uri)
	}
	return []byte(s), uri, nil
}

// parseWithBase parses a document as if it had been retrieved from uri.
func parseWithBase(t *testing.T, uri, src string) *Tree {
	t.Helper()
	tree, err := ParseString(src, ParseOptions{BaseURI: uri, DocumentURI: uri})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return tree
}

func run(t *testing.T, uri, src string, files map[string]string) (*Tree, *mapResolver, error) {
	t.Helper()
	tree := parseWithBase(t, uri, src)
	res := &mapResolver{files: files}
	err := ProcessXInclude(tree, XIncludeOptions{Resolver: res})
	return tree, res, err
}

// serialize renders an element subtree in a form the assertions can compare
// against. It is deliberately minimal — this is checking structure, not the
// serialiser.
func serialize(n *Node) string {
	var sb strings.Builder
	var walk func(*Node)
	walk = func(n *Node) {
		switch n.Kind {
		case KindDocument:
			for _, c := range n.Children {
				walk(c)
			}
		case KindElement:
			sb.WriteByte('<')
			sb.WriteString(n.Name.Local)
			for _, a := range n.Attrs {
				sb.WriteByte(' ')
				if a.Name.Prefix != "" {
					sb.WriteString(a.Name.Prefix)
					sb.WriteByte(':')
				}
				sb.WriteString(a.Name.Local)
				sb.WriteString(`="`)
				sb.WriteString(a.Value)
				sb.WriteByte('"')
			}
			if len(n.Children) == 0 {
				sb.WriteString("/>")
				return
			}
			sb.WriteByte('>')
			for _, c := range n.Children {
				walk(c)
			}
			sb.WriteString("</")
			sb.WriteString(n.Name.Local)
			sb.WriteByte('>')
		case KindText:
			sb.WriteString(n.Value)
		case KindComment:
			sb.WriteString("<!--" + n.Value + "-->")
		}
	}
	walk(n)
	return sb.String()
}

const xiNS = ` xmlns:xi="http://www.w3.org/2001/XInclude"`

// The common case: an href-only inclusion contributes the included document's
// element in place of the xi:include element (XInclude 1.0 §4.5.1).
func TestXIncludePlainXML(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="frag.xml"/></root>`,
		map[string]string{"mem:///doc/frag.xml": `<frag>hello</frag>`})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	// The xml:base is the section 4.5.5 fixup and is expected: the fragment
	// came from a different URI than the include element, so its base has to
	// be recorded or a relative reference inside it would resolve wrongly.
	if got, want := serialize(tree.Root), `<root><frag xml:base="mem:///doc/frag.xml">hello</frag></root>`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// §4.4: parse="text" contributes the resource as characters, not as markup, so
// what looks like an element in the resource stays text.
func TestXIncludeTextIsNotParsed(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="code.txt" parse="text"/></root>`,
		map[string]string{"mem:///doc/code.txt": `a < b && <notanelement/>`})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	kids := tree.Root.Children[0].Children
	if len(kids) != 1 || kids[0].Kind != KindText {
		t.Fatalf("want one text child, got %d children", len(kids))
	}
	if got, want := kids[0].Value, `a < b && <notanelement/>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A text inclusion between two text runs must leave ONE text node, since the
// data model forbids adjacent text siblings (XDM §6.1).
func TestXIncludeTextMergesWithNeighbours(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`>before<xi:include href="t.txt" parse="text"/>after</root>`,
		map[string]string{"mem:///doc/t.txt": `MID`})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	kids := tree.Root.Children[0].Children
	if len(kids) != 1 {
		t.Fatalf("want the three text runs merged into one node, got %d", len(kids))
	}
	if got, want := kids[0].Value, "beforeMIDafter"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// §4.3: a resource that cannot be fetched is recovered from by using the
// fallback rather than reported.
func TestXIncludeFallbackOnMissingResource(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="gone.xml"><xi:fallback><oops/></xi:fallback></xi:include></root>`,
		map[string]string{})
	if err != nil {
		t.Fatalf("a fallback should have recovered the failure: %v", err)
	}
	if got, want := serialize(tree.Root), `<root><oops/></root>`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// An empty fallback is a fallback: §3.2 permits one with no content, and it
// means "include nothing", which is different from having none at all.
func TestXIncludeEmptyFallbackIncludesNothing(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><a/><xi:include href="gone.xml"><xi:fallback/></xi:include><b/></root>`,
		map[string]string{})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	if got, want := serialize(tree.Root), `<root><a/><b/></root>`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// §4.3: "if the fallback element is absent, it is a fatal error."
func TestXIncludeMissingResourceWithNoFallbackIsFatal(t *testing.T) {
	_, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="gone.xml"/></root>`,
		map[string]string{})
	if err == nil {
		t.Fatal("want a fatal error for an unfetchable resource with no fallback")
	}
	if !strings.Contains(err.Error(), "no xi:fallback") {
		t.Errorf("error should say the fallback is what is missing: %v", err)
	}
}

// §4.5.5: an included element whose base differs from the include element's
// gets an xml:base recording where it came from, so that a relative reference
// written inside it still resolves.
func TestXIncludeBaseURIFixup(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="sub/frag.xml"/></root>`,
		map[string]string{"mem:///doc/sub/frag.xml": `<frag><img src="pic.png"/></frag>`})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	frag := tree.Root.Children[0].Children[0]
	xb := frag.Attr(NSXML, "base")
	if xb == nil {
		t.Fatal("the included element should carry an xml:base")
	}
	if got, want := xb.Value, "mem:///doc/sub/frag.xml"; got != want {
		t.Errorf("xml:base = %q, want %q", got, want)
	}
	if got, want := frag.BaseURI, "mem:///doc/sub/frag.xml"; got != want {
		t.Errorf("BaseURI = %q, want %q", got, want)
	}
	// The descendant inherits it, which is the point of adding the attribute.
	if got := elementBase(frag.Children[0]); got != "mem:///doc/sub/frag.xml" {
		t.Errorf("descendant base = %q", got)
	}
}

// An inclusion from the SAME directory needs no xml:base: the value it would
// carry is the one it inherits, and a redundant attribute changes the document
// for no gain.
func TestXIncludeNoRedundantBaseAttribute(t *testing.T) {
	// The include element's base is the document's, and the fragment's URI
	// differs from it — a fixup IS due here, per the spec's rule that the
	// bases differ. The case that must NOT get one is an inclusion whose
	// resolved base equals the include element's, which only arises when the
	// include names the document it already sits in; that is a cycle, so the
	// realistic check is that the attribute value is the resource's URI
	// rather than absent.
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="frag.xml"/></root>`,
		map[string]string{"mem:///doc/frag.xml": `<frag/>`})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	frag := tree.Root.Children[0].Children[0]
	if xb := frag.Attr(NSXML, "base"); xb == nil || xb.Value != "mem:///doc/frag.xml" {
		t.Errorf("want xml:base=mem:///doc/frag.xml, got %v", xb)
	}
}

// An included element that already states an xml:base keeps it, and the base
// is recomputed against where the element now lives. This is base-uri-052's
// fifth assertion, and the behaviour Xerces has.
func TestXIncludeExistingXMLBaseIsKeptAndRebased(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="dir/frag.xml"/></root>`,
		map[string]string{"mem:///doc/dir/frag.xml": `<frag xml:base="other/x.xml"/>`})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	frag := tree.Root.Children[0].Children[0]
	xb := frag.Attr(NSXML, "base")
	if xb == nil || xb.Value != "other/x.xml" {
		t.Fatalf("the document's own xml:base must survive, got %v", xb)
	}
	// Resolved against the INCLUDE element's base (mem:///doc/), not against
	// the included document's (mem:///doc/dir/).
	if got, want := frag.BaseURI, "mem:///doc/other/x.xml"; got != want {
		t.Errorf("BaseURI = %q, want %q", got, want)
	}
}

// §4.5: an inclusion inside an included document is processed too, and its
// href resolves against ITS document rather than against the outermost one.
func TestXIncludeNested(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="dir/a.xml"/></root>`,
		map[string]string{
			// b.xml is named relatively and lives beside a.xml, so resolving
			// against the outer document would look in the wrong directory.
			"mem:///doc/dir/a.xml": `<a` + xiNS + `><xi:include href="b.xml"/></a>`,
			"mem:///doc/dir/b.xml": `<b>deep</b>`,
		})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	if got := serialize(tree.Root); !strings.Contains(got, ">deep</b>") {
		t.Errorf("the nested inclusion did not happen: %s", got)
	}
}

// §4.5: "an inclusion loop ... is a fatal error". Without the check this
// recurses until the stack or the fetch bound stops it, and neither reports
// what actually went wrong.
func TestXIncludeCycleIsDetected(t *testing.T) {
	_, res, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="a.xml"/></root>`,
		map[string]string{
			"mem:///doc/a.xml": `<a` + xiNS + `><xi:include href="b.xml"/></a>`,
			"mem:///doc/b.xml": `<b` + xiNS + `><xi:include href="a.xml"/></b>`,
		})
	if err == nil {
		t.Fatal("want a fatal error for an inclusion loop")
	}
	if !strings.Contains(err.Error(), "loop") {
		t.Errorf("error should name the loop: %v", err)
	}
	// It must stop promptly rather than after the fetch bound: a cycle of two
	// costs three reads before it repeats.
	if len(res.reads) > 5 {
		t.Errorf("the loop ran %d times before being caught", len(res.reads))
	}
}

// A document that includes itself is the degenerate loop, and the including
// document is on the stack from the start so it is caught the same way.
func TestXIncludeSelfReferenceIsALoop(t *testing.T) {
	_, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="main.xml"/></root>`,
		map[string]string{"mem:///doc/main.xml": `<root/>`})
	if err == nil || !strings.Contains(err.Error(), "loop") {
		t.Fatalf("want a loop error, got %v", err)
	}
}

// §4.1.1: href must not carry a fragment identifier; the xpointer attribute
// is where a subresource is named. It is a fatal error rather than something
// to fall back from, so a fallback must not rescue it.
func TestXIncludeFragmentInHrefIsFatalDespiteFallback(t *testing.T) {
	_, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="frag.xml#bit"><xi:fallback><no/></xi:fallback></xi:include></root>`,
		map[string]string{"mem:///doc/frag.xml": `<frag/>`})
	if err == nil {
		t.Fatal("want a fatal error for a fragment identifier in href")
	}
	if !strings.Contains(err.Error(), "fragment identifier") {
		t.Errorf("error should name the fragment: %v", err)
	}
}

// §3.1: parse must be "xml" or "text"; anything else is a fatal error rather
// than a fallback condition, because it is the processor's instruction.
func TestXIncludeBadParseValueIsFatal(t *testing.T) {
	_, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="frag.xml" parse="json"/></root>`,
		map[string]string{"mem:///doc/frag.xml": `<frag/>`})
	if err == nil || !strings.Contains(err.Error(), `parse="json"`) {
		t.Fatalf("want a fatal error naming the bad parse value, got %v", err)
	}
}

// §3.2: xi:fallback is meaningful only as a child of xi:include.
func TestXIncludeStrayFallbackIsFatal(t *testing.T) {
	_, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:fallback/></root>`, map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "outside xi:include") {
		t.Fatalf("want a fatal error for a stray fallback, got %v", err)
	}
}

// §3.2 permits at most one fallback.
func TestXIncludeTwoFallbacksIsFatal(t *testing.T) {
	_, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="gone.xml"><xi:fallback/><xi:fallback/></xi:include></root>`,
		map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("want a fatal error for two fallbacks, got %v", err)
	}
}

// A fallback may itself include, and that inclusion is processed. §3.2 calls
// the fallback "an inclusion which is used when the original inclusion fails".
func TestXIncludeFallbackContainingAnInclude(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="gone.xml"><xi:fallback><xi:include href="alt.xml"/></xi:fallback></xi:include></root>`,
		map[string]string{"mem:///doc/alt.xml": `<alt/>`})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	if got := serialize(tree.Root); !strings.Contains(got, "<alt") {
		t.Errorf("the fallback's own inclusion did not happen: %s", got)
	}
}

// A shorthand xpointer names an element by ID (XPointer Framework §3.2), which
// is the form DocBook's own corpus uses.
func TestXIncludeShorthandXPointer(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="frag.xml" xpointer="p2"/></root>`,
		map[string]string{"mem:///doc/frag.xml": `<frag><p xml:id="p1">one</p><p xml:id="p2">two</p></frag>`})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	if got := serialize(tree.Root.Children[0].Children[0]); !strings.Contains(got, "two") {
		t.Errorf("want the p2 element, got %s", got)
	}
	if strings.Contains(serialize(tree.Root), "one") {
		t.Error("only the addressed element should be included")
	}
}

// The element() scheme addresses by child sequence.
func TestXIncludeElementSchemeXPointer(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="frag.xml" xpointer="element(/1/2)"/></root>`,
		map[string]string{"mem:///doc/frag.xml": `<frag><p>one</p><p>two</p></frag>`})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	if got := serialize(tree.Root); got != `<root><p xml:base="mem:///doc/frag.xml">two</p></root>` {
		t.Errorf("got %s", got)
	}
}

// An xpointer that identifies nothing is an error §4.4 lets a fallback
// recover, so the fallback must be reached.
func TestXIncludeUnresolvedXPointerFallsBack(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="frag.xml" xpointer="nope"><xi:fallback><alt/></xi:fallback></xi:include></root>`,
		map[string]string{"mem:///doc/frag.xml": `<frag/>`})
	if err != nil {
		t.Fatalf("the fallback should have recovered: %v", err)
	}
	if got := serialize(tree.Root); !strings.Contains(got, "<alt") {
		t.Errorf("got %s", got)
	}
}

// A nil resolver refuses everything rather than doing nothing: the include
// still fails, and still falls back or is fatal.
func TestXIncludeNilResolverRefuses(t *testing.T) {
	tree := parseWithBase(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="frag.xml"/></root>`)
	if err := ProcessXInclude(tree, XIncludeOptions{}); err == nil {
		t.Fatal("a nil resolver must refuse the inclusion, not skip it")
	}
}

// A document with no xi:include is left exactly as it was.
func TestXIncludeLeavesAnOrdinaryDocumentAlone(t *testing.T) {
	src := `<root><a>x</a><b/></root>`
	tree, res, err := run(t, "mem:///doc/main.xml", src, map[string]string{})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	if got := serialize(tree.Root); got != src {
		t.Errorf("got %s, want %s", got, src)
	}
	if len(res.reads) != 0 {
		t.Errorf("nothing should have been read, got %v", res.reads)
	}
}

// A fan-out of distinct resources repeats no URI, so the cycle check does not
// bound it; the fetch count does.
func TestXIncludeFetchCountIsBounded(t *testing.T) {
	files := map[string]string{}
	var sb strings.Builder
	sb.WriteString(`<root` + xiNS + `>`)
	for i := 0; i < maxIncludeFetches+10; i++ {
		name := fmt.Sprintf("f%d.xml", i)
		files["mem:///doc/"+name] = `<f/>`
		fmt.Fprintf(&sb, `<xi:include href=%q/>`, name)
	}
	sb.WriteString(`</root>`)
	_, res, err := run(t, "mem:///doc/main.xml", sb.String(), files)
	if err == nil {
		t.Fatal("want a refusal once the fetch bound is reached")
	}
	if len(res.reads) > maxIncludeFetches+1 {
		t.Errorf("read %d resources past a bound of %d", len(res.reads), maxIncludeFetches)
	}
}

// Document order must be correct over the merged content: the tree is
// re-finalised, so a node from an included document compares after one that
// precedes the include element.
func TestXIncludeDocumentOrderIsReassigned(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><a/><xi:include href="frag.xml"/><z/></root>`,
		map[string]string{"mem:///doc/frag.xml": `<m/>`})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	kids := tree.Root.Children[0].Children
	if len(kids) != 3 {
		t.Fatalf("want three children, got %d", len(kids))
	}
	if kids[0].Compare(kids[1]) >= 0 || kids[1].Compare(kids[2]) >= 0 {
		t.Errorf("document order is wrong across the inclusion: %d %d %d",
			kids[0].Order(), kids[1].Order(), kids[2].Order())
	}
	// Every node must belong to the including tree now, not to the parsed
	// fragment's — otherwise Compare falls back to the cross-tree rule and
	// the answer above would be accidental.
	if kids[1].Tree() != tree {
		t.Error("the included node still belongs to the fragment's tree")
	}
}

// An href-less include with an xpointer addresses a subresource of the
// INCLUDING document. That is not a loop — section 4.5's rule is about
// including a document in itself — and it must not read the file again: the
// selection has to see this tree, inclusions and all.
func TestXIncludeHreflessXPointerSelectsLocally(t *testing.T) {
	tree, res, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><p xml:id="src">quoted</p><q><xi:include xpointer="src"/></q></root>`,
		map[string]string{})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	if len(res.reads) != 0 {
		t.Errorf("a local selection must read nothing, got %v", res.reads)
	}
	q := tree.Root.Children[0].Children[1]
	if got := serialize(q); !strings.Contains(got, "quoted") {
		t.Errorf("the local selection did not happen: %s", got)
	}
	// A COPY, not a move: the original must still be where it was.
	if got := serialize(tree.Root.Children[0].Children[0]); !strings.Contains(got, "quoted") {
		t.Errorf("the selected node was moved rather than copied: %s", got)
	}
}

// A local selection that names the include element's own ancestor would embed
// the include element in its own replacement, which is a loop by another name.
func TestXIncludeHreflessXPointerRefusesAncestor(t *testing.T) {
	_, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><outer xml:id="o"><xi:include xpointer="o"/></outer></root>`,
		map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "ancestor") {
		t.Fatalf("want a refusal for selecting an ancestor, got %v", err)
	}
}

// An href-less include with NO xpointer asks for the whole including document,
// which IS the loop section 4.5 forbids.
func TestXIncludeHreflessWithoutXPointerIsALoop(t *testing.T) {
	_, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include/></root>`,
		map[string]string{"mem:///doc/main.xml": `<root/>`})
	if err == nil || !strings.Contains(err.Error(), "loop") {
		t.Fatalf("want a loop error, got %v", err)
	}
}

// A local selection that finds nothing is an error a fallback may recover.
func TestXIncludeHreflessXPointerFallsBack(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include xpointer="nothere"><xi:fallback><alt/></xi:fallback></xi:include></root>`,
		map[string]string{})
	if err != nil {
		t.Fatalf("the fallback should have recovered: %v", err)
	}
	if got := serialize(tree.Root); !strings.Contains(got, "<alt") {
		t.Errorf("got %s", got)
	}
}

// An href-less parse="text" include asks for the including document as
// characters. That is a real read rather than a selection, and it cannot
// recurse, so the loop rule does not apply to it.
func TestXIncludeHreflessTextReadsTheDocument(t *testing.T) {
	tree, res, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include parse="text"/></root>`,
		map[string]string{"mem:///doc/main.xml": "SELF"})
	if err != nil {
		t.Fatalf("ProcessXInclude: %v", err)
	}
	if len(res.reads) != 1 {
		t.Errorf("want one read, got %v", res.reads)
	}
	if got := serialize(tree.Root); got != "<root>SELF</root>" {
		t.Errorf("got %s", got)
	}
}

// An xpointer naming a scheme this package does not implement is not silently
// ignored: with nothing else to try it identifies nothing, which section 4.4
// makes a fallback condition rather than a success. DocBook's corpus writes
// xpath() pointers, which are a Saxon extension rather than XInclude, so this
// is the behaviour they meet.
func TestXIncludeUnsupportedSchemeIsAnError(t *testing.T) {
	_, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="frag.xml" xpointer="xpath(//p)"/></root>`,
		map[string]string{"mem:///doc/frag.xml": `<frag><p/></frag>`})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("want a refusal naming the scheme, got %v", err)
	}
}

// A scheme sequence falls through to the next part when the first is not
// supported — XPointer Framework section 3.3.
func TestXIncludeSchemeSequenceFallsThrough(t *testing.T) {
	tree, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="frag.xml" xpointer="xpath(//p) element(/1/1)"/></root>`,
		map[string]string{"mem:///doc/frag.xml": `<frag><p>found</p></frag>`})
	if err != nil {
		t.Fatalf("the element() part should have been tried: %v", err)
	}
	if got := serialize(tree.Root); !strings.Contains(got, "found") {
		t.Errorf("got %s", got)
	}
}

// A loop is fatal by section 4.5, so a fallback must not launder it into a
// successful transform. The fallback exists for a resource that "cannot be
// fetched" — a condition of the world — not for a defect in the document.
func TestXIncludeFallbackDoesNotRescueALoop(t *testing.T) {
	_, _, err := run(t, "mem:///doc/main.xml",
		`<root`+xiNS+`><xi:include href="a.xml"/></root>`,
		map[string]string{
			"mem:///doc/a.xml": `<a` + xiNS + `><xi:include href="a.xml">` +
				`<xi:fallback><rescued/></xi:fallback></xi:include></a>`,
		})
	if err == nil {
		t.Fatal("a fallback must not rescue an inclusion loop")
	}
	if !strings.Contains(err.Error(), "loop") {
		t.Errorf("error should still name the loop: %v", err)
	}
}

// The fetch bound is this processor refusing to spend more, not a property of
// any one resource, so a fallback must not be a way to keep asking.
func TestXIncludeFallbackDoesNotRescueTheFetchBound(t *testing.T) {
	files := map[string]string{}
	var sb strings.Builder
	sb.WriteString(`<root` + xiNS + `>`)
	for i := 0; i < maxIncludeFetches+10; i++ {
		name := fmt.Sprintf("f%d.xml", i)
		files["mem:///doc/"+name] = `<f/>`
		fmt.Fprintf(&sb, `<xi:include href=%q><xi:fallback/></xi:include>`, name)
	}
	sb.WriteString(`</root>`)
	_, _, err := run(t, "mem:///doc/main.xml", sb.String(), files)
	if err == nil {
		t.Fatal("a fallback must not carry the document past the fetch bound")
	}
}
