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
	// Children are the operands of all-of, any-of and not.
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
				case "error-code", "code":
					a.Code = at.Value
				case "file":
					a.File = at.Value
				case "normalize-space":
					a.Normalize = at.Value == "true" || at.Value == "yes"
				case "uri":
					a.URI = at.Value
				}
			}
			top := stack[len(stack)-1]
			top.Children = append(top.Children, a)
			stack = append(stack, &top.Children[len(top.Children)-1])
		case xml.CharData:
			stack[len(stack)-1].Value += string(t)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return root, nil
}
