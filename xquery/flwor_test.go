package xquery_test

import (
	"strings"
	"testing"

	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xquery"
)

// evalStrings runs a query and returns its result as one space-separated
// string, which is enough to assert on for every case here and keeps the
// table readable.
func evalStrings(t *testing.T, src string) (string, error) {
	t.Helper()
	seq, err := xquery.Eval(src, xpath.NewContext(nil, xpath.Builtins()),
		xquery.Options{})
	if err != nil {
		return "", err
	}
	var parts []string
	for _, it := range seq {
		parts = append(parts, itemString(it))
	}
	return strings.Join(parts, " "), nil
}

func itemString(it any) string {
	if v, ok := it.(interface{ StringValue() string }); ok {
		return v.StringValue()
	}
	if v, ok := it.(interface{ String() string }); ok {
		return v.String()
	}
	return ""
}

// The clauses, one at a time and then composed. These are the cases the
// tuple-stream model exists to get right: a "count" numbers whatever the
// clauses above it left, and a "group by" partitions rather than iterates.
func TestFLWORClauses(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`for $x in (1, 2, 3) return $x`, "1 2 3"},
		{`for $x in (1, 2, 3) where $x > 1 return $x`, "2 3"},
		{`for $x at $i in ("a", "b") return $i`, "1 2"},
		{`for $x in (1, 2), $y in (3, 4) return ($x, $y)`,
			"1 3 1 4 2 3 2 4"},
		{`let $x := 5 return $x + 1`, "6"},
		{`let $x := 1, $y := 2 return $x + $y`, "3"},
		{`for $x in (3, 1, 2) order by $x return $x`, "1 2 3"},
		{`for $x in (3, 1, 2) order by $x descending return $x`, "3 2 1"},
		{`for $x in (1, 2, 3) count $c return $c`, "1 2 3"},

		// "at" numbers one clause's items; "count" numbers the stream, so
		// after an "order by" it numbers the sorted order and the positional
		// variable still holds where the item came from.
		{`for $x at $i in (30, 10, 20) order by $x count $c return ($c, $i)`,
			"1 2 2 3 3 1"},

		// The whole point of the model: "group by" sees every tuple, and the
		// non-grouping variables become the partition's members.
		{`for $x in (1, 2, 3, 4) group by $k := $x mod 2 ` +
			`return concat($k, ":", count($x))`, "1:2 0:2"},

		// "allowing empty" is the outer join: an empty sequence still yields
		// one tuple, with the positional variable at 0.
		{`for $x allowing empty at $i in () return $i`, "0"},
		{`for $x allowing empty in () return "none"`, "none"},
		{`for $x in (1, 2) return $x`, "1 2"},
	} {
		got, err := evalStrings(t, c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
}

// The empty-order is not reversed by "descending" (section 3.10.6), and NaN
// takes the empty sequence's position rather than being incomparable.
//
// The ordering key has to be an expression that *yields* the empty sequence
// for some tuples, not a sequence with an empty item in it: "(2, (), 1)" is
// two items, because the empty sequence vanishes when a sequence is built.
// That is why these iterate over elements and key on a child that one of them
// lacks.
func TestOrderByEmptyAndNaN(t *testing.T) {
	const src = `for $x in (<r><k>2</k></r>, <r/>, <r><k>1</k></r>) ` +
		`order by $x/k `
	for _, c := range []struct{ src, want string }{
		{src + `empty least return string($x/k)`, " 1 2"},
		{src + `empty greatest return string($x/k)`, "1 2 "},
		// "descending" reverses the whole order, the empty key with it, so
		// the empty one that sorted first ascending under "empty least"
		// sorts last descending. prod-EmptyOrderDecl 10-13 and 18-21 are
		// the suite cases that fix this; an earlier reading of §3.10.6 had
		// the empty-order as a position the direction left alone, which
		// inverted all eight.
		{src + `descending empty least return string($x/k)`, "2 1 "},
		{src + `descending empty greatest return string($x/k)`, " 2 1"},
		// NaN orders where the empty sequence does.
		{`for $x in (2, xs:double("NaN"), 1) order by $x empty least ` +
			`return string($x)`, "NaN 1 2"},
		{`for $x in (2, xs:double("NaN"), 1) order by $x empty greatest ` +
			`return string($x)`, "1 2 NaN"},
	} {
		got, err := evalStrings(t, c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
}

// Section 3.10.7 rebinds a grouping variable to the *atomised* value of its
// key, so a constructor in the return expression sees the value rather than
// the node it came from.
func TestGroupByAtomisesTheKey(t *testing.T) {
	got, err := evalStrings(t,
		`for $x in (<a>k</a>, <a>k</a>) let $s := $x/string() `+
			`group by $s return <g>{$s}</g>`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "k" {
		t.Errorf("got %q, want %q", got, "k")
	}
}

// A "for" declaration constrains each item bound and a "let" declaration the
// whole sequence, so the same type accepts one and refuses the other.
func TestDeclaredTypes(t *testing.T) {
	if _, err := evalStrings(t,
		`for $x as xs:integer in (1, 2) return $x`); err != nil {
		t.Errorf("a matching per-item type should bind: %v", err)
	}
	_, err := evalStrings(t, `for $x as xs:string in (1, 2) return $x`)
	if err == nil || !strings.Contains(err.Error(), "XPTY0004") {
		t.Errorf("want XPTY0004 for a mismatched item type, got %v", err)
	}
	// "let" sees the sequence, so a one-item type refuses two items where the
	// same type on a "for" would have accepted each of them.
	_, err = evalStrings(t, `let $x as xs:integer := (1, 2) return $x`)
	if err == nil || !strings.Contains(err.Error(), "XPTY0004") {
		t.Errorf("want XPTY0004 for a mismatched sequence type, got %v", err)
	}
}

// A quantified expression short-circuits and admits a type declaration, which
// is the one thing XPath's form does not.
func TestQuantified(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`some $x in (1, 2) satisfies $x > 1`, "true"},
		{`every $x in (1, 2) satisfies $x > 1`, "false"},
		{`some $x in () satisfies true()`, "false"},
		{`every $x in () satisfies false()`, "true"},
		{`some $x as xs:integer in (1, 2) satisfies $x > 1`, "true"},
		{`some $x in (1, 2), $y in (3, 4) satisfies $x + $y eq 6`, "true"},
	} {
		got, err := evalStrings(t, c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
}

// Windows. A tumbling window's windows are disjoint and a sliding window's
// overlap, which is the whole of the difference between them.
func TestWindowClause(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`for tumbling window $w in (1, 2, 3, 4) start at $s when true() ` +
			`end at $e when $e - $s eq 1 return count($w)`, "2 2"},
		{`for sliding window $w in (1, 2, 3, 4) start at $s when true() ` +
			`end at $e when $e - $s eq 1 return count($w)`, "2 2 2 1"},

		// "only" discards a window that never closed, and keeps looking:
		// whether a window closes depends on where it started.
		{`for sliding window $w in (0, 1, 2, 3, 4, 14, 13, 12, 11) ` +
			`start $s when true() only end $e when $e eq $s + 10 ` +
			`return string-join($w ! string(), "-")`,
			"1-2-3-4-14-13-12-11 2-3-4-14-13-12 3-4-14-13 4-14"},

		// A tumbling window with no end condition ends where the next one
		// begins, which partitions the sequence.
		{`for tumbling window $w in (1, 2, 1, 2, 1) ` +
			`start $s when $s eq 1 return count($w)`, "2 2 1"},

		// An unclosed window binds its end variable to the last item, and
		// its "next" variable to the empty sequence — that, not the end
		// variable, is what distinguishes it from one that closed there.
		{`for tumbling window $w in (1, 2, 3) start $s when true() ` +
			`end $e next $n when false() return ($e, count($n))`, "3 0"},
	} {
		got, err := evalStrings(t, c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
}

// A window clause binds its variables together rather than in sequence, so a
// repeated name has no reading and is XQST0103. Two "for" clauses are not
// like that: the second shadows the first.
func TestWindowVariablesMustDiffer(t *testing.T) {
	_, err := evalStrings(t,
		`for tumbling window $w in (1 to 3) start $w when true() `+
			`end $e when false() return $w`)
	if err == nil || !strings.Contains(err.Error(), "XQST0103") {
		t.Errorf("want XQST0103, got %v", err)
	}
}

// The expressions that need both parsers: the syntax xpath cannot read on one
// side of a path or inside a call's arguments, and the ordinary XPath
// expression on the other.
func TestExpressionsNeedingBothParsers(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`count((<a/>, <b/>, <c/>))`, "3"},
		{`count(for $x in (1, 2, 3) where $x > 1 return $x)`, "2"},
		{`(for $x in (1, 2) return <a>{$x}</a>)/string()`, "1 2"},
		{`<e/>/(for $i in self::node() return $i)/name()`, "e"},
		{`let $x := (<a/>, <b/>) return count($x)`, "2"},
		{`<a><b/></a>/b/name()`, "b"},

		// "<" is markup only where an operand may begin; after a variable it
		// is the less-than operator whatever follows it.
		{`for $x in (1, 2) return $x < 3`, "true true"},
		{`1 < 2`, "true"},

		// A nested FLWOR's clauses must not be mistaken for the enclosing
		// one's.
		{`for $x in (1, 2) let $y := for $z in (10) return $z return $y`,
			"10 10"},
	} {
		got, err := evalStrings(t, c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
}

// A FLWOR variable is visible to every later clause and to the return
// expression, which is what binding the tuple onto the context achieves.
func TestVariableScope(t *testing.T) {
	got, err := evalStrings(t,
		`for $x in (1, 2, 3) let $d := $x * 2 where $d > 2 `+
			`order by $d descending count $c return concat($c, "/", $d)`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "1/6 2/4"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
