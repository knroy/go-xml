# Benchmark: go-xml v1 and v2 against established engines

How go-xml v1 and v2 compare with Saxon-HE, BaseX, Jing, Xerces-J, libxml2
and Go's `encoding/xml` on real workloads: DocBook rendering, e-invoice
validation and visualisation, XMark queries, schema validation, parsing and
canonicalisation. Each comparison is timed only after the engines are shown to
produce the same output.

**The short version.** Cold, both versions finish first everywhere except
small XSD validations, where xmllint's C start-up is 1.9× quicker; go-xml
starts in milliseconds where a JVM takes 0.4–1 s. Warm, in a long-running
process, v2 is faster than Saxon on DocBook (39 of 40 documents, against 36 of
40 for v1), level with Saxon on XMark and 2.7× faster than BaseX, and faster
than Xerces on XSD and Jing on RELAX NG, as v1 was. Saxon still leads on the
Schematron-shaped e-invoice stylesheets, by 1.2× to 1.8× in v2, down from
1.4× to 3.0× in v1. On large documents v2 needs a sixth to a third of v1's
peak memory, and less than libxml2.

Measured on one machine with the same harness and engine builds, otherwise
idle: v1 at `f45068c` (2026-10-08/09, after the round-3 and round-4 work in
[profiling](profiling.md)), and v2 as the `v2` branch on 2026-10-09 (after
the 40-byte node records, the split evaluation context and the allocation
cuts; see [profiling](profiling.md#where-go-xml-stands)). The reference
engines were re-run with each version, and every ratio uses the reference
times from its own run. Earlier runs, at `eb14939` and `22f4b04`, are in this
file's history.

## Contents

- [Machine and engines](#machine-and-engines)
- [Method](#method)
- [Results at a glance](#results-at-a-glance)
  - [What changed from v1 to v2](#what-changed-from-v1-to-v2)
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
| go-xml v1 | `f45068c` | everything |
| go-xml v2 | the `v2` branch on 2026-10-09 | everything |
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

**Ratios** are go-xml's time divided by the reference's, both from the same
run. Below 1 means go-xml is faster. Per-workload figures are the geometric
mean over the items both engines agreed on, with the range across items.
Absolute reference times in the tables below are from the v2 run; the v1 run's
are within noise of them except where noted.

**go-xml's flags** match what its own test runner uses for the same corpus
(`-allow-doctype`, `-allow-unparsed-text`, `-allow-dir`). The library refuses
these features by default; turning them on does not change its speed.

## Results at a glance

Ratios are go-xml's time over the reference's, geometric mean over the items
both agreed on; below 1 means go-xml is faster. Both versions time the same
items.

| Workload | Items timed | Reference | Cold, v1 | Cold, v2 | Warm, v1 | Warm, v2 |
|---|---:|---|---:|---:|---:|---:|
| DocBook xslTNG → XHTML5 | 40 / 42 | Saxon-HE | **0.09×** (12× faster) | **0.10×** (10× faster) | **0.46×** (2.2× faster) | **0.37×** (2.7× faster) |
| Peppol BIS Schematron → SVRL | 18 / 18 | Saxon-HE | **0.04×** (25× faster) | **0.04×** (23× faster) | 1.99× slower | 1.31× slower |
| XRechnung UBL → xr:invoice | 8 / 8 | Saxon-HE | **0.03×** (29× faster) | **0.03×** (32× faster) | 3.00× slower | 1.77× slower |
| XRechnung xr:invoice → HTML | 8 / 8 | Saxon-HE | **0.03×** (37× faster) | **0.03×** (36× faster) | 1.42× slower | 1.23× slower |
| XMark q1–q20, 1 and 11 MB | 40 / 40 | Saxon-HE | **0.07×** (14× faster) | **0.06×** (16× faster) | 1.00× (level) | 0.97× (level) |
| XMark q1–q20, 1 and 11 MB | 40 / 40 | BaseX | **0.05×** (18× faster) | **0.05×** (21× faster) | **0.39×** (2.6× faster) | **0.37×** (2.7× faster) |
| XSD 1.0/1.1 (catalog schemas) | 11 / 11 | Xerces-J | **0.05×** (18× faster) | **0.05×** (19× faster) | **0.73×** (1.4× faster) | **0.69×** (1.5× faster) |
| XSD 1.0/1.1 (catalog schemas) | 11 / 11 | xmllint | 1.87× slower | 1.92× slower | — | — |
| RELAX NG (DocBook 5.2) | 40 / 40 | Jing | **0.15×** (6.6× faster) | **0.15×** (6.8× faster) | **0.60×** | **0.75×** |
| RELAX NG (DocBook 5.2) | 40 / 40 | xmllint | 0.96× (level) | 0.95× (level) | — | — |
| Parse 1 / 10 / 100 MB | 3 / 3 | `encoding/xml` | **0.66×** | **0.60×** | **0.54×** | **0.57×** |
| Parse 1 / 10 / 100 MB | 3 / 3 | xmllint | **0.66×** | **0.61×** | — | — |
| Canonical XML 1 / 10 MB | 2 / 2 | xmllint | **0.72×** | **0.64×** | — | — |

The parse and C14N cold figures come from a helper that runs as the CLI does
(see [Reading these numbers](#reading-these-numbers)).

Compile time (warm mode, median per workload):

| Workload | v1 | v2 | Reference |
|---|---:|---:|---:|
| DocBook xslTNG (`docbook.xsl` and its modules) | 55 ms | 77 ms | Saxon 924 ms |
| Peppol Schematron (compiled XSLT 2.0; CEN and PEPPOL) | 23 ms | 24 ms | Saxon 574 ms |
| XRechnung UBL → xr | 6.6 ms | 7.5 ms | Saxon 419 ms |
| XRechnung xr → HTML | 7.8 ms | 7.3 ms | Saxon 429 ms |
| XMark query | 1.0 ms | 0.9 ms | Saxon 222 ms, BaseX 226 ms |
| XSD catalog schemas | 2.9 ms | 2.8 ms | Xerces 100 ms |
| DocBook 5.2 RELAX NG (608 KB) | 25 ms | 24 ms | Jing 118 ms |

### What changed from v1 to v2

**Memory.** v2 stores each node as a 40-byte record instead of a Go object
per node. Peak RSS on the 100 MB parse fell from 2.2 GB to 376 MB, now below
xmllint's 1.4 GB; XMark's median fell from 101 to 35 MB, and the transforms
use 18–27% less. Cold parse got faster with it (0.66× to 0.60×
`encoding/xml`), because the smaller heap needs less memory mapped in.

**Schematron-shaped XSLT.** The split evaluation context and the allocation
cuts took most of the per-assertion overhead out of the e-invoice
stylesheets: Peppol went from 1.99× to 1.31× Saxon warm, XRechnung stage 1
from 3.00× to 1.77×, stage 2 from 1.42× to 1.23×. Saxon still leads on all
three; [profiling](profiling.md#open-fixes) ranks what is left.

**DocBook warm.** 0.46× to 0.37× Saxon. Three of the four items v1 lost
(`epub.001`, `indexterm.001`, `chapter.003`) are now faster than Saxon;
`ptoc.001` is the only one slower, at 1.17× (it was 1.49×).

**Three figures that moved the wrong way**, each explained in
[profiling](profiling.md#the-three-apparent-regressions):
- **DocBook compile, 55 to 77 ms.** Real. The stylesheet checks walk
  ancestors for the version attribute, and each attribute lookup now goes
  through the name table instead of reading a field. It is the main reason
  DocBook cold went from 89 to 104 ms (0.09× to 0.10× Saxon).
- **RELAX NG warm, 0.60× to 0.75× Jing.** Mostly Jing: the same Jing jar ran
  15% faster in the v2 run. go-xml's own time is 6% higher overall (per
  document set-up on small documents), and long documents are faster than
  v1's.
- **Parse warm, 0.54× to 0.57× `encoding/xml`.** Not the parse: the warm item
  also writes Canonical XML, and that write is slower through the record
  accessors. Parse alone is level with v1 in wall time and uses less CPU.

**XMark** is level overall, with one query moving: q10 at 11 MB went from 94
to 122 ms, because XQuery element content is copied twice per constructed
node.

## XSLT: DocBook xslTNG

DocBook xslTNG 2.6.0 (`docbook.xsl`, XSLT 3.0) renders 42 of its own test
documents to XHTML5. They were chosen to span size (186 B to 54 KB) and
feature areas: books, sets, reference entries, tables, lists, synopses,
callouts, glossaries, bibliographies, indexes, footnotes, localisation and
dates.

| | v1 | v2 | Saxon-HE |
|---|---:|---:|---:|
| Cold, median over items | 89 ms | 104 ms | 1,091 ms |
| Warm, median over items | 13.0 ms | 10.9 ms | 33.6 ms |
| Compile | 55 ms | 77 ms | 924 ms |
| Peak RSS, cold (median) | 84 MB | 62 MB | 246 MB |

Cold, both versions are faster on every item (v1 0.08× to 0.13×, v2 0.09× to
0.13×). Warm, v1 is faster on 36 of 40 items (0.27× to 1.49×) and v2 on 39 of
40 (0.18× to 1.17×). The items v1 lost are the large ones with index or
table-of-contents work:

| Item | v1 warm | v2 warm | Saxon warm | Ratio, v1 | Ratio, v2 |
|---|---:|---:|---:|---:|---:|
| `table-html.001` | 7.9 ms | 5.4 ms | 29.6 ms | 0.27× | 0.18× |
| `blocks.002` | 10.1 ms | 7.6 ms | 31.8 ms | 0.31× | 0.24× |
| `book.001` | 31.3 ms | 24.7 ms | 41.2 ms | 0.76× | 0.60× |
| `epub.001` | 46.1 ms | 34.4 ms | 40.8 ms | 1.13× | 0.84× |
| `indexterm.001` | 63.6 ms | 50.7 ms | 51.3 ms | 1.24× | 0.99× |
| `chapter.003` | 66.4 ms | 50.6 ms | 52.2 ms | 1.25× | 0.97× |
| `ptoc.001` | 88.3 ms | 69.3 ms | 59.1 ms | 1.49× | 1.17× |

## XSLT: e-invoicing

**Peppol BIS Billing 3.0.** The CEN EN 16931 and Peppol Schematron rule sets,
compiled to XSLT 2.0 by SchXslt 1.10.1, run against each example invoice and
produce SVRL.

| | v1 | v2 | Saxon-HE |
|---|---:|---:|---:|
| Cold, median | 29 ms | 31 ms | 669 ms |
| Warm, per invoice | 1.7–6.7 ms | 1.0–3.9 ms | 1.0–2.6 ms |
| Warm, ratio by item | 1.66× to 2.61× | 1.01× to 1.51× | |

These stylesheets are hundreds of independent XPath assertions over one small
document. That is the shape where Saxon's bytecode generation and JIT pay off
most. Round 4 measured that interpreting the expressions is only 2–4% of
go-xml's time here; the rest was allocation and collection, which is what v2's
node and context changes cut
([profiling](profiling.md#measured-and-rejected)).

**XRechnung, stage 1** (KoSIT `ubl-invoice-xr.xsl`, XSLT 2.0): UBL invoices from
the KoSIT test suite to the intermediate `xr:invoice` XML.

| | v1 | v2 | Saxon-HE |
|---|---:|---:|---:|
| Cold, median | 18 ms | 16 ms | 525 ms |
| Warm, median | 6.2 ms | 3.6 ms | 2.1 ms |
| Warm, ratio by item | 2.11× to 4.47× | 1.36× to 2.25× | |

**XRechnung, stage 2** (`xrechnung-html.xsl`, XSLT 2.0): stage 1's output to
HTML. All 8 items agree with Saxon in both runs (see
[Correctness findings](#correctness-findings)).

| | v1 | v2 | Saxon-HE |
|---|---:|---:|---:|
| Cold, median | 15 ms | 15 ms | 547 ms |
| Warm, median | 4.3 ms | 3.3 ms | 2.8 ms |
| Warm, ratio by item | 1.15× to 1.92× | 0.83× to 1.77× | |

## XQuery: XMark

XMark q1–q20 over the auction documents at factors 0.01 (1 MB) and 0.1
(11 MB), with the auction document as the context item. All 40 items agree
byte-for-byte across the three engines after whitespace collapse.

At factor 0.1 (milliseconds; Saxon and BaseX from the v2 run):

| Query | v1 warm | v2 warm | Saxon warm | BaseX warm | v1 cold | v2 cold | Saxon cold | BaseX cold |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| q1 | 51 | 50 | 46 | 107 | 64 | 62 | 492 | 654 |
| q2 | 53 | 52 | 45 | 89 | 67 | 63 | 511 | 633 |
| q3 | 57 | 55 | 49 | 109 | 71 | 64 | 542 | 638 |
| q4 | 55 | 52 | 58 | 90 | 68 | 61 | 486 | 663 |
| q5 | 50 | 50 | 48 | 89 | 65 | 59 | 489 | 656 |
| q6 | 60 | 52 | 47 | 105 | 72 | 61 | 497 | 650 |
| q7 | 72 | 54 | 47 | 89 | 84 | 65 | 496 | 687 |
| q8 | **58** | **55** | 217 | 1,192 | 71 | 67 | 919 | 651 |
| q9 | **59** | **59** | 275 | 1,285 | 75 | 68 | 806 | 2,052 |
| q10 | 94 | 122 | 84 | 253 | 113 | 129 | 533 | 671 |
| q11 | **76** | **75** | 121 | 3,096 | 93 | 86 | 644 | 4,172 |
| q12 | 75 | 73 | 72 | 805 | 91 | 82 | 509 | 1,297 |
| q13 | 54 | 52 | 47 | 88 | 67 | 61 | 472 | 645 |
| q14 | 61 | 55 | 50 | 92 | 78 | 65 | 536 | 661 |
| q15 | 51 | 50 | 46 | 90 | 64 | 60 | 502 | 656 |
| q16 | 52 | 50 | 46 | 89 | 65 | 59 | 532 | 661 |
| q17 | 53 | 52 | 45 | 108 | 67 | 61 | 505 | 650 |
| q18 | 53 | 54 | 47 | 108 | 67 | 61 | 510 | 663 |
| q19 | 59 | 56 | 49 | 98 | 75 | 67 | 542 | 921 |
| q20 | 55 | 53 | 47 | 93 | 68 | 63 | 442 | 601 |

- **The value joins are go-xml's strongest queries.** q8, q9 and q11 are
  0.19× to 0.64× Saxon's warm time in both versions and 21–43× faster than
  BaseX. They run as hash or range joins
  ([changelog](../CHANGELOG.md)).
- **The rest are 1.0× to 1.5× Saxon warm in v1 and 0.9× to 1.45× in v2.** The
  floor of about 50 ms on the 11 MB document is the parse, which every engine
  repeats per run; Saxon's is about 45 ms. q10 is v2's one slower query
  (see [What changed](#what-changed-from-v1-to-v2)).
- **Cold, every query is faster than both JVMs**: 5× to 11× against Saxon in
  v1, 4× to 14× in v2.

## Schema validation

**XSD.** The XSLT 3.0 test-catalog schema (144 KB, XSD 1.0 with `vc:`
attributes) and the QT3 catalog schema (78 KB), validating 11 real catalog
files from 3.6 KB to 2.5 MB. They stand in for UBL 2.1, whose schemas are not
in the test corpus. All three validators agree on every verdict.

| | v1 | v2 | Xerces-J 1.1 | xmllint |
|---|---:|---:|---:|---:|
| Cold, median | 7.6 ms | 6.7 ms | 171 ms | 3.4 ms |
| Warm, median | 0.58 ms | 0.66 ms | 0.97 ms | — |
| Compile | 2.9 ms | 2.8 ms | 100 ms | — |

Warm, v1 ranges from 0.28× Xerces (faster, on small instances) to 1.68×
slower, and v2 from 0.25× to 1.64×; the slowest is the 2.5 MB `xp-striding`
catalog (v1 18.8 ms, v2 18.2 ms, Xerces 11.1 ms). libxml2's C validator is
1.4× to 2.2× faster than v1 cold and 1.5× to 2.3× faster than v2; about 3 ms
of go-xml's cold time is process start
([profiling](profiling.md#open-fixes)). xmllint implements XSD 1.0
only.

**RELAX NG.** DocBook 5.2 (`docbook.rng`, 608 KB) over 40 DocBook test
documents, all 40 timed. 5 are rejected by all three validators and are timed
because the verdicts agree: `JFK_Inaugural` uses `dialogue`, which DocBook 5.2
does not define, and four carry unexpanded `xi:include` elements.

| | v1 | v2 | Jing | xmllint |
|---|---:|---:|---:|---:|
| Cold, median | 27 ms | 27 ms | 182 ms | 28 ms |
| Warm, median per document | 0.029 ms | 0.030 ms | 0.026 ms | — |
| Compile | 25 ms | 24 ms | 118 ms | — |

Cold time is the grammar compile, about a fifth of Jing's and level with
xmllint overall. Validating once compiled takes about as long as Jing in both
versions; Jing's own median was 0.034 ms in the v1 run. The per-item ratio
(0.04× to 1.85× in v1, 0.05× to 1.99× in v2) is noise around very small
numbers.

## Parsing and Canonical XML

Documents generated with a fixed seed at 1, 10 and 100 MB. Cold runs parse and
write Canonical XML 1.0 to a file, so the outputs can be checked against
each other. Warm runs parse to a tree (go-xml), or tokenise and re-emit to a
null sink (`encoding/xml`).

| Size | v1 cold | v2 cold | v1 warm | v2 warm | encoding/xml cold | encoding/xml warm | xmllint cold |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 MB | 17 ms | 15 ms | 8.5 ms | 9.3 ms | 24 ms | 16 ms | 21 ms |
| 10 MB | 116 ms | 111 ms | 86 ms | 90 ms | 186 ms | 158 ms | 191 ms |
| 100 MB | 1,127 ms | 1,018 ms | 880 ms | 891 ms | 1,776 ms | 1,578 ms | 1,901 ms |

In both modes go-xml builds a full XDM tree (node identity, document order,
namespaces) and writes Canonical XML from it; warm runs write to a null sink.
That gives about **90 MB/s (v1) and 100 MB/s (v2)** cold for parse plus C14N,
and about 112 MB/s warm in both. `encoding/xml` reaches about 63 MB/s, but it
only tokenises and re-emits and builds no tree. xmllint parses and writes C14N
at about 53 MB/s.

The 100 MB document needs `MaxBytes: -1` (CLI `-max-bytes -1`). go-xml's
default 64 MB document limit is a deliberate guard against untrusted input.

Canonical XML (inclusive 1.0) of the 1 and 10 MB documents is faster than
`xmllint --c14n` cold: v1 19 and 112 ms, v2 15 and 109 ms, against 21 and
194 ms.

## Memory

Peak RSS in cold runs (median / maximum over items):

| Workload | v1 | v2 | Reference |
|---|---:|---:|---:|
| DocBook xslTNG | 84 / 147 MB | 62 / 95 MB | Saxon 246 / 293 MB |
| Peppol | 41 / 56 MB | 30 / 36 MB | Saxon 171 / 192 MB |
| XRechnung stage 1 | 32 / 38 MB | 24 / 28 MB | Saxon 129 / 138 MB |
| XRechnung stage 2 | 28 / 32 MB | 23 / 24 MB | Saxon 135 / 140 MB |
| XMark | 101 / 238 MB | 35 / 129 MB | Saxon 149 / 333 MB, BaseX 160 / 551 MB |
| XSD | 17 / 50 MB | 14 / 29 MB | Xerces 74 / 95 MB, xmllint 3 / 19 MB |
| RELAX NG DocBook | 38 / 39 MB | 26 / 26 MB | Jing 67 / 77 MB, xmllint 11 / 12 MB |
| Parse 1 MB | 37 MB | 16 MB | encoding/xml 18 MB, xmllint 16 MB |
| Parse 10 MB | 235 MB | **50 MB** | encoding/xml 35 MB, xmllint 142 MB |
| Parse 100 MB | 2.2 GB | **376 MB** | encoding/xml 209 MB, xmllint 1.4 GB |

For transforms v1 uses a fifth to two thirds of a JVM's memory, and v2 a
sixth to a quarter. The CLI runs its collector at `GOGC=200` unless `GOGC` is
set, which trades some peak memory for CPU. For large documents v1's tree
cost about 23 bytes of RSS per byte of input, 1.6× libxml2's; v2's costs
about 4, about a quarter of libxml2's. Only `encoding/xml`, which builds no
tree, uses less.

## Where the break-even is

From the cold and warm medians, a rough estimate of how many documents one
process must handle before the JVM's start and compile cost is paid back.
The fixed cost is the reference's cold median less go-xml's; the gap is
go-xml's warm median less the reference's.

| Workload | JVM fixed cost (v1 / v2 run) | Per-document gap, v1 | Per-document gap, v2 | Break-even, v1 | Break-even, v2 |
|---|---:|---:|---:|---:|---:|
| DocBook xslTNG | ~1.0 s | go-xml faster warm | go-xml faster warm | none | none |
| XMark, simple queries (11 MB) | ~0.41 / 0.44 s | ~7.9 ms | ~4.8 ms | ~50 documents | ~90 documents |
| XMark, value joins (11 MB) | ~0.67 / 0.74 s | go-xml faster warm | go-xml faster warm | none | none |
| Peppol Schematron | ~0.67 / 0.64 s | ~1.0 ms | ~0.47 ms | ~650 documents | ~1,400 documents |
| XRechnung stage 1 | ~0.53 / 0.51 s | ~4.1 ms | ~1.5 ms | ~130 documents | ~330 documents |
| XRechnung stage 2 | ~0.54 / 0.53 s | ~1.4 ms | ~0.53 ms | ~390 documents | ~1,000 documents |
| XSD catalogs | ~0.17 / 0.16 s | go-xml faster warm | go-xml faster warm | none | none |

Below these batch sizes per process, go-xml finishes first. Above them, a warm
JVM does. These are estimates from medians, not measured crossovers; real
documents vary. Part of XMark's move is the reference: Saxon's cold median
was 15% higher in the v2 run.

## What could not be measured

- **XMark at factor 1 (111 MB).** The workload stops at factor 0.1. Since
  `3727b35` the CLI takes `-max-bytes -1`, and `-max-items -1`
  lets q11 and q12 hold their ~37 M items (about 3.5 GB in v1); q11 then
  takes 3.3 s and matches Saxon. It has not been timed here.
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

Fixed after the first run (`22f4b04`):

- **RELAX NG: go-xml rejected 7 of 40 valid DocBook documents**
  (`bibliography.006`, `book.006`, `glossary.007`, `index.002`,
  `oxy-changemarkup.001`, `programlisting.004`, `xref.001`). Six came from
  whitespace between element children being matched as text (RELAX NG §6.2.7,
  `b44313c`); `xref.001` hit the derivative size bound because `choice` kept
  duplicate alternatives (`d8f0ac1`). All 40 verdicts now match Jing and
  xmllint.

Fixed after the second run (`eb14939`), which made stage 2 comparable:

- **XRechnung HTML: the stylesheet's `<meta charset="UTF-8"/>` was dropped**
  under `include-content-type="no"` (`262be91`), and html indentation split
  inline elements across lines (`4457808`). With both fixed all 8 items agree
  with Saxon, once its `<!DOCTYPE HTML>` is stripped: the stylesheet gives no
  `html-version`, whose default XSLT 3.0 §26 leaves implementation-defined.

The v1 and v2 runs agree and disagree on exactly the same items. The two
differences left are not go-xml bugs:

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

- **One machine, one run per version.** Laptop timings vary about ±10–15%
  from run to run, and the reference engines did too between the two runs:
  Jing ran 15% faster warm in the v2 run, and Saxon's XMark cold median was
  15% slower. Ratios under about 1.3× are not meaningful, and neither is a
  v1-to-v2 move of a few percent; the large ones are.
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
  The figures here come from the two runs recorded at the top of this page.
- **macOS, not Linux.** On macOS, Go's runtime re-commits every reused heap
  page with `madvise`, which costs 13–26% of CPU on parse-heavy work. Linux
  does not. In a 4-CPU Linux VM on the same machine, cold CLI runs used
  11–13% less CPU and warm in-process loops 2–20% more, with GC marking in
  `madvise`'s place. Bare-metal Linux has not been measured; expect cold
  parse and query times at or below these, and do not read the warm figures
  as Linux numbers ([profiling](profiling.md#linux)).
- **The parse and C14N helper runs as the CLI does** since round 4:
  `GOGC=200` and a streamed parse. Before that it ran at `GOGC=100` from an
  in-memory string, which read within noise at 1 and 10 MB and about 8–15%
  more CPU at 100 MB. The warm loops still parse with `ParseString` at the
  harness's own `GOGC`, as a library caller would.
