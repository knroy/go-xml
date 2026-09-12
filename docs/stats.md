# Statistics

Every measured figure this repository publishes, with the command that
produced it and the date it was measured.

**This file is generated.** It is written by `tests/conformance-docs.go` from
`tests/conformance/results.json` and from the source tree, and every figure
quoted anywhere else in this repository is a generated region fed from the same
two places. Do not edit it; edit the JSON, or the tree, and regenerate:

```sh
go run tests/conformance-docs.go          # rewrite every generated region
go run tests/conformance-docs.go -check   # fail if any has drifted
```

## Counted from the source tree

These are re-derived on every run of the generator, from the tree as checked
out. They are not recorded anywhere -- a figure typed into a data file is the
same defect as one typed into a sentence -- so what is recorded is the
counting method, because "how many tests" has several honest answers and the
claim is only as good as the command behind it.

| figure | count | counted by |
|---|---:|---|
| Unit tests | 2,248 | `grep -rn '^func Test' --include='*_test.go' . \| grep -vc '/\.claude/worktrees/'` |
| Fuzz targets | 9 | `grep -rn '^func Fuzz' --include='*_test.go' . \| grep -vc '/\.claude/worktrees/'` |
| Limit boundary tests | 14 | `grep -hc '^func Test' ./*/limits_boundary_test.go \| awk '{n += $1} END {print n + 0}'` |

## Measured by a suite run

A suite run costs minutes, so these are recorded rather than re-derived. They
are written to `tests/conformance/results.json` from a run of `tests/check.sh`,
and `tests/docfigures.sh` cross-checks the same numbers against
`tests/ratchet.txt`, which that run rewrites by a different route.

| suite | edition | in scope | passing | now | disagreements | measured | command |
|---|---|---:|---:|---|---:|---|---|
| QT3 — XPath 2.0 | XPath 2.0 (Second Edition) | 15,217 | 15,217 | 100.00% | **0** | 2026-09-11 | `tests/check.sh (TestQT3, lane xpath-2.0)` |
| QT3 — XPath 3.0 | XPath 3.0 | 19,362 | 19,362 | 100.00% | **0** | 2026-09-11 | `tests/check.sh (TestQT3, lane xpath-3.0)` |
| QT3 — XPath 3.1 | XPath 3.1 | 21,898 | 21,898 | 100.00% | **0** | 2026-09-11 | `tests/check.sh (TestQT3, lane xpath-3.1)` |
| QT3 — XQuery 3.1 | XQuery 3.1 | 30,346 | 30,345 | 100.00% | **1** | 2026-09-11 | `tests/check.sh (TestQT3XQuery)` |
| W3C XSLT 2.0 | XSLT 2.0 (Second Edition) | 6,201 | 6,193 | 99.87% | **8** | 2026-09-11 | `tests/check.sh (TestXSLTSuite)` |
| W3C XSLT 3.0 | XSLT 3.0 | 11,518 | 11,490 | 99.76% | **28** | 2026-09-11 | `tests/check.sh (TestXSLT30Suite)` |
| W3C xsdtests 1.0 | XML Schema 1.0 (Second Edition) | 39,388 | 39,358 | 99.92% | **30** | 2026-09-11 | `tests/check.sh (XSD10)` |
| W3C xsdtests 1.1 | XML Schema 1.1 | 41,598 | 41,566 | 99.92% | **32** | 2026-09-11 | `tests/check.sh (XSD11)` |
| Clark spectest | RELAX NG 1.0 | 965 | 965 | 100.00% | **0** | 2026-09-11 | `tests/check.sh (RelaxNGSpectest)` |
| **Total** | | | | | **99** | | |

W3C disagreements: 0 + 0 + 0 + 1 + 8 + 28 + 30 + 32 + 0 = 99.

## Real-world corpora

DocBook xslTNG and XSpec are stylesheet collections nobody wrote for a test
harness. They are measured the same way but kept out of the total above, which
counts disagreements with a specification; a corpus disagreement is a bug in
this engine or in that stylesheet, and it is not a conformance figure. The
separation is structural -- they are a different field in the JSON and a
different list in the generator -- so no arithmetic can merge them by accident.

| corpus | in scope | passing | now | failing | measured | command |
|---|---:|---:|---|---:|---|---|
| DocBook xslTNG | 577 | 577 | 100.00% | 0 | 2026-09-11 | `tests/check.sh (DocBook)` |
| XSpec | 225 | 225 | 100.00% | 0 | 2026-09-11 | `tests/check.sh (XSpec)` |

## Named subsets of a suite's disagreements

A published figure of the form "N of the M failures are X". Both halves are
quoted in prose, so both are recorded here and the subset is checked against
its suite's disagreement count on load.

| subset | count | of | note |
|---|---:|---|---|
| XSLT 3.0 failures wanting an XTSE3430 | 8 | 28 W3C XSLT 3.0 disagreements | Only the unwritten remainder of the §19.8 posture-and-sweep analysis can emit the refusal; §19.1 does not require a non-streaming processor to assess guaranteed-streamability. The one overlap is su-ascent-903, enumerated individually because its verdict is not the block's: §19.8.5.7 makes an ascent function's streaming parameter climbing and the category permits a climbing body. |

## Where each figure is published

Every one of these is a marked region. `go run tests/conformance-docs.go -check`
fails if any has been hand-edited, and `tests/check.sh` runs that check.

| file | region |
|---|---|
| `README.md` | TEST COUNT |
| `README.md` | TEST METHODS |
| `docs/conformance-gaps.md` | CONFORMANCE SUMMARY |
| `docs/conformance-gaps.md` | UNIT TEST COUNT |
| `docs/conformance-gaps.md` | XTSE3430 BLOCK |
| `docs/stats.md` | STATS |
| `docs/testing.md` | LAYER COUNTS |
| `docs/todo.md` | STATUS TABLE |

## What is still prose

Some figures stay hand-written, and the reason is always the same: the number
is inside a sentence whose *argument* would have to change with it, and a
generator that rewrote the number alone would leave the paragraph saying
something false with a correct figure in it. Those are guarded instead by
`tests/docfigures.sh`, which anchors on the in-scope denominator -- the one
number in a figure that does not move between runs -- and fails when the
passing count, failure count or percentage beside it disagrees with
`tests/ratchet.txt`. That is a check, not a rewrite, and the distinction is
the point: it fails at the moment someone should be re-reading the sentence.
