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
}

// Environment supplies the source documents, parameters and settings a test
// runs against.
type Environment struct {
	Name    string   `xml:"name,attr"`
	Ref     string   `xml:"ref,attr"`
	Sources []Source `xml:"source"`
	Params  []Param  `xml:"param"`
	Schemas []Schema `xml:"schema"`
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
}

type Param struct {
	Name   string `xml:"name,attr"`
	Select string `xml:"select,attr"`
	As     string `xml:"as,attr"`
	Static string `xml:"static,attr"`
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
	Name string `xml:"name,attr"`
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
}
