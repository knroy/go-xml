package xdm

import (
	"fmt"
	"strconv"
	"strings"
)

// Internal general entities declared in a DOCTYPE's internal subset.
//
// This exists for one real construct: a schema that composes a value from
// named fragments, as the W3C's own RFC 3986 type library does — fifty
// entities building a URI regex out of its grammar productions. Without them
// the document is simply unparseable, because the entity references have no
// definition anywhere else.
//
// **Only internal entities are read.** An entity declared SYSTEM or PUBLIC
// names something outside the document, and fetching it is XXE — the attack
// AllowDOCTYPE exists to gate. Those are recorded as refused rather than
// resolved, so a reference to one is an error and never a silent fetch.
//
// Expansion is bounded twice over, because nested entities are how
// billion-laughs works: each entity's replacement text is expanded at most
// maxEntityDepth levels deep, and the total expanded size of any one entity is
// capped at maxEntityBytes. Either limit turns the reference into an error
// rather than a hang.
const (
	// maxEntityDepth bounds nesting. The RFC 3986 library nests about six
	// deep; a hundred is far past anything hand-written and far short of
	// what an exponential bomb needs.
	maxEntityDepth = 100
	// maxEntityBytes bounds one entity's fully expanded replacement text.
	//
	// Measured against the largest legitimate use known: the W3C's RFC 3987
	// type library expands "IRI-reference" to 9,569 bytes out of 68 entities.
	// 64 KB leaves that six times over and still refuses a five-level
	// billion-laughs, which reaches 100,000 bytes — an earlier 1 MB cap let
	// that one through, which is the whole reason this number is measured
	// rather than picked.
	maxEntityBytes = 64 << 10
	// maxTotalEntityBytes bounds every expansion in one document together.
	// A bomb split across many entities, each under the per-entity cap, would
	// otherwise still add up.
	maxTotalEntityBytes = 1 << 20
	// maxEntityCount bounds how many an internal subset may declare.
	maxEntityCount = 10000
)

// entityTable holds the internal general entities a DOCTYPE declared.
type entityTable struct {
	raw      map[string]string // name -> replacement text, unexpanded
	expanded map[string]string // name -> fully expanded, memoised
	external map[string]bool   // names declared SYSTEM or PUBLIC
	// total is the expanded size of everything resolved so far, so that a
	// bomb divided among many entities cannot slip under the per-entity cap.
	total int
}

// parseInternalEntities reads the <!ENTITY ...> declarations out of a DOCTYPE
// internal subset.
//
// A parameter entity — "<!ENTITY % name ...>" — is skipped. Parameter entities
// are expanded inside the DTD itself rather than in content, and reading one
// would mean interpreting the subset as a grammar rather than scanning it.
func parseInternalEntities(subset string) *entityTable {
	t := &entityTable{
		raw:      map[string]string{},
		expanded: map[string]string{},
		external: map[string]bool{},
	}
	rest := subset
	for len(t.raw)+len(t.external) < maxEntityCount {
		i := strings.Index(rest, "<!ENTITY")
		if i < 0 {
			break
		}
		rest = rest[i+len("<!ENTITY"):]
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			break
		}
		body := strings.TrimSpace(rest[:end])
		rest = rest[end+1:]

		// A parameter entity declaration begins with "%".
		if strings.HasPrefix(body, "%") {
			continue
		}
		fields := attListFields(body)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if !isEntityName(name) {
			continue
		}
		// SYSTEM and PUBLIC name something outside this document. Recording
		// the name rather than dropping it means a reference to it is
		// reported as refused instead of as merely undeclared, which tells a
		// caller the difference between a typo and a blocked fetch.
		if fields[1] == "SYSTEM" || fields[1] == "PUBLIC" {
			t.external[name] = true
			continue
		}
		val := fields[1]
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') {
			// XML §4.2: where more than one declaration names the same
			// entity, the *first* binds and the rest are ignored. The RFC
			// 3986 type library relies on this — it declares sub-delims three
			// times, the first correctly escaped for a regex and the later
			// ones showing the unescaped grammar for a reader. Keeping the
			// last produced a pattern with bare "(" and "+" in it.
			if _, dup := t.raw[name]; !dup {
				t.raw[name] = unquote(val)
			}
		}
	}
	if len(t.raw) == 0 && len(t.external) == 0 {
		return nil
	}
	return t
}

func isEntityName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !isNameStartRune(r) {
			return false
		}
		if i > 0 && !isNameRune(r) {
			return false
		}
	}
	return true
}

// resolve returns the fully expanded replacement text for name.
//
// It implements xml.Decoder's Entity lookup, which takes a map rather than a
// function, so the expansion is done once up front for every declared entity
// and the result handed over as a plain map.
func (t *entityTable) resolve(name string) (string, error) {
	if s, ok := t.expanded[name]; ok {
		return s, nil
	}
	if t.external[name] {
		return "", fmt.Errorf(
			"entity %q is declared SYSTEM or PUBLIC; external entities are "+
				"never resolved", name)
	}
	raw, ok := t.raw[name]
	if !ok {
		return "", fmt.Errorf("entity %q is not declared", name)
	}
	// A placeholder guards against a cycle: an entity that refers to itself,
	// directly or through others, would otherwise recurse forever.
	t.expanded[name] = ""
	out, err := t.expand(raw, 0, map[string]bool{name: true})
	if err != nil {
		delete(t.expanded, name)
		return "", err
	}
	t.total += len(out)
	if t.total > maxTotalEntityBytes {
		delete(t.expanded, name)
		return "", fmt.Errorf(
			"entity expansion exceeds %d bytes in total", maxTotalEntityBytes)
	}
	t.expanded[name] = out
	return out, nil
}

func (t *entityTable) expand(s string, depth int, seen map[string]bool) (string, error) {
	if depth > maxEntityDepth {
		return "", fmt.Errorf("entity expansion exceeds %d levels", maxEntityDepth)
	}
	var sb strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != '&' {
			sb.WriteByte(c)
			i++
			continue
		}
		j := strings.IndexByte(s[i:], ';')
		if j < 0 {
			sb.WriteByte(c)
			i++
			continue
		}
		name := s[i+1 : i+j]
		// A character reference is decoded here. The decoder would do it for
		// text it reads directly, but replacement text arriving through
		// dec.Entity is substituted rather than re-scanned, so "&#xA0;" in an
		// entity would otherwise reach the value literally.
		if name != "" && name[0] == '#' {
			if r, ok := decodeCharRef(name); ok {
				sb.WriteRune(r)
				i += j + 1
				continue
			}
			sb.WriteString(s[i : i+j+1])
			i += j + 1
			continue
		}
		// The five predefined entities are decoded here for the same reason
		// as a character reference: the decoder substitutes replacement text
		// rather than re-scanning it, so "&amp;" inside an entity would reach
		// the value as the five characters "&amp;" instead of "&".
		if name == "" {
			sb.WriteString(s[i : i+j+1])
			i += j + 1
			continue
		}
		if r, ok := predefinedRune(name); ok {
			sb.WriteRune(r)
			i += j + 1
			continue
		}
		if seen[name] {
			return "", fmt.Errorf("entity %q refers to itself", name)
		}
		if t.external[name] {
			return "", fmt.Errorf(
				"entity %q is declared SYSTEM or PUBLIC; external entities are "+
					"never resolved", name)
		}
		raw, ok := t.raw[name]
		if !ok {
			return "", fmt.Errorf("entity %q is not declared", name)
		}
		seen[name] = true
		sub, err := t.expand(raw, depth+1, seen)
		delete(seen, name)
		if err != nil {
			return "", err
		}
		sb.WriteString(sub)
		if sb.Len() > maxEntityBytes {
			return "", fmt.Errorf(
				"entity expansion exceeds %d bytes", maxEntityBytes)
		}
		i += j + 1
	}
	return sb.String(), nil
}

// predefinedRune returns the character one of XML's five predefined entities
// stands for.
func predefinedRune(name string) (rune, bool) {
	switch name {
	case "amp":
		return '&', true
	case "lt":
		return '<', true
	case "gt":
		return '>', true
	case "quot":
		return '"', true
	case "apos":
		return '\'', true
	}
	return 0, false
}

// entityMap expands every declared entity and returns the map the decoder
// wants.
//
// An entity that cannot be expanded is left out rather than reported here: a
// document may declare an entity it never references, and refusing to parse it
// for that would be stricter than the spec. A *reference* to a missing name is
// still an error, raised by the decoder.
func (t *entityTable) entityMap() map[string]string {
	out := make(map[string]string, len(t.raw))
	for name := range t.raw {
		if s, err := t.resolve(name); err == nil {
			out[name] = s
		}
	}
	return out
}

// decodeCharRef turns "#38" or "#x26" into its rune.
func decodeCharRef(name string) (rune, bool) {
	digits, base := name[1:], 10
	if len(digits) > 1 && (digits[0] == 'x' || digits[0] == 'X') {
		digits, base = digits[1:], 16
	}
	if digits == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(digits, base, 32)
	if err != nil || n < 0 || n > 0x10FFFF {
		return 0, false
	}
	return rune(n), true
}
