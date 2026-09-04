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
| **Unit tests** | 1,173 | a plausible implementation that is quietly wrong | anything nobody thought to write a test for |
| **Limit boundary tests** | 7 tables | an off-by-one or an overflow at the edge of a configurable limit | a limit nobody added to the inventory |
| **Race detector** | same tests | shared state a single-goroutine run never reveals | a data race on a path no test walks |
| **W3C conformance suites** | ~128,000 cases | systematic divergence from the specification | what the suites do not ask about — see below |
| **Real-world stylesheets** | 818 documents | what large stylesheets do that a rule-at-a-time suite does not | constructs those two codebases happen not to use |
| **Production schema sets** | 65 + CII | what modular published schemas do | industries whose schemas are shaped differently |
| **Fuzzing** | 5 targets | a crash, hang or wrong refusal on input nobody would write | anything a coverage-guided search does not reach in the time given |
| **Generated oracle** | 8,397 documents | a *wrong answer* in the content-model matcher, on shapes nobody wrote a case for | only the occurrence shapes whose language is plain arithmetic — no wildcards, substitution groups, or interleaved choices |
| **The ratchet** | 7 marks | a silent revert, or a fix that quietly costs more than it gains | a regression in something no suite counts |

**The suites are the weakest of these where it counts most.** Every one of
them feeds the parser *well-formed* input and measures what happens after; none
systematically checks that malformed input is refused. Fuzzing is what covers
that, and it is why the row above exists: the targets feed the parser, the
schema assembler and the stylesheet compiler input no author would write, and
assert that a refusal arrives as an error rather than as a panic.

**Fuzzing asks whether anything crashes; the generated oracle asks whether the
answer is right.** That is the difference that matters for the content-model
matcher, where two occurrence bugs survived 80,879 suite agreements — a
repeating group with two or more distinct child names was always decided
correctly, so no suite case went near the single-child shape. `xsd/occurs_oracle_test.go`
generates the schema and the document and compares against a count derived from
interval arithmetic over the occurrence bounds, never from the engine: an oracle
that asked the engine would agree with the engine's bugs. Run against the code
as it stood before either fix, it reports 1,474 wrong answers, 165 of them
*false accepts* — a validator admitting documents no reading of the model
allows, which is the direction that actually hurts a caller. It covers only the
shapes whose language falls out of arithmetic; a choice whose branches repeat or
differ in length needs the same interleaving argument the matcher does, and an
oracle that reasons the same way is not independent, so those are left out
deliberately rather than guessed at.

**An unproven hypothesis is worth testing precisely because it is unproven.**
The fifth audit could not demonstrate that the `depth > 32` guards on four
schema-graph walks changed any answer, and said so — it filed them as "a
high-value target for differential testing, not a confirmed vulnerability"
rather than as a finding. Generating legal, acyclic schemas either side of the
bound turned two of them into confirmed false accepts within minutes. The
report was right to be tentative and right to point at them; what settled it
was construction, not argument. `xsd/depth_acyclic_test.go` keeps both shapes.

Worth recording how nearly it was missed: two of the first attempts to
reproduce it showed no difference at all, because Element Declarations
Consistent is an XSD 1.1 rule and `Options{}` defaults to 1.0, which silently
no-ops it. A baseline that reads "correct" for the wrong reason is the most
expensive kind of wrong answer, which is why the test asserts the shallow case
fails before it asserts anything about the deep one.

**An oracle only covers the shapes its generator makes.** The identity oracles
run 10,000 documents and agreed throughout while `mergeTables` had a bug that
made a key resolvable again at three siblings, five siblings, seven. The
generators put targets under one scope at a time, so an ancestor merging three
sibling tables never arose — the corpus could not express the bug, exactly as
the `.//box/leaf` case could not express a dropped leading step until loose
leaves were added.

An external reader found it by reading the merge. The durable fix is not more
documents but an invariant asserted on the data structure itself: if two
distinct nodes produced the same key sequence, that sequence must not still be
resolvable. That check is three lines, runs on every merged table, and fails at
the point of corruption rather than several layers away in a verdict.

**A probe that clears a guard has to reach the guard.** This file recorded a
300-link derivation chain as evidence that eleven remaining `seen > 64` and
`seen > 256` counters were sound, and an external reviewer reasonably relied on
it. The measurement was real and it cleared the wrong walk. The chain drove
facets, and `SimpleType.Primitive` is filled in eagerly during parsing — set on
the deepest link at depth 1, 64 and 300 alike — so `primitiveOf` returns on its
first iteration and a 300-link chain exercised the loop exactly **once**.

Six of those eleven turned out to be defects, two of them false accepts: a
duplicate `xs:ID` is admitted once the chain under `xs:ID` runs 64 links, and
`"1.5"` validates as a descendant of `xs:integer` at the same depth. Neither
schema is recursive or malformed.

Two habits follow. Assert a semantic property the walk decides — *this deep
type is still an ID* — rather than that a function returned something. And
before trusting a negative result, confirm the loop iterated: if the value is
memoised or filled eagerly, depth is not reaching it. A first attempt to
reproduce this independently also missed, by building n links where the bug
needs 64 iterations — off by one, exactly at the cliff.

**A corpus that cannot express the bug will not find it.** Widening the
identity oracle to a two-step selector, `.//box/leaf`, appeared to cover the
multi-step case. It did not. The generator put every `leaf` inside a `box`, so
a matcher that ignored the leading step selected exactly the same nodes — the
sabotage check found **zero** disagreements, not because the engine was right
but because the corpus could not tell the two apart. Adding loose `<leaf>`
elements directly under `<r>`, sharing the same id space so a wrong selection
manufactures a duplicate, took the same sabotage from 0 to 841 disagreements.

The lesson generalises past this test: when a sabotage check comes back clean,
the first suspect is the corpus, not the implementation.

**Counters say what a stopwatch cannot.** The identity-constraint evaluator's
problem is not that any one traversal is slow; it is that the same nodes are
walked once per enclosing scope, and elapsed time cannot distinguish that from
a large constant. `xsd/identity_stats_test.go` counts selector evaluations,
field evaluations and nodes visited, and prints the ratio:

    depth=120  nodesVisited=7260     growth on doubling: -
    depth=240  nodesVisited=28920    growth on doubling: 3.98x
    depth=480  nodesVisited=115440   growth on doubling: 3.99x
    depth=960  nodesVisited=461280   growth on doubling: 4.00x

Four times the work for twice the depth, and nodes-visited-per-node climbing
30, 60, 120, 240 in step with it. That is the quadratic stated as a measurement
rather than as an argument, and it is the number a redesign has to move — a
one-pass evaluator holds that ratio flat. The counters are nil in every
ordinary build and attach through a package-internal hook, so they cost a nil
check: the benchmark is unchanged to within noise with them compiled in.

**An oracle earns its keep by disagreeing.** The identity-constraint oracle in
`xsd/identity_oracle_test.go` reported 0 disagreements over 6,000 generated
documents the first time it ran, which says nothing on its own — a test that
cannot fail is indistinguishable from one that always passes. It was then run
against a `buildNodeTable` sabotaged to scan only direct children, which is
precisely the bug a cheaper incremental implementation invites, and it found
416 disagreements, all of them false accepts. That is what licensed the change
that followed. Any oracle written here should be checked the same way before it
is trusted: break the thing it watches, and confirm it notices.

**A test can also exist to stop a question being re-asked.**
`xsd/occurs_boundary_test.go` walks `minOccurs` and `maxOccurs` through the
integer edges where a representation could hide — 126/127/128,
253/254/255/256/257, 300, 1000, 65535/65536 — each at its bound and one either
side. It pins no bug. It exists because `encodeCounts` caps a count at 254 to
keep the vector inside a byte, which reads like a ceiling on `maxOccurs` and
was reported as one; the cap is unreachable, but only for a reason that takes
three functions to follow (see
[known-gaps.md](known-gaps.md)). The sweep answers in 0.1s what the argument
answers in a paragraph, and it keeps answering after the argument is
forgotten.

**The production schema sets found the most per hour.** Pointing the validator
at UBL 2.1 turned up two defects the entire W3C suite had not, and between them
they meant all 65 main-document schemas failed to load with 1,758 errors
apiece. Neither defect was exotic.

**The limit boundary tests exist because an audit found what unit tests should
have.** `xdm.ParseString("<r/>", xdm.ParseOptions{MaxBytes: math.MaxInt64})`
returned `parse XML: no root element`. The reader is wrapped in
`io.LimitReader(r, maxBytes+1)`, one byte over so a document at the limit can be
told from one past it; at `math.MaxInt64` that addition overflows to
`math.MinInt64`, which `io.LimitReader` reads as "nothing left". The value a
caller picks to mean *do not limit me* was the value that refused every
document. The same arithmetic in `xsd.HTTPResolver` was worse — an empty body
with a **nil error**, so a schema loaded silently as empty.

Every caller-settable limit is now exercised at the values where that class of
bug lives: `0`, negative, `1`, exactly at the limit, exactly one over, and
`MaxInt`/`MaxInt64` with its neighbour. The at-limit / one-over pair is the
load-bearing one — it pins the boundary in both directions, so neither
loosening nor tightening the comparison passes. Each refusal is asserted to
name the limit that fired, because `err != nil` alone also passes when the
wrong limit trips or the document was malformed for an unrelated reason.

Where a value's meaning is deliberate the test pins it rather than changing it,
with a comment saying why: `0` means the default everywhere, and negative means
"no limit" in most places but *the default* for `xdm.ParseOptions.MaxDepth` and
`xpath.Context.MaxDepth`, since a depth bound of zero would refuse every
document. See [options.md](options.md) for the field-by-field rule.

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
TestXSLT30Suite 8612
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

# The generated content-model oracle, with its per-shape document counts
go test ./xsd/ -run TestOccursOracle -count=1 -v

# The same sweeps with wider bounds and longer documents — about 2s rather
# than 0.4s, so it is opt-in rather than part of every `go test ./...`
GOXSLT_OCCURS_WIDE=1 go test ./xsd/ -run TestOccursOracle -count=1 -v

# Every configurable limit at its boundaries, across all six packages
go test ./xdm/ ./xsd/ ./xpath/ ./xslt/ ./relaxng/ ./dtd/ \
  -run Boundaries -count=1 -v
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

## Fuzzing

Five targets, using Go's native `testing.F` and no framework:

| target | package | asserts |
|---|---|---|
| `FuzzParseNoPanic` | `xdm` | `ParseString` never panics; a refusal is an error and never a tree beside it; an accepted tree walks with its parent links intact |
| `FuzzLoadSchemaNoPanic` | `xsd` | `Load` never panics at either XSD version, and every content model it accepts compiles to an automaton that answers total |
| `FuzzSerializeRoundTrip` | `xslt` | parse → serialise → parse yields the same document, compared on expanded names, kinds and string values |
| `FuzzCompileStylesheetNoPanic` | `xslt` | `Compile` never panics and never returns a stylesheet beside an error |
| `FuzzCompileNoPanic` | `xpath` | the expression compiler never panics, and every parse error carries a spec code |

A target lives in `zz_fuzz_test.go` in the package it exercises. The `zz_`
prefix is only to sort it last.

```sh
# Run one target's search. -run '^$' suppresses the ordinary tests so that
# only the fuzzing runs.
GOXSLT_NO_SUITES=1 go test ./xdm/ -run '^$' -fuzz FuzzParseNoPanic -fuzztime 120s
GOXSLT_NO_SUITES=1 go test ./xsd/ -run '^$' -fuzz FuzzLoadSchemaNoPanic -fuzztime 120s
GOXSLT_NO_SUITES=1 go test ./xslt/ -run '^$' -fuzz FuzzSerializeRoundTrip -fuzztime 120s
```

Only one target may be fuzzed per `go test` invocation; that is Go's
restriction, not this repository's.

**A plain `go test` runs the seed corpus and nothing else.** That is why the
seeds are kept short and few — a Go fuzz target replays every seed on every
ordinary test run, so a large corpus is a tax on every build. The five targets
together add well under a second.

**A limit firing is not a failure.** The parser's `MaxDepth`, `MaxBytes` and
`MaxNodes` exist precisely to refuse the input a fuzzer is good at generating,
and the targets lower them so that a pathological case costs milliseconds. A
target that treated a limit error as a crash would be asserting that the limits
should not work.

Crashers Go finds are written to `testdata/fuzz/<Target>/` inside the package
directory. That path is this repository's own, unrelated to the third-party
suite checkouts elsewhere under `testdata/`; a minimised crasher worth keeping
is committed there and replays as a seed thereafter.

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

**Point the variables at a real path, not a symlink.** XSpec reports 224
instead of 225 when `GOXSLT_XSPEC` reaches the corpus through a symlink, and
the cause is not this engine misbehaving. `issue-987_parent.xspec` is a
deliberate circular import — its own comment reads "Circular import. Should be
discarded" — which XSpec breaks by deduping on document URI. Reached through a
symlink the same file acquires two URIs, the dedupe misses, and `global-param`
is declared twice. The baseline binary fails identically, so a count that
disagrees with CI by exactly one here is a path artifact rather than a
regression. Document URIs are not canonicalised across symlinks.

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
