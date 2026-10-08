package xquery

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
)

// countJoins counts the joinClauses reachable from v, wherever the parser
// put them: a nested FLWOR may sit in a clause, a return, a constructor or a
// lifted operand, and walking the graph finds every one.
func countJoins(v any) int {
	seen := map[uintptr]bool{}
	var walk func(r reflect.Value) int
	walk = func(r reflect.Value) int {
		switch r.Kind() {
		case reflect.Pointer:
			if r.IsNil() || seen[r.Pointer()] {
				return 0
			}
			seen[r.Pointer()] = true
			n := walk(r.Elem())
			if r.Type() == reflect.TypeOf(&joinClause{}) {
				n++
			}
			return n
		case reflect.Interface:
			if r.IsNil() {
				return 0
			}
			return walk(r.Elem())
		case reflect.Struct:
			if r.Type().PkgPath() != reflect.TypeOf(flwor{}).PkgPath() {
				return 0 // only this package's tree holds clauses
			}
			n := 0
			for i := 0; i < r.NumField(); i++ {
				n += walk(r.Field(i))
			}
			return n
		case reflect.Slice, reflect.Array:
			n := 0
			for i := 0; i < r.Len(); i++ {
				n += walk(r.Index(i))
			}
			return n
		}
		return 0
	}
	return walk(reflect.ValueOf(v))
}

// joinPair returns a query twice: as written, which the planner turns into a
// join, and with the marked comparison wrapped in boolean(), which it does
// not recognise and so leaves as the nested loop. Same meaning, same errors.
func joinPair(src string) (join, loop string) {
	join = strings.NewReplacer("«", "", "»", "").Replace(src)
	loop = strings.NewReplacer("«", "boolean(", "»", ")").Replace(src)
	return join, loop
}

func runQuery(t *testing.T, q *Query) string {
	t.Helper()
	seq, err := q.Eval(xpath.NewContext(nil, xpath.Builtins()))
	if err != nil {
		return "ERROR " + err.Error()
	}
	var parts []string
	for _, it := range seq {
		parts = append(parts, fmt.Sprint(itemText(it)))
	}
	return strings.Join(parts, " ")
}

func itemText(it any) string {
	if v, ok := it.(interface{ StringValue() string }); ok {
		return v.StringValue()
	}
	if v, ok := it.(interface{ String() string }); ok {
		return v.String()
	}
	return "?"
}

// TestJoinMatchesNestedLoop runs each query as a join and as the nested loop
// and requires the same result, in the same order, or the same error. The
// marked comparison is the one the join takes over.
func TestJoinMatchesNestedLoop(t *testing.T) {
	const ts = `let $ts := (<t n="1"><k>a</k><k>b</k></t>, <t n="2"><k>b</k></t>,
	  <t n="3"/>, <t n="4"><k>a</k></t>, <t n="5"><k>c</k><k>a</k></t>) `
	cases := []struct{ name, src, want string }{
		{"multi-valued keys, let-nested", ts + `
		  for $p in ("a", "b", "x", "a")
		  let $m := for $t in $ts where «$t/k = $p» return $t/@n
		  return string-join($m, ",")`, "1,4,5 1,2  1,4,5"},
		{"multi-valued outer key", ts + `
		  for $n in (1, 2)
		  let $p := if ($n = 1) then ("b", "a") else "c"
		  let $m := for $t in $ts where «$p = $t/k» return $t/@n
		  return string-join($m, ",")`, "1,2,4,5 5"},
		{"multi-valued outer key in one let", ts + `
		  let $ps := ("c", "b")
		  return string-join(for $t in $ts where «$t/k = $ps» return $t/@n, ",")`,
			"1,2,5"},
		{"same-FLWOR shape keeps outer-then-inner order", ts + `
		  for $p in ("b", "a"), $t in $ts where «$t/k = $p» return concat($p, $t/@n)`,
			"b1 b2 a1 a4 a5"},
		{"untyped against untyped", `
		  let $ps := (<p id="x"/>, <p id="y"/>, <p id="x"/>)
		  let $ts := (<t r="y"/>, <t r="x"/>, <t r="x"/>)
		  for $p in $ps
		  return count(for $t in $ts where «$t/@r = $p/@id» return $t)`, "2 1 2"},
		{"numeric against untyped casts to double", `
		  let $ts := (<t v="1.0"/>, <t v="01"/>, <t v=" 2 "/>, <t v="1e0"/>)
		  for $p in (1, 2, xs:decimal("1.0"))
		  return string-join(for $t in $ts where «$t/@v = $p» return $t/@v, "|")`,
			"1.0|01|1e0  2  1.0|01|1e0"},
		{"untyped against numeric that does not cast", `
		  let $ts := (<t v="1"/>, <t v="abc"/>)
		  for $p in (1, 2)
		  return count(for $t in $ts where «$t/@v = $p» return $t)`, ""},
		{"untyped that does not cast, outer never matches first", `
		  let $ts := (<t v="abc"/>, <t v="1"/>)
		  for $p in (1, 2)
		  return count(for $t in $ts where «$t/@v = $p» return $t)`, ""},
		{"mixed types are a type error", `
		  let $ts := (xs:date("2020-01-01"), "x")
		  for $p in ("x")
		  return count(for $t in $ts where «$t = $p» return $t)`, ""},
		{"string and number never compare", `
		  for $p in (1)
		  return count(for $t in ("1", "2") where «$t = $p» return $t)`, ""},
		{"NaN is never equal", `
		  let $ts := (<t v="NaN"/>, <t v="1"/>)
		  for $p in (xs:double("NaN"), 1)
		  return count(for $t in $ts where «$t/@v = $p» return $t)`, "0 1"},
		{"NaN is unequal to everything", `
		  let $ts := (<t v="NaN"/>, <t v="1"/>)
		  for $p in (xs:double("NaN"), 1)
		  return count(for $t in $ts where «$t/@v != $p» return $t)`, "2 1"},
		{"ordering operators", ts + `
		  for $p in ("a", "b", "c")
		  return (string-join(for $t in $ts where «$t/k < $p» return $t/@n, ","),
		          string-join(for $t in $ts where «$p >= $t/k» return $t/@n, ","))`,
			" 1,4,5 1,4,5 1,2,4,5 1,2,4,5 1,2,4,5"},
		{"exactly-one raises from the inner operand", `
		  let $is := (<i>1</i>, <i/>, <i>3</i>)
		  for $p in (<p income="20000"/>, <p income="1"/>)
		  return count(for $i in $is where «$p/@income > 5000 * exactly-one($i/text())» return $i)`,
			""},
		{"exactly-one with no outer tuple raises nothing", `
		  let $is := (<i>1</i>, <i/>)
		  for $p in ()
		  return count(for $i in $is where «$p/@income > 5000 * exactly-one($i/text())» return $i)`,
			""},
		{"XMark q11 shape", `
		  let $is := (<i>1</i>, <i>2</i>, <i>3.5</i>)
		  for $p in (<p income="20000"/>, <p income="5000"/>, <p/>, <p income="1e9"/>)
		  return count(for $i in $is where «$p/@income > 5000 * exactly-one($i/text())» return $i)`,
			"3 0 0 3"},
		{"empty keys on both sides", `
		  let $ts := (<t/>, <t k=""/>)
		  for $n in (1, 2)
		  let $p := ("", <e/>/@none)[$n]
		  return count(for $t in $ts where «$t/@k = $p» return $t)`, "1 0"},
		{"S rebuilt when its variables change", `
		  for $x in (1, 2, 10)
		  let $s := ($x, $x * 10)
		  return count(for $v in $s where «$v = 10» return $v)`, "1 0 1"},
		{"inner operand erroring for an item never reached by the loop", `
		  let $ts := (<t v="a"/>, <t v="b"/>)
		  for $p in ()
		  return for $t in $ts where «xs:integer($t/@v) = $p» return $t`, ""},
		{"inner operand erroring", `
		  let $ts := (<t v="1"/>, <t v="b"/>)
		  for $p in (1, 2)
		  return count(for $t in $ts where «xs:integer($t/@v) = $p» return $t)`, ""},
		{"join inside a recursive function", `
		  declare function local:f($n as xs:integer, $ks as xs:string*) as xs:string* {
		    if ($n = 0) then () else
		    (string-join(for $k in ("a", "b", "c") where «$k = $ks» return $k, ""),
		     local:f($n - 1, ($ks, "c")))
		  };
		  local:f(3, "a")`, "a ac ac"},
		{"nested joins (XMark q9 shape)", `
		  let $ca := (<c><b p="1"/><r i="x"/></c>, <c><b p="2"/><r i="y"/></c>, <c><b p="1"/><r i="y"/></c>)
		  let $ei := (<item id="y"><name>Y</name></item>, <item id="x"><name>X</name></item>)
		  for $p in (<p id="1"/>, <p id="2"/>, <p id="3"/>)
		  let $a := for $t in $ca where «$p/@id = $t/b/@p»
		    return let $n := for $t2 in $ei where «$t/r/@i = $t2/@id» return $t2
		    return $n/name/text()
		  return string-join($a, ",")`, "X,Y Y "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			jsrc, lsrc := joinPair(c.src)
			jq, err := Compile(jsrc, Options{})
			if err != nil {
				t.Fatal(err)
			}
			lq, err := Compile(lsrc, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if n := countJoins(jq); n == 0 {
				t.Fatalf("no join planned for %s", jsrc)
			}
			if n := countJoins(lq); n != 0 {
				t.Fatalf("%d joins planned for the nested-loop twin", n)
			}
			got, want := runQuery(t, jq), runQuery(t, lq)
			if got != want {
				t.Fatalf("join gave\n  %q\nnested loop gave\n  %q", got, want)
			}
			if c.want != "" && got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
			if c.want == "" && !strings.HasPrefix(got, "ERROR") && got != "" {
				t.Logf("result %q", got)
			}
		})
	}
}

// TestJoinDefaultCollation pins that the hash path is not taken under a
// default collation other than codepoint: "A" = "a" only case-blind.
func TestJoinDefaultCollation(t *testing.T) {
	src := `declare default collation
	  "http://www.w3.org/2005/xpath-functions/collation/html-ascii-case-insensitive";
	  let $ts := ("A", "b", "a")
	  for $p in ("a", "B")
	  return string-join(for $t in $ts where $t = $p return $t, ",")`
	q, err := Compile(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if countJoins(q) == 0 {
		t.Fatal("no join planned")
	}
	if got, want := runQuery(t, q), "A,a b"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestJoinErrors pins the error the join must reproduce, not only that the
// two forms agree.
func TestJoinErrors(t *testing.T) {
	for _, c := range []struct{ src, code string }{
		{`let $is := (<i>1</i>, <i/>)
		  for $p in (<p income="20000"/>)
		  return count(for $i in $is where $p/@income > 5000 * exactly-one($i/text()) return $i)`,
			"FORG0005"},
		{`let $ts := (<t v="1"/>, <t v="abc"/>)
		  for $p in (1, 2)
		  return count(for $t in $ts where $t/@v = $p return $t)`, "FORG0001"},
		{`for $p in (1)
		  return count(for $t in ("1", "2") where $t = $p return $t)`, "XPTY0004"},
	} {
		q, err := Compile(c.src, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if countJoins(q) == 0 {
			t.Fatalf("no join planned for %s", c.src)
		}
		if got := runQuery(t, q); !strings.Contains(got, c.code) {
			t.Errorf("got %q, want error %s", got, c.code)
		}
	}
}

// TestJoinConcurrentEval evaluates one compiled join query from many
// goroutines at once, each over its own data. Run with -race: the caches are
// per evaluation, so nothing is shared.
func TestJoinConcurrentEval(t *testing.T) {
	q, err := Compile(`declare variable $k external;
	  let $ts := for $i in 1 to 50 return <t k="{$i mod 7}"/>
	  for $p in (string($k), "x")
	  return count(for $t in $ts where $t/@k = $p return $t)`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if countJoins(q) == 0 {
		t.Fatal("no join planned")
	}
	want := func(k int) string {
		n := 0
		for i := 1; i <= 50; i++ {
			if i%7 == k {
				n++
			}
		}
		return fmt.Sprintf("%d 0", n)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for r := 0; r < 20; r++ {
				k := (g + r) % 7
				ctx := xpath.NewContext(nil, xpath.Builtins())
				ctx.Vars["k"] = xdm.One(xdm.NewInteger(int64(k)))
				seq, err := q.Eval(ctx)
				if err != nil {
					errs <- err.Error()
					return
				}
				var parts []string
				for _, it := range seq {
					parts = append(parts, itemText(it))
				}
				if got := strings.Join(parts, " "); got != want(k) {
					errs <- fmt.Sprintf("k=%d: got %q, want %q", k, got, want(k))
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// TestJoinRespectsHostFunctions pins that a host library binding a name the
// purity allowlist knows -- fn:exactly-one here -- to its own implementation
// keeps the nested loop: the host function is called once per pair, as
// written, and its impure answers are seen in order. A host that re-adds the
// built-in itself still gets the join.
func TestJoinRespectsHostFunctions(t *testing.T) {
	src := `let $is := (<i>1</i>, <i>2</i>, <i>3</i>)
	  for $p in (<p income="20000"/>, <p income="20000"/>)
	  return count(for $i in $is where «$p/@income > 5000 * exactly-one($i/text())» return $i)`
	jsrc, lsrc := joinPair(src)
	run := func(q *Query, lib xpath.FunctionLibrary) string {
		seq, err := q.Eval(xpath.NewContext(nil, lib))
		if err != nil {
			return "ERROR " + err.Error()
		}
		var parts []string
		for _, it := range seq {
			parts = append(parts, itemText(it))
		}
		return strings.Join(parts, " ")
	}
	name := xdm.QName{URI: xdm.NSFN, Local: "exactly-one"}
	host := func(calls *int) xpath.FunctionLibrary {
		lib := xpath.NewLibrary(xpath.Builtins())
		lib.Add(xpath.Function{Name: name, Arity: 1,
			Call: func(_ *xpath.Context, args []xdm.Sequence) (xdm.Sequence, error) {
				*calls++
				// Impure: the answer depends on how often it was asked.
				return xdm.One(xdm.NewInteger(int64(*calls))), nil
			}})
		return lib
	}
	jq, err := Compile(jsrc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	lq, err := Compile(lsrc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if countJoins(jq) == 0 {
		t.Fatal("no join planned")
	}
	var jc, lc int
	got, want := run(jq, host(&jc)), run(lq, host(&lc))
	if got != want || jc != lc {
		t.Fatalf("join gave %q with %d host calls, nested loop %q with %d",
			got, jc, want, lc)
	}
	if lc != 6 {
		t.Fatalf("nested loop made %d host calls, want 6", lc)
	}

	// The built-in re-added under its own name is still the built-in.
	same := xpath.NewLibrary(xpath.Builtins())
	b, _ := xpath.Builtins().Lookup(name, 1)
	same.Add(b)
	if got := run(jq, same); got != "3 3" {
		t.Fatalf("with the built-in re-added: got %q, want %q", got, "3 3")
	}
}

// TestJoinChargesWhatItHolds pins X1 from profiling round 2: the join holds S
// once, so it charges S once, not |S| per outer tuple as the nested loop
// would re-materialise it. 2,000 × 3,000 charged per tuple is 6,000,000
// items, over MaxItems; held once it is 3,000 plus the kept tuples. A result
// that really is too large is still refused.
func TestJoinChargesWhatItHolds(t *testing.T) {
	const data = `let $ps := for $i in 1 to 2000 return <p id="{$i}"/>
	  let $os := for $i in 1 to 3000 return <o k="{$i mod 2000}"/> `
	for _, c := range []struct{ name, src, want string }{
		{"2000 x 3000 equality join", data +
			`return sum(for $p in $ps return count(for $o in $os where $o/@k = $p/@id return $o))`, "2999"},
		{"2000 x 3000 kept tuples over budget", data +
			`return sum(for $p in $ps return count(for $o in $os where $o/@k != $p/@id return $o))`, "XPDY0130"},
	} {
		t.Run(c.name, func(t *testing.T) {
			q, err := Compile(c.src, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if countJoins(q) == 0 {
				t.Fatal("no join planned")
			}
			if got := runQuery(t, q); !strings.Contains(got, c.want) {
				t.Fatalf("got %.200q, want %s", got, c.want)
			}
		})
	}
}

// TestJoinRangeAndCastMatchNestedLoop is the differential test for the
// join's numeric paths: the outer key cast once to xs:double, and the sorted
// range lookup for < <= > >=. Every operator, with the inner key on either
// side, over keys that are one xs:double each (the range path), numeric of
// another type, untyped, empty or missing, against outer keys that are
// untyped, typed, several, empty, or do not cast. NaN, ±0 and ±INF included.
func TestJoinRangeAndCastMatchNestedLoop(t *testing.T) {
	const ts = `let $ts := (<t v="1"/>, <t v="-0"/>, <t v="2.5"/>, <t v="INF"/>,
	  <t v="0"/>, <t v="-INF"/>, <t v="NaN"/>, <t v="1"/>, <t/>, <t v="1e1"/>, <t v="0.1"/>) `
	inner := []string{
		"number($t/@v)",    // one xs:double each, NaN for none
		"xs:double($t/@v)", // empty for <t/>
		"(if ($t/@v castable as xs:integer) then xs:integer($t/@v) else xs:decimal(0))", // not doubles
		"$t/@v",              // untyped
		"(number($t/@v), 2)", // two keys
	}
	outer := []string{"$p/@v", "number($p/@v)", "($p/@v, $p/@w)", "xs:float($p/@v)"}
	ps := []string{`<p v="0"/>`, `<p v="-0"/>`, `<p v="1"/>`, `<p v="NaN"/>`, `<p v="INF"/>`,
		`<p v="-INF"/>`, `<p v="3"/>`, `<p/>`, `<p v="x"/>`, `<p v="1" w="2.5"/>`, `<p v="1.0"/>`, `<p v="0.1"/>`}
	ran := 0
	for _, op := range []string{"<", "<=", ">", ">=", "=", "!="} {
		for _, in := range inner {
			for _, out := range outer {
				for _, innerLeft := range []bool{true, false} {
					test := in + " " + op + " " + out
					if !innerLeft {
						test = out + " " + op + " " + in
					}
					for _, p := range ps {
						src := ts + `for $p in (` + p + `) return string-join(for $t in $ts where «` +
							test + `» return string($t/@v), ",")`
						jsrc, lsrc := joinPair(src)
						jq, err := Compile(jsrc, Options{})
						if err != nil {
							t.Fatal(err)
						}
						lq, err := Compile(lsrc, Options{})
						if err != nil {
							t.Fatal(err)
						}
						if countJoins(jq) == 0 || countJoins(lq) != 0 {
							t.Fatalf("planner: %s", jsrc)
						}
						got, want := runQuery(t, jq), runQuery(t, lq)
						if strings.HasPrefix(want, "ERROR") {
							got, want = errCode(got), errCode(want)
						}
						if got != want {
							t.Errorf("%s with %s:\n join   %q\n nested %q", test, p, got, want)
						}
						ran++
					}
				}
			}
		}
	}
	t.Logf("%d comparisons", ran)
}

func errCode(s string) string {
	if f := strings.Fields(s); len(f) > 1 && f[0] == "ERROR" {
		return strings.TrimSuffix(f[1], ":")
	}
	return s
}

// TestJoinNumericAllocations pins R4's cost: the XMark q11 shape, 300 people
// against 300 prices, must not cast the outer key once per pair nor bind the
// return value through a fresh context per tuple. Measured: 195,847
// allocations before, 69,854 after (most of the rest builds the data).
func TestJoinNumericAllocations(t *testing.T) {
	q, err := Compile(`
	  let $is := for $n in 1 to 300 return <i>{$n}</i>
	  let $ps := for $n in 1 to 300 return <p income="{$n * 1000}"/>
	  return sum(for $p in $ps
	    let $l := for $i in $is where $p/@income > 5000 * exactly-one($i/text()) return $i
	    return count($l))`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if countJoins(q) == 0 {
		t.Fatal("no join planned")
	}
	ctx := xpath.NewContext(nil, xpath.Builtins())
	var got xdm.Sequence
	allocs := testing.AllocsPerRun(3, func() {
		if got, err = q.Eval(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if len(got) != 1 || itemText(got[0]) != "8850" {
		t.Fatalf("got %v", got)
	}
	t.Logf("%.0f allocations", allocs)
	if allocs > 120000 {
		t.Errorf("%.0f allocations, want at most 120,000", allocs)
	}
}
