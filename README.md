# go-xml

**XML Schema 1.0/1.1 validation, XPath 2.0 and XSLT 2.0 in pure Go.** No cgo,
no JVM, no libxml2 — one `go get`, and it cross-compiles like any Go package.

```
go get github.com/knroy/go-xml
```

Validate a document against a schema:

```go
schema, err := xsd.LoadFile("schemas/invoice.xsd", xsd.Options{})
tree, err := xdm.ParseString(src, xdm.ParseOptions{})
err = schema.Validate(tree.Root, xsd.ValidateOptions{})
```

Every error carries the spec's code and a path — `cvc-datatype-valid.1` at
`/invoice/total` — not just "invalid".

### The four packages

| | |
|---|---|
| [`xdm`](xdm/) | the parser and the XDM tree everything else reads |
| [`xsd`](xsd/) | XML Schema 1.0 and 1.1: assembly, validation, PSVI annotation |
| [`xpath`](xpath/) | XPath 2.0: lexer, parser, evaluator, `fn:` library |
| [`xslt`](xslt/) | XSLT 2.0: templates, modes, keys, grouping, serialisation |

### Safe by default

Aimed at input arriving over the wire, so the defaults are the settings you
would choose for untrusted documents. `Options{}` is already hardened:

* **DOCTYPE is refused**, which closes XXE and entity expansion at the door
* **No network access** — a `schemaLocation` cannot make this process fetch
* **`xsi:schemaLocation` is ignored**, so a document cannot pick its own schema
* **Size, node-count and depth limits** are on, sized for a general service

See [docs/security.md](docs/security.md) for the threat model and
[docs/server.md](docs/server.md) for a validating HTTP endpoint measured
against XXE, billion-laughs and resource exhaustion.

### Building and checking

```
go build ./...
go test -race ./...
```

`tests/check.sh` runs everything that has to pass before a change is done —
build, vet, unit tests, race, both W3C suites and the production corpora:

```
tests/check.sh fast                                  # no external suites
GOXSLT_UBL=<dir> GOXSLT_CII=<dir> tests/check.sh     # everything
```

A missing suite is reported as skipped; a suite that is present but produces no
result is a **failure**. A check that did not run must not look like one that
succeeded.

One dependency: `golang.org/x/text`, for Unicode normalisation and
language-sensitive collation. Nothing else is outside the standard library.
Requires Go 1.26 or later.

## Status

| | |
|---|---|
| **XPath 2.0** | 99.99% of the W3C QT3 suite (15,180 of 15,181 in scope) |
| **XSLT 2.0** | complete, including `xsl:import-schema`; verified against Saxon-HE 12.4 on two production corpora |
| **XSD 1.0** | 99.80% of the W3C xsdtests *instance* tests (24,953 of 25,003); **98.60%** of its *schema-validity* tests (14,204 of 14,405) |
| **XSD 1.1** | 99.79% instance (26,155 of 26,209); **97.93%** schema-validity (15,048 of 15,365); opt-in via `Version11` |
| **Tests** | 671, clean under `-race` (a few subtests skip without the corpora below) |
| **Production schemas** | UBL 2.1, UN/CEFACT CII, Factur-X/ZUGFeRD, Peppol BIS 3.0 — 88 schemas load, instances validate clean |
| **API** | pre-1.0; the shape is settled but not frozen |

**Read this before adopting it.** Three things are commonly assumed and are not
true here:

1. **Particle Valid (Restriction) is not checked** in either version, so a
   schema invalid in that specific way is accepted rather than reported —
   libxml2 does not implement it either and Xerces leaves it off by default.
   Both versions otherwise measure above 99% on their part of the suite; the
   remaining disagreements are listed in *Where it fails*, along with what the
   suite skips and why.
2. **A backreference to a variable-width group is refused, by design.** RE2
   has no backreferences, which is also why no pattern can hang this engine.
   Where the group has a *fixed* width the backreference is resolved exactly,
   in linear time — that covers `(a)\1`, `([md])[aeiou]\1` and the like. Where
   it can vary, `(a*)\1`, there is more than one way to split the match and RE2
   reports only one, so the answer is `FORX0002` rather than a guess. The XML
   Schema pattern facet has no backreference at all and rejects them outright,
   which is conformant: Appendix F's grammar has no form for one.
3. **The XSLT layer has no conformance suite behind it.** The W3C's XSLT tests
   target 3.0 and do not carry over. Its evidence is a differential against
   Saxon on real rule sets — strong, but corpus-shaped rather than systematic.
   *Where it fails* sets out exactly what that does and does not cover.

Every remote-reference mechanism — `DOCTYPE`, `fn:doc`, file reads — is off
unless enabled; see [Security defaults](#security-defaults).

## What this is

Four packages, each usable on its own:

| Package | What it holds |
|---|---|
| [`xdm`](xdm/) | The XQuery/XPath data model: typed atomic values, the node tree, an XML parser |
| [`xpath`](xpath/) | XPath 2.0: lexer, parser, evaluator, and the `fn:` function library |
| [`xslt`](xslt/) | XSLT 2.0: pattern matching, the stylesheet compiler, the transform runtime, serialisation |
| [`xsd`](xsd/) | XML Schema 1.0: the component model, schema assembly, content models, facets, identity constraints |
| [`cmd/go-xml`](cmd/go-xml/) | A command-line transformer |

## Documentation

* **[docs/validation.md](docs/validation.md)** — XSD validation, Schematron,
  and which kind of "valid" you actually need. Start here if you came to check
  a document against an `.xsd`.
* **[docs/xsd.md](docs/xsd.md)** — the schema validator in detail: 1.0 versus
  1.1, resolvers and what they will fetch, the PSVI, limits, concurrency, and
  what the conformance figures cover.
* **[docs/server.md](docs/server.md)** — compile once, transform or validate
  per request: a complete XSLT service, a hardened XSD validation endpoint
  (measured against XXE, billion-laughs and resource exhaustion), timeouts,
  limits, hot-reloading rule sets.
* **[docs/recipes.md](docs/recipes.md)** — batching, splitting, HTML
  rendering, parameters, custom resolvers, standalone XPath.
* **[docs/options.md](docs/options.md)** — every option in `xdm`, `xpath`,
  `xsd` and `xslt`: what each field does, what the zero value means, and the
  limits that bound a parse, a validation and a transform.
* **[docs/security.md](docs/security.md)** — threat model and audit results:
  XXE and entity expansion are closed even with `AllowDOCTYPE` on, and the two
  things a caller must still do themselves.
* **[docs/known-gaps.md](docs/known-gaps.md)** — every measured failure and why
  it is still open, including the fix attempts that were reverted because they
  cost more than they gained.

## Architecture

The layering is strict and one-directional. XSLT uses XPath, XPath uses XDM,
and XDM knows nothing about either — which is what keeps the data model honest
when XSLT needs something awkward.

```
  cmd/go-xml          command-line transformer
        │
  ┌─────▼──────────────────────────────────────────┐
  │ xslt      stylesheet compiler + runtime        │
  │           patterns · templates · instructions  │
  │           serialisation · result documents     │
  └─────┬──────────────────────────────────────────┘
        │  compiles match patterns and select
        │  expressions; evaluates them per node
  ┌─────▼──────────────────────────────────────────┐
  │ xpath     lexer → parser → optimiser → runtime │
  │           functions · operators · type system  │
  └─────┬──────────────────────────────────────────┘
        │  every value is an Item; every result a Sequence
  ┌─────▼──────────────────────────────────────────┐
  │ xdm       nodes · atomic values · sequences    │
  │           QNames · the XML parser              │
  └────────────────────────────────────────────────┘
```

**XDM is the centre, not a wrapper over `encoding/xml`.** The Go decoder is
used as a *tokeniser only*: it resolves prefixes into `Name.Space` and then
discards both the prefix and the `xmlns` declarations, and XSLT needs both —
namespace nodes are addressable on the namespace axis, and a literal result
element must serialise with the prefix its author wrote. So the tree is built
here. `encoding/xml` appears in exactly one file.

**Three types carry the model.** `Item` is a closed interface over `*Node`,
`*Atomic`, and `*Opaque` (engine-internal state threaded through the same
interface). `Sequence` is a flat `[]Item` — XDM has no nested sequences, and
every constructor maintains that. `Atomic` is a tagged union rather than
`interface{}`, because XPath's type system is not Go's: `xs:decimal` is a
`big.Rat` so `0.1 + 0.2` is exactly `0.3`, and `xs:dateTime` is not `time.Time`
because XML Schema timezones are *optional* and "absent" is a distinct value
from `+00:00`.

**Static and dynamic context are separate types.** Namespace bindings, the
default element namespace, and the function library are resolved at *parse*
time through `NamespaceResolver`. The context item, position, size, variables,
clock, and timezone live on `Context` at *evaluation* time. Merging them is a
trap: XSLT compiles one pattern once and evaluates it against every node, so
anything static must not be re-resolved per node.

**Compilation is separate from execution.** `Compile` is the expensive step and
produces an immutable `Stylesheet`; `Transform` does not mutate it. That is what
makes one compiled rule set safe to share across goroutines, and it is tested
under `-race` with a shared source tree as well as a shared stylesheet.

**Patterns are matched right to left.** An XSLT `match` pattern looks like a
path but means "does this node match", not "navigate from here". Evaluating the
path and testing membership is quadratic; matching upward from the candidate is
O(depth), which is the difference between finishing on a large document and not.

## Use

```go
sheetTree, err := xdm.ParseString(stylesheetSource, xdm.ParseOptions{})
sheet, err := xslt.Compile(sheetTree.Root, xslt.CompileOptions{})

docTree, err := xdm.ParseString(documentSource, xdm.ParseOptions{})
result, err := sheet.Transform(ctx, docTree.Root, xslt.TransformOptions{})

fmt.Println(result.String())     // serialised per xsl:output
result.Tree()                    // or keep navigating it
result.Messages                  // xsl:message output, collected not printed
result.Secondary                 // documents from xsl:result-document
```

Each `SecondaryResult` carries the `Href` the stylesheet asked for, its own
`Nodes`, and the `OutputSettings` that apply to it, and serialises itself:

```go
for _, doc := range result.Secondary {
    fmt.Println(doc.Href, doc.String())
}
```

Nothing is written to disk unless you write it — see *Security defaults*.

`Compile` is the expensive step and `Transform` does not mutate the compiled
stylesheet, so compile once and transform concurrently — the test suite
exercises that under `-race`.

XPath alone:

```go
ctx := xpath.NewContext(docTree.Root, xpath.Builtins())
seq, err := xpath.Eval("sum(//invoice/@total)", ctx, nil)
```

CLI:

```
go-xml -xsl transform.xsl input.xml
go-xml -xsl rules.xsl -p year=2024 -allow-dir ./codelists invoice.xml

# Validate a directory: report every failure rather than stopping at the first.
go-xml -xsl rules.xsl -keep-going invoices/*.xml

# Pin the clock and timezone so the run is reproducible.
go-xml -xsl report.xsl -now 2024-01-15T09:00:00Z -timezone 240 in.xml

# Report the source line each validation failure occurred on.
go-xml -xsl rules.xsl -track-positions invoice.xml

# Write the documents an xsl:result-document stylesheet produces.
go-xml -xsl split.xsl -result-dir ./out catalogue.xml
```

| Flag | Effect |
|---|---|
| `-xsl` | the stylesheet to apply (required) |
| `-o` | write to a file instead of stdout |
| `-p name=value` | supply a top-level `xsl:param`; repeatable |
| `-allow-dir` | open `xsl:include`/`doc()`/`document()` to further directories; the stylesheet's own directory is always readable |
| `-allow-doctype` | permit a `DOCTYPE` in the source |
| `-timeout` | bound the transform (default 60s) |
| `-initial-template` | start at a named template instead of matching the root |
| `-mode` | initial mode for `apply-templates` |
| `-messages` | print `xsl:message` output to stderr |
| `-now` | pin `fn:current-dateTime` (RFC 3339) |
| `-timezone` | implicit timezone in minutes |
| `-track-positions` | record source line/column; see below |
| `-result-dir` | where `xsl:result-document` outputs are written |
| `-keep-going` | continue a batch past a failure, still exiting non-zero |

The exit status is 0 only if every input transformed.

## Design notes

**Compilation has an optimisation stage.** `Compile` folds closed
sub-expressions to literals before evaluation, so `(1 + 2) * 3` costs one
literal load per node rather than three operations — 13.77 ns for a whole
arithmetic expression. Two conditions guard every rewrite: the expression must
be *closed* (no variable, context item, or context-dependent function), and the
rewrite must preserve errors as well as values. `1 idiv 0` is deliberately not
folded, because refusing a stylesheet at compile time for an error in a branch
that is never taken would be wrong.

Foldable functions are an allowlist rather than a denylist. A user-defined
function is never folded even if it happens to be pure, because the library it
resolves through is supplied by the caller.

**Errors carry their specification code as a field.** A message is prose that
may be reworded; a code is what a caller branches on and what a conformance
suite compares. `xdm.ErrorCode(err)` returns it, unwrapping as it goes, and
falls back to recognising the code where an error still carries it as a message
prefix. The rendered text is unchanged, so nothing reading error strings had to
move.

This is not cosmetic: making codes inspectable let the QT3 harness stop
accepting any error where a specific one was expected, which immediately found
961 wrong codes that the loose check had been hiding.

**Aggregates over a range are arithmetic, not iteration.** An integer range is
fully determined by its bounds, so nothing has to be built to answer a question
about it:

| | |
|---|---|
| `count(lo to hi)` | `hi - lo + 1` |
| `sum(lo to hi)` | `n(first + last) / 2` |
| `min` / `max` | the bounds themselves |
| `avg` | the midpoint |

`sum(1 to 10000000)` returns `50000005000000` in 6 MB rather than materialising
ten million values at 1.9 GB. The series is summed in `big.Int`, because
`n(first+last)` overflows int64 well before the bounds do —
`sum(1 to 5000000000000)` is about 1.25 × 10²⁵ and this engine returns it
exactly, where Saxon refuses the range outright (its sequences are capped at
int32).

The general form of the idea is a lazy sequence type carrying its own
cardinality — *don't ask a sequence to enumerate itself if the operation can be
answered from its structure*. That would mean making `xdm.Sequence` an
interface rather than a slice, which every operation across three packages
indexes and ranges over directly. The narrower version gets the same result for
the cases that arise, because `lo to hi` is the only XPath 2.0 construct that
can name a sequence too large to hold: a path expression is bounded by the
document, a literal sequence by the length of the stylesheet.

The recognition is deliberately narrow — only a bare `to` directly under the
aggregate. A predicate, a `for`, or a comma sequence changes which items
survive, so those evaluate normally and meet the item budget instead:
`sum((1 to 10)[. mod 2 = 0])` is 30, not 55. That narrowness is what lets the
budget stay strict, and it is what the tests check hardest.

**Sequences and types, not node-sets and strings.** XPath 1.0 had four types
and coerced freely between them. 2.0 replaces that with sequences of typed
items and explicit promotion rules, and most of the difficulty of the language
is there rather than in the syntax. `xs:decimal` is a `big.Rat`, so `0.1 + 0.2`
is exactly `0.3`; `xs:integer div xs:integer` yields a decimal; the four
numeric types promote along `integer → decimal → float → double` before any
comparison. Implementing this with `float64` throughout would pass a
surprising number of tests and then quietly misreport a monetary total.

**Dates are not `time.Time`.** XML Schema dates carry an *optional* timezone,
where "absent" is a distinct value from `+00:00`; they exceed the year range of
int64 nanoseconds; and their seconds have arbitrary fractional precision. The
`xdm.DateTime` type models all three, because a validator that treats an
unzoned date as UTC gets comparisons wrong at the day boundary.

**Patterns are matched right to left.** An XSLT `match` pattern looks like a
path expression but means "does this node match", not "navigate from here". The
obvious implementation — evaluate the path, test membership — is quadratic.
Matching from the candidate node upward makes it O(depth), which is the
difference between a transform that finishes on a large document and one that
does not.

**Templates are pre-sorted.** Selection scans the template list and stops at
the first match, because the list is ordered by (import precedence, priority,
declaration order) at compile time. Default priorities follow the spec's
values, since getting them wrong silently selects the wrong rule — a much
harder failure to debug than a crash.

**The parser separates namespace nodes from attributes.** Go's
`encoding/xml` resolves prefixes into `Name.Space` and then discards both the
prefix and the `xmlns` declarations. XSLT needs both: namespace nodes are
addressable on the namespace axis, and a literal result element must serialise
with the prefix its author wrote. So `encoding/xml` is used as a tokeniser only
and the tree is built here.

**Instructions write to a builder, not to a string.** A sequence constructor
produces a stream of nodes and atomic values: `xsl:element` opens a node that
later instructions add children to, an attribute added after children is an
error, and a variable with content becomes a navigable temporary tree.
Returning strings from each instruction would make all of that impossible.

That one decision is why `xsl:result-document` needed no architectural change:
every instruction already took a builder as a parameter, so a secondary
document is just a second builder rather than a rewrite of the output path.

**Unset must not look like a real answer.** Source positions are stored on the
node as the byte offset *plus one*, so the zero value reads as "unknown".
Nodes are built with plain struct literals in two dozen places across the
transform layer; had the raw offset been stored, every constructed node would
have claimed to start at line 1, and a construction site added later would
have inherited the bug invisibly. The same reasoning runs through the API:
`gx:line-number()` returns the empty sequence rather than 0, because a report
naming line 0 for every failure is worse than one naming none.

## Concurrency and memory

A compiled `Stylesheet` is immutable and safe to share: compile once, transform
from many goroutines. The tests exercise that under `-race` rather than
asserting it — including a *shared parsed source tree*, which is the stronger
claim, since it means evaluation never writes back into the document.

That last claim was false until an audit found the counter-example, and it is
worth recording how it hid. `xsl:sequence` handed source nodes straight to the
output builder, which calls `AppendChild` on them — and `AppendChild` rewrites
the node's parent and tree pointers, while `Finalize` renumbers its document
order. So reading from the document mutated it. The visible symptom was not a
race at all:

```xslt
<xsl:variable name="v"><xsl:sequence select="/r/b"/></xsl:variable>
```

Against `<r><a/><b/><c/></r>`, a later `string-join(/r/*/name(), ',')` returned
`a,c,b`. The variable is never used; evaluating it was enough to reorder the
input. `xsl:copy-of` was correct all along because it deep-copies. The guard now
lives in `appendNode` rather than at the two call sites, so a node that already
belongs to a tree is copied whatever reaches it.

The concurrency tests had covered key indexes, `xsl:message` and the regex cache
— everything that looked like shared state — and missed this because it did not
look like state at all.

A loaded `xsd.Schema` is the same kind of value: immutable once assembled, safe
to validate from many goroutines. Its one piece of lazily-built state, the
content-model cache, is a `sync.Map` for that reason. The tests exercise the
claim under `-race` rather than asserting it — validating cold schemas from
sixteen goroutines released together, so the cache is written under contention
rather than read from a warm one, and validating documents that carry the same
`xs:ID` values concurrently, since identity and ID/IDREF tables are the state
most likely to have been hung off the schema by mistake. Loading is covered
too, including a schema that declares over the built-in XML-namespace
attributes while others load beside it.

Per-transform state lives on a runtime struct that is copied on every focus
change. Anything that must survive those copies — `xsl:message` output,
`xsl:result-document` results — is held through a pointer, and there are tests
that fail if a transform ever sees another's.

The one piece of process-wide mutable state is the compiled-regex cache, and
**it was an unbounded leak**. The original reasoning was that patterns come
from stylesheets and so form a fixed set; that is wrong, because
`matches($s, $node/@pattern)` compiles a pattern taken from document data. A
long-running validator retained one compiled regexp per distinct pattern it had
ever seen — 17.6 MB after 20,000 patterns, still climbing. It is now bounded at
1024 entries and clears wholesale when full, which measures at 0.6 MB for the
same load and stays flat at five times it.

Clearing rather than evicting one entry at a time is deliberate: a true LRU
needs a lock on every *read*, which costs more than it saves when the working
set is the handful of patterns a stylesheet actually contains. Correctness does
not depend on a hit — every entry is reproducible from its key — and
[`TestRegexCacheStaysCorrectWhenCleared`](xpath/memory_test.go) pins that.

`FileResolver`'s document cache exists because `fn:doc` is defined to return
the *same* node for the same URI within one execution, so `doc('x') is doc('x')`
requires it. It is mutex-guarded, and now capped at 256 documents. It had been
unbounded on the reasoning that the directories the caller opened with
`-allow-dir` bound it in practice — true for the size of the *set*, but the
process holds every document it has ever parsed for its whole lifetime, and a
stylesheet chooses which ones to fetch. Reaching the cap costs one reparse.

Results never pin their source. `xsl:copy-of` deep-copies, so holding a
`Result` from a 20,000-element document costs a few hundred KB rather than the
document.

## Optimisation

Compilation has an optimisation stage between the parser and the runtime.
Everything it does is measured below; nothing is included because it seemed
likely to help.

**Constant folding.** A closed sub-expression is evaluated once at compile time
and replaced with a literal, so `(1 + 2) * 3 - 4 idiv 2` costs one literal load
per node instead of five operations — **13.8 ns** for the whole expression.
Two conditions guard every rewrite:

* The expression must be *closed*: no variable, no context item, no function
  that reads the dynamic context. `1 + 2` folds; `position() + 1` does not.
* The rewrite must preserve **errors** as well as values. `1 idiv 0` is
  deliberately not folded — refusing a stylesheet at compile time for an error
  in a branch that is never taken would be wrong.

Foldable functions are an allowlist, not a denylist. A user-defined function is
never folded even if it happens to be pure, because the library it resolves
through is supplied by the caller.

**Aggregates over a range are arithmetic.** An integer range is determined by
its bounds, so nothing is built to answer a question about it: `count` is
`hi - lo + 1`, `sum` is the arithmetic series, `min`/`max` are the bounds.
`sum(1 to 10000000)` returns `50000005000000` in **6 MB** rather than
materialising ten million values at **1.9 GB**. The series is summed in
`big.Int` because `n(first+last)` overflows int64 long before the bounds do —
`sum(1 to 5000000000000)` is exact here, and Saxon refuses the range outright
(its sequences cap at int32).

The recognition is deliberately narrow: only a bare `to` directly under the
aggregate. A predicate, a `for`, or a comma sequence changes which items
survive, so those evaluate normally — `sum((1 to 10)[. mod 2 = 0])` is 30, not
55.

**Compiled regexes are cached, bounded.** Schematron applies the same handful
of patterns to every node, and `regexp.Compile` dominates otherwise. The cache
was originally unbounded on the reasoning that patterns come from stylesheets —
which is wrong, because `matches($s, $node/@pattern)` compiles a pattern from
*document data*. That leaked 17.6 MB per 20,000 distinct patterns and never
shrank. It is now capped at 1024 entries and clears wholesale when full, which
measures 0.6 MB for the same load.

**Templates are pre-sorted, not indexed.** Selection scans the template list
and stops at the first match, because the list is ordered by (import
precedence, priority, declaration order) at compile time. A per-element-name
index would make dispatch cheaper in principle — but profiling a 67-template
renderer put `findTemplateFrom` nowhere near the top, so it is not implemented.
See *Where it is slow* below.

### What was tried and reverted

`Context.WithFocus` is the single largest allocation site in the engine —
around a quarter of everything a render allocates, because a path step runs
once per node. Reusing one focus context across a step loop looked like an
obvious win. Measured: **4,963,596 → 4,964,187 bytes per render**. Nothing. Go's
escape analysis was already handling it, and the aliasing risk that reuse
introduces bought exactly zero, so it was reverted. The comment in
`xpath/context.go` records the numbers so nobody repeats the experiment.

## Benchmarks

Apple M3 Pro, Go 1.26, `-benchtime=200x`, median of five runs. These are the
two production workloads, not microbenchmarks. Wall-clock figures vary about
±15% run to run on a laptop, so they are rounded; the allocation counts are
stable to three significant figures and are the more useful number.

| benchmark | time | allocated | allocs |
|---|---:|---:|---:|
| `UBLRender` — 100 KB stylesheet, 67 match templates → HTML | ~2.6 ms | 4.96 MB | 67,850 |
| `OmanValidate` — 87-template Schematron → SVRL | ~1.05 ms | 1.66 MB | 21,703 |
| `CompileUBL` — one-time stylesheet compilation | ~0.90 ms | 1.36 MB | 17,578 |
| `ParseInvoice` — XML parse, 61 MB/s | ~0.15 ms | 0.16 MB | 2,404 |

`CompileUBL` is the cost `Transform` amortises: compile once, transform many.
Reproduce with `go test ./xslt/ -bench=. -benchtime=200x` (requires a
`testdata/` corpus — see *How this was tested*).

`UBLRender` is a 100 KB, 67-template stylesheet rendering an invoice to HTML.
`OmanValidate` is an 87-template Schematron rule set producing an SVRL report.
`CompileUBL` is the one-time cost that `Transform` then amortises across every
document.

### Against Saxon-HE 12.4

Validating the same document repeatedly, one process handling the whole batch:

| documents | go-xml | Saxon-HE 12.4 |
|---:|---:|---:|
| 4 | **0.04 s** | 0.81 s |
| 100 | **0.16 s** | 0.81 s |
| 1000 | **1.38 s** | 2.01 s |

Read this carefully rather than as a win. Saxon's cost is almost entirely
**fixed**: 4 documents and 100 documents both take 0.81 s, because that is JVM
startup plus stylesheet compilation. Its *marginal* cost is about 1.2 ms per
document against go-xml's 1.3 ms — so on a hot loop the JIT is slightly ahead,
and at a large enough batch Saxon would catch up and pass.

Where go-xml is unambiguously better is anything that pays startup often: a CLI
invocation (0.26 s versus 0.67 s cold), a short-lived container, a per-request
validator. Where it is not better is a long-running process transforming
millions of documents. Neither engine is "fast"; they are fast at different
shapes of work.

## Security defaults

Every remote-reference mechanism is off unless you turn it on.

* **`DOCTYPE` is rejected** by the parser unless `ParseOptions.AllowDOCTYPE` is
  set. It is the entry point for both XXE and entity-expansion blowup.
* **`fn:doc` and `fn:document` fail closed** *as a library*. With no
  `DocumentResolver` configured, every URI is refused. The CLI is not
  fail-closed in the same sense: it always configures a resolver rooted at the
  stylesheet's own directory, because a rule set that includes a sibling module
  is the normal case. **That root is shared with `doc()`**, so a stylesheet can
  read any file beside it, not only include one — keep stylesheets in a
  directory of their own if that matters. Everything outside stays refused. `xslt.FileResolver` confines reads to
  directories you name, resolving symlinks *before* the containment check, and
  refuses every non-`file` scheme. There is no network option.
* **`xsl:include` and `xsl:import` fail closed** the same way, via
  `CompileOptions.Resolver`.
* **Nesting and recursion are bounded** — parse depth, XPath recursion and
  template recursion each have a limit that produces an error rather than a
  stack overflow.
* **Memory is bounded too.** Depth bounds the stack and `-timeout` bounds the
  clock, but neither bounds allocation: `sum(1 to 9999999)` is one shallow,
  fast expression that materialised nine million values and peaked at **1.8 GB
  of resident memory**. A per-evaluation item budget now refuses it. The budget
  resets for each expression, so a stylesheet evaluating a legitimate range
  once per node of a large document is unaffected — that distinction is the
  whole point, and it is tested.
* **Cancellation works.** `Transform` takes a `context.Context` and checks it
  at every loop boundary, so a pathological stylesheet is interruptible.
* **`xsl:result-document` never writes to disk.** Secondary documents are
  returned to the caller on `Result.Secondary`; a transform that can create
  files anywhere the process can write is a decision the caller should make.
  The CLI opts in with `-result-dir` and refuses any `href` resolving outside
  it, symlinks included.

The CLI mirrors these: `-allow-dir` opens document access, `-allow-doctype`
opens the parser, and `-timeout` bounds the transform. All three are off or
conservative by default.

**Treat a stylesheet as code, not as data.** It can read any file inside the
permitted roots, write anywhere under `-result-dir`, and spend the whole
`-timeout` doing it. The boundaries are enforced and tested — traversal,
symlinks into and out of the roots, absolute paths, and non-`file` schemes are
all refused, and `TestResolverContainmentAttacks` probes them the way an
attacker would — but inside those boundaries a stylesheet is a program you are
choosing to run.

One consequence worth stating plainly: engine-internal state is bound to
variables in a private namespace, and a stylesheet that names that namespace
can reach those values. Doing so used to panic the process, which for an
embedding server is a denial of service written in stylesheet text; every such
path now answers or errors instead. `TestInternalStateIsNotReachableAsAPanic`
covers twenty-two of them.

## What is implemented

Coverage was established by auditing against the spec's own inventories rather
than by recollection, across all three layers: the 7 XDM node kinds and the 22
instantiable XML Schema primitive types, all 58 XPath 2.0 grammar productions,
all 49 XSLT 2.0 elements with their behavioural attributes, and the 113
required `fn:` functions — checked at all 153 of their name/arity signatures,
since a call resolves against both and a function can be present at one arity
and missing at another.

**XDM.** All seven node kinds; the numeric tower with exact `xs:decimal`; the
date, time and duration types; and the five Gregorian types (`gYear`,
`gYearMonth`, `gMonth`, `gMonthDay`, `gDay`), which support equality but not
ordering — without a year, "is `--01-15` before `--02-01`" has no answer that
holds for every year. `xs:NOTATION` is the one primitive not present: it cannot
be instantiated directly and exists only as a DTD attribute type.

**XPath 2.0.** All thirteen axes; every node and kind test; the full precedence
ladder; value, general and node comparisons; `for`, `if`, `some`/`every`;
`instance of`, `cast`, `castable`, `treat`; sequence operators; and the
complete required function library — strings, sequences, numerics, regular
expressions, node properties, QName accessors, URI handling, dates, durations,
`format-dateTime`, `deep-equal`, plus the `xs:` constructors.

Functions are registered by name **and arity**, since the two are what a call
resolves against. That distinction matters for the ten functions carrying an
optional trailing collation argument — `compare`, `contains`, `starts-with`,
`ends-with`, `substring-before`, `substring-after`, `index-of`,
`distinct-values`, `min` and `max`. All accept it; the codepoint collation is
honoured and any other is refused rather than silently applied as ASCII order.
`xs:QName` is resolved during parsing rather than at run time, because a QName
value carries the namespace URI and the prefix binding exists only in the
static context — which is also why the spec restricts its argument to a string
literal.

**XSLT 2.0.** Every element: `apply-templates`
(with modes and the built-in rules), `apply-imports`, `next-match`,
`call-template`, `for-each`, `for-each-group` (all four grouping modes), `if`,
`choose`, `variable`, `param` with tunnel parameters, `element`, `attribute`,
`attribute-set`, `namespace`, `namespace-alias`, `comment`,
`processing-instruction`, `copy`, `copy-of`, `sequence`, `value-of`, `text`,
`sort`, `perform-sort`, `message`, `analyze-string`, `key`, `function`,
`include`, `import`, `strip-space`, `decimal-format`, `character-map`,
`number` (all three levels — `single`, `multiple` and `any` — with `count` and
`from`), `output` (the `xml`, `html` and `text` methods, named as well as
unnamed), `result-document`, `as` type declarations, attribute value templates,
and the simplified literal-result-element stylesheet form.

**Collations.** Two are implemented: codepoint, and the ASCII
case-insensitive collation the spec defines, which needs no locale data. Both
are *applied* rather than merely validated — an `xsl:sort/@collation` that was
accepted and then sorted by codepoint anyway would be the silent-wrong-answer
this engine exists to avoid. Relative URIs such as `collation/codepoint`
resolve, because stylesheets write them that way. Anything else is refused.

**Sorting and Unicode.** `xsl:sort` supports `@case-order` and `@lang`:
Swedish orders "ä" after "z" where German orders it next to "a", and codepoint
order gets both wrong. `fn:normalize-unicode` implements all four standard
forms. Both use `golang.org/x/text`. A `@lang` naming a language with no
collation data is refused rather than quietly falling back to codepoint order,
and `@collation` accepts only the codepoint URI — a language-sensitive
collation is spelled with `@lang`.

## Where it fails

Three separate things are worth distinguishing, because they fail for
different reasons.

### 1. Constructs that are refused outright

Each errors rather than doing something plausible. An XSLT processor that
accepts an instruction and quietly ignores it is the worst failure mode,
because the output looks fine and is wrong. See *What is not* below for the
full list — `fn:unparsed-text`, a backreference to a variable-width group, and
`fn:collection` or `fn:doc` when the caller has configured no resolver.

**Malformed stylesheets are refused too**, which is the same principle applied
one level up. An unknown element in the `xsl:` namespace is `XTSE0010` rather
than being skipped, so `xsl:tempalte` is a compile error instead of a silently
dropped template. So is an element in the wrong parent: `xsl:when` outside
`xsl:choose`, `xsl:sort` outside a sortable instruction, `xsl:with-param`
outside a call. Both used to be accepted and dropped, which meant a typo
produced an empty result and no diagnostic — found while writing
[docs/server.md](docs/server.md), by trying to demonstrate that a bad
stylesheet fails to compile and discovering it did not.

### 2. Where the QT3 suite still disagrees

**1 of 15,181 in-scope cases fails (0.01%).**

`fn-matches-51`:
`fn:matches("ab()cd()ef()gh", "^(ab)([()]*)(cd)([)(]*)ef\4gh$")`. It names
`([)(]*)`, a group whose width can vary, *and* places the backreference in the
middle of the pattern. Both are refused deliberately — see *The hard floor*
below.

Two things about how that number was reached are worth more than the number
itself.

**The denominator grew by 461.** Source paths in a test-set environment are
relative to the test-set file, not to the suite root; resolving them against
the root silently skipped every case whose environment named `../docs/…`. They
were never counted as failures — they were never counted at all.

**Five of the last seventeen failures were the harness, not the engine.**
Assertions required a literal boolean where the spec asks for an effective
boolean value; the result serialiser dropped in-scope namespace declarations;
an `assert-xml` whose expected value lived in a file compared against the empty
string. A conformance number is only as honest as the harness producing it,
which is why those are written down rather than quietly absorbed.

### 2a. Where the XSD suite still disagrees

Two figures, and the second is the one that matters.

| | schema-validity | instance |
|---|---|---|
| XSD 1.0 | 14,204 / 14,405 (98.60%) | 24,953 / 25,003 (99.80%) |
| XSD 1.1 | 15,048 / 15,365 (97.94%) | 26,155 / 26,209 (99.79%) |

**Earlier revisions of this file reported 99.56% and "XSD 1.1: 100%". Both were
measured wrongly, and the correction is large enough to state outright.**

*The schema-validity tests were never scored.* A test group whose schema the
suite marks invalid by design was counted as a "skip" on the grounds that
checking it is Schema Component Constraint territory rather than instance
validation. But that is exactly what those groups test: the schema is *meant*
to be rejected, and loading it without complaint is a failure. Scoring them
turned roughly 2,200 silent passes in 1.0 into what they always were. Most
have since been closed — the Part 2 facet constraints, the schema-for-schemas
shape check, the XSD regex grammar, and Particle Valid (Restriction) were the
four largest — but the figure is quoted here because the earlier one was not
measuring them at all.

*The 1.1 run scored about six per cent of its tests.* Per `common/xsts.xsd` the
`version` attribute is a **list of tokens**, not a single string — OR-joined on
`testSet`/`testGroup`/`schemaTest`/`instanceTest`, AND-joined on `expected`,
and **absent means the test applies to every processor**. The driver compared
it for equality with `"1.1"`, so the 1.1 run saw only the explicitly-marked 1.1
groups: 888 schema tests rather than the 15,365 that apply. The old "100%" was
a real result over an unrepresentative sixteenth of the suite.

The instance figures are now close to each other and to where 1.0 stood before,
which is the expected shape: most groups carry no version attribute and so are
scored identically by both runs.

The remaining gap is still dominated by **schema false-accepts** — invalid
schemas loaded without complaint, i.e. Schema Component Constraints not yet
checked — now concentrated in attribute declarations, wildcards, element
declarations and identity constraints. False *rejects*, where a valid schema or
instance is refused, number in the low tens and are tracked separately because
they are the more serious kind: refusing valid input breaks a caller, while
accepting invalid input only fails to catch their mistake.

Two notes on the denominator. A handful of schemas still fail to load for
reasons that are not bugs: nine are XML 1.1 documents, which this parser does
not read, and two need a DOCTYPE, refused by default because it enables XXE.

And some of the suite is disputed. `status="queried"` on a test means the W3C
has challenged the expected result, usually with a bugzilla reference, so those
disagreements are a ceiling rather than work outstanding. Twenty-seven of the
1.0 disagreements are marked that way:

| bug | cases | what |
|---|---:|---|
| 4113 | 19 | regex `\p{...}` general-category tests |
| 6901 | 2 | `gMonth002_2061`, `gMonth004_2063` |
| 4952 | 1 | `particlesW006` |
| 4680 | 1 | `elemZ027_c` |
| 4126 | 1 | `anyURI_a004_1339` |

One test *set* is mislabelled rather than one test. `ibmMeta/wildcard.testSet`
carries `version="1.0"`, which excludes it from the 1.1 run — but every one of
its seventeen groups cites the XSD **1.1** specification in its own
`documentationReference`, and four use `notQName`, a 1.1-only wildcard form,
while expecting the schema to be valid. Refusing `notQName` under 1.0 is
correct, and those four schemas do load under `Version11`, so the four
disagreements are the label's fault rather than the validator's.

Bug 4113 is the instructive one. The suite was written against **Unicode 3.1**,
and characters have moved between general categories since. `reJ11` asserts that
`\p{Lu}*` rejects U+1D7A8, which *is* an uppercase letter in modern Unicode.
Passing that test would mean shipping a frozen Unicode 3.1 table and being wrong
about every text written in the twenty years since — so these are left
disagreeing on purpose.

### Is 100% reachable?

For XPath, effectively yes: one case remains, and it is refused on purpose.

The route there is the useful part, because most of it was not what the
failures looked like. Of the seventeen that remained after the ordinary bugs,
**five were the test harness rather than the engine**, two needed DTD attribute
defaulting, two needed a document to be retrievable under the URI
`fn:document-uri` reports for it, one was a lexical form that disagreed with
its own value, and eleven were backreferences that turned out to be decidable.

Two of those are worth stating on their own.

`for $var in "ABC" return $var castable as xs:QName` is false while the same
expression with a literal is true, because a QName's namespace comes from the
static context and only a statically-known operand has one. That is a static
property of the operand, so it has to be decided where the expression is
visible — not in the cast, which sees a value and cannot tell a literal from a
variable holding one.

`xs:decimal` kept more precision than it printed. A literal with 360
fractional digits rendered as `0` while comparing unequal to zero, so
`0.000…1 eq 0` was false and `string(0.000…1)` was `"0"`. Both cannot be right;
the value is the one that must not move.

Three themes recur through everything that was fixed, because each is the same
mistake made repeatedly:

**A Go standard-library parser is not a schema validator.** `strconv.Atoi`
accepts a leading sign, so every fixed-width date and time field did too and
`"11:+1:11"` parsed as `11:01:11`. `big.Rat.SetString` reads Go's numeric
syntax, so `xs:unsignedLong("0x0")` was zero. `url.Parse` treats `"b.html"` as
a URI with an empty scheme, so it served as a base for `fn:resolve-uri`.
`strings.Fields` splits on every Unicode space, so `xs:token()` swallowed a
non-breaking space. `strings.ToUpper` applies simple case mapping, so
`upper-case("ß")` was not `"SS"`. Each turned a malformed or misread input into
a plausible value — worse than an error, because nothing downstream can tell.

**An out-of-range value is not a large value.** `"P768614336404564651Y"` parsed
as a *negative* duration, because the year count multiplied by twelve wrapped
an int64. Three separate panics — a parser that skipped a fixed token count
past the end of its input, a NaN produced by adding opposite infinities that
survived every clamp because comparisons against NaN are false, and an infinite
divisor that looked like zero because an infinity has no rational form — were
all the same failure to bound an input. The converse bit just as often: a
400-digit `xs:integer` literal is an ordinary arbitrary-precision value, a
range from 10²¹ to 10²¹+3 is four items, and a repeat count of 2147483647 is a
pattern that matches nothing rather than a malformed one. Refusing any of them
because a machine word could not hold the number was equally wrong.

A later audit found five more of the same shape — a `big.Int` or `big.Rat`
narrowed with `.Int64()` or `int(...)` at a site whose *sibling path already
range-checked*. `xs:yearMonthDuration("P768614336404564650Y") * 4` came back
negative; `fn:remove((1,2,3), 2^64+2)` deleted the second item because the
position truncated to `2`; and a range spanning the whole of int64 wrapped its
count to exactly zero, so `avg()` divided by it and panicked. None of these is
reachable from the conformance suite, which is why they survived it.

**A parameter declaration is a type, not a suggestion.** `fn:string-join(1 to
5, "")` returned `"12345"` because the implementation called `String()` on each
item rather than applying the conversion rule for the `xs:string*` it declares.
The same slip appeared in `fn:codepoints-to-string`, `fn:translate`,
`fn:normalize-unicode`, `fn:remove`, `fn:doc-available`, `fn:error` and every
collation argument — an empty sequence passed to a parameter declared
`xs:string` is `XPTY0004`, not a default.

An earlier version of this table claimed ~210 of the failures needed a static
typing pass. That was wrong, and worth recording: when the cases were actually
read rather than inferred from their error codes, all but one turned out to be
ordinary missing validation.

The one remaining case is **regex**, and the reasoning is worth setting out
because the obvious fix is wrong.

RE2 has no backreferences by design — the trade that buys linear-time matching
and the reason a pathological pattern cannot hang this engine. The natural
workaround is to capture the groups and compare the text explicitly. That fails
silently: RE2 returns a *single* submatch assignment, the greedy one, so
`(a*)\1` against `"aa"` reports the group as `"aa"`, leaves nothing for the
backreference, and answers **false** where the truth is true — the split is
`"a"` + `"a"`. The information needed was discarded before the comparison ran.

But when every group a backreference names has a **fixed** width, the greedy
assignment is the *only* assignment. There is nothing to enumerate, so
comparison is exact rather than approximate, and it runs in RE2's linear time:
measured on `([a-z])\1*`, 4,000 characters in 53 µs and 64,000 in 567 µs.

So the split is by what can be **decided**, not by what a caller asked for.
`(a)\1` and `([md])[aeiou]\1` are resolved; `(a*)\1` raises `FORX0002`. There
is deliberately no option to switch this on — an engine that answers correctly
or says it cannot is safe to have on always, where one that guesses is not safe
at any setting. No backtracking engine was added and the linear-time guarantee
is intact.

`fn-matches-51` is the case that stays: it names a variable-width group *and*
puts the backreference mid-pattern, which would need the comparison to feed
back into the automaton.

Everything else about the XML Schema regex flavour *is* implemented, and most
of it had to be, because RE2 silently disagrees rather than refusing: XML
Schema's escape set is closed (RE2 reads `\0` as a NUL byte), `.` excludes both
newline characters where RE2's excludes only `\n`, the `i` flag must not reach
inside `\p{Lu}`, the `x` flag strips whitespace *before* escapes are read, and
`\p{IsBasicLatin}` names a block RE2 has never heard of.

### 3. Where it is slow

Profiling the UBL renderer: the whole transform is ~12% of samples and GC is
~40%. The engine is **allocation-bound, not algorithm-bound**. The largest
single site is `Context.WithFocus`, at roughly a quarter of all allocation,
because a path step allocates a focus context per node.

The unimplemented items that would actually move this are structural, not
incremental:

* **Lazy sequence types.** `Sequence` is a flat `[]Item` that three packages
  index and range over directly (~100 call sites). Making it an interface with
  `ArraySeq` / `IntegerRange` / `FilteredSeq` implementations is the textbook
  answer, and it is why `1 to 10000000` needs the arithmetic special-case above
  rather than being free.
* **Receiver-based output.** Instructions write into an `outputBuilder` that
  materialises a tree (46 call sites). An event receiver — `StartElement`,
  `Attribute`, `Text`, `EndElement` — would let serialisation stream instead of
  building the whole result first.
* **Streaming.** Depends on the receiver work. Some expressions genuinely
  cannot stream (`last()`, `preceding::`, backward navigation), so this is a
  per-expression capability, not a mode.

None of these is reachable by incremental change, and the profile says the
payoff is smaller than intuition suggests — which is why they are documented
here rather than half-done in the code.

## What is not

Two things are unsupported, neither of them an XSLT element. **Each one errors
rather than doing something plausible** — an XSLT processor that accepts an
instruction and quietly ignores it is the worst possible failure mode, because
the output looks fine and is wrong.

* **`fn:unparsed-text`** — reading arbitrary files named by a stylesheet is
  disabled by design, unconditionally.
* **`fn:collection` and `fn:doc` without a resolver** — both fail closed rather
  than returning nothing, so a misconfiguration is reported instead of looking
  like a document set that happened to be empty. Supply a
  `CollectionResolver` or `DocumentResolver` to enable them; they are separate
  switches, so enabling `fn:doc` for a known code list does not also let a
  stylesheet enumerate a directory.
* **A backreference to a variable-width group** (`(a*)\1`) — RE2 returns one
  submatch assignment, and for a group whose width can vary that assignment may
  be the wrong split, so the answer is `FORX0002` rather than a guess. A
  fixed-width backreference (`(a)\1`) *is* resolved, exactly and in linear
  time. Character-class
  subtraction (`[a-z-[aeiou]]`) *is* implemented, by expanding both sides into
  codepoint ranges and taking the difference; only subtraction from a shorthand
  class (`[\i-[:]]`) is refused, because that needs the Unicode tables defining
  the shorthand. A hyphen between two classes (`[a-z]-[a-z]`, as in a UUID
  pattern) is not subtraction and is unaffected.

One approximation is documented rather than hidden: `fn:id`/`fn:idref` use
`xml:id` and conventional `id` attributes, because without a DTD or schema
nothing declares which attributes are of type ID. A stylesheet relying on
DTD-declared IDs gets an empty result rather than a wrong one.

## How this was tested

Five methods, each catching a class the others miss. That is the point: no
single one of them was sufficient, and each was added because the previous set
had let something through.

| method | what it catches | what it misses |
|---|---|---|
| **Unit tests** (671) | places where a plausible implementation is quietly wrong | anything nobody thought to write a test for |
| **Spec inventories** | features absent entirely | features present but behaving wrongly |
| **Saxon differential** | subtle behavioural divergence on real stylesheets | constructs the corpora do not use |
| **W3C QT3 suite** | systematic conformance across 15,181 cases | XSLT (it is an XPath suite) |
| **W3C xsdtests suite** | systematic XSD conformance across 25,003 instance and 14,405 schema-validity tests | schemas nobody writes by hand |
| **Production schema sets** | what large modular schemas do that suites do not | anything those industries happen not to use |

**The production schema sets are the newest of the five and found the most per
hour.** Pointing the validator at UBL 2.1 turned up two bugs the entire W3C
suite had not: deduplication keyed on the `schemaLocation` as written rather
than the resolved path, so a diamond in the import graph read one file twice
and reported every global in it as a duplicate; and attribute inheritance
scheduled on a fixed number of passes, which no chain depth is guaranteed to
fit. Between them they meant *all 65* UBL main-document schemas failed to load,
with 1,758 errors apiece. Neither is exotic — a diamond is the normal shape of
a modular schema set, and UBL's `EndpointIDType` is an empty
`<xs:extension base="udt:IdentifierType"/>`.

| schema set | schemas | result |
|---|---:|---|
| **UBL 2.1** (OASIS) | 65 | all load; 8 real invoice and credit-note instances validate clean |
| **UN/CEFACT CII** D16B (EN 16931) | 2 | both load; 15 instances validate clean |
| **Factur-X / ZUGFeRD** (all profiles) | 21 | all load; samples validate clean against their own profile |
| **Peppol BIS Billing 3.0** | — | 24 UBL instances validate clean |

The three Mustang fixtures this rejects are genuinely invalid — two carry a
second `URIID` where the schema allows one, and the third is named
`not_validating_…` in its own corpus.

**Unit tests concentrate on where being plausible is not enough** rather than
on breadth: exact decimal arithmetic, the canonical form of doubles at the
exponent boundary, `//title[1]` versus `(//title)[1]`, untypedAtomic comparing
as strings unless cast, reverse-axis position numbering, `=` not being the
negation of `!=` over sequences, template priority values, whitespace stripping
under `xml:space`, and the security defaults.

**Spec inventories are machine-checked, not recalled.** Two of the 296 are
coverage guards rather than behaviour tests: one parses every XPath 2.0 grammar
production, the other looks up every required function by name *and arity* in
the library `Builtins()` actually returns.

That second guard exists because an earlier audit was done by grepping the
source for `register("...")` literals and reported 31 functions missing that
were all present — they are registered through helper wrappers whose names the
grep never saw. A grep-based inventory can be wrong in *both* directions;
asking the library is the only form of the check that cannot flatter the
result. Checking by arity as well as by name is what later exposed ten
collation overloads that did not resolve at all.

**Verified against Saxon on two independent real-world corpora**, with golden
files generated by **Saxon-HE 12.4**, the reference XSLT 2.0 implementation.

*A production rendering stylesheet.* A 100KB UBL Invoice/CreditNote HTML
renderer (67 match templates) applied to the OpenPEPPOL BIS 3.0 examples —
credit notes and negative corrections included — producing output
byte-identical to Saxon's.

*A production validation rule set.* The Oman PINT e-invoicing Schematron rules,
including the 550KB jurisdiction-aligned set, applied to the official example
documents. The SVRL reports exactly the assertions Saxon reports, at exactly
the same locations. This is the closer test of the two: a validator that
disagrees with the reference implementation rejects valid invoices.

> **These corpora are not in this repository.** They are third-party production
> stylesheets and rule sets, so they are not redistributed; `testdata/` is
> git-ignored. A fresh clone runs 293 tests and skips 4, cleanly and silently —
> the differential tests detect the absent directory rather than failing.
>
> This matters when reading the numbers below: **the conformance figure is
> reproducible from a clone, the Saxon comparison is not.** If you are putting
> this in front of a rule set that matters, run the differential yourself
> against your own corpus. [docs/recipes.md](docs/recipes.md) shows the shape
> of the harness.

### Reporting the line a failure is on

SVRL identifies a failing element by XPath, which is exact but not something a
person navigates by — and it is ambiguous in the case that matters most, where
two siblings share a path and only one of them failed.

Parsing with `TrackPositions` records where each element starts, and a
stylesheet reads it through two extension functions in the namespace
`https://github.com/knroy/go-xml`:

```xslt
<xsl:if test="gx:line-number()">
  <xsl:attribute name="line"><xsl:value-of select="gx:line-number()"/></xsl:attribute>
  <xsl:attribute name="column"><xsl:value-of select="gx:column-number()"/></xsl:attribute>
</xsl:if>
```

```
$ go-xml -xsl rules.xsl -track-positions invoice.xml
<svrl:failed-assert id="PRICE-01" location="/order/group/item" line="4" column="5">
<svrl:failed-assert id="PRICE-01" location="/order/group/item" line="5" column="5">
```

Both failures carry the same `location`; the line is what separates them.

They return the **empty sequence** when the position is unknown — the document
was parsed without `TrackPositions`, or the node was built by the transform
rather than read from a file. That is why the example tests before emitting:
a report claiming line 0, or line 1, for every failure would be worse than one
carrying no line at all. Tracking costs about 10% more memory and no extra
parse time, and is opt-in because it buys nothing for a caller that never asks.

Between them these found seven real bugs, all since fixed:

* `fn:current()` returned the *predicate's* context item rather than the node
  the enclosing instruction was processing. Schematron's location-path
  generator counts preceding siblings with
  `[local-name() = local-name(current())]`, so the test was trivially true and
  every sibling counted — a document with two `cac:TaxTotal` elements reported
  `TaxTotal[18]`, and the mis-numbering produced a **false failed-assert**.
* `xsl:number level="multiple"` was unimplemented, so the rule set would not
  compile at all.
* A path rooted at a variable (`$codelists/cl[@id=$x]`) demanded a context
  item. It is legal inside `xsl:function`, where there deliberately is none.
* `as` type declarations were parsed and then ignored, so a parameter declared
  `as="xs:decimal?"` received an untypedAtomic and its arithmetic silently
  became floating point.
* `fn:format-number` was missing entirely, along with `xsl:decimal-format`.
* `U+00A0` was stripped as whitespace. Go's `strings.TrimSpace` uses
  `unicode.IsSpace`, which matches it; XML whitespace is only space, tab, CR
  and LF. A deliberate `&nbsp;` was silently disappearing.
* `xsl:message` output was collected into a slice held by value in a struct
  that gets copied on every focus change, so messages emitted inside a
  template never reached the caller.

Successive audits against the spec's own inventories found what the corpora
could not, because a corpus only exercises the constructs its stylesheets
happen to use:

| Audit | Found |
|---|---|
| XSLT elements | 9 missing, including `xsl:attribute-set` and `xsl:namespace-alias`, which were being **silently ignored** |
| Function library | 24 missing |
| XDM types | the 5 Gregorian types absent |
| XSLT attributes | `xsl:sort/@case-order` stored and never applied; `@lang` never read |
| Function arities | 10 collation overloads unregistered, so `compare($a,$b,$c)` did not resolve; `xs:QName` absent |
| Deliberate refusals | `xsl:number level="any"`, `xsl:result-document`, `fn:normalize-unicode` and `@lang` collation had been *declared* unsupportable rather than being hard |

Every one of those is now implemented. The last row is the one worth
dwelling on: four features had been written off in comments I had written
myself, and re-examining them found that three needed no architectural change
at all — `xsl:result-document` worked out to be a fresh output builder, because
every instruction already took one as a parameter.

**The corpora are not in this repository.** They are third-party material —
production stylesheets and jurisdiction-specific rule sets — so they are not
redistributed here. The tests that use them skip cleanly when `testdata/` is
absent, which is why a fresh clone builds and runs its full suite with nothing
extra.

To run the differential tests against your own corpus, put the stylesheet, the
source documents, and Saxon's output for each in a top-level `testdata/`
directory named as `<name>.xml` and `<name>.saxon.html`. That layout is what
[`xslt/conformance_test.go`](xslt/conformance_test.go) expects; the Oman
Schematron comparison in [`xslt/oman_test.go`](xslt/oman_test.go) reads
`testdata/oman/` the same way.

**Running the differential cases without the suite.** Every expectation the QT3
run produced is also checked in as a plain Go table in
[`xpath/saxon_diff_test.go`](xpath/saxon_diff_test.go), with the reason each row
exists and Saxon's actual output as the expected value:

```
go test ./xpath/ -run TestSaxon -v
```

That needs no download and no environment variable. The file's header comment
carries the four-line recipe for reproducing any single row by hand against
both engines.

### The W3C QT3 suite

The official W3C test suite for XPath — QT3, also called FOTS — runs against
this engine. It is not vendored: it belongs to the W3C, and `testdata/` is
gitignored for that reason. Clone it and point `GOXSLT_QT3` at the checkout;
without the variable those tests skip, so the ordinary `go test ./...` is
unaffected.

```
$ git clone --depth 1 https://github.com/w3c/qt3tests.git testdata/qt3tests
$ GOXSLT_QT3=$PWD/testdata/qt3tests go test ./qt3/ -v -timeout 1800s
QT3: 31821 cases, 15181 in scope, 16640 skipped
in-scope: 15180 passed, 1 failed (99.99%)
```

Two environment variables make a failure workable:

```
GOXSLT_QT3_VERBOSE=1              # list every failure, with the expression it ran
GOXSLT_QT3_SET=fn-collection      # run only test sets whose name contains this
```

`GOXSLT_QT3_SET` narrows a 31,821-case run to the handful you are working on;
the percentage is then labelled as filtered rather than quoted as the suite
result. Verbose output carries the expression, so a failure is reproducible
without opening the catalog:

```
FAIL fn-collection/collection-006: none of {all-of,error} held
     expr: collection("collection1")
```

The figures above were measured against
[w3c/qt3tests](https://github.com/w3c/qt3tests) at commit `201a6e46`
(2026-05-14), ~78 MB checked out. The suite is updated from time to time, so a
later checkout can move the denominator — see
[docs/known-gaps.md](docs/known-gaps.md) for how a path-resolution fix moved it
by 461 in one step.

**Skips are reported separately and are never counted as passes.** The suite is
FOTS 3.1 and covers XQuery as well as XPath 3.0/3.1; this is an XPath 2.0
processor, so 16,640 cases are out of scope — 15,545 of them requiring XQuery
or a later XPath. Failing a test for a language the engine does not claim to
implement would say nothing about conformance, and counting those as passes is
how a conformance number becomes meaningless.

It found four real bugs on the first run, none of which the two production
corpora had exercised:

* **Two crashes.** `insert-before($seq, (), $x)` and `remove($seq, ())`
  dereferenced the nil returned for an empty position argument and took the
  process down. Both now raise `XPTY0004`, which is what Saxon raises.
* **A hang.** `round-half-to-even(3.567812, 4294967296)` used the precision
  argument directly as an exponent of ten, so `big.Int` began computing a
  number with four billion digits. It consumed twenty minutes and all
  available memory before the run could report anything. Precision is now
  clamped, which is safe because rounding to more places than a value has is
  the identity.
* **`24:00:00` was not normalised.** XML Schema admits it as midnight ending a
  day but defines it to mean `00:00:00` on the next one, so
  `1999-12-31T24:00:00` has to become `2000-01-01T00:00:00`. It was being kept
  as hour 24, which serialised differently from an equal value written the
  ordinary way.
* **`xs:hexBinary` and `xs:base64Binary` did not inter-convert.** Both were
  stored as plain strings, so the cast reinterpreted the lexical form instead
  of re-encoding the octets — `xs:hexBinary(xs:base64Binary('D7c='))` was an
  error rather than `0FB7`.

Working through the failure clusters found nine more, all now fixed:

| Bug | What was wrong |
|---|---|
| Casting table unenforced | The cast functions read the *lexical* form, so `xs:base64Binary("10010101") castable as xs:float` answered true. The source type is now checked against the spec's table. |
| Derived types had no facets | `xs:byte` and `xs:token` were aliases for their primitives, so `128 castable as xs:byte` was true and `xs:token("a&#9;b")` kept its tab. Both range and whitespace facets now apply. |
| `xs:QName` not castable from a string | `CastAtomic` had no QName case at all, so `"ABC" castable as xs:QName` was false. |
| Duration division unreachable | `divideDurations` existed and was correct, but the operator dispatch never called it — `PT2H div PT1H` raised "not defined on two durations". |
| Duration lexical forms too permissive | `big.Rat.SetString` accepts `.5` and `30.`, so `PT.5S` parsed as half a second instead of being rejected. |
| `fn:codepoints-to-string` unvalidated | Any integer became a rune, so codepoint 0 silently produced U+FFFD instead of `FOCH0001`. |
| Multi-line `$` | Go treats the position after a trailing newline as an empty final line; XML Schema does not, so `^$` matched `"abcd\ndefg\n"`. |
| `to` truncated its operands | `1.1 to 3` became `1 to 3` rather than raising `XPTY0004` — a range the author never wrote. |
| `fn:QName` unvalidated | `QName("http://x", "1person")` built a QName whose lexical form cannot be written in any document. |

Together with the four crashes and hangs above, that took the in-scope pass
rate from **92.42% to 98.16%** — measured with the harness's original loose
error check, which accepted any error where a specific code was expected.
Tightening that check later dropped the honest figure to 96.22%, and grinding
the tail down from there brought it to **99.99%**; see below.

**What the one remaining failure is** is covered under *Where it fails* above:
a backreference to a group whose width can vary, which RE2 cannot resolve and
this refuses rather than guesses at.

Every other divergence between RE2 and the XML Schema flavour has been
implemented, and most of them had to be, because RE2 tends to disagree
*silently* rather than refuse. Character-class subtraction is implemented by
expanding both classes into codepoint ranges and taking the difference, but
only where both sides are literal characters and ranges — subtracting from
`\d` or `\p{L}` would need the Unicode tables that define them and is still
refused rather than approximated.

**Known weaknesses of the harness itself**, stated because a conformance number
is only as honest as what it measures:

* **Error codes are compared, not just error-ness.** An expected error is
  satisfied by any error only when the engine produced no code at all;
  otherwise the code must match. This is the single assumption that most
  inflates a conformance number — turning the check on dropped the score from
  98.16% to 91.62% and exposed 961 wrong codes, most of them one systematic
  confusion between "this value is wrong for the type" (`FORG0001`) and "this
  conversion is not defined at all" (`XPTY0004`).
* **`serialization-matches` and `assert-serialization-error` are unimplemented
  and count as failures**, not skips. This *understates* the pass rate.
  `assert-xml`, `assert-permutation` and expression-valued `assert` are
  implemented.
* **Every skip is now a spec or feature dependency**, not a missing file. An
  earlier run reported 503 cases skipped for an unavailable source; those were
  the path-resolution bug above, not the checkout.
* Two assertion conventions are honoured that a naive harness gets wrong: the
  catalog's `code="*"` wildcard means "an error, unspecified" rather than a
  code spelled `*`, and an `xsd-version` dependency selects between 1.0/1.1
  pairs that assert *opposite* results for the same expression
  (`xs:double("+INF")` is an error under 1.0 and `INF` under 1.1). This engine
  implements 1.1. Getting either wrong scores correct behaviour as failure.

**What is still unverified.** There is no XSLT 2.0 equivalent to this run: the
W3C's XSLT suite ([w3c/xslt30-test](https://github.com/w3c/xslt30-test))
targets XSLT 3.0, and its catalog format and result assertions differ enough
that the QT3 harness does not carry over. The XSLT layer's evidence remains the
Saxon differential corpora above. If you are putting this in front of a rule
set that matters, diff its output against Saxon on your own corpus first —
which is exactly how the earlier bugs were found.

### The W3C xsdtests suite

The official W3C test suite for XML Schema. Like QT3 it is not vendored — it
belongs to the W3C and is ~230 MB checked out:

```
git clone --depth 1 https://github.com/w3c/xsdtests.git testdata/xsdtests
```

Unlike QT3 there is **no `go test` integration**: the driver is not in the
repository, because it is a throwaway that walks `suite.xml`, loads each
`schemaTest`, validates each `instanceTest`, and compares against the
`<expected>` validity. The figures quoted in this file were produced by such a
driver built against the tree at HEAD.

If you are reproducing them, three details decide whether your numbers mean
anything, and each one silently inflated an earlier measurement here:

* **`version` is a list of tokens**, not a string. On `testSet`, `testGroup`,
  `schemaTest` and `instanceTest` the tokens are joined by OR — run the test if
  you support any of them. On `<expected>` the connector is AND. Comparing the
  attribute to `"1.0"` scores the multi-token spellings as neither version.
* **`status` lives on `<current>`**, not on `<expected>`. A test marked
  `queried` is one the W3C's own suite disputes; 49 of the 1.0 disagreements
  and 48 of the 1.1 ones are in that category.
* **Invalid-by-design schemas are the point**, not something to skip. Skipping
  them was a measurement bug here that hid roughly 14,000 real tests.

Measured against [w3c/xsdtests](https://github.com/w3c/xsdtests) at commit
`7bc3365c` (2026-04-01):

| | schema-validity | instance |
|---|---|---|
| XSD 1.0 | 14,204 / 14,405 (98.60%) | 24,953 / 25,003 (99.80%) |
| XSD 1.1 | 15,048 / 15,365 (97.94%) | 26,155 / 26,209 (99.79%) |

Every failure and the reason it is open is catalogued in
[docs/known-gaps.md](docs/known-gaps.md).

### Production corpora

The highest-yield fixture, and the only guard against a schema-validity rule
that is stricter than the spec — the conformance suite scores agreement with
W3C labels, so a rule that is merely *too strict* shows up only if the suite
happens to contain a valid schema exercising it. Real schemas catch it.

| corpus | source | result |
|---|---|---|
| UBL 2.1 | [OASIS UBL 2.1](http://docs.oasis-open.org/ubl/os-UBL-2.1/) | 65 of 65 schemas load clean |
| UN/CEFACT CII, EN 16931 | [ConnectingEurope/eInvoicing-EN16931](https://github.com/ConnectingEurope/eInvoicing-EN16931) | 427 of 427 load clean |
| Factur-X / ZUGFeRD (all profiles) | [factur-x](https://github.com/akretion/factur-x) | 21 load; samples validate clean |
| Peppol BIS Billing 3.0 | [OpenPEPPOL](https://docs.peppol.eu/poacc/billing/3.0/) | 24 UBL instances validate clean |

UBL needs `ParseOptions{AllowDOCTYPE: true}`: its dependency graph reaches the
W3C XML Signature schema, which carries a DOCTYPE, and without the flag all 65
fail with a cascade of unresolved `ds:` references from the one refused
include.


## Where this is going

The conformance tail is no longer the interesting work: 16 of the 26 remaining
QT3 failures are structural or harness artifacts, and the other ten are one
case each. Three
larger things are open, in rough order of how much they would change:

* **Receiver-based output.** The runtime builds a result tree and serialises
  it. Emitting events to a receiver instead is what makes streaming possible
  and would cut peak memory on large documents — it is the one change here that
  is architectural rather than additive.
* **Schema-aware atomisation.** `xsl:import-schema` loads a schema and makes
  its type names available, but a validated `<price>10.50</price>` still
  atomises as untyped: the typed value would have to be carried on the node
  rather than its name. Stylesheets relying on type assertions work; ones
  relying on schema-aware arithmetic do not.
* **An XSLT conformance run.** The absence of one is the largest gap in the
  evidence. Adapting the XSLT 3.0 catalog to the subset this engine claims is
  tractable and would replace corpus-shaped confidence with systematic
  coverage.

Contributions are welcome, particularly a differential against a corpus this
has not seen — that is how most of the bugs above were found, and the failure
modes it catches are the ones no suite covers.

## Licence

MIT. See [LICENSE](LICENSE).

The third-party corpora used for differential testing are *not* covered by it
and are not distributed here; see *How this was tested*.
