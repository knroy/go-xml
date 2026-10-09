# Profiling: why go-xml is slow in steady state, and what fixes it

[docs/benchmark.md](benchmark.md) showed go-xml losing to warm JVMs by 2.5× to
85× once compilation is paid. This report profiles every losing workload with
`pprof` (CPU, allocation and heap profiles plus `runtime.MemStats`). It names
the causes with evidence, and measures the most promising fixes on a private
copy of the tree.

**Status:** the analysis was made on `dev` at `4e0496f`. Tier 1 (T1–T10),
T11–T16, T18, T19 and the correctness bugs B1–B4 have since landed; see
[Implementation status](#implementation-status). A second round re-profiled
the result and its fixes have landed as well; see
[Round 2](#round-2-after-the-fixes-73a2963); [Round 3](#round-3-after-the-round-2-fixes-3fc468a)
ranks what is left. Profiling ran on the same
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
| `runtime.madvise` (re-committing reused heap pages, ~350 MB churned per parse; macOS only: Go's darwin runtime calls `madvise(MADV_FREE_REUSE)` on every reuse, the Linux runtime calls nothing) | 13–17% on macOS, ~0% on Linux |
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
| T23 | RELAX NG hash-consing with memoised derivatives (Jing's design) | Removes the derivative size bound; replaces B2's structural check | Large. Landed in round 5 (`77cdd65`): the bound stays, and the gain is on long documents only |

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

Still open: Tier 3 (T20–T23, which need v2). Round 2 below re-profiles the
result and ranks what is left; docs/benchmark.md was re-run at `eb14939`,
after both rounds.

## Round 2: after the fixes (`73a2963`)

All workloads were re-profiled on `dev` at `73a2963`, cold and warm, with the
committed `bench/` harness for go-xml and round 1's figures for the reference
engines (same machine). Four lanes ran at once, so wall times are noisy; the
claims rest on allocation counts, CPU time, GOGC=off A/B runs and back-to-back
pairs. Every prototype below was checked for byte-identical outputs and its
packages' unit tests; the parser and XSLT prototypes also against the XSLT,
QT3 and xsdtests suites.

### Where it stands

| Workload | Warm now (was) | Warm vs reference | Cold now (was) | Cold vs reference |
|---|---|---|---|---|
| Peppol CEN | 7.9 ms (38.4) | Saxon 4.9× slower (was 24×) | 60 ms (94) | Saxon 12× faster |
| Peppol PEPPOL | 5.5 ms (27.1) | 4.2× (was 21×) | 31 ms (52) | 20× faster |
| XRechnung xr | 14.4 ms (23.9) | 5.8× (was 9.6×) | 40 ms (44) | 14× faster |
| DocBook, 42 items | 21.4 ms (68.6) | **0.66×**; faster on 29 of 40 | 165 ms (165) | 6.5× faster |
| XMark 0.1, q1–q20 | 63–245 ms (152–7,189) | 0.29–2.9× Saxon; faster on q1, q5, q8, q9, q17 | 83–290 ms (168–6,351) | 3–10× faster |
| XSD catalog | 0.97 ms (2.8) | Xerces **0.69×** | 9.1 ms (11.5) | xmllint 2.0× slower |
| RELAX NG DocBook | 0.20 ms per document | Jing 6.7× slower | 39.9 ms (494) | xmllint 1.4× slower; Jing 4.5× faster |
| DocBook 5.2 compile | 35.7 ms (496) | Jing 3× faster | — | — |
| Parse 10 MB | 127 ms (268) | — | 160–178 ms (298) | `xmllint --c14n` about level |

The steady-state gap that is left is in Schematron-shaped XSLT (4–6× Saxon),
and in XMark queries that walk `//name` or still join pairwise. Cold, go-xml is
ahead of the JVMs everywhere and level with libxml2 on large inputs. The
remaining cold gap is on small inputs, where start-up and compile dominate.

### Where cold time goes

| Phase | Cost | Cause |
|---|---|---|
| Process start | 4.6–6 ms (xsltproc 2.2–2.9, empty Go binary 2.5) | `xsd.HTTPResolver` imports `net/http`, which links `crypto/tls` and, on macOS, the Security framework: about 1.7 ms. Package init is only 0.6–0.7 ms in total |
| Parsing (input, stylesheet modules, schema documents) | 5.5–11 ms of a RELAX NG compile; 16.7 of DocBook's 75 ms compile; 67 ms of every 11 MB XMark query | `validateStartElement` builds two maps per start tag, so with more than 8 namespaces in scope they go to the heap (`xdm/wellformed.go:106–131`): 13–22% of compile and load bytes. With `AllowDOCTYPE` set and no DOCTYPE, the source is still copied twice (cause L3 below) |
| Stylesheet compile | DocBook 70 ms, CEN 30 ms | `InScopeNamespaces` rebuilt per element (`xdm/node.go:1393`, through `newNSResolver`): 32–35% of compile bytes, about 10 ms per compile |
| Schema load | XSD 1.1 schema for schemas 4.4–5.4 ms; `schema-for-xslt30.xsd` with `-catalog` 7.3–9.4 ms | DTD attribute typing rebuilds `prefix:local` per ATTLIST per element, quadratically (`xdm/dtd_defaults.go:265`, `xdm/wellformed.go:196`): 30% of the schema-for-schemas load |
| Garbage collection in a cold CLI | 50–55% of CPU, 7–13% of wall | The heap only grows during a parse or compile, but GC cycles keep re-marking it |
| Output | q10 0.36 s, of which 0.21 s is system time | `xslt.Serialize` writes unbuffered to the `-o` file, one syscall per token (`xslt/serialize.go:60`) |

### Steady-state causes

| Cause | Where | Evidence |
|---|---|---|
| `//name` builds the whole `descendant-or-self::node()` sequence, sorts it, then visits every node again | `xpath/eval.go:75`, `:149`, `:264` | XMark q7 eval 112 ms, q6 44 ms; 27% of CEN's bytes |
| XSLT clears seven absent context components on every template and function call; patterns with no predicate still bind `current()` | `xslt/merge.go:1224`, `xslt/grouping_absent.go:30`, `xslt/pattern.go:466` | `clearMergeContext` is 91% of CEN's `withVar` bytes |
| GC is now most of the XSLT CPU: 52–72% warm | — | Every collection re-marks the retained stylesheet (CEN 19.7 MB). Round 1's "allocation is the cost, not collection" no longer holds for XSLT |
| `current()` is not a leaf call | `xpath/context.go:824` | 18% of XRechnung's bytes |
| The T14 join casts the untyped outer key once per pair, and checks its cache in O(\|S\|) | `xquery/join.go:283`, `:315` | q11/q12: 62% of objects; q9 at factor 1 grows 53× for 10× data |
| `Compiled.scope` copies the 496 B context on every XQuery expression | `xpath/xpath.go:335` | Fires on every evaluation measured; this is the T20 problem by another route |
| RELAX NG `startTagCloseDeriv` rebuilds the pattern for every element, and each rebuilt choice goes back through B2's dedupe | `relaxng/derive.go:337` | `patEq` 18% of validation samples |
| Text, attribute values and comments are one heap object each | `xdm/parse.go:870`, `internal/xmltok/xmltok.go:440` | 371k mallocs and 185k live objects per 10 MB parse |
| Node size | `xdm/node.go` | 280 B; heap about 13–22 B per input byte against libxml2's ~14 and Saxon's ~2.5–7. Below the 256 B size class needs T21 (v2) |

### Fix candidates, ranked

Every fix keeps the v1 API unless marked v2. "Measured" means prototyped and
timed on a copy of `73a2963`.

| # | Fix | Measured gain | Risk |
|---|---|---|---|
| R1 | **Look namespace prefixes up instead of building maps per start tag** (fall back to a map above 8–16 attributes) | DocBook RNG parse 11.3 → 5.5 ms; `validateStartElement` 9.5 → 3.5% of parse CPU; 13–17% of XSLT compile bytes | low |
| R2 | **Share one in-scope namespace map per declaring element during compile** | with R1: DocBook compile 70 → 51 ms, CEN 39 → 27 ms; retained stylesheet −14–23% | low |
| R3 | **Fuse `//T` into `descendant::T`** when neither step has a predicate | q7 112 → 28 ms, q6 50 → 15, q14 44 → 18; at factor 1 q7 2.46 → 0.48 s; CEN −8% bytes | low |
| R4 | **Join: cast the outer key once; range lookup for `< <= > >=`; identity check in `sameBindings`; `return $v` reads the tuple** | q11 173 → 29 ms, q12 172 → 30; q9 at factor 1 1,018 → 103 ms | low–medium |
| R5 | **Buffer `xslt.Serialize`** | CLI q10 0.36 → 0.12 s | low |
| R6 | **The CLI turns GC down while it parses and compiles** (`SetGCPercent`, never in the library) | CEN cold 58.7 → 41.9 ms, DocBook 129 → 95; 10 MB parse CPU 172 → 82 ms | memory: chapter.003 peak RSS 122 → 138 MB at 200 |
| R7 | **Stop the source copies once the root opens with no DOCTYPE** | −105 MB allocated per 10 MB parse; peak RSS 2.97 → 2.31 GB at 100 MB | low |
| R8 | **XSLT: skip clearing absent components; no bindings for predicate-free patterns; `current()` as a leaf** | CEN −14% wall, −23% CPU; PEPPOL −18%; DocBook −10% wall | low–medium |
| R9 | **DTD attribute typing builds names once per tag** | schema-for-schemas load −25%; `-catalog` XSLT 3.0 schema 8.5–9.4 → 6.6–7.0 ms | low |
| R10 | **RELAX NG: `startTagCloseDeriv` returns its input when nothing changed** | warm validation 2.2× faster, allocations ÷5 | low |
| R11 | String arenas for text and attribute values; 1,024-node chunks | mallocs 371k → 14k per 10 MB parse | low–medium |
| R12 | C14N: escape through lookup tables, 64 KiB output buffer | 10 MB C14N 22.4 → 16.5 ms | low |
| R13 | Small items: cache `canonicalLocation` per load; no character counting without a length facet; `nonSpaceText` before `Trim` | catalog load −5–7%; XSD validate −6% | low |
| E1 | XSLT dynamic state on one pointer instead of the variable chain (estimate) | 8–15% of XSLT bytes that R8 cannot reach | medium |
| E2 | Element-name index per tree for `descendant::name` (estimate) | q6/q7/q14 eval −60–80% after R3 | medium |
| E3 | Bitset NFA states in XSD 1.1 restriction checks (estimate) | −15–20% of XSD 1.1 schema loads | medium |
| v2 | Move `HTTPResolver` out of `xsd` (exported `Client *http.Client`); T20 context split; T21/T22 node slimming | start-up −1.7 ms; heap 13 → 5–7 B per byte | v2 |

Together on a copy of the tree, the XSLT prototypes (R1, R2, R3, R8) took
CEN from 6.56 to 5.64 ms and DocBook from 38.6 to 34.6 ms per item. The
XQuery ones (R3, R4, R5) took the worst warm ratio to Saxon from 2.94× to
1.52×, after which parse is 55–90% of every 11 MB item. The parse ones (R1,
R6, R7, R11) took a cold 10 MB transform to xsltproc's CPU time.

T23 (RELAX NG hash-consing) is not worth it yet: R10 removes most of B2's
cost in about 60 lines, and a cold run is dominated by the compile.

### Round 2 implementation status

Landed on `dev`, each with a test that fails without it; every suite and
corpus keeps its counts and failing names, and every workload output is
byte-identical.

| # | Commit | Measured after landing (against `afb3fae`) |
|---|---|---|
| R1 | `549d8db` | `docbook.rng` parse 9.7 → 4.6 ms, 51.7k → 0.5k allocations |
| R2 | `ebd1759` | compile bytes DocBook −30%, CEN −25%; retained stylesheet CEN 19.7 → 17.0 MB |
| R3 | `3cd86ee` | XMark 0.1 q7 99.6 → 27.7 ms, q6 42.6 → 14.2; factor 1 q7 6.1 → 0.45 s |
| R4 | `ba37f7c` | q11 188 → 30 ms, q12 162 → 27 ms |
| R5 | `a931b98` | CLI q10 `-o` 0.34–0.62 → 0.12–0.14 s |
| R6 | `5fbea36` | GOGC=200 chosen over pausing around parse/compile: cold CPU summed over four workloads 1,025 → 748 ms; DocBook peak RSS 114 → 161 MB |
| R7 | `b88105e` | 10 MB parse 335 → 230 MB allocated; 100 MB peak RSS 3.15 → 2.0 GB |
| R8 | `b0c7b30` | warm bytes CEN −16%, PEPPOL −23%, xr −23%, DocBook −16%; DocBook warm wall 54.7 → 37.5 ms per item |
| R9 | `68795ba` | XSD 1.1 schema for schemas load 4.6 → 3.2 ms |
| R10 | `7ad91bd` | 40-document validation pass 11.5 → 4.9 ms CPU, allocations ÷5 |
| R11 | `aa7d1e2` (with `8fb6f0b`, `d2d59ab`) | 10 MB parse 371k → 7k allocations, CPU about 175 → 135 ms |
| R12 | `8e63b9d` | 10 MB C14N 22.3 → 17 ms; to a file 28.6–34.5 → 19.4–20 ms |
| R13 | `3b06e4c` | five includes in a 2,000-file directory 20.5 → 3.6 ms; warm validation −9% |

Not landed: E1 (needs a new unexported `xpath.Context` field; deferred), E2
(an element-name index cannot be invalidated soundly while `xdm.Node`'s
`Children`, `Name` and `Parent` are exported fields), E3 (the restriction
checks are 6–9% of an XSD 1.1 load, so bitsets cannot reach the estimate),
P5 (1,024-node chunks: within noise, +0.28 GB peak RSS at 100 MB), `//@a`
fusion (needs a new walk; one workload uses it) and `//x[p]` fusion (a
different error can win with nested `x`, so it is not equivalent).

### Bugs found in round 2

| # | Bug | Status |
|---|---|---|
| X1 | A FLWOR join charges \|S\| items per outer tuple against `MaxItems` (5,000,000, not configurable), so XMark q8, q9, q11 and q12 fail at factor 1 with `XPDY0130`. The nested loop it replaced charged the same | the join part fixed in `2514a9a` (q8–q10 at factor 1 now run); q11 and q12 really keep ~12 M tuples and still exceed the non-configurable `MaxItems` |
| X2 | A pattern predicate that is numeric through a function call, as in `item[number(@n)]`, is evaluated with position fixed at 1, so it matches `@n = 1` rather than position `@n`. Saxon-HE 12.10 gives the positional answer | fixed in `0602694` |
| X3 | `-catalog` read a schema's sibling `XMLSchema.xsd` before the catalog's, and that copy's DOCTYPE was refused | fixed in `8aa5ab7` |
| X4 | With `AllowDOCTYPE` set and no DOCTYPE, the parser keeps two extra full copies of the document; `fn:doc` and `fn:parse-xml` always set it | fixed in `b88105e` (R7) |

The benchmark harness has an inconsistency: its cold parse helper sets
`AllowDOCTYPE` and its warm loop does not, so the two columns time different
code paths. `docs/benchmark.md` was re-run with every engine at `eb14939`,
after the round-2 fixes.

## Round 3: after the round-2 fixes (`3fc468a`)

Re-profiled after the benchmark re-run at `eb14939` (same code), with four
lanes and prototypes on copies of the tree. Each prototype kept every
workload output byte-identical; the parser, XSLT and XQuery prototypes also
held every conformance suite at its ratchet count with the same failing
names. Wall times are noisy (lanes ran together); the claims rest on CPU
time, allocation counts and alternated A/B runs.

### Where the room is

| Workload | Now | With round-3 prototypes | Reference | What is left after them |
|---|---:|---:|---:|---|
| Peppol (both rule sets), warm | 2.80× Saxon | **2.17×** | Saxon 0.9–2.9 ms | per-element namespace nodes in SVRL output, `//` and predicate context copies (v2 or medium) |
| XRechnung stage 1, warm | 3.91× | **2.55×** | Saxon 2.4 ms | the same, plus `name()` building strings |
| XRechnung stage 2 (HTML) | 0/8 comparable | **8/8 agree** | — | — |
| DocBook, warm | 0.65× | **0.51×**; items slower than Saxon 7 → 4 | Saxon 32 ms median | `ptoc.001` 1.8×, `chapter.003` 1.4× |
| XMark, warm | 1.09× | **1.01×** | Saxon | parse floor: 50 ms against 45, all node bytes |
| XSD warm | 0.86 ms (0.75× Xerces) | **0.735 ms** | Xerces 0.99 ms | `xp-striding` is now 69% parse |
| RELAX NG warm | 2.7× Jing | **~1.3×** | Jing 0.03 ms | `attDeriv` on attribute groups |
| RELAX NG compile | 26.9 ms | 23.5 ms | Jing 108 ms | — |
| CLI start-up | 4.8–5.5 ms | **3.2–3.8 ms** with a build tag | xsltproc 2.2–2.9 ms | — |
| Parse 10 MB, parse thread | 85.6 ms | 77 ms | — | tokeniser 45%; node allocation |
| Retained heap | 23 B per input byte | unchanged | libxml2 ~14 | Node structs are 95.6% of it: v2 |

The steady-state gap that remains after these is structural: XSLT holds
dynamic state in a variable chain and copies a 512 B context per predicate
item, and every node is 280 B. Below that, every lane found that the next
step needs an exported-field change (v2).

### Fix candidates, ranked

Every fix keeps the v1 API. "Measured" means prototyped and timed.

| # | Fix | Gain | Risk |
|---|---|---|---|
| S1 | **Call-site function cache** keyed on library, host, version and a library-generation counter, plus one runtime function library per stylesheet so it survives across transforms (`xpath/eval.go:597`, `xslt/runtime.go:790`) | measured: XRechnung −30% wall; CEN −10%, PEPPOL −11% | low–medium |
| S2 | **XSLT dynamic state on one unexported Context field** (E1), carrying the known-absent bits so stylesheet functions stop re-clearing merge, grouping and regex context | measured: DocBook bytes −11 to −17%; `epub.001` 100 → 49.5 ms with S1 and S3 | medium |
| S3 | **`TransformOptions` by pointer in the runtime** (672 → 328 B per copy) | measured: `ptoc.001` CPU −20%; `epub.001` −16% | low |
| S4 | **Namespace-free node ordering and live namespace fixup**: skip the namespace base when neither node is a namespace node; `fixupNamespaces` reads bindings instead of building a map | measured: CEN −8% and −5% | low |
| S5 | **Validation without allocation**: XSD count vectors built in place and deduplicated on insert (V3), per-validator walk buffers (V4) | measured: 101,519 → 343 allocations per catalog pass; warm −15% | low |
| S6 | **RELAX NG memo points**: subtrees of size ≥4 behind pre-resolved refs remembering their derivatives per element name; `noteBare` per innermost definition; ASCII NCName fast path | measured: warm 0.100 → 0.040 ms per document; compile −3.4 ms | medium |
| S7 | **Number nodes while parsing** instead of a `Finalize` walk; cache the last whitespace run per length; check eight ASCII bytes at a time in `checkChars` | measured: parse thread −8 to −10%; tokeniser −15% | low–medium |
| S8 | **`descendant::name` walks the tree directly** with the name test inlined | measured: XMark q7 eval −21%, q6 −22% | low |
| S9 | **CLI parses from a stream or `bytes.Reader`** instead of `ParseString(string(data))` | measured: 10 MB cold CPU −13 to −20%; XMark peak RSS −11 MB | low |
| S10 | **Build tag `goxml_nohttp`** keeping `HTTPResolver` out of the CLI binary (the default build keeps the API) | measured: start-up −1.6 to −2.0 ms, RSS −4.8 MB, binary −12% | low |
| S11 | **Configurable `MaxItems`**: an additive `xpath.Context.MaxItems` (0 = default 5,000,000, negative = none), `TransformOptions.MaxItems`, `-max-items`; the range cap and `AdoptBudget` follow it | lets XMark q11/q12 run at factor 1, at 37.5 M items and ~3.5 GB | low |
| E | Estimates: cache `doc()` per transform (−4% `indexterm.001`), compiled `xsl:evaluate` (−2.5% `ptoc.001`), `Compiled.scope` without a copy (3–5%), lazy RELAX NG `nsContext` (−7%; landed with S6 in `060b08f`), `attDeriv` memo for text-only attribute groups (≤ −15%) | — | low–medium |
| v2 | Node slimming (T21/T22), shared namespace maps on result elements, boolean constants, `HTTPResolver` out of `xsd`, the context split (T20) | heap 23 → 5–13 B per byte; SVRL −15–20% bytes | v2 |

Checked and rejected this round: a memory limit instead of `GOGC=200` (a
limit under the live heap costs 10–25× CPU, and the CLI cannot know the live
size); bigger node chunks (no gain); a SWAR scan in the tokeniser's `text()`
(no gain); a 64 KiB file read buffer (no gain).

### Round 3 implementation status

Landed on `dev`; every suite and corpus keeps its counts and failing names,
and every workload output is byte-identical. Figures are against `1cd6878`,
measured while other lanes ran, so the allocation and CPU figures are the
reliable ones.

| # | Commit | Measured after landing |
|---|---|---|
| S1 | `87cf96e` | call-site cache keyed per library (a global counter would have reset every cache on each XQuery operand); XRechnung CPU −26% with S2/S3 |
| S2 | `2a7843f` | XSLT state on one 8 B context field (context 504 B); DocBook CPU −27% over all 42 items with S1/S3, `epub.001` −42% |
| S3 | `ab65f9f` | runtime copy 672 → 328 B |
| S4 | `53cff35`, `7c0d318` | CEN allocations −3.2% and CPU −4.7% |
| S5 | `d006ad2` | XSD catalog pass 101,505 → 341 allocations, 17.9 → 14.0 ms CPU |
| S6 | `abd214b`, `850c04c`, `42aa3d1`, `060b08f`, `f6080a2` | RELAX NG 40-document pass 4.84 → 0.87 ms CPU; memo points need no `unsafe`, and `MaxPatternSize` fires on the same inputs |
| S7 | `e7ec81d`, `ac6d163`, `d19a0ce` | 10 MB parse −4% (numbering) and −1.6% (whitespace); tokeniser −36% on prose |
| S8 | `659fdc8` | XMark q6 eval −23%, q7 −28% at 0.1; −30% at factor 1 |
| S9 | `739c744` | peak RSS −11 MB on XMark q1, −10 MB on a 10 MB transform; the CPU gain profiling credited to `bytes.Reader` was GC timing and did not hold |
| S10 | `dccd679`, `6f63564` | CLI start-up 5.3–6.3 → 3.7–4.3 ms, RSS 12.5 → 7.7 MB, binary −12%; CI builds and tests both ways |
| S11 | `3727b35` | `MaxItems` configurable, plus CLI `-max-bytes`; XMark q11/q12 at factor 1 run (3.3 / 4.5 s, 3.6 / 2.9 GB) and match Saxon |
| E | `72cb46a`, `3574659` | `doc()` path resolution 10.6 → 2.6% of DocBook CPU samples; `xsl:evaluate` allocations −1.4 to −5.8% |

### Bugs found in round 3

| # | Bug | Status |
|---|---|---|
| Y1 | The html method drops the stylesheet's own `<meta charset>` and `<meta http-equiv>` even with `include-content-type="no"`; Serialization 3.1 §7.4.13 allows it only when the serializer adds one. This is why XRechnung stage 2 never agreed with Saxon | fixed in `262be91`, with `91beb30` (only children of `head`) and `1e2bad6` (`fn:serialize`) |
| Y2 | html indentation adds whitespace next to inline elements (`<p><b>…</b><i>…</i></p>` splits across lines), which §7.4.3 forbids | fixed in `4457808` (`xsl:output`) and `8964e07` (`fn:serialize`), sharing `internal/htmlser` |
| Y3 | `xsl:decimal-format NaN=""` and `infinity=""` are ignored: an empty value is taken as absent | fixed in `1b65291`; the nine single-character attributes had the reverse mistake, now `XTSE0020` |
| Y4 | XSD 1.1: in a choice of a wildcard and an element declaration, the wildcard's readings are committed before the element is tried, so `<r><a>1</a><a>x</a></r>` is accepted without checking `a`'s type; Xerces rejects it | fixed in `5912a5a`; 45 of 45 cases agree with Xerces |
| Y5 | Unverified: `Compiled.scope` installs an expression's namespaces only when the context has none, so a nested evaluation may resolve a `$calendar` prefix against the caller's namespaces | real; fixed in `8a792f7` for `format-date`, `format-dateTime`, `format-time` and `function-lookup` |

The benchmark harness's parse items run at `GOGC=100` through
`benchrun -helper`, while the CLI runs at 200, so those cold figures read
15–25% higher than the CLI would.
Round 4 measured it: within noise at 1 and 10 MB, and about 8–15% more CPU
at 100 MB. The helper now runs as the CLI does (see round 4).

## Round 4: efficiency without an API change (`1cfeebd`)

Five lanes, each measured A/B against `1cfeebd` with allocation counts and
getrusage CPU (the lanes ran together, so wall times are noisy). Every
landed change keeps every workload output byte-identical, and QT3, XSLT
2.0/3.0, RELAX NG, the DocBook corpus and XSpec keep their counts and failing
names.

### Landed

| Change | Commit | Measured |
|---|---|---|
| Profile-guided optimisation: `cmd/go-xml/default.pgo`, regenerated by `tests/pgo.sh` from every workload family | `b83418a` | cold CLI CPU −2 to −3%, warm loops −3 to −5% (DocBook, XMark, XRechnung); XSD and RELAX NG within noise; binary +98 KB, build time unchanged. Programs embedding the library gain only with their own profile |
| `//@a` walked in one pass, as S8 did for `//name` | `06c0df1` | CEN (21 such checks per invoice) CPU −5 to −8%, bytes −6% |
| Collation language matchers built on first use | `18ef0f0` | package init 0.65 → 0.51 ms; CLI cold start −0.15 ms |
| html attribute escaping copies printable ASCII through | `eac3823` | XRechnung stage 2 allocations −53%, CPU −14% |
| Serializer: the encoding name lower-cased once; names, bindings and attribute values written in pieces over one binding stack | `935e10e`, `aefdaea` | XRechnung stage 2 105,617 → 45,208 allocations per invoice with `eac3823` (−57%); CEN −2.6% |
| A path step over one node keeps its result instead of copying it into the path's accumulator; a relative path starts from the context item without boxing it | `d077c5a`, `3fc9f9e` | allocations CEN −10.7%, Peppol −10.2%, XMark −5.7%, DocBook −5.3%, XRechnung −2.0%; CPU CEN −4 to −7%, DocBook −2 to −4%, the rest within noise |

Benchmark re-run at `f45068c` ([benchmark.md](benchmark.md)), warm against
Saxon, `eb14939` → now: Peppol 2.83× → 1.99×, XRechnung stage 1 3.96× →
3.0×, XRechnung stage 2 not comparable → 1.42×, DocBook 0.62× → 0.46× (36 of
40 items faster), XMark 1.09× → 1.00×; RELAX NG warm against Jing 2.7× →
0.60×; XSD against Xerces 0.75× → 0.73×. These include round 3.

### Measured and not built

| Idea | Finding |
|---|---|
| Compile XPath to closures | Interpretation overhead (AST dispatch, name resolution) is 2–4% of Schematron CPU, at most 10% counted generously. A direct child-axis loop, the largest removable piece, measured −2 to −3% at `GOGC=400` and nothing at 100. The cost is allocation and GC: atomisation, boxed booleans and strings, `name()` strings, context copies and namespace nodes in SVRL output. CEN is mostly real tree walking: Schematron re-walks the same child paths per assertion |
| Stream the principal result into the serializer | Result-tree building is ≤8–10% of allocations on Schematron and ~0.5% of bytes on DocBook. Every Schematron and XRechnung stylesheet sets `indent="yes"`, and the serializer must see an element's children before it can indent them, so the output could not be byte-identical without buffering the tree. The conditions under which streaming is observably identical (XSLT 3.0 §2.3.6, §26; Serialization 3.1 §2, §5.1.4) are in the lane notes |
| Pooled argument slices for leaf built-ins, `.` held in a pooled slot, `strSeq`/`boolSeq` in one allocation | Allocations −6 to −10% on CEN and XRechnung, CPU within noise (−1.7 to +1%). The commits that landed removed a copy and its bytes; these removed only small objects, which did not turn into CPU |
| `Compiled.scope` without a context copy | Worth −4 to −10% CPU on CEN and DocBook, but only by mutating the caller's context, and `Compiled` is safe for concurrent use. It needs the runtime context to carry the package's version and static host, or the context split (T20) |

### Linux

Measured at `38e6af9` in a 4-CPU Docker Desktop VM on the same M3 Pro,
alternating with macOS, both binaries from one compiler:

- `runtime.madvise` is 0–1.6% of samples on Linux in every workload,
  against 13–26% on macOS for parse, XMark and XSD.
- Cold CLI runs use 11–13% less CPU on Linux (parse-10mb 178 → 156 ms;
  XMark q1/q6/q7 82/92/103 → 71/80/93 ms).
- Warm in-process loops use 2–20% more on Linux (DocBook +20%, CEN +18%):
  GC marking takes the share that `madvise` takes on macOS. The VM adds a
  layer of address translation, so treat the Linux figures as an upper
  bound.
- macOS CPU profiles catch only ~80% of the CPU getrusage reports and put up
  to half their samples on scheduler wait and wake calls
  (`pthread_cond_wait`, `kevent`). Compare shares only after setting those
  aside, and use getrusage for totals.

### Benchmark harness

The parse and C14N helper (`benchrun -helper`) now runs as the CLI does:
`GOGC=200` unless `GOGC` is set, and a parse streamed from the open file with
the CLI's default options plus `MaxBytes: -1`. The warm loops still parse
with `ParseString` at the harness's own `GOGC`, which is what a library
caller owning its GC settings pays.

### For tier 3 (v2)

`docs/audits/xdm-improvement-suggestion.md` was checked against the parser.
Its first two phases are already in place: chunked node arenas with doubling
chunk sizes (bigger chunks measured no gain in round 2), exact-size child and
attribute slices from shared arrays, per-document name interning, a text
accumulator that sets each value once, and numbering while parsing. What it
adds to the v2 plan:

1. Two representations behind one node interface: a compact, immutable,
   index-based parsed tree, and a mutable result tree with namespace maps
   shared between elements (namespace nodes are 86% of CEN's SVRL output).
2. Corpus statistics (children and attributes per element, node kinds)
   before choosing field layouts and widths.

## Round 5: the remaining non-v2 items (`bd32eff`)

Four lanes on what rounds 3 and 4 left open without an API change, measured
the same way (allocations and getrusage CPU, A/B against `bd32eff`). Every
landed change keeps every workload output byte-identical and every suite at
its counts and failing names.

| Change | Commit | Measured |
|---|---|---|
| `//x[p]` from a document root: XSLT remembers, per parsed document, which nodes have an `x` child, and runs the predicate step only over them | `b5864c4` | Root paths were 37.9% of CEN's transform time, 14.9% repeated. CEN CPU −18 to −23%, bytes −19%; others unchanged. Only trees from `xdm.Parse`; capped at 2^20 entries per transform; released with it ([options](options.md#source-trees-are-read-only-during-a-transform)) |
| `name()` comparisons (`name(.) = name(current())`, against a string literal) match prefix and local name without building strings | `738c1fb` | XRechnung stage 1 allocations −53%, CPU −20% (KoSIT's `xr:src-path`) |
| Two shared `xs:boolean` values from comparisons, logical operators, `instance of` and the boolean built-ins | `7dce099` | allocations CEN −8.1%, Peppol −5.9%, XRechnung −11.6%; CPU −4.8%, −5.1%, −7.5% |
| Serializer text and escaped attribute values written in runs | `ca3e6a1`, `3cf29c3` | XMark q2+q10 allocations −6.9%, CPU −3.8%; XRechnung HTML allocations −4.6%. No per-node or per-character allocation is left in the serializer |
| T23: RELAX NG patterns interned per validation and the four derivatives remembered, from the 1,000th element | `77cdd65` | the 40-document benchmark and compile unchanged; 1.1 MB `table-cals.049` 26.5 → 18.2 ms, allocations −76%; all 589 corpus documents 48.8 → 42.7 ms, allocations −48%. `MaxPatternSize` fires on the same inputs |

Shared booleans were blocked by a real bug. `xsl:iterate`, `xsl:merge`,
`xsl:sort` and `xsl:copy` with `select` did not clear the current template
rule (XSLT 3.0 §6.8), and only a pointer comparison of the focus item kept
`xsl:next-match` inside them from running the next rule. Fixed first in
`4d31869`. Saxon 12.10 agrees on every case except `xsl:merge-action`,
where it runs the next rule; go-xml follows §6.8, as it already did for
`xsl:analyze-string` and `xsl:key`, where Saxon also differs.

Measured and not landed:

| Idea | Finding |
|---|---|
| Relative-path memo (`cac:A/cac:B` across assertions) | repeats are ~2.4% of CEN; allocations −0.3%, bytes +5%, no CPU gain |
| `fuseDescendant` without a step allocation per evaluation | −0.13% allocations on CEN, CPU flat |
| `Compiled.scope` with the runtime carrying the package's version and static host | 0 copies skipped on any workload: a top-level evaluation is recognised by `StaticNamespaces == nil`, and CEN alone has 1,238 distinct namespace resolvers. Skipping the install changes what host functions see in the exported `StaticNamespaces`: v2 (T20) |
| A schema-wide RELAX NG derivative memo | 3.5× faster warm only because the benchmark re-validates the same documents; no gain on unseen ones, twice the bytes |

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
