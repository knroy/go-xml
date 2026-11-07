#!/usr/bin/env bash
# Downloads the W3C XML Conformance Test Suite (xmlconf) listed in
# tests/xmlconf-corpus.txt into testdata/xmlconf/ and unpacks it there, for
# TestXMLConf (tests/xmlconf).
#
# It is third-party material, so like the other W3C suites it lives under the
# untracked testdata/ rather than in the repository. It follows
# tests/fetch-c14n.sh: the archive is checked against the digest the manifest
# pins, and one that is already present and matches is neither fetched nor
# unpacked again, so running this on every check costs one hash.
#
# Usage: tests/fetch-xmlconf.sh [dest]   (dest defaults to testdata/xmlconf)
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
dest=${1:-$root/testdata/xmlconf}
manifest=$root/tests/xmlconf-corpus.txt

if command -v sha256sum >/dev/null 2>&1; then
	sha() { sha256sum "$1" | cut -d' ' -f1; }
else
	sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
fi

fetched=0 present=0
while read -r want path url; do
	case $want in '' | '#'*) continue ;; esac
	f=$dest/$path
	# The stamp names the digest that was unpacked, so a partial unpack (the
	# archive verified, tar interrupted) is redone rather than trusted.
	stamp=$dest/.unpacked
	if [ -f "$f" ] && [ "$(sha "$f")" = "$want" ] &&
		[ "$(cat "$stamp" 2>/dev/null)" = "$want" ]; then
		present=$((present + 1))
		continue
	fi
	mkdir -p "$dest"
	if ! [ -f "$f" ] || [ "$(sha "$f")" != "$want" ]; then
		curl -fsSL --retry 3 -o "$f.part" "$url"
		got=$(sha "$f.part")
		if [ "$got" != "$want" ]; then
			rm -f "$f.part"
			echo "fetch-xmlconf: $url: sha256 $got, manifest pins $want" >&2
			exit 1
		fi
		mv "$f.part" "$f"
		fetched=$((fetched + 1))
	fi
	# The archive's entries all sit under xmlconf/; strip it, so that the
	# catalog is testdata/xmlconf/xmlconf.xml.
	tar -xzf "$f" -C "$dest" --strip-components=1
	printf '%s\n' "$want" > "$stamp"
done < "$manifest"
echo "fetch-xmlconf: $fetched fetched, $present already present, in $dest"
