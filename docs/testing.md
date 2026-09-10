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
| **Unit tests** | 2,027 | a plausible implementation that is quietly wrong | anything nobody thought to write a test for |
| **Limit boundary tests** | 14 tests | an off-by-one or an overflow at the edge of a configurable limit | a limit nobody added to the inventory |
| **Race detector** | same tests | shared state a single-goroutine run never reveals | a data race on a path no test walks |
| **W3C conformance suites** | 141,691 cases | systematic divergence from the specification | what the suites do not ask about — see below |
| **Real-world stylesheets** | 818 documents | what large stylesheets do that a rule-at-a-time suite does not | constructs those two codebases happen not to use |
| **Production schema sets** | 65 + CII | what modular published schemas do | industries whose schemas are shaped differently |
| **Vendored real-world schemas** | 185 of 230 | a schema-validity rule that has become stricter than the spec, on every checkout — no licensed corpus needed | the deep industry vocabularies only UBL and CII carry |
| **Fuzzing** | 9 targets | a crash, hang or wrong refusal on input nobody would write | anything a coverage-guided search does not reach in the time given |
| **Generated oracle** | 8,397 documents | a *wrong answer* in the content-model matcher, on shapes nobody wrote a case for | only the occurrence shapes whose language is plain arithmetic — no wildcards, substitution groups, or interleaved choices |
| **The ratchet** | 10 marks | a silent revert, or a fix that quietly costs more than it gains | a regression in something no suite counts |

**How the first four counts are counted**, because "how many tests" has several
honest answers and the one meant here is the narrow one. The three that a
command can settle are asserted for equality by `tests/check.sh`'s *documented
figures* section, which fails the gate when this table drifts from the tree:

* **Unit tests** — `func Test` declarations, not subtests and not table rows:
  `grep -rn "func Test" --include='*_test.go' . | grep -vc '/\.claude/worktrees/'`
* **Limit boundary tests** — `func Test` declarations in the six
  `*/limits_boundary_test.go` files (dtd, relaxng, xdm, xpath, xsd, xslt); most
  are table-driven, so they run rather more than 13 cases:
  `grep -hc "func Test" ./*/limits_boundary_test.go | awk '{n += $1} END {print n + 0}'`
* **Fuzzing** — `grep -rn "func Fuzz" --include='*_test.go' . | grep -vc '/\.claude/worktrees/'`
* **W3C conformance suites** — the sum of the in-scope totals in the status
  table: XPath 2.0 15,222 + XQuery 3.1 29,964 + XSLT 2.0 6,201 + XSLT 3.0 11,518
  + XSD 1.0 39,388 + XSD 1.1 41,576 + RELAX NG 965. XPath 3.0 and 3.1 are not
  added again — the QT3 catalog is one corpus measured at three versions, and
  the 2.0 figure is the whole of it that this engine claims. An earlier
  revision said "~128,000", which no grouping of these numbers reaches.

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

**A hypothesis handed to an implementer is a lead, not a diagnosis.** When the
per-node type descriptor broke two XSLT cases, the reproduction came with a
guess: `as-3002` is named "list-builtin", and `xsd/assemble.go` skips
registering built-ins, so a node preferring an empty field over the registry
would lose typing for a list of built-ins. Plausible, and wrong.

The real cause was that the resolved fields **outlived their annotation**.
`Atomize` gates on `TypeAnnotation != ""` and `AtomizeList` did not, so a result
tree element arrived with an empty annotation and a live `ListItem`, split into
three tokens anyway, and compared three tokens against a whole string. The
registry path was immune for a reason worth keeping in mind: it derived the item
type *from* the annotation, so an empty annotation meant no list, structurally.

Two habits from this. State a hypothesis as a hypothesis and ask for it to be
verified rather than applied — it was, and the instrumented judge found the
truth in one pass. And when replacing a lookup that derived one fact from
another with two independent fields, the invariant that used to be structural
becomes something you have to maintain by hand: every reader of the new field
needs the same guard the old derivation gave for free.

**A boolean oracle can be too weak to see anything.** The keyref oracle first
compared valid against invalid, and roughly 85% of generated documents fail
somehow — so "invalid" agreed with a constant and the comparison was nearly
vacuous. Comparing the exact count of identity failures instead, with
`MaxErrors: -1`, forced the oracle to model the engine's reporting multiplicity
and made it sharp.

How sharp is worth recording. Sabotaging the merge to reintroduce the
three-sibling ambiguity bug produces **5 disagreements in 3,000 documents**;
dropping the inherited ambiguity set produces 7. Two of the other three
sabotages produce over a thousand each. That ratio is the whole reason the
original bug survived 10,000 documents against the earlier oracles: the shape
is rare, and a corpus that does not deliberately generate it will not stumble
into it. The histogram is printed with the result — 3, 4 and 5 sibling scopes
each occur in hundreds of documents — so a future generator change that stops
producing them is visible rather than silent.

**An oracle only covers the shapes its generator makes.** The identity oracles
run 10,000 documents and agreed throughout while `mergeTables` had a bug that
made a key resolvable again at three siblings, five siblings, seven. The
generators put targets under one scope at a time, so an ancestor merging three
sibling tables never arose — the corpus could not express the bug, exactly as
the `.//box/leaf` case could not express a dropped leading step until loose
leaves were added.

**A generator that emits one datatype cannot see a datatype bug.** The identity
oracles build every field from `xs:string` ids, so no number of documents from
them says anything about whether `3.0` and `3` collide as one `xs:decimal` —
equality on strings and equality on values are the same relation there. That
gap was found by asking the question directly instead:
`xsd/identity_typed_equality_test.go` asserts the spellings per primitive as
verdicts, and it is what showed that a field typed as a *union* recorded no
value at all and fell back to comparing raw strings. The oracles were green
before and after that fix, correctly — the case is outside what they generate.

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

The same counters later settled an argument the prose had got wrong. `keyref`
was documented as inherently quadratic — every enclosing scope must check every
target, which is true — and that was taken to mean nothing could be done. The
counters separated the two quantities the prose had run together: `fieldEvals`
and `targets` were already linear, so the *checks* were not the cost; only
`nodesVisited` grew at 3.92, 3.96, 3.98, and that is target rediscovery, which
is cacheable. `TestIdentityKeyrefAmplification` now holds it at 2.00x.

Elapsed time would not have separated them, and did not: after the traversal
was made linear the benchmark still grew fourfold, because two unrelated
quadratics in allocation had been hidden underneath it. `-benchmem` is what
showed those — bytes per operation growing 3.95x per doubling while the
allocation *count* grew 2.2x, which says the copies were getting larger rather
than more numerous, and pointed at the per-level table copying rather than at
anything in the walk.

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

**A verdict is the assertion; "no error" is not.**
`relaxng/datatype_bounds_test.go` asserts `valid` / `invalid` / `schema-error`
by name rather than checking that validation returned no error, because the
defects it pins are false accepts — a `maxInclusive` of `9007199254740992` that
admitted `9007199254740993`, and a `minLength` of `9223372036854775808` that
wrapped negative and admitted every string. Both produce no error at all, so a
test that only asked whether validation blew up would have passed against
either. The bound cases walk both sides of 2^53 in both directions, values past
the range of any float, negatives, and `xs:decimal` pairs differing only in the
twentieth significant digit; the `xs:double` cases are there to keep the fix
from over-reaching, since `float64` really is that type's value space. The file
also pins that an `<include>`d grammar is checked against section 7, which
`derive.go` assumes and the include path did not do.

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
let `tests/check.sh` find them under `testdata/`. The defaults in the table
below are **check.sh's**, not the tests' own: the suite tests skip unless their
variable is set, and a relative path resolves against the package directory
rather than the repository root, so `GOXSLT_QT3=testdata/qt3tests go test
./tests/qt3/` skips and prints PASS. Run `tests/check.sh`, which passes
absolute paths and fails a suite that reports no summary.

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
| Vendored schemas | *(none)* | `testdata/` | 230 real `.xsd` already in the tree; no variable, it always runs |

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
git clone --depth 1 https://github.com/docbook/xslTNG testdata/xsltng
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

### Vendored real-world schemas

The last row has no variable and no skip, which is the point of it. Every
schema-validity rule risks being *stricter than the spec*, and that is the one
defect the W3C suite structurally cannot catch: the suite scores agreement with
its own labels, so an over-strict rule shows up there only if the suite happens
to contain a valid schema exercising the exact shape. Real schemas catch it —
which is why UBL and CII exist in this list. But both are licensed, so on every
checkout that lacks them the guard was simply skipped, and schema rules were
landing without it.

230 real `.xsd` files are already in the tree as fixtures for the XSLT and
XQuery suites. `check.sh` loads each one on its own, in every run including
fast mode, and ratchets how many assemble:

```sh
go run ./tests/corpora vendored testdata/xslt30-test testdata/qt3tests testdata/xspec
# vendored schemas: 185 loaded, 38 failed, 7 excluded (of 230)
```

They are read as XSD 1.1, because it is a superset here — everything that
assembles under 1.0 also does under 1.1, and the schema-for-schemas in the
XSLT catalog is 1.1 by its own DOCTYPE.

The 38 failures are one fault counted 38 times: DocBook 5.0's XSD is genuinely
invalid under §3.8.6, and `docs/known-gaps.md` sets out why at length. The 7
exclusions are schemas for which "does it load alone?" has no right answer —
five fragments whose types are declared by a sibling, and two files whose
names are the error codes they exist to raise. Each is listed with its reason
in `vendoredExclude` (`tests/corpora/main.go`) and **counted in the output**,
so the size of what is not being scored stays as visible as the score; the
convention is `tests/xsdsuite`'s.

This does not replace UBL and CII. These are mostly test fixtures and
documentation schemas, thinner exactly where those corpora are thick. It
catches an over-strict rule that breaks *any* real schema; it does not catch
one that breaks only commercial vocabularies.

---

## The ratchet

`tests/ratchet.txt` records the highest passing count this repository has
seen. `check.sh` fails when a count goes **down**.

```
DocBook 577
RelaxNGSpectest 965
TestQT3 30233
TestQT3XQuery 30345
TestXSLT30Suite 11481
TestXSLTSuite 6193
VendoredSchemas 185
XSD10 39358
XSD10I 24973
XSD10S 14385
XSD11 41545
XSD11I 26196
XSD11S 15349
XSpec 225
```

`XSD10S`/`XSD10I` and `XSD11S`/`XSD11I` are the schema-validity and instance
halves of the two XSD totals. They are ratcheted separately because the
documentation quotes them separately, and a total cannot be split back.

`TestQT3` and `RelaxNGSpectest` were added late: both suites were being run and
printed, and neither was ratcheted, so an XPath 2.0 or RELAX NG count could
fall without `check.sh` saying anything. `TestQT3` logs one `in-scope:` line
per language version, so the mark is taken from the **last** of them — the
full 2.0 run — rather than the first. The spectest driver reports
`N assertions, M passed` instead of `in-scope: M passed`, so its count is
extracted in `check.sh` and handed to `ratchetCount`.

## Documented figures

Every conformance figure in `README.md` and `docs/` is a copy of a ratchet
mark, made by hand -- about thirty copies of eight figures. Nothing used to
fail when one went stale, and they did: on one day three files carried three
different unit-test counts, and an XSD split was current in `README.md` while
`docs/xsd.md` still had the previous measurement.

`tests/docfigures.sh`, run by `check.sh` in the *documented figures* section,
reads `tests/ratchet.txt` and examines every documentation line that names a
suite's **in-scope denominator** -- the one number in a figure that does not
move between runs (11,518 for XSLT 3.0, 30,346 for XQuery, and so on). The
passing count, failure count and percentage written beside it must equal the
ratchet's, in every form the documents use: `11,484 of 11,518`,
`34 of 11,518`, `11,484 / 11,518 (99.70%)`, `= 99.70%`, `(34 failing)`, and
the `| 11,518 | 11,484 | 99.70% | **34** |` summary-table row. A line stating
two figures is read as two claims. Failures name the file, line and the value
wanted.

It anchors on denominators rather than line numbers so that editing prose does
not break it, and it has no update mode for the same reason `docfigure` has
none: the number sits inside a sentence, and rewriting the number is the moment
to check the sentence. A denominator changes only when the suite checkout or
the scoping changes; when it does, change the table at the top of the script
in the same commit. Prose that states a count without its denominator ("the
37 failures") is not guarded, and the unit-test, fuzz and limit counts are
still asserted at fixed lines by `docfigure` in `check.sh`, whose method is
printed on failure.

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

**Every one of these sets the suite variable, and sets it to an absolute
path.** A suite test skips when its variable is unset, and a *relative* path
resolves against the package directory rather than the repository root — either
way `go test` prints `ok` having run nothing. That is the trap described under
[The suites](#the-suites); `tests/check.sh` avoids it with its `abspath` helper,
and these commands avoid it with `$PWD`, so they must be run from the
repository root.

```sh
# One suite, with its summary
GOXSLT_XSLTS=$PWD/testdata/xslt30-test go test ./tests/xslts/ \
  -run TestXSLT30Suite -count=1 -v

# Every failing case, by name and reason
GOXSLT_XSLTS=$PWD/testdata/xslt30-test GOXSLT_XSLTS_VERBOSE=1 \
  go test ./tests/xslts/ -run TestXSLT30Suite -count=1 -v

# Which test sets are worst — a set failing nearly everything is an
# unimplemented feature; one failing a handful is edge cases
GOXSLT_XSLTS=$PWD/testdata/xslt30-test GOXSLT_XSLTS_BYSET=1 \
  go test ./tests/xslts/ -run TestXSLT30Suite -count=1 -v

# One set only
GOXSLT_XSLTS=$PWD/testdata/xslt30-test \
  GOXSLT_XSLTS_ONLYSET=merge GOXSLT_XSLTS_VERBOSE=1 \
  go test ./tests/xslts/ -run TestXSLT30Suite -count=1 -v

# QT3 equivalents. Without GOXSLT_QT3 these skip and print ok in 0.4s.
GOXSLT_QT3=$PWD/testdata/qt3tests GOXSLT_QT3_VERBOSE=1 \
  go test ./tests/qt3/ -count=1 -v
GOXSLT_QT3=$PWD/testdata/qt3tests GOXSLT_QT3_SET=fn-matches \
  go test ./tests/qt3/ -count=1 -v

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

### A unit test may not cost minutes

The deadline that actually broke CI was not a per-case one. `84735c8` added
`TestMaxPositionsRealBoundary`, which compiled three content models of about
8,192 particles each to drive `maxPositions` at its edges.
`compileContentModel` is a Glushkov construction whose follow-set cost grows as
the **cube** of the particle count — measured on an idle 12-core laptop, 1,024
particles compile in 0.16s, 2,048 in 1.4s, 4,096 in 12s and 8,192 in 90s, each
doubling costing about nine times the last. The test alone ran 202 seconds
there, and under `-race` on a two-core runner it did not finish inside the
25-minute `go test` deadline at all.

That failed **both** CI jobs, which is what made it look like two bugs. The
`test` job runs `go test ./... -race`; `tests/check.sh` runs the same step in
its `race` section before it reaches a single suite, so the conformance job
died in the same place and never printed a conformance number.

The boundary is now driven at a forced budget of 64 via `withBudgets`, which is
what the neighbouring `TestMaxPositionsBoundary` already did. The off-by-one
being asserted is a property of the comparison, not of the constant's
magnitude: at 64 the same three cases run in microseconds and still fail if
`>=` is ever written as `>`. `TestMaxPositionsProductionValue` pins the shipped
8,192 separately, so lowering the real budget stays a deliberate edit.

The rule this leaves: a test in a `go test ./...` package is a unit test, and a
unit test that costs minutes is a bug in the test. Drive a budget's edges at a
forced budget and assert the production value separately.

`op:same-key-023` was the case that proved the rule from the other side: it
builds 75³ = 421,875 keys and calls `map:put` and `map:remove` once for each,
and while `MapItem` was an entries slice plus a rebuilt index both were O(n),
so the case was quadratic and finished in no deadline at all. It was recorded
as a performance defect rather than raised past, and fixing the defect — a
persistent hash array mapped trie — is what made it pass.

---

## Fuzzing

Nine targets, using Go's native `testing.F` and no framework:

| target | package | asserts |
|---|---|---|
| `FuzzParseNoPanic` | `xdm` | `ParseString` never panics; a refusal is an error and never a tree beside it; an accepted tree walks with its parent links intact |
| `FuzzParseDOCTYPE` | `xdm` | the DTD subset parser never panics on a malformed `<!DOCTYPE>` |
| `FuzzLoadSchemaNoPanic` | `xsd` | `Load` never panics at either XSD version, and every content model it accepts compiles to an automaton that answers total |
| `FuzzSchemaComplexity` | `xsd` | the complexity limits refuse a pathological schema rather than running unbounded |
| `FuzzSerializeRoundTrip` | `xslt` | parse → serialise → parse yields the same document, compared on expanded names, kinds and string values |
| `FuzzCompileStylesheetNoPanic` | `xslt` | `Compile` never panics and never returns a stylesheet beside an error |
| `FuzzCompileNoPanic` | `xpath` | the expression compiler never panics, and every parse error carries a spec code |
| `FuzzParseCompactNoPanic` | `relaxng` | the compact-syntax parser never panics |
| `FuzzTokenNoPanic` | `internal/xmlfork` | the forked tokeniser never panics and terminates on any byte string |

Most targets live in `zz_fuzz_test.go` in the package they exercise; the `zz_`
prefix is only to sort it last. Four sit beside the code they cover instead,
in `internal/xmlfork/fuzz_test.go`, `relaxng/compact_fuzz_test.go` and
`xsd/complexity_fuzz_test.go`.

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
ordinary test run, so a large corpus is a tax on every build. The nine targets
together add well under a second.

**The search itself runs nightly, not on every push.**
`.github/workflows/fuzz.yml` runs all nine at `-fuzztime 300s`, one per matrix
leg, on a `schedule:` cron and on `workflow_dispatch` for a run by hand. It is
kept out of the per-push gate on purpose: a coverage-guided search is
nondeterministic, so the same commit can pass one run and fail the next when
the mutator reaches further, and a five-minute job that blocks every merge on
that is a job that gets disabled rather than fixed. What `ci.yml` guarantees is
narrower and deterministic — every seed replays, so a target that stops
compiling is caught in a minute.

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
  2>&1 | sed -n 's/.*FAIL \([^:]*\):.*/\1/p' | sort -u > after.txt
diff before.txt after.txt
```

The extraction takes the case name only, up to the first colon. An earlier
version of this snippet kept everything after `FAIL `, which is the name *and*
the error text — so a reworded message read as a changed case list and the
by-name diff stopped answering the question it exists for. `sort -u` is the
other half: a failure is printed once by the subtest and once by its parent, so
without it every name appears twice.

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

**Skipped is not failed.** The suites skip cases by declared dependency — a
specific Unicode version, a spec version not being measured. (Streaming used to
head that list and no longer does: it was measured and found implemented.) The
XSLT 3.0 suite has 14,601 cases and 11,518 in scope; counting the difference as
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

**Where a value is encoded, test the encoding against the relation.** Several
places here compress a semantic rule into a string so a lookup is one map
access instead of a scan: `xdm.MapKeyOf` for map keys, `xpath.GroupingKey` for
`xsl:for-each-group`. The hazard is that the rule then exists only as the shape
of the strings the encoder happens to build, so any property phrased over the
encoding is satisfied by the encoding and can never contradict it.

Write the relation out separately, from the specification rather than from the
code, and assert that the two agree — in **both** directions. One direction is
always vacuous: "same key implies same value" holds of an encoder that gives
every value a distinct key, and its converse of one that gives every value the
same key. `xdm/samekey_oracle_test.go` does this for `op:same-key`, and adds a
second test that the relation is reflexive, symmetric and transitive, since a
relation that is not an equivalence cannot be implemented by any canonical key
and would leave the first test measuring against nothing.

This is the same failure that let a one-sided soundness property accept a UPA
budget which skipped its own check. A budget, like an encoding, must be tested
against the thing it claims to approximate, not against itself.

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

It runs on **`ubuntu-latest`, `windows-latest` and `macos-latest`**. This
library resolves schema and DTD references by path, and path separators,
symlink support, case sensitivity and the confinement rules `os.Root` enforces
all differ on Windows — so a cross-OS matrix is the only thing that
demonstrates the file handling actually works everywhere, rather than working
on the one platform anybody ran it on. `fail-fast` is off: one platform's
failure must not hide what the other two report.

The matrix stops at this job. `conformance` stays `ubuntu-latest` alone,
because it clones about 944M of W3C corpora and running that three times costs
three times as much for almost no signal the Linux run does not already give.
The steps that are shell scripts rather than a single `go` invocation are
pinned to `shell: bash`, since a Windows runner would otherwise hand them to
PowerShell.

**`conformance`** — fetches the four W3C suites into a cache keyed `suites-v2`
and runs `tests/check.sh`. This is the only place the suites are fetched
reproducibly. Without it the published percentages depend on someone
remembering to run the script, and a number nobody re-measures is a number that
quietly stops being true. It uploads `tests/last-run.txt` as an artifact named
`last-run`, on success and on failure alike — see [Provenance](#provenance).

---

## Provenance

Every figure the gate prints is a measurement, and a measurement whose
conditions are not recorded is a number someone will later read as current.

That is not hypothetical. An external audit report was written against a tree
nobody can now identify, quoted counts that no longer matched, and was read as
a description of this repository — and the reason it could not be refuted on
the spot is that **this repository could not prove what it had measured
either**. `tests/ratchet.txt` holds bare `<name> <count>` pairs, and the CI
cache key is the static string `suites-v2`, so even the suite revision behind a
CI figure was unrecoverable. Two counts from different trees, different Go
versions and different suite checkouts looked exactly alike.

So `check.sh` opens with a *provenance* section, printed into the transcript
and written to `tests/last-run.txt`:

```
go           go version go1.25.0 linux/amd64
commit       f2117cc30374fee7e545fa97a0e741954d96f70e
platform     linux/amd64 (Linux x86_64)
utc          2026-09-10T16:16:52Z
qt3tests     201a6e466940cdfc727f4babfedcde5332b9f578
xsdtests     7bc3365c652a322f3d762021b3879eb92dae7e30
xslt30       fddf1cf920087e791f13315d68dfbe874d97dc56
relaxng      (not a git checkout of its own)
xsltng       a840909a8c82d23458ba72e61e0eed4185be6b74
xspec        799d52a4239931197f5fa71476e79750b3e1d0ee
```

A tree with uncommitted changes is recorded as `(dirty)` rather than refused:
the gate is run on work in progress far more often than on a clean commit, and
a figure measured on uncommitted changes is precisely the one that must not be
quoted as that commit's.

`relaxng` reads `(not a git checkout of its own)` because `testdata/relaxng`
holds a copied `spectest.xml` rather than a clone. That wording is load-bearing.
`git -C` in a directory that is not itself a repository does not fail — it
walks *up* and answers with the enclosing repository's HEAD, which would record
a go-xml commit as the RelaxNG suite revision and look entirely plausible. So
`suiterev` records a revision only when `--show-toplevel` resolves to the suite
directory itself. Comparing against the repository root is not sufficient: in
an agent worktree `testdata/` is a symlink to the primary checkout, so the
enclosing repository is a different path than the root and the bogus answer
survives that test.

**`tests/last-run.txt` is gitignored, deliberately.** It changes on every run,
so committing it would put a diff in the tree every time anyone ran the gate
and make the ratchet's own commits unreadable — and a committed copy would
still only ever say what the last person to commit happened to run. What proves
a figure is the file emitted *beside* that figure: attached to the CI run, or
pasted into the issue that quotes the number. A file in git would be provenance
for the commit; this is provenance for the measurement.

It is **not** written to `tests/ratchet.txt`. The ratchet rewrites that file in
place — `grep -v` the line, append the new one, `sort` — so anything else
living there would be destroyed by the first count that moved.

---

## The second module has to be gated separately

`w3cschemas/` is its own module — the W3C documents it bundles are under W3C
terms, and keeping them out of the MIT core is the point of the split. That
split also means `go list ./...` at the repository root does not reach it, so
every root-level step misses it: build, vet, unit tests and race all stop at
the module boundary.

For a long time nothing else picked it up either. It appeared in neither
`ci.yml` nor `check.sh`, and its `go.mod` named a *published release* of
`github.com/knroy/go-xml` rather than this tree, so its imports of `xsd` and
`xdm` resolved to the module cache:

```sh
$ cd w3cschemas && go list -f '{{.Dir}}' github.com/knroy/go-xml/xsd
/Users/…/go/pkg/mod/github.com/knroy/go-xml@v1.1.0/xsd
```

Its tests passed, and what they were testing was a release a few hundred
commits old. An API break in `xsd` or `xdm` could not have failed them.

Two things fix it:

* **The `require` names v1.2.1, not v1.1.0.** v1.1.0 and v1.2.0 both declare
  `go 1.26`, and that is inherited: a 1.25 toolchain refuses the module
  outright, because MVS reads the required version's own `go.mod` before
  anything else gets a say. Since CI pins 1.25, the old pin made the module
  unbuildable there however it was wired. v1.2.1 declares `go 1.25.0`, which
  also let `w3cschemas/go.mod` drop from `go 1.26` to `go 1.25.0` and `x/text`
  from v0.37.0 to v0.36.0 — both now identical to the root module. The
  `go 1.26` was never a language requirement; it was the stale dependency
  showing through.
* **A step of its own**, in `check.sh` and in `ci.yml`'s fast job. `go list
  ./...` at the root still excludes it, by design, so without an explicit step
  nothing runs it at all.

What the step measures is **the module against the go-xml release its `go.mod`
names, not against this tree** — and that is a deliberate choice rather than an
oversight. A `go.work` at the repository root redirecting the dependency to `.`
was tried and removed. It worked, but it meant two mechanisms — the workspace
and the pin — had to agree, and the one that silently disagrees is the one
nobody notices. w3cschemas is published separately, so the version it pins is
the version its users get; testing it against an unreleased tree measures
something nobody can install.

The consequence is worth stating rather than papering over: this step catches a
broken w3cschemas, and it does **not** catch an API break in this tree that
would affect it. Bumping the pin after a release is what closes that gap, and
that is a release step.

The general shape of this: *a module that is in no gate cannot regress, and a
module pinned to a release is not testing your working tree even when it is.*

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
