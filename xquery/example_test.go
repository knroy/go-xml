package xquery_test

import (
	"fmt"
	"os"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xpath"
	"github.com/knroy/go-xml/xquery"
	"github.com/knroy/go-xml/xslt"
)

// Eval compiles and runs a query in one step.
func ExampleEval() {
	seq, err := xquery.Eval(`for $i in 1 to 3 return $i * $i`,
		xpath.NewContext(nil, xpath.Builtins()), xquery.Options{})
	if err != nil {
		panic(err)
	}
	for _, item := range seq {
		fmt.Print(item, " ")
	}
	// Output: 1 4 9
}

// A compiled Query is immutable and safe to evaluate concurrently, so the
// cost of parsing is paid once however many times it runs.
func ExampleCompile() {
	q, err := xquery.Compile(`<sum>{ 1 + 2 }</sum>`, xquery.Options{})
	if err != nil {
		panic(err)
	}
	seq, err := q.Eval(xpath.NewContext(nil, xpath.Builtins()))
	if err != nil {
		panic(err)
	}
	// A query returns a sequence; serialising it is a separate step.
	if err := xslt.Serialize(os.Stdout, seq,
		xslt.OutputSettings{OmitXMLDecl: true}, nil); err != nil {
		panic(err)
	}
	// Output: <sum>3</sum>
}

// Binding the parsed document as the context item makes path expressions
// resolve against it, exactly as they do in XPath.
func ExampleEval_document() {
	doc, err := xdm.ParseString(
		`<books><book price="30"><t>A</t></book><book price="10"><t>B</t></book></books>`,
		xdm.ParseOptions{})
	if err != nil {
		panic(err)
	}
	ctx := xpath.NewContext(doc.Root, xpath.Builtins())

	seq, err := xquery.Eval(
		`for $b in //book order by xs:decimal($b/@price) return string($b/t)`,
		ctx, xquery.Options{})
	if err != nil {
		panic(err)
	}
	fmt.Println(seq)
	// Output: [B A]
}

// An external variable is declared by the query and bound by the caller. The
// binding lives on the context rather than on the Query, which is what lets
// one compiled query serve many different bindings at once.
func ExampleQuery_Eval_externalVariable() {
	q, err := xquery.Compile(
		`declare variable $who external; concat("hello ", $who)`,
		xquery.Options{})
	if err != nil {
		panic(err)
	}
	ctx := xpath.NewContext(nil, xpath.Builtins())
	ctx.Vars = map[string]xdm.Sequence{"who": {xdm.NewString("world")}}

	seq, err := q.Eval(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(seq)
	// Output: [hello world]
}

// The prolog can state how the result should be serialised. Evaluating and
// serialising are separate steps, so those parameters are handed back rather
// than acted on -- the caller decides whether to honour them.
func ExampleQuery_SerializationOptions() {
	// "output" is not a predeclared prefix, so it must be bound first.
	q, err := xquery.Compile(`
		declare namespace output =
			"http://www.w3.org/2010/xslt-xquery-serialization";
		declare option output:method "json";
		1`, xquery.Options{})
	if err != nil {
		panic(err)
	}
	fmt.Println(q.SerializationOptions()["method"])
	// Output: json
}

// Errors carry the code the specification gives them, so a caller can match
// on the code rather than on the prose.
func ExampleEval_errors() {
	_, err := xquery.Eval(`1 div 0`,
		xpath.NewContext(nil, xpath.Builtins()), xquery.Options{})
	fmt.Println(err)
	// Output: FOAR0001: division by zero
}
