package tjs

import (
	"reflect"
	"testing"
)

func TestLexerLiteralsAndLocations(t *testing.T) {
	src := "var π = 0x2a + 1.5e2;\n\"hi\\u263a\""
	ts, err := LexWithStorage("script.tjs", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 9 {
		t.Fatalf("got %d tokens, want 9", len(ts))
	}
	if ts[0].Kind != TokenKeyword || ts[0].Text != "var" {
		t.Fatalf("first token = %#v", ts[0])
	}
	if ts[1].Location.Line != 1 || ts[1].Location.Column != 5 {
		t.Fatalf("identifier location = %#v", ts[1].Location)
	}
	if ts[2].Text != "=" || ts[2].Kind != TokenOperator {
		t.Fatalf("assignment token = %#v", ts[2])
	}
	if n, ok := ts[3].Value.(int64); !ok || n != 42 {
		t.Fatalf("hex value = %#v", ts[3].Value)
	}
	if n, ok := ts[5].Value.(float64); !ok || n != 150 {
		t.Fatalf("real value = %#v", ts[5].Value)
	}
	if s, ok := ts[7].Value.(UTF16); !ok || s.String() != "hi☺" {
		t.Fatalf("string value = %#v", ts[7].Value)
	}
	if ts[8].Kind != TokenEOF || ts[8].Location.Line != 2 {
		t.Fatalf("eof = %#v", ts[8])
	}
}

func TestLexerCommentsRegexAndDivision(t *testing.T) {
	ts, err := Lex("/* a */ /a[b\\/]c/gi / 2 // tail\n / 3")
	if err != nil {
		t.Fatal(err)
	}
	want := []TokenKind{TokenRegexp, TokenOperator, TokenInteger, TokenOperator, TokenInteger, TokenEOF}
	got := make([]TokenKind, len(ts))
	for i := range ts {
		got[i] = ts[i].Kind
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	r, ok := ts[0].Value.(RegexpLiteral)
	if !ok || r.Flags != "gi" || r.Pattern.String() != `a[b\/]c` {
		t.Fatalf("regexp = %#v", ts[0].Value)
	}
}

func TestLexerOctetsAndMalformedInput(t *testing.T) {
	ts, err := Lex("<% 00 ff, 10 %>")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := ts[0].Value.([]byte)
	if !ok || !reflect.DeepEqual(b, []byte{0, 255, 16}) {
		t.Fatalf("octets = %#v", ts[0].Value)
	}
	for _, src := range []string{"\"unterminated", "/* unterminated", "<% 0 %>", "1e+"} {
		if _, err := Lex(src); err == nil {
			t.Errorf("Lex(%q) succeeded", src)
		}
	}
}
func TestLexerInterpolation(t *testing.T) {
	ts, err := Lex(`@"Hello &name; ${x + 1}"`)
	if err != nil {
		t.Fatal(err)
	}
	if ts[0].Kind != TokenInterpolation {
		t.Fatalf("kind = %v", ts[0].Kind)
	}
	v, ok := ts[0].Value.(InterpolationLiteral)
	if !ok || len(v.Parts) != 4 || v.Parts[1].Expression != "name" || v.Parts[3].Expression != "x + 1" {
		t.Fatalf("interpolation = %#v", ts[0].Value)
	}
}
func TestLexerInterpolationEscapedMarkers(t *testing.T) {
	tokens, err := Lex(`@"first \&.orig and \${value}; &name;"`)
	if err != nil {
		t.Fatal(err)
	}
	literal, ok := tokens[0].Value.(InterpolationLiteral)
	if !ok || len(literal.Parts) != 2 {
		t.Fatalf("interpolation = %#v", tokens[0].Value)
	}
	if got, want := literal.Parts[0].Text.String(), "first &.orig and ${value}; "; got != want {
		t.Fatalf("escaped text = %q, want %q", got, want)
	}
	if literal.Parts[1].Expression != "name" {
		t.Fatalf("expression = %q, want name", literal.Parts[1].Expression)
	}
}

func TestLexerUTF16Columns(t *testing.T) {
	ts, err := Lex("😀 x")
	if err != nil {
		t.Fatal(err)
	}
	if ts[1].Location.Column != 4 {
		t.Fatalf("column = %d, want 4", ts[1].Location.Column)
	}
}

func TestLexerAdjacentStringsWhitespaceAndBoundaries(t *testing.T) {
	source := "\"a\\n\" \n \"\\uD83D\" \"\\uDE00\", \"tail\""
	tokens, err := LexWithStorage("adjacent.tjs", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 4 || tokens[0].Kind != TokenString {
		t.Fatalf("tokens = %#v", tokens)
	}
	value, ok := tokens[0].Value.(UTF16)
	if !ok || !reflect.DeepEqual(value.Units(), []uint16{'a', '\n', 0xd83d, 0xde00}) {
		t.Fatalf("joined string = %#v", tokens[0].Value)
	}
	if tokens[0].Location.Line != 1 || tokens[0].Location.Column != 1 ||
		tokens[0].End.Line != 2 || tokens[0].End.Column != 19 {
		t.Fatalf("joined string locations = %#v - %#v", tokens[0].Location, tokens[0].End)
	}
	if tokens[1].Text != "," || tokens[1].Location.Line != 2 || tokens[1].Location.Column != 19 {
		t.Fatalf("comma token = %#v", tokens[1])
	}

	for _, source := range []string{`'a' /* comment */ 'b'`, `'a' "b"`} {
		tokens, err := Lex(source)
		if err != nil {
			t.Fatalf("Lex(%q): %v", source, err)
		}
		if len(tokens) != 3 || tokens[0].Value.(UTF16).String() != "a" || tokens[1].Value.(UTF16).String() != "b" {
			t.Errorf("Lex(%q) = %#v; wanted two distinct string tokens", source, tokens)
		}
	}
	tokens, err = Lex(`'a''b'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 || tokens[0].Value.(UTF16).String() != "ab" {
		t.Fatalf("unspaced adjacent strings = %#v", tokens)
	}
}
