# Profiling: what is left to fix

This document lists the performance work that is still open on v2, the
ideas that were measured and rejected, and how to measure. It was measured on
the `v2` branch at `ef76ae2c`; the Go code has not changed since apart from
the version string.

Fixes that have landed are not repeated here. Each is in
[CHANGELOG.md](../CHANGELOG.md) and the git history. The six earlier
profiling rounds (T1–T23, R1–R13, S1–S11, the round 4–6 changes, the v2
context split and node records, and the v2 allocation cuts) are in the
previous version of this file: `git show aefbd8a5:docs/profiling.md`.

## Where go-xml stands

Warm time, go-xml over the reference engine (geometric mean). Each version is
compared with the reference times from its own benchmark run
([benchmark](benchmark.md)). The last column is the projection if the open
fixes V1–V11 below land.

| Workload | Reference | v1 | v2 | Projected after V1–V11 |
|---|---|---:|---:|---:|
| DocBook xslTNG | Saxon-HE | 0.46× | 0.37× | about 0.27× (with V4) |
| DocBook `ptoc.001` | Saxon-HE | 1.49× | 1.17× | about 1.0× |
| Peppol Schematron | Saxon-HE | 1.99× | 1.31× | about 1.24× |
| XRechnung stage 1 | Saxon-HE | 3.00× | 1.77× | about 1.65× |
| XRechnung stage 2 | Saxon-HE | 1.42× | 1.23× | about 1.0× |
| XMark q1–q20 | Saxon-HE | 1.00× | 0.97× | not projected |
| XSD catalogs | Xerces-J | 0.73× | 0.69× | not projected |
| RELAX NG DocBook 5.2 | Jing | 0.60× | 0.75× | not projected |
| Parse 1/10/100 MB | `encoding/xml` | 0.54× | 0.57× | not projected |

On XSLT, most of the time left goes to the garbage collector and the
allocator: 26–47% GC marking and 14–28% `mallocgc`, from Linux profiles. CPU
follows allocation volume. At factor 0.1, XMark is parse-bound: the parse
takes 48–50 ms of every query except q10–q12.

### The three apparent regressions

| Benchmark figure | Finding |
|---|---|
| DocBook compile 55 → 77 ms | **Real.** Bisected to `a81dff28` (the 40-byte records). The stylesheet checks walk ancestors for the version attribute (`effectiveForwards`, `moduleAtLeast30`, `xpathVersionAt` …). Each `Attr` lookup now goes through the name table instead of an inlined field read, which costs about 5 ms. The static-phase copies add about 2 ms, and the smaller heap runs more GC cycles (115 against 84 per 20 compiles). Fixed by V7 (landed, see [Landed](#landed)) |
| RELAX NG warm 0.60× → 0.75× Jing | **Mostly Jing.** The same Jing jar ran 15% faster in this run, which accounts for about 73% of the change. go-xml's own share is +4–6% on small documents, from per-document parse set-up (V18). Long documents are 18–40% faster than v1 |
| Parse warm 0.54× → 0.57× `encoding/xml` | **Not the parse.** Parse wall time is level with v1 and its CPU is 39% lower. The C14N write that the item includes was 14% slower. V11 (landed, see [Landed](#landed-in-the-v2-fix-round)) cut the write 10–12% and the item 3.7%, from the output buffer rather than the accessors |

### Two regressions the ratios do not show

- **Typed validation** (`ValidateCopy`) costs 2.5–3.4× what v1's in-place
  annotation did. It makes two full per-node copies: the first to
  validate, the second to drop whitespace and add defaults. On top of that,
  three callers still copy the input first, as v1 had to. CLI
  `-validate strict` on a 2.5 MB catalog takes 11 ms on v1 and 38 ms on v2.
  Fixed by V2 (landed; see [Landed](#landed)).
- **XMark q10** evaluation is 55% slower. XQuery element content is copied
  twice per node (`xdm.Copy`, then `AppendNode`'s own copy), and
  `limitInherited` builds three maps per constructed element. Fixed by V1
  (`484cb4e8`, see [Landed](#landed-in-the-v2-fix-round)).

## Open fixes

Gains are measured on prototypes, A/B against `ef76ae2c`, unless marked
*estimate*. Every prototype kept its workload outputs byte-identical. None
was run against every conformance suite, so each fix needs the full suite
gate before it lands. "API" is the effect on the exported v2 API.

Ranked by benefit for the effort. V1–V11 come from the v2 profile, and V12
onward are what earlier rounds left open.

| ID | Fix | Cause | Where | Gain | Risk | API |
|---|---|---|---|---|---|---|

| V4 | Evaluate global variables on first use, held in one map scope instead of a chain of about 950 single-variable scopes | DocBook declares about 946 globals per transform and reads about 75. Evaluating them all is 31% of DocBook's wall time | `xslt/runtime.go:863`, `:905`, `:1166`; `xpath/context.go:915` | DocBook a further −13% (−26% with V5/V7); the map scope alone −3.5% | Medium–high: errors must surface through the variable lookup, where the prototype panics. Four XSLT 3.0 cases (error-0610d, higher-order-functions-070, variable-0118, variable-0120) and one 2.0 case regress in the prototype | None |

| V12 | Give the XSLT runtime's context the stylesheet's common static part, so `Compiled.scope` stops copying the context on top-level evaluations | The runtime's context has version 2.0, no static host and no namespaces, while every expression carries its own, so `scope` copies on each evaluation: 7.7% of CEN's bytes | `xpath/xpath.go:349` | PEPPOL −8%. CEN unchanged as built; its upper bound with per-expression "needs namespaces" flags is −9% bytes, −2.5% wall | Medium: host functions would see a different `StaticNamespaces` | Observable in `StaticNamespaces` |
| V14 | XSLT `xsl:sequence` (an element with a parent) and `xsl:copy-of` copy once, into the builder | `deepCopy` and then `AppendNode` copy again: the same double copy as V1 | `xslt/instructions.go:159–161`, `:333` | Not measured; q10's −40% shows the size of the effect | Medium: the pre-copy rebases and strips namespaces, and that has to move into the single copy | The V1 builder method |
| V15 | Temporary trees: move the parse-only `Tree` fields behind a pointer; start a fragment's first chunk smaller | A `Tree` is about 370 B and CEN builds about 250 per invoice (`NewFragment`, 4.4% of CEN bytes). Their first chunks (`Tree.alloc`) are another 6% | `xdm/record.go:69`, `:211`, `:286` | Up to about 10% of CEN bytes (*estimate*) | Low–medium. A smaller start must not cost CPU the way the string-arena small start did (see rejected) | None |
| V16 | `FuncCall.Eval`'s argument slice | One slice per call: 4% of CEN bytes | `xpath/eval.go:693` | *Estimate.* Pooling measured −6 to −10% allocations but no CPU on v1 (see rejected). On v2, allocation drives GC cycles, so re-measure before building | Low | None |
| V17 | Slot-indexed frames for local variables (Saxon's design) instead of the `WithVar` scope chain | `WithVar` plus runtime copies are about 24% of bytes on the slow DocBook items; `lookupVarPlain` is 2.5–4.5% of CPU | `xpath/context.go`, `xslt/runtime.go:339` | DocBook about −10 to −15% (*estimate*) | High effort: slots resolved at compile time across XPath and XSLT | Host variable binding through the context must keep working |
| V18 | Small-document parse set-up | Every document starts record chunks of 4, 8, 16 … records, plus offset chunks and text-store blocks. This makes parsing small documents 11% slower (geomean) and is go-xml's +4–6% in the RELAX NG ratio | `xdm/record.go` | No fix prototyped | Low–medium: larger first chunks retain more heap per small tree | None |
| V19 | Element-name index per tree for `descendant::name` | `//name` walks the subtree. The index was blocked while `xdm.Node`'s `Children`, `Name` and `Parent` were exported fields. v2 removed them | `xdm`, `xpath` | XMark q6/q7/q14 eval (*estimate*, from before the direct walk landed; XMark at 0.1 is parse-bound, so the warm gain is bounded by the eval share) | Medium: the index must stay valid while a tree is built | None |

The projections in [Where go-xml stands](#where-go-xml-stands) scale V1–V11
onto the benchmark. V4 is the only fix there that moves DocBook from 0.37×
to 0.27×.

## Landed in the v2 fix round

A/B against `0ff09c65`, getrusage CPU and allocation counts, medians of three
alternated runs (four for parse).

| ID | Commit | Measured |
|---|---|---|
| V1 | `484cb4e8` | XMark q10 eval −38% at 0.1 (−32% at 0.01), allocations −43%; q13 eval −37%, allocations −27%; q1–q20 allocations −12.7% |
| V3 | `7186eb0e` | Parse 1 MB −2.2%, 10 MB −2.4%, `xp-striding` catalog −3.2%; XMark parse phase −6.0% at 0.1. Allocations +0.3% (one cache per indexed tree) |
| V6 | `ae4f4d1b` | `table-cals.049` validate 18.8 → 12.7 ms CPU (−33%); 589-document corpus −18% (−5% with parse); 40-document set flat. Verdicts and messages identical on all 589 documents; pattern bytes +2–24% |
| V8 | `5509745a` | `HTTPResolver` is `xsdnet.HTTPResolver`; `goxml_nohttp` removed. CLI binary −2.0 MB (−11%); small XSD validation 4.2 → 2.9 ms CPU, RSS 12.5 → 7.7 MB. Only `xsd/xsdnet` links `net/http` |
| V9 | `c5184cb8` | `default.pgo` regenerated on v2: warm CPU −1 to −6% (DocBook −1 to −2%, CEN −3 to −6%, RELAX NG −3 to −7%, XSD level); cold CLI within ±1.5%. `MemProfileRate = 0` was not added: the CLI does not link `runtime/pprof`, so the linker already turns sampling off (forcing it on costs up to 10% on DocBook). The −8% in the V9 row came from a harness that links `runtime/pprof` |
| V2 | `18c35f6`, `b48adaf` | XSD catalog warm `ValidateCopy` 102.2 → 53.5 ms CPU per pass, 74.8 → 53.5 MB; `xp-striding` 38–45 → 18–19 ms (v1 22), unicode-90 20.9 → 11.7 ms. 2.5 MB catalog: CLI `-validate strict` 63.0 → 43.2 ms CPU, `xsl:source-document validation="strict"` 77.9 → 54.2 ms, XQuery `validate strict {}` 68.5 → 44.9 ms. The clone is behind `internal/xdmclone`; no exported API. Still per node: a copy whose assessment added namespace declarations, and the ancestors-only first copy of a node below the document element. The source-document and merge pre-copy is kept for modes other than strict-with-schema and for trees with a DTD or positions; XQuery `validate` keeps its copy (made in bulk), since dropping it would change the result for an operand with a parent or a document-node operand |
| V10 | `e48af61` | XSD catalog warm `Validate` 18.9 → 18.1 ms CPU, unicode-90 5.29 → 4.80 ms, 508 → 19 KB and 387 → 316 allocations per pass |
| V5 | `462ee9a2` `NameTest.Matches` local name first; `35cd4d60` per-transform fields to `transformState` (runtime copy 176 → 112 B); `16f3d909` one focus context per predicate, behind a static capture check; `f2c08b2e` strip-space answer remembered per package and name (cap 4,096); `db7e1ba3` no position stamp on `next-iteration`/`break`; `40dbca21` `stripAnnotations` skips a never-typed tree (`Node.TreeHasTyping`) | XRechnung stage 1 −12% allocations, −10% CPU (focus reuse); stage 2 −9% CPU (untyped skip), −6% bytes; DocBook items −5% CPU and −8% bytes from the field move, −6% CPU and −6% allocations from the focus reuse; the strip pass 450 → 170 µs per DocBook document; an included `xsl:iterate` −10% per iteration; `Matches` −9% per call (within noise on the workloads) |
| V7 | `23ace644` version-attribute walks remembered per Compile, off during the static phase; `be9eb58e` `FileResolver` remembers `EvalSymlinks` per path (cap 4,096; `os.Root` still confines at open) | DocBook compile −5% CPU from the walks, −7% CPU and −4% allocations from `EvalSymlinks` (32% → 6% of the compile profile on macOS); CEN compile −3.5% |
| V11 | `63155c73` C14N appends to its own 64 KiB buffer instead of calling a `bufio.Writer` per token; `da27a7cd` the serializer's `element` has no defers (they forced the runtime's deferred-call path on every element) and calls `WriteString` directly | Against `257ade0d`, 3 alternated rounds: C14N 1 MB −9.6%, 10 MB −11.9% CPU (exclusive −10.8%, −10.5%); parse + C14N −3.7% at 1 and 10 MB; one allocation fewer, +16 KB buffer per call. Serializing a finished result: XMark q10 −22%, q2 −20%, XRechnung stage 2 −8%, DocBook −18%; allocations unchanged. No xdm API was added: a record walker measured slower than the accessors (see rejected) |
| V13 | `e09f1060` the runtime holds its selection by pointer, allocated with the copy that selects it (runtime copy 112 → 64 B) | DocBook items −5.5% bytes, −3.8% CPU; XRechnung stage 2 −4.4% bytes, −2% CPU; allocation counts unchanged. Allocating the selection separately added 1.8% allocations and was dropped |

V1 and V3 together: XMark q1–q20 CPU −8.8% at 0.1 and −7.7% at 0.01; q10
−33%, q13 −10%. The prototype's −40% q10 eval holds; its parse gains
(−5.5 to −7%) measured smaller here, with four lanes sharing the machine.

V5, V7 and V13 together, against `0ff09c65` (7 alternated rounds, CPU and allocations per
pass): CEN −4.0% CPU, −1.0% allocations; Peppol −6.8%, −2.4%; XRechnung
stage 1 −11.0%, −11.7%; stage 2 −14.6%, −1.4%; DocBook `ptoc.001`,
`indexterm.001` and `chapter.003` −17.1%, −7.5% (bytes −21%); all 42 DocBook
items −18.3%, −4.5%; DocBook compile −11.0%, −4.0%; CEN compile −1.9%.

## Measured and rejected

Each idea here was prototyped and measured, so there is no need to redo it.
Reopen one only if the reason no longer holds.

| Idea | Why not |
|---|---|
| Compile XPath to closures | Interpretation (AST dispatch, name resolution) is 2–4% of Schematron CPU. A direct child-axis loop, the largest removable piece, measured −2 to −3% at `GOGC=400` and nothing at 100. The cost is allocation, not dispatch |
| Stream the principal result into the serializer | Building the result tree is ≤8–10% of allocations on Schematron. Every Schematron and XRechnung stylesheet sets `indent="yes"`, and indenting needs an element's children first, so the output could not stay byte-identical without buffering |
| Pooled argument slices for leaf built-ins; `.` in a pooled slot; `strSeq`/`boolSeq` in one allocation | Allocations −6 to −10%, CPU within noise on v1. Small objects did not turn into CPU (V16 asks for a re-measure on v2) |
| `Compiled.scope` without a copy, by mutating the caller's context | −4 to −10% CPU, but `Compiled` is safe for concurrent use. V12 is the version that does not mutate |
| `Compiled.scope` with the runtime carrying the package's version and static host (v1) | Skipped 0 copies: a top-level evaluation is recognised by `StaticNamespaces == nil`, and CEN alone has 1,238 distinct namespace resolvers |
| Compile-time function resolution (T17) | After struct keys, a lookup allocates nothing and is about 3% of CPU. A cache that stayed correct when a library changed after first use cost more allocations than it saved. The call-site cache (S1) covers it |
| Relative-path memo (`cac:A/cac:B` across assertions) | Repeats are about 2.4% of CEN; allocations −0.3%, bytes +5%, no CPU gain |
| `fuseDescendant` without a step allocation per evaluation | −0.13% allocations on CEN, CPU flat |
| `//x[p]` fusion into `descendant::x[p]` | Not equivalent: with nested `x`, a different error can win. The per-document root-path index covers the case |
| Schema-wide RELAX NG derivative memo | 3.5× faster warm only because the benchmark re-validates the same documents. No gain on unseen documents, and twice the bytes |
| Bitset NFA states in XSD 1.1 restriction checks | The checks are 6–9% of an XSD 1.1 load, so the −15–20% estimate is out of reach |
| Bigger node chunks; 1,024-node chunks | No gain; +0.28 GB peak RSS at 100 MB |
| String arena starting small | Retained heap −20–37% on small documents, but DocBook +5% CPU: the smaller live heap makes the collector run more often at the same `GOGC` (85 cycles against 77). Reverted in `629cf48` |
| Chunked result nodes | −0.9% allocations at most, +0.4–0.7% bytes |
| A memory limit (`GOMEMLIMIT`) instead of `GOGC=200` in the CLI | A limit under the live heap costs 10–25× CPU, and the CLI cannot know the live size |
| Pausing GC around parse and compile instead of `GOGC=200` | Cold CPU summed over four workloads: 812 ms paused against 748 ms at `GOGC=200`, and pausing needs six wrapped call sites (`5fbea36`) |
| SWAR scan in the tokeniser's `text()` | No gain (round 3, and again on v2): it is already a tight table loop |
| 64 KiB file read buffer | No gain |
| A record walker for C14N and the serializer (V11 as proposed) | The accessors and the `Children`/`Attrs` iterators already inline. Walking the 10 MB document reading every name and value: recursive accessors 3.7 ms, a pre-order walker as a range-over-func iterator 5.4 ms (its yield is an indirect call per node), an exported cursor 4.1 ms. A raw chunk scan inside `xdm` takes 1.4 ms but has no enter/leave and is not reachable from another package without handing out records. The C14N gap to v1 was the per-token `bufio.Writer` calls instead |
| Interning `nodeTyping` per tree | Bytes −27%, wall +25%: hashing the key on every typing write costs more than the allocation it saves |
| Typing chunks under 32 KB | No gain. The `madvise` time in the typed path is macOS heap growth, not chunk size |
| Counting child elements lazily; testing `particleAcceptsEmpty` after the cheap checks (XSD) | Within noise |
| Keyed attribute sort in C14N | No gain |

## Correctness

None open. Every bug found while profiling has been fixed and is listed in
[CHANGELOG.md](../CHANGELOG.md). The v2 profile found no new bug: XSD and
RELAX NG verdicts and messages are identical to v1 (11/11 catalogs, 589/589
RELAX NG documents), and every prototype kept its outputs byte-identical.

One deliberate difference remains: inside `xsl:merge-action`, go-xml clears
the current template rule as XSLT 3.0 §6.8 requires, while Saxon 12.10 still
runs the next rule.

## Method notes

- **Harnesses** copy the benchmark's warm loop: compile once, then
  parse → transform/query/validate → serialise to `io.Discard`. They use the
  same parse options, resolvers and parameters as the benchmark items. The
  `benchrun -helper` used for parse and C14N runs as the CLI does (`GOGC=200`,
  streamed parse). The warm loops parse with `ParseString` at the harness's own
  `GOGC`.
- **Allocation counts are exact** and are the most reliable figure. CPU comes
  from getrusage. Wall times are noisy when lanes run together, so treat
  differences under about 3% as noise.
- **A/B runs alternate** the two builds, back to back, with medians of several
  runs. A prototype is built on a copy of the tree (`git archive` or a
  worktree), never in the checkout. It is checked by diffing every workload
  output against the unmodified build. Run the affected unit tests and the full
  conformance gate before landing.
- **Retained heap** is measured after `runtime.GC` with the input dropped. A
  cheaper measurement that keeps the input under-counts by about the input
  size.
- **macOS profiles under-count.** They catch about 80% of the CPU that
  getrusage reports, and put up to half their samples on scheduler waits
  (`pthread_cond_wait`, `kevent`). On v2 they also put 25–40% of XSLT samples
  on `EvalSymlinks` system calls, which an A/B showed cost no warm time. Use
  getrusage for totals, and Linux profiles for attribution.

### Linux

Measured in a 4-CPU Docker Desktop VM on the same M3 Pro, alternating with
macOS, with both binaries from one compiler:

- `runtime.madvise` is 0–1.6% of samples on Linux, against 13–26% on macOS for
  parse, XMark and XSD. On macOS, Go's darwin runtime calls
  `madvise(MADV_FREE_REUSE)` every time it reuses a heap page.
- Cold CLI runs use 11–13% less CPU on Linux.
- Warm in-process loops use 2–20% more CPU on Linux, because GC marking takes
  the share that `madvise` takes on macOS.
- The VM adds a layer of address translation, so read the Linux figures as
  an upper bound.
