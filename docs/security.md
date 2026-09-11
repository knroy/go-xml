# Security

What this library defends against, what it does not, and what a caller has to
do. Everything below was tested rather than reasoned about: each claim of
safety names the check that enforces it, and each finding was reproduced before
it was written down.

The threat model throughout is that **the attacker controls the instance
document**. Where a finding needs more than that — a hostile schema, a hostile
stylesheet, a caller-enabled option — it says so, because that changes who is
exposed.

## How to read a finding here

Eight audits have passed over this code, and the categories below are not
interchangeable. Conflating them is how a fixed bug stayed filed as live for a
release, and how an unproven suspicion was twice reported as a confirmed
vulnerability.

| category | what it means | what it needs to change category |
|---|---|---|
| **Fixed** | reproduced, then fixed, with a regression test that fails against the previous code | nothing; it stays as a record |
| **Open — performance** | reproduced, and the cost is real, but no wrong answer is produced | an algorithm that changes the complexity, not a smaller constant |
| **Deliberate limitation** | the behaviour is chosen, and the reasoning is written down | a spec citation showing the choice is wrong |
| **Withdrawn** | recorded as a defect, then measured and found not to be one | nothing; it stays so the same reading is not repeated |
| **Unverified** | a plausible reading of the code that nobody has demonstrated | a construction that produces a wrong answer, or a probe that shows it cannot |

The last row is the one that costs the most time. Three separate audits have
flagged a numeric bound in this package as a confirmed bug on the reading that
"deeper than N" implies "recursive"; two were right and the third was not, and
telling them apart took construction rather than argument every time. A guard
is only a defect once a legal, acyclic input crosses it and gets a wrong
answer. Until then it is debt, and `docs/known-gaps.md` records which of the
remaining ones have been probed and found sound.

## Current status

Eight audits have passed over this code. This section is the whole of what is
*live*: everything it names is described in full further down, and everything
already fixed has been reduced to one line apiece under *History* at the end,
with the narrative in [CHANGELOG.md](../CHANGELOG.md).

**Open.** One, and it is a caller responsibility rather than a defect here.

| finding | reach | why it is still open |
|---|---|---|
| `javascript:` URLs pass through | hostile stylesheet | an XSLT processor is not an HTML sanitiser; see *Open findings*. |

Three defects found on 2026-09-10 while verifying an external report — none of
them a claim *in* that report — were fixed the same day and are listed under
*History*. All three were one shape: a budget minted fresh where it should have
been inherited, at `fn:transform`'s nesting boundary, at a function item's
invoke sites, and at every XSD assertion. That seam has now produced five
findings, so it is the first place to look when a limit is reported as not
binding.

The eighth audit's six other findings are closed and are listed under
*History*. Two of them ended the process rather than the request — a Go stack
overflow is a fatal runtime error, not a panic, so `recover()` does not catch
it and one request takes the server with it. Both are now refusals: a
self-applying function item is charged recursion depth like a named one, and a
flat operator chain is bounded at the parser where a single limit protects
evaluation and serialisation as well as optimisation.

**Knowingly incomplete.** One narrowing remains, and it is in an API rather
than at a copy site. `xdmbuild.Builder.AddAttributeTyped` takes a type
annotation as a **string**, so an attribute entering a result tree through the
builder arrives carrying its annotation name and nothing else — `UnionMember`,
`DerivedPrimitive`, `ListItem`, `IsID`, `IsIDREFS` are all dropped there, on
every path, and have been since the builder was written. It is the one place
left where a node's typing is reconstructed from a name instead of copied. The
node-copy sites themselves no longer do this; see *History*.

**Deliberate limits**, which are resource controls and not bugs — a request
refused here is refused loudly, and the fallback is conservative in the
rejecting direction:

`xdm.ParseOptions` `MaxBytes` / `MaxDepth` / `MaxNodes` ·
`xslt.FileResolver.MaxBytes` · `xsd.Options` `MaxDocuments` ·
`xquery.Options` `MaxModules` / `MaxModuleBytes` / `MaxSchemaBytes` ·
`dtd.LoadOptions` `MaxExternalDocuments` / `MaxExternalBytes` /
`MaxEntityBytes` · `dtd.FileResolver.MaxBytes` ·
`xsd.ValidateOptions` `MaxDepth` / `MaxErrors` ·
`DefaultMaxMatchStates` · `subsumeMaxStates` · `subsumeMaxProduct` ·
`branchLimit` · `xpath` `MaxItems` / `MaxBytes` ·
`xsd.Options` `MaxContentModelPositions` · `maxUPAStateWidth` ·
`maxUPAPairTests` ·
`maxSubstitutionClosure` · `TransformOptions.MaxDepth` · the RELAX NG
derivative bound · the XPath regex step and depth budgets ·
`xpath` `maxChainLength` / `maxOptimizeDepth`.

"Refused loudly" needed qualifying, and now it holds in both halves. A limit
that raises an error has to borrow a *semantic* error code, because the specs
define none for "I gave up" — `XPDY0001` for a depth cap, `FORX0002` for a
valid pattern whose budget ran out, `cvc-elt.1` for a document that was never
assessed. Read alone, each of those tells the caller something untrue about
its input. Every such site now also wraps `xdm.ErrResourceLimit`, so
`errors.Is` separates a refusal from a fault while the code and message stay
byte-identical for the suites; `docs/options.md` tabulates the sites.

The `subsumeMaxStates`, `subsumeMaxProduct` and `branchLimit` declines are the
other half, and they are the quiet ones: they raise nothing at all, returning
"declined" so the caller falls back to the conservative structural table. That
is sound (below) but it was invisible — a schema refused because a budget ran
out looked exactly like one the table genuinely forbids. Those declines are now
counted through the `budgetStats` hook in `xsd/subsume.go`, on the model of
`icStats` in `xsd/identity.go`, with budget declines (fixable by raising a
limit) separated from structural ones (a recursive group, an all group, a
wildcard — no limit affects those). The counters observe; they change no
verdict, which `xsd/budget_stats_test.go` asserts alongside the counting.

For four of these — `maxPositions`, `branchLimit`, `subsumeMaxStates` and
`subsumeMaxProduct` — the claim that the fallback is conservative is no longer
only a reading of the code. `xsd/budget_soundness_test.go` enforces it
differentially: the four are package-level `var`s (never assigned outside
tests), and the suite computes each verdict twice — once normally, once with
the budget forced so low that every input exceeds it — then asserts the one
direction that matters,

    the budgeted path ACCEPTS  =>  the exact path also ACCEPTS.

The converse is allowed: a declined procedure may reject something a full
computation would have admitted, and a false reject is safe. Both valid and
invalid inputs are in the corpus, because a suite of valid inputs alone cannot
tell a sound fallback from one that accepts everything — which is the whole
failure mode. The harness was validated by sabotage: making the swallowed
`modelFor` error, a declined subsumption, and a declined branch enumeration
each report "no violation" was caught, with a concrete schema and document in
the failure message.

**The last unbudgeted load-time algorithm now has a budget.** `checkUPA`
(`xsd/upa.go`) is the Unique Particle Attribution check, and its cost was
O(states x pairs): `maxPositions` bounded the number of positions but not the
pairwise scan over them, so the work was cubic in the size of a content model.
Measured through the public `Load` API on a sequence of n optional elements,
the densest follow relation the shortest schema text can produce: n=256 26ms,
n=512 216ms, n=1024 1.67s, n=2048 12.0s and 1,431,655,424 pair tests, about 8x
per doubling. Extrapolated to a model of 8192 positions that is roughly
fourteen minutes and over a gigabyte for one ~320 KB schema document.

That extrapolation describes the **dense** shape, and it is now historical
rather than reachable: `maxUPAStateWidth` refuses a dense model of 1024
positions in 68ms, long before the position budget is approached. The
projection is kept because it is why the width gate exists, not because it is
what the position budget prevents — those are two different bounds guarding two
different shapes, and the section below separates them.

Two budgets now bound it. `maxUPAStateWidth` (256) is checked on `len(state)`
**before** the triangular loop begins, so no part of the quadratic cost is paid
before the gate notices, and a state is either scanned whole or declined whole
— a half-examined state would be a check reporting "no violation" having looked
at some pairs and not others. `maxUPAPairTests` (2^22) is cumulative across all
the states of one model, because the width gate alone does not bound the total:
8192 positions in 8192 states of width 255 each pass the width gate and still
perform ~260 million pair tests. With both in place the same n=2048 schema
loads in 0.51s rather than 12.0s, and the curve is quadratic — the cost of
building the follow relation — rather than cubic.

The threshold is chosen from measurement. Instrumented over every schema in
this tree, the widest state a real schema produces is **19** positions in
`testdata/xsdtests` (15,464 schemas) and **72** in `testdata/xslt30-test`,
whose widest single file is
`tests/expr/type-expr/variousTypesSchemaExpr.xsd`; the adversarial shape above
reaches 2,048. So 256 is 3.5x the widest real state and 8x the XSD suite's, and
cannot fire on legitimate input, while capping one state's scan at ~32,000 pair
tests. `TestUPABudgetDoesNotFireOnRealSchemas` pins that from below, and it is
the load-bearing test of the pair: since the budget refuses rather than skips,
a threshold quietly lowered stops legitimate schemas from loading.

**Exceeding either budget REFUSES the schema; it never skips the check.** An
earlier revision of this budget did skip, on the reasoning that it matched the
compile-failure precedent at `upa.go:179` and that a more permissive budget is
always the safe one. That reasoning was wrong, and the fix is recorded here
because the wrong version was written down in this file first.

UPA is a **normative** schema-component constraint (XSD 1.1 Part 1,
"Constraint on Complex Type Definition Schema Components", `cos-nonambig`): a
schema that violates it is *invalid*. Skipping the check therefore did not
decline to answer — it answered "valid" for a schema whose validity was never
established. The observable defect: a repeating choice of n identically named
elements is ambiguous at every n, yet at n=4 it was rejected as `cos-nonambig`
and at n=300 it loaded clean, the verdict decided purely by whether the state
width crossed `maxUPAStateWidth`. Same violation, opposite verdict, chosen by a
resource constant.

The governing principle is the one this file states everywhere else: **a
resource budget may decline to answer, but must never turn "I could not prove
the constraint" into "the constraint holds".** So:

| outcome | result |
| --- | --- |
| proven valid | accept |
| proven invalid | reject with `cos-nonambig` |
| cannot decide | refuse, carrying `xdm.ErrResourceLimit` |

The refusal wraps `xdm.ErrResourceLimit`, exactly as the `MaxDepth` refusal in
`xsd/validate.go` does, so a caller can tell "your schema is ambiguous" from
"this schema is too complex for me to check" — the first can never succeed on
retry, the second can, under a higher budget. `errors.Is(err,
xdm.ErrResourceLimit)` is true for the refusal and false for a genuine
`cos-nonambig`.

**This is a behaviour change, and it is stated plainly:** an *unambiguous*
schema with a state wider than `maxUPAStateWidth` loaded before this budget
existed and loaded while the budget skipped, and now FAILS. That cost is
unavoidable — the checker cannot distinguish a wide-and-fine model from a
wide-and-broken one without doing the work the budget forbids, and of the two
answers available only the refusal is honest. It is tolerable only because of
the gap between 256 and the widest real state measured (19 across 15,464 W3C
schemas, 72 in the XSLT corpus): nothing real reaches it, and the conformance
marks in `tests/ratchet.txt` for XSD 1.0 and 1.1 are unchanged by
the switch from skipping to refusing.

`contentModel.upaSkipped` is gone. It existed because a declined check and a
clean one both returned nil and were otherwise indistinguishable; a refusal is
its own signal, so the field was dead. The prior advice to wrap untrusted loads
in a wall-clock limit remains good practice, but it is no longer load-bearing
for this particular algorithm.

**The substitution closure is now bounded too, and one budget covers both
algorithms that depend on it.** `linkSubstitutionGroups` (`xsd/assemble.go`)
builds the transitive substitution-group membership of every global element,
once, before any content-model constraint is checked. It was quadratic and
unbounded: a chain of n elements where each substitutes for the one before
gives the i-th head a closure of n-i, so the structure is n^2/2 entries from a
schema text that grew linearly. Measured through the public `Load` API on that
chain — n=512 10ms, n=1024 39ms, n=2048 152ms, n=4096 633ms and **8,386,560
entries, 1.35GB allocated for one 273KB schema document**, 4x per doubling.

The second algorithm is why this was urgent rather than merely untidy.
`elementNamesOverlap` (`xsd/upa.go`) decides whether two element declarations
can match the same name, and it did so by looping one substitution closure
inside the other — O(|a|·|b|) for a *single pair test*. `checkUPA`'s
`maxUPAPairTests` budget counts that pair as **one**, because it was written on
the assumption that a pair test is O(1). It is not, and nothing else in the
package measured the closure either. The gap is not marginal: a content model
of just 64 positions — well inside `maxUPAStateWidth` of 256 — whose
declarations each carry a closure of 255 spends **28.5 seconds** inside
`checkUPA`, and 32 positions at closure 2,048 spends **90 seconds**, with
`maxPositions`, `maxUPAStateWidth` and `maxUPAPairTests` all satisfied
throughout. A budget can only bound what it measures, and none of them measured
this.

`elementNamesOverlap` was fixed by **algorithm rather than by budget**, and the
distinction is deliberate. A bound is the right answer only when the work is
irreducible; this work was a set intersection computed by brute force. It now
intersects through a map, O(|a|+|b|) per pair, and the 90-second shape costs
574ms. Refusing a schema for a cost that a map lookup removes would reject
legitimate input to avoid an expense the caller never needed to pay.
`TestElementNamesOverlapIsLinearInClosureSize` asserts the growth rate — a 4x
increase in closure size must not cost more than 8x — so a regression to the
nested form is caught as such rather than as a slow test.

The worst case measured on the pre-fix code is worse than the figures above,
and is recorded because it isolates the cause. Holding the PAIR COUNT FIXED at
2,016 and varying only the closure size, a 64-position model cost 2.3s at
closure 64, 8.1s at 128 and 31.1s at 256 — 4x per doubling of a quantity
`maxUPAPairTests` does not measure, while the number of pairs it does measure
never changed. At 128 positions the same shape cost 3m55s at closure 256 and
**18m4s at closure 512**, and it LOADED: an 18-minute schema load inside every
budget the package had. After the fix those same five shapes cost 437ms, 816ms,
1.53s, 12.1s and 25.0s — 2x per closure doubling instead of 4x, a 43x
improvement at the largest, and the speedup widens with size, which is the
signature of removing a quadratic factor rather than a constant one.

`maxSubstitutionClosure` (2^16 = 65,536) then bounds what remains: the total
membership entries one schema may produce, counted as the walk visits them.

**It is one budget, not two, and that is a ceiling rather than a sum.** Both
expensive algorithms are expensive for the same reason, and that reason is a
single number computed in a single place. Independent per-check budgets would
let one adversarial schema spend both, making the worst case their total; the
shared bound makes it a maximum. The usual argument against sharing — that one
legitimately expensive check starves another — does not apply, because this is
not a pool of work drawn down by whichever consumer runs first. It is a bound
on the **size of a shared data structure**, enforced once when that structure
is built, before any check reads it. Every consumer sees the same bound and
none can exhaust it for the others.

**Exceeding it REFUSES the schema; it does not truncate the closure.** The
truncating alternative is the tempting one and is the more dangerous by far,
because it looks conservative. Substitution membership decides which elements a
particle matches, so dropping members is not a smaller correct answer: a
document that should validate would fail, and — in the direction this file
cares about — a `cos-nonambig` or `cos-element-consistent` violation caused by
the dropped member would vanish, reported as satisfied by a check that never
saw it. That is precisely the false accept the UPA budget was corrected to
avoid.

`xsd/budget_soundness_test.go` states the same two-sided property it states for
UPA — over budget must produce an error wrapping `xdm.ErrResourceLimit`, for
valid and invalid schemas alike, because a closure that was never computed
cannot tell them apart — and `TestSubstitutionClosureRefusalIsNotATruncation`
drives an invalid schema whose fault lies in the last member of a chain,
proving the over-budget outcome is a refusal rather than a load. Both were
validated by sabotage: made to truncate and carry on, the harness reported
`FALSE ACCEPT` naming the schema; the `elementNamesOverlap` fix reverted to the
nested loop was caught by growth rate, 945ms at closure 1,024 against 13.7s at
4,096.

The threshold rests on a census, not on taste. Instrumented over all 15,702
`.xsd` files in this tree — `testdata/xsdtests`, `testdata/xslt30-test`,
`testdata/qt3tests`, `testdata/relaxng`, `testdata/xsltng`, `testdata/xspec`
and `w3cschemas` — loaded at both 1.0 and 1.1, the largest total closure any
real schema produces is **50 membership entries**, in
`testdata/xslt30-test/admin/catalog-schema.xsd`, whose widest single closure is
26 members. 65,536 is over 1,300x that.
`TestSubstitutionClosureBudgetDoesNotFireOnRealSchemas` pins the gap from
below, and it matters for the same reason the UPA one does: firing rejects.
Both XSD conformance marks in `tests/ratchet.txt` are unchanged.

**Three neighbouring checks were examined and deliberately left unbudgeted**,
because a budget on work that cannot be made expensive is dead code that only
adds a way to reject valid input. `checkSubstitutionEDC` is positions x
closure, but it dedups by name into a map, so its work is linear and each step
is a lookup: at its worst constructible shape — 32 heads, closure 2,048, 65,536
entries — it costs **9ms**, against 574ms for `checkUPA` on the same schema.
`checkWildcardEDC` is bounded by `maxPositions` at positions^2 and measures
**0ms** at 512 wildcards against 512 locals; it never even runs on that shape,
because `checkUPA` rejects the model first. `addFollow`'s linear dup-scan sits
within `maxPositions` and is dominated by the automaton build — 495ms at
n=2,048, a model `checkUPA`'s width gate refuses at n>=512 regardless. All
three are recorded with these numbers in the inventory in
`xsd/complexity_fuzz_test.go`.

That neighbouring path — when `compileContentModel` itself fails and the model
was skipped, leaving the schema to load unchecked — **was** recorded here as a
known gap and is now closed. `checkContentModelConstraints` refuses instead of
skipping: the refusal wraps `xdm.ErrResourceLimit` and carries no constraint
code, because nothing was examined and a refusal must not be mistaken for a
verdict in either direction. It gates every content-model constraint rather
than UPA alone — Unique Particle Attribution, Element Declarations Consistent,
and both the wildcard and substitution EDC checks — so a schema that reaches
the end of that loop has had all of them performed or has been refused.

The refusal wraps the sentinel for **every** compile failure, not only the
position-budget ones, and that is a deliberate over-approximation.
`compileContentModel` reports a budget decline ("more than N positions") and a
structural fault (a model group that reaches itself, an unexpected term, an
unknown compositor) as plain errors with nothing distinguishing them, so the
caller cannot separate the retryable case from the permanent one. Both mean the
constraints are undecided, so both are refused; the underlying error is wrapped,
so its text still says which occurred. Separating them properly means giving
the structural faults their own sentinel in `xsd/automaton.go`, which is worth
doing and has not been done.

### The position budget is a memory bound, and it is host-tunable

`Options.MaxContentModelPositions` (default `DefaultMaxContentModelPositions` =
8192) bounds the positions in any one compiled content model. It is the bound
whose justification recorded here was **wrong for years**, and the correction
matters more than the number.

The original reasoning was that the follow relation is quadratic in the
position count, so the budget bounded *time*. That half is now stale:
quadratic follow cost is a property of **dense** models, and `maxUPAStateWidth`
(256) owns those, refusing a dense sequence of 1024 optional elements in 68ms
without the position budget ever being consulted. Measured on time alone the
position budget looks redundant — a sparse model of two million positions
compiles and is fully checked in 600ms.

Measured on **memory** it is the only thing standing between a schema of a few
kilobytes and gigabytes of allocation. The shape is a group DAG in which each
of n groups references the next twice: valid, acyclic, tiny, and expanding to
2^(n-1) positions at a flat ~400 bytes each.

| n | positions | time | memory | schema size |
|---|---|---|---|---|
| 16 | 2^15 | 10.7ms | 12 MB | 1.7 KB |
| 20 | 2^19 | 155ms | 201 MB | 2.2 KB |
| 22 | 2^21 | 577ms | 786 MB | 2.4 KB |
| 24 | 2^23 | 2.47s | 3452 MB | 2.7 KB |

Time makes that model look affordable; memory is what makes it an attack. "An
unbounded model is a way to be handed an unbounded allocation" was the right
half of the original reasoning and is still exactly right.

The gate is **incremental**: it fires having already allocated in proportion to
the limit, so the limit is what a hostile schema can actually reserve, not
merely the point at which it is turned away.

| limit | refused in | peak memory |
|---|---|---|
| 8192 | 2.4ms | 2 MB |
| 1,048,576 | 263ms | 360 MB |
| 4,194,304 | 1.08s | 1521 MB |

**Raising it authorises proportional memory.** A host that sets 2^23 is
accepting that a 2.7 KB schema may allocate 3.4 GB, and it is exposed as an
option because a trusted generated schema may legitimately need the room — not
because the default is conservative. The default is the value a host gets when
it expresses no opinion, and it stays at 8192.

Zero means the default, and is resolved in `compileContentModel` rather than
normalised at `Load`, so there is a single place the default is applied. The
budget is retained on the `Schema` and used by validation as well as by the
load-time checks, so a model always compiles under the limit its constraints
were checked under: a schema whose constraints were never decided must not go
on to enforce a content model, and one loaded at a raised budget must not fail
to validate against it.

`NewSequenceMatcher` is the one caller that cannot honour a per-load budget —
it takes a bare `*Particle` and its caller is the DTD validator, which has no
`Options` — so it compiles at the package default.

**The distinction that matters**, and the single most useful idea this file has
produced — it decides whether a numeric constant in this code is a feature or a
bug, and it is the first thing to apply to any new one:

*A bound that bounds work is legitimate. A bound standing in for cycle
detection is a defect.* Each limit listed above bounds **work**, at the point
the work is done: crossing it means the request really did ask for that much,
the refusal is loud, and the constant is a policy choice a caller can move. A
bound used instead of cycle detection is a different animal. It is guarding
against a graph that reaches itself, but it cannot tell a *cyclic* graph from a
merely *deep* one, so it fires on legal acyclic input — and it has been a
defect every single time it appeared here: six `depth > 32` guards, two at 64,
twelve base-chain counters, all now converted to visited sets.

Two corollaries, both paid for the hard way.

The damage is worst when running out returns a **definite answer** rather than a
refusal, because the truncated answer is always the permissive one: a walk that
gave up decided *no cycle*, *not an ID*, *does not derive*, and a document the
schema forbids was accepted with no error anywhere. Where such a bound must
exist at all, exhausting it has to be an error, never a verdict.

And raising the constant is never the fix. The cliffs found here landed at 32,
64, 65, 257, 300 and 4096 — one walk counts links, another counts types, a
third counts decimal digits, and no single number is correct for any two of
them. The fix is a visited set keyed on the
component, which stops a cycle exactly and does not limit a legal chain.
[xsd.md](xsd.md#limits) sets out why a count could never do the job, and
[known-gaps.md](known-gaps.md) records the probe that read "sound" for the
wrong reason.

**Withdrawn.** Two findings were reported, measured, and did not reproduce: a
CR in a text node *does* survive a round trip, and `$e-1` naming a variable is
required by the suite rather than a defect.

## The short version

If you parse untrusted XML with default options, the dangerous classes are
closed: no XXE, no entity expansion, no network access, no file access, no
stylesheet-driven writes, and no XInclude — parsing alone never performs an
inclusion; see *XInclude* below for what running one grants and what it does
not. Input size, node count, nesting depth and recursion
depth are all bounded by default. The one thing you must still do yourself is
**sanitise URLs if you render transform output as HTML**.

Two further cautions, from the third audit. If you set `AllowDOCTYPE: true` —
which some real formats require — the entity expansion bound now counts every
*reference* rather than every distinct entity; before that fix a 70 KB document
could allocate hundreds of megabytes. The exponential schema-load case that was
open alongside it — the group-reference cycle check — has since been fixed, as
has the last open cost finding, `keyref`. See CHANGELOG.

---

## XInclude: a new reader on the old gate

XInclude was added after the third audit, and it is the first feature where the
**source document itself names a resource to read**. Everything else that
reads — `fn:doc`, `xsl:include`, `fn:unparsed-text`, external entities — is
named by a *stylesheet* or by a DOCTYPE. XInclude is named by an element in the
document, and the document is exactly the party this threat model treats as
hostile. That makes the confinement the whole of the safety argument rather
than part of it.

The answer is that there is no new gate. `xdm` has no filesystem and no
network; `ProcessXInclude` reads only what an `xdm.IncludeResolver` hands it,
and `xslt.FileResolver` implements that interface by calling the very same
`resolvePath` that already gates every other read: a non-`file` scheme is
rejected before the filesystem is touched, confinement is enforced at the
open by `os.Root`, and a path outside every root is refused. An inclusion
therefore reaches nothing `fn:doc` could not already reach — it is the same
files, from the same roots, with the same refusals.

Writing a second check here was considered and rejected. Two copies of a
containment rule are two things to keep correct, and the first time they drift,
one of them is the hole — which is the same reasoning that put
`ResolveEntity` and `ResolveText` through `resolvePath` rather than giving each
its own.

It is off by default in the sense that matters: nothing in this library calls
`ProcessXInclude`, so a caller who has not asked for XInclude does not have it,
and a caller who has asked has already named the roots. There is deliberately
no per-resolver `XInclude bool` beside `UnparsedText`: the switch would sit
below the one that already decides the question, and a caller who set it and
then wondered why nothing happened would be measuring the wrong thing.

Asserted by regression test rather than argued: a path escape (five spellings,
including `..` traversal and a bare absolute path), a symlink out of a root,
every network scheme against a canary HTTP server that records **zero** hits,
an end-to-end hostile document whose `xi:include` names an `http://` URL, and
that an `xi:fallback` cannot be used to launder a refusal into a read.

Two **resource budgets** hold the cost of one pass: at most 200 resources read
in total, and at most 40 levels of nesting. Both report `resource limit
exceeded`, and neither substitutes for the other — a fan-out of a thousand
distinct small files repeats nothing and would otherwise cost a thousand
parses, while a chain recurses in Go.

Neither is loop detection, and neither is allowed to stand in for it. A loop is
a *semantic* defect and is detected as one: `includeProc.stack` holds the URIs
of the inclusions currently in progress, and an inclusion whose URI is already
on that path is refused as `circular xi:include loop`, naming the URI. The
distinction matters in both directions. A loop is caught at depth one rather
than after forty fetches, and it names the resource that actually closed it. A
legal chain of forty-one distinct files is refused as the expense it is, not
described as circular.

The path is keyed on the URI the **resolver reports** rather than the `href` as
written, so that two spellings of one file — `b.xml` and `../d/b.xml` — are one
entry. Keyed on the raw reference, the loop above closes a lap later and blames
the wrong resource; `TestXIncludeCycleThroughDifferentRelativePathsIsCaught`
asserts exactly that difference. Entries are removed on the way out, which is
what makes it an active path rather than a visited set: a diamond — two
documents including a third — is legal and must be included twice, which
`TestXIncludeDiamondIsLegal` pins.

---

## XQuery `import module`: a resource named by the query

Module import was added after the seventh audit, and it is the second feature
where the **input itself names a resource to read** — XInclude above is the
first. An `import module ... at "..."` location is a string chosen by whoever
wrote the query, and a query is the more commonly untrusted of the two inputs a
host supplies: a stylesheet is usually the host's own, a query is often not.

The answer is the house pattern, applied without exception.
`xquery.Options.ModuleResolver` is **nil in the zero value**, exactly as
`xsd.Options.Resolver` is, and with no resolver configured an `at` location is
**never opened** — not attempted and failed, not opened. The import then fails
with `XQST0059`, which is §4.12's code for "no module found", and the message
names the option that would have allowed it rather than the location it
declined to read. So evaluating a query cannot grant its author the filesystem
or the network, and a query with no import never consults a resolver at all.

Two ways to supply modules read nothing. `Options.Modules` registers source
text directly, keyed by target namespace; `MapModuleResolver` answers from an
in-memory table and **ignores location hints entirely**, which §4.12 permits
because the hints are hints and the target namespace is the identity. A host
that wants locations followed must say so, and should give a rooted or
table-driven resolver rather than one that will open anything.

The claim was checked by sabotage rather than by reading: making the default
resolver fall back to opening the hint let `/etc/passwd` be opened and parsed
as a library module. `TestNoResolverDoesNotFetch` fails against that change and
asserts both halves — that the error is `XQST0059`, and that it does *not* name
the location, since a message quoting a parse failure or a permission error
would mean the file had been read.

**The two bounds refuse; they never truncate.** `MaxModules` (512, following
`xsd.DefaultMaxDocuments` in shape and value) bounds the modules one
compilation may load transitively, because a module that imports two modules
that each import two more is a fan-out with no natural bound — the same shape
as a schema's include graph. `MaxModuleBytes` (16 MB) bounds the source text
read, **cumulatively across the compilation** rather than per module, because a
budget spent one module at a time is not spent at all; the read is limited as
it happens rather than checked afterwards, so an oversized module is not first
brought into memory and then rejected.

Exceeding either **fails the compilation** with an error wrapping
`xdm.ErrResourceLimit`, and deliberately *not* with `XQST0059`. That
distinction is this file's governing invariant applied to a new path: the
budget declined to answer, and reporting "no such module" would be a claim
about the store that is not true — the module may well be there, under a higher
limit. A caller can tell the two apart with `errors.Is`, and only one of them
can succeed on retry.

What must never happen is a query compiled against the modules that happened to
fit. A truncated or partial set of imports is a static context missing
declarations, and evaluating against it is how an import comes to look
successful while half a library is absent — the same failure a skipped UPA
check was, in a different package. So there is no partial success: the
compilation fails, and `TestMaxModulesIsEnforced` asserts that the refusal
neither succeeds nor borrows `XQST0059` to explain itself.

## XQuery `import schema`: the same rule, a second time

`import schema` (§4.11) is the third feature where the **input itself names a
resource to read**, and it was built to the module import's pattern
deliberately rather than to one of its own. Every claim in the section above
holds here word for word, with the names changed:

`xquery.Options.SchemaResolver` is **nil in the zero value**, so with no
resolver configured an `at` location is **never opened** — not attempted and
failed, not opened. The import then fails with `XQST0059`, §4.11's code for a
schema import that cannot be satisfied, and the message names
`Options.SchemaResolver` rather than the location it declined to read.
`Options.Schemas` registers a schema by target namespace, as source text or as
already-assembled `*xsd.Schema` components, and reads nothing.

One thing is stronger here than for modules. `SchemaResolver` is
`xsd.Resolver`, the *same* interface `xsd.Load` takes, and the resolver the
query's import was granted is the one handed to `xsd` for the imported
schema's own `xs:include` and `xs:import`. So an imported schema can reach no
further than the import was granted: there is no second resolver that could
disagree about what this process may read, and no default of `xsd`'s own
applies. `TestSchemaResolverIsSharedWithXSD` asserts it by observing that the
query's resolver is the one asked for the include's location.

The no-fetch claim was checked by sabotage, and the first attempt at the test
was **too weak** — which is the part worth recording. Making the default
resolver fall back to opening the hint let `/etc/passwd` be opened; but
`/etc/passwd` is not a schema document, so the parse failed and the error was
still an `XQST0059` that did not name the file. Asserting only "the message
does not name the path" therefore passed against a real breach.
`TestSchemaNoResolverDoesNotFetch` now asserts the **positive** fact instead:
that the refusal is the configured-nothing refusal, naming
`no SchemaResolver is configured`, and that it does *not* report having read or
parsed anything. That version fails against the sabotage, as it must.

`MaxSchemaBytes` (16 MB, following `DefaultMaxModuleBytes` in shape and value)
bounds the schema source one compilation reads, **cumulatively across every
import** rather than per import, because a budget spent one import at a time is
not spent at all. Exceeding it **fails the compilation** with an error wrapping
`xdm.ErrResourceLimit` and deliberately *not* with `XQST0059` — the same
governing invariant: the budget declined to answer, and "no such schema" would
be a claim about the store that is not true. There is no partial success, for
the reason a truncated module set has none: a schema whose components are
partly missing validates documents against the half that is left.
`TestMaxSchemaBytesRefusesRatherThanTruncates` and
`TestMaxSchemaBytesIsPerCompilation` assert both halves.

## What fuzzing has ruled out, and what it has not

Every audit finding in this document was reasoned about and then asserted by a
regression test. Fuzzing is the complement: it searches for the input nobody reasoned
about. Five targets now do that — over the XML parser, the schema assembler and
its content-model compiler, the stylesheet compiler, the XPath expression
compiler, and a parse → serialise → parse round trip. See
[testing.md](testing.md#fuzzing) for how to run one.

Each was run for 150 seconds and none found a crash. The parser alone took
about 20 million executions, the schema assembler 4.4 million, and the round
trip 3.6 million. What that buys, stated precisely:

- **`xdm.ParseString` did not panic**, and every refusal came back as an error
  value with no tree beside it. A panic on parse is a denial of service for any
  host that parses input it did not write, which is every host that accepts a
  document over the wire.
- **`xsd.Load` did not panic** at either XSD version, and every content model
  it accepted compiled to an automaton that answered every query. A `.xsd` is
  as untrusted as a `.xml` when both arrive over the wire, and the assembler is
  the larger and more recursive body of code.
- **Serialisation is faithful.** A document that parses, serialises to
  something that parses back to the same document — same kinds, same expanded
  names, same string values. A serialiser that could emit text which reparses
  differently is an injection primitive wherever output is handed to another
  processor, and no input was found that does it.

What this does **not** establish. A coverage-guided search finds what it
reaches in the time it is given, and 150 seconds is a floor rather than a
ceiling; these runs are evidence of absence only in proportion to their length.
None of the targets asserts that an *answer* is correct, only that a refusal is
a refusal — the one content-model defect that fuzzing did find here was found
by comparing against an independent oracle, a technique still applied by hand
rather than by a standing target ([todo.md](todo.md)).

Nor did any of it bear on the two **cost** findings — the quadratic identity
constraints and the exponential group-reference cycle check. Both are fixed
now, and neither was found by fuzzing: they are unbounded time with flat
memory, and a fuzzer is nearly blind to that shape: Go reports a hang only
after ten seconds in a single execution, so an input merely expensive rather
than non-terminating is recorded as slow
and dropped. That the schema target ran 4.4 million executions without tripping
the hang detector says the search did not happen to generate a deep enough
reference DAG — not that one is hard to write by hand, because it is not.

---

## Open findings

### INFO — `javascript:` URLs pass through

`<a href="{/d/u}"/>` yields `href="javascript:alert(document.domain)"`. This is
spec-conformant — XSLT does not sanitise URLs, and the value *is* correctly
`&`-escaped. **If you render transform output as HTML, sanitise URL-valued
attributes yourself.**

### Withdrawn — a CR in a text node *does* survive a round trip

Recorded here as an open defect on the reading that `escapeText` handles `&`,
`<` and `>` but not `\r`. That missed the branch above the switch: a CR,
U+2028 and the whole C1 range are written as numeric references before the
named escapes are reached, which is what `K2-Serialization-5`, `-10` and `-11`
assert, with `-6` covering the attribute case. Measured across a bare CR, a CR
between characters, a doubled CR, a CRLF pair and an attribute value: every one
round-trips byte-identical. Pinned by `xslt/cr_roundtrip_test.go`.

The XSLT `html` method is deliberately excluded from that branch, and correctly
— HTML has no line-ending normalisation, so a CR there is an ordinary
character.

---

## Verified safe

Each of these was demonstrated by execution, not inferred from reading the code.

### XXE is absent, even with `AllowDOCTYPE: true`

This is the important result, and it is stronger than the code comments claimed.
`encoding/xml` never parses the DTD internal subset — it hands the whole
DOCTYPE over as one opaque `Directive` token. No DTD-declared entity ever
exists, so every reference to one is a hard syntax error.

Tested across external general entities (`file://`, bare paths, `/etc/passwd`,
`http://`), external parameter entities, PUBLIC identifiers, external DTD
subsets, entities in attribute values, and NDATA/NOTATION: **zero file reads,
zero network requests**, with a canary HTTP server recording `hits=0`. Even
*internal* entities fail.

This matters because real callers must set `AllowDOCTYPE: true` — UBL depends on
the W3C XML Signature schema, which carries a DOCTYPE. **That escape hatch does
not reopen XXE.**

### A content model cannot make the matcher allocate without a ceiling

Deciding whether an element's children match a content model needs the *set* of
readings the children admit, because nested occurrence bounds cannot be settled
one reading at a time — see *Why the occurrence counters are a vector and not
a bracket per scope* in [xsd.md](xsd.md#limits). A set is a thing a schema could
try to grow, and a schema is untrusted input: a `.xsd` arriving over the wire is
as hostile as a `.xml`.

Two things bound it. Each occurrence maximum is narrowed per document to what
that document can actually reach, so a scope cannot contribute distinguishable
readings it has no children to fill — `maxOccurs="100000000"` against a thousand
children behaves as `unbounded` does, and readings past the minimum merge
instead of multiplying. That alone holds both W3C suites, UBL 2.1 and the
DocBook corpus to single-digit set sizes. Above it sits `DefaultMaxMatchStates`,
a hard ceiling of 4,096 readings per element, and crossing it fails the element
with an error naming the limit rather than continuing to allocate.

The limit refuses rather than approximates, deliberately. A matcher that fell
back to a heuristic on a large set would be least exact precisely on the inputs
constructed to make it so, which is a validator that can be talked out of
validating.

### Billion laughs is impossible

Same cause. A 9-level, fan-10 entity bomb fails in 10 µs with `invalid character
entity &e9;` — the expansion is never attempted, with or without
`AllowDOCTYPE`.

### Regular expressions cannot backtrack catastrophically

Go's `regexp` is RE2. `matches('aaaa…!', '^(a+)+$')` is flat at 2–6 µs from
n=24 to n=40. Go also *rejects* repeat counts over 1000, so `{1,1000000}` cannot
be used to force an expansion; the XSD pattern translator allocates nothing on
nested quantifiers or 200-deep groups.

**Backreferences do not change this, by default.** XPath 2.0 has them and RE2
does not, and the usual way to bridge that is a backtracking engine — which is
exactly the denial-of-service vector RE2 exists to remove. The default engine
does not use one. A backreference is resolved only when every group it names,
*and the text between the group and the reference*, has a fixed width, where
RE2's single submatch assignment is the only assignment and one comparison
decides the answer; the whole match stays linear in the input. Measured on
`([a-z])\1*`: 4,000 characters in 53 µs, 64,000 in 567 µs.

Outside that subset — `(a*)\1` — the default refuses with `FORX0002` rather
than answering, because deciding it needs alternatives RE2 cannot enumerate. An
engine that answers correctly or says it cannot is safe to expose to untrusted
patterns; one that guesses is not safe at any setting.

**The group count is not bounded, and bounding it was a mistake.** A constant
here refused any pattern declaring more than 64 capturing groups. Neither the
XSD nor the XPath regex grammar sets such a ceiling, so that rejected valid
patterns — `fn:matches` with 65 groups raised `FORX0002` — and it did not buy
the safety it was aimed at. The width analysis it was protecting costs time in
proportion to the pattern's *source length*, not its group count: 20,000 flat
groups are analysed in about 120 µs, while a 12-deep alternation declaring only
12 groups takes eight times as long, because it is 16 KB of pattern text. The
cap therefore refused the cheap shape and admitted the expensive one. Reaching
a nesting depth that costs even half a second requires roughly 4 MB of pattern
the caller has already had to supply and parse, so the input bounds the work
without a ceiling on groups. Pinned by `TestBackrefManyGroups`; the step budget
above is unchanged and still converts exhaustion into `FORX0002`.

**A backtracking matcher is available, and is off by default.**
`xpath.SetBacktrackingRegex(true)`, or `-backtracking-regex` on the command
line, decides the general case. Leave it off for untrusted input. The reason is
the one above: `fn:matches` takes its pattern from the stylesheet, a stylesheet
may be caller-supplied, and `matches($s, $node/@pattern)` takes one from
*document data* — so enabling it globally lets a document being validated
choose how long the validation takes.

Even enabled it is bounded. Every match attempt is counted against a step
budget, and exhausting the budget raises `FORX0002` rather than returning a
silent "no match" — a budget that guessed would do it precisely on the inputs
where the answer was hardest to get. `FORX0002` means "invalid regular
expression", which this pattern is not; it also carries `xdm.ErrResourceLimit`
so a caller can tell the two apart without reading the prose. Measured from both ends: the hardest
honest pattern in either conformance suite answers in 525 steps, while
`(a*)*\1b` against sixty `a`s exhausts the whole budget in about 200 ms. So the
worst case is a fifth of a second of wasted work, not a hang — but it is still
work an attacker can ask for, which is why the default stands.

### Internal entities expand; external ones never do

`AllowDOCTYPE` now also enables **internal general entities** — the
`<!ENTITY name "text">` form declared in a document's own subset. Some schemas
need them: the W3C's RFC 3986 type library composes its URI regexes out of
fifty entities named after the grammar's productions, and without expansion the
document cannot be parsed at all.

The line that does not move is **external** entities. One declared `SYSTEM` or
`PUBLIC` names something outside the document, and fetching it is XXE. Those
are recorded as refused rather than resolved, so a reference to one is an error
and never a fetch — including when reached indirectly through an internal
entity. Parameter entities are not read either.

Expansion is bounded three ways, because nesting is exactly how billion-laughs
works:

| bound | value | why |
|---|---|---|
| depth | 100 levels | past anything hand-written, far short of a bomb |
| one entity | 64 KB | the largest legitimate expansion measured is 9,569 bytes |
| all entities | 1 MB | a bomb split across many entities cannot add up |

A cycle — direct or mutual — is detected and refused rather than recursed.

The 1 MB total is charged *before* expansion, by `entityChargeReader` in
`xdm/dtd_entities.go`, and that ordering is the control. Checking afterwards
reports the same verdict at a cost that makes reporting it pointless:
`encoding/xml` coalesces a run of substitutions into one token, so a document
whose references expand to gigabytes has allocated them all before any
post-parse check can look. The bound therefore caps *peak memory* near the
budget, not merely the accepted result.

That held only for what the reader saw streaming past. The charger buffers
bytes read before the DOCTYPE is parsed and the entity table installed, and it
used to drain that backlog on the *next* `Read` — which for a large entity
declaration never comes: the decoder's read-ahead window is sized in bytes, so
one big declaration fills it along with the whole body, the backlog was
dropped, and the budget was never charged. There was no threshold; a 10 KB
entity referenced 400 times already exceeded the 1 MB bound threefold, and a
1 MB source reached 1.4 GB of allocation — a single-request OOM reachable with
`AllowDOCTYPE` alone, no external entities and no network. The backlog is now
charged at the one point where the table becomes known, so the bound holds
whether the document streams or arrives in a single read. Pinned by
`TestEntityBudgetHoldsWhenDoctypeAndBodyShareOneRead`, which asserts the
allocation and not only the refusal.

Where the charge begins is decided by `endOfInternalSubset`, which must count
*uses* of an entity and not its *declaration*. It tracked quotes and brackets
but had no comment state, and XML 1.0 §2.8 permits comments in the internal
subset while §2.5 says their content is not markup — so an apostrophe or a `]`
written as prose inside one was read as structure, and the boundary moved in
either direction. Comments are now skipped whole.

`MaxBytes` bounds the raw source, so it is applied ahead of every other
reader wrapper — decoding included. It used to wrap *outside* the UTF-16
decoder, whose `fill` reads its whole input in one `io.ReadAll` (it has to: the
encoding declaration it rewrites sits at the front of text a streaming decoder
would already have handed on), so a UTF-16 document was pulled in and decoded
in full before a byte was counted. The limit described the refusal but not its
cost: 8 MB of UTF-16 allocated 136 MB against a `MaxBytes` of 1024. Pinned by
`TestMaxBytesBoundsUTF16Input`.

The parse limits apply to the result, not just the input. `MaxBytes`,
`MaxNodes` and `MaxDepth` are re-applied to the *expanded* text, which was the
suspected bypass and is not one — an entity cannot be used to smuggle a
document past a limit the same bytes would have failed. The re-parse cannot
itself recurse.

The per-entity figure is measured rather than chosen: a first attempt used
1 MB, and a five-level billion-laughs reaching 100,000 bytes parsed cleanly
through it. The regression test that caught that is `TestEntityExpansionBlowupIsRefused`.

### RELAX NG includes: a budget and a cycle check, separately

`<include>` and `<externalRef>` reach a caller-supplied `Resolver`, which may
read a file or the network, so a chain of them costs a fetch per level even
when every href is distinct. `maxIncludeDepth` (40) is the budget for that, and
exceeding it reports `resource limit exceeded`.

A circular schema — `a` includes `b` includes `a` — is a different failure, and
is detected separately by the set of hrefs on the active inclusion path. A
schema that is its own ancestor is refused as `circular schema inclusion`,
naming the href, at the depth where the loop actually closes rather than forty
fetches later. The set is keyed on the href **resolved against the base in
force**, so two spellings of one document are one entry, and it is shared
across the compiler an `<externalRef>` builds for itself so that a cycle
passing through one is still visible. Entries are removed on exit: a diamond,
where two schemas include a third, is legal and compiles.

This is the general rule the codebase follows: *a resource budget may reject an
otherwise valid operation, but a semantic algorithm must never use a resource
threshold to infer a semantic fact.* Before the split, a cycle here was caught
only by the depth counter, and the error said the schemas nested too deeply —
true of the counter, false of the schema. `relaxng/include_cycle_test.go` and
`xdm/xinclude_limits_test.go` hold the two failures apart at both sites.

### The DTD external subset: a new input path, closed by default

`dtd.Load` reads the half of a DTD that `<!DOCTYPE r SYSTEM "r.dtd">` names.
That is a new way for an untrusted document to make this process read
something, so it is gated the way every other one here is, and the gate is the
zero value rather than a flag someone has to remember.

**Default closed.** `dtd.LoadOptions.Resolver` is nil unless a caller sets it,
following `xsd.Options.Resolver` and `xdm.ParseOptions.ExternalEntities`. With
none, nothing is fetched. `dtd.Parse`, which never fetched, is unchanged and
still takes no options at all.

**The refusal is loud, and that is the load-bearing decision.** With no
resolver, a DOCTYPE naming an external subset is *refused* — an error wrapping
`dtd.ErrNoResolver` — rather than validated against the internal subset alone.
The quiet alternative is the defect class the invariant at the top of this
document names. A DTD is a closed description and the external subset routinely
holds every `<!ELEMENT>` in the language, with the internal one holding a
handful of overrides; validating against the internal half would report a
document valid without a single constraint having been checked, or would report
every element undeclared, which a caller silences with `AllowUndeclared` and is
then back to the first case. Neither has proven anything. The pre-existing
reading is still reachable and now has to be asked for, by name:
`LoadOptions.InternalSubsetOnly`. `TestNoResolverRefusesRatherThanValidatingHalfADTD` pins both halves, and
`TestInternalSubsetOnlyFetchesNothingEvenWithAResolver` pins that the refusal
happens *before* a resolver would be consulted rather than being an error
message over a fetch that already happened.

**The bounds are shared with the internal subset, not additional to it.** An
external subset is a new way to deliver a billion-laughs bomb: half the ladder
inline, half in a file. A design with a per-subset budget admits exactly that
product, so there is one counter:

| bound | default | what it counts |
|---|---|---|
| `MaxEntityBytes` | 1 MB | **expanded** parameter-entity bytes, across both subsets |
| `MaxExternalBytes` | 4 MB | bytes read from external resources, whole load |
| `MaxExternalDocuments` | 64 | resources fetched, the subset and every module |

Two details are the whole defence. What is charged is the size substitution
*produces*, not the raw text that produces it — `%a8;%a8;…` is thirty
characters however large `%a8;` expands to, and charging the raw form charges a
doubling ladder a few dozen bytes a rung. And the charge lands on the *loader*
rather than on the string builder of the call in progress, or every nested call
and every subset would get a fresh allowance. Fetched bytes are charged
*before* the text is scanned, on the model of `entityChargeReader` above, so an
oversized resource is refused on the strength of its own length without the
expander being handed it, and the read itself is capped so a resolver returning
an endless stream cannot hang the load before the check runs.

`TestBombSplitAcrossSubsetsIsRefused` builds one ladder cut in half between the
subsets and asserts each half harmless alone before asserting the pair refused,
so what it measures is the sharing and not merely a large file. Removing the
expansion charge was tried: it does not make that test fail politely, it makes
`TestBillionLaughsInTheExternalSubsetIsRefused` run until the two-minute
timeout panic, which is what the bomb going off looks like.

**Parameter-entity recursion is refused by name, not by budget.** XML 1.0 §4.1
makes a recursive entity a well-formedness error, so a name already being
substituted is detected on the first revisit — a cycle of any length, through
either subset, fails immediately rather than doubling until a limit catches it.

**Conditional sections cannot leak.** `<![IGNORE[` contents are not read as
declarations, but the nested `<![` and `]]>` delimiters inside them still are
counted (§3.4). Without that count the first inner `]]>` closes the outer
section and every declaration after it is applied when it should not be —
which is a wrong *verdict*, not a resource question. Removing the count was
tried; `TestNestedConditionalSections` catches it, naming the leaked
declaration.

**No filesystem or network in `dtd` itself.** The package constructs no path
and opens no socket. `dtd.FileResolver` is the only component that touches a
disk and is confined to one `Root`: `..`, an absolute path and a symlink
leading out are each refused *after* symlink resolution and before the file is
opened, and a non-`file` scheme is refused before the filesystem is consulted
at all, so `http://` is a clear refusal rather than a confusing "no such file".
`dtd.MapResolver` reads from memory and is the one that is safe to hand an
untrusted document without further thought. A system identifier is treated as a
URI rather than a path (XML 1.0 §4.2.2), which is also what makes
`file:///C:/dtd/r.dtd` name drive C rather than a host called `C:`.

**There is no partial-DTD path.** A resolver that errors, one that returns no
content and no error, and any budget that runs out all make `Load` return an
error. None of them produces a `*DTD` that a caller would then validate
against.

### All resolution defaults are closed

`doc()`, `document()`, `collection()`, `xsl:include` and `xsl:import` all refuse
when no resolver is configured, and so does `relaxng.Compile`, which rejects
every `href` until a caller supplies a resolver. `collection()` has its own switch —
`Collections`, separate from `Documents` — so enabling `fn:doc` for a known
code list does not also let a stylesheet enumerate whatever a collection URI
names; a resolver that accepts one should validate the URI it is handed, which
arrives from the stylesheet. `unparsed-text()` has its own switch too —
`FileResolver.UnparsedText`, off by default and implied by nothing else. It is
separate because it is the widest of them: `ResolveDocument` hands back a
parsed XML document, so a file that is not well-formed XML discloses nothing,
while `unparsed-text` hands back the raw bytes of any file inside `Roots`. A
root holding one XML data file and one private key leaks the key. An earlier
revision of this document said the function was disabled *unconditionally*;
that was true when written and is no longer, and the distinction matters to
anyone deciding what a root may contain. `xsl:result-document` never writes to
disk; the engine returns secondary results to the caller as data. XInclude is
off unless a caller runs `xdm.ProcessXInclude` and names the roots it may read
— see *XInclude: a new reader on the old gate* above.

`xsd` is the tenth path, and until recently it was the exception this heading
claimed did not exist. Four sites — `Load`, `LoadFile`, `LoadFiles` and
`WithInstanceLocations` — defaulted a nil `Options.Resolver` to a
`FileResolver` with no `Root`, which that type's own field comment describes as
permitting "any readable path". A zero-value `Options` therefore read whatever
an `xs:include` named, `/etc/hosts` included.

It was reachable from outside the package. `xslt` refuses an
`xsl:import-schema` that names a schema-location when no `SchemaResolver` is
configured — but for an *inline* `<xs:schema>` it passed that same nil straight
to `xsd.Load`, and the `xs:include` inside the inline schema was followed
against the open default. A caller who had deliberately configured no schema
resolver still had the filesystem readable through any stylesheet it compiled.

The default now follows the grant the caller has already made:

- **`Load` refuses.** It is handed a tree and no path, so nothing on disk was
  granted and nothing on disk is read. Only a document that actually names a
  location is affected; a self-contained schema still loads with a zero-value
  `Options`, which is how `Load` is almost always used.
- **`LoadFile` and `LoadFiles` keep a default**, because the caller named files
  and reading beside them is the thing that was asked for — refusing would
  break every relative `xs:include` for no gain. The default is now *rooted* at
  the directories the caller named, so a schema that then asks for an absolute
  path elsewhere, or climbs out with `..`, is refused. `LoadFiles` grants the
  set of its arguments' directories and nothing else.
- **`WithInstanceLocations` is rooted at the schema's own directories.** Its
  locations come from the instance, the least trusted input the package takes,
  so the unrooted default was worst of all here.

Two refusals are also made *loud*, which is a change in kind rather than
degree. §4.2.1 permits dropping an `xs:include` whose location cannot be
resolved, and that is right for a location that was looked for and not found —
a remote URL with no network resolver is the ordinary case, and the W3C suite's
own `common/xsts.xsd` depends on it being tolerated. It is wrong for a location
the *configuration* refused: "no resolver is configured" and "outside the
permitted root" are decisions, not misses, and dropping them left a caller who
had hardened with a schema quietly missing components and a successful return.
Both now surface as `src-resolve`. An `xs:import` keeps the silent path in
both cases, because §5.3 *Missing Sub-components* gives an unfetched namespace
a defined outcome — the references into it are ·absent· and the consequence
falls at validation — and every conforming processor loads such a schema.

Every rooted resolver enforces its root by the same **mechanism**: opening
through `os.OpenRoot`. `xslt.FileResolver`, `xsd.FileResolver`,
`dtd.FileResolver` and the `relaxng` resolver in `cmd/go-xml` all resolve each
path component against the root's own descriptor at open time, so containment
is enforced by the kernel at the moment of the open rather than by a string
comparison taken beforehand. A symlink swapped in after the check is refused
rather than followed.

Each still performs the earlier `EvalSymlinks` and prefix comparison, and that
is deliberate: it is the **diagnosis**, not the enforcement. It decides which
root a path belongs to, produces the error that names the permitted
directories, and — in `xsd` — distinguishes a location the configuration
refused from one that was simply not there, which §4.2.1 requires, since an
unresolvable `xs:include` may be dropped but a refused one must surface. The
final path component is deliberately *not* pre-resolved. Resolving it would
hand `os.Root` a path with every link already followed, leaving it nothing to
refuse, and would reinstate the window this shape exists to close.

Until 2026-09-10 `xsd`, `dtd` and the `relaxng` resolver used check-then-open:
`EvalSymlinks` on both sides, compare, then open the resolved path. That was
recorded here as an accepted risk, and the reasoning was sound as far as it
went — because the path opened was the *resolved* one, escaping required
replacing a directory component between the check and the open, which needs
write access inside the root, and an attacker holding that can write the file
directly. It was measured, too: a racer swapping a symlink inside the root
against a resolver reading in a tight loop produced over a hundred thousand
successful reads and **zero** escapes.

The position is nonetheless withdrawn. The window was never the argument; the
cost of maintaining two mechanisms for one property was. Four resolvers
enforcing the same guarantee four ways is more expensive to keep explaining —
and to keep re-litigating each time an external report cannot tell a reasoned
position from an oversight — than it is to unify. The narrowness of the
residual risk is why this was not urgent, not a reason to leave it open.

One consequence worth stating, because it shapes the tests: both shapes refuse
every *statically observable* vector identically, since `EvalSymlinks`
collapses a planted symlink before the prefix check ever reads it. A test that
plants a link and asserts refusal therefore passes against the unhardened code
and proves nothing. The confinement tests assert instead that the refusal
carries `os.Root`'s own "escapes from parent" wording, which is the evidence
that the *open* refused; that assertion fails the moment the pre-resolution
returns, which is what makes it worth having.

`AllowHost` resists spoofing: it uses `u.Hostname()`, so userinfo tricks
(`http://good.example@127.0.0.1/`) and ports do not fool it, and it is
re-checked after base-URI resolution.

### Escaping and serialisation

- Text and attribute escaping is correct in the xml, html and text methods.
- **`disable-output-escaping` is ignored** — the most common XSLT XSS primitive
  is simply absent.
- Comment breakout (`--`) and PI *content* breakout (`?>`) are both errors.
- `]]>` in text is escaped and reparses identically.
- NUL and control characters are rejected at parse and never reach the
  serialiser; a lone surrogate becomes U+FFFD.
- Namespace round-tripping is stable: prefix rebinding, default-namespace
  undeclaration, two prefixes for one URI and attribute-prefix shadowing all
  reserialise to identical expanded-name trees.

### No XPath expression injection

Every `xpath.Compile` call site takes stylesheet or schema source. Attribute
value templates compile at compile time from stylesheet text; document data only
ever supplies *values*. There is no `evaluate()`-style extension, so a document
cannot influence which expression is compiled.

### `xsi:type` is not a type-confusion vector

All five attacks were rejected: an unrelated type, a sibling type, an undeclared
prefix, and both directions of facet escape. Prefix rebinding resolves by URI,
and facets still apply to the substituted type.

### Concurrency and retention

- No `go func` anywhere in non-test code; goroutine count is stable.
- 20,000 parse-and-validate cycles show **0.00 MB** heap growth after GC.
- 2,000 distinct schema loads show 0.00 MB only when the schemas reuse type
  names. **A schema type registration is retained for the life of the
  process.** The derivation registries in `xdm` (`derivedPrimitives`,
  `unionMembers`, `listItems`) are process-global, keyed by expanded QName, and
  have no eviction: an entry outlives the `*Schema` that created it, which is
  itself collected normally. The cost is per *distinct type*, not per load —
  reloading the same schema rewrites the same keys — and is roughly 100 bytes
  per distinct type: 2,000 schemas with unique namespaces and unique type names
  retain 2,000 entries and about 0.20 MB after GC.
  `xsd.TestSchemaTypeRegistryRetainsPerType` pins both facts. A caller who can
  be made to load unbounded *distinct* schemas grows this without bound; one
  that replays the same schemas does not.
- The semantic risk of a shared registry — two schemas defining `{uri}T`
  differently, so a node atomises as whichever loaded last — is mitigated
  separately, by recording the resolved typing on the node at validation time:
  `xdm.Node.DerivedPrimitive`, `UnionMember` and `ListItem` are a per-node
  override of the global answer. See the commentary on those fields in
  `xdm/node.go`.
- The `xpath` regex cache is bounded at 1024, as is the backtracking engine's
  single-character-atom cache; the UCA collation cache is bounded at 256. All
  three hold their bound under concurrent use, not merely on a single goroutine:
  each is a `boundedCache` (`xpath/cache.go`) that performs the full-check and
  the insert under one lock hold. An earlier form checked an atomic size counter
  and inserted into a `sync.Map` as separate steps, which let concurrent callers
  interleave and carry the table past its bound — a peak of 1726 live entries
  against a bound of 1024 was measured with 200 concurrent callers. The overshoot
  scaled with the number of goroutines in flight rather than with the volume of
  input, so it was a violated bound rather than unbounded growth; more requests
  did not enlarge it. The `xsd` model cache is keyed by complex type, which is
  schema-controlled rather than attacker-controlled. The atom cache is reached
  only with `SetBacktrackingRegex(true)`, which is off by default.
- A compiled `Schema` and `Stylesheet` are safe for concurrent use, verified
  under `-race`.

### No unsafe code

No `unsafe`, no `cgo`, no `reflect` in any non-test file.

---

## What a caller must do

1. **Consider the defaults deliberately.** `MaxBytes` (64 MB), `MaxNodes` (10
   million), `MaxDepth` (1000, separately in `xdm`, `xsd`, `relaxng` and
   `xslt`) are set for a general-purpose service. If you know your documents
   are smaller, lower them: they are the bound on what one request can cost
   you.
2. **Leave `AllowDOCTYPE` off** unless a schema you control needs it. Turning it
   on does not reopen XXE, but it is still the wider setting.
3. **Sanitise URLs** if you serve transform output as HTML. XSLT does not, and
   is not supposed to.
4. **Leave `Environment` unset** unless a stylesheet genuinely needs a
   variable, and then expose only that variable rather than reaching for
   `xpath.OSEnvironment`. `fn:environment-variable` and
   `fn:available-environment-variables` withhold everything by default,
   returning the empty sequence — which the spec permits, because it makes
   availability implementation-dependent. Setting a document or text resolver
   does not set this.
5. **Set a `Root`** on `FileResolver`, and an `AllowHost` on `HTTPResolver`, if
   either resolves locations an attacker can influence. A `relaxng.Resolver` is
   your own code and has no such field: it receives the href with `..` intact
   and the scheme filled in, so it must do its own containment check. See the
   interface's documentation for measured examples.
6. **Set a timeout** on the request, and pass the context in. The
   identity-constraint finding above is CPU exhaustion; the depth limit caps it,
   but a `context` deadline is what bounds the general case. Use
   `xsd.Schema.ValidateContext` rather than `Validate`, and
   `xslt.Stylesheet.Transform`, which already takes one — a deadline the
   library never looks at bounds nothing.
7. **Raise `MaxDepth` only deliberately.** Past a few hundred thousand levels
   the XSD validator trades a clean error for an uncatchable stack overflow,
   and raising it also removes the ceiling on the identity-constraint cost. In
   `relaxng` the cost of depth is *quadratic*, so raising it there is the most
   expensive of the four.

## Re-running the audit

The probes are not checked in — they are written against a specific version and
would rot. The method that found these: build a document or stylesheet that
*tries* the attack, run it, and read the actual output rather than the code.
Every finding above was reproduced that way, including two that turned out to be
wrong on first framing.

---

## History

Eight audits have passed over this code. Every finding below was reproduced,
fixed, and pinned by a regression test that fails against the previous code;
the full narrative for each — what it was, how it was reproduced, why the fix
took the shape it did — now lives in [CHANGELOG.md](../CHANGELOG.md). They are
kept here in one line apiece, grouped by audit, so the chronology stays legible
and so a reader who suspects an old bug can tell at a glance whether it was
already found.

Each line names the **direction** of the defect, because that is what decides
who was exposed: a *false accept* let an invalid input through, a *false
reject* refused a legal one, and *cost* produced the right answer too slowly.

**Eighth audit.**

- **A 62-byte self-applying inline function overflowed the stack and killed the process** — availability, and unrecoverable: `recover()` does not catch a Go stack overflow. The dynamic-call path charged no recursion depth and the inline closure dropped `Depth`; both are fixed, so it refuses with `XPDY0001` like every other recursion. See CHANGELOG.
- **`TransformOptions.MaxDepth` did not govern expression recursion** — the same finding's second half: the option bounded templates only, so the XPath side kept its package default of 500 however the caller configured it. It now reaches the XPath context, which both honours a lowered bound and stops a legitimate 530-deep continuation-passing function being refused. See CHANGELOG.
- **`fn:distinct-values` was quadratic on numerics with heavy allocation** — cost, fixed: the pairwise `eq` scan now runs only over the float and double values, because promotion can round only there; integer and decimal key on their exact rational. 100,000 distinct integers fall from 573 s and 480 GB of allocation to 0.15 s and 109 MB. See CHANGELOG.
- **The `MaxItems` budget was not reached on the primary XQuery evaluation path** — cost, and worse than an absent budget: a caller read the documented option and it did not bind. A FLWOR is parsed and evaluated by `xquery` rather than by `xpath`, so its tuple stream reached none of the constructs that charge the budget, and the per-expression reset in `Compiled.Eval` cleared the counter once per tuple. `Context.HoldItemBudget` moves the boundary out to one query evaluation and `flwor.eval` charges both accumulators; the two paths now refuse the same expression at the same point. See CHANGELOG.
- **The process environment was readable by any stylesheet or query** — false accept, the only I/O in the library that failed open. `fn:environment-variable` and `fn:available-environment-variables` now answer from `Context.Environment`, and withhold everything when it is nil. See CHANGELOG.
- **A flat operator chain overflowed the stack during compilation** — availability, `maxParseDepth` counting nesting where the attack was length. Every infix loop in the precedence ladder now charges `maxChainLength`. See CHANGELOG.
- **A string could be doubled past every budget** — availability: `MaxItems` counts items and a string is one item however long it is, so a 1,009-byte expression of twenty-six nested `let`s returned 671,088,640 bytes with no error, and the same chain of `xsl:variable` and `xsl:value-of` did it from a stylesheet. The byte limits above all bound **ingress**; nothing bounded bytes **produced during evaluation**. `xpath.MaxBytes` now charges the constructs that concatenate as they build — held across one query or one transform, because the chain's steps are siblings that a per-expression boundary never sees — and refuses with `XPDY0130`. The bound is 1 GiB against a largest measured legitimate string of 14,516,346 bytes. See CHANGELOG.
- **A function item ran against a byte budget of its own** — cost, and the escape hatch from the finding above: both sites in `xpath/funcitem.go` that hand a call's per-evaluation limits to an invoked function item forwarded `items` and `Depth` but never `bytes`, so a body invoked under a caller that had all but spent `MaxBytes` started from zero. Not reachable from a stylesheet or a query, where the closure captures the same counter lexically; reachable through the public API, because `xdm.FunctionItem.Invoke` is exported and a function item produced under one `Context` can be invoked under another. Each budget now comes from the call together with its hold flag — the counter alone would let a closure captured outside a host's hold reset the caller's charges, which is the leak `HoldByteBudget`'s idempotence guard exists to prevent, arriving through the closure instead. See CHANGELOG.

**Ninth pass — found while verifying an external report, 2026-09-10.**

None of these was a claim in the report. Each was found in the code a claim
pointed at, which is the argument for reading such a report as a map rather
than as a description.

- **`fn:transform` minted a fresh depth allowance at every nesting level** — availability, and the third of this shape: a stylesheet calling `fn:transform` on itself reached a Go stack overflow, which is a runtime fatal `recover()` cannot catch, so the host process died — with `MaxDepth` explicitly set. `rt.depth` is per-runtime and each nested transform built one at zero, so the bound held within a level and never across one. The charge now comes from the call rather than the entry runtime, and the nested runtime continues the count. See CHANGELOG.
- **Every XSD assertion began its own 1 GiB allowance** — cost: `xpath.NewContext` per assertion per element, so a schema with many assertions over a large document had no aggregate bound. Validation has no caller budget to inherit, so the boundary is the validation episode, a third instance of the one `xslt` draws at "one transform". The budget alone was inert: XSD 1.1 makes an evaluation error a false result, so the refusal was reported as `cvc-assertion.3` "not satisfied" — calling the document invalid on a ground the schema never stated — and the walk kept spending. A resource refusal now stops the run, as the depth and error limits do. See CHANGELOG.
- **`xsl:result-document` wrote outside `-result-dir` through a symlink** — false accept, and the only *write* the program makes by name after checking the name: `writeSecondary` resolved symlinks on the root and then `os.Create`d the destination, so a link planted at the href, or at any directory component of it, was followed out of the root. `..` traversal was already refused, so the string check worked and only the filesystem question was unasked. The href may come from the source document through an attribute value template, which puts the untrusted *document* in charge of where the bytes land. It now opens through `os.OpenRoot`, and the directories are created one component at a time through the same root. See CHANGELOG.
- **A function applying itself through its own name recursed uncharged** — availability, and the same defect `35c2e77` closed for a self-applying inline function, in the sibling path: `withRetainedFocus` forwarded the caller's item and byte counters and not `Depth`, so a named reference (`local:f#2`) carried the depth it was *written* at and the charge never accumulated. 100,000 levels returned no error where the inline form refuses at 500, and `TransformOptions.MaxDepth` was bypassed entirely from XSLT. A second instance in `xquery/inline.go` discarded the call context whenever an inline body held XQuery-only syntax, so wrapping a body in `<a>{…}</a>` decided whether the recursion was bounded. `Depth` is an `int` where `items` and `bytes` are pointers, so it is the one budget lost by copying a context rather than forwarding it. See CHANGELOG.
- **An attribute value template built strings past the byte budget** — availability, and the same doubling `b50b373` closed for `xsl:value-of`: `avt.eval` concatenated into a `strings.Builder` with no charge, so a chain of `xsl:variable` bodies each holding `<x a="{$v}{$v}"/>` ran to completion where the text-node form refused at `XPDY0130`. Reproduced at 28 doublings targeting 2 GiB against a 1 GiB bound, with the `xsl:value-of` form as the control. Charged at `avt.eval` now. See CHANGELOG.
- **A binary value could not find its own entry in a map** — false reject: `MapKeyOf` keyed `xs:hexBinary` and `xs:base64Binary` on the lexical form, so `0F` and `0f` — one value under XSD Part 2 §3.2.15 — took different keys, as did base64 values differing only by the whitespace §3.2.16 permits between characters. Values built from element text are unnormalised and `eq` already decoded, so a value could compare equal to a key and still miss the lookup. The key is now the octets. Found by widening the `SameKey` differential corpus, where neither binary type had any coverage. See CHANGELOG.

**Sixth audit.**

- **A second schema silently retyped a document the first had validated** — false accept, process-global type registries. `DerivedPrimitive` and `ListItem` are recorded on the node now. See CHANGELOG. *(One gap remains; see* Current status *above.)*
- **A circular type longer than 4096 links loaded clean** — false accept, `checkTypeBaseCycles` counter, now a visited set. See CHANGELOG.
- **A decimal with more than 4096 fraction digits passed a `fractionDigits` or
  `totalDigits` facet it violates** — false accept, `countDigits`'s
  decimal-expansion loop returning its truncated count as a verdict. The scale
  is now computed exactly from the denominator's `2^a * 5^b` factorisation,
  with no bound to exhaust. See CHANGELOG.
- **RELAX NG refused a legal chain of 501 definitions** — false reject, `maxRefDepth`, removed. See CHANGELOG.
- **A permitted file was read whole with no byte limit** — cost, `FileResolver.MaxBytes`. See CHANGELOG.
- **`keyref` rediscovered its targets once per enclosing scope** — cost, fixed: the walk is pruned and seeded like `key` and `unique`, and per-level table copying was removed. 3.98x per doubling becomes 2.00x. See CHANGELOG.
- **A key ambiguous across three siblings became resolvable again** — false accept, identity-constraint scoping. See CHANGELOG.
- **The language-inclusion procedure declined any bound above 64** — false reject, redundant cliff in front of `subsumeMaxStates`, removed. See CHANGELOG.
- **Six base-chain counters accepted a duplicate `xs:ID` and rejected legal schemas** — false accept *and* false reject; all twelve counters are visited sets now. See CHANGELOG.
- **Occurrence arithmetic wrapped negative, and a bound was compared against garbage** — false accept, sixteen unguarded sites, saturating with exact comparison. See CHANGELOG.
- **Identity constraints were quadratic on recursive elements** — cost. See CHANGELOG.
- **A 3 KB schema took 35 seconds to load, in two places** — cost, exponential path enumeration in `cycleFrom` and `badNestedAll`, both memoised. See CHANGELOG.
- **An assertion rejected a valid document 33 elements deep** — false reject, `maxAnnotateDepth`, removed. See CHANGELOG.

**Fifth audit.**

- **A depth bound on four schema-graph walks accepted documents the schema forbids** — false accept at `depth > 32`, and again at `depth > 64` on two more walks; all now visited sets. See CHANGELOG.

**Fourth audit.**

- **A negative `xsd.ValidateOptions.MaxErrors` approved invalid documents** — false accept. See CHANGELOG.
- **The largest byte limit a caller could name refused every document** — false reject. See CHANGELOG.
- **`AllowHost` is a name check, and said otherwise** — documentation, not behaviour. See CHANGELOG.
- **Filesystem confinement is enforced at open time, not before it** — hardening. See CHANGELOG.
- **The resolver no longer serialises cache misses** — cost. See CHANGELOG.

**Third audit.**

- **Entity expansion was charged once per entity, not once per reference** — cost, and the sharpest of the four: a 70 KB document could allocate hundreds of megabytes with `AllowDOCTYPE: true`. See CHANGELOG.
- **A nested expression could kill the process, not the request** — availability. See CHANGELOG.
- **RELAX NG: nested `oneOrMore` is exponential in document width** — cost. See CHANGELOG.
- **`xsl:analyze-string` ignored the regex step budget** — cost. See CHANGELOG.
- **XSD group references were exponential at schema load** — cost. See CHANGELOG.

**Second audit.**

- **Entity references were expanded inside CDATA, comments and PIs** — false accept. See CHANGELOG.
- **Replacement text was decoded twice** — false accept. See CHANGELOG.
- **Unused entity declarations consumed the expansion budget** — false reject. See CHANGELOG.
- **RELAX NG validation was quadratic in depth with no bound of its own** — cost. See CHANGELOG.

**First audit.** Nine issues, each with a regression test verified to fail
without its fix: only the five predefined entities expand; `AllowHost` is
checked on every redirect hop; `FileResolver.Root` refuses symlinks; computed
names are validated; raw text may not end its element; a nil document is an
error rather than a panic; input size and node count are bounded; validation
depth is bounded separately from parsing; and the transform bound no longer
refuses legal documents. See CHANGELOG.
