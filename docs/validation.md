# Validating XML

## The three kinds of "valid" people mean

Being precise about which one you need saves the most time:

| you want to check | what does it | go-xml |
|---|---|---|
| the XML is **well-formed** — tags balance, entities resolve | any XML parser | ✅ `xdm.ParseString` |
| the XML matches a **structural schema** (XSD) | a schema validator | ✅ `xsd.LoadFile` + `Schema.Validate` |
| the XML matches a **DTD** | a validating parser | ✅ `dtd.Parse` + `dtd.Validate` (internal subset only) |
| the XML matches a **RELAX NG** schema | a different validator | ✅ `relaxng.Compile` + `Schema.Validate` |
| the XML satisfies **business rules** — cross-field arithmetic, code lists, conditional requirements | Schematron, compiled to XSLT | ✅ this is the use case |

### DTD

The DTD case needs care because the work is split between parsing and
validating.

A `DOCTYPE` is refused by default. With `ParseOptions.AllowDOCTYPE` set, two
declarations are applied — the two whose absence is visible in the data model:

* **`<!ATTLIST>` defaults.** A `#FIXED` or literal default is added to every
  matching element, including a namespace declaration, since
  `xmlns:p CDATA #FIXED "..."` is how a DTD supplies a binding.
* **`<!ENTITY>` internal general entities.** `&name;` expands. External
  entities — `SYSTEM` or `PUBLIC` — are never resolved, and expansion is
  bounded; see [security.md](security.md).

**Parsing still does not check anything else.** `AllowDOCTYPE` buys
parseability, not validation — a document that violates its own DTD parses
without complaint:

```go
// <!DOCTYPE r [ <!ELEMENT r (a)> ]>  <r><b>wrong</b></r>
tree, err := xdm.ParseString(doc, xdm.ParseOptions{AllowDOCTYPE: true})
// err is nil — parsing does not apply the content model
```

Validation is a separate call, in the [`dtd`](../dtd) package:

```go
d, err := dtd.Parse(tree.DocType)
err = dtd.Validate(tree.Root, d, dtd.Options{})
// /r: element b is not permitted here in the content of r
```

That checks `<!ELEMENT>` content models, attribute presence (`#REQUIRED` and
`#FIXED`), enumerated values, and `ID`/`IDREF`. The content models go through
the same Glushkov automaton the XSD validator uses — a DTD model is a strict
subset of what an `xsd.Particle` expresses, so there is no second engine.

Two things to know before relying on it:

* **An external subset is never fetched.** Fetching one is the attack
  `AllowDOCTYPE` exists to gate. `DTD.HasExternalSubset` records that one was
  named, so a caller knows the check was partial rather than clean.
* **A partial internal subset is common.** A document declaring a few things
  locally and naming an external DTD for the rest reports every other element
  as undeclared, which is strictly correct and useless. `Options.AllowUndeclared`
  skips those; what *is* declared stays enforced.

`ID`/`IDREF` are checked as a *validity* constraint, but the attribute types
are not fed back into the data model, which is why `fn:id` still falls back to
`xml:id` and a conventional `id` attribute.

The default is off for a reason beyond that: a DTD is the entry point for
entity expansion and XXE, so permitting one is a decision to make per document
source rather than globally.

## RELAX NG

`relaxng` validates against RELAX NG's XML syntax, at 100% of James Clark's
conformance suite (965 of 965 assertions).

```go
schema, err := xdm.ParseString(rngSource, xdm.ParseOptions{})
if err != nil {
    return err
}
s, err := relaxng.Compile(schema.Root)
if err != nil {
    return err
}
doc, err := xdm.ParseString(src, xdm.ParseOptions{})
if err != nil {
    return err
}
err = s.Validate(doc.Root)
```

It is a separate engine rather than a use of the XSD automaton, because RELAX
NG validates by a different model: a schema *is* a pattern, and validation
computes the derivative of that pattern with respect to each item of input,
accepting when what remains matches the empty sequence. There is no finite
automaton to build, and `interleave` — which admits its branches in any order,
with arbitrary patterns rather than single elements — is not something a
Glushkov construction expresses.

What is reused is what the two languages genuinely share: RELAX NG names XSD's
types through its datatype library, and its `pattern` parameter is the XML
Schema regex flavour, so both go through `xsd`.

### Reaching outside the schema document

`<externalRef>` and `<include>` name another file. Following one is a fetch,
and it is gated the way every other outward read in this project is:

```go
s, err := relaxng.CompileWithOptions(schema.Root, relaxng.Options{
    Resolver: myResolver, // nil refuses every href
    BaseURI:  "file:///schemas/invoice.rng",
})
```

`Compile` supplies no resolver, so every `href` is refused with an error
naming it. There is no default implementation: where a schema is allowed to
reach is the caller's decision, not the schema author's. A cycle of includes is
bounded — with a resolver reading from the network, that is a request loop
rather than merely a hang.

`<parentRef>` needs no resolver, reaching only into the enclosing grammar, and
always works.

### Two things to know

* **A schema's names follow XML 1.0 fourth edition**, which is the edition
  RELAX NG was specified against. The fifth edition made legal a great many
  names that were not — among them any name beginning with a combining mark —
  and `xdm` implements the fifth, as an XML parser should. The two differ
  deliberately; see `relaxng/ncname.go`.
* **Validation depth is bounded separately from the parser's.** Taking
  derivatives over a nested document costs time and memory quadratic in the
  depth, so `ValidateOptions.MaxDepth` bounds it at 1000 by default — raising
  `xdm`'s parser limit does not raise this one.

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

Measured against the W3C XSD test suite: **99.88%** agreement on 24,999
instance tests, and **99.51%** on its 14,405 schema-validity tests — the
second figure is the honest one to quote, and [xsd.md](xsd.md) explains why
earlier revisions reported neither.

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
versioning attributes, and the 1.1 built-ins. It measures **99.89%** on the
26,205 instance tests that apply to a 1.1 processor and **99.11%** on the
15,365 schema-validity tests. An earlier revision claimed 100%; that was
measured over the explicitly-marked 1.1 groups only, about a sixteenth of the
tests a 1.1 processor is meant to run. See [xsd.md](xsd.md).

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
