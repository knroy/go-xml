# Testing

How this engine is tested, how to run any part of it, and how to read what
comes back.

The short version: `tests/check.sh` is the gate, and a change is not done
until it prints `OK`.

```
tests/check.sh fast     # build, vet, unit tests, race — about a minute
tests/check.sh          # everything available, about eight minutes
```

---

## The layers, and what each one catches

No single method was sufficient. Each was added because the previous set had
let something through, and the column that matters is the last one.

| layer | count | catches | misses |
|---|---:|---|---|
| **Unit tests** | 1,082 | a plausible implementation that is quietly wrong | anything nobody thought to write a test for |
| **Race detector** | same tests | shared state a single-goroutine run never reveals | a data race on a path no test walks |
| **W3C conformance suites** | ~128,000 cases | systematic divergence from the specification | what the suites do not ask about — see below |
| **Real-world stylesheets** | 818 documents | what large stylesheets do that a rule-at-a-time suite does not | constructs those two codebases happen not to use |
| **Production schema sets** | 65 + CII | what modular published schemas do | industries whose schemas are shaped differently |
| **The ratchet** | 7 marks | a silent revert, or a fix that quietly costs more than it gains | a regression in something no suite counts |

**The suites are the weakest of these where it counts most.** Every one of
them feeds the parser *well-formed* input and measures what happens after; none
systematically checks that malformed input is refused. That gap is recorded in
[todo.md](todo.md), not papered over here.

**The production schema sets found the most per hour.** Pointing the validator
at UBL 2.1 turned up two defects the entire W3C suite had not, and between them
they meant all 65 main-document schemas failed to load with 1,758 errors
apiece. Neither defect was exotic.

---

## The suites

Third-party and not vendored. Point the variables at your own checkouts, or
let the defaults find them under `testdata/`.

| suite | variable | default | what it measures |
|---|---|---|---|
| W3C QT3 | `GOXSLT_QT3` | `testdata/qt3tests` | XPath 2.0, 3.0, 3.1 and XQuery 3.1 |
| W3C xslt30-test | `GOXSLT_XSLTS` | `testdata/xslt30-test` | XSLT 2.0 and 3.0, filtered by target |
| W3C xsdtests | `GOXSLT_XSDTS` | `testdata/xsdtests` | XSD 1.0 and 1.1, schema-validity and instance |
| RELAX NG spectest | `GOXSLT_RNG` | `testdata/relaxng/spectest.xml` | RELAX NG, James Clark's suite |
| DocBook xslTNG | `GOXSLT_XSLTNG` | `testdata/xsltng` | a 97-module real stylesheet over 593 documents |
| XSpec | `GOXSLT_XSPEC` | `testdata/xspec` | an XSLT compiler written in XSLT |
| UBL 2.1 | `GOXSLT_UBL` | — | 65 modular production schemas |
| UN/CEFACT CII | `GOXSLT_CII` | — | EN 16931 and CII schemas |

Fetching the four W3C suites:

```sh
git clone --depth 1 https://github.com/w3c/qt3tests.git      testdata/qt3tests
git clone --depth 1 https://github.com/w3c/xsdtests.git      testdata/xsdtests
git clone --depth 1 https://github.com/w3c/xslt30-test.git   testdata/xslt30-test
git clone --depth 1 https://github.com/relaxng/jing-trang.git /tmp/jing-trang
mkdir -p testdata/relaxng
cp /tmp/jing-trang/mod/rng-validate/test/spectest.xml testdata/relaxng/
```

DocBook needs its localisation files generated before it will transform
anything, and this engine can do it:

```sh
git clone --depth 1 https://github.com/docbook/xslt3ng testdata/xsltng
for f in testdata/xsltng/src/main/locale/*.xml; do
  go run ./cmd/go-xml -xsl testdata/xsltng/src/main/xslt/modules/xform-locale.xsl \
    -allow-dir testdata/xsltng \
    -o testdata/xsltng/src/main/xslt/locale/"$(basename "$f")" "$f"
done
```

That build step is why DocBook and XSpec are measured locally rather than in
CI, which fetches only the four W3C suites. UBL and CII are licensed and
cannot be cloned in a workflow at all. **Four skips in a CI log are the normal
reading**, and `check.sh` says so where it prints them.

---

## The ratchet

`tests/ratchet.txt` records the highest passing count this repository has
seen. `check.sh` fails when a count goes **down**.

```
DocBook 577
TestQT3XQuery 29800
TestXSLT30Suite 8611
TestXSLTSuite 6149
XSD10 39347
XSD11 41532
XSpec 225
```

It exists because build-and-test cannot see a silent revert: a stale copy of a
shared file committed over an additive change leaves a tree that compiles and
a suite that still passes, because other work is landing wins in parallel. The
total looks plausible and nothing flags it. That happened, to commit `9843c44`,
and was found by accident.

Percentages elsewhere are printed rather than asserted, because a hard
threshold turns every upstream suite update into a build break. A ratchet does
not: an update that adds cases moves the in-scope count, and only the *passing*
count going down is reported.

To record a deliberate change:

```sh
GOXSLT_RATCHET=update tests/check.sh   # record new highs
GOXSLT_RATCHET=off    tests/check.sh   # skip the check entirely
```

**Never record a mark from a tree you are not about to push in the same
commit.** Marks of 29,797 and 8,609 were once recorded from a local run
holding work that had not been pushed; CI then measured 29,796 and 8,608 and
correctly failed on a regression that was really a promise made too early.

A mark that goes *down* legitimately is possible and is not a regression: when
cases leave the denominator rather than the passing set. Both XSD marks fell
when `indeterminate` expectations stopped being scored as "must be invalid" —
the rate rose while the raw count fell. Say so in the commit message when it
happens.

---

## Running one thing

`-count=1` is **mandatory** on every suite run. Go caches test results, and a
cached pass cannot show a regression.

```sh
# One suite, with its summary
GOXSLT_XSLTS=testdata/xslt30-test go test ./tests/xslts/ \
  -run TestXSLT30Suite -count=1 -v

# Every failing case, by name and reason
GOXSLT_XSLTS_VERBOSE=1 go test ./tests/xslts/ -run TestXSLT30Suite -count=1 -v

# Which test sets are worst — a set failing nearly everything is an
# unimplemented feature; one failing a handful is edge cases
GOXSLT_XSLTS_BYSET=1 go test ./tests/xslts/ -run TestXSLT30Suite -count=1 -v

# One set only
GOXSLT_XSLTS_ONLYSET=merge GOXSLT_XSLTS_VERBOSE=1 \
  go test ./tests/xslts/ -run TestXSLT30Suite -count=1 -v

# QT3 equivalents
GOXSLT_QT3_VERBOSE=1 go test ./tests/qt3/ -count=1 -v
GOXSLT_QT3_SET=fn-matches go test ./tests/qt3/ -count=1 -v

# XSD, which has its own driver rather than a Go test
go run ./tests/xsdsuite testdata/xsdtests        # 1.0
go run ./tests/xsdsuite testdata/xsdtests -11    # 1.1
```

`GOXSLT_NO_SUITES=1` keeps the conformance suites out of a plain
`go test ./...`. Without it, a checkout that has `testdata/` on disk runs the
whole conformance job a second time — and under `-race` that is slow enough to
pass `go test`'s ten-minute default and panic rather than report.

`GOXSLT_CASE_TIMEOUT` overrides the 60-second per-case deadline for a slower
machine, in the QT3 driver as well as the XSLT one. It is a measurement parameter, not a limit on the engine: three cases
in the `catalog` set parse every stylesheet in the suite inside one transform,
and at the old 10s deadline the reported figure moved with whatever else was
running on the box — 8,605 under a parallel build, 8,607 on a quiet machine.
That is measurement noise presented as a conformance number.

QT3 was left on a 10-second deadline when the XSLT driver was raised, and it
failed the same way: CI reported XQuery 29,799 and 29,798 for the **same
commit** in two runs minutes apart, and the ratchet correctly called the second
a regression. Two different numbers for one commit cannot be a code change —
only cases sitting near the deadline on a loaded runner. Both drivers now use
60 seconds.

One case does not pass at any deadline. `op:same-key-023` builds 75³ = 421,875
keys and calls `map:put` and `map:remove` once for each; both are O(n) in this
representation, so the case is quadratic and does not finish in ten minutes. It
is a real performance defect and is recorded as one in
[conformance-gaps.md](conformance-gaps.md) — not a timeout to be raised past.

---

## Reading a result

**Compare the failing case list, not just the count.** A change that gains one
case and loses another leaves the total unmoved, and the total is what people
look at. This has happened twice here: a `product-version` fix gained
`package-version-011` and silently lost `package-version-010`, and a
`typeswitch` scope check gained its target and lost `K2-ForExprWithout-8`. Both
were caught only by diffing the names.

```sh
GOXSLT_XSLTS_VERBOSE=1 go test ./tests/xslts/ -run TestXSLT30Suite -count=1 -v \
  2>&1 | awk '/FAIL /{sub(/^.*FAIL /,"");print}' | sort > after.txt
diff before.txt after.txt
```

**A check that did not run must never look like one that succeeded.** A suite
that is missing is reported and skipped; a suite that is *present and reports
nothing* is a failure. That distinction exists because the first time this
script ran, a relative `GOXSLT_QT3` resolved against the wrong directory, the
test skipped itself, and `go test` printed PASS.

**Skipped is not failed.** The suites skip cases by declared dependency —
streaming, a specific Unicode version, a spec version not being measured. The
XSLT 3.0 suite has 14,601 cases and 8,625 in scope; counting the difference as
failures would understate the engine, and counting it as passes would overstate
it. Both figures are reported separately for that reason.

---

## Writing a test

Two rules, both learned the hard way.

**A regression test must fail without the fix.** Revert the change, run the
test, and confirm it reproduces the original failure *signature* — not merely
that it fails. A test that passes against broken code is worse than no test,
because it certifies the bug.

```sh
git stash push -- path/to/fix.go
go test ./pkg/ -run TestTheRegression -count=1   # must FAIL, with the old error
git stash pop
```

**Say what the case is, and why the answer is what it is.** The tests here
name the W3C case that motivated them and quote the rule being applied, because
a bare assertion is unmaintainable: the next person cannot tell a deliberate
behaviour from an accident of implementation. Where the spec and the suite
disagree, Saxon's published report at
`testdata/xslt30-test/report/submission/Saxon_9.8.xml` is the tiebreak.

---

## What CI runs

Two jobs, in [`.github/workflows/ci.yml`](../.github/workflows/ci.yml).

**`test`** — gofmt, vet, build, unit tests, and the race detector, with
`GOXSLT_NO_SUITES=1` throughout. About a minute; catches a broken commit fast.
gofmt is enforced rather than advisory.

**`conformance`** — fetches the four W3C suites into a cache keyed `suites-v2`
and runs `tests/check.sh`. This is the only place the suites are fetched
reproducibly. Without it the published percentages depend on someone
remembering to run the script, and a number nobody re-measures is a number that
quietly stops being true.

---

## The Go version is a conformance dependency

The module requires **Go 1.25**, and that is a measured floor rather than a
tidy default. `regexp` learned the Unicode category `Cn` (unassigned) in 1.25;
on 1.24 the pattern `^(?:\p{Cn}*)$` fails to compile, and `re00175` raises
FORX0002 where it should match. The cost of building on 1.24 is four cases:
XPath 3.0 and 3.1 fall off 100%, XQuery loses one, and XSD 1.0 loses two.

That was found the hard way. The floor was lowered to 1.24 on the reasoning
that nothing in the code imports anything newer — true, and irrelevant, because
the dependency is on standard-library *behaviour* rather than on a symbol.
Local runs did not catch it: `go.mod` said 1.24 while the installed toolchain
was 1.26, and Go builds with what is installed. CI, which honours
`go-version:`, was the only thing that saw it.

**To test a version floor, install that toolchain and run the suites with it.**

```sh
go install golang.org/dl/go1.25.1@latest && go1.25.1 download
GOXSLT_QT3=$PWD/testdata/qt3tests go1.25.1 test ./tests/qt3/ -count=1 -v
```

## Related

* [conformance-gaps.md](conformance-gaps.md) — the current figures and a
  case-by-case verdict on every failure.
* [known-gaps.md](known-gaps.md) — why the hard gaps are hard, including fixes
  that were attempted, measured and reverted.
* [reaching-100.md](reaching-100.md) — what the remaining distance consists of
  and which parts are worth buying.
