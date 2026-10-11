package tjs

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/eslzzyl/krkrgo/internal/core"
)

// Source is a TJS source unit. Storage is copied to every generated source
// location; Line and Column are one-based UTF-16 code-unit coordinates. A
// zero-valued Location starts at line 1, column 1.
type Source struct {
	Storage  string
	Text     string
	Location core.SourceLocation
}

// SourceChar associates one UTF-16 code unit with its source location.
type SourceChar struct {
	Unit     uint16
	Location core.SourceLocation
}

// PreprocessedSource is source after directives and disabled branches have
// been replaced by spaces. Replacements deliberately retain one UTF-16 code
// unit per input code unit, so lexer offsets and locations remain stable.
type PreprocessedSource struct {
	Text      string
	Units     UTF16
	Locations []core.SourceLocation
}

func (s PreprocessedSource) String() string { return s.Text }
func (s PreprocessedSource) Len() int       { return len(s.Units) }
func (s PreprocessedSource) At(index int) (SourceChar, bool) {
	if index < 0 || index >= len(s.Units) || index >= len(s.Locations) {
		return SourceChar{}, false
	}
	return SourceChar{Unit: s.Units[index], Location: s.Locations[index]}, true
}
func (s PreprocessedSource) SourceChars() []SourceChar {
	r := make([]SourceChar, len(s.Units))
	for i, u := range s.Units {
		if i < len(s.Locations) {
			r[i] = SourceChar{Unit: u, Location: s.Locations[i]}
		}
	}
	return r
}

// Preprocessor evaluates TJS conditional directives. Defines are signed
// 32-bit values, matching krkrz_tjs rather than the host machine's integer
// width.
type Preprocessor struct {
	defines map[string]int32
}

// NewPreprocessor creates an empty conditional environment.
func NewPreprocessor() *Preprocessor { return &Preprocessor{defines: make(map[string]int32)} }

func (p *Preprocessor) ensureDefines() {
	if p.defines == nil {
		p.defines = make(map[string]int32)
	}
}

// Define creates or replaces a numeric preprocessor symbol. With no value,
// the symbol is given the conventional true value 1.
func (p *Preprocessor) Define(name string, values ...int32) {
	p.ensureDefines()
	value := int32(1)
	if len(values) != 0 {
		value = values[0]
	}
	p.defines[name] = value
}

// Set is the map-like form of Define.
func (p *Preprocessor) Set(name string, values ...int32) { p.Define(name, values...) }

// Get obtains a defined symbol. Undefined symbols evaluate to zero in an
// expression, but Get distinguishes that case for callers managing symbols.
func (p *Preprocessor) Get(name string) (int32, bool) {
	if p == nil {
		return 0, false
	}
	v, ok := p.defines[name]
	return v, ok
}

func (p *Preprocessor) Undefine(name string) {
	if p != nil {
		delete(p.defines, name)
	}
}
func (p *Preprocessor) Unset(name string) { p.Undefine(name) }

// Expression is an alias for Evaluate retained for callers that call the
// conditional expression evaluator directly.
func (p *Preprocessor) Expression(expression string) (int32, error) {
	return p.Evaluate(expression)
}

// Evaluate evaluates all operands of expression (including both sides of &&
// and ||) using signed 32-bit arithmetic. This is intentional: conditional
// expressions are parsed eagerly by TJS's preprocessor.
func (p *Preprocessor) Evaluate(expression string) (int32, error) {
	if p == nil {
		p = NewPreprocessor()
	}
	p.ensureDefines()
	return newPPExpr(expression, p.defines).parse()
}

// Process preprocesses a Source or a string. Accepting both keeps the API
// useful to a compiler reading a resource and to a lexer working on text.
func (p *Preprocessor) Process(input any) (PreprocessedSource, error) {
	if p == nil {
		p = NewPreprocessor()
	}
	var source Source
	switch v := input.(type) {
	case string:
		source = Source{Text: v}
	case Source:
		source = v
	case *Source:
		if v == nil {
			return PreprocessedSource{}, fmt.Errorf("nil TJS source")
		}
		source = *v
	default:
		return PreprocessedSource{}, fmt.Errorf("unsupported TJS source type %T", input)
	}
	if source.Location.Storage != "" && source.Storage == "" {
		source.Storage = source.Location.Storage
	}
	units, locations := sourceUnits(source)
	out := append([]uint16(nil), units...)
	frames := make([]ppFrame, 0, 4)
	inBlockComment := false
	lineStart := 0
	for lineStart < len(units) {
		lineEnd := lineStart
		for lineEnd < len(units) && units[lineEnd] != '\n' && units[lineEnd] != '\r' {
			lineEnd++
		}
		end := lineEnd
		if lineEnd < len(units) {
			if units[lineEnd] == '\r' && lineEnd+1 < len(units) && units[lineEnd+1] == '\n' {
				end = lineEnd + 2
			} else {
				end = lineEnd + 1
			}
		}
		blockBeforeLine := inBlockComment
		kind, body, prefix, directiveLine := ppDirectiveState(units[lineStart:lineEnd], &inBlockComment)
		if directiveLine && prefix == "#" {
			active := currentPPActive(frames)
			loc := locations[lineStart]
			handled, err := p.directive(kind, body, prefix, &frames, loc)
			if err != nil {
				return PreprocessedSource{}, err
			}
			if handled {
				maskLine(out, lineStart, lineEnd)
			} else if !active {
				maskLine(out, lineStart, lineEnd)
			}
		} else {
			if directiveLine && prefix == "@" {
				inBlockComment = blockBeforeLine
			}
			if err := p.processAtDirectives(out, units[lineStart:lineEnd], lineStart, locations, &inBlockComment, &frames); err != nil {
				return PreprocessedSource{}, err
			}
		}
		lineStart = end
	}
	if len(frames) != 0 {
		loc := core.SourceLocation{Storage: source.Storage, Line: source.Location.Line, Column: source.Location.Column}
		if len(locations) != 0 {
			loc = locations[len(locations)-1]
		}
		return PreprocessedSource{}, fmt.Errorf("%s:%d:%d: unterminated conditional", loc.Storage, loc.Line, loc.Column)
	}
	result := PreprocessedSource{Units: NewUTF16(out), Locations: append([]core.SourceLocation(nil), locations...)}
	result.Text = result.Units.String()
	return result, nil
}

// EvaluateExpression evaluates an expression with no predefined symbols.
func EvaluateExpression(expression string) (int32, error) {
	return NewPreprocessor().Evaluate(expression)
}

// ProcessSource is the explicit descriptor form of Process.
func (p *Preprocessor) ProcessSource(source Source) (PreprocessedSource, error) {
	return p.Process(source)
}

// ProcessString is an alias useful at lexer call sites.
func (p *Preprocessor) ProcessString(text string) (PreprocessedSource, error) {
	return p.Process(text)
}

// ProcessText is the explicit string form of Process.
func (p *Preprocessor) ProcessText(text string) (PreprocessedSource, error) { return p.Process(text) }

// Preprocess runs a fresh preprocessor over text.
func Preprocess(text string) (PreprocessedSource, error) { return NewPreprocessor().Process(text) }

// PreprocessSource runs a fresh preprocessor over a source descriptor.
func PreprocessSource(source Source) (PreprocessedSource, error) {
	return NewPreprocessor().Process(source)
}

type ppFrame struct{ parent, active, taken, seenElse bool }

func currentPPActive(frames []ppFrame) bool { return len(frames) == 0 || frames[len(frames)-1].active }

func (p *Preprocessor) directive(kind, body, prefix string, frames *[]ppFrame, loc core.SourceLocation) (bool, error) {
	if prefix == "@" && kind != "if" && kind != "elif" && kind != "else" && kind != "endif" && kind != "set" {
		return false, nil
	}
	parent := currentPPActive(*frames)
	switch kind {
	case "if", "ifdef", "ifndef":
		var value int32
		var err error
		switch kind {
		case "ifdef", "ifndef":
			name := strings.TrimSpace(stripDirectiveComments(body))
			if !validPPName(name) {
				return true, ppError(loc, "invalid symbol name")
			}
			_, defined := p.Get(name)
			if kind == "ifndef" {
				defined = !defined
			}
			if defined {
				value = 1
			}
		default:
			value, err = p.Evaluate(stripDirectiveComments(body))
			if err != nil {
				return true, ppError(loc, err.Error())
			}
		}
		*frames = append(*frames, ppFrame{parent: parent, active: parent && value != 0, taken: value != 0})
		return true, nil
	case "elif":
		if len(*frames) == 0 {
			return true, ppError(loc, prefix+"elif without "+prefix+"if")
		}
		v, err := p.Evaluate(stripDirectiveComments(body))
		if err != nil {
			return true, ppError(loc, err.Error())
		}
		f := &(*frames)[len(*frames)-1]
		if f.seenElse {
			return true, ppError(loc, prefix+"elif after "+prefix+"else")
		}
		f.active = f.parent && !f.taken && v != 0
		if v != 0 {
			f.taken = true
		}
		return true, nil
	case "else":
		if len(*frames) == 0 {
			return true, ppError(loc, prefix+"else without "+prefix+"if")
		}
		f := &(*frames)[len(*frames)-1]
		if f.seenElse {
			return true, ppError(loc, "duplicate "+prefix+"else")
		}
		f.seenElse = true
		f.active = f.parent && !f.taken
		f.taken = true
		return true, nil
	case "endif":
		if len(*frames) == 0 {
			return true, ppError(loc, prefix+"endif without "+prefix+"if")
		}
		*frames = (*frames)[:len(*frames)-1]
		return true, nil
	case "define":
		if !parent {
			return true, nil
		}
		text := strings.TrimSpace(stripDirectiveComments(body))
		name, rest := ppWord(text)
		if !validPPName(name) {
			return true, ppError(loc, "invalid #define name")
		}
		value := int32(1)
		if strings.TrimSpace(rest) != "" {
			var err error
			value, err = p.Evaluate(rest)
			if err != nil {
				return true, ppError(loc, err.Error())
			}
		}
		p.Define(name, value)
		return true, nil
	case "undef", "set":
		if !parent {
			return true, nil
		}
		text := strings.TrimSpace(stripDirectiveComments(body))
		if kind == "set" && prefix == "@" {
			name, rest := ppWord(text)
			rest = strings.TrimSpace(rest)
			if !validPPName(name) || !strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, "==") {
				return true, ppError(loc, "invalid @set assignment")
			}
			value, err := p.Evaluate(strings.TrimSpace(rest[1:]))
			if err != nil {
				return true, ppError(loc, err.Error())
			}
			p.Set(name, value)
			return true, nil
		}
		name, rest := ppWord(text)
		if !validPPName(name) {
			return true, ppError(loc, "invalid symbol name")
		}
		if kind == "undef" {
			p.Undefine(name)
			return true, nil
		}
		if strings.TrimSpace(rest) == "" {
			return true, ppError(loc, "#set requires a value")
		}
		value, err := p.Evaluate(rest)
		if err != nil {
			return true, ppError(loc, err.Error())
		}
		p.Set(name, value)
		return true, nil
	case "pragma":
		return true, nil
	default:
		return false, nil
	}
}

func ppError(loc core.SourceLocation, message string) error {
	return fmt.Errorf("%s:%d:%d: %s", loc.Storage, loc.Line, loc.Column, message)
}

// ppDirectiveState first removes leading comments for directive recognition.
// Keeping the block-comment state across physical lines prevents a # or @
// inside a comment from changing conditional state.
func ppDirectiveState(line []uint16, inBlock *bool) (kind, body, prefix string, ok bool) {
	start := 0
	for {
		if *inBlock {
			for start+1 < len(line) && !(line[start] == '*' && line[start+1] == '/') {
				start++
			}
			if start+1 >= len(line) {
				return "", "", "", false
			}
			*inBlock = false
			start += 2
		}
		for start < len(line) && (line[start] == ' ' || line[start] == '\t' || line[start] == '\f' || line[start] == '\v') {
			start++
		}
		if start+1 < len(line) && line[start] == '/' && line[start+1] == '*' {
			*inBlock = true
			start += 2
			continue
		}
		if start+1 < len(line) && line[start] == '/' && line[start+1] == '/' {
			return "", "", "", false
		}
		break
	}
	if start >= len(line) || (line[start] != '#' && line[start] != '@') {
		return "", "", "", false
	}
	prefix = string(rune(line[start]))
	start++
	for start < len(line) && (line[start] == ' ' || line[start] == '\t') {
		start++
	}
	end := start
	for end < len(line) && ((line[end] >= 'a' && line[end] <= 'z') || (line[end] >= 'A' && line[end] <= 'Z') || line[end] == '_') {
		end++
	}
	if end == start {
		return "", "", prefix, false
	}
	bodyUnits := line[end:]
	for i := 0; i+1 < len(bodyUnits); i++ {
		if bodyUnits[i] == '/' && bodyUnits[i+1] == '/' {
			break
		}
		if bodyUnits[i] == '/' && bodyUnits[i+1] == '*' {
			j := i + 2
			for j+1 < len(bodyUnits) && !(bodyUnits[j] == '*' && bodyUnits[j+1] == '/') {
				j++
			}
			if j+1 >= len(bodyUnits) {
				*inBlock = true
			} else {
				i = j + 1
			}
		}
	}
	return strings.ToLower(string(utf16.Decode(line[start:end]))), string(utf16.Decode(bodyUnits)), prefix, true
}
func (p *Preprocessor) processAtDirectives(out, line []uint16, offset int, locations []core.SourceLocation, inBlock *bool, frames *[]ppFrame) error {
	for i := 0; i < len(line); {
		if *inBlock {
			if i+1 < len(line) && line[i] == '*' && line[i+1] == '/' {
				if !currentPPActive(*frames) {
					maskLine(out, offset+i, offset+i+2)
				}
				*inBlock = false
				i += 2
				continue
			}
			if !currentPPActive(*frames) {
				maskLine(out, offset+i, offset+i+1)
			}
			i++
			continue
		}
		if i+1 < len(line) && line[i] == '/' && line[i+1] == '*' {
			if !currentPPActive(*frames) {
				maskLine(out, offset+i, offset+i+2)
			}
			*inBlock = true
			i += 2
			continue
		}
		if i+1 < len(line) && line[i] == '/' && line[i+1] == '/' {
			if !currentPPActive(*frames) {
				maskLine(out, offset+i, offset+len(line))
			}
			break
		}
		if line[i] == '\'' || line[i] == '"' {
			quote := line[i]
			if !currentPPActive(*frames) {
				maskLine(out, offset+i, offset+i+1)
			}
			i++
			for i < len(line) {
				if !currentPPActive(*frames) {
					maskLine(out, offset+i, offset+i+1)
				}
				if line[i] == '\\' {
					i++
					if i < len(line) {
						if !currentPPActive(*frames) {
							maskLine(out, offset+i, offset+i+1)
						}
						i++
					}
					continue
				}
				if line[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		if line[i] == '@' {
			kind, body, end, recognized, err := parseAtDirective(line, i)
			if err != nil {
				return ppError(locations[offset+i], err.Error())
			}
			if recognized {
				handled, err := p.directive(kind, body, "@", frames, locations[offset+i])
				if err != nil {
					return err
				}
				if handled {
					maskLine(out, offset+i, offset+end)
					i = end
					continue
				}
			}
		}
		if !currentPPActive(*frames) {
			maskLine(out, offset+i, offset+i+1)
		}
		i++
	}
	return nil
}

func parseAtDirective(line []uint16, start int) (kind, body string, end int, recognized bool, err error) {
	if start+1 >= len(line) || line[start] != '@' {
		return "", "", start, false, nil
	}
	nameStart := start + 1
	nameEnd := nameStart
	for nameEnd < len(line) && ((line[nameEnd] >= 'a' && line[nameEnd] <= 'z') || (line[nameEnd] >= 'A' && line[nameEnd] <= 'Z') || line[nameEnd] == '_') {
		nameEnd++
	}
	if nameEnd == nameStart || nameEnd < len(line) && (line[nameEnd] >= 0x80 || (line[nameEnd] >= '0' && line[nameEnd] <= '9') || line[nameEnd] == '_' || (line[nameEnd] >= 'a' && line[nameEnd] <= 'z') || (line[nameEnd] >= 'A' && line[nameEnd] <= 'Z')) {
		return "", "", start, false, nil
	}
	kind = strings.ToLower(string(utf16.Decode(line[nameStart:nameEnd])))
	switch kind {
	case "if", "elif", "set":
		expressionStart := nameEnd
		for expressionStart < len(line) && (line[expressionStart] == ' ' || line[expressionStart] == '\t' || line[expressionStart] == '\f' || line[expressionStart] == '\v') {
			expressionStart++
		}
		if expressionStart < len(line) && line[expressionStart] == '(' {
			depth := 1
			expressionEnd := expressionStart + 1
			for expressionEnd < len(line) && depth != 0 {
				switch line[expressionEnd] {
				case '(':
					depth++
				case ')':
					depth--
				}
				expressionEnd++
			}
			if depth != 0 {
				return "", "", start, true, fmt.Errorf("unterminated @%s expression", kind)
			}
			body = string(utf16.Decode(line[expressionStart+1 : expressionEnd-1]))
			return kind, body, expressionEnd, true, nil
		}
		if kind != "set" {
			return kind, string(utf16.Decode(line[nameEnd:])), len(line), true, nil
		}
		return "", "", start, false, nil
	case "else", "endif":
		return kind, "", nameEnd, true, nil
	default:
		return "", "", start, false, nil
	}
}

func maskLine(out []uint16, start, end int) {
	for i := start; i < end; i++ {
		if out[i] != '\r' && out[i] != '\n' {
			out[i] = ' '
		}
	}
}
func ppWord(s string) (string, string) {
	i := 0
	for i < len(s) {
		r, n := utf8DecodeRune(s[i:])
		if !isPPNamePart(r) {
			break
		}
		i += n
	}
	return s[:i], s[i:]
}
func validPPName(s string) bool {
	if s == "" {
		return false
	}
	r, n := utf8DecodeRune(s)
	if !isPPNameStart(r) || n == 0 {
		return false
	}
	for _, x := range s[n:] {
		if !isPPNamePart(x) {
			return false
		}
	}
	return true
}
func isPPNameStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isPPNamePart(r rune) bool  { return isPPNameStart(r) || unicode.IsDigit(r) }
func utf8DecodeRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

func stripDirectiveComments(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '/' {
			break
		}
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 < len(s) {
				i += 2
			}
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func sourceUnits(source Source) ([]uint16, []core.SourceLocation) {
	units := utf16.Encode([]rune(source.Text))
	locs := make([]core.SourceLocation, 0, len(units))
	line, col := source.Location.Line, source.Location.Column
	if line == 0 {
		line = 1
	}
	if col == 0 {
		col = 1
	}
	storage := source.Storage
	runes := []rune(source.Text)
	for ri, r := range runes {
		width := 1
		if r > 0xffff {
			width = 2
		}
		for j := 0; j < width; j++ {
			locs = append(locs, core.SourceLocation{Storage: storage, Line: line, Column: col})
			col++
		}
		if r == '\r' {
			if ri+1 < len(runes) && runes[ri+1] == '\n' {
				continue
			}
			line++
			col = 1
		} else if r == '\n' {
			line++
			col = 1
		}
	}
	return units, locs
}

// ---- signed 32-bit expression parser ----
type ppExpr struct {
	s    string
	pos  int
	defs map[string]int32
}

func newPPExpr(s string, defs map[string]int32) *ppExpr { return &ppExpr{s: s, defs: defs} }
func (e *ppExpr) parse() (int32, error) {
	v, err := e.parseOr()
	if err != nil {
		return 0, err
	}
	e.space()
	if e.pos != len(e.s) {
		return 0, fmt.Errorf("unexpected token %q", e.s[e.pos:])
	}
	return v, nil
}
func (e *ppExpr) space() {
	for e.pos < len(e.s) {
		switch e.s[e.pos] {
		case ' ', '\t', '\r', '\n', '\f', '\v':
			e.pos++
		default:
			return
		}
	}
}
func (e *ppExpr) take(x string) bool {
	e.space()
	if strings.HasPrefix(e.s[e.pos:], x) {
		e.pos += len(x)
		return true
	}
	return false
}
func (e *ppExpr) parseOr() (int32, error) {
	a, err := e.parseXor()
	for err == nil && e.take("||") {
		b, x := e.parseXor()
		if x != nil {
			return 0, x
		}
		if a != 0 || b != 0 {
			a = 1
		} else {
			a = 0
		}
	}
	return a, err
}
func (e *ppExpr) parseXor() (int32, error) {
	a, err := e.parseAnd()
	for err == nil && e.take("^") {
		b, x := e.parseAnd()
		if x != nil {
			return 0, x
		}
		a ^= b
	}
	return a, err
}
func (e *ppExpr) parseAnd() (int32, error) {
	a, err := e.parseBitOr()
	for err == nil && e.take("&&") {
		b, x := e.parseBitOr()
		if x != nil {
			return 0, x
		}
		if a != 0 && b != 0 {
			a = 1
		} else {
			a = 0
		}
	}
	return a, err
}
func (e *ppExpr) parseBitOr() (int32, error) {
	a, err := e.parseBitAnd()
	for err == nil && e.takeSingle("|") {
		b, x := e.parseBitAnd()
		if x != nil {
			return 0, x
		}
		a |= b
	}
	return a, err
}
func (e *ppExpr) parseBitAnd() (int32, error) {
	a, err := e.parseEq()
	for err == nil && e.takeSingle("&") {
		b, x := e.parseEq()
		if x != nil {
			return 0, x
		}
		a &= b
	}
	return a, err
}
func (e *ppExpr) parseEq() (int32, error) {
	a, err := e.parseRel()
	for err == nil {
		if e.take("==") {
			b, x := e.parseRel()
			if x != nil {
				return 0, x
			}
			a = boolInt(a == b)
		} else if e.take("!=") {
			b, x := e.parseRel()
			if x != nil {
				return 0, x
			}
			a = boolInt(a != b)
		} else {
			break
		}
	}
	return a, err
}
func (e *ppExpr) parseRel() (int32, error) {
	a, err := e.parseShift()
	for err == nil {
		if e.take("<=") {
			b, x := e.parseShift()
			if x != nil {
				return 0, x
			}
			a = boolInt(a <= b)
		} else if e.take(">=") {
			b, x := e.parseShift()
			if x != nil {
				return 0, x
			}
			a = boolInt(a >= b)
		} else if e.take("<") {
			b, x := e.parseShift()
			if x != nil {
				return 0, x
			}
			a = boolInt(a < b)
		} else if e.take(">") {
			b, x := e.parseShift()
			if x != nil {
				return 0, x
			}
			a = boolInt(a > b)
		} else {
			break
		}
	}
	return a, err
}
func (e *ppExpr) parseShift() (int32, error) {
	a, err := e.parseAdd()
	for err == nil {
		if e.take("<<") {
			b, x := e.parseAdd()
			if x != nil {
				return 0, x
			}
			if b < 0 || b >= 32 {
				return 0, fmt.Errorf("invalid shift count")
			}
			a = int32(uint32(a) << uint32(b))
		} else if e.take(">>") {
			b, x := e.parseAdd()
			if x != nil {
				return 0, x
			}
			if b < 0 || b >= 32 {
				return 0, fmt.Errorf("invalid shift count")
			}
			a >>= uint32(b)
		} else {
			break
		}
	}
	return a, err
}
func (e *ppExpr) parseAdd() (int32, error) {
	a, err := e.parseMul()
	for err == nil {
		if e.take("+") {
			b, x := e.parseMul()
			if x != nil {
				return 0, x
			}
			a += b
		} else if e.take("-") {
			b, x := e.parseMul()
			if x != nil {
				return 0, x
			}
			a -= b
		} else {
			break
		}
	}
	return a, err
}
func (e *ppExpr) parseMul() (int32, error) {
	a, err := e.parseUnary()
	for err == nil {
		if e.take("*") {
			b, x := e.parseUnary()
			if x != nil {
				return 0, x
			}
			a *= b
		} else if e.take("/") {
			b, x := e.parseUnary()
			if x != nil {
				return 0, x
			}
			if b == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			a /= b
		} else if e.take("%") {
			b, x := e.parseUnary()
			if x != nil {
				return 0, x
			}
			if b == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			a %= b
		} else {
			break
		}
	}
	return a, err
}
func (e *ppExpr) parseUnary() (int32, error) {
	e.space()
	if e.take("!") {
		a, x := e.parseUnary()
		if x != nil {
			return 0, x
		}
		return boolInt(a == 0), nil
	}
	if e.take("~") {
		a, x := e.parseUnary()
		if x != nil {
			return 0, x
		}
		return ^a, nil
	}
	if e.take("+") {
		return e.parseUnary()
	}
	if e.take("-") {
		a, x := e.parseUnary()
		if x != nil {
			return 0, x
		}
		return -a, nil
	}
	return e.parsePrimary()
}
func (e *ppExpr) parsePrimary() (int32, error) {
	e.space()
	if e.pos >= len(e.s) {
		return 0, fmt.Errorf("expected expression")
	}
	if e.take("(") {
		a, x := e.parseOr()
		if x != nil {
			return 0, x
		}
		if !e.take(")") {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		return a, nil
	}
	start := e.pos
	if e.s[e.pos] >= '0' && e.s[e.pos] <= '9' {
		for e.pos < len(e.s) && ((e.s[e.pos] >= '0' && e.s[e.pos] <= '9') || (e.s[e.pos] >= 'a' && e.s[e.pos] <= 'f') || (e.s[e.pos] >= 'A' && e.s[e.pos] <= 'F') || e.s[e.pos] == 'x' || e.s[e.pos] == 'X') {
			e.pos++
		}
		raw := e.s[start:e.pos]
		base := 10
		if len(raw) > 2 && (raw[:2] == "0x" || raw[:2] == "0X") {
			base = 16
			raw = raw[2:]
		} else if len(raw) > 1 && raw[0] == '0' {
			base = 8
		}
		n, err := strconv.ParseUint(raw, base, 32)
		if err != nil {
			return 0, fmt.Errorf("invalid integer %q", e.s[start:e.pos])
		}
		return int32(uint32(n)), nil
	}
	if isPPNameStart(rune(e.s[e.pos])) {
		name, _ := ppWord(e.s[e.pos:])
		e.pos += len(name)
		if name == "defined" {
			e.space()
			paren := e.take("(")
			e.space()
			n, rest := ppWord(e.s[e.pos:])
			if !validPPName(n) {
				return 0, fmt.Errorf("defined requires a symbol")
			}
			e.pos += len(n)
			if paren && !e.take(")") {
				return 0, fmt.Errorf("missing closing parenthesis")
			}
			_ = rest
			if _, ok := e.defs[n]; ok {
				return 1, nil
			}
			return 0, nil
		}
		return e.defs[name], nil
	}
	return 0, fmt.Errorf("unexpected character %q", e.s[e.pos])
}
func (e *ppExpr) takeSingle(x string) bool {
	e.space()
	if strings.HasPrefix(e.s[e.pos:], x) && !strings.HasPrefix(e.s[e.pos:], x+x) {
		e.pos += len(x)
		return true
	}
	return false
}
func boolInt(x bool) int32 {
	if x {
		return 1
	}
	return 0
}
