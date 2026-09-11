# Changelog

Notable changes, newest first. Versions follow [semantic
versioning](https://semver.org): from 1.0.0 the exported API is stable, and a
breaking change means 2.0 with a new module path. See *Stability* below.

## Unreleased

### Added

| Change | What it does |
|---|---|
| **The foundation of the XSLT 3.0 §19.8 streamability analysis** | §19 infers a posture and sweep for every construct and refuses a free-ranging one with `XTSE3430`. The lattice and the §19.8.8 XPath rules are implemented; an unmodelled construct is "no opinion", not a rejection. |

### Fixed — engine

| Change | Problem → solution | Commit |
|---|---|---|
| Three rooted resolvers checked containment and then opened, so only `xslt` enforced its root at open time | `xsd`, `dtd` and the CLI's RELAX NG resolver open through `os.OpenRoot`; the string check stays as diagnosis, not enforcement. | [`37972d9`][37972d9] |
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
| **The XSLT instruction rules of §19.8.4, on the streamability lattice** | §19.8.4's operand roles for the 43 XSLT instructions, §19.8.6 attribute sets and §19.8.7 value templates were unimplemented. All now in `xslt/streaminstructions.go`; four instructions stay unmodelled. | [`2cf1ad6`][2cf1ad6] |
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
| The documented `MaxItems` budget never bound on an XQuery body | `Compiled.Eval` reset the counter once per tuple; `HoldItemBudget` holds it for one query. | [`fe41f3c`][fe41f3c] |
| Uncompilable content models skipped every constraint on them | A model that would not compile passed silently rather than declining. | [`b6fb5ab`][b6fb5ab] |
| A budget answered "valid" | Exhausting the budget was reported as success instead of as an inability to decide. | [`2c461c7`][2c461c7] |
| Budgets counted the wrong thing | A bound over the wrong quantity is not a bound. | [`8dcc4dc`][8dcc4dc] |
| Last unbudgeted load-time algorithm | Now bounded; `Options.MaxContentModelPositions` makes the position budget host-tunable. | [`81e6ee5`][81e6ee5] |
| Substitution closure unbounded | Bounded, along with the pairwise overlap test it fed. | [`1b027e5`][1b027e5] |
| A flat operator chain overflowed the stack at compile time | The depth cap counts nesting, and the attack is length. Every infix loop charges `maxChainLength`. | [`106bcdc`][106bcdc] |

### Fixed — test harness

A harness defect and an engine defect are not the same finding: these changed
what the suites *measured*, not what the library does.

| Change | Problem → solution | Commit |
|---|---|---|
| The gate recorded no provenance, so no figure could be tied to the tree that produced it | `check.sh` heads its transcript with Go version, commit, architecture and per-suite revision, and writes `tests/last-run.txt`. | [`03b5942`][03b5942] |
| CI ran on Linux only, so nothing proved the file handling rule 3 asks for | The fast job runs `ubuntu`/`windows`/`macos`; `conformance` stays Linux, where the corpora are. | [`03b5942`][03b5942] |
| Nine fuzz targets compiled and replayed seeds but never searched | A nightly matrix gives each 300s, off the per-push gate because the search is nondeterministic. | [`f2aeee8`][f2aeee8] |
| Four stale feature labels | `streaming`, `streaming-fallback`, `XPath_3.1` and `XML_1.1` sat in `unsupportedFeatures` long after implementation, hiding 2,862 cases. Each must *move* to `supportedFeatures`, not just be deleted. | [`be2938e`][be2938e] |
| Two more stale labels | `namespace-axis` and `infoset-dtd`; the two harnesses had been contradicting each other. | [`a820213`][a820213] |
| Dependencies merged additively | A set's `satisfied="true"` outlived a case's `"false"`, excluding all fourteen `fn-load-xquery-module-901..914` by the declaration they exist to contradict. Now merged per (type, value). | [`d15b6df`][d15b6df] |
| `SerializeAsXML` forced 1.0 | Tree assertions compared against the truncated prefix of a discarded error. The version, unlike method and indentation, decides whether a character can be written at all. | [`a45c3a6`][a45c3a6] |
| Ratchet read a shrinking corpus as a regression | A count taken over fewer roots is not comparable; it is now skipped, not passed, when a root is absent. | [`3b6e685`][3b6e685] |
| Cases never scored went uncounted | A case that is never scored must still appear in the denominator. | [`c3a52be`][c3a52be] |

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
| `validation-0201` was failing for a reason no document named | Five places across three documents said the case's last remaining difference was the indent width — Saxon's 3 spaces against this serializer's 2 — and §20's implementation-defined latitude. Measurement says otherwise: setting the width to 3 gains **nothing** (6,193 passed / 8 failed either way; the reported offset merely moves from 46 to 134). Strip all whitespace and the two sides are byte-identical, so the difference really is whitespace placement, but it is a *second*, independent one — inside `<head>` we write a newline before `<style>` and Saxon does not, because Saxon declines to indent before an element whose own content is significant text. Ours is the parent's call (`hasTextChild(<head>)` is false, so `indentChildren` stays on), and it is permitted: Serialization 3.1 §5 constrains where a serialiser *may not* indent, not where it must. The verdict — implementation-defined, not ours — is unchanged; the reason for it is now the true one, and pinned by a test. The case is the fourth time this one has failed for a reason underneath the reason last recorded, and this time the paragraph warning about exactly that had itself fallen into it | — |
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


[0048fde]: https://github.com/knroy/go-xml/commit/0048fde
[01b91ba]: https://github.com/knroy/go-xml/commit/01b91ba
[0634425]: https://github.com/knroy/go-xml/commit/0634425
[106bcdc]: https://github.com/knroy/go-xml/commit/106bcdc
[120e7ec]: https://github.com/knroy/go-xml/commit/120e7ec
[145d0d1]: https://github.com/knroy/go-xml/commit/145d0d1
[176ce57]: https://github.com/knroy/go-xml/commit/176ce57
[17ce36c]: https://github.com/knroy/go-xml/commit/17ce36c
[18a6d96]: https://github.com/knroy/go-xml/commit/18a6d96
[1b027e5]: https://github.com/knroy/go-xml/commit/1b027e5
[1c43c4e]: https://github.com/knroy/go-xml/commit/1c43c4e
[1d10349]: https://github.com/knroy/go-xml/commit/1d10349
[22d2d64]: https://github.com/knroy/go-xml/commit/22d2d64
[24c4cca]: https://github.com/knroy/go-xml/commit/24c4cca
[277599e]: https://github.com/knroy/go-xml/commit/277599e
[28699a9]: https://github.com/knroy/go-xml/commit/28699a9
[28e455a]: https://github.com/knroy/go-xml/commit/28e455a
[2c461c7]: https://github.com/knroy/go-xml/commit/2c461c7
[2cc633e]: https://github.com/knroy/go-xml/commit/2cc633e
[2cf1ad6]: https://github.com/knroy/go-xml/commit/2cf1ad6
[2eb28b6]: https://github.com/knroy/go-xml/commit/2eb28b6
[2ef8dba]: https://github.com/knroy/go-xml/commit/2ef8dba
[30dc68d]: https://github.com/knroy/go-xml/commit/30dc68d
[35c2e77]: https://github.com/knroy/go-xml/commit/35c2e77
[3672fa3]: https://github.com/knroy/go-xml/commit/3672fa3
[39f7174]: https://github.com/knroy/go-xml/commit/39f7174
[3b4b1e8]: https://github.com/knroy/go-xml/commit/3b4b1e8
[3b6e685]: https://github.com/knroy/go-xml/commit/3b6e685
[3f3cce3]: https://github.com/knroy/go-xml/commit/3f3cce3
[40930d5]: https://github.com/knroy/go-xml/commit/40930d5
[5964c0a]: https://github.com/knroy/go-xml/commit/5964c0a
[59ee9b9]: https://github.com/knroy/go-xml/commit/59ee9b9
[5cd6b38]: https://github.com/knroy/go-xml/commit/5cd6b38
[5f0df59]: https://github.com/knroy/go-xml/commit/5f0df59
[600e7c0]: https://github.com/knroy/go-xml/commit/600e7c0
[6567f8e]: https://github.com/knroy/go-xml/commit/6567f8e
[6654bac]: https://github.com/knroy/go-xml/commit/6654bac
[694fe29]: https://github.com/knroy/go-xml/commit/694fe29
[6c8405c]: https://github.com/knroy/go-xml/commit/6c8405c
[6e03e3d]: https://github.com/knroy/go-xml/commit/6e03e3d
[704222f]: https://github.com/knroy/go-xml/commit/704222f
[73d547b]: https://github.com/knroy/go-xml/commit/73d547b
[78f70d5]: https://github.com/knroy/go-xml/commit/78f70d5
[7ad2845]: https://github.com/knroy/go-xml/commit/7ad2845
[7b0562a]: https://github.com/knroy/go-xml/commit/7b0562a
[7c4bef2]: https://github.com/knroy/go-xml/commit/7c4bef2
[7f2d2d0]: https://github.com/knroy/go-xml/commit/7f2d2d0
[81e6ee5]: https://github.com/knroy/go-xml/commit/81e6ee5
[830ae11]: https://github.com/knroy/go-xml/commit/830ae11
[83148b7]: https://github.com/knroy/go-xml/commit/83148b7
[84735c8]: https://github.com/knroy/go-xml/commit/84735c8
[878f9ed]: https://github.com/knroy/go-xml/commit/878f9ed
[885f6b7]: https://github.com/knroy/go-xml/commit/885f6b7
[8dcc4dc]: https://github.com/knroy/go-xml/commit/8dcc4dc
[9113ac4]: https://github.com/knroy/go-xml/commit/9113ac4
[920fd8a]: https://github.com/knroy/go-xml/commit/920fd8a
[96171c5]: https://github.com/knroy/go-xml/commit/96171c5
[9a41bea]: https://github.com/knroy/go-xml/commit/9a41bea
[a3ec25e]: https://github.com/knroy/go-xml/commit/a3ec25e
[a45c3a6]: https://github.com/knroy/go-xml/commit/a45c3a6
[a820213]: https://github.com/knroy/go-xml/commit/a820213
[a883c0a]: https://github.com/knroy/go-xml/commit/a883c0a
[ab89b76]: https://github.com/knroy/go-xml/commit/ab89b76
[ac743d4]: https://github.com/knroy/go-xml/commit/ac743d4
[ad2c3dc]: https://github.com/knroy/go-xml/commit/ad2c3dc
[aeead08]: https://github.com/knroy/go-xml/commit/aeead08
[b21f5eb]: https://github.com/knroy/go-xml/commit/b21f5eb
[b361b10]: https://github.com/knroy/go-xml/commit/b361b10
[b4c4bb2]: https://github.com/knroy/go-xml/commit/b4c4bb2
[b50b373]: https://github.com/knroy/go-xml/commit/b50b373
[8e0f44d]: https://github.com/knroy/go-xml/commit/8e0f44d
[17b1c91]: https://github.com/knroy/go-xml/commit/17b1c91
[e511421]: https://github.com/knroy/go-xml/commit/e511421
[5c17280]: https://github.com/knroy/go-xml/commit/5c17280
[37972d9]: https://github.com/knroy/go-xml/commit/37972d9
[d63cbb2]: https://github.com/knroy/go-xml/commit/d63cbb2
[f161723]: https://github.com/knroy/go-xml/commit/f161723
[10486a6]: https://github.com/knroy/go-xml/commit/10486a6
[f2aeee8]: https://github.com/knroy/go-xml/commit/f2aeee8
[03b5942]: https://github.com/knroy/go-xml/commit/03b5942
[b6fb5ab]: https://github.com/knroy/go-xml/commit/b6fb5ab
[bb803d5]: https://github.com/knroy/go-xml/commit/bb803d5
[bc72bed]: https://github.com/knroy/go-xml/commit/bc72bed
[bd0aaf5]: https://github.com/knroy/go-xml/commit/bd0aaf5
[be2938e]: https://github.com/knroy/go-xml/commit/be2938e
[c3a52be]: https://github.com/knroy/go-xml/commit/c3a52be
[c8fc839]: https://github.com/knroy/go-xml/commit/c8fc839
[cc17983]: https://github.com/knroy/go-xml/commit/cc17983
[d0dd99d]: https://github.com/knroy/go-xml/commit/d0dd99d
[d145807]: https://github.com/knroy/go-xml/commit/d145807
[d15b6df]: https://github.com/knroy/go-xml/commit/d15b6df
[da1cde6]: https://github.com/knroy/go-xml/commit/da1cde6
[e049991]: https://github.com/knroy/go-xml/commit/e049991
[e125888]: https://github.com/knroy/go-xml/commit/e125888
[e2606cc]: https://github.com/knroy/go-xml/commit/e2606cc
[e51ed3f]: https://github.com/knroy/go-xml/commit/e51ed3f
[e967628]: https://github.com/knroy/go-xml/commit/e967628
[ea4681f]: https://github.com/knroy/go-xml/commit/ea4681f
[eb5ea72]: https://github.com/knroy/go-xml/commit/eb5ea72
[f0ffb5b]: https://github.com/knroy/go-xml/commit/f0ffb5b
[f536984]: https://github.com/knroy/go-xml/commit/f536984
[f88747b]: https://github.com/knroy/go-xml/commit/f88747b
[f9c0cf5]: https://github.com/knroy/go-xml/commit/f9c0cf5
[fe41f3c]: https://github.com/knroy/go-xml/commit/fe41f3c
