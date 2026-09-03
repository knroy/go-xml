package xsd

import (
	"errors"
	"io"
	"testing"

	"github.com/knroy/go-xml/xdm"
)

// refusingResolver refuses every location. Fuzzing must not read the
// filesystem: a generated schemaLocation is attacker-controlled by
// construction, and following one would make the target's behaviour depend on
// what happens to sit beside the test binary.
type refusingResolver struct{}

func (refusingResolver) Resolve(namespace, location, base string) (io.ReadCloser, string, error) {
	return nil, "", errors.New("fuzzing resolves nothing")
}

// fuzzSchemaOptions keep an assembly cheap and hermetic. MaxDocuments is
// irrelevant with a resolver that refuses, but it is set low anyway so that a
// future resolver cannot make this target slow by accident.
var fuzzSchemaOptions = Options{
	Resolver:     refusingResolver{},
	MaxDocuments: 4,
	ParseOptions: xdm.ParseOptions{MaxDepth: 100, MaxNodes: 20000},
}

// schemaSeeds are schema documents, valid and not. The content-model cases are
// deliberate: a particle is what compileContentModel turns into an automaton,
// and the counters, wildcards and nested repetitions below are the shapes that
// have historically been where that compiler goes wrong.
var schemaSeeds = []string{
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="a" type="xs:string"/></xs:schema>`,

	// Content models: sequence, choice, all, wildcards, repetition.
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="t"><xs:sequence><xs:element name="a"/><xs:element name="b" minOccurs="0"/></xs:sequence></xs:complexType></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="t"><xs:choice maxOccurs="unbounded"><xs:element name="a"/><xs:element name="b"/></xs:choice></xs:complexType></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="t"><xs:all><xs:element name="a"/><xs:element name="b" minOccurs="0"/></xs:all></xs:complexType></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="t"><xs:sequence minOccurs="2" maxOccurs="5"><xs:sequence minOccurs="0" maxOccurs="3"><xs:element name="a"/></xs:sequence></xs:sequence></xs:complexType></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="t"><xs:sequence><xs:any processContents="lax"/><xs:element name="a"/></xs:sequence></xs:complexType></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="t" mixed="true"><xs:sequence><xs:element name="a"/></xs:sequence></xs:complexType></xs:schema>`,

	// Unique Particle Attribution violation: must be refused, not accepted.
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="t"><xs:choice><xs:element name="a"/><xs:element name="a"/></xs:choice></xs:complexType></xs:schema>`,

	// Simple types, facets, patterns.
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:simpleType name="t"><xs:restriction base="xs:string"><xs:pattern value="[a-z]+"/><xs:maxLength value="3"/></xs:restriction></xs:simpleType></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:simpleType name="t"><xs:list itemType="xs:integer"/></xs:simpleType></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:simpleType name="t"><xs:union memberTypes="xs:integer xs:string"/></xs:simpleType></xs:schema>`,

	// Derivation, groups, substitution, identity constraints.
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:group name="g"><xs:sequence><xs:element name="a"/></xs:sequence></xs:group><xs:complexType name="t"><xs:group ref="g"/></xs:complexType></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="b"><xs:sequence><xs:element name="a"/></xs:sequence></xs:complexType><xs:complexType name="d"><xs:complexContent><xs:extension base="b"><xs:sequence><xs:element name="c"/></xs:sequence></xs:extension></xs:complexContent></xs:complexType></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="r"><xs:complexType><xs:sequence><xs:element name="a" maxOccurs="unbounded"/></xs:sequence></xs:complexType><xs:key name="k"><xs:selector xpath="a"/><xs:field xpath="."/></xs:key></xs:element></xs:schema>`,

	// Composition: must be refused by the resolver, not followed.
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:include schemaLocation="other.xsd"/></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:import namespace="u" schemaLocation="u.xsd"/></xs:schema>`,

	// Malformed and adversarial.
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element/></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="a" type="nosuch"/></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="t"><xs:sequence maxOccurs="-1"><xs:element name="a"/></xs:sequence></xs:complexType></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:complexType name="t"><xs:group ref="t"/></xs:complexType></xs:schema>`,
	`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:simpleType name="t"><xs:restriction base="t"/></xs:simpleType></xs:schema>`,
	`<notaschema/>`,
	`<xs:schema xmlns:xs="wrong-namespace"/>`,
}

// Load must never panic on a schema document, whatever it says. A .xsd is as
// untrusted as the .xml it validates when both arrive over the wire, and the
// assembler is a far larger and more recursive body of code than the parser.
//
// This target also covers the content-model compiler: every complexType the
// assembler accepts has its particle turned into an automaton before Load
// returns, so a model that breaks the compiler is reached from here.
func FuzzLoadSchemaNoPanic(f *testing.F) {
	for _, s := range schemaSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 4096 {
			return
		}
		tree, err := xdm.ParseString(src, fuzzSchemaOptions.ParseOptions)
		if err != nil {
			// Not well-formed XML. The XML parser's own target owns that
			// case; there is no schema here to assemble.
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Load(%q) panicked: %v", src, r)
			}
		}()
		for _, v := range []Version{Version10, Version11} {
			opts := fuzzSchemaOptions
			opts.Version = v
			s, err := Load(tree.Root, "", opts)
			if err != nil {
				// A refusal must be an error and nothing else.
				if s != nil {
					t.Fatalf("Load(%q) at %v returned both a schema and an error %v", src, v, err)
				}
				continue
			}
			if s == nil {
				t.Fatalf("Load(%q) at %v returned no error and no schema", src, v)
			}
			fuzzWalkSchema(t, src, s)
		}
	})
}

// fuzzWalkSchema forces the work an accepted schema defers, so that a defect
// reachable only from a later caller is reached here rather than in
// production. Compiling every content model is the point: that is the
// automaton builder, and it is what a caller validating a document runs.
func fuzzWalkSchema(t *testing.T, src string, s *Schema) {
	t.Helper()
	for _, ct := range s.allComplexTypes {
		if ct == nil || ct.Particle == nil {
			continue
		}
		m, err := NewSequenceMatcher(ct.Particle)
		if err != nil {
			// A model this compiler cannot build is a refusal, not a crash.
			continue
		}
		// Matching must be total: every name sequence gets a yes or a no.
		for _, names := range [][]xdm.QName{
			nil,
			{{Local: "a"}},
			{{Local: "a"}, {Local: "b"}},
			{{URI: "u", Local: "a"}, {Local: "a"}, {Local: "a"}},
		} {
			ok, at := m.Match(names)
			if !ok && (at < 0 || at > len(names)) {
				t.Fatalf("Load(%q): Match returned a rejection index %d outside 0..%d", src, at, len(names))
			}
		}
	}
}
