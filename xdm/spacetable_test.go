package xdm

import "testing"

// Whitespace runs are shared by length first; two runs of one length but
// different characters must still keep their own values.
func TestSpaceTableSameLengthRuns(t *testing.T) {
	runs := []string{"\n  ", "  \n", "\n  ", "\t\t\t", " \r\n", "\n  ", "x  ", "\n  "}
	var tab spaceTable
	for i, r := range runs {
		if got := tab.text([]byte(r)); got != r {
			t.Fatalf("run %d: got %q, want %q", i, got, r)
		}
	}

	tr, err := ParseString("<a>\n  <b/>  \n<c/>\n  <d/>\t\t\t<e/>\n  </a>", ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"\n  ", "  \n", "\n  ", "\t\t\t", "\n  "}
	var got []string
	for _, c := range tr.Root.children[0].children {
		if c.kind == KindText {
			got = append(got, c.value)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("text nodes %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("text node %d is %q, want %q", i, got[i], want[i])
		}
	}
}
