package xslt

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// Result.String discards the serialization error. That is deliberate -- the
// fmt.Stringer signature has nowhere to put one -- but it means the SEPM0016
// check that closes the doctype-system injection is enforced on Serialize and
// not on String, and an embedder who reaches for the inviting call gets "".
//
// This pins the three facts the doc comment on String asserts, because a
// comment that drifts from the behaviour is worse than no comment: it tells a
// reader the error is somewhere it is not.
//
//  1. Transform does NOT fail on invalid output settings. XSLT 3.0 section
//     2.10 puts a serialization error on the principal result "after the
//     transformation has finished", so moving the check into Transform would
//     diverge from the spec. If this assertion ever fails, the divergence has
//     been introduced and the comment must change with it.
//  2. Serialize DOES report it, with the code.
//  3. String returns "" and no signal, on the very same Result.
func TestResultStringDiscardsSerializationError(t *testing.T) {
	// Both quote kinds: Serialization 3.1 section 3 gives doctype-system a
	// value space excluding exactly this, since an external identifier
	// literal has no escaping mechanism and no delimiter is left.
	const sheet = `<xsl:stylesheet version="3.0"
    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:output method="xml" doctype-system="a&quot;b'c"/>
  <xsl:template match="/"><out/></xsl:template>
</xsl:stylesheet>`

	compiled, err := Compile(mustParse(t, sheet), CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	src, err := xdm.ParseString(`<r/>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}

	res, err := compiled.Transform(context.Background(), src.Root,
		TransformOptions{})
	if err != nil {
		t.Fatalf("Transform reported %v; XSLT 3.0 section 2.10 places a "+
			"serialization error on the principal result after the "+
			"transformation has finished, so an invalid doctype-system must "+
			"not fail the transform. If this is now intended, the doc "+
			"comment on Result.String must stop saying otherwise", err)
	}

	// Serialize, on this same Result, must surface it.
	var buf strings.Builder
	serr := res.Serialize(&buf)
	if serr == nil {
		t.Fatal("Serialize accepted a doctype-system holding both an " +
			"apostrophe and a quotation mark; SEPM0016 is required")
	}
	if !strings.Contains(serr.Error(), "SEPM0016") {
		t.Errorf("Serialize: got %v, want SEPM0016 -- the parameter value "+
			"was refused, not the document", serr)
	}

	// String, on that same Result, swallows it. Asserting the empty string
	// rather than merely "no panic" is the point: "" is indistinguishable
	// from a successfully serialised empty document, which is why the doc
	// comment has to warn and why an embedder must not use this call for
	// output whose failure matters.
	if got := res.String(); got != "" {
		t.Errorf("String() = %q, want %q; this test encodes the discard so "+
			"that a change to it is a deliberate one", got, "")
	}
}

// The warning is only useful where an embedder will read it. This fails if
// either String loses the two things that make it actionable: that the error
// is discarded, and what to call instead.
//
// Both methods are checked by one table because documenting only one of them
// is the mistake that was actually made here.
func TestResultStringDocumentsTheDiscard(t *testing.T) {
	for _, m := range []struct{ file, decl string }{
		{"transform.go", "func (r *Result) String() string"},
		{"resultdoc.go", "func (sr *SecondaryResult) String() string"},
	} {
		src, err := os.ReadFile(m.file)
		if err != nil {
			t.Fatal(err)
		}
		i := strings.Index(string(src), m.decl)
		if i < 0 {
			t.Fatalf("%s not found in %s -- if it was renamed or its receiver "+
				"changed, update this table rather than deleting the case; "+
				"an absent method was previously mistaken for a method that "+
				"does not exist", m.decl, m.file)
		}
		// The contiguous comment block immediately above the declaration.
		doc := string(src)[:i]
		if k := strings.LastIndex(doc, "\n\n"); k >= 0 {
			doc = doc[k:]
		}
		for _, want := range []string{"DISCARD", "Serialize"} {
			if !strings.Contains(doc, want) {
				t.Errorf("the doc comment on %s no longer mentions %q; it is "+
					"the only place an embedder learns that a serialization "+
					"error -- including the SEPM0016 doctype-system check -- "+
					"is dropped here and that Serialize is the call that "+
					"reports it", m.decl, want)
			}
		}
	}
}

// The same discard on SecondaryResult.String, which is the more exposed of the
// two and was missed when the primary one was documented: a grep for the
// method used the wrong receiver name, found nothing, and the absence was
// reported as "SecondaryResult has no String()". It has one.
//
// This drives doctype-system from the SOURCE DOCUMENT through the attribute
// value template that xsl:result-document's serialization attributes are,
// which is what makes the value attacker-supplied rather than authored -- the
// reason the injection fix exists at all.
func TestSecondaryResultStringDiscardsSerializationError(t *testing.T) {
	const sheet = `<xsl:stylesheet version="3.0"
    xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
  <xsl:template match="/">
    <xsl:result-document href="r.xml" doctype-system="{/doc/ds}">
      <out/>
    </xsl:result-document>
  </xsl:template>
</xsl:stylesheet>`
	compiled, err := Compile(mustParse(t, sheet), CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// Both quote kinds, supplied as element content in the source document.
	src, err := xdm.ParseString(`<doc><ds>a"b'c</ds></doc>`, xdm.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := compiled.Transform(context.Background(), src.Root,
		TransformOptions{})
	if err != nil {
		t.Fatalf("Transform reported %v; XSLT 3.0 §26 raises a secondary "+
			"serialization error in xsl:result-document, and this "+
			"stylesheet's body is otherwise valid", err)
	}
	if len(res.Secondary) != 1 {
		t.Fatalf("got %d secondary results, want 1", len(res.Secondary))
	}
	sr := res.Secondary[0]

	var buf bytes.Buffer
	serr := sr.Serialize(&buf, nil)
	if serr == nil {
		t.Fatal("Serialize accepted a source-supplied doctype-system holding " +
			"both an apostrophe and a quotation mark; SEPM0016 is required")
	}
	if !strings.Contains(serr.Error(), "SEPM0016") {
		t.Errorf("Serialize: got %v, want SEPM0016 -- the parameter value was "+
			"refused, not the document", serr)
	}
	if got := sr.String(); got != "" {
		t.Errorf("String() = %q, want %q; this encodes the discard so that a "+
			"change to it is a deliberate one", got, "")
	}
}
