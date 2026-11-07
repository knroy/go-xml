# Changelog

Notable changes, newest first. Versions follow [semantic
versioning](https://semver.org): from 1.0.0 the exported API is stable, and a
breaking change means 2.0 with a new module path. See *Stability* below.

## Unreleased

### Added

| Change | What it does | Commit |
|---|---|---|
| Streamed record paths with `//`, wildcards and `outermost()` | `//rec`, `doc//rec`, `/*/*/rec`, `*:rec`, `p:*` stream; nested matches ride inside their outermost record. Small records share one tree: 900-deep `count(//rec)` 2.0 s CPU. | [`df9677e`][df9677e], [`fd31bb3`][fd31bb3], [`382983c`][382983c], [`22af5f4`][22af5f4] |
| Streamed core for `xsl:source-document` | One streamed path for every trusted container: wrappers, choose, loops, split selects; any error re-runs on the tree, so errors are the tree's. W3C streamed cases 450 → 2,661. | [`ff0c463`][ff0c463], [`44db07f`][44db07f], [`096a587`][096a587], [`d1a98a8`][d1a98a8] |
| `Stylesheet.TransformReader`, `TransformOptions.SourceParseOptions` | Applies a stylesheet to a reader, streaming it when the initial mode is streamable and verified (a 1 GB filter in 620 MB heap, not 7.3 GB); otherwise exactly Parse + Transform. | [`bdf45d8`][bdf45d8] |
| Streamed `xsl:apply-templates` | Template rules, built-in rules, `next-match`, `apply-imports` and tunnels run on the cursor in a streamable container: a 1 GB filter in 624 MB, not 7.4 GB. | [`c8c0a01`][c8c0a01] |
| Streamed `xsl:for-each-group` | group-adjacent, -starting-with and -ending-with over a child path stream in a streamable container or rule, each group as it closes, `current-group()` read once off the cursor: a 1 GB log grouped in 158 MB heap, not 6.0 GB. | [`fe1eb18`][fe1eb18] |
| Streamed `xsl:try` | A try in a streamable container or rule streams its body; a caught error skips the rest of the context on the cursor and the catch runs on the start tag's view. The catch sees the tree's first error. | [`2447ca0`][2447ca0] |
| Stylesheet functions in streamed selects | A call whose body is its value exactly (one `xsl:sequence`, no failing conversion) is split as its body; `path ! f(.)` of atomic values reads as `path/f(.)`: an absorbing function over 1 GB of records in 3 MB heap, not 11 GB. | [`6abbb27`][6abbb27] |
| Streamable attribute sets | `xsl:use-attribute-sets` on streamed literal result elements, `xsl:element` and `xsl:copy`. | [`26caed6`][26caed6] |
| Streamed accumulators | `xsl:accumulator` values are computed as a streamed document is read, in a streamable `xsl:source-document` and through `TransformReader`: a running total over 1 GB in 3 MB heap, not 15 GB. | [`42902f3`][42902f3] |
| Streamed `xsl:fork` and `xsl:map` | Several prongs or entries reading one streamed document each run on a parse of their own over one read of its bytes, in turn; outputs in prong order, the first error the tree's. | [`2a1d6ad`][2a1d6ad] |
| Streamed `xsl:merge` | Each `for-each-source` document of a streamable merge source is read on its own cursor and merged by key as the tree merges: two 512 MB logs in 3.9 MB of heap. | [`2089255`][2089255] |
| `TransformOptions.Output` | Serializes the principal result to a writer as it is built: a 1 GB identity in 4.5 MB heap, not 9.4 GB. Written nodes no longer count toward `MaxNodes`; buffered results keep the cumulative count. | [`34e6b36`][34e6b36] |
| `TransformOptions.SecondaryOutput` | Each `xsl:result-document` is serialized to the writer the function returns for its URI, as it is made, and closed at its end; a 1 GB split into 7,018 files in 5.6 MB heap. | [`e94d4e0`][e94d4e0] |
| `TransformOptions.SourceValidation` (`SourceStrict`, `SourceLax`) | Validates the principal input against the stylesheet's schema; `TransformReader` validates it while streaming, with the tree's typing, defaults and error. | [`081c9d3`][081c9d3] |
| `TransformOptions` `MaxNodes`, `MaxBytes`, `MaxNodesBuilt`, `MaxBytesBuilt` | Held and total-work bounds for result and temporary trees and strings. Zero keeps the defaults; negative removes the bound. | [`294b965`][294b965] |
| `xslt.CompileContext` | `Compile` with a context: a call waiting for the compile slot returns `ctx.Err()`; the static phase and nested `fn:transform` compiles run under it. | [`cfa135c`][cfa135c], [`4694fa3`][4694fa3], [`0ecee9c`][0ecee9c] |
| `CompileOptions.MaxSchemaBytes`, `xslt.DefaultMaxSchemaBytes` | Total schema source `xsl:import-schema` may read per compile, closures included; zero means 16 MB. | [`4ad352d`][4ad352d] |
| `xdm.FlattenContext`, `XIncludeOptions.Context` | A cancellable `Flatten` that returns its step refusal as an error, and a cancellable XInclude pass. | [`9399efd`][9399efd] |
| `c14n.FromXPathFilterContext`, `FromXPathContext` | XML-DSig XPath transforms under a context; a filter's per-node evaluations share one item and byte budget. | [`2a41550`][2a41550] |
| `c14n.FromXPathFilterHere`, `FromXPathHere` | XML-DSig filters can call `here()`: the node bearing the expression, a text node read as its parent; a node outside the document is refused. | [`e0d2c13`][e0d2c13] |
| `xdm.ParseOptions.Warn` | Receives xml:id errors (duplicate ID, non-NCName value) as XQDY0091 `*xdm.Error`s, at most 100 then one summary; nil stays silent. | [`0caab03`][0caab03], [`c6508c3`][c6508c3] |
| `xslt.StreamingDocumentResolver`, `FileResolver.OpenDocument` | A resolver with `OpenDocument` streams streamable `xsl:source-document` and merge sources; one without it still builds the tree. | [`0d9c103`][0d9c103] |

### Changed

| Change | Problem → solution | Commit |
|---|---|---|
| `(P/position()) ! R` streamed position 1 for every item (the tail ran on each match's copy) | A path tail reading `position()` or `last()` of P's focus is refused for streaming. | [`1f0c721`][1f0c721] |
| An `xsl:iterate` over copies reading `last()` failed (XPDY0002) or re-ran on the tree | It is sized as a for-each over `copy-of(P)` is, or refused when the plan is made. | [`f4a7e22`][f4a7e22] |
| Copies of a streamed match ordered after a document the body loaded first (`union`, `<<`, `>>`) | The stream's tree id is fixed when its cursor opens. | [`0824c9c`][0824c9c] |
| A path's dedupe, `outermost()` over a stream node or `doc()` of its URI streamed wrong counts (since v2.2.0, [`3f185fe`][3f185fe]) | The alias guard counts them as uses. | [`a31a4fe`][a31a4fe] |
| Accumulators read on `copy-of(.)` of a streamed match or document answered with the copy's own values | The copy records its original's values and answers with them (§18.2.10). | [`9155044`][9155044] |
| With an accumulator declared, every streamed copy and its original were kept to the end: 1.4–1.6× the tree | A copy's record lives as long as the copy; loops hand temporaries back. | [`19b9185`][19b9185] |
| A streamed `xsl:iterate` carrying a large tree re-walked it after every item: 39 s against the tree's 0.1 s at 0.9 MB | A carried tree is measured once, an unchanged value not at all, after the cheap checks. | [`18670ae`][18670ae] |
| Streamed reads (a string value, an idle split loop, the drain) ignored a cancelled context, so an endless stream ran on | The cursor fails its next read with the context's error. | [`68b9320`][68b9320] |
| Cancelling a streamed `xsl:source-document` or `xsl:merge` re-ran it on the tree, building the whole document first | A cancelled stream returns the context's error, with no re-run. | [`383ed3d`][383ed3d] |
| A `group-by` in `xsl:fork` under `TransformReader` charged its event copies to `MaxNodes`, failing where the tree succeeds | They are built outside the result budget. | [`368b6c0`][368b6c0] |
| Under `Output`, an identity with a rule for `/` built the whole document (28 MB against 1.5 MB) | With `Result.Nodes` unused a kept leaf's copy serves, and nothing is built. | [`0201119`][0201119] |
| Streamed recursion through an inlined function failed as template recursion, where the tree raises XPDY0001 | The call is bounded by the XPath call depth, as the tree's is. | [`b2f4cb9`][b2f4cb9] |
| Compiling a multi-module stylesheet walked every module once per module (DocBook +37% time, +102% allocations) | The cross-module walk is memoised per module set. | [`9ac0f40`][9ac0f40] |
| A stream parse started a 64-byte text block and rebuilt its whitespace table after every element | Both carry over rewinds: a drain 9% faster, 3.3M allocations down to 2.9k. | [`edd4923`][edd4923] |
| Tree mode too: built-in template rules ignored a cancelled context, so an identity ran to completion | They check it per node and return the context's error. | [`68b9320`][68b9320] |
| Tree mode too: a `FileResolver` refusal named the resolved path and roots, revealing whether outside files exist | It names the href as written, and is `fs.ErrPermission`. | [`2f825da`][2f825da] |
| Tree mode too: an `xsl:merge` `for-each-source` href that is not a URI (a backslash, a bad `%`-escape) raised FODC0002 | It raises FODC0005, as `xsl:source-document` does. | [`acc5556`][acc5556] |
| Tree mode too: with an accumulator declared, every `copy-of()`/`snapshot()` copy was kept to the transform's end | A dropped copy is freed; output unchanged. | [`19b9185`][19b9185] |
| `xdm.ParseBursts` built an element off its path whole before dropping it | It now drops it as it parses, holding only the open path. | [`d1a98a8`][d1a98a8] |
| Streamed loop errors could come in a different order from tree mode | The burst loops are gone; the core re-runs on the tree after any error. | [`d1a98a8`][d1a98a8] |
| A consuming attribute value template or an `xsl:sequence` of nodes sent a streamed container to the tree | One consuming value-template part per element, and `xsl:sequence` in element content, stream as copy-of does. | [`fe1eb18`][fe1eb18] |
| A streamed `xsl:iterate` that carried a copy of its item, or read it in `xsl:next-iteration`, ran on the tree | The parameter is read off the cursor at the item; copies are carried on. | [`009d510`][009d510] |
| A grouping over grounded values (`group-by`, `xsl:sort`) in a streamed container ran it on the tree | Its select is read off the cursor; the tree's grouping runs over the values. | [`09174d4`][09174d4] |
| A streamed `treat as` over a path atomized the path's nodes first, raising XPDY0050 and re-running on the tree | The operand of `treat as` is kept as it is. | [`ba6c23f`][ba6c23f] |
| `accumulator-before`/`-after` on a node copied out of a stream walked the copy's tree | It answers from the stream's observer, or the container falls back to the tree; values are never computed over a copy. | [`42902f3`][42902f3] |
| A fork or map in a loop over records sent its container to the tree | The loop runs its body on a grounded copy of each record; `$m('k')` and `?k` on a variable proven a map or array are motionless. | [`2a1d6ad`][2a1d6ad] |
| A host function item reaching streamed evaluation could hang the transform | Every goroutine streaming evaluates on (prongs, parses, merge inputs) hands xpath's EvalLock back at each switch. | [`83e45c5`][83e45c5] |
| A namespace node added after an element's content, under `Output` | Raises XTDE0410 (§5.7.1) where the tree builder admits it: the streamed start tag is already written. | [`44d2374`][44d2374] |
| A streamed select at the document node that needs its matches (remove, treat as, deep-equal, a sequence of copies) ran the container on the tree | The matches are held, capped at the records the tree would hold; past that the tree answers. | [`e629035`][e629035] |
| A streamed count, exists, empty, boolean or not over remove/insert-before/subsequence/tail/... of a node path held copies of the matches | It counts them: no copy is made. | [`48b2598`][48b2598] |
| A validation or type attribute on a constructor in a streamed container ran it on the tree | The constructor streams and validates what it built, as the tree does; on `xsl:copy` only validation strip or preserve. | [`2f9f1b4`][2f9f1b4] |
| `xsl:copy` with a consuming select ran its container on the tree | It streams as a loop over its one item, the body reading the item's content off the cursor. | [`41ec3ba`][41ec3ba] |
| A consuming `xsl:on-empty` or `xsl:on-non-empty` ran its container on the tree | Both stream as `xsl:sequence` does; the §8.4.4 order is the tree's. | [`e65c621`][e65c621] |
| union, intersect and except of two streamed paths sent the container to the tree | One walk tests each node against every operand's path; a match is delivered once, in document order. | [`7765fca`][7765fca] |
| The identity template in a streamable mode, written with `select="@*"` and `select="node()"`, did not stream the principal input | Leaf and attribute rules apply templates over their empty children on the copy; `select="node()"` is the children. | [`75d485f`][75d485f] |
| A simple map or a position filter over streamed paths (`ITEM ! (PRICE \| QUANTITY)`, `(A \| B)[position() mod 2 = 0]`) ran on the tree | `P ! Q` reads as `P/Q` where that is the map exactly; the filter is applied to the values. | [`966b4ac`][966b4ac] |
| A positional step predicate other than `[n]` (`ITEM[position() lt 4]`) sent the container to the tree | It is counted on the cursor per context, as `[n]` is. | [`aea7acd`][aea7acd] |
| A for-each or iterate over `head()`, `subsequence()`, `insert-before()`, `($v, P)`, `P/@a`, `copy-of(P)` or a motionless choice ran on the tree | The select is normalised into the walk: motionless parts first, windows over the matches. | [`d1efae2`][d1efae2] |
| apply-templates over `head()`, `subsequence()` or `remove()` of a streamed path ran its container on the tree | Each match passes the window at its start tag and is dispatched as a bare path's is. | [`ddaa14c`][ddaa14c] |
| `count(P union $v)`, `P except $v`, `$v intersect P` over a streamed path ran its container on the tree | A variable's nodes are not the stream's: the operator is rewritten, the path still read and the variable checked. | [`30b4475`][30b4475] |
| A streamed select over `P ! R` making nodes, `tail(P)`, or `count(if (C) then A else B)` ran on the tree | R is P's per-match tail, `tail()` skips a match, a motionless choice is hoisted. | [`80f2586`][80f2586] |
| A text value template in a streamable body sent the container to the tree | Its parts read as an AVT's: motionless on the view, or one consuming part split on the cursor. | [`d37b98c`][d37b98c] |
| An `xsl:if` or `xsl:when` whose test reads the streamed node sent the container to the tree | The test is split on the cursor as `boolean(test)`; the motionless body runs on the view. | [`7a35cf3`][7a35cf3] |
| An array or map constructor, lookup or `array:`/`map:` call over a streamed path ran on the tree | The path is split inside it and the value built from the collected values; `array:size` counts members. | [`2beca7b`][2beca7b] |
| Kept text, comment and PI nodes of a streamed document (`copy-of //text()`, a loop over `text()`) ran on the tree | They are copies with the tree's identity, order and ancestors; leaf patterns may name ancestors. | [`66766f4`][66766f4] |
| A tail climbing from streamed matches (`//P/ancestor-or-self::*/@*`) ran on the tree | Climbed nodes are taken from the matches' views once each, in document order, read only for what a view has. | [`399418f`][399418f] |
| A simple map climbing from streamed matches (`P ! ancestor::*`) ran on the tree | P runs on the cursor with R as its per-match tail on the view, not merged across matches. | [`3c57043`][3c57043] |
| A path with a tail after text matches (`outermost(//PRICE/text()) ! (.+1)`) ran on the tree | The tail runs on each leaf's copy. | [`df0bc8f`][df0bc8f] |
| fold-left, filter, for-each, distinct-values, codepoints-to-string over a streamed path sent the container to the tree | The path's values are collected and the call made once at the context's end; function items in it are motionless. | [`ef2ae38`][ef2ae38] |
| `fn:trace` around a streamed select sent it to the tree | It is read as its argument, which is what it returns. | [`05eef52`][05eef52] |
| `xsl:message` in a streamable container or rule sent it to the tree | It streams; messages are the tree's, once each and in order, through re-runs and fork prongs. | [`4e19855`][4e19855] |
| A streamed `xsl:try` refused sum/avg/min/max over a path whose items could fail first | The fold cannot fail on numeric items or values a predicate compared numerically: the try streams. | [`1126c8b`][1126c8b] |
| An `xsl:fallback` in a streamed body, or an `xsl:iterate` parameter defaulting to `map{}`, sent the container to the tree | The fallback of a recognised instruction is skipped; map and array constructors are focus-free defaults. | [`9ca9f64`][9ca9f64] |
| A filter over a constant or variable beside a streamed select counted as a second consumer | Its predicates read the base's items, not the stream: it is motionless. | [`39e1a8e`][39e1a8e] |
| `TransformReader` did not stream a stylesheet with a global whose content is literal | Literal content reads nothing of the context item. | [`2303780`][2303780] |
| A mode with a rule for maps (`.[. instance of map(*)]`) did not stream its principal input | Such a rule matches no node of the input. | [`65212a3`][65212a3] |
| `snapshot()` of a streamed path written into an element sent the container to the tree | Each match is copied as it is read, which is what the snapshot's copy writes. | [`24fb4d6`][24fb4d6] |
| `xsl:result-document` in a streamed body sent the container to the tree | It streams as a wrapper; its result is built in memory, and a re-run takes back the failed run's results. | [`4de00c8`][4de00c8] |
| Under `TransformOptions.Output` with `indent="yes"`, an element was held as a tree to its end | It is held as its serialized bytes, indents recorded and kept or dropped once the serializer's rule decides. | [`f11ed2b`][f11ed2b] |
| `group-starting-with="."` or `group-ending-with="."` sent a streamed grouping to the tree | The pattern `.` is decided on any view. | [`691f6d1`][691f6d1] |
| A grouping over copies (`Order/copy-of()`) in a streamed body ran on the tree | The copies are numbered as the tree's select numbers them, so they order alike. | [`f08285d`][f08285d] |
| `current-group()` of a grounded group inside a nested `xsl:source-document` was unknown to the analysis | Allowed, as for any focus change over a grounded group (si-group-051, as the catalog expects). | [`a504b6b`][a504b6b] |
| A streamed merge whose sources wrote `data-type="{...}"` and `data-type="text"` ran on the tree | One type once the template is "text". | [`449f068`][449f068] |
| A streamed merge whose action called a named template or wrote `xsl:result-document` ran on the tree | The template is walked by the same rule; secondary results are taken back on a re-run. | [`df18696`][df18696] |
| Every streamed event cost a goroutine switch | The cursor parses in steps on the reader's goroutine: a 100 MB identity 1.73× → 1.43× the tree's CPU. | [`158bbb7`][158bbb7] |
| Each reading `xsl:fork` prong parsed the document again | Prongs that are folds share one walk: four aggregates over 100 MB 3.4× → 1.37× the tree's CPU. | [`bb81a54`][bb81a54] |
| `fn:transform` reported a nested XTSE3430 as FOXT0002 (fn-transform-77) | A static error code written as a trailing `(XTSE3430)` is kept, as F&O 3.1 §16.3.2 says. | [`5c154ba`][5c154ba] |
| A rule applied to a streamed group's member that read `accumulator-after` of it gave `TransformReader` an internal error | It reads the value as anywhere else. | [`d0ed436`][d0ed436] |
| A streamed merge's re-run took back secondary results but not messages, so a merge under `xsl:try` told each message twice | The re-run takes back the messages its failed run told. | [`c257efe`][c257efe] |
| An `xsl:source-document` with validation or type ran on the tree (parse, then validate a copy) | The document is validated as the cursor reads it; views and copies carry the tree's typing and defaults; an invalid one gets the tree's error. | [`d79951f`][d79951f] |
| A streamed merge source with validation, and a validated principal input, ran on the tree | Both validate on their cursor; `input-type-annotations="strip"` strips on the stream. | [`5772ca2`][5772ca2] |
| An `xsl:try` under `TransformOptions.Output` held its whole output as a tree until it ended | Each item it completes is written provisionally and rolled back on a caught error: a 100 MB loop in 41 MB heap, not 430 MB. | [`4cce5af`][4cce5af] |
| `rollback-output="no"` under `Output` buffered like "yes" | With a catch for every error its output is written through; XTDE3530 exactly as the tree raises it. | [`ba1fbb6`][ba1fbb6] |
| `TransformReader` gave "streaming: internal error" when a result-document body held a node of the stream | The stylesheet compiles to the tree path instead: the refusal is made at compile time. | [`971e795`][971e795] |
| XTDE1500 was missed when a read and a written `file:` URI named one file through a symlink (macOS `/var`) | A read is recorded under its real path and the URI it was asked for; hrefs compare lexically ([`e7f1116`][e7f1116]). | [`c5dbb81`][c5dbb81] |
| A union of a streamed path with a variable's nodes under `copy-of`, `sum` or a filter sent the container to the tree | The split hands the matches to the outer, which unions them as the tree does. | [`96d1d94`][96d1d94] |
| A `group-by` over a streamed population in `xsl:fork` sent the container to the tree | The population is held as copies under the tree's cap and grouped by the tree's code. | [`d1005db`][d1005db] |
| A union of two streamed paths sharing a tail (`A/ancestor::* union B/ancestor::*`) sent the container to the tree | It streams as `(A union B)/T`. | [`fe113df`][fe113df] |
| A loop over `snapshot(P)` sent the container to the tree | Each match's copy is handed with its ancestors, snapshotted; the body runs on the snapshots. | [`d29874a`][d29874a] |
| A local `xsl:variable` whose select read the streamed document sent the container to the tree | The select is read off the cursor where the variable is bound. | [`d52121f`][d52121f] |
| `outermost()` of ancestors climbed from streamed matches sent the container to the tree | The call runs over the climbed nodes, taken once each off the matches' views. | [`962f1dc`][962f1dc] |
| `($v, P)/T` and `insert-before(P, n, $v)/T` sent the container to the tree | They stream as the union of the paths they make. | [`ddafb12`][ddafb12] |
| A motionless `xsl:for-each` in a streamed body sent the container to the tree | Over a variable's items it runs as the tree runs it; over the view's nodes, on the view. | [`d244a2e`][d244a2e] |
| A text value template in a streamed `xsl:for-each` or `xsl:iterate` body sent the container to the tree | Its motionless parts are evaluated on each match's view. | [`0b75658`][0b75658] |
| A loop over `(P)[position() = 1 to n]` sent the container to the tree | It streams as a window over P. | [`1c1cb10`][1c1cb10] |
| A validated streamed input whose schema gives an element a default or fixed value ran on the tree | The stream validates the value as the typed copy does: the default for an empty element, the fixed check at the end tag. | [`bbdefe5`][bbdefe5] |
| A validated streamed input whose schema has XSD 1.1 open content ran on the tree | The stream admits what the open-content wildcard admits, interleaved or as a suffix, as the tree does. | [`8b5460b`][8b5460b] |
| A validated streamed input whose schema defaults an attribute in a namespace ran on the tree | The stream adds it with the in-scope prefix, or the XSD 1.1 fixup declaration, as the typed copy does. | [`b9c352c`][b9c352c] |
| A validated streamed input whose schema has `xs:key`, `xs:unique` or `xs:keyref` ran on the tree | The constraints are evaluated as the cursor reads, keeping only their key tables, with the tree's verdicts and messages. | [`c31fc14`][c31fc14] |
| A validated streamed source with a DOCTYPE ran on the tree | It is read as the typed copy reads it, without the DTD. | [`c36f667`][c36f667] |
| A validated streamed input could fail where the tree validates, on a content model nesting bounded `maxOccurs` | Such a schema is validated on the tree, decided before reading; others stream as before. | [`182fdef`][182fdef] |
| A validated streamed principal input dropped the ignorable whitespace of an element with `xml:space="preserve"` | It is kept, as the typed copy keeps it. | [`65d7c6f`][65d7c6f] |
| An `xsl:try` under `Output` whose body was one large element held that element as a tree to the try's end | The element is built in place where the try writes, its content written provisionally as made. | [`895b6a4`][895b6a4] |
| An `xsl:try` that was the first output under `Output`, with no `xsl:output` method, held its output to its end | The method is chosen provisionally from what the try writes; a caught error leaves it to the catch. | [`59b58c1`][59b58c1] |
| An `xsl:try` right after text in its element under `Output` held its output to its end | The text is left unwritten under the try's checkpoint, merged with as in tree mode, and restored on a rollback. | [`a2b8f9f`][a2b8f9f] |
| An `xsl:try` under `Output` whose body began with attributes held its output to its end | The attributes go on to the empty element it writes to, only its start tag held; a caught error puts the element back. | [`e2ccec2`][e2ccec2] |
| An `xsl:try` in a document node's streamed content (`xsl:copy` of `/`, the built-in document rule) under `Output` held its output to its end | It writes provisionally like a try in an element. | [`4ab5460`][4ab5460] |
| Every element of a streamed identity got a view even when no rule could match it | The built-in rule runs on the parse's record: identity 100 MB 1.52× → 1.27× the tree's CPU. | [`1a0ee45`][1a0ee45] |
| A streamed apply op's feed and a children filter were made per element | Both are made once per run: two-instruction identity 88 → 76 allocations per record. | [`40d9b34`][40d9b34] |
| A streamed leaf a rule matched got one runtime for matching and another for its copy | It gets one: 76 → 64 allocations per record. | [`a41d41d`][a41d41d] |
| A matched streamed node's runtime was copied twice, for its focus and its selection | Once: 64 → 52 allocations per record (the tree's 61), two-instruction identity 1.58× → 1.39× CPU. | [`f34dfa2`][f34dfa2] |
| An `xsl:try` rolled back under `Output` before the sink began could panic, or lose whitespace written ahead of the mark | A rollback puts back the items of the result's top and document level written after the mark. | [`a7befda`][a7befda] |
| A nested `xsl:try` rolled back on an element an outer try built in place under `Output` could miss XTDE0410 for a later attribute | The rollback keeps the child written before its mark. | [`7ff197b`][7ff197b] |
| A streamed expression calling a recursive or non-inlinable stylesheet function sent the container to the tree | The call runs on values, or on each match's grounded copy, when its body allows. | [`19593ed`][19593ed] |
| `[A, B]?*/T` over a streamed path sent the container to the tree | It streams as `(A, B)/T` when no member is a path of the stream (that is XTSE3430, §19.8.8). | [`ae36f0b`][ae36f0b] |
| A simple map over streamed text, comment or PI nodes sent the container to the tree | R runs on each kept leaf, as a path's tail over leaves does. | [`94181b7`][94181b7] |
| `replace()` over the streamed document's text sent the container to the tree | It is read off the stream as `matches()` is. | [`6ee4b2e`][6ee4b2e] |
| `($v \| P)/T`, a path over a union of a variable and a streamed path, sent the container to the tree | It is read as `($v union ())/T union P/T`, which streams. | [`95944f3`][95944f3] |
| `P ! (* union $v)` and the other set operators of a match's nodes and a variable sent the container to the tree | They run per match on the grounded copy, which orders as its original. | [`c665e0d`][c665e0d] |
| A for, some or every over values of a streamed path whose return used `\|\|` or an xs constructor sent the container to the tree | A return reading only its variables is evaluated over the values. | [`9f41f98`][9f41f98] |
| `(if ($c) then A else B) ! R` over streamed paths sent the container to the tree | The choice is lifted over the map; only the chosen branch streams. | [`e8246dc`][e8246dc] |
| A separator, name or namespace value template of `xsl:value-of`, `xsl:attribute` or `xsl:element` reading the stream sent the container to the tree | That part streams as a literal result element's value template does. | [`4ac277f`][4ac277f] |
| `generate-id()` of the context element in a streamed rule or loop sent the container to the tree | It reads the view's origin, the tree's id for the node. | [`4f9aea4`][4f9aea4] |
| A rule pattern whose predicate reads `accumulator-before` sent the streamed apply to the tree | The predicate reads the observer's value at the start tag. | [`2f6bca5`][2f6bca5] |
| A streamed path ending in a sequence of value paths, `P/(@a/string(), c/string())`, sent the container to the tree | The tail is known to make values and runs per match on its copy. | [`afadaf0`][afadaf0] |
| The range operator over a streamed operand, `1 to count(.//*)`, sent the container to the tree | The split takes it as it takes arithmetic. | [`fcf384f`][fcf384f] |
| A streamed loop body using `is`, `union`, `intersect` or `except` on its match sent the container to the tree | The body runs on a grounded copy, which has the original's identity and order. | [`a4162b6`][a4162b6] |
| `xsl:apply-templates` over `(P, $v)`, `insert-before(P, n, $v)` or `if ($c) then () else P` sent the container to the tree | The stream's matches are dispatched as read, the other items as the tree dispatches them. | [`b75f94e`][b75f94e] |
| `xsl:copy` with validation strict or lax, or a type, in a streamed container sent it to the tree, even for an element | It streams when the copied item is never an attribute, read from the select or the enclosing loop. | [`aa91721`][aa91721] |
| An `xsl:global-context-item` sent a streamable mode's principal input to the tree | With no type it reads nothing of the input, so the input streams. | [`904f322`][904f322] |
| `xsl:comment` or `xsl:processing-instruction` with a select reading the stream sent the container to the tree | The select is split as `xsl:sequence`'s and read off the cursor. | [`595bcf2`][595bcf2] |
| Streamed `Output` looked to grow its heap with output size (300,000 elements, 90 MB peak) | Its live heap is flat in output size; the growth was `1 to N`'s integers, held in tree mode too. A test pins it. | [`ac8bb00`][ac8bb00] |
| A document the conformance harness preloads as validated ran on the tree when read with the default validation | It streams, repeating the harness's validation as it reads. | [`e086524`][e086524] |
| `min(.)` or `max(.)` in a template rule below the document node ran on the tree | They stream, folded once over the context's values. | [`9643910`][9643910] |
| Tree mode too: under `TransformReader` in a streamable initial mode, a global read the input as its context item | The global context item is absent for a streamed input (XSLT 3.0 §2.3.2), streamed or on the tree fallback: XPDY0002. | [`6cc2247`][6cc2247] |
| Tree mode too: an `accumulator-after#1` item made on a `TransformReader` input kept its focus | §10.3.6 saves an absent focus on a streamed input, streamed or on the tree fallback: its call raises XTDE3350. | [`be09e99`][be09e99] |
| Tree mode too: an accumulator whose walk failed anywhere failed every read of it | A read fails only when its value depends on the failed computation (§18.2.5); values set before it stand. A cycle fails every read (below). | [`b90d54e`][b90d54e] |
| Tree mode too: a merge made its trees in source order, so an unstreamed source after a streamed one ran on the tree | Both modes collect unstreamed sources first (order across trees is implementation-dependent, XPath 3.1 §2.4.1). | [`df063a5`][df063a5] |
| Tree mode too: `document-uri()` of a source document stripped by `xsl:strip-space` returned the empty sequence | The stripped copy keeps the document's URI (F&O §2.6). | [`2e860a2`][2e860a2] |
| Tree mode too: a circular accumulator read at the document node gave its initial value (`error-3410a`) | Every read of it raises XTDE3400 (§18.2.6); a rule reading the value it computes is not streamed. | [`3916423`][3916423] |
| A streamed rule whose body read its element twice (two fork prongs, a copy beside a read) sent the container to the tree | The rule runs on a grounded copy of its element, at most the tree's memory. | [`5663391`][5663391] |
| `base-uri()`, `document-uri()` or `. instance of document-node()` at a streamed document node sent the container to the tree | The stand-in has the document's URIs from the start. | [`8e3270a`][8e3270a] |
| A streamed map constructor whose entries each read the document sent the container to the tree | Each entry is read on its own tee cursor; the first error in entry order is the map's. | [`fb356ca`][fb356ca] |
| An `xsl:fork` whose prongs each read the document, inside `xsl:try`, sent the container to the tree | The first error in prong order is the try's; a failed prong reads on, so parse errors stay the document's. | [`cca65c4`][cca65c4] |
| A streamed rule calling a global `accumulator-after#1` through a variable ran on the tree | The call reads no focus (§2.3.2, §10.3.6), so it is motionless; a supplied parameter declines streaming. | [`844cd8f`][844cd8f] |
| `unparsed-entity-uri()` in a streamed rule on a grounded copy answered "" | The copy is given the document's DTD. | [`6b07dab`][6b07dab] |
| `unparsed-entity-uri()` in a streamed rule for `/` before its consumer sent the input to the tree | The run moves to the document element first, where the DOCTYPE is known. | [`13f330b`][13f330b] |
| A streamed call on a function whose body climbs (recursion on `$n/..`, `parent::X` tests) ran on the tree | Bodies are judged per value kind; ancestors' copies answer names, attributes and existence. | [`d2a3158`][d2a3158] |
| `fn:xml-to-json` of a streamed document node or element ran on the tree | It is written from the parse events as the tree writes it; invalid input goes to the tree for its error. | [`5ed3b7d`][5ed3b7d], [`eb66e23`][eb66e23] |
| A streamed `filter(P, f#1)` or `outermost(copy-of(P))` ran on the tree | Named function references are read as calls; outermost or innermost of copies are the copies. | [`9f02ee9`][9f02ee9] |
| A streamed call on a function whose body has instructions (apply-templates, grouping, literal elements) ran on the tree | The body is planned and run inside the caller's plan. | [`0489759`][0489759] |
| A streamed body calling a stylesheet function on a variable's copies sent the container to the tree | The call reads nothing of the stream and is motionless. | [`c68e9b4`][c68e9b4] |
| A streamed call passing a path to an atomic parameter of a function with node parameters ran on the tree | The argument is read off the stream as values. | [`8a4f213`][8a4f213] |
| A streamed function recursing on the current group ran on the tree | Each recursive call site gets a call frame, a plan of its own run in the caller's stream. | [`0dfff09`][0dfff09] |
| A streamed call passing a path to a one-node parameter, or recursing as `$n!*!f(.)`, ran on the tree | It runs as a loop over the matches with the tree's cardinality check. | [`d7f3a71`][d7f3a71] |
| A streamed call whose value is copied gave up at the document element | The element's grounded copy is held under the tree's cap, as the tree holds it and the copy. | [`9354f1d`][9354f1d], [`63dd3ad`][63dd3ad], [`328460b`][328460b] |
| A streamed for-each over a stylesheet call on a path ran on the tree | The call runs on the matches' grounded copies and the loop over its value. | [`c942506`][c942506] |
| `copy-of(.)`, `snapshot(.)` or `value-of` of `.` at a streamed document node sent the container to the tree | The copy is written from the parse events, or built once; the string value is read off the text. | [`29cc3e9`][29cc3e9] |
| `xsl:copy` of a streamed document node sent the container to the tree unless its body was one apply-templates | The stand-in takes the DOCTYPE and base URI when the parse meets the root. | [`b9cbc6d`][b9cbc6d] |
| A copy-of or sequence of a choice of paths, in a rule that may match the document element, was refused | The condition is evaluated first and the chosen branch's matches copied as read. | [`932bb98`][932bb98] |
| A streamed rule for text, comment or PI nodes returning atomic values with `xsl:sequence` was refused | A select that can return only atomic values hands on no copy of the leaf. | [`ead3f81`][ead3f81] |
| A streamed `xsl:variable` with an atomic `as` type selecting its context was refused as a grounded copy | The declared type atomizes the value, which is read off the cursor. | [`2cfd986`][2cfd986] |
| apply-templates over a streamed path that may select text, comment or PI nodes sent the container to the tree | Each such match is dispatched on its complete copy, in place. | [`9a7551b`][9a7551b] |
| Copies of a streamed document made from the parse's events lost the DTD's ID attributes | They keep is-id and is-idrefs, and all typing where the tree's copy does. | [`f05fd39`][f05fd39] |
| `unparsed-entity-uri()` of a streamed document node sent the container to the tree | It reads the DOCTYPE the stand-in takes at the root, once the document is read. | [`ff1f3ac`][ff1f3ac] |
| A streamed `xsl:comment` or `xsl:processing-instruction` selecting elements in a loop that may match the root was refused | Their select is simple content, read as values. | [`302ddf2`][302ddf2] |
| `xsl:value-of` of text nodes beside a streamed path of elements was refused | Only the path is read as values; the text nodes merge as in the tree. | [`64ae463`][64ae463] |
| apply-templates over `copy-of()` or `P ! snapshot(.)` of a streamed node sent the container to the tree | The copies are read off the cursor and the templates applied to them on the tree. | [`8a68af2`][8a68af2] |
| A group-by over members that may be the root's children was refused by the memory rule | They are held as one fragment of copies made from the events, when the body cannot tell a copy. | [`a6f13b1`][a6f13b1] |
| An accumulator rule reading another accumulator, or itself, sent the document to the tree | The read accumulator is stepped first at the node, in the tree's error order; mutual reads stay refused. | [`87cea8b`][87cea8b] |
| `xsl:try` or `xsl:message` reading an accumulator sent the container to the tree | Under §18.2.5's errors both modes raise at the same read, so they stream. | [`827e309`][827e309], [`b90d54e`][b90d54e] |
| `xsl:apply-templates` inside a streamed `xsl:try` sent the mode to the tree | It streams when no rule it reaches can fail but the built-ins, whose first error both modes meet at one node. | [`1ac69c5`][1ac69c5] |
| A merge action asking for `last()` sent the merge to the tree | The streamed merge forms every group first, holding only the items' snapshots. | [`54dbc38`][54dbc38] |
| `xsl:message` in a merge action, or boolean or global-map merge keys, sent the merge to the tree | Admitted: messages are taken back on a re-run, key classes read from the expression. | [`54dbc38`][54dbc38] |
| A merge whose first streamed source's `for-each-source` failed re-ran on the tree | The error, the tree's first, is returned at once. | [`54dbc38`][54dbc38] |
| A streamed merge source with `use-accumulators` sent the merge to the tree | An observer per document gives each item's snapshot its values; a failing walk re-runs on the tree. | [`cb7bc56`][cb7bc56] |
| `copy-accumulators="no"` on a streamed `xsl:copy-of` sent it to the tree | It is the default and streams as the attribute's absence does. | [`3507414`][3507414] |
| An accumulator rule reading `base-uri(/)` sent the document to the tree | The parse's document node has the document's base URI from the start, so the rule streams. | [`b3cf350`][b3cf350] |
| `xsl:copy-of copy-accumulators="yes"` of streamed nodes sent the container to the tree | The matches' values are recorded while they are grounded and answer for the copies. | [`816d8ec`][816d8ec] |
| `intersect` or `except` of two climbing paths (`A/ancestor::* intersect B/ancestor::*`) sent the container to the tree | Each operand's climbed nodes are recorded once with a branch mask; the operator applies at the end. | [`fd303aa`][fd303aa] |
| A streamed `xsl:for-each-group` over `subsequence(P, a, b)` sent the container to the tree | The population walk counts P's matches and passes over those outside the window. | [`0bd7034`][0bd7034] |
| `xsl:for-each-group` over attributes, text nodes or `outermost(P)` of a stream sent the container to the tree | The population is held as collected, under the memory cap, and grouped by the tree's code. | [`de0784c`][de0784c] |
| A grouping over `(//x)[...]` with a motionless body, or text value templates in its body, sent the container to the tree | The population is walked as the split walks it, nested members included. | [`960370b`][960370b] |
| A grouping over attributes, text or `node()` in a rule for the document element sent it to the tree | The population is a pruned copy built from the events; the body runs off the stream. | [`5bbaba2`][5bbaba2] |
| A group-by in `xsl:fork` whose body applied templates to or sequenced `current-group()` sent the run to the tree | The members are held as copies under their ancestors' copies, from the events. | [`b79ca81`][b79ca81] |
| A streamed for-each over attributes or text nodes whose body read `data(.)` or `copy-of(.)` sent the container to the tree | The body is planned at a leaf's focus and runs on each complete item. | [`e73ae6b`][e73ae6b] |
| A for-each over a streamed element's attributes whose body read `.` sent the container to the tree | Its body is checked at a leaf's focus: each attribute is complete. | [`1d406ab`][1d406ab] |
| A streamed `xsl:iterate` over attributes or values whose body read `.` sent the container to the tree | Its body is checked at a leaf's focus, as a for-each's. | [`65ebe0e`][65ebe0e] |
| `xsl:apply-templates` over `copy-of(P)` or `P/copy-of(.)` of a streamed document sent the container to the tree | The copies are collected as read and dispatched as the tree does, with its sort, parameters and `last()`. | [`a52bc6b`][a52bc6b] |
| A streamed for-each over `P/copy-of(.)` whose matches may nest sent the container to the tree | It runs over the copies of a population, one grounded match at a time. | [`2536f98`][2536f98] |
| A streamed for-each over copies whose body read `last()` raised XPDY0002 where the tree gives the count | Such a loop runs on the tree; a body that cannot read `last()` still streams. | [`d2c48ef`][d2c48ef] |
| `xsl:value-of` over the context's own subtree, such as `descendant-or-self::*`, in a streamed rule sent the container to the tree | The context is grounded once and the values made from its copy. | [`d5178af`][d5178af] |
| A motionless `xsl:iterate` over a node's ancestors, or a motionless for-each with `xsl:sort` over a variable, sent the container to the tree | Both are checked as a motionless for-each is. | [`a0e226c`][a0e226c] |
| A streamed rule using `key()` over a global's document, or `current()` in its pattern, sent the container to the tree | Both are read off the candidate's view. | [`91af2f9`][91af2f9] |
| `current()` in a streamed rule's instruction, or apply-templates `select="@*"` on the document node, sent the stylesheet to the tree | `current()` is the instruction's item; `@*` of the document node is empty. | [`e4ecbd7`][e4ecbd7] |
| A streamed rule matching text nodes too that copied `@* except @a` sent the container to the tree | On a text node both operands are empty, so it is motionless. | [`0a73669`][0a73669] |
| A streamed rule applying templates to copies of its attributes, a map of its values or a variable's copies sent the container to the tree | The items go to the tree's rules off the cursor. | [`b50e009`][b50e009] |
| `has-children()` in a streamed rule's motionless instruction or pattern sent the stylesheet to the tree | The dispatch peeks past the element's start tag and records the answer on its view. | [`a386ca5`][a386ca5] |
| A streamed for-each or apply-templates over `if ($c) then P else Q`, or `$v union P`, sent the container to the tree | The chosen branch's own split is read; `$v`'s nodes are merged into the matches by order. | [`44fc1c0`][44fc1c0] |
| `xsl:apply-templates` over a value computed from the stream, such as `if (P) then 0 else 1`, sent the container to the tree | The values are read off the stream and dispatched as the tree does. | [`1f91b5d`][1f91b5d] |
| A streamed for-each or apply-templates over `[$v, P/copy-of()]?*` sent the container to the tree | The array's members are read as a sequence population. | [`c92d154`][c92d154] |
| An apply-templates in a streamed loop body over snapshots or copies required a streamable mode, sending the container to the tree | It runs the tree's dispatch on the copy, in any mode. | [`d26ad2e`][d26ad2e] |
| A streamed `xsl:call-template` passing a child of the context, atomized by the callee, sent the container to the tree | The parameter is read off the cursor as `xsl:next-iteration`'s is. | [`0f78020`][0f78020] |
| `outermost(P)/T` over a document where P's matches nest sent the container to the tree | The outermost matches do not nest: T runs on each one's copy. | [`bf94023`][bf94023] |
| A streamed rule applying templates to `doc()` of another document sent the container to the tree | That document's nodes go to the tree's rules; the streamed document itself is declined at run time. | [`6feaadd`][6feaadd] |
| `xsl:apply-templates` over a filter of copies, `(copy-of(P))[pred]`, sent the container to the tree | The copies are collected, filtered and dispatched as the tree does. | [`35d5ad7`][35d5ad7] |
| A window over a streamed path mapped to a name, `subsequence(P, 1, 4) ! name(..)`, sent the container to the tree | It is read as the window over the mapped path, which streams. | [`40bb829`][40bb829] |
| A streamed apply-templates over copies whose rules read accumulator values sent the stylesheet to the tree | The observer keeps the values of the nodes read while the copies are made, as the tree's tables would. | [`1b4d62e`][1b4d62e] |
| A streamed container reading a non-streamable accumulator ran on the tree | It streams and raises XTDE3362 as the tree does. | [`065b5b5`][065b5b5] |
| `root()` of a copied node, or `fold-right()` over values of the stream, left the streamed container with no verdict | The analysis models them for a grounded operand, by §19.8.9.18 and §19.8.9.9. | [`ef9e55b`][ef9e55b] |
| `xsl:copy` of a streamed attribute with a `type` sent the container to the tree | The attribute's name and value are assessed against the type as a detached copy, as the tree's result. | [`b7e79f7`][b7e79f7] |
| A for-each over a union of two same-depth paths with a `\|\|` body ran on the tree | The trusting walk reads the union as striding, since such nodes cannot nest; the refusal is unchanged. | [`4f2cc85`][4f2cc85] |
| `copy-of()` of the attributes a streamed path climbs to sent the container to the tree | A copy of an attribute or leaf is taken from its view, as complete as the tree's node. | [`adc813c`][adc813c] |
| `xsl:perform-sort` over copies or values of the streamed document sent the streamed rule to the tree | Its select is read off the stream and the sort runs on what it returned. | [`157f717`][157f717] |
| `xsl:apply-templates` building an `xsl:map` held in a typed variable sent the streamed container to the tree | The map's content is maps or an error alike in both modes, so the apply streams. | [`167a347`][167a347] |
| A stylesheet comparing streamed nodes with `doc()` nodes of the same document could stream a different answer from the tree's | It runs on the tree when a load may reach the streamed URI; a literal other URI still streams. | [`60cf53e`][60cf53e], [`ddcaebf`][ddcaebf] |
| A streamed container in a stylesheet that uses a package ran on the tree | A plain `xsl:use-package` leaves the using module's own declarations trusted, so it streams. | [`381a787`][381a787] |
| A streamable mode whose rules sit in imported or included modules ran on the tree | Its rules are verified across every module, by import precedence, and stream. | [`954a278`][954a278] |
| A streamed container in a package that overrides a used package's function ran on the tree | The overriding declarations are the using package's own and are trusted. | [`0175e4b`][0175e4b] |
| A streamed container calling a function of a used package ran on the tree | The function is verified in its own package and called as itself. | [`8d10f9e`][8d10f9e] |
| A streamed call on a stylesheet function with an instruction body over atomic arguments ran on the tree | The call is given values only and runs as itself. | [`d4feb67`][d4feb67] |
| A streamed call on a used package's absorbing function, such as the spec's xml-to-json package, ran on the tree | Its body runs as a call frame in the caller's stream, compiled in the package's state, its modes planned per package. | [`38629c8`][38629c8] |
| `accumulator-after` on an element whose subtree failed an accumulator rule gave a value when streamed | The read re-takes its frame after consuming the element and raises the error (§18.2.5). | [`59e5ee0`][59e5ee0] |
| A mode reading an accumulator whose end rule calls `accumulator-after` was not verified streamable | §19.8.9.1 rules 4 and 5: motionless in an end rule, free-ranging in a start rule. | [`a8b0eeb`][a8b0eeb] |
| A streamed rule on a grounded copy at the top of a principal result ran on the tree | It streams when its body's top makes only new nodes. | [`724617a`][724617a] |
| A streamed container ran on the tree when a used package declared an accumulator of the same name | The package's accumulator is its own and no longer refuses it. | [`d30a7e8`][d30a7e8] |
| A call on a used package's absorbing function through a wrapper still ran on the tree | The wrapper call is rewritten in place, `map:get` is focus-free, values calls are not inlined. | [`0ae8a17`][0ae8a17] |
| A streamed call `f(.)` at an element, on a function with an instruction body, ran on the tree | `.` at an element is the match the inlined body consumes. | [`678c4ef`][678c4ef] |
| A recursive streamed function over `tail(current-group())` ran on the tree | The frame's grouping reads the enclosing group past its first member. | [`4bdcad8`][4bdcad8] |
| A streamed call passing a path to a one-node parameter never streamed | Its loop path is split from a fresh parse; the body reads the parameter as the match's view. | [`0a00b54`][0a00b54] |
| A select calling an instruction-bodied function the inliner refused ran on the tree | It is planned by the split, as before inlining. | [`36ecaac`][36ecaac] |
| `P ! f(.)/@x` over nested matches with a climbing `f` ran on the tree | Atomized, it runs on each match's grounded copy. | [`a788a32`][a788a32] |
| A streamed group key reading a function parameter raised XPST0008 inside an inlined call (`su-absorbing-301`) | The key sees its grouping's own locals. | [`79b94b5`][79b94b5] |
| `tail(P) ! R` streamed ran R on P's first match, raising its error where the tree drops it | The split skips the match before its tail runs. | [`5804464`][5804464] |
| `xsl:value-of` of `(if (c) then A else B) ! R` sent the container to the tree | The choice is hoisted through simple content's `data()`. | [`8fe2846`][8fe2846] |
| A loop over `(P)[position() = L to H]` went to the tree again once filtered copies were read first | A refused filtered-copies reading leaves the window reading free. | [`cccfc14`][cccfc14] |
| A streamed for-each over `copy-of(P)` whose body read `last()` failed XPDY0002 (guard) | A population read off the cursor is checked for `last()` as an unplanned loop is. | [`3208035`][3208035] |
| An apply-templates over `snapshot(P)` whose rules read accumulators failed on the stream | The tree's dispatch over the copies keeps the originals' values, as for `copy-of`. | [`6fc06e9`][6fc06e9] |
| A for-each or apply-templates over `copy-of(//P)` or `snapshot(//P)` sent the container to the tree | Only the last step says whether a match may be a leaf. | [`608935d`][608935d] |
| An `xsl:iterate` or for-each over `P/name()` sent the container to the tree | A loop over an atomic tail of each match is a loop over values. | [`db6f542`][db6f542] |
| `subsequence(P, 1, 2) ! name(ancestor::*[1])` sent the container to the tree | A map climbing inside a function call is a mapped path, read off the matches' views. | [`5ab15c4`][5ab15c4] |
| `id()`, `idref()` or `unparsed-entity-uri()` on a variable's copy of the streamed document sent the container to the tree | A call whose node argument is a variable reads that node's document. | [`0b676e7`][0b676e7] |
| A streamed group's variable copying `current-group()` beside `copy-of(.)` hit the posture guard | A split whose outer calls `current-group()` is refused. | [`36f470e`][36f470e] |
| apply-templates over `$t/*` in a streamed group, with a with-param reading the group, hit the posture guard | A values population takes no with-param. | [`35422d9`][35422d9] |
| A streamed `string(.)` of a typed node gave its typed value's form ("4.5"), not its string value ("4.50") | The split takes `fn:string`'s argument as string values. | [`c2e2af2`][c2e2af2] |
| A streamed copy of the document declared each element's namespaces sorted by prefix, not in source order | `copy-of`'s binding order is used, for the document and a grouping's pruned copies. | [`9315ac0`][9315ac0], [`4f1dad9`][4f1dad9] |
| A streamed `tail(*)` or `tail(/*)` copied at the document node wrote the document element | The root copy skips it, as `tail()` does. | [`5c09c9d`][5c09c9d] |
| A streamed source-document inside another dropped the outer run's accumulator observer, re-running its rules on the tree | The rules' runtime no longer replaces the transform-wide list. | [`23da903`][23da903] |
| Under `Output`, a nested `xsl:try`'s rollback could lose the catch's text when a node reused a taken-back child's slot | The rollback trusts that child only while the sink holds it. | [`76bc90b`][76bc90b] |
| A streamed `tail(P)` over a path cut at a predicate the stream cannot check skipped the cut's first match, not P's: nothing was written | Such a `tail()` is refused; the tree runs it. | [`537885e`][537885e] |
| A `separator="\|"`, or `is` over `doc()` nodes alone, kept a whole stylesheet on the tree | The alias guard follows the parsed expressions as a flow, refusing only where a stream's node may meet a load's. | [`90b29d9`][90b29d9] |
| An `xsl:evaluate` given a stream's node was not counted by the alias guard | It is an identity use of both kinds (§10.4.2). | [`2de588e`][2de588e] |
| `last()` in a streamed rule's for-each over its ancestors sent the mode to the tree | A motionless loop's `last()` is its select's size, known before the body. | [`305b99d`][305b99d] |
| `xsl:next-match` in a streamed rule for text nodes sent the mode to the tree | The next rule runs on the complete text node, as the tree's does. | [`bf58cad`][bf58cad] |
| apply-templates over a variable holding `xsl:copy-of`'s copies sent a streamed mode to the tree | Such a variable holds new trees; the rules run on them. | [`acf9a9c`][acf9a9c] |
| A streamed text-node pattern reading an accumulator sent the mode to the tree | The observer keeps the text node's values for its pattern. | [`9f896b0`][9f896b0] |
| apply-templates over a global tree with a `copy-of(.)` parameter sent a streamed loop to the tree | The parameter is read off the cursor before the tree's rules run. | [`9e7f579`][9e7f579] |
| An `xsl:iterate` comparing carried copies with `current()`, or binding `copy-of()`, went to the tree | Both read the view and the item's copy. | [`acc785d`][acc785d] |
| A streamed path whose `text()` or `node()` step tested its parent's attribute went to the tree | The leaf's predicate reads its ancestors on its copy under theirs. | [`b0894e2`][b0894e2] |
| A streamed for-each over `head(//X)` whose body copied content went to the tree | The body runs on the first match; matches inside it are no items. | [`96b8df3`][96b8df3] |
| A test or predicate calling an inspection `xsl:function` on the stream went to the tree | The call is checked as its body; `[position() = N and P]` reads as `[N][P]`. | [`0e0d2b8`][0e0d2b8] |
| A streamed path testing its outermost ancestor by `ancestor-or-self::*[last()]` went to the tree | The view has every ancestor: positions among them are read on it. | [`af1a9e5`][af1a9e5] |
| A streamed rule filtering tokens of its own string by `last()` went to the tree | The outer has the whole value: `last()` is its count. | [`c4323f5`][c4323f5] |
| apply-templates to an array built of snapshots of the stream went to the tree | The array is made over the collected copies and dispatched. | [`f638162`][f638162] |
| A for-each over climbed ancestors (`function-5017`, `-5018`) ran on the tree | It streams when the body is motionless on their views. | [`256759e`][256759e] |
| `unparsed-entity-uri#1` in a rule for an element ran on the tree | The view's tree has the document's DTD, so it streams. | [`8a9d83d`][8a9d83d] |
| An accumulator rule with a sequence-constructor body (`accumulator-036s`) kept the mode on the tree | It is judged at its matched node, as a select is. | [`a98e534`][a98e534] |
| A used package's streamable `xsl:source-document` (`override-misc-007`) ran on the tree | It is trusted when used through `xsl:accept` alone, and its accumulators are observed. | [`66777b6`][66777b6] |
| A call at the document node on a module function given the document node (`function-5002`) ran on the tree | It is inlined as a call frame. | [`5a0eab3`][5a0eab3] |
| An instruction-body function given `copy-of(.)` in a tail (`function-5011`) ran on the tree | A call given only copies and values streams. | [`4bea4f9`][4bea4f9] |
| A copy of `/doc/chap[2]` at the document node kept a principal input on the tree | A held select with at most one match streams it. | [`eb144dd`][eb144dd] |
| A filter over `tokenize()`'s strings at the document node kept a principal input on the tree | Its predicates read atomic values only, and it streams. | [`8be963f`][8be963f] |
| A rule whose group is read beside its consuming path kept the mode on the tree | The rule runs on a grounded copy of its element. | [`7bec012`][7bec012] |
| A rule with a map of two consuming values kept the mode on the tree | The rule runs on a grounded copy of its element. | [`f6e3637`][f6e3637] |
| A rule on a grounded copy refused accumulators | The copy carries the document's accumulator values. | [`4e56b21`][4e56b21] |
| A copy-of whose tail runs on a copy of the document element kept a principal input on the tree | The element is grounded, as the tree holds it. | [`75c5d3c`][75c5d3c] |
| A leaf's rule returning a stylesheet function of the leaf stayed on the tree | The call is checked through its inlined body. | [`cac3851`][cac3851] |
| `current-group()!copy-of()` over the document element's children stayed on the tree | Each member is copied as its path's tail and the ground let go. | [`16a64ec`][16a64ec] |
| An `xsl:try` whose catch had `exclude-result-prefixes` ran on the tree | The attribute is not an instruction; the try streams. | [`cc11df5`][cc11df5] |
| apply-templates in a variable typed as maps or arrays ran on the tree | No node is an instance of that type: it streams, with the same XTTE. | [`7730dcb`][7730dcb] |
| An accumulator typed as a map or array of atomic values ran on the tree | Such a value holds no node of the parse: it streams. | [`95171aa`][95171aa] |
| `accumulator-before('m')(k)` was refused in a streamed rule | A call on an accumulator's value with atomic arguments reads nothing of the stream. | [`ce41a32`][ce41a32] |
| An accumulator pattern with a `some` or `every` predicate was refused | Quantified predicates over attribute values are decided at the start tag. | [`d02cb54`][d02cb54] |
| A merge of a collected path-keyed source with streamed string keys ran on the tree | It streams when the collected keys are untyped, checked as read. | [`42ce08f`][42ce08f] |
| A `group-by` in `xsl:fork` returning `map{...}` ran on the tree | A map constructor of atomic values hands no member on. | [`5531827`][5531827] |
| `f(snapshot(.))` in a rule for the document element (`sf-unparsed-entity-03`, `-04`) stayed on the tree | The one copy of the match streams, at the tree's memory. | [`a7d0796`][a7d0796] |
| `xsl:perform-sort` over `copy-of(item)` (`si-perform-sort-001`) stayed on the tree | Each item is copied as its tail and the ground let go. | [`20a5eef`][20a5eef] |
| `innermost` or `reverse` over `snapshot(/chapter)`, or `snapshot(/)`, at the document node stayed on the tree | The select runs on the document built off the stream. | [`4f870c3`][4f870c3] |
| A `snapshot(/*)` variable read with `$v/..` (`sf-snapshot-0101b`) stayed on the tree | In its scope the variable is read as the tree it is. | [`609bc45`][609bc45], [`3fbd05e`][3fbd05e] |
| An unclassified function given `copy-of(.)` at `/` (`function-5001`) stayed on the tree | The call is not inlined and runs on the document built. | [`330e96a`][330e96a] |
| A `copy-of(current-group())` parameter refused streaming | It is read from the group's members at the instruction; one group's copies are held. | [`19f2492`][19f2492] |
| A for-each over copies reading `last()` ran on the tree | The copies are collected first, then looped with their count. | [`c6b67df`][c6b67df] |
| A path climbing out of `snapshot(P)` ran on the tree | P's matches are grounded and the snapshots' own ancestors climbed. | [`bedbb23`][bedbb23] |
| An instruction-body function given the match in a tail ran on the tree | It runs on the match's grounded copy when its body sets no focus and reads only down. | [`84c0f1f`][84c0f1f] |
| `outermost(doc/f(x))`, and a loop over a tail on grounded copies, ran on the tree | The path is the document element, the tail run on its grounded copy. | [`d32fa1d`][d32fa1d] |
| `$f(snapshot(.))`, and `snapshot(@a)` applied in a tree mode, ran on the tree | A dynamic call given copies streams; `snapshot(@a)` is applied as `copy-of(@a)` is. | [`cc32228`][cc32228] |
| `xml-to-json()` of a path whose matches may nest stayed on the tree | The writer matches below the first match; a second raises the tree's XPTY0004. | [`eb315d3`][eb315d3] |
| A built-in copy of a comment, PI or text node at the top of a streamed principal result was refused | The document is built as read and `Result.Nodes` holds its nodes. | [`5cd3316`][5cd3316] |
| `max()` or `min()` below the document node over schema-typed elements stayed on the tree | A schema list of one atomic item type makes the fold exact. | [`6930e60`][6930e60] |
| A `group-by` body returning arrays or maps of atomic values kept its members on the tree | Such a body hands no member on; members are copied as events. | [`c5c4fa9`][c5c4fa9] |
| `xsl:source-document` with a `#id` fragment never streamed | The cursor is read to the element, whose copy is the body's context; absent, the tree runs. | [`11c4ba5`][11c4ba5] |
| `evaluate-046`'s preprocessor stayed on the tree | Atomic results in the alias flow, an accumulator's own value, except-patterns, a motionless `xsl:evaluate`. | [`69101da`][69101da], [`13ecb6e`][13ecb6e], [`615676a`][615676a], [`95338d5`][95338d5] |
| `system-property('xsl:supports-streaming')` answered `no` | It answers `yes`, and `fn:transform` requested-properties match it; `streaming-fallback` is not claimed. | [`de7aab2`][de7aab2] |
| `\|\|` was unmodelled in the §19.8 analysis, so si-fork-902 and -952 compiled | Modelled per §19.8.8 (`A \|\| A`), withheld in a crawling focus; both now get XTSE3430. | [`1f6576d`][1f6576d] |
| A streamable function whose body was not one `xsl:sequence` was never checked | Its sequence constructor is assessed (§19.8.5); su-absorbing-205 gets XTSE3430. All 151 W3C XTSE3430 cases now refuse. | [`6e960b7`][6e960b7] |
| The analysis took a simple map's sweep from its right operand only | The wider of both operands' sweeps (§19.8.8.7). | [`973790f`][973790f] |
| Absorbing `a ! @x` was charged a read of the stream | A simple map has its right operand's type (§19.8.1, §19.8.8.7). | [`03be6e6`][03be6e6] |
| `xsl:where-populated`, `xsl:on-empty`, `xsl:on-non-empty` were unmodelled in the §19.8 analysis | §19.8.4.42, .31 and .32 rules; their containers can stream. | [`43ddd9d`][43ddd9d] |
| 38 §19.8.9 proforma rows were missing from the analysis (`concat#2`, `trace`, `tokenize#1` …) | Added; constructs using them get verdicts. | [`785a0c6`][785a0c6] |
| `map:`, `array:` and `math:` calls were unknown to the §19.8 analysis | Their §19.8.9 rows added. | [`4fdf376`][4fdf376] |
| The match pattern `.` was unknown to the §19.8 analysis | Motionless (§19.8.10). | [`b54aee5`][b54aee5] |
| A rule pattern calling `accumulator-before` left its streamable mode unverified | Verified when the accumulator is (§19.8.9.2). | [`7e2bec4`][7e2bec4] |
| Streamed loops were refused after ~1M records of flat state | The node and byte budgets counted every temporary ever built. A loop item that writes no output now hands back what it dropped. | [`294b965`][294b965] |
| `fn:copy-of` and `fn:snapshot` copies were not charged to the result-node budget | Unlike `xsl:copy-of`. They are charged before the copy, so a transform making over 2M such copies is now refused. | [`294b965`][294b965] |


### Fixed

| Change | Problem → solution | Commit |
|---|---|---|
| XPath constant folding gave each fold a fresh 5M-item budget and ignored the compile deadline: 241 bytes allocated 2.98 GB | One shared 4,096-item fold budget per compile, bound to its Context; unequal literal ranges are not folded. | [`e73a30b`][e73a30b] |
| `ParseOptions.MaxNodes` charged only start tags, so a document of PIs or comments passed any bound | Text that is not all white space, comments and PIs now count one node each. | [`9e9a6f9`][9e9a6f9], [`089c346`][089c346] |
| k ATTLIST defaults on one tag cost k×k comparisons, reachable from `fn:parse-xml` | A tag whose declarations times attributes pass 64 indexes its attributes by name. | [`43ca7b2`][43ca7b2] |
| Copying one long text node into many elements used up the text store's blocks and panicked | One string reuses its block; past half the block space values are copied and packed. | [`ad032d1`][ad032d1] |
| A tree whose text needed over 1<<17 store blocks (~1 GiB copied) panicked the host | Parse, XPath, XQuery, XSLT and XSD entry points return `xdm.ErrResourceLimit` and no result. | [`91f30c9`][91f30c9] |
| Href-less `xi:include xpointer` copies doubled per level, with no fetch or depth charged | One pass's inclusions count what they copy in against `Parse.MaxNodes`. | [`eb4d0ae`][eb4d0ae] |
| A date plus `xs:yearMonthDuration` built a year the parser refuses, which compared wrongly and hung `format-date` `[W]` | It raises FODT0001; the ISO week-year search tests ±1 only. | [`734f53c`][734f53c], [`e720aa3`][e720aa3] |
| `format-number` found an exponent picture's exponent one power of ten per step, quadratic in digits | It is estimated from the digit counts. | [`eb5b289`][eb5b289] |
| Irregular grouping in `format-integer` and `format-number` scanned every separator per digit, quadratic in the picture | Separators are indexed by position. | [`78d92a8`][78d92a8] |
| The regex cache gate counted a character class as one instruction, so a class-heavy 1 KB key pinned megabytes | Each class's rune table counts, ten runes to an instruction. | [`a97b803`][a97b803] |
| Backreference width analysis rescanned the pattern per reference and per nesting level | It runs once per group, capped at 4M steps (`ErrResourceLimit`). | [`ba64164`][ba64164] |
| A nested function body or `xsl:evaluate` in a `for` reset the shared item count, so `MaxItems` never bound it | `Compiled.Eval` restores the enclosing item and byte count on return. | [`ab7d256`][ab7d256] |
| A `cache="yes"` or `new-each-time="no"` function memo held its results with no total against `MaxItems` | The memo totals what it holds and raises XPDY0130 past the bound. | [`3926b3e`][3926b3e] |
| Each `xsl:key` index, one per temporary tree, got a fresh `MaxItems` | Key entries are totalled across every index the transform holds. | [`d06d0f3`][d06d0f3] |
| A static `fn:transform` of its own stylesheet overflowed the stack: the static phase had no depth bound | It takes `DefaultMaxDepth` and the caller's depth (`ErrResourceLimit`). | [`a97ca6a`][a97ca6a] |
| A package using itself, directly or through another, overflowed the stack through `xsl:use-package` | Packages on the path are tracked; a revisit is XTSE3005. | [`dce8830`][dce8830] |
| An attribute value, comment, PI or nested element inside an html `<script>` or `<style>` could write `</` and close it | Every raw write there is held to the text rule: SERE0007. | [`f09487b`][f09487b] |
| A computed `xsl:result-document/@standalone` was written raw into the XML declaration, so `"?>` injected markup | Values other than yes, no and omit raise SEPM0016. | [`877b3af`][877b3af] |
| Result-document hrefs were symlink-resolved on the host with no file access granted | Hrefs compare lexically; a read is also recorded under its URI, so XTDE1500 holds. | [`e7f1116`][e7f1116] |
| A streamed `xsl:merge` held every `for-each-source` document open at once, two descriptors and a goroutine each | A merge of more than 64 inputs runs on the tree. | [`e621bf7`][e621bf7] |
| Streamed grounding copied every ancestor's attributes per record: N records × S attributes | Batches wait for 4× their spine copy; past 16M copies, over 64 per record is refused. | [`f90e97d`][f90e97d] |
| `xsi:nil` on a nillable complex-typed element skipped its attribute, ID, abstract-type and assertion checks | A nilled element gets every check except content, tree and stream. | [`50492e0`][50492e0] |
| Under XSD 1.1, `xsi:type` naming the declared type escaped an `xs:error` or tighter type alternative | `xsi:type` must derive from the selected type, tree and stream. | [`9aed5a3`][9aed5a3] |
| A key or unique duplicate in a nested scope below a lax or `xsi:type` wildcard child went unreported | The child's identity tables now go up, tree and stream. | [`7fdea1e`][7fdea1e] |
| The streamed identity check buffered text in every open complex-typed field, depth × text bytes | Complex fields keep no text and fail at their end tag. | [`9a34abe`][9a34abe] |
| A dense XSD content model was compiled in full before the UPA width gate refused it | The compile stops at the first follow set wider than `maxUPAStateWidth`. | [`2a27fc6`][2a27fc6] |
| Particle Valid (Restriction) flattened a group-ref DAG to one member per path | Flattened lengths are memoised per group and refused past the position budget. | [`df96ba9`][df96ba9] |
| XSD 1.1 assertions ignored the `ValidateContext` deadline and returned a verdict | The assertion carries the run's context; a cut-short evaluation returns `ctx.Err()`. | [`f065ab2`][f065ab2] |
| Each union member reference kept its own `{namespace}local` copy in the never-evicted global type tables | Duplicate members are dropped and one string is shared per name. | [`9e39b51`][9e39b51] |
| Each distinct XSD type kept its whole `{namespace}local` name in the global type tables: 49 MB for 10,000 types under a 4 KB namespace | The namespace is interned once per table: 0.64 MB. | [`6e51253`][6e51253] |
| `WithInstanceLocations` statted instance-named paths before the resolver saw them | Its dedup asks the filesystem only inside the schema's own directories. | [`aeae707`][aeae707] |
| `xsdnet.HTTPResolver` passed a `file:` location in a remote schema to an unrooted `FileResolver`, so a remote author read local files | It is refused before `Files` is asked. | [`a85f40a`][a85f40a] |
| `c14n.FromXPath` and `FromXPathFilter` ran XML-DSig XPath 1.0 transforms by 2.0 rules, so `"10" < "9"` was true | They evaluate in XPath 1.0 compatibility mode. | [`0c70a04`][0c70a04] |
| `dtd.Validate` ignored undeclared attributes and matched names by local part, missing `xml:lang` | Undeclared attributes are reported and names matched as written. | [`915eb63`][915eb63] |
| RELAX NG include and externalRef fan-out had no total bound, so a few tiny schemas cost F^depth fetches | One compilation makes at most 10,000 fetches, then `ErrResourceLimit`. | [`cfc090e`][cfc090e] |
| A module imported on several paths was compiled once per path, so imports doubling per level cost 2^N | One compilation resolves at most 10,000 include/import occurrences, then `ErrResourceLimit`. | [`77c3c5d`][77c3c5d] |
| XQuery `validate lax` with no imported schema wrote `xsi:type` annotations into the caller's tree | It annotates and returns the §3.21 copy; the input is left as it was. | [`f46ec7b`][f46ec7b] |
| Rooted file resolvers statted a `//host/share` or `file:////host/...` path before containment, reaching the host over SMB on Windows | It is refused first, unless a root is on that share. | [`60fed8a`][60fed8a] |
| A panic while `xslt.FileResolver` parsed a file left other callers waiting on it blocked forever | The in-flight entry is released by a defer and its waiters get an error. | [`60fed8a`][60fed8a] |
| A package used on several paths was read and compiled once per path, so a layered `xsl:use-package` DAG cost 2^N | Its read is shared per `Compile`; each read and compile counts against the 10,000 module visits. | [`4c5c3ee`][4c5c3ee] |
| `array:flatten` over an array doubled with `[$a,$a]` walked 2^k leaves with no charge or cancellation | `xdm.Flatten` refuses past 1<<24 items inside arrays; `array:flatten` and `fn:data` charge their growth to `MaxItems`. | [`9c19935`][9c19935], [`0d82e86`][0d82e86] |
| `Node.AddNamespace` copied and interned the whole frame per binding, so an element of K declarations kept K(K+1)/2 bindings | A frame of 8+ bindings that ends its tree is extended in place; assertion, alternative and copy scopes build their frame in one pass. | [`93f91ae`][93f91ae], [`952e6d2`][952e6d2], [`9999789`][9999789] |
| Href-less XInclude xpointers were charged by node count only (9 KB copied 65 MB of text), and each walked the tree from its root | One pass's copied values share `MaxBytes`, and its local searches share a visit budget of `MaxNodes`. | [`ed500f2`][ed500f2] |
| DTD and entity scanners searched for `;` again at every `&` or `%`, so a run without one was quadratic (200k `%`: 2.5 s) | Each `;` is found once per scan, in `xdm` and in `dtd.Load`. | [`e9e1442`][e9e1442], [`b9aebdf`][b9aebdf] |
| `dtd.Load`: parameter-entity references that expanded to nothing were free, so a doubling ladder over an empty entity ran without bound | Each reference is charged at least its own length against `MaxEntityBytes`. | [`b9aebdf`][b9aebdf] |
| `dtd.Validate` accepted a root other than the element the DOCTYPE names, skipping that element's attributes and model | It checks XML 1.0 VC Root Element Type. | [`a543265`][a543265] |
| `dtd.Validate` built a dense content model's n²/2 follow edges in full before refusing it, for every element: 36 s and 4 GB per element | The DTD matcher bounds its stored follow pieces at 2²⁰, so a repeated choice is linear; XSD models keep the 256-width cap. | [`e21d0c1`][e21d0c1], [`0c7f0eb`][0c7f0eb] |
| Each nested repeated group ref kept another copy of the inner model's edges: 216 MB retained from a 14.6 KB schema | Each follow edge is recorded once, against its innermost scope. | [`f856400`][f856400], [`6bb84b2`][6bb84b2] |
| Particle Valid (Restriction) walked parallel group-ref chains once per path, 2^n calls for an 11 KB schema | Each particle pair is decided once per check; subsumption state sets use `slices.Sort`. | [`282aec6`][282aec6], [`fce2d5e`][fce2d5e] |
| Each restriction of a union re-stored the whole member list in the never-evicted global type table, N×K names | Unions with identical member lists share one stored list, in `RegisterUnion` and `Merge`. | [`ed4a60f`][ed4a60f] |
| An XSD date year past about 2.9e11 wrapped in int64 or was refused, and then passed facets, enumerations and keyrefs | Temporal values compare with an unbounded year, in 400-year eras. | [`7b3b8cf`][7b3b8cf] |
| xs:gYear and xs:gYearMonth accepted any int64 year, so `daysFromCivil` wrapped and different years compared equal | The year is held to the xs:date range: FODT0001. | [`75039ea`][75039ea] |
| An element with `xsi:nil="true"` skipped the keys and keyrefs on its own declaration, tree and stream | Both validators evaluate a nilled element's identity constraints and pass its tables up. | [`ee8aaa2`][ee8aaa2] |
| Each nested asserted element deep-copied its subtree outside every budget, even after cancellation | The copy checks stop and cancel first and charges every node to the assertion node budget. | [`952e6d2`][952e6d2] |
| `WithInstanceLocations` checked only a pair's namespace text, so a document defining another namespace was loaded | A document not defining the namespace it was named for is declined, never added (src-import.3.1). | [`c767803`][c767803] |
| With `xsdnet.HTTPResolver`'s nil `Files`, an instance location could open any readable host file as its schema | A local location outside the schema's directories is declined before any resolver is asked. | [`c767803`][c767803] |
| `MaxDocuments` was counted as documents were dequeued, so references past the limit were fetched first | Each new document is charged after its reference resolves and before it is read and parsed. | [`429b9c6`][429b9c6] |
| `IsForeignNetworkPath` missed the NT `\??\` prefix, so `\??\UNC\host\share` could reach the host on Windows | `\??\` paths are device paths, refused whatever the roots. | [`639bcb4`][639bcb4] |
| `fn:parse-ietf-date` panicked out of Eval on seconds 00–09 with a fraction below 1e-9 | The one-digit seconds string is padded without indexing past it. | [`8211a76`][8211a76] |
| JSON fallback replacements, `fn:json-to-xml` nodes and `fn:xml-to-json` text (amplified 2×depth by indent) were built uncharged | Fallback uses and output text are charged to `MaxBytes`, built nodes to `MaxNodes`, streamed and tree alike. | [`406c6e1`][406c6e1], [`5423f40`][5423f40] |
| `unparsed-text`, `unparsed-text-lines` and `json-doc` returned resolver text uncharged, so repeated reads kept unbounded copies | Every read is charged to `MaxBytes`. | [`3251f40`][3251f40] |
| `fn:serialize`'s map form took a doctype-system with both quote kinds, breaking out of the literal into live DTD markup | It is checked like the element form: SEPM0016. | [`2f9d010`][2f9d010] |
| `format-number` padding added one zero per step and exponent splitting rescanned the prefix per separator, O(n²) in the picture | Both are linear. | [`d5e2b0f`][d5e2b0f] |
| A closed subtree whose constant fold failed was re-evaluated by every ancestor, so `() + 1 + 1 …` was quadratic in Compile | A declined fold is recorded and never retried. | [`42ac077`][42ac077] |
| The operator chain bound was counted per loop, so nested sub-limit chains composed into a spine that overflowed the stack | The parser bounds the chained height of each path at 10,000, postfix and lookup chains included (XPDY0130). | [`e965b1f`][e965b1f] |
| The opt-in backtracking regex recursed once per consumed character, so a long subject overflowed the stack | Recursion depth is capped at 50,000; past it is the XPDY0130 budget error. | [`a97ac65`][a97ac65] |
| Go's onepass copy duplicates a class per instruction, so `^\p{L}{990}$` cached about 8 MB and a facet kept as much | `CompileBounded` charges each onepass use against `MaxRegexpProgram`; the cache weighs it too. | [`d258054`][d258054], [`6ad619f`][6ad619f] |
| `group by` concatenated non-grouping variables uncharged, and sliding windows copied n(n+1)/2 items past `MaxItems` | Merged values and window copies are charged to the item budget; the window boundary scan observes cancellation. | [`11da206`][11da206] |
| An imported schema's include closure was read uncharged, and each library module got a fresh `MaxSchemaBytes` | The closure is read through a charging resolver, and one schema byte counter spans every module of a compile. | [`4fafb61`][4fafb61] |
| A module imported thousands of times was recompiled per import, since only the number of imports was capped | Module compiles charge their node count against 500,000 per `Compile` (`ErrResourceLimit`). | [`d0591ae`][d0591ae] |
| A static variable calling `fn:transform` on its own stylesheet ran B^K nested compiles, each with its own counts | Static-phase nested compiles share the outer `Compile`'s visit and node budget. | [`d0591ae`][d0591ae] |
| A global param bound to `accumulator-after#1`, and a used package's frame function, pinned every compiled stylesheet forever | Both global records are weak and die with their stylesheet. | [`69648fe`][69648fe] |
| `fn:transform` from bare XPath zeroed the caller's byte counter, so each call reset the bound | An adopted byte counter arrives held, so the nested run continues the count. | [`8aa67d3`][8aa67d3] |
| `xsl:sequence` into an as-typed variable charged no items, so sibling variables doubled to 10× `MaxItems` | A sequence constructor's held items are checked against `MaxItems` after each append. | [`5423f40`][5423f40] |
| Nested streamed merges each held 64 inputs open, about 128 descriptors per level, with no total | Open merge inputs are counted across the nest; a merge past 64 runs on the tree. | [`c1836df`][c1836df] |
| Streaming copied every spine element per burst but charged only attributes | The refusal counts every ancestor record; since small bursts share copies it is a backstop. | [`b2fcde5`][b2fcde5] |
| c14n under `Func`/`FromXPath` sets rescanned every in-scope binding and ancestor attribute at each apex, with no output | An apex considers only bindings that differ from those rendered; inherited `xml:*` is computed once per parent. | [`94aa606`][94aa606] |
| RELAX NG §7 checks re-walked a definition per `<ref>`: a 1.9 KB diamond chain took 8.7 s to compile | Each definition is checked once per relevant context. | [`5734988`][5734988] |
| RELAX NG list tokens skipped `MaxPatternSize`: 20 tokens in a nested-oneOrMore list cost 25 s and 224 MB | The bound is checked between list tokens. | [`f6079cf`][f6079cf] |
| Repeated RELAX NG includes were parsed and kept per fetch, so one 256 KB leaf held about 2.6 GB | Fetched documents share one compile-wide node and byte budget. | [`aff6564`][aff6564] |
| `not('a' eq 'A')` and other foldable parents of a comparison were folded under codepoint order, ignoring the default collation | A comparison is not closed to the folder, so its ancestors run under the collation in force. | [`967b27a`][967b27a] |
| `xsl:import-schema` read its include and import closure uncharged | Every closure document is charged to `MaxSchemaBytes`, one counter per `Compile`; over is `ErrResourceLimit`, not XTSE0220. | [`4ad352d`][4ad352d] |
| `c14n.FromXPathFilter` ran each node under a fresh, uncancellable context: a 70-byte filter held a CPU 4 s | `FromXPathFilterContext` runs them on one context, cancellable within a node, under one budget. | [`2a41550`][2a41550] |
| `xdm.Flatten` and XInclude could not be cancelled, and the CLI `-timeout` started after `-xinclude` | `FlattenContext` polls every 1024 steps, the pass checks its `Context`, and `-timeout` covers both. | [`9399efd`][9399efd] |
| A `Compile` queued behind another could not give up, and the static phase ran on `context.Background()` | `CompileContext` waits on a channel slot, and static and nested `fn:transform` compiles use the caller's context. | [`cfa135c`][cfa135c], [`4694fa3`][4694fa3], [`0ecee9c`][0ecee9c] |
| Package serials were process globals never reset, so generated `xsl:original` names depended on earlier compiles | Serials and composition caches live in a per-compile environment. | [`f84502b`][f84502b] |
| Overrides wrote the generated `xsl:original` namespace into literal results, then dropped user ones sharing its prefix | Only the exact URIs a compile generated are excluded (XSLT 3.0 §11.1.3). | [`591831a`][591831a], [`4950817`][4950817] |
| A transform's run-time `fn:transform` calls each minted a fresh compile budget | Each transform holds one budget, adopted by every nested call; a cache hit charges nothing. | [`e975737`][e975737] |
| Streamed grounding copied the document node, spine and attributes per burst, then refused deep documents | Small bursts share ancestor copies in one ground batch; the spine refusal is a backstop. | [`49721db`][49721db] |
| On Linux an `os.Root` symlink escape returned a raw error naming the resolved path | Any `os.Root` refusal but not-found is the outside-roots error naming only the href. | [`ddc6ee2`][ddc6ee2] |
| `fold-left`, `for-each`, `sort` and other function-item loops never polled cancellation | `callItem` polls the context once per call. | [`9aefe48`][9aefe48] |
| Regex size, backtracking and backreference-analysis limits raised FORX0002, and `Flatten`'s had no code | They raise XPDY0130 (XPath §2.3.1), and `xsl:analyze-string` no longer re-wraps them as XTDE1140. | [`e7042aa`][e7042aa], [`f077a70`][f077a70] |
| A regex compiled up to three times per call, and a program over 4,096 instructions was never cached | It compiles once; the cache is weighed by program bytes: 32 MiB in all, 4 MB an entry. | [`1efb8e1`][1efb8e1], [`f077a70`][f077a70] |
| `unparsed-text-available` charged the bytes it read, so a guarded `unparsed-text` paid twice | The probe runs the read's check, then refunds; a failed read charges nothing. | [`87743b2`][87743b2] |
| `FlattenContext` copied every input, so `fn:data` paid an O(n) copy with no array present | An array-free sequence is returned as it is (F&O §17.3.18). | [`e8f4dfa`][e8f4dfa] |
| Since `967b27a` no comparison folded, so `1 eq 1` and its ancestors waited for run time | A comparison of two numeric literals folds again; it needs no collation. | [`390b351`][390b351] |
| The backtracker's depth `defer` made it about 2.8× slower | Each return releases the depth directly; the bound is unchanged. | [`c870472`][c870472] |
| `MaxDocuments` was charged before dedup, so a cyclic set of exactly `MaxDocuments` could not load | Only a document not read yet is charged, after resolve and before parse. | [`6297a00`][6297a00] |
| An instance location outside the schema directories, unresolvable, or of another namespace failed the call | It is declined (XSD §4.3.2) and the call succeeds with the rest. | [`7e92b19`][7e92b19] |
| Errors about instance-reached schema documents quoted the absolute host path | They quote the location as written; resolver errors keep only their kind. | [`7b6ae04`][7b6ae04], [`25eea7b`][25eea7b] |
| A megabyte-long gYear cost seconds per comparison, and `parseYear` took a `+` sign | Over 1000 digits is refused with `ErrResourceLimit` (Part 2 §5.4); `+` is refused. | [`97fccf5`][97fccf5] |
| A date year over 18 digits was reported invalid, and long fractional seconds parsed superlinearly | The leap rule reads the last four digits; any temporal field over 1000 digits is refused. | [`eb9ddf8`][eb9ddf8] |
| Duration years and months were int64 months, so `P800000000000000000Y` compared shorter than `-P1Y` | Months are a `big.Int` and compare exactly. | [`177fbff`][177fbff] |
| A deterministic DTD `(a1|…|a1100)*` was refused: each last position copied the group's first set | Follow sets share disjoint pieces, about 2n for a repeated choice of n. | [`b6a5d50`][b6a5d50] |
| `fn:id`, `fn:idref` and `fn:element-with-id` took any unprefixed `id`/`idref`/`idrefs` attribute as typed | Only is-id/is-idrefs nodes count: DTD- or schema-typed, or `xml:id` (F&O §14.5). | [`b9171e8`][b9171e8], [`0caab03`][0caab03] |
| `fn:id` and `fn:element-with-id` returned every element holding a duplicated ID value | Each returns the first in document order (F&O §14.5.1); `fn:idref` still returns all. | [`ca1607b`][ca1607b] |
| An external subset declaring ATTLISTs but no entities never applied its attribute types | The expanded subset is re-read whenever it differs from the directive, so its `ID` types hold. | [`c5d5bce`][c5d5bce] |
| Date and time component accessors reported a failed untyped cast as XPTY0004 | They propagate the cast's FORG0001, as the duration accessors do (XPath §3.1.5.2). | [`1505000`][1505000] |
| `fn:serialize` with the xhtml method wrote `<br/>` and `<p/>` | A void element is `<br />`, any other empty one `<p></p>`, as `xsl:output` writes them (Serialization §6). | [`5ad849d`][5ad849d] |
| `fn:serialize` html left the added meta bare in an XHTML head and did not self-close XML islands | Both serializers share `htmlser.MetaEnd` and `htmlser.Island` (Serialization §7.1). | [`b12ff0b`][b12ff0b], [`bee5b12`][bee5b12] |
| The html method at version 5.0 wrote a C1 control in text raw | It is written as a character reference, as attribute values already were (Serialization §7.1). | [`9d7cf65`][9d7cf65] |
| `cache="yes"` returned the same nodes from a node-building function where fresh ones were due | A proactive function reuses only all-atomic results; an elidable one is memoised (XSLT §10.3.8). | [`605d0be`][605d0be] |
| `fn:transform` refused an atomic or function `global-context-item`, and ignored `package-*` beside `stylesheet-*` | Any item becomes the global context item; the mixed options raise FOXT0002. | [`57a0c9d`][57a0c9d] |
| A declared function type was split at commas, so a valid streamed `$f(.)` got XTSE3430 | The type is read with `xpath.ParseSequenceType` (XSLT §19.8.8.11). | [`2f78c52`][2f78c52] |
| §19.8 gave no verdict on text value templates, `fn:root`, `fold-right`, `function-lookup`, empty axes, `?` and `\|\|` | Each is modelled, so a required XTSE3430 is raised. | [`a5013a4`][a5013a4], [`b0428d0`][b0428d0], [`f41bb8c`][f41bb8c] |
| A namespace node after content was admitted in a tree, and `xsl:sequence` dropped a late attribute | Both raise XTDE0410 (XSLT §5.7.1), tree and stream. | [`090a9a8`][090a9a8], [`2ef0012`][2ef0012] |
| XQuery dropped a namespace node that followed content in a constructor | It raises XQTY0024 (XQuery §3.9.1.3). | [`2a1e3b9`][2a1e3b9] |
| `validate type T` with T out of scope was XQDY0084 at run time, and `xs:anyType` was refused | XQST0104 at compile; every built-in type is in scope (XQuery §3.21). | [`ff9032d`][ff9032d], [`1889367`][1889367] |
| `declare construction strip` left copied nodes typed | Copies get `xs:untyped` (attributes `xs:untypedAtomic`); the original keeps its type (XQuery §4.6). | [`fa87dcc`][fa87dcc] |
| `(1, <w>{1}</w>)` in a clause expression was XPST0003: a `<` after a comma read as less-than | It opens a direct constructor. | [`c13aa92`][c13aa92] |
| A library module importing its own namespace under its own prefix was XQST0033 | It is accepted (XQuery §4.12). | [`c114ef5`][c114ef5] |
| XQuery 1.0 and 3.0 modules were held to 3.1's `ordered{}`, pragma-name and `namespace-node()` rules | Each version applies its own (XPST0003, XPST0081, XPST0017). | [`cc13393`][cc13393] |
| An undeclared entity where XML §4.1 makes it a VC was dropped silently: `x&u;y` read as `xy` | It is an error naming the VC (§4.4.3: inform the application). | [`8650527`][8650527] |
| Under `AllowDOCTYPE` malformed DTD declarations and text declarations were accepted: 417 xmlconf not-wf cases | The declaration grammar is checked; 23 remain. | [`252b65b`][252b65b] |
| The tokeniser took any `<?xml` PI as the declaration and read its pseudo-attributes by substring | Only at offset 0, parsed by XML §2.8 [23]-[26]. | [`72599d9`][72599d9] |
| `dtd.Validate` passed a 1.0 document whose DTD loads a 1.1 external entity | `Load` records the entity and `Validate` refuses it (XML 1.1 §4.3.4). | [`ee5018c`][ee5018c] |
| XInclude did no language fixup, so an included element lost its inherited `xml:lang` | `xml:lang` is added where the languages differ (XInclude §4.5.6). | [`93b8855`][93b8855] |
| XSD 1.0 parsed and silently ignored 1.1-only components such as `xs:assert` | They are refused, "requires XSD 1.1"; `vc:minVersion` exclusion is kept. | [`f9a904d`][f9a904d] |
| A key field on an attribute a skip or lax wildcard left unassessed was accepted (`idZ015`) | The field fails (XSD §3.11.4 clause 3); under 1.1 a skipped one leaves the field. | [`7362b27`][7362b27] |
| RELAX NG's overlap test ignored `anyName` exceptions, refusing `xspec.rnc` | It uses jing's representative-name test (§7.3, §7.4). | [`e960da2`][e960da2] |
| `dtd.Validate` never saw `xmlns` attributes, so a `#REQUIRED xmlns:p` refused every document | Namespace declarations are checked as XML 1.0 attributes, as xmllint does. | [`c516340`][c516340] |
| `xsl:stream`, the last-call draft's name, was accepted beside `xsl:source-document` | It is XTSE0010 under version 3.0 again (REC change log, Bug29747). | [`b6aef3e`][b6aef3e] |
| `Result.Warnings` grew with the input under `warning-on-no-match` | It keeps the first 100 and one "further warnings suppressed" line. | [`94acc9f`][94acc9f] |
| `P instance of T*` at the document node held every match and fell back to the tree | It streams as a count of matches failing T (§27.5); fallbacks 26 → 23. | [`50bba71`][50bba71] |

### Changed — behaviour

| Change | What it means | Commit |
|---|---|---|
| `dtd.Validate` reports undeclared attributes | A document that validated before may now fail; `Options.AllowUndeclared` skips them, as it skips undeclared elements. | [`915eb63`][915eb63] |
| XQuery `validate lax` with no imported schema returns a copy of its operand | The result is a new node, not the input node, as §3.21 requires; the input keeps no annotation. | [`f46ec7b`][f46ec7b] |
| A tree past the text store's block ceiling is an error, not a panic | Entry points that return an error return `xdm.ErrResourceLimit`; `xdm`/`xdmbuild` methods with no error return still panic, with an error value that matches it. | [`91f30c9`][91f30c9] |
| `ParseOptions.MaxNodes` counts text that is not all white space, comments and PIs | A document near the bound may now be refused; whitespace-only text is still not counted. | [`9e9a6f9`][9e9a6f9], [`089c346`][089c346] |
| One XInclude pass shares a node budget | Everything its inclusions copy in counts against `Parse.MaxNodes`, once per level it is copied through. | [`eb4d0ae`][eb4d0ae] |
| An `xsl:use-package` cycle is XTSE3005 | A package that uses itself, directly or through another, is a static error, not a crash. | [`dce8830`][dce8830] |
| `Compile` refuses a stylesheet needing over 10,000 module visits | Each include/import resolution counts, in the static phase and the compile; real stylesheets need about 120. | [`77c3c5d`][77c3c5d] |
| Package reads and compiles count as module visits | A package graph compiling over 10,000 package copies is refused with `ErrResourceLimit`; the W3C package cases need at most 16. | [`4c5c3ee`][4c5c3ee] |
| A streamed parse refuses over 64 ancestor attributes per record past 16M copies | It returns `ErrResourceLimit` where Parse + Transform succeeds; back off to the tree on `errors.Is`. | [`f90e97d`][f90e97d] |
| A streamed `xsl:merge` of more than 64 inputs runs on the tree | Output is unchanged; memory is the tree's. | [`e621bf7`][e621bf7] |
| `Serialize` refuses an `OutputSettings.Standalone` other than "", omit, yes or no | SEPM0016, where a value such as "true" was written raw. | [`877b3af`][877b3af] |
| `xsl:key` entries are bounded per transform | They are totalled across every index against `MaxItems`, so many keyed temporary trees can raise XPDY0130. | [`d06d0f3`][d06d0f3] |
| c14n XPath transforms use XPath 1.0 rules | A relational comparison of two strings converts both to numbers, as XML-DSig requires. | [`0c70a04`][0c70a04] |
| Rooted file resolvers refuse UNC and device paths | `//host/…`, `\\host\…`, `file:////host/…` and `\\?\…` fail with "names a network path" on every OS unless a root is on that `\\host\share`. | [`60fed8a`][60fed8a] |
| `dtd.Validate` checks the root element against the DOCTYPE name | A document whose root is another declared element is now invalid, as XML 1.0 requires. | [`a543265`][a543265] |
| XPath refuses a chained-operator path over 10,000 nodes deep | XPDY0130 where a deeper expression used to overflow the stack; the longest real chain seen is 190. | [`e965b1f`][e965b1f] |
| The backtracking regex refuses subjects needing over 50,000 nested calls | XPDY0130 (`ErrResourceLimit`), about 25,000 characters under `.*`; only with the backtracker enabled. | [`a97ac65`][a97ac65] |
| Anchored regexes with large class payloads are refused | Only an onepass-sized anchored one: `^\pL{753}$` or a 990-copy facet fails `MaxRegexpProgram`; unanchored compiles. | [`d258054`][d258054], [`6ad619f`][6ad619f] |
| `Flatten` refuses over 16.7M items inside arrays | Even with `MaxItems` raised or -1; `array:flatten` and `fn:data` also charge the host's `MaxItems`. | [`9c19935`][9c19935], [`0d82e86`][0d82e86] |
| Resource text, JSON, sequence constructors, group-by and windows count against the item, node and byte budgets | Large results can now raise XPDY0130; `unparsed-text-available` raises the budget error instead of answering false. | [`406c6e1`][406c6e1], [`3251f40`][3251f40], [`5423f40`][5423f40], [`11da206`][11da206] |
| XInclude passes share byte and visit budgets | A pass whose inclusions copy over `MaxBytes`, or whose local searches visit over `MaxNodes`, is refused. | [`ed500f2`][ed500f2] |
| `Compile` refuses stylesheets whose modules total over 500,000 nodes | Counted across imports, includes, packages and static `fn:transform`; xslTNG needs about 40,000. | [`d0591ae`][d0591ae] |
| `WithInstanceLocations` binds documents to their namespace and the schema directories | A mismatched namespace, or a local location outside the schema's directories, is declined (XSD §4.3.2): skipped, and the call succeeds. | [`c767803`][c767803] |
| `\??\` paths are refused as device paths | A local `\??\C:\…` spelling is refused too, as `\\?\C:\` already was. | [`639bcb4`][639bcb4] |
| XSD `MaxDocuments` is charged per new document read | Charged after resolve and before parse; the document past the limit is never read, and repeat references cost nothing. | [`429b9c6`][429b9c6] |
| Nilled elements enforce their own identity constraints | A nilled element breaking a key on its own declaration is now invalid, tree and stream. | [`ee8aaa2`][ee8aaa2] |
| RELAX NG list derivatives and fetched documents are bounded | A list derivative past `MaxPatternSize`, or fetched documents totalling over xdm's single-document limits, are refused. | [`f6079cf`][f6079cf], [`aff6564`][aff6564] |
| `fn:serialize` map-form doctype parameters are checked | Values the element form refuses (SEPM0016) are refused in the map form too. | [`2f9d010`][2f9d010] |
| Regex and `Flatten` limits raise XPDY0130 | `catch err:FORX0002` no longer sees them; a malformed pattern is still FORX0002, and `xsl:analyze-string` passes them through. | [`e7042aa`][e7042aa], [`f077a70`][f077a70] |
| `xsl:import-schema` reads at most 16 MB per compile | Counted across modules, packages and static `fn:transform`; raise `CompileOptions.MaxSchemaBytes` for larger schema sets. | [`4ad352d`][4ad352d] |
| `WithInstanceLocations` declines rather than fails | An outside, unresolvable or mismatched location is skipped and the call succeeds; a reference needing it fails at the reference. | [`7e92b19`][7e92b19] |
| XSD temporal fields over 1000 digits are refused | `ValidationError.Err` wraps `xdm.ErrResourceLimit`: "cannot decide", not invalid. | [`97fccf5`][97fccf5], [`eb9ddf8`][eb9ddf8] |
| The CLI's `-timeout` covers `-xinclude` | A slow XInclude pass now counts against the same deadline as the transform. | [`9399efd`][9399efd] |
| `fn:id`, `fn:idref`, `fn:element-with-id` ignore undeclared `id` attributes | Declare the attribute `ID` in a DTD or schema, or use `xml:id`; a non-NCName token is ignored. | [`b9171e8`][b9171e8], [`0caab03`][0caab03] |
| An undeclared entity where XML §4.1 makes it a VC is refused | Such a document used to parse with the reference dropped; supply `ParseOptions.ExternalEntities` to read it. | [`8650527`][8650527] |
| Malformed DTD declarations are refused under `AllowDOCTYPE` | DTDs accepted before are refused when they break the XML 1.0 declaration grammar. | [`252b65b`][252b65b] |
| `dtd.Validate` refuses a 1.0 document using a 1.1 external entity | XML 1.1 §4.3.4; a tree with no declared version is 1.0. | [`ee5018c`][ee5018c] |
| `dtd.Validate` checks `xmlns` and `xmlns:p` as attributes | An undeclared namespace declaration is now a validity error, as in xmllint `--valid`. | [`c516340`][c516340] |
| XSD 1.0 refuses XSD 1.1 components | A 1.1 schema loaded as 1.0 fails with "requires XSD 1.1" instead of losing its constraints. | [`f9a904d`][f9a904d] |
| A key field on an unassessed wildcard attribute is invalid | Tree and stream, XSD 1.0 and 1.1; declare the attribute globally to keep a lax match typed. | [`7362b27`][7362b27] |
| `xsl:stream` is XTSE0010 | Write `xsl:source-document`; forwards-compatible mode takes the `xsl:fallback`. | [`b6aef3e`][b6aef3e] |
| An attribute after content from a successful `xsl:try` raises XTDE0410 outside the try | The error arises building the parent (§5.7.1, §8.3); it was dropped. Saxon-HE 12.7 catches it inside. | [`2ef0012`][2ef0012] |
| `cache="yes"` on a proactive function returns fresh nodes | Only atomic results are reused; mark the function `new-each-time="maybe"` to memoise nodes. | [`605d0be`][605d0be] |
| `fn:transform` refuses `package-*` beside `stylesheet-*` | FOXT0002 where the `package-*` option was ignored. | [`57a0c9d`][57a0c9d] |
| Date and time component accessors raise FORG0001 for a bad untyped value | `catch err:XPTY0004` no longer sees a failed cast there. | [`1505000`][1505000] |
| XQuery `validate type` checks its type at compile | XQST0104 from `Compile`; a built-in type with no schema import validates instead of XQDY0084. | [`ff9032d`][ff9032d], [`1889367`][1889367] |
| XQuery 1.0 and 3.0 modules apply their own version's rules | Empty `ordered{}`, an unprefixed pragma and a bare `namespace-node()` step are refused there; unversioned modules are 3.1. | [`cc13393`][cc13393] |

## v2.2.0 — 2026-11-01

Memory footprint work: what a tree holds and what a transform peaks at. Output
is byte-identical; new API is additive.

### Added

| Change | What it does | Commit |
|---|---|---|
| Streamed aggregates over records | A lone `value-of`/`copy-of`/`sequence` over one record path streams too; `count`/`sum`/`min`/`max` fold per record: 1 GB summed in 33 MB RSS. Any error re-runs on the tree. | [`457aa03`][457aa03], [`9f3d815`][9f3d815], [`48b1a38`][48b1a38], [`3157566`][3157566], [`4944511`][4944511] |
| Streamed `xsl:source-document`, first stage | A streamable `xsl:for-each`/`xsl:iterate` over a child path reads one record at a time: 1 GB totalled at 27 MB RSS, not 3.7 GB, at about 2× CPU. Other shapes build the tree. | [`3f185fe`][3f185fe], [`27757b3`][27757b3] |
| `xdm.ParseBursts` | Parses a document handing over one selected element at a time in a tree of its own, with limits counted over the whole document; memory stays at the spine plus one record. | [`b4b6052`][b4b6052], [`80f999f`][80f999f] |
| `xslt.FileResolver.MaxNodes`; `MaxBytes` reaches the parser | Raising `MaxBytes` past 64 MB had no effect on `fn:doc`, the parser kept its own default. Both limits now apply to what the resolver parses. | [`80f999f`][80f999f] |
| `xdmbuild.InPlaceDocument` | A builder rooted at a new document node, for content built straight into it. | [`302c932`][302c932] |

### Changed — streamability analysis

| Change | Problem → solution | Commit |
|---|---|---|
| Six constructs escaped the §19.8 check and compiled | A nested `xsl:map-entry`, extension instructions (their `xsl:fallback`), `xsl:result-document` serialization AVTs, boolean pattern predicates, `mode="#all"` rules and rule `xsl:param` defaults are now judged: such stylesheets get XTSE3430. | [`b5703be`][b5703be], [`8c391d8`][8c391d8], [`ff0ed5e`][ff0ed5e], [`ca1b029`][ca1b029], [`268acce`][268acce], [`7920635`][7920635] |
| A verdict could rest on a declaration nobody checked | Verdicts carry an unverified bit for streamable functions, attribute sets, accumulators, modes and other modules; streamed execution requires it clear. No refusal changes. | [`b12ecbf`][b12ecbf], [`e94cdf9`][e94cdf9] |

### Changed — memory

| Change | Problem → solution | Commit |
|---|---|---|
| Namespace nodes lived as long as their tree | `//namespace::*` on 5 MiB kept 140 MB. They are held weakly, 16 to a chunk, and freed when nothing holds them: under 0.5 MB after the query. | [`ac4973e`][ac4973e], [`fcab85a`][fcab85a], [`e1b5e1f`][e1b5e1f] |
| Built-in shallow-copy built a document result twice | In a mode with no template rules it builds in place: parse-100mb identity peak 1091 → 930 MB, CPU −25 to −40%. | [`302c932`][302c932] |
| Built trees stored every whitespace-only text value | They share one value per string, as a parse does: an identity result is the size of its source again. | [`c60f8f3`][c60f8f3], [`09f0c5d`][09f0c5d] |
| A deep copy duplicated the source's text | `xsl:copy-of` of 4,096+ records borrows the source blocks it fills by half or more: XMark identity result −28%, peak −19%. | [`546af03`][546af03], [`b4cd614`][b4cd614] |
| Every `copy-of` was kept until the transform ended | Each copied node was recorded against its original for accumulators, even with none declared: a 10 MB stream copying each record peaked at 830 MB. The table exists only when an accumulator does: 2.8 MB. Also in v2.1.1. | [`c7952b0`][c7952b0] |
| Full record chunks were padded 6% by the malloc header | They are allocated two at a time past the 8th, 40,960 B exactly: parse-100mb retained 370 → 350.5 MB. | [`907c674`][907c674] |

## v2.1.1 — 2026-11-01

Memory and CPU fixes from go-xmlsec's measurements against v2.1.0. No API
change; output is byte-identical.

### Fixed

| Change | Problem → solution | Commit |
|---|---|---|
| `c14n.ParsePrefixList` was quadratic in the prefixes | It deduplicated with a scan per token: 1 MiB of distinct prefixes took 10.9 s. It uses a map, keeping first-seen order: about 0.02 s. | [`ad8c47a`][ad8c47a] |

### Changed — memory

| Change | Problem → solution | Commit |
|---|---|---|
| A large text value cost up to 3.1× its size to parse | `slices.Grow` rounded the buffer past the hand-off check, so the value was copied twice. The buffer is grown exactly and handed to the tree when no later value wants it: 1 MiB 3.10 → 1.12×, 10 MiB 1.87 → 1.08×. | [`707ca28`][707ca28] |
| A tag with many attributes or namespace declarations peaked at 13–22× its size | Duplicates past 8 attributes are found by sorting, no name index is built for such a tag, one scope index serves both lookups and repeated URIs are interned: 800K attributes 21.5 → 12.8× peak, 100K declarations 16.5 → 6.6×. | [`5475837`][5475837], [`b08624c`][b08624c] |
| c14n allocated a map per element of a namespace node set; the namespace axis sorted through a map | The map is reused (2,000 elements: 4,016 → 18 allocations); `scopeSlots` uses one stable sort (4 → 1 allocation a call). | [`9572c44`][9572c44], [`fd9750e`][fd9750e] |
| `/r/l[1]` built every `l` child before the predicate ran | A step whose first predicate is an integer literal stops at that match. | [`5712d9f`][5712d9f] |

## v2.1.0 — 2026-11-01

Three memory audits across parsing, XPath/XQuery, XSLT, validation,
serialization and c14n, and the items go-xmlsec reported. Output is
byte-identical except where *Changed* says otherwise. New exported API, listed
under *Added*, makes this a minor release.

### Added

| Change | What it does | Commit |
|---|---|---|
| `xpath.SerializeJSONTo`, `xpath.SerializeAdaptiveTo` | The json and adaptive methods written to an `io.Writer` instead of returned as a string. | [`599b2f9`][599b2f9] |
| `xdm.Node.AddNamespaces` | Sets several namespace bindings on an element in one call. | [`b42f04a`][b42f04a] |
| `xdm.Node.Index`, `Node.SubtreeEnd`, `Tree.Len`, `Tree.NodeAt` | A node's dense, document-order position in its tree (attributes included, namespace nodes -1), stable once the node exists; size a table with `SubtreeEnd() - Index()` and look nodes up by position. | [`b37796b`][b37796b] |
| `xdm.Node.InScopeNamespaceBindings` | Iterates an element's in-scope prefixes and URIs without making a namespace node for each. | [`73249e2`][73249e2] |
| `xdm.ArrayItem.AppendMembers` | Appends an array's members to a slice without an intermediate copy. | [`59ac569`][59ac569] |
| `xpath.CompileBounded`, `xpath.MaxRegexpProgram` | `regexp.Compile` that refuses a program over 100,000 instructions; used by XPath and XSD patterns. | [`c1c2bb9`][c1c2bb9] |
| `xpath.Env.MaxDigits`, `xpath.MaxDigits` | Bounds the digits of an `xs:integer` or `xs:decimal` cast or arithmetic result; 0 is 10^6, negative is none. | [`8577906`][8577906] |
| `xdm.AppendIntegers`, `xpath.Context.WithVars` | Appends a run of integers allocated a chunk at a time; binds several variables in one scope. | [`30c7c07`][30c7c07], [`5951f24`][5951f24] |
| `xpath.Context.NodesRefused`, `ItemsRefused` | Whether the node budget was crossed, and whether a running item total is past the item bound, without charging either. | [`4e8f0a5`][4e8f0a5], [`21995a6`][21995a6] |

### Changed

| Change | What to do | Commit |
|---|---|---|
| `xsd` and `dtd` error messages clip document-supplied strings to 1 KB plus "..." | Nothing, unless you match messages longer than 1 KB. Message wording is not part of the contract. | [`20958aa`][20958aa], [`bd865e4`][bd865e4] |
| json and adaptive serialization can leave partial output when an error is raised mid-document | Treat the writer's content as invalid on error, as the XML methods already required. | [`599b2f9`][599b2f9] |
| New refusals: maps and arrays nested past 1,000 levels in json/adaptive output (XPDY0130), `deep-equal` and `map:find` (XPDY0001), with their own counter, apart from function recursion; regexes over 100,000 instructions (FORX0002, or a pattern error in XSD); picture widths over 65,536 (FOFD1340); `xsl:key` and `group-by` keys past 5,000,000 retained (XPDY0130); integers over 10^6 digits (FOCA0003, FOAR0002); sequences built by `,` or `!`, `xsl:next-iteration` values, `unparsed-text-lines` lines, `parse-json`/`json-doc` values and stored accumulator items past `MaxItems` (XPDY0130); `xsl:copy-of` of a document past `MaxNodes` (XPDY0130) | Nothing for real inputs: the largest W3C suite regex is 4,313 instructions. All wrap `xdm.ErrResourceLimit`. | [`702c6e6`][702c6e6], [`014a862`][014a862], [`c1c2bb9`][c1c2bb9], [`21995a6`][21995a6], [`8577906`][8577906], [`dcb49b2`][dcb49b2], [`c73a06b`][c73a06b], [`a3d8ef6`][a3d8ef6], [`40cd52c`][40cd52c], [`b35ec14`][b35ec14] |
| The CLI drops its collector from `GOGC=200` to 100 once the live heap passes 128 MB | Nothing; set `GOGC` to keep a fixed value. Peak RSS −20% on DocBook's largest table, about 12% more user CPU. | [`f3449f7`][f3449f7] |
| The `FileResolver` no longer evicts to admit a document that does not fit beside its cache | A document refused admission is parsed again on its next request, so across transforms it is not the same tree; within one transform it is. | [`f49ff92`][f49ff92] |
| `xdm.ProcessXInclude` returns a parsed tree unchanged when it has no XInclude element | Nothing; a parsed tree is frozen. A built tree is still copied. | [`e139525`][e139525], [`9c3a983`][9c3a983] |

### Fixed

| Change | Problem → solution | Commit |
|---|---|---|
| `xsd` error messages kept attacker strings whole | A 1 MB namespace in 200 attributes kept 105 MB. `fail` and `attrName` clip to 1 KB: 0.13 MB. | [`20958aa`][20958aa] |
| The DTD validator built every element's path, quadratic in depth | A valid 990-deep chain of 4 KB names allocated 2 GB. The path is rendered only for a failure, text unchanged. | [`8dcb07f`][8dcb07f] |
| DTD validity messages kept `#FIXED`, enum and ID values whole | 200 mismatches on a 1 MB `#FIXED` value kept 105 MB. `fail` clips to 1 KB. | [`bd865e4`][bd865e4] |
| The nested `fn:transform` cache kept compiles for dead resolvers | A fresh resolver per call kept 49 MB on DocBook. Resolvers are weak keys and dead entries are dropped: 0.3 MB. | [`a5b385d`][a5b385d] |
| Sequences built by `,` or `!` were not counted against `MaxItems` | A 70-byte doubling `fold-left` built 134M items against the 5M limit, at 6.3 GB; through recursive `with-param` too. They are charged above 64 items. Also in v2.0.3. | [`dcb49b2`][dcb49b2] |
| RELAX NG reported a valid document invalid | `(a?){13} a{13}` against 13 `<a/>` hit the derivative cap, naming a `oneOrMore` the schema lacks; `xmllint` accepts it. Choice chains merge alternative by alternative: N = 12, 13, 18, 30 validate. Also in v2.0.3. | [`44229aa`][44229aa] |
| Serializing a sequence of atomic values was quadratic | `1 to 100000` took 21.6 s and 6.95 GB, joined by repeated concatenation. Each run is joined once: 0.056 s. Also in v2.0.3. | [`a9ad280`][a9ad280] |
| Text lines, JSON values, DTD default values, integer digits and accumulator values grew past the limits | `unparsed-text-lines` kept 5.2 GB, 2 MB of DTD defaults 1.97 GB, a 4M-digit cast 34 GB, a 200 KB accumulator 12.4 GB. Each is charged, bounded, stored once or shared. Also in v2.0.3. | [`c73a06b`][c73a06b], [`be5afc3`][be5afc3], [`8577906`][8577906], [`40cd52c`][40cd52c] |
| `xsl:copy-of` of a document was not counted against `MaxNodes` | It copied 5.7M nodes and exited 0, where shallow-copy is refused. It is charged before copying. | [`b35ec14`][b35ec14] |
| Deeply nested maps and arrays crashed the process | `serialize(..., json)` or `deep-equal` on a 400,000-deep array from a 96-byte query overflowed the Go stack. They are refused with a resource-limit error; `xdm.Flatten` is iterative. Also in v2.0.3. | [`702c6e6`][702c6e6] |
| A built-in `shallow-copy` rule returned a truncated result past `MaxNodes` with no error | A 1M-element identity copy emptied a third of its elements and exited 0. The transform now fails with XPDY0130 whenever the node budget was crossed. Also in v2.0.3. | [`4e8f0a5`][4e8f0a5] |
| Sequential regex repeats compiled to huge programs | `\pL{1000}` repeated 150 times passes Go's 1,000-repeat check: 181 KB of patterns kept 984 MB. Programs over 100,000 instructions are refused in XPath and XSD. | [`c1c2bb9`][c1c2bb9] |
| `fn:replace`, picture widths, `xsl:key` and `group-by` grew past their budgets | `replace` charged after building (12.8 GB for 500 KB in), `[D01,1000000000]` allocated 2.8 GB, and `use="1 to 1000"` over 100k nodes 3.2 GB. Each is charged or refused first. | [`c1c2bb9`][c1c2bb9], [`21995a6`][21995a6] |
| Caches bounded by entries held gigabytes of long keys or documents | Regex, collation and char-atom keys over 1 KB are not cached; the `FileResolver` document cache is also bounded by 32 MB of source. | [`c1c2bb9`][c1c2bb9], [`de4a515`][de4a515] |

### Changed — memory

| Change | Problem → solution | Commit |
|---|---|---|
| Namespace URIs were tokeniser-arena strings | Each parsed tree kept a 32 KB arena block alive. URIs are copied at declaration: DocBook trees retain 31.6 → 12.3 MB. | [`18d374d`][18d374d] |
| Every parse allocated a 32 KB string-arena block | The first block is sized to the input: DocBook corpus parse 37.6 → 19.6 MB a pass. | [`9e13f70`][9e13f70] |
| `TrackPositions` kept the source text to count lines | Line starts are recorded as the text is read: a tracked 10 MB tree retains 50.4 → 41.5 MB. | [`ec1ac54`][ec1ac54] |
| Parsed trees kept build-only indexes, slack and copied long text twice | Indexes dropped at freeze, the last text block trimmed, long text handed over: DocBook retains a further −10%. | [`2ad3a32`][2ad3a32], [`a5ab736`][a5ab736], [`4440665`][4440665] |
| Each parse allocated three read windows | They are pooled: DocBook corpus parse 19.8 → 17.1 MB a pass. | [`6345114`][6345114] |
| `isBase64Binary` copied each value into a `[]rune` | One byte pass: a 380 KB value 1.54 MB → 0 allocations. | [`5d8fceb`][5d8fceb] |
| DTD content matching, ID bookkeeping and bound facets allocated per child or value | Two reused state sets, one ID map, bounds parsed once: `dtd` validation −47%, `xsd` instance −24%. | [`e056467`][e056467], [`f09c1a3`][f09c1a3], [`14c84b5`][14c84b5] |
| XQuery FLWOR tuples copied names, rebuilt values and grew results by doubling | Shared names, own value, reserved results, version set once, no per-call library: XMark q1–q20 eval 194.9 → 119.0 MB. | [`ba9e7a0`][ba9e7a0], [`f890740`][f890740], [`a8d6e7b`][a8d6e7b], [`24b963c`][24b963c], [`73af7a8`][73af7a8], [`f148247`][f148247] |
| `a to b` and `fn:distinct-values` used big arithmetic and eager promotion keys | int64 ranges, keys written once a double arrives: `distinct-values(1 to 100000)` 96.0 → 53.2 MB. | [`70ff980`][70ff980], [`ef5f064`][ef5f064] |
| Compiled stylesheets held ~980 copies of one namespace map and pinned module trees | Interned by content; instructions keep the map, not the element: compiled CEN 9.1 → 6.1 MB, DocBook 12.4 → 9.3 MB. | [`490fc5a`][490fc5a], [`dcb15d1`][dcb15d1] |
| XSLT calls built parameter maps, memo keys, namespace frames and error text per call | A slice of pairs, a reused key buffer, one-step frames, lazy text, shared global cells: DocBook section.001 −15%. | [`e3b6288`][e3b6288], [`b5aaa52`][b5aaa52], [`b42f04a`][b42f04a], [`b6d4e4e`][b6d4e4e], [`327b09e`][327b09e] |
| The json, adaptive and text methods built the whole output as a string | They stream to the writer: xmark json 152.7 MB → 12.6 KB, text 41.7 MB → 33 KB. Normalized output keeps the string. | [`599b2f9`][599b2f9], [`cf65d62`][cf65d62] |
| CDATA sections, character maps and `fn:serialize` escaping copied per node | Written straight to the sink: xmark CDATA 20.4 MB → 34 KB, character map 16.5 MB → 34 KB. | [`cf65d62`][cf65d62], [`599b2f9`][599b2f9] |
| c14n `Bytes` and `Result.String` doubled a buffer from empty; `Serialize` allocated 32 KB a call | Output chunks are joined once and the write buffer is pooled: c14n 3.7× → 2.1× the output, a small result 33.6 → 0.7 KB. | [`f235795`][f235795], [`9496810`][9496810], [`37206b1`][37206b1], [`c501fc4`][c501fc4], [`1f18b1b`][1f18b1b] |
| The `xslt` benchmarks read inputs no checkout has, so they always skipped | They run XRechnung and Peppol CEN from `testdata/bench`. | [`beb6072`][beb6072] |
| `-xinclude` copied every tree, and included documents twice, even with nothing to include | A parsed tree with no XInclude element is returned as is and inclusions are built into the destination: `parse-100mb` 514 → 0 MB. | [`e139525`][e139525] |
| RELAX NG copied its name memo on every store | Stores land in a pending map published as a snapshot, and after 8 locked hits: 1,024 stores 47.5 → under 2 MB, lookups lock-free. | [`9f0db9c`][9f0db9c], [`676ad52`][676ad52] |
| Integer `mod`, `idiv`, negation and comparison used big arithmetic; ranges made 3 allocations an integer | int64 fast paths and chunked ranges: comparison 2 → 0 allocations, `sum(1 to 1e6)` 800k → 4.8k allocations. | [`40085f6`][40085f6], [`30c7c07`][30c7c07] |
| Dynamic calls, FLWOR tuples and window clauses made a scope per variable; closures kept their whole scope | One scope per binding set, and a closure keeps only what its body reads: `$f($i)` over 1M 2,673 → 1,617 MB. | [`5951f24`][5951f24], [`a4f316d`][a4f316d], [`b5aaf06`][b5aaf06] |
| The function result memo kept each key string | It keeps a 128-bit SHA-256 hash: DocBook `table-cals.049` live heap 359 → 172 MB, peak 1,150 → 533 MB. | [`15273c7`][15273c7] |
| `fn:doc` read the file whole and copied it; `replace` copied and over-charged a non-matching input | `fn:doc` parses as it reads (16 MB file 82 → 29 MB); `replace` returns early with no match (64 MB: 256 → 0 MB). | [`a6f2eaa`][a6f2eaa], [`6bab64a`][6bab64a] |
| Every c14n call allocated an 80 KiB buffer, and `Bytes` grew its result by doubling | The buffer is pooled (101 `Digest` calls on a tiny document 8.4 MB → 121 KB); `Bytes` and package-made node sets write into a buffer sized from the tree (about 1.1× the output, from 2.1–3.6×). | [`127b2f2`][127b2f2], [`5d6454a`][5d6454a] |
| A tag with many unique attribute names grew its slices, name table and duplicate map step by step | They are sized in one step past 256 attributes: 10 MiB of unique attributes 99.5× → 37.6× allocated. Small documents are unchanged. | [`22d32ac`][22d32ac] |
| Each namespace node rebuilt its element's whole scope to find its place; accumulators kept two maps per node | The slot is passed in (c14n over an XPath node set at 1 MiB 356 → 299 MB); accumulator values sit in slices indexed by `Node.Index` (about 27% less per node). | [`73249e2`][73249e2], [`b331aa1`][b331aa1] |
| A predicate step heap-allocated its comparison, and an ancestor step grew its result one node at a time | Both are cut: `ancestor-or-self::*[@Id='x']` per node 384 B / 9.75 allocations → 304 B / 7. | [`567aa1a`][567aa1a] |
| `xsl:sort`, `xsl:iterate`, `xsl:result-document`, built-in shallow-copy, namespace scopes and `on-empty` allocated per item | Smaller sort values in one array, parameter slices, set lookups, no focus copy without rules, frame walks, a lazy leaf walk. `result-document` is linear. | [`6ea432f`][6ea432f], [`a3d8ef6`][a3d8ef6], [`951f0d9`][951f0d9], [`f650a19`][f650a19], [`4b0b564`][4b0b564], [`469c95e`][469c95e] |
| DTD defaults were scanned per element; XSD key scopes rebuilt their entries per level | Defaults indexed by element (188 → 5 MB); scopes extend in place (960 levels 84 → 1 MB). | [`6293e98`][6293e98], [`996b7db`][996b7db] |
| Large regex programs were cached; `parse-json` objects and `serialize` refusals cost per key and item | Programs over 4,096 instructions are not cached; objects use `MapBuilder` (about 3.3× less); `serialize` stops at the first refusal. | [`331bc96`][331bc96], [`98811f6`][98811f6], [`6317d49`][6317d49] |
| DTD validation built a state-set map, a name slice and an attribute map per element | Small sets dedup by scan, the slice is reused, no ATTLIST skips the map: 12,062 → 8,062 allocations for 6,001 elements. | [`5dc120c`][5dc120c] |
| `xsdnet.HTTPResolver` built a new transport per fetch | One guarded transport is shared, so connections are reused; the SSRF guard is unchanged. | [`da05b16`][da05b16] |
| Typed copies pinned dropped schemas; `array:append` copied twice; line index, interning key and memo key kept slack | Weak typing-name slots, one copy, clipped index, reused key buffer, oversized memo buffer dropped. | [`967dfb3`][967dfb3], [`59ac569`][59ac569], [`3b71501`][3b71501], [`69414ae`][69414ae], [`887d5d3`][887d5d3] |

## v2.0.3 — 2026-10-30

An XPath cost fix from an external audit. No API change.

### Fixed

| Change | Problem → solution | Commit |
|---|---|---|
| `fn:normalize-space` built a list of every token | 180 calls on a 15 MB value allocated 133 GB. It now collapses in one pass and returns a collapsed value uncopied: 0 GB, 8x faster. | [`4264de8`][4264de8] |

### w3cschemas

| Change | What to do | Commit |
|---|---|---|
| `w3cschemas` v0.8.3 requires go-xml v2.0.3 | Nothing; the pin follows the release. | — |

## v2.0.2 — 2026-10-30

A parser cost fix from an external audit. No API change.

### Fixed

| Change | Problem → solution | Commit |
|---|---|---|
| Parsing was quadratic in the namespace declarations in scope | Each prefix scanned the whole stack: 100k declarations and 300k attributes took 33 s. The stack is indexed past 32 bindings: 0.35 s. | [`8b7aab2`][8b7aab2] |

## v2.0.1 — 2026-10-15

A security and release-gate update from an external audit of v2.0.0. No API
change. Build with a patched Go as well:
[docs/security.md](docs/security.md#build-with-a-patched-go) explains which
fixes ship in Go releases rather than here.

### Fixed

| Change | Problem → solution | Commit |
|---|---|---|
| `golang.org/x/text` v0.36.0 loops forever on some invalid input (GO-2026-5970) | Reached through collation. go-xml requires v0.41.0, the newest that still builds with Go 1.25. | [`18efb89`][18efb89] |
| `tests/check.sh fast` failed in a checkout without the W3C suites | The vendored-schemas lane ran its driver with no roots. It is now SKIP, "fixture roots absent". | [`27f463d`][27f463d] |

### Added

| Change | What it does | Commit |
|---|---|---|
| CI job `vulncheck` | Runs `govulncheck` over go-xml and `w3cschemas` on the newest Go. | [`18efb89`][18efb89] |
| `TestTrailingSlashSymlinkStaysConfined` in each confined resolver | Pins that a name ending in `/` cannot follow a symlink out of an `os.Root`, even on a Go without the GO-2026-4970 fix. | [`b8f35ea`][b8f35ea] |

### w3cschemas

| Change | What to do | Commit |
|---|---|---|
| `w3cschemas` v0.8.0 requires go-xml v2 | `Catalog` returns a v2 `*xsd.CatalogResolver`; import `github.com/knroy/go-xml/v2/xsd` with it. v0.7.x stays with go-xml v1. | [`b76c85d`][b76c85d] |

## v2.0.0 — 2026-10-14

v2 is a new major version with a new module path, `github.com/knroy/go-xml/v2`.
Nodes are rebuilt as 40-byte records and the XPath context is split, which
changes the exported Go API; the command-line tool's flags and output do not
change. [docs/migrating-to-v2.md](docs/migrating-to-v2.md) has before/after
code for every breaking change, and `internal/tools/nodeaccess` rewrites most
of it. v1 continues at `github.com/knroy/go-xml`, released from the `v1`
branch.

**Behaviour changes to check before upgrading.** Results, error codes and
serialized output are those of v1, except:

- `generate-id()` strings are record positions, so stored ids change.
- Nodes of different trees order by when each tree was numbered (a
  constructed fragment: when first compared).
- An XSLT global variable is evaluated on first use, so the failure of one
  nothing reads is not reported (XSLT 3.0 §2.14). One with `xsl:message`,
  `xsl:assert`, `xsl:result-document` or `fn:trace` in its own body is still
  evaluated at the start.
- During a transform, a Go-built function item or a context-taking resolver
  receives a copy of the context, not the caller's own.

### Changed — breaking

| Change | What to do | Commit |
|---|---|---|
| Module path `github.com/knroy/go-xml/v2` | Import `github.com/knroy/go-xml/v2/<package>`; `nodeaccess -v1` moves a module's imports. `w3cschemas` still requires v1. | — |
| `xdm.Node` fields are unexported | Call the method of the same name (`n.Name()`); range `Children()`, `Attrs()`, `NamespaceDecls()`; index with `ChildAt`, `AttrAt`. | [`a8e5346`][a8e5346] |
| Trees are built top-down by appending; a parsed tree is frozen | `AppendElement`, `AppendText`, `AppendAttr`, `AppendCopy` replace `SetParent`, `SetChildren`, `AppendChild`, `AddAttr`. | [`3f33dce`][3f33dce], [`f64b0d1`][f64b0d1] |
| `xdm.ProcessXInclude` returns a new tree | `tree, err = xdm.ProcessXInclude(tree, opts)`; the input is not edited. | [`fb2426e`][fb2426e] |
| `xdmbuild` relinking setters removed (`SetParent`, `SetChildren`, `ReplaceChild`, …) | Use the `xdm.Node` method of the same name, or build a changed copy; `Builder.ReplaceOpen` swaps in a typed copy. | [`6b34bc3`][6b34bc3], [`13c4f5f`][13c4f5f] |
| `xpath.Context` per-evaluation fields move to `xpath.Env` | `ctx.Docs = r` → `ctx.WithEnv(func(e *xpath.Env) { e.Docs = r })`; read `ctx.Env().Docs`. | [`9af0e77`][9af0e77] |
| `xpath.Context` static fields become methods; `WithNow` removed | `ctx.Version` → `ctx.Version()`, set with `WithVersion`; `WithNow(t)` → `WithEnv` setting `Now` and `HasNow`. | [`9af0e77`][9af0e77] |
| `Context.StaticNamespaces` is set only for an expression that reads it | A host function resolves prefixes against a resolver it captured itself. | [`d5a980b`][d5a980b] |
| `Context.Position`, `Size` and `Depth` are `int32` | Convert where an `int` is mixed in; `WithFocus` keeps its `int` parameters. | [`7f6f168`][7f6f168] |
| `LazyVar.Share` removed; `Context.WithLazyVars` takes an `*xpath.EvalLock` | Pass the lock (nil for a fresh one) and call `lock.Share()` where `LazyVar.Share()` was. | [`fbd4b74`][fbd4b74] |
| `xsd.HTTPResolver` moves to `xsd/xsdnet`; build tag `goxml_nohttp` removed | Use `xsdnet.HTTPResolver`; importing `xsd` no longer links `net/http`. | [`5509745`][5509745] |
| `xsd.ValidateOptions.Annotate` removed | `Validate` only checks; take a typed tree from `ValidateCopy` or another `...Copy` function. | [`21105fe`][21105fe] |

### Added

| Change | What it does | Commit |
|---|---|---|
| `xdm.NewFragment`, `Tree.NewRoot`, `Copy`, `CopyPruned`, `ShallowCopy`, `NamespaceNodes`, … | The construction and reading calls the record layout is built on. | [`3f33dce`][3f33dce], [`86f991c`][86f991c], [`d337fd9`][d337fd9] |
| `xdm.Node.TreeHasTyping`, `SetAssessedTyping`, `DescendantsInheritBase` | Whether a tree was ever typed; write a node's typing in one call; whether a subtree sets its own base URI. | [`40dbca2`][40dbca2], [`8faf349`][8faf349], [`ae36039`][ae36039] |
| `xdmbuild.Builder.AppendCopyOf`, `NSDecl`, `NoteDeclaredList` | Copy a node straight into the tree being built; note a list of namespace bindings once. | [`484cb4e`][484cb4e], [`2c5ea0b`][2c5ea0b] |
| `xpath.LazyVar`, `ReadyVar`, `Context.WithLazyVars`, `WithStaticHost` | Variables evaluated on first reference, forced once from any goroutine once shared; a static host for expressions compiled without one. | [`7c5121c`][7c5121c], [`d5a980b`][d5a980b] |
| `xpath.EvalLock`, `xpath.EvalLocker` | A re-entrant lock a host runtime shares once other goroutines can reach it; free until shared. | [`fbd4b74`][fbd4b74], [`4954bf0`][4954bf0] |
| `go-xml xquery -validate strict\|lax`, `xquery.Query.Schema` | Validate the input against the schema the query imports before it runs. Also in v1.8.0. | [`130360b`][130360b] |
| `tests/record.sh`, `tests/recdiff` | Record every suite case's output and diff two checkouts, with allowed differences by rule. | [`5aa004e`][5aa004e] |
| `docs/benchmark.md` v1 against v2; `docs/profiling.md` | The benchmark with both versions against the reference engines; where v2's time goes and how to measure a change. | [`dcf9d53`][dcf9d53] |

### Fixed

| Change | Problem → solution | Commit |
|---|---|---|
| A function item returned by a transform raced when called from several goroutines | `key()`, accumulators and memoised functions wrote shared maps. One re-entrant lock per transform, also around Go extension calls and `fn:transform`. | [`fbd4b74`][fbd4b74], [`4954bf0`][4954bf0] |
| A called template or function could read a caller's local variable | It happened where a global of the same name existed. Locals now live in frames that each call raises. | [`a0250dd`][a0250dd] |
| go-xml did not compile for 32-bit targets | The `xs:date` year bound overflowed `int`, and array positions past 2^31 could wrap. CI now vets `linux/386`, `linux/arm`, `windows/386`. Also in v1.8.0. | [`4f6ed50`][4f6ed50] |
| `docs/xquery.md` said `import schema ... at` is not resolved from the command line | It is, through the query's directory, `-allow-dir` roots and `-catalog`. | — |

### Changed — performance

Ratios are warm time over the reference engine from
[docs/benchmark.md](docs/benchmark.md), v1 at `f45068c` against v2.0.0.

| Change | Problem → solution | Commit |
|---|---|---|
| A parsed node was a 280-byte struct of 16 pointers | 40-byte records in per-tree chunks. A 100 MB parse peaks at 371 MB (v1 2.2 GB); parse 0.54× → 0.46× `encoding/xml`. | [`a81dff2`][a81dff2], [`b08a3d4`][b08a3d4], [`2dd09c0`][2dd09c0] |
| Every scope change copied a 512-byte `xpath.Context` | A 96-byte scope over a shared `Env` and a static part; `Compiled.Eval` swaps a pointer. | [`9af0e77`][9af0e77], [`7f6f168`][7f6f168] |
| XSLT evaluated every global eagerly and gave each local and call a context copy | Lazy globals, one locals stack per transform, absent-component bits, int64 arithmetic. DocBook 0.46× → 0.20× Saxon. | [`7c5121c`][7c5121c], [`a0250dd`][a0250dd], [`1e976a3`][1e976a3], [`496f46a`][496f46a] |
| XQuery copied constructed content twice, and `//name` walked the tree each time | Content is copied once into the builder; a document indexes its element names on first use. XMark 1.00× → 0.77× Saxon. | [`484cb4e`][484cb4e], [`babd534`][babd534] |
| Typed validation copied the tree twice and wrote 80 bytes of typing per node | The source is validated read-only and copied once; typing shares its names. XSD 0.73× → 0.61× Xerces. | [`d3f4824`][d3f4824], [`8e29a38`][8e29a38], [`3208c54`][3208c54] |
| RELAX NG re-walked derivatives per element and hashed names through `sync.Map` | Sizes stored once, typed memos, reused buffers: −23 to −30% CPU, long documents −84% allocations. | [`ae4f4d1`][ae4f4d1], [`ea89438`][ea89438], [`fb7a15b`][fb7a15b] |
| C14N and the serializer paid a call or a defer per token and element | C14N writes to its own buffer; the serializer has no defers. C14N −10 to −12% CPU. | [`63155c7`][63155c7], [`da27a7c`][da27a7c] |
| Package init built large tables and compiled regexps on every run | Built on first use: init allocations 1,893 → 785, cold XSD validation −3 to −4%. `default.pgo` regenerated for v2. | [`c249ce3`][c249ce3], [`47db4aa`][47db4aa], [`c5184cb`][c5184cb] |

## v1.8.0 — 2026-10-10

### Added

| Change | What it does | Commit |
|---|---|---|
| `go-xml xquery -validate strict\|lax` | Validates the input document against the schema the query imports before it runs, so a declared typed context item (`document-node(schema-element(...))`) can be satisfied from the command line. | [`130360b`][130360b] |
| `xquery.Query.Schema` | The schema merged from a query's `import schema` declarations, or nil; mirrors `xslt.Stylesheet.Schema`. | [`130360b`][130360b] |

### Fixed

| Change | Problem → solution | Commit |
|---|---|---|
| go-xml did not compile for 32-bit targets, and an array position past 2^31 could wrap there | The `xs:date` year bound is compared as int64; `maxArrayIndex` is capped at a quarter of `math.MaxInt`. CI and the gate vet `linux/386`, `linux/arm` and `windows/386`. | [`50ccc85`][50ccc85] |

### Fixed — documentation

| Change | Problem → solution | Commit |
|---|---|---|
| `docs/xquery.md` said `import schema ... at` is not resolved from the command line | It is, through the query's directory and `-allow-dir` roots (and `-catalog`); the sentence is corrected. | — |

## v1.7.1 — 2026-10-09

v1.7.1 is v1.7.0 with a fix to the release workflow and nothing else. The
v1.7.0 tag and module were published, but its release stopped before the
gate ran and no GitHub release was made; the changes are listed under v1.7.0
below. `w3cschemas/v0.6.0` pins go-xml v1.7.0, which is identical in code.

### Fixed — release process

| Change | Problem → solution | Commit |
|---|---|---|
| The v1.7.0 release stopped at its changelog check | A row quoting a sample link was read as an undefined reference; code spans are ignored when collecting link labels. | [`cb604e9`][cb604e9] |

## v1.7.0 — 2026-10-09

**Behaviour changes to check before upgrading.** The exported API only
gains names, but three things you may observe change:

- `generate-id()` strings are now `N<tree>x<order>`. The old form collided
  between trees with more than 2^20 nodes; the spec leaves the format to the
  implementation, so code that stored generated ids will see new values.
- `xsl:next-match` and `xsl:apply-imports` inside `xsl:iterate`,
  `xsl:merge`, `xsl:sort` or `xsl:copy select` now raise `XTDE0560`, as XSLT
  3.0 §6.8 requires; they used to run the next rule. (Saxon 12.10 still runs
  it inside `xsl:merge-action`.)
- Several static and validation error messages changed wording or order
  (for example, XTSE0720 now shows the cycle). They are now the same on every
  run; before, Go's map order could pick a different message.

### Added

| Change | What it does | Commit |
|---|---|---|
| `xsd.Schema.ValidateCopy`, `ValidateCopyContext` | Validate into a typed copy and leave the input untouched, so one tree can be validated from several goroutines. | [`ec72376`][ec72376] |
| `xdmbuild` node constructors and setters (`NewElement`, `NewAttribute`, `ShallowCopy`, `DeepCopyPruned`, …) | Every package builds and edits trees through `xdmbuild` instead of node literals, the groundwork for v2. | [`655311a`][655311a] |
| `xdm` benchmarks | Navigation, corpus parse cost, retained heap per input byte and tree shape, the yardstick for the node redesign. | [`a5b4546`][a5b4546] |
| `xpath.Context.MaxItems`, `xslt.TransformOptions.MaxItems`, CLI `-max-items` | The item budget is configurable (0 = default 5,000,000, negative = none); a nested evaluation cannot raise it. | [`3727b35`][3727b35] |
| CLI `-max-bytes` | Lifts or sets the input document size limit on the transform and `xquery` (0 = default 64 MB, negative = none). | [`3727b35`][3727b35] |
| Build tag `goxml_nohttp` | Leaves `xsd.HTTPResolver` and `net/http` out: CLI start-up −1.6 ms, RSS −4.8 MB, binary −12%; the default build is unchanged. | [`dccd679`][dccd679] |
| `-catalog DIR` on `validate`, the transform and `xquery` | Answers references to the W3C schemas (`XMLSchema.xsd`, `xml.xsd`) from local copies, so a schema importing them by `www.w3.org` URL loads from the command line. | [`39c6931`][39c6931] |
| `xquery.Options.SchemaParseOptions` | Parser options for `import schema` documents, as `xslt.CompileOptions` already had; the zero value still refuses a DOCTYPE. | [`740c22a`][740c22a] |
| `xsd.ParseDocument`, `xsd.RootedFileResolver` | Parse a resolved schema document under the catalog's DOCTYPE rule; build the confined default resolver to use as a catalog's fallback. | [`d86f80c`][d86f80c] |
| `docs/profiling.md` | Three profiling rounds: the causes behind the benchmark's losses, each fix with its measured gain, what was rejected and why, and what is left for v2. | [`007e682`][007e682] |
| `docs/benchmark.md` re-run | Every engine re-measured at `eb14939`, after the performance work, and again at `f45068c` after rounds 3 and 4, with the previous run's figures alongside. | [`3fc468a`][3fc468a] |
| `docs/benchmark.md` | go-xml against Saxon-HE, BaseX, Jing, Xerces-J, libxml2 and `encoding/xml` on nine real workloads, cold and warm, timed only where outputs agree. | [`416ee50`][416ee50] |

### Fixed — engine

| Change | Problem → solution | Commit |
|---|---|---|
| A `version="2.0"` stylesheet was refused `match="root()"` and other 3.0 pattern forms | XSLT 3.0 §3.9.2 defines no 2.0 behaviour for a 3.0 processor; the 3.0 pattern grammar now applies, and the refusal stays for `MaxVersion` 2.0. Found by the Peppol BIS validators. | [`ca09e14`][ca09e14] |
| A `version="2.0"` stylesheet with `xsl:import` after other declarations was `XTSE0200` | XSLT 3.0 removed that rule; it now applies only to a 2.0 processor. Found by KoSIT's XRechnung HTML stylesheet. | [`04ddeea`][04ddeea] |
| RELAX NG recompiled a definition at every `<ref>` to it | Cost multiplied along chains, so DocBook 5.2 hit the 200,000-expansion limit. Each definition is compiled once and shared; DocBook compiles and validates in 0.55 s. | [`eb6901e`][eb6901e] |
| `indent` broke Serialization 3.1 §5.1.4 | Typed simple and mixed content was indented, changing a validated `xs:string` value; `fn:serialize` also ignored `xml:space` and nested `suppress-indentation`. All §5.1.4 constraints now hold in both serializers. | [`cec5f6f`][cec5f6f] |
| RELAX NG recompiled a recursive `<ref>`'s definition about 45 times | Lazy sub-compilers now share compiled definitions; DocBook 5.2 compiles in ~20 ms instead of 0.5 s. | [`c7769f5`][c7769f5] |
| RELAX NG matched whitespace between element children as text | Six valid DocBook documents were rejected; whitespace-only text among elements is now stripped (§6.2.7). | [`b44313c`][b44313c] |
| RELAX NG `choice` kept duplicate alternatives | Nested `oneOrMore` doubled the derivative per child and hit the size bound on DocBook `xref.001`; equal alternatives are merged. | [`d8f0ac1`][d8f0ac1] |
| RELAX NG `<include ns>` leaked into definitions reached through `<ref>`, `combine` and nested grammars | Each definition now compiles under the ns of the document it was written in (§4.8). | [`c9c7c79`][c9c7c79] |
| RELAX NG read an explicit `ns=""` as absent | An inherited ns overrode it in names, `nsName` and QName values; presence is now checked (§4.8). | [`5cf8104`][5cf8104] |
| Text split by many CDATA sections was joined piece by piece | Parsing was quadratic in the number of pieces (200k sections took 4.4 s); each text node is now joined once (30 ms). | [`0604061`][0604061] |
| XSD identity-constraint scopes on local and group declarations were missed | A recursive key re-reported each failure once per nesting level; scopes are now tracked on the validation walk. | [`1f80934`][1f80934] |
| `fn:transform` ignored a mistyped option | Every typed option now follows the F&O §1.5.4 conversion rules: arrays atomize, a string for an `xs:QName` is `XPTY0004`. | [`84bbe23`][84bbe23] |
| Nested `fn:transform` ran `xsl:assert` by default and ignored `enable-messages` | `enable-assertions` (default false) and `enable-messages=false` now apply; `terminate="yes"` still ends the transform. | [`a2603d3`][a2603d3] |
| Attribute values after a quote or `[` in a DTD comment were left unnormalised | The byte pre-pass lost its place in the internal subset; the tokenizer now normalises attribute values, and line numbers after a multi-line attribute are right. | [`d212f7a`][d212f7a] |
| In XML 1.1 a NEL, U+2028 or CR NEL in an attribute value became a newline | Each is now one space (§2.11, §3.3.3); 1.0 documents are unchanged. | [`e03ee3d`][e03ee3d] |
| Entity replacement text in an attribute value kept its tabs and newlines | §3.3.3 now applies on both entity paths; characters written as references are kept. | [`b7853d6`][b7853d6] |
| A PI with a quote or `>` in the internal subset broke the DOCTYPE | The scanner now skips PIs whole, as it does comments. | [`b707e39`][b707e39] |
| An `<!ENTITY>`, `<!ATTLIST>` or `<!ELEMENT>` inside a PI or comment in the internal subset was acted on | One declaration scanner now skips PIs, comments and quoted text; a default holding `>` is no longer cut short. | [`efd4178`][efd4178] |
| DTD attribute defaults were used as written | References now expand and white space is normalised (§3.3.2, §3.3.3); a `<`, bare `&` or external entity in one is a parse error. | [`02905e9`][02905e9] |
| Line ends in the DOCTYPE were not normalised | CR folds always, and NEL, U+2028 and CR NEL under XML 1.1, as in content (§2.11). | [`0ca7dac`][0ca7dac] |
| Character references in DTD entity values, replacement text and defaults were not checked | WFC Legal Character now applies there and to parameter-entity values, using the tokenizer's rule for each version. | [`81b0a27`][81b0a27] |
| In XML 1.1 a NEL, U+2028 or CR NEL in a comment or PI stayed as written | Comments and PIs fold line ends by the document's version, so a 1.0 CR NEL keeps its NEL and loses its CR. | [`17b99c3`][17b99c3] |
| An undeclared entity was always fatal | It is fatal only where §4.1's WFC applies; after an unread external subset or `%pe;` the reference is dropped, as libxml2 does in attribute values. | [`3d7e86b`][3d7e86b] |
| A comment with `--` or ending `--->` in the internal or an external subset was accepted | It is refused with the same error as in content (§2.5 [15]). | [`97148b4`][97148b4] |
| Entity and ATTLIST declarations after an unread `%pe;` were processed | Per §5.1 they are ignored unless the document is `standalone="yes"`. | [`7496ea8`][7496ea8] |
| A `CatalogResolver` holding the W3C schema for schemas needed `AllowDOCTYPE` for every schema document | A document the catalog answers from its own table may carry a DOCTYPE; documents from the fallback keep the caller's options. | [`d86f80c`][d86f80c] |
| `xsl:import-schema` ignored `SchemaParseOptions` on the located document and on a namespace-only import | Every path, including the schemas' own includes and imports, now uses them. | [`2402bca`][2402bca] |
| A namespace-only `xsl:import-schema` read its schema as XSD 1.0 | It is read as XSD 1.1 and honours `vc:minVersion`, like the location and inline paths. | [`987e24e`][987e24e] |
| `-catalog` read a schema's sibling `XMLSchema.xsd` before the catalog's | The W3C's own layout failed on the sibling's DOCTYPE; the catalog now answers every spelling of the files it holds. | [`8aa5ab7`][8aa5ab7] |
| A pattern predicate made numeric by a function call, as in `item[number(@n)]`, ran at position 1 | It is positional unless statically boolean, string or node-valued (XSLT 3.0 §5.5.3), as Saxon answers. | [`0602694`][0602694] |
| A FLWOR join charged its inner sequence once per outer tuple against `MaxItems` | It charges what it holds, so XMark q8–q10 at factor 1 no longer fail with `XPDY0130`. | [`2514a9a`][2514a9a] |
| With `AllowDOCTYPE` and no DOCTYPE, the parser kept two extra copies of the document | They are dropped when the root opens: 10 MB parse 335 → 230 MB allocated, 100 MB peak RSS 3.15 → 2.0 GB. | [`b88105e`][b88105e] |
| The html method dropped the stylesheet's own `<meta charset>` under `include-content-type="no"` | It is dropped only when the method adds its own (§7.4.13); XRechnung's HTML stage now matches Saxon. | [`262be91`][262be91] |
| html/xhtml `indent="yes"` split inline elements onto separate lines | No whitespace is added next to an inline element (§7.4.3, §6.1.4), in `xsl:output` and `fn:serialize`. | [`4457808`][4457808] |
| `xsd.Schema.Validate` without `Annotate` still wrote the union member, `nilled` and a lax-wildcard restore to the caller's tree, racing concurrent validation | Those writes need `Annotate`; a check-only run keeps them for that run, so assertion verdicts are unchanged. | [`5c2ca9c`][5c2ca9c] |
| XSD `mg-props-correct.2` and `src-attribute_group.3` named a group that only refers into another group's cycle, and which one varied per run | Strongly-connected components: every group on a cycle is reported, by name, and only those. | [`373da4b`][373da4b] |
| Map order chose which error was reported: XTSE0720 (which now shows the cycle), XTSE0020/0730, XTSE0010, XTSE0545, XTSE3350, XTSE3430, XTSE3070, XTSE3055/0770, XTTE0590, XTTE2230 | Each check walks declaration, document or sorted-name order, so one stylesheet gives one error. | [`8082a2c`][8082a2c] |
| Map order chose the reported XSD error: duplicate IDs (`cvc-id.2`), duplicate keys from nested scopes, an `xs:all` restriction's disallowed element; RELAX NG include overrides and ref cycles | Document order, or the first name in sorted order; found by the v2 output differential. | [`edea87c`][edea87c] |
| `generate-id` was `"N"` plus `tree·2^20 + order`, so a tree with over 2^20 nodes collided with the next tree | Ids are `N<tree>x<order>`, distinct for any size. | [`3f87498`][3f87498] |
| An element copied into new content by `xsl:sequence` lost the namespaces it inherited; a temporary tree's namespace nodes shared generate-ids | Both now follow XSLT 3.0 §5.7.1, and order slots are reserved for in-scope bindings. | [`77c78e9`][77c78e9] |
| `xsl:next-match` and `xsl:apply-imports` inside `xsl:iterate`, `xsl:merge`, `xsl:sort` or `xsl:copy select` ran the next rule | The current template rule is cleared there, so they raise `XTDE0560` as XSLT 3.0 §6.8 requires. | [`4d31869`][4d31869] |
| `xsl:decimal-format` read an empty `NaN`/`infinity` as absent and ignored an empty single-character attribute | Presence decides; an empty single character is `XTSE0020`. | [`1b65291`][1b65291] |
| `fn:serialize` with `include-content-type` kept the document's content-type meta, added none under xhtml, and ignored `media-type` | It replaces the head's meta, adds one under xhtml, and writes the media type, sharing `xsl:output`'s rules. | [`1e2bad6`][1e2bad6] |
| The html method dropped content-type metas anywhere inside `<head>` | Only direct children of `head` are replaced, as §7.4.13 says. | [`91beb30`][91beb30] |
| XSD 1.1: a wildcard before an element in a repeated choice spent the repeat bound, so bad `a` values passed | The element declaration is tried first; 45 of 45 cases now agree with Xerces. | [`5912a5a`][5912a5a] |
| RELAX NG `<data type="QName">` accepted undeclared prefixes and length params | Prefixes resolve against in-scope namespaces and length params are refused, as Jing does. | [`81d16d5`][81d16d5] |
| A function body expanded a prefixed `format-date` `$calendar` against the caller's namespaces | It uses its own; a false `FOFD1340` is gone. | [`8a792f7`][8a792f7] |
| Under `goxml_nohttp`, refusals still pointed at `HTTPResolver` | They name only the resolvers the build has. | [`6f63564`][6f63564] |
| Compact syntax refused a free-standing annotation element among definitions | DocBook's `s:ns [ ... ]` was read as a datatype name. The grammar allows it; it is now skipped like any annotation. | [`197eaad`][197eaad] |

### Changed — performance

From [docs/profiling.md](docs/profiling.md). Outputs are byte-identical on every benchmark workload and every conformance suite is unchanged.

| Change | Problem → solution | Commit |
|---|---|---|
| Template patterns bound `current()` before testing the node | Each failed candidate cost two context copies; a pattern now rejects on its node test first (DocBook ~3× less CPU). | [`262366d`][262366d] |
| Every function call formatted a `{uri}local#arity` string key | The function library is keyed by a struct. | [`9bd1c79`][9bd1c79] |
| A path step copied the evaluation context per input node | Plain axis steps get the context node directly (Peppol CEN 37 → 9 ms per invoice with the two above). | [`4f0cc55`][4f0cc55] |
| `SortDocumentOrder` sorted step results already in order | A one-tree, strictly increasing sequence is returned as it is. | [`1c7fb99`][1c7fb99] |
| General comparisons built an operator map on every evaluation | The operator maps to its value comparison through a switch. | [`6ce285f`][6ce285f] |
| `fn:transform` recompiled its stylesheet on every call | Compiled stylesheets are kept in a bounded per-stylesheet LRU keyed on every compile input; `cache=false` bypasses it. | [`e00735a`][e00735a] |
| XQuery `for … where A op B` value joins ran as nested loops | They run as a hash or cached-key join when every call resolves to a built-in, falling back on any error (XMark q8 3.3 s → 14 ms). | [`2aa49d8`][2aa49d8] |
| XSD parsed every `xs:integer` into a `big.Rat` for bounds and digit facets | Bounds are skipped without bound facets and digits are counted from the lexical form. | [`62c7117`][62c7117] |
| XSD kept identity-constraint bookkeeping for every node | It is kept only inside a constraint's scope; with the collapse, primitive and `StringValue` fast paths, `xp-striding` validates 3.4× faster. | [`949e0fa`][949e0fa] |
| Every parsed node was its own allocation | Nodes come from per-parse chunks, and rarely set fields sit behind a lazy pointer (node 296 → 280 B). | [`bcae9bd`][bcae9bd] |
| Parse allocated every token, name, attribute slice and whitespace text | The tokenizer reuses tokens and buffers and interns names; a 10 MB parse makes 70% fewer allocations and runs 34% faster. | [`446432b`][446432b] |
| Every variable binding built a one-entry map | `WithVar` stores its binding inline (DocBook allocations −27%). | [`8cfeef4`][8cfeef4] |
| Every built-in call copied the context to count call depth | Leaf built-ins that cannot re-enter user code count it in place; limits and errors are unchanged. | [`569af46`][569af46] |
| A focus-free comparison operand in a predicate was re-evaluated per item | It is evaluated once per predicate, falling back per item on error (DocBook `indexterm.001` −32% CPU). | [`1253f9a`][1253f9a] |
| Template dispatch tried every rule in the mode per node | Rules are indexed by mode, node kind and name in linear-scan order (≈90 → 2 patterns per node on Schematron). | [`c008a63`][c008a63] |
| Attribute normalisation was a byte-by-byte pass over the whole document | The tokenizer does it (10 MB parse CPU −29%). | [`d212f7a`][d212f7a] |
| The duplicate-attribute check and child/attribute slice growth allocated per attribute and per child | Keys are compared without allocation and slices are cut exact-size from shared arrays; a 10 MB parse makes 371k allocations, down from 1.13 M. | [`b764d67`][b764d67] |
| Every start tag built two maps of the namespaces in scope | Prefixes are looked up directly; `docbook.rng` parses in half the time. | [`549d8db`][549d8db] |
| Text, attribute values and comments were one allocation each | They share arena blocks; a 10 MB parse makes 7k allocations instead of 371k. | [`aa7d1e2`][aa7d1e2] |
| The position-tracking source copy grew by doubling; a US-ASCII document was copied before being checked | The copy is sized once and US-ASCII is checked as it streams. | [`8fb6f0b`][8fb6f0b] |
| DTD attribute typing built `prefix:local` per declaration per element | Names are compared in place; the XSD 1.1 schema for schemas loads 30% faster. | [`68795ba`][68795ba] |
| Stylesheet compile rebuilt each element's in-scope namespaces | One map per declaring element is shared: DocBook compile bytes −30%, CEN −25%. | [`ebd1759`][ebd1759] |
| XSLT calls cleared context that was already absent; `current()` copied the context | Both are skipped: warm bytes CEN −16%, PEPPOL −23%, DocBook −16%. | [`b0c7b30`][b0c7b30] |
| `//name` built and sorted every node below the context first | It is evaluated as `descendant::name` when neither step has a predicate (XMark q7 3.6× faster). | [`3cd86ee`][3cd86ee] |
| The XQuery join cast the outer key and scanned every inner key per tuple | It casts once and range-searches sorted doubles for `< <= > >=` (q11 188 → 30 ms). | [`ba37f7c`][ba37f7c] |
| `xslt.Serialize` wrote to files one system call per token | Output to a writer not in memory is buffered, with identical bytes. | [`a931b98`][a931b98] |
| RELAX NG rebuilt the pattern on every start-tag close | It returns its input when nothing changed: DocBook validation 2.3× faster. | [`7ad91bd`][7ad91bd] |
| XSD re-listed a directory per include and counted characters with no length facet | Locations are cached per load and the count is skipped. | [`3b06e4c`][3b06e4c] |
| C14N escaped with a per-byte switch into a 4 KiB buffer | Lookup tables and a 64 KiB buffer: 10 MB canonicalisation 22 → 17 ms. | [`8e63b9d`][8e63b9d] |
| The CLI spent about half its cold CPU on GC while the heap only grew | It runs at GOGC=200 unless `GOGC` is set: DocBook cold CPU −33%, peak RSS 114 → 161 MB. | [`5fbea36`][5fbea36] |
| Every call re-resolved its function and every transform rebuilt its runtime library | Call sites cache the resolution and each stylesheet builds one library (XRechnung CPU −26%). | [`87cf96e`][87cf96e] |
| `key()`, `current()` and stylesheet functions walked the variable chain for XSLT state | It rides on one context field; with options held by pointer, DocBook CPU −27%. | [`2a7843f`][2a7843f] |
| Repeated `fn:doc` calls re-resolved the path; `xsl:evaluate` re-parsed its target | Per-transform `doc()` cache; up to 64 compiled expressions kept per instruction. | [`72cb46a`][72cb46a] |
| Namespace fixup built a scope map per constructed element; node ordering built one per comparison | Bindings are read live, and the namespace base is skipped for non-namespace nodes (CEN −3 to −5%). | [`53cff35`][53cff35] |
| `descendant::name` went through a closure and an interface call per node | It walks elements directly with the test inlined (XMark q6/q7 eval −23 to −30%). | [`659fdc8`][659fdc8] |
| The parser numbered nodes in a second walk, looked whitespace up in a map, and checked characters one byte at a time | Nodes are numbered while built, the last run per length is cached, and ASCII is checked eight bytes at a time. | [`e7ec81d`][e7ec81d] |
| The CLI held two copies of each input during the parse | It parses from the file; XMark peak RSS −11 MB. | [`739c744`][739c744] |
| XSD validation allocated a count vector per child and a slice per element | Buffers are reused: 101,505 → 341 allocations per catalog pass. | [`d006ad2`][d006ad2] |
| RELAX NG re-derived the schema's fixed subtrees per element and built a namespace map per node | Memo points, a lazy namespace context and an attribute memo: DocBook validation −82% CPU. | [`abd214b`][abd214b] |
| The CLI was built without profile-guided optimisation | `cmd/go-xml/default.pgo` is committed (regenerate with `tests/pgo.sh`): CPU −2 to −3% cold, −3 to −5% warm. | [`b83418a`][b83418a] |
| `//@a` built and sorted every node below the context before reading attributes | It is one walk over a parsed tree (CEN CPU −5 to −8%). | [`06c0df1`][06c0df1] |
| Both collation language matchers were built at start-up on every run | They are built on first use (CLI cold start about −0.15 ms). | [`18ef0f0`][18ef0f0] |
| The html serializer allocated twice per attribute character | Printable ASCII is copied through: XRechnung HTML allocations −53%, CPU −14%. | [`eac3823`][eac3823] |
| The serializer lower-cased the encoding per non-ASCII character and built names and a namespace map per element | One binding stack, pieces written: XRechnung HTML allocations −57% in all. | [`aefdaea`][aefdaea] |
| A path step copied its own result into the path's accumulator, and every relative path boxed the context item | The result is kept and the item stays on the stack: CEN allocations −11%, CPU −4 to −7%. | [`d077c5a`][d077c5a] |
| `//x[p]` from a document root re-walked the whole document on every evaluation (~38% of CEN) | XSLT remembers per parsed document which nodes have an `x` child: CEN CPU −18 to −23%. | [`b5864c4`][b5864c4] |
| `name(.) = name(current())` built, boxed and atomized two names per node | Name comparisons match prefix and local name directly: XRechnung allocations −53%, CPU −20%. | [`738c1fb`][738c1fb] |
| Every comparison and boolean built-in allocated a fresh `xs:boolean` | Two shared values are returned: allocations −6 to −12%, CPU −5 to −7.5% on Schematron. | [`7dce099`][7dce099] |
| Each serialized text node and escaped attribute value was built in a builder of its own | Both are written in runs straight to the buffered writer (XMark q2+q10 allocations −6.9%). | [`ca3e6a1`][ca3e6a1] |
| Literal result elements and `xsl:copy` repeated every in-scope namespace on each element (86% of SVRL nodes) | A binding the parent already has is inherited: CEN allocations −14%, CPU −15%; Peppol −8%. | [`77c78e9`][77c78e9] |
| Builders kept a map of declared prefixes per element; copies built a map of in-scope namespaces per element | A small slice, and a direct read of the ancestors. | [`b13e8f4`][b13e8f4] |
| Parse-time namespace nodes and PIs were two allocations each | They come from the node chunk: parse allocations −10% on stylesheets. | [`092bdce`][092bdce] |
| RELAX NG re-derived every element of a long document from scratch | Patterns are interned and derivatives remembered past 1,000 elements: 1.1 MB table −32% CPU, −76% allocations. | [`77cdd65`][77cdd65] |

### Fixed — release process

| Change | Problem → solution | Commit |
|---|---|---|
| The release workflow never released `w3cschemas` | `actions/checkout` refetches the pushed tag as a lightweight ref, so the tag message's `w3cschemas: vX.Y.Z` line read as empty and the step skipped; v0.4.0 and v0.5.0 were tagged by hand. The tag object is now fetched before it is read. | [`549d113`][549d113] |
| Release notes printed the commit links as `[`abc1234`][abc1234]` | The body is the CHANGELOG section alone, and the link definitions sit at the file's foot. The section's definitions are now appended to the body. | [`78c47fb`][78c47fb] |

## v1.6.0 — 2026-10-07

### Added

| Change | What it does | Commit |
|---|---|---|
| RELAX NG compact syntax from the CLI and in includes | `go-xml validate -rng` read every schema as XML, and `FileResolver` parsed every include as XML, so a `.rnc` schema failed from the CLI and a modular one failed everywhere. Both now read either syntax, decided by content. | [`6e13dee`][6e13dee] |
| Schema-aware processing from the CLI | `xsl:import-schema` and `import schema ... at` had no resolver in `go-xml`, so they failed to compile. Both now read through the confined resolver (`-allow-dir`), and `-validate strict\|lax` types the source so it atomises to schema types. | [`4658352`][4658352] |

### Fixed — engine

| Change | Problem → solution | Commit |
|---|---|---|
| `html` and `xhtml` output was never indented by default (#17) | XSLT 3.0 §26 defaults `indent` to `yes` for both methods, including when an `<html>` root selects the method. A stylesheet's unstated `indent` now follows the method; XQuery's default stays `no`. | [`701239a`][701239a] |
| A `version="3.0"` stylesheet ignored misplaced top-level XSLT elements | Forwards-compatible leniency applied to any version above 2.0; XSLT 3.0 §3.9 grants it only above the processor's. Now `XTSE0010`. | [`abb33c7`][abb33c7] |
| The XSLT element table accepted values the syntax summaries forbid | `hidden` on `xsl:expose`, `xsl:expose` outside a package, any `method`/`data-type`, bad standard attributes, `xsl:package` without `version`. Now refused; a test diffs the table against the Recommendation. | [`d758fa2`][d758fa2] |
| `xs:yearMonthDuration` / `xs:dayTimeDuration` accepted any duration in XSD validation | `P1D` was a valid year-month duration. Both now carry the Part 2 pattern facets, in values and facet values alike. | [`a0d6a58`][a0d6a58] |
| `whiteSpace fixed="true"` was never enforced | A restriction could override it. Enforced now; `xs:string`, `normalizedString` and `token` no longer mark it fixed, matching the schema for schemas. | [`eec3464`][eec3464] |
| XSD 1.0 mode honoured some 1.1 constructs and accepted 1.1-only names | The assertion facet, `explicitTimezone` and `defaultAttributes` are now ignored under 1.0, `notNamespace` and `xs:dateTimeStamp`/`xs:error` refused; see docs/xsd.md for the one leniency kept. | [`5a20c7b`][5a20c7b] |
| Schema documents the schema for schemas forbids were loaded | Facet attributes, top-level element `minOccurs`/`ref`/`form`, `xs:import` after declarations and five more shapes are now refused. | [`5eb354f`][5eb354f] |
| Serialization settings from a parameter document or `declare option output:*` were not validated | `standalone="maybe"` reached the XML declaration. Values are now checked against the Serialization 3.1 schema (`SEPM0016`/`SEPM0017`); a repeat is `SEPM0019`. | [`fee47ab`][fee47ab] |
| A parameter document replaced `cdata-section-elements` and `suppress-indentation` | XSLT 3.0 §26.1 merges them with `xsl:output`'s lists; they now merge. | [`448683d`][448683d] |
| RELAX NG compact `string` and `token` were not keywords | `{ string }` became a reference to an undefined pattern. Both are built-in datatypes now, and every compact keyword is reserved. | [`4d90e69`][4d90e69] |
| A RELAX NG nested `<grammar>` inside `<define>` was refused under §7.1.5 | Its `start` was taken for the schema's own. It now stands where the grammar does, as §4.18 says; `schema-for-xslt30.rnc` compiles. | [`27f1945`][27f1945] |
| The RELAX NG checker accepted shapes the §3 grammar forbids | `start` with two patterns, `anyName`/`nsName` with non-`except` children, patterns in `include`, `div` as a pattern, `param` after `except`, a bad `combine`. Now refused. | [`4df1399`][4df1399] |
| A RELAX NG include resolved against a Windows path base became `c:///…` | `url.Parse` read the drive letter as a scheme, so the include was refused as remote. A drive-path base is now joined as a path. | [`d3ce46f`][d3ce46f] |
| `fn:element-with-id` was unavailable at XPath 2.0 | F&O 2.0 §15.5.7 defines it; it was registered from 3.0. | [`5cfe76f`][5cfe76f] |
| The `w3cschemas` catalog shadowed a schema set's own `xml.xsd` | A bare file name or namespace match beat the fallback. A local copy the fallback can read is now read; W3C URLs and namespace-only imports still get the bundled schema. | [`3c743f4`][3c743f4] |

### Documentation

| Change | What it does | Commit |
|---|---|---|
| Validated nodes have typed values | `docs/validation.md`, the README, `xquery.md` and `todo.md` said a validated node still atomised as untyped; it never did once annotated. The example now sets `Annotate`, which is what types the source. | [`4658352`][4658352] |
| Schema drift audit | `w3cschemas` NOTICE gives each file's source and licence terms; the element-table policy, `docs/xsd.md` and `docs/validation.md` describe current behaviour; `check.sh` runs the w3cschemas suite test. | [`fb64893`][fb64893] |

## v1.5.0 — 2026-10-04

### Added

| Change | What it does | Commit |
|---|---|---|
| `fn:load-xquery-module` | It always raised `FOQM0006`. `xquery` now registers a loader reachable from XQuery, XPath and XSLT, reading modules only through the caller's resolver and budgets. | [`6e6fa8a`][6e6fa8a] |
| `fn:transform` from XQuery and XPath | It raised `FOXT0004` outside a stylesheet. Linking `xslt` registers a sandboxed, depth-bounded processor; `go-xml xquery` runs it. QT3 now measures its 124 cases. | [`6e6fa8a`][6e6fa8a] |
| `internal/xmltok`, an original XML tokeniser | `xdm` read through a fork of `encoding/xml` that had to be re-applied each Go release. Replaced after a zero-difference differential over 59,984 corpus files; 2-5x faster. | [`f18912e`][f18912e] |
| `fn:transform` option `source-location` (#12) | Saxon's option, standard in F&O 4.0, was refused. The document is read through the `fn:doc` resolver and becomes the initial match selection; never streamed. | [`900cece`][900cece] |
| `go-xml xquery` | The CLI could not run XQuery. `go-xml xquery -q Q.xq [INPUT.xml]` honours `output:*` options, takes `-p`, `-o`, `-now`; module and `doc()` reads confined to `-allow-dir`. | [`900cece`][900cece] |
| Merlin C14N interop corpora | `merlin-c14n-three` (27 references) and `merlin-exc-c14n-one` (4) were untested; octets and signed digests now checked, both vendored with provenance. | [`900cece`][900cece] |
| `xdm.Node.Walk` and `xdm.Node.FirstElement` | Finding an element by expanded name needed XPath or a hand-written loop. `Walk` visits elements in document order with early stop; `FirstElement` returns the first match. | [`ba09cac`][ba09cac] |
| `c14n` differential: a PrefixList prefix bound nowhere | The `xmlsec1` differential never named one. `nosuchprefix` lists added (1250 comparisons); expected differences now key on input and list. | [`ba09cac`][ba09cac] |

### Fixed — engine

| Change | Problem → solution | Commit |
|---|---|---|
| Four XSLT 3.0 stylesheets that §19.8 refuses were compiled | `?` lookups modelled (§19.8.8.18), a multi-node streaming parameter consumes (§19.8.5.2), shallow-descent bodies must stride (§19.8.5.5), a streamed ascent call climbs (§19.8.5.7). XSLT 3.0 11,491 → 11,495. | [`7798acd`][7798acd] |
| `fn:transform` accepted string keys in parameter maps | `stylesheet-params`, `static-params`, `template-params` and `tunnel-params` are `map(xs:QName, item()*)`; a string key is now `FOXT0002`, as F&O and Saxon have it. XPath 3.1 reaches 100%. | [`d029347`][d029347] |
| The parser accepted 20 not-well-formed documents from the W3C XML suite | Missing space between attributes, bad comment/PI characters, `<?pi+?>`, QName colons, text outside the root via references or CDATA, a BOM contradicting the encoding. All refused now (XML 1.0 §2.5–3.1, Namespaces §3/§7). | [`8cf4584`][8cf4584] |
| The parser rejected 16 well-formed documents | Duplicate ATTLIST (first wins, §3.3), UTF-16 external entities (§4.3.3), parameter-entity scope and base (§4.2.2), CDATA in entity values, and `version="1.x"` (§2.8). | [`8cf4584`][8cf4584] |
| `fn:transform` ignored `serialization-params`, `requested-properties`, `xslt-version` and `global-context-item` | They now apply per F&O 3.1 §14.7.1: params merge over `xsl:output`, unmet properties are `FOXT0001`, `xslt-version` picks the 2.0 or 3.0 processor. QT3 +20 per lane. | [`d029347`][d029347] |
| `fn:transform` entry-point and result edge cases | No entry point is `XTDE0040` (via `xsl:initial-template`), `initial-function` needs `function-params` (`FOXT0002`), and an empty principal result beside result documents is omitted. | [`d029347`][d029347] |
| `system-property('xsl:is-schema-aware')` said `no` | The processor implements `xsl:import-schema` and schema-aware validation; it now says `yes`. | [`d029347`][d029347] |
| A 3.0 processor refused XSLT 3.0 attributes on `version="2.0"` modules | XSLT 3.0 §3.9.2 defines no difference for 2.0 behaviour; only a 2.0 processor (`MaxVersion` 2.0) refuses them now. | [`d029347`][d029347] |
| A module importing its own namespace raised `XQST0073` | XQuery 3.1 §4.12: "A module may import its own target namespace". The rule is removed. | [`6e6fa8a`][6e6fa8a] |
| An accumulator applied without `use-accumulators` (#16) | An initial `xsl:mode` with no list left every accumulator readable on the source. Its absent list is now empty (§18.2.2), so a read is `XTDE3362`; `accumulator-073` diverges, as Saxon does. | [`5549c2f`][5549c2f] |
| `fn:transform` ignored `static-params` (#15) | A nested stylesheet's static parameter kept its default. The map now binds them at the nested compilation. | [`5549c2f`][5549c2f] |
| `fn:transform` refused option names it did not know | F&O 3.1 §1.7 says such entries must be ignored; they raised `FOXT0002`. String and QName names F&O does not define are now ignored (#12). | [`900cece`][900cece] |
| Exclusive C14N rendered namespace nodes outside the node set | A visibly utilised prefix rendered after an XPath filter removed its node; Exc-C14N §1.1 forbids it. Now dropped, matching Baltimore's Merlin signatures. | [`900cece`][900cece] |
| `new-each-time="no"` rebuilt nodes for node arguments (#13) | `f(.) is f(.)` was false. Calls are now cached per F&O 1.7.4-identical arguments: nodes by identity, atomics by exact type. DocBook `table-cals.049` no longer times out (578). | [`900cece`][900cece] |
| A numeric predicate calling a focus-dependent function was striding | `key#2`, `current#0`, `copy-of#0` and others passed §19.8.8.9's focus-free test. It now reads the `focusDependent` table. | [`ba09cac`][ba09cac] |
| `descendant::x[$i + 1]` was refused `XTSE3430` | A variable declared `as="xs:integer"` was never numeric (§19.8.8.9). A declared numeric type now counts; `let`, `for` and `some` bindings shadow it. | [`ba09cac`][ba09cac] |
| `key(k, v)` gave no streamability verdict | §19.8.9 defines it as `key(k, v, /)`; the call is now assessed in that form. | [`ba09cac`][ba09cac] |

### Documentation

| Change | What it does | Commit |
|---|---|---|
| `c14n` figures | The join-URI row is 67 / 67, not 69; the `xmlsec1` differences are each tied to the clause `xmlsec1` departs from. | [`ba09cac`][ba09cac] |
| Go 1.25 floor in `RELEASE.md` and `README.md` | The floor, and that `golang.org/x/text` v0.35+ requires 1.25 on its own. | [`ba09cac`][ba09cac] |

## v1.4.0 — 2026-09-25

New package `c14n`: Canonical XML 1.0, 1.1 and Exclusive C14N. It ships ahead of the full verification gate in its design: it is checked against the Recommendations' examples, the W3C C14N 1.1 interop cases, `xmllint` and `xmlsec1`, while the Apache Santuario differential, the Merlin corpus and captured Peppol and SAML messages are still to come ([docs/c14n.md](docs/c14n.md#verification-still-to-do)).

### Added

| Change | What it does | Commit |
|---|---|---|
| `method="json"` honours `indent="yes"` | JSON output was always compact. Serialization 3.1 §9.1.4 makes indentation optional — `indent=yes` MAY add whitespace, `indent=no` MUST NOT — and a result of any size is hard to check by eye on one line. Two-space indent matching Saxon and the XML method; a map or array of leaves stays inline, so `[ 3, 2, 1 ]` keeps its shape. `indent="no"` output is unchanged. | [`7dda133`][7dda133] |
| Package `c14n`: Canonical XML 1.0, 1.1 and Exclusive C14N | XML-DSig, SAML and WS-Security digest a byte-exact canonical form, which go-xml could not produce. Streams over any node set; no default algorithm. | [`48651cf`][48651cf] |
| `xdm.Tree.XMLVersion` | Records the version an XML declaration names, `1.0` when there is none. Canonical XML is not defined for XML 1.1, and `c14n` uses it to refuse such input. | [`48651cf`][48651cf] |
| `c14n` differential against `xmlsec1` | 870 node-set comparisons against libxml2's signer, in CI on Linux and locally via Docker (`tests/c14n-xmlsec1.sh`). The 80 differences are 12 inputs where `xmlsec1` departs from the specifications; each is listed and asserted. | [`1a3cf7f`][1a3cf7f] |
| `c14n.FromXPathFilter` and `c14n.NamespaceSet` | The XML-DSig XPath Filter transform, namespace nodes included, so a filter may keep part of an element's namespace axis. It was the one input `c14n` could not express; C14N 1.0 §2.3 and Exclusive C14N §3 now apply literally. | [`1a3cf7f`][1a3cf7f] |

### Fixed — engine

| Change | Problem → solution | Commit |
|---|---|---|
| A non-CDATA attribute declared in the internal subset kept its spaces | XML 1.0 §3.3.3 collapses spaces in `NMTOKENS`, `ID` and other non-CDATA values; they were kept. Now collapsed, and a `NOTATION` list is no longer read as the default. | [`01e9f50`][01e9f50] |
| A character reference in an entity value was expanded one level short | `<!ENTITY a "&#38;#38;">` gave `&#38;`, not `&` (XML 1.0 Appendix D). References are now decoded at declaration and the replacement text parsed as content. | [`01e9f50`][01e9f50] |
| A DTD default `xmlns:p` did not reach an element named `p:doc` | The ATTLIST was matched by local name only, so the element failed the Prefix Declared check. It now matches the name as written. | [`01e9f50`][01e9f50] |
| Line ends were folded late, and not at all in comments and PIs | A CR survived in comment and PI values, and CR-LF in an attribute became two spaces. XML 1.0 §2.11 is now applied on input, before parsing. | [`67c960b`][67c960b] |

### Fixed — tooling

| Change | Problem → solution | Commit |
|---|---|---|
| `tests/check.sh` recorded a failing lane as PASS after an earlier failure | `failed` was a flag, and a lane is judged by whether it moved, so a second failure moved nothing. It counts failures now. | [`1803696`][1803696] |
| The `c14n` `xmllint` differential failed on Windows | `xmllint` writes stdout in text mode there, so every LF came back CR-LF. The translation is undone; a canonical form never holds a literal CR. | [`20214e7`][20214e7] |
| Complexity tests failed on loaded CI runners | They asserted fixed wall-clock budgets. `TestOptimizeNotQuadratic` now counts optimiser predicate walks; the xsd shapes keep only their allocation ceilings. | [`32609d4`][32609d4] |
| The other 15 cost tests still timed themselves | In `xsd`, `relaxng`, `xquery` and `c14n`. Each now counts work, allocation or allocation growth, and was shown to fail with its guard disabled ([docs/testing.md](docs/testing.md#a-unit-test-may-not-cost-minutes)). | [`1c7e368`][1c7e368] |

### Fixed — documentation

| Change | Problem → solution | Commit |
|---|---|---|
| The README's link to the XML 1.1 row of `docs/todo.md` went nowhere | The heading had been renamed, so the anchor matched nothing. It points at the heading as it now reads. | [`1a3cf7f`][1a3cf7f] |
| `docs/testing.md` listed ten fuzz targets where there are twelve | The two `xquery` targets were missing, and nothing said why the nightly run omits them. Both are listed, with the reason. | [`1a3cf7f`][1a3cf7f] |

## v1.3.1 — 2026-09-16

### Fixed — engine

| Change | Problem → solution | Commit |
|---|---|---|
| `system-property('xsl:product-version')` answered `0.0.0` in every program that embeds go-xml | It read `debug.ReadBuildInfo().Main.Version` — the version of the MAIN module, i.e. the importing program, which is `(devel)` under development. It is the constant `version.Version` (`internal/version`) now, checked each CI run against `CHANGELOG.md` and on a tag push against the tag. | [`ca3ff70`][ca3ff70] |
| `format-dateTime`, `format-date`, `format-time` and the duration accessors refused an attribute node | They atomized their argument but never applied the XPath 3.1 §3.1.5.2 cast, so an `xs:untypedAtomic` — any unvalidated attribute — raised `XPTY0004`. They now cast to the declared type; an `xs:string` is still refused, and a failed cast is `FORG0001`. | [`8c0f570`][8c0f570] |
| `fn:error` and `fn:function-lookup` reported the wrong code for an untyped `xs:QName` argument | Both parameters are namespace-sensitive, which XPath 3.1 §3.1.5.2 gives `XPTY0117` rather than the general type error. `argQName` had no `Context`, so it could not see that the code exists only from XPath 3.0; it takes one now, and a genuinely wrong type stays `XPTY0004`. | [`ab80fb1`][ab80fb1] |
| `fn:function-lookup` refused an untyped `$arity` for not being an integer | `$arity` is declared `xs:integer` but was read through `argNumber`, which casts an untyped value to `xs:double`: the §3.1.5.2 cast happened, to the wrong target. It now casts to `xs:integer`, so a non-integral lexical is `FORG0001` — a bad value, not a bad type. | [`ab80fb1`][ab80fb1] |

### Fixed — documentation

| Change | Problem → solution | Commit |
|---|---|---|
| Every generated table in the README and docs rendered as raw text on GitHub | The `<!-- BEGIN GENERATED ... -->` markers sat between table rows, and a Markdown row must begin with `|` — a comment between rows ends the table. The figures are spliced without markers inside a table now, and `stats_test.go` guards the shape. | [`291f67e`][291f67e], [`8fcd0d5`][8fcd0d5] |
| Four fixes were listed under the released `## v1.3.0` heading | They shipped after that tag, so the changelog claimed v1.3.0 contained them. Moved to this section, each with its commit link. | [`7f7fc4e`][7f7fc4e] |

### Added

| Change | What it does | Commit |
|---|---|---|
| `RELEASE.md` and a tag-push release workflow | The release procedure was never written down, which is why the version constant went stale for three releases. The tag push now runs the full gate, refuses a tag that disagrees with the committed constant, and builds the release notes from this file. | [`ca3ff70`][ca3ff70] |
| A link to the browser playground, and an Acknowledgements section | Credits the independently built and maintained go-xml fiddle. | [`cb43c76`][cb43c76] |

## v1.3.0 — 2026-09-14

### Added

| Change | What it does |
|---|---|
| **`xpath.CompileWith`, a bounded and cancellable compile** | The four positional spellings now delegate to one options-taking entry point, so the next axis is a field rather than a fifth name. `MaxBytes` refuses an over-large expression before parsing; `Context` bounds the work, observed between phases and inside the optimiser but never inside the folding decision. Additive: every existing signature is unchanged, as v1 requires. |
| **An independent model for XSD wildcard acceptance and the UPA rule** | The wildcard rules are set membership, so the oracle is the spec text: §3.10.4.2 and §3.10.4.3 as clauses over sets, checked against `AllowsName` and the real `checkUPA` across 60,000 generated pairs with a fixed seed. It calls none of the matcher's own predicates -- a model that consults the implementation can only find inconsistency, never error. |
| **The foundation of the XSLT 3.0 §19.8 streamability analysis** | §19 infers a posture and sweep for every construct and refuses a free-ranging one with `XTSE3430`. The lattice and the §19.8.8 XPath rules are implemented; an unmodelled construct is "no opinion", not a rejection. |
| **Every function binds against its declared types** | The call-binding table carried 21 of the manifest's 272 entries; it now carries all 272. A parameter F&O 3.1 declares without `?` refuses an empty sequence at the call rather than in a hand-written guard, and `TestMigratedSignaturesMatchManifest` fails the build on a spelling that disagrees with the Recommendation. |
| **The `map:`, `array:` and `math:` families became expressible at all** | A signature key could only ever name the `fn:` namespace, so 46 entries were unreachable rather than merely unmigrated; a prefixed key format reaches them. The hardcoding was duplicated in the builder and the enforcement test, so fixing one alone would have left the other checking the wrong names. |
| **The regex and formatting families, deferred twice on a premise that was wrong** | `$pattern`, `$flags` and `$picture` were held back for fear call binding would answer `XPTY0004` where a hand guard answers `FORX0002`. The codes do not compete: `FORX0002` is for a malformed pattern, reached only once a pattern exists. All four QT3 cases passing `()` in such a position expect `XPTY0004`. |

### Deprecated

| Change | What to do instead | Commit |
|---|---|---|
| `xpath.CompileVersion`, `CompileXQuery` and `CompileVersionRefFloor` | They delegate to `CompileWith` and stay for v1; every caller in this repository, 20 sites, now spells the options struct. Use `CompileWith`. | [`b90c8f5`][b90c8f5] |

### Fixed — engine

| Change | Problem → solution | Commit |
|---|---|---|
| A Windows absolute path parsed as a one-letter scheme, so every local-file resolver refused it | `url.Parse("C:/x")` gives Scheme `"c"`, and the guard refusing non-`file` schemes ran before `fileURIToPath`. `internal/uripath` exempts a drive path textually — a scheme-colon not followed by `//` — keeping `c:///secret.rng` refused. |  |
| A `file:` base URI was built by concatenating `"file://"` with an OS path | On Windows that made the drive the URI *authority* and left backslashes, so the base did not parse and every relative reference against it was refused. Both halves now go through `internal/fileuri`, tested with Windows shapes on every platform. |  |
| `resolveBase` discarded an `xml:base`'s base URI without a word when the base did not parse | It returned the bare reference, so a Windows base made the element's base `"deeper/"` — the working directory, not the document's — and `inheritedBaseURI` stops at it, poisoning the subtree. It now merges by RFC 3986 §5.2.3 on path structure alone. |  |
| `SecondaryResult.String()` dropped it too, on the path the source document reaches | Missed when the primary one was documented: a grep used the wrong receiver name. `xsl:result-document`'s serialization attributes are AVTs, so this is where untrusted input lands. | [`6aa8324`][6aa8324] |
| `Result.String()` dropped the serialization error the injection fixes raise | An embedder writing output through the inviting call got `""` where `Serialize` reports `SEPM0016`. The check stays in `Serialize` — XSLT 3.0 §2.10 puts it after the transform — and the doc comment now names the discard. | [`56adce5`][56adce5] |
| `media-type` was written into the injected `<meta>` tag unescaped | Every sibling attribute goes through `escapeAttrRunes`; this one was concatenated raw, so `text/html"><script>…` closed the tag and ran in the `<head>`. It is now escaped at the write site. | [`1f638d1`][1f638d1] |
| A `doctype-system` value could close its own quoted literal | `a"b'>` was written as `SYSTEM 'a"b'>` and the rest became markup, appending a live entity declaration. Serialization 3.1 §3 forbids both quote kinds: now `SEPM0016`. | [`411fdd5`][411fdd5] |
| Two `xsl:global-context-item` attributes were listed but unreachable | `@streamable` and `@use-accumulators` are not in §3.10, and the element is checked against the `context-item` key, so both were already refused — naming an element the stylesheet had not written. Entries deleted; the diagnostic now names `xsl:global-context-item`. | [`c9945c5`][c9945c5] |
| `xsl:output` and `xsl:function` attributes were flagged as attribute value templates | The Recommendation braces only `xsl:result-document`'s, so `build-tree="{$x}"` escaped XTSE0020. Four `avt` flags cleared, on the attributes whose enumeration the flag was suppressing; `streamability` keeps its EQName union. | [`4410604`][4410604] |
| `xsl:output/@parameter-document` and `@json-node-output-method` reached no validator | Neither had a type, so any value passed. Now `uri` and `"xml"`/`"html"`/`"xhtml"`/`"text"`/eqname; a `{...}` is XTSE0020. | [`a048213`][a048213] |
| `visibility="hidden"` was declarable and `cache="full"` accepted | Hidden is acquired through `xsl:accept`/`xsl:expose`, and `cache? = boolean`. The enumerations now match the summaries; both are XTSE0020. | [`4410604`][4410604] |
| Three working-draft attributes were tolerated on a 3.0 module | `function/@identity-sensitive`, `accumulator/@applies-to` and `package/@use-package` are now `removed30`: XTSE0090. `param/@export` stays tolerated, so `iterate-024` still reaches its XTSE0010. | [`4410604`][4410604] |
| A misplaced `xsl:on-completion` was outranked by an unknown attribute | §8.4's placement rule is now read off the tree ahead of the grammar sweep, so a doubly-broken module reports the structural XTSE0010, not XTSE0090. | [`2bd65b9`][2bd65b9] |
| `xsl:param/@export` was accepted on a 3.0 module | §9.2's summary stops at `static?`, so the withdrawn draft spelling is now `removed30`: XTSE0090. The ordering fix above keeps `iterate-024` passing. | [`2bd65b9`][2bd65b9] |
| The XTSE0020 message for a yes-or-no attribute listed only `yes, no` at 3.0 | The check already accepted `true/false/1/0` there. The message now lists the spellings in force for the module's version. | [`f9d96aa`][f9d96aa] |
| `xsl:merge-source/@for-each-stream` was accepted alongside `for-each-source` | Bug29804 renamed it; the old name survives only in the stale schema appendix. It is now `removed30`, so a 3.0 module naming it gets XTSE0090. | [`c852de7`][c852de7] |
| The XTSE0020 message listed the boolean spellings twice | The 20 enumerations that already spell all six had `true, false, 1, 0` appended again. An alias the enumeration already carries is no longer added. | [`bf2ac23`][bf2ac23] |
| §19.8.8.15 decided a named function reference only under a grounded context | Nothing recorded focus dependence. A table of the 33 focus-dependent (name, arity) pairs, from the F&O and XSLT "Properties" paragraphs, now decides the rest. | [`acdc330`][acdc330] |
| §19.8.8.11 dynamic function calls had no streamability rule | `$f(child::x)` in a streamed context was accepted for want of an opinion. The base is inspected and each argument navigates, the spec's no-signature fallback, so a streamed argument is refused. | [`a09c437`][a09c437] |
| The numeric-predicate rules of §19.8.8.9 and §19.8.8.10 were missing | `descendant::section[1]` and `(//x)[3]` came back crawling, though the spec makes a focus-independent numeric predicate striding. Both rules now apply, for statically certain types. | [`f05bea8`][f05bea8] |
| §19.8.8.11 ignored the declared type of the function variable | `$m(@class)` under `as="map(*)"` was refused, so `accumulator-054` regressed to `XTSE3430`. The binding's `as` is read by the §9.7 scoping walk; a map or array absorbs, a `function(A, B)` types each argument. | [`ab053ac`][ab053ac] |
| The `SERE0007` raw-text guard looked at one text node at a time | Adjacent text nodes are written into one raw run, so `"a<"` + `"/script>"` emitted a contiguous `</script>` under the html method. The guard now spans the boundary. | [`2acbab4`][2acbab4] |
| 29 cast and type errors rendered their code twice | `xdm.ErrCast` and `xdm.ErrType` already carry `FORG0001`/`XPTY0004` and `Error()` prints it, yet the call sites spelled it again, so `xs:byte(999)` read `FORG0001: FORG0001: …`. The format strings no longer repeat it. | [`0af8592`][0af8592] |
| Five parser limits reported `XPST0003` for a well-formed expression | §2.3.1 names `XPDY0130` for an implementation-dependent limit; the depth, chain, type-nesting and XQuery nesting caps use it now. Messages and the sentinel are unchanged. | [`75cb3af`][75cb3af] |
| The base-cycle check walked the chain once per type | `checkTypeBaseCycles` was the residual O(N²) after the facet merge: 10,000 chained types cost 4.3 s, a 10,001-type ring 7.8 s. Proven-acyclic and on-cycle types are remembered, so both now take under 0.2 s. | [`db0448d`][db0448d] |
| XSD facet checking was quadratic in the depth of a restriction chain | `mergedFacets` flattened the whole chain for every type asked about, so 10,000 chained restrictions took 15.4 s to load; the merged set is memoised on the parser and the merge is gone from the profile. | [`33263c8`][33263c8] |
| The entity-expansion budget restarted for every resolved module | `parseUncached` minted a fresh allowance per file, so 60 imports each under the 1 MB ceiling expanded 42 MB and were accepted; the compilation and the evaluation now share one. | [`9681343`][9681343] |
| The resolver cache cleared itself instead of evicting | Past 256 URIs the whole map was thrown away, so a stylesheet cycling over more documents re-parsed every one; evicting a single entry cut 300 documents × 4 rounds from 1.05s to 0.31s. | [`d1a9131`][d1a9131] |
| `xsl:evaluate`'s restriction leaked into the body of a function it called | §10.4.1 governs the names the target expression may reference, not what those functions call. A public function's private callee was refused, costing 512 of 593 DocBook documents. | [`e812277`][e812277] |
| A private stylesheet function was callable from `xsl:evaluate` | §3.5 makes a plain `xsl:stylesheet` an implicit package, so its undecorated functions are private. `evaluate-045` now passes, and with the leak above fixed it costs no real-world document. | [`e812277`][e812277] |
| An XQuery clause scan hung forever on a combining mark | `isNameStartByte` accepted every byte ≥ 0x80 while `scanNCName` applied the real rune production, so a name char that may not start a name left the cursor unmoved and the loop spun. The two tests now agree, and a non-advancing scan is refused as `XPST0003`. | [`c2af54f`][c2af54f] |
| A truncated direct constructor in a variable declaration panicked | `scanDeclExpr` discarded `skipDirConstructor`'s error, so the cursor ran one past the end and the closing slice went out of range on input as short as `declare variable$A:=<`. The error is returned now. | [`c2af54f`][c2af54f] |
| `fn:format-number` rounded a tie away from zero | F&O 3.1 §4.7.5 defines the rounding by calling `fn:round-half-to-even`, so `format-number(2.5,'0')` answered 3 where the shared function answered 2. No corpus holds a tie, so only the new test pins it. | [`c01b98a`][c01b98a] |
| A `format-number` exponent separator was split on what followed it alone | F&O 3.1 §4.7.3 needs an active character on *both* sides, so `'e0'` wrongly split to an empty mantissa and errored naming a picture nobody wrote; it is passive prefix, giving `e1234`. | [`4c78fae`][4c78fae] |
| An active character after the `format-number` exponent digits was kept as a suffix | §4.7.3 forbids any following active non-digit, but only a second separator-plus-digits was caught, so `'0e0.0'` gave `1e3.0`; it now raises `FODF1310`, while passive `'0e0xyz'` still formats. | [`4c78fae`][4c78fae] |
| The XQuery constructor scan was exponential and skipped the depth bound | A failed skip was re-derived at every nesting level, so 120 bytes took 52s; it is memoised now. The scan also ran before the depth counter, so 20,000 levels were accepted. | [`3ae153c`][3ae153c] |
| The XML output method never escaped NEL, U+2028 or the C0/C1 controls | Serialization 3.1 §5 requires them as references; a literal U+0085 reparsed as a line feed, silently corrupting the round trip. One range-aware escaper now serves both positions. | [`282953e`][282953e] |
| `fn:serialize` with `method="html"` wrote XML syntax for empty elements | `<div/>` made an HTML parser read an unclosed tag and swallow the rest of the document. The void-element table is now read, which needed `html-version` honoured. | [`e3cb35a`][e3cb35a] |
| The `json` output method escaped only `#x1`–`#x1F` | Serialization 3.1 §9 also requires 127–159, so DEL and the C1 controls went out raw; `fn:xml-to-json` already had the range right. | [`e3cb35a`][e3cb35a] |
| The 49 `xs:` constructors carried no signature | Arity-only matching made every one-argument function test answer true. Signatures are now derived from F&O 18.1. | [`e3cb35a`][e3cb35a] |
| `fn:concat` was capped at arity 2²⁰ | F&O 3.1 states no maximum, but the synthesizer materialised an `arity+1` slice, so the cap was load-bearing. A compact descriptor made it constant. | [`5cb5358`][5cb5358] |
| `fn:function-available` disagreed with `fn:function-lookup` above arity 100 | `LookupDynamic` returned early for a library answering for itself, before reaching the variadic synthesis. Both routes now share it. | [`5cb5358`][5cb5358] |
| `let` expressions and inline function declarations had no streamability rule | §19.8.8's table gives `let` `N`/`T`; §19.8.8.16 makes an inline function roaming when it textually mentions a containing function's streaming parameter. | [`7f70d40`][7f70d40] |
| Six more instance-level lexical paths read XSD values through Go Unicode whitespace | Six sites reached `strings.TrimSpace`/`Fields`. The sharpest: an empty-content element holding only U+00A0 validated as EMPTY, where `cvc-complex-type.2.1` must report it. | [`0f604be`][0f604be] |
| XQuery computed node names and PI targets trimmed a no-break space as whitespace | Both are `whiteSpace="collapse"`, so U+00A0 is a name character; `element {"<NBSP>e"}` silently built `<e/>`. Both now trim through `xdm.TrimXMLSpace` (`XQDY0074`/`XQDY0041`). | [`0f604be`][0f604be] |
| Nine XSD schema-parsing paths read lexical values through Go Unicode whitespace | Nine sites reached `strings.TrimSpace`/`Fields`, so `maxOccurs="<NBSP>unbounded"` was accepted. The package's own `trimXMLSpace`/`splitFields` now do the work. | [`0f604be`][0f604be] |
| `fn:path` built a string of unbounded length against no byte budget | Its result carries one step per ancestor, so length grows with the depth of the node rather than with any argument: a document 500 elements deep produced 4,000 bytes against a budget with 16 left. It returns through `stringResult` now, as does `fn:generate-id` — bounded and tiny, but charged under the same rule that whoever allocates charges. | [`aa11b38`][aa11b38] |
| `fn:concat` answered at a different maximum arity depending on how it was named | The bound lived at `fn:function-lookup` alone, so `concat#9223372036854775807` built a function item claiming arity 2^63-1. One `maxVariadicArity` now governs both routes. | [`84ad066`][84ad066] |
| The dynamic `xs:QName()` constructor trimmed a no-break space as whitespace | Only a *computed* argument reaches this path, so the literal form was refused by the folder while `concat("<NBSP>","xs:string")` succeeded. Three audits misread that as sound; both forms are now probed. | [`5405f36`][5405f36] |
| A synthesized `fn:concat` arity carried the signature of `fn:concat#2` | It borrowed arity-2's signature and changed only `Arity`, so a length/arity mismatch read as "no declared type" and matched on arity alone. Now materialised to the real arity. | [`5405f36`][5405f36] |
| `fn:function-lookup` returned a function item with no signature | `NamedFunctionRef.Eval` set `Signature` and the lookup callback did not, so the same function item answered `instance of` differently by route. Both now carry it. | [`e3e3b3a`][e3e3b3a] |
| `fn:QName` and `fn:resolve-QName` trimmed a no-break space as whitespace | `xs:QName` is `whiteSpace="collapse"`, whose whitespace is XML `S`; `strings.TrimSpace` also strips U+00A0, so `QName("...", "<NBSP>p:a")` was accepted where F&O 10.1.2 requires `FOCA0002`. Both now trim through `trimXMLSpace`. | [`0f604be`][0f604be] |
| `fn:normalize-unicode` accepted a form name padded with a no-break space | F&O 5.4.6 makes the effective form `fn:upper-case(fn:normalize-space($form))`, which is XML `S` only, so an NBSP-padded `"NFC"` is an unrecognised form and must raise `FOCH0003`; it was normalising as NFC instead. Now collapsed through `collapseXMLSpace`. | [`0f604be`][0f604be] |
| `fn:reverse`, `fn:insert-before` and `fn:remove` built a second sequence against no budget | All three were declared `func(_ *Context, ...)`, so `MaxItems` was never asked about a copy the size of the input — unbounded from a host that invokes the built-in directly rather than through an enclosing evaluator. Each now reserves through `makeSequence` before the `make`; `fn:remove` past the end returns its argument and stays uncharged. | [`aa11b38`][aa11b38] |
| Every normative manifest row is now asserted to be registered | Coverage ran one way only — registered functions were checked against the manifest, which cannot catch a standard function that stops being registered. `TestManifestFunctionsAreAllRegistered` closes the other direction at green: 272 rows, 0 unregistered. | [`84ad066`][84ad066] |
| A typed function test was answered on arity alone | `applyBuiltinSignatures` read a seventeen-entry table of its own while call binding read all 272, so `fn:concat#2` satisfied `function(xs:date, xs:date) as xs:integer` -- both take two arguments. Function items now read `specSignatures`, and `fn:concat` gets its per-arity type. | [`a3f5568`][a3f5568] |
| Three item types rendered as a bare `item()` | `xs:numeric`, an array test and a typed function test lost their identity in `SequenceType.String()`, which is what a signature is compared through, so an identical type failed to match itself once signatures were consulted. | [`a3f5568`][a3f5568] |
| `fn:string-to-codepoints` and all three `fn:tokenize` paths materialised items against no budget | The item charge lived at `LetExpr`, `evalFor` and the range operator, so a host calling a built-in directly got the full `MaxItems` again. Each reserves its known count once through `makeSequence`, before the `make` rather than after. `fn:substring` is charged here too: it allocates through `string(runes[...])` where its `-before` and `-after` siblings only slice. | [`1e5e26c`][1e5e26c] |
| `fn:serialize` and nine other string producers built strings against no byte budget | Serialization charged nothing at all, and `normalize-space`, the case mappings, the URI escapes, `normalize-unicode` and the formatters returned newly built strings through `strSeq`. The XML, JSON and adaptive writers charge before each append through `serializeSink`; the rest return through `stringResult`. | [`924ef76`][924ef76] |
| `fn:normalize-space` collapsed a no-break space | `strings.Fields` splits on the whole Unicode White_Space set, so `normalize-space("a\u00a0b")` returned `"a b"` with the NBSP replaced. F&O 3.1 §5.4.5 defines the whitespace as exactly XML S, so the NBSP is data and survives. | [`4c06a1f`][4c06a1f] |
| `fn:id`, `fn:idref` and list tokenization split on a no-break space | An IDREFS value, an `xml:id` and any `xs:list` tokenize by the whiteSpace=collapse rule, whose separators are XML S. `strings.Fields`/`TrimSpace` made an NBSP a token boundary, so `id()` found names the document does not hold and one NMTOKEN became two. | [`a0cf1da`][a0cf1da] |
| Datatype lexical casts stripped a no-break space | A collapse-faceted type trims XML S only, so an NBSP makes the lexical form invalid. It was stripped instead, and `xs:integer`, the temporal and duration types, `xs:boolean`, `xs:hexBinary`, `xs:base64Binary`, `xs:QName` and `xs:anyURI` all accepted values their grammar rejects. | [`b4a8b53`][b4a8b53] |
| XSD facet bounds trimmed a no-break space | The bound facets and the two QName resolvers trim before parsing, which is the collapse edge trim and takes XML S only. `strings.TrimSpace` also stripped U+00A0, so a bound written with an NBSP parsed as though it were well formed. | [`4fd0df5`][4fd0df5] |
| A nested `fn:transform` began its own item and byte allowances | `newRuntime` mints both counters through `xpath.NewContext`, so every level of a nest got the full `MaxBytes` again while the depth budget inherited; 500 levels charged ~5.5 GB with no refusal. The caller's context is adopted now, each counter with its held flag. | [`9f033e2`][9f033e2] |
| Every derivation walk was decided by whichever schema loaded last | `instance of`, `castable as`, the `element()` and `attribute()` tests, `fn:id` and XTTE0950 all walked the process-global derivation table, so a second schema reusing a type name silently retyped a node the first had validated. A node now carries the validating schema's `TypeEnvironment` and the walks read it off the node. | [`cdba77a`][cdba77a] |
| `XTTE1545` was decided by whichever schema loaded last | The walk deciding whether a constructed attribute may be validated against a named type read the process-global derivation table, so a second schema reusing the name overwrote the answer — permissively, letting the validation §19.2 forbids proceed. It now walks the environment of the schema being validated against, which `mergeSchema` carries into the stylesheet's aggregate. | [`1e21828`][1e21828] |
| `fn:serialize` accepted `indent` and never indented | The parameter had two writes and no reads, so one document indented through `xsl:result-document` and not through `fn:serialize`. Serialization 3.1 §4's rules, including the significant-whitespace and html comment/PI exceptions. | [`34908a7`][34908a7] |
| A no-namespace `xs:QName` map key named a standard serialization parameter | Serialization 3.1 §3 gives parameter names as `xs:string`, reserving `xs:QName` with a non-absent namespace for implementation-defined ones. The key was compared with `String()`, so `QName('','indent')` matched `indent`. | [`34908a7`][34908a7] |
| `build-tree` was inert for the principal result | §26.1 applies it to "the raw principal result or secondary result"; only `xsl:result-document` honoured it. `Tree()` now reports the absence §24.1 describes, and `BuildsTree()` tells it from an empty result. | [`34908a7`][34908a7] |
| `XTDE1500` was detected in one direction only | The spec's "write to ... and read from the same resource" is symmetric; a document written and then read back was not caught. The written URIs are recorded beside the read ones. | [`34908a7`][34908a7] |
| `fn:serialize` ignored `doctype-public`, `doctype-system` and `escape-uri-attributes` | The three had no field to write to, so a request for a doctype produced none and a URI attribute went unescaped though the parameter defaults to `yes`. `xsl:output` had honoured all three throughout. | [`93c5e88`][93c5e88] |
| `suppress-indentation` suppressed the whole document, and only in one spelling | It is now per-element in both the map and element forms: the named element keeps its own indentation and only its content is spared, which is what `serialize-xml-108` asserts. | [`93c5e88`][93c5e88] |
| Every JSON option was dropped when its name was an `xs:untypedAtomic` key | `duplicates`, `escape`, `validate` and `fallback` across `parse-json`, `json-doc` and `json-to-xml` silently did nothing. The key family is the one `xdm/maparray.go` already documents. | [`93c5e88`][93c5e88] |
| `xsl:function/@cache` rejected two of the three values the spec defines | `cache="full"` and `cache="partial"` were static errors, and widening the table alone would have turned that into silent loss of memoisation because the hint is read with `isYes`. | [`93c5e88`][93c5e88] |
| `xsl:function/@identity-sensitive` and `xsl:merge-source/@for-each-stream` were unknown attributes | Both are in the 3.0 element syntax; the merge spelling is the Recommendation's, and the withdrawn `for-each-source` stays accepted beside it. | [`93c5e88`][93c5e88] |
| `xsl:accumulator-rule` accepted a `priority` attribute that no specification defines | A misspelling of `phase` was swallowed in silence; it is now `XTSE0090`. | [`93c5e88`][93c5e88] |
| The second presentation modifier `a` and `c` raised `FOFD1340` on valid pictures | `[M1a]` was an error and `[MNna]` silently lost its name presentation and emitted a bare number. All four modifiers the grammar admits are now stripped. | [`93c5e88`][93c5e88] |
| `fn:default-collation()` reported codepoint whatever the static context said | It ignored its context entirely, so a stylesheet with `default-collation=` got case-blind `contains` and a contradicting `default-collation()`. The URI now travels with the collation from all three hosts. | [`93c5e88`][93c5e88] |
| `fn:adjust-*-to-timezone` accepted an offset that was not a whole number of minutes | `PT1M30S` was truncated to `+00:01` instead of raising `FODT0003`; the check tested integral seconds where the spec says minutes. | [`93c5e88`][93c5e88] |
| `fn:serialize` read `indent="true"` as `indent="no"` | `checkYesNo` admitted all six lexical booleans and discarded the normalisation, so five parameters took the affirmative spelling and set the negative. It now returns the normalised value. | [`7ffd7da`][7ffd7da] |
| `fn:serialize` wrote `standalone="true"` into the XML declaration | XML 1.0 §2.9 admits only `yes` or `no` in an SDDecl, so the output was malformed; `xsl:output` had normalised it all along. | [`7ffd7da`][7ffd7da] |
| Twelve functions accepted an empty sequence for a parameter the spec declares without `?` | F&O 3.1 makes `()` there `XPTY0004`. `fn:round` was the worst: it neither raised nor returned empty but silently substituted precision 0. | [`7668773`][7668773] |
| Five attributes accepted values no specification defines | `xsl:function/@visibility` excludes `hidden`, and `@streamability`, `@new-each-time`, `@component` and `xsl:copy-of/@validation` each have a closed set the table did not carry. | [`1b7a25a`][1b7a25a] |
| `validate` accepted a document node with more than one element child | XQuery 3.1 §3.21 requires exactly one element plus zero or more comments and PIs; the operand was returned unchecked instead of raising `XQDY0061`. | [`7e7c766`][7e7c766] |
| `fn:analyze-string` refused a backreference pattern its four siblings accepted | It compiled through RE2 alone where `fn:matches`, `fn:replace`, `fn:tokenize` and `xsl:analyze-string` reach the backtracking engine, so `(a)\1` was a static error from one function and a working pattern from the other four. An exhausted step budget mid-scan also built a result element describing an input the engine never finished reading. | [`9ae8c57`][9ae8c57] |
| The conformance total was typed rather than derived, and said 168 where its rows summed to 104 | Every check in the repository guards a figure against the ratchet, and the total is not such a figure: it is the sum of the rows, so each row could agree with its measurement while the sum was wrong. `tests/conformance/results.json` is now the source and the generator does the addition, refusing a total the document supplies. | [`d683fd8`][d683fd8] |
| `xdm.Parse` accepted documents that are not well formed | `RawToken` leaves duplicate attributes, namespace well-formedness and document grammar to its caller, and `Parse` was not checking them. An unbound prefix returning an empty URI is not a harmless recovery: it gives the node a different XDM name. | [`4ce4086`][4ce4086] |
| The XSLT serializer bound one prefix twice, writing XML it could not read back | `xsl:namespace-alias` with competing aliases at different import precedence leaves two namespace nodes for one prefix, and `namespace-alias-2620` emitted two `xmlns:y` attributes on one element. It passed only because the malformed result failed to parse and the judge compared text instead. | [`220b466`][220b466] |
| Comments and processing instructions were written verbatim, through no character check | `--` inside a comment, `?>` inside a PI, a reserved `xml` target and a C0 control all reached the output. A caller can build XDM directly, so serialization owes its own refusal: `SERE0003`, `SERE0006`. | [`220b466`][220b466] |
| `relaxng` was the one package with no library-level confined resolver | `xsd`, `dtd` and `xslt` each expose one; `relaxng` had it only inside `cmd/go-xml`. `FileResolver` confines through `os.OpenRoot`, leaving the leaf unresolved so the check-then-open race `37972d9` removed is not reintroduced, and bounds one schema by `MaxBytes`. | [`f3ff553`][f3ff553] |
| `fn:serialize` parsed `normalization-form` and ignored it | A request for NFC returned the text unchanged and an unsupported form was accepted in silence. Normalization runs at the text-writing site, so a character map's replacement stays written as given. | [`17bcdb6`][17bcdb6] |
| The `same-key` oracle called the grouping function it was meant to check | It shared `typeFamilyOf` with production, so a wrong family grouping made both agree and the test stayed green. `oracleFamily` is written from `op:same-key` instead. | [`abc8cbc`][abc8cbc] |
| `<xsl:value-of selct="..."/>` compiled clean and emitted nothing | §3.9 grants forwards-compatible leniency only *above* the version the processor implements; the guard measured against 2.0, so a modern stylesheet got 2.0-era silence for a typo. The leniency and an element table that had never listed `visibility`, `streamability` or `applies-to` each made the other look harmless. | [`6eacc2d`][6eacc2d] |
| An `intersect`/`except` pattern took the default priority, so the wrong template fired | §6.4 gives such a pattern its first operand's priority; it took 0.5 instead, and `a except b` beat an explicit `priority="0.25"`. | [`6eacc2d`][6eacc2d] |
| `html-version="7"` was accepted and silently demoted to HTML 4 | The check read `@version`, the fallback, rather than `@html-version`. | [`6eacc2d`][6eacc2d] |
| `XTDE1490` compared `href`s as written, so `out.xml` and `./out.xml` overwrote each other | The two resolve to one URI; the comparison now happens after resolution. | [`6eacc2d`][6eacc2d] |
| `fn:deep-equal` had its collation carve-out inverted, comparing namespace URIs case-blind | F&O 3.1 exempts names from the supplied collation; the exemption was applied to everything *but* them. | [`6eacc2d`][6eacc2d] |
| Seven smaller divergences in one audit | A JSON fallback could put a character into the tree that serialization then dropped silently; the regex `q` flag did not suppress `x`; `byte-order-mark` was ignored by the `json` and `adaptive` methods; `fn:serialize` rejected the empty-sequence `standalone`; `cdata-section-elements` worked in the map spelling and not the element one; an uncomputable tabulated calendar was an error rather than the §9.8.4.6 fallback; and a minimum width was parsed and discarded. | [`6eacc2d`][6eacc2d] |
| `xsl:stream` was rejected | Accepted beside `xsl:source-document`. This entry first had the names reversed: `xsl:stream` is the last-call draft's name, the Recommendation renamed it `xsl:source-document` (Bug29747), and `xsl:stream` is XTSE0010 again since round SG. | [`75d633e`][75d633e] |
| Forwards compatible processing was decided against a fixed 3.0, so a 2.0 processor rejected a construct it must ignore | §3.9 measures the effective version against the version the processor implements, which `CompileOptions.MaxVersion` sets. | [`187dfec`][187dfec] |
| Three rooted resolvers checked containment and then opened, so only `xslt` enforced its root at open time | `xsd`, `dtd` and the CLI's RELAX NG resolver open through `os.OpenRoot`; the string check stays as diagnosis, not enforcement. | [`37972d9`][37972d9] |
| `fn:transform` accepted the `post-process` option and ignored it, so a pipeline silently ran one stage short | The function is applied to every result document after delivery, and an option name the processor does not know is now `FOXT0002` rather than silence. Reported as issue #5. | [`e8ebf4b`][e8ebf4b] |
| `fn:parse-json` and `fn:json-to-xml` recursed once per nesting level with nothing counting them | `maxJSONDepth` bounds nesting at 1000, matching the XML parser's own depth limit. | [`9660e52`][9660e52] |
| The CLI's RELAX NG resolver read a schema whole, the only resolver in the library with no byte limit | `DefaultMaxRNGBytes` bounds one schema at 16 MB, matching `xsd`. | [`f29b554`][f29b554] |
| `go-xml validate` confined nothing unless `-root` was given | A non-nil resolver with an empty `Root` reads anywhere, so the flag's default undid the library's closed one; it now defaults to the schema's directory, as the transform does. | [`0aa92ca`][0aa92ca] |
| Template recursion and an oversize range refused without the resource sentinel, so a caller could not classify them | Both wrap `xdm.ErrResourceLimit`; the range also carries `XPDY0130`, as its counted twin already did. | [`57a2b64`][57a2b64] |
| `xsl:result-document` followed a symlink out of `-result-dir`, so a stylesheet could write anywhere | The write opens through `os.OpenRoot`, and each directory is made through the same root. | [`17b1c91`][17b1c91] |
| A function applying itself through its own name recursed uncharged, bypassing `MaxDepth` | Both function-item invocation paths take `Depth` from the call, as the inline path already did. | [`e511421`][e511421] |
| An attribute value template concatenated past `MaxBytes`, so the byte budget bound `xsl:value-of` and not `{$v}{$v}` | `avt.eval` charges the text before it joins the builder. | [`5c17280`][5c17280] |
| `fn:transform` minted a fresh depth allowance per nesting level, so a self-calling stylesheet killed the process | The charge comes from the call and the nested runtime continues the count, so nesting refuses with `XPDY0001`. | [`8e0f44d`][8e0f44d] |
| A function item invoked through the public API ran against a byte budget of its own | Both invoke sites forward each budget with the flag marking its boundary, `items` included. | [`d63cbb2`][d63cbb2] |
| Every XSD assertion began its own 1 GiB allowance, and a refusal was reported as an unsatisfied assertion | One budget per validation episode, and a resource refusal stops the run instead of reading as invalid. | [`f161723`][f161723] |
| A map keyed `xs:hexBinary` and `xs:base64Binary` on their spelling, so `0F` and `0f` were two entries | The key is the decoded octets, which is what `eq` already compared. | [`10486a6`][10486a6] |
| Nothing bounded the bytes an evaluation produced: a 1,009-byte expression returned 640 MB | `xpath.MaxBytes` charges the constructs that concatenate as they build. Refuses with `XPDY0130`. | [`b50b373`][b50b373] |
| A self-applying function item recursed uncharged and killed the process | `DynamicCall.Eval` descends before `Invoke`, so the attack refuses with `XPDY0001`. | [`35c2e77`][35c2e77] |
| `TransformOptions.MaxDepth` bounded template recursion only, not expressions | The option now reaches the XPath context, which is the path that takes untrusted input. | [`35c2e77`][35c2e77] |
| `fn:distinct-values` compared numerics pairwise, costing O(n^2) `eq` calls | Integer and decimal key on their exact rational; 100,000 integers go from 573 s to 0.15 s. | [`694fe29`][694fe29] |
| The process environment was readable with no opt-in | Both functions answer from `Context.Environment`, nil withholding everything. | [`40930d5`][40930d5] |
| `map:put` and `map:remove` copied the whole entry slice, so a large map cost O(n) per call | The map is a persistent hash array mapped trie that shares structure and keeps insertion order by sequence number; `same-key-023`'s 421,875 keys now finish. | [`ac743d4`][ac743d4] |
| Conformance figures in the documentation drifted from the measured ones and nothing failed | `tests/docfigures.sh` reads `tests/ratchet.txt` and fails `check.sh` on any copy beside a suite denominator that disagrees; the XSD schema/instance split is ratcheted too. | [`920fd8a`][920fd8a] |
| The two ExprSingle scanners bounded a branch with flat counters, which cannot record the nesting order of interleaved `if` and FLWOR | Both keep a nesting stack, so a stop keyword is honoured only when nothing nested is open to claim it; the last branch of `if` and `switch` now scans with the enclosing clause's stops. `RexParser`. | [`7f2d2d0`][7f2d2d0] |
| `fn:transform` from a `static="yes"` variable deadlocked: the nested compile asked for a mutex the outer one held | `Compile` now takes the lock and delegates to `compileLocked`, which the static phase re-enters without it; the static library binds the real function. `transform-004`. | [`59ee9b9`][59ee9b9] |
| The §19.8.8.2 quantified rule was withheld over a streamed binding: faithful, it refused three valid stylesheets | A data-flow environment gives the range variable the binding's posture, so `$t/@value` stays striding and `$t/preceding-sibling::*` roams. `streamable-129`. | [`6e03e3d`][6e03e3d] |
| §19.8.9.4's third condition was unenforced: a `current-group()` call kept its group across a focus-changing container | A nested `xsl:for-each`, `xsl:iterate` or `xsl:copy select=` is now the call's focus-setting container, so the call is roaming. `si-group-031`. | [`3672fa3`][3672fa3] |
| An end-phase accumulator rule reading its own pre-descent value was refused as circular | The walk now hands back its partial table, and `XTDE3400` fires only for a value not yet recorded. `evaluate-046`. | [`878f9ed`][878f9ed] |
| `../accumulator-after()` was accepted: the Last Call cascade has no climbing rule | Added the Recommendation's rule — a climbing context posture is free-ranging, the parent's post-descent value being unknown. `accumulator-060`. | [`878f9ed`][878f9ed] |
| `fn:accumulator-before` was unmodelled, hiding a consuming `accumulator-after` beside it | §19.8.9.2: grounded and motionless with a motionless argument, else roaming. `accumulator-059`'s pre-descent difference is now refused. | [`878f9ed`][878f9ed] |
| `fn:current` was unmodelled, abandoning every construct containing it | §19.8.9.3 gives the call the outermost expression's context posture (striding in a pattern), and §19.8.1 charges absorbing it by that item's type. | [`ab89b76`][ab89b76] |
| A streamable template rule could return streamed nodes | §18.1 demands a grounded result of an `xsl:stream` body "or of a streamable template rule"; only the former was checked. Applied it to rule bodies too. | [`2eb28b6`][2eb28b6] |
| A path descending from a climbing posture was rescued as a scan | §19.8.8.7's reassessment assumes a striding start, so `for-each select=".."` with a descending body was accepted where `count(../*)` was refused. | [`2eb28b6`][2eb28b6] |
| `fn:accumulator-after` had no streamability rule, so §19.8.9.1 never fired | Absent from the operand-usage table, every call was unmodelled and suppressed its sequence constructor's verdict. Implemented the cascade; rule 8's "enclosing node" inherits into nested constructors. | [`1d10349`][1d10349] |
| A `.`-rooted pattern escaped §19.8.10 entirely | `.[pred]` parses as a filter over the context item, not a step, so the pattern classifier abandoned it and an absorbing predicate was never refused. Classified it like a step. | [`1d10349`][1d10349] |
| `fn:current()` in a pattern was unmodelled | §19.8.9.3 fixes it striding and motionless in a pattern, denoting the node the whole pattern matches; supplying that node's kind separates a consuming element from a motionless text node. | [`1d10349`][1d10349] |
| A call on a stylesheet function inside a streamable instruction was never assessed | The §19.8.5 function table reached the body check but not the instruction analyser, so every such call was unmodelled and suppressed the whole body's verdict. Threaded it through. | [`b4c4bb2`][b4c4bb2] |
| `for` expressions were unmodelled, hiding §19.8.8.11 | §19.8.8.1 makes the return clause a higher-order operand, which turns a streaming-parameter reference inside it roaming. The §19.8.8.2 quantified rule is withheld over a streamed binding: faithful, it refuses three valid stylesheets. | [`b4c4bb2`][b4c4bb2] |
| A schema type's constructor was unreachable through `function-lookup` or `t(?)` | The constructor is registered in no library, so every dynamic route to it reported `XPST0017`. Both now resolve it from the static context, as `t#1` already did. | [`d145807`][d145807] |
| A cast to a union lost the member type that accepted the value | F&O 3.0 §18.3.2 makes the result an instance of that member, but the erased code turned `xs:NCName` into `xs:string`. The member's name now travels with its code. | [`d145807`][d145807] |
| A cast to a union built a QName with no namespace | The prefix resolves against the bindings where the TYPE NAME was written, and `CastAtomic` has none. A resolver is captured there, and an operand already a QName is kept. | [`d145807`][d145807] |
| A cast to a restricted union returned the source type, not the member's | The same §18.3.2 rule, in the impure branch: `s:restrictedUnion('2012-10-08')` was an `xs:string` where an `xs:date` is owed. The atomic members are now tried in order. | [`d145807`][d145807] |
| Five §19.8 rules missing from the streamability analysis let unstreamable stylesheets compile | `fn:outermost`, constructor functions and the `group-starting-with` pattern were unmodelled, the grouping key was assessed grounded in every clause, and a streamed document's body was only checked streamable, not grounded as §18.1 demands. | [`1c43c4e`][1c43c4e] |
| A streamable rule using `current-group()` in its own grouping was refused | §19.8.8.4 widens a union of two striding operands to crawling by its own admission, so `current-group() except .` roamed and `si-group-055` was rejected though the catalog asserts output. Withheld inside the grouping only, which keeps `si-fork-116` refused. | [`d0dd99d`][d0dd99d] |
| A pattern facet was tested against the source value, not the canonical result | F&O 3.0 §18.3.3 tests the pattern against the cast result's canonical form; the engine handed the schema the source's `fn:string` form. `canonicalLexical` answers for the numeric primitives. | [`120e7ec`][120e7ec] |
| A cast to a union's list member returned one item instead of a sequence | F&O 3.0 §18.3.6 makes a cast to a list type a sequence, but the impure-union branch returned the operand unchanged. `SchemaUnionListMemberType` builds the sequence, atomic members tried first. | [`120e7ec`][120e7ec] |
| A schema-defined list or impure union was accepted as an item type | §2.5.4 admits only a generalized atomic type as an ItemType; the purity check covered only the three built-in list types. `SchemaListType` and `SchemaSimpleType` are now `XPST0051`. | [`5f0df59`][5f0df59] |
| A pure union declared as a return type refused an `xs:untypedAtomic` | §3.1.5 casts an `xs:untypedAtomic` to a declared union by trying its members, but a union has no atomic type code and failed the guard in front of the conversion. The guard now admits a pure union. | [`5f0df59`][5f0df59] |
| A namespace-sensitive union converted where §3.1.5 forbids it | §3.1.5 gives `XPTY0117` for a namespace-sensitive target, but the test asked only whether the declared type *was* `xs:QName`, which a union hides. It now walks the union's members. | [`5f0df59`][5f0df59] |
| A duplicate key in an XPath map constructor used XQuery's error code under XSLT | The same duplicate-key error is `XQDY0137` in XQuery §3.11.1 and `XTDE3365` in XSLT §17.4; the engine raised the XQuery code from both hosts. `xpath.Context.MapDuplicateCode` makes it a host property, defaulting to XQuery's. | [`885f6b7`][885f6b7] |
| `xsl:source-document/@use-accumulators` was parsed and discarded | The attribute was accepted and ignored, but §18.2.2 makes it the applicable accumulator set unconditionally on streaming. Parsed with the same `parseUseAccumulators` `xsl:merge-source` used. | [`885f6b7`][885f6b7] |
| `xsl:source-document` withheld every streamability verdict around it | §19.8.4.35 is written for `xsl:stream`, the draft's name for it, so a nested source document read as unmodelled and silenced the enclosing rule. It is grounded, with its href's sweep. | [`f9c0cf5`][f9c0cf5] |
| A streamed `xsl:merge-source` was never checked against §15.4 | §19.8.4.25 measures only the merge's effect on its container. §15.4's four conditions — striding `select`, no `sort-before-merge` — now reject a source that cannot in fact be streamed. | [`f9c0cf5`][f9c0cf5] |
| The body of a template rule in a streamable mode was never assessed | §19.6 makes such a rule a focus-setting container with a striding context posture, so its body is decidable alone — no fixed point over the mode's rules. | [`f9c0cf5`][f9c0cf5] |
| A union of attribute steps was charged as if it could hold children | §19.8.1 downgrades absorption to inspection when the type has no children, and `@* except @length` returns attributes only; a path step now also carries its context item's type, so `@nr/string()` is motionless. | [`f9c0cf5`][f9c0cf5] |
| **The XPath expression rules of §19.8.8 the analysis still lacked** | Unions, map and array constructors, `fn:last` and `fn:position` were unmodelled, so any construct holding one got no verdict. All now in `xslt/streamexprs.go`. | [`0634425`][0634425] |
| **§19.4 type-determined usage, and nine more §19.8.4 instruction rules** | Six instruction rules were held to be blocked on type-determined usage, which is decided by the required type alone. `instrTypeDeterminedUsage` reads it, unblocking `xsl:apply-templates`, `xsl:call-template`, `xsl:next-match` and six more. | [`0634425`][0634425] |
| **A recursive streamable function is assessed, and `fn:reverse`/`fn:innermost` classified** | A recursive `xsl:function` went unassessed, and §19.8.9 had no entry for `fn:reverse` or `fn:innermost`. §19.8.5 resolves a call from its declared category, so one pass is the fixed point. | [`0634425`][0634425] |
| §19.8.10 classified accumulator patterns but never a template rule's | §19.6 gives a rule in a `streamable="yes"` mode a striding context posture, so its match pattern must be motionless; no code followed a mode declaration to its rules. `checkStreamableModePatterns` does, reusing the existing classifier. | [`0634425`][0634425] |
| **The XSLT instruction rules of §19.8.4, on the streamability lattice** | §19.8.4's operand roles for the 43 XSLT instructions, §19.8.6 attribute sets and §19.8.7 value templates were unimplemented. All now in `xslt/streaminstructions.go`; ten instructions stay unmodelled. | [`2cf1ad6`][2cf1ad6] |
| **Streamable stylesheet functions — XSLT 3.0 §19.8.5** | §19.8.5's seven `xsl:function` streamability categories were unimplemented, so the analysis abandoned every call site. All seven now in `xslt/streamfunctions.go`, with §19.8.8.11 and §19.8.8.6. | [`2cf1ad6`][2cf1ad6] |
| **XSLT 3.0 §18.2.8: a streamable accumulator is held to its five conditions** | §18.2.8's five conditions on a `streamable="yes"` accumulator went unchecked. `xslt/streamaccumulators.go` checks all five and raises `XTSE3430`; fixes `accumulator-019s`, `-029s`, `-030s`, `-076`. | [`2cf1ad6`][2cf1ad6] |
| **`fn:string()` and `fn:string(.)` classify identically** | §19.8.1 downgrades an absorption to motionless when the operand cannot have children, but a zero-arity built-in's implicit `.` hardcoded `allowsChildren` true. It now carries the context item's answer. | [`2cf1ad6`][2cf1ad6] |
| A `schema-element(E)` test matched members that could never validate a node | `schema-element(E)` used `Substitutable()`, a content-model answer keeping abstract members (§3.3.6) and non-nillable members under a nillable head (§2.5.5.3). `SchemaElementMembers` filters both. | [`18a6d96`][18a6d96] |
| A function test compared schema type names written in two different alphabets | A sequence type resolved a schema type to Clark notation while `declaredSignature` kept the source text, so `s:myUnionType1` could not match itself. `typeSource` now renders the compiled type. | [`18a6d96`][18a6d96] |
| Schema-defined union and restriction types had no subtype relation | `atomicSubsumes` knew only built-ins, so §2.5.6.2's Judgement 1 went unanswered for schema types. Three clauses answer it from `xsd`'s registries: restriction, union membership, union subset. | [`18a6d96`][18a6d96] |
| A function result converted to an imported schema type failed the type it was converted to | `CastToDerived` stamps the derived name only for built-ins, so §3.1.5's conversion returned a bare `xs:date` for a restriction and failed `MatchesItem`. `castOne` now applies the facets and `WithDerived`. | [`18a6d96`][18a6d96] |
| Lax validation skipped an undeclared element that named its own type | §3.3.4 clause 1.2.1.2 assesses an element against its `xsi:type` whether or not a declaration exists; `ValidateElementLax` tested only for a declaration. The skip now also requires no `xsi:type`. | [`18a6d96`][18a6d96] |
| A path step was accepted after a `validate` expression | `[102] ValidateExpr` is not a `PrimaryExpr`, but operand substitution lifted it into a synthetic call — which is — so `validate { … }/*` parsed where `XPST0003` is owed. Substitution is now abandoned there. | [`18a6d96`][18a6d96] |
| An `<environment>` schema was registered as source text, so its own `xs:import` was never followed | `caseSchemas` handed the schema over as `Source` with no `BaseURI`, so `qischema032.xsd`'s own `xs:import` could not be followed. Now assembled with `xsd.LoadFiles`. | [`18a6d96`][18a6d96] |
| A function declaration could shadow an imported schema type's constructor | §4.15 makes a declaration whose name and arity are already in the static context `XQST0034`, and §4.11 puts one constructor per imported simple type there; nothing registered those, so the loop had nothing to compare against. | [`24c4cca`][24c4cca] |
| Element-test subtyping compared type names for equality, and compared prefixes | XPath 3.1 §2.5.6.2 asks that the subtype's annotation be *derived from* the supertype's; `KindTest.String()` also rendered the author's prefix into the signature, so the `{uri}local` registries were unreachable. Restriction only — `schemaSubsumes` relates a union and its restriction both ways. | [`24c4cca`][24c4cca] |
| `op-same-key/same-key-023` | Not a conformance gap: `map:put`/`map:remove` are O(n) copies of a flat entry slice, measured at 3.66ms/19.2ms per op at 421,875 keys, so the case needs ~2h40m. A persistent trie would fix it; entry order is load-bearing for serialization. | [`24c4cca`][24c4cca] |
| `prod-ContextItemDecl/contextDecl-052` | A W3C fixture defect: `libmodule-3.xq` declares target namespace `…/libmodule1` while the catalog registers `…/libmodule3`, so §4.12's `XQST0059` correctly precedes the wanted `XQST0113`. | [`18a6d96`][18a6d96] |
| `fn:data` on an element with no typed value returned `xs:untypedAtomic("")` | XDM 3.1 §6.2.4 leaves `dm:typed-value` undefined for element-only content and F&O owes `FOTY0012`; the annotation cannot say so, since a mixed type also reads `anyType`. `Node.NoTypedValue` records it and `AtomizeChecked` raises. | [`24c4cca`][24c4cca] |
| A `typeswitch` branch could name a variable nothing binds | §3.14.2 scopes a case variable to its own branch, so a sibling's name is `XPST0008` — but an unreached branch never evaluated and the query answered from another. `checkClauseVars` judges each branch against the live scope, so an outer shadow still resolves. | [`24c4cca`][24c4cca] |
| A library module's prolog was parsed and then thrown away | `libModule` carried only variables, functions and decimal formats, losing §4.16's context-item type constraint and §2.2.4's `XQST0108` for an `output:` declaration. Both now survive compilation. | [`3b4b1e8`][3b4b1e8] |
| `XQST0058` was reported as `XQST0059` because the schema was fetched first | §4.11's ban on a doubled schema namespace is a fault of the prolog's text, but an unresolvable first import aborted with `XQST0059` first. `parseProlog` now pre-scans the prolog textually. | [`3b4b1e8`][3b4b1e8] |
| `current-merge-group()` with an unknown source name was unreachable behind an earlier error | §15.6.1's `XTDE3490` was raised only inside `xsl:merge-action`, which `merge-077` never reaches. `compileMerge` takes §15.6.1's static licence for a literal argument; a computed one stays dynamic. | [`3b4b1e8`][3b4b1e8] |
| A cast target ate the operator after it | `cast as` takes a `[77] SingleType`, whose only indicator is `?`, but `parseSequenceType` ate the `+` in `15 cast as t:sizeType + 15` as one. A `Parser.singleType` flag narrows it. | [`885f6b7`][885f6b7] |
| A URI literal was trimmed where the spec collapses it | A `URILiteral` is an `xs:anyURI`, whose whitespace facet is `collapse`, not the `strings.TrimSpace` the prolog applied. `collapseURI` applies the facet at both import sites and the module declaration. | [`885f6b7`][885f6b7] |
| A library module's context item declaration could carry a value | §4.16 forbids a value on a library module's context item declaration, but the parser accepted one and the query failed later with `XPDY0002`. The check keys on the initialiser, so the type-only form stays legal. | [`885f6b7`][885f6b7] |
| A typeswitch case accepted a type that is not an item type | §3.14.2's `CaseClause` ItemType must be a generalized atomic type, but `parseTypeUntil` skipped the purity gate, so an impure type fell to `default` — a static error turned into a wrong value. | [`885f6b7`][885f6b7] |
| An inline function converted its arguments by different rules than a declared one | Two inline-function implementations disagreed and the *body* decided which ran, sending a simple body's signature to xpath's schema-blind converter. `parseInlineFunc` now keeps ownership of schema types. | [`885f6b7`][885f6b7] |
| Numeric promotion was applied to a union target | §3.1.5 defines promotion against a single target type, not a union, but `castOne` called `CastToUnion` for any atomic. The union cast is now restricted to `xs:untypedAtomic`. | [`885f6b7`][885f6b7] |
| An `<assert>` expression had no context item | The catalog makes the test result both `$result` and the context item; the harness bound only `$result`, so cases writing an absolute path reported `XPDY0002`. A single-item result is now the context item. | [`885f6b7`][885f6b7] |
| A C0 control was delivered into a text node under XML 1.1's rules (XPath) | `fn:json-to-xml` used the XML 1.1 `Char` production, but a JSON string has no character-reference escape, so XML 1.0 applies. A JSON-local predicate now decides. | [`7ad2845`][7ad2845] |
| The built-in schema for the XML representation of JSON was a paraphrase (XSD) | `xsd/jsonschema.go` was commented "§C.2 verbatim" and was not: `j:boolean` was `xs:boolean` where §C.2 gives `j:booleanType`. Replaced with a faithful copy. | [`7ad2845`][7ad2845] |
| A simple-content type's derivation skipped its named base (XSD) | For simple content the assembler registered the type the value atomises as, skipping the schema's own named base, so an element test against it answered false. The named base is now registered. | [`7ad2845`][7ad2845] |
| `fn:json-to-xml` with `validate:true()` could not validate (XQuery) | `xquery` never wired the `xpath.TreeValidator` hook `xslt` has had since `jsonvalidate.go`, so validation always gave `FOJS0004`. Now installed in `prepare`; `fn-xml-to-json` reaches 100%. | [`b361b10`][b361b10] |
| The QT3 harness could not supply a schema named only by an `at` hint | `qischema041` and `qischema083` import a namespace no `<environment>` declares. The harness now reads those files itself; `Options.SchemaResolver` stays nil, so the engine opens no `at` hint. | [`b361b10`][b361b10] |
| Casting to a schema-defined union raised instead of answering | The §2.5 purity rule gated cast targets as well as item types, but §3.14.2 admits any simple type as a cast target. `SchemaSimpleType` now marks a target the schema decides. | [`6654bac`][6654bac] |
| A schema import was installed too late for the prolog's own declarations | Imports were followed at the end of the prolog, too late for a signature parsed where it stands, so a type from the import above was `XPST0051`. Each import is now followed where it is read. | [`6654bac`][6654bac] |
| A cast to a union validated the operand rather than the result | `castToUnion` validated the *operand's* `"123.12"` after the member cast had produced `"123"`, raising `FORG0001` where `123` is owed. A cast converts; it does not validate its input. | [`6654bac`][6654bac] |
| A union member's own facets were skipped by a shortcut | An item whose type code matched a member's was returned untouched, skipping the facets a restriction member carries. The shortcut is now taken only when no schema validation is attached. | [`6654bac`][6654bac] |
| A union's list member was reachable from a non-string source | F&O 3.0 §18.3 defines the cast to a list type from `xs:string` and `xs:untypedAtomic` only, which validation cannot see. `SchemaImpureUnionTypes` reports the atomic members and the source type decides. | [`6654bac`][6654bac] |
| `validate` in tail position evaluated to the empty sequence | `validateExpr.eval` computed its result and threw it away, so `validate lax {…}` in tail position yielded nothing. Invisible until now because `validate strict` always raised first. | [`73d547b`][73d547b] |
| `type=` naming an attribute declaration was accepted (XSD) | §3.3.2 requires `type=` to name a type definition, but a name resolving to an attribute declaration was carried under §3.3.3's deferral, which no later document could satisfy. Non-types are now reported first. | [`830ae11`][830ae11] |
| `keyref refer=` reached a key its document never imported (XSD) | The `refer=` fixup resolved against one flat map over the assembly, reaching a key the asking document never imported (§4.2.6.1 `src-resolve`). It now requires the declaring document's own or an imported namespace. | [`830ae11`][830ae11] |
| Typed mode unchecked under a built-in rule | XTTE3100/XTTE3110 were checked only on an explicit `xsl:apply-templates`, but §6.7.3 and §2.3.3 make the built-in rules and the initial selection the same. The check moved into `applyToNode`. | [`bc72bed`][bc72bed] |
| `typed="false"`, `"0"` and `"unspecified"` read as their opposite | `@typed` is `boolean \| "strict" \| "lax" \| "unspecified"`, but `checkModeTyped` tested only the literal `"no"`, so `"false"`, `"0"` and `"unspecified"` asserted the reverse. | [`bc72bed`][bc72bed] |
| JSON-nested HTML wrote the wrong content-type meta | `json-node-output-method="html"` wrote the HTML5 `<meta charset>` where `output-0702`/`-0716` require the `http-equiv` form the full `xslt` serializer already wrote. The namespace test now matches it. | [`bc72bed`][bc72bed] |
| `fn:function-lookup` hidden from `xsl:evaluate` | §10.4.1 hides the XSLT-defined functions from the *static* context and `restrictedLibrary` applied that to every lookup, but §10.4.2 keeps the dynamic context intact. `LookupDynamic` drops the static hiding. | [`bc72bed`][bc72bed] |
| C0 controls serialized raw | `#x1`-`#x1F` fell past every arm of the escaper into a plain rune write, producing unparseable output. The version now decides: 1.1 writes character references, 1.0 raises `SERE0006`. | [`a45c3a6`][a45c3a6] |
| XML declaration hardcoded `1.0` | `xsl:output/@version="1.1"` was parsed and never reached the declaration. Also mapped `xsl:result-document/@output-version`. | [`a45c3a6`][a45c3a6] |
| Arrays dropped from constructed content | An `*xdm.ArrayItem` matched neither arm of a two-arm type switch, so `('a',[1,2],'b')` yielded `"a b"`. `xdm.Flatten` was already correct and never called. | [`be2938e`][be2938e] |
| Arrays rejected by `xsl:sequence` and `xsl:apply-templates` | `xsl:sequence` handed an array to the builder, which raised `XTDE0450`, and `xsl:apply-templates` dispatched it as one opaque item. §5.7.1 words `XTDE0450` against a *function item*, which an array is not. | [`f536984`][f536984] |
| Constructed trees sorted before parsed ones | `Node.Compare` read a treeless node's tree id as zero and `Order()`'s fixed `1<<20` bias was overrun by tree id 1272658. Detached roots now draw from the parser's own counter. | [`f536984`][f536984] |
| Current group survived a streamed invocation | §14.4 sets the current group and grouping key to absent inside a declared-streamable construct; only the merge group was cleared. The two invocation instructions now clear the grouping scope too. | [`f536984`][f536984] |
| `match="."` rejected below version 3.0 | The XSLT 3.0 `PredicatePattern` was gated on the module's version, so `match="."` in a `version="2.0"` module got `XTSE0340`. It now follows the processor's version, as the `$v` form did. | [`f536984`][f536984] |
| Namespace-node identity | The axis synthesizes a node per walk, so `is` compared two fresh pointers. `Node.Is` now defers to `Order()` and set operators key on `IdentityKey`; `KindNamespace` only. | [`d15b6df`][d15b6df] |
| Library-module variable scope | Bodies were checked against a flat pool of every loaded module's globals, so a module could read `$foo:test` having imported no `foo` (§4.12). | [`d15b6df`][d15b6df] |
| Decimal formats crossed modules | `fn:format-number` in a library module resolved format names against the importing query's prolog rather than its own (§4.4). | [`d15b6df`][d15b6df] |
| One namespace, several modules | `Options.Modules` keyed on namespace alone, so a second registration replaced the first instead of contributing alongside it. | [`d15b6df`][d15b6df] |
| DTD typos disabled validation | A misspelt `#REQUIRED` was read as the default value `"#REQUIRE"`, silently demoting a required attribute to optional. Now recorded as invalid and reported. | [`96171c5`][96171c5] |
| `namespace=""` matched everything | An empty wildcard spelling was defaulted to `##any`, admitting every element (Part 1 §3.10.2). | [`39f7174`][39f7174] |
| Annotations lost on copy | `xsl:result-document` carried a validation's annotation back by name only, dropping `UnionMember`, `DerivedPrimitive` and `ListItem`. | [`39f7174`][39f7174] |
| Optional `<all>` read as a range | Reading a disjunction as a range let an invalid instance validate. | [`39f7174`][39f7174] |
| XML 1.1 documents | Read as 1.1: `[2] Char` and `[2a] RestrictedChar` per version, `NEL` and U+2028 as line ends, and an external entity's version checked against the including document (§4.3.4). | [`2cc633e`][2cc633e] |
| `fn:transform` entry points | `initial-function` and `function-params` were unimplemented and silently dropped, leaving the inner stylesheet with no entry point — the cause of issue #4's intermittent `XTDE0044`. | [`e049991`][e049991] |
| Included-document errors | An error from an included schema did not say which document it came from. | [`9a41bea`][9a41bea] |
| `fn:current-output-uri()` | Reported the stylesheet's own location instead of the output's. | [`bd0aaf5`][bd0aaf5] |
| `xsl:result-document` with no `href` | Produced no output at all from the CLI; it names the principal output. | [`6c8405c`][6c8405c] |
| `validation="lax"` demanded a schema | XTSE1660 names what a non-schema-aware processor must refuse — "other than strip, preserve, or lax" — and lax is not among it. Lax with no schema leaves the node untyped; strict still errors. | [`f536984`][f536984] |
| `xsl:inherit-namespaces` ignored on a literal result element | Implemented for `xsl:element` and `xsl:copy` only, so the blocking pass never ran on an LRE and no namespace undeclaration was ever owed. | [`f536984`][f536984] |
| `xsl:copy` with `copy-namespaces="no"` broke an XDM invariant | The copied element carried no namespace node for its own prefix, so `in-scope-prefixes()` answered `("xml")` alone. §5.8.3 fixup applies whether or not the source's namespace nodes were copied. | [`ea4681f`][ea4681f] |
| `xsl:function` accepted a streamability it cannot have | XTSE3155 was unimplemented: a function with no `xsl:param` children may only declare `streamability="unclassified"`, the other categories describing a streamed argument it does not take. | [`9113ac4`][9113ac4] |
| A result-document href absolute on another platform | `filepath.IsAbs` answers for the host, so `C:/out.xml` was refused on Windows and made a directory named `C:` on Unix. Now refused textually everywhere: leading slash, drive letter, or UNC. | [`28699a9`][28699a9] |
| `xsl:source-document` ignored `xml:base` | The href resolved against the stylesheet module rather than the expression's static base URI. | [`f536984`][f536984] |
| A Windows filename parsed as a URI scheme | `c:\my\doc\books.xml` was read as scheme `c`; backslashes are not legal URI characters on any platform, so it is FODC0005. | [`f536984`][f536984] |
| `fn:transform` codes and options | FOXT0001 where the options identify no stylesheet (QT3 reserves it for an unavailable *product*); an option as element content refused as a text node; an href-less `xsl:result-document` treated as secondary. | [`f536984`][f536984] |
| Four unchecked or unread attributes | `exponent-separator` accepted but never mapped into the format, XTSE0730 unimplemented, `xsl:attribute-set/@streamable` missing from the table, and `xsl:` attribute *values* on an LRE never checked. | [`f536984`][f536984] |
| A cast to a schema list type built tokens from the item type's erased code | `xs:IDREF` became `xs:string` and a union item type nothing, against F&O 3.0 §18.3.6. The item type is now resolved as a full cast target and each token cast through it. | [`aeead08`][aeead08] |
| A union over list types returned the one string it was handed | The list member was looked up by item code, and `xs:IDREFS` is a built-in the schema's type table lacks. The members are now resolved by name and tried in order. | [`aeead08`][aeead08] |

### Fixed — bounds and budgets

The governing rule: *a resource budget may decline to answer, but must never
turn "I could not prove the constraint" into "the constraint holds."*

| Change | Problem → solution | Commit |
|---|---|---|
| XSD validation allocated 16 MB per KB against a long restriction chain | Three base-chain walks ran per validated value, so a 500-link chain cost 999 MB for 64 KB; memoised per type, 2.7 MB. | [`b9c5a5a`][b9c5a5a] |
| The namespace count rebuilt the whole ancestor chain per element | `Tree.assign` took `len(InScopeNamespaces())` per element, making Finalize O(depth²): 224 kB nested 32,000 deep allocated 17.8 GB. The scope is now threaded down the walk; 13.4 MB. | [`eff4094`][eff4094] |
| The per-node cost behind the `MaxNodes` default did not reproduce | The comment claimed 672 bytes of live heap per node and a 1.3 GB cap; measured independently it is 328, capping ~0.6 GB. Safe direction, default unchanged. | [`7c81c60`][7c81c60] |
| A transform's result tree was bounded by nothing | Two nested `xsl:for-each` squared the input: 24 kB built 9M nodes, 44 GB. `xpath.MaxNodes` charges construction. | [`9908bbe`][9908bbe] |
| The RELAX NG pattern-size bound could not fire on the attribute path | Checked once per element, never between attributes, so a 106-byte document did not finish in 60 s even at `MaxPatternSize: 1`. Now checked per attribute. | [`fbe8f3c`][fbe8f3c] |
| A nested DTD content model could exhaust the stack and kill the process | `parseCP`/`parseGroup` recursed unbounded, so 2.5M parens were a `fatal error` `recover()` cannot catch; `maxModelDepth` caps nesting at 1000. | [`020ef7f`][020ef7f] |
| Merging adjacent text nodes was quadratic in their number | `AppendText` re-concatenated the whole run per piece, so 40,000 source text nodes cost 7,946 MB; it now accumulates in a buffer, 171 MB. | [`41ca5dc`][41ca5dc] |
| The text accumulator recovered silently from a state it cannot reach | The restart branch was never entered in 308,423 calls, so its aliasing guard had no test that could fail; it asserts the invariant now, as `strings.Builder` does. | [`33572e9`][33572e9] |
| The entity-expansion budget restarted for every XInclude'd document | Each included parse minted a fresh `entityTable`, so 200 documents got 200 × 1 MB: 95 KB expanded to 149 MB. One `entityBudget` is now shared across the pass, and a refusal is fatal to `xi:fallback`. | [`989e88d`][989e88d] |
| The documented `MaxItems` budget never bound on an XQuery body | `Compiled.Eval` reset the counter once per tuple; `HoldItemBudget` holds it for one query. | [`fe41f3c`][fe41f3c] |
| Uncompilable content models skipped every constraint on them | A model that would not compile passed silently rather than declining. | [`b6fb5ab`][b6fb5ab] |
| A budget answered "valid" | Exhausting the budget was reported as success instead of as an inability to decide. | [`2c461c7`][2c461c7] |
| Budgets counted the wrong thing | A bound over the wrong quantity is not a bound. | [`8dcc4dc`][8dcc4dc] |
| Last unbudgeted load-time algorithm | Now bounded; `Options.MaxContentModelPositions` makes the position budget host-tunable. | [`81e6ee5`][81e6ee5] |
| Substitution closure unbounded | Bounded, along with the pairwise overlap test it fed. | [`1b027e5`][1b027e5] |
| A flat operator chain overflowed the stack at compile time | The depth cap counts nesting, and the attack is length. Every infix loop charges `maxChainLength`. | [`106bcdc`][106bcdc] |
| Constant folding rescanned each subtree per node, so compiling was quadratic in expression size | `isClosed` and `containsCompatSensitive` memoise per node: 640 kB fell from 2.79 s to 215 ms. Reachable from data via `xsl:evaluate`. | [`eb12a69`][eb12a69] |
| `xsd.HTTPResolver` checked host *names* only, so a permitted name reaching loopback or 169.254.169.254 was an SSRF | The dialler now refuses private, loopback and link-local addresses by default, closing the rebinding window; `AllowPrivateAddresses` opts out. | [`9f69069`][9f69069] |
| `file://evil.example.com/etc/x.dtd` read the local `/etc/x.dtd` in `xsd`, `dtd` and `xslt` | The resolvers took the URI path alone, dropping the authority; a `file:` URI naming any host but `localhost` is now refused, as `relaxng` already did. | [`3831726`][3831726] |

### Fixed — test harness

A harness defect and an engine defect are not the same finding: these changed
what the suites *measured*, not what the library does.

| Change | Problem → solution | Commit |
|---|---|---|
| A growth-rate test read CI contention as a quadratic regression | `elementNamesOverlap` measured 10.9x on a shared macOS runner under `-race` against an 8x bound, and 3.4-4.4x locally; the larger closure falls out of cache, not into a nested loop. Best of five trials now, since contention only lengthens a timing. |  |
| `cmd/go-xml`'s `cliResolve` split its base with `strings.LastIndexByte(dir, '/')`, so five RNG resolver cases failed on Windows | The base is an OS path: `C:\...\001\main.rng` has no forward slash, so the directory came out empty and `"top.rng"` resolved outside the root. It now spells the base as a `file:` URI and joins with `url.ResolveReference`, which is the branch of `relaxng.joinRef` the CLI really takes. |  |
| The QT3 and XSLTS runners built every base URI as `"file://" + ToSlash(path)`, so a Windows lane would mis-resolve every case | On Windows the path has no leading slash, making the drive the URI authority; the static base URI, the entity URI and the test-set URI now go through `internal/fileuri`, as do the two `TrimPrefix` inverses that unbuilt them. |  |
| Four refusal tests spelled their hostile `file:` URI by concatenation, so on Windows each was refused for the wrong reason | `"file://evil.example.com" + path` named the host `evil.example.comC:`, and `"file://" + path` does not parse at all. `fileuri.OnHost` and `fileuri.Of` build both shapes; the vectors now reach the guards they name. |  |
| `TestSourceDocumentResolvesAgainstXMLBase` built a base URI as `"file://" + path`, failing on Windows | Two slashes make the drive the URI authority, so `u.Path` loses `C:` and `filepath.Abs` re-anchors it onto the current drive — the file was sought on `D:` while the root was on `C:`. It uses `fileURIOf`, which writes the RFC 8089 three-slash form. |  |
| The `dirURI` and `baseOutputURI` tests hard-coded `file:///tmp/...`, which is not what Windows returns | `/tmp/results` has no drive, so `filepath.Abs` prepends one and the answer is `file:///D:/tmp/results/`. They now assert the trailing-slash and `-o`-precedence properties against `fileURI` of the same path, with the drive spellings covered host-independently. |  | Both expanded unquoted: the flag truncated at the first space, the glob matched nothing, and the corpus skipped in silence. `set --` and `find`. | [`8008194`][8008194] |
| The XSLT skip count folded "not implemented" into "out of scope" | Each reason is classed in `deps.go`; the summary line and `docs/stats.md` publish both counts, and an unclassed reason fails a test. | [`1680456`][1680456] |
| The optimiser timing bound failed on the race lane | `TestOptimizeNotQuadratic` allowed 1.5 s and the race detector took the compile to 1.7 s; the budget is widened sixfold under `-race`, still far below the quadratic figure. | [`d02fc89`][d02fc89] |
| The W3C suites were cloned unpinned, so a figure could move with no change here | `results.json` records each suite revision, CI clones at that SHA, and a test fails when the two disagree. | [`7f70d40`][7f70d40] |
| `-race` never declared that it needs cgo | CI passed only because its runners have gcc; an auditor without one read a toolchain error as a test failure. The lane now skips with a reason. | [`7f70d40`][7f70d40] |
| The gate recorded no provenance, so no figure could be tied to the tree that produced it | `check.sh` heads its transcript with Go version, commit, architecture and per-suite revision, and writes `tests/last-run.txt`. | [`03b5942`][03b5942] |
| CI ran on Linux only, so nothing proved the file handling rule 3 asks for | The fast job runs `ubuntu`/`windows`/`macos`; `conformance` stays Linux, where the corpora are. | [`03b5942`][03b5942] |
| Nine fuzz targets compiled and replayed seeds but never searched | A nightly matrix gives each 300s, off the per-push gate because the search is nondeterministic. | [`f2aeee8`][f2aeee8] |
| Four stale feature labels | `streaming`, `streaming-fallback`, `XPath_3.1` and `XML_1.1` sat in `unsupportedFeatures` long after implementation, hiding 2,862 cases. Each must *move* to `supportedFeatures`, not just be deleted. | [`be2938e`][be2938e] |
| Two more stale labels | `namespace-axis` and `infoset-dtd`; the two harnesses had been contradicting each other. | [`a820213`][a820213] |
| Dependencies merged additively | A set's `satisfied="true"` outlived a case's `"false"`, excluding all fourteen `fn-load-xquery-module-901..914` by the declaration they exist to contradict. Now merged per (type, value). | [`d15b6df`][d15b6df] |
| `SerializeAsXML` forced 1.0 | Tree assertions compared against the truncated prefix of a discarded error. The version, unlike method and indentation, decides whether a character can be written at all. | [`a45c3a6`][a45c3a6] |
| Ratchet read a shrinking corpus as a regression | A count taken over fewer roots is not comparable; it is now skipped, not passed, when a root is absent. | [`3b6e685`][3b6e685] |
| Cases never scored went uncounted | A case that is never scored must still appear in the denominator. | [`c3a52be`][c3a52be] |
| The XSLT judge matched an expected error code anywhere in the rendered message | A code quoted in the input, or a substring of a longer one, passed a case that failed for another reason. The leading code decides now, with the substring kept for the 351 errors that carry theirs in a trailing parenthetical. In-scope unchanged at 11,490. | [`8b7161f`][8b7161f] |
| The regex budget tests never reached the budget | The backtracking engine is off by default, so the probe pattern died at compile time with the same `FORX0002` the tests asserted on; deleting the guard left them green. They now enable the engine and assert `xdm.ErrResourceLimit`, which only the budget wraps. | [`5d1cc6f`][5d1cc6f] |
| The XSLT URI test demanded a Windows separator from a function that never touched the string | Every `want` went through `filepath.FromSlash`, including the two non-`file:` inputs `fileURIToPath` returns unchanged; identity on Unix hid it until Windows CI. Only URI cases convert now, and a companion test asserts the Windows spellings on every platform. |  |
| `speccites.sh` passed green while 16 citations named the wrong subject | It checked only that a number names some section, and only in `xslt/stream*.go`. It now covers `xpath/` too (1058 sites) and fails when a citation under a `case "NAME":` names another instruction's §19.8.4 section. | [`ed625d2`][ed625d2] |
| `tests/qt3` skipped unless `GOXSLT_QT3` was set, so `go test ./...` printed `ok` in 0.3s having run none of 30,345 cases | It now falls back to `../../testdata/qt3tests` exactly as the XSLT lane does, and honours `GOXSLT_NO_SUITES`. A bare run takes ~82s and does real work. | [`3d5e519`][3d5e519] |
| The latin-1 charset case asserted only that the parse succeeded | `café` decoding to `cafi` passed. Each case that must parse now names the decoded string, and the high range and the `latin1` alias are covered too. | [`3d5e519`][3d5e519] |
| The CR round-trip test compared `""` to `""` for its one attribute case | `StringValue()` on the element never reaches an attribute, so writing an attribute CR as a literal LF -- which the next parse normalises away -- looked lossless. The attribute value is read directly now, and a literal CR in the output fails. | [`3d5e519`][3d5e519] |
| `runtimeFuncNames`'s reverse check was a hand-typed roster of 13 of the 19 names | It could only find a name someone had already added to it. The registered set is enumerated from the library instead, with the three stateless XSLT-defined functions named as a deliberate omission and pinned to `lateBoundFuncNames`. | [`3d5e519`][3d5e519] |
| The global-ordering fixture was already in declaration order | Binding `$v:flag` before the global that needs it passed whether or not the sequence constructor was scanned at all. The fixture is reordered so declaration order and dependency order disagree. | [`3d5e519`][3d5e519] |
| Five ratchet marks were in neither figure guard | `TestQT3`, `RelaxNGSpectest`, `DocBook` and `XSpec` copies in README and docs are generated regions now, inline where mid-sentence; `VendoredSchemas` joined `docfigures.sh`'s table. | [`1c7edec`][1c7edec] |
| The QT3 XPath lane's `-run TestQT3` was unanchored, so it also ran `TestQT3XQuery` | The XQuery summary was the last `in-scope:` line, so the `TestQT3` mark recorded the XQuery count. Anchored to `^TestQT3$`; XQuery now runs once per gate, not twice. | [`0d8c028`][0d8c028] |
| The three QT3 XPath versions were ratcheted by nothing | One mark over three subtests measured whichever ran last. `TestQT3XPath20/30/31` guard 15,217 / 19,362 / 21,898 each, attributed by the `=== RUN` line rather than order. | [`0d8c028`][0d8c028] |
| The three published XPath figures were outside `docfigures.sh`'s cross-check | Neither the ratchet nor the script guarded them, so a stale `15,217` / `19,362` / `21,898` in README or docs failed nothing. Each version now has a row with its own denominator. | [`0d8c028`][0d8c028] |
| All four ratchet helpers failed open on an unparseable count | `[ -n "$c" ] || return 0` meant a driver rewording silently disabled the guard and the gate still reported PASS. An unreadable count now fails, naming the mark and printing the input. | [`0d8c028`][0d8c028] |
| The gate rewrote `tests/ratchet.txt` on a new high, dirtying the tree its own provenance then recorded as dirty | An increase now fails with the command to record it; only `GOXSLT_RATCHET=update` writes. A missing mark fails too, since new and deleted are indistinguishable from inside the script. | [`0d8c028`][0d8c028] |

### Documentation

| Change | Problem → solution | Commit |
|---|---|---|
| `-allow-dir` help said "empty disables all of them" while the stylesheet's own directory was always a root | The grant is deliberate — a stylesheet that cannot read the modules beside it is useless — but `-h` denied it, so a service saving uploads beside the stylesheet exposed them to `doc()`. Text corrected, `TestAllowDirHelpMatchesRoots` pins it to `readableRoots`. | [`74350cf`][74350cf] |
| `MaxDepth`'s "a negative value means no limit" did not say what no limit costs | Stack exhaustion is `fatal error: stack overflow`, which `recover()` cannot catch: the process dies rather than the request failing. Now stated in `xslt`, `relaxng` and `xsd`; `xdm` is unaffected, as it clamps a negative to the default. | [`74350cf`][74350cf] |
| Embedders were given no guidance on `xsl:result-document` hrefs | The library never writes a file, so the href reaches the host as a stylesheet-controlled string that `os.Create` would treat as a traversal. `docs/security.md` now points at `writeSecondary` as the reference, including why the write goes through `os.OpenRoot`. | [`74350cf`][74350cf] |
| `xdm` parsing cannot be interrupted, and deep nesting is superlinear | No `context` in the package, so a deadline cannot bound a parse. Recorded in `docs/known-gaps.md` with measurements, naming `ParseOptions.MaxDepth` (default 1000) as the only lever a caller has. | [`74350cf`][74350cf] |
| The element table was reconciled against two documents by hand, with no stated policy | Each divergence from the XSLT 3.0 Recommendation was decided ad hoc in a scattered comment. `docs/element-table-policy.md` names the Recommendation as authoritative, enumerates all nine deliberate divergences with their evidence, and gives the rule for a new one; `TestElementTablePolicyDeclaresEveryDivergence` fails on an undeclared divergence and on a fixed one whose row survives. | [`cc47bf9`][cc47bf9] |
| `xpath/funcspec_table.go` called its table a bootstrap of seventeen entries | It covers the whole F&O manifest and the tests enforce that. The header now states the invariant and names `fn:concat` as the synthesised exception. | [`c5b237c`][c5b237c] |
| The README called `xsl:stream` and `xsl:fork` absent | Both execute end-to-end by building the tree, as §19.1 permits a non-streaming processor to do. The README now says what is absent is streamed execution, not the vocabulary — matching `docs/conformance-gaps.md`. | [`a09c936`][a09c936] |
| The `evaluate-045` divergence was justified by a false spec argument | The comment claimed a plain `xsl:stylesheet` has no package boundary; §3.5 makes it an implicit package whose functions default to private. Reframed as a known divergence kept for cost (577 of 593 DocBook documents against 67), with the corpus size corrected from 613 to 593. | [`c6532ba`][c6532ba] |
| Twelve spec citations named a real section about the wrong subject | The REC renumbered §19.8.4 after the Last Call draft, shifting every instruction after `xsl:text` by two, and four F&O numbers were transcription errors wrong in every version. Each new number was checked against the vendored spec's own heading. | [`ed625d2`][ed625d2] |

### Investigated — not defects

| Finding | Verdict | Commit |
|---|---|---|
| The §19.8.8 `A \|\| A` rule for `\|\|` cannot land alone | Faithful to the table and it refuses `si-fork-952`, but it unshields §19.8.8.4's admitted striding-union widening and costs 18 asserting cases. A U-type inference is not the way in: narrowing a union of sibling name tests was built, gained the two `si-fork` cases, lost `sx-union-202`, and contradicts the spec's own `unordered(a\|b)` and `count((author \| editor))` examples, both crawling. Reverted. | [`5cd6b38`][5cd6b38] |
| `system-property('xsl:supports-streaming')` answers "no" | Correct, and must stay: §26.5 says a non-conforming processor "must return the value no", and "yes" would lie to stylesheets that branch on it to pick a fallback. | [`f536984`][f536984] |
| `merge-097`, `-097s`, `-097sf` | Not interoperable, per the test set's own maintainer comment: they rely on Saxon's `?select=` collection URIs and declare no environment. | [`f536984`][f536984] |
| Unrecognised `fn:transform` options | Ignoring them is correct: `fn-transform-48` is titled "…unrecognised option which is ignored" and asserts success. | [`e049991`][e049991] |
| Two XSD 1.1 "false accepts" | The suite's, not ours. | [`7c4bef2`][7c4bef2] |
| DocBook 5.0's XSD refused | The schema is genuinely invalid. | [`c8fc839`][c8fc839] |
| 22 MS-Regex false accepts | One rule — the same one the XSLT suite exercises. | [`7c4bef2`][7c4bef2] |
| Three "gaps" in XML 1.0 5th-edition names | Not gaps: 1.0 5e adopted 1.1's name productions verbatim. | [`83148b7`][83148b7] |
| `op:same-key` canonical key | Two canonical keys disagree by design; the recurring Gregorian "fix" is wrong. | [`b6fb5ab`][b6fb5ab] |

### Added

| Feature | Notes | Commit |
|---|---|---|
| XQuery `import schema` | Schema import per XQuery 3.1 §4.11, the last structural gap in `xquery`, which until now refused it with `XQST0059`. Components reach the static context before the body parses; nothing is fetched by default. | [`73d547b`][73d547b] |
| DTD external subsets | `dtd.Load` reads the second half of a DTD. Nothing is fetched by default and bounds are shared across subsets. | [`b6fb5ab`][b6fb5ab] |
| RELAX NG compact syntax | `relaxng.CompileCompact`. | [`b6fb5ab`][b6fb5ab] |
| XQuery `import module` | Module import per XQuery 3.1. A cycle is not an error, `Options.ModuleResolver` is nil by default, and `MaxModules` refuses rather than truncates. | [`b6fb5ab`][b6fb5ab] |
| RELAX NG §7.3 | Was refusing seven of nine real-world schemas; `<ref>` expansion is no longer shared between references. | [`7c4bef2`][7c4bef2] |

## v1.2.2 — 2026-09-05

Security and the honesty of the numbers. Measured with `tests/check.sh`:
XPath 2.0, 3.0 and 3.1 all at 100%, XQuery 3.1 at 99.99% (29,800 of 29,803),
XSLT 2.0 at 99.87% (6,149 of 6,157), XSLT 3.0 at 99.85% (8,612 of 8,625),
XSD 1.0 at 99.90% (39,347 agreeing) and 1.1 at 99.91% (41,532), RELAX NG at
100% (965 of 965). DocBook xslTNG 577 of 593 and XSpec 225, with 1,435 unit
tests and 6 fuzz targets clean under `-race`. No conformance figure moved in
either direction: every change here was made without spending a case.

### Headline

| Change | Detail | Commit |
|---|---|---|
| Four silent wrong answers are closed | A schema loaded with a zero-value `xsd.Options` read whatever an `xs:include` named, `/etc/hosts` included. INF and NaN passed every facet a schema could write | — |
| Two bounds did not bound | The regular-expression caches were bounded against a single goroutine and not against several, peaking at 2,984 entries against a limit of 1,024. `FileResolver.Preload` wrote past its own bound | — |
| The documentation is now checked by the gate | Nine conformance marks were already machine-enforced and all nine were correct; the unit test count, which nothing checked, was wrong in four files at once | — |
| One limitation is now written down rather than absent | `checkUPA` is the only load-time algorithm with no budget: its cost is cubic in the size of a content model, and 116 KB of legal schema takes 12 seconds | — |

### Fixed — CI

| Change | Detail | Commit |
|---|---|---|
| The `w3cschemas` module was testing a published release, not this tree | A separate module — the W3C documents it bundles are under W3C terms rather than MIT — and it was in no gate at all: absent from `ci.yml`, absent from `tests/check.sh`, and out of reach of `go list ./...` at the root | — |
| Investigated, not changed: the `XSpec 225` ratchet mark is correct | Reported as stale on the grounds that the corpus measured 224. Three runs — twice at `HEAD`, once at [`e51ed3f`][e51ed3f] — each transformed 225 of 284 inputs with a byte-identical set of 59 failures, so the mark stands | — |
| A unit test that cost minutes broke both CI jobs | `TestMaxPositionsRealBoundary`, added in [`84735c8`][84735c8], compiled three content models of ~8,192 particles to drive `maxPositions` at its edges | — |
| `TestQT3` and the RELAX NG spectest are now ratcheted | Both were run and printed but not recorded, so an XPath 2.0 or RELAX NG count could fall without `check.sh` saying anything | — |

### Security

| Change | Detail | Commit |
|---|---|---|
| Two caches with no bound | A bound on a cache is a property of the cache, so every path that writes to one has to obey it; these two did not | — |
| Those cache bounds held only on one goroutine | The fix above, and the two bounded caches it left in place, shared an idiom that is not atomic as a group: read an atomic size counter, clear the `sync.Map` wholesale if it is full, then `LoadOrStore` the new entry | [`6567f8e`][6567f8e] |
| `xsd` no longer installs an unconfined file resolver by default | `Load`, `LoadFile`, `LoadFiles` and `WithInstanceLocations` each defaulted a nil `Options.Resolver` to a `FileResolver` with no `Root` — "any readable path", by that field's own comment | — |

### Documentation

| Change | Detail | Commit |
|---|---|---|
| `validation-0201` was failing for a reason no document named | Three documents blamed the indent width; setting it to 3 gains nothing. The real difference is a newline before `<style>` in `<head>`, which §5 permits. Now pinned by a test. | — |
| Four claims that the code had already outgrown | Each described behaviour that changed under it, and three of the four understated the risk or overstated what is refused | — |
| A retention claim overstated what was measured | `docs/security.md` read "2,000 distinct schema loads ... show **0.00 MB** heap growth after GC". That holds only when the schemas reuse type names | — |
| Numbers and counts that had drifted apart between documents | An audit compared every stated conformance figure against `tests/ratchet.txt`, which `tests/check.sh` enforces, and every stated count against the list it introduces | — |

### Fixed

| Change | Detail | Commit |
|---|---|---|
| `INF`, `-INF` and `NaN` no longer satisfy every bound facet | `checkBounds` compared with `big.Rat` and read a lexical with no rational form as "no opinion", returning success: with `xs:double` constrained to `0..100`, `101` was correctly refused while `INF`, `-INF` and `NaN` were all accepted | — |
| A derived type could widen a bound to `INF` and the schema still loaded | `compareBoundValues` returned "unordered" for a bound with no rational form and `checkBoundOrder` reads unordered as satisfied, so a restriction that widened its base went unnoticed | [`a883c0a`][a883c0a] |
| A validated `xs:double` that overflowed stopped being a double | Building a typed value from a schema annotation parsed the lexical form with `strconv.ParseFloat` and treated any error as "not a lexical form of this type" | — |
| A processing instruction in an internal DTD subset broke the subset scan | XML 1.0 §2.8 admits a PI to `intSubset` — `markupdecl` is "elementdecl \| AttlistDecl \| EntityDecl \| NotationDecl \| PI \| Comment" — and §2.6 makes its content text rather than markup | — |

### Changed

| Change | Detail | Commit |
|---|---|---|
| A resource refusal can be told apart from a malformed expression | New `xdm.ErrResourceLimit` is a sentinel that resource-exhaustion errors wrap with `%w`, so a caller can ask `errors.Is(err, xdm.ErrResourceLimit)` | — |

### Fixed

| Change | Detail | Commit |
|---|---|---|
| A grammar reached through `<include>` was not checked against section 7 | `collectInclude` (`relaxng/compile.go`) ran `checkSyntax` on the included document but not `checkRestrictions`, unlike the top-level and `<externalRef>` paths, which run both | — |
| Four silent numeric narrowings: a wrong value, with no error raised | `big.Int.Int64` is *undefined* out of range rather than saturating, so an unbounded `xs:integer` argument arrived as its low 64 bits and the result was computed from that — and in each case the wrapped value was itself plausible, so nothing failed | — |
| `fn:round-half-to-even` clamped its precision, which changed answers | The precision argument was clamped to ±4096 before use, which silently changed the answer rather than refusing the request | — |
| A backreference pattern was refused for having too many groups | The backreference path rejected any pattern declaring more than 64 capturing groups with `FORX0002`, reachable from `fn:matches`, `fn:replace`, `fn:tokenize` and `fn:analyze-string` | — |
| `keyref` rediscovered its targets once per enclosing scope | A `keyref` on a self-embedding element walked the whole remaining subtree once per level: nodes visited grew 3.92x, 3.96x, 3.98x as depth doubled, so a hostile instance against a recursive schema cost quadratic work | [`e125888`][e125888] |
| Thirteen derivation walks stopped at 32 or 64 steps | Twelve stopped at 32 and one at 64, each walking a type's derivation chain or a node's copy lineage: five in `xdm`, five in `xpath`, two in `xslt` | [`eb5ea72`][eb5ea72] |
| Nine node-copy sites each hand-picked which type properties to carry | `xdm.Node` records seven PSVI properties — `TypeAnnotation`, `UnionMember`, `DerivedPrimitive`, `ListItem`, `IsID`, `IsIDREFS`, `IsNilled` — and every place that copied a node wrote its own field list | — |
| A second schema silently retyped a document the first had validated | `xdm` keyed `derivedPrimitives`, `listItems` and `unionMembers` by QName alone, process-wide. Mutexes make that race-free, not isolated: the last schema to register a name wins | [`eb5ea72`][eb5ea72] |
| A circular type longer than 4096 links loaded clean | Reachable from a hostile *schema*, not from an instance. `checkTypeBaseCycles` walks a global type's base chain looking for a return to itself, stopped at `steps < 4096`, and appended **no** error on running out — the permissive verdict | [`ad2c3dc`][ad2c3dc] |
| RELAX NG refused a legal chain of 501 definitions | The mirror image of the same mistake, failing in the other direction. `maxRefDepth = 500` was a hard refusal, so a legal chain of 501 definitions became uncompilable with "recurses more than 500 deep" when nothing recursed | — |
| A permitted file was read whole with no byte limit | `readConfined` ended in a bare `io.ReadAll`, so every path through the filesystem resolver read a file entirely into memory before anything could refuse it: `fn:doc`, external entities, `fn:unparsed-text`, and XInclude `parse="text"` | — |
| An ambiguous key came back at three siblings | `mergeTables` dropped a key sequence that two children both defined, because an ancestor's keyref cannot say which of them it resolves to | [`28e455a`][28e455a] |
| The language-inclusion procedure declined any bound above 64 | The subsumption procedure fell back to the structural XSD 1.0 rules above `maxOccurs="64"`, which can refuse a restriction whose language really is a subset | — |
| Six base-chain counters were defects, two of them false accepts | Reachable from a schema rather than from an instance. Eleven remaining `seen > 64` and `seen > 256` counters had been recorded as sound on evidence that did not cover them | — |
| Occurrence arithmetic is exact, not saturating | Saturating arithmetic fixed an earlier wrap and left two bounds above `occursHuge` comparing equal, so a base of 1e30 restricted by three members of 1e30 was accepted | [`f88747b`][f88747b] |
| Identity constraints are linear in the document, not quadratic | Doubling the depth now doubles the work rather than quadrupling it, measured at 2.00x across 240, 480 and 960 where it was 3.98, 3.99 and 4.00. The fix is not a cheaper traversal | [`277599e`][277599e] |
| A 3 KB schema took 35 seconds to load, in two places | Reachable from a hostile schema. A group referencing the next one twice, 29 times over, is acyclic and valid and fits in 3.0 KB; loading it took 35.8 seconds | — |
| Occurrence arithmetic wrapped negative | `occursHuge` is a quarter of the int range, so it survives doubling but not tripling; the derivation checks multiply and sum bounds until they wrap negative | [`145d0d1`][145d0d1] |
| An assertion rejected a valid document 33 elements deep | `maxAnnotateDepth = 32` bounded the walk that types an element and its descendants before an XSD 1.1 assertion runs | [`145d0d1`][145d0d1] |
| Six walks stopped at depth 32 and accepted documents the schema forbids | Reachable from a schema, not from an instance: a deployment with a trusted schema and untrusted documents cannot reach it | [`3f3cce3`][3f3cce3] |
| An iteration that matches nothing is still an iteration | A sweep of 2,028 combinations of outer bounds, inner bounds and child count found 40 still wrong after the count-vector rewrite — every one a false rejection | [`0048fde`][0048fde] |
| A negative `xsd.ValidateOptions.MaxErrors` approved invalid documents | `fail()` stopped recording once `len(v.errs) >= opts.MaxErrors`, with no guard on the limit being positive; at `MaxErrors = -1` the first comparison already held, so nothing was ever recorded | — |
| Filesystem confinement is enforced when the file is opened | `resolvePath` called `EvalSymlinks`, compared against the roots, and the file was opened later — a window an attacker who can write to the filesystem could use | [`5964c0a`][5964c0a] |
| The resolver no longer serialises cache misses | `loadTracked` held its mutex across `os.ReadFile` and the parse, so concurrent transforms sharing one resolver loaded modules one at a time whenever the cache was cold | — |
| The largest byte limit a caller can name is not a refusal | `ParseOptions.MaxBytes` and `xsd.HTTPResolver.MaxBytes` wrap the reader in `io.LimitReader(r, max+1)`, one byte over so that hitting the limit is distinguishable from a document exactly at it | [`f0ffb5b`][f0ffb5b] |
| A schema-aware stylesheet panicked on every run | `ValidateContext` gave `validator` a `ctx` field and a cancellation check on the validation walk; `validateNodeAgainstType` builds the same struct without one, so every schema-aware run dereferenced nil | — |
| An XInclude copy dropped the union member on attributes | `copySubtree` carried `UnionMember` on the element and dropped it on the element's attributes — an inconsistency inside one function, and the same half-omission that silently untyped a validated document at three copy sites elsewhere | [`30dc68d`][30dc68d] |
| XSD: nested occurrence bounds are decided exactly | A repeated group whose only child is itself repeating was decided wrongly in *both* directions — false accepts and false rejections from the same arithmetic | [`17ce36c`][17ce36c] |
| A timezone name was read off a saturated instant | `applyPlace` (`xpath/fn_misc.go`) has to put a value on the timeline before it can ask an Olson zone which offset applied at that moment, and it took the second count with `new(big.Float).SetRat(utc).Int64()` | — |
| The `[0,60)` second invariant is written down instead of re-derived | The `[s]` and `[f]` components of `fn:format-dateTime` each split `dt.Second` into a whole part and a fraction, narrowing the whole part to `int64` | — |

### Changed

| Change | Detail | Commit |
|---|---|---|
| Every configurable limit is now tested at its edges | An off-by-one or an overflow at the boundary of a caller-settable limit is precisely what a unit test should catch before an auditor does, and nothing covered the edges of any limit before | [`2ef8dba`][2ef8dba] |
| `endOfInternalSubset` read comment text as structure | It tracked quotes and brackets but had no comment state, and XML 1.0 §2.8 permits comments in the internal subset while §2.5 says their content is not markup | — |
| `xdm`'s own limits did not carry `xdm.ErrResourceLimit` | The package that *defines* the sentinel, and documents it as how a caller tells "the processor declined" from "your input is wrong", applied it to none of its own limits | — |
| `MaxBytes` did not bound a UTF-16 document at all | `ParseOptions.MaxBytes` is documented as bounding the source document, and the code says the limit "wraps the reader, so it bounds what is read rather than what a caller remembered to check" | — |

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

| Change | Detail | Commit |
|---|---|---|
| A private function of a used package is not callable from outside it | `use-package-003` asked for it and this file had recorded it as needing "the package threaded through the XPath static context", the single largest structural change on the list | — |
| An instruction in 1.0 compatibility mode inside a streamable mode | `XTSE3430`. Section 3.9.1 states the rule "notwithstanding anything stated in 19 Streamability": an instruction processed with XSLT 1.0 behavior *is* roaming and free-ranging | [`7b0562a`][7b0562a] |
| The QT3 per-case deadline was measuring the runner | CI reported XQuery 29,799 passing in one run and 29,798 in another, for the same commit, minutes apart, and the ratchet correctly called the second a regression | — |
| Verdicts that were re-derived and found wrong | `validation-0201` was filed as fixable in the harness -- the suite does license a driver to "ignore differences in the serialization that are known to be irrelevant", and the case is not a serializer test | — |
| Also in this release | Two ratchet marks go **down** and neither is a regression. XSD 1.0 fell from 39,353 to 39,347 and XSD 1.1 rose from 41,525 to 41,532: `indeterminate` expectations stopped being scored as "must be invalid" | — |
| system-property('xsl:product-version') was answering 0.1 | It was a constant nobody edited, so it said `0.1` through the 1.0, 1.1 and 1.2 releases -- and a stylesheet dispatching on it, which is the only reason section 18.2 defines the property, got an answer three tags out of date | — |
| -allow-dir says where, not what | The flag's help named only `xsl:include` and `document()`. It also governs `xsl:import`, `fn:doc`, `fn:unparsed-text`, external entities and XInclude -- one root list for every reader, each of the riskier ones gated by its own flag on top | — |
| An indeterminate expectation is not a demand to reject | `<expected validity="indeterminate"/>` prescribes no result. The W3C uses it where the working group left an area underspecified: `schZ012_a`'s annotation says "The WG decided the spec. is underspecified in this area | [`704222f`][704222f] |
| An XSLT 3.0 static expression can read a document | The static phase built its evaluation context with no document resolver, so `fn:doc` in a `use-when`, a `static="yes"` variable or a shadow attribute always failed with `FODC0002: document access is disabled` | [`a3ec25e`][a3ec25e] |
| The gate no longer runs the conformance suites twice | `tests/check.sh` ran its `unit tests` and `race` steps without `GOXSLT_NO_SUITES`, so in an environment that has `testdata/` on disk — which is exactly the conformance job | — |
| A base URI is a URI, not a path | `fn:static-base-uri` and `fn:base-uri` returned the filesystem path the file was read from — `C:\Users\m\s.xsl` on Windows | — |
| XInclude | `xdm.ProcessXInclude` implements XML Inclusions (XInclude) 1.0, Second Edition, as a pass over an already-parsed tree — which is what the specification says it is: §4 defines XInclude as a transformation from one infoset to another | — |
| One scanner for what is not syntax (internal) | XQuery's parser decides which sub-parser reads an expression by scanning ahead over raw source, and that scan has to step over the regions whose bytes look like grammar but are not: string literals, comments (which nest) | — |
| Internal: a compiled XQuery expression can no longer be evaluated unsafely | No behaviour changes and no conformance movement; this removes the shape of a bug rather than an instance of one | [`b21f5eb`][b21f5eb] |
| The declared XQuery version is recorded | `parseVersionDecl` used to read `xquery version "1.0";`, check the literal against a list of three, and throw it away | [`78f70d5`][78f70d5] |
| XQuery conformance: 99.61% to 99.98% | 29,796 of 29,803 QT3 cases in scope, up 107 across seven passes. XPath 2.0/3.0/3.1 stay at 100% and XSLT at 8,606 / 6,149 | — |
| Fixed — found by real-world stylesheets | Measured against [DocBook xslTNG](https://github.com/docbook/xslt3ng) and [XSpec](https://github.com/xspec/xspec), two XSLT 3.0 codebases large enough to exercise combinations the W3C suites do not reach. 577 of DocBook's 593 test documents now | — |

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

| Change | Detail | Commit |
|---|---|---|
| Security: three bounds that were not bounding | A third audit. Full detail in [docs/security.md](docs/security.md) | — |
| A constraint that never ran against the ordinary spelling | Unique Particle Attribution and Element Declarations Consistent were checked only against a schema's *named* complex types | [`e967628`][e967628] |
| Circularity, and the exception that terminates every chain | Schema validity reaches 99.51% on XSD 1.0 and 99.11% on 1.1 — both at the ceiling this project's own analysis predicted, with the remainder dominated by tests the W3C's metadata disputes | — |
| The harness claimed two mutually exclusive processor configurations | XSD 1.1 schema validity reaches 99.00%, and 10 of the 22 tests gained were never a validator defect. `tests/xsdsuite` claimed support for both `restricted-xpath-in-CTA` and `full-xpath-in-CTA` | [`176ce57`][176ce57] |
| Conditional type assignment, all-groups, and open content | Four rules, +12 on XSD 1.1 with XSD 1.0 untouched | [`176ce57`][176ce57] |
| XSD 1.1 wildcard attributes | Schema validity reaches 99.47% on XSD 1.0 and 98.85% on 1.1. `namespace` and `notNamespace` on a wildcard are mutually exclusive — they are two spellings of one `{namespace constraint}` property | — |
| Restricting xs:anySimpleType, and a contravariant wildcard rule | Schema validity reaches 99.45% on XSD 1.0 and 98.78% on 1.1. `<xs:restriction base="xs:anySimpleType"/>` is now rejected | [`01b91ba`][01b91ba] |
| A valid document was being refused because an assertion crashed | XSD 1.1 defines the default collection in the dynamic context of an assertion or type alternative as the empty sequence | [`da1cde6`][da1cde6] |
| Schema-validity: 99.40% on XSD 1.0, 98.72% on 1.1 | A third round, +29 on 1.0 and +38 on 1.1 with nothing lost. Five rules | — |
| Instance validation reaches 99.88% on XSD 1.0 and 99.89% on 1.1 | Instance disagreements fall from 48 to 31 on 1.0 and from 51 to 29 on 1.1, with no schema-side regression. Nine rules, each measured on its own | [`bb803d5`][bb803d5] |
| Schema-validity: 98.60% to 99.19% on XSD 1.0, 97.96% to 98.48% on 1.1 | A second round adds src-redefine 6.2.2 and 7.2.2 (section 4.2.2): a `<group>` or `<attributeGroup>` redefined without a self-reference must be a valid restriction of what it replaces | — |
| Schema-validity, first round: the constraint that never ran | 122 schema documents that were accepted despite being invalid are now rejected, with no instance test and no other suite moving | — |
| Entity replacement text inside an attribute value is included literally | XML 1.0 section 4.4.5, "Included in Literal": a reference inside an attribute value has its replacement text included *as literal characters*, so a quote in that text is data and does not end the attribute | [`e2606cc`][e2606cc] |
| EXSLT `node-set` | `{http://exslt.org/common}node-set` is available to stylesheets. XSLT 2.0 eliminated the result-tree-fragment type (section J.1.2), so on this processor the conversion is the identity on its argument | — |
| Fixed: a multi-digit backreference to an unclosed group was renumbered | In the backtracking matcher, `\10` written inside the tenth group was split into `\1` followed by a literal `0` rather than being rejected | — |
| Backtracking regular expressions, off by default | `xpath.SetBacktrackingRegex(true)`, or `-backtracking-regex` on the command line, enables a matcher for the backreferences RE2 cannot express: variable-width groups, backreferences in the middle of a pattern, alternation and lazy quantifiers | — |
| XSLT 1.0 backwards-compatible behaviour | `[xsl:]version="1.0"` now enables the behaviour XSLT 2.0 section 3.8 and XPath 2.0 section B.1 define for it, instead of raising XTDE0160. Argument coercion takes the first item of a sequence where 2.0 raises a type error | [`22d2d64`][22d2d64] |
| XSLT 2.0 conformance: 98.83% to 99.61% over a scope that grew | 6,024 of 6,052 in scope, up from 5,982 of 6,053. 43 tests fixed across two rounds, no regressions. XSD 1.1 gained one instance test (26,158 of 26,209); XSD 1.0, QT3 and RELAX NG are unchanged | [`600e7c0`][600e7c0] |

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

## Before v0.1.0

Unversioned work, recorded at the time under headings of its own. Filed
here so every entry in this file sits under a release.

| Change | Detail |
|---|---|
| Four more exact values narrowed without a range proof | The audit that produced the nine fixes in [`cc17983`][cc17983] found four further sites of the same family in the date, duration, and cast paths |
| fn:format-date no longer claims the Julian calendar | `supportedCalendar` accepted `OS` alongside `AD`, `ISO` and the default. `OS` is Old Style — the Julian calendar — not a third spelling of the Gregorian one |
| xslt: a lossy grouping key merged distinct integers into one group | `xsl:for-each-group group-by=` found a group by hashing the key value to a string and looking it up in a map. For an `xs:integer` or `xs:decimal` that string is `xpath.GroupingKey`, which formats the value through a `float64` |
| xslt: system-property and its neighbours truncated a sequence argument | `stringArg` read `atoms[0]` of whatever it was given, so `system-property(('xsl:version','xsl:vendor'))` answered `"3.0"` as though the second item had not been written |
| xpath: [Y] on a BCE year is correct, and is now pinned | `format-date`/`format-dateTime` with `[Y]` renders `xs:dateTime( "-1000000-06-15T12:00:00Z")` as `1000000`, with no minus |
| xsd: an identity field typed as a union compared spellings, not values | Identity-constraint equality is defined on values. `keyString` already builds a type-tagged canonical form for every field before the sequence is joined, so `3.0` and `3` collide as one `xs:decimal`, `007` and `7` as one `xs:integer` |

[020ef7f]: https://github.com/knroy/go-xml/commit/020ef7f
[0aa92ca]: https://github.com/knroy/go-xml/commit/0aa92ca
[0048fde]: https://github.com/knroy/go-xml/commit/0048fde
[01b91ba]: https://github.com/knroy/go-xml/commit/01b91ba
[03b5942]: https://github.com/knroy/go-xml/commit/03b5942
[0634425]: https://github.com/knroy/go-xml/commit/0634425
[0d8c028]: https://github.com/knroy/go-xml/commit/0d8c028
[0f604be]: https://github.com/knroy/go-xml/commit/0f604be
[10486a6]: https://github.com/knroy/go-xml/commit/10486a6
[106bcdc]: https://github.com/knroy/go-xml/commit/106bcdc
[120e7ec]: https://github.com/knroy/go-xml/commit/120e7ec
[145d0d1]: https://github.com/knroy/go-xml/commit/145d0d1
[1680456]: https://github.com/knroy/go-xml/commit/1680456
[176ce57]: https://github.com/knroy/go-xml/commit/176ce57
[17b1c91]: https://github.com/knroy/go-xml/commit/17b1c91
[17bcdb6]: https://github.com/knroy/go-xml/commit/17bcdb6
[17ce36c]: https://github.com/knroy/go-xml/commit/17ce36c
[187dfec]: https://github.com/knroy/go-xml/commit/187dfec
[18a6d96]: https://github.com/knroy/go-xml/commit/18a6d96
[1b027e5]: https://github.com/knroy/go-xml/commit/1b027e5
[1b7a25a]: https://github.com/knroy/go-xml/commit/1b7a25a
[1c43c4e]: https://github.com/knroy/go-xml/commit/1c43c4e
[1d10349]: https://github.com/knroy/go-xml/commit/1d10349
[1e21828]: https://github.com/knroy/go-xml/commit/1e21828
[1e5e26c]: https://github.com/knroy/go-xml/commit/1e5e26c
[56adce5]: https://github.com/knroy/go-xml/commit/56adce5
[b9c5a5a]: https://github.com/knroy/go-xml/commit/b9c5a5a
[eff4094]: https://github.com/knroy/go-xml/commit/eff4094
[9908bbe]: https://github.com/knroy/go-xml/commit/9908bbe
[7c81c60]: https://github.com/knroy/go-xml/commit/7c81c60
[6aa8324]: https://github.com/knroy/go-xml/commit/6aa8324
[1f638d1]: https://github.com/knroy/go-xml/commit/1f638d1
[220b466]: https://github.com/knroy/go-xml/commit/220b466
[22d2d64]: https://github.com/knroy/go-xml/commit/22d2d64
[24c4cca]: https://github.com/knroy/go-xml/commit/24c4cca
[277599e]: https://github.com/knroy/go-xml/commit/277599e
[282953e]: https://github.com/knroy/go-xml/commit/282953e
[28699a9]: https://github.com/knroy/go-xml/commit/28699a9
[28e455a]: https://github.com/knroy/go-xml/commit/28e455a
[2bd65b9]: https://github.com/knroy/go-xml/commit/2bd65b9
[2c461c7]: https://github.com/knroy/go-xml/commit/2c461c7
[2cc633e]: https://github.com/knroy/go-xml/commit/2cc633e
[2cf1ad6]: https://github.com/knroy/go-xml/commit/2cf1ad6
[2eb28b6]: https://github.com/knroy/go-xml/commit/2eb28b6
[2ef8dba]: https://github.com/knroy/go-xml/commit/2ef8dba
[30dc68d]: https://github.com/knroy/go-xml/commit/30dc68d
[33572e9]: https://github.com/knroy/go-xml/commit/33572e9
[34908a7]: https://github.com/knroy/go-xml/commit/34908a7
[35c2e77]: https://github.com/knroy/go-xml/commit/35c2e77
[3672fa3]: https://github.com/knroy/go-xml/commit/3672fa3
[37972d9]: https://github.com/knroy/go-xml/commit/37972d9
[39f7174]: https://github.com/knroy/go-xml/commit/39f7174
[3ae153c]: https://github.com/knroy/go-xml/commit/3ae153c
[3b4b1e8]: https://github.com/knroy/go-xml/commit/3b4b1e8
[3b6e685]: https://github.com/knroy/go-xml/commit/3b6e685
[3d5e519]: https://github.com/knroy/go-xml/commit/3d5e519
[3f3cce3]: https://github.com/knroy/go-xml/commit/3f3cce3
[40930d5]: https://github.com/knroy/go-xml/commit/40930d5
[33263c8]: https://github.com/knroy/go-xml/commit/33263c8
[411fdd5]: https://github.com/knroy/go-xml/commit/411fdd5
[41ca5dc]: https://github.com/knroy/go-xml/commit/41ca5dc
[4410604]: https://github.com/knroy/go-xml/commit/4410604
[4c06a1f]: https://github.com/knroy/go-xml/commit/4c06a1f
[4c78fae]: https://github.com/knroy/go-xml/commit/4c78fae
[4ce4086]: https://github.com/knroy/go-xml/commit/4ce4086
[4fd0df5]: https://github.com/knroy/go-xml/commit/4fd0df5
[5405f36]: https://github.com/knroy/go-xml/commit/5405f36
[57a2b64]: https://github.com/knroy/go-xml/commit/57a2b64
[5964c0a]: https://github.com/knroy/go-xml/commit/5964c0a
[59ee9b9]: https://github.com/knroy/go-xml/commit/59ee9b9
[5c17280]: https://github.com/knroy/go-xml/commit/5c17280
[5cb5358]: https://github.com/knroy/go-xml/commit/5cb5358
[5cd6b38]: https://github.com/knroy/go-xml/commit/5cd6b38
[5d1cc6f]: https://github.com/knroy/go-xml/commit/5d1cc6f
[1c7edec]: https://github.com/knroy/go-xml/commit/1c7edec
[5f0df59]: https://github.com/knroy/go-xml/commit/5f0df59
[600e7c0]: https://github.com/knroy/go-xml/commit/600e7c0
[6567f8e]: https://github.com/knroy/go-xml/commit/6567f8e
[6654bac]: https://github.com/knroy/go-xml/commit/6654bac
[694fe29]: https://github.com/knroy/go-xml/commit/694fe29
[6c8405c]: https://github.com/knroy/go-xml/commit/6c8405c
[6e03e3d]: https://github.com/knroy/go-xml/commit/6e03e3d
[6eacc2d]: https://github.com/knroy/go-xml/commit/6eacc2d
[704222f]: https://github.com/knroy/go-xml/commit/704222f
[73d547b]: https://github.com/knroy/go-xml/commit/73d547b
[74350cf]: https://github.com/knroy/go-xml/commit/74350cf
[75cb3af]: https://github.com/knroy/go-xml/commit/75cb3af
[75d633e]: https://github.com/knroy/go-xml/commit/75d633e
[7668773]: https://github.com/knroy/go-xml/commit/7668773
[78f70d5]: https://github.com/knroy/go-xml/commit/78f70d5
[7ad2845]: https://github.com/knroy/go-xml/commit/7ad2845
[7b0562a]: https://github.com/knroy/go-xml/commit/7b0562a
[7c4bef2]: https://github.com/knroy/go-xml/commit/7c4bef2
[7e7c766]: https://github.com/knroy/go-xml/commit/7e7c766
[7f2d2d0]: https://github.com/knroy/go-xml/commit/7f2d2d0
[7f70d40]: https://github.com/knroy/go-xml/commit/7f70d40
[7ffd7da]: https://github.com/knroy/go-xml/commit/7ffd7da
[8008194]: https://github.com/knroy/go-xml/commit/8008194
[81e6ee5]: https://github.com/knroy/go-xml/commit/81e6ee5
[830ae11]: https://github.com/knroy/go-xml/commit/830ae11
[83148b7]: https://github.com/knroy/go-xml/commit/83148b7
[84735c8]: https://github.com/knroy/go-xml/commit/84735c8
[84ad066]: https://github.com/knroy/go-xml/commit/84ad066
[878f9ed]: https://github.com/knroy/go-xml/commit/878f9ed
[885f6b7]: https://github.com/knroy/go-xml/commit/885f6b7
[8b7161f]: https://github.com/knroy/go-xml/commit/8b7161f
[8dcc4dc]: https://github.com/knroy/go-xml/commit/8dcc4dc
[8e0f44d]: https://github.com/knroy/go-xml/commit/8e0f44d
[9113ac4]: https://github.com/knroy/go-xml/commit/9113ac4
[920fd8a]: https://github.com/knroy/go-xml/commit/920fd8a
[924ef76]: https://github.com/knroy/go-xml/commit/924ef76
[93c5e88]: https://github.com/knroy/go-xml/commit/93c5e88
[96171c5]: https://github.com/knroy/go-xml/commit/96171c5
[9660e52]: https://github.com/knroy/go-xml/commit/9660e52
[9681343]: https://github.com/knroy/go-xml/commit/9681343
[2acbab4]: https://github.com/knroy/go-xml/commit/2acbab4
[989e88d]: https://github.com/knroy/go-xml/commit/989e88d
[9a41bea]: https://github.com/knroy/go-xml/commit/9a41bea
[9ae8c57]: https://github.com/knroy/go-xml/commit/9ae8c57
[9f033e2]: https://github.com/knroy/go-xml/commit/9f033e2
[9f69069]: https://github.com/knroy/go-xml/commit/9f69069
[a048213]: https://github.com/knroy/go-xml/commit/a048213
[a09c437]: https://github.com/knroy/go-xml/commit/a09c437
[a09c936]: https://github.com/knroy/go-xml/commit/a09c936
[a0cf1da]: https://github.com/knroy/go-xml/commit/a0cf1da
[a3ec25e]: https://github.com/knroy/go-xml/commit/a3ec25e
[a3f5568]: https://github.com/knroy/go-xml/commit/a3f5568
[a45c3a6]: https://github.com/knroy/go-xml/commit/a45c3a6
[a820213]: https://github.com/knroy/go-xml/commit/a820213
[a883c0a]: https://github.com/knroy/go-xml/commit/a883c0a
[aa11b38]: https://github.com/knroy/go-xml/commit/aa11b38
[ab053ac]: https://github.com/knroy/go-xml/commit/ab053ac
[ab89b76]: https://github.com/knroy/go-xml/commit/ab89b76
[abc8cbc]: https://github.com/knroy/go-xml/commit/abc8cbc
[ac743d4]: https://github.com/knroy/go-xml/commit/ac743d4
[acdc330]: https://github.com/knroy/go-xml/commit/acdc330
[ad2c3dc]: https://github.com/knroy/go-xml/commit/ad2c3dc
[aeead08]: https://github.com/knroy/go-xml/commit/aeead08
[b21f5eb]: https://github.com/knroy/go-xml/commit/b21f5eb
[b361b10]: https://github.com/knroy/go-xml/commit/b361b10
[b4a8b53]: https://github.com/knroy/go-xml/commit/b4a8b53
[b4c4bb2]: https://github.com/knroy/go-xml/commit/b4c4bb2
[b50b373]: https://github.com/knroy/go-xml/commit/b50b373
[b6fb5ab]: https://github.com/knroy/go-xml/commit/b6fb5ab
[b90c8f5]: https://github.com/knroy/go-xml/commit/b90c8f5
[bb803d5]: https://github.com/knroy/go-xml/commit/bb803d5
[bc72bed]: https://github.com/knroy/go-xml/commit/bc72bed
[bd0aaf5]: https://github.com/knroy/go-xml/commit/bd0aaf5
[be2938e]: https://github.com/knroy/go-xml/commit/be2938e
[bf2ac23]: https://github.com/knroy/go-xml/commit/bf2ac23
[c01b98a]: https://github.com/knroy/go-xml/commit/c01b98a
[c2af54f]: https://github.com/knroy/go-xml/commit/c2af54f
[c3a52be]: https://github.com/knroy/go-xml/commit/c3a52be
[c5b237c]: https://github.com/knroy/go-xml/commit/c5b237c
[c6532ba]: https://github.com/knroy/go-xml/commit/c6532ba
[c852de7]: https://github.com/knroy/go-xml/commit/c852de7
[c8fc839]: https://github.com/knroy/go-xml/commit/c8fc839
[c9945c5]: https://github.com/knroy/go-xml/commit/c9945c5
[cc17983]: https://github.com/knroy/go-xml/commit/cc17983
[cc47bf9]: https://github.com/knroy/go-xml/commit/cc47bf9
[cdba77a]: https://github.com/knroy/go-xml/commit/cdba77a
[d02fc89]: https://github.com/knroy/go-xml/commit/d02fc89
[d0dd99d]: https://github.com/knroy/go-xml/commit/d0dd99d
[d145807]: https://github.com/knroy/go-xml/commit/d145807
[d15b6df]: https://github.com/knroy/go-xml/commit/d15b6df
[d1a9131]: https://github.com/knroy/go-xml/commit/d1a9131
[d63cbb2]: https://github.com/knroy/go-xml/commit/d63cbb2
[d683fd8]: https://github.com/knroy/go-xml/commit/d683fd8
[da1cde6]: https://github.com/knroy/go-xml/commit/da1cde6
[db0448d]: https://github.com/knroy/go-xml/commit/db0448d
[e049991]: https://github.com/knroy/go-xml/commit/e049991
[e125888]: https://github.com/knroy/go-xml/commit/e125888
[e2606cc]: https://github.com/knroy/go-xml/commit/e2606cc
[e3cb35a]: https://github.com/knroy/go-xml/commit/e3cb35a
[e3e3b3a]: https://github.com/knroy/go-xml/commit/e3e3b3a
[e511421]: https://github.com/knroy/go-xml/commit/e511421
[e51ed3f]: https://github.com/knroy/go-xml/commit/e51ed3f
[e812277]: https://github.com/knroy/go-xml/commit/e812277
[e8ebf4b]: https://github.com/knroy/go-xml/commit/e8ebf4b
[e967628]: https://github.com/knroy/go-xml/commit/e967628
[ea4681f]: https://github.com/knroy/go-xml/commit/ea4681f
[eb12a69]: https://github.com/knroy/go-xml/commit/eb12a69
[eb5ea72]: https://github.com/knroy/go-xml/commit/eb5ea72
[ed625d2]: https://github.com/knroy/go-xml/commit/ed625d2
[f05bea8]: https://github.com/knroy/go-xml/commit/f05bea8
[f0ffb5b]: https://github.com/knroy/go-xml/commit/f0ffb5b
[f161723]: https://github.com/knroy/go-xml/commit/f161723
[f29b554]: https://github.com/knroy/go-xml/commit/f29b554
[f2aeee8]: https://github.com/knroy/go-xml/commit/f2aeee8
[f3ff553]: https://github.com/knroy/go-xml/commit/f3ff553
[f536984]: https://github.com/knroy/go-xml/commit/f536984
[f88747b]: https://github.com/knroy/go-xml/commit/f88747b
[f9c0cf5]: https://github.com/knroy/go-xml/commit/f9c0cf5
[f9d96aa]: https://github.com/knroy/go-xml/commit/f9d96aa
[fbe8f3c]: https://github.com/knroy/go-xml/commit/fbe8f3c
[fe41f3c]: https://github.com/knroy/go-xml/commit/fe41f3c
[0af8592]: https://github.com/knroy/go-xml/commit/0af8592
[3831726]: https://github.com/knroy/go-xml/commit/3831726
[ca3ff70]: https://github.com/knroy/go-xml/commit/ca3ff70
[8c0f570]: https://github.com/knroy/go-xml/commit/8c0f570
[ab80fb1]: https://github.com/knroy/go-xml/commit/ab80fb1
[291f67e]: https://github.com/knroy/go-xml/commit/291f67e
[8fcd0d5]: https://github.com/knroy/go-xml/commit/8fcd0d5
[7f7fc4e]: https://github.com/knroy/go-xml/commit/7f7fc4e
[cb43c76]: https://github.com/knroy/go-xml/commit/cb43c76
[7dda133]: https://github.com/knroy/go-xml/commit/7dda133
[01e9f50]: https://github.com/knroy/go-xml/commit/01e9f50
[20214e7]: https://github.com/knroy/go-xml/commit/20214e7
[1c7e368]: https://github.com/knroy/go-xml/commit/1c7e368
[32609d4]: https://github.com/knroy/go-xml/commit/32609d4
[67c960b]: https://github.com/knroy/go-xml/commit/67c960b
[1803696]: https://github.com/knroy/go-xml/commit/1803696
[48651cf]: https://github.com/knroy/go-xml/commit/48651cf
[7798acd]: https://github.com/knroy/go-xml/commit/7798acd
[8cf4584]: https://github.com/knroy/go-xml/commit/8cf4584
[701239a]: https://github.com/knroy/go-xml/commit/701239a
[6e13dee]: https://github.com/knroy/go-xml/commit/6e13dee
[4658352]: https://github.com/knroy/go-xml/commit/4658352
[27f1945]: https://github.com/knroy/go-xml/commit/27f1945
[3c743f4]: https://github.com/knroy/go-xml/commit/3c743f4
[448683d]: https://github.com/knroy/go-xml/commit/448683d
[4d90e69]: https://github.com/knroy/go-xml/commit/4d90e69
[4df1399]: https://github.com/knroy/go-xml/commit/4df1399
[5a20c7b]: https://github.com/knroy/go-xml/commit/5a20c7b
[5cfe76f]: https://github.com/knroy/go-xml/commit/5cfe76f
[5eb354f]: https://github.com/knroy/go-xml/commit/5eb354f
[a0d6a58]: https://github.com/knroy/go-xml/commit/a0d6a58
[abb33c7]: https://github.com/knroy/go-xml/commit/abb33c7
[d758fa2]: https://github.com/knroy/go-xml/commit/d758fa2
[eec3464]: https://github.com/knroy/go-xml/commit/eec3464
[fb64893]: https://github.com/knroy/go-xml/commit/fb64893
[fee47ab]: https://github.com/knroy/go-xml/commit/fee47ab
[d3ce46f]: https://github.com/knroy/go-xml/commit/d3ce46f
[549d113]: https://github.com/knroy/go-xml/commit/549d113
[78c47fb]: https://github.com/knroy/go-xml/commit/78c47fb
[cb604e9]: https://github.com/knroy/go-xml/commit/cb604e9
[ca09e14]: https://github.com/knroy/go-xml/commit/ca09e14
[04ddeea]: https://github.com/knroy/go-xml/commit/04ddeea
[eb6901e]: https://github.com/knroy/go-xml/commit/eb6901e
[197eaad]: https://github.com/knroy/go-xml/commit/197eaad
[cec5f6f]: https://github.com/knroy/go-xml/commit/cec5f6f
[416ee50]: https://github.com/knroy/go-xml/commit/416ee50
[3727b35]: https://github.com/knroy/go-xml/commit/3727b35
[dccd679]: https://github.com/knroy/go-xml/commit/dccd679
[262be91]: https://github.com/knroy/go-xml/commit/262be91
[4457808]: https://github.com/knroy/go-xml/commit/4457808
[1b65291]: https://github.com/knroy/go-xml/commit/1b65291
[1e2bad6]: https://github.com/knroy/go-xml/commit/1e2bad6
[91beb30]: https://github.com/knroy/go-xml/commit/91beb30
[5912a5a]: https://github.com/knroy/go-xml/commit/5912a5a
[81d16d5]: https://github.com/knroy/go-xml/commit/81d16d5
[8a792f7]: https://github.com/knroy/go-xml/commit/8a792f7
[6f63564]: https://github.com/knroy/go-xml/commit/6f63564
[87cf96e]: https://github.com/knroy/go-xml/commit/87cf96e
[2a7843f]: https://github.com/knroy/go-xml/commit/2a7843f
[72cb46a]: https://github.com/knroy/go-xml/commit/72cb46a
[53cff35]: https://github.com/knroy/go-xml/commit/53cff35
[659fdc8]: https://github.com/knroy/go-xml/commit/659fdc8
[b83418a]: https://github.com/knroy/go-xml/commit/b83418a
[06c0df1]: https://github.com/knroy/go-xml/commit/06c0df1
[18ef0f0]: https://github.com/knroy/go-xml/commit/18ef0f0
[eac3823]: https://github.com/knroy/go-xml/commit/eac3823
[aefdaea]: https://github.com/knroy/go-xml/commit/aefdaea
[d077c5a]: https://github.com/knroy/go-xml/commit/d077c5a
[4d31869]: https://github.com/knroy/go-xml/commit/4d31869
[b5864c4]: https://github.com/knroy/go-xml/commit/b5864c4
[738c1fb]: https://github.com/knroy/go-xml/commit/738c1fb
[7dce099]: https://github.com/knroy/go-xml/commit/7dce099
[ca3e6a1]: https://github.com/knroy/go-xml/commit/ca3e6a1
[77cdd65]: https://github.com/knroy/go-xml/commit/77cdd65
[ec72376]: https://github.com/knroy/go-xml/commit/ec72376
[655311a]: https://github.com/knroy/go-xml/commit/655311a
[a5b4546]: https://github.com/knroy/go-xml/commit/a5b4546
[3f87498]: https://github.com/knroy/go-xml/commit/3f87498
[77c78e9]: https://github.com/knroy/go-xml/commit/77c78e9
[b13e8f4]: https://github.com/knroy/go-xml/commit/b13e8f4
[092bdce]: https://github.com/knroy/go-xml/commit/092bdce
[373da4b]: https://github.com/knroy/go-xml/commit/373da4b
[5c2ca9c]: https://github.com/knroy/go-xml/commit/5c2ca9c
[130360b]: https://github.com/knroy/go-xml/commit/130360b
[8082a2c]: https://github.com/knroy/go-xml/commit/8082a2c
[edea87c]: https://github.com/knroy/go-xml/commit/edea87c
[e7ec81d]: https://github.com/knroy/go-xml/commit/e7ec81d
[739c744]: https://github.com/knroy/go-xml/commit/739c744
[d006ad2]: https://github.com/knroy/go-xml/commit/d006ad2
[abd214b]: https://github.com/knroy/go-xml/commit/abd214b
[007e682]: https://github.com/knroy/go-xml/commit/007e682
[3fc468a]: https://github.com/knroy/go-xml/commit/3fc468a
[0602694]: https://github.com/knroy/go-xml/commit/0602694
[2514a9a]: https://github.com/knroy/go-xml/commit/2514a9a
[b88105e]: https://github.com/knroy/go-xml/commit/b88105e
[549d8db]: https://github.com/knroy/go-xml/commit/549d8db
[aa7d1e2]: https://github.com/knroy/go-xml/commit/aa7d1e2
[8fb6f0b]: https://github.com/knroy/go-xml/commit/8fb6f0b
[68795ba]: https://github.com/knroy/go-xml/commit/68795ba
[ebd1759]: https://github.com/knroy/go-xml/commit/ebd1759
[b0c7b30]: https://github.com/knroy/go-xml/commit/b0c7b30
[3cd86ee]: https://github.com/knroy/go-xml/commit/3cd86ee
[ba37f7c]: https://github.com/knroy/go-xml/commit/ba37f7c
[a931b98]: https://github.com/knroy/go-xml/commit/a931b98
[7ad91bd]: https://github.com/knroy/go-xml/commit/7ad91bd
[3b06e4c]: https://github.com/knroy/go-xml/commit/3b06e4c
[8e63b9d]: https://github.com/knroy/go-xml/commit/8e63b9d
[5fbea36]: https://github.com/knroy/go-xml/commit/5fbea36
[8aa5ab7]: https://github.com/knroy/go-xml/commit/8aa5ab7
[740c22a]: https://github.com/knroy/go-xml/commit/740c22a
[2402bca]: https://github.com/knroy/go-xml/commit/2402bca
[987e24e]: https://github.com/knroy/go-xml/commit/987e24e
[39c6931]: https://github.com/knroy/go-xml/commit/39c6931
[d86f80c]: https://github.com/knroy/go-xml/commit/d86f80c
[81b0a27]: https://github.com/knroy/go-xml/commit/81b0a27
[17b99c3]: https://github.com/knroy/go-xml/commit/17b99c3
[3d7e86b]: https://github.com/knroy/go-xml/commit/3d7e86b
[97148b4]: https://github.com/knroy/go-xml/commit/97148b4
[7496ea8]: https://github.com/knroy/go-xml/commit/7496ea8
[efd4178]: https://github.com/knroy/go-xml/commit/efd4178
[02905e9]: https://github.com/knroy/go-xml/commit/02905e9
[0ca7dac]: https://github.com/knroy/go-xml/commit/0ca7dac
[d212f7a]: https://github.com/knroy/go-xml/commit/d212f7a
[e03ee3d]: https://github.com/knroy/go-xml/commit/e03ee3d
[b7853d6]: https://github.com/knroy/go-xml/commit/b7853d6
[b707e39]: https://github.com/knroy/go-xml/commit/b707e39
[8cfeef4]: https://github.com/knroy/go-xml/commit/8cfeef4
[569af46]: https://github.com/knroy/go-xml/commit/569af46
[1253f9a]: https://github.com/knroy/go-xml/commit/1253f9a
[c008a63]: https://github.com/knroy/go-xml/commit/c008a63
[b764d67]: https://github.com/knroy/go-xml/commit/b764d67
[0604061]: https://github.com/knroy/go-xml/commit/0604061
[1f80934]: https://github.com/knroy/go-xml/commit/1f80934
[84bbe23]: https://github.com/knroy/go-xml/commit/84bbe23
[a2603d3]: https://github.com/knroy/go-xml/commit/a2603d3
[262366d]: https://github.com/knroy/go-xml/commit/262366d
[9bd1c79]: https://github.com/knroy/go-xml/commit/9bd1c79
[4f0cc55]: https://github.com/knroy/go-xml/commit/4f0cc55
[1c7fb99]: https://github.com/knroy/go-xml/commit/1c7fb99
[6ce285f]: https://github.com/knroy/go-xml/commit/6ce285f
[e00735a]: https://github.com/knroy/go-xml/commit/e00735a
[2aa49d8]: https://github.com/knroy/go-xml/commit/2aa49d8
[62c7117]: https://github.com/knroy/go-xml/commit/62c7117
[949e0fa]: https://github.com/knroy/go-xml/commit/949e0fa
[bcae9bd]: https://github.com/knroy/go-xml/commit/bcae9bd
[446432b]: https://github.com/knroy/go-xml/commit/446432b
[c7769f5]: https://github.com/knroy/go-xml/commit/c7769f5
[b44313c]: https://github.com/knroy/go-xml/commit/b44313c
[d8f0ac1]: https://github.com/knroy/go-xml/commit/d8f0ac1
[c9c7c79]: https://github.com/knroy/go-xml/commit/c9c7c79
[5cf8104]: https://github.com/knroy/go-xml/commit/5cf8104
[d029347]: https://github.com/knroy/go-xml/commit/d029347
[6e6fa8a]: https://github.com/knroy/go-xml/commit/6e6fa8a
[f18912e]: https://github.com/knroy/go-xml/commit/f18912e
[5549c2f]: https://github.com/knroy/go-xml/commit/5549c2f
[900cece]: https://github.com/knroy/go-xml/commit/900cece
[ba09cac]: https://github.com/knroy/go-xml/commit/ba09cac
[1a3cf7f]: https://github.com/knroy/go-xml/commit/1a3cf7f
[9af0e77]: https://github.com/knroy/go-xml/commit/9af0e77
[13c4f5f]: https://github.com/knroy/go-xml/commit/13c4f5f
[1e976a3]: https://github.com/knroy/go-xml/commit/1e976a3
[21105fe]: https://github.com/knroy/go-xml/commit/21105fe
[2c5ea0b]: https://github.com/knroy/go-xml/commit/2c5ea0b
[2dd09c0]: https://github.com/knroy/go-xml/commit/2dd09c0
[3208c54]: https://github.com/knroy/go-xml/commit/3208c54
[3f33dce]: https://github.com/knroy/go-xml/commit/3f33dce
[40dbca2]: https://github.com/knroy/go-xml/commit/40dbca2
[47db4aa]: https://github.com/knroy/go-xml/commit/47db4aa
[484cb4e]: https://github.com/knroy/go-xml/commit/484cb4e
[4954bf0]: https://github.com/knroy/go-xml/commit/4954bf0
[496f46a]: https://github.com/knroy/go-xml/commit/496f46a
[4f6ed50]: https://github.com/knroy/go-xml/commit/4f6ed50
[5509745]: https://github.com/knroy/go-xml/commit/5509745
[5aa004e]: https://github.com/knroy/go-xml/commit/5aa004e
[63155c7]: https://github.com/knroy/go-xml/commit/63155c7
[6b34bc3]: https://github.com/knroy/go-xml/commit/6b34bc3
[7c5121c]: https://github.com/knroy/go-xml/commit/7c5121c
[7f6f168]: https://github.com/knroy/go-xml/commit/7f6f168
[86f991c]: https://github.com/knroy/go-xml/commit/86f991c
[8e29a38]: https://github.com/knroy/go-xml/commit/8e29a38
[8faf349]: https://github.com/knroy/go-xml/commit/8faf349
[a0250dd]: https://github.com/knroy/go-xml/commit/a0250dd
[a81dff2]: https://github.com/knroy/go-xml/commit/a81dff2
[a8e5346]: https://github.com/knroy/go-xml/commit/a8e5346
[ae36039]: https://github.com/knroy/go-xml/commit/ae36039
[ae4f4d1]: https://github.com/knroy/go-xml/commit/ae4f4d1
[b08a3d4]: https://github.com/knroy/go-xml/commit/b08a3d4
[babd534]: https://github.com/knroy/go-xml/commit/babd534
[c249ce3]: https://github.com/knroy/go-xml/commit/c249ce3
[c5184cb]: https://github.com/knroy/go-xml/commit/c5184cb
[d337fd9]: https://github.com/knroy/go-xml/commit/d337fd9
[d3f4824]: https://github.com/knroy/go-xml/commit/d3f4824
[d5a980b]: https://github.com/knroy/go-xml/commit/d5a980b
[da27a7c]: https://github.com/knroy/go-xml/commit/da27a7c
[dcf9d53]: https://github.com/knroy/go-xml/commit/dcf9d53
[ea89438]: https://github.com/knroy/go-xml/commit/ea89438
[f64b0d1]: https://github.com/knroy/go-xml/commit/f64b0d1
[fb2426e]: https://github.com/knroy/go-xml/commit/fb2426e
[fb7a15b]: https://github.com/knroy/go-xml/commit/fb7a15b
[fbd4b74]: https://github.com/knroy/go-xml/commit/fbd4b74
[50ccc85]: https://github.com/knroy/go-xml/commit/50ccc85
[18efb89]: https://github.com/knroy/go-xml/commit/18efb89
[27f463d]: https://github.com/knroy/go-xml/commit/27f463d
[b8f35ea]: https://github.com/knroy/go-xml/commit/b8f35ea
[b76c85d]: https://github.com/knroy/go-xml/commit/b76c85d
[8b7aab2]: https://github.com/knroy/go-xml/commit/8b7aab2
[4264de8]: https://github.com/knroy/go-xml/commit/4264de8
[599b2f9]: https://github.com/knroy/go-xml/commit/599b2f9
[b42f04a]: https://github.com/knroy/go-xml/commit/b42f04a
[20958aa]: https://github.com/knroy/go-xml/commit/20958aa
[bd865e4]: https://github.com/knroy/go-xml/commit/bd865e4
[8dcb07f]: https://github.com/knroy/go-xml/commit/8dcb07f
[a5b385d]: https://github.com/knroy/go-xml/commit/a5b385d
[18d374d]: https://github.com/knroy/go-xml/commit/18d374d
[9e13f70]: https://github.com/knroy/go-xml/commit/9e13f70
[ec1ac54]: https://github.com/knroy/go-xml/commit/ec1ac54
[2ad3a32]: https://github.com/knroy/go-xml/commit/2ad3a32
[a5ab736]: https://github.com/knroy/go-xml/commit/a5ab736
[4440665]: https://github.com/knroy/go-xml/commit/4440665
[6345114]: https://github.com/knroy/go-xml/commit/6345114
[5d8fceb]: https://github.com/knroy/go-xml/commit/5d8fceb
[e056467]: https://github.com/knroy/go-xml/commit/e056467
[f09c1a3]: https://github.com/knroy/go-xml/commit/f09c1a3
[14c84b5]: https://github.com/knroy/go-xml/commit/14c84b5
[ba9e7a0]: https://github.com/knroy/go-xml/commit/ba9e7a0
[f890740]: https://github.com/knroy/go-xml/commit/f890740
[a8d6e7b]: https://github.com/knroy/go-xml/commit/a8d6e7b
[24b963c]: https://github.com/knroy/go-xml/commit/24b963c
[73af7a8]: https://github.com/knroy/go-xml/commit/73af7a8
[f148247]: https://github.com/knroy/go-xml/commit/f148247
[70ff980]: https://github.com/knroy/go-xml/commit/70ff980
[ef5f064]: https://github.com/knroy/go-xml/commit/ef5f064
[490fc5a]: https://github.com/knroy/go-xml/commit/490fc5a
[dcb15d1]: https://github.com/knroy/go-xml/commit/dcb15d1
[e3b6288]: https://github.com/knroy/go-xml/commit/e3b6288
[b5aaa52]: https://github.com/knroy/go-xml/commit/b5aaa52
[b6d4e4e]: https://github.com/knroy/go-xml/commit/b6d4e4e
[327b09e]: https://github.com/knroy/go-xml/commit/327b09e
[cf65d62]: https://github.com/knroy/go-xml/commit/cf65d62
[f235795]: https://github.com/knroy/go-xml/commit/f235795
[9496810]: https://github.com/knroy/go-xml/commit/9496810
[37206b1]: https://github.com/knroy/go-xml/commit/37206b1
[c501fc4]: https://github.com/knroy/go-xml/commit/c501fc4
[1f18b1b]: https://github.com/knroy/go-xml/commit/1f18b1b
[beb6072]: https://github.com/knroy/go-xml/commit/beb6072
[59ac569]: https://github.com/knroy/go-xml/commit/59ac569
[c1c2bb9]: https://github.com/knroy/go-xml/commit/c1c2bb9
[4e8f0a5]: https://github.com/knroy/go-xml/commit/4e8f0a5
[21995a6]: https://github.com/knroy/go-xml/commit/21995a6
[702c6e6]: https://github.com/knroy/go-xml/commit/702c6e6
[e139525]: https://github.com/knroy/go-xml/commit/e139525
[9c3a983]: https://github.com/knroy/go-xml/commit/9c3a983
[de4a515]: https://github.com/knroy/go-xml/commit/de4a515
[9f0db9c]: https://github.com/knroy/go-xml/commit/9f0db9c
[5dc120c]: https://github.com/knroy/go-xml/commit/5dc120c
[da05b16]: https://github.com/knroy/go-xml/commit/da05b16
[967dfb3]: https://github.com/knroy/go-xml/commit/967dfb3
[3b71501]: https://github.com/knroy/go-xml/commit/3b71501
[69414ae]: https://github.com/knroy/go-xml/commit/69414ae
[887d5d3]: https://github.com/knroy/go-xml/commit/887d5d3
[8577906]: https://github.com/knroy/go-xml/commit/8577906
[30c7c07]: https://github.com/knroy/go-xml/commit/30c7c07
[5951f24]: https://github.com/knroy/go-xml/commit/5951f24
[014a862]: https://github.com/knroy/go-xml/commit/014a862
[dcb49b2]: https://github.com/knroy/go-xml/commit/dcb49b2
[c73a06b]: https://github.com/knroy/go-xml/commit/c73a06b
[a3d8ef6]: https://github.com/knroy/go-xml/commit/a3d8ef6
[40cd52c]: https://github.com/knroy/go-xml/commit/40cd52c
[b35ec14]: https://github.com/knroy/go-xml/commit/b35ec14
[f3449f7]: https://github.com/knroy/go-xml/commit/f3449f7
[f49ff92]: https://github.com/knroy/go-xml/commit/f49ff92
[44229aa]: https://github.com/knroy/go-xml/commit/44229aa
[a9ad280]: https://github.com/knroy/go-xml/commit/a9ad280
[be5afc3]: https://github.com/knroy/go-xml/commit/be5afc3
[676ad52]: https://github.com/knroy/go-xml/commit/676ad52
[40085f6]: https://github.com/knroy/go-xml/commit/40085f6
[a4f316d]: https://github.com/knroy/go-xml/commit/a4f316d
[b5aaf06]: https://github.com/knroy/go-xml/commit/b5aaf06
[15273c7]: https://github.com/knroy/go-xml/commit/15273c7
[a6f2eaa]: https://github.com/knroy/go-xml/commit/a6f2eaa
[6bab64a]: https://github.com/knroy/go-xml/commit/6bab64a
[6ea432f]: https://github.com/knroy/go-xml/commit/6ea432f
[951f0d9]: https://github.com/knroy/go-xml/commit/951f0d9
[f650a19]: https://github.com/knroy/go-xml/commit/f650a19
[4b0b564]: https://github.com/knroy/go-xml/commit/4b0b564
[469c95e]: https://github.com/knroy/go-xml/commit/469c95e
[6293e98]: https://github.com/knroy/go-xml/commit/6293e98
[996b7db]: https://github.com/knroy/go-xml/commit/996b7db
[331bc96]: https://github.com/knroy/go-xml/commit/331bc96
[98811f6]: https://github.com/knroy/go-xml/commit/98811f6
[6317d49]: https://github.com/knroy/go-xml/commit/6317d49
[b37796b]: https://github.com/knroy/go-xml/commit/b37796b
[73249e2]: https://github.com/knroy/go-xml/commit/73249e2
[127b2f2]: https://github.com/knroy/go-xml/commit/127b2f2
[5d6454a]: https://github.com/knroy/go-xml/commit/5d6454a
[22d32ac]: https://github.com/knroy/go-xml/commit/22d32ac
[b331aa1]: https://github.com/knroy/go-xml/commit/b331aa1
[567aa1a]: https://github.com/knroy/go-xml/commit/567aa1a
[ad8c47a]: https://github.com/knroy/go-xml/commit/ad8c47a
[707ca28]: https://github.com/knroy/go-xml/commit/707ca28
[5475837]: https://github.com/knroy/go-xml/commit/5475837
[b08624c]: https://github.com/knroy/go-xml/commit/b08624c
[9572c44]: https://github.com/knroy/go-xml/commit/9572c44
[fd9750e]: https://github.com/knroy/go-xml/commit/fd9750e
[5712d9f]: https://github.com/knroy/go-xml/commit/5712d9f
[3f185fe]: https://github.com/knroy/go-xml/commit/3f185fe
[27757b3]: https://github.com/knroy/go-xml/commit/27757b3
[b4b6052]: https://github.com/knroy/go-xml/commit/b4b6052
[80f999f]: https://github.com/knroy/go-xml/commit/80f999f
[302c932]: https://github.com/knroy/go-xml/commit/302c932
[ac4973e]: https://github.com/knroy/go-xml/commit/ac4973e
[fcab85a]: https://github.com/knroy/go-xml/commit/fcab85a
[e1b5e1f]: https://github.com/knroy/go-xml/commit/e1b5e1f
[c60f8f3]: https://github.com/knroy/go-xml/commit/c60f8f3
[09f0c5d]: https://github.com/knroy/go-xml/commit/09f0c5d
[546af03]: https://github.com/knroy/go-xml/commit/546af03
[b4cd614]: https://github.com/knroy/go-xml/commit/b4cd614
[907c674]: https://github.com/knroy/go-xml/commit/907c674
[c7952b0]: https://github.com/knroy/go-xml/commit/c7952b0
[b5703be]: https://github.com/knroy/go-xml/commit/b5703be
[8c391d8]: https://github.com/knroy/go-xml/commit/8c391d8
[ff0ed5e]: https://github.com/knroy/go-xml/commit/ff0ed5e
[ca1b029]: https://github.com/knroy/go-xml/commit/ca1b029
[268acce]: https://github.com/knroy/go-xml/commit/268acce
[7920635]: https://github.com/knroy/go-xml/commit/7920635
[b12ecbf]: https://github.com/knroy/go-xml/commit/b12ecbf
[e94cdf9]: https://github.com/knroy/go-xml/commit/e94cdf9
[457aa03]: https://github.com/knroy/go-xml/commit/457aa03
[9f3d815]: https://github.com/knroy/go-xml/commit/9f3d815
[48b1a38]: https://github.com/knroy/go-xml/commit/48b1a38
[3157566]: https://github.com/knroy/go-xml/commit/3157566
[4944511]: https://github.com/knroy/go-xml/commit/4944511
[1f0c721]: https://github.com/knroy/go-xml/commit/1f0c721
[f4a7e22]: https://github.com/knroy/go-xml/commit/f4a7e22
[0824c9c]: https://github.com/knroy/go-xml/commit/0824c9c
[a31a4fe]: https://github.com/knroy/go-xml/commit/a31a4fe
[9155044]: https://github.com/knroy/go-xml/commit/9155044
[19b9185]: https://github.com/knroy/go-xml/commit/19b9185
[18670ae]: https://github.com/knroy/go-xml/commit/18670ae
[68b9320]: https://github.com/knroy/go-xml/commit/68b9320
[383ed3d]: https://github.com/knroy/go-xml/commit/383ed3d
[368b6c0]: https://github.com/knroy/go-xml/commit/368b6c0
[0201119]: https://github.com/knroy/go-xml/commit/0201119
[b2f4cb9]: https://github.com/knroy/go-xml/commit/b2f4cb9
[9ac0f40]: https://github.com/knroy/go-xml/commit/9ac0f40
[edd4923]: https://github.com/knroy/go-xml/commit/edd4923
[2f825da]: https://github.com/knroy/go-xml/commit/2f825da
[acc5556]: https://github.com/knroy/go-xml/commit/acc5556
[df9677e]: https://github.com/knroy/go-xml/commit/df9677e
[fd31bb3]: https://github.com/knroy/go-xml/commit/fd31bb3
[382983c]: https://github.com/knroy/go-xml/commit/382983c
[22af5f4]: https://github.com/knroy/go-xml/commit/22af5f4
[d1a98a8]: https://github.com/knroy/go-xml/commit/d1a98a8
[bdf45d8]: https://github.com/knroy/go-xml/commit/bdf45d8
[c8c0a01]: https://github.com/knroy/go-xml/commit/c8c0a01
[fe1eb18]: https://github.com/knroy/go-xml/commit/fe1eb18
[009d510]: https://github.com/knroy/go-xml/commit/009d510
[09174d4]: https://github.com/knroy/go-xml/commit/09174d4
[e629035]: https://github.com/knroy/go-xml/commit/e629035
[48b2598]: https://github.com/knroy/go-xml/commit/48b2598
[2f9f1b4]: https://github.com/knroy/go-xml/commit/2f9f1b4
[41ec3ba]: https://github.com/knroy/go-xml/commit/41ec3ba
[e65c621]: https://github.com/knroy/go-xml/commit/e65c621
[7765fca]: https://github.com/knroy/go-xml/commit/7765fca
[75d485f]: https://github.com/knroy/go-xml/commit/75d485f
[966b4ac]: https://github.com/knroy/go-xml/commit/966b4ac
[aea7acd]: https://github.com/knroy/go-xml/commit/aea7acd
[d1efae2]: https://github.com/knroy/go-xml/commit/d1efae2
[ddaa14c]: https://github.com/knroy/go-xml/commit/ddaa14c
[30b4475]: https://github.com/knroy/go-xml/commit/30b4475
[80f2586]: https://github.com/knroy/go-xml/commit/80f2586
[d37b98c]: https://github.com/knroy/go-xml/commit/d37b98c
[7a35cf3]: https://github.com/knroy/go-xml/commit/7a35cf3
[2beca7b]: https://github.com/knroy/go-xml/commit/2beca7b
[66766f4]: https://github.com/knroy/go-xml/commit/66766f4
[399418f]: https://github.com/knroy/go-xml/commit/399418f
[3c57043]: https://github.com/knroy/go-xml/commit/3c57043
[df0bc8f]: https://github.com/knroy/go-xml/commit/df0bc8f
[ef2ae38]: https://github.com/knroy/go-xml/commit/ef2ae38
[05eef52]: https://github.com/knroy/go-xml/commit/05eef52
[4e19855]: https://github.com/knroy/go-xml/commit/4e19855
[1126c8b]: https://github.com/knroy/go-xml/commit/1126c8b
[9ca9f64]: https://github.com/knroy/go-xml/commit/9ca9f64
[39e1a8e]: https://github.com/knroy/go-xml/commit/39e1a8e
[2303780]: https://github.com/knroy/go-xml/commit/2303780
[65212a3]: https://github.com/knroy/go-xml/commit/65212a3
[24fb4d6]: https://github.com/knroy/go-xml/commit/24fb4d6
[4de00c8]: https://github.com/knroy/go-xml/commit/4de00c8
[f11ed2b]: https://github.com/knroy/go-xml/commit/f11ed2b
[691f6d1]: https://github.com/knroy/go-xml/commit/691f6d1
[f08285d]: https://github.com/knroy/go-xml/commit/f08285d
[a504b6b]: https://github.com/knroy/go-xml/commit/a504b6b
[449f068]: https://github.com/knroy/go-xml/commit/449f068
[df18696]: https://github.com/knroy/go-xml/commit/df18696
[158bbb7]: https://github.com/knroy/go-xml/commit/158bbb7
[bb81a54]: https://github.com/knroy/go-xml/commit/bb81a54
[5c154ba]: https://github.com/knroy/go-xml/commit/5c154ba
[d0ed436]: https://github.com/knroy/go-xml/commit/d0ed436
[c257efe]: https://github.com/knroy/go-xml/commit/c257efe
[e94d4e0]: https://github.com/knroy/go-xml/commit/e94d4e0
[081c9d3]: https://github.com/knroy/go-xml/commit/081c9d3
[d79951f]: https://github.com/knroy/go-xml/commit/d79951f
[5772ca2]: https://github.com/knroy/go-xml/commit/5772ca2
[4cce5af]: https://github.com/knroy/go-xml/commit/4cce5af
[ba1fbb6]: https://github.com/knroy/go-xml/commit/ba1fbb6
[971e795]: https://github.com/knroy/go-xml/commit/971e795
[c5dbb81]: https://github.com/knroy/go-xml/commit/c5dbb81
[96d1d94]: https://github.com/knroy/go-xml/commit/96d1d94
[d1005db]: https://github.com/knroy/go-xml/commit/d1005db
[fe113df]: https://github.com/knroy/go-xml/commit/fe113df
[d29874a]: https://github.com/knroy/go-xml/commit/d29874a
[d52121f]: https://github.com/knroy/go-xml/commit/d52121f
[962f1dc]: https://github.com/knroy/go-xml/commit/962f1dc
[ddafb12]: https://github.com/knroy/go-xml/commit/ddafb12
[d244a2e]: https://github.com/knroy/go-xml/commit/d244a2e
[0b75658]: https://github.com/knroy/go-xml/commit/0b75658
[1c1cb10]: https://github.com/knroy/go-xml/commit/1c1cb10
[bbdefe5]: https://github.com/knroy/go-xml/commit/bbdefe5
[8b5460b]: https://github.com/knroy/go-xml/commit/8b5460b
[b9c352c]: https://github.com/knroy/go-xml/commit/b9c352c
[c31fc14]: https://github.com/knroy/go-xml/commit/c31fc14
[c36f667]: https://github.com/knroy/go-xml/commit/c36f667
[182fdef]: https://github.com/knroy/go-xml/commit/182fdef
[65d7c6f]: https://github.com/knroy/go-xml/commit/65d7c6f
[895b6a4]: https://github.com/knroy/go-xml/commit/895b6a4
[59b58c1]: https://github.com/knroy/go-xml/commit/59b58c1
[a2b8f9f]: https://github.com/knroy/go-xml/commit/a2b8f9f
[e2ccec2]: https://github.com/knroy/go-xml/commit/e2ccec2
[4ab5460]: https://github.com/knroy/go-xml/commit/4ab5460
[1a0ee45]: https://github.com/knroy/go-xml/commit/1a0ee45
[40d9b34]: https://github.com/knroy/go-xml/commit/40d9b34
[a41d41d]: https://github.com/knroy/go-xml/commit/a41d41d
[f34dfa2]: https://github.com/knroy/go-xml/commit/f34dfa2
[a7befda]: https://github.com/knroy/go-xml/commit/a7befda
[7ff197b]: https://github.com/knroy/go-xml/commit/7ff197b0
[19593ed]: https://github.com/knroy/go-xml/commit/19593ed
[ae36f0b]: https://github.com/knroy/go-xml/commit/ae36f0b
[94181b7]: https://github.com/knroy/go-xml/commit/94181b7
[6ee4b2e]: https://github.com/knroy/go-xml/commit/6ee4b2e
[95944f3]: https://github.com/knroy/go-xml/commit/95944f3
[c665e0d]: https://github.com/knroy/go-xml/commit/c665e0d
[9f41f98]: https://github.com/knroy/go-xml/commit/9f41f98
[e8246dc]: https://github.com/knroy/go-xml/commit/e8246dc
[4ac277f]: https://github.com/knroy/go-xml/commit/4ac277f
[4f9aea4]: https://github.com/knroy/go-xml/commit/4f9aea4
[2f6bca5]: https://github.com/knroy/go-xml/commit/2f6bca5
[afadaf0]: https://github.com/knroy/go-xml/commit/afadaf0
[fcf384f]: https://github.com/knroy/go-xml/commit/fcf384f
[a4162b6]: https://github.com/knroy/go-xml/commit/a4162b6
[b75f94e]: https://github.com/knroy/go-xml/commit/b75f94e
[aa91721]: https://github.com/knroy/go-xml/commit/aa91721
[904f322]: https://github.com/knroy/go-xml/commit/904f322
[595bcf2]: https://github.com/knroy/go-xml/commit/595bcf2
[ac8bb00]: https://github.com/knroy/go-xml/commit/ac8bb00
[e086524]: https://github.com/knroy/go-xml/commit/e086524
[9643910]: https://github.com/knroy/go-xml/commit/9643910
[6cc2247]: https://github.com/knroy/go-xml/commit/6cc2247
[be09e99]: https://github.com/knroy/go-xml/commit/be09e99
[b90d54e]: https://github.com/knroy/go-xml/commit/b90d54e
[df063a5]: https://github.com/knroy/go-xml/commit/df063a5
[5663391]: https://github.com/knroy/go-xml/commit/5663391
[8e3270a]: https://github.com/knroy/go-xml/commit/8e3270a
[fb356ca]: https://github.com/knroy/go-xml/commit/fb356ca
[cca65c4]: https://github.com/knroy/go-xml/commit/cca65c4
[844cd8f]: https://github.com/knroy/go-xml/commit/844cd8f
[6b07dab]: https://github.com/knroy/go-xml/commit/6b07dab
[13f330b]: https://github.com/knroy/go-xml/commit/13f330b
[d2a3158]: https://github.com/knroy/go-xml/commit/d2a3158
[5ed3b7d]: https://github.com/knroy/go-xml/commit/5ed3b7d
[eb66e23]: https://github.com/knroy/go-xml/commit/eb66e23
[9f02ee9]: https://github.com/knroy/go-xml/commit/9f02ee9
[0489759]: https://github.com/knroy/go-xml/commit/0489759
[c68e9b4]: https://github.com/knroy/go-xml/commit/c68e9b4
[8a4f213]: https://github.com/knroy/go-xml/commit/8a4f213
[0dfff09]: https://github.com/knroy/go-xml/commit/0dfff09
[d7f3a71]: https://github.com/knroy/go-xml/commit/d7f3a71
[9354f1d]: https://github.com/knroy/go-xml/commit/9354f1d
[63dd3ad]: https://github.com/knroy/go-xml/commit/63dd3ad
[328460b]: https://github.com/knroy/go-xml/commit/328460b
[c942506]: https://github.com/knroy/go-xml/commit/c942506
[29cc3e9]: https://github.com/knroy/go-xml/commit/29cc3e9
[b9cbc6d]: https://github.com/knroy/go-xml/commit/b9cbc6d
[932bb98]: https://github.com/knroy/go-xml/commit/932bb98
[ead3f81]: https://github.com/knroy/go-xml/commit/ead3f81
[2cfd986]: https://github.com/knroy/go-xml/commit/2cfd986
[9a7551b]: https://github.com/knroy/go-xml/commit/9a7551b
[f05fd39]: https://github.com/knroy/go-xml/commit/f05fd39
[ff1f3ac]: https://github.com/knroy/go-xml/commit/ff1f3ac
[302ddf2]: https://github.com/knroy/go-xml/commit/302ddf2
[64ae463]: https://github.com/knroy/go-xml/commit/64ae463
[8a68af2]: https://github.com/knroy/go-xml/commit/8a68af2
[a6f13b1]: https://github.com/knroy/go-xml/commit/a6f13b1
[87cea8b]: https://github.com/knroy/go-xml/commit/87cea8b
[827e309]: https://github.com/knroy/go-xml/commit/827e309
[1ac69c5]: https://github.com/knroy/go-xml/commit/1ac69c5
[54dbc38]: https://github.com/knroy/go-xml/commit/54dbc38
[cb7bc56]: https://github.com/knroy/go-xml/commit/cb7bc56
[3507414]: https://github.com/knroy/go-xml/commit/3507414
[b3cf350]: https://github.com/knroy/go-xml/commit/b3cf350
[816d8ec]: https://github.com/knroy/go-xml/commit/816d8ec
[fd303aa]: https://github.com/knroy/go-xml/commit/fd303aa
[0bd7034]: https://github.com/knroy/go-xml/commit/0bd7034
[de0784c]: https://github.com/knroy/go-xml/commit/de0784c
[960370b]: https://github.com/knroy/go-xml/commit/960370b
[5bbaba2]: https://github.com/knroy/go-xml/commit/5bbaba2
[b79ca81]: https://github.com/knroy/go-xml/commit/b79ca81
[e73ae6b]: https://github.com/knroy/go-xml/commit/e73ae6b
[1d406ab]: https://github.com/knroy/go-xml/commit/1d406ab
[65ebe0e]: https://github.com/knroy/go-xml/commit/65ebe0e
[a52bc6b]: https://github.com/knroy/go-xml/commit/a52bc6b
[2536f98]: https://github.com/knroy/go-xml/commit/2536f98
[d2c48ef]: https://github.com/knroy/go-xml/commit/d2c48ef
[d5178af]: https://github.com/knroy/go-xml/commit/d5178af
[a0e226c]: https://github.com/knroy/go-xml/commit/a0e226c
[91af2f9]: https://github.com/knroy/go-xml/commit/91af2f9
[e4ecbd7]: https://github.com/knroy/go-xml/commit/e4ecbd7
[0a73669]: https://github.com/knroy/go-xml/commit/0a73669
[b50e009]: https://github.com/knroy/go-xml/commit/b50e009
[a386ca5]: https://github.com/knroy/go-xml/commit/a386ca5
[44fc1c0]: https://github.com/knroy/go-xml/commit/44fc1c0
[1f91b5d]: https://github.com/knroy/go-xml/commit/1f91b5d
[c92d154]: https://github.com/knroy/go-xml/commit/c92d154
[d26ad2e]: https://github.com/knroy/go-xml/commit/d26ad2e
[0f78020]: https://github.com/knroy/go-xml/commit/0f78020
[bf94023]: https://github.com/knroy/go-xml/commit/bf94023
[6feaadd]: https://github.com/knroy/go-xml/commit/6feaadd
[35d5ad7]: https://github.com/knroy/go-xml/commit/35d5ad7
[40bb829]: https://github.com/knroy/go-xml/commit/40bb829
[1b4d62e]: https://github.com/knroy/go-xml/commit/1b4d62e
[065b5b5]: https://github.com/knroy/go-xml/commit/065b5b5
[ef9e55b]: https://github.com/knroy/go-xml/commit/ef9e55b
[b7e79f7]: https://github.com/knroy/go-xml/commit/b7e79f7
[4f2cc85]: https://github.com/knroy/go-xml/commit/4f2cc85
[adc813c]: https://github.com/knroy/go-xml/commit/adc813c
[157f717]: https://github.com/knroy/go-xml/commit/157f717
[167a347]: https://github.com/knroy/go-xml/commit/167a347
[60cf53e]: https://github.com/knroy/go-xml/commit/60cf53e
[ddcaebf]: https://github.com/knroy/go-xml/commit/ddcaebf
[381a787]: https://github.com/knroy/go-xml/commit/381a787
[954a278]: https://github.com/knroy/go-xml/commit/954a278
[0175e4b]: https://github.com/knroy/go-xml/commit/0175e4b
[8d10f9e]: https://github.com/knroy/go-xml/commit/8d10f9e
[d4feb67]: https://github.com/knroy/go-xml/commit/d4feb67
[38629c8]: https://github.com/knroy/go-xml/commit/38629c8
[de7aab2]: https://github.com/knroy/go-xml/commit/de7aab2
[1f6576d]: https://github.com/knroy/go-xml/commit/1f6576d
[6e960b7]: https://github.com/knroy/go-xml/commit/6e960b7
[973790f]: https://github.com/knroy/go-xml/commit/973790f
[03be6e6]: https://github.com/knroy/go-xml/commit/03be6e6
[43ddd9d]: https://github.com/knroy/go-xml/commit/43ddd9d
[785a0c6]: https://github.com/knroy/go-xml/commit/785a0c6
[4fdf376]: https://github.com/knroy/go-xml/commit/4fdf376
[b54aee5]: https://github.com/knroy/go-xml/commit/b54aee5
[7e2bec4]: https://github.com/knroy/go-xml/commit/7e2bec4
[42902f3]: https://github.com/knroy/go-xml/commit/42902f3
[2a1d6ad]: https://github.com/knroy/go-xml/commit/2a1d6ad
[2089255]: https://github.com/knroy/go-xml/commit/2089255
[34e6b36]: https://github.com/knroy/go-xml/commit/34e6b36
[83e45c5]: https://github.com/knroy/go-xml/commit/83e45c5
[44d2374]: https://github.com/knroy/go-xml/commit/44d2374
[2447ca0]: https://github.com/knroy/go-xml/commit/2447ca0
[6abbb27]: https://github.com/knroy/go-xml/commit/6abbb27
[26caed6]: https://github.com/knroy/go-xml/commit/26caed6
[ba6c23f]: https://github.com/knroy/go-xml/commit/ba6c23f
[ff0c463]: https://github.com/knroy/go-xml/commit/ff0c463
[44db07f]: https://github.com/knroy/go-xml/commit/44db07f
[096a587]: https://github.com/knroy/go-xml/commit/096a587
[294b965]: https://github.com/knroy/go-xml/commit/294b965
[2e860a2]: https://github.com/knroy/go-xml/commit/2e860a2
[3916423]: https://github.com/knroy/go-xml/commit/3916423
[59e5ee0]: https://github.com/knroy/go-xml/commit/59e5ee0
[a8b0eeb]: https://github.com/knroy/go-xml/commit/a8b0eeb
[724617a]: https://github.com/knroy/go-xml/commit/724617a
[d30a7e8]: https://github.com/knroy/go-xml/commit/d30a7e8
[0ae8a17]: https://github.com/knroy/go-xml/commit/0ae8a17
[678c4ef]: https://github.com/knroy/go-xml/commit/678c4ef
[4bdcad8]: https://github.com/knroy/go-xml/commit/4bdcad8
[0a00b54]: https://github.com/knroy/go-xml/commit/0a00b54
[36ecaac]: https://github.com/knroy/go-xml/commit/36ecaac
[a788a32]: https://github.com/knroy/go-xml/commit/a788a32
[79b94b5]: https://github.com/knroy/go-xml/commit/79b94b5
[5804464]: https://github.com/knroy/go-xml/commit/5804464
[8fe2846]: https://github.com/knroy/go-xml/commit/8fe2846
[cccfc14]: https://github.com/knroy/go-xml/commit/cccfc14
[3208035]: https://github.com/knroy/go-xml/commit/3208035
[6fc06e9]: https://github.com/knroy/go-xml/commit/6fc06e9
[608935d]: https://github.com/knroy/go-xml/commit/608935d
[db6f542]: https://github.com/knroy/go-xml/commit/db6f542
[5ab15c4]: https://github.com/knroy/go-xml/commit/5ab15c4
[0b676e7]: https://github.com/knroy/go-xml/commit/0b676e7
[36f470e]: https://github.com/knroy/go-xml/commit/36f470e
[35422d9]: https://github.com/knroy/go-xml/commit/35422d9
[c2e2af2]: https://github.com/knroy/go-xml/commit/c2e2af2
[9315ac0]: https://github.com/knroy/go-xml/commit/9315ac0
[4f1dad9]: https://github.com/knroy/go-xml/commit/4f1dad9
[5c09c9d]: https://github.com/knroy/go-xml/commit/5c09c9d
[537885e]: https://github.com/knroy/go-xml/commit/537885e
[23da903]: https://github.com/knroy/go-xml/commit/23da903
[76bc90b]: https://github.com/knroy/go-xml/commit/76bc90b
[90b29d9]: https://github.com/knroy/go-xml/commit/90b29d9
[2de588e]: https://github.com/knroy/go-xml/commit/2de588e
[305b99d]: https://github.com/knroy/go-xml/commit/305b99d
[bf58cad]: https://github.com/knroy/go-xml/commit/bf58cad
[acf9a9c]: https://github.com/knroy/go-xml/commit/acf9a9c
[9f896b0]: https://github.com/knroy/go-xml/commit/9f896b0
[9e7f579]: https://github.com/knroy/go-xml/commit/9e7f579
[acc785d]: https://github.com/knroy/go-xml/commit/acc785d
[b0894e2]: https://github.com/knroy/go-xml/commit/b0894e2
[96b8df3]: https://github.com/knroy/go-xml/commit/96b8df3
[0e0d2b8]: https://github.com/knroy/go-xml/commit/0e0d2b8
[af1a9e5]: https://github.com/knroy/go-xml/commit/af1a9e5
[c4323f5]: https://github.com/knroy/go-xml/commit/c4323f5
[f638162]: https://github.com/knroy/go-xml/commit/f638162
[256759e]: https://github.com/knroy/go-xml/commit/256759e
[8a9d83d]: https://github.com/knroy/go-xml/commit/8a9d83d
[a98e534]: https://github.com/knroy/go-xml/commit/a98e534
[66777b6]: https://github.com/knroy/go-xml/commit/66777b6
[5a0eab3]: https://github.com/knroy/go-xml/commit/5a0eab3
[4bea4f9]: https://github.com/knroy/go-xml/commit/4bea4f9
[eb144dd]: https://github.com/knroy/go-xml/commit/eb144dd
[8be963f]: https://github.com/knroy/go-xml/commit/8be963f
[7bec012]: https://github.com/knroy/go-xml/commit/7bec012
[f6e3637]: https://github.com/knroy/go-xml/commit/f6e3637
[4e56b21]: https://github.com/knroy/go-xml/commit/4e56b21
[75c5d3c]: https://github.com/knroy/go-xml/commit/75c5d3c
[cac3851]: https://github.com/knroy/go-xml/commit/cac3851
[16a64ec]: https://github.com/knroy/go-xml/commit/16a64ec
[cc11df5]: https://github.com/knroy/go-xml/commit/cc11df5
[7730dcb]: https://github.com/knroy/go-xml/commit/7730dcb
[95171aa]: https://github.com/knroy/go-xml/commit/95171aa
[ce41a32]: https://github.com/knroy/go-xml/commit/ce41a32
[d02cb54]: https://github.com/knroy/go-xml/commit/d02cb54
[42ce08f]: https://github.com/knroy/go-xml/commit/42ce08f
[5531827]: https://github.com/knroy/go-xml/commit/5531827
[a7d0796]: https://github.com/knroy/go-xml/commit/a7d0796
[20a5eef]: https://github.com/knroy/go-xml/commit/20a5eef
[4f870c3]: https://github.com/knroy/go-xml/commit/4f870c3
[609bc45]: https://github.com/knroy/go-xml/commit/609bc45
[3fbd05e]: https://github.com/knroy/go-xml/commit/3fbd05e
[330e96a]: https://github.com/knroy/go-xml/commit/330e96a
[19f2492]: https://github.com/knroy/go-xml/commit/19f2492
[c6b67df]: https://github.com/knroy/go-xml/commit/c6b67df
[bedbb23]: https://github.com/knroy/go-xml/commit/bedbb23
[84c0f1f]: https://github.com/knroy/go-xml/commit/84c0f1f
[d32fa1d]: https://github.com/knroy/go-xml/commit/d32fa1d
[cc32228]: https://github.com/knroy/go-xml/commit/cc32228
[eb315d3]: https://github.com/knroy/go-xml/commit/eb315d3
[5cd3316]: https://github.com/knroy/go-xml/commit/5cd3316
[6930e60]: https://github.com/knroy/go-xml/commit/6930e60
[c5c4fa9]: https://github.com/knroy/go-xml/commit/c5c4fa9
[11c4ba5]: https://github.com/knroy/go-xml/commit/11c4ba5
[69101da]: https://github.com/knroy/go-xml/commit/69101da
[13ecb6e]: https://github.com/knroy/go-xml/commit/13ecb6e
[615676a]: https://github.com/knroy/go-xml/commit/615676a
[95338d5]: https://github.com/knroy/go-xml/commit/95338d5
[9e9a6f9]: https://github.com/knroy/go-xml/commit/9e9a6f9
[089c346]: https://github.com/knroy/go-xml/commit/089c346
[43ca7b2]: https://github.com/knroy/go-xml/commit/43ca7b2
[ad032d1]: https://github.com/knroy/go-xml/commit/ad032d1
[91f30c9]: https://github.com/knroy/go-xml/commit/91f30c9
[f46ec7b]: https://github.com/knroy/go-xml/commit/f46ec7b
[eb4d0ae]: https://github.com/knroy/go-xml/commit/eb4d0ae
[734f53c]: https://github.com/knroy/go-xml/commit/734f53c
[e720aa3]: https://github.com/knroy/go-xml/commit/e720aa3
[eb5b289]: https://github.com/knroy/go-xml/commit/eb5b289
[78d92a8]: https://github.com/knroy/go-xml/commit/78d92a8
[a97b803]: https://github.com/knroy/go-xml/commit/a97b803
[ba64164]: https://github.com/knroy/go-xml/commit/ba64164
[ab7d256]: https://github.com/knroy/go-xml/commit/ab7d256
[3926b3e]: https://github.com/knroy/go-xml/commit/3926b3e
[d06d0f3]: https://github.com/knroy/go-xml/commit/d06d0f3
[a97ca6a]: https://github.com/knroy/go-xml/commit/a97ca6a
[dce8830]: https://github.com/knroy/go-xml/commit/dce8830
[f09487b]: https://github.com/knroy/go-xml/commit/f09487b
[877b3af]: https://github.com/knroy/go-xml/commit/877b3af
[e7f1116]: https://github.com/knroy/go-xml/commit/e7f1116
[e621bf7]: https://github.com/knroy/go-xml/commit/e621bf7
[f90e97d]: https://github.com/knroy/go-xml/commit/f90e97d
[50492e0]: https://github.com/knroy/go-xml/commit/50492e0
[9aed5a3]: https://github.com/knroy/go-xml/commit/9aed5a3
[7fdea1e]: https://github.com/knroy/go-xml/commit/7fdea1e
[9a34abe]: https://github.com/knroy/go-xml/commit/9a34abe
[2a27fc6]: https://github.com/knroy/go-xml/commit/2a27fc6
[df96ba9]: https://github.com/knroy/go-xml/commit/df96ba9
[f065ab2]: https://github.com/knroy/go-xml/commit/f065ab2
[9e39b51]: https://github.com/knroy/go-xml/commit/9e39b51
[6e51253]: https://github.com/knroy/go-xml/commit/6e51253
[aeae707]: https://github.com/knroy/go-xml/commit/aeae707
[e73a30b]: https://github.com/knroy/go-xml/commit/e73a30b
[a85f40a]: https://github.com/knroy/go-xml/commit/a85f40a
[0c70a04]: https://github.com/knroy/go-xml/commit/0c70a04
[915eb63]: https://github.com/knroy/go-xml/commit/915eb63
[cfc090e]: https://github.com/knroy/go-xml/commit/cfc090e
[77c3c5d]: https://github.com/knroy/go-xml/commit/77c3c5d
[60fed8a]: https://github.com/knroy/go-xml/commit/60fed8a
[4c5c3ee]: https://github.com/knroy/go-xml/commit/4c5c3ee
[2a41550]: https://github.com/knroy/go-xml/commit/2a41550
[4ad352d]: https://github.com/knroy/go-xml/commit/4ad352d
[9399efd]: https://github.com/knroy/go-xml/commit/9399efd
[cfa135c]: https://github.com/knroy/go-xml/commit/cfa135c
[4694fa3]: https://github.com/knroy/go-xml/commit/4694fa3
[0ecee9c]: https://github.com/knroy/go-xml/commit/0ecee9c
[f84502b]: https://github.com/knroy/go-xml/commit/f84502b
[49721db]: https://github.com/knroy/go-xml/commit/49721db
[ddc6ee2]: https://github.com/knroy/go-xml/commit/ddc6ee2
[9aefe48]: https://github.com/knroy/go-xml/commit/9aefe48
[591831a]: https://github.com/knroy/go-xml/commit/591831a
[4950817]: https://github.com/knroy/go-xml/commit/4950817
[e975737]: https://github.com/knroy/go-xml/commit/e975737
[6297a00]: https://github.com/knroy/go-xml/commit/6297a00
[7e92b19]: https://github.com/knroy/go-xml/commit/7e92b19
[e7042aa]: https://github.com/knroy/go-xml/commit/e7042aa
[87743b2]: https://github.com/knroy/go-xml/commit/87743b2
[97fccf5]: https://github.com/knroy/go-xml/commit/97fccf5
[e8f4dfa]: https://github.com/knroy/go-xml/commit/e8f4dfa
[b6a5d50]: https://github.com/knroy/go-xml/commit/b6a5d50
[eb9ddf8]: https://github.com/knroy/go-xml/commit/eb9ddf8
[7b6ae04]: https://github.com/knroy/go-xml/commit/7b6ae04
[25eea7b]: https://github.com/knroy/go-xml/commit/25eea7b
[1efb8e1]: https://github.com/knroy/go-xml/commit/1efb8e1
[177fbff]: https://github.com/knroy/go-xml/commit/177fbff
[390b351]: https://github.com/knroy/go-xml/commit/390b351
[c870472]: https://github.com/knroy/go-xml/commit/c870472
[f077a70]: https://github.com/knroy/go-xml/commit/f077a70
[e0d2c13]: https://github.com/knroy/go-xml/commit/e0d2c13
[0caab03]: https://github.com/knroy/go-xml/commit/0caab03
[c6508c3]: https://github.com/knroy/go-xml/commit/c6508c3
[0d9c103]: https://github.com/knroy/go-xml/commit/0d9c103
[b9171e8]: https://github.com/knroy/go-xml/commit/b9171e8
[ca1607b]: https://github.com/knroy/go-xml/commit/ca1607b
[c5d5bce]: https://github.com/knroy/go-xml/commit/c5d5bce
[1505000]: https://github.com/knroy/go-xml/commit/1505000
[5ad849d]: https://github.com/knroy/go-xml/commit/5ad849d
[b12ff0b]: https://github.com/knroy/go-xml/commit/b12ff0b
[bee5b12]: https://github.com/knroy/go-xml/commit/bee5b12
[9d7cf65]: https://github.com/knroy/go-xml/commit/9d7cf65
[605d0be]: https://github.com/knroy/go-xml/commit/605d0be
[57a0c9d]: https://github.com/knroy/go-xml/commit/57a0c9d
[2f78c52]: https://github.com/knroy/go-xml/commit/2f78c52
[a5013a4]: https://github.com/knroy/go-xml/commit/a5013a4
[b0428d0]: https://github.com/knroy/go-xml/commit/b0428d0
[f41bb8c]: https://github.com/knroy/go-xml/commit/f41bb8c
[090a9a8]: https://github.com/knroy/go-xml/commit/090a9a8
[2ef0012]: https://github.com/knroy/go-xml/commit/2ef0012
[2a1e3b9]: https://github.com/knroy/go-xml/commit/2a1e3b9
[ff9032d]: https://github.com/knroy/go-xml/commit/ff9032d
[1889367]: https://github.com/knroy/go-xml/commit/1889367
[fa87dcc]: https://github.com/knroy/go-xml/commit/fa87dcc
[c13aa92]: https://github.com/knroy/go-xml/commit/c13aa92
[c114ef5]: https://github.com/knroy/go-xml/commit/c114ef5
[cc13393]: https://github.com/knroy/go-xml/commit/cc13393
[8650527]: https://github.com/knroy/go-xml/commit/8650527
[252b65b]: https://github.com/knroy/go-xml/commit/252b65b
[72599d9]: https://github.com/knroy/go-xml/commit/72599d9
[ee5018c]: https://github.com/knroy/go-xml/commit/ee5018c
[93b8855]: https://github.com/knroy/go-xml/commit/93b8855
[f9a904d]: https://github.com/knroy/go-xml/commit/f9a904d
[7362b27]: https://github.com/knroy/go-xml/commit/7362b27
[e960da2]: https://github.com/knroy/go-xml/commit/e960da2
[c516340]: https://github.com/knroy/go-xml/commit/c516340
[b6aef3e]: https://github.com/knroy/go-xml/commit/b6aef3e
[94acc9f]: https://github.com/knroy/go-xml/commit/94acc9f
[50bba71]: https://github.com/knroy/go-xml/commit/50bba71
[0c7f0eb]: https://github.com/knroy/go-xml/commit/0c7f0eb
[0d82e86]: https://github.com/knroy/go-xml/commit/0d82e86
[11da206]: https://github.com/knroy/go-xml/commit/11da206
[282aec6]: https://github.com/knroy/go-xml/commit/282aec6
[2f9d010]: https://github.com/knroy/go-xml/commit/2f9d010
[3251f40]: https://github.com/knroy/go-xml/commit/3251f40
[406c6e1]: https://github.com/knroy/go-xml/commit/406c6e1
[429b9c6]: https://github.com/knroy/go-xml/commit/429b9c6
[42ac077]: https://github.com/knroy/go-xml/commit/42ac077
[4fafb61]: https://github.com/knroy/go-xml/commit/4fafb61
[5423f40]: https://github.com/knroy/go-xml/commit/5423f40
[5734988]: https://github.com/knroy/go-xml/commit/5734988
[639bcb4]: https://github.com/knroy/go-xml/commit/639bcb4
[69648fe]: https://github.com/knroy/go-xml/commit/69648fe
[6ad619f]: https://github.com/knroy/go-xml/commit/6ad619f
[6bb84b2]: https://github.com/knroy/go-xml/commit/6bb84b2
[75039ea]: https://github.com/knroy/go-xml/commit/75039ea
[7b3b8cf]: https://github.com/knroy/go-xml/commit/7b3b8cf
[8211a76]: https://github.com/knroy/go-xml/commit/8211a76
[8aa67d3]: https://github.com/knroy/go-xml/commit/8aa67d3
[93f91ae]: https://github.com/knroy/go-xml/commit/93f91ae
[94aa606]: https://github.com/knroy/go-xml/commit/94aa606
[952e6d2]: https://github.com/knroy/go-xml/commit/952e6d2
[967b27a]: https://github.com/knroy/go-xml/commit/967b27a
[9999789]: https://github.com/knroy/go-xml/commit/9999789
[9c19935]: https://github.com/knroy/go-xml/commit/9c19935
[a543265]: https://github.com/knroy/go-xml/commit/a543265
[a97ac65]: https://github.com/knroy/go-xml/commit/a97ac65
[aff6564]: https://github.com/knroy/go-xml/commit/aff6564
[b2fcde5]: https://github.com/knroy/go-xml/commit/b2fcde5
[b9aebdf]: https://github.com/knroy/go-xml/commit/b9aebdf
[c1836df]: https://github.com/knroy/go-xml/commit/c1836df
[c767803]: https://github.com/knroy/go-xml/commit/c767803
[d0591ae]: https://github.com/knroy/go-xml/commit/d0591ae
[d258054]: https://github.com/knroy/go-xml/commit/d258054
[d5e2b0f]: https://github.com/knroy/go-xml/commit/d5e2b0f
[e21d0c1]: https://github.com/knroy/go-xml/commit/e21d0c1
[e965b1f]: https://github.com/knroy/go-xml/commit/e965b1f
[e9e1442]: https://github.com/knroy/go-xml/commit/e9e1442
[ed4a60f]: https://github.com/knroy/go-xml/commit/ed4a60f
[ed500f2]: https://github.com/knroy/go-xml/commit/ed500f2
[ee8aaa2]: https://github.com/knroy/go-xml/commit/ee8aaa2
[f6079cf]: https://github.com/knroy/go-xml/commit/f6079cf
[f856400]: https://github.com/knroy/go-xml/commit/f856400
[fce2d5e]: https://github.com/knroy/go-xml/commit/fce2d5e
