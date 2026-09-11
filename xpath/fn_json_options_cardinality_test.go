package xpath

import (
	"strings"
	"testing"
)

// TestJSONOptionsArgCardinality pins the cardinality of the $options parameter
// shared by the four JSON functions.
//
// F&O 3.1 declares it as map(*) with no occurrence indicator in all four
// signatures -- fn:parse-json (§17.5.1), fn:json-doc (§17.5.2),
// fn:json-to-xml (§17.5.3) and fn:xml-to-json (§17.5.4):
//
//	fn:parse-json($json-text as xs:string?, $options as map(*)) as item()?
//
// The empty sequence does not match map(*), so a PRESENT-but-empty second
// argument is XPTY0004. That is distinct from OMITTING the argument, which
// selects the one-argument form and is perfectly legal, and from supplying an
// EMPTY MAP, which matches map(*) and selects the defaults.
//
// Nothing on the call path enforced this. registerFn stores only a name and an
// arity; Function carries no parameter-type field, and builtinSignatures is
// consulted by "instance of function(...)" and never when a call is made. So
// the declared type was enforced solely by what the body happened to do with
// the argument, and singleMapArg folded "absent" and "present but empty" into
// the same nil-map return -- making parse-json("1",()) yield xs:double("1"),
// json-to-xml("1",()) return a document node and xml-to-json(...,()) return
// its JSON text, where all three must raise.
func TestJSONOptionsArgCardinality(t *testing.T) {
	// A json-to-xml-shaped document, the input fn:xml-to-json requires.
	node := `parse-xml('<map xmlns="http://www.w3.org/2005/xpath-functions">` +
		`<string key="k">v</string></map>')`

	for _, tc := range []struct {
		name string
		// empty writes () as the second argument: the defect under test.
		empty string
		// absent is the one-argument form: must keep working.
		absent string
		// emptyMap supplies map{}: matches map(*), must keep working.
		emptyMap string
		// nonMap supplies a present non-map: already XPTY0004, kept as a
		// control so a fix cannot regress it into something else.
		nonMap string
	}{
		{
			name:     "parse-json",
			empty:    `parse-json("1",())`,
			absent:   `parse-json("1")`,
			emptyMap: `parse-json("1",map{})`,
			nonMap:   `parse-json("1","nope")`,
		},
		{
			name:     "json-to-xml",
			empty:    `json-to-xml("1",())`,
			absent:   `json-to-xml("1")`,
			emptyMap: `json-to-xml("1",map{})`,
			nonMap:   `json-to-xml("1","nope")`,
		},
		{
			name:     "xml-to-json",
			empty:    `xml-to-json(` + node + `,())`,
			absent:   `xml-to-json(` + node + `)`,
			emptyMap: `xml-to-json(` + node + `,map{})`,
			nonMap:   `xml-to-json(` + node + `,"nope")`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The defect: () is present and does not match map(*).
			if _, err := evalJSONCardinality(tc.empty); !isXPTY0004(err) {
				t.Errorf("%s: an empty second argument must raise XPTY0004, got err %v", tc.empty, err)
			}
			// Boundary on the other side: omitting the argument is the
			// one-argument form and must NOT be tightened into an error.
			if _, err := evalJSONCardinality(tc.absent); err != nil {
				t.Errorf("%s: the one-argument form must keep working, got %v", tc.absent, err)
			}
			// map{} matches map(*): legal, and selects the defaults.
			if _, err := evalJSONCardinality(tc.emptyMap); err != nil {
				t.Errorf("%s: an empty map is a valid options map, got %v", tc.emptyMap, err)
			}
			// A present non-map was already XPTY0004 and must stay so.
			if _, err := evalJSONCardinality(tc.nonMap); !isXPTY0004(err) {
				t.Errorf("%s: a non-map options argument must raise XPTY0004, got %v", tc.nonMap, err)
			}
		})
	}

	// fn:json-doc carries the same signature (§17.5.2), but its own read is
	// disabled in this context and reports FOUT1170. The options argument is
	// decoded BEFORE the read, so the type error must win -- asserting that
	// ordering is what makes this case meaningful rather than accidentally
	// green on the wrong error.
	t.Run("json-doc", func(t *testing.T) {
		_, err := evalJSONCardinality(`json-doc("no-such-file-xyz.json",())`)
		if !isXPTY0004(err) {
			t.Errorf("json-doc with an empty options argument must raise XPTY0004 "+
				"before attempting the read, got %v", err)
		}
		// The control: a present non-map likewise reports the type error
		// rather than the read failure.
		_, err = evalJSONCardinality(`json-doc("no-such-file-xyz.json","nope")`)
		if !isXPTY0004(err) {
			t.Errorf("json-doc with a non-map options argument must raise XPTY0004, got %v", err)
		}
	})

	// An empty FIRST argument does not excuse the second. $json-text is
	// xs:string? so () there is legal and yields the empty sequence, but the
	// options argument is still present and still does not match map(*), so
	// the type error is raised rather than an empty result returned.
	t.Run("empty-first-argument-does-not-excuse", func(t *testing.T) {
		for _, expr := range []string{
			`parse-json((),())`,
			`json-to-xml((),())`,
			`xml-to-json((),())`,
		} {
			if _, err := evalJSONCardinality(expr); !isXPTY0004(err) {
				t.Errorf("%s: the options argument is still present, so XPTY0004 is "+
					"required rather than an empty result, got %v", expr, err)
			}
		}
	})
}

// isXPTY0004 reports whether err is the XPath type error. The code is asserted
// rather than the message so the test turns on the behavioural difference --
// raising versus not raising -- and not on wording.
func isXPTY0004(err error) bool {
	return err != nil && strings.Contains(err.Error(), "XPTY0004")
}

// evalJSONCardinality evaluates expr under XPath 3.1.
func evalJSONCardinality(expr string) (int, error) {
	ctx := NewContext(nil, Builtins())
	ctx.Version, ctx.LibraryVersion = XPath31, XPath31
	seq, err := Eval(expr, ctx, nil)
	return len(seq), err
}
