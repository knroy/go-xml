# Known gaps

Why the hard gaps are hard. For the current figures and a case-by-case verdict
on what is fixable, see [conformance-gaps.md](conformance-gaps.md); this file
is the diagnosis behind the entries there. Nothing here is aspirational: if a
gap has no entry, it has not been measured.

The file is named for gaps, and it holds only gaps. An entry whose subject has
been fixed is not a gap any more and is deleted, however carefully it was
written; the changelog and `docs/conformance-gaps.md` are where closed work is
recorded. Three kinds of thing survive, and the file is in three sections:

- **Open gaps** — what is failing now. A diagnosed cause, and what a real fix
  would cost where the answer is a rewrite rather than a patch.
- **Deliberate divergences** — behaviour that disagrees with a test on
  purpose, each with the spec clause it rests on and the **measured** cost of
  changing it. These are not gaps, and they are kept because without the number
  someone re-attempts a bad trade.
- **Corrections** — where a verdict recorded in this file was *wrong*. Not
  "this is fixed", but "this file said X and X was false". They sit at the
  bottom, away from the open work, because their whole value is stopping the
  next reader re-deriving a conclusion that has already been disproved.

Durable design rationale that is not a gap — why the occurrence counters are a
vector, why saturation is right for the matcher and wrong for the derivation
checks, why a depth bound is not cycle detection — has moved to
[xsd.md](xsd.md#limits), where the code it constrains is documented.

A note on which direction matters. A **false reject** is valid input refused;
a **false accept** is invalid input allowed. False rejects are the more serious
kind — they break working documents — so they are listed first throughout.

## Where the numbers stand

Current conformance figures live in **[conformance-gaps.md](conformance-gaps.md)**,
which names every failing case and says whether it is fixable, and are re-measured
by `tests/check.sh`. They are deliberately not repeated here: this file explains
*why* the hard gaps are hard, and a percentage copied into two places drifts in one
of them.

For orientation only, and re-derived rather than inherited: XPath 2.0, 3.0 and
3.1 and RELAX NG are at **100%** with no failures at all; XSLT 2.0 has 8
failures of 6,201; XQuery 3.1 has 1 of 30,346; XSLT 3.0 has 28 of 11,518;
XSD 1.0 disagrees on 30 of 39,388 and XSD 1.1 on 31 of 41,598. Everything below
is an account of those 98 cases, or of a decision that produced some of them.
The 98 is the sum of the nine figures above, computed from
[tests/conformance/results.json](../tests/conformance/results.json) rather than
written: `tests/docfigures.sh` re-derives it from `tests/ratchet.txt` and fails
if this file and the generated table disagree.

What this file adds, and that one does not:

- the diagnosed cause behind a gap, rather than its error code;
- what a real fix would cost, where the answer is a rewrite rather than a patch;
- the measured price of each deliberate divergence, so it is not re-attempted;
- DTD and XDM, which have no public suite and so appear in no percentage.

---

## Open gaps

Real gaps: a genuine bug or an unimplemented rule, failing now. Ordered by how
much each costs.

### §19.8 streamability analysis is partially implemented (XSLT 3.0)

**11 of the 31 XSLT 3.0 failures**, and still the largest single gap in the
project — down from 14 since the §19.8.5 streaming-parameter table was
corrected. `varPosture` read *grounded* where §19.8.5.2 and §19.8.5.3 say
*striding*, so a function whose body returned its streaming parameter — a node,
which in a streamed tree is never grounded — cleared the "must be grounded"
rule its own category imposes. Four cases moved; `su-ascent-903` moved the
other way and is now adjudicated individually, since §19.8.5.7 makes an ascent
function's streaming parameter *climbing* and the ascent category permits a
climbing body. The posture-and-sweep lattice exists, and so now do the rules built on
it: the §19.8.4 instruction rules, §19.8.5 streamable stylesheet functions,
§18.2.8 accumulators, the §19.8.8 expression rules, the §19.8.9 function
classifications, §19.6's context posture for both
`xsl:source-document`/`xsl:stream` and the template rules of a streamable mode,
and §18.1's grounded demand at both the sites it names — an `xsl:stream` body
and, by its own parenthetical, a streamable template rule.
What remains is a long tail of individual constructs rather than a missing body
of rules.

Every one of the 14 fails in the same direction: the suite expects `XTSE3430` —
*this construct is not guaranteed streamable* — and the transform succeeds
instead, because the analysis returns `known=false` for a construct it cannot
yet model and correctly declines to raise an error it has not proved.

That direction is the whole diagnosis. The engine builds a tree and streams
nothing, so every construct the analysis would reject is one it simply
executes. It produces the **right answer** for all 37; what it does not
produce is the static refusal §19.8 requires a streaming processor to make
before running anything. A construct that is not guaranteed streamable is still
a construct with a well-defined result, and a tree-building processor reaches
it. So these are not wrong answers, and they are not silent erasure: they are a
static analysis that is not yet complete.

They cluster by construct rather than by cause, which is what confirms it is
missing rules and not 27 defects: `su-absorbing`, `su-shallow-descent` and
`si-fork`, then a long tail across `su-*`, `si-*`, `sf-*` and `sx-*`.

**A partial analysis is safe here, and the safety is structural.** The worry
that a partial analysis is worse than none — a processor raising `XTSE3430` on
some unstreamable constructs and not others tells the caller nothing — is
answered by never guessing. The analysis reports whether it *modelled* every
construct it met separately from what it concluded, and an error is raised only
on a fully-modelled verdict; anything else is "no opinion". So the two failure
modes are not symmetric: a missing rule leaves a case failing, while a wrong
rule would reject a valid stylesheet, and it is the second that the
whole-corpus scan measures at zero.

**Nine of them are not missing rules — they are unreachable under the
published text.** `su-absorbing-205`, `-901`, `-905`, `-908`;
`su-inspection-901`, `-902`, `-903`; `su-shallow-descent-902`, `-906`. All nine
want `XTSE3430` for a declared-streamable `xsl:function` whose body the
published rules find perfectly streamable. The suite's own descriptions name
three intended rules, and each of the three is blocked by the spec itself.

*"Not grounded" / "consumes the streamed input"* (`su-absorbing-901`,
`su-inspection-901`, `-903`). §19.8.8.11's table gives a reference to the
streaming parameter posture **grounded** for both the absorbing and inspection
categories — for inspection, whether the reference is singular or not — and
§19.8.1 then says *"If P is grounded, then S′ is S"*, so absorbing it is
charged nothing. `su-inspection-901`'s body ends `else string($element)` and
comes out grounded and motionless, which §19.8.5.3 permits.

The comparison that settles it is `su-inspection-A`, which the catalog expects
to **run**, against `su-inspection-901`, which it expects **refused**. Their
functions are the same function; the only difference is the final `else` arm:

    A:   else f:get-inherited-attribute-value-004($element/.., $attribute-name)
    901: else string($element)

Under §19.8.8.11 both arms are grounded and motionless, so the two bodies are
indistinguishable. A rule counting references to the streaming parameter does
not separate them either: `A`'s `f:depth-002` references `$input` twice.

*"First argument allows a sequence"* (`su-inspection-902`,
`su-shallow-descent-906`). The intended rule is that a streaming parameter
declared `node()*` — or, in `-906`, with no `as` at all — disqualifies the
function. **Both §19.8.5.3 and §19.8.5.5 declare exactly that in their own
worked examples** and call the result guaranteed-streamable:

    <xsl:function name="f:depth" as="xs:integer" streamability="inspection">
      <xsl:param name="input" as="node()*"/>            <!-- §19.8.5.3 -->

    <xsl:function name="f:alternate-children" streamability="shallow-descent">
      <xsl:param name="input" as="element()*"/>         <!-- §19.8.5.5 -->

Implementing the rule refuses the specification's own examples, which is the
spurious-rejection failure mode this analysis exists to avoid.

*"Two consuming references to the variable"* (`su-absorbing-205`, `-905`,
`-908`). **The suite says outright that this rule is not in the spec.** The
catalog entry for `su-absorbing-205` reads, verbatim:

> Recursive absorbing function .
> See https://saxonica.plan.io/issues/4561
> See https://github.com/w3c/qtspecs/issues/15
> Analysis suggests there's a rule missing in the spec: multiple references
> to the streaming parameter, or references within a higher-order operand, should not be allowed.

Saxon 9.8 passes all nine, which is what a submission does when it implements a
rule its own author has filed against the specification. We do not, and the
reason is the asymmetry in the note above: the rule cannot be transcribed
because there is nothing to transcribe, and inventing it means refusing
stylesheets on a rule no reader of the specification could have anticipated.
`su-absorbing-205` is additionally withheld for an ordinary reason — its body is
an `xsl:copy` with children, a sequence-constructor shape `analyzeFunctionBody`
does not model — so it would still report nothing even if a rule existed.

These nine are recorded here rather than left to be re-derived: the derivation
above has been done at least twice, and both times the reasoning was correct and
went unwritten.

One withholding is worth naming, because it looks like a gap and is not.
§19.8.8.4 widens a union of two striding operands to crawling by its own
admission rather than by necessity, so a rule applying templates to
`current-group() except .` is withheld rather than refused — `si-group-055`
asserts output for exactly that. The withholding is lifted where §19.8.4.19
refuses the same grouping for a reason that never consults the call — a
free-ranging `group-starting-with` pattern, or a grouping key that is not
motionless — since neither answer rests on the widening.

**Note what it would and would not buy.** Completing it would move the 14
cases still wanting an `XTSE3430` and take XSLT 3.0 from 99.70% to about
99.82%. Nine of those 14 are the unreachable group above, so the reachable
gain is 5. It would not make the engine stream, and it would not change the result
of a single transform that currently succeeds — it would convert 14 correct
answers into 14 refusals to answer. That is the conformant behaviour, and it
is worth being explicit that the gain is measured in conformance rather than in
capability.

The remaining 20 are singletons or near-singletons and are catalogued in
[conformance-gaps.md](conformance-gaps.md) rather than here. Only three pairs
share anything: `merge-097`/`-097s` both fail on `FODC0002`, and the
CHANGELOG records them as not interoperable on the test set's own maintainer
comment — they rely on Saxon's `?select=` collection URIs and declare no
environment for the harness to honour. `-097sf` was read here as a third
member and is not one: it declares `<feature value="streaming-fallback"/>`,
which this engine does not claim, so it is skipped and never reaches
`FODC0002` at all. `si-copy-117`/`si-copy-of-117` both get
`XTTE1540` where `XTTE1510` is wanted; and `si-fork-814`/`sx-MapExpr-007` both
get `XQDY0137` for `XTDE3365`. The rest — `docbook-001` (`XTMM9000`, chunking),
`validation-0201` (whitespace placement),
`strip-space-009`, `system-property-012` and a scatter of one-off error-code
disagreements — share no cause with each other at all. That is the useful fact
about them: after the streamability pass there is no second cluster waiting
behind it.

`system-property-012` is not a defect and should not be read as one: it asserts
that `system-property('xsl:supports-streaming')` answers `yes`. §26.5 requires a
processor that does not conform to the streaming feature to answer `no`, which
is what this answers. Passing it would mean lying to every stylesheet that
branches on it to choose a fallback. It is the same gap as the 40 above, seen
from the other side, and it stays failing for as long as the analysis is
missing — which is the correct behaviour, not a cost.

### XQuery schema awareness: a tail of features `import schema` made reachable (XQuery 3.1)

**1 failure of 30,346, and not a regression.** This entry exists
because the number is easy to misread. `import schema` was implemented, and
implementing it brought **416 previously-skipped cases into scope**, of which
339 now pass. The in-scope count went 29,930 → 30,346 and the passing count
29,918 → 30,343. A lift that admits failing cases raises the failure count by
construction, and quoting the failure count without the denominator beside it
would describe a gain as a loss.

The tail has fallen 203 → 113 → 101 → 93 → 42 → 26 → 10 → 3 → **1** as the features
behind it landed. What remains is a tail of separate features that
`import schema` made *reachable* without making them present. `docs/todo.md`
§1.5 names them and is the forward-looking half of this entry; what belongs
here is the measured shape, because it is what says the tail is several
features rather than one broken import.

What remains is one singleton, `prod-ContextItemDecl`; `op-same-key` and
`app-Demos` have since closed. `prod-CastExpr.schema` is at 88 / 88.

The last of the cast group was a single shape, and a narrow one: a cast to a
list type built each token from the item type's erased *code*. `xs:IDREF`
erases to `xs:string`, a union to nothing, so the sequence F&O 3.0 18.3.6 owes
— "each of which is an instance of the item type" — came back as bare strings
or, for a union over lists, as the one string handed in, because the union's
list member was looked up by item code and `xs:IDREFS` is a built-in the
schema's type table does not hold. The union-member fix had already shown the
answer — carry the NAME the code loses — and this is that answer one level
further in: the item type, and each list member of a union, is resolved as a
full cast target of its own, and every token is cast through it.

**The error-code mismatches are the interesting minority.** A group raises
`XPST0008` where `XQDY0027` is wanted. The `XPST0017`-for-`FORG0001` group that
stood beside it is closed: the constructor of an imported schema type is
registered in no library, so every DYNAMIC route to it -- `function-lookup`, and
a partial application `t(?)` -- reported the name unknown where the cast it
stands for owed `FORG0001`. Those are not missing features — they are the static-versus-dynamic
boundary being drawn one step too early, and they are the part of this tail
that is a defect rather than an absence. They are worth separating out
precisely because the rest are not defects and it would be easy to let these be
counted with them. Two groups that stood here were that same boundary drawn on
the wrong side, and each needed an existing check to look one field further
rather than a new feature — which is the evidence for reading the rest of this
group the same way rather than as absent machinery.

### XSLT 2.0: eight failures, and no cause peculiar to the lane

**8 of 6,201.** They are recorded here only because the lane has no entry
anywhere else and eight is small enough to name: `docbook-001`,
`format-number-070`, `import-schema-137`, `regex-syntax-xslt20-0984`, `-0985`,
`-0987`, `sequence-0132` and `validation-0201`.

Nothing here is a 2.0-specific gap in the engine. Three of the eight —
`docbook-001`, `import-schema-137` and `validation-0201` — fail identically in
the 3.0 lane and are covered above or in
[conformance-gaps.md](conformance-gaps.md).

`format-number-070` and `sequence-0132` fail only here, and both are recorded
in [conformance-gaps.md](conformance-gaps.md) as suite defects:
`format-number-070` invokes a template the stylesheet does not declare —
verified, zero `xsl:import`/`xsl:include` and zero `name="main"`. Neither is a
rule this engine has failed to implement.

The three `regex-syntax-xslt20` cases are the last, and they are the same
*kind* of case as the 22 `MS-Regex` schema cases under *Unicode category
drift* below; the CHANGELOG puts them under one heading. Each asserts a
single-character class against a single codepoint: `[\w]` against U+2308,
`[\d]` against U+1369, `[\c]` against U+0346. All three classes are defined by
Unicode general category, and all three codepoints sit where the assignment has
moved or is read differently than the suite assumed. Passing them means
freezing an old character database, which is the same refusal made below for
the same reason.

Worth stating, because the shape invites the opposite conclusion: three
failures in one test set normally means a cluster worth chasing. Here it is
three independent codepoints reached through three different classes, and what
they agree on is the *rule*, not a bug they share.

### Schema-validity rules not yet implemented (XSD)

**1 schema false accept in 1.0, 4 in 1.1** — invalid schemas this loads without
complaint. At this count the remaining cases are named individually; there is
no cluster left to tabulate.

| Case | Versions | Constraint |
|---|---|---|
| `MS-DataTypes/anyURI_b006_1356` | 1.0 only | RFC 2396 excluded characters in an `anyURI` enumeration |
| `Simple/simple093` | 1.1 only | `xs:NOTATION` as a union member type |
| `MS-Element/elemZ026` | 1.1 only | a 1.0 substitution-group rewrite XSD 1.1 deleted |
| `MS-Particles/particlesZ026a` | 1.1 only | same, plus a validity the W3C never settled |
| `MS-Particles/particlesZ033_g` | 1.1 only | a 1.0-era `invalid` verdict inherited by the 1.1 run |

**None of the five is a gap to be closed.** Every one is argued under
*Deliberate divergences* below: each is a case where the suite contradicts
itself or where the only rule that would reject the schema is one XSD 1.1
deliberately removed, and enforcing it costs more valid schemas than it buys.
They are listed here rather than only there because the count is what a reader
looking for open XSD work will find first, and the honest answer is that the
schema-validity column is empty of tractable work.

That is a statement about *these five*, not about the area. Adding rules here
would be the highest-yield remaining work if any were left un-argued, and it is
also the riskiest: a rule stricter than the spec starts rejecting real schemas
the suite never covers. Every change must be measured against both suite
directions *and* the production corpora (65 UBL + 427 CII), which are the
strongest guard against over-strictness — and, where those are absent, against
the 230 real-world schemas vendored in `testdata/`.

**A note on that guard when the corpora are absent.** UBL and CII are licensed
and unvendored, so a checkout without `GOXSLT_UBL`/`GOXSLT_CII` cannot run
them, and DocBook and XSpec exercise the XSLT engine rather than the schema
loader. Three things stand in when they are missing, and it is worth stating
all three, because it is easy to conclude there is no guard at all.

The first is a standing check rather than a fallback. 230 real-world `.xsd`
files ship in `testdata/` as fixtures for the XSLT and XQuery suites, and
`tests/check.sh` loads each on its own in every run, fast mode included,
ratcheted as `VendoredSchemas` — 185 load, 38 are DocBook 5.0's genuinely
invalid schema (below), 7 are excluded as fragments or deliberately invalid
test data, each named in `vendoredExclude`. A rule that starts rejecting one of
the 185 fails the build and says so. What this does **not** do is replace UBL
and CII: these are mostly test fixtures, documentation schemas and namespace
vocabularies, so they are thinner exactly where the corpora are thick — the
deep industry vocabularies with long derivation chains, large substitution
groups and heavy `xs:union`/`xs:key` use that UBL's 65 and CII's 427 exercise.
It catches the over-strict rule that breaks *any* real schema; it does not
catch the one that breaks only commercial ones.

The second is the suite population: roughly 16,000 schemas are labelled
*valid*, and a rule that over-rejects turns one of those into a false reject,
which the harness counts directly. "False rejects did not rise" over that
population is a real over-strictness signal — weaker than the corpora on the
shapes production schemas favour and no substitute for them, but far from
nothing. Pairing each new rule with a valid schema that must still load, in
`xsd/falseaccept_test.go`, is the third.

### XSD instance validation: three remaining, none addressable

Three instance cases remain, and "open" overstates all three — each is argued
and none is tractable work:

- `MS-IdentityConstraint/idZ015` — a field selecting an attribute matched by a
  `lax`/`skip` `anyAttribute`. Open under W3C bug 4063, and left alone until
  the W3C settles it.
- `MS-Attribute/attP031` — a false *reject*, declined on purpose. The reasoning
  is under *A prohibited attribute use creates no attribute use* below.
- `saxonData/Id/id017.n01.xml` (1.1 only) — a defaulted `xs:ENTITY` in a
  document carrying no DTD. Declined on purpose: the unparsed-entity check bails
  when the instance declares no unparsed entity at all, which is what keeps
  `as-34` and the XSLT suite's `as-3401`, `match-208` and `match-209` passing.
  Read in full under *All 62 are adjudicated case by case* in
  [conformance-gaps.md](conformance-gaps.md).

### XSD particle restriction: `particlesZ001` and the two-job wrapper

`particlesZ001` is the one remaining addressable XSD 1.0 schema false reject,
and it is a real gap rather than a divergence: the schema is valid and this
refuses it. It is a `<sequence>` whose `<element name="element" minOccurs="0"
maxOccurs="unbounded"/>` restricts a base `<choice minOccurs="0"
maxOccurs="unbounded">` containing that element.

The cause is visible: `recurseAsIfGroup` wraps the element in a group of one
and hardcodes the wrapper at `1..1`, discarding the element's own occurrence
range. A once-only group is then compared against a repeating one, so the
repetition the base allows looks like something the restriction dropped.

**The obvious fix was measured and reverted, and the ratio is why.** Moving the
range onto the wrapper makes `particlesZ001`, `Z023` and `Z024` load, but
schema agreement falls 14,204 → 14,194 on 1.0 and 15,045 → 15,038 on 1.1 —
about **eleven invalid schemas newly accepted for each valid one recovered**.
(Those totals are the baseline of the run that measured them, not current
figures; what matters is the ratio, which is why they are left as recorded.)

The reason is that the wrapper's range is doing two jobs. For the mapping in
clause 2 it should repeat; for the *effective total range* check it should not,
because a group of one repeating N times contributes N elements where the
original particle contributed its own range. Carrying the range fixes the first
and breaks the second. **A correct fix needs the two separated rather than one
range serving both — which is a change to `effectiveTotalRange`'s contract, not
a change to this wrapper.** That is what makes this an open gap with a known
shape rather than a patch nobody has tried.

Three further 1.1 cases — `particlesHb008`, `particlesHb011` and
`particlesZ028` — need XSD 1.1's §3.4.6.4 intensional restriction: genuine
language inclusion in *both* directions rather than the structural table.
`particlesHb008` restricts `choice{e1, sequence{e2,e3,e4}}` by a reordered
`choice{e1, sequence{e2, choice{e3,e4}}}` that no table can relate. That is an
automaton subsumption engine, not a rule, and two rounds declined it
deliberately rather than ship a partial one.

### `ste110`

The remaining schema false reject on both versions, `queried` against W3C bug
4957. Carried here rather than under *Deliberate divergences* because no
reading has been recorded for it either way: it is a false reject, which is the
direction that matters, and it has not been diagnosed.

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

### XML 1.1 external entities and DTD-side rules

XML 1.1 sat outside all of this until the character layer was implemented. The
gap was never that 1.1 documents were refused — they parsed — but that
`version="1.1"` was rewritten to `1.0` and the document then read under the
wrong language's rules. The version now reaches the tokeniser, and [2] `Char`,
[2a] `RestrictedChar` and the §2.11 line ends follow it. What is still missing
is in the external-entity and DTD layers, and is described in
[todo.md](todo.md#11-xml-11-documents--character-rules-done-dtd-side-rules-outstanding).

---

## Deliberate divergences

Not gaps. Each is a behaviour that disagrees with a test, or with a reading
someone will propose, on purpose — and each carries the spec clause it rests on
and the **measured** cost of changing it. The number is the point of the entry:
without it, the trade gets re-attempted.

### A duplicate attribute is accepted, as `encoding/xml` accepts it

XML 1.0 §3.1 makes `<r b="safe" b="evil"/>` fatally malformed, and Namespaces
in XML §6.3 says the same of two prefixes bound to one namespace producing the
same expanded name. This parser accepts both. Each attribute survives in
`el.Attrs`; `Attr()` returns the first, and `xsl:copy-of` re-serialises both.

The divergence is inherited rather than chosen: Go's `encoding/xml` accepts the
same document — `xml.Token()` returns no error, verified — and this package
reads tokens from it. The check **was** implemented and reverted: it rejected
this library's own serialiser output for an element that undeclares the default
namespace, which is a worse failure than the one it prevented
(`xdm/parse.go:286-293`).

Two things bound the consequence. XSD validation checks *both* attributes, so
nothing passes the schema path silently — `<r b="1" b="notanint"/>` fails
`cvc-attribute.3`. And nothing here smuggles markup: both values are parsed
attribute values, not text.

What remains is a parser differential, and it is recorded here because
`SECURITY.md` tells a reader that "a document accepted that the schema forbids"
is in scope. A pipeline that authorises on `Attr()` while something downstream
reads `Attrs[1]`, or re-parses the round-tripped output with a stricter parser,
will not agree with itself about what the document said. If that shape is in
your design, reject duplicates before this parser sees them.

### DOCTYPE is refused by default

A DOCTYPE is the entry point for XXE and entity-expansion attacks. Refusing it
unless the caller opts in is the correct default for a library that will be
pointed at untrusted input. `xdm.ParseOptions{AllowDOCTYPE: true}` enables it
where the documents are trusted — which is what loading UBL requires, because
the W3C XML Signature schema it depends on carries one.

**Cost: none, once the distinction is drawn in the right place.** A conformance
harness pointed at a vendored suite is not the untrusted caller the default
protects, so `tests/xsdsuite` sets `AllowDOCTYPE` on the schema-load path only,
leaving external entities off. The default itself is unchanged.

### `xsi:schemaLocation` is ignored by default

Honouring it lets the document choose the schema it is validated against, which
defeats the purpose of validating. `WithInstanceLocations` opts in, with a
policy that names which namespaces may be resolved.

### `fn:collection()` raises an error rather than returning empty

**Cost: zero cases.** Nothing scores against this any more; the entry is kept
because the default is what a future caller will meet, and the argument for it
is not obvious.

Returning an empty sequence for an unconfigured collection would let a
stylesheet silently process no documents and report success, which is worse
than an error, so `FODC0002` stands as the default. With no resolver configured
it is still `FODC0002` even where a hook is available, which is the point.

### Unicode category drift (bug 4113)

**22** cases in `MS-Regex2006-07-15`, identical in both versions, all flagged
`queried bug4113` by the W3C: `reJ11`, `reJ13`, `reJ19`, `reJ21`, `reJ23`,
`reJ25`, `reJ29`, `reJ31`, `reJ33`, `reJ35`, `reJ61`, `reJ69`, `reJ75`, `reJ77`,
`reL98`, `reL99`, `reM98`, `reN99`, `reS21`, `reS42`, `reT63`, `reT84`. The
list is enumerated so the next re-measurement can diff it rather than re-count.

**Cost of matching them: shipping a frozen 2001 character database.** They
assert that `\p{Lu}` rejects characters that *are* uppercase letters in current
Unicode. The suite was written against Unicode 3.1; the codepoints in question
— U+1D7A8 among them — were categorised differently then.

These are **22 of the 30 disagreements on 1.0 and 22 of the 32 on 1.1** — two
thirds of everything the suite reports against this engine, and the single
largest reason XSD cannot reach 100% and should not try. The three
`regex-syntax-xslt20` failures in the XSLT 2.0 lane are the same rule seen from
the other side.

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
in the lexical space rather than out of it — **gains three cases where the
alternative gains one.**

There is a second, larger cost. Facet-value validation and instance validation
share one path down to `isAnyURILexical`, with no seam between them, so
tightening 1.0 `anyURI` would reject **every instance value containing a
Windows path or an unescaped space**. UBL is a 1.0 schema set and its documents
carry exactly those. The one suite case is not worth that.

**`simple093`** names `xs:NOTATION` as a union member type. §3.2.19 does forbid
using NOTATION directly, and this package enforces that for a restriction base,
a list item type, and an element or attribute type. The union arm is left
unenforced because `MS-Particles/particlesZ007` contains
`<xsd:union memberTypes="xsd:NOTATION"/>` and is labelled **valid**, with a
dependent instance test that only runs if the schema loads. Both cases carry
status `accepted`.

**Measured twice: enforcing the rule trades simple093 for particlesZ007 in 1.1
and costs 1.0 two cases outright**, because simple093 is a `saxonData` case
that never runs under 1.0 at all. The comment in `facet_check.go` records the
measurement so it is not retried.

### `particlesZ033_g` is a 1.0 verdict scored against a 1.1 run

`MS-Particles/particlesZ033_g` — 1 schema false accept, 1.1 only.

Under 1.1 this schema has **no competing pair at all**. Enumerating every state
of every content model shows the two candidates a reader will reach for do not
arise: the two `e2` positions never share a state, because the inner
`<sequence minOccurs="56">` has to be re-entered or completed, so the outer
`e2` is only ever reachable from `follow` sets the inner one is absent from;
and `m1`-against-`head` is what `particlesZ033_e` and `_f` write, both
correctly rejected in *both* versions on that pair, while `_g` is the variant
in which the author replaced it — the inner choice became `m3` against
`ref='head'`, and `m3` is a fresh name that overlaps nothing. The remaining
`ref='m1'` sits behind `<element e3 minOccurs="2">` in a sequence, so it is not
in the model's `first` set and never meets `head`.

What is left is the single pair `ref='m1'` against `<xsd:any/>`, which is what
rejects the schema under 1.0. `XSD1_1TestCategories.xml` names that relaxation
outright — "Relaxation of UPA: wildcard/element competition no longer violates
UPA" — and `s3_10_1v04s` through `s3_10_1ii09s` are written to depend on it. So
under 1.1 the model is unambiguous by the letter of the rule this suite states,
and accepting it is right.

The verdict scored against it is a 1.0 verdict. The group carries no `version`
attribute, its `documentationReference` points at the 2004 XSD 1.0 REC, and its
one bare `<expected validity="invalid"/>` is therefore inherited by the 1.1 run
under `appliesAND`. A test in the wrong bucket, not a missing rule.

**The measurement, so it is not retried. Restoring element-against-wildcard
competition under 1.1 gains this one case and costs seventeen valid schemas**,
taking the 1.1 total from 41536 to 41494 at the time it was measured. The false
rejects it creates are `addB153`, `all006`, `wild030`, `wild047`, `wild049`,
`wild050`, `wild052`, `wild072`, `wild073`, `s3_3_6v01`, `s3_3_6v04`,
`s3_8_6v01`, `s3_8_6ii01`, `s3_4_6v01`, `s3_4_6v04`, `s3_10_1v04` and `ste110`
— that is, the whole family of groups whose subject *is* the relaxation. No
narrower rule separates them: `_g`'s pair is a counted element against a
following wildcard, and `wild047`'s is the same shape. **One case for seventeen
is the wrong direction.**

Accepting it is also not a soundness hazard. The runtime in `nfa.go` is a
subset construction over the counter state, exploring every position in
parallel, so an element-against-wildcard choice UPA no longer objects to is
still resolved correctly at validation time. `particlesZ033_g` produces no
instance disagreement, only the schema one.

One negative result is worth keeping: this is **not** a budget decline. Every
give-up path in `upa.go` and `assemble.go` — the state-width cap, the pair-test
cap, `compileContentModel`, and the substitution-closure cap — returns an error
wrapping `xdm.ErrResourceLimit` rather than accepting. The huge occurrence
values here never inflate the automaton, because occurrences are runtime counts
on a counter automaton and not states. The cannot-decide invariant holds.

### The range comparison XSD 1.1 deleted (elemZ026, particlesZ026a)

`MS-Element/elemZ026` and `MS-Particles/particlesZ026a` — 2 schema false
accepts, 1.1 only. Both are **suite artifacts, not gaps**.

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
`{1,unbounded}` against a member's `{1,1}`. Occurrence Range OK fails.

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
contains its substitutable members.

**The named fix, measured.** Threading each step's particle through `stepNFA`
into `declCompatible` and applying `occurrenceRangeOK` was implemented in full.
It flips elemZ026 to `invalid` as predicted — **and it also re-rejects
`particlesHa161` and `particlesZ001`, both marked `accepted` by the suite and
both valid schemas the subsumption engine was built to accept. XSD 1.1 fell
41536 → 41534: a net loss.** The fix is not narrower than the rejected
alternative; it is the same 1.0 artifact reintroduced through a different door.

The reason it fires at all is narrow and accidental. When the head is abstract,
`asSubstitutionChoice` omits it and `stepNFA` sees a single declaration on the
base side, so `declCompatible`'s clauses run. When the head is concrete,
`stepNFA` finds two declarations for the same name, sets `multiple` and returns
none, so the range would never be compared. A rule that depends on whether the
base's head happens to be abstract is not Occurrence Range OK under any
reading.

**What the suite says.** elemZ026's `<expected validity="invalid"/>` carries
`status="queried"` against W3C bug 4146, opened by Michael Kay in 2007: "the
metadata describes the schema as invalid, but it contains no obvious error. XSV
reports it as valid." The bug is still `NEW`, keyworded `disputedTest`, and its
whiteboard records an intent to fork a separate 1.1 test that was never done.
`particlesZ026a` is weaker still — the TSTF concluded its validity was
"implementation-determined" and the WG never decided, which the test's own
annotation says in as many words.

`xsd/subsume_occurs_test.go` pins this in both directions: the two
substitution-group restrictions that must keep being accepted, and the genuine
widenings — `maxOccurs` 2→3, and bounded→unbounded — that language inclusion
still rejects without ever comparing a bound to a bound.

### A prohibited attribute use creates no attribute use (attP031)

`MS-Attribute/attP031` — the one remaining addressable XSD instance false
reject, declined on purpose.

It is a suite self-contradiction rather than a defect here: it declares
`use="prohibited"` with a `fixed` value and expects the instance supplying that
value to be *valid*, while `attF001` — structurally identical but **without**
`fixed` — expects invalid, and both carry status `accepted`. §3.4.2 gives
`{attribute uses}` only the declarations whose `use` is absent, `optional` or
`required`, so a prohibited use creates no attribute use at all. Making
`attP031` pass means treating `fixed` as the discriminator, which no clause
supports.

**Measured in a clean checkout so the figure is attributable: keeping a
prohibited use that carries `fixed` takes XSD 1.0 from 39,345 to 39,346 and
leaves 1.1 unchanged**, with no schema-level change and no false accept
introduced — `attF001` still rejects. (Those are the baseline totals of that
run; both have since risen, and what matters is the delta.) So it is a clean
**+1, and it is declined anyway**: the same change makes this validator accept
`att="37"` against a declaration that prohibits the attribute, which is a
deliberate false accept bought for one suite point. A case that passes without
a clause behind it is not a fix.

### `xs:gMonth` old lexical form (bug 6901)

`gMonth002_2061`, `gMonth004_2063`, flagged `queried bug6901` — false rejects
in both versions.

These use `--03--`, the withdrawn gMonth syntax from the original XSD 1.0
release. The errata replaced it with `--03`. **Cost of accepting them:
accepting a form no current spec defines.**

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

**Measured cost of the default: one QT3 case.** `fn-matches-51` names a group
whose width can vary *and* places the backreference mid-pattern; it passes with
`xpath.SetBacktrackingRegex(true)`, which takes that lane to 15,222 of 15,222.
That figure is not the headline one, because the switch is off by default and
the headline number reports the default configuration. Closing the last case by
default would cost the linear-time guarantee, which is a worse trade than the
case is worth.

The XML Schema pattern facet is unaffected: Appendix F's `atom` production has
no form for a backreference, so `xsd` still rejects `\1` under both versions.

### DocBook 5.0's XSD refuses to load — DocBook's schema is invalid

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
**So `CheckOptions.LaxUPA` is not merely off by default, it would be wrong as a
default: loading DocBook with `LaxUPA` set moves 568 errors to 567.** These are
distinct `*ElementDecl`s with distinct types, not one declaration seen twice —
the genuinely-same-declaration case (`<xs:element ref=>` twice in a sequence)
already loads clean.

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
construct XSD cannot express — which is why DocBook ships RELAX NG as
normative.

**Cost of relaxing either check: a false accept against `mgR002` and `mgQ021`
directly.** A search over all 11,060 expected-valid schemaTests in the suite
found no case with two same-named local declarations of differing types in one
content model, so there is no counterexample to the current behaviour, and none
of the schema-level disagreements in `xsdtests` mentions `cos-nonambig` or
`cos-element-consistent` in either direction.

### Four suite cases that should be read as disputed

Each carries status `accepted` and each is questionable on the suite's own
evidence. Only `particlesZ001` (1.0) and `simple093` (1.1) still cost anything;
the rest are kept for the argument, because each is a guard on a fix someone
will propose for a neighbouring case.

* **`simple093` contradicts `particlesZ007`.** The first declares
  `<xs:union memberTypes="xs:QName xs:NOTATION"/>` invalid; the second contains
  `<xsd:union memberTypes="xsd:NOTATION"/>` and is valid. Enforcing the rule
  trades one for the other, and loses `particlesZ007` outright under 1.0 where
  `simple093` is not even run. The list form is enforced; the union form is
  deliberately not.
* **`particlesK006` under 1.1.** L(R) ⊆ L(B) holds, so it is a valid
  restriction under §3.4.6.4, yet it is marked invalid with no version
  qualifier. Its documentation is what states the distinction that constrains
  any change to all-group restriction:

  > B's minOccurs=0, B's maxOccurs=absent, but the element has min=max=1,
  > R's minOccurs=0, R's maxOccurs=1 — expected **invalid**

* **`particlesZ001` under 1.0** is expected valid with no version attribute
  while its own annotation calls the 1.0 rule "ambiguous" and tags it as
  intensional restriction, a 1.1 feature. It is a genuine false reject all the
  same, and is written up under *Open gaps* above.
* **`simple004`/`005`** are self-flagged as depending on the resolution of spec
  bug 2074, and `simple006`'s own note says "one could argue for valid".

### Constraints on the 1.1 restriction relaxations

The relaxations themselves are done. What must survive is the set of guards
each one turned out to need, because every guard was found by breaking a case
that the obvious version of the change had not considered. Each is a measured
cost of the naive alternative.

**They are version-gated, not general.** `particlesT002`/`T009` (a reordered
choice), `particlesHa161` (an optional element restricting an optional choice)
and `particlesZ023`/`Z024` (a one-member choice) are all marked invalid under
1.0 and valid under 1.1. 1.0's RecurseLax really is written as an
order-preserving walk, and `stripPointless` removing a one-member choice is
*correct* for the 1.0 table. **Removing the strip unconditionally fixed the two
1.1 cases and broke the same two under 1.0, for a net loss of three.**

**A range cannot serve both `recurseAsIfGroup` and `effectiveTotalRange`.**
Moving an optional element's range onto the wrapper works only where the base
does not repeat: in `effectiveTotalRange` a group of one repeating N times
contributes N elements, so the same range means two different things. **This is
what broke `particlesV020`**, and it is the same collision recorded under
*XSD particle restriction: `particlesZ001` and the two-job wrapper* above.

**The derived minimum must already satisfy the base's.** Without that
condition, moving a `minOccurs` of 0 onto the wrapper made it violate a base
requiring 1, and **`ctF007` became a false reject for exactly one case gained.**

**The base's compositor decides whether a wrapper may be kept.** Keeping every
one-member choice under 1.1 **turned `particlesR001` into a false reject**: a
one-member choice restricting a sequence-with-wildcard is valid, and only
reaches a cell of §3.9.6's table once the wrapper is gone. The wrapper is
preserved only when *both* sides are choices, where the pair decides the cell.

**Only a group occurring exactly once may be inlined into an all group.** An
all group of all groups admits exactly the interleaving of their members, so
the nesting carries no information the flat list does not — but a *repeating*
group multiplies its members' occurrence ranges, and folding that into the
parent would compare the wrong budgets. That is the ambiguity `allSubsumes`
exists to refuse rather than guess at.

**An optional all group is a disjunction, not a scaled budget.** Scaling each
per-name budget by the base group's range is the obvious reading and it is
wrong in both directions: it makes `mgO029` load and **breaks `particlesK006`**,
whose documentation is quoted above. The base is *(empty) | (every budget met)*
— two alternatives checked separately — and the derived side is a disjunction
too. Flattening either side to `0..1` per name cannot express the coupling
between two required members, which admits this false **accept**, invisible to
the suite:

```xml
<!-- B: {} | {a1,a2} — never {a1} alone -->
<xsd:all minOccurs="0"><xsd:element name="a1"/><xsd:element name="a2"/></xsd:all>
<!-- R: admits {a1} alone, which B forbids -->
<xsd:all minOccurs="0"><xsd:element name="a1"/><xsd:element name="a2" minOccurs="0"/></xsd:all>
```

A discriminator on *shape* rather than on language — zeroing the budget floors
only when the derived side is also a group — scores both suite cases correctly
for the wrong reason and admits exactly that schema. Both halves are guarded
independently in `xsd/allgroup_disjunction_test.go`.

**A wildcard's occurrences do not split between the names it spans by a simple
count.** `all244.n` is the negative test that holds this honest, and it is
still rejected, with `the base requires a wildcard, which the restriction
omits`. A relaxation that recovered `all206`/`all218`/`all237`/`wild049`/
`wild050` by loosening the wildcard-occurrence rule would take `all244.n` with
them; `xsd/allgroup_wildcard_test.go` pins all six shapes together. Their XSD
1.0 rejections are **correct** and are not gaps: 1.0's `cos-all-limited.1`
genuinely forbids a non-element particle in an all group, and `wild049`/
`wild050` also spell `notQName`, which is 1.1-only. The 1.0 lane is pinned in
the same file.

### A union's selected member is a third fact, beside the annotation

Recorded as an invariant rather than as a gap, because the mistake it describes
is available at every site that copies a node, and one of those sites is added
whenever a new copying instruction is.

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
so a stylesheet could sort by a key that saw the type and then dispatch on a
pattern that did not — and `xsl:strip-space`, a declaration about whitespace
with nothing to say about types, was what untyped the document.

It was diagnosed by measurement rather than by reading, which is the
transferable part: a probe over the validated tree showed `union="StandardDate"`
present on every `Date`, and a trace at the `instance of` match site showed the
annotated value arriving 1614 times from the sort key and an *unannotated* one
arriving twice, from `apply-templates`. Reading the copy sites would not have
narrowed it; counting arrivals did.

**The invariant, which is what must survive.** The seven PSVI properties are
carried by two named operations on `xdm.Node` — `CopyTypingFrom` and
`CopyTypingStrippedFrom`/`StripTyping` — rather than by a hand-written field
list at each of ten copy sites. `xdm/typing_test.go` censuses `Node`'s exported
fields against the PSVI list and against an explicit list of the fields that
are deliberately excluded, so a new field is neither absorbed nor exempted
silently. `DocumentURI` is the one that looks like it belongs and does not — it
is the URI a document was RETRIEVED BY, and a copy was not retrieved.

The tenth site is the instructive one, and the direction of its copy is why
reading missed it. `copyAnnotationTree` in `xslt/validate.go` carries an
assessment *back*, from the document `xsl:result-document` validated onto the
nodes the result actually records. Every other site copies FROM the tree the
caller holds; this one copies from a tree the engine built and is about to
discard, so it does not look like a copy site at all until the question is
asked as "what arrives here, carrying what?".

---

## Corrections

Verdicts recorded in this file that were **wrong**. Not fixes — a fix leaves no
trace here — but readings that were believed, quoted, and disproved. They are
kept because a negative result that was believed for two revisions is more
dangerous than an open bug, and deleting one invites the same probe again.

### "§19.8.9.3 costs four valid stylesheets" — it gains ten and costs none

An uncommitted verdict, carried between sessions as an oral claim and never
written down until it was re-measured and found wrong. The claim was that
implementing §19.8.9.3 — the streamability of `fn:current` — measured `+5` by
count but `9 passing / 4 regressed` by name, and that the four regressions were
spurious refusals of `stream-200`..`203`, whose accumulator rule is
`part-name/text()[$selected-parts = current()]`. On that basis the work was
said to have been reverted.

**Re-measured from the same baseline: +10 cases, zero regressions.**
`sf-current-901`..`905` as expected, and five more that came free —
`si-for-each-904`, `si-iterate-035`, `si-iterate-904`, `stream-204`,
`streamable-110` — because a `current()` call the analysis could not model used
to abandon the whole enclosing construct.

**The regression was real, and it was a bug in the implementation rather than a
cost of the rule.** §19.8.9.3 gives the call the context posture of the
*outermost* containing XPath expression; what it does not say, because §19.8.1
already does, is what happens when that call is *absorbed*. `current()` in
`text()[$parts = current()]` denotes the text node the pattern matched, and a
text node has no children, so §19.8.1's downgrade — *"If U is absorption and
the intersection of T with U{element(), document-node()} is U{} … then U′ is
inspection"* — applies exactly as it does to the equivalent `text()[$parts =
.]`. An implementation that answers §19.8.1's question for `current()` with the
blanket "assume children" default makes the predicate consuming and refuses all
four stylesheets. Answering it from the outermost context item instead — the
`currentAllowsChildren` field in `xslt/streamability.go` — refuses none of
them.

The suite pins both directions, which is why the fix is not a loosening:
`stream-200`..`203` match on `text()` and must **run**, while `stream-204` is
the same accumulator rule on an element step, where the absorption stands and
`XTSE3430` is correct. `stream-204` is one of the ten gained.

The lesson is the one this section exists for. **A revert recorded only in
conversation is a measurement that cannot be checked**: there was no revert
commit, no residual code and no entry in either gap file, so the claim survived
purely on retelling while `conformance-gaps.md` went on listing `fn:current`
among the rules absent entirely. Had the four names been written down beside
the verdict, the next reader would have seen in one step that the four share a
`text()` step and that the fifth sibling on an element step is a case the suite
wants refused — which is the whole diagnosis.

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
revisions. The walks that *do* iterate are the ones asking a question the
parser did not pre-answer: which built-in a type descends from, and whether one
type derives from another. Six of those truncated on a legal acyclic chain, and
two false accepts are what the superseded reasoning had licensed — a duplicate
`xs:ID` accepted once the restriction chain under `xs:ID` ran 64 links, and
`"1.5"` validating against a type descending from `xs:integer` at the same
depth. Neither schema is recursive or malformed.

So the rule for any future audit of a bound: **measure the chain length on the
built component before concluding anything**, and show the loop taking one step
per link. `TestBaseChainActuallyIterates` is written that way for exactly this
reason, and `TestDeepFacetChainCollapses` and `TestDeepUnionAndListCollapse`
pin the collapsing walks so that the superseded negative result above cannot be
re-derived from the same shape. The design rationale that replaced it — why a
visited set is the exact mechanism and a count never was — is in
[xsd.md](xsd.md#limits).

### `particlesZ033_g` was diagnosed as an element-against-element pair, and there is no competing pair at all

This file previously reasoned that since XSD 1.1 switches element-against-
wildcard competition off, the 1.1 rejection had to come from an
element-against-element pair, and named two candidates — the two `e2`
declarations at different nesting levels, and `ref='m1'` against `ref='head'` —
both of which it believed `counterForces` was suppressing, so it pointed a fix
at `exitBlocked`.

Enumerating every state of every content model in the schema shows otherwise:
under 1.1 the schema has **no competing pair, suppressed or not**.
`counterForces` is never consulted and `exitBlocked` never sees the model. The
correct reading, and the seventeen-schema cost of the fix that was pointed at
the wrong place, are under *Deliberate divergences* above. The lesson is that a
diagnosis reached by elimination — "it cannot be the wildcard, so it must be an
element pair" — is not a diagnosis until the enumeration is actually done.

### The counts in *What would move the numbers* had drifted by two orders of magnitude

A section stood here carrying an XSD table that summed to 251 and 368
disagreements against 51 and 47 actually measured, and describing twenty-six
open XPath failures on suites that report none. It also restated percentages
that this file's own opening rule says belong in
[conformance-gaps.md](conformance-gaps.md) alone, precisely because a figure
copied into two places drifts in one of them. It drifted in this one.

What was durable is the route rather than the arithmetic, and it is worth
keeping because it predicts where the next round of failures will turn out to
live. Of the seventeen XPath disagreements that remained after the ordinary
bugs were fixed, **five were the QT3 harness rather than the engine**, two
needed DTD attribute defaulting, two needed a document to be retrievable under
the URI `fn:document-uri` reports for it, and one was a lexical form that
disagreed with its own value. The adversarial audit of the XSLT and XSD
verdicts found the same shape again: of the twenty-three cases it judged
fixable, most were the harness — chiefly eight XSD `indeterminate` expectations
per version silently scored as "must be invalid" — and only four were engine
defects.

**A conformance number is only as honest as the harness producing it, and a
verdict is only as good as the last time someone re-derived it.**

### "The fixable column is empty on every suite" was scoped to one audit

That sentence was true of the population the audit covered — the XSD and XPath
disagreements standing at the time — and is still true of those: XPath is 100%
at all three versions, and the XSD remainder is argued case by case above. It
was never true of the suites as a whole. **XSLT 3.0 carries 126 failures of
which 45 are one missing analysis, and XQuery 3.1 carries 42, and both are
eminently fixable.** A sentence scoped to one audit and left standing after the
scope changed is the same decay this file keeps recording.

### "3 (1.0) and 2 (1.1) *addressable* false rejects" counted direction as tractability

A heading here read that, and then the body argued each of the three down:
`attP031` is a suite self-contradiction and its +1 is declined on purpose, and
the two `gMonth` cases are a withdrawn lexical form under an open bug. None of
the three was addressable. The heading counted false rejects and called them
addressable **because** they were false rejects, which is the direction that
matters — but direction is not the same as tractability, and the two had been
conflated.

### A case list is a measurement, and it decays

An *Instance validation gaps* section listed 25 XSD instance false accepts by
name — `Simple/simple001`, `simple002`, `simple016`, `simple086`,
`ElemDecl/typeDef012*`, `valueConstraint007*`, `MS-ComplexType/ctZ013c`/`-d`/
`-e`, `MS-IdentityConstraint/idG006`, `idK012`, `suntest/idc006.nogen`,
`XmlVersions/xv009`, `MS-Schema/schU4`, `schU5`, `MS-Additional/isDefault070`,
`isDefault077`, `MS-SimpleType/stE054`, `MS-Regex/reK6`, `Complex/complex022`,
`CTA/cta0006`. Re-measured, **not one of them still disagreed, in either
version**. The entries had survived several rounds after the bugs behind them
were already fixed, and each round quoted the list rather than re-deriving it.

The same decay ran through the numbers: an entry saying 18 bug-4113 cases
beside a section saying nineteen, when the enumerated count is 22; a
schema-validity section quoting 195 and 305 open rules measured before the bulk
of them landed; a "13 and 12 disagreements" summary that had been 5 and 7 and
was by then 3 and 5. **Re-measure before working an entry in this file.**
`tests/check.sh` is how, and *How to re-measure* below is where.

---

## Related

[conformance-gaps.md](conformance-gaps.md) is the ledger: every failing case in
every suite, named, with the current numbers and a fixable / not-fixable
verdict. This file is the reasoning behind the hard ones.

[xsd.md](xsd.md#limits) holds the design rationale for the schema engine's
bounds — the occurrence-count vector, the saturation split between the matcher
and the derivation checks, and why every graph walk is bounded by a visited set
rather than a step count.

[todo.md](todo.md) is the forward-looking half of this file: what to build next
and what each item would cost. Several gaps here — XML 1.1 DTD-side rules, DTD
support — are entries there as features rather than bugs.

[reaching-100.md](reaching-100.md) is what buying the remainder would cost.

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

Before accepting any change that adds a schema-validity rule, load the
production corpora — 65 UBL 2.1 entry points and 427 UN/CEFACT CII schemas.
The suite cannot catch a rule that is stricter than the spec; real schemas can.
