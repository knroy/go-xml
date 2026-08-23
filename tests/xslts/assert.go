package xslts

import (
	"encoding/xml"
	"io"
	"strings"
)

// Assertion is one node of a result tree.
//
// The suite nests all-of, any-of and not to arbitrary depth, so the result is
// walked with the token stream rather than described with struct tags.
type Assertion struct {
	// Kind is the element name: "assert", "assert-xml", "error", "all-of".
	Kind string
	// Value is the character data, which for most kinds is the expected
	// result or the XPath expression to evaluate against it.
	Value string
	// Code is the @error-code of an <error>.
	Code string
	// File names a document holding the expected value, used where it is too
	// large to sit inline.
	File string
	// Normalize records @normalize-space on an assert-xml, which says
	// whitespace differences do not count.
	Normalize bool
	// URI is the @uri of an assert-result-document, naming which secondary
	// output the nested assertions apply to.
	URI string
	// Flags is the @flags of a serialization-matches, in the XPath regular
	// expression flag vocabulary: i, s, m and x.
	Flags string
	// Encoding is the @encoding of an assert-serialization, naming the
	// character encoding the expected-result file is written in. Without it
	// the file is UTF-8, which is what almost every one of them is.
	Encoding string
	// NS holds the prefix-to-URI bindings in scope where the assertion was
	// written. The suite declares them on the assertion element or on an
	// enclosing combinator — an XPath assertion that names a prefix cannot be
	// evaluated without them.
	NS map[string]string
	// Children are the operands of all-of, any-of and not.
	Children []Assertion
}

// ParseAssert parses the raw result XML into an assertion tree.
func ParseAssert(raw []byte) (Assertion, error) {
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	root := Assertion{Kind: "all-of"}
	stack := []*Assertion{&root}
	// Namespace scopes parallel the element stack, so a binding declared on a
	// combinator reaches the assertions nested inside it.
	nsStack := []map[string]string{nil}
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
			ns := nsStack[len(nsStack)-1]
			for _, at := range t.Attr {
				if at.Name.Space == "xmlns" || (at.Name.Space == "" && at.Name.Local == "xmlns") {
					next := make(map[string]string, len(ns)+1)
					for k, v := range ns {
						next[k] = v
					}
					if at.Name.Space == "xmlns" {
						next[at.Name.Local] = at.Value
					} else {
						next[""] = at.Value
					}
					ns = next
					continue
				}
				switch at.Name.Local {
				case "error-code", "code":
					a.Code = at.Value
				case "file":
					a.File = at.Value
				case "normalize-space":
					a.Normalize = at.Value == "true" || at.Value == "yes"
				case "uri":
					a.URI = at.Value
				case "flags":
					a.Flags = at.Value
				case "encoding":
					a.Encoding = at.Value
				}
			}
			a.NS = ns
			nsStack = append(nsStack, ns)
			top := stack[len(stack)-1]
			top.Children = append(top.Children, a)
			stack = append(stack, &top.Children[len(top.Children)-1])
		case xml.CharData:
			stack[len(stack)-1].Value += string(t)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
				nsStack = nsStack[:len(nsStack)-1]
			}
		}
	}
	return root, nil
}
