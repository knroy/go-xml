# Benchmark: go-xml against established engines

How go-xml compares with Saxon-HE, BaseX, Jing, Xerces-J, libxml2 and Go's
`encoding/xml` on real workloads: DocBook rendering, e-invoice validation and
visualisation, XMark queries, schema validation, parsing and canonicalisation.
Each comparison is timed only after the engines are shown to produce the same
output.

**The short version.** go-xml wins wherever a process is started for a small
amount of work: a CLI call, a short-lived container, a per-request worker. It
starts in milliseconds where a JVM takes 0.5–1 s. It loses once a long-running
process has compiled once and is transforming in a loop: there Saxon is 2.5× to
20× faster per document, and up to 85× on XMark's value joins. It also uses
more memory per parsed byte than the C and Java engines.

Measured 2026-10-08 on the tree at `22f4b04`.

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
| go-xml | this tree (`22f4b04`) | everything |
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

| Workload | Items timed | Reference | Cold | Warm |
|---|---:|---|---:|---:|
| DocBook xslTNG → XHTML5 | 40 / 42 | Saxon-HE | **0.17×** (5.9× faster) | 2.54× slower |
| Peppol BIS Schematron → SVRL | 18 / 18 | Saxon-HE | **0.10×** (10× faster) | 20.4× slower |
| XRechnung UBL → xr:invoice | 8 / 8 | Saxon-HE | **0.08×** (12.5× faster) | 9.6× slower |
| XRechnung xr:invoice → HTML | 0 / 8 | Saxon-HE | — | — (see [findings](#correctness-findings)) |
| XMark q1–q20, 1 and 11 MB | 40 / 40 | Saxon-HE | **0.27×** (3.7× faster) | 5.2× slower |
| XMark q1–q20, 1 and 11 MB | 40 / 40 | BaseX | **0.21×** (4.8× faster) | 1.9× slower |
| XSD 1.0/1.1 (catalog schemas) | 11 / 11 | Xerces-J | **0.08×** (12.5× faster) | 1.8× slower |
| XSD 1.0/1.1 (catalog schemas) | 11 / 11 | xmllint | 2.9× slower | — |
| RELAX NG (DocBook 5.2) | 33 / 40 | Jing | 2.7× slower | 4.7× slower |
| RELAX NG (DocBook 5.2) | 33 / 40 | xmllint | 17× slower | — |
| Parse 1 / 10 / 100 MB | 3 / 3 | `encoding/xml` | 1.8× slower | 1.7× slower |
| Parse 1 / 10 / 100 MB | 3 / 3 | xmllint | 1.75× slower | — |
| Canonical XML 1 / 10 MB | 2 / 2 | xmllint | 1.6× slower | — |

Compile time (warm mode, median per workload):

| Workload | go-xml | Reference |
|---|---:|---:|
| DocBook xslTNG (`docbook.xsl` and its modules) | 101 ms | Saxon 980 ms |
| Peppol Schematron (compiled XSLT 2.0) | 54 ms | Saxon 697 ms |
| XRechnung UBL → xr | 19 ms | Saxon 478 ms |
| XMark query | 0.9 ms | Saxon 298 ms, BaseX 244 ms |
| XSD catalog schemas | 9.7 ms | Xerces 152 ms |
| DocBook 5.2 RELAX NG (608 KB) | **496 ms** | Jing 108 ms |

## XSLT: DocBook xslTNG

DocBook xslTNG 2.6.0 (`docbook.xsl`, XSLT 3.0) renders 42 of its own test
documents to XHTML5. They were chosen to span size (186 B to 54 KB) and
feature areas: books, sets, reference entries, tables, lists, synopses,
callouts, glossaries, bibliographies, indexes, footnotes, localisation and
dates.

| | go-xml | Saxon-HE |
|---|---:|---:|
| Cold, median over items | 165 ms | 1,081 ms |
| Warm, median over items | 68.6 ms | 32.6 ms |
| Compile | 101 ms | 980 ms |
| Peak RSS, cold | 104 MB | 275 MB |

Cold, go-xml is faster on every item (0.13× to 0.49×). Warm, it is 1.55× to
8.6× slower. The gap widens with document size and with index and
table-of-contents work:

| Item | go-xml warm | Saxon warm | Ratio |
|---|---:|---:|---:|
| `blocks.002` | 48.0 ms | 31.0 ms | 1.55× |
| `table-html.001` | 44.1 ms | 28.3 ms | 1.56× |
| `book.001` | 182 ms | 39.7 ms | 4.6× |
| `indexterm.001` | 368 ms | 49.5 ms | 7.4× |
| `chapter.003` | 428 ms | 50.9 ms | 8.4× |
| `ptoc.001` | 493 ms | 57.5 ms | 8.6× |

## XSLT: e-invoicing

**Peppol BIS Billing 3.0.** The CEN EN 16931 and Peppol Schematron rule sets,
compiled to XSLT 2.0 by SchXslt 1.10.1, run against each example invoice and
produce SVRL. This is the most lopsided result in the set:
- **Cold:** go-xml is 10× faster (78 ms against 687 ms).
- **Warm:** Saxon finishes an invoice in 1.1–2.5 ms; go-xml takes 15–65 ms,
  12.8× to 25.8× slower.

These stylesheets are hundreds of independent XPath assertions over one small
document. That is the shape where Saxon's bytecode generation and JIT pay off
most.

**XRechnung, stage 1** (KoSIT `ubl-invoice-xr.xsl`, XSLT 2.0): UBL invoices from
the KoSIT test suite to the intermediate `xr:invoice` XML.
- **Cold:** 44 ms against 555 ms (12.5× faster).
- **Warm:** 23.9 ms against 2.5 ms (9.6× slower).

**XRechnung, stage 2** (`xrechnung-html.xsl`): no item could be timed. See
[Correctness findings](#correctness-findings).

## XQuery: XMark

XMark q1–q20 over the auction documents at factors 0.01 (1 MB) and 0.1
(11 MB), with the auction document as the context item. All 40 items agree
byte-for-byte across the three engines after whitespace collapse.

At factor 0.1 (milliseconds):

| Query | go-xml warm | Saxon warm | BaseX warm | go-xml cold | Saxon cold | BaseX cold |
|---|---:|---:|---:|---:|---:|---:|
| q1 | 156 | 73 | 126 | 168 | 485 | 630 |
| q2 | 162 | 54 | 146 | 176 | 467 | 642 |
| q3 | 163 | 51 | 123 | 207 | 488 | 650 |
| q4 | 160 | 54 | 95 | 172 | 525 | 582 |
| q5 | 152 | 74 | 120 | 170 | 489 | 663 |
| q6 | 272 | 54 | 719 | 292 | 441 | 602 |
| q7 | 419 | 62 | 291 | 438 | 424 | 657 |
| q8 | 2,808 | 233 | 1,400 | 2,859 | 1,009 | 675 |
| q9 | 3,384 | 300 | 1,520 | 3,563 | 1,025 | 2,203 |
| q10 | 572 | 95 | 254 | 789 | 602 | 788 |
| q11 | 7,189 | 124 | 3,116 | 6,129 | 770 | 3,927 |
| q12 | 6,977 | 82 | 759 | 6,351 | 514 | 1,316 |
| q13 | 170 | 65 | 91 | 186 | 524 | 708 |
| q14 | 273 | 57 | 90 | 299 | 579 | 710 |
| q15 | 433 | 53 | 87 | 177 | 535 | 686 |
| q16 | 642 | 55 | 96 | 209 | 523 | 655 |
| q17 | 322 | 68 | 94 | 193 | 606 | 674 |
| q18 | 301 | 54 | 107 | 182 | 465 | 667 |
| q19 | 301 | 51 | 100 | 224 | 580 | 711 |
| q20 | 198 | 49 | 100 | 216 | 480 | 668 |

Three things stand out:

- **No go-xml query on the 11 MB document runs in under about 150 ms**, the
  time of q1–q5, which barely touch the data. That floor is largely the parse,
  which every engine repeats per run. Saxon's floor on the same queries is
  50–75 ms.
- **The value joins q8, q9, q11 and q12 are go-xml's real weakness**: 12× to 85×
  slower than Saxon warm, and the only queries where go-xml loses cold as
  well. go-xml evaluates these joins as written, a nested loop. Saxon's and BaseX's
  timings show they do not, though how each one rewrites them was not
  examined. At factor 0.01 the same queries are
  already 12–13× slower, so the cost grows faster than the document.
- **An anomaly we have not explained:** for q15–q19 at 0.1, go-xml's warm
  median is higher than its cold one (433 ms against 177 ms for q15). A cold
  run is one evaluation in a fresh process; the warm loop re-parses the 11 MB
  document 36 times in one heap. Garbage-collector pressure from the retained
  trees is the likely cause, but it has not been profiled.

## Schema validation

**XSD.** The XSLT 3.0 test-catalog schema (144 KB, XSD 1.0 with `vc:`
attributes) and the QT3 catalog schema (78 KB), validating 11 real catalog
files from 3.6 KB to 2.5 MB. They stand in for UBL 2.1, whose schemas are not
in the test corpus. All three validators agree on every verdict.

| | go-xml | Xerces-J 1.1 | xmllint |
|---|---:|---:|---:|
| Cold, median | 11.5 ms | 216 ms | 4.5 ms |
| Warm, median | 2.8 ms | 1.4 ms | — |
| Compile | 9.7 ms | 152 ms | — |

Warm, go-xml ranges from 0.49× (faster, on small instances) to 6.3× slower
(the 2.5 MB `xp-striding` catalog). libxml2's C validator is about 2.9×
faster than go-xml cold, but it implements XSD 1.0 only.

**RELAX NG.** DocBook 5.2 (`docbook.rng`, 608 KB) over 40 DocBook test
documents. 33 were timed; go-xml rejects the other 7, which both other
validators accept (see findings).

| | go-xml | Jing | xmllint |
|---|---:|---:|---:|
| Cold, median | 494 ms | 178 ms | 28 ms |
| Warm, median per document | 0.1 ms | 0.03 ms | — |
| Compile | 496 ms | 108 ms | — |

Nearly all of go-xml's cold time is compiling the 608 KB grammar. Validating
once compiled is sub-millisecond; the warm ratio against Jing (0.27× to 31×)
is noise around very small numbers. Compiling DocBook 5.2 was impossible
before this benchmark: see [Correctness findings](#correctness-findings).

## Parsing and Canonical XML

Documents generated with a fixed seed at 1, 10 and 100 MB. Cold runs parse and
write Canonical XML 1.0 to a file, so the outputs can be checked against
each other. Warm runs parse to a tree (go-xml), or tokenise and re-emit to a
null sink (`encoding/xml`).

| Size | go-xml cold | go-xml warm | encoding/xml cold | encoding/xml warm | xmllint cold |
|---:|---:|---:|---:|---:|---:|
| 1 MB | 35 ms | 27 ms | 23 ms | 16 ms | 21 ms |
| 10 MB | 298 ms | 268 ms | 186 ms | 158 ms | 202 ms |
| 100 MB | 4,055 ms | 2,780 ms | 1,742 ms | 1,579 ms | 1,900 ms |

In both modes go-xml builds a full XDM tree (node identity, document order,
namespaces) and writes Canonical XML from it; warm runs write to a null sink.
That gives about **36–37 MB/s** for parse plus C14N. `encoding/xml` reaches
about 63 MB/s, but it only tokenises and re-emits, and builds no tree.
xmllint parses and writes C14N at about 50 MB/s.

The 100 MB document needs `MaxBytes: -1`. go-xml's default 64 MB document
limit is a deliberate guard against untrusted input, and the CLI has no flag
to raise it.

Canonical XML (inclusive 1.0) of the 1 and 10 MB documents is 1.6× slower
than `xmllint --c14n` cold (36 against 21 ms, and 299 against 194 ms). That is
the parse cost again.

## Memory

Peak RSS in cold runs:

| Workload | go-xml | Reference |
|---|---:|---:|
| DocBook xslTNG | 104 MB | Saxon 275 MB |
| Peppol | 68 MB | Saxon 183 MB |
| XRechnung stage 1 | 33 MB | Saxon 127 MB |
| XMark (median / max) | 236 / 466 MB | Saxon 172 / 333 MB, BaseX 162 / 583 MB |
| XSD | 19 MB | Xerces 75 MB, xmllint 3 MB |
| RELAX NG DocBook | 188 MB | Jing 75 MB, xmllint 11 MB |
| Parse 10 MB | 379 MB | encoding/xml 35 MB, xmllint 142 MB |
| Parse 100 MB | **3.5 GB** | encoding/xml 209 MB, xmllint 1.4 GB |

For small transforms go-xml uses a third of a JVM's memory. For large
documents it is the reverse: the XDM tree costs about 35 bytes of RSS per byte
of input, 2.5× libxml2's. The 64 MB default document limit exists partly for
this reason. A 64 MB document needs roughly 2.2 GB.

## Where the break-even is

From the cold and warm medians, a rough estimate of how many documents one
process must handle before the JVM's start and compile cost is paid back:

| Workload | Saxon/Xerces fixed cost | Per-document gap | Break-even |
|---|---:|---:|---:|
| DocBook xslTNG | ~1.0 s | ~36 ms | ~30 documents |
| Peppol Schematron | ~0.69 s | ~26 ms | ~25 documents |
| XRechnung stage 1 | ~0.55 s | ~21 ms | ~25 documents |
| XSD catalogs | ~0.21 s | ~1.4 ms | ~150 documents |

Below these batch sizes per process, go-xml finishes first. Above them, a warm
JVM does. These are estimates from medians, not measured crossovers; real
documents vary.

## What could not be measured

- **XMark at factor 1 (111 MB).** The go-xml CLI refuses documents over 64 MB
  and has no flag to raise the limit. The library can (`MaxBytes: -1`, as the
  parse workload does), but XMark runs through the CLI.
- **XRechnung stage 2 (HTML).** No item agreed with Saxon, so nothing was timed
  (below).
- **xsltproc.** Every XSLT workload here is XSLT 2.0 or 3.0, and libxslt
  implements 1.0 only.
- **libxml2 warm mode.** There is no in-process harness for the C tools, so
  they appear in cold mode only.
- **UBL 2.1 XSD.** The UBL schemas are not part of the test corpus; the W3C
  catalog schemas stand in for them.

## Correctness findings

The agreement check found real bugs. Four were fixed before the timed run:

| Found by | Problem | Fix |
|---|---|---|
| Peppol (SchXslt output) | A `version="2.0"` stylesheet was refused `match="root()"` and other 3.0 pattern forms | `ca09e14`: a 3.0 processor applies the 3.0 pattern grammar |
| XRechnung HTML | A late `xsl:import` in a 2.0 stylesheet was `XTSE0200` | `04ddeea`: only a 2.0 processor applies that rule |
| DocBook 5.2 RELAX NG | Every `<ref>` recompiled its definition, so cost multiplied along chains and hit the 200,000-expansion limit | `eb6901e`: compiled once and shared; now 0.5 s |
| DocBook 5.2 RNC | A free-standing annotation element among definitions was refused | `197eaad` |

Still open:

- **RELAX NG: go-xml rejects 7 of 40 valid DocBook documents**
  (`bibliography.006`, `book.006`, `glossary.007`, `index.002`,
  `oxy-changemarkup.001`, `programlisting.004`, `xref.001`). Jing and xmllint
  both accept them. The cause is whitespace handling in the validator (RELAX NG
  §6.2.7).
- **XRechnung HTML: the stylesheet's `<meta charset="UTF-8"/>` is dropped.**
  With `include-content-type="no"`, go-xml's html method still removes an
  existing `<meta charset>` from `<head>`, so the output loses an element the
  stylesheet wrote. The run also showed a second difference that is not a bug:
  - The stylesheet (`version="2.0"`) gives no `html-version`, whose default
    XSLT 3.0 §26 leaves implementation-defined.
  - Saxon serialises as HTML5 and writes `<!DOCTYPE HTML>`; go-xml uses HTML 4
    and writes none.
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
  everything it needs. That is the steady state of a long-running service, and
  it is where go-xml is slower.
- **go-xml re-parses each input in both modes**, as the other engines do. No
  engine was allowed to cache documents across passes.
- **The harness is not part of the repository.** The engine downloads,
  corpora and generated inputs are large, and none is third-party code this
  repository should carry. The method above is complete enough to rebuild it.
  The figures here come from the run recorded at the top of this page.
