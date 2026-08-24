package xdm

import (
	"strings"
	"testing"
)

// An internal general entity is expanded. Some schemas need this: the W3C's
// own RFC 3986 type library composes its URI regexes out of fifty entities
// named after the grammar's productions, and without expansion the document is
// simply unparseable.
func TestInternalEntityExpands(t *testing.T) {
	cases := []struct{ src, want string }{
		{`<!DOCTYPE r [<!ENTITY x "hello">]><r>&x;</r>`, "hello"},
		// Nesting is what makes this useful, and what makes it dangerous.
		{`<!DOCTYPE r [<!ENTITY a "1"><!ENTITY b "&a;2">]><r>&b;</r>`, "12"},
		// A character reference inside replacement text is decoded here: text
		// arriving through the decoder's entity map is substituted rather
		// than re-scanned, so "&#65;" would otherwise reach the value whole.
		{`<!DOCTYPE r [<!ENTITY x "&#65;&#x42;">]><r>&x;</r>`, "AB"},
	}
	for _, c := range cases {
		tree, err := ParseString(c.src, ParseOptions{AllowDOCTYPE: true})
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got := tree.Root.ChildElements()[0].StringValue(); got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
}

// XML §4.2: where an entity is declared more than once the *first* binds.
//
// The RFC 3986 library depends on it — sub-delims is declared three times, the
// first escaped for a regex and the later ones showing the unescaped grammar
// for a human reader. Keeping the last produced a pattern with bare "(" and
// "+" in it, which then failed to compile.
func TestFirstEntityDeclarationWins(t *testing.T) {
	const src = `<!DOCTYPE r [<!ENTITY x "first"><!ENTITY x "second">]><r>&x;</r>`
	tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := tree.Root.ChildElements()[0].StringValue(); got != "first" {
		t.Errorf("got %q, want the first declaration", got)
	}
}

// Nothing external is ever fetched. This is the line AllowDOCTYPE exists to
// hold, and expanding internal entities must not move it.
func TestExternalEntitiesStayRefused(t *testing.T) {
	for _, src := range []string{
		`<!DOCTYPE r [<!ENTITY x SYSTEM "file:///etc/passwd">]><r>&x;</r>`,
		`<!DOCTYPE r [<!ENTITY x SYSTEM "http://127.0.0.1/">]><r>&x;</r>`,
		`<!DOCTYPE r [<!ENTITY x PUBLIC "-//id" "file:///etc/passwd">]><r>&x;</r>`,
		// Reached through an internal entity rather than directly.
		`<!DOCTYPE r [<!ENTITY e SYSTEM "file:///etc/passwd"><!ENTITY x "&e;">]><r>&x;</r>`,
	} {
		if _, err := ParseString(src, ParseOptions{AllowDOCTYPE: true}); err == nil {
			t.Errorf("an external entity was resolved: %s", src)
		}
	}
}

// Expansion is bounded, because nesting is exactly how billion-laughs works.
func TestEntityExpansionIsBounded(t *testing.T) {
	// Five levels of ten reaches 100,000 bytes. An earlier 1 MB per-entity cap
	// let this through, which is why the limit is measured against the largest
	// legitimate use (9,569 bytes) rather than picked.
	bomb := `<!DOCTYPE r [
<!ENTITY a "aaaaaaaaaa">
<!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;&a;&a;">
<!ENTITY c "&b;&b;&b;&b;&b;&b;&b;&b;&b;&b;">
<!ENTITY d "&c;&c;&c;&c;&c;&c;&c;&c;&c;&c;">
<!ENTITY e "&d;&d;&d;&d;&d;&d;&d;&d;&d;&d;">
]><r>&e;</r>`
	if _, err := ParseString(bomb, ParseOptions{AllowDOCTYPE: true}); err == nil {
		t.Error("an entity-expansion bomb parsed successfully")
	}

	// A cycle must terminate rather than recurse forever.
	for _, src := range []string{
		`<!DOCTYPE r [<!ENTITY x "&x;">]><r>&x;</r>`,
		`<!DOCTYPE r [<!ENTITY x "&y;"><!ENTITY y "&x;">]><r>&x;</r>`,
	} {
		if _, err := ParseString(src, ParseOptions{AllowDOCTYPE: true}); err == nil {
			t.Errorf("a self-referential entity resolved: %s", src)
		}
	}
}

// A parameter entity is not read: those are expanded inside the DTD itself,
// and interpreting one means treating the subset as a grammar rather than
// scanning it.
func TestParameterEntitiesAreIgnored(t *testing.T) {
	const src = `<!DOCTYPE r [<!ENTITY % p SYSTEM "file:///etc/passwd">%p;]><r>ok</r>`
	tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		// Refusing outright is also acceptable.
		return
	}
	if got := tree.Root.ChildElements()[0].StringValue(); got != "ok" {
		t.Errorf("got %q; a parameter entity must not have been expanded", got)
	}
}

// Without AllowDOCTYPE nothing changes: the declaration is refused before any
// of this runs.
func TestEntitiesNeedAllowDOCTYPE(t *testing.T) {
	const src = `<!DOCTYPE r [<!ENTITY x "hello">]><r>&x;</r>`
	_, err := ParseString(src, ParseOptions{})
	if err == nil {
		t.Fatal("a DOCTYPE must be refused by default")
	}
	if !strings.Contains(err.Error(), "DOCTYPE") {
		t.Errorf("error = %v, want it to name the DOCTYPE", err)
	}
}

// The predefined five keep working and are not double-expanded.
func TestPredefinedEntitiesUnaffected(t *testing.T) {
	const src = `<!DOCTYPE r [<!ENTITY x "a&amp;b">]><r>&x;</r>`
	tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := tree.Root.ChildElements()[0].StringValue(); got != "a&b" {
		t.Errorf("got %q, want a&b", got)
	}
}

// An entity whose replacement text is markup is parsed as markup.
//
// encoding/xml cannot do this: dec.Entity maps a name to a string and the
// decoder substitutes that string as character data without re-scanning it, so
// <!ENTITY e "<b/>"> would reach the tree as the four characters "<b/>". XML
// says the replacement text is parsed, which is what makes an entity a way to
// factor out a fragment rather than only a phrase.
func TestEntityHoldingMarkupIsParsed(t *testing.T) {
	tree, err := ParseString(
		`<!DOCTYPE d [<!ENTITY e "<b/>">]><d>&e;</d>`,
		ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	kids := tree.Root.ChildElements()
	if len(kids) != 1 {
		t.Fatalf("root has %d element children, want 1", len(kids))
	}
	inner := kids[0].ChildElements()
	if len(inner) != 1 || inner[0].Name.Local != "b" {
		t.Fatalf("entity expanded to %v, want an element named b", inner)
	}
}

// A declaration ends at the first ">" outside a quoted value.
//
// Scanning for a bare ">" truncates the replacement text of any entity that
// contains one, silently: the entity still expands, to less than it says. It
// is how <!ENTITY e "<b/>"> came to hold the value "<b/".
func TestEntityValueMayContainAngleBrackets(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"a greater-than in text",
			`<!DOCTYPE d [<!ENTITY e "a > b">]><d>&e;</d>`, "a > b"},
		{"a quoted greater-than after another declaration",
			`<!DOCTYPE d [<!ENTITY x "plain"><!ENTITY e "1 > 0">]><d>&e;</d>`,
			"1 > 0"},
		{"apostrophe-quoted value",
			`<!DOCTYPE d [<!ENTITY e 'a > b'>]><d>&e;</d>`, "a > b"},
	}
	for _, c := range cases {
		tree, err := ParseString(c.src, ParseOptions{AllowDOCTYPE: true})
		if err != nil {
			t.Errorf("%s: parse: %v", c.name, err)
			continue
		}
		if got := tree.Root.StringValue(); got != c.want {
			t.Errorf("%s: expanded to %q, want %q", c.name, got, c.want)
		}
	}
}

// Substituting markup must not widen what the entity bounds admit. Each of
// these takes the markup path, which is a different code path from the one
// the other expansion tests cover.
func TestMarkupEntityExpansionIsStillBounded(t *testing.T) {
	cases := []struct{ name, src string }{
		{"a billion-laughs built from elements", `<!DOCTYPE d [
			<!ENTITY a "<x/><x/><x/><x/><x/><x/><x/><x/><x/><x/>">
			<!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;&a;&a;">
			<!ENTITY c "&b;&b;&b;&b;&b;&b;&b;&b;&b;&b;">
			<!ENTITY e "&c;&c;&c;&c;&c;&c;&c;&c;&c;&c;">
			<!ENTITY f "&e;&e;&e;&e;&e;&e;&e;&e;&e;&e;">
			]><d>&f;</d>`},

		{"an entity that refers to itself",
			`<!DOCTYPE d [<!ENTITY r "<x/>&r;">]><d>&r;</d>`},

		{"an external entity beside a markup one",
			`<!DOCTYPE d [<!ENTITY x SYSTEM "/etc/passwd">` +
				`<!ENTITY m "<b/>">]><d>&m;&x;</d>`},
	}
	for _, c := range cases {
		if _, err := ParseString(c.src, ParseOptions{AllowDOCTYPE: true}); err == nil {
			t.Errorf("%s: was accepted; it should be refused", c.name)
		}
	}

	// Many references, each individually small, are bounded by count.
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE d [<!ENTITY m "<b/>">]><d>`)
	for i := 0; i < maxEntityCount+1; i++ {
		sb.WriteString("&m;")
	}
	sb.WriteString(`</d>`)
	if _, err := ParseString(sb.String(), ParseOptions{AllowDOCTYPE: true}); err == nil {
		t.Error("a document of nothing but entity references was accepted")
	}
}

// The markup path must not change what a text entity does, and must not run
// for a document that has none.
func TestTextEntitiesAreUnaffected(t *testing.T) {
	tree, err := ParseString(
		`<!DOCTYPE d [<!ENTITY t "plain text">]><d>&t;</d>`,
		ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := tree.Root.StringValue(); got != "plain text" {
		t.Errorf("expanded to %q, want %q", got, "plain text")
	}
}

// CDATA sections, comments and processing instructions are the three regions
// where XML does not recognise an entity reference: inside them "&e;" is five
// characters and nothing else.
//
// A rewrite that does not know this does two wrong things at once. It expands
// a reference the document meant literally — and, worse, it lets replacement
// text close the region and open a new one, so "]]><evil/><![CDATA[" turns an
// entity's contents into document structure. Both are silent, and both change
// what a validator concludes: a document valid per spec was rejected, and
// markup could be smuggled past a validator whose downstream consumer parses
// CDATA correctly.
//
// Found by audit, not by the conformance suite, which has no case for it.
func TestEntityReferencesAreNotRecognisedInCDATACommentsOrPIs(t *testing.T) {
	cases := []struct{ name, src, wantText string }{
		{"a reference inside CDATA is literal",
			`<!DOCTYPE r [<!ENTITY e "<b/>">]><r><![CDATA[&e;]]></r>`, "&e;"},

		{"replacement text cannot break out of CDATA",
			`<!DOCTYPE r [<!ENTITY e "]]><evil/><![CDATA[">]>` +
				`<r><![CDATA[&e;]]></r>`, "&e;"},

		{"a reference inside a comment is not expanded",
			`<!DOCTYPE r [<!ENTITY e "--><evil/><!--">]><r><!--&e;--></r>`, ""},

		{"a reference inside a PI is not expanded",
			`<!DOCTYPE r [<!ENTITY e "?><evil/><?p ">]><r><?p &e;?></r>`, ""},
	}
	for _, c := range cases {
		tree, err := ParseString(c.src, ParseOptions{AllowDOCTYPE: true})
		if err != nil {
			t.Errorf("%s: parse: %v", c.name, err)
			continue
		}
		r := tree.Root.ChildElements()[0]
		if got := r.StringValue(); got != c.wantText {
			t.Errorf("%s: text is %q, want %q", c.name, got, c.wantText)
		}
		// The breakout cases are the point: no element may appear inside <r>
		// that the document did not write.
		for _, el := range r.ChildElements() {
			t.Errorf("%s: an element %q was manufactured from entity text",
				c.name, el.Name.Local)
		}
	}
}

// The same entity must expand to the same thing whether or not some *other*
// entity happens to contain markup.
//
// Replacement text is decoded once, not twice. Through dec.Entity the decoder
// substitutes without re-scanning, so "&amp;" has to be decoded during
// expansion; through the rewrite the text is scanned again, so decoding it
// during expansion as well turns "&amp;lt;" into "<" — manufacturing markup
// characters out of data the document deliberately escaped.
//
// A character reference is the opposite case and is decoded on both paths: it
// may form part of a *name*, as <!ENTITY dii "<&#xE14;&#xE35;/>"> does, and a
// name is not a place a reference survives to be decoded later.
func TestReplacementTextIsDecodedOnce(t *testing.T) {
	// The declaration's literal value is "&amp;lt;evil/&amp;gt;". XML §4.4.5
	// expands "&amp;" at declaration time, so the replacement text is
	// "&lt;evil/&gt;" — and those are *not* expanded again, so the text value
	// is those twelve characters.
	//
	// Confirmed against xmllint, which serialises this document's content as
	// "&amp;lt;evil/&amp;gt;" — the escaping of a value that is literally
	// "&lt;evil/&gt;". Decoding twice would give "<evil/>", which is the bug
	// this test exists to catch.
	const want = "&lt;evil/&gt;"
	cases := []struct{ name, src string }{
		{"without a markup entity",
			`<!DOCTYPE r [<!ENTITY e "&amp;lt;evil/&amp;gt;">]><r>&e;</r>`},
		{"with a markup entity forcing the rewrite",
			`<!DOCTYPE r [<!ENTITY m "<b/>"><!ENTITY e "&amp;lt;evil/&amp;gt;">]>` +
				`<r>&e;</r>`},
	}
	for _, c := range cases {
		tree, err := ParseString(c.src, ParseOptions{AllowDOCTYPE: true})
		if err != nil {
			t.Errorf("%s: parse: %v", c.name, err)
			continue
		}
		r := tree.Root.ChildElements()[0]
		if got := r.StringValue(); got != want {
			t.Errorf("%s: expanded to %q, want %q", c.name, got, want)
		}
		for _, el := range r.ChildElements() {
			t.Errorf("%s: escaped text became an element %q",
				c.name, el.Name.Local)
		}
	}
}

// Entities the document never references must not consume the expansion
// budget. Testing whether any entity holds markup resolves every declaration,
// and charging those would let a subset full of large unused entities exhaust
// the budget — so a legitimate reference then failed with an error about
// something else entirely.
func TestUnusedEntitiesDoNotConsumeTheBudget(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE r [`)
	big := strings.Repeat("x", 60000)
	for i := 0; i < 30; i++ {
		sb.WriteString(`<!ENTITY unused`)
		sb.WriteString(itoaTest(i))
		sb.WriteString(` "`)
		sb.WriteString(big)
		sb.WriteString(`">`)
	}
	sb.WriteString(`<!ENTITY m "<b/>"><!ENTITY used "ok">]><r>&m;&used;</r>`)

	tree, err := ParseString(sb.String(),
		ParseOptions{AllowDOCTYPE: true, MaxBytes: -1})
	if err != nil {
		t.Fatalf("unused declarations exhausted the budget: %v", err)
	}
	// &m; is an element and &used; is text, so the string value of <r> is
	// just the text: the point is that both expanded at all.
	r := tree.Root.ChildElements()[0]
	if got := r.StringValue(); got != "ok" {
		t.Errorf("expanded to %q, want %q", got, "ok")
	}
	if len(r.ChildElements()) != 1 {
		t.Errorf("the markup entity did not expand to an element")
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// A character reference to "<" starts markup; "&lt;" does not.
//
// Expansion turns "&#x3C;b/&#x3E;" into "<b/>", which is then parsed as an
// element — while "&lt;b/&gt;" is an escaped character and stays text. The
// three spellings look interchangeable and are not, and libxml2 draws the
// line in the same place; these expectations were checked against it.
func TestCharacterReferenceToAngleBracketIsMarkup(t *testing.T) {
	cases := []struct {
		name, src   string
		wantElement bool
		wantText    string
	}{
		{"a hex character reference",
			`<!DOCTYPE r [<!ENTITY a "&#x3C;b/&#x3E;">]><r>&a;</r>`, true, ""},
		{"a decimal character reference",
			`<!DOCTYPE r [<!ENTITY a "&#60;b/&#62;">]><r>&a;</r>`, true, ""},
		{"an escaped angle bracket stays text",
			`<!DOCTYPE r [<!ENTITY a "&lt;b/&gt;">]><r>&a;</r>`, false, "<b/>"},
		{"markup reached only through nesting",
			`<!DOCTYPE r [<!ENTITY a "<b/>"><!ENTITY c "&a;">]><r>&c;</r>`,
			true, ""},
	}
	for _, c := range cases {
		tree, err := ParseString(c.src, ParseOptions{AllowDOCTYPE: true})
		if err != nil {
			t.Errorf("%s: parse: %v", c.name, err)
			continue
		}
		r := tree.Root.ChildElements()[0]
		if got := len(r.ChildElements()) > 0; got != c.wantElement {
			t.Errorf("%s: produced an element = %v, want %v",
				c.name, got, c.wantElement)
		}
		if got := r.StringValue(); got != c.wantText {
			t.Errorf("%s: text is %q, want %q", c.name, got, c.wantText)
		}
	}
}

// Whether an entity holds markup must not depend on map iteration order.
//
// Deciding it by resolving every declaration made the answer order-dependent:
// a subset whose large entities happened to be visited first exhausted the
// budget, resolution failed, and the entity that did hold markup was never
// reached — so the same document parsed differently from run to run. The test
// repeats because that is the only way a map-order bug shows itself.
func TestMarkupDetectionIsDeterministic(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE r [`)
	big := strings.Repeat("x", 60000)
	for i := 0; i < 30; i++ {
		sb.WriteString(`<!ENTITY unused` + itoaTest(i) + ` "` + big + `">`)
	}
	sb.WriteString(`<!ENTITY m "<b/>">]><r>&m;</r>`)
	src := sb.String()

	for i := 0; i < 20; i++ {
		tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true, MaxBytes: -1})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		r := tree.Root.ChildElements()[0]
		if len(r.ChildElements()) != 1 {
			t.Fatalf("run %d: the markup entity did not expand to an element", i)
		}
	}
}

// XML 1.0 section 4.4.5, "Included in Literal": when an entity reference
// appears inside an attribute value, its replacement text is included as
// though it were literal characters, so a quote in that text is DATA and does
// not end the attribute.
//
// The rewrite path splices replacement text straight into the source, where a
// bare quote would end the attribute and the rest of the tag would be read as
// garbage. DocBook is the real case: its common/entities.ent declares
//
//	<!ENTITY primary 'normalize-space(concat(primary/@sortas, " ", primary))'>
//
// and fo/autoidx.xsl uses &primary; inside a double-quoted attribute. Before
// this was handled the file failed to parse with "expected attribute name in
// element" — which is what a stray quote looks like from the decoder.
//
// The rewrite path is what needs testing, so every case here declares one
// entity holding markup to select it.
func TestQuotesInReplacementTextDoNotEndAnAttribute(t *testing.T) {
	const markup = `<!ENTITY frag "<b/>">`
	cases := []struct{ name, decls, doc, want string }{
		{
			name:  "double quote in a double-quoted attribute",
			decls: `<!ENTITY q 'concat(a, " ", b)'>`,
			doc:   `<r v="&q;"/>`,
			want:  `concat(a, " ", b)`,
		},
		{
			name:  "single quote in a single-quoted attribute",
			decls: `<!ENTITY q "it's">`,
			doc:   `<r v='&q;'/>`,
			want:  `it's`,
		},
		{
			name:  "both quote kinds, either delimiter",
			decls: `<!ENTITY q "a'b&#34;c">`,
			doc:   `<r v="&q;"/>`,
			want:  `a'b"c`,
		},
		{
			name:  "the attribute after the reference is still an attribute",
			decls: `<!ENTITY q 'x " y'>`,
			doc:   `<r v="&q;" w="kept"/>`,
			want:  `x " y`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "<!DOCTYPE r [" + markup + c.decls + "]>" + c.doc
			tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
			if err != nil {
				t.Fatalf("%s: %v", src, err)
			}
			el := tree.Root.ChildElements()[0]
			if got := el.AttrValue("v"); got != c.want {
				t.Errorf("v = %q, want %q", got, c.want)
			}
		})
	}
}

// A quote inside replacement text must not change the scanner's idea of where
// an attribute ends either — the state machine advances on the document's own
// bytes only. Without that, an entity holding a lone quote would leave the
// scanner "inside" an attribute for the rest of the document and every later
// substitution would be escaped wrongly.
func TestReplacementQuotesDoNotDesynchroniseTheScanner(t *testing.T) {
	src := `<!DOCTYPE r [<!ENTITY frag "<b/>"><!ENTITY q '"'><!ENTITY t "plain">]>` +
		`<r v="&q;"><c>&t;</c>&frag;</r>`
	tree, err := ParseString(src, ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("%v", err)
	}
	r := tree.Root.ChildElements()[0]
	if got := r.AttrValue("v"); got != `"` {
		t.Errorf(`v = %q, want '"'`, got)
	}
	kids := r.ChildElements()
	if len(kids) != 2 {
		t.Fatalf("got %d child elements, want 2 (c and b)", len(kids))
	}
	// Text content after the quote-bearing attribute is still content, not
	// an escaped attribute value.
	if got := kids[0].StringValue(); got != "plain" {
		t.Errorf("c = %q, want %q", got, "plain")
	}
	if kids[1].Name.Local != "b" {
		t.Errorf("second child is %q, want b", kids[1].Name.Local)
	}
}
