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

Seven audits have passed over this code, and the categories below are not
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

Seven audits have passed over this code. This section is the whole of what is
*live*: everything it names is described in full further down, and everything
already fixed has been reduced to one line apiece under *History* at the end,
with the narrative in [CHANGELOG.md](../CHANGELOG.md).

**Open.** Two, both cost rather than correctness, and both needing an
attacker-controlled input the threat model already accounts for:

| finding | reach | why it is still open |
|---|---|---|
| `javascript:` URLs pass through | hostile stylesheet | an XSLT processor is not an HTML sanitiser; see *Open findings*. |

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
`xsd.ValidateOptions` `MaxDepth` / `MaxErrors` ·
`DefaultMaxMatchStates` · `subsumeMaxStates` · `subsumeMaxProduct` ·
`branchLimit` · `maxPositions` · `TransformOptions.MaxDepth` · the RELAX NG
derivative bound · the XPath regex step and depth budgets.

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

**One load-time algorithm is not budgeted, and the omission is deliberate
rather than overlooked.** `checkUPA` (`xsd/upa.go`) is the Unique Particle
Attribution check, and its cost is O(states x pairs): `maxPositions` bounds the
number of positions but not the pairwise scan over them, so the work is cubic
in the size of a content model. Measured through the public `Load` API on a
schema of nothing but optional elements: 116 KB takes 12.6s and 115 MB, 163 KB
takes 1m45s and 369 MB, roughly 8x per doubling. `xsd/complexity_fuzz_test.go`
records this in its inventory and asserts the growth exponent so it cannot
worsen unnoticed.

The reason it is still here is measurement rather than optimism. Instrumented
over every schema in this tree -- 11,610 of them, the whole W3C suite included
-- the widest state any real schema produces is 19 positions; the adversarial
shape above reaches 2,048. So a caller loading schemas it wrote is nowhere near
this, and a caller loading schemas an attacker wrote should not be doing so
without a timeout in any case. The fix is a decision between bounding the scan
and rewriting the element-vs-element test as a name-bucket intersection, and it
is tracked rather than pretended away. Until then: **do not load untrusted
schemas without a wall-clock limit around the call.**

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
[known-gaps.md](known-gaps.md) records which of the remaining bounds have been
probed and found sound.

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
rejected before the filesystem is touched, symlinks are resolved before the
containment check, and a path outside every root is refused. An inclusion
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
one reading at a time — see *Nested occurrence bounds were wrong in both
directions* in [known-gaps.md](known-gaps.md). A set is a thing a schema could
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

The two rooted resolvers enforce their roots by different **mechanisms**, and
the difference is deliberate rather than an oversight. `xslt.FileResolver`
opens through `os.OpenRoot`, which resolves each path component against the
root at open time; `xsd.FileResolver` resolves symlinks with
`filepath.EvalSymlinks`, compares the result against the root, and then opens.
The second is the check-then-use shape that a time-of-check/time-of-use race
attacks in general — but not in this one, because the path that is opened is
the *resolved* one. `EvalSymlinks` returns a path with every link already
followed and the code opens that, so a link that passed the check is never
traversed a second time and cannot be swung between the two steps. A racer
that swaps a symlink inside the root as fast as the filesystem allows, against
a resolver reading in a tight loop, produced over a hundred thousand
successful reads and **zero** that escaped.

What is left is narrower than the general shape suggests: an attacker who can
replace a *directory component of the already-resolved path* between the check
and the open. That requires write access inside the root, and anyone with it
can put the bytes they want in the file directly — the read is no longer the
weak link. This is the sense in which the threat model holds: the party this
document treats as hostile is the *document*, and a document names a location,
it does not get to move files. `os.OpenRoot` would close even the narrow
window, and would be the right change if `xsd` were ever hardened against a
hostile local process sharing the root; it is not adopted today because it
would buy nothing against the attacker this library actually defends against.
The asymmetry is recorded here so that it is a known position rather than a
discrepancy someone rediscovers.

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
4. **Set a `Root`** on `FileResolver`, and an `AllowHost` on `HTTPResolver`, if
   either resolves locations an attacker can influence. A `relaxng.Resolver` is
   your own code and has no such field: it receives the href with `..` intact
   and the scheme filled in, so it must do its own containment check. See the
   interface's documentation for measured examples.
5. **Set a timeout** on the request, and pass the context in. The
   identity-constraint finding above is CPU exhaustion; the depth limit caps it,
   but a `context` deadline is what bounds the general case. Use
   `xsd.Schema.ValidateContext` rather than `Validate`, and
   `xslt.Stylesheet.Transform`, which already takes one — a deadline the
   library never looks at bounds nothing.
6. **Raise `MaxDepth` only deliberately.** Past a few hundred thousand levels
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

Seven audits have passed over this code. Every finding below was reproduced,
fixed, and pinned by a regression test that fails against the previous code;
the full narrative for each — what it was, how it was reproduced, why the fix
took the shape it did — now lives in [CHANGELOG.md](../CHANGELOG.md). They are
kept here in one line apiece, grouped by audit, so the chronology stays legible
and so a reader who suspects an old bug can tell at a glance whether it was
already found.

Each line names the **direction** of the defect, because that is what decides
who was exposed: a *false accept* let an invalid input through, a *false
reject* refused a legal one, and *cost* produced the right answer too slowly.

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
