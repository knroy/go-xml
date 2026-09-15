package xpath

import (
	"strings"
	"testing"
)

// TestSerializeIndentIsHonoured pins that fn:serialize acts on the indent
// parameter.
//
// Serialization 3.1 §4 defines indent="yes" as asking the serialiser to "add
// whitespace ... in order to indicate the hierarchic structure", constrained
// by the rule that it may not add whitespace "in any place where it is
// significant". The parameter was parsed into serializeOptions.indent and the
// field had two writes and no reads, so fn:serialize accepted indent="yes"
// and returned exactly what it returned without it -- while
// xsl:result-document on the same document indented properly. One request,
// two answers, decided by which spelling was used.
func TestSerializeIndentIsHonoured(t *testing.T) {
	const doc = `<a><b><c/></b></a>`

	plain, err := evalSerialize(t,
		`serialize(parse-xml('`+doc+`'), map{'indent': false()})`)
	if err != nil {
		t.Fatalf("serialize without indent: %v", err)
	}
	indented, err := evalSerialize(t,
		`serialize(parse-xml('`+doc+`'), map{'indent': true()})`)
	if err != nil {
		t.Fatalf("serialize with indent: %v", err)
	}

	if plain == indented {
		t.Fatalf("indent=true() changed nothing: %q", indented)
	}
	if !strings.Contains(indented, "\n") {
		t.Errorf("indented output has no newline: %q", indented)
	}
	// The nesting must actually be expressed, not merely a newline somewhere.
	if !strings.Contains(indented, "\n  <b>") {
		t.Errorf("child not indented one level: %q", indented)
	}
	if !strings.Contains(indented, "\n    <c/>") {
		t.Errorf("grandchild not indented two levels: %q", indented)
	}
}

// TestSerializeIndentLeavesTextAlone pins the significant-whitespace rule:
// an element holding non-whitespace text is not re-indented, because the
// inserted whitespace would become part of its string value.
func TestSerializeIndentLeavesTextAlone(t *testing.T) {
	got, err := evalSerialize(t,
		`serialize(parse-xml('<a><b>text</b></a>'), map{'indent': true()})`)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if !strings.Contains(got, "<b>text</b>") {
		t.Errorf("text content was re-indented: %q", got)
	}
}
