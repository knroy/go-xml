# Options reference

Every configuration field in the library, what it does, what the zero value
means, and when you would change it.

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

Two sets of exceptions, both verified by the boundary tests described in
[testing.md](testing.md):

* **A negative `MaxDepth` means the default, not "no limit"**, in
  `xdm.ParseOptions` and `xpath.Context` — a depth bound of zero or below would
  reject every document, so there is no useful reading of a negative value
  other than "the caller set nothing". Elsewhere — `xsd.ValidateOptions`,
  `relaxng.ValidateOptions`, `xslt.TransformOptions` — a negative `MaxDepth`
  really does mean no limit.
* **`xsd.HTTPResolver.MaxBytes` has no unlimited setting.** A negative value
  refuses every fetch, with an error naming the limit. That is deliberate: a
  schema is not a stream, so an unbounded read is a way to be handed an
  unbounded allocation. Use a large number, not `-1`.

Every limit is tested at `0`, negative, `1`, exactly at the limit, exactly one
over, and `MaxInt`/`MaxInt64`. The largest value a caller can name is always a
*permissive* setting and never a refusal — it was briefly the opposite, when
`maxBytes+1` overflowed to a negative `io.LimitReader` bound.

### Recognising a refusal: `xdm.ErrResourceLimit`

"Your expression is malformed" and "this processor declined to do the work"
are different conditions with different remedies, but they arrive looking
alike. The specs define an error code for every *semantic* condition and none
for "I gave up", so a limit has to borrow one: the parser's nesting guard
reports `XPST0003`, which properly means a syntactically unacceptable
expression. A caller reading the code alone would tell its user their
expression was invalid, when in fact it was merely deeper than this processor
will parse.

`xdm.ErrResourceLimit` is a sentinel wrapped into those errors so the two can
be told apart:

```go
seq, err := xpath.Eval(expr, ctx, nil)
switch {
case errors.Is(err, xdm.ErrResourceLimit):
    // The input was refused, not rejected. Retry with a smaller request,
    // shed load, or raise the limit — do not report a syntax error.
case err != nil:
    // A genuine fault in the expression or the document.
}
```

The sentinel is **added** to the error, never substituted for it. The spec
error code and the leading message text are unchanged, so `xdm.ErrorCode`
still returns `XPST0003`, the conformance suites still match, and existing
code that reads the message keeps working. Wrap a new limit the same way:

```go
return fmt.Errorf("XPDY0130: ... : %w", MaxItems, xdm.ErrResourceLimit)
```

#### Which limits carry it

Every limit below reports a **semantic** code that misdescribes what happened,
and every one of them now also carries the sentinel. The rightmost column is
why the code alone is misleading; the codes are kept because callers and the
conformance suites read them, and changing one is spec-visible.

| Limit | Site | Code | What the code actually means |
|---|---|---|---|
| `maxParseDepth` (expression) | `xpath/parser.go` | `XPST0003` | the expression is syntactically invalid |
| `maxParseDepth` (type) | `xpath/parser_path.go` | `XPST0003` | as above; a *type* nests through a path the expression counter never sees |
| `MaxItems` | `xpath/context.go` | `XPDY0130` | (no misdescription; the code is this engine's own) |
| `Context.MaxDepth` | `xpath/context.go` | `XPDY0001` | no context item is defined |
| `backtrackBudget` | `xpath/regex_backtrack.go` | `FORX0002` | the regular expression is invalid |
| range bound | `xpath/operators.go` | `FOAR0002` | a numeric operation overflowed |
| `maxNestDepth` | `xquery/nested.go` | `XPST0003` | the query is syntactically invalid |
| `ValidateOptions.MaxDepth` | `xsd/validate.go` | `cvc-elt.1` | the element is invalid against its declaration |

The XSD case reaches a caller through `xsd.ValidationErrors`, which now
unwraps to the individual `*ValidationError`s, so `errors.Is` over the whole
set finds a sentinel any one of them carries:

```go
if err := schema.Validate(root, opts); errors.Is(err, xdm.ErrResourceLimit) {
    // Nothing was assessed. The document may well be valid.
}
```

Note the asymmetry that makes this worth doing: a refusal says **nothing**
about the input. A document refused for depth may be perfectly valid, and a
pattern refused for its backtracking budget may well match. Reporting either
as a fault tells the user something untrue.

#### Limits that decline without an error at all

`xsd/subsume.go` and `xsd/restrict.go` do not raise anything when their budgets
run out. They return "declined", and the caller falls back to the conservative
XSD 1.0 structural table, which may reject a derivation the exact procedure
would have admitted. That is sound — `xsd/budget_soundness_test.go` proves the
fallback never accepts what the exact answer rejects — but it is silent: a
schema refused because a budget ran out looked exactly like one the table
genuinely forbids.

Those declines are now **counted** rather than raised, through the
`budgetStats` hook in `xsd/subsume.go`, on the model of `icStats` in
`xsd/identity.go`. Budget declines (fixable by raising `subsumeMaxStates`,
`subsumeMaxProduct` or `branchLimit`) are counted separately from structural
ones (a recursive model group, an all group, a wildcard — no budget affects
these). The counters are a measurement tool for the package rather than API
surface, and nothing reads them to make a decision: the verdicts are exactly
what they were before the counting existed.

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
| `XPathVersion` | `xpath.Version` | `XPath20` | The version of XPath the 1.1 assertions and conditional type alternatives are written in. See [Choosing a language version](#choosing-a-language-version). |

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

Passed to `Schema.Validate` and `Schema.ValidateContext`.

```go
err := schema.Validate(doc.Root, xsd.ValidateOptions{
    MaxErrors: 25,
    Annotate:  true,
})
```

| Field | Type | Zero value | What it does |
|---|---|---|---|
| `MaxErrors` | `int` | `DefaultMaxErrors` = 100 | Stops after this many failures. A document wrong in every element would otherwise produce an error per element, which helps nobody and costs memory proportional to the document. **Do not pass a negative value** — see below. |
| `MaxDepth` | `int` | `DefaultMaxDepth` = 1000 | Recursion limit for validation. A negative value means no limit. |

> **A negative `xsd.ValidateOptions.MaxErrors` reports an invalid document as
> valid.** `Validate` returns `nil`. Unlike `dtd.Options.MaxErrors`, where a
> negative value correctly means "no limit", the stop check here is
> `len(v.errs) >= v.opts.MaxErrors` with no `> 0` guard, so `0 >= -1` holds on
> the first failure and validation stops before recording anything. Recorded as
> a skipped test in `xsd/limits_boundary_test.go`. Use `0` for the default or a
> large positive number; there is no unlimited setting.
| `Annotate` | `bool` | off | Writes each node's type into `TypeAnnotation`, together with the resolved `DerivedPrimitive`, `ListItem` and `UnionMember` beside it, producing the part of the PSVI that XPath and XSLT consume. The resolved fields are recorded per node rather than looked up later, so a schema loaded afterwards cannot retype a document this one already validated. Off by default because it **mutates the tree you passed in**. |

### Bounding a run with a context

`Schema.ValidateContext(ctx, root, opts)` is `Validate` with a cancellable
context. `Validate` itself is unchanged and delegates with
`context.Background()`, so existing callers keep working.

```go
ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
defer cancel()
err := schema.ValidateContext(ctx, doc.Root, xsd.ValidateOptions{})
```

A cancelled or expired context returns the context's **own** error —
`context.Canceled` or `context.DeadlineExceeded`, matchable with `errors.Is` —
and *not* a `*ValidationErrors`. That distinction is the point: "I ran out of
time" is not a verdict on the document, and the failures found so far are
discarded rather than handed back as a partial one.

Cancellation is observed once per element of the tree walk and once per node
selected by an identity constraint. Those are the two places a run can spend
unbounded time; see [security.md](security.md) for the measured cost of the
second. It is not observed inside a single element's content-model matching,
which is bounded by that element's own declaration.

`Schema.ValidateElement`, `ValidateElementLax` and `ValidateAgainstType` do not
take a context. They exist for XSLT's `validation="strict"` on a *constructed*
element, which is bounded by what the transform just built — and the transform
already has the caller's context.

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
    StaticParams: map[string]xdm.Sequence{
        "build": xdm.One(xdm.NewString("42")),
    },
})
```

| Field | Type | Zero value | What it does |
|---|---|---|---|
| `BaseURI` | `string` | none | What relative `xsl:include` and `xsl:import` resolve against. |
| `Resolver` | `ModuleResolver` | disabled | Loads included and imported modules. **Nil means a stylesheet cannot pull in another file** — the safe default. `xslt.NewFileResolver(roots...)` confines it to directories you name, each covering its subdirectories to any depth; a symlink out of a root is resolved before the containment check, so it does not escape. |
| `StaticParams` | `map[string]xdm.Sequence` | none | Values for `xsl:param static="yes"`, keyed by the parameter's `{uri}local` name. A static parameter is bound before static analysis begins, so its value must come from the caller rather than from `Transform`'s runtime `Params`. |
| `SchemaResolver` | `xsd.Resolver` | disabled | Loads schemas for `xsl:import-schema`. |
| `XPathVersion` | `*xpath.Version` | derive | Pins the XPath version for every expression in the stylesheet, overriding what the stylesheet declares. Nil derives it from the `version` attribute. See [Choosing a language version](#choosing-a-language-version). |

### Choosing a language version

Three languages meet in this library and each picks its version differently,
because each is asked a different question.

**XSLT** is chosen by the stylesheet. The `version` attribute says which
language the stylesheet is written in, and the engine believes it. A version
this processor does not know is read as the highest it does, which is what
forwards compatibility means.

**XPath inside a stylesheet** follows from that, and not as the identity you
might expect:

| Stylesheet `version` | XPath compiled |
|---|---|
| `1.0` | XPath 2.0, under the 1.0 backwards-compatibility rules |
| `2.0` | XPath 2.0 |
| `3.0` | **XPath 3.1** |

XSLT 3.0 section 2.2 requires an XPath 3.1 processor, so `version="3.0"` gets
maps, arrays and the lookup operator, not merely the 3.0 additions. Such a
stylesheet also gets the `map:`, `array:`, `math:` and `err:` prefixes
predeclared, per section 3.1 — a stylesheet that binds one itself still wins.

The version is *static per element*, exactly like the base URI and the default
collation, so an included 2.0 module keeps its own answer inside a 3.0
stylesheet rather than inheriting the importing module's.

**XPath inside a schema** does not follow from anything, because a schema
document states no XPath version anywhere. It is XPath 2.0, which is what XSD
1.1 requires: assertions are defined against a subset of 2.0.

`XPathVersion` overrides the derivation in both directions:

```go
// Hold an untrusted stylesheet down to the smaller language, whatever it
// declares.
v := xpath.XPath20
sty, err := xslt.Compile(sheet.Root, xslt.CompileOptions{XPathVersion: &v})

// Let assertions in your own schemas use the 3.1 function library.
schema, err := xsd.LoadFile("main.xsd", xsd.Options{
    Version:      xsd.Version11,
    XPathVersion: xpath.XPath31,
})
```

Setting it is a deliberate departure from conformance, which is why nothing
sets it implicitly. Raising a version makes a document this engine's rather
than every engine's: a stylesheet or schema that relies on a construct its
declared version does not have will be rejected by any other conforming
processor. Lowering one is the safer direction — it narrows the surface a
document you did not write can reach.

On the command line the same choice is `-xpath-version`:

```sh
go-xml -xsl sheet.xsl -xpath-version 2.0 in.xml
go-xml validate -xsd s.xsd -xsd-version 1.1 -xpath-version 3.1 in.xml
```

For the transform it defaults to deriving from the stylesheet; for `validate`
it defaults to `2.0`, since there is nothing in a schema to derive it from.

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
| `Collections` | `xpath.CollectionResolver` | disabled | Resolves `fn:collection`. **Nil disables it**, and setting `Documents` does not set this — the two are separate switches on purpose. |
| `MaxDepth` | `int` | `DefaultMaxDepth` = 1000 | Template recursion limit. Catches a stylesheet with no base case. |
| `DisableAssertions` | `bool` | `false` — assertions enabled | Turns off `xsl:assert` checking for the whole transformation. XSLT 3.0 §22.2: "By default, assertions are enabled." |
| `InitialMode` | `string` | default mode | Mode for the initial `apply-templates`. |
| `InitialTemplate` | `string` | match the root | Invokes a named template instead, which is how a stylesheet of only named templates is entered. |
| `Now` | `time.Time` | wall clock | Fixes what `fn:current-dateTime` returns. Set it to make a transform reproducible — the same input gives the same output, which is what makes golden-file tests possible. |
| `ImplicitTimezone` | `int` | UTC | Offset in minutes for values that carry no timezone. |

### MaxDepth counts ordinary descent

It is not only "a template calling itself". An identity transform recurses once
per level of the document, so a limit below the parser's would refuse documents
you had just successfully parsed. The default matches `xdm.DefaultMaxDepth` for
that reason. Raise it only alongside the parser's.

### DisableAssertions

XSLT 3.0 §22.2 asks for it: "An implementation *should* provide an external
mechanism to disable assertion checking for the stylesheet as a whole (either
statically or dynamically). The detail of such mechanisms is
implementation-defined."

This is the dynamic reading, so one compiled stylesheet can be run with
assertions on in test and off in production without recompiling:

```go
res, err := sty.Transform(ctx, doc.Root, xslt.TransformOptions{
    DisableAssertions: true,
})
```

A disabled assertion is skipped before its `@test` is evaluated, so it cannot
fail and costs nothing. It is also the *only* thing that skips one — §22.2
closes by asking implementations to "avoid optimizing `xsl:assert` instructions
away", so nothing else in this engine elides them.

The static reading is `use-when`, which §22.2 names first and which needs no
option: a `use-when="false()"` removes the element before compilation.

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
| `Collections` | `CollectionResolver` | Resolves `fn:collection`. Nil disables it. Independent of `Docs`. |
| `Parent` | `*Context` | The enclosing context, for nested evaluation. |
| `Depth` | `int` | Recursion depth, maintained by the engine. |

### fn:collection

`Collections` enables `fn:collection`, and it is a **separate switch from
`Docs`**. Setting one does not set the other: a caller who wants `fn:doc` for
the code lists shipped beside a stylesheet does not thereby want a collection
URI enumerated.

```go
type Codelists struct{ dir string }

func (c Codelists) ResolveCollection(uri, base string) (xdm.Sequence, error) {
    // uri is "" for the default collection — fn:collection() with no
    // argument. Return an error rather than an empty sequence if you have
    // no default.
    files, err := filepath.Glob(filepath.Join(c.dir, uri, "*.xml"))
    if err != nil {
        return nil, err
    }
    var out xdm.Sequence
    for _, f := range files {
        b, err := os.ReadFile(f)
        if err != nil {
            return nil, err
        }
        tree, err := xdm.ParseString(string(b), xdm.ParseOptions{})
        if err != nil {
            return nil, err
        }
        out = append(out, tree.Root)
    }
    return out, nil
}

ctx.Collections = Codelists{dir: "lists"}   // xpath
opts.Collections = Codelists{dir: "lists"}  // xslt.TransformOptions
```

With none configured, `fn:collection()` raises `FODC0002` rather than
returning an empty sequence — a stylesheet that iterates a collection and
finds nothing cannot tell "no documents" from "collections are switched off",
and silently processing zero documents looks like success. Return an error
from the resolver for the same reason.

Validate the URI inside your resolver. It arrives from the stylesheet, so
treat it as untrusted input: constrain it to a set you control, and do not
join it into a path without checking for traversal.

### fn:unparsed-text

Off by default, and it has a switch of its own: `TransformOptions.Texts` in
`xslt`, `Context.Texts` in `xpath`. Setting the document resolver does not set
it, and that separation is the point. `fn:doc` hands back a parsed XML
document, so a file that is not well-formed discloses nothing; `fn:unparsed-text`
hands back the raw bytes of any file inside the roots.

`xslt.FileResolver` implements it behind `UnparsedText`, off by default and
implied by nothing else. On the command line it is `-allow-unparsed-text`,
reading from the `-allow-dir` roots.

### xslt.FileResolver

`xslt.NewFileResolver(roots...)` is the one confinement every read goes
through — `fn:doc` and `xsl:import`, external entities, `fn:unparsed-text` and
XInclude alike. Its fields are set on the returned value; the constructor takes
only the roots.

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `Roots` | `[]string` | the constructor's arguments | The directories a path may resolve inside. A path escaping all of them is refused, and a symlink is resolved before the check. |
| `AllowDOCTYPE` | `bool` | refuse | Permits a `DOCTYPE` in the documents this resolver parses. |
| `ExternalEntities` | `bool` | refuse | Permits those documents to read external entities, through this same resolver. Separate from `AllowDOCTYPE`. |
| `UnparsedText` | `bool` | refuse | Permits `fn:unparsed-text` to read through this resolver. |
| `MaxBytes` | `int64` | `DefaultMaxResourceBytes` = 64 MB | Bounds **one file**, whichever way it is asked for. Zero means the default; negative means no limit. A file over the limit is refused, naming it — never truncated. |

`MaxBytes` is one number rather than one per call path deliberately. The
confinement is a property of the *file*: every root is readable by every path,
so a stylesheet refused a large file through `fn:unparsed-text` would simply
ask for it through `fn:doc`, and two limits would only mean the effective one
is the larger of them. It is set to the same 64 MB as `xdm.DefaultMaxBytes`, so
that a document refused at the read is refused before the bytes are spent and
the parser's identical limit stays the backstop for input arriving another way.

It bounds what `xdm.ParseOptions.MaxBytes` cannot. That limit bounds the
*parse*; the resolver has the whole file in memory before the parser is handed
anything, and `fn:unparsed-text` and XInclude `parse="text"` never reach a
parser at all.

```go
r, err := xslt.NewFileResolver("/srv/xsl")
if err != nil { return err }
r.UnparsedText = true
r.MaxBytes = 4 << 20   // this deployment's files are small
```

### XInclude

Off unless asked for, and asked for per call rather than per process. XInclude
is not a parsing option: XInclude 1.0 §4 defines it as a transformation from
one infoset to another, so it runs as a pass over an already-parsed tree.

```go
tree, err := xdm.ParseString(src, opts)
if err != nil { return err }
err = xdm.ProcessXInclude(tree, xdm.XIncludeOptions{
    Resolver: resolver, // an *xslt.FileResolver, or your own
    Parse:    opts,     // the included documents get the same limits
})
```

`ProcessXInclude` modifies the tree in place and re-finalises document order,
so node identities must not be held across the call.

**The resolver is the whole of the confinement.** `xdm` has no filesystem and
no network; it can read only what a resolver hands it. `xslt.FileResolver`
implements `xdm.IncludeResolver` through the same `resolvePath` that gates
`fn:doc`, `xsl:include` and external entities — a non-file scheme is rejected
before the filesystem is touched, symlinks are resolved before the containment
check, and a path outside the roots is refused. An inclusion therefore reaches
nothing `fn:doc` could not already reach. A **nil** resolver refuses every
inclusion, which is not the same as doing nothing: the include still fails, so
it still uses its `xi:fallback` or is a fatal error.

Implemented: `parse="xml"` and `parse="text"` (honouring `encoding`), an
absent or empty `href` meaning the including document, `xi:fallback` on any
failure, the `xml:base` fixup of §4.5.5, recursive inclusion, and loop
detection. For `xpointer`, the two forms XInclude requires — a **shorthand**
pointer (an ID) and the **element()** scheme (a child sequence) — are
supported. `xpointer()`/`xpath()` are not: they are not required by XInclude,
and they would invert this package's dependency on the XPath evaluator.
RFC 5147 text fragments (`line=`, `char=`, `search=`) are **not** implemented
either; they are a DocBook convention layered on `parse="text"`, not part of
XInclude, and an unsupported pointer part falls through by the XPointer
Framework's own rule rather than being an error.

Two resource budgets apply to one pass: at most 200 resources are read in
total, and inclusions may nest at most 40 deep. Both report `resource limit
exceeded`, and neither substitutes for the other — a fan-out of a thousand
distinct files repeats nothing and still costs a thousand parses.

Neither is loop detection. An inclusion loop is caught by URI, on the path of
inclusions currently in progress, and reported as `circular xi:include loop`
naming the URI — at depth one for a document that includes itself, rather than
after forty fetches. The path is keyed on the URI the resolver reports, so two
spellings of one file are one entry, and it is popped on the way out, so a
diamond of inclusions is legal. Neither budget is configurable: they bound one
pass of an opt-in transformation, and the switch that decides whether
inclusions happen at all is the resolver the caller supplies.

The `go-xml` command exposes this as `-xinclude`, reading from the
`-allow-dir` roots.

### Regular-expression backreferences

Not configurable, and deliberately so. `fn:matches` resolves a backreference
when every group it names has a **fixed** width:

```
matches("aa", "(a)\1")              → true
matches("Mum", "([md])[aeiou]\1", "i") → true
matches("A", "([A-Z])\1*")           → true
```

A group whose width can vary raises `FORX0002` instead:

```
matches("aa", "(a*)\1")             → FORX0002
```

The reason is that Go's `regexp` is RE2, which returns a *single* submatch
assignment — the greedy one. For a fixed-width group that is the only possible
assignment, so comparing the captured text is exact and runs in linear time.
For a variable-width group there are several splits and RE2 reports one, so a
comparison would answer confidently and wrongly: `(a*)` against `"aa"` does
match, by the split `"a"` + `"a"`, but the greedy assignment leaves nothing for
the backreference.

The default refuses rather than guesses: an engine that answers correctly or
says it cannot is safe to point at untrusted patterns, and one that guesses is
not safe at any setting. `xpath.SetBacktrackingRegex(true)` decides the general
case with a backtracking matcher, bounded by a step budget whose exhaustion is
an error rather than a silent "no match". It is off by default — `fn:matches`
takes its pattern from the stylesheet, and `matches($s, $node/@pattern)` takes
one from document data. See
[security.md](security.md#regular-expressions-cannot-backtrack-catastrophically).

The XML Schema pattern facet has no backreference at all — Appendix F's grammar
has no form for one — so `xsd` rejects them outright under both versions.

---

## xquery.Options

The zero value is the specification's defaults, so `xquery.Options{}` is a
conformant starting point. Every field is the caller's way of saying what the
query's prolog could have said itself — and if the prolog *does* say it, the
prolog wins.

```go
q, err := xquery.Compile(`<a>{ 1 + 2 }</a>`, xquery.Options{})
seq, err := q.Eval(xpath.NewContext(nil, xpath.Builtins()))
```

| Field | Type | Prolog equivalent | What it does |
|---|---|---|---|
| `BaseURI` | `string` | `declare base-uri` | Stamped on constructed elements; what a relative reference resolves against. |
| `BoundarySpace` | `BoundarySpace` | `declare boundary-space` | `StripSpace` (zero) discards whitespace that only separates markup; `PreserveSpace` keeps it. |
| `Construction` | `Construction` | `declare construction` | `PreserveTypes` (zero) keeps a copied node's type annotation; `StripTypes` replaces it with `xs:untyped`. |
| `DefaultElementNamespace` | `string` | `declare default element namespace` | Applied to an unprefixed *element* name. Never to an attribute name. |
| `Namespaces` | `map[string]string` | `declare namespace` | Extra prefix bindings. |

Nine prefixes are bound before `Namespaces` is consulted and never need to be
listed: `xml`, `xs`, `xsi`, `fn`, `local`, `math`, `map` and `array` from
§4.1, plus `err` from §3.16, which is what makes `catch err:FODC0002` work
with no declaration of its own.

`BoundarySpace` is the default most likely to surprise. Whitespace between
markup is **stripped**, so `<a>  <b/>  </a>` constructs `<a><b/></a>`.

A query is *code*, not data. The sandbox is the same as everywhere else in
this library — `fn:doc`, `fn:collection` and `fn:unparsed-text` all refuse
without a resolver — but an untrusted query can still spend arbitrary CPU and
memory, so bound it with `ctx.Ctx` and a timeout the way
[server.md](server.md) does for stylesheets.

See [xquery.md](xquery.md) for the guide.

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
