package kag

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/eslzzyl/krkrgo/internal/core"
)

func parseTag(input string) (Tag, error) {
	chars := []rune(input)
	i := 0
	skip := func() {
		for i < len(chars) && isKAGSpace(chars[i]) {
			i++
		}
	}
	skip()
	start := i
	for i < len(chars) && !isKAGSpace(chars[i]) {
		i++
	}
	if i == start {
		return Tag{}, fmt.Errorf("empty KAG tag")
	}
	name := asciiLower(string(chars[start:i]))
	attributes := make([]Attribute, 0)
	for i < len(chars) {
		skip()
		if i == len(chars) {
			break
		}
		start = i
		for i < len(chars) && !isKAGSpace(chars[i]) && chars[i] != '=' {
			i++
		}
		if i == start {
			return Tag{}, fmt.Errorf("missing attribute name")
		}
		key := asciiLower(string(chars[start:i]))
		skip()
		value := "true"
		entity := false
		if i < len(chars) && chars[i] == '=' {
			i++
			valueStart := i
			skip()
			emptyValue := i == len(chars)
			// If the next whitespace-delimited token is another assignment, this value is empty.
			if !emptyValue && i > valueStart {
				next := i
				for next < len(chars) && !isKAGSpace(chars[next]) && chars[next] != '=' {
					next++
				}
				emptyValue = next > i && next < len(chars) && chars[next] == '='
			}
			if emptyValue {
				value = ""
			}
			if !emptyValue {
				if i < len(chars) && chars[i] == '&' {
					entity = true
					i++
					if i == len(chars) {
						return Tag{}, fmt.Errorf("missing value for KAG attribute %s", key)
					}
				}
				quote := rune(0)
				if i < len(chars) && (chars[i] == '\'' || chars[i] == '"') {
					quote = chars[i]
					i++
					if i < len(chars) && chars[i] == '&' && !entity {
						entity = true
						i++
					}
				}
				var b strings.Builder
				closed := quote == 0
				for i < len(chars) {
					c := chars[i]
					if quote != 0 && c == quote {
						closed = true
						i++
						break
					}
					if quote == 0 && isKAGSpace(c) {
						break
					}
					if c == '`' {
						i++
						if i == len(chars) {
							return Tag{}, fmt.Errorf("truncated KAG escape")
						}
						b.WriteRune(chars[i])
						i++
						continue
					}
					b.WriteRune(c)
					i++
				}
				if !closed {
					return Tag{}, fmt.Errorf("unterminated KAG attribute quote")
				}
				if i < len(chars) && !isKAGSpace(chars[i]) {
					return Tag{}, fmt.Errorf("missing whitespace after KAG attribute")
				}
				value = b.String()
			}
		}
		attributes = append(attributes, Attribute{Name: key, Value: value, Entity: entity})
	}
	return Tag{Name: name, Attributes: attributes}, nil
}

func isInlineScriptMarker(line, name string) bool {
	return line == "["+name+"]" || line == "["+name+"]\\" || line == "@"+name
}

func isBracketTagCommandLine(line []rune, start int) bool {
	seenTag := false
	for {
		for start < len(line) && (line[start] == ' ' || line[start] == '\t') {
			start++
		}
		if start == len(line) {
			return seenTag
		}
		if line[start] != '[' || start+1 < len(line) && line[start+1] == '[' {
			return false
		}
		start++
		quote := rune(0)
		for start < len(line) {
			c := line[start]
			if c == '`' {
				if start+1 >= len(line) {
					return false
				}
				start += 2
				continue
			}
			if quote == c {
				quote = 0
			} else if quote == 0 && (c == '\'' || c == '"') {
				quote = c
			} else if quote == 0 && c == ']' {
				break
			}
			start++
		}
		if start == len(line) {
			return false
		}
		start++
		seenTag = true
	}
}

// Parse parses source as a KAG scenario stored under storage.
func Parse(storage, source string) (Scenario, error) {
	lines := scenarioLines(source)
	tokens := make([]Token, 0)
	labels := make(map[string]int)
	labelCounts := make(map[string]int)
	previousLabel := ""
	hasPreviousLabel := false
	for lineNo := 0; lineNo < len(lines); lineNo++ {
		original := lineNo
		text := lines[lineNo]
		if isInlineScriptMarker(text, "iscript") {
			end := original + 1
			var script strings.Builder
			for end < len(lines) && !isInlineScriptMarker(lines[end], "endscript") {
				script.WriteString(lines[end])
				script.WriteString("\r\n")
				end++
			}
			if end == len(lines) {
				return Scenario{}, fmt.Errorf("%s:%d:1: unterminated KAG inline script", storage, original+1)
			}
			location := core.SourceLocation{Storage: storage, Line: uint64(original + 1), Column: 1}
			tag := Tag{Name: "iscript"}
			tokens = append(tokens, Token{
				Location: location,
				Kind:     KindTag,
				Tag:      &tag,
				InlineScript: &InlineScript{
					Source:    script.String(),
					StartLine: uint64(original + 2),
					LineCount: uint64(end - original - 1),
				},
			})
			if len(tokens) > 1_000_000 {
				return Scenario{}, fmt.Errorf("KAG token limit exceeded")
			}
			lineNo = end
			continue
		}
		if strings.HasPrefix(strings.TrimLeftFunc(text, unicode.IsSpace), ";") || text == "" {
			continue
		}
		location := core.SourceLocation{Storage: storage, Line: uint64(original + 1), Column: 1}
		if strings.HasPrefix(text, "*") {
			label := text[1:]
			name := label
			var caption *string
			if before, after, ok := strings.Cut(label, "|"); ok {
				name = before
				caption = &after
			}
			if len(text) == 1 {
				// Native KAG ignores a lone '*' as a label because it has no
				// label name; it must not poison the next named label.
				continue
			}
			if name == "" {
				if !hasPreviousLabel {
					return Scenario{}, fmt.Errorf("cannot omit first KAG label name")
				}
				name = previousLabel
			} else {
				name = "*" + name
			}
			previousLabel = name
			hasPreviousLabel = true
			labelCounts[name]++
			alias := name
			if labelCounts[name] > 1 {
				alias += ":" + strconv.Itoa(labelCounts[name])
			}
			labels[alias] = len(tokens)
			labelValue := &Label{Name: alias, Caption: caption}
			tokens = append(tokens, Token{Location: location, Kind: KindLabel, Label: labelValue})
			continue
		}

		multiline := false
		for strings.HasSuffix(text, " \\") {
			multiline = true
			text = text[:len(text)-1]
			lineNo++
			if lineNo >= len(lines) {
				return Scenario{}, fmt.Errorf("missing multiline KAG continuation")
			}
			continuation := lines[lineNo]
			if !strings.HasPrefix(continuation, ";") {
				return Scenario{}, fmt.Errorf("KAG continuation must start with ';'")
			}
			text += continuation[1:]
		}

		commandStart := 0
		for commandStart < len(text) && (text[commandStart] == ' ' || text[commandStart] == '\t') {
			commandStart++
		}
		if commandStart < len(text) && text[commandStart] == '@' {
			tag, err := parseTag(text[commandStart+1:])
			if err != nil {
				return Scenario{}, fmt.Errorf("%s:%d: %w", storage, location.Line, err)
			}
			location.Column = uint64(commandStart + 1)
			tokens = append(tokens, Token{Location: location, Kind: KindTag, Tag: &tag})
			if len(tokens) > 1_000_000 {
				return Scenario{}, fmt.Errorf("KAG token limit exceeded")
			}
			continue
		}

		chars := []rune(text)
		bracketTagLine := commandStart < len(chars) && isBracketTagCommandLine(chars, commandStart)
		i := 0
		column := 1
		if bracketTagLine {
			i = commandStart
			column = commandStart + 1
		}
		var literal strings.Builder
		for i < len(chars) {
			c := chars[i]
			if bracketTagLine && (c == ' ' || c == '\t') {
				i++
				column++
				continue
			}
			if c == '[' && i+1 < len(chars) && chars[i+1] == '[' {
				literal.WriteRune('[')
				i += 2
				column += 2
				continue
			}
			if c != '[' {
				literal.WriteRune(c)
				i++
				column += utf16Width(c)
				continue
			}
			if literal.Len() != 0 {
				tokens = append(tokens, Token{Location: location, Kind: KindText, Text: literal.String()})
				literal.Reset()
			}
			start := i + 1
			tagColumn := column
			i++
			column++
			quote := rune(0)
			for i < len(chars) {
				c = chars[i]
				if c == '`' {
					if i+1 >= len(chars) {
						return Scenario{}, fmt.Errorf("truncated KAG tag escape")
					}
					column += utf16Width(c) + utf16Width(chars[i+1])
					i += 2
					continue
				}
				if quote == c {
					quote = 0
				} else if quote == 0 && (c == '\'' || c == '"') {
					quote = c
				} else if quote == 0 && c == ']' {
					break
				}
				i++
				column += utf16Width(c)
			}
			if i >= len(chars) {
				return Scenario{}, fmt.Errorf("%s:%d:%d: unterminated KAG tag", storage, location.Line, tagColumn)
			}
			tag, err := parseTag(string(chars[start:i]))
			if err != nil {
				return Scenario{}, fmt.Errorf("%s:%d:%d: %w", storage, location.Line, tagColumn, err)
			}
			tokens = append(tokens, Token{
				Location: core.SourceLocation{Storage: storage, Line: location.Line, Column: uint64(tagColumn)},
				Kind:     KindTag,
				Tag:      &tag,
			})
			i++
			column++
			if multiline {
				break
			}
		}
		if literal.Len() != 0 {
			tokens = append(tokens, Token{Location: location, Kind: KindText, Text: literal.String()})
		}
		if !multiline {
			tokens = append(tokens, Token{Location: location, Kind: KindNewline})
		}
		if len(tokens) > 1_000_000 {
			return Scenario{}, fmt.Errorf("KAG token limit exceeded")
		}
	}
	return Scenario{Storage: storage, Tokens: tokens, Labels: labels, SourceLines: lines}, nil
}

// ParseScenario is the descriptive alias for Parse.
func ParseScenario(storage, source string) (Scenario, error) { return Parse(storage, source) }

// Restore replaces the cursor after checking all positions against the
// scenario. A copy is taken so a caller cannot mutate parser state indirectly.
func (p *Parser) Restore(state Cursor) error {
	if state.Position < 0 || state.Position > len(p.scenario.Tokens) || len(state.Calls) > 128 {
		return fmt.Errorf("invalid KAG cursor state")
	}
	if state.CurrentLine < 0 || state.CurrentLine > len(p.scenario.SourceLines) {
		return fmt.Errorf("invalid KAG cursor state")
	}
	for _, position := range state.Calls {
		if position < 0 || position > len(p.scenario.Tokens) {
			return fmt.Errorf("invalid KAG cursor state")
		}
	}
	currentLine := state.CurrentLine
	if currentLine == 0 {
		currentLine = p.lineAtPreviousToken(state.Position)
	}
	p.cursor = Cursor{
		Position:    state.Position,
		Calls:       append([]int{}, state.Calls...),
		CurrentLine: currentLine,
	}
	return nil
}

// Jump moves to the token indexed by label. A leading '*' is optional.
// An empty label leaves the current position unchanged, matching KAG's
// GoToLabel behavior.
func (p *Parser) Jump(label string) error {
	if label == "" {
		return nil
	}
	name := strings.TrimLeft(label, "*")
	position, ok := p.scenario.Labels["*"+name]
	if !ok {
		position, ok = p.scenario.Labels[name]
	}
	if !ok {
		return fmt.Errorf("KAG label not found: %s", label)
	}
	p.cursor.Position = position
	return nil
}

// Call jumps to label and saves the current position for ReturnFromCall.
func (p *Parser) Call(label string) error {
	if len(p.cursor.Calls) >= 128 {
		return fmt.Errorf("KAG call depth exceeded")
	}
	position := p.cursor.Position
	if err := p.Jump(label); err != nil {
		return err
	}
	p.cursor.Calls = append(p.cursor.Calls, position)
	return nil
}

// ReturnFromCall resumes at the position saved by Call and restores its source line.
func (p *Parser) ReturnFromCall() error {
	if len(p.cursor.Calls) == 0 {
		return fmt.Errorf("KAG return without call")
	}
	last := len(p.cursor.Calls) - 1
	p.cursor.Position = p.cursor.Calls[last]
	p.cursor.Calls = p.cursor.Calls[:last]
	if line := p.physicalLineAtPosition(p.cursor.Position); line != 0 {
		p.cursor.CurrentLine = line
	}
	return nil
}

// RequirePlainTag rejects control tags whose execution belongs to the
// framework dispatcher rather than the simple token parser.
func RequirePlainTag(tag Tag) error {
	switch tag.Name {
	case "if", "elsif", "else", "endif", "ignore", "endignore", "iscript", "endscript":
		return fmt.Errorf("KAG control execution is not implemented: %s", tag.Name)
	default:
		return nil
	}
}

// RequirePlainTag is also available as a parser method for parity with the
// Rust associated helper.
func (p *Parser) RequirePlainTag(tag Tag) error { return RequirePlainTag(tag) }
