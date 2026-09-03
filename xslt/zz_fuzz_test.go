package xslt

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// fuzzParseOptions bound what a generated document may cost. A limit firing is
// a correct refusal, so lowering these changes only the price of a run.
var fuzzParseOptions = xdm.ParseOptions{MaxDepth: 100, MaxNodes: 20000}

// roundTripSeeds are documents whose serialisation is where the two halves of
// the engine have to agree. Namespace redeclaration and undeclaration, text
// that contains "]]>", adjacent text nodes and CR/LF are the cases where a
// serialiser most easily writes something that does not parse back to the same
// tree.
var roundTripSeeds = []string{
	`<a/>`,
	`<a>text</a>`,
	`<a xmlns="u"><b/></a>`,
	`<a xmlns:p="u"><p:b p:c="1"/></a>`,
	`<a xmlns:p="u"><b xmlns:p="v"><p:c/></b></a>`,
	`<a xmlns:p="u"><b xmlns:p=""><c/></b></a>`,
	`<a xmlns="u"><b xmlns=""><c/></b></a>`,
	`<a><![CDATA[<not markup & co>]]></a>`,
	`<a>]]&gt;</a>`,
	`<a><!--c--><?pi data?>t</a>`,
	`<a>&#65;&amp;&lt;&gt;&quot;</a>`,
	`<a>x<b/>y<c/>z</a>`,
	"<a>x\r\ny\rz\n</a>",
	`<a b="&#10;&#9;&#13;" c="&quot;'&amp;"/>`,
	`<a>` + "é中\U0001F600" + `</a>`,
	`<_a.b-c xmlns:_p.q-r="u" _p.q-r:d="1"/>`,
	`<a><b><c><d><e/></d></c></b></a>`,
}

// A document must survive being written out and read back. Serialisation is
// the only place a tree leaves this engine, so a tree that cannot be written
// in a form that parses back to itself is a correctness bug wherever the
// result of a transform is handed on to another processor.
//
// The comparison is semantic rather than byte-for-byte: which prefix a
// namespace is written with is the serialiser's choice, and two spellings of
// the same expanded name are the same tree. What must survive is what the data
// model says a node is — its kind, its expanded name, and its string value.
func FuzzSerializeRoundTrip(f *testing.F) {
	for _, s := range roundTripSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 4096 {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("round trip of %q panicked: %v", src, r)
			}
		}()
		tree, err := xdm.ParseString(src, fuzzParseOptions)
		if err != nil {
			return // not a document; the parser's own target owns this case
		}

		var sb strings.Builder
		seq := xdm.Sequence{tree.Root}
		if err := Serialize(&sb, seq, OutputSettings{Method: "xml"}, nil); err != nil {
			// A serialiser may refuse — a character no encoding can hold, say
			// — but it must say so as an error.
			return
		}
		out := sb.String()

		again, err := xdm.ParseString(out, fuzzParseOptions)
		if err != nil {
			t.Fatalf("serialising %q produced %q, which does not parse back: %v", src, out, err)
		}
		compareTrees(t, src, out, tree.Root, again.Root)
	})
}

// compareTrees asserts that two trees are the same document in the terms the
// data model uses. Prefixes and namespace-declaration placement are excluded
// deliberately: they are serialisation choices, not part of what a node is.
func compareTrees(t *testing.T, src, out string, a, b *xdm.Node) {
	t.Helper()
	if a.Kind != b.Kind {
		t.Fatalf("round trip of %q via %q changed a node kind: %v became %v", src, out, a.Kind, b.Kind)
	}
	switch a.Kind {
	case xdm.KindElement, xdm.KindAttribute:
		// Expanded name only: URI and local name, never the prefix.
		if a.Name.URI != b.Name.URI || a.Name.Local != b.Name.Local {
			t.Fatalf("round trip of %q via %q changed a name: {%s}%s became {%s}%s",
				src, out, a.Name.URI, a.Name.Local, b.Name.URI, b.Name.Local)
		}
	case xdm.KindPI:
		if a.Name.Local != b.Name.Local {
			t.Fatalf("round trip of %q via %q changed a PI target: %s became %s",
				src, out, a.Name.Local, b.Name.Local)
		}
	}

	// Attributes compare as a set: their order is not part of the data model.
	if got, want := attrSet(b), attrSet(a); !sameStrings(got, want) {
		t.Fatalf("round trip of %q via %q changed the attributes of {%s}%s: %v became %v",
			src, out, a.Name.URI, a.Name.Local, want, got)
	}

	// Children compare after merging adjacent text and dropping the empty
	// text nodes a tree may carry: "x" and "x"+"" are the same content, and
	// the data model has no adjacent text nodes to distinguish.
	ac, bc := contentChildren(a), contentChildren(b)
	if len(ac) != len(bc) {
		t.Fatalf("round trip of %q via %q changed the child count of {%s}%s: %d became %d",
			src, out, a.Name.URI, a.Name.Local, len(ac), len(bc))
	}
	for i := range ac {
		if ac[i].Kind == xdm.KindText {
			if ac[i].Value != bc[i].Value {
				t.Fatalf("round trip of %q via %q changed text: %q became %q",
					src, out, ac[i].Value, bc[i].Value)
			}
			continue
		}
		compareTrees(t, src, out, ac[i], bc[i])
	}
}

// contentChildren returns the children that carry content, with adjacent text
// merged. Comments are kept — the xml method writes them and they are nodes —
// but an empty text node is not a node the data model distinguishes.
func contentChildren(n *xdm.Node) []*xdm.Node {
	var out []*xdm.Node
	for _, c := range n.Children {
		if c.Kind == xdm.KindText {
			if c.Value == "" {
				continue
			}
			if len(out) > 0 && out[len(out)-1].Kind == xdm.KindText {
				merged := *out[len(out)-1]
				merged.Value += c.Value
				out[len(out)-1] = &merged
				continue
			}
			// Copy, so that merging into it later cannot mutate the tree.
			c2 := *c
			c = &c2
		}
		out = append(out, c)
	}
	return out
}

// attrSet renders an element's attributes as sorted "{uri}local=value"
// strings, which is the comparison the data model licenses.
func attrSet(n *xdm.Node) []string {
	out := make([]string, 0, len(n.Attrs))
	for _, a := range n.Attrs {
		out = append(out, "{"+a.Name.URI+"}"+a.Name.Local+"="+a.StringValue())
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stylesheetSeeds are stylesheets, valid and not. A stylesheet is untrusted
// input for any host that runs one it did not write.
var stylesheetSeeds = []string{
	`<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"/>`,
	`<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:template match="/"><out/></xsl:template></xsl:stylesheet>`,
	`<xsl:stylesheet version="2.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:template match="a|b" priority="2"><xsl:value-of select="."/></xsl:template></xsl:stylesheet>`,
	`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:function name="f:g" xmlns:f="u"><xsl:sequence select="1"/></xsl:function></xsl:stylesheet>`,
	`<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:variable name="v" select="1+"/></xsl:stylesheet>`,
	`<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:template match="["/></xsl:stylesheet>`,
	`<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"/>`,
	`<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:include href="other.xsl"/></xsl:stylesheet>`,
	`<xsl:transform version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:output method="html" indent="yes"/></xsl:transform>`,
	`<out xsl:version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"/>`,
	`<notastylesheet/>`,
}

// Compile must never panic on a stylesheet. A nil Resolver means includes are
// refused rather than followed, which is both the safe default and what keeps
// this target from reading the filesystem.
func FuzzCompileStylesheetNoPanic(f *testing.F) {
	for _, s := range stylesheetSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 4096 {
			return
		}
		tree, err := xdm.ParseString(src, fuzzParseOptions)
		if err != nil {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Compile(%q) panicked: %v", src, r)
			}
		}()
		ss, err := Compile(tree.Root, CompileOptions{})
		if err != nil {
			if ss != nil {
				t.Fatalf("Compile(%q) returned both a stylesheet and an error %v", src, err)
			}
			// A compile error must not be an empty message: a refusal the
			// caller cannot report is as unhelpful as no refusal at all.
			//
			// It is deliberately not asserted that every error carries a
			// spec code. This package writes them in two spellings -- a
			// "XTSE0010: ..." prefix in most places and a "... (XTSE0010)"
			// suffix in a handful -- and only the prefix is what
			// xdm.ErrorCode recognises. Tightening this belongs with
			// settling that inconsistency, not with a fuzz target; and some
			// refusals here are configuration rather than spec, such as an
			// xsl:include reaching a nil Resolver, and have no code to give.
			if strings.TrimSpace(err.Error()) == "" {
				t.Fatalf("Compile(%q) failed with an empty message", src)
			}
			return
		}
		if ss == nil {
			t.Fatalf("Compile(%q) returned no error and no stylesheet", src)
		}
	})
}
