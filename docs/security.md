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
stylesheet-driven writes. The two things you must still do yourself are **bound
the input size** and **sanitise URLs if you render transform output as HTML**.

---

## Fixed in this audit

Six issues, each with a regression test that was verified to fail without its
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

---

## Open findings

### HIGH — no input size limit; up to 53x memory amplification

`xdm.ParseOptions` has `MaxDepth` but no byte or node cap. Every node costs a
flat ~212 bytes of heap regardless of content, so amplification is driven by
node density:

| document | input | heap | amplification |
|---|---|---|---|
| `<a/>` repeated | 52.4 MB | 2.77 GB | **52.8x** |
| invoice-like | 52.4 MB | 1.24 GB | 23.6x |
| text-heavy | 52.4 MB | 286 MB | 5.5x |

A 100 MB body of empty elements is roughly 5.3 GB of **live retained** heap.

**A caller must bound the input itself** — `http.MaxBytesReader` or an
`io.LimitReader` around the request body. Nothing in the library does it for
you. A byte cap alone is a loose bound given the 5.5x–53x spread; a node cap
would convert directly into a memory bound, and adding `MaxBytes`/`MaxNodes` to
`ParseOptions` is the right fix.

### HIGH — identity constraints are quadratic on recursive elements

Reachable from a hostile instance with default settings, but it needs a schema
where a **recursive** element carries an identity constraint with a `.//`
selector. Independently reproduced:

| depth | input | time | allocated |
|---|---|---|---|
| 60 | 6.9 KB | 9 ms | 5 MB |
| 120 | 14.0 KB | 25 ms | 20 MB |
| 240 | 28.9 KB | 79 ms | 82 MB |
| 480 | 58.7 KB | 304 ms | 332 MB |

Doubling the depth quadruples both. **59 KB of input buys 332 MB of allocation
churn**; at larger depths the reported figures reach 25 s and 9.6 GB for 371 KB.

The cause is structural, not a hot loop: profiling attributes the cost across
`selectNodes` → `buildNodeTable` → `copyEntries`. A constraint on a recursive
element runs at every level of the recursion, and each run selects the whole
remaining subtree *and builds a key table for it*.

A narrower `selectNodes` (walking descendants once for a single-step `.//a`
rather than re-walking from every descendant) was tried and **reverted**: it cut
allocations about 11% and left the curve quadratic, which is not worth a second
code path. The real fix is to compute each subtree's key table once and reuse it
up the recursion.

Peak *live* heap stays low (~29 MB), so this starves a service of CPU rather
than OOM-killing it.

### MEDIUM — a raised `MaxDepth` arms an uncatchable stack overflow

The XSD validator recurses per element depth at roughly 2.9 KB of stack per
level. At the default `MaxDepth` of 1000 this is irrelevant. A caller who raises
it to accommodate a legitimately deep document silently arms a crash: depth
300,000 (a 2.1 MB document) exhausts Go's 1 GB stack limit and produces
`fatal error: stack overflow`, which **`recover()` cannot catch** — it kills the
process, not the request.

`MaxDepth` reads as a parser knob, but it is also the only thing standing
between the validator and that crash. The fix is an explicit depth counter in
the validator so exceeding it is a returnable error.

### INFO — `javascript:` URLs pass through

`<a href="{/d/u}"/>` yields `href="javascript:alert(document.domain)"`. This is
spec-conformant — XSLT does not sanitise URLs, and the value *is* correctly
`&`-escaped. **If you render transform output as HTML, sanitise URL-valued
attributes yourself.**

### LOW — a CR in a text node does not survive a round trip

`escapeText` escapes `&`, `<` and `>` but not `\r`. XML parsers normalise a
literal CR to LF, so an identity transform silently changes the data.
`escapeAttr` handles this correctly with `&#13;`.

### LOW — XSLT refuses documents the parser accepts

The XSLT recursion bound is 300; the parser's depth bound is 1000. An identity
transform over a legal 999-deep document fails with `template recursion exceeded
300 levels`. An availability gap between two limits, not an attack.

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

### All resolution defaults are closed

`doc()`, `document()`, `collection()`, `xsl:include` and `xsl:import` all refuse
when no resolver is configured. `unparsed-text()` is disabled *unconditionally*
— it cannot read a file even with a resolver set. `xsl:result-document` never
writes to disk; the engine returns secondary results to the caller as data.
XInclude is not implemented.

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

1. **Bound the input.** `http.MaxBytesReader` on the request body. Nothing in
   this library caps document size, and the amplification is up to 53x.
2. **Leave `AllowDOCTYPE` off** unless a schema you control needs it. Turning it
   on does not reopen XXE, but it is still the wider setting.
3. **Sanitise URLs** if you serve transform output as HTML. XSLT does not, and
   is not supposed to.
4. **Set a `Root`** on `FileResolver`, and an `AllowHost` on `HTTPResolver`, if
   either resolves locations an attacker can influence.
5. **Set a timeout** on the request. The identity-constraint finding above is
   CPU exhaustion, and a `context` deadline is what bounds it today.
6. **Keep `MaxDepth` at its default** unless you have measured the consequence;
   raising it far past 1000 trades a clean error for an uncatchable crash.

## Re-running the audit

The probes are not checked in — they are written against a specific version and
would rot. The method that found these: build a document or stylesheet that
*tries* the attack, run it, and read the actual output rather than the code.
Every finding above was reproduced that way, including two that turned out to be
wrong on first framing.
