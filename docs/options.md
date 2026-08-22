# Options reference

Every configuration field in the four packages, what it does, what the zero
value means, and when you would change it.

The code in this file is compiled and run before it is committed, so the field
names and types are the real ones.

## The zero value is the answer

Each option struct is designed so that `Options{}` is the setting you would pick
for untrusted input. Nothing here needs configuring to be safe; the fields exist
for when you know something the library does not — that your documents are
smaller than the default allows, or that a particular file really is trusted.

Limits follow one rule throughout:

```go
0    // the default
n    // your number
-1   // no limit — for input you produced yourself
```

`MaxDepth` is the exception: `0` and negative both mean the default, because a
depth of zero would reject every document.

---

## xdm.ParseOptions

Passed to `xdm.Parse` and `xdm.ParseString`, and carried inside
`xsd.Options.ParseOptions` for the documents a schema pulls in.

```go
tree, err := xdm.ParseString(src, xdm.ParseOptions{
    BaseURI:        "file:///srv/docs/invoice.xml",
    TrackPositions: true,
    MaxBytes:       2 << 20,
    MaxNodes:       50_000,
})
```

| Field | Type | Zero value | What it does |
|---|---|---|---|
| `BaseURI` | `string` | none | Recorded on the document node; what `fn:base-uri`, `fn:doc` and relative `xsl:include` resolve against. Set it whenever the document came from somewhere addressable. |
| `StripSpace` | `func(QName) bool` | keep all | Drops whitespace-only text nodes for the element names it returns true for. A predicate rather than a bool because `xsl:strip-space` is per-element. |
| `AllowDOCTYPE` | `bool` | refuse | Permits a `DOCTYPE` declaration. See below. |
| `TrackPositions` | `bool` | off | Records where each element starts, so a validation error can name a line. Retains the source text for the life of the tree — about 10% more memory, no extra parse time. |
| `MaxDepth` | `int` | `DefaultMaxDepth` = 1000 | Nesting limit. Deep input is the cheapest route to stack exhaustion. |
| `MaxBytes` | `int64` | `DefaultMaxBytes` = 64 MB | Source size limit, enforced on the read. |
| `MaxNodes` | `int` | `DefaultMaxNodes` = 10,000,000 | Node count limit, counting attributes and namespaces. |

### Why there are two size limits

A node costs a fixed ~200 bytes whatever it holds, so the heap a document needs
follows its **node count**, not its length. Measured:

| document | input | heap | amplification |
|---|---|---|---|
| `<a/>` repeated | 0.8 MB | 40.7 MB | 53.3x |
| invoice-like | 11.9 MB | 284.7 MB | 23.9x |
| text-heavy | 39.5 MB | 74.2 MB | 1.9x |

Across a 1.9x–53x spread a byte cap says very little about memory. `MaxBytes`
bounds the read; `MaxNodes` bounds what the read can allocate. `DefaultMaxNodes`
is chosen to cap a tree at roughly 2 GB.

Tighten both if you know your shape:

```go
// Invoices, not archives.
xdm.ParseOptions{MaxBytes: 2 << 20, MaxNodes: 100_000}

// Input this process produced itself.
xdm.ParseOptions{MaxBytes: -1, MaxNodes: -1}
```

### AllowDOCTYPE

Off by default because a DOCTYPE is the entry point for XXE and
entity-expansion attacks. **Turning it on does not reopen either** —
`encoding/xml` never parses the internal subset, so no DTD-declared entity ever
exists (see [security.md](security.md)) — but it is still the wider setting.

You need it for UBL, whose dependency graph reaches the W3C XML Signature
schema, and that file carries a DOCTYPE:

```go
xsd.LoadFile("UBL-Invoice-2.1.xsd", xsd.Options{
    ParseOptions: xdm.ParseOptions{AllowDOCTYPE: true},
})
```

Reasonable for schemas you ship. Leave it off for instance documents.

---

## xsd.Options

Passed to `xsd.Load`, `xsd.LoadFile` and `xsd.LoadFiles`.

```go
schema, err := xsd.LoadFile("main.xsd", xsd.Options{
    Version:      xsd.Version11,
    MaxDocuments: 64,
    Resolver:     &xsd.FileResolver{Root: "/srv/schemas"},
})
```

| Field | Type | Zero value | What it does |
|---|---|---|---|
| `Version` | `Version` | `Version10` | Selects XSD 1.0 or 1.1. The zero value is 1.0 so that a schema written for 1.0 does not silently acquire 1.1's relaxations — 1.1 changes which *schemas* are legal, not only which documents. |
| `MaxDocuments` | `int` | `DefaultMaxDocuments` = 512 | How many documents one assembly may read, following `import`, `include` and `redefine`. |
| `Resolver` | `Resolver` | `FileResolver{}` | How a `schemaLocation` becomes bytes. See below. |
| `ParseOptions` | `xdm.ParseOptions` | zero | Applied to every document the assembly reads. |
| `LaxUPA` | `bool` | enforce | Relaxes Unique Particle Attribution. Some published schemas violate it; this loads them anyway. |

### Resolvers

`FileResolver` reads from disk. **Set `Root`** whenever a location could be
influenced by anyone but you — it refuses `..`, absolute paths, `file:` URLs and
symlinks that lead outside:

```go
&xsd.FileResolver{Root: "/srv/schemas"}
```

`HTTPResolver` adds network fetching, which is off unless you ask for it:

```go
&xsd.HTTPResolver{
    Timeout:   10 * time.Second,          // DefaultFetchTimeout = 30s
    MaxBytes:  4 << 20,                   // DefaultMaxSchemaBytes = 16 MB
    AllowHost: func(h string) bool { return h == "schemas.example.com" },
    Files:     &xsd.FileResolver{Root: "/srv/schemas"},
}
```

`AllowHost` is the SSRF control: it runs before the request **and on every
redirect hop**, so a permitted host cannot bounce you to a denied one. It sees
`u.Hostname()`, so userinfo tricks like `http://good.example@127.0.0.1/` do not
fool it. Use it to refuse loopback, link-local and private ranges.

`Client` lets you supply your own `*http.Client`; the redirect check is
installed on a copy, so your client is not mutated.

---

## xsd.ValidateOptions

Passed to `Schema.Validate`.

```go
err := schema.Validate(doc.Root, xsd.ValidateOptions{
    MaxErrors: 25,
    Annotate:  true,
})
```

| Field | Type | Zero value | What it does |
|---|---|---|---|
| `MaxErrors` | `int` | `DefaultMaxErrors` = 100 | Stops after this many failures. A document wrong in every element would otherwise produce an error per element, which helps nobody and costs memory proportional to the document. |
| `MaxDepth` | `int` | `DefaultMaxDepth` = 1000 | Recursion limit for validation. |
| `Annotate` | `bool` | off | Writes each node's type into `TypeAnnotation`, producing the part of the PSVI that XPath and XSLT consume. Off by default because it **mutates the tree you passed in**. |

### Why validation has its own MaxDepth

It is deliberately not the parser's. Validation recurses once per element depth
at roughly 3 kB of stack a level, and exceeding Go's stack limit is
`fatal error: stack overflow` — which `recover()` cannot catch, so it kills the
process rather than failing the request.

Raising `xdm.ParseOptions.MaxDepth` to accept a legitimately deep document is a
different decision from letting the validator recurse that far. If you need
both, raise both, knowingly:

```go
xdm.ParseOptions{MaxDepth: 5000}       // accept the document
xsd.ValidateOptions{MaxDepth: 5000}    // and validate it
```

### Annotate and concurrency

`Annotate: true` writes to the tree. A compiled `*Schema` is safe to share
across goroutines, but a *tree* being annotated is not — give each goroutine its
own parse, or leave `Annotate` off.

---

## xsd.CheckOptions

Passed to `Schema.CheckConstraints`, which runs the schema-component constraints
that loading defers.

```go
err := schema.CheckConstraints(xsd.CheckOptions{Version: xsd.Version11})
```

| Field | Type | Zero value | What it does |
|---|---|---|---|
| `Version` | `Version` | `Version10` | Which version's rules to apply. Match whatever you loaded with. |
| `LaxUPA` | `bool` | enforce | Skips Unique Particle Attribution, as in `xsd.Options`. |

---

## xslt.CompileOptions

Passed to `xslt.Compile`. Compilation is the expensive step — do it once and
share the `*Stylesheet`.

```go
sty, err := xslt.Compile(sheet.Root, xslt.CompileOptions{
    BaseURI:      "file:///srv/xsl/main.xsl",
    Resolver:     resolver,
    StaticParams: map[string]string{"build": "42"},
})
```

| Field | Type | Zero value | What it does |
|---|---|---|---|
| `BaseURI` | `string` | none | What relative `xsl:include` and `xsl:import` resolve against. |
| `Resolver` | `ModuleResolver` | disabled | Loads included and imported modules. **Nil means a stylesheet cannot pull in another file** — the safe default. `xslt.NewFileResolver(roots...)` confines it to directories you name. |
| `StaticParams` | `map[string]string` | none | Values for `xsl:param static="yes"`, which participate in `use-when` and so must be known at compile time. |
| `SchemaResolver` | `xsd.Resolver` | disabled | Loads schemas for `xsl:import-schema`. |

---

## xslt.TransformOptions

Passed to `Stylesheet.Transform`, once per document.

```go
res, err := sty.Transform(ctx, doc.Root, xslt.TransformOptions{
    Params:   map[string]xdm.Sequence{"who": xdm.One(xdm.NewString("world"))},
    MaxDepth: 2000,
    Now:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
})
```

| Field | Type | Zero value | What it does |
|---|---|---|---|
| `Params` | `map[string]xdm.Sequence` | none | Values for top-level `xsl:param`, keyed by Clark name (`{uri}local`, or plain `local` for no namespace). |
| `Documents` | `xpath.DocumentResolver` | disabled | Resolves `fn:doc` and `fn:document`. **Nil disables them**, which is the default: a stylesheet that can open arbitrary URIs is an SSRF and file-disclosure vector. |
| `MaxDepth` | `int` | `DefaultMaxDepth` = 1000 | Template recursion limit. Catches a stylesheet with no base case. |
| `InitialMode` | `string` | default mode | Mode for the initial `apply-templates`. |
| `InitialTemplate` | `string` | match the root | Invokes a named template instead, which is how a stylesheet of only named templates is entered. |
| `Now` | `time.Time` | wall clock | Fixes what `fn:current-dateTime` returns. Set it to make a transform reproducible — the same input gives the same output, which is what makes golden-file tests possible. |
| `ImplicitTimezone` | `int` | UTC | Offset in minutes for values that carry no timezone. |

### MaxDepth counts ordinary descent

It is not only "a template calling itself". An identity transform recurses once
per level of the document, so a limit below the parser's would refuse documents
you had just successfully parsed. The default matches `xdm.DefaultMaxDepth` for
that reason. Raise it only alongside the parser's.

### Cancellation

`Transform` takes a `context.Context`. That is the bound on a transform that is
merely slow rather than infinitely recursive — always set a deadline for
untrusted input:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
res, err := sty.Transform(ctx, doc.Root, xslt.TransformOptions{})
```

---

## xpath.Context

XPath has no `Options` struct; configuration is the evaluation context you build
with `xpath.NewContext(item, funcs)`.

```go
ctx := xpath.NewContext(doc.Root, xpath.Builtins())
ctx.Ctx = context.Background()
ctx.StaticBaseURI = "file:///srv/docs/"
ctx.Vars = map[string]xdm.Sequence{"n": xdm.One(xdm.NewInteger(3))}

seq, err := xpath.Eval(`$n * 2`, ctx, nil)   // [6]
```

| Field | Type | What it does |
|---|---|---|
| `Item` | `xdm.Item` | The context item — `.` in an expression. |
| `Position`, `Size` | `int` | `position()` and `last()`. Set by the engine when iterating; set them yourself only when evaluating inside a sequence you are driving. |
| `Vars` | `map[string]xdm.Sequence` | In-scope variables, keyed by Clark name. |
| `Funcs` | `FunctionLibrary` | Function resolution. `xpath.Builtins()` is XPath 2.0; XSLT adds its own on top. |
| `StaticBaseURI` | `string` | What `fn:static-base-uri` returns and what `fn:resolve-uri` resolves against by default. Distinct from a *node's* base URI, which comes from the document. |
| `Docs` | `DocumentResolver` | `fn:doc` and `fn:document`. Nil disables them. |
| `Ctx` | `context.Context` | Cancellation. Set it for untrusted expressions. |
| `Now`, `HasNow` | `time.Time`, `bool` | Fixes `fn:current-dateTime`. `HasNow` distinguishes "midnight 1 January year 1" from "not set". |
| `ImplicitTimezone` | `int` | Offset in minutes for values with no timezone. |
| `Parent` | `*Context` | The enclosing context, for nested evaluation. |
| `Depth` | `int` | Recursion depth, maintained by the engine. |

### fn:collection and fn:unparsed-text

Neither is configurable. `fn:collection()` raises `FODC0002` because returning
an empty sequence would let a stylesheet silently process no documents and
report success. `fn:unparsed-text()` is disabled unconditionally — it cannot
read a file even with `Docs` set.

---

## Putting it together: a validating service

Everything a server handling untrusted uploads should set.

```go
// Once, at startup. Compilation is the expensive step.
schema, err := xsd.LoadFile("schemas/main.xsd", xsd.Options{
    Version:      xsd.Version11,
    Resolver:     &xsd.FileResolver{Root: "schemas"},
    ParseOptions: xdm.ParseOptions{AllowDOCTYPE: true}, // trusted, ours
})
if err != nil {
    log.Fatal(err)
}

// Per request. The *Schema is safe to share across goroutines.
func handle(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()

    // Belt and braces: MaxBytes bounds the parse, MaxBytesReader bounds
    // the read, and one of them will fire before the other wastes work.
    body := http.MaxBytesReader(w, r.Body, 4<<20)

    tree, err := xdm.Parse(body, xdm.ParseOptions{
        MaxBytes:       4 << 20,
        MaxNodes:       200_000,
        MaxDepth:       200,
        TrackPositions: true,   // so errors name a line
        // AllowDOCTYPE stays off: this document is not ours.
    })
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    if err := schema.Validate(tree.Root, xsd.ValidateOptions{
        MaxErrors: 25,
    }); err != nil {
        http.Error(w, err.Error(), http.StatusUnprocessableEntity)
        return
    }
    _ = ctx
    w.WriteHeader(http.StatusNoContent)
}
```

[server.md](server.md) develops this into a complete service with
hot-reloading rule sets. [security.md](security.md) explains what each of these
settings defends against, and the two things this checklist cannot do for you.
