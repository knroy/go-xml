package xpath

import "testing"

// TestCheckSerializationParam pins the schema types (Serialization 3.1
// appendix B) the shared check holds each string-valued parameter to.
// fn:serialize's element form used to accept json as a node output method and
// any html-version; the parameter-document reader accepted everything.
func TestCheckSerializationParam(t *testing.T) {
	for _, tc := range []struct{ name, val, want string }{
		{"indent", " true ", "yes"},
		{"standalone", "omit", "omit"},
		{"method", "Q{}json", "json"},
		{"json-node-output-method", "xhtml", "xhtml"},
		{"encoding", "iso-8859-1", "iso-8859-1"},
		{"html-version", "5.0", "5.0"},
		{"media-type", " text/html ", "text/html"},
		{"cdata-section-elements", "a p:b Q{u}c", "a p:b Q{u}c"},
	} {
		got, err := CheckSerializationParam(tc.name, tc.val)
		if err != nil || got != tc.want {
			t.Errorf("%s=%q: got %q, %v; want %q", tc.name, tc.val, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ name, val string }{
		{"indent", "maybe"},
		{"omit-xml-declaration", "omit"},
		{"standalone", "maybe"},
		{"method", "foo"},
		{"json-node-output-method", "json"},
		{"encoding", "UTF 8"},
		{"encoding", "8bit"},
		{"doctype-public", "a{b}"},
		{"doctype-system", `a'"b`},
		{"html-version", "five"},
		{"normalization-form", "a b"},
		{"cdata-section-elements", "::x"},
		{"build-tree", "yes"},
	} {
		if got, err := CheckSerializationParam(tc.name, tc.val); err == nil {
			t.Errorf("%s=%q: accepted as %q", tc.name, tc.val, got)
		}
	}
}
