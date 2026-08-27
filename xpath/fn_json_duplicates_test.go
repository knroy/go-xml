package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestJSONToXMLDuplicatesAcrossNesting covers the frame the duplicates option
// is tracked in.
//
// A container is attached to its parent at the moment it opens, because the
// pending key belongs to it and its own entries would overwrite the slot. The
// key must be recorded against the CONTAINING map's frame, not the frame of
// the map being opened — pushing the new frame first pointed it at the wrong
// one, so a duplicate key whose value was a map or an array went undetected
// while a duplicate key on a scalar was caught normally.
//
// json-to-xml-duplicates-001 is the case: under use-first it kept all four
// entries of {"one":{…},"two":{…},"one":{…},"two":{…}} while correctly
// dropping the repeated scalar key inside each.
func TestJSONToXMLDuplicatesAcrossNesting(t *testing.T) {
	const in = `{"one": {"a":2, "a":5}, "two": [1], "one": {"a":3}, "two": [2]}`
	for _, tc := range []struct {
		duplicates string
		want       string
	}{
		{"use-first", `<map xmlns="http://www.w3.org/2005/xpath-functions">` +
			`<map key="one"><number key="a">2</number></map>` +
			`<array key="two"><number>1</number></array></map>`},
		{"retain", `<map xmlns="http://www.w3.org/2005/xpath-functions">` +
			`<map key="one"><number key="a">2</number><number key="a">5</number></map>` +
			`<array key="two"><number>1</number></array>` +
			`<map key="one"><number key="a">3</number></map>` +
			`<array key="two"><number>2</number></array></map>`},
	} {
		ctx := NewContext(nil, Builtins())
		ctx.Version, ctx.LibraryVersion = XPath31, XPath31
		seq, err := Eval(
			`serialize(json-to-xml($j, map{'duplicates': $d}))`, ctx.
				WithVar(xdm.QName{Local: "j"}, xdm.One(xdm.NewString(in))).
				WithVar(xdm.QName{Local: "d"}, xdm.One(xdm.NewString(tc.duplicates))), nil)
		if err != nil {
			t.Fatalf("duplicates=%s: %v", tc.duplicates, err)
		}
		got := seq[0].(*xdm.Atomic).String()
		if got != tc.want {
			t.Errorf("duplicates=%s:\n got %s\nwant %s", tc.duplicates, got, tc.want)
		}
	}
	// reject sees the outer duplicate for the same reason use-first drops it.
	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	_, err := Eval(`json-to-xml($j, map{'duplicates': 'reject'})`,
		ctx.WithVar(xdm.QName{Local: "j"}, xdm.One(xdm.NewString(`{"a":{"x":1},"a":{"y":2}}`))), nil)
	if err == nil || !strings.Contains(err.Error(), "FOJS0003") {
		t.Errorf("a duplicate key on a nested map should be FOJS0003, got %v", err)
	}
}
