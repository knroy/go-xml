package xdm

import "testing"

// xml:id section 4 requires the value to be whitespace-normalized at parse
// time, and xml:id is of type xs:ID, whose whiteSpace facet is collapse. The
// separator set for collapse is XML S, so a no-break space in the value is
// data and must survive: strings.Fields replaced it with an ordinary space,
// producing a node whose value is not the one the data model says the
// document has.
func TestXMLIDNormalizationUsesXMLSpaceOnly(t *testing.T) {
	const nbsp = "\u00a0"
	for _, tc := range []struct{ name, in, want string }{
		{"xml S collapses", "  a\t\n b ", "a b"},
		{"nbsp survives", "a" + nbsp + "b", "a" + nbsp + "b"},
		{"nbsp not trimmed", nbsp + "a" + nbsp, nbsp + "a" + nbsp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := ParseString(`<r xml:id="`+tc.in+`"/>`, ParseOptions{})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got := tree.Root.Children[0].Attrs[0].Value
			if got != tc.want {
				t.Errorf("xml:id %q normalized to %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A whitespace-separated list type tokenizes on XML S. strings.Fields split on
// the whole Unicode White_Space set, turning the single NMTOKEN "a<NBSP>b"
// into the two tokens "a" and "b".
func TestSplitXMLSpaceIsNotUnicodeFields(t *testing.T) {
	const nbsp = "\u00a0"
	if got := SplitXMLSpace("a" + nbsp + "b"); len(got) != 1 || got[0] != "a"+nbsp+"b" {
		t.Errorf("SplitXMLSpace(a<NBSP>b) = %q, want one token", got)
	}
	if got := SplitXMLSpace(" a\t b\n"); len(got) != 2 {
		t.Errorf("SplitXMLSpace on XML S = %q, want two tokens", got)
	}
	if got := TrimXMLSpace(nbsp + "a" + nbsp); got != nbsp+"a"+nbsp {
		t.Errorf("TrimXMLSpace trimmed a NBSP: %q", got)
	}
}

// A list type's value is tokenized on XML S. A no-break space inside a token
// is data, so "a<NBSP>b c" is two NMTOKENs and not three: strings.Fields made
// it three, splitting a token the schema says is one.
func TestAtomizeListTokenizesOnXMLSpaceOnly(t *testing.T) {
	const nbsp = " "
	n := &Node{Kind: KindAttribute, Value: "a" + nbsp + "b c"}
	n.SetTypeAnnotation("NMTOKENS")

	seq, ok := n.AtomizeList()
	if !ok {
		t.Fatal("AtomizeList reported not-a-list")
	}
	if len(seq) != 2 {
		t.Fatalf("got %d tokens, want 2: %v", len(seq), seq)
	}
	if got := seq[0].(*Atomic).String(); got != "a"+nbsp+"b" {
		t.Errorf("first token = %q, want %q", got, "a"+nbsp+"b")
	}
}
