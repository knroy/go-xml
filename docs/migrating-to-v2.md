# Migrating to v2

v2 changes the exported Go API. It does not change what a stylesheet, query
or schema produces: results, error codes and serialized output stay the same,
apart from the `generate-id` strings, the relative order of nodes from
different trees, and the failure of an XSLT global variable nothing reads
([no longer reported](#global-variables-are-evaluated-on-first-use)), listed
below. Every breaking change in the
v2 section of [CHANGELOG.md](../CHANGELOG.md) is covered here, each with code
before and after.

Most of the work is mechanical, and a tool does it: see
[the rewriter](#the-rewriter-nodeaccess). The compiler finds the rest, because
every change below removes or renames something rather than changing what an
existing name does. The exceptions are
[validation](#validation-never-writes-to-your-tree),
[`generate-id`](#generate-id-strings-change),
[global variables](#global-variables-are-evaluated-on-first-use), and that a tree is now
[built top-down](#trees-are-built-top-down-by-appending): an append to a node
that is no longer being built panics at run time.

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

## Trees are built top-down, by appending

A v2 node is a 40-byte record in its tree's record array, and a tree is
stored in document order: each element followed by its attributes, then its
children. So a tree is built the way a parser reads one, top-down, by
appending to a node that is still being built, and nothing is ever inserted,
moved or relinked afterwards. Code that changed a tree's shape builds a copy
with the change made.

```go
// v1
doc := xdm.NewTree()
el := &xdm.Node{Kind: xdm.KindElement, Name: xdm.QName{Local: "p"}}
el.AddAttr(&xdm.Node{Name: xdm.QName{Local: "id"}, Value: "a1"})
el.AppendChild(&xdm.Node{Kind: xdm.KindText, Value: "hello"})
doc.Root.AppendChild(el)
doc.Finalize()

// v2
doc := xdm.NewTree()
el := doc.Root.AppendElement(xdm.QName{Local: "p"})
el.AppendAttr(xdm.QName{Local: "id"}, "a1")
el.AppendText("hello")
doc.Finalize()
```

The calls are `AppendElement`, `AppendText`, `AppendComment`, `AppendPI`,
`AppendAttr` (before the element's first child), `AddNamespace`,
`AppendCopy` (a deep copy) and `AppendShallowCopy` (the node alone, for a copy
you complete yourself). A node is open from the call that made it until you
append to something that is not it or inside it; appending to a closed node,
or adding an attribute after a child, panics. `Finalize` closes everything.

What may still change after a node is made is its scalar state: `SetName`,
`SetValue`, `AppendValue` (merging adjacent text), `SetBaseURI`,
`SetDocumentURI`, the typing setters, and the namespace declarations of an
element (`AddNamespace`, `SetNamespaceDecl`, `RemoveNamespaceDecls`) of a tree
that is not a parsed document. A parsed document is frozen.

`xdm.NewNode(kind, name, value)` still makes a parentless node; it belongs to
a fragment of its own, and its `Tree()` is nil as before. Many parentless
nodes made together (a builder's sequence, say) share one fragment:
`xdm.NewFragment()` then `NewRoot(kind, name, value)`. `xdm.Copy`,
`CopyPruned` and `ShallowCopy` give parentless copies.

Gone: `SetParent`, `SetChildren`, `SetAttrs`, `SetNamespaceDecls`,
`AppendChild` and `AddAttr` of a node you made separately,
`SetSynthesizedOrder`, and `xdmbuild.ShallowCopy`, `ReplaceChild` and
`PrependChild`. `RemoveLastChild` and `ReplaceLastChild` exist for the one case
where the subtree just appended has to be taken back or exchanged.

```go
// v1: rewrite a finished tree in place
el.SetAttrs(append(attrs, extra))
el.SetChildren(kept)

// v2: build the rewritten tree
c := parent.AppendShallowCopy(el)
for a := range el.Attrs() {
    c.AppendShallowCopy(a)
}
c.AppendAttr(extra.Name(), extra.Value())
for ch := range el.Children() {
    if keep(ch) {
        c.AppendCopy(ch)
    }
}
```

Reading is as in the previous section, with three additions that are
constant time per step: `NextSibling`, `PrevSibling` and `Descendants` (a
scan of the subtree). `NumChildren` and `ChildAt` now walk the children, so a
loop over them belongs with `Children()` or `NextSibling`.
`DeclaredNamespaces()` reads an element's declarations as prefix and URI
without making a namespace node for each.

Namespace nodes exist once per element and prefix: the namespace axis, and
`NamespaceDeclAt`, return the same pointer every time, so `Is` and `==`
agree for them.

`xdm.ProcessXInclude` returns the included document as a new tree instead of
editing the one it is given: `tree, err = xdm.ProcessXInclude(tree, opts)`.

## xdmbuild setters removed

`xdmbuild.SetParent`, `SetChildren`, `SetAttrs`, `SetNamespaces`, `SetName`
and `SetBaseURI` are gone. Call the node's method where there is one (`SetName`,
`SetBaseURI`), or build the tree top-down as above.

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

`xsd.ValidateOptions.Annotate` is gone, and `Schema.Validate` only checks: it
never writes to the tree it is given.

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

There is no in-place annotation, not even for a tree you have just built: a
tree is never edited once its nodes are made, and annotating adds attributes
and drops whitespace. The typed-copy counterparts of the other entry points
are `ValidateElementLaxCopy`, `ValidateAttributeCopy` and
`ValidateAgainstTypeCopy`; `ValidateElement`, `ValidateElementLax`,
`ValidateAttribute` and `ValidateAgainstType` check only.

This is the one change the compiler cannot find for you if your code never
set `Annotate`: in v1 before `5c2ca9c`, `Validate` without `Annotate` still
wrote two things to the tree: the member type that matched a union-typed
value, and `nilled` on an element with `xsi:nil="true"`. Code that read those
after a plain `Validate` reads nothing in v2. Use `ValidateCopy`.

To ask whether a tree carries any typing at all, call `n.TreeHasTyping()` on
any of its nodes. A copy from `ValidateCopy` does; a parsed document does not,
and false means every node in the tree is untyped.

## generate-id strings change

`generate-id()` returns `N<tree>x<position>`, for example `N3x17`, where v1.0
returned `N` followed by one number. The old numbers could collide between
two trees when a tree had more than 2^20 nodes. The format is also in the v1
line after v1.0; in v2 the numbers are the node's position in its record
array, so they differ from v1's for the same document.

Nodes of different trees are ordered by when the trees were numbered: a
document when it is made, a set of constructed nodes the first time one of
them is compared. The spec leaves that order to the implementation, and it can
differ from v1's for constructed nodes.

The spec leaves the strings to the implementation. Compare them for equality
within one transformation; do not parse them, store them, or compare them
across runs.

## HTTPResolver moves to package xsdnet

`xsd.HTTPResolver` was the one part of the module that needed `net/http`, so
every program that imported `xsd` linked it, TLS and x509, fetching or not. It
now lives in `github.com/knroy/go-xml/v2/xsd/xsdnet` with the same fields and
behaviour, and so do the names that only it uses:

| v1 | v2 |
|---|---|
| `xsd.HTTPResolver` | `xsdnet.HTTPResolver` |
| `xsd.ErrPrivateAddress` | `xsdnet.ErrPrivateAddress` |
| `xsd.DefaultFetchTimeout` | `xsdnet.DefaultFetchTimeout` |
| `xsd.DefaultMaxSchemaBytes` | `xsdnet.DefaultMaxSchemaBytes` |

```go
// v1
import "github.com/knroy/go-xml/xsd"

opts := xsd.Options{Resolver: &xsd.HTTPResolver{AllowHost: allow}}

// v2
import (
    "github.com/knroy/go-xml/v2/xsd"
    "github.com/knroy/go-xml/v2/xsd/xsdnet"
)

opts := xsd.Options{Resolver: &xsdnet.HTTPResolver{AllowHost: allow}}
```

The `goxml_nohttp` build tag is gone. It existed to leave `HTTPResolver` and
`net/http` out of a build; a program that does not import `xsdnet` now gets
that without a tag.

## Global variables are evaluated on first use

An XSLT global variable or parameter is evaluated when a reference first
needs it, not when the transform starts. A global nothing reads is not
evaluated, so its failure is no longer reported; section 2.14 of XSLT 3.0
allows this. What stays as it was:

- A failure met through a reference is not caught by an `xsl:try` around the
  reference (section 9.5), and the code is the one the eager evaluation gave.
- A required parameter left unset is `XTDE0050` at the start.
- A global that names itself is `XPST0008` at the start, read or not.
- A global whose own `select` or content holds `xsl:message`, `xsl:assert`,
  `xsl:result-document` or a call to `fn:trace` is evaluated at the start, so
  its effect does not depend on whether anything reads it. One that reaches
  such an instruction only through a function or template it calls is
  evaluated on first use, as Saxon does.

A host language can bind variables the same way through
`xpath.Context.WithLazyVars` and `xpath.LazyVar`; an expression's reference
reports the evaluation's error, while `Context.LookupVar` reports a failed
one as unbound.

## Not changed

The command-line tool's flags and output, the `xslt`, `xquery` and `relaxng`
entry points, `xsd` schema loading, and every error code are the same as in
v1.
