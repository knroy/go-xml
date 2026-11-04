package xsd

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/internal/xdmstream"
	"github.com/knroy/go-xml/v2/internal/xsdstream"
	"github.com/knroy/go-xml/v2/xdm"
)

// The validation of a stream against the tree's: for each schema and
// document, the copy the cursor grounds of the document element carries
// exactly the typing, the attributes (defaults among them) and the text of
// the typed copy ValidateCopy (or ValidateAgainstTypeCopy, or
// ValidateElementLaxCopy) makes, and the stream finds the document invalid
// exactly when the tree does.

const svBooks = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:element name="BOOKLIST" type="BOOKLIST-type"/>
 <xs:complexType name="BOOKLIST-type"><xs:sequence>
   <xs:element ref="BOOKS"/><xs:element ref="CATEGORIES"/></xs:sequence></xs:complexType>
 <xs:element name="BOOKS"><xs:complexType><xs:sequence>
   <xs:element ref="ITEM" maxOccurs="unbounded"/></xs:sequence>
   <xs:assert test="count(ITEM) le 3"/></xs:complexType></xs:element>
 <xs:element name="CATEGORIES"><xs:complexType mixed="true">
   <xs:sequence minOccurs="0" maxOccurs="unbounded"><xs:element ref="CATEGORY"/></xs:sequence>
   <xs:attribute name="DESC" type="xs:string" use="required"/></xs:complexType></xs:element>
 <xs:element name="CATEGORY"><xs:complexType>
   <xs:attribute name="CODE" type="xs:ID" use="required"/>
   <xs:attribute name="DESC" type="xs:string" use="required"/></xs:complexType></xs:element>
 <xs:element name="ITEM"><xs:complexType><xs:sequence>
    <xs:element name="TITLE" type="xs:string"/>
    <xs:element name="PRICE" type="money"/>
    <xs:element name="DIM" type="dims" minOccurs="0"/>
    <xs:element name="QTY" type="xs:integer" minOccurs="0" maxOccurs="3"/>
   </xs:sequence>
   <xs:attribute name="CAT" type="xs:IDREF" use="required"/>
   <xs:attribute name="TAX" type="xs:NMTOKEN" default="V"/>
   <xs:attribute name="RATE" type="xs:decimal" default="1.50"/></xs:complexType></xs:element>
 <xs:complexType name="money"><xs:simpleContent><xs:extension base="xs:decimal">
   <xs:attribute name="cur" type="cur"/></xs:extension></xs:simpleContent></xs:complexType>
 <xs:simpleType name="cur"><xs:restriction base="xs:string">
   <xs:enumeration value="USD"/><xs:enumeration value="GBP"/></xs:restriction></xs:simpleType>
 <xs:simpleType name="dims"><xs:restriction><xs:simpleType><xs:list itemType="xs:decimal"/></xs:simpleType>
   <xs:length value="3"/></xs:restriction></xs:simpleType>
</xs:schema>`

const svBooksDoc = `<BOOKLIST>
 <BOOKS>
  <ITEM CAT="P"><TITLE>One</TITLE><PRICE cur="USD">4.50</PRICE><DIM>1 2 3</DIM><QTY>1</QTY><QTY>2</QTY></ITEM>
  <ITEM CAT="H" TAX="Z"><TITLE>Two</TITLE><PRICE>10</PRICE></ITEM>
 </BOOKS>
 <CATEGORIES DESC="all">text <CATEGORY CODE="P" DESC="p"/> more <CATEGORY CODE="H" DESC="h"/></CATEGORIES>
</BOOKLIST>`

const svMisc = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
   xmlns:m="urn:m" targetNamespace="urn:m" elementFormDefault="qualified">
 <xs:element name="doc"><xs:complexType><xs:sequence>
   <xs:element name="u" type="m:u" minOccurs="0" maxOccurs="unbounded"/>
   <xs:element name="n" type="xs:int" nillable="true" minOccurs="0" maxOccurs="unbounded"/>
   <xs:element ref="m:shape" minOccurs="0" maxOccurs="unbounded"/>
   <xs:element name="b" type="m:base" minOccurs="0" maxOccurs="unbounded"/>
   <xs:element name="all" minOccurs="0"><xs:complexType><xs:all>
     <xs:element name="x" type="xs:date"/><xs:element name="y" type="xs:boolean" minOccurs="0"/></xs:all>
   </xs:complexType></xs:element>
   <xs:element name="ch" minOccurs="0"><xs:complexType><xs:choice maxOccurs="2">
     <xs:element name="p" type="xs:string"/><xs:element name="q" type="m:lst"/></xs:choice>
   </xs:complexType></xs:element>
   <xs:element name="w" minOccurs="0"><xs:complexType><xs:sequence>
     <xs:any namespace="urn:o urn:m" processContents="lax" minOccurs="0" maxOccurs="unbounded"/>
     <xs:any namespace="urn:skip" processContents="skip" minOccurs="0"/></xs:sequence>
     <xs:anyAttribute processContents="lax"/></xs:complexType></xs:element>
   <xs:element name="e" minOccurs="0"><xs:complexType/></xs:element>
   <xs:element name="mx" minOccurs="0"><xs:complexType mixed="true"><xs:sequence>
     <xs:element name="i" type="xs:integer" minOccurs="0" maxOccurs="unbounded"/></xs:sequence></xs:complexType></xs:element>
 </xs:sequence></xs:complexType></xs:element>
 <xs:simpleType name="u"><xs:union memberTypes="xs:integer xs:date xs:string"/></xs:simpleType>
 <xs:simpleType name="lst"><xs:list itemType="xs:integer"/></xs:simpleType>
 <xs:element name="shape" type="m:shapeT" abstract="true"/>
 <xs:element name="circle" substitutionGroup="m:shape"><xs:complexType><xs:complexContent>
   <xs:extension base="m:shapeT"><xs:attribute name="r" type="xs:double"/></xs:extension></xs:complexContent></xs:complexType></xs:element>
 <xs:element name="square" substitutionGroup="m:shape" type="m:shapeT"/>
 <xs:complexType name="shapeT"><xs:attribute name="id" type="xs:ID"/></xs:complexType>
 <xs:complexType name="base"><xs:sequence><xs:element name="v" type="xs:decimal"/></xs:sequence></xs:complexType>
 <xs:complexType name="ext"><xs:complexContent><xs:extension base="m:base"><xs:sequence>
   <xs:element name="w" type="xs:token"/></xs:sequence></xs:extension></xs:complexContent></xs:complexType>
 <xs:element name="g" type="xs:gYear"/>
</xs:schema>`

const svMiscDoc = `<doc xmlns="urn:m" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:o="urn:o" xmlns:m="urn:m">
  <u>12</u><u>2001-02-03</u><u>abc</u>
  <n>5</n><n xsi:nil="true"/>
  <circle r="2" id="c1"/><square id="s1"/>
  <b><v>1.5</v></b><b xsi:type="m:ext"><v>2</v><w>  a  b </w></b>
  <all><y>true</y><x>2020-01-01</x></all>
  <ch><q>1 2 3</q><p>z</p></ch>
  <w o:at="1" m:g="1999"><o:x a="1"><o:y/></o:x><m:g>2002</m:g><s:k xmlns:s="urn:skip"><s:any/></s:k></w>
  <e/>
  <mx>a<i>1</i>b<i>2</i></mx>
</doc>`

// svValues: element default and fixed values (effectiveValue,
// checkFixedValueConstraint): an empty element takes its default, one with
// any child (a comment too) does not.
const svValues = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
 <xs:element name="r"><xs:complexType><xs:sequence>
   <xs:element name="i" type="xs:integer" default="5" minOccurs="0" maxOccurs="unbounded"/>
   <xs:element name="u" type="iord" default="2001-01-01" minOccurs="0" maxOccurs="unbounded"/>
   <xs:element name="f" type="xs:decimal" fixed="1.5" minOccurs="0" maxOccurs="unbounded"/>
   <xs:element name="s" default="7" minOccurs="0" maxOccurs="unbounded"><xs:complexType><xs:simpleContent>
     <xs:extension base="xs:int"><xs:attribute name="a" type="xs:string"/></xs:extension></xs:simpleContent></xs:complexType></xs:element>
   <xs:element name="m" fixed="ab" minOccurs="0" maxOccurs="unbounded"><xs:complexType mixed="true"><xs:sequence>
     <xs:element name="x" minOccurs="0"/></xs:sequence></xs:complexType></xs:element>
 </xs:sequence></xs:complexType></xs:element>
 <xs:simpleType name="iord"><xs:union memberTypes="xs:integer xs:date"/></xs:simpleType>
</xs:schema>`

const svValuesDoc = `<r><i/><i></i><i>7</i><u/><u>3</u><f/><f>1.50</f><s a="z"/><s>8</s><m/><m>ab</m><m>a<!--c-->b</m></r>`

// svOpenSchema: XSD 1.1 open content, interleaved and suffixed, on a sequence, an
// all group, mixed content, an empty model, and an empty type
// (defaultOpenContent appliesToEmpty).
const svOpenSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:o="urn:o">
 <xs:defaultOpenContent appliesToEmpty="true" mode="interleave"><xs:any namespace="urn:e" processContents="skip"/></xs:defaultOpenContent>
 <xs:element name="r"><xs:complexType><xs:sequence>
   <xs:element name="il" minOccurs="0" maxOccurs="unbounded"><xs:complexType>
     <xs:openContent><xs:any namespace="urn:o" processContents="lax"/></xs:openContent>
     <xs:sequence><xs:element name="a" type="xs:int"/><xs:element name="b" minOccurs="0" maxOccurs="2" type="xs:int"/></xs:sequence></xs:complexType></xs:element>
   <xs:element name="sf" minOccurs="0" maxOccurs="unbounded"><xs:complexType>
     <xs:openContent mode="suffix"><xs:any namespace="urn:o" processContents="lax"/></xs:openContent>
     <xs:sequence><xs:element name="a" type="xs:int"/></xs:sequence></xs:complexType></xs:element>
   <xs:element name="al" minOccurs="0" maxOccurs="unbounded"><xs:complexType>
     <xs:openContent><xs:any namespace="urn:o" processContents="lax"/></xs:openContent>
     <xs:all><xs:element name="a" type="xs:int"/><xs:element name="b" type="xs:int" minOccurs="0"/></xs:all></xs:complexType></xs:element>
   <xs:element name="mx" minOccurs="0" maxOccurs="unbounded"><xs:complexType mixed="true">
     <xs:openContent><xs:any namespace="urn:o" processContents="lax"/></xs:openContent>
     <xs:sequence><xs:element name="a" type="xs:int" minOccurs="0"/></xs:sequence></xs:complexType></xs:element>
   <xs:element name="none" minOccurs="0" maxOccurs="unbounded"><xs:complexType>
     <xs:openContent><xs:any namespace="urn:o" processContents="lax"/></xs:openContent><xs:sequence/></xs:complexType></xs:element>
   <xs:element name="emp" minOccurs="0" maxOccurs="unbounded"><xs:complexType><xs:attribute name="x"/></xs:complexType></xs:element>
 </xs:sequence></xs:complexType></xs:element>
 <xs:element name="g" type="xs:date" xmlns="" />
</xs:schema>`

const svOpenDoc = `<r xmlns:o="urn:o" xmlns:e="urn:e">
 <il><o:x/><a>1</a><o:y><z/></o:y><b>2</b><o:x>t</o:x><b>3</b></il><il><a>1</a></il>
 <sf><a>1</a><o:k/></sf>
 <al><o:x/><b>2</b><o:x/><a>1</a></al>
 <mx>t<o:x/>u<a>1</a>v</mx>
 <none><o:x/><o:y/></none>
 <emp x="1">text<e:q><e:w/></e:q></emp>
</r>`

// svNSDefaults: defaulted attributes in a namespace: a prefix in scope is
// used, and under XSD 1.1 one is declared when none is (namespace fixup),
// which a descendant's default then finds in scope.
const svNSDefaults = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:a="urn:a"
   targetNamespace="urn:a" elementFormDefault="qualified" attributeFormDefault="qualified">
 <xs:attribute name="g" type="xs:QName" default="a:q"/>
 <xs:element name="r"><xs:complexType><xs:sequence>
   <xs:element name="e" minOccurs="0" maxOccurs="unbounded"><xs:complexType><xs:sequence>
     <xs:element name="c" minOccurs="0"><xs:complexType><xs:attribute name="k" type="xs:int" default="3"/></xs:complexType></xs:element>
   </xs:sequence><xs:attribute name="foo" default="x"/><xs:attribute ref="a:g"/><xs:attribute name="plain" form="unqualified" default="p"/></xs:complexType></xs:element>
 </xs:sequence></xs:complexType></xs:element>
</xs:schema>`

const svNSDefaultsDoc = `<r xmlns="urn:a"><e><c/></e><e xmlns:p="urn:a"><c/></e><e xmlns:ns0="urn:other"/><x:e xmlns:x="urn:a" x:foo="y"/></r>`

// svKeys: identity constraints. A key on the document element with a
// keyref to it (values compared as values: decimals, QNames, a defaulted
// attribute), a unique scoped on a recursive element (the outer scope sees
// the inner's targets), a keyref whose key is declared only below it (the
// children's tables, merged: a value two siblings define is ambiguous), an
// element field, ".//" and "|" paths, a field under skipped content.
const svKeys = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:k="urn:k" targetNamespace="urn:k" elementFormDefault="qualified">
 <xs:element name="r"><xs:complexType><xs:sequence>
   <xs:element name="item" minOccurs="0" maxOccurs="unbounded"><xs:complexType><xs:sequence>
     <xs:element name="code" type="xs:decimal" minOccurs="0" maxOccurs="2"/></xs:sequence>
     <xs:attribute name="id" type="xs:decimal"/><xs:attribute name="q" type="xs:QName"/>
     <xs:attribute name="cat" type="xs:string" default="none"/></xs:complexType></xs:element>
   <xs:element name="ref" minOccurs="0" maxOccurs="unbounded"><xs:complexType>
     <xs:attribute name="to" type="xs:decimal"/><xs:attribute name="q" type="xs:QName"/></xs:complexType></xs:element>
   <xs:element ref="k:node" minOccurs="0" maxOccurs="unbounded"/>
   <xs:element name="grp" minOccurs="0" maxOccurs="unbounded"><xs:complexType><xs:sequence>
     <xs:element name="sub" minOccurs="0" maxOccurs="unbounded"><xs:complexType><xs:sequence>
       <xs:element name="x" minOccurs="0"><xs:complexType/></xs:element>
       <xs:element name="def" minOccurs="0" maxOccurs="unbounded"><xs:complexType><xs:attribute name="v" type="xs:int"/></xs:complexType></xs:element>
     </xs:sequence></xs:complexType>
       <xs:key name="D"><xs:selector xpath="k:def"/><xs:field xpath="@v"/></xs:key></xs:element>
     <xs:element name="use" minOccurs="0" maxOccurs="unbounded"><xs:complexType><xs:attribute name="v" type="xs:int"/></xs:complexType></xs:element>
     <xs:any namespace="urn:skip" processContents="skip" minOccurs="0"/>
   </xs:sequence></xs:complexType>
     <xs:keyref name="U" refer="k:D"><xs:selector xpath=".//k:use"/><xs:field xpath="@v"/></xs:keyref>
     <xs:unique name="S"><xs:selector xpath="k:sub"/><xs:field xpath="k:x"/></xs:unique></xs:element>
 </xs:sequence></xs:complexType>
  <xs:key name="K"><xs:selector xpath="k:item"/><xs:field xpath="@id"/></xs:key>
  <xs:unique name="Q"><xs:selector xpath="k:item"/><xs:field xpath="@q"/></xs:unique>
  <xs:unique name="C"><xs:selector xpath="k:item"/><xs:field xpath="k:code"/><xs:field xpath="@cat"/></xs:unique>
  <xs:keyref name="R" refer="k:K"><xs:selector xpath="k:ref"/><xs:field xpath="@to"/></xs:keyref>
  <xs:keyref name="RQ" refer="k:Q"><xs:selector xpath="k:ref"/><xs:field xpath="@q"/></xs:keyref>
 </xs:element>
 <xs:element name="node"><xs:complexType><xs:sequence>
   <xs:element ref="k:node" minOccurs="0" maxOccurs="unbounded"/>
   <xs:element name="leaf" minOccurs="0" maxOccurs="unbounded" type="xs:token"/>
   <xs:any namespace="urn:w" processContents="lax" minOccurs="0"/></xs:sequence>
   <xs:attribute name="n" type="xs:token"/></xs:complexType>
  <xs:unique name="N"><xs:selector xpath=".//k:leaf | k:node/k:leaf"/><xs:field xpath="."/></xs:unique>
  <xs:unique name="NN"><xs:selector xpath="k:node"/><xs:field xpath="@n"/></xs:unique>
 </xs:element>
<xs:complexType name="wrapT"><xs:sequence><xs:element ref="k:node"/></xs:sequence></xs:complexType>
</xs:schema>`

const svKeysDoc = `<r xmlns="urn:k" xmlns:p="urn:p" xmlns:q="urn:p">
 <item id="1" q="p:a"><code>1.0</code></item><item id="2.50" q="p:b"><code>2</code></item><item id="3" cat="x"><code>1</code></item>
 <ref to="1.00"/><ref to="2.5" q="q:a"/><ref/>
 <node><node n="x"><node n="y"/><leaf> b </leaf></node><leaf>a</leaf><leaf>c</leaf></node>
 <grp><sub><def v="1"/><def v="2"/></sub><sub><def v="2"/><def v="3"/></sub><use v="1"/><use v="3"/><s:x xmlns:s="urn:skip"><use v="9"/></s:x></grp>
</r>`

// svRun streams doc and returns the grounded document element, the
// validator's verdict, and whether it ran.
func svRun(t *testing.T, s *Schema, doc string, m xsdstream.Mode, ground bool) (*xdm.Node, error) {
	t.Helper()
	sv := xsdstream.Open(s, m)
	c := xdmstream.Open(strings.NewReader(doc), xdm.ParseOptions{})
	defer c.Close()
	c.Observe(func(n *xdm.Node, d int, end bool, _ uint64) { sv.Observe(n, d, end) })
	c.StripWhitespace(sv.Drops)
	var g *xdm.Node
	for {
		ev, err := c.Next()
		if err == io.EOF || err == nil && ev.Kind == xdmstream.EndDocument {
			break
		}
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		if ground && ev.Kind == xdmstream.StartElement && ev.Depth == 1 {
			if g, err = c.Ground(); err != nil {
				t.Fatalf("ground: %v", err)
			}
		}
	}
	return g, sv.Err()
}

// svTree is the tree's typed copy of doc's element (or document, for
// m.Doc), and its error, as the XSLT layer asks for it.
func svTree(t *testing.T, s *Schema, doc string, m xsdstream.Mode) (*xdm.Node, error) {
	t.Helper()
	tree, err := xdm.ParseString(doc, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	el := tree.Root.ChildElements()[0]
	switch {
	case m.Doc:
		c, err := s.ValidateCopy(tree.Root, ValidateOptions{})
		return c.ChildElements()[0], err
	case m.Root == xsdstream.Type:
		return s.ValidateAgainstTypeCopy(el, m.Type, ValidateOptions{})
	case m.Root == xsdstream.Lax:
		return s.ValidateElementLaxCopy(el, ValidateOptions{})
	}
	return s.ValidateCopy(el, ValidateOptions{})
}

// svSame compares a and b, nodes of the tree's copy and the stream's.
func svSame(a, b *xdm.Node, path string) string {
	if a.Kind() != b.Kind() || a.Name() != b.Name() {
		return fmt.Sprintf("%s: %v %v, stream %v %v", path, a.Kind(), a.Name(), b.Kind(), b.Name())
	}
	if ta, tb := xdm.TypingOf(a), xdm.TypingOf(b); ta != tb {
		return fmt.Sprintf("%s: typing %+v, stream %+v", path, ta, tb)
	}
	if (a.TypeEnv() == nil) != (b.TypeEnv() == nil) {
		return fmt.Sprintf("%s: type environment %v, stream %v", path, a.TypeEnv() != nil, b.TypeEnv() != nil)
	}
	if a.Kind() != xdm.KindElement {
		if a.Value() != b.Value() {
			return fmt.Sprintf("%s: value %q, stream %q", path, a.Value(), b.Value())
		}
		return ""
	}
	if na, nb := fmt.Sprint(a.InScopeNamespaces()), fmt.Sprint(b.InScopeNamespaces()); na != nb {
		return fmt.Sprintf("%s: namespaces %s, stream %s", path, na, nb)
	}
	var aa, ba []*xdm.Node
	for x := range a.Attrs() {
		aa = append(aa, x)
	}
	for x := range b.Attrs() {
		ba = append(ba, x)
	}
	if len(aa) != len(ba) {
		return fmt.Sprintf("%s: %d attributes, stream %d", path, len(aa), len(ba))
	}
	for i := range aa {
		if w := svSame(aa[i], ba[i], path+"/@"+aa[i].Name().Local); w != "" {
			return w
		}
	}
	ac, bc := a.FirstChild(), b.FirstChild()
	for i := 1; ac != nil || bc != nil; i++ {
		if ac == nil || bc == nil {
			return fmt.Sprintf("%s: children differ in number at %d", path, i)
		}
		if w := svSame(ac, bc, fmt.Sprintf("%s/%d", path, i)); w != "" {
			return w
		}
		ac, bc = ac.NextSibling(), bc.NextSibling()
	}
	return ""
}

func svLoad(t *testing.T, src string) *Schema {
	t.Helper()
	tree, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(tree.Root, "s.xsd", Options{Version: Version11})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStreamValidationMatchesTree(t *testing.T) {
	books, misc, values, open := svLoad(t, svBooks), svLoad(t, svMisc), svLoad(t, svValues), svLoad(t, svOpenSchema)
	nsd, keys := svLoad(t, svNSDefaults), svLoad(t, svKeys)
	nsTree, err := xdm.ParseString(svNSDefaults, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nsd10, err := Load(nsTree.Root, "s.xsd", Options{Version: Version10})
	if err != nil {
		t.Fatal(err)
	}
	strict, doc := xsdstream.Mode{Root: xsdstream.Strict}, xsdstream.Mode{Root: xsdstream.Strict, Doc: true}
	lax := xsdstream.Mode{Root: xsdstream.Lax}
	typed := xsdstream.Mode{Root: xsdstream.Type, Type: xdm.QName{Local: "BOOKLIST-type"}}
	rows := []struct {
		name string
		s    *Schema
		doc  string
		m    xsdstream.Mode
		bad  bool
	}{
		{"books strict", books, svBooksDoc, strict, false},
		{"books doc", books, svBooksDoc, doc, false},
		{"books type", books, svBooksDoc, typed, false},
		{"books lax", books, svBooksDoc, lax, false},
		{"books lax undeclared", books, `<X><BOOKS/></X>`, lax, false},
		{"misc strict", misc, svMiscDoc, strict, false},
		{"misc doc", misc, svMiscDoc, doc, false},
		{"xml:space keeps ignorable whitespace", misc, strings.Replace(strings.Replace(svMiscDoc, `<m:g>2002</m:g>`, ` <m:g>2002</m:g> `, 1),
			`<w o:at="1"`, `<w xml:space="preserve" o:at="1"`, 1), doc, false},
		{"empty with whitespace", misc, strings.Replace(svMiscDoc, `<e/>`, `<e> </e>`, 1), strict, false},
		{"misc root xsi:type", misc, `<r xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:m="urn:m" xsi:type="m:base"><v xmlns="urn:m">3</v></r>`, strict, false},
		{"values strict", values, svValuesDoc, strict, false},
		{"values doc", values, svValuesDoc, doc, false},
		{"keys", keys, svKeysDoc, strict, false},
		{"keys doc", keys, svKeysDoc, doc, false},
		{"keys: duplicate key", keys, strings.Replace(svKeysDoc, `id="3"`, `id="1.000"`, 1), strict, true},
		{"keys: key field missing", keys, strings.Replace(svKeysDoc, ` id="3"`, ``, 1), strict, true},
		{"keys: dangling keyref", keys, strings.Replace(svKeysDoc, `<ref to="1.00"/>`, `<ref to="4"/>`, 1), strict, true},
		{"keys: dangling QName keyref", keys, strings.Replace(svKeysDoc, `q="q:a"`, `q="p:c"`, 1), strict, true},
		{"keys: duplicate QName", keys, strings.Replace(svKeysDoc, `q="p:b"`, `q="q:a"`, 1), strict, true},
		{"keys: two fields equal", keys, strings.Replace(svKeysDoc, `<item id="3" cat="x"><code>1</code>`, `<item id="3"><code>1.0</code>`, 1), strict, true},
		// The typed copy keys a defaulted attribute by its lexical form and a
		// written one by its value (keyString without, and with, keyValues).
		{"keys: defaulted field and a written one", keys, strings.Replace(strings.Replace(svKeysDoc, `<item id="3" cat="x"><code>1</code>`, `<item id="3"><code>1</code>`, 1), `<item id="1" q="p:a">`, `<item id="1" q="p:a" cat="none">`, 1), strict, false},
		{"keys: recursive unique", keys, strings.Replace(svKeysDoc, `<leaf>c</leaf>`, `<leaf>a</leaf>`, 1), strict, true},
		{"keys: recursive unique inner", keys, strings.Replace(svKeysDoc, `<node n="y"/>`, `<node n="y"><leaf>b</leaf></node>`, 1), strict, true},
		{"keys: recursive unique, two scopes", keys, strings.Replace(svKeysDoc, `<node n="y"/>`, `<node n="y"><leaf>c</leaf></node>`, 1), strict, true},
		{"keys: keyref to an ambiguous key", keys, strings.Replace(svKeysDoc, `<use v="1"/>`, `<use v="2"/>`, 1), strict, true},
		{"keys: keyref under skip", keys, strings.Replace(svKeysDoc, `<use v="3"/>`, ``, 1), strict, false},
		{"keys: keyref with no key in scope", keys, strings.Replace(svKeysDoc, `<grp><sub><def v="1"/><def v="2"/></sub><sub><def v="2"/><def v="3"/></sub>`, `<grp>`, 1), strict, true},
		{"keys: key duplicate in a sub", keys, strings.Replace(svKeysDoc, `<def v="2"/><def v="3"/>`, `<def v="3"/><def v="3"/>`, 1), strict, true},
		{"keys: element field compared as a value", keys, strings.Replace(svKeysDoc, `<item id="3" cat="x"><code>1</code>`, `<item id="3"><code>1</code>`, 1), strict, true},
		// A nested scope reached through an element assessed by xsi:type
		// alone: its table does not come up (validateAgainstType's tables
		// are dropped), and the outer scope's walk does not go into it.
		{"keys: a nested scope's table lost", keys, strings.Replace(svKeysDoc, `<leaf>c</leaf></node>`,
			`<leaf>c</leaf><w:wrap xmlns:w="urn:w" xmlns:k="urn:k" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="k:wrapT"><node><leaf>a</leaf></node></w:wrap></node>`, 1), strict, false},
		{"keys: field selects two", keys, strings.Replace(svKeysDoc, `<code>1.0</code>`, `<code>1.0</code><code>2</code>`, 1), strict, true},
		{"keys: field selects a complex type", keys, strings.Replace(svKeysDoc, `<sub><def v="1"/>`, `<sub><x/><def v="1"/>`, 1), strict, true},
		{"keys: unique attribute", keys, strings.Replace(svKeysDoc, `<node n="y"/>`, `<node n="y"/><node n="y"/>`, 1), strict, true},
		{"ns defaults", nsd, svNSDefaultsDoc, strict, false},
		{"ns defaults doc", nsd, svNSDefaultsDoc, doc, false},
		{"ns defaults 1.0", nsd10, svNSDefaultsDoc, strict, false},
		{"open strict", open, svOpenDoc, strict, false},
		{"open doc", open, svOpenDoc, doc, false},
		// Invalid, each for one reason the tree finds.
		{"order", books, strings.Replace(svBooksDoc, "<TITLE>One</TITLE><PRICE cur=\"USD\">4.50</PRICE>", "<PRICE>4.50</PRICE><TITLE>One</TITLE>", 1), strict, true},
		{"required attribute", books, strings.Replace(svBooksDoc, ` CAT="H"`, "", 1), strict, true},
		{"bad decimal", books, strings.Replace(svBooksDoc, ">10<", ">ten<", 1), strict, true},
		{"bad enumeration", books, strings.Replace(svBooksDoc, `cur="USD"`, `cur="EUR"`, 1), strict, true},
		{"list length", books, strings.Replace(svBooksDoc, "1 2 3", "1 2", 1), strict, true},
		{"too many", books, strings.Replace(svBooksDoc, "<QTY>2</QTY>", "<QTY>2</QTY><QTY>3</QTY><QTY>4</QTY>", 1), strict, true},
		{"dangling IDREF", books, strings.Replace(svBooksDoc, `CAT="H"`, `CAT="Q"`, 1), strict, true},
		{"duplicate ID", books, strings.Replace(svBooksDoc, `CODE="H"`, `CODE="P"`, 1), strict, true},
		{"duplicate ID adjacent", books, strings.Replace(svBooksDoc, ` more <CATEGORY CODE="H" DESC="h"/>`, `<CATEGORY CODE="H" DESC="h"/><CATEGORY CODE="H" DESC="i"/>`, 1), strict, true},
		{"assertion", books, strings.Replace(svBooksDoc, "</BOOKS>", "<ITEM CAT='P'><TITLE/><PRICE>1</PRICE></ITEM><ITEM CAT='P'><TITLE/><PRICE>1</PRICE></ITEM></BOOKS>", 1), strict, true},
		{"element-only text", books, strings.Replace(svBooksDoc, "<BOOKS>", "<BOOKS>x", 1), strict, true},
		{"undeclared root", books, `<NOPE/>`, strict, true},
		{"type mismatch", books, `<BOOKLIST><CATEGORIES DESC="d"/></BOOKLIST>`, typed, true},
		{"nil with content", misc, strings.Replace(svMiscDoc, `<n xsi:nil="true"/>`, `<n xsi:nil="true">1</n>`, 1), strict, true},
		{"abstract", misc, strings.Replace(svMiscDoc, `<square id="s1"/>`, `<shape/>`, 1), strict, true},
		{"union", misc, strings.Replace(svMiscDoc, `<u>12</u>`, `<u><x/></u>`, 1), strict, true},
		{"all missing", misc, strings.Replace(svMiscDoc, `<x>2020-01-01</x>`, ``, 1), strict, true},
		{"all twice", misc, strings.Replace(svMiscDoc, `<x>2020-01-01</x>`, `<x>2020-01-01</x><x>2020-01-01</x>`, 1), strict, true},
		{"choice", misc, strings.Replace(svMiscDoc, `<p>z</p>`, `<p>z</p><p>z</p>`, 1), strict, true},
		{"lax invalid", misc, strings.Replace(svMiscDoc, `<m:g>2002</m:g>`, `<m:g>never</m:g>`, 1), strict, true},
		{"empty with text", misc, strings.Replace(svMiscDoc, `<e/>`, `<e>x</e>`, 1), strict, true},
		{"default not taken: a comment", values, strings.Replace(svValuesDoc, `<i></i>`, `<i><!--c--></i>`, 1), strict, true},
		{"fixed simple", values, strings.Replace(svValuesDoc, `<f>1.50</f>`, `<f>2</f>`, 1), strict, true},
		{"fixed mixed", values, strings.Replace(svValuesDoc, `<m>ab</m>`, `<m>ba</m>`, 1), strict, true},
		{"fixed mixed child", values, strings.Replace(svValuesDoc, `<m>ab</m>`, `<m>ab<x/></m>`, 1), strict, true},
		{"fixed mixed space", values, strings.Replace(svValuesDoc, `<m>ab</m>`, `<m> </m>`, 1), strict, true},
		{"open: name outside the wildcard", open, strings.Replace(svOpenDoc, `<b>2</b><o:x>t</o:x>`, `<b>2</b><q/>`, 1), strict, true},
		{"open: suffix before the end", open, strings.Replace(svOpenDoc, `<sf><a>1</a><o:k/></sf>`, `<sf><o:k/><a>1</a></sf>`, 1), strict, true},
		{"open: model after the suffix", open, strings.Replace(svOpenDoc, `<o:k/></sf>`, `<o:k/><a>2</a></sf>`, 1), strict, true},
		{"open: all group", open, strings.Replace(svOpenDoc, `<b>2</b><o:x/><a>1</a>`, `<b>2</b><o:x/><a>1</a><a>2</a>`, 1), strict, true},
		{"open: empty model", open, strings.Replace(svOpenDoc, `<none><o:x/>`, `<none><x/>`, 1), strict, true},
		{"open: empty type", open, strings.Replace(svOpenDoc, `<e:q><e:w/></e:q>`, `<q/>`, 1), strict, true},
		{"open: still complete", open, strings.Replace(svOpenDoc, `<il><a>1</a></il>`, `<il><o:x/></il>`, 1), strict, true},
		{"xsi:type not derived", misc, strings.Replace(svMiscDoc, `xsi:type="m:ext"`, `xsi:type="m:shapeT"`, 1), strict, true},
	}
	for _, r := range rows {
		want, werr := svTree(t, r.s, r.doc, r.m)
		got, gerr := svRun(t, r.s, r.doc, r.m, true)
		// Read without grounding, the parse reuses the records of what
		// it has dropped: the verdict must not change.
		if _, err := svRun(t, r.s, r.doc, r.m, false); (err == nil) != (werr == nil) {
			t.Errorf("%s (not grounded): tree error %v, stream error %v", r.name, werr, err)
		}
		if (werr == nil) != (gerr == nil) {
			t.Errorf("%s: tree error %v, stream error %v", r.name, werr, gerr)
			continue
		}
		if r.bad != (werr != nil) {
			t.Errorf("%s: tree error %v, want invalid %v", r.name, werr, r.bad)
		}
		if werr != nil {
			continue // the tree answers an invalid document
		}
		if w := svSame(want, got, ""); w != "" {
			t.Errorf("%s: %s", r.name, w)
		}
	}
}

func TestStreamValidationRefusals(t *testing.T) {
	for _, r := range []struct{ name, schema, want string }{
		{"key streams", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r"><xs:complexType><xs:sequence>
			<xs:element name="k" maxOccurs="unbounded"><xs:complexType><xs:attribute name="id"/></xs:complexType></xs:element></xs:sequence></xs:complexType>
			<xs:key name="K"><xs:selector xpath="k"/><xs:field xpath="@id"/></xs:key>
			<xs:keyref name="R" refer="K"><xs:selector xpath="k"/><xs:field xpath="@id"/></xs:keyref></xs:element></xs:schema>`,
			""},
		{"element default", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r" type="xs:int" default="1"/></xs:schema>`, ""},
		{"open content", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r"><xs:complexType>
			<xs:openContent><xs:any/></xs:openContent><xs:sequence/></xs:complexType></xs:element></xs:schema>`, ""},
		{"bounded readings", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r"><xs:complexType>
			<xs:sequence maxOccurs="100"><xs:element name="a" maxOccurs="100"/></xs:sequence></xs:complexType></xs:element></xs:schema>`,
			"may hold more than 4096 count readings"},
		{"bounded readings, few", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r"><xs:complexType>
			<xs:sequence maxOccurs="60"><xs:element name="a" maxOccurs="60"/><xs:element name="b" maxOccurs="unbounded"/></xs:sequence></xs:complexType></xs:element></xs:schema>`, ""},
		{"plain", svBooks, ""},
	} {
		got := xsdstream.Refusal(svLoad(t, r.schema))
		if !strings.Contains(got, r.want) || r.want == "" && got != "" {
			t.Errorf("%s: refusal %q, want %q", r.name, got, r.want)
		}
	}
}
