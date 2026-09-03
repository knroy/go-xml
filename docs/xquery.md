# XQuery

XQuery 3.1, measured at **99.99%** of the W3C QT3 suite (29,800 of 29,803 in
scope). What is here is the language on top of XPath: constructors, FLWOR, the
prolog, and the expression forms that are XQuery's alone. Expressions
themselves are compiled by [`xpath`](../xpath/), which is at 100% of the same
suite for 2.0, 3.0 and 3.1.

```
go get github.com/knroy/go-xml
```

## Two calls

`Eval` compiles and runs in one step. `Compile` gives you a `*Query` you can
run many times.

```go
seq, err := xquery.Eval(`for $i in 1 to 3 return $i * $i`,
    xpath.NewContext(nil, xpath.Builtins()), xquery.Options{})
// seq is 1, 4, 9
```

A `*Query` is immutable and **safe for concurrent use** — compile once at
startup, evaluate from as many goroutines as you like:

```go
q, err := xquery.Compile(src, xquery.Options{})
if err != nil {
    return err // a static error: XPST0003, XPST0008, XQST0059 …
}
seq, err := q.Eval(ctx) // a dynamic error: XPTY0004, FOAR0001, XQDY0025 …
```

The split is not cosmetic. Compiling resolves every namespace, fixes the shape
of every constructor and compiles every expression; nothing about a `Query`
changes when it runs. That is what makes the concurrency safe, and it is why
a syntax error can never reach you from `Eval`.

## Getting output

`Eval` returns an `xdm.Sequence` — items, not text. Turning that into XML,
HTML, JSON or text is a separate step, and it lives in `xslt`:

```go
import "github.com/knroy/go-xml/xslt"

seq, err := xquery.Eval(`<sum>{ 1 + 2 }</sum>`, ctx, xquery.Options{})
err = xslt.Serialize(os.Stdout, seq, xslt.OutputSettings{OmitXMLDecl: true}, nil)
// <sum>3</sum>
```

Without `OmitXMLDecl` you get `<?xml version="1.0" encoding="UTF-8"?>` first,
which is correct for a document and usually not what you want for a fragment.

The two steps are separate because only the first belongs to XQuery: a query
produces a sequence, and what you do with it is yours. But the *parameters*
for the second step can be stated in the query, and `SerializationOptions`
hands them to you rather than making you re-parse the prolog:

```go
q, _ := xquery.Compile(`
    declare namespace output = "http://www.w3.org/2010/xslt-xquery-serialization";
    declare option output:method "json";
    declare option output:indent "yes";
    1`, xquery.Options{})

q.SerializationOptions() // map[indent:yes method:json]
```

Note the `declare namespace` line. `output` is **not** one of the predeclared
prefixes, so a query that uses `output:method` without binding it first gets
`XPST0081`.

## Querying a document

Bind the document as the context item and paths work as they do in XPath:

```go
doc, err := xdm.ParseString(src, xdm.ParseOptions{})
ctx := xpath.NewContext(doc.Root, xpath.Builtins())

seq, err := xquery.Eval(
    `for $b in //book order by xs:decimal($b/@price) return string($b/t)`,
    ctx, xquery.Options{})
```

`group by` works the way you would expect, and constructors nest inside it:

```go
`<r>{
   for $b in //book
   group by $p := if (xs:decimal($b/@price) gt 20) then "hi" else "lo"
   return <g k="{$p}">{ count($b) }</g>
 }</r>`
// <r><g k="hi">1</g><g k="lo">1</g></r>
```

## External variables

Declare them in the prolog and bind them on the context:

```go
ctx := xpath.NewContext(nil, xpath.Builtins())
ctx.Vars = map[string]xdm.Sequence{"who": {xdm.NewString("world")}}

q, _ := xquery.Compile(`declare variable $who external; concat("hello ", $who)`,
    xquery.Options{})
seq, _ := q.Eval(ctx) // "hello world"
```

`ctx.Vars` is keyed by the variable's local name for a name in no namespace.
Because the binding lives on the context rather than the query, one compiled
`Query` can be run against many different bindings concurrently.

## Options

The zero value is the specification's defaults, so `xquery.Options{}` is a
conformant starting point. Every field corresponds to a prolog declaration a
query could have made itself:

| Field | Prolog equivalent | Zero value |
|---|---|---|
| `BaseURI` | `declare base-uri` | none |
| `BoundarySpace` | `declare boundary-space` | `StripSpace` |
| `Construction` | `declare construction` | `PreserveTypes` |
| `DefaultElementNamespace` | `declare default element namespace` | no namespace |
| `Namespaces` | `declare namespace` | the predeclared set |

`Namespaces` adds bindings as though the prolog had declared them. Nine
prefixes are bound already and never need to appear: `xml`, `xs`, `xsi`, `fn`,
`local`, `math`, `map` and `array` from §4.1, plus `err` from §3.16 — which is
what lets `catch err:FODC0002` work with no declaration.

`BoundarySpace` is the one whose default surprises people. Whitespace that
only separates markup is **stripped**:

```xquery
<a>  <b/>  </a>          (: <a><b/></a> :)
```

Set `BoundarySpace: xquery.PreserveSpace` to keep it, which is exactly what
`declare boundary-space preserve` does.

## Errors carry their spec code

Every error is prefixed with the code the specification gives it, so you can
match on it rather than on prose:

```go
_, err := xquery.Eval(`1 div 0`, ctx, xquery.Options{})
// FOAR0001: division by zero
```

Static and dynamic errors are separated the way the specification requires,
and this is observable through `try`/`catch`: a **static** error (the `XPST`
and `XQST` prefixes) is not catchable, because the query never runs; a
**dynamic** error (`XPDY`, `XQDY`, `XPTY`, `XQTY`, and the `FO` family) is.

```xquery
try { 1 div 0 } catch * { "caught" }        (: "caught"  — dynamic :)
try { $undeclared } catch * { "caught" }    (: XPST0008  — static, escapes :)
```

## The version declaration

A module may open with `xquery version "1.0";`, `"3.0"` or `"3.1"`. The version
it names is recorded on the module's static context and is reachable from both
the parser and the evaluator. A module that names no version is compiled as
3.1: §4.1 leaves that case implementation-defined, and 3.1 is what this engine
implements. A version this processor does not implement — anything other than
those three — is `XQST0031`.

```xquery
xquery version "1.0"; declare option myopt "v"; true()
(: XPST0081 — 1.0 §4.16 requires an option name to be prefixed :)

xquery version "3.0"; declare option myopt "v"; true()
(: true()   — 3.0 §4.19 dropped that requirement :)
```

The engine still *implements* 3.1 almost everywhere. What the recorded version
buys is that the places where the versions are known to disagree can ask, and
three do:

| Rule | 1.0 | 3.0 and later |
| --- | --- | --- |
| Unprefixed `declare option` name (§4.16 / §4.19) | `XPST0081` | legal, ignored |
| Cast target naming a type not in scope (§3.13.2) | `XPST0051` | `XQST0052` |
| Variable circularity through a function body (§4.14 / §4.16) | `XQST0054` (static) | `XQDY0054` (dynamic) |

The declared version also selects the expression language each expression is
compiled at, since XQuery 1.0 is defined over XPath 2.0, 3.0 over XPath 3.0 and
3.1 over XPath 3.1.

Everything else remains judged by 3.1's rules whatever the module declares —
including the empty operand of `ordered {}` and `unordered {}`, an unprefixed
pragma name, and `XQST0134` for a bare `namespace-node()` step. Those are
accepted at every version rather than refused at the earlier ones. This is a
permissive divergence: a 1.0 module that a conforming 1.0 processor would
reject is accepted here, but no module is given a wrong *answer*.

## What is not implemented

Two declarations parse and are then refused, rather than being mis-parsed.
Both need a module store this package does not have:

* **`import module`** raises `XQST0059`.
* **`import schema`** leaves the in-scope schema definitions empty, so
  `validate { … }` raises `XQDY0084`.

Everything else in 3.1 is implemented: every FLWOR clause — `for`, `let`,
`where`, `group by`, `order by`, `count`, and both the tumbling and sliding
window clauses; direct and computed
constructors; `try`/`catch`; `switch`; `typeswitch`; quantified expressions;
`ordered`/`unordered`; the extension expression; and the string constructor.

The remaining 3 failures are a long tail rather than a missing feature, and
each is understood:

* **Pathological map cost.** `same-key-023` builds 421,875 keys and performs an
  O(n) `map:put` and `map:remove` per key. `MapItem` is an entries slice plus a
  rebuilt index, so both are linear in the map's size no matter how small the
  constant factor is made; terminating needs a persistent map — a HAMT, or a
  copy-on-write overlay. Its sibling `same-key-024`, at 11,250 keys, passes.

* **`K2-sequenceExprTypeswitch-5`** wants a static `XPST0008` for a variable
  named in an unreached `typeswitch` branch. A check restricted to
  sibling-clause variables passed eleven tests and then broke
  `K2-ForExprWithout-8`, where a sibling's name is shadowed by an outer
  binding — so seeing it free proves nothing. A sound check needs the parser to
  track in-scope variables, which it does not do today.

* **One demo query.** `app-Demos/RexParser` fails to parse at offset 0 with
  `XPST0003`. Its sibling `sudoku` was fixed by making a FLWOR in a conditional
  branch belong to that branch; this one still fails for a different reason in
  the same family. It is a large real-world query rather than a targeted case,
  which is what makes it worth keeping in view.

The groups this section used to list have all been fixed: schema-aware
`validate lax`, namespace non-inheritance on constructed elements, zero-length
text in `document {}`, the `sudoku` demo, a prolog base URI that is relative,
and `eqname-007`'s prefix bound by an enclosing element constructor.

See [known-gaps.md](known-gaps.md) for the variable-name/subtraction defect,
which the suite does not cover.

## Security

The same defaults as the rest of the library. A query cannot read a file or
open a socket unless you give it something that can: `fn:doc` and
`fn:collection` resolve through a resolver that is **nil by default**, and a
nil resolver fetches nothing. See [security.md](security.md).

Note that a query is *code*. Compiling one from untrusted input is closer to
`eval` than to parsing a document — the sandbox above bounds what it can
reach, but an untrusted query can still spend arbitrary CPU and memory. Put a
timeout and a memory bound around it, as [server.md](server.md) shows for the
validation endpoint.
