# Benchmark: go-xml against established engines

How go-xml compares with Saxon-HE, BaseX, Jing, Xerces-J, libxml2 and Go's
`encoding/xml` on real workloads: DocBook rendering, e-invoice validation and
visualisation, XMark queries, schema validation, parsing and canonicalisation.
Each comparison is timed only after the engines are shown to produce the same
output.

**The short version.** Cold, go-xml finishes first everywhere except small XSD
validations, where xmllint's C start-up is 1.9× quicker; it starts in
milliseconds where a JVM takes 0.5–1 s. Warm, in a long-running process, it is
now faster than Saxon on DocBook (34 of 40 documents) and faster than BaseX on
XMark, about level with Saxon on XMark, and faster than Xerces on XSD. Saxon
still leads by 2.8× to 4× on Schematron-shaped e-invoice stylesheets. Memory
per parsed byte is still above libxml2's.

Measured 2026-10-08 on the tree at `eb14939`, with the machine otherwise idle.
The first run, at `22f4b04` before the work in [profiling.md](profiling.md),
is given alongside where it shows what changed.

## Contents

- [Machine and engines](#machine-and-engines)
- [Method](#method)
- [Results at a glance](#results-at-a-glance)
- [XSLT: DocBook xslTNG](#xslt-docbook-xsltng)
- [XSLT: e-invoicing (Peppol, XRechnung)](#xslt-e-invoicing)
- [XQuery: XMark](#xquery-xmark)
- [Schema validation: XSD and RELAX NG](#schema-validation)
- [Parsing and Canonical XML](#parsing-and-canonical-xml)
- [Memory](#memory)
- [Where the break-even is](#where-the-break-even-is)
- [What could not be measured, and why](#what-could-not-be-measured)
- [Correctness findings](#correctness-findings)
- [Reading these numbers](#reading-these-numbers)

## Machine and engines

| | |
|---|---|
| Machine | Apple M3 Pro, 12 cores, 18 GB, macOS (arm64) |
| Go | go1.26.4 |
| Java | OpenJDK 23.0.2 |

| Engine | Version | Used for |
|---|---|---|
| go-xml | this tree (`eb14939`) | everything |
| Saxon-HE | 12.10 | XSLT, XQuery |
| BaseX | 12.4 | XQuery |
| Jing | 20241231 | RELAX NG |
| Xerces-J | 2.12.2, XML Schema 1.1 build | XSD |
| libxml2 (`xmllint`, `xsltproc`) | 2.9.13 / libxslt 1.1.35 | parse, C14N, XSD 1.0, RELAX NG |
| `encoding/xml` | Go standard library | parse baseline |

Every engine was downloaded at a pinned version and checked against a SHA-256
before use. Every input is pinned too: public corpora by version and checksum,
generated documents by a fixed seed.

## Method

**Correctness before timing.** Each workload item is run once on every engine
first. Outputs are compared after the item's declared normalisation:
- byte-equal;
- whitespace-collapsed, where indentation width differs by design;
- re-canonicalised XML;
- for validators, the valid or invalid verdict.

Things that differ by design are stripped before comparing: timestamps,
processor names in reports, absolute file URIs. An item where go-xml and the
reference engine disagree is **not timed for that pair**. It is reported under
[Correctness findings](#correctness-findings) instead. Every number below
compares two engines that produced the same answer.

**Two modes, never mixed.**

- **Cold**: one operating-system process per run. Each run covers start,
  compile, run, and serialise to a file. Two untimed warm-up runs come first,
  then ten timed ones. The figures are the median wall time and the peak
  resident set size (RSS). This is what a CLI call or a fresh container
  pays.
- **Warm**: compile once, then run the items in a loop inside one process.
  There is an untimed warm-up of a fifth as many passes, then 30 timed passes,
  reporting the median per item.
  - **go-xml:** a Go loop through the public API.
  - **JVM engines:** a small Java harness using each engine's API (Saxon
    s9api, BaseX, Jing, Xerces), so the JIT is fully warmed.
  - **libxml2 tools:** cold only, since there is no in-process harness for
    them.

  Warm mode is what a long-running service pays per document. Compile time is
  measured separately in warm mode.

**Ratios** are go-xml's time divided by the reference's. Below 1 means go-xml
is faster. Per-workload figures are the geometric mean over the items both
engines agreed on, with the range across items.

**go-xml's flags** match what its own test runner uses for the same corpus
(`-allow-doctype`, `-allow-unparsed-text`, `-allow-dir`). The library refuses
these features by default; turning them on does not change its speed.

## Results at a glance

Ratios are go-xml's time over the reference's, geometric mean over the items
both agreed on; below 1 means go-xml is faster.

| Workload | Items timed | Reference | Cold | Warm | Warm at `22f4b04` |
|---|---:|---|---:|---:|---:|
| DocBook xslTNG → XHTML5 | 40 / 42 | Saxon-HE | **0.09×** (11× faster) | **0.62×** (1.6× faster) | 2.54× |
| Peppol BIS Schematron → SVRL | 18 / 18 | Saxon-HE | **0.04×** (24× faster) | 2.83× slower | 20.4× |
| XRechnung UBL → xr:invoice | 8 / 8 | Saxon-HE | **0.04×** (23× faster) | 3.96× slower | 9.6× |
| XRechnung xr:invoice → HTML | 0 / 8 | Saxon-HE | — | — (see [findings](#correctness-findings)) | — |
| XMark q1–q20, 1 and 11 MB | 40 / 40 | Saxon-HE | **0.08×** (12× faster) | 1.09× slower | 5.2× |
| XMark q1–q20, 1 and 11 MB | 40 / 40 | BaseX | **0.06×** (17× faster) | **0.42×** (2.4× faster) | 1.9× |
| XSD 1.0/1.1 (catalog schemas) | 11 / 11 | Xerces-J | **0.05×** (19× faster) | **0.75×** (1.3× faster) | 1.8× |
| XSD 1.0/1.1 (catalog schemas) | 11 / 11 | xmllint | 1.9× slower | — | — |
| RELAX NG (DocBook 5.2) | 40 / 40 | Jing | **0.16×** (6.2× faster) | 2.7× slower | 4.7× |
| RELAX NG (DocBook 5.2) | 40 / 40 | xmllint | 1.01× (level) | — | — |
| Parse 1 / 10 / 100 MB | 3 / 3 | `encoding/xml` | **0.67×** | **0.55×** | 1.7× |
| Parse 1 / 10 / 100 MB | 3 / 3 | xmllint | **0.66×** | — | — |
| Canonical XML 1 / 10 MB | 2 / 2 | xmllint | **0.70×** | — | — |

The cold column at `22f4b04` was 0.17×, 0.10×, 0.08×, 0.27×, 0.21×, 0.08×,
2.9×, 2.7×, 17×, 1.8×, 1.75× and 1.6× in the same order.

Compile time (warm mode, median per workload):

| Workload | go-xml | at `22f4b04` | Reference |
|---|---:|---:|---:|
| DocBook xslTNG (`docbook.xsl` and its modules) | 56 ms | 101 ms | Saxon 827 ms |
| Peppol Schematron (compiled XSLT 2.0; CEN 31, PEPPOL 13) | 22 ms | 38 ms | Saxon 582 ms |
| XRechnung UBL → xr | 6.7 ms | 19 ms | Saxon 464 ms |
| XMark query | 0.9 ms | 0.9 ms | Saxon 222 ms, BaseX 224 ms |
| XSD catalog schemas | 3.3 ms | 9.7 ms | Xerces 98 ms |
| DocBook 5.2 RELAX NG (608 KB) | 26 ms | 496 ms | Jing 108 ms |

## XSLT: DocBook xslTNG

DocBook xslTNG 2.6.0 (`docbook.xsl`, XSLT 3.0) renders 42 of its own test
documents to XHTML5. They were chosen to span size (186 B to 54 KB) and
feature areas: books, sets, reference entries, tables, lists, synopses,
callouts, glossaries, bibliographies, indexes, footnotes, localisation and
dates.

| | go-xml | at `22f4b04` | Saxon-HE |
|---|---:|---:|---:|
| Cold, median over items | 92 ms | 165 ms | 1,065 ms |
| Warm, median over items | 16.6 ms | 68.6 ms | 32.4 ms |
| Compile | 56 ms | 101 ms | 827 ms |
| Peak RSS, cold (median) | 86 MB | 104 MB | 273 MB |

Cold, go-xml is faster on every item (0.08× to 0.16×). Warm, it is faster on
34 of 40 items (0.34× to 2.51×). The items it still loses are the large ones
with index or table-of-contents work:

| Item | go-xml warm | at `22f4b04` | Saxon warm | Ratio |
|---|---:|---:|---:|---:|
| `table-html.001` | 9.5 ms | 44.1 ms | 28.3 ms | 0.34× |
| `blocks.002` | 11.6 ms | 48.0 ms | 30.4 ms | 0.38× |
| `book.001` | 42.0 ms | 182 ms | 39.5 ms | 1.06× |
| `indexterm.001` | 76.0 ms | 368 ms | 49.4 ms | 1.54× |
| `chapter.003` | 95.7 ms | 428 ms | 51.1 ms | 1.87× |
| `ptoc.001` | 125 ms | 493 ms | 57.3 ms | 2.19× |
| `epub.001` | 98.2 ms | 231 ms | 39.1 ms | 2.51× |

## XSLT: e-invoicing

**Peppol BIS Billing 3.0.** The CEN EN 16931 and Peppol Schematron rule sets,
compiled to XSLT 2.0 by SchXslt 1.10.1, run against each example invoice and
produce SVRL.
- **Cold:** go-xml is 24× faster (30 ms against 683 ms; 78 ms at `22f4b04`).
- **Warm:** Saxon finishes an invoice in 0.9–2.9 ms; go-xml takes 2.2–9.4 ms,
  2.4× to 3.5× slower (12.8× to 25.8× at `22f4b04`).

These stylesheets are hundreds of independent XPath assertions over one small
document. That is the shape where Saxon's bytecode generation and JIT pay off
most, and where go-xml's per-call allocation and collection still show
([profiling](profiling.md#round-2-after-the-fixes-73a2963)).

**XRechnung, stage 1** (KoSIT `ubl-invoice-xr.xsl`, XSLT 2.0): UBL invoices from
the KoSIT test suite to the intermediate `xr:invoice` XML.
- **Cold:** 22 ms against 535 ms (23× faster; 44 ms at `22f4b04`).
- **Warm:** 9.7 ms against 2.4 ms (4.0× slower; 9.6× at `22f4b04`).

**XRechnung, stage 2** (`xrechnung-html.xsl`): no item could be timed. See
[Correctness findings](#correctness-findings).

## XQuery: XMark

XMark q1–q20 over the auction documents at factors 0.01 (1 MB) and 0.1
(11 MB), with the auction document as the context item. All 40 items agree
byte-for-byte across the three engines after whitespace collapse.

At factor 0.1 (milliseconds):

| Query | go-xml warm | at `22f4b04` | Saxon warm | BaseX warm | go-xml cold | Saxon cold | BaseX cold |
|---|---:|---:|---:|---:|---:|---:|---:|
| q1 | 56 | 156 | 46 | 92 | 70 | 462 | 564 |
| q2 | 59 | 162 | 45 | 92 | 72 | 420 | 582 |
| q3 | 61 | 163 | 47 | 89 | 78 | 446 | 583 |
| q4 | 58 | 160 | 47 | 92 | 74 | 456 | 578 |
| q5 | 55 | 152 | 44 | 90 | 71 | 447 | 566 |
| q6 | 66 | 272 | 45 | 89 | 82 | 476 | 623 |
| q7 | 80 | 419 | 47 | 87 | 97 | 423 | 593 |
| q8 | **63** | 2,808 | 218 | 1,266 | 79 | 967 | 659 |
| q9 | **65** | 3,384 | 297 | 1,302 | 82 | 808 | 2,080 |
| q10 | 103 | 572 | 83 | 257 | 123 | 594 | 724 |
| q11 | **81** | 7,189 | 120 | 3,137 | 101 | 571 | 3,950 |
| q12 | 79 | 6,977 | 76 | 721 | 98 | 510 | 1,260 |
| q13 | 57 | 170 | 46 | 89 | 74 | 434 | 580 |
| q14 | 70 | 273 | 52 | 92 | 85 | 432 | 592 |
| q15 | 55 | 433 | 47 | 107 | 70 | 423 | 570 |
| q16 | 56 | 642 | 46 | 88 | 71 | 430 | 581 |
| q17 | 58 | 322 | 45 | 88 | 74 | 476 | 651 |
| q18 | 57 | 301 | 48 | 90 | 75 | 505 | 661 |
| q19 | 65 | 301 | 48 | 94 | 83 | 505 | 683 |
| q20 | 60 | 198 | 46 | 109 | 75 | 491 | 661 |

- **The value joins are now go-xml's strongest queries.** q8, q9 and q11 are
  0.22× to 0.68× Saxon's warm time and 20–40× faster than BaseX; at `22f4b04`
  they were 12× to 85× slower than Saxon. They run as hash or range joins
  ([profiling](profiling.md#round-2-implementation-status)).
- **The rest are 1.2× to 1.7× Saxon warm.** The floor of about 55 ms on the
  11 MB document is the parse, which every engine repeats per run; Saxon's is
  about 45 ms. go-xml's floor was about 150 ms at `22f4b04`.
- **Cold, every query is faster than both JVMs**: 5× to 12× against Saxon.

## Schema validation

**XSD.** The XSLT 3.0 test-catalog schema (144 KB, XSD 1.0 with `vc:`
attributes) and the QT3 catalog schema (78 KB), validating 11 real catalog
files from 3.6 KB to 2.5 MB. They stand in for UBL 2.1, whose schemas are not
in the test corpus. All three validators agree on every verdict.

| | go-xml | at `22f4b04` | Xerces-J 1.1 | xmllint |
|---|---:|---:|---:|---:|
| Cold, median | 7.1 ms | 11.5 ms | 175 ms | 3.7 ms |
| Warm, median | 0.77 ms | 2.8 ms | 0.99 ms | — |
| Compile | 3.3 ms | 9.7 ms | 98 ms | — |

Warm, go-xml ranges from 0.26× (faster, on small instances) to 1.9× slower
(the 2.5 MB `xp-striding` catalog, 20.9 against 10.9 ms). libxml2's C
validator is 1.5× to 2.3× faster than go-xml cold; about 3 ms of go-xml's 7 ms
is process start ([profiling](profiling.md#where-cold-time-goes)). xmllint
implements XSD 1.0 only.

**RELAX NG.** DocBook 5.2 (`docbook.rng`, 608 KB) over 40 DocBook test
documents, all 40 timed; at `22f4b04` go-xml rejected 7 valid ones (see
findings). 5 are rejected by all three validators and are timed because the
verdicts agree: `JFK_Inaugural` uses `dialogue`, which DocBook 5.2 does not
define, and four carry unexpanded `xi:include` elements.

| | go-xml | at `22f4b04` | Jing | xmllint |
|---|---:|---:|---:|---:|
| Cold, median | 28 ms | 494 ms | 174 ms | 28 ms |
| Warm, median per document | 0.09 ms | 0.14 ms | 0.03 ms | — |
| Compile | 26 ms | 496 ms | 108 ms | — |

Cold time is the grammar compile, now a quarter of Jing's and level with
xmllint overall. Validating once compiled is sub-millisecond; the warm ratio
against Jing (0.23× to 8.6×) is noise around very small numbers.

## Parsing and Canonical XML

Documents generated with a fixed seed at 1, 10 and 100 MB. Cold runs parse and
write Canonical XML 1.0 to a file, so the outputs can be checked against
each other. Warm runs parse to a tree (go-xml), or tokenise and re-emit to a
null sink (`encoding/xml`).

| Size | go-xml cold | at `22f4b04` | go-xml warm | encoding/xml cold | encoding/xml warm | xmllint cold |
|---:|---:|---:|---:|---:|---:|---:|
| 1 MB | 17 ms | 35 ms | 8.7 ms | 24 ms | 16 ms | 21 ms |
| 10 MB | 117 ms | 298 ms | 86 ms | 180 ms | 158 ms | 193 ms |
| 100 MB | 1,094 ms | 4,055 ms | 892 ms | 1,724 ms | 1,576 ms | 1,873 ms |

In both modes go-xml builds a full XDM tree (node identity, document order,
namespaces) and writes Canonical XML from it; warm runs write to a null sink.
That gives about **85–90 MB/s** cold for parse plus C14N, and about 110 MB/s
warm. `encoding/xml` reaches about 60 MB/s, but it only tokenises and re-emits
and builds no tree. xmllint parses and writes C14N at about 53 MB/s.

The 100 MB document needs `MaxBytes: -1`. go-xml's default 64 MB document
limit is a deliberate guard against untrusted input, and the CLI has no flag
to raise it.

Canonical XML (inclusive 1.0) of the 1 and 10 MB documents is now faster than
`xmllint --c14n` cold: 16 against 20 ms, and 110 against 190 ms.

## Memory

Peak RSS in cold runs (medians, with the maximum where it differs):

| Workload | go-xml | at `22f4b04` | Reference |
|---|---:|---:|---:|
| DocBook xslTNG | 86 / 145 MB | 104 / 122 MB | Saxon 273 MB |
| Peppol | 44 MB | 56 MB | Saxon 169 MB |
| XRechnung stage 1 | 33 MB | 32 MB | Saxon 127 MB |
| XMark (median / max) | 108 / 234 MB | 150 / 466 MB | Saxon 148 / 334 MB, BaseX 175 / 554 MB |
| XSD | 17 MB | 19 MB | Xerces 77 MB, xmllint 4 MB |
| RELAX NG DocBook | 37 MB | 188 MB | Jing 74 MB, xmllint 11 MB |
| Parse 10 MB | 245 MB | 379 MB | encoding/xml 35 MB, xmllint 142 MB |
| Parse 100 MB | **2.3 GB** | 3.5 GB | encoding/xml 209 MB, xmllint 1.4 GB |

For transforms go-xml uses a third to half of a JVM's memory. The CLI runs its
collector at `GOGC=200` unless `GOGC` is set, which trades some peak memory
for CPU: DocBook's largest item rose from 122 to 145 MB. For large documents
libxml2 is still leaner: the XDM tree costs about 23 bytes of RSS per byte of
input, 1.7× libxml2's. Closing that needs the node changes that wait for a v2
API ([profiling](profiling.md#fix-candidates-ranked)).

## Where the break-even is

From the cold and warm medians, a rough estimate of how many documents one
process must handle before the JVM's start and compile cost is paid back:

| Workload | Saxon/Xerces fixed cost | Per-document gap | Break-even |
|---|---:|---:|---:|
| DocBook xslTNG | ~1.0 s | go-xml faster warm | none: go-xml finishes first at any batch size |
| XMark, simple queries | ~0.4 s | ~10 ms | ~40 documents |
| XMark, value joins | ~0.6 s | go-xml faster warm | none |
| Peppol Schematron | ~0.68 s | ~2.5 ms | ~270 documents |
| XRechnung stage 1 | ~0.53 s | ~7 ms | ~70 documents |
| XSD catalogs | ~0.17 s | go-xml faster warm | none |

Below these batch sizes per process, go-xml finishes first. Above them, a warm
JVM does. These are estimates from medians, not measured crossovers; real
documents vary. At `22f4b04` the break-even was 25–30 documents for the XSLT
workloads and about 150 for XSD.

## What could not be measured

- **XMark at factor 1 (111 MB).** The CLI refused documents over 64 MB when
  this was run. Since `3727b35` it takes `-max-bytes -1`, and `-max-items -1`
  lets q11 and q12 hold their ~37 M items (about 3.5 GB); q11 then takes 3.3 s
  and matches Saxon. It has not been timed here.
- **XRechnung stage 2 (HTML).** No item agreed with Saxon in this run, so
  nothing was timed; the cause is fixed since (below).
- **xsltproc.** Every XSLT workload here is XSLT 2.0 or 3.0, and libxslt
  implements 1.0 only.
- **libxml2 warm mode.** There is no in-process harness for the C tools, so
  they appear in cold mode only.
- **UBL 2.1 XSD.** The UBL schemas are not part of the test corpus; the W3C
  catalog schemas stand in for them.

## Correctness findings

The agreement check found real bugs. Four were fixed before the first timed
run:

| Found by | Problem | Fix |
|---|---|---|
| Peppol (SchXslt output) | A `version="2.0"` stylesheet was refused `match="root()"` and other 3.0 pattern forms | `ca09e14`: a 3.0 processor applies the 3.0 pattern grammar |
| XRechnung HTML | A late `xsl:import` in a 2.0 stylesheet was `XTSE0200` | `04ddeea`: only a 2.0 processor applies that rule |
| DocBook 5.2 RELAX NG | Every `<ref>` recompiled its definition, so cost multiplied along chains and hit the 200,000-expansion limit | `eb6901e`: compiled once and shared |
| DocBook 5.2 RNC | A free-standing annotation element among definitions was refused | `197eaad` |

Fixed between the two runs:

- **RELAX NG: go-xml rejected 7 of 40 valid DocBook documents**
  (`bibliography.006`, `book.006`, `glossary.007`, `index.002`,
  `oxy-changemarkup.001`, `programlisting.004`, `xref.001`). Six came from
  whitespace between element children being matched as text (RELAX NG §6.2.7,
  `b44313c`); `xref.001` hit the derivative size bound because `choice` kept
  duplicate alternatives (`d8f0ac1`). All 40 verdicts now match Jing and
  xmllint.

Fixed after the second run, so stage 2 is still untimed above:

- **XRechnung HTML: the stylesheet's `<meta charset="UTF-8"/>` was dropped**
  under `include-content-type="no"` (`262be91`), and html indentation split
  inline elements across lines (`4457808`). With both fixed all 8 items agree
  with Saxon, once its `<!DOCTYPE HTML>` is stripped: the stylesheet gives no
  `html-version`, whose default XSLT 3.0 §26 leaves implementation-defined.

Differences that are not go-xml bugs:

- **DocBook `dates.001`:** the picture `At [h1]:[m01][P] on [F], …` gives
  `4:49pm on friday` in go-xml and `4:49p.m. on Friday` in Saxon. F&O 3.1
  §9.8.4.2 sets the default presentation modifier of both `F` and `P` to `n`,
  which is lower case. So go-xml's output is what the specification asks for,
  and Saxon's departs from it. The wording of the am/pm marker is
  implementation-defined.
- **DocBook `fit.001`:** Saxon fails on this item ("Cannot read image
  properties (no extension)"), so there is no reference output to compare
  against.

## Reading these numbers

- **One machine, one run.** Laptop timings vary about ±10–15% from run to
  run. Ratios under about 1.3× are not meaningful; the large ones are.
- **Cold includes everything a caller pays.** That is process start, compile,
  input parse and output write. For the JVM engines most of it is the JVM.
  This is the number for a CLI or per-request process, not for a server.
- **Warm favours the JVM, deliberately.** 30 passes after warm-up give the JIT
  everything it needs. That is the steady state of a long-running service.
- **go-xml re-parses each input in both modes**, as the other engines do. No
  engine was allowed to cache documents across passes.
- **The harness is not part of the repository.** The engine downloads,
  corpora and generated inputs are large, and none is third-party code this
  repository should carry. The method above is complete enough to rebuild it.
  The figures here come from the run recorded at the top of this page.
- **macOS, not Linux.** On macOS, Go's runtime re-commits every reused heap
  page with `madvise`, which costs 13–26% of CPU on parse-heavy work. Linux
  does not. In a 4-CPU Linux VM on the same machine, cold CLI runs used
  11–13% less CPU and warm in-process loops 2–20% more, with GC marking in
  `madvise`'s place. Bare-metal Linux has not been measured; expect cold
  parse and query times at or below these, and do not read the warm figures
  as Linux numbers ([profiling, round 4](profiling.md#linux)).
- **The parse and C14N helper runs as the CLI does** since round 4:
  `GOGC=200` and a streamed parse. Before that it ran at `GOGC=100` from an
  in-memory string, which read within noise at 1 and 10 MB and about 8–15%
  more CPU at 100 MB. The warm loops still parse with `ParseString` at the
  harness's own `GOGC`, as a library caller would.
