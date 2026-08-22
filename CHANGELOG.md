# Changelog

Notable changes, newest first. Versions follow [semantic
versioning](https://semver.org), with the caveat that a `0.x` release may
break the API — see *Stability* below.

## v0.1.0 — 2026-08-22

First tagged release. Everything before this was unversioned, so this entry
describes what the library does rather than what changed.

### What it is

XPath 2.0, XSLT 2.0 and three schema languages in pure Go. No cgo, no JVM, no
libxml2.

| | Suite | Result |
|---|---|---|
| XPath 2.0 | W3C QT3 (FOTS) | 99.99% — 15,180 of 15,181 in scope |
| XSD 1.0 | W3C xsdtests | 99.80% instance · 98.60% schema-validity |
| XSD 1.1 | W3C xsdtests | 99.79% instance · 97.96% schema-validity |
| RELAX NG | James Clark's spectest | 100.00% — 965 of 965 |
| DTD | *no public suite* | content models, defaults, `ID`/`IDREF` |
| XSLT 2.0 | *no public suite* | differential against Saxon-HE 12.4 |

XSLT and DTD have no percentage because no redistributable conformance suite
exists for either. That is stated rather than papered over: neither figure is
comparable to a suite percentage and neither should be quoted as one.

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

* XSD schema-validity, at 98.60% (1.0) and 97.96% (1.1). Instance validation —
  what most callers do — is above 99.7% in both. A substantial share of the
  remaining disagreements are cases the W3C's own suite marks as disputed.
* RELAX NG's compact syntax is not implemented; only the XML syntax is.
* A DTD's external subset is never fetched, so validation against one is
  partial by design. `DTD.HasExternalSubset` says when that happened.

### Stability

The API is pre-1.0. The shape is settled and the conformance figures are not
expected to move down, but names may still change — `relaxng` narrowed its
exported surface from 27 symbols to 7 shortly before this release, which is
the kind of change a `0.x` version exists to allow.
