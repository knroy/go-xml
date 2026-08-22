package dtd

import (
	"fmt"
	"strings"

	"github.com/knroy/go-xml/xdm"
	"github.com/knroy/go-xml/xsd"
)

// parseModel compiles a DTD element-only content model into an xsd.Particle.
//
// The grammar is small — XML §3.2.1:
//
//	children ::= (choice | seq) ('?' | '*' | '+')?
//	cp       ::= (Name | choice | seq) ('?' | '*' | '+')?
//	choice   ::= '(' cp ( '|' cp )+ ')'
//	seq      ::= '(' cp ( ',' cp )* ')'
//
// Targeting xsd.Particle rather than a private model is the whole point: DTD's
// models are a strict subset of XSD's — no numeric occurrence bounds, no
// wildcards, no all groups — so the Glushkov automaton in xsd already decides
// them, and matching an instance is then the same code that validates against
// a schema.
func parseModel(s string) (*xsd.Particle, error) {
	p := &modelParser{src: strings.TrimSpace(s)}
	part, err := p.parseCP()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.src) {
		return nil, fmt.Errorf("unexpected %q after the content model",
			p.src[p.pos:])
	}
	return part, nil
}

type modelParser struct {
	src string
	pos int
}

func (p *modelParser) skipSpace() {
	for p.pos < len(p.src) && isSpace(rune(p.src[p.pos])) {
		p.pos++
	}
}

// parseCP reads one content particle and any quantifier on it.
func (p *modelParser) parseCP() (*xsd.Particle, error) {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return nil, fmt.Errorf("expected a name or '('")
	}

	var part *xsd.Particle
	if p.src[p.pos] == '(' {
		g, err := p.parseGroup()
		if err != nil {
			return nil, err
		}
		part = g
	} else {
		name, err := p.parseName()
		if err != nil {
			return nil, err
		}
		// A DTD name is not namespace-qualified: the whole prefix mechanism
		// postdates DTDs, and a prefixed name in a DTD is one name containing
		// a colon. Keeping it in Local matches how the instance is compared.
		part = &xsd.Particle{
			MinOccurs: 1, MaxOccurs: 1,
			Term: &xsd.ElementDecl{Name: xdm.QName{Local: name}},
		}
	}
	p.applyQuantifier(part)
	return part, nil
}

// parseGroup reads a parenthesised choice or sequence.
func (p *modelParser) parseGroup() (*xsd.Particle, error) {
	p.pos++ // consume '('
	var members []*xsd.Particle
	sep := byte(0)
	for {
		m, err := p.parseCP()
		if err != nil {
			return nil, err
		}
		members = append(members, m)
		p.skipSpace()
		if p.pos >= len(p.src) {
			return nil, fmt.Errorf("unclosed '(' in the content model")
		}
		switch c := p.src[p.pos]; c {
		case ',', '|':
			// A group is a sequence or a choice, never both: "(a, b | c)" is
			// not a model, and reading it as either would invent a grouping
			// the author did not write.
			if sep != 0 && sep != c {
				return nil, fmt.Errorf(
					"a content model group mixes ',' and '|'")
			}
			sep = c
			p.pos++
		case ')':
			p.pos++
			comp := xsd.CompositorSequence
			if sep == '|' {
				comp = xsd.CompositorChoice
			}
			// A group of one is the particle itself: the parentheses in
			// "(a)*" carry the quantifier, not a grouping.
			if len(members) == 1 {
				return members[0], nil
			}
			return &xsd.Particle{
				MinOccurs: 1, MaxOccurs: 1,
				Term: &xsd.ModelGroup{Compositor: comp, Particles: members},
			}, nil
		default:
			return nil, fmt.Errorf("unexpected %q in the content model",
				string(c))
		}
	}
}

func (p *modelParser) parseName() (string, error) {
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if isSpace(rune(c)) || c == ',' || c == '|' || c == ')' ||
			c == '?' || c == '*' || c == '+' || c == '(' {
			break
		}
		p.pos++
	}
	if p.pos == start {
		return "", fmt.Errorf("expected a name at offset %d", start)
	}
	return p.src[start:p.pos], nil
}

// applyQuantifier reads a trailing ?, * or + and sets the occurrence range.
func (p *modelParser) applyQuantifier(part *xsd.Particle) {
	if p.pos >= len(p.src) {
		return
	}
	switch p.src[p.pos] {
	case '?':
		part.MinOccurs, part.MaxOccurs = 0, 1
		p.pos++
	case '*':
		part.MinOccurs, part.MaxOccurs = 0, xsd.Unbounded
		p.pos++
	case '+':
		part.MinOccurs, part.MaxOccurs = 1, xsd.Unbounded
		p.pos++
	}
}
