# Recipes

Each of these is a shape that comes up in practice. All the code compiles; the
imports are elided for brevity but are the obvious ones.

## Validate against several rule sets at once

E-invoicing specifications layer rules: a base standard, a national extension,
a jurisdiction profile. Parse once, run each set over the same tree.

```go
type Suite struct {
    sheets map[string]*xslt.Stylesheet // name → compiled rules
}

func (s *Suite) ValidateAll(ctx context.Context, src []byte) (map[string]*xslt.Result, error) {
    tree, err := xdm.ParseString(string(src), xdm.ParseOptions{TrackPositions: true})
    if err != nil {
        return nil, fmt.Errorf("not well-formed: %w", err)
    }
    out := make(map[string]*xslt.Result, len(s.sheets))
    for name, sheet := range s.sheets {
        res, err := sheet.Transform(ctx, tree.Root, xslt.TransformOptions{})
        if err != nil {
            return nil, fmt.Errorf("rule set %q: %w", name, err)
        }
        out[name] = res
    }
    return out, nil
}
```

Running them **concurrently** is also safe — each `Transform` is independent
and the source tree is read-only — but on documents of ordinary size the
parse dominates, so the goroutines rarely pay for themselves. Measure before
adding them.

## Batch a directory

The CLI already does this: `-keep-going` reports every failure instead of
stopping at the first, and still exits non-zero.

```
go-xml -xsl rules.xslt -keep-going invoices/*.xml
```

In code, the thing worth getting right is that one bad document must not
abort the run:

```go
var failed int
for _, path := range paths {
    src, err := os.ReadFile(path)
    if err != nil {
        log.Printf("%s: %v", path, err)
        failed++
        continue
    }
    res, err := v.Validate(ctx, src)
    if err != nil {
        log.Printf("%s: %v", path, err)
        failed++
        continue
    }
    report(path, res)
}
if failed > 0 {
    os.Exit(1)
}
```

## Render XML to HTML

The same engine, a different stylesheet. Output settings come from
`xsl:output` in the stylesheet, so `Serialize` already knows whether to emit
XML or HTML:

```go
res, err := renderer.Transform(ctx, tree.Root, xslt.TransformOptions{})
if err != nil {
    return err
}
w.Header().Set("Content-Type", "text/html; charset=utf-8")
return res.Serialize(w)
```

`Serialize` writes straight to the `http.ResponseWriter`; `res.String()`
builds the whole document in memory first, so prefer `Serialize` in a handler.

## Split one document into many

`xsl:result-document` produces secondary outputs. This engine **never writes
them to disk** — a transform that can create files anywhere the process can
write is a decision the caller should make:

```go
res, err := splitter.Transform(ctx, tree.Root, xslt.TransformOptions{})
if err != nil {
    return err
}
for _, doc := range res.Secondary {
    // doc.Href is whatever the stylesheet asked for. Validate it before
    // using it as a path: it is stylesheet-controlled input.
    name := filepath.Base(filepath.Clean(doc.Href))
    if name == "." || name == string(filepath.Separator) {
        return fmt.Errorf("unusable href %q", doc.Href)
    }
    f, err := os.Create(filepath.Join(outDir, name))
    if err != nil {
        return err
    }
    err = doc.Serialize(f, nil)
    if cerr := f.Close(); err == nil {
        err = cerr
    }
    if err != nil {
        return err
    }
}
```

Note the close-error handling: `defer f.Close()` would discard a flush failure,
which is exactly when you most want to know.

## Pass parameters into a stylesheet

Top-level `xsl:param` values are supplied per transform, so one compiled
stylesheet can serve many configurations:

```go
res, err := sheet.Transform(ctx, tree.Root, xslt.TransformOptions{
    Params: map[string]xdm.Sequence{
        "profile":  xdm.One(xdm.NewString("OM-B2G")),
        "strict":   xdm.One(xdm.NewBoolean(true)),
        "asOfDate": xdm.One(xdm.NewString("2026-01-01")),
    },
})
```

Keys are Clark names: `"local"` for no namespace, `"{uri}local"` otherwise.

## Make a transform reproducible

`fn:current-dateTime` follows the wall clock by default, which makes
golden-file tests flap. Pin it:

```go
res, err := sheet.Transform(ctx, tree.Root, xslt.TransformOptions{
    Now:              time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC),
    ImplicitTimezone: 240, // minutes; affects unzoned date comparison
})
```

The CLI equivalents are `-now` and `-timezone`.

## Load code lists from disk

Rule sets routinely check values against shipped code lists via `document()`.
That is refused unless you configure a resolver:

```go
res, err := xslt.NewFileResolver("rules", "codelists")
if err != nil {
    return err
}

// For xsl:include and xsl:import, at compile time:
sheet, err := xslt.Compile(tree.Root, xslt.CompileOptions{Resolver: res})

// For doc() and document(), at transform time:
result, err := sheet.Transform(ctx, doc.Root, xslt.TransformOptions{
    Documents: res,
})
```

The same resolver serves both. It confines reads to the named directories,
resolves symlinks *before* the containment check, and refuses every non-`file`
scheme. There is no network option.

## Write a custom resolver

To serve rule sets from embedded files, a database, or an object store,
implement the interface yourself:

```go
type embeddedResolver struct{ fsys fs.FS }

func (e embeddedResolver) ResolveDocument(uri, base string) (*xdm.Tree, error) {
    // uri is stylesheet-controlled. Reject anything that is not a plain
    // relative name before touching the filesystem.
    name := path.Clean(uri)
    if path.IsAbs(name) || strings.HasPrefix(name, "..") {
        return nil, fmt.Errorf("refusing %q", uri)
    }
    data, err := fs.ReadFile(e.fsys, name)
    if err != nil {
        return nil, err
    }
    return xdm.ParseString(string(data), xdm.ParseOptions{BaseURI: name})
}
```

`fn:doc` is defined to return the *same* node for the same URI within one
execution, so `doc('x') is doc('x')` is true. If your resolver re-parses on
every call it will break node identity — cache the tree.

## Evaluate XPath without XSLT

The `xpath` package stands alone:

```go
tree, err := xdm.ParseString(src, xdm.ParseOptions{})
if err != nil {
    return err
}
ctx := xpath.NewContext(tree.Root, xpath.Builtins())
seq, err := xpath.Eval("sum(//invoice/@total)", ctx, nil)
```

For an expression evaluated repeatedly, compile it once — that is also what
triggers constant folding:

```go
compiled, err := xpath.Compile("count(//item[@price > 100])", nil)
for _, tree := range trees {
    seq, err := compiled.Eval(xpath.NewContext(tree.Root, xpath.Builtins()))
}
```
