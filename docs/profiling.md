# Profiling: why go-xml is slow in steady state, and what fixes it

[docs/benchmark.md](benchmark.md) showed go-xml losing to warm JVMs by 2.5× to
85× once compilation is paid. This report profiles every losing workload with
`pprof` (CPU, allocation and heap profiles plus `runtime.MemStats`). It names
the causes with evidence, and measures the most promising fixes on a private
copy of the tree.

**Status:** the analysis was made on `dev` at `4e0496f`. Tier 1 (T1–T10),
T11–T16, T18, T19 and the correctness bugs B1–B4 have since landed; see
[Implementation status](#implementation-status). Profiling ran on the same
machine as the benchmark, with five profiles running at once, so wall times
are noisy (±2×). The claims rest on allocation counts, which are
deterministic, on profile shares, and on A/B runs done back to back. Each
finding is marked **measured** (a prototype was built and timed) or
**estimate**.

## Summary

The JVM's JIT is not the main reason go-xml is slow. Most of the gap comes
from five problems, each of which can be fixed:

1. **Allocation churn, so the garbage collector does most of the work.** The
   XPath evaluation context is a 440-byte struct, copied on every focus change,
   variable binding and function call. On a 10 KB Peppol invoice that is 134 MB
   allocated per transform. The transform code itself is under 5% of CPU
   samples; the rest is the collector and the allocator.
2. **Template dispatch allocates before it tests.** Every pattern tried binds
   two variables (two context copies, two maps) *before* checking the element
   name. Templates are scanned in a straight line, 20–40 patterns per node, so
   almost all of that allocation is for patterns that then fail. This is 60–97%
   of allocated bytes on DocBook and Peppol.
3. **Algorithmic gaps in four places:**
   - XQuery value joins run as nested loops (XMark q8–q12).
   - The RELAX NG compiler recompiles each definition about 45 times.
   - `fn:transform` recompiles its stylesheet on every call.
   - XSD parses every `xs:integer` into a `big.Rat` three times, even with no
     facets to check.
4. **The tree node is 296 bytes** (allocated as 320), and each node is a
   separate allocation, scattered in memory. Parsed trees take 26 bytes of heap
   per input byte, and walking them misses the cache.
5. **Small per-call waste on hot paths:** `fmt.Sprintf` keys for every function
   lookup, two map literals built on every general comparison, and a document
   order sort even when the input is already sorted.

Measured effect of the low-risk fixes, all prototyped (warm, per item):

| Workload | Before | After | Gain | Saxon / reference |
|---|---:|---:|---:|---:|
| Peppol CEN | 46 ms | 11 ms | 4.2× | ~1.5 ms |
| Peppol PEPPOL | 33 ms | 8 ms | 3.8× | ~1.1 ms |
| XRechnung stage 1 | 41 ms | 23 ms | 1.6× | 2.5 ms |
| DocBook `chapter.003` (CPU) | 1,727 ms | 494 ms | 3.5× | — |
| DocBook `blocks.002` (CPU) | 133 ms | 39 ms | 3.4× | — |
| XMark q8 at 0.1 (eval only) | 3,239 ms | 17 ms | ~190× | 233 ms (incl. parse) |
| XMark q11 at 0.1 (eval only) | 7,918 ms | 193 ms | ~40× | 124 ms (incl. parse) |
| RELAX NG DocBook 5.2 compile | 500–650 ms | 40–50 ms | ~13× | Jing 108 ms |
| XSD `xp-striding` (validate only) | 33–35 ms | 11–12 ms | 2.8× | — |
| Parse 10 MB | 254 ms | ~200 ms | 1.27× | — |
| Heap per parsed byte | 26.0 B | 22.9 B | −12% | libxml2 ~14 |

DocBook figures are CPU time, which was steadier than wall time under the
concurrent load.

Outputs are byte-identical before and after on every benchmark item touched:
26 Peppol/XRechnung items, 42 DocBook items, all 40 XMark items. Where they
were run, the unit tests and conformance figures are unchanged: QT3 XQuery
30,516 passed; xsdtests 39,358 / 41,567 agree; RELAX NG spec tests 965/965.
The XSLT suites have not yet been run on the XSLT fixes.

## Cross-cutting causes

### C1. Copying the XPath context

`xpath.Context` is 440 bytes (`unsafe.Sizeof`). The evaluator copies it by
value at every scope change:

| Copy site | What triggers it | Where it dominates |
|---|---|---|
| `Context.WithVar` (`xpath/context.go:712`) | every variable/param binding: a copy, a one-entry map and a Clark-name string | DocBook 69% of bytes, Peppol 63%, XMark q8 22% |
| `Context.WithFocus` (`xpath/context.go:700`) | every node an axis step visits (`evalStepOver`, `xpath/eval.go:195`), every predicate candidate | Peppol 22%, XMark q8 35% |
| `Context.Descend` (`xpath/eval.go:562`) | every function call, built-ins included, only to do `Depth++` | XRechnung 25%, XMark q11 9% |
| `Compiled.Eval` (`xpath/xpath.go:334`) | `sub := *ctx` on every compiled-expression evaluation | XMark q8 12% |

Allocation is the cost, not collection: raising `GOGC` to 400 cut XMark q8's
GC cycles from 116 to 27 with no wall-time change. Every remaining fix in this
family removes copies; none tunes the collector.

### C2. Template patterns allocate before testing

`Pattern.matches` (`xslt/pattern.go:456`, `:461`) binds `current()` and the
output-URI variable with two `WithVar` calls, then tests the node.
`findTemplateFrom` (`xslt/apply.go:235`) tries every template in the mode in
turn. Counts per DocBook transform:

| Item | Dispatches | `Pattern.matches` calls | Patterns per dispatch |
|---|---:|---:|---:|
| `chapter.003` | 13,280 | 556,504 | ≈40 |
| `ptoc.001` | 19,439 | 558,782 | ≈26 |
| `blocks.002` | 1,227 | 10,465 | ≈8.5 |

So the cost grows with node count. The benchmark's "super-linear" gap against
Saxon is this per-node cost becoming visible as documents grow. It is not
O(n²): repeating `chapter.003`'s body 1, 2, 4 and 8 times scales linearly.

### C3. Function lookup by formatted string

`libKey` and `specKey` (`xpath/functions.go:26`, `xpath/funcspec.go:181`) are
`fmt.Sprintf("%s#%d", name.Clark(), arity)`. They run at every library level
of the parent chain on every `FuncCall.Eval`, built-ins included, and again in
`lookupSpecParams`. XMark q11 spends 0.69 s of 6.2 s in `lookupFor`.

### C4. Small per-call waste

- **Comparison map literals:** `evalGeneralComparison` (`xpath/operators.go:196`,
  `:239`) builds two map literals on every call, about 11% of comparison CPU
  in XMark.
- **Document-order sort:** `SortDocumentOrder` (`xdm/nodeset.go:27`) always
  allocates two slices and runs a stable sort, even when a forward axis has
  already produced document order.

## Per area

### XSLT: Peppol and XRechnung (Schematron shape)

CPU in the baseline CEN profile: `main.main`, which is all real work, is
4.9% cumulative; the GC (`gcBgMarkWorker`/`gcDrain`) is 30–43%. The rest is
thread hand-off and the allocator.

| Allocation site, CEN | Bytes | Objects |
|---|---:|---:|
| `Context.WithVar` | 62.5% | 47.5% |
| `Context.WithFocus` | 22.0% | 10.6% |
| `QName.Clark` | 3.2% | 19.5% |
| `SortDocumentOrder` | 2.5% | — |

SchXslt output has 106 templates in one mode plus `next-match` chains, which
is C2 at its worst. XRechnung is shaped differently: function calls
(`Descend`, C3) and predicates dominate, not dispatch.

### XSLT: DocBook xslTNG

The same C1/C2 profile: about 60% of CPU is GC, and `Pattern.matches` is
61.6% of bytes cumulative. Two workload-specific causes on top:

- **`fn:transform` recompiles every time.** `fp:run-transforms`
  (`docbook.xsl:459`) calls `transform()` with a `stylesheet-location`, and
  `compileNested` (`xslt/fntransform.go:795`) recompiles three stylesheets on
  every transform. F&O's `cache` option (default true) is accepted but has no
  effect. This is 24% of CPU on small documents, and most of the 1.55× gap on
  `blocks.002`.
- **`indexterm.001`: a context-independent value recomputed for every item.**
  `$letters/l:l[. = substring($term,1,2)]` (`modules/index.xsl:150`)
  re-evaluates `substring($term,…)` for each of about 620 `l:l` elements, for
  every index term, in both the group-by and the sort.

### XQuery: XMark

- **Joins are nested loops.** `letClause.apply` re-evaluates the inner FLWOR
  for every outer item, and `whereClause.apply` (`xquery/flwor.go:279`) tests
  every pair. Going from factor 0.01 to 0.1 multiplies q8's evaluation time by
  about 110, which is quadratic.
  - q8 makes 2.49 M comparisons at 26 allocations, 3.6 KB and 1.3 µs each.
  - q11/q12 make 3.06 M comparisons at 2.35 µs each. Inside each one,
    `5000 * exactly-one(...)` pays C3 and a `big.Rat` integer-to-double
    conversion.
- **The 150 ms floor on q1–q5 is the parse**: about 97% of q1, at 160–175 ms,
  against 1.7 ms of evaluation. See *Parsing and tree memory*.
- **The q15–q19 "warm slower than cold" anomaly is not go-xml.** In
  `results.json`, q15 warm has minimum 162 ms, median 433 ms, p75 626 ms. The
  minimum matches cold (177 ms), and a re-run in one process gives a 177 ms
  median. GC was cheap and the heap stable (about 480 MB) across groups. The
  bimodal, contiguous slowdown points to other load on the machine during
  that window. *The GC explanation in docs/benchmark.md is wrong and should be
  corrected.*

### Schema validation

**RELAX NG compile.** `checkCompetition` takes 455 ms of the 500–650 ms.
- **Cause:** `lazyRef` (`relaxng/compile.go:1116`) resolves a recursive
  reference with a fresh sub-compiler that does not share the `compiled` map.
  So 1,922 definitions are compiled 86,752 times, about 45× each.
- **Knock-on effects:**
  - Each copy has new pointers, which defeats the pointer-keyed memo in
    `competingSeen` (`relaxng/restrict.go:494`).
  - `noteBare` (`relaxng/compile.go:723`) adds 1.32 M map writes, against 29k
    with sharing.

**XSD validation.** On the 2.5 MB striding catalog:
- **`big.Rat` for every integer.** `validateAtomicValueBoundsIn`
  (`xsd/validate_simple.go:221`) is 40% of validation. It parses every
  `xs:integer` value with `big.Rat.SetString` (the `checkBounds` path), even
  though `xs:integer` has no bounds. It parses it again for the digit facets,
  which the built-in `fractionDigits=0` forces, and re-parses the bound
  literals on every value. There are 24,882 such attributes.
- **Bookkeeping nothing reads.** `recordKeyValue` fills a per-node map for
  every simple value, which only identity constraints read, and these schemas
  have none. `checkIdentityConstraints` merges tables for every element even
  when there are no constraints. `primitiveOf`, whitespace collapse and
  `StringValue` allocate on paths where nothing changes.
- **After the fixes, parsing is 70–80% of the XSD warm loop**: about 33 ms
  parse against 9 ms validation on the striding catalog.

### Parsing and tree memory

On the 10 MB file there are 784,958 nodes: 46% text, 31% of them
whitespace-only with just 27 distinct values. Retained heap after parse is
260 MB, 26 bytes per input byte:

| Component | Bytes | Share |
|---|---:|---:|
| `xdm.Node` structs (785k × 320 B size class) | 251 MB | **96.7%** |
| `Children` arrays (28% unused capacity) | 6.4 MB | 2.5% |
| `Attrs` arrays | 2.0 MB | 0.8% |
| Value strings (copies, not substrings) | 5.4 MB | ~2% |
| Name strings (22 distinct names, not interned) | 1.8 MB | <1% |

A text node needs about 48 bytes but carries 296. The typing fields
(`TypeAnnotation`, `UnionMember`, `DerivedPrimitive`, `ListItem`,
`DocumentURI`, `typeEnv`, five bools, `detachedID`, `numbered`) and
`Namespaces` are empty on nearly every node. Reordering fields does not help:
they add up to 289 bytes, which rounds to 296 in any order.

Parse CPU:

| Component | Share |
|---|---:|
| Tokenizer | ~50% |
| GC on other cores | ~26% |
| `runtime.madvise` (re-committing released pages; ~350 MB churned per parse) | 13–17% |
| `attNormReader` (byte-at-a-time pre-pass; new 4 KB buffer per `Read`, `append` that always reallocates — `xdm/attnorm.go:117`) | ~10% |
| Text copied twice (tokenizer `CharData`, then `string(t)`) | — |
| `StartElement` boxed into an interface per tag | — |

## Fix candidates

"API" means compatible with the v1 public API (exported fields and
signatures). Gains are measured in a private copy unless marked as an
estimate.

### Tier 1: small, low risk, large measured gains

| # | Fix | Where | Measured gain | API | Risk |
|---|---|---|---|---|---|
| T1 | **Reject before binding**: in `Pattern.matches`, return false when there are no general predicates and no alternative's last step passes the existing `nodeTestHolds`, *before* the two `WithVar` calls | `xslt/pattern.go` | DocBook ~2.5× (chapter 1,803→702 ms CPU); Peppol CEN 46→24 ms; allocations ÷3–7 | ✔ | low: a failed node test means no predicate runs |
| T2 | **Share the compiled-definition map with lazy sub-compilers** (and `defineNs`/`inheritedNs`) | `relaxng/compile.go:1116` | compile ~13× faster; 192→46 MB RSS; fixes bug B3 | ✔ | low: spec tests 965/965 |
| T3 | **Function keys as structs** `{uri, local, arity}` instead of `Sprintf` | `xpath/functions.go`, `funcspec.go` | XRechnung allocations 483k→230k, 39→29 ms; indexterm allocations −22% | ✔ (internal; 3 tests treat keys as strings) | low |
| T4 | **No focus copy for plain steps**: `Step.evalFrom(ctx, node)` called directly from `evalStepOver` | `xpath/eval.go:195` | Peppol CEN →11.4 ms, PEPPOL →6.8 ms (cumulative with T1, T3) | ✔ | low |
| T5 | **Skip the sort when already in order**: `SortDocumentOrder` returns its input when it is one tree in strictly increasing order (detached roots and namespace duplicates excluded) | `xdm/nodeset.go:27` | a further 10–20% fewer bytes on Peppol/XRechnung | ✔ | low |
| T6 | **Switch, not map literals**, in `evalGeneralComparison` | `xpath/operators.go:196`, `:239` | ~11% of comparison CPU | ✔ | low |
| T7 | **XSD fast paths**: `checkBounds` returns early without bound facets; digit facets counted from the lexical form; identity-constraint bookkeeping skipped when the schema has none; fast paths in `primitiveOf`, whitespace collapse, `StringValue` | `xsd/validate_simple.go`, `identity.go`, `xdm` | striding 2.8×, unicode-90 1.6×, others ~2× | ✔ | low: xsdtests unchanged. The identity gate must use the schema's constraint count, not `hasIdentityConstraints()` (bug B4) |
| T8 | **Allocate nodes in chunks** (≤32 KiB; larger chunks are page-rounded and *grow* the heap) | `xdm/parse.go` | parse −22%, mallocs −21%, GC mark time −43%; Finalize ~4× and C14N ~2× faster from locality | ✔ | low: one retained node keeps its 32 KB chunk alive |
| T9 | **Unexported node fields out of line** (`typeEnv`, `detachedID`, `numbered` behind a lazily created pointer) | `xdm/node.go` | node 296→280 B (size class 320→288); heap −9.7%; with T8, −12% | ✔ | low |
| T10 | **Reuse `attNormReader`'s buffer** | `xdm/attnorm.go:117` | −19 MB allocated per 10 MB parse | ✔ | low |

### Tier 2: medium effort or medium risk

| # | Fix | Measured / estimated gain | Risk and what it needs |
|---|---|---|---|
| T11 | **Honour the `fn:transform` cache**: cache `compileNested` by (module tree, base URI, static params, version, resolver) | DocBook `blocks.002` ~2.5×, larger items 5–20% | Low. Needs a bounded cache attached to the outer stylesheet; the prototype used an unbounded `sync.Map` |
| T12 | **Inline single binding**: `WithVar` stores one name/value pair in the context instead of a one-entry map | On top of T1: DocBook chapter 702→501 ms CPU | Medium: anything that copies a context and swaps `Vars` must carry the inline pair (only `xpath/funcitem.go` found). Context grows 440→480 B |
| T13 | **Hoist a context-independent comparison operand** out of the predicate loop (literal, variable, or pure-function call over those), falling back to the per-item loop on error so error order is unchanged | `indexterm.001` 599→449 ms CPU, allocations −44% | Low–medium: limited to the existing pure-function allowlist |
| T14 | **FLWOR join**: for `for $v in S where A op B` with one side reading only `$v` and the other only another variable: hoist `S`, cache each item's atomized key, hash-join `=` over strings/untyped with the codepoint collation, otherwise loop over the cached keys with the original comparison routine; any error falls back to the nested loop | q8 3,239→17 ms, q9 3,683→23, q11 7,918→193, q12 7,556→191 (eval); all 40 outputs identical; QT3 XQuery unchanged | Medium. The prototype's per-clause mutex must become per-`Eval` state (concurrency, re-entrancy, retention). Needs tests for multi-valued keys, numeric/typed keys, `exactly-one` errors, NaN, default collation |
| T15 | **Count call depth in place**: `ctx.Depth++ / --` instead of `Descend()` | XRechnung 34→23 MB per item, ~29→23 ms | Medium: mutates a shared context. Safer variant: only for built-ins that never call back into user code |
| T16 | **Index templates by mode and element name** (rule chains) instead of a linear scan | Estimate: removes the remaining 20–40 iterations per node after T1 | Medium; precedence, priority and `next-match` order must be preserved exactly |
| T17 | **Resolve function calls at compile time**, once per `FuncCall` node | Estimate: removes C3 entirely | Medium (dynamic contexts, `function-lookup`) |
| T18 | **Parser copies**: tokenizer returns strings (no double text copy), attribute slice reused, names and whitespace-only text interned | Estimate: ~660k fewer allocations per 10 MB parse | Low–medium |
| T19 | **Attribute normalisation inside the tokenizer**, dropping the pre-pass | Estimate: ~10% of parse CPU | Medium: conformance-sensitive |

### Tier 3: large, or needs v2

| # | Fix | Estimated gain | Constraint |
|---|---|---|---|
| T20 | Split `xpath.Context` into a small per-scope part and a pointer to the static part (resolvers, versions, budgets, host) | Every remaining copy ~5× cheaper; the broadest single win | Exported fields → API change (v2) |
| T21 | Move node typing fields and `DocumentURI` behind accessors | Node 280→192 B, heap −40% | Exported fields → v2 (~125 call sites) |
| T22 | Further node packing: `BaseURI` stored only where it differs from the parent, `Namespaces` out of line, interned `*QName` | Node to 112–152 B (2–3× smaller than today) | v2 |
| T23 | RELAX NG hash-consing with memoised derivatives (Jing's design) | Removes the derivative size bound; replaces B2's structural check | Large |

## Implementation status

Landed on `dev`, each with a test that fails without it. QT3, XSLT 2.0/3.0,
XSD 1.0/1.1 and the RELAX NG spec tests keep the same counts and the same
failing names, and every benchmark output touched is byte-identical.

| # | Commit | Measured after landing |
|---|---|---|
| T1, T3, T4 | `262366d`, `9bd1c79`, `4f0cc55` | Peppol CEN 37.1 → 8.8 ms, PEPPOL 26.2 → 6.2 ms, XRechnung 29.0 → 18.4 ms per item; DocBook `chapter.003` 1,570 → 524 ms CPU (with T5) |
| T5 | `1c7fb99` | CEN −16.5% bytes |
| T6 | `6ce285f` | `$a = $b` 274 → 160 ns; no allocation was removed (the map literals never escaped), so no visible workload gain. The "~11% of comparison CPU" above overstated it |
| T2 + B3 | `c7769f5` | DocBook 5.2 compile 0.5 s → 20–25 ms, 364 → 12 MB allocated |
| B1, B2 | `b44313c`, `d8f0ac1` | all 40 DocBook verdicts match Jing and xmllint |
| T7 | `ad3aa67`, `62c7117`, `30a9117`, `d229d13`, `af36d31`, `949e0fa` | `xp-striding` 32 → 9.4 ms, 654k → 27k allocations |
| B4 | `1f80934` | root cause: the schema scan missed constraints on local and group declarations, so recursive scopes re-reported errors once per level. Verdicts were never wrong. `hasIdentityConstraints` is gone; scopes are counted on the walk |
| T8–T10, T18 | `bcae9bd`, `b5280b4`, `cadd717`, `41e5cb4`, `446432b` | 10 MB parse: 3.72 M → 1.13 M allocations, 243 → 161 ms, retained heap 27.0 → 23.4 B per input byte. T18's double text copy did not exist; the token boxing did, and is gone |
| T11 | `e00735a` | DocBook `blocks.002` −40% CPU; `chapter.003` unchanged (the 2.5× estimate held only for small documents) |
| T14 | `2aa49d8`, `ed75977` | XMark q8 3,296 → 14 ms, q9 3,918 → 26, q11 7,238 → 223, q12 6,992 → 230 (evaluation only) |
| T12 | `8cfeef4` | CEN 78.6k → 65.3k, PEPPOL 70.5k → 55.3k, `chapter.003` 1.88 M → 1.37 M allocations per item. Context grows 440 → 496 B |
| T13 | `1253f9a` | `indexterm.001` 540 → 370 ms CPU, 3.75 M → 1.64 M allocations |
| T15 | `569af46` | safe variant: a reviewed allowlist of leaf built-ins. XRechnung 33.2 → 26.0 MB per item; `indexterm.001` 529 → 466 ms CPU |
| T16 | `c008a63` | patterns tried per dispatch: CEN 90 → 1.9, `chapter.003` 40 → 1.3. CPU −5–17%; no allocation change, since T1 had already made a rejected pattern free |
| T19 | `d212f7a`, `6f236b5`, `b764d67` | with the duplicate-attribute and slice changes: 10 MB parse 1.13 M → 0.37 M allocations, 149 → 93 ms. Line-end normalisation (§2.11) stays a pre-pass; it now skips reads with no CR |

Found and fixed along the way: RELAX NG `<include ns>` leaking into
definitions reached through `<ref>` (`c9c7c79`), an explicit `ns=""` read as
absent (`5cf8104`), quadratic text joining across CDATA sections (`0604061`),
`fn:transform` option typing and `enable-*` switches (`84bbe23`,
`a2603d3`), and XML attribute-value normalisation: 1.1 line ends,
entity replacement text, a PI in the internal subset, DTD declarations
inside PIs, attribute defaults, DOCTYPE line ends, Legal Character, `--` in
DTD comments and the §4.1/§5.1 entity rules (`e03ee3d`, `b7853d6`, `b707e39`,
`efd4178`, `02905e9`, `0ca7dac`, `81b0a27`, `17b99c3`, `3d7e86b`, `4949a8f`,
`97148b4`, `7496ea8`, `ef1eba5`).

T17 was prototyped and not landed: since T3 a lookup allocates nothing, it is
about 3% of CPU, and a cache that stays correct when a library changes after
first use cost more allocations than it saved.

Still open: Tier 3 (T20–T23, which need v2). The benchmark has not been
re-run; docs/benchmark.md still shows the `4e0496f` figures.

## Correctness bugs found while profiling

| # | Bug | Evidence | Fix |
|---|---|---|---|
| B1 | **RELAX NG §6.2.7**: whitespace-only text between element children is matched against `text` instead of being dropped, which kills the element branches of a choice. 6 of the 7 DocBook documents go-xml wrongly rejects | Minimal repro: `<p>\n <f>a</f>\n</p>` against `choice(zeroOrMore(text), oneOrMore(element f))` is rejected | strip in `childrenDeriv`, ~15 lines (measured) |
| B2 | **RELAX NG `choice()` does not merge identical alternatives**: nested `oneOrMore` doubles the derivative per item, so `xref.001` hits the 100,000-node limit | With B1 fixed, `xref.001` fails on the size limit | structural dedupe in `choice()`, ~50 lines (measured); with B1 all 40 verdicts match Jing and xmllint. Validation ~15% slower per document until T23 |
| B3 | **RELAX NG recursive refs in `<include ns=…>` lose the namespace**: the lazy sub-compiler drops `defineNs`/`inheritedNs` | `<a xmlns="urn:x"><a/></a>` wrongly rejected, `<a xmlns="urn:x"><a xmlns=""/></a>` wrongly accepted | fixed by T2 |
| B4 | **`hasIdentityConstraints()` returns false for schemas that do have constraints** (`xsd/identity.go:701`; `declaresConstraint` at `:725` sees a nil declaration) | Gating on it broke `TestIdentityKeyrefIsSubtreeScoped` and `TestIdentityConstraintRef` | Not investigated. Whether it causes wrong verdicts today is an open question |

Corrections for [docs/benchmark.md](benchmark.md):
- The q15–q19 anomaly is not GC: see *XQuery: XMark*.
- The RELAX NG accounting needs a note. Of the 33 timed documents, 5 are
  rejected by all three validators: `JFK_Inaugural` uses `dialogue`, which is
  not in DocBook 5.2, and four carry unexpanded `xi:include` elements. They were
  timed because all three verdicts agree.

## Suggested order

1. **Tier 1, T1–T10.** Each is a small, separate commit, measured against this
   benchmark and gated on the conformance suites (same failing sets in QT3,
   XSLT 2.0/3.0 and xsdtests; RELAX NG spec tests). T1, T2 and T7 alone give
   the largest measured gains.
2. **Correctness B1–B3**, with B3 arriving through T2. Investigate B4.
3. **Tier 2:** T11 and T14 first (largest measured gains), then T12, T13, T15,
   with T16–T19 as follow-ups.
4. **Re-run the full benchmark** and update docs/benchmark.md with the new
   figures and the two corrections above.
5. **Tier 3** is a v2 discussion: the context split (T20) and node slimming
   (T21–T22) are where the remaining gap to Saxon and libxml2 lives.

## Method notes

- **Harnesses** copy the benchmark's warm loop: compile once, then
  parse → transform/query/validate → serialise to `io.Discard`, with the same
  parse options, resolvers and parameters as the benchmark items.
- **Profiles** are `runtime/pprof` CPU and `allocs`. Heap size comes from
  `runtime.GC` and then `MemStats`.
- **Wall times** were noisy under concurrent load. Allocation counts are exact;
  DocBook figures use CPU time (getrusage).
- **Prototypes** were built on `git archive` copies of the tree, never in the
  repository. Each was checked by diffing all workload outputs against the
  unmodified build and running the affected packages' unit tests. The XSLT
  conformance suites were not run on the XSLT prototypes; that is required
  before any of them lands.
