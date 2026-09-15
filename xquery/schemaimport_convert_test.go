package xquery_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xquery"
)

// TestFunctionResultConvertsToImportedSchemaType pins the function conversion
// rules of XPath 3.1 §3.1.5 against a declared type that came from an imported
// schema.
//
// A function whose declared return type is a schema simple type, returning an
// ELEMENT, must have that element atomised and the resulting xs:untypedAtomic
// cast to the declared type -- and the value that comes back is an instance of
// that type, not of the primitive it restricts. Casting alone was not enough:
// the value has to carry the schema type's identity, or it fails the very type
// it was just converted to.
func TestFunctionResultConvertsToImportedSchemaType(t *testing.T) {
	src := fmt.Sprintf(
		`import schema namespace h = %q;
		 declare function local:f() as h:hatsize { <n>8</n> };
		 local:f()`, hatsNS)
	got, err := run(t, src, withHats())
	if err != nil {
		t.Fatalf("§3.1.5 atomises the element and casts it to the "+
			"declared type: %v", err)
	}
	if got != "8" {
		t.Errorf("got %q, want %q", got, "8")
	}
}

// TestFunctionResultSchemaTypeFacetsApply is the other half of the same rule:
// the conversion is to the SCHEMA type, so the schema's own facets decide the
// value. 99 is outside hatsize's maxInclusive of 12, so the conversion fails
// rather than delivering an unconstrained integer.
func TestFunctionResultSchemaTypeFacetsApply(t *testing.T) {
	src := fmt.Sprintf(
		`import schema namespace h = %q;
		 declare function local:f() as h:hatsize { <n>99</n> };
		 local:f()`, hatsNS)
	if _, err := run(t, src, withHats()); err == nil {
		t.Fatal("a value outside the schema type's facets must be refused")
	}
}

// TestValidateLaxAssessesXSIType pins XSD 1.0 §3.3.4 clause 1.2.1.2: lax
// assessment of an element with no declaration of its own is NOT skipped when
// the element carries xsi:type. The type is resolved and the element assessed
// against it, so invalid content is invalid -- XQDY0027 by §3.21.
//
// "abc 123" contains a space, which no xs:NCName may.
func TestValidateLaxAssessesXSIType(t *testing.T) {
	src := fmt.Sprintf(
		`import schema namespace h = %q;
		 validate lax { <undeclared xsi:type="xs:NCName"
		   xmlns:xs="http://www.w3.org/2001/XMLSchema"
		   xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
		   >abc 123</undeclared> }`, hatsNS)
	_, err := run(t, src, withHats())
	if err == nil {
		t.Fatal("lax assessment must apply the type xsi:type names")
	}
	if !strings.Contains(err.Error(), "XQDY0027") {
		t.Errorf("want XQDY0027 for content invalid against xsi:type, got %v", err)
	}
}

// TestValidateLaxAcceptsValidXSIType is the positive half: the same assessment
// runs, and content that IS valid against the named type comes through.
func TestValidateLaxAcceptsValidXSIType(t *testing.T) {
	src := fmt.Sprintf(
		`import schema namespace h = %q;
		 validate lax { <undeclared xsi:type="xs:NCName"
		   xmlns:xs="http://www.w3.org/2001/XMLSchema"
		   xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
		   >abc</undeclared> }`, hatsNS)
	if _, err := run(t, src, withHats()); err != nil {
		t.Errorf("a valid NCName must pass lax assessment: %v", err)
	}
}

// TestValidateLaxStillSkipsUndeclaredWithoutXSIType is the boundary the change
// above must not blur. An element the schema does not describe, carrying no
// xsi:type, is still skipped -- that is what lax means, and it must keep
// succeeding.
func TestValidateLaxStillSkipsUndeclaredWithoutXSIType(t *testing.T) {
	src := fmt.Sprintf(
		`import schema namespace h = %q;
		 validate lax { <undeclared>anything at all</undeclared> }`, hatsNS)
	if _, err := run(t, src, withHats()); err != nil {
		t.Errorf("lax assessment skips an element with no declaration "+
			"and no xsi:type: %v", err)
	}
}

// TestValidateExprIsNotAPrimary pins the grammar. [102] ValidateExpr is an
// ExprSingle form, not one of the [128] PrimaryExpr alternatives a StepExpr
// reaches through [121] PostfixExpr, so no step and no predicate may follow
// one: XPST0003.
func TestValidateExprIsNotAPrimary(t *testing.T) {
	for _, src := range []string{
		`validate { <a/> }/*`,
		`validate lax { <a/> }/*`,
		`validate strict { <a/> }/self::a`,
		`validate { <a/> }[1]`,
	} {
		_, err := run(t, src, withHats())
		if err == nil || !strings.Contains(err.Error(), "XPST0003") {
			t.Errorf("%s: a step may not follow a validate expression; "+
				"want XPST0003, got %v", src, err)
		}
	}
}

// TestParenthesisedValidateTakesAPath is the contrast case: "(" Expr ")" is
// [133] ParenthesizedExpr, which IS a primary, so the very same path is legal
// once the validate expression is wrapped. Without this the rule above could
// have been a blanket refusal of the path rather than the grammar distinction.
func TestParenthesisedValidateTakesAPath(t *testing.T) {
	src := fmt.Sprintf(
		`import schema namespace h = %q;
		 (validate lax { <box xmlns=%q><hat>8</hat></box> })/*`, hatsNS, hatsNS)
	got, err := run(t, src, withHats())
	if err != nil {
		t.Fatalf("a path may follow a parenthesised validate expression: %v", err)
	}
	if !strings.Contains(got, "hat") {
		t.Errorf("the step must select the child element, got %q", got)
	}
}

// TestOrderedExprTakesAPath guards the neighbour the rule must not catch.
// [136] OrderedExpr is a primary -- PathExpr-21 writes "/ordered{bid}" -- so a
// step following one stays legal.
func TestOrderedExprTakesAPath(t *testing.T) {
	got, err := run(t, `ordered { <a><b/></a> }/*`, xquery.Options{})
	if err != nil {
		t.Fatalf("a step may follow an ordered expression: %v", err)
	}
	if got != "<b/>" {
		t.Errorf("got %q, want %q", got, "<b/>")
	}
}
