# Testing

How this engine is tested, how to run any part of it, and how to read what
comes back.

The short version: `tests/check.sh` is the gate, and a change is not done
until it prints `OK`.

```
tests/check.sh fast     # build, vet (64- and 32-bit), gofmt, unit tests, race — about a minute
                        # (in a checkout without the suites, their lanes are SKIP, not FAIL)
tests/check.sh          # everything available, about eight minutes
```

---

## The layers, and what each one catches

No single method was sufficient. Each was added because the previous set had
let something through, and the column that matters is the last one.

| layer | count | catches | misses |
|---|---:|---|---|
| **Unit tests** | 3,459 | a plausible implementation that is quietly wrong | anything nobody thought to write a test for |
| **Limit boundary tests** | 14 tests | an off-by-one or an overflow at the edge of a configurable limit | a limit nobody added to the inventory |
| **Race detector** | same tests | shared state a single-goroutine run never reveals | a data race on a path no test walks |
| **W3C conformance suites** | 152,260 cases | systematic divergence from the specification | what the suites do not ask about — see below |
| **Real-world stylesheets** | 803 documents | what large stylesheets do that a rule-at-a-time suite does not | constructs those two codebases happen not to use |
| **Production schema sets** | 65 + CII | what modular published schemas do | industries whose schemas are shaped differently |
| **Vendored real-world schemas** | 185 of 230 | a schema-validity rule that has become stricter than the spec, on every checkout — no licensed corpus needed | the deep industry vocabularies only UBL and CII carry |
| **Fuzzing** | 12 targets | a crash, hang or wrong refusal on input nobody would write | anything a coverage-guided search does not reach in the time given |
| **Generated oracle** | 8,397 documents | a *wrong answer* in the content-model matcher, on shapes nobody wrote a case for | only the occurrence shapes whose language is plain arithmetic — no interleaved choices |
| **Wildcard/UPA model** | 60,000 pairs | a *wrong answer* in wildcard acceptance or in the UPA competition rule | anything outside a single wildcard against a single name, or a pair of terms in one choice |
| **The ratchet** | 16 marks | a silent revert, or a fix that quietly costs more than it gains | a regression in something no suite counts |
**How the first four counts are counted**, because "how many tests" has several
honest answers and the one meant here is the narrow one. The three that a
command can settle are asserted for equality by `tests/check.sh`'s *documented
figures* section, which fails the gate when this table drifts from the tree:

* **Unit tests** — `func Test` declarations, not subtests and not table rows:
  `grep -rn "^func Test" --include='*_test.go' . | grep -v '/\.claude/worktrees/' | grep -vc '^\./bench/'`
* **Limit boundary tests** — `func Test` declarations in the
  `*/limits_boundary*_test.go` files of dtd, relaxng, xdm, xpath, xsd and xslt
  (xsd's `xsdnet.HTTPResolver` limits sit in `limits_boundary_http_test.go`, in
  the external `xsd_test` package); most
  are table-driven, so they run rather more than 13 cases:
  `grep -hc "^func Test" ./*/limits_boundary*_test.go | awk '{n += $1} END {print n + 0}'`
* **Fuzzing** — `grep -rn "^func Fuzz" --include='*_test.go' . | grep -v '/\.claude/worktrees/' | grep -vc '^\./bench/'`
* **W3C conformance suites** — the sum of the in-scope totals in the status
  table: XPath 3.1 22,054 + XQuery 3.1 30,530 + XSLT 2.0 6,202 + XSLT 3.0 11,522
  + XSD 1.0 39,388 + XSD 1.1 41,598 + RELAX NG 965. XPath 2.0 and 3.0 are not
  added again — the QT3 catalog is one corpus measured at three XPath versions,
  and the 3.1 lane is the widest of them. An earlier
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

**The wildcard rules are set membership, so the oracle can be the spec text.**
`xsd/wildcard_model_test.go` restates *Wildcard allows Namespace Name*
(§3.10.4.2) and *Wildcard allows Expanded Name* (§3.10.4.3) as four clauses over
sets — variety plus namespaces, then disallowed names, `##defined` and
`##definedSibling` — and the §3.8.6 competition rule as a case analysis over the
two varieties. Nothing in the model calls `Wildcard.Allows`, `AllowsName`,
`Disallows`, `wildcardsOverlap`, `wildcardAdmitsElement` or `positionsCompete`;
that independence is the whole point, because the grouping-key oracle that
called the function it was checking stayed green through a wrong answer for
months. 20,000 generated wildcard/name pairs go through `AllowsName`, and 20,000
term pairs per version are composed into `<choice><a?/><b?/></choice>` and put
through the real `checkUPA`. `##definedSibling` is bound by compiling that
content model rather than by writing the unexported field, so the test walks the
same `bindSiblings` path a schema does. The seed is fixed at `0x5A1D11` for CI
and replayable with `-wildcard.seed`; twenty hand-written keyword examples and
eleven competition examples sit alongside, because a generated failure names a
case index and not the rule that broke. Sabotaging three live paths — `##other`
admitting the absent namespace, `##definedSibling` ignored, two negations
reported disjoint — is caught by both halves.

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

**A loose comparison hides two opposite defects at once.** The XSLT judge
decided an expected error code with `strings.Contains(terr.Error(), a.Code)` —
a substring search over the whole rendered message. Replacing it with a
structured `xdm.ErrorCode` comparison looks like pure hardening and drops the
in-scope figure from **11,490 to 11,134**. Instrumenting the judge to log every
case where the two answers differ found 356, splitting into two groups that
want opposite treatment:

- **351 carry no structured code at all.** A large family of static errors is
  built with `fmt.Errorf` and writes the code as a trailing parenthetical —
  `attribute "as" is not allowed on xsl:call-template (XTSE0090)`.
  `xdm.ErrorCode` looks for a leading code or one delimited by `": "`, so it
  answers `""` for all of them.
- **5 report the correct code but have it read past.** These are built as
  `fmt.Errorf("%s: %s: %w", code, what, inner)`, so the *outer* code is the
  engine's verdict while the wrapped inner cause is still an `*xdm.Error`.
  `errors.As` finds the inner one first: `as-1602` renders `XTTE0505: …:
  FORG0001: …` and is right to, but a strict swap reads `FORG0001` and fails a
  case the engine got right. `variable-0115`, `sequence-0132`, `avt-3201` and
  `error-0340c` have the same shape.

So the outermost code is the authoritative one, and the judge now takes it from
the message prefix, falling back to `xdm.ErrorCode` and then to the substring
match for the uncoded family. The count is unchanged at 11,490, which is the
point: the fix had to tighten the comparison *without* moving the figure in
either direction. `tests/xslts/errorcode_test.go` pins all three shapes.

The real fix is to move that family to `xdm.Errorf` and give `ErrorCode`
outermost-wins precedence — but `ErrorCode` also decides `xsl:catch` matching,
so that is an engine change with its own conformance risk, not a harness one.

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
`xpath.Env.MaxDepth`, since a depth bound of zero would refuse every
document. See [options.md](options.md) for the field-by-field rule.

---

## The suites

Third-party and not vendored. Point the variables at your own checkouts, or
let `tests/check.sh` find them under `testdata/`.

Two lanes find a checkout on their own. `tests/qt3` and `tests/xslts` fall back
to `../../testdata/<suite>` when their variable is unset, so a bare
`go test ./...` in a tree with the checkouts in place really runs them — QT3
takes about 80s rather than the 0.3s it used to report. That 0.3s was the
problem the fallback fixes: an `ok` for a lane that had run none of its 30,345
cases is indistinguishable from an `ok` for a lane that ran all of them.
`GOXSLT_NO_SUITES=1` turns both off regardless of what is on disk, which is
what CI's fast gate and `check.sh`'s unit and race steps set.

Every other lane still skips unless its variable is set, and for those the
defaults in the table below are **check.sh's**, not the tests' own. A relative
path resolves against the package directory rather than the repository root,
so `GOXSLT_XSDTS=testdata/xsdtests go test ./tests/xsd/` skips and prints PASS.
Run `tests/check.sh`, which passes absolute paths and fails a suite that
reports no summary.

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
| W3C xmlconf | *(none)* | `testdata/xmlconf` | XML 1.0 well-formedness and DTD validity (`xdm.Parse`, `dtd.Load`, `dtd.Validate`); fetched by `tests/fetch-xmlconf.sh` |

Fetching the four W3C suites:

```sh
git clone --depth 1 https://github.com/w3c/qt3tests.git      testdata/qt3tests
git clone --depth 1 https://github.com/w3c/xsdtests.git      testdata/xsdtests
git clone --depth 1 https://github.com/w3c/xslt30-test.git   testdata/xslt30-test
git clone --depth 1 https://github.com/relaxng/jing-trang.git /tmp/jing-trang
mkdir -p testdata/relaxng
cp /tmp/jing-trang/mod/rng-validate/test/spectest.xml testdata/relaxng/
```

The XML Conformance Test Suite is one archive (`xmlts20130923`), so it is
fetched and pinned like the Canonical XML corpus rather than cloned:
`tests/fetch-xmlconf.sh` downloads the tarball `tests/xmlconf-corpus.txt`
lists, checks its SHA-256 and unpacks it into `testdata/xmlconf/`, and
`check.sh` runs it before `TestXMLConf` (`tests/xmlconf`). The test skips when
the suite is absent unless `GOXML_XMLCONF=1`, which `check.sh` sets. A not-wf
case passes when `xdm.Parse` refuses it; a valid one when it parses and
`dtd.Load` plus `dtd.Validate` report nothing; an invalid one when it parses
and either reports an error. Both parsers read external entities through a
resolver confined to the suite. `TYPE="error"`, XML 1.1, editions before the
Fifth and `NAMESPACE="no"` cases are out of scope and counted as skipped. The
passing count is the `TestXMLConf` ratchet mark.

The count cannot see one case start failing while another starts passing, so
`tests/xmlconf/expected-pass.txt` lists, one ID per line and sorted, every case
that passes. With the suite present, a listed ID that does not pass fails the
test as `regressed: <id>: <reason>`; a passing ID that is not listed is logged
as `newly passing: <id>` and does not fail. After a change that makes cases
pass (or, deliberately, stop passing), regenerate the list with
`GOXML_XMLCONF_UPDATE=1`, which rewrites the file from the run instead of
checking it. Review `git diff tests/xmlconf/expected-pass.txt` before
committing: every removed line is a case that stopped passing.

```sh
tests/fetch-xmlconf.sh
go test ./tests/xmlconf/ -count=1 -run TestXMLConf -v   # every failure, by type
GOXML_XMLCONF_UPDATE=1 go test ./tests/xmlconf/ -count=1 -run TestXMLConf   # rewrite expected-pass.txt
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

### Tokeniser differential

`internal/xmltok` is this library's own tokeniser. It replaced
`internal/xmlfork`, a fork of `encoding/xml` that had to be re-applied by hand
on every Go release, and the switch was gated by a differential:
`TestTokenDifferential` tokenised every file in every corpus above, plus
`testdata/c14n` and `tests/c14n/testdata`, with both decoders configured as
`xdm` configures its own, a second time with each DOCTYPE's internal general
entities installed, and compared every token byte for byte, `InputOffset` and
`IsVersion11` before every token, and how each stream ended.

On 2026-10-01 it read 59,984 files with **zero differences**, the 175 that
both reject rejected at the same token with the same message; a perturbation
of each compared property was reported, so the harness could fail. The
harness compared against the fork and went with it; it is in the history in
the commit that added `internal/xmltok`. xmltok reproduces the fork's
observable behaviour, quirks included — each is marked `parity:` in the
source — so a conformance change to the tokeniser is now a deliberate, tested
edit rather than a side effect of a Go upgrade.

Six have since been made, against the W3C XML Conformance Test Suite
(`xmlts20130923`, run through `xdm.Parse` beside `xmllint`): white space is
required between attributes (§3.1 [40]/[44]) and after a PI target (§2.6
[16]); comment and PI bodies are checked against [2] `Char` (§2.5 [15], §2.6
[16]); a version `1.x` other than `1.1` is read as 1.0 (§2.8, Fifth Edition);
and `Decoder.Literal` lets `xdm` refuse character data outside the document
element that came from a reference or a CDATA section (§2.1 [1], [27] `Misc`),
which `&#32;` or `<![CDATA[]]>` did not reveal by its text. The XML
declaration is now read only at the very start of the stream and its
pseudo-attributes are parsed by [23]-[26] rather than found by substring
search (§2.8; §2.6 [17] reserves every other spelling of `xml`), which `xdm`
already enforced itself; the one observable change is that `version = "1.1"`,
with the white space [25] `Eq` allows, now declares 1.1 rather than being read
as 1.0. None of them
changed a result in the corpora above. The kept leniencies are listed in
[known-gaps.md](known-gaps.md#the-tokeniser-keeps-six-leniencies).

### Output differential

The suites count passes, so a change can keep every figure and still change
what a case writes. v2 rewrites the node record under every package and must
not change observable behaviour, so it is gated the way the tokeniser swap was:
record what every case actually produces on both sides and show **zero
unexplained differences**.

```
tests/record.sh <dir>                    # ~3 min on an M3 Pro, ~290 MB
go run ./tests/recdiff compare -allow tests/recdiff/allow.txt <dirA> <dirB>
```

`GOXSLT_RECORD_DIR` turns on a small hook in each existing runner
([`internal/record`](../internal/record/record.go)); with it unset nothing is
recorded and nothing changes. What is recorded per case:

| Suite | Key | Recorded |
|---|---|---|
| QT3, four lanes (`qt3-xpath20` … `qt3-xquery31`) | `set/case` | pass flag; the error, or each item's type and the adaptive serialisation; the serialised text a serialization assertion asked for |
| XSLT 2.0 and 3.0 (`xslt20`, `xslt30`) | `set/case` | the error, or the serialised principal result and every secondary result |
| XSD 1.0 and 1.1 (`xsd10`, `xsd11`) | `set/group/test` | `valid`, or `invalid:` and the full error text |
| RELAX NG spectest (`relaxng`) | `case-NNNN/what` | each compile and validate, `ok` or the error |
| Vendored schemas (`vendored`) | path | `loaded` or the error |
| DocBook, XSpec (`docbook`, `xspec`) | file`.out`, file`.err` | the CLI's output and stderr, with `tests/check.sh`'s flags |
| `peppol-cen`, `peppol`, `xrechnung-xr`, `xrechnung-html`, `xmark-0.01`, `xmark-0.1` | file or query | the benchmark workloads through the CLI; stage 2 of XRechnung reads stage 1's output |
| `parse-c14n` | file | Canonical XML with comments of the 1, 10 and 100 MB parse inputs |

A recording is `<suite>.tsv` manifests (key, SHA-256, length) and the bytes,
stored once each under `obj/`. Every path handed to the engine is `testdata/`
with its symlinks resolved, and the CLI runs get a fixed `-now`, so two
checkouts sharing one `testdata/` record the same base URIs and clocks.
`tests/record.sh` also keeps each suite's log under `<dir>/logs`; its in-scope
lines must equal the usual figures, because recording may not change a result.

`compare` prints every case whose bytes differ, with the lines that differ cut
to a window around the first differing byte, and exits 1 if any are left after
the allow rules. A rule in `tests/recdiff/allow.txt` is `<suite glob> <key
glob> <regexp>`, and its matches are deleted from both sides before they are
compared again; every rule says which behaviour change it admits and how many
cases it explained. A case recorded on one side only compares as `<missing>`.
The rules today are generate-id strings (`N<tree>x<order>`, whose tree number
depends on how many trees the process built first) and the clock in two XSLT
cases whose harness does not fix `fn:current-dateTime`. Two more rules once
admitted messages v1 wrote differently from run to run of one binary, XTSE0720
naming whichever attribute set of a cycle it reached first (`error-0720*`) and
`cvc-id.2` listing IDs in Go map order (`validation-1602`). Both messages are
deterministic since `8082a2c` and `edea87c`, and the rules were removed once a
recording of v1 `dev` with those fixes matched without them.

A v1 checkout records with the same hook: apply its commit with the module
path rewritten (`sed 's#knroy/go-xml/v2/#knroy/go-xml/#g'`). The baseline,
`05ca570` (v1 `dev`) against `12720d5` (v2's module rename), 188,903 cases:
188,898 byte-identical, 5 explained, **zero unexplained**, every suite figure
unchanged; a second v2 recording against both also reads zero. Against v1
`dev` at `a33c0b7`, v2 at `5362841` reads 188,903 cases, 188,898 identical, 5
explained by the generate-id and clock rules alone, zero unexplained. After
merging `dev` at `062a821` (check-only `Validate` writes nothing; assertion
copies get the held-back typing), v2 against the same v1 recording reads
188,899 identical, 4 explained, zero unexplained, check-only xsdsuite
verdicts included. Without the
allow file the same comparison reports those 5 and exits 1, and
`tests/recdiff/main_test.go` pins a real difference and a missing case. An
empty or mistyped recording directory is an error, never "zero differences".

---

### Streaming differential

Streamed execution must be byte-identical to tree mode, which stays the
oracle. Two tools check this, and both count what took the streamed path
([`internal/streamtrace`](../internal/streamtrace/streamtrace.go)): runs of
the streamed core, its sNodes by kind, fallbacks and re-runs on the tree, and
guard hits, which must be 0. The guard is xdm's posture guard: a view of a
streamed element (`Cursor.View` in
[`internal/xdmstream`](../internal/xdmstream/xdmstream.go)) asked for
content it does not have panics with `streamtrace.PostureError` and counts
a hit.

```
go run ./tests/streamdiff <base-rev> <outdir>     # ~3 min
go test ./xslt -run 'TestStreamFuzz$' -v          # 1000 cases; GOXSLT_STREAM_FUZZ=N, _SEED=S
```

The streamed XPath split ([`xslt/streamsplit.go`](../xslt/streamsplit.go))
has its own differential against tree evaluation of the same expression:
`go test ./xslt -run TestStreamSplit -v` runs hand-written constructs, the W3C
suite's consuming expressions, and a fuzzer (`GOXSLT_SPLIT_FUZZ=N`,
`GOXSLT_SPLIT_FUZZ_SEED=S`).

`tests/streamdiff` records TestXSLT30Suite four times:
1. on `<base-rev>`, extracted with `git archive`;
2. on the checkout;
3. on the checkout with `GOXSLT_STREAM_OFF=1`, which sends every container
   to the tree;
4. on the checkout with `GOXSLT_STREAM_NORERUN=1` (`streamtrace.SetNoRerun`),
   where a container that fails while streaming returns its streamed error
   instead of running again on the tree.

It compares base with branch, and branch with off, using `tests/recdiff`
and `tests/streamdiff/allow.txt`. That file holds 25 case-scoped rules:
generate-id numbering in docbook-002, si-fork-801, document-2011 and
streamable-007/-022 (a principal input `TransformReader` streamed), the clock in
evaluate-052, cvt-042, format-date-037 and error-3160a, and three cases whose
wanted `XTSE3430` M8's analysis now raises where the base wrote output
(si-fork-902, si-fork-952, su-absorbing-205), which fire only against a base
before M8. Thirteen more came with rounds L and F (c875b977), against base
3fb9c066 (v2.1.1): the version string `system-property('xsl:product-version')`
writes (accumulator-083/-084/-085, evaluate-052, function-lookup-008,
su-absorbing-301, document-2011); `xsl:supports-streaming` reporting `yes`
(system-property-012); a whitespace-stripped source keeping its document URI
(2e860a20: document-0113/-0114/-0115 sort documents by it); and the two places
the off leg differs by design, where `TransformReader` follows the spec and
`Transform` of a parsed tree does not: an absent global context item in a
streamable initial mode (§2.3.2, 6cc2247f: `streamable-116`) and an absent
focus saved by an accumulator function item on a streamed input (§10.3.6,
be09e991: `accumulator-061`). Two came with round SI (lane ST1, against
70e049a5): sx-square-array-041 and -043 stream now, and their `/` sorts the
PRICEs of two trees, whose relative order is "stable but
implementation-dependent" (XPath 3.1 §2.4.1); the catalog accepts either
order. Each rule cites its commit; drop it once the base moves past that
commit.

With `GOXSLT_RECORD_DIR` set, tests/xslts writes
`<dir>/streams/<suite>.tsv`, one line per case that streamed. The runner
fails when any of these is true:
- a difference is unexplained;
- fewer cases streamed than the floor (`-floor`, `GOXSLT_STREAM_FLOOR`,
  default 2661);
- a guard fired;
- the off leg streamed;
- a case with no re-run moved between the branch and norerun legs.

The norerun leg is not compared with recdiff: it differs by design. Each
judged case also gets a line in `<dir>/streams/<suite>-verdicts.tsv` (pass or
fail, and the error code the judge reads), and for every case where a
container failed while streaming the streamed code is set against the branch
leg's, which is the tree's answer, into `<outdir>/errors.tsv`: the same code
and verdict, another code the catalog also accepts (the case still passes),
or a wrong code. That matrix says which constructs' streamed errors can be
final (§19.8.8.5: an error during streamed processing invalidates the output
written before it; §2.14: the first error detected may be the one raised).
The error path's memory is pinned by `go test ./xslt -run
TestStreamErrorPathMemory -v`: a sum over big transactions that fails at the
last one (FORG0001, a final family) holds 3.1 MB streamed, with no re-run,
against the tree's 48.6 MB.

A container's streamed error is final, with no re-run, when its family is
listed in `finalCodes` (`xslt/streamexec.go`; `mergeFinalCodes` adds the
merge's own) and the rest of the document then reads, validates and
accumulates without error (`settle`; the merge's `settleMerge` reads every
input to its end); the list's comment gives each family's argument.
`go test ./xslt -run TestStreamFinalErrors` raises each family streamed and
on the tree, requires the same text with no re-run, then drops the family
from the list and requires the re-run.

An input document's invalidity is final too where the stream's first
failure tells the tree's code (`inputFinalCodes`, `xslt/streamvalidin.go`):
`XTTE1540` for `type=`, and `XTTE1510` for `validation="strict"` when the
schema can assess the document element and the first failure is not an
ID/IDREF one. This holds also when the harness's own validation of a
preloaded source fails first, which it never reports
(`TestStreamInputInvalidMarkedFinal`, streamable-149). The tree validates
before its body runs, so this is its error
whether the streamed body succeeded or failed first. Its text is the first
failure, marked `the first failure detected`, where the tree's lists every
failure (XSLT 3.0 §2.14: "an implementation is not required to signal any
errors other than the first one that it detects"). So the harnesses compare
this one family by code and every other error in full
(`streamtrace.SameError`): `TestStreamFuzz`, `coreCheck`, the output leg,
and streamdiff's base/branch and branch/off comparisons, which add a rule
per such case to `<outdir>/<a>-vs-<b>.allow` and print the count.
`validation="lax"` (XTTE1515 or XTTE1510 depending on every failure) and an
ID-first failure still re-run (`TestStreamInputInvalidReruns`).

An error from a `GroundLater` callback (`internal/xdmstream`) does not stop
the parse. The cursor call during which the copy was handed over returns
it, and the cursor reads on, so `settle` can read the rest of the document:
a final family raised in a late copy (si-map-007, sx-MapExpr-007,
stream-106's validated `xsl:copy-of`) needs no re-run
(`TestStreamGroundLaterErrorFinal`, `TestCursorGroundLaterError`).

A principal input the catalog declares `streaming="true"`, a file or inline
`<content>` (no `@select` or XInclude, no initial-mode selection), is handed
to `TransformReader` unparsed; a validated one through
`TransformOptions.SourceValidation`, when it is valid and the stylesheet's
schema types it as the environment's does; under `GOXSLT_STREAM_OFF=1` it is parsed and given
to `Transform`, so the off leg compares Parse + Transform.

An environment's `<schema>` is loaded as XSD 1.1 when it says
`xsd-version="1.1"` or the document carries `vc:minVersion="1.1"`, and as
1.0 otherwise; read as 1.0, a 1.1 schema such as `strip-space-009.xsd`
(an `xs:assert`) is refused and the case runs with no schema
(`go test ./tests/xslts -run TestEnvSchemaHonoursXSDVersion`).

QT3 is different: its environment schemas are what `import schema` imports,
so `tests/qt3` assembles every one as XSD 1.1, the version `xquery` reads an
imported schema at (`go test ./tests/qt3 -run TestCaseSchemasReadAsXSD11`).
A case written for a 1.0 type system declares `<dependency type="xsd-version"
value="1.0"/>` and is skipped.

`-output` runs a fifth leg with `GOXSLT_OUTPUT_CHECK`
([`tests/xslts/outputcheck.go`](../tests/xslts/outputcheck.go)): every case
again with its principal result streamed to `TransformOptions.Output`,
compared byte for byte, error for error, with `Result.Serialize`, and its
secondary results written to `TransformOptions.SecondaryOutput`, each compared
with `SecondaryResult.Serialize` (the summary counts them). A case whose
third, plain run also differs is counted unstable (the clock, generate-id);
any other difference fails.

At 1d789fec (rounds L, F and M), with `-output` against 3fb9c066, it reads:
- 2,661 cases on the streamed path (2,281 at 9d90a6a7, the completion lanes
  J1–J4): runs core 2,468, main 175 (principal inputs through
  `TransformReader`), merge 34; 26 with a fallback (a held select past its
  cap, or one meeting the document element, answered from the tree), 137
  re-run on the tree; 0 guard hits;
- zero unexplained differences: 11,509 identical, 9 explained by the rules;
- the output leg: 0 bytes and 0 errors differ in all 11,518 cases (13
  unstable: the clock, generate-id); of the 2,661 streamed cases 2,658 are
  identical and 3 unstable, 664 of them writing output before the end; 561
  secondary results in 226 cases, 99 of them in 49 streamed cases.

At round SI lane ST1, with `-output` against 70e049a5: 2,661 cases streamed,
14 with a fallback (23 before: the nine square-array cases stream now), 137
re-run, 0 guard hits, zero unexplained differences (27 rules); the branch leg
11,505 in scope, the norerun leg 11,501. Of the 137 containers that failed
while streaming, 133 give the tree's code and verdict, none another accepted
code, and 4 a wrong one: `stream-002` and `non-stream-002` (a missing
document: the streamed open error carries no `FODC0002`) and `stream-104` and
`non-stream-104` (`XTTE1510` where the tree's `type=` validation raises
`XTTE1540`).

At round SI lane ST3, with `-output` against a77565f9: 2,661 cases streamed,
8 with a fallback, 28 re-run (107 at a77565f9), 0 guard hits, zero
unexplained differences; base, branch and norerun legs 11,505 passing. All
28 containers that still fail while streaming give the tree's code and
verdict: XPTY0004 (14, not listed: a rewritten set operator's text says
"union" for an `except`), the input's own invalidity under `validation=`
(5) or `type=` (2: the tree's text lists every failure, the stream's the
first), XTDE0640 (1), and 6 whose error the settle could not confirm
(`merge-072`, `merge-074`, `si-map-007`, `sx-MapExpr-007`, `sx-treat-102`,
`sx-treat-103`).

At 7ee44d8b (rounds SH and SI integrated), from the stream records of a
`TestXSLT30Suite` run: 0 fallbacks (23 at round SG) and 9 re-runs (137), 0
guard hits, 11,505 passing. Since ST4 made XPTY0004, XTDE2220 and XTDE0640
final, the 9 are the input's own invalidity under `validation=` (5, XTTE1510)
or `type=` (2, XTTE1540), and `si-map-007` and `sx-MapExpr-007` (XTDE3365);
each gives the tree's code.

The TSV line holds the runs per strategy (core: containers; main: principal
inputs `TransformReader` streamed; merge: streamed merge sources), fallbacks, guard hits, re-runs and
sNodes per kind. The core has its own differential, `go test ./xslt -run
TestStreamCore -v`: hand-written bodies × documents × whitespace
declarations, the document-element fallback, the result budgets pinned to
the retired burst path's, and, without `-short`, peak heap against the tree.

`TestStreamFuzz` (skipped under `-short`) generates a record document and a
streamable stylesheet per seed. It runs each streamed and on the tree and
requires exactly the same output and error text; a re-run where the tree
succeeds counts as a difference. The core's shapes come from their own random
stream, and modes and template rules from a third (`GOXSLT_STREAM_FUZZ_CORE=0`
and `GOXSLT_STREAM_FUZZ_MODES=0` reproduce the earlier cases). Grouping
cases come from a fourth stream (key-run documents, every grouping form, the
body reading the group or refused; the modes grammar's rules group too, for
`TestStreamFuzzMain`); `GOXSLT_STREAM_FUZZ_GROUPS=0` reproduces the M2 cases. Try and function-call
cases come from a fifth stream, and the modes grammar's rules get tries and
calls too; `GOXSLT_STREAM_FUZZ_TRY=0` leaves them out. Accumulator cases
come from a stream of their own (random declarations read in every way a run
answers, `use-accumulators` lists, failing rules), and a fifth of the modes
grammar's stylesheets get accumulators, so `TestStreamFuzzMain` has them;
`GOXSLT_STREAM_FUZZ_ACCUMS=0` leaves them out. Fork and map cases (1–4
prongs or entries, duplicate keys, a fork per record, under `xsl:try` (which
streams since L1's cca65c41), with limits) come from another, and the modes grammar's rule bodies get forks
and maps too; `GOXSLT_STREAM_FUZZ_FORKS=0` leaves them out.
`TestStreamFuzzMerge` generates 1–3 logs (sorted, ties, out of order,
non-numeric keys, malformed) and 1–3 merge sources over them, with collected
sources, descending order, seven actions and node limits. `TestStreamOutFuzz`
gives each case of the core and modes grammars random serialization
parameters (method, indent, character maps, cdata-section-elements, now and
then one that holds the result to the end) and a random shape around its
output (an element, an html page, a try), and requires the bytes `Output`
receives and the error to be `Result.Serialize`'s; a share of its cases wrap
template bodies in tries (rollback yes and no, nested, items and errors before
and after, catches that take or miss the error; `GOXSLT_STREAM_FUZZ_TRYOUT=0`
leaves them out), and a share of those try bodies make one element (plain,
attributes before or after content, html) or begin with attributes.
`TestSecondaryOutputFuzz` runs result-documents before,
after and inside streamed containers and streamable modes (per record, per
group, failing, under a try, nested, colliding) with and without
`SecondaryOutput`, comparing the principal result, every secondary result by
URI and the error text, and that each writer is opened and closed once. The gap lanes added
grammars of their own, each drawing its share of the seeds first, so per-grammar
tallies at a seed are not comparable across them: held and counted selects,
`xsl:copy` select, on-empty and validation (`GOXSLT_STREAM_FUZZ_HOLD=0` leaves
them out); set operators, maps, positional steps and loop or apply windows
(`GOXSLT_STREAM_FUZZ_PATHS=0`); maps making nodes, `tail()`, text value
templates, consuming tests, arrays and maps (`GOXSLT_STREAM_FUZZ_SHAPES=0`);
whole-argument functions, `trace`, `xsl:message` (messages compared too),
tries over folds, `xsl:fallback` (`GOXSLT_STREAM_FUZZ_G5=0`); validated
documents (`GOXSLT_STREAM_FUZZ_VALID=0`); and climbing and
leaf tails and patterns drawn inside the core grammars. `TestStreamFuzzG6`
compares `xsl:result-document` (principal and secondary results), `.`
groupings and copy populations with the tree. A failing case prints its seed
and the command that replays it.

At 7344aa13, 100,000 cases (seed 1) give 0 differences, 36,142 of them
streamed and 11,523 of those mode shapes; 100,000 of the M1 grammar alone
give 0 too, as do 100,000 of the earlier grammar, in which the retired burst
loops needed an error-order allowance of 9 per 100k (at 75a97759). At
0b88ba7a, 100,000 cases (seed 1) give 0 differences: 42,763 streamed, 11,526
of them running a streamed grouping; `TestStreamFuzzMain` 100,000 give 0,
1,719 running a grouping rule; 50,000 (seed 12345) with
`GOXSLT_STREAM_FUZZ_GROUPS=0` give the base's cases and 0 differences. At
40ab5b4b, 100,000 cases give 0 differences, 637 of them catching an error on
the cursor; `TestStreamFuzzMain` 100,000 give 0, 1,992 streamed with a try
rule. At badba78b, 100,000 cases each (seed 11) of `TestStreamFuzz`,
`TestStreamFuzzMain`, `TestStreamFuzzMerge` and `TestStreamOutFuzz` give 0
differences, and at fe989cf3 100,000 each (seed 1) give 0 again. Each gap
lane ran the four at 100,000 (seed 1) with 0 differences on its own branch;
the path grammar's first run found 6 faults its lane then fixed, and a run
after the merge (seed 31) 5 more, in loops over kept leaves. At c257efe2
`TestStreamFuzz` and `TestStreamFuzzMain`, 100,000 each (seed 51), give 0
differences. After H1–H3, `TestStreamFuzz`, `TestStreamFuzzMain`,
`TestStreamFuzzMerge`, `TestStreamOutFuzz`, `TestStreamFuzzG6` and
`TestSecondaryOutputFuzz` each give 0 differences in 100,000 cases. After the
completion lanes (J1–J4) the six are run at 100,000 cases (seed 91). After
round L the first run (seed 91) found 9 seeds over four causes, fixed in round
F (F8); at 1d789fec, after rounds F and M, `TestStreamFuzzMain`,
`TestStreamFuzzMerge`, `TestStreamOutFuzz`, `TestStreamFuzzG6` and
`TestSecondaryOutputFuzz` give 0 differences and `TestStreamFuzz` 2 (seeds
34820 and 71752, both OUTPUT), fixed by F9 (537885e3): after it `TestStreamFuzz`
gives 0 differences in 100,000 cases (seed 91).

Audit round A (at 32e31c6f) ran four lanes against the streaming branch, each
with reproducers through the public API kept outside the tree (`zz_*` probe
files, not committed). A1, streaming correctness: hand-written differentials
of streamed against tree runs over the rounds' new shapes, `git bisect run` to
find each fault's commit, and `TestStreamFuzz` and `TestStreamFuzzMain` at
100,000 cases on three new seeds (1000001, 2000001, 3000001): 2, 1 and 1
differences, all one fault (A1-F1), and 0 for `TestStreamFuzzMain`. A2,
memory: a harness sampling peak heap, live heap and RSS of 42 strategies at
10 to 500 MB against the tree, every row checked to have streamed and to give
the tree's output, with goroutines and the heap after return. A3, profiling:
benchmarks against v2.1.1 (n=10, interleaved) and CPU and allocation profiles
of five streamed workloads over 111 MB of XMark. A4, security: 26 href forms
over four instructions with real and symlinked roots, DTD and entity
expansion, every resource limit with and without `Output`, recursion depth,
and a 600 s native fuzz of `TransformReader` (6.94M executions, 0 failures).
It found 4 silent wrong results, 9 P1 and 12 P2 items; fix round R (R1 to R6)
closed 17, and [known-gaps.md](known-gaps.md) records what is open. After R1
`TestStreamFuzz` gives 0 differences at all three new seeds, 100,000 cases
each. At ce880ea0 (all of round R merged) the six differential fuzzers give
0 differences at 100,000 cases each (seed 91), and `tests/streamdiff` 0
unexplained with 2661 cases streamed.

Round R added fifteen regression tests:
`TestStreamTailFocusPosition`, `TestStreamIterateLast`
(`xslt/streamtailfocus_test.go`; A1-F1, A1-F4); rows in `l7Rows`
(`xslt/streaml7_test.go`; A1-F2, A1-F3); `TestStreamCopyAccumulatorValues`
(`xslt/streamcopyaccum_test.go`) and `TestCopyOriginReleasedWithCopy`
(`xslt/copyorigin_test.go`; A2-1, A2-2); `TestLiveCarriedTreeMeasuredOnce`
(`xslt/livebudget_test.go`), `TestCancelledBuiltInRulesAndStreams`
(`xslt/transform_test.go`) and `TestCancelledStreamIsNotRerun`
(`xslt/streamexec_test.go`; A4-F1, A4-F2, A2-5, A2-4);
`TestStreamForkGroupByMaxNodes` (`xslt/streamcopy_l3_test.go`),
`TestStreamMainLeafTopUnderOutput` (`xslt/stream_mm8_test.go`),
`TestStreamInlinedRecursionLimit` (`xslt/streamfn_inline_test.go`),
`TestMergeSourceURIChecked` (`xslt/mergestreamable_test.go`) and
`TestFileResolverOutsideErrorNamesOnlyTheHref` (`xslt/resolver_test.go`;
A2-3, A2-6, A4-F5, A4-F6, A4-F3); `TestCrossModulesWalkedOnce`
(`xslt/streamxmod_memo_test.go`), `TestRewindKeepsWhatWasRead` and
`TestDrainStoresTextOnce` (`internal/xdmstream/rewind_test.go`; A3-1, A3-2).

`TestStreamFuzzMain` applies each case of the modes grammar to its document
in an initial mode through `TransformReader` and through `xdm.Parse` +
`Transform`, compared exactly, `Result.Nodes` included (`GOXSLT_STREAM_FUZZ=N`,
`_SEED=S`). At 6a4d2832, 100,000 cases give 0 differences: 32,613 streamed,
7,855 of them ending in an error, each Parse + Transform's. `go test ./xslt
-run TestStreamMain -v` compares `TransformReader` with Parse + Transform over
hand-written cases, errors and parse limits, and, without `-short`, peak
heap. `go test ./xslt -run TestStreamGroup -v` compares groupings with the
tree over key-run documents and whitespace declarations, errors in the
tree's order through `TransformReader`, and, without `-short`, peak heap.
`go test ./xslt -run 'TestStreamTry|TestStreamCall|TestStreamAttributeSets' -v`
compares tries (errors at record k, the tree's error code and description, a
parse error after a caught one, `TransformReader`), calls and attribute sets
with the tree. `go test ./xslt -run TestStreamAccum -v` compares accumulators
with the tree in containers and through `TransformReader`, errors in the
tree's order, the item limit, and, without `-short`, peak heap;
`go test ./internal/xdmstream -run 'TestObserverSeesEverything|TestTee'`
checks the cursor's observer against a tree walk and the tee against one
cursor. `go test ./xslt -run 'TestStreamFork|TestStreamMerge|TestStreamDynamicCall' -v`
compares forks, maps and merges with the tree, the tee's error order, host
function items reaching a stream (a hang fails), dynamic calls, and, without
`-short`, a merge's peak heap. `go test ./xslt -run TestStreamOut -v`
compares `Output` with `Result.Serialize` over shapes, `MaxNodes` under
`Output`, indented output held as bytes, and, without `-short`, peak heap.
The gap lanes' differentials, each core or `TransformReader` against the tree:
`go test ./xslt -run 'TestStreamHold|TestStreamResultValidation|TestStreamCopySelect|TestStreamOnEmpty'`
(held and counted selects, validation, `xsl:copy` select, on-empty);
`-run 'TestStreamUnion|TestStreamIdentity|TestStreamPop'` (set operators, the
identity template, loop and apply populations); `-run TestStreamShapes`
(maps, `tail()`, text value templates, consuming tests, arrays);
`-run 'TestStreamLeaves|TestStreamClimb'` and `go test ./internal/xdmstream
-run 'TestLeafMatchesTree|TestCursorParsesOnTheReader'` (kept leaves,
climbing, the step-function cursor); `-run 'TestStreamWholeFns|TestStreamMessages|TestStreamTryFoldOrder|TestStreamFallback|TestStreamMainLiteralGlobals|TestStreamPatternMatchesNoNode'`;
`-run 'TestStreamResultDoc|TestStreamGroupDotPattern|TestStreamForkFused'`;
and `go test ./tests/xslts -run TestStreamedSourceInline` (the harness hands
inline streaming sources to `TransformReader`). For validated inputs, try
output and secondary results: `go test ./xsd -run
'TestStreamValidationMatchesTree|TestStreamValidationRefusals'` (the validator
on a cursor's events against the typed copy) and `go test ./xslt -run
'TestStreamInputValidation|TestStreamMergeValidation|TestStreamMainValidation'`;
`go test ./xslt -run TestStreamTryOut` (tries under `Output`: rollback,
nesting, indent, html, namespaces, `rollback-output="no"` and XTDE3530, against
`Result.Serialize`); `go test ./xslt -run TestSecondaryOutput` (each writer's
bytes against `SecondaryResult.Serialize`, held results, errors and
`MaxNodes`); `go test ./xslt -run
'TestXTDE1500SymlinkedPath|TestResultHrefNotSymlinkResolved'` (doc() and
streamed source-document, both directions; hrefs compared lexically). For the
completion lanes: `go test ./xslt -run
'TestStreamForeignUnionOrder|TestStreamHeldGroupCap'` (unions with a
variable's nodes in either creation order, the held group-by's cap);
`-run TestStreamTryOutOneElement` (a try around one large element under
`Output`: `MaxNodes` with `rollback-output="no"`, peak heap with "yes");
`-run 'TestStreamLiveBuiltIn|TestStreamDispatchAllocs|TestStreamDispatchWarnings'`
(the built-in rule on the parse's record, allocations per record against the
tree's, warnings as the tree's). For the completion lanes K1–K5, written and
not yet run except where marked: `-run
'TestStreamSheetCalls|TestStreamK2|TestStreamGroundIdentity'` (stylesheet
functions on values or grounded copies; the split's shapes, conditions and
value templates, of which the `($v | P)/T` rows ran; identity and set
operators in a grounded loop body), `-run
'TestStreamAsReadMarked|TestStreamMinOfContextBelowRoot'` (a harness-validated
document read with the default validation, `min(.)` below the document node),
and `-run TestStreamOutLiveHeapFlat` (the live heap under `Output` may grow
by at most 1 MiB from 10% to 90% of the output; skipped under `-short` and
`-race`); new rows in `TestStreamUnion`, `TestStreamShapes`,
`TestStreamWholeFns`, `TestStreamPop` and the core tables, and in
`TestStreamResultValidation` (ran) for `xsl:copy` with validation of items that
are never attributes. For the completion round L (L1–L7):
`-run 'TestStreamXMLToJSONMatchesTree|TestStreamInlinedCallsMatchTree'`
(`xml-to-json` from the events; function bodies with instructions planned in
the caller's plan, call frames, one-node parameters); `-run
'TestStreamDocCopyEvents|TestStreamCopyDocNode|TestStreamCopyChoice|TestStreamLeafAtomicSequence|TestStreamAtomicTypedVariable|TestStreamApplyLeafMatches|TestStreamDocCopyIDs|TestStreamDocEntities|TestStreamCommentSelectValues|TestStreamValueOfTextBeside|TestStreamApplyFreshCopies|TestStreamGroupByEventCopies'`
(document-level copies, the DOCTYPE, leaf rules, copies applied and
grouped); `-run
'TestStreamAccumReadsMatchTree|TestStreamTryAccumFailsAtStart|TestStreamTryApplyMatchesTree|TestAccumDynamicCallStreamedInput|TestStreamMergeL4|TestStreamMergeAccums|TestStreamCopyAccumulatorsNo'`
(accumulators reading accumulators, §18.2.5 errors in both modes, try over
apply-templates, §10.3.6 on a streamed input, merges with `last()`, messages,
accumulators and the two-pass collect, `copy-accumulators`); `-run
TestStreamL6MatchesTree` (loop bodies at a leaf's focus, copies applied,
`has-children`, choice and union populations); and `-run
'TestStreamL7MatchesTree|TestStreamL7UsePackageTrust|TestStreamL7ImportTrust|TestStreamL7PackageFrame'`
(analysis rows, the alias guard, §2.3.2, trust across packages and imports,
the cross-package call frame). L1 and L5 added rows only: `TestStreamApply`,
`TestStreamCore`, `TestStreamFork`, `TestStreamMain` (with a new `ndata`
document), `TestStreamUnion`, `TestStreamGroup`, and `TestStreamSheetCalls`
gained rows from L2. `mainRun`, the main-mode oracle, compares
`TransformReader` with `xdm.Parse` + `Transform` except for rows marked
`<!--2.3.2-->` (`globalItemRow`), whose globals read the context item; those
compare with `TransformReader`'s own tree fallback, where the global context
item is absent too (e3659459). Rounds F and M added, in
`xslt/streamstrval_test.go`, `-run
'TestStreamStringOfTyped|TestStreamCopyNamespaceOrder'` (`string()` of a typed
node is its string value; a streamed copy declares namespaces in source
order); in `xslt/streamf6_test.go`, `-run
'TestStreamGroupOuterReadsNoGroup|TestStreamValuesApplyTakesNoParams|TestStreamAccumCircularAfterCopy'`
(`current-group()` outside a split's consuming path, a values population with
parameters, XTDE3400 on every read of a circular accumulator); in
`xslt/stream_mm5_test.go`, `-run
'TestStreamAccumMapTyped|TestStreamAccumQuantifiedPattern|TestStreamApplyInMapTypedVariable|TestStreamTryCatchPrefixes|TestStreamMergeCollectedPathKeys|TestStreamGroupMapOfAtomics'`;
in `xslt/stream_mm7_test.go`, `-run
'TestStreamGroupCopiesParam|TestStreamLoopCopiesSized|TestStreamSnapshotClimb|TestStreamInstrBodyBurst|TestStreamOneMatchTail|TestStreamDynCallCopies'`;
and in `xslt/stream_mm8_test.go`, `-run
'TestStreamXMLToJSONNestedMatches|TestStreamMainLeafTop|TestStreamSchemaTypedMax|TestStreamGroupNodeFreeBody|TestStreamSourceDocumentFragment|TestStreamEvaluatePreprocessor'`.
Each compares the streamed run with the tree's and asserts whether it streamed;
the other fixes added rows to the existing tables (`mainCases`, `coreBodies`,
`l7Rows`, `accumReadDecls`, `tryOutSheets`, `TestStreamDocCopyEvents`).


## The ratchet

`tests/ratchet.txt` records the highest passing count this repository has
seen. `check.sh` fails when a count goes **down**.

```
DocBook 578
RelaxNGSpectest 965
TestQT3XPath20 15217
TestQT3XPath30 19362
TestQT3XPath31 22054
TestQT3XQuery 30516
TestXSLT30Suite 11499
TestXSLTSuite 6193
VendoredSchemas 185
XSD10 39358
XSD10I 24973
XSD10S 14385
XSD11 41567
XSD11I 26217
XSD11S 15350
XSpec 225
```

`XSD10S`/`XSD10I` and `XSD11S`/`XSD11I` are the schema-validity and instance
halves of the two XSD totals. They are ratcheted separately because the
documentation quotes them separately, and a total cannot be split back.

`RelaxNGSpectest` was added late: the suite was being run and printed and was
not ratcheted, so a RELAX NG count could fall without `check.sh` saying
anything. The spectest driver reports `N assertions, M passed` instead of
`in-scope: M passed`, so its count is extracted in `check.sh` and handed to
`ratchetCount`.

**The three XPath versions carry a mark each.** `TestQT3` runs 2.0, 3.0 and
3.1 as subtests and logs one `in-scope:` line per version, and there used to
be a single `TestQT3` mark over them. It did not measure any of them. The
lane invoked `go test -run TestQT3`, which is a *substring* match and so also
selected `TestQT3XQuery`; the XQuery summary was then the last `in-scope:`
line in the output, and the mark selected from it recorded the XQuery count
under an XPath name. All three XPath counts were ratcheted by nothing, and
XQuery ran twice per gate — once here and once in its own lane. The pattern
is anchored (`-run '^TestQT3$'`), the `TestQT3` mark is gone, and
`TestQT3XPath20`, `TestQT3XPath30` and `TestQT3XPath31` each guard their own
version. The lane reads the version from the `=== RUN TestQT3/XPath_x.y`
line that precedes each summary rather than from line order, so reordering
the subtests cannot silently swap two marks.

### The gate does not write the file

`check.sh` **never** rewrites `tests/ratchet.txt`. A figure that has gone
**up** fails the run with a message saying so and naming the command that
records it:

```
GOXSLT_RATCHET=update tests/check.sh
```

That is the only mode that writes. It used to record a new high
automatically, which meant an ordinary gate run edited a tracked file: the
tree went dirty mid-run, and the provenance block that runs later then
recorded the run as having been made against a dirty tree — an artifact
describing a state the gate itself had created. Recording a new high is a
deliberate act with a commit behind it, so it is now a deliberate invocation.

A **missing** mark also fails, rather than bootstrapping itself. Writing it
once is friendlier for a genuinely new mark, and was rejected for
consistency: from inside the script a mark missing because it is new and a
mark missing because someone deleted the line are indistinguishable, and the
second is exactly the silent-revert case the file exists to catch.

`GOXSLT_RATCHET=off` still skips the check entirely.

### The guards fail closed

Each helper parses a count out of the driver's output. They used to open with
`[ -n "$count" ] || return 0`: a driver whose wording changed stopped matching
the parser, the helper returned success without comparing anything, and the
gate reported **PASS with the ratchet silently disabled**. A guard that turns
itself off under exactly the conditions it exists to catch is worse than no
guard, because it reads as a check that ran.

An unreadable count is now a gate failure. The message names the mark, says
that nothing is guarding it, and prints the input the parser could not read,
because the fix is always "the driver now says X, teach the parser X". This
couples the parsers to the drivers' wording deliberately: if one changes, the
other must be made to match, and the gate says so.

## Documented figures

Every conformance figure in `README.md` and `docs/` is a copy of a ratchet
mark, made by hand -- about thirty copies of eight figures. Nothing used to
fail when one went stale, and they did: on one day three files carried three
different unit-test counts, and an XSD split was current in `README.md` while
`docs/xsd.md` still had the previous measurement.

`tests/docfigures.sh`, run by `check.sh` in the *documented figures* section,
reads `tests/ratchet.txt` and examines every documentation line that names a
suite's **in-scope denominator** -- the one number in a figure that does not
move between runs (11,522 for XSLT 3.0, 30,530 for XQuery, and so on). The
passing count, failure count and percentage written beside it must equal the
ratchet's, in every form the documents use: `11,505 of 11,522`,
`17 of 11,522`, `11,505 / 11,522 (99.85%)`, `= 99.84%`, `(17 failing)`, and
the `| 11,522 | 11,505 | 99.85% | **17** |` summary-table row. A line stating
two figures is read as two claims. Failures name the file, line and the value
wanted.

That check anchors on the denominators in its own table, and five ratchet
marks — the XPath ones, `RelaxNGSpectest`, `DocBook`, `XSpec` and
`VendoredSchemas` — were never in it. The figures they measure were guarded
only where a generated region happened to carry them, and the hand-written
copies were guarded by nothing: the three XPath rows and the RELAX NG row of
the README status table, DocBook's **577** and XSpec's 225 in its real-world
section, and the same XPath and RELAX NG figures in `docs/conformance-gaps.md`,
`docs/todo.md` §1.3 and `docs/validation.md`. Every one of those is a generated
region now, fed from `results.json`, which already records those four suites.
A figure that sits mid-sentence uses the *inline* form — both markers on the
prose line, around the number alone — because an HTML comment on a line of
its own is a block, and a block inside a paragraph or a list item splits it
mid-sentence; a table row takes the ordinary block form.
`TestInlineRegionStaysOnItsLine` pins the difference, and
`TestHandEditInsideARegionIsCaught` moves a digit in every region, inline ones
included, and asserts that `-check` would see it. `VendoredSchemas` is a count
of the tree, which `results.json` does not record by design, so it went into
`docfigures.sh`'s table instead with 230 as its denominator: the `185 of 230`
in the layer table above is checked against the ratchet like any other row.

The three XPath figures are in that table too, one row each:

```
TestQT3XPath20  15217 XPath-2.0
TestQT3XPath30  19362 XPath-3.0
TestQT3XPath31  22054 XPath-3.1
```

They need a row each because the three are three different scopings of one
catalog and no single denominator covers them. Until the per-version marks
existed there was nothing to check them against, so the published
`15,217 / 19,362 / 21,898` in `README.md`, `docs/conformance-gaps.md`,
`docs/stats.md` and `docs/todo.md` were guarded by neither the ratchet nor
this script, and a stale copy of any of them failed nothing. They are at
100.00% and so contribute zero failures; they are deliberately **not** added
to the Total-row sum below, which would only create a second place for the
same denominators to be written down.

### The figures name the suite revision they were measured against

`tests/conformance/results.json` records the commit of each vendored W3C
suite under `suite_revisions`, and
`TestRecordedSuiteRevisionsMatchTheCheckouts` fails when a recorded revision
and the checkout disagree.

Without it a figure in that file is not reproducible. The suites are separate
checkouts, and CI cloned them from their default branch, so **a suite update
could move a count with no change to this repository at all** — and the
ratchet would then fail on a commit that changed nothing, with nothing to
distinguish that from a real regression. `tests/check.sh` had always printed
the revisions into its provenance, but that is a per-run artifact that
expires; this is the copy that travels with the numbers it explains. CI now
clones each suite at the recorded SHA, so adopting a newer suite means
changing the SHA, re-running the gate and updating `results.json` in one
commit — the count moves together with the reason it moved.

A suite that is not its own git checkout is recorded as absent rather than
wrong. `testdata/relaxng` is vendored files, and asking git about it answers
with *this* repository's HEAD; both the script and the test apply the same
containment check that `suiterev` does, so that answer is never mistaken for
a suite revision.

### The race lane says when it cannot run

`-race` needs cgo, and cgo needs a C toolchain. An external audit ran the gate
without one, got a failure whose message was about the toolchain, and reported
"race tests failed" — the cause only emerged after installing gcc. The lane
now tests for a usable compiler and **skips with a reason** instead: a check
that did not run must not look like one that passed, and must not look like
one that failed either. `ci.yml` sets `CGO_ENABLED=1` explicitly on the same
step, so the prerequisite is part of the recipe rather than a property of the
runner that happens to satisfy it.

### Spec citations are checked the same way

`tests/speccites.sh`, run by `check.sh` beside the figures check, holds the
other kind of claim the documents make: a **section number**. The streamability
files cite the spec on nearly every rule — 1058 `§N.N` references across
`xslt/stream*.go` and `xpath/*.go` — and they were written against the **Last
Call Working Draft**, which the Recommendation renumbered. That produced
citations pointing at real sections about the wrong subject: `§19.8.8.11`
("Dynamic Function Calls") on code implementing variable references, `§18.2.8`
("Importing of Accumulators") on their streamability.

A wrong section number is worse than a dangling one. It reads as authority, and
following it lands on plausible text, so nothing looks amiss. One of them —
the variable-reference citation — had **already been diagnosed** in a comment
in `streamfunctions_test.go` and corrected at exactly one of its 25 sites; the
other 24 stood for as long as the file did.

The script parses the `<hN>` headings out of the vendored specs under
`testdata/xslt30-test/specs/` — XSLT 3.0, F&O 3.1, XPath 3.1 and
Serialization 3.1 — and runs two checks. It refuses to run if fewer than 200
XSLT headings or 30 `Streamability of xsl:*` headings parse, so a change to the
spec's markup cannot make either pass vacuously.

1. **Dangling** — complete, over every cited file. A number naming no section
   of any vendored spec, listed with file, line and source line.
2. **Wrong subject** — partial, `xslt/stream*.go` only. The 19.8.4 headings are
   `Streamability of xsl:NAME`, one-to-one with the `case "NAME":` labels the
   code switches on, so a citation under such a case must name that
   instruction's section. 30 sites qualify. This is what catches the
   LCWD-to-REC renumbering, which shifted every entry after `xsl:text` by two.

Being exact about the second check's reach matters, because the first one
passed green on 978 sites while sixteen of them named the wrong subject. It
does **not** check: citations outside a `case` block (helper functions, file
headers, the 19.8.8 expression table — roughly half of `xslt/stream*.go`); any
citation in `xpath/`, which is prose about functions rather than a switch over
instruction names; or any spec but XSLT 3.0. Those get the dangling check only,
and a wrong subject among them still needs a reader — which is why every
corrected site records the number it used to carry.

The 491 citations in `xsd/` and `xdm/` are not checked at all: the XML 1.0 and
XSD prose specifications are not vendored here, so there is nothing local to
check them against. Vendoring those would close the gap.

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

## The generated conformance summary

Every check above guards a figure someone typed. One figure could not be
guarded that way, and it was the one that went wrong: the **Total** row of the
summary table in `docs/conformance-gaps.md` is the *sum* of the other rows, so
every row could agree with the ratchet while the sum disagreed with all of
them. It did. The document printed **168** disagreements while its own rows
summed to **104**, and nothing failed, because nothing anywhere did the
addition.

So the total is no longer written anywhere a person can write it.
`tests/conformance/results.json` is the checked-in source of truth. It records,
per suite: the suite identifier, the specification edition, the passed count,
the disagreement count, the total in scope, the run date, the command that
produced them, and the disagreeing case IDs with a verdict from a closed
vocabulary — `implementation`, `fixture`, `implementation-defined`, `optional`,
`deliberate-divergence`, `not-run`.

`tests/conformance-docs.go` reads that file and rewrites exactly one region of
`docs/conformance-gaps.md`:

```text
<!-- BEGIN GENERATED CONFORMANCE SUMMARY -->
…the table and its arithmetic, derived from results.json…
<!-- END GENERATED CONFORMANCE SUMMARY -->
```

Everything outside the markers is hand-written analysis — the per-case
verdicts, the spec citations, the measured costs of each divergence — and is
never touched. Only the table and the total are generated.

```sh
go run tests/conformance-docs.go          # rewrite the region
go run tests/conformance-docs.go -check   # fail if it would change
```

Two properties are load-bearing:

* **The generator computes the total; it never reads one.** `Total()` sums the
  suite rows and there is no field a document or a JSON file could use to
  supply a different answer. Real-world corpora — DocBook xslTNG and XSpec —
  live in a separate `corpora` list, so they cannot reach the total by
  accident; the total counts W3C disagreements only.
* **`passed + disagreements == total`, per suite, or generation fails by
  name.** An unnamed arithmetic error in a nine-row file is barely better than
  none; the message names the suite and prints all three numbers.

`check.sh` runs `-check` in its *generated conformance summary* section rather
than regenerating and diffing with git: the gate is run on a dirty tree far
more often than on a clean commit, and a `git diff` there would report work in
progress as a failure.

`tests/docfigures.sh` keeps its per-row denominator check and adds one of its
own: it derives the total a second time, from `tests/ratchet.txt`, and compares
it with the total the generated region prints. The two numbers come from two
different files by two different routes, so agreement between them means
`results.json` was re-measured rather than merely re-typed.

To record a new measurement: run the suites, copy the passing counts into
`tests/conformance/results.json` together with the run date and the command,
regenerate, and check that the prose around the table still says something
true. The generator will refuse the file if any suite's three numbers do not
add up.

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

# QT3 equivalents. Without GOXSLT_QT3 these fall back to testdata/qt3tests and
# run the whole suite; GOXSLT_NO_SUITES=1 is what skips them.
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

# Every configurable limit at its boundaries, across all seven packages
go test ./xdm/ ./xsd/ ./xpath/ ./xslt/ ./relaxng/ ./dtd/ ./c14n/ \
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

### The race lane samples volume

The full-streaming work's stream-vs-tree differentials took the xslt package
to 1312 s under `-race` on ubuntu CI, and past the 25-minute deadline on
Windows. Under `-race` (`raceEnabled`, xslt/race_on_test.go):

- the `Test*MatchesTree` differentials check one case in `raceStride` (4),
  picked by `raceSkip` from a hash of the case's own values, so every race run
  checks the same cases;
- `TestStreamFuzz`, `TestStreamFuzzMain` and `TestStreamOutFuzz` run seeds
  1–100 instead of 1–1000;
- the live-heap tests (`TestStream*Memory`, `TestStreamCopyAccumulatorsReleased`,
  the heap half of `TestStreamMainLeafTopUnderOutput`) and `TestLiveNodesBuilt`
  skip; their bounds were already off under `-race`, or they only build
  millions of nodes on one goroutine.

The streaming paths these walk run on one goroutine, so the race detector gains
nothing from the extra cases. The plain `go test` step still runs every case.
The concurrency tests (Tee, fork, concurrent compile and transform, shared
caches) run in full under `-race`. Locally (12-core arm64) the package went from
1081 s to 404 s under `-race`. Most of what is left is the budget tests that
main already had, about 219 s of main's run.

`op:same-key-023` was the case that proved the rule from the other side: it
builds 75³ = 421,875 keys and calls `map:put` and `map:remove` once for each,
and while `MapItem` was an entries slice plus a rebuilt index both were O(n),
so the case was quadratic and finished in no deadline at all. It was recorded
as a performance defect rather than raised past, and fixing the defect — a
persistent hash array mapped trie — is what made it pass.

A cost test counts work; it never compares against the clock. Fixed
wall-clock budgets failed on loaded CI runners (an `xsd` chain load took
2.7 s against 2 s on Windows, where it takes well under a second idle), and a
budget loose enough to survive load is too loose to catch a regression. Each
cost test instead asserts one of three measures, and each was shown to fail
with the guarded mechanism disabled:

- a work counter: `TestOptimizeNotQuadratic` counts the optimiser's uncached
  predicate walks, `TestNamespaceScopeNotQuadratic` the namespace bindings
  `c14n` examines, `TestGroupDAGLoadsInGraphTime` the groups each walk expands;
- bytes allocated (`runtime.MemStats.TotalAlloc`), which does not vary with
  load: the `relaxng` pattern-size tests, the `xquery` scanner and depth tests,
  `TestSubsumeLargeOccursTerminates`, `TestAdversarialShapesStayBounded`;
- the growth exponent of allocation between n and 2n, for costs that are
  linear by design: `TestBaseCycleCheckLinearInChainDepth`,
  `TestFacetMergeLinearInChainDepth`.

`TestValidateContextCancels` uses a context that expires on its Nth poll rather
than on a timer, so where cancellation lands no longer depends on speed.

---

## Fuzzing

Twelve targets, using Go's native `testing.F` and no framework:

| target | package | asserts |
|---|---|---|
| `FuzzParseNoPanic` | `xdm` | `ParseString` never panics; a refusal is an error and never a tree beside it; an accepted tree walks with its parent links intact |
| `FuzzParseDOCTYPE` | `xdm` | the DTD subset parser never panics on a malformed `<!DOCTYPE>` |
| `FuzzLoadSchemaNoPanic` | `xsd` | `Load` never panics at either XSD version, and every content model it accepts compiles to an automaton that answers total |
| `FuzzSchemaComplexity` | `xsd` | the complexity limits refuse a pathological schema rather than running unbounded: no load of a small schema allocates more than 8 MB |
| `FuzzSerializeRoundTrip` | `xslt` | parse → serialise → parse yields the same document, compared on expanded names, kinds and string values |
| `FuzzCompileStylesheetNoPanic` | `xslt` | `Compile` never panics and never returns a stylesheet beside an error |
| `FuzzCompileNoPanic` | `xpath` | the expression compiler never panics, and every parse error carries a spec code |
| `FuzzParseCompactNoPanic` | `relaxng` | the compact-syntax parser never panics |
| `FuzzRawTokenNoPanic` | `internal/xmltok` | the tokeniser never panics and terminates on any byte string |
| `FuzzCanonicalizeNoPanic` | `c14n` | parse then canonicalize never panics or hangs; a parsed document is refused only for depth, a relative namespace URI or XML 1.1; inclusive canonical forms re-canonicalize unchanged; `FromXPathFilter(doc, "true()")` reproduces the whole document exactly, and filters that split namespace axes canonicalize |
| `FuzzCompileNoPanic` | `xquery` | `xquery.Compile` never panics, and no compile of a 400-byte input allocates more than 64 MiB |
| `FuzzConstructorDepthIsBounded` | `xquery` | a query nested past the constructor-depth bound is refused, and one inside it compiles |

Most targets live in `zz_fuzz_test.go` in the package they exercise; the `zz_`
prefix is only to sort it last. Four sit beside the code they cover instead,
in `internal/xmltok/xmltok_test.go`, `relaxng/compact_fuzz_test.go`,
`xsd/complexity_fuzz_test.go` and `c14n/fuzz_test.go`.

The nightly workflow runs ten of the twelve. The two `xquery` targets are left
out on purpose: `FuzzCompileNoPanic` rediscovers a known, still-open fault
within seconds: a range such as `0 to 700000` folded at compile time into
213 MB (its doc comment names it; the two prolog-scanner faults it found
beside it were fixed on 2026-09-13, see [security.md](security.md)), so a
nightly run would fail every night on a bug already recorded. Run them by hand while working on `xquery`.

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
ordinary test run, so a large corpus is a tax on every build. The ten targets
together add well under a second.

**The search itself runs nightly, not on every push.**
`.github/workflows/fuzz.yml` runs all ten at `-fuzztime 300s`, one per matrix
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

## Canonical XML (`c14n`)

The tests are in two places. `c14n/` holds the unit, adversarial, fuzz and
benchmark tests, whose inputs are written inline. `tests/c14n/` holds the
conformance harnesses — the Recommendations' worked examples, the `xmllint`
differential, the W3C interop cases and the Merlin signatures — with the corpus they read in
`tests/c14n/testdata/`, beside the other suite harnesses rather than inside
the library package. The interop cases are a small download, which
`tests/fetch-c14n.sh` fetches into `testdata/c14n/`:

```sh
tests/fetch-c14n.sh               # once; a no-op when the files are present
go test ./c14n/ ./tests/c14n/ -count=1
```

**The W3C interop cases.** `TestW3CC14N11Interop` runs the 20 cases the XML
Security Working Group used for the C14N 1.1 interop round: an input
document, an XPath filter, C14N 1.1 and a SHA-1 digest. The expected results
are the five implementations' own — each case's digest is checked against the
`DigestValue` IAIK, IBM, Oracle, Sun and UPC each signed, and the octets
against the canonical form IAIK published. They are third-party material, so
like the other W3C suites they are fetched, not committed; the files, their
URLs and a SHA-256 per file are pinned in `tests/c14n-corpus.txt`, and a file
that does not match its digest fails the fetch. The test skips when the files
are absent; `GOXML_C14N_W3C=1` makes absence a failure, and `tests/check.sh`
(which runs the fetch itself) and CI both set it. The same script fetches the
three Recommendations into `testdata/c14n/specs/` for reference.

**The Merlin interop signatures.** `TestMerlinC14NThree` and
`TestMerlinExcC14NOne` follow every `ds:Reference` of two Baltimore signatures
from the XML-DSig interop round (Merlin Hughes, 2002) through its transforms
and check the canonical octets against the `c14n-N.txt` Baltimore published
and their SHA-1 against the signed `DigestValue`: 27 references in
`merlin-c14n-three`, whose XPath filters keep some of an element's namespace
nodes and drop others, and 4 in `merlin-exc-c14n-one`. Baltimore's
implementation is independent of this package and of libxml2. The files are
small and vendored verbatim, with a `PROVENANCE` file each, under
`tests/c14n/testdata/merlin-*`; they never skip.

**Golden files.** Canonical outputs are compared byte for byte against files
in `tests/c14n/testdata/`, which is tracked (unlike the top-level `testdata/`) and
marked `-text` in `.gitattributes` so a Windows checkout cannot rewrite their
line endings. The golden files are regenerated **only from `xmllint`**, never
from this package's own output — a golden file written by the code under test
checks nothing:

```sh
go test ./tests/c14n/ -update -count=1   # needs xmllint
```

**The `xmllint` differential.** When `xmllint` is on the `PATH`, whole-document
cases are also canonicalized live by `xmllint --c14n`, `--exc-c14n` and
`--c14n11` (libxml2, the canonicalizer `xmlsec1` is built on) and compared.
Without it the test skips, which on a developer machine is the right default
and in CI is the wrong one — so `GOXML_C14N_XMLLINT=1` turns a missing
`xmllint` into a failure, and CI sets it. `xmllint` cannot take a node set, so
for subsets the second implementation is the W3C interop round above — twenty
cases, five implementations — and beyond those the Recommendations' own
examples; [c14n.md](c14n.md#verification-still-to-do) lists what
that leaves open.

**The `xmlsec1` differential.** `TestC14NDifferentialXMLSec1` takes node sets
to `xmlsec1`, which has no canonicalize command but digests a node set for
every signature reference: each comparison is a `ds:Reference` in an enveloped
signature template, and `xmlsec1 --sign --store-references` prints the octets
it digests, which are checked against the digest it printed and then compared
byte for byte with this package's output for the same node set. It covers the
`xmllint` corpus under all six algorithms, the enveloped-signature transform
(`ExcludeSubtree`, including two documents in `tests/c14n/testdata/xmlsec1/`
with the signature mid-document), XPath filter transforms over the
Recommendations' subset examples and the W3C interop inputs, five inputs
whose filters split namespace axes (`FromXPathFilter`, a `NamespaceSet`), and
Exclusive C14N with an `InclusiveNamespaces` PrefixList (every declared
prefix, and a prefix bound nowhere alone, after `#default` and after one
declared prefix; naming it must not change this package's output). It skips without `xmlsec1` on
the `PATH`; `GOXML_C14N_XMLSEC1=1` makes that a failure, and CI sets it on
Linux, where it installs `xmlsec1` from apt. Without a local `xmlsec1`, a
script runs it in a Debian container and needs only Docker:

```sh
tests/c14n-xmlsec1.sh         # the differential
tests/c14n-xmlsec1.sh bench   # and the throughput comparison (about a minute)
```

Where `xmlsec1` is known to differ, the case is listed in
`xmlsec1Differences` in `tests/c14n/xmlsec1_test.go` with its reason, and
[c14n.md](c14n.md#where-xmlsec1-differs) states each one. An entry derives the
exact octets `xmlsec1` produces for each reference, algorithm and PrefixList
included (or names the error it stops with), so it
fails both when `xmlsec1` changes and when it starts agreeing. Any other
difference is a failure. An entry needs a reason grounded in a Recommendation;
one is never added to make the test pass.

**Fuzzing.** `FuzzCanonicalizeNoPanic` parses its input and canonicalizes
what parses, asserting no panic and no hang; it runs nightly with the
other targets ([Fuzzing](#fuzzing)):

```sh
GOXSLT_NO_SUITES=1 go test ./c14n/ -run '^$' -fuzz FuzzCanonicalizeNoPanic -fuzztime 120s
```

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

**A checkout path may contain a space.** The two stylesheet corpora take their
confinement root and their input directory as separate arguments, and the
runner places the root in `"$@"` with `set --` and finds the inputs with
`find`, so neither is word-split. Expanding them unquoted, as the runner once
did, truncated `-allow-dir` at the first space and left the remainder as stray
inputs, and matched no files at all — both silently, since the corpus then
reported "matched no inputs" and skipped. Only the remaining flags are
word-split, and those are the literal switches written at the call site.

**Each failing corpus file is named.** Below the count, the gate log prints
one line per failure, `  XSpec failed: <file>: <first line of its error>`. A
file counts as failed if the CLI exits non-zero *or* writes anything to
stderr. Diffing two logs then shows which file moved; that is how a 224 in a
git worktree, whose `testdata` is a symlink, was traced to
`issue-987_parent.xspec` and the symlink artifact described above.

**Skipped is not failed.** The suites skip cases by declared dependency — a
specific Unicode version, a spec version not being measured. (Streaming used to
head that list and no longer does: it was measured and found implemented.) The
XSLT 3.0 suite has 14,601 cases and 11,522 in scope; counting the difference as
failures would understate the engine, and counting it as passes would overstate
it. Both figures are reported separately for that reason.

**But not every skip is the same kind of skip**, and one number for all of them
hid the difference. Each reason string in `tests/xslts/deps.go` now carries a
class, and the suite summary prints both: `skipped N: out of scope A,
unimplemented B`. *Out of scope* is what the suite itself says a conforming
processor may leave out — the wrong `<spec>` version, a construct that only
exists in the version not being measured, a Unicode version, an optional
numbering language or calendar, a document only the network can supply, a case
that needs a feature this engine *has* to be absent. *Unimplemented* is the rest:
a gap wearing a skip label. At the last run that is **2,951 out of scope and 128
unimplemented** of the 3,079 XSLT 3.0 skips, and **8,332 / 66** of the 8,398 at
the 2.0 target. The XSLT 3.0 unimplemented 128 are: `disabling_output_escaping`
(33), `xsl-stylesheet-processing-instruction` (18), `enable_assertions` (24),
`package_version_resolution` (12), `additional_normalization_form` (12),
`streaming-fallback` (7), `maximum_number_of_decimal_digits` (7),
`supported_calendars_in_date_formatting_functions` (4),
`unparsed_text_encoding` (3), and one or two each of `default_output_encoding`,
`ignore_doc_failure`, `default_language_for_numbering`,
`detect_accumulator_cycles`, `extension-function` and
`recognize_id_as_uri_fragment`. The first two are features this engine does not
implement; the rest are dependencies the harness does not model, which is the
same admission from the other side — it cannot run the case under the conditions
the case asked for.

Folding those 134 into the denominator as failures is a defensible alternative
reading, and it gives 11,499 of 11,652, or 98.69%. It is **not** the figure this
repository publishes, for the same reason the other skips are not: an
unmodelled dependency is not a measured disagreement with the specification, and
scoring it as one would put a number on cases that were never run. The published
pass rate stays over the in-scope denominator, and the split is published beside
it so a reader can do the other arithmetic deliberately rather than be handed it
silently. A reason string with no class fails
`TestSkipReasonsAreClassified`, and the two counts failing to sum to the skipped
total fails the suite test itself.

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

**A fixed corpus is exhaustive over what it enumerates, and blind to the rest.**
`xdm/samekey_oracle_test.go` compares every ordered pair and triple of a
hand-enumerated corpus, which is its strength — randomising over those same
values would add nothing. What it cannot do is reach a value class nobody
listed. `xdm/samekey_property_test.go` is the generated lane for exactly that
gap: negative zero, the two binary types, large exact integers, timezone
boundary crossings, decimal/float/double aliases, duration aliases, QName
namespace differences, and malformed internal representations. It reuses the
`SameKey` oracle rather than writing a second one — a second oracle is a second
chance to make the same mistake — and asserts reflexivity, symmetry,
transitivity, agreement with the encoding in both directions, and the map
behaviour the key exists for: insert, look up, replace, remove.

**Determinism means a failure is reproducible.** The seed is a fixed constant
so CI runs the same values every time, `GOXSLT_SAMEKEY_SEED` overrides it to
widen the search, and the seed is printed on **every** failure message rather
than once at the top of the log, so a truncated log still carries the one fact
needed to re-run the failing input.

**Generate the equal values in different spellings, or the lane cannot see a
split.** Three generators here had to be rewritten after sabotage showed them
green. Sampling a spelling at random meant each octet sequence reached exactly
one spelling over a class's draws, so no `0f`/`0F` pair existed and a
case-sensitive key went undetected; the same mistake in the duration class meant
`P1Y` and `P12M` never appeared together. A class that must catch a *split*
needs the equal-but-differently-spelled pair to exist **by construction** — the
binary and duration classes now enumerate spellings from a counter rather than
sampling them. A class whose every value is one key (the signed zeros) can only
witness a merge, which is why it also emits values that must *not* collide.

**Check that the sabotage is live, not merely that the source changed.** Two
sabotage attempts here proved nothing: removing `strings.ToLower` from the hex
branch is a *no-op*, because Go's `hex.DecodeString` accepts both cases anyway,
and deleting the branch outright orphaned an import and failed to build. Both
looked like edits. A mutation must be shown to change *behaviour* — the live
version keys on the raw spelling while still decoding, so the import stays used
and `0f` and `0F` genuinely split.

**Say what the case is, and why the answer is what it is.** The tests here
name the W3C case that motivated them and quote the rule being applied, because
a bare assertion is unmaintainable: the next person cannot tell a deliberate
behaviour from an accident of implementation. Where the spec and the suite
disagree, Saxon's published report at
`testdata/xslt30-test/report/submission/Saxon_9.8.xml` is the tiebreak.

---

## What CI runs

Three jobs, in [`.github/workflows/ci.yml`](../.github/workflows/ci.yml).

**`test`** — gofmt, vet, build, unit tests, and the race detector, with
`GOXSLT_NO_SUITES=1` throughout. About a minute; catches a broken commit fast.
gofmt is enforced rather than advisory. On Linux it also vets the tree,
tests included, cross-compiled for `linux/386`, `linux/arm` and
`windows/386`: no runner is 32-bit, and a 32-bit `int` once stopped the
library compiling at all. `tests/check.sh` runs the same as its `vet32` lane.

**`vulncheck`** — `govulncheck` over go-xml and `w3cschemas`, on the newest
Go rather than 1.25. Standard-library fixes ship in Go releases, so on an old
toolchain it would only report the toolchain's age; on the newest it reports
go-xml's own dependencies, as it did for `golang.org/x/text` v0.36.0
(GO-2026-5970).

It runs on **`ubuntu-latest`, `windows-latest` and `macos-latest`**. This
library resolves schema and DTD references by path, and path separators,
symlink support, case sensitivity and the confinement rules `os.Root` enforces
all differ on Windows — so a cross-OS matrix is the only thing that
demonstrates the file handling actually works everywhere, rather than working
on the one platform anybody ran it on. `fail-fast` is off: one platform's
failure must not hide what the other two report.

The unit-test step sets `GOXML_C14N_XMLLINT=1` on Linux and macOS, so the
`c14n` differential against `xmllint` fails rather than skips when the tool is
missing; Linux installs it from `libxml2-utils`, macOS ships it. Windows is not
required to have it, but GitHub's Windows runner does carry an `xmllint` on the
`PATH`, so the differential runs there too. That `xmllint` writes its output in
text mode, turning every LF into CR-LF; a canonical form never holds a literal
CR, so the tests undo that translation before comparing. Before the
tests, a step runs `tests/fetch-c14n.sh` on all three platforms, and
`GOXML_C14N_W3C=1` makes the W3C interop cases fail rather than skip if the
fetch left them absent — see [Canonical XML](#canonical-xml-c14n).

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
`last-run`, on success and on failure alike — see [Provenance](#provenance) —
and `tests/release-record.txt` as `release-record-<ref>`, which is what a
release points at; see [The release record](#the-release-record).

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

It is **not** written to `tests/ratchet.txt`. `GOXSLT_RATCHET=update` rewrites
that file in place — `grep -v` the line, append the new one, `sort` — so
anything else living there would be destroyed by the first count recorded.

Provenance also records the **dependency graph**: `go list -m all`, as a digest
and its expansion, plus a digest of `go.sum`. A release claim that names a Go
version but not what the build linked against is answerable for only half of
what produced the figures.

---

## The release record

Provenance says *what was measured*. The release record says *what the
measurement found*, and it is the single artifact a release points at.

The pieces it collects were all present and all separate: `vet` at its section,
the full package run at its own, `race` at its own, each W3C driver printing
its own summary, and provenance in `tests/last-run.txt`. Assembling a release
claim meant scraping a transcript for nine figures and then arguing that the
transcript belonged to the commit — the same "measured against what?" that
provenance exists to answer, one level up.

So `check.sh` writes `tests/release-record.txt`: the provenance block, then
every lane with its verdict and the figure that lane's own driver produced,
then one verdict line.

```
lanes
build                  PASS go build ./...
vet                    PASS go vet ./...
documented figures     PASS tests/docfigures.sh and the documented grep commands
generated figures      PASS go run ./tests/conformance-docs.go -check
package tests          PASS GOXSLT_NO_SUITES=1 go test ./... -count=1
race                   PASS GOXSLT_NO_SUITES=1 go test -race ./... -count=1 -timeout 25m
w3cschemas             PASS build, vet and test of the separate module
vendored schemas       PASS vendored schemas: 185 loaded, 38 failed, 7 excluded (of 230)
W3C QT3 XPath 2.0      SKIP suite absent at /nonexistent-qt3
W3C QT3 XPath 3.0      SKIP suite absent at /nonexistent-qt3
W3C QT3 XPath 3.1      SKIP suite absent at /nonexistent-qt3
RELAX NG spectest      PASS spectest_test.go:116: RELAX NG spectest: 965 assertions, 965 passed, 0 failed (100.00%)
UBL                    SKIP not set; licensed corpus, cannot be cloned in CI (expected)
DocBook                SKIP stylesheet absent; not fetched in CI (expected)

verdict      VERIFIED WITH GAPS — every lane that ran passed, 10 did not run
```

**The third column is the point.** A record listing only the lanes that ran is
a record that lies by omission: the `conformance` job has corpora the `test`
job does not, and DocBook, XSpec, UBL and CII are deliberately absent from CI.
"All suites pass" over a run where four never started is indistinguishable from
one where they did — so every lane declares itself `PASS`, `FAIL` or `SKIP`, a
skipped lane names *why*, and the verdict counts all three. A run with skips is
`VERIFIED WITH GAPS`, spelled out rather than left for a reader to notice that
a suite is missing from a list.

Nothing in the file is typed. Every figure is the string the lane extracted
from its own driver's output; a number written in by hand would be exactly the
defect the generated-figures work removed — correct on the day it was pasted
and unfalsifiable afterwards.

It is written from **both** exits, the `fast` return and the end of a full run.
A fast run's record is a legitimate record; it simply is not a release one, and
what distinguishes them is that its external lanes are all present and all say
`SKIP`.

**`tests/release-record.txt` is gitignored**, for the reason `last-run.txt` is.
It changes on every run, so committing it would put a diff in the tree every
time anyone ran the gate, and a committed copy would prove only what the last
committer happened to run. A release does not point at a file in the working
tree: it points at the artifact the tagged CI run uploaded, named
`release-record-<ref>`. Committing one would be provenance for the commit,
which is what the tag already is. The artifact is uploaded on every run, not
only on release day, so the mechanism is never first exercised when it matters.

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

* **The `require` moved off v1.1.0** — to v1.2.1 at the time; the current pin
  is whatever `w3cschemas/go.mod` names. v1.1.0 and v1.2.0 both declare
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
  nothing runs it at all. `check.sh` passes `GOXSLT_XSLTS` to it when the XSLT
  suite is present, so `TestLoadsSchemaForXSLT30WithoutTheNetwork` — the test
  of the module's reason to exist — runs there instead of skipping, including
  in the CI job that clones the suites and runs `check.sh`. The fast job's own
  w3cschemas step has no suite checkout and still skips it.

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
that is a release step — now an automated one: `.github/workflows/release.yml`
makes the bump, tests it and tags the module, when the release tag's message
asks for it. See [RELEASE.md](../RELEASE.md), which also records why the
w3cschemas version cannot be derived from this one.

The general shape of this: *a module that is in no gate cannot regress, and a
module pinned to a release is not testing your working tree even when it is.*

## The Go version is a conformance dependency

The module requires **Go 1.25**, and that is a measured floor rather than a
tidy default. `regexp` learned the Unicode category `Cn` (unassigned) in 1.25;
on 1.24 the pattern `^(?:\p{Cn}*)$` fails to compile, and `re00175` raises
FORX0002 where it should match. The cost of building on 1.24 is four cases:
XPath 3.0 and 3.1 fall off 100%, XQuery loses one, and XSD 1.0 loses two.
Independently of that, `golang.org/x/text` v0.35 and later declare
`go 1.25.0`, so the pinned dependency sets the same floor.

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

## The function-signature migration

`xpath/spec/function-signatures.json` is the normalized extraction of the F&O
3.1 function proformas — 272 `(name, arity)` entries, written by
`cmd/genfunctions` from the vendored Recommendation, so the data source is
offline and deterministic. `xpath/funcspec_table.go` holds `specSignatures`,
the parsed form of that data, and a family is migrated by adding its keys
there: nothing else changes, because a declared type constrains an existing
registration rather than replacing it.

`specSignatures` has **two consumers**, and both read the one table:

* **Call binding**, through `lookupSpecParams`. A parameter F&O declares
  without `?` refuses an empty sequence at the call, as `XPTY0004`, rather
  than in a hand-written guard inside the function body.
* **Function items**, through `applyBuiltinSignatures`, which copies each
  entry onto the registered `Function.Signature` so a named function
  reference carries it. That is what a typed function test —
  `fn:concat#2 instance of function(xs:date, xs:date) as xs:integer` — is
  judged against, by `functionItemMatches` in `xpath/subtype.go`.

The second consumer is the later of the two. Function items originally read a
separate seventeen-entry table of their own, so migrating a family for call
binding left the function item unannotated, and `functionItemMatches` fell
back to matching on **arity alone** — which made the `instance of` above
answer `true`, since `fn:concat#2` and the test both take two arguments. That
is an observably wrong answer rather than a missing diagnostic. The seventeen
entries now live in `specSignatures` verbatim, and there is one table rather
than two that could disagree.

Two functions in a manifest namespace carry no manifest row, for reasons the
enforcement tests hold them to:

* `fn:concat` is **variadic** — its proforma ends in a literal `...`, so no
  single row can describe it, and `cmd/genfunctions` excludes it.
  `applyVariadicSignatures` writes its type out per arity instead: every
  parameter is `xs:anyAtomicType?` and the result is `xs:string`, which is
  fully determined even though its arity is not.
* `fn:stream-available` is an **XSLT 3.0 extension**, which F&O does not
  define at all. It is in `extensionAllowlist` with that reason.

Because a signature is carried as a *spelling* and compared through
`SequenceType.String()`, that rendering has to be lossless for any type a
signature can name. Three item types rendered as a bare `item()` until
function items began reading the manifest: `xs:numeric`, which has no type
code of its own; an array test; and a typed function test. While every
function was judged on arity that cost nothing, and it became four wrong QT3
answers the moment signatures were consulted.
`TestSequenceTypeSpellingIsLosslessForSubtyping` pins the renderings, and
`TestFunctionSubtypingAcrossItemKinds` pins the §2.5.6.2 relations that let a
function test be wider than a map or an array.

A key is `"local/arity"` for an `fn:` function and `"prefix:local/arity"` —
`"math:pow/2"`, `"map:get/2"`, `"array:size/1"` — for the other three
namespaces the manifest covers. `splitSpecEntryKey` reads both forms, and
`TestMigratedSignaturesMatchManifest` reads keys through that same function,
so the table and the test cannot disagree about what a key means.

Three tests measure it, and they ask different questions:

* `TestRegisteredFunctionsHaveManifestMetadata` — does the specification
  describe every registered function? This already enforces; a new callback
  with no proforma fails here unless it is an allowlisted host extension.
* `TestCallBindingMigrationInventory` — does the cardinality check actually
  *run* for it? This reports rather than fails, and prints the migrated count.
  It is the number a family agent works down; flip its `report := t.Logf` to
  `t.Errorf` when it reaches zero.
* `TestMigratedSignaturesMatchManifest` — does what has been migrated agree
  with the Recommendation? This one fails, because it does not depend on how
  far the migration has got. A mistyped `?` is precisely the defect the
  mechanism exists to prevent, so a hand-edited spelling that disagrees with
  the manifest breaks the build.
* `TestEveryStandardBuiltinHasAManifestSignature` — does the *function item*
  consumer reach every standard function? A function registered in one of the
  four manifest namespaces with no signature and no `extensionAllowlist`
  entry fails here. Without it a hole is invisible from outside, because the
  arity-only answer it produces is a plausible one.
* `TestFunctionItemSignaturesMatchSpecSignatures` — do the two consumers read
  the same table? It walks every `specSignatures` entry and checks the
  registered function carries exactly those spellings, so a family migrated
  for call binding can never again reach function items unannotated.

The migration is complete at 272 of 272: the seventeen that function items
carried before the table was unified, `fn:substring` and `fn:subsequence`, then the numeric (14), non-regex string
(25), temporal (28), node and accessor (34), sequence (14), higher-order (12),
QName and URI (15), input and document (20), JSON (10), context, boolean and
error (14), `math:` (14), `map:` (11), `array:` (21), regex (9) and
formatting (10) families. With `pending` at zero,
`TestCallBindingMigrationInventory` is ready for its `report := t.Logf` to be
flipped to `t.Errorf`; that flip is a change of enforcement mode and is left
as its own commit.

The regex family was deferred twice on the belief that declaring `$pattern`
and `$flags` would change which error code an empty sequence raises -- that
call binding would answer `XPTY0004` where the hand guard answers `FORX0002`.
It does not. `FORX0002` is for a *malformed* pattern, a question reached only
once a pattern exists; an empty sequence in a non-nullable position is a
*cardinality* refusal. The three QT3 cases that pass `()` in a pattern
position -- `K-MatchesFunc-1`, `K-ReplaceFunc-2`, `K-TokenizeFunc-2` -- all
expect `XPTY0004`, as does `K-MatchesFunc-3` for `()` in `$flags`. The
wording moves, because the refusal now comes from call binding rather than
from `argFlags`; the code does not.

The formatting family -- `fn:format-date`, `fn:format-dateTime`,
`fn:format-time`, `fn:format-integer` and `fn:format-number` -- was deferred
on the matching belief about `FOFD1340`, and that premise fails for a stronger
reason: `FOFD1340` is for an invalid *picture*, and no QT3 case passes `()` in
a picture position at all. The cases that resemble it pass `()` in the first
argument, which every proforma declares `?`. `$picture` is the only
non-nullable parameter in the family; on the arity-5 forms `$language`,
`$calendar` and `$place` are all `xs:string?`.

`math:pi` was the first entry to use a prefixed key, and it landed with the
mechanism rather than with its family. It is nullary, so it constrains no
argument and can change no behaviour, which is exactly what makes it the entry
that proves the prefixed path is live rather than dead code: under the old
fn:-only expansion its key constrained a non-existent `fn:pi`.
`TestPrefixedSpecKeysNameOtherNamespaces` is what keeps that path from
decaying back.

Every row the prefixed key format made reachable has now been migrated, and
the regex and formatting families that were twice deferred have followed;
nothing remains.

The `math:`, `map:` and `array:` families differ from the `fn:` ones in what
migrating them buys. Every parameter of those 46 is already guarded by hand
inside `fn_math.go`, `fn_map.go` and `fn_array.go` — `argMap`, `argArray` and
their neighbours raise XPTY0004 for an empty sequence and for several items
alike — so a declared type there re-derives a refusal the callback already
produces rather than adding a new one. The gain is that the refusal becomes a
property of the manifest, where a mistyped occurrence indicator fails
`TestMigratedSignaturesMatchManifest` instead of quietly constraining a
function wrongly, and where the hand guard and the declared type are checked
against each other by the QT3 lanes.

What does change for these three is the WORDING of the refusal, not the code
or the result. The declared type is consulted at call binding, before the
callback runs, so `map:get((), 1)` now reads "an empty sequence is not allowed
as the first argument of map:get(), which is declared map(*)" where `argMap`
used to say "argument 1: expected a single map, got 0 items". The error code
is XPTY0004 either way and all four QT3 lanes are unmoved, which is what makes
this a message change rather than a behaviour one. `math:pow`'s message names
its declared type as `item()`, because this package parses the union spelling
`xs:numeric` to `item()`; the cardinality it carries — exactly one — is the
one F&O declares, which is all the check reads. The same is true of
`array(*)` and of the `function(...) as T` proformas, so `array:size(())`
names its declared type as `item()` too. Only the occurrence indicator is
load-bearing here: `checkArgCardinality` reads `AllowsEmpty` and `AllowsMany`
and nothing else, which is why a spelling this package parses loosely still
constrains the right cardinality.

The last 19 — the regex and formatting families — were twice held back on the
belief that they needed a new lane asserting on error codes, because both
groups looked like they would change *which* error is raised rather than only
whether one is. They do not, and the lane was never needed: the QT3 driver
already compares error codes (`tests/qt3/runner.go` reads `xdm.ErrorCode` and
`sameErrorCode`), so the suite *is* that lane. What settled it was reading the
cases rather than reasoning about the codes — only four in the whole suite pass
`()` in a non-nullable position, and every one of them expects `XPTY0004`.

Migrate in family-sized commits, and run the full QT3 lanes after each one —
the four in-scope counts are the check, and any drop is a regression rather
than a newly-revealed bug:

```sh
GOXSLT_QT3=$PWD/testdata/qt3tests go test ./tests/qt3/ -count=1 -run '^TestQT3$' -v
GOXSLT_QT3=$PWD/testdata/qt3tests go test ./tests/qt3/ -count=1 -run TestQT3XQuery -v
```

Omitting `GOXSLT_QT3` makes the lane skip and still print `ok`, so assert the
in-scope counts are non-zero before believing a result. The anchor on the
first pattern matters: unanchored, `-run TestQT3` also selects
`TestQT3XQuery`, so the XPath command runs the XQuery suite as well and the
last `in-scope:` line in its output is XQuery's, not XPath 3.1's.

A caution that is specific to this work: if a case changes result after a
signature is added, establish whether the behaviour is version-gated before
treating it as a defect. `lookupFor` in `xpath/version.go` hides a function
whose `Since` exceeds the context's version, so a function legitimately absent
or differently typed at an earlier F&O version is a configuration posture, not
a bug. The manifest carries no `since` field; it describes F&O 3.1 alone.

## Related

* [conformance-gaps.md](conformance-gaps.md) — the current figures and a
  case-by-case verdict on every failure.
* [known-gaps.md](known-gaps.md) — why the hard gaps are hard, including fixes
  that were attempted, measured and reverted.
* [reaching-100.md](reaching-100.md) — what the remaining distance consists of
  and which parts are worth buying.

## Regenerating the PGO profile

`tests/pgo.sh` rebuilds `cmd/go-xml/default.pgo` from CPU profiles of the CLI
on every benchmark workload family, weighted so none dominates. It needs the
untracked corpora under `testdata/`, including `testdata/bench`, and takes
about two minutes. A stale profile costs only speed, never correctness, so
regenerate it after a change moves the hot paths, not on every commit.
