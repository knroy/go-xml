package xpath

import (
	"strings"
	"testing"
)

// TestSerializeQNameKeyDoesNotNameStandardParameter pins how a map key is
// typed in the map form of fn:serialize's parameters.
//
// Serialization 3.1 §3: "the key of the entry is an xs:string value in the
// cases of parameter names defined in these specifications, or an xs:QName
// (with non-absent namespace) in the case of implementation-defined
// serialization parameters."
//
// QName("", "indent") is neither: a no-namespace QName is not the xs:string
// key that names the standard indent parameter, and not the non-absent-
// namespace QName that names an implementation-defined one. So it must not
// turn indentation on. The key was compared with String(), which renders that
// QName as "indent" and matched it like the string.
//
// This was invisible for as long as the indent parameter itself did nothing:
// accepting a key and then ignoring what it asked for looks the same as
// rejecting it. serialize-xml-120 and -120b caught it the moment indent
// started working, which is why they are cited here.
func TestSerializeQNameKeyDoesNotNameStandardParameter(t *testing.T) {
	// serialize-xml-120: contains(serialize(<e><f/></e>, $params), " ") is
	// false, because the QName key never selects indent.
	got, err := evalSerialize(t,
		`serialize(parse-xml('<e><f/></e>'),`+
			` map{QName('','indent'): true(), 'omit-xml-declaration': true()})`)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if strings.Contains(got, " ") || strings.Contains(got, "\n") {
		t.Errorf("a no-namespace QName key turned indentation on: %q", got)
	}

	// The guard: the xs:string key is the spelling that does name the
	// parameter, and it must still work.
	indented, err := evalSerialize(t,
		`serialize(parse-xml('<e><f/></e>'),`+
			` map{'indent': true(), 'omit-xml-declaration': true()})`)
	if err != nil {
		t.Fatalf("serialize with string key: %v", err)
	}
	if !strings.Contains(indented, "\n") {
		t.Errorf("the xs:string key stopped selecting indent: %q", indented)
	}
}
