package c14n

import (
	"strconv"
	"testing"
)

// The C14N 1.1 section 2.4 join-URI-References examples and the Appendix A
// remove-dot-segments table, transcribed from the Recommendation. They test
// the xml:base fix-up's URI arithmetic directly, which is unexported.

var joinCases = []struct{ base, ref, want string }{
	{"abc/", "../", ""},
	{"../", "../", "../../"},
	{"..", "..", "../../"},
}

// dotSegmentCases is C14N 1.1 Appendix A, "example results of the modified
// Remove Dot Segments algorithm described in Section 2.4", row for row.
var dotSegmentCases = [][2]string{
	{"no/.././/pseudo-netpath/seg/file.ext", "pseudo-netpath/seg/file.ext"},
	{"no/..//.///pseudo-netpath/seg/file.ext", "pseudo-netpath/seg/file.ext"},
	{"yes/no//..//.///pseudo-netpath/seg/file.ext", "yes/pseudo-netpath/seg/file.ext"},
	{"no/../yes", "yes"},
	{"no/../yes/", "yes/"},
	{"no/../yes/no/..", "yes/"},
	{"../../no/../..", "../../../"},
	{"no/../..", "../"},
	{"no/..", ""},
	{"no/../", ""},
	{"/a/b/c/./../../g", "/a/g"},
	{"mid/content=5/../6", "mid/6"},
	{"../../..", "../../../"},
	{"no/../../", "../"},
	{"..yes/..no/..no/..no/../../../..yes", "..yes/..yes"},
	{"..yes/..no/..no/..no/../../../..yes/", "..yes/..yes/"},
	{"../..", "../../"},
	{"../../../", "../../../"},
	{".", ""},
	{"./", ""},
	{"./.", ""},
	{"//no/..", "/"},
	{"../../no/..", "../../"},
	{"../../no/../", "../../"},
	{"yes/no/../", "yes/"},
	{"yes/no/no/../..", "yes/"},
	{"yes/no/no/no/../../..", "yes/"},
	{"yes/no/../yes/no/no/../..", "yes/yes/"},
	{"yes/no/no/no/../../../yes", "yes/yes"},
	{"yes/no/no/no/../../../yes/", "yes/yes/"},
	{"/no/../", "/"},
	{"/yes/no/../", "/yes/"},
	{"/yes/no/no/../..", "/yes/"},
	{"/yes/no/no/no/../../..", "/yes/"},
	{"../../..no/..", "../../"},
	{"../../..no/../", "../../"},
	{"..yes/..no/../", "..yes/"},
	{"..yes/..no/..no/../..", "..yes/"},
	{"..yes/...no/..no/..no/../../..", "..yes/"},
	{"..yes/..no/../..yes/..no/..no/../..", "..yes/..yes/"},
	{"/..no/../", "/"},
	{"/..yes/..no/../", "/..yes/"},
	{"/..yes/..no/..no/../..", "/..yes/"},
	{"/..yes/..no/..no/..no/../../..", "/..yes/"},
	{"/", "/"},
	{"/.", "/"},
	{"/./", "/"},
	{"/./.", "/"},
	{"/././", "/"},
	{"/..", "/"},
	{"/../..", "/"},
	{"/../../..", "/"},
	{"/../../..", "/"},
	{"//..", "/"},
	{"//..//..", "/"},
	{"//..//..//..", "/"},
	{"/./..", "/"},
	{"/./.././..", "/"},
	{"/./.././.././..", "/"},
	{".", ""},
	{"./", ""},
	{"./.", ""},
	{"..", "../"},
	{"../", "../"},
}

func TestJoinURIReferencesSpecTables(t *testing.T) {
	for _, c := range joinCases {
		t.Run("c14n11/2.4-join/"+c.base+"+"+c.ref, func(t *testing.T) {
			if got := joinURIReferences(c.base, c.ref); got != c.want {
				t.Errorf("join-URI-References(%q, %q) = %q, want %q", c.base, c.ref, got, c.want)
			}
		})
	}
	for i, c := range dotSegmentCases {
		t.Run("c14n11/appendix-A/"+strconv.Itoa(i+1), func(t *testing.T) {
			if got := removeDots(c[0]); got != c[1] {
				t.Errorf("remove dot segments(%q) = %q, want %q", c[0], got, c[1])
			}
		})
	}

}
