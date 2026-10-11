package tjs

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/eslzzyl/krkrgo/internal/core"
)

// TokenKind identifies the lexical class of a TJS token.
type TokenKind uint8

const (
	TokenEOF TokenKind = iota
	TokenIdentifier
	TokenKeyword
	TokenInteger
	TokenReal
	TokenString
	TokenOctet
	TokenRegexp
	TokenOperator
	TokenPunctuation
	TokenInterpolation
)

// Short aliases make the API convenient for parsers which use the customary
// names.  A token's Text is always the original source spelling.
const (
	TokenEOFKind           = TokenEOF
	TokenIdentifierKind    = TokenIdentifier
	TokenKeywordKind       = TokenKeyword
	TokenIntegerKind       = TokenInteger
	TokenRealKind          = TokenReal
	TokenStringKind        = TokenString
	TokenOctetKind         = TokenOctet
	TokenRegexpKind        = TokenRegexp
	TokenOperatorKind      = TokenOperator
	TokenPunctuationKind   = TokenPunctuation
	TokenInterpolationKind = TokenInterpolation
	TokenRegex             = TokenRegexp
	TokenNumber            = TokenReal
)

func (k TokenKind) String() string {
	switch k {
	case TokenEOF:
		return "eof"
	case TokenIdentifier:
		return "identifier"
	case TokenKeyword:
		return "keyword"
	case TokenInteger:
		return "integer"
	case TokenReal:
		return "real"
	case TokenString:
		return "string"
	case TokenInterpolation:
		return "interpolation"
	case TokenOctet:
		return "octet"
	case TokenRegexp:
		return "regexp"
	case TokenOperator:
		return "operator"
	case TokenPunctuation:
		return "punctuation"
	default:
		return "unknown"
	}
}

// RegexpLiteral is the decoded form of a /pattern/flags token.
type RegexpLiteral struct {
	Pattern UTF16
	Flags   string
}

// InterpolationLiteral is the lossless representation of TJS's embeddable
// string syntax (@\"text &name; ${expression}\"). Text parts retain escapes;
// Expression parts contain the source between &...; or ${...}.
type InterpolationPart struct {
	Text       UTF16
	Expression string
}
type InterpolationLiteral struct{ Parts []InterpolationPart }

// Token is one lexical item. Value is an int64, float64, UTF16, []byte, or
// RegexpLiteral for literal tokens and is nil for names and punctuation.
type Token struct {
	Kind     TokenKind
	Text     string
	Value    any
	Location core.SourceLocation
	End      core.SourceLocation
}

func (t Token) Is(k TokenKind) bool { return t.Kind == k }
func (t Token) String() string {
	if t.Kind == TokenEOF {
		return "<eof>"
	}
	return t.Text
}

// Lexer scans a UTF-16 TJS source buffer. The scanner retains code units so
// source columns and escaped lone surrogates have the same behavior as TJS.
type Lexer struct {
	units        []uint16
	storage      string
	i, line, col uint64
	previous     TokenKind
	previousText string
	err          error
}

func NewLexer(source string) *Lexer { return NewLexerWithStorage("", source) }
func NewLexerWithStorage(storage, source string) *Lexer {
	return &Lexer{units: []uint16(UTF16FromString(source)), storage: storage, line: 1, col: 1}
}
func NewLexerUTF16(units []uint16) *Lexer { return NewLexerUTF16WithStorage("", units) }
func NewLexerUTF16WithStorage(storage string, units []uint16) *Lexer {
	return &Lexer{units: append([]uint16(nil), units...), storage: storage, line: 1, col: 1}
}

func (l *Lexer) Location() core.SourceLocation {
	return core.SourceLocation{Storage: l.storage, Line: l.line, Column: l.col}
}
func (l *Lexer) Done() bool { return l.i >= uint64(len(l.units)) }

func (l *Lexer) peek(off uint64) uint16 {
	p := l.i + off
	if p >= uint64(len(l.units)) {
		return 0
	}
	return l.units[p]
}
func (l *Lexer) at(p uint64) uint16 {
	if p >= uint64(len(l.units)) {
		return 0
	}
	return l.units[p]
}
func (l *Lexer) advance() uint16 {
	if l.i >= uint64(len(l.units)) {
		return 0
	}
	u := l.units[l.i]
	l.i++
	if u == '\r' {
		if l.peek(0) == '\n' {
			l.i++
		}
		l.line++
		l.col = 1
		return u
	}
	if u == '\n' {
		l.line++
		l.col = 1
		return u
	}
	l.col++
	return u
}
func (l *Lexer) runeAt(p uint64) (rune, uint64) {
	if p >= uint64(len(l.units)) {
		return 0, 0
	}
	u := l.units[p]
	if utf16.IsSurrogate(rune(u)) {
		if u >= 0xd800 && u <= 0xdbff && p+1 < uint64(len(l.units)) && l.units[p+1] >= 0xdc00 && l.units[p+1] <= 0xdfff {
			return utf16.DecodeRune(rune(u), rune(l.units[p+1])), 2
		}
		return unicode.ReplacementChar, 1
	}
	return rune(u), 1
}
func (l *Lexer) text(start, end uint64) string { return UTF16(l.units[start:end]).String() }
func (l *Lexer) token(k TokenKind, start uint64, value any, loc core.SourceLocation) Token {
	return Token{Kind: k, Text: l.text(start, l.i), Value: value, Location: loc, End: l.Location()}
}

var keywords = map[string]bool{
	"new": true, "delete": true, "typeof": true, "instanceof": true, "incontextof": true, "void": true,
	"this": true, "super": true, "global": true, "class": true, "extends": true, "function": true,
	"property": true, "private": true, "public": true, "protected": true, "static": true, "return": true,
	"break": true, "continue": true, "export": true, "import": true, "switch": true, "case": true,
	"default": true, "for": true, "while": true, "do": true, "if": true, "var": true, "const": true,
	"enum": true, "goto": true, "throw": true, "try": true, "catch": true, "finally": true, "debugger": true, "in": true, "setter": true, "getter": true,
	"else": true, "synchronized": true, "with": true, "int": true, "real": true, "string": true, "octet": true,
	"false": true, "null": true, "true": true, "NaN": true, "Infinity": true, "isvalid": true, "invalidate": true,
}
var regexAfter = map[string]bool{"return": true, "throw": true, "case": true, "delete": true, "typeof": true, "void": true, "new": true, "in": true, "instanceof": true, "incontextof": true, "else": true, "do": true}

func lexerIDStart(r rune) bool { return r == '_' || r == '$' || unicode.IsLetter(r) || r >= utf8Self }
func lexerIDContinue(r rune) bool {
	return lexerIDStart(r) || unicode.IsDigit(r) || r == '\u200c' || r == '\u200d'
}

const utf8Self rune = 0x80

func isSpace(u uint16) bool {
	return u == ' ' || u == '\t' || u == '\v' || u == '\f' || u == '\u00a0' || u == '\ufeff'
}
func isHex(u uint16) bool {
	return (u >= '0' && u <= '9') || (u >= 'a' && u <= 'f') || (u >= 'A' && u <= 'F')
}
func hexVal(u uint16) int {
	if u >= '0' && u <= '9' {
		return int(u - '0')
	}
	if u >= 'a' && u <= 'f' {
		return int(u-'a') + 10
	}
	return int(u-'A') + 10
}
func isDigitForBase(u uint16, base int) bool {
	if base <= 10 {
		return u >= '0' && u <= '0'+uint16(base-1)
	}
	return (u >= '0' && u <= '9') || (u >= 'a' && u <= 'a'+uint16(base-11)) || (u >= 'A' && u <= 'A'+uint16(base-11))
}

func (l *Lexer) skipSpace() error {
	for l.i < uint64(len(l.units)) {
		u := l.peek(0)
		if isSpace(u) || u == '\r' || u == '\n' {
			l.advance()
			continue
		}
		if u == '/' && l.peek(1) == '/' {
			l.advance()
			l.advance()
			for l.i < uint64(len(l.units)) && l.peek(0) != '\r' && l.peek(0) != '\n' {
				l.advance()
			}
			continue
		}
		if u == '/' && l.peek(1) == '*' {
			loc := l.Location()
			l.advance()
			l.advance()
			level := 1
			for l.i < uint64(len(l.units)) && level > 0 {
				if l.peek(0) == '/' && l.peek(1) == '*' {
					l.advance()
					l.advance()
					level++
					continue
				}
				if l.peek(0) == '*' && l.peek(1) == '/' {
					l.advance()
					l.advance()
					level--
					continue
				}
				l.advance()
			}
			if level != 0 {
				return l.failAt(loc, "unterminated block comment")
			}
			continue
		}
		break
	}
	return nil
}
func (l *Lexer) failAt(loc core.SourceLocation, msg string) error {
	return fmt.Errorf("%s:%d:%d: %s", loc.Storage, loc.Line, loc.Column, msg)
}
func (l *Lexer) fail(msg string) error { return l.failAt(l.Location(), msg) }

// Next returns and consumes the next token. Whitespace and comments are not
// emitted. Once an error is returned, subsequent calls return that error.
func (l *Lexer) Next() (Token, error) {
	if l.err != nil {
		return Token{}, l.err
	}
	if e := l.skipSpace(); e != nil {
		l.err = e
		return Token{}, e
	}
	if l.i >= uint64(len(l.units)) {
		t := Token{Kind: TokenEOF, Location: l.Location(), End: l.Location()}
		l.previous = TokenEOF
		return t, nil
	}
	start := l.i
	loc := l.Location()
	u := l.peek(0)
	if u == '@' && (l.peek(1) == '\'' || l.peek(1) == '"') {
		t, e := l.scanInterpolation(start, loc)
		if e != nil {
			l.err = e
			return Token{}, e
		}
		l.previous = t.Kind
		l.previousText = t.Text
		return t, nil
	}
	if u == '\'' || u == '"' {
		t, e := l.scanString(start, loc, u)
		if e != nil {
			l.err = e
			return Token{}, e
		}
		value := t.Value.(UTF16)
		// Comments end adjacency; only whitespace may separate matching quotes.
		for {
			saveI, saveLine, saveCol := l.i, l.line, l.col
			for l.i < uint64(len(l.units)) {
				space := l.peek(0)
				if !isSpace(space) && space != '\r' && space != '\n' {
					break
				}
				l.advance()
			}
			if l.peek(0) != u {
				l.i, l.line, l.col = saveI, saveLine, saveCol
				break
			}
			next, e2 := l.scanString(l.i, l.Location(), u)
			if e2 != nil {
				l.err = e2
				return Token{}, e2
			}
			value = append(value, next.Value.(UTF16)...)
			t.Value = value
			t.Text = l.text(start, l.i)
			t.End = l.Location()
		}
		l.previous = t.Kind
		l.previousText = t.Text
		return t, nil
	}
	if u == '<' && l.peek(1) == '%' {
		t, e := l.scanOctet(start, loc)
		if e != nil {
			l.err = e
			return Token{}, e
		}
		l.previous = t.Kind
		l.previousText = t.Text
		return t, nil
	}
	if u >= '0' && u <= '9' || (u == '.' && l.peek(1) >= '0' && l.peek(1) <= '9') {
		t, e := l.scanNumber(start, loc)
		if e != nil {
			l.err = e
			return Token{}, e
		}
		l.previous = t.Kind
		l.previousText = t.Text
		return t, nil
	}
	if r, w := l.runeAt(l.i); w > 0 && lexerIDStart(r) {
		for l.i < uint64(len(l.units)) {
			r, w = l.runeAt(l.i)
			if w == 0 || !lexerIDContinue(r) {
				break
			}
			for z := uint64(0); z < w; z++ {
				l.advance()
			}
		}
		text := l.text(start, l.i)
		k := TokenIdentifier
		if keywords[text] {
			k = TokenKeyword
		}
		t := l.token(k, start, nil, loc)
		l.previous = k
		l.previousText = text
		return t, nil
	}
	if u == '/' && l.peek(1) != '/' && l.peek(1) != '*' && l.regexContext() {
		t, e := l.scanRegexp(start, loc)
		if e != nil {
			l.err = e
			return Token{}, e
		}
		l.previous = t.Kind
		l.previousText = t.Text
		return t, nil
	}
	ops := []string{"===", "!==", ">>>=", "<<=", ">>=", "<->", "&&=", "||=", "+=", "-=", "*=", "/=", "%=", "\\=", "|=", "&=", "^=", "=>", "==", "!=", "<=", ">=", "<<", ">>", ">>>", "&&", "||", "++", "--", "->", "..."}
	for _, op := range ops {
		if l.match(op) {
			for range op {
				l.advance()
			}
			k := TokenOperator
			if strings.ContainsAny(op, "()[]{};,.:?") {
				k = TokenPunctuation
			}
			t := l.token(k, start, nil, loc)
			l.previous = k
			l.previousText = op
			return t, nil
		}
	}
	if strings.ContainsRune("()[]{};,.:", rune(u)) {
		l.advance()
		t := l.token(TokenPunctuation, start, nil, loc)
		l.previous = TokenPunctuation
		l.previousText = t.Text
		return t, nil
	}
	if strings.ContainsRune("=+-*/%\\!~&|^<>?", rune(u)) {
		l.advance()
		t := l.token(TokenOperator, start, nil, loc)
		l.previous = TokenOperator
		l.previousText = t.Text
		return t, nil
	}
	return Token{}, l.setError(l.fail("invalid character"))
}
func (l *Lexer) setError(e error) error { l.err = e; return e }
func (l *Lexer) match(s string) bool {
	us := UTF16FromString(s)
	if uint64(len(us)) > uint64(len(l.units))-l.i {
		return false
	}
	for j, u := range us {
		if l.peek(uint64(j)) != u {
			return false
		}
	}
	return true
}
func (l *Lexer) regexContext() bool {
	if l.previous == TokenEOF {
		return true
	}
	switch l.previous {
	case TokenIdentifier, TokenKeyword, TokenInteger, TokenReal, TokenString, TokenOctet, TokenRegexp:
		return regexAfter[l.previousText]
	}
	if l.previous == TokenPunctuation && (l.previousText == ")" || l.previousText == "]" || l.previousText == "}") {
		return false
	}
	return true
}

func (l *Lexer) scanString(start uint64, loc core.SourceLocation, quote uint16) (Token, error) {
	l.advance()
	out := make([]uint16, 0, 16)
	for l.i < uint64(len(l.units)) {
		u := l.peek(0)
		if u == quote {
			l.advance()
			return l.token(TokenString, start, NewUTF16(out), loc), nil
		}
		if u == '\r' || u == '\n' {
			return Token{}, l.failAt(loc, "newline in string literal")
		}
		if u != '\\' {
			out = append(out, l.advance())
			continue
		}
		l.advance()
		if l.i >= uint64(len(l.units)) {
			return Token{}, l.failAt(loc, "unterminated string escape")
		}
		e := l.advance()
		switch e {
		case 'a':
			out = append(out, 0x07)
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'v':
			out = append(out, '\v')
		case '0':
			v := 0
			for v < 3 && l.peek(0) >= '0' && l.peek(0) <= '7' {
				v = v*8 + int(l.advance()-'0')
			}
			out = append(out, uint16(v))
		case '\\', '\'', '"':
			out = append(out, e)
		case '\r', '\n':
			// A backslash-newline is a continuation, not string content.
		case 'x':
			v, e2 := l.escapeHex(4)
			if e2 != nil {
				return Token{}, e2
			}
			out = append(out, uint16(v))
		case 'u':
			v, e2 := l.escapeHexExact(4)
			if e2 != nil {
				return Token{}, e2
			}
			out = append(out, uint16(v))
		default:
			out = append(out, e)
		}
	}
	return Token{}, l.failAt(loc, "unterminated string literal")
}
func (l *Lexer) escapeHex(max int) (int, error) {
	if l.i >= uint64(len(l.units)) || !isHex(l.peek(0)) {
		return 0, l.fail("short hexadecimal escape")
	}
	v := 0
	n := 0
	for n < max && isHex(l.peek(0)) {
		v = v*16 + hexVal(l.advance())
		n++
	}
	return v, nil
}
func (l *Lexer) escapeHexExact(n int) (int, error) {
	if uint64(n) > uint64(len(l.units))-l.i {
		return 0, l.fail("short hexadecimal escape")
	}
	v := 0
	for range n {
		if !isHex(l.peek(0)) {
			return 0, l.fail("invalid hexadecimal escape")
		}
		v = v*16 + hexVal(l.advance())
	}
	return v, nil
}
func (l *Lexer) scanInterpolation(start uint64, loc core.SourceLocation) (Token, error) {
	l.advance()
	quote := l.advance()
	parts := make([]InterpolationPart, 0, 3)
	textStart := l.i
	flush := func(end uint64) {
		if end > textStart {
			parts = append(parts, InterpolationPart{Text: NewUTF16(l.units[textStart:end])})
		}
	}
	for l.i < uint64(len(l.units)) {
		u := l.peek(0)
		if u == quote {
			flush(l.i)
			l.advance()
			return l.token(TokenInterpolation, start, InterpolationLiteral{Parts: parts}, loc), nil
		}
		if u == '\\' {
			l.advance()
			if l.i < uint64(len(l.units)) {
				l.advance()
			}
			continue
		}
		if u == '&' {
			flush(l.i)
			l.advance()
			expr := l.i
			for l.i < uint64(len(l.units)) && l.peek(0) != ';' && l.peek(0) != '\r' && l.peek(0) != '\n' {
				l.advance()
			}
			if l.i >= uint64(len(l.units)) || l.peek(0) != ';' {
				return Token{}, l.failAt(loc, "unterminated embedded expression")
			}
			parts = append(parts, InterpolationPart{Expression: l.text(expr, l.i)})
			l.advance()
			textStart = l.i
			continue
		}
		if u == '$' && l.peek(1) == '{' {
			flush(l.i)
			l.advance()
			l.advance()
			expr := l.i
			depth := 1
			for l.i < uint64(len(l.units)) && depth > 0 {
				if l.peek(0) == '{' {
					depth++
				}
				if l.peek(0) == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
				l.advance()
			}
			if depth != 0 {
				return Token{}, l.failAt(loc, "unterminated embedded expression")
			}
			parts = append(parts, InterpolationPart{Expression: l.text(expr, l.i)})
			l.advance()
			textStart = l.i
			continue
		}
		l.advance()
	}
	return Token{}, l.failAt(loc, "unterminated embedded string")
}

func (l *Lexer) scanNumber(start uint64, loc core.SourceLocation) (Token, error) {
	base := 10
	realNum := false
	prefixed := false
	if l.peek(0) == '0' && (l.peek(1) == 'x' || l.peek(1) == 'X' || l.peek(1) == 'b' || l.peek(1) == 'B') {
		prefixed = true
		mark := l.peek(1)
		if mark == 'x' || mark == 'X' {
			base = 16
		} else {
			base = 2
		}
		l.advance()
		l.advance()
		digits := 0
		for isDigitForBase(l.peek(0), base) || l.peek(0) == '_' {
			if l.peek(0) != '_' {
				digits++
			}
			l.advance()
		}
		if l.peek(0) == '.' {
			realNum = true
			l.advance()
			for isDigitForBase(l.peek(0), base) || l.peek(0) == '_' {
				if l.peek(0) != '_' {
					digits++
				}
				l.advance()
			}
		}
		if base == 16 && (l.peek(0) == 'p' || l.peek(0) == 'P') {
			realNum = true
			l.advance()
			if l.peek(0) == '+' || l.peek(0) == '-' {
				l.advance()
			}
			if l.peek(0) < '0' || l.peek(0) > '9' {
				return Token{}, l.failAt(loc, "missing exponent digits")
			}
			for l.peek(0) >= '0' && l.peek(0) <= '9' {
				l.advance()
			}
		}
		if digits == 0 {
			return Token{}, l.failAt(loc, "missing digits in numeric literal")
		}
	} else {
		if l.peek(0) == '0' && l.peek(1) >= '0' && l.peek(1) <= '7' {
			base = 8
			l.advance()
			for l.peek(0) >= '0' && l.peek(0) <= '9' {
				if l.peek(0) >= '8' {
					return Token{}, l.failAt(loc, "invalid octal literal")
				}
				l.advance()
			}
		} else {
			for (l.peek(0) >= '0' && l.peek(0) <= '9') || l.peek(0) == '_' {
				l.advance()
			}
		}
		if l.peek(0) == '.' {
			realNum = true
			l.advance()
			for (l.peek(0) >= '0' && l.peek(0) <= '9') || l.peek(0) == '_' {
				l.advance()
			}
		}
		if l.peek(0) == 'e' || l.peek(0) == 'E' {
			realNum = true
			l.advance()
			if l.peek(0) == '+' || l.peek(0) == '-' {
				l.advance()
			}
			if l.peek(0) < '0' || l.peek(0) > '9' {
				return Token{}, l.failAt(loc, "missing exponent digits")
			}
			for (l.peek(0) >= '0' && l.peek(0) <= '9') || l.peek(0) == '_' {
				l.advance()
			}
		}
	}
	raw := strings.ReplaceAll(l.text(start, l.i), "_", "")
	if realNum {
		v, e := strconv.ParseFloat(raw, 64)
		if e != nil {
			return Token{}, l.failAt(loc, "invalid real literal")
		}
		return l.token(TokenReal, start, v, loc), nil
	}
	if prefixed {
		v, e := strconv.ParseInt(raw[2:], base, 64)
		if e != nil {
			return Token{}, l.failAt(loc, "integer literal out of range")
		}
		return l.token(TokenInteger, start, v, loc), nil
	}
	if base == 8 {
		v, e := strconv.ParseInt(raw, 8, 64)
		if e != nil {
			return Token{}, l.failAt(loc, "integer literal out of range")
		}
		return l.token(TokenInteger, start, v, loc), nil
	}
	v, e := strconv.ParseInt(raw, 10, 64)
	if e != nil {
		return Token{}, l.failAt(loc, "integer literal out of range")
	}
	return l.token(TokenInteger, start, v, loc), nil
}

func (l *Lexer) scanRegexp(start uint64, loc core.SourceLocation) (Token, error) {
	l.advance()
	pat := make([]uint16, 0, 16)
	class := false
	closed := false
	for l.i < uint64(len(l.units)) {
		u := l.peek(0)
		if u == '\r' || u == '\n' {
			break
		}
		if u == '\\' {
			pat = append(pat, l.advance())
			if l.i < uint64(len(l.units)) {
				pat = append(pat, l.advance())
			}
			continue
		}
		if u == '[' {
			class = true
		}
		if u == ']' {
			class = false
		}
		if u == '/' && !class {
			l.advance()
			closed = true
			break
		}
		pat = append(pat, l.advance())
	}
	if !closed {
		return Token{}, l.failAt(loc, "unterminated regular expression")
	}
	fs := make([]byte, 0, 4)
	for l.i < uint64(len(l.units)) {
		u := l.peek(0)
		if (u >= 'a' && u <= 'z') || (u >= 'A' && u <= 'Z') {
			fs = append(fs, byte(l.advance()))
		} else {
			break
		}
	}
	return l.token(TokenRegexp, start, RegexpLiteral{Pattern: NewUTF16(pat), Flags: string(fs)}, loc), nil
}

func (l *Lexer) scanOctet(start uint64, loc core.SourceLocation) (Token, error) {
	l.advance()
	l.advance()
	out := make([]byte, 0, 16)
	nibble := -1
	for l.i < uint64(len(l.units)) {
		u := l.peek(0)
		if u == '%' && l.peek(1) == '>' {
			if nibble >= 0 {
				return Token{}, l.failAt(loc, "odd number of octet digits")
			}
			l.advance()
			l.advance()
			return l.token(TokenOctet, start, append([]byte(nil), out...), loc), nil
		}
		if u == '/' && (l.peek(1) == '/' || l.peek(1) == '*') {
			if e := l.skipSpace(); e != nil {
				return Token{}, e
			}
			continue
		}
		if isSpace(u) || u == '\r' || u == '\n' {
			l.advance()
			continue
		}
		if u == ',' || u == ';' {
			if nibble >= 0 {
				out = append(out, byte(nibble))
				nibble = -1
			}
			l.advance()
			continue
		}
		if !isHex(u) {
			return Token{}, l.failAt(l.Location(), "invalid octet digit")
		}
		v := hexVal(l.advance())
		if nibble < 0 {
			nibble = v
		} else {
			out = append(out, byte(nibble*16+v))
			nibble = -1
		}
	}
	return Token{}, l.failAt(loc, "unterminated octet literal")
}

// LexAll scans the complete source and includes a final EOF token.
func (l *Lexer) LexAll() ([]Token, error) {
	out := make([]Token, 0)
	for {
		t, e := l.Next()
		if e != nil {
			return nil, e
		}
		out = append(out, t)
		if t.Kind == TokenEOF {
			return out, nil
		}
	}
}
func Lex(source string) ([]Token, error) { return NewLexer(source).LexAll() }
func LexWithStorage(storage, source string) ([]Token, error) {
	return NewLexerWithStorage(storage, source).LexAll()
}
