# Benchmark: go-xml against established engines

How go-xml compares with Saxon-HE, BaseX, Jing, Xerces-J, libxml2 and Go's
`encoding/xml` on real workloads: DocBook rendering, e-invoice validation and
visualisation, XMark queries, schema validation, parsing and canonicalisation.
Each comparison is timed only after the engines are shown to produce the same
output.

**The short version.** Cold, go-xml finishes first everywhere except small XSD
validations, where xmllint's C start-up is 1.9× quicker; it starts in
milliseconds where a JVM takes 0.4–1 s. Warm, in a long-running process, it is
faster than Saxon on DocBook (36 of 40 documents), level with Saxon on XMark
and 2.6× faster than BaseX, and faster than Xerces on XSD and Jing on RELAX
NG. Saxon still leads on the Schematron-shaped e-invoice stylesheets, by 1.4×
to 3×, down from 2.8× to 4×. Memory per parsed byte is still above libxml2's.

Measured 2026-10-08 on the tree at `f45068c`, after the round-3 and round-4
work in [profiling.md](profiling.md), with the machine otherwise idle. The
previous run, at `eb14939`, is given alongside; the first, at `22f4b04`
before any of the profiling work, is in this file's history.

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
| go-xml | this tree (`f45068c`) | everything |
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

| Workload | Items timed | Reference | Cold | Warm | Warm at `eb14939` |
|---|---:|---|---:|---:|---:|
| DocBook xslTNG → XHTML5 | 40 / 42 | Saxon-HE | **0.09×** (11× faster) | **0.46×** (2.2× faster) | 0.62× |
| Peppol BIS Schematron → SVRL | 18 / 18 | Saxon-HE | **0.04×** (25× faster) | 1.99× slower | 2.83× |
| XRechnung UBL → xr:invoice | 8 / 8 | Saxon-HE | **0.03×** (29× faster) | 3.0× slower | 3.96× |
| XRechnung xr:invoice → HTML | 8 / 8 | Saxon-HE | **0.03×** (37× faster) | 1.42× slower | not comparable |
| XMark q1–q20, 1 and 11 MB | 40 / 40 | Saxon-HE | **0.07×** (14× faster) | 1.00× (level) | 1.09× |
| XMark q1–q20, 1 and 11 MB | 40 / 40 | BaseX | **0.05×** (19× faster) | **0.39×** (2.6× faster) | 0.42× |
| XSD 1.0/1.1 (catalog schemas) | 11 / 11 | Xerces-J | **0.05×** (19× faster) | **0.73×** (1.4× faster) | 0.75× |
| XSD 1.0/1.1 (catalog schemas) | 11 / 11 | xmllint | 1.9× slower | — | — |
| RELAX NG (DocBook 5.2) | 40 / 40 | Jing | **0.15×** (6.6× faster) | **0.60×** | 2.7× |
| RELAX NG (DocBook 5.2) | 40 / 40 | xmllint | 0.96× (level) | — | — |
| Parse 1 / 10 / 100 MB | 3 / 3 | `encoding/xml` | **0.66×** | **0.54×** | 0.55× |
| Parse 1 / 10 / 100 MB | 3 / 3 | xmllint | **0.66×** | — | — |
| Canonical XML 1 / 10 MB | 2 / 2 | xmllint | **0.72×** | — | — |

The cold column at `eb14939` was 0.09×, 0.04×, 0.04×, —, 0.08×, 0.06×, 0.05×,
1.9×, 0.16×, 1.01×, 0.67×, 0.66× and 0.70× in the same order. The parse and
C14N cold figures now come from a helper that runs as the CLI does (see
[Reading these numbers](#reading-these-numbers)).

Compile time (warm mode, median per workload):

| Workload | go-xml | at `eb14939` | Reference |
|---|---:|---:|---:|
| DocBook xslTNG (`docbook.xsl` and its modules) | 55 ms | 56 ms | Saxon 951 ms |
| Peppol Schematron (compiled XSLT 2.0; CEN and PEPPOL) | 23 ms | 22 ms | Saxon 593 ms |
| XRechnung UBL → xr | 6.6 ms | 6.7 ms | Saxon 432 ms |
| XRechnung xr → HTML | 7.8 ms | — | Saxon 486 ms |
| XMark query | 1.0 ms | 0.9 ms | Saxon 221 ms, BaseX 226 ms |
| XSD catalog schemas | 2.9 ms | 3.3 ms | Xerces 106 ms |
| DocBook 5.2 RELAX NG (608 KB) | 25 ms | 26 ms | Jing 118 ms |

## XSLT: DocBook xslTNG

DocBook xslTNG 2.6.0 (`docbook.xsl`, XSLT 3.0) renders 42 of its own test
documents to XHTML5. They were chosen to span size (186 B to 54 KB) and
feature areas: books, sets, reference entries, tables, lists, synopses,
callouts, glossaries, bibliographies, indexes, footnotes, localisation and
dates.

| | go-xml | at `eb14939` | Saxon-HE |
|---|---:|---:|---:|
| Cold, median over items | 89 ms | 92 ms | 1,082 ms |
| Warm, median over items | 13.0 ms | 16.6 ms | 33.7 ms |
| Compile | 55 ms | 56 ms | 951 ms |
| Peak RSS, cold (median) | 84 MB | 86 MB | 242 MB |

Cold, go-xml is faster on every item (0.08× to 0.13×). Warm, it is faster on
36 of 40 items (0.27× to 1.49×). The four it still loses are the large ones
with index or table-of-contents work:

| Item | go-xml warm | at `eb14939` | Saxon warm | Ratio |
|---|---:|---:|---:|---:|
| `table-html.001` | 7.9 ms | 9.5 ms | 29.1 ms | 0.27× |
| `blocks.002` | 10.1 ms | 11.6 ms | 32.1 ms | 0.31× |
| `book.001` | 31.3 ms | 42.0 ms | 41.0 ms | 0.76× |
| `epub.001` | 46.1 ms | 98.2 ms | 40.6 ms | 1.13× |
| `indexterm.001` | 63.6 ms | 76.0 ms | 51.3 ms | 1.24× |
| `chapter.003` | 66.4 ms | 95.7 ms | 53.0 ms | 1.25× |
| `ptoc.001` | 88.3 ms | 125 ms | 59.1 ms | 1.49× |

## XSLT: e-invoicing

**Peppol BIS Billing 3.0.** The CEN EN 16931 and Peppol Schematron rule sets,
compiled to XSLT 2.0 by SchXslt 1.10.1, run against each example invoice and
produce SVRL.
- **Cold:** go-xml is 25× faster (29 ms against 702 ms; 30 ms at `eb14939`).
- **Warm:** Saxon finishes an invoice in 0.9–2.7 ms; go-xml takes 1.7–6.7 ms,
  1.7× to 2.6× slower (2.4× to 3.5× at `eb14939`).

These stylesheets are hundreds of independent XPath assertions over one small
document. That is the shape where Saxon's bytecode generation and JIT pay off
most. Round 4 measured that interpreting the expressions is only 2–4% of
go-xml's time here; the rest is allocation and collection, which the node
and context changes planned for v2 address
([profiling](profiling.md#round-4-efficiency-without-an-api-change-1cfeebd)).

**XRechnung, stage 1** (KoSIT `ubl-invoice-xr.xsl`, XSLT 2.0): UBL invoices from
the KoSIT test suite to the intermediate `xr:invoice` XML.
- **Cold:** 18 ms against 543 ms (29× faster; 22 ms at `eb14939`).
- **Warm:** 6.2 ms against 2.1 ms (3.0× slower, 2.1× to 4.5× by item; 4.0× at
  `eb14939`).

**XRechnung, stage 2** (`xrechnung-html.xsl`, XSLT 2.0): stage 1's output to
HTML. All 8 items now agree with Saxon (see
[Correctness findings](#correctness-findings)).
- **Cold:** 15 ms against 554 ms (37× faster).
- **Warm:** 4.3 ms against 2.9 ms (1.42× slower, 1.15× to 1.92× by item).

## XQuery: XMark

XMark q1–q20 over the auction documents at factors 0.01 (1 MB) and 0.1
(11 MB), with the auction document as the context item. All 40 items agree
byte-for-byte across the three engines after whitespace collapse.

At factor 0.1 (milliseconds):

| Query | go-xml warm | at `eb14939` | Saxon warm | BaseX warm | go-xml cold | Saxon cold | BaseX cold |
|---|---:|---:|---:|---:|---:|---:|---:|
| q1 | 51 | 56 | 45 | 90 | 64 | 485 | 644 |
| q2 | 53 | 59 | 45 | 92 | 67 | 477 | 655 |
| q3 | 57 | 61 | 47 | 92 | 71 | 519 | 665 |
| q4 | 55 | 58 | 45 | 93 | 68 | 476 | 662 |
| q5 | 50 | 55 | 46 | 93 | 65 | 470 | 653 |
| q6 | 60 | 66 | 47 | 93 | 72 | 455 | 619 |
| q7 | 72 | 80 | 47 | 89 | 84 | 417 | 651 |
| q8 | **58** | 63 | 218 | 1,199 | 71 | 748 | 632 |
| q9 | **59** | 65 | 312 | 1,304 | 75 | 826 | 2,182 |
| q10 | 94 | 103 | 83 | 257 | 113 | 557 | 713 |
| q11 | **76** | 81 | 119 | 3,232 | 93 | 630 | 3,905 |
| q12 | 75 | 79 | 75 | 736 | 91 | 671 | 1,301 |
| q13 | 54 | 57 | 47 | 94 | 67 | 494 | 668 |
| q14 | 61 | 70 | 52 | 96 | 78 | 507 | 587 |
| q15 | 51 | 55 | 45 | 91 | 64 | 416 | 575 |
| q16 | 52 | 56 | 46 | 89 | 65 | 479 | 603 |
| q17 | 53 | 58 | 46 | 90 | 67 | 464 | 590 |
| q18 | 53 | 57 | 47 | 89 | 67 | 438 | 611 |
| q19 | 59 | 65 | 49 | 97 | 75 | 458 | 603 |
| q20 | 55 | 60 | 45 | 96 | 68 | 432 | 588 |

- **The value joins are go-xml's strongest queries.** q8, q9 and q11 are
  0.19× to 0.64× Saxon's warm time and 17–43× faster than BaseX. They run as
  hash or range joins ([profiling](profiling.md#round-2-implementation-status)).
- **The rest are 1.0× to 1.5× Saxon warm,** down from 1.2× to 1.7×. The floor
  of about 50 ms on the 11 MB document is the parse, which every engine
  repeats per run; Saxon's is about 45 ms.
- **Cold, every query is faster than both JVMs**: 5× to 11× against Saxon.

## Schema validation

**XSD.** The XSLT 3.0 test-catalog schema (144 KB, XSD 1.0 with `vc:`
attributes) and the QT3 catalog schema (78 KB), validating 11 real catalog
files from 3.6 KB to 2.5 MB. They stand in for UBL 2.1, whose schemas are not
in the test corpus. All three validators agree on every verdict.

| | go-xml | at `eb14939` | Xerces-J 1.1 | xmllint |
|---|---:|---:|---:|---:|
| Cold, median | 7.6 ms | 7.1 ms | 175 ms | 3.7 ms |
| Warm, median | 0.58 ms | 0.77 ms | 0.92 ms | — |
| Compile | 2.9 ms | 3.3 ms | 106 ms | — |

Warm, go-xml ranges from 0.28× (faster, on small instances) to 1.7× slower
(the 2.5 MB `xp-striding` catalog, 18.8 against 11.2 ms). libxml2's C
validator is 1.4× to 2.2× faster than go-xml cold; about 3 ms of go-xml's
7.6 ms is process start ([profiling](profiling.md#where-cold-time-goes)).
xmllint implements XSD 1.0 only.

**RELAX NG.** DocBook 5.2 (`docbook.rng`, 608 KB) over 40 DocBook test
documents, all 40 timed. 5 are rejected by all three validators and are timed
because the verdicts agree: `JFK_Inaugural` uses `dialogue`, which DocBook 5.2
does not define, and four carry unexpanded `xi:include` elements.

| | go-xml | at `eb14939` | Jing | xmllint |
|---|---:|---:|---:|---:|
| Cold, median | 27 ms | 28 ms | 182 ms | 29 ms |
| Warm, median per document | 0.03 ms | 0.09 ms | 0.03 ms | — |
| Compile | 25 ms | 26 ms | 118 ms | — |

Cold time is the grammar compile, about a fifth of Jing's and level with
xmllint overall. Validating once compiled now takes about as long as Jing:
the round-3 memo points cut it to a third. The per-item ratio (0.04× to
1.85×) is noise around very small numbers.

## Parsing and Canonical XML

Documents generated with a fixed seed at 1, 10 and 100 MB. Cold runs parse and
write Canonical XML 1.0 to a file, so the outputs can be checked against
each other. Warm runs parse to a tree (go-xml), or tokenise and re-emit to a
null sink (`encoding/xml`).

| Size | go-xml cold | at `eb14939` | go-xml warm | encoding/xml cold | encoding/xml warm | xmllint cold |
|---:|---:|---:|---:|---:|---:|---:|
| 1 MB | 17 ms | 17 ms | 8.5 ms | 24 ms | 16 ms | 21 ms |
| 10 MB | 116 ms | 117 ms | 86 ms | 183 ms | 158 ms | 195 ms |
| 100 MB | 1,127 ms | 1,094 ms | 880 ms | 1,740 ms | 1,582 ms | 1,871 ms |

In both modes go-xml builds a full XDM tree (node identity, document order,
namespaces) and writes Canonical XML from it; warm runs write to a null sink.
That gives about **85–90 MB/s** cold for parse plus C14N, and about 115 MB/s
warm. `encoding/xml` reaches about 60 MB/s, but it only tokenises and re-emits
and builds no tree. xmllint parses and writes C14N at about 53 MB/s. Round 4
did not touch the parser, so these are unchanged within noise.

The 100 MB document needs `MaxBytes: -1` (CLI `-max-bytes -1`). go-xml's
default 64 MB document limit is a deliberate guard against untrusted input.

Canonical XML (inclusive 1.0) of the 1 and 10 MB documents is faster than
`xmllint --c14n` cold: 19 against 21 ms, and 112 against 194 ms.

## Memory

Peak RSS in cold runs (medians, with the maximum where it differs):

| Workload | go-xml | at `eb14939` | Reference |
|---|---:|---:|---:|
| DocBook xslTNG | 84 / 147 MB | 86 / 145 MB | Saxon 242 MB |
| Peppol | 41 MB | 44 MB | Saxon 170 MB |
| XRechnung stage 1 | 32 MB | 33 MB | Saxon 127 MB |
| XMark (median / max) | 101 / 238 MB | 108 / 234 MB | Saxon 147 / 333 MB, BaseX 160 / 547 MB |
| XSD | 17 MB | 17 MB | Xerces 77 MB, xmllint 3 MB |
| RELAX NG DocBook | 38 MB | 37 MB | Jing 70 MB, xmllint 11 MB |
| Parse 10 MB | 235 MB | 245 MB | encoding/xml 35 MB, xmllint 142 MB |
| Parse 100 MB | **2.2 GB** | 2.3 GB | encoding/xml 209 MB, xmllint 1.4 GB |

For transforms go-xml uses a third to half of a JVM's memory. The CLI runs its
collector at `GOGC=200` unless `GOGC` is set, which trades some peak memory
for CPU: DocBook's largest item rose from 122 to 147 MB. For large documents
libxml2 is still leaner: the XDM tree costs about 23 bytes of RSS per byte of
input, 1.7× libxml2's. Closing that needs the node changes that wait for a v2
API ([profiling](profiling.md#fix-candidates-ranked)).

## Where the break-even is

From the cold and warm medians, a rough estimate of how many documents one
process must handle before the JVM's start and compile cost is paid back:

| Workload | Saxon/Xerces fixed cost | Per-document gap | Break-even |
|---|---:|---:|---:|
| DocBook xslTNG | ~1.0 s | go-xml faster warm | none: go-xml finishes first at any batch size |
| XMark, simple queries | ~0.4 s | ~6 ms | ~70 documents |
| XMark, value joins | ~0.7 s | go-xml faster warm | none |
| Peppol Schematron | ~0.67 s | ~1.0 ms | ~650 documents |
| XRechnung stage 1 | ~0.52 s | ~4 ms | ~130 documents |
| XRechnung stage 2 | ~0.54 s | ~1.4 ms | ~390 documents |
| XSD catalogs | ~0.17 s | go-xml faster warm | none |

Below these batch sizes per process, go-xml finishes first. Above them, a warm
JVM does. These are estimates from medians, not measured crossovers; real
documents vary. At `eb14939` the break-even was about 270 documents for Peppol
and 70 for XRechnung stage 1.

## What could not be measured

- **XMark at factor 1 (111 MB).** The workload stops at factor 0.1. Since
  `3727b35` the CLI takes `-max-bytes -1`, and `-max-items -1`
  lets q11 and q12 hold their ~37 M items (about 3.5 GB); q11 then takes 3.3 s
  and matches Saxon. It has not been timed here.
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

Fixed between the second and third runs, which made stage 2 comparable:

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
