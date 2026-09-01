package xquery

import "testing"

// The boundary between XML syntax and expression syntax. Every case here is
// one where a naive brace count gets a different answer.
func TestFindEnclosed(t *testing.T) {
	// want is the index of the closing brace: every case is one where a
	// naive brace count gets a different answer.
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"simple", "{$x}", 3},
		{"nested braces", "{ {1} }", 6},
		{"map inside", "{ map {'a':1} }", 14},
		{"brace in single-quoted string", "{ '}' }", 6},
		{"brace in double-quoted string", `{ "}" }`, 6},
		{"escaped quote then brace", "{ 'it''s }' }", 12},
		{"double-quoted with apostrophe", `{ "it's }" }`, 11},
		{"brace in comment", "{ (: } :) 1 }", 12},
		{"nested comment", "{ (: a (: } :) b :) 1 }", 22},
		{"apostrophe in comment", "{ (: it's :) 1 }", 15},
		{"constructor inside", "{ <b>{$x}</b> }", 14},
		{"string then comment", "{ 'a' (: } :) }", 14},
	} {
		got, err := findEnclosed(c.src, 0)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: close at %d, want %d, in %q",
				c.name, got, c.want, c.src)
		}
	}
}

func TestFindEnclosedRejectsUnterminated(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"no close", "{$x"},
		{"unterminated string", "{ 'abc"},
		{"unterminated comment", "{ (: abc"},
		{"close inside string only", "{ '}'"},
		{"nested comment unclosed", "{ (: a (: b :) "},
	} {
		if _, err := findEnclosed(c.src, 0); err == nil {
			t.Errorf("%s: want an error for %q", c.name, c.src)
		}
	}
}

func TestFindEnclosedRequiresABrace(t *testing.T) {
	if _, err := findEnclosed("$x", 0); err == nil {
		t.Error("want an error when the offset is not a brace")
	}
	if _, err := findEnclosed("", 0); err == nil {
		t.Error("want an error at end of input")
	}
}

// The extracted substring is what reaches the XPath parser, so it has to be
// exactly the expression and neither brace.
func TestEnclosedBodyIsTheExpression(t *testing.T) {
	src := "{ $x + 1 }"
	end, err := findEnclosed(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := src[1:end], " $x + 1 "; got != want {
		t.Errorf("body %q, want %q", got, want)
	}
}
