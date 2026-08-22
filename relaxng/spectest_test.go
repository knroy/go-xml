package relaxng

import (
	"os"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// The RELAX NG conformance suite, James Clark's spectest.xml.
//
// It is not vendored — it belongs to its author and lives in the jing-trang
// repository. Point GOXSLT_RNG at a copy to run it:
//
//	git clone --depth 1 https://github.com/relaxng/jing-trang.git
//	GOXSLT_RNG=jing-trang/mod/rng-validate/test/spectest.xml go test ./relaxng/
//
// Without it these tests skip, so the ordinary `go test ./...` is unaffected.
//
// Each case is one of three shapes: an <incorrect> schema that must be
// rejected, a <correct> schema with <valid> documents that must pass, and the
// same with <invalid> documents that must fail. The suite is weighted towards
// the first — 213 of 385 — which makes it as much a test of the schema parser
// as of the validator.
func TestSpectest(t *testing.T) {
	path := os.Getenv("GOXSLT_RNG")
	if path == "" {
		path = "../testdata/relaxng/spectest.xml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("set GOXSLT_RNG to a copy of spectest.xml to run the suite")
	}
	tree, err := xdm.ParseString(string(data), xdm.ParseOptions{AllowDOCTYPE: true})
	if err != nil {
		t.Fatalf("parse spectest.xml: %v", err)
	}

	var pass, fail int
	byKind := map[string]int{}
	var failures []string

	var walk func(n *xdm.Node)
	walk = func(n *xdm.Node) {
		for _, kid := range n.ChildElements() {
			if kid.Name.Local == "testCase" {
				p, f, kind, why := runCase(t, kid)
				pass += p
				fail += f
				if f > 0 {
					byKind[kind] += f
					if len(failures) < 40 {
						failures = append(failures, why)
					}
				}
				continue
			}
			walk(kid)
		}
	}
	walk(tree.Root)

	total := pass + fail
	t.Logf("RELAX NG spectest: %d assertions, %d passed, %d failed (%.2f%%)",
		total, pass, fail, 100*float64(pass)/float64(total))
	for k, v := range byKind {
		t.Logf("  %-10s %d failing", k, v)
	}
	if os.Getenv("GOXSLT_RNG_VERBOSE") != "" {
		for _, f := range failures {
			t.Log("  " + f)
		}
	}
	if total == 0 {
		t.Error("no assertions ran, which means the harness is wrong")
	}
}

// runCase returns the passed and failed assertion counts for one testCase.
func runCase(t *testing.T, tc *xdm.Node) (pass, fail int, kind, why string) {
	var correct, incorrect *xdm.Node
	var valids, invalids []*xdm.Node
	for _, kid := range tc.ChildElements() {
		switch kid.Name.Local {
		case "correct":
			correct = kid
		case "incorrect":
			incorrect = kid
		case "valid":
			valids = append(valids, kid)
		case "invalid":
			invalids = append(invalids, kid)
		}
	}

	if incorrect != nil {
		schema := firstElement(incorrect)
		if schema == nil {
			return 0, 0, "", ""
		}
		if _, err := Compile(schema); err == nil {
			return 0, 1, "incorrect", "incorrect schema accepted: " + summarise(schema)
		}
		return 1, 0, "", ""
	}

	if correct == nil {
		return 0, 0, "", ""
	}
	schema := firstElement(correct)
	if schema == nil {
		return 0, 0, "", ""
	}
	s, err := Compile(schema)
	if err != nil {
		// One assertion, not one per document: a schema that will not compile
		// is a single failure, and counting the documents it would have
		// checked inflates the number without adding information.
		return 0, 1, "correct", "correct schema rejected: " + err.Error()
	}
	pass++ // the schema compiled, which is itself an assertion

	for _, v := range valids {
		doc := firstElement(v)
		if doc == nil {
			continue
		}
		if err := s.Validate(doc); err != nil {
			fail++
			kind = "valid"
			why = "valid rejected: " + summarise(schema) + " || " +
				summarise(doc) + " || " + err.Error()
		} else {
			pass++
		}
	}
	for _, v := range invalids {
		doc := firstElement(v)
		if doc == nil {
			continue
		}
		if err := s.Validate(doc); err == nil {
			fail++
			kind = "invalid"
			why = "invalid accepted: " + summarise(schema) + " || " + summarise(doc)
		} else {
			pass++
		}
	}
	return pass, fail, kind, why
}

func firstElement(n *xdm.Node) *xdm.Node {
	for _, kid := range n.ChildElements() {
		return kid
	}
	return nil
}

func summarise(n *xdm.Node) string {
	s := strings.Join(strings.Fields(nodeText(n)), " ")
	if len(s) > 90 {
		s = s[:90] + "..."
	}
	return "<" + n.Name.Local + "> " + s
}

func nodeText(n *xdm.Node) string {
	var sb strings.Builder
	for _, a := range n.Attrs {
		sb.WriteString(" " + a.Name.Local + "=" + a.Value)
	}
	for _, c := range n.ChildElements() {
		sb.WriteString(" <" + c.Name.Local + ">")
	}
	return sb.String()
}
