#!/bin/sh
# tests/docfigures.sh -- a conformance figure written in the documentation must
# equal the one the ratchet measured.
#
# tests/ratchet.txt holds the passing count of every suite, re-derived by
# tests/check.sh on each run. The same numbers are copied by hand into
# README.md and docs/*.md -- about thirty copies of eight figures -- and
# nothing failed when a copy went stale. It happened repeatedly: three files
# carried three different unit-test counts on the same day, and an XSD split
# survived two re-measurements in one file while being current in another.
#
# This script does not anchor on line numbers, which move. It anchors on the
# one number in each figure that does NOT move between runs: the in-scope
# denominator (11,518 for XSLT 3.0, 30,346 for XQuery, ...). Every line in the
# documentation that names a denominator is examined, and the passing count,
# failure count and percentage written beside it must agree with the ratchet.
# The forms recognised are the ones the documents actually use:
#
#     11,481 of 11,518          37 of 11,518          (either count is true)
#     11,481 / 11,518 (99.68%)  11,481 / 11,518 = 99.68%
#     99.68% ... (11,481 of 11,518 in scope) (37 failing)
#     | 11,518 | 11,481 | 99.68% | **37** |            (the summary table)
#
# A denominator changes only when the suite checkout or the scoping changes;
# when it does, change it in the table below deliberately, in the same commit
# as the documents. Prose that states a count without its denominator ("the 37
# failures") is not guarded here -- it is merely asserted, as it always was.
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
RATCHET="$ROOT/tests/ratchet.txt"

# ratchet mark | in-scope denominator | label
TABLE='
TestQT3XQuery   30346 XQuery-3.1
TestXSLT30Suite 11518 XSLT-3.0
TestXSLTSuite   6201  XSLT-2.0
XSD10           39388 XSD-1.0
XSD11           41576 XSD-1.1
XSD10S          14388 XSD-1.0-schema
XSD10I          25000 XSD-1.0-instance
XSD11S          15354 XSD-1.1-schema
XSD11I          26222 XSD-1.1-instance
'

commas() { printf '%s' "$1" | awk '{ s=$0; while (s ~ /[0-9][0-9][0-9][0-9]/) sub(/[0-9][0-9][0-9]$|[0-9][0-9][0-9],/, ",&", s); print s }' | sed 's/,,/,/g'; }

ALLDEN=$(printf '%s\n' "$TABLE" | awk 'NF { print $2 }' | while read -r d; do commas "$d"; done | tr '\n' ' ')

bad=0
printf '%s\n' "$TABLE" | awk 'NF' | while read -r key denom label; do
	pass=$(sed -n "s/^$key \([0-9]*\)$/\1/p" "$RATCHET" | head -1)
	if [ -z "$pass" ]; then
		printf '  tests/ratchet.txt has no mark for %s (%s); run tests/check.sh to record one\n' "$key" "$label"
		exit 1
	fi
	fail=$((denom - pass))
	pct=$(awk -v p="$pass" -v d="$denom" 'BEGIN { printf "%.2f", p * 100 / d }')
	passc=$(commas "$pass"); denomc=$(commas "$denom")
	grep -n -- "$denomc" "$ROOT/README.md" "$ROOT"/docs/*.md 2>/dev/null |
	awk -F: -v denom="$denomc" -v pass="$passc" -v fail="$fail" -v pct="$pct" -v alld="$ALLDEN" -v label="$label" -v root="$ROOT/" '
	BEGIN { nd = split(alld, D, " ") }
	{
		file = $1; sub(root, "", file)
		ln = $2
		line = $0; sub(/^[^:]*:[0-9]*:/, "", line)
		rest = line; pre = ""
		while ((i = index(rest, denom)) > 0) {
			before = pre substr(rest, 1, i - 1)
			after = substr(rest, i + length(denom))
			# The segment is the text since the last OTHER denominator on the
			# line, so a line carrying two figures is read as two claims.
			seg = before
			for (k = 1; k <= nd; k++) {
				if (D[k] == denom) continue
				s = seg; cut = 0; off = 0
				while ((p = index(s, D[k])) > 0) { off += p + length(D[k]) - 1; cut = off; s = substr(s, p + length(D[k])) }
				if (cut > 0) seg = substr(seg, cut + 1)
			}
			msg = ""
			# A percentage written AFTER the denominator ("/ 11,518 (99.68%)",
			# "/ 11,518 = 99.68%") belongs to this claim; only when there is
			# none is the nearest one BEFORE the numerator read as its own.
			afterpct = (after ~ /^ \([0-9]+\.[0-9][0-9]%\)/ || after ~ /^ = [0-9]+\.[0-9][0-9]%/)
			if (seg ~ /[0-9][0-9,]* (of|\/) $/) {
				x = seg; sub(/ (of|\/) $/, "", x); sub(/.*[^0-9,]/, "", x)
				if (x != pass && x != fail) msg = msg " numerator " x " (want " pass " passing or " fail " failing)"
				if (!afterpct && match(seg, /[0-9]+\.[0-9][0-9]%[^%]*$/)) {
					p = substr(seg, RSTART); sub(/%.*/, "", p)
					if (p != pct) msg = msg " percentage " p "% (want " pct "%)"
				}
			}
			if (match(after, /^ \([0-9]+\.[0-9][0-9]%\)/) || match(after, /^ = [0-9]+\.[0-9][0-9]%/)) {
				p = substr(after, RSTART, RLENGTH); sub(/^[ (=]*/, "", p); sub(/%.*/, "", p)
				if (p != pct) msg = msg " percentage " p "% (want " pct "%)"
			}
			if (match(after, /^ \| [0-9][0-9,]* \| [0-9]+\.[0-9][0-9]% \| \*\*[0-9]+\*\*/)) {
				t = substr(after, RSTART, RLENGTH); n = split(t, c, / \| /)
				x = c[2]; p = c[3]; sub(/%/, "", p); f = c[4]; gsub(/\*/, "", f)
				if (x != pass) msg = msg " passing " x " (want " pass ")"
				if (p != pct) msg = msg " percentage " p "% (want " pct "%)"
				if (f != fail) msg = msg " failing " f " (want " fail ")"
			}
			if (match(after, /^[^|]*\([0-9]+ failing\)/)) {
				t = substr(after, RSTART, RLENGTH); sub(/.*\(/, "", t); sub(/ failing\)/, "", t)
				if (t != fail) msg = msg " failing " t " (want " fail ")"
			}
			if (msg != "") { printf "  %s:%s  %s:%s\n", file, ln, label, msg; bad = 1 }
			pre = before denom; rest = after
		}
	}
	END { exit bad ? 1 : 0 }' || bad=1
	[ "$bad" = 0 ] || exit 1
done

# The Total row is the one figure above that no suite run produces on its own:
# it is the sum of the others, and it was the one that went wrong. The document
# once printed 168 while its own rows summed to 104, and every loop above
# passed, because each individual row agreed with the ratchet. A per-row check
# cannot see a bad sum.
#
# So the total is derived here too, from tests/ratchet.txt via the denominators
# in TABLE, and compared with the total the generated region prints. The
# generator (tests/conformance-docs.go) derives its total from
# tests/conformance/results.json; this derives the same number from the
# ratchet. They come from two different files by two different routes, which is
# the point: agreement between them means results.json was re-measured, not
# merely re-typed.
#
# XPath and RELAX NG are not in TABLE -- they have no denominator that has ever
# drifted, and they contribute zero -- so this sums the suites that can move:
# XQuery, XSLT 2.0, XSLT 3.0, XSD 1.0 and XSD 1.1. The XSD schema/instance
# split rows are components of XSD10/XSD11 and must not be added twice.
want_total=0
for key_denom in 'TestQT3XQuery 30346' 'TestXSLT30Suite 11518' 'TestXSLTSuite 6201' 'XSD10 39388' 'XSD11 41576'; do
	key=${key_denom% *}
	denom=${key_denom#* }
	pass=$(sed -n "s/^$key \([0-9]*\)$/\1/p" "$RATCHET" | head -1)
	want_total=$((want_total + denom - pass))
done

GAPS="$ROOT/docs/conformance-gaps.md"
got_total=$(sed -n 's/^| | \*\*Total\*\* | | | | \*\*\([0-9]*\)\*\* |$/\1/p' "$GAPS" | head -1)
if [ -z "$got_total" ]; then
	printf '  docs/conformance-gaps.md has no generated Total row; run: go run tests/conformance-docs.go\n'
	exit 1
fi
if [ "$got_total" != "$want_total" ]; then
	printf '  docs/conformance-gaps.md:  Total %s, but tests/ratchet.txt sums to %s.\n' "$got_total" "$want_total"
	printf '    The Total is generated from tests/conformance/results.json. Bring that\n'
	printf '    file up to date with the measured run and regenerate:\n'
	printf '      go run tests/conformance-docs.go\n'
	exit 1
fi

# The same total also appears in prose that carries no denominator -- "those
# 104 cases" in docs/known-gaps.md -- which the loop above cannot see, because
# it has nothing to anchor on. The forms are narrow enough to match directly:
# "those N cases", "N disagreements in all", and "The N is the sum". A figure
# written any of those ways is a copy of the generated total and must equal it.
#
# "The N is the sum" was added when the figures moved into generated regions.
# Most published copies of the total now live inside a marked region and are
# rewritten by tests/conformance-docs.go, but that sentence in
# docs/known-gaps.md is an ARGUMENT about where the number comes from, not a
# figure: generating it would leave the surrounding paragraph reasoning about a
# number a script had silently changed. So it stays prose and is checked here
# instead, which fails at the moment someone should be re-reading the sentence.
#
# Deliberately NOT matched: the historical narrative. docs/conformance-gaps.md
# and docs/testing.md both recount that this document once printed 168 while
# its own rows summed to 104. That 104 is a fact about the past and must not
# follow the current total -- if the suites move, the story of the 168/104 bug
# is still the story of 168 and 104. The patterns above are worded to match the
# live claims and miss the narrative ones, which is why they are three narrow
# forms rather than "any number near the word total".
grep -n -E 'those [0-9,]+ cases|[0-9,]+ disagreements in all|The [0-9,]+ is the sum' \
	"$ROOT/README.md" "$ROOT"/docs/*.md 2>/dev/null |
	awk -F: -v want="$want_total" -v root="$ROOT/" '
	{
		file = $1; sub(root, "", file); ln = $2
		line = $0; sub(/^[^:]*:[0-9]*:/, "", line)
		while (match(line, /those [0-9,]+ cases|[0-9,]+ disagreements in all|The [0-9,]+ is the sum/)) {
			t = substr(line, RSTART, RLENGTH); line = substr(line, RSTART + RLENGTH)
			n = t; gsub(/[^0-9]/, "", n)
			if (n != want) {
				printf "  %s:%s  free-form total %s (want %s, the sum of the suite rows)\n", file, ln, n, want
				bad = 1
			}
		}
	}
	END { exit bad ? 1 : 0 }' || exit 1
