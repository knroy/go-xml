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

### Content model matching is greedy, not backtracking

`MS-Particles/particlesZ040` (instance, both versions).

`matchSequence` walks the Glushkov automaton one path at a time and arbitrates
nested occurrence counters with heuristics. It decides every content model in
the suite and both production corpora except this one: a
`<sequence maxOccurs="3">` holding an optional element, an unbounded wildcard
and another optional element. Because the neighbours are optional, the wildcard
is both a *first* and a *last* position of the outer scope, so every
wildcard-to-wildcard step reads as a restart of the sequence. Twenty-three
children drove the outer count to 14 against a maximum of 3.

Two fixes were tried and both reverted:

1. Skipping the outer increment when an inner counter accounts for the step —
   restoring symmetry with `counterAllows`, which already excuses such a step
   from the outer *bound*. Unit tests stayed green; the suite lost **6 cases on
   both versions**.
2. Additionally recording, per scope, the wrap-around follow edges the compiler
   lays to make a scope repeatable, and treating only those as restarts. That
   fixes `particlesZ040` and breaks `TestInnerBoundIsNotATotal`, where an outer
   choice and an inner element are self-loops on the *same* position: the edge
   serves both scopes and excluding it from either reading is wrong.

The two pull in opposite directions because the ambiguity is real. Which scope
repeats is only knowable from the rest of the input, which is a backtracking or
subset-construction question, not something a per-edge compile-time label can
answer.

A third attempt did land: bracketing the repetition count into a low and a
high reading, which carries `particlesZ040` in both versions and costs
nothing elsewhere. It does not make the matcher exact, and the section below
is the part it does not reach.

### Nested occurrence bounds are wrong in both directions

Not a suite case — found by differential fuzzing against a brute-force
reference, and invisible to both W3C suites.

A repeated group whose *only* child is itself repeating is decided wrongly in
both directions. For `<sequence minOccurs="5" maxOccurs="5">` over
`<element c minOccurs="2" maxOccurs="2"/>`, the only valid document is ten
`c`, and it is **refused**; five `c`, which no reading admits, is **accepted**.
For `<sequence 2..2>` over `c{2,4}` the valid range is four to eight, and the
answers are inverted across the whole sweep.

The cause is the bracket itself: `counterAllows` consults the low count and
`countersSatisfied` the high one, so a document is admitted when *different*
readings satisfy each bound though no single consistent reading satisfies
both. When the group holds one particle its FIRST and LAST positions coincide,
which is what makes the wraparound edge indistinguishable from the inner
element's own repeat edge, so the bracket cannot be narrowed locally.

The false-accept direction matters more than the arithmetic: a `minOccurs`
floor is silently not enforced, so a schema believed to require a minimum
count does not require it.

A group with two or more distinct child names is decided correctly, which is
why 39,347 XSD 1.0 agreements and 41,532 on 1.1 do not cover it. Verified
present at `06e8a75`, before the bracket existed, so this is long-standing
rather than a regression — the bracket's commit message documents the
false-reject half and not the false-accept half.

Fixing it means tracking the set of reachable count-**vectors**, and the
emphasis is the point. A fourth attempt replaced the low/high pair with a set
of reachable counts *per scope* and measured it: the false accept of five `c`
disappeared, and so did the odd counts under an unbounded outer scope. But
every valid document stayed refused, because a per-scope set still loses the
correlation between scopes — when the outer sequence restarts, the inner
element's count must restart *with it*, and two independent sets cannot say
which inner count belongs to which outer reading. Resetting nested counters on
an outer restart fires on the same transition that set them, and took
`TestRepeatedOptionalSequence` and the substitution-group cases down with it.

So the unit of tracking has to be a vector over all scopes at once — a set of
whole readings, not a set per scope. That is a subset construction over the
counter state, which is a different matcher rather than a patch to this one. `TestNestedOccursBoundsAreWrong` in
`xsd/occurs_nested_test.go` records the case and is skipped.

Fixing the matcher properly means replacing it — a deliberate project weighed
against the 99.8% that already holds.

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

### A union's selected member did not survive a tree copy (XSLT) — fixed

`<xsl:template match="Date[data(.) instance of StandardDate]">` never matched,
where `StandardDate` is a named simple type — `xs:string` restricted by a
pattern — brought in by `xsl:import-schema`. The plain `match="Date"` template
won instead, and the source text was copied through unprocessed.

The cause was neither of the two this entry once named as the candidates. The
element *did* carry its annotation after validation, and the type name *did*
resolve in the pattern's static context. What was lost was the third fact, the
one between them.

`Date` has type `DateType`, a complex type with simple content extending
`GeneralDate`, which is a union of `StandardDate` and `xs:string`. XSD 1.0
§3.14.4 selects a union's member per *value*, so the annotation alone cannot
say whether "29 MAY 1917" is a `StandardDate` or a plain string — the validator
records the winning member separately, in `xdm.Node.UnionMember`, and
atomisation reads it to decide what the typed value is. A union's own
derivation chain runs to `xs:anySimpleType` and stops, so without the member
there is nothing to build a typed value from and the node atomises to
`xs:untypedAtomic`.

Three copy sites carried `TypeAnnotation` and dropped `UnionMember` beside it:
`stripCopyNode` in `xslt/transform.go`, `xdmbuild.DeepCopy`, and the parentless
attribute copy in `xslt/copyfuncs.go`. `xdm/xinclude.go` already copied both,
which is what made the omission visible as an inconsistency rather than a
design.

The failure was silent and selective. The same pattern answered *true* on any
path that had not been through a copy, so `validation-0201` sorted its events
by a key that saw the type and then dispatched on a pattern that did not.
`xsl:strip-space` — a declaration about whitespace, with nothing to say about
types — was what untyped the document.

Diagnosed by measurement rather than by reading: a probe over the validated
tree showed `union="StandardDate"` present on every `Date`, and a trace at the
`instance of` match site showed the annotated value arriving 1614 times from
the sort key and an *unannotated* one arriving twice, from `apply-templates` —
the two calls that produce the output.

With the fix, the case's output is byte-identical to Saxon's expected file
apart from whitespace: all three dates now render "29 May 1917", "12 September
1953", "22 November 1963". It still fails, on the indent width alone — see
`validation-0201` in `docs/conformance-gaps.md`, where that difference is
recorded as implementation-defined. So the fix costs and gains no suite case,
and is covered by `xslt/unionmember_test.go` instead.

## Open

Real gaps with no work done. Ordered by how much they cost.

### A hyphen after a variable reference is read as part of the name (XPath, XQuery)

`$e-1` evaluates as a reference to a variable named `e-1`, not as `$e - 1`.
The same applies to `$e-$s`, which fails to parse at all. Both are accepted by
Saxon and BaseX as subtraction.

```
$e-1     XPST0008: undeclared variable $e-1
$e-$s    XPST0003: unexpected "s" after expression
$e - 1   4          (whitespace makes it work)
```

The cause is not a lexer defect. `-` is a legal `NameChar`, so `e-1` really is
a well-formed NCName and `lexNCName` is right to take it; XQuery genuinely
allows hyphenated variable names, and `$a-b` **must** keep reading as the
single variable `a-b`. The two cases are distinguished only by the grammar's
whitespace rules, which say a `-` may end a name here — so the fix belongs at
the variable-reference site in the parser, not in the scanner.

It is unfixed because both candidate sites — `xpath/lexer.go` and
`xpath/parser*.go` — are the files the XQuery design depends on not changing,
and a change there risks a component measured at 100% for a defect the suite
does not exercise. Neither QT3 target covers it, which is why both languages
report clean figures with the bug live.

The workaround is a space: write `$e - 1`. Any query generated by a tool
should emit spaces around binary operators for this reason.

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

### A choice is unordered under 1.1 (fixed)

`particlesT002`, `particlesT009`: the derived choice offers the base's
alternatives swapped. A choice imposes no order on what it admits, so the
language is identical — but `recurseLax` walked the base list left to right and
could not go back.

1.0's RecurseLax really is written as an order-preserving walk, and the suite
marks both cases invalid under 1.0 and valid under 1.1, so the relaxation is
version-gated. Under 1.1 the assignment is a matching instead: each derived
alternative must restrict *some* unused base alternative. Each base alternative
backs at most one, since merging two would let the restriction admit a sequence
twice where the base admits it once.

### An optional element may restrict an optional choice (fixed)

`particlesHa161`: `<element name="a" minOccurs="0"/>` restricting
`<choice minOccurs="0">` whose branches are `1..1`. `recurseAsIfGroup` wrapped
the element at a fixed `1..1`, so its optionality was compared against a
branch's `1..1` and rejected — but the optionality belongs to the choice, not
to the alternative inside it.

Three conditions, and the third was learned the hard way:

* **Version.** Marked invalid under 1.0, valid under 1.1, like the reorder.
* **A non-repeating base.** Where the base repeats, moving the range is what
  broke `particlesV020`: the wrapper's range also feeds `effectiveTotalRange`,
  where a group of one repeating N times contributes N elements. One range
  cannot serve both uses.
* **The derived minimum must already satisfy the base's.** Without it, moving a
  `minOccurs` of 0 onto the wrapper made it violate a base requiring 1, and
  `ctF007` became a false reject for exactly one case gained.

1.1 schema agreement 15,048 → 15,051 across these two entries, with 1.0 and
both instance figures unchanged. (A figure from the run that measured it, not a
current total — later rounds moved the baseline well past it.)

### A nested all group is flattened before budgeting (fixed)

`all206`: a base `<all>` holding `<group ref>` and an element, restricted by an
`<all>` holding that element and a narrower group.

`allSubsumes` gave up on any base particle that was not an element
declaration, so a nested group sent the derivation to the 1.0 table, which
calls it Forbidden. XSD 1.1 requires a group reference inside an all group to
name a group whose model is itself an all group, and an all group of all groups
admits exactly the interleaving of their members — so the nesting carries no
information the flat list does not.

Only a group occurring **exactly once** is inlined. A repeating one multiplies
its members' occurrence ranges, and folding that into the parent would compare
the wrong budgets — the ambiguity `allSubsumes` exists to refuse rather than
guess at.

1.1 schema agreement 15,047 → 15,048, with 1.0 and both instance figures
unchanged.

### A one-member choice is not pointless under 1.1 (fixed)

`particlesZ023` and `particlesZ024`: a derived `<choice>` holding one
three-element sequence, restricting a base `<choice>` of two such sequences —
a valid dropping of one alternative.

`stripPointless` removed *any* one-member group wrapper, choices included. That
turned a choice-restricting-choice derivation into a sequence restricting a
choice, a different cell of §3.9.6's table with a different rule, and it was
rejected for "maxOccurs 3 exceeds the base's 1" — the three elements summed
against a choice that admits one branch.

Two conditions, both learned by measuring:

* **Version.** The suite marks these invalid under 1.0 and valid under 1.1, so
  the strip is *correct* for the 1.0 table and wrong only for 1.1's language
  inclusion. Removing it unconditionally fixed two 1.1 cases and broke the same
  two under 1.0, for a net loss of 3.
* **The base's compositor.** Keeping every one-member choice under 1.1 then
  turned `particlesR001` into a false reject: a one-member choice restricting a
  sequence-with-wildcard is valid, and only reaches a cell once the wrapper is
  gone. The wrapper is preserved only when *both* sides are choices, where the
  pair decides the cell.

1.1 schema agreement 15,045 → 15,047, with 1.0 unchanged.

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

### `fn:collection()` — fixed (XPath)

All 7 QT3 `fn-collection` failures are closed; the set is 17 of 17.

`xpath.CollectionResolver` and `Context.Collections` mirror
`DocumentResolver`/`Docs`; `xslt.TransformOptions.Collections` threads it
through a transform; and the harness parses `<collection>` environments,
loading through `Runner.loadDoc` so node identity and collection stability hold
across calls.

The last two were a relative collection URI. `fn:collection` passed the
*context item's* base URI to the resolver, so `collection("collection1")` asked
about whichever document was in focus rather than what the expression named;
the spec resolves the argument against the **static** base URI. The item's base
remains the fallback for a caller who set no static base, and resolving stays
the resolver's job — the engine hands over the base and does not guess what a
URI means to the caller.

Measured against the real suite, not inferred: 7 failures before, 0 after.

`cta0022` is unaffected. With no resolver configured the default is still
`FODC0002`, which is the point.

### Harness source paths were resolved against the wrong directory (fixed)

Not an engine bug, but it was suppressing 461 cases, so it belongs in the
record.

A `<source file="...">` path is relative to the document that names it. The
catalog writes `docs/atomic.xml` from the suite root; a test-set writes
`../docs/bib.xml` from its own directory. The runner joined every path against
the root, so each test-set-relative path escaped above it, the document was not
found, and the case was skipped as "source unavailable" rather than counted.

Resolution now happens during the environment merge, where the origin is still
known — after the merge a source no longer records which document named it.
In-scope cases went from 14,720 to 15,181.

Two consequences worth noting. `fn:doc` needed a resolver in the harness for
the same reason `fn:collection` did — environments name documents by URI, and
without one those cases failed closed. And two genuine `fn-doc` serialisation
bugs are now visible that were never previously exercised: an empty element
with a non-ASCII name, and namespace declarations on a document read through
`fn:doc`.

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

### `xs:decimal` printed fewer digits than it kept (fixed)

`K2-Literals-7` — a decimal literal with 359 leading zeros after the point.

`decimalScale` capped rendering at 18 fractional digits, so the literal printed
as `0` while the value kept full precision: `0.000…1 eq 0` was **false** and
`string(0.000…1)` was `"0"`. Whichever answer a caller trusted, the other
contradicted it.

A terminating decimal is now rendered in full. The bound moved rather than
disappearing — a rational that does not terminate, which is what division
produces, is still rendered at the 18 digits XPath 2.0 requires, and so is one
needing more than 1,024 digits, so formatting cannot be made to allocate
without limit. The *value* did not move; only its lexical form now says what it
is.

### Singleton XPath failures (all fixed)

Six remained here, one per set, each needing its own diagnosis: `fn-doc-29`
(namespace declarations dropped on a document read through `fn:doc`),
`op-concatenate-mix-args-019`, `fn-union-node-args-003`, `ForExpr013`,
`CondExpr017` and `K2-Literals-7`, the decimal literal above. All are closed:
XPath reports no in-scope failures at 2.0, 3.0 or 3.1.

Two of the four listed here before them were fixed first:

* **`fn-in-scope-prefixes-23`** — `in-scope-prefixes(/)` answered with the root
  element's prefixes. The parameter is `element()`, so a document node is
  `XPTY0004`; answering a different question hid the mistake.
* **`CastableAs648`** — `for $var in "ABC" return $var castable as xs:QName`
  answered true. Casting to `xs:QName` is defined only from a *literal* string,
  because the namespace comes from the static context and only a literal is
  folded where the prefix bindings are in scope. This is a static property of
  the operand, so it is decided in `CastExpr.Eval` rather than in
  `CastToDerived`, which sees a value and cannot tell a literal from a variable
  holding one. A value that is already an `xs:QName` is exempt — it carries its
  own binding — which `K-SeqExprCastable-18` pins.

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

XML 1.1 support sits outside all of this and unlocks 38 instance tests plus
nine schemas that do not parse today; it is a larger piece of work and is
described in [todo.md](todo.md#11-xml-11-documents--the-largest-single-win).

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

### XSD instance: 2 addressable false rejects

`attP031.i` and `particlesZ040.i`, in both versions.

`particlesZ040` is the greedy content-model matcher, with two reverted attempts
recorded above. `attP031` is a suite self-contradiction rather than a defect
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
