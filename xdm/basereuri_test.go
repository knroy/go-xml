package xdm

import (
	"strings"
	"testing"
)

// resolveBase is where an xml:base value meets the base already in force, and
// it is the one piece of base-URI handling that no test on darwin or Linux
// exercised with the input that breaks it. That input is a WINDOWS base URI.
//
// Why a darwin run can prove a Windows behaviour here at all: resolveBase
// takes two strings and returns a string. It calls nothing platform-dependent
// — no filepath, no os, no Abs, no ToSlash — so the function's behaviour is a
// function of its arguments alone and is byte-for-byte identical on every
// GOOS. What differs between platforms is only which arguments REACH it, and
// those are written out literally below. A Windows CI run and this darwin run
// execute the same code over the same bytes.
//
// The bytes that mattered are in the first two rows: Windows CI reported a
// base URI of file://C:\Users\RUNNER~1\...\level1\element.xml, which
// url.Parse rejects outright because a backslash after the host reads as a
// port. The old code answered that by returning the bare reference, so the
// element's base URI became "deeper/" — a relative reference naming the
// process's working directory rather than the document's location — with no
// error anywhere. That verbatim string is the third Windows CI failure.
func TestResolveBaseKeepsAnUnparseableBase(t *testing.T) {
	// The spelling that reached Windows CI, and that url.Parse refuses.
	const winBad = `file://C:\Users\RUNNER~1\AppData\Local\Temp\001\level1\element.xml`
	// The spelling internal/fileuri produces for the same path, which parses.
	const winGood = `file:///C:/Users/RUNNER~1/AppData/Local/Temp/001/level1/element.xml`

	tests := []struct {
		name string
		base string
		ref  string
		want string
	}{{
		// The correct Windows spelling resolves by the ordinary RFC 3986
		// rules. This is the row that says the fix did not disturb the path
		// that already worked.
		name: "well-formed windows base resolves normally",
		base: winGood,
		ref:  "deeper/",
		want: `file:///C:/Users/RUNNER~1/AppData/Local/Temp/001/level1/deeper/`,
	}, {
		// The regression. Before the fix this returned "deeper/" — the base
		// discarded entirely. The base is unusable as a URI, but it still has
		// path structure, and the answer must stay underneath what it named.
		name: "unparseable windows base is merged, not discarded",
		base: winBad,
		ref:  "deeper/",
		want: `file://C:\Users\RUNNER~1\AppData\Local\Temp\001\level1\deeper/`,
	}, {
		// The two-slash form parses, but with "C:" as the authority. It is
		// still wrong, and it is still not this function's job to say so —
		// what matters is that the drive is not silently dropped.
		name: "two-slash windows base keeps its authority",
		base: `file://C:/Users/RUNNER~1/level1/element.xml`,
		ref:  "deeper/",
		want: `file://C:/Users/RUNNER~1/level1/deeper/`,
	}, {
		// A relative base is legitimate: a document parsed with no BaseURI
		// whose root carries xml:base="sub/" has one. RFC 3986 section 5.2.3
		// merges against it on path structure alone, and the old code threw
		// it away here too.
		name: "relative base merges rather than vanishing",
		base: "sub/",
		ref:  "deeper/",
		want: "sub/deeper/",
	}, {
		name: "relative base with a dot segment",
		base: "a/b/c.xml",
		ref:  "../d/",
		want: "a/d/",
	}, {
		// Section 5.2.2: a path-absolute reference replaces the base's path.
		name: "rooted reference replaces a relative base",
		base: "sub/doc.xml",
		ref:  "/abs/x",
		want: "/abs/x",
	}, {
		// Section 5.2.3 merges a single-segment base against the empty path.
		name: "base with no slash",
		base: "doc.xml",
		ref:  "sub/",
		want: "sub/",
	}, {
		name: "absolute reference replaces any base",
		base: winBad,
		ref:  "http://example.com/x/",
		want: "http://example.com/x/",
	}, {
		name: "empty base leaves the reference alone",
		base: "",
		ref:  "sub/",
		want: "sub/",
	}, {
		name: "empty reference leaves the base alone",
		base: winGood,
		ref:  "",
		want: winGood,
	}, {
		name: "posix base still resolves as it always did",
		base: "file:///var/tmp/001/level1/element.xml",
		ref:  "deeper/",
		want: "file:///var/tmp/001/level1/deeper/",
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveBase(tc.base, tc.ref); got != tc.want {
				t.Errorf("resolveBase(%q, %q) = %q, want %q",
					tc.base, tc.ref, got, tc.want)
			}
		})
	}
}

// The property that the table rows above are individually checking, stated
// once: a non-empty base is never simply dropped. This is what makes the
// failure mode impossible rather than merely absent from the table — a future
// base spelling nobody thought of still cannot relocate the document.
//
// It is asserted as a property because the damage is not local. xpath's
// inheritedBaseURI walks up until it finds a non-empty BaseURI, so an element
// whose base has been replaced by a bare relative reference is a stopping
// point: the walk never reaches the ancestor whose base IS usable, and every
// descendant resolves against the working directory instead.
func TestResolveBaseNeverDiscardsTheBase(t *testing.T) {
	bases := []string{
		`file://C:\dir\doc.xml`,
		`file:///C:/dir/doc.xml`,
		`file://C:/dir/doc.xml`,
		"file:///var/tmp/doc.xml",
		"sub/dir/doc.xml",
		"http://example.com/a/b",
	}
	refs := []string{"deeper/", "x.xml", "./y", "../z/"}

	for _, base := range bases {
		for _, ref := range refs {
			got := resolveBase(base, ref)
			if got == ref {
				t.Errorf("resolveBase(%q, %q) = %q: the base was discarded, "+
					"so the reference now names the working directory",
					base, ref, got)
			}
			// Dropping the scheme is the same defect in a subtler spelling:
			// a result that no longer says "file:" is no longer a location.
			if i := strings.Index(base, ":"); i > 0 && !strings.HasPrefix(ref, "/") {
				if scheme := base[:i+1]; !strings.HasPrefix(got, scheme) {
					t.Errorf("resolveBase(%q, %q) = %q: lost the %q scheme",
						base, ref, got, scheme)
				}
			}
		}
	}
}
