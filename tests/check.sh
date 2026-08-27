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
#   GOXSLT_UBL=<dir>    UBL 2.1, the directory holding maindoc/
#   GOXSLT_CII=<dir>    UN/CEFACT CII or EN 16931 schemas
#
# A missing suite is reported and skipped; a suite that is present but produces
# no result is a failure. A check that did not run must never look like a check
# that succeeded — which is exactly what happened the first time this script
# ran: a relative GOXSLT_QT3 resolved against ./qt3/ rather than the repository
# root, the test skipped itself, and `go test` reported PASS.

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

QT3=$(abspath "${GOXSLT_QT3:-testdata/qt3tests}")
XSDTS=$(abspath "${GOXSLT_XSDTS:-testdata/xsdtests}")
RNG=$(abspath "${GOXSLT_RNG:-testdata/relaxng/spectest.xml}")
XSLTS=$(abspath "${GOXSLT_XSLTS:-testdata/xslt30-test}")
UBL="${GOXSLT_UBL:-}"
CII="${GOXSLT_CII:-}"
[ -n "$UBL" ] && UBL=$(abspath "$UBL")
[ -n "$CII" ] && CII=$(abspath "$CII")

failed=0
skipped=""

section() { printf '\n=== %s\n' "$1"; }
fail()    { printf 'FAIL: %s\n' "$1"; failed=1; }
skip()    { skipped="${skipped}  - $1
"; }

section "build"
$GO build ./... || fail "build"

section "vet"
$GO vet ./... || fail "vet"

section "unit tests"
$GO test ./... || fail "unit tests"

section "race"
$GO test -race ./... || fail "race"

if [ "$MODE" = fast ]; then
	printf '\n=== fast mode: external suites not run\n'
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
	else
		fail "QT3 ran but reported no summary — did it skip?"
		printf '%s\n' "$out" | tail -5
	fi
else
	skip "QT3 not at $QT3
    git clone --depth 1 https://github.com/w3c/qt3tests.git $QT3"
fi

section "W3C xsdtests (XML Schema 1.0 and 1.1)"
if [ -f "$XSDTS/suite.xml" ]; then
	for flag in "" -11; do
		if [ -z "$flag" ]; then printf -- '--- XSD 1.0\n'; else printf -- '--- XSD 1.1\n'; fi
		out=$($GO run ./tests/xsdsuite "$XSDTS" $flag 2>&1) || true
		if printf '%s' "$out" | grep -q '^TOTAL'; then
			printf '%s\n' "$out" | grep -E '^(SCHEMA|INSTANCE|TOTAL)'
		else
			fail "xsdtests produced no totals"
			printf '%s\n' "$out" | tail -5
		fi
	done
else
	skip "xsdtests not at $XSDTS
    git clone --depth 1 https://github.com/w3c/xsdtests.git $XSDTS"
fi

section "RELAX NG (James Clark's spectest)"
if [ -f "$RNG" ]; then
	out=$(GOXSLT_RNG="$RNG" $GO test ./relaxng/ -count=1 -run TestSpectest -v 2>&1) || true
	if printf '%s' "$out" | grep -q 'spectest:'; then
		printf '%s\n' "$out" | grep -E 'spectest:|failing'
	else
		fail "spectest ran but reported no summary"
		printf '%s\n' "$out" | tail -5
	fi
else
	skip "spectest.xml not at $RNG
    git clone --depth 1 https://github.com/relaxng/jing-trang.git
    cp jing-trang/mod/rng-validate/test/spectest.xml $RNG"
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
		else
			fail "the XSLT suite ran but reported no summary — did it skip?"
			printf '%s\n' "$out" | tail -5
		fi
	done
else
	skip "the XSLT suite is not at $XSLTS
    git clone --depth 1 https://github.com/w3c/xslt30-test.git $XSLTS"
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
	*"0 failed") ;;
	*) fail "$1: $line" ;;
	esac
}
if [ -n "$UBL" ] && [ -d "$UBL/maindoc" ]; then
	corpus UBL maindoc "$UBL"
else
	skip "UBL not set — GOXSLT_UBL=<dir holding maindoc/>"
fi
if [ -n "$CII" ] && [ -d "$CII" ]; then
	corpus CII walk "$CII"
else
	skip "CII not set — GOXSLT_CII=<dir of .xsd files>"
fi

printf '\n'
if [ -n "$skipped" ]; then
	printf 'Checks skipped (not run, not passed):\n%s' "$skipped"
fi
if [ "$failed" -eq 0 ]; then printf 'OK\n'; else printf 'FAILED\n'; fi
exit "$failed"
