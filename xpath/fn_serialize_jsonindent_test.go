package xpath

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// TestSerializeJSONIndent pins the shape of indented JSON output.
//
// Serialization 3.1 section 9.1.4 leaves this optional in one direction --
// indent=yes MAY add whitespace around the structural tokens, indent=no MUST
// NOT -- so what is pinned here is a choice, not a requirement, and the reason
// for the choice is that it is Saxon's. The request that prompted it compared
// the two processors side by side, and output that differs only in whitespace
// is output a reader has to diff by hand.
//
// The inline rule is the part worth pinning hardest: a map or array whose
// members are all leaves stays on one line. Without it [3,2,1] becomes four
// lines holding one number each, which is taller without being clearer, and
// the grouping results that prompted the request are exactly lists of strings
// nested inside maps.
func TestSerializeJSONIndent(t *testing.T) {
	arr := func(items ...xdm.Item) *xdm.ArrayItem {
		seqs := make([]xdm.Sequence, len(items))
		for i, it := range items {
			seqs[i] = xdm.Sequence{it}
		}
		return xdm.NewArray(seqs...)
	}
	str := func(s string) xdm.Item { return xdm.NewString(s) }
	num := func(f float64) xdm.Item { return xdm.NewDouble(f) }

	for _, c := range []struct {
		name   string
		item   xdm.Item
		indent bool
		want   string
	}{{
		name:   "leaf array stays inline",
		item:   arr(num(3), num(2), num(1)),
		indent: true,
		want:   "[ 3, 2, 1 ]",
	}, {
		// The MUST NOT half of 9.1.4. This is also what every existing
		// caller got before indentation existed, so it is the regression
		// guard for output nobody asked to change.
		name:   "indent off is byte-for-byte compact",
		item:   arr(num(3), num(2), num(1)),
		indent: false,
		want:   "[3,2,1]",
	}, {
		name:   "empty array has no whitespace to add",
		item:   arr(),
		indent: true,
		want:   "[]",
	}, {
		name:   "nested array breaks, inner stays inline",
		item:   arr(arr(num(1), num(2)), arr(num(3), num(4))),
		indent: true,
		want:   "[\n  [ 1, 2 ],\n  [ 3, 4 ]\n]",
	}, {
		name:   "string members inline",
		item:   arr(str("a"), str("b")),
		indent: true,
		want:   `[ "a", "b" ]`,
	}} {
		t.Run(c.name, func(t *testing.T) {
			got, err := SerializeJSON(xdm.Sequence{c.item}, SerializeParams{Indent: c.indent})
			if err != nil {
				t.Fatalf("SerializeJSON: %v", err)
			}
			if got != c.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

// TestSerializeJSONIndentTwoSpaces pins the width, which is two rather than
// four for a reason that is not taste: the XML output method in the xslt
// package already indents by two, and one processor indenting its two output
// methods differently is a difference a reader has to explain. Saxon also
// writes two, which is what makes the outputs comparable.
func TestSerializeJSONIndentTwoSpaces(t *testing.T) {
	inner := xdm.NewArray(xdm.Sequence{xdm.NewDouble(1)}, xdm.Sequence{xdm.NewDouble(2)})
	outer := xdm.NewArray(xdm.Sequence{inner}, xdm.Sequence{inner})
	got, err := SerializeJSON(xdm.Sequence{outer}, SerializeParams{Indent: true})
	if err != nil {
		t.Fatalf("SerializeJSON: %v", err)
	}
	for _, line := range strings.Split(got, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if n := len(line) - len(trimmed); n != 0 && n != 2 {
			t.Errorf("line %q is indented by %d spaces, want a multiple of 2", line, n)
		}
	}
}

// --- helpers shared by the cases below ---

// jsonMap builds a map from alternating key/value arguments, in insertion
// order, which is the order the JSON method writes them in.
func jsonMap(t *testing.T, kv ...any) *xdm.MapItem {
	t.Helper()
	if len(kv)%2 != 0 {
		t.Fatalf("jsonMap wants pairs, got %d arguments", len(kv))
	}
	b := xdm.NewMapBuilder()
	for i := 0; i < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			t.Fatalf("jsonMap key %d is %T, want string", i, kv[i])
		}
		var val xdm.Sequence
		switch v := kv[i+1].(type) {
		case nil:
			val = nil // the empty sequence, which JSON writes as null
		case xdm.Sequence:
			val = v
		case xdm.Item:
			val = xdm.Sequence{v}
		default:
			t.Fatalf("jsonMap value for %q is %T", key, kv[i+1])
		}
		if err := b.Set(xdm.NewString(key), val); err != nil {
			t.Fatalf("Set(%q): %v", key, err)
		}
	}
	return b.Build()
}

// TestSerializeJSONIndentMaps covers the map arm, which the array cases above
// do not reach at all.
//
// The map arm is the one that had to change shape rather than just gain a
// depth: deciding whether the map fits on one line needs every value before
// the first is written, and Entries is the only way to read them, so the
// entries are walked twice. These pin that the second walk agrees with the
// first -- a map that reports itself inline and then writes a nested value
// would produce output with a break in the middle of a line.
func TestSerializeJSONIndentMaps(t *testing.T) {
	for _, c := range []struct {
		name   string
		item   xdm.Item
		indent bool
		want   string
	}{{
		name:   "empty map has no whitespace to add",
		item:   xdm.NewMap(),
		indent: true,
		want:   "{}",
	}, {
		// The space after the colon is Saxon's and is what makes the output
		// comparable; without it the indentation is right and the lines still
		// do not match.
		name:   "leaf map stays inline with a space after the colon",
		item:   jsonMap(t, "a", xdm.NewDouble(1), "b", xdm.NewDouble(2)),
		indent: true,
		want:   `{ "a": 1, "b": 2 }`,
	}, {
		name:   "indent off writes no space anywhere",
		item:   jsonMap(t, "a", xdm.NewDouble(1), "b", xdm.NewDouble(2)),
		indent: false,
		want:   `{"a":1,"b":2}`,
	}, {
		name:   "a nested map breaks the outer one",
		item:   jsonMap(t, "a", jsonMap(t, "b", xdm.NewDouble(1))),
		indent: true,
		want:   "{\n  \"a\": { \"b\": 1 }\n}",
	}, {
		// Insertion order, not sorted order. encoding/json would sort these;
		// XSLT map order is the map's own, and reordering would change the
		// document.
		name:   "entries keep insertion order",
		item:   jsonMap(t, "z", xdm.NewDouble(1), "a", xdm.NewDouble(2)),
		indent: true,
		want:   `{ "z": 1, "a": 2 }`,
	}, {
		// An empty sequence is null, and null is a leaf: it must not push the
		// map onto several lines.
		name:   "a null value is a leaf",
		item:   jsonMap(t, "a", nil, "b", xdm.NewDouble(1)),
		indent: true,
		want:   `{ "a": null, "b": 1 }`,
	}} {
		t.Run(c.name, func(t *testing.T) {
			got, err := SerializeJSON(xdm.Sequence{c.item}, SerializeParams{Indent: c.indent})
			if err != nil {
				t.Fatalf("SerializeJSON: %v", err)
			}
			if got != c.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

// TestSerializeJSONIndentNesting pins that indentation accumulates with depth
// and that a container breaks as soon as ONE member is nested.
//
// The mixed case is the one worth having: an array holding a scalar and a map
// must break, because leaving it inline would put a multi-line map in the
// middle of a single line. jsonFitsInline answers "are they ALL leaves", and
// a test with only all-leaf and all-nested inputs would pass just as well
// against "is ANY of them a leaf".
func TestSerializeJSONIndentNesting(t *testing.T) {
	for _, c := range []struct {
		name string
		item xdm.Item
		want string
	}{{
		name: "three levels each indent one step further",
		item: xdm.NewArray(xdm.Sequence{xdm.NewArray(xdm.Sequence{
			xdm.NewArray(xdm.Sequence{xdm.NewDouble(1)}, xdm.Sequence{xdm.NewDouble(2)}),
		}, xdm.Sequence{xdm.NewDouble(9)})}),
		want: "[\n  [\n    [ 1, 2 ],\n    9\n  ]\n]",
	}, {
		name: "one nested member breaks a mixed array",
		item: xdm.NewArray(
			xdm.Sequence{xdm.NewDouble(1)},
			xdm.Sequence{xdm.NewArray(xdm.Sequence{xdm.NewDouble(2)})},
		),
		want: "[\n  1,\n  [ 2 ]\n]",
	}, {
		name: "a map inside an array breaks the array",
		item: xdm.NewArray(xdm.Sequence{xdm.NewDouble(1)}, xdm.Sequence{jsonMapFor(t)}),
		want: "[\n  1,\n  { \"k\": 1 }\n]",
	}, {
		name: "an empty container nested inside stays a leaf",
		item: xdm.NewArray(xdm.Sequence{xdm.NewDouble(1)}, xdm.Sequence{xdm.NewArray()}),
		want: "[\n  1,\n  []\n]",
	}} {
		t.Run(c.name, func(t *testing.T) {
			got, err := SerializeJSON(xdm.Sequence{c.item}, SerializeParams{Indent: true})
			if err != nil {
				t.Fatalf("SerializeJSON: %v", err)
			}
			if got != c.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

func jsonMapFor(t *testing.T) *xdm.MapItem { return jsonMap(t, "k", xdm.NewDouble(1)) }

// TestSerializeJSONIndentDoesNotChangeContent is the property that matters
// more than any particular layout: indentation adds whitespace BETWEEN tokens
// and changes nothing inside them.
//
// It is written as a comparison rather than as expected strings because that
// is the claim -- strip the whitespace the indenter adds and the compact
// output must come back exactly. A string holding a newline, a quote or a
// brace is the case this protects: those characters are escaped inside a JSON
// string, so removing whitespace outside strings cannot touch them, and if
// the indenter ever wrote a raw break inside one this would catch it.
func TestSerializeJSONIndentDoesNotChangeContent(t *testing.T) {
	tricky := jsonMap(t,
		"newline", xdm.NewString("a\nb"),
		"quote", xdm.NewString(`he said "hi"`),
		"braces", xdm.NewString("{[,]}"),
		"unicode", xdm.NewString("näïve    "),
		"tab", xdm.NewString("a\tb"),
		"nested", xdm.NewArray(xdm.Sequence{xdm.NewString("x, y")}),
	)
	compact, err := SerializeJSON(xdm.Sequence{tricky}, SerializeParams{})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	indented, err := SerializeJSON(xdm.Sequence{tricky}, SerializeParams{Indent: true})
	if err != nil {
		t.Fatalf("indented: %v", err)
	}
	if stripped := stripJSONWhitespace(indented); stripped != compact {
		t.Errorf("indented output does not reduce to the compact one.\n"+
			"stripped: %s\ncompact:  %s\nindented:\n%s", stripped, compact, indented)
	}
}

// stripJSONWhitespace removes whitespace that sits OUTSIDE JSON strings,
// tracking quoting and backslash escapes so that a space or newline inside a
// string is kept.
func stripJSONWhitespace(s string) string {
	var b strings.Builder
	inString, escaped := false, false
	for _, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case !inString && (r == ' ' || r == '\n' || r == '\t' || r == '\r'):
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestSerializeJSONIndentErrorsStillFire pins that the errors survive the
// rearranged map and array arms.
//
// This is the regression this change could most plausibly have caused: the
// map arm now collects every value before writing any, so a mistake there
// could return the collected values and never reach the duplicate-name check,
// silently emitting an object the caller cannot read back.
func TestSerializeJSONIndentErrorsStillFire(t *testing.T) {
	dup := xdm.NewMapBuilder()
	if err := dup.Set(xdm.NewString("a"), xdm.Sequence{xdm.NewDouble(1)}); err != nil {
		t.Fatal(err)
	}
	// xs:QName("a") and the string "a" are distinct XDM values that render
	// to the same JSON name, which is exactly the collision SERE0022 exists
	// for: JSON has no way to keep them apart.
	qn := xdm.NewQNameValue(xdm.QName{Local: "a"})
	if err := dup.Set(qn, xdm.Sequence{xdm.NewDouble(2)}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		item xdm.Item
		code string
	}{{
		name: "duplicate names under indent",
		item: dup.Build(),
		code: "SERE0022",
	}, {
		name: "a multi-item sequence member under indent",
		item: xdm.NewArray(xdm.Sequence{xdm.NewDouble(1), xdm.NewDouble(2)}),
		code: "SERE0023",
	}} {
		t.Run(c.name, func(t *testing.T) {
			for _, indent := range []bool{false, true} {
				_, err := SerializeJSON(xdm.Sequence{c.item}, SerializeParams{Indent: indent})
				if err == nil {
					t.Fatalf("indent=%v: no error, want %s", indent, c.code)
				}
				if !strings.Contains(err.Error(), c.code) {
					t.Errorf("indent=%v: %v, want %s", indent, err, c.code)
				}
			}
		})
	}
}
