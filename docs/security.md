# Security

What this library defends against, what it does not, and what a caller has to
do. Everything below was tested rather than reasoned about: each claim of
safety names the check that enforces it, and each finding was reproduced before
it was written down.

The threat model throughout is that **the attacker controls the instance
document**. Where a finding needs more than that — a hostile schema, a hostile
stylesheet, a caller-enabled option — it says so, because that changes who is
exposed.

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
could allocate hundreds of megabytes. And if you compile *untrusted schemas*
rather than only validating untrusted documents, one exponential case remains
open and is described under *Open findings*.

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

Two bounds hold the cost of one pass: at most 200 resources read in total, and
at most 40 levels of nesting. Neither substitutes for the other, and neither
substitutes for loop detection — a loop repeats a URI and is caught by name,
while a fan-out of a thousand distinct files repeats nothing and would
otherwise cost a thousand parses.

---

## Fixed in the third audit

Four issues, each with a regression test. Two of the three high-severity ones
were reachable without any opt-in beyond what this document already tells real
callers to set.

### Entity expansion was charged once per entity, not once per reference

**Reachable with `AllowDOCTYPE: true` alone** — no external entities, no
resolver, no hostile schema. `maxTotalEntityBytes` was charged where an
entity's replacement text is first memoised, which happens once per *distinct
name*. The decoder then substituted it any number of times with no further
accounting. Neither `MaxBytes` nor `MaxNodes` caught it: a reference is three
bytes, and a run of them coalesces into a single text node.

A 70 KB document allocated 741 MB and was accepted; a 356 KB one reached 14 GB.
The bound existed and was reported as working because the *rewrite* path
charged per reference — so which of two code paths a document happened to take
decided whether it was bounded at all.

The charge now happens as the document streams past, before the decoder expands
anything. Checking after the parse was tried first and rejected: `encoding/xml`
coalesces a run of references into one token, so the refusal arrived only after
the memory had been allocated.

### A nested expression could kill the process, not the request

**Reachable from a hostile stylesheet or `xs:assert/@test`**, not from a
document. The XPath parser had no depth limit, so deeply nested parentheses
exhausted the goroutine stack. In Go that is a *fatal error*: `recover()`
cannot catch it, so a server does not fail the request, it dies. The XML
parser's element-nesting limit is no help, because the whole expression lives
inside one attribute value. Under a hardened `SetMaxStack`, 14 KB was enough.

Expression nesting is now bounded at 1000 levels and reported as `XPST0003`.

That bound was first described here as "counted at the single point every
nesting construct passes through". It was not: two constructs did not pass
through it, and a fourth audit found both still crashing the process.

- **Sequence types.** `parseSequenceType` recurses into itself for a
  parenthesised item type, for a function test's argument and return types,
  and for the member types of `map()` and `array()` — none through
  `parseExprSingle`. `1 instance of ((((…item()…))))` at 400 KB was enough at
  Go's default 1 GB stack. Reachable through any `@select`, `@test` or `@as`,
  and through `xs:assert/@test`.
- **XSD pattern facets.** The XSD-flavour regex parser recurses once per
  group and counted nothing; a 6 MB schema was enough. Hostile-schema only —
  the XPath-flavour checker is an iterative scanner, so a pattern arriving as
  document data never reached it.

Both are now bounded at 1000 levels, counted at their own recursion points.
The lesson is that "every construct passes through here" is a claim about a
grammar, and it needs a test per construct rather than one test and an
argument: `TestDeepTypeIsRefusedNotFatal` and
`TestDeepSchemaPatternIsRefusedNotFatal` now stand alongside the original.

### RELAX NG: nested `oneOrMore` is exponential in document *width*

**Reachable with default options from a hostile instance** — the most exposed
of the three. A 189-byte schema and a 63-byte instance cost over a second and
more than a gigabyte, growing several times over for every two children added.

`MaxDepth` provably cannot bound this: the document is two levels deep however
wide it grows. The derivative pattern is now bounded by size
(`MaxPatternSize`, default 100,000 nodes), which holds the cost flat.

**This is a bound, not a cure.** The structural fix is interning the pattern
so that shared sub-patterns are not duplicated, which is a redesign of the
derivative engine. A legitimately very wide document validated against a
`oneOrMore` nested in a `oneOrMore` will hit the limit and get an error naming
the cause rather than a verdict. The spec suite passes unchanged.

### `xsl:analyze-string` ignored the regex step budget

**Only when the backtracking matcher is explicitly enabled**, which is a
documented opt-in. The grouping code called the matcher without checking
whether the budget had been exhausted, which the interface's own contract
requires. An exhausted budget was indistinguishable from a genuine non-match,
so the transform silently produced wrong output on exactly the inputs where the
answer was hardest to compute — the guess this package refuses to make
everywhere else. It now raises `XTDE1140`.

### Still open: XSD group references are exponential at schema load

**Hostile schema only**, so materially lower severity than the above: a caller
compiling an untrusted schema has already accepted more than a caller
validating an untrusted document. A 3.8 KB acyclic schema takes over 30
seconds. The cycle check enumerates paths rather than traversing the graph, so
cost is exponential in the depth of the reference DAG while memory stays flat —
invisible to a memory limit. The fix is mark-acyclic memoisation, which
preserves the disjoint-route semantics the current code deliberately keeps.

---

## Fixed in the second audit

Four issues, all in code added after the first audit: the entity-markup
rewrite in `xdm`, and the RELAX NG validator. Each has a regression test.

### Entity references were expanded inside CDATA, comments and PIs

**High.** The rewrite that lets an entity hold markup was a flat byte scan for
`&` with no lexical state, so it expanded references in the three regions XML
defines as *not* recognising one. That was wrong twice over: it expanded a
reference the document meant literally, and it let replacement text close the
region and open a new one.

```xml
<!DOCTYPE r [<!ENTITY e "]]><evil/><![CDATA[">]><r><![CDATA[&e;]]></r>
```

produced a real `<evil/>` element. Entity text became document structure,
silently, and it moved validation verdicts in both directions — a document
valid per spec was rejected, and structure could be smuggled past a validator
whose downstream consumer parses CDATA correctly. The trigger was cheap: any
one entity containing `<` switched the whole document onto that path.

Fixed by giving the scanner the three regions to skip. Verified against
libxml2, which agrees the reference stays literal.

### Replacement text was decoded twice

**Medium.** Expansion decoded `&amp;` because `dec.Entity` substitutes without
re-scanning — but on the rewrite path the text *is* scanned again, so
`&amp;lt;evil/&amp;gt;` became `<evil/>`, manufacturing markup from data the
document had escaped. The same entity gave different results depending on
whether an unrelated entity happened to contain `<`.

A character reference is the opposite case and is still decoded on both paths:
it may form part of a *name*, and a name is not a place a reference survives to
be decoded later.

### Unused entity declarations consumed the expansion budget

**Low.** Testing whether any entity held markup resolved every declaration,
charging unused ones against the shared cap — so a subset full of large unused
entities made a legitimate reference fail with an error about something else.
It also made the result depend on map iteration order, so the same document
parsed differently from run to run. The check now reads the raw text and
resolves nothing.

### RELAX NG validation was quadratic in depth with no bound of its own

**Medium.** Each level of nesting carries the pattern remaining at every level
above it, so cost grows with the square of the depth: 8000 levels cost 487ms
and 911MB, and doubling the depth quadrupled both. `xdm`'s `MaxDepth` capped it
by accident. `relaxng.ValidateOptions.MaxDepth` now bounds it deliberately,
matching what `xsd` already had — a caller who raises the parser's limit, or
builds a tree by transform rather than parsing, has not agreed to spend a
gigabyte validating it.

### Checked and clean

XXE stays closed through the new path — file, HTTP, `PUBLIC`, parameter
entities and external entities reached indirectly through an internal markup
entity were all refused, with a canary HTTP server recording zero hits. Entity
bombs remain bounded through the rewrite, including the many-small-references
case. `MaxBytes`, `MaxNodes` and `MaxDepth` are re-applied to the expanded
text, which was the suspected bypass and is not one. The re-parse cannot
recurse. RELAX NG `Compile` with no resolver refuses every `href`.

---

## Fixed in the first audit

Nine issues, each with a regression test that was verified to fail without its
fix.

### Only the five predefined entities expand

`xdm/parse.go` set the decoder's entity map to `xml.HTMLEntity`, which defines
252 HTML entities, so `&nbsp;` and `&copy;` expanded in a document declaring no
DTD at all. A conforming XML parser must reject an undeclared entity.

Not an injection vector — the map is a fixed table and a numeric reference that
decodes to `&` does not start a second round of expansion (verified: `&#38;#60;`
yields the literal `&#60;`). The risk was divergence: this validator accepted
documents the next consumer in the chain would reject.

### `AllowHost` is checked on every redirect hop

`HTTPResolver.AllowHost` ran once, before the first request, and the default
`http.Client` follows redirects. A schema on a permitted host answering `302`
had the redirect followed and the body returned — the SSRF the field exists to
prevent, reachable through any open redirector on an allowed host.

The returned path was the *original* URL too, so a caller logging it never
learned where the bytes actually came from.

```
named=http://127.0.0.1:60317 (ALLOWED)
redirected to=http://localhost:60315/secret.xsd (DENIED by policy)
result err=<nil> data="INTERNAL-SECRET-SCHEMA"
AllowHost was consulted for: [127.0.0.1]
```

Now checked per hop via `CheckRedirect`, installed on a copy of the client so a
caller's own client is not mutated, and `Resolve` returns the document's real
origin.

### `FileResolver.Root` refuses symlinks

`Root` refused `..`, absolute paths and `file:` URLs, but not symlinks: a link
planted inside the root passed the containment check and `os.Open` then followed
it out. The doc comment already claimed symlinks were refused, and `xslt`'s
resolver had always done it correctly.

Needs an attacker-planted symlink in the schema directory, so it is a
defence-in-depth failure rather than a remote hole.

### Computed names are validated

A name computed by `xsl:element`, `xsl:attribute` or
`xsl:processing-instruction` was checked only for a non-empty local part, and is
written to the output verbatim:

```
name="a><evil/><x"  ->  <r><a><evil/><x>t</a><evil/><x></r>
name="a b"          ->  <r><a b>t</a b></r>
name="1abc"         ->  <r><1abc>t</1abc></r>
```

The processing-instruction target was not checked at all, and its worst case is
quieter than malformed output — a target of `a?><evil/><?b` closed the
instruction, opened an element, and the result **reparsed cleanly as a
different tree**, which nothing downstream would notice.

Both halves of a computed QName must now be NCNames, and a PI target may not be
the reserved name `xml`.

Reachability: needs a stylesheet that computes a name from document data. That
is a normal stylesheet pattern, but it is not the default path.

### Raw text may not end its element

The HTML output method writes `<script>` and `<style>` content unescaped, which
is correct — escaping `&` and `>` there would corrupt a JavaScript comparison.
The rule the spec pairs with that one was missing: content containing `</` ends
the element early and everything after it is markup.

```
<script>var u = "</script><img src=x onerror=alert(1)>";</script>
```

That is the standard XSS primitive, and it cannot be escaped away, so the spec
makes it a serialization error — as `--` in a comment and `?>` in a PI already
were here. Ordinary JavaScript containing `<`, `>` and `&&` still passes through
unescaped, which is the point of the raw-text rule.

### A nil document is an error, not a panic

`Schema.Validate`, `ParseSchema` and `xslt.Compile` dereferenced their document
argument immediately. A caller's mistake rather than an attack — but in a
server, a nil arriving from a failed parse upstream takes down every other
request in the process, not just the one that caused it.

### Input size and node count are bounded

`xdm.ParseOptions` had `MaxDepth` but no byte or node cap, and a node costs a
fixed ~200 bytes whatever it holds — so the heap a document needs follows its
node count, not its length:

| document | input | heap | amplification |
|---|---|---|---|
| `<a/>` repeated | 0.8 MB | 40.7 MB | **53.3x** |
| invoice-like | 11.9 MB | 284.7 MB | 23.9x |
| text-heavy | 39.5 MB | 74.2 MB | 1.9x |

`MaxBytes` (default 64 MB) bounds the read; `MaxNodes` (default 10 million,
about 2 GB of tree) bounds what the read can allocate. Neither alone is a memory
bound, which is why there are two: across a 1.9x–53x spread, a byte cap says
little about the heap. Attributes and namespaces count, because a document of
few elements carrying many attributes allocates most of its memory in those.

The byte limit wraps the reader rather than trusting the caller to check, and
reads one byte past the cap so hitting it is distinguishable from a document of
exactly the maximum size. A negative value disables either check.

**Micro-optimisation was tried first and did not work.** Slab-allocating nodes
cut allocation *count* 13% but raised total bytes, because slabs over-allocate;
interning names gave nothing, because Go already shares the decoder's string
storage. The `Node` struct is 39.7 MB of the 41.6 MB a 200,000-element document
costs, so the limits are the defence. `Node` did lose 8 bytes — `order` narrowed
to `int32` and moved beside `offset` so the two share a word — for about 3%.

### Validation depth is bounded separately from parsing

The XSD validator recurses once per element depth at roughly 3 kB of stack a
level. Exceeding Go's stack limit is `fatal error: stack overflow`, which
**`recover()` cannot catch** — it kills the process, not the request.

`ValidateOptions.MaxDepth` (default 1000) makes that an ordinary validation
error. It is deliberately a separate knob from the parser's: a caller who raises
`xdm.ParseOptions.MaxDepth` to accept a legitimately deep document has not
thereby agreed to arm a crash.

The reported error path is elided in the middle as well — a failure at depth
50,000 produced fifty thousand `/r` segments, which is unreadable and costs more
memory than the error it decorates.

### The transform bound no longer refuses legal documents

XSLT recursion was capped at a fixed 300, below the parser's 1000 — and that
bound counts the ordinary descent of an identity transform, not only a template
calling itself. So a legal 500-deep document could be parsed and then not
transformed.

`TransformOptions.MaxDepth` now defaults to 1000, matching the parser. A
stylesheet with no base case is still caught.

---

## Open findings

### Open: identity constraints are quadratic on recursive elements

Reachable from a hostile instance with default settings, but it needs a schema
where a **recursive** element carries an identity constraint with a `.//`
selector. Independently reproduced:

| depth | input | time | allocated |
|---|---|---|---|
| 60 | 6.9 KB | 9 ms | 5 MB |
| 120 | 14.0 KB | 25 ms | 20 MB |
| 240 | 28.9 KB | 79 ms | 82 MB |
| 480 | 58.7 KB | 304 ms | 332 MB |

Doubling the depth quadruples both.

**The default `MaxDepth` of 1000 bounds it.** At that ceiling the worst case
measured is 111 KB of input costing 1.2 s and 1.2 GB of allocation churn — bad,
but finite, and peak *live* heap stays low, so this starves a service of CPU
rather than OOM-killing it. Raising `MaxDepth` removes that bound.

Two fixes were tried and **both reverted**:

- A narrower `selectNodes`, walking descendants once for a single-step `.//a`
  rather than re-walking from every descendant: cut allocations ~11% and left
  the curve quadratic.
- Memoising selector evaluation per (element, constraint): no effect at all,
  because each level of the recursion is a *different* element, so the cache
  never hits.

The reason it resists a local fix is that **cross-level duplicate detection
needs the whole-subtree walk**. A key at depth 1 and the same key at depth 2 must
collide, and only the ancestor's full walk sees both — verified: that document
is correctly rejected today. `mergeTables` cannot be reused for this, because it
*drops* conflicting entries by design (the spec's rule for tables merged from
below) where `buildNodeTable` must *report* them. A bottom-up rewrite would have
to reproduce that difference exactly, along with per-target error reporting.
That is a redesign of identity-constraint evaluation, not an optimisation.

**A caller can now bound it.** `Schema.ValidateContext(ctx, root, opts)` takes a
context and stops when it ends, returning `context.DeadlineExceeded` or
`context.Canceled` rather than a `*ValidationErrors`. The check sits in the
selected-node loop of `buildNodeTable` and `checkKeyref` — inside the walk whose
cost this finding is about, not merely around it — so a deadline is honoured
mid-run rather than reported afterwards. The quadratic curve is unchanged; what
changes is that a service can put a ceiling on it that does not depend on
`MaxDepth`. `Validate` without a context still runs to completion.

### INFO — `javascript:` URLs pass through

`<a href="{/d/u}"/>` yields `href="javascript:alert(document.domain)"`. This is
spec-conformant — XSLT does not sanitise URLs, and the value *is* correctly
`&`-escaped. **If you render transform output as HTML, sanitise URL-valued
attributes yourself.**

### LOW — a CR in a text node does not survive a round trip

`escapeText` escapes `&`, `<` and `>` but not `\r`. XML parsers normalise a
literal CR to LF, so an identity transform silently changes the data.
`escapeAttr` handles this correctly with `&#13;`.

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
where the answer was hardest to get. Measured from both ends: the hardest
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

The per-entity figure is measured rather than chosen: a first attempt used
1 MB, and a five-level billion-laughs reaching 100,000 bytes parsed cleanly
through it. The regression test that caught that is `TestEntityExpansionBlowupIsRefused`.

### All resolution defaults are closed

`doc()`, `document()`, `collection()`, `xsl:include` and `xsl:import` all refuse
when no resolver is configured. `collection()` has its own switch —
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
- 20,000 parse-and-validate cycles and 2,000 distinct schema loads each show
  **0.00 MB** heap growth after GC.
- The `xpath` regex cache is bounded at 1024; the `xsd` model cache is keyed by
  complex type, which is schema-controlled rather than attacker-controlled.
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
