# Changelog

Notable changes, newest first. Versions follow [semantic
versioning](https://semver.org): from 1.0.0 the exported API is stable, and a
breaking change means 2.0 with a new module path. See *Stability* below.

## Unreleased

### Fixed — CI

- **The `w3cschemas` module was testing a published release, not this tree.**
  It is a separate module — the W3C documents it bundles are under W3C terms
  rather than MIT — and it was in no gate at all: absent from `ci.yml`, absent
  from `tests/check.sh`, and out of reach of `go list ./...` at the root, which
  does not cross a module boundary. Its `go.mod` required
  `github.com/knroy/go-xml v1.1.0`, so its imports of `xsd` and `xdm` resolved
  to the module cache rather than to the working tree:
  `go list -f '{{.Dir}}' github.com/knroy/go-xml/xsd` inside it answered
  `…/pkg/mod/github.com/knroy/go-xml@v1.1.0/xsd`. Its tests passed while
  exercising a release roughly three hundred commits behind, so an API break in
  the two packages changed most this cycle could not have failed them.

  A committed `go.work` at the repository root now redirects the dependency to
  `.`. The redirection is kept out of `w3cschemas/go.mod` deliberately: that
  module is published (tag `w3cschemas/v0.1.0`), and a `replace` in a published
  `go.mod` has to be stripped at every tag or it misleads whoever reads it.
  `go.work` names only `.` and `./w3cschemas`, so it reproduces identically for
  every developer and for CI, which is the case where committing a workspace is
  correct.

  A workspace alone was not enough, and the reason is worth recording: a 1.25
  toolchain rejected the module *before* consulting the workspace, because MVS
  reads the required version's own `go.mod` regardless of any replacement, and
  both v1.1.0 and v1.2.0 declare `go 1.26`. Since CI pins `go-version: '1.25'`,
  the module would have failed there even wired correctly. The `require` now
  names v1.2.1, which declares `go 1.25.0`. That in turn let `w3cschemas/go.mod`
  drop from `go 1.26` to `go 1.25.0` and `golang.org/x/text` from v0.37.0 to
  v0.36.0, both now identical to the root module — the dependency skew is gone
  and the module builds standalone on 1.25 as well as inside the workspace. The
  `go 1.26` was never a language requirement; it was the stale pin showing
  through, which is why `go mod tidy` kept restoring it.

  Finally, a step of its own in `tests/check.sh` and in `ci.yml`'s fast job. A
  workspace makes the module compile against the right code; it does not cause
  anything to run it.

- **Investigated, not changed: the `XSpec 225` ratchet mark is correct.** It
  was reported as stale on the grounds that the corpus measured 224. It does
  not: three runs — twice at `HEAD` and once at `e51ed3f`, the last green
  commit before this cycle — each transformed **225 of 284** inputs with a
  byte-identical set of 59 failures, against a clean `testdata/xspec` at
  `799d52a`. So there is no engine regression and no upstream drift, and the
  mark is left alone. Timeouts are not the explanation either, which was the
  other candidate given how load-sensitive this script has been before: the
  slowest *passing* case, `report-sequence.xspec`, takes 871ms against the
  120s deadline `check.sh` passes, a margin of 138×. The 224 could not be
  reproduced. Recorded here so the mark is not lowered on the strength of a
  single measurement — lowering it is exactly how a real regression would get
  normalised away.

- **A unit test that cost minutes broke both CI jobs.**
  `TestMaxPositionsRealBoundary`, added in `84735c8`, compiled three content
  models of ~8,192 particles to drive `maxPositions` at its edges.
  `compileContentModel` is cubic in the particle count (1,024 particles in
  0.16s, 2,048 in 1.4s, 4,096 in 12s, 8,192 in 90s on an idle 12-core laptop),
  so the test ran 202s there and, under `-race` on a two-core runner, did not
  finish inside the 25-minute `go test` deadline. That failed the `test` job
  and — because `tests/check.sh` runs the same `-race` step before any suite —
  the `conformance` job too, which is why one bug looked like two. The
  boundary is now driven at a forced budget of 64 through `withBudgets`, as the
  neighbouring `TestMaxPositionsBoundary` already did; the off-by-one asserted
  is a property of the comparison, not of the constant. A new
  `TestMaxPositionsProductionValue` pins the shipped 8,192 so lowering it stays
  deliberate. Full `-race` run at `GOMAXPROCS=2`: never finished (>25m) before,
  1m17s after.

- **`TestQT3` and the RELAX NG spectest are now ratcheted.** Both were run and
  printed but not recorded, so an XPath 2.0 or RELAX NG count could fall
  without `check.sh` saying anything. `TestQT3` logs one `in-scope:` line per
  language version and the mark is taken from the last of them; the spectest
  driver reports `N assertions, M passed`, so its count is extracted and handed
  to `ratchetCount`. `tests/ratchet.txt` goes from seven marks to nine.

### Security

- **`xsd` no longer installs an unconfined file resolver by default.**
  `Load`, `LoadFile`, `LoadFiles` and `WithInstanceLocations` each defaulted a
  nil `Options.Resolver` to a `FileResolver` with no `Root` — "any readable
  path", by that field's own comment. A zero-value `Options` therefore opened
  and read whatever an `xs:include` named: an absolute `/etc/hosts` came back
  as a parse error *about the contents of /etc/hosts*, which is the proof the
  read happened. `SECURITY.md` claimed there was "no default resolver anywhere
  in this library, in any package", and for `xsd` that was false.

  It was reachable from outside the package. `xslt` refuses an
  `xsl:import-schema` naming a schema-location when no `SchemaResolver` is
  configured, then passed that same nil to `xsd.Load` for an *inline*
  `<xs:schema>` — whose own `xs:include` was followed anyway. A caller who
  hardened `xslt` still had the filesystem readable through a stylesheet.

  The default now follows the grant the caller already made. `Load` takes a
  tree and no path, so it refuses; a self-contained schema, which is nearly
  every use of `Load`, is unaffected because it asks for nothing. `LoadFile`
  and `LoadFiles` were handed paths, so a sibling include still resolves — but
  the default is rooted at the directories the caller named, so an absolute
  path elsewhere or a climb through `..` is refused, which the unrooted
  default permitted. `WithInstanceLocations` is rooted the same way, and it
  mattered most there: its locations come from the instance.

- **A resolution the configuration refused is now reported instead of
  dropped.** §4.2.1 permits an `xs:include` whose location cannot be resolved
  to contribute nothing, and that is right for a location looked for and not
  found — a remote URL with no network resolver, most commonly. It is wrong
  for "no resolver is configured" and "outside the permitted root", which are
  decisions rather than misses: a caller who hardened got a schema quietly
  missing components and a `nil` error, and then validated documents against
  what was left. Both now surface as `src-resolve`. `xs:import` keeps the
  silent path in both cases, because §5.3 *Missing Sub-components* gives an
  unfetched namespace a defined outcome and every conforming processor loads
  such a schema — the W3C suite's own `common/xsts.xsd` is exactly that.

### Documentation

- **The two rooted file resolvers enforce their roots by different mechanisms,
  and that is now written down.** `xslt.FileResolver` opens through
  `os.OpenRoot`, which enforces at open time; `xsd.FileResolver` resolves
  symlinks, compares against the root, then opens — the check-then-use shape a
  TOCTOU race attacks in general. It was reviewed and deliberately kept. What
  is opened is the *resolved* path, so a link that passed the check is never
  traversed again and cannot be swung between the two steps; a racer swapping a
  symlink inside the root as fast as the filesystem allows, against a resolver
  reading in a tight loop, produced over a hundred thousand successful reads
  and zero escapes. The residual window needs an attacker who can replace a
  directory component of the already-resolved path, which requires write access
  inside the root — and anyone with that can write the file directly. The party
  this library treats as hostile is the *document*, which names a location and
  cannot move files. `docs/security.md` and the code comment now state the
  asymmetry and the reasoning rather than leaving it to be rediscovered.

### Fixed

- **`INF`, `-INF` and `NaN` no longer satisfy every bound facet.** `checkBounds`
  compared with `big.Rat` and read a lexical with no rational form as "no
  opinion", returning success: with `xs:double` constrained to `0..100`, `101`
  was correctly refused while `INF`, `-INF` and `NaN` were all accepted. The
  comment explaining this had the right premise and the inverted conclusion —
  being outside any bound the schema can write is exactly what makes these
  values invalid.

  §3.3.5 and §3.3.6 put `positiveInfinity` above every finite value and
  `negativeInfinity` below every one, and leave `NaN` incomparable to
  everything including itself. So `INF` fails `maxInclusive="100"`, `-INF`
  fails `minInclusive="0"`, and `NaN` fails *any* ordering facet the type
  carries — min or max, inclusive or exclusive — because satisfying one would
  need a comparison that does not hold. A bound may itself be `INF`, and then
  it bounds nothing on that side, which is the pair `jsonschema.go` writes for
  a JSON number.

  The gap was in the facet path only. The same value in an XSD 1.1
  `xs:assert` was already refused correctly, and `enumeration` was already
  right because it is equality rather than order — a test now holds that,
  so a later change to the bound comparison cannot take it along.

- **A derived type could widen a bound to `INF` and the schema still loaded.**
  `compareBoundValues` returned "unordered" for a bound with no rational form
  and `checkBoundOrder` reads unordered as satisfied, so a restriction of a
  base with `maxInclusive="10"` could set `maxInclusive="INF"` without
  complaint, which §4.3.7.4 forbids. This had no instance consequence — the
  facet chain still enforced the base's bound at validation — so it was a
  missing diagnostic rather than a false accept. `NaN` stays unordered, because
  it genuinely orders against nothing and refusing would assert an order it
  does not have.

- **A validated `xs:double` that overflowed stopped being a double.** Building
  a typed value from a schema annotation parsed the lexical form with
  `strconv.ParseFloat` and treated any error as "not a lexical form of this
  type". `ParseFloat("1e400", 64)` returns `+Inf` *with* `strconv.ErrRange` —
  the right value carried by an error — and the fallback switch that follows
  only recognises the literals `INF`, `-INF` and `NaN`, so `1e400` matched
  nothing and the function returned no atomic at all. The caller then fell back
  to `xs:untypedAtomic`: a node the schema had validated as `xs:double`, and
  annotated as one, atomised as untyped, so it compared as a string and lost
  every numeric operation. This is the third instance of the same
  `ParseFloat`-error confusion, after `xpath/cast.go` and `xpath/fn_json.go`,
  and it is fixed the same way — discard `strconv.ErrRange` and keep the value,
  which F&O 3.0 §4.2 permits and which the other two sites already do. It
  reached only *typed* data, which is why the earlier fixes' tests did not see
  it. Underflow was never affected: Go returns `0` with a nil error for it.
  `xs:float` is fixed with the same change, `NewFloat` narrowing `+Inf` through
  `float32` as before.

- **A processing instruction in an internal DTD subset broke the subset scan.**
  XML 1.0 §2.8 admits a PI to `intSubset` — `markupdecl` is "elementdecl |
  AttlistDecl | EntityDecl | NotationDecl | PI | Comment" — and §2.6 makes its
  content text rather than markup. `endOfInternalSubset` skipped comments for
  exactly this reason but not PIs, so a `]`, a `>` or a quote written inside
  one was read as structure: `<?p a ] b ?>` ended the subset in the middle of
  the declarations, and the entity references in the body were then rewritten
  against a boundary in the wrong place. The skip now covers both constructs.
  CDATA needs none: `CDSect` belongs to `content`, not to `intSubset`, so one
  written there is malformed and the decoder rejects it. A PI holding an
  unbalanced apostrophe, or a bare `>`, is still refused — `encoding/xml` ends
  its `Directive` token at that character and never delivers the DOCTYPE whole,
  which is a defect in the standard library's directive scanner and not in this
  one.


### Changed

- **A number too large for a double now casts to `INF` instead of being
  rejected as a malformed lexical form.** `xs:double("1e400")` raised
  `FORG0001: invalid xs:double value`, but `1e400` *is* a well-formed
  `xs:double` lexical form — the grammar puts no upper bound on the exponent,
  and the shape check accepted it. What failed was the parse, with
  `strconv.ErrRange`, and a range error was being reported as a lexical one.
  F&O 3.0 §18.3 sends a value "too large or too small to be accurately
  represented" to §4.2, which permits exactly three behaviours for an
  `xs:double` or `xs:float` overflow: raise `FOAR0002`, return `±INF`, or
  return the largest finite value. `FORG0001` is none of the three. The engine
  also contradicted itself four ways: the literal `1e400` was already `INF`,
  `1e308 * 10` was already `INF`, `xs:float("1e39")` was `INF` while
  `xs:float("1e400")` was an error, and underflow was handled correctly, so
  only the overflow direction was broken. `±INF` is now returned throughout, in
  line with what the lexer and the arithmetic operators had already chosen.
  The silent case was the worse one: `fn:number` turns a failed cast into
  `NaN` by design, so `number("1e400")` answered `NaN` for a value it could
  convert, and `<v>1e400</v>` made `/r/v > 0` false with nothing to signal it;
  `sum`, `avg` and `max` over the same document raised `FORG0001`. A
  malformed form is still `FORG0001`, and the spellings XML Schema does not
  admit — `Inf`, `Infinity`, `+Inf`, `0x10` — are still rejected. The same
  mistake had a second, independent copy in the JSON scanner, where
  `parse-json('1e400')` raised `FOJS0001` for text JSON permits while
  `json-to-xml('1e400')` carried the identical lexeme through untouched.

- **The `html` and `xhtml` output methods no longer indent inside `pre` and
  `textarea`.** With `indent="yes"`, an element-only `<pre><span>func</span>
  <span> main</span></pre>` came out across three indented lines, changing the
  rendered text from `func main` to a three-line block; inside a `<textarea>`
  the inserted whitespace becomes part of the value the control submits.
  Serialization 3.1 §5 grants the licence this serialiser cites for indenting
  at all — whitespace may be added "only where the effect is not significant"
  — and inside these elements every user agent renders it literally. Only
  element-only content was exposed: an element with a non-whitespace text
  child was already spared by the mixed-content rule, so `<pre>a\nb</pre>` was
  never at risk, but element-only content is exactly what a syntax highlighter
  or a DocBook-style stylesheet emits. The mechanism already existed —
  `suppress-indentation="pre textarea"` produced correct output — so what was
  missing was the default. The set is `pre`, `listing`, `plaintext`,
  `textarea` and `xmp`: HTML 4.01 §9.3.4 names the first, third, fifth and
  `listing` as the elements where white space is significant and §17.7 makes
  `textarea`'s content the control's literal initial value, while HTML5's
  default style sheet applies `white-space: pre` to the same five. Both
  versions agree, so unlike the void-element tables there is no version split.
  `script` and `style` are absent because they are never indented anyway. The
  `xml` method is deliberately unchanged and pinned by a test: it serialises a
  tree with no HTML semantics, where `<pre>` is an ordinary element name and
  indenting it is correct.

- **Dividing one duration by another now returns the same `xs:decimal` as
  dividing the equivalent numbers.** `xs:dayTimeDuration("PT1S") div
  xs:dayTimeDuration("PT3S")` kept the exact rational 1/3, while `1 div 3` is
  normalized to the 18 fraction digits `xs:decimal` supports. The two printed
  identically, compared unequal, and their difference printed as `0` without
  being zero. F&O defines both duration-division operators by delegation to
  `op:numeric-divide` -- the day/time ratio "by applying `op:numeric-divide` to
  the two `xs:decimal` operands", the year/month ratio "according to the rules
  of the `op:numeric-divide` function for integer operands" -- so they take
  that function's precision, and §9.7.1 scopes duration arithmetic to what
  happens when a processor limits the digits in an `xs:decimal`. Both paths now
  round like ordinary division. Terminating ratios are unchanged: `PT1S div
  PT2S` is still exactly `0.5`.

- **A singleton parameter given two items is now a type error rather than a
  wrong answer.** `fn:format-number`, `fn:format-integer`, `fn:format-date`
  and its siblings, `fn:dateTime`, `fn:error`, `fn:key` and `fn:regex-group`
  all read their argument as `xdm.Atomize(args[i])` followed by `atoms[0]`,
  which takes the first item of whatever it is handed. Every one of those
  parameters is declared as a singleton by the spec — `numeric?`,
  `xs:integer?`, `xs:date?`, `xs:QName?`, `xs:string`, `xs:integer` — so a
  two-item argument is XPTY0004 under the function conversion rules, not a
  request to use the first item and discard the rest. `format-number((1,2),
  '0')` answered `"1"`; it now raises. New helpers `argAtomicOptional` and
  `argAtomicRequired` (`xpath/argcardinality.go`) express the two
  cardinalities in one place, and atomise through `xdm.AtomizeChecked`, so a
  function item reaching one of these arguments is FOTY0013 rather than being
  silently dropped. Parameters genuinely declared `item()*` or
  `xs:anyAtomicType*` — `fn:key`'s `$key-value`, `fn:id`'s argument — are
  unchanged and still take a sequence.

- **A resource refusal can be told apart from a malformed expression.** New
  `xdm.ErrResourceLimit` is a sentinel that resource-exhaustion errors wrap
  with `%w`, so a caller can ask `errors.Is(err, xdm.ErrResourceLimit)`. The
  two conditions arrived indistinguishable: the parser's nesting guard reports
  XPST0003 because that is the code the specs give for an unacceptable
  expression, and an embedding caller reading the code alone told its user the
  expression was invalid when in fact it was merely deeper than this processor
  will parse. The specs give no code for "the processor declined", so the code
  cannot carry this and a sentinel does. Wrapping **adds** the sentinel and
  changes neither the code nor the leading message text, so `xdm.ErrorCode`,
  the conformance suites and the tests that match on message text are all
  unaffected. Wired up at the two limits in `xpath`: the parse-depth guard
  (`xpath/parser.go`) and the evaluation item budget (`xpath/context.go`).

- **One decimal magnitude primitive replaces three copies of the same
  mathematics.** `decimalScale` (`xdm/atomic.go`) and `decimalMagnitudeOf`
  (`xsd/facet.go`) each factored a `big.Rat` denominator into 2^a*5^b to find
  a decimal scale, by slightly different routes, and `ratScale`
  (`xpath/fn_node.go`) does it a third time. The first two now call
  `xdm.DecimalMagnitudeOf`, which returns the exact coefficient and scale or
  an explicit false; `ratScale` is left for a follow-up.

  The primitive takes the *strict* contract, which is the point of the change
  rather than a detail of it. The two callers deliberately differed at the
  edges: the renderer answered 18 for a value with no finite expansion and 1
  for an integer, while the facet check refused to answer at all, because its
  answer decides validity and an invented digit count would let 1/3 pass a
  `fractionDigits` facet it violates. Sharing the *approximating* contract
  would have leaked a renderer's fallback into a validity decision, so the
  fallbacks moved outward: they now sit visibly in `decimalScale`, where the
  comment says which is a policy and why, and nothing else can pick them up by
  accident. Behaviour at every existing call site is unchanged, pinned by
  characterization tests written before the refactor.

  `countDigits` counting the leading zeros of `0.001` as three total digits is
  likewise pinned, with the citation. It looks like an off-by-one and is not:
  §4.3.12.4 requires `fractionDigits <= totalDigits`, so total=1 frac=3 would
  make `0.001` unrepresentable by any conforming schema.

### Fixed

- **RELAX NG compared `xs:integer` and `xs:decimal` bounds through
  `float64`.** `parseNumber` (`relaxng/datatype_xsd.go`) read both the instance
  value and the `minInclusive`/`maxInclusive`/`minExclusive`/`maxExclusive`
  parameter with `strconv.ParseFloat`, and `checkBound` compared the two as
  `float64`. Above 2^53 consecutive integers share a `float64`, so a
  `maxInclusive` of `9007199254740992` accepted `9007199254740993`, and a
  `maxInclusive` of `1.00000000000000000001` on `xs:decimal` accepted
  `1.00000000000000000002`. Both directions were affected, and the exclusive
  bounds inverted at the boundary as well -- a `maxExclusive` of
  `9007199254740993` rejected the `9007199254740992` it should admit. These are
  false accepts and false rejects on arbitrary-precision types, which
  `xs:integer` and `xs:decimal` both are. Comparison now goes through
  `math/big.Rat`, matching what `checkBounds` in `xsd/validate_simple.go`
  already did; `xs:float` and `xs:double` keep `float64`, since that is
  genuinely their value space, and `INF`/`NaN` -- which have no rational form
  and can only arise there -- are carried as floats, with `NaN` reported as
  unordered rather than silently satisfying a bound. This is the same defect
  class as the sweep in "an exact value must never be routed through float64",
  which did not reach `relaxng/`.

- **An out-of-range `length` parameter in RELAX NG became a bound that
  accepted everything.** `atoiParam` (`relaxng/datatype_xsd.go`) accumulated
  `n = n*10 + int(c-'0')` with no overflow guard, so a `minLength` of
  `9223372036854775808` wrapped to a negative `int`. The comparison `l < n` was
  then never true, and a parameter written to reject every string accepted
  every string instead. A length too large to represent names no bound at all,
  so it is now refused as a schema error at compile time rather than at first
  use -- whether a parameter is well formed does not depend on a value, unlike
  a facet violation, which does. This matches `uintFacet` in
  `xsd/parse_type.go`, which already rejected an unrepresentable length facet.

- **A grammar reached through `<include>` was not checked against section 7.**
  `collectInclude` (`relaxng/compile.go`) ran `checkSyntax` on the included
  document but not `checkRestrictions`, unlike the top-level and
  `<externalRef>` paths, which run both. `derive.go` assumes every pattern it
  sees has already passed `restrict.go`, so a construct the restrictions forbid
  -- an `<attribute>` inside an `<attribute>`, a `<text>` inside a `<list>` --
  could reach the deriver from an included file, and the schema compiled
  without error instead of being rejected. The included document is now checked
  the same way the others are.

- **A resource guard on `fn:round` was changing answers, not just refusing
  them.** `roundGuard` (`xpath/fn_node.go`) clamped the requested precision on
  both sides, and its comment asserted that this "changes how long a hopeless
  request takes to refuse, never an answer". That was false, and was the fixed
  `±4096` clamp relocated rather than removed: `round-half-to-even(1 div
  2^1048579, 1048577)` was answered at precision 1048576, a different number,
  and `round-half-to-even(3e1048586, -1048581)` returned `0` where rounding
  below a value's lowest significant digit discards only zeros and must return
  the value unchanged. Both were silent — no error, just a wrong number. The
  guard now applies only to a non-terminating rational, the sole case with no
  finite scale to bound it; for a terminating value the work is already bounded
  by the value's own scale (positive precision) or integer-digit count
  (negative precision), which is proportional to the caller's own input, so no
  constant belongs there. The asserted comment is replaced by the property that
  actually holds, and `TestRoundGuardReductionPreservesValue`
  (`xpath/round_guard_test.go`) enforces it over generated `xs:decimal` and
  `xs:integer` values with scales and magnitudes on both sides of the guard.

- **Four silent numeric narrowings: a wrong value, with no error raised.**
  `big.Int.Int64` is *undefined* out of range rather than saturating, so an
  unbounded `xs:integer` argument arrived as its low 64 bits and the result was
  computed from that — and in each case the wrapped value was itself plausible,
  so nothing failed. `round(1.55, 2^64+1)` wrapped the precision to `1` and
  answered `1.6` instead of `1.55`; `codepoints-to-string(2^64+65)` wrapped to
  `65` and returned `"A"` instead of raising FOCH0001; and
  `function-available('name', 2^64+1)` wrapped to `1`, found `fn:name#1`, and
  answered `true`. A fourth was a two's-complement trap rather than a wrap:
  `roundPlaces` tested `-places > intDigits+1`, and negating `places` overflows
  for `math.MinInt64`, so `round(1234.5, -2^63)` fell through the zero test and
  was answered at precision 0, giving `1235` where the true answer is `0`. The
  precision is now read through a saturating `saturateInt64`
  (`xpath/fn_node.go`) — exact, not merely safe, since no `xs:decimal` has a
  scale or integer-digit count near 2^63, so every out-of-range precision is
  already past both of `roundPlaces`' bounds and the saturated value lands on
  the same side; the comparison is written `places < -(intDigits+1)` so nothing
  is negated; `fn:codepoints-to-string` (`xpath/fn_string.go`) checks
  `FitsInt64` first, so an out-of-range codepoint is refused by its own digits
  rather than by whatever its low 64 bits spell; and `fn:function-available`
  (`xslt/rtfuncs.go`) both converts exactly and bounds the arity by
  `maxAvailableArity`, mirroring `fn:function-lookup` — exact conversion alone
  is insufficient, because an exactly-converted 2^62 still names no function.
  `xpath/narrowing_test.go` and `xslt/functionavailable_arity_test.go` assert
  the resulting values across the int32, float64-integer, int64 and pure-bignum
  boundaries; every case asserts a value or a specific error code, since a test
  that only checks for the absence of an error cannot see this class of bug.

- **`xsl:number value=` rejected a finite `xs:integer` above the float64
  range, and silently narrowed one under `version="1.0"`.** The same defect as
  the `idiv` and cast ones below, at the last two sites the audit found.
  `numberValueOf` (`xslt/grouping.go`) guarded its conversion with
  `math.IsNaN(f) || math.IsInf(f, 0)` on the value cast to `xs:double`, so
  `<xsl:number value="xs:integer('1' followed by 309 zeros)"/>` raised
  `XTDE0980` for a number the data model holds exactly — with the boundary on
  the float64 exponent limit, 10^308 fine and 10^309 not. The exact `Rat()`
  path directly below the guard already computed the right answer; the guard
  rejected the value before it could get there. The test is now skipped
  exactly when the item has an exact value of its own, which is precisely when
  that path is taken, so a genuine `xs:double` `INF` or `NaN` — and a
  non-numeric value such as `'apples'` — still raises `XTDE0980` as before.

  The backwards-compatible path a hundred lines above did the whole
  conversion in `float64` and `int64` rather than only guarding with it, so
  under `version="1.0"` an `xs:integer` above the float64 range formatted as
  the string `"NaN"`, one above 2^53 lost its low digits with no error at all,
  and one above the int64 range clamped to `9223372036854775807` — 10^24 came
  out as 9223372036854775807. It now calls `numberValueOf` for the conversion
  and keeps only XSLT 1.0's *outcomes*: an infinity, a `NaN`, a negative and a
  non-number all format as the string `"NaN"`, since XSLT 1.0 had no
  `XTDE0980` to raise. Values that reach that path through 1.0 arithmetic are
  unaffected and still narrow, because XPath 1.0 compatibility casts the
  operands of `+ - * div` to `xs:double` unconditionally (B.1) — there the
  value really is a double by the time `xsl:number` sees it.

- **`idiv` raised `FOAR0002` for a finite `xs:integer` above the float64
  range, and answered zero for a finite huge divisor.** The NaN/infinite guard
  in `integerDivide` (`xpath/operators.go`) asked
  `math.IsInf(a.Float64(), 0)`. `Float64()` on an arbitrary-precision
  `xs:integer` or `xs:decimal` overflows to `+Inf` above the float64 range, so
  the guard was answering a question about a float64 *projection* of the value
  rather than about the value. `xs:integer('1' followed by 309 zeros) idiv 1`
  raised `FOAR0002` for a perfectly finite number, with the boundary sitting
  exactly on the float64 exponent limit — 10^308 fine, 10^309 not — which is
  not a boundary the XPath data model has. The divisor side of the same guard
  sent a finite huge divisor down the "an infinite divisor truncates to zero"
  path, so `3×10^400 idiv 10^400` answered `0` instead of `3`; that fault was
  invisible until the dividend guard was fixed, because the dividend check
  fired first. Both now go through a value-level `isInfinite`, which only
  `xs:double` and `xs:float` can ever satisfy. `mod` was already correct: its
  exact path never consults `Float64()`. The general rule, which the comment
  now records: an arbitrary-precision value must never be routed through
  float64 to answer a question about itself.

- **Casting a finite `xs:decimal` or `xs:integer` above the float64 range
  raised `FOCA0002`.** The same defect as the `idiv` one above, at two more
  sites. `castToNumeric` (`xpath/cast.go`) guarded both the `xs:integer` and
  the `xs:decimal` target with `math.IsInf(a.Float64(), 0)`, so a 401-digit
  `xs:decimal` cast to `xs:integer`, and `xs:integer('1' followed by 400
  zeros)` cast to `xs:decimal`, were refused as infinite. The boundary was the
  float64 exponent limit again — 10^308 fine, 10^309 not. Both now use the
  value-level `isInfinite` helper, which tests the type first: only `xs:double`
  and `xs:float` can be infinite, and casting one of those to an exact type
  still raises `FOCA0002`, which the new tests pin in both directions.

- **`fn:format-integer` silently truncated any value a `float64` could not
  hold.** `integerValueOf` (`xpath/fn_formatinteger.go`) returned
  `int64(conv.Float64())`. This one raised no error at all: `format-integer(10^400,
  '1')` answered `9223372036854775807`, and `format-integer(123456789012345678,
  '1')` answered `123456789012345680`, because a double holds about 17
  significant digits and the `int64` conversion then saturates. `$value` is
  declared `xs:integer?`, which is unbounded, and F&O gives no error code for a
  value that is merely large — so the value is now carried as a sign and an
  exact decimal digit string and formatted, not refused. A decimal-digit-pattern
  pads and groups those digits directly; the named sequences (Roman, letters,
  spelled-out words) keep an `int64`, since a value too large for one is past
  every one of their ranges anyway and takes the fallback to the digit string
  that those sequences already produce there. Because the sign is now taken off
  before formatting, `format-integer(-9223372036854775808, '1')` also stops
  answering `--9223372036854775808`: the old `if n < 0 { n = -n }` left
  `math.MinInt64` negative and the minus was prepended to a string that had one.

- **Scaling a duration by a finite factor above the float64 range took the
  infinity path.** A fourth instance of the same pattern, found while fixing
  the casts. `scaleDuration` (`xpath/cast.go`) asked
  `math.IsInf(n.Float64(), 0)` of the scaling factor, so an `xs:integer` or
  `xs:decimal` factor past the float64 range was treated as an infinity:
  multiplying reported `FODT0002` without ever looking at whether the result
  overflowed, and — silently, which is worse — dividing took the "dividing by
  an infinity shrinks the duration to nothing" branch, so
  `xs:dayTimeDuration('PT1S') div xs:integer(10^309)` answered `PT0S` for an
  ordinary representable division. A genuine overflow is still caught by the
  range check on the scaled value below the branch, which is its right place.

- **A `fractionDigits` or `totalDigits` facet accepted a value that violates
  it, once the value had more than 4096 fraction digits.** `countDigits` in
  `xsd/facet.go` derived both counts by multiplying the value by ten until it
  became integral, bounded by `const maxScale = 4096`, and on hitting the bound
  returned the truncated count as though the walk had finished. A value with
  4600 fraction digits therefore reported 4096 of them and passed
  `fractionDigits="4500"`, which it violates; the same truncation defeated
  `totalDigits`. An invalid document accepted as valid is the worst failure a
  validator has, and this one needed no malice to hit — only a long decimal.

  The bound was not merely too small, and raising it would only have moved the
  cliff to a value nobody would test. The counts are now computed exactly and
  in closed form. `big.Rat` holds lowest terms, so a value is an exact decimal
  precisely when its denominator factors as `2^a * 5^b` with nothing left over,
  and then `fractionDigits` is `max(a, b)` — no expansion, and no bound to
  exceed. `totalDigits` is the digit count of `numerator * 2^(scale-a) *
  5^(scale-b)`, the value written at exactly that scale, raised to the scale
  itself when the integer part is zero so that the leading zeros of `0.001`
  still count three. A value with no terminating expansion now reports that
  fact instead of a truncated count; `isDecimalLexical` admits only a sign,
  digits and one point, so no `xs:decimal` literal can produce one, but a count
  is no longer invented for a value that does not have one.

  Purely integral values were never affected, since the old loop did not run
  for them; they are pinned as the regression guard. The exact computation is
  also far faster than the loop it replaces — counting a 100000-digit value
  went from 6.2s to 3.5ms, since the old code did one big-`Rat` multiplication
  per digit.

- **`xs:decimal` rendering capped the scale at 1024 digits, so a value past it
  printed as a different number.** A terminating decimal needing more than 1024
  fractional digits fell back to an 18-digit rendering, and since those values
  are mostly leading zeros the result was `"0"` — for `1/10^5000`, a lexical
  form that compares unequal to the value it came from. `string()`, `cast as
  xs:string` and the round trip `xs:decimal(string($x)) eq $x` all disagreed
  with `$x eq 0`, and only one of the two answers can be right.

  This is the second time the cliff moved rather than went: the cap was 18,
  which printed a 360-digit literal as `0`, and raising it to 1024 left the
  same contradiction waiting at 10^-1025. `xs:decimal` is arbitrary precision in
  XML Schema, and the code already factored the denominator into `2^a·5^b` — it
  knew the value terminated and approximated it anyway.

  A terminating value now renders at whatever scale it needs, with no ceiling.
  The bound was reacting to a real cost, but had it on the wrong side: printing
  50000 digits takes 261µs, while the loop that *found* the scale by dividing
  off one factor at a time took 907ms, being quadratic in the exponent. Stripping
  the twos with a single shift and the fives by repeated squaring makes that
  415µs, so the ceiling has nothing left to buy. A 10000-digit value formats
  end-to-end in 92µs.

  A rational with no finite decimal expansion — `1/3`, from division — is a
  genuinely different case, keeps the 18-digit policy XPath 2.0 requires as a
  minimum, and is unchanged: it has no exact lexical form, so rounding there
  discards nothing the form could have carried.

- **`fn:round-half-to-even` clamped its precision, which changed answers.** The
  precision argument was clamped to ±4096 before use. The clamp was there for a
  real reason — the precision reaches `exactRound` as an exponent of ten, so
  `round($x, 4294967296)` asked for a bignum with four billion digits and the
  process stopped responding — but it answered a different question than the one
  asked. Rounding a value to at least as many places as its own scale cannot
  change it, yet `round-half-to-even(10⁻⁵⁰⁰⁰, 5000)` returned **zero**: the
  precision was reduced to 4096 first and every significant digit fell off.

  The comment defending the clamp claimed that rounding below `-maxRoundPlaces`
  "gives zero either way". That holds for `xs:double`, which tops out near
  1.8 × 10³⁰⁸, but not for `xs:decimal` and `xs:integer`, which XPath leaves
  unbounded: `round-half-to-even(10⁵⁰⁰⁰, -5000)` keeps its leading digit. The
  claim was checked rather than trusted, and it was wrong in both directions —
  the positive clamp zeroed a value it had to preserve, the negative one
  preserved a value it had to zero.

  The bound now comes from the value being rounded instead of from a constant.
  A precision at or above the value's own scale is answered by returning the
  value, and one past its last integer digit by returning zero, neither of which
  builds a scale factor at all; in between, the meaningful precision is bounded
  by the value's own size, so the bignum is proportional to input the caller
  already supplied. `round-half-to-even(1.234, 4294967296)` still returns
  promptly. Held by `TestRoundPrecisionAboveScaleIsIdentity`,
  `TestRoundPrecisionBelowScaleStillRounds` and `TestRoundPrecisionNegative`.

- **A backreference pattern was refused for having too many groups.** The
  backreference path rejected any pattern declaring more than 64 capturing
  groups with `FORX0002`, reachable from `fn:matches`, `fn:replace`,
  `fn:tokenize` and `fn:analyze-string`. No such ceiling exists in either the
  XSD or the XPath regular-expression grammar, so a 65-group pattern is valid
  and was being refused.

  The constant was aimed at the fixed-width analysis, on the stated theory that
  it is "quadratic in the nesting". Measurement put the cost on a different
  axis: it tracks the pattern's source length, not its group count. 20,000 flat
  groups analyse in about 120 µs, while a 12-deep alternation declaring only 12
  groups takes eight times as long, because it is 16 KB of text. The cap was
  refusing the cheap shape while admitting the expensive one — it was not
  bounding the quantity it named. Reaching a depth that costs even half a second
  needs about 4 MB of pattern the caller must already supply and parse, so the
  input bounds the work on its own and the cap is gone. The step budget that
  bounds the optional backtracking matcher is untouched, and exhausting it still
  raises `FORX0002` rather than a silent `false`. Held by
  `TestBackrefManyGroups` and `TestBackrefManyGroupsStillBounded`.

- **`keyref` rediscovered its targets once per enclosing scope.** A `keyref` on
  a self-embedding element walked the whole remaining subtree once per level:
  nodes visited grew 3.92x, 3.96x, 3.98x as depth doubled, so a hostile instance
  against a recursive schema cost quadratic work.

  The finding had been filed as *inherent, not unfinished*, on the reasoning
  that a node under a nested keyref scope is a target of that scope and of every
  enclosing one, each resolving against its own key table — so the count of
  checks is nodes times scopes and no traversal change removes it. That part is
  true, and it is still true. What it obscured is that the checks are map
  lookups and were never the cost. Instrumentation separated the two quantities
  the prose had run together: `fieldEvals` and `targets` were already linear,
  and only `nodesVisited` grew quadratically — target *rediscovery*, which is
  cacheable.

  The premise that blocked the fix was "a `keyref` produces nothing for an
  ancestor to seed from". Giving it a table of its own — a target cache, never
  resolved against, since a keyref defines no keys — lets it prune and seed
  exactly as `key` and `unique` do. Growth per doubling becomes 2.00x, held by
  `TestIdentityKeyrefAmplification`. The number of checks is deliberately
  unchanged; only the rediscovery went.

  A second quadratic sat underneath, in allocation rather than traversal: every
  level rebuilt maps holding every target below it, 225 MB per validation at
  depth 960. Child tables are now adopted rather than copied, which is sound
  because each has exactly one consumer. Elapsed time could not separate the
  two — after the walk was made linear the benchmark still grew fourfold;
  `-benchmem` is what showed bytes per operation growing 3.95x while the
  allocation count grew 2.2x.

- **Thirteen derivation walks stopped at 32 or 64 steps.** Twelve stopped at 32
  and one at 64, each walking a type's derivation chain or a node's copy
  lineage: five in `xdm`, five in `xpath`, two in `xslt`. A legal acyclic chain
  of 33 user-defined types stopped atomising — chain=32 gave `xs:integer`,
  chain=33 gave nothing. Nothing in such a schema is recursive or malformed;
  the walks were bounded because a bound looks like cycle detection.

  Seven fail restrictively: a legal query answers wrongly, `instance of` goes
  false for a type's own ancestor, `fn:id` goes blind to a deep restriction of
  `xs:ID`. Two fail permissively, which is worse — `XTTE0950` and `XTTE1545`
  went unreported, so a stylesheet did what the specification forbids.

  The accumulator's 64 was the worst. It follows a copy back to its origin,
  which XSLT 3.0 18.3 makes the only correct source, and past 64 links it
  returned an intermediate copy — a node in a tree of its own, where the
  accumulator computes something else. Not a refusal and not a crash: a
  legal-looking wrong number nothing downstream can detect. Its own doc comment
  said "the chain is followed to its end", which the loop did not do.

- **Nine node-copy sites each hand-picked which type properties to carry.**
  `xdm.Node` records seven PSVI properties — `TypeAnnotation`, `UnionMember`,
  `DerivedPrimitive`, `ListItem`, `IsID`, `IsIDREFS`, `IsNilled` — and every
  place that copied a node wrote its own field list. No two lists agreed. Four
  sites dropped `DerivedPrimitive` and `ListItem` (recorded as knowingly
  incomplete in `docs/security.md`): `xsl:copy-of` via `xdmbuild.DeepCopy`, the
  `xsl:strip-space` tree copy, `fn:snapshot`'s ancestor spine, and
  `fn:copy-of` on a parentless attribute. `xsd/assert.go` additionally dropped
  `IsID`, `IsIDREFS` and `IsNilled`, so an XSD 1.1 assertion evaluated over a
  clone the validator had annotated and the clone had not. `xdm/xinclude.go`
  and the suite judge dropped the resolved pair too.

  The loss is silent until a second schema defines the same QName differently,
  because a copy holding only the annotation NAME asks the process-global
  registries what it means — and they answer for whichever schema loaded last.
  A list-typed value copied through `xsl:copy-of` then split into `xs:string`
  items where the validating schema said `xs:decimal`: same lexical form, a
  confidently wrong type, and `'10' lt '9'` true under string ordering where
  the numeric answer is false.

  Two `xsl:strip-space`-adjacent strip paths had the mirror defect: both
  `stripAnnotationCopy` and `stripAnnotations` emptied `TypeAnnotation` by
  assignment and left `DerivedPrimitive` and `ListItem` behind, describing a
  type the node no longer claimed. Atomisation gates on the annotation being
  non-empty, so the stale fields went unread until something re-annotated the
  node.

  All nine now go through one of two named operations on `xdm.Node`:
  `CopyTypingFrom` carries all seven, `CopyTypingStrippedFrom` (and its
  in-place spelling `StripTyping`) clears the four that make up the annotation
  and keeps `IsID`/`IsIDREFS`, which XSLT 2.0 §3.5 exempts from stripping by
  name, while clearing `IsNilled`, which the same section makes false for every
  element in a stripped tree.

- **A second schema silently retyped a document the first had validated.**
  `xdm` keyed `derivedPrimitives`, `listItems` and `unionMembers` by QName
  alone, process-wide. Mutexes make that race-free, not isolated: the last
  schema to register a name wins. A value atomised as `xs:decimal` became
  `xs:string` once another schema registered the same name over a string
  restriction, so `. lt '9'` answered true under string ordering where the
  numeric answer is false — a wrong comparison with no error. Worse,
  compile-time registration is not replayed per transform, so one compiled
  `*Stylesheet` returned DECIMAL and then STRING for the same input because an
  unrelated part of the process called `xsd.Load`.

  The fix is the pattern the package had already chosen twice. `UnionMember`,
  `IsID` and `IsIDREFS` record the assessment's answer on the node, and unions
  were measurably immune to this bug for exactly that reason. `DerivedPrimitive`
  and `ListItem` join them, resolved against the schema being validated, with
  the registry kept as the fallback for nodes annotated another way. Additive:
  `TypeAnnotation` stays public and no exported signature changed.

  Two XSLT cases broke mid-work and were caught by the gate rather than by
  review — `as/as-1811` and `as/as-3002`, taking XSLT 2.0 to 6147 and 3.0 to
  8610 before the real cause was found. The resolved fields outlived their
  annotation: `Atomize` gates on `TypeAnnotation != ""` and `AtomizeList` did
  not, so a result-tree element arrived with an empty annotation and a live
  `ListItem` and split into three tokens anyway. Replacing a derivation with two
  independent fields turns a structural invariant into one maintained by hand.

  Four literal copy sites in `xslt` still drop the new fields the way they
  already drop `UnionMember`. That is harmless while the annotation survives —
  the registry answers — and is recorded in `docs/security.md` rather than left
  for the next audit.

- **A circular type longer than 4096 links loaded clean.** Reachable from a
  hostile *schema*, not from an instance. `checkTypeBaseCycles` walks a global
  type's base chain looking for a return to itself, stopped at `steps < 4096`,
  and appended **no** error on running out — the permissive verdict. A ring of
  4,097 types loaded clean where 4,096 reported every link: a sharp cliff, and a
  false accept of exactly the violation the function exists to diagnose. The
  loop was confirmed to iterate at that depth before anything was changed, since
  an earlier 300-link facet probe had cleared six counters without ever reaching
  them.

- **RELAX NG refused a legal chain of 501 definitions.** The mirror image of the
  same mistake, failing in the other direction. `maxRefDepth = 500` was a hard
  refusal, so a legal chain of 501 definitions became uncompilable with
  "recurses more than 500 deep" when nothing recursed. The counter could never
  do its nominal job: `c.expanding` sits immediately above it and already
  catches every re-entry into a name still on the stack, and runtime recursion
  unfolds through `lazyRef` with a fresh compiler at depth zero. The only thing
  the count could reach was the acyclic case it had no business refusing.
  Removed rather than raised, because the state doing the real work was already
  there. The test asserts a semantic property rather than the absence of a
  crash — the chain must still *enforce* its trailing `<text/>` at depth 4,096,
  since a chain that collapsed to "anything goes" would pass a
  compiles-without-error check.

- **A permitted file was read whole with no byte limit.** `readConfined` ended
  in a bare `io.ReadAll`, so every path through the filesystem resolver read a
  file entirely into memory before anything could refuse it: `fn:doc`, external
  entities, `fn:unparsed-text`, and XInclude `parse="text"`. The first looked
  covered — the parse downstream has `MaxBytes` — but the bytes are already
  spent by then, and the other three had no bound at all. Measured: a 40 MB file
  returned 41,943,040 bytes with a nil error. Once a caller enables a resolver
  the stylesheet chooses which permitted file is read, so a large permitted file
  was a memory-exhaustion primitive.

  Refusal rather than truncation: a half-read stylesheet is a smaller stylesheet
  that may well parse, and a half-read text resource is a string the stylesheet
  computes with. The increment saturates, matching `xdm/parse.go` and
  `xsd/resolve.go`, so the largest limit a caller can name does not silently
  return an empty file — that exact bug was an earlier audit's finding and the
  silent-empty half was the dangerous one.

- **An ambiguous key came back at three siblings.** `mergeTables` dropped a key
  sequence that two children both defined, because an ancestor's keyref cannot
  say which of them it resolves to. Deleting the entry was the whole record of
  that, and a key is absent both before it is first seen and after it has been
  dropped — the merge could not tell those apart, so a third sibling found
  nothing there and put it back. It oscillated with the count: resolvable at
  one, ambiguous at two, resolvable again at three, ambiguous at four. The wrong
  direction was acceptance, with an outer keyref resolving against a key three
  separate subtrees defined.

  `nodeTable.ambiguous` makes the third state explicit and `mergeEntry` is the
  single path every fold goes through, so ambiguity is terminal. Worth recording
  how it survived: the identity oracles ran 10,000 documents and agreed
  throughout, because both generators put targets under one scope at a time and
  a three-sibling ancestor never arose. An external reader found it by reading
  the merge, and the durable answer is the invariant rather than more documents.
  A later independent keyref oracle — 3,000 documents, 0 disagreements — was
  sharpened by comparing exact identity-failure counts rather than a boolean
  verdict, and sabotaging the merge to reintroduce this bug produces 5
  disagreements in 3,000 where two other sabotages produce over a thousand each.
  That ratio is why the shape went unfound for so long.

- **The language-inclusion procedure declined any bound above 64.** The
  subsumption procedure fell back to the structural XSD 1.0 rules above
  `maxOccurs="64"`, which can refuse a restriction whose language really is a
  subset. That was a second cliff standing in front of `subsumeMaxStates`, which
  the unroll loop already checks on every iteration — the budget bounds the cost
  at the point it is incurred rather than at a number chosen in advance.
  Removed; `1000000` and unbounded still load in milliseconds.

- **Six base-chain counters were defects, two of them false accepts.**
  Reachable from a schema rather than from an instance. Eleven remaining
  `seen > 64` and `seen > 256` counters had been recorded as sound on the
  strength of a 300-link derivation chain, and an external reviewer reasonably
  relied on that and withdrew their claim. The measurement was real and it
  cleared the wrong walk: the chain drove facets, and `SimpleType.Primitive` is
  filled in eagerly during parsing — set on the deepest link at depth 1, 64 and
  300 alike — so `primitiveOf` returns on its first iteration and a 300-link
  chain exercised the loop once.

  Six were defects:

      idKind                a duplicate xs:ID is ACCEPTED at 64 links
      descendsFromInteger   "1.5" validates as an integer descendant at 64
      derivationMethodsTo   a legal schema fails to LOAD at 65
      typeDerivedFrom       false reject at 300
      derivedFrom           xsi:type false reject at 257
      substitution block    unreachable until the load bug above was cleared

  The cliffs sit at different constants because one walk counts links and
  another counts types, which is the argument against raising a constant rather
  than removing it. All twelve counters are visited sets now, and the five walks
  that genuinely collapse at parse are pinned as such so the superseded negative
  result is not re-derived from the same shape.

- **Occurrence arithmetic is exact, not saturating.** Saturating arithmetic
  fixed an earlier wrap and left two bounds above `occursHuge` comparing equal,
  so a base of 1e30 restricted by three members of 1e30 was accepted. `Particle`
  now carries an exact `*big.Int` alongside the int bounds, nil unless clamping
  discarded something, and the derivation checks compare exactly. The diagnostic
  quotes the true product: "maxOccurs 237684487542733012780631851005 exceeds the
  base's 79228162514244337593543950335".

  The int fields stay for the runtime. `nfa.go`, `upa.go`, the matcher and the
  subsumption checker are untouched, because comparing a bound against a
  document is a different question from comparing two bounds against each other
  — 1e30 and 3e30 are the same proposition to an instance. Schema load is
  unmoved and the allocation count is identical, 31,623 before and after: no
  schema in either suite leaves the int fast path.

- **Identity constraints are linear in the document, not quadratic.** Doubling
  the depth now doubles the work rather than quadrupling it, measured at 2.00x
  across 240, 480 and 960 where it was 3.98, 3.99 and 4.00.

  The fix is not a cheaper traversal. Four attempts at that are recorded in
  `docs/security.md` and all four were reverted, because the traversal was never
  the thing to change. What was wrong is *which* subtree gets walked: a
  descendant element declaring the same constraint is itself a scope, and its
  table already holds every target beneath it, so the walk stops there rather
  than descending again. On a recursive shape that turns a whole-subtree walk
  per level into a walk of the gap between one scope and the next.

  That needs the child tables to carry every target they selected, not only the
  entries that survived merging. `entries` and `targets` answer different
  questions and are now separate: `entries` is what a keyref resolves against
  and must drop a sequence two siblings share, because an ancestor cannot say
  which of them it resolves to; `targets` is what a duplicate check counts and
  must keep it, because the ancestor's scope contains both occurrences.

      depth 960           23.6 ms, 1,400,001 allocs  ->  1.2 ms, 15,423
      depth 200 width 40   214 ms, 1,223,930 allocs  ->  121 ms, 104,340

  Two earlier steps lowered the constant on the way. Seeding the key sequences
  from the subtree's table rather than deriving them again at every level took
  width 20 from 2,703,986 allocations to 718,996 and width 40 from 5,196,173 to
  1,223,930, with time down 25% and 15%. And since keyref cannot use the pruning
  that made key and unique linear — a keyref produces nothing for an ancestor to
  seed from, and a node under a nested keyref scope is a target of that scope and
  of every enclosing one — its field extraction is memoised per (node,
  constraint) instead: depth 960 from 221 ms and 5,164,375 allocations to 158 ms
  and 2,863,754, and width 40 from 324 ms and 5,269,468 to 217 ms and 1,297,551.
  The curve there is unchanged because it cannot be.

  The first attempt at the linear form was wrong in the direction that matters
  and the oracle caught it: seeding the subtree's targets only when the walk
  revisited them meant a target from below was never visited at all, so a key at
  depth 1 and the same key at depth 2 stopped colliding — 771 disagreements
  across the two generated corpora. `TestIdentityConstraintAmplification` now
  asserts the growth ratio, so a return to the per-scope rescan fails the build
  rather than being found in a profile later.

- **A 3 KB schema took 35 seconds to load, in two places.** Reachable from a
  hostile schema. A group referencing the next one twice, 29 times over, is
  acyclic and valid and fits in 3.0 KB; loading it took 35.8 seconds. A CPU
  profile put 86% in `cycleFrom` and 8% in `badNestedAll`, and nothing in group
  expansion, automaton construction or UPA checking — a `<group ref>` resolves
  to the definition's own `ModelGroup` pointer, so the graph is a DAG with flat
  memory and validation against the same schema is linear. The whole cost was
  two load-time walks enumerating 2^28 paths through a graph with 29 nodes.

  `cycleFrom` kept only the current descent, reasoning that a group reachable by
  two disjoint routes is not a cycle. That is a correct objection to two-colour
  marking and not to three-colour: a group explored to the bottom without a
  cycle stays acyclic whatever route reaches it next, so it prunes, while only a
  group on the current path is a back edge. `badNestedAll` had no memo at all,
  so fixing only the first would have left an 8%-of-a-huge-number exponential to
  reach the same wall a few groups later. Both memoise, and the done set spans
  every root rather than being rebuilt per root. n=40 — over five hundred
  billion paths — now loads in 0.01s, where n=32 did not finish in 90 seconds.

- **Occurrence arithmetic wrapped negative.** `occursHuge` is a quarter of the
  int range, so it survives doubling but not tripling. The derivation checks
  multiply a particle's bounds by a model group's length and sum them across its
  members with no guard, so a sequence of three members at a saturated
  `minOccurs` produced "minOccurs -4611686018427387907 is below the base's 0".
  The wrapped diagnostic is the visible half; the half that matters is that a
  negative bound can satisfy an inequality it should fail. Sixteen sites now
  saturate. Three were the reported ones; the sums in `effectiveTotalRange` are
  the worse find, overflowing at two saturated members rather than three and
  feeding the wildcard path independently. (The saturating form is itself
  superseded by the exact `big.Int` comparison above.)

- **An assertion rejected a valid document 33 elements deep.**
  `maxAnnotateDepth = 32` bounded the walk that types an element and its
  descendants before an XSD 1.1 assertion runs. Past it the descendants went
  unannotated, and an unannotated node atomises as `xs:untypedAtomic` — what the
  annotation exists to prevent. XSD 1.1 makes an evaluation error a false
  assertion result rather than a distinct outcome, so this did not degrade to
  "unknown": a schema whose assertion holds was valid at nesting 32 and invalid
  at 33, on documents differing only in depth.

  The bound's comment claimed the walk follows declarations and would otherwise
  not terminate. Checking that is what settled the fix: it descends
  `el.ChildElements()`, entering a child only where the instance has one. A
  recursive type is legal but its instance is finite, and depth is already
  bounded by `xdm.ParseOptions.MaxDepth`. The bound never prevented a loop, only
  truncated. Removed rather than raised; a self-referential type still
  terminates at instance depth 900.

- **Two depth-64 walks dropped declarations, and an invalid schema loaded
  clean.** `walkParticleElements` feeds `checkTypeTables`, so a declaration the
  walk does not reach is one whose type alternatives are never checked: a schema
  violating `src-type-alternative` — a default alternative that is not last —
  loaded without error once its declaration sat 64 model groups deep.
  `allDerivedDecls` feeds three restriction checks and dropped declarations the
  same way.

  `allDerivedDecls` is the instructive one. It already kept a `seen` set, so it
  looked like it had cycle detection; but the set was on *declarations*, which
  deduplicates the result without bounding the walk. A model group that reaches
  itself revisits the same particle forever without ever repeating a
  declaration. A visited set has to be keyed on what the recursion actually
  revisits, and that is the particle.

- **Six walks stopped at depth 32 and accepted documents the schema forbids.**
  Reachable from a schema, not from an instance: a deployment with a trusted
  schema and untrusted documents cannot reach it. It matters where schemas are
  accepted from callers, and for machine-generated schemas, which reach nesting
  hand-written ones do not.

  Four walks over the schema graph stopped at `depth > 32`. The bound had a real
  reason — a model group or union chain that reaches itself is legal to write,
  and these walks run before the content-model compiler that reports it — but it
  conflated a graph that is cyclic with one that is merely deep, and three of
  the four returned a definite answer on running out rather than a refusal.
  Every one of those answers was the permissive one:

  * `collectElementDecls` returned an empty map, which reads as "the base
    declares nothing here" and skips Element Declarations Consistent.
  * `nonAtomicUnionMember` returned nil, the same value as a clean result, so
    `cos-list-of-atomic` passed and a list of lists loaded.
  * `particleMatchesOnlyEmpty` returned false, which through
    `applyDefaultOpenContent` opens a type whose `appliesToEmpty="false"` says
    not to.
  * `SchemaUnionMemberTypes` returned `(nil, false)` — "not a union I can answer
    for" — so `1 instance of t:U` went false where 2.5.5's transitive membership
    makes true the only correct answer.

  The first is the sharpest. Take the suite's own `saxonData/wild068` — a base
  declaring `<e>` as a date/time union, a derived type replacing it with a lax
  wildcard, a global `<e>` of type `xs:duration` matched through it — and nest
  the base's declaration inside 32 sequences. A document XSD 1.1 requires
  rejecting was accepted. Nothing in that schema is recursive or malformed.

  Each bound is now a visited set keyed on the component pointer, which stops a
  cycle exactly and does not limit a legal chain. Raising the constant would
  have moved the cliff without removing it. The audit that raised this reported
  it as unproven — "a high-value target for differential testing, not a
  confirmed vulnerability" — and was right to: two first attempts to reproduce
  it showed no difference, because the rule is XSD 1.1 only and `Options{}`
  defaults to 1.0, which silently no-ops it.

- **An iteration that matches nothing is still an iteration.** A sweep of 2,028
  combinations of outer bounds, inner bounds and child count found 40 still
  wrong after the count-vector rewrite — every one a false rejection, every one
  with inner `minOccurs="0"` and an outer `minOccurs` of two or more at a small
  child count. `<sequence minOccurs="2" maxOccurs="2">` over `<element c
  minOccurs="0" maxOccurs="2"/>` is the witness, and its answers were
  self-inconsistent: zero `c` accepted, one refused, two through four accepted.
  A language that admits 0 and 2 but not 1 is not the language of any particle.

  A count advanced only on a transition between two matched positions, so a
  scope could reach its minimum only by consuming an element per iteration, and
  satisfying `minOccurs="2"` from a single `c` needs one iteration matching the
  `c` and one matching nothing. Zero `c` was accepted only because the empty
  document short-circuits through the model's own nullable flag and never
  consults a counter, so the two accepts came from two different code paths,
  neither of which modelled an empty iteration. XSD satisfies a particle by
  partitioning the content into consecutive parts each matching the term, and
  nothing in that rule requires a part to be non-empty. Measured: 0 of 2,028
  wrong, down from 40, with `BenchmarkValidateInstance` unmoved and allocations
  per operation identical.

- **A negative `xsd.ValidateOptions.MaxErrors` approved invalid documents.**
  `fail()` stopped recording once `len(v.errs) >= opts.MaxErrors`, with no guard
  on the limit being positive. At `MaxErrors = -1` the comparison `0 >= -1`
  holds on the very first failure, so validation stopped before recording
  anything and `Validate` returned nil. A flagrantly invalid document —
  `<r><nope/></r>` against a schema declaring `<r>` with empty content —
  validated clean. That is a silent pass: the dangerous outcome is not an error,
  it is the absence of one.

  The convention was already established and already implemented correctly next
  door. `dtd.Options.MaxErrors` documents "a negative value means no limit" and
  guards it with `v.max > 0 &&`; `xsd`'s field documented only the zero case,
  which is how the guard came to be missing, so a caller copying the working
  idiom from `dtd` or from `xdm.ParseOptions.MaxBytes` got a validator that
  approved everything.

- **Filesystem confinement is enforced when the file is opened.**
  `resolvePath` called `EvalSymlinks`, compared against the roots, and the file
  was opened later; an attacker able to write to the filesystem between those
  moments can replace a checked path with a link out of the root, and the opened
  file is then not the checked one. Reads go through `os.Root` now, so each
  component is resolved against the root's own descriptor and leaving the root
  is refused by the kernel at open time. The earlier check is kept for what it
  is good at: deciding which root a path belongs to, and producing the error
  that names the permitted directories. The parent directory is
  symlink-resolved so it can be compared with a resolved root — on macOS `/var`
  is a link to `/private/var` — while the final component is deliberately left
  unresolved, since resolving it would restore the gap being closed.

- **The resolver no longer serialises cache misses.** `loadTracked` held its
  mutex across `os.ReadFile` and the parse, so concurrent transforms sharing one
  resolver loaded modules one at a time whenever the cache was cold. Releasing
  it alone would have been wrong: `fn:doc` must return the same node for the
  same URI, so the cache is correctness and not merely speed, and two goroutines
  that each parsed and each published would hand out two document nodes for one
  document. The lock now covers the cache and an in-flight table; a path already
  being read is announced, and a second caller waits on that parse rather than
  starting its own.

- **The largest byte limit a caller can name is not a refusal.**
  `ParseOptions.MaxBytes` and `xsd.HTTPResolver.MaxBytes` wrap the reader in
  `io.LimitReader(r, max+1)`, one byte over so that hitting the limit is
  distinguishable from a document exactly at it. At `math.MaxInt64` that
  addition overflows to `math.MinInt64`, which `io.LimitReader` reads as
  "nothing left" — so the setting a caller picks to mean "do not limit me" was
  the setting that broke:

      MaxBytes=9223372036854775806  ->  parses
      MaxBytes=9223372036854775807  ->  "no root element"

  The HTTP resolver failed worse: it returned an empty body with a nil error, so
  a schema silently loaded as empty rather than refusing to load. Both saturate
  now, and the small-limit path still refuses. `AllowHost`'s documentation is
  corrected in the same pass — it claimed to be "the place to refuse loopback,
  link-local and private ranges", which it cannot be: it receives a hostname, a
  permitted name may resolve to any of those ranges, and DNS rebinding defeats a
  name check by construction. The boundary belongs in a `Transport` whose
  `DialContext` sees the resolved address.

- **A schema-aware stylesheet panicked on every run.** `ValidateContext` gave
  `validator` a `ctx` field and put a cancellation check on the validation walk.
  `validateNodeAgainstType` builds the same struct without one — it is XSLT's
  `validation="strict"` entry, bounded by what the transform just built, and the
  transform already honours the caller's context — so `checkCancelled` read
  `Err()` off a nil interface and panicked: 53 cases on the XSLT 2.0 target and
  77 on 3.0, all of them "transform failed: panic: runtime error: invalid memory
  address or nil pointer dereference". None of it was visible from the XSD
  suites the change was measured against, which stayed at 39,347 and 41,532
  because they never take that path.

- **An XInclude copy dropped the union member on attributes.** `copySubtree`
  carried `UnionMember` on the element and dropped it on the element's
  attributes — an inconsistency inside one function, and the same half-omission
  that silently untyped a validated document at three copy sites elsewhere.
  Nothing exercises this path today, which is why no suite moved; it is fixed
  because the next thing to walk an included subtree would inherit the bug.

- **XSD: nested occurrence bounds are decided exactly.** A repeated group whose
  only child is itself repeating was decided wrongly in *both* directions. For
  `<sequence minOccurs="5" maxOccurs="5">` over
  `<element c minOccurs="2" maxOccurs="2"/>` the only valid document is ten `c`
  and it was refused, while five `c` — which no reading admits — was accepted.
  The false accept is the serious half: a `minOccurs` floor was silently not
  enforced, so a schema believed to require a minimum count did not require it.

  The matcher tracked a *low* and a *high* reading of each occurrence count
  independently, and consulted them in opposite directions, so a document was
  admitted when different readings satisfied different bounds though no single
  consistent reading satisfied all of them. It now carries a set of whole count
  vectors — every reading consistent from the first child to the current one —
  so each bound is answered from one execution. The walk stays deterministic on
  the positions, which Unique Particle Attribution guarantees; only the counts
  are searched.

  Found by differential fuzzing against a brute-force reference, and invisible
  to both W3C suites: a group with two or more distinct child names was always
  decided correctly, which is why 39,347 XSD 1.0 agreements and 41,532 on 1.1
  did not cover it. Both suites are unchanged after the fix, case for case.

  Validating an instance costs about 2.7% more time and 3.1% more memory;
  compiling a schema is unchanged.

- **An array position and a `fn:function-lookup` arity went through
  `float64`.** The last three sites of the family fixed in cc17983, where an
  arbitrary-precision `xs:integer` was routed through a `double` to answer a
  question about itself. `float64` is exact only to 2^53 and saturates at
  2^63, so the value that arrived was not always the value written.

  `fn:function-lookup` (`xpath/fn_hof.go`) took `int(arity.Float64())`, which
  made the defect a *wrong answer* rather than a wrong message:
  `function-lookup(fn:concat, 10^32)` saturated the arity to `maxint`, and
  because `fn:concat` is variadic the lookup **matched**, handing back a
  function item for an arity no function can have. F&O 3.0 §16.1.1 has a
  single catch-all — "if no known function can be identified by name and
  arity, an empty sequence is returned" — and names no error for a negative
  or an enormous arity, so an arity too large to name a callable function is
  now simply unmatchable and yields the empty sequence.

  The bound there is not just a guard on the conversion. `synthesizeVariadic`
  builds an `fn:concat` entry at *any* arity at or above 2, so merely
  converting exactly would still answer `2^63-1` with a function item
  claiming that arity — the same wrong answer, since the narrowing saturated
  every huge arity onto exactly that value. `maxLookupArity` bounds it at
  what a function item could actually be called with.

  The array lookup `$a?($k)` (`xpath/maparray.go`) called
  `v.Member(int(k.Float64()))` instead of the package's existing
  `integerPosition`, so `$a?(1000000000000000001)` reported itself as
  `...000` and `$a?(10^32)` as `9223372036854775807`. The key's type check is
  unchanged: `?(1.0)` is still `XPTY0004`.

  The literal form `$a?3` was narrowed in the parser (`xpath/parser_path.go`),
  which is why `LookupExpr.Index` is now the `*xdm.Atomic` the parser had
  already built exactly, rather than an `int`. This is a smaller change than
  it looks — the field had three consumers, all in these two files — and it
  removes the conversion instead of choosing a width for it. `String()` prints
  the literal's own digits, so an expression round-trips.

  `maxArrayIndex` is deliberately **not** widened to `math.MaxInt`, and now
  says why: `array:subarray` compares `start+n` against the array length, and
  at `math.MaxInt` that sum wraps negative, the guard reads false, and an
  out-of-range request is accepted rather than raising `FOAY0001`. The `1<<40`
  bound keeps twice any accepted position inside an `int`. A test pins the
  invariant.

  Both W3C suites are unchanged, case for case. QT3 does not cover this
  corner: its largest `function-lookup` arity is 5, and it has no negative
  one.

- **A timezone name was read off a saturated instant.** `applyPlace`
  (`xpath/fn_misc.go`) has to put a value on the timeline before it can ask an
  Olson zone which offset applied at that moment, and it took the second count
  with `new(big.Float).SetRat(utc).Int64()`. `big.Float.Int64` saturates at
  `MaxInt64` and reports the clamp only through an `Accuracy` result, which the
  call discarded — so every instant past the `int64` second boundary had its
  zone looked up at the *same* clamped moment, 292277026596-12-04T15:30:07Z,
  and whatever name and offset applied there was attached to the value in hand.

  Mostly this was masked downstream: `dateTimeFromSeconds` guards the shifted
  count with its own `IsInt64` test and refused, leaving the value unnamed. But
  a negative offset pulls the shifted count back under `MaxInt64`, so for a
  value overflowing by less than the offset the guard passed and the fabricated
  name survived. `format-dateTime(xs:dateTime("292277026596-12-04T15:30:08Z"),
  "[Y] [H01]:[m01]:[s01] [ZN] [Z]", (), (), "America/New_York")` — one second
  past the boundary — rendered `292277026596 10:30:08 EST -05:00`, a wall clock
  and a zone belonging to a different instant entirely. No error: a wrong zone
  name reads as a plausible result.

  The count is now an exact `big.Int` quotient with an explicit `IsInt64`
  guard. Outside that range there is no instant for the zone database to be
  asked about, so no name is supplied and `[ZN]` falls back to the numeric
  offset, which describes the value as it actually stands.
  `TestFormatDateTimePlaceZoneNameIsExact` (`xpath/formatdate_exact_test.go`)
  asserts the rendered text on both sides of the boundary, including the
  ordinary daylight/standard cases that must keep working.

- **The `[0,60)` second invariant is written down instead of re-derived.** The
  `[s]` and `[f]` components of `fn:format-dateTime` each split `dt.Second`
  into a whole part and a fraction, narrowing the whole part to `int64`. That
  narrowing is safe, but only because of a range guarantee living in the
  constructors, and each site re-derived it. New `splitSecond`
  (`xpath/fn_misc.go`) does the split once and documents the precondition and
  every path that upholds it: `ParseDateTime` and `ParseGregorian` run
  `valid()`, which rejects a second outside `[0,60)`; `dateTimeFromSeconds`
  builds it as a non-negative remainder modulo 60 plus a fraction below 1; the
  cast and adjustment paths carry a checked field or reset it; and
  `dateTimeFromGoTime` takes it from Go's clock. No behaviour change — this was
  audited as a possible narrowing bug and found not to be one.
  `TestSplitSecondPrecondition` pins the range across the parsers, the
  arithmetic that borrows across a minute, and timezone adjustment, so the
  assumption fails loudly if a constructor stops providing it.

- **Six resource limits reported semantic error codes that actively misled a
  caller, and two more declined with no error at all.** `xdm.ErrResourceLimit`
  was added in the previous release and wired into two of the eight sites; the
  rest are done here, the same way -- the sentinel is APPENDED with `%w`, so
  the spec code and the leading message stay byte-identical and the ~30 sites
  that match on error text, plus every conformance suite, are unaffected.

  What each code claimed, and what had actually happened: `XPDY0001` ("no
  context item is defined") for `Context.Descend`'s recursion cap, which has a
  context and is merely deep; `XPST0003` ("the expression is syntactically
  invalid") for `parseSequenceType`'s type-nesting cap and for `xquery`'s
  `maxNestDepth`, both of which refuse well-formed input; `FORX0002` ("invalid
  regular expression") for a pattern that is valid and may well match, whose
  backtracking budget ran out; `FOAR0002` ("numeric overflow") for a range
  bound the enumerator declined, where nothing overflowed; and `cvc-elt.1`
  ("the element is invalid against its declaration") for a `MaxDepth` refusal,
  where nothing was assessed against any declaration at all.

  The asymmetry is what makes these worth separating: a refusal says *nothing*
  about the input. A document refused for depth may be perfectly valid, and the
  code told its caller the opposite. Every code is kept -- callers and the
  suites read them, and changing one is spec-visible -- so the distinction is
  drawn by `errors.Is(err, xdm.ErrResourceLimit)` instead. The XSD case reaches
  a caller through `xsd.ValidationErrors`, which now unwraps to the individual
  failures so `errors.Is` over the whole set finds it.

  `xsd/subsume.go` and `xsd/restrict.go` are the quiet pair: their budgets
  raise nothing, returning "declined" so the caller falls back to the
  conservative XSD 1.0 structural table. That fallback is SOUND -- the
  differential suite in `xsd/budget_soundness_test.go` proves it never accepts
  what the exact answer rejects, and no verdict here changes -- but it was
  invisible, so a legal XSD 1.1 restriction refused because a budget ran out
  was indistinguishable from one the table genuinely forbids. Those declines
  are now counted through a `budgetStats` hook, on the model of `icStats` in
  `xsd/identity.go`: nil in every ordinary build, read by nothing that makes a
  decision. Budget declines, which raising `subsumeMaxStates`,
  `subsumeMaxProduct` or `branchLimit` would turn into exact answers, are
  counted separately from structural ones -- a recursive model group, an all
  group, a wildcard with no per-name count -- which no limit affects.

  Both halves were validated by sabotage. Unwrapping the type-nesting site,
  substituting the XSD code instead of adding to it, mis-attributing a budget
  decline as structural, and turning one decline into an acceptance each failed
  a named test; the last was caught by the pre-existing soundness suite as well.

  Noted, not changed: `xpath.singleInteger`, which holds the `FOAR0002` site,
  currently has no caller -- `to` reads its bounds through `bigInteger`, which
  keeps the arbitrary precision rather than narrowing. It is wrapped anyway and
  covered by a test, because a limit that reports `FOAR0002` without the
  sentinel is exactly the defect this contract exists to stop recurring.


### Added

- `xslt.FileResolver.MaxBytes` bounds what the filesystem resolver reads, for
  every path through it — `fn:doc`, external entities, `fn:unparsed-text` and
  XInclude `parse="text"`. One limit rather than separate document and text
  ones, because confinement is a property of the file and not of the method:
  every root is readable by every path, so a stylesheet refused a large file
  through `unparsed-text` would simply ask for it through `doc()`, and two
  numbers would have looked like the smaller one bound while the effective limit
  was the larger. The default is 64 MB, deliberately the same as
  `xdm.DefaultMaxBytes` — a different number would make one of the two dead.

- `xdm.NodeAnnotation` records a validated node's `DerivedPrimitive` and
  `ListItem` alongside the `UnionMember`, `IsID` and `IsIDREFS` it already
  carried, so a node's type is answered from the schema that validated it rather
  than from a process-global registry. Additive: `TypeAnnotation` stays public,
  no exported signature changed, and the registry remains the fallback for nodes
  annotated another way.

- `xsd.DefaultMaxMatchStates` (4,096) bounds the readings the content-model
  matcher carries at once. Exceeding it fails the element with an error naming
  the limit rather than allocating without a ceiling. Each occurrence maximum is
  also narrowed per document to what that document can reach, which keeps
  ordinary schemas — and `maxOccurs="100000000"` — in single digits.

### Changed

- **Every configurable limit is now tested at its edges.** An off-by-one or an
  overflow at the boundary of a caller-settable limit is precisely what a unit
  test should catch before an auditor does, and nothing covered the edges of any
  limit before. Each is now exercised at 0, negative, 1, exactly at the limit,
  exactly one over, and `MaxInt`/`MaxInt64` with its neighbour; each refusal is
  asserted to name the limit that fired, because `err != nil` alone also passes
  when the wrong limit trips. Where a value's meaning is deliberate the test
  pins it rather than changing it: negative means "no limit" in most places but
  the default for `xdm.ParseOptions.MaxDepth` and `xpath.Context.MaxDepth`,
  since a depth bound of zero would refuse every document, and
  `xsd.HTTPResolver.MaxBytes` has no unlimited setting at all, because a schema
  is not a stream. The negative `MaxErrors` bug above is what this layer found.

- **The content-model matcher and the identity-constraint evaluator are each
  checked against a generated oracle.** Both oracles decide the answer from the
  specification's own definition and never call the code under test, which is
  the point — an oracle that asked the engine would agree with the engine's
  bugs, and that is exactly how both W3C suites missed the nested-occurrence
  defect across 80,879 agreeing cases.

  The occurrence oracle is derived from arithmetic: for a sequence repeated *i*
  times over a child occurring *iMin..iMax* times, the admissible totals are the
  union over *i* in [*oMin*, *oMax*] of [*i·iMin*, *i·iMax*]. Six shapes, 8,397
  documents, 0.4s as part of `go test ./...`; `GOXSLT_OCCURS_WIDE=1` widens
  every sweep to about 2s. Run against the code as it stood before either
  occurrence fix it reports 1,474 wrong answers, 165 of them false accepts.
  Shapes whose language is an interleaving are left out rather than guessed at,
  since an oracle for them would have to reimplement the matcher.

  The identity oracle agrees with the engine on 6,000 documents, and against a
  `buildNodeTable` sabotaged to scan only direct children it catches 416
  disagreements, every one a false accept. Widening it to a two-step
  `.//box/leaf` selector was nearly worthless at first — zero disagreements
  across 4,000 documents under the same sabotage, because every generated leaf
  sat inside a box, so `.//leaf` and `.//box/leaf` returned the same nodes and
  the leading step was unobservable. Adding loose `<leaf>` elements sharing the
  id space fixed that: the same sabotage now produces 841 disagreements. Worth
  recording as a rule rather than an anecdote — **when a sabotage check comes
  back clean, suspect the corpus before the implementation.**

  Instrumentation came first and is what made the quadratic legible. Elapsed
  time cannot tell "the same nodes walked once per enclosing scope" apart from a
  large constant, which is how two of four reverted attempts came to look like
  improvements; counters can. `nodesVisited` at depths 120, 240, 480 and 960 was
  7,260, 28,920, 115,440 and 461,280 — four times the work for twice the depth.
  The counters are nil in every ordinary build and attach through a
  package-internal hook.

- **The entity expansion budget was bypassed whenever the DOCTYPE and the
  document body arrived in one read.** `entityChargeReader` exists to charge
  references *before* `encoding/xml` substitutes them, because a post-parse
  check reports the same verdict only after the expansion it is refusing has
  already been allocated. While the entity table is nil the reader buffers into
  a backlog, and it drained that backlog on the *next* `Read` — which for a
  large entity declaration never comes: the decoder's read-ahead window is
  sized in bytes, so one big declaration fills it along with the entire body,
  the backlog was dropped, and `charge` was never called. Five enforcement
  sites existed and the one guarding the streaming path never fired.

  There was no threshold. Measured with `AllowDOCTYPE: true` and one entity
  referenced 400 times: a 10 KB entity (11 KB of source) expanded to 3 MB past
  the 1 MB budget and allocated 11 MB; a 60 KB entity reached 22 MB expanded
  and 87 MB allocated; a 1 MB entity — a 1 MB request body — reached 381 MB
  expanded and **1,423 MB allocated**, a single-request OOM reachable with
  `AllowDOCTYPE` alone, no external entities and no network.

  The backlog is now charged at `arm`, the one point in `xdm/parse.go` where
  the table becomes known and where the backlog is sitting. All three are
  refused, and the largest now allocates 21 MB rather than 1,423 MB — the
  bound is on peak memory again, which is the only reason to charge before
  expansion rather than measure after it.
  `TestEntityBudgetHoldsWhenDoctypeAndBodyShareOneRead` asserts the allocation
  and not merely the refusal, so a fix that refuses expensively still fails.

- **`endOfInternalSubset` read comment text as structure.** It tracked quotes
  and brackets but had no comment state, and XML 1.0 §2.8 permits comments in
  the internal subset while §2.5 says their content is not markup. So
  `<!DOCTYPE d [ <!-- it's here --> … ]>` opened a quote that never closed and
  the scan ran off the end, returning `0` — making `charge` treat the subset
  itself as content — while `<!DOCTYPE d [ <!-- ] --> … ]>` closed the bracket
  early, returning 24 instead of 43 and scanning the declarations as content.
  Comments are now skipped whole.

  This was unreachable end-to-end on its own: the backlog bypass above
  dominated every case, so nothing downstream of the boundary was ever
  consulted. Fixing the first exposes the second, which is why they are fixed
  and tested together —
  `TestEntityBudgetWithCommentInSubset` exercises the interaction in both
  directions, refusing 4 MB of expansion past the budget and still accepting
  100 KB under it with the same comment present.

- **`xdm`'s own limits did not carry `xdm.ErrResourceLimit`.** The package
  that *defines* the sentinel, and documents it as the way a caller
  distinguishes "the processor declined" from "your input is wrong", applied
  it to none of its four parse limits: depth, `MaxNodes`, `MaxBytes` and the
  entity budget all returned `errors.Is(err, ErrResourceLimit) == false`, the
  same answer as a genuine syntax error. `xdm/xinclude.go` already wrote
  "resource limit exceeded" into its message text without wrapping, which is
  the version of the bug that looks fixed.

  Fourteen sites across `parse.go`, `dtd_entities.go`, `dtd_external.go` and
  `xinclude.go` now wrap it, following the established rule: the sentinel is
  **appended**, so every existing message stays byte-identical for the tests
  and suites that match on it. New `xdm/resourcelimit_test.go` asserts the full
  message string rather than a substring — a wrap that renders the sentinel
  first and the reason after would pass a `Contains` check — and asserts that a
  malformed document is still *not* a resource limit.

- **`MaxBytes` did not bound a UTF-16 document at all.** `ParseOptions.MaxBytes`
  is documented as bounding the source document, and the code says the limit
  "wraps the reader, so it bounds what is read rather than what a caller
  remembered to check". But `decodeReader` ran *before* that wrap, and
  `utf16Reader.fill` reads its whole input in a single `io.ReadAll` — it has
  to, since the encoding declaration it rewrites sits at the front of text a
  streaming decoder would already have handed on. So the entire document was
  pulled in and decoded to UTF-8 before one byte was counted: 8 MB of UTF-16
  allocated 136 MB against a `MaxBytes` of 1024, a refusal costing far more
  than an acceptance. `fill`'s own comment concedes the whole-input read and
  justifies it with "schema and instance documents are small enough", which is
  not a property attacker-supplied input has.

  The limit now wraps first, ahead of every other reader including the
  decoder, so the `ReadAll` hits the bounded reader and stops. The same probe
  allocates nothing measurable. UTF-8 is unaffected — `decodeReader` only
  strips a BOM or rewrites a version declaration — and a small UTF-16 document
  still parses, which `TestMaxBytesBoundsUTF16Input` checks alongside the
  allocation, so the bound cannot be met by dropping the UTF-16 support XML
  1.0 §4.3.3 makes mandatory.

## v1.2.1 — 2026-09-03

Conformance, correctness, and the honesty of the numbers reporting them.
Measured with `tests/check.sh`: XPath 2.0, 3.0 and 3.1 all at 100%, XQuery
3.1 at 99.99% (29,800 of 29,803), XSLT 2.0 at 99.87% (6,149 of 6,157), XSLT
3.0 at 99.85% (8,612 of 8,625), XSD 1.0 and 1.1 at 99.90% and 99.91%
agreeing, RELAX NG at 100%. DocBook xslTNG 577 of 593 and XSpec 225, with
1,095 unit tests clean under `-race`.

**The Go floor is lowered to 1.25**, from 1.26, so this builds on one more
toolchain than v1.2.0 did. It is measured rather than nominal: `regexp`
learned the Unicode category `Cn` in 1.25, and building on 1.24 costs four
conformance cases -- the dependency is on standard-library behaviour, not on
a symbol, which is why a local run with a newer toolchain installed could not
see it.

### A private function of a used package is not callable from outside it

`use-package-003` asked for it and this file had recorded it as needing "the
package threaded through the XPath static context", the single largest
structural change on the list. The narrow form turned out to be contained:
the declaring package's visibility is carried on the function component and
checked at the call site, through a `ScopedFunctionLibrary` paralleling the
existing `DynamicFunctionLibrary`. An earlier lexical-rename attempt broke
`override-f-026`, where one name exists at two arities; a rename was the
wrong shape, not the idea.

### An instruction in 1.0 compatibility mode inside a streamable mode

`XTSE3430`. Section 3.9.1 states the rule "notwithstanding anything stated in
19 Streamability": an instruction processed with XSLT 1.0 behavior *is*
roaming and free-ranging, by declaration rather than as something a posture
and sweep analysis concludes. So `streamable-141`, filed here as needing the
§19.8 analysis this engine does not implement, never needed it. The check is
scoped to exactly that shape -- a template whose `@mode` names a mode
declared `streamable="yes"`, containing an element that states
`version="1.0"` -- because a processor that does not stream is not required
to assess whether anything else is guaranteed-streamable.

### The QT3 per-case deadline was measuring the runner

CI reported XQuery 29,799 passing in one run and 29,798 in another, for the
same commit, minutes apart, and the ratchet correctly called the second a
regression. Two different numbers for one commit cannot come from a code
change. `tests/qt3` was still on a 10-second per-case deadline that
`tests/xslts` had been raised off for this exact reason; it is 60 seconds
now, and honours `GOXSLT_CASE_TIMEOUT` as the XSLT driver already did.

### Verdicts that were re-derived and found wrong

`validation-0201` was filed as fixable in the harness -- the suite does
license a driver to "ignore differences in the serialization that are known
to be irrelevant", and the case is not a serializer test. Implementing that
showed the indentation was the first of three differences and the case still
failed. Behind it: an expected file declaring `iso-8859-1` with no
`@encoding` for the harness to read, fixed here; and then an engine defect,
`29 MAY 1917` where `29 May 1917` is wanted, because an imported schema's
named simple type is invisible to `instance of` in a match pattern. That is
now recorded in `docs/known-gaps.md` as an open gap rather than papered over
as a harness fix.

The XSD "disputed" counts had never been derived from the suite at all.
Counted from the `<current>` status of every disagreeing case: 71 of 79 are
ones the W3C itself challenged, and the remaining 8 are `accepted` -- the
only settled XSD expectations this validator disagrees with.

### Also in this release

Two ratchet marks go **down** and neither is a regression. XSD 1.0 fell from
39,353 to 39,347 and XSD 1.1 rose from 41,525 to 41,532: `indeterminate`
expectations stopped being scored as "must be invalid", and those cases leave
the denominator as well as the numerator, so the rate rose while 1.0's raw
count fell. XSD 1.1 rose outright because `iri-001`'s schema, which builds
its RFC 3986 patterns from an internal DTD subset, is now loaded with
`AllowDOCTYPE` on -- recovering the schema case and the 12 instance tests its
load failure had been suppressing.

The entries below this line were written against `Unreleased` and all ship in
v1.2.1.


### system-property('xsl:product-version') was answering 0.1

It was a constant nobody edited, so it said `0.1` through the 1.0, 1.1 and
1.2 releases -- and a stylesheet dispatching on it, which is the only reason
section 18.2 defines the property, got an answer three tags out of date. It
is read from the build now, reduced to the release triple it descends from:
a pre-release or build suffix is dropped, and anything that is not a bare
`N.N.N` answers `0.0.0`. The shape matters as much as the value --
`package-version-010` writes the property into `xsl:package/@package-version`
after stripping all but digits and dots, so a pseudo-version becomes an
invalid package version rather than an untidy string. The case says so in its
own description, and it caught the first version of this fix.

Reported from the field against 1.2.0, alongside the base-URI defect below.

### -allow-dir says where, not what

The flag's help named only `xsl:include` and `document()`. It also governs
`xsl:import`, `fn:doc`, `fn:unparsed-text`, external entities and XInclude --
one root list for every reader, each of the riskier ones gated by its own flag
on top. A root covers its subdirectories to any depth, which the text did not
say either, and which is the question a user asked after reading it.

### An indeterminate expectation is not a demand to reject

`<expected validity="indeterminate"/>` prescribes no result. The W3C uses it
where the working group left an area underspecified: `schZ012_a`'s annotation
says "The WG decided the spec. is underspecified in this area, so
implementations may reasonably differ", and `particlesZ026` records that the
TSTF found its validity implementation-determined.

`expectedValidity` in `tests/xsdsuite` read the attribute as `w == "valid"`,
which collapsed three outcomes into two and turned "no answer is prescribed"
into "must be invalid". Accepting such a schema then scored as a false accept.
The driver now treats `indeterminate` as its own outcome, skips the case
entirely, and prints the skipped count beside agree and disagree so the
denominator stays legible rather than silently shrinking.

Sixteen cases on 1.0 and fourteen on 1.1 leave the ratio. Ten of the 1.0 and
eight of the 1.1 had been counted as disagreements; the rest had been agreeing
by accident, which is why the agreeing counts fall by six on each version even
as the percentages rise:

    XSD 1.0  39,353 / 39,404 (99.87%)  ->  39,347 / 39,388 (99.90%)
    XSD 1.1  41,525 / 41,572 (99.89%)  ->  41,519 / 41,558 (99.91%)

`tests/ratchet.txt` is lowered to the new agreeing counts. No engine behaviour
changed; only what the harness counts.
### An XSLT 3.0 static expression can read a document

The static phase built its evaluation context with no document resolver, so
`fn:doc` in a `use-when`, a `static="yes"` variable or a shadow attribute
always failed with `FODC0002: document access is disabled`. That is XSLT 2.0's
rule, not XSLT 3.0's: 2.0's 3.13 table fixes *Available documents* at **None**,
while 3.0's 9.7 table replaces it with **implementation-defined**, for both
*Available documents* and *Statically known documents*. The severe constraints
9.7 does impose are on what the stylesheet may be asked about — no context
item, no stylesheet functions, no source document — not on whether documents
resolve at all.

At the 3.0 target the module resolver now answers, and only it: a host that
supplied no `Resolver` gave the stylesheet no way to reach the filesystem, and
the static phase is not the place to hand it one. At the 2.0 target nothing
resolves, as before.

`package-version-011` is the case, and it is the one the suite marks: it writes
a shadow `package-version` attribute that reads the module's own `@version`
through an empty document reference. Saxon 9.8 passes it. `use-when-0406` pins
the other side, and its own modification note states the split — *"Marked test
as 2.0-only: in 3.0, use-when expressions can access documents"* — which is why
the change is gated on the processor version rather than applied to both.

XSLT 3.0 8608 → 8609 passing. XSLT 2.0 unchanged at 6149; ungating it cost
`use-when-0406` and took 2.0 to 6148, which is what the gate is for. XQuery and
both XSD targets unchanged.

### The gate no longer runs the conformance suites twice

`tests/check.sh` ran its `unit tests` and `race` steps without
`GOXSLT_NO_SUITES`, so in an environment that has `testdata/` on disk — which
is exactly the conformance job — each suite harness fell back to it and ran
the whole conformance job a second time. Under `-race` that second run passed
`go test`'s 10-minute default and panicked rather than reported:

    panic: test timed out after 10m0s
        running tests:
            TestXSLTSuite (1m53s)

The two figures only reconcile if the deadline was spent by the 3.0 suite
before the 2.0 suite started, both in the same binary. The step now takes 21
seconds. `-timeout` is set here as well as in `ci.yml`, where it had been
fixed and here had not, and `-count=1` because a cached result cannot show a
regression. The suites still run in full in their own section.

Two comments were also rewritten so that `gofmt` leaves them alone: it turns a
pair of apostrophes in running doc-comment prose into a typographic quote, and
both quoted XPath or XQuery in which `''` is the empty string literal.

### A base URI is a URI, not a path

`fn:static-base-uri` and `fn:base-uri` returned the filesystem path the file
was read from — `C:\Users\m\s.xsl` on Windows, `/home/u/s.xsl` on Unix —
where they are defined to return a URI. Saxon returns
`file:///C:/Users/m/s.xsl` for the same stylesheet. The consequence was not
just a differently-spelled string: a bare path has no scheme, so it is not an
absolute URI, and the idiom that locates a file beside the stylesheet —
`resolve-uri('sample2.xml', base-uri())` — failed outright with `FORG0002`.
Reported against 1.2.0 on both Windows and Linux.

The CLI now spells every base and document URI with `file:`, in the RFC 8089
empty-authority form: `file:///home/u/s.xsl`, `file:///C:/dir/s.xsl`. The
three slashes matter on Windows. A Windows absolute path has no leading slash
of its own, so the two-slash spelling `file://C:/dir/s.xsl` makes `C:` the
*authority* rather than the drive: parsing it back yields host `C:` and a path
with the drive letter gone, and every URI resolved against it names a file
that is not there.

The reverse conversion was textual too — stripping a `file://` prefix — which
left `/C:/dir/s.xsl` on Windows, a path no filesystem call accepts, and left
percent-escapes undecoded, so a directory whose name contains a space became
one containing `%20`. It now goes through a real URI parse.

### XInclude

`xdm.ProcessXInclude` implements XML Inclusions (XInclude) 1.0, Second Edition,
as a pass over an already-parsed tree — which is what the specification says it
is: §4 defines XInclude as a transformation from one infoset to another, not as
a parsing option. Running it after the parse means an included document is
parsed by the same `Parse` under the same limits, an `xi:fallback` subtree is
already built and can simply be moved, and a loop is caught by comparing URIs
rather than by inspecting a half-built tree.

Implemented: `parse="xml"` and `parse="text"` (honouring `encoding`), an absent
or empty `href` meaning the including document, `xi:fallback` on any failure
with a fatal error when there is none, the `xml:base` fixup of §4.5.5,
recursive inclusion, and loop detection. For `xpointer`, the two forms
XInclude requires a conforming processor to support: a **shorthand** pointer
(an ID) and the **element()** scheme (a child sequence).

Deliberately not implemented: the `xpointer()` and `xpath()` schemes — neither
is required by XInclude, and both would invert this package's dependency on the
XPath evaluator — and RFC 5147 text fragments (`line=`, `char=`, `search=`),
which are a DocBook convention layered on `parse="text"` rather than part of
XInclude at all. An unsupported scheme falls through to the next pointer part
by the XPointer Framework's own rule, and identifies nothing when there is no
other part, which §4.4 makes a fallback condition.

An href-less include carrying an `xpointer` selects from the tree it already
sits in rather than re-reading the file. That is not the loop §4.5 forbids —
that rule is about including a document *in itself* — and reading the file
again would both report a loop that is not one and address a reparse rather
than the tree an earlier inclusion has already modified.

**The confinement is unchanged.** `xdm` has no filesystem and no network; it
reads only what an `xdm.IncludeResolver` hands it. `xslt.FileResolver`
implements that interface through the very same `resolvePath` that gates
`fn:doc`, `xsl:include`, `fn:unparsed-text` and external entities — a non-file
scheme is rejected before the filesystem is touched, symlinks are resolved
before the containment check, and a path outside the roots is refused. An
inclusion therefore reaches nothing `fn:doc` could not already reach. There is
deliberately no second gate written here: two copies of a containment check are
two things to keep correct, and the first time they drift one of them is the
hole. Regression tests assert the path escapes, the symlink escape, every
network scheme against a canary server that records zero hits, and that a
fallback cannot be used to launder a refusal.

It is off unless asked for. `ProcessXInclude` is never called by this library
on its own; the `go-xml` command exposes it as `-xinclude`, reading from the
`-allow-dir` roots.

The XSLT 3.0 suite's `base-uri-052` passes as a result — it was previously
listed as not implementable purely because the feature was absent — taking the
suite from 8,606 to 8,607. In the DocBook xslTNG corpus it takes documents that
transform cleanly from 549 to 577 of 593 — measured with the flags a reader of
that corpus would pass, which is what `tests/check.sh` now does.

### One scanner for what is not syntax (internal)

XQuery's parser decides which sub-parser reads an expression by scanning ahead
over raw source, and that scan has to step over the regions whose bytes look
like grammar but are not: string literals, comments (which nest), pragmas
(whose contents are unparsed text) and string constructors. That logic was
duplicated across eighteen scanners in thirteen files, and the copies did not
agree — most were blind to `(#`, so the quote in `1 eq (#p:x " #) {1}` opened a
literal that never closed and the expression was routed to the XPath parser,
which has no pragma in its grammar at all. Fixing three copies left the rest
carrying the same defect.

All of them now route through one `skipNonSyntax` helper, which answers "what
here is not syntax" in one place and reports an unterminated region rather than
scanning through it as though its contents were grammar. Two scanners gained a
comment case they had silently lacked (`parseTypeUntil` and the kind-test scan
in `flwor_type.go`); the two whitespace skippers deliberately stay
comment-only, since A.2.4.1 gives `Whitespace ::= S | Comment` and the other
three regions are expressions. No behaviour change in the suites: XQuery stays
at 29,796, XPath at 15,183 / 19,244 / 21,786, XSLT at 8,606 / 6,149.
### Internal: a compiled XQuery expression can no longer be evaluated unsafely

No behaviour changes and no conformance movement; this removes the shape of a
bug rather than an instance of one. An XQuery expression that xpath compiled
carries three things besides the compiled form — the XQuery-only primaries
lifted out of its source, the error-code rewrite a declared type needs, and a
standalone type check — and every one of them is skipped by evaluating the
compiled form directly. That read as correct at each call site and had already
produced two real bugs, in a computed constructor's name expression and in a
processing instruction's target, each surfacing as `XPST0017` naming a
function the query never wrote.

The compiled form is now unreachable by name from the call sites. Evaluation
goes through `eval`, `evalBool` or `evalIn`, which apply the machinery;
static analysis, which wants the compiled form and nothing around it, goes
through `inspect`, whose name says so. Ten direct uses were converted, one of
which — the effective boolean value a `where` clause, a `satisfies` clause and
a window clause's conditions take — was a latent instance of the same bug,
unreached only because two independent lexical scanners happen to agree about
which expressions carry a lifted operand.
### The declared XQuery version is recorded

`parseVersionDecl` used to read `xquery version "1.0";`, check the literal
against a list of three, and throw it away. Nothing recorded which version a
module had declared, so every rule that differs between 1.0 and 3.1 was
answered by whichever reading the 3.1 conformance run wanted — correct for the
suite, and silently wrong for a module that had said 1.0.

The version is now recorded on the module's static context, which is the one
structure both the parser and the evaluator hold: `Query.sc`, `evalContext.sc`
and the parser's `sc` are the same pointer, so a static rule and a dynamic one
cannot disagree about which version applies. A module with no version
declaration is compiled as 3.1, which §4.1 leaves implementation-defined and
which is what this engine implements — so nothing about existing behaviour
changed. `XQST0031` is raised for a version outside the three, from the same
function that maps the literal, so the error and the recorded state agree by
construction.

Three decision points that had been hardcoded now ask:

* An unprefixed `declare option` name is `XPST0081` under 1.0 §4.16 ("The
  QName must have a prefix; if it does not, a static error is raised") and
  legal from 3.0 §4.19, which drops the sentence and puts the name in no
  namespace.
* A cast target naming a type that is in scope nowhere is `XPST0051` under 1.0
  and `XQST0052` from 3.0 §3.13.2. The gate for this already existed in
  `xpath.castTargetTypeError` and was starved of input, because the XQuery
  parser was pinned to XPath 3.1; the declared version now selects the
  expression language too, since XQuery 1.0 is defined over XPath 2.0, 3.0
  over XPath 3.0 and 3.1 over XPath 3.1.
* A variable circularity running through a function body is the static
  `XQST0054` under 1.0 §4.14 and the dynamic `XQDY0054` from 3.0 §4.16, which
  narrows the static error to a cycle whose every edge is a direct variable
  reference.

Everything else stays at 3.1's reading whatever the module declares — the
empty operand of `ordered {}`, an unprefixed pragma name, `XQST0134` for a
bare `namespace-node()` step. Each is a permissive divergence: a 1.0 module a
conforming 1.0 processor would reject is accepted, and none is given a wrong
answer. `docs/xquery.md` lists them.

No conformance movement: 29,796 / 7 on the XQuery target, 100% on all three
XPath targets, 8,606 and 6,149 on XSLT.

### XQuery conformance: 99.61% to 99.98%

29,796 of 29,803 QT3 cases in scope, up 107 across seven passes. XPath 2.0/3.0/3.1 stay at 100% and
XSLT at 8,606 / 6,149.

* **A constructed element no longer inherits its parent's namespace fixup.**
  XQuery §3.9.1.3 passes down exactly one kind of binding — the ones written as
  namespace *declaration attributes* — while the bindings §3.9.1.1 adds so that
  an element's own name and its attributes' names resolve are local to that
  element. Reading in-scope namespaces by walking the XDM tree cannot tell the
  two apart, so `<e a:n1="c" b:n1="c">` handed both `a` and `b` to every child;
  the child now undeclares what it is not entitled to. K2-NameTest-30/31,
  K2-InScopePrefixesFunc-25 and cbcl-directconelem-001/002.
* **`copy-namespaces preserve` stops undeclaring the default namespace on a
  prefixed copy.** The default namespace applies to unprefixed element names
  and to nothing else, so a `<bar:b>` copied under an `<a xmlns="http://foo">`
  is not moved by that binding, and the `xmlns=""` was taking away a namespace
  §4.8's `inherit` entitles the copy to keep. An unprefixed copy in no
  namespace still gets the undeclaration it needs.
* **The XML output method honours `undeclare-prefixes`.** The parameter was
  validated and then ignored: a `xmlns:p=""` in the tree was written out
  regardless, which is XML 1.1 syntax. XSLT 3.0 §11.7 generates prefix
  undeclarations only when `undeclare-prefixes="yes"`, so they are now omitted
  by default. The default-namespace undeclaration `xmlns=""` is legal in XML
  1.0 and is unaffected.
* **The QT3 harness compares result *sequences* by infoset.** A query returning
  two elements, or text beside an element, serialises to something that is not
  a well-formed document, so the infoset comparison — the only one that ignores
  where a namespace declaration sits — could not parse it and never ran. Both
  sides are now wrapped in the same synthetic root first. This is what the
  note on K2-FilterExpr-7 had already identified as the fix worth making.

* **A range survives being counted through a comma expression.** `fn:count`,
  `fn:empty` and `fn:exists` are defined purely on the length of their
  argument, so a sequence constructor's length is now summed from its parts and
  a `lo to hi` part contributes its cardinality without being built.
  `count(((), f(()), (1 to 10000000), f(1)))` no longer trips the five-million
  item guard. Parts that *are* materialised are still charged to that budget.
* **`fn:distinct-values` compares numerics pairwise** instead of hashing them.
  F&O §14.1.7 warns that `eq` is not transitive across numeric types, and
  constrains the result only to "no two items compare equal" and "every input
  equals some output" — constraints a single hash key cannot satisfy. This also
  drops the whole-sequence scan that degraded every numeric to float precision
  whenever one `xs:float` appeared anywhere in the input.
* **`fn:deep-equal` no longer merges text across a comment or PI.** The rule for
  an untyped (mixed-content) element is that `$i1/(*|text())` be deep-equal to
  `$i2/(*|text())` — a selection over the child axis, which drops comments and
  PIs but leaves the text nodes either side of one separate. Merging them made
  `<e>te<?t d?>xt</e>` wrongly equal to `<e>text</e>`.
* **`fn:filter` applies the function conversion rules to its predicate's
  result**, so a predicate returning an element is atomised and cast rather
  than refused. This is a cast, not an effective boolean value: an `xs:string`
  result is still `XPTY0004`, as are an empty and a two-item result.
* **The date and time component accessors cast `xs:untypedAtomic`.** Content
  from an unvalidated document reached `month-from-date` and friends as
  `xs:untypedAtomic` and was rejected; the function conversion rules cast it to
  the accessor's declared type. `xs:string` is still not accepted.
* **`method="json"` is accepted in the element form of the serialization
  parameters**, which had a narrower list of methods than the map form. The two
  spellings now share one check.
* **Synthetic names survive a prolog that rebinds the `local` prefix.** The
  argument variables and step functions this package invents are written with
  that prefix and so resolve through the static context; they are now bound
  under whatever it actually points at rather than under the fixed
  local-function namespace.

### Fixed — found by real-world stylesheets

Measured against [DocBook xslTNG](https://github.com/docbook/xslt3ng) and
[XSpec](https://github.com/xspec/xspec), two XSLT 3.0 codebases large enough to
exercise combinations the W3C suites do not reach. 577 of DocBook's 593 test
documents now render, byte-identical to the Saxon reference output once the
timestamp and generator metadata are normalised, and all 225 applicable XSpec
descriptions compile. 28 of those came from implementing XInclude, which is
what those documents actually need rather than the Saxon-Java extension
function their pipeline reaches for; 14 of the remaining 16 want `xpointer`
schemes that are not part of XInclude.

* **`xsl:copy` over a non-node context item** no longer raises `XTTE0945`.
  11.9.1 raises it only when the context item is *absent*; one that is present
  but is an atomic value returns that value. Conflating absent with
  not-a-node made `xsl:copy` inside `xsl:for-each` over atomics an error.
* **`fn:key` resolves its name against every binding of the prefix**, not just
  the first. The name is a lexical QName expanded at run time, so keeping one
  URI per prefix let whichever module was included last decide what it meant —
  XSpec binds `local` to 19 different URIs, one per module, and
  `key('local:scenarios', …)` failed purely by include order.
* **`xsl:evaluate` can call the stylesheet's own functions** outside an
  `xsl:package`. §10.4.1 excludes *private* functions and the default is
  private, but visibility is a property of a component of a package and a plain
  `xsl:stylesheet` is not one. Inside an `xsl:package` declared visibility is
  honoured as before. This costs W3C `evaluate-045` (XSLT 3.0: 8,607 → 8,606
  of 8,626); Saxon's own results report that case as `wrongError` too.
* **`tests/check.sh` gates on both corpora.** A new *real-world stylesheets*
  section transforms every DocBook and XSpec input and ratchets the number that
  succeeds, so a future change that breaks them fails the build rather than
  being noticed by hand. Both are skipped, not failed, when absent.
* **The CLI passes base URIs as `file:` URIs** rather than filesystem paths.
  `fn:resolve-uri` and `fn:static-base-uri` are defined over RFC 3986
  references, so `resolve-uri(rel, static-base-uri())` — the idiom a stylesheet
  uses to find a file beside itself — raised `FORG0002` on every run.

## v1.2.0 — 2026-09-02

XQuery 3.1 from 99.07% to 99.61% of the QT3 suite, and the second host
language reached parity: constructors, FLWOR, the prolog, try/catch, switch,
typeswitch and windows. `fn:transform` was added, so a stylesheet can run a
stylesheet. Three bugs that DocBook xslTNG and XSpec found and the W3C suites
did not were fixed with them.

### generate-id() must tell apart the nodes of a built tree

Reported from the field: a Schematron schema transpiled by SchXslt2 raised
`XTDE3365` on a duplicate map key, in this engine *and* in Saxon, from a
stylesheet this engine had generated. Both failing on the same generated file
is what said the error was correct and the stylesheet producing it was not.

`generate-id()` is built on `Node.Order()`, which combines a tree identity
with the node's document-order index. A tree assembled by a sequence
constructor is never finalized, so every node under one root still carried
the index `0` it was built with: the identity told two trees apart, and
nothing told the nodes within one tree apart. SchXslt2 keys a map on
`generate-id()` of each `sch:assert` and `sch:report`, so two distinct nodes
collided. `TestOrderDistinguishesUnfinalizedNodes` guards it.

## v1.1.0 — 2026-09-01

Additive throughout: nothing exported by v1.0.0 was removed or changed shape,
so a v1.0 program compiles and behaves the same.

### Conformance

|  | v1.0.0 | v1.1.0 |
|---|---|---|
| XPath 2.0 (QT3) | 99.99% | **100%** |
| XPath 3.0 (QT3) | — | **100%** |
| XPath 3.1 (QT3) | — | **100%** |
| XSLT 2.0 | 99.63% | **99.85%** |
| XSLT 3.0 | — | **99.79%** |
| XSD 1.0 schema / instance | 99.56% / 99.88% | **99.86% / 99.88%** |
| XSD 1.1 schema / instance | 99.18% / 99.89% | **99.88% / 99.89%** |
| RELAX NG (spectest) | 100% | **100%** |

**XSLT 3.0 is the headline.** v1.0.0 shipped XSLT 2.0; this release adds
packages (`xsl:package`, `use-package`, `accept`, `expose`, `override`),
accumulators, `xsl:evaluate`, `xsl:iterate`, `xsl:merge`, `xsl:try`, maps and
arrays, higher-order functions, JSON, and the 3.0 serialization methods.
Streaming is not implemented, and its 2,646 cases are out of scope rather than
failing — though measured with that gate lifted, 92% of them pass anyway,
because §19.1 lets a processor answer a request for streamed evaluation by
building the tree.

**Schema validity was the weak half and is no longer.** XSD 1.1 went from
99.18% to 99.88% — roughly a hundred and thirty missing schema-validity rules,
written one at a time and each measured against both versions so that no
agreement count ever fell.

**126 disagreements remain and none is a known defect in this engine.** Every
one is a suite defect, an expectation the W3C has itself challenged, a network
fetch, a vendor extension, or a Unicode snapshot that has moved.
[docs/conformance-gaps.md](docs/conformance-gaps.md) names each case and says
which; two open questions are recorded there as open rather than settled.

### Resolving schemaLocation without the network

Schemas name their imports as absolute URLs, and those fetches are unreliable
by design — the W3C throttles them. The W3C's own copy of the XSLT 3.0 schema
in the XSLT test suite was edited in 2021 to use a relative path, the comment
there giving the reason as "W3C web site throttling".

`xsd.CatalogResolver` answers a `schemaLocation` from memory, keyed by what a
reference *means* rather than how it is spelled: one entry answers the `TR/`
URL, the `2001/` URL, a bare relative path, and an `xs:import` that gives only
a namespace. A miss is an error rather than a request, which is what makes it
usable in a server. `xsd.W3CEntries` states the aliasing as data.

The schemas themselves are in a companion module, `w3cschemas`, because they
are W3C documents under W3C terms rather than MIT.

### Security: two nesting constructs the depth counter never saw

The v1.0.0 notes below describe an XPath depth bound "counted at the single
point every nesting construct passes through". That was an argument about the
grammar rather than a fact about it, and two constructs did not pass through
that point. Each exhausted the goroutine stack, which in Go is a *fatal error*
that `recover()` cannot catch — so an untrusted input killed the process
rather than failing the request.

* **Sequence types.** `parseSequenceType` recurses into itself for a
  parenthesised item type, for a function test's argument and return types,
  and for the member types of `map()` and `array()`. 400 KB of
  `1 instance of ((((…item()…))))` was enough at Go's default 1 GB stack, and
  it is reachable through any `@select`, `@test` or `@as`, and through
  `xs:assert/@test`.
* **XSD pattern facets.** The XSD-flavour regular-expression parser recurses
  once per group and counted nothing; a 6 MB schema was enough. Hostile-schema
  only: the XPath-flavour checker is an iterative scanner, so a pattern
  arriving as document data never reached it.

Both are now bounded at 1000 levels at their own recursion points, and each
construct has its own test rather than sharing one and an argument.

Also fixed: **a named function reference with no function library panicked**
where the equivalent call correctly raised `XPST0017`, and two schema-assembly
sites **leaked a reader** when a resolver returned one alongside an error.

### Known: nested occurrence bounds

Found by differential fuzzing against a brute-force reference, and invisible
to both W3C suites. A repeated group whose *only* child is itself repeating is
decided wrongly in both directions: for `<sequence minOccurs="5"
maxOccurs="5">` over `<element c minOccurs="2" maxOccurs="2"/>`, ten `c` is
the only valid document and is refused, while five `c` is accepted. **The
false-accept direction means a `minOccurs` floor is silently not enforced.**

A group with two or more distinct child names is decided correctly, which is
why 80,878 suite agreements step around it. Long-standing rather than new.
Diagnosed in [docs/known-gaps.md](docs/known-gaps.md); not fixed here, because
the fix is a matcher change the suites cannot defend.

### Also

* **`xsl:import-schema` can now read a schema carrying a `DOCTYPE`**, via
  `CompileOptions.SchemaParseOptions`. It still refuses one by default. The
  W3C's schema for schemas declares its entities that way, so without this the
  XSLT 3.0 schema could not be loaded at all.
* **An imported schema is read under the XSD version it declares.** A document
  carrying `vc:minVersion="1.1"` read as 1.0 is conditionally excluded in its
  entirety — silently, since that is what the attribute asks a 1.0 processor to
  do. The XSLT 3.0 schema is such a document, and all 81 of its element
  declarations were being dropped without an error.
* **`fn:load-xquery-module` reports the absent XQuery processor uniformly.**
  It has no processor to defer to, so every code it can raise describes that.

## v1.0.0 — 2026-08-24

### Security: three bounds that were not bounding

A third audit. Full detail in [docs/security.md](docs/security.md).

* **Entity expansion was charged once per entity, not once per reference.**
  Reachable with `AllowDOCTYPE: true` alone. A 70 KB document allocated 741 MB
  and was accepted. `MaxBytes` and `MaxNodes` both missed it — a reference is
  three bytes and a run of them coalesces into one text node. The bound was
  reported as working because a *different* code path charged correctly, so
  which path a document took decided whether it was bounded.
* **A nested XPath expression could kill the process rather than the request.**
  Reachable from a hostile stylesheet. A stack overflow in Go is a fatal error
  `recover()` cannot catch. Expression nesting is now bounded at 1000 levels.
* **RELAX NG nested `oneOrMore` is exponential in document width.** Reachable
  with default options from a hostile instance — a 189-byte schema and a
  63-byte instance cost over a second and a gigabyte. `MaxDepth` cannot bound
  it: the document is two levels deep however wide it grows. `MaxPatternSize`
  now holds the cost flat. This is a bound, not a cure; the structural fix is
  pattern interning.
* **`xsl:analyze-string` ignored the regex step budget**, so with the
  backtracking matcher enabled an exhausted budget was indistinguishable from a
  genuine non-match and the transform silently emitted wrong output.

One finding is left open and documented: compiling an *untrusted schema* can be
exponential in the depth of its group-reference graph.

### A constraint that never ran against the ordinary spelling

Unique Particle Attribution and Element Declarations Consistent were checked
only against a schema's *named* complex types. A type declared inline in an
element — the ordinary spelling — was never checked, so a schema with no named
types at all was checked against nothing and `(a?, a)` loaded clean.

This is a validator failing open rather than a missing conformance point, and
it is the second time the same shape has been found here: Particle Valid
(Restriction) had the identical gap earlier in this cycle. The lesson is
recorded in `docs/known-gaps.md` — when adding a schema component constraint,
verify the walk that reaches it visits anonymous types too. `xsd/upa_test.go`
now asserts both spellings, since every existing test used the named one.

Schema validity reaches **99.56%** on XSD 1.0 and **99.18%** on 1.1, both above
the ceiling this project's own analysis predicted. Also fixed: NOTATION as a
list item type, and `xs:anyAtomicType` as a restriction base, list item type or
union member.

### Circularity, and the exception that terminates every chain

Schema validity reaches 99.51% on XSD 1.0 and 99.11% on 1.1 — both at the
ceiling this project's own analysis predicted, with the remainder dominated by
tests the W3C's metadata disputes.

Four kinds of circularity were undetected, each because the structure that
would have revealed it is acyclic:

* A union whose transitive `{member type definitions}` contain the union
  itself (`st-props-correct.2`). The base-chain walk cannot see this: two
  unions naming each other have entirely acyclic base chains.
* A complex type reachable from its own `{base type definition}`
  (`ct-props-correct.3`).
* A circular substitution group (`e-props-correct.6`). The linker already
  survived cycles with a seen-set, so nothing downstream ever noticed one.
* `src-import.1.2` — an `<xs:import>` with no `namespace` requires the
  importing schema to have a `targetNamespace`.

The base-type rule needs the specification's "except for the ur-type
definition" exception, and it is not a courtesy: `xs:anyType` is its own base,
so the exception is what terminates every other chain. Omitting it rejected
**11,044 of 14,405 schemas**. That number is now recorded beside the check.

One test moves from passing to failing: `ste110`, "test circular union",
expects a schema where two unions name each other to be *valid*. Its W3C status
is `queried` under bug 4957, and it contradicts `st-props-correct.2` outright.

Also `cos-ct-extends.1.4.1`, checked on the source form rather than the
resolved content type because the extension splice rewrites the latter from the
base before any later pass could look; and the open-content half of
`cos-ct-restricts`, where a restriction declaring `{open content}` requires a
base that declares one and may not `interleave` where the base only appends.
Both open-content halves are conditioned on the derived particle admitting
something other than the empty sequence — with an empty model there is nothing
to interleave among, and `interleave` and `suffix` denote the same language.

### The harness claimed two mutually exclusive processor configurations

XSD 1.1 schema validity reaches 99.00%, and 10 of the 22 tests gained were
never a validator defect. `tests/xsdsuite` claimed support for both
`restricted-xpath-in-CTA` and `full-xpath-in-CTA`. Those name *mutually
exclusive* configurations, and a CTA test states an expectation for each:
`cta0022` is `valid` under full-xpath and `invalid` under restricted-xpath. The
later `<expected>` wins, so claiming both made the harness demand rejection of
schemas this processor correctly accepts. Only the token describing what is
implemented may be claimed.

The denominator is unchanged at 15,365 — these tests moved from failing to
passing, not from failing to skipped.

### Conditional type assignment, all-groups, and open content

Four rules, +12 on XSD 1.1 with XSD 1.0 untouched:

* `e-props-correct.5` (section 3.3.6) — every type an alternative can select
  must be validly derived from the element's declared type, `xs:error`
  excepted. Four of this project's own test fixtures declared schemas that
  violate this; they now name a common base, and what each test asserts is
  unchanged.
* A default alternative — one with no `test` — must come last (section 3.3.3).
* `cos-all-limited` (section 3.8.3) — a model group inside `xs:all` must
  itself be an all-group, and a nested all-group may not repeat.
* `cos-ct-restricts.2` (section 3.4.6.2) — an open-content wildcard must be a
  subset of the base's and may not loosen `processContents`.
* The wildcard arm of Element Declarations Consistent (section 3.8.6), where a
  strict or lax wildcard matches a global declaration whose type table differs
  from a like-named local particle's.

Three over-broad readings were caught and reverted before they shipped. A type
extending a base with open content and declaring none *inherits* it (section
3.4.2.3.3) rather than closing it, so the "extension mirror" rule rejected six
valid schemas. "`interleave` cannot restrict `suffix`" holds only when there is
something to interleave among. And the *type* half of wildcard EDC is a
validation-time check rather than a schema constraint — `wild061` says so
outright: the schema is valid though no document can satisfy it.

### XSD 1.1 wildcard attributes

Schema validity reaches 99.47% on XSD 1.0 and 98.85% on 1.1.

`namespace` and `notNamespace` on a wildcard are mutually exclusive — they are
two spellings of one `{namespace constraint}` property, and letting
`notNamespace` quietly win discarded what the schema author wrote. The check is
deliberately not version-gated: under XSD 1.0 `notNamespace` is an unrecognised
attribute in the XSD namespace, which is not a licence to ignore the conflict,
and that is what earns the two XSD 1.0 gains.

Every QName in `notQName` must lie in a namespace the wildcard's own constraint
admits (section 3.10.3). Excluding a name the wildcard could never match is a
contradiction rather than a narrowing. `##defined` and `##definedSibling` name
no namespace and stay unconstrained.

And `":stylesheet"` is not a QName. The resolver split it into an empty prefix
and a local name, then took the *no-prefix* path and accepted it as an
unqualified name; `xs:QName` requires a non-empty NCName on both sides of the
colon.

### Restricting xs:anySimpleType, and a contravariant wildcard rule

Schema validity reaches 99.45% on XSD 1.0 and 98.78% on 1.1.

`<xs:restriction base="xs:anySimpleType"/>` is now rejected. No clause forbids
it by name, which is why reading section 3.14.6 alone does not find it; the
rule is a three-step chain through the `{variety}` property. Part 2 section
4.1.1 makes `anySimpleType` the simple ur-type definition, whose variety is
*absent*; `cos-st-restricts` has a restriction inherit its base's variety; and
`st-props-correct.1` requires every simple type definition's variety to be
atomic, list or union. Absent is none of them. xmllint reports the same case as
"The variety is absent."

The rule stays narrow deliberately: naming `xs:anySimpleType` as a
*declaration's* type creates no new simple type definition and remains legal.

Wildcard Subset (section 3.10.6) clause 2 is **contravariant** in the excluded
set. The shared helper required two negated wildcards to exclude exactly the
same namespaces — correct for XSD 1.0, where a negation names one namespace. In
1.1 `notNamespace` names a *set*, and a negation of S1 is a subset of a
negation of S2 exactly when S2 is a subset of S1: excluding more admits fewer.
Equality is the special case where each set contains the other, so the fix
subsumes the 1.0 reading and needs no version gate. This also retires a
duplicate of the rule that had been added locally for attribute wildcards.

Also: derivation-ok-restriction clause 2.1.2 — where a restriction redeclares
an attribute, its type must derive from the base's, so a restriction could
previously widen an attribute's type freely. Only a simpleContent *extension*
may name a simple type as its base. And an unresolvable union member type is a
hard error rather than a deferred one, since a union cannot be built without
its members; the same treatment is deliberately *not* applied to a list's item
type, where two suite tests contradict each other and the existing deferral is
pinned by `missing006`.

### A valid document was being refused because an assertion crashed

XSD 1.1 defines the default collection in the dynamic context of an assertion
or type alternative as the empty sequence. It was left nil, so `fn:collection()`
raised FODC0002 — and because a type alternative whose test *raises* is silently
skipped, the failure was indistinguishable from a test that simply returned
false. `cta0022` fell through to its declared union and rejected a perfectly
valid `xs:date`. A crash wearing the costume of a wrong answer.

### Schema-validity: 99.40% on XSD 1.0, 98.72% on 1.1

A third round, +29 on 1.0 and +38 on 1.1 with nothing lost. Five rules:

* src-ct.1 and src-ct.2.1 (section 3.4.3) — which base shape each content form
  may sit on. `readSimpleContent` is the only path that sets a content type to
  simple, so the source form is recoverable without a new component field.
* cos-ct-extends.1.4.3.2.2.1 and derivation-ok-restriction.5.4.1.2 — mixedness
  consistency between a type and its base.
* derivation-ok-restriction.4, via Wildcard Subset (section 3.10.6), for
  attribute wildcards.
* Section 3.14.2 local simpleType form, and cos-list-of-atomic (Part 2 section
  4.1.5).

**Two over-broad readings were caught only by re-measuring**, and both would
have shipped as wins:

* **Mixedness is not symmetric.** Restricting a mixed base to element-only is a
  legitimate narrowing; only the reverse is forbidden. Extension is an "if and
  only if", restriction is one-directional. Reading them as one rule cost four
  tests.
* **cos-list-of-atomic must recurse through nested unions.** A union's members
  may themselves be unions. The test suite's own catalog schema defines a list
  of a union of eight unions, so rejecting any union-typed member rejected the
  catalog itself — and with it 91 instance tests per version.

### Instance validation reaches 99.88% on XSD 1.0 and 99.89% on 1.1

Instance disagreements fall from 48 to 31 on 1.0 and from 51 to 29 on 1.1,
with no schema-side regression. Nine rules, each measured on its own:

* cvc-elt 5.2.2.1 (section 3.3.4) — a fixed value constraint forbids element
  children. And 5.2.2.2.1: for a *mixed* content type the element's initial
  value must match the fixed value. Only the simple-content half of that
  clause, 5.2.2.2.2, had been implemented.
* cvc-type 3.1.1 on a nilled element. Clause 5.2 applies exactly when 3.2 has
  applied, and 5.2.1 still runs Element Locally Valid (Type); only 3.1.3 is
  conditioned on nilling. The whole rule was being skipped.
* Section 3.11.4 clause 3 — an identity-constraint field must select a node
  with a simple type. A complex-typed one fell back to its string value.
* Part 2 section 3.2.6.1 — duration seconds are `\d+(\.\d+)?`, so digits are
  required after the point; `PT12H30M12.S` was accepted.
* Both fixed-value checks validated the fixed literal as XSD 1.0 regardless of
  the schema's version. Under 1.1 `+INF` failed that validation, and since the
  comparison is guarded on it succeeding, the fixed check was silently skipped
  altogether.
* Section 3.14.4 — union member selection is per value: each side takes the
  first member whose lexical space contains its own literal. Trying every
  member until a pair agreed equated values of different primitive types.
* Section 3.9.6 Particle Emptiable 2.2.2 — an empty `<xs:choice/>` with
  `minOccurs="1"` admits the empty *language*, not the empty *string*. The
  vacuous-quantification answer is right for a sequence and wrong for a choice.
* The XML NameStartChar production. `isNameStartRune` accepted any rune at or
  above U+0080, which is far wider than the production; NEL and LS are among
  the characters it must exclude. This also governs `Name`, `NCName`, `ID`,
  `IDREF` and `ENTITY`.

### Schema-validity: 98.60% to 99.19% on XSD 1.0, 97.96% to 98.48% on 1.1

A second round adds src-redefine 6.2.2 and 7.2.2 (section 4.2.2): a `<group>`
or `<attributeGroup>` redefined without a self-reference must be a valid
restriction of what it replaces. The group form defers to Particle Valid
(Restriction); the attribute form applies derivation-ok-restriction clauses
2.1.1, 2.1.2, 2.1.3, 2.2 and 3.

Two things about where that check can run. It belongs in `applyRedefine` — the
only point where the original and its replacement both exist — and the
attribute half must be deferred to the post-fixup pass, because until type
fixups drain every base attribute still reads as untyped and the derivation
clause passes vacuously. Version gating came for free: threading the schema
version into the particle check makes the 1.1 all-group rule decide the one
test annotated invalid for 1.0 and valid for 1.1.

### Schema-validity, first round: the constraint that never ran

122 schema documents that were accepted despite being invalid are now
rejected, with no instance test and no other suite moving. Every one is a test
the W3C's own metadata records as `accepted`; no `queried` or bug-tied test was
targeted, which is the line this project holds — 48 of the remaining 1.0
disagreements and 50 of the 1.1 ones are suite disputes, 18 of them bug 4113
alone, where "passing" would mean freezing a Unicode 3.1 table and being wrong
about modern text.

The largest single cause was not a missing rule. `checkParticleRestriction`
walked `schema.Types`, which holds only *named* types, so Particle Valid
(Restriction) (section 3.9.6) never ran on a restriction written as an inline
`<xs:complexType>` — which is how most of them are written. The dispatch table
had been correct all along and was simply never reached for those types.

The rules that were genuinely missing:

* Clause 2.2 pointless-group inlining. `stripPointless` unwraps only the
  particle being compared, so a same-compositor wrapper among a group's
  *members* left the two sides of a Recurse at different depths.
* NameAndTypeOK clause 3.2.4 — the restricting declaration must block
  everything the base blocks. `#all` is masked to the three derivations
  `block` can name, since `#all` and "substitution extension restriction"
  denote the same set there (W3C bug 4144).
* src-include.1, src-redefine.1 and src-import.3.1/3.2: the referenced
  document's target namespace must match the referring one.
* src-redefine clauses 5, 6.1.1, 6.1.2, 6.2.1, 7.1, 7.2.1 and 2 — a redefined
  type must derive from itself, a redefined group has at most one
  self-reference with unit occurrence, and two children of one redefine may
  not redefine the same name. Answering the "defined in the redefined
  document" clauses needed a new fact: which document each include or redefine
  actually names, since the redefining document may declare the same name.
* localComplexType: `name`, `abstract`, `final` and `block` are prohibited on
  an inline complexType; `substitution` is not a legal token in a type's
  `block`; `targetNamespace=""` names no namespace; and an attribute
  declaration may not be in the schema-instance namespace.

Three XSD 1.1 particle tests move from passing to failing, and the change is
still correct: they are annotated `invalid version="1.0"` / `valid
version="1.1"` in the suite's own catalog, so they were false accepts in both
versions before and are now right in 1.0. Passing them in 1.1 needs the
section 3.4.6.4 intensional-restriction check — true language inclusion rather
than the structural table — which is not implemented.

### Entity replacement text inside an attribute value is included literally

XML 1.0 section 4.4.5, "Included in Literal": a reference inside an attribute
value has its replacement text included *as literal characters*, so a quote in
that text is data and does not end the attribute. The substitution path that
rewrites entity references into the source spliced the text in raw, so an
entity whose text contained a quote produced a malformed document.

DocBook is the case that found it — `entities.ent` declares

    <!ENTITY primary 'normalize-space(concat(primary/@sortas, " ", primary))'>

and every stylesheet using `&primary;` inside a double-quoted attribute failed
to parse. The fix tracks start-tag and attribute-quote state while scanning,
advancing it on the document's own bytes only: a quote arriving from
replacement text is data, and letting it change the state is precisely the bug.
Inside an attribute value the three characters that would be markup there are
written as character references. `&` is deliberately not among them, because
the rewrite path leaves it for the second parse to decode.

### EXSLT `node-set`

`{http://exslt.org/common}node-set` is available to stylesheets. XSLT 2.0
eliminated the result-tree-fragment type (section J.1.2), so on this processor
the conversion is the identity on its argument, and it passes the sequence
through rather than wrapping it — wrapping would change the node identity that
`generate-id()` and the union operators compare.

It is registered into the library a running stylesheet sees, not into
`xpath.Builtins()`. A plain XPath caller still gets XPST0017 for it, which is
what a processor is required to report for a function it does not have.

### Fixed: a multi-digit backreference to an unclosed group was renumbered

In the backtracking matcher, `\10` written inside the tenth group was split
into `\1` followed by a literal `0` rather than being rejected. Erratum FO.E24
makes a backreference to a group that has not yet closed a malformed pattern,
so this turned a required FORX0002 into a quiet non-match. The greedy split is
now attempted only when the number names no group at all. Single-digit
references were already correct; the bug needed two digits to reach the split.

### Backtracking regular expressions, off by default

`xpath.SetBacktrackingRegex(true)`, or `-backtracking-regex` on the command
line, enables a matcher for the backreferences RE2 cannot express:
variable-width groups, backreferences in the middle of a pattern, alternation
and lazy quantifiers.

It is off by default and should stay off for untrusted input. RE2 is linear in
the length of the input and cannot be made to backtrack; this engine has no
such guarantee, and a pattern is not always the caller's own — `matches($s,
$node/@pattern)` takes one from document data, so enabling it globally would
let a document being validated choose how long the validation takes.

Even enabled, a step budget bounds every match, and exhausting it raises
FORX0002 rather than returning a silent "no match": a budget that guessed
would do it precisely on the inputs where the answer was hardest to get. The
budget is measured from both ends — the hardest honest pattern in either
conformance suite answers in 525 steps, while `(a*)*\1b` against sixty `a`s
exhausts the whole budget in about 200 ms.

The default path is unchanged. Patterns whose backreferences are decidable by
the existing fixed-width analysis still take it, since that path is exact and
faster, and with the switch off the conformance failure set is byte-identical
to before.

With it on, nine XSLT tests and the last QT3 failure pass — XSLT 2.0 at 99.69%
and QT3 at 15,183 of 15,183. The headline figures below report the default
configuration.

### XSLT 1.0 backwards-compatible behaviour

`[xsl:]version="1.0"` now enables the behaviour XSLT 2.0 section 3.8 and XPath
2.0 section B.1 define for it, instead of raising XTDE0160. Argument coercion
takes the first item of a sequence where 2.0 raises a type error; comparison
with a node-set uses the existential 1.0 rules; arithmetic on a non-numeric
yields NaN rather than failing; `xsl:value-of` takes the first item;
`system-property('xsl:supports-backwards-compatibility')` answers "yes"; and a
call to an unavailable extension function becomes a *dynamic* XTDE1425 raised
only if it is evaluated, rather than a static XPST0017.

The flag is static, resolved at compile time and carried on the compiled
expression, so it reaches evaluation through a single write site. A stylesheet
that does not declare 1.0 is byte-identical to before; QT3 was measured five
times across the work and never moved.

One subtlety: the constant folder was baking in 2.0 answers, folding `1 + 1` to
an `xs:integer` where 1.0 requires `xs:double`. Folding is now withheld for the
operators B.1 redefines.

**This grows the measured denominator**, because 112 tests that declared the
feature as a dependency were previously skipped. 104 of the 107 newly in-scope
tests pass. The absolute count went from 6,028 passing to 6,132; the percentage
moved from 99.60% to 99.56%, because the admitted set is harder than the corpus
average. The number below is the honest one, and it is not comparable to
earlier figures measured over the smaller scope.

Two further fixes in backwards-compatible mode. Unary minus now converts its
operand with `fn:number` as B.1 rule 2 requires, so `-0` under 1.0 keeps its
sign — in 2.0 it is unary minus on an integer, which has no signed zero, and
`0` stays the right answer there. And an unprefixed atomic type name is
resolved in the default element namespace, which is inherited rather than read
from the element alone; `use-when` on a template whose sibling declares
`xpath-default-namespace` does not see that declaration.

### XSLT 2.0 conformance: 98.83% to 99.61% over a scope that grew

6,024 of 6,052 in scope, up from 5,982 of 6,053. 43 tests fixed across two
rounds, no regressions. XSD 1.1 gained one instance test (26,158 of 26,209);
XSD 1.0, QT3 and RELAX NG are unchanged.

The second round added `xsl:mode/@on-no-match` (all six values of section
6.6 — the attribute had been parsed and discarded, though 335 stylesheets in
the suite use it), the `item-separator` serialization parameter, `case-order`
as a tertiary tiebreak under a language collation, and per-node base URIs for
content that arrives through an external entity, so `xml:base` inside an
entity now resolves against the entity rather than its including document.

Two further wrong answers, both in the parser:

- Attribute values were not normalized per XML 1.0 section 3.3.3, so a
  literal tab or newline inside an attribute survived into the value. This
  had to be done at the lexical layer: after `encoding/xml` decodes, a
  character reference and the character it denotes are indistinguishable, and
  section 3.3.3 requires `&#10;` to survive where a raw newline becomes a
  space.
- Whitespace in element-only content declared by a schema was not treated as
  ignorable, the schema-side counterpart to the DTD fix in the first round.
- Type annotations are namespace-qualified. They were keyed on the bare local
  name in a process-global map, so a schema defining its own type with a
  built-in's local name displaced the built-in for every schema loaded
  afterwards — permanently, and across documents. The XSLT 2.0 schema does
  exactly that with `QName`, and says in its own text why. Names in the XML
  Schema namespace stay bare so that built-in comparisons are unaffected; only
  genuinely ambiguous names change spelling.
- A node whose type was a union atomised to `xs:untypedAtomic`, because a
  union's base is `xs:anySimpleType` and the derivation chain dead-ended there.
  A union's members are *siblings* rather than ancestors, so no single name can
  answer for both: the union stays on the node's annotation and the member
  chosen for that particular value is recorded beside it. `data(x) instance of
  member` and `x instance of element(*, union)` are now both true.
- A restriction *of* a union carries no member list of its own — it inherits
  one — so union validation iterated an empty slice and admitted nothing.
- Whitespace-only text was stripped from an element whose type is a simple
  type, or a complex type with simple content, when `xsl:strip-space` named it.
  Section 4.4 exempts such an element whatever the declarations say: the text
  is its entire typed value, and stripping it leaves an annotation describing a
  value the node no longer holds.
- `fn:idref` split its argument on whitespace. `fn:id` does, because its
  argument is a sequence of IDREFS values and an IDREFS value is a
  whitespace-separated list; `fn:idref` takes `xs:string` and matches it
  whole, so `idref('a c')` looks for the single name "a c" — not a valid
  `xs:IDREF`, and so matching nothing.

`xsl:variable` bindings in a sequence constructor are now skipped when the
name is never referenced later, which XSLT 2.0 section 5.2 permits: a
circularity is an error only if the variable involved is actually evaluated.
The check is deliberately one-sided, and a declaration whose own select
references its own name still binds eagerly, since XPST0008 is a static
error and is due whether or not the value is demanded.

New in the engine: `xsl:evaluate` and `xsl:iterate` (recognised under 2.0
semantics, which is what the suite's `XSLT20+` dependency means); the XPath
3.0 `||` and `=>` operators alongside the simple map `!`; the
`http://www.w3.org/2013/collation/UCA` collation family, backed by the CLDR
tables already vendored for `xsl:sort`; and backreferences in `replace()`.

Corrections worth noting, because each was a wrong answer rather than a
missing feature:

- `matches()` returned `false` for text it matches whenever a backreference
  was separated from its group by a variable-width expression. The soundness
  check examined only the group. A wrong answer is worse than the `FORX0002`
  refusal it now gives.
- Whitespace in DTD element-only content was not ignorable, so
  `xsl:preserve-space` could preserve what XML 1.0 section 2.10 defines away.
- `xsl:result-document` ran its body without binding variables declared
  inside it, so every reference to one raised `XPST0008`.
- An expression containing a braced URI literal silently lost the schema from
  its static context, sending every schema lookup down its "no schema" branch.
- `deepCopy` and the type-annotation stripper dropped the is-id and is-idrefs
  properties they were documented to preserve.
- A value derived by restriction from `xs:boolean` atomised to nothing when
  its lexical form carried the whitespace `xs:boolean` collapses.
- An `xs:QName` with no in-scope binding for its prefix was accepted; Part 2
  section 3.2.18 makes it denote no value.

The UCA collation refuses `caseFirst`, `alternate`, `maxVariable`, `reorder`
and `backwards` even under `fallback=yes`, where the specification permits
ignoring them. Ignoring a parameter that changes the *order* yields a sort
that is quietly wrong, which is worse for the caller than a refusal.

## v0.1.0 — 2026-08-22

First tagged release. Everything before this was unversioned, so this entry
describes what the library does rather than what changed.

### What it is

XPath 2.0, XSLT 2.0 and three schema languages in pure Go. No cgo, no JVM, no
libxml2.

| | Suite | Result |
|---|---|---|
| XPath 2.0 | W3C QT3 (FOTS) | 99.99% — 15,182 of 15,183 in scope |
| XSD 1.0 | W3C xsdtests | 99.88% instance · 99.56% schema-validity |
| XSD 1.1 | W3C xsdtests | 99.89% instance · 99.18% schema-validity |
| RELAX NG | James Clark's spectest | 100.00% — 965 of 965 |
| DTD | *no public suite* | content models, defaults, `ID`/`IDREF` |
| XSLT 2.0 | W3C xslt30-test, filtered | 99.63% — 6,136 of 6,159 in scope |

DTD has no percentage because no public conformance suite exists for it.

XSLT's percentage is not comparable to the others. There is no maintained
XSLT 2.0 suite — the original froze in 2007 — so this is the XSLT 3.0 suite
filtered by each test's declared version dependency, which measures something
different from running a suite written for the version under test. It is also
young, and the first runs were dominated by harness bugs rather than engine
ones, so read it as a floor. The differential against Saxon-HE 12.4 on two
production corpora remains the stronger evidence for real stylesheets.

### Packages

`xdm` (data model and parser), `xpath`, `xslt`, `xsd`, `dtd`, `relaxng`, and a
`go-xml` command-line transformer. Each is usable on its own; the layering is
strict and one-directional.

### Security posture

Every mechanism that reaches outside the document is off by default: `DOCTYPE`
is refused, `xsi:schemaLocation` is ignored, and no schema, document or entity
is fetched without a caller-supplied resolver. Input size, node count, nesting
depth and recursion depth are all bounded.

Two security audits have been run against the library, both recorded in
[docs/security.md](docs/security.md) with the findings and their fixes. The
second audit found four defects in code added during the same session and all
four are fixed here.

The one thing a caller must still do is **sanitise URLs when rendering
transform output as HTML** — XSLT does not, and is not supposed to.

### Known gaps

Every measured failure is listed in [docs/known-gaps.md](docs/known-gaps.md),
including fix attempts that were reverted for costing more than they gained.
The largest are:

* XSD schema-validity, at 99.56% (1.0) and 99.18% (1.1). Instance validation —
  what most callers do — is above 99.7% in both. A substantial share of the
  remaining disagreements are cases the W3C's own suite marks as disputed.
* RELAX NG's compact syntax is not implemented; only the XML syntax is.
* A DTD's external subset is never fetched, so validation against one is
  partial by design. `DTD.HasExternalSubset` says when that happened.

### Stability

**This is the 1.0 release: the exported API is now stable.** Every exported
name and signature keeps its meaning, and anything that has to break goes to
2.0 with a new module path.

The surface was reviewed before freezing rather than after, which is the only
time such a review is cheap. `relaxng` had already narrowed from 27 exported
symbols to 7; the same pass over the other packages found an exported mutable
global that any importer could corrupt, a package-scope `All` that named only
derivation methods, three exported fields typed by unexported types, and one
exported function with no callers at all. Those are fixed. A handful of
further narrowings are recorded in the commit log as deliberate non-changes,
because they are judgement calls rather than defects and 1.0 can carry them.

What is *not* frozen by this: the conformance figures, which are expected to
rise; the internal representation behind every interface; and the default
values of the resource limits, which may tighten if an audit finds a bound
that does not bound — three such were found and fixed shortly before this
release.

## Four more exact values narrowed without a range proof

The audit that produced the nine fixes in `cc17983` found four further sites of
the same family in the date, duration, and cast paths. Each converted an exact
value to a machine integer without first proving it fit, and each answered
wrongly rather than reporting a failure.

* **Ordering two year-month durations subtracted their month counts.** The
  difference between the two extremes is nearly twice the int range, so it
  wrapped and reversed the answer: `P768614336404564650Y gt
  -P768614336404564650Y` was `false`, and `max()` over the pair returned the
  negative one. The operands are now compared directly, which cannot overflow.
  The day-time path already compared exact rationals and needed no change.

* **The duration component accessors truncated before reducing.** Every
  accessor but `days-from-duration` reduces its quotient modulo its own unit —
  hours mod 24, minutes and seconds mod 60 — so the answer is small however
  large the duration is. The quotient was narrowed to `int64` *before* that
  reduction, discarding exactly the high bits the reduction needed:
  `hours-from-duration(PT10000000000000000000H)` answered `0`, and
  `days-from-duration(P10000000000000000000D)` came back negative. The modulo
  is now taken in big arithmetic and only the reduced value is converted.
  `days-from-duration` has no modulo to shrink its result, so it returns the
  arbitrary-precision `xs:integer` it is; `seconds-from-duration` takes its
  fraction against the exact quotient rather than the reduced one.

* **The ±14:00 timezone bound was checked after the narrowing.** The second
  argument to `adjust-dateTime-to-timezone` and its siblings was truncated to
  `int64` and divided by 60 before the range test, so an offset whose low bits
  landed inside the range was accepted: 129127208515966879312 seconds — some
  four trillion years — was silently taken as `+05:00`. The comparison is now
  made on the exact value.

* **`roundRat` returned an `int64` from an unbounded quotient.** It now returns
  the exact `*big.Int`, and its one caller — the month count of a scaled
  year-month duration — range-checks that before narrowing. The check moved
  from the truncated value to the rounded one, closing a gap of one: a product
  half a month below the limit truncated inside the range and rounded outside
  it.

Two further sites in `fn:format-dateTime` were identified and left alone: they
lie in a file another change was holding at the time. One converts a seconds
value that is mathematically constrained to `[0, 60)`, so it is safe but its
invariant is implicit; the other reaches `int64` through a `big.Float`, which
is the wrong instrument for an exact conversion.

The new tests assert results rather than the absence of an error, since the
failure mode throughout this family is a confidently wrong answer. Each was
confirmed to fail against a deliberate reintroduction of the narrowing it
covers.

### `roundGuard`: a test that named a branch it never entered

The regression test shipped with the `roundGuard` fix did not exercise the
branch it was named for. Instrumenting `roundPlaces` showed the whole `xpath`
package test suite entering the non-terminating branch **zero** times: every
generated value — `1/2^n`, `3*10^k`, `1/2` — terminates, so all 460 calls took
the `ratScale(r) == true` path or the negative-precision path. The audit that
raised this was correct.

`roundGuard` is now a package-level `var`, matching the seam
`xsd/budget_soundness_test.go` already uses for `maxPositions`, `branchLimit`,
`subsumeMaxStates` and `subsumeMaxProduct`. Tests inject a small guard and
restore it with `defer`; it is never assigned outside `_test.go`. Injection is
the only way to reach the branch at all — at the shipped `1<<20` it needs a
precision above a million, and `exactRound` would then build a bignum with a
million digits.

Four tests were added over genuinely non-terminating rationals (`1/3`, `1/7`,
`22/7`, `1/6`, `2/11`). They assert the branch was **entered**, by the one
observable no terminating value can produce: a precision above the guard
coming back reduced to exactly the guard, while a precision at or below it
passes through unchanged.

**The guard's contract, now stated and tested.** A non-terminating rational has
no finite decimal expansion, so for every precision `p` it lies *strictly*
between the two adjacent multiples of `10^-p` — never on one, never on the
halfway point. Rounding it is therefore decided by a strict inequality that no
further digit can flip: a tie is unreachable, `round` and `round-half-to-even`
must agree, and the result is never the identity and never zero. That is what
makes reducing the precision safe. It does *not* claim `round(x, p)` equals
`round(x, guard)` — those are different numbers — only that the reduced answer
is the correctly rounded value at the reduced precision, computed without a
fabricated tie.

**Policy, previously undocumented.** The code reduces precision rather than
raising an error, and that choice is now written down rather than left to be
inferred. An `xdm.ErrResourceLimit` refusal was considered and rejected because
the branch is **unreachable from production callers**: the `xs:integer` and
`xs:decimal` branches of `fn:round` hold terminating values by construction,
and the `xs:double` branch builds its value with `big.Rat.SetFloat64`, which
yields a dyadic rational — also terminating. A resource error no caller can
provoke would be untestable dead policy. A documented implementation precision
limit is the honest description of a fallback reachable only by future callers,
and the contract above is what such a caller would inherit.

The terminating-value tests are kept and strengthened: one now forces the guard
to 2, so a reintroduced clamp cannot hide behind `1<<20` exceeding any
precision a test would otherwise use.

Sensitivity was confirmed in both directions. Restoring the clamp on
terminating values failed `TestRoundGuardReductionPreservesValue`,
`TestRoundGuardKnownRegressions` and the new
`TestRoundGuardTerminatingNeverClamped`. Three separate corruptions of the
non-terminating branch — returning `identity` instead of a precision, an
off-by-one clamp, and a wrong threshold with a halved result — each failed the
new tests; the off-by-one and threshold variants passed every pre-existing
test, which is the gap this change closes.

## fn:format-date no longer claims the Julian calendar

`supportedCalendar` accepted `OS` alongside `AD`, `ISO` and the default. `OS`
is Old Style — the Julian calendar — not a third spelling of the Gregorian
one. Section 9.8.4.3 of *XPath and XQuery Functions and Operators 3.0* lists it
that way, and requires that the value "must be converted to a value in the
specified calendar and then converted to a string using the conventions of that
calendar"; its note formats the XML Schema date `1502-01-11` as *1 January
1502* to make the point that the value is converted rather than relabelled.

There is no Julian arithmetic anywhere in the engine. `formatDateTimePicture`
takes no calendar argument at all, so every calendar it accepts is rendered
from the same Gregorian fields. The result was that
`format-date(xs:date('2026-08-24'), '[Y0001]-[M01]-[D01]', (), 'OS', ())`
returned `2026-08-24` — the Gregorian date, thirteen days from the Julian one
in 2026. That is a wrong date, not a wrong label, and it was returned silently.

`OS` is therefore no longer claimed. Which calendars are supported is
·implementation-defined· and the specification requires only that at least one
be supported, so declining a calendar the formatter cannot compute is the
conformant answer; the name now falls to the existing `FOFD1340` path. The
supported set is exactly the two Gregorian spellings the formatter implements.

The alternative — implementing Julian conversion — was rejected as
disproportionate. It is not one function: the conversion interacts with the era
component, with the year-zero convention (which the specification says differs
between `OS` and `ISO`), and with the `$place` argument, since countries using
Old Style began the year on different days and disagreed about leap years. None
of that is exercised by any test in either suite.

ISO week numbering was audited at the same time and needed no change.
`weekOfYear`, `weekOfMonth` and the `[F]` component already implement the
ISO 8601 rules the same section fixes — weeks running Monday to Sunday, week 1
being the one containing the year's first Thursday, and a week belonging to the
month containing its Thursday. These were untested from XSLT and now are:
2005-01-01 is week 53 of the previous year and 2007-01-01 is week 1, which is
what separates the ISO rule from numbering weeks from 1 January.

Sensitivity was confirmed: restoring `"OS"` to `supportedCalendar` failed
`TestFormatDateCalendarArgument` with `got 2026-08-24, want FOFD1340` — the
wrong date the change removes.

## xslt: a lossy grouping key merged distinct integers into one group

`xsl:for-each-group group-by=` found a group by hashing the key value to a
string and looking it up in a map. For an `xs:integer` or `xs:decimal` that
string is `xpath.GroupingKey`, which formats the value through a `float64`, so
it is lossy exactly where those two types hold a value no double can name:
`9007199254740992` and `9007199254740993` produce the same string, as does
every value past the double range once it formats as `+Inf`.

The map lookup trusted a HIT. `groupByKey` already carried an exact rescan for
erratum E25, but it runs only on a MISS, so a wrongly-matching key was never
re-verified. Distinct `xs:integer`s above 2^53 were therefore put in one group,
and a population above 1.8e308 collapsed into a single group — with a count
that nothing in the result reveals as wrong. Grouping now verifies a numeric
hit with `xpath.GroupingEqual` before joining, which is the same comparison the
miss path already used.

The fix is deliberately in the grouping code and not in the key. Making the key
exact — appending the value's rational — was tried first and is wrong: grouping
compares with `eq`, which is **not transitive** across the numeric types.
`xs:decimal("1.00000000000000000001")` and `xs:decimal("1.00000000000000000002")`
are unequal, yet each equals `xs:double("1.0")`, and no single string can
express a non-transitive relation. The attempt also regressed four suite cases
(`key-003`, `key-004`, `key-068`, `backwards-039`), because `xsl:key` and the
accumulators index by the same string with no rescan at all and routinely store
a key under one numeric type and seek it under another — `key-004` stores
`number(q)` as `xs:double` 3.7 and seeks the `xs:decimal` literal 3.7. The key
is a bucket; the comparison decides.

`fn:distinct-values` was audited and needed no change: it compares numerics
pairwise with `compareValues` and never reaches the lossy key.

Sensitivity was confirmed: removing the hit verification failed
`TestGroupByKeyIsExactBeyondDouble` on all three cases — 2^53 and 2^53+1 in one
group of two, the two sub-precision decimals in one group of two, and the three
values above 1.8e308 in a single group of three.

## xslt: system-property and its neighbours truncated a sequence argument

`stringArg` read `atoms[0]` of whatever it was given, so
`system-property(('xsl:version','xsl:vendor'))` answered `"3.0"` as though the
second item had not been written. Every parameter it reads is declared
`as xs:string` in XSLT 3.0 — no occurrence indicator — so under the function
conversion rules a two-item argument is `XPTY0004` and the empty sequence is
too. The declared cardinality was checked against the vendored specification
for all nine call sites before the behaviour was changed:
`fn:system-property`, `fn:function-available` (both arities),
`fn:type-available`, `fn:element-available`, `fn:unparsed-entity-uri`,
`fn:unparsed-entity-public-id` (both arities) and the one-argument
`fn:current-merge-group`. All nine are singletons; none is `xs:string*`, so
none was spared.

`stringArg` now returns an error and names the function in it. It mirrors
`xpath.argAtomicRequired` rather than calling it — that helper is unexported
and takes the whole argument list — including its use of `AtomizeChecked`,
which reports `FOTY0013` for a function item where plain `Atomize` drops it.

Sensitivity was confirmed: restoring the truncating read failed
`TestStringArgIsASingleton` on all seven subtests, with
`system-property(('xsl:version','xsl:vendor'))` again answering `"3.0"`.

## xpath: [Y] on a BCE year is correct, and is now pinned

`format-date`/`format-dateTime` with `[Y]` renders `xs:dateTime(
"-1000000-06-15T12:00:00Z")` as `1000000`, with no minus. This was reported as
a dropped sign. It is not one: the F&O 3.0 component table defines the
specifier as "Y — year (absolute value)" and gives the sign its own specifier
in the same table, "E — era". `[Y] [E]` answers `1000000 bc`, which is correct,
and emitting a minus from `[Y]` would state the sign twice for every picture
that already names `[E]`.

No code changed. `TestFormatDateTimeYearIsAbsoluteValue` is a characterization
test recording the rule, so that the next reader of the `if y < 0 { y = -y }`
line finds it rather than the surprise. Sensitivity was confirmed by making
`[Y]` emit the sign: the test failed with `-1000000 bc` and `bc -1000000`,
which is the double-signed output the rule exists to prevent.

## xsd: an identity field typed as a union compared spellings, not values

Identity-constraint equality is defined on values. `keyString` already builds a
type-tagged canonical form for every field before the sequence is joined, so
`3.0` and `3` collide as one `xs:decimal`, `007` and `7` as one `xs:integer`,
`1` and `true` as one `xs:boolean`, and a keyref written `5.0` resolves a key
written `5`. All of that was already right; the audit's reading of the joined
string as a lexical comparison was wrong, and `TestIdentityTypedEquality` now
pins the sixteen cases as verdicts rather than leaving it to be re-derived.

One family was not right. `recordKeyValue` asks `primitiveOf` for the field's
primitive and gives up when there is none, and a union has none of its own —
so a union-typed field recorded no value at all and fell back to comparing the
raw string. Two elements carrying the decimals `1.5` and `1.50` are one value
and did not collide under `xs:unique`: a false negative, an invalid document
accepted.

The member that validated supplies the primitive. `unionMemberFor` already
existed for `fixedValueEqual`, which needs the same answer for the same reason:
the member is the one the value is valid against, not any member that happens
to accept it. stE054 is why that distinction is load-bearing — scanning every
member makes a boolean and a double compare equal.

Sixteen lines in `recordKeyValue`. Both suites are unchanged at 39347 and
41532 agreements, with the failing sets identical by name, so no suite case
contradicts the rule.

The other half of the audit item — that `keySep = "\x1f"` could forge a field
boundary — stays latent and is now pinned rather than argued.
`TestIdentityKeySeparatorUnreachableInXML10` asserts that the parser refuses
U+001F in character data, which is what makes the joined string safe under XML
1.0. When XML 1.1 lands that test fails and names the work, which is the
dependency `docs/todo.md` already records. Replacing the join with a tuple
today would buy nothing and would disturb the three invariants below.

Those invariants now have tests of their own, because the rewrite this item
asked for would have had to preserve them: ambiguity is terminal
(`TestIdentityAmbiguityIsTerminal`, over one to six sibling definers, since the
reverted bug was right at two and four and wrong at three and five), tables are
seeded from `below.targets` before the walk
(`TestIdentitySeededFromBelowBeforeWalk`), and the pruned walk stays linear
(`TestIdentityWalkStaysLinear`, asserted on the work counter rather than on
elapsed time).

Each was proved sensitive by sabotage. Making ambiguity non-terminal failed the
first at three and five siblings exactly as the historical bug did; skipping
the seed produced 416 and 234 oracle disagreements plus the cross-level cases;
dropping the keyref check failed the keyref oracle; disabling `canonicalValue`
failed the typed-equality cases across every primitive; and disabling the union
fix failed the new union case. `visited/node` is unchanged at 1.0/1.9/2.0.
