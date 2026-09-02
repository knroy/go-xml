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

	// File is the test-set file itself, relative to the suite root.
	//
	// Dir is not enough for the one thing this is for: §2.1.2 defaults the
	// static base URI to the URI of the resource holding the expression, and
	// K2-BaseURIProlog-5 declares the relative base URI "" and then requires
	// fn:static-base-uri() to end with "prod/BaseURIDecl.xml" -- the file, not
	// the directory that contains it.
	File string `xml:"-"`
}

// ContextItem is an environment's "context-item", whose select attribute is
// an XPath expression evaluated to produce the item.
type ContextItem struct {
	Select string `xml:"select,attr"`
}

type Environment struct {
	Name    string   `xml:"name,attr"`
	Ref     string   `xml:"ref,attr"`
	Sources []Source `xml:"source"`
	Params  []Param  `xml:"param"`
	// ContextItem supplies the context item as an expression to evaluate,
	// rather than as a document to load the way a "." source does. A handful
	// of environments set an atomic or an array this way — "declare context
	// item as xs:integer external" against a supplied 'London' is how
	// contextDecl-020 asks for XPTY0004 — and without it the item is absent
	// and the case gets XPDY0002 instead.
	ContextItem []ContextItem `xml:"context-item"`
	Namespaces  []Namespace   `xml:"namespace"`
	Schemas     []Schema      `xml:"schema"`
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
	ExponentSeparator string     `xml:"exponent-separator,attr"`
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
	// A param's name may carry a prefix, and the binding for it is declared
	// on the param element rather than on the environment — extvardeclwithtype-24
	// writes xmlns:test on the <param> and names "test:x". Capturing the
	// element's own attributes is the only way to see it, since the
	// environment's <namespace> children do not mention it.
	Attrs []xml.Attr `xml:",any,attr"`
}

// nsFor resolves the param's prefix against the xmlns attributes written on
// the param element itself, returning the empty string when there is none.
func (p Param) nsFor(prefix string) string {
	for _, a := range p.Attrs {
		if a.Name.Space == "xmlns" && a.Name.Local == prefix {
			return a.Value
		}
	}
	return ""
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
	// Test carries the <test> element both ways it can be written: the query
	// inline as character data, or a "file" attribute naming a file beside
	// the test set that holds it. LoadTestSet reads the file form in, so
	// everything downstream reads Test.Query whichever way the case was
	// written.
	//
	// One field rather than two because the decoder refuses to map two struct
	// fields onto the same element name.
	Test struct {
		Query string `xml:",chardata"`
		File  string `xml:"file,attr"`
	} `xml:"test"`
	Modules []struct {
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
	// Flags is the @flags of a serialization-matches: the regular expression
	// flags fn:matches would be given. It is a field of its own rather than
	// another use of Code because a serialization-matches carries both — the
	// suite writes flags="q" beside no code at all, and folding them together
	// would make a literal-match assertion look like an error assertion.
	Flags string
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
				case "flags":
					a.Flags = at.Value
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
	ts.File = file
	// Pull in the cases whose query lives in its own file. Without this they
	// ran as the empty query, which fails every assertion the case makes and
	// counted as an engine failure rather than a harness gap — the
	// whitespace-sensitive constructor cases are written this way precisely
	// because their exact bytes matter, and inlining them in XML would not
	// preserve those bytes.
	for i := range ts.Cases {
		if ts.Cases[i].Test.File == "" {
			continue
		}
		q, err := os.ReadFile(
			filepath.Join(root, ts.Dir, ts.Cases[i].Test.File))
		if err != nil {
			return nil, fmt.Errorf("test-case %s: %v", ts.Cases[i].Name, err)
		}
		ts.Cases[i].Test.Query = string(q)
	}
	return &ts, nil
}

// Collection is a named set of documents an environment supplies to
// fn:collection.
//
// The sources carry the same role/file/uri attributes as a context source,
// but role is unused: membership in the collection is the point, not the name
// it is bound to.
// A collection may instead name its members by a query rather than by files.
// The catalog schema allows <collection> to hold either <source> children or
// <query> children, and the query form is how the suite builds collections
// whose members are not documents at all: "integer-collection" is
// <query>1 to 10</query> and "atomic-collection" is <query>(1, "hello", 1e0)
// </query>. The engine's CollectionResolver already returns an xdm.Sequence
// rather than a node list precisely so that such a collection can be
// expressed, so honouring <query> here costs nothing on the engine side.
type Collection struct {
	URI     string            `xml:"uri,attr"`
	Sources []Source          `xml:"source"`
	Queries []CollectionQuery `xml:"query"`
}

// CollectionQuery is one <query> child of a <collection>: an XQuery expression
// whose result is contributed to the collection.
//
// The optional uri attribute is the document URI of the item the query
// produces, which UseCaseR31's "users-json" collection uses so that a
// collection member can be named. It is recorded for completeness; a member
// that is not a node has no document URI to stamp.
type CollectionQuery struct {
	URI  string `xml:"uri,attr"`
	Expr string `xml:",chardata"`

	// dir is the directory the environment was written in, filled in by
	// resolveEnv. A query may name a file relative to its own test-set, and
	// after the merge there is nothing else left to say which one that was.
	dir string
}
