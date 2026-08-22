# Security Policy

## Reporting a vulnerability

Report privately, not as a public issue: open a
[security advisory](https://github.com/knroy/go-xml/security/advisories/new),
or email <rax.komol@gmail.com>.

Please include a minimal reproducing document or schema. This library's whole
job is to handle input that is trying to break it, so a concrete input is worth
more than a description.

Expect an acknowledgement within a week. This is a single-maintainer project,
not a vendor with an on-call rotation — if that response time does not suit
your situation, factor that in before depending on it.

## What counts as a vulnerability

This library parses untrusted XML on servers, so anything that lets a document
do more than be rejected is in scope:

* **Reading anything off the machine** — a file, a URL, a DNS lookup —
  triggered by a document or schema, without the caller having enabled it.
* **Resource exhaustion** disproportionate to the input: an entity bomb, a
  quadratic blowup, unbounded memory or recursion. The limits are in
  `ParseOptions`; a way past them is a bug.
* **A document accepted that the schema forbids**, when the caller is relying
  on validation as a trust boundary.
* **A panic on any input.** Callers validate in request handlers; a panic is a
  denial of service.

## What does not

* **A document rejected that a schema permits.** A false rejection is a
  conformance bug — file it as an issue, they are taken seriously, but it is
  not a security matter.
* **Behaviour after a feature was explicitly enabled.** `AllowDOCTYPE`, a
  `DocumentResolver`, a `relaxng.Resolver`, an `xsd` network resolver: each is
  off by default and each exists to let a caller decide what may be reached.
  Enabling one and then reaching what it permits is the feature working.
* **The known gaps.** Every measured conformance failure is listed in
  [docs/known-gaps.md](docs/known-gaps.md).

## The defaults, and why they are what they are

Three that most often surprise:

* **A `DOCTYPE` is refused** unless `AllowDOCTYPE` is set. It is the entry
  point for XXE and for entity expansion, so permitting one is a decision to
  make per input source rather than globally.
* **`xsi:schemaLocation` is ignored.** Honouring it lets a document choose the
  schema it is validated against, which is the document grading its own work.
* **No schema, document or entity is fetched** unless a resolver is supplied.
  There is no default resolver anywhere in this library, in any package.

[docs/security.md](docs/security.md) has the threat model, the audit results,
and the two things a caller must still do themselves.
