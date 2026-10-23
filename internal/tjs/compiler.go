package tjs

// This file contains the source-to-register compiler.  The instruction format is
// intentionally plain Go data: hosts can persist a Program or inspect it without
// retaining parser state.  The VM is the sole consumer of execution semantics.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/eslzzyl/krkrgo/internal/core"
)

type OpCode string

const (
	OpParameter     OpCode = "parameter"
	OpDeleteName    OpCode = "delete_name"
	OpDeleteMember  OpCode = "delete_member"
	OpConstant      OpCode = "constant"
	OpLoad          OpCode = "load"
	OpStore         OpCode = "store"
	OpDeclare       OpCode = "declare"
	OpMove          OpCode = "move"
	OpUnary         OpCode = "unary"
	OpBinary        OpCode = "binary"
	OpGet           OpCode = "get"
	OpTypeOfMember  OpCode = "typeof_member"
	OpEnsureMember  OpCode = "ensure_member"
	OpSet           OpCode = "set"
	OpArray         OpCode = "array"
	OpDictionary    OpCode = "dictionary"
	OpFunction      OpCode = "function"
	OpCall          OpCode = "call"
	OpConstruct     OpCode = "construct"
	OpClass         OpCode = "class"
	OpProperty      OpCode = "property"
	OpReadProperty  OpCode = "read_property"
	OpWriteProperty OpCode = "write_property"
	OpEval          OpCode = "eval"
	OpJump          OpCode = "jump"
	OpJumpUnless    OpCode = "jump_unless"
	OpEnterScope    OpCode = "enter_scope"
	OpLeaveScope    OpCode = "leave_scope"
	OpEnterWith     OpCode = "enter_with"
	OpLeaveWith     OpCode = "leave_with"
	OpWith          OpCode = "with"
	OpTry           OpCode = "try"
	OpEndTry        OpCode = "end_try"
	OpEndFinally    OpCode = "end_finally"
	OpUnwind        OpCode = "unwind"
	OpThrow         OpCode = "throw"
	OpReturn        OpCode = "return"
)

type ArgumentKind uint8

const (
	ArgumentValue ArgumentKind = iota
	ArgumentSpread
	ArgumentForward
	ArgumentForwardFrom
)

type Argument struct {
	Kind     ArgumentKind `json:"kind"`
	Register int          `json:"register,omitempty"`
	From     int          `json:"from,omitempty"`
}
type DictionaryOperand struct {
	Key   int `json:"key"`
	Value int `json:"value"`
}

// Op is a tagged, serializable instruction.  Unused fields are left at zero;
// keeping one concrete struct makes encoding/json and compatibility tooling
// considerably less surprising than an interface containing many variants.
type Op struct {
	StaticKey    bool   `json:"static_key,omitempty"`
	Code         OpCode `json:"code"`
	Out          int    `json:"out,omitempty"`
	Input        int    `json:"input,omitempty"`
	Object       int    `json:"object,omitempty"`
	Key          int    `json:"key,omitempty"`
	Left         int    `json:"left,omitempty"`
	Right        int    `json:"right,omitempty"`
	Callee       int    `json:"callee,omitempty"`
	Context      int    `json:"context,omitempty"`
	Target       int    `json:"target,omitempty"`
	Exception    int    `json:"exception,omitempty"`
	Scopes       int    `json:"scopes,omitempty"`
	Handlers     int    `json:"handlers,omitempty"`
	Withs        int    `json:"withs,omitempty"`
	Finally      int    `json:"finally,omitempty"`
	ContextSet   bool   `json:"context_set,omitempty"`
	TargetSet    bool   `json:"target_set,omitempty"`
	Name         string `json:"name,omitempty"`
	Operator     string `json:"operator,omitempty"`
	Optional     bool   `json:"optional,omitempty"`
	Raw          bool   `json:"raw,omitempty"`
	ResultNeeded bool   `json:"result_needed,omitempty"`
	Rest         bool   `json:"rest,omitempty"`
	Index        int    `json:"index,omitempty"`
	Const        int32  `json:"const,omitempty"`
	extra        *opExtra
}

type opExtra struct {
	items    []int
	pairs    []DictionaryOperand
	args     []Argument
	function *Function
	class    *Class
	property *Property
}

func (o Op) items() []int {
	if o.extra == nil {
		return nil
	}
	return o.extra.items
}

func (o Op) pairs() []DictionaryOperand {
	if o.extra == nil {
		return nil
	}
	return o.extra.pairs
}

func (o Op) args() []Argument {
	if o.extra == nil {
		return nil
	}
	return o.extra.args
}

func (o Op) function() *Function {
	if o.extra == nil {
		return nil
	}
	return o.extra.function
}

func (o Op) class() *Class {
	if o.extra == nil {
		return nil
	}
	return o.extra.class
}

func (o Op) property() *Property {
	if o.extra == nil {
		return nil
	}
	return o.extra.property
}

// FunctionOperand, ClassOperand, and PropertyOperand expose nested descriptors
// to runtime helpers without restoring their per-instruction storage cost.
func (o Op) FunctionOperand() *Function { return o.function() }
func (o Op) ClassOperand() *Class       { return o.class() }
func (o Op) PropertyOperand() *Property { return o.property() }

type Instruction struct {
	Location core.SourceLocation `json:"location"`
	Op       Op                  `json:"op"`
}
type Program struct {
	Storage   string        `json:"storage"`
	Registers int           `json:"registers"`
	Constants []Value       `json:"constants,omitempty"`
	Code      []Instruction `json:"code"`
}

// Validate checks structural limits, register operands, and branch targets before execution.
func (p Program) Validate() error {
	if p.Registers < 0 || p.Registers > 1000000 {
		return fmt.Errorf("invalid TJS register count")
	}
	if len(p.Code) > 1000000 {
		return fmt.Errorf("TJS program exceeds instruction limit")
	}
	invalid := func(i int, format string, args ...any) error {
		in := p.Code[i]
		prefix := fmt.Sprintf("%s:%d:%d: ", in.Location.Storage, in.Location.Line, in.Location.Column)
		return fmt.Errorf(prefix+format, args...)
	}
	register := func(i, r int, what string) error {
		if r < 0 || r >= p.Registers {
			return invalid(i, "invalid %s register %d", what, r)
		}
		return nil
	}
	for i, in := range p.Code {
		op := in.Op
		if op.Code == OpFunction && op.function() == nil {
			return invalid(i, "nil function descriptor")
		}
		if op.Code == OpClass && op.class() == nil {
			return invalid(i, "nil class descriptor")
		}
		if op.Code == OpProperty && op.property() == nil {
			return invalid(i, "nil property descriptor")
		}
		if (op.Code == OpJump || op.Code == OpJumpUnless) && (op.Target < 0 || op.Target > len(p.Code)) {
			return invalid(i, "invalid TJS branch target %d", op.Target)
		}
		if op.Code == OpTry {
			if op.Target < -1 || op.Target > len(p.Code) || op.Finally < -1 || op.Finally > len(p.Code) {
				return invalid(i, "invalid TJS try target")
			}
		}
		if op.Code == OpUnwind && op.Operator == "jump" && (op.Target < 0 || op.Target > len(p.Code)) {
			return invalid(i, "invalid TJS unwind target %d", op.Target)
		}
		check := func(r int, what string) error { return register(i, r, what) }
		if op.Code == OpClass {
			for _, r := range op.items() {
				if e := check(r, "class base"); e != nil {
					return e
				}
			}
		}
		switch op.Code {
		case OpConstant, OpLoad, OpDeleteName, OpFunction, OpClass, OpProperty, OpWith:
			if e := check(op.Out, "output"); e != nil {
				return e
			}
		case OpStore, OpDeclare:
			if e := check(op.Input, "input"); e != nil {
				return e
			}
		case OpMove, OpUnary, OpReadProperty:
			if e := check(op.Out, "output"); e != nil {
				return e
			}
			if e := check(op.Input, "input"); e != nil {
				return e
			}
		case OpBinary:
			if e := check(op.Out, "output"); e != nil {
				return e
			}
			if e := check(op.Left, "left"); e != nil {
				return e
			}
			if e := check(op.Right, "right"); e != nil {
				return e
			}
		case OpGet, OpTypeOfMember:
			if e := check(op.Out, "output"); e != nil {
				return e
			}
			if e := check(op.Object, "object"); e != nil {
				return e
			}
			if e := check(op.Key, "key"); e != nil {
				return e
			}
		case OpEnsureMember:
			if e := check(op.Object, "object"); e != nil {
				return e
			}
			if e := check(op.Key, "key"); e != nil {
				return e
			}
		case OpSet:
			if e := check(op.Object, "object"); e != nil {
				return e
			}
			if e := check(op.Key, "key"); e != nil {
				return e
			}
			if e := check(op.Input, "input"); e != nil {
				return e
			}
		case OpArray:
			if e := check(op.Out, "output"); e != nil {
				return e
			}
			for _, r := range op.items() {
				if e := check(r, "array item"); e != nil {
					return e
				}
			}
		case OpDictionary:
			if e := check(op.Out, "output"); e != nil {
				return e
			}
			for _, pair := range op.pairs() {
				if e := check(pair.Key, "dictionary key"); e != nil {
					return e
				}
				if e := check(pair.Value, "dictionary value"); e != nil {
					return e
				}
			}
		case OpCall, OpConstruct:
			if e := check(op.Out, "output"); e != nil {
				return e
			}
			if e := check(op.Callee, "callee"); e != nil {
				return e
			}
			if op.ContextSet {
				if e := check(op.Context, "context"); e != nil {
					return e
				}
			}
			if op.TargetSet {
				if op.Code != OpConstruct {
					return invalid(i, "assignment target is only valid for construction")
				}
				if e := check(op.Object, "construction target object"); e != nil {
					return e
				}
				if e := check(op.Key, "construction target key"); e != nil {
					return e
				}
			}
			for _, arg := range op.args() {
				switch arg.Kind {
				case ArgumentValue:
					if e := check(arg.Register, "argument"); e != nil {
						return e
					}
				case ArgumentSpread:
					if e := check(arg.Register, "spread"); e != nil {
						return e
					}
				case ArgumentForward:
				case ArgumentForwardFrom:
					if arg.From < 0 {
						return invalid(i, "invalid forwarded argument offset %d", arg.From)
					}
				default:
					return invalid(i, "invalid argument kind %d", arg.Kind)
				}
			}
		case OpWriteProperty:
			if e := check(op.Context, "context"); e != nil {
				return e
			}
			if e := check(op.Input, "input"); e != nil {
				return e
			}
		case OpEval, OpJumpUnless, OpThrow, OpReturn:
			if e := check(op.Input, "input"); e != nil {
				return e
			}
		}
	}
	return nil
}

// --- scanner -----------------------------------------------------------------
type ctoken struct {
	kind, text    string
	value         Value
	regexp        *RegexpLiteral
	interpolation *InterpolationLiteral
	controlClose  bool
	loc           core.SourceLocation
}
type cscan struct {
	src, storage string
	i            int
	line, col    uint64
	out          []ctoken
	parenRegex   []bool
}

func (s *cscan) advance() rune {
	if s.i >= len(s.src) {
		return 0
	}
	r, n := utf8.DecodeRuneInString(s.src[s.i:])
	s.i += n
	if r == '\n' {
		s.line++
		s.col = 1
	} else {
		s.col += uint64(utf16.RuneLen(r))
	}
	return r
}
func (s *cscan) loc() core.SourceLocation {
	return core.SourceLocation{Storage: s.storage, Line: s.line, Column: s.col}
}
func (s *cscan) add(k, t string, v Value, l core.SourceLocation) {
	token := ctoken{kind: k, text: t, value: v, loc: l}
	if k == "symbol" {
		switch t {
		case "(":
			control := false
			if len(s.out) > 0 {
				previous := s.out[len(s.out)-1]
				if previous.kind == "name" {
					switch previous.text {
					case "if", "while", "for", "with", "switch", "catch":
						control = true
					}
				}
			}
			s.parenRegex = append(s.parenRegex, control)
		case ")":
			if n := len(s.parenRegex); n > 0 {
				token.controlClose = s.parenRegex[n-1]
				s.parenRegex = s.parenRegex[:n-1]
			}
		}
	}
	s.out = append(s.out, token)
}
func isIDStart(r rune) bool { return r == '_' || unicode.IsLetter(r) || r >= utf8.RuneSelf }
func isID(r rune) bool      { return isIDStart(r) || unicode.IsDigit(r) }
func appendUTF16Rune(out UTF16, r rune) UTF16 {
	if r <= 0xffff {
		return append(out, uint16(r))
	}
	r -= 0x10000
	return append(out, uint16(0xd800+(r>>10)), uint16(0xdc00+(r&0x3ff)))
}
func (s *cscan) scanString(quote rune, l core.SourceLocation) (UTF16, error) {
	out := make(UTF16, 0, 16)
	for s.i < len(s.src) {
		r := s.advance()
		if r == quote {
			return out, nil
		}
		if r != '\\' {
			out = appendUTF16Rune(out, r)
			continue
		}
		if s.i >= len(s.src) {
			break
		}
		e := s.advance()
		switch e {
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
		case '\\', '\'', '"':
			out = appendUTF16Rune(out, e)
		case 'x':
			if s.i+2 > len(s.src) {
				return nil, fmt.Errorf("short hex escape")
			}
			x := s.src[s.i : s.i+2]
			s.i += 2
			s.col += 2
			n, e2 := strconv.ParseUint(x, 16, 8)
			if e2 != nil {
				return nil, fmt.Errorf("invalid hex escape")
			}
			out = append(out, uint16(n))
		case 'u':
			if s.i+4 > len(s.src) {
				return nil, fmt.Errorf("short unicode escape")
			}
			x := s.src[s.i : s.i+4]
			s.i += 4
			s.col += 4
			n, e2 := strconv.ParseUint(x, 16, 16)
			if e2 != nil {
				return nil, fmt.Errorf("invalid unicode escape")
			}
			out = append(out, uint16(n))
		default:
			out = appendUTF16Rune(out, e)
		}
	}
	return nil, fmt.Errorf("unterminated string at %s:%d:%d", l.Storage, l.Line, l.Column)
}
func cscanOctetHex(r rune) (byte, bool) {
	switch {
	case r >= '0' && r <= '9':
		return byte(r - '0'), true
	case r >= 'a' && r <= 'f':
		return byte(r-'a') + 10, true
	case r >= 'A' && r <= 'F':
		return byte(r-'A') + 10, true
	default:
		return 0, false
	}
}

func (s *cscan) scanOctet(l core.SourceLocation) (Value, error) {
	s.advance()
	s.advance()
	out := make([]byte, 0, 16)
	nibble := byte(0)
	haveNibble := false
	for s.i < len(s.src) {
		r, _ := utf8.DecodeRuneInString(s.src[s.i:])
		if r == '%' && s.i+1 < len(s.src) && s.src[s.i+1] == '>' {
			if haveNibble {
				return Value{}, fmt.Errorf("odd number of octet digits at %s:%d:%d", l.Storage, l.Line, l.Column)
			}
			s.advance()
			s.advance()
			return OctetValue(out), nil
		}
		if r == '/' && s.i+1 < len(s.src) && (s.src[s.i+1] == '/' || s.src[s.i+1] == '*') {
			if s.src[s.i+1] == '/' {
				s.advance()
				s.advance()
				for s.i < len(s.src) && s.src[s.i] != '\n' {
					s.advance()
				}
			} else {
				comment := s.loc()
				s.advance()
				s.advance()
				closed := false
				for s.i < len(s.src) {
					if s.src[s.i] == '*' && s.i+1 < len(s.src) && s.src[s.i+1] == '/' {
						s.advance()
						s.advance()
						closed = true
						break
					}
					s.advance()
				}
				if !closed {
					return Value{}, fmt.Errorf("unterminated comment at %s:%d:%d", comment.Storage, comment.Line, comment.Column)
				}
			}
			continue
		}
		if unicode.IsSpace(r) {
			s.advance()
			continue
		}
		if r == ',' || r == ';' {
			if haveNibble {
				return Value{}, fmt.Errorf("odd number of octet digits at %s:%d:%d", l.Storage, l.Line, l.Column)
			}
			s.advance()
			continue
		}
		n, ok := cscanOctetHex(r)
		if !ok {
			return Value{}, fmt.Errorf("invalid octet digit at %s:%d:%d", s.storage, s.line, s.col)
		}
		s.advance()
		if haveNibble {
			out = append(out, nibble<<4|n)
			haveNibble = false
		} else {
			nibble = n
			haveNibble = true
		}
	}
	return Value{}, fmt.Errorf("unterminated octet literal at %s:%d:%d", l.Storage, l.Line, l.Column)
}

func (s *cscan) regexContext() bool {
	if len(s.out) == 0 {
		return true
	}
	prev := s.out[len(s.out)-1]
	switch prev.kind {
	case "name":
		switch prev.text {
		case "int", "real", "string", "isvalid", "invalidate":
			return true
		default:
			return regexAfter[prev.text]
		}
	case "literal", "regexp":
		return false
	case "symbol":
		if prev.text == ")" {
			return prev.controlClose
		}
	}
	return prev.text != "]" && prev.text != "}"
}

func (s *cscan) scanRegexp(l core.SourceLocation) (RegexpLiteral, error) {
	s.advance()
	pattern := make(UTF16, 0, 16)
	inClass := false
	closed := false
	for s.i < len(s.src) {
		r, _ := utf8.DecodeRuneInString(s.src[s.i:])
		if r == '\r' || r == '\n' {
			break
		}
		if r == '\\' {
			pattern = appendUTF16Rune(pattern, s.advance())
			if s.i < len(s.src) {
				pattern = appendUTF16Rune(pattern, s.advance())
			}
			continue
		}
		if r == '[' {
			inClass = true
		} else if r == ']' {
			inClass = false
		} else if r == '/' && !inClass {
			s.advance()
			closed = true
			break
		}
		pattern = appendUTF16Rune(pattern, s.advance())
	}
	if !closed {
		return RegexpLiteral{}, fmt.Errorf("unterminated regular expression at %s:%d:%d", l.Storage, l.Line, l.Column)
	}
	flags := make([]byte, 0, 4)
	for s.i < len(s.src) {
		r := s.src[s.i]
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			break
		}
		flags = append(flags, byte(s.advance()))
	}
	return RegexpLiteral{Pattern: NewUTF16(pattern), Flags: string(flags)}, nil
}

func (s *cscan) scanNumber(loc core.SourceLocation) (string, Value, error) {
	start := s.i
	base := 10
	realNum := false
	prefixed := false

	if s.src[s.i] == '0' && s.i+1 < len(s.src) {
		marker := s.src[s.i+1]
		if marker == 'x' || marker == 'X' || marker == 'b' || marker == 'B' {
			prefixed = true
			if marker == 'x' || marker == 'X' {
				base = 16
			} else {
				base = 2
			}
			s.advance()
			s.advance()
			digits := 0
			for s.i < len(s.src) && (isDigitForBase(uint16(s.src[s.i]), base) || s.src[s.i] == '_') {
				if s.src[s.i] != '_' {
					digits++
				}
				s.advance()
			}
			if s.i < len(s.src) && s.src[s.i] == '.' {
				realNum = true
				s.advance()
				for s.i < len(s.src) && (isDigitForBase(uint16(s.src[s.i]), base) || s.src[s.i] == '_') {
					if s.src[s.i] != '_' {
						digits++
					}
					s.advance()
				}
			}
			if base == 16 && s.i < len(s.src) && (s.src[s.i] == 'p' || s.src[s.i] == 'P') {
				realNum = true
				s.advance()
				if s.i < len(s.src) && (s.src[s.i] == '+' || s.src[s.i] == '-') {
					s.advance()
				}
				if s.i >= len(s.src) || s.src[s.i] < '0' || s.src[s.i] > '9' {
					return "", Value{}, fmt.Errorf("%s:%d:%d: missing exponent digits", loc.Storage, loc.Line, loc.Column)
				}
				for s.i < len(s.src) && s.src[s.i] >= '0' && s.src[s.i] <= '9' {
					s.advance()
				}
			}
			if digits == 0 {
				return "", Value{}, fmt.Errorf("%s:%d:%d: missing digits in numeric literal", loc.Storage, loc.Line, loc.Column)
			}
		}
	}

	if !prefixed {
		if s.src[s.i] == '0' && s.i+1 < len(s.src) && s.src[s.i+1] >= '0' && s.src[s.i+1] <= '7' {
			base = 8
			s.advance()
			for s.i < len(s.src) && s.src[s.i] >= '0' && s.src[s.i] <= '9' {
				if s.src[s.i] >= '8' {
					return "", Value{}, fmt.Errorf("%s:%d:%d: invalid octal literal", loc.Storage, loc.Line, loc.Column)
				}
				s.advance()
			}
		} else {
			for s.i < len(s.src) && (s.src[s.i] >= '0' && s.src[s.i] <= '9' || s.src[s.i] == '_') {
				s.advance()
			}
		}
		if s.i+1 < len(s.src) && s.src[s.i] == '.' && s.src[s.i+1] >= '0' && s.src[s.i+1] <= '9' {
			realNum = true
			s.advance()
			for s.i < len(s.src) && (s.src[s.i] >= '0' && s.src[s.i] <= '9' || s.src[s.i] == '_') {
				s.advance()
			}
		}
		if s.i < len(s.src) && (s.src[s.i] == 'e' || s.src[s.i] == 'E') {
			realNum = true
			s.advance()
			if s.i < len(s.src) && (s.src[s.i] == '+' || s.src[s.i] == '-') {
				s.advance()
			}
			if s.i >= len(s.src) || s.src[s.i] < '0' || s.src[s.i] > '9' {
				return "", Value{}, fmt.Errorf("%s:%d:%d: missing exponent digits", loc.Storage, loc.Line, loc.Column)
			}
			for s.i < len(s.src) && (s.src[s.i] >= '0' && s.src[s.i] <= '9' || s.src[s.i] == '_') {
				s.advance()
			}
		}
	}

	text := s.src[start:s.i]
	raw := strings.ReplaceAll(text, "_", "")
	if realNum {
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return "", Value{}, fmt.Errorf("%s:%d:%d: invalid real literal", loc.Storage, loc.Line, loc.Column)
		}
		return text, RealValue(n), nil
	}
	if prefixed {
		n, err := strconv.ParseInt(raw[2:], base, 64)
		if err != nil {
			return "", Value{}, fmt.Errorf("%s:%d:%d: integer literal out of range", loc.Storage, loc.Line, loc.Column)
		}
		return text, IntegerValue(n), nil
	}
	if base == 8 {
		n, err := strconv.ParseInt(raw, 8, 64)
		if err != nil {
			return "", Value{}, fmt.Errorf("%s:%d:%d: integer literal out of range", loc.Storage, loc.Line, loc.Column)
		}
		return text, IntegerValue(n), nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return "", Value{}, fmt.Errorf("%s:%d:%d: integer literal out of range", loc.Storage, loc.Line, loc.Column)
	}
	return text, IntegerValue(n), nil
}
func (s *cscan) scanInterpolation(loc core.SourceLocation) error {
	lexer := NewLexerWithStorage(s.storage, s.src[s.i:])
	token, err := lexer.Next()
	if err != nil {
		return err
	}
	if token.Kind != TokenInterpolation {
		return fmt.Errorf("%s:%d:%d: expected interpolated string", loc.Storage, loc.Line, loc.Column)
	}
	consumed := 0
	var units uint64
	for consumed < len(s.src)-s.i && units < lexer.i {
		r, size := utf8.DecodeRuneInString(s.src[s.i+consumed:])
		units += uint64(utf16.RuneLen(r))
		consumed += size
	}
	if units != lexer.i {
		return fmt.Errorf("%s:%d:%d: invalid interpolated string length", loc.Storage, loc.Line, loc.Column)
	}
	for consumed > 0 {
		_, size := utf8.DecodeRuneInString(s.src[s.i:])
		s.advance()
		consumed -= size
	}
	interpolation, ok := token.Value.(InterpolationLiteral)
	if !ok {
		return fmt.Errorf("%s:%d:%d: invalid interpolated string token", loc.Storage, loc.Line, loc.Column)
	}
	s.out = append(s.out, ctoken{kind: "interpolation", interpolation: &interpolation, loc: loc})
	return nil
}

func (s *cscan) tokens() ([]ctoken, error) {
	s.line = 1
	s.col = 1
	s.parenRegex = nil
	for s.i < len(s.src) {
		rr, _ := utf8.DecodeRuneInString(s.src[s.i:])
		if unicode.IsSpace(rr) {
			s.advance()
			continue
		}
		r := s.src[s.i]
		l := s.loc()
		if r == '/' && s.i+1 < len(s.src) && s.src[s.i+1] == '/' {
			for s.i < len(s.src) && s.src[s.i] != '\n' {
				s.advance()
			}
			continue
		}
		if r == '/' && s.i+1 < len(s.src) && s.src[s.i+1] == '*' {
			s.advance()
			s.advance()
			ok := false
			for s.i < len(s.src) {
				if s.src[s.i] == '*' && s.i+1 < len(s.src) && s.src[s.i+1] == '/' {
					s.advance()
					s.advance()
					ok = true
					break
				}
				s.advance()
			}
			if !ok {
				return nil, fmt.Errorf("unterminated comment at %s:%d:%d", l.Storage, l.Line, l.Column)
			}
			continue
		}
		if r == '<' && s.i+1 < len(s.src) && s.src[s.i+1] == '%' {
			v, e := s.scanOctet(l)
			if e != nil {
				return nil, e
			}
			s.add("literal", "", v, l)
			continue
		}
		if r == '@' && s.i+1 < len(s.src) && (s.src[s.i+1] == '\'' || s.src[s.i+1] == '"') {
			if err := s.scanInterpolation(l); err != nil {
				return nil, err
			}
			continue
		}

		if r == '\'' || r == '"' {
			q := s.advance()
			value, e := s.scanString(q, l)
			if e != nil {
				return nil, e
			}
			// Comments end adjacency; only whitespace may separate matching quotes.
			for {
				saveI, saveLine, saveCol := s.i, s.line, s.col
				for s.i < len(s.src) {
					space, _ := utf8.DecodeRuneInString(s.src[s.i:])
					if !unicode.IsSpace(space) {
						break
					}
					s.advance()
				}
				nextQuote, _ := utf8.DecodeRuneInString(s.src[s.i:])
				if s.i >= len(s.src) || nextQuote != q {
					s.i, s.line, s.col = saveI, saveLine, saveCol
					break
				}
				nextLocation := s.loc()
				s.advance()
				next, e := s.scanString(q, nextLocation)
				if e != nil {
					return nil, e
				}
				value = append(value, next...)
			}
			s.add("literal", "", StringUnits(value), l)
			continue
		}
		if r == '/' && s.regexContext() {
			regexp, e := s.scanRegexp(l)
			if e != nil {
				return nil, e
			}
			s.out = append(s.out, ctoken{kind: "regexp", regexp: &regexp, loc: l})
			continue
		}
		if isIDStart(rr) {
			start := s.i
			s.advance()
			for s.i < len(s.src) {
				x, _ := utf8.DecodeRuneInString(s.src[s.i:])
				if !isID(x) {
					break
				}
				s.advance()
			}
			s.add("name", s.src[start:s.i], Value{}, l)
			continue
		}
		if r >= '0' && r <= '9' || r == '.' && s.i+1 < len(s.src) && s.src[s.i+1] >= '0' && s.src[s.i+1] <= '9' {
			txt, value, e := s.scanNumber(l)
			if e != nil {
				return nil, e
			}
			s.add("literal", txt, value, l)
			continue
		}
		ops := []string{"<->", "===", "!==", ">>>=", "<<<", "<<=", ">>=", "...", "&&=", "||=", "++", "--", "==", "!=", "<=", ">=", "&&", "||", "<<", ">>>", ">>", "+=", "-=", "*=", "/=", "%=", "\\=", "&=", "|=", "^=", "=>", "%["}
		found := ""
		for _, op := range ops {
			if strings.HasPrefix(s.src[s.i:], op) && len(op) > len(found) {
				found = op
			}
		}
		if found == "" {
			found = string(s.advance())
		} else {
			for range found {
				s.advance()
			}
		}
		s.add("symbol", found, Value{}, l)
	}
	s.out = append(s.out, ctoken{kind: "eof", loc: s.loc()})
	return s.out, nil
}

// --- parser AST --------------------------------------------------------------
type cexpr struct {
	kind, text         string
	prefix             bool
	value              Value
	left, right, third *cexpr
	args               []*cexpr
	pairs              [][2]*cexpr
	entries            []arrayLiteralEntry
	fn                 *Function
	loc                core.SourceLocation
}
type arrayLiteralEntry struct {
	key, value *cexpr
}

type cparameterDefault struct {
	expression *cexpr
	loc        core.SourceLocation
}
type cparser struct {
	ts []ctoken
	p  int
	c  *compiler
}

func (p *cparser) at() ctoken {
	if len(p.ts) == 0 {
		return ctoken{kind: "eof"}
	}
	if p.p < 0 {
		p.p = 0
	}
	if p.p >= len(p.ts) {
		return p.ts[len(p.ts)-1]
	}
	return p.ts[p.p]
}
func (p *cparser) is(x string) bool {
	if x == "," && p.at().text == "=>" {
		return true
	}
	return p.at().text == x
}
func (p *cparser) eat(x string) bool {
	if p.is(x) {
		if p.p < len(p.ts) {
			p.p++
		}
		return true
	}
	return false
}
func (p *cparser) need(x string) error {
	if !p.eat(x) {
		return fmt.Errorf("expected %q, found %q", x, p.at().text)
	}
	return nil
}
func (p *cparser) name() (string, error) {
	t := p.at()
	if t.kind != "name" {
		return "", fmt.Errorf("expected identifier")
	}
	p.p++
	return t.text, nil
}
func (p *cparser) withMember(name string, loc core.SourceLocation) *cexpr {
	object := &cexpr{kind: "name", text: "global", loc: loc}
	if p.c != nil && p.c.withDepth > 0 {
		object = &cexpr{kind: "with", loc: loc}
	}
	return &cexpr{
		kind:  "member",
		left:  object,
		right: &cexpr{kind: "value", value: StringValue(name), loc: loc},
		loc:   loc,
	}
}

func (p *cparser) full() (*cexpr, error) {
	e, err := p.expr(0)
	if err != nil {
		return nil, err
	}
	for p.eat(",") {
		r, er := p.expr(0)
		if er != nil {
			return nil, er
		}
		e = &cexpr{kind: "seq", left: e, right: r, loc: e.loc}
	}
	return e, nil
}

// functionDescriptor consumes an optional function parameter list and body while using
// the parent parser stream. The nested compiler has independent registers and
// control stacks, making recursion and closures safe at execution time.
func (p *cparser) functionDescriptor(name string) (*Function, error) {
	var params []string
	var defaults map[int]cparameterDefault
	rest := ""
	unnamedRest := 0
	if p.eat("(") {
		for !p.eat(")") {
			if p.eat("*") {
				if !p.is(")") {
					return nil, fmt.Errorf("unnamed rest parameter must be last")
				}
				if err := p.need(")"); err != nil {
					return nil, err
				}
				unnamedRest = len(params)
				break
			}
			if len(params) >= 1024 {
				return nil, fmt.Errorf("too many TJS parameters")
			}
			isRest := p.eat("...")
			n, err := p.name()
			if err != nil {
				return nil, err
			}
			if p.eat("*") {
				isRest = true
			}
			if isRest {
				if p.eat("=") {
					return nil, fmt.Errorf("rest parameter cannot have a default value")
				}
				if rest != "" {
					return nil, fmt.Errorf("multiple rest parameters")
				}
				rest = n
				if !p.is(")") {
					return nil, fmt.Errorf("rest parameter must be last")
				}
			} else {
				index := len(params)
				params = append(params, n)
				if p.eat("=") {
					at := p.at().loc
					expression, err := p.expr(0)
					if err != nil {
						return nil, err
					}
					if defaults == nil {
						defaults = make(map[int]cparameterDefault)
					}
					defaults[index] = cparameterDefault{expression: expression, loc: at}
				}
			}
			if !p.eat(",") && !p.is(")") {
				return nil, fmt.Errorf("expected comma in parameter list")
			}
		}
	}
	if err := p.need("{"); err != nil {
		return nil, err
	}
	// withMember reads p.c.withDepth, so point it at the compiler parsing this body.
	activeCompiler := p.c
	sub := &compiler{program: Program{Storage: p.c.program.Storage}, p: p, withDepth: p.c.withDepth, forwardArgsFrom: unnamedRest, functionBody: true}
	p.c = sub
	defer func() { p.c = activeCompiler }()
	for i, n := range params {
		sub.emit(p.at().loc, Op{Code: OpParameter, Name: n, Index: i})
		if def, ok := defaults[i]; ok {
			parameter := sub.reg()
			sub.emit(def.loc, Op{Code: OpLoad, Out: parameter, Name: n})
			voidValue := sub.reg()
			sub.emitValue(def.loc, Op{Code: OpConstant, Out: voidValue}, VoidValue)
			missing := sub.reg()
			sub.emit(def.loc, Op{Code: OpBinary, Out: missing, Operator: "===", Left: parameter, Right: voidValue})
			skipDefault := sub.emit(def.loc, Op{Code: OpJumpUnless, Input: missing})
			value, err := sub.compile(def.expression)
			if err != nil {
				return nil, err
			}
			sub.emit(def.loc, Op{Code: OpStore, Name: n, Input: value})
			sub.patch(skipDefault, len(sub.program.Code))
		}
	}
	if rest != "" {
		sub.emit(p.at().loc, Op{Code: OpParameter, Name: rest, Index: len(params), Rest: true})
	}
	for !p.eat("}") {
		if p.at().kind == "eof" {
			return nil, fmt.Errorf("unterminated function body")
		}
		if err := sub.statement(); err != nil {
			return nil, err
		}
	}
	return &Function{Name: name, Parameters: params, Rest: rest, Members: sub.localFunctions, Properties: sub.localProperties, Program: sub.program}, nil
}

func (p *cparser) propertyFunctionDescriptor(name string, setter bool) (*Function, error) {
	var params []string
	if p.eat("(") && !p.eat(")") {
		for {
			n, err := p.name()
			if err != nil {
				return nil, err
			}
			params = append(params, n)
			if p.eat(")") {
				break
			}
			if err = p.need(","); err != nil {
				return nil, err
			}
		}
	}
	if setter && len(params) != 1 {
		return nil, fmt.Errorf("property setter requires one parameter")
	}
	if !setter && len(params) != 0 {
		return nil, fmt.Errorf("property getter cannot take parameters")
	}
	if err := p.need("{"); err != nil {
		return nil, err
	}
	// Keep withMember's lexical with-depth lookup aligned with this body.
	sub := &compiler{program: Program{Storage: p.c.program.Storage}, p: p, functionBody: true}
	activeCompiler := p.c
	p.c = sub
	defer func() { p.c = activeCompiler }()
	for i, n := range params {
		sub.emit(p.at().loc, Op{Code: OpParameter, Name: n, Index: i})
	}
	for !p.eat("}") {
		if p.at().kind == "eof" {
			return nil, fmt.Errorf("unterminated property function body")
		}
		if err := sub.statement(); err != nil {
			return nil, err
		}
	}
	if sub.program.Code == nil {
		sub.program.Code = []Instruction{}
	}
	return &Function{Name: name, Parameters: params, Members: sub.localFunctions, Properties: sub.localProperties, Program: sub.program}, nil
}

func (p *cparser) propertyDescriptor() (*Property, error) {
	name, err := p.name()
	if err != nil {
		return nil, err
	}
	if err = p.need("{"); err != nil {
		return nil, err
	}
	property := &Property{Name: name}
	for !p.eat("}") {
		if p.at().kind == "eof" {
			return nil, fmt.Errorf("unterminated property descriptor")
		}
		role := p.at()
		if role.kind != "name" || role.text != "getter" && role.text != "setter" {
			return nil, fmt.Errorf("expected property getter or setter")
		}
		p.p++
		if role.text == "getter" && property.Getter != nil {
			return nil, fmt.Errorf("duplicate property getter")
		}
		if role.text == "setter" && property.Setter != nil {
			return nil, fmt.Errorf("duplicate property setter")
		}
		fn, e := p.propertyFunctionDescriptor(name+"."+role.text, role.text == "setter")
		if e != nil {
			return nil, e
		}
		if role.text == "getter" {
			property.Getter = fn
		} else {
			property.Setter = fn
		}
	}
	return property, nil
}

var prec = map[string]int{"=": 1, "<->": 1, "+=": 1, "-=": 1, "*=": 1, "/=": 1, "%=": 1, "\\=": 1, "&&=": 1, "||=": 1, "&=": 1, "|=": 1, "^=": 1, "<<=": 1, ">>=": 1, "||": 3, "&&": 4, "|": 5, "^": 6, "&": 7, "==": 8, "!=": 8, "===": 8, "!==": 8, "<": 9, ">": 9, "<=": 9, ">=": 9, "instanceof": 9, "<<": 10, ">>": 10, ">>>": 10, "+": 11, "-": 11, "*": 12, "/": 12, "%": 12, "\\": 12, "incontextof": 14}

func (p *cparser) expr(min int) (*cexpr, error) {
	if min > 20 {
		return nil, fmt.Errorf("expression complexity exceeds limit")
	}
	t := p.at()
	var left *cexpr
	if t.kind == "interpolation" {
		p.p++
		left = &cexpr{kind: "value", value: StringValue(""), loc: t.loc}
		for _, part := range t.interpolation.Parts {
			var value *cexpr
			if part.Expression == "" {
				value = &cexpr{kind: "value", value: StringUnits(part.Text), loc: t.loc}
			} else {
				scanner := &cscan{src: part.Expression, storage: t.loc.Storage}
				tokens, err := scanner.tokens()
				if err != nil {
					return nil, err
				}
				embedded := &cparser{ts: tokens, c: p.c}
				value, err = embedded.expr(1)
				if err != nil {
					return nil, err
				}
				if embedded.at().kind != "eof" {
					return nil, fmt.Errorf("%s:%d:%d: unexpected token %q in interpolated expression", t.loc.Storage, t.loc.Line, t.loc.Column, embedded.at().text)
				}
			}
			left = &cexpr{kind: "binary", text: "+", left: left, right: value, loc: t.loc}
		}
		if len(t.interpolation.Parts) == 0 {
			left = &cexpr{kind: "value", value: StringValue(""), loc: t.loc}
		}
	} else {
		cast := ""
		if p.p+2 < len(p.ts) &&
			p.ts[p.p].text == "(" &&
			p.ts[p.p+1].kind == "name" &&
			(p.ts[p.p+1].text == "int" || p.ts[p.p+1].text == "real" || p.ts[p.p+1].text == "string" || p.ts[p.p+1].text == "const") &&
			p.ts[p.p+2].text == ")" {
			cast = p.ts[p.p+1].text
		}
		if cast != "" {
			p.p += 3
			x, e := p.expr(13)
			if e != nil {
				return nil, e
			}
			left = &cexpr{kind: "unary", text: cast, left: x, loc: t.loc}
		} else if p.eat("(") {
			var e error
			left, e = p.full()
			if e != nil {
				return nil, e
			}
			if e = p.need(")"); e != nil {
				return nil, e
			}
		} else if p.eat("[") {
			left = &cexpr{kind: "array", loc: t.loc}
			if !p.eat("]") {
				for {
					if p.eat(",") {
						left.entries = append(left.entries, arrayLiteralEntry{value: &cexpr{kind: "value", value: VoidValue, loc: t.loc}})
						continue
					}
					x, e := p.expr(0)
					if e != nil {
						return nil, e
					}
					if p.eat("=>") || p.eat(":") {
						v, err := p.expr(0)
						if err != nil {
							return nil, err
						}
						left.entries = append(left.entries, arrayLiteralEntry{key: x, value: v})
					} else {
						left.entries = append(left.entries, arrayLiteralEntry{value: x})
					}
					if p.eat("]") {
						break
					}
					if e = p.need(","); e != nil {
						return nil, e
					}
					if p.eat("]") {
						left.entries = append(left.entries, arrayLiteralEntry{value: &cexpr{kind: "value", value: VoidValue, loc: t.loc}})
						break
					}
				}
			}
		} else if p.eat("%[") {
			left = &cexpr{kind: "dict", loc: t.loc}
			if !p.eat("]") {
				for {
					a, e := p.expr(0)
					if e != nil {
						return nil, e
					}
					if p.eat(":") {
						if a.kind == "name" {
							a = &cexpr{kind: "value", value: StringValue(a.text), loc: a.loc}
						}
					} else if !p.eat("=>") {
						if e = p.need(","); e != nil {
							return nil, e
						}
					}
					b, e := p.expr(0)
					if e != nil {
						return nil, e
					}
					left.pairs = append(left.pairs, [2]*cexpr{a, b})
					if p.eat("]") {
						break
					}
					if e = p.need(","); e != nil {
						return nil, e
					}
					if p.eat("]") {
						break
					}
				}
			}
		} else if p.eat("function") {
			name := ""
			if p.at().kind == "name" {
				name = p.at().text
				p.p++
			}
			fn, err := p.functionDescriptor(name)
			if err != nil {
				return nil, err
			}
			left = &cexpr{kind: "function", fn: fn, loc: t.loc}
		} else if p.is("++") || p.is("--") {
			op := p.at().text
			p.p++
			x, e := p.expr(13)
			if e != nil {
				return nil, e
			}
			left = &cexpr{kind: "update", text: op, left: x, prefix: true, loc: t.loc}
		} else if p.is("!") || p.is("~") || p.is("+") || p.is("-") || p.is("typeof") || p.is("delete") || p.is("int") || p.is("real") || p.is("string") || p.is("#") || p.is("$") || p.is("&") || p.is("*") || p.is("isvalid") || p.is("invalidate") {
			op := p.at().text
			p.p++
			if op == "&" && p.eat(".") {
				name, e := p.name()
				if e != nil {
					return nil, e
				}
				left = &cexpr{kind: "unary", text: op, left: p.withMember(name, t.loc), loc: t.loc}
			} else {
				operandMin := 13
				if op == "&" {
					// A property reference binds before incontextof; otherwise
					// &member incontextof receiver addresses the whole bound
					// expression instead of the raw property descriptor.
					operandMin = 15
				}
				x, e := p.expr(operandMin)
				if e != nil {
					return nil, e
				}
				left = &cexpr{kind: "unary", text: op, left: x, loc: t.loc}
			}
		} else if p.eat("new") {
			x, e := p.expr(15)
			if e != nil {
				return nil, e
			}
			if x.kind != "call" {
				return nil, fmt.Errorf("new requires a constructor call")
			}
			x.kind = "construct"
			left = x
		} else if p.eat(".") {
			name, e := p.name()
			if e != nil {
				return nil, e
			}
			left = p.withMember(name, t.loc)
		} else if t.kind == "literal" {
			p.p++
			left = &cexpr{kind: "value", value: t.value, loc: t.loc}
		} else if t.kind == "regexp" && t.regexp != nil {
			p.p++
			left = &cexpr{
				kind: "construct",
				left: &cexpr{kind: "name", text: "RegExp", loc: t.loc},
				args: []*cexpr{
					{kind: "value", value: StringUnits(t.regexp.Pattern), loc: t.loc},
					{kind: "value", value: StringValue(t.regexp.Flags), loc: t.loc},
				},
				loc: t.loc,
			}
		} else if t.kind == "name" {
			if t.text == "instanceof" {
				return nil, fmt.Errorf("unsupported TJS construct %s", t.text)
			}
			p.p++
			switch t.text {
			case "NaN":
				left = &cexpr{kind: "value", value: RealValue(math.NaN()), loc: t.loc}
			case "Infinity":
				left = &cexpr{kind: "value", value: RealValue(math.Inf(1)), loc: t.loc}
			case "true":
				left = &cexpr{kind: "value", value: IntegerValue(1), loc: t.loc}
			case "false":
				left = &cexpr{kind: "value", value: IntegerValue(0), loc: t.loc}
			case "null":
				left = &cexpr{kind: "value", value: NullValue, loc: t.loc}
			case "void":
				left = &cexpr{kind: "value", value: VoidValue, loc: t.loc}
			default:
				left = &cexpr{kind: "name", text: t.text, loc: t.loc}
			}
		} else {
			return nil, fmt.Errorf("unsupported TJS expression token %q", t.text)
		}
	}

	for {
		if p.eat(".") {
			n, e := p.name()
			if e != nil {
				return nil, e
			}
			left = &cexpr{kind: "member", left: left, right: &cexpr{kind: "value", value: StringValue(n), loc: p.at().loc}, loc: left.loc}
			continue
		}
		if p.eat("[") {
			k, e := p.full()
			if e != nil {
				return nil, e
			}
			if e = p.need("]"); e != nil {
				return nil, e
			}
			left = &cexpr{kind: "member", left: left, right: k, loc: left.loc}
			continue
		}
		if p.eat("(") {
			call := &cexpr{kind: "call", left: left, loc: left.loc}
			if !p.eat(")") {
				for {
					if p.is(",") || p.is(")") {
						call.args = append(call.args, &cexpr{kind: "value", value: VoidValue, loc: p.at().loc})
					} else if p.eat("...") {
						if p.is(")") || p.is(",") {
							call.args = append(call.args, &cexpr{kind: "forward", loc: t.loc})
						} else {
							x, e := p.expr(0)
							if e != nil {
								return nil, e
							}
							call.args = append(call.args, &cexpr{kind: "spread", left: x, loc: t.loc})
						}
					} else if p.eat("*") {
						if p.is(")") || p.is(",") {
							call.args = append(call.args, &cexpr{kind: "forward_tail", loc: t.loc})
						} else {
							x, e := p.expr(0)
							if e != nil {
								return nil, e
							}
							call.args = append(call.args, &cexpr{kind: "spread", left: x, loc: t.loc})
						}
					} else {
						start := p.p
						x, e := p.expr(13)
						if e != nil {
							return nil, e
						}
						if p.eat("*") && (p.is(")") || p.is(",")) {
							call.args = append(call.args, &cexpr{kind: "spread", left: x, loc: t.loc})
						} else {
							p.p = start
							x, e = p.expr(0)
							if e != nil {
								return nil, e
							}
							call.args = append(call.args, x)
						}
					}
					if p.eat(")") {
						break
					}
					if e := p.need(","); e != nil {
						return nil, e
					}
				}
			}
			left = call
			continue
		}
		if p.eat("++") {
			left = &cexpr{kind: "update", text: "++", left: left, loc: left.loc}
			continue
		}
		if p.eat("--") {
			left = &cexpr{kind: "update", text: "--", left: left, loc: left.loc}
			continue
		}
		if p.eat("!") {
			left = &cexpr{kind: "eval", left: left, loc: left.loc}
			continue
		}
		if min <= 13 && p.is("isvalid") {
			p.p++
			left = &cexpr{kind: "unary", text: "isvalid", left: left, loc: left.loc}
			continue
		}
		if min <= 2 && p.eat("?") {
			a, e := p.expr(0)
			if e != nil {
				return nil, e
			}
			if e = p.need(":"); e != nil {
				return nil, e
			}
			b, e := p.expr(2)
			if e != nil {
				return nil, e
			}
			left = &cexpr{kind: "cond", left: left, right: a, third: b, loc: left.loc}
			continue
		}
		op := p.at().text
		pp, ok := prec[op]
		if !ok || pp < min {
			break
		}
		p.p++
		rightMin := pp + 1
		if pp == 1 {
			// TJS assignment expressions are right-associative.
			rightMin = pp
		}
		r, e := p.expr(rightMin)
		if e != nil {
			return nil, e
		}
		k := "binary"
		if pp == 1 {
			k = "assign"
		}
		left = &cexpr{kind: k, text: op, left: left, right: r, loc: left.loc}
	}
	return left, nil
}

// --- lowering ----------------------------------------------------------------
type compiler struct {
	program               Program
	p                     *cparser
	controls              []controlContext
	handlerDepth          int
	scopeDepth            int
	withDepth             int
	forwardArgsFrom       int
	hoisted               []Instruction
	functionBody          bool
	localFunctions        []Function
	localProperties       []Property
	constructTargetExpr   *cexpr
	constructTargetObject int
	constructTargetKey    int
}

func (c *compiler) hoistGlobalFunction(loc core.SourceLocation, fn *Function, register int, name string) {
	if c.scopeDepth != 0 || c.withDepth != 0 {
		return
	}
	c.hoisted = append(c.hoisted,
		Instruction{Location: loc, Op: Op{Code: OpFunction, Out: register, extra: &opExtra{function: fn}}},
		Instruction{Location: loc, Op: Op{Code: OpDeclare, Name: name, Input: register}},
	)
}
func (c *compiler) hoistGlobalProperty(loc core.SourceLocation, property *Property, register int, name string) {
	if c.scopeDepth != 0 || c.withDepth != 0 {
		return
	}
	c.hoisted = append(c.hoisted,
		Instruction{Location: loc, Op: Op{Code: OpProperty, Out: register, extra: &opExtra{property: property}}},
		Instruction{Location: loc, Op: Op{Code: OpDeclare, Name: name, Input: register}},
	)
}

func (c *compiler) prependHoistedDeclarations() {
	if len(c.hoisted) == 0 {
		return
	}
	offset := len(c.hoisted)
	for i := range c.program.Code {
		op := &c.program.Code[i].Op
		switch op.Code {
		case OpJump, OpJumpUnless, OpTry, OpUnwind:
			if op.Target >= 0 {
				op.Target += offset
			}
			if op.Finally >= 0 {
				op.Finally += offset
			}
		}
	}
	code := make([]Instruction, 0, offset+len(c.program.Code))
	code = append(code, c.hoisted...)
	code = append(code, c.program.Code...)
	c.program.Code = code
}

type controlContext struct {
	breaks       []int
	continues    []int
	loop         bool
	handlerDepth int
	scopeDepth   int
	withDepth    int
}

func (c *compiler) reg() int {
	r := c.program.Registers
	c.program.Registers++
	return r
}
func (c *compiler) emit(loc core.SourceLocation, op Op) int {
	i := len(c.program.Code)
	c.program.Code = append(c.program.Code, Instruction{Location: loc, Op: op})
	return i
}

func (c *compiler) constant(value Value) int32 {
	index := int32(len(c.program.Constants))
	c.program.Constants = append(c.program.Constants, value)
	return index
}

func (c *compiler) emitValue(loc core.SourceLocation, op Op, value Value) int {
	op.Const = c.constant(value)
	return c.emit(loc, op)
}

type swapTarget struct {
	kind         string
	value        int
	name         string
	object, key  int
	staticKey    Value
	staticKeySet bool
	property     int
}

func (c *compiler) compileSwapTarget(expression *cexpr) (swapTarget, error) {
	switch {
	case expression != nil && expression.kind == "name":
		value := c.reg()
		c.emit(expression.loc, Op{Code: OpLoad, Out: value, Name: expression.text})
		return swapTarget{kind: "name", value: value, name: expression.text}, nil
	case expression != nil && expression.kind == "member":
		object, err := c.compile(expression.left)
		if err != nil {
			return swapTarget{}, err
		}
		value := c.reg()
		if staticKey, ok := staticMemberKey(expression.right); ok {
			c.emitValue(expression.loc, Op{Code: OpGet, Out: value, Object: object, StaticKey: true}, staticKey)
			return swapTarget{kind: "member", value: value, object: object, staticKey: staticKey, staticKeySet: true}, nil
		}
		key, err := c.compile(expression.right)
		if err != nil {
			return swapTarget{}, err
		}
		c.emit(expression.loc, Op{Code: OpGet, Out: value, Object: object, Key: key})
		return swapTarget{kind: "member", value: value, object: object, key: key}, nil
	case expression != nil && expression.kind == "unary" && expression.text == "*":
		property, err := c.compile(expression.left)
		if err != nil {
			return swapTarget{}, err
		}
		value := c.reg()
		c.emit(expression.loc, Op{Code: OpReadProperty, Out: value, Input: property})
		return swapTarget{kind: "property", value: value, property: property}, nil
	default:
		return swapTarget{}, fmt.Errorf("invalid swap assignment target")
	}
}

func (c *compiler) writeSwapTarget(target swapTarget, value int, loc core.SourceLocation) error {
	switch target.kind {
	case "name":
		c.emit(loc, Op{Code: OpStore, Name: target.name, Input: value})
	case "member":
		op := Op{Code: OpSet, Object: target.object, Input: value}
		if target.staticKeySet {
			op.StaticKey, op.Const = true, c.constant(target.staticKey)
		} else {
			op.Key = target.key
		}
		c.emit(loc, op)
	case "property":
		c.emit(loc, Op{Code: OpWriteProperty, Context: target.property, Input: value})
	default:
		return fmt.Errorf("invalid swap assignment target")
	}
	return nil
}
func (c *compiler) patch(i, target int) { c.program.Code[i].Op.Target = target }

func rawPropertyTarget(e *cexpr) (*cexpr, bool) {
	if e != nil && e.kind == "unary" && e.text == "&" {
		e = e.left
	}
	if e == nil {
		return nil, false
	}
	switch e.kind {
	case "name", "member":
		return e, true
	default:
		return nil, false
	}
}

func (c *compiler) compileRawProperty(e *cexpr) (int, error) {
	out := c.reg()
	switch e.kind {
	case "name":
		c.emit(e.loc, Op{Code: OpLoad, Out: out, Name: e.text, Raw: true})
	case "member":
		object, err := c.compile(e.left)
		if err != nil {
			return 0, err
		}
		if key, ok := staticMemberKey(e.right); ok {
			c.emitValue(e.loc, Op{Code: OpGet, Out: out, Object: object, StaticKey: true, Raw: true}, key)
			break
		}
		key, err := c.compile(e.right)
		if err != nil {
			return 0, err
		}
		c.emit(e.loc, Op{Code: OpGet, Out: out, Object: object, Key: key, Raw: true})
	default:
		return 0, fmt.Errorf("invalid raw property reference")
	}
	return out, nil
}

func (c *compiler) writeRawProperty(e *cexpr, input int) error {
	switch e.kind {
	case "name":
		c.emit(e.loc, Op{Code: OpStore, Name: e.text, Input: input, Raw: true})
	case "member":
		object, err := c.compile(e.left)
		if err != nil {
			return err
		}
		if key, ok := staticMemberKey(e.right); ok {
			c.emitValue(e.loc, Op{Code: OpSet, Object: object, StaticKey: true, Input: input, Raw: true}, key)
			break
		}
		key, err := c.compile(e.right)
		if err != nil {
			return err
		}
		c.emit(e.loc, Op{Code: OpSet, Object: object, Key: key, Input: input, Raw: true})
	default:
		return fmt.Errorf("invalid raw property assignment target")
	}
	return nil
}

func staticMemberKey(e *cexpr) (Value, bool) {
	if e == nil || e.kind != "value" || e.value.Kind() != KindString {
		return Value{}, false
	}
	return e.value, true
}

func (c *compiler) compile(e *cexpr) (int, error) {
	out := c.reg()
	if e == nil {
		return out, fmt.Errorf("nil expression")
	}
	switch e.kind {
	case "value":
		c.emitValue(e.loc, Op{Code: OpConstant, Out: out}, e.value)
	case "function":
		if e.fn == nil {
			return 0, fmt.Errorf("nil function descriptor")
		}
		c.emit(e.loc, Op{Code: OpFunction, Out: out, extra: &opExtra{function: e.fn}})
	case "name":
		c.emit(e.loc, Op{Code: OpLoad, Out: out, Name: e.text})
	case "with":
		c.emit(e.loc, Op{Code: OpWith, Out: out})
	case "member":
		o, err := c.compile(e.left)
		if err != nil {
			return 0, err
		}
		if key, ok := staticMemberKey(e.right); ok {
			c.emitValue(e.loc, Op{Code: OpGet, Out: out, Object: o, StaticKey: true}, key)
			break
		}
		k, err := c.compile(e.right)
		if err != nil {
			return 0, err
		}
		c.emit(e.loc, Op{Code: OpGet, Out: out, Object: o, Key: k})
	case "array":
		items := make([]int, 0, len(e.entries)*2)
		type associativeEntry struct{ key, value int }
		var associative []associativeEntry
		for _, entry := range e.entries {
			var key int
			if entry.key != nil {
				var err error
				key, err = c.compile(entry.key)
				if err != nil {
					return 0, err
				}
			}
			value, err := c.compile(entry.value)
			if err != nil {
				return 0, err
			}
			if entry.key != nil {
				items = append(items, key)
			}
			items = append(items, value)
			if entry.key != nil {
				associative = append(associative, associativeEntry{key: key, value: value})
			}
		}
		c.emit(e.loc, Op{Code: OpArray, Out: out, extra: &opExtra{items: items}})
		for _, pair := range associative {
			c.emit(e.loc, Op{Code: OpSet, Object: out, Key: pair.key, Input: pair.value})
		}
	case "dict":
		items := make([]DictionaryOperand, len(e.pairs))
		for i, x := range e.pairs {
			k, err := c.compile(x[0])
			if err != nil {
				return 0, err
			}
			v, err := c.compile(x[1])
			if err != nil {
				return 0, err
			}
			items[i] = DictionaryOperand{Key: k, Value: v}
		}
		c.emit(e.loc, Op{Code: OpDictionary, Out: out, extra: &opExtra{pairs: items}})
	case "seq":
		if _, err := c.compile(e.left); err != nil {
			return 0, err
		}
		return c.compile(e.right)
	case "unary":
		if e.text == "const" {
			return c.compile(e.left)
		}
		if e.text == "delete" {
			switch e.left.kind {
			case "name":
				c.emit(e.loc, Op{Code: OpDeleteName, Out: out, Name: e.left.text})
			case "member":
				o, err := c.compile(e.left.left)
				if err != nil {
					return 0, err
				}
				if key, ok := staticMemberKey(e.left.right); ok {
					c.emitValue(e.loc, Op{Code: OpDeleteMember, Out: out, Object: o, StaticKey: true}, key)
					break
				}
				k, err := c.compile(e.left.right)
				if err != nil {
					return 0, err
				}
				c.emit(e.loc, Op{Code: OpDeleteMember, Out: out, Object: o, Key: k})
			default:
				return 0, fmt.Errorf("delete requires a member or variable")
			}
			break
		}
		if e.text == "typeof" && e.left != nil && e.left.kind == "member" {
			o, err := c.compile(e.left.left)
			if err != nil {
				return 0, err
			}
			if key, ok := staticMemberKey(e.left.right); ok {
				c.emitValue(e.loc, Op{Code: OpTypeOfMember, Out: out, Object: o, StaticKey: true}, key)
				break
			}
			k, err := c.compile(e.left.right)
			if err != nil {
				return 0, err
			}
			c.emit(e.loc, Op{Code: OpTypeOfMember, Out: out, Object: o, Key: k})
			break
		}
		if e.text == "typeof" && e.left != nil && e.left.kind == "name" {
			value := c.reg()
			c.emit(e.loc, Op{Code: OpLoad, Out: value, Name: e.left.text, Optional: true})
			c.emit(e.loc, Op{Code: OpUnary, Out: out, Operator: "typeof", Input: value})
			break
		}
		if e.text == "&" {
			if target, ok := rawPropertyTarget(e.left); ok {
				r, err := c.compileRawProperty(target)
				if err != nil {
					return 0, err
				}
				c.emit(e.loc, Op{Code: OpMove, Out: out, Input: r})
				break
			}
		}
		if e.text == "*" {
			var r int
			var err error
			if target, ok := rawPropertyTarget(e.left); ok {
				r, err = c.compileRawProperty(target)
			} else {
				r, err = c.compile(e.left)
			}
			if err != nil {
				return 0, err
			}
			c.emit(e.loc, Op{Code: OpReadProperty, Out: out, Input: r})
			break
		}
		r, err := c.compile(e.left)
		if err != nil {
			return 0, err
		}
		c.emit(e.loc, Op{Code: OpUnary, Out: out, Operator: e.text, Input: r})
	case "binary":
		if e.text == "&&" || e.text == "||" {
			l, err := c.compile(e.left)
			if err != nil {
				return 0, err
			}
			// Both operators retain the selected operand, rather than coercing
			// it to a boolean. JumpUnless is the VM's truth conversion point.
			if e.text == "&&" {
				j := c.emit(e.loc, Op{Code: OpJumpUnless, Input: l})
				r, err := c.compile(e.right)
				if err != nil {
					return 0, err
				}
				c.emit(e.loc, Op{Code: OpMove, Out: out, Input: r})
				end := c.emit(e.loc, Op{Code: OpJump})
				c.patch(j, len(c.program.Code))
				c.emit(e.loc, Op{Code: OpMove, Out: out, Input: l})
				c.patch(end, len(c.program.Code))
			} else {
				j := c.emit(e.loc, Op{Code: OpJumpUnless, Input: l})
				c.emit(e.loc, Op{Code: OpMove, Out: out, Input: l})
				end := c.emit(e.loc, Op{Code: OpJump})
				c.patch(j, len(c.program.Code))
				r, err := c.compile(e.right)
				if err != nil {
					return 0, err
				}
				c.emit(e.loc, Op{Code: OpMove, Out: out, Input: r})
				c.patch(end, len(c.program.Code))
			}
		} else {
			l, err := c.compile(e.left)
			if err != nil {
				return 0, err
			}
			r, err := c.compile(e.right)
			if err != nil {
				return 0, err
			}
			c.emit(e.loc, Op{Code: OpBinary, Out: out, Operator: e.text, Left: l, Right: r})
		}

	case "assign":
		if e.text == "<->" {
			left, err := c.compileSwapTarget(e.left)
			if err != nil {
				return 0, err
			}
			right, err := c.compileSwapTarget(e.right)
			if err != nil {
				return 0, err
			}
			c.emit(e.loc, Op{Code: OpMove, Out: out, Input: right.value})
			if err := c.writeSwapTarget(left, right.value, e.loc); err != nil {
				return 0, err
			}
			if err := c.writeSwapTarget(right, left.value, e.loc); err != nil {
				return 0, err
			}
			break
		}
		if e.left != nil && e.left.kind == "unary" && e.left.text == "&" {
			if e.text != "=" {
				return 0, fmt.Errorf("raw property assignment does not support compound operators")
			}
			r, err := c.compile(e.right)
			if err != nil {
				return 0, err
			}
			c.emit(e.loc, Op{Code: OpMove, Out: out, Input: r})
			target, ok := rawPropertyTarget(e.left)
			if !ok {
				return 0, fmt.Errorf("invalid raw property assignment target")
			}
			if err = c.writeRawProperty(target, out); err != nil {
				return 0, err
			}
			break
		}
		if e.left != nil && e.left.kind == "unary" && e.left.text == "*" {
			var property int
			var err error
			if target, ok := rawPropertyTarget(e.left.left); ok {
				property, err = c.compileRawProperty(target)
			} else {
				property, err = c.compile(e.left.left)
			}
			if err != nil {
				return 0, err
			}
			r, err := c.compile(e.right)
			if err != nil {
				return 0, err
			}
			if e.text != "=" {
				old := c.reg()
				c.emit(e.loc, Op{Code: OpReadProperty, Out: old, Input: property})
				c.emit(e.loc, Op{Code: OpBinary, Out: out, Operator: strings.TrimSuffix(e.text, "="), Left: old, Right: r})
			} else {
				c.emit(e.loc, Op{Code: OpMove, Out: out, Input: r})
			}
			c.emit(e.loc, Op{Code: OpWriteProperty, Context: property, Input: out})
			break
		}
		if e.text == "=" && e.left != nil && e.left.kind == "member" &&
			e.left.left != nil && e.left.left.kind == "name" && e.left.left.text == "global" {
			object, err := c.compile(e.left.left)
			if err != nil {
				return 0, err
			}
			key, err := c.compile(e.left.right)
			if err != nil {
				return 0, err
			}
			if e.right.kind == "construct" {
				c.constructTargetExpr = e.right
				c.constructTargetObject = object
				c.constructTargetKey = key
				r, err := c.compile(e.right)
				c.constructTargetExpr = nil
				if err != nil {
					return 0, err
				}
				c.emit(e.loc, Op{Code: OpMove, Out: out, Input: r})
			} else {
				r, err := c.compile(e.right)
				if err != nil {
					return 0, err
				}
				c.emit(e.loc, Op{Code: OpMove, Out: out, Input: r})
			}
			c.emit(e.loc, Op{Code: OpSet, Object: object, Key: key, Input: out})
			break
		}
		r, err := c.compile(e.right)
		if err != nil {
			return 0, err
		}
		var old int
		if e.text != "=" {
			switch e.left.kind {
			case "name":
				old = c.reg()
				c.emit(e.loc, Op{Code: OpLoad, Out: old, Name: e.left.text})
			case "member":
				o, x := c.compile(e.left.left)
				if x != nil {
					return 0, x
				}
				old = c.reg()
				if key, ok := staticMemberKey(e.left.right); ok {
					c.emitValue(e.loc, Op{Code: OpGet, Out: old, Object: o, StaticKey: true}, key)
					break
				}
				k, x := c.compile(e.left.right)
				if x != nil {
					return 0, x
				}
				c.emit(e.loc, Op{Code: OpGet, Out: old, Object: o, Key: k})
			default:
				return 0, fmt.Errorf("invalid assignment target")
			}
			c.emit(e.loc, Op{Code: OpBinary, Out: out, Operator: strings.TrimSuffix(e.text, "="), Left: old, Right: r})
		} else {
			c.emit(e.loc, Op{Code: OpMove, Out: out, Input: r})
		}
		switch e.left.kind {
		case "name":
			c.emit(e.loc, Op{Code: OpStore, Name: e.left.text, Input: out})
		case "member":
			o, x := c.compile(e.left.left)
			if x != nil {
				return 0, x
			}
			if key, ok := staticMemberKey(e.left.right); ok {
				c.emitValue(e.loc, Op{Code: OpSet, Object: o, StaticKey: true, Input: out}, key)
				break
			}
			k, x := c.compile(e.left.right)
			if x != nil {
				return 0, x
			}
			c.emit(e.loc, Op{Code: OpSet, Object: o, Key: k, Input: out})
		default:
			return 0, fmt.Errorf("invalid assignment target")
		}
	case "call", "construct":
		var callee, context int
		contextSet := false
		if e.left.kind == "member" {
			o, err := c.compile(e.left.left)
			if err != nil {
				return 0, err
			}
			callee = c.reg()
			if key, ok := staticMemberKey(e.left.right); ok {
				c.emitValue(e.loc, Op{Code: OpGet, Out: callee, Object: o, StaticKey: true}, key)
			} else {
				k, err := c.compile(e.left.right)
				if err != nil {
					return 0, err
				}
				c.emit(e.loc, Op{Code: OpGet, Out: callee, Object: o, Key: k})
			}
			context, contextSet = o, true
		} else {
			var err error
			callee, err = c.compile(e.left)
			if err != nil {
				return 0, err
			}
		}
		args := make([]Argument, len(e.args))
		for i, x := range e.args {
			if x.kind == "forward" {
				args[i] = Argument{Kind: ArgumentForward}
				continue
			}
			if x.kind == "forward_tail" {
				args[i] = Argument{Kind: ArgumentForwardFrom, From: c.forwardArgsFrom}
				continue
			}
			if x.kind == "spread" {
				r, err := c.compile(x.left)
				if err != nil {
					return 0, err
				}
				args[i] = Argument{Kind: ArgumentSpread, Register: r}
				continue
			}
			r, err := c.compile(x)
			if err != nil {
				return 0, err
			}
			args[i] = Argument{Kind: ArgumentValue, Register: r}
		}
		op := Op{Code: OpCall, Out: out, Callee: callee, Context: context, ContextSet: contextSet, extra: &opExtra{args: args}, ResultNeeded: true}
		if e.kind == "construct" {
			op.Code = OpConstruct
			op.ContextSet = false
			if c.constructTargetExpr == e {
				op.TargetSet = true
				op.Object = c.constructTargetObject
				op.Key = c.constructTargetKey
			}
		}
		c.emit(e.loc, op)
	case "cond":
		t, err := c.compile(e.left)
		if err != nil {
			return 0, err
		}
		j := c.emit(e.loc, Op{Code: OpJumpUnless, Input: t})
		a, err := c.compile(e.right)
		if err != nil {
			return 0, err
		}
		c.emit(e.loc, Op{Code: OpMove, Out: out, Input: a})
		end := c.emit(e.loc, Op{Code: OpJump})
		c.patch(j, len(c.program.Code))
		b, err := c.compile(e.third)
		if err != nil {
			return 0, err
		}
		c.emit(e.loc, Op{Code: OpMove, Out: out, Input: b})
		c.patch(end, len(c.program.Code))
	case "eval":
		r, err := c.compile(e.left)
		if err != nil {
			return 0, err
		}
		c.emit(e.loc, Op{Code: OpEval, Out: out, Input: r, ResultNeeded: true})
	case "update":
		if e.left.kind != "name" && e.left.kind != "member" {
			return 0, fmt.Errorf("invalid update target")
		}
		old := c.reg()
		var object, key int
		var staticKey Value
		var staticKeySet bool
		if e.left.kind == "name" {
			c.emit(e.loc, Op{Code: OpLoad, Out: old, Name: e.left.text})
		} else {
			var err error
			object, err = c.compile(e.left.left)
			if err != nil {
				return 0, err
			}
			if staticKey, staticKeySet = staticMemberKey(e.left.right); staticKeySet {
				c.emitValue(e.loc, Op{Code: OpGet, Out: old, Object: object, StaticKey: true}, staticKey)
			} else {
				key, err = c.compile(e.left.right)
				if err != nil {
					return 0, err
				}
				c.emit(e.loc, Op{Code: OpGet, Out: old, Object: object, Key: key})
			}
		}
		one := c.reg()
		c.emitValue(e.loc, Op{Code: OpConstant, Out: one}, IntegerValue(1))
		next := c.reg()
		operator := "+"
		if e.text == "--" {
			operator = "-"
		}
		c.emit(e.loc, Op{Code: OpBinary, Out: next, Operator: operator, Left: old, Right: one})
		if e.left.kind == "name" {
			c.emit(e.loc, Op{Code: OpStore, Name: e.left.text, Input: next})
		} else {
			op := Op{Code: OpSet, Object: object, Input: next}
			if staticKeySet {
				op.StaticKey, op.Const = true, c.constant(staticKey)
			} else {
				op.Key = key
			}
			c.emit(e.loc, op)
		}
		if e.prefix {
			c.emit(e.loc, Op{Code: OpMove, Out: out, Input: next})
		} else {
			c.emit(e.loc, Op{Code: OpMove, Out: out, Input: old})
		}
	default:
		return 0, fmt.Errorf("unsupported expression %q", e.kind)
	}
	return out, nil
}
func (c *compiler) block() error {
	if err := c.p.need("{"); err != nil {
		return err
	}
	c.emit(c.p.at().loc, Op{Code: OpEnterScope})
	c.scopeDepth++
	for !c.p.eat("}") {
		if c.p.at().kind == "eof" {
			return fmt.Errorf("unterminated block")
		}
		if err := c.statement(); err != nil {
			return err
		}
	}
	c.scopeDepth--
	c.emit(c.p.at().loc, Op{Code: OpLeaveScope})
	return nil
}

func (c *compiler) tryStatement(loc core.SourceLocation) error {
	if !c.p.eat("try") {
		return fmt.Errorf("expected try")
	}
	exception := c.reg()
	tryOp := c.emit(loc, Op{Code: OpTry, Target: -1, Exception: exception, Finally: -1})
	c.handlerDepth++
	if err := c.block(); err != nil {
		return err
	}
	c.handlerDepth--
	c.emit(loc, Op{Code: OpEndTry})
	normal := c.emit(loc, Op{Code: OpJump})
	catchJump := -1
	catchTry := -1
	if c.p.eat("catch") {
		catchName := ""
		var err error
		if c.p.eat("(") {
			if !c.p.eat(")") {
				catchName, err = c.p.name()
				if err != nil {
					return err
				}
				if err = c.p.need(")"); err != nil {
					return err
				}
			}
		}
		catchStart := len(c.program.Code)
		c.patch(tryOp, catchStart)
		c.emit(loc, Op{Code: OpEnterScope})
		if catchName != "" {
			c.emit(loc, Op{Code: OpDeclare, Name: catchName, Input: exception})
		}
		c.scopeDepth++
		catchTry = c.emit(loc, Op{Code: OpTry, Target: -1, Finally: -1})
		if err = c.block(); err != nil {
			return err
		}
		c.emit(loc, Op{Code: OpEndTry})
		c.scopeDepth--
		c.emit(loc, Op{Code: OpLeaveScope})
		catchJump = c.emit(loc, Op{Code: OpJump})
	}
	hasFinally := c.p.eat("finally")
	if hasFinally {
		finallyStart := len(c.program.Code)
		c.program.Code[tryOp].Op.Finally = finallyStart
		if catchTry >= 0 {
			c.program.Code[catchTry].Op.Finally = finallyStart
		}
		c.patch(normal, finallyStart)
		if catchJump >= 0 {
			c.patch(catchJump, finallyStart)
		}
		if err := c.block(); err != nil {
			return err
		}
		c.emit(loc, Op{Code: OpEndFinally})
	} else {
		if catchJump >= 0 {
			c.patch(normal, len(c.program.Code))
			c.patch(catchJump, len(c.program.Code))
		} else {
			c.patch(normal, len(c.program.Code))
		}
	}
	if c.p.is("catch") || c.p.is("finally") {
		return fmt.Errorf("duplicate catch/finally")
	}
	return nil
}

func (c *compiler) classStatement(loc core.SourceLocation) error {
	name, err := c.p.name()
	if err != nil {
		return err
	}
	var bases []int
	if c.p.eat("extends") {
		for {
			expression, parseErr := c.p.expr(0)
			if parseErr != nil {
				return parseErr
			}
			base, compileErr := c.compile(expression)
			if compileErr != nil {
				return compileErr
			}
			bases = append(bases, base)
			if !c.p.eat(",") {
				break
			}
		}
	}
	if err = c.p.need("{"); err != nil {
		return err
	}

	outerProgram := c.program
	outerControls := c.controls
	outerHandlerDepth, outerScopeDepth, outerWithDepth := c.handlerDepth, c.scopeDepth, c.withDepth
	definition := &Class{Name: name}
	var initializer Program
	parseErr := func() error {
		c.program = Program{Storage: outerProgram.Storage}
		c.controls = nil
		c.handlerDepth, c.scopeDepth, c.withDepth = 0, 0, 0
		defer func() {
			initializer = c.program
			c.program = outerProgram
			c.controls = outerControls
			c.handlerDepth, c.scopeDepth, c.withDepth = outerHandlerDepth, outerScopeDepth, outerWithDepth
		}()
		for !c.p.eat("}") {
			if c.p.at().kind == "eof" {
				return fmt.Errorf("unterminated class body")
			}
			switch {
			case c.p.eat("function"):
				methodName, e := c.p.name()
				if e != nil {
					return e
				}
				method, e := c.p.functionDescriptor(methodName)
				if e != nil {
					return e
				}
				definition.Methods = append(definition.Methods, *method)
			case c.p.eat("property"):
				property, e := c.p.propertyDescriptor()
				if e != nil {
					return e
				}
				definition.Properties = append(definition.Properties, *property)
			default:
				if e := c.statement(); e != nil {
					return e
				}
			}
		}
		return nil
	}()
	if parseErr != nil {
		return parseErr
	}
	for _, instruction := range initializer.Code {
		if instruction.Op.Code == OpLoad && instruction.Op.Name == "super" {
			return fmt.Errorf("super is not available in a class initializer")
		}
	}
	definition.Initializer = initializer
	out := c.reg()
	c.emit(loc, Op{Code: OpClass, Out: out, extra: &opExtra{class: definition, items: bases}})
	c.emit(loc, Op{Code: OpDeclare, Name: name, Input: out})
	c.p.eat(";")
	return nil
}

func (c *compiler) statement() error {
	t := c.p.at()
	if c.p.eat(";") {
		return nil
	}
	if c.p.eat("class") {
		return c.classStatement(t.loc)
	}
	if c.p.is("property") {
		c.p.p++
		property, err := c.p.propertyDescriptor()
		if err != nil {
			return err
		}
		if c.functionBody && c.scopeDepth == 0 && c.withDepth == 0 {
			c.localProperties = append(c.localProperties, property.Clone())
		}
		r := c.reg()
		if !c.functionBody && c.scopeDepth == 0 && c.withDepth == 0 {
			c.hoistGlobalProperty(t.loc, property, r, property.Name)
			if c.p.eat(";") {
				return nil
			}
			return nil
		}
		c.emit(t.loc, Op{Code: OpProperty, Out: r, extra: &opExtra{property: property}})
		c.emit(t.loc, Op{Code: OpDeclare, Name: property.Name, Input: r})
		c.hoistGlobalProperty(t.loc, property, r, property.Name)
		if c.p.eat(";") {
			return nil
		}
		return nil
	}
	if c.p.is("function") {
		c.p.p++
		name, err := c.p.name()
		if err != nil {
			return err
		}
		fn, err := c.p.functionDescriptor(name)
		if err != nil {
			return err
		}
		if c.functionBody {
			c.localFunctions = append(c.localFunctions, fn.Clone())
		}
		r := c.reg()
		if !c.functionBody && c.scopeDepth == 0 && c.withDepth == 0 {
			c.hoistGlobalFunction(t.loc, fn, r, name)
			if c.p.eat(";") {
				return nil
			}
			return nil
		}
		c.emit(t.loc, Op{Code: OpFunction, Out: r, extra: &opExtra{function: fn}})
		c.emit(t.loc, Op{Code: OpDeclare, Name: name, Input: r})
		c.hoistGlobalFunction(t.loc, fn, r, name)
		if c.p.eat(";") {
			return nil
		}
		return nil
	}
	if c.p.is("try") {
		return c.tryStatement(t.loc)
	}
	if c.p.eat("throw") {
		x, err := c.p.full()
		if err != nil {
			return err
		}
		r, err := c.compile(x)
		if err != nil {
			return err
		}
		c.emit(t.loc, Op{Code: OpThrow, Input: r})
		return c.end()
	}
	if c.p.eat("with") {
		if err := c.p.need("("); err != nil {
			return err
		}
		x, err := c.p.full()
		if err != nil {
			return err
		}
		if err = c.p.need(")"); err != nil {
			return err
		}
		r, err := c.compile(x)
		if err != nil {
			return err
		}
		c.emit(t.loc, Op{Code: OpEnterWith, Input: r})
		c.withDepth++
		err = c.statement()
		c.withDepth--
		c.emit(t.loc, Op{Code: OpLeaveWith})
		return err
	}
	if c.p.is("{") {
		return c.block()
	}
	if c.p.eat("var") || c.p.eat("const") {
		for {
			n, e := c.p.name()
			if e != nil {
				return e
			}
			v := &cexpr{kind: "value", value: VoidValue, loc: t.loc}
			if c.p.eat("=") {
				v, e = c.p.expr(0)
				if e != nil {
					return e
				}
			}
			r, e := c.compile(v)
			if e != nil {
				return e
			}
			c.emit(t.loc, Op{Code: OpDeclare, Name: n, Input: r})
			if !c.p.eat(",") {
				break
			}
		}
		return c.end()
	}
	if c.p.eat("if") {
		if e := c.p.need("("); e != nil {
			return e
		}
		x, e := c.p.full()
		if e != nil {
			return e
		}
		if e = c.p.need(")"); e != nil {
			return e
		}
		r, e := c.compile(x)
		if e != nil {
			return e
		}
		j := c.emit(t.loc, Op{Code: OpJumpUnless, Input: r})
		if e = c.statement(); e != nil {
			return e
		}
		if c.p.eat("else") {
			z := c.emit(t.loc, Op{Code: OpJump})
			c.patch(j, len(c.program.Code))
			if e = c.statement(); e != nil {
				return e
			}
			c.patch(z, len(c.program.Code))
		} else {
			c.patch(j, len(c.program.Code))
		}
		return nil
	}
	if c.p.is("switch") {
		return c.switchStatement()
	}
	if c.p.is("for") {
		return c.forStatement(t.loc)
	}
	if c.p.is("break") || c.p.is("continue") {
		word := c.p.at().text
		c.p.p++
		if e := c.end(); e != nil {
			return e
		}
		var target *controlContext
		if word == "break" {
			if len(c.controls) == 0 {
				return fmt.Errorf("%s outside loop", word)
			}
			target = &c.controls[len(c.controls)-1]
		} else {
			i := len(c.controls) - 1
			for i >= 0 && !c.controls[i].loop {
				i--
			}
			if i < 0 {
				return fmt.Errorf("%s outside loop", word)
			}
			target = &c.controls[i]
		}
		j := c.emit(t.loc, Op{Code: OpUnwind, Operator: "jump", Scopes: target.scopeDepth, Handlers: target.handlerDepth, Withs: target.withDepth})
		if word == "break" {
			target.breaks = append(target.breaks, j)
		} else {
			target.continues = append(target.continues, j)
		}
		return nil
	}
	if c.p.eat("while") {
		if e := c.p.need("("); e != nil {
			return e
		}
		top := len(c.program.Code)
		x, e := c.p.full()
		if e != nil {
			return e
		}
		if e = c.p.need(")"); e != nil {
			return e
		}
		r, e := c.compile(x)
		if e != nil {
			return e
		}
		j := c.emit(t.loc, Op{Code: OpJumpUnless, Input: r})
		c.controls = append(c.controls, controlContext{loop: true, handlerDepth: c.handlerDepth, scopeDepth: c.scopeDepth, withDepth: c.withDepth})
		if e = c.statement(); e != nil {
			return e
		}
		for _, q := range c.controls[len(c.controls)-1].continues {
			c.patch(q, top)
		}
		c.emit(t.loc, Op{Code: OpJump, Target: top})
		end := len(c.program.Code)
		c.patch(j, end)
		for _, q := range c.controls[len(c.controls)-1].breaks {
			c.patch(q, end)
		}
		c.controls = c.controls[:len(c.controls)-1]
		return nil
	}
	if c.p.eat("do") {
		top := len(c.program.Code)
		c.controls = append(c.controls, controlContext{loop: true, handlerDepth: c.handlerDepth, scopeDepth: c.scopeDepth, withDepth: c.withDepth})
		if e := c.statement(); e != nil {
			return e
		}
		condStart := len(c.program.Code)
		for _, q := range c.controls[len(c.controls)-1].continues {
			c.patch(q, condStart)
		}
		if e := c.p.need("while"); e != nil {
			return e
		}
		if e := c.p.need("("); e != nil {
			return e
		}
		x, e := c.p.full()
		if e != nil {
			return e
		}
		if e = c.p.need(")"); e != nil {
			return e
		}
		r, e := c.compile(x)
		if e != nil {
			return e
		}
		j := c.emit(t.loc, Op{Code: OpJumpUnless, Input: r})
		c.emit(t.loc, Op{Code: OpJump, Target: top})
		end := len(c.program.Code)
		c.patch(j, end)
		for _, q := range c.controls[len(c.controls)-1].breaks {
			c.patch(q, end)
		}
		c.controls = c.controls[:len(c.controls)-1]
		return c.end()
	}
	if c.p.eat("return") {
		var x *cexpr
		var e error
		if c.p.is(";") || c.p.is("}") || c.p.at().kind == "eof" {
			x = &cexpr{kind: "value", value: VoidValue, loc: t.loc}
		} else {
			x, e = c.p.full()
			if e != nil {
				return e
			}
		}
		r, e := c.compile(x)
		if e != nil {
			return e
		}
		c.emit(t.loc, Op{Code: OpReturn, Input: r})
		return c.end()
	}
	x, e := c.p.full()
	if e != nil {
		return e
	}
	if c.p.eat("if") {
		test, err := c.p.full()
		if err != nil {
			return err
		}
		input, err := c.compile(test)
		if err != nil {
			return err
		}
		jump := c.emit(t.loc, Op{Code: OpJumpUnless, Input: input})
		if err = c.compileDiscard(x); err != nil {
			return err
		}
		c.patch(jump, len(c.program.Code))
	} else if e = c.compileDiscard(x); e != nil {
		return e
	}
	return c.end()
}

func (c *compiler) compileDiscard(e *cexpr) error {
	if e == nil {
		return fmt.Errorf("nil expression")
	}
	if e.kind == "seq" {
		if err := c.compileDiscard(e.left); err != nil {
			return err
		}
		return c.compileDiscard(e.right)
	}
	if e.kind == "eval" {
		input, err := c.compile(e.left)
		if err != nil {
			return err
		}
		out := c.reg()
		c.emit(e.loc, Op{Code: OpEval, Out: out, Input: input, ResultNeeded: false})
		return nil
	}
	out, err := c.compile(e)
	if err != nil {
		return err
	}
	if len(c.program.Code) > 0 {
		op := &c.program.Code[len(c.program.Code)-1].Op
		if op.Code == OpCall && op.Out == out {
			op.ResultNeeded = false
		}
	}
	return nil
}

func (c *compiler) forStatement(loc core.SourceLocation) error {
	if !c.p.eat("for") {
		return fmt.Errorf("expected for")
	}
	if e := c.p.need("("); e != nil {
		return e
	}
	if !c.p.is(";") {
		if c.p.eat("var") || c.p.eat("const") {
			for {
				n, e := c.p.name()
				if e != nil {
					return e
				}
				v := &cexpr{kind: "value", value: VoidValue, loc: loc}
				if c.p.eat("=") {
					v, e = c.p.expr(0)
					if e != nil {
						return e
					}
				}
				r, e := c.compile(v)
				if e != nil {
					return e
				}
				c.emit(loc, Op{Code: OpDeclare, Name: n, Input: r})
				if !c.p.eat(",") {
					break
				}
			}
		} else {
			x, e := c.p.full()
			if e != nil {
				return e
			}
			if _, e = c.compile(x); e != nil {
				return e
			}
		}
	}
	if e := c.p.need(";"); e != nil {
		return e
	}
	top := len(c.program.Code)
	var condJump int
	if c.p.is(";") {
		r := c.reg()
		c.emitValue(loc, Op{Code: OpConstant, Out: r}, IntegerValue(1))
		condJump = c.emit(loc, Op{Code: OpJumpUnless, Input: r})
	} else {
		x, e := c.p.full()
		if e != nil {
			return e
		}
		r, e := c.compile(x)
		if e != nil {
			return e
		}
		condJump = c.emit(loc, Op{Code: OpJumpUnless, Input: r})
	}
	if e := c.p.need(";"); e != nil {
		return e
	}
	var post *cexpr
	if !c.p.is(")") {
		var e error
		post, e = c.p.full()
		if e != nil {
			return e
		}
	}
	if e := c.p.need(")"); e != nil {
		return e
	}
	c.controls = append(c.controls, controlContext{loop: true, handlerDepth: c.handlerDepth, scopeDepth: c.scopeDepth, withDepth: c.withDepth})
	if e := c.statement(); e != nil {
		return e
	}
	postStart := len(c.program.Code)
	for _, q := range c.controls[len(c.controls)-1].continues {
		c.patch(q, postStart)
	}
	if post != nil {
		if _, e := c.compile(post); e != nil {
			return e
		}
	}
	c.emit(loc, Op{Code: OpJump, Target: top})
	end := len(c.program.Code)
	c.patch(condJump, end)
	for _, q := range c.controls[len(c.controls)-1].breaks {
		c.patch(q, end)
	}
	c.controls = c.controls[:len(c.controls)-1]
	return nil
}

// switchStatement lowers the common case/default form. Cases use TJS loose
// equality, and selected bodies fall through directly to the next body.
func (c *compiler) switchStatement() error {
	at := c.p.at()
	if !c.p.eat("switch") {
		return fmt.Errorf("expected switch")
	}
	if e := c.p.need("("); e != nil {
		return e
	}
	test, err := c.p.full()
	if err != nil {
		return err
	}
	if err = c.p.need(")"); err != nil {
		return err
	}
	ref, err := c.compile(test)
	if err != nil {
		return err
	}
	if err = c.p.need("{"); err != nil {
		return err
	}
	c.controls = append(c.controls, controlContext{handlerDepth: c.handlerDepth, scopeDepth: c.scopeDepth, withDepth: c.withDepth})
	var pending, fallthroughs []int
	haveCase := false
	for !c.p.eat("}") {
		if c.p.at().kind == "eof" {
			return fmt.Errorf("unterminated switch")
		}
		caseStart := len(c.program.Code)
		isDefault := c.p.eat("default")
		if !isDefault && !c.p.eat("case") {
			return fmt.Errorf("expected case or default in switch")
		}
		if haveCase && !isDefault {
			for _, j := range pending {
				c.patch(j, caseStart)
			}
			pending = nil
		}
		if !isDefault {
			key, e := c.p.full()
			if e != nil {
				return e
			}
			if e = c.p.need(":"); e != nil {
				return e
			}
			right, e := c.compile(key)
			if e != nil {
				return e
			}
			match := c.reg()
			c.emit(at.loc, Op{Code: OpBinary, Out: match, Operator: "==", Left: ref, Right: right})
			pending = append(pending, c.emit(at.loc, Op{Code: OpJumpUnless, Input: match}))
		} else if e := c.p.need(":"); e != nil {
			return e
		}
		bodyStart := len(c.program.Code)
		if haveCase {
			if isDefault {
				for _, j := range pending {
					c.patch(j, bodyStart)
				}
				pending = nil
			}
			// A matched body skips later tests entirely.
			for _, j := range fallthroughs {
				c.patch(j, bodyStart)
			}
			fallthroughs = nil
		}
		haveCase = true
		for !c.p.is("case") && !c.p.is("default") && !c.p.is("}") {
			if e := c.statement(); e != nil {
				return e
			}
		}
		fallthroughs = append(fallthroughs, c.emit(at.loc, Op{Code: OpJump}))
	}
	end := len(c.program.Code)
	for _, j := range pending {
		c.patch(j, end)
	}
	for _, j := range fallthroughs {
		c.patch(j, end)
	}
	for _, j := range c.controls[len(c.controls)-1].breaks {
		c.patch(j, end)
	}
	c.controls = c.controls[:len(c.controls)-1]
	return nil
}
func (c *compiler) end() error {
	if c.p.eat(";") || c.p.is("}") || c.p.at().kind == "eof" {
		return nil
	}
	return fmt.Errorf("expected semicolon; unsupported statement continuation")
}

func unsupportedStatement(k string) error {
	switch k {
	case "class":
	}
	return nil
}

// compile_with_preprocessor runs conditional compilation before scanning. The
// preprocessor replaces disabled source with spaces while retaining locations.
func compile_with_preprocessor(storage, source string, expression bool, state *Preprocessor) (Program, error) {
	if state == nil {
		state = NewPreprocessor()
	}
	pp, err := state.Process(Source{Storage: storage, Text: source, Location: core.SourceLocation{Storage: storage, Line: 1, Column: 1}})
	if err != nil {
		return Program{}, err
	}
	ts, err := (&cscan{storage: storage, src: pp.Text}).tokens()
	if err != nil {
		return Program{}, err
	}
	p := &cparser{ts: ts}
	c := &compiler{program: Program{Storage: storage}}
	p.c = c
	c.p = p
	if expression {
		if p.at().kind == "eof" {
			r := c.reg()
			c.emitValue(p.at().loc, Op{Code: OpConstant, Out: r}, VoidValue)
			c.emit(p.at().loc, Op{Code: OpReturn, Input: r})
		} else {
			if p.at().kind == "name" {
				switch p.at().text {
				case "switch", "do", "octet", "with":
					return Program{}, fmt.Errorf("%s:%d:%d: unsupported TJS construct %s", storage, p.at().loc.Line, p.at().loc.Column, p.at().text)
				}
			}
			x, e := p.full()
			if e != nil {
				return Program{}, fmt.Errorf("%s:%d:%d: %w", storage, p.at().loc.Line, p.at().loc.Column, e)
			}
			for p.eat(";") {
			}
			if p.at().kind != "eof" {
				return Program{}, fmt.Errorf("%s:%d:%d: unexpected tokens after TJS expression", storage, p.at().loc.Line, p.at().loc.Column)
			}
			r, e := c.compile(x)
			if e != nil {
				return Program{}, e
			}
			c.emit(x.loc, Op{Code: OpReturn, Input: r})
		}
	} else {
		for p.at().kind != "eof" {
			before := p.p
			if p.is("switch") {
				if e := c.switchStatement(); e != nil {
					return Program{}, fmt.Errorf("%s:%d:%d: %w", storage, p.at().loc.Line, p.at().loc.Column, e)
				}
			} else {
				if e := unsupportedStatement(p.at().text); e != nil {
					return Program{}, fmt.Errorf("%s:%d:%d: %w", storage, p.at().loc.Line, p.at().loc.Column, e)
				}
				if e := c.statement(); e != nil {
					return Program{}, fmt.Errorf("%s:%d:%d: %w", storage, p.at().loc.Line, p.at().loc.Column, e)
				}
			}
			if p.p <= before {
				return Program{}, fmt.Errorf("%s:%d:%d: compiler made no progress", storage, p.at().loc.Line, p.at().loc.Column)
			}
		}
	}
	c.prependHoistedDeclarations()
	return c.program, nil
}
func compile(storage, source string) (Program, error) {
	return compile_with_preprocessor(storage, source, false, NewPreprocessor())
}
func compile_expression(storage, source string) (Program, error) {
	return compile_with_preprocessor(storage, source, true, NewPreprocessor())
}

// Compile compiles a TJS statement script with a fresh preprocessor state.
func Compile(storage, source string) (Program, error) { return compile(storage, source) }

// CompileExpression compiles one TJS expression and emits a Return operation.
func CompileExpression(storage, source string) (Program, error) {
	return compile_expression(storage, source)
}

// CompileEval compiles the expression or statement script accepted by TJS's
// eval operator.
func CompileEval(storage, source string) (Program, error) {
	program, err := compile_expression(storage, source)
	if err == nil {
		return program, nil
	}
	return compile(storage, source)
}

// CompileWithPreprocessor preserves macro definitions across source units.
func CompileWithPreprocessor(storage, source string, expression bool, state *Preprocessor) (Program, error) {
	return compile_with_preprocessor(storage, source, expression, state)
}
