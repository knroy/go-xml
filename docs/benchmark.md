# Benchmark: go-xml v1 and v2 against established engines

How go-xml v1 and v2 compare with Saxon-HE, BaseX, Jing, Xerces-J, libxml2
and Go's `encoding/xml` on real workloads: DocBook rendering, e-invoice
validation and visualisation, XMark queries, schema validation, parsing and
canonicalisation. Each comparison is timed only after the engines are shown to
produce the same output.

**The short version.** Cold, both versions finish first everywhere except
small XSD validations, where xmllint's C start-up is quicker (1.9× against v1,
1.5× against v2); go-xml starts in milliseconds where a JVM takes 0.4–1 s.
Warm, in a long-running process, v2 is 5.2× faster than Saxon on DocBook and
faster on all 40 documents (v1: 2.2×, 36 of 40), 0.84× Saxon on XMark and 3.0×
faster than BaseX, and faster than Saxon on XRechnung stage 2 (0.89×), where v1
was 1.4× slower. It is faster than Xerces on XSD and than Jing on RELAX NG, as
v1 was. Saxon still leads on Peppol, by 1.16× (v1 1.99×), and on XRechnung
stage 1, by 1.25× (v1 3.00×). On large documents v2 needs a sixth to a third
of v1's peak memory, and less than libxml2.

Measured on one machine with the same harness and engine builds, otherwise
idle: v1 at `f45068c` (2026-10-08/09, after the round-3 and round-4 work in
[profiling](profiling.md)), and v2 as the `v2` branch at `4068c98c` on
2026-10-10 (after the 40-byte node records, the split evaluation context, the
allocation cuts, and fix rounds 1–3; see
[profiling](profiling.md#where-go-xml-stands)). The reference engines were
re-run with each version, and every ratio uses the reference times from its
own run. Two earlier v2 runs are cited below where they explain a move: "pre-fix
v2", before the fix rounds, and "v2 at `377452c0`" (2026-10-09), after rounds
1 and 2. They and the earlier runs at `eb14939` and `22f4b04` are in this
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
| go-xml v2 | `4068c98c` (the `v2` branch on 2026-10-10) | everything |
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
`4068c98c`; the v1 run's are within noise of them except where noted (see
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
| DocBook xslTNG → XHTML5 | 40 / 42 | Saxon-HE | **0.09×** (12× faster) | **0.08×** (12× faster) | **0.46×** (2.2× faster) | **0.19×** (5.2× faster) |
| Peppol BIS Schematron → SVRL | 18 / 18 | Saxon-HE | **0.04×** (25× faster) | **0.04×** (26× faster) | 1.99× slower | 1.16× slower |
| XRechnung UBL → xr:invoice | 8 / 8 | Saxon-HE | **0.03×** (29× faster) | **0.03×** (40× faster) | 3.00× slower | 1.25× slower |
| XRechnung xr:invoice → HTML | 8 / 8 | Saxon-HE | **0.03×** (37× faster) | **0.02×** (45× faster) | 1.42× slower | **0.89×** (1.1× faster) |
| XMark q1–q20, 1 and 11 MB | 40 / 40 | Saxon-HE | **0.07×** (14× faster) | **0.05×** (18× faster) | 1.00× (level) | **0.84×** (1.2× faster) |
| XMark q1–q20, 1 and 11 MB | 40 / 40 | BaseX | **0.05×** (18× faster) | **0.04×** (25× faster) | **0.39×** (2.6× faster) | **0.33×** (3.0× faster) |
| XSD 1.0/1.1 (catalog schemas) | 11 / 11 | Xerces-J | **0.05×** (18× faster) | **0.04×** (24× faster) | **0.73×** (1.4× faster) | **0.67×** (1.5× faster) |
| XSD 1.0/1.1 (catalog schemas) | 11 / 11 | xmllint | 1.87× slower | 1.53× slower | — | — |
| RELAX NG (DocBook 5.2) | 40 / 40 | Jing | **0.15×** (6.6× faster) | **0.14×** (7.0× faster) | **0.60×** | **0.76×** |
| RELAX NG (DocBook 5.2) | 40 / 40 | xmllint | 0.96× (level) | 0.89× (level) | — | — |
| Parse 1 / 10 / 100 MB | 3 / 3 | `encoding/xml` | **0.66×** | **0.58×** | **0.54×** | **0.57×** |
| Parse 1 / 10 / 100 MB | 3 / 3 | xmllint | **0.66×** | **0.56×** | — | — |
| Canonical XML 1 / 10 MB | 2 / 2 | xmllint | **0.72×** | **0.60×** | — | — |

The parse and C14N cold figures come from a helper that runs as the CLI does
(see [Reading these numbers](#reading-these-numbers)). The RELAX NG and XSD
warm ratios depend on how fast the JVM validators ran in each run more than on
go-xml (see [Schema validation](#schema-validation)).

Compile time (warm mode, median per workload):

| Workload | v1 | v2 | Reference |
|---|---:|---:|---:|
| DocBook xslTNG (`docbook.xsl` and its modules) | 55 ms | 59 ms | Saxon 808 ms |
| Peppol Schematron (compiled XSLT 2.0; CEN and PEPPOL) | 23 ms | 22 ms | Saxon 585 ms |
| XRechnung UBL → xr | 6.6 ms | 7.6 ms | Saxon 428 ms |
| XRechnung xr → HTML | 7.8 ms | 6.3 ms | Saxon 443 ms |
| XMark query | 1.0 ms | 0.35 ms | Saxon 217 ms, BaseX 225 ms |
| XSD catalog schemas | 2.9 ms | 2.3 ms | Xerces 99 ms |
| DocBook 5.2 RELAX NG (608 KB) | 25 ms | 24 ms | Jing 108 ms |

### What changed from v1 to v2

**DocBook warm.** 0.46× to 0.19× Saxon (pre-fix v2: 0.37×; v2 at `377452c0`:
0.24×); the median document went from 13.0 to 5.3 ms. All four items v1 lost
are now faster than Saxon; the slowest, `ptoc.001`, went from 1.49× to 0.86×
(pre-fix v2: 1.17×; `377452c0`: 0.98×). Most of the gain from the first two
fix rounds is lazy globals (V4): DocBook runs several transforms per document
with 300–950 globals each and evaluates few of them. The third round's XSLT
items (V17, V43–V51) took it from 0.24× to 0.19×.

**Schematron-shaped XSLT.** The split evaluation context, the allocation cuts
and the three fix rounds took most of the per-assertion overhead out of the
e-invoice stylesheets: Peppol went from 1.99× to 1.16× Saxon warm (pre-fix v2
1.31×, `377452c0` 1.22×), XRechnung stage 1 from 3.00× to 1.25× (1.77×,
1.46×), stage 2 from 1.42× to 0.89× (1.23×, 0.75×). go-xml's stage 2 median
went from 4.3 ms (v1) and 3.3 ms (pre-fix) to 2.6 ms (`377452c0`) and 2.3 ms
now. The stage 2 ratio rose from 0.75× at `377452c0` because Saxon's own
median was 3.4 ms in that run and 2.6 ms in this one; go-xml is faster than
Saxon on 6 of the 8 invoices. Saxon still leads on Peppol and stage 1;
[profiling](profiling.md#open-fixes) ranks what is left.

**Memory.** v2 stores each node as a 40-byte record instead of a Go object
per node. Peak RSS on the 100 MB parse fell from 2.2 GB to 371 MB, below
xmllint's 1.4 GB; XMark's median fell from 101 to 28 MB, and the transforms
use 35–44% less. Cold parse got faster with it (0.66× to 0.58×
`encoding/xml`), because the smaller heap needs less memory mapped in.

**Two pre-fix regressions are gone:**
- **DocBook compile** went from 55 ms in v1 to 77 ms in pre-fix v2, from
  attribute lookups through the name table; it is 59 ms now (V7). DocBook cold
  is 85 ms against v1's 89 ms (pre-fix v2: 104 ms).
- **XMark q10** at 11 MB went from 94 ms in v1 to 122 ms in pre-fix v2,
  because XQuery element content was copied twice per constructed node; it is
  82 ms now (V1, then V38 and V39; 88 ms at `377452c0`). XMark overall went
  from 1.00× to 0.84× Saxon, with 34 of 40 items faster than Saxon (v1: 9).

**Parse warm** went from 0.54× `encoding/xml` (v1) to 0.57× (pre-fix v2) to
0.53× (`377452c0`): the warm item also writes Canonical XML, and the C14N write
buffer (V11) recovered what the record accessors had cost. It is 0.57× in this
run. `encoding/xml` was level between the two runs (1,569 and 1,582 ms at
100 MB), so the move is go-xml's: 839 to 890 ms warm and 901 to 954 ms cold at
100 MB, 5–7% at every size. That is inside run-to-run noise, and the round-3
parse items measured faster in isolation (V34, −5 to −8% CPU); it has not been
bisected.

**RELAX NG and XSD warm: the ratios follow the JVM validators.** The JVM
validators ran at different speeds in the four runs. Jing's warm geometric
mean per document was 44.5 µs in the v1 run, 37.9 µs in the pre-fix v2 run,
30.4 µs at `377452c0` and 32.5 µs in this one; Xerces's was 1.47, 1.53, 1.39
and 1.49 ms. go-xml's RELAX NG time went from 26.8 µs per document in v1 to
28.4 µs in pre-fix v2, 27.6 µs at `377452c0` and 24.5 µs now (V22, V23), so
the ratio went from 0.60× to 0.91× Jing and back to 0.76×. On XSD go-xml went
from 1.07 to 1.02 to 0.99 ms; the ratio was 0.73× Xerces in v1 and at
`377452c0` and is 0.67× now, partly because Xerces ran 7% slower than at
`377452c0`. See
[profiling](profiling.md#the-three-apparent-regressions) for the per-document
set-up cost behind the small RELAX NG documents.

## XSLT: DocBook xslTNG

DocBook xslTNG 2.6.0 (`docbook.xsl`, XSLT 3.0) renders 42 of its own test
documents to XHTML5. They were chosen to span size (186 B to 54 KB) and
feature areas: books, sets, reference entries, tables, lists, synopses,
callouts, glossaries, bibliographies, indexes, footnotes, localisation and
dates.

| | v1 | v2 | Saxon-HE |
|---|---:|---:|---:|
| Cold, median over items | 89 ms | 85 ms | 1,056 ms |
| Warm, median over items | 13.0 ms | 5.3 ms | 32.6 ms |
| Compile | 55 ms | 59 ms | 808 ms |
| Peak RSS, cold (median) | 84 MB | 54 MB | 271 MB |

Cold, both versions are faster on every item (v1 0.08× to 0.13×, v2 0.08× to
0.11×). Warm, v1 is faster on 36 of 40 items (0.27× to 1.49×) and v2 on all
40 (0.05× to 0.86×). The items v1 lost are the large ones with index or
table-of-contents work:

| Item | v1 warm | v2 warm | Saxon warm | Ratio, v1 | Ratio, v2 |
|---|---:|---:|---:|---:|---:|
| `table-html.001` | 7.9 ms | 1.4 ms | 28.5 ms | 0.27× | 0.05× |
| `blocks.002` | 10.1 ms | 3.1 ms | 30.7 ms | 0.31× | 0.10× |
| `book.001` | 31.3 ms | 16.0 ms | 39.4 ms | 0.76× | 0.41× |
| `epub.001` | 46.1 ms | 24.5 ms | 39.4 ms | 1.13× | 0.62× |
| `indexterm.001` | 63.6 ms | 38.5 ms | 49.4 ms | 1.24× | 0.78× |
| `chapter.003` | 66.4 ms | 35.3 ms | 51.0 ms | 1.25× | 0.69× |
| `ptoc.001` | 88.3 ms | 49.6 ms | 57.6 ms | 1.49× | 0.86× |

## XSLT: e-invoicing

**Peppol BIS Billing 3.0.** The CEN EN 16931 and Peppol Schematron rule sets,
compiled to XSLT 2.0 by SchXslt 1.10.1, run against each example invoice and
produce SVRL.

| | v1 | v2 | Saxon-HE |
|---|---:|---:|---:|
| Cold, median | 29 ms | 28 ms | 678 ms |
| Warm, per invoice | 1.7–6.7 ms | 0.8–3.5 ms | 0.8–2.5 ms |
| Warm, ratio by item | 1.66× to 2.61× | 0.96× to 1.36× | |

These stylesheets are hundreds of independent XPath assertions over one small
document. That is the shape where Saxon's bytecode generation and JIT pay off
most. Round 4 measured that interpreting the expressions is only 2–4% of
go-xml's time here; the rest was allocation and collection, which is what v2's
node and context changes and the three fix rounds cut
([profiling](profiling.md#measured-and-rejected)). v2 is faster than Saxon on
two of the 18 invoices.

**XRechnung, stage 1** (KoSIT `ubl-invoice-xr.xsl`, XSLT 2.0): UBL invoices from
the KoSIT test suite to the intermediate `xr:invoice` XML.

| | v1 | v2 | Saxon-HE |
|---|---:|---:|---:|
| Cold, median | 18 ms | 13 ms | 528 ms |
| Warm, median | 6.2 ms | 2.7 ms | 2.1 ms |
| Warm, ratio by item | 2.11× to 4.47× | 1.00× to 1.58× | |

**XRechnung, stage 2** (`xrechnung-html.xsl`, XSLT 2.0): stage 1's output to
HTML. All 8 items agree with Saxon in both runs (see
[Correctness findings](#correctness-findings)).

| | v1 | v2 | Saxon-HE |
|---|---:|---:|---:|
| Cold, median | 15 ms | 13 ms | 550 ms |
| Warm, median | 4.3 ms | 2.3 ms | 2.6 ms |
| Warm, ratio by item | 1.15× to 1.92× | 0.68× to 1.22× | |

Saxon's stage 2 median was 2.9 ms in the v1 run, 3.4 ms at `377452c0` and
2.6 ms in this one, so the move from 0.75× at `377452c0` to 0.89× is Saxon's;
go-xml's own median fell by 47% from v1, and by 12% from `377452c0`.

## XQuery: XMark

XMark q1–q20 over the auction documents at factors 0.01 (1 MB) and 0.1
(11 MB), with the auction document as the context item. All 40 items agree
byte-for-byte across the three engines after whitespace collapse.

At factor 0.1 (milliseconds; Saxon and BaseX from the v2 run):

| Query | v1 warm | v2 warm | Saxon warm | BaseX warm | v1 cold | v2 cold | Saxon cold | BaseX cold |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| q1 | 51 | 45 | 46 | 89 | 64 | 50 | 417 | 563 |
| q2 | 53 | 46 | 45 | 87 | 67 | 51 | 420 | 578 |
| q3 | 57 | 48 | 47 | 93 | 71 | 53 | 445 | 584 |
| q4 | 55 | 48 | 46 | 91 | 68 | 52 | 454 | 578 |
| q5 | 50 | 45 | 45 | 108 | 65 | 51 | 448 | 566 |
| q6 | 60 | 45 | 46 | 87 | 72 | 51 | 416 | 565 |
| q7 | 72 | 43 | 46 | 88 | 84 | 51 | 430 | 644 |
| q8 | **58** | **48** | 220 | 1,251 | 71 | 56 | 854 | 653 |
| q9 | **59** | **51** | 279 | 1,234 | 75 | 59 | 1,014 | 2,087 |
| q10 | 94 | 82 | 86 | 256 | 113 | 91 | 534 | 667 |
| q11 | **76** | **67** | 123 | 3,058 | 93 | 76 | 610 | 3,883 |
| q12 | 75 | 66 | 75 | 721 | 91 | 74 | 505 | 1,279 |
| q13 | 54 | 44 | 45 | 92 | 67 | 52 | 440 | 583 |
| q14 | 61 | 47 | 51 | 93 | 78 | 55 | 429 | 574 |
| q15 | 51 | 43 | 45 | 86 | 64 | 50 | 408 | 563 |
| q16 | 52 | 43 | 45 | 88 | 65 | 51 | 472 | 622 |
| q17 | 53 | 45 | 46 | 88 | 67 | 52 | 513 | 628 |
| q18 | 53 | 45 | 47 | 90 | 67 | 53 | 523 | 624 |
| q19 | 59 | 49 | 48 | 93 | 75 | 57 | 486 | 602 |
| q20 | 55 | 46 | 49 | 92 | 68 | 54 | 487 | 646 |

- **The value joins are go-xml's strongest queries.** q8, q9 and q11 are
  0.19× to 0.64× Saxon's warm time in v1 and 0.18× to 0.55× in v2, and 21–43×
  (v1) and 24–45× (v2) faster than BaseX. They run as hash or range joins
  ([changelog](../CHANGELOG.md)).
- **The rest are 0.96× to 1.52× Saxon warm in v1 and 0.83× to 1.19× in v2.**
  At 11 MB v2 is 0.89× to 1.04× on these queries. The floor of about 45 ms on
  the 11 MB document is the parse, which every engine repeats per run; Saxon's
  is about the same. q10 is v2's slowest query at 1 MB, 1.19×; at 11 MB it is
  0.95× (see [What changed](#what-changed-from-v1-to-v2)).
- **Cold, every query is faster than both JVMs**: 5× to 11× against Saxon at
  11 MB in v1 and 6× to 17× in v2. The top of v2's range is Saxon's: its cold
  q8 and q9 took 854 and 1,014 ms in this run against 651 and 687 ms at
  `377452c0`.

## Schema validation

**XSD.** The XSLT 3.0 test-catalog schema (144 KB, XSD 1.0 with `vc:`
attributes) and the QT3 catalog schema (78 KB), validating 11 real catalog
files from 3.6 KB to 2.5 MB. They stand in for UBL 2.1, whose schemas are not
in the test corpus. All three validators agree on every verdict.

| | v1 | v2 | Xerces-J 1.1 | xmllint |
|---|---:|---:|---:|---:|
| Cold, median | 7.6 ms | 5.2 ms | 174 ms | 3.4 ms |
| Warm, median | 0.58 ms | 0.58 ms | 0.98 ms | — |
| Warm, geometric mean | 1.07 ms | 0.99 ms | 1.49 ms | — |
| Compile | 2.9 ms | 2.3 ms | 99 ms | — |

Warm, v1 ranges from 0.28× Xerces (faster, on small instances) to 1.68×
slower, and v2 from 0.25× to 1.74×; the slowest is the 2.5 MB `xp-striding`
catalog (v1 18.8 ms, v2 19.0 ms, Xerces 11.0 ms). go-xml's geometric mean fell
from 1.07 to 0.99 ms and the ratio from 0.73× to 0.67×. Xerces ran at 1.47 ms
in the v1 run and 1.49 ms in this one; at `377452c0` it ran at 1.39 ms, and
go-xml's 1.02 ms gave 0.73×. libxml2's C validator is 1.4×
to 2.2× faster than v1 cold and 1.2× to 1.7× faster than v2; about 3 ms of
go-xml's cold time is process start ([profiling](profiling.md#open-fixes)).
xmllint implements XSD 1.0 only.

**RELAX NG.** DocBook 5.2 (`docbook.rng`, 608 KB) over 40 DocBook test
documents, all 40 timed. 5 are rejected by all three validators and are timed
because the verdicts agree: `JFK_Inaugural` uses `dialogue`, which DocBook 5.2
does not define, and four carry unexpanded `xi:include` elements.

| | v1 | v2 | Jing | xmllint |
|---|---:|---:|---:|---:|
| Cold, median | 27 ms | 25 ms | 172 ms | 28 ms |
| Warm, median per document | 0.029 ms | 0.026 ms | 0.025 ms | — |
| Warm, geometric mean per document | 26.8 µs | 24.5 µs | 32.5 µs | — |
| Compile | 25 ms | 24 ms | 108 ms | — |

Cold time is the grammar compile, about a seventh of Jing's; v2 is faster than
xmllint on all 40 documents (0.89×). Validating once compiled takes about as
long as Jing in both versions. **The warm ratio moved from 0.60× to 0.76× Jing
although go-xml got faster**: its geometric mean is 26.8 µs per document in
v1 and 24.5 µs in v2 (28.4 µs in pre-fix v2, 27.6 µs at `377452c0`), while the
same Jing jar ran at 44.5 µs in the v1 run, 37.9 µs in the pre-fix v2 run,
30.4 µs at `377452c0` and 32.5 µs in this one. The per-item ratio (0.04× to
1.85× in v1, 0.06× to 2.20× in v2) is noise around very small numbers.

## Parsing and Canonical XML

Documents generated with a fixed seed at 1, 10 and 100 MB. Cold runs parse and
write Canonical XML 1.0 to a file, so the outputs can be checked against
each other. Warm runs parse to a tree (go-xml), or tokenise and re-emit to a
null sink (`encoding/xml`).

| Size | v1 cold | v2 cold | v1 warm | v2 warm | encoding/xml cold | encoding/xml warm | xmllint cold |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 MB | 17 ms | 13 ms | 8.5 ms | 9.2 ms | 22 ms | 16 ms | 21 ms |
| 10 MB | 116 ms | 100 ms | 86 ms | 90 ms | 178 ms | 158 ms | 192 ms |
| 100 MB | 1,127 ms | 954 ms | 880 ms | 890 ms | 1,738 ms | 1,582 ms | 1,888 ms |

In both modes go-xml builds a full XDM tree (node identity, document order,
namespaces) and writes Canonical XML from it; warm runs write to a null sink.
That gives about **90 MB/s (v1) and 105 MB/s (v2)** cold for parse plus C14N,
and about 114 MB/s (v1) and 112 MB/s (v2) warm. `encoding/xml` reaches about
63 MB/s warm, but it only tokenises and re-emits and builds no tree. xmllint
parses and writes C14N at about 53 MB/s.

The 100 MB document needs `MaxBytes: -1` (CLI `-max-bytes -1`). go-xml's
default 64 MB document limit is a deliberate guard against untrusted input.

Canonical XML (inclusive 1.0) of the 1 and 10 MB documents is faster than
`xmllint --c14n` cold: v1 19 and 112 ms, v2 12.8 and 105 ms, against 20 and
189 ms.

## Memory

Peak RSS in cold runs (median / maximum over items):

| Workload | v1 | v2 | Reference |
|---|---:|---:|---:|
| DocBook xslTNG | 84 / 147 MB | 54 / 76 MB | Saxon 271 / 299 MB |
| Peppol | 41 / 56 MB | 26 / 31 MB | Saxon 182 / 205 MB |
| XRechnung stage 1 | 32 / 38 MB | 18 / 19 MB | Saxon 127 / 140 MB |
| XRechnung stage 2 | 28 / 32 MB | 18 / 19 MB | Saxon 135 / 140 MB |
| XMark | 101 / 238 MB | 28 / 74 MB | Saxon 151 / 334 MB, BaseX 177 / 556 MB |
| XSD | 17 / 50 MB | 9 / 20 MB | Xerces 77 / 96 MB, xmllint 3 / 19 MB |
| RELAX NG DocBook | 38 / 39 MB | 21 / 24 MB | Jing 75 / 79 MB, xmllint 11 / 12 MB |
| Parse 1 MB | 37 MB | 11 MB | encoding/xml 13 MB, xmllint 16 MB |
| Parse 10 MB | 235 MB | **45 MB** | encoding/xml 30 MB, xmllint 142 MB |
| Parse 100 MB | 2.2 GB | **371 MB** | encoding/xml 203 MB, xmllint 1.4 GB |

For transforms v1 uses a fifth to two thirds of a JVM's memory, and v2 a
seventh to a fifth. The CLI runs its collector at `GOGC=200` unless `GOGC` is
set, which trades some peak memory for CPU. For large documents v1's tree
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
| DocBook xslTNG | ~0.99 / 0.97 s | go-xml faster warm | go-xml faster warm | none | none |
| XMark, simple queries (11 MB) | ~0.41 / 0.40 s | ~7.9 ms | go-xml faster warm | ~50 documents | none |
| XMark, value joins (11 MB) | ~0.67 / 0.80 s | go-xml faster warm | go-xml faster warm | none | none |
| Peppol Schematron | ~0.67 / 0.65 s | ~1.0 ms | ~0.15 ms | ~650 documents | ~4,500 documents |
| XRechnung stage 1 | ~0.53 / 0.52 s | ~4.1 ms | ~0.55 ms | ~130 documents | ~930 documents |
| XRechnung stage 2 | ~0.54 / 0.54 s | ~1.4 ms | go-xml faster warm | ~390 documents | none |
| XSD catalogs | ~0.17 / 0.17 s | go-xml faster warm | go-xml faster warm | none | none |

Below these batch sizes per process, go-xml finishes first. Above them, a warm
JVM does. These are estimates from medians, not measured crossovers; real
documents vary. On XMark's simple queries v2's warm median is 0.95 ms below
Saxon's (at `377452c0` the medians differed by 0.02 ms), so the JVM never pays
its start back. The value joins' fixed cost rose from 0.59 s at `377452c0` to
0.80 s because Saxon's cold q8 and q9 were slower in this run.

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

The v1 run and all three v2 runs agree and disagree on exactly the same items.
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
  from run to run, and the reference engines did too between runs. Jing's
  warm geometric mean was 44.5 µs in the v1 run, 37.9 µs in the pre-fix v2
  run, 30.4 µs at `377452c0` and 32.5 µs in this one; Xerces's was 1.47,
  1.53, 1.39 and 1.49 ms; Saxon's XRechnung stage 2 median was 2.9, 2.8, 3.4
  and 2.6 ms. A ratio can move with no
  change in go-xml, so read go-xml's absolute times beside it. Ratios under
  about 1.3× are not meaningful, and neither is a v1-to-v2 move of a few
  percent; the large ones are.
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
