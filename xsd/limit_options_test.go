package xsd

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/knroy/go-xml/v2/xdm"
)

// The Options and ValidateOptions budgets that used to be fixed: each test
// pins that the zero value keeps today's verdict, that a lowered value takes
// effect, and that a raised value admits what the default refuses.

func loadString(t *testing.T, src string, opts Options) (*Schema, error) {
	t.Helper()
	doc, err := xdm.ParseString(src, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return Load(doc.Root, "", opts)
}

// optionalRoot is genOptionalSequence with a global element r of that type,
// so a document can be validated against it.
func optionalRoot(n int) string {
	var b strings.Builder
	b.WriteString(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r"><xs:complexType><xs:sequence>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<xs:element name="e%d" minOccurs="0"/>`, i)
	}
	b.WriteString(`</xs:sequence></xs:complexType></xs:element></xs:schema>`)
	return b.String()
}

func TestOptionsMaxUPAStateWidth(t *testing.T) {
	if DefaultMaxUPAStateWidth != 256 || maxUPAStateWidth != DefaultMaxUPAStateWidth {
		t.Fatalf("default width %d/%d, want 256", DefaultMaxUPAStateWidth, maxUPAStateWidth)
	}
	wide := optionalRoot(260)
	for _, w := range []int{0, -1} {
		if _, err := loadString(t, wide, Options{MaxUPAStateWidth: w}); !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("MaxUPAStateWidth %d: a 260-wide state loaded (%v), want the default refusal", w, err)
		}
	}
	if _, err := loadString(t, wide, Options{MaxUPAStateWidth: math.MaxInt}); err != nil {
		t.Errorf("MaxUPAStateWidth MaxInt: %v", err)
	}
	s, err := loadString(t, wide, Options{MaxUPAStateWidth: 300})
	if err != nil {
		t.Fatalf("MaxUPAStateWidth 300: %v", err)
	}
	// Validation compiles the model again; it must use the width the schema
	// loaded under, or a schema that loaded could not validate anything.
	doc, _ := xdm.ParseString(`<r><e0/><e259/></r>`, xdm.ParseOptions{})
	if err := s.Validate(doc.Root, ValidateOptions{}); err != nil {
		t.Errorf("validating under the raised width: %v", err)
	}
	// The first state of n optional elements is n wide: at the limit loads,
	// one over is refused.
	if _, err := loadString(t, optionalRoot(20), Options{MaxUPAStateWidth: 20}); err != nil {
		t.Errorf("MaxUPAStateWidth 20: a 20-wide state was refused: %v", err)
	}
	for _, w := range []int{1, 19} {
		if _, err := loadString(t, optionalRoot(20), Options{MaxUPAStateWidth: w}); !errors.Is(err, xdm.ErrResourceLimit) {
			t.Errorf("MaxUPAStateWidth %d: a 20-wide state loaded (%v), want a refusal", w, err)
		}
	}
}

func TestOptionsMaxUPAPairTests(t *testing.T) {
	if DefaultMaxUPAPairTests != 1<<22 || maxUPAPairTests != DefaultMaxUPAPairTests {
		t.Fatalf("default pairs %d/%d, want 1<<22", DefaultMaxUPAPairTests, maxUPAPairTests)
	}
	narrow := optionalRoot(20)
	for _, p := range []int{0, -1, math.MaxInt} {
		if _, err := loadString(t, narrow, Options{MaxUPAPairTests: p}); err != nil {
			t.Errorf("MaxUPAPairTests %d: %v", p, err)
		}
	}
	if _, err := loadString(t, narrow, Options{MaxUPAPairTests: 10}); !errors.Is(err, xdm.ErrResourceLimit) {
		t.Errorf("MaxUPAPairTests 10: loaded (%v), want a refusal", err)
	}
	// Raised past a lowered package default, the option wins.
	defer func(p int) { maxUPAPairTests = p }(maxUPAPairTests)
	maxUPAPairTests = 10
	if _, err := loadString(t, narrow, Options{MaxUPAPairTests: 5000}); err != nil {
		t.Errorf("MaxUPAPairTests 5000 over a default of 10: %v", err)
	}
}

// A 1.1 restriction only the exact inclusion check admits: the 1.0 table maps
// each derived particle to its own base particle, so two a's cannot restrict
// one a{0,2}. Declining sends it to the table, which rejects it.
const subsumeOnly = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
<xs:complexType name="B"><xs:sequence><xs:element name="a" maxOccurs="2"/></xs:sequence></xs:complexType>
<xs:complexType name="D"><xs:complexContent><xs:restriction base="B"><xs:sequence>
<xs:element name="a"/><xs:element name="a"/></xs:sequence></xs:restriction></xs:complexContent></xs:complexType>
</xs:schema>`

// A choice restricting an all group: 1.0 forbids it, the 1.1 branch count
// admits it.
const branchOnly = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
<xs:complexType name="B"><xs:all><xs:element name="a" minOccurs="0"/><xs:element name="b" minOccurs="0"/></xs:all></xs:complexType>
<xs:complexType name="D"><xs:complexContent><xs:restriction base="B"><xs:choice>
<xs:element name="a"/><xs:element name="b"/></xs:choice></xs:restriction></xs:complexContent></xs:complexType>
</xs:schema>`

func TestOptionsRestrictionBudgets(t *testing.T) {
	if subsumeMaxStates != DefaultMaxSubsumptionStates || DefaultMaxSubsumptionStates != 4096 ||
		subsumeMaxProduct != DefaultMaxSubsumptionProduct || DefaultMaxSubsumptionProduct != 20000 ||
		branchLimit != DefaultMaxRestrictionBranches || DefaultMaxRestrictionBranches != 4096 {
		t.Fatal("a restriction budget default moved")
	}
	for _, c := range []struct {
		name   string
		src    string
		set    func(*Options, int)
		lowerV *int
	}{
		{"MaxSubsumptionStates", subsumeOnly, func(o *Options, n int) { o.MaxSubsumptionStates = n }, &subsumeMaxStates},
		{"MaxSubsumptionProduct", subsumeOnly, func(o *Options, n int) { o.MaxSubsumptionProduct = n }, &subsumeMaxProduct},
		{"MaxRestrictionBranches", branchOnly, func(o *Options, n int) { o.MaxRestrictionBranches = n }, &branchLimit},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := loadString(t, c.src, Options{}); err == nil {
				t.Fatal("control: XSD 1.0 accepted a restriction only 1.1 admits")
			}
			for _, n := range []int{0, -1, math.MaxInt} {
				o := Options{Version: Version11}
				c.set(&o, n)
				if _, err := loadString(t, c.src, o); err != nil {
					t.Errorf("%s %d: %v", c.name, n, err)
				}
			}
			o := Options{Version: Version11}
			c.set(&o, 1)
			if _, err := loadString(t, c.src, o); err == nil {
				t.Errorf("%s 1: the exact check did not decline", c.name)
			}
			defer func(v int) { *c.lowerV = v }(*c.lowerV)
			*c.lowerV = 1
			if _, err := loadString(t, c.src, Options{Version: Version11}); err == nil {
				t.Fatalf("lowered package default did not decline")
			}
			c.set(&o, 4096)
			if _, err := loadString(t, c.src, o); err != nil {
				t.Errorf("%s 4096 over a default of 1: %v", c.name, err)
			}
		})
	}
}

// Nested occurrence bounds leave several count readings live at once.
const manyReadings = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
<xs:element name="r"><xs:complexType><xs:sequence maxOccurs="10">
<xs:element name="a" minOccurs="0" maxOccurs="10"/></xs:sequence></xs:complexType></xs:element>
</xs:schema>`

func TestValidateOptionsMaxMatchStates(t *testing.T) {
	s, err := loadString(t, manyReadings, Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := xdm.ParseString(`<r><a/><a/><a/><a/><a/></r>`, xdm.ParseOptions{})
	for _, n := range []int{0, -1, DefaultMaxMatchStates, math.MaxInt} {
		if err := s.Validate(doc.Root, ValidateOptions{MaxMatchStates: n}); err != nil {
			t.Errorf("MaxMatchStates %d: %v", n, err)
		}
	}
	err = s.Validate(doc.Root, ValidateOptions{MaxMatchStates: 1})
	if err == nil || !strings.Contains(err.Error(), errMatchStates.Error()) {
		t.Errorf("MaxMatchStates 1: %v, want the match-state refusal", err)
	}
	// The streamed refusal reads the same budget; MaxInt must not overflow.
	m, err := s.newValidator(nil, ValidateOptions{}).modelFor(s.Elements[xdm.QName{Local: "r"}].Type.(*ComplexType))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []int{-1, math.MaxInt, DefaultMaxMatchStates} {
		if !streamReadingsBounded(m, b) {
			t.Errorf("streamReadingsBounded(budget %d) = false, want true", b)
		}
	}
	if streamReadingsBounded(m, 1) {
		t.Error("streamReadingsBounded(budget 1) = true for a model with 11x11 readings")
	}
}
