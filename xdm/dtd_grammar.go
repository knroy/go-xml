package xdm

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// The markup-declaration grammar of XML 1.0 §2.8 and §3.2-§4.7, recognised
// and nothing more.
//
// The rest of this package reads a DTD for what it needs (entities, ATTLIST
// defaults and types, element-only models) and skips whatever it does not
// understand, so a declaration that is not well formed went unnoticed: §5.1
// requires every processor, validating or not, to check "the document entity,
// including the entire internal DTD subset, for well-formedness", and to report
// the well-formedness errors of any parsed entity it reads. checkDoctypeGrammar
// is that check for the DOCTYPE, and checkExtSubsetGrammar for an external
// subset this package read.
//
// A parameter-entity reference inside a markup declaration is legal in the
// external subset (WFC: PEs in Internal Subset says so by exception) and this
// package does not expand references there, so a declaration holding one is
// passed over unchecked rather than refused: the reference may supply exactly
// the token the declaration seems to lack. A conditional section whose keyword
// is a reference is passed over for the same reason.

// dtdScan is the recogniser's position in one subset.
type dtdScan struct {
	s        string
	i        int
	external bool // extSubsetDecl rules: conditional sections, references in declarations
}

func dtdErr(format string, a ...any) error {
	return fmt.Errorf("malformed DTD: "+format, a...)
}

// checkDoctypeGrammar checks [28] doctypedecl. raw is the directive as the
// tokeniser returned it, from "DOCTYPE" to the byte before the closing ">";
// the tokeniser has already replaced each comment in it with a space and held
// its body to [15]. external selects the extSubsetDecl rules for the internal
// subset, for the text that results once parameter entities are substituted:
// WFC: PEs in Internal Subset "does not apply to references that occur in
// external parameter entities or to the content of internal parameter
// entities".
func checkDoctypeGrammar(raw string, external bool) error {
	p := &dtdScan{s: raw, external: external}
	if !strings.HasPrefix(raw, "DOCTYPE") {
		return dtdErr("expected DOCTYPE")
	}
	p.i = len("DOCTYPE")
	if !p.space() {
		return dtdErr("expected white space after DOCTYPE")
	}
	if _, ok := p.name(); !ok {
		return dtdErr("expected the document element's name in the DOCTYPE")
	}
	if p.space() {
		if c := p.peek(); c == 'S' || c == 'P' {
			if err := p.externalID(false); err != nil {
				return err
			}
			p.space()
		}
	}
	if p.peek() == '[' {
		p.i++
		if err := p.subset("]"); err != nil {
			return err
		}
		p.i++
		p.space()
	}
	if p.i != len(p.s) {
		return dtdErr("unexpected %q in the DOCTYPE", p.rest())
	}
	return nil
}

// checkExtSubsetGrammar checks [30] extSubset (its text declaration already
// removed) or the text of an external parameter entity: [31] extSubsetDecl.
func checkExtSubsetGrammar(text string) error {
	return (&dtdScan{s: text, external: true}).subset("")
}

func (p *dtdScan) peek() byte {
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *dtdScan) rest() string {
	r := p.s[p.i:]
	if len(r) > 20 {
		r = r[:20] + "..."
	}
	return r
}

// space skips [3] S and reports whether there was any.
func (p *dtdScan) space() bool {
	from := p.i
	for p.i < len(p.s) && isXMLSpaceByte(p.s[p.i]) {
		p.i++
	}
	return p.i > from
}

// name reads [5] Name.
func (p *dtdScan) name() (string, bool) {
	return p.nameOr(false)
}

// nmtoken reads [7] Nmtoken.
func (p *dtdScan) nmtoken() (string, bool) {
	return p.nameOr(true)
}

func (p *dtdScan) nameOr(token bool) (string, bool) {
	from := p.i
	for p.i < len(p.s) {
		r, n := utf8.DecodeRuneInString(p.s[p.i:])
		if !isNameRune(r) || (!token && p.i == from && !isNameStartRune(r)) {
			break
		}
		p.i += n
	}
	return p.s[from:p.i], p.i > from
}

// keyword consumes the first of words the input continues with, as a whole
// name, and returns it, or "".
func (p *dtdScan) keyword(words ...string) string {
	at := p.i
	n, _ := p.nmtoken()
	for _, w := range words {
		if n == w {
			return w
		}
	}
	p.i = at
	return ""
}

func (p *dtdScan) lit(s string) bool {
	if strings.HasPrefix(p.s[p.i:], s) {
		p.i += len(s)
		return true
	}
	return false
}

// quoted reads a quoted literal and returns its body.
func (p *dtdScan) quoted(what string) (string, error) {
	q := p.peek()
	if q != '"' && q != '\'' {
		return "", dtdErr("expected a quoted %s, found %q", what, p.rest())
	}
	end := strings.IndexByte(p.s[p.i+1:], q)
	if end < 0 {
		return "", dtdErr("unterminated %s", what)
	}
	v := p.s[p.i+1 : p.i+1+end]
	p.i += end + 2
	return v, nil
}

// subset reads markupdecls, DeclSeps and (external only) conditional
// sections up to end, which is left unconsumed, or to the end of the text
// when end is "". An INCLUDE section's contents are read by this same loop,
// counted in open until its "]]>", so nesting them costs no stack.
func (p *dtdScan) subset(end string) error {
	open := 0 // INCLUDE sections entered and not yet closed
	for {
		p.space()
		if p.i == len(p.s) {
			if end != "" || open > 0 {
				return dtdErr("unterminated internal subset or conditional section")
			}
			return nil
		}
		if open > 0 {
			if p.lit("]]>") {
				open--
				continue
			}
		} else if end != "" && strings.HasPrefix(p.s[p.i:], end) {
			return nil
		}
		switch {
		case p.peek() == '%':
			p.i++
			if err := p.reference(); err != nil {
				return err
			}
		case p.lit("<!--"):
			e := strings.Index(p.s[p.i:], "-->")
			if e < 0 {
				return dtdErr("unterminated comment")
			}
			p.i += e + 3
		case p.lit("<?"):
			if err := p.pi(); err != nil {
				return err
			}
		case p.lit("<!["):
			if !p.external {
				return dtdErr("a conditional section is allowed only in the external subset (XML 1.0 §3.4)")
			}
			include, err := p.conditional()
			if err != nil {
				return err
			}
			if include {
				open++
			}
		case p.lit("<!"):
			if err := p.declaration(); err != nil {
				return err
			}
		default:
			return dtdErr("unexpected %q where a markup declaration may appear", p.rest())
		}
	}
}

// reference reads the rest of [69] PEReference or [68] EntityRef after its
// "%" or "&".
func (p *dtdScan) reference() error {
	if _, ok := p.name(); !ok || !p.lit(";") {
		return dtdErr("malformed entity reference before %q", p.rest())
	}
	return nil
}

// pi reads [16] PI after "<?".
func (p *dtdScan) pi() error {
	target, ok := p.name()
	if !ok {
		return dtdErr("expected a processing-instruction target")
	}
	if strings.EqualFold(target, "xml") {
		return dtdErr("processing-instruction target %q is reserved (XML 1.0 §2.6 [17])", target)
	}
	if !p.space() && !strings.HasPrefix(p.s[p.i:], "?>") {
		return dtdErr("expected white space after processing-instruction target %s", target)
	}
	e := strings.Index(p.s[p.i:], "?>")
	if e < 0 {
		return dtdErr("unterminated processing instruction")
	}
	p.i += e + 2
	return nil
}

// conditional reads the start of [61] conditionalSect after "<![". An
// IGNORE section is read whole; for an INCLUDE it reads up to the "[" and
// reports include, and the calling subset reads the contents and "]]>".
func (p *dtdScan) conditional() (include bool, err error) {
	p.space()
	kw := p.keyword("INCLUDE", "IGNORE")
	ref := kw == ""
	if ref {
		if !p.lit("%") {
			return false, dtdErr("expected INCLUDE or IGNORE after <![")
		}
		if err := p.reference(); err != nil {
			return false, err
		}
		kw = "IGNORE" // a reference decides it; read past without checking
	}
	p.space()
	// After a reference the "[" may be in its text (xmltest
	// invalid-not-sa-022: <!ENTITY % e "INCLUDE[">).
	if !p.lit("[") && !ref {
		return false, dtdErr("expected [ after %s", kw)
	}
	if kw == "INCLUDE" {
		return true, nil
	}
	end := ignoreSectEnd(p.s, p.i)
	if end < 0 {
		return false, dtdErr("unterminated conditional section")
	}
	p.i = end
	return false, nil
}

// ignoreSectEnd returns the offset just past the "]]>" that closes an IGNORE
// section whose [63] ignoreSectContents start at s[i], or -1 when none does.
// [63]-[65]: the contents are not markup, but their "<![" and "]]>" still
// nest. One forward pass: the contents are read once whatever the nesting.
func ignoreSectEnd(s string, i int) int {
	for depth := 1; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "<!["):
			depth++
			i += len("<![")
		case strings.HasPrefix(s[i:], "]]>"):
			i += len("]]>")
			if depth--; depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return -1
}

// declaration reads one markup declaration after "<!".
func (p *dtdScan) declaration() error {
	kw := p.keyword("ELEMENT", "ATTLIST", "ENTITY", "NOTATION")
	if kw == "" {
		return dtdErr("unknown markup declaration %q", p.rest())
	}
	start := p.i
	end := endOfDeclaration(p.s[start:])
	if end < 0 {
		if p.external && hasUnquotedPercent(p.s[start:]) {
			// A reference may supply the ">" (xmltest valid-not-sa-005's
			// shape); nothing after it can be told apart from it.
			p.i = len(p.s)
			return nil
		}
		return dtdErr("unterminated <!%s declaration", kw)
	}
	body := p.s[start : start+end]
	p.i = start + end + 1
	refs := body
	if kw == "ENTITY" {
		// The "%" of "<!ENTITY % name" declares a parameter entity; it is
		// not a reference.
		if t := strings.TrimLeft(refs, " \t\r\n"); len(t) < len(refs) && strings.HasPrefix(t, "%") {
			refs = t[1:]
		}
	}
	if hasUnquotedPercent(refs) {
		if p.external {
			return nil // see the note on dtdScan
		}
		return dtdErr("a parameter-entity reference inside <!%s in the internal subset (WFC: PEs in Internal Subset)", kw)
	}
	d := &dtdScan{s: body, external: p.external}
	var err error
	switch kw {
	case "ELEMENT":
		err = d.elementDecl()
	case "ATTLIST":
		err = d.attlistDecl()
	case "ENTITY":
		err = d.entityDecl()
	case "NOTATION":
		err = d.notationDecl()
	}
	if err != nil {
		return err
	}
	if d.space(); d.i != len(d.s) {
		return dtdErr("unexpected %q in <!%s", d.rest(), kw)
	}
	return nil
}

// hasUnquotedPercent reports a "%" outside the declaration's quoted literals.
func hasUnquotedPercent(s string) bool {
	var q byte
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '%':
			return true
		}
	}
	return false
}

// elementDecl reads the rest of [45] elementdecl.
func (p *dtdScan) elementDecl() error {
	if !p.space() {
		return dtdErr("expected white space after <!ELEMENT")
	}
	if _, ok := p.name(); !ok {
		return dtdErr("expected an element name in <!ELEMENT")
	}
	if !p.space() {
		return dtdErr("expected white space before the content specification")
	}
	if p.keyword("EMPTY", "ANY") != "" {
		return nil
	}
	if !p.lit("(") {
		return dtdErr("expected EMPTY, ANY or ( in <!ELEMENT, found %q", p.rest())
	}
	p.space()
	if p.lit("#PCDATA") {
		return p.mixed()
	}
	if err := p.group(); err != nil {
		return err
	}
	p.occurrence()
	return nil
}

// mixed reads the rest of [51] Mixed after "(" S? "#PCDATA".
func (p *dtdScan) mixed() error {
	names := 0
	for {
		p.space()
		if p.lit(")") {
			if p.lit("*") || names == 0 {
				return nil
			}
			return dtdErr("mixed content naming elements must end with )*")
		}
		if !p.lit("|") {
			return dtdErr("expected | or ) in mixed content, found %q", p.rest())
		}
		p.space()
		if _, ok := p.name(); !ok {
			return dtdErr("expected an element name in mixed content")
		}
		names++
	}
}

// group reads the rest of [49] choice or [50] seq after "(" S?, every nested
// [48] cp group included. It iterates with a stack holding each open group's
// separator rather than recursing per "(": the nesting is bounded only by the
// input's length, and a goroutine stack overflow is fatal, not an error.
func (p *dtdScan) group() error {
	seps := []byte{0} // the separator of each open group, 0 until its first
	for {
		// [48] cp: a nested group opens, or a name is read.
		if p.lit("(") {
			p.space()
			seps = append(seps, 0)
			continue
		}
		if _, ok := p.name(); !ok {
			return dtdErr("expected a name or ( in a content model, found %q", p.rest())
		}
		p.occurrence()
		// Close groups until a separator continues one.
		for {
			p.space()
			if !p.lit(")") {
				break
			}
			seps = seps[:len(seps)-1]
			if len(seps) == 0 {
				return nil // the caller reads the outermost group's occurrence
			}
			p.occurrence()
		}
		c := p.peek()
		if c != '|' && c != ',' {
			return dtdErr("expected |, , or ) in a content model, found %q", p.rest())
		}
		top := &seps[len(seps)-1]
		if *top != 0 && c != *top {
			return dtdErr("a content model group mixes | and ,")
		}
		*top = c
		p.i++
		p.space()
	}
}

func (p *dtdScan) occurrence() {
	if c := p.peek(); c == '?' || c == '*' || c == '+' {
		p.i++
	}
}

// attlistDecl reads the rest of [52] AttlistDecl.
func (p *dtdScan) attlistDecl() error {
	if !p.space() {
		return dtdErr("expected white space after <!ATTLIST")
	}
	if _, ok := p.name(); !ok {
		return dtdErr("expected an element name in <!ATTLIST")
	}
	for {
		spaced := p.space()
		if p.i == len(p.s) {
			return nil
		}
		// [53] AttDef ::= S Name S AttType S DefaultDecl
		if !spaced {
			return dtdErr("expected white space before an attribute definition, found %q", p.rest())
		}
		if _, ok := p.name(); !ok {
			return dtdErr("expected an attribute name in <!ATTLIST, found %q", p.rest())
		}
		if !p.space() {
			return dtdErr("expected white space before the attribute type")
		}
		if err := p.attType(); err != nil {
			return err
		}
		if !p.space() {
			return dtdErr("expected white space before the attribute default")
		}
		if err := p.defaultDecl(); err != nil {
			return err
		}
	}
}

// attType reads [54] AttType.
func (p *dtdScan) attType() error {
	switch p.keyword("CDATA", "ID", "IDREF", "IDREFS", "ENTITY", "ENTITIES", "NMTOKEN", "NMTOKENS", "NOTATION") {
	case "NOTATION":
		if !p.space() {
			return dtdErr("expected white space after NOTATION")
		}
		if !p.lit("(") {
			return dtdErr("expected ( after NOTATION")
		}
		return p.enumeration(false)
	case "":
		if !p.lit("(") {
			return dtdErr("expected an attribute type, found %q", p.rest())
		}
		return p.enumeration(true)
	}
	return nil
}

// enumeration reads the rest of [58] NotationType or [59] Enumeration after
// "(".
func (p *dtdScan) enumeration(tokens bool) error {
	for {
		p.space()
		if _, ok := p.nameOr(tokens); !ok {
			return dtdErr("expected a value in an enumerated type, found %q", p.rest())
		}
		p.space()
		if p.lit(")") {
			return nil
		}
		if !p.lit("|") {
			return dtdErr("expected | or ) in an enumerated type, found %q", p.rest())
		}
	}
}

// defaultDecl reads [60] DefaultDecl.
func (p *dtdScan) defaultDecl() error {
	if p.lit("#REQUIRED") || p.lit("#IMPLIED") {
		return nil
	}
	if p.lit("#FIXED") && !p.space() {
		return dtdErr("expected white space after #FIXED")
	}
	v, err := p.quoted("attribute default")
	if err != nil {
		return err
	}
	// [10] AttValue ::= '"' ([^<&"] | Reference)* '"'
	if strings.IndexByte(v, '<') >= 0 {
		return dtdErr("an attribute default contains <")
	}
	return checkReferences(v, '&')
}

// checkReferences holds every occurrence of each of refs in a literal to
// [67] Reference for "&" and [69] PEReference for "%".
func checkReferences(v string, refs ...byte) error {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !strings.ContainsRune(string(refs), rune(c)) {
			continue
		}
		p := &dtdScan{s: v, i: i + 1}
		if c == '&' && p.lit("#") {
			digits := "0123456789"
			if p.lit("x") {
				digits = "0123456789abcdefABCDEF"
			}
			from := p.i
			for p.i < len(v) && strings.IndexByte(digits, v[p.i]) >= 0 {
				p.i++
			}
			if p.i == from || !p.lit(";") {
				return dtdErr("malformed character reference in %q", v)
			}
		} else if err := p.reference(); err != nil {
			return dtdErr("malformed reference in %q", v)
		}
		i = p.i - 1
	}
	return nil
}

// entityDecl reads the rest of [70] EntityDecl.
func (p *dtdScan) entityDecl() error {
	if !p.space() {
		return dtdErr("expected white space after <!ENTITY")
	}
	pe := p.lit("%")
	if pe && !p.space() {
		return dtdErr("expected white space after %% in <!ENTITY")
	}
	// Namespaces in XML 1.0 §7 (NE08): no entity name contains a colon.
	if n, ok := p.name(); !ok {
		return dtdErr("expected an entity name in <!ENTITY, found %q", p.rest())
	} else if strings.IndexByte(n, ':') >= 0 {
		return dtdErr("entity name %q contains a colon", n)
	}
	if !p.space() {
		return dtdErr("expected white space after the entity name")
	}
	if c := p.peek(); c == '"' || c == '\'' {
		v, err := p.quoted("entity value")
		if err != nil {
			return err
		}
		// [9] EntityValue ::= '"' ([^%&"] | PEReference | Reference)* '"'.
		// WFC: PEs in Internal Subset forbids the reference there.
		if !p.external && strings.IndexByte(v, '%') >= 0 {
			return dtdErr("a parameter-entity reference in an entity value in the internal subset (WFC: PEs in Internal Subset)")
		}
		return checkReferences(v, '&', '%')
	}
	if err := p.externalID(false); err != nil {
		return err
	}
	at := p.i
	if p.space() && p.keyword("NDATA") != "" {
		if pe {
			return dtdErr("a parameter entity cannot be unparsed (NDATA)")
		}
		if !p.space() {
			return dtdErr("expected white space after NDATA")
		}
		if _, ok := p.name(); !ok {
			return dtdErr("expected a notation name after NDATA")
		}
		return nil
	}
	p.i = at
	return nil
}

// notationDecl reads the rest of [82] NotationDecl.
func (p *dtdScan) notationDecl() error {
	if !p.space() {
		return dtdErr("expected white space after <!NOTATION")
	}
	// Namespaces in XML 1.0 §7 (NE08): no notation name contains a colon.
	if n, ok := p.name(); !ok {
		return dtdErr("expected a notation name in <!NOTATION")
	} else if strings.IndexByte(n, ':') >= 0 {
		return dtdErr("notation name %q contains a colon", n)
	}
	if !p.space() {
		return dtdErr("expected white space after the notation name")
	}
	return p.externalID(true)
}

// externalID reads [75] ExternalID, or with public also [83] PublicID.
func (p *dtdScan) externalID(public bool) error {
	switch p.keyword("SYSTEM", "PUBLIC") {
	case "SYSTEM":
		if !p.space() {
			return dtdErr("expected white space after SYSTEM")
		}
		_, err := p.quoted("system literal")
		return err
	case "PUBLIC":
		if !p.space() {
			return dtdErr("expected white space after PUBLIC")
		}
		id, err := p.quoted("public identifier")
		if err != nil {
			return err
		}
		// [13] PubidChar
		if strings.TrimLeft(id, " \r\nabcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-'()+,./:=?;!*#@$_%") != "" {
			return dtdErr("public identifier %q holds a character [13] PubidChar excludes", id)
		}
		at := p.i
		if p.space() {
			if c := p.peek(); c == '"' || c == '\'' {
				_, err := p.quoted("system literal")
				return err
			}
		}
		p.i = at
		if !public {
			return dtdErr("expected a system literal after the public identifier")
		}
		return nil
	}
	return dtdErr("expected SYSTEM or PUBLIC, found %q", p.rest())
}
