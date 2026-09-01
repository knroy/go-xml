# w3cschemas

The W3C schemas that other schemas refer to by URL, bundled so that loading one
does not depend on reaching w3.org.

```go
r, err := w3cschemas.Catalog()
if err != nil { ... }

s, err := xsd.Load(root, base, xsd.Options{
	Version:  xsd.Version11,
	Resolver: r,
})
```

## Why this exists

Schemas name their imports as absolute URLs, and those fetches are unreliable
by design — the W3C throttles them. The XSLT 3.0 schema imports the XSD 1.1
schema for schemas that way:

```xml
<xs:import namespace="http://www.w3.org/2001/XMLSchema"
           schemaLocation="http://www.w3.org/TR/xmlschema11-1/XMLSchema.xsd"/>
```

The W3C's own copy of that file in the XSLT test suite was edited in 2021 to
use a relative path instead, the comment giving the reason as *"W3C web site
throttling"*. Anything resolving such a reference over the network inherits
that; a catalog does not.

## Why it is a separate module

The bundled documents are W3C documents under W3C terms, not MIT like the rest
of `go-xml`. Keeping them here means the core module's licensing stays simple
and depending on these files is an explicit choice. See [NOTICE](NOTICE).

## What is bundled

| File | Namespace |
|---|---|
| `schemas/XMLSchema.xsd` | `http://www.w3.org/2001/XMLSchema` (XSD 1.1 schema for schemas) |
| `schemas/xml.xsd` | `http://www.w3.org/XML/1998/namespace` (`xml:lang`, `xml:space`, `xml:base`, `xml:id`) |

Each is registered under its namespace and under every `schemaLocation`
spelling it is referred to by, so an `xs:import` finds it whether it names the
`TR/` URL, the `2001/` URL, a bare relative path, or only the namespace.

## What it does not do

`Catalog` answers only what is bundled. A reference to anything else is an
error rather than a request — which is the point in a server. To also read
schemas from disk:

```go
r.SetFallback(&xsd.FileResolver{Root: "schemas"})
```

If you need a specific reviewed copy of these schemas, do not use this module.
Build the catalog from your own files instead; that is what
`xsd.CatalogResolver`, `xsd.W3CEntries` and `AddFromFS` are for, and it is a
few lines:

```go
r := xsd.NewCatalogResolver()
err := r.AddFromFS(os.DirFS("my-schemas"), xsd.W3CEntries())
```
