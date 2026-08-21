# XSD

`xsd` implements XML Schema 1.0 and 1.1. This page is the reference; if you
are deciding *whether* XSD is the right tool at all, read
[validation.md](validation.md) first — it starts with what schema validation
cannot check, which is the part most likely to save you time.

XSD is the only schema language implemented. A DTD is parsed past rather than
applied, and RELAX NG is not implemented at all; see
[validation.md](validation.md#dtd-and-relax-ng) for what that means in
practice.

## Loading and validating

```go
schema, err := xsd.LoadFile("invoice.xsd", xsd.Options{})
if err != nil {
    return err
}

doc, err := xdm.ParseString(src, xdm.ParseOptions{})
if err != nil {
    return err
}

if err := schema.Validate(doc.Root, xsd.ValidateOptions{}); err != nil {
    return err
}
```

`Validate` returns nil for a valid document. Otherwise the error is a
`*xsd.ValidationErrors` holding one entry per failure, each carrying the
spec's own error code:

```go
var invalid *xsd.ValidationErrors
if errors.As(err, &invalid) {
    for _, e := range invalid.Errors {
        fmt.Printf("%s:%d:%d %s: %s\n", e.Path, e.Line, e.Column, e.Code, e.Message)
    }
}
```

The code — `cvc-complex-type.2.4.a`, `cvc-datatype-valid.1.2.1` — is what
lets a caller tell the kinds of failure apart without matching on message
text. Message wording is not part of the contract; codes are.

There are four ways in, differing only in where the schema comes from:

| | use when |
|---|---|
| `LoadFile(path, opts)` | one schema document on disk |
| `LoadFiles(paths, opts)` | several documents that together make one schema |
| `Load(root, baseURI, opts)` | the document is already parsed |
| `ParseSchema(root)` | one document, following no include or import |

`LoadFiles` is the one to reach for with a modular schema set. Handing it
every root document assembles them into a single schema, which is what UBL,
CII and the other large vocabularies expect.

## Choosing the version

1.1 is opt-in:

```go
xsd.Options{Version: xsd.Version11}
```

It is not the default because **1.1 changes which documents are valid**, and a
schema written for 1.0 must not acquire its relaxations by accident. Under
`Version11` you get `xs:assert`, conditional type assignment through
`xs:alternative` and inheritable attributes, `xs:openContent` and
`xs:defaultOpenContent`, `xs:override`, the `notNamespace` and `notQName`
wildcard forms, `explicitTimezone`, conditional inclusion through the
versioning attributes, and the 1.1 built-in types.

The 1.1 constructs are always *parsed*, whichever version is selected — a
schema that uses one is not rejected for it. Whether it is *honoured* is what
the version selects, and the distinction matters:

```go
// <xs:assert test="xs:int(a) gt 100"/> on the type of <r>
schema10.Validate(doc.Root, xsd.ValidateOptions{})   // nil — the assertion is not run
schema11.Validate(doc.Root, xsd.ValidateOptions{})   // cvc-assertion.3
```

So loading a 1.1 schema under the default version gives you a working 1.0
validator for it, silently missing the 1.1 constraints. **If a schema uses 1.1
features, select `Version11`.** Nothing warns you.

The exception is `notQName`, which is an error under 1.0 rather than ignored,
because it *narrows* a wildcard: ignoring it would accept documents the schema
means to exclude, where ignoring an assertion only fails to reject them. The
asymmetry is not principled — it is where the line happens to fall today.

## Checking the schema itself

Loading a schema does not check every constraint the spec places on schemas.
Two of them are a separate call:

```go
if err := schema.CheckConstraints(xsd.CheckOptions{}); err != nil {
    return err
}
```

That covers Unique Particle Attribution and Element Declarations Consistent.
They are separate because they are the expensive half and say nothing about
whether any instance document is valid — the same reason Xerces gates exactly
these behind `schema-full-checking`, default false.

`CheckOptions.LaxUPA` selects the permissive reading Saxon and XSV use, in
which two competing particles may be references to the same element
declaration. The strict reading rejects those. It is off by default because
strict is the conforming one, but schemas written against Saxon may rely on
it.

**Particle Valid (Restriction) is not checked.** A schema whose restriction
is unsound in that specific way is accepted here rather than reported. This is
a deliberate gap, and it is the company the implementation keeps: libxml2 does
not implement it at all, and Xerces leaves it off by default.

## Resolving schemaLocation

`include`, `import` and `redefine` name other documents, so following them
means fetching whatever the schema asks for. The default resolver reads from
disk only:

```go
xsd.Options{Resolver: &xsd.FileResolver{}}   // the default when Resolver is nil
```

To follow remote locations you have to say so, and say which hosts:

```go
xsd.Options{Resolver: &xsd.HTTPResolver{
    AllowHost: func(host string) bool { return host == "schemas.example.com" },
}}
```

`AllowHost` runs *before* the request, which makes it the place to refuse
loopback and private address ranges. Network resolution is off by default
because turning it on hands control of what this process fetches to whoever
wrote the schema.

In a server, prefer neither:

```go
xsd.Options{Resolver: &xsd.MapResolver{
    ByLocation:  map[string]string{"common.xsd": commonXSD},
    ByNamespace: map[string]string{"urn:example:common": commonXSD},
}}
```

`ByNamespace` answers an `import` that gives a namespace but no location.

`MapResolver` answers from an in-memory table and touches neither disk nor
network, so schema assembly cannot become an outbound request or a blocking
read.

### schemaLocation is a hint

The spec is explicit that `schemaLocation` offers a document rather than
requiring one, and this implementation follows that: an `include` or `import`
whose location cannot be resolved does not fail the schema. What that document
would have contributed is simply missing.

A reference that genuinely needed those components still fails — at the
reference, naming what is missing, and against the instance that reaches it.
That is both more useful than a load-time failure and where the spec puts it.

A `redefine` is the exception. Its children are defined in terms of what it
redefines, so an unresolvable location there is an error — unless the
`redefine` redefines nothing, in which case it asks nothing of the document
it names.

### xsi:schemaLocation is ignored by default

`xsi:schemaLocation` lives in the *instance document*. Honouring it lets
whoever supplied the document choose which schema it is judged against — a
document that fails can name a permissive schema and pass — so by default the
schema is the one the caller loaded and nothing else.

Where you do need it, it is opt-in and gated on an allowlist:

```go
extended, err := schema.WithInstanceLocations(doc.Root, xsd.InstanceLocationPolicy{
    AllowNamespace: func(ns string) bool { return ns == "urn:example:addenda" },
}, xsd.Options{Resolver: myResolver})
if err != nil {
    return err
}
err = extended.Validate(doc.Root, xsd.ValidateOptions{})
```

Three things about that shape are deliberate:

* **The zero policy grants nothing.** A nil `AllowNamespace` allows no
  namespace, so a policy that merely exists does not open the door.
  `AllowNoNamespace` is separate again, because `""` is not a namespace a
  caller thinks about and folding it in would grant it by accident.
* **A refused location is ignored, not an error.** §4.3.2 makes the attribute
  a hint, so declining to take it is not a fault in the document. A reference
  that really needed the components still fails, at the reference.
* **The receiver is not modified.** A `Schema` is immutable and shared, so
  this returns a new one. That means a fresh assembly per instance, which is
  why it is a separate call rather than something `Validate` does — a caller
  validating many documents against one schema should not pay for it.

The resolver still decides what can actually be fetched. Following untrusted
documents means pairing this with a `MapResolver`, or an `HTTPResolver` whose
`AllowHost` refuses everything you have not vouched for: the allowlist says
*which namespaces* an instance may extend, not *what it may reach*.

## The PSVI

`ValidateOptions.Annotate` writes the type of each validated node into its
`TypeAnnotation`:

```go
err := schema.Validate(doc.Root, xsd.ValidateOptions{Annotate: true})
```

That is the part of the post-schema-validation infoset the XPath and XSLT
layers consume — it is what makes `element(*, xs:date)` and typed value
comparison mean anything. It is off by default because **it mutates the tree
you passed in**, which also makes it the one option that is unsafe to use on a
tree shared between goroutines.

## Limits

Both entry points bound their work, because a schema or a document is input
like any other:

| option | default | bounds |
|---|---|---|
| `Options.MaxDocuments` | 512 | documents one assembly may read |
| `Options.ParseOptions` | refuses DOCTYPE | entity expansion in schema documents |
| `ValidateOptions.MaxErrors` | 100 | failures collected before stopping |

`MaxDocuments` exists because a schema that includes a generator of schemas
would otherwise be a way to spend the process. `MaxErrors` exists because a
document that is wrong in every element would otherwise produce an error for
each, which helps nobody and costs memory proportional to the document.

The DOCTYPE default is worth naming: a schema document has no use for one, and
it is the entry point for entity expansion attacks. Set
`ParseOptions.AllowDOCTYPE` only for a schema you control.

One consequence catches people out. UBL 2.1 ships
`UBL-xmldsig-core-schema-2.1.xsd` with a DOCTYPE, and every UBL document
schema includes it, so loading any of the 65 without the flag fails with a
cascade of

    src-resolve: element ref "ds:Signature" names no element declaration

— one refused include, reported as a dozen unresolved references rather than
as the DOCTYPE it actually is. With `AllowDOCTYPE` set, all 65 load clean.

## Concurrency

A loaded `Schema` is immutable and safe to validate from any number of
goroutines:

```go
schema, err := xsd.LoadFile("invoice.xsd", xsd.Options{})   // once, at startup
// ...then, per request, from many goroutines:
err = schema.Validate(doc.Root, xsd.ValidateOptions{})
```

Its one piece of lazily-built state, the content-model cache, is synchronised
for that reason: content models are compiled on first use, so the cache is
written by whichever goroutine reaches a type first and read by the rest.

Two things are *not* shared:

* **A schema still being assembled.** Finish loading before publishing it.
* **A document tree.** Parse one per goroutine. `Annotate: true` writes into
  the tree, and even without it the tree is the one mutable thing in play.

This is tested rather than asserted. The suite runs validation from many
goroutines against both warm and deliberately cold schemas, validates
documents carrying identical `xs:ID` and key values concurrently — the state
most likely to have been hung off the schema by mistake — and loads schemas
in parallel, all under `-race`.

### Determinism

Loading the same schema twice gives the same answer. That is worth stating
because it was once untrue: `{substitution group}` membership was built by
ranging a Go map, whose iteration order is deliberately randomised, and
Particle Valid (Restriction) maps a derived choice onto a base choice with an
*order-preserving* mapping. So when a substitution-group head was expanded into
a choice, the order of that choice decided the answer — one suite schema,
loaded forty times from the same file, was accepted five times and rejected
thirty-five.

Membership is now ordered by qualified name. The spec does not order
`{substitution group}`, so any stable order conforms; what does not conform is
a different one each run. The regression test loads the schema many times,
because a single load used to pass by luck about one time in eight.

Error *reporting* is stabilised the same way: where several independent types
each fail, the messages are sorted before the error is built, so the one
message a caller sees under `MaxErrors: 1` does not vary between runs.

## Conformance

Measured against the W3C xsdtests suite:

| | schema-validity | instance |
|---|---|---|
| **XSD 1.0** | 14,123 / 14,405 (98.04%) | 24,949 / 25,002 (99.79%) |
| **XSD 1.1** | 14,906 / 15,365 (97.01%) | 26,150 / 26,209 (99.77%) |

**Earlier revisions reported 99.56% and "1.1: 100%". Both were measured
wrongly.** Two bugs in the test driver, both of which flattered the result:

*Schema-validity tests were not scored at all.* A group whose schema the suite
marks invalid by design was treated as a skip, on the reasoning that Schema
Component Constraints are a separate concern from instance validation. They are
not a separate concern from *conformance*: the schema is meant to be rejected,
and accepting it is a failure. Scoring them exposed roughly 2,200 of them.

*The 1.1 run scored about six per cent of its tests.* `common/xsts.xsd` defines
`version` as a **list of tokens** — OR-joined on `testSet`, `testGroup`,
`schemaTest` and `instanceTest`, AND-joined on `expected`, and **absent means
the test applies to every processor**. Comparing the attribute for equality
with `"1.1"` restricted the 1.1 run to the explicitly-marked groups: 888 schema
tests instead of 15,365.

The dominant remaining gap is schema false-accepts — Schema Component
Constraints not yet enforced — across facet consistency, regular-expression
syntax, particles and model groups, complex-type derivation and identity
constraints. Instance false-rejects and false-accepts together are in the low
hundreds.

Some disagreements are suite defects rather than bugs here: `anyURI_a004` is
marked `status="queried"` against an open W3C bug, and its own group annotation
contradicts the expectation recorded for it. Twenty-seven such queried cases
sit in the 1.0 instance tail.

A further 20 test groups are skipped because their schema does not load, and
most of those are correct behaviour rather than gaps: nine are XML 1.1
documents, which the parser does not read; five use 1.1 constructs under 1.0
and are *meant* to fail; two need a DOCTYPE, refused by default; and several
name a document that is deliberately absent.

Beyond the suite, the validator is run against production schema sets — UBL
2.1, UN/CEFACT CII, Peppol BIS Billing 3.0, Factur-X/ZUGFeRD. Those found bugs
25,000 suite cases had not, because a diamond in the import graph is the normal
shape of a large modular schema set and the suite has no such thing. See the
testing section of the [main README](../README.md) for what each method
catches.
