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
| Unit tests | 3,422 | `grep -rn '^func Test' --include='*_test.go' . \| grep -v '/\.claude/worktrees/' \| grep -vc '^\./bench/'` |
| Fuzz targets | 12 | `grep -rn '^func Fuzz' --include='*_test.go' . \| grep -v '/\.claude/worktrees/' \| grep -vc '^\./bench/'` |
| Limit boundary tests | 14 | `grep -hc '^func Test' ./*/limits_boundary*_test.go \| awk '{n += $1} END {print n + 0}'` |

## Measured by a suite run

A suite run costs minutes, so these are recorded rather than re-derived. They
are written to `tests/conformance/results.json` from a run of `tests/check.sh`,
and `tests/docfigures.sh` cross-checks the same numbers against
`tests/ratchet.txt`, which that run rewrites by a different route.

| suite | edition | in scope | passing | now | disagreements | skipped: out of scope / unimplemented | measured | command |
|---|---|---:|---:|---|---:|---:|---|---|
| QT3 — XPath 2.0 | XPath 2.0 (Second Edition) | 15,217 | 15,217 | 100.00% | **0** |  | 2026-09-11 | `tests/check.sh (TestQT3, lane xpath-2.0)` |
| QT3 — XPath 3.0 | XPath 3.0 | 19,362 | 19,362 | 100.00% | **0** |  | 2026-09-11 | `tests/check.sh (TestQT3, lane xpath-3.0)` |
| QT3 — XPath 3.1 | XPath 3.1 | 22,054 | 22,054 | 100.00% | **0** |  | 2026-10-01 | `tests/check.sh (TestQT3, lane xpath-3.1)` |
| QT3 — XQuery 3.1 | XQuery 3.1 | 30,530 | 30,529 | 100.00% | **1** |  | 2026-11-06 | `tests/check.sh (TestQT3XQuery)` |
| W3C XSLT 2.0 | XSLT 2.0 (Second Edition) | 6,203 | 6,195 | 99.87% | **8** | 8,332 / 66 | 2026-11-06 | `tests/check.sh (TestXSLTSuite)` |
| W3C XSLT 3.0 | XSLT 3.0 | 11,522 | 11,505 | 99.85% | **17** | 2,951 / 128 | 2026-11-06 | `tests/check.sh (TestXSLT30Suite)` |
| W3C xsdtests 1.0 | XML Schema 1.0 (Second Edition) | 39,388 | 39,359 | 99.93% | **29** |  | 2026-11-06 | `tests/check.sh (XSD10)` |
| W3C xsdtests 1.1 | XML Schema 1.1 | 41,598 | 41,568 | 99.93% | **30** |  | 2026-11-06 | `tests/check.sh (XSD11)` |
| Clark spectest | RELAX NG 1.0 | 965 | 965 | 100.00% | **0** |  | 2026-09-11 | `tests/check.sh (RelaxNGSpectest)` |
| **Total** | | | | | **85** | | | |

The skipped column is the XSLT lanes' exclusions split by class: cases the
suite says a conforming processor may leave out, and cases excluded because a
feature is not implemented or a dependency is not modelled. Neither is in the
denominator; docs/testing.md explains the line between them.

W3C disagreements: 0 + 0 + 0 + 1 + 8 + 17 + 29 + 30 + 0 = 85.

## Real-world corpora

DocBook xslTNG and XSpec are stylesheet collections nobody wrote for a test
harness. They are measured the same way but kept out of the total above, which
counts disagreements with a specification; a corpus disagreement is a bug in
this engine or in that stylesheet, and it is not a conformance figure. The
separation is structural -- they are a different field in the JSON and a
different list in the generator -- so no arithmetic can merge them by accident.

| corpus | in scope | passing | now | failing | measured | command |
|---|---:|---:|---|---:|---|---|
| DocBook xslTNG | 578 | 578 | 100.00% | 0 | 2026-09-29 | `tests/check.sh (DocBook)` |
| XSpec | 225 | 225 | 100.00% | 0 | 2026-09-11 | `tests/check.sh (XSpec)` |

## Where each figure is published

Every one of these is a marked region. `go run tests/conformance-docs.go -check`
fails if any has been hand-edited, and `tests/check.sh` runs that check.

| file | region |
|---|---|
| `README.md` | DOCBOOK COUNT |
| `README.md` | XSPEC COUNT |
| `docs/conformance-gaps.md` | CONFORMANCE SUMMARY |
| `docs/conformance-gaps.md` | UNIT TEST COUNT |
| `docs/conformance-gaps.md` | XTSE3430 BLOCK |
| `docs/conformance-gaps.md` | XPATH FIGURE |
| `docs/conformance-gaps.md` | RELAX NG FIGURE |
| `docs/stats.md` | STATS |
| `docs/todo.md` | STATUS TABLE |
| `docs/todo.md` | RELAX NG FIGURE |
| `docs/validation.md` | RELAX NG FIGURE |

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
