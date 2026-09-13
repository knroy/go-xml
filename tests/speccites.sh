#!/bin/sh
# speccites.sh — every §N.N citation in xslt/stream*.go and xpath/*.go must
# name a real section of one of the vendored specifications, and where the
# subject can be read off the code, it must name the RIGHT one.
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
# spec. It runs two checks, and it is worth being precise about the reach of
# each, because the first one passed green on 978 sites while sixteen of them
# named the wrong subject:
#
#   1. DANGLING (complete, all cited files). A number that names no section of
#      any vendored spec. Cheap and exhaustive.
#
#   2. WRONG SUBJECT (partial, xslt/stream*.go only). The XSLT 3.0 headings in
#      the 19.8.4 table are "Streamability of xsl:NAME", and the code that
#      implements each instruction sits under a `case "NAME":` in a switch. So
#      where a citation appears under such a case, the cited section's heading
#      must name that same instruction. This is what catches the LCWD-to-REC
#      renumbering, which shifted every entry after xsl:text by two.
#
# What check 2 does NOT catch, and a reader should not assume it does:
#
#   - Citations outside a `case` block -- helper functions, file headers, the
#     19.8.8 expression table -- have no machine-readable subject, so they are
#     not checked at all. Roughly half the sites in xslt/stream*.go.
#   - Every citation in xpath/, which is prose about functions and types rather
#     than a switch over instruction names. These get the dangling check only.
#   - Any spec other than XSLT 3.0. F&O, XPath and Serialization citations are
#     checked for existence, not for subject.
#
# The 491 citations in xsd/ and xdm/ are not checked at all: the XML 1.0 and
# XSD prose specifications are not vendored in this repository, so there is
# nothing local to check them against. Vendoring those would close the gap.
#
# Exits non-zero, listing each offender, if any citation dangles or names a
# section about a demonstrably different instruction.
set -e
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SPECS="$ROOT/testdata/xslt30-test/specs"

if [ ! -f "$SPECS/xslt-30.html" ]; then
	echo "speccites: $SPECS/xslt-30.html is absent; the suite is not vendored here, skipping" >&2
	exit 0
fi

python3 - "$ROOT" "$SPECS" <<'PYEOF'
import glob, html, os, re, sys

root, specs = sys.argv[1], sys.argv[2]

def headings(path):
    """number -> heading text, for every <hN> that starts with a section number."""
    out = {}
    if not os.path.exists(path):
        return out
    text = open(path, encoding="utf-8", errors="ignore").read()
    for m in re.finditer(r"<h[1-6][^>]*>(.*?)</h[1-6]>", text, re.S):
        t = re.sub(r"\s+", " ", html.unescape(re.sub("<[^>]+>", "", m.group(1)))).strip()
        n = re.match(r"^([A-Z]?\.?\d+(?:\.\d+)*)\s+(\S.*)$", t)
        if n:
            out.setdefault(n.group(1), n.group(2))
    return out

xslt = headings(os.path.join(specs, "xslt-30.html"))
others = {
    "F&O 3.1":           headings(os.path.join(specs, "functions-and-operators-31.html")),
    "XPath 3.1":         headings(os.path.join(specs, "xpath-31.html")),
    "Serialization 3.1": headings(os.path.join(specs, "serialization-31.html")),
}

if len(xslt) < 200:
    sys.exit("speccites: only %d headings parsed from xslt-30.html; the format "
             "changed and this check would pass vacuously" % len(xslt))

known = set(xslt)
for t in others.values():
    known |= set(t)

# instruction name -> section, read off the "Streamability of xsl:NAME" headings.
instr = {}
for n, h in xslt.items():
    m = re.match(r"Streamability of (xsl:[\w-]+)$", h)
    if m:
        instr[m.group(1)] = n
if len(instr) < 30:
    sys.exit("speccites: only %d 'Streamability of xsl:*' headings found; the "
             "subject check would pass vacuously" % len(instr))

paths = sorted(glob.glob(os.path.join(root, "xslt", "stream*.go"))) + \
        sorted(glob.glob(os.path.join(root, "xpath", "*.go")))

dangling, wrong, sites, checked = [], [], 0, 0
for path in paths:
    rel = os.path.relpath(path, root)
    subject_check = rel.startswith("xslt" + os.sep)
    lines = open(path, encoding="utf-8").readlines()
    case = None
    for i, line in enumerate(lines, 1):
        cm = re.match(r'\s*case ((?:"[\w-]+",?\s*)+):', line)
        if cm:
            case = re.findall(r'"([\w-]+)"', cm.group(1))
        elif re.match(r"^(func|})", line):
            case = None          # left the switch; no governing subject
        for m in re.finditer(r"§(\d+(?:\.\d+)*)", line):
            sites += 1
            num = m.group(1).rstrip(".")
            if num not in known:
                dangling.append((rel, i, num, line.strip()[:78]))
                continue
            # Only the 19.8.4 streamability table is subject-checkable: its
            # headings are one-to-one with instruction names. A citation of
            # some other section (§18.1, "The xsl:source-document Instruction",
            # for the instruction's own semantics) is a different claim about
            # the same instruction and is none of this check's business.
            if not (subject_check and case and num.startswith("19.8.4.")):
                continue
            want = {instr["xsl:" + c] for c in case if "xsl:" + c in instr}
            if not want:
                continue
            checked += 1
            if num not in want:
                names = ", ".join("xsl:" + c for c in case)
                wrong.append((rel, i, num, xslt[num], names, sorted(want)[0],
                              line.strip()[:78]))

for rel, i, num, src in dangling:
    print("%s:%d: §%s names no section of any vendored spec\n    %s" % (rel, i, num, src))
for rel, i, num, head, names, want, src in wrong:
    print("%s:%d: §%s is \"%s\", but this code implements %s (§%s)\n    %s"
          % (rel, i, num, head, names, want, src))

if dangling or wrong:
    sys.exit("speccites: %d dangling, %d wrong-subject, of %d citations"
             % (len(dangling), len(wrong), sites))
print("spec citations: %d sites in xslt/stream*.go and xpath/*.go name real "
      "sections; %d of them also checked for subject" % (sites, checked))
PYEOF
