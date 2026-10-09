package xdm

import (
	"fmt"
	"math/rand"
	"testing"

	xml "github.com/knroy/go-xml/v2/internal/xmltok"
)

// validateStartElementMaps is validateStartElement as it was before tagScope:
// the in-scope bindings and the tag's own declarations built as two maps per
// start tag. It is kept as the reference the lookup is checked against.
func validateStartElementMaps(t xml.StartElement, parent *Node, xml11 bool) error {
	if err := requireQName(t.Name); err != nil {
		return err
	}
	for _, a := range t.Attr {
		if err := requireQName(a.Name); err != nil {
			return err
		}
	}
	bindings := map[string]string{"xml": NSXML}
	for p := parent; p != nil; p = p.Parent() {
		for _, ns := range nsOf(p) {
			if _, seen := bindings[ns.Name().Local]; !seen {
				bindings[ns.Name().Local] = ns.Value()
			}
		}
	}
	declared := map[string]bool{}
	for _, a := range t.Attr {
		prefix, isDecl := namespaceDecl(a)
		if !isDecl {
			continue
		}
		if declared[prefix] {
			return fmt.Errorf("parse XML: duplicate namespace declaration for prefix %q", prefix)
		}
		declared[prefix] = true
		if err := validateNamespaceBinding(prefix, a.Value, xml11); err != nil {
			return err
		}
		bindings[prefix] = a.Value
	}
	bound := func(prefix string) error {
		if prefix == "" {
			return nil
		}
		if uri, ok := bindings[prefix]; !ok || uri == "" {
			return fmt.Errorf("parse XML: no namespace declaration is in scope for prefix %q", prefix)
		}
		return nil
	}
	if err := bound(t.Name.Space); err != nil {
		return err
	}
	if t.Name.Space == "xmlns" {
		return fmt.Errorf("parse XML: xmlns prefix cannot be used in an element name")
	}
	seen := map[expandedName]bool{}
	for _, a := range t.Attr {
		if _, declaration := namespaceDecl(a); declaration {
			continue
		}
		if err := bound(a.Name.Space); err != nil {
			return err
		}
		uri := ""
		if a.Name.Space != "" {
			uri = bindings[a.Name.Space]
		}
		k := expandedName{uri, a.Name.Local}
		if seen[k] {
			return fmt.Errorf("parse XML: duplicate attribute {%s}%s", uri, a.Name.Local)
		}
		seen[k] = true
	}
	return nil
}

// TestTagScopeMatchesMaps generates start tags under generated ancestor
// chains and requires validateStartElement to reach the verdict, and the
// error text, of the map-building version it replaced. The vocabulary is
// small on purpose, so that duplicates, unbound and undeclared prefixes,
// rebinding xml and xmlns, and XML 1.1 undeclarations all come up often, and
// tag sizes straddle smallTag so that both the scan and the map are tried.
func TestTagScopeMatchesMaps(t *testing.T) {
	// Each list holds its common names first and, after the bar, the ones
	// that break a rule; rare draws one from after the bar.
	prefixes := []string{"", "a", "b", "c", "xml", "xmlns", "a:"}
	const prefixBar = 4
	uris := []string{"urn:a", "urn:b", "urn:c", "", NSXML, NSXMLNS}
	const uriBar = 4
	rng := rand.New(rand.NewSource(1))
	draw := func(s []string, bar int) string {
		if rng.Intn(20) == 0 {
			return s[bar+rng.Intn(len(s)-bar)]
		}
		return s[rng.Intn(bar)]
	}
	local := func() string { return string(rune('a' + rng.Intn(26))) }

	var errs, oks, bigOKs int
	for range 200000 {
		var parent *Node
		for range rng.Intn(5) {
			var n *Node
			if parent == nil {
				n = NewNode(KindElement, QName{Local: "e"}, "")
			} else {
				n = parent.AppendElement(QName{Local: "e"})
			}
			for range rng.Intn(4) {
				n.AddNamespace(draw(prefixes, prefixBar), draw(uris, uriBar))
			}
			parent = n
		}
		tag := xml.StartElement{Name: xml.Name{Space: draw(prefixes, prefixBar), Local: local()}}
		for range rng.Intn(2 * (smallTag + 2)) {
			var a xml.Attr
			switch rng.Intn(12) {
			case 0: // xmlns:p="..."
				a.Name = xml.Name{Space: "xmlns", Local: draw(prefixes, prefixBar)}
				a.Value = draw(uris, uriBar)
			case 1: // xmlns="..."
				a.Name = xml.Name{Local: "xmlns"}
				a.Value = draw(uris, uriBar)
			default:
				a.Name = xml.Name{Space: draw(prefixes, prefixBar), Local: local()}
			}
			tag.Attr = append(tag.Attr, a)
		}
		xml11 := rng.Intn(2) == 0

		got := validateStartElement(tag, parent, xml11)
		want := validateStartElementMaps(tag, parent, xml11)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("tag %+v xml11=%v:\n got  %v\n want %v", tag, xml11, got, want)
		}
		if want == nil {
			oks++
			if len(tag.Attr) > smallTag {
				bigOKs++
			}
		} else {
			errs++
		}
	}
	// Both verdicts must be well represented, or the comparison says little.
	if oks < 10000 || errs < 10000 || bigOKs < 1000 {
		t.Fatalf("generator is lopsided: %d accepted (%d above smallTag), %d rejected", oks, bigOKs, errs)
	}
}

// TestTagScopeDoesNotAllocate is the reason for tagScope: a small start tag
// under ancestors with many declarations in scope is checked without
// building a map of them.
func TestTagScopeDoesNotAllocate(t *testing.T) {
	var parent *Node
	for d := range 4 {
		var n *Node
		if parent == nil {
			n = NewNode(KindElement, QName{Local: "e"}, "")
		} else {
			n = parent.AppendElement(QName{Local: "e"})
		}
		for i := range 4 {
			n.AddNamespace(fmt.Sprintf("p%d%d", d, i), fmt.Sprintf("urn:%d%d", d, i))
		}
		parent = n
	}
	tok := xml.StartElement{
		Name: xml.Name{Space: "p00", Local: "e"},
		Attr: []xml.Attr{
			{Name: xml.Name{Space: "xmlns", Local: "q"}, Value: "urn:q"},
			{Name: xml.Name{Space: "q", Local: "a"}, Value: "1"},
			{Name: xml.Name{Space: "p31", Local: "a"}, Value: "2"},
		},
	}
	if n := testing.AllocsPerRun(100, func() {
		if err := validateStartElement(tok, parent, false); err != nil {
			t.Fatal(err)
		}
	}); n != 0 {
		t.Errorf("validateStartElement under 16 inherited bindings allocated %.0f times, want 0", n)
	}
}
