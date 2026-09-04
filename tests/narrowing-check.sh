#!/bin/sh
# narrowing-check.sh — an advisory grep for the three ways an exact XDM value
# gets silently narrowed. See the "Never narrow an exact XDM value" section of
# CONTRIBUTING.md for why each form is wrong and what to use instead.
#
# THIS SCRIPT ALWAYS EXITS 0. It is a reading list for review, not a gate.
# The patterns cannot tell a proven narrowing from an unproven one in every
# case, so a clean run means nothing and a noisy one is expected; the value is
# in having the candidate sites in one place when a function that takes a
# position, an arity, a precision or a codepoint is being touched.
#
#   tests/narrowing-check.sh          # every category
#   tests/narrowing-check.sh int64    # one category: int64 | float | index
#
# _test.go files and testdata/ are excluded: a test may narrow deliberately in
# order to construct the very value the engine must not narrow.

set -u

cd "$(dirname "$0")/.."

WANT="${1:-all}"

# Go sources only, no tests, no third-party corpora.
sources() {
	find . -name '*.go' ! -name '*_test.go' \
		! -path './testdata/*' ! -path './.git/*' \
		! -path './.claude/*' | sort
}

hits=0

# report prints one WARNING per hit and counts it. It reads file:line:text
# triples on stdin.
report() {
	_n=0
	while IFS= read -r _line; do
		[ -n "$_line" ] || continue
		_where=${_line%%:*}
		_rest=${_line#*:}
		_no=${_rest%%:*}
		_text=${_rest#*:}
		# Trim leading tabs and spaces from the source line.
		_text=$(printf '%s' "$_text" | sed 's/^[ 	]*//')
		printf 'WARNING %s:%s\n        %s\n' "$_where" "$_no" "$_text"
		_n=$((_n + 1))
	done
	printf '%s\n' "$_n" > "$COUNTFILE"
}

COUNTFILE=$(mktemp -t narrowing.XXXXXX)
trap 'rm -f "$COUNTFILE"' EXIT

section() {
	printf '\n=== %s\n' "$1"
}

count_of() {
	_c=$(cat "$COUNTFILE" 2>/dev/null || printf 0)
	printf '%s' "$_c"
}

int64_hits=0
float_hits=0
index_hits=0

# ---------------------------------------------------------------------------
# 1. Unproven .Int64()
#
# A .Int64() is called proven when the same line, or any of the four lines
# before it, mentions a range test: FitsInt64, Cmp, IsInt, BitLen, Sign, or a
# Max/Min bound. That window is why clampPosition does not appear below --
# its FitsInt64 guard is the line above the Int64 call. Four lines rather than
# two because the two sanctioned narrowings both put the guard in a multi-line
# `if` whose closing brace and error return push the conversion further away:
# at two lines both integerPosition and the range operator's item-count check
# were reported, which is exactly the wrong signal to send.
if [ "$WANT" = all ] || [ "$WANT" = int64 ]; then
	section "unproven .Int64() — no FitsInt64/Cmp/IsInt/bounds check within 4 lines"
	sources | while IFS= read -r f; do
		grep -n '\.Int64()' "$f" |
			grep -vE '^[0-9]+:[ 	]*(//|\*)' |
			while IFS= read -r m; do
				n=${m%%:*}
				start=$((n - 4))
				[ "$start" -lt 1 ] && start=1
				win=$(sed -n "${start},${n}p" "$f")
				case "$win" in
				*FitsInt64* | *Cmp* | *IsInt* | *BitLen* | *"Sign()"* | \
					*MaxInt* | *MinInt* | *MaxUint* | *IsInt64*) ;;
				*) printf '%s:%s\n' "$f" "$m" ;;
				esac
			done
	done | report
	int64_hits=$(count_of)
fi

# ---------------------------------------------------------------------------
# 2. XDM -> float64 -> int
#
# int(x.Float64()) and its int32/int64/uint relatives, plus the two-step
# spelling where a Float64 result is converted on a later line -- that second
# form is not caught, and is the main thing a reviewer still has to look for
# by eye.
if [ "$WANT" = all ] || [ "$WANT" = float ]; then
	section "XDM -> float64 -> int — an exact value routed through 53 bits of mantissa"
	sources | while IFS= read -r f; do
		grep -nE '\b(u?int(8|16|32|64)?)\([^()]*\.Float64\(\)' "$f" |
			grep -vE '^[0-9]+:[ 	]*(//|\*)' |
			sed "s|^|$f:|"
	done | report
	float_hits=$(count_of)
fi

# ---------------------------------------------------------------------------
# 3. sequence[0] for a singleton parameter
#
# args[N][0] and seq[0] / items[0] style indexing. The identifier list is
# deliberately short: a bare s[0] or val[0] is far more often a string or a
# byte slice than an xdm.Sequence, and including them buried the real hits.
# A cardinality check is assumed present when len( appears on the line or the
# five before it. Comment lines are dropped.
#
# This category is the noisy one, and no window makes it clean: the guard is
# often a `len(args[0]) == 0` early return several lines up, or lives in a
# helper (argArray, argQName) that the indexing line cannot see. Read it as
# "these functions index a sequence directly" rather than as a defect list.
if [ "$WANT" = all ] || [ "$WANT" = index ]; then
	section "sequence[0] — a singleton parameter taken without a cardinality check"
	sources | while IFS= read -r f; do
		grep -nE '\b(args\[[0-9A-Za-z_]+\]|seq|sq|items|seqs)\[0\]' "$f" |
			grep -vE '^[0-9]+:[ 	]*(//|\*)' |
			while IFS= read -r m; do
				n=${m%%:*}
				start=$((n - 5))
				[ "$start" -lt 1 ] && start=1
				win=$(sed -n "${start},${n}p" "$f")
				case "$win" in
				*"len("* | *Cardinality* | *singleton*) ;;
				*) printf '%s:%s\n' "$f" "$m" ;;
				esac
			done
	done | report
	index_hits=$(count_of)
fi

total=$((int64_hits + float_hits + index_hits))

printf '\n'
printf 'unproven .Int64()          %4s\n' "$int64_hits"
printf 'XDM -> float64 -> int      %4s\n' "$float_hits"
printf 'sequence[0], unchecked     %4s\n' "$index_hits"
printf 'total                      %4s\n' "$total"
printf '\nAdvisory only — this script always exits 0. Each hit is a site to read,\n'
printf 'not a defect. See CONTRIBUTING.md for the three sanctioned patterns.\n'

exit 0
