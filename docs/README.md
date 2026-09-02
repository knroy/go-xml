# Documentation

* **[Using go-xml in a server](server.md)** — compile once, transform or
  validate per request; a hardened XSD validation endpoint, timeouts, limits,
  and the mistakes that matter under load.
* **[XQuery](xquery.md)** — XQuery 3.1: the two entry points, getting output,
  external variables, options, and what is not implemented.
* **[XQuery plan](xquery-plan.md)** — *historical*: the plan written before
  the work, kept for the design rationale, in particular why the parser reads
  source rather than a token stream.
* **[Validating XML](validation.md)** — what this engine can and cannot check,
  and how to combine it with the pieces it does not provide.
* **[XSD](xsd.md)** — the schema validator in detail: versions, resolvers,
  limits, the PSVI, concurrency, and where conformance stands.
* **[Options](options.md)** — every configuration field in the four packages,
  what its zero value means, and worked examples. Start here when you want to
  know what you can change.
* **[Security](security.md)** — the threat model, what is verified safe (XXE,
  entity expansion, regex backtracking), what a caller must still do, and the
  findings from the audit that produced them.
* **[Conformance gaps](conformance-gaps.md)** — the current figures, and a
  case-by-case verdict on every failure: fixable, open, or unreachable, with
  the reason. This is where the numbers live.
* **[Known gaps](known-gaps.md)** — why the hard gaps are hard: deliberate
  refusals, the two engine limits with their reverted fix attempts, and the
  unimplemented rules. The diagnosis behind the verdicts above, and
  deliberately free of percentages so that no figure drifts between the two.
* **[Reaching 100%](reaching-100.md)** — what the remaining distance to a
  perfect score actually consists of, and which parts of it are worth buying.
* **[TODO](todo.md)** — what is left: features, the measured bug tail, and
  the non-goals recorded so they are not proposed again as oversights.
* **[Recipes](recipes.md)** — batch validation, splitting documents, reporting
  line numbers, rendering to HTML, custom document resolvers.

Start with [validation.md](validation.md) if you are here to check documents
rather than to transform them. It begins with what this library is *not*,
which is the part most likely to save you time.
