# XQuery

XQuery 3.1, measured at **100.00%** of the W3C QT3 suite (30,345 of 30,346 in
scope). That percentage fell from 99.96% when `import schema` was implemented:
416 previously-skipped cases entered the denominator and 315 of them pass, so
the passing count rose by 315 while the rate fell. No case that passed before
fails now. What is here is the language on top of XPath: constructors, FLWOR, the
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
`xsd.Load`, or from a stylesheet's `Schema()`) instead of `Source`, which is
how one schema is shared between a stylesheet and a query without loading it
twice and risking the two disagreeing.

`SchemaResolver` supplies a schema the store does not have. It is
`xsd.Resolver`, deliberately: the **same** resolver is handed to `xsd` for the
imported schema's own `xs:include` and `xs:import`, so an imported schema can
reach no further than the query's import was granted. `MaxSchemaBytes` bounds
the total schema text one compilation reads, cumulatively across every import,
and exceeding it **fails** the compilation with an error wrapping
`xdm.ErrResourceLimit` rather than compiling against a truncated schema.

**Not implemented on this path.** Typed *input*: a source document does not
arrive schema-validated, so a node still atomises as untyped however the query
imported. `SchemaUnionTypes` and `SchemaListTypes` — the two optional
interfaces `xslt` also implements — are not implemented here, so a union or
list type an imported schema defines resolves as a name but does not match a
value.

## What is not implemented

Everything in 3.1 is implemented, `import module` and `import schema` included: every FLWOR clause — `for`, `let`,
`where`, `group by`, `order by`, `count`, and both the tumbling and sliding
window clauses; direct and computed
constructors; `try`/`catch`; `switch`; `typeswitch`; quantified expressions;
`ordered`/`unordered`; the extension expression; and the string constructor.

The remaining 2 failures are a long tail rather than a missing feature, and
each is understood:

* **`K2-sequenceExprTypeswitch-5`** wants a static `XPST0008` for a variable
  named in an unreached `typeswitch` branch. A check restricted to
  sibling-clause variables passed eleven tests and then broke
  `K2-ForExprWithout-8`, where a sibling's name is shadowed by an outer
  binding — so seeing it free proves nothing. A sound check needs the parser to
  track in-scope variables, which it does not do today.

The groups this section used to list have all been fixed: the `RexParser`
demo, schema-aware
`validate lax`, namespace non-inheritance on constructed elements, zero-length
text in `document {}`, the `sudoku` demo, a prolog base URI that is relative,
and `eqname-007`'s prefix bound by an enclosing element constructor.

See [known-gaps.md](known-gaps.md) for the variable-name/subtraction defect,
which the suite does not cover.

## Security

The same defaults as the rest of the library. A query cannot read a file or
open a socket unless you give it something that can: `fn:doc`, `fn:collection`
`import module` and `import schema` all resolve through a resolver that is
**nil by default**, and a nil resolver fetches nothing. An `import module ...
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
