package xsd

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// A key sequence is compared MEMBER BY MEMBER (§3.11.4 clause 4), so the table
// key standing in for it has to be injective on the sequence. It was not: the
// fields were joined on U+001F, a delimiter that lives inside the value space,
// so a field value carrying that byte could forge the boundary between two
// other fields. ("a\x1fX", "c") and ("a", "X\x1fc") joined to one string, and
// xs:key reported cvc-identity-constraint.4.2.2 on a document whose two key
// sequences are distinct.
//
// U+001F is forbidden in XML 1.0 content, which is why this never showed
// through the parser and why it is pinned here at the level it is reachable
// at: xdm.Node values a caller assembles or a future XML 1.1 mode admits. The
// defect is in the ENCODING, not in the character check, and a character check
// is the wrong place to fix it — the next delimiter chosen would have the same
// hole.

// keySepSchema puts a two-field key over `i`, which is the smallest shape in
// which one field's content can impersonate a field boundary. Two fields are
// needed: with one there is no boundary to forge.
const keySepSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:sequence>
      <xs:element name="i" maxOccurs="unbounded">
        <xs:complexType>
          <xs:attribute name="a" type="xs:string"/>
          <xs:attribute name="b" type="xs:string"/>
        </xs:complexType>
      </xs:element>
    </xs:sequence></xs:complexType>
    <xs:key name="k">
      <xs:selector xpath=".//i"/>
      <xs:field xpath="@a"/>
      <xs:field xpath="@b"/>
    </xs:key>
  </xs:element>
</xs:schema>`

// keySepTree builds the instance directly rather than parsing one.
//
// It has to: the values under test are exactly the ones xdm.ParseString
// rejects as "illegal character code U+001F", so a parsed document cannot
// carry them. Validation takes an *xdm.Node and does not require the tree to
// have come from the parser, so this is a real input shape, and it is the one
// an XML 1.1 instance would produce once that path admits the character.
func keySepTree(pairs ...[2]string) *xdm.Node {
	root := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "root"}}
	for _, p := range pairs {
		i := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "i"}, Parent: root}
		for _, at := range [][2]string{{"a", p[0]}, {"b", p[1]}} {
			i.Attrs = append(i.Attrs, &xdm.Node{
				Kind:   xdm.KindAttribute,
				Name:   xdm.QName{Local: at[0]},
				Value:  at[1],
				Parent: i,
			})
		}
		root.Children = append(root.Children, i)
	}
	return root
}

func keySepSchemaLoad(t *testing.T) *Schema {
	t.Helper()
	st, err := xdm.ParseString(keySepSchema, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	return s
}

// TestIdentityKeySequenceInjective is the verdict: distinct key sequences must
// never be reported as a duplicate, whatever their fields contain.
//
// The adversarial pairs each join to ONE string under the old separator
// encoding. keyString prefixes each field with its primitive — "string/" here,
// since values from different primitives are never equal (idF012) — so the
// forged boundary has to carry that prefix with it for the two joins to line
// up; that is what the "string/" inside the values is doing. Without it the
// pair does not collide even on the broken code, and the test would pass
// against the bug it exists to catch.
func TestIdentityKeySequenceInjective(t *testing.T) {
	s := keySepSchemaLoad(t)

	cases := []struct {
		name string
		docs [2][2]string
	}{
		{
			// The delimiter-injection pair proper: field a of the
			// first ends where field b of the second begins.
			"separator injected into a field",
			[2][2]string{{"a\x1fstring/b", "c"}, {"a", "b\x1fstring/c"}},
		},
		{
			// ("a","b") against ("ab"): the classic concatenation
			// ambiguity, distinct here only because the encoding
			// records where each field ends.
			"split against concatenated",
			[2][2]string{{"a", "b"}, {"ab", ""}},
		},
		{
			// ("", "a") against ("a", ""): the empty field must be
			// a position, not an absence. An encoding that skipped
			// empty fields would make these one key.
			"empty field on either side",
			[2][2]string{{"", "a"}, {"a", ""}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := keySepTree(tc.docs[0], tc.docs[1])
			if err := s.Validate(doc, ValidateOptions{}); err != nil {
				t.Errorf("distinct key sequences %q and %q reported as a duplicate: %v",
					tc.docs[0], tc.docs[1], err)
			}
		})
	}
}

// TestIdentityKeySequenceStillDetectsDuplicates is the other half of the
// verdict, and the one a careless fix breaks: an encoding can be made
// injective by making it injective on the NODE too, at which point no
// duplicate is ever found and the constraint is dead. Two targets that really
// do carry the same sequence must still fail.
func TestIdentityKeySequenceStillDetectsDuplicates(t *testing.T) {
	s := keySepSchemaLoad(t)

	// The ordinary case, with no adversarial content at all.
	doc := keySepTree([2]string{"a", "b"}, [2]string{"a", "b"})
	err := s.Validate(doc, ValidateOptions{})
	if err == nil {
		t.Fatal("two targets with the same key sequence were accepted")
	}
	if !strings.Contains(err.Error(), "cvc-identity-constraint.4.2.2") {
		t.Errorf("want cvc-identity-constraint.4.2.2, got %v", err)
	}

	// And with the separator present in both, identically: still one
	// sequence, still a duplicate. Injectivity does not mean "never equal".
	doc = keySepTree([2]string{"a\x1fb", "c"}, [2]string{"a\x1fb", "c"})
	if err := s.Validate(doc, ValidateOptions{}); err == nil {
		t.Error("identical key sequences containing U+001F were accepted")
	}
}

// TestKeySequenceEncodingRoundTrips pins the encoding itself, below the
// validator, over the field shapes the document-level tests cannot reach —
// including the ":" that delimits the length prefix, which is the one byte a
// length-prefixed encoding could plausibly be confused by.
func TestKeySequenceEncodingRoundTrips(t *testing.T) {
	seqs := [][]string{
		{},
		{""},
		{"", ""},
		{"a"},
		{"a", "b"},
		{"ab"},
		{"", "a"},
		{"a", ""},
		{"a\x1fb", "c"},
		{"a", "b\x1fc"},
		{"1:a", "b"},
		{"1", ":a", "b"},
		{"3:xyz"},
		{"\x1f", "\x1f"},
	}

	seen := map[string][]string{}
	for _, seq := range seqs {
		enc := joinKeySequence(seq)
		if prev, dup := seen[enc]; dup {
			t.Errorf("distinct sequences %q and %q encode alike as %q", prev, seq, enc)
			continue
		}
		seen[enc] = seq

		got := splitKeySequence(enc)
		if len(got) != len(seq) {
			t.Errorf("%q encoded as %q decoded to %q", seq, enc, got)
			continue
		}
		for i := range seq {
			if got[i] != seq[i] {
				t.Errorf("%q encoded as %q decoded to %q", seq, enc, got)
				break
			}
		}
	}
}

// TestRenderKeySequenceIsTotal covers the error-message path. A keyref that
// finds no match names the sequence it wanted, comma-separated, and that
// rendering must not be able to fail: splitKeySequence returns nil for a string
// this package did not encode, and the render then shows it verbatim rather
// than reporting a truncated sequence.
func TestRenderKeySequenceIsTotal(t *testing.T) {
	if got := renderKeySequence(joinKeySequence([]string{"a", "b"})); got != "a, b" {
		t.Errorf("render = %q, want %q", got, "a, b")
	}
	for _, bad := range []string{"nope", "9:ab", "-1:a", "x:a"} {
		if got := renderKeySequence(bad); got != bad {
			t.Errorf("render(%q) = %q, want it unchanged", bad, got)
		}
	}
}
