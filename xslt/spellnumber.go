package xslt

import (
	"strconv"
	"strings"
)

// Numbers written as words, for the "w", "W" and "Ww" format tokens of
// section 12.3, and ordinal numbering for the ordinal attribute.
//
// The specification makes both language-sensitive and both
// implementation-defined in their detail: "the set of languages for which
// numbering is supported is implementation-defined", and a processor that does
// not support a requested language "uses the language that it would use if the
// lang attribute were omitted". English is what is implemented here, and a
// request for anything else falls back to it rather than failing — which is
// exactly what that rule prescribes.

var smallWords = []string{
	"zero", "one", "two", "three", "four", "five", "six", "seven", "eight",
	"nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
	"sixteen", "seventeen", "eighteen", "nineteen",
}

var tensWords = []string{
	"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy",
	"eighty", "ninety",
}

// scaleWords are the powers of a thousand, in the short scale.
var scaleWords = []string{"", " thousand", " million", " billion", " trillion"}

// spellNumber writes n in English words.
//
// ordinal selects the ordinal form, which changes only the *last* word:
// "twenty-first", not "twentieth-first". That is why the ordinal conversion is
// applied to the assembled string rather than to each group.
func spellNumber(n int64, ordinal bool) string {
	if n < 0 {
		return "minus " + spellNumber(-n, ordinal)
	}
	if n == 0 {
		if ordinal {
			return "zeroth"
		}
		return "zero"
	}
	// Beyond the scales named above there is no agreed English word, and the
	// specification permits a bound: outside it the format token 1 is used.
	if n >= 1000000000000000 {
		return strconv.FormatInt(n, 10)
	}

	// Split into groups of three from the least significant end, so that each
	// group is spoken with its own scale word.
	var groups []int64
	for n > 0 {
		groups = append(groups, n%1000)
		n /= 1000
	}

	var parts []string
	for i := len(groups) - 1; i >= 0; i-- {
		if groups[i] == 0 {
			continue
		}
		parts = append(parts, spellUnder1000(groups[i])+scaleWords[i])
	}
	s := strings.Join(parts, " ")
	if ordinal {
		return ordinalWords(s)
	}
	return s
}

// spellUnder1000 writes a number below a thousand.
func spellUnder1000(n int64) string {
	switch {
	case n < 20:
		return smallWords[n]
	case n < 100:
		s := tensWords[n/10]
		if n%10 != 0 {
			// The hyphen is the English convention for the compound tens,
			// and it is what a title-cased "Twenty-One" then capitalises on
			// both sides of.
			s += "-" + smallWords[n%10]
		}
		return s
	default:
		s := smallWords[n/100] + " hundred"
		if n%100 != 0 {
			s += " " + spellUnder1000(n%100)
		}
		return s
	}
}

// irregularOrdinals are the words whose ordinal is not formed by adding "th".
var irregularOrdinals = map[string]string{
	"one": "first", "two": "second", "three": "third", "five": "fifth",
	"eight": "eighth", "nine": "ninth", "twelve": "twelfth",
}

// ordinalWords converts the last word of a spelled number to its ordinal.
//
// Only the last word changes: "one hundred and twenty-first" ends in the
// ordinal and everything before it stays cardinal.
func ordinalWords(s string) string {
	// The last word is what follows the final space or hyphen, and the
	// separator has to be kept: "twenty-first" and "twenty first" are not
	// interchangeable.
	i := strings.LastIndexAny(s, " -")
	head, last := "", s
	if i >= 0 {
		head, last = s[:i+1], s[i+1:]
	}
	if o, ok := irregularOrdinals[last]; ok {
		return head + o
	}
	// A word ending in "y" forms its ordinal in "ieth": twenty, twentieth.
	if strings.HasSuffix(last, "y") {
		return head + last[:len(last)-1] + "ieth"
	}
	return head + last + "th"
}

// ordinalSuffix returns the English ordinal suffix for a number written in
// digits: 1st, 2nd, 3rd, 4th, and 11th/12th/13th which are exceptions.
func ordinalSuffix(n int64) string {
	if n < 0 {
		n = -n
	}
	// The teens are the exception: 11, 12 and 13 take "th" despite ending in
	// 1, 2 and 3, and that repeats every hundred.
	switch n % 100 {
	case 11, 12, 13:
		return "th"
	}
	switch n % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

// German numbering, for lang="de". Section 12.3 makes the supported set of
// languages implementation-defined; German is added because the suite asks
// for it, and because its cardinals are regular enough to write down in full
// rather than approximated.
//
// Three things make German different from the English above and are the whole
// reason it cannot reuse spellUnder1000:
//
//   - The units come BEFORE the tens and are joined by "und": 21 is
//     "einundzwanzig", literally one-and-twenty. English's "twenty-one"
//     order is simply wrong here.
//   - Everything below a million is written as ONE word, with no spaces:
//     "einhundertfünfundzwanzigtausend". Only the scale words from a million
//     up stand apart, and they are nouns that inflect for plural
//     ("eine Million", "zwei Millionen").
//   - "eins" is the standalone form and "ein" the form used in a compound:
//     1 alone is "eins", but 21 is "einundzwanzig" and 100 is "einhundert",
//     never "einsundzwanzig". germanUnits therefore holds the compound form
//     and spellNumberDE substitutes "eins" only for the bare value 1.
var germanUnits = []string{
	"null", "ein", "zwei", "drei", "vier", "fünf", "sechs", "sieben", "acht",
	"neun", "zehn", "elf", "zwölf", "dreizehn", "vierzehn", "fünfzehn",
	"sechzehn", "siebzehn", "achtzehn", "neunzehn",
}

// germanTens holds the multiples of ten. Note the irregular stems: 16 and 17
// drop a consonant ("sechzehn", not "sechszehn"; "siebzehn", not
// "siebenzehn") which germanUnits above already spells out, and 60/70 do the
// same here. 30 is the only one spelled with ß.
var germanTens = []string{
	"", "zehn", "zwanzig", "dreißig", "vierzig", "fünfzig", "sechzig",
	"siebzig", "achtzig", "neunzig",
}

// germanScales are the powers of a thousand. "tausend" attaches to the
// preceding word; from a million up the scale is a separate noun with a
// plural form, so both are recorded.
var germanScales = []struct{ singular, plural string }{
	{"", ""},
	{"tausend", "tausend"},
	{"Million", "Millionen"},
	{"Milliarde", "Milliarden"},
	{"Billion", "Billionen"},
}

// spellNumberDE writes n in German words.
//
// Ordinals are not attempted: German ordinals are declined for case and
// gender ("der erste", "dem ersten"), and xsl:number's ordinal attribute
// carries no such information. Section 12.3 lets a processor fall back to
// the language it would use with lang omitted, so an ordinal request in
// German is answered in English rather than in a wrong German inflection.
func spellNumberDE(n int64) string {
	if n < 0 {
		return "minus " + spellNumberDE(-n)
	}
	if n == 0 {
		return "null"
	}
	// The standalone form. Every other appearance of 1 is inside a compound
	// and takes "ein", which germanUnits already holds.
	if n == 1 {
		return "eins"
	}
	if n >= 1000000000000000 {
		return strconv.FormatInt(n, 10)
	}

	var groups []int64
	for m := n; m > 0; m /= 1000 {
		groups = append(groups, m%1000)
	}

	var b strings.Builder
	for i := len(groups) - 1; i >= 0; i-- {
		g := groups[i]
		if g == 0 {
			continue
		}
		scale := germanScales[i]
		switch {
		case i == 0:
			b.WriteString(spellUnder1000DE(g))
		case i == 1:
			// "tausend" is glued on: 2000 is "zweitausend", one word.
			b.WriteString(spellUnder1000DE(g))
			b.WriteString(scale.singular)
		default:
			// A million and above is a separate noun, so it takes a space on
			// each side and the feminine "eine" when there is exactly one:
			// "eine Million", not "ein Million".
			if b.Len() > 0 {
				b.WriteString(" ")
			}
			if g == 1 {
				b.WriteString("eine " + scale.singular)
			} else {
				b.WriteString(spellUnder1000DE(g) + " " + scale.plural)
			}
			if i > 0 && groups[0] != 0 {
				b.WriteString(" ")
			}
		}
	}
	return b.String()
}

// spellUnder1000DE writes a number below a thousand as a single German word.
func spellUnder1000DE(n int64) string {
	switch {
	case n < 20:
		return germanUnits[n]
	case n < 100:
		unit, ten := n%10, germanTens[n/10]
		if unit == 0 {
			return ten
		}
		// Units first, joined by "und": 21 = ein + und + zwanzig.
		return germanUnits[unit] + "und" + ten
	default:
		s := germanUnits[n/100] + "hundert"
		if n%100 != 0 {
			s += spellUnder1000DE(n % 100)
		}
		return s
	}
}
