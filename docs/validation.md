# Validating XML

## First: this library does not do XSD

If you came here to validate against an `.xsd` schema, **go-xml cannot do
that**, and no combination of its APIs will. It implements XPath 2.0 and
XSLT 2.0. `xsl:import-schema` is refused at compile time with an explicit
error rather than silently ignored:

```
xsl:import-schema is not supported: this engine is not schema-aware
```

That refusal is deliberate. Schema-awareness changes how *every* value in a
stylesheet atomises — a schema-validated `<price>10.50</price>` yields an
`xs:decimal`, an unvalidated one yields an `xs:untypedAtomic` — so accepting
the declaration and ignoring it would make the stylesheet's type assertions
quietly meaningless rather than obviously broken.

### The three kinds of "valid" people mean

Being precise about which one you need saves the most time:

| you want to check | what does it | go-xml |
|---|---|---|
| the XML is **well-formed** — tags balance, entities resolve | any XML parser | ✅ `xdm.ParseString` |
| the XML matches a **structural schema** (XSD, DTD, RELAX NG) | a schema validator | ❌ not implemented |
| the XML satisfies **business rules** — cross-field arithmetic, code lists, conditional requirements | Schematron, compiled to XSLT | ✅ this is the use case |

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
