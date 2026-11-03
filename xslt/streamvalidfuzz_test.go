package xslt

// Lane H1's grammar of TestStreamFuzz: an xsl:source-document validating the
// document it streams (streamvalidin.go). Each case makes a schema of its own
// -- record content as a sequence, a choice or an all group, children typed
// xs:integer, xs:decimal, xs:string, xs:date, xs:boolean, a list or a union,
// with occurrence bounds, nillable ones, mixed content, attributes required,
// defaulted, ID and IDREF typed, sometimes an assertion -- a document of that
// schema, mostly valid and sometimes made invalid in one place, and a body
// whose answer depends on the typing: typed sums and comparisons, atomized
// lists and unions, annotations tested, copies carrying defaults, rules
// matching types. GOXSLT_STREAM_FUZZ_VALID=0 leaves them out.

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
)

var fuzzValidShapes = os.Getenv("GOXSLT_STREAM_FUZZ_VALID") != "0"

// fuzzIsValid decides, from a random stream of its own, whether the case of
// seed is one of fuzzMakeValid's.
func fuzzIsValid(seed int64) bool {
	return fuzzValidShapes && rand.New(rand.NewSource(seed*15485863+41)).Intn(100) < 12
}

// vChild is one child element of the record type.
type vChild struct {
	name, typ    string
	min, max     int // max -1: unbounded
	nillable     bool
	values, bads []string
}

var vTypes = []struct {
	typ          string
	values, bads []string
}{
	{"xs:integer", []string{"1", "2", "-3", " 7 ", "10", "007"}, []string{"1.5", "x", ""}},
	{"xs:decimal", []string{"1.10", "0.1", "0.2", "2", "-0.5", " 3.25 "}, []string{"1e3", "x", "NaN"}},
	{"xs:string", []string{"a", "b", " a ", "", "é"}, nil},
	{"xs:date", []string{"2020-02-29", "1999-12-31", "2001-01-01Z"}, []string{"2021-02-29", "x"}},
	{"xs:boolean", []string{"true", "false", "1", "0"}, []string{"yes"}},
	{"lst", []string{"1 2 3", "4", "", " 5  6 "}, []string{"1 x"}},
	{"uni", []string{"1", "2020-01-01", "12"}, []string{"abc"}},
}

func fuzzMakeValid(seed int64) fuzzCase {
	g := &fuzzGen{r: rand.New(rand.NewSource(seed*7919 + 13))}
	// The record type.
	var kids []vChild
	for i, n := 0, 1+g.n(4); i < n; i++ {
		t := vTypes[g.n(len(vTypes))]
		c := vChild{name: fmt.Sprintf("c%d", i), typ: t.typ, min: g.n(2), max: 1, values: t.values, bads: t.bads}
		if g.p(40) {
			c.max = g.pick2(2, -1)
		}
		c.nillable = g.p(25)
		kids = append(kids, c)
	}
	model := g.pick("sequence", "sequence", "choice", "all")
	if model == "all" {
		for i := range kids {
			kids[i].max = 1
		}
	}
	mixed := model != "all" && g.p(20)
	assertN := 0
	if g.p(20) {
		assertN = 2 + g.n(10)
	}
	var sch strings.Builder
	sch.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
	sch.WriteString(`<xs:simpleType name="lst"><xs:list itemType="xs:decimal"/></xs:simpleType>`)
	sch.WriteString(`<xs:simpleType name="uni"><xs:union memberTypes="xs:integer xs:date"/></xs:simpleType>`)
	sch.WriteString(`<xs:element name="doc"><xs:complexType><xs:sequence><xs:element ref="rec" minOccurs="0" maxOccurs="unbounded"/>`)
	sch.WriteString(`</xs:sequence>`)
	if assertN > 0 {
		fmt.Fprintf(&sch, `<xs:assert test="count(rec) le %d"/>`, assertN)
	}
	sch.WriteString(`</xs:complexType></xs:element>`)
	fmt.Fprintf(&sch, `<xs:element name="rec"><xs:complexType mixed="%v"><xs:%s>`, mixed, model)
	for _, c := range kids {
		max := "unbounded"
		if c.max >= 0 {
			max = fmt.Sprint(c.max)
		}
		fmt.Fprintf(&sch, `<xs:element name="%s" type="%s" minOccurs="%d" maxOccurs="%s" nillable="%v"/>`,
			c.name, c.typ, c.min, max, c.nillable)
	}
	fmt.Fprintf(&sch, `</xs:%s>`, model)
	sch.WriteString(`<xs:attribute name="n" type="xs:integer" use="required"/>`)
	sch.WriteString(`<xs:attribute name="r" type="xs:decimal" default="1.50"/>`)
	sch.WriteString(`<xs:attribute name="k" type="xs:token" default=" a  b "/>`)
	sch.WriteString(`<xs:attribute name="id" type="xs:ID"/><xs:attribute name="ref" type="xs:IDREF"/>`)
	sch.WriteString(`</xs:complexType></xs:element></xs:schema>`)

	// A document of it.
	nrec := g.n(8)
	if g.p(10) {
		nrec = 100 + g.n(300)
	}
	bad := g.p(25)
	badAt := g.n(nrec + 1)
	var doc strings.Builder
	doc.WriteString(`<doc xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">`)
	ws := g.p(50)
	for i := 0; i < nrec; i++ {
		if ws {
			doc.WriteString("\n ")
		}
		fmt.Fprintf(&doc, `<rec n="%d"`, g.n(5))
		if g.p(30) {
			doc.WriteString(` r="` + g.pick("2", "0.25", "3.0") + `"`)
		}
		if g.p(20) {
			fmt.Fprintf(&doc, ` id="i%d"`, i)
		}
		if i > 0 && g.p(15) {
			doc.WriteString(` ref="i0"`) // valid only when rec 0 has id="i0"
		}
		doc.WriteString(`>`)
		g.vContent(&doc, kids, model, mixed, bad && i == badAt)
		doc.WriteString(`</rec>`)
	}
	if ws {
		doc.WriteString("\n")
	}
	doc.WriteString(`</doc>`)

	// A body that reads it.
	c0 := kids[0]
	num := ""
	for _, c := range kids {
		if c.typ == "xs:decimal" || c.typ == "xs:integer" {
			num = c.name
			break
		}
	}
	bodies := []string{
		`<v><xsl:value-of select="count(doc/rec)"/></v>`,
		`<xsl:for-each select="doc/rec"><r n="{@n + 1}" r="{data(@r)}" k="{data(@k)}" t="{. instance of element(rec)}"/></xsl:for-each>`,
		`<o><xsl:copy-of select="doc/rec[` + fmt.Sprint(1+g.n(3)) + `]"/></o>`,
		`<o><xsl:copy-of select="doc/rec"/></o>`,
		`<v><xsl:value-of select="string-join(doc/rec/` + c0.name + `/data() ! string(), ',')"/></v>`,
		`<v><xsl:value-of select="count(doc/rec/` + c0.name + `/data())"/></v>`,
		`<xsl:for-each select="doc/rec/` + c0.name + `"><c nil="{nilled(.)}"/></xsl:for-each>`,
		`<xsl:apply-templates select="doc/rec" mode="m"/>`,
		`<xsl:apply-templates mode="m"/>`,
		`<v><xsl:value-of select="count(doc/text())"/></v>`,
		`<v><xsl:value-of select="exists(doc/rec)"/></v>`,
	}
	if num != "" {
		bodies = append(bodies,
			`<v><xsl:value-of select="sum(doc/rec/`+num+`)"/></v>`,
			`<v><xsl:value-of select="avg(doc/rec/`+num+`)"/></v>`,
			`<v><xsl:value-of select="max(doc/rec/`+num+`)"/></v>`,
			`<xsl:for-each select="doc/rec/`+num+`"><x v="{. + 1}"/></xsl:for-each>`)
	}
	body := bodies[g.n(len(bodies))]
	validation := g.pick(`validation="strict"`, `validation="strict"`, `validation="lax"`, `type="docT"`, `validation="preserve"`)
	if validation == `type="docT"` {
		// The document element's type, named.
		s := sch.String()
		s = strings.Replace(s, `<xs:element name="doc"><xs:complexType>`, `<xs:element name="doc" type="docT"/><xs:complexType name="docT">`, 1)
		s = strings.Replace(s, `</xs:complexType></xs:element><xs:element name="rec">`, `</xs:complexType><xs:element name="rec">`, 1)
		sch.Reset()
		sch.WriteString(s)
	}
	var s strings.Builder
	s.WriteString(`<xsl:stylesheet version="3.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform" xmlns:xs="http://www.w3.org/2001/XMLSchema" exclude-result-prefixes="xs"`)
	if g.p(10) {
		s.WriteString(` input-type-annotations="strip"`)
	}
	s.WriteString(">\n<xsl:import-schema>" + sch.String() + "</xsl:import-schema>\n")
	if g.p(30) {
		s.WriteString(`<xsl:strip-space elements="*"/>` + "\n")
	}
	s.WriteString(`<xsl:mode name="m" streamable="yes"/>` + "\n")
	s.WriteString(g.pick(
		`<xsl:template match="element(rec)" mode="m"><r n="{data(@n)}" r="{data(@r)}"/></xsl:template>`,
		`<xsl:template match="schema-element(rec)" mode="m"><r k="{@k}"><xsl:apply-templates mode="m"/></r></xsl:template>`,
		`<xsl:template match="element(*, xs:decimal)" mode="m"><d v="{data(.) * 3}"/></xsl:template>`,
		`<xsl:template match="element(*, xs:untyped)" mode="m"><u/></xsl:template>`,
		`<xsl:template match="rec/*" mode="m"><c v="{.}"/></xsl:template>`) + "\n")
	s.WriteString(`<xsl:template name="main"><top><xsl:source-document streamable="yes" href="doc.xml" ` + validation + `>`)
	s.WriteString(body + "</xsl:source-document></top></xsl:template>\n</xsl:stylesheet>\n")
	return fuzzCase{seed: seed, sheet: s.String(), doc: doc.String(), kind: "valid"}
}

func (g *fuzzGen) pick2(a, b int) int {
	if g.p(50) {
		return a
	}
	return b
}

// vContent writes a record's children: valid for the model, or, with bad,
// wrong in one way.
func (g *fuzzGen) vContent(b *strings.Builder, kids []vChild, model string, mixed, bad bool) {
	type out struct{ name, val string }
	var els []out
	switch model {
	case "choice":
		c := kids[g.n(len(kids))]
		for j, n := 0, max(1, g.occ(c)); j < n; j++ {
			els = append(els, out{c.name, g.vValue(c)})
		}
	default:
		for _, c := range kids {
			for j, n := 0, g.occ(c); j < n; j++ {
				els = append(els, out{c.name, g.vValue(c)})
			}
		}
		if model == "all" {
			g.r.Shuffle(len(els), func(i, j int) { els[i], els[j] = els[j], els[i] })
		}
	}
	if bad {
		switch g.n(5) {
		case 0:
			els = append(els, out{"zz", ""}) // not in the model
		case 1:
			if len(els) > 1 && model == "sequence" {
				els[0], els[len(els)-1] = els[len(els)-1], els[0] // out of order (perhaps still valid)
			}
		case 2:
			for i := range els {
				for _, c := range kids {
					if c.name == els[i].name && len(c.bads) > 0 {
						els[i].val = c.bads[g.n(len(c.bads))]
					}
				}
			}
		case 3:
			b.WriteString("x") // character data (valid in mixed content)
		default:
			els = append(els, els...) // too many (perhaps still valid)
		}
	}
	for _, e := range els {
		if mixed && g.p(30) {
			b.WriteString(g.pick("t", " ", "u v"))
		}
		if e.val == "\x00nil" {
			b.WriteString(`<` + e.name + ` xsi:nil="true"/>`)
			continue
		}
		b.WriteString(`<` + e.name + `>` + e.val + `</` + e.name + `>`)
	}
}

// occ is how many times c occurs in a valid record.
func (g *fuzzGen) occ(c vChild) int {
	hi := c.max
	if hi < 0 {
		hi = 3
	}
	return c.min + g.n(hi-c.min+1)
}

// vValue is a valid value for c, or its nil.
func (g *fuzzGen) vValue(c vChild) string {
	if c.nillable && g.p(25) {
		return "\x00nil"
	}
	return c.values[g.n(len(c.values))]
}
