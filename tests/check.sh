#!/bin/sh
# check.sh — everything that has to pass before a change is called done.
#
# Written because the useful checks were being reassembled from memory each
# time, and the ones that got skipped were the ones that caught the most: a
# schema-validity rule looks fine against the W3C suite and still breaks real
# schemas, and a fix aimed at one suite can quietly cost ground in another.
#
#   tests/check.sh          # everything available
#   tests/check.sh fast     # build, vet, unit tests, race — no external suites
#
# Suites and corpora are third-party and are not vendored. Point these at your
# own checkouts, or let the defaults find them under testdata/:
#
#   GOXSLT_QT3=<dir>    github.com/w3c/qt3tests    (default testdata/qt3tests)
#   GOXSLT_XSDTS=<dir>  github.com/w3c/xsdtests    (default testdata/xsdtests)
#   GOXSLT_XSLTS=<dir>  github.com/w3c/xslt30-test (default testdata/xslt30-test)
#   GOXSLT_RNG=<file>   spectest.xml from relaxng/jing-trang
#                                                  (default testdata/relaxng/spectest.xml)
#   GOXSLT_XSLTNG=<dir> docbook/xslTNG        (default testdata/xsltng)
#   GOXSLT_XSPEC=<dir>  xspec/xspec           (default testdata/xspec)
#   GOXSLT_UBL=<dir>    UBL 2.1, the directory holding maindoc/
#   GOXSLT_CII=<dir>    UN/CEFACT CII or EN 16931 schemas
#
# A missing suite is reported and skipped; a suite that is present but produces
# no result is a failure. A check that did not run must never look like a check
# that succeeded — which is exactly what happened the first time this script
# ran: a relative GOXSLT_QT3 resolved against ./qt3/ rather than the repository
# root, the test skipped itself, and `go test` reported PASS.
#
# The last four of those are expected to be absent in CI, which fetches only
# the four W3C suites. UBL and CII are licensed corpora that cannot be cloned
# in a workflow at all; DocBook xslTNG and XSpec could be, and are not, because
# each needs a build step of its own before it yields anything to transform.
# They are measured locally instead, and their ratchet lines exist so that a
# developer who does have them cannot lose ground silently. Four skips in a CI
# log are therefore the normal reading, not a broken checkout — the failure
# mode to look for is a corpus that is present and reports nothing.

set -eu

# Everything below is relative to the repository root, whatever the caller's
# working directory is.
cd "$(dirname "$0")/.."
ROOT=$(pwd)

GO="${GO:-go}"
MODE="${1:-full}"

# The suite paths are made absolute because the qt3 test runs with its own
# package directory as the working directory.
abspath() {
	case "$1" in
	/*) printf '%s\n' "$1" ;;
	*)  printf '%s/%s\n' "$ROOT" "$1" ;;
	esac
}

# ratchet compares a suite's passing count against the highest this repository
# has recorded, and fails if it has gone down.
#
# The percentages elsewhere in this script are printed rather than asserted,
# because a hard threshold turns every upstream suite update into a build
# break. A ratchet does not: an update that adds cases moves the in-scope
# count, and only the PASSING count going down is reported — which is a
# regression however the suite changed underneath it.
#
# It exists because the build-and-test gate cannot see a silent revert. An
# agent committing a stale copy of a shared file over an additive change
# leaves a tree that compiles and a suite that still passes, because other
# work is landing wins in parallel; the total looks plausible and nothing
# flags it. That happened here, to commit 9843c44, and was found only by
# accident. A number that may not go down is what catches it.
#
# Load sensitivity used to make this fire on unmodified trees, and the cause
# was the harness rather than the engine. Three cases in the "catalog" set
# parse every non-error stylesheet in the suite inside a single transform;
# catalog-005b and catalog-007 are the two that came closest to the old 10s
# per-case deadline. An idle machine finished them and a loaded one did not,
# so the reported figure moved with whatever else was running on the box --
# 8605 in CI under a parallel build, 8607 on a quiet laptop -- and the mark
# had to be carried at the low end to stay usable.
#
# The per-case deadline is now 60s (GOXSLT_CASE_TIMEOUT overrides it), which
# is far enough from the wall that both cases finish on a loaded runner. The
# mark is therefore the real figure rather than the degraded one. If this
# starts drifting again, re-measure before recording a new mark: a number that
# moves between runs is measurement noise, and recording the high end of it
# just moves the false failure to the next slow machine.
#
# Set GOXSLT_RATCHET=update to record a new high after a deliberate change,
# or GOXSLT_RATCHET=off to skip the check entirely.
RATCHET_FILE="$ROOT/tests/ratchet.txt"
ratchet() {
	_t=$1
	_passed=$(printf '%s' "$2" | sed -n 's/.*in-scope: \([0-9]*\) passed.*/\1/p' | head -1)
	[ -n "$_passed" ] || return 0
	case "${GOXSLT_RATCHET:-on}" in
	off) return 0 ;;
	esac
	_best=$(sed -n "s/^$_t \([0-9]*\)$/\1/p" "$RATCHET_FILE" 2>/dev/null | head -1)
	if [ -n "$_best" ] && [ "$_passed" -lt "$_best" ]; then
		fail "$_t: $_passed passing, down from $_best.
    A passing count that went down is a regression even where the suite still
    reports PASS. If this is deliberate, record it:
        GOXSLT_RATCHET=update tests/check.sh"
		return 0
	fi
	if [ "${GOXSLT_RATCHET:-on}" = update ] ||
		{ [ -n "$_passed" ] && [ -z "$_best" ]; } ||
		{ [ -n "$_best" ] && [ "$_passed" -gt "$_best" ]; }; then
		touch "$RATCHET_FILE"
		_tmp="$RATCHET_FILE.tmp"
		grep -v "^$_t " "$RATCHET_FILE" > "$_tmp" 2>/dev/null || true
		printf '%s %s\n' "$_t" "$_passed" >> "$_tmp"
		sort -o "$RATCHET_FILE" "$_tmp"
		rm -f "$_tmp"
		printf -- '--- ratchet: %s high-water mark now %s\n' "$_t" "$_passed"
	fi
}

# ratchetXSD is ratchet for the XSD driver, which reports "TOTAL agree N
# disagree M" rather than the "in-scope: N passed" the Go suites log. The
# number that may not go down is the agreement count.
ratchetXSD() {
	_t=$1
	_agree=$(printf '%s' "$2" | sed -n 's/^TOTAL[^0-9]*\([0-9]*\).*/\1/p' | head -1)
	[ -n "$_agree" ] || return 0
	# The schema and instance halves are quoted separately in README.md and
	# docs/, and the TOTAL cannot reconstruct them, so each gets its own mark
	# (XSD10S, XSD10I, ...) for tests/docfigures.sh to read.
	# ratchetCount assigns _t itself, and these are POSIX shell functions with
	# no locals, so the names are built and the calls made before _t is read
	# again below. Getting this wrong once wrote marks named XSD10SI.
	_xsdS="${_t}S" _xsdI="${_t}I"
	_s=$(printf '%s' "$2" | sed -n 's/^SCHEMA[^0-9]*\([0-9]*\).*/\1/p' | head -1)
	_i=$(printf '%s' "$2" | sed -n 's/^INSTANCE[^0-9]*\([0-9]*\).*/\1/p' | head -1)
	[ -n "$_s" ] && ratchetCount "$_xsdS" "$_s"
	[ -n "$_i" ] && ratchetCount "$_xsdI" "$_i"
	_t=$1
	case "${GOXSLT_RATCHET:-on}" in
	off) return 0 ;;
	esac
	_best=$(sed -n "s/^$_t \([0-9]*\)$/\1/p" "$RATCHET_FILE" 2>/dev/null | head -1)
	if [ -n "$_best" ] && [ "$_agree" -lt "$_best" ]; then
		fail "$_t: $_agree agreeing, down from $_best.
    An agreement count that went down is a regression even where the suite
    still reports totals. If this is deliberate, record it:
        GOXSLT_RATCHET=update tests/check.sh"
		return 0
	fi
	if [ "${GOXSLT_RATCHET:-on}" = update ] ||
		{ [ -n "$_agree" ] && [ -z "$_best" ]; } ||
		{ [ -n "$_best" ] && [ "$_agree" -gt "$_best" ]; }; then
		touch "$RATCHET_FILE"
		_tmp="$RATCHET_FILE.tmp"
		grep -v "^$_t " "$RATCHET_FILE" > "$_tmp" 2>/dev/null || true
		printf '%s %s\n' "$_t" "$_agree" >> "$_tmp"
		sort -o "$RATCHET_FILE" "$_tmp"
		rm -f "$_tmp"
		printf -- '--- ratchet: %s high-water mark now %s\n' "$_t" "$_agree"
	fi
}

# ratchetCount is ratchet for a driver that reports a bare number rather than
# a suite summary line. The number that may not go down is passed directly.
ratchetCount() {
	_t=$1
	_n=$2
	[ -n "$_n" ] || return 0
	case "${GOXSLT_RATCHET:-on}" in
	off) return 0 ;;
	esac
	_best=$(sed -n "s/^$_t \([0-9]*\)$/\1/p" "$RATCHET_FILE" 2>/dev/null | head -1)
	if [ -n "$_best" ] && [ "$_n" -lt "$_best" ]; then
		fail "$_t: $_n transformed, down from $_best.
    A count that went down is a regression. If this is deliberate, record it:
        GOXSLT_RATCHET=update tests/check.sh"
		return 0
	fi
	if [ "${GOXSLT_RATCHET:-on}" = update ] ||
		{ [ -n "$_n" ] && [ -z "$_best" ]; } ||
		{ [ -n "$_best" ] && [ "$_n" -gt "$_best" ]; }; then
		touch "$RATCHET_FILE"
		_tmp="$RATCHET_FILE.tmp"
		grep -v "^$_t " "$RATCHET_FILE" > "$_tmp" 2>/dev/null || true
		printf '%s %s\n' "$_t" "$_n" >> "$_tmp"
		sort -o "$RATCHET_FILE" "$_tmp"
		rm -f "$_tmp"
		printf -- '--- ratchet: %s high-water mark now %s\n' "$_t" "$_n"
	fi
}

# ratchetVendored is ratchetCount for the vendored schema corpus. It is a
# separate function only because of its failure message: what a drop here
# MEANS is specific, and the number is useless to whoever reads it in CI
# unless the message says so.
#
# The count is schemas that still assemble. Nothing in this repository makes
# a real schema stop loading except a schema-validity rule that has become
# stricter than the spec -- which is the one defect the W3C suite structurally
# cannot catch, because it scores agreement with its own labels and an
# over-strict rule only shows up there if the suite happens to contain a valid
# schema exercising it. So a drop here is not "a test broke"; it is the
# specific news that a rule added upstream of it now rejects a schema that a
# human being wrote and shipped.
ratchetVendored() {
	_n=$1
	[ -n "$_n" ] || return 0
	case "${GOXSLT_RATCHET:-on}" in
	off) return 0 ;;
	esac
	_t=VendoredSchemas
	_best=$(sed -n "s/^$_t \([0-9]*\)$/\1/p" "$RATCHET_FILE" 2>/dev/null | head -1)
	if [ -n "$_best" ] && [ "$_n" -lt "$_best" ]; then
		fail "$_t: $_n real schemas still load, down from $_best.
    A SCHEMA-VALIDITY RULE HAS BECOME TOO STRICT. These are real schemas that
    people wrote and shipped, and $((_best - _n)) of them loaded before your
    change and do not now. That is over-strictness, and it is the one defect
    the W3C suite cannot see: the suite scores agreement with its own labels,
    so a rule stricter than the spec shows up there only if the suite happens
    to contain a valid schema exercising it.
    Run the corpus to see which, and read the first error of each:
        go run ./tests/corpora vendored testdata/xslt30-test \\
            testdata/qt3tests testdata/xspec
    Compare like with like: the mark covers all three roots, so a run that
    omits one reports a smaller count that is not a regression.
    Fix the rule. Record a new mark ONLY if you have established that
    rejecting those schemas is correct and the spec requires it:
        GOXSLT_RATCHET=update tests/check.sh fast"
		return 0
	fi
	if [ "${GOXSLT_RATCHET:-on}" = update ] ||
		{ [ -n "$_n" ] && [ -z "$_best" ]; } ||
		{ [ -n "$_best" ] && [ "$_n" -gt "$_best" ]; }; then
		touch "$RATCHET_FILE"
		_tmp="$RATCHET_FILE.tmp"
		grep -v "^$_t " "$RATCHET_FILE" > "$_tmp" 2>/dev/null || true
		printf '%s %s\n' "$_t" "$_n" >> "$_tmp"
		sort -o "$RATCHET_FILE" "$_tmp"
		rm -f "$_tmp"
		printf -- '--- ratchet: %s high-water mark now %s\n' "$_t" "$_n"
	fi
}

QT3=$(abspath "${GOXSLT_QT3:-testdata/qt3tests}")
XSDTS=$(abspath "${GOXSLT_XSDTS:-testdata/xsdtests}")
RNG=$(abspath "${GOXSLT_RNG:-testdata/relaxng/spectest.xml}")
XSLTS=$(abspath "${GOXSLT_XSLTS:-testdata/xslt30-test}")
UBL="${GOXSLT_UBL:-}"
CII="${GOXSLT_CII:-}"
XSLTNG=$(abspath "${GOXSLT_XSLTNG:-testdata/xsltng}")
XSPEC=$(abspath "${GOXSLT_XSPEC:-testdata/xspec}")
[ -n "$UBL" ] && UBL=$(abspath "$UBL")
[ -n "$CII" ] && CII=$(abspath "$CII")

failed=0
skipped=""

# The release record. Everything above and below this line already measures
# something; what did not exist was ONE artifact that says "this tree was
# verified, here is the toolchain, and here is what every lane reported".
#
# The pieces were all present and all separate: vet at its section, the full
# package run at its own, race at its own, each W3C driver printing its own
# summary, and provenance in tests/last-run.txt. A reader assembling a release
# claim had to scrape a transcript for nine figures and then argue that the
# transcript belonged to the commit -- which is the same "measured against
# what?" that provenance was written to answer, one level up. Provenance says
# WHAT was measured; this says WHAT THE MEASUREMENT SAID.
#
# The property that makes it worth having is the third column. A record that
# lists only the lanes that ran is a record that lies by omission: the
# conformance job has corpora the fast job does not, and DocBook, XSpec, UBL
# and CII are deliberately absent from CI (ci.yml and the header above). A
# release note reading "all suites pass" over a run where four never started
# is worse than no note at all, because it cannot be distinguished from one
# where they did. So every lane declares itself PASS, FAIL or SKIP, a skipped
# lane names why, and the verdict line at the end counts all three.
#
# NOTHING here is typed. Every figure is the detail string a lane already
# extracted from its own driver output; `lane` is handed that string and
# stores it. A number written into this file by hand would be exactly the
# defect the generated-figures work removed -- a figure with no command behind
# it, correct on the day it was pasted and unfalsifiable afterwards.
RECORD_FILE="$ROOT/tests/release-record.txt"
_record=""

# lane records one verification lane: its name, its verdict, and the measured
# detail the lane itself produced.
#
#   lane vet PASS "go vet ./..."
#   lane "XSLT 3.0" PASS "in-scope: 11518 cases, 11484 passed"
#   lane DocBook SKIP "not fetched in CI"
#
# Appended in call order, so the file reads in the order the gate ran.
lane() {
	_record="${_record}$(printf '%-22s %-4s %s' "$1" "$2" "$3")
"
}

# laneFromStatus is lane for a step whose only result is whether it failed.
# It reads the failure counter around the step rather than a driver summary,
# so that a lane cannot be recorded PASS by a caller who forgot to check.
laneFromStatus() {
	if [ "$2" -eq "$failed" ]; then lane "$1" PASS "$3"; else lane "$1" FAIL "$3"; fi
}

# emit_record writes the one file. It is called from BOTH exits -- the fast
# mode return and the end of a full run -- because a record that exists only on
# the path nobody takes when debugging is a record nobody has.
#
# Content is provenance (the same function that fills tests/last-run.txt, so
# the two can never disagree about what was measured), then the lanes in the
# order they ran, then a verdict that counts them. The verdict says PASS only
# when nothing failed AND nothing was skipped; a run with skips is VERIFIED
# WITH GAPS, spelled out rather than left for a reader to notice that a suite
# is missing from a list. That distinction is the whole point: "all suites
# pass" over a run where four never started is indistinguishable from one
# where they did, unless the file says so itself.
#
# tests/release-record.txt is GITIGNORED for the reason tests/last-run.txt is
# -- see the PROVENANCE_FILE comment above, whose argument this follows. It
# changes on every run, so committing it would put a diff in the tree every
# time anyone ran the gate; and a committed copy would prove only what the last
# person to commit happened to run, which is the weaker claim. A release does
# not point at a file in the working tree: it points at the artifact this run
# uploaded, named for the tag, which is a record OF A MEASUREMENT and stays
# attached to the run that produced it. Committing one would be provenance for
# the commit, which is the thing the tag already is.
emit_record() {
	_skips=$(printf '%s' "$_record" | grep -c ' SKIP ' || true)
	_fails=$(printf '%s' "$_record" | grep -c ' FAIL ' || true)
	if [ "$_fails" -gt 0 ]; then
		_verdict="FAILED — $_fails lane(s) failed, $_skips skipped"
	elif [ "$_skips" -gt 0 ]; then
		_verdict="VERIFIED WITH GAPS — every lane that ran passed, $_skips did not run"
	else
		_verdict="VERIFIED — every lane ran and passed"
	fi
	{
		printf 'go-xml release verification record\n'
		printf 'mode         %s\n' "$MODE"
		provenance
		printf '\nlanes\n'
		printf '%s' "$_record"
		printf '\nverdict      %s\n' "$_verdict"
	} > "$RECORD_FILE" 2>/dev/null || {
		printf -- '--- NOT written to tests/release-record.txt (not writable)\n'
		return 0
	}
	section "release record"
	cat "$RECORD_FILE"
	printf -- '--- written to tests/release-record.txt\n'
}

section() { printf '\n=== %s\n' "$1"; }
fail()    { printf 'FAIL: %s\n' "$1"; failed=1; }
skip()    { skipped="${skipped}  - $1
"; }


# Provenance. Every figure this script prints is a measurement, and a
# measurement whose conditions are not recorded is a number someone will later
# read as current.
#
# That is not hypothetical here: an external audit report was written against a
# tree nobody can now identify, quoted counts that no longer matched, and was
# read as a description of this repository — and the reason it could not be
# refuted on the spot is that WE COULD NOT PROVE WHAT WE HAD MEASURED EITHER.
# tests/ratchet.txt holds bare "<name> <count>" pairs; the CI cache key is the
# static string suites-v2, so even the suite revision behind a CI figure was
# unrecoverable. Two numbers from different trees, different Go versions and
# different suite checkouts looked exactly alike.
#
# So: Go version, repo commit and whether the tree was dirty, GOOS/GOARCH, the
# UTC time, and the checkout revision of every suite present. Printed into the
# transcript, where whoever reads a failing CI log sees it, AND written to
# tests/last-run.txt, which survives after the log is gone.
#
# It is NOT written to tests/ratchet.txt. The ratchet rewrites that file
# in place -- grep -v the line, append the new one, sort -- so anything else
# living there would be destroyed by the first count that moved.
#
# tests/last-run.txt is deliberately GITIGNORED. It changes on every run, so
# committing it would put a diff in every gate run and make the ratchet's own
# commits unreadable; and a committed copy would still only prove what the last
# person to commit ran, which is the weaker of the two claims anyone wants.
# What proves a figure is the file emitted BESIDE that figure -- attached to a
# CI run, or pasted into the issue that quotes the number -- and CI uploads it
# for exactly that reason. A file in git would be provenance for the commit;
# this is provenance for the measurement.
PROVENANCE_FILE="$ROOT/tests/last-run.txt"

# digest hashes stdin. The command differs between Linux and macOS, so it is
# resolved once rather than assumed; a record that says "(no sha256 tool)" is
# honest, and one that silently prints nothing is not.
digest() {
	if command -v sha256sum > /dev/null 2>&1; then
		sha256sum | cut -c1-16
	elif command -v shasum > /dev/null 2>&1; then
		shasum -a 256 | cut -c1-16
	else
		cat > /dev/null; printf '(no sha256 tool)'
	fi
}

# suiterev prints the revision of one vendored suite. They are separate
# checkouts under testdata/, not submodules, so each is asked on its own.
#
# The --show-toplevel comparison is the part that matters, and it must compare
# against the SUITE directory rather than against $ROOT. testdata/relaxng holds
# a single copied spectest.xml rather than a clone, and `git -C` there does not
# fail -- it walks UP and answers with whatever repository encloses it, which
# would record a go-xml commit as the RelaxNG suite revision and look entirely
# plausible. Comparing to $ROOT is not enough to catch that: in an agent
# worktree testdata/ is a symlink to the primary checkout, so the enclosing
# repository is a different path than $ROOT and the bogus answer survives. The
# only revision worth recording is one from a checkout whose ROOT IS THE SUITE.
# A suite that is not one is recorded as such; that is a fact about the
# measurement, not a reason to fail the gate.
suiterev() {
	_name=$1 _dir=$2
	[ -d "$_dir" ] || { printf '%-12s (absent)\n' "$_name"; return 0; }
	_top=$(git -C "$_dir" rev-parse --show-toplevel 2>/dev/null || true)
	# Both sides resolved through the same command so that a symlinked
	# testdata/ compares equal to the path git reports.
	_real=$(cd "$_dir" 2>/dev/null && pwd -P) || _real=""
	_topreal=$([ -n "$_top" ] && cd "$_top" 2>/dev/null && pwd -P) || _topreal=""
	if [ -z "$_topreal" ] || [ "$_topreal" != "$_real" ]; then
		printf '%-12s (not a git checkout of its own)\n' "$_name"
		return 0
	fi
	printf '%-12s %s\n' "$_name" \
		"$(git -C "$_dir" rev-parse HEAD 2>/dev/null || echo '(unknown)')"
}

provenance() {
	printf 'go           %s\n' "$($GO version 2>/dev/null || echo '(unknown)')"
	_head=$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo '(not a git checkout)')
	# A dirty tree is recorded rather than refused: the gate is run on work in
	# progress far more often than on a clean commit, and a figure measured on
	# uncommitted changes is exactly the one that must not be quoted as the
	# commit's.
	if [ -n "$(git -C "$ROOT" status --porcelain 2>/dev/null)" ]; then
		printf 'commit       %s (dirty)\n' "$_head"
	else
		printf 'commit       %s\n' "$_head"
	fi
	printf 'platform     %s/%s (%s)\n' \
		"$($GO env GOOS 2>/dev/null)" "$($GO env GOARCH 2>/dev/null)" \
		"$(uname -sm 2>/dev/null || echo unknown)"
	printf 'utc          %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
	# The dependency graph, as a digest plus its expansion. ci.yml's upload
	# step already described the artifact as carrying "the resolved module
	# graph and its digest" and it did not: provenance recorded the toolchain
	# and the suites but nothing about what the build linked against. A
	# release claim that names a Go version and not its dependencies is
	# answerable only for half of what produced the figures.
	_mods=$($GO list -m all 2>/dev/null || true)
	if [ -n "$_mods" ]; then
		printf 'modules      %s (%s modules)\n' \
			"$(printf '%s\n' "$_mods" | digest)" \
			"$(printf '%s\n' "$_mods" | grep -c .)"
		printf '%s\n' "$_mods" | sed 's/^/  /'
	else
		printf 'modules      (unavailable)\n'
	fi
	if [ -f "$ROOT/go.sum" ]; then
		printf 'go.sum       %s\n' "$(digest < "$ROOT/go.sum")"
	else
		printf 'go.sum       (absent)\n'
	fi
	suiterev qt3tests "$QT3"
	suiterev xsdtests "$XSDTS"
	suiterev xslt30 "$XSLTS"
	suiterev relaxng "$(dirname "$RNG")"
	suiterev xsltng "$XSLTNG"
	suiterev xspec "$XSPEC"
	[ -n "$UBL" ] && suiterev ubl "$UBL"
	[ -n "$CII" ] && suiterev cii "$CII"
	return 0
}

section "provenance"
# Written first, then echoed, rather than piped through tee: in a pipeline the
# write failing is the exit status of a subshell nobody reads, and provenance
# that silently did not persist is the exact failure this section exists to
# prevent. A tree that cannot be written to still prints -- the transcript is
# the half that matters in CI -- but it says so.
if provenance > "$PROVENANCE_FILE" 2>/dev/null; then
	cat "$PROVENANCE_FILE"
	printf -- '--- written to tests/last-run.txt\n'
else
	provenance
	printf -- '--- NOT written to tests/last-run.txt (not writable)\n'
fi
section "build"
_f0=$failed
$GO build ./... || fail "build"
laneFromStatus build "$_f0" "go build ./..."

section "vet"
_f0=$failed
$GO vet ./... || fail "vet"
laneFromStatus vet "$_f0" "go vet ./..."

# docfigure asserts that a number written in the documentation still equals the
# number the command beside it produces.
#
# This is NOT a ratchet. A ratchet records a high-water mark and only a DROP is
# a regression, which is right for a conformance score that moves with upstream
# and with the engine. A documented count is different: a test count that fell
# is exactly as stale as one that rose, because the claim is "this is how many
# there are", not "at least this many". So the comparison is equality and the
# marks live here rather than in tests/ratchet.txt, whose whole contract is
# "may not decrease".
#
# It exists because the two mechanisms were once measured against each other on
# this tree. Every conformance figure guarded by the ratchet was correct to the
# case, eight for eight; the unit test count -- guarded by nothing but prose --
# was stale by 59 in all four files that stated it. That gap is not diligence.
# One of the numbers is re-derived by a script that fails the build when it
# moves and the other is not, and reading does not close the difference: three
# of those four stale copies were written and reviewed by people who were at
# that moment actively hunting stale claims.
#
# THE COUNTING METHOD IS PART OF THE CLAIM. "How many tests" has several honest
# answers -- `func Test` declarations, subtests, table rows -- and a reader who
# re-derives it differently will report drift that is not there. Every figure
# below therefore carries its exact command, the same command is quoted in the
# documentation beside each number, and the failure message prints it. A figure
# whose method is not written down is not guarded; it is merely asserted twice.
#
# It runs in fast mode as well: it costs a few greps, it needs no suite on
# disk, and stale doc figures are the drift most likely to be introduced by a
# change that is otherwise entirely green.
#
# The counting commands. Each excludes .claude/worktrees, which holds agent
# checkouts of this same repository and would otherwise multiply every count.
docfigure_tests() {
	grep -rn "^func Test" --include='*_test.go' . |
		grep -vc '/\.claude/worktrees/'
}
docfigure_fuzz() {
	grep -rn "^func Fuzz" --include='*_test.go' . |
		grep -vc '/\.claude/worktrees/'
}
docfigure_limits() {
	grep -hc "^func Test" ./*/limits_boundary_test.go |
		awk '{n += $1} END {print n + 0}'
}

# docfigure name, expected, actual, files...
docfigure() {
	_what=$1 _want=$2 _got=$3
	shift 3
	[ "$_want" = "$_got" ] && return 0
	fail "$_what: the generator counts $_want, the documented grep counts $_got.
    counted by: $(docfigure_cmd "$_what")
    and by:
$(for _f in "$@"; do printf '        %s\n' "$_f"; done)
    These two must agree. The number published in README.md and docs/ comes
    from the generator, and the command printed beside it in the prose is the
    grep -- so a disagreement means the published figure is not reproducible by
    the command that claims to produce it. Fix whichever of the two is wrong
    (most often the Go walker's skip list and the grep's exclusions have
    drifted apart), then regenerate:
        go run tests/conformance-docs.go"
}

# The command text for a figure, printed by the failure above so that whoever
# reads it in CI can re-derive the number without opening this script. This is
# the same text quoted beside each figure in docs/stats.md, which is where the
# published copy of the number comes from.
docfigure_cmd() {
	case $1 in
	"unit test count")
		printf '%s' "grep -rn '^func Test' --include='*_test.go' . | grep -vc '/\\.claude/worktrees/'" ;;
	"fuzz target count")
		printf '%s' "grep -rn '^func Fuzz' --include='*_test.go' . | grep -vc '/\\.claude/worktrees/'" ;;
	"limit boundary test count")
		printf '%s' "grep -hc '^func Test' ./*/limits_boundary_test.go | awk '{n += \$1} END {print n + 0}'" ;;
	esac
}

# The expected values are no longer typed here either. They used to be -- three
# integers in this script, alongside the same three in five documents -- and
# that was the same defect one layer down: on 2026-09-11 the unit-test count
# was edited 2112 -> 2122 -> 2131 -> 2149 in five files each time, and this
# script was a sixth place to forget. The figures in the documents are now
# GENERATED regions fed by tests/conformance-docs.go, which counts the tree
# itself, so the question this section can still usefully ask is a different
# one: does the generator's count agree with the grep the documentation quotes?
#
# That is worth asking because the two use different implementations on
# purpose. The generator walks the tree in Go so that the figure is identical
# on Linux, macOS and Windows; the documentation quotes a grep pipeline a
# reader can paste. If those ever disagree, one of them is lying to somebody,
# and which one hardly matters -- the published number is no longer reproducible
# by the command printed beside it, which is the whole claim.
section "documented figures"
_docfig_before=$failed
_gen_counts=$($GO run ./tests/conformance-docs.go -counts 2>/dev/null)
_gen_tests=$(printf '%s\n' "$_gen_counts" | sed -n 's/^unit-tests \([0-9]*\)$/\1/p')
_gen_fuzz=$(printf '%s\n' "$_gen_counts" | sed -n 's/^fuzz-targets \([0-9]*\)$/\1/p')
_gen_limits=$(printf '%s\n' "$_gen_counts" | sed -n 's/^limit-boundary-tests \([0-9]*\)$/\1/p')
if [ -z "$_gen_tests" ] || [ -z "$_gen_fuzz" ] || [ -z "$_gen_limits" ]; then
	fail "tests/conformance-docs.go -counts did not report all three tree counts.
    It is the source of every published test count; without it the figures in
    README.md and docs/ are unguarded. Run it by hand to see the error:
        go run tests/conformance-docs.go -counts"
else
	docfigure "unit test count" "$_gen_tests" "$(docfigure_tests)" \
		'tests/conformance/stats.go (countKind "func-test")'
	docfigure "fuzz target count" "$_gen_fuzz" "$(docfigure_fuzz)" \
		'tests/conformance/stats.go (countKind "func-fuzz")'
	docfigure "limit boundary test count" "$_gen_limits" "$(docfigure_limits)" \
		'tests/conformance/stats.go (countKind "limits-boundary")'
fi
if [ "$failed" -eq "$_docfig_before" ]; then
	printf 'tests %s, fuzz targets %s, limit boundary tests %s — the generator and the documented grep agree\n' \
		"$(docfigure_tests)" "$(docfigure_fuzz)" "$(docfigure_limits)"
fi
# The conformance figures are checked the other way round: the ratchet has
# already measured them, so tests/docfigures.sh reads tests/ratchet.txt and
# looks for every copy in the documentation that disagrees. It anchors on
# in-scope denominators rather than line numbers; see its header.
if sh "$ROOT/tests/docfigures.sh"; then
	printf 'conformance figures in README.md and docs/ agree with tests/ratchet.txt\n'
else
	fail "a conformance figure in the documentation disagrees with tests/ratchet.txt (listed above).
    Re-derive it from the suite run, fix every copy, and check the sentence
    around it still says something true."
fi
laneFromStatus "documented figures" "$_docfig_before" "tests/docfigures.sh and the documented grep commands"

# The summary table at the top of docs/conformance-gaps.md is GENERATED, and
# this is the step that proves the checked-in copy still matches its source.
#
# Everything above guards a figure someone typed. The Total could not be
# guarded that way: it is the sum of the other rows, so every row can agree
# with the ratchet while the sum is wrong -- and it was. The document printed
# 168 disagreements while its own rows summed to 104, and no check anywhere saw
# it, because no check anywhere did the addition. Now nothing does the addition
# but tests/conformance-docs.go, reading tests/conformance/results.json, and a
# hand-edit to the table is a diff rather than a new fact.
#
# -check rather than regenerate-then-`git diff`: the tree is dirty in most runs
# of this gate, so a git diff here would report the user's own work in progress
# as a failure. -check reads the two files and compares, touching nothing.
#
# It covers every generated region, not only that table. docs/stats.md is
# generated end to end -- it is the one page carrying every figure with the
# command that produced it and the date it was measured -- and README.md,
# docs/conformance-gaps.md, docs/testing.md and docs/todo.md each carry small
# marked regions fed from the same two sources. A hand-edit to any of them is
# caught here.
section "generated conformance summary"
_f0=$failed
if $GO run ./tests/conformance-docs.go -check; then
	:
else
	fail "a generated region no longer matches tests/conformance/results.json and the tree.
    Every figure this repository publishes is generated: the summary table and
    its Total, the test, fuzz and limit counts, the status tables, and the
    whole of docs/stats.md. None of them is written by hand.
      - A suite figure moved: record the measured counts in
        tests/conformance/results.json -- passed, disagreements and total must
        add up, per suite.
      - A test count moved: nothing to record, the generator counts the tree.
    Either way, regenerate:
        go run tests/conformance-docs.go
    Then check that the hand-written prose AROUND each region still says
    something true; only the marked regions are rewritten, and a sentence that
    argues from a number does not update itself."
fi
laneFromStatus "generated figures" "$_f0" "go run ./tests/conformance-docs.go -check"

# GOXSLT_NO_SUITES keeps the conformance suites out of these two steps. They
# are the fast gate, and the suites get their own sections below; without it a
# run that has testdata/ on disk does the whole conformance job twice, because
# each suite harness falls back to ./testdata when its own variable is unset.
#
# Under -race that second run is not merely wasted: it is slow enough to pass
# go test's 10m default and panic rather than report, which is what CI saw --
# "panic: test timed out after 10m0s, running tests: TestXSLTSuite (1m53s)".
# The two figures only reconcile if the deadline was spent by the 3.0 suite
# before the 2.0 suite started, both of them running in the same binary.
#
# -timeout is therefore set explicitly here as well as in ci.yml, and
# -count=1 because a cached result cannot show a regression.
section "unit tests"
_f0=$failed
GOXSLT_NO_SUITES=1 $GO test ./... -count=1 || fail "unit tests"
laneFromStatus "package tests" "$_f0" "GOXSLT_NO_SUITES=1 go test ./... -count=1"

section "race"
_f0=$failed
GOXSLT_NO_SUITES=1 $GO test -race ./... -count=1 -timeout 25m || fail "race"
laneFromStatus race "$_f0" "GOXSLT_NO_SUITES=1 go test -race ./... -count=1 -timeout 25m"

# w3cschemas is a module of its own -- the schemas it bundles are W3C-licensed
# and the core module is MIT -- so `go list ./...` at the root does not reach
# it and none of the steps above cover it. It needs a step of its own or it is
# in no gate at all, which is what it was in: nothing here and nothing in
# ci.yml ran it.
#
# What this step measures is the module against the go-xml RELEASE its go.mod
# names, not against this tree. That is deliberate: w3cschemas is published
# separately, so the version it pins is the version its users get, and testing
# it against an unreleased tree would measure something nobody can install. It
# catches a broken w3cschemas; it does NOT catch an API break in this tree that
# would affect it. Bumping the pin after a release is what closes that gap, and
# is a release step rather than something to paper over with a workspace file.
section "w3cschemas (separate module)"
_f0=$failed
(cd w3cschemas && $GO build ./... && $GO vet ./... &&
	$GO test ./... -count=1) || fail "w3cschemas"
laneFromStatus w3cschemas "$_f0" "build, vet and test of the separate module"

section "vendored real-world schemas"
# The over-strictness guard that is always available.
#
# docs/known-gaps.md used to name UBL and CII as the ONLY guard against a
# schema-validity rule stricter than the spec, and both are licensed corpora
# that cannot be vendored. So on every checkout that does not already have
# them -- CI included -- schema rules were landing with that guard skipped.
# The skip was honest and the hole was real.
#
# These 230 schemas are real ones people wrote, they are already in the tree
# as fixtures for the XSLT and XQuery suites, and until now nothing ever
# asked whether they still LOAD. That is the same question UBL and CII ask,
# on a smaller and less commercial sample. It does not replace them: these
# are mostly test fixtures and documentation schemas, not the deep industry
# vocabularies UBL and CII exercise, so the blocks below stay exactly as
# they were.
#
# The number that may not go down is how many assemble. That makes this a
# ratchet rather than an equality check, for the reason the ratchet comment
# above gives: a schema that starts loading is progress, and only a schema
# that STOPS loading is the regression this exists to catch. It uses
# ratchetCount rather than ratchet or ratchetXSD because the driver reports
# a bare count of its own rather than an "in-scope: N passed" suite summary
# or the XSD driver's agreement line.
#
# Excluded schemas are printed with the count so the size of what is not
# scored stays visible; see vendoredExclude in tests/corpora for why each is
# not a standalone schema.
# The roots are only those actually present. The corpus skips a root it
# cannot find, which shrinks the DENOMINATOR silently -- and a ratchet reads
# a smaller denominator as schemas that stopped loading. That is exactly what
# happened: the mark was recorded on a tree holding XSpec, whose test/
# directory contributes 7 schemas, and CI does not fetch XSpec. It reported
# "178, down from 185: A SCHEMA-VALIDITY RULE HAS BECOME TOO STRICT" for 7
# files it had never opened.
#
# So the ratchet is only meaningful over a fixed set of roots. Naming which
# are missing keeps a count taken over fewer of them from being compared
# against one taken over more.
_vend_roots=""
_vend_absent=""
for _r in xslt30-test qt3tests xspec; do
	if [ -d "$ROOT/testdata/$_r" ]; then
		_vend_roots="$_vend_roots $ROOT/testdata/$_r"
	else
		_vend_absent="$_vend_absent $_r"
	fi
done
_vend=$($GO run ./tests/corpora vendored $_vend_roots 2>&1 >/dev/null | tail -1)
_vend_ok=$(printf '%s' "$_vend" | sed -n 's/^vendored schemas: \([0-9]*\) loaded.*/\1/p')
if [ -z "$_vend_ok" ]; then
	fail "vendored schemas: the corpus produced no result.
    These schemas are vendored in testdata/, so unlike UBL and CII this
    check has no legitimate skip. A corpus that is present and reports
    nothing is the failure mode to look for."
	lane "vendored schemas" FAIL "the corpus produced no result"
elif [ -n "$_vend_absent" ]; then
	# A count over fewer roots is not comparable with the recorded mark, so
	# it is reported and skipped rather than ratcheted. Skipped, not passed:
	# the guard genuinely did not run over what it was calibrated on.
	printf '%s\n' "$_vend"
	skip "vendored schemas: not ratcheted --$_vend_absent absent from testdata/ (the mark covers all three roots)"
	lane "vendored schemas" SKIP "not ratcheted --$_vend_absent absent from testdata/"
else
	printf '%s\n' "$_vend"
	ratchetVendored "$_vend_ok"
	lane "vendored schemas" PASS "$_vend"
fi

if [ "$MODE" = fast ]; then
	printf '\n=== fast mode: external suites not run\n'
	# Every external lane is recorded as skipped BY NAME rather than left out.
	# A fast run's record is a legitimate record -- it just is not a release
	# one, and the only thing that distinguishes them is that these lines are
	# present and say SKIP. Dropping them would produce a file whose verdict
	# line reads the same as a full run's.
	for _l in "W3C QT3 XPath" "W3C QT3 XQuery" "W3C XSD 1.0" "W3C XSD 1.1" \
		"RELAX NG spectest" "W3C XSLT 2.0" "W3C XSLT 3.0" \
		UBL CII DocBook XSpec; do
		lane "$_l" SKIP "fast mode: tests/check.sh fast does not run external suites"
	done
	emit_record
	if [ "$failed" -eq 0 ]; then printf 'OK\n'; else printf 'FAILED\n'; fi
	exit "$failed"
fi

section "W3C QT3 (XPath 2.0)"
if [ -f "$QT3/catalog.xml" ]; then
	# The percentage is the result, so it is printed rather than asserted: a
	# hard threshold would turn every upstream suite update into a build
	# break. What *is* asserted is that a summary appeared at all.
	out=$(GOXSLT_QT3="$QT3" $GO test ./tests/qt3/ -count=1 -run TestQT3 -v 2>&1) || true
	if printf '%s' "$out" | grep -q 'in-scope:'; then
		printf '%s\n' "$out" | grep -E 'QT3:|in-scope:'
		# Ratcheted like every other suite. This was the one Go suite whose
		# passing count could fall without the script noticing -- the comment
		# above explains why the PERCENTAGE is not asserted, which is a
		# different thing from letting the count drop silently.
		#
		# TestQT3 logs one "in-scope:" line per language version, so the
		# figure to ratchet is the LAST of them (the full 2.0 run), not the
		# first. ratchet() takes head -1, so the line is selected here.
		ratchet TestQT3 "$(printf '%s\n' "$out" | grep 'in-scope:' | tail -1)"
		# TestQT3 logs one "in-scope:" line per language version and all of
		# them are recorded: a release record carrying only one version
		# would be silent about the other two the same run measured. The
		# loop runs in THIS shell rather than a pipeline, because `lane`
		# appends to a variable and a `while read` behind a pipe would
		# append it in a subshell and lose every line.
		_qt3lines=$(printf '%s\n' "$out" | grep 'in-scope:')
		_oldifs=$IFS; IFS='
'
		for _ln in $_qt3lines; do
			lane "W3C QT3 XPath" PASS "$_ln"
		done
		IFS=$_oldifs
	else
		fail "QT3 ran but reported no summary — did it skip?"
		printf '%s\n' "$out" | tail -5
		lane "W3C QT3 XPath" FAIL "ran but reported no summary"
	fi
else
	skip "QT3 not at $QT3
    git clone --depth 1 https://github.com/w3c/qt3tests.git $QT3"
	lane "W3C QT3 XPath" SKIP "suite absent at $QT3"
fi

section "W3C QT3 (XQuery 3.1)"
# The XQuery half of the same catalog. It is a separate test from TestQT3 --
# the XPath figures are a regression check that must not move while XQuery is
# worked on -- and it was previously not run here at all, which is why XQuery
# had no row in docs/conformance-gaps.md.
if [ -f "$QT3/catalog.xml" ]; then
	out=$(GOXSLT_QT3="$QT3" $GO test ./tests/qt3/ -count=1 -run TestQT3XQuery -v 2>&1) || true
	if printf '%s' "$out" | grep -q 'in-scope:'; then
		printf '%s\n' "$out" | grep -E 'in-scope:'
		ratchet TestQT3XQuery "$out"
		lane "W3C QT3 XQuery" PASS \
			"$(printf '%s\n' "$out" | grep 'in-scope:' | head -1)"
	else
		fail "the XQuery suite ran but reported no summary — did it skip?"
		printf '%s\n' "$out" | tail -5
		lane "W3C QT3 XQuery" FAIL "ran but reported no summary"
	fi
else
	skip "QT3 not at $QT3 (XQuery)"
	lane "W3C QT3 XQuery" SKIP "suite absent at $QT3"
fi

section "W3C xsdtests (XML Schema 1.0 and 1.1)"
if [ -f "$XSDTS/suite.xml" ]; then
	for flag in "" -11; do
		if [ -z "$flag" ]; then printf -- '--- XSD 1.0\n'; else printf -- '--- XSD 1.1\n'; fi
		out=$($GO run ./tests/xsdsuite "$XSDTS" $flag 2>&1) || true
		if printf '%s' "$out" | grep -q '^TOTAL'; then
			printf '%s\n' "$out" | grep -E '^(SCHEMA|INSTANCE|TOTAL)'
			# The XSD driver reports agreement rather than a passing
			# count, so it needs its own ratchet line; see ratchetXSD.
			if [ -z "$flag" ]; then _n=XSD10; else _n=XSD11; fi
			ratchetXSD "$_n" "$out"
			lane "W3C $_n" PASS \
				"$(printf '%s\n' "$out" | grep '^TOTAL' | head -1)"
		else
			fail "xsdtests produced no totals"
			printf '%s\n' "$out" | tail -5
			if [ -z "$flag" ]; then _n=XSD10; else _n=XSD11; fi
			lane "W3C $_n" FAIL "produced no totals"
		fi
	done
else
	skip "xsdtests not at $XSDTS
    git clone --depth 1 https://github.com/w3c/xsdtests.git $XSDTS"
	lane "W3C XSD 1.0" SKIP "suite absent at $XSDTS"
	lane "W3C XSD 1.1" SKIP "suite absent at $XSDTS"
fi

section "RELAX NG (James Clark's spectest)"
if [ -f "$RNG" ]; then
	out=$(GOXSLT_RNG="$RNG" $GO test ./relaxng/ -count=1 -run TestSpectest -v 2>&1) || true
	if printf '%s' "$out" | grep -q 'spectest:'; then
		printf '%s\n' "$out" | grep -E 'spectest:|failing'
		# The spectest driver logs "N assertions, M passed" rather than the
		# "in-scope: M passed" the Go suites use, so the passing count is
		# extracted here and handed to ratchetCount.
		_rng=$(printf '%s' "$out" | sed -n 's/.*spectest: [0-9]* assertions, \([0-9]*\) passed.*/\1/p' | head -1)
		ratchetCount RelaxNGSpectest "$_rng"
		lane "RELAX NG spectest" PASS \
			"$(printf '%s\n' "$out" | grep 'spectest:' | head -1 | sed 's/^ *//')"
	else
		fail "spectest ran but reported no summary"
		printf '%s\n' "$out" | tail -5
		lane "RELAX NG spectest" FAIL "ran but reported no summary"
	fi
else
	skip "spectest.xml not at $RNG
    git clone --depth 1 https://github.com/relaxng/jing-trang.git
    cp jing-trang/mod/rng-validate/test/spectest.xml $RNG"
	lane "RELAX NG spectest" SKIP "spectest.xml absent at $RNG"
fi

# Both targets, always. The same catalog measures 2.0 and 3.0, and the question
# a change has to answer is not "how much 3.0 works" but "how much 3.0 works
# without costing 2.0" — which only two runs answer.
section "W3C XSLT suite (XSLT 2.0 and 3.0)"
if [ -f "$XSLTS/catalog.xml" ]; then
	for t in TestXSLTSuite TestXSLT30Suite; do
		if [ "$t" = TestXSLTSuite ]; then
			printf -- '--- filtered to XSLT 2.0\n'
		else
			printf -- '--- filtered to XSLT 3.0\n'
		fi
		out=$(GOXSLT_XSLTS="$XSLTS" $GO test ./tests/xslts/ -count=1 -run "$t" -v 2>&1) || true
		if printf '%s' "$out" | grep -q 'in-scope:'; then
			printf '%s\n' "$out" | grep -E 'XSLT suite:|XSLT 3.0 suite:|in-scope:'
			ratchet "$t" "$out"
			if [ "$t" = TestXSLTSuite ]; then _v="2.0"; else _v="3.0"; fi
			lane "W3C XSLT $_v" PASS \
				"$(printf '%s\n' "$out" | grep 'in-scope:' | head -1)"
		else
			fail "the XSLT suite ran but reported no summary — did it skip?"
			printf '%s\n' "$out" | tail -5
			if [ "$t" = TestXSLTSuite ]; then _v="2.0"; else _v="3.0"; fi
			lane "W3C XSLT $_v" FAIL "ran but reported no summary"
		fi
	done
else
	skip "the XSLT suite is not at $XSLTS
    git clone --depth 1 https://github.com/w3c/xslt30-test.git $XSLTS"
	lane "W3C XSLT 2.0" SKIP "suite absent at $XSLTS"
	lane "W3C XSLT 3.0" SKIP "suite absent at $XSLTS"
fi

section "production corpora"
# The only guard against a schema-validity rule stricter than the spec. The
# conformance suite cannot catch that — it scores agreement with W3C labels, so
# an over-strict rule shows up only if the suite happens to contain a valid
# schema exercising it. Real schemas do catch it.
corpus() { # name, mode, dir
	out=$($GO run ./tests/corpora "$2" "$3" 2>&1) || true
	line=$(printf '%s' "$out" | tail -1)
	printf '%-6s %s\n' "$1" "$line"
	case "$line" in
	*"0 failed") lane "$1" PASS "$line" ;;
	*) fail "$1: $line"; lane "$1" FAIL "$line" ;;
	esac
}
if [ -n "$UBL" ] && [ -d "$UBL/maindoc" ]; then
	corpus UBL maindoc "$UBL"
else
	skip "UBL not set (expected in CI) — GOXSLT_UBL=<dir holding maindoc/>"
	lane UBL SKIP "not set; licensed corpus, cannot be cloned in CI (expected)"
fi
if [ -n "$CII" ] && [ -d "$CII" ]; then
	corpus CII walk "$CII"
else
	skip "CII not set (expected in CI) — GOXSLT_CII=<dir of .xsd files>"
	lane CII SKIP "not set; licensed corpus, cannot be cloned in CI (expected)"
fi

section "real-world stylesheets"
# The W3C suites test the language a rule at a time; these test what a large
# stylesheet does with it. Four defects survived both suites and were found
# only here -- xsl:copy over a non-node context item, fn:key with a prefix
# bound to different URIs per module, xsl:evaluate calling the stylesheet's own
# functions, and a base URI spelled as a filesystem path rather than a URI.
#
# The number that may not go down is how many inputs transform without error.
# Upstream can add or remove documents, so only a DROP is a regression.
#
# Only stderr decides the outcome: both stylesheets write progress comments to
# stdout, and a comment is not a failure.
stylesheetCorpus() { # name, stylesheet, glob, extra flags
	_name=$1 _xsl=$2 _glob=$3 _flags=${4:-}
	if [ ! -f "$_xsl" ]; then
		skip "$_name not at $_xsl (expected in CI; it fetches only the W3C suites)"
		lane "$_name" SKIP "stylesheet absent; not fetched in CI (expected)"
		return 0
	fi
	_ok=0 _bad=0
	for _f in $_glob; do
		[ -f "$_f" ] || continue
		if _err=$("$BIN" -timeout 120s -xsl "$_xsl" $_flags -o /dev/null "$_f" \
			2>&1 >/dev/null) && [ -z "$_err" ]; then
			_ok=$((_ok + 1))
		else
			_bad=$((_bad + 1))
		fi
	done
	if [ "$((_ok + _bad))" -eq 0 ]; then
		skip "$_name matched no inputs"
		lane "$_name" SKIP "present but matched no inputs"
		return 0
	fi
	printf '%-8s %s transformed, %s failed\n' "$_name" "$_ok" "$_bad"
	ratchetCount "$_name" "$_ok"
	lane "$_name" PASS "$_ok transformed, $_bad failed"
}

# One build, reused for every input: `go run` per document would dominate the
# runtime of the whole script.
BIN=$(mktemp -t goxml.XXXXXX) || BIN=""
if [ -n "$BIN" ] && $GO build -o "$BIN" ./cmd/go-xml; then
	# DocBook is run with the flags a real user of this corpus would pass, so
	# the count measures the engine rather than a thin invocation. Two of its
	# documents declare a DOCTYPE, and three build a temporary tree from a
	# sequence containing an attribute -- correct to refuse by 5.8.1, which is
	# why the relaxation is opt-in rather than the default. Measuring without
	# these reported 544 where the engine could already do 549.
	#
	# -xinclude is there for the same reason: the corpus assembles documents
	# from parts, so a run without it measures the engine against inputs no
	# reader of this corpus would use. It was worth 28 documents when the
	# flag landed, and leaving it off held the count at 549.
	stylesheetCorpus DocBook \
		"$XSLTNG/src/main/xslt/docbook.xsl" \
		"$XSLTNG/src/test/resources/xml/*.xml" \
		"-allow-dir $XSLTNG -allow-unparsed-text -allow-doctype -xinclude \
		 -compat-drop-attributes-on-document"
	stylesheetCorpus XSpec \
		"$XSPEC/src/compiler/compile-xslt-tests.xsl" \
		"$XSPEC/test/*.xspec" \
		"-allow-dir $XSPEC -allow-unparsed-text"
	rm -f "$BIN"
else
	skip "could not build ./cmd/go-xml for the stylesheet corpora"
	lane DocBook SKIP "could not build ./cmd/go-xml"
	lane XSpec SKIP "could not build ./cmd/go-xml"
fi

emit_record

printf '\n'
if [ -n "$skipped" ]; then
	printf 'Checks skipped (not run, not passed):\n%s' "$skipped"
fi
if [ "$failed" -eq 0 ]; then printf 'OK\n'; else printf 'FAILED\n'; fi
exit "$failed"
