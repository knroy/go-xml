// Package xslts runs the W3C XSLT test suite against this engine, filtered to
// the tests an XSLT 2.0 processor is expected to pass.
//
// There is no maintained XSLT 2.0 suite. The original XSLTS was frozen at
// 1.1.0, was distributed from w3.org behind a click-through licence rather
// than a repository, and has had no maintenance since XSLT 2.0 reached
// Recommendation in 2007. What replaced it is the XSLT 3.0 suite, which
// carried most of those tests forward and records a version dependency on
// each one — so a 2.0-conformance run is a filtered run of the 3.0 suite
// rather than a separate corpus.
//
// The suite is not vendored: it is ~470MB and belongs to the W3C. Point
// GOXSLT_XSLTS at a checkout of https://github.com/w3c/xslt30-test and the
// tests in this package run; without it they skip.
//
//	git clone --depth 1 https://github.com/w3c/xslt30-test.git
//	GOXSLT_XSLTS=xslt30-test go test ./xslts/
package xslts

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// decode unmarshals a suite file.
//
// The catalogs are UTF-8 but many carry a byte-order mark, which Go's decoder
// treats as content and refuses. Stripping it is safe: a BOM on UTF-8 carries
// no information.
func decode(data []byte, v any) error {
	data = stripBOM(data)
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		switch strings.ToLower(charset) {
		case "us-ascii", "ascii", "iso-8859-1", "latin1", "utf-8", "":
			// All are byte-compatible with UTF-8 over the range the catalogs
			// use; a document with non-ASCII content declares UTF-8.
			return input, nil
		}
		return nil, fmt.Errorf("unsupported charset %q", charset)
	}
	return dec.Decode(v)
}

func stripBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

// Catalog is the top-level catalog.xml.
type Catalog struct {
	XMLName      xml.Name      `xml:"catalog"`
	TestSets     []TestSetRef  `xml:"test-set"`
	Environments []Environment `xml:"environment"`
}

type TestSetRef struct {
	Name string `xml:"name,attr"`
	File string `xml:"file,attr"`
}

// TestSet is one test-set file.
type TestSet struct {
	XMLName      xml.Name      `xml:"test-set"`
	Name         string        `xml:"name,attr"`
	Environments []Environment `xml:"environment"`
	// Dependencies declared here apply to every case in the set unless the
	// case states its own. Seven thousand cases inherit rather than declare,
	// so a runner that reads only the case-level ones admits tests that need
	// XSLT 3.0 and reports them as failures of this engine.
	Dependencies Dependencies `xml:"dependencies"`
	Cases        []TestCase   `xml:"test-case"`

	// Dir is the test-set file's directory, relative to the suite root.
	// Stylesheet and source paths are relative to it rather than to the root.
	Dir string `xml:"-"`

	// Path is the test-set file itself. An inline <source><content> is a part
	// of this file, so this -- not a synthesised name in Dir -- is the base
	// URI the XML spec gives the element the content becomes.
	Path string `xml:"-"`
}

// Environment supplies the source documents, parameters and settings a test
// runs against.
type Environment struct {
	Name    string   `xml:"name,attr"`
	Ref     string   `xml:"ref,attr"`
	Sources []Source `xml:"source"`
	Params  []Param  `xml:"param"`
	Schemas []Schema `xml:"schema"`
	// Collections are the document sets fn:collection resolves. An
	// environment declaring one is what makes collection() answer at all:
	// without it the engine refuses, correctly, because a collection URI it
	// was never told about is not a document set it can invent.
	Collections []Collection `xml:"collection"`
	// Stylesheets declared here are shared by every case referencing the
	// environment. A test-set built that way states no stylesheet on its
	// cases at all, so a runner reading only the case-level ones finds none
	// and reports the whole set as unrunnable.
	Stylesheets []StylesheetRef `xml:"stylesheet"`
}

// Source is one input document, either named by file or given inline.
//
// role="." marks the principal input — the document the transform starts on.
// Any other role names a secondary document reachable through fn:doc.
type Source struct {
	Role    string `xml:"role,attr"`
	File    string `xml:"file,attr"`
	URI     string `xml:"uri,attr"`
	Content string `xml:"content"`
	// Select is an XPath expression that picks the initial context node out
	// of the loaded document -- or, with no file and no inline content,
	// constructs it outright (parse-xml('<root/>')). The initial context
	// item need not be a document node, and three cases in the suite name an
	// element or a text node this way.
	Select string `xml:"select,attr"`
	// Validation is "strict", "lax" or "skip". A source declared strict is
	// meant to reach the transform carrying type annotations from the
	// environment's schema, which is what makes "instance of my:type"
	// answer true for a value read out of it.
	Validation string `xml:"validation,attr"`
}

type Param struct {
	Name   string `xml:"name,attr"`
	Select string `xml:"select,attr"`
	As     string `xml:"as,attr"`
	Static string `xml:"static,attr"`
}

// Collection is a named set of documents fn:collection returns.
//
// An empty uri names the *default* collection — the one collection() with no
// argument returns — which is why the attribute is not treated as missing when
// it is present and blank.
type Collection struct {
	URI     string   `xml:"uri,attr"`
	Sources []Source `xml:"source"`
}

type Schema struct {
	URI  string `xml:"uri,attr"`
	File string `xml:"file,attr"`
}

// Dependencies is the set of conditions a test needs.
//
// Only the ones that decide whether a test is in scope here are modelled.
// Everything else — the many feature and implementation-defined flags — is
// read as a raw name so that an unrecognised one excludes the test rather
// than being silently ignored.
type Dependencies struct {
	Specs    []Spec    `xml:"spec"`
	Features []Feature `xml:"feature"`
	Others   []Other   `xml:",any"`
}

type Spec struct {
	Value string `xml:"value,attr"`
}

type Feature struct {
	Value     string `xml:"value,attr"`
	Satisfied string `xml:"satisfied,attr"`
}

// Other is any dependency element this runner does not model.
type Other struct {
	XMLName   xml.Name
	Value     string `xml:"value,attr"`
	Satisfied string `xml:"satisfied,attr"`
}

// TestCase is one test.
type TestCase struct {
	Name         string        `xml:"name,attr"`
	Description  string        `xml:"description"`
	Environments []Environment `xml:"environment"`
	Dependencies Dependencies  `xml:"dependencies"`
	Test         Test          `xml:"test"`
	Result       Result        `xml:"result"`
}

// Test says what to run.
type Test struct {
	Stylesheets     []StylesheetRef `xml:"stylesheet"`
	Packages        []PackageRef    `xml:"package"`
	InitialTemplate *NamedThing     `xml:"initial-template"`
	InitialMode     *NamedThing     `xml:"initial-mode"`
	InitialFunction *NamedThing     `xml:"initial-function"`
	Params          []Param         `xml:"param"`
	Output          *OutputRef      `xml:"output"`
	// PostureAndSweep marks a streamability test, which is XSLT 3.0 only.
	PostureAndSweep *struct{} `xml:"posture-and-sweep"`
	XPath           string    `xml:"xpath"`
}

type StylesheetRef struct {
	File    string `xml:"file,attr"`
	Role    string `xml:"role,attr"`
	Content string `xml:",innerxml"`
}

type PackageRef struct {
	File string `xml:"file,attr"`
	Role string `xml:"role,attr"`
}

type NamedThing struct {
	// Name is the lexical QName exactly as the catalog wrote it.
	Name string `xml:"name,attr"`
	// URI is the namespace the catalog's own declarations bind Name's prefix
	// to. It is filled in by resolveInitialTemplateNames rather than by
	// encoding/xml, which never resolves a QName held in an attribute value.
	// Empty means the name has no prefix, or the prefix was unbound.
	URI string `xml:"-"`
}

type OutputRef struct {
	File      string `xml:"file,attr"`
	Serialize string `xml:"serialize,attr"`
}

// Result is the assertion tree, kept as raw XML.
//
// The nesting is arbitrary — all-of and any-of contain further assertions to
// any depth — so it is walked with the token stream rather than described
// with struct tags.
type Result struct {
	Inner string `xml:",innerxml"`
	// NS holds the prefix-to-URI bindings in scope at the <result> element
	// itself. encoding/xml's innerxml keeps only what is *inside* the
	// element, so a declaration written on <result> — which is where the
	// suite puts the XHTML binding an assertion's XPath needs — is not in
	// Inner and cannot be recovered by parsing it. Filled in by
	// resolveResultNamespaces.
	NS map[string]string `xml:"-"`
}

// resolveInitialTemplateNames fills in NamedThing.URI for every
// <initial-template> in a test-set.
//
// The catalog writes the initial template as a lexical QName whose prefix is
// bound by the catalog itself, and that binding is not always on the element:
//
//	<test-set xmlns:xsl="http://www.w3.org/1999/XSL/Transform" ...>
//	  ...<initial-template name="xsl:initial-template"/>
//	<initial-template xmlns:my="www.example.com/myTemp" name="my:temp"/>
//
// encoding/xml resolves the namespace of element and attribute *names* but
// never of a QName that appears as an attribute *value*, and an UnmarshalXML
// method sees only its own start element, not its ancestors' declarations. So
// the bindings are recovered here with a second token pass that maintains the
// in-scope prefix stack, keyed by the enclosing test-case name.
//
// Without this the lexical prefix reaches the engine and is resolved against
// the *stylesheet's* namespace declarations instead. call-template-0105 is the
// test that catches it: the catalog binds my: to "www.example.com/myTemp" and
// names my:temp, which the stylesheet does not declare, so XTDE0040 is due —
// but the stylesheet binds the same prefix my: to "http://www.othertemp.com"
// and does declare my:temp there, so the misresolved lookup finds a template
// the catalog never named and the transform wrongly succeeds.
func resolveInitialTemplateNames(data []byte, set *TestSet) error {
	found, err := scanInitialTemplateNames(data)
	if err != nil {
		return err
	}
	for i := range set.Cases {
		nt := set.Cases[i].Test.InitialTemplate
		if nt == nil {
			continue
		}
		if uri, ok := found[set.Cases[i].Name]; ok {
			nt.URI = uri
		}
	}
	return nil
}

// scanInitialTemplateNames maps test-case name -> resolved namespace URI of
// the prefix on that case's <initial-template name="..."> QName. A case whose
// name has no prefix, or whose prefix is unbound, is absent from the map.
func scanInitialTemplateNames(data []byte) (map[string]string, error) {
	dec := xml.NewDecoder(strings.NewReader(string(stripBOM(data))))
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	out := map[string]string{}
	// scopes[i] holds the declarations made by the element at depth i; a
	// lookup walks it from the top down so that an inner binding wins.
	var scopes []map[string]string
	caseName := ""
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			decls := map[string]string{}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" {
					decls[a.Name.Local] = a.Value
				}
			}
			scopes = append(scopes, decls)
			switch t.Name.Local {
			case "test-case":
				caseName = attrValue(t, "name")
			case "initial-template":
				name := attrValue(t, "name")
				if prefix, _, hasPrefix := strings.Cut(name, ":"); hasPrefix &&
					caseName != "" {
					if uri := lookupPrefix(scopes, prefix); uri != "" {
						out[caseName] = uri
					}
				}
			}
		case xml.EndElement:
			if len(scopes) > 0 {
				scopes = scopes[:len(scopes)-1]
			}
			if t.Name.Local == "test-case" {
				caseName = ""
			}
		}
	}
	return out, nil
}

// resolveResultNamespaces fills in Result.NS for every test-case, with the
// namespace bindings in scope at that case's <result> element.
//
// The suite writes them there rather than on the assertion:
//
//	<result xmlns:h="http://www.w3.org/1999/xhtml">
//	   <all-of>
//	      <assert-result-document uri="...">
//	         <assert>/h:html/h:head/h:title/... = "Index of names"</assert>
//
// innerxml starts after the <result> start tag, so the xmlns:h is gone by the
// time ParseAssert runs and the XPath fails with XPST0081 on an unbound
// prefix — a failure of the assertion, not of the transform.
func resolveResultNamespaces(data []byte, set *TestSet) error {
	found, err := scanResultNamespaces(data)
	if err != nil {
		return err
	}
	for i := range set.Cases {
		if ns, ok := found[set.Cases[i].Name]; ok {
			set.Cases[i].Result.NS = ns
		}
	}
	return nil
}

// scanResultNamespaces maps test-case name -> the prefixes in scope at that
// case's <result>. A case whose result inherits no declaration is absent.
func scanResultNamespaces(data []byte) (map[string]map[string]string, error) {
	dec := xml.NewDecoder(strings.NewReader(string(stripBOM(data))))
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	out := map[string]map[string]string{}
	var scopes []map[string]string
	caseName := ""
	depth := 0
	resultDepth := -1
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			decls := map[string]string{}
			for _, a := range t.Attr {
				// Prefixed declarations only. The catalog's own default
				// namespace is on its root element and means nothing to an
				// assertion's XPath, which addresses the *result* tree;
				// seeding it as the default element namespace makes every
				// unprefixed step in every assertion in the suite fail to
				// match.
				if a.Name.Space == "xmlns" {
					decls[a.Name.Local] = a.Value
				}
			}
			scopes = append(scopes, decls)
			depth++
			if t.Name.Local == "test-case" {
				caseName = attrValue(t, "name")
			}
			// Only the <result> that is the test-case's own child; an
			// "result" appearing deeper inside an assertion's expected XML
			// is not one.
			if t.Name.Local == "result" && caseName != "" && resultDepth < 0 {
				resultDepth = depth
				flat := map[string]string{}
				for _, sc := range scopes {
					for k, v := range sc {
						flat[k] = v
					}
				}
				if len(flat) > 0 {
					out[caseName] = flat
				}
			}
		case xml.EndElement:
			if resultDepth == depth {
				resultDepth = -1
			}
			if len(scopes) > 0 {
				scopes = scopes[:len(scopes)-1]
			}
			depth--
			if t.Name.Local == "test-case" {
				caseName = ""
			}
		}
	}
	return out, nil
}

func attrValue(e xml.StartElement, local string) string {
	for _, a := range e.Attr {
		if a.Name.Local == local && a.Name.Space == "" {
			return a.Value
		}
	}
	return ""
}

func lookupPrefix(scopes []map[string]string, prefix string) string {
	for i := len(scopes) - 1; i >= 0; i-- {
		if uri, ok := scopes[i][prefix]; ok {
			return uri
		}
	}
	return ""
}
