package dtd

import (
	"fmt"
	"strings"
)

// parseAttList reads "<!ATTLIST element name type default ...>".
//
// One declaration defines any number of attributes, each three tokens: a name,
// a type, and a default declaration that is itself one or two tokens. The type
// may be a parenthesised enumeration, which the tokeniser keeps whole.
func parseAttList(body string) []*Attribute {
	f := attFields(body)
	if len(f) < 2 {
		return nil
	}
	element := f[0]
	var out []*Attribute
	for i := 1; i+1 < len(f); {
		a := &Attribute{Element: element, Name: f[i], Type: f[i+1]}
		i += 2
		// A NOTATION type is written "NOTATION (a|b)", so the enumeration is
		// the token after it rather than the type itself.
		if a.Type == "NOTATION" && i < len(f) && strings.HasPrefix(f[i], "(") {
			a.Enum = enumValues(f[i])
			i++
		} else if strings.HasPrefix(a.Type, "(") {
			a.Enum = enumValues(a.Type)
			a.Type = "ENUMERATION"
		}
		if i >= len(f) {
			break
		}
		switch d := f[i]; {
		case d == "#REQUIRED":
			a.Default = AttrRequired
			i++
		case d == "#IMPLIED":
			a.Default = AttrImplied
			i++
		case d == "#FIXED":
			a.Default = AttrFixed
			i++
			if i < len(f) {
				a.Value = unquote(f[i])
				i++
			}
		case strings.HasPrefix(d, "#"):
			// Not one of the three keywords, and an unquoted literal cannot
			// begin with "#". Recorded as invalid rather than read as the
			// default value "#REQUIRE", which would silently demote a
			// required attribute to an optional one.
			a.Default = AttrInvalid
			a.Value = d
			i++
		default:
			a.Default = AttrDefaulted
			a.Value = unquote(d)
			i++
		}
		out = append(out, a)
	}
	return out
}

// attributeDeclFaults applies the validity constraints XML 1.0 §3.3 places on
// an attribute definition itself, given the name of the ID attribute already
// bound for its element ("" when none is), and describes each one broken.
//
// A default is checked whether or not any element uses it (rmt-e2e-9b), but
// only lexically: that an ENTITY default names an unparsed entity, or an
// IDREF default an ID, matters only once it is applied, and the value check
// in Validate sees it then (rmt-e3e-06i).
func attributeDeclFaults(a *Attribute, boundID string) []string {
	var out []string
	add := func(format string, args ...any) {
		out = append(out, fmt.Sprintf("attribute %s of %s ", a.Name, a.Element)+fmt.Sprintf(format, args...))
	}
	// §3.3.1 VC No Duplicate Tokens: "The notation names in a single
	// NotationType attribute declaration, as well as the NmTokens in a
	// single Enumeration attribute declaration, MUST all be distinct."
	// A set, not a scan of the prefix: that cost the square of the tokens,
	// 1.35 s for one 40,000-token enumeration.
	seen := make(map[string]struct{}, len(a.Enum))
	for _, t := range a.Enum {
		if _, dup := seen[t]; dup {
			add("lists %s more than once (XML §3.3.1 No Duplicate Tokens)", t)
			break
		}
		seen[t] = struct{}{}
	}
	if a.Type == "ID" && boundID != "" {
		// §3.3.1 VC One ID per Element Type: "An element type MUST NOT
		// have more than one ID attribute specified."
		add("is a second ID attribute, after %s (XML §3.3.1 One ID per Element Type)", boundID)
	}
	if a.Default != AttrFixed && a.Default != AttrDefaulted {
		return out
	}
	if a.Type == "ID" {
		// §3.3.1 VC ID Attribute Default: "An ID attribute MUST have a
		// declared default of #IMPLIED or #REQUIRED."
		add("is an ID with a default value (XML §3.3.1 ID Attribute Default)")
		return out
	}
	// §3.3.2 VC Attribute Default Value Syntactically Correct: "The
	// declared default value MUST meet the syntactic constraints of the
	// declared attribute type." The default is normalized first, as a
	// value of that type would be (§3.3.3).
	v := normalizedDefault(a.Value)
	if a.Type != "CDATA" {
		v = strings.Join(tokens(v), " ")
	}
	if len(a.Enum) > 0 && !containsString(a.Enum, v) {
		add("has the default %q, which is not one of %s (XML §3.3.2 Attribute Default Value Syntactically Correct)", v, strings.Join(a.Enum, ", "))
	} else if f := lexicalFault(a.Type, v); f != "" {
		add("has the default %q, which %s (XML §3.3.2 Attribute Default Value Syntactically Correct)", v, f)
	}
	return out
}

// attFields splits an ATTLIST body, keeping a quoted literal or a
// parenthesised enumeration as one field.
func attFields(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '"' || c == '\'':
			j := strings.IndexByte(s[i+1:], c)
			if j < 0 {
				out = append(out, s[i:])
				return out
			}
			out = append(out, s[i:i+j+2])
			i += j + 2
		case c == '(':
			j := strings.IndexByte(s[i:], ')')
			if j < 0 {
				out = append(out, s[i:])
				return out
			}
			out = append(out, s[i:i+j+1])
			i += j + 1
		default:
			j := i
			for j < len(s) && !strings.ContainsRune(" \t\n\r", rune(s[j])) {
				j++
			}
			out = append(out, s[i:j])
			i = j
		}
	}
	return out
}

// enumValues reads "(a | b | c)" into its members.
func enumValues(s string) []string {
	s = strings.TrimPrefix(strings.TrimSuffix(s, ")"), "(")
	var out []string
	for _, v := range strings.Split(s, "|") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
