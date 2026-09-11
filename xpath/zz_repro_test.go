package xpath

import "testing"

func TestZZReproNBSP(t *testing.T) {
	nb := " "
	for _, q := range []string{
		"string-length(normalize-space('a" + nb + "b'))",
		"string-length(normalize-space('a" + nb + nb + "b'))",
		"string-length(normalize-space('a b'))",
		"count(tokenize('a" + nb + "b'))",
		"string(xs:boolean('true" + nb + "'))",
		"string(xs:integer('1" + nb + "'))",
		"string(xs:hexBinary('AB" + nb + "'))",
		"string(xs:base64Binary('AQID" + nb + "'))",
		"string-length(string(xs:anyURI('" + nb + "x" + nb + "')))",
		"string(xs:QName('" + nb + "a" + nb + "'))",
	} {
		root := mustParse(t, testDoc)
		ctx := NewContext(root, Builtins())
		seq, err := Eval(q, ctx, testNS{})
		if err != nil {
			t.Logf("%-60s => ERROR %v", q, err)
			continue
		}
		t.Logf("%-60s => %q", q, renderSeq(seq))
	}
}
