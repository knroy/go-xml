package xsd

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// keyref is the half of identity-constraint evaluation the two existing
// oracles do not reach.
//
// identity_oracle_test.go and identity_oracle2_test.go decide validity for
// `key` and `unique` from a generated tree's own shape, and that is enough for
// those two: a duplicate is a local property of one scope's target list. A
// keyref is not local. It resolves against the node table of the key it refers
// to AS THAT TABLE STANDS AT THE KEYREF'S OWN SCOPE, and that table is
// assembled recursively from descendants — so the answer depends on where the
// key was defined relative to where the reference was made, and on what the
// SIBLING subtrees of the reference's scope happened to contain. Neither
// existing oracle generates a document where those differ, and neither
// generates a keyref scope that is not also a key scope, which is the only
// place the sibling rule is observable at all.
//
// The specific failure this exists to catch already happened once. Commit
// 28e455a fixed a merge that could not distinguish "this sequence was never
// seen" from "this sequence was dropped because two sibling subtrees both
// defined it": the second sibling deleted the entry and the third put it back,
// so an ambiguous key became resolvable again, and the bug OSCILLATED with the
// sibling count — wrong at three siblings and five, right at two and four.
// Ten thousand generated documents did not catch it, because every generator
// in the package emitted at most two sibling subtrees. This one emits up to
// five and asserts that it reached three, four and five.
//
// What the oracle computes, from §3.11.4 clause 4.3 and the recursive
// assembly of node tables in §3.11.5:
//
//   - <g> declares the key. Its table is REBUILT over its whole subtree: every
//     <def> at or below it is a target, the first occurrence of a sequence
//     wins, and a repeat is a key violation rather than an ambiguity.
//   - <w> declares only keyrefs. Its table is the MERGE of its children's
//     tables, and a sequence two or more distinct child subtrees each define
//     is ambiguous there — absent from the table, resolvable by nobody.
//     Ambiguity is terminal: a later sibling cannot restore it, and it
//     propagates upward through further merges.
//   - A <use> is a target of its own scope and of every enclosing scope; each
//     resolves against that scope's own table, and all of them must pass.
//   - A <use> whose field is absent does not participate at all — unlike a key
//     target, which must be qualified.
//   - A scope with no key table below it at all fails every participating
//     reference: no key is in scope.
//
// The oracle derives all of that from the generator's record of what it
// emitted. It never calls selectNodes, buildNodeTable, mergeTables or
// checkKeyref: an oracle that consults the implementation agrees with the
// implementation's bugs.
//
// An oracle that agrees proves nothing until it is shown it can disagree, so
// identity.go was deliberately broken five ways and the corpus rerun. Each
// count is disagreements out of 3000 documents:
//
//	mergeEntry ignores the ambiguous flag (28e455a's bug)          5
//	mergeTables drops the inherited ambiguous set                  7
//	checkKeyref skips a node that has a nearer enclosing scope   1739
//	a keyref with an absent field fails instead of abstaining    1088
//	mergeEntry keeps the first sibling instead of dropping both    85
//
// The two ambiguity sabotages are the interesting ones. Five and seven
// documents out of three thousand is how rare the shape is, which is why the
// original bug survived ten thousand generated documents in the existing
// oracles: they never emitted three sibling subtrees, and never put a keyref
// on a scope that did not itself declare the key. Both are required, and the
// generator asserts it reached the first.

// The schema.
//
// Two element kinds carry constraints. <g> carries the key and a keyref, so it
// is both a key scope and a keyref scope. <w> carries keyrefs only, so its
// table for the key is whatever merges up from below — the case where the
// sibling-ambiguity rule decides the answer. Both nest freely in each other.
//
// Three further things in it carry weight:
//
//   - the key is composite (@a, @b), so the field separator matters: a table
//     that joined the fields on a space would equate ("x y") with ("x", "y").
//   - the reference leaf <use> is declared globally and appears under both
//     scope kinds, so one node is a target of the keyref on its own scope and
//     of the keyref on every enclosing scope, whichever kind those are. Two
//     keyrefs selecting one node at one scope is pinned separately, in
//     TestKeyrefOracleTwoKeyrefsOneNode, because it needs a schema shape the
//     generator does not produce.
//   - <def> appears only inside <g>, so every key entry has a well-defined
//     scope of origin and the oracle can say exactly which subtree produced it.
const krSchema = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:choice minOccurs="0" maxOccurs="unbounded">
      <xs:element ref="g"/>
      <xs:element ref="w"/>
    </xs:choice></xs:complexType>
  </xs:element>

  <xs:element name="g">
    <xs:complexType><xs:choice minOccurs="0" maxOccurs="unbounded">
      <xs:element name="def">
        <xs:complexType>
          <xs:attribute name="a" type="xs:string"/>
          <xs:attribute name="b" type="xs:string"/>
        </xs:complexType>
      </xs:element>
      <xs:element ref="use"/>
      <xs:element ref="g"/>
      <xs:element ref="w"/>
    </xs:choice></xs:complexType>
    <xs:key name="k">
      <xs:selector xpath=".//def"/>
      <xs:field xpath="@a"/><xs:field xpath="@b"/>
    </xs:key>
    <xs:keyref name="krg" refer="k">
      <xs:selector xpath=".//use"/>
      <xs:field xpath="@a"/><xs:field xpath="@b"/>
    </xs:keyref>
  </xs:element>

  <xs:element name="w">
    <xs:complexType><xs:choice minOccurs="0" maxOccurs="unbounded">
      <xs:element ref="use"/>
      <xs:element ref="g"/>
      <xs:element ref="w"/>
    </xs:choice></xs:complexType>
    <xs:keyref name="krw" refer="k">
      <xs:selector xpath=".//use"/>
      <xs:field xpath="@a"/><xs:field xpath="@b"/>
    </xs:keyref>
  </xs:element>

  <xs:element name="use">
    <xs:complexType>
      <xs:attribute name="a" type="xs:string"/>
      <xs:attribute name="b" type="xs:string"/>
    </xs:complexType>
  </xs:element>
</xs:schema>`

// krNode is one generated element: a <g> (isG) or a <w>.
//
// defs are emitted only on a <g>, since only <g> declares the key. refsLast
// and defsLast move the leaves after the nested elements, so a reference can
// appear in document order either before or after the definition it resolves
// to. Document order must not matter — the node table is assembled over the
// whole subtree before any keyref is resolved — and a forward reference is the
// cheapest way for an incremental evaluator to be wrong.
type krNode struct {
	isG  bool
	defs []krField // <def>, only when isG
	uses []krField // <use>
	kids []*krNode

	defsLast bool
	refsLast bool
}

// krField is the two fields of a key or a reference. Either may be "", meaning
// the attribute is omitted and the sequence is incomplete.
type krField struct{ a, b string }

// krVals is the number of distinct values a field can take.
//
// It is the dial that sets how often a reference happens to match and how
// often two subtrees happen to collide. Too small and nearly every document is
// invalid, so the oracle would agree with a constant "invalid" and prove
// nothing; too large and no two subtrees ever share a sequence, so ambiguity
// never arises. The test asserts the resulting balance rather than trusting
// this number.
const krVals = 6

func krGen(rnd *rand.Rand, depth, maxKids int) *krNode {
	val := func() string {
		if rnd.Intn(12) == 0 {
			return "" // attribute omitted
		}
		return fmt.Sprintf("v%d", rnd.Intn(krVals))
	}
	n := &krNode{
		// A <w> is what makes sibling ambiguity observable, since only a
		// scope that does NOT declare the key resolves against a merge.
		// A <g> is what puts anything in the table at all. The mix is
		// held near even: all <w> and no reference ever resolves, all
		// <g> and the merge is never consulted.
		isG:      rnd.Intn(2) == 0,
		defsLast: rnd.Intn(2) == 0,
		refsLast: rnd.Intn(2) == 0,
	}
	if n.isG {
		for i := 1 + rnd.Intn(3); i > 0; i-- {
			n.defs = append(n.defs, krField{val(), val()})
		}
	}
	for i := rnd.Intn(2); i > 0; i-- {
		n.uses = append(n.uses, krField{val(), val()})
	}
	if depth > 1 {
		// Drawn over the full range including 0, so a single run covers
		// 1, 2, 3, 4 and 5 sibling subtrees beneath one parent.
		for i := rnd.Intn(maxKids + 1); i > 0; i-- {
			n.kids = append(n.kids, krGen(rnd, depth-1, maxKids))
		}
	}
	return n
}

func (n *krNode) render(sb *strings.Builder) {
	tag := "w"
	if n.isG {
		tag = "g"
	}
	attrs := func(f krField) {
		if f.a != "" {
			fmt.Fprintf(sb, ` a=%q`, f.a)
		}
		if f.b != "" {
			fmt.Fprintf(sb, ` b=%q`, f.b)
		}
	}
	writeDefs := func() {
		for _, f := range n.defs {
			sb.WriteString("<def")
			attrs(f)
			sb.WriteString("/>")
		}
	}
	writeRefs := func() {
		for _, u := range n.uses {
			sb.WriteString("<use")
			attrs(u)
			sb.WriteString("/>")
		}
	}
	fmt.Fprintf(sb, "<%s>", tag)
	if !n.defsLast {
		writeDefs()
	}
	if !n.refsLast {
		writeRefs()
	}
	for _, k := range n.kids {
		k.render(sb)
	}
	if n.defsLast {
		writeDefs()
	}
	if n.refsLast {
		writeRefs()
	}
	fmt.Fprintf(sb, "</%s>", tag)
}

// krSeq joins two field values the way a key sequence is joined, or reports
// that the sequence is incomplete because a field is absent.
//
// The separator is a unit separator rather than a space precisely so that
// ("x y") and ("x", "y") stay distinct; the corpus contains no spaces, and the
// hand-written table below pins the case that does.
func krSeq(f krField) (string, bool) {
	if f.a == "" || f.b == "" {
		return "", false
	}
	return f.a + "\x1f" + f.b, true
}

// krTable is one scope's view of the key, as the oracle computes it.
//
// present is false when nothing at or below this scope produced a table for
// the key at all, which the spec makes a failure for every participating
// reference rather than a silent pass. entries are the sequences that resolve
// here; ambiguous are the sequences two or more distinct sibling subtrees each
// defined, which resolve nowhere at or above this scope.
type krTable struct {
	present   bool
	entries   map[string]bool
	ambiguous map[string]bool
}

// table computes this scope's key table from the spec's recursive definition.
//
// The two element kinds differ, and that difference is the whole point of the
// schema:
//
//   - a <g> DECLARES the key, so its table is rebuilt over its entire subtree.
//     Every <def> below it is a target of this scope. The first occurrence of
//     a sequence wins and a repeat is a validity failure, not an ambiguity —
//     so nothing is ambiguous in a table a <g> produces, whatever the shape
//     below it.
//   - a <w> declares no key, so its table is purely the merge of its
//     children's, and the sibling rule applies: a sequence two distinct child
//     subtrees each define is dropped and marked ambiguous, terminally.
func (n *krNode) table() krTable {
	if n.isG {
		t := krTable{present: true, entries: map[string]bool{}, ambiguous: map[string]bool{}}
		for _, f := range n.subtreeDefs() {
			if s, ok := krSeq(f); ok {
				t.entries[s] = true
			}
		}
		return t
	}

	t := krTable{entries: map[string]bool{}, ambiguous: map[string]bool{}}
	for _, k := range n.kids {
		ct := k.table()
		if !ct.present {
			continue
		}
		t.present = true
		for s := range ct.ambiguous {
			t.ambiguous[s] = true
			delete(t.entries, s)
		}
		for s := range ct.entries {
			if t.ambiguous[s] {
				continue // terminal: a later sibling cannot restore it
			}
			if t.entries[s] {
				t.ambiguous[s] = true
				delete(t.entries, s)
				continue
			}
			t.entries[s] = true
		}
	}
	return t
}

// subtreeDefs returns every <def> at or below this node. Only a <g> carries
// defs, but a <g>'s subtree may contain further <g> elements whose defs are
// also targets of the outer one.
func (n *krNode) subtreeDefs() []krField {
	out := append([]krField{}, n.defs...)
	for _, k := range n.kids {
		out = append(out, k.subtreeDefs()...)
	}
	return out
}

// subtreeRefs returns every <use> at or below this node, since a
// reference is a target of its own scope and of every enclosing scope.
func (n *krNode) subtreeRefs() []krField {
	out := append([]krField{}, n.uses...)
	for _, k := range n.kids {
		out = append(out, k.subtreeRefs()...)
	}
	return out
}

// keyrefFailures counts every (scope, keyref, reference) triple that fails.
//
// A count rather than a boolean, because a boolean is a weak oracle here. The
// outermost scope has to resolve every reference in the whole document against
// its own table, so on a tree of any depth almost every generated document is
// invalid somehow, and "invalid" agrees with a constant. The count does not:
// it changes when one scope stops checking one reference, which is exactly the
// shape of the bugs this is aimed at.
//
// The rules it encodes:
//
//   - Every scope — <g> and <w> alike — checks every reference at or below it
//     against its own table, and each failure is reported separately. One node
//     under three enclosing scopes can fail three times.
//   - A reference whose sequence is incomplete does not participate anywhere.
//   - A scope with no key table below it at all fails every participating
//     reference below it: no key is in scope. The engine reports that one per
//     scope and stops, since nothing below can resolve either.
func (n *krNode) keyrefFailures() int {
	t := n.table()
	fails := 0
	if !t.present {
		// "no k is in scope" is reported once for the scope, at the
		// first participating reference, and the scope gives up.
		for _, u := range n.subtreeRefs() {
			if _, ok := krSeq(u); ok {
				fails++
				break
			}
		}
	} else {
		for _, u := range n.subtreeRefs() {
			s, ok := krSeq(u)
			if !ok || t.entries[s] {
				continue
			}
			fails++
		}
	}
	for _, k := range n.kids {
		fails += k.keyrefFailures()
	}
	return fails
}

// keyFailures counts the `key` violations, which the oracle has to model too:
// the engine reports them into the same error list, and a comparison that
// ignored them would be comparing two different quantities.
//
// A key failure is not reported once per enclosing scope the way a keyref
// failure is. A key produces a table and a keyref does not, and the table
// decides what an ancestor sees:
//
//   - Each scope records every target it selected, with the sequence that
//     target produced, and an ancestor scope inherits that record wholesale.
//     So a target is examined by every enclosing key scope, but through the
//     record rather than by being re-selected.
//   - A target the scope REJECTED is not recorded. An unqualified def, and the
//     second of two defs that collide in one scope's own selection, are
//     therefore reported exactly once, by the innermost scope that saw them,
//     and never reach an ancestor.
//   - Two defs in SIBLING nested scopes are each recorded by their own scope,
//     since neither of those scopes sees the other. Both records reach the
//     common ancestor, which sees the collision for the first time and reports
//     it — and so does ITS ancestor, because both records are still there.
//
// That last asymmetry is the engine's, not the spec's: the same document is
// invalid either way, so it is a difference in how many times one violation is
// described rather than in the verdict. The oracle encodes it because
// comparing counts is what makes this test sharp, and see
// TestKeyrefOracleDuplicateReportingIsAsymmetric, which pins it deliberately
// so that a change to it is a decision rather than an accident.
func (n *krNode) keyFailures() int {
	fails := 0
	n.keyRecorded(&fails)
	return fails
}

// keyRecorded returns the sequences this subtree's tables record for the key —
// one entry per RECORDED target, so a sequence can appear more than once —
// and adds every failure at or below this node into fails.
func (n *krNode) keyRecorded(fails *int) []string {
	// Targets this scope selects directly: its own defs, and those under
	// any descendant that declares no key of its own.
	var direct func(m *krNode) []krField
	direct = func(m *krNode) []krField {
		out := append([]krField{}, m.defs...)
		for _, k := range m.kids {
			if !k.isG {
				out = append(out, direct(k)...)
			}
		}
		return out
	}
	// Records inherited from nested key scopes, whose own failures are
	// counted by their own call.
	var inherited []string
	var collect func(m *krNode)
	collect = func(m *krNode) {
		for _, k := range m.kids {
			if k.isG {
				inherited = append(inherited, k.keyRecorded(fails)...)
			} else {
				collect(k)
			}
		}
	}
	collect(n)

	if !n.isG {
		// Not a key scope: it holds no table, so it neither checks nor
		// records anything. What its descendants recorded passes
		// through, and the defs it holds directly are selected by the
		// nearest enclosing key scope instead.
		return inherited
	}

	seen := map[string]bool{}
	var recorded []string
	// Inherited records are seeded first, and a collision among them is a
	// failure at this scope. Both records survive into this scope's own
	// record, which is why the same collision is reported again by every
	// further ancestor.
	for _, s := range inherited {
		if seen[s] {
			*fails++
		}
		seen[s] = true
		recorded = append(recorded, s)
	}
	// Then this scope's own selection. A rejected target is NOT recorded.
	for _, f := range direct(n) {
		s, ok := krSeq(f)
		if !ok {
			*fails++ // a key target with an absent field
			continue
		}
		if seen[s] {
			*fails++
			continue
		}
		seen[s] = true
		recorded = append(recorded, s)
	}
	return recorded
}

// krMaxSiblings reports the largest number of children under any one node, so
// the test can prove the corpus really reached the counts at which the 28e455a
// bug oscillated rather than merely being configured to.
func (n *krNode) krMaxSiblings() int {
	m := len(n.kids)
	for _, k := range n.kids {
		if c := k.krMaxSiblings(); c > m {
			m = c
		}
	}
	return m
}

func TestKeyrefOracle(t *testing.T) {
	st, err := xdm.ParseString(krSchema, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}

	rnd := rand.New(rand.NewSource(20260904))
	sibHist := map[int]int{}
	var checked, wrong, cleanCount int
	const n = 3000
	for i := 0; i < n; i++ {
		// One or two top-level subtrees. A key in a sibling of the
		// outermost scope is out of scope entirely: <root> declares
		// nothing, so a document-wide table would wrongly resolve
		// across that boundary.
		roots := make([]*krNode, 1+rnd.Intn(2))
		want := 0
		for j := range roots {
			roots[j] = krGen(rnd, 1+rnd.Intn(3), 5)
			sibHist[roots[j].krMaxSiblings()]++
			want += roots[j].keyFailures() + roots[j].keyrefFailures()
		}
		var sb strings.Builder
		sb.WriteString("<root>")
		for _, r := range roots {
			r.render(&sb)
		}
		sb.WriteString("</root>")
		doc := sb.String()

		tr, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("parse instance: %v\n%s", err, doc)
		}
		// The number of failures is compared, not merely whether there
		// were any. A boolean is a weak oracle on this shape: the
		// outermost scope re-checks every reference in the document, so
		// nearly every generated document fails somehow and "invalid"
		// agrees with a constant. The count moves when a single scope
		// stops checking a single reference, which is the granularity
		// the bugs live at. MaxErrors is lifted so the count is not
		// truncated at the default hundred.
		got := 0
		if err := s.Validate(tr.Root, ValidateOptions{MaxErrors: -1}); err != nil {
			ve, okType := err.(*ValidationErrors)
			if !okType {
				t.Fatalf("unexpected error type %T: %v\n%s", err, err, doc)
			}
			for _, e := range ve.Errors {
				if !strings.HasPrefix(e.Code, "cvc-identity-constraint.") {
					t.Fatalf("non-identity failure %s: %v\n%s", e.Code, e, doc)
				}
				got++
			}
		}
		checked++
		if want == 0 {
			cleanCount++
		}
		if got != want {
			wrong++
			if wrong <= 3 {
				t.Errorf("engine reported %d identity failures, oracle %d\n%s",
					got, want, doc)
			}
		}
	}

	// Comparing counts already rules out agreement with a constant, but a
	// corpus with no fully valid document at all would never exercise the
	// path where a reference resolves, so a floor is asserted.
	if cleanCount < n/20 {
		t.Errorf("degenerate corpus: only %d of %d documents fully valid", cleanCount, n)
	}
	// The 28e455a bug was invisible below three siblings. Assert the corpus
	// reaches five, so a future narrowing of the generator fails here
	// instead of quietly losing the coverage.
	for _, want := range []int{3, 4, 5} {
		if sibHist[want] == 0 {
			t.Errorf("no node had exactly %d sibling subtrees; the ambiguity "+
				"oscillation is unobservable in this corpus", want)
		}
	}
	var counts []int
	for k := range sibHist {
		counts = append(counts, k)
	}
	sort.Ints(counts)
	var hist []string
	for _, k := range counts {
		hist = append(hist, fmt.Sprintf("%d:%d", k, sibHist[k]))
	}
	t.Logf("keyref: %d documents checked, %d disagreements (%d fully valid); "+
		"max-siblings histogram %s",
		checked, wrong, cleanCount, strings.Join(hist, " "))
}

// The generator covers the shapes it makes. These pin the cases the dimensions
// are named for, so a narrowing of the generator cannot silently drop them and
// so a failure names which property broke.
func TestKeyrefOracleDimensions(t *testing.T) {
	st, _ := xdm.ParseString(krSchema, xdm.ParseOptions{})
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// def(x,y) is the definition every case below refers to.
	for _, tc := range []struct {
		name    string
		doc     string
		invalid bool
	}{
		{"backward reference resolves",
			`<root><g><def a="x" b="y"/><use a="x" b="y"/></g></root>`, false},
		{"forward reference resolves: document order does not matter",
			`<root><g><use a="x" b="y"/><def a="x" b="y"/></g></root>`, false},
		{"a reference matching nothing fails",
			`<root><g><def a="x" b="y"/><use a="x" b="zz"/></g></root>`, true},
		{"composite: the fields are not joined on a space",
			`<root><g><def a="x y" b="z"/><use a="x" b="y z"/></g></root>`, true},
		{"a use with an absent field does not participate",
			`<root><g><def a="x" b="y"/><use a="x"/></g></root>`, false},
		{"a def with an absent field is a key failure",
			`<root><g><def a="x"/></g></root>`, true},
		{"a duplicate def is a key failure",
			`<root><g><def a="x" b="y"/><def a="x" b="y"/></g></root>`, true},

		// Scope.
		{"a key in a sibling top-level subtree is out of scope",
			`<root><g><def a="x" b="y"/></g><w><use a="x" b="y"/></w></root>`, true},
		{"a key in a nested scope satisfies an enclosing keyref",
			`<root><w><use a="x" b="y"/><g><def a="x" b="y"/></g></w></root>`, false},
		{"a key in an enclosing scope does NOT satisfy a nested keyref",
			`<root><g><def a="x" b="y"/><w><use a="x" b="y"/></w></g></root>`, true},
		{"a key in a sibling nested scope does not satisfy the other",
			`<root><w><g><def a="x" b="y"/></g><w><use a="x" b="y"/></w></w></root>`, true},
		{"a scope with no key below it at all fails the reference",
			`<root><w><use a="x" b="y"/></w></root>`, true},

		// One leaf a target of several scopes at once: it resolves in the
		// inner scope but not in the outer, and both must pass.
		{"a use resolving inside but ambiguous outside fails at the outer scope",
			`<root><w><g><def a="x" b="y"/><use a="x" b="y"/></g><g><def a="x" b="y"/></g></w></root>`, true},
	} {
		tr, err := xdm.ParseString(tc.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := s.Validate(tr.Root, ValidateOptions{}) != nil; got != tc.invalid {
			t.Errorf("%s: invalid=%v want %v\n%s", tc.name, got, tc.invalid, tc.doc)
		}
	}
}

// Ambiguity must not oscillate with the sibling count.
//
// This is 28e455a restated at the document level rather than at the table
// level identity_merge_test.go uses. N sibling <g> subtrees each define the
// same key sequence; the enclosing <w> declares no key of its own, so its
// table is exactly the merge, and the reference it holds must fail for every
// N of two or more. The buggy merge answered "resolves" at three and five.
func TestKeyrefOracleAmbiguityDoesNotOscillate(t *testing.T) {
	st, _ := xdm.ParseString(krSchema, xdm.ParseOptions{})
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for n := 1; n <= 8; n++ {
		var sb strings.Builder
		sb.WriteString(`<root><w>`)
		for i := 0; i < n; i++ {
			sb.WriteString(`<g><def a="x" b="y"/></g>`)
		}
		sb.WriteString(`<use a="x" b="y"/></w></root>`)
		doc := sb.String()
		tr, err := xdm.ParseString(doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		// One definer resolves; two or more are ambiguous, terminally.
		want := n != 1
		if got := s.Validate(tr.Root, ValidateOptions{}) != nil; got != want {
			t.Errorf("%d sibling definers: invalid=%v want %v\n%s", n, got, want, doc)
		}
	}
}

// One node selected by two keyrefs at once, at the same scope.
//
// The generator does not produce this — its scopes carry one keyref each — but
// it is the case a per-node memo gets wrong: the key sequence of a node does
// not depend on which constraint is asking, and identity.go caches it per
// (node, constraint) for exactly that reason. If the cache were keyed on the
// node alone, the second keyref would read the first's answer, and a schema
// where the two select different fields would resolve against the wrong value.
// Here they select the same fields, so the two must simply agree, and the
// failing case must be reported twice rather than once.
func TestKeyrefOracleTwoKeyrefsOneNode(t *testing.T) {
	src := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
  <xs:element name="root">
    <xs:complexType><xs:choice minOccurs="0" maxOccurs="unbounded">
      <xs:element name="def">
        <xs:complexType>
          <xs:attribute name="a" type="xs:string"/>
          <xs:attribute name="b" type="xs:string"/>
        </xs:complexType>
      </xs:element>
      <xs:element name="use">
        <xs:complexType>
          <xs:attribute name="a" type="xs:string"/>
          <xs:attribute name="b" type="xs:string"/>
        </xs:complexType>
      </xs:element>
    </xs:choice></xs:complexType>
    <xs:key name="k2">
      <xs:selector xpath=".//def"/>
      <xs:field xpath="@a"/><xs:field xpath="@b"/>
    </xs:key>
    <xs:keyref name="r1" refer="k2">
      <xs:selector xpath=".//use"/>
      <xs:field xpath="@a"/><xs:field xpath="@b"/>
    </xs:keyref>
    <xs:keyref name="r2" refer="k2">
      <xs:selector xpath=".//use"/>
      <xs:field xpath="@a"/><xs:field xpath="@b"/>
    </xs:keyref>
  </xs:element>
</xs:schema>`
	st, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	sc, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, tc := range []struct {
		doc  string
		want int // identity failures expected
	}{
		{`<root><def a="x" b="y"/><use a="x" b="y"/></root>`, 0},
		{`<root><def a="x" b="y"/><use a="x" b="zz"/></root>`, 2},
		{`<root><def a="x" b="y"/><use a="x"/></root>`, 0},
	} {
		tr, err := xdm.ParseString(tc.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("%v", err)
		}
		got := 0
		if err := sc.Validate(tr.Root, ValidateOptions{MaxErrors: -1}); err != nil {
			got = len(err.(*ValidationErrors).Errors)
		}
		if got != tc.want {
			t.Errorf("%s: %d failures, want %d", tc.doc, got, tc.want)
		}
	}
}

// A duplicate key is described once when one scope selected both occurrences
// and once per enclosing scope when two nested scopes each contributed one.
//
// This is a reporting asymmetry, not a difference in verdict: both documents
// are invalid, and both are invalid for the same reason. It falls out of the
// node table keeping a record of every target it accepted and dropping the
// ones it rejected — a rejected duplicate never reaches an ancestor, while two
// separately-accepted records both do and collide again at every level above.
//
// It is pinned rather than left implicit because TestKeyrefOracle compares
// failure COUNTS, and the count is what makes that test sharp enough to catch
// a scope quietly skipping a check. Anything that changes this multiplicity
// changes those counts, and should be a decision made here rather than a
// surprise there.
func TestKeyrefOracleDuplicateReportingIsAsymmetric(t *testing.T) {
	st, _ := xdm.ParseString(krSchema, xdm.ParseOptions{})
	s, err := Load(st.Root, "", Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, tc := range []struct {
		name string
		doc  string
		want int
	}{
		{"both occurrences selected by one scope: described once",
			`<root><g><g><g><def a="x" b="y"/><def a="x" b="y"/></g></g></g></root>`, 1},
		{"one occurrence in each of two nested scopes: described at every scope above",
			`<root><g><g><g><def a="x" b="y"/></g><g><def a="x" b="y"/></g></g></g></root>`, 2},
		{"an unqualified target is described once, by its innermost scope",
			`<root><g><g><g><def a="x"/></g></g></g></root>`, 1},
	} {
		tr, err := xdm.ParseString(tc.doc, xdm.ParseOptions{})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := 0
		if err := s.Validate(tr.Root, ValidateOptions{MaxErrors: -1}); err != nil {
			got = len(err.(*ValidationErrors).Errors)
		}
		if got != tc.want {
			t.Errorf("%s: %d failures, want %d\n%s", tc.name, got, tc.want, tc.doc)
		}
	}
}
