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

Any instance carrying a DOCTYPE. **`IRI/iri-001` no longer belongs here** — the
XSD driver was corrected by **3f2602e** (*"the XSD driver could not read a
schema built from entities"*) to load schema documents with `AllowDOCTYPE` set,
since a schema the suite ships is trusted input by construction. `iri-001` and
its ten masked instance cases now pass, and at `a8dee9a` neither `iri-001` nor
anything under `wgMeta/IRI.testSet` appears in either lane's disagreements.

The default policy below is unchanged; what changed is that a conformance
harness pointed at a vendored suite is not the untrusted caller the default
protects.

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

### Two schema false accepts the suite contradicts itself on

`MS-DataTypes/anyURI_b006_1356` (1.0) and `Simple/simple093` (1.1). Both are
invalid schemas this loads, and in both cases the rule that would reject them
also rejects a schema the same suite labels valid.

**`anyURI_b006`** puts backslashes in `xs:anyURI` enumeration values. The suite
annotates it "TSTF ruled that strictly speaking, per 1.0, the schema contains
one or more invalid anyURIs", and expects `invalid` under 1.0 but `valid` under
1.1 — 1.1 relaxed `anyURI` so that any sequence of characters is in the lexical
space, which is why this case appears in the 1.0 list only and not the 1.1 one.
That asymmetry is real and `isAnyURILexical` already implements it.

The rule that would catch it is "reject the RFC 2396 excluded characters", and
`anyURI_a014`, `a015` and `a016` are why it cannot be written: `a016` puts
`foo<bar`, `foo>bar` and `foo"bar` in `anyURI` enumerations and is expected
**valid**, unqualified by version. Backslash and `<>"` are the same production
in RFC 2396, so no uniform rule separates b006 from a016. The reading the code
takes — XML Linking §5.4 percent-escapes the excluded characters, so they are
in the lexical space rather than out of it — is the one that satisfies three
cases instead of one.

There is a second reason to leave it. Facet-value validation and instance
validation share one path down to `isAnyURILexical`, with no seam between them,
so tightening 1.0 `anyURI` would reject every instance value containing a
Windows path or an unescaped space. UBL is a 1.0 schema set and its documents
carry exactly those. The one suite case is not worth that.

**`simple093`** names `xs:NOTATION` as a union member type. §3.2.19 does forbid
using NOTATION directly, and this package enforces that for a restriction base,
a list item type, and an element or attribute type. The union arm is left
unenforced because `MS-Particles/particlesZ007` contains
`<xsd:union memberTypes="xsd:NOTATION"/>` and is labelled **valid**, with a
dependent instance test that only runs if the schema loads. Both cases carry
status `accepted`. Enforcing the rule was implemented and measured twice: it
trades simple093 for particlesZ007 in 1.1 and costs 1.0 two cases outright,
because simple093 is a `saxonData` case that never runs under 1.0 at all. The
comment in `facet_check.go` records the measurement so it is not retried.

### Unicode category drift (bug 4113)

**22** cases in `MS-Regex2006-07-15`, identical in both versions, all flagged
`queried bug4113` by the W3C: `reJ11`, `reJ13`, `reJ19`, `reJ21`, `reJ23`,
`reJ25`, `reJ29`, `reJ31`, `reJ33`, `reJ35`, `reJ61`, `reJ69`, `reJ75`, `reJ77`,
`reL98`, `reL99`, `reM98`, `reN99`, `reS21`, `reS42`, `reT63`, `reT84`. (This
entry said 18 and the ceiling section below said nineteen; both were stale. The
list is enumerated here so the next re-measurement can diff it rather than
re-count.) They are two thirds of every disagreement the suite reports against
this engine — 22 of 33 on 1.0 and 22 of 34 on 1.1.

These assert that `\p{Lu}` rejects characters that *are* uppercase letters in
current Unicode. The suite was written against Unicode 3.1; the codepoints in
question — U+1D7A8 among them — were categorised differently then. Matching the
suite would mean shipping a frozen 2001 character database.

### `particlesZ033_g` is a 1.0 verdict scored against a 1.1 run

`MS-Particles/particlesZ033_g` — 1 schema false accept, 1.1 only. Previously
filed under *Needs an engine change*; measurement moved it here.

The earlier diagnosis in this file was wrong on its central point, and the
correction is the useful part. It reasoned that since XSD 1.1 switches
element-against-wildcard competition off, the 1.1 rejection had to come from an
element-against-element pair, and named two candidates — the two `e2`
declarations at different nesting levels, and `ref='m1'` against `ref='head'`
where `m1` substitutes for `head` — both of which it believed `counterForces`
was suppressing, so it pointed a fix at `exitBlocked`.

Enumerating every state of every content model in the schema shows otherwise.
Under 1.1 this schema has **no competing pair at all**, suppressed or not.
`counterForces` is never consulted, and `exitBlocked` never sees the model. The
two named candidates do not arise:

- The `e2` positions never share a state. The inner `<sequence minOccurs="56">`
  has to be re-entered or completed, so the outer `e2` is only ever reachable
  from `follow` sets the inner one is absent from.
- `m1`-against-`head` is exactly what `particlesZ033_e` and `_f` write, and both
  are correctly rejected in *both* versions on that pair. `_g` is the variant in
  which the author replaced it: the inner choice became `m3` against
  `ref='head'`, and `m3` is a fresh name that overlaps nothing. The remaining
  `ref='m1'` sits behind `<element e3 minOccurs="2">` in a sequence, so it is
  not in the model's `first` set and never meets `head`.

What is left is the single pair `ref='m1'` against `<xsd:any/>`, which is what
rejects the schema under 1.0. `XSD1_1TestCategories.xml` names that relaxation
outright — "Relaxation of UPA: wildcard/element competition no longer violates
UPA" — and `s3_10_1v04s` through `s3_10_1ii09s` are written to depend on it. So
under 1.1 the model is unambiguous by the letter of the rule this suite states,
and accepting it is right.

The verdict scored against it is a 1.0 verdict. The group carries no `version`
attribute, its `documentationReference` points at the 2004 XSD 1.0 REC, and its
one bare `<expected validity="invalid"/>` is therefore inherited by the 1.1 run
under `appliesAND`. This is the same species of defect as
`ibmMeta/wildcard.testSet` above: a test in the wrong bucket, not a missing rule.

**The measurement, so it is not retried.** Restoring element-against-wildcard
competition under 1.1 gains this one case and costs seventeen valid schemas,
taking the 1.1 total from 41536 to 41494. The false rejects it creates are
`addB153`, `all006`, `wild030`, `wild047`, `wild049`, `wild050`, `wild052`,
`wild072`, `wild073`, `s3_3_6v01`, `s3_3_6v04`, `s3_8_6v01`, `s3_8_6ii01`,
`s3_4_6v01`, `s3_4_6v04`, `s3_10_1v04` and `ste110` — that is, the whole family
of groups whose subject *is* the relaxation. No narrower rule separates them:
`_g`'s pair is a counted element against a following wildcard, and `wild047`'s
is the same shape. One case for seventeen is the wrong direction.

Accepting it is also not a soundness hazard. The runtime in `nfa.go` is a subset
construction over the counter state, exploring every position in parallel, so an
element-against-wildcard choice UPA no longer objects to is still resolved
correctly at validation time. `particlesZ033_g` produces no instance
disagreement, only the schema one.

**On the history.** `docs/conformance-gaps.md` records this case being settled
as unfixable in round 3, then *reopened* on the ground that it "was called a
suite defect without a reading of the rule it turns on". That reading is now on
record above: the rule is the 1.1 wildcard relaxation the suite itself states as
a feature category, and the cost of not applying it is seventeen valid schemas.
The reopening was the right call and the question it asked is answered.

One negative result from the earlier investigation stands and is worth keeping:
this is **not** a budget decline. Every give-up path in `upa.go` and
`assemble.go` — the state-width cap, the pair-test cap, `compileContentModel`,
and the substitution-closure cap — returns an error wrapping
`xdm.ErrResourceLimit` rather than accepting. The huge occurrence values here
never inflate the automaton, because occurrences are runtime counts on a counter
automaton and not states. The cannot-decide invariant holds.

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

### An optional all group is a disjunction, not a scaled budget — closed

*Constraint, and a retraction of the entry that stood here.* This was filed as
`MS-ModelGroups/mgO029` failing (schema, 1.1). It no longer fails, and the
attempt this entry warned against was not the one that landed.

`allSubsumes` reads a base `<all>` group as a per-name occurrence budget. The
original bug was that it ignored the *group's* own `minOccurs` while the derived
side folded its group's range into its branch counts, so the two sides
disagreed about the same group and mgO029 — whose base and derived are spelled
identically, both `<all minOccurs="0">` around a required element — was refused
as an invalid restriction of itself.

The entry recorded that scaling each budget by the base group's range fixes
mgO029 and breaks `particlesK006`, whose documentation states the distinction:

> B's minOccurs=0, B's maxOccurs=absent, but the element has min=max=1,
> R's minOccurs=0, R's maxOccurs=1 — expected **invalid**

and prescribed reading the base as *(empty) | (every budget met)*, two
alternatives checked separately. That prescription was right, and it is what is
now implemented — but the entry named only half of it.

**The half it missed.** An intermediate fix zeroed the budget floors only when
the derived side was also a group (`b.MinOccurs == 0 && rIsGroup`). That is a
discriminator on *shape*, not on language, and it scored mgO029 and K006
correctly for the wrong reason. It admits a schema neither suite case covers:

```xml
<!-- B: {} | {a1,a2} — never {a1} alone -->
<xsd:all minOccurs="0"><xsd:element name="a1"/><xsd:element name="a2"/></xsd:all>
<!-- R: admits {a1} alone, which B forbids -->
<xsd:all minOccurs="0"><xsd:element name="a1"/><xsd:element name="a2" minOccurs="0"/></xsd:all>
```

Flattening each name to `0..1` cannot express the coupling between two required
members, so this was accepted — a false **accept**, the dangerous direction,
and invisible to the suite.

**What the two alternatives actually require.** Both sides are disjunctions,
and both had to be forked:

- *Base.* Floors stay as the members spell them.
  `branchFitsSkippableBudget` charges a branch producing nothing to the skip
  alternative and every other branch to the full match. A branch that straddles
  — K006's `a1` at `0..1`, neither certainly empty nor certainly a full match —
  fits neither and is rejected.
- *Derived.* `allBranchCounts` scaled a skippable group by `0..max`, which is
  the same flattening on the other side. It now forks into an empty branch plus
  the branches of a match with floor 1. Without this mgO029 still fails: R's own
  group straddles.

Fixing only the base side reintroduces mgO029; fixing only the derived side
reintroduces the coupling false accept. Both halves are guarded independently in
`xsd/allgroup_disjunction_test.go`.

**Measured.** XSD 1.0 total agree 39349, XSD 1.1 total agree 41536 — unchanged,
with the disagreement sets byte-identical before and after. Vendored corpora
unchanged at 185 loaded / 38 failed / 7 excluded. The gain is a false accept
closed that no suite case reaches, which is why it costs nothing on the marks.

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

The general fix landed later: the seven PSVI properties are now carried by two
named operations on `xdm.Node` -- `CopyTypingFrom` and
`CopyTypingStrippedFrom`/`StripTyping` -- rather than by a hand-written field
list at each of nine copy sites. `xslt/typingcopy_test.go` pins the preserving
and stripping halves against a schema pair that redefines the same QName, which
is the only arrangement in which the loss is observable. See CHANGELOG.md.

A later audit of every site that copies a node found a tenth, and it was found
the same way the original was: by counting arrivals rather than by reading.
`copyAnnotationTree` in `xslt/validate.go` carries an assessment BACK, from the
document `xsl:result-document` validated onto the nodes the result actually
records, and it still went through `SetTypeAnnotation` — which carries the name
and re-derives is-id from it, leaving `UnionMember`, `DerivedPrimitive` and
`ListItem` behind on the copy that is then thrown away. A probe on that line
measured three arrivals per transform, every one of them carrying resolved
typing the destination did not receive. It is now `CopyTypingFrom`, and
`TestResultDocumentCarriesResolvedTyping` pins it.

The direction of that copy is why reading missed it. Every other site copies
FROM the tree the caller holds; this one copies from a tree the engine built
and is about to discard, so it does not look like a copy site at all until the
question is asked as "what arrives here, carrying what?".

That audit also asked whether *seven* is still the whole set, since a property
added to `xdm.Node` and never added to the operations would be dropped by all
ten sites at once. It is: `xdm/typing_test.go` censuses `Node`'s exported
fields against the PSVI list and against an explicit list of the fields that
are deliberately excluded, so a new field is neither absorbed nor exempted
silently. `DocumentURI` is the one that looks like it belongs and does not — it
is the URI a document was RETRIEVED BY, and a copy was not retrieved.

`validation-0201` still fails, on indent width alone — recorded as
implementation-defined in `docs/conformance-gaps.md` — so this costs and gains
no suite case, and `xslt/unionmember_test.go` is what pins it instead.

## Open

Real gaps, together with the constraints and retractions that bound how they
may be closed. Ordered by how much they cost. Entries marked *closed* or *not a
defect* are kept here rather than collapsed into *Fixed* because their bodies
are the argument that bounds a neighbouring gap; the one-line records of
everything else that closed are under *Fixed*.

### The `dtd` package cannot enforce XML §4.3.4

`xdm` checks an external entity's declared version against the including
document's: a 1.0 document may not include a 1.1 entity, an unrecognised
version is refused rather than assumed compatible, and a table whose version
was never determined enforces the stricter 1.0 rule. `dtd` does none of this,
and the reason is structural rather than an oversight.

`dtd.Load` takes a DOCTYPE *directive string*, not a document. It therefore
never sees an XML declaration and has nothing to compare an entity's version
against — its own `stripTextDecl` discards the text declaration for exactly
the reason `xdm`'s once did. Closing it means giving `dtd` a way to be told
the including document's version, which is a new API surface, not a gap
closure. **It should wait for a caller that needs it**: inventing the
parameter now would fix the shape of something no test exercises.

Nothing in the suites scores this. The `XmlVersions` cases reach the parser
through `xdm`, which is checked; a caller reaching `dtd.Load` directly is the
uncovered path.

### DocBook 5.0's XSD refuses to load (XSD) — not a defect, DocBook's schema is invalid

`tests/corpora walk testdata/xslt30-test` reports 39 failures, 38 of them the
DocBook 5.0 XSD under
`tests/misc/docbook/docbook-xsl-1.79.1/slides/schema/xsd/`. Every one of the 38
fails identically — the walk loads each file on its own and each includes the
same `pool.xsd`, so it is one fault counted 38 times — with 568 errors: 281
`cos-element-consistent` and 287 `cos-nonambig`.

Refusing a schema this widely deployed is strong evidence of a bug here, so it
was investigated as one. It is not. All three error clusters are genuine
violations of §3.8.6, and the W3C suite states each outright.

The whole fault reduces to seventeen lines. `db.indexterm` (`index.xsd`) is a
`<xs:choice>` of three named groups, each declaring a **local** element
`indexterm` with a **different** anonymous type; `db.firstterm`/`db._firstterm`
(`glossary.xsd`) and the five `info` declarations (`pool.xsd`) are the same
shape:

```xml
<xs:group name="a"><xs:sequence><xs:element name="x" type="xs:string"/></xs:sequence></xs:group>
<xs:group name="b"><xs:sequence><xs:element name="x" type="xs:int"/></xs:sequence></xs:group>
<xs:choice><xs:group ref="a"/><xs:group ref="b"/></xs:choice>
```

That is `msData/modelGroups/mgR022.xsd` almost verbatim, and `mgR002` is the
inlined form. All 22 of `mgR001..mgR022` carry `<expected validity="invalid"/>`
with `status="accepted"`, under the documentation *"2 particles with idendical
element declarations (different type)"* (sic). The `mgQ` series is the control:
`mgQ003` — the same model with the second `e1` given the **same** type — is
expected **valid**, and we accept it. The pair differs only in the type, which
is exactly the distinction `checkElementDeclarationsConsistent` draws.

The suspicious-looking `cos-nonambig` message that names one QName against
itself — *"element firstterm and element firstterm can both match the same
element"* — is likewise correct. `mgS002..mgS005` and `mgQ001`/`mgQ021` produce
that same message shape here and are all expected invalid; `mgQ021` is two
particles for the same name with the **same** type, still invalid under UPA.
So the reading behind `CheckOptions.LaxUPA` is not merely off by default, it
would be wrong as a default: loading DocBook with `LaxUPA` set moves 568 errors
to 567. These are distinct `*ElementDecl`s with distinct types, not one
declaration seen twice — the genuinely-same-declaration case
(`<xs:element ref=>` twice in a sequence) already loads clean.

The nine wildcard errors — *"element abstract and wildcard ##any can both match
the same element"* — come from `db._any`, a bare `<xs:any processContents="skip"/>`
sitting in a `<xs:choice>` beside named element refs (`pool.xsd`). That is a
UPA violation in 1.0 and not in 1.1, but these files carry no `vc:minVersion`
and no `version="1.1"`: they are 1.0 schemas, and 1.0 is the right rule for
them. Loading them as 1.1 would be answering a different question.

The cause is visible in DocBook's own tree. The **normative** DocBook schema is
RELAX NG, and `relaxng/index.rng` defines `db.indexterm` as a `<choice>` of
three `<element name="indexterm">` patterns distinguished only by the value of a
required `class` attribute (`singular`/`startofrange`/`endofrange`). RELAX NG
resolves that by inspecting the attribute. §3.8.6 requires the particle be
determined *"without examining the content or attributes of that item"*, which
forbids precisely this. The XSD files are a lossy machine translation of a
construct XSD cannot express — which is why DocBook ships RELAX NG as normative.

Nothing was changed. A search over all 11,060 expected-valid schemaTests in the
suite found no case with two same-named local declarations of differing types in
one content model, so there is no counterexample to the current behaviour; and
none of the five schema-level disagreements in `xsdtests` mentions `cos-nonambig`
or `cos-element-consistent` in either direction. Relaxing either check to make
DocBook load would introduce a false accept against `mgR002` and `mgQ021`
directly.

### Schema-validity rules not yet implemented (XSD)

**1 schema false accept in 1.0, 4 in 1.1** — invalid schemas this loads
without complaint. Two of the rules listed here as open have since been
implemented; their rows are kept below, marked fixed, because the reason each
was hard to see is the useful part. The figures that stood here before (195 and 305, with a
nine-row cluster table) were stale by two orders of magnitude: they were
measured before the bulk of these rules landed, and the cluster table described
a distribution that no longer exists. There is no cluster left to tabulate. At
this count the remaining cases are named individually.

| Case | Versions | Constraint | Verdict |
|---|---|---|---|
| `MS-Element/elemM002` | 1.0, 1.1 | `type="foo"` names an *attribute* declaration | **fixed** — see below |
| `MS-IdentityConstraint/idC019` | 1.0, 1.1 | `keyref` resolving `refer` across a namespace the declaring document never imported | **fixed** — see below |
| `MS-DataTypes/anyURI_b006_1356` | 1.0 only | RFC 2396 excluded characters in an `anyURI` enumeration | won't fix — see below |
| `Simple/simple093` | 1.1 only | `xs:NOTATION` as a union member type | won't fix — see below |
| `MS-Element/elemZ026` | 1.1 only | a 1.0 substitution-group rewrite XSD 1.1 deleted | won't fix — see below |
| `MS-Particles/particlesZ026a` | 1.1 only | same, plus a validity the W3C never settled | won't fix — see below |
| `MS-Particles/particlesZ033_g` | 1.1 only | a 1.0-era `invalid` verdict inherited by the 1.1 run | won't fix — see below |

### Two rules that were open after all (elemM002, idC019)

Both were **false accepts** — invalid schemas loading clean — and both are
fixed. They are recorded together because the reason each was invisible is the
same shape: a deliberate, correct piece of latitude, applied one step too
widely.

**`MS-Element/elemM002`.** `<xsd:element name="myElem" type="foo"/>` sits
beside `<xsd:attribute name="foo"/>`. §3.3.2 requires `type=` to resolve to a
*type definition*, and this one resolves to a component of the wrong kind.

What hid it is the deferral §3.3.3 grants an element declaration, implemented
in `deferrableMiss`: a missing type matters only where the declaration is used,
so an unresolved name may be carried on the component instead of reported. The
unprefixed `type="foo"` lands in the absent namespace, the schema declares
components there, and `deferrableMiss` therefore answered true. But the
deferral exists because a document read *later* might supply the type, and no
document can turn an attribute declaration into a type definition. The miss is
final at the moment it is made. `resolveTypeRefLazy` now reports a name the
assembly defines as something other than a type before it consults the
deferral at all.

The case that keeps this honest is `saxonData Missing/missing001`. It writes
`type="absent"` into the absent namespace of a schema that declares components
there — identical to elemM002 in every respect `deferrableMiss` can see — but
`absent` names *nothing at all*, so nothing contradicts the deferral and the
schema still loads, as the suite expects. A check that rejected both would not
have implemented the rule; it would have broken the deferral.

**`MS-IdentityConstraint/idC019`.** `schema.identityConstraints` is one flat
map over the whole assembly, so a `refer=` lookup that ignores *which document
asked* can cross a namespace boundary the asking document never imported.
§4.2.6.1 `src-resolve` scopes the licence an `<xs:import>` grants to the
document that wrote it — which is exactly why `doc.imports` already existed
beside the per-assembly `importedNamespaces`. The `refer=` fixup was not
consulting it.

idC019 imports `idC017a.xsd`, whose `targetNamespace` is `diffNS` and whose
`keyref` writes `refer="keyName"` **unprefixed**, with no default namespace in
scope. §3.11.2 resolves that to the *absent* namespace, not to the target one —
the distinction that trips readers of every QName-valued attribute in XSD.
Nothing in `diffNS` declares `keyName`; the match came from the importing
document's own absent namespace, which `idC017a.xsd` does not import. The
suite's own annotation states the rule: "TSTF agreed that an un-imported NS
used in a QName is a schema error."

`resolveQName` already runs `checkReferenceImported` for `refer=`, and it still
cannot catch this one: an unprefixed name with no default namespace in scope
returns early through `chameleonQName`, before the switch that calls the check
is reached. So the test belongs at the fixup, which now captures its declaring
document and requires the key's namespace to be that document's own target
namespace or one it imports.

The negative arm is the ordinary case and the common one — a `keyref` naming a
key in its own target namespace — plus one naming a key across a namespace the
document *does* import. Dropping the `imports` half of the test breaks the
second, which is what makes it part of the rule rather than decoration.

Both fixes are covered in `xsd/falseaccept_test.go`, each with the negative arm
beside it. Measured in an isolated worktree, the two together move XSD 1.0
schema agreement 14,383 → **14,385** (disagreements 5 → 3) and 1.1 15,347 →
**15,349** (7 → 5), with both instance lanes unchanged case for case, no new
disagreement on either version, and the vendored-schema corpus steady at 185
loaded.

Four of these are not gaps to be closed. `anyURI_b006` and `simple093` are
cases where the suite contradicts itself, and enforcing either rule costs more
cases than it buys. `particlesZ033_g` is a 1.0-era expectation the 1.1 run
inherits, and the only rule that would reject it is the one XSD 1.1 deliberately
removed — all three are written up under *Won't fix* below. `elemZ026` and
`particlesZ026a` share one cause, described under *Deciding is not the same as
declining* below.

These are unwritten rules rather than broken ones: each fails to reject a
schema that should be rejected. None affects a *valid* schema, which is why the
false-reject count is smaller still — 2 in 1.0 and 1 in 1.1, all of them
pattern cases unrelated to schema validity.

Adding rules here is the highest-yield remaining work and also the riskiest: a
rule stricter than the spec starts rejecting real schemas the suite never
covers. Every change must be measured against both suite directions *and* the
production corpora (65 UBL + 427 CII), which are the strongest guard against
over-strictness — and, where those are absent, against the 230 real-world
schemas vendored in `testdata/` (see below).

**A note on that guard when the corpora are absent.** UBL and CII are licensed
and unvendored, so a checkout without `GOXSLT_UBL`/`GOXSLT_CII` cannot run
them, and DocBook and XSpec exercise the XSLT engine rather than the schema
loader. Three things stand in when they are missing, and it is worth stating
all three, because it is easy to conclude there is no guard at all.

The first is now a standing check rather than a fallback. 230 real-world
`.xsd` files ship in `testdata/` as fixtures for the XSLT and XQuery suites,
and `tests/check.sh` loads each on its own in every run, fast mode included,
ratcheted as `VendoredSchemas` — 185 load, 38 are DocBook 5.0's genuinely
invalid schema (above), 7 are excluded as fragments or deliberately invalid
test data, each named in `vendoredExclude`. A rule that starts rejecting one
of the 185 fails the build and says so. What this does **not** do is replace
UBL and CII: these are mostly test fixtures, documentation schemas and
namespace vocabularies, so they are thinner exactly where the corpora are
thick — the deep industry vocabularies with long derivation chains, large
substitution groups and heavy `xs:union`/`xs:key` use that UBL's 65 and CII's
427 exercise. It catches the over-strict rule that breaks *any* real schema;
it does not catch the one that breaks only commercial ones.

The second is the suite population: roughly 16,000 schemas are labelled
*valid*, and a rule that over-rejects turns one of those into a false reject,
which the harness counts directly. "False rejects did not rise" over that
population is a real over-strictness signal — weaker than the corpora on the
shapes production schemas favour and no substitute for them, but far from
nothing. Pairing each new rule with a valid schema that must still load, in
`xsd/falseaccept_test.go`, is the third.

### The range comparison XSD 1.1 deleted (elemZ026, particlesZ026a)

`MS-Element/elemZ026` and `MS-Particles/particlesZ026a` — 2 schema false
accepts, 1.1 only. Both are now believed to be **suite artifacts, not gaps**.
This entry previously proposed a fix; that fix was implemented and measured, it
made conformance worse, and the spec says it was never the rule. What follows
replaces it.

**What the schemas do.** elemZ026's disagreeing site is not the inner type at
all. Its `restrictedBasicBitType` narrows `maxOccurs` from `unbounded` to `1`,
and both versions accept that — narrowing is what a restriction is *for*. The
divergence is one level out, at `restrictedBasicBitContainerType`: the base
names a substitution-group head with `maxOccurs="unbounded"`, the derived names
a concrete member of that group, also unbounded.

**Why 1.0 rejects it.** Clause 2.1 of Particle Valid (Restriction) rewrites an
element particle whose declaration heads a substitution group into a *choice*
over the members. `asSubstitutionChoice` implements that faithfully: the choice
keeps the original particle's range and each member gets unit occurrence. So
the base becomes `(mem{1,1}){1,unbounded}` and Elt:Elt compares the derived
`{1,unbounded}` against a member's `{1,1}`. Occurrence Range OK fails, and the
error surfaces as "an element declaration is not one of the base's
alternatives".

**Why 1.1 accepts it, correctly.** `(a{1,1}){1,unbounded}` and `a{1,unbounded}`
are the *same language*. The 1.0 rejection is a table artifact — a pairwise
bound comparison standing in for an inclusion the table cannot compute — and it
is exactly the class of artifact the 1.1 relaxation exists to remove.
`particleSubsumes` decides this pair by language inclusion and returns
"included", which is the right answer.

**The spec settles it.** XSD 1.1 Part 1 has no Particle Valid (Restriction) and
no Occurrence Range OK. §3.9.6 retains only Particle Correct, Particle Valid
(Extension) and Particle Emptiable; Appendix B.4's constraint index lists
`cos-particle-extend` with no restriction counterpart, and `range-ok` does not
appear anywhere in the document. §3.4.6.4 (`cos-content-act-restrict`) is two
clauses, and clause 1 is the whole content-model test: "Every sequence of
element information items which is ·locally valid· with respect to R is also
·locally valid· with respect to B." The substitution-group-as-choice rewrite is
likewise absent — under 1.1 a substitution group enters restriction checking
only through ·locally valid·, because an element particle's language already
contains its substitutable members. There is no occurrence clause to restore to
`declCompatible`, because 1.1 deleted the constraint that clause would express.

**The named fix, measured.** Threading each step's particle through `stepNFA`
into `declCompatible` and applying `occurrenceRangeOK` was implemented in full.
It flips elemZ026 to `invalid` as predicted — and it also re-rejects
`particlesHa161` and `particlesZ001`, both marked `accepted` by the suite and
both documented elsewhere in this file as 1.1 false rejects the subsumption
engine was *built to fix*. XSD11 fell 41536 → 41534. It is a net loss, and
`particlesZ026a` does not settle either way. The fix is not narrower than the
rejected alternative; it is the same 1.0 artifact reintroduced through a
different door.

The reason it fires at all is narrow and accidental. When the head is abstract,
`asSubstitutionChoice` omits it and `stepNFA` sees a single declaration on the
base side, so `declCompatible`'s clauses run. When the head is concrete,
`stepNFA` finds two declarations for the same name, sets `multiple` and returns
none, so the range would never be compared. A rule that depends on whether the
base's head happens to be abstract is not Occurrence Range OK under any reading.

**What the suite says.** elemZ026's `<expected validity="invalid"/>` carries
`status="queried"` against W3C bug 4146, opened by Michael Kay in 2007: "the
metadata describes the schema as invalid, but it contains no obvious error. XSV
reports it as valid." The bug is still `NEW`, keyworded `disputedTest`, and its
whiteboard records an intent to fork a separate 1.1 test that was never done.
particlesZ026a is weaker still — the TSTF concluded its validity was
"implementation-determined" and the WG never decided, which the test's own
annotation says in as many words.

So both cases are counted against us by a version-unqualified expectation
written for 1.0 and disputed ever since. Accepting them under 1.1 is what
§3.4.6.4 clause 1 requires. `xsd/subsume_occurs_test.go` pins that in both
directions: the two substitution-group restrictions that must keep being
accepted, and the genuine widenings — `maxOccurs` 2→3, and bounded→unbounded —
that language inclusion still rejects without ever comparing a bound to a
bound. Closing these two would mean re-adopting a 1.0 rule at a cost of two
schemas that really are valid.

### Restriction of an all group by a wildcard or a named group (XSD 1.1) — closed

`All/all206`, `all218`, `all237`, `Wild/wild049`, `wild050` were recorded here
as 5 schema false rejects. All five load clean under 1.1. The entry outlived the
fix: `restrict.go` carries named handling for each of the five shapes — a
wildcard inside a base all group, a named model group merged into one, and the
two-branch containment `wild050` needs.

Their XSD 1.0 rejections are **correct** and are not gaps: 1.0's
`cos-all-limited.1` genuinely forbids a non-element particle in an all group,
and `wild049`/`wild050` also spell `notQName`, which is 1.1-only.

The `all244` caveat — that a wildcard's occurrences do not split between the
names it spans by a simple count — was the real constraint, and it was honoured
rather than worked around. `all244.n` is a negative test and is still rejected,
with `the base requires a wildcard, which the restriction omits`. That pairing
is the load-bearing part: the five valid schemas are accepted without the
invalid twin becoming accepted with them, so completeness was gained without
trading soundness for it.

**Measured** at `a8dee9a`: XSD 1.0 total agree 39355, XSD 1.1 total agree 41542
— equal to `tests/ratchet.txt`, with one schema false reject left on 1.1
(`ste110`) and none of it from this family.

All six shapes are now pinned in `xsd/allgroup_wildcard_test.go`, the five valid
ones beside `all244.n`. The pairing is the point: the conformance total says
only that a number moved, never which shape moved it, and a relaxation that
recovered the five by loosening the wildcard-occurrence rule would take
`all244.n` with them. The 1.0 lane is pinned in the same file, since
`cos-all-limited.1` must keep rejecting all six there.

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
  could not catch a large circular type. `countDigits` in `xsd/facet.go` is the
  same shape outside a graph walk: it expanded a decimal one digit at a time,
  stopped at 4096, and returned the short count, so a value with 4600 fraction
  digits satisfied `fractionDigits="4500"`. The scale now comes from factoring
  the denominator as `2^a * 5^b`, which is exact and has nothing to exhaust.
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
and leaves the same bug waiting at a depth nobody will test. `maxDecimalScale`
is the case that proves it: capped at 18 it printed a 360-digit decimal as `0`,
was raised to 1024, and printed `1/10^5000` as `0` for exactly the same reason.
Raising it a third time would have been the same move again — it is now gone,
the scale following the value. The arbitrariness
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

### Instance validation gaps (XSD)

This section used to list 25 instance false accepts, named case by case. That
list is gone: every case on it — `Simple/simple001`, `simple002`, `simple016`,
`simple086`, `ElemDecl/typeDef012*`, `valueConstraint007*`,
`MS-ComplexType/ctZ013c`/`-d`/`-e`, `MS-IdentityConstraint/idG006`, `idK012`,
`suntest/idc006.nogen`, `XmlVersions/xv009`, `MS-Schema/schU4`, `schU5`,
`MS-Additional/isDefault070`, `isDefault077`, `MS-SimpleType/stE054`,
`MS-Regex/reK6`, `Complex/complex022`, `CTA/cta0006` — was re-measured and
none of them still disagrees, in either version. `xv009` closed with the
XML 1.1 parser fork under `internal/xmlfork`; the rest closed with the
identity-constraint and value-constraint work recorded above.

What remains after re-measuring is three cases, and only one was addressable:

- `MS-Wildcards/wildZ010` — **fixed.** `namespace=""` was defaulted to
  `##any`, so a wildcard that admits *nothing* admitted *everything*. §3.10.2
  defaults only an **absent** `namespace`; a present empty value is an
  `xs:namespaceList` with no members, which is the empty set. The TSTF ruling
  on bug 4066 says the same — "no defaulting of the empty string to ##any is
  licensed by the spec" — and the case is `status="stable"`, not disputed.
  Worth +1 on each version.
- `MS-IdentityConstraint/idZ015` — a field selecting an attribute matched by a
  `lax`/`skip` `anyAttribute`. Open under W3C bug 4063, and left alone.
- `MS-Attribute/attP031` — the one remaining false *reject*, declined on
  purpose; the reasoning is under *XSD instance: 1 addressable false reject*
  below, and has not changed.

The lesson is the one this file keeps relearning: a case list is a measurement,
and it decays. These entries survived several rounds after the bugs behind them
were already fixed.

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

XML 1.1 sat outside all of this until the character layer was implemented. The
gap was never that 1.1 documents were refused — they parsed — but that
`version="1.1"` was rewritten to `1.0` and the document then read under the
wrong language's rules. That is fixed: the version reaches the tokeniser, and
[2] `Char`, [2a] `RestrictedChar` and the §2.11 line ends now follow it. The
four `XmlVersions` instances that carry C0 controls as character references
(xv003, xv006, xv008, xv009) parse and are scored, where they were previously
unreadable. What is still missing is in the external-entity and DTD layers,
and is described in
[todo.md](todo.md#11-xml-11-documents--character-rules-done-dtd-side-rules-outstanding).

---


## What 100% would take

Measured at `6fa4150` with both suites present. The short answer: **XPath now
reaches 100% at all three versions, and XSD cannot reach 100% at all** — part
of the remaining gap is the suite disagreeing with itself.

### The ceiling that is not ours

Re-measured at `a8dee9a`. The table that stood here read 51 and 47
disagreements against 45 and 44 disputed; both columns had drifted down as
fixes landed and were never re-derived.

| | XSD 1.0 | XSD 1.1 |
|---|---:|---:|
| disagreements | **33** | **34** |
| of those, W3C-flagged `queried` or tied to an open bug | **31** | **32** |
| left carrying suite status `accepted` | **2** | **2** |

Those first are cases where the W3C's own metadata records a dispute about the
expected result. **Twenty-two of them are one cause** in each version: bug 4113,
the `\p{Lu}`, `\p{Ll}` and `\p{Lo}` tests, written against Unicode 3.1 before
characters such as U+1D7A8 moved between general categories. Passing them means
freezing a Unicode 3.1 table and being wrong about modern text. **They are a
reason to stop short of 100%, not a defect to fix.** (This section said
nineteen and the *Unicode category drift* entry above said 18; the enumerated
list is now kept there.)

The four remaining `accepted` cases are named, and each is argued in this file:
`attP031` and `particlesZ001` on 1.0, `particlesZ033_g` and `simple093` on 1.1.
Every one of the four is a suite self-contradiction the relevant entry sets out
— which is why the addressable column is, in substance, empty on both versions.

So the ceiling is **99.99% on either version**, and the engine stands at
**99.92% on both** as the driver reports it. Those figures rose without any
behaviour changing, when the driver stopped scoring the suite's `indeterminate`
expectations — 16 cases on 1.0 and 14 on 1.1 prescribe no result — and again
when it moved the four 1.1-syntax `ibmMeta` groups out of scope.

### XPath: no failures; one case refused by default until the harness enabled it

`fn-matches-51` names a group whose width can vary *and* places the
backreference mid-pattern. Under the default engine both are refused: RE2
returns a single submatch assignment, so for a variable-width group the split
it reports may not be the one that matches, and a comparison against it would
answer confidently and wrongly.

It passes with `xpath.SetBacktrackingRegex(true)`, which takes QT3 to
15,222 of 15,222. That figure is not the headline one, because the switch is
off by default and the headline number reports the default configuration.

Eleven of the twelve backreference cases that used to sit here are fixed. When
every named group has a **fixed** width the greedy assignment is the only
assignment, so comparison is exact and stays linear — no backtracking engine,
and the DoS class [security.md](security.md) keeps out stays out. The full
reasoning is under *Regular expression backreferences* above.

**Closing the last one would cost the linear-time guarantee**, which is a worse
trade than the case is worth.

### XSD schema-validity: 3 (1.0) and 6 (1.1) that are ours

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

What remains is short enough to name. `elemM002` (queried, bug 29085) and
`idC019` (bug 4057) stood on both versions and are now **fixed** — written up
under *Two rules that were open after all* above. That leaves 1.0 with
`anyURI_b006_1356` (bug 4048), and 1.1 with `elemZ026` (bug 4146),
`particlesZ026a` (bug 4071), `particlesZ033_g` and `simple093` — the last two
the only ones the suite marks `accepted`, and `simple093` is argued under
*Suite cases that should be read as disputed* below, where enforcing its rule
costs `particlesZ007`.

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

### XSD schema-validity: 2 (1.0) and 1 (1.1) addressable false rejects

The ones that matter, because a false reject breaks a working caller. Both
versions carry `ste110` (queried, bug 4957); 1.0 adds `particlesZ001`, a
Particle Valid (Restriction) case argued under *Suite cases that should be
read as disputed* below. The 1.1 figure was 11 until the suite harness
began loading schemas with `AllowDOCTYPE` set: `iri-001` and its ten masked
instance cases were refused for wanting a DOCTYPE, not for anything the
validator decided.

One attempt is recorded above as reverted: carrying the element's occurrence
range onto `recurseAsIfGroup`'s wrapper fixes `particlesZ001`, `Z023` and
`Z024` and costs about eleven false accepts for each — the wrapper's range
serves two jobs that want opposite answers. A correct fix separates them, which
is a change to `effectiveTotalRange`'s contract.

Three cases that once stood here under 1.1 — `particlesHb008`,
`particlesHb011` and `particlesZ028` — need XSD 1.1's §3.4.6.4 intensional
restriction: genuine language inclusion in *both* directions rather than the structural table.
`particlesHb008` restricts `choice{e1, sequence{e2,e3,e4}}` by a reordered
`choice{e1, sequence{e2, choice{e3,e4}}}` that no table can relate. That is an
automaton subsumption engine, not a rule, and two rounds declined it
deliberately rather than ship a partial one.

### XSD instance: 3 (1.0) and 2 (1.1) addressable false rejects

`attP031.i` under 1.0, plus `gMonth002_2061.v` and `gMonth004_2063.v` in both
versions. `particlesZ040.i` stood here too and no longer does; the matcher that
decides it is described under *Why the occurrence counters are a vector and not
a bracket per scope* above.

`attP031` is a suite self-contradiction rather than a defect
here: it declares `use="prohibited"` with a `fixed` value and expects the
instance supplying that value to be *valid*, while `attF001` — structurally
identical but without `fixed` — expects invalid, and both carry status
`accepted`. §3.4.2 gives `{attribute uses}` only the declarations whose `use`
is absent, `optional` or `required`, so a prohibited use creates no attribute
use at all. Making `attP031` pass means treating `fixed` as the discriminator,
which no clause supports.

That relaxation was measured rather than assumed, in a clean checkout so the
figure is attributable: keeping a prohibited use that carries `fixed` takes
XSD 1.0 from 39,345 to 39,346 and leaves 1.1 at 41,532, with no schema-level
change and no false accept introduced — `attF001` still rejects. So it is a
clean +1, and it is declined anyway. The same change makes this validator
accept `att="37"` against a declaration that prohibits the attribute, which is
a deliberate false accept bought for one suite point. A case that passes
without a clause behind it is not a fix.

The other two are disputed rather than addressable: `gMonth002_2061` and
`gMonth004_2063` test the old `--MM--` form under W3C bug 6901. `cta0022` was
in this list and is now fixed — its type alternative's XPath was *raising* rather than answering, and a type alternative whose test
raises is silently skipped, so a crash was indistinguishable from a false
test.

### Suite cases that should be read as disputed

Each carries status `accepted` and each is questionable on the suite's own
evidence. **Most of them no longer cost anything**, and the list is kept for the
argument rather than the arithmetic: re-measured at `a8dee9a`, the only two
still disagreeing are `particlesZ001` (1.0 only) and `simple093` (1.1 only).
The four `notQName` groups are now scored out-of-scope by the driver (see
*`ibmMeta/wildcard.testSet` scored in the wrong lane* under *Fixed*), and
`particlesK006`,
`particlesZ007`, `simple004`, `simple005` and `simple006` all agree in both
versions. The sentence that stood here — "so the addressable counts above
include them" — was true when written and is not now.

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

Re-measured at `a8dee9a`. The two schema rows read 13 and 12 disagreements and
named `iri-001` as addressable; both counts were stale and `iri-001` has passed
since **3f2602e**.

| | now | reachable | what stands in the way |
|---|---|---|---|
| XPath 2.0 | **100.00%** | 100.00% | reached |
| XSD 1.0 instance | **99.89%** | ~99.99% | 28 disagreements, 27 of them W3C-disputed; `attP031.i` is the lone `accepted` one, and it is a suite self-contradiction |
| XSD 1.1 instance | **99.90%** | ~99.99% | 27 disagreements, all 27 W3C-disputed |
| XSD 1.0 schema | **99.97%** | **~99.99%** | 5 disagreements; 4 queried or bug-tied, `particlesZ001` the one `accepted` case and disputed on the suite's own annotation |
| XSD 1.1 schema | **99.95%** | **~99.99%** | 7 disagreements; 5 queried or bug-tied, `particlesZ033_g` and `simple093` the two `accepted` ones, both argued above as suite defects |

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

and today **33 and 34, of which 2 and 2 are addressable** — the intermediate
figures this paragraph carried (95 and 154, of which 46 and 103) were a
snapshot from partway through and were never re-derived. Twenty-two of the
disputes in *each* version are bug 4113 alone. Reaching 99.99% would have meant fixing 200
of the original 201 1.0 schema disagreements and 313 of the 314 in 1.1 —
arithmetically impossible without "fixing" tests the W3C itself questions.

Nothing here is blocked on a missing idea. XPath's last case is a deliberate
refusal, the XSD false accepts are volume rather than difficulty, and the false
rejects are one subsystem that needs its occurrence handling reworked rather
than patched.

**Note that reaching 100% on XSD is not possible and not desirable.** 31 of the
33 1.0 disagreements and 32 of the 34 on 1.1 are cases the W3C's own metadata
records a dispute about; twenty-two in each are the bug 4113 general-category tests,
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
so a value printed as `0` while comparing unequal to it (silent erasure) —
and then capped again at 1024, which moved that same contradiction to
10^-1025 rather than removing it; the scale now follows the value.
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

**`ibmMeta/wildcard.testSet` scored in the wrong lane.** 1.1 cases counted
against the 1.0 run; the driver now scopes them out and counts the exclusion
(harness, not engine). Closed by **3104543**.

**`xsl:sort` through `float64`.** Investigated as a precision defect and
retracted: doubles pre-filter and an exact `Rat.Cmp` decides ties, so the
comparison is sound and stays linear.

**A hyphen after a variable reference.** Reported as a lexing defect and
retracted — `$e-1` is one name, and QT3 writes such names itself.

**Particle restriction edge cases (XSD).** `particlesT002`, `T009` and `Ha161`
closed by **7495485**, `addB183` by **9a6567f** (false rejects). Only
`particlesZ001` remains, recorded under *the occurrence-carrying wrapper*
above.

**A collection URI resolved against the context item.** `fn:collection` now
resolves against the static base URI, with the item's base as the fallback.
The two tests that guarded it both passed when it was reverted — each set only
one of the two bases — so `TestCollectionStaticBaseBeatsItemBase` pins the
distinction.

**Three XPath cases predicted to keep failing.** `fn-doc-available-5`,
`functx-fn-doc-available-1` and `fn-in-scope-prefixes-25` all pass; the
DTD-defaulting blocker was closed by **87d618b**.

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
