package runtime

import (
	"fmt"
	"sort"
	"strings"

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
