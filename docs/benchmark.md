# Benchmark: go-xml v1 and v2 against established engines

How go-xml v1 and v2 compare with Saxon-HE, BaseX, Jing, Xerces-J, libxml2
and Go's `encoding/xml` on real workloads: DocBook rendering, e-invoice
validation and visualisation, XMark queries, schema validation, parsing and
canonicalisation. Each comparison is timed only after the engines are shown to
produce the same output.

**The short version.** Cold, both versions finish first everywhere except
small XSD validations, where xmllint's C start-up is quicker (1.9× against v1,
1.5× against v2); go-xml starts in milliseconds where a JVM takes 0.4–1 s.
Warm, in a long-running process, v2 is 5.1× faster than Saxon on DocBook and
faster on all 40 documents (v1: 2.2×, 36 of 40), 0.77× Saxon on XMark and 3.6×
faster than BaseX, and faster than Saxon on XRechnung stage 2 (0.64×), where
v1 was 1.4× slower. On Peppol (0.94×) and XRechnung stage 1 (1.08×) v2 and
Saxon are level within the JVM's run-to-run variance; v1 was 1.99× and 3.00×
slower. It is faster than Xerces on XSD and than Jing on RELAX NG, as v1 was.
On large documents v2 needs a sixth to a third of v1's peak memory, and less
than libxml2.

Measured on one machine with the same harness and engine builds, otherwise
idle: v1 at `f45068c` (2026-10-08/09), and v2 at version 2.0.0, `a6f334f1`,
on 2026-10-10. The reference engines were re-run with each version, and every
ratio uses the reference times from its own run. The previous v2 run, "v2 at
`4068c98c`" (earlier on 2026-10-10, before the 2.0.0 release commits), is
cited below where it explains a move; it and the earlier runs are in this
file's history. Where go-xml's time goes, and how to measure a change, is in
[profiling](profiling.md).

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
| go-xml v2 | 2.0.0, `a6f334f1` | everything |
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
Absolute reference times in the tables below are from the v2 run at
`a6f334f1`; the v1 run's are within noise of them except where noted (see
[Reading these numbers](#reading-these-numbers)).

**go-xml's flags** match what its own test runner uses for the same corpus
(`-allow-doctype`, `-allow-unparsed-text`, `-allow-dir`). The library refuses
these features by default; turning them on does not change its speed.

## Results at a glance

Ratios are go-xml's time over the reference's, geometric mean over the items
both agreed on; below 1 means go-xml is faster. Both versions time the same
items.

| Workload | Items timed | Reference | Cold, v1 | Cold, v2 | Warm, v1 | Warm, v2 |
|---|---:|---|---:|---:|---:|---:|
| DocBook xslTNG → XHTML5 | 40 / 42 | Saxon-HE | **0.09×** (12× faster) | **0.08×** (12× faster) | **0.46×** (2.2× faster) | **0.20×** (5.1× faster) |
| Peppol BIS Schematron → SVRL | 18 / 18 | Saxon-HE | **0.04×** (25× faster) | **0.04×** (26× faster) | 1.99× slower | **0.94×** (level) |
| XRechnung UBL → xr:invoice | 8 / 8 | Saxon-HE | **0.03×** (29× faster) | **0.03×** (40× faster) | 3.00× slower | 1.08× (level) |
| XRechnung xr:invoice → HTML | 8 / 8 | Saxon-HE | **0.03×** (37× faster) | **0.02×** (44× faster) | 1.42× slower | **0.64×** (1.6× faster) |
| XMark q1–q20, 1 and 11 MB | 40 / 40 | Saxon-HE | **0.07×** (14× faster) | **0.05×** (20× faster) | 1.00× (level) | **0.77×** (1.3× faster) |
| XMark q1–q20, 1 and 11 MB | 40 / 40 | BaseX | **0.05×** (18× faster) | **0.04×** (27× faster) | **0.39×** (2.6× faster) | **0.28×** (3.6× faster) |
| XSD 1.0/1.1 (catalog schemas) | 11 / 11 | Xerces-J | **0.05×** (18× faster) | **0.04×** (25× faster) | **0.73×** (1.4× faster) | **0.61×** (1.6× faster) |
| XSD 1.0/1.1 (catalog schemas) | 11 / 11 | xmllint | 1.87× slower | 1.50× slower | — | — |
| RELAX NG (DocBook 5.2) | 40 / 40 | Jing | **0.15×** (6.6× faster) | **0.14×** (7.3× faster) | **0.60×** | **0.55×** |
| RELAX NG (DocBook 5.2) | 40 / 40 | xmllint | 0.96× (level) | 0.88× (level) | — | — |
| Parse 1 / 10 / 100 MB | 3 / 3 | `encoding/xml` | **0.66×** | **0.48×** | **0.54×** | **0.46×** |
| Parse 1 / 10 / 100 MB | 3 / 3 | xmllint | **0.66×** | **0.46×** | — | — |
| Canonical XML 1 / 10 MB | 2 / 2 | xmllint | **0.72×** | **0.48×** | — | — |

The parse and C14N cold figures come from a helper that runs as the CLI does
(see [Reading these numbers](#reading-these-numbers)). Several warm ratios
moved with the JVM rather than with go-xml. Against v2 at `4068c98c`, go-xml's
own times on Peppol, both XRechnung stages and RELAX NG moved by under 4%.
Saxon's and Jing's moved by 15–37% (see [Reading these
numbers](#reading-these-numbers)). In that run the same four ratios read
1.16×, 1.25×, 0.89× and 0.76×.

Compile time (warm mode, median per workload):

| Workload | v1 | v2 | Reference |
|---|---:|---:|---:|
| DocBook xslTNG (`docbook.xsl` and its modules) | 55 ms | 57 ms | Saxon 848 ms |
| Peppol Schematron (compiled XSLT 2.0; CEN and PEPPOL) | 23 ms | 23 ms | Saxon 599 ms |
| XRechnung UBL → xr | 6.6 ms | 8.2 ms | Saxon 422 ms |
| XRechnung xr → HTML | 7.8 ms | 6.3 ms | Saxon 437 ms |
| XMark query | 1.0 ms | 0.85 ms | Saxon 222 ms, BaseX 229 ms |
| XSD catalog schemas | 2.9 ms | 2.2 ms | Xerces 99 ms |
| DocBook 5.2 RELAX NG (608 KB) | 25 ms | 23 ms | Jing 120 ms |

Each compile is timed once, so a sub-millisecond figure is noisy. XMark's
query compile read 0.35 ms at `4068c98c` and 0.85 ms here. Compiled 300 times
in a loop, both trees take about 16 µs a query and make the same 251
allocations.

### What changed from v1 to v2

**DocBook warm.** 0.46× to 0.20× Saxon (v2 at `4068c98c`: 0.19×); the median
document went from 13.0 to 5.3 ms. All four items v1 lost are now faster than
Saxon; the slowest, `ptoc.001`, went from 1.49× to 0.85×. Lazy globals gave
most of the first fix rounds' gain: DocBook runs several transforms per
document with 300–950 globals each, and evaluates few of them. The v2 node
records, the split evaluation context and the allocation cuts gave the rest
([changelog](../CHANGELOG.md)).

**Schematron-shaped XSLT.** Peppol went from 1.99× to 0.94× Saxon warm,
XRechnung stage 1 from 3.00× to 1.08×, stage 2 from 1.42× to 0.64×.
go-xml's own medians fell:

| Workload | v1 | v2 at `4068c98c` | v2.0.0 |
|---|---:|---:|---:|
| Peppol | 2.4 ms | 1.3 ms | 1.3 ms |
| XRechnung stage 1 | 6.2 ms | 2.7 ms | 2.6 ms |
| XRechnung stage 2 | 4.3 ms | 2.3 ms | 2.2 ms |

Saxon's moved more between the two v2 runs than go-xml's did (see
[Reading these numbers](#reading-these-numbers)). v2 is faster than Saxon on
9 of the 18 Peppol items, 4 of the 8 stage 1 invoices and all 8 stage 2
invoices. [Profiling](profiling.md#xslt-peppol-and-xrechnung) shows where the
time goes.

**Memory.** v2 stores each node as a 40-byte record instead of a Go object
per node. Peak RSS on the 100 MB parse fell from 2.2 GB to 371 MB, below
xmllint's 1.4 GB; XMark's median fell from 101 to 28 MB, and the transforms
use 36–44% less. Cold parse got faster with it (0.66× to 0.48×
`encoding/xml`): the smaller heap needs less memory mapped in.

**Compile and XMark q10.** DocBook compile is 55 ms in v1 and 57 ms in v2.
DocBook cold is 84 ms against v1's 89 ms. XMark q10 at 11 MB went from 94 ms
in v1 to 79 ms. XMark overall went from 1.00× to 0.77× Saxon, with 39 of 40
items faster than Saxon (v1: 9).

**Parse.** Warm, 0.54× `encoding/xml` in v1 and 0.46× in v2.0.0. At 100 MB,
go-xml's times were:

| | v1 | v2 at `4068c98c` | v2.0.0 |
|---|---:|---:|---:|
| Warm | 880 ms | 890 ms | 718 ms |
| Cold | 1,127 ms | 954 ms | 796 ms |

`encoding/xml` was level across the runs (1,582 and 1,585 ms warm). Two
commits explain the change since `4068c98c`:
- The 5–7% slowdown at `4068c98c` was bisected to `smallNames` 24, which cost
  parsing 7%. `44cd0280` set it back to 16.
- V36 (`b08a3d4a`), one namespace stack for checking and building a start
  tag, then made parse 13–15% faster.

**RELAX NG and XSD warm: the ratios follow the JVM validators.** Jing's warm
geometric mean per document was 44.5 µs in the v1 run, 32.5 µs at `4068c98c`
and 42.6 µs in this one. Xerces's was 1.47, 1.49 and 1.49 ms. go-xml's
RELAX NG time went from 26.8 µs per document in v1 to 24.5 µs at `4068c98c`
and 23.6 µs now. The ratio went from 0.60× to 0.76× Jing and back to 0.55×,
mostly with Jing. On XSD, go-xml went from 1.07 ms to 0.99 and 0.92 ms, and
the ratio from 0.73× to 0.61× Xerces with Xerces level: that move is
go-xml's, from the faster parse. [Profiling](profiling.md#relax-ng) shows
where RELAX NG's time goes on small documents.

## XSLT: DocBook xslTNG

DocBook xslTNG 2.6.0 (`docbook.xsl`, XSLT 3.0) renders 42 of its own test
documents to XHTML5. They were chosen to span size (186 B to 54 KB) and
feature areas: books, sets, reference entries, tables, lists, synopses,
callouts, glossaries, bibliographies, indexes, footnotes, localisation and
dates.

| | v1 | v2 | Saxon-HE |
|---|---:|---:|---:|
| Cold, median over items | 89 ms | 84 ms | 1,082 ms |
| Warm, median over items | 13.0 ms | 5.3 ms | 33.2 ms |
| Compile | 55 ms | 57 ms | 848 ms |
| Peak RSS, cold (median) | 84 MB | 54 MB | 240 MB |

Cold, both versions are faster on every item (v1 0.08× to 0.13×, v2 0.07× to
0.11×). Warm, v1 is faster on 36 of 40 items (0.27× to 1.49×) and v2 on all
40 (0.05× to 0.85×). The items v1 lost are the large ones with index or
table-of-contents work:

| Item | v1 warm | v2 warm | Saxon warm | Ratio, v1 | Ratio, v2 |
|---|---:|---:|---:|---:|---:|
| `table-html.001` | 7.9 ms | 1.4 ms | 28.8 ms | 0.27× | 0.05× |
| `blocks.002` | 10.1 ms | 3.4 ms | 31.4 ms | 0.31× | 0.11× |
| `book.001` | 31.3 ms | 16.1 ms | 40.4 ms | 0.76× | 0.40× |
| `epub.001` | 46.1 ms | 25.2 ms | 39.8 ms | 1.13× | 0.63× |
| `indexterm.001` | 63.6 ms | 38.4 ms | 49.9 ms | 1.24× | 0.77× |
| `chapter.003` | 66.4 ms | 35.4 ms | 52.0 ms | 1.25× | 0.68× |
| `ptoc.001` | 88.3 ms | 49.5 ms | 58.2 ms | 1.49× | 0.85× |

## XSLT: e-invoicing

**Peppol BIS Billing 3.0.** The CEN EN 16931 and Peppol Schematron rule sets,
compiled to XSLT 2.0 by SchXslt 1.10.1, run against each example invoice and
produce SVRL.

| | v1 | v2 | Saxon-HE |
|---|---:|---:|---:|
| Cold, median | 29 ms | 28 ms | 700 ms |
| Warm, per invoice | 1.7–6.7 ms | 0.8–3.5 ms | 1.1–3.2 ms |
| Warm, ratio by item | 1.66× to 2.61× | 0.75× to 1.11× | |

These stylesheets are hundreds of independent XPath assertions over one small
document. That is the shape where Saxon's bytecode generation and JIT pay off
most. Interpreting the expressions is only 2–4% of go-xml's time here
([profiling](profiling.md#measured-and-rejected)). Half of it is the
collector and the allocator, driven by atomising node values for comparisons
([profiling](profiling.md#xslt-peppol-and-xrechnung)). v2 is faster than Saxon
on 9 of the 18 items in this run. At `4068c98c` it was faster on 2, with
go-xml's times the same and Saxon's 19% lower.

**XRechnung, stage 1** (KoSIT `ubl-invoice-xr.xsl`, XSLT 2.0): UBL invoices from
the KoSIT test suite to the intermediate `xr:invoice` XML.

| | v1 | v2 | Saxon-HE |
|---|---:|---:|---:|
| Cold, median | 18 ms | 13 ms | 528 ms |
| Warm, median | 6.2 ms | 2.6 ms | 2.5 ms |
| Warm, ratio by item | 2.11× to 4.47× | 0.82× to 1.36× | |

**XRechnung, stage 2** (`xrechnung-html.xsl`, XSLT 2.0): stage 1's output to
HTML. All 8 items agree with Saxon in both runs (see
[Correctness findings](#correctness-findings)).

| | v1 | v2 | Saxon-HE |
|---|---:|---:|---:|
| Cold, median | 15 ms | 13 ms | 549 ms |
| Warm, median | 4.3 ms | 2.2 ms | 3.5 ms |
| Warm, ratio by item | 1.15× to 1.92× | 0.56× to 0.77× | |

Saxon's stage 2 median was 2.9 ms in the v1 run, 2.6 ms at `4068c98c` and
3.5 ms in this one. The move from 0.89× at `4068c98c` to 0.64× is Saxon's.
go-xml's own median fell by 48% from v1, and by 1% from `4068c98c`.

## XQuery: XMark

XMark q1–q20 over the auction documents at factors 0.01 (1 MB) and 0.1
(11 MB), with the auction document as the context item. All 40 items agree
byte-for-byte across the three engines after whitespace collapse.

At factor 0.1 (milliseconds; Saxon and BaseX from the v2 run):

| Query | v1 warm | v2 warm | Saxon warm | BaseX warm | v1 cold | v2 cold | Saxon cold | BaseX cold |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| q1 | 51 | 40 | 47 | 108 | 64 | 47 | 480 | 649 |
| q2 | 53 | 41 | 45 | 92 | 67 | 48 | 515 | 661 |
| q3 | 57 | 43 | 46 | 93 | 71 | 49 | 510 | 598 |
| q4 | 55 | 42 | 48 | 111 | 68 | 48 | 453 | 628 |
| q5 | 50 | 41 | 46 | 107 | 65 | 47 | 445 | 576 |
| q6 | 60 | 40 | 48 | 106 | 72 | 48 | 414 | 556 |
| q7 | 72 | 40 | 47 | 92 | 84 | 47 | 430 | 582 |
| q8 | **58** | **45** | 218 | 1,207 | 71 | 52 | 658 | 665 |
| q9 | **59** | **48** | 313 | 1,288 | 75 | 56 | 778 | 2,183 |
| q10 | 94 | 79 | 84 | 259 | 113 | 87 | 658 | 695 |
| q11 | **76** | **65** | 123 | 3,119 | 93 | 73 | 655 | 4,004 |
| q12 | 75 | 64 | 79 | 752 | 91 | 72 | 683 | 1,296 |
| q13 | 54 | 41 | 46 | 97 | 67 | 48 | 498 | 671 |
| q14 | 61 | 44 | 50 | 116 | 78 | 52 | 508 | 667 |
| q15 | 51 | 40 | 45 | 111 | 64 | 48 | 508 | 658 |
| q16 | 52 | 40 | 45 | 106 | 65 | 48 | 495 | 672 |
| q17 | 53 | 42 | 46 | 109 | 67 | 49 | 497 | 673 |
| q18 | 53 | 41 | 48 | 89 | 67 | 50 | 504 | 667 |
| q19 | 59 | 47 | 48 | 94 | 75 | 53 | 501 | 687 |
| q20 | 55 | 43 | 47 | 93 | 68 | 51 | 514 | 685 |

- **The value joins are go-xml's strongest queries.** q8, q9 and q11 are
  0.19× to 0.64× Saxon's warm time in v1 and 0.15× to 0.53× in v2, and 21–43×
  (v1) and 27–48× (v2) faster than BaseX. They run as hash or range joins
  ([changelog](../CHANGELOG.md)).
- **The rest are 0.96× to 1.52× Saxon warm in v1 and 0.75× to 1.15× in v2.**
  At 11 MB v2 is 0.81× to 0.97× on these queries. The floor of about 40 ms on
  the 11 MB document is the parse, which every engine repeats per run; 82% of
  go-xml's XMark CPU is parsing ([profiling](profiling.md#xquery-xmark)). q10
  is v2's slowest query at 1 MB, 1.15×; at 11 MB it is 0.94×.
- **Cold, every query is faster than both JVMs**: 5× to 11× against Saxon at
  11 MB in v1 and 8× to 14× in v2.

## Schema validation

**XSD.** The XSLT 3.0 test-catalog schema (144 KB, XSD 1.0 with `vc:`
attributes) and the QT3 catalog schema (78 KB), validating 11 real catalog
files from 3.6 KB to 2.5 MB. They stand in for UBL 2.1, whose schemas are not
in the test corpus. All three validators agree on every verdict.

| | v1 | v2 | Xerces-J 1.1 | xmllint |
|---|---:|---:|---:|---:|
| Cold, median | 7.6 ms | 5.2 ms | 174 ms | 3.3 ms |
| Warm, median | 0.58 ms | 0.56 ms | 0.96 ms | — |
| Warm, geometric mean | 1.07 ms | 0.92 ms | 1.49 ms | — |
| Compile | 2.9 ms | 2.2 ms | 99 ms | — |

Warm, v1 ranges from 0.28× Xerces (faster, on small instances) to 1.68×
slower, and v2 from 0.24× to 1.44×. The slowest is the 2.5 MB `xp-striding`
catalog (v1 18.8 ms, v2 16.1 ms, Xerces 11.2 ms). go-xml's geometric mean fell
from 1.07 to 0.92 ms and the ratio from 0.73× to 0.61×; Xerces ran at 1.47 ms
in the v1 run and 1.49 ms in this one. libxml2's C validator is 1.4× to 2.2×
faster than v1 cold and 1.2× to 1.7× faster than v2; about 3 ms of go-xml's
cold time is process start. xmllint implements XSD 1.0 only.

**RELAX NG.** DocBook 5.2 (`docbook.rng`, 608 KB) over 40 DocBook test
documents, all 40 timed. 5 are rejected by all three validators and are timed
because the verdicts agree: `JFK_Inaugural` uses `dialogue`, which DocBook 5.2
does not define, and four carry unexpanded `xi:include` elements.

| | v1 | v2 | Jing | xmllint |
|---|---:|---:|---:|---:|
| Cold, median | 27 ms | 25 ms | 180 ms | 28 ms |
| Warm, median per document | 0.029 ms | 0.025 ms | 0.033 ms | — |
| Warm, geometric mean per document | 26.8 µs | 23.6 µs | 42.6 µs | — |
| Compile | 25 ms | 23 ms | 120 ms | — |

Cold time is the grammar compile, about a seventh of Jing's; v2 is faster than
xmllint on 39 of 40 documents (0.88×). **The warm ratio follows Jing.** go-xml's
geometric mean is 26.8 µs per document in v1 and 23.6 µs in v2 (24.5 µs at
`4068c98c`). The same Jing jar ran at 44.5 µs in the v1 run, 32.5 µs at
`4068c98c` and 42.6 µs in this one. So the ratio went 0.60× (v1), 0.76×
(`4068c98c`) and 0.55× (v2.0.0). The per-item ratio (0.04× to 1.85× in v1,
0.04× to 1.56× in v2) is noise around very small numbers.

## Parsing and Canonical XML

Documents generated with a fixed seed at 1, 10 and 100 MB. Cold runs parse and
write Canonical XML 1.0 to a file, so the outputs can be checked against
each other. Warm runs parse to a tree (go-xml), or tokenise and re-emit to a
null sink (`encoding/xml`).

| Size | v1 cold | v2 cold | v1 warm | v2 warm | encoding/xml cold | encoding/xml warm | xmllint cold |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 MB | 17 ms | 11 ms | 8.5 ms | 7.5 ms | 22 ms | 16 ms | 21 ms |
| 10 MB | 116 ms | 84 ms | 86 ms | 73 ms | 181 ms | 159 ms | 192 ms |
| 100 MB | 1,127 ms | 796 ms | 880 ms | 718 ms | 1,744 ms | 1,585 ms | 1,895 ms |

In both modes go-xml builds a full XDM tree (node identity, document order,
namespaces) and writes Canonical XML from it; warm runs write to a null sink.
At 100 MB that gives about **89 MB/s (v1) and 126 MB/s (v2)** cold for parse
plus C14N, and about 114 MB/s (v1) and 139 MB/s (v2) warm. `encoding/xml`
reaches about 63 MB/s warm, but it only tokenises and re-emits and builds no
tree. xmllint parses and writes C14N at about 53 MB/s.

The 100 MB document needs `MaxBytes: -1` (CLI `-max-bytes -1`). go-xml's
default 64 MB document limit is a deliberate guard against untrusted input.

Canonical XML (inclusive 1.0) of the 1 and 10 MB documents is faster than
`xmllint --c14n` cold: v1 19 and 112 ms, v2 11 and 85 ms, against 21 and
197 ms.

## Memory

Peak RSS in cold runs (median / maximum over items):

| Workload | v1 | v2 | Reference |
|---|---:|---:|---:|
| DocBook xslTNG | 84 / 147 MB | 54 / 77 MB | Saxon 240 / 297 MB |
| Peppol | 41 / 56 MB | 26 / 30 MB | Saxon 170 / 186 MB |
| XRechnung stage 1 | 32 / 38 MB | 18 / 19 MB | Saxon 127 / 136 MB |
| XRechnung stage 2 | 28 / 32 MB | 18 / 19 MB | Saxon 135 / 139 MB |
| XMark | 101 / 238 MB | 28 / 74 MB | Saxon 147 / 333 MB, BaseX 161 / 558 MB |
| XSD | 17 / 50 MB | 9 / 20 MB | Xerces 75 / 98 MB, xmllint 3 / 20 MB |
| RELAX NG DocBook | 38 / 39 MB | 21 / 24 MB | Jing 73 / 74 MB, xmllint 11 / 11 MB |
| Parse 1 MB | 37 MB | 11 MB | encoding/xml 12 MB, xmllint 16 MB |
| Parse 10 MB | 235 MB | **45 MB** | encoding/xml 30 MB, xmllint 142 MB |
| Parse 100 MB | 2.2 GB | **371 MB** | encoding/xml 203 MB, xmllint 1.4 GB |

For transforms v1 uses a fifth to two thirds of a JVM's memory, and v2 an
eighth to a quarter. The CLI runs its collector at `GOGC=200` unless `GOGC`
is set, which trades some peak memory for CPU. For large documents v1's tree
cost about 23 bytes of RSS per byte of input, 1.6× libxml2's; v2's costs
under 4, about a quarter of libxml2's. Only `encoding/xml`, which builds no
tree, uses less.

## Where the break-even is

From the cold and warm medians, a rough estimate of how many documents one
process must handle before the JVM's start and compile cost is paid back.
The fixed cost is the reference's cold median less go-xml's; the gap is
go-xml's warm median less the reference's.

| Workload | JVM fixed cost (v1 / v2 run) | Per-document gap, v1 | Per-document gap, v2 | Break-even, v1 | Break-even, v2 |
|---|---:|---:|---:|---:|---:|
| DocBook xslTNG | ~0.99 / 1.00 s | go-xml faster warm | go-xml faster warm | none | none |
| XMark, simple queries (11 MB) | ~0.41 / 0.45 s | ~7.9 ms | go-xml faster warm | ~50 documents | none |
| XMark, value joins (11 MB) | ~0.67 / 0.60 s | go-xml faster warm | go-xml faster warm | none | none |
| Peppol Schematron | ~0.67 / 0.67 s | ~1.0 ms | go-xml faster warm | ~650 documents | none |
| XRechnung stage 1 | ~0.53 / 0.52 s | ~4.1 ms | ~0.13 ms | ~130 documents | ~4,000 documents |
| XRechnung stage 2 | ~0.54 / 0.54 s | ~1.4 ms | go-xml faster warm | ~390 documents | none |
| XSD catalogs | ~0.17 / 0.17 s | go-xml faster warm | go-xml faster warm | none | none |

Below these batch sizes per process, go-xml finishes first. Above them, a warm
JVM does. These are estimates from medians, not measured crossovers; real
documents vary, and so does the JVM. Peppol and stage 1 are the sensitive
rows: at `4068c98c` Saxon's warm medians were 0.15 and 0.55 ms below go-xml's,
for break-evens of about 4,500 and 930 documents. In this run Saxon's Peppol
median is 0.13 ms above go-xml's.

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

The v1 run and every v2 run agree and disagree on exactly the same items.
The two differences left are not go-xml bugs:

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
  from run to run, and the reference engines vary too. In the v1 run, at
  `4068c98c` and in this run:

  | Reference figure | v1 run | `4068c98c` | v2.0.0 |
  |---|---:|---:|---:|
  | Jing, warm geometric mean per document | 44.5 µs | 32.5 µs | 42.6 µs |
  | Xerces, warm geometric mean | 1.47 ms | 1.49 ms | 1.49 ms |
  | Saxon, Peppol warm geometric mean | 1.46 ms | 1.29 ms | 1.60 ms |
  | Saxon, XRechnung stage 1 warm geometric mean | 2.16 ms | 2.27 ms | 2.60 ms |
  | Saxon, XRechnung stage 2 warm median | 2.9 ms | 2.6 ms | 3.5 ms |

  A ratio can move with no change in go-xml, so read go-xml's absolute times
  beside it. Ratios under about 1.3× are not meaningful, and neither is a
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
