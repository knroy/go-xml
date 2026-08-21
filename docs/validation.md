# Validating XML

## The three kinds of "valid" people mean

Being precise about which one you need saves the most time:

| you want to check | what does it | go-xml |
|---|---|---|
| the XML is **well-formed** — tags balance, entities resolve | any XML parser | ✅ `xdm.ParseString` |
| the XML matches a **structural schema** (XSD) | a schema validator | ✅ `xsd.LoadFile` + `Schema.Validate` |
| the XML matches a **DTD** | a validating parser | ❌ a DOCTYPE is parsed past, never applied |
| the XML matches a **RELAX NG** schema | a different validator | ❌ not implemented |
| the XML satisfies **business rules** — cross-field arithmetic, code lists, conditional requirements | Schematron, compiled to XSLT | ✅ this is the use case |

### DTD and RELAX NG

Neither is implemented, and the DTD case has a trap worth naming. A `DOCTYPE`
is refused by default; set `ParseOptions.AllowDOCTYPE` and it is *parsed past*
rather than applied. A document that violates its own internal DTD parses
without complaint, and entities the DTD declares are not expanded:

```go
// <!DOCTYPE r [ <!ELEMENT r (a)> ]>  <r><b>wrong</b></r>
_, err := xdm.ParseString(doc, xdm.ParseOptions{AllowDOCTYPE: true})
// err is nil — the content model is never checked
```

So `AllowDOCTYPE` buys tolerance, not validation. If a document's constraints
live in a DTD and you need them enforced, this is not the library for that
document — convert the DTD to XSD, or use a validating parser.

The default is off for a reason beyond that: a DTD is the entry point for
entity expansion and XXE, so permitting one is a decision to make per document
source rather than globally.

RELAX NG is a larger absence: it validates by a different model — derivatives
over patterns rather than a finite automaton — so it is a separate engine
rather than a use of the one here, and its `interleave` is the part that makes
it so.

## XSD

`xsd` implements XML Schema 1.0 and 1.1: the component model, schema assembly
through `include`, `import`, `redefine` and `override`, content models, simple
types and facets, `xsi:type` and `xsi:nil`, substitution groups, wildcards,
identity constraints and document-level ID/IDREF.

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
    // *xsd.ValidationErrors, one entry per failure, each carrying the
    // spec's error code.
    return err
}
```

Measured against the W3C XSD test suite: **99.56%** agreement on 24,986
instance tests.

This section is the overview. [xsd.md](xsd.md) is the reference — resolvers,
limits, the PSVI, concurrency, and what the conformance figures do and do not
cover.

XSD **1.1** is implemented and opt-in:

```go
xsd.Options{Version: xsd.Version11}
```

That brings in `xs:assert`, conditional type assignment with
`xs:alternative` and inheritable attributes, `xs:openContent` and
`xs:defaultOpenContent`, `xs:override`, the `notNamespace` and `notQName`
wildcard forms, `explicitTimezone`, conditional inclusion through the
versioning attributes, and the 1.1 built-ins. It measures **100%** on the
suite's 1.1 instance tests (1,083 of 1,083).

The version is opt-in rather than automatic because 1.1 changes which
documents are valid, so a 1.0 schema must not acquire its behaviour by
accident. The 1.1 constructs are always *parsed* — a schema that uses one is
not made valid by pretending it is absent — but only honoured under
`Version11`.

### Checking the schema itself

Unique Particle Attribution and Element Declarations Consistent are checked on
request:

```go
if err := schema.CheckConstraints(xsd.CheckOptions{}); err != nil {
    return err
}
```

They are a separate call because they are the expensive half and say nothing
about whether an instance document is valid — the same reason Xerces gates
them behind `schema-full-checking`, default false. `CheckOptions.LaxUPA`
selects the permissive reading Saxon and XSV use, where only the element
declaration need be identifiable rather than the particle.

Particle Valid (Restriction) is **not** checked. libxml2 does not implement it
at all and Xerces leaves it off by default, so a schema invalid in that
specific way is accepted here rather than reported.

### Resolving schemaLocation

`include`, `import` and `redefine` name other documents, and following those
names means fetching whatever the schema says. The default `FileResolver` reads
only from disk. To follow remote locations, opt in:

```go
xsd.Options{Resolver: &xsd.HTTPResolver{
    AllowHost: func(host string) bool { return host == "schemas.example.com" },
}}
```

`AllowHost` runs before the request, so it is the place to refuse loopback and
private address ranges. `MapResolver` resolves from an in-memory table and
touches neither disk nor network, which is the right choice in a server.

Note that `xsi:schemaLocation` lives in the *instance document*. Honouring it
lets whoever supplied the document choose which schema it is judged against, so
by default this library does not read it — the schema is the one the caller
loaded. Where you need it, `Schema.WithInstanceLocations` takes it under a
namespace allowlist; see [xsd.md](xsd.md#xsischemalocation-is-ignored-by-default).

## xsl:import-schema

A stylesheet can declare a schema, which makes its type names available and
lets a caller validate the source against the same components:

```go
sheet, err := xslt.Compile(styleTree.Root, xslt.CompileOptions{
    SchemaResolver: &xsd.FileResolver{},
})
if err != nil {
    return err
}
if s := sheet.Schema(); s != nil {
    if err := s.Validate(srcTree.Root, xsd.ValidateOptions{}); err != nil {
        return err
    }
}
```

One boundary worth stating plainly: importing a schema makes type *names*
known, but it does not change how a node atomises. A `<price>10.50</price>`
annotated as `xs:decimal` still atomises as untyped, because the typed value
would have to be carried on the node rather than its name. A stylesheet
relying on schema-aware *arithmetic* will behave as it does without a schema;
one relying on type assertions will not.

Most real "invoice validation" pipelines need the first and third, and use the
second mainly as a cheap early filter. The rules that actually reject documents
in production — *"if the tax category is exempt, an exemption reason is
required"*, *"the sum of line totals must equal the invoice total"* — are not
expressible in XSD at all. They are Schematron, and Schematron is XSLT.

### If you do need XSD

Use a schema validator alongside this one. In Go the practical options are
cgo bindings to libxml2 (`lestrrat-go/libxml2`), shelling out to `xmllint`, or
a JVM sidecar. Run the schema check first, then the Schematron rules — that is
the order the e-invoicing specifications themselves prescribe, because a
document that is structurally wrong produces useless business-rule output.

```go
// Stage 1: well-formedness. Always yours to do.
tree, err := xdm.ParseString(src, xdm.ParseOptions{})
if err != nil {
    return fmt.Errorf("not well-formed: %w", err)
}

// Stage 2: XSD, if you need it — not this library.
if err := externalSchemaValidator.Validate(src); err != nil {
    return err
}

// Stage 3: business rules. This library.
res, err := sheet.Transform(ctx, tree.Root, xslt.TransformOptions{})
```

## Schematron in practice

You do not write XSLT by hand for this. Schematron is a small rules language
that *compiles to* XSLT through a published pipeline (the ISO skeleton), and
every e-invoicing specification ships the compiled `.xslt` alongside the
`.sch` source. Point go-xml at the compiled stylesheet.

A rule set produces an **SVRL** report — an XML document listing which
assertions failed and where:

```xml
<svrl:schematron-output>
  <svrl:failed-assert id="BR-CO-10" location="/*:Invoice[1]/*:LegalMonetaryTotal[1]">
    <svrl:text>Sum of line net amounts must equal the invoice line total.</svrl:text>
  </svrl:failed-assert>
</svrl:schematron-output>
```

An empty `schematron-output` means the document passed. Counting
`failed-assert` elements is how you decide, and `@flag` or `@role` is how you
separate errors from warnings — those attributes come from the rule set, not
from this engine.

### Turning a report into a decision

```go
res, err := sheet.Transform(ctx, tree.Root, xslt.TransformOptions{})
if err != nil {
    // The stylesheet itself failed — a bug in the rules or an unsupported
    // construct. This is not "the document is invalid".
    return nil, fmt.Errorf("running rules: %w", err)
}

// Walk the report rather than string-matching it.
const svrlNS = "http://purl.oclc.org/dsdl/svrl"
var failures []Failure
var walk func(*xdm.Node)
walk = func(n *xdm.Node) {
    if n.Kind == xdm.KindElement &&
        n.Name.URI == svrlNS && n.Name.Local == "failed-assert" {
        failures = append(failures, Failure{
            ID:       n.AttrValue("id"),
            Location: n.AttrValue("location"),
            Message:  strings.TrimSpace(n.StringValue()),
        })
    }
    for _, c := range n.Children {
        walk(c)
    }
}
walk(res.Tree())
```

`res.Tree()` gives you the report as a navigable document, which is almost
always what you want in a server — `res.String()` is for when you are handing
the SVRL onward to something that expects XML.

**Distinguish the two failure modes.** A transform error means the *rules* are
broken; a non-empty report means the *document* is. Returning 500 for the first
and 422 for the second is the difference between an alert that pages someone
and a response the client can act on.

## Reporting the line a failure occurred on

SVRL identifies a failing element by XPath, which is exact but not something a
person navigates by — and it is ambiguous where two siblings share a path.
Parse with `TrackPositions` and the rule set can report the line:

```go
tree, err := xdm.ParseString(src, xdm.ParseOptions{TrackPositions: true})
```

The stylesheet reads it through two extension functions:

```xslt
xmlns:gx="https://github.com/knroy/go-xml"

<xsl:if test="gx:line-number()">
  <xsl:attribute name="line"><xsl:value-of select="gx:line-number()"/></xsl:attribute>
</xsl:if>
```

They return the **empty sequence** when the position is unknown, which is why
the example tests before emitting. A report claiming line 0 for every failure
would be worse than one carrying no line at all.

Tracking costs about 10% more memory and no extra parse time. It is opt-in
because it buys nothing for a caller that never asks.
