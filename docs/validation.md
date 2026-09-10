# Validating XML

## The three kinds of "valid" people mean

Being precise about which one you need saves the most time:

| you want to check | what does it | go-xml |
|---|---|---|
| the XML is **well-formed** — tags balance, entities resolve | any XML parser | ✅ `xdm.ParseString` |
| the XML matches a **structural schema** (XSD) | a schema validator | ✅ `xsd.LoadFile` + `Schema.Validate` |
| the XML matches a **DTD** | a validating parser | ✅ `dtd.Parse` + `dtd.Validate`, or `dtd.Load` for the external subset |
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
`#FIXED`), enumerated values, `ID`/`IDREF`, and §3.3.1's two cross-referencing
rules — a `NOTATION` attribute's enumeration must name only declared
`<!NOTATION>`s, and an `ENTITY`/`ENTITIES` attribute must name entities
declared with `NDATA`. The content models go through
the same Glushkov automaton the XSD validator uses — a DTD model is a strict
subset of what an `xsd.Particle` expresses, so there is no second engine.

`dtd.Parse` reads the internal subset alone and fetches nothing. To validate
against a DTD that lives in another file, use `dtd.Load`:

```go
d, err := dtd.Load(tree.DocType, dtd.LoadOptions{
    Resolver: &dtd.FileResolver{Root: "/srv/dtds"},
    BaseURI:  "file:///srv/docs/order.xml",
})
err = dtd.Validate(tree.Root, d, dtd.Options{})
```

That reads both halves and applies them together: parameter entities span the
two subsets with XML 1.0 §2.8's precedence (the internal subset is read first
and its declarations bind), a `%pe;` in the external subset may expand to whole
declarations, and conditional sections — `<![INCLUDE[` and `<![IGNORE[`, §3.4 —
are resolved, including nested ones.

Four things to know before relying on it:

* **Nothing is fetched without a `Resolver`, and with none the load is
  refused.** `dtd.Load` returns an error wrapping `dtd.ErrNoResolver` rather
  than validating against the internal subset alone — half a DTD proves
  nothing, and reporting success on it would turn "I could not read the
  constraints" into "the constraints hold". Pass `LoadOptions.InternalSubsetOnly`
  to ask for the partial reading deliberately.
* **A resolver hands control of what this process reads to whoever wrote the
  DOCTYPE.** `FileResolver` is confined to one `Root`, refusing `..`, absolute
  paths, symlinks leading out and any non-`file` scheme; `MapResolver` reads
  from memory and touches no disk. Expansion across both subsets is charged to
  one shared budget, so a billion-laughs bomb split between them meets the same
  limit a wholly internal one does. See [security.md](security.md).
* **A partial internal subset is common.** A document declaring a few things
  locally and naming an external DTD for the rest reports every other element
  as undeclared, which is strictly correct and useless. `Options.AllowUndeclared`
  skips those; what *is* declared stays enforced. `DTD.HasExternalSubset`
  records that a DOCTYPE named one.
* **A declaration outside XML 1.0 §3.3's closed sets is reported, not
  skipped.** The attribute type is one of ten names or an enumeration, and the
  default declaration is `#REQUIRED`, `#IMPLIED`, `#FIXED AttValue` or a
  literal. A one-character typo — `IDREFF`, `#REQUIRE` — used to leave the
  attribute unconstrained and silent, so `<!ATTLIST r a CDATA #REQUIRE>` on a
  document omitting `a` passed. Validate now says which attribute went
  unchecked and why, for the reason `HasExternalSubset` exists: a caller has to
  be able to tell a validated attribute from an unexamined one. The
  declaration is kept rather than the parse failed, so the rest of the subset
  still applies.

`ID`/`IDREF` are checked as a *validity* constraint, but the attribute types
are not fed back into the data model, which is why `fn:id` still falls back to
`xml:id` and a conventional `id` attribute.

`NOTATION` and `ENTITY`/`ENTITIES` are checked against the rest of the DTD
rather than against a value space, so both are skipped when only half the DTD
was read — `dtd.Parse`, or `dtd.Load` with `InternalSubsetOnly`. A name absent
from an internal subset may simply be declared in the external one, and
reporting it would reject a document that is valid. An undeclared notation is
also reported once per `<!ATTLIST>`, not once per element, because the fault
is in the declaration.

The default is off for a reason beyond that: a DTD is the entry point for
entity expansion and XXE, so permitting one is a decision to make per document
source rather than globally.

## RELAX NG

`relaxng` validates against RELAX NG in both its notations, at 100% of James
Clark's conformance suite (965 of 965 assertions).

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

### The compact syntax

A schema written in the compact syntax is compiled by `CompileCompact`, and
`ParseCompact` returns the XML-syntax tree on its own for a caller converting
between the two notations:

```go
s, err := relaxng.CompileCompact(rncSource, relaxng.Options{})
```

The compact syntax is not a second implementation of the language. It is
parsed into the XML syntax and handed to the same compiler, so the section 7
restrictions, the datatype library and the validator are reached through one
tree and the two notations cannot come to disagree about what a schema means.
A test asserts that directly: for a schema written both ways, the two parsers
must produce structurally identical trees.

`include` and `external` reach a `Resolver` exactly as `<include>` and
`<externalRef>` do, and are refused when none is supplied. A `Resolver`
returns an XML-syntax document, so one serving compact schemas calls
`ParseCompact` itself.

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
* **A very large modular grammar may be refused at compile time.** Expanding a
  `<ref>` re-compiles the definition's body, and that work is not shared
  between two references naming the same definition, so a grammar whose
  definitions form a long chain costs expansions that grow multiplicatively
  rather than additively. A fixed budget of 200,000 expansions turns what would
  otherwise be an unbounded compile into an error naming the cause. DocBook 5.1
  is over that budget and is refused; schemas of ordinary size are far under
  it. This is a known limitation rather than a design choice — see
  [todo.md](todo.md).

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

Measured against the W3C XSD test suite: **99.89%** agreement on 25,000
instance tests, and **99.98%** on its 14,388 schema-validity tests — the
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
versioning attributes, and the 1.1 built-ins. It measures **99.90%** on the
26,204 instance tests that apply to a 1.1 processor and **99.92%** on the
15,354 schema-validity tests. An earlier revision claimed 100%; that was
measured over the explicitly-marked 1.1 groups only, about a sixteenth of the
tests a 1.1 processor is meant to run. See [xsd.md](xsd.md).

The version is opt-in rather than automatic because 1.1 changes which
documents are valid, so a 1.0 schema must not acquire its behaviour by
accident. The 1.1 constructs are always *parsed* — a schema that uses one is
not made valid by pretending it is absent — but only honoured under
`Version11`.

### Checking the schema itself

Unique Particle Attribution, Element Declarations Consistent and Particle
Valid (Restriction) are all applied at load time. Each is a property of the
schema alone, so a schema violating one fails to load rather than validating
clean.

`Options.LaxUPA` selects the permissive UPA reading Saxon and XSV use, where
only the element declaration need be identifiable rather than the particle;
without it such a schema does not load. `Schema.CheckConstraints` re-runs UPA
and Element Declarations Consistent on a loaded schema, which is how a caller
that loaded permissively asks for the strict answer:

```go
if err := schema.CheckConstraints(xsd.CheckOptions{}); err != nil {
    return err
}
```

See [xsd.md](xsd.md) for why these gate loading rather than sitting behind an
opt-in as they do in Xerces.

### Resolving schemaLocation

`include`, `import` and `redefine` name other documents, and following those
names means fetching whatever the schema says. Nothing is fetched unless a
resolver is configured. With `Resolver` nil, `xsd.Load` refuses a named
location outright — it was handed a tree, not a path, so nothing on disk was
granted — while `LoadFile` and `LoadFiles` were handed paths and default to a
`FileResolver` rooted at the directories those paths name: a sibling
`xs:include` resolves, an absolute path elsewhere or a climb through `..` does
not. To follow remote locations, opt in:

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
    // Rooted: the stylesheet names the schema location, so confine
    // resolution to the directory the schemas live in. A FileResolver
    // with no Root reads any path the stylesheet asks for.
    SchemaResolver: &xsd.FileResolver{Root: "/srv/schemas"},
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

The `xsd` package covers it — see [xsd.md](xsd.md) — so the three stages are
one library. Run the schema check first and the Schematron rules after: that is
the order the e-invoicing specifications themselves prescribe, because a
document that is structurally wrong produces useless business-rule output.

```go
// Stage 1: well-formedness. Always yours to do.
tree, err := xdm.ParseString(src, xdm.ParseOptions{})
if err != nil {
    return fmt.Errorf("not well-formed: %w", err)
}

// Stage 2: XSD. Load the schema once, outside the request path.
if err := schema.Validate(tree.Root, xsd.ValidateOptions{MaxErrors: 25}); err != nil {
    return err
}

// Stage 3: business rules. Schematron compiled to XSLT.
res, err := sheet.Transform(ctx, tree.Root, xslt.TransformOptions{})
```

`schema` here is an `*xsd.Schema` from `xsd.LoadFile("invoice.xsd",
xsd.Options{})` — immutable once loaded and safe to share across goroutines,
which is why it belongs in start-up rather than per document. Stage 2 takes the
parsed tree, not the source text, so the document is read once.

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
