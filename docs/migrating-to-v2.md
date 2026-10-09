# Migrating to v2

v2 changes the exported Go API. It does not change what a stylesheet, query
or schema produces: results, error codes and serialized output stay the same,
apart from the `generate-id` strings listed below. Every breaking change in the
v2 section of [CHANGELOG.md](../CHANGELOG.md) is covered here, each with code
before and after.

Most of the work is mechanical, and a tool does it: see
[the rewriter](#the-rewriter-nodeaccess). The compiler finds the rest, because
every change below removes or renames something rather than changing what an
existing name does. The two exceptions are
[validation](#validation-never-writes-to-your-tree) and
[`generate-id`](#generate-id-strings-change).

## Module path

v2 has its own module path. v1 stays at `github.com/knroy/go-xml`.

```go
// v1
import "github.com/knroy/go-xml/xdm"

// v2
import "github.com/knroy/go-xml/v2/xdm"
```

```
go get github.com/knroy/go-xml/v2
```

The rewriter's `-v1` flag changes the imports and runs `go mod tidy` for you.

## The rewriter (nodeaccess)

`internal/tools/nodeaccess` rewrites code written against v1 into v2 code. It
type-checks your module, so it recognises an `xdm.Node` field by the type it is
selected from, not by its spelling. It handles everything listed under
[node fields](#node-fields-become-methods),
[building nodes](#nodes-are-built-with-newnode-and-setters) and
[the `xdmbuild` setters](#xdmbuild-setters-removed). It does not touch the
`xpath.Context` or `xsd` changes. The compiler finds those, and the sections
below show the fix for each.

It is a separate module inside the go-xml repository. Run it from a v2
checkout and give it the root of your module:

```
git clone https://github.com/knroy/go-xml
cd go-xml && git checkout v2            # or a v2 tag
cd internal/tools/nodeaccess
go run . -v1 /path/to/your/module
```

What `-v1` does:

1. Rewrites every `github.com/knroy/go-xml/...` import in your module's `.go`
   files to `github.com/knroy/go-xml/v2/...`. It skips `vendor`, `testdata`,
   directories starting with `.` or `_`, and nested modules, as the go command
   does.
2. Runs `go mod tidy` in your module, which adds the v2 requirement and
   drops v1. If the v2 version you want is not published, add it to `go.mod`
   before running the tool, with `go get github.com/knroy/go-xml/v2@<version>`
   or a `replace` directive pointing at a checkout.
3. Rewrites the node field uses, reloading and repeating until nothing is left
   that it knows how to rewrite.

It edits files in place, so commit or stash first. When it is done it prints a
`MANUAL path:line:col: …` line for each site it could not rewrite safely, and
exits 1 if there are any. Other flags:

| Flag | Effect |
|---|---|
| `-report` | Lists the sites and counts, changes nothing. |
| `-v` | Prints how many rewrites of each kind each pass made. |
| `-flip` | Only for this repository's own switch-over (renames the fields inside package `xdm`). Not needed for your code. |

Then build and test your module, and fix what the compiler reports using the
sections below. The tool was tested on a sample v1 module (range over
children, `len`, first and last child, indexed namespace loop, field reads and
writes, a node literal, `xdmbuild.SetBaseURI`): after `-v1` it built against
v2 and its v1 tests passed unchanged apart from the rewrite.

One rewrite is not exactly equivalent. `for i, c := range n.Children` becomes
`for i := range n.NumChildren() { c := n.ChildAt(i)`: the v1 loop read the
slice once, and the new loop reads the current children on each turn. A loop
body that replaces the children of the node it is iterating needs a look.

## Node fields become methods

`xdm.Node`'s fields are unexported. Read them through methods of the same
name. Slices are not handed out: children, attributes and namespace
declarations are iterators, with count and index methods beside them.

```go
// v1
if n.Kind == xdm.KindElement && n.Name.Local == "title" {
    fmt.Println(n.Value, n.BaseURI, n.TypeAnnotation)
}
for _, c := range n.Children {
    use(c)
}
for _, a := range n.Attrs {
    use(a)
}
for _, ns := range n.Namespaces {
    use(ns)
}
k := len(n.Children)
first, last := n.Children[0], n.Children[len(n.Children)-1]
third := n.Children[2]
a0 := n.Attrs[0]
p := n.Parent

// v2
if n.Kind() == xdm.KindElement && n.Name().Local == "title" {
    fmt.Println(n.Value(), n.BaseURI(), n.TypeAnnotation())
}
for c := range n.Children() {
    use(c)
}
for a := range n.Attrs() {
    use(a)
}
for ns := range n.NamespaceDecls() {
    use(ns)
}
k := n.NumChildren()
first, last := n.FirstChild(), n.LastChild()
third := n.ChildAt(2)
a0 := n.AttrAt(0)
p := n.Parent()
```

| v1 field | v2 read | count, index |
|---|---|---|
| `Kind`, `Name`, `Value`, `Parent`, `BaseURI`, `DocumentURI` | same name, called | |
| `TypeAnnotation`, `UnionMember`, `DerivedPrimitive`, `ListItem`, `IsID`, `IsIDREFS`, `IsNilled`, `NoTypedValue`, `MixedContent` | same name, called | |
| `Children` | `Children()` (iterator) | `NumChildren`, `ChildAt`, `FirstChild`, `LastChild` |
| `Attrs` | `Attrs()` (iterator) | `NumAttrs`, `AttrAt` |
| `Namespaces` | `NamespaceDecls()` (iterator) | `NumNamespaceDecls`, `NamespaceDeclAt` |

Code that needs a slice collects one: `slices.Collect(n.Children())`.

## Nodes are built with NewNode and setters

A node literal becomes `xdm.NewNode(kind, name, value)`, and a field write
becomes a setter. The typing fields are written through `ApplyTyping` and the
`SetTypeAnnotation*` methods.

```go
// v1
t := &xdm.Node{Kind: xdm.KindText, Value: "hello"}
el := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "p"}, Parent: doc}
el.Name = xdm.QName{Local: "para"}
el.BaseURI = "http://example.com/"
el.Children = kids
el.TypeAnnotation = "string"

// v2
t := xdm.NewNode(xdm.KindText, xdm.QName{}, "hello")
el := xdm.NewNode(xdm.KindElement, xdm.QName{Local: "p"}, "")
el.SetParent(doc)
el.SetName(xdm.QName{Local: "para"})
el.SetBaseURI("http://example.com/")
el.SetChildren(kids)
el.SetTypeAnnotation("string")
```

The setters are `SetName`, `SetValue`, `SetParent`, `SetChildren`,
`SetAttrs`, `SetNamespaceDecls`, `SetBaseURI` and `SetDocumentURI`. Like the
v1 field writes they replace, they set one link and do nothing else:
`SetChildren` does not set the children's parents, and no setter attaches the
node to a tree. To build a tree with parents and document order set for you,
use the builder functions (`AppendChild`, `AddAttr`, `AddNamespace` and the
`xdmbuild` constructors), as in v1.

## xdmbuild setters removed

`xdmbuild.SetParent`, `SetChildren`, `SetAttrs`, `SetNamespaces`, `SetName`
and `SetBaseURI` are gone. Call the node's method. `SetNamespaces` is
`SetNamespaceDecls`.

```go
// v1
xdmbuild.SetParent(n, p)
xdmbuild.SetNamespaces(n, decls)

// v2
n.SetParent(p)
n.SetNamespaceDecls(decls)
```

## xpath.Context: per-evaluation settings move to Env

`xpath.Context` is split. Its focus and variables stay on the `Context`. The
settings that hold for a whole evaluation (resolvers, clock, limits and so on)
move to an `xpath.Env` that every derived context shares, which is what makes
deriving a context cheap. The static settings (version, static base URI) are
read through methods and changed through `With*` methods, which return a new
context.

Set the environment when you create the context:

```go
// v1
ctx := xpath.NewContext(doc, xpath.Builtins())
ctx.Docs = resolver
ctx.ImplicitTimezone = 60 // minutes east of UTC
ctx.MaxItems = 100000

// v2
ctx := xpath.NewContext(doc, xpath.Builtins(), func(e *xpath.Env) {
    e.Docs = resolver
    e.ImplicitTimezone = 60
    e.MaxItems = 100000
})
```

or change it later, on a copy:

```go
// v1
ctx.Docs = resolver

// v2
ctx = ctx.WithEnv(func(e *xpath.Env) { e.Docs = resolver })
```

Read a setting through `Env()`:

```go
// v1
r := ctx.Docs

// v2
r := ctx.Env().Docs
```

The fields that moved are `Ctx`, `Docs`, `Collections`, `Texts`, `Entities`,
`Environment`, `Modules`, `Validator`, `Now`, `HasNow`, `ImplicitTimezone`,
`RegexVersion`, `LibraryVersion`, `MaxDepth`, `MaxItems`, `QualifyVar`,
`MissingVar` and `MapDuplicateCode`.

The budgets themselves (the counters an evaluation is charged against) cannot
be set or reset through `Env`: `WithEnv` keeps the context's own counters
whatever the function does.

The static part:

```go
// v1
v := ctx.Version
ctx.Version = xpath.XPath31
ctx.StaticBaseURI = "http://example.com/"
if ctx.Compat { … }

// v2
v := ctx.Version()
ctx = ctx.WithVersion(xpath.XPath31)
ctx = ctx.WithStaticBaseURI("http://example.com/")
if ctx.Compat() { … }
```

`StaticHost` and `StaticNamespaces` are methods too, read-only.

## Context.WithNow removed

Set the clock through the environment:

```go
// v1
ctx = ctx.WithNow(t)

// v2
ctx = ctx.WithEnv(func(e *xpath.Env) { e.Now, e.HasNow = t, true })
```

`xslt.TransformOptions.Now` is unchanged.

## Validation never writes to your tree

`xsd.ValidateOptions.Annotate` is renamed `AnnotateInPlace`, and
`Schema.Validate` without it no longer writes anything to the tree it is
given.

To get a typed tree, which is what makes a validated `<price>` atomise to an
`xs:decimal` in XPath, XSLT and XQuery, use `ValidateCopy`. It validates a
copy and returns the copy's counterpart of the node you passed, with types,
defaulted attributes and stripped ignorable whitespace. The input is left as
it was, so one parsed document can be validated from several goroutines.

```go
// v1
if err := schema.Validate(doc.Root, xsd.ValidateOptions{Annotate: true}); err != nil {
    return err
}
res, err := sheet.Transform(ctx, doc.Root, opts)

// v2
typed, err := schema.ValidateCopy(doc.Root, xsd.ValidateOptions{})
if err != nil {
    return err
}
res, err := sheet.Transform(ctx, typed, opts)
```

The copy is a new tree. `is` between it and the original is false, and it
carries no source positions, so a failure on it still reports the original's
line and column but `gx:line-number()` has nothing to read on it.

`AnnotateInPlace: true` keeps v1's `Annotate` behaviour exactly. Use it for a
tree you have just built or parsed yourself and that nothing else holds, when
you want the typing in that tree and not in a copy:

```go
// v1
err := schema.Validate(doc.Root, xsd.ValidateOptions{Annotate: true})

// v2, the same behaviour
err := schema.Validate(doc.Root, xsd.ValidateOptions{AnnotateInPlace: true})
```

This is the one change the compiler cannot find for you: code that never set
`Annotate` still compiles. In v1, `Validate` without `Annotate` still wrote
two things to the tree: the member type that matched a union-typed value, and
`nilled` on an element with `xsi:nil="true"`. Code that read those after a
plain `Validate` reads nothing in v2. Use `ValidateCopy`, or
`AnnotateInPlace`, which records them as before.

`ValidateElement`, `ValidateElementLax`, `ValidateAttribute` and
`ValidateAgainstType` take the same options and follow the same rule.

## generate-id strings change

`generate-id()` returns `N<tree>x<order>`, for example `N3x17`, where v1.0
returned `N` followed by one number. The old numbers could collide between
two trees when a tree had more than 2^20 nodes. This change is also in the v1
line after v1.0, so it is not specific to v2.

The spec leaves the strings to the implementation. Compare them for equality
within one transformation; do not parse them, store them, or compare them
across runs.

## Not changed

The command-line tool's flags and output, the `xslt`, `xquery` and `relaxng`
entry points, `xsd` schema loading, and every error code are the same as in
v1.
