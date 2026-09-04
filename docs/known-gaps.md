# Known gaps

Why the hard gaps are hard. For the current figures and a case-by-case verdict
on what is fixable, see [conformance-gaps.md](conformance-gaps.md); this file
is the diagnosis behind the entries there. Nothing here is aspirational: if a
gap has no entry, it has not been measured.

Three categories run through the list:

- **Won't fix** — the behaviour is deliberate, and the test disagrees with a
  choice made on purpose. Changing it would be a regression in something that
  matters more.
- **Needs an engine change** — the cause is understood and the fix is real, but
  it is a rewrite of a component rather than a patch. Attempted patches are
  recorded so they are not retried.
- **Open** — a genuine bug or unimplemented rule with no work done yet.

Alongside those, and interleaved with them because they are the reasoning the
open verdicts rest on, are three kinds of record that outlive the work that
produced them: **constraints** a past fix turned out to need, and which any
future change in the same area must still satisfy; **retractions**, where the
reading recorded here was itself wrong; and **superseded measurements**, where
a probe answered the wrong question convincingly. Those last are kept
deliberately. A negative result that was believed for two revisions is more
dangerous than an open bug, and deleting it invites the same probe again.

A note on which direction matters. A **false reject** is valid input refused;
a **false accept** is invalid input allowed. False rejects are the more serious
kind — they break working documents — so they are listed first throughout.

## Where the numbers stand

Current conformance figures live in **[conformance-gaps.md](conformance-gaps.md)**,
which names every failing case and says whether it is fixable, and are re-measured
by `tests/check.sh`. They are deliberately not repeated here: this file explains
*why* the hard gaps are hard, and a percentage copied into two places drifts in one
of them.

What this file adds, and that one does not:

- the diagnosed cause behind a gap, rather than its error code;
- fixes that were attempted, measured and reverted — recorded so the obvious
  patch is not tried a second time;
- what a real fix would cost, where the answer is a rewrite rather than a patch;
- measurements that were made, believed, and later shown to have proved nothing;
- DTD and XDM, which have no public suite and so appear in no percentage.

## Won't fix

### DOCTYPE is refused by default

`IRI/iri-001` (schema), and any instance carrying a DOCTYPE.

A DOCTYPE is the entry point for XXE and entity-expansion attacks. Refusing it
unless the caller opts in is the correct default for a library that will be
pointed at untrusted input. `xdm.ParseOptions{AllowDOCTYPE: true}` enables it
where the documents are trusted — which is what loading UBL requires, because
the W3C XML Signature schema it depends on carries one.

### `xsi:schemaLocation` is ignored by default

Honouring it lets the document choose the schema it is validated against, which
defeats the purpose of validating. `WithInstanceLocations` opts in, with a
policy that names which namespaces may be resolved.

### `fn:collection()` raises an error rather than returning empty

`CTA/cta0022` (XSD), 7 cases in QT3 `fn-collection`.

`cta0022` wants `empty(collection())` to be true. Returning an empty sequence
for an unconfigured collection would let a stylesheet silently process no
documents and report success, which is worse than an error. `FODC0002` stands.

The 7 QT3 cases are a different matter and are listed under *Open* below: they
supply real documents through a `<collection>` environment, so they are a
capability gap rather than a disagreement. That gap is now closed, in the
engine and in the harness.

Note that `cta0022` is unaffected by the hook. With no resolver configured the
default is still `FODC0002`, which is the point.

### Unicode category drift (bug 4113)

18 cases in `MS-Regex2006-07-15` (`reJ11`, `reJ13`, `reJ19`, …), all flagged
`queried bug4113` by the W3C.

These assert that `\p{Lu}` rejects characters that *are* uppercase letters in
current Unicode. The suite was written against Unicode 3.1; the codepoints in
question — U+1D7A8 among them — were categorised differently then. Matching the
suite would mean shipping a frozen 2001 character database.

### `ibmMeta/wildcard.testSet` is mislabelled

4 cases in 1.0 (`s3_10_6v02s`, `s3_10_1ii08s`, `s3_10_1ii09s`, and one in
`anyAttribute`).

The set is tagged `version="1.0"`, but every group in it cites the 1.1 spec and
four use `notQName`, which is 1.1-only. Rejecting `notQName` under 1.0 is
correct; the tests are in the wrong bucket.

### `xs:gMonth` old lexical form (bug 6901)

`gMonth002_2061`, `gMonth004_2063`, flagged `queried bug6901`.

These use `--03--`, the withdrawn gMonth syntax from the original XSD 1.0
release. The errata replaced it with `--03`. Accepting both would mean
accepting a form no current spec defines.

---

## Needs an engine change

Each of these has a diagnosed cause and at least one attempted fix that was
measured and reverted. The attempts are recorded because the obvious patch is
wrong in a way that is not obvious.

### An optional all group is a disjunction, not a scaled budget

`MS-ModelGroups/mgO029` (schema, 1.1).

`allSubsumes` turns a base all group into a per-name occurrence budget and
ignores the *group's* own `minOccurs`, while the derived side folds its group's
range into its branch counts. That asymmetry rejects `mgO029`, whose base and
derived are spelled identically — both `<all minOccurs="0">` around a required
element — so a type is refused as an invalid restriction of itself.

Multiplying each budget by the base group's range fixes `mgO029` and breaks
`particlesK006`, whose own documentation states the distinction:

> B's minOccurs=0, B's maxOccurs=absent, but the element has min=max=1,
> R's minOccurs=0, R's maxOccurs=1 — expected **invalid**

`<all minOccurs="0">` around a required `a1` means *either the group is skipped
entirely, or a1 appears exactly once*. It does not mean `a1` is independently
optional. Scaling to `0..1` flattens a disjunction into a range and loses the
all-or-nothing coupling that `particlesK006` exists to catch. Net effect of the
attempt was zero: one false reject fixed, one false accept introduced.

Deciding both needs the base read as *(empty) | (every budget met)* — two
alternatives checked separately.

### Regular expression backreferences — two engines, one default

**The default engine is RE2 and stays RE2.** RE2 has no backreference, but it
does return capture positions, and a backreference is only *hard* when the
group it names can match more than one width. RE2 returns a single submatch
assignment — the greedy one — and cannot enumerate alternatives, so for
`(a*)\1` against `"aa"` it reports the group as `"aa"`, leaving nothing for the
backreference, and a comparison against that answers **false** where the
correct answer is true (the split is `"a"` + `"a"`). The information needed was
discarded before the comparison ran.

When every group a backreference names has a *fixed* width, the greedy
assignment is the only assignment. There is nothing to enumerate, so
capture-and-compare is not an approximation — it is exact, and it runs in RE2's
linear time with one comparison pass per candidate position. Measured on
`([a-z])\1*`: 4,000 characters in 53 µs, 64,000 in 567 µs. That path is
unconditional, and it is what the default uses.

A variable-width backreference is refused with `FORX0002` rather than guessed
at, because an engine that answers correctly or says it cannot is safe to have
on always, where one that guesses is not safe at any setting.

**The general case is available, and off by default.**
`xpath.SetBacktrackingRegex(true)` — or `-backtracking-regex` on the command
line — enables a backtracking matcher that decides the rest: variable-width
groups, backreferences mid-pattern, alternation, and lazy quantifiers.

It is off by default because it has no linear-time guarantee, and a pattern is
not always the caller's own: `matches($s, $node/@pattern)` takes one from
document data, so enabling it globally would let a document being validated
choose how long the validation takes. Catastrophic backtracking is a denial of
service with a one-line payload.

Even enabled, a step budget bounds every match, and exhausting it raises
`FORX0002` rather than returning a silent "no match" — a budget that guessed
would do it precisely on the inputs where the answer was hardest to get. The
budget is measured from both ends: the hardest honest pattern in either
conformance suite (`regex-032`, fifteen lazy groups and a `\14` against 180
characters) answers in 525 steps, five orders of magnitude below the ceiling,
while `(a*)*\1b` against sixty `a`s exhausts the full budget in about 200 ms.

Character-class semantics are not duplicated between the two engines. The
backtracking matcher parses the *already-translated* pattern and compiles each
single-character leaf with RE2, so subtraction, `\p{IsGreek}`, `\i`, `\c` and
the Unicode-wide reading of `\d` and `\w` are owned by `translatePattern` and
applied in exactly one place.

The XML Schema pattern facet is unaffected: Appendix F's `atom` production has
no form for a backreference, so `xsd` still rejects `\1` under both versions.

### A union's selected member is a third fact, beside the annotation

Recorded as an invariant rather than as a fix, because the mistake it describes
is available at every site that copies a node, and one of those sites is added
whenever a new copying instruction is.

`<xsl:template match="Date[data(.) instance of StandardDate]">` never matched,
where `StandardDate` is a named simple type brought in by `xsl:import-schema`.
The cause was neither of the two candidates an earlier revision of this entry
named. The element *did* carry its annotation after validation, and the type
name *did* resolve in the pattern's static context. What was lost was the third
fact, the one between them.

`Date` has type `DateType`, a complex type with simple content extending
`GeneralDate`, which is a union of `StandardDate` and `xs:string`. **XSD 1.0
§3.14.4 selects a union's member per *value*,** so the annotation alone cannot
say whether "29 MAY 1917" is a `StandardDate` or a plain string — the validator
records the winning member separately, in `xdm.Node.UnionMember`, and
atomisation reads it to decide what the typed value is. A union's own
derivation chain runs to `xs:anySimpleType` and stops, so **without the member
there is nothing to build a typed value from and the node atomises to
`xs:untypedAtomic`.** Any copy that carries `TypeAnnotation` and drops
`UnionMember` beside it therefore untypes the node silently.

Two things make this class of defect hard to see. The failure is **selective**:
the same pattern answers *true* on any path that has not been through a copy,
so `validation-0201` sorted its events by a key that saw the type and then
dispatched on a pattern that did not — and `xsl:strip-space`, a declaration
about whitespace with nothing to say about types, was what untyped the
document. And the inconsistency is what exposed it: `xdm/xinclude.go` already
copied both fields, which is what made the omission elsewhere read as an
oversight rather than a design.

It was diagnosed by measurement rather than by reading, which is the
transferable part: a probe over the validated tree showed `union="StandardDate"`
present on every `Date`, and a trace at the `instance of` match site showed the
annotated value arriving 1614 times from the sort key and an *unannotated* one
arriving twice, from `apply-templates` — the two calls that produce the output.
Reading the copy sites would not have narrowed it; counting arrivals did.

`validation-0201` still fails, on indent width alone — recorded as
implementation-defined in `docs/conformance-gaps.md` — so this costs and gains
no suite case, and `xslt/unionmember_test.go` is what pins it instead.

## Open

Real gaps, together with the constraints and retractions that bound how they
may be closed. Ordered by how much they cost.

### A hyphen after a variable reference is part of the name (XPath, XQuery) — not a defect, retracted

`$e-1` evaluates as a reference to a variable named `e-1` rather than as
`$e - 1`. This was recorded here as a defect, on the grounds that Saxon and
BaseX read it as subtraction. That was wrong, and the QT3 suite settles it
twice over.

**The suite writes such names itself and depends on them.**
`app/fo-spec-examples.xml` binds `let $tz-10 := xs:dayTimeDuration("-PT10H")`
and then passes `$tz-10` to `fn:adjust-dateTime-to-timezone` as a single
variable; `$in-xml-1` and `$in-xml-2` account for 106 further uses. If a hyphen
before a digit ended a name, every one of those cases would fail. They pass.

**And it states the trailing case outright.** `prod/NameTest.xml`'s
`K-NameTest-3` is `foo- foo`, described as "'foo-' is an invalid nametest.
Whitespace is wrong", expecting `XPST0003`. So a name absorbs a final hyphen
*even when whitespace follows*, and the result is a syntax error rather than
subtraction.

That second point was found the expensive way. An earlier reading of this held
that `$e- 1` should be subtraction because whitespace after the hyphen proves
the hyphen cannot continue the name — plausible, and wrong. Implemented as a
one-line lookahead in `lexNCName`, it broke `K-NameTest-3` in all four suites
at once: XPath 2.0 15,183 to 15,182, 3.0 19,244 to 19,243, 3.1 21,786 to
21,785, XQuery 29,800 to 29,799. Reverted. The counts alone would have shown
four losses with no gains; the case list showed it was one case, four times.

The rule is plain longest-match over `NameChar` with no lookahead, and this
engine implements it. `xpath/hyphen_test.go` pins the whole table so the
retraction is not re-litigated. Anyone wanting arithmetic writes a space:
`$e - 1`.

### Schema-validity rules not yet implemented (XSD)

195 false accepts in 1.0, 305 in 1.1 — invalid schemas this loads without
complaint. 25 of the 1.0 cases and 26 of the 1.1 cases are W3C-flagged. The
rest concentrate in:

| Cluster | 1.1 count | What is missing |
|---|---|---|
| `MS-Particles` | 46 | Particle-level Schema Component Constraints |
| `MS-Schema` | 44 | schema-document structural rules |
| `MS-SimpleType` | 21 | simple type derivation constraints |
| `MS-ComplexType` | 18 | complex type derivation constraints |
| `Wild` | 17 | 1.1 wildcard rules (`notQName`, `notNamespace`) |
| `Simple` | 16 | 1.1 simple type rules |
| `CTA` | 16 | conditional type assignment constraints |
| `Open` / `PopenContent` | 15 | open content and interleave |
| `Override` | 7 | `xs:override` semantics |

These are unwritten rules rather than broken ones: each rejects a schema that
should be rejected but currently loads. None of them affects a *valid* schema,
which is why the false-reject count is two orders of magnitude smaller.

Adding rules here is the highest-yield remaining work and also the riskiest: a
rule stricter than the spec starts rejecting real schemas the suite never
covers. Every change must be measured against both suite directions *and* the
production corpora (65 UBL + 427 CII), which is the only guard against
over-strictness.

### Restriction of an all group by a wildcard or a named group (XSD 1.1)

`All/all206`, `all218`, `all237`, `Wild/wild049`, `wild050` — 5 schema false
rejects.

XSD 1.1 permits derivations the 1.0 table calls Forbidden: a sequence or a
wildcard restricting an all group, and a named model group merged into one.
`allSubsumes` decides the case where every base particle is an element
declaration, and falls back to the 1.0 table otherwise — sound, but
conservative, so these five valid schemas are refused. Extending it to cover
wildcards means deciding how a wildcard's occurrences split between the names
it spans, which `all244` shows is not a simple count.

### Why the occurrence counters are a vector and not a bracket per scope

Four attempts to fix nested occurrence bounds are recorded in the history, each
of which traded one case for another. They are summarised here so a fifth is
not made along the same lines.

**The bug they were attacking.** A repeated group whose *only* child is itself
repeating was decided wrongly in both directions. For
`<sequence minOccurs="5" maxOccurs="5">` over `<element c minOccurs="2"
maxOccurs="2"/>` the only valid document is ten `c`, and it was **refused**;
five `c`, which no reading admits, was **accepted**. The false-accept direction
was the serious one: a `minOccurs` floor was silently not enforced.

**Why no suite saw it.** A group with two or more distinct child names was
decided correctly, which is why 39,347 XSD 1.0 agreements and 41,532 on 1.1
never covered it. It was found by differential fuzzing against a brute-force
reference and is invisible to both W3C suites — a standing reminder that suite
agreement is not coverage.

**Why the obvious fixes all failed.** `matchSequence` walked the automaton one
path at a time and arbitrated the nested counters with heuristics, tracking a
*low* and a *high* reading of each count independently. `counterAllows`
consulted the low count and `countersSatisfied` the high one, so a document was
admitted when *different* readings satisfied each bound though no single
consistent reading satisfied both. When a group holds one particle its FIRST
and LAST positions coincide, which makes the group's wraparound edge
indistinguishable from the inner element's own repeat edge, so the bracket
cannot be narrowed locally. **No per-edge compile-time label can resolve
this**, because the ambiguity is real: which scope repeats is only knowable
from the rest of the input. Every attempt that tried to label the edge
therefore had to trade one case for another.

**What the resolution requires.** The unit of tracking must be a *vector over
every scope at once* — a set of whole readings, not a bracket per scope — so
that counts inside one vector belong to one execution by construction and no
bound is ever met by a reading another bound is not measured against. Two
properties keep such a set small, and both are load-bearing: states agreeing on
position and counts are merged with a scope left behind reset to zero, so
converged executions are recognised as converged; and each maximum is narrowed
per document to what that document can actually reach. Without that narrowing
the suite's `particlesZ036` — a choice of 100,000 over a sequence of
100,000,000 over an unbounded element — gives each step three readings that
stay distinct forever and the set grows until the budget stops it.

**Only the counts are searched.** The walk stays deterministic on the
*positions*: Unique Particle Attribution guarantees at most one element
particle matches a name, and the one remaining ambiguity, an element against a
wildcard, is what erratum E1-29 leaves to the processor.

`DefaultMaxMatchStates` bounds the set at 4,096; see
[xsd.md](xsd.md#limits) and [security.md](security.md). Both W3C suites, UBL 2.1
and the DocBook corpus stay in single digits.

### The 254 cap in `encodeCounts` is not a bound on `maxOccurs`

The count vector is carried as a string of bytes, and `encodeCounts` caps each
count at 254. Read on its own that looks like a ceiling: an audit predicted
that `maxOccurs="300"` would accept a 301st child and that `minOccurs="300"`
would reject a valid 300-child document, since 255 and 256 both encode as 254.

Neither happens, and the reason is that a count never arrives at
`encodeCounts` un-narrowed. `reachable()` runs first and replaces every bound
above the document's own child count with `Unbounded` — a maximum a document
has too few children to reach cannot be broken, so it behaves exactly as
`unbounded` does. `capCount()` then clamps against that *narrowed* bound, and
once the maximum is out of reach it returns at most `min+1`. So a stored count
above 254 would require a scope with 255+ children still in play, and in that
scope the bound is already `Unbounded` and the exact value has stopped
deciding anything.

That is three functions' worth of reasoning to re-derive, which is why it is
pinned rather than argued: `xsd/occurs_boundary_test.go` walks `minOccurs` and
`maxOccurs` through 126/127/128, 253/254/255/256/257, 300, 1000 and
65535/65536, each at its bound and one either side, plus the nested-scope form
where the outer counter is the one that would saturate, and `maxOccurs` values
of 1,000,000 and 79228162514244337593543950335 which must behave as unbounded.

**A related reading, worth stating because it is not obvious.** A sweep of
2,028 combinations of outer bounds, inner bounds and child count once found 40
answers wrong, every one a false *rejection*, and every one with inner
`minOccurs="0"` and outer `minOccurs` of two or more at a small child count.

`<sequence minOccurs="2" maxOccurs="2">` over `<element c minOccurs="0"
maxOccurs="2"/>` is the witness, and its answers were self-inconsistent: zero
`c` accepted, one **refused**, two through four accepted. Accepting 0 and 2 but
not 1 is not the language of any particle, which is what makes it a bug and not
a defensible reading. That model describes exactly `c` occurring nought to four
times.

The rule the engine had missed: XSD satisfies a particle by partitioning the
content into between `minOccurs` and `maxOccurs` consecutive parts each
matching the term, and **nothing in that rule requires a part to be
non-empty**. When the term is nullable, an empty part satisfies it. An
iteration that matches nothing is still an iteration. So the legal totals are
the union over `i` in `[oMin, oMax]` of `[i*iMin, i*iMax]`, which for
`iMin = 0` is just `[0, oMax*iMax]`.

The corollary constrains any future change here: **no maximum needs relaxing to
accommodate this.** Empty iterations are only ever added to reach a floor, and
a reading that would break a ceiling can decline to add them.

### Saturation is right for the matcher and wrong for the derivation checks

Occurrence bounds saturate at `occursHuge` = 4611686018427387903. Two bounds
that both exceeded it once compared **equal**, because both clamped to it: a
base `maxOccurs="1000000000000000000000000000000"` (1e30) restricted by three
members each at the same value has a true effective total of 3e30 against a
base of 1e30, so the restriction is invalid and was accepted. A false *accept*,
which is why it was fixed rather than documented.

**The distinction is the durable part, and it is a live constraint on anyone
touching occurrence arithmetic.** The matcher compares a bound against a
*document*, where 1e30 and 3e30 genuinely are the same proposition — more
children than any document will ever have. The derivation checks compare two
bounds against *each other*, where they are emphatically not. So the exact
value is carried alongside the clamped one rather than replacing it:
`Particle` keeps `MinOccurs`/`MaxOccurs` as `int`, since the automaton, the UPA
checker and the matcher neither need exactness nor should pay for it, and gains
`*big.Int` fields that are nil unless clamping actually discarded something.

**`maxOccurs="unbounded"` stays the `Unbounded` sentinel and is never written
as a magnitude.** "No limit" and "a very large limit" are different
propositions, and conflating them is precisely how the original defect arose;
folding unbounded into the exact layer would have recreated it one level up.

**What is deliberately not exact:** everything downstream of content-model
compilation. A bound reaching the automaton is still the clamped int, because
the runtime question is "did this document supply enough children", and no
document can approach the saturation point.

### A depth bound is not cycle detection

The counterpart to the entry above on saturation, and the opposite verdict: a
bound that *does* look wrong and *is*. Twenty-four guards across `xsd/`, `relaxng/`,
`xdm/`, `xpath/` and `xslt/` stopped a graph walk at a step count — 32, 64,
256, 500, 4096 — and every one of them was a defect. They are gone; what
follows is why, because the shape is easy to reintroduce and was reintroduced
six times before it was named.

**The reason each bound was written was sound.** A model group, a union chain
or a base-type chain that reaches itself is legal to *write*; these walks run
before the content-model compiler that reports it, and would otherwise recurse
forever. The count terminated them.

**What makes it a defect is that a count cannot tell a cyclic graph from a
merely deep one.** A legal, acyclic, entirely ordinary schema — 33 user-defined
restrictions over `xs:int`, or a base declaration nested inside 32 sequences,
or 501 distinct definitions each `<ref>`ing the next — crosses the cliff and
gets the truncated answer. Nothing in such a schema is recursive or malformed.

**The dangerous part is returning a definite answer rather than a refusal.**
Almost every one of these walks answers a yes/no question, and on running out
of steps returned a `false`, a `nil` or an empty map that the caller could not
distinguish from a completed walk. The failure directions were all three kinds:

* **acceptance** — `collectElementDecls` returning an empty map skipped Element
  Declarations Consistent entirely; `nonAtomicUnionMember` returning `nil` let
  a list of lists load; a duplicate `xs:ID` was accepted once the restriction
  chain under `xs:ID` ran 64 links, and `"1.5"` validated against a type
  descending from `xs:integer` at the same depth; `checkTypeBaseCycles` giving
  up after 4096 steps meant the function that exists to catch circular types
  could not catch a large circular type.
* **rejection** — `derivedFrom` refusing a legal `xsi:type`, and `relaxng`'s
  `maxRefDepth = 500` refusing a legal 501-definition grammar outright.
* **silent erasure**, the worst of the three, because nothing reports an error.
  The five walks over `derivedPrimitives` in `xdm/node.go` simply delivered the
  value untyped past 32 links, so a comparison that should have been numeric
  became a string comparison and a transform produced a wrong answer rather
  than a diagnostic. `accumulatorOrigin` in `xslt/accumulator.go` is sharper
  still: past 64 links it returned the intermediate copy it had reached — a
  node in a tree of its own, where the accumulator computes something else
  entirely. A legal-looking wrong number that nothing downstream can detect.

**Why not raise the constant.** 32 to 1024 moves the cliff without removing it
and leaves the same bug waiting at a depth nobody will test. The arbitrariness
is the argument: `derivationMethodsTo` surfaced only because a legal schema
stopped *loading*, and its cliff sat at 65 where the validation-time walks sat
at 257, because one counted links and the other types. `relaxng`'s bound is the
sharpest case — the mechanism it was named for, `c.expanding`, sat immediately
above it and already caught every re-entry, so the count could never do the job
and could only refuse valid grammars.

**A visited set is the exact mechanism, and it must be keyed on what the
recursion revisits.** Every bound is now a set keyed on the component pointer,
or on the name string where the graph is a name-to-name registry. That
identifies a cycle exactly — the only thing the count was ever trying to
catch — and imposes no limit on a legal chain. `allDerivedDecls` is the
instructive failure: it already kept a `seen` set, but on *declarations*, which
deduplicates the result without bounding the walk, since a model group that
reaches itself revisits the same particle forever without ever repeating a
declaration.

**Convert the unreachable ones too.** Several of these walks were already
unreachable because their chains collapse during parsing. They were converted
anyway: a lone survivor of a pattern this one invites the next reader to copy
it.

**Reachable from a schema, not from an instance.** A trusted schema with
untrusted documents cannot reach any of them, which is why none of this is a
security bound and why removing the counts costs nothing there.

**What such a test must assert.** Depths on either side of every old cliff, a
*semantic* property at each rather than that a call returned; the negative, so
that a visited set which widened the relation is caught; a genuinely cyclic
input behind a watchdog, because the regression a visited set can introduce is
a hang, which no assertion catches; and, where the registry is process-global,
type names carrying the case's own depth and walk, since `go test` runs one
process and two cases sharing a name would answer each other's questions.

Above all, **a probe must establish that the loop it measures actually runs** —
which is the subject of the next entry.

### A negative result on a bound must prove the loop it bounds actually runs

The most expensive lesson in this file, and the one most likely to be
re-learned by anyone auditing a step count. An earlier revision of this
document argued that a family of `seen > 64` and `seen > 256` counters were
*not* defects, and the argument read:

> These counters remain on *iterative* walks up a type's base chain rather than
> recursive descent through a graph; a legal restriction chain 300 links long
> was checked in both directions and the facet survived intact, so these
> counters are not truncating a real schema.

**The measurement was real and the conclusion was wrong, because the probe
drove a walk that does not iterate.** A facet chain collapses during parsing:
`SimpleType.Primitive` is filled in on every link as it is built, so
`primitiveOf` returns on its *first* iteration whatever the chain length, and
the facet is enforced from the merged `FacetSet` rather than by walking at all.
A 300-link chain exercised the loop exactly once. The probe proved nothing
about the bound and everything about the parser.

**A baseline that reads "correct" for the wrong reason is worse than no
baseline**, because it is quoted afterwards as evidence. This one was, for two
revisions.

The walks that *do* iterate are the ones asking a question the parser did not
pre-answer: which built-in a type descends from, and whether one type derives
from another. Six of those truncated on a legal acyclic chain, and the two
false accepts are what the superseded reasoning had licensed: a duplicate
`xs:ID` was **accepted** once the restriction chain under `xs:ID` ran 64 links,
because `idKind` returned `""` and the ID bookkeeping skipped the value; and
`"1.5"` validated against a type descending from `xs:integer`, because
`descendsFromInteger` returned `false` and the integer lexical check never ran.
Neither schema is recursive or malformed.

So the rule for any future audit of a bound: **measure the chain length on the
built component before concluding anything**, and show the loop taking one step
per link. `TestBaseChainActuallyIterates` is written that way for exactly this
reason, and `TestDeepFacetChainCollapses` and `TestDeepUnionAndListCollapse`
pin the collapsing walks so that the superseded negative result above cannot be
re-derived from the same shape.

### Four constraints on the 1.1 restriction relaxations

The relaxations themselves are done and are in the changelog. What must survive
is the set of guards each one turned out to need, because every guard was found
by breaking a case that the obvious version of the change had not considered.

**They are version-gated, not general.** `particlesT002`/`T009` (a reordered
choice), `particlesHa161` (an optional element restricting an optional choice)
and `particlesZ023`/`Z024` (a one-member choice) are all marked invalid under
1.0 and valid under 1.1. 1.0's RecurseLax really is written as an
order-preserving walk, and `stripPointless` removing a one-member choice is
*correct* for the 1.0 table. Removing the strip unconditionally fixed the two
1.1 cases and broke the same two under 1.0, for a net loss of three.

**A range cannot serve both `recurseAsIfGroup` and `effectiveTotalRange`.**
Moving an optional element's range onto the wrapper works only where the base
does not repeat: in `effectiveTotalRange` a group of one repeating N times
contributes N elements, so the same range means two different things. This is
what broke `particlesV020`, and it is the same collision recorded in full under
*the occurrence-carrying wrapper* below.

**The derived minimum must already satisfy the base's.** Without that condition,
moving a `minOccurs` of 0 onto the wrapper made it violate a base requiring 1,
and `ctF007` became a false reject for exactly one case gained.

**The base's compositor decides whether a wrapper may be kept.** Keeping every
one-member choice under 1.1 turned `particlesR001` into a false reject: a
one-member choice restricting a sequence-with-wildcard is valid, and only
reaches a cell of §3.9.6's table once the wrapper is gone. The wrapper is
preserved only when *both* sides are choices, where the pair decides the cell.

**Only a group occurring exactly once may be inlined into an all group.** An
all group of all groups admits exactly the interleaving of their members, so
the nesting carries no information the flat list does not — but a *repeating*
group multiplies its members' occurrence ranges, and folding that into the
parent would compare the wrong budgets. That is the ambiguity `allSubsumes`
exists to refuse rather than guess at.

### Particle restriction: the occurrence-carrying wrapper (attempted, reverted)

`particlesZ001` is a `<sequence>` whose `<element name="element" minOccurs="0"
maxOccurs="unbounded"/>` restricts a base `<choice minOccurs="0"
maxOccurs="unbounded">` containing that element. It is valid under both
versions and is refused.

The cause is visible: `recurseAsIfGroup` wraps the element in a group of one
and hardcodes the wrapper at `1..1`, discarding the element's own occurrence
range. A once-only group is then compared against a repeating one, so the
repetition the base allows looks like something the restriction dropped.

**Moving the range onto the wrapper fixes the case and loses ground overall.**
Measured: `particlesZ001` and `particlesZ023`/`Z024` start loading, but schema
agreement falls 14,204 → 14,194 on 1.0 and 15,045 → 15,038 on 1.1 — about
eleven invalid schemas newly accepted for each valid one recovered. Reverted.
(Those totals are the baseline of the run that measured them, not the current
figures. What matters is the ratio, which is why they are left as recorded.)

The reason is that the wrapper's range is doing two jobs. For the mapping in
clause 2 it should repeat; for the *effective total range* check it should not,
because a group of one repeating N times contributes N elements where the
original particle contributed its own range. Carrying the range fixes the first
and breaks the second. A correct fix needs the two separated rather than one
range serving both — which is a change to `effectiveTotalRange`'s contract, not
a change to this wrapper.

### Particle restriction edge cases (XSD)

`addB118`, `addB183`, `particlesHa161`, `particlesT002`, `particlesT009`,
`particlesZ001` — 6 schema false rejects in
1.1, 2 of which (`addB183`, `particlesZ001`) also fail in 1.0.

Individually diagnosed cases in Particle Valid (Restriction) rather than one
cluster. `particlesZ001` and `addB183` failing in both versions makes them the
best entry point: they are bugs in the shared logic, not 1.1-specific gaps.

### A collection URI resolves against the static base, not the context item

Recorded because the reading is not the obvious one. `fn:collection` once
passed the *context item's* base URI to the resolver, so
`collection("collection1")` asked about whichever document was in focus rather
than about what the expression named. The spec resolves the argument against
the **static** base URI. The item's base remains the fallback for a caller who
set no static base, and resolving stays the resolver's job — the engine hands
over the base and does not guess what a URI means to the caller.

`cta0022` is unaffected either way. With no resolver configured the default is
still `FODC0002`, which is the point, and the refusal is recorded under *Won't
fix* above.

### Instance validation gaps (XSD)

25 real instance false accepts in 1.1 after removing the W3C-flagged ones.
Diagnosed individually rather than by cluster:

- `Simple/simple001`, `simple002`, `simple016`, `simple086` — keyref `@ref`,
  and union member substitutability through a restricted union.
- `ElemDecl/typeDef012*`, `valueConstraint007*` — element declaration value
  constraints.
- `MS-ComplexType/ctZ013c`, `-d`, `-e` — complex type edge cases.
- `MS-IdentityConstraint/idG006`, `idK012` — identity constraint scoping.
- `suntest/idc006.nogen` — keyref resolution across a subtree boundary
  (a false *reject*, so more serious than the rest of this list).
- `XmlVersions/xv009` — XML 1.1 line-end normalisation, which requires the
  parser to distinguish XML 1.0 from 1.1 document declarations.
- `MS-Schema/schU4`, `schU5`, `MS-Additional/isDefault070`, `isDefault077`,
  `MS-SimpleType/stE054`, `MS-Regex/reK6`, `Complex/complex022`,
  `CTA/cta0006` — one-off cases, each needing its own diagnosis.

### XPath cases that are not engine bugs

`fn-doc-available-5` and `functx-fn-doc-available-1` are **not** engine bugs:
their environment declares no `uri` for the source, so `fn:document-uri`
correctly answers with a filesystem path that no resolver knows.
`fn-in-scope-prefixes-25` needs a namespace declared through a DTD default
attribute, which `encoding/xml` never parses.

---

## What would move the numbers

The counts this section used to carry were measured in August and were never
re-derived. They had drifted badly — the XSD table summed to 251 and 368
disagreements against 51 and 47 actually measured, and the XPath section
described twenty-six open failures on suites that report none. Restating
figures here also violated this file's own rule at the top: they live in
[conformance-gaps.md](conformance-gaps.md), because a percentage copied into
two places drifts in one of them. It drifted in this one.

What is durable from it is the route rather than the arithmetic. Of the
seventeen XPath disagreements that remained after the ordinary bugs were
fixed, **five were the QT3 harness rather than the engine**, two needed DTD
attribute defaulting, two needed a document to be retrievable under the URI
`fn:document-uri` reports for it, and one was a lexical form that disagreed
with its own value. The adversarial audit of the XSLT and XSD verdicts found
the same shape again: of the twenty-three cases it judged fixable, most were
the harness — chiefly eight XSD `indeterminate` expectations per version
silently scored as "must be invalid" — and only four were engine defects.
All twenty-three have since been fixed or reclassified, and the fixable column
is empty on every suite.

A conformance number is only as honest as the harness producing it, and a
verdict is only as good as the last time someone re-derived it.

For what is currently fixable, open, or unreachable, and why, see
[conformance-gaps.md](conformance-gaps.md). For what buying it would cost, see
[reaching-100.md](reaching-100.md).

XML 1.1 support sits outside all of this. It is not a matter of schemas that
fail to parse — measured, every schema in the suite's `XmlVersions` set parses
and loads, because `version="1.1"` is accepted and then read under 1.0 rules.
The gap is that the reading is wrong, not that it is refused. It is a larger
piece of work and is described in
[todo.md](todo.md#11-xml-11-documents--the-largest-single-win).

---


## What 100% would take

Measured at `6fa4150` with both suites present. The short answer: **XPath now
reaches 100% at all three versions, and XSD cannot reach 100% at all** — part
of the remaining gap is the suite disagreeing with itself.

### The ceiling that is not ours

| | XSD 1.0 | XSD 1.1 |
|---|---:|---:|
| disagreements | 51 | 47 |
| of those, W3C-flagged `queried` or tied to an open bug | 45 | 44 |

Those are cases where the W3C's own metadata records a dispute about the
expected result. Nineteen of them are one cause: bug 4113, the `\p{Lu}`,
`\p{Ll}` and `\p{Lo}` tests, written against Unicode 3.1 before characters such
as U+1D7A8 moved between general categories. Passing them means freezing a
Unicode 3.1 table and being wrong about modern text. **They are a reason to
stop short of 100%, not a defect to fix.**

So the ceiling is **99.90% on 1.0 and 99.91% on 1.1**, not 100 — and both are
where the engine already stands, since none of what remains is fixable. Those
figures rose without any behaviour changing, when the driver stopped scoring the
suite's `indeterminate` expectations: 16 cases on 1.0 and 14 on 1.1 prescribe no
result, so they now leave the ratio instead of being counted as failures.

### XPath: no failures; one case refused by default until the harness enabled it

`fn-matches-51` names a group whose width can vary *and* places the
backreference mid-pattern. Under the default engine both are refused: RE2
returns a single submatch assignment, so for a variable-width group the split
it reports may not be the one that matches, and a comparison against it would
answer confidently and wrongly.

It passes with `xpath.SetBacktrackingRegex(true)`, which takes QT3 to
15,183 of 15,183. That figure is not the headline one, because the switch is
off by default and the headline number reports the default configuration.

Eleven of the twelve backreference cases that used to sit here are fixed. When
every named group has a **fixed** width the greedy assignment is the only
assignment, so comparison is exact and stays linear — no backtracking engine,
and the DoS class [security.md](security.md) keeps out stays out. The full
reasoning is under *Regular expression backreferences* above.

**Closing the last one would cost the linear-time guarantee**, which is a worse
trade than the case is worth.

### XSD schema-validity: 36 (1.0) and 90 (1.1) that are ours

All false *accepts* — invalid schemas that load.

Four of them were not a missing rule at all. `checkContentModelConstraints`
walked only the schema's *named* types, so Unique Particle Attribution and
Element Declarations Consistent never ran against a complex type declared
inline in an element — the ordinary spelling. A schema with no named types was
checked against nothing, and `(a?, a)` loaded clean. **That is a validator
failing open, not a conformance point**, and it is the second time this exact
shape has been found here: the particle-restriction constraint had the same
gap. When adding a schema-component constraint, check that the walk reaching it
visits anonymous types too.

What remains clusters in 1.0 `MS-Particles`, `MS-Additional` and
`MS-Wildcards`; in 1.1 `Simple`, `MS-Particles`, `Zone`, `Override` and
`Open`.

Each is an unwritten Schema Component Constraint. There is no single change
here: it is one rule at a time, and **every rule added is a chance to reject a
schema real systems depend on**. That is not hypothetical — the rounds that
produced these figures caught, and reverted before shipping, a
`cos-list-of-atomic` reading that rejected the test suite's *own catalog
schema* and 91 instance tests with it, a base-type circularity check that
rejected 11,044 of 14,405 schemas by omitting the ur-type exception, and a
wildcard rule that rejected sixteen valid schemas by treating a
validation-time constraint as a schema-time one.

The conformance suite cannot be relied on to catch that on its own: it scores
agreement with W3C labels, so an over-strict rule shows up only where the
suite happens to contain a valid schema exercising it. `tests/check.sh`
re-loads the production corpora for exactly this reason, and the W3C's own
`schema-for-xslt30.xsd` — reached through the XSLT suite in nine seconds —
proved the sharper guard of the two.

### XSD schema-validity: 6 (1.0) and 11 (1.1) addressable false rejects

The ones that matter, because a false reject breaks a working caller. Most are
Particle Valid (Restriction); `iri-001` needs a DOCTYPE and is refused by
design.

One attempt is recorded above as reverted: carrying the element's occurrence
range onto `recurseAsIfGroup`'s wrapper fixes `particlesZ001`, `Z023` and
`Z024` and costs about eleven false accepts for each — the wrapper's range
serves two jobs that want opposite answers. A correct fix separates them, which
is a change to `effectiveTotalRange`'s contract.

Three of the 1.1 entries — `particlesHb008`, `particlesHb011` and
`particlesZ028` — need XSD 1.1's §3.4.6.4 intensional restriction: genuine
language inclusion in *both* directions rather than the structural table.
`particlesHb008` restricts `choice{e1, sequence{e2,e3,e4}}` by a reordered
`choice{e1, sequence{e2, choice{e3,e4}}}` that no table can relate. That is an
automaton subsumption engine, not a rule, and two rounds declined it
deliberately rather than ship a partial one.

### XSD instance: 1 addressable false reject

`attP031.i`, in both versions. `particlesZ040.i` stood here too and no longer
does; the matcher that decides it is described under *Why the occurrence
counters are a vector and not a bracket per scope* above.

`attP031` is a suite self-contradiction rather than a defect
here: it declares `use="prohibited"` with a `fixed` value and expects the
instance supplying that value to be *valid*, while `attF001` — structurally
identical but without `fixed` — expects invalid, and both carry status
`accepted`. §3.4.2 gives `{attribute uses}` only the declarations whose `use`
is absent, `optional` or `required`, so a prohibited use creates no attribute
use at all. Making `attP031` pass means treating `fixed` as the discriminator,
which no clause supports.

Two further instance false rejects are disputed rather than addressable:
`gMonth002_2061` and `gMonth004_2063` test the old `--MM--` form under W3C bug
6901. `cta0022` was in this list and is now fixed — its type alternative's
XPath was *raising* rather than answering, and a type alternative whose test
raises is silently skipped, so a crash was indistinguishable from a false
test.

### Suite cases that should be read as disputed

These carry status `accepted`, so the addressable counts above include them,
but each is questionable on the suite's own evidence:

* **Four `notQName` tests are 1.1-only in substance but run under 1.0.**
  `s3_10_1ii08s`/`ii09s` are the only un-versioned groups in `wildcard.testSet`
  using `notQName`, while seven sibling groups carry `version="1.1"`. The
  `s3_10_6` pair is worse: `v01`/`v02` *do* carry `version="1.1"`, but the
  tests that fail are `ii01`/`ii02`, different un-versioned groups reusing
  those names. Our version logic implements the suite's own token rules
  correctly; the data is what is wrong.
* **`simple093` contradicts `particlesZ007`.** The first declares
  `<xs:union memberTypes="xs:QName xs:NOTATION"/>` invalid; the second contains
  `<xsd:union memberTypes="xsd:NOTATION"/>` and is valid. Enforcing the rule
  trades one for the other, and loses `particlesZ007` outright under 1.0 where
  `simple093` is not even run. The list form is enforced; the union form is
  deliberately not.
* **`particlesK006` under 1.1.** L(R) ⊆ L(B) holds, so it is a valid
  restriction under §3.4.6.4, yet it is marked invalid with no version
  qualifier. It is the guard that constrains any fix for `mgO029`.
* **`particlesZ001` under 1.0** is expected valid with no version attribute
  while its own annotation calls the 1.0 rule "ambiguous" and tags it as
  intensional restriction, a 1.1 feature.
* **`simple004`/`005`** are self-flagged as depending on the resolution of spec
  bug 2074, and `simple006`'s own note says "one could argue for valid".

### Honest summary

| | now | reachable | what stands in the way |
|---|---|---|---|
| XPath 2.0 | **100.00%** | 100.00% | reached |
| XSD 1.0 instance | **99.89%** | ~99.9% | at the target; 2 addressable false rejects, both recorded as hard |
| XSD 1.1 instance | **99.90%** | ~99.9% | same |
| XSD 1.0 schema | **99.91%** | **~99.91%** | 13 disagreements, all queried or suite defects |
| XSD 1.1 schema | **99.92%** | **~99.92%** | 12 disagreements; `iri-001` is the one addressable case |

The two schema rows once read `~99.9%`, which contradicted the ceiling derived
under *What 100% would take* above and could not be reached. The reachable
figure is set by how many disagreements the W3C's own metadata disputes, found
by splitting each one on the `<current status=...>` the suite records. When
that work started:

| | XSD 1.0 | XSD 1.1 |
|---|---:|---:|
| disagreements | 249 | 365 |
| `accepted` — addressable | 197 | 311 |
| `queried` or bug-tied — the ceiling | 52 | 54 |

and today 95 and 154, of which 46 and 103 are addressable. Eighteen of the
1.0 disputes are bug 4113 alone. Reaching 99.99% would have meant fixing 200
of the original 201 1.0 schema disagreements and 313 of the 314 in 1.1 —
arithmetically impossible without "fixing" tests the W3C itself questions.

Nothing here is blocked on a missing idea. XPath's last case is a deliberate
refusal, the XSD false accepts are volume rather than difficulty, and the false
rejects are one subsystem that needs its occurrence handling reworked rather
than patched.

**Note that reaching 100% on XSD is not possible and not desirable.** 48 of the
1.0 disagreements and 47 of the 1.1 ones are cases the W3C's own metadata
records a dispute about; nineteen are the bug 4113 general-category tests,
where passing means freezing a Unicode 3.1 table and being wrong about modern
text.

## Fixed

Defects that were diagnosed here, fixed, and carry no lesson the entries above
do not already state. Kept as one line each so a reader who remembers the
symptom can find the change; the mechanism and the measurements are in the
changelog. Direction is given because it is what decides how much a defect
mattered: a false reject breaks working input, a false accept lets bad input
through, and a silent erasure produces a wrong answer with no diagnostic.

**Occurrence and particle handling (XSD).** Nested occurrence bounds decided
wrongly in both directions for a repeated group with a single repeating child
(false accept and false reject). An emptiable inner particle refused at small
child counts (false reject). Two occurrence bounds past the saturation point
comparing equal in the derivation checks (false accept). A reordered choice, an
optional element restricting an optional choice, a nested all group, and a
one-member choice all refused under 1.1 (false rejects). See CHANGELOG.

**Graph walks bounded by a step count.** Twenty-four guards across `xsd/`,
`relaxng/`, `xdm/`, `xpath/` and `xslt/` replaced by visited sets: four schema
walks and twelve base-chain counters, a 4096-step base-cycle check, a 500-deep
`relaxng` ref bound, five data-model walks and seven in the query and transform
layers (false accepts, false rejects and silent erasure, one of each kind).
See CHANGELOG.

**Schema component constraints.** `checkContentModelConstraints` walking only
*named* types, so UPA and Element Declarations Consistent never ran against an
inline complex type (false accept). See CHANGELOG.

**XSLT and XPath.** A union's selected member dropped by three copy sites
(silent erasure). `fn:collection()` unimplemented, and then resolving a
relative collection URI against the context item's base rather than the static
base (capability gap). `xs:decimal` rendering capped at 18 fractional digits,
so a value printed as `0` while comparing unequal to it (silent erasure).
`in-scope-prefixes(/)` answering for the root element rather than raising
`XPTY0004`, and `castable as xs:QName` answering true for a non-literal operand
(false accepts). Four further singleton failures — `fn-doc-29`,
`op-concatenate-mix-args-019`, `fn-union-node-args-003`, `ForExpr013`,
`CondExpr017`. See CHANGELOG.

**Harness, not engine.** `<source file="...">` paths resolved against the suite
root rather than the document that named them, which skipped 461 cases as
"source unavailable" rather than counting them; in-scope cases went from 14,720
to 15,181. Recorded because a suppressed case is not a passing one, and the
count moved without any engine behaviour changing. See CHANGELOG.

## Related

[conformance-gaps.md](conformance-gaps.md) is the ledger: every failing case in
every suite, named, with the current numbers and a fixable / not-fixable
verdict. This file is the reasoning behind the hard ones.

[todo.md](todo.md) is the forward-looking half of this file: what to build next
and what each item would cost. Several gaps here — XML 1.1 line ends, DTD
support — are entries there as features rather than bugs.

## How to re-measure

Neither suite is vendored — both belong to the W3C, and `testdata/` is
gitignored. Clone them where the commands below expect:

```
git clone --depth 1 https://github.com/w3c/qt3tests.git   testdata/qt3tests
git clone --depth 1 https://github.com/w3c/xsdtests.git   testdata/xsdtests
```

The figures in this file were measured against `qt3tests` at `201a6e46`
(2026-05-14) and `xsdtests` at `7bc3365c` (2026-04-01). Both are updated from
time to time, so a later checkout can move a denominator.

`tests/check.sh` runs everything below in one go, and is what to use after any
substantive change:

```
GOXSLT_UBL=<ubl-dir> GOXSLT_CII=<cii-dir> tests/check.sh
tests/check.sh fast     # build, vet, unit tests, race only
```

It reports a missing suite as skipped and a present-but-silent suite as a
failure, because a check that did not run must not look like one that
succeeded. That distinction is not theoretical: the first run of this script
caught a relative `GOXSLT_QT3` resolving against `./tests/qt3/` rather than the
repository root, which made the suite skip itself while `go test` reported
PASS.

QT3 also runs from the test suite directly:

```
GOXSLT_QT3=$PWD/testdata/qt3tests go test ./tests/qt3/ -run TestQT3 -v
```

Set `GOXSLT_QT3_VERBOSE=1` to list every failure with the expression it ran,
and `GOXSLT_QT3_SET=<substring>` to run only the matching test sets — the
percentage is then labelled as filtered rather than quoted as the suite
result.

The XSD driver and the corpora runners live in [`tests/`](../tests): they were
rebuilt from scratch each time before that, which is how three metadata rules
came to silently inflate earlier measurements. See the README's *W3C xsdtests
suite* section for what those rules are.

Before accepting any change that adds a schema-validity rule, load the
production corpora — 65 UBL 2.1 entry points and 427 UN/CEFACT CII schemas.
The suite cannot catch a rule that is stricter than the spec; real schemas can.
