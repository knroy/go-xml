# XSD

`xsd` implements XML Schema 1.0 and 1.1. This page is the reference; if you
are deciding *whether* XSD is the right tool at all, read
[validation.md](validation.md) first — it starts with what schema validation
cannot check, which is the part most likely to save you time.

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

### xsi:schemaLocation is ignored

`xsi:schemaLocation` lives in the *instance document*. Honouring it would let
whoever supplied the document choose which schema it is judged against, which
defeats the purpose of validating it. The schema is always the one the caller
loaded.

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

## Conformance

Measured against the W3C xsdtests suite:

| | |
|---|---|
| **XSD 1.0** | 99.51% — 24,863 of 24,986 instance tests |
| **XSD 1.1** | 100% — 1,083 of 1,083 |

The remaining 1.0 disagreements are a long tail with no group above ten cases,
spread across content models, attribute resolution, identity constraints and
datatype edges. At least one is a suite defect rather than a bug here:
`anyURI_a004` is marked `status="queried"` against an open W3C bug, and its
own group annotation contradicts the expectation recorded for it.

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
