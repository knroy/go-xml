#!/bin/sh
# speccites.sh — every §N.N citation in xslt/stream*.go must name a real
# section of the XSLT 3.0 Recommendation.
#
# These files were originally written against the Last Call Working Draft, and
# the Recommendation renumbered parts of section 19 and section 18.2. That left
# citations pointing at real sections about the wrong subject -- §19.8.8.11
# ("Dynamic Function Calls") on code implementing variable references, §18.2.8
# ("Importing of Accumulators") on their streamability -- which is worse than a
# dangling reference, because it reads as authority and cannot be spotted by
# following the link.
#
# A comment cannot fail, so this script is what holds the citations to the
# spec. It catches the cheap half automatically: a number that names no section
# at all. The other half -- a real number about the wrong subject -- still
# needs a person, but the header of each fixed site now records the old number,
# so a reader who follows one finds the history rather than a silent edit.
#
# Exits non-zero, listing each offender, if any citation dangles.
set -e
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SPEC="$ROOT/testdata/xslt30-test/specs/xslt-30.html"

if [ ! -f "$SPEC" ]; then
	echo "speccites: $SPEC is absent; the suite is not vendored here, skipping" >&2
	exit 0
fi

python3 - "$ROOT" "$SPEC" <<'PYEOF'
import glob, html, os, re, sys

root, spec = sys.argv[1], sys.argv[2]
text = open(spec, encoding="utf-8", errors="ignore").read()

# Real headings only: "<hN ...>19.8.8.12 Streamability of ...</hN>". The table
# of contents repeats them as links, which is harmless -- this is a set.
sections = set()
for m in re.finditer(r"<h[1-6][^>]*>(.*?)</h[1-6]>", text, re.S):
    t = re.sub(r"\s+", " ", html.unescape(re.sub("<[^>]+>", "", m.group(1)))).strip()
    n = re.match(r"^([A-Z]?\.?\d+(?:\.\d+)*)\s+\S", t)
    if n:
        sections.add(n.group(1))
if len(sections) < 200:
    sys.exit("speccites: only %d headings parsed from the spec; the format "
             "changed and this check would pass vacuously" % len(sections))

bad, sites = [], 0
for path in sorted(glob.glob(os.path.join(root, "xslt", "stream*.go"))):
    rel = os.path.relpath(path, root)
    for i, line in enumerate(open(path, encoding="utf-8"), 1):
        for m in re.finditer(r"§(\d+(?:\.\d+)*)", line):
            sites += 1
            num = m.group(1).rstrip(".")
            if num not in sections:
                bad.append((rel, i, num, line.strip()[:78]))

for rel, i, num, src in bad:
    print("%s:%d: §%s names no section of XSLT 3.0\n    %s" % (rel, i, num, src))
if bad:
    sys.exit("speccites: %d of %d citations dangle" % (len(bad), sites))
print("spec citations: %d sites in xslt/stream*.go all name real sections" % sites)
PYEOF
