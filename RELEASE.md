# Releasing

There was no document for this, and `fn:system-property('xsl:product-version')`
has now been wrong twice because of it. First it reported `0.1` through the
1.0, 1.1 and 1.2 releases — the constant behind it was a release step nobody
had written down, so it was a release step nobody did. Then the remedy for
that, reading the version from `debug.ReadBuildInfo`, reported `0.0.0` to
every program that embeds this library, because what build info reports is the
version of the *importing* program.

This file is the missing step, written down.

It is a root-level file rather than a section of `docs/testing.md` or
`CONTRIBUTING.md` on purpose. `CONTRIBUTING.md` is the rules for writing code
here — every entry is a defect class learned the expensive way — and
`docs/testing.md` is how to run and read the gate. Releasing is neither: it is
a procedure with an ordering that matters, performed a few times a year by
someone who will not remember it. It also has to be findable from the
repository root by someone who has never read `docs/`.

---

## The design in one line

**The constant is the source of truth; the tag follows it.**

`internal/version/version.go` holds `const Version = "X.Y.Z"`, edited by hand.
Everything else — what CI asserts, what the release workflow refuses, what
`fn:system-property('xsl:product-version')` answers — is derived from that
line.

It is `internal/version` and not `xslt` because it is the **module's**
version. `xsd`, `relaxng`, `xpath` and `xdm` are released under the same tag
and are equally entitled to ask; a constant in `xslt` would have made every
one of them import the heaviest package in the repository to learn what
release it belongs to, and would have said — falsely — that the version is
XSLT's. It stays internal because the version is not part of the API: it
answers an XSLT contract, and exporting it would add something every future
release has to keep working, to no one's benefit.

The inverse arrangement is the tempting one: tag first, and have a generator
or a workflow write the constant to match. It is wrong for one reason. The
constant must be committed **before** the tag exists, or the tag points at a
commit that claims the previous version — and a build from that tag then
reports the wrong version, which is the original bug with extra steps. The
only way to repair it after the fact is to amend the commit or move the tag,
i.e. to rewrite published history from CI. So the workflow does not repair
anything. It **refuses**: a tag that disagrees with the constant fails the
release, loudly, before anything is pushed or created.

## Why a hand-edited constant is safe now

It was not before. The property was a hand-typed constant once and went stale
for three releases. The lesson people usually draw from that is "do not let a
human type it", and generation is the usual remedy.

That is the wrong diagnosis. The failure was not the typing; it was that
**nothing checked it**. A right constant and a wrong one were
indistinguishable to the build, so the wrong one survived three tags and was
found by a user rather than by CI. Generation would have fixed that defect by
removing the human, but so does a check — with one less moving part, no
generator to run, no generated file to review, and no dependency on git tags
in the everyday build.

Two checks cover it:

| Check | Where | What it catches |
|---|---|---|
| `internal/version.TestVersionIsReleasedAndDescribed` | every CI run, every push and PR | `Version` is not an `N.N.N` triple, or `CHANGELOG.md` has no `## vX.Y.Z` section for it, or an `## Unreleased` heading sits *below* that section — i.e. the release heading was never renamed |
| `release.yml`, pre-tag checks | the tag push | the tag does not equal the constant; the changelog section is missing or empty |

The CI check asserts only what is true **without** a tag, which is the point.
A check that ran `git describe` could not run under `actions/checkout`'s
default shallow, tagless clone — it would skip, and a guard that skips in CI
is a guard that does not run in the one place running is automatic. Nothing in
the everyday path touches tags, so nothing needs `fetch-depth: 0`.

What the CI check cannot see is a tag disagreeing with the constant, because
on an ordinary commit there is no tag to disagree. That is exactly the gap the
tag-push workflow closes.

---

## The manual steps

Four, in this order. The order is the whole point: the version commit precedes
the tag.

1. **Edit `internal/version/version.go`.** Set `const Version` to the version you are about
   to release, without the leading `v`. This is the source of truth.

2. **Rename the changelog heading.** In `CHANGELOG.md`, rename `## Unreleased`
   to `## vX.Y.Z — YYYY-MM-DD`, with an em dash, matching the sections above
   it. The release notes are built from this section verbatim, so it must not
   be empty.

   This step has been missed before: **v1.2.2 was tagged with `## Unreleased`
   still at the top of the changelog**, which is why the check below looks for
   it. An `## Unreleased` section *above* the released one is fine — that is
   the next cycle's notes accumulating — but one *below* it means this rename
   never happened.

3. **Commit.** Both files together:

   ```
   git add internal/version/version.go CHANGELOG.md
   git commit -m "release: vX.Y.Z"
   git push
   ```

   Let CI go green first. `TestVersionIsReleasedAndDescribed` runs here and
   will tell you if the constant and the changelog disagree — which is much
   cheaper to find now than after the tag is public.

4. **Tag and push the tag.**

   ```
   git tag -a vX.Y.Z -m "go-xml vX.Y.Z"
   git push origin vX.Y.Z
   ```

   If `w3cschemas` should be released too, put a line in the **tag message**:

   ```
   git tag -a vX.Y.Z -m "go-xml vX.Y.Z

   w3cschemas: v0.4.0"
   ```

   See *The w3cschemas version* below for why this is an input rather than
   something the workflow works out.

## What the workflow then does

`.github/workflows/release.yml`, on `push: tags: ['v*']`, in this order:

1. **Refuses a tag that disagrees with the constant.** `v1.3.1` against
   `Version = "1.3.0"` fails here. Nothing has been written yet, so the fix is
   to delete the tag, correct the commit, and tag again.
2. **Refuses a missing or empty changelog section**, and extracts that section
   as the release body.
3. **Runs the full gate** — `tests/check.sh`, all four W3C suites — on the
   tagged commit, and uploads the release verification record as
   `release-record-vX.Y.Z`.
4. **Creates the GitHub release** with the changelog section as the body.
5. **Bumps, tests and tags `w3cschemas`**, only if the tag message asked for
   it: `go mod edit` the pin to the new go-xml version, `go mod tidy`, build,
   vet, test, commit onto the default branch, tag `w3cschemas/vX.Y.Z`.

The release comes **before** the `w3cschemas` steps on purpose. The GitHub
release is the primary artifact; the pin bump is a follow-up that happens to
be automatable. In the other order a `w3cschemas` failure — a protected
default branch, a version number already taken — would block the release of
go-xml itself, which is already fully verified by step 3.

Steps 1–3 write nothing. A failure in any of them leaves the repository
exactly as it was, with the tag pushed and nothing else done — delete the tag,
fix, re-tag.

### If it fails after step 3

The tag exists and the release may have been created. There is no automatic
unwind, deliberately. Finish by hand — the remaining work is a `go mod edit`,
a commit, a tag push and possibly a `gh release create`, all of which the
workflow logs show in full.

Note that if the default branch is protected against direct pushes, step 5
will always fail. That is not worked around: the alternatives are weakening
the protection or forcing past it, and a failed step with a clear log is
better than either. The pin bump is then a normal pull request.

## The w3cschemas version

`w3cschemas` is a second module, published separately because the schemas it
bundles are under W3C terms rather than MIT. **Its version does not track this
one**, and the workflow does not try to derive it:

| go-xml | w3cschemas |
|---|---|
| v1.1.0 | w3cschemas/v0.1.0 |
| v1.2.0 | *(no release)* |
| v1.2.1 | *(no release)* |
| v1.2.2 | w3cschemas/v0.2.0 |
| v1.3.0 | w3cschemas/v0.3.0 |

The minor number counts **w3cschemas releases**, not go-xml ones, and two
go-xml releases produced none at all. Any rule derived from the parent version
would have tagged `w3cschemas/v1.3.0`, and a module version is permanent once
the proxy has seen it.

So it is an explicit input in the tag message, and the step is **skipped**
when the line is absent rather than guessed — skipping is recoverable (tag it
by hand afterwards), guessing is not. The workflow also refuses a version
whose tag already exists.

The common case is to omit it: `w3cschemas` needs a release only when its own
content changed. Note that its `go.mod` pins a *published* go-xml release, so
its CI job measures it against that pin rather than against the working tree —
bumping the pin after a release is what closes that gap, which is why the bump
is a release step and not a development one.

## Permissions

`ci.yml` keeps `permissions: contents: read` and is unchanged by any of this.

`release.yml` declares `contents: read` at the top level and grants
`contents: write` to the single `release` job, which is the only write grant
in the repository. It is needed for exactly three things: pushing the
`w3cschemas` pin commit, pushing the `w3cschemas/vX.Y.Z` tag, and creating the
GitHub release.

It cannot be reached from untrusted input. The workflow's only trigger is
`push: tags`, which fires only when a ref is pushed to this repository and
therefore only for someone who already has push access — someone who could
already do all three of those things by hand. There is deliberately no
`pull_request` trigger (a fork's PR must never reach a write-scoped job), no
`pull_request_target`, and no `workflow_dispatch`: a dispatch button is one
settings change away from running the write-scoped job on an arbitrary ref,
and "only a tag push reaches it" is the entire safety argument.

## Checklist

- [ ] `internal/version/version.go` bumped
- [ ] `CHANGELOG.md` heading renamed from `## Unreleased`, section non-empty
- [ ] committed and pushed, CI green
- [ ] annotated tag pushed, with a `w3cschemas:` line if that module is being
      released too
- [ ] the Release workflow went green, and the GitHub release has the right
      notes
