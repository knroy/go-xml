package dtd

import "strings"

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
		default:
			a.Default = AttrDefaulted
			a.Value = unquote(d)
			i++
		}
		out = append(out, a)
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
