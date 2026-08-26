// Package qt3 runs the W3C QT3 (FOTS) test suite against this engine.
//
// The suite is not vendored: it is ~78MB and belongs to the W3C. Point
// GOXSLT_QT3 at a checkout of https://github.com/w3c/qt3tests and the tests
// in this package run; without it they skip. That keeps `go test ./...` fast
// and dependency-free for everyone else while making the conformance run
// reproducible for anyone who wants it.
package qt3

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// decode unmarshals a suite file.
//
// The suite declares us-ascii and iso-8859-1 encodings, which Go's decoder
// refuses without a CharsetReader. Both are byte-compatible with UTF-8 for the
// ASCII range the catalogs actually use, and the tests that carry non-ASCII
// data use UTF-8, so passing the bytes through is correct here rather than a
// shortcut — but an unknown encoding is refused rather than mangled.
func decode(data []byte, v any) error {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		switch strings.ToLower(charset) {
		case "us-ascii", "ascii", "iso-8859-1", "latin1", "utf-8":
			return input, nil
		}
		return nil, fmt.Errorf("unsupported charset %q", charset)
	}
	return dec.Decode(v)
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

// TestSet is one test-set file, e.g. fn/abs.xml.
type TestSet struct {
	XMLName      xml.Name      `xml:"test-set"`
	Name         string        `xml:"name,attr"`
	Environments []Environment `xml:"environment"`
	Dependencies []Dependency  `xml:"dependency"`
	Cases        []TestCase    `xml:"test-case"`

	// Dir is the test-set file's directory, relative to the suite root.
	//
	// Source paths are relative to the test-set file, not to the root:
	// fn/collection.xml names "../docs/bib.xml". Joining those against the
	// root walks above it and the file is not found, so the directory has to
	// travel with the parsed set.
	Dir string `xml:"-"`
}

type Environment struct {
	Name       string      `xml:"name,attr"`
	Ref        string      `xml:"ref,attr"`
	Sources    []Source    `xml:"source"`
	Params     []Param     `xml:"param"`
	Namespaces []Namespace `xml:"namespace"`
	Schemas    []Schema    `xml:"schema"`
	// Collations and a declared default collation appear on a few
	// environments; a test needing a non-codepoint one is unsupported here.
	Collations []Collation `xml:"collation"`
	// StaticBaseURI supplies the base URI of the expression itself, which is
	// what fn:static-base-uri returns and what fn:resolve-uri resolves
	// against.
	StaticBaseURI []StaticBaseURI `xml:"static-base-uri"`
	// Collections are the document sets fn:collection returns.
	Collections []Collection `xml:"collection"`
	// Resources are the non-XML files fn:unparsed-text reads, mapping the URI
	// a case names to the file in the checkout that holds it.
	Resources []Resource `xml:"resource"`
	// DecimalFormats are the decimal formats fn:format-number reads. Only the
	// unnamed (default) one is used by these cases.
	DecimalFormats []DecimalFormatDecl `xml:"decimal-format"`
}

// DecimalFormatDecl is one <decimal-format> declaration. Every attribute is
// optional and defaults to the standard symbol, so each is a pointer-free
// string tested for emptiness rather than a value with its own default.
type DecimalFormatDecl struct {
	// Name is the lexical QName the declaration carries. Its prefix may be
	// declared on the decimal-format element itself, so the namespace it
	// binds to is recovered from Attrs rather than from the environment.
	Name              string     `xml:"name,attr"`
	Attrs             []xml.Attr `xml:",any,attr"`
	DecimalSeparator  string     `xml:"decimal-separator,attr"`
	GroupingSeparator string     `xml:"grouping-separator,attr"`
	Percent           string     `xml:"percent,attr"`
	PerMille          string     `xml:"per-mille,attr"`
	ZeroDigit         string     `xml:"zero-digit,attr"`
	Digit             string     `xml:"digit,attr"`
	PatternSeparator  string     `xml:"pattern-separator,attr"`
	MinusSign         string     `xml:"minus-sign,attr"`
	Infinity          string     `xml:"infinity,attr"`
	NaN               string     `xml:"NaN,attr"`
}

// Resource is one <resource> declaration: a file in the checkout published
// under the URI a test case uses to reach it.
type Resource struct {
	File      string `xml:"file,attr"`
	URI       string `xml:"uri,attr"`
	MediaType string `xml:"media-type,attr"`
	Encoding  string `xml:"encoding,attr"`
}

type StaticBaseURI struct {
	URI string `xml:"uri,attr"`
}

type Source struct {
	Role string `xml:"role,attr"`
	File string `xml:"file,attr"`
	URI  string `xml:"uri,attr"`
}

type Param struct {
	Name   string `xml:"name,attr"`
	Select string `xml:"select,attr"`
	As     string `xml:"as,attr"`
}

type Namespace struct {
	Prefix string `xml:"prefix,attr"`
	URI    string `xml:"uri,attr"`
}

type Schema struct {
	URI  string `xml:"uri,attr"`
	File string `xml:"file,attr"`
}

type Collation struct {
	URI     string `xml:"uri,attr"`
	Default string `xml:"default,attr"`
}

type Dependency struct {
	Type      string `xml:"type,attr"`
	Value     string `xml:"value,attr"`
	Satisfied string `xml:"satisfied,attr"`
}

type TestCase struct {
	Name         string        `xml:"name,attr"`
	Description  string        `xml:"description"`
	Environments []Environment `xml:"environment"`
	Dependencies []Dependency  `xml:"dependency"`
	Test         string        `xml:"test"`
	Modules      []struct {
		URI  string `xml:"uri,attr"`
		File string `xml:"file,attr"`
	} `xml:"module"`
	Result Result `xml:"result"`
}

// Result is the assertion tree, kept as raw XML and parsed by ParseAssert.
//
// The nesting is arbitrary — all-of and any-of contain further assertions to
// any depth — and struct tags cannot express that, so it is walked with the
// token stream instead.
type Result struct {
	Raw []byte `xml:",innerxml"`
}

// Assertion is one node of the result tree.
type Assertion struct {
	// Kind is the element name: "assert-eq", "all-of", "error", and so on.
	Kind string
	// Value is the character data, which for most kinds is the expected value.
	Value string
	// Code is the @code of an <error>, and @type of an assert-type.
	Code string
	// File is the @file of an assert-xml whose expected value is held in a
	// separate document rather than inline. Without it the comparison ran
	// against an empty string, which reported every such case as a mismatch
	// against "".
	File string
	// Children are the operands of all-of / any-of / not.
	Children []Assertion
}

// ParseAssert parses the raw result XML into an assertion tree.
func ParseAssert(raw []byte) (Assertion, error) {
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	root := Assertion{Kind: "all-of"}
	stack := []*Assertion{&root}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return root, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			a := Assertion{Kind: t.Name.Local}
			for _, at := range t.Attr {
				switch at.Name.Local {
				case "code", "type", "value":
					a.Code = at.Value
				case "file":
					a.File = at.Value
				}
			}
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, a)
			stack = append(stack, &parent.Children[len(parent.Children)-1])
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			cur := stack[len(stack)-1]
			cur.Value += string(t)
		}
	}
	// A result with exactly one assertion needs no synthetic all-of wrapper.
	if len(root.Children) == 1 {
		return root.Children[0], nil
	}
	return root, nil
}

// SuiteRoot returns the checkout directory, or "" when the suite is absent.
func SuiteRoot() string {
	root := os.Getenv("GOXSLT_QT3")
	if root == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(root, "catalog.xml")); err != nil {
		return ""
	}
	return root
}

// LoadCatalog reads catalog.xml from the suite root.
func LoadCatalog(root string) (*Catalog, error) {
	data, err := os.ReadFile(filepath.Join(root, "catalog.xml"))
	if err != nil {
		return nil, err
	}
	var c Catalog
	if err := decode(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// LoadTestSet reads one test-set file named relative to the suite root.
func LoadTestSet(root, file string) (*TestSet, error) {
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return nil, err
	}
	var ts TestSet
	if err := decode(data, &ts); err != nil {
		return nil, err
	}
	ts.Dir = filepath.Dir(file)
	return &ts, nil
}

// Collection is a named set of documents an environment supplies to
// fn:collection.
//
// The sources carry the same role/file/uri attributes as a context source,
// but role is unused: membership in the collection is the point, not the name
// it is bound to.
type Collection struct {
	URI     string   `xml:"uri,attr"`
	Sources []Source `xml:"source"`
}
