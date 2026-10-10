# XQuery

XQuery 3.1, measured at **100.00%** of the W3C QT3 suite (30,516 of 30,517 in
scope). That percentage fell from 99.96% when `import schema` was implemented:
416 previously-skipped cases entered the denominator and 315 of them pass, so
the passing count rose by 315 while the rate fell. No case that passed before
fails now. What is here is the language on top of XPath: constructors, FLWOR, the
prolog, and the expression forms that are XQuery's alone. Expressions
themselves are compiled by [`xpath`](../xpath/), which is at 100% of the same
suite for 2.0, 3.0 and 3.1.

```
go get github.com/knroy/go-xml/v2
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
import "github.com/knroy/go-xml/v2/xslt"

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

The map holds the values as written. A name that is not a serialization
parameter is `XQST0109` when the query compiles; a value outside the
parameter's type in the Serialization 3.1 schema — `standalone "maybe"`,
`method "foo"`, `json-node-output-method "json"` — is `SEPM0016` when
`xslt.SetSerializationParam` reads it, which is how the command line and the
test harness serialize. A parameter document named by
`output:parameter-document` is held to the same schema through
`xslt.ApplyParameterDocument`, with `SEPM0017` for a bad value and `SEPM0019`
for a parameter given twice; `fn:serialize` uses the same check.

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
| `Modules` | *(the module store)* | empty |
| `ModuleResolver` | *(none)* | **nil — nothing is fetched** |
| `MaxModules` | *(none)* | `DefaultMaxModules` (512) |
| `MaxModuleBytes` | *(none)* | `DefaultMaxModuleBytes` (16 MB) |

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

## Importing modules

`import module` finds a library module by its **target namespace** (§4.12).
The `at` clause is a set of location *hints*, which the specification lets a
processor ignore — and with no resolver configured, this one does not merely
ignore them, it never opens them:

```go
lib := xquery.Module{
    Namespace: "http://example.com/util",
    Source: `module namespace u="http://example.com/util";
             declare function u:double($x) { $x * 2 };`,
}
q, err := xquery.Compile(
    `import module namespace u="http://example.com/util"; u:double(21)`,
    xquery.Options{Modules: []xquery.Module{lib}})
```

An imported module contributes its **public** functions and variables. A
`%private` one stays behind — but is still visible to the module's own bodies,
which is the half of §4.15 that is easy to get backwards: private scopes a
declaration *to* its module rather than withholding it from it.

Modules may be **mutually recursive**. XQuery 1.0 forbade any cycle of imports
with `XQST0093`; 3.0 removed that rule, so two modules importing each other is
legal and only a circularity among the *values* is an error — `XQDY0054`, and
dynamic, because once the imports may loop there is no static order for two
modules' variables to be in.

To supply modules at run time rather than up front, set a `ModuleResolver`.
`MapModuleResolver` answers from a table and reads nothing:

```go
opts := xquery.Options{ModuleResolver: xquery.MapModuleResolver{
    Modules: map[string]string{"http://example.com/util": src},
}}
```

Writing one that reads the filesystem or the network is a deliberate grant to
whoever wrote the query. See [security.md](security.md).

A module may import its own target namespace; §4.12 allows it in as many
words, and the loader treats the revisit like any other cycle.

### Loading a module at run time

`fn:load-xquery-module` (F&O 3.1 §14.6.1) is implemented. It returns
`map{"variables": map{QName: value}, "functions": map{QName: map{arity:
function}}}` for the public declarations of the module whose target namespace
it is given, and the function items run in the module's own context wherever
they are called:

```xquery
load-xquery-module("http://example.com/util")("functions")
    (QName("http://example.com/util", "double"))(1)(21)   (: 42 :)
```

It reads modules through exactly what `import module` reads through, and
nothing else. From a query that is the query's own `Options.Modules` and
`Options.ModuleResolver`; from XPath it is `xpath.Env.Modules`, and from
XSLT `xslt.TransformOptions.Modules` — both nil by default, so a call finds
nothing and raises `FOQM0002`, the same answer an import gives as `XQST0059`.
The `location-hints` option is passed to the resolver, resolved against the
caller's static base URI. Loading charges the caller's module budgets
(`MaxModules`, `MaxModuleBytes`) and the module's evaluation spends the
caller's item, byte, entity and node budgets; running out is a resource-limit
error, never `FOQM0003`.

The options are all honoured: `xquery-version` (anything up to 3.1;
higher is `FOQM0006`), `location-hints`, `context-item`, `variables` (also for
external variables of modules the loaded one imports) and `vendor-options`,
which is checked and then ignored because no vendor option is recognised. A
supplied variable or context item that does not match its declared type is
`FOQM0005`; a static error in the module is `FOQM0003`; an empty URI is
`FOQM0001`.

The function lives in `xpath`'s library, but the processor behind it lives
here, and `xpath` cannot import `xquery`. So **a Go program gets it by
importing this package** — a blank `import _ "github.com/knroy/go-xml/v2/xquery"`
is enough for a stylesheet or an XPath expression to use it. A program that
does not import `xquery` gets `FOQM0006`, which the specification defines for
a processor without the function. The `go-xml` command links `xquery`, so both
`go-xml -xsl` and `go-xml xquery` have it.

## Importing schemas

`import schema` finds a schema by its **target namespace** (§4.11), and the
`at` clause is a set of location *hints* on exactly the same terms `import
module`'s is: with no resolver configured they are never opened.

What an import buys is the schema's components in the **static context** —
§2.1.1's in-scope schema definitions — which is what `cast as`, `castable as`,
`instance of`, `element(*, T)`, `schema-element(E)` and `validate` are all
judged against:

```go
q, err := xquery.Compile(
    `import schema namespace h = "http://example.org/hats";
     8 cast as h:hatsize`,
    xquery.Options{Schemas: []xquery.Schema{
        {Namespace: "http://example.org/hats", Source: hatsXSD},
    }})
```

The components reach the static context **as each import is read**, which is
what makes the feature work at all: XQuery resolves type names while parsing
rather than after, so `h:hatsize` is decided by whether the static context
knows the name at the moment the parser reaches it. That is why the import is
followed where it stands rather than at the end of the prolog — a function
signature is parsed where *it* stands, so

```
import schema namespace h = "http://example.org/hats";
declare function local:f($a as h:hatsize) { $a };
```

needs the schema installed by the second line, not merely by the body.
(`import module` is followed *after* the body, because a module contributes
functions and variables, and those are resolved late.)

The facets the schema author wrote are applied, not merely the base type: a
`hatsize` restricted to 4–12 makes `99 castable as h:hatsize` false.

### Casting to a schema type

An imported simple type is a **cast target** and a **constructor function**,
which are the same thing: §3.14.2 admits any simple type in the in-scope schema
types as a cast target, and a constructor is *defined* as that cast. So
`h:hatsize("8")` and `"8" cast as h:hatsize?` are one expression, and both
apply the schema's facets.

The rules that are easy to get subtly wrong, and what this implementation
does:

| Target | Behaviour |
|---|---|
| An atomic restriction | Cast to the nearest **built-in ancestor**, not to the XSD primitive, then check the facets. A restriction of `xs:integer` yields an `xs:integer`, so `instance of xs:integer` is true of what the cast just produced. |
| A **pure** union | Member types are tried in declaration order and the first that accepts the value wins, so the result is an instance of a *member*, never of the union. The member cast runs **first** and the schema is asked about **its** result: `123.12 cast as` a union over `xs:integer` yields `123`, because a cast converts. A member that is itself a restriction still has its own facets applied. |
| An **impure** or **restricted** union — one carrying facets, or holding a list type | A legal cast target, decided by the schema's own validation. `castable as` answers `true` or `false`; `cast as` raises `FORG0001`. A source that is not `xs:string` or `xs:untypedAtomic` may reach only the union's **atomic** members, because F&O §18.3 defines the cast to a list type from a string alone. |
| A list type | Castable is the schema's answer over the whole value; the result is one item per whitespace-separated token. |

The purity rule of §2.5 is a rule about **item types**, not about casts. A
union carrying facets is still refused in `instance of`, in `treat as` and in a
function signature — a value must not stand in for a faceted union it may not
satisfy, which is the XSD 1.0 error XSD 1.1 §3.16.6.3 corrected — while the
same union is a perfectly good cast target, because a cast has a lexical form
in hand and can put the facets to the schema.

`import schema default element namespace "…"` additionally makes the imported
namespace the default element **and type** namespace for the rest of the
module, so the type is nameable with no prefix.

A `validate` expression is assessed against the imported schema and the result
is **annotated**, so `validate strict { <hat>8</hat> } instance of element(*,
hatsize)` is true. Strict assessment of an element the schema does not declare
at top level is `XQDY0084`; an element that is declared and found invalid is
`XQDY0027`. A query that imported no schema is unchanged: `validate strict` is
`XQDY0084` and `validate lax` is a skipped assessment that yields its operand.

A `Schema` may carry already-assembled `Components` (an `*xsd.Schema` from
`xsd.Load`, or from a stylesheet's or another query's `Schema()`) instead of `Source`, which is
how one schema is shared between a stylesheet and a query without loading it
twice and risking the two disagreeing.

`SchemaResolver` supplies a schema the store does not have. It is
`xsd.Resolver`, deliberately: the **same** resolver is handed to `xsd` for the
imported schema's own `xs:include` and `xs:import`, so an imported schema can
reach no further than the query's import was granted. `MaxSchemaBytes` bounds
the total schema text one compilation reads, cumulatively across every import,
and exceeding it **fails** the compilation with an error wrapping
`xdm.ErrResourceLimit` rather than compiling against a truncated schema.

**Not implemented on this path.** Automatic validation of the input: a source
document is not validated because the query imported a schema, so it stays
untyped until the query asks. `validate strict { . }` types it, as does a
caller that validates it first with `xsd.Schema.ValidateCopy`; a
validated node then atomises to its typed value. `SchemaUnionTypes` and `SchemaListTypes` — the two optional
interfaces `xslt` also implements — are not implemented here, so a union or
list type an imported schema defines resolves as a name but does not match a
value.

## Running XSLT from a query: `fn:transform`

`fn:transform` (F&O 3.1 §14.7.1) runs an XSLT 3.0 transformation when the
program links the `xslt` package — importing it registers the processor with
`xpath`, which cannot import `xslt` itself. `go-xml xquery` links it. A program
that imports only `xpath` or `xquery` gets `FOXT0004`, the specification's code
for "no processor".

```go
import "github.com/knroy/go-xml/v2/xslt" // linking xslt registers the processor

res, err := xslt.NewFileResolver("/srv/xsl") // all the transformation may read
ctx := xpath.NewContext(nil, xpath.Builtins(), func(e *xpath.Env) { e.Docs = res })
seq, err := xquery.Eval(`transform(map{
    'stylesheet-location': 'render.xsl', 'source-location': 'in.xml'})?output`,
    ctx, xquery.Options{BaseURI: "file:///srv/xsl/"}) // relative locations resolve here
```

The nested transformation inherits the query's `Context` and nothing else:
`stylesheet-location`, `source-location`, its `xsl:include`s and its own
`fn:doc` resolve through `Env.Docs` only, so a query with no resolver gets the
same `FOXT0002` refusal a stylesheet with none gets, and `package-name` is
refused (there is no package resolver). Its recursion depth continues the
query's and is bounded by `Env.MaxDepth`, so a stylesheet that calls back into
a query that transforms again is refused with `XPDY0001` rather than
exhausting the stack. The options F&O defines that are accepted without effect
are listed in [known-gaps.md](known-gaps.md).

## What is not implemented

Everything in 3.1 is implemented, `import module` and `import schema` included: every FLWOR clause — `for`, `let`,
`where`, `group by`, `order by`, `count`, and both the tumbling and sliding
window clauses; direct and computed
constructors; `try`/`catch`; `switch`; `typeswitch`; quantified expressions;
`ordered`/`unordered`; the extension expression; and the string constructor.

The remaining failure is not a missing feature, and it is understood:

* **`prod-ContextItemDecl/contextDecl-052`** is a W3C fixture defect: the
  catalog registers `libmodule-3.xq` under one namespace and the file declares
  another, so `XQST0059` is correct and precedes the wanted `XQST0113`. See
  [conformance-gaps.md](conformance-gaps.md).

`K2-sequenceExprTypeswitch-5`, which this section used to list, now passes:
the `XPST0008` for a variable named in an unreached `typeswitch` branch is
judged against the live scope. The groups this section used to list have all
been fixed: the `RexParser`
demo, schema-aware
`validate lax`, namespace non-inheritance on constructed elements, zero-length
text in `document {}`, the `sudoku` demo, a prolog base URI that is relative,
and `eqname-007`'s prefix bound by an enclosing element constructor.

See [known-gaps.md](known-gaps.md) for the variable-name/subtraction defect,
which the suite does not cover.

## Command line

```
go-xml xquery -q QUERY.xq [flags] [INPUT.xml]
```

`INPUT.xml`, when given, is the context item; without it the query has none.
`-p name=value` binds an external variable as `xs:string`, and the result is
written through `xslt.Serialize` with the parameters from
`SerializationOptions` — an unstated method is chosen from the result.
`import module ... at`, `fn:load-xquery-module`, `fn:doc` and (with
`-allow-unparsed-text`) `fn:unparsed-text` read only the query's own directory
and the `-allow-dir` roots; a location hint outside them is refused with
`XQST0059` (`FOQM0002` from `fn:load-xquery-module`). `fn:transform` reads its
stylesheet and source through the same roots, and a location outside them is
refused with `FOXT0002`. A stylesheet run by `go-xml -xsl` reads
`fn:load-xquery-module` modules from its `-allow-dir` roots the same way. `import
schema ... at` resolves through the same roots, and `-catalog` answers the W3C
schemas from local copies.

`-validate strict|lax` validates `INPUT.xml` against the schema the query
imports (`Query.Schema`, the merged set of its `import schema` declarations)
before the query runs, as the transform's `-validate` does for
`xsl:import-schema`. A validated document carries its schema types, so it
satisfies a declared typed context item such as
`declare context item as document-node(schema-element(h:hat)) external`,
which an unvalidated document fails with `XPTY0004`, and its values atomise to
typed values. `strict` requires the document element to be declared; `lax`
checks it only if it is. `-validate` is refused when the query imports no
schema or there is no input. Run `go-xml xquery -h` for every flag.

## Security

The same defaults as the rest of the library. A query cannot read a file or
open a socket unless you give it something that can: `fn:doc`, `fn:collection`,
`fn:transform`, `import module`, `fn:load-xquery-module` and `import schema`
all resolve through a resolver that is **nil by default**, and a nil resolver
fetches nothing. An `import module ...
at "/etc/passwd"` is not attempted and refused — it is never opened, and the
import fails with `XQST0059`. The same holds word for word for `import schema
... at "/etc/passwd"`, and the refusal names `Options.SchemaResolver` rather
than the path, which is what distinguishes "nothing was configured" from "that
file could not be read". See [security.md](security.md).

Note that a query is *code*. Compiling one from untrusted input is closer to
`eval` than to parsing a document — the sandbox above bounds what it can
reach, but an untrusted query can still spend arbitrary CPU and memory. Put a
timeout and a memory bound around it, as [server.md](server.md) shows for the
validation endpoint.
