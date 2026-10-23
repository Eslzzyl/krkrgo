package runtime

import (
	"fmt"
	"sort"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/kag"
)

func indexKAGMacros(scenario kag.Scenario) (map[string][]kag.Token, map[int]int, map[string][]kag.Attribute, error) {
	macros := make(map[string][]kag.Token)
	ranges := make(map[int]int)
	pmacros := make(map[string][]kag.Attribute)
	for i := 0; i < len(scenario.Tokens); i++ {
		token := scenario.Tokens[i]
		if token.Kind != kag.KindTag || token.Tag == nil {
			continue
		}
		switch token.Tag.Name {
		case "macro":
			name, ok := token.Tag.Get("name")
			if !ok || strings.TrimSpace(name) == "" {
				return nil, nil, nil, kagLocationError(token.Location, fmt.Errorf("KAG macro missing name"))
			}
			name = strings.ToLower(name)
			if _, exists := macros[name]; exists {
				return nil, nil, nil, kagLocationError(token.Location, fmt.Errorf("duplicate KAG macro: %s", name))
			}
			start := i + 1
			depth := 1
			end := -1
			for j := start; j < len(scenario.Tokens); j++ {
				inner := scenario.Tokens[j]
				if inner.InlineScript != nil {
					return nil, nil, nil, kagLocationError(inner.Location, fmt.Errorf("inline script inside KAG macro"))
				}
				if inner.Kind != kag.KindTag || inner.Tag == nil {
					continue
				}
				switch inner.Tag.Name {
				case "macro":
					depth++
				case "endmacro":
					depth--
					if depth == 0 {
						end = j
					}
				}
				if end >= 0 {
					break
				}
			}
			if end < 0 {
				return nil, nil, nil, kagLocationError(token.Location, fmt.Errorf("KAG macro %s missing endmacro", name))
			}
			body := append([]kag.Token(nil), scenario.Tokens[start:end]...)
			macros[name] = body
			ranges[i] = end + 1
			i = end
		case "endmacro":
			return nil, nil, nil, kagLocationError(token.Location, fmt.Errorf("KAG endmacro without macro"))
		case "pmacro":
			name, ok := token.Tag.Get("name")
			if !ok || strings.TrimSpace(name) == "" {
				return nil, nil, nil, kagLocationError(token.Location, fmt.Errorf("KAG parameter macro missing name"))
			}
			name = strings.ToLower(name)
			if _, exists := pmacros[name]; exists {
				return nil, nil, nil, kagLocationError(token.Location, fmt.Errorf("duplicate KAG parameter macro: %s", name))
			}
			attrs := make([]kag.Attribute, 0, len(token.Tag.Attributes))
			for _, attr := range token.Tag.Attributes {
				if attr.Name != "name" {
					attrs = append(attrs, attr)
				}
			}
			pmacros[name] = attrs
		}
	}
	return macros, ranges, pmacros, nil
}

func macroArgumentMap(tag *kag.Tag) map[string]string {
	args := make(map[string]string, len(tag.Attributes))
	for _, attr := range tag.Attributes {
		name := strings.ToLower(attr.Name)
		if name != "tagname" {
			args[name] = attr.Value
		}
	}
	return args
}

func substituteKAGMacroValue(value string, args map[string]string) string {
	if !strings.Contains(value, "%") {
		return value
	}
	var out strings.Builder
	for i := 0; i < len(value); {
		if value[i] != '%' {
			out.WriteByte(value[i])
			i++
			continue
		}
		start := i + 1
		i = start
		for i < len(value) && (value[i] == '_' || value[i] >= 'a' && value[i] <= 'z' || value[i] >= 'A' && value[i] <= 'Z' || value[i] >= '0' && value[i] <= '9') {
			i++
		}
		if i == start {
			out.WriteByte('%')
			continue
		}
		name := strings.ToLower(value[start:i])
		replacement, exists := args[name]
		if i < len(value) && value[i] == '|' {
			i++
			fallbackStart := i
			for i < len(value) && value[i] != '%' {
				i++
			}
			if !exists {
				replacement = value[fallbackStart:i]
				exists = true
			}
		}
		if exists {
			out.WriteString(replacement)
		}
	}
	return out.String()
}

func expandKAGTag(tag kag.Tag, pmacros map[string][]kag.Attribute, args map[string]string) kag.Tag {
	attributes := make([]kag.Attribute, 0, len(tag.Attributes)+len(args))
	for _, attr := range tag.Attributes {
		if attr.Name == "*" && args != nil {
			names := make([]string, 0, len(args))
			for name := range args {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				attributes = append(attributes, kag.Attribute{Name: name, Value: args[name]})
			}
			continue
		}
		if replacements, ok := pmacros[attr.Name]; ok {
			for _, replacement := range replacements {
				attributes = append(attributes, kag.Attribute{
					Name: replacement.Name, Value: substituteKAGMacroValue(replacement.Value, args), Entity: replacement.Entity,
				})
			}
			continue
		}
		attributes = append(attributes, kag.Attribute{
			Name: attr.Name, Value: substituteKAGMacroValue(attr.Value, args), Entity: attr.Entity,
		})
	}
	return kag.Tag{Name: tag.Name, Attributes: attributes}
}

func expandKAGToken(token kag.Token, pmacros map[string][]kag.Attribute, args map[string]string) kag.Token {
	if token.Kind == kag.KindTag && token.Tag != nil {
		tag := expandKAGTag(*token.Tag, pmacros, args)
		token.Tag = &tag
	} else if token.Kind == kag.KindText {
		token.Text = substituteKAGMacroValue(token.Text, args)
	}
	return token
}

func (p *KAGParser) nextKAGToken() *kag.Token {
	if len(p.injectedTokens) != 0 {
		token := &p.injectedTokens[0]
		p.injectedTokens = p.injectedTokens[1:]
		return token
	}
	for len(p.macroFrames) != 0 {
		last := len(p.macroFrames) - 1
		frame := &p.macroFrames[last]
		if frame.position >= len(frame.tokens) {
			p.macroFrames = p.macroFrames[:last]
			continue
		}
		token := expandKAGToken(frame.tokens[frame.position], p.paramMacroDefs, frame.arguments)
		frame.position++
		return &token
	}
	token := p.parser.NextToken()
	if token == nil {
		return nil
	}
	expanded := expandKAGToken(*token, p.paramMacroDefs, nil)
	return &expanded
}
func (p *KAGParser) nextKAGTokenKind() (kag.Kind, bool) {
	if len(p.injectedTokens) != 0 {
		return p.injectedTokens[0].Kind, true
	}
	for i := len(p.macroFrames) - 1; i >= 0; i-- {
		frame := &p.macroFrames[i]
		if frame.position < len(frame.tokens) {
			return frame.tokens[frame.position].Kind, true
		}
	}
	if p.parser == nil {
		return 0, false
	}
	scenario := p.parser.Scenario()
	position := p.parser.State().Position
	if position < 0 || position >= len(scenario.Tokens) {
		return 0, false
	}
	return scenario.Tokens[position].Kind, true
}

func (p *KAGParser) pushKAGMacro(tag *kag.Tag) error {
	name := strings.ToLower(tag.Name)
	body, ok := p.macroDefs[name]
	if !ok {
		return nil
	}
	if len(p.macroFrames) >= 128 {
		return fmt.Errorf("KAG macro depth exceeded")
	}
	p.macroFrames = append(p.macroFrames, kagMacroFrame{name: name, tokens: body, arguments: macroArgumentMap(tag)})
	return nil
}

// kagMacroDefinitionEnd returns the index of the endmacro token that closes the
// macro definition beginning at start. Nested definitions increment the depth,
// so a definition inside a recording body keeps its own terminator.
func kagMacroDefinitionEnd(tokens []kag.Token, start int) (int, bool) {
	if start < 0 || start >= len(tokens) {
		return 0, false
	}
	opening := tokens[start]
	if opening.Kind != kag.KindTag || opening.Tag == nil || opening.Tag.Name != "macro" {
		return 0, false
	}
	depth := 1
	for i := start + 1; i < len(tokens); i++ {
		token := tokens[i]
		if token.Kind != kag.KindTag || token.Tag == nil {
			continue
		}
		switch token.Tag.Name {
		case "macro":
			depth++
		case "endmacro":
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

// defineKAGMacro registers a macro body. The reference parser stores macros in
// a dictionary with TJS_MEMBERENSURE, so a later definition replaces an
// earlier one without a duplicate-name error.
func (p *KAGParser) defineKAGMacro(name string, body []kag.Token, location core.SourceLocation) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return kagLocationError(location, fmt.Errorf("KAG macro missing name"))
	}
	if p.macroDefs == nil {
		p.macroDefs = make(map[string][]kag.Token)
	}
	p.macroDefs[name] = body
	return nil
}

// kagMacroBodyText renders a tokenized macro body as canonical KAG text for
// the native macros projection. Raw source spelling is not retained by the
// tokenizer, and the reference [macropop] sentinel is omitted because this
// executor pops macro frames structurally when the body is exhausted.
func kagMacroBodyText(tokens []kag.Token) string {
	var text strings.Builder
	for i := range tokens {
		token := tokens[i]
		if token.InlineScript != nil {
			text.WriteString("[iscript]\n")
			text.WriteString(token.InlineScript.Source)
			text.WriteString("[endscript]")
			continue
		}
		switch token.Kind {
		case kag.KindText:
			text.WriteString(strings.ReplaceAll(token.Text, "[", "[["))
		case kag.KindTag:
			if token.Tag != nil {
				text.WriteString(kagTagText(token.Tag))
			}
		case kag.KindLabel:
			if token.Label != nil {
				text.WriteByte('*')
				text.WriteString(token.Label.Name)
				if token.Label.Caption != nil {
					text.WriteByte('|')
					text.WriteString(*token.Label.Caption)
				}
			}
		case kag.KindNewline:
			text.WriteByte('\n')
		}
	}
	return text.String()
}

// kagTagText renders one tag in canonical KAG bracket form, preserving entity
// and macro-argument spellings that the projection must expose.
func kagTagText(tag *kag.Tag) string {
	var text strings.Builder
	text.WriteByte('[')
	text.WriteString(tag.Name)
	for _, attribute := range tag.Attributes {
		text.WriteByte(' ')
		text.WriteString(attribute.Name)
		text.WriteByte('=')
		if attribute.Entity {
			text.WriteByte('&')
		}
		text.WriteString(kagAttributeText(attribute.Value))
	}
	text.WriteByte(']')
	return text.String()
}

func kagAttributeText(value string) string {
	if value == "" {
		return ""
	}
	if !strings.ContainsAny(value, " \t\r\n\"'`") {
		return value
	}
	var text strings.Builder
	text.WriteByte('"')
	for _, r := range value {
		if r == '"' || r == '`' {
			text.WriteByte('`')
		}
		text.WriteRune(r)
	}
	text.WriteByte('"')
	return text.String()
}
